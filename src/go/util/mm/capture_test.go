package mm

import (
	"errors"
	"slices"
	"testing"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
)

// StopVMCapture tells "stop only the capture on interface 0" (0 is also the
// options' zero value) from "stop every capture of the VM".
func TestStopVMCapture(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		opts    []Option
		want    []string
		wantErr error
	}{
		"stops the capture on interface 0": {
			opts: []Option{CaptureInterface(0)},
			want: []string{captureCmd, "capture pcap delete vm vm 0"},
		},
		"stops the capture on another interface": {
			opts: []Option{CaptureInterface(3)},
			want: []string{captureCmd, "capture pcap delete vm vm 3"},
		},
		"stops every capture without an interface": {
			want: []string{captureCmd, "capture pcap delete vm vm"},
		},
		"reports an interface without a capture": {
			opts:    []Option{CaptureInterface(1)},
			want:    []string{captureCmd},
			wantErr: ErrNoCaptures,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base != captureCmd {
					return nil
				}

				return []*minicli.Response{mmtest.Tabular("head", []string{"interface", "path"},
					[]string{"vm:0", "/vm0.pcap"},
					[]string{"vm:3", "/vm3.pcap"},
					[]string{"other:1", "/other1.pcap"},
				)}
			})

			err := (Minimega{}).StopVMCapture(append([]Option{NS("exp"), VMName("vm")}, tc.opts...)...)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("StopVMCapture = %v, want %v", err, tc.wantErr)
			}

			cmds := received()

			if got := mmtest.Bases(cmds); !slices.Equal(got, tc.want) {
				t.Fatalf("sent %q, want %q", got, tc.want)
			}

			for _, cmd := range cmds {
				if cmd.Namespace != "exp" {
					t.Fatalf("%q sent in namespace %q, want exp", cmd.Base, cmd.Namespace)
				}
			}
		})
	}
}
