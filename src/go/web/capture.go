package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
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
//
// Lists the VM interface captures of the experiment's VMs that the role may list, and its experiment captures, whether
// minimega supports them, and the Open vSwitch mirror budget of each host and bridge that the experiment uses.
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
		captures = vm.ExperimentCaptures(ctx, name)
		allowed  []mm.Capture
		running  = experiment.Running(name)
	)

	if requestGone(r) {
		return nil
	}

	for _, capture := range captures {
		if role.Allowed("experiments/captures", "list", name+"/"+capture.VM) {
			allowed = append(allowed, capture)
		}
	}

	list, err := experimentCaptureList(r, name, running)
	if err != nil {
		return answerExperimentCaptureError(r, name, "", err)
	}

	list.Captures = util.CapturesToProtobuf(allowed)

	body, err := marshaler.Marshal(list)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling captures for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// vmCaptureList is a capture list of VM interface captures alone.
func vmCaptureList(captures []mm.Capture) *proto.CaptureList {
	return &proto.CaptureList{
		Captures:                util.CapturesToProtobuf(captures),
		ExperimentCaptures:      []*proto.ExperimentCapture{},
		Support:                 nil,
		Budget:                  []*proto.MirrorBudget{},
		BudgetError:             "",
		ExperimentCapturesError: "",
	}
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

	captures := vm.Captures(ctx, exp, name)
	if requestGone(r) {
		return nil
	}

	body, err := marshaler.Marshal(vmCaptureList(captures))
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

	if err := checkCaptureInterface(&req); err != nil {
		return err
	}

	iface := int(req.GetInterface())
	captured := audit.Fields{Experiment: exp, VM: name, Interface: &iface, File: req.GetFilename()}
	opts := vm.CaptureOptions{Format: req.GetFormat(), MaxSizeMiB: o.captureMaxSize}

	if req.InterfaceName != nil {
		captured.Interface = nil
		captured.Extra = []any{"phenix.capture.interface_name", req.GetInterfaceName()}
	}

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	err = startVMCapture(change, exp, name, req.InterfaceName, &iface, req.GetFilename(), opts)
	if err != nil {
		audit.Failure(r, audit.CaptureStart, "starting capture for VM failed", captured, err)

		if requestCanceled(r, err) {
			return nil
		}

		switch {
		case errors.Is(err, vm.ErrNoCaptureFile):
			return weberror.New(http.StatusBadRequest, vm.ErrNoCaptureFile.Error())
		case errors.Is(err, vm.ErrInvalidInterface):
			return weberror.New(http.StatusBadRequest, vm.ErrInvalidInterface.Error())
		case errors.Is(err, vm.ErrUnknownInterface):
			return weberror.New(http.StatusBadRequest, err.Error())
		case errors.Is(err, vm.ErrDisconnectedInterface):
			return weberror.New(http.StatusBadRequest, vm.ErrDisconnectedInterface.Error())
		case errors.Is(err, vm.ErrInvalidFormat):
			return weberror.New(http.StatusBadRequest, vm.ErrInvalidFormat.Error())
		}

		if werr := vmStateError(exp, name, err); werr != nil {
			return werr
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

	captured.Interface = &iface
	audit.Success(r, audit.CaptureStart, "vm capture started", captured)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// checkCaptureInterface refuses a capture start request that names both, or
// neither, of an interface index and an interface name.
func checkCaptureInterface(req *proto.StartCaptureRequest) error {
	if req.Interface != nil && req.InterfaceName != nil {
		return weberror.New(http.StatusBadRequest, "give interface or interfaceName, not both")
	}

	if req.Interface == nil && req.InterfaceName == nil {
		return weberror.New(http.StatusBadRequest, "give interface or interfaceName")
	}

	return nil
}

// startVMCapture starts the VM's capture into out on the interface that named
// names, as vm.ResolveInterface finds it, or else on the index iface. It sets
// iface to the index of the interface it captures.
func startVMCapture(ctx context.Context, exp, name string, named *string, iface *int, out string, opts vm.CaptureOptions) error {
	if named == nil {
		return vm.StartCapture(ctx, exp, name, *iface, out, opts) //nolint:wrapcheck // the handler words it
	}

	if out == "" {
		return vm.ErrNoCaptureFile
	}

	v, idx, err := resolveVMInterface(ctx, exp, name, *named)
	if err != nil {
		return err
	}

	*iface = idx

	return vm.StartCaptureForVM(ctx, v, exp, name, idx, out, opts) //nolint:wrapcheck // the handler words it
}

// resolveVMInterface returns the VM and the index of its interface that named
// names, as vm.ResolveInterface finds it.
func resolveVMInterface(ctx context.Context, exp, name, named string) (*mm.VM, int, error) {
	v, err := vm.Get(ctx, exp, name)
	if err != nil {
		return nil, 0, fmt.Errorf("getting VM details: %w", err)
	}

	idx, err := vm.ResolveInterface(v, named)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // the handler words it
	}

	return v, idx, nil
}

// noCaptures reports whether err is about a capture that does not run.
func noCaptures(err error) bool {
	return errors.Is(err, vm.ErrNoCaptures) || errors.Is(err, mm.ErrNoCaptures)
}

// StopVMCaptures - DELETE /experiments/{exp}/vms/{name}/captures[?iface={iface}|?ifaceName={name}].
//
// If the iface or the ifaceName query parameter is provided, only the packet
// capture running on that interface is stopped, leaving any other running
// captures for the VM untouched. Otherwise, all packet captures for the VM
// are stopped.
//
//nolint:funlen // the handler's steps in order
func StopVMCaptures(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx       = r.Context()
		role      = middleware.RoleFromContext(ctx)
		vars      = mux.Vars(r)
		exp       = vars["exp"]
		name      = vars["name"]
		query     = r.URL.Query()
		ifaceStr  = query.Get("iface")
		ifaceName = query.Get("ifaceName")
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

	if query.Has("iface") && query.Has("ifaceName") {
		return weberror.New(http.StatusBadRequest, "give the iface or the ifaceName query parameter, not both")
	}

	if query.Has("ifaceName") && ifaceName == "" {
		return weberror.New(http.StatusBadRequest, "the ifaceName query parameter must name an interface")
	}

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	if ifaceName != "" {
		ifaceStr = ifaceName
	}

	if ifaceStr == "" {
		err = vm.StopCaptures(change, exp, name)
	} else {
		var iface int

		if ifaceName != "" {
			stopped.Extra = []any{"phenix.capture.interface_name", ifaceName}
			_, iface, err = resolveVMInterface(change, exp, name, ifaceName)
		} else {
			iface, err = strconv.Atoi(ifaceStr)
			if err != nil {
				return weberror.New(http.StatusBadRequest, "iface query parameter must be an integer")
			}
		}

		if err == nil {
			stopped.Interface = &iface
			err = vm.StopCapture(change, exp, name, iface)
		}

		if err == nil {
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

		switch {
		case errors.Is(err, vm.ErrInvalidInterface):
			return weberror.New(http.StatusBadRequest, vm.ErrInvalidInterface.Error())
		case errors.Is(err, vm.ErrUnknownInterface):
			return weberror.New(http.StatusBadRequest, err.Error())
		case noCaptures(err) && ifaceStr != "":
			return weberror.New(http.StatusNotFound, fmt.Sprintf("no capture runs on interface %s of VM %s", ifaceStr, name))
		case noCaptures(err):
			return weberror.New(http.StatusNotFound, fmt.Sprintf("no captures run on VM %s", name))
		}

		if werr := vmStateError(exp, name, err); werr != nil && ifaceName != "" {
			return werr
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

	vms, err := filteredVMs(change, exp, req.GetVms(), req.GetFilter())
	if err != nil {
		return err
	}

	vmCaptures, err := vm.CaptureSubnet(change, exp, req.GetSubnet(), vms, vm.CaptureOptions{
		Format: "", MaxSizeMiB: o.captureMaxSize,
	})
	if err != nil {
		audit.Failure(r, audit.CaptureStart, "starting subnet capture failed", audit.Fields{Experiment: exp, Extra: subnet}, err)

		if requestCanceled(r, err) {
			return nil
		}

		if errors.Is(err, vm.ErrInvalidSubnet) {
			return weberror.New(http.StatusBadRequest, err.Error())
		}

		plog.Error(plog.TypeSystem, "unable to start subnet capture", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err = marshaler.Marshal(vmCaptureList(vmCaptures))
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

	named, err := filteredVMs(change, exp, req.GetVms(), req.GetFilter())
	if err != nil {
		return err
	}

	vms, err := vm.StopCaptureSubnet(change, exp, req.GetSubnet(), named)
	if err != nil {
		audit.Failure(r, audit.CaptureStop, "stopping subnet capture failed", audit.Fields{Experiment: exp, Extra: subnet}, err)

		if requestCanceled(r, err) {
			return nil
		}

		if errors.Is(err, vm.ErrInvalidSubnet) {
			return weberror.New(http.StatusBadRequest, err.Error())
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

// filteredVMs returns the VMs of a subnet or VLAN capture request: names, or
// the VMs of the experiment exp that filter matches (see vm.FilterVMs). It is
// empty, for every VM, when the request gives neither. Its error is a
// *weberror.WebError: 400 for a request that gives both, or a filter that
// matches no VM.
func filteredVMs(ctx context.Context, exp string, names []string, filter string) ([]string, error) {
	if filter == "" {
		return names, nil
	}

	if len(names) > 0 {
		return nil, weberror.New(http.StatusBadRequest, "give vms or filter, not both")
	}

	matched, err := vm.FilterVMs(ctx, exp, filter)
	if err == nil {
		return matched, nil
	}

	if errors.Is(err, vm.ErrNoFilterMatch) {
		return nil, weberror.New(http.StatusBadRequest, err.Error())
	}

	if werr := vmStateError(exp, "", err); werr != nil {
		return nil, werr
	}

	plog.Error(plog.TypeSystem, "filtering the VMs of a capture", "exp", exp, "err", err)

	return nil, weberror.New(http.StatusInternalServerError, err.Error())
}

// filteredPermittedVMs returns the VMs of the experiment exp that the filter
// of the VLAN capture request selects, of those in permitted, which an empty
// VM list selects. Its error is as filteredVMs gives it, or 403 when the role
// may act on none of them.
func filteredPermittedVMs(r *http.Request, exp string, req *proto.CaptureVlanRequest, permitted []string) ([]string, error) {
	matched, err := filteredVMs(r.Context(), exp, req.GetVms(), req.GetFilter())
	if err != nil {
		return nil, err
	}

	var targets []string

	for _, name := range matched {
		if slices.Contains(permitted, name) {
			targets = append(targets, name)
		}
	}

	if len(targets) == 0 {
		return nil, weberror.New(http.StatusForbidden, "forbidden")
	}

	return targets, nil
}

// vlanCaptureTargets reads the VLAN capture request of r for the experiment exp. It returns the request and the VMs
// to act on: those the request names (all the experiment's VMs when it names none) whose captures the role may
// change with verb (create to start, delete to stop). Its error is a *weberror.WebError: 403 when the role may change
// the captures of none of them, 400 for a request without a VLAN, 404 for an experiment that does not exist. A missing
// experiment is 403, not 404, for a role that may not change the captures of all its VMs ("<exp>/*").
func vlanCaptureTargets(r *http.Request, exp, verb string, action audit.Action) (*proto.CaptureVlanRequest, []string, error) {
	role := middleware.RoleFromContext(r.Context())

	denied := func() error {
		audit.Denied(r, action, "changing VLAN captures for experiment not allowed", audit.Fields{Experiment: exp})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if !role.Allowed("vms/captures", verb) {
		return nil, nil, denied()
	}

	stored, err := experiment.Get(exp)
	if err != nil {
		if !role.Allowed("vms/captures", verb, exp+"/*") {
			return nil, nil, denied()
		}

		return nil, nil, experimentError(exp, err)
	}

	allowed := func(name string) bool { return role.Allowed("vms/captures", verb, exp+"/"+name) }

	var permitted []string

	if topology := stored.Spec.Topology(); topology != nil {
		for _, node := range topology.Nodes() {
			name := node.General().Hostname()
			if allowed(name) && !slices.Contains(permitted, name) {
				permitted = append(permitted, name)
			}
		}
	}

	if len(permitted) == 0 {
		return nil, nil, denied()
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return nil, nil, weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.CaptureVlanRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return nil, nil, weberror.New(http.StatusBadRequest, err.Error())
	}

	if req.GetVlan() == "" {
		return nil, nil, weberror.New(http.StatusBadRequest, vm.ErrNoVLAN.Error())
	}

	if req.GetFilter() != "" {
		targets, err := filteredPermittedVMs(r, exp, &req, permitted)

		return &req, targets, err
	}

	if len(req.GetVms()) == 0 {
		return &req, permitted, nil
	}

	var targets []string

	for _, name := range req.GetVms() {
		if allowed(name) {
			targets = append(targets, name)

			continue
		}

		audit.Denied(r, action, "changing VLAN captures for VM not allowed", audit.Fields{
			Experiment: exp, VM: name, Extra: []any{"phenix.capture.vlan", req.GetVlan()},
		})
	}

	if len(targets) == 0 {
		return nil, nil, weberror.New(http.StatusForbidden, "forbidden")
	}

	return &req, targets, nil
}

// StartCaptureVLAN - POST /experiments/{exp}/captureVlan.
func StartCaptureVLAN(w http.ResponseWriter, r *http.Request) error {
	exp := mux.Vars(r)["exp"]

	req, targets, err := vlanCaptureTargets(r, exp, "create", audit.CaptureStart)
	if err != nil {
		return err
	}

	vlan := []any{"phenix.capture.vlan", req.GetVlan()}

	change, cancel := changeContext(r, vmsChangeTimeout)
	defer cancel()

	captures, err := vm.CaptureVLAN(change, exp, req.GetVlan(), targets, vm.CaptureOptions{
		Format: req.GetFormat(), MaxSizeMiB: o.captureMaxSize,
	})
	if err != nil {
		audit.Failure(r, audit.CaptureStart, "starting VLAN capture failed", audit.Fields{Experiment: exp, Extra: vlan}, err)

		if requestCanceled(r, err) {
			return nil
		}

		if errors.Is(err, vm.ErrInvalidFormat) {
			return weberror.New(http.StatusBadRequest, vm.ErrInvalidFormat.Error())
		}

		plog.Error(plog.TypeSystem, "unable to start VLAN capture", "exp", exp, "vlan", req.GetVlan(), "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err := marshaler.Marshal(vmCaptureList(captures))
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to marshal vm capture list", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	for _, c := range captures {
		started, _ := json.Marshal(map[string]any{"interface": c.Interface, "filename": filepath.Base(c.Filepath)})

		broker.Broadcast(
			bt.NewRequestPolicy("vms/captures", "create", exp+"/"+c.VM),
			bt.NewResource("experiment/vm/capture", exp+"/"+c.VM, "start"),
			started,
		)

		audit.Success(r, audit.CaptureStart, "VLAN capture started", audit.Fields{
			Experiment: exp, VM: c.VM, Interface: &c.Interface, File: c.Filepath, Extra: vlan,
		})
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// StopCaptureVLAN - POST /experiments/{exp}/stopCaptureVlan.
func StopCaptureVLAN(w http.ResponseWriter, r *http.Request) error {
	exp := mux.Vars(r)["exp"]

	req, targets, err := vlanCaptureTargets(r, exp, "delete", audit.CaptureStop)
	if err != nil {
		return err
	}

	vlan := []any{"phenix.capture.vlan", req.GetVlan()}

	change, cancel := changeContext(r, vmsChangeTimeout)
	defer cancel()

	captures, err := vm.StopCaptureVLAN(change, exp, req.GetVlan(), targets)
	if err != nil {
		audit.Failure(r, audit.CaptureStop, "stopping VLAN capture failed", audit.Fields{Experiment: exp, Extra: vlan}, err)

		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "unable to stop VLAN capture", "exp", exp, "vlan", req.GetVlan(), "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	vms := []string{}

	for _, c := range captures {
		if !slices.Contains(vms, c.VM) {
			vms = append(vms, c.VM)
		}

		stopped, _ := json.Marshal(map[string]int{"interface": c.Interface})

		broker.Broadcast(
			bt.NewRequestPolicy("vms/captures", "delete", exp+"/"+c.VM),
			bt.NewResource("experiment/vm/capture", exp+"/"+c.VM, "stop"),
			stopped,
		)

		audit.Success(r, audit.CaptureStop, "VLAN capture stopped", audit.Fields{
			Experiment: exp, VM: c.VM, Interface: &c.Interface, Extra: vlan,
		})
	}

	body, err := marshaler.Marshal(&proto.VMNameList{Vms: vms})
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to marshal vm capture list", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
