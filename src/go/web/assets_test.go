package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testAssets() http.FileSystem {
	return http.FS(testPublic())
}

func TestStaticHandlerGzipsText(t *testing.T) {
	h := StaticHandler(testAssets(), true)
	want := string(testPublic()["assets/index-abc123.js"].Data)

	// twice: the second response comes from the compressed cache
	for range 2 {
		resp := get(t, h, "/assets/index-abc123.js", "gzip, deflate, br")

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}

		if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip", got)
		}

		if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "javascript") {
			t.Errorf("Content-Type = %q, want javascript", got)
		}

		if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
			t.Errorf("Vary = %q, want Accept-Encoding", got)
		}

		if got := resp.Header.Get("Cache-Control"); got != immutableCacheControl {
			t.Errorf("Cache-Control = %q, want %q", got, immutableCacheControl)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if len(body) >= len(want) {
			t.Errorf("compressed body is %d bytes, not smaller than %d", len(body), len(want))
		}

		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}

		plain, _ := io.ReadAll(zr)
		if string(plain) != want {
			t.Error("decompressed body does not match the file")
		}
	}
}

func TestStaticHandlerPlain(t *testing.T) {
	h := StaticHandler(testAssets(), false)

	cases := map[string]struct{ target, acceptEncoding string }{
		"no Accept-Encoding": {"/assets/index-abc123.js", ""},
		"gzip refused":       {"/assets/index-abc123.js", "gzip;q=0, br"},
		"binary file":        {"/assets/logo-abc123.png", "gzip"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp := get(t, h, c.target, c.acceptEncoding)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}

			if got := resp.Header.Get("Content-Encoding"); got != "" {
				t.Errorf("Content-Encoding = %q, want none", got)
			}

			body, _ := io.ReadAll(resp.Body)
			if want := testPublic()[strings.TrimPrefix(c.target, "/")].Data; !bytes.Equal(body, want) {
				t.Errorf("body is %d bytes, want the file's %d", len(body), len(want))
			}

			if got := resp.Header.Get("Cache-Control"); got != "" {
				t.Errorf("Cache-Control = %q, want none for non-hashed assets", got)
			}
		})
	}
}

func TestStaticHandlerETag(t *testing.T) {
	h := StaticHandler(testAssets(), false)

	cases := map[string]struct {
		target, acceptEncoding, suffix string
	}{
		"gzipped text": {"/assets/index-abc123.js", "gzip", `-gz"`},
		"plain text":   {"/assets/index-abc123.js", "", `"`},
		"binary file":  {"/assets/logo-abc123.png", "gzip", `"`},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp := get(t, h, c.target, c.acceptEncoding)
			resp.Body.Close()

			etag := resp.Header.Get("ETag")
			if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, c.suffix) ||
				(c.suffix == `"` && strings.HasSuffix(etag, `-gz"`)) {
				t.Fatalf("ETag = %q, want a quoted hash ending in %s", etag, c.suffix)
			}

			// embedded files have no modification time, so the ETag is the
			// only way a browser can revalidate them
			resp = getIfNoneMatch(t, h, c.target, c.acceptEncoding, etag)
			resp.Body.Close()

			if resp.StatusCode != http.StatusNotModified {
				t.Errorf("revalidation status = %d, want 304", resp.StatusCode)
			}
		})
	}
}

func TestStaticHandlerDirectoryIndex(t *testing.T) {
	h := StaticHandler(testAssets(), false)

	resp := get(t, h, "/docs/", "gzip")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", got)
	}

	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	if plain, _ := io.ReadAll(zr); !strings.Contains(string(plain), "phenix docs") {
		t.Error("directory URL did not serve its index.html")
	}

	resp = getIfNoneMatch(t, h, "/docs/", "gzip", resp.Header.Get("ETag"))
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want 304", resp.StatusCode)
	}

	// FileServer behavior is kept for a missing trailing slash and for a
	// directory without an index
	resp = get(t, h, "/docs", "gzip")
	resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("/docs status = %d, want 301 redirect", resp.StatusCode)
	}

	resp = get(t, h, "/novnc/app/", "gzip")
	listing, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK || !strings.Contains(string(listing), "readme.txt") {
		t.Errorf("/novnc/app/ = %d, want a directory listing", resp.StatusCode)
	}
}

// Only a served asset is marked immutable: a missing one may be deployed
// later, and a redirect is not the asset.
func TestStaticHandlerCachesOnlyServedAssets(t *testing.T) {
	h := StaticHandler(testAssets(), true)

	cases := map[string]struct {
		target string
		status int
	}{
		"missing":  {"/assets/nope.js", http.StatusNotFound},
		"redirect": {"/assets", http.StatusMovedPermanently},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp := get(t, h, c.target, "gzip")
			defer resp.Body.Close()

			if resp.StatusCode != c.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, c.status)
			}

			if got := resp.Header.Get("Cache-Control"); got != "" {
				t.Errorf("Cache-Control = %q, want none", got)
			}
		})
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                 false,
		"br":               false,
		"gzip":             true,
		"GZIP":             true,
		"deflate, gzip":    true,
		"gzip;q=0.5":       true,
		"gzip;q=0":         false,
		"gzip; q=0.000":    false,
		"x-gzip, identity": false,
	}

	for header, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", header)

		if got := acceptsGzip(req); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
