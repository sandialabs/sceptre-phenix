package vm

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"phenix/api/experiment"
	ifaces "phenix/types/interfaces"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/firecracker"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
)

// ErrNotSupported is wrapped by the error of a VM operation that does not apply
// to the VM's type.
var ErrNotSupported = errors.New("not supported")

const (
	// snapshotSidecarExt is the extension of the file that vm save writes next
	// to the snapshot of a firecracker VM. A snapshot without it is a KVM
	// snapshot.
	snapshotSidecarExt = "json"
	taskPollInterval   = 1 * time.Second
	percent            = 100
)

// NotSupportedError returns the error for an operation that a firecracker VM
// does not support. It wraps ErrNotSupported.
func NotSupportedError(op string) error {
	return fmt.Errorf("%s is %w for firecracker VMs", op, ErrNotSupported)
}

// NodeVMType returns the minimega VM type of a topology node: kvm when the node
// does not set one.
func NodeVMType(node ifaces.NodeSpec) string {
	return cmp.Or(node.General().VMType(), mm.DefaultVMType)
}

// lookupVMType returns the minimega VM type of the topology node vmName. It
// reads the experiment from the store and sends no minimega command.
func lookupVMType(expName, vmName string) (string, error) {
	exp, err := experiment.Get(expName)
	if err != nil {
		return "", fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	node := exp.Spec.Topology().FindNodeByName(vmName)
	if node == nil {
		return mm.DefaultVMType, nil
	}

	return NodeVMType(node), nil
}

// refuseFirecracker returns the not-supported error for op when vmName is a
// firecracker VM.
func refuseFirecracker(expName, vmName, op string) error {
	vmType, err := lookupVMType(expName, vmName)
	if err != nil {
		return err
	}

	if vmType == firecracker.VMType {
		return NotSupportedError(op)
	}

	return nil
}

// waitForState polls the state of a VM once a second until it is state, and
// reports false when timeout passes first.
func waitForState(expName, vmName, state string, timeout time.Duration) bool {
	after := time.After(timeout)

	for {
		select {
		case <-after:
			return false
		default:
			time.Sleep(1 * time.Second)

			current, _ := mm.GetVMState(mm.NS(expName), mm.VMName(vmName))
			if current == state {
				return true
			}
		}
	}
}

// waitForTask polls a minimega command that, with no arguments, shows the
// progress of a VM task in the table format of vm save (vm export-disk, vm
// memory-dump). It gives the percent complete to progress and returns when the
// task of the VM is completed.
func waitForTask(expName, vmName, command string, progress func(float64)) error {
	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = command
	cmd.Columns = []string{"name", "status", "complete (%)"}
	cmd.Filters = []string{"name=" + vmName}

	for {
		time.Sleep(taskPollInterval)

		rows := mmcli.RunTabular(cmd)
		if len(rows) == 0 {
			return fmt.Errorf("no %s status for VM %s", command, vmName)
		}

		status := rows[0]["status"]

		if status == statusCompleted {
			return nil
		}

		if strings.Contains(status, "fail") || strings.Contains(status, "error") {
			return fmt.Errorf("%s for VM %s: %s", command, vmName, status)
		}

		done, _ := strconv.ParseFloat(rows[0]["complete (%)"], 64)
		progress(done)
	}
}

// restartFirecracker cold restarts a firecracker VM, which has no reset: it
// kills the VM and starts it again from QUIT.
func restartFirecracker(expName, vmName string) error {
	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = "vm kill " + vmName

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("killing VM %s in experiment %s: %w", vmName, expName, err)
	}

	if !waitForState(expName, vmName, vmStateQuit, shutdownTimeout) {
		return fmt.Errorf("VM %s in experiment %s did not stop within %v", vmName, expName, shutdownTimeout)
	}

	if err := mm.StartVM(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return fmt.Errorf("starting VM %s in experiment %s: %w", vmName, expName, err)
	}

	return nil
}

// resetFirecrackerDisk makes the disk of a firecracker VM again from its image
// with minimega's vm reset-disk, which needs the VM in QUIT. status is the VM's
// vm info row.
func resetFirecrackerDisk(expName, vmName string, status map[string]string) error {
	if status["snapshot"] == "false" {
		return fmt.Errorf("snapshot flag for %s was not set", vmName)
	}

	if status["state"] != vmStateQuit {
		if status["state"] == vmStateRunning {
			err := StopCaptures(expName, vmName)
			if err != nil && !errors.Is(err, ErrNoCaptures) {
				return fmt.Errorf("stopping captures for VM %s in experiment %s: %w", vmName, expName, err)
			}
		}

		cmd := mmcli.NewNamespacedCommand(expName)
		cmd.Command = "vm kill " + vmName

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("killing VM %s in experiment %s: %w", vmName, expName, err)
		}

		if !waitForState(expName, vmName, vmStateQuit, shutdownTimeout) {
			return fmt.Errorf("VM %s in experiment %s did not stop within %v", vmName, expName, shutdownTimeout)
		}
	}

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = "vm reset-disk " + vmName

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("resetting disk for VM %s in experiment %s: %w", vmName, expName, err)
	}

	if err := mm.StartVM(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return fmt.Errorf("starting VM %s in experiment %s: %w", vmName, expName, err)
	}

	return nil
}

// exportFirecrackerDisk writes the disk of a firecracker VM to out as a qcow2
// image with minimega's vm export-disk, then makes the image available on
// every cluster node. The VM keeps its state. It returns out relative to the
// phenix images directory.
func exportFirecrackerDisk(expName, vmName, out string, cb func(float64)) (string, error) {
	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"host", "name"}
	cmd.Filters = []string{"name=" + vmName}

	status := mmcli.RunTabular(cmd)
	if len(status) == 0 {
		return "", errors.New("vm not found")
	}

	cmd = mmcli.NewNamespacedCommand(expName)
	cmd.Command = fmt.Sprintf("vm export-disk %s %s qcow2", vmName, out)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return "", fmt.Errorf("starting disk export for VM %s: %w", vmName, err)
	}

	err := waitForTask(expName, vmName, "vm export-disk", func(done float64) {
		if cb != nil {
			cb(done / percent)
		}
	})
	if err != nil {
		return "", fmt.Errorf("exporting disk for VM %s: %w", vmName, err)
	}

	rel, _ := filepath.Rel(common.PhenixBase+"/images/", out)

	if !mm.IsHeadnode(status[0]["host"]) {
		headnode, _ := os.Hostname()

		if err := file.CopyFile(rel, headnode, nil); err != nil {
			return "", fmt.Errorf("pulling exported disk to headnode: %w", err)
		}
	}

	if err := file.SyncFile(rel, nil); err != nil {
		return "", fmt.Errorf("syncing new backing image across cluster: %w", err)
	}

	return rel, nil
}

// dumpFirecrackerMemory writes an ELF dump of the memory of a firecracker VM
// to out with minimega's vm memory-dump.
func dumpFirecrackerMemory(expName, vmName, out string, cb func(string)) error {
	report := func(s string) {
		if cb != nil {
			cb(s)
		}
	}

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = fmt.Sprintf("vm memory-dump %s %s elf", vmName, out)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("starting memory snapshot for VM %s: %w", vmName, err)
	}

	err := waitForTask(expName, vmName, "vm memory-dump", func(done float64) {
		report(strconv.FormatFloat(done/percent, 'f', -1, 64))
	})
	if err != nil {
		report("failed")

		return fmt.Errorf("creating memory snapshot for VM %s: %w", vmName, err)
	}

	report(statusCompleted)

	return nil
}

// checkSnapshotType refuses to restore a snapshot on a VM of the other type. A
// firecracker snapshot has a sidecar file and a KVM snapshot does not.
func checkSnapshotType(expName, vmName, snap string, firecrackerVM bool) error {
	files, err := file.GetExperimentFiles(expName, "")
	if err != nil {
		return fmt.Errorf("getting experiment files: %w", err)
	}

	var (
		sidecar    = snap + "." + snapshotSidecarExt
		hasSidecar bool
	)

	for _, f := range files {
		if f.Path == sidecar {
			hasSidecar = true

			break
		}
	}

	switch {
	case firecrackerVM && !hasSidecar:
		return fmt.Errorf(
			"snapshot %s is a KVM snapshot: restoring it on firecracker VM %s is %w",
			snap, vmName, ErrNotSupported,
		)
	case !firecrackerVM && hasSidecar:
		return fmt.Errorf(
			"snapshot %s is a firecracker snapshot: restoring it on VM %s is %w",
			snap, vmName, ErrNotSupported,
		)
	}

	return nil
}
