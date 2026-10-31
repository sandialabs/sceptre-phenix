package experiment

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"
	"testing"
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
