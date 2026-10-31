package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/vm"
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

const percentDivisor = 100

// GetVMSnapshots - GET /experiments/{exp}/vms/{name}/snapshots.
func GetVMSnapshots(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/snapshots", "list", fmt.Sprintf("%s/%s", exp, name)) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing snapshots for VM not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	snapshots, err := vm.Snapshots(ctx, exp, name)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"getting list of snapshots for VM",
			"exp",
			exp,
			"vm",
			name,
			"err",
			err,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err := marshaler.Marshal(&proto.SnapshotList{Snapshots: snapshots})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// SnapshotVM - POST /experiments/{exp}/vms/{name}/snapshots.
//
//nolint:funlen // handler
func SnapshotVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/snapshots", "create", fullName) {
		audit.Denied(r, audit.VMSnapshot, "snapshotting VM not allowed", audit.Fields{Experiment: exp, VM: name})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.SnapshotRequest

	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if err := cache.LockVMForSnapshotting(exp, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			exp,
			"vm",
			name,
			"action",
			"snapshotting",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(exp, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "creating"),
		nil,
	)

	status := make(chan string)
	// vm.Snapshot reports no end when it fails, so the progress goroutine ends when the handler does
	defer close(status)

	go func() {
		for s := range status {
			progress, err := strconv.ParseFloat(s, 64)
			if err == nil {
				plog.Debug(plog.TypeSystem, "snapshot percent complete", statusKeyPercent, progress)

				status := map[string]any{
					statusKeyPercent: progress / percentDivisor,
				}

				marshalled, _ := json.Marshal(status)

				broker.Broadcast(
					bt.NewRequestPolicy("vms/snapshots", "create", fullName),
					bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "progress"),
					marshalled,
				)
			}
		}
	}()

	cb := func(s string) { status <- s }
	snapshotted := audit.Fields{Experiment: exp, VM: name, Target: req.GetFilename()}

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	if err := vm.Snapshot(change, exp, name, req.GetFilename(), cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/snapshots", "create", fullName),
			bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "errorCreating"),
			nil,
		)

		// one that gave up before its first change left the VM as it was
		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "snapshotting VM", "exp", exp, "vm", name, "err", err)
		audit.Failure(r, audit.VMSnapshot, "snapshotting VM failed", snapshotted, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "create"),
		nil,
	)

	audit.Success(r, audit.VMSnapshot, "vm snapshotted", snapshotted)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// RestoreVM - POST /experiments/{exp}/vms/{name}/snapshots/{snapshot}.
func RestoreVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
		snap     = vars["snapshot"]
	)

	restored := audit.Fields{Experiment: exp, VM: name, File: snap}

	if !role.Allowed("vms/snapshots", "update", fullName) {
		audit.Denied(r, audit.VMRestore, "restoring VM not allowed", restored)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := cache.LockVMForRestoring(exp, name)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			exp,
			"vm",
			name,
			"action",
			"restoring",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(exp, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", fmt.Sprintf("%s/%s", exp, name), "restoring"),
		nil,
	)

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	err = vm.Restore(change, exp, name, snap)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/snapshots", "create", fullName),
			bt.NewResource(
				"experiment/vm/snapshot",
				fmt.Sprintf("%s/%s", exp, name),
				"errorRestoring",
			),
			nil,
		)

		// one that gave up before its first change left the VM as it was
		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "restoring VM", "exp", exp, "vm", name, "err", err)
		audit.Failure(r, audit.VMRestore, "restoring VM failed", restored, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "restore"),
		nil,
	)

	audit.Success(r, audit.VMRestore, "vm restored", restored)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// CommitVM - POST /experiments/{exp}/vms/{name}/commit.
//
//nolint:funlen // handler
func CommitVM(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/commit", "create", fullName) {
		audit.Denied(r, audit.VMCommit, "committing VM not allowed", audit.Fields{Experiment: expName, VM: name})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var filename string

	// If user provided body to this request, expect it to specify the
	// filename to use for the commit. If no body was provided, pass an
	// empty string to `api.CommitToDisk` to let it create a copy based on
	// the existing file name for the base image.
	if len(body) != 0 {
		var req proto.BackingImageRequest

		err = unmarshaler.Unmarshal(body, &req)
		if err != nil {
			plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

			return weberror.New(http.StatusBadRequest, err.Error())
		}

		if req.GetFilename() == "" {
			plog.Error(plog.TypeSystem, "missing filename for commit")

			return weberror.New(http.StatusBadRequest, "missing 'filename' key")
		}

		filename = req.GetFilename()
	}

	if err := cache.LockVMForCommitting(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"committing",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(expName, name)

	if filename == "" {
		/*
			if filename, err = api.GetNewDiskName(exp, name); err != nil {
				log.Error("failure getting new disk name for commit")
				http.Error(w, "failure getting new disk name for commit", http.StatusInternalServerError)
				return
			}
		*/

		return weberror.New(http.StatusBadRequest, "must provide new disk name for commit")
	}

	payload := &proto.BackingImageResponse{Disk: filename} //nolint:exhaustruct // partial initialization
	body, _ = marshaler.Marshal(payload)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/commit", "create", fullName),
		bt.NewResource("experiment/vm/commit", expName+"/"+name, "committing"),
		body,
	)

	status := make(chan float64)

	go func() {
		for s := range status {
			plog.Debug(plog.TypeSystem, "VM commit percent complete", statusKeyPercent, s)

			status := map[string]any{
				statusKeyPercent: s,
			}

			marshalled, _ := json.Marshal(status)

			broker.Broadcast(
				bt.NewRequestPolicy("vms/commit", "create", fullName),
				bt.NewResource("experiment/vm/commit", expName+"/"+name, "progress"),
				marshalled,
			)
		}
	}()

	cb := func(s float64) { status <- s }
	committed := audit.Fields{Experiment: expName, VM: name, Target: filename}

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	if _, err = vm.CommitToDisk(change, expName, name, filename, cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/commit", "create", fullName),
			bt.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		// one that gave up before its first change left the VM as it was
		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "committing VM", "exp", expName, "vm", name, "err", err)
		audit.Failure(r, audit.VMCommit, "committing VM failed", committed, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	// committed, whether or not the VM can be read back for the response
	audit.Success(r, audit.VMCommit, "vm committed", committed)

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/commit", "create", fullName),
			bt.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	// broadcast to every client, so read even once this request's client left
	v, err := vm.GetFor(context.WithoutCancel(ctx), exp, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/commit", "create", fullName),
			bt.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	payload.Vm = util.VMToProtobuf(expName, *v, exp.Spec.Topology())
	body, _ = marshaler.Marshal(payload)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/commit", "create", fmt.Sprintf("%s/%s", expName, name)),
		bt.NewResource("experiment/vm/commit", expName+"/"+name, "commit"),
		body,
	)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// CreateVMMemorySnapshot - POST /experiments/{exp}/vms/{name}/memorySnapshot.
//
//nolint:funlen // handler
func CreateVMMemorySnapshot(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/memorySnapshot", "create", fullName) {
		audit.Denied(r, audit.VMMemorySnapshot, "capturing memory snapshot of VM not allowed", audit.Fields{
			Experiment: exp, VM: name,
		})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var filename string

	// If user provided body to this request, expect it to specify the
	// filename to use for capturing a memory snapshot.
	if len(body) != 0 {
		var req proto.MemorySnapshotRequest

		err = unmarshaler.Unmarshal(body, &req)
		if err != nil {
			plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

			return weberror.New(http.StatusBadRequest, err.Error())
		}

		if req.GetFilename() == "" {
			plog.Error(plog.TypeSystem, "missing filename for memory snapshot")

			return weberror.New(http.StatusBadRequest, "missing 'filename' key")
		}

		filename = req.GetFilename()
	}

	if err := cache.LockVMForMemorySnapshotting(exp, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			exp,
			"vm",
			name,
			"action",
			"memory snapshotting",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockVM(exp, name)

	if filename == "" {
		return weberror.New(http.StatusBadRequest, "must provide new disk name for memory snapshot")
	}

	payload := &proto.MemorySnapshotResponse{Disk: filename} //nolint:exhaustruct // partial initialization
	body, _ = marshaler.Marshal(payload)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
		bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "committing"),
		body,
	)

	status := make(chan string)

	go func() {
		defer close(status)

		for {
			s := <-status
			if s == "failed" || s == "completed" {
				return
			}

			progress, err := strconv.ParseFloat(s, 64)
			if err == nil {
				status := map[string]any{
					statusKeyPercent: progress,
				}

				plog.Info(plog.TypeSystem, "memory snapshot percent complete", statusKeyPercent, progress)

				marshalled, _ := json.Marshal(status)

				broker.Broadcast(
					bt.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
					bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "progress"),
					marshalled,
				)
			}
		}
	}()

	cb := func(s string) { status <- s }
	snapshotted := audit.Fields{Experiment: exp, VM: name, Target: filename}

	change, cancel := changeContext(r, vmChangeTimeout)
	defer cancel()

	if _, err = vm.MemorySnapshot(change, exp, name, filename, cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
			bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "errorCommitting"),
			nil,
		)

		// one that gave up before its first change left the VM as it was
		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "memory snapshot for VM", "exp", exp, "vm", name, "err", err)
		audit.Failure(r, audit.VMMemorySnapshot, "memory snapshot of VM failed", snapshotted, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	marshalled, _ := json.Marshal(util.WithRoot("disk", filename))

	broker.Broadcast(
		bt.NewRequestPolicy("vms/memorySnapshot", "create", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "commit"),
		marshalled,
	)

	audit.Success(r, audit.VMMemorySnapshot, "vm memory snapshot created", snapshotted)
	w.WriteHeader(http.StatusNoContent)

	return nil
}
