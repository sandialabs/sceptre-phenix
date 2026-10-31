package partition

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"unicode/utf16"
)

const (
	gptSignature    = "EFI PART"
	gptMinHeader    = 92
	gptMinEntry     = 128
	gptEntryAlign   = 8
	gptMaxEntries   = 512 << 10 // bytes of entries Read reads at most
	gptNameAt       = 56
	gptNameLength   = 72
	gptBIOSBootable = 1 << 2
	largeSectorSize = 4096
	guidSize        = 16
)

// gptHeader is the part of a GPT header that Read uses.
type gptHeader struct {
	diskGUID   string
	entriesLBA uint64
	count      uint64
	entrySize  uint64
	entriesCRC uint32
}

// readGPT adds to t the GPT partitions of the disk r holds, from the primary header or, when it is damaged, from the
// backup header at the end of the disk. It tries a sector size of 512 bytes, then 4096.
func readGPT(r io.ReaderAt, size int64, t Table) (Table, error) {
	for _, ss := range []int64{sectorSize, largeSectorSize} {
		primary, ok, err := gptAt(r, size, ss, 1)
		if err != nil {
			return t, err
		}

		if !ok {
			continue
		}

		if parts, ok, err := gptEntries(r, size, ss, primary, &t); err != nil || ok {
			return withGPT(t, ss, primary, parts), err
		}

		break
	}

	// the backup header is in the last sector of the disk
	for _, ss := range []int64{sectorSize, largeSectorSize} {
		last := uint64(size/ss) - 1 //nolint:gosec // G115 size is at least one sector

		backup, ok, err := gptAt(r, size, ss, last)
		if err != nil {
			return t, err
		}

		if !ok {
			continue
		}

		if parts, ok, err := gptEntries(r, size, ss, backup, &t); err != nil || ok {
			t.Notes = append(t.Notes, "the primary GPT is damaged, so phenix read the backup GPT at the end of the disk")

			return withGPT(t, ss, backup, parts), err
		}
	}

	t.Scheme = SchemeUnknown
	t.Notes = append(t.Notes, "the disk has a GPT protective MBR but no valid GPT")

	return t, nil
}

func withGPT(t Table, ss int64, h gptHeader, parts []Partition) Table {
	t.Scheme = SchemeGPT
	t.SectorSize = ss
	t.DiskID = h.diskGUID
	t.Partitions = parts

	return t
}

// gptAt reads the GPT header at sector lba for sectors of ss bytes, and reports whether it is valid: its signature,
// sizes and checksum are good, and it says it is at lba.
func gptAt(r io.ReaderAt, size, ss int64, lba uint64) (gptHeader, bool, error) {
	if lba > uint64(size/ss) { //nolint:gosec // G115 size is not negative
		return gptHeader{}, false, nil
	}

	buf, err := read(r, size, int64(lba)*ss, int(ss)) //nolint:gosec // G115 lba is inside the disk
	if err != nil || int64(len(buf)) < ss || !bytes.HasPrefix(buf, []byte(gptSignature)) {
		return gptHeader{}, false, err
	}

	le := binary.LittleEndian

	length := le.Uint32(buf[12:])
	if length < gptMinHeader || int64(length) > ss || le.Uint64(buf[24:]) != lba {
		return gptHeader{}, false, nil
	}

	header := bytes.Clone(buf[:length])
	clear(header[16:20])

	if crc32.ChecksumIEEE(header) != le.Uint32(buf[16:]) {
		return gptHeader{}, false, nil
	}

	h := gptHeader{
		diskGUID:   guid(buf[56:]),
		entriesLBA: le.Uint64(buf[72:]),
		count:      uint64(le.Uint32(buf[80:])),
		entrySize:  uint64(le.Uint32(buf[84:])),
		entriesCRC: le.Uint32(buf[88:]),
	}

	if h.entrySize < gptMinEntry || h.entrySize%gptEntryAlign != 0 || h.count*h.entrySize > gptMaxEntries {
		return gptHeader{}, false, nil
	}

	return h, true, nil
}

// gptEntries returns the partitions in the entries h names, and false when the entries are outside the disk or their
// checksum is wrong. It adds a note to t for a partition it leaves out or cuts.
func gptEntries(r io.ReaderAt, size, ss int64, h gptHeader, t *Table) ([]Partition, bool, error) {
	n := h.count * h.entrySize
	if h.entriesLBA > uint64(size/ss) || n > uint64(size)-h.entriesLBA*uint64(ss) { //nolint:gosec // G115 not negative
		return nil, false, nil
	}

	buf, err := read(r, size, int64(h.entriesLBA)*ss, int(n)) //nolint:gosec // G115 inside the disk
	if err != nil || uint64(len(buf)) < n || crc32.ChecksumIEEE(buf) != h.entriesCRC {
		return nil, false, err
	}

	parts := []Partition{}
	le := binary.LittleEndian

	for i := range h.count {
		e := buf[i*h.entrySize : (i+1)*h.entrySize]
		if allZero(e[:guidSize]) {
			continue
		}

		number := int(i) + 1

		first, last := le.Uint64(e[32:]), le.Uint64(e[40:])
		if last < first {
			t.Notes = append(t.Notes, fmt.Sprintf("partition %d ends before it starts", number))

			continue
		}

		start, length, cut, ok := span(first, last-first+1, ss, size)

		switch {
		case !ok:
			t.Notes = append(t.Notes, fmt.Sprintf("partition %d starts past the end of the disk", number))

			continue
		case cut:
			t.Notes = append(t.Notes, fmt.Sprintf("partition %d ends past the end of the disk, so phenix shows it cut", number))
		}

		if len(parts) == MaxPartitions {
			t.Notes = append(t.Notes, fmt.Sprintf("phenix shows the first %d partitions only", MaxPartitions))

			break
		}

		typeID := guid(e)
		parts = append(parts, Partition{
			Number: number, Start: start, Size: length, Type: gptTypeName(typeID), TypeID: typeID,
			Bootable: le.Uint64(e[48:])&gptBIOSBootable != 0, Name: gptName(e[gptNameAt : gptNameAt+gptNameLength]),
			GUID: guid(e[guidSize:]), Extended: false, Logical: false, Filesystem: nil,
		})
	}

	return parts, true, nil
}

// guid formats the mixed-endian GUID at the start of b in upper case.
func guid(b []byte) string {
	le := binary.LittleEndian

	return fmt.Sprintf("%08X-%04X-%04X-%X-%X", le.Uint32(b), le.Uint16(b[4:]), le.Uint16(b[6:]), b[8:10], b[10:16])
}

// gptName decodes a GPT partition name, UTF-16LE up to the first NUL.
func gptName(b []byte) string {
	units := make([]uint16, 0, len(b)/2) //nolint:mnd // two bytes per unit

	for i := 0; i+1 < len(b); i += 2 {
		u := binary.LittleEndian.Uint16(b[i:])
		if u == 0 {
			break
		}

		units = append(units, u)
	}

	return string(utf16.Decode(units))
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}

	return true
}
