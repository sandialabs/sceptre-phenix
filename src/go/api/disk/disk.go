package disk

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

type MMDiskFiles struct{}

// diskSizeRe validates the size argument passed to `disk resize` before it is
// interpolated into a minimega command. It is anchored so the entire value must
// be an (optionally signed) integer followed by a unit suffix, matching the
// format the web UI allows (see views/Disks.vue).
var diskSizeRe = regexp.MustCompile(`^[+-]?\d+[KMGTPE]$`)

// quoteArg quotes s as one minimega argument, escaping what minicli reads back,
// so spaces, quotes, and '#' in a path cannot split it or end the command.
func quoteArg(s string) string {
	return `"` + strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	).Replace(s) + `"`
}

// runDiskCommand runs a minimega command that answers with no data.
func runDiskCommand(command string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = command
	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
}

func commitCommand(path string) string { return "disk commit " + quoteArg(path) }

func snapshotCommand(src, dst string) string {
	return fmt.Sprintf("disk snapshot %s %s", quoteArg(src), quoteArg(dst))
}

func rebaseCommand(src, dst string, unsafe bool) string {
	command := "disk rebase " + quoteArg(src)
	if unsafe {
		command = "disk set-backing " + quoteArg(src)
	}

	// No backing file makes src standalone.
	if dst != "" {
		command += " " + quoteArg(dst)
	}

	return command
}

func resizeCommand(src, size string) string {
	return fmt.Sprintf("disk resize %s %s", quoteArg(src), size)
}

func cloneCommand(src, dst string) string {
	return fmt.Sprintf("shell cp %s %s", quoteArg(src), quoteArg(dst))
}

func renameCommand(src, dst string) string {
	return fmt.Sprintf("shell mv %s %s", quoteArg(src), quoteArg(dst))
}

func deleteCommand(src string) string { return "shell rm " + quoteArg(src) }

func infoCommand(path string) string {
	return fmt.Sprintf("disk info %s recursive", quoteArg(path))
}

func (MMDiskFiles) CommitDisk(path string) error {
	return runDiskCommand(commitCommand(path))
}

func (MMDiskFiles) SnapshotDisk(src, dst string) error {
	return runDiskCommand(snapshotCommand(src, dst))
}

func (MMDiskFiles) RebaseDisk(src, dst string, unsafe bool) error {
	return runDiskCommand(rebaseCommand(src, dst, unsafe))
}

func (MMDiskFiles) ResizeDisk(src, size string) error {
	if !diskSizeRe.MatchString(size) {
		return fmt.Errorf("invalid disk size %q: must be an optionally signed integer followed by one of K, M, G, T, P, E", size)
	}

	return runDiskCommand(resizeCommand(src, size))
}

func (MMDiskFiles) CloneDisk(src, dst string) error {
	return runDiskCommand(cloneCommand(src, dst))
}

func (MMDiskFiles) RenameDisk(src, dst string) error {
	return runDiskCommand(renameCommand(src, dst))
}

func (MMDiskFiles) DeleteDisk(src string) error {
	return runDiskCommand(deleteCommand(src))
}

func (MMDiskFiles) GetImages(expName string) ([]Details, error) {
	// Using a map here to weed out duplicates.
	details := make(map[string]Details)

	// Add all the files from the minimega files directory
	getAllFiles(details)

	// Add all files defined in the experiment topology if given; otherwise check all experiments
	if len(expName) > 0 {
		err := getTopologyFiles(expName, details)
		if err != nil {
			return nil, err
		}
	} else {
		experiments, err := experiment.List()
		if err != nil {
			return nil, err
		}

		for _, exp := range experiments {
			err = getTopologyFiles(exp.Metadata.Name, details)
			if err != nil {
				return nil, err
			}
		}
	}

	var images []Details
	for name := range details {
		images = append(images, details[name])
	}

	return images, nil
}

func (MMDiskFiles) GetImage(path string) (Details, error) {
	if !filepath.IsAbs(path) {
		path = mm.GetMMFullPath(path)
	}

	// Unlike a listing, a disk named in a request is resolved whatever its extension.
	images := imageChain(path)
	if len(images) == 0 {
		return Details{}, fmt.Errorf("could not resolve file specified: %s", path)
	}

	return images[0], nil
}

// Get all image files from the minimega files directory.
func getAllFiles(details map[string]Details) {
	// First, get file listings from cluster nodes.
	cmd := mmcli.NewCommand()
	cmd.Command = "file list"

	for _, row := range mmcli.RunTabular(cmd) {
		if _, ok := details[row["name"]]; row["dir"] == "" && !ok {
			for _, image := range resolveImage(mm.GetMMFullPath(row["name"])) {
				if _, ok2 := details[image.Name]; !ok2 {
					details[image.Name] = image
				}
			}
		}
	}
}

// Retrieves all the unique image names defined in the topology.
func getTopologyFiles(expName string, details map[string]Details) error {
	// Retrieve the experiment
	exp, err := experiment.Get(expName)
	if err != nil {
		return fmt.Errorf("unable to retrieve %v", expName)
	}

	for _, node := range exp.Spec.Topology().Nodes() {
		for _, drive := range node.Hardware().Drives() {
			if len(drive.Image()) == 0 {
				continue
			}

			path := drive.Image()
			if !filepath.IsAbs(path) {
				path = mm.GetMMFullPath(path)
			}

			if _, ok := details[filepath.Base(path)]; !ok {
				for _, image := range resolveImage(path) {
					if _, ok2 := details[image.Name]; !ok2 {
						details[image.Name] = image
					}
				}
			}
		}
	}

	return nil
}

func resolveImage(path string) []Details {
	imageDetails := []Details{}

	knownFormat := false

	for _, f := range knownImageExtensions {
		if strings.HasSuffix(path, f) {
			knownFormat = true

			break
		}
	}

	if !knownFormat {
		// Only log non-snapshot files
		if !strings.Contains(path, "_snapshot") {
			plog.Debug(plog.TypeSystem, "file didn't match known image extensions", "path", path, "knownExtensions", knownImageExtensions)
		}

		return imageDetails
	}

	return imageChain(path)
}

// imageChain returns the details of the image at path and of each image in its
// backing chain, as minimega's disk info reports them.
func imageChain(path string) []Details {
	cmd := mmcli.NewCommand()
	cmd.Command = infoCommand(path)
	images := mmcli.RunTabular(cmd)

	imageDetails := make([]Details, 0, len(images))

	for i, row := range images {
		image := Details{ //nolint:exhaustruct // partial initialization
			Name:          filepath.Base(row["image"]),
			FullPath:      row["image"],
			Size:          row["disksize"],
			VirtualSize:   row["virtualsize"],
			BackingImages: []string{},
			Format:        row["format"],
		}

		for _, backing := range images[i+1:] {
			image.BackingPaths = append(image.BackingPaths, backing["image"])
			image.BackingFormats = append(image.BackingFormats, backing["format"])
		}

		switch {
		case row["format"] == "qcow2":
			image.Kind = VMImage

			for _, backing := range images[i+1:] {
				image.BackingImages = append(image.BackingImages, filepath.Base(backing["image"]))
			}
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
