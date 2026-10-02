package mm

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/activeshadow/libminimega/ranges"
	"github.com/hashicorp/go-multierror"

	"phenix/util/common"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

var (
	ErrCaptureExists      = errors.New("capture already exists")
	ErrNoCaptures         = errors.New("no captures exist")
	ErrC2ClientNotActive  = errors.New("C2 client not active for VM")
	ErrVMNotFound         = errors.New("VM not found")
	ErrScreenshotNotFound = errors.New("screenshot not found")
)

const (
	vmInfoCmd        = "vm info"
	vmInfoSummaryCmd = "vm info summary"
	vmLaunchCmd      = "vm launch"
	nsQueueCmd       = "ns queue"
	ccClientCmd      = "cc client"
	ccCommandsCmd    = "cc commands"
	clearCCFilterCmd = "clear cc filter"
	captureCmd       = "capture"

	// phenixHostsNS is a namespace phenix only runs `host` in, to reach every
	// compute node.
	phenixHostsNS = "__phenix__"
)

const (
	hostColumn             = "host"
	stateColumn            = "state"
	c2ActiveCheckInterval  = 2 * time.Second
	responseWaitInterval   = 1 * time.Second
	responseRegexGroupUUID = 2
	responseRegexGroupType = 3
)

// headnodeTTL is how long a looked-up headnode name is reused before it is
// looked up again. It rarely changes, but can while phenix keeps running (say,
// minimega's container is recreated under another name).
const headnodeTTL = 5 * time.Minute

// Cache of the last known good headnode name, and when it was looked up.
var headnode struct { //nolint:gochecknoglobals // process-wide cache
	mu   sync.Mutex
	name string
	at   time.Time
}

// Regular express to use for matching C2 response headers.
var responseRegex = regexp.MustCompile(`(\d*)\/(.*)\/(stdout|stderr):`)

var digitRuns = regexp.MustCompile(`\d+`)

// queuedCountRegex matches the number of VMs in each batch `ns queue` lists.
// The batch's `Names:` line is no use for counting: minimega compresses it into
// ranges such as `vm[1-50]`.
var queuedCountRegex = regexp.MustCompile(`(?m)^VMs: (\d+)$`)

type Minimega struct{}

// ReadScriptFromFile runs `read` against ns. The namespace has to travel
// with the command: minimega's `read` prefixes every line of the script
// with the namespace that was active when it started.
func (Minimega) ReadScriptFromFile(ns, filename string) error {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "read " + filename

	// A script can take minutes to run; don't hold the shared connection (and
	// so every other command) for all of it.
	err := mmcli.ErrorResponse(mmcli.RunDedicated(cmd))
	if err != nil {
		return fmt.Errorf("reading mmcli script: %w", err)
	}

	return nil
}

func (Minimega) ClearNamespace(ns string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = "clear namespace " + ns

	// The namespace's VMs are gone (or going) either way.
	forgetSnapshotDisks(ns)

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("clearing minimega namespace: %w", err)
	}

	return nil
}

func (Minimega) LaunchVMs(ns string, start ...string) error {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = vmLaunchCmd

	// Launching every queued VM can take minutes; don't hold the shared
	// connection (and so every other command, including launch progress
	// polling) for all of it.
	err := mmcli.ErrorResponse(mmcli.RunDedicated(cmd))
	if err != nil {
		return fmt.Errorf("launching VMs: %w", err)
	}

	if start == nil {
		cmd.Command = "vm start all"

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("starting VMs: %w", err)
		}
	} else {
		for _, batch := range vmStartBatches(start) {
			err := startVMs(ns, batch)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

const (
	// maxVMStartBatch and maxVMStartTarget bound how many VMs, and how long a
	// target list, one `vm start` names.
	maxVMStartBatch  = 100
	maxVMStartTarget = 4096
)

// vmStartBatches groups the VMs to start into `vm start` target lists, in
// order. `vm start` in a namespace runs on every node in it, so each command
// costs a mesh round trip. A VM whose name minimega would not read back as
// itself from a comma-separated list (see batchableVMName) gets a batch of its
// own.
func vmStartBatches(names []string) [][]string {
	var (
		batches [][]string
		current []string
		length  int
	)

	flush := func() {
		if len(current) > 0 {
			batches = append(batches, current)
		}

		current, length = nil, 0
	}

	for _, name := range names {
		if !batchableVMName(name) {
			flush()

			batches = append(batches, []string{name})

			continue
		}

		if len(current) == maxVMStartBatch || length+len(name)+1 > maxVMStartTarget {
			flush()
		}

		current = append(current, name)
		length += len(name) + 1
	}

	flush()

	return batches
}

// batchableVMName reports whether name can be one of several in a `vm start`
// target list. minimega splits the list on commas and expands brackets as
// ranges (ranges.SplitList), and treats "all" as every VM, so names with any of
// those, or that the command line would split or unquote, are started alone.
func batchableVMName(name string) bool {
	if name == "" || name == "all" {
		return false
	}

	return !strings.ContainsAny(name, ",[]\"'` \t\r\n")
}

// startVMs starts the named VMs with one `vm start` on a connection of its own
// (starting many VMs can take a while). The error for a single VM names it. For
// several, minimega reports each failure without the VM's name, so the VMs of
// the batch left in the error state are looked up to name them.
func startVMs(ns string, names []string) error {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "vm start " + strings.Join(names, ",")

	err := mmcli.ErrorResponse(mmcli.RunDedicated(cmd))
	if err == nil {
		return nil
	}

	if len(names) == 1 {
		return fmt.Errorf("starting VM %s: %w", names[0], err)
	}

	if failed := vmsInErrorState(ns, names); len(failed) > 0 {
		return fmt.Errorf("starting VMs %s (of %s): %w",
			strings.Join(failed, ", "), strings.Join(names, ", "), err)
	}

	return fmt.Errorf("starting VMs %s: %w", strings.Join(names, ", "), err)
}

// vmsInErrorState returns which of the named VMs minimega reports in the error
// state, in the order given.
func vmsInErrorState(ns string, names []string) []string {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = vmInfoSummaryCmd
	cmd.Columns = []string{"name", stateColumn}

	errored := make(map[string]bool)

	for _, row := range mmcli.RunTabular(cmd) {
		if strings.EqualFold(row[stateColumn], "ERROR") {
			errored[row["name"]] = true
		}
	}

	var failed []string

	for _, name := range names {
		if errored[name] {
			failed = append(failed, name)
		}
	}

	return failed
}

func (Minimega) GetLaunchProgress(ns string, expected int) (float64, error) {
	var (
		queued   int
		parseErr error
	)

	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = nsQueueCmd

	for resps := range mmcli.Run(cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				continue
			}

			for _, m := range queuedCountRegex.FindAllStringSubmatch(resp.Response, -1) {
				count, err := strconv.Atoi(m[1])
				if err != nil {
					// Keep draining the responses; see mmcli.Run.
					parseErr = fmt.Errorf("parsing queued VM count %q: %w", m[1], err)

					continue
				}

				queued += count
			}
		}
	}

	if parseErr != nil {
		return 0, parseErr
	}

	// `ns queue` will be empty once queued VMs have been launched.

	if queued == 0 {
		// Only the state is wanted, and `vm info summary` has it without
		// gathering every other VM detail on each node.
		cmd.Command = vmInfoSummaryCmd
		cmd.Columns = []string{stateColumn}

		status := mmcli.RunTabular(cmd)

		if len(status) == 0 {
			return 0.0, nil
		}

		for _, s := range status {
			if s[stateColumn] == "BUILDING" {
				queued++
			}
		}
	}

	return float64(queued) / float64(expected), nil
}

// GetVMInfo returns minimega's details for the VMs in a namespace, optionally
// filtered by VM name. Concurrent calls asking for the same VMs share one set of
// minimega commands (see flightGroup); each caller gets its own copy.
func (m Minimega) GetVMInfo(opts ...Option) VMs {
	o := NewOptions(opts...)

	vms, err := vmInfoFlights.do(o.ns+"\x00"+o.vm, func(seal func()) VMs {
		return m.getVMInfo(o, seal)
	})
	if err != nil {
		// The run this call joined panicked. GetVMInfo has no error to return,
		// so it answers an empty list, as it does when minimega can't be read.
		plog.Error(plog.TypeSystem, "getting VM info", "ns", o.ns, "vm", o.vm, "error", err)

		return nil
	}

	return cloneVMs(vms)
}

// getVMInfo does GetVMInfo's work, calling started just before its first
// minimega command is sent.
func (m Minimega) getVMInfo(o options, started func()) VMs {
	// don't rely on `cc_active` column in `vm info` table
	activeC2 := getActiveC2(o.ns, started)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{
		"uuid",
		hostColumn,
		"name",
		stateColumn,
		"uptime",
		"vlan",
		"tap",
		"ip",
		"memory",
		"vcpus",
		"disks",
		"snapshot",
		"cdrom",
		"tags",
	}

	if o.vm != "" {
		cmd.Filters = []string{"name=" + o.vm}
	}

	status := mmcli.RunTabular(cmd)
	received := time.Now()
	vms := make(VMs, 0, len(status))

	var captures map[string][]Capture

	// One `capture` listing for the namespace, rather than one per VM.
	if len(status) > 0 {
		captures = GroupCapturesByVM(m.GetExperimentCaptures(NS(o.ns)))
	}

	for _, row := range status {
		vm := VM{ //nolint:exhaustruct // partial initialization
			UUID:     row["uuid"],
			Host:     row[hostColumn],
			Name:     row["name"],
			State:    row[stateColumn],
			Running:  row[stateColumn] == "RUNNING",
			CCActive: activeC2[row["uuid"]],
			CdRom:    row["cdrom"],
			Networks: parseList(row["vlan"]),
			Taps:     parseList(row["tap"]),
			IPv4:     parseList(row["ip"]),
			Captures: captures[row["name"]],
		}

		var tags map[string]string

		_ = json.Unmarshal([]byte(row["tags"]), &tags)
		vm.Tags = tags

		uptime, uptimeErr := time.ParseDuration(row["uptime"])
		if uptimeErr == nil {
			vm.Uptime = uptime.Seconds()
		}

		vm.RAM, _ = strconv.Atoi(row["memory"])
		vm.CPUs, _ = strconv.Atoi(row["vcpus"])

		var disk string
		// Multiple disks are space-separated (minimega DiskConfigs.String).
		if fields := strings.Fields(row["disks"]); len(fields) > 0 {
			// Each diskspec is comma-separated (path,interface,cache); path is first.
			disk = strings.Split(fields[0], ",")[0]
		}

		snapshot, _ := strconv.ParseBool(row["snapshot"])

		if snapshot && disk != "" {
			launch := vmLaunch{ //nolint:exhaustruct // at is only known with an uptime
				ns:   o.ns,
				name: vm.Name,
				uuid: vm.UUID,
				host: vm.Host,
				disk: disk,
			}

			// The disk's backing file can only change while its VM's process is
			// gone, so a result is only reused for the same launch of a VM whose
			// process is still up.
			if uptimeErr == nil && (vm.State == "RUNNING" || vm.State == "PAUSED") {
				launch.at = received.Add(-uptime)
			}

			vm.Disk = snapshotBackingDisk(launch)
		} else if disk != "" {
			// Attempting to get disk info when not using a snapshot will cause a
			// locked file error.
			vm.Disk = filepath.Clean(disk)
		}

		vms = append(vms, vm)
	}

	return vms
}

// snapshotBackingDisk returns the image a VM's snapshot disk is based on (or
// the snapshot itself if minimega can't say), asking minimega at most once per
// launch of the VM.
func snapshotBackingDisk(launch vmLaunch) string {
	if disk, ok := snapshotDisks.lookup(launch); ok {
		return disk
	}

	cmd := mmcli.NewCommand()
	cmd.Command = "disk info " + launch.disk

	if !IsHeadnode(launch.host) {
		cmd.Command = fmt.Sprintf("mesh send %s %s", launch.host, cmd.Command)
	}

	resp := mmcli.RunTabular(cmd)

	if len(resp) == 0 {
		return launch.disk
	}

	// Only expect one row returned
	info := resp[0]

	disk := info["backingfile"]

	switch {
	case disk == "":
		disk = info["image"]
	case filepath.IsAbs(disk):
		disk = CleanBackingPath(disk)
	default:
		// minimega names the backing file relative to the snapshot's directory
		// unless it runs with -abssnapshot
		disk = CleanBackingPath(filepath.Dir(launch.disk) + "/" + disk)
	}

	snapshotDisks.store(launch, disk)

	return disk
}

func (Minimega) GetVMScreenshot(opts ...Option) ([]byte, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm screenshot %s file /dev/null %s", o.vm, o.screenshotSize)

	var (
		screenshot []byte
		err        error
	)

	// Drain the full response channel
	for resps := range mmcli.Run(cmd) {
		for _, resp := range resps.Resp {
			if screenshot != nil {
				continue
			}

			if resp.Error != "" {
				if strings.HasPrefix(resp.Error, "vm not found:") || strings.HasPrefix(resp.Error, "vm not running:") {
					err = ErrVMNotFound
				}

				continue
			}

			if resp.Data == nil {
				continue
			}

			data, _ := resp.Data.(string)

			decoded, decErr := base64.StdEncoding.DecodeString(data)
			if decErr != nil {
				err = fmt.Errorf("decoding screenshot: %w", decErr)
				continue
			}

			screenshot = decoded
		}
	}

	// A screenshot found on any host wins over any error recorded from another
	if screenshot != nil {
		return screenshot, nil
	}

	if err != nil {
		return nil, err
	}

	return nil, ErrScreenshotNotFound
}

func (Minimega) GetVNCEndpoint(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{hostColumn, "vnc_port"}
	cmd.Filters = []string{"type=kvm", "name=" + o.vm}

	var endpoint string

	for _, vm := range mmcli.RunTabular(cmd) {
		endpoint = fmt.Sprintf("%s:%s", vm[hostColumn], vm["vnc_port"])
	}

	if endpoint == "" {
		return "", errors.New("not found")
	}

	return endpoint, nil
}

func (Minimega) StartVM(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm start " + o.vm

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("starting VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) StopVM(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm stop " + o.vm

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("stopping VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) RedeployVM(opts ...Option) error { //nolint:funlen // complex logic
	o := NewOptions(opts...)

	// The VM's snapshot disk may be recreated from a different image.
	forgetSnapshotDisks(o.ns)

	cmd := mmcli.NewNamespacedCommand(o.ns)

	// Get VM info before killing VM below.
	cmd.Command = vmInfoCmd
	cmd.Filters = []string{"name=" + o.vm}

	info := mmcli.RunTabular(cmd)
	if len(info) == 0 {
		return fmt.Errorf("no info found for VM %s in namespace %s", o.vm, o.ns)
	}

	cmd.Filters = nil

	// From `vm config clone` to `vm launch`, the namespace's VM config and
	// launch queue are this VM's alone.
	defer vmConfigLocks.lock(o.ns)()

	cmd.Command = "vm config clone " + o.vm

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("cloning VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	cmd.Command = "clear vm config state"

	err = mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("clearing config for VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	cmd.Command = "vm kill " + o.vm

	err = mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("killing VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	err = flushVM(o.ns, o.vm)
	if err != nil {
		return err
	}

	if o.cpu != 0 {
		cmd.Command = fmt.Sprintf("vm config vcpus %d", o.cpu)

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("configuring VCPUs for VM %s in namespace %s: %w", o.vm, o.ns, err)
		}
	}

	if o.mem != 0 {
		cmd.Command = fmt.Sprintf("vm config mem %d", o.mem)

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
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
				newDisk := newDiskConfig(o.disk)

				// Delete disk snapshot file across cluster
				err := deleteFile(old.base)
				if err != nil {
					return fmt.Errorf("deleting old disk snapshot: %w", err)
				}

				cmd.Command = fmt.Sprintf("disk snapshot %s %s", newDisk.path, old.base)

				err = mmcli.ErrorResponse(mmcli.Run(cmd))
				if err != nil {
					return fmt.Errorf(
						"snapshotting disk for VM %s in namespace %s: %w",
						o.vm,
						o.ns,
						err,
					)
				}

				err = inject(old.base, o.injectPart, o.injects...)
				if err != nil {
					return err
				}

				// Use disk cache mode if provided by user. Otherwise, use original disk
				// cache mode.
				disk = old.string(newDisk.cache)
			} else {
				disk = o.disk
			}
		}

		cmd.Command = "vm config disk " + disk

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("configuring disk for VM %s in namespace %s: %w", o.vm, o.ns, err)
		}
	}

	cmd.Command = "vm launch kvm " + o.vm

	err = mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("scheduling VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	cmd.Command = vmLaunchCmd

	err = mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("launching scheduled VMs in namespace %s: %w", o.ns, err)
	}

	cmd.Command = "vm start " + o.vm

	err = mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("starting VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) KillVM(opts ...Option) error {
	o := NewOptions(opts...)

	forgetSnapshotDisks(o.ns)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm kill " + o.vm

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("killing VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return flushVM(o.ns, o.vm)
}

func (Minimega) GetVMHost(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{hostColumn}
	cmd.Filters = []string{"name=" + o.vm}

	status := mmcli.RunTabular(cmd)

	if len(status) == 0 {
		return "", fmt.Errorf("vm %s not found", o.vm)
	}

	return status[0][hostColumn], nil
}

// GetVMHosts returns the host each VM in a namespace (optionally filtered by VM
// name) is running on, keyed by VM name, using a single narrow `vm info`.
func (Minimega) GetVMHosts(opts ...Option) map[string]string {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{hostColumn, "name"}

	if o.vm != "" {
		cmd.Filters = []string{"name=" + o.vm}
	}

	hosts := make(map[string]string)

	for _, row := range mmcli.RunTabular(cmd) {
		hosts[row["name"]] = row[hostColumn]
	}

	return hosts
}

// GetVMIPv4 returns the IPv4 addresses minimega reports for a VM's interfaces,
// in interface order (nil if it reports none), using a single narrow `vm info`.
func (Minimega) GetVMIPv4(opts ...Option) ([]string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"ip"}
	cmd.Filters = []string{"name=" + o.vm}

	rows := mmcli.RunTabular(cmd)
	if len(rows) == 0 {
		return nil, fmt.Errorf("vm %s in namespace %s: %w", o.vm, o.ns, ErrVMNotFound)
	}

	return parseList(rows[0]["ip"]), nil
}

// getVMIdentities returns the name and UUID of the VMs in a namespace
// (optionally filtered by VM name), and nothing else, using a single narrow
// `vm info`.
func getVMIdentities(ns, name string) VMs {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"name", "uuid"}

	if name != "" {
		cmd.Filters = []string{"name=" + name}
	}

	rows := mmcli.RunTabular(cmd)
	vms := make(VMs, 0, len(rows))

	for _, row := range rows {
		vms = append(vms, VM{Name: row["name"], UUID: row["uuid"]}) //nolint:exhaustruct // partial initialization
	}

	return vms
}

// GetVMStates returns the state minimega reports for each VM in the
// namespace, by VM name, from one narrow `vm info summary`. Unlike GetVMInfo
// it returns an error when minimega (or one of its hosts) cannot answer, so a
// caller can tell an unreachable minimega from a namespace without VMs.
func (Minimega) GetVMStates(opts ...Option) (map[string]string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoSummaryCmd
	cmd.Columns = []string{"name", stateColumn}

	var (
		states = make(map[string]string)
		errs   []string
	)

	for resps := range mmcli.Run(cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				errs = append(errs, resp.Error)

				continue
			}

			name, state := slices.Index(resp.Header, "name"), slices.Index(resp.Header, stateColumn)
			if name < 0 || state < 0 {
				continue
			}

			for _, row := range resp.Tabular {
				if name < len(row) && state < len(row) {
					states[row[name]] = row[state]
				}
			}
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("getting VM states in namespace %s: %s", o.ns, strings.Join(errs, "; "))
	}

	return states, nil
}

func (Minimega) GetVMState(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm info summary"
	cmd.Columns = []string{stateColumn}
	cmd.Filters = []string{"name=" + o.vm}

	status := mmcli.RunTabular(cmd)

	if len(status) == 0 {
		return "", fmt.Errorf("vm %s not found", o.vm)
	}

	return status[0][stateColumn], nil
}

func (Minimega) SetVMTags(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("clear vm tag %s ", o.vm)

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("failed to clear tags for vm %s: %w", o.vm, err)
	}

	for k, v := range o.tags {
		cmd.Command = fmt.Sprintf("vm tag %s \"%s\" \"%s\"", o.vm, k, v)

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("failed to set tag for vm %s: %s=%s %w", o.vm, k, v, err)
		}
	}

	return nil
}

func (Minimega) ConnectVMInterface(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = connectVMInterfaceCommand(o)

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf(
			"connecting interface %d on VM %s to VLAN %s in namespace %s: %w",
			o.connectIface,
			o.vm,
			o.connectVLAN,
			o.ns,
			err,
		)
	}

	return nil
}

func connectVMInterfaceCommand(o options) string {
	cmd := fmt.Sprintf("vm net connect %s %d %s", o.vm, o.connectIface, o.connectVLAN)
	if o.bridge != "" {
		cmd += " " + o.bridge
	}

	return cmd
}

func (Minimega) DisconnectVMInterface(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm net disconnect %s %d", o.vm, o.connectIface)

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf(
			"disconnecting interface %d on VM %s in namespace %s: %w",
			o.connectIface,
			o.vm,
			o.ns,
			err,
		)
	}

	return nil
}

func (Minimega) CreateBridge(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("ns bridge %s gre", o.bridge)

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf(
			"creating bridge %s with GRE mesh between namespace hosts: %w",
			o.bridge,
			err,
		)
	}

	return nil
}

func (Minimega) CreateTunnel(opts ...Option) error {
	host, err := GetVMHost(opts...)
	if err != nil {
		return fmt.Errorf("unable to determine what host the VM is scheduled on: %w", err)
	}

	o := NewOptions(opts...)

	var cmdPrefix string

	if !IsHeadnode(host) {
		cmdPrefix = fmt.Sprintf("mesh send %s namespace %s", host, o.ns)
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf(
		"%s cc tunnel %s %d %s %d",
		cmdPrefix,
		o.vm,
		o.srcPort,
		o.dstHost,
		o.dstPort,
	)

	// For a VM on the headnode, the command runs on every node in the
	// namespace, and the others answer that the VM isn't found.
	if err := mmcli.VMTargetErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf(
			"creating tunnel to %s (%d:%s:%d): %w",
			o.vm,
			o.srcPort,
			o.dstHost,
			o.dstPort,
			err,
		)
	}

	return nil
}

func (Minimega) GetTunnels(opts ...Option) []map[string]string {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "cc tunnel list all"

	if o.vm != "" {
		cmd.Command = "cc tunnel list " + o.vm
	}

	if o.dstHost != "" {
		cmd.Filters = append(cmd.Filters, "dst="+o.dstHost)
	}

	if o.dstPort != 0 {
		cmd.Filters = append(cmd.Filters, fmt.Sprintf("'dst port'=%d", o.dstPort))
	}

	// The source port is the tunnel's own port on the cluster host, and tells
	// apart tunnels different users opened to the same destination.
	if o.srcPort != 0 {
		cmd.Filters = append(cmd.Filters, fmt.Sprintf("'src port'=%d", o.srcPort))
	}

	return mmcli.RunTabular(cmd)
}

func (Minimega) CloseTunnel(opts ...Option) error {
	tunnels := GetTunnels(opts...)

	host, err := GetVMHost(opts...)
	if err != nil {
		return fmt.Errorf("unable to determine what host the VM is scheduled on: %w", err)
	}

	o := NewOptions(opts...)

	var (
		cmdPrefix string
		errs      error
	)

	if !IsHeadnode(host) {
		cmdPrefix = fmt.Sprintf("mesh send %s namespace %s", host, o.ns)
	}

	for _, row := range tunnels {
		cmd := mmcli.NewNamespacedCommand(o.ns)
		cmd.Command = fmt.Sprintf("%s cc tunnel close %s %s", cmdPrefix, o.vm, row["id"])

		// See CreateTunnel.
		err := mmcli.VMTargetErrorResponse(mmcli.Run(cmd))
		if err != nil {
			errs = multierror.Append(
				errs,
				fmt.Errorf("closing tunnel to %s (%s:%d): %w", o.vm, o.dstHost, o.dstPort, err),
			)
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
		return errors.New("path for capture file should not be absolute")
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
		return fmt.Errorf(
			"starting VM capture for interface %d on VM %s in namespace %s: %w",
			o.captureIface,
			o.vm,
			o.ns,
			err,
		)
	}

	return nil
}

// StopVMCapture stops packet captures for a VM. If the CaptureInterface
// option is provided, only the capture running on that interface index is
// stopped, leaving any other captures for the VM running. Otherwise, all
// captures for the VM are stopped.
func (Minimega) StopVMCapture(opts ...Option) error {
	o := NewOptions(opts...)

	captures := GetVMCaptures(opts...)

	if o.captureIfaceSet {
		var found bool

		for _, capture := range captures {
			if capture.Interface == o.captureIface {
				found = true
				break
			}
		}

		if !found {
			return ErrNoCaptures
		}
	} else if len(captures) == 0 {
		return ErrNoCaptures
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)

	if o.captureIfaceSet {
		cmd.Command = fmt.Sprintf("capture pcap delete vm %s %d", o.vm, o.captureIface)
	} else {
		cmd.Command = "capture pcap delete vm " + o.vm
	}

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		if o.captureIfaceSet {
			return fmt.Errorf(
				"deleting VM capture for interface %d on VM %s in namespace %s: %w",
				o.captureIface,
				o.vm,
				o.ns,
				err,
			)
		}

		return fmt.Errorf("deleting VM captures for VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	return nil
}

func (Minimega) GetExperimentCaptures(opts ...Option) []Capture {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = captureCmd
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
		vm, idxStr, ok := strings.Cut(row["interface"], ":")
		if !ok {
			plog.Warn(
				plog.TypeSystem,
				"unexpected capture interface format",
				"interface", row["interface"],
			)

			continue
		}

		idx, _ := strconv.Atoi(idxStr)

		capture := Capture{
			VM:        vm,
			Interface: idx,
			Filepath:  row["path"],
		}

		captures = append(captures, capture)
	}

	return captures
}

func (m Minimega) GetVMCaptures(opts ...Option) []Capture {
	o := NewOptions(opts...)

	var (
		captures = m.GetExperimentCaptures(opts...)
		keep     []Capture
	)

	for _, capture := range captures {
		if capture.VM == o.vm {
			keep = append(keep, capture)
		}
	}

	return keep
}

// GetClusterHosts returns the cluster's hosts, or only those VMs can be
// scheduled on.
//
// A schedOnly call is always answered from minimega: the schedulers (and apps
// given it, see app.PopulateRuntime) place VMs by each host's VM count and
// commit, which starting another experiment changes moments later. The full
// list is only shown to people (the Hosts page, which polls it, and the
// experiment page's host choices), so it is reused for up to clusterHostsTTL.
// Both kinds share one set of minimega commands with concurrent calls (see
// flightGroup), and every fresh answer refreshes the reused one. Each caller
// gets its own copy.
func (m Minimega) GetClusterHosts(schedOnly bool) (Hosts, error) {
	if !schedOnly {
		if hosts, ok := recentHosts.get(); ok {
			return cloneHosts(hosts), nil
		}
	}

	res, err := clusterHostsFlights.do("", func(seal func()) clusterHosts {
		at := time.Now()

		hosts, err := m.getClusterHosts(seal)
		if err == nil {
			recentHosts.put(hosts, at)
		}

		return clusterHosts{hosts: hosts, err: err}
	})
	if err != nil {
		return nil, fmt.Errorf("getting cluster hosts: %w", err)
	}

	if res.err == nil && schedOnly {
		return schedulableHosts(res.hosts), nil
	}

	return cloneHosts(res.hosts), res.err
}

// getClusterHosts does GetClusterHosts' work for the full list, calling
// started just before its first minimega command is sent. The headnode is
// always last.
func (m Minimega) getClusterHosts(started func()) (Hosts, error) {
	// Get headnode details. The default namespace only ever spans this node.
	hosts := processNamespaceHosts("minimega", started)

	if len(hosts) == 0 {
		return []Host{}, errors.New("no cluster hosts found")
	}

	head := hosts[0]
	head.Schedulable = false
	head.Headnode = true

	rememberHeadnode(head.Name)

	var cluster []Host

	// Recreate the dummy namespace used for getting compute nodes if the mesh
	// has changed since it was created (see phenixNamespaceCurrent).
	if !phenixNamespaceCurrent(head.Name) {
		_ = m.ClearNamespace(phenixHostsNS)
	}

	// Get compute nodes details
	hosts = processNamespaceHosts(phenixHostsNS, nil)

	for _, host := range hosts {
		// This will happen if the headnode is included as a compute node
		// (ie. when there's only one node in the cluster).
		if host.Name == head.Name {
			head.Schedulable = true

			continue
		}

		host.Name = common.TrimHostnameSuffixes(host.Name)
		host.Schedulable = true

		// Add disk info
		host.DiskUsage = m.getHostDiskUsage(host.Name)

		cluster = append(cluster, host)
	}

	head.Name = common.TrimHostnameSuffixes(head.Name)

	// Add disk info
	head.DiskUsage = m.getHostDiskUsage(head.Name)

	cluster = append(cluster, head)

	return cluster, nil
}

// phenixNamespaceCurrent reports whether the dummy namespace phenix runs `host`
// in to reach every compute node already spans exactly the nodes it would if
// it were created now, so it need not be destroyed and recreated.
//
// minimega fixes a namespace's hosts when it creates the namespace: every mesh
// peer, or just the local node when it has none. Nodes joining or leaving the
// mesh later are not picked up. Clearing the namespace before every `host`
// would pick them up, but costs a `clear namespace` broadcast to the whole mesh
// plus tearing down and rebuilding the namespace on every node, twice as many
// mesh commands as the `host` itself. Instead, the namespace's hosts are
// compared with the current mesh peers (both local commands) and it is only
// recreated when they differ, or when either can't be read (such as a minimega
// without `mesh list peers`).
func phenixNamespaceCurrent(self string) bool {
	peers, err := hostList(mmcli.NewCommand(), "mesh list peers")
	if err != nil {
		return false
	}

	// Looking the namespace's hosts up creates it, with the current peers, if
	// it doesn't exist (after minimega restarted, say).
	members, err := hostList(mmcli.NewNamespacedCommand(phenixHostsNS), "ns hosts")
	if err != nil {
		return false
	}

	if len(peers) == 0 {
		peers = []string{self}
	}

	return sameHostSet(peers, members)
}

// hostList runs a command answering with a minimega host list such as
// `node[1-3],head` and expands it.
func hostList(cmd *mmcli.Command, command string) ([]string, error) {
	cmd.Command = command

	resp, err := mmcli.SingleResponse(mmcli.Run(cmd))
	if err != nil {
		return nil, fmt.Errorf("running %s: %w", command, err)
	}

	hosts, err := ranges.SplitList(strings.TrimSpace(resp))
	if err != nil {
		return nil, fmt.Errorf("parsing %s response %q: %w", command, resp, err)
	}

	return hosts, nil
}

// sameHostSet reports whether two host lists name the same hosts. minimega
// compresses host lists into ranges, dropping leading zeros on the way (node01
// comes back as node1), so names are compared with them dropped.
func sameHostSet(a, b []string) bool {
	set := func(names []string) []string {
		out := make([]string, len(names))

		for i, name := range names {
			out[i] = withoutLeadingZeros(name)
		}

		slices.Sort(out)

		return slices.Compact(out)
	}

	return slices.Equal(set(a), set(b))
}

// withoutLeadingZeros drops the leading zeros from every number in name.
func withoutLeadingZeros(name string) string {
	return digitRuns.ReplaceAllStringFunc(name, func(digits string) string {
		if trimmed := strings.TrimLeft(digits, "0"); trimmed != "" {
			return trimmed
		}

		return "0"
	})
}

// schedulableHosts copies the hosts VMs can be scheduled on out of
// getClusterHosts' full list, which is nil when there are none.
func schedulableHosts(hosts Hosts) Hosts {
	if n := len(hosts); n > 0 && hosts[n-1].Headnode && !hosts[n-1].Schedulable {
		hosts = hosts[:n-1]
	}

	if len(hosts) == 0 {
		return nil
	}

	return cloneHosts(hosts)
}

func (m Minimega) GetNamespaceHosts(ns string) (Hosts, error) {
	// Get namespace nodes details
	processed := processNamespaceHosts(ns, nil)

	hosts := make([]Host, 0, len(processed))

	for _, host := range processed {
		host.Name = common.TrimHostnameSuffixes(host.Name)

		// Add disk info
		host.DiskUsage = m.getHostDiskUsage(host.Name)

		hosts = append(hosts, host)
	}

	return hosts, nil
}

// Headnode returns the name of the host phenix's minimega runs on. It is
// looked up at most once every headnodeTTL; when a lookup fails, the last
// known name (if any) is returned.
func (Minimega) Headnode() string {
	headnode.mu.Lock()
	name, at := headnode.name, headnode.at
	headnode.mu.Unlock()

	if name != "" && time.Since(at) < headnodeTTL {
		return name
	}

	// Get headnode details
	hosts := processNamespaceHosts("minimega", nil)

	if len(hosts) == 0 {
		// hosts is empty on any mmcli failure, fall back to the last known
		// headnode, if any
		headnode.mu.Lock()
		defer headnode.mu.Unlock()

		return headnode.name
	}

	return rememberHeadnode(hosts[0].Name)
}

// rememberHeadnode caches the headnode's name, as `host` in the default
// namespace reported it, and returns it as cached.
func rememberHeadnode(name string) string {
	// Trim host name suffixes (like -minimega, or -phenix) potentially added to
	// Docker containers by Docker Compose config.
	name = common.TrimHostnameSuffixes(name)

	headnode.mu.Lock()
	defer headnode.mu.Unlock()

	headnode.name, headnode.at = name, time.Now()

	return name
}

func (m Minimega) IsHeadnode(node string) bool {
	// Trim node name suffixes (like -minimega, or -phenix) potentially added to
	// Docker containers by Docker Compose config.
	node = common.TrimHostnameSuffixes(node)

	if node == m.Headnode() {
		return true
	}

	// Fall back to the local hostname
	if local, err := os.Hostname(); err == nil {
		return node == common.TrimHostnameSuffixes(local)
	}

	return false
}

func (m Minimega) GetMMArgs() (map[string]string, error) {
	cmd := mmcli.NewCommand()
	cmd.Command = "args"

	rows := mmcli.RunTabular(cmd)
	if len(rows) == 1 {
		return rows[0], nil
	}

	return nil, errors.New("no args returned")
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

func (m Minimega) IsC2ClientActive(opts ...C2Option) error {
	_, _, err := m.c2Client(NewC2Options(opts...))

	return err
}

// pickVM returns the VM named exactly `name`, else the first match. The lookup
// folds case, so a namespace holding two names differing only in case would
// otherwise be able to resolve to the wrong VM.
func pickVM(vms VMs, name string) VM {
	for _, vm := range vms {
		if vm.Name == name {
			return vm
		}
	}

	// If exact match not found, return the first match.
	return vms[0]
}

// c2Client waits for a VM's miniccc client to register and returns the name
// minimega launched the VM under along with its UUID. Callers must address the
// VM by one of those: this lookup folds case (minicli's `.filter` lowercases
// both sides) while `cc filter name=`, `cc mount` and `clear cc mount` match
// exactly, so a mis-cased name passes the check here and then targets zero
// clients. The UUID is immune to that and is preferred where minimega accepts
// one.
func (Minimega) c2Client(o c2Options) (string, string, error) {
	if o.skipActiveClientCheck {
		return o.vm, "", nil
	}

	vms := getVMIdentities(o.ns, o.vm)
	if len(vms) == 0 {
		return "", "", fmt.Errorf("vm %s does not exist", o.vm)
	}

	// Try to find exact name match
	vm := pickVM(vms, o.vm)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = ccClientCmd

	if o.idByUUID {
		// We use the UUID of the VM instead of the name since `cc clients` returns
		// the actual hostname of the VM as reported by the miniccc agent, which may
		// not always match the name minimega uses to track the VM.
		cmd.Columns = []string{"uuid"}
		cmd.Filters = []string{"uuid=" + vm.UUID}
	} else {
		// Even though `cc clients` returns the actual hostname of the VM as reported
		// by the miniccc agent, we still go ahead and check for the VM name as
		// defined in the topology since that is what the hostname should be in the VM
		// (per the startup app). This way, we don't consider Windows VMs ready until
		// they've rebooted to get their hostname set correctly.
		cmd.Columns = []string{"hostname"}
		cmd.Filters = []string{"hostname=" + vm.Name}
	}

	after := time.After(o.timeout)

	for {
		select {
		case <-o.ctx.Done():
			return "", "", o.ctx.Err()
		case <-after:
			return "", "", ErrC2ClientNotActive
		default:
			rows := mmcli.RunTabular(cmd)

			if len(rows) != 0 {
				return vm.Name, vm.UUID, nil
			}
		}

		// Wait to check again, but give up as soon as the timeout passes or the
		// context is done.
		select {
		case <-o.ctx.Done():
			return "", "", o.ctx.Err()
		case <-after:
			return "", "", ErrC2ClientNotActive
		case <-time.After(c2ActiveCheckInterval):
		}
	}
}

func (m Minimega) ExecC2Command(opts ...C2Option) (string, error) { //nolint:funlen // complex logic
	o := NewC2Options(opts...)

	// Address the VM below by the name minimega launched it under, not by
	// whatever the caller spelled: `cc filter name=`, `cc mount` and
	// `clear cc mount` all match it exactly.
	vmName, vmUUID, err := m.c2Client(o)
	if err != nil {
		return "", fmt.Errorf("cannot execute command: %w", err)
	}

	// post runs a cc command, returning what minimega answers with (for
	// commands sent to clients, the command's ID) and minimega's error as is.
	post := func(cmd string) (string, error) {
		c := mmcli.NewNamespacedCommand(o.ns)
		c.Command = cmd
		c.Timeout = o.timeout

		data, err := mmcli.SingleDataResponse(mmcli.Run(c))
		if err != nil {
			return "", err //nolint:wrapcheck // wrapped by postErr
		}

		return fmt.Sprintf("%v", data), nil
	}

	postErr := func(cmd string, err error) error {
		if errors.Is(err, mmcli.ErrTimeout) {
			return fmt.Errorf("timeout running '%s' in vm %s", cmd, vmName)
		}

		return fmt.Errorf("running '%s' in vm %s: %w", cmd, vmName, err)
	}

	// exec posts a cc command for this VM alone. `cc filter` is one setting
	// for the whole namespace, applied to every cc command posted while it is
	// set, so the namespace's cc lock is held from setting it until it has been
	// cleared again. Left set, it would also keep later unfiltered cc commands
	// (such as `phenix mm cc ...`) from reaching any other VM.
	exec := func(cmd string) (string, error) {
		defer ccLocks.lock(o.ns)()

		c := mmcli.NewNamespacedCommand(o.ns)

		// Filter by UUID where we have one. `cc filter name=` falls through to
		// an implicit tag filter matched against the VM name with a plain
		// string compare
		if vmUUID != "" {
			c.Command = "cc filter uuid=" + vmUUID
		} else {
			c.Command = "cc filter name=" + vmName
		}

		if err := mmcli.ErrorResponse(mmcli.Run(c)); err != nil {
			return "", fmt.Errorf("setting host filter to %s: %w", vmName, err)
		}

		id, err := post(cmd)
		if errors.Is(err, mmcli.ErrTimeout) || errors.Is(err, mmcli.ErrNoResponse) {
			// minimega may yet post the command. With the filter cleared by
			// then, it would reach every VM.
			plog.Warn(
				plog.TypeSystem,
				"leaving cc filter set after an unanswered cc command",
				"ns", o.ns, "vm", vmName, "cmd", cmd, "err", err,
			)

			return "", postErr(cmd, err)
		}

		c.Command = clearCCFilterCmd

		if clearErr := mmcli.ErrorResponse(mmcli.Run(c)); clearErr != nil {
			// Whatever the command's outcome, it was posted with this VM's
			// filter. phenix sets its own filter before each cc command, so
			// only unfiltered commands from elsewhere are affected.
			plog.Error(
				plog.TypeSystem,
				"clearing cc filter",
				"ns", o.ns, "vm", vmName, "cmd", cmd, "err", clearErr,
			)
		}

		if err != nil {
			return "", postErr(cmd, err)
		}

		return id, nil
	}

	// wait waits for a posted command's response, then deletes the command
	// (see deleteC2Command) whether or not a response came.
	wait := func(id string) error {
		defer deleteC2Command(o.ns, id)

		err := waitForResponse(o.ctx, o.ns, id, o.timeout)
		if err != nil {
			return fmt.Errorf("waiting for response: %w", err)
		}

		return nil
	}

	if o.testConn != "" {
		cmd := "cc test-conn " + o.testConn

		id, err := exec(cmd)
		if err != nil {
			return "", fmt.Errorf("calling '%s' for vm %s: %w", cmd, o.vm, err)
		}

		if o.wait {
			if err := wait(id); err != nil {
				return "", err
			}
		}

		return id, nil
	}

	if o.sendFile != "" {
		cmd := "cc send " + o.sendFile

		id, err := exec(cmd)
		if err != nil {
			return "", fmt.Errorf("sending file '%s' to vm %s: %w", o.sendFile, o.vm, err)
		}

		// Special case: if both the `sendFile` and `command` options are set, then
		// send the file first, wait for it to be sent (no matter what), then
		// execute the command.
		if o.command != "" || o.wait {
			if err := wait(id); err != nil {
				return "", err
			}
		}

		if o.command == "" {
			return id, nil
		}
	}

	if o.command != "" {
		cmd := "cc exec " + o.command

		id, err := exec(cmd)
		if err != nil {
			return "", fmt.Errorf("calling '%s' for vm %s: %w", cmd, o.vm, err)
		}

		if o.wait {
			if err := wait(id); err != nil {
				return "", err
			}
		}

		return id, nil
	}

	// `cc mount` and `clear cc mount` name the VM themselves; the cc filter
	// plays no part in them.
	if o.mount != nil {
		if *o.mount {
			var (
				path = GetLocalMountPath(o.ns, o.vm)
				cmd  = fmt.Sprintf("cc mount %s %s", vmName, path)
			)

			if err := os.MkdirAll(path, 0o750); err != nil {
				return "", fmt.Errorf("creating mount directory: %w", err)
			}

			id, err := post(cmd)
			if err != nil {
				return "", fmt.Errorf("error creating mount: %w", postErr(cmd, err))
			}

			return id, nil
		} else {
			cmd := "clear cc mount " + vmName

			id, err := post(cmd)
			if err != nil {
				return "", fmt.Errorf("error clearing mount: %w", postErr(cmd, err))
			}

			return id, nil
		}
	}

	return "", errors.New("no options to execute were provided")
}

func (Minimega) GetC2Response(opts ...C2Option) (string, error) {
	o := NewC2Options(opts...)

	if o.responseType == "" {
		return getResponse(o.ns, o.commandID)
	}

	if o.vm == "" {
		return "", errors.New("must provide VM when getting typed response")
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "cc response " + o.commandID

	resp, err := mmcli.SingleResponse(mmcli.Run(cmd))
	if err != nil {
		return "", fmt.Errorf("getting response for command %s: %w", o.commandID, err)
	}

	vms := getVMIdentities(o.ns, o.vm)
	if len(vms) == 0 {
		return "", fmt.Errorf("vm %s does not exist", o.vm)
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

			if match[responseRegexGroupType] == string(o.responseType) && match[responseRegexGroupUUID] == uuid {
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

	err := waitForResponse(o.ctx, o.ns, o.commandID, o.timeout)

	// Done waiting either way; the response itself is kept.
	deleteC2Command(o.ns, o.commandID)

	if err != nil {
		return "", err
	}

	return getResponse(o.ns, o.commandID)
}

func (Minimega) ClearC2Responses(opts ...C2Option) error {
	o := NewC2Options(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "clear cc responses"

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("clearing C2 responses for namespace %s: %w", o.ns, err)
	}

	return nil
}

func (m Minimega) TapVLAN(opts ...TapOption) error { //nolint:funlen // complex logic
	o := NewTapOptions(opts...)

	if o.untap {
		plog.Info(plog.TypeSystem, "deleting tap from host", "tap", o.name, hostColumn, o.host)

		var errs error

		cmd := "tap delete " + o.name

		err := m.MeshSend(o.ns, o.host, cmd)
		if err != nil {
			errs = multierror.Append(
				errs,
				fmt.Errorf("deleting tap %s on node %s: %w", o.name, o.host, err),
			)
		}

		if o.netns != "" {
			plog.Info(
				plog.TypeSystem,
				"deleting network namespace from host",
				"ns",
				o.netns,
				hostColumn,
				o.host,
			)

			cmd := "ip netns delete " + o.netns

			err := m.MeshShell(o.host, cmd)
			if err != nil {
				return fmt.Errorf("deleting netns %s on node %s: %w", o.netns, o.host, err)
			}
		}

		return errs
	}

	plog.Info(
		plog.TypeSystem,
		"creating tap on host",
		"tap",
		o.name,
		"vlan",
		o.vlan,
		"bridge",
		o.bridge,
		hostColumn,
		o.host,
	)

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

	err := m.MeshSend(o.ns, o.host, cmd)
	if err != nil {
		return fmt.Errorf("creating tap %s on node %s: %w", o.name, o.host, err)
	}

	if o.netns != "" {
		plog.Info(
			plog.TypeSystem,
			"creating network namespace for tap on host",
			"tap",
			o.name,
			hostColumn,
			o.host,
		)

		cmd := "ip netns add " + o.name

		err := m.MeshShell(o.host, cmd)
		if err != nil {
			return fmt.Errorf("creating network namespace on host %s: %w", o.host, err)
		}

		plog.Info(
			plog.TypeSystem,
			"moving tap to network namespace on host",
			"tap",
			o.name,
			hostColumn,
			o.host,
		)

		cmd = fmt.Sprintf("ip link set dev %s netns %s", o.name, o.name)

		err = m.MeshShell(o.host, cmd)
		if err != nil {
			return fmt.Errorf("moving tap to network namespace on host %s: %w", o.host, err)
		}

		plog.Info(
			plog.TypeSystem,
			"bringing tap up in network namespace on host",
			"tap",
			o.name,
			hostColumn,
			o.host,
		)

		cmd = fmt.Sprintf("ip netns exec %s ip link set dev %s up", o.name, o.name)

		err = m.MeshShell(o.host, cmd)
		if err != nil {
			return fmt.Errorf("bringing tap up in network namespace on host %s: %w", o.host, err)
		}

		if o.ip != "" {
			plog.Info(
				plog.TypeSystem,
				"setting IP address for tap in network namespace on host",
				"tap",
				o.name,
				hostColumn,
				o.host,
			)

			cmd := fmt.Sprintf("ip netns exec %s ip addr add %s dev %s", o.name, o.ip, o.name)

			err := m.MeshShell(o.host, cmd)
			if err != nil {
				return fmt.Errorf(
					"setting IP address for tap in network namespace on host %s: %w",
					o.host,
					err,
				)
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
		cmd.Command = "shell " + command
	} else {
		cmd.Command = fmt.Sprintf("mesh send %s shell %s", host, command)
	}

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("running shell command (host %s) %s: %w", host, command, err)
	}

	return nil
}

func (Minimega) MeshShellResponse(host, command string) (string, error) {
	cmd := mmcli.NewCommand()

	if host == "" {
		host = Headnode()
	}

	if IsHeadnode(host) {
		cmd.Command = "shell " + command
	} else {
		cmd.Command = fmt.Sprintf("mesh send %s shell %s", host, command)
	}

	var (
		out   string
		found bool
	)

	// Drain the full response channel -- returning early would abandon it and wedge the shared minimega connection.
	for resps := range mmcli.Run(cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				plog.Warn(plog.TypeSystem, "error running shell command", "cmd", cmd.Command, "error", resp.Error)

				continue
			}

			if found {
				continue
			}

			out, found = strings.TrimSpace(resp.Response), true
		}
	}

	if !found {
		return "", errors.New("error running MeshShellResponse()")
	}

	return out, nil
}

func (Minimega) MeshSend(ns, host, command string) error {
	if host == "" {
		host = Headnode()
	}

	cmd := mmcli.NewCommand()

	switch {
	case IsHeadnode(host):
		cmd.Namespace = ns
		cmd.Command = command
	case ns == "":
		cmd.Command = fmt.Sprintf("mesh send %s %s", host, command)
	default:
		// The namespace goes inside the `mesh send`, as minimega sends
		// namespaced commands to other nodes itself: minimega 2.9 runs the
		// command on the receiving node in that node's active namespace.
		cmd.Command = fmt.Sprintf("mesh send %s namespace %q %s", host, ns, command)
	}

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("executing mesh send (%s): %w", cmd.Command, err)
	}

	return nil
}

// GetLocalMountPath returns where the mount path should be on this filesystem
// for the given namespace and VM.
func GetLocalMountPath(ns, vm string) string {
	return filepath.Join(common.MountDir(), ns, vm)
}

func getActiveC2(ns string, started func()) map[string]bool {
	active := make(map[string]bool)

	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = ccClientCmd

	for _, row := range mmcli.RunTabularStarted(cmd, started) {
		active[row["uuid"]] = true
	}

	return active
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

// deleteC2Command deletes a cc command phenix is done waiting on from the
// namespace's command list. The command's responses are kept.
//
// Until deleted, a `cc exec` stays in the list and is sent again to every
// client that connects, so a restarted miniccc would run it again. `cc
// exec-once` is no alternative: minimega marks it sent the first time it sends
// commands out, whether or not the client it is filtered to is connected yet,
// and a client connecting later never gets it.
//
// A failed delete is logged rather than returned: the caller's command has run
// (or been given up on) either way.
func deleteC2Command(ns, id string) {
	if _, err := strconv.Atoi(id); err != nil {
		plog.Warn(plog.TypeSystem, "not deleting C2 command with a malformed ID", "ns", ns, "id", id)

		return
	}

	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "cc delete command " + id

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		plog.Warn(plog.TypeSystem, "deleting C2 command", "ns", ns, "id", id, "err", err)
	}
}

// flushVM discards minimega's record of one killed VM. A bare `vm flush` would
// discard every quit or errored VM in the namespace, including VMs users shut
// down and mean to start again.
func flushVM(ns, vm string) error {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "vm flush " + vm

	err := mmcli.VMTargetErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("flushing VM %s in namespace %s: %w", vm, ns, err)
	}

	return nil
}

func inject(disk string, part int, injects ...string) error {
	files := strings.Join(injects, " ")

	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("disk inject %s:%d files %s", disk, part, files)

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
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

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("deleting file from cluster nodes: %w", err)
		}
	}

	return nil
}

func processNamespaceHosts(namespace string, started func()) Hosts {
	cmd := mmcli.NewNamespacedCommand(namespace)
	cmd.Command = hostColumn

	status := mmcli.RunTabularStarted(cmd, started)
	hosts := make(Hosts, 0, len(status))

	for _, row := range status {
		host := Host{Name: row[hostColumn]} //nolint:exhaustruct // partial initialization
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

	return hosts
}

// diskUsageTTL is how long a host's disk usage is reused. The Hosts page polls
// the hosts, and each measurement is a mesh command that waits in minimega's
// command queue behind experiment work.
const diskUsageTTL = time.Minute

type diskUsageEntry struct {
	value float64
	at    time.Time
}

var (
	diskUsageMu    sync.Mutex                    //nolint:gochecknoglobals // cache shared across requests
	diskUsageCache = map[string]diskUsageEntry{} //nolint:gochecknoglobals // cache shared across requests
)

// getHostDiskUsage returns the percent of the disks holding phenix's and
// minimega's base directories on `host` that is in use, measured at most once
// per diskUsageTTL. Both are measured with a single shell command.
func (m Minimega) getHostDiskUsage(host string) DiskUsage {
	paths := []string{common.PhenixBase, common.MinimegaBase}
	usage := make([]float64, len(paths))
	stale := false

	diskUsageMu.Lock()

	for i, path := range paths {
		entry, ok := diskUsageCache[host+"\x00"+path]
		if ok && time.Since(entry.at) < diskUsageTTL {
			usage[i] = entry.value
		} else {
			stale = true
		}
	}

	diskUsageMu.Unlock()

	if stale {
		measured := m.measureDiskUsage(host, paths...)
		now := time.Now()

		diskUsageMu.Lock()

		for i, path := range paths {
			if value, ok := measured[i]; ok {
				usage[i] = value
				diskUsageCache[host+"\x00"+path] = diskUsageEntry{value: value, at: now}
			}
		}

		diskUsageMu.Unlock()
	}

	return DiskUsage{Phenix: usage[0], Minimega: usage[1]}
}

// measureDiskUsage runs one shell command on `host` to get the disk usage for
// each of `paths`, returning the usage keyed by index into `paths` for each
// path it could be measured for.
func (m Minimega) measureDiskUsage(host string, paths ...string) map[int]float64 {
	resp, err := m.MeshShellResponse(host, diskUsageCommand(paths...))
	if err != nil {
		return nil
	}

	return parseDiskUsage(resp, len(paths))
}

// diskUsageCommand builds a shell command printing `<index>=<use%>` for each
// path, e.g. `0=42% 1=17%`. The index tags each value, so a path df fails for
// (printing nothing) can't be mistaken for another.
func diskUsageCommand(paths ...string) string {
	parts := make([]string, len(paths))

	for i, path := range paths {
		parts[i] = fmt.Sprintf(`%d=$(df %s | awk '{print $(NF-1)}' | tail -1)`, i, path)
	}

	return fmt.Sprintf(`bash -c "echo %s"`, strings.Join(parts, " "))
}

// parseDiskUsage parses diskUsageCommand's output for `count` paths.
func parseDiskUsage(resp string, count int) map[int]float64 {
	usage := make(map[int]float64)

	for field := range strings.FieldsSeq(resp) {
		idxStr, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}

		idx, err := strconv.Atoi(idxStr)
		if err != nil || idx < 0 || idx >= count {
			continue
		}

		diskUsage, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
		if err != nil {
			continue
		}

		usage[idx] = diskUsage
	}

	return usage
}
