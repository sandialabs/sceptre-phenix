package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/disk"
	"phenix/api/experiment"
	"phenix/api/job"
	"phenix/api/vm"
	ifaces "phenix/types/interfaces"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/rbac"
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

	vmQuery := util.VMQuery{ //nolint:exhaustruct // this route does not filter
		ShowDNB: true,
		SortCol: sortColumn(sortCol, sortDir),
		SortAsc: sortDir == sortAsc,
		Page:    page,
		Size:    pageSize,
	}

	vms, observed := vm.ListForObserved(ctx, exp)
	allowed, total := util.SelectVMs(expName, vms, exp.Spec.Topology(), vmQuery, role)

	if size != "" {
		addVMScreenshots(ctx, expName, allowed, size)
	}

	if requestGone(r) {
		return nil
	}

	resp := util.VMListToProtobuf(expName, allowed, total, exp.Spec.Topology())
	util.SetVMListObservation(resp, observed)
	resp.Labels = util.VMLabelsToProtobuf(util.VMLabels(expName, vms, vmQuery.ShowDNB, role))
	resp.Hosts = util.VMHosts(expName, vms, vmQuery.ShowDNB, role)
	resp.Running = util.CountRunning(expName, vms, vmQuery.ShowDNB, role)
	freshness.SetObserved(w, observed)

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

	vm, err := vm.GetFor(ctx, exp, name)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	if vm.Running && size != "" {
		screenshot, err := util.GetScreenshotContext(ctx, expName, name, size)
		if requestCanceled(r, err) {
			return nil
		}

		if err != nil {
			plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
		} else {
			vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		}
	}

	body, err := marshaler.Marshal(vmWithAnnotations(expName, *vm, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// vmWithAnnotations converts a VM for a response about that one VM, which,
// unlike VM lists and broadcasts, carries its node's annotations.
func vmWithAnnotations(expName string, v mm.VM, topo ifaces.TopologySpec) *proto.VM {
	pb := util.VMToProtobuf(expName, v, topo)

	annotations, err := util.AnnotationsToProtobuf(v.Annotations)
	if err != nil {
		plog.Error(plog.TypeSystem, "converting VM annotations", "exp", expName, "vm", v.Name, "err", err)

		return pb
	}

	pb.Annotations = annotations

	return pb
}

// vmUpdateFields name the VM name of exp a PATCH request updates and, in
// phenix.vm.changed, the settings it changes, without their values. Only a
// single-VM PATCH (single) changes the inject partition, tags and annotations.
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
		{"annotations", single && req.GetAnnotations() != nil},
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

	if req.GetAnnotations() != nil {
		annotations := req.GetAnnotations().AsMap()

		if changesAutotunnel(expName, name, annotations) && forwardDeniedVM(role, expName, []string{name}) != "" {
			denied := updated
			denied.Reason = "port forwards not allowed"
			audit.Denied(r, audit.VMUpdate, "creating port forwards not allowed", denied)

			return weberror.New(http.StatusForbidden, "forbidden")
		}

		opts = append(opts, vm.UpdateWithAnnotations(annotations))
	}

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	if err := vm.Update(append(opts, vm.UpdateWithContext(change))...); err != nil {
		// one that gave up before its first change left the VM as it was
		if requestCanceled(r, err) {
			return nil
		}

		audit.Failure(r, audit.VMUpdate, "updating vm failed", updated, err)

		if errors.Is(err, vm.ErrInvalidAnnotations) || errors.Is(err, vm.ErrInvalidDisk) || errors.Is(err, mm.ErrUnsafeArg) {
			return weberror.New(http.StatusBadRequest, err.Error())
		}

		plog.Error(plog.TypeSystem, "updating VM", "err", err)

		return weberror.New(http.StatusInternalServerError, "unable to update VM")
	}

	audit.Success(r, audit.VMUpdate, "vm updated", updated)

	exp, err := experiment.Get(expName)
	if err != nil {
		return weberror.New(http.StatusBadRequest, "unable to get experiment")
	}

	// broadcast to every client, so read even once this request's client left
	vm, err := vm.GetFor(context.WithoutCancel(ctx), exp, name)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "unable to get VM")
	}

	if vm.Running {
		vm.Screenshot = vmScreenshot(change, expName, name, defaultScreenshotSize)
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

	// a caller who may also read the VM gets its annotations
	if role.Allowed("vms", "get", fmt.Sprintf("%s/%s", expName, name)) {
		body, err = marshaler.Marshal(vmWithAnnotations(expName, *vm, exp.Spec.Topology()))
		if err != nil {
			return weberror.New(http.StatusInternalServerError, err.Error())
		}
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// UpdateVMs - PATCH /experiments/{exp}/vms: updates each VM the role may patch, carrying on past failures, and
// answers with the VMs read back and in errors those skipped or failed; 403 if it may patch none, 500 if all fail.
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

	// refused before any VM changes, as minimega could not take the disk or VLAN
	for _, vmRequest := range req.GetVms() {
		if err := disk.ValidateMinimegaPath(vmRequest.GetDisk()); err != nil {
			return weberror.New(http.StatusBadRequest, fmt.Sprintf("VM %s: %v", vmRequest.GetName(), err))
		}

		if vlan := vmRequest.GetInterface().GetVlan(); vlan != "" {
			if err := mm.ValidateArg(vlan); err != nil {
				return weberror.New(http.StatusBadRequest, fmt.Sprintf("VM %s: VLAN: %v", vmRequest.GetName(), err))
			}
		}
	}

	allowed, skipped := patchableVMs(r, role, expName, req.GetVms())
	if len(allowed) == 0 && len(skipped) > 0 {
		return weberror.New(http.StatusForbidden, "forbidden")
	}

	resp := &proto.VMList{ //nolint:exhaustruct // partial initialization
		Vms:    make([]*proto.VM, 0, len(allowed)),
		Errors: skipped,
	}

	// every VM is updated even once the client has left, until the deadline
	change, cancel := committedChangeContext(r, vmsChangeTimeout)
	defer cancel()

	var failed []error

	for _, vmRequest := range allowed {
		if err := updateListedVM(r, change, expName, vmRequest); err != nil {
			failed = append(failed, fmt.Errorf("VM %s: %w", vmRequest.GetName(), err))
			resp.Errors = append(resp.Errors, &proto.VMItemError{
				Name: vmRequest.GetName(), Status: string(job.ItemFailed), Error: err.Error(),
			})

			continue
		}

		// The rest only reads the VM back for the response. Once the client
		// has left, the remaining VMs are still updated, but not read back.
		if requestGone(r) {
			continue
		}

		if pb := readBackVM(ctx, r, expName, vmRequest.GetName()); pb != nil {
			resp.Vms = append(resp.Vms, pb)
		}
	}

	if requestGone(r) {
		return nil
	}

	if len(allowed) > 0 && len(failed) == len(allowed) {
		return weberror.NewWebError(errors.Join(failed...), "unable to update VMs").
			SetStatus(http.StatusInternalServerError)
	}

	resp.Total = uint32(len(resp.GetVms())) //nolint:gosec // G115 as many VMs as the request named

	body, err = marshaler.Marshal(resp)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// patchableVMs splits the VMs a bulk update names into those the role may
// patch and, as errors, those it skips, auditing each refusal.
func patchableVMs(
	r *http.Request, role rbac.Role, expName string, vms []*proto.UpdateVMRequest,
) ([]*proto.UpdateVMRequest, []*proto.VMItemError) {
	var (
		allowed = make([]*proto.UpdateVMRequest, 0, len(vms))
		skipped []*proto.VMItemError
	)

	for _, vmRequest := range vms {
		if role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, vmRequest.GetName())) {
			allowed = append(allowed, vmRequest)

			continue
		}

		audit.Denied(r, audit.VMUpdate, "updating vm not allowed", vmUpdateFields(expName, vmRequest.GetName(), vmRequest, false))

		skipped = append(skipped, &proto.VMItemError{
			Name: vmRequest.GetName(), Status: string(job.ItemSkipped), Error: job.ErrForbidden.Error(),
		})
	}

	return allowed, skipped
}

// updateListedVM makes the change a bulk update asks of one VM, in change,
// and audits it.
func updateListedVM(r *http.Request, change context.Context, expName string, vmRequest *proto.UpdateVMRequest) error {
	updated := vmUpdateFields(expName, vmRequest.GetName(), vmRequest, false)

	opts := []vm.UpdateOption{
		vm.UpdateExperiment(expName),
		vm.UpdateVM(vmRequest.GetName()),
		vm.UpdateWithCPU(int(vmRequest.GetCpus())),
		vm.UpdateWithMem(int(vmRequest.GetRam())),
		vm.UpdateWithDisk(vmRequest.GetDisk()),
		vm.UpdateWithContext(change),
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
		plog.Error(plog.TypeSystem, "updating VM", "exp", expName, "vm", vmRequest.GetName(), "err", err)

		return err //nolint:wrapcheck // the caller names the VM
	}

	audit.Success(r, audit.VMUpdate, "vm updated", updated)

	return nil
}

// readBackVM returns the VM name as a bulk update that took left it, with a screenshot when it runs, or nil
// when it cannot be read.
func readBackVM(ctx context.Context, r *http.Request, expName, name string) *proto.VM {
	exp, err := experiment.Get(expName)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading back updated VM", "exp", expName, "vm", name, "err", err)

		return nil
	}

	v, err := vm.GetFor(ctx, exp, name)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "reading back updated VM", "exp", expName, "vm", name, "err", err)

		return nil
	}

	if v.Running {
		screenshot, err := util.GetScreenshotContext(ctx, expName, name, defaultScreenshotSize)
		if requestCanceled(r, err) {
			return nil
		}

		if err != nil {
			plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
		} else {
			v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		}
	}

	return util.VMToProtobuf(expName, *v, exp.Spec.Topology())
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
	)

	redeployed := audit.Fields{Experiment: expName, VM: name}

	if !role.Allowed("vms/redeploy", "update", fullName) {
		audit.Denied(r, audit.VMRedeploy, "redeploying vm not allowed", redeployed)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.VMRedeployRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	// refused before the VM is touched, as minimega could not boot it
	if err := disk.ValidateMinimegaPath(req.GetDisk()); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if err := cache.LockVMForRedeploying(expName, name); err != nil {
		audit.Failure(r, audit.VMRedeploy, "redeploying vm failed", redeployed, err)
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
		audit.Failure(r, audit.VMRedeploy, "redeploying vm failed", redeployed, err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	// the redeploy starts only once this has answered, within the deadline
	v, err := vm.GetFor(change, exp, name)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		audit.Failure(r, audit.VMRedeploy, "redeploying vm failed", redeployed, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	v.Busy = true

	body, err = marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/redeploy", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "redeploying"),
		body,
	)

	done := make(chan error)

	go func() {
		defer close(done)

		opts := []vm.RedeployOption{
			vm.CPU(int(req.GetCpus())),
			vm.Memory(int(req.GetRam())),
			vm.Disk(req.GetDisk()),
			vm.Inject(req.GetInjects()),
		}

		if err := vm.Redeploy(change, expName, name, opts...); err != nil {
			done <- err
		}

		v.Busy = false
	}()

	// HACK: mandatory sleep time to make it seem like a redeploy is
	// happening client-side, even when the redeploy is fast (like for
	// Linux VMs).
	time.Sleep(5 * time.Second) //nolint:mnd // sleep duration

	err = <-done
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/redeploy", "update", fullName),
			bt.NewResource("experiment/vm", expName+"/"+name, "errorRedeploying"),
			nil,
		)

		// one that gave up before its first change left the VM as it was
		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "redeploying VM", "exp", expName, "vm", name, "err", err)
		audit.Failure(r, audit.VMRedeploy, "redeploying vm failed", redeployed, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.VMRedeploy, "vm redeployed", redeployed)

	// Get the VM details again since redeploying may have changed them. They
	// are broadcast to every client, so read even once this request's left.
	v, err = vm.Get(context.WithoutCancel(ctx), expName, name)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	v.Screenshot = vmScreenshot(change, expName, name, defaultScreenshotSize)

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
		_, _ = w.Write([]byte(encoded)) //nolint:gosec // G705 base64 text, not HTML

		return nil
	}

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(screenshot) //nolint:gosec // G705 a PNG image, not HTML

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
		plog.Warn(plog.TypeSecurity, "listing vms not allowed", "user", middleware.UserFromContext(ctx))

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	page, pageSize, err := util.ParsePage(query.Get("pageNum"), query.Get("perPage"))
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	exps, err := experiment.List()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)

		// an empty list would read as no running VMs
		if len(exps) == 0 {
			return weberror.New(http.StatusInternalServerError, "unable to read experiments from the store")
		}
	}

	type runningVM struct {
		exp   string
		vm    mm.VM
		nodes util.NodeIndex
	}

	var (
		running  []runningVM
		observed = freshness.Unobserved()
	)

	for _, exp := range exps {
		if !exp.Running() {
			// We only care about getting running VMs, which are only present in
			// running experiments.
			continue
		}

		vms, obs := vm.ListForObserved(ctx, &exp)
		observed = observed.Older(obs)
		nodes := util.IndexTopology(exp.Spec.Topology())

		for _, vm := range vms {
			// We only care about running VMs.
			if vm.Running && role.Allowed("vms", "list", exp.Metadata.Name+"/"+vm.Name) {
				running = append(running, runningVM{exp: exp.Metadata.Name, vm: vm, nodes: nodes})
			}
		}
	}

	total := len(running)
	running = util.Page(running, page, pageSize)
	allowed := make([]*proto.VM, 0, len(running))

	for _, v := range running {
		if size != "" {
			screenshot, err := util.GetScreenshotContext(ctx, v.exp, v.vm.Name, size)
			if requestGone(r) {
				return nil
			}

			if err != nil {
				plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
			} else {
				v.vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
			}
		}

		allowed = append(allowed, util.VMToProtobufIndexed(v.exp, v.vm, v.nodes))
	}

	if requestGone(r) {
		return nil
	}

	resp := &proto.VMList{ //nolint:exhaustruct // partial initialization
		Total: uint32(total), //nolint:gosec // G115 counts fit in a uint32
		Vms:   allowed,
	}
	util.SetVMListObservation(resp, observed)
	freshness.SetObserved(w, observed)

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

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	// a canceled request left the VM as it was, so is not audited
	err := vm.ChangeOpticalDisc(change, exp, name, isoPath)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		audit.Failure(r, audit.VMDiscChange, "changing optical disc failed", changed, err)
		plog.Error(plog.TypeSystem, "changing disc for VM", "exp", exp, "vm", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.VMDiscChange, "optical disc changed", changed)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/cdrom", "update", fullName),
		bt.NewResource("experiment/vm", fullName, "cdrom-inserted"),
		nil,
	)

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

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	// a canceled request left the VM as it was, so is not audited
	err := vm.EjectOpticalDisc(change, exp, name)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		audit.Failure(r, audit.VMDiscEject, "ejecting optical disc failed", ejected, err)
		plog.Error(plog.TypeSystem, "ejecting disc for VM", "exp", exp, "vm", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.VMDiscEject, "optical disc ejected", ejected)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/cdrom", "delete", fullName),
		bt.NewResource("experiment/vm", fullName, "cdrom-ejected"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)

	return nil
}
