package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"phenix/util/common"
)

const (
	kernelModulesName = "kernel-modules"
	nbdName           = "nbd"
	openvswitchName   = "openvswitch"
	imageBuildName    = "image-build"

	linuxOS = "linux"

	sysModuleDir   = "/sys/module"
	procModules    = "/proc/modules"
	modulesDir     = "/lib/modules"
	rebootRequired = "/var/run/reboot-required"

	// minimega's internal/nbd skips a /dev/nbdN with a lock file here or a pid in /sys/block.
	nbdLockDir = "/var/lock"

	defaultOVSRunDir   = "/var/run/openvswitch"
	debootstrapScripts = "/usr/share/debootstrap/scripts"

	imageBuildToolsRow    = "image-build tools"
	imageBuildRootRow     = "image-build root"
	imageBuildLoopRow     = "image-build loop"
	imageBuildKeyringsRow = "image-build keyrings"

	capSysAdmin       = 21 // the bit of CAP_SYS_ADMIN in CapEff
	mountFields       = 2  // the device and mount point that start a line of /proc/mounts
	commandWaitDelay  = time.Second
	kernelModulesNote = " (see the kernel-modules row)"

	// Phrases in the details of rows, which their hints look for.
	qemuNBDMissingPhrase  = "qemu-nbd is not a program on PATH"
	nbdNotLoadedPhrase    = "the nbd module is not loaded"
	nbdBuiltInPhrase      = "nbd is part of the kernel, not a module"
	nbdNoPartitionsPhrase = "the kernel makes no partition devices"
	nbdContainerPhrase    = "this container has no /dev/nbd"
	nbdNoDevicesPhrase    = "/dev has no nbd device"
	nbdNoFreePhrase       = "no free nbd device"
	nbdLockPhrase         = "but no connection"
	nbdGonePhrase         = "which is not running"
	nbdLeftPhrase         = "(qemu-nbd pid"
	ovsMissingPhrase      = "ovs-vsctl is not a program on PATH"
	ovsDeniedPhrase       = "Permission denied"
	ovsNoAnswerPhrase     = "answer ovs-vsctl show"
	ovsSwitchdPhrase      = "ovs-vswitchd does not run"
	ovsModulePhrase       = "the openvswitch kernel module is not loaded"
	noScriptPhrase        = "debootstrap has no script"
	noKeyringPhrase       = "cannot check the Release signature"
)

// validRelease matches a release name that can be a file name in debootstrapScripts.
var validRelease = regexp.MustCompile(`^[a-z0-9][a-z0-9.+_-]*$`)

// imageBuildTools are the programs phenix image build needs past vmdb2, each with the package that holds it on Debian
// and Ubuntu.
//
//nolint:gochecknoglobals // constant list
var imageBuildTools = [][2]string{
	{"debootstrap", "debootstrap"}, {"kpartx", "kpartx"}, {"losetup", "util-linux"}, {"qemu-img", "qemu-utils"},
}

// runDoctorCommand runs the program name with args, and returns what it writes to stdout and stderr.
func runDoctorCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = commandWaitDelay

	return cmd.CombinedOutput() //nolint:wrapcheck // the check explains the error
}

func hostSkip(detail string) doctorCheck {
	return doctorCheck{Name: "", Status: doctorSkip, Detail: "not checked, because " + detail}
}

// moduleState is what doctor knows of a kernel module.
type moduleState int

const (
	moduleMissing moduleState = iota
	moduleLoaded
	moduleBuiltIn // in /sys/module, but not in /proc/modules, which lsmod lists
)

// kernelModule reports the state of the kernel module name.
func (e doctorEnv) kernelModule(name string) moduleState {
	if _, err := e.stat(filepath.Join(sysModuleDir, name)); err != nil {
		return moduleMissing
	}

	data, err := e.readFile(procModules)
	if err != nil {
		return moduleLoaded
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
			return moduleLoaded
		}
	}

	return moduleBuiltIn
}

// procRunning reports whether /proc has the process pid.
func (e doctorEnv) procRunning(pid int) bool {
	_, err := e.stat("/proc/" + strconv.Itoa(pid))

	return err == nil
}

// readPid reads the process id in the file path.
func (e doctorEnv) readPid(path string) (int, error) {
	data, err := e.readFile(path)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(strings.TrimSpace(string(data))) //nolint:wrapcheck // the callers ignore the cause
}

// kernelModulesCheck warns when modprobe cannot load modules for the running kernel, or when the host waits for a
// reboot.
func kernelModulesCheck(in *doctorInput) doctorCheck {
	if in.system.os != linuxOS {
		return hostSkip("kernel modules are a Linux feature")
	}

	if in.system.kernel == "" || in.system.kernel == "unknown" {
		return hostSkip("the release of the running kernel is not known")
	}

	var problems []string

	dir := filepath.Join(modulesDir, in.system.kernel)
	notInContainer := false

	if _, err := in.env.stat(dir); err != nil {
		if _, err := in.env.stat(modulesDir); err != nil && containerHint(in.env) != "" {
			notInContainer = true
		} else {
			problems = append(problems, dir+" does not exist, so modprobe cannot load modules such as nbd and openvswitch. "+
				"A package upgrade can remove the modules of the running kernel")
		}
	}

	if _, err := in.env.stat(rebootRequired); err == nil {
		problems = append(problems, rebootRequired+" exists, so the host waits for a reboot after an upgrade")
	}

	switch {
	case len(problems) > 0:
		return warnCheck(strings.Join(problems, ". "))
	case notInContainer:
		return hostSkip("this container has no " + modulesDir + ". Mount it from the host to check it")
	}

	return passCheck(dir + " exists, and no reboot is pending")
}

// kernelModulesHint is what to do about a kernel-modules row that warns.
func kernelModulesHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "reboot the host into the installed kernel"
}

// nbdCheck checks, by the rules of minimega's internal/nbd, what minimega disk inject needs on this host: qemu-nbd,
// the nbd module in lsmod with max_part, and a /dev/nbdN with no lock file and no pid. Injects at a start and phenix
// image inject-miniexe use disk inject.
func nbdCheck(in *doctorInput) doctorCheck {
	if in.system.os != linuxOS {
		return hostSkip("minimega uses nbd devices only on Linux")
	}

	status := doctorPass

	var problems []string

	warn := func(detail string) {
		problems = append(problems, detail)
		if status == doctorPass {
			status = doctorWarn
		}
	}

	if _, err := in.env.lookPath("qemu-nbd"); err != nil {
		status = doctorFail
		problems = append(problems, qemuNBDMissingPhrase+
			", so minimega disk inject fails (injects at an experiment start and phenix image inject-miniexe)")
	}

	module := in.env.kernelModule(nbdName)

	switch module {
	case moduleMissing:
		warn(nbdNotLoadedPhrase + ". minimega loads it with modprobe nbd max_part=10 only when it runs as root" +
			kernelModulesNote)
	case moduleBuiltIn:
		warn(nbdBuiltInPhrase + ", so lsmod does not show it and minimega disk inject fails (add module 'nbd')")
	case moduleLoaded:
	}

	noPartitions := ", so " + nbdNoPartitionsPhrase + ", such as /dev/nbd0p1, and minimega disk inject fails " +
		"(no partitions found on image)"

	var maxPart string

	if module != moduleMissing {
		data, err := in.env.readFile(filepath.Join(sysModuleDir, nbdName, "parameters", "max_part"))
		maxPart = strings.TrimSpace(string(data))

		switch {
		case err != nil:
			warn("the nbd module has no max_part parameter" + noPartitions)
		case maxPart == "0":
			warn("the nbd module has max_part=0" + noPartitions)
		}
	}

	devices, free, left := in.nbdDevices()

	switch {
	case module != moduleMissing && len(devices) == 0 && containerHint(in.env) != "":
		warn(nbdContainerPhrase + "* device, so minimega disk inject in this container fails")
	case module != moduleMissing && len(devices) == 0:
		warn("the nbd module is loaded, but " + nbdNoDevicesPhrase)
	case len(devices) > 0 && free == 0:
		warn(fmt.Sprintf("%s: each of the %d devices has a pid or a lock file, so minimega disk inject fails "+
			"(no available nbds found)", nbdNoFreePhrase, len(devices)))
	}

	if len(left) > 0 {
		warn("devices that a failed inject can leave, if no inject runs now: " + shortList(left))
	}

	if len(problems) == 0 {
		return passCheck(fmt.Sprintf("%d of %d nbd devices free, max_part=%s", free, len(devices), maxPart))
	}

	return doctorCheck{Name: "", Status: status, Detail: strings.Join(problems, ". ")}
}

// nbdDevices returns the /dev/nbdN devices that minimega's internal/nbd looks at, how many of them it can use, and
// those that a failed inject or a stopped qemu-nbd left behind.
func (in *doctorInput) nbdDevices() ([]string, int, []string) {
	entries, err := in.env.readDir("/dev")
	if err != nil {
		return nil, 0, nil
	}

	var (
		devices, left []string
		free          int
		mounts        = procMounts(in.env)
		container     = containerHint(in.env) != ""
	)

	for _, entry := range entries {
		dev := entry.Name()
		// minimega's rule, which also leaves out the partitions, such as nbd0p1
		if !strings.Contains(dev, nbdName) || strings.Contains(dev, "p") {
			continue
		}

		devices = append(devices, dev)

		lock := filepath.Join(nbdLockDir, "qemu-nbd-"+dev)
		_, lockErr := in.env.stat(lock)
		pid, pidErr := in.env.readPid(filepath.Join("/sys/block", dev, "pid"))

		if errors.Is(lockErr, fs.ErrNotExist) && errors.Is(pidErr, fs.ErrNotExist) {
			free++

			continue
		}

		if item := in.leftNBD(dev, lock, lockErr, pid, pidErr, mounts, container); item != "" {
			left = append(left, item)
		}
	}

	return devices, free, left
}

// leftNBD describes dev when a failed inject or a stopped qemu-nbd left it behind, or is "".
func (in *doctorInput) leftNBD(
	dev, lock string, lockErr error, pid int, pidErr error, mounts [][2]string, container bool,
) string {
	switch {
	case lockErr == nil && errors.Is(pidErr, fs.ErrNotExist):
		return fmt.Sprintf("%s has the lock file %s %s", dev, lock, nbdLockPhrase)
	case pidErr != nil:
		return ""
	case !in.env.procRunning(pid) && container:
		return fmt.Sprintf("%s belongs to pid %d, %s in this container", dev, pid, nbdGonePhrase)
	case !in.env.procRunning(pid):
		return fmt.Sprintf("%s belongs to pid %d, %s", dev, pid, nbdGonePhrase)
	}

	cmdline, err := in.env.readFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")

	if err != nil || len(args) < 2 || filepath.Base(args[0]) != "qemu-nbd" {
		return ""
	}

	item := fmt.Sprintf("%s holds an image %s %d)", dev, nbdLeftPhrase, pid)
	// only minimega's form, qemu-nbd -c <device> <image>, shows the image: other arguments can hold secrets
	if len(args) == 4 && args[1] == "-c" && args[2] == "/dev/"+dev && !strings.HasPrefix(args[3], "-") {
		item = fmt.Sprintf("%s holds %s %s %d)", dev, args[3], nbdLeftPhrase, pid)
	}

	var mounted []string

	for _, m := range mounts {
		if m[0] == "/dev/"+dev || strings.HasPrefix(m[0], "/dev/"+dev+"p") {
			mounted = append(mounted, m[1])
		}
	}

	if len(mounted) > 0 {
		item += ", mounted at " + strings.Join(mounted, ", ")
	}

	return item
}

// procMounts returns the device and mount point of each entry in /proc/mounts.
func procMounts(env doctorEnv) [][2]string {
	data, err := env.readFile("/proc/mounts")
	if err != nil {
		return nil
	}

	var mounts [][2]string

	for line := range strings.SplitSeq(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) >= mountFields {
			mounts = append(mounts, [2]string{unescapeMount(fields[0]), unescapeMount(fields[1])})
		}
	}

	return mounts
}

// unescapeMount replaces the octal escapes of /proc/mounts, such as \040 for a space, with their characters.
func unescapeMount(field string) string {
	if !strings.Contains(field, `\`) {
		return field
	}

	var b strings.Builder

	for i := 0; i < len(field); i++ {
		if field[i] == '\\' && i+4 <= len(field) {
			if n, err := strconv.ParseUint(field[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))

				i += 3

				continue
			}
		}

		b.WriteByte(field[i])
	}

	return b.String()
}

// nbdHint is what to do about each problem of an nbd row.
func nbdHint(c doctorCheck) string {
	return hostHints(c, [][2]string{
		{qemuNBDMissingPhrase, "install qemu-nbd (the qemu-utils package on Debian and Ubuntu)"},
		{nbdNotLoadedPhrase, "load the module as root with modprobe nbd max_part=10"},
		{nbdBuiltInPhrase, "use a kernel that has nbd as a module, because minimega looks for it with lsmod"},
		{nbdNoPartitionsPhrase, "load the module again as root while no nbd device is connected: " +
			"modprobe -r nbd && modprobe nbd max_part=10"},
		{nbdContainerPhrase, "run the container privileged with /dev from the host (docker run --privileged -v /dev:/dev)"},
		{nbdNoDevicesPhrase, "load the module again as root: modprobe -r nbd && modprobe nbd max_part=10"},
		{nbdNoFreePhrase, "disconnect a device that no inject uses with qemu-nbd -d /dev/nbdN"},
		{nbdLockPhrase, "remove the lock file of a device that no qemu-nbd uses"},
		{nbdGonePhrase, "disconnect the device with qemu-nbd -d /dev/nbdN"},
		{nbdLeftPhrase, "when no inject runs, unmount the partition with umount <mount point>, then disconnect the device " +
			"with qemu-nbd -d /dev/nbdN"},
	})
}

// hostHints joins the fix of each phrase in fixes that the detail of c holds, once each, as sentences.
func hostHints(c doctorCheck, fixes [][2]string) string {
	if c.Status == doctorSkip {
		return ""
	}

	var hints []string

	for _, fix := range fixes {
		if strings.Contains(c.Detail, fix[0]) && !slices.Contains(hints, fix[1]) {
			hints = append(hints, fix[1])
		}
	}

	for i := 1; i < len(hints); i++ {
		r := []rune(hints[i])
		hints[i] = string(unicode.ToUpper(r[0])) + string(r[1:])
	}

	return strings.Join(hints, ". ")
}

// openvswitchCheck asks ovsdb-server for the Open vSwitch configuration, as minimega's ovs-vsctl commands do, and
// reports the Open vSwitch version and the experiment bridge.
func openvswitchCheck(ctx context.Context, in *doctorInput) doctorCheck {
	if in.system.os != linuxOS {
		return hostSkip("minimega runs only on Linux")
	}

	if _, err := in.env.lookPath("ovs-vsctl"); err != nil {
		if containerHint(in.env) != "" {
			return hostSkip("ovs-vsctl is not on PATH in this container. minimega runs ovs-vsctl where it runs, " +
				"such as in its own container")
		}

		return failCheck(ovsMissingPhrase + ", so minimega cannot make bridges and taps for VMs")
	}

	var module string
	if in.env.kernelModule(openvswitchName) == moduleMissing {
		module = ovsModulePhrase + ", so ovs-vswitchd cannot make kernel bridges" + kernelModulesNote
	}

	show := in.runOVSShow(ctx)

	if show.err != nil {
		check := ovsShowFailure(show, in.timeout)
		if module != "" {
			check.Detail += ". " + module
		}

		return check
	}

	version, bridges := parseOVSShow(string(show.out))

	var problems []string

	if !in.ovsSwitchdRuns() {
		problems = append(problems, "ovsdb-server answers, but "+ovsSwitchdPhrase+", so the bridges pass no traffic")
	}

	if module != "" {
		problems = append(problems, module)
	}

	summary := "Open vSwitch " + version
	if version == "" {
		summary = "Open vSwitch (version not known)"
	}

	switch {
	case in.text("bridge-mode") == string(common.BridgeModeAuto):
		summary += fmt.Sprintf(", bridge-mode auto, so each experiment uses a bridge with its own name (%d bridges now)",
			len(bridges))
	case slices.Contains(bridges, defaultExperimentBridge):
		summary += ", bridge " + defaultExperimentBridge + " exists"
	default:
		summary += ", no bridge " + defaultExperimentBridge + " yet. minimega makes it when it launches the first VM on it"
	}

	if len(problems) > 0 {
		return warnCheck(strings.Join(problems, ". ") + ". " + summary)
	}

	return passCheck(summary)
}

// defaultExperimentBridge is the bridge of an experiment that sets none, in bridge-mode manual.
const defaultExperimentBridge = "phenix"

// doctorOVSRun is one run of ovs-vsctl show, which a run of doctor shares.
type doctorOVSRun struct {
	out      []byte // stdout and stderr
	err      error
	timedOut bool // the doctor timeout ended the run
}

// runOVSShow runs ovs-vsctl show once in a run, waiting at most the doctor timeout, and returns that run.
func (in *doctorInput) runOVSShow(ctx context.Context) *doctorOVSRun {
	if in.found != nil && in.found.ovsShow != nil {
		return in.found.ovsShow
	}

	ctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()

	out, err := in.command(ctx, "ovs-vsctl", ovsTimeoutArg(in.timeout), "show")
	show := &doctorOVSRun{out: out, err: err, timedOut: ctx.Err() != nil}

	if in.found != nil {
		in.found.ovsShow = show
	}

	return show
}

// ovsTimeoutArg is the --timeout option that makes ovs-vsctl give up after timeout, in whole seconds.
func ovsTimeoutArg(timeout time.Duration) string {
	return "--timeout=" + strconv.Itoa(max(1, int(math.Ceil(timeout.Seconds()))))
}

// ovsShowFailure explains the failure of show, a run of ovs-vsctl show.
func ovsShowFailure(show *doctorOVSRun, timeout time.Duration) doctorCheck {
	out := show.out

	text := strings.TrimSpace(string(out))
	if first, _, ok := strings.Cut(text, "\n"); ok {
		text = first
	}

	switch {
	case strings.Contains(string(out), ovsDeniedPhrase):
		return warnCheck("this user cannot connect to ovsdb-server (" + ovsDeniedPhrase + "). minimega runs ovs-vsctl as root")
	case show.timedOut || strings.Contains(strings.ToLower(text), "timeout"):
		return failCheck(fmt.Sprintf("ovsdb-server did not %s within %s", ovsNoAnswerPhrase, timeout))
	case text == "":
		text = show.err.Error()
	}

	return failCheck(fmt.Sprintf("ovsdb-server does not %s: %s", ovsNoAnswerPhrase, text))
}

// parseOVSShow returns the ovs_version and the bridges in the output of ovs-vsctl show.
func parseOVSShow(out string) (string, []string) {
	var (
		version string
		bridges []string
	)

	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)

		if name, ok := strings.CutPrefix(line, "Bridge "); ok {
			bridges = append(bridges, strings.Trim(name, `"`))
		} else if v, ok := strings.CutPrefix(line, "ovs_version: "); ok {
			version = strings.Trim(v, `"`)
		}
	}

	return version, bridges
}

// ovsSwitchdRuns reports whether the process in the pid file of ovs-vswitchd runs. In a container that does not see
// the process, it reports true, because doctor cannot tell.
func (in *doctorInput) ovsSwitchdRuns() bool {
	dir := defaultOVSRunDir
	if v, ok := in.env.lookup("OVS_RUNDIR"); ok && v != "" {
		dir = v
	}

	pid, err := in.env.readPid(filepath.Join(dir, "ovs-vswitchd.pid"))
	if err != nil {
		return false
	}

	return in.env.procRunning(pid) || containerHint(in.env) != ""
}

// openvswitchHint is what to do about each problem of an openvswitch row.
func openvswitchHint(c doctorCheck) string {
	return hostHints(c, [][2]string{
		{ovsMissingPhrase, "install Open vSwitch (the openvswitch-switch package on Debian and Ubuntu)"},
		{ovsDeniedPhrase, "run phenix doctor as root, as minimega runs"},
		{ovsNoAnswerPhrase, "start Open vSwitch (systemctl start openvswitch-switch on Debian and Ubuntu), or increase --timeout"},
		{ovsSwitchdPhrase, "restart Open vSwitch (systemctl restart openvswitch-switch on Debian and Ubuntu)"},
		{ovsModulePhrase, "load the module as root with modprobe openvswitch"},
	})
}

// imageBuildChecks checks what phenix image build needs past vmdb2: its programs, root, a loop device, and the
// debootstrap script and keyring of the release of each stored image config.
func imageBuildChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	if in.system.os != linuxOS {
		return []doctorCheck{hostSkip("phenix image build runs only on Linux")}
	}

	rows := []doctorCheck{imageBuildToolsCheck(in.env), imageBuildRootCheck(in), imageBuildLoopCheck(in.env),
		imageBuildKeyringsCheck(ctx, in)}

	for i, name := range []string{imageBuildToolsRow, imageBuildRootRow, imageBuildLoopRow, imageBuildKeyringsRow} {
		rows[i].Name = name
	}

	return rows
}

func imageBuildToolsCheck(env doctorEnv) doctorCheck {
	var missing, found []string

	for _, tool := range imageBuildTools {
		if _, err := env.lookPath(tool[0]); err != nil {
			missing = append(missing, fmt.Sprintf("%s (package %s)", tool[0], tool[1]))
		} else {
			found = append(found, tool[0])
		}
	}

	if len(missing) > 0 {
		return warnCheck("not on PATH: " + strings.Join(missing, ", ") + ", so phenix image build fails")
	}

	return passCheck(strings.Join(found, ", ") + " are on PATH")
}

// imageBuildRootCheck warns when this process does not have CAP_SYS_ADMIN, which vmdb2 needs for loop devices, mount
// and chroot. Without /proc/self/status, only root passes.
func imageBuildRootCheck(in *doctorInput) doctorCheck {
	capable, known := in.env.capSysAdmin()
	if !known {
		capable = in.uid == 0
	}

	switch {
	case capable:
		return passCheck(fmt.Sprintf("uid %d with CAP_SYS_ADMIN", in.uid))
	case in.uid != 0:
		return warnCheck(fmt.Sprintf("phenix runs as uid %d, not root, so vmdb2 cannot use loop devices, mount or chroot",
			in.uid))
	}

	return warnCheck("root does not have CAP_SYS_ADMIN here, such as in a container that is not privileged, " +
		"so vmdb2 cannot use loop devices or mount")
}

// capSysAdmin reports whether CapEff in /proc/self/status has CAP_SYS_ADMIN, and false for known when it cannot tell.
func (e doctorEnv) capSysAdmin() (bool, bool) {
	data, err := e.readFile("/proc/self/status")
	if err != nil {
		return false, false
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if hex, ok := strings.CutPrefix(line, "CapEff:"); ok {
			caps, err := strconv.ParseUint(strings.TrimSpace(hex), 16, 64)
			if err != nil {
				return false, false
			}

			return caps&(1<<capSysAdmin) != 0, true
		}
	}

	return false, false
}

// imageBuildLoopCheck warns when no loop device is free: each /dev/loopN has a /sys/block/loopN/loop directory, which
// the kernel makes while a file is attached, and there is no /dev/loop-control to make a new one.
func imageBuildLoopCheck(env doctorEnv) doctorCheck {
	var free, total int

	entries, _ := env.readDir("/dev")

	for _, entry := range entries {
		n, ok := strings.CutPrefix(entry.Name(), "loop")
		if _, err := strconv.Atoi(n); !ok || err != nil {
			continue
		}

		total++

		if _, err := env.stat(filepath.Join("/sys/block", entry.Name(), "loop")); errors.Is(err, fs.ErrNotExist) {
			free++
		}
	}

	_, controlErr := env.stat("/dev/loop-control")

	switch {
	case free > 0:
		return passCheck(fmt.Sprintf("%d of %d loop devices free", free, total))
	case controlErr == nil:
		return passCheck(fmt.Sprintf("none of %d loop devices free, and the kernel makes one through /dev/loop-control", total))
	case containerHint(env) != "":
		return warnCheck("no free /dev/loop* device and no /dev/loop-control in this container, " +
			"so vmdb2 cannot attach the image")
	}

	return warnCheck("no free /dev/loop* device and no /dev/loop-control, so vmdb2 cannot attach the image")
}

// imageBuildKeyringsCheck checks, for the release of each stored image config, the debootstrap script and the keyring
// it names. phenix gives vmdb2 no keyring, so debootstrap uses the keyring of its script.
func imageBuildKeyringsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	configs, missing, why := in.storedConfigs(ctx, "Image")

	switch {
	case why != "":
		return hostSkip(why)
	case missing:
		return passCheck("no image configs, because the store file does not exist")
	}

	releases := make(map[string][]string)

	for _, c := range configs {
		release, _ := c.Spec["release"].(string)
		// as phenix image build does
		if release == "kali" {
			release = "kali-rolling"
		}

		if release != "" {
			releases[release] = append(releases[release], c.Metadata.Name)
		}
	}

	if len(releases) == 0 {
		return passCheck("no image configs with a release in the store")
	}

	if _, err := in.env.stat(debootstrapScripts); err != nil {
		return hostSkip(debootstrapScripts + " does not exist (the image-build tools row checks debootstrap)")
	}

	var problems, good []string

	for _, release := range slices.Sorted(maps.Keys(releases)) {
		names := releases[release]
		slices.Sort(names)
		configs := shortList(names)

		if !validRelease.MatchString(release) {
			problems = append(problems, fmt.Sprintf("the release %q of %s is not a debootstrap script name", release, configs))

			continue
		}

		script, err := in.env.readFile(filepath.Join(debootstrapScripts, release))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s for %s (%s)", noScriptPhrase, release, configs))

			continue
		}

		if keyring := scriptKeyring(string(script)); keyring != "" {
			if _, err := in.env.stat(keyring); err != nil {
				problems = append(problems, fmt.Sprintf("%s for %s (%s) does not exist, so debootstrap %s",
					keyring, release, configs, noKeyringPhrase))

				continue
			}
		}

		good = append(good, release)
	}

	if len(problems) > 0 {
		return warnCheck(strings.Join(problems, ". "))
	}

	return passCheck("debootstrap scripts and keyrings found for " + strings.Join(good, ", "))
}

// scriptKeyring is the first keyring a debootstrap script names, or "".
func scriptKeyring(script string) string {
	for line := range strings.SplitSeq(script, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "keyring" && filepath.IsAbs(fields[1]) {
			return fields[1]
		}
	}

	return ""
}

// imageBuildHint is what to do about an image-build row that warns.
func imageBuildHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case c.Name == imageBuildToolsRow:
		return "install the packages that the row names"
	case c.Name == imageBuildRootRow:
		return "run phenix image build as root, or in a privileged container"
	case c.Name == imageBuildLoopRow:
		return "load the loop module as root (modprobe loop), or run the container privileged with /dev from the host"
	}

	return hostHints(c, [][2]string{
		{noScriptPhrase, "install a later debootstrap, or add a script for the release to " + debootstrapScripts +
			" (for Kali, see the phenix-image skill)"},
		{noKeyringPhrase, "install the package that holds the keyring: debian-archive-keyring, ubuntu-keyring or " +
			"kali-archive-keyring"},
		{"is not a debootstrap script name", "set the release of the image config to a debootstrap release name"},
	})
}
