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
	// errNotPcap is returned for a file that starts with neither a pcap
	// header nor a pcapng section header block.
	errNotPcap = errors.New("not a pcap or pcapng file")

	// errCorrupt is returned for a record or block header no writer would produce.
	errCorrupt = errors.New("corrupt pcap record or pcapng block")
)

// format is how a capture file keeps its packets: pcap records after a file
// header, or pcapng blocks. order is the byte order of the records, or of the
// pcapng section the next block is in. It is nil until the format is known.
type format struct {
	ng    bool
	order binary.ByteOrder
}

// parseHeader finds the format of a file from its first fileHeaderLen bytes or
// more: a pcap magic number, or a pcapng section header block.
func parseHeader(b []byte) (format, error) {
	if len(b) < fileHeaderLen {
		return format{}, fmt.Errorf("%w: header is %d bytes", errNotPcap, len(b))
	}

	// the magic number, written in the writer's byte order, says both that
	// this is pcap (micro- or nanosecond timestamps) and which order that is
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		switch order.Uint32(b) {
		case magicMicroseconds, magicNanoseconds:
			return format{ng: false, order: order}, nil
		}
	}

	if binary.LittleEndian.Uint32(b) == blockSHB {
		if order := sectionOrder(b[8:]); order != nil {
			return format{ng: true, order: order}, nil
		}
	}

	return format{}, errNotPcap
}

// headerLen is the length of the file header before the first record or block.
func (f format) headerLen() int {
	if f.ng {
		return 0
	}

	return fileHeaderLen
}

// complete returns how many leading bytes of b, which starts at a record or
// block boundary, are whole records or blocks, and the format of the bytes
// after them. The rest is a record or block still being written. visit, when
// not nil, gets the type and the range in b of each whole pcapng block.
func (f format) complete(b []byte, visit func(blockType uint32, start, end int)) (int, format, error) {
	if f.ng {
		return f.blocks(b, visit)
	}

	n, err := f.records(b)

	return n, f, err
}

// pending returns the length of the record or block that b starts with, or 0
// when b does not hold all of its header or the header is corrupt.
func (f format) pending(b []byte) int {
	if f.ng {
		n, _, err := f.blockHeader(b)
		if err != nil {
			return 0
		}

		return n
	}

	if len(b) < recordHeaderLen {
		return 0
	}

	if incl := f.order.Uint32(b[8:]); incl <= maxRecordLen {
		return recordHeaderLen + int(incl)
	}

	return 0
}

// records returns how many leading bytes of b, which starts at a record
// boundary, are whole pcap records.
func (f format) records(b []byte) (int, error) {
	var n int

	for len(b)-n >= recordHeaderLen {
		incl := f.order.Uint32(b[n+8:])
		orig := f.order.Uint32(b[n+12:])

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
