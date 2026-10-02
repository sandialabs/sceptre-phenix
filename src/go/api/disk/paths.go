package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog"
)

// maxDepth is how many folder levels below the minimega files directory the
// disk listing and watcher search.
const maxDepth = 16

var (
	// ErrNotManaged is returned for a path the disk actions do not act on:
	// outside the minimega files directory, in a folder the listing leaves out
	// (see Resolve), or not a regular file.
	ErrNotManaged = errors.New("not a disk image phenix manages")

	// ErrUnsafeName is returned for a path minimega cannot take as one
	// argument (see ValidateMinimegaPath).
	ErrUnsafeName = errors.New("minimega cannot use this path")

	// ErrExists is returned when an action would replace an existing file.
	ErrExists = errors.New("disk image already exists")

	// systemDirs hold files that are not disk images, and that can block or
	// have side effects when opened; topologies naming them are not inspected.
	systemDirs = []string{"/proc", "/sys", "/dev", "/run"} //nolint:gochecknoglobals // constant

	depthWarned atomic.Bool //nolint:gochecknoglobals // warn once per process
)

// Resolve returns the cleaned absolute path of the disk image p names,
// relative to the minimega files directory or absolute, when the disk actions
// may act on it: it is inside the files directory, and not in a folder the
// listing leaves out. The check reads nothing from disk, so it can run before
// any permission check, and it compares the paths as minimega does, as text.
func Resolve(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("%w: no disk given", ErrNotManaged)
	}

	path := mm.GetMMFullPath(p)

	if err := managed(mm.GetMMFullPath(""), path); err != nil {
		return "", err
	}

	return path, nil
}

// ValidateMinimegaPath returns an error wrapping ErrUnsafeName, and naming the
// first character at fault, when minimega cannot take path as one literal
// argument (see minimegaSafe).
func ValidateMinimegaPath(path string) error {
	for _, r := range path {
		if !minimegaSafe(string(r)) {
			return fmt.Errorf("%w: %q contains %q", ErrUnsafeName, path, r)
		}
	}

	return nil
}

// minimegaSafe reports whether minimega takes p as one literal argument:
// file.CommandSafe rejects what its command line splits, unquotes or drops
// as a comment, and glob syntax; minimega also expands "$" as an environment
// variable in every argument, and "," ends the path in a VM's disk spec.
func minimegaSafe(p string) bool {
	return file.CommandSafe(p) && !strings.ContainsAny(p, "$,")
}

// skipped reports whether the listing leaves out rel, a path relative to the
// minimega files directory with no "." or ".." segments, and everything under
// it:
//   - hidden names (starting with "."), such as tool state and partial
//     downloads, and lost+found;
//   - <top>/files, <top>/tmp and <top>/miniccc_responses: the files, scratch
//     images and command responses of the experiment <top>, which its Files
//     tab owns;
//   - top-level saved and transfer_*: minimega's saved VM state and cluster
//     transfers in progress.
func skipped(rel string) bool {
	for i, name := range strings.Split(filepath.ToSlash(rel), "/") {
		switch {
		case strings.HasPrefix(name, "."), name == "lost+found":
			return true
		case i == 0 && (name == "saved" || strings.HasPrefix(name, "transfer_")):
			return true
		case i == 1 && (name == "files" || name == "tmp" || name == "miniccc_responses"):
			return true
		}
	}

	return false
}

// tooDeep reports whether rel, a folder relative to the minimega files
// directory, is more than maxDepth levels below it, logging the first such
// folder the process meets.
func tooDeep(dir, rel string) bool {
	if rel == "." || strings.Count(rel, string(filepath.Separator)) < maxDepth {
		return false
	}

	if depthWarned.CompareAndSwap(false, true) {
		plog.Warn(
			plog.TypeSystem,
			"disk list leaves out folders nested too deeply",
			"folder", filepath.Join(dir, rel),
			"maxDepth", maxDepth,
		)
	}

	return true
}

// managed returns an error wrapping ErrNotManaged unless the disk actions act
// on path: it is inside the files directory dir, and not dir itself or in a
// folder the listing leaves out.
func managed(dir, path string) error {
	rel, inside := within(dir, path)

	switch {
	case !inside:
		return fmt.Errorf("%w: %s is outside the minimega files directory %s", ErrNotManaged, path, dir)
	case rel == ".":
		return fmt.Errorf("%w: %s is the minimega files directory", ErrNotManaged, path)
	case skipped(rel):
		return fmt.Errorf("%w: %s is in a folder the disk list leaves out", ErrNotManaged, path)
	}

	return nil
}

// within returns path relative to dir, and whether it is inside dir.
func within(dir, path string) (string, bool) {
	if !file.WithinDir(dir, path) {
		return "", false
	}

	rel, err := filepath.Rel(dir, path)

	return rel, err == nil
}

// locate sets where the image is relative to the minimega files directory
// dir.
func locate(image *Details, dir string) {
	image.RelativePath, _ = within(dir, image.FullPath)
	image.OutsideFilesDir = !file.WithinDir(dir, image.FullPath)
	image.ReadOnly = managed(dir, image.FullPath) != nil
}

// openRegular opens path for reading when it is a regular file, without
// blocking on a FIFO or device put in its place.
func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("opening disk image: %w", err)
	}

	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%w: %s is not a regular file", ErrNotManaged, path)
	}

	if err != nil {
		_ = f.Close()

		return nil, err
	}

	return f, nil
}

// usableImage reports whether phenix can read path as a regular file outside
// the system directories.
func usableImage(path string) bool {
	for _, dir := range systemDirs {
		if file.WithinDir(dir, path) {
			plog.Debug(plog.TypeSystem, "not inspecting a disk image in a system directory", "image", path)

			return false
		}
	}

	f, err := openRegular(path)
	if err != nil {
		plog.Debug(plog.TypeSystem, "leaving out a disk image phenix cannot read", "image", path, "err", err)

		return false
	}

	_ = f.Close()

	return true
}

// minimegaMayInspect reports whether minimega may run qemu-img on path, an
// image `file list` reported. qemu-img blocks for good on a FIFO or device,
// and minimega runs one command at a time, so a path phenix sees as something
// other than a regular file, or a symlink to one, is left out. A path phenix
// cannot stat is kept: minimega may see files phenix does not.
func minimegaMayInspect(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return true
	}

	if info.Mode()&fs.ModeSymlink != 0 {
		info, err = os.Stat(path)
	}

	if err != nil || !info.Mode().IsRegular() {
		plog.Debug(plog.TypeSystem, "not inspecting a disk image that is not a regular file", "image", path, "err", err)

		return false
	}

	return true
}

// requireFile returns an error unless path is a regular file (following
// symlinks): one wrapping [fs.ErrNotExist] when it is missing, and ErrNotManaged
// when it is something else.
func requireFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("disk image: %w", err)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrNotManaged, path)
	}

	return nil
}

// imageFiles lists, as paths under dir, the images in the minimega files
// directory dir at any depth down to maxDepth: regular files, and symlinks to
// them, with a known image extension. It leaves out what skipped names and
// does not follow symlinked folders. dir may itself be a symlink. An
// unreadable folder under dir is logged and left out; an unreadable dir fails
// the listing.
func imageFiles(dir string) ([]string, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}

	var paths []string

	err = filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if p == root {
			return err
		}

		rel, relErr := filepath.Rel(root, p)

		switch {
		case relErr != nil:
			return relErr
		case err != nil:
			plog.Warn(plog.TypeSystem, "disk list leaves out a folder it cannot read", "folder", filepath.Join(dir, rel), "err", err)

			return nil
		case skipped(rel), entry.IsDir() && tooDeep(dir, rel):
			if entry.IsDir() {
				return fs.SkipDir
			}

			return nil
		case entry.IsDir(), !knownImage(entry.Name()):
			return nil
		}

		regular := entry.Type().IsRegular()

		if entry.Type()&fs.ModeSymlink != 0 {
			info, err := os.Stat(p)
			regular = err == nil && info.Mode().IsRegular()
		}

		if regular {
			paths = append(paths, filepath.Join(dir, rel))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}

	return paths, nil
}
