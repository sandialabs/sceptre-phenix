package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"phenix/api/vm"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/util"
	"phenix/web/weberror"
)

// actionUpdate is the "update" verb of RBAC checks and of websocket events that tell of a change.
const actionUpdate = "update"

// maxQoSBody is the largest link quality request body read, in bytes.
const maxQoSBody = 4 << 10

// vmQoSResponse is the link quality of each interface of a VM.
type vmQoSResponse struct {
	Interfaces []mm.InterfaceQoS `json:"interfaces"`
}

// vmQoSRequest is the link quality to apply to one VM interface. A setting that is absent or empty is not set.
type vmQoSRequest struct {
	Loss  string `json:"loss"`
	Delay string `json:"delay"`
	Rate  string `json:"rate"`
}

// qosFields name the VM and, when index is 0 or more, the interface of a link quality change for its audit event.
func qosFields(expName, name string, index int) audit.Fields {
	f := audit.Fields{Experiment: expName, VM: name, Extra: []any{"phenix.vm.changed", []string{"qos"}}}

	if index >= 0 {
		f.Interface = &index
	}

	return f
}

// GetVMQoS - GET /experiments/{exp}/vms/{name}/qos.
func GetVMQoS(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms", "patch", expName+"/"+name) {
		plog.Warn(plog.TypeSecurity, "getting vm link quality not allowed",
			"user", middleware.UserFromContext(ctx), "exp", expName, "vm", name)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if _, err := vmTarget(expName, name, true); err != nil {
		return err
	}

	_, ifaces, err := vm.GetQoS(ctx, expName, name)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		return vmQoSError(expName, name, err)
	}

	return writeJSONBody(w, vmQoSResponse{Interfaces: ifaces})
}

// SetVMInterfaceQoS - PUT /experiments/{exp}/vms/{name}/interfaces/{iface}/qos.
func SetVMInterfaceQoS(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	index, indexErr := qosIndex(vars["iface"])
	fields := qosFields(expName, name, index)

	if !role.Allowed("vms", "patch", expName+"/"+name) {
		audit.Denied(r, audit.VMUpdate, "changing vm link quality not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if indexErr != nil {
		return indexErr
	}

	var req vmQoSRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQoSBody)).Decode(&req); err != nil {
		return weberror.New(http.StatusBadRequest, "unable to parse the link quality: "+err.Error())
	}

	qos := mm.InterfaceQoS{Interface: index, Loss: req.Loss, Delay: req.Delay, Rate: req.Rate}
	if err := mm.ValidateQoS(qos); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if _, err := vmTarget(expName, name, true); err != nil {
		return err
	}

	exp, qos, err := vm.SetQoS(ctx, expName, name, qos)
	if err != nil {
		if requestCanceled(r, err) {
			return nil
		}

		if !errors.Is(err, vm.ErrNoInterface) {
			audit.Failure(r, audit.VMUpdate, "changing vm link quality failed", fields, err)
		}

		return vmQoSError(expName, name, err)
	}

	audit.Success(r, audit.VMUpdate, "vm link quality changed", fields)
	broadcastVMUpdate(exp, name)

	return writeJSONBody(w, qos)
}

// ClearVMInterfaceQoS - DELETE /experiments/{exp}/vms/{name}/interfaces/{iface}/qos.
func ClearVMInterfaceQoS(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	index, indexErr := qosIndex(vars["iface"])
	fields := qosFields(expName, name, index)

	if !role.Allowed("vms", "patch", expName+"/"+name) {
		audit.Denied(r, audit.VMUpdate, "clearing vm link quality not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if indexErr != nil {
		return indexErr
	}

	if _, err := vmTarget(expName, name, true); err != nil {
		return err
	}

	exp, err := vm.ClearQoS(ctx, expName, name, index)
	if err != nil {
		if requestCanceled(r, err) {
			return nil
		}

		if !errors.Is(err, vm.ErrNoInterface) {
			audit.Failure(r, audit.VMUpdate, "clearing vm link quality failed", fields, err)
		}

		return vmQoSError(expName, name, err)
	}

	audit.Success(r, audit.VMUpdate, "vm link quality cleared", fields)
	broadcastVMUpdate(exp, name)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// qosIndex parses the interface index of a link quality route: -1 and a 400
// error unless it is a whole number of 0 or more.
func qosIndex(value string) (int, error) {
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 {
		return -1, weberror.New(http.StatusBadRequest, fmt.Sprintf("interface index %q is not a whole number of 0 or more", value))
	}

	return index, nil
}

// vmQoSError is the error that a link quality route answers for err: 404 for a
// VM that minimega does not have, 400 with minimega's message for a setting
// minimega refuses or for an interface index past the VM's last interface,
// else 500.
func vmQoSError(expName, name string, err error) error {
	if errors.Is(err, mm.ErrVMNotFound) {
		return vmNotFound(expName, name)
	}

	if errors.Is(err, mm.ErrInvalidQoS) || errors.Is(err, vm.ErrNoInterface) {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	plog.Error(plog.TypeSystem, "vm link quality", "exp", expName, "vm", name, "err", err)

	return weberror.NewWebError(err, "unable to change the link quality of VM %s", name).
		SetStatus(http.StatusInternalServerError)
}

// broadcastVMUpdate tells the clients that may patch the VM name of exp that it changed, with the VM as it is now.
func broadcastVMUpdate(exp *types.Experiment, name string) {
	expName := exp.Metadata.Name

	v, err := vm.GetFor(context.Background(), exp, name)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading changed VM", "exp", expName, "vm", name, "err", err)

		return
	}

	pb := util.VMToProtobuf(expName, *v, exp.Spec.Topology())
	util.SetVMDefaults(expName, exp.Metadata.Annotations, pb)

	body, err := marshaler.Marshal(pb)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling changed VM", "exp", expName, "vm", name, "err", err)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms", "patch", expName+"/"+name),
		bt.NewResource("experiment/vm", expName+"/"+name, actionUpdate),
		body,
	)
}

// writeJSONBody answers with the JSON of payload.
func writeJSONBody(w http.ResponseWriter, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
