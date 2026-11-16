package mm

import (
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// captureFilterTests are filters and whether ValidateCaptureFilter takes them; FuzzValidateCaptureFilter starts
// from them.
//
//nolint:gochecknoglobals // read-only test table
var captureFilterTests = []struct {
	filter string
	ok     bool
}{
	{filter: "", ok: true},
	{filter: "tcp port 80 and (host 10.0.0.1 or net 10.1.0.0/16)", ok: true},
	{filter: "vlan 101 && ether[12:2] != 0x0800, ip6 or !arp > <= * - +", ok: true},
	{filter: strings.Repeat("a", maxFilterLength), ok: true},
	{filter: strings.Repeat("a", maxFilterLength+1), ok: false},
	{filter: `host "a"`, ok: false},
	{filter: `host \a`, ok: false},
	{filter: "tcp # comment", ok: false},
	{filter: "tcp\nudp", ok: false},
	{filter: "tcp; shell", ok: false},
	{filter: "ip and é", ok: false},
}

// filterOracle reports whether f is a filter phenix gives minimega, from its characters one by one.
func filterOracle(f string) bool {
	if len(f) > maxFilterLength {
		return false
	}

	for _, r := range f {
		ok := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune(" .:/[]()&|!<>=+-*,", r)
		if !ok {
			return false
		}
	}

	return true
}

func TestValidateCaptureFilter(t *testing.T) {
	t.Parallel()

	for _, test := range captureFilterTests {
		err := ValidateCaptureFilter(test.filter)

		if test.ok && err != nil {
			t.Errorf("ValidateCaptureFilter(%q) error = %v, want nil", test.filter, err)
		}

		if !test.ok && !errors.Is(err, ErrUnsafeArg) {
			t.Errorf("ValidateCaptureFilter(%q) error = %v, want %v", test.filter, err, ErrUnsafeArg)
		}
	}
}

func TestValidateCaptureFilterProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		f := rapid.OneOf(
			rapid.String(),
			rapid.StringMatching(`[A-Za-z0-9 .:/\[\]()&|!<>=+\-*,"#\\]{0,40}`),
		).Draw(t, "filter")

		if got, want := ValidateCaptureFilter(f) == nil, filterOracle(f); got != want {
			t.Fatalf("ValidateCaptureFilter(%q) accepts = %t, want %t", f, got, want)
		}
	})
}
