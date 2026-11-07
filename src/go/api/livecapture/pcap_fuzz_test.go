package livecapture

import (
	"encoding/binary"
	"errors"
	"testing"
)

// FuzzPcap checks parseHeader against the four pcap magic numbers, and that
// complete, given what follows the header, returns a prefix of whole records:
// one complete takes whole, after which no whole valid record follows.
func FuzzPcap(f *testing.F) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		header := pcapFile(order, magicMicroseconds)
		one, two := record(order, 60), record(order, 1500)

		f.Add(header)
		f.Add(pcapFile(order, magicNanoseconds))
		f.Add(append(append(append([]byte{}, header...), one...), two[:100]...))
		f.Add(append(append([]byte{}, header...), pcapRecordAs(order, 10, 5)...))
	}

	f.Add(pcapFile(binary.LittleEndian, 0x0a0d0d0a))
	f.Add([]byte{0xd4, 0xc3, 0xb2, 0xa1})

	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := parseHeader(b)

		magic := func(order binary.ByteOrder) bool {
			m := order.Uint32(b)

			return m == magicMicroseconds || m == magicNanoseconds
		}

		switch want := len(b) >= fileHeaderLen && (magic(binary.LittleEndian) || magic(binary.BigEndian)); {
		case want != (err == nil):
			t.Fatalf("parseHeader(%x) error = %v, want accepted %t", b, err, want)
		case err != nil:
			if !errors.Is(err, errNotPcap) {
				t.Fatalf("parseHeader(%x) error = %v, want %v", b, err, errNotPcap)
			}

			return
		case !magic(h.order):
			t.Fatalf("parseHeader(%x).order = %v, which does not read the magic number", b, h.order)
		}

		records := b[fileHeaderLen:]

		n, err := h.complete(records)
		if n < 0 || n > len(records) {
			t.Fatalf("complete(%x) = %d, want 0 to %d", records, n, len(records))
		}

		if err != nil && !errors.Is(err, errCorrupt) {
			t.Fatalf("complete(%x) error = %v, want nil or %v", records, err, errCorrupt)
		}

		if again, err := h.complete(records[:n]); again != n || err != nil {
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
