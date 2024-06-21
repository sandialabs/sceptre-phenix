package disk

// defines disk API functions
// all path, src, dst arguments should be either absolute paths or relative paths from the mm files directory
type DiskFiles interface {
	// Get list of VM disk images, container filesystems, or both.
	// Assumes disk images have `.qc2` or `.qcow2` extension.
	// Assumes container filesystems have `_rootfs.tgz` suffix.
	// Alternatively, we could force the use of subdirectories w/ known names
	// (such as `base-images` and `container-fs`).
	// Looks in base directory, plus any images that expName references
	// if expName is empty, will check all known experiments 
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

	// makes a copy of `src` at `dst`. This is equivalent to a shell `cp`
	CloneDisk(src, dst string) error
	// renames `src` to `dst`. This is equivalent to a shell `mv`.
	// Note that if this image backs others, they will need to be rebased to the new name (can use unsafe)
	RenameDisk(src, dst string) error
	// deletes `src`. This is equivalent to a shell `rm`.
	// Note that if this image backs others, they will become invalid
	DeleteDisk(src string) error
}

var DefaultDiskFiles DiskFiles = new(MMDiskFiles)

func GetImages(expName string) ([]Details, error) {
	return DefaultDiskFiles.GetImages(expName)
}

func GetImage(path string) (Details, error) {
	return DefaultDiskFiles.GetImage(path)
}

func CommitDisk(path string) error {
	return DefaultDiskFiles.CommitDisk(path)
}

func SnapshotDisk(src, dst string) error {
	return DefaultDiskFiles.SnapshotDisk(src, dst)
}

func RebaseDisk(src, dst string, unsafe bool) error {
	return DefaultDiskFiles.RebaseDisk(src, dst, unsafe)
}

func ResizeDisk(src, size string) error {
	return DefaultDiskFiles.ResizeDisk(src, size)
}

func CloneDisk(src, dst string) error {
	return DefaultDiskFiles.CloneDisk(src, dst)
}

func RenameDisk(src, dst string) error {
	return DefaultDiskFiles.RenameDisk(src, dst)
}

func DeleteDisk(src string) error {
	return DefaultDiskFiles.DeleteDisk(src)
}