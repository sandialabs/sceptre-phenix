// Package partitiontest builds small file system images for tests of the partition package and of its users. It is
// imported only by tests, as net/http/httptest is.
package partitiontest

import (
	"encoding/binary"
	"slices"
)

// The layout of the NTFS volume that NTFS.Bytes builds, in bytes from the start of the volume.
const (
	NTFSSize       = 16 << 10
	NTFSCluster    = 512
	NTFSRecordSize = 1024
	NTFSMFT        = 8 * NTFSCluster
	NTFSLogFile    = NTFSMFT + 2*NTFSRecordSize
	NTFSVolume     = NTFSMFT + 3*NTFSRecordSize
	NTFSLog        = 16 * NTFSCluster
	NTFSPageSize   = 4096

	// NTFSUSAOffset is the offset of the update sequence array in an MFT record and NTFSUSN its sequence number.
	NTFSUSAOffset = 0x30
	NTFSUSN       = 0x0001

	// AttrVolumeInformation and AttrData are the NTFS attribute types $VOLUME_INFORMATION and $DATA.
	AttrVolumeInformation = 0x70
	AttrData              = 0x80
)

const (
	fixupBlock     = 512
	firstAttr      = 0x38
	restartAt      = 0x30
	pageUSA        = 0x1E
	mftMirror      = 2
	recordSizeLog  = 0xF6 // -10: records of 2^10 bytes
	inUse          = 0x0001
	attrEnd        = 0xFFFFFFFF
	attrEndSize    = 8
	residentHeader = 0x18
	nonresHeader   = 0x40
	volInfoSize    = 12
	volInfoFlags   = 0x0A
	ntfsMajor      = 3
	ntfsMinor      = 1
	volumeDirty    = 0x0001
	noClient       = 0xFFFF
	logClean       = 0x0002
	runHeader      = 0x22 // two bytes for the length and two for the cluster
	runSize        = 5
	usnSize        = 2
	alignment      = 8
	logSize        = 2 * NTFSPageSize

	extStart    = 2048
	extSuper    = 1024
	extMagic    = 0xEF53
	extIncompat = 0x42 // extents and flex_bg
)

// Restart is a restart page of $LogFile.
type Restart struct {
	// LSN is the current LSN of the restart area.
	LSN uint64
	// InUse sets a client in the client in-use list.
	InUse bool
	// Clean sets the RESTART_VOLUME_IS_CLEAN flag.
	Clean bool
}

// NTFS describes an NTFS volume of NTFSSize bytes, with only the records and pages that a state check reads: the
// boot sector, the MFT records of $LogFile and $Volume, and two restart pages. The zero value is a clean volume.
type NTFS struct {
	// Dirty sets VOLUME_IS_DIRTY in $VOLUME_INFORMATION.
	Dirty bool
	// First and Second are the restart pages.
	First, Second Restart
	// EmptyLog fills $LogFile with 0xFF bytes, as ntfsfix does, in place of the restart pages.
	EmptyLog bool
}

// Bytes returns the volume.
func (v NTFS) Bytes() []byte {
	b := make([]byte, NTFSSize)
	copy(b, Boot())

	volInfo := make([]byte, volInfoSize)
	volInfo[8], volInfo[9] = ntfsMajor, ntfsMinor

	if v.Dirty {
		binary.LittleEndian.PutUint16(volInfo[volInfoFlags:], volumeDirty)
	}

	logRun := Run(logSize/NTFSCluster, NTFSLog/NTFSCluster)
	copy(b[NTFSLogFile:], FileRecord(NonResident(AttrData, logRun)))
	copy(b[NTFSVolume:], FileRecord(Resident(AttrVolumeInformation, volInfo)))

	if v.EmptyLog {
		copy(b[NTFSLog:], slices.Repeat([]byte{0xFF}, logSize))

		return b
	}

	copy(b[NTFSLog:], RestartPage(v.First))
	copy(b[NTFSLog+NTFSPageSize:], RestartPage(v.Second))

	return b
}

// Boot returns the boot sector of the volume: 512 bytes per sector, one sector per cluster, the MFT at cluster 8
// and MFT records of 1 KiB.
func Boot() []byte {
	b := make([]byte, fixupBlock)
	le := binary.LittleEndian

	b[0], b[1], b[2] = 0xEB, 0x52, 0x90
	copy(b[3:], "NTFS    ")
	le.PutUint16(b[0x0B:], fixupBlock)
	b[0x0D] = NTFSCluster / fixupBlock
	b[0x15] = 0xF8
	le.PutUint64(b[0x28:], NTFSSize/fixupBlock-1)
	le.PutUint64(b[0x30:], NTFSMFT/NTFSCluster)
	le.PutUint64(b[0x38:], mftMirror)
	b[0x40] = recordSizeLog
	b[0x44] = 1
	b[0x1FE], b[0x1FF] = 0x55, 0xAA

	return b
}

// FileRecord returns an MFT record in use that holds attrs, with its fixups applied as on disk.
func FileRecord(attrs ...[]byte) []byte {
	b := make([]byte, NTFSRecordSize)
	le := binary.LittleEndian

	copy(b, "FILE")
	le.PutUint16(b[4:], NTFSUSAOffset)
	le.PutUint16(b[6:], NTFSRecordSize/fixupBlock+1)
	le.PutUint16(b[0x10:], 1)
	le.PutUint16(b[0x12:], 1)
	le.PutUint16(b[0x14:], firstAttr)
	le.PutUint16(b[0x16:], inUse)

	off := firstAttr
	for _, a := range attrs {
		off += copy(b[off:], a)
	}

	le.PutUint32(b[off:], attrEnd)
	le.PutUint32(b[0x18:], uint32(off+attrEndSize))
	le.PutUint32(b[0x1C:], NTFSRecordSize)

	Protect(b, NTFSUSAOffset, NTFSUSN)

	return b
}

// Protect writes usn at the end of each 512-byte block of b, and keeps the bytes it replaces in the update sequence
// array at usa.
func Protect(b []byte, usa int, usn uint16) {
	le := binary.LittleEndian
	le.PutUint16(b[usa:], usn)

	for i := 1; i <= len(b)/fixupBlock; i++ {
		end := i*fixupBlock - usnSize
		copy(b[usa+usnSize*i:usa+usnSize*(i+1)], b[end:end+usnSize])
		le.PutUint16(b[end:], usn)
	}
}

// Resident returns a resident attribute of type typ, with no name, that holds value.
func Resident(typ uint32, value []byte) []byte {
	b := make([]byte, align8(residentHeader+len(value)))
	le := binary.LittleEndian
	le.PutUint32(b[0:], typ)
	le.PutUint32(b[4:], uint32(len(b)))        //nolint:gosec // G115 a small attribute
	le.PutUint32(b[0x10:], uint32(len(value))) //nolint:gosec // G115 a small value
	le.PutUint16(b[0x14:], residentHeader)
	copy(b[residentHeader:], value)

	return b
}

// NonResident returns a non-resident attribute of type typ, with no name, and the data runs runs.
func NonResident(typ uint32, runs []byte) []byte {
	b := make([]byte, align8(nonresHeader+len(runs)+1))
	le := binary.LittleEndian
	le.PutUint32(b[0:], typ)
	le.PutUint32(b[4:], uint32(len(b))) //nolint:gosec // G115 a small attribute
	b[8] = 1
	le.PutUint16(b[0x20:], nonresHeader)
	copy(b[nonresHeader:], runs)

	return b
}

// Run returns one data run of clusters clusters at cluster lcn, with two bytes for each number.
func Run(clusters, lcn uint16) []byte {
	b := make([]byte, runSize)
	b[0] = runHeader
	binary.LittleEndian.PutUint16(b[1:], clusters)
	binary.LittleEndian.PutUint16(b[3:], lcn)

	return b
}

// RestartPage returns a restart page of NTFSPageSize bytes with r in its restart area, with its fixups applied.
func RestartPage(r Restart) []byte {
	b := make([]byte, NTFSPageSize)
	le := binary.LittleEndian

	copy(b, "RSTR")
	le.PutUint16(b[4:], pageUSA)
	le.PutUint16(b[6:], NTFSPageSize/fixupBlock+1)
	le.PutUint32(b[0x10:], NTFSPageSize)
	le.PutUint32(b[0x14:], NTFSPageSize)
	le.PutUint16(b[0x18:], restartAt)
	le.PutUint16(b[0x1A:], 1)
	le.PutUint16(b[0x1C:], 1)

	inUseList := uint16(noClient)
	if r.InUse {
		inUseList = 0
	}

	var flags uint16
	if r.Clean {
		flags = logClean
	}

	area := b[restartAt:]
	le.PutUint64(area[0:], r.LSN)
	le.PutUint16(area[0x08:], 1)
	le.PutUint16(area[0x0A:], noClient)
	le.PutUint16(area[0x0C:], inUseList)
	le.PutUint16(area[0x0E:], flags)

	Protect(b, pageUSA, NTFSUSN)

	return b
}

// Ext returns the first 2 KiB of an ext4 file system whose superblock has the state state: 1 is clean, and the bit
// 0x0002 marks errors.
func Ext(state uint16) []byte {
	b := make([]byte, extStart)
	le := binary.LittleEndian
	le.PutUint16(b[extSuper+0x38:], extMagic)
	le.PutUint16(b[extSuper+0x3A:], state)
	le.PutUint32(b[extSuper+0x60:], extIncompat)

	return b
}

func align8(n int) int {
	return (n + alignment - 1) &^ (alignment - 1)
}
