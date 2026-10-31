package web

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/util"
)

// GetVMs - GET /experiments/{exp}/vms.
func GetVMs(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	vms, err := vm.List(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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

	if sortCol != "" && sortDir != "" {
		allowed.SortBy(sortCol, sortDir == "asc")
	}

	if pageNum != "" && perPage != "" {
		n, _ := strconv.Atoi(pageNum)
		s, _ := strconv.Atoi(perPage)

		allowed = allowed.Paginate(n, s)
	}

	resp := &proto.VMList{
		Total: uint32(len(allowed)), //nolint:gosec // integer overflow conversion int -> uint32
		Vms:   make([]*proto.VM, len(allowed)),
	}
	for i, v := range allowed {
		resp.Vms[i] = util.VMToProtobuf(expName, v, exp.Spec.Topology())
	}

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetVM - GET /experiments/{exp}/vms/{name}.
func GetVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	vm, err := vm.Get(expName, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// UpdateVM - PATCH /experiments/{exp}/vms/{name}.
//
//nolint:funlen // handler
func UpdateVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, name)) {
		plog.Warn(
			plog.TypeSecurity,
			"updating vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.UpdateVMRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
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

	if err := vm.Update(opts...); err != nil {
		plog.Error(plog.TypeSystem, "updating VM", "err", err)
		http.Error(w, "unable to update VM", http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, "unable to get experiment", http.StatusBadRequest)

		return
	}

	vm, err := vm.Get(expName, name)
	if err != nil {
		http.Error(w, "unable to get VM", http.StatusInternalServerError)

		return
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
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms", "patch", fmt.Sprintf("%s/%s", expName, name)),
		bt.NewResource("experiment/vm", fmt.Sprintf("%s/%s", expName, name), "update"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm updated",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// UpdateVMs - PATCH /experiments/{exp}/vms.
//
//nolint:funlen // handler
func UpdateVMs(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
	)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.UpdateVMRequestList
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	resp := &proto.VMList{Total: req.GetTotal()} //nolint:exhaustruct // partial initialization
	resp.Vms = make([]*proto.VM, int(req.GetTotal()))

	for index, vmRequest := range req.GetVms() {
		// Skip any vms that are not allowed to be updated
		if !role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, vmRequest.GetName())) {
			plog.Warn(
				plog.TypeSecurity,
				"updating vm is not allowed",
				"user",
				middleware.UserFromContext(ctx),
				"exp",
				expName,
				"vm",
				vmRequest.GetName(),
			)

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
			plog.Error(plog.TypeSystem, "updating VM", "err", err)
			http.Error(w, "unable to update VM", http.StatusInternalServerError)

			return
		}

		exp, err := experiment.Get(expName)
		if err != nil {
			http.Error(w, "unable to get experiment", http.StatusBadRequest)

			return
		}

		vm, err := vm.Get(expName, vmRequest.GetName())
		if err != nil {
			http.Error(w, "unable to get VM", http.StatusInternalServerError)

			return
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
		plog.Info(
			plog.TypeAction,
			"vm updated",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			vmRequest.GetName(),
		)
	}

	body, err = marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// RedeployVM - POST /experiments/{exp}/vms/{name}/redeploy.
//
//nolint:funlen // handler
func RedeployVM(w http.ResponseWriter, r *http.Request) {
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

	if !role.Allowed("vms/redeploy", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"redeploying vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if err := cache.LockVMForRedeploying(expName, name); err != nil {
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
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	v.Busy = true

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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

		broker.Broadcast(
			bt.NewRequestPolicy("vms/redeploy", "update", fullName),
			bt.NewResource("experiment/vm", expName+"/"+name, "errorRedeploying"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// Get the VM details again since redeploying may have changed them.
	v, err = vm.Get(expName, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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

	plog.Info(
		plog.TypeAction,
		"vm redeployed",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetScreenshot - GET /experiments/{exp}/vms/{name}/screenshot.png.
func GetScreenshot(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if size == "" {
		size = defaultScreenshotSize
	}

	screenshot, err := util.GetScreenshot(exp, name, size)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if encode {
		encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		_, _ = w.Write([]byte(encoded)) //nolint:gosec // XSS via taint analysis

		return
	}

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(screenshot) //nolint:gosec // XSS via taint analysis
}

// GetAllVMs - GET /vms.
func GetAllVMs(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	exps, err := experiment.List()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)
	}

	allowed := []*proto.VM{}

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

			if size != "" {
				screenshot, err := util.GetScreenshot(exp.Metadata.Name, vm.Name, size)
				if err != nil {
					plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
						screenshot,
					)
				}
			}

			allowed = append(allowed, util.VMToProtobuf(exp.Metadata.Name, vm, exp.Spec.Topology()))
		}
	}

	resp := &proto.VMList{Total: uint32(len(allowed)), Vms: allowed} //nolint:gosec // integer overflow conversion int -> uint32

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// ChangeOpticalDisc - POST /experiments/{exp}/vms/{name}/cdrom.
func ChangeOpticalDisc(w http.ResponseWriter, r *http.Request) {
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

	if !role.Allowed("vms/cdrom", "update", fullName) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"changing optical disk not allowed",
			user,
			"exp",
			exp,
			"vm",
			name,
			"iso",
			isoPath,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := vm.ChangeOpticalDisc(exp, name, isoPath)
	if err != nil {
		plog.Error(plog.TypeSystem, "changing disc for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/cdrom", "update", fullName),
		bt.NewResource("experiment/vm", fullName, "cdrom-inserted"),
		nil,
	)

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"optical disk changed",
		user,
		"exp",
		exp,
		"vm",
		name,
		"iso",
		isoPath,
	)
	w.WriteHeader(http.StatusNoContent)
}

// EjectOpticalDisc - DELETE /experiments/{exp}/vms/{name}/cdrom.
func EjectOpticalDisc(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/cdrom", "delete", fullName) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"ejecting optical disk not allowed",
			user,
			"exp",
			exp,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := vm.EjectOpticalDisc(exp, name)
	if err != nil {
		plog.Error(plog.TypeSystem, "ejecting disc for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/cdrom", "delete", fullName),
		bt.NewResource("experiment/vm", fullName, "cdrom-ejected"),
		nil,
	)

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"optical disk ejected",
		user,
		"exp",
		exp,
		"vm",
		name,
	)
	w.WriteHeader(http.StatusNoContent)
}
