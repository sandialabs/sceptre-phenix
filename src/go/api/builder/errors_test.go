package builder

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
)

func TestErrorMessages(t *testing.T) {
	t.Parallel()

	full, gone := errors.New("disk full"), errors.New("disk gone")

	for name, tt := range map[string]struct {
		err  error
		want string
	}{
		"a conflict": {
			err:  &ConflictError{Kind: kindDraft, ID: "d", Expected: 2, Actual: 3, Reason: ""},
			want: `builder: revision conflict: draft "d" (expected revision 2, actual revision 3)`,
		},
		"a conflict with a reason": {
			err:  &ConflictError{Kind: kindDraft, ID: "d", Expected: 2, Actual: 0, Reason: "already exists"},
			want: `builder: revision conflict: draft "d" (expected revision 2, actual revision 0): already exists`,
		},
		"a missing record": {
			err:  &NotFoundError{Kind: kindDraft, ID: "d"},
			want: `builder: not found: draft "d"`,
		},
		"a taken icon name": {
			err:  &IconNameTakenError{Name: "PLC", Icon: "plc", Owner: "alice"},
			want: `builder: revision conflict: icon name "PLC" belongs to an icon alice uploaded. Choose another name`,
		},
		"a cleanup with one failure": {
			err:  &CleanupError{Op: "deleting draft d", Errors: []error{full}},
			want: "builder: cleanup failed: deleting draft d: disk full",
		},
		"a cleanup with two failures": {
			err:  &CleanupError{Op: "deleting draft d", Errors: []error{full, gone}},
			want: "builder: cleanup failed: deleting draft d: disk full. disk gone",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, tt.err.Error()); diff != "" {
				t.Errorf("Error() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestStoreErrorOfAnInvalidRecord gives storeError the error of a namespace or
// a key that the store refuses: the caller gets an ErrInvalid for the object.
func TestStoreErrorOfAnInvalidRecord(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"namespace": store.ValidateRecordNamespace(""),
		"key":       store.ValidateRecordKey(""),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if cause == nil {
				t.Fatalf("the store accepts an empty %s, want an error to translate", name)
			}

			err := storeError(kindDraft, "", store.AnyRevision, cause)

			want := &ValidationError{Field: kindDraft, Reason: cause.Error(), Cause: nil}
			if diff := cmp.Diff(want, err); diff != "" {
				t.Errorf("storeError(invalid %s) mismatch (-want +got):\n%s", name, diff)
			}

			if !errors.Is(err, ErrInvalid) {
				t.Errorf("storeError(invalid %s) = %v, want it to match ErrInvalid", name, err)
			}
		})
	}
}
