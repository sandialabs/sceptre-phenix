package mm

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

// StopVMCapture tells "stop only the capture on interface 0" (0 is also the
// options' zero value) from "stop every capture of the VM".
func TestStopVMCapture(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		opts      []Option
		none      bool   // the VM has no captures
		deleteErr string // the delete's error, "" for success
		want      []string
		wantErr   error
		wantText  string // the start of the error, which holds the delete's error; "" to skip
	}{
		"reports a VM without captures": {
			none:    true,
			want:    []string{captureCmd},
			wantErr: ErrNoCaptures,
		},
		"reports an empty list of captures given": {
			opts:    []Option{KnownCaptures(nil)},
			want:    nil,
			wantErr: ErrNoCaptures,
		},
		"reports a failed delete of every capture": {
			deleteErr: "boom",
			want:      []string{captureCmd, "capture pcap delete vm vm"},
			wantText:  "deleting VM captures for VM vm in namespace exp: ",
		},
		"reports a failed delete of one capture": {
			opts:      []Option{CaptureInterface(3)},
			deleteErr: "boom",
			want:      []string{captureCmd, "capture pcap delete vm vm 3"},
			wantText:  "deleting VM capture for interface 3 on VM vm in namespace exp: ",
		},
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
		"stops the captures it is given without listing them": {
			opts: []Option{KnownCaptures([]Capture{{VM: "vm", Interface: 0, Filepath: "/vm0.pcap"}})},
			want: []string{"capture pcap delete vm vm"},
		},
		"reports an interface without a capture among those given": {
			opts:    []Option{CaptureInterface(3), KnownCaptures([]Capture{{VM: "vm", Interface: 0, Filepath: "/vm0.pcap"}})},
			want:    nil,
			wantErr: ErrNoCaptures,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch {
				case cmd.Base != captureCmd && tc.deleteErr != "":
					return []*minicli.Response{mmtest.Error("head", tc.deleteErr)}
				case cmd.Base != captureCmd:
					return nil
				case tc.none:
					return []*minicli.Response{mmtest.Tabular("head", []string{"interface", "path"})}
				}

				return []*minicli.Response{mmtest.Tabular("head", []string{"interface", "path"},
					[]string{"vm:0", "/vm0.pcap"},
					[]string{"vm:3", "/vm3.pcap"},
					[]string{"other:1", "/other1.pcap"},
				)}
			})

			err := (Minimega{}).StopVMCapture(append([]Option{NS("exp"), VMName("vm")}, tc.opts...)...)
			switch {
			case tc.wantText != "":
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantText) || !strings.Contains(err.Error(), tc.deleteErr) {
					t.Errorf("StopVMCapture() error = %v, want %q and %q", err, tc.wantText, tc.deleteErr)
				}
			case !errors.Is(err, tc.wantErr):
				t.Errorf("StopVMCapture() error = %v, want %v", err, tc.wantErr)
			}

			want := make([]mmtest.Command, 0, len(tc.want))
			for _, base := range tc.want {
				want = append(want, mmtest.Command{Namespace: "exp", Base: base})
			}

			if diff := cmp.Diff(want, received(), ignoreRaw, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("StopVMCapture() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// StartVMCapture makes the capture's directory on the VM's node, then starts
// the capture, unless the interface is captured already or the file is unsafe.
func TestStartVMCapture(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	const (
		listing = `.record false namespace "exp" .columns "interface","path" capture`
		lookup  = `.record false namespace "exp" .columns "name" .filter name=a vm info`
		start   = `.record false namespace "exp" capture pcap vm a 1 exp/files/a.pcap`
	)

	mkdir := "shell mkdir -p " + common.PhenixBase + "/images/exp/files"

	for name, tc := range map[string]struct {
		host     string // the node running VM a, "" for none
		file     string
		captured bool   // interface 1 is captured already
		failing  string // the base of the command minimega fails, "" for none
		want     []string
		wantIs   error
		wantMsg  string // a part of the error, "" for none
	}{
		"on the headnode": {
			host: "head", file: "exp/files/a.pcap",
			want: []string{listing, lookup, ".record false  " + mkdir, start},
		},
		"on another node": {
			host: "compute1", file: "exp/files/a.pcap",
			want: []string{listing, lookup, ".record false mesh send compute1 " + mkdir, start},
		},
		"reports a capture already on the interface": {
			host: "head", file: "exp/files/a.pcap", captured: true, wantIs: ErrCaptureExists,
			want: []string{listing},
		},
		"refuses an absolute file": {
			host: "head", file: "/exp/files/a.pcap", wantMsg: "should not be absolute",
			want: []string{listing},
		},
		"refuses a file minimega would split": {
			host: "head", file: "exp/files/my capture.pcap", wantIs: ErrUnsafeArg,
			want: []string{listing},
		},
		"reports a missing VM": {
			file: "exp/files/a.pcap", wantMsg: "vm a not found",
			want: []string{listing, lookup},
		},
		"reports a failed mkdir": {
			host: "compute1", file: "exp/files/a.pcap", failing: "mesh send compute1 " + mkdir, wantMsg: "boom",
			want: []string{listing, lookup, ".record false mesh send compute1 " + mkdir},
		},
		"reports a failed capture": {
			host: "head", file: "exp/files/a.pcap", failing: "capture pcap vm a 1 exp/files/a.pcap", wantMsg: "boom",
			want: []string{listing, lookup, ".record false  " + mkdir, start},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch {
				case cmd.Base == tc.failing || strings.TrimSpace(cmd.Base) == tc.failing:
					return []*minicli.Response{mmtest.Error("head", "boom")}
				case cmd.Base == captureCmd && tc.captured:
					return []*minicli.Response{mmtest.Tabular("head", []string{"interface", "path"}, []string{"a:1", "/a.pcap"})}
				case cmd.Base == captureCmd:
					return []*minicli.Response{mmtest.Tabular("head", []string{"interface", "path"}, []string{"a:0", "/a.pcap"})}
				case cmd.Base == vmInfoCmd && tc.host != "":
					return []*minicli.Response{mmtest.Tabular(tc.host, []string{"name"}, []string{"a"})}
				}

				return answering(nil)(cmd)
			})

			err := StartVMCapture(NS("exp"), VMName("a"), CaptureInterface(1), CaptureFile(tc.file))

			switch {
			case tc.wantIs == nil && tc.wantMsg == "" && err != nil:
				t.Errorf("StartVMCapture(a, 1, %s) error = %v, want nil", tc.file, err)
			case tc.wantIs != nil && !errors.Is(err, tc.wantIs):
				t.Errorf("StartVMCapture(a, 1, %s) error = %v, want %v", tc.file, err, tc.wantIs)
			case tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)):
				t.Errorf("StartVMCapture(a, 1, %s) error = %v, want one holding %q", tc.file, err, tc.wantMsg)
			}

			if diff := cmp.Diff(tc.want, sentRaw(received())); diff != "" {
				t.Errorf("StartVMCapture(a, 1, %s) sent commands mismatch (-want +got):\n%s", tc.file, diff)
			}
		})
	}
}
