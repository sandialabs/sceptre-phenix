package web

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/util"
	"phenix/web/weberror"
)

// DeleteVM - DELETE /experiments/{exp}/vms/{name}.
func DeleteVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms", "delete", fmt.Sprintf("%s/%s", expName, name)) {
		plog.Warn(
			plog.TypeSecurity,
			"deleting vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
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

	if !exp.Running() {
		return weberror.New(http.StatusBadRequest, "experiment not running")
	}

	if err := mm.KillVM(mm.NS(expName), mm.VMName(name)); err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms", "delete", fmt.Sprintf("%s/%s", expName, name)),
		bt.NewResource("experiment/vm", fmt.Sprintf("%s/%s", expName, name), "delete"),
		nil,
	)

	plog.Info(
		plog.TypeAction,
		"vm deleted",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// StartVM - POST /experiments/{exp}/vms/{name}/start.
//
//nolint:funlen // handler
func StartVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/start", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"starting vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if err := cache.LockVMForStarting(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"starting",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/start", "update", fullName),
		bt.NewResource("experiment/vm", name, "starting"),
		nil,
	)

	if err := mm.StartVM(mm.NS(expName), mm.VMName(name)); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	screenshot, err := util.GetScreenshot(expName, name, "215")
	if err != nil {
		plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/start", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "start"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm started",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// StopVM - POST /experiments/{exp}/vms/{name}/stop.
//
//nolint:funlen // handler
func StopVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/stop", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"stopping vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"stopping",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/stop", "update", fullName),
		bt.NewResource("experiment/vm", name, "stopping"),
		nil,
	)

	if err := mm.StopVM(mm.NS(expName), mm.VMName(name)); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/stop", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "stop"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm stopped",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// RestartVM - POST /experiments/{exp}/vms/{name}/restart.
func RestartVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/restart", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"restarting vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if err := cache.LockVMForStarting(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"starting",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/restart", "update", fullName),
		bt.NewResource("experiment/vm", name, "restarting"),
		nil,
	)

	if err := vm.Restart(expName, name); err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	screenshot, err := util.GetScreenshot(expName, name, defaultScreenshotSize)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/restart", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "update"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm restarted",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// ShutdownVM - POST /experiments/{exp}/vms/{name}/shutdown.
func ShutdownVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/shutdown", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"shutting down vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"stopping",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(expName, name)

	if err := vm.Shutdown(expName, name); err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	v.Running = false

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/shutdown", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "shutdown"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm shutdown",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// ResetVM - POST /experiments/{exp}/vms/{name}/reset.
func ResetVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/reset", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"resetting vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"stopping",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(expName, name)

	if err := vm.ResetDiskState(expName, name); err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/reset", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "reset"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm reset",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}
