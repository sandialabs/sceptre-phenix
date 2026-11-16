package mm

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
)

func FuzzValidateCaptureFilter(f *testing.F) {
	for _, test := range captureFilterTests {
		f.Add(test.filter)
	}

	f.Fuzz(func(t *testing.T, filter string) {
		if got, want := ValidateCaptureFilter(filter) == nil, filterOracle(filter); got != want {
			t.Fatalf("ValidateCaptureFilter(%q) accepts = %t, want %t", filter, got, want)
		}
	})
}

// registerNamespaceCapturePattern registers a pattern of the experiment capture start with its options as a list.
//
//nolint:gochecknoglobals // minicli's handlers are global; register them once
var registerNamespaceCapturePattern = sync.OnceFunc(func() {
	minicli.MustRegister(&minicli.Handler{Patterns: []string{"capture <pcap,> namespace <file> [option]..."}})
})

// FuzzNamespaceCaptureCommand checks the experiment capture start command against minimega's own command parser: an
// accepted command reaches the handler as the file and the options phenix asked for, each one argument that
// minimega's environment variable expansion leaves as it is. A refused command wraps ErrInvalidCaptureOption or
// ErrUnsafeArg.
func FuzzNamespaceCaptureCommand(f *testing.F) {
	registerNamespaceCapturePattern()

	const file = "exp/files/exp-20261105-100000.pcapng"

	// the options of TestStartNamespaceCapture: file, VLANs (separated by a newline), filter, snaplen, format, maxsize
	for _, seed := range []struct {
		file, vlans, filter string
		snaplen             int
		format              string
		maxSize             int64
	}{
		{file: file},
		{file: file, vlans: "EXP_1\nEXP_2", filter: "tcp port 80 and not (host 10.0.0.1)", snaplen: 1600, format: "pcapng", maxSize: 10240},
		{file: file, snaplen: 14, format: "pcap"},
		{file: "/" + file},
		{file: "exp/files/a b.pcap"},
		{file: ""},
		{file: file, vlans: "A,B"},
		{file: file, vlans: `A"`},
		{file: file, filter: `host "a"`},
		{file: file, filter: "port 80 # x"},
		{file: file, filter: strings.Repeat("a", 1025)},
		{file: file, snaplen: 13},
		{file: file, snaplen: 65536},
		{file: file, format: "erf"},
		{file: "exp/files/$HOME.pcap", vlans: "x=y\n${A}", filter: "vlan 1 = 2", maxSize: -1},
	} {
		f.Add(seed.file, seed.vlans, seed.filter, seed.snaplen, seed.format, seed.maxSize)
	}

	f.Fuzz(func(t *testing.T, file, vlans, filter string, snaplen int, format string, maxSize int64) {
		var vlanList []string
		if vlans != "" {
			vlanList = strings.Split(vlans, "\n")
		}

		o := NewOptions(
			CaptureFile(file), CaptureVLANs(vlanList), CaptureFilter(filter), CaptureSnaplen(snaplen),
			CaptureFormat(format), CaptureMaxSize(maxSize),
		)

		command, err := namespaceCaptureCommand(o)
		if err != nil {
			if !errors.Is(err, ErrInvalidCaptureOption) && !errors.Is(err, ErrUnsafeArg) {
				t.Fatalf("namespaceCaptureCommand() error = %v, want %v or %v", err, ErrInvalidCaptureOption, ErrUnsafeArg)
			}

			return
		}

		var want []string
		if len(vlanList) > 0 {
			want = append(want, "vlans="+strings.Join(vlanList, ","))
		}

		if filter != "" {
			want = append(want, "filter="+filter)
		}

		if snaplen != 0 {
			want = append(want, "snaplen="+strconv.Itoa(snaplen))
		}

		if format != "" {
			want = append(want, "format="+format)
		}

		if maxSize > 0 {
			want = append(want, "maxsize="+strconv.FormatInt(maxSize, 10)+"M")
		}

		cmd, err := minicli.Compile(command)
		if err != nil || cmd == nil {
			t.Fatalf("namespaceCaptureCommand() = %q, which minimega does not take (error %v)", command, err)
		}

		if got := cmd.StringArgs["file"]; got != file {
			t.Errorf("namespaceCaptureCommand() = %q, which minimega reads with the file %q, want %q", command, got, file)
		}

		if diff := cmp.Diff(want, cmd.ListArgs["option"]); diff != "" {
			t.Errorf("namespaceCaptureCommand() = %q options as minimega reads them mismatch (-want +got):\n%s", command, diff)
		}

		// minimega splits the vlans option at each comma
		if opts := cmd.ListArgs["option"]; len(vlanList) > 0 && len(opts) > 0 {
			if diff := cmp.Diff(vlanList, strings.Split(strings.TrimPrefix(opts[0], "vlans="), ",")); diff != "" {
				t.Errorf("namespaceCaptureCommand() = %q VLANs as minimega splits them mismatch (-want +got):\n%s", command, diff)
			}
		}

		if expanded := os.Expand(command, func(string) string { return "<env>" }); expanded != command {
			t.Errorf("namespaceCaptureCommand() = %q, which minimega expands to %q", command, expanded)
		}
	})
}
