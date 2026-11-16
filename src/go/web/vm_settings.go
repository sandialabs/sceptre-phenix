package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/job"
	"phenix/api/vm"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// resetSettingsChanged names, in phenix.vm.changed, the settings that a reset restores.
var resetSettingsChanged = []string{ //nolint:gochecknoglobals // fixed list
	"cpus", "memory", "disk", "inject_partition", "do_not_boot", "host", "snapshot",
}

// ResetVMSettings - POST /experiments/{exp}/vms/resetSettings: restores, in the
// stopped experiment, the settings that each VM named in vms (every VM the
// role may patch when vms is empty) had when phenix created the experiment. It
// answers as PATCH /experiments/{exp}/vms does, but with 200 also when every
// VM failed, so that the errors name each VM that has no saved settings.
func ResetVMSettings(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		expName = mux.Vars(r)["exp"]
	)

	if !role.Allowed("vms", "patch") {
		audit.Denied(r, audit.VMUpdate, "resetting vm settings not allowed", audit.Fields{Experiment: expName})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.VMNameList

	if len(strings.TrimSpace(string(body))) > 0 {
		if err := unmarshaler.Unmarshal(body, &req); err != nil {
			return weberror.New(http.StatusBadRequest, err.Error())
		}
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return experimentError(expName, err)
	}

	allowed, skipped := resettableVMs(r, role, exp, req.GetVms())
	if len(allowed) == 0 && (len(skipped) > 0 || (len(req.GetVms()) == 0 && len(experimentVMNames(exp)) > 0)) {
		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if exp.Running() {
		return weberror.New(http.StatusConflict, fmt.Sprintf("experiment %s is running", expName))
	}

	// held for the whole reset, so a start or another change cannot begin
	if err := cache.LockExperimentForUpdate(expName); err != nil {
		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockExperiment(expName)

	resp := &proto.VMList{ //nolint:exhaustruct // partial initialization
		Vms:    make([]*proto.VM, 0, len(allowed)),
		Errors: skipped,
	}

	vmErrs, err := vm.ResetSettings(expName, allowed)
	if err != nil {
		for _, name := range allowed {
			audit.Failure(r, audit.VMUpdate, "resetting vm settings failed", resetFields(expName, name), err)
		}

		return resetExperimentError(expName, err)
	}

	for _, name := range allowed {
		if pb, vmErr := resetResult(r, expName, name, vmErrs[name]); vmErr != nil {
			resp.Errors = append(resp.Errors, vmErr)
		} else if pb != nil {
			resp.Vms = append(resp.Vms, pb)
		}
	}

	if requestGone(r) {
		return nil
	}

	resp.Total = uint32(len(resp.GetVms())) //nolint:gosec // G115 as many VMs as the experiment has

	body, err = marshaler.Marshal(resp)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// resetResult audits the reset of the VM name of expName, which failed with
// err unless it is nil. It returns the error item of a failed VM, or else the
// VM read back, nil when it cannot be read, and tells the clients about it.
func resetResult(r *http.Request, expName, name string, err error) (*proto.VM, *proto.VMItemError) {
	fields := resetFields(expName, name)

	if err != nil {
		audit.Failure(r, audit.VMUpdate, "resetting vm settings failed", fields, err)
		plog.Error(plog.TypeSystem, "resetting VM settings", "exp", expName, "vm", name, "err", err)

		return nil, &proto.VMItemError{Name: name, Status: string(job.ItemFailed), Error: resetSettingsError(expName, name, err)}
	}

	audit.Success(r, audit.VMUpdate, "vm settings reset", fields)

	// the reset is made, so every client hears of it, also once this one left
	pb := readBackVM(context.WithoutCancel(r.Context()), r, expName, name)
	if pb == nil {
		return nil, nil
	}

	if body, err := marshaler.Marshal(pb); err == nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms", "patch", expName+"/"+name),
			bt.NewResource("experiment/vm", expName+"/"+name, actionUpdate),
			body,
		)
	}

	return pb, nil
}

// experimentVMNames returns the names of the experiment's VMs, leaving out
// external nodes, which phenix does not deploy.
func experimentVMNames(exp *types.Experiment) []string {
	topo := exp.Spec.Topology()
	if topo == nil {
		return nil
	}

	var names []string

	for _, node := range topo.Nodes() {
		if !node.External() {
			names = append(names, node.General().Hostname())
		}
	}

	return names
}

// resettableVMs splits the VMs that a reset names into those the role may
// patch and, as errors, those it skips, auditing each refusal. With no names,
// it takes every VM of the experiment that the role may patch, and skips none.
func resettableVMs(r *http.Request, role rbac.Role, exp *types.Experiment, names []string) ([]string, []*proto.VMItemError) {
	var (
		expName = exp.Metadata.Name
		allowed []string
		skipped []*proto.VMItemError
	)

	if len(names) == 0 {
		for _, name := range experimentVMNames(exp) {
			if role.Allowed("vms", "patch", expName+"/"+name) {
				allowed = append(allowed, name)
			}
		}

		return allowed, nil
	}

	for _, name := range names {
		if role.Allowed("vms", "patch", expName+"/"+name) {
			allowed = append(allowed, name)

			continue
		}

		audit.Denied(r, audit.VMUpdate, "resetting vm settings not allowed",
			audit.Fields{Experiment: expName, VM: name, Extra: []any{"phenix.vm.changed", resetSettingsChanged}})

		skipped = append(skipped, &proto.VMItemError{
			Name: name, Status: string(job.ItemSkipped), Error: job.ErrForbidden.Error(),
		})
	}

	return allowed, skipped
}

// resetFields name the VM name of expName and the settings a reset restores for its audit event.
func resetFields(expName, name string) audit.Fields {
	return audit.Fields{Experiment: expName, VM: name, Extra: []any{"phenix.vm.changed", resetSettingsChanged}}
}

// resetExperimentError is the error that a reset answers when it can reset no
// VM of the experiment expName: 409 for a running experiment, 404 for one that
// does not exist, else 500.
func resetExperimentError(expName string, err error) error {
	switch {
	case errors.Is(err, experiment.ErrExperimentRunning):
		return weberror.New(http.StatusConflict, fmt.Sprintf("experiment %s is running", expName))
	case storeStatus(err) == http.StatusNotFound:
		return experimentError(expName, err)
	}

	return weberror.NewWebError(err, "unable to reset the VM settings of experiment %s", expName).
		SetStatus(http.StatusInternalServerError)
}

// resetSettingsError is the error that a reset reports for the VM name.
func resetSettingsError(expName, name string, err error) string {
	switch {
	case errors.Is(err, experiment.ErrNoVMDefaults):
		return experiment.ErrNoVMDefaults.Error()
	case errors.Is(err, mm.ErrVMNotFound):
		return fmt.Sprintf("VM %s not found in experiment %s", name, expName)
	}

	return "unable to reset the VM settings"
}
