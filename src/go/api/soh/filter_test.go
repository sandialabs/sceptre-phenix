package soh

import (
	"errors"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

// ValidateStatusFilter takes no filter and each of StatusFilters, and refuses every other value.
func TestValidateStatusFilterProperties(t *testing.T) {
	t.Parallel()

	for _, filter := range append(StatusFilters(), "") {
		if err := ValidateStatusFilter(filter); err != nil {
			t.Errorf("ValidateStatusFilter(%q) error = %v, want none", filter, err)
		}
	}

	rapid.Check(t, func(t *rapid.T) {
		filter := rapid.String().Draw(t, "filter")
		if filter == "" || slices.Contains(StatusFilters(), filter) {
			return
		}

		if err := ValidateStatusFilter(filter); !errors.Is(err, ErrInvalidStatusFilter) {
			t.Fatalf("ValidateStatusFilter(%q) error = %v, want %v", filter, err, ErrInvalidStatusFilter)
		}
	})
}
