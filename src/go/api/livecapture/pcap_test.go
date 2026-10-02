package livecapture

import (
	"encoding/binary"
	"errors"
	"testing"
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
				t.Fatalf("got error %v, want %v", err, tt.err)
			}

			if tt.err == nil && h.order != tt.order {
				t.Fatalf("got byte order %v, want %v", h.order, tt.order)
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
				t.Fatalf("got error %v, want %v", err, tt.err)
			}

			if got != tt.want {
				t.Fatalf("got %d whole bytes, want %d", got, tt.want)
			}
		})
	}
}
