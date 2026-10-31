package diskchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// Format is the kind of image a header shows.
type Format string

// The formats ParseHeader tells apart.
const (
	// FormatOther is raw, an ISO, or any image phenix does not recognize: the base of its chain.
	FormatOther Format = ""
	FormatQcow2 Format = "qcow2"
	// FormatQcow is a qcow version 1 image, which shares its magic with qcow2.
	FormatQcow Format = "qcow"
	FormatQED  Format = "qed"
	FormatVMDK Format = "vmdk"
)

const (
	// headerRead is how many bytes of an image ParseHeader reads at most, besides a backing file name past them.
	headerRead = 64 << 10

	// maxBackingName is the longest backing file name qemu accepts.
	maxBackingName = 1023

	qcow2V2HeaderLength = 72
	qcow2V3HeaderLength = 104
	minClusterBits      = 9
	maxClusterBits      = 21
	extensionHeader     = 8
	extensionEnd        = 0x00000000
	extensionBackingFmt = 0xE2792ACA
	versionEnd          = 8
	qcowHeaderLength    = 48
	maxQcowClusterBits  = 16
	l2BitsBelowCluster  = 3
	qcow2CompressionAt  = 104

	qcow2Magic = "QFI\xfb"
	qedMagic   = "QED\x00"
	vmdkMagic  = "KDMV"
	vmdkText   = "# Disk DescriptorFile"
)

var (
	// ErrBadHeader is wrapped by the error ParseHeader returns for a qcow2 header qemu would refuse.
	ErrBadHeader = errors.New("invalid qcow2 header")

	// ErrUnsupportedVersion is wrapped by the error ParseHeader returns for a qcow2 version later than 3.
	ErrUnsupportedVersion = errors.New("unsupported qcow2 version")
)

// Encryption is the encryption method a qcow or qcow2 header names.
type Encryption uint32

// The encryption methods qemu knows.
const (
	EncryptionNone Encryption = 0
	// EncryptionAES is the old qcow AES-CBC encryption, which qemu system emulators refuse.
	EncryptionAES Encryption = 1
	// EncryptionLUKS is qcow2 LUKS encryption, which needs a key secret.
	EncryptionLUKS Encryption = 2
)

// The incompatible feature bits of a qcow2 version 3 header.
const (
	// FeatureDirty marks an image that qemu did not close cleanly.
	FeatureDirty uint64 = 1 << iota
	// FeatureCorrupt marks an image that qemu found corrupt.
	FeatureCorrupt
	// FeatureDataFile marks an image whose data is in an external data file.
	FeatureDataFile
	// FeatureCompression marks an image whose compression type is not zlib.
	FeatureCompression
	// FeatureExtendedL2 marks an image with extended L2 entries (subclusters).
	FeatureExtendedL2

	// KnownFeatures are the incompatible feature bits qemu knows. qemu refuses an image with any other bit.
	KnownFeatures = FeatureDirty | FeatureCorrupt | FeatureDataFile | FeatureCompression | FeatureExtendedL2
)

// Header is what an image's header says about its chain and format.
type Header struct {
	Format Format
	// Version is the qcow or qcow2 version; 0 for other formats.
	Version int
	// Backing is the backing file name as the header stores it, or "" for none.
	Backing string
	// BackingFormat is the backing file's format from the header extension, or "" when qemu probes it.
	BackingFormat string
	// DataFile is set for a qcow2 image whose data is in an external data file.
	DataFile bool
	// Encryption is the encryption method of a qcow or qcow2 image.
	Encryption Encryption
	// Incompatible are the incompatible feature bits of a qcow2 version 3 image (see KnownFeatures).
	Incompatible uint64
	// Zstd is set for a qcow2 image whose compression type is zstd.
	Zstd bool
}

// ParseHeader reads the header of the image r holds. It returns an error wrapping ErrBadHeader or
// ErrUnsupportedVersion for a qcow or qcow2 header qemu would refuse as invalid, and the read error when r cannot be
// read. It does not refuse what qemu refuses for other reasons, such as encryption or unknown feature bits: the
// header shows them.
func ParseHeader(r io.ReaderAt) (Header, error) {
	buf := make([]byte, headerRead)

	n, err := r.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return formatOnly(FormatOther), fmt.Errorf("reading image header: %w", err)
	}

	buf = buf[:n]

	switch {
	case bytes.HasPrefix(buf, []byte(qcow2Magic)) && len(buf) >= versionEnd && binary.BigEndian.Uint32(buf[4:]) == 1:
		return parseQcow(r, buf)
	// qemu probes a qcow2 magic with version 0 as raw
	case bytes.HasPrefix(buf, []byte(qcow2Magic)) && len(buf) >= versionEnd && binary.BigEndian.Uint32(buf[4:]) >= 2:
		return parseQcow2(r, buf)
	case bytes.HasPrefix(buf, []byte(qedMagic)):
		return formatOnly(FormatQED), nil
	case bytes.HasPrefix(buf, []byte(vmdkMagic)), bytes.HasPrefix(buf, []byte(vmdkText)):
		return formatOnly(FormatVMDK), nil
	default:
		return formatOnly(FormatOther), nil
	}
}

// formatOnly is the header of an image phenix does not follow.
func formatOnly(f Format) Header {
	return Header{
		Format: f, Version: 0, Backing: "", BackingFormat: "", DataFile: false,
		Encryption: EncryptionNone, Incompatible: 0, Zstd: false,
	}
}

// parseQcow parses the qcow version 1 header at the start of buf as qemu's qcow driver checks it, reading from r a
// backing file name past buf's end. A qcow version 1 image has no backing format: qemu probes its backing file.
func parseQcow(r io.ReaderAt, buf []byte) (Header, error) {
	h := formatOnly(FormatQcow)
	h.Version = 1

	if len(buf) < qcowHeaderLength {
		return h, fmt.Errorf("%w: %d bytes long", ErrBadHeader, len(buf))
	}

	be := binary.BigEndian

	var (
		backingOffset = be.Uint64(buf[8:])
		backingSize   = uint64(be.Uint32(buf[16:]))
		size          = be.Uint64(buf[24:])
		clusterBits   = uint32(buf[32])
		l2Bits        = uint32(buf[33])
		crypt         = Encryption(be.Uint32(buf[36:]))
	)

	switch {
	case size <= 1:
		return h, fmt.Errorf("%w: virtual size %d", ErrBadHeader, size)
	case clusterBits < minClusterBits || clusterBits > maxQcowClusterBits:
		return h, fmt.Errorf("%w: cluster bits %d", ErrBadHeader, clusterBits)
	case l2Bits < minClusterBits-l2BitsBelowCluster || l2Bits > maxQcowClusterBits-l2BitsBelowCluster:
		return h, fmt.Errorf("%w: L2 bits %d", ErrBadHeader, l2Bits)
	case crypt > EncryptionAES:
		return h, fmt.Errorf("%w: encryption method %d", ErrBadHeader, crypt)
	case backingOffset != 0 && (backingSize > maxBackingName || backingOffset > math.MaxInt64-maxBackingName):
		return h, fmt.Errorf("%w: backing file name of %d bytes at %d", ErrBadHeader, backingSize, backingOffset)
	}

	h.Encryption = crypt

	if backingOffset == 0 {
		return h, nil
	}

	name, err := readBacking(r, buf, backingOffset, backingSize)
	h.Backing = name

	return h, err
}

// readBacking reads the backing file name of size bytes at offset, from buf when it holds them, else from r. The
// caller checks that offset and size fit in an int64.
func readBacking(r io.ReaderAt, buf []byte, offset, size uint64) (string, error) {
	name := make([]byte, size)
	if offset+size <= uint64(len(buf)) {
		copy(name, buf[offset:])
	} else if _, err := r.ReadAt(name, int64(offset)); err != nil { //nolint:gosec // G115 checked by the caller
		return "", fmt.Errorf("%w: reading the backing file name: %w", ErrBadHeader, err)
	}

	return string(name), nil
}

// parseQcow2 parses the qcow2 header at the start of buf, reading from r a backing file name past buf's end.
func parseQcow2(r io.ReaderAt, buf []byte) (Header, error) {
	h := formatOnly(FormatQcow2)

	if len(buf) < qcow2V2HeaderLength {
		return h, fmt.Errorf("%w: %d bytes long", ErrBadHeader, len(buf))
	}

	be := binary.BigEndian

	version := be.Uint32(buf[4:])
	h.Version = int(version)

	if version > 3 { //nolint:mnd // qcow2 version 3
		return h, fmt.Errorf("%w %d", ErrUnsupportedVersion, version)
	}

	var (
		backingOffset = be.Uint64(buf[8:])
		backingSize   = uint64(be.Uint32(buf[16:]))
		clusterBits   = be.Uint32(buf[20:])
		crypt         = Encryption(be.Uint32(buf[32:]))
		headerLength  = uint64(qcow2V2HeaderLength)
	)

	if crypt > EncryptionLUKS {
		return h, fmt.Errorf("%w: encryption method %d", ErrBadHeader, crypt)
	}

	h.Encryption = crypt

	if clusterBits < minClusterBits || clusterBits > maxClusterBits {
		return h, fmt.Errorf("%w: cluster bits %d", ErrBadHeader, clusterBits)
	}

	clusterSize := uint64(1) << clusterBits

	if version == 3 { //nolint:mnd // qcow2 version 3
		if len(buf) < qcow2V3HeaderLength {
			return h, fmt.Errorf("%w: %d bytes long", ErrBadHeader, len(buf))
		}

		h.Incompatible = be.Uint64(buf[72:])
		h.DataFile = h.Incompatible&FeatureDataFile != 0
		headerLength = uint64(be.Uint32(buf[100:]))

		if headerLength < qcow2V3HeaderLength || headerLength > clusterSize {
			return h, fmt.Errorf("%w: header length %d", ErrBadHeader, headerLength)
		}

		if err := readCompression(&h, buf, headerLength); err != nil {
			return h, err
		}
	}

	if backingOffset != 0 &&
		(backingOffset > clusterSize || backingSize > min(maxBackingName, clusterSize-backingOffset)) {
		return h, fmt.Errorf("%w: backing file name of %d bytes at %d", ErrBadHeader, backingSize, backingOffset)
	}

	extEnd := clusterSize
	if backingOffset != 0 {
		extEnd = backingOffset
	}

	if err := readExtensions(&h, buf, headerLength, extEnd); err != nil {
		return h, err
	}

	if backingOffset == 0 || backingSize == 0 {
		return h, nil
	}

	name, err := readBacking(r, buf, backingOffset, backingSize)
	h.Backing = name

	return h, err
}

// readCompression sets h's compression type from a version 3 header of headerLength bytes in buf, as qemu checks it:
// zlib or zstd, with the compression type bit set only for zstd.
func readCompression(h *Header, buf []byte, headerLength uint64) error {
	var compression byte

	// a file that ends first reads as zeros, as qemu reads past the end of a file
	if headerLength > qcow2CompressionAt && len(buf) > qcow2CompressionAt {
		compression = buf[qcow2CompressionAt]
	}

	switch {
	case compression > 1:
		return fmt.Errorf("%w: compression type %d", ErrBadHeader, compression)
	case (compression == 1) != (h.Incompatible&FeatureCompression != 0):
		return fmt.Errorf("%w: compression type %d with compression bit %t",
			ErrBadHeader, compression, h.Incompatible&FeatureCompression != 0)
	}

	h.Zstd = compression == 1

	return nil
}

// readExtensions reads the header extensions from start up to end, both offsets into the image, setting h's backing
// format. Extensions past buf are not read.
func readExtensions(h *Header, buf []byte, start, end uint64) error {
	end = min(end, uint64(len(buf)))

	for off := start; off+extensionHeader <= end; {
		typ := binary.BigEndian.Uint32(buf[off:])
		length := uint64(binary.BigEndian.Uint32(buf[off+4:]))
		off += extensionHeader

		if typ == extensionEnd {
			return nil
		}

		if length > end-off {
			return fmt.Errorf("%w: header extension 0x%08x runs past the header", ErrBadHeader, typ)
		}

		if typ == extensionBackingFmt {
			h.BackingFormat = string(bytes.TrimRight(buf[off:off+length], "\x00"))
		}

		off += (length + 7) &^ 7 //nolint:mnd // padded to 8 bytes
	}

	return nil
}
