package livecapture

import (
	"encoding/binary"
	"fmt"
)

const (
	// the pcapng block types a late reader needs to read the blocks after
	// them: the section header, interface description and name resolution.
	blockSHB = 0x0a0d0d0a
	blockIDB = 0x00000001
	blockNRB = 0x00000004

	// byteOrderMagic is in each section header, in the section's byte order.
	byteOrderMagic = 0x1a2b3c4d

	// blockHeaderLen is the length of a block's type and leading length.
	blockHeaderLen = 8

	// minBlockLen is the length of a block with no body: its type and its
	// leading and trailing lengths.
	minBlockLen = 12

	// minSHBLen adds the byte-order magic, the version and the section length.
	minSHBLen = 28

	// maxBlockLen bounds one block. An EPB holds one packet, and an IDB or
	// NRB holds a few names; a larger length is not a block header at all.
	maxBlockLen = 16 << 20
)

// sectionOrder returns the byte order that writes byteOrderMagic as the
// first four bytes of b, or nil when neither does.
func sectionOrder(b []byte) binary.ByteOrder {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		if order.Uint32(b) == byteOrderMagic {
			return order
		}
	}

	return nil
}

// blockHeader returns the total length of the pcapng block that b starts
// with, and the byte order of its section, which a section header sets. The
// length is 0 when b does not hold all of the block's header yet.
func (f format) blockHeader(b []byte) (int, binary.ByteOrder, error) {
	order, least := f.order, uint32(minBlockLen)

	if len(b) < blockHeaderLen {
		return 0, order, nil
	}

	// the type of a section header reads the same in both byte orders
	if binary.LittleEndian.Uint32(b) == blockSHB {
		if len(b) < minBlockLen {
			return 0, order, nil
		}

		if order, least = sectionOrder(b[8:]), minSHBLen; order == nil {
			return 0, f.order, fmt.Errorf("%w: section header with no byte-order magic", errCorrupt)
		}
	}

	n := order.Uint32(b[4:])
	if n < least || n%4 != 0 || n > maxBlockLen {
		return 0, order, fmt.Errorf("%w: block of %d bytes", errCorrupt, n)
	}

	return int(n), order, nil
}

// blocks returns how many leading bytes of b, which starts at a block
// boundary, are whole pcapng blocks, and the format of the bytes after them.
func (f format) blocks(b []byte, visit func(blockType uint32, start, end int)) (int, format, error) {
	var n int

	for {
		size, order, err := f.blockHeader(b[n:])
		if err != nil {
			return n, f, err
		}

		end := n + size
		if size == 0 || end > len(b) {
			return n, f, nil
		}

		if trailing := order.Uint32(b[end-4:]); int(trailing) != size {
			return n, f, fmt.Errorf("%w: block of %d bytes ends with length %d", errCorrupt, size, trailing)
		}

		if visit != nil {
			visit(order.Uint32(b[n:]), n, end)
		}

		f.order, n = order, end
	}
}
