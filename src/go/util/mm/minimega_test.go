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

func TestDiskDeleteCommand(t *testing.T) {
	tests := []struct {
		osType string
		want   string
	}{
		{
			osType: "linux",
			want:   `disk inject vm_snapshot:1 delete files "/etc/old-config","/var/tmp/stale dir"`,
		},
		{
			osType: "windows",
			want: `disk inject vm_snapshot:1 options "-t lowntfs-3g -o ignore_case" ` +
				`delete files "/etc/old-config","/var/tmp/stale dir"`,
		},
	}

	for _, test := range tests {
		t.Run(test.osType, func(t *testing.T) {
			got := diskDeleteCommand(
				"vm_snapshot", 1, test.osType, "/etc/old-config", "/var/tmp/stale dir",
			)
			if got != test.want {
				t.Fatalf("diskDeleteCommand returned %q, want %q", got, test.want)
			}
		})
	}
}
