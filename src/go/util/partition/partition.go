package partition

import (
	"errors"
	"fmt"
	"io"
)

// Scheme is the kind of partition table on a disk.
type Scheme string

// The schemes Read tells apart.
const (
	// SchemeNone is a disk with no partition table, such as a file system on the whole disk.
	SchemeNone Scheme = "none"
	// SchemeMBR is an MBR (DOS) partition table.
	SchemeMBR Scheme = "mbr"
	// SchemeGPT is a GUID partition table.
	SchemeGPT Scheme = "gpt"
	// SchemeUnknown is a disk with the mark of a partition table that phenix cannot read (see Table.Notes).
	SchemeUnknown Scheme = "unknown"
)

const (
	// MaxPartitions is the most partitions Read returns. It also limits the logical partitions it follows.
	MaxPartitions = 256

	// MaxProbes is the most partitions whose file system Inspect reads.
	MaxProbes = 64

	sectorSize = 512
)

// Partition is one partition of a disk.
type Partition struct {
	// Number is the number Linux gives the partition: 1 to 4 for MBR primary partitions, 5 and up for logical
	// partitions in chain order, and the entry index plus one for GPT.
	Number int
	// Start is the offset of the partition from the start of the disk, in bytes.
	Start int64
	// Size is the size of the partition in bytes, cut to the end of the disk.
	Size int64
	// Type is the name of the partition type, such as "Linux" or "EFI System", or "" for a type phenix does not know.
	Type string
	// TypeID is the MBR type byte, such as "0x83", or the GPT type GUID in upper case.
	TypeID string
	// Bootable is the MBR active flag, or the GPT legacy BIOS bootable attribute.
	Bootable bool
	// Name is the GPT partition name, or "".
	Name string
	// GUID is the GPT unique partition GUID in upper case, or "".
	GUID string
	// Extended is set for an MBR extended partition, which holds the logical partitions.
	Extended bool
	// Logical is set for an MBR logical partition.
	Logical bool
	// Filesystem is the file system Inspect found in the partition, or nil when it did not read the partition.
	Filesystem *Filesystem
}

// Table is the partition table of a disk.
type Table struct {
	Scheme Scheme
	// SectorSize is the sector size the table uses, in bytes, or 0 for SchemeNone and SchemeUnknown.
	SectorSize int64
	// DiskID is the MBR disk signature, such as "0x1a2b3c4d", or the GPT disk GUID in upper case.
	DiskID string
	// Partitions are in the order of their numbers.
	Partitions []Partition
	// Filesystem is the file system Inspect found on the whole disk when Scheme is SchemeNone, or nil.
	Filesystem *Filesystem
	// Notes say what is wrong with the table, each in one sentence without a final period.
	Notes []string
}

// Read reads the partition table of the disk r holds, which is size bytes long. It returns an error only when r
// fails with an error other than [io.EOF]; a damaged table gives SchemeUnknown, or the partitions phenix could read,
// with Notes that say what is wrong.
func Read(r io.ReaderAt, size int64) (Table, error) {
	t := Table{Scheme: SchemeNone, SectorSize: 0, DiskID: "", Partitions: []Partition{}, Filesystem: nil, Notes: nil}

	mbr, err := read(r, size, 0, sectorSize)
	if err != nil {
		return t, err
	}

	if len(mbr) < sectorSize || !bootMark(mbr) {
		return t, nil
	}

	if protective(mbr) {
		return readGPT(r, size, t)
	}

	return readMBR(r, size, mbr, t)
}

// Inspect reads the partition table of the disk r holds (see Read), then the file system of each partition, or of
// the whole disk when it has no table. It reads at most MaxProbes partitions, and none of an extended partition. On
// an error from r it returns the table with the file systems read so far.
func Inspect(r io.ReaderAt, size int64) (Table, error) {
	t, err := Read(r, size)
	if err != nil {
		return t, err
	}

	if t.Scheme == SchemeNone {
		fs, err := Probe(r, 0, size)
		if err != nil {
			return t, err
		}

		t.Filesystem = &fs

		return t, nil
	}

	probed := 0

	for i := range t.Partitions {
		p := &t.Partitions[i]
		if p.Extended {
			continue
		}

		if probed == MaxProbes {
			t.Notes = append(t.Notes, fmt.Sprintf("phenix read the file systems of the first %d partitions only", MaxProbes))

			break
		}

		probed++

		fs, err := Probe(r, p.Start, p.Size)
		if err != nil {
			return t, err
		}

		p.Filesystem = &fs
	}

	return t, nil
}

// read returns up to n bytes of r at off, cut to the disk's size: fewer when the disk ends first, and none when off
// is outside the disk. It fails only on an error from r other than [io.EOF].
func read(r io.ReaderAt, size, off int64, n int) ([]byte, error) {
	if off < 0 || off >= size || n <= 0 {
		return nil, nil
	}

	if rest := size - off; int64(n) > rest {
		n = int(rest)
	}

	buf := make([]byte, n)

	got, err := r.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	return buf[:min(max(got, 0), n)], nil
}

// span returns the start and size in bytes of a partition of sectors sectors at sector first, cut to the disk's
// size, and false when it starts past the end of the disk or has no sectors. cut is set when the partition ran past
// the end.
func span(first, sectors uint64, ss, size int64) (int64, int64, bool, bool) {
	disk := uint64(size) / uint64(ss) //nolint:gosec // G115 size is not negative
	if sectors == 0 || first >= disk {
		return 0, 0, false, false
	}

	cut := sectors > disk-first
	if cut {
		sectors = disk - first
	}

	return int64(first) * ss, int64(sectors) * ss, cut, true //nolint:gosec // G115 both are below size / ss
}
