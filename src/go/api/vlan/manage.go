package vlan

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
)

var (
	// ErrInvalid is returned for a VLAN ID, range or alias that phenix does not accept.
	ErrInvalid = errors.New("invalid VLAN change")
	// ErrNotFound is returned for an experiment, namespace or blacklist entry that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrRunning is returned for a spec change to a running experiment.
	ErrRunning = errors.New("experiment is running")
	// ErrNotRunning is returned by PinLive for a stopped experiment.
	ErrNotRunning = errors.New("experiment is not running")
	// ErrInUse is returned when VMs or an experiment still use VLAN IDs, or minimega refuses a change.
	ErrInUse = errors.New("VLAN IDs in use")
	// ErrMinimegaDown is returned by PinAlias, unless forced, when minimega does not answer.
	ErrMinimegaDown = errors.New("minimega did not answer, so phenix cannot check its VLAN IDs and blacklist")
	// ErrUnsupported is returned by RemoveFromBlacklist when minimega cannot remove a blacklisted VLAN.
	ErrUnsupported = mm.ErrBlacklistRemovalUnsupported
)

// ConflictError is the refusal of a spec change whose issues the caller did
// not force.
type ConflictError struct {
	Issues []Issue
}

func (e *ConflictError) Error() string {
	if len(e.Issues) == 0 {
		return "VLAN conflict"
	}

	return e.Issues[0].Message
}

// stoppedExperiment returns the experiment, refusing a missing or a running one.
func stoppedExperiment(name string) (*types.Experiment, error) {
	exp, err := getExperiment(name)
	if err != nil {
		return nil, err
	}

	if exp.Running() {
		return nil, fmt.Errorf("%w: stop experiment %s first", ErrRunning, name)
	}

	return exp, nil
}

func getExperiment(name string) (*types.Experiment, error) {
	exp, err := experiment.Get(name)
	if errors.Is(err, store.ErrNotExist) {
		return nil, fmt.Errorf("%w: experiment %s", ErrNotFound, name)
	}

	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", name, err)
	}

	return exp, nil
}

func saveSpec(exp *types.Experiment) error {
	if err := experiment.Save(
		experiment.SaveWithName(exp.Metadata.Name),
		experiment.SaveWithSpec(exp.Spec),
	); err != nil {
		return fmt.Errorf("saving updated spec for experiment %s: %w", exp.Metadata.Name, err)
	}

	return nil
}

// ValidID reports whether id is a VLAN ID that phenix and minimega accept.
func ValidID(id int) bool { return id >= MinID && id <= MaxID }

// conflicts returns the issues of kinds that the proposal adds, as view sees
// them, keeping those that keep returns true for. With live set, it refuses
// with ErrMinimegaDown when minimega does not answer.
func conflicts(ctx context.Context, view View, p Proposal, live bool, keep func(Issue) bool) ([]Issue, error) {
	in, _, err := Read(ctx)
	if err != nil {
		return nil, err
	}

	if live && !in.Minimega.OK {
		return nil, fmt.Errorf("%w (%s); force the pin to save it anyway", ErrMinimegaDown, in.Minimega.Err)
	}

	var found []Issue

	for _, i := range Check(in, view, p) {
		if keep(i) {
			found = append(found, i)
		}
	}

	return found, nil
}

// PinAlias pins the alias of a stopped experiment to the VLAN ID. Unless
// force is set, it refuses with a *ConflictError when another owner pins or
// uses the ID, or minimega blacklists it, and with ErrMinimegaDown when
// minimega does not answer.
func PinAlias(ctx context.Context, view View, name, alias string, id int, force bool) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: VLAN ID %d is not in %d–%d", ErrInvalid, id, MinID, MaxID)
	}

	exp, err := stoppedExperiment(name)
	if err != nil {
		return err
	}

	spec := exp.Spec.VLANs()
	aliases := maps.Clone(spec.Aliases())

	if _, ok := aliases[alias]; !ok {
		return fmt.Errorf("%w: experiment %s has no VLAN alias %s", ErrAliasNotFound, name, alias)
	}

	e := FromExperiment(*exp)
	if lo, hi, ok := bounds(e); ok && (id < lo || id > hi) {
		return fmt.Errorf("%w: VLAN ID %d is outside the range %d–%d of experiment %s", ErrInvalid, id, lo, hi, name)
	}

	aliases[alias] = id

	if !force {
		found, err := conflicts(ctx, view, Proposal{Experiment: name, Aliases: aliases, Min: e.Min, Max: e.Max},
			true, func(i Issue) bool {
				return (i.Kind == KindDuplicatePin || i.Kind == KindPinBlacklisted) &&
					i.Alias == alias && i.VLAN != nil && *i.VLAN == id
			})
		if err != nil {
			return err
		}

		if len(found) > 0 {
			return &ConflictError{Issues: found}
		}
	}

	spec.SetAliases(aliases)

	return saveSpec(exp)
}

// UnpinAlias clears the pin of the alias of a stopped experiment, so minimega
// chooses its ID at start.
func UnpinAlias(name, alias string) error {
	exp, err := unpinTarget(name, alias)
	if err != nil {
		return err
	}

	spec := exp.Spec.VLANs()
	aliases := maps.Clone(spec.Aliases())
	aliases[alias] = 0
	spec.SetAliases(aliases)

	return saveSpec(exp)
}

// CheckUnpinAlias checks an unpin as UnpinAlias does, and changes nothing.
func CheckUnpinAlias(name, alias string) error {
	_, err := unpinTarget(name, alias)

	return err
}

// unpinTarget returns the stopped experiment name, which must have the alias.
func unpinTarget(name, alias string) (*types.Experiment, error) {
	exp, err := stoppedExperiment(name)
	if err != nil {
		return nil, err
	}

	if _, ok := exp.Spec.VLANs().Aliases()[alias]; !ok {
		return nil, fmt.Errorf("%w: experiment %s has no VLAN alias %s", ErrAliasNotFound, name, alias)
	}

	return exp, nil
}

// ChangeRange sets the VLAN range of a stopped experiment. It refuses a range
// that leaves out a pin, and, unless force is set, one that overlaps another
// experiment's range, with a *ConflictError.
func ChangeRange(ctx context.Context, view View, name string, lo, hi int, force bool) error {
	if !ValidID(lo) || !ValidID(hi) || lo > hi {
		return fmt.Errorf("%w: the range %d–%d is not within %d–%d, lowest first", ErrInvalid, lo, hi, MinID, MaxID)
	}

	exp, err := stoppedExperiment(name)
	if err != nil {
		return err
	}

	spec := exp.Spec.VLANs()

	for _, alias := range slices.Sorted(maps.Keys(spec.Aliases())) {
		if id := spec.Aliases()[alias]; id != 0 && (id < lo || id > hi) {
			return fmt.Errorf("%w: VLAN alias %s pins %d, outside the range %d–%d", ErrInvalid, alias, id, lo, hi)
		}
	}

	if !force {
		found, err := conflicts(ctx, view, Proposal{Experiment: name, Aliases: spec.Aliases(), Min: lo, Max: hi},
			false, func(i Issue) bool { return i.Kind == KindRangeOverlap })
		if err != nil {
			return err
		}

		if len(found) > 0 {
			return &ConflictError{Issues: found}
		}
	}

	spec.SetMin(lo)
	spec.SetMax(hi)

	return saveSpec(exp)
}

// CheckClearRange checks a clear as ClearRange does, and changes nothing.
func CheckClearRange(name string) error {
	_, err := stoppedExperiment(name)

	return err
}

// ClearRange clears the VLAN range of a stopped experiment.
func ClearRange(name string) error {
	exp, err := stoppedExperiment(name)
	if err != nil {
		return err
	}

	exp.Spec.VLANs().SetMin(0)
	exp.Spec.VLANs().SetMax(0)

	return saveSpec(exp)
}

// PinLive copies minimega's VLAN IDs of a running experiment into its spec,
// so the next start gives the same IDs, and returns the IDs it pinned.
func PinLive(ctx context.Context, name string) (map[string]int, error) {
	exp, err := getExperiment(name)
	if err != nil {
		return nil, err
	}

	if !exp.Running() {
		return nil, fmt.Errorf("%w: start experiment %s first", ErrNotRunning, name)
	}

	live, err := mm.GetVLANs(mm.NS(exp.Spec.ExperimentName()), mm.Context(ctx))
	if err != nil {
		return nil, fmt.Errorf("reading the VLANs of experiment %s from minimega: %w", name, err)
	}

	spec := exp.Spec.VLANs()
	e := FromExperiment(*exp)
	aliases := maps.Clone(spec.Aliases())

	for _, alias := range slices.Sorted(maps.Keys(live)) {
		id := live[alias]
		if lo, hi, ok := bounds(e); ok && (id < lo || id > hi) {
			return nil, fmt.Errorf("%w: minimega gives %s the ID %d, outside the range %d–%d", ErrInvalid, alias, id, lo, hi)
		}

		aliases[alias] = id
	}

	spec.SetAliases(aliases)

	if err := saveSpec(exp); err != nil {
		return nil, err
	}

	return live, nil
}

// Blacklist reads minimega's blacklist, and whether minimega can remove an ID
// from it.
func Blacklist(ctx context.Context) ([]int, bool, error) {
	ids, err := mm.GetVLANBlacklist(mm.Context(ctx))
	if err != nil {
		return nil, false, fmt.Errorf("reading the minimega VLAN blacklist: %w", err)
	}

	removable, err := mm.VLANBlacklistRemovable(mm.Context(ctx))
	if err != nil {
		return nil, false, fmt.Errorf("reading minimega's commands: %w", err)
	}

	if ids == nil {
		ids = []int{}
	}

	return ids, removable, nil
}

// AddToBlacklist adds the VLAN ID to minimega's blacklist. It refuses an ID
// that a minimega namespace has given an alias, since minimega would drop
// that alias. An ID already on the blacklist is no change.
func AddToBlacklist(ctx context.Context, id int) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: VLAN ID %d is not in %d–%d", ErrInvalid, id, MinID, MaxID)
	}

	m := readMinimega(ctx)
	if !m.OK {
		return fmt.Errorf("reading minimega's VLANs: %s", m.Err) //nolint:err113 // minimega's own text
	}

	if slices.Contains(m.Blacklist, id) {
		return nil
	}

	for _, name := range slices.Sorted(maps.Keys(m.Namespaces)) {
		for alias, live := range m.Namespaces[name].Aliases {
			if live == id {
				return fmt.Errorf("%w: namespace %s gives VLAN alias %s the ID %d", ErrInUse, name, alias, id)
			}
		}
	}

	defer Forget()

	if err := mm.BlacklistVLAN(id, mm.Context(ctx)); err != nil {
		return fmt.Errorf("blacklisting VLAN %d: %w", id, err)
	}

	return nil
}

// RemoveFromBlacklist removes the VLAN ID from minimega's blacklist. The
// error wraps ErrUnsupported when minimega cannot, ErrNotFound when the ID is
// not on the blacklist, and ErrInUse when minimega refuses (a VM uses the ID,
// a host did not answer, or another removal holds it).
func RemoveFromBlacklist(ctx context.Context, id int) error {
	if err := CheckRemoveFromBlacklist(ctx, id); err != nil {
		return err
	}

	defer Forget()

	err := mm.UnblacklistVLAN(id, mm.Context(ctx))

	switch {
	case err == nil:
		return nil
	case errors.Is(err, mm.ErrBlacklistRemovalUnsupported):
		return err //nolint:wrapcheck // already names the VLAN
	case ctx.Err() != nil:
		return fmt.Errorf("removing VLAN %d from the blacklist: %w", id, err)
	case strings.Contains(err.Error(), "is not blacklisted"):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %w", ErrInUse, err)
	}
}

// CheckRemoveFromBlacklist checks a removal as RemoveFromBlacklist does
// before it asks minimega to remove the ID, and changes nothing.
func CheckRemoveFromBlacklist(ctx context.Context, id int) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: VLAN ID %d is not in %d–%d", ErrInvalid, id, MinID, MaxID)
	}

	ids, removable, err := Blacklist(ctx)
	if err != nil {
		return err
	}

	if !removable {
		return fmt.Errorf("%w: update minimega to remove VLAN %d", ErrUnsupported, id)
	}

	if !slices.Contains(ids, id) {
		return fmt.Errorf("%w: minimega does not blacklist VLAN %d", ErrNotFound, id)
	}

	return nil
}

// Release clears the VLAN aliases of a minimega namespace that no experiment
// has. It refuses a namespace that an experiment has, or whose VMs are on its
// VLANs.
func Release(ctx context.Context, namespace string) error {
	if err := CheckRelease(ctx, namespace); err != nil {
		return err
	}

	defer Forget()

	if err := mm.ClearVLANs(mm.NS(namespace), mm.Context(ctx)); err != nil {
		return fmt.Errorf("releasing the VLANs of namespace %s: %w", namespace, err)
	}

	return nil
}

// CheckRelease checks a release as Release does, and changes nothing.
func CheckRelease(ctx context.Context, namespace string) error {
	exps, err := experiment.List()
	if err != nil {
		return fmt.Errorf("listing experiments: %w", err)
	}

	for _, exp := range exps {
		if exp.Metadata.Name == namespace || exp.Spec.ExperimentName() == namespace {
			return fmt.Errorf("%w: experiment %s has the namespace %s", ErrInUse, exp.Metadata.Name, namespace)
		}
	}

	m := readMinimega(ctx)
	if !m.OK {
		return fmt.Errorf("reading minimega's VLANs: %s", m.Err) //nolint:err113 // minimega's own text
	}

	ns, ok := m.Namespaces[namespace]
	if !ok {
		return fmt.Errorf("%w: minimega has no namespace %s", ErrNotFound, namespace)
	}

	ids := map[int]bool{}
	for _, id := range ns.Aliases {
		ids[id] = true
	}

	for _, v := range ns.VMs {
		if ids[v.VLAN] {
			return fmt.Errorf("%w: VM %s of namespace %s is on VLAN %d", ErrInUse, v.VM, namespace, v.VLAN)
		}
	}

	return nil
}
