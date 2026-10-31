package diskchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Format is the kind of image a header shows.
type Format string

// The formats ParseHeader tells apart.
const (
	// FormatOther is raw, an ISO, or any image phenix does not recognize: the base of its chain.
	FormatOther Format = ""
	FormatQcow2 Format = "qcow2"
	FormatQED   Format = "qed"
	FormatVMDK  Format = "vmdk"
)

const (
	// headerRead is how many bytes of an image ParseHeader reads at most, besides a backing file name past them.
	headerRead = 64 << 10

	// maxBackingName is the longest backing file name qemu accepts.
	maxBackingName = 1023

	qcow2V2HeaderLength  = 72
	qcow2V3HeaderLength  = 104
	minClusterBits       = 9
	maxClusterBits       = 21
	extensionHeader      = 8
	extensionEnd         = 0x00000000
	extensionBackingFmt  = 0xE2792ACA
	incompatibleDataFile = 1 << 2

	qcow2Magic = "QFI\xfb"
	qedMagic   = "QED\x00"
	vmdkMagic  = "KDMV"
	vmdkText   = "# Disk DescriptorFile"
)

var (
	// ErrBadHeader is wrapped by the error ParseHeader returns for a qcow2 header qemu would refuse.
	ErrBadHeader = errors.New("invalid qcow2 header")

	// ErrUnsupportedVersion is wrapped by the error ParseHeader returns for a qcow2 version other than 2 or 3.
	ErrUnsupportedVersion = errors.New("unsupported qcow2 version")
)

// Header is what an image's header says about its chain.
type Header struct {
	Format Format
	// Version is the qcow2 version; 0 for other formats.
	Version int
	// Backing is the backing file name as the header stores it, or "" for none.
	Backing string
	// BackingFormat is the backing file's format from the header extension, or "" when qemu probes it.
	BackingFormat string
	// DataFile is set for a qcow2 image whose data is in an external data file.
	DataFile bool
}

// ParseHeader reads the header of the image r holds. It returns an error wrapping ErrBadHeader or
// ErrUnsupportedVersion for a qcow2 header qemu would refuse, and the read error when r cannot be read.
func ParseHeader(r io.ReaderAt) (Header, error) {
	buf := make([]byte, headerRead)

	n, err := r.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return formatOnly(FormatOther), fmt.Errorf("reading image header: %w", err)
	}

	buf = buf[:n]

	switch {
	case bytes.HasPrefix(buf, []byte(qcow2Magic)):
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
	return Header{Format: f, Version: 0, Backing: "", BackingFormat: "", DataFile: false}
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

	if version != 2 && version != 3 {
		return h, fmt.Errorf("%w %d", ErrUnsupportedVersion, version)
	}

	var (
		backingOffset = be.Uint64(buf[8:])
		backingSize   = uint64(be.Uint32(buf[16:]))
		clusterBits   = be.Uint32(buf[20:])
		headerLength  = uint64(qcow2V2HeaderLength)
	)

	if clusterBits < minClusterBits || clusterBits > maxClusterBits {
		return h, fmt.Errorf("%w: cluster bits %d", ErrBadHeader, clusterBits)
	}

	clusterSize := uint64(1) << clusterBits

	if version == 3 { //nolint:mnd // qcow2 version 3
		if len(buf) < qcow2V3HeaderLength {
			return h, fmt.Errorf("%w: %d bytes long", ErrBadHeader, len(buf))
		}

		h.DataFile = be.Uint64(buf[72:])&incompatibleDataFile != 0
		headerLength = uint64(be.Uint32(buf[100:]))

		if headerLength < qcow2V3HeaderLength || headerLength > clusterSize {
			return h, fmt.Errorf("%w: header length %d", ErrBadHeader, headerLength)
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

	name := make([]byte, backingSize)
	if backingOffset+backingSize <= uint64(len(buf)) {
		copy(name, buf[backingOffset:])
	} else if _, err := r.ReadAt(name, int64(backingOffset)); err != nil { //nolint:gosec // G115 at most a cluster
		return h, fmt.Errorf("%w: reading the backing file name: %w", ErrBadHeader, err)
	}

	h.Backing = string(name)

	return h, nil
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
