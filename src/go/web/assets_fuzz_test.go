package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FuzzAcceptsGzip checks acceptsGzip on any Accept-Encoding header against
// rules that decide it: only the first gzip entry counts, a later entry
// changes nothing, and a header that names no gzip is refused.
func FuzzAcceptsGzip(f *testing.F) {
	for _, seed := range []string{
		"", "br", "gzip", "GZIP", "deflate, gzip", "gzip;q=0.5", "gzip;q=0", "gzip; q=0.000",
		"x-gzip, identity", "gzip;q=abc", "gzip;level=1", " gzip ; Q=1 ", "gzip;q=0, gzip", ",,gzip",
	} {
		f.Add(seed)
	}

	accepts := func(header string) bool {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header["Accept-Encoding"] = []string{header}

		return acceptsGzip(req)
	}

	f.Fuzz(func(t *testing.T, header string) {
		got := accepts(header)

		if got && !strings.Contains(strings.ToLower(header), "gzip") {
			t.Fatalf("acceptsGzip(%q) = true, want false: it names no gzip", header)
		}

		if accepts("gzip;q=0,"+header) || !accepts("gzip,"+header) {
			t.Fatalf("acceptsGzip(gzip entry + %q) did not follow the first gzip entry", header)
		}

		if accepts(header+",br") != got || (got && !accepts(header+",gzip;q=0")) {
			t.Fatalf("acceptsGzip(%q + a later entry) changed from %t", header, got)
		}
	})
}
