package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
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
		captures = mm.GetExperimentCaptures(mm.NS(name))
		allowed  []mm.Capture
	)

	for _, capture := range captures {
		if role.Allowed("experiments/captures", "list", capture.VM) {
			allowed = append(allowed, capture)
		}
	}

	body, err := marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(allowed)})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling captures for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	//nolint:gosec // XSS via taint analysis
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

	captures := mm.GetVMCaptures(mm.NS(exp), mm.VMName(name))

	body, err := marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(captures)})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"starting capture for VM not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			name,
		)

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

	if err := vm.StartCapture(exp, name, int(req.GetInterface()), req.GetFilename()); err != nil {
		plog.Error(plog.TypeSystem, "starting capture for VM", "exp", exp, "vm", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "start"),
		body,
	)

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"vm capture started",
		"user",
		user,
		"exp",
		exp,
		"vm",
		name,
	)
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"stopping captures for VM not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	var (
		err  error
		body json.RawMessage
	)

	if ifaceStr == "" {
		err = vm.StopCaptures(exp, name)
	} else {
		var iface int

		iface, err = strconv.Atoi(ifaceStr)
		if err != nil {
			return weberror.New(http.StatusBadRequest, "iface query parameter must be an integer")
		}

		if err = vm.StopCapture(exp, name, iface); err == nil {
			body, err = json.Marshal(map[string]int{"interface": iface})
			if err != nil {
				plog.Error(plog.TypeSystem, "marshaling capture stop result", "err", err)

				return weberror.New(http.StatusInternalServerError, err.Error())
			}
		}
	}

	if err != nil {
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

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "stop"),
		body,
	)

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"vm capture stopped",
		"user",
		user,
		"exp",
		exp,
		"vm",
		name,
		"iface",
		ifaceStr,
	)
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"starting subnet capture for experiment not allowed",
			"user",
			user,
			"exp",
			exp,
		)

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

	vmCaptures, err := vm.CaptureSubnet(exp, req.GetSubnet(), req.GetVms())
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to start subnet capture", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err = marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(vmCaptures)})
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to marshal vm capture list", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"subnet capture started",
		"user",
		user,
		"exp",
		exp,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

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

	if !role.Allowed("exp/captureSubnet", "create", exp) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"stopping subnet capture for experiment not allowed",
			"user",
			user,
			"exp",
			exp,
		)

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

	vms, err := vm.StopCaptureSubnet(exp, req.GetSubnet(), req.GetVms())
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to stop subnet capture", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err = marshaler.Marshal(&proto.VMNameList{Vms: vms})
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to marshal vm capture list", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"subnet capture stopped",
		"user",
		user,
		"exp",
		exp,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}
