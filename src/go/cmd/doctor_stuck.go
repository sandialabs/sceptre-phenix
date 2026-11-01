package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	stuckStatusName = "stuck-status"
	uiLimitsName    = "ui-limits"
	phenixProgram   = "phenix"
	uiCommand       = "ui"

	// Phrases in the details of rows, which their hints look for.
	stuckPhrase       = "but no phenix process can own them"
	interruptedPhrase = "was interrupted"
)

// doctorStuck is what the stored status of a running experiment marks as in progress.
type doctorStuck struct {
	apps        []string // the apps whose running flag is set, other than scorch
	scorch      bool     // the running flag of scorch is set
	scorchRun   int      // the SCORCH run that the status of scorch names, or -1
	pending     bool     // delayed VM starts or post-start apps wait for phenix ui
	interrupted bool     // phenix could not resume the start
}

// items names each part of s in progress, for the experiment name.
func (s doctorStuck) items(name string) []string {
	items := make([]string, 0, len(s.apps)+2) //nolint:mnd // the SCORCH run and the delayed starts

	for _, a := range s.apps {
		items = append(items, fmt.Sprintf("app %s of %s", a, name))
	}

	switch {
	case s.scorch && s.scorchRun >= 0:
		items = append(items, fmt.Sprintf("SCORCH run %d of %s", s.scorchRun, name))
	case s.scorch:
		items = append(items, "a SCORCH run of "+name)
	}

	if s.pending {
		items = append(items, "the delayed starts of "+name)
	}

	return items
}

// doctorUI is whether a phenix ui answers on unix-socket, which a run shares.
type doctorUI struct {
	answered bool
	why      string // why no phenix ui answered, or ""
}

// phenixUI asks the phenix ui on unix-socket for its options once in a run, unless the server check already did.
func (in *doctorInput) phenixUI(ctx context.Context) *doctorUI {
	if in.found != nil && in.found.ui != nil {
		return in.found.ui
	}

	ui := &doctorUI{answered: false, why: ""}
	socket := in.text("unix-socket")

	if socket == "" {
		ui.why = "unix-socket is empty"
	} else {
		rows, answered := serverChecks(ctx, socket, in.timeout, in.server, in.env.lstat, "", "")
		ui.answered = answered

		if !answered && len(rows) > 0 {
			ui.why = rows[0].Detail
		}
	}

	if in.found != nil {
		in.found.ui = ui
	}

	return ui
}

// doctorProc is a phenix process on this host, other than doctor.
type doctorProc struct {
	pid  int
	args []string // the command line, the program first
}

// isUI reports whether p runs phenix ui: its first argument that is not a flag
// or the value of a flag is ui.
func (p doctorProc) isUI() bool {
	flags := rootCmd.PersistentFlags()

	for i := 1; i < len(p.args); i++ {
		arg := p.args[i]

		switch {
		case arg == "--":
			return false
		case !strings.HasPrefix(arg, "-"):
			return arg == uiCommand
		}

		// "--flag value" gives the value in the next argument, unless the flag needs no value
		if name, ok := strings.CutPrefix(arg, "--"); ok && !strings.Contains(name, "=") {
			if f := flags.Lookup(name); f != nil && f.NoOptDefVal == "" {
				i++
			}
		}
	}

	return false
}

// doctorProcs is the phenix processes on this host, which a run shares.
type doctorProcs struct {
	list []doctorProc
	why  string // why doctor did not list them, or ""
}

// phenixProcesses lists once in a run the phenix processes in /proc, except phenix doctor.
func (in *doctorInput) phenixProcesses(ctx context.Context) *doctorProcs {
	if in.found != nil && in.found.procs != nil {
		return in.found.procs
	}

	procs := &doctorProcs{list: nil, why: ""}
	if in.found != nil {
		in.found.procs = procs
	}

	if in.system.os != linuxOS {
		procs.why = "only Linux has /proc"

		return procs
	}

	entries, err := in.env.readDir("/proc")
	if err != nil {
		procs.why = "this host has no /proc to read"

		return procs
	}

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		if ctx.Err() != nil {
			procs.why = "the time ran out"

			return procs
		}

		if args, ok := in.phenixArgs(pid); ok && !slices.Contains(args, "doctor") {
			procs.list = append(procs.list, doctorProc{pid: pid, args: args})
		}
	}

	return procs
}

// phenixArgs is the command line of the process pid, if it runs the phenix program.
func (in *doctorInput) phenixArgs(pid int) ([]string, bool) {
	data, err := in.env.readFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(data) == 0 {
		return nil, false
	}

	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")

	return args, filepath.Base(args[0]) == phenixProgram
}

// owner names what can own the operations of experiment name that the store marks as in progress, or "".
func (procs *doctorProcs) owner(ui *doctorUI, name string) string {
	if ui.answered {
		return "phenix ui answers"
	}

	for _, p := range procs.list {
		if p.isUI() {
			return fmt.Sprintf("phenix ui runs as process %d", p.pid)
		}

		if slices.Contains(p.args[1:], name) || slices.Contains(p.args[1:], "all") {
			return fmt.Sprintf("phenix process %d names the experiment", p.pid)
		}
	}

	return ""
}

// stuckStatusCheck warns about the operations that the store marks as in progress for a running experiment when no
// phenix process can own them, such as after phenix ui stopped during a start, and about interrupted starts.
func stuckStatusCheck(ctx context.Context, in *doctorInput) doctorCheck {
	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	var (
		stuck, owned, interrupted []string
		owners                    []string
		running                   int
		ui                        = in.phenixUI(ctx)
		procs                     = in.phenixProcesses(ctx)
	)

	for _, exp := range experiments {
		if !exp.running {
			continue
		}

		running++

		if exp.stuck.interrupted {
			interrupted = append(interrupted, exp.name)
		}

		items := exp.stuck.items(exp.name)
		if len(items) == 0 {
			continue
		}

		if owner := procs.owner(ui, exp.name); owner != "" {
			owned = append(owned, items...)

			if !slices.Contains(owners, owner) {
				owners = append(owners, owner)
			}
		} else {
			stuck = append(stuck, items...)
		}
	}

	var parts []string

	if len(stuck) > 0 {
		parts = append(parts, fmt.Sprintf("the store marks some operations as in progress, %s. The operations are: %s. %s",
			stuckPhrase, shortList(stuck), noOwnerReason(ui, procs)))
	}

	if len(interrupted) > 0 {
		parts = append(parts, fmt.Sprintf("The start of %s %s, so some VMs did not start",
			shortList(interrupted), interruptedPhrase))
	}

	switch {
	case len(parts) > 0:
		parts[0] = capitalize(parts[0])

		return warnCheck(strings.Join(parts, ". "))
	case len(owned) > 0:
		return passCheck(fmt.Sprintf("the store marks some operations as in progress, and %s, so it can own them. "+
			"The operations are: %s", strings.Join(owners, " and "), shortList(owned)))
	case running == 0:
		return passCheck("no stored experiment runs")
	}

	return passCheck(fmt.Sprintf("no app run, SCORCH run or delayed start of the %s is marked as in progress",
		plural(running, "running experiment", "running experiments")))
}

// noOwnerReason says why no phenix process can own the operations in progress.
func noOwnerReason(ui *doctorUI, procs *doctorProcs) string {
	reason := "No phenix ui answers. "
	if ui.why != "" {
		reason += "Reason: " + ui.why + ". "
	}

	if procs.why != "" {
		return reason + "Doctor cannot list the phenix processes, because " + procs.why
	}

	return reason + "No other phenix process on this host names the experiment"
}

// stuckStatusHint is what to do about a stuck-status row that warns.
func stuckStatusHint(c doctorCheck) string {
	var hints []string

	if strings.Contains(c.Detail, stuckPhrase) {
		hints = append(hints, "start or restart phenix ui. As it starts, it clears the app running flags of the "+
			"running experiments and resumes their delayed starts")
	}

	if strings.Contains(c.Detail, interruptedPhrase) {
		hints = append(hints, "for an interrupted start, stop the experiment with phenix experiment stop "+
			"<experiment>, then start it again")
	}

	for i := 1; i < len(hints); i++ {
		hints[i] = capitalize(hints[i])
	}

	return strings.Join(hints, ". ")
}

// uiProcess finds the phenix ui process: the peer of unix-socket, else a phenix ui in /proc. It says why it found none.
func (in *doctorInput) uiProcess(ctx context.Context) (int, string) {
	if socket := in.text("unix-socket"); socket != "" && in.env.socketPeer != nil {
		if pid, err := in.env.socketPeer(ctx, socket, in.timeout); err == nil && pid > 0 {
			if _, ok := in.phenixArgs(pid); ok {
				return pid, ""
			}
		}
	}

	procs := in.phenixProcesses(ctx)
	if procs.why != "" {
		return 0, "doctor cannot list the processes, because " + procs.why
	}

	for _, p := range procs.list {
		if p.isUI() {
			return p.pid, ""
		}
	}

	return 0, "no phenix ui process runs on this host, or it runs in another container"
}

// uiLimitsCheck warns when the phenix ui process on this host has more than 80% of its soft limit of open files open.
func uiLimitsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	if in.system.os != linuxOS {
		return hostSkip("doctor reads the limits of phenix ui in /proc, which only Linux has")
	}

	pid, why := in.uiProcess(ctx)
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
		return passCheck(fmt.Sprintf("phenix ui (pid %d) has no limit of open files", pid))
	}

	entries, err := in.env.readDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return hostSkip(fmt.Sprintf("this user cannot count the open files in /proc/%d/fd, such as when phenix ui "+
			"runs as another user", pid))
	}

	hardText := strconv.Itoa(hard)
	if hard == unlimitedOpenFiles {
		hardText = unlimitedText
	}

	detail := fmt.Sprintf("phenix ui (pid %d) can open %d files (soft limit, hard limit %s). %s",
		pid, soft, hardText, capitalize(openFilesNow(len(entries))))

	if len(entries)*percent > soft*busyFilesPercent {
		return warnCheck(fmt.Sprintf("%s, so more than %d percent %s", detail, busyFilesPercent, filesBusyPhrase))
	}

	return passCheck(detail)
}

// uiLimitsHint is what to do about a ui-limits row that warns.
func uiLimitsHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "increase the open files limit of phenix ui with LimitNOFILE= in its systemd unit, or with docker run " +
		"--ulimit nofile=<n>:<n>, then restart phenix ui"
}
