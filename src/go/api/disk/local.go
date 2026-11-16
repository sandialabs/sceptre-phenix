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

	"phenix/util/diskchain"
	"phenix/util/mm"
	"phenix/util/plog"
)

// Listing disks through minimega runs one `disk info` (a qemu-img process) per image while holding the shared minimega
// connection, so when qemu-img is installed phenix inspects images in the files directory itself: in parallel, and only
// those that changed since they were last inspected.

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
// the chain, and what the header of each says about its format. For a chain
// that qemu cannot open, broken is the walk that found why, and stamps also
// hold the files it read and the folder of a missing file.
type cacheEntry struct {
	chain   []qemuImage
	stamps  []fileStamp
	paths   []string
	headers []imageHeader
	broken  diskchain.Result
}

// ClearCache forgets every inspected image, so the next listing and each
// details request (see Inspect) inspect them again.
func ClearCache() {
	cacheMu.Lock()
	imageCache = make(map[string]cacheEntry)
	cacheMu.Unlock()

	inspectMu.Lock()
	inspections = make(map[string]inspectionEntry)
	inspectMu.Unlock()
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

	out, err := exec.CommandContext(ctx, "qemu-img", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("qemu-img info %s: %s", path, strings.TrimSpace(string(exitErr.Stderr)))
		}

		return nil, fmt.Errorf("qemu-img info %s: %w", path, err)
	}

	return out, nil
}

// inspectChain runs qemu-img on the image and its backing chain, falling back
// like minimega to the image alone, reported incomplete, if a backing file fails.
func inspectChain(path string) ([]qemuImage, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), qemuImgTimeout)
	defer cancel()

	out, chainErr := runQemuImgInfo(ctx, path, true)
	if chainErr == nil {
		var chain []qemuImage
		if err := json.Unmarshal(out, &chain); err != nil {
			return nil, false, fmt.Errorf("parsing qemu-img info for %s: %w", path, err)
		}

		if len(chain) > 0 {
			return chain, true, nil
		}

		chainErr = errors.New("empty backing chain")
	}

	out, err := runQemuImgInfo(ctx, path, false)
	if err != nil {
		return nil, false, err
	}

	var image qemuImage
	if err := json.Unmarshal(out, &image); err != nil {
		return nil, false, fmt.Errorf("parsing qemu-img info for %s: %w", path, err)
	}

	plog.Warn(plog.TypeSystem, "showing image without its backing chain", "image", path, "err", chainErr)

	return []qemuImage{image}, false, nil
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

// cachedChain returns the image's chain and stamps, inspecting it again only once
// it or its backing files changed. An incomplete chain is cached only when it is
// broken, until a file it read or the folder of the missing file changes.
func cachedChain(path string) (cacheEntry, error) {
	cacheMu.Lock()
	entry, ok := imageCache[path]
	cacheMu.Unlock()

	if ok && stampsCurrent(entry.stamps) {
		return entry, nil
	}

	chain, complete, err := inspectChain(path)
	if err != nil {
		return cacheEntry{}, err
	}

	entry = cacheEntry{ //nolint:exhaustruct // addBreak sets broken
		chain:   chain,
		stamps:  make([]fileStamp, 0, len(chain)),
		paths:   make([]string, 0, len(chain)),
		headers: make([]imageHeader, 0, len(chain)),
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

		entry.headers = append(entry.headers, readHeader(stamp.path, entry.paths[i]))
	}

	cacheable := complete
	if !complete {
		cacheable = entry.addBreak(path)
	}

	cacheMu.Lock()
	if cacheable {
		imageCache[path] = entry
	} else {
		delete(imageCache, path)
	}
	cacheMu.Unlock()

	return entry, nil
}

// addBreak records in e why qemu cannot open the chain of the image at path,
// which qemu-img could not read to its base. It reports whether e can be
// cached: brokenChain found why, and stamps tell when the files it read
// change, including the folder in which a missing file would appear.
func (e *cacheEntry) addBreak(path string) bool {
	res, ok := brokenChain(path)
	if !ok {
		return false
	}

	e.broken = res

	watched := slices.Clone(res.Members)
	if res.Problem == diskchain.Missing {
		watched = append(watched, filepath.Dir(res.At))
	}

	for _, p := range watched {
		if p == path {
			continue
		}

		stamp, err := stat(p)
		if err != nil {
			return false
		}

		e.stamps = append(e.stamps, stamp)
	}

	return true
}

// brokenChain walks the backing chain of the image at path and returns the
// walk, its paths cleaned (see mm.CleanBackingPath), when qemu cannot open the
// image: the chain is broken, or qemu refuses an image of it (see
// diskchain.Result.Broken and diskchain.Result.Refused).
func brokenChain(path string) (diskchain.Result, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), qemuImgTimeout)
	defer cancel()

	res := diskchain.Walk(ctx, path)
	if !res.Broken() && !res.Refused() {
		return diskchain.Result{}, false
	}

	return res.MapPaths(mm.CleanBackingPath), true
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

// lockedInodes returns the inodes of files with a lock held on them (the files open in a running VM), as minimega decides
// an image is in use. The device is left out on purpose: on a btrfs subvolume /proc/locks prints a different device than
// stat reports, and matching it would show an image in use as free.
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
		case qcowFormat(image.Format):
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

		details.setFormat(image.Format, entry.headers[i])

		images = append(images, details)
	}

	setRefusedBackings(images, entry.headers)

	if len(images) > 0 {
		images[0].setChain(entry.broken)
	}

	return images
}

func knownImage(path string) bool {
	return ImageExtension(path) != ""
}

// ImageExtension returns the known image extension that name ends in, such as
// .qcow2 or _rootfs.tgz, or "" when it ends in none.
func ImageExtension(name string) string {
	for _, ext := range knownImageExtensions {
		if strings.HasSuffix(name, ext) {
			return ext
		}
	}

	return ""
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

				if image, ok := refusedImage(path, locked); ok {
					results[i] = []Details{image}
				}

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
