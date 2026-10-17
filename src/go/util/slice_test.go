package util

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

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
