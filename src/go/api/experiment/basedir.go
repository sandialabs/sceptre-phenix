package experiment

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"phenix/util/common"
	"phenix/util/plog"
)

// ErrBaseDirOutside is wrapped by the error of a create or an update through
// the web API for an experiment base directory that is not inside the
// experiments folder (see [BaseDirRoot]).
var ErrBaseDirOutside = errors.New("experiment base directory is not inside the experiments folder")

// BaseDirRoot is the experiments folder, <base-dir.phenix>/experiments. A
// create, update or delete through the web API keeps experiment base
// directories inside it.
func BaseDirRoot() string {
	return filepath.Join(common.PhenixBase, "experiments")
}

// checkBaseDir returns an error that wraps ErrBaseDirOutside unless dir is an
// absolute path of a folder inside root, not root itself, with the symlinks
// of both resolved (see resolveExisting).
func checkBaseDir(dir, root string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrBaseDirOutside, dir)
	}

	resolvedRoot, err := resolveExisting(root)
	if err != nil {
		return fmt.Errorf("%w: resolving %s: %w", ErrBaseDirOutside, root, err)
	}

	resolved, err := resolveExisting(dir)
	if err != nil {
		return fmt.Errorf("%w: resolving %s: %w", ErrBaseDirOutside, dir, err)
	}

	if !strictlyInside(resolved, resolvedRoot) {
		return fmt.Errorf("%w: %s is not a folder inside %s", ErrBaseDirOutside, dir, root)
	}

	return nil
}

// sameBaseDir reports whether the absolute paths dir and stored name the same
// folder: they are the same after Clean, or after resolveExisting.
func sameBaseDir(dir, stored string) bool {
	if stored == "" || !filepath.IsAbs(dir) || !filepath.IsAbs(stored) {
		return false
	}

	if filepath.Clean(dir) == filepath.Clean(stored) {
		return true
	}

	resolved, err := resolveExisting(dir)
	if err != nil {
		return false
	}

	resolvedStored, err := resolveExisting(stored)

	return err == nil && resolved == resolvedStored
}

// maxSymlinks is the most symlinks resolveExisting follows for one path.
const maxSymlinks = 255

// errSymlinkLoop is the error of resolveExisting for a path with more than
// maxSymlinks symlinks.
var errSymlinkLoop = errors.New("too many levels of symlinks")

// resolveExisting is the clean, absolute path of the folder that the
// absolute path names when its missing folders are made, as mkdir -p makes
// them: it follows each symlink, also a symlink before "..", and keeps each
// missing part as it is written.
func resolveExisting(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is not an absolute path", path)
	}

	links := 0

	return resolveFrom(string(filepath.Separator), path, &links)
}

// resolveFrom resolves path, one part at a time, from the resolved folder cur.
func resolveFrom(cur, path string, links *int) (string, error) {
	for part := range strings.SplitSeq(path, string(filepath.Separator)) {
		switch part {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)

			continue
		}

		next := filepath.Join(cur, part)

		info, err := os.Lstat(next)

		switch {
		case errors.Is(err, fs.ErrNotExist):
			cur = next
		case err != nil:
			return "", fmt.Errorf("reading %s: %w", next, err)
		case info.Mode()&fs.ModeSymlink != 0:
			if *links++; *links > maxSymlinks {
				return "", fmt.Errorf("resolving %s: %w", next, errSymlinkLoop)
			}

			target, err := os.Readlink(next)
			if err != nil {
				return "", fmt.Errorf("reading symlink %s: %w", next, err)
			}

			if filepath.IsAbs(target) {
				cur = string(filepath.Separator)
			}

			if cur, err = resolveFrom(cur, target, links); err != nil {
				return "", err
			}
		default:
			cur = next
		}
	}

	return cur, nil
}

// strictlyInside reports whether the clean path is in folder dir, and is not
// dir itself.
func strictlyInside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}

	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// within reports whether the clean path is folder dir or in it.
func within(path, dir string) bool {
	return path == dir || strictlyInside(path, dir)
}

// protectedDir reports whether removing dir would remove the phenix base or
// the experiments folder: dir is empty, relative, one of them or a folder
// that holds them, such as /.
func protectedDir(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return true
	}

	forms := []string{filepath.Clean(dir)}
	if resolved, err := resolveExisting(dir); err == nil {
		forms = append(forms, resolved)
	}

	for _, keep := range []string{common.PhenixBase, BaseDirRoot()} {
		keeps := []string{filepath.Clean(keep)}
		if resolved, err := resolveExisting(keep); err == nil {
			keeps = append(keeps, resolved)
		}

		for _, d := range forms {
			for _, k := range keeps {
				if within(k, d) {
					return true
				}
			}
		}
	}

	return false
}

// removeBaseDir removes the base directory dir of a deleted experiment. It
// leaves a protected folder (see protectedDir) on every path. When root is not
// empty, as for a delete through the web API, it also leaves a folder outside
// root. It logs a warning for each folder it leaves.
func removeBaseDir(exp, dir, root string) error {
	if protectedDir(dir) {
		plog.Warn(
			plog.TypeSystem, "not removing experiment base directory: it holds the phenix base directory",
			"exp", exp, "dir", dir,
		)

		return nil
	}

	if root != "" && checkBaseDir(dir, root) != nil {
		plog.Warn(
			plog.TypeSystem, "not removing experiment base directory: it is not inside the experiments folder",
			"exp", exp, "dir", dir, "root", root,
		)

		return nil //nolint:nilerr // the folder stays, with a warning, and the delete goes on
	}

	return os.RemoveAll(dir) //nolint:wrapcheck // the caller wraps it
}
