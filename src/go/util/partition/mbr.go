package partition

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	mbrEntries      = 446
	mbrEntrySize    = 16
	mbrPrimaries    = 4
	mbrSignatureAt  = 440
	mbrBootFlag     = 0x80
	mbrTypeAt       = 4
	mbrStartAt      = 8
	mbrSectorsAt    = 12
	bootMarkAt      = 510
	typeGPT         = 0xEE
	firstLogical    = 5
	extendedDOS     = 0x05
	extendedLBA     = 0x0F
	extendedLinux   = 0x85
	maxLogicalLinks = MaxPartitions
)

// mbrEntry is one 16-byte entry of an MBR or an extended boot record.
type mbrEntry struct {
	boot    byte
	typ     byte
	first   uint64
	sectors uint64
}

// bootMark reports whether the sector ends in the 0x55 0xAA mark of a boot sector.
func bootMark(sector []byte) bool {
	return len(sector) >= sectorSize && sector[bootMarkAt] == 0x55 && sector[bootMarkAt+1] == 0xAA
}

// entries returns the four partition entries of an MBR or extended boot record.
func entries(sector []byte) []mbrEntry {
	es := make([]mbrEntry, 0, mbrPrimaries)

	for i := range mbrPrimaries {
		e := sector[mbrEntries+i*mbrEntrySize:]
		es = append(es, mbrEntry{
			boot:    e[0],
			typ:     e[mbrTypeAt],
			first:   uint64(binary.LittleEndian.Uint32(e[mbrStartAt:])),
			sectors: uint64(binary.LittleEndian.Uint32(e[mbrSectorsAt:])),
		})
	}

	return es
}

// protective reports whether the MBR has a GPT protective partition.
func protective(mbr []byte) bool {
	for _, e := range entries(mbr) {
		if e.typ == typeGPT {
			return true
		}
	}

	return false
}

// empty reports whether e holds no partition. Like Linux, it looks only at the size, not at the type.
func (e mbrEntry) empty() bool {
	return e.sectors == 0
}

func (e mbrEntry) extended() bool {
	return e.typ == extendedDOS || e.typ == extendedLBA || e.typ == extendedLinux
}

// readMBR adds to t the partitions of the MBR in mbr, the first sector of the disk r holds, as Linux reads it: a
// sector that is a FAT or NTFS boot sector, or whose boot flags are not 0x00 or 0x80, holds no table.
func readMBR(r io.ReaderAt, size int64, mbr []byte, t Table) (Table, error) {
	if bootSector(mbr) {
		return t, nil
	}

	es := entries(mbr)

	for _, e := range es {
		if e.boot != 0 && e.boot != mbrBootFlag {
			t.Scheme = SchemeUnknown
			t.Notes = append(t.Notes, "the first sector has the boot mark but no valid MBR partition table")

			return t, nil
		}
	}

	t.Scheme = SchemeMBR
	t.SectorSize = sectorSize
	t.DiskID = fmt.Sprintf("0x%08x", binary.LittleEndian.Uint32(mbr[mbrSignatureAt:]))

	var extended []Partition

	for i, e := range es {
		if e.empty() {
			continue
		}

		p, ok := mbrPartition(&t, e, i+1, 0, size)
		if !ok {
			continue
		}

		p.Extended = e.extended()
		t.Partitions = append(t.Partitions, p)

		if p.Extended {
			extended = append(extended, p)
		}
	}

	next := firstLogical

	for _, ext := range extended {
		var err error

		if t, next, err = readLogical(r, size, ext, t, next); err != nil {
			return t, err
		}
	}

	return t, nil
}

// mbrPartition returns partition number of entry e, whose start is relative to sector base, cut to the disk. It adds
// a note to t for a partition past the end of the disk.
func mbrPartition(t *Table, e mbrEntry, number int, base uint64, size int64) (Partition, bool) {
	start, length, cut, ok := span(base+e.first, e.sectors, sectorSize, size)

	switch {
	case !ok:
		t.Notes = append(t.Notes, fmt.Sprintf("partition %d starts past the end of the disk", number))

		return Partition{}, false
	case cut:
		t.Notes = append(t.Notes, fmt.Sprintf("partition %d ends past the end of the disk, so phenix shows it cut", number))
	}

	return Partition{
		Number: number, Start: start, Size: length, Type: mbrTypeName(e.typ), TypeID: fmt.Sprintf("0x%02x", e.typ),
		Bootable: e.boot == mbrBootFlag, Name: "", GUID: "", Extended: false, Logical: false, Filesystem: nil,
	}, true
}

// readLogical adds to t the logical partitions in the chain of extended boot records of ext, numbered from next, and
// returns the number after the last. It stops at a link outside ext, a loop, or a sector without the boot mark.
func readLogical(r io.ReaderAt, size int64, ext Partition, t Table, next int) (Table, int, error) {
	var (
		extFirst   = uint64(ext.Start / sectorSize) //nolint:gosec // G115 Start is not negative
		extSectors = uint64(ext.Size / sectorSize)  //nolint:gosec // G115 Size is not negative
		ebr        = extFirst
		seen       = make(map[uint64]bool)
	)

	for range maxLogicalLinks {
		if seen[ebr] {
			t.Notes = append(t.Notes, fmt.Sprintf("the logical partitions of partition %d form a loop", ext.Number))

			return t, next, nil
		}

		seen[ebr] = true

		sector, err := read(r, size, int64(ebr)*sectorSize, sectorSize) //nolint:gosec // G115 inside the disk
		if err != nil {
			return t, next, err
		}

		if !bootMark(sector) {
			if ebr != extFirst {
				t.Notes = append(t.Notes, fmt.Sprintf("a link of the logical partitions of partition %d is not valid", ext.Number))
			}

			return t, next, nil
		}

		var link *mbrEntry

		for i, e := range entries(sector) {
			switch {
			case e.empty():
			case e.extended():
				if link == nil {
					link = &e
				}
			case len(t.Partitions) == MaxPartitions:
				return t, next, nil
			case ebr+e.first+e.sectors > extFirst+extSectors && i > 1:
				// Linux ignores the third and fourth entries outside the extended partition: they can hold garbage
			default:
				if p, ok := mbrPartition(&t, e, next, ebr, size); ok {
					p.Logical = true
					t.Partitions = append(t.Partitions, p)
				}

				next++
			}
		}

		if link == nil {
			return t, next, nil
		}

		ebr = extFirst + link.first
		if link.first >= extSectors {
			t.Notes = append(t.Notes, fmt.Sprintf("a link of the logical partitions of partition %d is outside it", ext.Number))

			return t, next, nil
		}
	}

	t.Notes = append(t.Notes, fmt.Sprintf("partition %d has more logical partitions than phenix reads", ext.Number))

	return t, next, nil
}
