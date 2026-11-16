package mm_test

import (
	"errors"
	"testing"

	"phenix/util/mm"
)

func TestDiskSpecPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		spec, want string
		wantErr    error
	}{
		{spec: "new.qc2", want: "new.qc2"},
		{spec: "/phenix/images/new.qc2,virtio", want: "/phenix/images/new.qc2"},
		{spec: "new.qc2,writeback", want: "new.qc2"},
		{spec: "new.qc2,ide,none", want: "new.qc2"},
		{spec: "new.qc2,bogus", wantErr: mm.ErrMalformedDiskSpec},
		{spec: ",virtio", wantErr: mm.ErrMalformedDiskSpec},
		{spec: "", wantErr: mm.ErrMalformedDiskSpec},
	}

	for _, test := range tests {
		t.Run(test.spec, func(t *testing.T) {
			t.Parallel()

			got, err := mm.DiskSpecPath(test.spec)
			if !errors.Is(err, test.wantErr) {
				t.Errorf("DiskSpecPath(%q) error = %v, want %v", test.spec, err, test.wantErr)
			}

			if got != test.want {
				t.Errorf("DiskSpecPath(%q) = %q, want %q", test.spec, got, test.want)
			}
		})
	}
}
