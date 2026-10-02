package disk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"phenix/util/mm"
	"phenix/util/plog"
)

// Listing disks through minimega runs one `disk info` (a qemu-img process) per
// image, one after another, while holding the shared minimega connection that
// every other phenix request needs. phenix already reads and writes the
// minimega files directory itself (disk uploads), so when qemu-img is
// installed it inspects images directly instead: in parallel, and only for
// images that changed since they were last inspected.

const (
	// qemuImgTimeout bounds a single image inspection.
	qemuImgTimeout = 30 * time.Second

	// maxQemuImgWorkers caps parallel inspections, so a rescan of many images
	// does not swamp the head node.
	maxQemuImgWorkers = 8
)

var (
	errNoQemuImg = errors.New("qemu-img not found")

	cacheMu    sync.Mutex                    //nolint:gochecknoglobals // guards imageCache
	imageCache = make(map[string]cacheEntry) //nolint:gochecknoglobals // inspected images by path
)

// qemuImage is the part of `qemu-img info --output=json` phenix uses.
type qemuImage struct {
	Filename    string `json:"filename"`
	Format      string `json:"format"`
	VirtualSize int64  `json:"virtual-size"`
	ActualSize  int64  `json:"actual-size"`
}

// fileStamp identifies a version of a file: any write changes its size or
// modification time.
type fileStamp struct {
	path    string
	size    int64
	modTime time.Time
	inode   uint64
}

// cacheEntry is an image's backing chain, the image first, as last inspected,
// with a stamp and the cleaned path (see mm.CleanBackingPath) of every file in
// the chain.
type cacheEntry struct {
	chain  []qemuImage
	stamps []fileStamp
	paths  []string
}

// ClearCache forgets every inspected image, so the next listing inspects them
// all again.
func ClearCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	imageCache = make(map[string]cacheEntry)
}

// LocalInspection reports whether phenix inspects images itself (and caches
// the results) rather than through minimega.
func LocalInspection() bool {
	_, err := exec.LookPath("qemu-img")

	return err == nil
}

func runQemuImgInfo(ctx context.Context, path string, chain bool) ([]byte, error) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return nil, errNoQemuImg
	}

	// -U (force share) reads images that running VMs hold locked
	args := []string{"info", "-U", "--output=json"}
	if chain {
		args = append(args, "--backing-chain")
	}

	args = append(args, path)

	out, err := exec.CommandContext(ctx, "qemu-img", args...).Output() //nolint:gosec // path is an image path, not a shell string
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("qemu-img info %s: %s", path, strings.TrimSpace(string(exitErr.Stderr)))
		}

		return nil, fmt.Errorf("qemu-img info %s: %w", path, err)
	}

	return out, nil
}

// inspectChain runs qemu-img on the image and its backing chain. Like
// minimega, it falls back to the image alone when a backing file is unreadable.
func inspectChain(path string) ([]qemuImage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), qemuImgTimeout)
	defer cancel()

	out, chainErr := runQemuImgInfo(ctx, path, true)
	if chainErr == nil {
		var chain []qemuImage
		if err := json.Unmarshal(out, &chain); err != nil {
			return nil, fmt.Errorf("parsing qemu-img info for %s: %w", path, err)
		}

		if len(chain) > 0 {
			return chain, nil
		}

		chainErr = errors.New("empty backing chain")
	}

	out, err := runQemuImgInfo(ctx, path, false)
	if err != nil {
		return nil, err
	}

	var image qemuImage
	if err := json.Unmarshal(out, &image); err != nil {
		return nil, fmt.Errorf("parsing qemu-img info for %s: %w", path, err)
	}

	plog.Warn(plog.TypeSystem, "showing image without its backing chain", "image", path, "err", chainErr)

	return []qemuImage{image}, nil
}

func stat(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, fmt.Errorf("stat %s: %w", path, err)
	}

	stamp := fileStamp{path: path, size: info.Size(), modTime: info.ModTime(), inode: 0}
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		stamp.inode = sys.Ino
	}

	return stamp, nil
}

// cachedChain returns the image's chain and stamps, inspecting the image only
// when it or a file it is backed by changed since the last inspection.
func cachedChain(path string) (cacheEntry, error) {
	cacheMu.Lock()
	entry, ok := imageCache[path]
	cacheMu.Unlock()

	if ok && stampsCurrent(entry.stamps) {
		return entry, nil
	}

	chain, err := inspectChain(path)
	if err != nil {
		return cacheEntry{}, err
	}

	entry = cacheEntry{
		chain:  chain,
		stamps: make([]fileStamp, 0, len(chain)),
		paths:  make([]string, 0, len(chain)),
	}

	for i, image := range chain {
		stamp, err := stat(chainPath(path, image.Filename))
		if err != nil {
			return cacheEntry{}, err
		}

		entry.stamps = append(entry.stamps, stamp)

		// qemu-img reports a backing file as its overlay's directory joined with
		// the stored name, so it can hold ".." segments; the image itself is
		// reported as given
		if i == 0 {
			entry.paths = append(entry.paths, stamp.path)
		} else {
			entry.paths = append(entry.paths, mm.CleanBackingPath(stamp.path))
		}
	}

	cacheMu.Lock()
	imageCache[path] = entry
	cacheMu.Unlock()

	return entry, nil
}

// chainPath resolves a filename qemu-img reported, which is relative to the
// image's directory when the image or its backing reference was relative.
func chainPath(image, name string) string {
	if filepath.IsAbs(name) {
		return name
	}

	return filepath.Join(filepath.Dir(image), name)
}

func stampsCurrent(stamps []fileStamp) bool {
	for _, old := range stamps {
		current, err := stat(old.path)
		if err != nil || current != old {
			return false
		}
	}

	return true
}

// lockedInodes returns the inodes of files with a lock held on them: the files
// open in a running VM. It is how minimega decides an image is in use. The
// device is left out on purpose: /proc/locks prints the filesystem's device,
// which on a btrfs subvolume differs from the one stat reports, so matching it
// would show an image in use as free. A locked file on another device with the
// same inode shows an image in use instead, which only holds back its actions.
func lockedInodes() (map[uint64]bool, error) {
	file, err := os.Open("/proc/locks")
	if err != nil {
		return nil, fmt.Errorf("reading file locks: %w", err)
	}

	defer func() { _ = file.Close() }()

	locked := make(map[uint64]bool)

	// e.g. "1: POSIX  ADVISORY  WRITE 1234 08:02:5678 0 EOF"; the MAJ:MIN:INODE
	// field is the only one with two colons
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		for field := range strings.FieldsSeq(scanner.Text()) {
			parts := strings.Split(field, ":")
			if len(parts) != 3 { //nolint:mnd // MAJ:MIN:INODE
				continue
			}

			if inode, err := strconv.ParseUint(parts[2], 10, 64); err == nil {
				locked[inode] = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading file locks: %w", err)
	}

	return locked, nil
}

// humanReadableBytes formats sizes the way minimega's `disk info` does.
func humanReadableBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}

	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// chainDetails turns an inspected chain into Details for the image and each
// image backing it, as resolveImage does with minimega's output.
func chainDetails(entry cacheEntry, locked map[uint64]bool) []Details {
	images := make([]Details, 0, len(entry.chain))

	for i, image := range entry.chain {
		path := entry.paths[i]
		details := Details{ //nolint:exhaustruct // partial initialization
			Name:          filepath.Base(path),
			FullPath:      path,
			Size:          humanReadableBytes(image.ActualSize),
			VirtualSize:   humanReadableBytes(image.VirtualSize),
			BackingImages: []string{},
			InUse:         locked[entry.stamps[i].inode],
		}

		switch {
		case image.Format == "qcow2":
			details.Kind = VMImage

			details.BackingImages = slices.Clone(entry.paths[i+1:])
		case strings.HasSuffix(details.Name, "_rootfs.tgz"):
			details.Kind = ContainerImage
		case strings.HasSuffix(details.Name, ".hdd"):
			details.Kind = VMImage
		case strings.HasSuffix(details.Name, ".iso"):
			details.Kind = ISOImage
		default:
			details.Kind = UNKNOWN
		}

		images = append(images, details)
	}

	return images
}

func knownImage(path string) bool {
	for _, ext := range knownImageExtensions {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}

	return false
}

// resolveLocal adds to details the Details of each image path and the images
// backing it, keyed by full path; a path already in details, or reported for
// an earlier path, is kept, as in GetImages. Paths that are not usable images
// (see usableImage) are left out without running qemu-img, and images that
// cannot be inspected are logged and left out.
func resolveLocal(paths []string, details map[string]Details) {
	locked, err := lockedInodes()
	if err != nil {
		plog.Warn(plog.TypeSystem, "cannot tell which disks are in use", "err", err)
	}

	results := make([][]Details, len(paths))

	var (
		wg      sync.WaitGroup
		workers = make(chan struct{}, min(runtime.NumCPU(), maxQemuImgWorkers))
	)

	for i, path := range paths {
		wg.Add(1)

		go func() {
			defer wg.Done()

			workers <- struct{}{}
			defer func() { <-workers }()

			// experiments may name images that were never uploaded, or any file
			if !usableImage(path) {
				return
			}

			entry, err := cachedChain(path)
			if err != nil {
				plog.Warn(plog.TypeSystem, "cannot inspect disk image", "image", path, "err", err)

				return
			}

			results[i] = chainDetails(entry, locked)
		}()
	}

	wg.Wait()

	for _, images := range results {
		for _, image := range images {
			if _, ok := details[image.FullPath]; !ok {
				details[image.FullPath] = image
			}
		}
	}
}
