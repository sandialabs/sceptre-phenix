package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"phenix/api/workflow"
	"phenix/util/plog"
)

// markerNames returns the entries that mark a topology directory.
func markerNames() []string {
	return []string{workflowConfigName, legacyWorkflowConfig, configsDirName, injectsDirName}
}

// isMarkerName reports whether name is a marker name in any letter case, as
// a file system that ignores letter case would find it.
func isMarkerName(name string) bool {
	return slices.ContainsFunc(markerNames(), func(marker string) bool { return strings.EqualFold(name, marker) })
}

// injectsMarkerEntry returns the first entry with a marker name at the top
// level of injects or directly inside one of its top-level directories,
// relative to injects, or "". It follows no symbolic link, since staging
// copies a link as a link.
func injectsMarkerEntry(injects string) string {
	top, _ := os.ReadDir(injects)

	for _, entry := range top {
		if isMarkerName(entry.Name()) {
			return entry.Name()
		}

		if !entry.IsDir() {
			continue
		}

		inner, _ := os.ReadDir(filepath.Join(injects, entry.Name()))

		for _, child := range inner {
			if isMarkerName(child.Name()) {
				return filepath.Join(entry.Name(), child.Name())
			}
		}
	}

	return ""
}

// stage copies phenix-injects into <injectsBase>/<name>, once checkDestination
// looked at the destination again. When only the previous copy could not be
// removed, the new tree is in place, so stage warns and goes on.
func (r *applyRun) stage(injectsBase string) error {
	if r.injects == "" {
		plog.Warn(plog.TypeSystem, "no phenix-injects directory; skipping", "step", stepInjects, "dir", r.dir)

		return nil
	}

	dest := filepath.Join(injectsBase, r.name)
	cwd, _ := os.Getwd()

	if err := r.checkDestination(injectsBase, dest, cwd); err != nil {
		return err
	}

	// Staging must not replace the topology directory or the working
	// directory.
	protect := []string{r.dir}
	if cwd != "" {
		protect = append(protect, cwd)
	}

	plog.Info(plog.TypeSystem, "staging injects", "step", stepInjects, "dir", dest)

	count, err := workflow.StageInjects(r.injects, injectsBase, r.name, protect...)

	// kept is the error that names the previous copy this run kept, if any.
	var kept error

	switch {
	case errors.Is(err, workflow.ErrPreviousCopyKept):
		kept = err

		plog.Warn(
			plog.TypeSystem, "injects staged, but the previous copy could not be removed; remove it by hand",
			"step", stepInjects, "dir", dest, "err", err,
		)
	case errors.Is(err, fs.ErrPermission):
		// Name the remedy for the path at fault: a source file that cannot
		// be read, or else the injects directory.
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) && workflow.PathWithin(pathErr.Path, r.injects) {
			return fmt.Errorf("staging injects into %s: %w; make %s readable by the user running phenix", dest, err, pathErr.Path)
		}

		return fmt.Errorf("staging injects into %s: %w; run phenix as a user who can write to %s", dest, err, injectsBase)
	case err != nil:
		return fmt.Errorf("staging injects into %s: %w", dest, err)
	}

	r.staged = true

	plog.Info(plog.TypeSystem, "injects staged", "step", stepInjects, "dir", dest, "files", count)

	warnStagingLeftovers(injectsBase, r.name, kept)

	return nil
}

// checkDestination refuses to stage into injectsBase, or to replace dest, its
// <injectsBase>/<name>, when a wrong --base-dir.injects or -b would make
// staging replace a topology directory, a part of one, the topologies
// directory or a file. These checks are preflight-only: StageInjects knows
// nothing of topology directories and guards only its source and the
// protected paths. Like StageInjects, it resolves the symbolic links in the
// base but not dest itself, which staging replaces as a link. cwd resolves a
// relative dest or topologies directory. It only reads.
func (r *applyRun) checkDestination(injectsBase, dest, cwd string) error {
	if marker := topologyMarker(injectsBase); marker != "" {
		return fmt.Errorf(
			"refusing to stage into %s: it holds %s, which marks a topology directory; check --base-dir.injects", injectsBase, marker,
		)
	}

	absDest := absolutePath(dest, cwd)

	// The path that staging replaces: <resolved base>/<name>.
	base := resolvedPath(filepath.Dir(absDest))
	replaced := filepath.Join(base, filepath.Base(absDest))

	if workflow.PathWithin(replaced, resolvedPath(r.dir)) {
		return fmt.Errorf(
			"refusing to replace %s: it lies inside the topology directory %s; check --base-dir.injects and -b", dest, r.dir,
		)
	}

	if r.topologiesUnder(replaced, cwd) {
		return fmt.Errorf(
			"refusing to replace %s: it is or holds the topologies directory %s; check --base-dir.injects and -b",
			dest,
			r.opts.TopologiesBase,
		)
	}

	// A dest that does not exist yet holds nothing, and staging replaces a
	// dest that is a link, not what it points to.
	if info, err := os.Lstat(dest); err == nil && info.Mode()&fs.ModeSymlink == 0 {
		if err := checkReplacedEntry(dest, info.IsDir()); err != nil {
			return err
		}
	}

	if ancestor, marker := topologyAncestor(base); ancestor != "" {
		return fmt.Errorf(
			"refusing to stage into %s: it lies inside %s, which holds %s, a name that marks a topology directory; "+
				"check --base-dir.injects",
			injectsBase, ancestor, marker,
		)
	}

	return nil
}

// topologiesUnder reports whether the topologies directory exists and, with
// its symbolic links resolved, is replaced or lies inside it.
func (r *applyRun) topologiesUnder(replaced, cwd string) bool {
	if r.opts.TopologiesBase == "" {
		return false
	}

	topologies, err := filepath.EvalSymlinks(absolutePath(r.opts.TopologiesBase, cwd))

	return err == nil && workflow.PathWithin(topologies, replaced)
}

// topologyAncestor returns the nearest directory above dir, which is
// absolute, that holds an entry marking a topology directory, and that
// entry, or "", "".
func topologyAncestor(dir string) (string, string) {
	for current := dir; current != filepath.Dir(current); {
		current = filepath.Dir(current)

		if marker := topologyMarker(current); marker != "" {
			return current, marker
		}
	}

	return "", ""
}

// absolutePath returns path, joined to cwd when it is relative.
func absolutePath(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(cwd, path)
}

// checkReplacedEntry refuses to replace dest, an existing entry that is not a
// symbolic link, unless it is a directory and neither it nor a directory in
// it holds an entry marking a topology directory.
func checkReplacedEntry(dest string, isDir bool) error {
	if !isDir {
		return fmt.Errorf("refusing to replace %s: it is not a directory; check --base-dir.injects and -b", dest)
	}

	if marker := topologyMarker(dest); marker != "" {
		return markedDestinationError(dest, "it", marker)
	}

	// A destination that cannot be listed has no directory to look into.
	entries, _ := os.ReadDir(dest)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		if marker := topologyMarker(filepath.Join(dest, entry.Name())); marker != "" {
			return markedDestinationError(dest, "its directory "+entry.Name(), marker)
		}
	}

	return nil
}

// markedDestinationError refuses to replace dest because holder, dest itself
// or one of its directories, holds marker. The command never stages such an
// entry, so dest holds content it did not stage.
func markedDestinationError(dest, holder, marker string) error {
	return fmt.Errorf(
		"refusing to replace %s: %s holds %s, which marks a topology directory; check --base-dir.injects and -b", dest, holder, marker,
	)
}

// topologyMarker returns the first entry of dir that marks a topology
// directory, in the order of markerNames, or "".
func topologyMarker(dir string) string {
	for _, name := range markerNames() {
		if pathExists(filepath.Join(dir, name)) {
			return name
		}
	}

	return ""
}

// pathExists reports whether there is an entry at path, a dangling symbolic
// link included.
func pathExists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

// resolvedPath resolves the symbolic links in the longest leading part of
// path, which is absolute, that exists; the rest follows as written. Unlike
// StageInjects, the preflight must handle a base that does not exist yet.
func resolvedPath(path string) string {
	dir, rest := path, ""

	resolved, err := filepath.EvalSymlinks(dir)
	for err != nil && dir != filepath.Dir(dir) {
		dir, rest = filepath.Dir(dir), filepath.Join(filepath.Base(dir), rest)
		resolved, err = filepath.EvalSymlinks(dir)
	}

	return filepath.Join(resolved, rest)
}

// warnStagingLeftovers warns about each leftover of an earlier staging of
// name next to the staged tree, except the previous copy this run kept, which
// kept names. It removes none: a staging of the same name that is still
// running owns its copy.
func warnStagingLeftovers(injectsBase, name string, kept error) {
	leftovers, err := workflow.StagingLeftovers(injectsBase, name)
	if err != nil {
		plog.Warn(
			plog.TypeSystem, "could not look for leftovers of earlier runs next to the staged injects",
			"step", stepInjects, "dir", injectsBase, "err", err,
		)

		return
	}

	for _, path := range leftovers {
		if kept != nil && strings.Contains(kept.Error(), path) {
			continue
		}

		plog.Warn(
			plog.TypeSystem, "leftover of an earlier run next to the staged injects; remove it by hand if no other run is active",
			"step", stepInjects, "path", path,
		)
	}
}
