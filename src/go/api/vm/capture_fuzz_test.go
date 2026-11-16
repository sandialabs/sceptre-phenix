package vm_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"phenix/api/vm"
	"phenix/util/mm"
)

// FuzzCaptureFile checks the file of a VM capture for any user-supplied file name: it is one path element in the
// experiment's files directory with the format's extension, made from the base name of the input without one .pcap
// or .pcapng extension, and it equals the table's file for each seed.
func FuzzCaptureFile(f *testing.F) {
	seeds := []struct {
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

	want := make(map[[2]string]string, len(seeds))

	for _, seed := range seeds {
		f.Add(seed.out, seed.format == mm.CaptureFormatPcapng)
		want[[2]string{seed.out, seed.format}] = seed.want
	}

	f.Fuzz(func(t *testing.T, out string, pcapng bool) {
		format := mm.CaptureFormatPcap
		if pcapng {
			format = mm.CaptureFormatPcapng
		}

		got := vm.CaptureFileOf("exp", out, format)

		if w, ok := want[[2]string{out, format}]; ok && got != w {
			t.Fatalf("captureFile(exp, %q, %s) = %q, want %q", out, format, got, w)
		}

		name, ok := strings.CutPrefix(got, "exp/files/")
		if !ok || name == "" || strings.Contains(name, "/") || filepath.Join("exp", "files", name) != got {
			t.Fatalf("captureFile(exp, %q, %s) = %q, want one path element in exp/files", out, format, got)
		}

		stem, ok := strings.CutSuffix(name, "."+format)
		if !ok || stem == "" {
			t.Fatalf("captureFile(exp, %q, %s) = %q, want a name with the extension .%s", out, format, got, format)
		}

		if err := checkCaptureStem(out, stem); err != nil {
			t.Fatalf("captureFile(exp, %q, %s) = %q: %v", out, format, got, err)
		}
	})
}

// checkCaptureStem returns why stem is not the capture name of out, or nil when it is: "capture" for an input with no
// file name, else the base name of out without at most one .pcap or .pcapng extension.
func checkCaptureStem(out, stem string) error {
	base := filepath.Base(out)
	if base == "/" || base == "." || base == ".." {
		if stem != "capture" {
			return errors.New("want the name capture for an input with no file name")
		}

		return nil
	}

	rest, ok := strings.CutPrefix(base, stem)
	if !ok {
		return errors.New("want a name that the base name " + base + " starts with")
	}

	switch {
	case rest == "":
		for _, ext := range []string{".pcapng", ".pcap"} {
			if len(base) > len(ext) && strings.EqualFold(base[len(base)-len(ext):], ext) {
				return errors.New("want the extension " + ext + " removed")
			}
		}
	case !strings.EqualFold(rest, ".pcapng") && !strings.EqualFold(rest, ".pcap"):
		return errors.New("want only a .pcap or .pcapng extension removed, not " + rest)
	}

	return nil
}
