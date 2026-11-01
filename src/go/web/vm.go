package web

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/vm"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/util"
	"phenix/web/weberror"
)

// GetVMs - GET /experiments/{exp}/vms.
func GetVMs(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		query   = r.URL.Query()
		size    = query.Get("screenshot")
		sortCol = query.Get("sortCol")
		sortDir = query.Get("sortDir")
		pageNum = query.Get("pageNum")
		perPage = query.Get("perPage")
	)

	if !role.Allowed("vms", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting vms file not allowed",
			user,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	page, pageSize, err := util.ParsePage(pageNum, perPage)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	vms, err := vm.List(expName)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	allowed := mm.VMs{}

	for _, vm := range vms {
		if role.Allowed("vms", "list", fmt.Sprintf("%s/%s", expName, vm.Name)) {
			if vm.Running && size != "" {
				screenshot, err := util.GetScreenshot(expName, vm.Name, size)
				if err != nil {
					plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
						screenshot,
					)
				}
			}

			allowed = append(allowed, vm)
		}
	}

	if col := sortColumn(sortCol, sortDir); col != "" {
		allowed.SortBy(col, sortDir == sortAsc)
	}

	total := len(allowed)
	allowed = util.Page(allowed, page, pageSize)

	resp := &proto.VMList{
		Total: uint32(total), //nolint:gosec // G115 counts fit in a uint32
		Vms:   make([]*proto.VM, len(allowed)),
	}
	for i, v := range allowed {
		resp.Vms[i] = util.VMToProtobuf(expName, v, exp.Spec.Topology())
	}

	body, err := marshaler.Marshal(resp)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// GetVM - GET /experiments/{exp}/vms/{name}.
func GetVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
		query   = r.URL.Query()
		size    = query.Get("screenshot")
	)

	if !role.Allowed("vms", "get", fmt.Sprintf("%s/%s", expName, name)) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting vm not allowed",
			"user",
			user,
			"exp",
			expName,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	vm, err := vm.Get(expName, name)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	if vm.Running && size != "" {
		screenshot, err := util.GetScreenshot(expName, name, size)
		if err != nil {
			plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
		} else {
			vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		}
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *vm, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// vmUpdateFields name the VM name of exp a PATCH request updates and, in
// phenix.vm.changed, the settings it changes, without their values. Only a
// single-VM PATCH (single) changes the inject partition and tags.
func vmUpdateFields(exp, name string, req *proto.UpdateVMRequest, single bool) audit.Fields {
	var (
		f       = audit.Fields{Experiment: exp, VM: name}
		changed = []string{}
	)

	for _, c := range []struct {
		name string
		set  bool
	}{
		{"cpus", req.GetCpus() != 0},
		{"memory", req.GetRam() != 0},
		{"disk", req.GetDisk() != ""},
		{"inject_partition", single && req.GetInjectPartition() != 0},
		{"interface", req.GetInterface() != nil},
		{"tags", single && req.GetTagUpdateMode() != proto.TagUpdateMode_NONE},
		{"do_not_boot", req.GetBoot() != nil},
		{"host", req.GetClusterHost() != nil},
		{"snapshot", req.GetSnapshotOption() != nil},
	} {
		if c.set {
			changed = append(changed, c.name)
		}
	}

	if req.GetInterface() != nil {
		iface := int(req.GetInterface().GetIndex())
		f.Interface = &iface
	}

	f.Extra = []any{"phenix.vm.changed", changed}

	return f
}

// UpdateVM - PATCH /experiments/{exp}/vms/{name}.
//
//nolint:funlen // handler
func UpdateVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, name)) {
		audit.Denied(r, audit.VMUpdate, "updating vm not allowed", audit.Fields{Experiment: expName, VM: name})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.UpdateVMRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	opts := []vm.UpdateOption{
		vm.UpdateExperiment(expName),
		vm.UpdateVM(name),
		vm.UpdateWithCPU(int(req.GetCpus())),
		vm.UpdateWithMem(int(req.GetRam())),
		vm.UpdateWithDisk(req.GetDisk()),
		vm.UpdateWithPartition(int(req.GetInjectPartition())),
	}

	if req.GetInterface() != nil {
		opts = append(
			opts,
			vm.UpdateWithInterface(
				int(req.GetInterface().GetIndex()),
				req.GetInterface().GetVlan(),
			),
		)
	}

	switch req.GetTagUpdateMode() {
	case proto.TagUpdateMode_SET:
		opts = append(opts, vm.UpdateWithTags(req.GetTags(), false))
	case proto.TagUpdateMode_ADD:
		opts = append(opts, vm.UpdateWithTags(req.GetTags(), true))
	case proto.TagUpdateMode_NONE:
		// do nothing
	}

	if req.GetBoot() != nil {
		opts = append(opts, vm.UpdateWithDNB(req.GetDoNotBoot()))
	}

	if req.GetClusterHost() != nil {
		opts = append(opts, vm.UpdateWithHost(req.GetHost()))
	}

	if req.GetSnapshotOption() != nil {
		opts = append(opts, vm.UpdateWithSnapshot(req.GetSnapshot()))
	}

	updated := vmUpdateFields(expName, name, &req, true)

	if err := vm.Update(opts...); err != nil {
		audit.Failure(r, audit.VMUpdate, "updating vm failed", updated, err)
		plog.Error(plog.TypeSystem, "updating VM", "err", err)

		return weberror.New(http.StatusInternalServerError, "unable to update VM")
	}

	audit.Success(r, audit.VMUpdate, "vm updated", updated)

	exp, err := experiment.Get(expName)
	if err != nil {
		return weberror.New(http.StatusBadRequest, "unable to get experiment")
	}

	vm, err := vm.Get(expName, name)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "unable to get VM")
	}

	if vm.Running {
		screenshot, err := util.GetScreenshot(expName, name, defaultScreenshotSize)
		if err != nil {
			plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
		} else {
			vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		}
	}

	body, err = marshaler.Marshal(util.VMToProtobuf(expName, *vm, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms", "patch", fmt.Sprintf("%s/%s", expName, name)),
		bt.NewResource("experiment/vm", fmt.Sprintf("%s/%s", expName, name), "update"),
		body,
	)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// UpdateVMs - PATCH /experiments/{exp}/vms.
func UpdateVMs(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
	)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.UpdateVMRequestList
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	resp := &proto.VMList{Total: req.GetTotal()} //nolint:exhaustruct // partial initialization
	resp.Vms = make([]*proto.VM, int(req.GetTotal()))

	for index, vmRequest := range req.GetVms() {
		updated := vmUpdateFields(expName, vmRequest.GetName(), vmRequest, false)

		// Skip any vms that are not allowed to be updated
		if !role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, vmRequest.GetName())) {
			audit.Denied(r, audit.VMUpdate, "updating vm not allowed", updated)

			continue
		}

		opts := []vm.UpdateOption{
			vm.UpdateExperiment(expName),
			vm.UpdateVM(vmRequest.GetName()),
			vm.UpdateWithCPU(int(vmRequest.GetCpus())),
			vm.UpdateWithMem(int(vmRequest.GetRam())),
			vm.UpdateWithDisk(vmRequest.GetDisk()),
		}

		if vmRequest.GetInterface() != nil {
			opts = append(
				opts,
				vm.UpdateWithInterface(
					int(vmRequest.GetInterface().GetIndex()),
					vmRequest.GetInterface().GetVlan(),
				),
			)
		}

		if vmRequest.GetBoot() != nil {
			opts = append(opts, vm.UpdateWithDNB(vmRequest.GetDoNotBoot()))
		}

		if vmRequest.GetClusterHost() != nil {
			opts = append(opts, vm.UpdateWithHost(vmRequest.GetHost()))
		}

		if vmRequest.GetSnapshotOption() != nil {
			opts = append(opts, vm.UpdateWithSnapshot(vmRequest.GetSnapshot()))
		}

		if err := vm.Update(opts...); err != nil {
			audit.Failure(r, audit.VMUpdate, "updating vm failed", updated, err)
			plog.Error(plog.TypeSystem, "updating VM", "err", err)

			return weberror.New(http.StatusInternalServerError, "unable to update VM")
		}

		audit.Success(r, audit.VMUpdate, "vm updated", updated)

		exp, err := experiment.Get(expName)
		if err != nil {
			return weberror.New(http.StatusBadRequest, "unable to get experiment")
		}

		vm, err := vm.Get(expName, vmRequest.GetName())
		if err != nil {
			return weberror.New(http.StatusInternalServerError, "unable to get VM")
		}

		if vm.Running {
			screenshot, err := util.GetScreenshot(expName, vmRequest.GetName(), defaultScreenshotSize)
			if err != nil {
				plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
			} else {
				vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
					screenshot,
				)
			}
		}

		resp.Vms[index] = util.VMToProtobuf(expName, *vm, exp.Spec.Topology())
	}

	body, err = marshaler.Marshal(resp)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// RedeployVM - POST /experiments/{exp}/vms/{name}/redeploy.
//
//nolint:funlen // handler
func RedeployVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
		query    = r.URL.Query()
		inject   = query.Get("replicate-injects") != ""
	)

	audited := audit.Fields{Experiment: expName, VM: name}

	if !role.Allowed("vms/redeploy", "update", fullName) {
		audit.Denied(r, audit.VMRedeploy, "redeploying vm not allowed", audited)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if err := cache.LockVMForRedeploying(expName, name); err != nil {
		audit.Failure(r, audit.VMRedeploy, "redeploying vm failed", audited, err)
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"redeploying",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(expName, name)

	exp, err := experiment.Get(expName)
	if err != nil {
		audit.Failure(r, audit.VMRedeploy, "redeploying vm failed", audited, err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		audit.Failure(r, audit.VMRedeploy, "redeploying vm failed", audited, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	v.Busy = true

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))

	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/redeploy", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "redeploying"),
		body,
	)

	redeployed := make(chan error)

	go func() {
		defer close(redeployed)

		body, err := io.ReadAll(r.Body)
		if err != nil && !errors.Is(err, io.EOF) {
			redeployed <- err

			return
		}

		opts := []vm.RedeployOption{
			vm.CPU(v.CPUs),
			vm.Memory(v.RAM),
			vm.Disk(v.Disk),
			vm.Inject(inject),
		}

		// `body` will be nil if err above was EOF.
		if body != nil {
			var req proto.VMRedeployRequest

			// Update VM struct with values from POST request body.
			err := unmarshaler.Unmarshal(body, &req)
			if err != nil {
				redeployed <- err

				return
			}

			opts = []vm.RedeployOption{
				vm.CPU(int(req.GetCpus())),
				vm.Memory(int(req.GetRam())),
				vm.Disk(req.GetDisk()),
				vm.Inject(req.GetInjects()),
			}
		}

		if err := vm.Redeploy(expName, name, opts...); err != nil {
			redeployed <- err
		}

		v.Busy = false
	}()

	// HACK: mandatory sleep time to make it seem like a redeploy is
	// happening client-side, even when the redeploy is fast (like for
	// Linux VMs).
	time.Sleep(5 * time.Second) //nolint:mnd // sleep duration

	err = <-redeployed
	if err != nil {
		plog.Error(plog.TypeSystem, "redeploying VM", "exp", expName, "vm", name, "err", err)
		audit.Failure(r, audit.VMRedeploy, "redeploying vm failed", audited, err)

		broker.Broadcast(
			bt.NewRequestPolicy("vms/redeploy", "update", fullName),
			bt.NewResource("experiment/vm", expName+"/"+name, "errorRedeploying"),
			nil,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.VMRedeploy, "vm redeployed", audited)

	// Get the VM details again since redeploying may have changed them.
	v, err = vm.Get(expName, name)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	screenshot, err := util.GetScreenshot(expName, name, defaultScreenshotSize)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, _ = marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))

	broker.Broadcast(
		bt.NewRequestPolicy("vms/redeploy", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "redeployed"),
		body,
	)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// GetScreenshot - GET /experiments/{exp}/vms/{name}/screenshot.png.
func GetScreenshot(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx    = r.Context()
		role   = middleware.RoleFromContext(ctx)
		vars   = mux.Vars(r)
		exp    = vars["exp"]
		name   = vars["name"]
		query  = r.URL.Query()
		size   = query.Get("size")
		encode = query.Get("base64") != ""
	)

	if !role.Allowed("vms/screenshot", "get", exp+"/"+name) {
		plog.Warn(
			plog.TypeSecurity,
			"screenshotting vm not allowed",
			"user",

			ctx.Value(middleware.ContextKeyUser),
			"exp",
			exp,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if size == "" {
		size = defaultScreenshotSize
	}

	screenshot, err := util.GetScreenshot(exp, name, size)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if encode {
		encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		_, _ = w.Write([]byte(encoded)) //nolint:gosec // XSS via taint analysis

		return nil
	}

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(screenshot) //nolint:gosec // XSS via taint analysis

	return nil
}

// GetAllVMs - GET /vms.
//
// Lists the running VMs of every running experiment that the role may list;
// with pageNum and perPage, only those on that page get screenshots.
func GetAllVMs(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		query = r.URL.Query()
		size  = query.Get("screenshot")
	)

	if !role.Allowed("vms", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing vms not allowed",
			"user",
			user,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	page, pageSize, err := util.ParsePage(query.Get("pageNum"), query.Get("perPage"))
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	exps, err := experiment.List()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)
	}

	type runningVM struct {
		exp  string
		vm   mm.VM
		topo ifaces.TopologySpec
	}

	var running []runningVM

	for _, exp := range exps {
		if !exp.Running() {
			// We only care about getting running VMs, which are only present in
			// running experiments.
			continue
		}

		vms, err := vm.List(exp.Spec.ExperimentName())
		if err != nil {
			plog.Error(
				plog.TypeSystem,
				"listing VMs for experiment",
				"exp",
				exp.Spec.ExperimentName(),
				"err",
				err,
			)
		}

		for _, vm := range vms {
			id := exp.Metadata.Name + "/" + vm.Name

			if !role.Allowed("vms", "list", id) {
				continue
			}

			if !vm.Running {
				// We only care about running VMs.
				continue
			}

			running = append(running, runningVM{exp: exp.Metadata.Name, vm: vm, topo: exp.Spec.Topology()})
		}
	}

	total := len(running)
	running = util.Page(running, page, pageSize)
	allowed := make([]*proto.VM, 0, len(running))

	for _, v := range running {
		if size != "" {
			screenshot, err := util.GetScreenshot(v.exp, v.vm.Name, size)
			if err != nil {
				plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
			} else {
				v.vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
			}
		}

		allowed = append(allowed, util.VMToProtobuf(v.exp, v.vm, v.topo))
	}

	resp := &proto.VMList{Total: uint32(total), Vms: allowed} //nolint:gosec // G115 counts fit in a uint32

	body, err := marshaler.Marshal(resp)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// ChangeOpticalDisc - POST /experiments/{exp}/vms/{name}/cdrom.
func ChangeOpticalDisc(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		query    = r.URL.Query()
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		isoPath  = query.Get("isoPath")
		fullName = exp + "/" + name
	)

	changed := audit.Fields{Experiment: exp, VM: name, File: isoPath}

	if !role.Allowed("vms/cdrom", "update", fullName) {
		audit.Denied(r, audit.VMDiscChange, "changing optical disc not allowed", changed)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := vm.ChangeOpticalDisc(exp, name, isoPath)
	if err != nil {
		audit.Failure(r, audit.VMDiscChange, "changing optical disc failed", changed, err)
		plog.Error(plog.TypeSystem, "changing disc for VM", "exp", exp, "vm", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/cdrom", "update", fullName),
		bt.NewResource("experiment/vm", fullName, "cdrom-inserted"),
		nil,
	)

	audit.Success(r, audit.VMDiscChange, "optical disc changed", changed)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// EjectOpticalDisc - DELETE /experiments/{exp}/vms/{name}/cdrom.
func EjectOpticalDisc(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	ejected := audit.Fields{Experiment: exp, VM: name}

	if !role.Allowed("vms/cdrom", "delete", fullName) {
		audit.Denied(r, audit.VMDiscEject, "ejecting optical disc not allowed", ejected)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := vm.EjectOpticalDisc(exp, name)
	if err != nil {
		audit.Failure(r, audit.VMDiscEject, "ejecting optical disc failed", ejected, err)
		plog.Error(plog.TypeSystem, "ejecting disc for VM", "exp", exp, "vm", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/cdrom", "delete", fullName),
		bt.NewResource("experiment/vm", fullName, "cdrom-ejected"),
		nil,
	)

	audit.Success(r, audit.VMDiscEject, "optical disc ejected", ejected)
	w.WriteHeader(http.StatusNoContent)

	return nil
}
