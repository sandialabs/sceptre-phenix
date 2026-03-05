package file

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"phenix/util"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
)

var DefaultClusterFiles ClusterFiles = new(MMClusterFiles) //nolint:gochecknoglobals // default implementation

const snapshotBoth = "both"
const scorchComponentIndex = 2

type ClusterFiles interface {
	GetExperimentFiles(exp, filter string) (Files, error)

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

func GetExperimentFiles(exp, filter string) (Files, error) {
	return DefaultClusterFiles.GetExperimentFiles(exp, filter) //nolint:wrapcheck // passthrough
}

func GetExperimentSnapshots(exp string) ([]string, error) {
	return DefaultClusterFiles.GetExperimentSnapshots(exp) //nolint:wrapcheck // passthrough
}

func CopyFile(path, dest string, status CopyStatus) error {
	return DefaultClusterFiles.CopyFile(path, dest, status) //nolint:wrapcheck // passthrough
}

func SyncFile(path string, status CopyStatus) error {
	return DefaultClusterFiles.SyncFile(path, status) //nolint:wrapcheck // passthrough
}

func DeleteFile(path string) error {
	return DefaultClusterFiles.DeleteFile(path) //nolint:wrapcheck // passthrough
}

type MMClusterFiles struct{}

func (MMClusterFiles) GetExperimentFiles(exp, filter string) (Files, error) {
	var (
		// Using a map here to weed out duplicates. The key is the relative path to
		// the file to ensure files with the same name in different directories get
		// included.
		matches = make(map[string]File)
		root    = exp + "/files/"
	)

	// First get file listings from mesh, then from headnode.
	commands := []string{
		fmt.Sprintf("mesh send all file list %s recursive", root),
		fmt.Sprintf("file list %s recursive", root),
	}

	cmd := mmcli.NewCommand()

	// Build a Boolean expression tree and determine
	// the fields that should be searched
	filterTree := BuildTree(filter)

	for _, command := range commands {
		cmd.Command = command

		for _, row := range mmcli.RunTabular(cmd) {
			name := filepath.Base(row["name"])
			file := File{Name: name, Path: strings.TrimPrefix(row["name"], root)} //nolint:exhaustruct // partial initialization

			if _, ok := matches[file.Path]; ok {
				continue
			}

			if strings.Contains(file.Path, "scorch") {
				file.Categories = append(file.Categories, "Scorch Artifact")

				directories := strings.Split(filepath.Dir(file.Path), "/")

				if len(directories) > 1 {
					// Add Scorch run ID as a category.
					file.Categories = append(file.Categories, directories[1])
				}

				if strings.Contains(file.Path, "filebeat") {
					if name != "filebeat.log" {
						// Exclude Filebeat-relevant files except for the log file.
						continue
					}

					file.Categories = append(file.Categories, "Filebeat")
				} else if len(directories) > scorchComponentIndex {
					// Add Scorch component name as a category.
					file.Categories = append(file.Categories, directories[scorchComponentIndex])
				}
			}

			switch extension := filepath.Ext(name); extension {
			case ".pcap":
				file.Categories = append(file.Categories, "Packet Capture")
			case ".elf":
				file.Categories = append(file.Categories, "ELF Memory Snapshot")
			case ".state":
				file.Categories = append(file.Categories, "VM Memory Snapshot")
			}

			file.Size, _ = strconv.ParseInt(row["size"], 10, 64)
			file.Date = row["modified"]
			file.dateTime, _ = time.Parse(time.RFC3339, row["modified"])

			matches[file.Path] = file
		}
	}

	var (
		files Files
		plain = []string{".json", ".jsonl", ".log", ".txt", ".yaml", ".yml"}
	)

	for _, file := range matches {
		extension := filepath.Ext(file.Name)

		// Add categories for qcow images prior to filtering.
		switch extension {
		case ".hdd":
			file.Categories = append(file.Categories, "VM Disk Snapshot")
		case ".qc2", ".qcow2":
			file.Categories = append(file.Categories, "Backing Image")
		}

		if util.StringSliceContains(plain, extension) {
			file.PlainText = true
		}

		if len(file.Categories) == 0 {
			file.Categories = []string{"Unknown"}
		}

		// Apply any filters
		if len(filter) > 0 {
			if filterTree == nil {
				continue
			}

			if !filterTree.Evaluate(&file) {
				continue
			}
		}

		files = append(files, file)
	}

	return files, nil
}

func (MMClusterFiles) GetExperimentSnapshots(exp string) ([]string, error) {
	// Using a map here to weed out duplicates and to ensure each snapshot has
	// both a memory snapshot (.snap) and a disk snapshot (.qc2).
	matches := make(map[string]string)

	files, err := GetExperimentFiles(exp, "")
	if err != nil {
		return nil, fmt.Errorf("getting experiment file names: %w", err)
	}

	for _, f := range files {
		ext := filepath.Ext(f.Name)

		switch ext {
		case ".hdd":
			ss := strings.TrimSuffix(f.Name, ext)

			if m, ok := matches[ss]; !ok {
				matches[ss] = "hdd"
			} else if m == "state" {
				matches[ss] = snapshotBoth
			}
		case ".state":
			ss := strings.TrimSuffix(f.Name, ext)

			if m, ok := matches[ss]; !ok {
				matches[ss] = "state"
			} else if m == "hdd" {
				matches[ss] = snapshotBoth
			}
		}
	}

	var snapshots []string

	for ss := range matches {
		if matches[ss] == snapshotBoth {
			snapshots = append(snapshots, ss)
		}
	}

	return snapshots, nil
}

func (MMClusterFiles) CopyFile(path, dest string, status CopyStatus) error {
	cmd := mmcli.NewCommand()

	if mm.IsHeadnode(dest) {
		cmd.Command = "file get " + path
	} else {
		cmd.Command = fmt.Sprintf(`mesh send %s file get %s`, dest, path)
	}

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("copying file to destination: %w", err)
	}

	if mm.IsHeadnode(dest) {
		cmd.Command = "file status"
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

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("syncing file to cluster nodes: %w", err)
	}

	if status != nil {
		_ = status
	}

	return nil
}

func (MMClusterFiles) DeleteFile(path string) error {
	// NOTE: this is replicated in `internal/mm/minimega.go` to avoid cyclical
	// dependency between mm and file packages.

	// First delete file from mesh, then from headnode.
	commands := []string{"mesh send all file delete", "file delete"}

	cmd := mmcli.NewCommand()

	for _, command := range commands {
		cmd.Command = fmt.Sprintf("%s %s", command, path)

		err := mmcli.ErrorResponse(mmcli.Run(cmd))
		if err != nil {
			return fmt.Errorf("deleting file from cluster nodes: %w", err)
		}
	}

	return nil
}
