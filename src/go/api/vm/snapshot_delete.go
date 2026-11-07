package vm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"phenix/api/experiment"
	"phenix/util/mm"
)

// ErrSnapshotInUse is the error of DeleteSnapshot for a snapshot whose disk a VM of the running experiment uses, as it
// does after a restore.
var ErrSnapshotInUse = errors.New("a running VM uses the disk of the snapshot")

// DeleteSnapshot deletes the snapshot snap of the VM vmName of the experiment expName: its memory state (.state) and
// its disk (.hdd) in the experiment's files directory, on every cluster node that has them. snap is a name that
// Snapshots returns; an extension is removed. It returns the names of the deleted files. The error wraps
// ErrSnapshotNotFound for a snapshot that the VM does not have, ErrSnapshotInUse, and the errors of
// experiment.DeleteFiles. Once ctx is done, the listing of the snapshots gives up.
func DeleteSnapshot(ctx context.Context, expName, vmName, snap string) ([]string, error) {
	snap, err := CheckDeleteSnapshot(ctx, expName, vmName, snap)
	if err != nil {
		return nil, err
	}

	files := []string{snap + ".state", snap + ".hdd"}

	deleted, refused, err := experiment.DeleteFiles(ctx, expName, files)
	if err != nil {
		return deleted, fmt.Errorf("deleting snapshot %s: %w", snap, err)
	}

	var errs []error

	for _, f := range files {
		if err := refused[f]; err != nil {
			errs = append(errs, err)
		}
	}

	if err := errors.Join(errs...); err != nil {
		return deleted, fmt.Errorf("deleting snapshot %s: %w", snap, err)
	}

	return deleted, nil
}

// CheckDeleteSnapshot checks that DeleteSnapshot can delete the snapshot snap of the VM vmName of the experiment
// expName, and deletes nothing. It returns the snapshot name without its extension, and the errors of DeleteSnapshot
// before it deletes a file.
func CheckDeleteSnapshot(ctx context.Context, expName, vmName, snap string) (string, error) {
	snap = strings.TrimSuffix(snap, filepath.Ext(snap))

	snapshots, err := Snapshots(ctx, expName, vmName)
	if err != nil {
		return "", fmt.Errorf("getting list of snapshots for VM: %w", err)
	}

	if !slices.Contains(snapshots, snap) {
		return "", fmt.Errorf("snapshot %s of VM %s: %w", snap, vmName, ErrSnapshotNotFound)
	}

	if err := checkSnapshotUnused(ctx, expName, snap); err != nil {
		return "", err
	}

	return snap, nil
}

// checkSnapshotUnused returns an error wrapping ErrSnapshotInUse when a VM of the running experiment expName runs
// from the disk of the snapshot snap, as Restore configures it. A stopped experiment runs no VM.
func checkSnapshotUnused(ctx context.Context, expName, snap string) error {
	if !experiment.Running(expName) {
		return nil
	}

	disk := expName + "/files/" + snap + ".hdd"

	for _, v := range mm.GetVMInfo(mm.NS(expName), mm.Context(ctx)) {
		// the disk is the backing file when minimega runs the VM from a snapshot of it
		if v.Disk == disk || strings.HasSuffix(v.Disk, "/"+disk) {
			return fmt.Errorf("snapshot %s: VM %s: %w", snap, v.Name, ErrSnapshotInUse)
		}
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("listing the VMs of experiment %s: %w", expName, err)
	}

	return nil
}
