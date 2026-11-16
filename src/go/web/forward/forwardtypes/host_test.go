package forwardtypes_test

import (
	"errors"
	"strings"
	"testing"

	"phenix/util/mm"
	ft "phenix/web/forward/forwardtypes"
)

func TestCheckHost(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"127.0.0.1", "::1", "fd00::5", "node-1", "a.b.example"} {
		if err := ft.CheckHost(host); err != nil {
			t.Errorf("CheckHost(%q) = %v, want nil", host, err)
		}
	}

	for _, host := range []string{"", "$HOST", "${IP}", "a;b", "a b", "-a", "a..b", "file:x", strings.Repeat("a", 254)} {
		if err := ft.CheckHost(host); !errors.Is(err, ft.ErrBadHost) {
			t.Errorf("CheckHost(%q) = %v, want %v", host, err, ft.ErrBadHost)
		}
	}
}

// FuzzCheckHost checks that a host CheckHost takes reaches minimega unchanged
// as one argument of the tunnel command.
func FuzzCheckHost(f *testing.F) {
	for _, seed := range []string{"127.0.0.1", "::1", "node-1", "$HOST", "a;b", "file:x", "a$(b)", "fe80::1%eth0"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, host string) {
		if ft.CheckHost(host) != nil {
			return
		}

		if err := mm.ValidateArg(host); err != nil {
			t.Errorf("CheckHost(%q) = nil, but minimega would not take it as one argument: %v", host, err)
		}

		if err := mm.ValidateUnexpanded(host); err != nil {
			t.Errorf("CheckHost(%q) = nil, but minimega would change it: %v", host, err)
		}
	})
}
