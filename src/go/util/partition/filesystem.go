package partition

import (
	"bytes"
	"encoding/binary"
	"io"
	"strconv"
	"strings"
)

// The file system types Probe returns, as blkid names them.
const (
	FSUnknown  = "unknown"
	FSExt2     = "ext2"
	FSExt3     = "ext3"
	FSExt4     = "ext4"
	FSXFS      = "xfs"
	FSBtrfs    = "btrfs"
	FSNTFS     = "ntfs"
	FSExFAT    = "exfat"
	FSVFAT     = "vfat"
	FSSwap     = "swap"
	FSLVM      = "LVM2_member"
	FSLUKS     = "crypto_LUKS"
	FSISO9660  = "iso9660"
	FSSquashFS = "squashfs"
)

// ProbeSize is the most bytes Probe reads.
const ProbeSize = 68 << 10

const (
	extSuperblock   = 1024
	extMagicAt      = extSuperblock + 0x38
	extCompatAt     = extSuperblock + 0x5C
	extIncompatAt   = extSuperblock + 0x60
	extROCompatAt   = extSuperblock + 0x64
	extLabelAt      = extSuperblock + 0x78
	extLabelLength  = 16
	extMagic        = 0xEF53
	extHasJournal   = 0x0004
	ext3Incompat    = 0x0002 | 0x0004 | 0x0010 // filetype, recover, meta_bg
	ext3ROCompat    = 0x0001 | 0x0002 | 0x0004 // sparse_super, large_file, btree_dir
	extJournalDev   = 0x0008
	xfsLabelAt      = 0x6C
	xfsLabelLength  = 12
	btrfsSuperblock = 0x10000
	btrfsMagicAt    = btrfsSuperblock + 0x40
	btrfsLabelAt    = btrfsSuperblock + 0x12B
	btrfsLabelLen   = 256
	swapLabelAt     = 1024 + 0x1C
	swapLabelLength = 16
	swapMagicLength = 10
	luksLabelAt     = 0x18
	luksLabelLength = 48
	luksVersionAt   = 6
	isoMagicAt      = 0x8001
	isoLabelAt      = 0x8028
	isoLabelLength  = 32
	oemNameAt       = 3
	lvmLabelSectors = 4
	lvmTypeAt       = 24
	fatLabelAt16    = 0x2B
	fatLabelAt32    = 0x47
	fatLabelLength  = 11
	fatSigAt16      = 0x26
	fatSigAt32      = 0x42
	fatExtendedSig  = 0x29
	fat12Clusters   = 4085
	fatEntrySize    = 32
)

// Filesystem is the file system Probe found.
type Filesystem struct {
	// Type is the file system type as blkid names it, such as "ext4" or "vfat", or FSUnknown.
	Type string
	// Version is "FAT12", "FAT16" or "FAT32" for vfat, the LUKS version for crypto_LUKS, or "".
	Version string
	// Label is the file system label, or "".
	Label string
}

// Probe returns the file system in the size bytes at start of the disk r holds, from the magic bytes in its first
// ProbeSize bytes. It returns an error only when r fails with an error other than [io.EOF].
func Probe(r io.ReaderAt, start, size int64) (Filesystem, error) {
	if size < 0 || start < 0 {
		return Filesystem{Type: FSUnknown, Version: "", Label: ""}, nil
	}

	b, err := read(r, start+min(size, ProbeSize), start, ProbeSize)
	if err != nil {
		return Filesystem{Type: FSUnknown, Version: "", Label: ""}, err
	}

	return detect(b), nil
}

// detect returns the file system whose magic bytes b, the start of a partition, holds.
func detect(b []byte) Filesystem {
	fs := Filesystem{Type: FSUnknown, Version: "", Label: ""}

	switch {
	case has(b, 0, "LUKS\xba\xbe") && len(b) >= luksVersionAt+2:
		fs.Type = FSLUKS
		fs.Version = strconv.Itoa(int(binary.BigEndian.Uint16(b[luksVersionAt:])))

		if fs.Version == "2" {
			fs.Label = label(b, luksLabelAt, luksLabelLength)
		}
	case has(b, 0, "XFSB"):
		fs.Type, fs.Label = FSXFS, label(b, xfsLabelAt, xfsLabelLength)
	case lvm(b):
		fs.Type = FSLVM
	case swap(b):
		fs.Type, fs.Label = FSSwap, label(b, swapLabelAt, swapLabelLength)
	case has(b, btrfsMagicAt, "_BHRfS_M"):
		fs.Type, fs.Label = FSBtrfs, label(b, btrfsLabelAt, btrfsLabelLen)
	case len(b) >= extLabelAt+extLabelLength && binary.LittleEndian.Uint16(b[extMagicAt:]) == extMagic:
		fs.Type, fs.Label = ext(b), label(b, extLabelAt, extLabelLength)
	case has(b, isoMagicAt, "CD001"):
		fs.Type, fs.Label = FSISO9660, label(b, isoLabelAt, isoLabelLength)
	case has(b, 0, "hsqs"):
		fs.Type = FSSquashFS
	case has(b, oemNameAt, "NTFS    "):
		fs.Type = FSNTFS
	case has(b, oemNameAt, "EXFAT   "):
		fs.Type = FSExFAT
	default:
		if version, ok := fat(b); ok {
			fs.Type, fs.Version, fs.Label = FSVFAT, version, fatLabel(b, version)
		}
	}

	return fs
}

// bootSector reports whether sector, the first of a disk, is the boot sector of a FAT, NTFS or exFAT file system
// rather than an MBR.
func bootSector(sector []byte) bool {
	_, isFAT := fat(sector)

	return isFAT || has(sector, oemNameAt, "NTFS    ") || has(sector, oemNameAt, "EXFAT   ")
}

// has reports whether b holds magic at off.
func has(b []byte, off int, magic string) bool {
	return off >= 0 && len(b) >= off+len(magic) && string(b[off:off+len(magic)]) == magic
}

// label returns the label of n bytes at off in b, cut at the first NUL and without the spaces that pad it.
func label(b []byte, off, n int) string {
	if len(b) < off+n {
		return ""
	}

	l, _, _ := bytes.Cut(b[off:off+n], []byte{0})

	return strings.ToValidUTF8(strings.TrimRight(string(l), " "), "")
}

// ext tells ext2, ext3 and ext4 apart by their feature bits as blkid does.
func ext(b []byte) string {
	le := binary.LittleEndian
	compat, incompat, roCompat := le.Uint32(b[extCompatAt:]), le.Uint32(b[extIncompatAt:]), le.Uint32(b[extROCompatAt:])

	switch {
	case incompat&^(ext3Incompat|extJournalDev) != 0 || roCompat&^ext3ROCompat != 0:
		return FSExt4
	case compat&extHasJournal != 0:
		return FSExt3
	default:
		return FSExt2
	}
}

// lvm reports whether one of the first four sectors of b holds an LVM2 physical volume label.
func lvm(b []byte) bool {
	for s := range lvmLabelSectors {
		if has(b, s*sectorSize, "LABELONE") && has(b, s*sectorSize+lvmTypeAt, "LVM2 001") {
			return true
		}
	}

	return false
}

// swap reports whether b holds a Linux swap signature at the end of its first page, for page sizes up to 64 KiB.
func swap(b []byte) bool {
	for _, page := range []int{4 << 10, 8 << 10, 16 << 10, 64 << 10} {
		if has(b, page-swapMagicLength, "SWAPSPACE2") || has(b, page-swapMagicLength, "SWAP-SPACE") {
			return true
		}
	}

	return false
}

// fat returns "FAT12", "FAT16" or "FAT32" when b starts with the boot sector of a FAT file system, from its BIOS
// parameter block: a jump instruction, a sector size, a power of two sectors per cluster, a reserved sector, one or
// two FATs and a media byte.
func fat(b []byte) (string, bool) {
	if len(b) < sectorSize || (b[0] != 0xEB && b[0] != 0xE9) {
		return "", false
	}

	le := binary.LittleEndian

	var (
		bps         = uint64(le.Uint16(b[0x0B:]))
		spc         = uint64(b[0x0D])
		reserved    = uint64(le.Uint16(b[0x0E:]))
		fats        = uint64(b[0x10])
		rootEntries = uint64(le.Uint16(b[0x11:]))
		total       = uint64(le.Uint16(b[0x13:]))
		media       = b[0x15]
		fatSize     = uint64(le.Uint16(b[0x16:]))
	)

	switch {
	case bps != 512 && bps != 1024 && bps != 2048 && bps != 4096:
		return "", false
	case spc == 0 || spc&(spc-1) != 0, reserved == 0, fats == 0 || fats > 2:
		return "", false
	case media != 0xF0 && media < 0xF8:
		return "", false
	}

	if total == 0 {
		total = uint64(le.Uint32(b[0x20:]))
	}

	if fatSize == 0 && rootEntries == 0 {
		return "FAT32", true
	}

	rootSectors := (rootEntries*fatEntrySize + bps - 1) / bps
	used := reserved + fats*fatSize + rootSectors

	if total <= used {
		return "", false
	}

	if (total-used)/spc < fat12Clusters {
		return "FAT12", true
	}

	return "FAT16", true
}

// fatLabel returns the volume label in the FAT boot sector b of the version fat returned, or "" for none.
func fatLabel(b []byte, version string) string {
	sigAt, at := fatSigAt16, fatLabelAt16
	if version == "FAT32" {
		sigAt, at = fatSigAt32, fatLabelAt32
	}

	if len(b) <= sigAt || b[sigAt] != fatExtendedSig {
		return ""
	}

	if l := label(b, at, fatLabelLength); l != "NO NAME" {
		return l
	}

	return ""
}
