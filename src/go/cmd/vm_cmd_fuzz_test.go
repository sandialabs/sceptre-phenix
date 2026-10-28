package cmd

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

// FuzzNormalizeVMFilterValues checks that the --label values split into
// pieces that are not empty, have no comma and no space at an end, and keep
// every other character of the values in order.
func FuzzNormalizeVMFilterValues(f *testing.F) {
	for _, seed := range []string{"", "a", "a,b", " a , b ", ",,", "ot-*, *-server", "a\tb, c ", "x\ny"} {
		f.Add(seed, "")
		f.Add(seed, seed)
	}

	f.Fuzz(func(t *testing.T, first, second string) {
		if !utf8.ValidString(first) || !utf8.ValidString(second) {
			t.Skip("flag values are text")
		}

		in := []string{first, second}
		got := normalizeVMFilterValues(in)

		for _, piece := range got {
			if piece == "" || strings.Contains(piece, ",") || piece != strings.TrimSpace(piece) {
				t.Fatalf("normalizeVMFilterValues(%q) piece %q is empty, has a comma or a space at an end", in, piece)
			}
		}

		drop := func(r rune) rune {
			if r == ',' || unicode.IsSpace(r) {
				return -1
			}

			return r
		}

		if want, kept := strings.Map(drop, first+second), strings.Map(drop, strings.Join(got, "")); kept != want {
			t.Fatalf("normalizeVMFilterValues(%q) = %q keeps %q, want %q", in, got, kept, want)
		}

		if diff := cmp.Diff(got, normalizeVMFilterValues(got)); diff != "" {
			t.Fatalf("normalizeVMFilterValues(%q) is not a fixed point (-want +got):\n%s", got, diff)
		}
	})
}

// FuzzParseVMInfoFilters checks that parseVMInfoFilters gives one filter for
// each --filter value, on a known column with a trimmed value, and that a
// filter written again as COLUMN=VALUE parses to itself.
func FuzzParseVMInfoFilters(f *testing.F) {
	for _, seed := range []string{"RUNNING=true", "os type = linux", "Os_Type=x", "ram=512", "CPUS=2", "disk=", "=x", "name", "state=on", "taps=a=b"} {
		f.Add(seed, "NAME=web")
	}

	known := map[string]bool{
		vmInfoColumnHost: true, vmInfoColumnName: true, vmInfoColumnRunning: true, vmInfoColumnDisk: true,
		vmInfoColumnInterfaces: true, vmInfoColumnUptime: true, vmInfoColumnMemory: true, vmInfoColumnVCPUs: true,
		vmInfoColumnOSType: true, vmInfoColumnTaps: true,
	}

	f.Fuzz(func(t *testing.T, first, second string) {
		raw := []string{first, second}

		filters, err := parseVMInfoFilters(raw)
		if err != nil {
			if filters != nil {
				t.Fatalf("parseVMInfoFilters(%q) = %v, %v, want no filters with the error", raw, filters, err)
			}

			return
		}

		if len(filters) != len(raw) {
			t.Fatalf("parseVMInfoFilters(%q) gave %d filters, want %d", raw, len(filters), len(raw))
		}

		again := make([]string, 0, len(filters))

		for _, filter := range filters {
			if !known[filter.column] || filter.value != strings.TrimSpace(filter.value) {
				t.Fatalf("parseVMInfoFilters(%q) filter %+v has an unknown column or an untrimmed value", raw, filter)
			}

			again = append(again, filter.column+"="+filter.value)
		}

		reparsed, err := parseVMInfoFilters(again)
		if err != nil {
			t.Fatalf("parseVMInfoFilters(%q) error = %v, want the filters of %q", again, err, raw)
		}

		if diff := cmp.Diff(filters, reparsed, cmp.AllowUnexported(vmInfoFilter{})); diff != "" {
			t.Fatalf("parseVMInfoFilters(%q) mismatch with the filters of %q (-want +got):\n%s", again, raw, diff)
		}
	})
}
