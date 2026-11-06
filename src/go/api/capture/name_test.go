package capture

import (
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"phenix/util/mm"
)

// fileNameTests are the names fileName is given, and what it makes of them; FuzzFileName starts from them.
//
//nolint:gochecknoglobals // read-only test table
var fileNameTests = []struct {
	name, format, want string
}{
	{name: "capture", format: "pcapng", want: "capture.pcapng"},
	{name: "capture.pcap", format: "pcapng", want: "capture.pcapng"},
	{name: "capture.PCAPNG", format: "pcap", want: "capture.pcap"},
	{name: "capture.cap", format: "pcap", want: "capture.cap.pcap"},
	{name: "my capture #1", format: "pcap", want: "my_capture__1.pcap"},
	{name: "../../etc/passwd", format: "pcapng", want: "_.._etc_passwd.pcapng"},
	{name: "...hidden", format: "pcapng", want: "hidden.pcapng"},
	{name: ".pcapng", format: "pcapng", want: "capture.pcapng"},
	{name: "", format: "pcap", want: "capture.pcap"},
	{name: "dés-ért", format: "pcap", want: "d_s-_rt.pcap"},
	{name: strings.Repeat("a", 300), format: "pcap", want: strings.Repeat("a", maxStemLength) + ".pcap"},
}

func TestFileName(t *testing.T) {
	t.Parallel()

	for _, test := range fileNameTests {
		if got := fileName(test.name, test.format); got != test.want {
			t.Errorf("fileName(%q, %s) = %q, want %q", test.name, test.format, got, test.want)
		}
	}
}

// safeFileName is a name fileName may give: no path, no leading dot, only safe characters, and the format's
// extension.
var safeFileName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,199}\.(pcapng|pcap)$`)

// checkFileName returns what is wrong with fileName's answer got for format, or "".
func checkFileName(got, format string) string {
	switch {
	case !safeFileName.MatchString(got):
		return "not a safe file name"
	case !strings.HasSuffix(got, "."+format):
		return "not the format's extension"
	case mm.ValidateArg(got) != nil:
		return "not a minimega argument"
	}

	return ""
}

func TestFileNameProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.String().Draw(t, "name")
		format := rapid.SampledFrom([]string{mm.CaptureFormatPcapng, mm.CaptureFormatPcap}).Draw(t, "format")

		got := fileName(name, format)
		if problem := checkFileName(got, format); problem != "" {
			t.Fatalf("fileName(%q, %s) = %q: %s", name, format, got, problem)
		}

		// a safe name stays as it is, but for the extension
		if again := fileName(got, format); again != got {
			t.Fatalf("fileName(%q, %s) = %q, want %q unchanged", got, format, again, got)
		}
	})
}
