package util

import (
	"slices"
	"testing"
)

func TestUnique(t *testing.T) {
	t.Parallel()

	in := []string{"b", "a", "b", "c", "a"}

	if got, want := Unique(in), []string{"b", "a", "c"}; !slices.Equal(got, want) {
		t.Errorf("Unique(%v) = %v, want %v", in, got, want)
	}

	// the input is left as it is
	if want := []string{"b", "a", "b", "c", "a"}; !slices.Equal(in, want) {
		t.Errorf("Unique changed its input to %v", in)
	}

	if got := Unique[string](nil); len(got) != 0 {
		t.Errorf("Unique(nil) = %v, want none", got)
	}
}
