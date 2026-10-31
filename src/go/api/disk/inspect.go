package disk

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"phenix/api/experiment"
	"phenix/util/diskchain"
	"phenix/util/mm"
	"phenix/util/partition"
)

// maxInspections bounds the inspections kept; past it the cache starts again empty.
const maxInspections = 1024

const blockUnit = 512

var (
	inspectMu   sync.Mutex                         //nolint:gochecknoglobals // guards inspections
	inspections = make(map[string]inspectionEntry) //nolint:gochecknoglobals // inspections by path
	rawFormats  = []string{"", "raw"}              //nolint:gochecknoglobals // constant
)

// Inspection is what phenix reads from a disk image for its details card, past what the disk list shows (see
// Inspect).
type Inspection struct {
	FullPath      string       `json:"fullPath"`
	Format        string       `json:"format"`
	FormatVersion int          `json:"formatVersion"`
	FormatIssue   *FormatIssue `json:"formatIssue"`
	// FileBytes is the size of the file, AllocatedBytes the space it takes on disk, and VirtualBytes the size of the
	// disk it holds, or 0 when phenix does not know it.
	FileBytes      int64 `json:"fileBytes"`
	AllocatedBytes int64 `json:"allocatedBytes"`
	VirtualBytes   int64 `json:"virtualBytes"`
	// Modified is the time the file last changed, in RFC 3339, or "".
	Modified string `json:"modified"`
	// Qcow is the header of a qcow or qcow2 image, or nil.
	Qcow *QcowHeader `json:"qcow"`
	// FileUnavailable says why phenix cannot open the image, or is "".
	FileUnavailable string `json:"fileUnavailable"`
	// PartitionTable is mbr, gpt, none or unknown, or "" when PartitionsUnavailable says why phenix did not read it.
	PartitionTable string      `json:"partitionTable"`
	DiskID         string      `json:"diskId"`
	SectorSize     int64       `json:"sectorSize"`
	Partitions     []Partition `json:"partitions"`
	// Filesystem is the file system on the whole disk when it has no partition table, or nil.
	Filesystem *Filesystem `json:"filesystem"`
	// PartitionNotes say what is wrong with the partition table, or what phenix did not read.
	PartitionNotes        []string `json:"partitionNotes"`
	PartitionsUnavailable string   `json:"partitionsUnavailable"`
	// Overlays are the listed images whose backing chain holds this image.
	Overlays []Overlay `json:"overlays"`
	// VMs are the VMs whose drives name this image (see VMUses).
	VMs []VMUse `json:"vms"`
}

// QcowHeader is what the header of a qcow or qcow2 image says.
type QcowHeader struct {
	// Compat is the qcow2 compatibility level, "0.10" for version 2 and "1.1" for version 3, or "" for qcow.
	Compat           string `json:"compat"`
	ClusterSize      uint64 `json:"clusterSize"`
	RefcountBits     uint64 `json:"refcountBits"`
	Encryption       string `json:"encryption"`
	Compression      string `json:"compression"`
	Dirty            bool   `json:"dirty"`
	Corrupt          bool   `json:"corrupt"`
	ExternalDataFile bool   `json:"externalDataFile"`
	ExtendedL2       bool   `json:"extendedL2"`
	LazyRefcounts    bool   `json:"lazyRefcounts"`
	Snapshots        uint32 `json:"snapshots"`
	// BackingFile is the backing file name as the header stores it, and BackingFormat its format, or "".
	BackingFile   string `json:"backingFile"`
	BackingFormat string `json:"backingFormat"`
}

// Partition is a partition of the disk an image holds (see partition.Partition).
type Partition struct {
	Number     int         `json:"number"`
	Type       string      `json:"type"`
	TypeID     string      `json:"typeId"`
	Start      int64       `json:"start"`
	Size       int64       `json:"size"`
	Bootable   bool        `json:"bootable"`
	Extended   bool        `json:"extended"`
	Logical    bool        `json:"logical"`
	Name       string      `json:"name"`
	GUID       string      `json:"guid"`
	Filesystem *Filesystem `json:"filesystem"`
}

// Filesystem is the file system in a partition (see partition.Filesystem).
type Filesystem struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	Label   string `json:"label"`
}

// Overlay is an image backed by the inspected one.
type Overlay struct {
	FullPath string `json:"fullPath"`
	// Direct is set when the inspected image is the overlay's own backing file.
	Direct bool `json:"direct"`
}

// VMUse is a VM of an experiment whose drive names a disk image.
type VMUse struct {
	Experiment string `json:"experiment"`
	Running    bool   `json:"running"`
	VM         string `json:"vm"`
	// Drive is the index of the drive in the VM's drives.
	Drive int `json:"drive"`
	// InjectPartition is the partition phenix injects files into: that of the first drive, or 0 for none.
	InjectPartition int `json:"injectPartition"`
}

// inspectionEntry is a cached inspection, with a stamp of each file it read.
type inspectionEntry struct {
	stamps []fileStamp
	found  Inspection
}

// Inspect reads the image the disk list shows as image: its file, its qcow header, and the partitions and file
// systems of the disk it holds, through its backing chain. It finds the overlays in images, the disk list. It reads
// at most maxDiskRead bytes of the disk for at most qemuImgTimeout, and keeps the result until a file it read changes.
// The caller fills VMs.
func Inspect(ctx context.Context, image Details, images []Details) Inspection {
	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		qemuImg = ""
	}

	found := cachedInspection(ctx, image, qemuImg)
	found.Overlays = overlays(image.FullPath, images)

	return found
}

// InspectPath is Inspect for the VM disk image at the clean absolute path, in the format its header gives, read with
// qemuImg: the path of qemu-img, or "" when it is not installed. It finds no overlays.
func InspectPath(ctx context.Context, path, qemuImg string) Inspection {
	image := Details{Kind: VMImage, FullPath: path} //nolint:exhaustruct // inspect reads only these and the format

	return cachedInspection(ctx, image, qemuImg)
}

// cachedInspection is inspect, kept until a file it read changes.
func cachedInspection(ctx context.Context, image Details, qemuImg string) Inspection {
	inspectMu.Lock()
	entry, ok := inspections[image.FullPath]
	inspectMu.Unlock()

	if ok && stampsCurrent(entry.stamps) {
		return withListing(entry.found, image)
	}

	found, stamps := inspect(ctx, image, qemuImg)

	inspectMu.Lock()
	defer inspectMu.Unlock()

	if len(inspections) >= maxInspections {
		inspections = make(map[string]inspectionEntry)
	}

	if stamps != nil {
		inspections[image.FullPath] = inspectionEntry{stamps: stamps, found: found}
	} else {
		delete(inspections, image.FullPath)
	}

	return found
}

// withListing returns found with the format fields of image, as the disk list shows them.
func withListing(found Inspection, image Details) Inspection {
	found.FullPath = image.FullPath
	found.Format = image.Format
	found.FormatVersion = image.FormatVersion
	found.FormatIssue = image.FormatIssue

	return found
}

// inspect reads the image (see Inspect) with qemuImg, the path of qemu-img or "" when it is not installed. It returns
// the stamps of the files it read, or nil when the result is not to be kept.
func inspect(ctx context.Context, image Details, qemuImg string) (Inspection, []fileStamp) {
	found := withListing(emptyInspection(), image)

	f, err := diskchain.OpenRegular(image.FullPath)
	if err != nil {
		found.FileUnavailable = fmt.Sprintf(
			"phenix cannot open the image (%s). If minimega runs on a different host, only minimega can read it", cause(err))
		found.PartitionsUnavailable = found.FileUnavailable

		return found, nil
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		found.FileUnavailable = "phenix cannot read the image: " + cause(err)
		found.PartitionsUnavailable = found.FileUnavailable

		return found, nil
	}

	found.FileBytes = info.Size()
	found.Modified = info.ModTime().UTC().Format(time.RFC3339)

	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		found.AllocatedBytes = sys.Blocks * blockUnit
	}

	stamp, err := stat(image.FullPath)
	if err != nil {
		return found, nil
	}

	h, headerErr := diskchain.ParseHeader(f)
	qcow := h.Format == diskchain.FormatQcow2 || h.Format == diskchain.FormatQcow

	if qcow && headerErr == nil {
		if g, err := diskchain.ParseGeometry(f, h); err == nil {
			found.Qcow = qcowHeader(h, g)
			found.VirtualBytes = int64(min(g.VirtualSize, 1<<62)) //nolint:gosec,mnd // G115 cut to fit an int64
		}
	}

	ctx, cancel := context.WithTimeout(ctx, qemuImgTimeout)
	defer cancel()

	stamps := []fileStamp{stamp}

	switch {
	case image.Kind == ContainerImage:
		found.PartitionsUnavailable = "a container image holds a file system archive, not a disk with partitions"
	case qcow && found.Qcow == nil:
		found.PartitionsUnavailable = "phenix cannot read the partitions: " +
			diskchain.Check(image.FullPath, h, headerErr).Sentence()
	case qcow:
		stamps = readQcowPartitions(ctx, &found, image.FullPath, string(h.Format), qemuImg, stamps)
	case qcowFormat(image.Format):
		found.PartitionsUnavailable = "phenix cannot read the partitions: the image does not have a " + image.Format + " header"
	case h.Format == diskchain.FormatOther && slices.Contains(rawFormats, image.Format):
		found.VirtualBytes = found.FileBytes
		readPartitions(ctx, &found, fileReader{ctx: ctx, f: f, budget: &readBudget{left: maxDiskRead}}, found.FileBytes)
	default:
		format := cmp.Or(image.Format, string(h.Format))
		found.PartitionsUnavailable = "phenix reads partitions only in raw, qcow2 and qcow images, not in " + format + " images"
	}

	// a run cut short by its deadline can work the next time
	if ctx.Err() != nil {
		return found, nil
	}

	return found, stamps
}

// emptyInspection is an Inspection with empty lists.
func emptyInspection() Inspection {
	return Inspection{ //nolint:exhaustruct // inspect fills the rest
		Partitions: []Partition{}, PartitionNotes: []string{}, Overlays: []Overlay{}, VMs: []VMUse{},
	}
}

// readQcowPartitions reads into found the partitions of the qcow or qcow2 image at path, of format, through qemuImg,
// once phenix finds that qemu can open the image's backing chain. It returns stamps with a stamp of each backing file,
// or nil when phenix cannot stamp a file that it watches.
func readQcowPartitions(ctx context.Context, found *Inspection, path, format, qemuImg string, stamps []fileStamp) []fileStamp {
	if qemuImg == "" {
		found.PartitionsUnavailable = fmt.Sprintf(
			"qemu-img is not installed where phenix runs, so phenix cannot read the partitions of a %s image", format)

		return stamps
	}

	res := diskchain.Walk(ctx, path)

	// the folder of a missing file changes when the file appears
	watched := res.Members[min(1, len(res.Members)):]
	if res.Problem == diskchain.Missing {
		watched = append(slices.Clone(watched), filepath.Dir(res.At))
	}

	for _, p := range watched {
		stamp, err := stat(p)
		if err != nil {
			// with no stamp, phenix cannot tell when the file changes, so it does not keep the result
			stamps = nil

			break
		}

		stamps = append(stamps, stamp)
	}

	if res.Problem != diskchain.Whole {
		found.PartitionsUnavailable = "phenix cannot read the partitions: " + res.MapPaths(mm.CleanBackingPath).Sentence()

		return stamps
	}

	dir, err := os.MkdirTemp("", "phenix-disk-")
	if err != nil {
		found.PartitionsUnavailable = "phenix cannot read the partitions: " + cause(err)

		return stamps
	}

	defer func() { _ = os.RemoveAll(dir) }()

	r := newQemuReader(ctx, qemuImg, path, format, found.VirtualBytes, dir, &readBudget{left: maxDiskRead})
	readPartitions(ctx, found, r, found.VirtualBytes)

	return stamps
}

// readPartitions reads into found the partition table and file systems of the disk r holds, size bytes long.
func readPartitions(ctx context.Context, found *Inspection, r io.ReaderAt, size int64) {
	table, err := partition.Inspect(r, size)

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		found.PartitionsUnavailable = fmt.Sprintf("phenix stopped reading the partitions after %s", qemuImgTimeout)

		return
	case err != nil && !errors.Is(err, errReadLimit):
		found.PartitionsUnavailable = "phenix cannot read the partitions: " + err.Error()

		return
	case err != nil && table.Scheme != partition.SchemeNone && len(table.Partitions) > 0:
		table.Notes = append(table.Notes, fmt.Sprintf(
			"phenix read %d MiB of the disk, its limit, so it shows only some file systems", maxDiskReadMiB))
	case err != nil:
		found.PartitionsUnavailable = fmt.Sprintf(
			"phenix read %d MiB of the disk, its limit, before it found the partitions", maxDiskReadMiB)

		return
	}

	found.PartitionTable = string(table.Scheme)
	found.DiskID = table.DiskID
	found.SectorSize = table.SectorSize
	found.Filesystem = filesystem(table.Filesystem)
	found.PartitionNotes = append(found.PartitionNotes, table.Notes...)

	for _, p := range table.Partitions {
		found.Partitions = append(found.Partitions, Partition{
			Number: p.Number, Type: p.Type, TypeID: p.TypeID, Start: p.Start, Size: p.Size, Bootable: p.Bootable,
			Extended: p.Extended, Logical: p.Logical, Name: p.Name, GUID: p.GUID, Filesystem: filesystem(p.Filesystem),
		})
	}
}

func filesystem(fs *partition.Filesystem) *Filesystem {
	if fs == nil {
		return nil
	}

	return &Filesystem{Type: fs.Type, Version: fs.Version, Label: fs.Label}
}

// qcowHeader is what h and g, the header and geometry of a qcow or qcow2 image, say.
func qcowHeader(h diskchain.Header, g diskchain.Geometry) *QcowHeader {
	q := &QcowHeader{
		Compat: "", ClusterSize: g.ClusterSize, RefcountBits: g.RefcountBits, Encryption: "none", Compression: "zlib",
		Dirty:            h.Incompatible&diskchain.FeatureDirty != 0,
		Corrupt:          h.Incompatible&diskchain.FeatureCorrupt != 0,
		ExternalDataFile: h.DataFile,
		ExtendedL2:       h.Incompatible&diskchain.FeatureExtendedL2 != 0,
		LazyRefcounts:    g.Compatible&diskchain.FeatureLazyRefcounts != 0,
		Snapshots:        g.Snapshots,
		BackingFile:      h.Backing,
		BackingFormat:    h.BackingFormat,
	}

	switch h.Version {
	case 2: //nolint:mnd // qcow2 version 2
		q.Compat = "0.10"
	case 3: //nolint:mnd // qcow2 version 3
		q.Compat = "1.1"
	}

	switch h.Encryption {
	case diskchain.EncryptionAES:
		q.Encryption = "aes"
	case diskchain.EncryptionLUKS:
		q.Encryption = "luks"
	case diskchain.EncryptionNone:
	}

	if h.Zstd {
		q.Compression = "zstd"
	}

	return q
}

// overlays returns the images whose backing chain holds the image at path, sorted by full path.
func overlays(path string, images []Details) []Overlay {
	found := []Overlay{}

	for _, image := range images {
		if slices.Contains(image.BackingImages, path) {
			found = append(found, Overlay{FullPath: image.FullPath, Direct: image.BackingImages[0] == path})
		}
	}

	slices.SortFunc(found, func(a, b Overlay) int { return cmp.Compare(a.FullPath, b.FullPath) })

	return found
}

// VMUses returns the VMs of every experiment whose drives name the image at path, by its full path.
func VMUses(path string) ([]VMUse, error) {
	experiments, err := experiment.List()
	if err != nil {
		return nil, fmt.Errorf("listing experiments: %w", err)
	}

	uses := []VMUse{}

	for _, exp := range experiments {
		for _, node := range exp.Spec.Topology().Nodes() {
			if node.Hardware() == nil {
				continue
			}

			for i, drive := range node.Hardware().Drives() {
				if drive.Image() == "" || mm.GetMMFullPath(drive.Image()) != path {
					continue
				}

				use := VMUse{
					Experiment:      exp.Metadata.Name,
					Running:         exp.Running(),
					VM:              node.General().Hostname(),
					Drive:           i,
					InjectPartition: 0,
				}
				if i == 0 && drive.InjectPartition() != nil {
					use.InjectPartition = *drive.InjectPartition()
				}

				uses = append(uses, use)
			}
		}
	}

	return uses, nil
}

// cause is err's cause in a few words, without the path it names.
func cause(err error) string {
	var perr *os.PathError
	if errors.As(err, &perr) {
		return perr.Err.Error()
	}

	return err.Error()
}
