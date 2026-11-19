package routing

import (
	"errors"
	"net/http"
	"testing"
)

// NewMux returns errBadPrefix for a prefix with a "{", "}", "%", space or tab, and errBadPath
// for a path with a space or a tab, with a method or of every method.
func TestNewMuxRefusesBadText(t *testing.T) {
	t.Parallel()

	ok := http.NotFoundHandler()

	group := func(prefix string, routes ...Route) Group {
		return Group{Prefix: prefix, Routes: routes, Middleware: nil, Options: nil, NotFound: nil, MethodNotAllowed: nil}
	}

	tests := map[string]struct {
		group Group
		want  error
	}{
		"a prefix with a variable":        {group("/api/{v}", Row(http.MethodGet, "/x", ok)), errBadPrefix},
		"a prefix with a {":               {group("/api{", Row(http.MethodGet, "/x", ok)), errBadPrefix},
		"a prefix with a }":               {group("/api}"), errBadPrefix},
		"a prefix with a space":           {group("/a b", Row(http.MethodGet, "/x", ok)), errBadPrefix},
		"a prefix with an escape":         {group("/a%2Fb", Row(http.MethodGet, "/x", ok)), errBadPrefix},
		"a path with a space":             {group("", Row(http.MethodGet, "/x y", ok)), errBadPath},
		"a path with a tab":               {group("", Row(http.MethodGet, "/x\ty", ok)), errBadPath},
		"a path of every method, a space": {group("", Row("", "/x y", ok)), errBadPath},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if m, err := NewMux(tt.group); !errors.Is(err, tt.want) || m != nil {
				t.Errorf("NewMux(%s) = %v, %v, want nil and %v", name, m, err, tt.want)
			}
		})
	}
}
