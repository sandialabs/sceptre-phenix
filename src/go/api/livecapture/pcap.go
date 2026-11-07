package livecapture

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// fileHeaderLen is the length of a pcap file's global header.
	fileHeaderLen = 24

	recordHeaderLen = 16

	// the magic numbers of pcap files with micro- and nanosecond timestamps.
	magicMicroseconds = 0xa1b2c3d4
	magicNanoseconds  = 0xa1b23c4d

	// maxRecordLen bounds one record's captured length. minimega's snaplen
	// defaults to 1600 bytes, and no link type captures more than 256 KiB;
	// a larger length means the bytes are not a record header at all.
	maxRecordLen = 256 * 1024
)

var (
	// errNotPcap is returned for a file that does not start with a pcap
	// header. minimega writes every capture as classic pcap, never pcapng.
	errNotPcap = errors.New("not a pcap file")

	// errCorrupt is returned for a record header no pcap writer would produce.
	errCorrupt = errors.New("corrupt pcap record")
)

// pcapHeader is what a pcap file's global header says about its records.
type pcapHeader struct {
	order binary.ByteOrder
}

// parseHeader reads a pcap global header, which must be fileHeaderLen bytes.
func parseHeader(b []byte) (pcapHeader, error) {
	if len(b) < fileHeaderLen {
		return pcapHeader{}, fmt.Errorf("%w: header is %d bytes", errNotPcap, len(b))
	}

	// the magic number, written in the writer's byte order, says both that
	// this is pcap (micro- or nanosecond timestamps) and which order that is
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		switch order.Uint32(b) {
		case magicMicroseconds, magicNanoseconds:
			return pcapHeader{order: order}, nil
		}
	}

	return pcapHeader{}, errNotPcap
}

// complete returns how many leading bytes of b, which starts at a record
// boundary, are whole records. The rest is a record still being written.
func (h pcapHeader) complete(b []byte) (int, error) {
	var n int

	for len(b)-n >= recordHeaderLen {
		incl := h.order.Uint32(b[n+8:])
		orig := h.order.Uint32(b[n+12:])

		if incl > maxRecordLen || (orig != 0 && orig < incl) {
			return n, fmt.Errorf("%w: %d captured of %d bytes", errCorrupt, incl, orig)
		}

		end := n + recordHeaderLen + int(incl)
		if end > len(b) {
			break
		}

		n = end
	}

	return n, nil
}
