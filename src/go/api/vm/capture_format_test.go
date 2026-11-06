package vm_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// useCaptureSupport gives the test minimega's capture support: forced on, or a minimega that answers the version
// command with 3.3.0.
func useCaptureSupport(t *testing.T, available bool) {
	t.Helper()

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	if available {
		mm.SetCaptureFeaturesForced(true)
		t.Cleanup(func() { mm.SetCaptureFeaturesForced(false) })

		return
	}

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "version" {
			return []*minicli.Response{mmtest.Text("head", "minimega 3.3.0 abc123 2026-01-01")}
		}

		return nil
	})
}

func TestCaptureFile(t *testing.T) {
	tests := []struct {
		out, format, want string
	}{
		{out: "out", format: mm.CaptureFormatPcapng, want: "exp/files/out.pcapng"},
		{out: "out.pcap", format: mm.CaptureFormatPcapng, want: "exp/files/out.pcapng"},
		{out: "out.PCAPNG", format: mm.CaptureFormatPcap, want: "exp/files/out.pcap"},
		{out: "out.pcap", format: mm.CaptureFormatPcap, want: "exp/files/out.pcap"},
		{out: "out.txt", format: mm.CaptureFormatPcap, want: "exp/files/out.txt.pcap"},
		{out: "dir/../out.pcapng", format: mm.CaptureFormatPcapng, want: "exp/files/out.pcapng"},
		{out: ".pcap", format: mm.CaptureFormatPcap, want: "exp/files/.pcap.pcap"},
		{out: "/", format: mm.CaptureFormatPcapng, want: "exp/files/capture.pcapng"},
		{out: "dir/..", format: mm.CaptureFormatPcap, want: "exp/files/capture.pcap"},
		{out: ".", format: mm.CaptureFormatPcap, want: "exp/files/capture.pcap"},
	}

	for _, test := range tests {
		t.Run(test.out+" as "+test.format, func(t *testing.T) {
			if got := vm.CaptureFileOf("exp", test.out, test.format); got != test.want {
				t.Errorf("captureFile(exp, %q, %s) = %q, want %q", test.out, test.format, got, test.want)
			}
		})
	}
}

// A capture file is always in the experiment's files directory, with the format's extension.
func TestCaptureFileProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		out := rapid.String().Draw(t, "out")
		format := rapid.SampledFrom([]string{mm.CaptureFormatPcapng, mm.CaptureFormatPcap}).Draw(t, "format")

		got := vm.CaptureFileOf("exp", out, format)

		name, ok := strings.CutPrefix(got, "exp/files/")
		if !ok || strings.Contains(name, "/") {
			t.Fatalf("captureFile(exp, %q, %s) = %q, want a file in exp/files", out, format, got)
		}

		if !strings.HasSuffix(name, "."+format) {
			t.Fatalf("captureFile(exp, %q, %s) = %q, want the extension .%s", out, format, got, format)
		}
	})
}

func TestCaptureFormat(t *testing.T) {
	tests := []struct {
		name      string
		available bool
		format    string
		want      string
		wantErr   error
	}{
		{name: "pcapng by default", available: true, want: mm.CaptureFormatPcapng},
		{name: "pcap when asked", available: true, format: mm.CaptureFormatPcap, want: mm.CaptureFormatPcap},
		{name: "pcapng when asked", available: true, format: mm.CaptureFormatPcapng, want: mm.CaptureFormatPcapng},
		{name: "pcap from an older minimega", want: mm.CaptureFormatPcap},
		{name: "pcap from an older minimega asked for pcapng", format: mm.CaptureFormatPcapng, want: mm.CaptureFormatPcap},
		{name: "refuses another format", available: true, format: "erf", wantErr: vm.ErrInvalidFormat},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useCaptureSupport(t, test.available)

			got, err := vm.CaptureFormatOf(t.Context(), vm.CaptureOptions{Format: test.format, MaxSizeMiB: 0})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CaptureOptions{Format: %q}.format() error = %v, want %v", test.format, err, test.wantErr)
			}

			if got != test.want {
				t.Errorf("CaptureOptions{Format: %q}.format() = %q, want %q", test.format, got, test.want)
			}
		})
	}
}

// A start in a format that minimega does not write sends nothing to minimega.
func TestCaptureStartsRefuseFormat(t *testing.T) {
	opts := vm.CaptureOptions{Format: "erf", MaxSizeMiB: 0}

	t.Run("StartCapture", func(t *testing.T) {
		fake := useCaptureVM(t, &fakeMM{}, "EXP_1 (101)", "EXP_2 (102)")

		err := vm.StartCapture(t.Context(), testExp, "test-vm", 0, "out", opts)
		if !errors.Is(err, vm.ErrInvalidFormat) {
			t.Errorf("StartCapture(format erf) error = %v, want %v", err, vm.ErrInvalidFormat)
		}

		if fake.starts != 0 {
			t.Errorf("StartCapture(format erf) minimega capture starts = %d, want 0", fake.starts)
		}
	})

	t.Run("CaptureVLAN", func(t *testing.T) {
		fake := useSubnetVMs(t, false, &fakeMM{})

		if _, err := vm.CaptureVLAN(t.Context(), testExp, "EXP_1", nil, opts); !errors.Is(err, vm.ErrInvalidFormat) {
			t.Errorf("CaptureVLAN(format erf) error = %v, want %v", err, vm.ErrInvalidFormat)
		}

		if fake.starts != 0 {
			t.Errorf("CaptureVLAN(format erf) minimega capture starts = %d, want 0", fake.starts)
		}
	})

	t.Run("CaptureSubnet", func(t *testing.T) {
		fake := useSubnetVMs(t, false, &fakeMM{})

		if _, err := vm.CaptureSubnet(t.Context(), testExp, "10.0.0.0/24", nil, opts); !errors.Is(err, vm.ErrInvalidFormat) {
			t.Errorf("CaptureSubnet(format erf) error = %v, want %v", err, vm.ErrInvalidFormat)
		}

		if fake.starts != 0 {
			t.Errorf("CaptureSubnet(format erf) minimega capture starts = %d, want 0", fake.starts)
		}
	})
}
