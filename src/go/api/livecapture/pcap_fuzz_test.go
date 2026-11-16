package livecapture

import (
	"encoding/binary"
	"errors"
	"testing"
)

// FuzzPcap checks parseHeader against the four pcap magic numbers and the
// pcapng section header, and that complete, given what follows the pcap
// header, returns a prefix of whole records: one complete takes whole, after
// which no whole valid record follows.
func FuzzPcap(f *testing.F) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		header := pcapFile(order, magicMicroseconds)
		one, two := record(order, 60), record(order, 1500)

		f.Add(header)
		f.Add(pcapFile(order, magicNanoseconds))
		f.Add(append(append(append([]byte{}, header...), one...), two[:100]...))
		f.Add(append(append([]byte{}, header...), pcapRecordAs(order, 10, 5)...))
		f.Add(ngFile(ngSHB(order), ngIDB(order)))
	}

	f.Add(pcapFile(binary.LittleEndian, blockSHB))
	f.Add([]byte{0xd4, 0xc3, 0xb2, 0xa1})

	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := parseHeader(b)

		magic := func(order binary.ByteOrder) bool {
			m := order.Uint32(b)

			return m == magicMicroseconds || m == magicNanoseconds
		}

		ng := len(b) >= fileHeaderLen && binary.BigEndian.Uint32(b) == blockSHB &&
			(binary.BigEndian.Uint32(b[8:]) == byteOrderMagic || binary.LittleEndian.Uint32(b[8:]) == byteOrderMagic)

		switch want := ng || len(b) >= fileHeaderLen && (magic(binary.LittleEndian) || magic(binary.BigEndian)); {
		case want != (err == nil):
			t.Fatalf("parseHeader(%x) error = %v, want accepted %t", b, err, want)
		case err != nil:
			if !errors.Is(err, errNotPcap) {
				t.Fatalf("parseHeader(%x) error = %v, want %v", b, err, errNotPcap)
			}

			return
		case h.ng != ng:
			t.Fatalf("parseHeader(%x).ng = %t, want %t", b, h.ng, ng)
		case ng:
			if h.order.Uint32(b[8:]) != byteOrderMagic || h.headerLen() != 0 {
				t.Fatalf("parseHeader(%x) = %+v, which does not read the byte-order magic", b, h)
			}

			return
		case !magic(h.order):
			t.Fatalf("parseHeader(%x).order = %v, which does not read the magic number", b, h.order)
		}

		records := b[fileHeaderLen:]

		n, next, err := h.complete(records, nil)
		if n < 0 || n > len(records) || next != h {
			t.Fatalf("complete(%x) = %d, %+v; want 0 to %d, %+v", records, n, next, len(records), h)
		}

		if err != nil && !errors.Is(err, errCorrupt) {
			t.Fatalf("complete(%x) error = %v, want nil or %v", records, err, errCorrupt)
		}

		if again, _, err := h.complete(records[:n], nil); again != n || err != nil {
			t.Fatalf("complete(its own %d whole bytes) = %d, %v; want %d, nil", n, again, err, n)
		}

		// without an error, what is left is less than one whole record
		if rest := records[n:]; err == nil && len(rest) >= recordHeaderLen {
			if end := recordHeaderLen + int(h.order.Uint32(rest[8:])); end <= len(rest) {
				t.Fatalf("complete(%x) = %d, but a whole record of %d bytes follows", records, n, end)
			}
		}
	})
}

// ngModelHeader is the fuzz model of a pcapng block header at the start of b,
// in a section of the given order: the block's order and length, whether all
// of the header is in b, and whether the header is one a writer produces.
func ngModelHeader(b []byte, order binary.ByteOrder) (binary.ByteOrder, int, bool, bool) {
	shb := len(b) >= 4 && b[0] == 0x0a && b[1] == 0x0d && b[2] == 0x0d && b[3] == 0x0a

	switch {
	case len(b) < 8, shb && len(b) < 12:
		return order, 0, false, true
	case shb && binary.LittleEndian.Uint32(b[8:]) == byteOrderMagic:
		order = binary.LittleEndian
	case shb && binary.BigEndian.Uint32(b[8:]) == byteOrderMagic:
		order = binary.BigEndian
	case shb:
		return order, 0, true, false
	}

	n, least := int(order.Uint32(b[4:])), 12
	if shb {
		least = 28
	}

	return order, n, true, n >= least && n%4 == 0 && n <= 16<<20
}

// FuzzPcapng checks the pcapng block walker against ngModelHeader: it never
// fails other than with errCorrupt, the length it returns is the sum of the
// whole blocks it visits, each of which ends with its own length, and the
// block after them is corrupt, when it fails, or not whole yet.
func FuzzPcapng(f *testing.F) {
	for _, tt := range ngWalkCases() {
		f.Add(tt.b, tt.order == binary.BigEndian)
	}

	f.Fuzz(func(t *testing.T, b []byte, bigEndian bool) {
		var order binary.ByteOrder = binary.LittleEndian
		if bigEndian {
			order = binary.BigEndian
		}

		var (
			end   int
			model = order
		)

		n, next, err := format{ng: true, order: order}.complete(b, func(blockType uint32, start, stop int) {
			blockOrder, size, _, valid := ngModelHeader(b[start:], model)

			switch {
			case start != end:
				t.Fatalf("complete(%x) visited a block at byte %d after one that ends at %d", b, start, end)
			case !valid || stop-start != size || stop > len(b):
				t.Fatalf("complete(%x) visited bytes %d to %d, which are not a whole valid block", b, start, stop)
			case int(blockOrder.Uint32(b[stop-4:])) != size:
				t.Fatalf("complete(%x) visited a block at byte %d that ends with another length", b, start)
			case blockType != blockOrder.Uint32(b[start:]):
				t.Fatalf("complete(%x) visited a block of type %#x at byte %d, want %#x", b, blockType, start, blockOrder.Uint32(b[start:]))
			}

			end, model = stop, blockOrder
		})

		if n != end || next != (format{ng: true, order: model}) {
			t.Fatalf("complete(%x) = %d, %+v; want the end %d of the visited blocks, %v", b, n, next, end, model)
		}

		if err != nil && !errors.Is(err, errCorrupt) {
			t.Fatalf("complete(%x) error = %v, want nil or %v", b, err, errCorrupt)
		}

		if again, _, err := (format{ng: true, order: order}).complete(b[:n], nil); again != n || err != nil {
			t.Fatalf("complete(its own %d whole bytes) = %d, %v; want %d, nil", n, again, err, n)
		}

		blockOrder, size, known, valid := ngModelHeader(b[n:], model)
		whole := known && valid && size <= len(b)-n
		wantErr := known && (!valid || whole && int(blockOrder.Uint32(b[n+size-4:])) != size)

		switch {
		case (err != nil) != wantErr:
			t.Fatalf("complete(%x) = %d, %v; want an error %t for the block at byte %d", b, n, err, wantErr, n)
		case err == nil && whole:
			t.Fatalf("complete(%x) = %d, nil; but a whole block of %d bytes follows", b, n, size)
		}
	})
}
