package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/util"
	"phenix/web/weberror"
)

// GetExperimentCaptures - GET /experiments/{name}/captures.
func GetExperimentCaptures(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/captures", "list", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing experiment captures not allowed",
			"user",
			user,
			"exp",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	var (
		captures []mm.Capture
		allowed  []mm.Capture
	)

	// A stopped experiment has no captures, and asking minimega would
	// recreate its namespace (see experiment.Running).
	if experiment.Running(name) {
		captures = mm.GetExperimentCaptures(mm.NS(name), mm.Context(ctx))
	}

	if requestGone(r) {
		return nil
	}

	for _, capture := range captures {
		if role.Allowed("experiments/captures", "list", name+"/"+capture.VM) {
			allowed = append(allowed, capture)
		}
	}

	body, err := marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(allowed)})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling captures for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// GetVMCaptures - GET /experiments/{exp}/vms/{name}/captures.
func GetVMCaptures(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "list", fmt.Sprintf("%s/%s", exp, name)) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting captures for VM not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	captures := mm.GetVMCaptures(mm.NS(exp), mm.VMName(name), mm.Context(ctx))
	if requestGone(r) {
		return nil
	}

	body, err := marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(captures)})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// StartVMCapture - POST /experiments/{exp}/vms/{name}/captures.
func StartVMCapture(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)) {
		audit.Denied(r, audit.CaptureStart, "starting capture for VM not allowed", audit.Fields{Experiment: exp, VM: name})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.StartCaptureRequest

	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	iface := int(req.GetInterface())
	captured := audit.Fields{Experiment: exp, VM: name, Interface: &iface, File: req.GetFilename()}

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	if err := vm.StartCapture(change, exp, name, iface, req.GetFilename()); err != nil {
		audit.Failure(r, audit.CaptureStart, "starting capture for VM failed", captured, err)

		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "starting capture for VM", "exp", exp, "vm", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	// the capture writes to the experiment's files directory
	experiment.InvalidateFiles(exp)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "start"),
		body,
	)

	audit.Success(r, audit.CaptureStart, "vm capture started", captured)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// StopVMCaptures - DELETE /experiments/{exp}/vms/{name}/captures[?iface={iface}].
//
// If the iface query parameter is provided, only the packet capture running
// on that interface is stopped, leaving any other running captures for the
// VM untouched. Otherwise, all packet captures for the VM are stopped.
func StopVMCaptures(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		ifaceStr = r.URL.Query().Get("iface")
	)

	if !role.Allowed("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)) {
		audit.Denied(r, audit.CaptureStop, "stopping captures for VM not allowed", audit.Fields{Experiment: exp, VM: name})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	var (
		err     error
		body    json.RawMessage
		stopped = audit.Fields{Experiment: exp, VM: name}
	)

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	if ifaceStr == "" {
		err = vm.StopCaptures(change, exp, name)
	} else {
		var iface int

		iface, err = strconv.Atoi(ifaceStr)
		if err != nil {
			return weberror.New(http.StatusBadRequest, "iface query parameter must be an integer")
		}

		stopped.Interface = &iface

		if err = vm.StopCapture(change, exp, name, iface); err == nil {
			body, err = json.Marshal(map[string]int{"interface": iface})
			if err != nil {
				plog.Error(plog.TypeSystem, "marshaling capture stop result", "err", err)

				return weberror.New(http.StatusInternalServerError, err.Error())
			}
		}
	}

	if err != nil {
		audit.Failure(r, audit.CaptureStop, "stopping captures for VM failed", stopped, err)

		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(
			plog.TypeSystem,
			"stopping captures for VM",
			"exp",
			exp,
			"name",
			name,
			"iface",
			ifaceStr,
			"err",
			err,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	// the stopped captures' files are now complete
	experiment.InvalidateFiles(exp)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "stop"),
		body,
	)

	audit.Success(r, audit.CaptureStop, "vm capture stopped", stopped)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// StartCaptureSubnet - POST /experiments/{exp}/captureSubnet.
func StartCaptureSubnet(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
	)

	if !role.Allowed("exp/captureSubnet", "create", exp) {
		audit.Denied(r, audit.CaptureStart, "starting subnet capture for experiment not allowed", audit.Fields{Experiment: exp})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.CaptureSubnetRequest

	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	subnet := []any{"phenix.capture.subnet", req.GetSubnet()}

	change, cancel := changeContext(r, vmsChangeTimeout)
	defer cancel()

	vmCaptures, err := vm.CaptureSubnet(change, exp, req.GetSubnet(), req.GetVms())
	if err != nil {
		audit.Failure(r, audit.CaptureStart, "starting subnet capture failed", audit.Fields{Experiment: exp, Extra: subnet}, err)

		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "unable to start subnet capture", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err = marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(vmCaptures)})
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to marshal vm capture list", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	for _, c := range vmCaptures {
		audit.Success(r, audit.CaptureStart, "subnet capture started", audit.Fields{
			Experiment: exp, VM: c.VM, Interface: &c.Interface, File: c.Filepath, Extra: subnet,
		})
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// StopCaptureSubnet - POST /experiments/{exp}/stopCaptureSubnet.
func StopCaptureSubnet(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
	)

	if !role.Allowed("exp/captureSubnet", "delete", exp) {
		audit.Denied(r, audit.CaptureStop, "stopping subnet capture for experiment not allowed", audit.Fields{Experiment: exp})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.CaptureSubnetRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	subnet := []any{"phenix.capture.subnet", req.GetSubnet()}

	change, cancel := changeContext(r, vmsChangeTimeout)
	defer cancel()

	vms, err := vm.StopCaptureSubnet(change, exp, req.GetSubnet(), req.GetVms())
	if err != nil {
		audit.Failure(r, audit.CaptureStop, "stopping subnet capture failed", audit.Fields{Experiment: exp, Extra: subnet}, err)

		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "unable to stop subnet capture", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err = marshaler.Marshal(&proto.VMNameList{Vms: vms})
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to marshal vm capture list", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	for _, name := range vms {
		audit.Success(r, audit.CaptureStop, "subnet capture stopped", audit.Fields{Experiment: exp, VM: name, Extra: subnet})
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
