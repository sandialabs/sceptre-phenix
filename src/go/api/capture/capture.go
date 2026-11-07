// Package capture starts, stops and lists experiment captures: one packet capture of an experiment's traffic on all
// its hosts, which minimega 3.4.0 or later merges into one file on the head node. A start is checked against the Open
// vSwitch mirror budget of each host and bridge that the experiment uses. Each function returns an error wrapping
// mm.ErrCaptureUnsupported when minimega does not support experiment captures.
package capture

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"phenix/api/experiment"
	"phenix/types"
	"phenix/util/mm"
)

const (
	// MirrorLimit is how many mirrors Open vSwitch applies on one bridge.
	MirrorLimit = 32
	// MaxExperimentCaptures is how many experiment captures one experiment may run at the same time.
	MaxExperimentCaptures = 32

	// more than halfBudget mirrors of one experiment on a bridge is more than half of MirrorLimit.
	halfBudget = 16
	// nearBudget mirrors on a bridge leave 4 or fewer.
	nearBudget = 28

	defaultSnaplen = 1600
	minSnaplen     = 14
	maxSnaplen     = 65535

	// maxStemLength bounds a capture file name without its extension.
	maxStemLength = 200

	// startedListTimeout bounds the read of the list after a start.
	startedListTimeout = 10 * time.Second

	// maxNameSuffix is the largest number that a default file name gets to differ from the files that exist.
	maxNameSuffix = 100
)

// The budget levels of a host and bridge.
const (
	LevelOK   = "ok"
	LevelHalf = "half"
	LevelNear = "near"
	LevelFull = "full"
)

var (
	// ErrInvalid is wrapped by the error of a start request that phenix does not send to minimega.
	ErrInvalid = errors.New("invalid experiment capture request")
	// ErrBudget is wrapped by the error of a start that the mirror budget refuses. Nothing was sent to minimega.
	ErrBudget = errors.New("the Open vSwitch mirror budget refuses the capture")
	// ErrNotFound is wrapped by the error for an experiment capture ID that minimega does not list.
	ErrNotFound = errors.New("experiment capture not found")
	// ErrNotRunning is wrapped by the error of a start or a budget read for an experiment that does not run.
	ErrNotRunning = errors.New("experiment is not running")
	// ErrPathInUse is wrapped by the error of a start whose file a running experiment capture already writes, or whose
	// given file name the head node already has. Nothing was sent to minimega.
	ErrPathInUse = errors.New("the capture file is in use")
)

// StartRequest is what a start asks for. Empty VLANs captures every VLAN, a zero Snaplen keeps 1600 bytes of each
// frame, an empty Format is pcapng and an empty Name gives the default file name.
type StartRequest struct {
	VLANs   []string
	Filter  string
	Snaplen int
	Name    string
	Format  string
}

// Started is a capture that a start made, and the budget warnings for the mirrors it uses.
type Started struct {
	Capture  mm.NamespaceCapture
	Warnings []string
}

// Budget is how many Open vSwitch mirrors there are on one host and bridge that an experiment uses: in total, and of
// the experiment's captures. Level is LevelOK, LevelHalf, LevelNear or LevelFull.
type Budget struct {
	Host, Bridge             string
	Total, Experiment, Limit int
	Level                    string
}

// fileNameUnsafe matches each character that a capture file name may not hold.
var fileNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// supported returns an error wrapping mm.ErrCaptureUnsupported when minimega does not support experiment captures.
func supported(ctx context.Context) error {
	if s := mm.QueryCaptureSupport(ctx); !s.Available {
		return fmt.Errorf("%w: %s", mm.ErrCaptureUnsupported, s.Reason)
	}

	return nil
}

// runningExperiment returns the experiment exp, and whether it runs.
func runningExperiment(exp string) (*types.Experiment, bool, error) {
	stored, err := experiment.Get(exp)
	if err != nil {
		return nil, false, fmt.Errorf("getting experiment %s: %w", exp, err)
	}

	return stored, stored.Running(), nil
}

// List returns the experiment captures of exp, sorted by ID. A stopped experiment has none, and minimega is not asked
// about it, which would make its namespace again.
func List(ctx context.Context, exp string) ([]mm.NamespaceCapture, error) {
	_, running, err := runningExperiment(exp)
	if err != nil {
		return nil, err
	}

	if err := supported(ctx); err != nil {
		return nil, err
	}

	if !running {
		return nil, nil
	}

	captures, err := mm.GetNamespaceCaptures(mm.NS(exp), mm.Context(ctx))
	if err != nil {
		return nil, fmt.Errorf("listing the experiment captures of %s: %w", exp, err)
	}

	return captures, nil
}

// Get returns the experiment capture id of exp, with its streams.
func Get(ctx context.Context, exp, id string) (mm.NamespaceCapture, error) {
	captures, err := List(ctx, exp)
	if err != nil {
		return mm.NamespaceCapture{}, err
	}

	return find(captures, exp, id)
}

// find returns the capture id of the list of exp's captures.
func find(captures []mm.NamespaceCapture, exp, id string) (mm.NamespaceCapture, error) {
	for _, c := range captures {
		if c.ID == id {
			return c, nil
		}
	}

	return mm.NamespaceCapture{}, fmt.Errorf("%w: %s in experiment %s", ErrNotFound, id, exp)
}

// Stop stops the experiment capture id of exp and returns it as minimega last gave it.
func Stop(ctx context.Context, exp, id string) (mm.NamespaceCapture, error) {
	listed, err := Get(ctx, exp, id)
	if err != nil {
		return listed, err
	}

	rows, err := mm.StopNamespaceCapture(mm.NS(exp), mm.CaptureID(id), mm.Context(ctx))
	if err != nil {
		return listed, fmt.Errorf("stopping experiment capture %s of %s: %w", id, exp, err)
	}

	if final, err := find(rows, exp, id); err == nil {
		return final, nil
	}

	return listed, nil
}

// File is the path of the capture's file relative to the files directory of the experiment exp, or "" when the file
// is not there.
func File(exp string, c mm.NamespaceCapture) string {
	file, _ := experiment.CaptureFile(exp, c.Path)

	return file
}

// Start starts an experiment capture of exp, into a file of at most maxSizeMiB MiB (0 for no limit) when minimega
// takes a size limit. It refuses with ErrBudget, sending nothing to minimega, when a host and bridge that the capture
// needs has every mirror Open vSwitch applies, or when exp already runs MaxExperimentCaptures captures. The starts of
// all experiments in the process run one at a time, because experiments share bridges and their mirror budget.
func Start(ctx context.Context, exp string, req StartRequest, maxSizeMiB int64) (Started, error) {
	stored, running, err := runningExperiment(exp)
	if err != nil {
		return Started{}, err
	}

	if err := supported(ctx); err != nil {
		return Started{}, err
	}

	valid, err := validate(stored, req, time.Now())
	if err != nil {
		return Started{}, err
	}

	if !running {
		return Started{}, fmt.Errorf("%w: %s", ErrNotRunning, exp)
	}

	unlock, err := lockStarts(ctx, exp)
	if err != nil {
		return Started{}, err
	}

	defer unlock()

	// the experiment can stop while the start waits, and a namespace call would make its namespace again
	if _, running, err = runningExperiment(exp); err != nil {
		return Started{}, err
	}

	if !running {
		return Started{}, fmt.Errorf("%w: %s", ErrNotRunning, exp)
	}

	captures, err := mm.GetNamespaceCaptures(mm.NS(exp), mm.Context(ctx))
	if err != nil {
		return Started{}, fmt.Errorf("listing the experiment captures of %s: %w", exp, err)
	}

	if n := runningCaptures(captures); n >= MaxExperimentCaptures {
		return Started{}, fmt.Errorf("%w: %s already runs %d experiment captures", ErrBudget, exp, n)
	}

	valid.file, err = freeFile(exp, valid, captures)
	if err != nil {
		return Started{}, err
	}

	var warnings []string

	needed, err := budgets(ctx, stored, valid.vlans)

	switch {
	case ctx.Err() != nil:
		return Started{}, fmt.Errorf("counting the Open vSwitch mirrors of %s: %w", exp, ctx.Err())
	case err != nil:
		warnings = append(warnings, "cannot count Open vSwitch mirrors: "+err.Error())
	default:
		if refusal := refused(needed); refusal != "" {
			return Started{}, fmt.Errorf("%w: %s", ErrBudget, refusal)
		}
	}

	id, err := mm.StartNamespaceCapture(
		mm.NS(exp),
		mm.CaptureFile(valid.file),
		mm.CaptureVLANs(valid.vlans),
		mm.CaptureFilter(valid.filter),
		mm.CaptureSnaplen(valid.snaplen),
		mm.CaptureFormat(valid.format),
		mm.CaptureMaxSize(maxSizeMiB),
		mm.Context(ctx),
	)
	if err != nil {
		if errors.Is(err, mm.ErrInvalidCaptureOption) || errors.Is(err, mm.ErrUnsafeArg) {
			err = fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		return Started{}, fmt.Errorf("starting an experiment capture of %s: %w", exp, err)
	}

	return Started{Capture: started(ctx, exp, id, valid), Warnings: append(warnings, budgetWarnings(exp, needed)...)}, nil
}

// started returns the capture id that a start of valid made, as minimega lists it now, or as the start asked for it
// when minimega does not list it. It reads the list for at most startedListTimeout, even when ctx ends, as the capture
// runs.
func started(ctx context.Context, exp, id string, valid request) mm.NamespaceCapture {
	list, cancel := context.WithTimeout(context.WithoutCancel(ctx), startedListTimeout)
	defer cancel()

	captures, err := mm.GetNamespaceCaptures(mm.NS(exp), mm.Context(list))
	if err == nil {
		if c, err := find(captures, exp, id); err == nil {
			return c
		}
	}

	return mm.NamespaceCapture{ //nolint:exhaustruct // no counters yet
		ID:     id,
		State:  "starting",
		Path:   mm.GetMMFullPath(valid.file),
		Format: valid.format,
		Filter: valid.filter,
		VLANs:  valid.vlans,
	}
}

func runningCaptures(captures []mm.NamespaceCapture) int {
	var n int

	for _, c := range captures {
		if c.Running() {
			n++
		}
	}

	return n
}

// startLock is a lock of one slot for all experiment capture starts in the process. Experiments share bridges, and
// so the mirror budget of each host and bridge.
var startLock = make(chan struct{}, 1) //nolint:gochecknoglobals // one lock for the process

// lockStarts waits until no other experiment capture start runs, of exp or of another experiment, or until ctx ends,
// and returns what lets the next start go.
func lockStarts(ctx context.Context, exp string) (func(), error) {
	select {
	case startLock <- struct{}{}:
		return func() { <-startLock }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for another capture start of %s: %w", exp, ctx.Err())
	}
}

// request is a StartRequest as validate accepts it, with every default filled in.
type request struct {
	vlans     []string // the experiment's aliases
	filter    string
	snaplen   int
	format    string
	file      string // relative to minimega's files directory
	defaulted bool   // the file has the default name
}

// writes reports whether one of the running captures of exp writes file, relative to minimega's files directory.
func writes(exp string, captures []mm.NamespaceCapture, file string) bool {
	for _, c := range captures {
		if c.Running() && exp+"/files/"+File(exp, c) == file {
			return true
		}
	}

	return false
}

// freeFile returns the file of the start valid of a capture of exp, which captures run. A given name that a running
// capture writes, or that the experiment's files directory on the headnode has (a symbolic link too), is refused with
// ErrPathInUse. A default name that either has gets the first free suffix "-2", "-3" and so on.
func freeFile(exp string, valid request, captures []mm.NamespaceCapture) (string, error) {
	if !valid.defaulted {
		switch {
		case writes(exp, captures, valid.file):
			return "", fmt.Errorf("%w: a running experiment capture writes %s", ErrPathInUse, valid.file)
		case experiment.LocalFileExists(exp, strings.TrimPrefix(valid.file, exp+"/files/")):
			return "", fmt.Errorf("%w: the file %s exists", ErrPathInUse, valid.file)
		}

		return valid.file, nil
	}

	var (
		ext  = "." + valid.format
		stem = strings.TrimSuffix(valid.file, ext)
	)

	for n := 1; n <= maxNameSuffix; n++ {
		file := valid.file
		if n > 1 {
			file = stem + "-" + strconv.Itoa(n) + ext
		}

		if !writes(exp, captures, file) && !experiment.LocalFileExists(exp, strings.TrimPrefix(file, exp+"/files/")) {
			return file, nil
		}
	}

	return "", fmt.Errorf("%w: %s and the names with the suffixes -2 to -%d", ErrPathInUse, valid.file, maxNameSuffix)
}

// validate checks req for a start of a capture of exp at now, and returns it with its defaults. Its error wraps
// ErrInvalid.
func validate(exp *types.Experiment, req StartRequest, now time.Time) (request, error) {
	valid := request{
		vlans: nil, filter: req.Filter, snaplen: req.Snaplen, format: req.Format, file: "", defaulted: req.Name == "",
	}

	switch valid.format {
	case "":
		valid.format = mm.CaptureFormatPcapng
	case mm.CaptureFormatPcapng, mm.CaptureFormatPcap:
	default:
		return request{}, fmt.Errorf("%w: format %q is not pcapng or pcap", ErrInvalid, req.Format)
	}

	switch {
	case valid.snaplen == 0:
		valid.snaplen = defaultSnaplen
	case valid.snaplen < minSnaplen || valid.snaplen > maxSnaplen:
		return request{}, fmt.Errorf("%w: snaplen %d is not from %d to %d", ErrInvalid, req.Snaplen, minSnaplen, maxSnaplen)
	}

	if err := mm.ValidateCaptureFilter(req.Filter); err != nil {
		return request{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	aliases := vlanAliases(exp)

	for _, vlan := range req.VLANs {
		i := slices.IndexFunc(aliases, func(alias string) bool { return strings.EqualFold(alias, vlan) })
		if i < 0 {
			return request{}, fmt.Errorf("%w: %q is not a VLAN of experiment %s", ErrInvalid, vlan, exp.Metadata.Name)
		}

		if !slices.Contains(valid.vlans, aliases[i]) {
			valid.vlans = append(valid.vlans, aliases[i])
		}
	}

	name := req.Name
	if name == "" {
		name = defaultName(exp.Metadata.Name, valid.vlans, now)
	}

	valid.file = exp.Metadata.Name + "/files/" + fileName(name, valid.format)

	if err := mm.ValidateArg(valid.file); err != nil {
		return request{}, fmt.Errorf("%w: file name: %w", ErrInvalid, err)
	}

	return valid, nil
}

// defaultName is the file name stem of a capture of exp's vlans that starts at now: <exp>[-<vlan>]-<YYYYmmdd-HHMMSS>,
// with the VLAN only when there is exactly one.
func defaultName(exp string, vlans []string, now time.Time) string {
	parts := []string{exp}
	if len(vlans) == 1 {
		parts = append(parts, vlans[0])
	}

	return strings.Join(append(parts, now.Format("20060102-150405")), "-")
}

// fileName is name as a capture file name in format: each character but letters, digits, '.', '_' and '-' becomes
// '_', leading dots go, a .pcap or .pcapng extension is replaced by the format's, and the stem is at most 200 bytes.
// A name with nothing left is "capture".
func fileName(name, format string) string {
	stem := fileNameUnsafe.ReplaceAllString(name, "_")

	for _, ext := range []string{"." + mm.CaptureFormatPcapng, "." + mm.CaptureFormatPcap} {
		if len(stem) >= len(ext) && strings.EqualFold(stem[len(stem)-len(ext):], ext) {
			stem = stem[:len(stem)-len(ext)]

			break
		}
	}

	stem = strings.TrimLeft(stem, ".")
	stem = stem[:min(len(stem), maxStemLength)]

	if stem == "" {
		stem = "capture"
	}

	return stem + "." + format
}

// vlanAliases are the VLAN aliases of exp: those it was given when it started, those of its spec and those its
// topology's interfaces name, sorted.
func vlanAliases(exp *types.Experiment) []string {
	var aliases []string

	add := func(alias string) {
		if alias != "" && !slices.Contains(aliases, alias) {
			aliases = append(aliases, alias)
		}
	}

	if exp.Status != nil {
		for alias := range exp.Status.VLANs() {
			add(alias)
		}
	}

	if exp.Spec == nil {
		return aliases
	}

	if vlans := exp.Spec.VLANs(); vlans != nil {
		for alias := range vlans.Aliases() {
			add(alias)
		}
	}

	if topology := exp.Spec.Topology(); topology != nil {
		for _, node := range topology.Nodes() {
			for _, iface := range node.Network().Interfaces() {
				add(iface.VLAN())
			}
		}
	}

	slices.Sort(aliases)

	return aliases
}
