package web

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/vm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// DeleteVMSnapshot - DELETE /experiments/{exp}/vms/{name}/snapshots/{snapshot}.
func DeleteVMSnapshot(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
		snap     = vars["snapshot"]
	)

	deleted := audit.Fields{Experiment: exp, VM: name, File: snap}

	if !role.Allowed("vms/snapshots", "delete", fullName) {
		audit.Denied(r, audit.FileDelete, "deleting VM snapshot not allowed", deleted)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if _, err := vmTarget(exp, name, false); err != nil {
		return err
	}

	files, err := vm.DeleteSnapshot(ctx, exp, name, snap)
	for _, f := range files {
		revokeExperimentFileGrants(exp, f)
	}

	if err != nil {
		if requestCanceled(r, err) {
			return nil
		}

		audit.Failure(r, audit.FileDelete, "deleting VM snapshot failed", deleted, err)

		return snapshotDeleteError(exp, name, err)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "list", fullName),
		bt.NewResource("experiment/vm/snapshot", fullName, "delete"),
		nil,
	)

	audit.Success(r, audit.FileDelete, "vm snapshot deleted", deleted)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// snapshotDeleteError is the answer to err, from vm.DeleteSnapshot for the VM name of the experiment expName: 404 for
// a snapshot the VM does not have, 409 for a snapshot that a running VM uses, the status of an experiment file error,
// else 500.
func snapshotDeleteError(expName, name string, err error) error {
	switch {
	case errors.Is(err, vm.ErrSnapshotNotFound):
		return weberror.New(http.StatusNotFound, fmt.Sprintf("snapshot not found for VM %s in experiment %s", name, expName))
	case errors.Is(err, vm.ErrSnapshotInUse):
		return weberror.New(http.StatusConflict, err.Error())
	}

	status := fileErrorStatus(err)
	if status == http.StatusInternalServerError {
		plog.Error(plog.TypeSystem, "deleting VM snapshot", "exp", expName, "vm", name, "err", err)
	}

	return weberror.New(status, err.Error())
}
