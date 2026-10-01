package workflow

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ownerAccess is added to the mode of every staged directory, so a later
// stage can always replace the tree.
const ownerAccess fs.FileMode = 0o700

// The hidden siblings of base/name that a staging works in are named
// .<name>, then stagingInfix for the copy being made or previousInfix for
// the tree it replaces, then a random suffix from [rand.Text]: randomSuffixLen
// characters from A to Z and 2 to 7.
const (
	stagingInfix    = ".tmp-"
	previousInfix   = ".old-"
	randomSuffixLen = 26
)

// ErrUnsafeDestination is returned by [StageInjects] when replacing the
// destination would remove the source or a protected path, or would copy the
// source into itself.
var ErrUnsafeDestination = errors.New("unsafe injects destination")

// ErrPreviousCopyKept is wrapped by the error [StageInjects] returns when the
// new tree is in place but the previous copy could not be removed.
var ErrPreviousCopyKept = errors.New("could not remove the previous injects copy")

// rename is [os.Rename]. Tests replace it to fail the renames in swapDir,
// which a test user cannot make fail in a directory it has just written to.
var rename = os.Rename //nolint:gochecknoglobals // overridden by tests

// removeAll is [os.RemoveAll]. Tests replace it to fail the removals in
// StageInjects and swapDir, which a test user cannot make fail in a directory
// it has just written to.
var removeAll = os.RemoveAll //nolint:gochecknoglobals // overridden by tests

// StageInjects copies the directory tree src to base/name and returns the
// number of regular files it copied. On any error it returns 0, except for an
// error that wraps [ErrPreviousCopyKept]: the new tree is then in place, the
// count is returned, and the error names the old tree.
//
// The name must pass [ValidateName], so base/name is always a direct child of
// base. Base is created (mode 0o755 before the umask) when it is missing.
// That happens before the safety checks, because symlinks can only be
// resolved in paths that exist. Symlinks in base, src and every protect path
// are then resolved. The call fails with [ErrUnsafeDestination] when
// base/name is src or lies inside it, or when src or a protect path is
// base/name or lies inside it. Base/name may lie inside a protect path, so a
// caller can protect its working directory even when that is an ancestor of
// base. Every protect path must exist.
//
// The tree is copied into a hidden .<name>.tmp-<random> sibling of base/name.
// Symlinks are recreated, never followed. Regular files keep their permission
// bits. Directories keep theirs plus owner read, write and search. Only
// permission bits are copied: setuid, setgid and sticky bits are dropped, so a
// staged directory in a setgid base does not keep the setgid bit it inherits.
// Any other file type fails the copy. An existing base/name is then renamed to
// a hidden .<name>.old-<random> sibling, the copy is renamed into place and
// the old tree is removed, so base/name is missing only between those two
// renames. If the copy or the swap fails, the copy is removed and any
// existing base/name is put back as it was. If putting it back fails too, it
// stays in the .<name>.old-<random> sibling, which the error names. A failure
// to remove the copy is added to the error, which then names the copy. If
// only removing the old tree fails, the new tree stays in place and the error
// wraps [ErrPreviousCopyKept]. Filesystem errors are wrapped, so callers can
// match them with [errors.Is], for example against [fs.ErrPermission].
func StageInjects(src, base, name string, protect ...string) (int, error) {
	if err := ValidateName(name); err != nil {
		return 0, err
	}

	info, err := os.Stat(src)
	if err != nil {
		return 0, fmt.Errorf("reading injects source: %w", err)
	}

	if !info.IsDir() {
		return 0, fmt.Errorf("injects source %s is not a directory", src)
	}

	if err := os.MkdirAll(base, 0o755); err != nil {
		return 0, fmt.Errorf("creating injects directory %s: %w", base, err)
	}

	dest, err := injectsDestination(src, base, name, protect)
	if err != nil {
		return 0, err
	}

	parent := filepath.Dir(dest)
	tmp := filepath.Join(parent, "."+name+stagingInfix+rand.Text())

	files, err := copyTree(src, tmp, info.Mode().Perm())
	if err != nil {
		return 0, withSecondError(fmt.Errorf("copying %s to %s: %w", src, parent, err), removeStaging(tmp))
	}

	if err := swapDir(tmp, dest); err != nil {
		// The copy is base/name now; only the old tree is left over.
		if errors.Is(err, ErrPreviousCopyKept) {
			return files, err
		}

		return 0, withSecondError(err, removeStaging(tmp))
	}

	return files, nil
}

// removeStaging removes the staging copy tmp of a failed staging. The error
// names tmp, which stays behind when the removal fails.
func removeStaging(tmp string) error {
	if err := removeAll(tmp); err != nil {
		return fmt.Errorf("removing the staging copy %s: %w", tmp, err)
	}

	return nil
}

// withSecondError returns an error that wraps cause and each error of more
// that is not nil, and puts them on one line in that order, separated by
// "; ", so that a staging error stays one line in a log. Errors that are nil
// are left out: it returns nil when all are, and the one error as it is when
// only one is not nil.
func withSecondError(cause error, more ...error) error {
	err := cause

	for _, second := range more {
		switch {
		case second == nil:
		case err == nil:
			err = second
		default:
			err = fmt.Errorf("%w; %w", err, second)
		}
	}

	return err
}

// StagingLeftovers returns the paths of the hidden .<name>.tmp-<random> and
// .<name>.old-<random> siblings of base/name, sorted. [StageInjects] leaves
// such a sibling behind when it is killed, or when it cannot remove it; a
// staging of the same name that is still running also holds one. Only an
// entry whose suffix is a random suffix as StageInjects makes it counts, so
// the siblings of another name that starts with name, such as foo.old-x for
// foo, are not listed. StagingLeftovers removes nothing. The name must pass
// [ValidateName], and symlinks in base, which must exist, are resolved, as
// StageInjects does.
func StagingLeftovers(base, name string) ([]string, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	dir, err := resolveRealPath(base)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", base, err)
	}

	// ReadDir sorts the entries by name, so the result is sorted too.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}

	var leftovers []string

	for _, entry := range entries {
		for _, infix := range []string{stagingInfix, previousInfix} {
			suffix, ok := strings.CutPrefix(entry.Name(), "."+name+infix)
			if ok && isRandomSuffix(suffix) {
				leftovers = append(leftovers, filepath.Join(dir, entry.Name()))
			}
		}
	}

	return leftovers, nil
}

// isRandomSuffix reports whether s could come from [rand.Text]: exactly
// randomSuffixLen characters from A to Z and 2 to 7.
func isRandomSuffix(s string) bool {
	if len(s) != randomSuffixLen {
		return false
	}

	for _, c := range s {
		if (c < 'A' || c > 'Z') && (c < '2' || c > '7') {
			return false
		}
	}

	return true
}

// injectsDestination resolves symlinks in base, src and every protect path
// and returns base/name. It fails with [ErrUnsafeDestination] when base/name
// is src or lies inside it, or when src or a protect path is base/name or
// lies inside it. Every path must exist.
func injectsDestination(src, base, name string, protect []string) (string, error) {
	paths := slices.Concat([]string{base, src}, protect)
	resolved := make([]string, 0, len(paths))

	for _, path := range paths {
		abs, err := resolveRealPath(path)
		if err != nil {
			return "", fmt.Errorf("resolving %s: %w", path, err)
		}

		resolved = append(resolved, abs)
	}

	dest := filepath.Join(resolved[0], name)
	source := resolved[1]

	if PathWithin(dest, source) {
		return "", fmt.Errorf("%w: the destination %s is the injects source %s or inside it", ErrUnsafeDestination, dest, source)
	}

	// resolved[1:] is src followed by every protect path.
	for _, path := range resolved[1:] {
		if PathWithin(path, dest) {
			return "", fmt.Errorf("%w: replacing %s would remove %s", ErrUnsafeDestination, dest, path)
		}
	}

	return dest, nil
}

// resolveRealPath returns p as an absolute path with every symlink resolved.
func resolveRealPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}

	return abs, err
}

// PathWithin reports whether target is dir or lies inside it. Both paths
// must be absolute and clean. The workflow apply command shares it, so its
// preflight and the staging agree on what lies inside what.
func PathWithin(target, dir string) bool {
	rel, err := filepath.Rel(dir, target)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyTree copies the directory src to the new directory dst, then gives dst
// the permissions perm plus owner access. It returns the number of regular
// files copied.
func copyTree(src, dst string, perm fs.FileMode) (int, error) {
	entries, err := os.ReadDir(src)
	if err == nil {
		err = os.Mkdir(dst, ownerAccess)
	}

	if err != nil {
		return 0, err
	}

	files := 0

	for _, entry := range entries {
		n, err := copyEntry(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()))
		if err != nil {
			return 0, err
		}

		files += n
	}

	return files, os.Chmod(dst, perm|ownerAccess)
}

// copyEntry copies the directory, regular file or symlink src to dst. It
// returns the number of regular files copied.
func copyEntry(src, dst string) (int, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return 0, err
	}

	mode := info.Mode()

	switch {
	case mode&fs.ModeSymlink != 0:
		return 0, copySymlink(src, dst)
	case mode.IsDir():
		return copyTree(src, dst, mode.Perm())
	case mode.IsRegular():
		return 1, copyFile(src, dst, mode.Perm())
	default:
		return 0, fmt.Errorf("%s: unsupported file type %s", src, mode.Type())
	}
}

// copyFile copies the regular file src to the new file dst and gives dst the
// permissions perm.
func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}

	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, err = io.Copy(out, in)
		err = withSecondError(err, out.Close(), os.Chmod(dst, perm))
	}

	return err
}

// copySymlink creates dst as a symlink with the same target as src.
func copySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err == nil {
		err = os.Symlink(target, dst)
	}

	return err
}

// swapDir renames tmp to dest. An existing dest is first renamed to a hidden
// .<name>.old-<random> sibling. That sibling is put back if tmp cannot take
// its place, and it is removed once tmp has. When only that removal fails,
// the error wraps [ErrPreviousCopyKept].
func swapDir(tmp, dest string) error {
	old := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+previousInfix+rand.Text())

	err := rename(dest, old)
	if errors.Is(err, fs.ErrNotExist) {
		if err := rename(tmp, dest); err != nil {
			return fmt.Errorf("moving staged injects to %s: %w", dest, err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("moving %s aside: %w", dest, err)
	}

	if err = rename(tmp, dest); err != nil {
		moveErr := fmt.Errorf("moving staged injects to %s: %w", dest, err)

		// Put the previous copy back, and say where it is if that fails too.
		if restoreErr := rename(old, dest); restoreErr != nil {
			return withSecondError(moveErr, fmt.Errorf("putting the previous copy back failed; it is kept in %s: %w", old, restoreErr))
		}

		return moveErr
	}

	if err = removeAll(old); err != nil {
		return fmt.Errorf("staged injects in %s, but %w %s: %w", dest, ErrPreviousCopyKept, old, err)
	}

	return nil
}
