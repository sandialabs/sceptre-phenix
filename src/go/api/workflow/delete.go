package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/file"
)

// ErrRunning is wrapped by the error [NewDeletePlan] returns when the mapped
// experiment is running and the caller did not set force.
var ErrRunning = errors.New("workflow experiment is running")

// DeletePlan is what a delete of a workflow branch does. Experiment names the
// experiment mapped to the branch, "" for none, and Stop is true when the
// delete stops it first.
type DeletePlan struct {
	Experiment string
	Stop       bool
}

// NewDeletePlan decides what a delete of branch does. mapped holds the
// experiments mapped to branch (see [Mapped]). The error wraps [ErrConflict]
// when more than one experiment is mapped, and [ErrRunning] when the mapped
// experiment is running and force is false; the plan then still names it.
func NewDeletePlan(branch string, mapped []types.Experiment, force bool) (DeletePlan, error) {
	switch len(mapped) {
	case 0:
		return DeletePlan{Experiment: "", Stop: false}, nil
	case 1:
	default:
		return DeletePlan{Experiment: "", Stop: false}, conflictMapped(branch, mapped)
	}

	name := mapped[0].Metadata.Name

	if !mapped[0].Running() {
		return DeletePlan{Experiment: name, Stop: false}, nil
	}

	if !force {
		return DeletePlan{Experiment: name, Stop: false}, fmt.Errorf(
			"%w: experiment %q mapped to workflow branch %q is running; use force (the -f flag) to stop and delete it",
			ErrRunning, name, branch,
		)
	}

	return DeletePlan{Experiment: name, Stop: true}, nil
}

// InjectsUsers returns the sorted names of the experiments in exps, except the
// experiment named except, with a node injection whose absolute source path is
// dir or is inside dir. It compares each path as written and with its symbolic
// links resolved (see [ResolvedPath]). It ignores relative source paths. A
// relative dir matches nothing. The result is never nil.
func InjectsUsers(exps []types.Experiment, dir, except string) []string {
	users := []string{}

	if !filepath.IsAbs(dir) {
		return users
	}

	dirs := []string{filepath.Clean(dir)}
	if resolved := ResolvedPath(dir); resolved != dirs[0] {
		dirs = append(dirs, resolved)
	}

	for _, exp := range exps {
		if exp.Metadata.Name == except || exp.Spec == nil {
			continue
		}

		if injectsFrom(exp.Spec.Topology(), dirs) {
			users = append(users, exp.Metadata.Name)
		}
	}

	slices.Sort(users)

	return slices.Compact(users)
}

// injectsFrom reports whether a node of topology has an injection whose
// absolute source path, as written or resolved, is one of dirs or is inside one.
func injectsFrom(topology ifaces.TopologySpec, dirs []string) bool {
	if topology == nil {
		return false
	}

	within := func(src string) bool {
		return slices.ContainsFunc(dirs, func(dir string) bool { return file.WithinDir(dir, src) })
	}

	for _, node := range topology.Nodes() {
		for _, inject := range node.Injections() {
			src := inject.Src()
			if !filepath.IsAbs(src) {
				continue
			}

			// ResolvedPath gets the text as written: Clean would drop a ".." after a link.
			if within(filepath.Clean(src)) || within(ResolvedPath(src)) {
				return true
			}
		}
	}

	return false
}

// ResolvedPath returns the absolute path with the symbolic links resolved in
// its longest leading part that exists; the rest follows as written. A path
// that does not resolve keeps its cleaned text.
func ResolvedPath(path string) string {
	sep := string(filepath.Separator)
	dir, rest := path, ""

	// Clean would remove ".." before the link in front of it resolves.
	if trimmed := strings.TrimRight(path, sep); trimmed != "" {
		dir = trimmed
	}

	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}

		// filepath.Dir cleans, so the last part is cut off by hand.
		cut := strings.LastIndex(dir, sep)
		if cut < 0 {
			return filepath.Clean(path)
		}

		rest = filepath.Join(dir[cut+1:], rest)

		if dir = strings.TrimRight(dir[:cut], sep); dir == "" {
			dir = sep
		}
	}
}

// DeleteResult is the JSON body of a workflow delete response. Experiment is
// "" when no experiment is mapped. Stopped is true when the delete stopped the
// experiment, or when a dry run would stop it. InjectsUsedBy names the other
// experiments that inject files from InjectsDir.
type DeleteResult struct {
	Branch        string   `json:"branch"`
	DryRun        bool     `json:"dryRun"`
	Experiment    string   `json:"experiment"`
	Stopped       bool     `json:"stopped"`
	InjectsDir    string   `json:"injectsDir"`
	InjectsUsedBy []string `json:"injectsUsedBy"`
}

// MarshalJSON encodes r with a nil InjectsUsedBy as an empty array.
func (r DeleteResult) MarshalJSON() ([]byte, error) {
	type plain DeleteResult

	if r.InjectsUsedBy == nil {
		r.InjectsUsedBy = []string{}
	}

	return json.Marshal(plain(r)) //nolint:wrapcheck // the encoding of a plain struct
}
