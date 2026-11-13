package util

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// servedFromStart serves size bytes with the Range header through [http.ServeContent] and reports whether it
// answered 206 and whether any range it sent, alone or as a multipart part, starts at the first byte.
func servedFromStart(t *testing.T, header string, size int64) (bool, bool) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	req.Header.Set("Range", header)

	rec := httptest.NewRecorder()
	http.ServeContent(rec, req, "", time.Time{}, strings.NewReader(strings.Repeat("x", int(size))))

	if rec.Code != http.StatusPartialContent {
		return false, false
	}

	if cr := rec.Header().Get("Content-Range"); cr != "" {
		return true, strings.HasPrefix(cr, "bytes 0-")
	}

	_, params, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
	if err != nil {
		t.Fatalf("ServeContent(Range %q) Content-Type %q: %v", header, rec.Header().Get("Content-Type"), err)
	}

	parts := multipart.NewReader(rec.Body, params["boundary"])

	for {
		part, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			return true, false
		}

		if err != nil {
			t.Fatalf("ServeContent(Range %q) multipart body: %v", header, err)
		}

		if strings.HasPrefix(part.Header.Get("Content-Range"), "bytes 0-") {
			return true, true
		}
	}
}

// For every Range header net/http answers with 206, rangeFromStart agrees
// with the ranges it sent on whether one starts at the first byte.
func FuzzRangeFromStart(f *testing.F) {
	for _, seed := range []string{
		"bytes=0-9", "bytes=10-19", "bytes=90-", "bytes=999999-", "bytes=0-9,10-", "bytes=50-59, 0-9",
		"bytes=-100,0-0", "bytes=10-19,30-39", "bytes=-10,20-29", "items=0-9,10-19", "bytes=-0", "bytes=+0-",
		"bytes= 0 - 5", "bytes=0-\t", "bytes=,0-", "bytes=5-1",
	} {
		f.Add(seed, uint8(100))
	}

	f.Add("bytes=-5", uint8(0))
	f.Add("bytes=0-", uint8(1))

	f.Fuzz(func(t *testing.T, header string, size uint8) {
		partial, want := servedFromStart(t, header, int64(size))
		if !partial {
			return
		}

		if got := rangeFromStart(header, int64(size)); got != want {
			t.Fatalf("rangeFromStart(%q, %d) = %t, want %t as net/http served it", header, size, got, want)
		}
	})
}
