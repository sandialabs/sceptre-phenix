package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"phenix/util/mm"
)

// DiskFiles defines disk API functions. The package's functions check every
// path before calling them (see Resolve), so the actions only receive cleaned
// absolute paths inside the minimega files directory.
type DiskFiles interface {
	// Get list of VM disk images, container filesystems, ISOs, and the other
	// images topologies name (see GetImages).
	// Looks in the minimega files directory and its folders, plus any images
	// that expName references; if expName is empty, will check all known
	// experiments.
	GetImages(expName string) ([]Details, error)
	// Gets a single image
	GetImage(path string) (Details, error)

	// commits a qcow2. This writes the contents of the disk at `path` to its backing file.
	CommitDisk(path string) error
	// creates a snapshot `dst` of the disk at `src`. `dst` will then be a new image backed by `src`
	SnapshotDisk(src, dst string) error
	// rebases disk at `src` onto `dst`. Any difference between the old backing file and `dst` is written to `src`
	// when `unsafe` is true, will only change the reference to the backing file rather than actually moving contents
	// dst can be left blank to make `src` into an independent image
	RebaseDisk(src, dst string, unsafe bool) error
	// resizes the specified disk.
	// size is suffixed with one of "K,M,G,T,P,E" and can be absolute or relative with a +/-
	// for example: "50G" or "-500M"
	ResizeDisk(src, size string) error

	// makes a copy of `src` at `dst`, a new file.
	CloneDisk(src, dst string) error
	// renames `src` to `dst`, which must not exist.
	// Note that if this image backs others, they will need to be rebased to the new name (can use unsafe)
	RenameDisk(src, dst string) error
	// deletes `src`.
	// Note that if this image backs others, they will become invalid
	DeleteDisk(src string) error
}

var DefaultDiskFiles DiskFiles = new(MMDiskFiles) //nolint:gochecknoglobals // default implementation

// GetImages lists the disk images: those under the minimega files directory
// at any depth (except in the folders Resolve refuses), the images the
// experiment expName (or, when it is "", every experiment) names wherever
// they are, and the images backing any of them.
func GetImages(expName string) ([]Details, error) {
	return DefaultDiskFiles.GetImages(expName)
}

// GetImage inspects the image at path, relative to the minimega files
// directory or absolute, which must be a regular file.
func GetImage(path string) (Details, error) {
	path = mm.GetMMFullPath(path)

	if err := requireFile(path); err != nil {
		return Details{}, err
	}

	return DefaultDiskFiles.GetImage(path)
}

// CommitDisk writes the image at path into its backing image. Both must be
// images the disk actions act on (see Resolve).
func CommitDisk(path string) error {
	path, err := minimegaImage(path)
	if err != nil {
		return err
	}

	info, err := DefaultDiskFiles.GetImage(path)
	if err != nil {
		return err
	}

	if len(info.BackingImages) == 0 {
		return fmt.Errorf("image %s has no backing image to commit to", path)
	}

	if _, err := Resolve(info.BackingImages[0]); err != nil {
		return fmt.Errorf("committing into the backing image: %w", err)
	}

	return DefaultDiskFiles.CommitDisk(path)
}

func SnapshotDisk(src, dst string) error {
	src, err := minimegaImage(src)
	if err != nil {
		return err
	}

	dst, err = newImage(dst)
	if err != nil {
		return err
	}

	return DefaultDiskFiles.SnapshotDisk(src, dst)
}

// RebaseDisk rebases the image at src onto the image at dst, or makes it
// independent when dst is "".
func RebaseDisk(src, dst string, unsafe bool) error {
	src, err := minimegaImage(src)
	if err != nil {
		return err
	}

	if dst != "" {
		if dst, err = minimegaImage(dst); err != nil {
			return err
		}
	}

	return DefaultDiskFiles.RebaseDisk(src, dst, unsafe)
}

func ResizeDisk(src, size string) error {
	src, err := minimegaImage(src)
	if err != nil {
		return err
	}

	return DefaultDiskFiles.ResizeDisk(src, size)
}

func CloneDisk(src, dst string) error {
	src, err := Resolve(src)
	if err != nil {
		return err
	}

	if err := requireFile(src); err != nil {
		return err
	}

	dst, err = newImage(dst)
	if err != nil {
		return err
	}

	return DefaultDiskFiles.CloneDisk(src, dst)
}

func RenameDisk(src, dst string) error {
	src, err := existingFile(src)
	if err != nil {
		return err
	}

	dst, err = newImage(dst)
	if err != nil {
		return err
	}

	return DefaultDiskFiles.RenameDisk(src, dst)
}

func DeleteDisk(src string) error {
	src, err := existingFile(src)
	if err != nil {
		return err
	}

	return DefaultDiskFiles.DeleteDisk(src)
}

// minimegaImage resolves path (see Resolve) for an action minimega runs on
// it: minimega must be able to take it as an argument, and it must be a
// regular file.
func minimegaImage(path string) (string, error) {
	path, err := Resolve(path)
	if err != nil {
		return "", err
	}

	if err := ValidateMinimegaPath(path); err != nil {
		return "", err
	}

	if err := requireFile(path); err != nil {
		return "", err
	}

	return path, nil
}

// existingFile resolves path (see Resolve), which must exist and not be a
// folder. Any name is accepted, as phenix acts on it itself.
func existingFile(path string) (string, error) {
	path, err := Resolve(path)
	if err != nil {
		return "", err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("disk image: %w", err)
	}

	if info.IsDir() {
		return "", fmt.Errorf("%w: %s is a folder", ErrNotManaged, path)
	}

	return path, nil
}

// newImage resolves dst (see Resolve) for an image an action creates:
// minimega must be able to take it as an argument, it must not exist yet, and
// its folder must.
func newImage(dst string) (string, error) {
	dst, err := Resolve(dst)
	if err != nil {
		return "", err
	}

	if err := ValidateMinimegaPath(dst); err != nil {
		return "", err
	}

	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("%w: %s", ErrExists, dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("new disk image: %w", err)
	}

	info, err := os.Stat(filepath.Dir(dst))
	if err != nil {
		return "", fmt.Errorf("folder for the new disk image: %w", err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s is not a folder", ErrNotManaged, filepath.Dir(dst))
	}

	return dst, nil
}
