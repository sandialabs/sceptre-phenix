package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/activeshadow/libminimega/ranges"
)

const (
	minimegaLimitsName  = "minimega-limits"
	limitsFilesRow      = "minimega-limits open files"
	limitsMemoryRow     = "minimega-limits memory"
	stateDriftName      = "state-drift"
	driftExperimentsRow = "state-drift experiments"
	driftNamespacesRow  = "state-drift namespaces"
	vlansName           = "vlans"
	vlansPinnedRow      = "vlans pinned"
	vlansFreeRow        = "vlans free"
	bridgeTrunkName     = "bridge-trunk"
	scheduleHostsName   = "schedule-hosts"
	ccClientsName       = "cc-clients"

	// minimega writes its pid to this file in its base directory when it starts.
	minimegaPidFile   = "minimega.pid"
	minimegaDefaultNS = "minimega"

	// minimega's own files, and those it keeps open for each VM: QMP, the VNC shim, the cc port and two qemu pipes.
	minimegaBaseFiles  = 64
	minimegaFilesPerVM = 5
	busyFilesPercent   = 80
	lowMemoryPercent   = 10
	busySwapPercent    = 50
	percent            = 100
	kib                = 1024
	maxMeminfoValue    = math.MaxInt64 / kib // in kB, so the value in bytes fits an int64
	mibShiftBytes      = 20

	// minimega's default VLAN range, [vlanStart, vlanEnd).
	vlanStart = 101
	vlanEnd   = 4096

	// A VM can take minutes to boot and start miniccc.
	ccStartGrace    = 5 * time.Minute
	minimegaLogTail = 1 << 20
	minimegaLogTime = "2006/01/02 15:04:05" // the time at the start of each line of minimega's log
	logTimeSlack    = 2 * time.Second       // the log gives whole seconds, and uptime is a bit old
	limitsFields    = 5                     // Max open files <soft> <hard>

	// Phrases in the details of rows, which their hints look for.
	pinnedPhrase       = "pin the same VLAN ID"
	vlanPoolPhrase     = "VLANs are free"
	vlanOverlapPhrase  = "overlaps the VLAN range"
	vlanInUsePhrase    = "is in use in the namespace"
	noTrunkPhrase      = "no trunk or tunnel port"
	noClientPhrase     = "no miniccc client"
	minicccDiffPhrase  = "differs from its own"
	filesTooFewPhrase  = "too few for its VMs"
	filesBusyPhrase    = "of the limit are open"
	unscheduledPhrase  = "not in minimega's mesh"
	unlimitedOpenFiles = -1
)

// digitRun matches a number in a host name.
var digitRun = regexp.MustCompile(`[0-9]+`)

// minicccMismatch matches the warning that minimega logs when a miniccc client of another version connects, with the
// local time that minimega's log adds to each line.
var minicccMismatch = regexp.MustCompile(
	`(?m)^(?:(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) )?.*?mismatched miniccc version on (\S+): (\S+)`)

// tunnelTypes are the Open vSwitch interface types that connect a bridge to other nodes.
//
//nolint:gochecknoglobals // constant list
var tunnelTypes = []string{"gre", "vxlan", "geneve", "stt", "erspan", "ip6gre"}

// doctorMesh is the nodes of minimega's mesh, which a run of doctor shares.
type doctorMesh struct {
	nodes []string // every node, this node too
	why   string   // why doctor does not know them, or ""
}

// minimegaMesh asks minimega once in a run for the nodes of its mesh (mesh list all).
func (in *doctorInput) minimegaMesh(ctx context.Context) *doctorMesh {
	if in.found != nil && in.found.mesh != nil {
		return in.found.mesh
	}

	mesh := &doctorMesh{nodes: nil, why: ""}
	if in.found != nil {
		in.found.mesh = mesh
	}

	if in.found == nil || !in.found.minimegaUp {
		mesh.why = "minimega gave no version"

		return mesh
	}

	const command = "mesh list all"

	resps, err := in.askMinimega(ctx, command)
	if err != nil {
		mesh.why = minimegaAskProblem(command, err, in.timeout)

		return mesh
	}

	for _, r := range resps {
		nodes, err := ranges.SplitList(strings.TrimSpace(r.Response))
		if err != nil {
			mesh.why = fmt.Sprintf("minimega's answer to %s does not parse: %v", command, err)

			return mesh
		}

		for _, node := range nodes {
			if node = strings.TrimSpace(node); node != "" && !slices.Contains(mesh.nodes, node) {
				mesh.nodes = append(mesh.nodes, node)
			}
		}
	}

	if len(mesh.nodes) == 0 {
		mesh.why = "minimega's answer to " + command + " names no node"
	}

	return mesh
}

// localVMs are the VMs that minimega runs on this node, in every namespace.
func (inv *doctorInventory) localVMs() []doctorMMVM {
	var vms []doctorMMVM

	for _, vm := range inv.vms {
		if vm.alive() && vm.host == inv.local {
			vms = append(vms, vm)
		}
	}

	return vms
}

// vmCounts is the number of VMs, in any state, in each namespace.
func (inv *doctorInventory) vmCounts() map[string]int {
	counts := make(map[string]int)
	for _, vm := range inv.vms {
		counts[vm.namespace]++
	}

	return counts
}

// minimegaLimitsChecks gives the open files row and the memory row of minimega on this host.
func minimegaLimitsChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	if in.system.os != linuxOS {
		files, memory := hostSkip("minimega runs only on Linux"), hostSkip("minimega runs only on Linux")
		files.Name, memory.Name = limitsFilesRow, limitsMemoryRow

		return []doctorCheck{files, memory}
	}

	files, memory := minimegaOpenFilesCheck(ctx, in), minimegaMemoryCheck(ctx, in)
	files.Name, memory.Name = limitsFilesRow, limitsMemoryRow

	return []doctorCheck{files, memory}
}

// minimegaProcess finds the minimega process through the pid file in minimega's base directory. It says why it found
// none, such as when minimega runs in another container.
func (in *doctorInput) minimegaProcess() (int, string) {
	base := in.text("base-dir.minimega")
	if in.found != nil && in.found.minimegaArgs["base"] != "" {
		base = in.found.minimegaArgs["base"]
	}

	path := filepath.Join(base, minimegaPidFile)

	pid, err := in.env.readPid(path)
	if err != nil {
		return 0, path + " cannot be read, so the minimega process is not known"
	}

	cmdline, err := in.env.readFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return 0, fmt.Sprintf("this host shows no process %d (from %s), such as when minimega runs in another container",
			pid, path)
	}

	if program, _, _ := strings.Cut(string(cmdline), "\x00"); !strings.Contains(filepath.Base(program), "minimega") {
		return 0, fmt.Sprintf("the process %d (from %s) is not minimega here, such as when minimega runs in another "+
			"container", pid, path)
	}

	return pid, ""
}

// parseOpenFilesLimit returns the soft and hard limits of open files in /proc/<pid>/limits, each -1 for unlimited.
func parseOpenFilesLimit(limits string) (int, int, bool) {
	for line := range strings.SplitSeq(limits, "\n") {
		fields := strings.Fields(line)
		if len(fields) < limitsFields || strings.Join(fields[:3], " ") != "Max open files" {
			continue
		}

		values := [2]int{}

		for i, field := range fields[3:5] {
			if field == "unlimited" {
				values[i] = unlimitedOpenFiles

				continue
			}

			n, err := strconv.Atoi(field)
			if err != nil || n < 0 {
				return 0, 0, false
			}

			values[i] = n
		}

		return values[0], values[1], true
	}

	return 0, 0, false
}

// minimegaOpenFilesCheck compares the soft limit of open files of the minimega process with what its VMs on this node
// need, and with the files it has open.
func minimegaOpenFilesCheck(ctx context.Context, in *doctorInput) doctorCheck {
	pid, why := in.minimegaProcess()
	if why != "" {
		return hostSkip(why)
	}

	path := fmt.Sprintf("/proc/%d/limits", pid)

	data, err := in.env.readFile(path)
	if err != nil {
		return hostSkip(fmt.Sprintf("this user cannot read %s: %v", path, unwrapPathError(err)))
	}

	soft, hard, ok := parseOpenFilesLimit(string(data))

	switch {
	case !ok:
		return hostSkip(path + " gives no limit of open files")
	case soft == unlimitedOpenFiles:
		return passCheck(fmt.Sprintf("minimega (pid %d) has no limit of open files", pid))
	}

	hardText := strconv.Itoa(hard)
	if hard == unlimitedOpenFiles {
		hardText = "unlimited"
	}

	parts := []string{fmt.Sprintf("minimega (pid %d) can open %d files (soft limit, hard limit %s)", pid, soft, hardText)}
	warn := false

	if inv := in.minimegaInventory(ctx); inv.why == "" {
		vms := len(inv.localVMs())
		need := minimegaBaseFiles + minimegaFilesPerVM*vms
		verb := "need"
		if vms == 1 {
			verb = "needs"
		}

		part := fmt.Sprintf("its %s on this node %s about %d (%d, and %d for each VM: QMP, VNC, cc and two qemu "+
			"output pipes)", vmCount(vms), verb, need, minimegaBaseFiles, minimegaFilesPerVM)

		if need > soft {
			warn = true
			part = "that is " + filesTooFewPhrase + ": " + part
		}

		parts = append(parts, part)
	} else {
		parts = append(parts, "the VMs are not counted, because "+inv.why)
	}

	if entries, err := in.env.readDir(fmt.Sprintf("/proc/%d/fd", pid)); err == nil {
		part := openFilesNow(len(entries))

		if len(entries)*percent > soft*busyFilesPercent {
			warn = true
			part = fmt.Sprintf("%s, so more than %d percent %s", part, busyFilesPercent, filesBusyPhrase)
		}

		parts = append(parts, part)
	} else {
		parts = append(parts, fmt.Sprintf("this user cannot count the open files in /proc/%d/fd", pid))
	}

	for i := 1; i < len(parts); i++ {
		parts[i] = capitalize(parts[i])
	}

	if warn {
		return warnCheck(strings.Join(parts, ". "))
	}

	return passCheck(strings.Join(parts, ". "))
}

// openFilesNow says that n files are open now.
func openFilesNow(n int) string {
	if n == 1 {
		return "1 file is open now"
	}

	return fmt.Sprintf("%d files are open now", n)
}

// parseMeminfo returns each value of /proc/meminfo in bytes, by name.
func parseMeminfo(meminfo string) map[string]int64 {
	values := make(map[string]int64)

	for line := range strings.SplitSeq(meminfo, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		fields := strings.Fields(rest)

		if !ok || len(fields) == 0 {
			continue
		}

		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || n < 0 || n > maxMeminfoValue {
			continue
		}

		if len(fields) > 1 && fields[1] == "kB" {
			n *= kib
		}

		values[strings.TrimSpace(name)] = n
	}

	return values
}

// minimegaMemoryCheck warns when this host is short of memory, when swap is mostly in use, or when the VMs that
// minimega runs on this node ask for more memory than the host has with its swap.
func minimegaMemoryCheck(ctx context.Context, in *doctorInput) doctorCheck {
	data, err := in.env.readFile("/proc/meminfo")
	if err != nil {
		return hostSkip("this host has no /proc/meminfo to read")
	}

	mem := parseMeminfo(string(data))

	total := mem["MemTotal"]
	if total == 0 {
		return hostSkip("/proc/meminfo gives no MemTotal")
	}

	avail, hasAvail := mem["MemAvailable"]
	swapTotal, swapFree := mem["SwapTotal"], mem["SwapFree"]
	facts := fmt.Sprintf("MemTotal %s, MemAvailable %s, SwapTotal %s, SwapFree %s",
		doctorBytes(total), doctorBytes(avail), doctorBytes(swapTotal), doctorBytes(swapFree))

	var problems []string

	if hasAvail && avail*percent < total*lowMemoryPercent {
		problems = append(problems, fmt.Sprintf("only %s of %s memory is available, less than %d percent",
			doctorBytes(avail), doctorBytes(total), lowMemoryPercent))
	}

	if used := swapTotal - swapFree; swapTotal > 0 && used*percent > swapTotal*busySwapPercent {
		problems = append(problems, fmt.Sprintf("%s of %s swap is in use, more than %d percent",
			doctorBytes(used), doctorBytes(swapTotal), busySwapPercent))
	}

	var asked string

	if inv := in.minimegaInventory(ctx); inv.why == "" {
		vms := inv.localVMs()

		var bytes int64
		for _, vm := range vms {
			bytes += int64(vm.memory) << mibShiftBytes
		}

		verb := "ask"
		if len(vms) == 1 {
			verb = "asks"
		}

		asked = fmt.Sprintf("the %s on this node %s for %s of memory", vmCount(len(vms)), verb, doctorBytes(bytes))

		if bytes > total+swapTotal {
			problems = append(problems, fmt.Sprintf("%s, more than the %s of memory and swap", asked,
				doctorBytes(total+swapTotal)))
			asked = ""
		}
	} else {
		asked = "the memory of the VMs is not checked, because " + inv.why
	}

	parts := problems
	if asked != "" {
		parts = append(parts, asked)
	}

	parts = append(parts, facts)

	for i := 1; i < len(parts); i++ {
		parts[i] = capitalize(parts[i])
	}

	if len(problems) > 0 {
		return warnCheck(strings.Join(parts, ". "))
	}

	return passCheck(strings.Join(parts, ". "))
}

// minimegaLimitsHint is what to do about a minimega-limits row that warns.
func minimegaLimitsHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case c.Name == limitsMemoryRow:
		return "stop VMs, or give the VMs less memory in their topologies"
	}

	return "increase the open files limit of minimega, with LimitNOFILE= in its systemd unit or " +
		"docker run --ulimit nofile=<n>:<n>, then restart minimega"
}

// stateDriftChecks compares the stored experiments with minimega's namespaces: the experiments that the store marks
// as running with no VMs in minimega, and the namespaces with VMs that no stored experiment owns.
func stateDriftChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	inv := in.minimegaInventory(ctx)
	experiments, why := in.storedExperiments(ctx)

	if inv.why != "" {
		why = inv.why
	}

	if why != "" {
		skip := hostSkip(why)
		skip.Name = driftExperimentsRow
		other := skip
		other.Name = driftNamespacesRow

		return []doctorCheck{skip, other}
	}

	counts := inv.vmCounts()

	rows := []doctorCheck{driftExperimentsCheck(inv, counts, experiments), driftNamespacesCheck(inv, counts, experiments)}
	rows[0].Name, rows[1].Name = driftExperimentsRow, driftNamespacesRow

	return rows
}

func driftExperimentsCheck(inv *doctorInventory, counts map[string]int, experiments []doctorExperiment) doctorCheck {
	var (
		items   []string
		running int
	)

	for _, exp := range experiments {
		if !exp.running {
			continue
		}

		running++

		switch {
		case !slices.Contains(inv.namespaces, exp.name):
			items = append(items, exp.name+" (no namespace)")
		case counts[exp.name] == 0:
			items = append(items, exp.name+" (no VMs in its namespace)")
		}
	}

	switch {
	case len(items) > 0:
		return warnCheck("the store marks experiments as running, but minimega has no VMs for them, such as after " +
			"minimega restarted: " + shortList(items))
	case running == 0:
		return passCheck("the store marks no experiment as running")
	}

	return passCheck(fmt.Sprintf("minimega has VMs for each of the %d experiments that the store marks as running",
		running))
}

func driftNamespacesCheck(inv *doctorInventory, counts map[string]int, experiments []doctorExperiment) doctorCheck {
	var items []string

	for _, ns := range inv.namespaces {
		if _, stored := experimentNamed(experiments, ns); stored || counts[ns] == 0 {
			continue
		}

		items = append(items, fmt.Sprintf("%s (%s)", ns, vmCount(counts[ns])))
	}

	if len(items) > 0 {
		return warnCheck("minimega namespaces with VMs that no stored experiment owns: " + shortList(items))
	}

	return passCheck("each minimega namespace with VMs belongs to a stored experiment")
}

// vmCount is n with the word VM, plural unless n is 1.
func vmCount(n int) string {
	return plural(n, "VM", "VMs")
}

// plural is n with the words one when n is 1, else with the words many.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return fmt.Sprintf("%d %s", n, many)
}

// stateDriftHint is what to do about a state-drift row that warns.
func stateDriftHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case c.Name == driftExperimentsRow:
		return "reset the stored state of each experiment with phenix experiment stop <experiment>"
	}

	return "make sure that doctor reads the store of phenix ui. Then kill the VMs of each namespace with " +
		"minimega -e 'clear namespace <namespace>'"
}

// vlansChecks gives the pinned row, which needs only the store, and the free row, which also needs minimega.
func vlansChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	experiments, why := in.storedExperiments(ctx)

	rows := []doctorCheck{vlansPinnedCheck(experiments, why), vlansFreeCheck(ctx, in, experiments, why)}
	rows[0].Name, rows[1].Name = vlansPinnedRow, vlansFreeRow

	return rows
}

// vlansPinnedCheck warns about the VLAN IDs that more than one stored experiment pins, because minimega refuses an
// alias for a VLAN that is in use.
func vlansPinnedCheck(experiments []doctorExperiment, why string) doctorCheck {
	if why != "" {
		return hostSkip(why)
	}

	type pin struct{ exp, alias string }

	byID := make(map[int][]pin)

	for _, exp := range experiments {
		for _, alias := range slices.Sorted(maps.Keys(exp.vlans.pinned)) {
			id := exp.vlans.pinned[alias]
			byID[id] = append(byID[id], pin{exp: exp.name, alias: alias})
		}
	}

	var items []string

	for _, id := range slices.Sorted(maps.Keys(byID)) {
		pins := byID[id]

		var exps, names []string

		for _, p := range pins {
			if !slices.Contains(exps, p.exp) {
				exps = append(exps, p.exp)
			}

			names = append(names, fmt.Sprintf("%s (alias %s)", p.exp, p.alias))
		}

		if len(exps) > 1 {
			items = append(items, fmt.Sprintf("VLAN %d in %s", id, strings.Join(names, " and ")))
		}
	}

	if len(items) > 0 {
		return warnCheck("stored experiments " + pinnedPhrase + ", so they cannot run at the same time: " +
			shortList(items))
	}

	return passCheck("no two stored experiments pin the same VLAN ID")
}

// doctorVLANState is what minimega gives about its VLANs.
type doctorVLANState struct {
	min, max  int               // the default range, [min, max)
	ranges    map[string][2]int // the range of each other namespace that has one
	used      map[int]string    // the namespace of each VLAN that an alias uses
	blacklist map[int]bool      // the VLANs that minimega does not give out
}

// minimegaVLANs asks minimega, in each namespace it listed, for the VLAN aliases and the VLAN range, and for the
// VLANs it does not give out. It says why it read none.
func (in *doctorInput) minimegaVLANs(ctx context.Context, inv *doctorInventory) (*doctorVLANState, string) {
	state := &doctorVLANState{
		min: vlanStart, max: vlanEnd, ranges: map[string][2]int{}, used: map[int]string{}, blacklist: map[int]bool{},
	}

	ask := func(command string) ([]map[string]string, string) {
		resps, err := in.askMinimega(ctx, command)
		if err != nil {
			return nil, minimegaAskProblem(command, err, in.timeout)
		}

		return minimegaRows(resps), ""
	}

	for _, ns := range inv.namespaces {
		rows, why := ask("namespace " + ns + " vlans")
		if why != "" {
			return nil, why
		}

		for _, row := range rows {
			if id, err := strconv.Atoi(row["vlan"]); err == nil {
				state.used[id] = ns
			}
		}

		if rows, why = ask("namespace " + ns + " vlans range"); why != "" {
			return nil, why
		}

		for _, row := range rows {
			low, errMin := strconv.Atoi(row["min"])
			high, errMax := strconv.Atoi(row["max"])

			switch {
			case errMin != nil || errMax != nil:
			case ns == minimegaDefaultNS:
				state.min, state.max = low, high
			default:
				state.ranges[ns] = [2]int{low, high}
			}
		}
	}

	if slices.Contains(inv.namespaces, minimegaDefaultNS) {
		rows, why := ask("namespace " + minimegaDefaultNS + " vlans blacklist")
		if why != "" {
			return nil, why
		}

		for _, row := range rows {
			if id, err := strconv.Atoi(row["vlan"]); err == nil {
				state.blacklist[id] = true
			}
		}
	}

	return state, ""
}

// free counts the VLANs in [low, high) that minimega can give the namespace ns: not in use by another namespace, not
// on the blacklist and, for the default range, not in the range of another namespace.
func (s *doctorVLANState) free(ns string, low, high int, ownRange bool) int {
	free := 0

	for id := max(low, 0); id < high; id++ {
		if owner, used := s.used[id]; (used && owner != ns) || s.blacklist[id] {
			continue
		}

		reserved := false

		for other, r := range s.ranges {
			if !ownRange && other != ns && id >= r[0] && id < r[1] {
				reserved = true
			}
		}

		if !reserved {
			free++
		}
	}

	return free
}

// vlansFreeCheck warns about each stopped experiment that cannot get its VLANs from minimega now: too few free VLANs
// in its range, a range that overlaps the range of another namespace, or a pinned VLAN that another namespace uses.
func vlansFreeCheck(ctx context.Context, in *doctorInput, experiments []doctorExperiment, why string) doctorCheck {
	if why != "" {
		return hostSkip(why)
	}

	inv := in.minimegaInventory(ctx)
	if inv.why != "" {
		return hostSkip(inv.why)
	}

	state, why := in.minimegaVLANs(ctx, inv)
	if why != "" {
		return hostSkip(why)
	}

	var (
		items   []string
		stopped int
	)

	for _, exp := range experiments {
		if exp.running {
			continue
		}

		stopped++

		items = append(items, vlanProblems(state, exp)...)
	}

	pool := fmt.Sprintf("%d of minimega's VLAN range %d-%d are free", state.free("", state.min, state.max, false),
		state.min, state.max)

	switch {
	case len(items) > 0:
		return warnCheck("stopped experiments cannot get their VLANs now: " + shortList(items) + ". " + capitalize(pool))
	case stopped == 0:
		return passCheck("no stopped experiment in the store. " + capitalize(pool))
	}

	return passCheck(fmt.Sprintf("each of the %d stopped experiments can get its VLANs. %s", stopped, capitalize(pool)))
}

// vlanProblems explains why the stopped experiment exp cannot get its VLANs from minimega in state.
func vlanProblems(state *doctorVLANState, exp doctorExperiment) []string {
	var problems []string

	for _, alias := range slices.Sorted(maps.Keys(exp.vlans.pinned)) {
		id := exp.vlans.pinned[alias]
		if owner, used := state.used[id]; used && owner != exp.name {
			problems = append(problems, fmt.Sprintf("VLAN %d of %s (alias %s) %s %s", id, exp.name, alias,
				vlanInUsePhrase, owner))
		}
	}

	low, high, own := state.min, state.max, exp.vlans.min != 0 && exp.vlans.max != 0
	if own {
		low, high = exp.vlans.min, exp.vlans.max

		for _, other := range slices.Sorted(maps.Keys(state.ranges)) {
			if r := state.ranges[other]; other != exp.name && low < r[1] && r[0] <= high {
				problems = append(problems, fmt.Sprintf("the VLAN range %d-%d of %s %s %d-%d of the namespace %s",
					low, high, exp.name, vlanOverlapPhrase, r[0], r[1], other))
			}
		}
	}

	need := exp.vlans.count - len(exp.vlans.pinned)
	if free := state.free(exp.name, low, high, own); need > free {
		problems = append(problems, fmt.Sprintf("%s needs %d VLANs from the range %d-%d, but only %d %s",
			exp.name, need, low, high, free, vlanPoolPhrase))
	}

	return problems
}

// vlansHint is what to do about each problem of a vlans row.
func vlansHint(c doctorCheck) string {
	return hostHints(c, [][2]string{
		{pinnedPhrase, "give one of the experiments another VLAN ID with phenix vlan alias <experiment> <alias> <id> --force"},
		{vlanInUsePhrase, "stop the experiment that uses the VLAN, or pin another VLAN ID with phenix vlan alias " +
			"<experiment> <alias> <id> --force"},
		{vlanOverlapPhrase, "set a VLAN range that no other experiment uses with phenix vlan range <experiment> <min> " +
			"<max> --force"},
		{vlanPoolPhrase, "stop experiments to free VLANs, or set a larger range with phenix vlan range <experiment> " +
			"<min> <max> --force (vlans.min and vlans.max in the spec)"},
	})
}

// ovsBridge is a bridge in the output of ovs-vsctl show.
type ovsBridge struct {
	name  string
	ports []ovsBridgePort
}

// ovsBridgePort is a port of an ovsBridge, with its VLAN tag and its interfaces.
type ovsBridgePort struct {
	name, tag  string
	interfaces []ovsInterface
}

// ovsInterface is an interface of an ovsBridgePort, with its type: "" for a system interface.
type ovsInterface struct {
	name, kind string
}

// parseOVSBridges returns each bridge in the output of ovs-vsctl show, with its ports and their interfaces.
func parseOVSBridges(out string) []ovsBridge {
	var bridges []ovsBridge

	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		key, value, _ := strings.Cut(line, " ")
		value = strings.Trim(strings.TrimSpace(value), `"`)

		if key == "Bridge" {
			bridges = append(bridges, ovsBridge{name: value, ports: nil})

			continue
		}

		if len(bridges) == 0 {
			continue
		}

		b := &bridges[len(bridges)-1]

		if key == "Port" {
			b.ports = append(b.ports, ovsBridgePort{name: value, tag: "", interfaces: nil})

			continue
		}

		if len(b.ports) == 0 {
			continue
		}

		p := &b.ports[len(b.ports)-1]

		switch key {
		case "Interface":
			p.interfaces = append(p.interfaces, ovsInterface{name: value, kind: ""})
		case "tag:":
			p.tag = value
		case "type:":
			if len(p.interfaces) > 0 {
				p.interfaces[len(p.interfaces)-1].kind = value
			}
		}
	}

	return bridges
}

// trunkPort describes p when it can carry the VLANs of the bridge to other nodes: an untagged port with a tunnel, an
// Open vSwitch bond, or a system interface that is not a minimega tap.
func (e doctorEnv) trunkPort(bridge string, p ovsBridgePort) (string, bool) {
	if p.tag != "" || p.name == bridge || len(p.interfaces) == 0 {
		return "", false
	}

	for _, iface := range p.interfaces {
		if slices.Contains(tunnelTypes, iface.kind) {
			return fmt.Sprintf("%s (%s tunnel)", p.name, iface.kind), true
		}
	}

	if len(p.interfaces) > 1 {
		names := make([]string, 0, len(p.interfaces))
		for _, iface := range p.interfaces {
			names = append(names, iface.name)
		}

		return fmt.Sprintf("%s (Open vSwitch bond of %s)", p.name, strings.Join(names, ", ")), true
	}

	iface := p.interfaces[0]
	if (iface.kind != "" && iface.kind != "system") || strings.Contains(iface.name, minimegaTapName) {
		return "", false
	}

	kind := "interface"

	switch {
	case e.exists(filepath.Join(sysClassNet, iface.name, "bonding")):
		kind = "bond"
	case e.exists(filepath.Join("/proc/net/vlan", iface.name)):
		kind = "VLAN device"
	case e.exists(filepath.Join(sysClassNet, iface.name, "device")):
		kind = "physical NIC"
	}

	return fmt.Sprintf("%s (%s)", p.name, kind), true
}

func (e doctorEnv) exists(path string) bool {
	_, err := e.stat(path)

	return err == nil
}

// bridgeTrunkCheck warns, in a mesh of more than one node, about each experiment bridge on this node with no trunk or
// tunnel port, because its VMs cannot reach VMs on other nodes. minimega bridge trunk adds an untagged port.
func bridgeTrunkCheck(ctx context.Context, in *doctorInput) doctorCheck {
	if in.system.os != linuxOS {
		return hostSkip("minimega runs only on Linux")
	}

	mesh := in.minimegaMesh(ctx)

	switch {
	case mesh.why != "":
		return hostSkip(mesh.why)
	case len(mesh.nodes) <= 1:
		return doctorCheck{Name: "", Status: doctorSkip, Detail: "one node, no trunk needed"}
	case in.text("use-gre-mesh") == boolStringTrue:
		return passCheck(fmt.Sprintf("the mesh has %d nodes, and use-gre-mesh is true, so phenix connects the "+
			"nodes with GRE tunnels at each start", len(mesh.nodes)))
	}

	if _, err := in.env.lookPath("ovs-vsctl"); err != nil {
		return hostSkip("ovs-vsctl is not on PATH")
	}

	show := in.runOVSShow(ctx)
	if show.err != nil {
		return hostSkip("ovs-vsctl show failed (see the openvswitch row)")
	}

	var missing, trunked []string

	for _, b := range parseOVSBridges(string(show.out)) {
		vmTaps := slices.ContainsFunc(b.ports, func(p ovsBridgePort) bool {
			return p.tag != "" && strings.Contains(p.name, minimegaTapName)
		})

		if b.name != defaultExperimentBridge && !vmTaps {
			continue
		}

		var trunks []string

		for _, p := range b.ports {
			if trunk, ok := in.env.trunkPort(b.name, p); ok {
				trunks = append(trunks, trunk)
			}
		}

		if len(trunks) == 0 {
			missing = append(missing, b.name)
		} else {
			trunked = append(trunked, fmt.Sprintf("the bridge %s connects to %s", b.name, strings.Join(trunks, ", ")))
		}
	}

	meshText := fmt.Sprintf("the mesh has %d nodes", len(mesh.nodes))

	switch {
	case len(missing) > 0:
		return warnCheck(fmt.Sprintf("%s, but these bridges on this node have %s, so their VMs cannot reach VMs on "+
			"other nodes: %s", meshText, noTrunkPhrase, strings.Join(missing, ", ")))
	case len(trunked) == 0:
		return hostSkip(fmt.Sprintf("this node has no bridge %s and no bridge with VM taps yet", defaultExperimentBridge))
	}

	return passCheck(capitalize(meshText) + ". " + capitalize(strings.Join(trunked, ". ")))
}

// bridgeTrunkHint is what to do about a bridge-trunk row that warns.
func bridgeTrunkHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "add a trunk port to the bridge on each node with minimega -e 'bridge trunk <bridge> <interface>', " +
		"or set use-gre-mesh to true"
}

// scheduleHostsCheck warns about each cluster node that the VM schedules of a stored experiment name, but that is not
// in minimega's mesh.
func scheduleHostsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	mesh := in.minimegaMesh(ctx)
	if mesh.why != "" {
		return hostSkip(mesh.why)
	}

	suffixes := in.text("hostname-suffixes")
	known := map[string]bool{"localhost": true}

	for _, node := range append(slices.Clone(mesh.nodes), in.minimegaInventory(ctx).host) {
		if node != "" {
			known[meshNodeKey(trimSuffixes(node, suffixes))] = true
		}
	}

	var (
		items     []string
		scheduled int
	)

	for _, exp := range experiments {
		for _, vm := range slices.Sorted(maps.Keys(exp.schedules)) {
			node := exp.schedules[vm]
			if node == "" {
				continue
			}

			scheduled++

			if !known[meshNodeKey(trimSuffixes(node, suffixes))] {
				items = append(items, fmt.Sprintf("%s VM %s on %s", exp.name, vm, node))
			}
		}
	}

	switch {
	case len(items) > 0:
		return warnCheck(fmt.Sprintf("VM schedules name cluster nodes that are %s (%d nodes): %s",
			unscheduledPhrase, len(mesh.nodes), shortList(items)))
	case scheduled == 0:
		return passCheck("no stored experiment schedules a VM on a cluster node")
	}

	return passCheck(fmt.Sprintf("each of the %d VM schedules names a node in minimega's mesh (%d nodes)",
		scheduled, len(mesh.nodes)))
}

// meshNodeKey is name as minimega compares host names: without the leading zeros of its numbers, which minimega
// drops when it compresses a list of hosts, such as node01 to node1.
func meshNodeKey(name string) string {
	return digitRun.ReplaceAllStringFunc(name, func(digits string) string {
		if trimmed := strings.TrimLeft(digits, "0"); trimmed != "" {
			return trimmed
		}

		return "0"
	})
}

// scheduleHostsHint is what to do about a schedule-hosts row that warns.
func scheduleHostsHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "change the schedules with phenix experiment edit <experiment>, or start minimega on the node and " +
		"connect it to the mesh"
}

// ccClientsCheck warns about the running VMs that phenix needs a miniccc client in but that have none in minimega's
// cc clients, and about the clients that minimega logged with a miniccc version that differs from its own.
func ccClientsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	inv := in.minimegaInventory(ctx)
	if inv.why != "" {
		return hostSkip(inv.why)
	}

	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	var (
		missing, notes []string
		checked        int
		launches       = make(map[string]time.Time) // the first launch of the VMs of each client host name
		running        []doctorExperiment
	)

	for _, exp := range experiments {
		if exp.running && slices.Contains(inv.namespaces, exp.name) {
			running = append(running, exp)
		}
	}

	for i, exp := range running {
		command := "namespace " + exp.name + " cc clients"

		resps, err := in.askMinimega(ctx, command)
		if err != nil {
			notes = append(notes, fmt.Sprintf("the clients of %s are not checked, because %s", exp.name,
				minimegaAskProblem(command, err, in.timeout)))

			if rest := len(running) - i - 1; rest > 0 && (errors.Is(err, errNoAnswer) || ctx.Err() != nil) {
				notes = append(notes, fmt.Sprintf("the clients of %s are not checked",
					plural(rest, "more running experiment", "more running experiments")))

				break
			}

			continue
		}

		clients := make(map[string]bool)

		for _, row := range minimegaRows(resps) {
			uuid := strings.ToLower(row["uuid"])
			clients[uuid] = true
			vm, ok := inv.vmByUUID(exp.name, uuid)
			in.addLaunch(launches, row["hostname"], vm, ok)
		}

		for _, vm := range exp.vms {
			mmVM, ok := inv.vm(exp.name, vm.name)
			if vm.cc == "" || !ok || mmVM.state != "RUNNING" || mmVM.uptime < ccStartGrace {
				continue
			}

			checked++

			if !clients[strings.ToLower(mmVM.uuid)] {
				missing = append(missing, fmt.Sprintf("%s VM %s (%s)", exp.name, vm.name, vm.cc))
			}
		}
	}

	var problems []string

	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("VMs that have %s in minimega, but phenix needs one: %s",
			noClientPhrase, shortList(missing)))
	}

	if len(launches) > 0 {
		mismatched, why := in.minicccMismatches(launches)

		switch {
		case why != "":
			notes = append(notes, "the miniccc versions are not checked, because "+why)
		case len(mismatched) > 0:
			problems = append(problems, fmt.Sprintf("minimega logged a miniccc version that %s for these clients: %s",
				minicccDiffPhrase, shortList(mismatched)))
		}
	}

	for i := range notes {
		notes[i] = capitalize(notes[i])
	}

	switch {
	case len(problems) > 0:
		return warnCheck(strings.Join(append(problems, notes...), ". "))
	case checked == 0:
		return passCheck(strings.Join(append([]string{fmt.Sprintf("no running VM that phenix needs miniccc in has run "+
			"for %s or more", ccStartGrace)}, notes...), ". "))
	}

	return passCheck(strings.Join(append([]string{fmt.Sprintf("each of the %d running VMs that phenix needs "+
		"miniccc in has a client", checked)}, notes...), ". "))
}

// vm returns the VM name in the namespace ns, if minimega has it.
func (inv *doctorInventory) vm(ns, name string) (doctorMMVM, bool) {
	i := slices.IndexFunc(inv.vms, func(vm doctorMMVM) bool { return vm.namespace == ns && vm.name == name })
	if i < 0 {
		return doctorMMVM{namespace: "", name: "", state: "", pid: 0, host: "", uuid: "", memory: 0, uptime: 0}, false
	}

	return inv.vms[i], true
}

// addLaunch keeps in launches the first launch time of the client host's VMs. A VM that minimega does not show gives
// the zero time, so each logged line counts for that host.
func (in *doctorInput) addLaunch(launches map[string]time.Time, host string, vm doctorMMVM, ok bool) {
	launch := time.Time{}
	if ok {
		launch = in.now().Add(-vm.uptime - logTimeSlack)
	}

	if first, seen := launches[host]; !seen || launch.Before(first) {
		launches[host] = launch
	}
}

// vmByUUID returns the VM with the lowercase uuid in the namespace ns, if minimega has it.
func (inv *doctorInventory) vmByUUID(ns, uuid string) (doctorMMVM, bool) {
	i := slices.IndexFunc(inv.vms, func(vm doctorMMVM) bool {
		return vm.namespace == ns && strings.ToLower(vm.uuid) == uuid
	})
	if i < 0 {
		return doctorMMVM{namespace: "", name: "", state: "", pid: 0, host: "", uuid: "", memory: 0, uptime: 0}, false
	}

	return inv.vms[i], true
}

// minicccMismatches reads the end of minimega's -logfile for the client hosts in launches that minimega logged with
// another miniccc version after their VMs launched. Each has the last version and time it logged. It says why it read
// nothing.
func (in *doctorInput) minicccMismatches(launches map[string]time.Time) ([]string, string) {
	var log string
	if in.found != nil {
		log = in.found.minimegaArgs["logfile"]
	}

	switch {
	case log == "":
		return nil, "minimega gives no -logfile"
	case !filepath.IsAbs(log):
		return nil, "minimega's -logfile " + log + " is relative to the directory of minimega, which doctor does not know"
	}

	info, err := in.env.stat(log)
	if err != nil {
		return nil, "this user cannot read " + log
	}

	data, err := in.env.readAt(log, max(0, info.Size()-minimegaLogTail), minimegaLogTail)
	if err != nil {
		return nil, "this user cannot read " + log
	}

	latest := make(map[string]string)

	for _, m := range minicccMismatch.FindAllStringSubmatch(string(data), -1) {
		launch, ok := launches[m[2]]
		if !ok {
			continue
		}

		logged, err := time.ParseInLocation(minimegaLogTime, m[1], in.now().Location())
		if err == nil && logged.Before(launch) {
			continue
		}

		latest[m[2]] = "miniccc " + m[3]
		if err == nil {
			latest[m[2]] += ", logged " + m[1]
		}
	}

	items := make([]string, 0, len(latest))
	for _, host := range slices.Sorted(maps.Keys(latest)) {
		items = append(items, fmt.Sprintf("%s (%s)", host, latest[host]))
	}

	return items, ""
}

// ccClientsHint is what to do about each problem of a cc-clients row.
func ccClientsHint(c doctorCheck) string {
	return hostHints(c, [][2]string{
		{noClientPhrase, "make sure that miniccc runs in each VM that the row names: copy it into the image with " +
			"phenix image inject-miniexe <miniccc> <image>, and start it when the VM boots"},
		{minicccDiffPhrase, "copy the miniccc of the same minimega release into the images with phenix image " +
			"inject-miniexe <miniccc> <image>"},
	})
}
