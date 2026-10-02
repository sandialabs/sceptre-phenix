package disk

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"phenix/api/experiment"
	"phenix/types"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

// MMDiskFiles runs the disk actions on paths the package's functions have
// already checked: the qemu-img ones through minimega, which applies its own
// backing file naming (-abssnapshot), and the file ones directly.
type MMDiskFiles struct{}

// diskSizeRe validates the size argument passed to `disk resize` before it is
// interpolated into a minimega command. It is anchored so the entire value must
// be an (optionally signed) integer followed by a unit suffix, matching the
// format the web UI allows (see views/Disks.vue).
var diskSizeRe = regexp.MustCompile(`^[+-]?\d+[KMGTPE]$`)

func (MMDiskFiles) CommitDisk(path string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = "disk commit " + path
	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
}

func (MMDiskFiles) SnapshotDisk(src, dst string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("disk snapshot %s %s", src, dst)
	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
}

func (MMDiskFiles) RebaseDisk(src, dst string, unsafe bool) error {
	cmd := mmcli.NewCommand()
	if unsafe {
		cmd.Command = fmt.Sprintf("disk set-backing %s %s", src, dst)
	} else {
		cmd.Command = fmt.Sprintf("disk rebase %s %s", src, dst)
	}

	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
}

func (MMDiskFiles) ResizeDisk(src, size string) error {
	if !diskSizeRe.MatchString(size) {
		return fmt.Errorf("invalid disk size %q: must be an optionally signed integer followed by one of K, M, G, T, P, E", size)
	}

	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("disk resize %s %s", src, size)
	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
}

// CloneDisk copies src to dst, a file it creates, so it never replaces one.
// The copy is not sparse.
func (MMDiskFiles) CloneDisk(src, dst string) error {
	in, err := openRegular(src)
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("cloning %s: %w", src, err)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", ErrExists, dst)
	}

	if err != nil {
		return fmt.Errorf("cloning %s: %w", src, err)
	}

	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		_ = os.Remove(dst)

		return fmt.Errorf("cloning %s to %s: %w", src, dst, err)
	}

	return nil
}

// RenameDisk renames src to dst, never replacing a file at dst.
func (MMDiskFiles) RenameDisk(src, dst string) error {
	err := renameNoReplace(src, dst)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", ErrExists, dst)
	}

	if err != nil {
		return fmt.Errorf("renaming disk image: %w", err)
	}

	return nil
}

func (MMDiskFiles) DeleteDisk(src string) error {
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("deleting disk image: %w", err)
	}

	return nil
}

func (MMDiskFiles) GetImages(expName string) ([]Details, error) {
	// keyed by full path, so an image reached more than once is listed once
	details := make(map[string]Details)

	// the experiment given, or else every experiment
	var experiments []types.Experiment

	if len(expName) > 0 {
		exp, err := experiment.Get(expName)
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve %v: %w", expName, err)
		}

		experiments = []types.Experiment{*exp}
	} else {
		var err error

		experiments, err = experiment.List()
		if err != nil {
			return nil, err
		}
	}

	if LocalInspection() {
		if err := getImagesLocal(experiments, details); err != nil {
			return nil, err
		}
	} else {
		// Add all the files from the minimega files directory
		getAllFiles(details)

		// Add all files defined in the experiment topologies
		for _, exp := range experiments {
			getTopologyFiles(exp, details)
		}
	}

	addExperimentUses(experiments, details)

	dir := mm.GetMMFullPath("")
	images := make([]Details, 0, len(details))

	for _, image := range details {
		locate(&image, dir)
		images = append(images, image)
	}

	return images, nil
}

func (MMDiskFiles) GetImage(path string) (Details, error) {
	path = mm.GetMMFullPath(path)

	var images []Details

	if LocalInspection() {
		entry, err := cachedChain(path)
		if err != nil {
			return Details{}, err
		}

		locked, _ := lockedInodes()
		images = chainDetails(entry, locked)
	} else {
		images = resolveImage(path)
	}

	if len(images) == 0 {
		return Details{}, fmt.Errorf("could not resolve file specified: %s", path)
	}

	locate(&images[0], mm.GetMMFullPath(""))

	return images[0], nil
}

// addExperimentUses records which experiments use each disk: the images their
// topologies name, and the images backing those, all matched by full path.
func addExperimentUses(experiments []types.Experiment, details map[string]Details) {
	for _, exp := range experiments {
		use := ExperimentUse{Name: exp.Metadata.Name, Running: exp.Running()}
		used := make(map[string]bool)

		for _, node := range exp.Spec.Topology().Nodes() {
			for _, drive := range node.Hardware().Drives() {
				if drive.Image() == "" {
					continue
				}

				image, ok := details[mm.GetMMFullPath(drive.Image())]
				if !ok {
					continue
				}

				used[image.FullPath] = true
				for _, backing := range image.BackingImages {
					used[backing] = true
				}
			}
		}

		for path := range used {
			if image, ok := details[path]; ok {
				image.Experiments = append(image.Experiments, use)
				details[path] = image
			}
		}
	}
}

// getImagesLocal is GetImages with images inspected by phenix itself (see
// local.go): the files directory first, then images the experiments use.
func getImagesLocal(experiments []types.Experiment, details map[string]Details) error {
	paths, err := imageFiles(mm.GetMMFullPath(""))
	if err != nil {
		return err
	}

	// a topology names its images explicitly, so any extension is listed
	for _, exp := range experiments {
		for _, node := range exp.Spec.Topology().Nodes() {
			for _, drive := range node.Hardware().Drives() {
				if path := drive.Image(); path != "" {
					paths = append(paths, mm.GetMMFullPath(path))
				}
			}
		}
	}

	resolveLocal(util.Unique(paths), details)

	return nil
}

// getAllFiles adds the images in the minimega files directory, as minimega
// lists them, down to maxDepth: one non-recursive `file list` per folder,
// because minimega stops a recursive listing at its first unreadable entry.
// Like imageFiles, it does not follow symlinked folders, which minimega lists
// as files, and it leaves out what is not a regular file (see
// minimegaMayInspect).
func getAllFiles(details map[string]Details) {
	for folders := []string{""}; len(folders) > 0; folders = folders[1:] {
		cmd := mmcli.NewCommand()
		cmd.Command = strings.TrimSpace("file list " + folders[0])

		for _, row := range mmcli.RunTabular(cmd) {
			name := row["name"]

			switch {
			case name == "" || skipped(name):
				continue
			case row["dir"] != "":
				if listable(name) && !tooDeep(mm.GetMMFullPath(""), name) {
					folders = append(folders, name)
				}
			case knownImage(name):
				if path := mm.GetMMFullPath(name); minimegaMayInspect(path) {
					addResolved(details, path)
				}
			}
		}
	}
}

// listable reports whether folder, relative to the minimega files directory,
// can be passed to `file list` as one literal path: minimega would also read a
// scheme (such as "file:" or "http:") in a relative path as a URL to fetch.
func listable(folder string) bool {
	first, _, _ := strings.Cut(folder, "/")

	if !minimegaSafe(folder) || strings.Contains(first, ":") {
		plog.Debug(plog.TypeSystem, "disk list leaves out a folder minimega cannot list", "folder", folder)

		return false
	}

	return true
}

// getTopologyFiles adds the images the experiment's topology names, of any
// extension, that phenix can read (see usableImage).
func getTopologyFiles(exp types.Experiment, details map[string]Details) {
	for _, node := range exp.Spec.Topology().Nodes() {
		for _, drive := range node.Hardware().Drives() {
			if len(drive.Image()) == 0 {
				continue
			}

			if path := mm.GetMMFullPath(drive.Image()); usableImage(path) {
				addResolved(details, path)
			}
		}
	}
}

// addResolved adds the image at path and the images backing it, as minimega
// inspects them, unless path is listed already; an image listed already is
// kept.
func addResolved(details map[string]Details, path string) {
	if _, ok := details[path]; ok {
		return
	}

	for _, image := range resolveImage(path) {
		if _, ok := details[image.FullPath]; !ok {
			details[image.FullPath] = image
		}
	}
}

// resolveImage inspects the image at path, a cleaned absolute path, and the
// images backing it through minimega.
func resolveImage(path string) []Details {
	if !minimegaSafe(path) {
		plog.Warn(plog.TypeSystem, "minimega cannot inspect a disk image with this name", "image", path)

		return nil
	}

	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("disk info %v recursive", path)
	images := mmcli.RunTabular(cmd)

	// minimega reports a backing file as its overlay's directory joined with
	// the stored name, so it can hold ".." segments
	paths := make([]string, len(images))
	for i, row := range images {
		if i == 0 {
			paths[i] = filepath.Clean(row["image"])
		} else {
			paths[i] = mm.CleanBackingPath(row["image"])
		}
	}

	imageDetails := make([]Details, 0, len(images))

	for i, row := range images {
		image := Details{ //nolint:exhaustruct // partial initialization
			Name:          filepath.Base(paths[i]),
			FullPath:      paths[i],
			Size:          row["disksize"],
			VirtualSize:   row["virtualsize"],
			BackingImages: []string{},
		}

		switch {
		case row["format"] == "qcow2":
			image.Kind = VMImage
			image.BackingImages = slices.Clone(paths[i+1:])
		case strings.HasSuffix(image.Name, "_rootfs.tgz"):
			image.Kind = ContainerImage
		case strings.HasSuffix(image.Name, ".hdd"):
			image.Kind = VMImage
		case strings.HasSuffix(image.Name, ".iso"):
			image.Kind = ISOImage
		default:
			image.Kind = UNKNOWN
		}

		var err error

		image.InUse, err = strconv.ParseBool(row["inuse"])
		if err != nil {
			plog.Warn(plog.TypeSystem, "could not determine if image in use", "image", path)
		}

		imageDetails = append(imageDetails, image)
	}

	return imageDetails
}
