package vm

import (
	"context"
	"fmt"
	"strings"

	"phenix/api/experiment"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
)

// CheckTarget reads the experiment expName and checks that its topology has the VM vmName and, if running is set,
// that the experiment runs. Its error wraps the store's error when the experiment cannot be read, mm.ErrVMNotFound
// when the topology does not have the VM, and ErrExperimentNotRunning when the experiment must run and does not.
func CheckTarget(expName, vmName string, running bool) (*types.Experiment, error) {
	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	if topo := exp.Spec.Topology(); topo == nil || topo.FindNodeByName(vmName) == nil {
		return nil, &mm.VMNotFoundError{Err: fmt.Errorf("VM %s not found in experiment %s", vmName, expName)}
	}

	if running && !exp.Running() {
		return nil, fmt.Errorf("experiment %s: %w", expName, ErrExperimentNotRunning)
	}

	return exp, nil
}

// checkSnapshotFlag returns an error wrapping ErrSnapshotDisabled when row, the VM's `vm info` row, says that minimega
// writes to the VM's disk in place.
func checkSnapshotFlag(vmName string, row map[string]string) error {
	if row["snapshot"] == "false" {
		return fmt.Errorf("VM %s: %w", vmName, ErrSnapshotDisabled)
	}

	return nil
}

// checkOneDisk returns an error wrapping ErrMultipleDisks when row, the VM's `vm info` row, lists more than one disk.
func checkOneDisk(vmName string, row map[string]string) error {
	// minimega separates the disks with spaces (see mm.GetVMInfo)
	if n := len(strings.Fields(row["disks"])); n > 1 {
		return fmt.Errorf("VM %s has %d disks: %w", vmName, n, ErrMultipleDisks)
	}

	return nil
}

// checkVMDisks returns an error wrapping ErrMultipleDisks when minimega lists more than one disk for the VM vmName of
// the experiment expName, an error wrapping ErrSnapshotDisabled when its snapshot flag is not set, or the error of the
// listing.
func checkVMDisks(ctx context.Context, expName, vmName string) error {
	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"disks", "snapshot"}
	cmd.Filters = []string{"name=" + vmName}

	rows, err := mmcli.RunTabularCheckedContext(ctx, cmd, nil)
	if err != nil {
		return fmt.Errorf("getting disks of VM %s: %w", vmName, err)
	}

	if len(rows) == 0 {
		return nil
	}

	if err := checkSnapshotFlag(vmName, rows[0]); err != nil {
		return err
	}

	return checkOneDisk(vmName, rows[0])
}
