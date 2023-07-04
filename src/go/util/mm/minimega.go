package mm

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"phenix/util/common"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"

	"github.com/hashicorp/go-multierror"
)

var (
	ErrCaptureExists      = fmt.Errorf("capture already exists")
	ErrNoCaptures         = fmt.Errorf("no captures exist")
	ErrC2ClientNotActive  = fmt.Errorf("C2 client not active for VM")
	ErrVMNotFound         = fmt.Errorf("VM not found")
	ErrScreenshotNotFound = fmt.Errorf("screenshot not found")
)

// Mutex to protect minimega cc filter setting when configuring cc commands from
// different Goroutines. This is at the package level to protect across multiple
// instances of the Minimega struct.
var ccMu sync.Mutex

// Regular express to use for matching C2 response headers.
var responseRegex = regexp.MustCompile(`(\d*)\/(.*)\/(stdout|stderr):`)

type Minimega struct{}

func (Minimega) ReadScriptFromFile(filename string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = "read " + filename

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("reading mmcli script: %w", err)
	}

	return nil
}

func (Minimega) ClearNamespace(ns string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = "clear namespace " + ns

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("clearing minimega namespace: %w", err)
	}

	return nil
}

func (Minimega) LaunchVMs(ns string, start ...string) error {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "vm launch"

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("launching VMs: %w", err)
	}

	if start == nil {
		cmd.Command = "vm start all"

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("starting VMs: %w", err)
		}
	} else {
		for _, name := range start {
			cmd.Command = "vm start " + name

			if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
				return fmt.Errorf("starting VM %s: %w", name, err)
			}
		}
	}

	return nil
}

func (Minimega) GetLaunchProgress(ns string, expected int) (float64, error) {
	var queued int

	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "ns queue"

	re := regexp.MustCompile(`Names: (.*)`)

	for resps := range mmcli.Run(cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				continue
			}

			for _, m := range re.FindAllStringSubmatch(resp.Response, -1) {
				queued += len(strings.Split(m[1], ","))
			}
		}
	}

	// `ns queue` will be empty once queued VMs have been launched.

	if queued == 0 {
		cmd.Command = "vm info"
		cmd.Columns = []string{"state"}

		status := mmcli.RunTabular(cmd)

		if len(status) == 0 {
			return 0.0, nil
		}

		for _, s := range status {
			if s["state"] == "BUILDING" {
				queued++
			}
		}
	}

	return float64(queued) / float64(expected), nil

}

func (this Minimega) GetVMInfo(opts ...Option) VMs {
	o := NewOptions(opts...)

	// don't rely on `cc_active` column in `vm info` table
	activeC2 := getActiveC2(o.ns)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm info"
	cmd.Columns = []string{"uuid", "host", "name", "state", "uptime", "vlan", "tap", "ip", "memory", "vcpus", "disks", "snapshot", "tags"}

	if o.vm != "" {
		cmd.Filters = []string{"name=" + o.vm}
	}

	var vms VMs

	for _, row := range mmcli.RunTabular(cmd) {
		vm := VM{
			UUID:     row["uuid"],
			Host:     row["host"],
			Name:     row["name"],
			State:    row["state"],
			Running:  row["state"] == "RUNNING",
			CCActive: activeC2[row["uuid"]],
		}

		s := row["vlan"]
		s = strings.TrimPrefix(s, "[")
		s = strings.TrimSuffix(s, "]")

		if s != "" {
			vm.Networks = strings.Split(s, ", ")
		}

		s = row["tap"]
		s = strings.TrimPrefix(s, "[")
		s = strings.TrimSuffix(s, "]")

		if s != "" {
			vm.Taps = strings.Split(s, ", ")
		}

		s = row["ip"]
		s = strings.TrimPrefix(s, "[")
		s = strings.TrimSuffix(s, "]")

		if s != "" {
			vm.IPv4 = strings.Split(s, ", ")
		}

		s = row["tags"]
		s = strings.TrimPrefix(s, "{")
		s = strings.TrimSuffix(s, "}")

		if s != "" {
			vm.Tags = strings.Split(s, ",")
		}

		// Make sure the VM name is set prior to calling `GetVMCaptures`, as the VM
		// name is not always set when calling `GetVMInfo`.
		vm.Captures = this.GetVMCaptures(NS(o.ns), VMName(vm.Name))

		uptime, err := time.ParseDuration(row["uptime"])
		if err == nil {
			vm.Uptime = uptime.Seconds()
		}

		vm.RAM, _ = strconv.Atoi(row["memory"])
		vm.CPUs, _ = strconv.Atoi(row["vcpus"])

		// TODO: confirm multiple disks are separated by whitespace.
		disk := strings.Fields(row["disks"])[0]
		// diskspec can include multiple settings separated by comma. Path to disk
		// will always be first setting.
		disk = strings.Split(disk, ",")[0]

		snapshot, _ := strconv.ParseBool(row["snapshot"])

		if snapshot {
			cmd = mmcli.NewCommand()
			cmd.Command = "disk info " + disk

			// Only expect one row returned
			// TODO (btr): check length to avoid a panic.
			resp := mmcli.RunTabular(cmd)[0]

			if resp["backingfile"] == "" {
				vm.Disk = resp["image"]
			} else {
				vm.Disk = resp["backingfile"]
			}
		} else {
			// Attempting to get disk info when not using a snapshot will cause a
			// locked file error.
			vm.Disk = disk
		}

		vms = append(vms, vm)
	}

	return vms
}

func (Minimega) GetVMScreenshot(opts ...Option) ([]byte, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm screenshot %s file /dev/null %s", o.vm, o.screenshotSize)

	for resps := range mmcli.Run(cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				if strings.HasPrefix(resp.Error, "vm not found:") {
					return nil, ErrVMNotFound
				}

				if strings.HasPrefix(resp.Error, "vm not running:") {
					return nil, ErrVMNotFound
				}

				continue
			}

			if resp.Data == nil {
				continue
			}

			screenshot, err := base64.StdEncoding.DecodeString(resp.Data.(string))
			if err != nil {
				return nil, fmt.Errorf("decoding screenshot: %w", err)
			}

			return screenshot, nil
		}
	}

	return nil, ErrScreenshotNotFound
}

func (Minimega) GetVNCEndpoint(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm info"
	cmd.Columns = []string{"host", "vnc_port"}
	cmd.Filters = []string{"type=kvm", fmt.Sprintf("name=%s", o.vm)}

	var endpoint string

	for _, vm := range mmcli.RunTabular(cmd) {
		endpoint = fmt.Sprintf("%s:%s", vm["host"], vm["vnc_port"])
	}

	if endpoint == "" {
		return "", fmt.Errorf("not found")
	}

	return endpoint, nil
}

func (Minimega) StartVM(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm start %s", o.vm)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("starting VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) StopVM(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm stop %s", o.vm)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("stopping VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) RedeployVM(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)

	// Get VM info before killing VM below.
	cmd.Command = "vm info"
	cmd.Filters = []string{"name=" + o.vm}

	info := mmcli.RunTabular(cmd)
	if len(info) == 0 {
		return fmt.Errorf("no info found for VM %s in namespace %s", o.vm, o.ns)
	}

	cmd.Filters = nil

	cmd.Command = "vm config clone " + o.vm
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("cloning VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	cmd.Command = "clear vm config migrate"
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("clearing config for VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	cmd.Command = "vm kill " + o.vm
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("killing VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	if err := flush(o.ns); err != nil {
		return err
	}

	if o.cpu != 0 {
		cmd.Command = fmt.Sprintf("vm config vcpus %d", o.cpu)

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("configuring VCPUs for VM %s in namespace %s: %w", o.vm, o.ns, err)
		}
	}

	if o.mem != 0 {
		cmd.Command = fmt.Sprintf("vm config mem %d", o.mem)

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("configuring memory for VM %s in namespace %s: %w", o.vm, o.ns, err)
		}
	}

	if o.disk != "" {
		var disk string

		if len(o.injects) == 0 {
			disk = o.disk
		} else {
			// Should only be one row of data since we filtered by VM name.
			disks := info[0]["disks"]

			// Only do injects if this VM was originally deployed with a disk snapshot.
			if strings.Contains(disks, "_snapshot") {
				old := newDiskConfig(disks)
				new := newDiskConfig(o.disk)

				// Delete disk snapshot file across cluster
				deleteFile(old.base)

				cmd.Command = fmt.Sprintf("disk snapshot %s %s", new.path, old.base)

				if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
					return fmt.Errorf("snapshotting disk for VM %s in namespace %s: %w", o.vm, o.ns, err)
				}

				if err := inject(old.base, o.injectPart, o.injects...); err != nil {
					return err
				}

				// Use disk cache mode if provided by user. Otherwise, use original disk
				// cache mode.
				disk = old.string(new.cache)
			} else {
				disk = o.disk
			}
		}

		cmd.Command = "vm config disk " + disk

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("configuring disk for VM %s in namespace %s: %w", o.vm, o.ns, err)
		}
	}

	cmd.Command = "vm launch kvm " + o.vm
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("scheduling VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	cmd.Command = "vm launch"
	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("launching scheduled VMs in namespace %s: %w", o.ns, err)
	}

	cmd.Command = fmt.Sprintf("vm start %s", o.vm)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("starting VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) KillVM(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm kill %s", o.vm)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("killing VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return flush(o.ns)
}

func (Minimega) GetVMHost(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm info"
	cmd.Columns = []string{"host"}
	cmd.Filters = []string{"name=" + o.vm}

	status := mmcli.RunTabular(cmd)

	if len(status) == 0 {
		return "", fmt.Errorf("VM %s not found", o.vm)
	}

	return status[0]["host"], nil
}

func (Minimega) GetVMState(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm info summary"
	cmd.Columns = []string{"state"}
	cmd.Filters = []string{"name=" + o.vm}

	status := mmcli.RunTabular(cmd)

	if len(status) == 0 {
		return "", fmt.Errorf("VM %s not found", o.vm)
	}

	return status[0]["state"], nil
}

func (Minimega) ConnectVMInterface(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm net connect %s %d %s", o.vm, o.connectIface, o.connectVLAN)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("connecting interface %d on VM %s to VLAN %s in namespace %s: %w", o.connectIface, o.vm, o.connectVLAN, o.ns, err)
	}

	return nil
}

func (Minimega) DisconnectVMInterface(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm net disconnect %s %d", o.vm, o.connectIface)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("disconnecting interface %d on VM %s in namespace %s: %w", o.connectIface, o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) CreateTunnel(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("cc tunnel %s %d %s %d", o.vm, o.srcPort, o.dstHost, o.dstPort)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("creating tunnel to %s (%d:%s:%d): %w", o.vm, o.srcPort, o.dstHost, o.dstPort, err)
	}

	return nil
}

func (Minimega) GetTunnels(opts ...Option) []map[string]string {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "cc tunnel list all"

	if o.vm != "" {
		cmd.Command = fmt.Sprintf("cc tunnel list %s", o.vm)
	}

	if o.dstHost != "" {
		cmd.Filters = append(cmd.Filters, fmt.Sprintf("dst=%s", o.dstHost))
	}

	if o.dstPort != 0 {
		cmd.Filters = append(cmd.Filters, fmt.Sprintf("'dst port'=%d", o.dstPort))
	}

	return mmcli.RunTabular(cmd)
}

func (Minimega) CloseTunnel(opts ...Option) error {
	tunnels := GetTunnels(opts...)

	o := NewOptions(opts...)
	var errs error

	for _, row := range tunnels {
		cmd := mmcli.NewNamespacedCommand(o.ns)
		cmd.Command = fmt.Sprintf("cc tunnel close %s %s", o.vm, row["id"])

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("closing tunnel to %s (%s:%d): %w", o.vm, o.dstHost, o.dstPort, err))
		}
	}

	return errs
}

func (Minimega) StartVMCapture(opts ...Option) error {
	o := NewOptions(opts...)

	captures := GetVMCaptures(opts...)

	for _, capture := range captures {
		if capture.Interface == o.captureIface {
			return ErrCaptureExists
		}
	}

	if filepath.IsAbs(o.captureFile) {
		return fmt.Errorf("path for capture file should not be absolute")
	}

	host, err := GetVMHost(opts...)
	if err != nil {
		return fmt.Errorf("unable to determine what host the VM is scheduled on: %w", err)
	}

	var cmdPrefix string

	if !IsHeadnode(host) {
		cmdPrefix = "mesh send " + host
	}

	dir := common.PhenixBase + "/images/" + filepath.Dir(o.captureFile)
	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("%s shell mkdir -p %s", cmdPrefix, dir)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("ensuring experiment files directory exists: %w", err)
	}

	cmd = mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("capture pcap vm %s %d %s", o.vm, o.captureIface, o.captureFile)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("starting VM capture for interface %d on VM %s in namespace %s: %w", o.captureIface, o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) StopVMCapture(opts ...Option) error {
	captures := GetVMCaptures(opts...)

	if len(captures) == 0 {
		return ErrNoCaptures
	}

	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("capture pcap delete vm %s", o.vm)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("deleting VM captures for VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) GetExperimentCaptures(opts ...Option) []Capture {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "capture"
	cmd.Columns = []string{"interface", "path"}

	var captures []Capture

	for _, row := range mmcli.RunTabular(cmd) {
		// `interface` column will be empty if the capture is bridge-wide
		if row["interface"] == "" {
			// currently phenix doesn't provide the option to create bridge-wide
			// captures, so if one exists (via manual creation) we just ignore it
			continue
		}

		// `interface` column will be in the form of <vm_name>:<iface_idx>
		iface := strings.Split(row["interface"], ":")

		vm := iface[0]
		idx, _ := strconv.Atoi(iface[1])

		capture := Capture{
			VM:        vm,
			Interface: idx,
			Filepath:  row["path"],
		}

		captures = append(captures, capture)
	}

	return captures
}

func (this Minimega) GetVMCaptures(opts ...Option) []Capture {
	o := NewOptions(opts...)

	var (
		captures = this.GetExperimentCaptures(opts...)
		keep     []Capture
	)

	for _, capture := range captures {
		if capture.VM == o.vm {
			keep = append(keep, capture)
		}
	}

	return keep
}

func (Minimega) GetClusterHosts(schedOnly bool) (Hosts, error) {
	// Get headnode details
	hosts, err := processNamespaceHosts("minimega")
	if err != nil {
		return nil, fmt.Errorf("processing headnode details: %w", err)
	}

	if len(hosts) == 0 {
		return []Host{}, fmt.Errorf("no cluster hosts found")
	}

	head := hosts[0]
	head.Schedulable = false
	head.Headnode = true

	var cluster []Host

	// Clear dummy namespace used for getting compute nodes in case a new compute
	// node has been added since the last time the dummy namespace was created.
	ClearNamespace("__phenix__")

	// Get compute nodes details
	hosts, err = processNamespaceHosts("__phenix__")
	if err != nil {
		return nil, fmt.Errorf("processing compute nodes details: %w", err)
	}

	for _, host := range hosts {
		// This will happen if the headnode is included as a compute node
		// (ie. when there's only one node in the cluster).
		if host.Name == head.Name {
			head.Schedulable = true
			continue
		}

		host.Name = common.TrimHostnameSuffixes(host.Name)
		host.Schedulable = true

		cluster = append(cluster, host)
	}

	if schedOnly && !head.Schedulable {
		return cluster, nil
	}

	head.Name = common.TrimHostnameSuffixes(head.Name)

	cluster = append(cluster, head)

	return cluster, nil
}

func (Minimega) Headnode() string {
	// Get headnode details
	hosts, _ := processNamespaceHosts("minimega")

	if len(hosts) == 0 {
		return "" // ???
	}

	headnode := hosts[0].Name

	// Trim host name suffixes (like -minimega, or -phenix) potentially added to
	// Docker containers by Docker Compose config.
	return common.TrimHostnameSuffixes(headnode)
}

func (this Minimega) IsHeadnode(node string) bool {
	// Trim node name suffixes (like -minimega, or -phenix) potentially added to
	// Docker containers by Docker Compose config.
	node = common.TrimHostnameSuffixes(node)

	return node == this.Headnode()
}

func (Minimega) GetVLANs(opts ...Option) (map[string]int, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vlans"

	var (
		vlans  = make(map[string]int)
		status = mmcli.RunTabular(cmd)
	)

	for _, row := range status {
		alias := row["alias"]
		id, err := strconv.Atoi(row["vlan"])
		if err != nil {
			return nil, fmt.Errorf("converting VLAN ID to integer: %w", err)
		}

		vlans[alias] = id
	}

	return vlans, nil
}

func (Minimega) IsC2ClientActive(opts ...C2Option) error {
	o := NewC2Options(opts...)
	if o.skipActiveClientCheck {
		return nil
	}

	vms := GetVMInfo(NS(o.ns), VMName(o.vm))
	if len(vms) == 0 {
		return fmt.Errorf("VM %s does not exist", o.vm)
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "cc client"

	if o.idByUUID {
		// We use the UUID of the VM instead of the name since `cc clients` returns
		// the actual hostname of the VM as reported by the miniccc agent, which may
		// not always match the name minimega uses to track the VM.
		cmd.Columns = []string{"uuid"}
		cmd.Filters = []string{"uuid=" + vms[0].UUID}
	} else {
		// Even though `cc clients` returns the actual hostname of the VM as reported
		// by the miniccc agent, we still go ahead and check for the VM name as
		// defined in the topology since that is what the hostname should be in the VM
		// (per the startup app). This way, we don't consider Windows VMs ready until
		// they've rebooted to get their hostname set correctly.
		cmd.Columns = []string{"hostname"}
		cmd.Filters = []string{"hostname=" + vms[0].Name}
	}

	after := time.After(o.timeout)

	for {
		select {
		case <-o.ctx.Done():
			return o.ctx.Err()
		case <-after:
			return ErrC2ClientNotActive
		default:
			rows := mmcli.RunTabular(cmd)

			if len(rows) != 0 {
				return nil
			}

			time.Sleep(2 * time.Second)
		}
	}
}

func (this Minimega) ExecC2Command(opts ...C2Option) (string, error) {
	if err := this.IsC2ClientActive(opts...); err != nil {
		return "", fmt.Errorf("cannot execute command: %w", err)
	}

	o := NewC2Options(opts...)

	exec := func(ns, vm, cmd string) (string, error) {
		ccMu.Lock()
		defer ccMu.Unlock()

		c := mmcli.NewNamespacedCommand(ns)
		c.Command = fmt.Sprintf("cc filter name=%s", vm)

		if err := mmcli.ErrorResponse(mmcli.Run(c)); err != nil {
			return "", fmt.Errorf("setting host filter to %s: %w", vm, err)
		}

		c.Command = cmd
		c.Timeout = o.timeout

		data, err := mmcli.SingleDataResponse(mmcli.Run(c))
		if err != nil {
			if errors.Is(err, mmcli.ErrTimeout) {
				return "", fmt.Errorf("timeout running '%s' in vm %s", cmd, vm)
			}

			return "", fmt.Errorf("running '%s' in vm %s: %w", cmd, vm, err)
		}

		return fmt.Sprintf("%v", data), nil
	}

	if o.testConn != "" {
		cmd := fmt.Sprintf("cc test-conn %s", o.testConn)

		id, err := exec(o.ns, o.vm, cmd)
		if err != nil {
			return "", fmt.Errorf("calling '%s' for vm %s: %w", cmd, o.vm, err)
		}

		if o.wait {
			if err := waitForResponse(o.ctx, o.ns, id, o.timeout); err != nil {
				return "", fmt.Errorf("waiting for response: %w", err)
			}
		}

		return id, nil
	}

	if o.sendFile != "" {
		cmd := fmt.Sprintf("cc send %s", o.sendFile)

		id, err := exec(o.ns, o.vm, cmd)
		if err != nil {
			return "", fmt.Errorf("sending file '%s' to vm %s: %w", o.sendFile, o.vm, err)
		}

		// Special case: if both the `sendFile` and `command` options are set, then
		// send the file first, wait for it to be sent (no matter what), then
		// execute the command.
		if o.command != "" || o.wait {
			if err := waitForResponse(o.ctx, o.ns, id, o.timeout); err != nil {
				return "", fmt.Errorf("waiting for response: %w", err)
			}
		}

		if o.command == "" {
			return id, nil
		}
	}

	if o.command != "" {
		cmd := fmt.Sprintf("cc exec %s", o.command)

		id, err := exec(o.ns, o.vm, cmd)
		if err != nil {
			return "", fmt.Errorf("calling '%s' for vm %s: %w", cmd, o.vm, err)
		}

		if o.wait {
			if err := waitForResponse(o.ctx, o.ns, id, o.timeout); err != nil {
				return "", fmt.Errorf("waiting for response: %w", err)
			}
		}

		return id, nil
	}

	if o.mount != nil {
		if *o.mount {
			var (
				path = GetLocalMountPath(o.ns, o.vm)
				cmd  = fmt.Sprintf("cc mount %s %s", o.vm, path)
			)

			os.MkdirAll(path, os.ModePerm)

			id, err := exec(o.ns, o.vm, cmd)
			if err != nil {
				return "", fmt.Errorf("Error creating mount: %w", err)
			}

			return id, nil
		} else {
			cmd := fmt.Sprintf("clear cc mount %s", o.vm)

			id, err := exec(o.ns, o.vm, cmd)
			if err != nil {
				return "", fmt.Errorf("Error clearing mount: %w", err)
			}

			return id, nil
		}
	}

	return "", fmt.Errorf("no options to execute were provided")
}

func (Minimega) GetC2Response(opts ...C2Option) (string, error) {
	o := NewC2Options(opts...)

	if o.responseType == "" {
		return getResponse(o.ns, o.commandID)
	}

	if o.vm == "" {
		return "", fmt.Errorf("must provide VM when getting typed response")
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("cc response %s", o.commandID)

	resp, err := mmcli.SingleResponse(mmcli.Run(cmd))
	if err != nil {
		return "", fmt.Errorf("getting response for command %s: %w", o.commandID, err)
	}

	vms := GetVMInfo(NS(o.ns), VMName(o.vm))
	if len(vms) == 0 {
		return "", fmt.Errorf("VM %s does not exist", o.vm)
	}

	var (
		scanner = bufio.NewScanner(strings.NewReader(resp))
		uuid    = vms[0].UUID
	)

	var output []string

	for scanner.Scan() {
		line := scanner.Text()

		if match := responseRegex.FindStringSubmatch(line); match != nil {
			if len(output) > 0 {
				return strings.Join(output, "\n"), nil
			}

			if match[3] == string(o.responseType) && match[2] == uuid {
				output = []string{}
			}

			continue
		}

		if output != nil {
			output = append(output, line)
		}
	}

	if len(output) > 0 {
		return strings.Join(output, "\n"), nil
	}

	return "", nil
}

func (Minimega) WaitForC2Response(opts ...C2Option) (string, error) {
	o := NewC2Options(opts...)

	if err := waitForResponse(o.ctx, o.ns, o.commandID, o.timeout); err != nil {
		return "", err
	}

	return getResponse(o.ns, o.commandID)
}

func (Minimega) ClearC2Responses(opts ...C2Option) error {
	o := NewC2Options(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "clear cc responses"

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("clearing C2 responses for namespace %s: %w", o.ns, err)
	}

	return nil
}

func (this Minimega) TapVLAN(opts ...TapOption) error {
	o := NewTapOptions(opts...)

	if o.untap {
		plog.Info("deleting tap from host", "tap", o.name, "host", o.host)

		var errs error

		cmd := fmt.Sprintf("tap delete %s", o.name)
		if err := this.MeshSend(o.ns, o.host, cmd); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("deleting tap %s on node %s: %w", o.name, o.host, err))
		}

		if o.netns != "" {
			plog.Info("deleting network namespace from host", "ns", o.netns, "host", o.host)

			cmd := fmt.Sprintf("ip netns delete %s", o.netns)
			if err := this.MeshShell(o.host, cmd); err != nil {
				return fmt.Errorf("deleting netns %s on node %s: %w", o.netns, o.host, err)
			}
		}

		return errs
	}

	plog.Info("creating tap on host", "tap", o.name, "vlan", o.vlan, "bridge", o.bridge, "host", o.host)

	var cmd string

	if o.ip == "" || o.netns != "" {
		cmd = fmt.Sprintf(
			"tap create %s bridge %s name %s",
			o.vlan, o.bridge, o.name,
		)
	} else {
		cmd = fmt.Sprintf(
			"tap create %s bridge %s ip %s %s",
			o.vlan, o.bridge, o.ip, o.name,
		)
	}

	if err := this.MeshSend(o.ns, o.host, cmd); err != nil {
		return fmt.Errorf("creating tap %s on node %s: %w", o.name, o.host, err)
	}

	if o.netns != "" {
		plog.Info("creating network namespace for tap on host", "tap", o.name, "host", o.host)

		cmd := fmt.Sprintf("ip netns add %s", o.name)
		if err := this.MeshShell(o.host, cmd); err != nil {
			return fmt.Errorf("creating network namespace on host %s: %w", o.host, err)
		}

		plog.Info("moving tap to network namespace on host", "tap", o.name, "host", o.host)

		cmd = fmt.Sprintf("ip link set dev %s netns %s", o.name, o.name)
		if err := this.MeshShell(o.host, cmd); err != nil {
			return fmt.Errorf("moving tap to network namespace on host %s: %w", o.host, err)
		}

		plog.Info("bringing tap up in network namespace on host", "tap", o.name, "host", o.host)

		cmd = fmt.Sprintf("ip netns exec %s ip link set dev %s up", o.name, o.name)
		if err := this.MeshShell(o.host, cmd); err != nil {
			return fmt.Errorf("bringing tap up in network namespace on host %s: %w", o.host, err)
		}

		if o.ip != "" {
			plog.Info("setting IP address for tap in network namespace on host", "tap", o.name, "host", o.host)

			cmd := fmt.Sprintf("ip netns exec %s ip addr add %s dev %s", o.name, o.ip, o.name)
			if err := this.MeshShell(o.host, cmd); err != nil {
				return fmt.Errorf("setting IP address for tap in network namespace on host %s: %w", o.host, err)
			}
		}
	}

	return nil
}

func (Minimega) MeshShell(host, command string) error {
	cmd := mmcli.NewCommand()

	if host == "" {
		host = Headnode()
	}

	if IsHeadnode(host) {
		cmd.Command = fmt.Sprintf("shell %s", command)
	} else {
		cmd.Command = fmt.Sprintf("mesh send %s shell %s", host, command)
	}

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("running shell command (host %s) %s: %w", host, command, err)
	}

	return nil
}

func (Minimega) MeshSend(ns, host, command string) error {
	var cmd *mmcli.Command

	if ns == "" {
		cmd = mmcli.NewCommand()
	} else {
		cmd = mmcli.NewNamespacedCommand(ns)
	}

	if host == "" {
		host = Headnode()
	}

	if IsHeadnode(host) {
		cmd.Command = command
	} else {
		cmd.Command = fmt.Sprintf("mesh send %s %s", host, command)
	}

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("executing mesh send (%s): %w", cmd.Command, err)
	}

	return nil
}

// GetLocalMountPath returns where the mount path should be on this filesystem
// for the given namespace and VM.
func GetLocalMountPath(ns string, vm string) string {
	return filepath.Join(common.PhenixBase, "mounts", ns, vm)
}

func getActiveC2(ns string) map[string]bool {
	active := make(map[string]bool)

	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "cc client"

	for _, row := range mmcli.RunTabular(cmd) {
		active[row["uuid"]] = true
	}

	return active
}

func waitForResponse(ctx context.Context, ns, id string, timeout time.Duration) error {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "cc commands"
	cmd.Columns = []string{"id", "responses"}
	cmd.Filters = []string{"id=" + id}

	// Multiple rows will come back for each command ID, one row per cluster host.
	// Because the `ExecC2Command` sets the filter to a specific VM, only one of
	// the rows will have a response since a VM can only run on a single cluster
	// host.

	after := time.After(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-after:
			return fmt.Errorf("timeout waiting for response for command %s", id)
		default:
			rows := mmcli.RunTabular(cmd)

			if len(rows) == 0 {
				return fmt.Errorf("no commands returned for ID %s", id)
			}

			if rid := rows[0]["id"]; rid != id {
				return fmt.Errorf("wrong command returned: %s", rid)
			}

			for _, row := range rows {
				if row["responses"] != "0" {
					return nil
				}
			}

			time.Sleep(1 * time.Second)
		}
	}
}

func getResponse(ns, id string) (string, error) {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = fmt.Sprintf("cc response %s raw", id)

	resp, err := mmcli.SingleResponse(mmcli.Run(cmd))
	if err != nil {
		return "", fmt.Errorf("getting response for command %s: %w", id, err)
	}

	return resp, nil
}

func flush(ns string) error {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "vm flush"

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("flushing VMs in namespace %s: %w", ns, err)
	}

	return nil
}

func inject(disk string, part int, injects ...string) error {
	files := strings.Join(injects, " ")

	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("disk inject %s:%d files %s", disk, part, files)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("injecting files into disk %s: %w", disk, err)
	}

	return nil
}

// Ugh... replicating `file.DeleteFile` here to avoid cyclical dependency
// between mm and file packages.
func deleteFile(path string) error {
	// First delete file from mesh, then from headnode.
	commands := []string{"mesh send all file delete", "file delete"}

	cmd := mmcli.NewCommand()

	for _, command := range commands {
		cmd.Command = fmt.Sprintf("%s %s", command, path)

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("deleting file from cluster nodes: %w", err)
		}
	}

	return nil
}

func processNamespaceHosts(namespace string) (Hosts, error) {
	cmd := mmcli.NewNamespacedCommand(namespace)
	cmd.Command = "host"

	var (
		hosts  Hosts
		status = mmcli.RunTabular(cmd)
	)

	for _, row := range status {
		host := Host{Name: row["host"]}
		host.CPUs, _ = strconv.Atoi(row["cpus"])
		host.CPUCommit, _ = strconv.Atoi(row["cpucommit"])
		host.Load = strings.Split(row["load"], " ")
		host.MemUsed, _ = strconv.Atoi(row["memused"])
		host.MemTotal, _ = strconv.Atoi(row["memtotal"])
		host.MemCommit, _ = strconv.Atoi(row["memcommit"])
		host.VMs, _ = strconv.Atoi(row["vms"])

		host.Tx, _ = strconv.ParseFloat(row["tx"], 64)
		host.Rx, _ = strconv.ParseFloat(row["rx"], 64)
		host.Bandwidth = fmt.Sprintf("rx: %.1f / tx: %.1f", host.Rx, host.Tx)
		host.NetCommit, _ = strconv.Atoi(row["netcommit"])

		uptime, _ := time.ParseDuration(row["uptime"])
		host.Uptime = uptime.Seconds()

		hosts = append(hosts, host)
	}

	return hosts, nil
}
