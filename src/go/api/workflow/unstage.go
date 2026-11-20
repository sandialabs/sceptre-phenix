package workflow

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"phenix/util/file"
)

// Staged is what [StageInjects] left for a name in an injects base. Dir is
// base/name, with the symlinks in base resolved, or "" when it does not exist.
// Leftovers are the paths that [StagingLeftovers] finds.
type Staged struct {
	Dir       string
	Leftovers []string
}

// Paths returns Dir, when it is set, followed by the leftovers.
func (s Staged) Paths() []string {
	if s.Dir == "" {
		return slices.Clone(s.Leftovers)
	}

	return append([]string{s.Dir}, s.Leftovers...)
}

// RemoveOptions configures [RemoveStagedInjects]. With KeepDir, only the
// leftovers are removed. Protect lists paths that the removal must not remove.
type RemoveOptions struct {
	KeepDir bool
	Protect []string
}

// FindStagedInjects returns what [RemoveStagedInjects] would remove for name
// in base. It removes nothing. It fails with [ErrUnsafeDestination] when
// base/name is not a directory or a symlink, or when a protect path, with its
// symlinks resolved, is a removed path or lies inside one. It also fails when
// the name does not pass [ValidateName].
func FindStagedInjects(base, name string, protect ...string) (Staged, error) {
	return findStaged(base, name, RemoveOptions{KeepDir: false, Protect: protect})
}

// RemoveStagedInjects removes base/name and the staging leftovers of name in
// base, and returns what it removed. It removes a symlink at base/name, not its
// target. A missing base or base/name is not an error. It gives the errors of
// [FindStagedInjects], and removes nothing then. On a failed removal, it removes
// the other paths and returns them with the error.
func RemoveStagedInjects(base, name string, opts RemoveOptions) (Staged, error) {
	found, err := findStaged(base, name, opts)
	if err != nil {
		return Staged{}, err
	}

	var (
		removed Staged
		errs    error
	)

	if found.Dir != "" {
		if err := removeAll(found.Dir); err != nil {
			errs = withSecondError(errs, fmt.Errorf("removing %s: %w", found.Dir, err))
		} else {
			removed.Dir = found.Dir
		}
	}

	for _, leftover := range found.Leftovers {
		if err := removeAll(leftover); err != nil {
			errs = withSecondError(errs, fmt.Errorf("removing %s: %w", leftover, err))

			continue
		}

		removed.Leftovers = append(removed.Leftovers, leftover)
	}

	return removed, errs
}

// findStaged returns what [RemoveStagedInjects] removes with opts. Dir is
// "" when opts.KeepDir is set.
func findStaged(base, name string, opts RemoveOptions) (Staged, error) {
	if err := ValidateName(name); err != nil {
		return Staged{}, err
	}

	dir, err := resolveRealPath(base)
	if errors.Is(err, fs.ErrNotExist) {
		return Staged{}, nil
	}

	if err != nil {
		return Staged{}, fmt.Errorf("resolving %s: %w", base, err)
	}

	var found Staged

	dest := filepath.Join(dir, name)

	info, err := os.Lstat(dest)

	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Staged{}, fmt.Errorf("reading %s: %w", dest, err)
	case info.Mode()&fs.ModeSymlink == 0 && !info.IsDir():
		return Staged{}, fmt.Errorf("%w: %s is not a directory", ErrUnsafeDestination, dest)
	case !opts.KeepDir:
		found.Dir = dest
	}

	if found.Leftovers, err = StagingLeftovers(dir, name); err != nil {
		return Staged{}, err
	}

	for _, path := range opts.Protect {
		resolved, err := resolveRealPath(path)
		if err != nil {
			return Staged{}, fmt.Errorf("resolving %s: %w", path, err)
		}

		for _, removed := range found.Paths() {
			if file.WithinDir(removed, resolved) {
				return Staged{}, fmt.Errorf("%w: removing %s would remove %s", ErrUnsafeDestination, removed, path)
			}
		}
	}

	return found, nil
}
