package soh

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// Run metadata comes as a string from scorch and the CLI, and as the values of
// a URL query parameter from the UI.
func TestMetadataString(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		val  any
		want string
	}{
		"string":         {val: "5m", want: "5m"},
		"query values":   {val: []string{"5m", "1h"}, want: "5m"},
		"no values":      {val: []string{}, want: ""},
		"missing":        {val: nil, want: ""},
		"other type":     {val: 5, want: ""},
		"list of others": {val: []any{"5m"}, want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := metadataString(tc.val); got != tc.want {
				t.Errorf("metadataString(%#v) = %q, want %q", tc.val, got, tc.want)
			}
		})
	}
}

func TestMetadataDuration(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		val    any
		want   time.Duration
		wantOK bool
	}{
		"string":       {val: "42s", want: 42 * time.Second, wantOK: true},
		"query values": {val: []string{"1m"}, want: time.Minute, wantOK: true},
		"zero":         {val: "0s", wantOK: true},
		"not duration": {val: "soon"},
		"empty":        {val: ""},
		"missing":      {val: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := metadataDuration(tc.val)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("metadataDuration(%#v) = %v, %t; want %v, %t", tc.val, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestMetadataStrings(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		val  any
		want []string
	}{
		"string":                   {val: "files", want: []string{"files"}},
		"comma-separated string":   {val: "files,flows", want: []string{"files", "flows"}},
		"white space around items": {val: " files , flows ", want: []string{"files", "flows"}},
		"slice":                    {val: []string{"files", "flows"}, want: []string{"files", "flows"}},
		"query value with commas":  {val: []string{"files,flows", "ports"}, want: []string{"files", "flows", "ports"}},
		"empty items":              {val: ",, ,", want: nil},
		"empty slice":              {val: []string{}, want: nil},
		"missing":                  {val: nil, want: nil},
		"other type":               {val: []any{"files"}, want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, metadataStrings(tc.val)); diff != "" {
				t.Errorf("metadataStrings(%#v) mismatch (-want +got):\n%s", tc.val, diff)
			}
		})
	}
}

// Items joined with commas and optional white space, in any grouping into the
// values of a slice, read back as the same items in order.
func TestMetadataStringsProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		items := rapid.SliceOf(rapid.StringMatching(`[a-z][a-z-]{0,11}`)).Draw(t, "items")
		pad := rapid.SampledFrom([]string{"", " ", "\t"})

		var (
			values = make([]string, 0, len(items))
			value  = make([]string, 0, len(items))
		)

		for i, item := range items {
			value = append(value, pad.Draw(t, "before")+item+pad.Draw(t, "after"))

			if i == len(items)-1 || rapid.Bool().Draw(t, "split") {
				values = append(values, strings.Join(value, ","))
				value = value[:0]
			}
		}

		var want []string
		if len(items) > 0 {
			want = items
		}

		if diff := cmp.Diff(want, metadataStrings(values)); diff != "" {
			t.Fatalf("metadataStrings(%q) mismatch (-want +got):\n%s", values, diff)
		}

		if len(values) == 1 {
			if diff := cmp.Diff(want, metadataStrings(values[0])); diff != "" {
				t.Fatalf("metadataStrings(%q) mismatch (-want +got):\n%s", values[0], diff)
			}
		}
	})
}
