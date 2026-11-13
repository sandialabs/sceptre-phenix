package cmd

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"phenix/api/config"
	"phenix/store"
	"phenix/util"
)

// deleteWildcard is the only wildcard of a delete pattern. It matches zero or
// more characters.
const deleteWildcard = "*"

// errNoMatch is the cause of the failure of a delete pattern that matches no
// stored configuration.
var errNoMatch = errors.New("the pattern matches no configuration")

// isDeletePattern reports whether arg is a delete pattern: an argument that
// contains the wildcard *.
func isDeletePattern(arg string) bool {
	return strings.Contains(arg, deleteWildcard)
}

// matchDeletePattern reports whether name matches pattern, in which each *
// matches zero or more characters and every other character, such as ?, [
// and \, matches only itself. The match is case-sensitive.
func matchDeletePattern(pattern, name string) bool {
	parts := strings.Split(pattern, deleteWildcard)
	if len(parts) == 1 {
		return pattern == name
	}

	first, last := parts[0], parts[len(parts)-1]
	if len(name) < len(first)+len(last) || !strings.HasPrefix(name, first) || !strings.HasSuffix(name, last) {
		return false
	}

	// The first match of each middle part leaves the most room for the next.
	rest := name[len(first) : len(name)-len(last)]

	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(rest, part)
		if i < 0 {
			return false
		}

		rest = rest[i+len(part):]
	}

	return true
}

// deleteKey is the key of the config of kind, in any letter case, and name
// in a delete plan: the lowercase kind, a slash and the name, such as
// topology/foo.
func deleteKey(kind, name string) string {
	return strings.ToLower(kind) + "/" + name
}

// deleteStep is one argument of a delete command: an exact config, or the
// configs that a pattern selects.
type deleteStep struct {
	arg     string          // the argument as given
	key     string          // the key of an exact argument; empty for a pattern
	kind    string          // the kind that config.DeleteKind takes for a pattern, or "all"
	matches []string        // the keys that the pattern selects, sorted
	keys    map[string]bool // the keys of matches
}

// deletePlan is what a delete command deletes, one step for each argument. A
// config that two arguments select is in the step of the first one only.
type deletePlan struct {
	steps   []deleteStep
	claimed map[string]bool
	failed  []*util.HumanizedError
}

func newDeletePlan() *deletePlan {
	return &deletePlan{steps: nil, claimed: make(map[string]bool), failed: nil}
}

// addExact adds the exact argument arg, the config of kind and name, unless
// an earlier argument selects it.
func (p *deletePlan) addExact(arg, kind, name string) {
	key := deleteKey(kind, name)
	if p.claimed[key] {
		return
	}

	p.claimed[key] = true
	p.steps = append(p.steps, deleteStep{arg: arg, key: key, kind: "", matches: nil, keys: nil})
}

// addPattern adds the pattern argument arg, which selects the configs of kind
// ("all" for every kind) whose names match pattern, except those that an
// earlier argument selects. When every is false, a pattern that matches no
// stored config is a failure. A failure to list the configs is a failure.
func (p *deletePlan) addPattern(arg, kind, pattern string, every bool) {
	configs, err := config.List(kind)
	if err != nil {
		p.failed = append(p.failed, util.HumanizeError(err, "%s", "Unable to list the configurations that "+arg+" matches"))

		return
	}

	step := deleteStep{arg: arg, key: "", kind: kind, matches: nil, keys: make(map[string]bool)}
	found := false

	for _, c := range configs {
		if !matchDeletePattern(pattern, c.Metadata.Name) {
			continue
		}

		found = true
		key := deleteKey(c.Kind, c.Metadata.Name)

		if p.claimed[key] {
			continue
		}

		p.claimed[key] = true
		step.keys[key] = true
		step.matches = append(step.matches, key)
	}

	if !found && !every {
		p.failed = append(p.failed, util.HumanizeError(fmt.Errorf("unable to delete %s: %w", arg, errNoMatch), ""))

		return
	}

	slices.Sort(step.matches)
	p.steps = append(p.steps, step)
}

// writeDeleteMatches writes to w the names that the pattern arg selects, with
// their count, such as "2 configurations match topology/cm-*:" and one
// indented name on each line. It writes nothing for no names.
func writeDeleteMatches(w io.Writer, noun, arg string, names []string) {
	switch len(names) {
	case 0:
		return
	case 1:
		fmt.Fprintf(w, "1 %s matches %s:\n", noun, arg)
	default:
		fmt.Fprintf(w, "%d %ss match %s:\n", len(names), noun, arg)
	}

	for _, name := range names {
		fmt.Fprintf(w, "  %s\n", name)
	}
}

// deleteStepMatches deletes the configs that the pattern step selects with
// config.DeleteKind, so that it does not delete the config of a running
// experiment. In a dry run, it calls dry.skip with item(key) for each config
// and deletes nothing. It calls deleted with the key of each config it
// deleted, and returns a failure for each config it did not delete.
func deleteStepMatches(
	dry dryRun, step deleteStep, item func(key string) string, deleted func(key string),
) []*util.HumanizedError {
	if len(step.matches) == 0 {
		return nil
	}

	opts := []config.DeleteKindOption{
		config.DeleteKindSelected(func(c store.Config) bool {
			return step.keys[deleteKey(c.Kind, c.Metadata.Name)]
		}),
	}

	if dry.enabled() {
		opts = append(opts, config.DeleteKindWith(func(name string) error {
			kind, rest, _ := strings.Cut(name, "/")
			dry.skip("delete", item(deleteKey(kind, rest)))

			return nil
		}))
	}

	names, fails, err := config.DeleteKind(step.kind, opts...)
	if err != nil {
		return []*util.HumanizedError{
			util.HumanizeError(err, "%s", "Unable to list the configurations that "+step.arg+" matches"),
		}
	}

	if !dry.enabled() {
		for _, name := range names {
			kind, rest, _ := strings.Cut(name, "/")
			deleted(deleteKey(kind, rest))
		}
	}

	failed := make([]*util.HumanizedError, 0, len(fails))

	for _, f := range fails {
		kind, rest, _ := strings.Cut(f.Config, "/")
		key := deleteKey(kind, rest)

		if errors.Is(f.Err, config.ErrRunningExperiment) {
			failed = append(failed, util.HumanizeError(fmt.Errorf("unable to delete the %s configuration: %w", key, f.Err), ""))

			continue
		}

		failed = append(failed, util.HumanizeError(f.Err, "%s", "Unable to delete the "+key+" configuration"))
	}

	return failed
}
