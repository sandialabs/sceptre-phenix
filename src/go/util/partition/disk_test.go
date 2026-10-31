package partition_test

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"unicode/utf16"
)

// testDisk is a sparse disk image of size bytes for tests: bytes not written read as zeros. It records each read
// outside the disk.
type testDisk struct {
	size     int64
	segments []segment
	outside  []string
}

type segment struct {
	off  int64
	data []byte
}

func newDisk(size int64) *testDisk {
	return &testDisk{size: size}
}

// write puts data at off.
func (d *testDisk) write(off int64, data []byte) *testDisk {
	d.segments = append(d.segments, segment{off: off, data: data})

	return d
}

// bytes returns the whole disk, for a disk small enough to hold in memory.
func (d *testDisk) bytes() []byte {
	b := make([]byte, d.size)
	_, _ = d.ReadAt(b, 0)

	return b
}

func (d *testDisk) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > d.size {
		d.outside = append(d.outside, "read outside the disk")
	}

	if off < 0 || off >= d.size {
		return 0, io.EOF
	}

	n := len(p)
	if rest := d.size - off; int64(n) > rest {
		n = int(rest)
	}

	clear(p[:n])

	for _, s := range d.segments {
		lo, hi := max(s.off, off), min(s.off+int64(len(s.data)), off+int64(n))
		if lo < hi {
			copy(p[lo-off:hi-off], s.data[lo-s.off:hi-s.off])
		}
	}

	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

// mbrEntry is a partition entry of a test MBR or extended boot record.
type mbrEntry struct {
	boot    byte
	typ     byte
	first   uint32
	sectors uint32
}

// mbr returns a boot sector with the disk signature id and the entries.
func mbr(id uint32, entries ...mbrEntry) []byte {
	b := make([]byte, 512)
	binary.LittleEndian.PutUint32(b[440:], id)

	for i, e := range entries {
		at := b[446+16*i:]
		at[0] = e.boot
		at[4] = e.typ
		binary.LittleEndian.PutUint32(at[8:], e.first)
		binary.LittleEndian.PutUint32(at[12:], e.sectors)
	}

	b[510], b[511] = 0x55, 0xAA

	return b
}

// gptEntry is a partition entry of a test GPT.
type gptEntry struct {
	typ, id     [16]byte
	first, last uint64
	attrs       uint64
	name        string
}

// guidBytes returns the on-disk bytes of the GUID s, such as "C12A7328-F81F-11D2-BA4B-00A0C93EC93B".
func guidBytes(s string) [16]byte {
	var (
		g   [16]byte
		hex []byte
	)

	for _, c := range []byte(s) {
		if c != '-' {
			hex = append(hex, c)
		}
	}

	raw := make([]byte, 16)
	for i := range raw {
		raw[i] = unhex(hex[2*i])<<4 | unhex(hex[2*i+1])
	}

	binary.LittleEndian.PutUint32(g[0:], binary.BigEndian.Uint32(raw[0:]))
	binary.LittleEndian.PutUint16(g[4:], binary.BigEndian.Uint16(raw[4:]))
	binary.LittleEndian.PutUint16(g[6:], binary.BigEndian.Uint16(raw[6:]))
	copy(g[8:], raw[8:])

	return g
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	default:
		return c - '0'
	}
}

// gptEntries returns an entry array of 128-byte entries, with room for at least four, padded to whole sectors.
func gptEntries(entries ...gptEntry) []byte {
	b := make([]byte, (max(4, len(entries))*128+511)/512*512)

	for i, e := range entries {
		at := b[128*i:]
		copy(at[0:], e.typ[:])
		copy(at[16:], e.id[:])
		binary.LittleEndian.PutUint64(at[32:], e.first)
		binary.LittleEndian.PutUint64(at[40:], e.last)
		binary.LittleEndian.PutUint64(at[48:], e.attrs)

		// the name field holds 36 UTF-16 units, and a rune past U+FFFF takes two
		units := utf16.Encode([]rune(e.name))
		for j, u := range units[:min(len(units), 36)] {
			binary.LittleEndian.PutUint16(at[56+2*j:], u)
		}
	}

	return b
}

// gptHeader returns the header of a GPT at sector lba, with sectors of ss bytes and its entries at entriesLBA.
func gptHeader(ss int64, lba, alternate, entriesLBA, lastLBA uint64, diskGUID [16]byte, entries []byte) []byte {
	b := make([]byte, ss)
	copy(b, "EFI PART")
	binary.LittleEndian.PutUint32(b[8:], 0x00010000)
	binary.LittleEndian.PutUint32(b[12:], 92)
	binary.LittleEndian.PutUint64(b[24:], lba)
	binary.LittleEndian.PutUint64(b[32:], alternate)
	binary.LittleEndian.PutUint64(b[40:], 34)
	binary.LittleEndian.PutUint64(b[48:], lastLBA-33)
	copy(b[56:], diskGUID[:])
	binary.LittleEndian.PutUint64(b[72:], entriesLBA)
	binary.LittleEndian.PutUint32(b[80:], uint32(len(entries)/128))
	binary.LittleEndian.PutUint32(b[84:], 128)
	binary.LittleEndian.PutUint32(b[88:], crc32.ChecksumIEEE(entries))
	binary.LittleEndian.PutUint32(b[16:], crc32.ChecksumIEEE(b[:92]))

	return b
}

// gptDisk returns a disk of size bytes with sectors of ss bytes holding a protective MBR, and a primary and backup
// GPT of the entries with the disk GUID diskGUID.
func gptDisk(size, ss int64, entries ...gptEntry) *testDisk {
	var (
		array    = gptEntries(entries...)
		last     = uint64(size/ss) - 1
		backupAt = last - (uint64(len(array))+uint64(ss)-1)/uint64(ss)
		g        = guidBytes(diskGUID)
	)

	sectors := uint32(min(uint64(size/ss)-1, 0xFFFFFFFF))

	return newDisk(size).
		write(0, mbr(0, mbrEntry{typ: 0xEE, first: 1, sectors: sectors})).
		write(ss, gptHeader(ss, 1, last, 2, last, g, array)).
		write(2*ss, array).
		write(int64(backupAt)*ss, array).
		write(int64(last)*ss, gptHeader(ss, last, 1, backupAt, last, g, array))
}

// The file systems' magic bytes, at the start of a partition.

func extSuperblock(compat, incompat, roCompat uint32, label string) []byte {
	b := make([]byte, 2048)
	binary.LittleEndian.PutUint16(b[1024+0x38:], 0xEF53)
	binary.LittleEndian.PutUint32(b[1024+0x5C:], compat)
	binary.LittleEndian.PutUint32(b[1024+0x60:], incompat)
	binary.LittleEndian.PutUint32(b[1024+0x64:], roCompat)
	copy(b[1024+0x78:], label)

	return b
}

func at(off int, magic string) []byte {
	b := make([]byte, off+len(magic))
	copy(b[off:], magic)

	return b
}

func fatBootSector(fat32 bool, totalSectors uint32, label string) []byte {
	b := make([]byte, 512)
	b[0], b[1], b[2] = 0xEB, 0x3C, 0x90
	copy(b[3:], "mkfs.fat")
	binary.LittleEndian.PutUint16(b[0x0B:], 512)
	b[0x0D] = 4
	binary.LittleEndian.PutUint16(b[0x0E:], 4)
	b[0x10] = 2
	b[0x15] = 0xF8
	binary.LittleEndian.PutUint32(b[0x20:], totalSectors)

	if fat32 {
		binary.LittleEndian.PutUint32(b[0x24:], 1000)
		b[0x42] = 0x29
		copy(b[0x47:], label+"           "[len(label):])
	} else {
		binary.LittleEndian.PutUint16(b[0x11:], 512)
		binary.LittleEndian.PutUint16(b[0x16:], 64)
		b[0x26] = 0x29
		copy(b[0x2B:], label+"           "[len(label):])
	}

	b[510], b[511] = 0x55, 0xAA

	return b
}
