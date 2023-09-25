package vm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"phenix/api/experiment"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"

	"golang.org/x/sync/errgroup"
)

var vlanAliasRegex = regexp.MustCompile(`(.*) \(\d*\)`)

func Count(expName string) (int, error) {
	if expName == "" {
		return 0, fmt.Errorf("no experiment name provided")
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return 0, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	return len(exp.Spec.Topology().Nodes()), nil
}

// List collects VMs, combining topology settings with running VM details if the
// experiment is running. It returns a slice of VM structs and any errors
// encountered while gathering them.
func List(expName string) ([]mm.VM, error) {
	if expName == "" {
		return nil, fmt.Errorf("no experiment name provided")
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	var (
		running = make(map[string]mm.VM)
		vms     []mm.VM
	)

	if exp.Running() {
		for _, vm := range mm.GetVMInfo(mm.NS(expName)) {
			running[vm.Name] = vm
		}
	}

	for idx, node := range exp.Spec.Topology().Nodes() {
		var (
			disk string
			dnb  bool
		)

		if drives := node.Hardware().Drives(); len(drives) > 0 {
			disk = drives[0].Image()
		}

		if node.General().DoNotBoot() != nil {
			dnb = *node.General().DoNotBoot()
		}

		vm := mm.VM{
			ID:         idx,
			Name:       node.General().Hostname(),
			Experiment: exp.Spec.ExperimentName(),
			CPUs:       node.Hardware().VCPU(),
			RAM:        node.Hardware().Memory(),
			Disk:       disk,
			Interfaces: make(map[string]string),
			DoNotBoot:  dnb,
			Type:       node.Type(),
			OSType:     node.Hardware().OSType(),
		}

		for _, iface := range node.Network().Interfaces() {
			vm.IPv4 = append(vm.IPv4, iface.Address()) // empty for DHCP

			if iface.VLAN() != "" { // might be empty for external nodes
				vm.Networks = append(vm.Networks, iface.VLAN())
				vm.Interfaces[iface.VLAN()] = iface.Address() // empty for DHCP
			}
		}

		if node.External() {
			vm.State = "EXTERNAL"
		} else if details, ok := running[vm.Name]; ok {
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

			// Since we get the IP from the experiment config, but the network name
			// from minimega (to preserve iface to network ordering), make sure the
			// ordering of IPs matches the odering of networks. We could just use a
			// map here, but then the iface to network ordering that minimega ensures
			// would be lost.
			for idx, nw := range details.Networks {
				// If it's set here, we got it from minimega, which is the source of truth
				// for running experiments.
				if vm.IPv4[idx] != "" {
					continue
				}

				// At this point, `nw` will look something like `EXP_1 (101)`. In the
				// experiment config, we just have `EXP_1` so we need to use that
				// portion from minimega as the `Interfaces` map key.
				if match := vlanAliasRegex.FindStringSubmatch(nw); match != nil {
					vm.IPv4[idx] = vm.Interfaces[match[1]]
				} else {
					vm.IPv4[idx] = "n/a"
				}
			}
		} else {
			vm.Host = exp.Spec.Schedules()[vm.Name]
		}

		vms = append(vms, vm)
	}

	return vms, nil
}

// Get retrieves the VM with the given name from the experiment with the given
// name. If the experiment is running, topology VM settings are combined with
// running VM details. It returns a pointer to a VM struct, and any errors
// encountered while retrieving the VM.
func Get(expName, vmName string) (*mm.VM, error) {
	if expName == "" {
		return nil, fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return nil, fmt.Errorf("no VM name provided")
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	var vm *mm.VM

	for idx, node := range exp.Spec.Topology().Nodes() {
		if node.General().Hostname() != vmName {
			continue
		}

		vm = &mm.VM{
			ID:          idx,
			Name:        node.General().Hostname(),
			Experiment:  exp.Spec.ExperimentName(),
			CPUs:        node.Hardware().VCPU(),
			RAM:         node.Hardware().Memory(),
			Disk:        util.GetMMFullPath(node.Hardware().Drives()[0].Image()),
			Interfaces:  make(map[string]string),
			DoNotBoot:   *node.General().DoNotBoot(),
			OSType:      string(node.Hardware().OSType()),
			Metadata:    make(map[string]interface{}),
			Labels:      node.Labels(),
			Annotations: node.Annotations(),
		}

		for _, iface := range node.Network().Interfaces() {
			vm.IPv4 = append(vm.IPv4, iface.Address()) // empty for DHCP
			vm.Networks = append(vm.Networks, iface.VLAN())
			vm.Interfaces[iface.VLAN()] = iface.Address() // empty for DHCP
		}

		for _, app := range exp.Apps() {
			for _, h := range app.Hosts() {
				if h.Hostname() == vm.Name {
					vm.Metadata[app.Name()] = h.Metadata
				}
			}
		}
	}

	if vm == nil {
		return nil, fmt.Errorf("VM %s not found in experiment %s", vmName, expName)
	}

	if !exp.Running() {
		vm.Host = exp.Spec.Schedules()[vm.Name]
		return vm, nil
	}

	details := mm.GetVMInfo(mm.NS(expName), mm.VMName(vmName))

	if len(details) != 1 {
		return vm, nil
	}

	vm.Host = details[0].Host
	vm.State = details[0].State
	vm.Running = details[0].Running
	vm.Networks = details[0].Networks
	vm.Taps = details[0].Taps
	vm.IPv4 = details[0].IPv4
	vm.Captures = details[0].Captures
	vm.CdRom = details[0].CdRom
	vm.Tags = details[0].Tags
	vm.Uptime = details[0].Uptime
	vm.CPUs = details[0].CPUs
	vm.RAM = details[0].RAM
	vm.Disk = details[0].Disk
	vm.CCActive = details[0].CCActive

	// `vm.IPv4` could be nil/empty if minimega isn't reporting any IPs for it
	if len(vm.IPv4) == 0 {
		vm.IPv4 = make([]string, len(details[0].Networks))
	}

	// Since we get the IP from the experiment config, but the network name from
	// minimega (to preserve iface to network ordering), make sure the ordering of
	// IPs matches the odering of networks. We could just use a map here, but then
	// the iface to network ordering that minimega ensures would be lost.
	for idx, nw := range details[0].Networks {
		// If it's set here, we got it from minimega, which is the source of truth
		// for running experiments.
		if vm.IPv4[idx] != "" {
			continue
		}

		// At this point, `nw` will look something like `EXP_1 (101)`. In the exp,
		// we just have `EXP_1` so we need to use that portion from minimega as the
		// `Interfaces` map key.
		if match := vlanAliasRegex.FindStringSubmatch(nw); match != nil {
			vm.IPv4[idx] = vm.Interfaces[match[1]]
		} else {
			vm.IPv4[idx] = "n/a"
		}
	}

	return vm, nil
}

func Update(opts ...UpdateOption) error {
	o := newUpdateOptions(opts...)

	if o.exp == "" || o.vm == "" {
		return fmt.Errorf("experiment or VM name not provided")
	}

	running := experiment.Running(o.exp)

	if running && o.iface == nil {
		return fmt.Errorf("only interface connections can be updated while experiment is running")
	}

	// The only setting that can be updated while an experiment is running is the
	// VLAN an interface is connected to.
	if running {
		if o.iface.vlan == "" {
			return Disonnect(o.exp, o.vm, o.iface.index)
		} else {
			return Connect(o.exp, o.vm, o.iface.index, o.iface.vlan)
		}
	}

	exp, err := experiment.Get(o.exp)
	if err != nil {
		return fmt.Errorf("unable to get experiment %s: %w", o.exp, err)
	}

	vm := exp.Spec.Topology().FindNodeByName(o.vm)
	if vm == nil {
		return fmt.Errorf("unable to find VM %s in experiment %s", o.vm, o.exp)
	}

	if o.cpu != 0 {
		vm.Hardware().SetVCPU(o.cpu)
	}

	if o.mem != 0 {
		vm.Hardware().SetMemory(o.mem)
	}

	if o.disk != "" {
		vm.Hardware().Drives()[0].SetImage(o.disk)
	}

	if o.dnb != nil {
		vm.General().SetDoNotBoot(*o.dnb)
	}

	if o.host != nil {
		if *o.host == "" {
			delete(exp.Spec.Schedules(), o.vm)
		} else {
			exp.Spec.ScheduleNode(o.vm, *o.host)
		}
	}

	err = experiment.Save(experiment.SaveWithName(o.exp), experiment.SaveWithSpec(exp.Spec))
	if err != nil {
		return fmt.Errorf("unable to save experiment with updated VM: %w", err)
	}

	return nil
}

func Screenshot(expName, vmName, size string) ([]byte, error) {
	screenshot, err := mm.GetVMScreenshot(mm.NS(expName), mm.VMName(vmName), mm.ScreenshotSize(size))
	if err != nil {
		return nil, fmt.Errorf("getting VM screenshot: %w", err)
	}

	return screenshot, nil
}

// Pause stops a running VM with the given name in the experiment with the given
// name. It returns any errors encountered while pausing the VM.
func Pause(expName, vmName string) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	err := StopCaptures(expName, vmName)
	if err != nil && !errors.Is(err, ErrNoCaptures) {
		return fmt.Errorf("stopping captures for VM %s in experiment %s: %w", vmName, expName, err)
	}

	if err := mm.StopVM(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return fmt.Errorf("pausing VM: %w", err)
	}

	return nil
}

// Restarts a running VM with the given name in the experiment with the given
// name. It returns any errors encountered while restarting the VM.
func Restart(expName, vmName string) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	state, err := mm.GetVMState(mm.NS(expName), mm.VMName(vmName))

	if err != nil {
		return fmt.Errorf("Retrieving state for VM %s in experiment %s: %w", vmName, expName, err)
	}

	//Using "system_reset" on a VM that is in the "QUIT" state fails
	if state == "QUIT" {
		return mm.StartVM(mm.NS(expName), mm.VMName(vmName))

	}

	cmd := mmcli.NewNamespacedCommand(expName)
	qmp := fmt.Sprintf(`{ "execute": "system_reset" }`)
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, qmp)

	_, err = mmcli.SingleResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("restarting VM %s: %w", vmName, err)
	}

	return nil
}

// Powers off a running VM with the given name in the experiment with the given
// name. It returns any errors encountered while shutting down the VM.
func Shutdown(expName, vmName string) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	state, err := mm.GetVMState(mm.NS(expName), mm.VMName(vmName))
	if err != nil {
		return fmt.Errorf("retrieving state for VM %s in experiment %s: %w", vmName, expName, err)
	}

	//No need to power off a VM that has already been powered down
	if state == "QUIT" {
		return nil
	}

	// Stop all packet captures for a vm that will be powered down
	err = StopCaptures(expName, vmName)
	if err != nil && !errors.Is(err, ErrNoCaptures) {
		return fmt.Errorf("stopping captures for VM %s in experiment %s: %w", vmName, expName, err)
	}

	// Send a powerdown signal to the VM using QEMU QMP.
	cmd := mmcli.NewNamespacedCommand(expName)
	qmp := `{ "execute": "system_powerdown" }`
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, qmp)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		// return fmt.Errorf("powering down VM %s: %w", vmName, err)

		cmd.Command = "vm kill " + vmName
		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("shutting down VM %s in experiment %s: %w", vmName, expName, err)
		}
	}

	waitForShutdown := func() bool {
		// Give the VM a maximum of 30s to shutdown.
		after := time.After(30 * time.Second)

		for {
			select {
			case <-after:
				return false
			default:
				time.Sleep(1 * time.Second)

				state, _ := mm.GetVMState(mm.NS(expName), mm.VMName(vmName))
				if state == "QUIT" {
					return true
				}
			}
		}
	}

	if !waitForShutdown() {
		// Forced shutdown implementation is equivalent to killing the vm without a
		// flush to preserve the state.
		cmd.Command = "vm kill " + vmName
		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("shutting down VM %s in experiment %s: %w", vmName, expName, err)
		}
	}

	return nil
}

// Restores the disk state of a vm to the initial disk state
// It returns any errors encountered while restarting the VM.
func ResetDiskState(expName, vmName string) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	// Overwrite the snapshot in the vm instance
	// directory with a new snapshot.
	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = "vm info"
	cmd.Columns = []string{"host", "name", "id", "state", "disks", "snapshot"}
	cmd.Filters = []string{"name=" + vmName}

	status := mmcli.RunTabular(cmd)

	if len(status) == 0 {
		return fmt.Errorf("VM not found")
	}

	var (
		origSnap  = strings.Split(status[0]["disks"], ",")[0]
		finalDst  = fmt.Sprintf("%s/%s/%s", common.MinimegaBase, status[0]["id"], "disk-0.qcow2")
		tmpSnap   = fmt.Sprintf("%s_%s_disk-0.qcow2", expName, vmName)
		node      = status[0]["host"]
		cmdPrefix = ""
	)

	// Make sure the snapshot flag is set
	if status[0]["snapshot"] == "false" {
		return fmt.Errorf("Snapshot flag for %s was not set", vmName)
	}

	// Stop all packet captures for a vm that will be reset to
	// its original state.  We are going to assume that a vm
	// that has been shutdown will not have any active packet captures
	if status[0]["state"] == "RUNNING" {

		err := StopCaptures(expName, vmName)
		if err != nil && !errors.Is(err, ErrNoCaptures) {
			return fmt.Errorf("stopping captures for VM %s in experiment %s: %w", vmName, expName, err)
		}

		// Kill the vm without a flush to preserve state
		cmd := mmcli.NewNamespacedCommand(expName)
		cmd.Command = "vm kill " + vmName

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("Killing VM %s in experiment %s: %w", vmName, expName, err)
		}

	}

	if !mm.IsHeadnode(node) {
		cmdPrefix = "mesh send " + node
	}

	// Create a snapshot of the original snapshot
	// to exclude file injections in the new snapshot
	cmd.Command = fmt.Sprintf("%s disk snapshot %s %s", cmdPrefix, origSnap, tmpSnap)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("taking disk snapshot remotely for VM %s in experiment %s: %w", vmName, expName, err)
	}

	// Move the snapshot to the vm instance path
	tmpSnapFullPath := fmt.Sprintf("%s/%s", filepath.Dir(origSnap), tmpSnap)
	cmd.Command = fmt.Sprintf("%s shell mv %s %s", cmdPrefix, tmpSnapFullPath, finalDst)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("moving disk snapshot remotely for VM %s in experiment %s: %w", vmName, expName, err)
	}

	// restart the vm
	if err := mm.StartVM(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return fmt.Errorf("starting VM %s in experiment %s: %w", vmName, expName, err)
	}

	return nil
}

// Resume starts a paused VM with the given name in the experiment with the
// given name. It returns any errors encountered while resuming the VM.
func Resume(expName, vmName string) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	if err := mm.StartVM(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return fmt.Errorf("resuming VM: %w", err)
	}

	return nil
}

// Redeploy redeploys a VM with the given name in the experiment with the given
// name. Multiple redeploy options can be passed to alter the resulting
// redeployed VM, such as CPU, memory, and disk options. It returns any errors
// encountered while redeploying the VM.
func Redeploy(expName, vmName string, opts ...RedeployOption) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
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
				o.disk = n.Hardware().Drives()[0].Image()
				o.part = *n.Hardware().Drives()[0].InjectPartition()
			}

			for _, i := range n.Injections() {
				injects = append(injects, fmt.Sprintf("%s:%s", i.Src(), i.Dst()))
			}

			break
		}
	}

	mmOpts := []mm.Option{
		mm.NS(expName),
		mm.VMName(vmName),
		mm.CPU(o.cpu),
		mm.Mem(o.mem),
		mm.Disk(o.disk),
		mm.Injects(injects...),
		mm.InjectPartition(o.part),
	}

	if err := mm.RedeployVM(mmOpts...); err != nil {
		return fmt.Errorf("redeploying VM: %w", err)
	}

	return nil
}

// Kill deletes a VM with the given name in the experiment with the given name.
// It returns any errors encountered while killing the VM.
func Kill(expName, vmName string) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	if err := mm.KillVM(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return fmt.Errorf("killing VM: %w", err)
	}

	return nil
}

func Snapshots(expName, vmName string) ([]string, error) {
	snapshots, err := file.GetExperimentSnapshots(expName)
	if err != nil {
		return nil, fmt.Errorf("getting list of experiment snapshots: %w", err)
	}

	var (
		prefix = fmt.Sprintf("%s__", vmName)
		names  []string
	)

	for _, ss := range snapshots {
		if strings.HasPrefix(ss, prefix) {
			names = append(names, ss)
		}
	}

	return names, nil
}

func Snapshot(expName, vmName, out string, cb func(string)) error {
	vm, err := Get(expName, vmName)
	if err != nil {
		return fmt.Errorf("getting VM details: %w", err)
	}

	if !vm.Running {
		return errors.New("VM is not running")
	}

	out = strings.TrimSuffix(out, filepath.Ext(out))
	out = fmt.Sprintf("%s_%s__%s", expName, vmName, out)

	// ***** BEGIN: SNAPSHOT VM *****

	// Get minimega's snapshot path for VM

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = "vm info"
	cmd.Columns = []string{"host", "id"}
	cmd.Filters = []string{"name=" + vmName}

	status := mmcli.RunTabular(cmd)

	if len(status) == 0 {
		return fmt.Errorf("VM %s not found", vmName)
	}

	cmd.Columns = nil
	cmd.Filters = nil

	var (
		host = status[0]["host"]
		fp   = fmt.Sprintf("%s/%s", common.MinimegaBase, status[0]["id"])
	)

	qmp := fmt.Sprintf(`{ "execute": "query-block" }`)
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, qmp)

	res, err := mmcli.SingleResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("querying for block device details for VM %s: %w", vmName, err)
	}

	var v map[string][]mm.BlockDevice
	json.Unmarshal([]byte(res), &v)

	var device string

	for _, dev := range v["return"] {
		if dev.Inserted != nil {
			if strings.HasPrefix(dev.Inserted.File, fp) {
				device = dev.Device
				break
			}
		}
	}

	target := fmt.Sprintf("%s/images/%s.qc2", common.PhenixBase, out)

	qmp = fmt.Sprintf(`{ "execute": "drive-backup", "arguments": { "device": "%s", "sync": "top", "target": "%s" } }`, device, target)
	cmd.Command = fmt.Sprintf(`vm qmp %s '%s'`, vmName, qmp)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("starting disk snapshot for VM %s: %w", vmName, err)
	}

	qmp = fmt.Sprintf(`{ "execute": "query-block-jobs" }`)
	cmd.Command = fmt.Sprintf(`vm qmp %s '%s'`, vmName, qmp)

	for {
		res, err := mmcli.SingleResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("querying for block device jobs for VM %s: %w", vmName, err)
		}

		var v map[string][]mm.BlockDeviceJobs
		json.Unmarshal([]byte(res), &v)

		if len(v["return"]) == 0 {
			break
		}

		for _, job := range v["return"] {
			if job.Device != device {
				continue
			}

			if cb != nil {
				// Cut progress in half since drive backup is 1 of 2 steps.
				progress := float64(job.Offset) / float64(job.Length)
				progress = progress * 0.5

				cb(fmt.Sprintf("%f", progress))
			}
		}

		time.Sleep(1 * time.Second)
	}

	// ***** END: SNAPSHOT VM *****

	// ***** BEGIN: MIGRATE VM *****

	cmd.Command = fmt.Sprintf("vm migrate %s %s.SNAP", vmName, out)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("starting memory snapshot for VM %s: %w", vmName, err)
	}

	cmd.Command = "vm migrate"
	cmd.Columns = []string{"name", "status", "complete (%)"}
	cmd.Filters = []string{"name=" + vmName}
	//Adding a 1 second delay before calling "vm migrate"
	//for a status update appears to prevent the status call
	//from crashing minimega
	time.Sleep(1 * time.Second)
	for {
		status := mmcli.RunTabular(cmd)[0]

		if cb != nil {
			if status["status"] == "completed" {
				cb("completed")
			} else {
				// Cut progress in half and add 0.5 to it since migrate is 2 of 2 steps.
				progress, _ := strconv.ParseFloat(status["complete (%)"], 64)
				progress = 0.5 + (progress * 0.5)

				cb(fmt.Sprintf("%f", progress))
			}
		}

		if status["status"] == "completed" {
			break
		}

		time.Sleep(1 * time.Second)
	}

	// ***** END: MIGRATE VM *****

	cmd.Command = fmt.Sprintf("vm start %s", vmName)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("resuming VM %s after snapshot: %w", vmName, err)
	}

	var (
		dst       = fmt.Sprintf("%s/images/%s/files", common.PhenixBase, expName)
		cmdPrefix string
	)

	if !mm.IsHeadnode(host) {
		cmdPrefix = "mesh send " + host
	}

	cmd = mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("%s shell mkdir -p %s", cmdPrefix, dst)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("ensuring experiment files directory exists: %w", err)
	}

	final := strings.TrimPrefix(out, expName+"_")

	cmd.Command = fmt.Sprintf("%s shell mv %s/images/%s.SNAP %s/%s.SNAP", cmdPrefix, common.PhenixBase, out, dst, final)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("moving memory snapshot to experiment files directory: %w", err)
	}

	cmd.Command = fmt.Sprintf("%s shell mv %s/images/%s.qc2 %s/%s.qc2", cmdPrefix, common.PhenixBase, out, dst, final)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("moving disk snapshot to experiment files directory: %w", err)
	}

	return nil

}

func Restore(expName, vmName, snap string) error {
	snap = strings.TrimSuffix(snap, filepath.Ext(snap))

	snapshots, err := Snapshots(expName, vmName)
	if err != nil {
		return fmt.Errorf("getting list of snapshots for VM: %w", err)
	}

	var found bool

	for _, ss := range snapshots {
		if snap == ss {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("snapshot does not exist on cluster")
	}

	snap = fmt.Sprintf("%s/files/%s", expName, snap)

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = fmt.Sprintf("vm config clone %s", vmName)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("cloning config for VM %s: %w", vmName, err)
	}

	cmd.Command = fmt.Sprintf("vm config migrate %s.SNAP", snap)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("configuring migrate file for VM %s: %w", vmName, err)
	}

	cmd.Command = fmt.Sprintf("vm config disk %s.qc2,writeback", snap)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("configuring disk file for VM %s: %w", vmName, err)
	}

	cmd.Command = fmt.Sprintf("vm kill %s", vmName)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("killing VM %s: %w", vmName, err)
	}

	// TODO: explicitly flush killed VM by name once we start using that version
	// of minimega.
	cmd.Command = "vm flush"

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("flushing VMs: %w", err)
	}

	cmd.Command = fmt.Sprintf("vm launch kvm %s", vmName)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("relaunching VM %s: %w", vmName, err)
	}

	cmd.Command = "vm launch"

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("scheduling VM %s: %w", vmName, err)
	}

	cmd.Command = fmt.Sprintf("vm start %s", vmName)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("starting VM %s: %w", vmName, err)
	}

	return nil

}

func CommitToDisk(expName, vmName, out string, cb func(float64)) (string, error) {
	// Determine name of new disk image, if not provided.
	if out == "" {
		var err error

		out, err = GetNewDiskName(expName, vmName)
		if err != nil {
			return "", fmt.Errorf("getting new disk name for VM %s in experiment %s: %w", vmName, expName, err)
		}
	}

	base, err := getBaseImage(expName, vmName)
	if err != nil {
		return "", fmt.Errorf("getting base image for VM %s in experiment %s: %w", vmName, expName, err)
	}

	// Get status of VM (scheduled host, VM state).

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = "vm info"
	cmd.Columns = []string{"host", "name", "id", "state"}
	cmd.Filters = []string{"name=" + vmName}

	status := mmcli.RunTabular(cmd)

	if len(status) == 0 {
		return "", fmt.Errorf("VM not found")
	}

	var (
		// Get current disk snapshot on the compute node (based on VM ID).
		snap = fmt.Sprintf("%s/%s/disk-0.qcow2", common.MinimegaBase, status[0]["id"])
		node = status[0]["host"]
	)

	if !filepath.IsAbs(base) {
		base = common.PhenixBase + "/images/" + base
	}

	if !filepath.IsAbs(out) {
		out = common.PhenixBase + "/images/" + out
	}

	wait, ctx := errgroup.WithContext(context.Background())

	// Make copy of base image locally on headnode. Using a context here will help
	// cancel the potentially long running copy of a large base image if the other
	// Goroutine below fails.

	wait.Go(func() error {
		copier := newCopier()
		s := copier.subscribe()

		go func() {
			for p := range s {
				// If the callback is set, intercept it to reflect the copy stage as the
				// initial 80% of the effort.
				if cb != nil {
					cb(p * 0.8)
				}
			}
		}()

		if err := copier.copy(ctx, base, out); err != nil {
			os.Remove(out) // cleanup
			return fmt.Errorf("making copy of backing image: %w", err)
		}

		return nil
	})

	// VM can't be running or we won't be able to copy snapshot remotely.
	if status[0]["state"] != "QUIT" {
		if err := Shutdown(expName, vmName); err != nil {
			return "", fmt.Errorf("stopping VM: %w", err)
		}
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

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("ensuring experiment tmp directory exists: %w", err)
		}

		tmp = fmt.Sprintf("%s/images/%s/tmp/%s.qc2", common.PhenixBase, expName, vmName)
		cmd.Command = fmt.Sprintf("%s shell cp %s %s", cmdPrefix, snap, tmp)

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("copying snapshot remotely: %w", err)
		}

		headnode, _ := os.Hostname()
		tmp = fmt.Sprintf("%s/tmp/%s.qc2", expName, vmName)

		if err := file.CopyFile(tmp, headnode, nil); err != nil {
			return fmt.Errorf("pulling snapshot to headnode: %w", err)
		}

		return nil
	})

	if err := wait.Wait(); err != nil {
		return "", fmt.Errorf("preparing images for rebase/commit: %w", err)
	}

	snap = fmt.Sprintf("%s/images/%s/tmp/%s.qc2", common.PhenixBase, expName, vmName)

	shell := exec.Command("qemu-img", "rebase", "-f", "qcow2", "-b", out, "-F", "qcow2", snap)

	res, err := shell.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("rebasing snapshot (%s): %w", string(res), err)
	}

	done := make(chan struct{})
	defer close(done)

	if cb != nil {
		stat, _ := os.Stat(out)
		targetSize := float64(stat.Size())

		stat, _ = os.Stat(snap)
		targetSize += float64(stat.Size())

		go func() {
			for {
				select {
				case <-done:
					return
				default:
					// We sleep at the beginning instead of the end to ensure the command
					// we shell out to below has time to run before we try to stat the
					// destination file.
					time.Sleep(2 * time.Second)

					stat, err := os.Stat(out)
					if err != nil {
						continue
					}

					p := float64(stat.Size()) / targetSize

					cb(0.8 + (p * 0.2))
				}
			}
		}()
	}

	shell = exec.Command("qemu-img", "commit", snap)

	res, err = shell.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("committing snapshot (%s): %w", string(res), err)
	}

	out, _ = filepath.Rel(common.PhenixBase+"/images/", out)

	if err := file.SyncFile(out, nil); err != nil {
		return "", fmt.Errorf("syncing new backing image across cluster: %w", err)
	}

	//restart the vm
	if err := mm.StartVM(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return "", fmt.Errorf("starting VM: %w", err)
	}

	return out, nil

}

func MemorySnapshot(expName, vmName, out string, cb func(string)) (string, error) {

	_, err := Get(expName, vmName)
	if err != nil {
		return "", fmt.Errorf("getting VM details: %w", err)
	}

	if out == "" {
		out = fmt.Sprintf("%s_%s.elf", vmName, getTimestamp())
	}

	// Make all output files have a .elf extension
	if filepath.Ext(out) != ".elf" {
		if filepath.Ext(out) != "" {
			out = strings.TrimSuffix(out, filepath.Ext(out))
		}
		out += ".elf"
	}

	// Get compute node VM is running on.

	cmd := mmcli.NewNamespacedCommand(expName)
	cmd.Command = "vm info"
	cmd.Columns = []string{"host", "name", "id", "state"}
	cmd.Filters = []string{"name=" + vmName}

	status := mmcli.RunTabular(cmd)

	if len(status) == 0 {
		return "", fmt.Errorf("VM not found")
	}

	// If only the filename was specified,
	// save in the experiment files directory so that the file
	// appears in the Web GUI's files tab
	if !filepath.IsAbs(out) {
		out = fmt.Sprintf("%s/images/%s/files/%s", common.PhenixBase, expName, out)
	}

	// Make sure that the memory snapshot directory exists
	var cmdPrefix string
	if !mm.IsHeadnode(status[0]["host"]) {
		cmdPrefix = "mesh send " + status[0]["host"]
	}

	cmd.Columns = nil
	cmd.Filters = nil
	cmd.Command = fmt.Sprintf("%s shell mkdir -p %s", cmdPrefix, filepath.Dir(out))

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return "", fmt.Errorf("ensuring experiment files directory exists: %w", err)
	}
	// ***** BEGIN: MEMORY SNAPSHOT VM *****

	qmp := fmt.Sprintf(`{ "execute": "dump-guest-memory", "arguments": { "protocol": "file:%s", "paging": false, "format": "elf" , "detach": true} }`, out)
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, qmp)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return "", fmt.Errorf("starting memory snapshot for VM %s: ERROR: %w", vmName, err)

	}

	qmp = fmt.Sprintf(`{ "execute": "query-dump" }`)
	cmd.Command = fmt.Sprintf("vm qmp %s '%s'", vmName, qmp)

	var (
		v        mm.BlockDumpResponse
		res      string
		progress string
	)

	for {
		// sleep before querying the vm to to prevent errors from start delays
		time.Sleep(1 * time.Second)
		res, err = mmcli.SingleResponse(mmcli.Run(cmd))
		if err != nil {
			if cb != nil {
				cb("failed")
			}
			return "", fmt.Errorf("getting memory snapshot status for VM %s: %w", vmName, err)
		}

		json.Unmarshal([]byte(res), &v)

		if len(v.Return.Status) == 0 {
			if cb != nil {
				cb("failed")
			}
			return "", fmt.Errorf("no status available for %s: %s", vmName, v)

		}

		if v.Return.Status == "failed" {
			if cb != nil {
				cb("failed")
			}
			return "failed", fmt.Errorf("failed to create memory snapshot for %s: %s", vmName, v)

		}

		progress = fmt.Sprintf("%v", float64(v.Return.Completed)/float64(v.Return.Total))

		cb(progress)

		if v.Return.Status == "completed" {
			cb("completed")
			break
		}

	}

	// Copy the ELF memory dump to the headnode if the VM was not
	// hosted on the headnode
	if !mm.IsHeadnode(status[0]["host"]) {

		// File path should be relative to the minimega files directory
		memoryDumpPath := fmt.Sprintf("%s/files/%s", expName, filepath.Base(out))

		cmd.Command = fmt.Sprintf(`file get %s`, memoryDumpPath)

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return "", fmt.Errorf("pulling ELF memory snapshot to headnode: %w", err)
		}

	}

	return out, nil

}

// CaptureSubnet starts packet captures for all the VMs that
// have an interface in the specified subnet.  The vmList argument
// is optional and defines the list of VMs to search.
func CaptureSubnet(expName, subnet string, vmList []string) ([]mm.Capture, error) {

	// Make sure the experiment is running
	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	if !exp.Running() {
		return nil, fmt.Errorf("packet captures can only be started for a running experiment")
	}

	vms, err := List(expName)

	if err != nil {
		return nil, fmt.Errorf("Getting vm list for %s failed", expName)
	}

	_, refNet, err := net.ParseCIDR(subnet)

	if err != nil {
		return nil, fmt.Errorf("Unable to parse %s", subnet)
	}

	// Use empty struct for code consistency and
	// slight memory savings
	var vmTable map[string]struct{}
	var matchedVMs []string

	// An optional list of VMs can be provided
	// to restrict the search scope
	if len(vmList) > 0 {
		// Put vms in a table for quick lookup
		vmTable = make(map[string]struct{})

		for _, vmName := range vmList {

			if _, ok := vmTable[vmName]; !ok {
				vmTable[vmName] = struct{}{}
			}
		}
	}

	// Find the interfaces that are in the
	// specified subnet
	for _, vm := range vms {

		// Make sure the VM is running
		state, err := mm.GetVMState(mm.NS(expName), mm.VMName(vm.Name))

		if err != nil {
			continue
		}

		if state != "RUNNING" {
			continue
		}

		// Skip vms not in the list
		if vmTable != nil {
			if _, ok := vmTable[vm.Name]; !ok {
				continue
			}
		}

		for iface, network := range vm.IPv4 {
			address := net.ParseIP(network)

			if address == nil {
				continue
			}

			if refNet.Contains(address) {
				timeStamp := getTimestamp()

				filename := fmt.Sprintf("%s_%d_%s.pcap", vm.Name, iface, timeStamp)
				if StartCapture(expName, vm.Name, iface, filename) == nil {
					matchedVMs = append(matchedVMs, vm.Name)
				}

			}

		}

	}

	// Get all the captures for all the VMs
	var allVMCaptures []mm.Capture
	for _, vmName := range matchedVMs {

		vmCaptures := mm.GetVMCaptures(mm.NS(expName), mm.VMName(vmName))

		allVMCaptures = append(allVMCaptures, vmCaptures...)
	}

	return allVMCaptures, nil

}

// StopCaptureSubnet will stop all captures for any VM
// that has an interface in the specified subnet. Unfortunately
// due to a limitation in the minimega capture cli, a capture for
// just the interface that is found in the specified subnet can not
// be stopped.  The subnet argument is optional.  If the subnet
// argument is not specified, then all captures for all VMs will be stopped.
func StopCaptureSubnet(expName, subnet string, vmList []string) ([]string, error) {

	// Make sure the experiment is running
	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	if !exp.Running() {
		return nil, fmt.Errorf("packet captures can only be stopped for a running experiment")
	}

	vms, err := List(expName)

	if err != nil {
		return nil, fmt.Errorf("Getting vm list for %s failed", expName)
	}

	_, refNet, err := net.ParseCIDR(subnet)

	if err != nil {
		refNet = nil
	}

	// Use empty struct for code consistency and
	// slight memory savings
	var vmTable map[string]struct{}
	var matchedVMs []string

	// An optional list of VMs can be provided
	// to restrict the search scope
	if len(vmList) > 0 {
		// Put vms in a table for quick lookup
		vmTable = make(map[string]struct{})

		for _, vmName := range vmList {

			if _, ok := vmTable[vmName]; !ok {
				vmTable[vmName] = struct{}{}
			}
		}
	}

	// Find the interfaces that are in the
	// specified subnet
	for _, vm := range vms {

		// Skip vms with no current captures
		if len(vm.Captures) == 0 {
			continue
		}

		// Make sure the VM is running
		state, err := mm.GetVMState(mm.NS(expName), mm.VMName(vm.Name))

		if err != nil {
			continue
		}

		if state != "RUNNING" {
			continue
		}

		// Skip vms not in the list
		if vmTable != nil {
			if _, ok := vmTable[vm.Name]; !ok {
				continue
			}
		}

		// if no subnet was specified, then stop
		// all the captures for this VM
		if len(subnet) == 0 {
			if StopCaptures(expName, vm.Name) == nil {
				matchedVMs = append(matchedVMs, vm.Name)
			}
			continue
		}

		if refNet == nil {
			continue
		}

		for _, network := range vm.IPv4 {
			address := net.ParseIP(network)

			if address == nil {
				continue
			}

			if refNet.Contains(address) {

				if StopCaptures(expName, vm.Name) == nil {
					matchedVMs = append(matchedVMs, vm.Name)

					// Avoid trying to stop captures for
					// the same vm since all the captures
					// for a VM should be stopped
					break
				}

			}

		}

	}

	return matchedVMs, nil

}

// Changes the optical disc in the first drive
func ChangeOpticalDisc(expName, vmName, isoPath string) error {

	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	if isoPath == "" {
		return fmt.Errorf("no optical disc path provided")
	}

	
	cmd := mmcli.NewNamespacedCommand(expName)	
	cmd.Command = fmt.Sprintf("vm cdrom change %s %s",vmName,isoPath)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("changing optical disc for VM %s: %w", vmName, err)
	}
	

	
	return nil
}

// Ejects the optical disc in the first drive
func EjectOpticalDisc(expName, vmName string) error {

	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

		
	cmd := mmcli.NewNamespacedCommand(expName)	
	cmd.Command = fmt.Sprintf("vm cdrom eject %s",vmName)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("ejecting optical disc for VM %s: %w", vmName, err)
	}

	
	return nil
}

