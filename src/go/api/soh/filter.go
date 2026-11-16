package soh

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrInvalidStatusFilter is returned for a VM status that GetFor cannot filter on.
var ErrInvalidStatusFilter = errors.New("invalid VM status filter")

// StatusFilters returns the VM statuses that the statusFilter of GetFor takes, as GetFor names them.
func StatusFilters() []string {
	return []string{"running", "notrunning", "notboot", "notdeploy", "external"}
}

// ValidateStatusFilter returns an error wrapping ErrInvalidStatusFilter unless filter is "" (no filter) or one of
// StatusFilters.
func ValidateStatusFilter(filter string) error {
	if filter == "" || slices.Contains(StatusFilters(), filter) {
		return nil
	}

	return fmt.Errorf("%w %q: use one of %s", ErrInvalidStatusFilter, filter,
		strings.Join(StatusFilters(), ", "))
}
