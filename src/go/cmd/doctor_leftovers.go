package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/activeshadow/libminimega/minicli"
)

const (
	leftoversName       = "leftovers"
	leftoverSnapshotRow = "leftovers snapshots"
	leftoverFilesRow    = "leftovers files"
	leftoverMountsRow   = "leftovers mounts"
	leftoverTapsRow     = "leftovers taps"
	leftoverMirrorsRow  = "leftovers mirrors"

	// phenix names the snapshot of a VM's first drive <head node>_<experiment>_<VM>_snapshot, in minimega's -filepath.
	snapshotSuffix = "_snapshot"
	// minimega names its taps mega_tap<N> (TapFmt), and the mirror of a capture tap mirror-<tap>.
	minimegaTapName = "mega_tap"
	mirrorPrefix    = "mirror-"
	sysClassNet     = "/sys/class/net"

	// Phrases in the details of rows, which their hints look for.
	orphanTapPhrase  = "taps that no VM or host tap owns"
	orphanPortPhrase = "Open vSwitch ports"
	ovsNoDevice      = "could not open network device"
)

// validNamespace matches a name minimega accepts for a namespace.
var validNamespace = regexp.MustCompile(`^[a-zA-Z0-9-_.]+$`)

// doctorInventory is what minimega gives about its namespaces, which a run of doctor shares.
type doctorInventory struct {
	host           string          // the host name minimega answers from, without the hostname-suffixes
	vms            []doctorMMVM    // the VMs of every namespace, on every node of the namespace
	taps           map[string]bool // the taps of those VMs, and the host taps of every namespace
	bridgeCaptures map[string]bool // the bridges with a pcap capture of the whole bridge
	why            string          // why doctor does not know them, or ""
}

// doctorMMVM is one VM in minimega's vm info.
type doctorMMVM struct {
	namespace, name, state string
	pid                    int
}

// alive reports whether minimega still runs the VM: its state is not QUIT or ERROR.
func (vm doctorMMVM) alive() bool {
	return vm.state != "QUIT" && vm.state != "ERROR"
}

// askMinimega sends minimega command, waiting at most the doctor timeout for its answer.
func (in *doctorInput) askMinimega(ctx context.Context, command string) ([]*minicli.Response, error) {
	base := in.text("base-dir.minimega")

	return boundedCall(ctx, in.timeout, func() ([]*minicli.Response, error) {
		return in.minimega(base, command, in.timeout)
	})
}

// minimegaInventory asks minimega once in a run for its namespaces, then for the VMs, host taps and captures of each.
// It sends namespace <name> only for a namespace that minimega listed, so it creates no namespace.
func (in *doctorInput) minimegaInventory(ctx context.Context) *doctorInventory {
	if in.found != nil && in.found.inventory != nil {
		return in.found.inventory
	}

	inv := &doctorInventory{host: "", vms: nil, taps: map[string]bool{}, bridgeCaptures: map[string]bool{}, why: ""}
	if in.found != nil {
		in.found.inventory = inv
	}

	if in.found == nil || !in.found.minimegaUp {
		inv.why = "minimega gave no version"

		return inv
	}

	resps, err := in.askMinimega(ctx, "namespace")
	if err != nil {
		inv.why = minimegaAskProblem("namespace", err, in.timeout)

		return inv
	}

	for _, r := range resps {
		if inv.host == "" && r.Host != "" {
			inv.host = trimSuffixes(r.Host, in.text("hostname-suffixes"))
		}
	}

	for _, row := range minimegaRows(resps) {
		ns := row["namespace"]
		if !validNamespace.MatchString(ns) {
			continue
		}

		for _, part := range []string{"vm info", "tap", "capture"} {
			command := "namespace " + ns + " " + part

			resps, err := in.askMinimega(ctx, command)
			if err != nil {
				inv.why = minimegaAskProblem(command, err, in.timeout)

				return inv
			}

			inv.add(ns, part, minimegaRows(resps))
		}
	}

	return inv
}

// add records rows, minimega's answer to part (vm info, tap or capture) in the namespace ns.
func (inv *doctorInventory) add(ns, part string, rows []map[string]string) {
	for _, row := range rows {
		switch part {
		case "vm info":
			pid, _ := strconv.Atoi(row["pid"])
			inv.vms = append(inv.vms, doctorMMVM{namespace: ns, name: row["name"], state: row["state"], pid: pid})

			for _, tap := range minimegaList(row["tap"]) {
				inv.taps[tap] = true
			}
		case "tap":
			inv.taps[row["tap"]] = true
		case "capture":
			// a pcap capture of a VM names its interface; one of a whole bridge mirrors the bridge to a tap
			if row["type"] == "pcap" && row["interface"] == "" {
				inv.bridgeCaptures[row["bridge"]] = true
			}
		}
	}
}

// liveNamespaces are the namespaces in which minimega runs a VM. leftovers keeps what belongs to them, because the
// store that doctor reads can be another store than the one phenix ui uses.
func (inv *doctorInventory) liveNamespaces() map[string]bool {
	live := make(map[string]bool)

	for _, vm := range inv.vms {
		if vm.alive() {
			live[vm.namespace] = true
		}
	}

	return live
}

// vmAlive reports whether minimega runs the VM name in the namespace ns.
func (inv *doctorInventory) vmAlive(ns, name string) bool {
	return slices.ContainsFunc(inv.vms, func(vm doctorMMVM) bool {
		return vm.namespace == ns && vm.name == name && vm.alive()
	})
}

// minimegaAskProblem explains err, the failure of minimega to answer command.
func minimegaAskProblem(command string, err error, timeout time.Duration) string {
	if errors.Is(err, errNoAnswer) {
		return fmt.Sprintf("minimega did not answer %s within %s", command, timeout)
	}

	return fmt.Sprintf("minimega did not answer %s: %v", command, err)
}

// minimegaRows is each row of the tabular responses, by column name.
func minimegaRows(resps []*minicli.Response) []map[string]string {
	var rows []map[string]string

	for _, resp := range resps {
		for _, values := range resp.Tabular {
			row := make(map[string]string, len(resp.Header))

			for i, name := range resp.Header {
				if i < len(values) {
					row[name] = values[i]
				}
			}

			rows = append(rows, row)
		}
	}

	return rows
}

// minimegaList splits a list that minimega's vm info prints, such as [mega_tap1, mega_tap2].
func minimegaList(value string) []string {
	value = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(value), "["), "]")

	var items []string

	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}

	return items
}

// trimSuffixes removes from name each suffix in the comma-separated list suffixes, as phenix does for host names.
func trimSuffixes(name, suffixes string) string {
	for suffix := range strings.SplitSeq(suffixes, ",") {
		name = strings.TrimSuffix(name, suffix)
	}

	return name
}

// doctorLeftovers is what the leftovers check found that it is safe to delete.
type doctorLeftovers struct {
	bytes int64 // the size of the snapshot files and directories of deleted experiments
	items int
}

// leftoversChecks looks for what phenix and minimega can leave behind: snapshot files and directories of experiments
// that the store does not have, VM file system mounts, taps and Open vSwitch ports that no VM owns, and mirrors that
// no capture owns. It never fails.
func leftoversChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	found := &doctorLeftovers{bytes: 0, items: 0}
	experiments, why := in.storedExperiments(ctx)

	rows := make([]doctorCheck, 0, 5) //nolint:mnd // the five rows
	rows = append(rows,
		leftoverSnapshotsCheck(ctx, in, experiments, why, found),
		leftoverFilesCheck(ctx, in, experiments, why, found),
		leftoverMountsCheck(ctx, in, experiments, why),
	)
	rows = append(rows, leftoverNetworkChecks(ctx, in)...)

	for i, name := range []string{leftoverSnapshotRow, leftoverFilesRow, leftoverMountsRow, leftoverTapsRow, leftoverMirrorsRow} {
		rows[i].Name = name
	}

	if in.found != nil {
		in.found.leftovers = found
	}

	return rows
}

// headNode is the name of this head node in the names of snapshot files: the host minimega answers from, else this
// host, without the hostname-suffixes.
func (in *doctorInput) headNode(ctx context.Context) string {
	if inv := in.minimegaInventory(ctx); inv.host != "" {
		return inv.host
	}

	name, err := in.hostname()
	if err != nil {
		return ""
	}

	return trimSuffixes(name, in.text("hostname-suffixes"))
}

// leftoverSnapshotsCheck warns about the snapshot files of this head node in the images directory whose experiment
// the store does not have. A file that can belong to a stored experiment or to a namespace in which minimega runs a VM,
// such as foo_bar_vm_snapshot when foo is stored, is not a leftover.
func leftoverSnapshotsCheck(
	ctx context.Context, in *doctorInput, experiments []doctorExperiment, why string, found *doctorLeftovers,
) doctorCheck {
	if why != "" {
		return hostSkip(why)
	}

	dir := in.imagesDir()

	entries, err := in.env.readDir(dir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return hostSkip(dir + " does not exist")
	case err != nil:
		return hostSkip(fmt.Sprintf("this user cannot list %s: %v", dir, unwrapPathError(err)))
	}

	head := in.headNode(ctx)
	if head == "" {
		return hostSkip("the name of this head node is not known")
	}

	live := in.minimegaInventory(ctx).liveNamespaces()
	kept := func(middle string) bool {
		for ns := range live {
			if strings.HasPrefix(middle, ns+"_") {
				return true
			}
		}

		return slices.ContainsFunc(experiments, func(e doctorExperiment) bool { return strings.HasPrefix(middle, e.name+"_") })
	}

	var (
		items []string
		total int64
	)

	for _, entry := range entries {
		name := entry.Name()
		middle, ok := strings.CutPrefix(name, head+"_")

		if !ok || entry.IsDir() || !strings.HasSuffix(middle, snapshotSuffix) {
			continue
		}

		middle = strings.TrimSuffix(middle, snapshotSuffix)
		if kept(middle) {
			continue
		}

		var size int64
		if info, err := entry.Info(); err == nil {
			size = info.Size()
		}

		total += size
		items = append(items, fmt.Sprintf("%s (%s)", name, doctorBytes(size)))
	}

	if len(items) == 0 {
		return passCheck(fmt.Sprintf("no snapshot files of deleted experiments in %s", dir))
	}

	found.bytes += total
	found.items += len(items)

	return warnCheck(fmt.Sprintf("%d snapshot files in %s belong to experiments that the store does not have, "+
		"%s in all: %s", len(items), dir, doctorBytes(total), shortList(items)))
}

// leftoverFilesCheck warns about the directories of experiments that the store does not have, and in which minimega
// runs no VM: the base directories in <base-dir.phenix>/experiments, and the file directories (<experiment>/files and
// miniccc_responses) in the images directory. It measures them for at most the doctor timeout.
func leftoverFilesCheck(
	ctx context.Context, in *doctorInput, experiments []doctorExperiment, why string, found *doctorLeftovers,
) doctorCheck {
	if why != "" {
		return hostSkip(why)
	}

	live := in.minimegaInventory(ctx).liveNamespaces()
	stored := func(name, path string) bool {
		_, ok := experimentNamed(experiments, name)

		return ok || live[name] || slices.ContainsFunc(experiments, func(e doctorExperiment) bool {
			return e.baseDir != "" && filepath.Clean(e.baseDir) == path
		})
	}

	var dirs []string

	base := filepath.Join(in.text("base-dir.phenix"), "experiments")
	if entries, err := in.env.readDir(base); err == nil {
		for _, entry := range entries {
			if path := filepath.Join(base, entry.Name()); entry.IsDir() && !stored(entry.Name(), path) {
				dirs = append(dirs, path)
			}
		}
	}

	images := in.imagesDir()
	if entries, err := in.env.readDir(images); err == nil {
		for _, entry := range entries {
			path := filepath.Join(images, entry.Name())
			if !entry.IsDir() || stored(entry.Name(), path) || !in.experimentFilesDir(path) {
				continue
			}

			dirs = append(dirs, path)
		}
	}

	if len(dirs) == 0 {
		return passCheck(fmt.Sprintf("no directories of deleted experiments in %s or %s", base, images))
	}

	ctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()

	var (
		items []string
		total int64
		short bool
	)

	for _, dir := range dirs {
		size, whole := in.env.dirSize(ctx, dir)
		total += size
		short = short || !whole

		items = append(items, fmt.Sprintf("%s (%s)", dir, doctorBytes(size)))
	}

	found.bytes += total
	found.items += len(items)

	size := doctorBytes(total) + " in all"
	if short {
		size = fmt.Sprintf("at least %s in all (not measured in full within %s)", doctorBytes(total), in.timeout)
	}

	return warnCheck(fmt.Sprintf("%d directories belong to experiments that the store does not have, %s: %s",
		len(items), size, shortList(items)))
}

// experimentFilesDir reports whether dir, in the images directory, holds what minimega keeps for an experiment: a
// files or miniccc_responses directory.
func (in *doctorInput) experimentFilesDir(dir string) bool {
	for _, sub := range []string{"files", "miniccc_responses"} {
		if info, err := in.env.stat(filepath.Join(dir, sub)); err == nil && info.IsDir() {
			return true
		}
	}

	return false
}

// dirSize adds up the sizes of the files under dir until ctx ends, and reports whether it measured every file. It
// does not follow symbolic links.
func (e doctorEnv) dirSize(ctx context.Context, dir string) (int64, bool) {
	var total int64

	entries, err := e.readDir(dir)
	if err != nil {
		return 0, true
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			return total, false
		}

		if entry.IsDir() {
			size, whole := e.dirSize(ctx, filepath.Join(dir, entry.Name()))
			total += size

			if !whole {
				return total, false
			}

			continue
		}

		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}

	return total, true
}

// leftoverMountsCheck warns about the VM file system mounts under mount-dir (<mount-dir>/<experiment>/<VM>, which the
// VM file browser makes with minimega cc mount) whose experiment the store does not have and minimega does not run, or
// whose VM minimega does not run.
func leftoverMountsCheck(ctx context.Context, in *doctorInput, experiments []doctorExperiment, why string) doctorCheck {
	if why != "" {
		return hostSkip(why)
	}

	if _, err := in.env.readFile("/proc/mounts"); err != nil {
		return hostSkip("this host has no /proc/mounts to read")
	}

	dir := filepath.Clean(derivedBaseDir(in.text("mount-dir"), in.text("base-dir.phenix"), "mounts"))

	var (
		items     []string
		unchecked int
		mounts    int
	)

	live := in.minimegaInventory(ctx).liveNamespaces()

	for _, m := range procMounts(in.env) {
		rel, err := filepath.Rel(dir, m[1])
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}

		mounts++
		exp, vm, _ := strings.Cut(rel, string(filepath.Separator))

		if _, ok := experimentNamed(experiments, exp); !ok && !live[exp] {
			items = append(items, fmt.Sprintf("%s (the store has no experiment %s)", m[1], exp))

			continue
		}

		inv := in.minimegaInventory(ctx)

		switch {
		case inv.why != "":
			unchecked++
		case !inv.vmAlive(exp, vm):
			items = append(items, fmt.Sprintf("%s (minimega does not run the VM %s)", m[1], vm))
		}
	}

	var note string
	if unchecked > 0 {
		note = fmt.Sprintf(". It did not check whether the VMs of %d mounts run, because %s",
			unchecked, in.minimegaInventory(ctx).why)
	}

	switch {
	case len(items) > 0:
		return warnCheck(fmt.Sprintf("VM file system mounts under %s with no running VM: %s%s", dir, shortList(items), note))
	case mounts == 0:
		return passCheck("no VM file system mounts under " + dir)
	}

	return passCheck(fmt.Sprintf("%d VM file system mounts under %s, each of an experiment that the store has or "+
		"minimega runs%s", mounts, dir, note))
}

// ovsPort is a port on an Open vSwitch bridge, as ovs-vsctl show prints it.
type ovsPort struct {
	bridge, name, err string // err is the error of its interface, or ""
}

// parseOVSPorts returns each port in the output of ovs-vsctl show, with the error of its interface.
func parseOVSPorts(out string) []ovsPort {
	var (
		ports  []ovsPort
		bridge string
	)

	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "Bridge "):
			bridge = strings.Trim(strings.TrimPrefix(line, "Bridge "), `"`)
		case strings.HasPrefix(line, "Port "):
			ports = append(ports, ovsPort{bridge: bridge, name: strings.Trim(strings.TrimPrefix(line, "Port "), `"`), err: ""})
		case strings.HasPrefix(line, "error: ") && len(ports) > 0:
			ports[len(ports)-1].err = strings.Trim(strings.TrimPrefix(line, "error: "), `"`)
		}
	}

	return ports
}

// parseOVSRecords returns the records of ovs-vsctl list, each a map from column to value: lines "column : value",
// with an empty line between records. A quoted value loses its quotes.
func parseOVSRecords(out string) []map[string]string {
	var (
		records []map[string]string
		record  map[string]string
	)

	for line := range strings.SplitSeq(out, "\n") {
		column, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(line) == "" {
			record = nil

			continue
		}

		if record == nil {
			record = make(map[string]string)
			records = append(records, record)
		}

		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}

		record[strings.TrimSpace(column)] = value
	}

	return records
}

// ovsMirrors returns the mirrors on each bridge, by name, from two ovs-vsctl list commands, each bounded by the
// doctor timeout. It reports why it read none, or "".
func (in *doctorInput) ovsMirrors(ctx context.Context) (map[string][]string, string) {
	list := func(columns, table string) ([]map[string]string, error) {
		ctx, cancel := context.WithTimeout(ctx, in.timeout)
		defer cancel()

		out, err := in.command(ctx, "ovs-vsctl", ovsTimeoutArg(in.timeout), "--columns="+columns, "list", table)
		if err != nil {
			return nil, fmt.Errorf("ovs-vsctl list %s: %w", table, err)
		}

		return parseOVSRecords(string(out)), nil
	}

	mirrors, err := list("_uuid,name", "Mirror")
	if err != nil {
		return nil, err.Error()
	}

	names := make(map[string]string, len(mirrors))
	for _, m := range mirrors {
		names[m["_uuid"]] = m["name"]
	}

	bridges, err := list("name,mirrors", "Bridge")
	if err != nil {
		return nil, err.Error()
	}

	onBridge := make(map[string][]string)

	for _, b := range bridges {
		for _, uuid := range minimegaList(b["mirrors"]) {
			if name, ok := names[uuid]; ok {
				onBridge[b["name"]] = append(onBridge[b["name"]], name)
			}
		}
	}

	return onBridge, ""
}

// leftoverNetworkChecks gives the taps row and the mirrors row: the minimega taps (mega_tap*) on this host and on the
// Open vSwitch bridges that no VM or host tap in any minimega namespace owns, the ports whose network device is gone,
// and the minimega mirrors (mirror-<tap>) that no capture owns.
func leftoverNetworkChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	if in.system.os != linuxOS {
		skip := hostSkip("minimega runs only on Linux")

		return []doctorCheck{skip, skip}
	}

	inv := in.minimegaInventory(ctx)
	if inv.why != "" {
		skip := hostSkip(inv.why)

		return []doctorCheck{skip, skip}
	}

	var (
		portsWhy, mirrorsWhy string
		ports                []ovsPort
		mirrors              map[string][]string
	)

	if _, err := in.env.lookPath("ovs-vsctl"); err != nil {
		portsWhy = "ovs-vsctl is not on PATH"
	} else if show := in.runOVSShow(ctx); show.err != nil {
		portsWhy = "ovs-vsctl show failed (see the openvswitch row)"
	} else {
		ports = parseOVSPorts(string(show.out))
		mirrors, mirrorsWhy = in.ovsMirrors(ctx)
	}

	if portsWhy != "" {
		mirrorsWhy = portsWhy
	}

	var captureWhy string
	if mirrorsWhy != "" && portsWhy == "" && len(inv.bridgeCaptures) > 0 {
		captureWhy = mirrorsWhy
	}

	owned := func(tap string) bool {
		// a capture of a whole bridge can own any tap when its mirrors are not known
		if inv.taps[tap] || captureWhy != "" {
			return true
		}

		// the tap of a capture of a whole bridge is in no namespace's tap list
		for bridge, names := range mirrors {
			if inv.bridgeCaptures[bridge] && slices.Contains(names, mirrorPrefix+tap) {
				return true
			}
		}

		return false
	}

	return []doctorCheck{
		leftoverTapsCheck(in, owned, ports, portsWhy, captureWhy), leftoverMirrorsCheck(inv, mirrors, mirrorsWhy),
	}
}

// leftoverTapsCheck warns about the minimega taps on this host that owned reports no owner for, and about the minimega
// ports on the bridges whose device is gone or that have no owner. portsWhy says why it read no ports, and captureWhy
// why it does not know the taps of captures of whole bridges. In a container, it checks the taps only when it sees
// the network devices of the host.
func leftoverTapsCheck(in *doctorInput, owned func(string) bool, ports []ovsPort, portsWhy, captureWhy string) doctorCheck {
	var taps, gone, unowned []string

	entries, err := in.env.readDir(sysClassNet)

	var tapsWhy string

	switch {
	case err != nil:
		tapsWhy = sysClassNet + " cannot be read"
	case containerHint(in.env) != "" && !hostNetworkVisible(entries, ports):
		tapsWhy = "this container shows no minimega tap, Open vSwitch bridge or ovs-system device, " +
			"so it can have its own network namespace"
		entries = nil
	}

	for _, entry := range entries {
		if name := entry.Name(); strings.Contains(name, minimegaTapName) && !owned(name) {
			taps = append(taps, name)
		}
	}

	for _, p := range ports {
		switch {
		case !strings.Contains(p.name, minimegaTapName):
		case strings.Contains(p.err, ovsNoDevice):
			gone = append(gone, p.bridge+" "+p.name)
		case !owned(p.name):
			unowned = append(unowned, p.bridge+" "+p.name)
		}
	}

	var problems, notes []string

	if len(taps) > 0 {
		problems = append(problems, orphanTapPhrase+": "+shortList(taps))
	}

	if len(gone) > 0 {
		problems = append(problems, orphanPortPhrase+" (bridge and port) whose network device is gone: "+shortList(gone))
	}

	if len(unowned) > 0 {
		problems = append(problems, orphanPortPhrase+" (bridge and port) that no VM or host tap owns: "+shortList(unowned))
	}

	if tapsWhy != "" {
		notes = append(notes, "the taps on this host are not checked, because "+tapsWhy)
	}

	if portsWhy != "" {
		notes = append(notes, "the Open vSwitch ports are not checked, because "+portsWhy)
	}

	if captureWhy != "" {
		notes = append(notes, "the taps that no VM owns are not checked, because a capture of a whole bridge runs, "+
			"and its tap is not known: "+captureWhy)
	}

	for i := range notes {
		notes[i] = capitalize(notes[i])
	}

	switch {
	case len(problems) > 0:
		return warnCheck(strings.Join(append(problems, notes...), ". "))
	case tapsWhy != "" && portsWhy != "":
		return hostSkip(strings.Join(notes, ". "))
	case len(notes) > 0:
		return passCheck("every minimega tap has an owner. " + strings.Join(notes, ". "))
	}

	return passCheck("every minimega tap on this host and on the Open vSwitch bridges has an owner")
}

// hostNetworkVisible reports whether the network devices in entries, from /sys/class/net, can be those of the host: a
// minimega tap, an Open vSwitch bridge in ports, or the ovs-system device of the Open vSwitch kernel module.
func hostNetworkVisible(entries []fs.DirEntry, ports []ovsPort) bool {
	return slices.ContainsFunc(entries, func(entry fs.DirEntry) bool {
		name := entry.Name()

		return strings.Contains(name, minimegaTapName) || name == "ovs-system" ||
			slices.ContainsFunc(ports, func(p ovsPort) bool { return p.bridge == name })
	})
}

// leftoverMirrorsCheck warns about the minimega mirrors (mirror-<tap>) on a bridge with no pcap capture of the whole
// bridge, whose tap is not a VM tap or a host tap either (as minimega tap mirror makes).
func leftoverMirrorsCheck(inv *doctorInventory, mirrors map[string][]string, ovsWhy string) doctorCheck {
	if ovsWhy != "" {
		return hostSkip(ovsWhy)
	}

	var items []string

	for _, bridge := range slices.Sorted(maps.Keys(mirrors)) {
		if inv.bridgeCaptures[bridge] {
			continue
		}

		for _, name := range mirrors[bridge] {
			if tap, ok := strings.CutPrefix(name, mirrorPrefix); ok && !inv.taps[tap] {
				items = append(items, fmt.Sprintf("%s on bridge %s", name, bridge))
			}
		}
	}

	if len(items) > 0 {
		return warnCheck("Open vSwitch mirrors that no capture owns: " + shortList(items))
	}

	return passCheck("no minimega mirror without a capture on the Open vSwitch bridges")
}

// leftoversHint is what to do about a leftovers row that warns.
func leftoversHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case c.Name == leftoverSnapshotRow:
		return "delete each file that the row names, with rm <file>. No stored experiment uses it"
	case c.Name == leftoverFilesRow:
		return "delete each directory that the row names, with rm -r <directory>, after you copy the files you want to keep"
	case c.Name == leftoverMountsRow:
		return "unmount each path with umount <path>, then remove its empty directory"
	case c.Name == leftoverMirrorsRow:
		return "remove each mirror with ovs-vsctl -- --id=@m get mirror <mirror> -- remove bridge <bridge> mirrors @m"
	}

	return hostHints(c, [][2]string{
		{orphanPortPhrase, "delete each port with ovs-vsctl del-port <bridge> <port>"},
		{orphanTapPhrase, "delete each tap with ip link delete <tap>"},
	})
}

// unwrapPathError is the error that a *[fs.PathError] holds, or err.
func unwrapPathError(err error) error {
	var perr *fs.PathError
	if errors.As(err, &perr) {
		return perr.Err
	}

	return err
}

// capitalize makes the first letter of s a capital letter.
func capitalize(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

// doctorBytes is n bytes in the largest binary unit that keeps it at 1 or more, such as 1.5 GiB.
func doctorBytes(n int64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 5; m /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
