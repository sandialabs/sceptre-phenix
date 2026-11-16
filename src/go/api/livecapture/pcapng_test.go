package livecapture

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"
)

// the pcapng block types the tests write besides blockSHB, blockIDB and blockNRB.
const (
	blockEPB = 0x00000006
	blockISB = 0x00000005
)

// ngOption is a pcapng option of a block.
type ngOption struct {
	code  uint16
	value []byte
}

func ngOpt(code uint16, value string) ngOption {
	return ngOption{code: code, value: []byte(value)}
}

// ngAppend returns the order as an AppendByteOrder. The tests use only the little- and big-endian orders, which are both.
func ngAppend(order binary.ByteOrder) binary.AppendByteOrder {
	return order.(binary.AppendByteOrder)
}

// ngPad pads b with zeros to a multiple of 4 bytes.
func ngPad(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}

	return b
}

// ngBlock returns a pcapng block of the given type, body and options, with
// the body and each option padded, and opt_endofopt after any options.
func ngBlock(order binary.ByteOrder, typ uint32, body []byte, opts ...ngOption) []byte {
	b := ngAppend(order).AppendUint32(nil, typ)
	b = ngAppend(order).AppendUint32(b, 0)
	b = ngPad(append(b, body...))

	for _, opt := range opts {
		b = ngAppend(order).AppendUint16(b, opt.code)
		b = ngAppend(order).AppendUint16(b, uint16(len(opt.value)))
		b = ngPad(append(b, opt.value...))
	}

	if len(opts) > 0 {
		b = ngAppend(order).AppendUint32(b, 0)
	}

	b = ngAppend(order).AppendUint32(b, 0)
	order.PutUint32(b[4:], uint32(len(b)))
	order.PutUint32(b[len(b)-4:], uint32(len(b)))

	return b
}

// ngSHB returns a section header block of a section of unknown length.
func ngSHB(order binary.ByteOrder, opts ...ngOption) []byte {
	body := ngAppend(order).AppendUint32(nil, byteOrderMagic)
	body = ngAppend(order).AppendUint16(body, 1)
	body = ngAppend(order).AppendUint16(body, 0)
	body = ngAppend(order).AppendUint64(body, ^uint64(0))

	return ngBlock(order, blockSHB, body, opts...)
}

// ngIDB returns an interface description block of an Ethernet interface.
func ngIDB(order binary.ByteOrder, opts ...ngOption) []byte {
	body := ngAppend(order).AppendUint16(nil, 1)
	body = ngAppend(order).AppendUint16(body, 0)
	body = ngAppend(order).AppendUint32(body, 1600)

	return ngBlock(order, blockIDB, body, opts...)
}

// ngEPB returns an enhanced packet block of interface iface with n bytes of packet data.
func ngEPB(order binary.ByteOrder, iface uint32, n int, opts ...ngOption) []byte {
	body := ngAppend(order).AppendUint32(nil, iface)
	body = ngAppend(order).AppendUint32(body, 0x5f5e1)
	body = ngAppend(order).AppendUint32(body, 0x2a)
	body = ngAppend(order).AppendUint32(body, uint32(n))
	body = ngAppend(order).AppendUint32(body, uint32(n))

	for i := range n {
		body = append(body, byte(i))
	}

	return ngBlock(order, blockEPB, body, opts...)
}

// ngISB returns an interface statistics block of interface iface.
func ngISB(order binary.ByteOrder, iface uint32, opts ...ngOption) []byte {
	body := ngAppend(order).AppendUint32(nil, iface)
	body = ngAppend(order).AppendUint32(body, 0x5f5e1)
	body = ngAppend(order).AppendUint32(body, 0x2a)

	return ngBlock(order, blockISB, body, opts...)
}

// ngNRB returns a name resolution block that gives 10.1.1.20 each name.
func ngNRB(order binary.ByteOrder, names ...string) []byte {
	var body []byte

	for _, name := range names {
		value := append([]byte{10, 1, 1, 20}, name...)
		value = append(value, 0)

		body = ngAppend(order).AppendUint16(body, 1) // nrb_record_ipv4
		body = ngAppend(order).AppendUint16(body, uint16(len(value)))
		body = ngPad(append(body, value...))
	}

	body = ngAppend(order).AppendUint32(body, 0) // nrb_record_end

	return ngBlock(order, blockNRB, body)
}

// ngFile joins blocks into a file.
func ngFile(blocks ...[]byte) []byte {
	return bytes.Join(blocks, nil)
}

var errNgInvalid = errors.New("invalid pcapng")

// readNg reads a whole pcapng file without the package's walker, and returns
// the type of each block. It fails unless the file starts with a section
// header and each packet or statistics block comes after the description of
// its interface in the same section.
func readNg(b []byte) ([]uint32, error) {
	var (
		types  []uint32
		order  binary.ByteOrder
		ifaces uint32
	)

	for off := 0; off < len(b); {
		rest := b[off:]
		if len(rest) < minBlockLen {
			return types, fmt.Errorf("%w: %d bytes after the last block at byte %d", errNgInvalid, len(rest), off)
		}

		if binary.BigEndian.Uint32(rest) == blockSHB {
			switch {
			case binary.BigEndian.Uint32(rest[8:]) == byteOrderMagic:
				order = binary.BigEndian
			case binary.LittleEndian.Uint32(rest[8:]) == byteOrderMagic:
				order = binary.LittleEndian
			default:
				return types, fmt.Errorf("%w: section header at byte %d has no byte-order magic", errNgInvalid, off)
			}

			ifaces = 0
		} else if order == nil {
			return types, fmt.Errorf("%w: block at byte %d is not in a section", errNgInvalid, off)
		}

		typ, n := order.Uint32(rest), int(order.Uint32(rest[4:]))
		if n < minBlockLen || n%4 != 0 || n > len(rest) || int(order.Uint32(rest[n-4:])) != n {
			return types, fmt.Errorf("%w: block at byte %d has a bad length %d", errNgInvalid, off, n)
		}

		switch typ {
		case blockIDB:
			ifaces++
		case blockEPB, blockISB:
			if iface := order.Uint32(rest[8:]); iface >= ifaces {
				return types, fmt.Errorf("%w: block at byte %d uses interface %d of %d", errNgInvalid, off, iface, ifaces)
			}
		}

		types = append(types, typ)
		off += n
	}

	return types, nil
}

// ngWalkCase is a walk of the blocks of b, from a block boundary in a section of the given order.
type ngWalkCase struct {
	name      string
	order     binary.ByteOrder
	b         []byte
	want      int
	wantOrder binary.ByteOrder
	wantTypes []uint32
	err       error
}

// ngWalkCases are the walks TestCompleteNg checks and FuzzPcapng starts from.
func ngWalkCases() []ngWalkCase {
	var (
		le, be  = binary.LittleEndian, binary.BigEndian
		shb     = ngSHB(le, ngOpt(4, "minimega 3.4.0"), ngOpt(1, "exp/capture/1"))
		idb     = ngIDB(le, ngOpt(2, "compute1/mega_bridge_exp"), ngOpt(9, "\x09"))
		epb     = ngEPB(le, 0, 61, ngOpt(1, "late"))
		isb     = ngISB(le, 0, ngOpt(4, "\x00\x00\x00\x00\x00\x00\x00\x07"))
		nrb     = ngNRB(le, "web01", "db")
		section = ngFile(shb, idb, epb, isb, nrb)
		bigShb  = ngSHB(be, ngOpt(3, "Linux"))
		bigIdb  = ngIDB(be)
		bigEpb  = ngEPB(be, 0, 3)
		mixed   = ngFile(section, bigShb, bigIdb, bigEpb)
		allLE   = []uint32{blockSHB, blockIDB, blockEPB, blockISB, blockNRB}
	)

	// with sets the four bytes at off of a copy of b to v, in order
	with := func(b []byte, order binary.ByteOrder, off int, v uint32) []byte {
		b = bytes.Clone(b)
		order.PutUint32(b[off:], v)

		return b
	}

	n := len(section)

	return []ngWalkCase{
		{"empty", le, nil, 0, le, nil, nil},
		{"section header only", be, shb, len(shb), le, []uint32{blockSHB}, nil},
		{"whole blocks", le, section, n, le, allLE, nil},
		{"whole blocks big endian", be, ngFile(bigShb, bigIdb, bigEpb), len(bigShb) + len(bigIdb) + len(bigEpb), be,
			[]uint32{blockSHB, blockIDB, blockEPB}, nil},
		{"blocks in the middle of a section", le, ngFile(epb, isb), len(epb) + len(isb), le, []uint32{blockEPB, blockISB}, nil},
		{"partial block header", le, append(bytes.Clone(section), epb[:7]...), n, le, allLE, nil},
		{"partial section header", le, append(bytes.Clone(section), bigShb[:11]...), n, le, allLE, nil},
		{"partial body", le, append(bytes.Clone(section), epb[:len(epb)-1]...), n, le, allLE, nil},
		{"second section in the other byte order", le, mixed, len(mixed), be,
			append(slices.Clone(allLE), blockSHB, blockIDB, blockEPB), nil},
		{"partial second section", le, mixed[:len(mixed)-1], len(mixed) - len(bigEpb), be,
			append(slices.Clone(allLE), blockSHB, blockIDB), nil},
		{"length below 12", le, append(bytes.Clone(section), with(epb, le, 4, 8)...), n, le, allLE, errCorrupt},
		{"section header below 28", le, with(bigShb, be, 4, 24), 0, le, nil, errCorrupt},
		{"length not a multiple of 4", le, append(bytes.Clone(section), with(epb, le, 4, 82)...), n, le, allLE, errCorrupt},
		{"length too large", le, append(bytes.Clone(section), with(epb, le, 4, maxBlockLen+4)[:16]...), n, le, allLE, errCorrupt},
		{"trailing length mismatch", le, append(bytes.Clone(section), with(epb, le, len(epb)-4, 96)...), n, le, allLE, errCorrupt},
		{"length in the wrong byte order", le, append(bytes.Clone(section), bigEpb...), n, le, allLE, errCorrupt},
		{"section header without a byte-order magic", be, with(shb, le, 8, 0x4d3c2b1b), 0, be, nil, errCorrupt},
	}
}

func TestCompleteNg(t *testing.T) {
	t.Parallel()

	for _, tt := range ngWalkCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var types []uint32

			got, next, err := format{ng: true, order: tt.order}.complete(tt.b, func(blockType uint32, _, _ int) {
				types = append(types, blockType)
			})
			if !errors.Is(err, tt.err) {
				t.Errorf("complete(%s) error = %v, want %v", tt.name, err, tt.err)
			}

			if got != tt.want {
				t.Errorf("complete(%s) = %d, want %d", tt.name, got, tt.want)
			}

			if want := (format{ng: true, order: tt.wantOrder}); next != want {
				t.Errorf("complete(%s) format = %+v, want %+v", tt.name, next, want)
			}

			if diff := cmp.Diff(tt.wantTypes, types); diff != "" {
				t.Errorf("complete(%s) visited block types mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}

func TestCompleteNgLargestBlock(t *testing.T) {
	t.Parallel()

	le := binary.LittleEndian
	largest := ngEPB(le, 0, maxBlockLen-32)

	if len(largest) != maxBlockLen {
		t.Fatalf("len(largest test block) = %d, want %d", len(largest), maxBlockLen)
	}

	f := format{ng: true, order: le}

	if got, _, err := f.complete(largest, nil); got != maxBlockLen || err != nil {
		t.Errorf("complete(a block of %d bytes) = %d, %v; want %d, nil", maxBlockLen, got, err, maxBlockLen)
	}

	if got := f.pending(largest[:8]); got != maxBlockLen {
		t.Errorf("pending(the header of a block of %d bytes) = %d, want %d", maxBlockLen, got, maxBlockLen)
	}
}

func TestPending(t *testing.T) {
	t.Parallel()

	var (
		le, be = binary.LittleEndian, binary.BigEndian
		epb    = ngEPB(be, 0, 100)
		shb    = ngSHB(le, ngOpt(4, "minimega"))
		rec    = record(le, 60)
	)

	tests := []struct {
		name string
		f    format
		b    []byte
		want int
	}{
		{"pcap record", format{ng: false, order: le}, rec, len(rec)},
		{"pcap record header only", format{ng: false, order: le}, rec[:recordHeaderLen], len(rec)},
		{"partial pcap record header", format{ng: false, order: le}, rec[:recordHeaderLen-1], 0},
		{"corrupt pcap record", format{ng: false, order: be}, rec, 0},
		{"pcapng block header only", format{ng: true, order: be}, epb[:8], len(epb)},
		{"partial pcapng block header", format{ng: true, order: be}, epb[:7], 0},
		{"pcapng section header", format{ng: true, order: be}, shb[:12], len(shb)},
		{"partial pcapng section header", format{ng: true, order: be}, shb[:11], 0},
		{"corrupt pcapng block", format{ng: true, order: le}, epb, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.f.pending(tt.b); got != tt.want {
				t.Errorf("pending(%x) = %d, want %d", tt.b, got, tt.want)
			}
		})
	}
}

func TestReadNg(t *testing.T) {
	t.Parallel()

	le := binary.LittleEndian

	tests := []struct {
		name string
		b    []byte
		want []uint32
		err  error
	}{
		{"interfaces before packets", ngFile(ngSHB(le), ngIDB(le), ngEPB(le, 0, 4)), []uint32{blockSHB, blockIDB, blockEPB}, nil},
		{"packet before its interface", ngFile(ngSHB(le), ngEPB(le, 0, 4)), []uint32{blockSHB}, errNgInvalid},
		{"no section header", ngIDB(le), nil, errNgInvalid},
		{"partial block", ngSHB(le)[:20], nil, errNgInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := readNg(tt.b)
			if !errors.Is(err, tt.err) {
				t.Errorf("readNg(%s) error = %v, want %v", tt.name, err, tt.err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("readNg(%s) block types mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}

// genPcapngBlock draws a block of a section in the given order that has ifaces interfaces.
func genPcapngBlock(t *rapid.T, order binary.ByteOrder, ifaces uint32, label string) []byte {
	opts := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) ngOption {
		return ngOpt(rapid.Uint16Range(1, 12).Draw(t, "code"), rapid.StringN(0, 9, -1).Draw(t, "value"))
	}), 0, 2).Draw(t, label+" options")

	kinds := []uint32{blockIDB, blockNRB}
	if ifaces > 0 {
		kinds = append(kinds, blockEPB, blockEPB, blockISB)
	}

	switch rapid.SampledFrom(kinds).Draw(t, label) {
	case blockIDB:
		return ngIDB(order, opts...)
	case blockNRB:
		return ngNRB(order, rapid.SliceOfN(rapid.StringN(1, 12, -1), 0, 3).Draw(t, label+" names")...)
	case blockEPB:
		return ngEPB(order, rapid.Uint32Range(0, ifaces-1).Draw(t, label+" iface"), rapid.IntRange(0, 100).Draw(t, label+" len"), opts...)
	default:
		return ngISB(order, rapid.Uint32Range(0, ifaces-1).Draw(t, label+" iface"), opts...)
	}
}

// complete stops at a block boundary of any prefix of a valid pcapng file:
// the last one in the prefix, in the byte order of that block's section.
// What follows it is a block that pending measures once its header is in.
func TestCompleteNgProperties(t *testing.T) {
	t.Parallel()

	orders := []binary.ByteOrder{binary.LittleEndian, binary.BigEndian}

	rapid.Check(t, func(t *rapid.T) {
		var (
			b      []byte
			bounds = []int{0}
			start  = rapid.SampledFrom(orders).Draw(t, "start order")
			after  = []binary.ByteOrder{start} // the order after each bound
		)

		for s := range rapid.IntRange(1, 3).Draw(t, "sections") {
			order := rapid.SampledFrom(orders).Draw(t, "order"+strconv.Itoa(s))
			blocks := [][]byte{ngSHB(order, ngOpt(4, rapid.StringN(0, 20, -1).Draw(t, "app")))}

			var ifaces uint32

			for i := range rapid.IntRange(0, 6).Draw(t, "blocks"+strconv.Itoa(s)) {
				block := genPcapngBlock(t, order, ifaces, fmt.Sprintf("block %d.%d", s, i))
				if order.Uint32(block) == blockIDB {
					ifaces++
				}

				blocks = append(blocks, block)
			}

			for _, block := range blocks {
				b = append(b, block...)
				bounds, after = append(bounds, len(b)), append(after, order)
			}
		}

		if _, err := readNg(b); err != nil {
			t.Fatalf("readNg(the drawn file) error = %v", err)
		}

		cut := rapid.OneOf(rapid.IntRange(0, len(b)), rapid.SampledFrom(bounds)).Draw(t, "cut")

		last := 0
		for i, bound := range bounds {
			if bound <= cut {
				last = i
			}
		}

		var ends []int

		f := format{ng: true, order: start}

		got, next, err := f.complete(b[:cut], func(_ uint32, _, end int) { ends = append(ends, end) })
		if got != bounds[last] || err != nil || next.order != after[last] {
			t.Fatalf("complete(%d of %d bytes) = %d, %v, %v; want %d, nil, %v",
				cut, len(b), got, err, next.order, bounds[last], after[last])
		}

		if diff := cmp.Diff(bounds[1:last+1], ends, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("complete(%d of %d bytes) visited block ends mismatch (-want +got):\n%s", cut, len(b), diff)
		}

		if rest := b[got:cut]; len(rest) >= minBlockLen && last+1 < len(bounds) {
			if n, want := next.pending(rest), bounds[last+1]-got; n != want {
				t.Fatalf("pending(%d bytes after the last whole block) = %d, want %d", len(rest), n, want)
			}
		}
	})
}
