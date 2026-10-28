package partition

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"slices"
	"strings"
)

// The reasons CheckNTFS and CheckExt give in VolumeState.Reason.
const (
	ReasonNTFSDirty    = "the NTFS volume is marked dirty"
	ReasonNTFSLog      = "Windows did not shut down fully: the NTFS log is not clean (hibernation or Fast Startup)"
	ReasonExtHasErrors = "the file system is marked with errors that e2fsck did not repair"
)

// NTFSReadMax is the most bytes that CheckNTFS reads.
const NTFSReadMax = sectorSize + 2*ntfsRecordMax + 2*(ntfsPageMin+ntfsPageMax)

const (
	ntfsClusterMax = 2 << 20
	ntfsRecordMin  = 512
	ntfsRecordMax  = 4096
	ntfsPageMin    = 512
	ntfsPageMax    = 64 << 10
	ntfsFixupBlock = 512
	ntfsMFTLogFile = 2
	ntfsMFTVolume  = 3

	ntfsBytesPerSectorAt = 0x0B
	ntfsSectorsAt        = 0x0D
	ntfsMFTClusterAt     = 0x30
	ntfsRecordSizeAt     = 0x40

	ntfsInUse        = 0x0001
	ntfsAttrVolInfo  = 0x70
	ntfsAttrData     = 0x80
	ntfsAttrEnd      = 0xFFFFFFFF
	ntfsVolumeDirty  = 0x0001
	ntfsLogClean     = 0x0002
	ntfsLogNoClient  = 0xFFFF
	ntfsVolInfoSize  = 12
	ntfsRestartSize  = 0x10
	ntfsAttrHeader   = 16
	ntfsResidentSize = 0x18
	ntfsNonresSize   = 0x40
	ntfsRecordHeader = 0x30

	ntfsSectorMin      = 256
	ntfsSectorMax      = 4096
	ntfsSPCNegative    = 0x80 // a sectors per cluster value over this is a negative power of two
	ntfsByteValues     = 256
	ntfsClusterLog2Max = 21
	ntfsRecordLog2Max  = 16
	ntfsHeaderMin      = 8
	ntfsUSNSize        = 2
	ntfsRunSizeMask    = 0x0F
	ntfsRunSizeShift   = 4
	ntfsNumberMax      = 8
	ntfsDefaultPage    = 4096
	ntfsEmptyByte      = 0xFF
	ntfsAreaMin        = 0x1E
	ntfsAlign          = 8
	byteBits           = 8
	uint64Bits         = 64

	extStateAt    = extSuperblock + 0x3A
	extErrorState = 0x0002
)

var (
	errNTFS     = errors.New("not a valid NTFS volume")
	errCutShort = errors.New("the file system is cut short")
)

// VolumeState is the state of a file system that Linux reads before it mounts it (see CheckNTFS and CheckExt).
type VolumeState struct {
	// Dirty is set when the NTFS volume has the VOLUME_IS_DIRTY flag, which chkdsk /f clears.
	Dirty bool
	// LogNotClean is set when the NTFS $LogFile has a client and no clean flag: Windows did not shut down fully.
	LogNotClean bool
	// HasErrors is set when an ext2, ext3 or ext4 file system has the error state.
	HasErrors bool
	// Reason says why the volume is not clean, or is "" when it is clean.
	Reason string
}

// CheckNTFS reads the state of the NTFS volume in the size bytes of r, as ntfs-3g does before a read-write mount.
// It reads the boot sector, the MFT records of $LogFile and $Volume, and the restart pages of $LogFile, at most
// NTFSReadMax bytes. It does not look for hiberfil.sys. It returns an error for bad data, or when r fails.
func CheckNTFS(r io.ReaderAt, size int64) (VolumeState, error) {
	var state VolumeState

	boot, err := readExact(r, size, 0, sectorSize)
	if err != nil {
		return state, err
	}

	geo, err := ntfsGeometry(boot, size)
	if err != nil {
		return state, err
	}

	volume, err := geo.mftRecord(r, size, ntfsMFTVolume)
	if err != nil {
		return state, fmt.Errorf("$Volume: %w", err)
	}

	flags, err := volumeFlags(volume)
	if err != nil {
		return state, fmt.Errorf("$Volume: %w", err)
	}

	logFile, err := geo.mftRecord(r, size, ntfsMFTLogFile)
	if err != nil {
		return state, fmt.Errorf("$LogFile: %w", err)
	}

	start, length, err := geo.firstRun(logFile, size)
	if err != nil {
		return state, fmt.Errorf("$LogFile: %w", err)
	}

	clean, err := logClean(r, size, start, length)
	if err != nil {
		return state, fmt.Errorf("$LogFile: %w", err)
	}

	state.Dirty = flags&ntfsVolumeDirty != 0
	state.LogNotClean = !clean

	var reasons []string
	if state.Dirty {
		reasons = append(reasons, ReasonNTFSDirty)
	}

	if state.LogNotClean {
		reasons = append(reasons, ReasonNTFSLog)
	}

	state.Reason = strings.Join(reasons, ", and ")

	return state, nil
}

// CheckExt reads the state of the ext2, ext3 or ext4 file system in the size bytes of r from its superblock. It does
// not report a journal that needs recovery, because a read-write mount replays it.
func CheckExt(r io.ReaderAt, size int64) (VolumeState, error) {
	var state VolumeState

	b, err := readExact(r, size, extSuperblock, extStateAt+2-extSuperblock)
	if err != nil {
		return state, err
	}

	if binary.LittleEndian.Uint16(b[extMagicAt-extSuperblock:]) != extMagic {
		return state, errors.New("no ext2, ext3 or ext4 superblock")
	}

	if binary.LittleEndian.Uint16(b[extStateAt-extSuperblock:])&extErrorState != 0 {
		state.HasErrors, state.Reason = true, ReasonExtHasErrors
	}

	return state, nil
}

// ntfsGeo is the layout of an NTFS volume from its boot sector.
type ntfsGeo struct {
	cluster, record, mft int64
}

// ntfsGeometry reads the layout from boot, the boot sector of a volume of size bytes.
func ntfsGeometry(boot []byte, size int64) (ntfsGeo, error) {
	var geo ntfsGeo

	if !has(boot, oemNameAt, "NTFS    ") {
		return geo, fmt.Errorf("%w: no NTFS boot sector", errNTFS)
	}

	le := binary.LittleEndian
	bps := int64(le.Uint16(boot[ntfsBytesPerSectorAt:]))

	if bps < ntfsSectorMin || bps > ntfsSectorMax || bits.OnesCount64(uint64(bps)) != 1 {
		return geo, fmt.Errorf("%w: %d bytes per sector", errNTFS, bps)
	}

	// a value over 0x80 is a negative power of two, for clusters over 64 KiB
	spc := int64(boot[ntfsSectorsAt])
	if spc > ntfsSPCNegative {
		shift := ntfsByteValues - spc
		if shift > ntfsClusterLog2Max {
			return geo, fmt.Errorf("%w: sectors per cluster value %d", errNTFS, spc)
		}

		spc = 1 << shift
	}

	geo.cluster = bps * spc
	if spc == 0 || bits.OnesCount64(uint64(spc)) != 1 || geo.cluster > ntfsClusterMax {
		return geo, fmt.Errorf("%w: %d sectors per cluster", errNTFS, spc)
	}

	// a negative value is the record size as a power of two
	switch v := int64(int8(boot[ntfsRecordSizeAt])); {
	case v > 0:
		geo.record = v * geo.cluster
	case v < 0 && v >= -ntfsRecordLog2Max:
		geo.record = 1 << -v
	}

	if geo.record < ntfsRecordMin || geo.record > ntfsRecordMax || bits.OnesCount64(uint64(geo.record)) != 1 {
		return geo, fmt.Errorf("%w: an MFT record size of %d bytes", errNTFS, geo.record)
	}

	lcn := le.Uint64(boot[ntfsMFTClusterAt:])
	if lcn == 0 || lcn >= uint64(size/geo.cluster) { //nolint:gosec // G115 size is not negative
		return geo, fmt.Errorf("%w: the MFT starts at cluster %d, which is not in the volume", errNTFS, lcn)
	}

	geo.mft = int64(lcn) * geo.cluster //nolint:gosec // G115 lcn is under size / cluster

	return geo, nil
}

// mftRecord reads MFT record n with its fixups applied.
func (g ntfsGeo) mftRecord(r io.ReaderAt, size int64, n int64) ([]byte, error) {
	if g.mft > math.MaxInt64-(n+1)*g.record {
		return nil, fmt.Errorf("%w: MFT record %d is past the end of the volume", errNTFS, n)
	}

	b, err := readExact(r, size, g.mft+n*g.record, int(g.record))
	if err != nil {
		return nil, err
	}

	if !has(b, 0, "FILE") {
		return nil, fmt.Errorf("%w: MFT record %d has no FILE mark", errNTFS, n)
	}

	if err := applyFixups(b); err != nil {
		return nil, fmt.Errorf("MFT record %d: %w", n, err)
	}

	if binary.LittleEndian.Uint16(b[0x16:])&ntfsInUse == 0 {
		return nil, fmt.Errorf("%w: MFT record %d is not in use", errNTFS, n)
	}

	return b, nil
}

// applyFixups puts back the last two bytes of each 512-byte block of b, a multi-sector record, from its update
// sequence array. It returns an error when a block does not end with the update sequence number: a torn write.
func applyFixups(b []byte) error {
	le := binary.LittleEndian

	if len(b) < ntfsFixupBlock || len(b)%ntfsFixupBlock != 0 {
		return fmt.Errorf("%w: a record of %d bytes", errNTFS, len(b))
	}

	usa, count := int(le.Uint16(b[4:])), int(le.Uint16(b[6:]))
	if count != len(b)/ntfsFixupBlock+1 || usa < ntfsHeaderMin || usa%ntfsUSNSize != 0 ||
		usa+ntfsUSNSize*count > ntfsFixupBlock-ntfsUSNSize {
		return fmt.Errorf("%w: an update sequence array of %d entries at %d", errNTFS, count, usa)
	}

	for i := 1; i < count; i++ {
		end := i*ntfsFixupBlock - ntfsUSNSize
		if b[end] != b[usa] || b[end+1] != b[usa+1] {
			return fmt.Errorf("%w: block %d does not match the update sequence number", errNTFS, i-1)
		}

		b[end], b[end+1] = b[usa+ntfsUSNSize*i], b[usa+ntfsUSNSize*i+1]
	}

	return nil
}

// attribute returns the first attribute of type typ with no name in rec, an MFT record, or an error.
func attribute(rec []byte, typ uint32) ([]byte, error) {
	le := binary.LittleEndian
	end := min(int(le.Uint32(rec[0x18:])), len(rec))
	off := int(le.Uint16(rec[0x14:]))

	if off < ntfsRecordHeader-8 || off >= end {
		return nil, fmt.Errorf("%w: the first attribute is at %d", errNTFS, off)
	}

	for off+4 <= end {
		t := le.Uint32(rec[off:])
		if t == ntfsAttrEnd {
			break
		}

		if off+ntfsAttrHeader > end {
			return nil, fmt.Errorf("%w: an attribute runs past the end of the record", errNTFS)
		}

		length := int(le.Uint32(rec[off+4:]))
		if length < ntfsAttrHeader || length > end-off {
			return nil, fmt.Errorf("%w: an attribute of %d bytes at %d", errNTFS, length, off)
		}

		if t == typ && rec[off+9] == 0 {
			return rec[off : off+length], nil
		}

		off += length
	}

	return nil, fmt.Errorf("%w: no attribute 0x%x", errNTFS, typ)
}

// volumeFlags returns the flags of the $VOLUME_INFORMATION attribute of rec, the $Volume record.
func volumeFlags(rec []byte) (uint16, error) {
	a, err := attribute(rec, ntfsAttrVolInfo)
	if err != nil {
		return 0, err
	}

	if a[8] != 0 || len(a) < ntfsResidentSize {
		return 0, fmt.Errorf("%w: $VOLUME_INFORMATION is not resident", errNTFS)
	}

	le := binary.LittleEndian
	n, off := int64(le.Uint32(a[0x10:])), int64(le.Uint16(a[0x14:]))

	if n < ntfsVolInfoSize || off+n > int64(len(a)) {
		return 0, fmt.Errorf("%w: $VOLUME_INFORMATION of %d bytes at %d", errNTFS, n, off)
	}

	return le.Uint16(a[off+0x0A:]), nil
}

// firstRun returns the offset and length in bytes of the first data run of the unnamed $DATA attribute of rec, cut
// to the volume of size bytes.
func (g ntfsGeo) firstRun(rec []byte, size int64) (int64, int64, error) {
	a, err := attribute(rec, ntfsAttrData)
	if err != nil {
		return 0, 0, err
	}

	if a[8] != 1 || len(a) < ntfsNonresSize {
		return 0, 0, fmt.Errorf("%w: $DATA is not non-resident", errNTFS)
	}

	le := binary.LittleEndian
	if le.Uint64(a[0x10:]) != 0 {
		return 0, 0, fmt.Errorf("%w: $DATA does not start at cluster 0", errNTFS)
	}

	off := int(le.Uint16(a[0x20:]))
	if off < ntfsNonresSize || off >= len(a) {
		return 0, 0, fmt.Errorf("%w: the data runs are at %d", errNTFS, off)
	}

	run := a[off:]
	lenSize, lcnSize := int(run[0]&ntfsRunSizeMask), int(run[0]>>ntfsRunSizeShift)

	if lenSize == 0 || lenSize > ntfsNumberMax || lcnSize == 0 || lcnSize > ntfsNumberMax || 1+lenSize+lcnSize > len(run) {
		return 0, 0, fmt.Errorf("%w: the first data run has the header 0x%02x", errNTFS, run[0])
	}

	clusters := leUint(run[1 : 1+lenSize])
	lcn := leInt(run[1+lenSize : 1+lenSize+lcnSize])
	volume := uint64(size / g.cluster) //nolint:gosec // G115 size is not negative

	if clusters == 0 || lcn <= 0 || uint64(lcn) >= volume {
		return 0, 0, fmt.Errorf("%w: the first data run is %d clusters at cluster %d, which is not in the volume", errNTFS, clusters, lcn)
	}

	clusters = min(clusters, volume-uint64(lcn))

	return lcn * g.cluster, int64(clusters) * g.cluster, nil //nolint:gosec // G115 both are under size / cluster
}

// leUint is the little-endian unsigned number in b, of at most 8 bytes.
func leUint(b []byte) uint64 {
	var v uint64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<byteBits | uint64(b[i])
	}

	return v
}

// leInt is the little-endian signed number in b, of 1 to 8 bytes.
func leInt(b []byte) int64 {
	shift := uint64Bits - byteBits*len(b)

	return int64(leUint(b)<<shift) >> shift //nolint:gosec // G115 the cast keeps the sign bit, on purpose
}

// restartPage is what logClean reads from one restart page of $LogFile.
type restartPage struct {
	pageSize int64
	lsn      uint64
	clean    bool
}

// logClean reports whether $LogFile, at start in length bytes, is clean, as ntfs-3g reads it: an empty log, or a
// restart area with no client in use or with the clean flag. It uses the restart page with the newest LSN.
func logClean(r io.ReaderAt, size, start, length int64) (bool, error) {
	first, empty, err := readRestart(r, size, start, length)
	if err != nil || empty {
		return empty, err
	}

	// the second restart page is one system page after the first; without the first, ntfs-3g tries 4 KiB
	at := int64(ntfsDefaultPage)
	if first != nil {
		at = first.pageSize
	}

	var second *restartPage
	if at < length {
		second, _, err = readRestart(r, size, start+at, length-at)
		if err != nil {
			second = nil
		}
	}

	switch {
	case first == nil && second == nil:
		return false, fmt.Errorf("%w: no valid restart page", errNTFS)
	case first == nil:
		return second.clean, nil
	case second != nil && second.lsn > first.lsn:
		return second.clean, nil
	}

	return first.clean, nil
}

// readRestart reads the restart page at off, of at most length bytes. It returns nil for a page that is not a valid
// restart page, empty for a page that holds only 0xFF bytes (an emptied log), and an error only when r fails.
func readRestart(r io.ReaderAt, size, off, length int64) (*restartPage, bool, error) {
	head, err := readExact(r, size, off, int(min(ntfsPageMin, length)))
	if err != nil {
		return nil, false, err
	}

	if !slices.ContainsFunc(head, func(c byte) bool { return c != ntfsEmptyByte }) {
		return nil, true, nil
	}

	chkdsk := has(head, 0, "CHKD")
	if (!chkdsk && !has(head, 0, "RSTR")) || len(head) < ntfsPageMin {
		return nil, false, nil
	}

	le := binary.LittleEndian
	pageSize := int64(le.Uint32(head[0x10:]))

	if pageSize < ntfsPageMin || pageSize > ntfsPageMax || pageSize > length || bits.OnesCount64(uint64(pageSize)) != 1 {
		return nil, false, nil
	}

	// chkdsk writes CHKD restart pages; ntfs-3g mounts such a volume, so it counts as clean
	if chkdsk {
		return &restartPage{pageSize: pageSize, lsn: 0, clean: true}, false, nil
	}

	page, err := readExact(r, size, off, int(pageSize))
	if err != nil {
		return nil, false, err
	}

	ra := int64(le.Uint16(page[0x18:]))
	if !fixed(page) || ra%ntfsAlign != 0 || ra < ntfsAreaMin || ra+ntfsRestartSize > pageSize {
		return nil, false, nil
	}

	area := page[ra:]
	inUse, flags := le.Uint16(area[0x0C:]), le.Uint16(area[0x0E:])

	return &restartPage{
		pageSize: pageSize, lsn: le.Uint64(area[0:]), clean: inUse == ntfsLogNoClient || flags&ntfsLogClean != 0,
	}, false, nil
}

// fixed applies the fixups of page, and reports whether they match.
func fixed(page []byte) bool {
	return applyFixups(page) == nil
}

// readExact reads n bytes at off of the size bytes of r, or returns an error when they run past size.
func readExact(r io.ReaderAt, size, off int64, n int) ([]byte, error) {
	if off < 0 || n <= 0 || off > size || int64(n) > size-off {
		return nil, fmt.Errorf("%w: %d bytes at %d run past the end of the volume of %d bytes", errCutShort, n, off, size)
	}

	b, err := read(r, size, off, n)
	if err != nil {
		return nil, err
	}

	if len(b) < n {
		return nil, fmt.Errorf("%w: read %d bytes at %d, not %d", errCutShort, len(b), off, n)
	}

	return b, nil
}
