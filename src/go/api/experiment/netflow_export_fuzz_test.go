package experiment

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// netflowExportLines counts the lines of in that the export writes or skips:
// the non-blank ones, and those too long to read.
func netflowExportLines(in string) int64 {
	var n int64

	for line := range strings.SplitSeq(in, "\n") {
		if len(line) >= netflowLineBuffer || strings.TrimSpace(line) != "" {
			n++
		}
	}

	return n
}

// Any input converts without panicking, writing or skipping each non-blank
// line, to CSV that parses and JSONL lines that are each valid JSON.
func FuzzExportNetflow(f *testing.F) {
	f.Add(netflowExportInput)
	f.Add("")
	f.Add("\n\n \n")
	f.Add("2026-10-28T21:30:12Z 1s 6 [fe80::1%eth0]:1 <-> 10.0.0.2:2 1 1\n")
	f.Add("2026-10-28T21:30:12Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1")

	f.Fuzz(func(t *testing.T, in string) {
		for _, format := range []string{NetflowFormatCSV, NetflowFormatJSONL} {
			var out bytes.Buffer

			stats, err := ExportNetflow(t.Context(), func() io.Writer { return &out }, strings.NewReader(in), format)
			if err != nil {
				t.Fatalf("ExportNetflow(%s) error = %v", format, err)
			}

			if want := netflowExportLines(in); stats.Flows+stats.Skipped != want {
				t.Errorf("ExportNetflow(%s) of %q wrote %d and skipped %d lines, want %d in all",
					format, in, stats.Flows, stats.Skipped, want)
			}

			if format == NetflowFormatCSV {
				rows, err := csv.NewReader(&out).ReadAll()
				if err != nil || int64(len(rows)) != stats.Flows+1 {
					t.Fatalf("CSV export of %q = %d rows, %v; want %d", in, len(rows), err, stats.Flows+1)
				}

				for _, row := range rows {
					if len(row) != len(netflowColumns) {
						t.Errorf("CSV export row %q has %d columns, want %d", row, len(row), len(netflowColumns))
					}
				}

				continue
			}

			for line := range strings.SplitSeq(strings.TrimSuffix(out.String(), "\n"), "\n") {
				if line != "" && !json.Valid([]byte(line)) {
					t.Errorf("JSONL export line %q is not valid JSON", line)
				}
			}
		}
	})
}

// A line parseNetflowRecord accepts has minimega's eight fields, and the record it gives formats back, as minimega
// formats it, to a line that parses to the same record, exported with a column for each field.
func FuzzParseNetflowRecord(f *testing.F) {
	for _, seed := range []string{
		"2026-10-28T15:30:12-06:00\t2s\t6\t10.0.0.1:1234\t<->\t10.0.0.2:443\t2\t2048\n",
		"2026-10-28T15:30:12-06:00 1.5s 17 fe80::1:53 <-> 2001:db8::2:5353 3 4096",
		"2026-10-28T15:30:12-06:00 0s 253 [2001:db8::1]:1 <-> [::ffff:10.0.0.9]:2 1 1",
		"2026-10-28T15:30:12.5Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1",
		"2026-10-28T15:30:12Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1",
		"2026-10-28T15:30:12Z -1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1",
		"2026-10-28T15:30:12Z 1s 6 [fe80::1%eth0]:1 <-> 10.0.0.2:2 1 1",
		"0 0 6 10.0.0.1:1 <-> 10.0.0.2:2 1 1",
		"",
	} {
		f.Add(seed)
	}

	same := cmp.Options{cmp.Comparer(func(a, b netip.Addr) bool { return a == b }), cmp.Comparer(time.Time.Equal)}

	f.Fuzz(func(t *testing.T, line string) {
		rec, ok := parseNetflowRecord(line)
		if !ok {
			return
		}

		if fields := strings.Fields(line); len(fields) != 8 || fields[4] != "<->" {
			t.Fatalf("parseNetflowRecord(%q) accepted fields %q, want 8 with <-> fifth", line, fields)
		}

		if rec.Duration < 0 || rec.Src.Zone() != "" || rec.Dst.Zone() != "" || rec.Src.Is4In6() || rec.Dst.Is4In6() {
			t.Fatalf("parseNetflowRecord(%q) = %+v, want a duration not negative and unzoned, unmapped addresses", line, rec)
		}

		// minimega's gonetflow Packet.GoString, with fractional seconds kept
		again := fmt.Sprintf("%v\t%v\t%v\t%v\t<->\t%v\t%v\t%v\n", rec.Start.Format(time.RFC3339Nano), rec.Duration, rec.Proto,
			netip.AddrPortFrom(rec.Src, rec.SPort), netip.AddrPortFrom(rec.Dst, rec.DPort), rec.Packets, rec.Bytes)

		got, ok := parseNetflowRecord(again)
		if !ok {
			t.Fatalf("parseNetflowRecord(%q) refused the record %q gave", again, line)
		}

		if diff := cmp.Diff(rec, got, same); diff != "" {
			t.Fatalf("parseNetflowRecord(%q) mismatch with the record of %q (-want +got):\n%s", again, line, diff)
		}

		row := rec.csv()
		if len(row) != len(netflowColumns) || row[8] != strconv.Itoa(int(rec.Proto)) || row[0] != rec.json().Start {
			t.Fatalf("csv() of %+v = %q, want %d columns matching json()", rec, row, len(netflowColumns))
		}
	})
}
