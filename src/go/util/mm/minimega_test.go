package mm

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/mm/mmtest"
)

func TestConnectVMInterface(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		opts []Option
		want string
	}{
		"with bridge":    {opts: []Option{Bridge("phenix")}, want: "vm net connect test-vm 1 EXP_1 phenix"},
		"without bridge": {want: "vm net connect test-vm 1 EXP_1"},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, nil)

			opts := append([]Option{NS("exp"), VMName("test-vm"), ConnectInterface(1), ConnectVLAN("EXP_1")}, tc.opts...)

			if err := (Minimega{}).ConnectVMInterface(opts...); err != nil {
				t.Errorf("ConnectVMInterface() error = %v", err)
			}

			want := []mmtest.Command{{Raw: `.record false namespace "exp" ` + tc.want, Namespace: "exp", Base: tc.want}}

			if diff := cmp.Diff(want, received()); diff != "" {
				t.Errorf("ConnectVMInterface() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
