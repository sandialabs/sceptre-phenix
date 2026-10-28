package experiment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

func TestParseNetflowRecord(t *testing.T) {
	var (
		v4    = netip.MustParseAddr
		start = time.Date(2026, 10, 28, 15, 30, 12, 0, time.FixedZone("", -6*60*60))
		same  = []cmp.Option{cmp.Comparer(func(a, b netip.Addr) bool { return a == b }), cmp.Comparer(time.Time.Equal)}
	)

	tests := []struct {
		name string
		line string
		want *netflowRecord // nil when the line is refused
	}{
		{
			name: "IPv4 flow",
			line: "2026-10-28T15:30:12-06:00\t2s\t6\t10.0.0.1:1234\t<->\t10.0.0.2:443\t2\t2048\n",
			want: &netflowRecord{
				Start: start, Duration: 2 * time.Second, Proto: 6, Src: v4("10.0.0.1"), Dst: v4("10.0.0.2"),
				SPort: 1234, DPort: 443, Packets: 2, Bytes: 2048,
			},
		},
		{
			name: "unbracketed IPv6 and a fractional duration",
			line: "2026-10-28T15:30:12-06:00 1.5s 17 fe80::1:53 <-> 2001:db8::2:5353 3 4096",
			want: &netflowRecord{
				Start: start, Duration: 1500 * time.Millisecond, Proto: 17, Src: v4("fe80::1"), Dst: v4("2001:db8::2"),
				SPort: 53, DPort: 5353, Packets: 3, Bytes: 4096,
			},
		},
		{
			name: "bracketed IPv6, 4-in-6 and an unknown protocol",
			line: "2026-10-28T15:30:12-06:00 0s 253 [2001:db8::1]:1 <-> [::ffff:10.0.0.9]:2 1 1",
			want: &netflowRecord{
				Start: start, Duration: 0, Proto: 253, Src: v4("2001:db8::1"), Dst: v4("10.0.0.9"),
				SPort: 1, DPort: 2, Packets: 1, Bytes: 1,
			},
		},
		{name: "7 fields", line: "2026-10-28T15:30:12Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1"},
		{name: "9 fields", line: "2026-10-28T15:30:12Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1 1"},
		{name: "bad time", line: "0 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1"},
		{name: "negative duration", line: "2026-10-28T15:30:12Z -1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1"},
		{name: "port 65536", line: "2026-10-28T15:30:12Z 1s 6 10.0.0.1:65536 <-> 10.0.0.2:2 1 1"},
		{name: "proto 256", line: "2026-10-28T15:30:12Z 1s 256 10.0.0.1:1 <-> 10.0.0.2:2 1 1"},
		{name: "zone", line: "2026-10-28T15:30:12Z 1s 6 [fe80::1%eth0]:1 <-> 10.0.0.2:2 1 1"},
		{name: "missing <->", line: "2026-10-28T15:30:12Z 1s 6 10.0.0.1:1 -> 10.0.0.2:2 1 1"},
		{name: "live view placeholder times", line: "0 0 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseNetflowRecord(test.line)
			if ok != (test.want != nil) {
				t.Fatalf("parseNetflowRecord(%q) ok = %t, want %t", test.line, ok, test.want != nil)
			}

			if !ok {
				return
			}

			if diff := cmp.Diff(*test.want, got, same...); diff != "" {
				t.Errorf("parseNetflowRecord(%q) mismatch (-want +got):\n%s", test.line, diff)
			}
		})
	}
}

// exportNetflow converts in to format, returning the output, its stats,
// whether the output was started, and its error.
func exportNetflow(ctx context.Context, in, format string) (string, NetflowExport, bool, error) {
	var (
		out     bytes.Buffer
		started bool
	)

	stats, err := ExportNetflow(ctx, func() io.Writer {
		started = true

		return &out
	}, strings.NewReader(in), format)

	return out.String(), stats, started, err
}

const netflowExportInput = "2026-10-28T15:30:12-06:00\t1.5s\t6\t10.0.0.1:1234\t<->\t10.0.0.2:443\t2\t2048\n" +
	"garbage\n" +
	"   \n" +
	"2026-10-28T21:30:12.25Z 0s 253 [2001:db8::1]:1 <-> ::ffff:10.0.0.9:2 1 1\n" +
	"2026-10-28T21:30:12Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1"

func TestExportNetflow(t *testing.T) {
	tests := map[string]struct {
		in, format string
		want       string
		stats      NetflowExport
	}{
		"csv": {
			in: netflowExportInput, format: NetflowFormatCSV,
			want: "event.start,event.end,event.duration,source.ip,source.port,destination.ip,destination.port," +
				"network.transport,network.iana_number,network.packets,network.bytes\n" +
				"2026-10-28T21:30:12Z,2026-10-28T21:30:13.5Z,1500000000,10.0.0.1,1234,10.0.0.2,443,tcp,6,2,2048\n" +
				"2026-10-28T21:30:12.25Z,2026-10-28T21:30:12.25Z,0,2001:db8::1,1,10.0.0.9,2,,253,1,1\n",
			stats: NetflowExport{Flows: 2, Skipped: 2},
		},
		"jsonl": {
			in: netflowExportInput, format: NetflowFormatJSONL,
			want: `{"event.start":"2026-10-28T21:30:12Z","event.end":"2026-10-28T21:30:13.5Z",` +
				`"event.duration":1500000000,"source.ip":"10.0.0.1","source.port":1234,"destination.ip":"10.0.0.2",` +
				`"destination.port":443,"network.transport":"tcp","network.iana_number":"6","network.packets":2,` +
				`"network.bytes":2048}` + "\n" +
				`{"event.start":"2026-10-28T21:30:12.25Z","event.end":"2026-10-28T21:30:12.25Z","event.duration":0,` +
				`"source.ip":"2001:db8::1","source.port":1,"destination.ip":"10.0.0.9","destination.port":2,` +
				`"network.iana_number":"253","network.packets":1,"network.bytes":1}` + "\n",
			stats: NetflowExport{Flows: 2, Skipped: 2},
		},
		"empty csv has its header": {
			in: "", format: NetflowFormatCSV,
			want: strings.Join(netflowColumns, ",") + "\n",
		},
		"empty jsonl": {in: "\n \n", format: NetflowFormatJSONL, want: ""},
		"a line over the buffer is skipped": {
			in:     strings.Repeat("x", netflowLineBuffer+10) + "\n2026-10-28T21:30:12Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1\n",
			format: NetflowFormatCSV,
			want: strings.Join(netflowColumns, ",") + "\n" +
				"2026-10-28T21:30:12Z,2026-10-28T21:30:13Z,1000000000,10.0.0.1,1,10.0.0.2,2,tcp,6,1,1\n",
			stats: NetflowExport{Flows: 1, Skipped: 1},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, stats, started, err := exportNetflow(t.Context(), tt.in, tt.format)
			if err != nil || !started {
				t.Fatalf("ExportNetflow(%s) error = %v, started %t; want nil, started", tt.format, err, started)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ExportNetflow(%s) mismatch (-want +got):\n%s", tt.format, diff)
			}

			if stats != tt.stats {
				t.Errorf("ExportNetflow(%s) stats = %+v, want %+v", tt.format, stats, tt.stats)
			}
		})
	}
}

// A bad format, or a canceled request, writes nothing.
func TestExportNetflowRefuses(t *testing.T) {
	if _, _, started, err := exportNetflow(t.Context(), "", NetflowFormatText); !errors.Is(err, ErrInvalidNetflowFormat) ||
		started {
		t.Errorf("ExportNetflow(text) = %v, started %t; want %v, not started", err, started, ErrInvalidNetflowFormat)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	in := strings.Repeat("2026-10-28T21:30:12Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1\n", 2*netflowCancelCheck)

	if _, _, started, err := exportNetflow(ctx, in, NetflowFormatJSONL); !errors.Is(err, context.Canceled) || started {
		t.Errorf("ExportNetflow canceled = %v, started %t; want %v, not started", err, started, context.Canceled)
	}
}

// genNetflowRecordAddr draws an IPv4 or IPv6 address, as a record holds it.
func genNetflowRecordAddr() *rapid.Generator[netip.Addr] {
	return rapid.Custom(func(t *rapid.T) netip.Addr {
		if rapid.Bool().Draw(t, "v6") {
			return netip.AddrFrom16([16]byte(rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(t, "ip"))).Unmap()
		}

		return netip.AddrFrom4([4]byte(rapid.SliceOfN(rapid.Byte(), 4, 4).Draw(t, "ip")))
	})
}

// genNetflowRecord draws a record minimega could send.
func genNetflowRecord() *rapid.Generator[netflowRecord] {
	return rapid.Custom(func(t *rapid.T) netflowRecord {
		zone := time.FixedZone("", rapid.IntRange(-12, 14).Draw(t, "zone")*60*60)

		return netflowRecord{
			Start:    time.Unix(rapid.Int64Range(0, 1<<35).Draw(t, "start"), 0).In(zone),
			Duration: time.Duration(rapid.Int64Range(0, 1<<45).Draw(t, "duration")),
			Proto:    rapid.Uint8().Draw(t, "proto"),
			Src:      genNetflowRecordAddr().Draw(t, "src"),
			Dst:      genNetflowRecordAddr().Draw(t, "dst"),
			SPort:    rapid.Uint16().Draw(t, "sport"),
			DPort:    rapid.Uint16().Draw(t, "dport"),
			Packets:  rapid.Uint64().Draw(t, "packets"),
			Bytes:    rapid.Uint64().Draw(t, "bytes"),
		}
	})
}

// A record minimega formats parses back to itself.
func TestParseNetflowRecordProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		want := genNetflowRecord().Draw(t, "record")

		// minimega's gonetflow Packet.GoString
		line := fmt.Sprintf("%v\t%v\t%v\t%v:%v\t<->\t%v:%v\t%v\t%v\n", want.Start.Format(time.RFC3339), want.Duration,
			want.Proto, want.Src, want.SPort, want.Dst, want.DPort, want.Packets, want.Bytes)

		got, ok := parseNetflowRecord(line)
		if !ok {
			t.Fatalf("parseNetflowRecord(%q) refused it", line)
		}

		same := cmp.Comparer(func(a, b netip.Addr) bool { return a == b })
		if diff := cmp.Diff(want, got, same, cmp.Comparer(time.Time.Equal)); diff != "" {
			t.Fatalf("parseNetflowRecord(%q) mismatch (-want +got):\n%s", line, diff)
		}
	})
}

// A request that ends mid-export stops it.
func TestExportNetflowStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var (
		out   bytes.Buffer
		lines = 4 * netflowCancelCheck
		in    = strings.Repeat("2026-10-28T21:30:12Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1\n", lines)
	)

	stats, err := ExportNetflow(ctx, func() io.Writer {
		cancel()

		return &out
	}, strings.NewReader(in), NetflowFormatJSONL)

	if !errors.Is(err, context.Canceled) || stats.Flows >= int64(lines) || out.Len() == 0 {
		t.Errorf("ExportNetflow canceled once writing = %+v, %v after %d bytes; want %v partway",
			stats, err, out.Len(), context.Canceled)
	}
}

// The fuzz target's line count holds for lines around the read buffer's size.
func TestNetflowExportLinesAtBufferSize(t *testing.T) {
	for _, n := range []int{netflowLineBuffer - 2, netflowLineBuffer - 1, netflowLineBuffer, netflowLineBuffer + 1} {
		for _, in := range []string{strings.Repeat(" ", n), strings.Repeat(" ", n) + "\n", strings.Repeat("x", n)} {
			_, stats, _, err := exportNetflow(t.Context(), in, NetflowFormatCSV)
			if err != nil || stats.Flows+stats.Skipped != netflowExportLines(in) {
				t.Errorf("ExportNetflow of a %d-byte line (%q...) = %+v, %v; want %d lines", len(in), in[:1], stats, err,
					netflowExportLines(in))
			}
		}
	}
}
