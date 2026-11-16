package util

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

func TestStringSliceContains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		slice []string
		s     string
		want  bool
	}{
		{name: "nil", slice: nil, s: "a", want: false},
		{name: "empty string in nil", slice: nil, s: "", want: false},
		{name: "first", slice: []string{"a", "b"}, s: "a", want: true},
		{name: "last", slice: []string{"a", "b"}, s: "b", want: true},
		{name: "missing", slice: []string{"a", "b"}, s: "c", want: false},
		{name: "empty string present", slice: []string{"a", ""}, s: "", want: true},
		{name: "case differs", slice: []string{"A"}, s: "a", want: false},
		{name: "prefix only", slice: []string{"abc"}, s: "ab", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := StringSliceContains(test.slice, test.s); got != test.want {
				t.Errorf("StringSliceContains(%q, %q) = %t, want %t", test.slice, test.s, got, test.want)
			}
		})
	}
}

func TestUnique(t *testing.T) {
	t.Parallel()

	in := []string{"b", "a", "b", "c", "a"}

	if diff := cmp.Diff([]string{"b", "a", "c"}, Unique(in)); diff != "" {
		t.Errorf("Unique(%q) mismatch (-want +got):\n%s", in, diff)
	}

	if diff := cmp.Diff([]string{"b", "a", "b", "c", "a"}, in); diff != "" {
		t.Errorf("input after Unique mismatch (-want +got):\n%s", diff)
	}

	if got := Unique[string](nil); len(got) != 0 {
		t.Errorf("Unique(nil) = %v, want none", got)
	}
}

// genSliceRepeats draws strings from a small alphabet so most slices repeat some.
func genSliceRepeats() *rapid.Generator[[]string] {
	return rapid.SliceOf(rapid.SampledFrom([]string{"a", "b", "c", "d", "e"}))
}

func TestUniqueProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		in := genSliceRepeats().Draw(t, "in")
		before := slices.Clone(in)
		got := Unique(in)

		if diff := cmp.Diff(before, in); diff != "" {
			t.Fatalf("input after Unique mismatch (-want +got):\n%s", diff)
		}

		// each value appears once, in the order of its first appearance in in
		last := -1

		for i, v := range got {
			if slices.Contains(got[:i], v) {
				t.Fatalf("Unique(%q) = %q, repeats %q", in, got, v)
			}

			first := slices.Index(in, v)
			if first <= last {
				t.Fatalf("Unique(%q) = %q, %q is out of first-appearance order", in, got, v)
			}

			last = first
		}

		for _, v := range in {
			if !slices.Contains(got, v) {
				t.Fatalf("Unique(%q) = %q, missing %q", in, got, v)
			}
		}

		if again := Unique(got); !slices.Equal(again, got) {
			t.Fatalf("Unique(Unique(%q)) = %q, want %q", in, again, got)
		}
	})
}
