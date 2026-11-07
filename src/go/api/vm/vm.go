package vm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hashicorp/go-multierror"
	"golang.org/x/sync/errgroup"

	"phenix/api/disk"
	"phenix/api/experiment"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

const (
	vmStateQuit          = "QUIT"
	vmStateRunning       = "RUNNING"
	vmInfoCmd            = "vm info"
	statusCompleted      = "completed"
	statusFailed         = "failed"
	shutdownTimeout      = 30 * time.Second
	copyProgressFactor   = 0.8
	commitWait           = 2 * time.Second
	commitProgressBase   = 0.8
	commitProgressFactor = 0.2
)

var vlanAliasRegex = regexp.MustCompile(`(.*) \(\d*\)`)

func Count(expName string) (int, error) {
	if expName == "" {
		return 0, errors.New("no experiment name provided")
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return 0, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	return len(exp.Spec.Topology().Nodes()), nil
}

// List collects VMs, combining topology settings with running VM details if the
// experiment is running. It returns a slice of VM structs and any errors
// encountered while gathering them. Its minimega commands give up once ctx is
// done (see mm.Context), leaving the running VMs' details out.
func List(ctx context.Context, expName string) ([]mm.VM, error) {
	if expName == "" {
		return nil, errors.New("no experiment name provided")
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	return ListFor(ctx, exp), nil
}

// ListFor is List for an experiment the caller already read from the store, so
// the experiment is not read again.
func ListFor(ctx context.Context, exp *types.Experiment) []mm.VM {
	vms, _ := ListForObserved(ctx, exp)

	return vms
}

// ListForObserved is ListFor, also reporting when minimega was read (Unobserved for a stopped
// experiment). Unless the observation is Known, the VMs minimega missed are listed as configured,
// with no state; a listing whose commands gave up because ctx is done carries ctx's error.
func ListForObserved(ctx context.Context, exp *types.Experiment) ([]mm.VM, freshness.Observation) {
	if !exp.Running() {
		return listVMs(*exp, nil, false), freshness.Unobserved()
	}

	vms, obs := mm.GetVMInfoObserved(mm.NS(exp.Metadata.Name), mm.Context(ctx))

	running := make(map[string]mm.VM, len(vms))

	for _, vm := range vms {
		running[vm.Name] = vm
	}

	return listVMs(*exp, running, obs.Known), obs
}

// ListConfigured lists the experiment's VMs as its topology configures them,
// without asking minimega for their state, for when minimega is busy.
func ListConfigured(exp types.Experiment) []mm.VM {
	return listVMs(exp, nil, false)
}

// listVMs combines the topology's VMs with running, minimega's details for a
// running experiment's VMs. When complete, running holds every VM minimega
// runs, so a bootable VM it lacks was not deployed and is left out.
func listVMs(exp types.Experiment, running map[string]mm.VM, complete bool) []mm.VM {
	var vms []mm.VM

	for idx, node := range exp.Spec.Topology().Nodes() {
		vm, ifaceNameByVLAN := nodeVM(exp, idx, node)

		details, exists := running[vm.Name]
		if complete && exp.Running() && !exp.DryRun() && !node.External() && !vm.DoNotBoot && !exists {
			continue
		}

		switch {
		case node.External():
			vm.State = "EXTERNAL"
		case exists:
			mergeRunning(&vm, details, ifaceNameByVLAN)
		default:
			vm.Host = exp.Spec.Schedules()[vm.Name]
		}

		vms = append(vms, vm)
	}

	return vms
}

// nodeVM is the VM the topology configures for its node at index idx, and
// the name of its interface on each of its VLANs.
func nodeVM(exp types.Experiment, idx int, node ifaces.NodeSpec) (mm.VM, map[string]string) {
	var (
		disk            string
		dnb             bool
		snapshot        bool
		injectPartition int
	)

	if drives := node.Hardware().Drives(); len(drives) > 0 {
		disk = mm.GetMMFullPath(drives[0].Image())
		injectPartition = injectPartitionOf(drives[0])
	}

	if node.General().DoNotBoot() != nil {
		dnb = *node.General().DoNotBoot()
	}

	if node.General().Snapshot() != nil {
		snapshot = *node.General().Snapshot()
	}

	vm := mm.VM{ //nolint:exhaustruct // partial initialization
		ID:              idx,
		Name:            node.General().Hostname(),
		Description:     node.General().Description(),
		Experiment:      exp.Spec.ExperimentName(),
		CPUs:            node.Hardware().VCPU(),
		RAM:             node.Hardware().Memory(),
		Disk:            disk,
		InjectPartition: injectPartition,
		Interfaces:      make(map[string]string),
		DoNotBoot:       dnb,
		Type:            node.Type(),
		OSType:          node.Hardware().OSType(),
		Snapshot:        snapshot,
		Tags:            node.Labels(),
	}

	ifaceNameByVLAN := make(map[string]string)

	for _, iface := range node.Network().Interfaces() {
		// IPv4[i] is the address on Networks[i], so an interface without a VLAN has neither
		if iface.VLAN() != "" { // might be empty for external nodes
			vm.IPv4 = append(vm.IPv4, iface.Address()) // empty for DHCP
			vm.Networks = append(vm.Networks, iface.VLAN())
			vm.IfaceNames = append(vm.IfaceNames, iface.Name())
			vm.Interfaces[iface.VLAN()] = iface.Address() // empty for DHCP
			ifaceNameByVLAN[iface.VLAN()] = iface.Name()
		}
	}

	return vm, ifaceNameByVLAN
}

// mergeRunning overlays minimega's details of a running VM on the VM its
// topology configures; ifaceNameByVLAN names its interface on each VLAN.
func mergeRunning(vm *mm.VM, details mm.VM, ifaceNameByVLAN map[string]string) {
	vm.Host = details.Host
	vm.State = details.State
	vm.Running = details.Running
	vm.Networks = details.Networks
	vm.Taps = details.Taps
	vm.IPv4 = details.IPv4
	vm.Captures = details.Captures
	vm.CdRom = details.CdRom
	vm.Tags = details.Tags
	vm.Uptime = details.Uptime
	vm.CPUs = details.CPUs
	vm.RAM = details.RAM
	vm.Disk = details.Disk
	vm.CCActive = details.CCActive

	// `vm.IPv4` could be nil/empty if minimega isn't reporting any IPs for it
	if len(vm.IPv4) == 0 {
		vm.IPv4 = make([]string, len(details.Networks))
	}

	// minimega is the source of truth for interface ordering in a running
	// experiment, so rebuild `vm.IfaceNames` to match `details.Networks`
	// instead of leaving it in the (possibly different) order the interfaces
	// were declared in the topology.
	vm.IfaceNames = make([]string, len(details.Networks))

	// Since we get the IP from the experiment config, but the network name
	// from minimega (to preserve iface to network ordering), make sure the
	// ordering of IPs matches the ordering of networks. We could just use a
	// map here, but then the iface to network ordering that minimega ensures
	// would be lost.
	for i, nw := range details.Networks {
		// At this point, `nw` will look something like `EXP_1 (101)`. In the
		// experiment config, we just have `EXP_1` so we need to use that
		// portion from minimega as the `Interfaces`/`ifaceNameByVLAN` map key.
		match := vlanAliasRegex.FindStringSubmatch(nw)

		// If it's set here, we got it from minimega, which is the source of truth
		// for running experiments.
		if vm.IPv4[i] == "" {
			if match != nil {
				vm.IPv4[i] = vm.Interfaces[match[1]]
			} else {
				vm.IPv4[i] = "n/a"
			}
		}

		if match != nil {
			vm.IfaceNames[i] = ifaceNameByVLAN[match[1]]
		}
	}
}

// Get retrieves the VM with the given name from the experiment with the given
// name. If the experiment is running, topology VM settings are combined with
// running VM details. It returns a pointer to a VM struct, and any errors
// encountered while retrieving the VM, among them ctx's error once ctx is done
// (see mm.Context).
func Get(ctx context.Context, expName, vmName string) (*mm.VM, error) {
	if expName == "" {
		return nil, errors.New("no experiment name provided")
	}

	if vmName == "" {
		return nil, errors.New("no VM name provided")
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	return GetFor(ctx, exp, vmName)
}

// GetFor is Get for an experiment the caller already read from the store, so
// the experiment is not read again.
func GetFor(ctx context.Context, exp *types.Experiment, vmName string) (*mm.VM, error) {
	if vmName == "" {
		return nil, errors.New("no VM name provided")
	}

	expName := exp.Metadata.Name

	var (
		vm              *mm.VM
		ifaceNameByVLAN map[string]string
	)

	for idx, node := range exp.Spec.Topology().Nodes() {
		if node.General().Hostname() != vmName {
			continue
		}

		var configured mm.VM

		configured, ifaceNameByVLAN = nodeVM(*exp, idx, node)
		configured.Metadata = make(map[string]any)
		configured.Labels = node.Labels()
		configured.Annotations = node.Annotations()

		for _, app := range exp.Apps() {
			for _, h := range app.Hosts() {
				if h.Hostname() == configured.Name {
					configured.Metadata[app.Name()] = h.Metadata()
				}
			}
		}

		vm = &configured
	}

	if vm == nil {
		return nil, &mm.VMNotFoundError{Err: fmt.Errorf("vm %s not found in experiment %s", vmName, expName)}
	}

	if !exp.Running() {
		vm.Host = exp.Spec.Schedules()[vm.Name]

		return vm, nil
	}

	details := mm.GetVMInfo(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("getting details of VM %s: %w", vmName, err)
	}

	if len(details) != 1 {
		return vm, nil
	}

	mergeRunning(vm, details[0], ifaceNameByVLAN)

	return vm, nil
}

var (
	// ErrInvalidAnnotations wraps why Update rejected a VM's new annotations.
	ErrInvalidAnnotations = errors.New("invalid annotations")

	// ErrInvalidDisk wraps why Update, Redeploy or ChangeOpticalDisc rejected a VM's new disk.
	ErrInvalidDisk = errors.New("invalid disk")

	// ErrNoDiscPath is the error of ChangeOpticalDisc for an empty optical disc path.
	ErrNoDiscPath = errors.New("no optical disc path provided")

	// ErrRemoteDumpPath wraps why MemorySnapshot rejected the path of a memory dump of a VM on a node other than
	// the headnode: the path is not in the experiment's files directory, from which phenix pulls the dump.
	ErrRemoteDumpPath = errors.New("memory snapshot path not in the experiment files directory")

	// ErrVMNotRunning is the error of Snapshot and StartCapture for a VM that is not running.
	ErrVMNotRunning = errors.New("VM is not running")

	// ErrRunningUpdate is the error of Update for a change that it cannot make while the experiment runs.
	ErrRunningUpdate = errors.New(
		"while the experiment is running, you can update only interface connections, tags and annotations",
	)

	// ErrStoppedInterfaceUpdate is the error of Update for an interface change while the experiment is stopped.
	ErrStoppedInterfaceUpdate = errors.New(
		"you can update interface connections only while the experiment is running; " +
			"edit the topology of a stopped experiment",
	)

	// ErrSnapshotNotFound is the error of Restore for a snapshot that the VM does not have.
	ErrSnapshotNotFound = errors.New("snapshot does not exist on cluster")

	// ErrExperimentNotRunning is the error of CheckTarget for an experiment that must run and does not.
	ErrExperimentNotRunning = errors.New("experiment is not running")

	// ErrSnapshotDisabled is the error of Snapshot, Restore and CommitToDisk for a VM whose snapshot flag is not set: minimega
	// writes to its image in place, so the VM has no disk snapshot to save or commit.
	ErrSnapshotDisabled = errors.New("snapshot flag is not set")

	// ErrMultipleDisks is the error of Snapshot and Restore for a VM with more than one disk: a snapshot holds only
	// the first disk, so it cannot restore such a VM (see sandialabs/sceptre-phenix#145).
	ErrMultipleDisks = errors.New("snapshots support only VMs with one disk")
)

// updateFirstDrive sets the image and inject partition of the node's first drive that the update asks for.
func updateFirstDrive(node ifaces.NodeSpec, o updateOptions) error {
	if o.disk == "" && o.partition == nil {
		return nil
	}

	drives := node.Hardware().Drives()
	if len(drives) == 0 {
		return fmt.Errorf("updating the disk of VM %s: topology node has no drives", o.vm)
	}

	if o.disk != "" {
		drives[0].SetImage(o.disk)
	}

	if o.partition != nil {
		drives[0].SetInjectPartition(o.partition)
	}

	return nil
}

func Update(opts ...UpdateOption) error { //nolint:funlen,gocyclo,cyclop  // complex logic
	o := newUpdateOptions(opts...)

	if o.exp == "" || o.vm == "" {
		return errors.New("experiment or VM name not provided")
	}

	if err := validateDisk(o.disk); err != nil {
		return err
	}

	if o.tags != nil {
		if err := mm.ValidateTags(*o.tags); err != nil {
			return err
		}
	}

	if o.iface != nil && o.iface.vlan != "" {
		if err := mm.ValidateArg(o.iface.vlan); err != nil {
			return fmt.Errorf("VLAN: %w", err)
		}
	}

	exp, err := experiment.Get(o.exp)
	if err != nil {
		return fmt.Errorf("unable to get experiment %s: %w", o.exp, err)
	}

	vm := exp.Spec.Topology().FindNodeByName(o.vm)
	if vm == nil {
		return &mm.VMNotFoundError{Err: fmt.Errorf("unable to find VM %s in experiment %s", o.vm, o.exp)}
	}

	if o.annotations != nil {
		if err := experiment.ValidateNodeAnnotations(o.annotations); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidAnnotations, err)
		}
	}

	if o.tags != nil && o.appendTags {
		if err := appendLabels(*o.tags, vm.Labels()); err != nil {
			return err
		}
	}

	running := experiment.Running(o.exp)

	// The only settings that can be updated while an experiment is running are
	// the VLAN an interface is connected to, the VM's tags, and its annotations,
	// which apps read when the experiment next starts
	if running {
		unsupported := o.cpu != 0 ||
			o.mem != 0 ||
			o.disk != "" ||
			o.partition != nil ||
			o.dnb != nil ||
			o.host != nil ||
			o.snapshot != nil

		if unsupported || (o.iface == nil && o.tags == nil && o.annotations == nil) {
			return ErrRunningUpdate
		}

		ctx := o.ctx

		if o.iface != nil {
			if o.iface.vlan == "" {
				err := disconnect(ctx, o.exp, o.vm, o.iface.index)
				if err != nil {
					return err
				}
			} else {
				err := connect(ctx, o.exp, o.vm, o.iface.index, o.iface.vlan)
				if err != nil {
					return err
				}
			}

			// the interface changed, so the tags and labels follow whatever ctx says
			ctx = context.WithoutCancel(ctx)
		}

		if o.tags != nil {
			// update both the live minimega tags and the experiment spec labels
			err := mm.SetVMTags(mm.NS(o.exp), mm.VMName(o.vm), mm.Tags(*o.tags), mm.Context(ctx))
			if err != nil {
				return err
			}

			vm.SetLabels(*o.tags)
		}

		if o.annotations != nil {
			vm.SetAnnotations(o.annotations)
		}

		if o.tags != nil || o.annotations != nil {
			err = experiment.Save(
				experiment.SaveWithName(o.exp),
				experiment.SaveWithSpec(exp.Spec),
			)
			if err != nil {
				return fmt.Errorf("unable to save experiment with updated VM: %w", err)
			}
		}

		return nil
	}

	if o.iface != nil {
		return ErrStoppedInterfaceUpdate
	}

	if o.cpu != 0 {
		vm.Hardware().SetVCPU(o.cpu)
	}

	if o.mem != 0 {
		vm.Hardware().SetMemory(o.mem)
	}

	if err := updateFirstDrive(vm, o); err != nil {
		return err
	}

	if o.dnb != nil {
		vm.General().SetDoNotBoot(*o.dnb)
	}

	if o.tags != nil {
		vm.SetLabels(*o.tags)
	}

	if o.annotations != nil {
		vm.SetAnnotations(o.annotations)
	}

	if o.host != nil {
		if *o.host == "" {
			delete(exp.Spec.Schedules(), o.vm)
		} else {
			_ = exp.Spec.ScheduleNode(o.vm, *o.host)
		}
	}

	if o.snapshot != nil {
		vm.General().SetSnapshot(*o.snapshot)
	}

	err = experiment.Save(experiment.SaveWithName(o.exp), experiment.SaveWithSpec(exp.Spec))
	if err != nil {
		return fmt.Errorf("unable to save experiment with updated VM: %w", err)
	}

	return nil
}

// appendLabels copies into tags each label it lacks, once every such label
// passes mm.ValidateTags, as they all go to minimega with the new tags.
func appendLabels(tags, labels map[string]string) error {
	kept := make(map[string]string)

	for k, v := range labels {
		if _, ok := tags[k]; !ok {
			kept[k] = v
		}
	}

	if err := mm.ValidateTags(kept); err != nil {
		return fmt.Errorf("existing label: %w", err)
	}

	maps.Copy(tags, kept)

	return nil
}

func Screenshot(expName, vmName, size string) ([]byte, error) {
	screenshot, err := mm.GetVMScreenshot(
		mm.NS(expName),
		mm.VMName(vmName),
		mm.ScreenshotSize(size),
	)
	if err != nil {
		return nil, fmt.Errorf("getting VM screenshot: %w", err)
	}

	return screenshot, nil
}

// Pause pauses a running VM with the given name in the experiment with the
// given name. It leaves the VM's packet captures running: minimega keeps a
// paused VM's taps, so a capture goes on when the VM resumes. Once ctx is
// done, a minimega command not yet sent gives up (see mm.Context).
func Pause(ctx context.Context, expName, vmName string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	if err := mm.StopVM(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx)); err != nil {
		return fmt.Errorf("pausing VM: %w", err)
	}

	return nil
}

// Restart restarts a running VM with the given name in the experiment with the given
// name. It returns any errors encountered while restarting the VM. Once ctx is
// done, a minimega command not yet sent gives up (see mm.Context).
func Restart(ctx context.Context, expName, vmName string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	state, err := mm.GetVMState(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	if err != nil {
		return fmt.Errorf("retrieving state for VM %s in experiment %s: %w", vmName, expName, err)
	}

	// Using "system_reset" on a VM that is in the "QUIT" state fails
	if state == vmStateQuit {
		return mm.StartVM(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	}

	cmd := mmcli.NewNamespacedCommand(expName)
	qmp := `{ "execute": "system_reset" }`
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, qmp)

	_, err = mmcli.SingleResponse(mmcli.RunContext(ctx, cmd))
	if err != nil {
		return fmt.Errorf("restarting VM %s: %w", vmName, err)
	}

	return nil
}

// Shutdown powers off a running VM with the given name in the experiment with the given
// name. It returns any errors encountered while shutting down the VM. ctx is
// checked until the shutdown starts (see mm.Context); from then on, it goes on
// whatever ctx says.
func Shutdown(ctx context.Context, expName, vmName string) error {
	return shutdown(ctx, expName, vmName, time.Second, shutdownTimeout)
}

// shutdown is Shutdown, asking for the VM's state every poll until it has quit, and killing it after timeout.
func shutdown(ctx context.Context, expName, vmName string, poll, timeout time.Duration) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	state, err := mm.GetVMState(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	if err != nil {
		return fmt.Errorf("retrieving state for VM %s in experiment %s: %w", vmName, expName, err)
	}

	// No need to power off a VM that has already been powered down
	if state == vmStateQuit {
		return nil
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("shutting down VM %s in experiment %s: %w", vmName, expName, err)
	}

	// Stop all packet captures for a vm that will be powered down
	err = StopCaptures(context.Background(), expName, vmName)
	if err != nil && !errors.Is(err, ErrNoCaptures) {
		return fmt.Errorf("stopping captures for VM %s in experiment %s: %w", vmName, expName, err)
	}

	// Send a powerdown signal to the VM using QEMU QMP.
	cmd := mmcli.NewNamespacedCommand(expName)
	qmp := `{ "execute": "system_powerdown" }`
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, qmp)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		cmd.Command = "vm kill " + vmName

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("shutting down VM %s in experiment %s: %w", vmName, expName, err)
		}
	}

	waitForShutdown := func() bool {
		after := time.After(timeout)

		for {
			select {
			case <-after:
				return false
			default:
				time.Sleep(poll)

				state, _ := mm.GetVMState(mm.NS(expName), mm.VMName(vmName))
				if state == vmStateQuit {
					return true
				}
			}
		}
	}

	if !waitForShutdown() {
		// Forced shutdown implementation is equivalent to killing the vm without a
		// flush to preserve the state.
		cmd.Command = "vm kill " + vmName

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("shutting down VM %s in experiment %s: %w", vmName, expName, err)
		}
	}

	return nil
}

// CheckResetDiskState checks that ResetDiskState can reset the disk of the VM vmName of the experiment expName:
// minimega has the VM, and its snapshot flag is set. It changes nothing.
func CheckResetDiskState(ctx context.Context, expName, vmName string) error {
	_, err := resetDiskStatus(ctx, expName, vmName)

	return err
}

// resetDiskStatus returns the vm info row that ResetDiskState reads for the VM, after the checks of
// CheckResetDiskState.
func resetDiskStatus(ctx context.Context, expName, vmName string) ([]map[string]string, error) {
	if expName == "" {
		return nil, errors.New("no experiment name provided")
	}

	if vmName == "" {
		return nil, errors.New("no VM name provided")
	}

	status := mmcli.RunTabularContext(ctx, resetDiskInfoCmd(expName, vmName))

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("resetting disk of VM %s in experiment %s: %w", vmName, expName, err)
	}

	if len(status) == 0 {
		return nil, &mm.VMNotFoundError{Err: errors.New("vm not found")}
	}

	// Make sure the snapshot flag is set
	if status[0]["snapshot"] == "false" {
		return nil, fmt.Errorf("snapshot flag for %s was not set", vmName)
	}

	return status, nil
}

// resetDiskInfoCmd returns the vm info command that ResetDiskState reads the VM with.
func resetDiskInfoCmd(expName, vmName string) *mmcli.Command {
	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"host", "name", "id", "state", "disks", "snapshot"}
	cmd.Filters = []string{"name=" + vmName}

	return cmd
}

// ResetDiskState restores the disk state of a vm to the initial disk state
// It returns any errors encountered while restarting the VM. ctx is checked
// until the reset starts (see mm.Context); from then on, it goes on whatever
// ctx says.
func ResetDiskState(ctx context.Context, expName, vmName string) error {
	status, err := resetDiskStatus(ctx, expName, vmName)
	if err != nil {
		return err
	}

	var (
		origSnap  = strings.Split(status[0]["disks"], ",")[0]
		finalDst  = fmt.Sprintf("%s/%s/%s", common.MinimegaBase, status[0]["id"], "disk-0.qcow2")
		tmpSnap   = fmt.Sprintf("%s_%s_disk-0.qcow2", expName, vmName)
		node      = status[0]["host"]
		cmdPrefix = ""
		cmd       = resetDiskInfoCmd(expName, vmName)
	)

	// Stop all packet captures for a vm that will be reset to
	// its original state.  We are going to assume that a vm
	// that has been shutdown will not have any active packet captures
	if status[0]["state"] == vmStateRunning {
		err := StopCaptures(context.Background(), expName, vmName)
		if err != nil && !errors.Is(err, ErrNoCaptures) {
			return fmt.Errorf(
				"stopping captures for VM %s in experiment %s: %w",
				vmName,
				expName,
				err,
			)
		}

		// Kill the vm without a flush to preserve state
		kill := mmcli.NewNamespacedCommand(expName)
		kill.Command = "vm kill " + vmName

		if err := mmcli.ErrorResponse(mmcli.Run(kill)); err != nil {
			return fmt.Errorf("killing VM %s in experiment %s: %w", vmName, expName, err)
		}
	}

	if !mm.IsHeadnode(node) {
		cmdPrefix = "mesh send " + node
	}

	// Create a snapshot of the original snapshot
	// to exclude file injections in the new snapshot
	cmd.Command = fmt.Sprintf("%s disk snapshot %s %s", cmdPrefix, origSnap, tmpSnap)

	err = mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf(
			"taking disk snapshot remotely for VM %s in experiment %s: %w",
			vmName,
			expName,
			err,
		)
	}

	// Move the snapshot to the vm instance path
	tmpSnapFullPath := fmt.Sprintf("%s/%s", filepath.Dir(origSnap), tmpSnap)
	cmd.Command = fmt.Sprintf("%s shell mv %s %s", cmdPrefix, tmpSnapFullPath, finalDst)

	err = mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf(
			"moving disk snapshot remotely for VM %s in experiment %s: %w",
			vmName,
			expName,
			err,
		)
	}

	// restart the vm
	err = mm.StartVM(mm.NS(expName), mm.VMName(vmName))
	if err != nil {
		return fmt.Errorf("starting VM %s in experiment %s: %w", vmName, expName, err)
	}

	return nil
}

// ErrNotLaunched is the error of Resume for a VM that minimega does not know
// because the topology sets it to not boot.
var ErrNotLaunched = errors.New("minimega did not launch it")

// Resume starts a paused VM with the given name in the experiment with the
// given name. For a VM that minimega does not know and the topology sets to not
// boot, the error wraps ErrNotLaunched and says how to launch it. Once ctx is
// done, a minimega command not yet sent gives up (see mm.Context).
func Resume(ctx context.Context, expName, vmName string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	err := mm.StartVM(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	if err == nil {
		return nil
	}

	if strings.Contains(err.Error(), "vm not found") && doNotBoot(expName, vmName) {
		return fmt.Errorf(
			"VM %s is set to not boot, so %w. Redeploy the VM to launch it", vmName, ErrNotLaunched,
		)
	}

	return fmt.Errorf("resuming VM: %w", err)
}

// doNotBoot reports whether the topology of the stored experiment expName sets
// the VM vmName to not boot.
func doNotBoot(expName, vmName string) bool {
	stored, err := experiment.Get(expName)
	if err != nil || stored.Spec == nil || stored.Spec.Topology() == nil {
		return false
	}

	node := stored.Spec.Topology().FindNodeByName(vmName)
	if node == nil || node.General() == nil {
		return false
	}

	set := node.General().DoNotBoot()

	return set != nil && *set
}

// Redeploy kills the VM and launches it again with the options' changes (CPU, memory, disk). Unsent minimega
// commands give up once ctx is done, until the first change is sent; after that it ignores ctx (see mm.Context).
func Redeploy(ctx context.Context, expName, vmName string, opts ...RedeployOption) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	o := newRedeployOptions(opts...)

	var injects []string

	if o.inject {
		exp, err := experiment.Get(expName)
		if err != nil {
			return fmt.Errorf("getting experiment %s: %w", expName, err)
		}

		for _, n := range exp.Spec.Topology().Nodes() {
			if n.General().Hostname() != vmName {
				continue
			}

			if o.disk == "" {
				drives := n.Hardware().Drives()
				if len(drives) == 0 {
					return fmt.Errorf("redeploying VM %s with injections: topology node has no drives", vmName)
				}

				o.disk = drives[0].Image()
				o.part = injectPartitionOf(drives[0])
			}

			for _, i := range n.Injections() {
				injects = append(injects, fmt.Sprintf("%s:%s", i.Src(), i.Dst()))
			}

			break
		}
	}

	if err := ValidateDiskSpec(o.disk); err != nil {
		return err
	}

	mmOpts := []mm.Option{
		mm.NS(expName),
		mm.VMName(vmName),
		mm.CPU(o.cpu),
		mm.Mem(o.mem),
		mm.Disk(o.disk),
		mm.Injects(injects...),
		mm.InjectPartition(o.part),
		mm.Context(ctx),
	}

	err := mm.RedeployVM(mmOpts...)
	if err != nil {
		return fmt.Errorf("redeploying VM: %w", err)
	}

	return nil
}

// injectPartitionOf returns the partition of drive that phenix injects files
// into: partition 1 when the drive does not set one, as at experiment start.
func injectPartitionOf(drive ifaces.NodeDrive) int {
	if p := drive.InjectPartition(); p != nil {
		return *p
	}

	return 1
}

// ValidateDiskSpec returns an error wrapping ErrInvalidDisk when minimega
// could not boot a VM from the disk spec <path>[,<interface>][,<cache>]. An
// empty spec leaves the VM's disk unchanged and is valid.
func ValidateDiskSpec(spec string) error {
	if spec == "" {
		return nil
	}

	path, err := mm.DiskSpecPath(spec)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDisk, err)
	}

	return validateDisk(path)
}

// validateDisk returns an error wrapping ErrInvalidDisk when minimega could
// not boot a VM from the disk image path, which is "" to leave a VM's disk
// unchanged.
func validateDisk(path string) error {
	if path == "" {
		return nil
	}

	if err := disk.ValidateMinimegaPath(path); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDisk, err)
	}

	return nil
}

// Kill deletes a VM with the given name in the experiment with the given name.
// It returns any errors encountered while killing the VM. Once ctx is done, a
// minimega command not yet sent gives up (see mm.Context).
func Kill(ctx context.Context, expName, vmName string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	err := mm.KillVM(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	if err != nil {
		return fmt.Errorf("killing VM: %w", err)
	}

	return nil
}

// Snapshots lists the VM's snapshots. Once ctx is done, the listing's
// minimega commands give up (see mm.Context) and it returns an error wrapping
// ctx's error.
func Snapshots(ctx context.Context, expName, vmName string) ([]string, error) {
	snapshots, err := file.GetExperimentSnapshotsContext(ctx, expName)
	if err != nil {
		return nil, fmt.Errorf("getting list of experiment snapshots: %w", err)
	}

	var (
		prefix = vmName + "__"
		names  []string
	)

	for _, ss := range snapshots {
		if strings.HasPrefix(ss, prefix) {
			names = append(names, ss)
		}
	}

	return names, nil
}

// SnapshotOption changes how Snapshot waits for the VM's memory snapshot.
type SnapshotOption func(*saveTiming)

// saveTiming is how often awaitSave asks minimega how a memory snapshot is going (asking right after it starts can
// crash minimega), and how long it waits for one.
type saveTiming struct {
	poll, timeout time.Duration
}

// WithSaveTiming polls the memory snapshot every poll, giving up after timeout, in place of every second for an hour.
func WithSaveTiming(poll, timeout time.Duration) SnapshotOption {
	return func(t *saveTiming) { t.poll, t.timeout = poll, timeout }
}

// Snapshot saves the VM's disk and memory state as a snapshot it can later be restored from. Unsent minimega commands
// give up once ctx is done, until the save starts; after that it ignores ctx. Before the save, it refuses a VM that is
// not running (ErrVMNotRunning), whose snapshot flag is not set (ErrSnapshotDisabled) or that has more than one disk
// (ErrMultipleDisks). The snapshot's files stay on the VM's node, in the experiment's files directory.
func Snapshot(ctx context.Context, expName, vmName, out string, cb func(string), opts ...SnapshotOption) error {
	timing := saveTiming{poll: time.Second, timeout: time.Hour}
	for _, opt := range opts {
		opt(&timing)
	}

	vm, err := Get(ctx, expName, vmName)
	if err != nil {
		return fmt.Errorf("getting VM details: %w", err)
	}

	if !vm.Running {
		return ErrVMNotRunning
	}

	// the snapshot's files land in the experiment's files directory, possibly
	// only partly if a step fails
	defer file.InvalidateExperimentFiles(expName)

	out = strings.TrimSuffix(out, filepath.Ext(out))
	out = fmt.Sprintf("%s_%s__%s", expName, vmName, out)

	// ***** BEGIN: SNAPSHOT VM *****

	// Get minimega's snapshot path for VM

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"host", "id", "disks", "snapshot"}
	cmd.Filters = []string{"name=" + vmName}

	status := mmcli.RunTabularContext(ctx, cmd)

	if len(status) == 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("getting VM %s: %w", vmName, err)
		}

		return &mm.VMNotFoundError{Err: fmt.Errorf("vm %s not found", vmName)}
	}

	if err := checkSnapshotFlag(vmName, status[0]); err != nil {
		return err
	}

	if err := checkOneDisk(vmName, status[0]); err != nil {
		return err
	}

	cmd.Columns = nil
	cmd.Filters = nil

	host := status[0]["host"]

	// ***** BEGIN: SAVE VM *****

	cmd.Command = fmt.Sprintf("vm save %s %s", vmName, out)

	// the first change: the commands after it run whatever ctx says
	if err := mmcli.ErrorResponse(mmcli.RunContext(ctx, cmd)); err != nil {
		return fmt.Errorf("starting memory snapshot for VM %s: %w", vmName, err)
	}

	if err := awaitSave(expName, vmName, cb, timing); err != nil {
		return err
	}

	// ***** END: SAVE VM *****

	dst := fmt.Sprintf("%s/images/%s/files", common.PhenixBase, expName)

	if err := mm.MeshShell(host, "mkdir -p "+dst); err != nil {
		return fmt.Errorf("ensuring experiment files directory exists: %w", err)
	}

	final := strings.TrimPrefix(out, expName+"_")

	err = mm.MeshShell(host, fmt.Sprintf("mv %s/images/%s.state %s/%s.state", common.PhenixBase, out, dst, final))
	if err != nil {
		return fmt.Errorf("moving memory snapshot to experiment files directory: %w", err)
	}

	err = mm.MeshShell(host, fmt.Sprintf("mv %s/images/%s.hdd %s/%s.hdd", common.PhenixBase, out, dst, final))
	if err != nil {
		return fmt.Errorf("moving disk snapshot to experiment files directory: %w", err)
	}

	return nil
}

// awaitSave reports the progress of the VM's memory snapshot to cb, if set, until it completes, fails or
// outlasts timing.timeout, then resumes the VM whatever the outcome.
func awaitSave(expName, vmName string, cb func(string), timing saveTiming) (err error) {
	cmd := mmcli.NewNamespacedCommand(expName)

	defer func() {
		resume := mmcli.NewNamespacedCommand(expName)
		resume.Command = "vm start " + vmName

		if rerr := mmcli.ErrorResponse(mmcli.Run(resume)); rerr != nil {
			err = errors.Join(err, fmt.Errorf("resuming VM %s after snapshot: %w", vmName, rerr))
		}
	}()

	cmd.Command = "vm save"
	cmd.Columns = []string{"name", "status", "complete (%)"}
	cmd.Filters = []string{"name=" + vmName}

	var (
		deadline = time.Now().Add(timing.timeout)
		lastErr  error
	)

	for {
		time.Sleep(timing.poll)

		rows, qerr := mmcli.RunTabularCheckedContext(context.Background(), cmd, nil)

		switch {
		case qerr != nil:
			lastErr = qerr
		case len(rows) == 0:
			lastErr = errors.New("no status reported")
		case rows[0]["status"] == statusCompleted:
			if cb != nil {
				cb(statusCompleted)
			}

			return nil
		case rows[0]["status"] == statusFailed || rows[0]["status"] == "cancelled":
			return fmt.Errorf("memory snapshot for VM %s %s", vmName, rows[0]["status"])
		default:
			lastErr = nil

			if cb != nil {
				progress, _ := strconv.ParseFloat(rows[0]["complete (%)"], 64)
				cb(fmt.Sprintf("%f", progress))
			}
		}

		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("memory snapshot for VM %s did not complete within %s: %w", vmName, timing.timeout, lastErr)
			}

			return fmt.Errorf("memory snapshot for VM %s did not complete within %s", vmName, timing.timeout)
		}
	}
}

// Restore relaunches the VM from one of its snapshots. Unsent minimega commands give up once ctx is done, until the
// first change is sent; after that it ignores ctx. Before the first change, it refuses a snapshot the VM does not have
// (ErrSnapshotNotFound), a VM whose snapshot flag is not set (ErrSnapshotDisabled) and a VM with more than one disk
// (ErrMultipleDisks).
func Restore(ctx context.Context, expName, vmName, snap string) error {
	snap = strings.TrimSuffix(snap, filepath.Ext(snap))

	snapshots, err := Snapshots(ctx, expName, vmName)
	if err != nil {
		return fmt.Errorf("getting list of snapshots for VM: %w", err)
	}

	var found bool

	if slices.Contains(snapshots, snap) {
		found = true
	}

	if !found {
		return ErrSnapshotNotFound
	}

	snap = fmt.Sprintf("%s/files/%s", expName, snap)

	details := mm.GetVMInfo(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	if len(details) == 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("getting vm details: %w", err)
		}

		return &mm.VMNotFoundError{Err: errors.New("error getting vm details")}
	}

	if err := checkVMDisks(ctx, expName, vmName); err != nil {
		return err
	}

	cmd := mmcli.NewNamespacedCommand(expName)

	// From `vm config clone` to `vm launch`, the namespace's VM config and
	// launch queue are this VM's alone.
	defer mm.LockVMConfig(expName)()

	// the first change: the commands after it run whatever ctx says
	cmd.Command = "vm config clone " + vmName
	if err := mmcli.ErrorResponse(mmcli.RunContext(ctx, cmd)); err != nil {
		return fmt.Errorf("cloning config for VM %s: %w", vmName, err)
	}

	// Have to copy over UUID separate from clone.
	// Needs to stay the same for miniccc agent to connect
	cmd.Command = "vm config uuid " + details[0].UUID
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("setting uuid for VM %s: %w", vmName, err)
	}

	cmd.Command = fmt.Sprintf("vm config state %s.state", snap)
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("configuring state file for VM %s: %w", vmName, err)
	}

	cmd.Command = fmt.Sprintf("vm config disk %s.hdd,writeback", snap)
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("configuring disk file for VM %s: %w", vmName, err)
	}

	cmd.Command = "vm kill " + vmName
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("killing VM %s: %w", vmName, err)
	}

	// `vm flush` runs on every node in the namespace; the nodes not running the
	// VM answer that it isn't found.
	cmd.Command = "vm flush " + vmName
	if err := mmcli.VMTargetErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("flushing VM %s: %w", vmName, err)
	}

	cmd.Command = "vm launch kvm " + vmName
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("relaunching VM %s: %w", vmName, err)
	}

	cmd.Command = "vm launch"
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("scheduling VM %s: %w", vmName, err)
	}

	cmd.Command = "vm start " + vmName
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("starting VM %s: %w", vmName, err)
	}

	return nil
}

// CommitOption changes how CommitToDisk reports the progress of the commit, or waits for the VM to stop.
type CommitOption func(*commitTiming)

// commitTiming is how often CommitToDisk reports the progress of the commit, the first report included, and how
// often it asks for the state of the VM it stops.
type commitTiming struct {
	wait, poll time.Duration
}

// WithCommitWait reports the progress of the commit every wait, in place of every two seconds.
func WithCommitWait(wait time.Duration) CommitOption {
	return func(t *commitTiming) { t.wait = wait }
}

// WithCommitShutdownPoll asks for the state of the VM that CommitToDisk stops every poll, in place of every second.
func WithCommitShutdownPoll(poll time.Duration) CommitOption {
	return func(t *commitTiming) { t.poll = poll }
}

// CommitToDisk writes the running VM's current disk to a new image, out: it copies the topology's base image to out,
// stops the VM, rebases copies of its snapshots onto the copy, commits them and starts the VM. Once ctx is done the
// VM's status read gives up; from the copy on it ignores ctx. It removes its copies in <images>/<exp>/tmp. On a failure
// it removes out if it made out, and starts a VM that it stopped or tried to stop. It returns only after its last
// progress report to cb. It commits only the VM's first disk, and refuses a VM whose snapshot flag is not set
// (ErrSnapshotDisabled) before it changes anything.
//
//nolint:cyclop,funlen,gocyclo,maintidx // complex logic
func CommitToDisk(
	ctx context.Context, expName, vmName, out string, cb func(float64), opts ...CommitOption,
) (string, error) {
	timing := commitTiming{wait: commitWait, poll: time.Second}
	for _, opt := range opts {
		opt(&timing)
	}

	// Determine name of new disk image, if not provided.
	if out == "" {
		var err error

		out, err = GetNewDiskName(expName, vmName)
		if err != nil {
			return "", fmt.Errorf(
				"getting new disk name for VM %s in experiment %s: %w",
				vmName,
				expName,
				err,
			)
		}
	}

	disk, err := getVMImage(expName, vmName)
	if err != nil {
		return "", fmt.Errorf(
			"getting disk image for VM %s in experiment %s: %w",
			vmName,
			expName,
			err,
		)
	}

	if !filepath.IsAbs(disk) {
		disk = common.PhenixBase + "/images/" + disk
	}

	if !filepath.IsAbs(out) {
		out = common.PhenixBase + "/images/" + out
	}

	base, err := getBackingImage(ctx, disk)
	if err != nil {
		return "", fmt.Errorf(
			"getting backing image for VM %s in experiment %s: %w",
			vmName,
			expName,
			err,
		)
	}

	snapshots, err := getImageSnapshots(ctx, disk)
	if err != nil {
		return "", fmt.Errorf(
			"getting disk snapshots for VM %s in experiment %s: %w",
			vmName,
			expName,
			err,
		)
	}

	// Get status of VM (scheduled host, VM state).

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"host", "name", "id", "state", "snapshot"}
	cmd.Filters = []string{"name=" + vmName}

	status := mmcli.RunTabularContext(ctx, cmd)

	if len(status) == 0 {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("getting VM %s: %w", vmName, err)
		}

		return "", &mm.VMNotFoundError{Err: errors.New("vm not found")}
	}

	if err := checkSnapshotFlag(vmName, status[0]); err != nil {
		return "", err
	}

	var (
		// Get current disk snapshot on the compute node (based on VM ID).
		snap = fmt.Sprintf("%s/%s/disk-0.qcow2", common.MinimegaBase, status[0]["id"])
		node = status[0]["host"]
	)

	// the new image is removed unless the commit completes, if this call made it
	_, statErr := os.Stat(out)
	created := errors.Is(statErr, fs.ErrNotExist)
	newImage := out

	stop, cancel := context.WithCancel(context.Background())
	wait, ctx := errgroup.WithContext(stop)

	var (
		committed bool
		// the copies in <images>/<exp>/tmp that CommitToDisk can have made, on the headnode and on node
		tmpCopies []string
	)

	// On an early return the copies stop, and end (with their progress reports) before CommitToDisk returns.
	defer func() {
		cancel()
		_ = wait.Wait()

		if created && !committed {
			_ = os.Remove(newImage)
		}

		removeCommitCopies(expName, vmName, node, tmpCopies)
	}()

	// Make copy of base image locally on headnode. Using a context here will help
	// cancel the potentially long running copy of a large base image if the other
	// Goroutine below fails.

	wait.Go(func() error {
		copier := newCopier(copyProgressInterval)
		s := copier.subscribe()
		forwarded := make(chan struct{})

		go func() {
			defer close(forwarded)

			for p := range s {
				// If the callback is set, intercept it to reflect the copy stage as the
				// initial 80% of the effort.
				if cb != nil {
					cb(p * copyProgressFactor)
				}
			}
		}()

		err := copier.copy(ctx, base, out)

		// copy closes s, so the last report has been made after this
		<-forwarded

		if err != nil {
			_ = os.Remove(out) // cleanup

			return fmt.Errorf("making copy of backing image: %w", err)
		}

		return nil
	})

	// VM can't be running or we won't be able to copy snapshot remotely.
	stopped := status[0]["state"] != vmStateQuit

	// fail starts the VM again when CommitToDisk stopped it, and returns err with any start error
	fail := func(err error) error {
		if !stopped {
			return err
		}

		if startErr := mm.StartVM(mm.NS(expName), mm.VMName(vmName)); startErr != nil {
			return errors.Join(err, fmt.Errorf("starting VM again: %w", startErr))
		}

		return err
	}

	if stopped {
		// a failed shutdown can leave the VM down; minimega's start does nothing to a running VM
		err := shutdown(context.Background(), expName, vmName, timing.poll, shutdownTimeout)
		if err != nil {
			return "", fail(fmt.Errorf("stopping VM: %w", err))
		}
	}

	for idx := range snapshots {
		tmpCopies = append(tmpCopies, fmt.Sprintf("%s/images/%s/tmp/snapshot-%d.qc2", common.PhenixBase, expName, idx))
	}

	tmpCopies = append(tmpCopies, fmt.Sprintf("%s/images/%s/tmp/%s.qc2", common.PhenixBase, expName, vmName))

	// Make a copy of any snapshots pointed at by the VM disk image in the
	// experiment topology so they can be rebased onto the base backing image for
	// the VM.

	for idx, snapshot := range snapshots {
		var (
			id   = idx
			file = snapshot
		)

		wait.Go(func() error {
			copier := newCopier(copyProgressInterval)

			tmp := fmt.Sprintf("%s/images/%s/tmp/snapshot-%d.qc2", common.PhenixBase, expName, id)

			err := os.MkdirAll(filepath.Dir(tmp), 0o750)
			if err != nil {
				return fmt.Errorf("creating experiment tmp directory: %w", err)
			}

			err = copier.copy(ctx, file, tmp)
			if err != nil {
				_ = os.Remove(tmp)

				return fmt.Errorf("making copy of snapshot %s: %w", file, err)
			}

			// update snapshots entry to point at new copy of snapshot
			snapshots[id] = tmp

			return nil
		})
	}

	// Copy minimega snapshot disk on remote machine to a location (still on
	// remote machine) that can be seen by minimega files. Then use minimega `file
	// get` to copy it to the headnode.

	wait.Go(func() error {
		var cmdPrefix string

		if !mm.IsHeadnode(node) {
			cmdPrefix = "mesh send " + node
		}

		tmp := fmt.Sprintf("%s/images/%s/tmp", common.PhenixBase, expName)

		cmd := mmcli.NewCommand()
		cmd.Command = fmt.Sprintf("%s shell mkdir -p %s", cmdPrefix, tmp)

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("ensuring experiment tmp directory exists: %w", err)
		}

		tmp = fmt.Sprintf("%s/images/%s/tmp/%s.qc2", common.PhenixBase, expName, vmName)
		cmd.Command = fmt.Sprintf("%s shell cp %s %s", cmdPrefix, snap, tmp)

		err = mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("copying snapshot remotely: %w", err)
		}

		headnode, _ := os.Hostname()
		tmp = fmt.Sprintf("%s/tmp/%s.qc2", expName, vmName)

		err = file.CopyFile(tmp, headnode, nil)
		if err != nil {
			return fmt.Errorf("pulling snapshot to headnode: %w", err)
		}

		return nil
	})

	if err := wait.Wait(); err != nil {
		return "", fail(fmt.Errorf("preparing images for rebase/commit: %w", err))
	}

	// Add the final boss (the minimega snapshot disk) to the end of the list of
	// snapshots to rebase onto the copy of the original backing image. If the
	// image pointed at by the VM config in the topology was not a snapshot, then
	// this will be the only snapshot in the list.
	snapshots = append(
		snapshots,
		fmt.Sprintf("%s/images/%s/tmp/%s.qc2", common.PhenixBase, expName, vmName),
	)

	for idx, snapshot := range snapshots {
		// first parent should be copy of original backing image
		parent := out

		if idx != 0 {
			parent = snapshots[idx-1]
		}

		shell := exec.CommandContext(
			context.Background(),
			"qemu-img",
			"rebase",
			"-f",
			"qcow2",
			"-b",
			parent,
			"-F",
			"qcow2",
			snapshot,
		)

		res, err := shell.CombinedOutput()
		if err != nil {
			return "", fail(fmt.Errorf("rebasing snapshot (%s): %w", string(res), err))
		}
	}

	// stopProgress ends the commit's progress reports, and returns once none is left to come
	stopProgress := func() {}

	if cb != nil {
		// the commit adds the headnode's copy of the VM's snapshot (the last of snapshots) to out
		var (
			target     = out
			targetSize float64
			done       = make(chan struct{})
			reports    sync.WaitGroup
		)

		for _, path := range []string{target, snapshots[len(snapshots)-1]} {
			if stat, err := os.Stat(path); err == nil {
				targetSize += float64(stat.Size())
			}
		}

		reports.Add(1)

		go func() {
			defer reports.Done()

			// the first report comes after the commit has had time to start
			ticker := time.NewTicker(timing.wait)
			defer ticker.Stop()

			for {
				select {
				case <-done:
					return
				case <-ticker.C:
				}

				stat, err := os.Stat(target)
				if err != nil || targetSize == 0 {
					continue
				}

				p := float64(stat.Size()) / targetSize

				cb(commitProgressBase + (p * commitProgressFactor))
			}
		}()

		stopProgress = func() {
			close(done)
			reports.Wait()
		}
	}

	shell := exec.CommandContext(context.Background(), "qemu-img", "commit", "-b", out, snapshots[len(snapshots)-1])

	res, err := shell.CombinedOutput()

	stopProgress()

	if err != nil {
		return "", fail(fmt.Errorf("committing snapshot (%s): %w", string(res), err))
	}

	committed = true

	out, _ = filepath.Rel(common.PhenixBase+"/images/", out)

	if err := file.SyncFile(out); err != nil {
		return "", fail(fmt.Errorf("syncing new backing image across cluster: %w", err))
	}

	// restart the vm
	if err := mm.StartVM(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return "", fmt.Errorf("starting VM: %w", err)
	}

	return out, nil
}

// removeCommitCopies removes the copies that CommitToDisk made in <images>/<exp>/tmp. The copy of the VM's
// snapshot (the last of copies) is also on node, when node is not the headnode. It logs a copy it cannot remove.
func removeCommitCopies(expName, vmName, node string, copies []string) {
	for _, path := range copies {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			plog.Warn(plog.TypeSystem, "removing commit copy", "exp", expName, "vm", vmName, "path", path, "err", err)
		}
	}

	if len(copies) == 0 || mm.IsHeadnode(node) {
		return
	}

	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("mesh send %s shell rm -f %s", node, copies[len(copies)-1])

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		plog.Warn(plog.TypeSystem, "removing commit copy", "exp", expName, "vm", vmName, "node", node, "err", err)
	}
}

// MemorySnapshotOption changes how MemorySnapshot waits for the VM's memory dump.
type MemorySnapshotOption func(*dumpTiming)

// dumpTiming is how often MemorySnapshot asks QEMU how a memory dump is going, and how long it waits for one.
type dumpTiming struct {
	poll, timeout time.Duration
}

// WithDumpTiming asks QEMU how the memory dump is going every poll, giving up after timeout, in place of every
// second for an hour.
func WithDumpTiming(poll, timeout time.Duration) MemorySnapshotOption {
	return func(t *dumpTiming) { t.poll, t.timeout = poll, timeout }
}

// MemorySnapshot creates an ELF memory snapshot of a running VM for the Volatility and Rekall forensic toolkits.
// It rejects an output path that minimega would not take as one literal argument, and, for a VM on a node other
// than the headnode, a path outside the experiment's files directory (ErrRemoteDumpPath). Unsent minimega
// commands give up once ctx is done, until the dump starts; after that it ignores ctx.
func MemorySnapshot(
	ctx context.Context, expName, vmName, out string, cb func(string), opts ...MemorySnapshotOption,
) (string, error) {
	timing := dumpTiming{poll: time.Second, timeout: time.Hour}
	for _, opt := range opts {
		opt(&timing)
	}

	_, err := Get(ctx, expName, vmName)
	if err != nil {
		return "", fmt.Errorf("getting VM details: %w", err)
	}

	// the dump usually lands in the experiment's files directory, possibly
	// only partly if it fails
	defer file.InvalidateExperimentFiles(expName)

	if out == "" {
		out = fmt.Sprintf("%s_%s.elf", vmName, getTimestamp(time.Now()))
	}

	// Make all output files have a .elf extension
	if filepath.Ext(out) != ".elf" {
		if filepath.Ext(out) != "" {
			out = strings.TrimSuffix(out, filepath.Ext(out))
		}

		out += ".elf"
	}

	// If only the filename was specified,
	// save in the experiment files directory so that the file
	// appears in the Web GUI's files tab
	if !filepath.IsAbs(out) {
		out = fmt.Sprintf("%s/images/%s/files/%s", common.PhenixBase, expName, out)
	}

	if err := validateDumpPath(out); err != nil {
		return "", err
	}

	// Get compute node VM is running on.

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"host", "name", "id", "state"}
	cmd.Filters = []string{"name=" + vmName}

	status := mmcli.RunTabularContext(ctx, cmd)

	if len(status) == 0 {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("getting VM %s: %w", vmName, err)
		}

		return "", &mm.VMNotFoundError{Err: errors.New("vm not found")}
	}

	var (
		host = status[0]["host"]
		// the dump's path in the minimega files directory, from which `file get` pulls a remote VM's dump
		pull      string
		cmdPrefix string
	)

	if !mm.IsHeadnode(host) {
		if pull, err = pulledDumpPath(expName, out); err != nil {
			return "", err
		}

		cmdPrefix = "mesh send " + host
	}

	// Make sure that the memory snapshot directory exists
	cmd.Columns = nil
	cmd.Filters = nil
	cmd.Command = fmt.Sprintf("%s shell mkdir -p %s", cmdPrefix, filepath.Dir(out))

	if err := mmcli.ErrorResponse(mmcli.RunContext(ctx, cmd)); err != nil {
		return "", fmt.Errorf("ensuring experiment files directory exists: %w", err)
	}
	// ***** BEGIN: MEMORY SNAPSHOT VM *****

	cmd.Command = dumpGuestMemoryCommand(vmName, out)

	// the first change: the commands after it run whatever ctx says
	if err := mmcli.ErrorResponse(mmcli.RunContext(ctx, cmd)); err != nil {
		return "", fmt.Errorf("starting memory snapshot for VM %s: ERROR: %w", vmName, err)
	}

	if res, err := awaitDump(expName, vmName, status[0]["state"] == vmStateRunning, cb, timing); err != nil {
		return res, err
	}

	// Copy the ELF memory dump to the headnode if the VM was not
	// hosted on the headnode
	if pull != "" {
		cmd.Command = "file get " + pull

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return "", fmt.Errorf("pulling ELF memory snapshot to headnode: %w", err)
		}
	}

	return out, nil
}

// awaitDump reports the progress of the VM's memory dump to cb, if set, until it completes, fails or outlasts
// timing.timeout. After a timeout it resumes the VM, if it was running (see resumeAfterDump). It returns "failed"
// with the error of a dump that QEMU reports as failed.
func awaitDump(expName, vmName string, running bool, cb func(string), timing dumpTiming) (string, error) {
	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, `{ "execute": "query-dump" }`)

	report := func(s string) {
		if cb != nil {
			cb(s)
		}
	}

	deadline := time.Now().Add(timing.timeout)

	for {
		// sleep before querying the vm to prevent errors from start delays
		time.Sleep(timing.poll)

		var v mm.BlockDumpResponse

		res, err := mmcli.SingleResponse(mmcli.Run(cmd))
		if err != nil {
			report(statusFailed)

			return "", fmt.Errorf("getting memory snapshot status for VM %s: %w", vmName, err)
		}

		_ = json.Unmarshal([]byte(res), &v)

		switch v.Return.Status {
		case "":
			report(statusFailed)

			return "", fmt.Errorf("no status available for %s: %v", vmName, v)
		case statusFailed:
			report(statusFailed)

			return statusFailed, fmt.Errorf("failed to create memory snapshot for %s: %v", vmName, v)
		}

		var progress float64
		if v.Return.Total > 0 {
			progress = float64(v.Return.Completed) / float64(v.Return.Total)
		}

		report(fmt.Sprintf("%v", progress))

		if v.Return.Status == statusCompleted {
			report(statusCompleted)

			return "", nil
		}

		if time.Now().After(deadline) {
			report(statusFailed)

			err := fmt.Errorf("memory snapshot for VM %s did not complete within %s", vmName, timing.timeout)

			return "", errors.Join(err, resumeAfterDump(expName, vmName, running))
		}
	}
}

// resumeAfterDump resumes a VM that QEMU paused for a memory dump that did not end in time. QEMU pauses only a
// running VM, so it does nothing for a VM that was not running. It sends QMP `cont`: minimega still reports the
// paused VM as running, so `vm start` does nothing.
func resumeAfterDump(expName, vmName string, running bool) error {
	if !running {
		return nil
	}

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, `{ "execute": "cont" }`)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("resuming VM %s after memory snapshot: %w", vmName, err)
	}

	return nil
}

// pulledDumpPath is the path, in the minimega files directory, from which `file get` pulls the memory dump at
// path to the headnode. It returns an error wrapping ErrRemoteDumpPath for a path outside the experiment's files
// directory, as the dump would stay on the VM's node.
func pulledDumpPath(expName, path string) (string, error) {
	files := fmt.Sprintf("%s/images/%s/files", common.PhenixBase, expName)

	rel, err := filepath.Rel(files, path)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %s is not in %s", ErrRemoteDumpPath, path, files)
	}

	return expName + "/files/" + filepath.ToSlash(rel), nil
}

// validateDumpPath returns an error wrapping disk.ErrUnsafeName when path cannot be a memory dump's path in
// dumpGuestMemoryCommand: minimega would not take it as one literal argument, or it is not UTF-8, as QMP's JSON is.
func validateDumpPath(path string) error {
	if !utf8.ValidString(path) {
		return fmt.Errorf("memory snapshot path: %w: %q is not UTF-8", disk.ErrUnsafeName, path)
	}

	if err := disk.ValidateMinimegaPath(path); err != nil {
		return fmt.Errorf("memory snapshot path: %w", err)
	}

	return nil
}

// dumpGuestMemoryCommand is the minimega command that has QEMU dump the VM's memory to path as an ELF file, in
// the background.
func dumpGuestMemoryCommand(vmName, path string) string {
	qmp := fmt.Sprintf(
		`{ "execute": "dump-guest-memory", "arguments": { "protocol": "file:%s", "paging": false, "format": "elf" , "detach": true} }`,
		path,
	)

	return fmt.Sprintf("vm qmp %s '%s'", vmName, qmp)
}

// ErrInvalidSubnet is wrapped by the error of CaptureSubnet and StopCaptureSubnet for a subnet that is not a CIDR
// prefix, such as 10.0.0.0/24.
var ErrInvalidSubnet = errors.New("the subnet must be a CIDR prefix, such as 10.0.0.0/24")

// CaptureSubnet starts a capture on each interface in subnet of every running VM (of vmList, if
// given), in the format of opts, and returns those VMs' captures, each once. ctx is checked only
// until the first capture starts (see mm.Context).
func CaptureSubnet(ctx context.Context, expName, subnet string, vmList []string, opts CaptureOptions) ([]mm.Capture, error) {
	_, refNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidSubnet, subnet)
	}

	vms, err := runningCaptureVMs(ctx, expName, "started", vmList)
	if err != nil {
		return nil, err
	}

	inSubnet := func(v mm.VM, iface int) bool {
		if iface >= len(v.IPv4) {
			return false
		}

		address := net.ParseIP(v.IPv4[iface])

		return address != nil && refNet.Contains(address)
	}

	matchedVMs, _, err := startMatchingCaptures(ctx, expName, "subnet "+subnet, vms, inSubnet, opts)
	if err != nil {
		return nil, err
	}

	return capturesForVMs(expName, matchedVMs), nil
}

// capturesForVMs returns the captures for each of the named VMs, in the order
// named, with one `capture` listing for the experiment rather than one per VM.
func capturesForVMs(expName string, vmNames []string) []mm.Capture {
	if len(vmNames) == 0 {
		return nil
	}

	byVM := mm.GroupCapturesByVM(mm.GetExperimentCaptures(mm.NS(expName)))

	var captures []mm.Capture

	for _, vmName := range vmNames {
		captures = append(captures, byVM[vmName]...)
	}

	return captures
}

// StopCaptureSubnet stops every capture of each running VM (of vmList, if given) with an address
// in subnet, or of every such VM when subnet is empty. A subnet that is not a CIDR prefix returns an
// error that wraps ErrInvalidSubnet. It returns the VMs it stopped, and checks ctx only until the
// first VM's captures stop.
func StopCaptureSubnet(ctx context.Context, expName, subnet string, vmList []string) ([]string, error) {
	vms, err := SubnetCaptureVMs(ctx, expName, subnet, vmList)
	if err != nil {
		return nil, err
	}

	var matchedVMs []string

	committed := context.WithoutCancel(ctx)

	for _, vm := range vms {
		// once one VM's captures are stopped, stop the rest
		run := ctx
		if len(matchedVMs) > 0 {
			run = committed
		}

		err := stopListedCaptures(run, expName, vm.Name, nil, vm.Captures)
		if err == nil {
			matchedVMs = append(matchedVMs, vm.Name)

			continue
		}

		if err := run.Err(); err != nil {
			return nil, fmt.Errorf("stopping captures in subnet %s: %w", subnet, err)
		}
	}

	// the stopped captures' files are now complete
	file.InvalidateExperimentFiles(expName)

	return matchedVMs, nil
}

// SubnetCaptureVMs returns the running VMs of the experiment expName (of vmList, if given) whose captures
// StopCaptureSubnet stops: each VM with a capture and, for a subnet that is not empty, an address in subnet. It stops
// no capture. Its errors are those of StopCaptureSubnet before it stops a capture.
func SubnetCaptureVMs(ctx context.Context, expName, subnet string, vmList []string) ([]mm.VM, error) {
	var refNet *net.IPNet

	if subnet != "" {
		var err error

		if _, refNet, err = net.ParseCIDR(subnet); err != nil {
			return nil, fmt.Errorf("%w: %q", ErrInvalidSubnet, subnet)
		}
	}

	vms, err := runningCaptureVMs(ctx, expName, "stopped", vmList)
	if err != nil {
		return nil, err
	}

	matched := make([]mm.VM, 0, len(vms))

	for _, vm := range vms {
		if len(vm.Captures) == 0 || (subnet != "" && !hasAddressIn(vm.IPv4, refNet)) {
			continue
		}

		matched = append(matched, vm)
	}

	return matched, nil
}

// hasAddressIn reports whether any of the addresses is in subnet.
func hasAddressIn(addresses []string, subnet *net.IPNet) bool {
	if subnet == nil {
		return false
	}

	return slices.ContainsFunc(addresses, func(address string) bool {
		ip := net.ParseIP(address)

		return ip != nil && subnet.Contains(ip)
	})
}

// minimegaLacksVM returns err, the error of a command that names one VM, as a *mm.VMNotFoundError when each error
// that minimega answered says that it does not have the VM, else err as it is.
func minimegaLacksVM(err error) error {
	var answered *multierror.Error
	if !errors.As(err, &answered) || len(answered.Errors) == 0 {
		return err
	}

	for _, e := range answered.Errors {
		if !strings.HasPrefix(e.Error(), "vm not found: ") {
			return err
		}
	}

	return &mm.VMNotFoundError{Err: err}
}

// ChangeOpticalDisc changes the optical disc in the first drive. It rejects an
// empty path with ErrNoDiscPath, and, with an error wrapping ErrInvalidDisk, a
// path minimega would not take as one literal argument. Once ctx is done, the change gives up if minimega has not
// been sent it (see mm.Context).
func ChangeOpticalDisc(ctx context.Context, expName, vmName, isoPath string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	if isoPath == "" {
		return ErrNoDiscPath
	}

	if err := validateDisk(isoPath); err != nil {
		return err
	}

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = fmt.Sprintf("vm cdrom change %s %s", vmName, isoPath)

	err := mmcli.ErrorResponse(mmcli.RunContext(ctx, cmd))
	if err != nil {
		return fmt.Errorf("changing optical disc for VM %s: %w", vmName, minimegaLacksVM(err))
	}

	return nil
}

// EjectOpticalDisc ejects the optical disc in the first drive. Once ctx is
// done, the eject gives up if minimega has not been sent it (see mm.Context).
func EjectOpticalDisc(ctx context.Context, expName, vmName string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = "vm cdrom eject " + vmName

	err := mmcli.ErrorResponse(mmcli.RunContext(ctx, cmd))
	if err != nil {
		return fmt.Errorf("ejecting optical disc for VM %s: %w", vmName, minimegaLacksVM(err))
	}

	return nil
}
