package mm

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"
	"github.com/sandia-minimega/minimega/v2/pkg/ranges"
	"golang.org/x/sync/errgroup"

	"phenix/util/common"
	"phenix/util/freshness"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

var (
	ErrCaptureExists      = errors.New("capture already exists")
	ErrNoCaptures         = errors.New("no captures exist")
	ErrC2ClientNotActive  = errors.New("C2 client not active for VM")
	ErrC2CommandMismatch  = errors.New("C2 command ID names another VM's command on the VM's host")
	ErrVMNotFound         = errors.New("VM not found")
	ErrScreenshotNotFound = errors.New("screenshot not found")

	// ErrOldMinimega is the error of a minimega whose `vm info` does not have the vga column. phenix needs a minimega
	// release that has it.
	ErrOldMinimega = errors.New("minimega is too old: vm info has no vga column, update minimega")

	// ErrMeshUnavailable is the error of a host read for VM placement when the cluster has mesh peers but the
	// `mesh send all host` command failed or no peer answered it, so the compute nodes are unknown.
	ErrMeshUnavailable = errors.New("the mesh peers did not answer")
)

// VMNotFoundError is an error about a VM that minimega or the experiment does not have. It keeps the message of Err
// and matches ErrVMNotFound.
type VMNotFoundError struct {
	Err error
}

func (e *VMNotFoundError) Error() string { return e.Err.Error() }

func (e *VMNotFoundError) Unwrap() []error { return []error{e.Err, ErrVMNotFound} }

// vmNotFound returns err as a *VMNotFoundError when each error that minimega answered says that it does not have the
// VM, else err as it is.
func vmNotFound(err error) error {
	var answered *multierror.Error
	if !errors.As(err, &answered) || len(answered.Errors) == 0 {
		return err
	}

	for _, e := range answered.Errors {
		if !strings.HasPrefix(e.Error(), "vm not found: ") {
			return err
		}
	}

	return &VMNotFoundError{Err: err}
}

const (
	vmInfoCmd        = "vm info"
	vmInfoSummaryCmd = "vm info summary"
	vmLaunchCmd      = "vm launch"
	nsQueueCmd       = "ns queue"
	ccClientCmd      = "cc client"
	ccCommandsCmd    = "cc commands"
	clearCCFilterCmd = "clear cc filter"
	captureCmd       = "capture"
	argsCmd          = "args"
)

const (
	hostColumn             = "host"
	stateColumn            = "state"
	stateRunning           = "RUNNING"
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
	// held during a lookup, so concurrent callers share it
	lookup sync.Mutex
}

// responseRegex matches the line minimega writes before each C2 response file,
// `<command ID>/<client UUID>/<file>:`, and only that whole line.
var responseRegex = regexp.MustCompile(`^(\d+)/(.+)/(stdout|stderr):$`)

// queuedCountRegex matches the number of VMs in each batch `ns queue` lists.
// The batch's `Names:` line is no use for counting: minimega compresses it into
// ranges such as `vm[1-50]`.
var queuedCountRegex = regexp.MustCompile(`(?m)^VMs: (\d+)$`)

type Minimega struct{}

// run sends cmd for o's context (see Context).
func (o options) run(cmd *mmcli.Command) chan *miniclient.Response {
	return mmcli.RunContext(o.ctx, cmd)
}

// tabular is mmcli.RunTabular for o's context (see Context).
func (o options) tabular(cmd *mmcli.Command) []map[string]string {
	return mmcli.RunTabularContext(o.ctx, cmd)
}

// readTabular is tabular for a command that only reads, on the connections of
// mmcli.RunReadOnly in line.
func (o options) readTabular(line mmcli.ReadLine, cmd *mmcli.Command) []map[string]string {
	if o.onShared {
		return o.tabular(cmd)
	}

	return mmcli.RunTabularReadOnly(o.ctx, line, cmd)
}

// nsLine is the mmcli.ReadLine of a read in namespace ns: mmcli.MeshRead, as
// minimega sends it to the peers of the namespace, or mmcli.HeadRead for "".
func nsLine(ns string) mmcli.ReadLine {
	if ns != "" {
		return mmcli.MeshRead
	}

	return mmcli.HeadRead
}

// ended returns, once ctx is done, an error wrapping ctx's error, so an answer
// left empty because its commands gave up (see Context) is not taken for an
// empty answer from minimega.
func ended(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("minimega commands stopped: %w", err)
	}

	// a command's own timer, set from the deadline, can fire before ctx's
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return fmt.Errorf("minimega commands stopped: %w", context.DeadlineExceeded)
	}

	return nil
}

// committed is o for the commands of a call after its first change was sent:
// they run whatever o's context says, so the call is never left half done.
func (o options) committed() options {
	o.ctx = context.WithoutCancel(o.ctx)

	return o
}

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

	for _, row := range mmcli.RunTabularReadOnly(context.Background(), nsLine(ns), cmd) {
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

	for resps := range mmcli.RunReadOnly(context.Background(), nsLine(ns), cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				continue
			}

			for _, m := range queuedCountRegex.FindAllStringSubmatch(resp.Response, -1) {
				count, err := strconv.Atoi(m[1])
				if err != nil {
					// Keep draining the responses; see mmcli.RunReadOnly.
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

		status := mmcli.RunTabularReadOnly(context.Background(), nsLine(ns), cmd)

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

// GetVMInfo returns minimega's details for a namespace's VMs, or one VM's. Concurrent identical
// calls share one run (see flight.Group) and each gets a copy. A failed read leaves the list partial
// and is only logged; GetVMInfoObserved reports it.
func (m Minimega) GetVMInfo(opts ...Option) VMs {
	vms, obs := m.GetVMInfoObserved(opts...)

	switch o := NewOptions(opts...); {
	case obs.Err == nil:
	case o.ctx.Err() != nil:
		plog.Debug(plog.TypeSystem, "getting VM info canceled", "ns", o.ns, "error", obs.Err)
	default:
		plog.Error(plog.TypeSystem, "getting VM info", "ns", o.ns, "error", obs.Err)
	}

	return vms
}

// GetVMInfoObserved is GetVMInfo, also reporting when the VMs were read and
// whether minimega answered for all of them. An observation that is not Known
// carries the error, and the VMs then hold only those minimega did report.
func (m Minimega) GetVMInfoObserved(opts ...Option) (VMs, freshness.Observation) {
	o := NewOptions(opts...)

	res, err := vmInfoFlights.Do(o.ctx, o.ns+"\x00"+o.vm, func(ctx context.Context, seal func()) (vmInfoResult, error) {
		// before the run's first command, so the data is no older than this
		at := time.Now()

		run := o
		run.ctx = ctx

		vms, err := m.getVMInfo(run, seal)

		return vmInfoResult{vms: vms, at: at, err: err}, nil
	})
	if err == nil {
		err = res.err
	}

	if err != nil {
		return cloneVMs(res.vms), freshness.Observation{At: res.at, Stale: false, Known: false, Err: err}
	}

	return cloneVMs(res.vms), freshness.Observed(res.at)
}

// getVMInfo does GetVMInfo's work, calling started just before its first
// minimega command is sent. The VMs are those minimega reported even when it
// also reported an error. Its reads run at once on RunReadOnly's connections:
// `cc client` beside `vm info`, then the capture listing and the `disk info`
// of the snapshot disks.
func (m Minimega) getVMInfo(o options, started func()) (VMs, error) {
	var startOnce sync.Once

	start := func() {
		if started != nil {
			startOnce.Do(started)
		}
	}

	// don't rely on `cc_active` column in `vm info` table
	var activeC2 map[string]bool

	c2Read := make(chan struct{})

	go func() {
		defer close(c2Read)

		activeC2 = getActiveC2(o.ctx, o.ns, start)
	}()

	status, statusErr := mmcli.RunTabularReadOnlyChecked(o.ctx, nsLine(o.ns), vmInfoCommand(o), start)
	received := time.Now()
	vms := make(VMs, 0, len(status))

	var (
		reads    errgroup.Group
		captures map[string][]Capture
		// by index in vms, set by the reads
		backingDisks = make(map[int]*string)
	)

	reads.SetLimit(mmcli.ReadOnlyLimit())

	// One `capture` listing for the namespace, rather than one per VM.
	if len(status) > 0 {
		reads.Go(func() error {
			captures = GroupCapturesByVM(m.GetExperimentCaptures(NS(o.ns), Context(o.ctx)))

			return nil
		})
	}

	for _, row := range status {
		vm, uptime, uptimeErr := vmFromRow(row)

		disk := firstDisk(row["disks"])
		snapshot, _ := strconv.ParseBool(row["snapshot"])

		if snapshot && disk != "" {
			launch := snapshotLaunch(o.ns, vm, disk, received, uptime, uptimeErr)
			backing := new(string)
			backingDisks[len(vms)] = backing

			reads.Go(func() error {
				*backing = snapshotBackingDisk(o.ctx, launch)

				return nil
			})
		} else if disk != "" {
			// Attempting to get disk info when not using a snapshot will cause a
			// locked file error.
			vm.Disk = filepath.Clean(disk)
		}

		vms = append(vms, vm)
	}

	_ = reads.Wait()

	<-c2Read

	for i := range vms {
		vms[i].CCActive = activeC2[vms[i].UUID]
		vms[i].Captures = captures[vms[i].Name]

		if backing, ok := backingDisks[i]; ok {
			vms[i].Disk = *backing
		}
	}

	if statusErr != nil {
		return vms, fmt.Errorf("reading VM info: %w", statusErr)
	}

	// the capture and disk reads give up quietly once o.ctx is done
	if err := ended(o.ctx); len(status) > 0 && err != nil {
		return vms, err
	}

	return vms, nil
}

// vmInfoCommand is the `vm info` that getVMInfo sends for o.
func vmInfoCommand(o options) *mmcli.Command {
	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{
		"uuid", hostColumn, "name", stateColumn, "uptime", "vlan", "tap", "ip", "memory", "vcpus", "disks",
		"snapshot", "cdrom", "tags",
	}

	if o.vm != "" {
		cmd.Filters = []string{"name=" + o.vm}
	}

	return cmd
}

// firstDisk is the path of the first disk in the disks column of `vm info`, "" for none.
func firstDisk(disks string) string {
	// Multiple disks are space-separated (minimega DiskConfigs.String).
	fields := strings.Fields(disks)
	if len(fields) == 0 {
		return ""
	}

	// Each diskspec is comma-separated (path,interface,cache); path is first.
	return strings.Split(fields[0], ",")[0]
}

// snapshotLaunch identifies the launch of vm, whose first disk is the snapshot
// disk, from its uptime as read when its `vm info` was received.
func snapshotLaunch(ns string, vm VM, disk string, received time.Time, uptime time.Duration, uptimeErr error) vmLaunch {
	launch := vmLaunch{ //nolint:exhaustruct // at is only known with an uptime
		ns:   ns,
		name: vm.Name,
		uuid: vm.UUID,
		host: vm.Host,
		disk: disk,
	}

	// The disk's backing file can only change while its VM's process is
	// gone, so a result is only reused for the same launch of a VM whose
	// process is still up.
	if uptimeErr == nil && (vm.State == stateRunning || vm.State == "PAUSED") {
		launch.at = received.Add(-uptime)
	}

	return launch
}

// vmFromRow reads a VM from its row of getVMInfo's `vm info`, leaving out its
// disk, C2 state and captures, and returns its uptime as parsed.
func vmFromRow(row map[string]string) (VM, time.Duration, error) {
	vm := VM{ //nolint:exhaustruct // partial initialization
		UUID:     row["uuid"],
		Host:     row[hostColumn],
		Name:     row["name"],
		State:    row[stateColumn],
		Running:  row[stateColumn] == stateRunning,
		CdRom:    row["cdrom"],
		Networks: parseList(row["vlan"]),
		Taps:     parseList(row["tap"]),
		IPv4:     parseList(row["ip"]),
	}

	var tags map[string]string

	_ = json.Unmarshal([]byte(row["tags"]), &tags)
	vm.Tags = tags

	uptime, err := time.ParseDuration(row["uptime"])
	if err == nil {
		vm.Uptime = uptime.Seconds()
	}

	vm.RAM, _ = strconv.Atoi(row["memory"])
	vm.CPUs, _ = strconv.Atoi(row["vcpus"])

	return vm, uptime, err //nolint:wrapcheck // only tells whether the uptime is known
}

// snapshotBackingDisk returns the image a VM's snapshot disk is based on (or
// the snapshot itself if minimega can't say), asking minimega at most once per
// launch of the VM.
func snapshotBackingDisk(ctx context.Context, launch vmLaunch) string {
	if disk, ok := snapshotDisks.lookup(launch); ok {
		return disk
	}

	cmd := mmcli.NewCommand()
	cmd.Command = "disk info " + launch.disk
	line := mmcli.HeadRead

	if !IsHeadnode(launch.host) {
		cmd.Command = fmt.Sprintf("mesh send %s %s", launch.host, cmd.Command)
		line = mmcli.MeshRead
	}

	resp := mmcli.RunTabularReadOnly(ctx, line, cmd)

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
	for resps := range mmcli.RunReadOnly(o.ctx, mmcli.ScreenshotRead, cmd) {
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

	if err := ended(o.ctx); err != nil {
		return nil, err
	}

	return nil, ErrScreenshotNotFound
}

// The `vm info` columns that GetVNCInfo reads.
const (
	vncPortColumn   = "vnc_port"
	vgaColumn       = "vga"
	copyPasteColumn = "bidirectional-copy-paste"
)

// VNCInfo is how to reach the VNC server of a KVM VM and how its console works, as `vm info` gives them.
type VNCInfo struct {
	// Host is the minimega node that runs the VM.
	Host string
	// Port is the TCP port of the VM's VNC server on Host.
	Port int
	// VGA is the video card that QEMU emulates for the VM, such as std, qxl or virtio.
	VGA string
	// BidirectionalCopyPaste is true when the VM shares its clipboard with VNC clients through qemu-vdagent.
	BidirectionalCopyPaste bool
}

// Endpoint is the VM's VNC server as host:port.
func (i VNCInfo) Endpoint() string {
	return net.JoinHostPort(i.Host, strconv.Itoa(i.Port))
}

// GetVNCInfo reads the VNC endpoint, the video card and the copy and paste mode of the KVM VM in one `vm info`. An
// error wraps ErrVMNotFound when minimega has no such running KVM VM, and ErrOldMinimega when minimega has no vga
// column.
func (Minimega) GetVNCInfo(opts ...Option) (VNCInfo, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{hostColumn, vncPortColumn, vgaColumn, copyPasteColumn}
	cmd.Filters = []string{"type=kvm", "name=" + o.vm}

	rows, err := mmcli.RunTabularReadOnlyChecked(o.ctx, nsLine(o.ns), cmd, nil)

	switch {
	case err != nil && strings.Contains(err.Error(), "no such column"):
		return VNCInfo{}, fmt.Errorf("getting VNC info of VM %s: %w: %w", o.vm, ErrOldMinimega, err)
	case len(rows) == 0 && err != nil:
		return VNCInfo{}, fmt.Errorf("getting VNC info of VM %s in namespace %s: %w", o.vm, o.ns, err)
	case len(rows) == 0:
		if err := ended(o.ctx); err != nil {
			return VNCInfo{}, err
		}

		return VNCInfo{}, fmt.Errorf("getting VNC info of VM %s in namespace %s: %w", o.vm, o.ns, ErrVMNotFound)
	}

	row := rows[len(rows)-1]

	port, err := strconv.Atoi(row[vncPortColumn])
	if err != nil {
		return VNCInfo{}, fmt.Errorf("VM %s has the VNC port %q, want a number: %w", o.vm, row[vncPortColumn], err)
	}

	copyPaste, err := strconv.ParseBool(row[copyPasteColumn])
	if err != nil {
		return VNCInfo{}, fmt.Errorf(
			"VM %s has the %s value %q, want true or false: %w", o.vm, copyPasteColumn, row[copyPasteColumn], err,
		)
	}

	return VNCInfo{Host: row[hostColumn], Port: port, VGA: row[vgaColumn], BidirectionalCopyPaste: copyPaste}, nil
}

func (Minimega) StartVM(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm start " + o.vm

	err := mmcli.ErrorResponse(o.run(cmd))
	if err != nil {
		return fmt.Errorf("starting VM %s in namespace %s: %w", o.vm, o.ns, vmNotFound(err))
	}

	return nil
}

func (Minimega) StopVM(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm stop " + o.vm

	err := mmcli.ErrorResponse(o.run(cmd))
	if err != nil {
		return fmt.Errorf("stopping VM %s in namespace %s: %w", o.vm, o.ns, vmNotFound(err))
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

	info := o.tabular(cmd)
	if err := ended(o.ctx); err != nil {
		return err
	}

	if len(info) == 0 {
		return &VMNotFoundError{Err: fmt.Errorf("no info found for VM %s in namespace %s", o.vm, o.ns)}
	}

	// relaunched as the type it runs as, such as container
	vmType := info[0]["type"]
	if vmType == "" {
		vmType = "kvm"
	}

	if err := ValidateArg(vmType); err != nil {
		return fmt.Errorf("type of VM %s in namespace %s: %w", o.vm, o.ns, err)
	}

	var disks redeployDisks

	if o.disk != "" {
		// Should only be one row of data since we filtered by VM name.
		plan, err := planRedeployDisks(info[0]["disks"], o.disk, len(o.injects) > 0)
		if err != nil {
			return fmt.Errorf("disks of VM %s in namespace %s: %w", o.vm, o.ns, err)
		}

		disks = plan
	}

	cmd.Filters = nil

	// From `vm config clone` to `vm launch`, the namespace's VM config and
	// launch queue are this VM's alone.
	defer vmConfigLocks.lock(o.ns)()

	cmd.Command = "vm config clone " + o.vm

	// the first change: the commands after it run whatever o's context says
	err := mmcli.ErrorResponse(o.run(cmd))
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
		if disks.snapshot != "" {
			// Delete disk snapshot file across cluster
			err := deleteFile(disks.snapshot)
			if err != nil {
				return fmt.Errorf("deleting old disk snapshot: %w", err)
			}

			cmd.Command = fmt.Sprintf("disk snapshot %s %s", disks.source, disks.snapshot)

			err = mmcli.ErrorResponse(mmcli.Run(cmd))
			if err != nil {
				return fmt.Errorf("snapshotting disk for VM %s in namespace %s: %w", o.vm, o.ns, err)
			}

			err = inject(disks.snapshot, o.injectPart, o.injects...)
			if err != nil {
				return err
			}
		}

		cmd.Command = "vm config disk " + joinDiskSpecs(disks.specs)

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("configuring disk for VM %s in namespace %s: %w", o.vm, o.ns, err)
		}
	}

	cmd.Command = fmt.Sprintf("vm launch %s %s", vmType, o.vm)

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

	err := mmcli.ErrorResponse(o.run(cmd))
	if err != nil {
		return fmt.Errorf("killing VM %s in namespace %s: %w", o.vm, o.ns, vmNotFound(err))
	}

	// the VM is gone, so its record goes too, whatever o's context says
	return flushVM(o.ns, o.vm)
}

func (Minimega) GetVMHost(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoCmd
	// The name keeps the answer narrow: the host is not a column (see mmcli.Command).
	cmd.Columns = []string{hostColumn, "name"}
	cmd.Filters = []string{"name=" + o.vm}

	status := o.readTabular(nsLine(o.ns), cmd)

	if err := ended(o.ctx); len(status) == 0 && err != nil {
		return "", err
	}

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

	for _, row := range mmcli.RunTabularReadOnly(o.ctx, nsLine(o.ns), cmd) {
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

	rows := o.readTabular(nsLine(o.ns), cmd)
	if err := ended(o.ctx); len(rows) == 0 && err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("vm %s in namespace %s: %w", o.vm, o.ns, ErrVMNotFound)
	}

	return parseList(rows[0]["ip"]), nil
}

// getVMIdentities returns the name, UUID and host of the VMs in a namespace
// (optionally filtered by VM name), and nothing else, using a single narrow
// `vm info`.
func getVMIdentities(ctx context.Context, ns, name string) VMs {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{hostColumn, "name", "uuid"}

	if name != "" {
		cmd.Filters = []string{"name=" + name}
	}

	rows := mmcli.RunTabularReadOnly(ctx, nsLine(ns), cmd)
	vms := make(VMs, 0, len(rows))

	for _, row := range rows {
		vms = append(vms, VM{Name: row["name"], UUID: row["uuid"], Host: row[hostColumn]}) //nolint:exhaustruct // partial initialization
	}

	return vms
}

// GetVMStates returns each VM's state in the namespace, by name, from one `vm info summary`; unlike
// GetVMInfo it errors when minimega cannot answer. Concurrent calls share one run, each getting a copy.
func (Minimega) GetVMStates(opts ...Option) (map[string]string, error) {
	o := NewOptions(opts...)

	res, err := vmStatesFlights.Do(o.ctx, o.ns, func(ctx context.Context, seal func()) (vmStates, error) {
		run := o
		run.ctx = ctx

		states, err := getVMStates(run, seal)

		return vmStates{states: states, err: err}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("getting VM states in namespace %s: %w", o.ns, err)
	}

	if res.err != nil {
		return nil, res.err
	}

	return maps.Clone(res.states), nil
}

// getVMStates does GetVMStates's work, calling started just before its
// command is sent.
func getVMStates(o options, started func()) (map[string]string, error) {
	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = vmInfoSummaryCmd
	cmd.Columns = []string{"name", stateColumn}

	var (
		states = make(map[string]string)
		errs   []string
	)

	for resps := range mmcli.RunReadOnlyStarted(o.ctx, nsLine(o.ns), cmd, started) {
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

	if err := ended(o.ctx); err != nil {
		return nil, err
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("getting VM states in namespace %s: %s", o.ns, strings.Join(errs, ". "))
	}

	return states, nil
}

func (Minimega) GetVMState(opts ...Option) (string, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vm info summary"
	cmd.Columns = []string{stateColumn}
	cmd.Filters = []string{"name=" + o.vm}

	status := o.readTabular(nsLine(o.ns), cmd)

	if err := ended(o.ctx); len(status) == 0 && err != nil {
		return "", err
	}

	if len(status) == 0 {
		return "", &VMNotFoundError{Err: fmt.Errorf("vm %s not found", o.vm)}
	}

	return status[0][stateColumn], nil
}

func (Minimega) SetVMTags(opts ...Option) error {
	o := NewOptions(opts...)

	if err := ValidateTags(o.tags); err != nil {
		return fmt.Errorf("setting tags for vm %s: %w", o.vm, err)
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("clear vm tag %s ", o.vm)

	err := mmcli.ErrorResponse(o.run(cmd))
	if err != nil {
		return fmt.Errorf("failed to clear tags for vm %s: %w", o.vm, err)
	}

	o = o.committed()

	for k, v := range o.tags {
		cmd.Command = fmt.Sprintf("vm tag %s \"%s\" \"%s\"", o.vm, k, v)

		err := mmcli.ErrorResponse(o.run(cmd))
		if err != nil {
			return fmt.Errorf("failed to set tag for vm %s: %s=%s %w", o.vm, k, v, err)
		}
	}

	return nil
}

func (Minimega) ConnectVMInterface(opts ...Option) error {
	o := NewOptions(opts...)

	command, err := connectVMInterfaceCommand(o)
	if err != nil {
		return err
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = command

	err = mmcli.ErrorResponse(o.run(cmd))
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

func connectVMInterfaceCommand(o options) (string, error) {
	if err := ValidateArg(o.connectVLAN); err != nil {
		return "", fmt.Errorf("connecting interface %d on VM %s: VLAN: %w", o.connectIface, o.vm, err)
	}

	cmd := fmt.Sprintf("vm net connect %s %d %s", o.vm, o.connectIface, o.connectVLAN)

	if o.bridge != "" {
		if err := ValidateArg(o.bridge); err != nil {
			return "", fmt.Errorf("connecting interface %d on VM %s: bridge: %w", o.connectIface, o.vm, err)
		}

		cmd += " " + o.bridge
	}

	return cmd, nil
}

func (Minimega) DisconnectVMInterface(opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("vm net disconnect %s %d", o.vm, o.connectIface)

	err := mmcli.ErrorResponse(o.run(cmd))
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

	if err := ValidateArg(o.bridge); err != nil {
		return fmt.Errorf("creating bridge: %w", err)
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("ns bridge %s gre", o.bridge)

	err := mmcli.ErrorResponse(o.run(cmd))
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
	if err := mmcli.VMTargetErrorResponse(o.run(cmd)); err != nil {
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

	return o.readTabular(nsLine(o.ns), tunnelListing(o))
}

// tunnelListing is the `cc tunnel list` command that GetTunnels sends for o.
func tunnelListing(o options) *mmcli.Command {
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

	return cmd
}

func (Minimega) CloseTunnel(opts ...Option) error {
	o := NewOptions(opts...)

	// on the shared connection, in order with the closes that follow
	tunnels := GetTunnels(append(slices.Clone(opts), readOnShared())...)

	host, err := GetVMHost(opts...)
	if err != nil {
		return fmt.Errorf("unable to determine what host the VM is scheduled on: %w", err)
	}

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
		err := mmcli.VMTargetErrorResponse(o.run(cmd))
		if err == nil {
			// once one is closed, close the rest
			o = o.committed()

			continue
		}

		errs = multierror.Append(
			errs,
			fmt.Errorf("closing tunnel to %s (%s:%d): %w", o.vm, o.dstHost, o.dstPort, err),
		)

		// none is closed yet, and the rest would give up as well
		if errors.Is(err, mmcli.ErrNotSent) && o.ctx.Err() != nil {
			return errs
		}
	}

	return errs
}

// StartVMCapture starts a capture on interface CaptureInterface of the VM into CaptureFile, relative to minimega's
// -filepath. With minimega 3.4.0 or later it passes CaptureFormat and CaptureMaxSize, and minimega makes the file's
// directory; with an older minimega phenix makes the directory and ignores those options.
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

	if err := ValidateArg(o.captureFile); err != nil {
		return fmt.Errorf("capture file: %w", err)
	}

	host, err := GetVMHost(opts...)
	if err != nil {
		return fmt.Errorf("unable to determine what host the VM is scheduled on: %w", err)
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("capture pcap vm %s %d %s", o.vm, o.captureIface, o.captureFile)

	if captureSupport(o.ctx).Available {
		format, err := captureFormatArg(o.captureFormat)
		if err != nil {
			return err
		}

		cmd.Command += format + captureMaxSizeArg(o.captureMaxSize)
	} else if err := makeCaptureDir(o, host); err != nil {
		return err
	}

	if err := mmcli.ErrorResponse(o.run(cmd)); err != nil {
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

// makeCaptureDir makes the directory of the capture file on host, for a minimega older than 3.4.0.
func makeCaptureDir(o options, host string) error {
	var cmdPrefix string

	if !IsHeadnode(host) {
		cmdPrefix = "mesh send " + host
	}

	dir := common.PhenixBase + "/images/" + filepath.Dir(o.captureFile)
	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("%s shell mkdir -p %s", cmdPrefix, dir)

	if err := mmcli.ErrorResponse(o.run(cmd)); err != nil {
		return fmt.Errorf("ensuring experiment files directory exists: %w", err)
	}

	return nil
}

// StopVMCapture stops packet captures for a VM. If the CaptureInterface
// option is provided, only the capture running on that interface index is
// stopped, leaving any other captures for the VM running. Otherwise, all
// captures for the VM are stopped. The VM's captures are listed first unless
// given with KnownCaptures.
func (Minimega) StopVMCapture(opts ...Option) error {
	o := NewOptions(opts...)

	captures := o.captures

	if !o.capturesKnown {
		captures = GetVMCaptures(opts...)
		if err := ended(o.ctx); err != nil {
			return err
		}
	}

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

	err := mmcli.ErrorResponse(o.run(cmd))
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

// GetExperimentCaptures is GetExperimentCapturesChecked, logging a failed listing; the captures of the responses that
// failed are left out.
func (m Minimega) GetExperimentCaptures(opts ...Option) []Capture {
	captures, err := m.GetExperimentCapturesChecked(opts...)

	switch o := NewOptions(opts...); {
	case err == nil:
	case o.ctx.Err() != nil && errors.Is(err, o.ctx.Err()):
		plog.Debug(plog.TypeSystem, "listing VM captures canceled", "ns", o.ns, "error", err)
	default:
		plog.Error(plog.TypeSystem, "listing VM captures", "ns", o.ns, "error", err)
	}

	return captures
}

// GetExperimentCapturesChecked lists the VM interface captures of the namespace. With minimega 3.4.0 or later (see
// CaptureFeatures) it reads every column by name, so it also fills the capture's ID, state, format and counters. A
// failed minimega read is an error; the captures of the responses without an error are then returned too.
func (Minimega) GetExperimentCapturesChecked(opts ...Option) ([]Capture, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = captureCmd

	refreshed := CaptureFeatures()
	if !refreshed {
		cmd.Columns = []string{"interface", "path"}
	}

	rows, err := mmcli.RunTabularReadOnlyChecked(o.ctx, nsLine(o.ns), cmd, nil)
	if err != nil {
		err = fmt.Errorf("listing the VM captures of namespace %s: %w", o.ns, err)
	}

	var captures []Capture

	for _, row := range rows {
		if refreshed {
			if capture, ok := vmCapture(row); ok {
				captures = append(captures, capture)
			}

			continue
		}

		vm, idx, ok := vmInterface(row["interface"])
		if !ok {
			continue
		}

		captures = append(captures, Capture{VM: vm, Interface: idx, Filepath: row["path"]}) //nolint:exhaustruct // older minimega
	}

	return captures, err
}

// vmInterface splits the interface column of the capture listing, <vm>:<index>. It is empty for a capture of a whole
// bridge, which phenix does not start, so such a capture is left out.
func vmInterface(iface string) (string, int, bool) {
	if iface == "" {
		return "", 0, false
	}

	vm, idxStr, ok := strings.Cut(iface, ":")
	if !ok {
		plog.Warn(plog.TypeSystem, "unexpected capture interface format", "interface", iface)

		return "", 0, false
	}

	idx, _ := strconv.Atoi(idxStr)

	return vm, idx, true
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

// GetClusterHosts returns the cluster's hosts, or only those VMs can be scheduled on, with their disk usage only when
// diskUsage is set; see GetClusterHostsObserved.
func (m Minimega) GetClusterHosts(schedOnly, diskUsage bool) (Hosts, error) {
	hosts, _, err := m.GetClusterHostsObserved(context.Background(), schedOnly, diskUsage)

	return hosts, err
}

// GetClusterHostsObserved returns the cluster's hosts, also reporting when they were read from minimega. The headnode
// is always last. diskUsage measures the disk usage (see fillDiskUsage) of the hosts returned, and only of them; without
// it, each DiskUsage is nil.
//
// The full list, shown only to people, is reused for up to clusterHostsTTL, and concurrent calls for it share one set
// of commands (see flight.Group); a list with disk usage and one without are read and kept apart. Its commands give up
// once ctx, and that of every other caller waiting for the same list, is done (see Context), and the call then returns
// an error wrapping ctx's error. When the mesh peers cannot be read (see ErrMeshUnavailable), the full list has the
// headnode and the peers that answered.
//
// A schedOnly call, for VM placement or an app, gives only the hosts VMs can be scheduled on, nil when there are none.
// It always reads minimega and shares no commands with other callers, so ctx's deadline bounds each of its commands.
// When the cluster has mesh peers and none of them answers, or the mesh command fails, it returns an error wrapping
// ErrMeshUnavailable.
func (m Minimega) GetClusterHostsObserved(
	ctx context.Context,
	schedOnly, diskUsage bool,
) (Hosts, freshness.Observation, error) {
	if schedOnly {
		at := time.Now()

		hosts, err := schedulableClusterHosts(ctx, diskUsage)
		if err != nil {
			return nil, freshness.Observation{}, err
		}

		return hosts, freshness.Observed(at), nil
	}

	recent := recentHostsFor(diskUsage)

	if hosts, at, ok := recent.get(); ok {
		return cloneHosts(hosts), freshness.Observed(at), nil
	}

	key := ""
	if diskUsage {
		key = "diskusage"
	}

	res, err := clusterHostsFlights.Do(ctx, key, func(ctx context.Context, seal func()) (clusterHosts, error) {
		at := time.Now()

		hosts, err := getClusterHosts(ctx, seal, diskUsage)
		if err == nil {
			recent.put(hosts, at)
		}

		return clusterHosts{hosts: hosts, at: at, err: err}, nil
	})
	if err != nil {
		return nil, freshness.Observation{}, fmt.Errorf("getting cluster hosts: %w", err)
	}

	if res.err != nil {
		return cloneHosts(res.hosts), freshness.Observation{}, res.err
	}

	return cloneHosts(res.hosts), freshness.Observed(res.at), nil
}

// getClusterHosts does GetClusterHostsObserved's work for the full list, calling started just before its first
// minimega command is sent. The headnode is always last. Once ctx is done, it gives up with an error wrapping ctx's
// error rather than return a list some commands gave up on.
func getClusterHosts(ctx context.Context, started func(), diskUsage bool) (Hosts, error) {
	scan, err := scanClusterHosts(ctx, started)
	if err != nil {
		return scan.hosts, err
	}

	hosts := scan.hosts

	if diskUsage {
		fillDiskUsage(ctx, hosts, func(name string) bool { return name == hosts[len(hosts)-1].Name })
	}

	if err := ended(ctx); err != nil {
		return nil, err
	}

	return hosts, nil
}

// schedulableClusterHosts does GetClusterHostsObserved's work for a schedOnly call. It measures the disk usage of the
// schedulable hosts only, so not that of a headnode VMs are not scheduled on.
func schedulableClusterHosts(ctx context.Context, diskUsage bool) (Hosts, error) {
	scan, err := scanClusterHosts(ctx, nil)
	if err != nil {
		return nil, err
	}

	if scan.peerErr != nil {
		return nil, scan.peerErr
	}

	head := scan.hosts[len(scan.hosts)-1].Name
	hosts := schedulableHosts(scan.hosts)

	if diskUsage && len(hosts) > 0 {
		fillDiskUsage(ctx, hosts, func(name string) bool { return name == head })

		if err := ended(ctx); err != nil {
			return nil, err
		}
	}

	return hosts, nil
}

// clusterScan is the cluster's hosts as scanClusterHosts reads them. peerErr,
// which wraps ErrMeshUnavailable, tells that the compute nodes could not be
// read; hosts then has only the headnode, or the peers that answered.
type clusterScan struct {
	hosts   Hosts
	peerErr error
}

// peerScan is what scanPeers reads of the mesh peers.
type peerScan struct {
	compute Hosts
	none    bool // the cluster has no mesh peers
	peerErr error
	err     error
}

// scanClusterHosts reads the cluster's hosts without their disk usage, the
// headnode last. It reads the headnode and the mesh peers at once, calling
// started just before the first command is sent.
func scanClusterHosts(ctx context.Context, started func()) (clusterScan, error) {
	var startOnce sync.Once

	start := func() {
		if started != nil {
			startOnce.Do(started)
		}
	}

	peersRead := make(chan peerScan, 1)

	go func() { peersRead <- scanPeers(ctx, start) }()

	// Get headnode details. The default namespace only ever spans this node.
	hosts := processNamespaceHosts(ctx, "minimega", start)
	peers := <-peersRead

	if err := ended(ctx); err != nil {
		return clusterScan{}, err
	}

	if len(hosts) == 0 {
		return clusterScan{hosts: []Host{}, peerErr: nil}, errors.New("no cluster hosts found")
	}

	head := hosts[0]
	head.Schedulable = false
	head.Headnode = true

	rememberHeadnode(head.Name)

	if peers.err != nil {
		return clusterScan{}, peers.err
	}

	// A node with no peers runs the VMs itself, as minimega then puts it in
	// every new namespace.
	head.Schedulable = peers.none

	cluster := make(Hosts, 0, len(peers.compute)+1)

	for _, host := range peers.compute {
		host.Name = common.TrimHostnameSuffixes(host.Name)
		host.Schedulable = true

		cluster = append(cluster, host)
	}

	head.Name = common.TrimHostnameSuffixes(head.Name)

	cluster = append(cluster, head)

	return clusterScan{hosts: cluster, peerErr: peers.peerErr}, nil
}

// scanPeers reads the compute nodes, which are the mesh peers, calling started
// just before its first command is sent.
func scanPeers(ctx context.Context, started func()) peerScan {
	peers, err := hostList(ctx, mmcli.NewCommand(), "mesh list peers", started)
	if endErr := ended(ctx); endErr != nil {
		return peerScan{err: endErr} //nolint:exhaustruct // an ended scan
	}

	if err != nil {
		return peerScan{err: fmt.Errorf("getting compute nodes: %w", err)} //nolint:exhaustruct // a failed scan
	}

	if len(peers) == 0 {
		return peerScan{none: true} //nolint:exhaustruct // no peers to read
	}

	cmd := mmcli.NewCommand()
	cmd.Command = "mesh send all " + hostColumn
	rows, err := mmcli.RunTabularReadOnlyChecked(ctx, mmcli.MeshRead, cmd, nil)
	scan := peerScan{compute: parseHosts(rows)} //nolint:exhaustruct // errors set below

	switch {
	case err != nil && ended(ctx) == nil:
		plog.Error(plog.TypeSystem, "error running mm cmd", "cmd", cmd.Command, "error", err)

		scan.peerErr = fmt.Errorf("%w: reading the compute nodes with %q: %w", ErrMeshUnavailable, cmd.Command, err)
	case err == nil && len(scan.compute) == 0:
		scan.peerErr = fmt.Errorf("%w: none of the mesh peers %s answered %q",
			ErrMeshUnavailable, strings.Join(peers, ", "), cmd.Command)
	}

	return scan
}

// hostList runs a command of the head node answering with a minimega host
// list such as `node[1-3],head` and expands it, calling started just before
// it is sent.
func hostList(ctx context.Context, cmd *mmcli.Command, command string, started func()) ([]string, error) {
	cmd.Command = command

	resp, err := mmcli.SingleResponse(mmcli.RunReadOnlyStarted(ctx, mmcli.HeadRead, cmd, started))
	if err != nil {
		return nil, fmt.Errorf("running %s: %w", command, err)
	}

	hosts, err := ranges.SplitList(strings.TrimSpace(resp))
	if err != nil {
		return nil, fmt.Errorf("parsing %s response %q: %w", command, resp, err)
	}

	return hosts, nil
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

// GetNamespaceHosts returns the hosts of the namespace ns, from one `host` in
// it, without their disk usage. Its command gives up once ctx is done, and
// the call then returns an error wrapping ctx's error.
func (Minimega) GetNamespaceHosts(ctx context.Context, ns string) (Hosts, error) {
	hosts := processNamespaceHosts(ctx, ns, nil)

	if err := ended(ctx); err != nil {
		return nil, err
	}

	for i := range hosts {
		hosts[i].Name = common.TrimHostnameSuffixes(hosts[i].Name)
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

	headnode.lookup.Lock()
	defer headnode.lookup.Unlock()

	headnode.mu.Lock()
	name, at = headnode.name, headnode.at
	headnode.mu.Unlock()

	// another caller looked it up while this one waited
	if name != "" && time.Since(at) < headnodeTTL {
		return name
	}

	// Get headnode details
	hosts := processNamespaceHosts(context.Background(), "minimega", nil)

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
	return mmArgs(context.Background())
}

// mmArgs is GetMMArgs, its command giving up once ctx is done.
func mmArgs(ctx context.Context) (map[string]string, error) {
	cmd := mmcli.NewCommand()
	cmd.Command = argsCmd

	rows, err := mmcli.RunTabularReadOnlyChecked(ctx, mmcli.HeadRead, cmd, nil)

	switch {
	case len(rows) == 1:
		return rows[0], nil
	case err != nil:
		return nil, fmt.Errorf("reading minimega's args: %w", err)
	}

	return nil, errors.New("no args returned")
}

func (Minimega) GetVLANs(opts ...Option) (map[string]int, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "vlans"

	vlans := make(map[string]int)

	status, err := mmcli.RunTabularReadOnlyChecked(o.ctx, nsLine(o.ns), cmd, nil)
	if err != nil {
		return nil, fmt.Errorf("listing VLAN aliases: %w", err)
	}

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
	_, err := m.c2Client(NewC2Options(opts...))

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

// c2Target is a VM as C2 commands address it: the name minimega launched it
// under, its UUID, and the cluster host whose cc server has its client. An
// empty UUID or host is not known.
type c2Target struct {
	name, uuid, host string
}

// c2Client waits for a VM's miniccc client to register and returns the VM's
// target. Callers must address the VM by its name or UUID: this lookup folds
// case (minicli's `.filter` lowercases both sides) while `cc filter name=`,
// `cc mount` and `clear cc mount` match exactly, so a mis-cased name passes
// the check here and then targets zero clients. The UUID is immune to that and
// is preferred where minimega accepts one.
func (Minimega) c2Client(o c2Options) (c2Target, error) {
	if o.skipActiveClientCheck {
		return c2Target{name: o.vm, uuid: "", host: ""}, nil
	}

	vms := getVMIdentities(o.ctx, o.ns, o.vm)
	if err := ended(o.ctx); len(vms) == 0 && err != nil {
		return c2Target{}, err
	}

	if len(vms) == 0 {
		return c2Target{}, fmt.Errorf("vm %s does not exist", o.vm)
	}

	// Try to find exact name match
	vm := pickVM(vms, o.vm)

	// Look the client up by UUID: `cc clients` gives the hostname the miniccc
	// agent reports, which can differ from the name minimega tracks the VM by.
	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = ccClientCmd
	cmd.Columns = []string{hostColumn, "uuid", "hostname"}
	cmd.Filters = []string{"uuid=" + vm.UUID}

	after := time.After(o.timeout)

	for {
		select {
		case <-o.ctx.Done():
			return c2Target{}, o.ctx.Err()
		case <-after:
			return c2Target{}, ErrC2ClientNotActive
		default:
			for _, row := range mmcli.RunTabularReadOnly(o.ctx, nsLine(o.ns), cmd) {
				// Unless told to trust the UUID alone, also wait for the guest to
				// report the topology name as its hostname (the startup app sets
				// it), so Windows VMs are not ready until they have rebooted.
				if o.idByUUID || filterMatches(vm.Name, row["hostname"]) {
					return c2Target{name: vm.Name, uuid: vm.UUID, host: row[hostColumn]}, nil
				}
			}
		}

		// Wait to check again, but give up as soon as the timeout passes or the
		// context is done.
		select {
		case <-o.ctx.Done():
			return c2Target{}, o.ctx.Err()
		case <-after:
			return c2Target{}, ErrC2ClientNotActive
		case <-time.After(c2ActiveCheckInterval):
		}
	}
}

func (m Minimega) ExecC2Command(opts ...C2Option) (string, error) { //nolint:funlen // complex logic
	o := NewC2Options(opts...)

	// Address the VM below by the name minimega launched it under, not by
	// whatever the caller spelled: `cc filter name=`, `cc mount` and
	// `clear cc mount` all match it exactly.
	target, err := m.c2Client(o)
	if err != nil {
		return "", fmt.Errorf("cannot execute command: %w", err)
	}

	vmName, vmUUID := target.name, target.uuid

	// post runs a cc command for ctx, returning what host ("" for any host)
	// answers with (for commands sent to clients, the command's ID) and
	// minimega's error as is.
	post := func(ctx context.Context, cmd, host string) (string, error) {
		c := mmcli.NewNamespacedCommand(o.ns)
		c.Command = cmd
		c.Timeout = o.timeout

		data, err := mmcli.HostDataResponse(mmcli.RunContext(ctx, c), host)
		if err != nil {
			return "", err //nolint:wrapcheck // wrapped by postErr
		}

		return c2CommandID(data), nil
	}

	postErr := func(cmd string, err error) error {
		if errors.Is(err, mmcli.ErrTimeout) {
			return fmt.Errorf("timeout running '%s' in vm %s", cmd, vmName)
		}

		return fmt.Errorf("running '%s' in vm %s: %w", cmd, vmName, err)
	}

	// exec posts a cc command for this VM alone and returns its ID on the VM's
	// host: each host numbers its cc commands itself. `cc filter` is one setting
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

		if err := mmcli.ErrorResponse(mmcli.RunContext(o.ctx, c)); err != nil {
			return "", fmt.Errorf("setting host filter to %s: %w", vmName, err)
		}

		// With the filter set, the command is posted and the filter cleared
		// whatever o's context says.
		committed := context.WithoutCancel(o.ctx)

		id, err := post(committed, cmd, target.host)
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

		if clearErr := mmcli.ErrorResponse(mmcli.RunContext(committed, c)); clearErr != nil {
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

		return postedID(id, cmd, vmName)
	}

	// wait waits for a posted command's response, then deletes the command
	// (see deleteC2Command) whether or not a response came.
	wait := func(id string) error {
		defer deleteC2Command(o.ns, target.host, id)

		err := waitForResponse(o.ctx, ccCommand{
			ns: o.ns, id: id, host: target.host, uuid: vmUUID, appearGrace: o.appearGrace, clientGrace: o.clientGrace,
		}, o.timeout)
		if err != nil {
			return fmt.Errorf("waiting for response: %w", err)
		}

		return nil
	}

	if o.testConn != "" {
		cmd := ".preprocess false cc test-conn " + o.testConn

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

	if o.recvFile != "" {
		cmd := "cc recv " + o.recvFile

		id, err := exec(cmd)
		if err != nil {
			return "", fmt.Errorf("receiving file '%s' from vm %s: %w", o.recvFile, o.vm, err)
		}

		if o.wait {
			if err := wait(id); err != nil {
				return "", err
			}
		}

		return id, nil
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

	if o.mount != nil {
		return c2Mount(o, vmName, post, postErr)
	}

	return "", errors.New("no options to execute were provided")
}

// c2Mount posts `cc mount` for the VM, or `clear cc mount` when o unmounts,
// through post, and words the error with postErr. These commands name the VM
// themselves; the cc filter plays no part in them. The headnode answers them,
// for VMs on other hosts too, so any host's answer is accepted.
func c2Mount(
	o c2Options, vmName string,
	post func(context.Context, string, string) (string, error), postErr func(string, error) error,
) (string, error) {
	if !*o.mount {
		cmd := "clear cc mount " + vmName

		id, err := post(o.ctx, cmd, "")
		if err != nil {
			return "", fmt.Errorf("error clearing mount: %w", postErr(cmd, err))
		}

		return id, nil
	}

	var (
		path = GetLocalMountPath(o.ns, o.vm)
		cmd  = fmt.Sprintf("cc mount %s %s", vmName, path)
	)

	if err := os.MkdirAll(path, 0o750); err != nil {
		return "", fmt.Errorf("creating mount directory: %w", err)
	}

	id, err := post(o.ctx, cmd, "")
	if err != nil {
		return "", fmt.Errorf("error creating mount: %w", postErr(cmd, err))
	}

	return id, nil
}

// postedID returns the ID of a cc command posted to a client, which minimega
// always gives.
func postedID(id, cmd, vm string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("running '%s' in vm %s: no command ID in the answer", cmd, vm)
	}

	return id, nil
}

// c2CommandID is the ID minimega answers a cc command with, "" for none:
// `cc mount` and `clear cc mount` answer without data.
func c2CommandID(data any) string {
	if data == nil {
		return ""
	}

	return fmt.Sprintf("%v", data)
}

// GetC2Response returns the response to a cc command. Each host numbers its
// cc commands itself, so with a VM given it reads the response on the VM's
// host; without one it takes any host's response.
func (Minimega) GetC2Response(opts ...C2Option) (string, error) {
	o := NewC2Options(opts...)

	if o.responseType == "" {
		target, err := c2CommandTarget(o)
		if err != nil {
			return "", err
		}

		return getResponse(o.ctx, o.ns, target.host, o.commandID)
	}

	if o.vm == "" {
		return "", errors.New("must provide VM when getting typed response")
	}

	target, err := c2CommandTarget(o)
	if err != nil {
		return "", err
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "cc response " + o.commandID

	resp, err := mmcli.HostResponse(mmcli.RunReadOnly(o.ctx, nsLine(o.ns), cmd), target.host)
	if err != nil {
		return "", fmt.Errorf("getting response for command %s: %w", o.commandID, err)
	}

	return typedC2Response(resp, target.uuid, o.responseType), nil
}

// c2CommandTarget returns the VM o names, for reading a command's response, or
// an empty target if o names no VM.
func c2CommandTarget(o c2Options) (c2Target, error) {
	if o.vm == "" {
		return c2Target{}, nil
	}

	vms := getVMIdentities(o.ctx, o.ns, o.vm)
	if err := ended(o.ctx); len(vms) == 0 && err != nil {
		return c2Target{}, err
	}

	if len(vms) == 0 {
		return c2Target{}, fmt.Errorf("vm %s does not exist", o.vm)
	}

	vm := pickVM(vms, o.vm)

	return c2Target{name: vm.Name, uuid: vm.UUID, host: vm.Host}, nil
}

// typedC2Response returns, from a `cc response` listing, the first non-empty
// file of type typ that the client with uuid wrote, or "" if there is none.
func typedC2Response(resp, uuid string, typ C2ResponseType) string {
	scanner := bufio.NewScanner(strings.NewReader(resp))
	// A line can be as long as the whole response; the default limit is 64 KiB.
	scanner.Buffer(nil, len(resp)+1)

	var output []string

	for scanner.Scan() {
		line := scanner.Text()

		if match := responseRegex.FindStringSubmatch(line); match != nil {
			if len(output) > 0 {
				return strings.Join(output, "\n")
			}

			output = nil

			if match[responseRegexGroupType] == string(typ) && match[responseRegexGroupUUID] == uuid {
				output = []string{}
			}

			continue
		}

		if output != nil {
			output = append(output, line)
		}
	}

	return strings.Join(output, "\n")
}

// WaitForC2Response waits for a cc command's response and returns it. With a
// VM given, it waits for and reads the response on the VM's host, as
// GetC2Response does.
func (Minimega) WaitForC2Response(opts ...C2Option) (string, error) {
	o := NewC2Options(opts...)

	target, err := c2CommandTarget(o)
	if err == nil {
		err = waitForResponse(o.ctx, ccCommand{
			ns: o.ns, id: o.commandID, host: target.host, uuid: target.uuid,
			appearGrace: o.appearGrace, clientGrace: o.clientGrace,
		}, o.timeout)
	}

	// Done waiting either way; the response itself is kept.
	deleteC2Command(o.ns, target.host, o.commandID)

	if err != nil {
		return "", err
	}

	return getResponse(o.ctx, o.ns, target.host, o.commandID)
}

func (Minimega) ClearC2Responses(opts ...C2Option) error {
	o := NewC2Options(opts...)

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "clear cc responses"

	err := mmcli.ErrorResponse(mmcli.RunContext(o.ctx, cmd))
	if err != nil {
		return fmt.Errorf("clearing C2 responses for namespace %s: %w", o.ns, err)
	}

	return nil
}

func (m Minimega) TapVLAN(opts ...TapOption) error { //nolint:funlen // complex logic
	o := NewTapOptions(opts...)

	if err := validateTap(o); err != nil {
		return err
	}

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
			"configuring network namespace for tap on host",
			"tap",
			o.name,
			hostColumn,
			o.host,
		)

		steps := []ShellStep{
			{Action: "creating network namespace on host " + o.host, Command: "ip netns add " + o.name},
			{
				Action:  "moving tap to network namespace on host " + o.host,
				Command: fmt.Sprintf("ip link set dev %s netns %s", o.name, o.name),
			},
			{
				Action:  "bringing tap up in network namespace on host " + o.host,
				Command: fmt.Sprintf("ip netns exec %s ip link set dev %s up", o.name, o.name),
			},
		}

		if o.ip != "" {
			steps = append(steps, ShellStep{
				Action:  "setting IP address for tap in network namespace on host " + o.host,
				Command: fmt.Sprintf("ip netns exec %s ip addr add %s dev %s", o.name, o.ip, o.name),
			})
		}

		if err := shellSteps(m.MeshShell, o.host, steps); err != nil {
			return err
		}
	}

	return nil
}

// validateTap returns an error wrapping ErrUnsafeArg unless minimega takes
// each value TapVLAN writes into its commands as one argument.
func validateTap(o tapOptions) error {
	type arg struct {
		what, value string
		required    bool // an optional value is left out when empty
	}

	args := []arg{
		{what: "name", value: o.name, required: true},
		{what: "host", value: o.host, required: false}, // "" is the headnode
		{what: "network namespace", value: o.netns, required: false},
		{what: "IP", value: o.ip, required: false},
	}

	if !o.untap {
		args = append(args, arg{what: "VLAN", value: o.vlan, required: true}, arg{what: "bridge", value: o.bridge, required: true})
	}

	for _, a := range args {
		if a.value == "" && !a.required {
			continue
		}

		if err := ValidateArg(a.value); err != nil {
			return fmt.Errorf("tap %s: %w", a.what, err)
		}
	}

	// a create in a network namespace writes the name and IP into one sh script
	if !o.untap && o.netns != "" {
		if err := ValidateShellWord(o.name); err != nil {
			return fmt.Errorf("tap name: %w", err)
		}

		if o.ip != "" {
			if err := ValidateShellWord(o.ip); err != nil {
				return fmt.Errorf("tap IP: %w", err)
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

// MeshShellResponse runs the shell command on host and returns its output.
// It sends the command on the shared connection.
func (Minimega) MeshShellResponse(ctx context.Context, host, command string) (string, error) {
	return meshShellResponse(ctx, runShared, host, command)
}

// MeshShellRead is MeshShellResponse for a shell command that changes nothing,
// such as a qemu-img info. It sends the command with mmcli.RunReadOnly:
// mmcli.HeadRead on the head node, else mmcli.MeshRead.
func (Minimega) MeshShellRead(ctx context.Context, host, command string) (string, error) {
	return meshShellResponse(ctx, mmcli.RunReadOnly, host, command)
}

// runShared is mmcli.RunContext with the signature of mmcli.RunReadOnly; it
// ignores line.
func runShared(ctx context.Context, _ mmcli.ReadLine, cmd *mmcli.Command) chan *miniclient.Response {
	return mmcli.RunContext(ctx, cmd)
}

// meshShellResponse is MeshShellResponse, its command sent with run in the
// line of host and giving up once ctx is done (see Context).
func meshShellResponse(
	ctx context.Context,
	run func(context.Context, mmcli.ReadLine, *mmcli.Command) chan *miniclient.Response,
	host, command string,
) (string, error) {
	cmd := mmcli.NewCommand()
	line := mmcli.HeadRead

	// "" is the headnode, which runs the minimega phenix talks to, so it needs no
	// lookup, shared with other callers, that ctx would not bound
	if host == "" || IsHeadnode(host) {
		cmd.Command = "shell " + command
	} else {
		cmd.Command = fmt.Sprintf("mesh send %s shell %s", host, command)
		line = mmcli.MeshRead
	}

	var (
		out   string
		found bool
	)

	// Drain the full response channel -- returning early would abandon it and wedge its minimega connection.
	for resps := range run(ctx, line, cmd) {
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

	if err := ended(ctx); !found && err != nil {
		return "", err
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

func getActiveC2(ctx context.Context, ns string, started func()) map[string]bool {
	active := make(map[string]bool)

	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = ccClientCmd

	for _, row := range mmcli.RunTabularReadOnlyStarted(ctx, nsLine(ns), cmd, started) {
		active[row["uuid"]] = true
	}

	return active
}

// getResponse returns the raw response to command id on host, "" for any host.
func getResponse(ctx context.Context, ns, host, id string) (string, error) {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = fmt.Sprintf("cc response %s raw", id)

	resp, err := mmcli.HostResponse(mmcli.RunReadOnly(ctx, nsLine(ns), cmd), host)
	if err != nil {
		return "", fmt.Errorf("getting response for command %s: %w", id, err)
	}

	return resp, nil
}

// deleteC2Command deletes a cc command phenix is done waiting on, keeping its responses, so a restarted miniccc does not
// run it again (`cc exec-once` is marked sent before a filtered client connects, so it is no alternative). The ID is
// that of host, which gets the delete alone: another host's command of the same ID is not this one. An empty host or
// the headnode, which `mesh send` cannot address, gets it on every host. A failed delete is logged rather than returned.
func deleteC2Command(ns, host, id string) {
	if _, err := strconv.Atoi(id); err != nil {
		plog.Warn(plog.TypeSystem, "not deleting C2 command with a malformed ID", "ns", ns, "id", id)

		return
	}

	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = "cc delete command " + id

	if host != "" && !IsHeadnode(host) {
		cmd.Command = fmt.Sprintf("mesh send %s namespace %s %s", host, ns, cmd.Command)
	}

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		plog.Warn(plog.TypeSystem, "deleting C2 command", "ns", ns, "host", host, "id", id, "err", err)
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

func processNamespaceHosts(ctx context.Context, namespace string, started func()) Hosts {
	cmd := mmcli.NewNamespacedCommand(namespace)
	cmd.Command = hostColumn

	return processHosts(ctx, nsLine(namespace), cmd, started)
}

// processHosts runs cmd, `host` or a command that forwards it to other nodes,
// in line, and reads one Host from each row of its answer.
func processHosts(ctx context.Context, line mmcli.ReadLine, cmd *mmcli.Command, started func()) Hosts {
	return parseHosts(mmcli.RunTabularReadOnlyStarted(ctx, line, cmd, started))
}

// parseHosts reads one Host from each row of a `host` answer.
func parseHosts(status []map[string]string) Hosts {
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

// diskUsageFailureTTL is how long a host whose disk usage could not be
// measured is not asked again; its usage reads as 0 until then.
const diskUsageFailureTTL = 15 * time.Second

type diskUsageEntry struct {
	value  float64
	at     time.Time
	failed bool
}

// fresh reports whether e can still be used at now.
func (e diskUsageEntry) fresh(now time.Time) bool {
	if e.failed {
		return now.Sub(e.at) < diskUsageFailureTTL
	}

	return now.Sub(e.at) < diskUsageTTL
}

var (
	diskUsageMu    sync.Mutex                    //nolint:gochecknoglobals // cache shared across requests
	diskUsageCache = map[string]diskUsageEntry{} //nolint:gochecknoglobals // cache shared across requests
)

// fillCachedDiskUsage sets the DiskUsage of each of hosts that has a cached
// measurement of paths, and gives the index of each other host, as false.
func fillCachedDiskUsage(hosts Hosts, paths []string) map[int]bool {
	stale := make(map[int]bool) // index in hosts: true for the headnode, set later

	diskUsageMu.Lock()
	defer diskUsageMu.Unlock()

	now := time.Now()

	for i, host := range hosts {
		if usage, ok := cachedDiskUsage(host.Name, paths, now); ok {
			hosts[i].DiskUsage = &usage
		} else {
			stale[i] = false
		}
	}

	return stale
}

// fillDiskUsage sets the DiskUsage of each of hosts: the percent of the disks
// holding phenix's and minimega's base directories that is in use. It reuses
// a measurement for diskUsageTTL, and a failed one for diskUsageFailureTTL.
// It measures all stale mesh peers with one `mesh send all` command, and the
// headnode, which isHead names, with one `shell` command, at once. A failure
// while ctx is done is not kept.
func fillDiskUsage(ctx context.Context, hosts Hosts, isHead func(string) bool) {
	paths := []string{common.PhenixBase, common.MinimegaBase}

	stale := fillCachedDiskUsage(hosts, paths)
	if len(stale) == 0 {
		return
	}

	command, err := diskUsageCommand(paths...)
	if err != nil {
		plog.Warn(plog.TypeSystem, "phenix cannot measure the disk usage of these base directories", "error", err)

		for i := range stale {
			hosts[i].DiskUsage = &DiskUsage{Phenix: 0, Minimega: 0}
		}

		return
	}

	var (
		measured   = make(map[string]map[int]float64)
		measuredMu sync.Mutex
		mesh       map[string]map[int]float64
		reads      errgroup.Group
		meshStale  bool
	)

	reads.SetLimit(mmcli.ReadOnlyLimit())

	for i := range stale {
		if !isHead(hosts[i].Name) {
			meshStale = true

			continue
		}

		stale[i] = true
		name := hosts[i].Name

		reads.Go(func() error {
			resp, err := meshShellResponse(ctx, mmcli.RunReadOnly, "", command)
			if err == nil {
				measuredMu.Lock()
				measured[name] = parseDiskUsage(resp, len(paths))
				measuredMu.Unlock()
			}

			return nil
		})
	}

	if meshStale {
		reads.Go(func() error {
			mesh = measureMeshDiskUsage(ctx, command, len(paths))

			return nil
		})
	}

	_ = reads.Wait()

	maps.Copy(measured, mesh)

	// a command's own timer, set from ctx's deadline, can fire before ctx's
	keepFailures := ended(ctx) == nil

	diskUsageMu.Lock()
	defer diskUsageMu.Unlock()

	now := time.Now()

	for name, values := range measured {
		for idx, path := range paths {
			if value, ok := values[idx]; ok {
				diskUsageCache[name+"\x00"+path] = diskUsageEntry{value: value, at: now, failed: false}
			}
		}
	}

	for i := range stale {
		name := hosts[i].Name
		values := measured[name]
		usage := make([]float64, len(paths))

		for idx, path := range paths {
			if value, ok := values[idx]; ok {
				usage[idx] = value
			} else if keepFailures {
				diskUsageCache[name+"\x00"+path] = diskUsageEntry{value: 0, at: now, failed: true}
			}
		}

		hosts[i].DiskUsage = &DiskUsage{Phenix: usage[0], Minimega: usage[1]}
	}
}

// cachedDiskUsage returns host's disk usage for paths from the cache when
// every path's entry is fresh at now. diskUsageMu must be held.
func cachedDiskUsage(host string, paths []string, now time.Time) (DiskUsage, bool) {
	usage := make([]float64, len(paths))

	for i, path := range paths {
		entry, ok := diskUsageCache[host+"\x00"+path]
		if !ok || !entry.fresh(now) {
			return DiskUsage{}, false
		}

		usage[i] = entry.value
	}

	return DiskUsage{Phenix: usage[0], Minimega: usage[1]}, true
}

// measureMeshDiskUsage runs command, a diskUsageCommand for count paths, on
// every mesh peer with one `mesh send all` command. It returns the usage of
// each peer that answered without an error, by host name without its suffixes.
func measureMeshDiskUsage(ctx context.Context, command string, count int) map[string]map[int]float64 {
	cmd := mmcli.NewCommand()
	cmd.Command = "mesh send all shell " + command

	usage := make(map[string]map[int]float64)

	// Drain the full response channel -- returning early would abandon it and wedge its minimega connection.
	for resps := range mmcli.RunReadOnly(ctx, mmcli.MeshRead, cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				if ctx.Err() == nil {
					plog.Warn(plog.TypeSystem, "measuring disk usage", "host", resp.Host, "error", resp.Error)
				}

				continue
			}

			usage[common.TrimHostnameSuffixes(resp.Host)] = parseDiskUsage(resp.Response, count)
		}
	}

	return usage
}

// diskUsageCommand builds a shell command printing `<index>=<use%>` for each
// path, e.g. `0=42% 1=17%`. The index tags each value, so a path df fails for
// (printing nothing) can't be mistaken for another. It returns an error that
// wraps mmcli.ErrUnsafeArg for a path that fails mmcli.SafeArg, as the path
// goes into a quoted bash script in a minimega command.
func diskUsageCommand(paths ...string) (string, error) {
	parts := make([]string, len(paths))

	for i, path := range paths {
		if err := mmcli.SafeArg(path); err != nil {
			return "", fmt.Errorf("disk usage path: %w", err)
		}

		parts[i] = fmt.Sprintf(`%d=$(df %s | awk '{print $(NF-1)}' | tail -1)`, i, path)
	}

	return fmt.Sprintf(`bash -c "echo %s"`, strings.Join(parts, " ")), nil
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
