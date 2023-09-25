package cluster

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"phenix/api/experiment"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
)

type ImageKind uint8
type CopyStatus func(float64)

const (
	UNKNOWN ImageKind = 1 << iota
	VM_IMAGE
	CONTAINER_IMAGE
	ISO_IMAGE
)

type ImageDetails struct {
	Kind     ImageKind
	Name     string
	FullPath string
	Size     int
}

var DefaultClusterFiles ClusterFiles = new(MMClusterFiles)
var mmFilesDirectory = util.GetMMFilesDirectory()

type ClusterFiles interface {
	// Get list of VM disk images, container filesystems, or both.
	// Assumes disk images have `.qc2` or `.qcow2` extension.
	// Assumes container filesystems have `_rootfs.tgz` suffix.
	// Alternatively, we could force the use of subdirectories w/ known names
	// (such as `base-images` and `container-fs`).
	GetImages(expName string, kind ImageKind) ([]ImageDetails, error)

	GetExperimentFileNames(exp string) ([]string, error)

	// Looks in experiment directory on each cluster node for matching filenames
	// that end in both `.SNAP` and `.qc2`.
	GetExperimentSnapshots(exp string) ([]string, error)

	// Should leverage meshage and iomeshage to make a `file` API call on the
	// destination cluster node for the given path.
	CopyFile(path, dest string, status CopyStatus) error

	// Should leverage meshage and iomeshage to make a `file get` API call on all
	// mesh nodes for the given path.
	SyncFile(path string, status CopyStatus) error

	DeleteFile(path string) error
}

func GetImages(expName string, kind ImageKind) ([]ImageDetails, error) {
	return DefaultClusterFiles.GetImages(expName, kind)
}

func GetExperimentFileNames(exp string) ([]string, error) {
	return DefaultClusterFiles.GetExperimentFileNames(exp)
}

func GetExperimentSnapshots(exp string) ([]string, error) {
	return DefaultClusterFiles.GetExperimentSnapshots(exp)
}

func CopyFile(path, dest string, status CopyStatus) error {
	return DefaultClusterFiles.CopyFile(path, dest, status)
}

func SyncFile(path string, status CopyStatus) error {
	return DefaultClusterFiles.SyncFile(path, status)
}

func DeleteFile(path string) error {
	return DefaultClusterFiles.DeleteFile(path)
}

type MMClusterFiles struct{}

func (MMClusterFiles) GetImages(expName string, kind ImageKind) ([]ImageDetails, error) {
	// Using a map here to weed out duplicates.
	details := make(map[string]ImageDetails)

	// Add all the files from the minimega files directory
	if err := getAllFiles(details); err != nil {
		return nil, err
	}

	// Add all files defined in the experiment topology
	// if an experiment name was given
	if len(expName) > 0 {
		if err := getTopologyFiles(expName, details); err != nil {
			return nil, err
		}
	}

	var images []ImageDetails

	for name := range details {
		// Only return image types that were requested
		if kind & details[name].Kind == 0 {
			continue
		}

		images = append(images, details[name])
	}

	return images, nil
}

func (MMClusterFiles) GetExperimentFileNames(exp string) ([]string, error) {
	// Using a map here to weed out duplicates.
	matches := make(map[string]struct{})

	// First get file listings from mesh, then from headnode.
	commands := []string{"mesh send all file list", "file list"}

	cmd := mmcli.NewCommand()

	for _, command := range commands {
		cmd.Command = command

		for _, row := range mmcli.RunTabular(cmd) {
			// Only looking for files.
			if row["dir"] != "" {
				continue
			}

			name := filepath.Base(row["name"])
			matches[name] = struct{}{}
		}
	}

	var files []string

	for f := range matches {
		files = append(files, f)
	}

	return files, nil
}

func (MMClusterFiles) GetExperimentSnapshots(exp string) ([]string, error) {
	// Using a map here to weed out duplicates and to ensure each snapshot has
	// both a memory snapshot (.snap) and a disk snapshot (.qc2).
	matches := make(map[string]string)

	files, err := GetExperimentFileNames(exp)
	if err != nil {
		return nil, fmt.Errorf("getting experiment file names: %w", err)
	}

	for _, name := range files {
		ext := filepath.Ext(name)

		switch ext {
		case ".qc2", ".qcow2":
			ss := strings.TrimSuffix(name, ext)

			if m, ok := matches[ss]; !ok {
				matches[ss] = "qcow"
			} else if m == "snap" {
				matches[ss] = "both"
			}
		case ".SNAP", ".snap":
			ss := strings.TrimSuffix(name, ext)

			if m, ok := matches[ss]; !ok {
				matches[ss] = "snap"
			} else if m == "qcow" {
				matches[ss] = "both"
			}
		}
	}

	var snapshots []string

	for ss := range matches {
		if matches[ss] == "both" {
			snapshots = append(snapshots, ss)
		}
	}

	return snapshots, nil
}

func (MMClusterFiles) CopyFile(path, dest string, status CopyStatus) error {
	cmd := mmcli.NewCommand()

	if mm.IsHeadnode(dest) {
		cmd.Command = fmt.Sprintf(`file get %s`, path)
	} else {
		cmd.Command = fmt.Sprintf(`mesh send %s file get %s`, dest, path)
	}

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("copying file to destination: %w", err)
	}

	if mm.IsHeadnode(dest) {
		cmd.Command = fmt.Sprintf(`file status`)
	} else {
		cmd.Command = fmt.Sprintf(`mesh send %s file status`, dest)
	}

	for {
		var found bool

		for _, row := range mmcli.RunTabular(cmd) {
			if row["filename"] == path {
				comp := strings.Split(row["completed"], "/")

				parts, _ := strconv.ParseFloat(comp[0], 64)
				total, _ := strconv.ParseFloat(comp[1], 64)

				if status != nil {
					status(parts / total)
				}

				found = true
				break
			}
		}

		// If the file is done transferring, then it will not have been present in
		// the results from `file status`.
		if !found {
			break
		}
	}

	return nil
}

func (MMClusterFiles) SyncFile(path string, status CopyStatus) error {
	cmd := mmcli.NewCommand()
	cmd.Command = "mesh send all file get " + path

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("syncing file to cluster nodes: %w", err)
	}

	if status != nil {
		// TODO: use mesh to get file status transfer for file from each node.
	}

	return nil
}

func (MMClusterFiles) DeleteFile(path string) error {
	// First delete file from mesh, then from headnode.
	commands := []string{"mesh send all file delete", "file delete"}

	cmd := mmcli.NewCommand()

	for _, command := range commands {
		cmd.Command = fmt.Sprintf("%s %s", command, path)

		if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
			return fmt.Errorf("deleting file from cluster nodes: %w", err)
		}
	}

	return nil
}

// Get all image files from the minimega files directory
func getAllFiles(details map[string]ImageDetails) error {

	// First get file listings from mesh, then from headnode.
	commands := []string{"mesh send all file list", "file list"}

	// First, get file listings from cluster nodes.
	cmd := mmcli.NewCommand()

	for _, command := range commands {
		cmd.Command = command

		for _, row := range mmcli.RunTabular(cmd) {

			// Only look in the base directory
			if row["dir"] != "" {
				continue
			}

			baseName := filepath.Base(row["name"])

			// Avoid adding the same image twice
			if _, ok := details[baseName]; ok {
				continue
			}

			image := ImageDetails{
				Name:     baseName,
				FullPath: util.GetMMFullPath(row["name"]),
			}

			if strings.HasSuffix(image.Name, ".qc2") || strings.HasSuffix(image.Name, ".qcow2") {
				image.Kind = VM_IMAGE
			} else if strings.HasSuffix(image.Name, "_rootfs.tgz") {
				image.Kind = CONTAINER_IMAGE
			} else if strings.HasSuffix(image.Name, ".hdd") {
				image.Kind = VM_IMAGE
			} else if strings.HasSuffix(image.Name, ".iso") {
				image.Kind = ISO_IMAGE
			} else {
				continue
			}

			var err error

			image.Size, err = strconv.Atoi(row["size"])
			if err != nil {
				return fmt.Errorf("getting size of file: %w", err)
			}

			details[image.Name] = image
		}
	}

	return nil

}

// Retrieves all the unique image names defined in the topology
func getTopologyFiles(expName string, details map[string]ImageDetails) error {
	// Retrieve the experiment
	exp, err := experiment.Get(expName)
	if err != nil {
		return fmt.Errorf("unable to retrieve %v", expName)
	}

	
	for _, node := range exp.Spec.Topology().Nodes() {
		for _, drive := range node.Hardware().Drives() {
			cmd := mmcli.NewCommand()

			if len(drive.Image()) == 0 {
				continue
			}

			relMMPath,_ := filepath.Rel(mmFilesDirectory,drive.Image())

			if len(relMMPath) == 0 {
				relMMPath = drive.Image()
			}

			cmd.Command = "file list " + relMMPath

			for _, row := range mmcli.RunTabular(cmd) {
				if row["dir"] != "" {
					continue
				}

				baseName := filepath.Base(row["name"])

				// Avoid adding the same image twice
				if _, ok := details[baseName]; ok {
					continue
				}

				image := ImageDetails{
					Name:     baseName,
					FullPath: util.GetMMFullPath(row["name"]),
					Kind:     VM_IMAGE,
				}

				var err error

				if image.Size, err = strconv.Atoi(row["size"]); err != nil {
					return fmt.Errorf("getting size of file: %w", err)
				}

				details[image.Name] = image
			}
		}
	}

	return nil
}

func getExperimentNames() (map[string]struct{}, error) {
	experiments, err := experiment.List()
	if err != nil {
		return nil, err
	}

	expNames := make(map[string]struct{})

	for _, exp := range experiments {
		expNames[exp.Spec.ExperimentName()] = struct{}{}
	}

	return expNames, nil
}
