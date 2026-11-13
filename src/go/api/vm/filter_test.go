package vm_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/vm"
)

// FilterVMs returns the names of the VMs a filter matches, none for an empty
// filter, and ErrNoFilterMatch for a filter that matches no VM.
//
//nolint:paralleltest // replaces the store and minimega
func TestFilterVMs(t *testing.T) {
	tests := []struct {
		name    string
		filter  string
		want    []string
		noMatch bool
	}{
		{name: "an empty filter restricts nothing", filter: "", want: []string{}},
		{name: "a name term", filter: "test-vm", want: []string{"test-vm"}},
		{name: "a state term", filter: "running", want: []string{"test-vm"}},
		{name: "no VM matches", filter: "other-vm", noMatch: true},
		{name: "a filter that does not parse", filter: `"`, noMatch: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useCaptureVM(t, &fakeMM{}, "EXP_1 (101)", "EXP_2 (102)")

			got, err := vm.FilterVMs(t.Context(), testExp, test.filter)
			if errors.Is(err, vm.ErrNoFilterMatch) != test.noMatch {
				t.Fatalf("FilterVMs(%q) error = %v, want ErrNoFilterMatch %t", test.filter, err, test.noMatch)
			}

			if test.noMatch {
				return
			}

			if err != nil {
				t.Fatalf("FilterVMs(%q) error = %v", test.filter, err)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("FilterVMs(%q) mismatch (-want +got):\n%s", test.filter, diff)
			}
		})
	}
}
