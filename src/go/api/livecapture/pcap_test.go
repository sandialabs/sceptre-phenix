package livecapture

import (
	"encoding/binary"
	"errors"
	"slices"
	"strconv"
	"testing"

	"pgregory.net/rapid"
)

// pcapFile returns a pcap file header in the given byte order.
func pcapFile(order binary.ByteOrder, magic uint32) []byte {
	b := make([]byte, fileHeaderLen)
	order.PutUint32(b, magic)
	order.PutUint16(b[4:], 2)
	order.PutUint16(b[6:], 4)
	order.PutUint32(b[16:], 1600) // snaplen
	order.PutUint32(b[20:], 1)    // Ethernet

	return b
}

// record returns a pcap record with a payload of n bytes.
func record(order binary.ByteOrder, n int) []byte {
	b := make([]byte, recordHeaderLen+n)
	order.PutUint32(b, 1700000000)
	order.PutUint32(b[8:], uint32(n))  //nolint:gosec // test sizes are small
	order.PutUint32(b[12:], uint32(n)) //nolint:gosec // test sizes are small

	for i := range n {
		b[recordHeaderLen+i] = byte(i)
	}

	return b
}

func TestParseHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		b     []byte
		order binary.ByteOrder
		err   error
	}{
		{"little endian", pcapFile(binary.LittleEndian, 0xa1b2c3d4), binary.LittleEndian, nil},
		{"big endian", pcapFile(binary.BigEndian, 0xa1b2c3d4), binary.BigEndian, nil},
		{"nanosecond", pcapFile(binary.LittleEndian, 0xa1b23c4d), binary.LittleEndian, nil},
		{"pcapng", pcapFile(binary.LittleEndian, 0x0a0d0d0a), nil, errNotPcap},
		{"short", []byte{0xd4, 0xc3, 0xb2, 0xa1}, nil, errNotPcap},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := parseHeader(tt.b)
			if !errors.Is(err, tt.err) {
				t.Errorf("parseHeader(%x) error = %v, want %v", tt.b, err, tt.err)
			}

			if tt.err == nil && h.order != tt.order {
				t.Errorf("parseHeader(%x).order = %v, want %v", tt.b, h.order, tt.order)
			}
		})
	}
}

func TestComplete(t *testing.T) {
	t.Parallel()

	var (
		h     = pcapHeader{order: binary.BigEndian}
		one   = record(binary.BigEndian, 60)
		two   = record(binary.BigEndian, 1500)
		whole = append(append([]byte{}, one...), two...)
	)

	tests := []struct {
		name string
		b    []byte
		want int
		err  error
	}{
		{"empty", nil, 0, nil},
		{"whole records", whole, len(whole), nil},
		{"partial header", append(append([]byte{}, one...), two[:10]...), len(one), nil},
		{"partial payload", append(append([]byte{}, one...), two[:100]...), len(one), nil},
		{"absurd length", func() []byte {
			b := record(binary.BigEndian, 10)
			binary.BigEndian.PutUint32(b[8:], maxRecordLen+1)
			binary.BigEndian.PutUint32(b[12:], maxRecordLen+1)

			return append(append([]byte{}, one...), b...)
		}(), len(one), errCorrupt},
		{"captured more than sent", func() []byte {
			b := record(binary.BigEndian, 10)
			binary.BigEndian.PutUint32(b[12:], 5)

			return b
		}(), 0, errCorrupt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := h.complete(tt.b)
			if !errors.Is(err, tt.err) {
				t.Errorf("complete(%d bytes of %s) error = %v, want %v", len(tt.b), tt.name, err, tt.err)
			}

			if got != tt.want {
				t.Errorf("complete(%d bytes of %s) = %d, want %d", len(tt.b), tt.name, got, tt.want)
			}
		})
	}
}

// genPcapRecordLen draws a captured length: small, around minimega's snaplen, or the largest allowed.
func genPcapRecordLen() *rapid.Generator[int] {
	return rapid.OneOf(rapid.IntRange(0, 64), rapid.IntRange(1500, 1600), rapid.Just(maxRecordLen))
}

// pcapRecordAs returns a record of n bytes whose header gives orig as the length on the wire.
func pcapRecordAs(order binary.ByteOrder, n int, orig uint32) []byte {
	b := record(order, n)
	order.PutUint32(b[12:], orig)

	return b
}

// complete counts the whole records of any prefix of a capture, and stops with
// errCorrupt at a record header no writer produces once all 16 bytes are in.
func TestCompleteProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		order := rapid.SampledFrom([]binary.ByteOrder{binary.LittleEndian, binary.BigEndian}).Draw(t, "order")
		lens := rapid.SliceOfN(genPcapRecordLen(), 0, 6).Draw(t, "lens")

		var (
			b      []byte
			bounds = make([]int, 1, len(lens)+1)
		)

		for i, n := range lens {
			// orig 0 (unknown) or at least the captured length (cut by the snaplen)
			orig := rapid.SampledFrom([]int{0, n, n + 100}).Draw(t, "orig"+strconv.Itoa(i))
			b = append(b, pcapRecordAs(order, n, uint32(orig))...) //nolint:gosec // test sizes are small
			bounds = append(bounds, len(b))
		}

		whole := len(b)

		corrupt := rapid.SampledFrom([]string{"", "too long", "captured more than sent"}).Draw(t, "corrupt")
		switch corrupt {
		case "too long":
			incl := rapid.IntRange(maxRecordLen+1, maxRecordLen+1000).Draw(t, "incl")
			bad := pcapRecordAs(order, 0, uint32(incl)) //nolint:gosec // test sizes are small
			order.PutUint32(bad[8:], uint32(incl))      //nolint:gosec // test sizes are small
			b = append(b, bad...)
		case "captured more than sent":
			incl := rapid.IntRange(2, 64).Draw(t, "incl")
			orig := rapid.IntRange(1, incl-1).Draw(t, "short orig")
			b = append(b, pcapRecordAs(order, incl, uint32(orig))...) //nolint:gosec // test sizes are small
		}

		// record boundaries, and either side of where the corrupt header is whole
		edges := append(slices.Clone(bounds), len(b))
		if corrupt != "" {
			edges = append(edges, whole+recordHeaderLen-1, whole+recordHeaderLen)
		}

		cut := rapid.OneOf(rapid.IntRange(0, len(b)), rapid.SampledFrom(edges)).Draw(t, "cut")

		want := 0
		for _, bound := range bounds {
			if bound <= cut {
				want = bound
			}
		}

		var wantErr error
		if corrupt != "" && cut >= whole+recordHeaderLen {
			wantErr = errCorrupt
		}

		got, err := pcapHeader{order: order}.complete(b[:cut])
		if got != want || !errors.Is(err, wantErr) {
			t.Fatalf("complete(%d of %d bytes, records %v, %q) = %d, %v; want %d, %v",
				cut, len(b), lens, corrupt, got, err, want, wantErr)
		}
	})
}
