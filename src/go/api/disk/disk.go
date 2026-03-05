package disk

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

type MMDiskFiles struct{}

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
	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("disk resize %s %s", src, size)
	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
}

func (MMDiskFiles) CloneDisk(src, dst string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("shell cp %s %s", src, dst)
	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
}

func (MMDiskFiles) RenameDisk(src, dst string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("shell mv %s %s", src, dst)
	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
}

func (MMDiskFiles) DeleteDisk(src string) error {
	cmd := mmcli.NewCommand()
	cmd.Command = "shell rm " + src
	_, err := mmcli.SingleDataResponse(mmcli.Run(cmd))

	return err
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

	images := resolveImage(path)
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
		plog.Debug(plog.TypeSystem, "file didn't match know image extensions: %s", "path", path)

		return imageDetails
	}

	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("disk info %v recursive", path)
	images := mmcli.RunTabular(cmd)

	for i, row := range images {
		image := Details{ //nolint:exhaustruct // partial initialization
			Name:          filepath.Base(row["image"]),
			FullPath:      row["image"],
			Size:          row["disksize"],
			VirtualSize:   row["virtualsize"],
			BackingImages: []string{},
		}

		switch {
		case row["format"] == "qcow2":
			image.Kind = VMImage

			backingChain := []string{}
			for _, backing := range images[i+1:] {
				backingChain = append(backingChain, filepath.Base(backing["image"]))
			}

			image.BackingImages = backingChain
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
