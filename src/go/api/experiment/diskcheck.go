package experiment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"phenix/app"
	ifaces "phenix/types/interfaces"
	"phenix/util/common"
	"phenix/util/diskchain"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/plog"
)

const (
	// diskCheckTimeout is how long a disk check may read images before the start goes on without the rest.
	diskCheckTimeout = 20 * time.Second

	// maxListedProblems is how many problems an error lists before it counts the rest.
	maxListedProblems = 20

	// maxListedVMs is how many VMs a warning about a shared image names before it counts the rest.
	maxListedVMs = 5

	driveDisk  = "disk"
	driveCDROM = "cdrom"

	// cdromSetting is the advanced (vm config) setting naming a VM's CD-ROM image.
	cdromSetting = "cdrom"
)

// ErrBrokenDisks is matched by the error CheckDisks and Start return when a VM's disk image or CD-ROM image is
// missing, its backing chain is broken, or qemu cannot open an image of the chain (see BrokenDisksError).
var ErrBrokenDisks = errors.New("broken VM disk images")

// DiskProblem is a disk image or CD-ROM image of a VM that qemu cannot open.
type DiskProblem struct {
	// VM is the VM's hostname.
	VM string `json:"vm"`
	// Drive is "disk" for a hardware.drives entry and "cdrom" for the VM's CD-ROM.
	Drive string `json:"drive"`
	// Image is the image as the topology names it.
	Image string `json:"image"`
	// Path is the image's full path.
	Path string `json:"path"`
	// Missing is the full path of the missing file, the image or one in its chain, or "" for a chain that loops or is
	// too deep, or an image that qemu cannot open.
	Missing string `json:"missing"`
	// Problem is what is wrong, in one sentence.
	Problem string `json:"problem"`

	index int // the drive's position in hardware.drives; the CD-ROM comes after the drives
}

// String is the problem as one item of a BrokenDisksError.
func (p DiskProblem) String() string {
	return fmt.Sprintf("VM %s: %s %s: %s", p.VM, driveLabel(p.Drive), p.Image, p.Problem)
}

// BrokenDisksError lists the VM disk images and CD-ROM images that qemu cannot open, sorted by VM and drive. It
// matches ErrBrokenDisks.
type BrokenDisksError struct {
	Problems []DiskProblem
}

// Error gives a header counting the disks and VMs, then one line per problem, up to maxListedProblems, in the list
// format of go-multierror.
func (e *BrokenDisksError) Error() string {
	vms := make(map[string]struct{})
	items := make([]string, 0, len(e.Problems))

	for _, p := range e.Problems {
		vms[p.VM] = struct{}{}
		items = append(items, p.String())
	}

	return fmt.Sprintf(
		"qemu cannot open %s on %s: %s", countOf(len(e.Problems), "disk"), countOf(len(vms), "VM"), errorList(items),
	)
}

// Is reports whether target is ErrBrokenDisks.
func (e *BrokenDisksError) Is(target error) bool {
	return target == ErrBrokenDisks
}

// errorList lists items as go-multierror does, with the real count, showing at most maxListedProblems of them.
func errorList(items []string) string {
	shown, more := items, ""
	if len(items) > maxListedProblems {
		shown, more = items[:maxListedProblems], fmt.Sprintf("\n\t* and %d more", len(items)-maxListedProblems)
	}

	word := "errors"
	if len(items) == 1 {
		word = "error"
	}

	return fmt.Sprintf("%d %s occurred:\n\t* %s%s", len(items), word, strings.Join(shown, "\n\t* "), more)
}

// CheckOption changes what CheckDisks does.
type CheckOption func(*checkOptions)

type checkOptions struct {
	mode       common.DiskCheck
	asWarnings bool
	vms        map[string]bool
	booting    map[string]bool
}

// CheckWithMode checks as the experiment.disk-check mode m says; the default is common.CurrentDiskCheck.
func CheckWithMode(m common.DiskCheck) CheckOption {
	return func(o *checkOptions) { o.mode = m }
}

// CheckAsWarnings reports every problem as a warning when w is true, as a dry run does.
func CheckAsWarnings(w bool) CheckOption {
	return func(o *checkOptions) { o.asWarnings = o.asWarnings || w }
}

// CheckVMs checks only the VMs with the given hostnames.
func CheckVMs(names ...string) CheckOption {
	return func(o *checkOptions) {
		o.vms = make(map[string]bool, len(names))
		for _, name := range names {
			o.vms[name] = true
		}
	}
}

// checkBooting checks the VMs with the given hostnames even when the spec marks them do not boot, such as VMs that
// a pre-start app marked so after it found their disk image missing.
func checkBooting(names map[string]bool) CheckOption {
	return func(o *checkOptions) { o.booting = names }
}

// bootingVMs returns the hostnames of spec's nodes that are not marked do not boot.
func bootingVMs(spec ifaces.ExperimentSpec) map[string]bool {
	names := make(map[string]bool)

	if spec == nil || spec.Topology() == nil {
		return names
	}

	for _, node := range spec.Topology().Nodes() {
		if node != nil && node.General() != nil && !derefBool(node.General().DoNotBoot(), false) {
			names[node.General().Hostname()] = true
		}
	}

	return names
}

// CheckDisks checks that each disk and CD-ROM image of a booting VM in spec exists, that each disk image's backing
// chain reaches its base, and that qemu can open each image of the chain. It reads the files on the head node for at
// most 20 seconds. It returns a *BrokenDisksError for images qemu cannot open, unless the mode is warn or
// CheckAsWarnings is set. Other problems, such as images it cannot read or did not check in time, a qcow version 1
// image or an image qemu did not close cleanly, are warnings. With the mode off it checks nothing. When ctx is done
// before the check ends, it returns ctx's error.
func CheckDisks(ctx context.Context, spec ifaces.ExperimentSpec, opts ...CheckOption) ([]error, error) {
	o := checkOptions{mode: common.CurrentDiskCheck(), asWarnings: false, vms: nil, booting: nil}
	for _, opt := range opts {
		opt(&o)
	}

	if o.mode == common.DiskCheckOff || spec == nil || spec.Topology() == nil {
		return nil, nil
	}

	warnings, problems := checkDisks(ctx, spec, diskScope{vms: o.vms, booting: o.booting}, mmCheckEnv())

	// only the check's own time limit starts anyway: a caller that gave up must not stop or start an experiment
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checking disk images: %w", err)
	}

	return o.outcome(warnings, problems)
}

// outcome is the result of a check that found warnings and problems: a *BrokenDisksError for the problems, or with
// the warn mode or asWarnings, more warnings.
func (o checkOptions) outcome(warnings []error, problems []DiskProblem) ([]error, error) {
	if len(problems) == 0 {
		return warnings, nil
	}

	if o.mode == common.DiskCheckWarn || o.asWarnings {
		for _, p := range problems {
			warnings = append(warnings, errors.New(p.String()))
		}

		return warnings, nil
	}

	return warnings, &BrokenDisksError{Problems: problems}
}

// CheckDisksBeforeStop is CheckDisks for a restart, run on the spec the start will use before the running experiment
// stops, so a restart that would fail leaves it running. Apps can change a VM's disks before the start checks them
// again, so when the scenario has a user app every problem is a warning.
func CheckDisksBeforeStop(ctx context.Context, spec ifaces.ExperimentSpec, opts ...CheckOption) ([]error, error) {
	return CheckDisks(ctx, spec, append(opts, CheckAsWarnings(hasUserApps(spec)))...)
}

// hasUserApps reports whether spec's scenario runs an app other than the default apps, which leave drives alone.
func hasUserApps(spec ifaces.ExperimentSpec) bool {
	if spec == nil || spec.Scenario() == nil {
		return false
	}

	defaults := app.DefaultApps()

	for _, a := range spec.Scenario().Apps() {
		if !a.Disabled() && !slices.Contains(defaults, a.Name()) {
			return true
		}
	}

	return false
}

// reportDiskWarnings records the warnings on ctx and logs each one, so a web start, whose notes nobody prints,
// reports them too.
func reportDiskWarnings(ctx context.Context, exp string, warnings []error) {
	if len(warnings) == 0 {
		return
	}

	if notes.Flushes(ctx) {
		notes.AddWarnings(ctx, true, warnings...)

		return
	}

	notes.AddWarnings(ctx, false, warnings...)

	for _, w := range warnings {
		plog.Warn(plog.TypeSystem, w.Error(), "exp", exp)
	}
}

// checkEnv is what a disk check reads from minimega: its files directory and whether phenix can see it, which hosts
// the VMs can run on, and how long the check may take.
type checkEnv struct {
	filesDir     string
	filesDirSeen bool
	// isHeadnode reports whether a cluster host is the head node, or why phenix cannot tell.
	isHeadnode func(host string) (bool, error)
	hosts      func(ctx context.Context) (mm.Hosts, error)
	timeout    time.Duration
}

// mmCheckEnv reads the check's environment from minimega.
func mmCheckEnv() checkEnv {
	dir := mm.GetMMFullPath("")

	seen := false

	select {
	case <-mm.FilesDirectoryGiven():
		info, err := os.Stat(dir)
		seen = err == nil && info.IsDir()
	default:
	}

	return checkEnv{
		filesDir:     dir,
		filesDirSeen: seen,
		isHeadnode: func(host string) (bool, error) {
			if mm.Headnode() == "" {
				return false, errors.New("minimega did not name its host")
			}

			return mm.IsHeadnode(host), nil
		},
		hosts: func(ctx context.Context) (mm.Hosts, error) {
			hosts, _, err := mm.GetClusterHostsObserved(ctx, false)

			return hosts, err //nolint:wrapcheck // the caller says what it was for
		},
		timeout: diskCheckTimeout,
	}
}

// diskUse is one VM drive or CD-ROM using an image.
type diskUse struct {
	vm, drive, image string
	index            int
	snapshot         bool // the VM's first drive, which minimega snapshots on the head node
	readOnly         bool // qemu opens the image read-only: a CD-ROM, or a drive of a VM with snapshot set
}

// label names the use as an item of an error or warning.
func (u diskUse) label() string {
	return fmt.Sprintf("VM %s: %s %s", u.vm, driveLabel(u.drive), u.image)
}

func driveLabel(drive string) string {
	if drive == driveCDROM {
		return "CD-ROM"
	}

	return driveDisk
}

// diskScope is which VMs a check reads: those in vms (all when nil) that boot, or that are in booting.
type diskScope struct {
	vms, booting map[string]bool
}

// checkDisks checks the images of spec's VMs in scope in env, returning the warnings and the problems that refuse the
// start, sorted by VM and drive.
func checkDisks(ctx context.Context, spec ifaces.ExperimentSpec, scope diskScope, env checkEnv) ([]error, []DiskProblem) {
	uses, walkOnly := collectDisks(spec, scope, env.filesDir)
	if len(uses) == 0 {
		return nil, nil
	}

	paths := make([]string, 0, len(uses))
	for path := range uses {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	results := walkImages(ctx, paths, walkOnly, env.timeout)

	var (
		warnings  []error
		problems  []DiskProblem
		unchecked []string
		where     = newPlacement(spec, env)
	)

	for _, path := range paths {
		res, ok := results[path]
		if !ok {
			unchecked = append(unchecked, path)

			continue
		}

		w, p := checkResult(ctx, env, where, uses[path], path, res)
		warnings, problems = append(warnings, w...), append(problems, p...)
	}

	if len(unchecked) > 0 {
		warnings = append(warnings, fmt.Errorf(
			"did not finish checking %s within %s, so the experiment starts without checking %s",
			countOf(len(unchecked), "disk image"), env.timeout, listOf(unchecked, maxListedProblems),
		))
	}

	slices.SortFunc(problems, func(a, b DiskProblem) int {
		if c := strings.Compare(a.VM, b.VM); c != 0 {
			return c
		}

		return a.index - b.index
	})

	return warnings, problems
}

// checkResult returns the warnings and the problems refusing the start that res, the walk of the image at path, gives
// for the uses of the image.
func checkResult(
	ctx context.Context, env checkEnv, where *placement, uses []diskUse, path string, res diskchain.Result,
) ([]error, []DiskProblem) {
	var (
		warnings []error
		problems []DiskProblem
		refused  = res.Broken() || res.Refused()
	)

	add := func(use diskUse, sentence, missing string) {
		problem, warning := classify(ctx, env, where, use, path, sentence, missing)
		if warning != nil {
			warnings = append(warnings, warning)
		} else {
			problems = append(problems, problem)
		}
	}

	switch {
	case refused:
		sentence, missing := res.Sentence(), ""
		if res.Problem == diskchain.Missing {
			missing = res.At
		}

		for _, use := range uses {
			if use.drive == driveCDROM && res.Problem == diskchain.Missing {
				add(use, "CD-ROM image "+res.At+" is missing", missing)
			} else {
				add(use, sentence, missing)
			}
		}
	case res.Problem != diskchain.Whole:
		warnings = append(warnings, sharedWarning(uses, res.Sentence()))
	}

	for _, note := range res.Notes {
		switch {
		case note.Kind == diskchain.Corrupt:
			// qemu refuses a corrupt image only when it opens the image read/write
			var readOnly []diskUse

			for _, use := range uses {
				if refused || use.readOnly || len(res.Members) == 0 || note.Path != res.Members[0] {
					readOnly = append(readOnly, use)
				} else {
					add(use, note.Sentence(), "")
				}
			}

			if len(readOnly) > 0 {
				warnings = append(warnings, sharedWarning(readOnly, note.Sentence()+". Copy it, then run qemu-img check -r all on it"))
			}
		case note.AtStart():
			warnings = append(warnings, sharedWarning(uses, note.Sentence()))
		}
	}

	return warnings, problems
}

// collectDisks maps the full path of each image the VMs in scope boot from to its uses, in VM order, with dir as the
// minimega files directory, and sets for each path whether it is only opened: a CD-ROM or ISO image, which qemu reads
// as raw.
func collectDisks(spec ifaces.ExperimentSpec, scope diskScope, dir string) (map[string][]diskUse, map[string]bool) {
	uses := make(map[string][]diskUse)
	openOnly := make(map[string]bool)

	add := func(use diskUse, cdrom bool) {
		path := fullPath(dir, use.image)

		if _, ok := openOnly[path]; !ok {
			openOnly[path] = true
		}

		openOnly[path] = openOnly[path] && cdrom
		uses[path] = append(uses[path], use)
	}

	for _, node := range spec.Topology().Nodes() {
		if node == nil || node.External() || node.General() == nil {
			continue
		}

		hostname := node.General().Hostname()
		if scope.vms != nil && !scope.vms[hostname] {
			continue
		}

		if derefBool(node.General().DoNotBoot(), false) && !scope.booting[hostname] {
			continue
		}

		snapshot := derefBool(node.General().Snapshot(), true)

		var drives []ifaces.NodeDrive
		if node.Hardware() != nil {
			drives = node.Hardware().Drives()
		}

		for i, d := range drives {
			if d == nil || d.Image() == "" {
				continue
			}

			iso := strings.EqualFold(filepath.Ext(d.Image()), ".iso")

			add(diskUse{
				vm: hostname, drive: driveDisk, image: d.Image(), index: i, snapshot: i == 0 && snapshot, readOnly: snapshot,
			}, iso)
		}

		if image := node.Advanced()[cdromSetting]; image != "" {
			add(diskUse{
				vm: hostname, drive: driveCDROM, image: image, index: len(drives), snapshot: false, readOnly: true,
			}, true)
		}
	}

	return uses, openOnly
}

// fullPath is image as minimega reads it: cleaned when absolute, else joined onto the files directory dir.
func fullPath(dir, image string) string {
	if filepath.IsAbs(image) {
		return filepath.Clean(image)
	}

	return filepath.Join(dir, image)
}

func derefBool(b *bool, unset bool) bool {
	if b == nil {
		return unset
	}

	return *b
}

// walkImages reads paths, each only opened when openOnly says so, until timeout (see diskchain.WalkAll), and cleans the
// paths of each result. A path missing from the result was not read in time.
func walkImages(ctx context.Context, paths []string, openOnly map[string]bool, timeout time.Duration) map[string]diskchain.Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results := diskchain.WalkAll(ctx, paths, openOnly)
	for path, res := range results {
		results[path] = cleanResult(res)
	}

	return results
}

// cleanResult names the files of res as the kernel opens them, without "." and ".." segments (see
// mm.CleanBackingPath).
func cleanResult(res diskchain.Result) diskchain.Result {
	return res.MapPaths(mm.CleanBackingPath)
}

// classify returns use of the image at path, which qemu cannot open for the reason sentence gives, as a problem
// refusing the start, or as a warning when the VM may run where phenix did not look. missing is the missing file, or
// "" for another reason.
func classify(
	ctx context.Context, env checkEnv, where *placement, use diskUse, path, sentence, missing string,
) (DiskProblem, error) {
	problem := DiskProblem{
		VM: use.vm, Drive: use.drive, Image: use.image, Path: path, Missing: missing, Problem: sentence, index: use.index,
	}

	warn := func(why string) (DiskProblem, error) {
		return DiskProblem{}, fmt.Errorf("%s: %s. %s", use.label(), sentence, why)
	}

	switch {
	case missing != "" && !env.filesDirSeen:
		return warn(fmt.Sprintf("phenix cannot see minimega's files directory %s, so it did not check the image", env.filesDir))
	case use.snapshot, file.WithinDir(env.filesDir, path):
		return problem, nil
	}

	onHead, err := where.onHeadnode(ctx, use.vm)

	switch {
	case err != nil:
		return warn("phenix could not tell which node runs the VM: " + err.Error())
	case onHead:
		return problem, nil
	default:
		return warn("phenix checks only the head node, and the VM may run on another node")
	}
}

// sharedWarning reports sentence, a problem that does not refuse the start, of an image the uses share.
func sharedWarning(uses []diskUse, sentence string) error {
	names := make([]string, 0, len(uses))
	for _, use := range uses {
		if !slices.Contains(names, use.vm) {
			names = append(names, use.vm)
		}
	}

	word := "VM"
	if len(names) > 1 {
		word = "VMs"
	}

	return fmt.Errorf(
		"%s %s of %s %s: %s",
		driveLabel(uses[0].drive), uses[0].image, word, listOf(names, maxListedVMs), sentence,
	)
}

// placement tells whether a VM runs on the head node, asking minimega for the cluster's hosts at most once.
type placement struct {
	env       checkEnv
	deploy    common.DeploymentMode
	schedules map[string]string

	once  sync.Once
	hosts mm.Hosts
	err   error
}

func newPlacement(spec ifaces.ExperimentSpec, env checkEnv) *placement {
	return &placement{
		env: env, deploy: common.DeploymentMode(spec.DeployMode()), schedules: spec.Schedules(),
		once: sync.Once{}, hosts: nil, err: nil,
	}
}

// onHeadnode reports whether VM vm runs on the head node: it is scheduled there, the experiment deploys only to the
// head node, or the cluster is the head node alone.
func (p *placement) onHeadnode(ctx context.Context, vm string) (bool, error) {
	if host := p.schedules[vm]; host != "" {
		return p.env.isHeadnode(host)
	}

	if p.deploy == common.DeployModeOnlyHeadnode {
		return true, nil
	}

	p.once.Do(func() { p.hosts, p.err = p.env.hosts(ctx) })

	if p.err != nil {
		return false, p.err
	}

	for _, host := range p.hosts {
		if !host.Headnode {
			return false, nil
		}
	}

	return len(p.hosts) > 0, nil
}

// countOf is n with word, plural unless n is 1.
func countOf(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}

	return fmt.Sprintf("%d %ss", n, word)
}

// listOf joins items with commas, naming at most limit of them and counting the rest.
func listOf(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}

	return fmt.Sprintf("%s and %d more", strings.Join(items[:limit], ", "), len(items)-limit)
}
