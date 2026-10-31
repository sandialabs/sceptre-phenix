package file

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"phenix/util"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
)

var DefaultClusterFiles ClusterFiles = new(MMClusterFiles) //nolint:gochecknoglobals // default implementation

const snapshotBoth = "both"
const scorchComponentIndex = 2

// copyStatusInterval is how often CopyFile polls a transfer's progress.
const copyStatusInterval = 500 * time.Millisecond

type ClusterFiles interface {
	GetExperimentFiles(exp, filter string) (Files, error)

	// Looks in experiment directory on each cluster node for matching filenames
	// that end in both `.state` and `.hdd`.
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

// filesObserver is the optional part of ClusterFiles that reports when a
// listing was read. It is kept out of ClusterFiles so the stand-ins tests use
// need not implement it.
type filesObserver interface {
	GetExperimentFilesObserved(ctx context.Context, exp, filter string) (Files, freshness.Observation, error)
}

// GetExperimentFilesObserved is GetExperimentFiles, also reporting when the listing was read (now,
// for a ClusterFiles that cannot tell). Its commands give up once ctx and those of the other callers
// waiting for it are done, and the call then returns ctx's error.
func GetExperimentFilesObserved(ctx context.Context, exp, filter string) (Files, freshness.Observation, error) {
	if o, ok := DefaultClusterFiles.(filesObserver); ok {
		return o.GetExperimentFilesObserved(ctx, exp, filter)
	}

	at := time.Now()

	files, err := DefaultClusterFiles.GetExperimentFiles(exp, filter)
	if err != nil {
		return nil, freshness.Observation{}, err //nolint:wrapcheck // passthrough
	}

	return files, freshness.Observed(at), nil
}

func GetExperimentSnapshots(exp string) ([]string, error) {
	return DefaultClusterFiles.GetExperimentSnapshots(exp) //nolint:wrapcheck // passthrough
}

// snapshotsLister is the optional part of ClusterFiles whose snapshot listing
// gives up once a context is done; see filesObserver.
type snapshotsLister interface {
	GetExperimentSnapshotsContext(ctx context.Context, exp string) ([]string, error)
}

// GetExperimentSnapshotsContext is GetExperimentSnapshots, its listing's
// minimega commands giving up once ctx is done as GetExperimentFilesObserved's
// do, where DefaultClusterFiles supports it.
func GetExperimentSnapshotsContext(ctx context.Context, exp string) ([]string, error) {
	if l, ok := DefaultClusterFiles.(snapshotsLister); ok {
		return l.GetExperimentSnapshotsContext(ctx, exp) //nolint:wrapcheck // passthrough
	}

	return DefaultClusterFiles.GetExperimentSnapshots(exp) //nolint:wrapcheck // passthrough
}

func CopyFile(path, dest string, status CopyStatus) error {
	return DefaultClusterFiles.CopyFile(path, dest, status) //nolint:wrapcheck // passthrough
}

func SyncFile(path string, status CopyStatus) error {
	return DefaultClusterFiles.SyncFile(path, status) //nolint:wrapcheck // passthrough
}

func DeleteFile(path string) error {
	// The path may be in any experiment's files directory, or name several.
	defer invalidateAllExperimentFiles()

	return DefaultClusterFiles.DeleteFile(path) //nolint:wrapcheck // passthrough
}

type MMClusterFiles struct{}

// GetExperimentFiles lists the experiment's files, reusing a listing made in
// the last few seconds (see listingTTL), and keeps those matching filter. It
// returns an error, never a partial listing, when a cluster node could not be
// listed.
func (f MMClusterFiles) GetExperimentFiles(exp, filter string) (Files, error) {
	files, _, err := f.GetExperimentFilesObserved(context.Background(), exp, filter)

	return files, err
}

// GetExperimentFilesObserved is GetExperimentFiles, also reporting when the
// listing was read from minimega.
func (MMClusterFiles) GetExperimentFilesObserved(
	ctx context.Context,
	exp, filter string,
) (Files, freshness.Observation, error) {
	files, started, err := experimentListings.getContext(ctx, listingKey(exp), func(ctx context.Context) (Files, error) {
		return listExperimentFiles(ctx, exp)
	})
	if err != nil {
		return nil, freshness.Observation{}, err
	}

	observed := freshness.Observed(started)

	if len(filter) == 0 {
		return files, observed, nil
	}

	// Build a Boolean expression tree and determine
	// the fields that should be searched
	filterTree := BuildTree(filter)
	if filterTree == nil {
		return nil, observed, nil
	}

	var matched Files

	for _, file := range files {
		if filterTree.Evaluate(&file) {
			matched = append(matched, file)
		}
	}

	return matched, observed, nil
}

// listExperimentFiles asks minimega for every file in the experiment's files
// directory, on the mesh nodes and the headnode, and categorizes them. A
// directory that does not exist lists as empty (minimega's `file list` globs
// the path); a node that fails to answer is an error, as is ctx ending while
// the commands wait for the shared minimega connection.
func listExperimentFiles(ctx context.Context, exp string) (Files, error) {
	var (
		// Using a map here to weed out duplicates. The key is the relative path to
		// the file to ensure files with the same name in different directories get
		// included.
		matches = make(map[string]File)
		root    = listingKey(exp)
	)

	// First get file listings from mesh, then from headnode.
	commands := []string{
		fmt.Sprintf("mesh send all file list %s recursive", root),
		fmt.Sprintf("file list %s recursive", root),
	}

	cmd := mmcli.NewCommand()

	for _, command := range commands {
		cmd.Command = command

		rows, err := mmcli.RunTabularCheckedContext(ctx, cmd, nil)
		if err != nil {
			return nil, fmt.Errorf("listing files of experiment %s: %w", exp, err)
		}

		for _, row := range rows {
			name := filepath.Base(row["name"])
			file := File{Name: name, Path: strings.TrimPrefix(row["name"], root)} //nolint:exhaustruct // partial initialization
			file.Categories = []string{}

			if _, ok := matches[file.Path]; ok {
				continue
			}

			// a recorded netflow capture's metadata, not a file of its own
			if strings.HasPrefix(file.Path, "netflow/") && filepath.Ext(name) == ".json" {
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
		case ".qc2", ".qcow", ".qcow2":
			file.Categories = append(file.Categories, "Backing Image")
		}

		if util.StringSliceContains(plain, extension) {
			file.PlainText = true
		}

		if len(file.Categories) == 0 {
			file.Categories = []string{"Unknown"}
		}

		files = append(files, file)
	}

	return files, nil
}

func (f MMClusterFiles) GetExperimentSnapshots(exp string) ([]string, error) {
	return f.GetExperimentSnapshotsContext(context.Background(), exp)
}

// GetExperimentSnapshotsContext is GetExperimentSnapshots, giving up once ctx
// is done as GetExperimentFilesObserved does.
func (f MMClusterFiles) GetExperimentSnapshotsContext(ctx context.Context, exp string) ([]string, error) {
	// Using a map here to weed out duplicates and to ensure each snapshot has
	// both a memory snapshot (.state) and a disk snapshot (.hdd).
	matches := make(map[string]string)

	files, _, err := f.GetExperimentFilesObserved(ctx, exp, "")
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
	var (
		cmd      = mmcli.NewCommand()
		headnode = mm.IsHeadnode(dest)
	)

	if headnode {
		cmd.Command = "file get " + path
	} else {
		cmd.Command = fmt.Sprintf(`mesh send %s file get %s`, dest, path)
	}

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("copying file to destination: %w", err)
	}

	if headnode {
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

		// Don't poll back to back: every poll waits its turn for the minimega
		// connection that every other request shares.
		time.Sleep(copyStatusInterval)
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
	// NOTE: this is replicated in `util/mm/minimega.go` to avoid cyclical
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
