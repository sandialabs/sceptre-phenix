package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"phenix/api/vlan"
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

// FuzzParseLabelFlags checks that vm set --label takes a value only as
// key=value with a key that is not blank, splits it at the first "=", and
// keeps the key and value as given.
func FuzzParseLabelFlags(f *testing.F) {
	for _, seed := range []string{"tier=front", "tier=", "=front", " =x", "tier", "a=b=c", "k = v", "", "é=ü", "\x00=\x00"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		got, err := parseLabelFlags([]string{value})

		key, val, found := strings.Cut(value, "=")
		if !found || strings.TrimSpace(key) == "" {
			if err == nil {
				t.Fatalf("parseLabelFlags(%q) = %v, want an error", value, got)
			}

			if want := fmt.Sprintf("invalid label %q (expected key=value)", value); err.Error() != want {
				t.Fatalf("parseLabelFlags(%q) error = %q, want %q", value, err, want)
			}

			return
		}

		if err != nil {
			t.Fatalf("parseLabelFlags(%q) error = %v, want nil", value, err)
		}

		if diff := cmp.Diff(map[string]string{key: val}, got); diff != "" {
			t.Fatalf("parseLabelFlags(%q) mismatch (-want +got):\n%s", value, diff)
		}

		if again, err := parseLabelFlags([]string{key + "=" + got[key]}); err != nil || !cmp.Equal(got, again) {
			t.Fatalf("parseLabelFlags(%q) = %v, %v; want %v again", key+"="+got[key], again, err, got)
		}
	})
}

// FuzzParseIfaceIndex checks that vm net takes an interface index only as a
// whole number of 0 or more, and refuses every other argument with
// errBadIfaceIndex and the argument quoted.
func FuzzParseIfaceIndex(f *testing.F) {
	for _, seed := range []string{"0", "1", "07", "+1", "-1", "IF0", "", " 1", "1 ", "1.0", "0x1", "9223372036854775807", "9223372036854775808", "١"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, arg string) {
		got, err := parseIfaceIndex(arg)

		want, atoiErr := strconv.Atoi(arg)
		if atoiErr != nil || want < 0 {
			if !errors.Is(err, errBadIfaceIndex) || !strings.Contains(err.Error(), strconv.Quote(arg)) {
				t.Fatalf("parseIfaceIndex(%q) = %d, %v; want an error wrapping errBadIfaceIndex that quotes the argument", arg, got, err)
			}

			return
		}

		if err != nil || got != want {
			t.Fatalf("parseIfaceIndex(%q) = %d, %v; want %d, nil", arg, got, err, want)
		}

		if again, err := parseIfaceIndex(strconv.Itoa(got)); err != nil || again != got {
			t.Fatalf("parseIfaceIndex(%q) = %d, %v; want %d, nil", strconv.Itoa(got), again, err, got)
		}
	})
}

// FuzzParseCLIVLANID checks that the vlan commands take a VLAN ID only as a
// whole number in 1–4094, and refuse every other argument with errBadVLANID
// and the argument quoted.
func FuzzParseCLIVLANID(f *testing.F) {
	for _, seed := range []string{"1", "100", "4094", "0", "4095", "-1", "+5", "007", "x", "", " 10", "1e3", "١٠٠"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, arg string) {
		got, err := parseCLIVLANID(arg)

		want, atoiErr := strconv.Atoi(arg)
		if atoiErr != nil || want < vlan.MinID || want > vlan.MaxID {
			if !errors.Is(err, errBadVLANID) || !strings.Contains(err.Error(), strconv.Quote(arg)) {
				t.Fatalf("parseCLIVLANID(%q) = %d, %v; want an error wrapping errBadVLANID that quotes the argument", arg, got, err)
			}

			return
		}

		if err != nil || got != want || !vlan.ValidID(got) {
			t.Fatalf("parseCLIVLANID(%q) = %d, %v; want %d, nil", arg, got, err, want)
		}
	})
}
