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
)

// DeleteVM - DELETE /experiments/{exp}/vms/{name}.
func DeleteVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if !exp.Running() {
		http.Error(w, "experiment not running", http.StatusBadRequest)

		return
	}

	if err := mm.KillVM(mm.NS(expName), mm.VMName(name)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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
}

// StartVM - POST /experiments/{exp}/vms/{name}/start.
//
//nolint:funlen // handler
func StartVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
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
		http.Error(w, err.Error(), http.StatusConflict)

		return
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

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	screenshot, err := util.GetScreenshot(expName, name, "215")
	if err != nil {
		plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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
}

// StopVM - POST /experiments/{exp}/vms/{name}/stop.
//
//nolint:funlen // handler
func StopVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
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
		http.Error(w, err.Error(), http.StatusConflict)

		return
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

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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
}

// RestartVM - GET /experiments/{exp}/vms/{name}/restart.
//
//nolint:funlen // handler
func RestartVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
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
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/restart", "update", fullName),
		bt.NewResource("experiment/vm", name, "restarting"),
		nil,
	)

	if err := vm.Restart(expName, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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

	screenshot, err := util.GetScreenshot(expName, name, defaultScreenshotSize)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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
}

// ShutdownVM - GET /experiments/{exp}/vms/{name}/shutdown.
func ShutdownVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
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
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	if err := vm.Shutdown(expName, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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

	v.Running = false

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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
}

// ResetVM - GET /experiments/{exp}/vms/{name}/reset.
func ResetVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
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
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	if err := vm.ResetDiskState(expName, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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
}
