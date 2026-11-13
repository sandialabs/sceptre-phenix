package mm

import (
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// FuzzParseMinimegaVersion checks the version parser against semver: a version it gives is valid and is the second
// field of the answer, and the support is on exactly for a version from MinCaptureVersion's pre-releases on.
func FuzzParseMinimegaVersion(f *testing.F) {
	for _, tc := range versionCases {
		f.Add(tc.answer)
	}

	f.Fuzz(func(t *testing.T, answer string) {
		v, ok := parseMinimegaVersion(answer)
		fields := strings.Fields(answer)

		if ok != (len(fields) > 1 && semver.IsValid("v"+strings.TrimPrefix(fields[1], "v"))) {
			t.Fatalf("parseMinimegaVersion(%q) = %q, %t; want ok only for a semantic version in the second field",
				answer, v, ok)
		}

		s := CaptureSupportOf(answer, false)

		if want := ok && semver.Compare(v, MinCaptureVersion+"-0") >= 0; s.Available != want {
			t.Errorf("CaptureSupportOf(%q).Available = %t, want %t", answer, s.Available, want)
		}

		if s.Available == (s.Reason != "") {
			t.Errorf("CaptureSupportOf(%q) = %+v, want a reason exactly when it is off", answer, s)
		}

		if ok && "v"+s.Version != v {
			t.Errorf("CaptureSupportOf(%q).Version = %q, want %q", answer, s.Version, strings.TrimPrefix(v, "v"))
		}
	})
}
