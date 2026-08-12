package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/plog"
)

// MountTimeout is how long to wait for a mount/unmount C2 command to complete.
const MountTimeout = 5 * time.Second

func init() { //nolint:gochecknoinits // registering experiment lifecycle hooks
	// Make sure VM filesystem mounts don't outlive their experiment. Without
	// this, a forgotten mount can leave a stale FUSE mount on the headnode
	// that requires a reboot to clear once the backing VM is gone.
	experiment.RegisterHook("stop", func(_, expName string) { unmountExperiment(expName) })
	experiment.RegisterHook("delete", func(_, expName string) { unmountExperiment(expName) })
}

// Mount mounts the filesystem of the given running VM to hostPath. If
// hostPath is empty, the default phenix mount path
// (<mount-dir>/<experiment>/<vm-name>) is used instead. It returns the
// resolved host path the VM was mounted to, along with any errors encountered.
//
// This is the same implementation used by the `POST
// /experiments/{exp}/vms/{name}/mount` API endpoint, so behavior is
// consistent between the CLI and web UI.
func Mount(expName, vmName, hostPath string) (string, error) {
	if expName == "" {
		return "", errors.New("no experiment name provided")
	}

	if vmName == "" {
		return "", errors.New("no VM name provided")
	}

	details, err := Get(expName, vmName)
	if err != nil {
		return "", fmt.Errorf("getting VM details: %w", err)
	}

	if !details.Running {
		return "", errors.New("vm is not running")
	}

	if err := mm.IsC2ClientActive(
		mm.C2NS(expName),
		mm.C2VM(vmName),
		mm.C2IDClientsByUUID(),
		mm.C2Timeout(MountTimeout),
	); err != nil {
		return "", fmt.Errorf("vm's C2 agent (miniccc) is not reachable: %w", err)
	}

	if hostPath == "" {
		hostPath = mm.GetLocalMountPath(expName, vmName)
	}

	hostPath, err = filepath.Abs(hostPath)
	if err != nil {
		return "", fmt.Errorf("resolving mount path: %w", err)
	}

	if err := os.MkdirAll(hostPath, 0o750); err != nil {
		return "", fmt.Errorf("creating mount directory %s: %w", hostPath, err)
	}

	if err := MountFilesystem(expName, vmName); err != nil {
		return "", err
	}

	plog.Info(plog.TypeAction, "vm mounted", "exp", expName, "vm", vmName, "path", hostPath)

	return hostPath, nil
}

// Unmount unmounts the filesystem of the given VM. It returns any errors
// encountered while unmounting.
//
// This is the same implementation used by the `POST
// /experiments/{exp}/vms/{name}/unmount` API endpoint, so behavior is
// consistent between the CLI and web UI.
func Unmount(expName, vmName string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	if err := UnmountFilesystem(expName, vmName); err != nil {
		return err
	}

	plog.Info(plog.TypeAction, "vm unmounted", "exp", expName, "vm", vmName)

	return nil
}

// MountFilesystem issues the C2 command to mount a VM's filesystem, tolerating
// the case where it's already mounted. It is used by both the CLI (via Mount)
// and the `POST /experiments/{exp}/vms/{name}/mount` API endpoint.
func MountFilesystem(expName, vmName string) error {
	_, err := mm.ExecC2Command(
		mm.C2NS(expName),
		mm.C2VM(vmName),
		mm.C2Mount(),
		mm.C2IDClientsByUUID(),
		mm.C2Timeout(MountTimeout),
	)

	if err != nil && !strings.Contains(err.Error(), "already connected") {
		return fmt.Errorf("mounting VM filesystem: %w", err)
	}

	return nil
}

// UnmountFilesystem issues the C2 command to unmount a VM's filesystem. It is
// used by both the CLI (via Unmount) and the `POST
// /experiments/{exp}/vms/{name}/unmount` API endpoint.
func UnmountFilesystem(expName, vmName string) error {
	_, err := mm.ExecC2Command(
		mm.C2NS(expName),
		mm.C2VM(vmName),
		mm.C2Unmount(),
		mm.C2Timeout(MountTimeout),
		mm.C2SkipActiveClientCheck(true),
	)
	if err != nil {
		return fmt.Errorf("unmounting VM filesystem: %w", err)
	}

	return nil
}

// unmountExperiment unmounts any VM filesystems still mounted for the given
// experiment and removes the experiment's mount directory. Errors are logged
// but not returned since this is invoked from experiment lifecycle hooks.
func unmountExperiment(expName string) {
	vms, err := List(expName)
	if err != nil {
		plog.Warn(plog.TypeSystem, "listing VMs to unmount", "exp", expName, "err", err)
	}

	for _, v := range vms {
		if err := UnmountFilesystem(expName, v.Name); err != nil {
			plog.Warn(
				plog.TypeSystem,
				"unmounting vm during experiment cleanup",
				"exp",
				expName,
				"vm",
				v.Name,
				"err",
				err,
			)
		}
	}

	mountDir := mm.GetLocalMountPath(expName, "")

	if _, err := os.Stat(mountDir); err != nil {
		return
	}

	if err := os.RemoveAll(mountDir); err != nil {
		plog.Warn(
			plog.TypeSystem,
			"removing experiment mount directory",
			"exp",
			expName,
			"path",
			mountDir,
			"err",
			err,
		)
	}
}
