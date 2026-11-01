package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

func testAssets() http.FileSystem {
	return http.FS(testPublic())
}

func TestStaticHandlerGzipsText(t *testing.T) {
	const target = "/assets/index-abc123.js"

	h := StaticHandler(testAssets(), true)
	want := string(testPublic()["assets/index-abc123.js"].Data)

	// twice: the second response comes from the compressed cache
	for _, name := range []string{"first", "cached"} {
		t.Run(name, func(t *testing.T) {
			resp := get(t, h, target, "gzip, deflate, br")
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s = %d, want %d", target, resp.StatusCode, http.StatusOK)
			}

			if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
				t.Fatalf("GET %s Content-Encoding = %q, want gzip", target, got)
			}

			if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "javascript") {
				t.Errorf("GET %s Content-Type = %q, want javascript", target, got)
			}

			if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
				t.Errorf("GET %s Vary = %q, want Accept-Encoding", target, got)
			}

			if got := resp.Header.Get("Cache-Control"); got != immutableCacheControl {
				t.Errorf("GET %s Cache-Control = %q, want %q", target, got, immutableCacheControl)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading GET %s: %v", target, err)
			}

			if len(body) >= len(want) {
				t.Errorf("GET %s body = %d bytes, want fewer than the file's %d", target, len(body), len(want))
			}

			zr, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatalf("GET %s body is not gzip: %v", target, err)
			}

			plain, err := io.ReadAll(zr)
			if err != nil {
				t.Fatalf("decompressing GET %s: %v", target, err)
			}

			if diff := cmp.Diff(want, string(plain)); diff != "" {
				t.Errorf("GET %s decompressed body mismatch (-want +got):\n%s", target, diff)
			}
		})
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
				t.Errorf("GET %s with Accept-Encoding %q = %d, want %d", c.target, c.acceptEncoding, resp.StatusCode, http.StatusOK)
			}

			if got := resp.Header.Get("Content-Encoding"); got != "" {
				t.Errorf("GET %s with Accept-Encoding %q: Content-Encoding = %q, want none", c.target, c.acceptEncoding, got)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading GET %s: %v", c.target, err)
			}

			want := testPublic()[strings.TrimPrefix(c.target, "/")].Data
			if diff := cmp.Diff(want, body); diff != "" {
				t.Errorf("GET %s with Accept-Encoding %q body mismatch (-want +got):\n%s", c.target, c.acceptEncoding, diff)
			}

			if got := resp.Header.Get("Cache-Control"); got != "" {
				t.Errorf("GET %s from an unhashed handler: Cache-Control = %q, want none", c.target, got)
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
				t.Fatalf("GET %s with Accept-Encoding %q: ETag = %q, want a quoted hash ending in %s",
					c.target, c.acceptEncoding, etag, c.suffix)
			}

			// embedded files have no modification time, so the ETag is the
			// only way a browser can revalidate them
			resp = getIfNoneMatch(t, h, c.target, c.acceptEncoding, etag)
			resp.Body.Close()

			if resp.StatusCode != http.StatusNotModified {
				t.Errorf("GET %s If-None-Match %s = %d, want %d", c.target, etag, resp.StatusCode, http.StatusNotModified)
			}
		})
	}
}

func TestStaticHandlerDirectoryIndex(t *testing.T) {
	h := StaticHandler(testAssets(), false)

	resp := get(t, h, "/docs/", "gzip")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /docs/ = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("GET /docs/ Content-Type = %q, want text/html", got)
	}

	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("GET /docs/ Content-Encoding = %q, want gzip", got)
	} else if body := readBody(t, resp); !strings.Contains(body, "phenix docs") {
		t.Errorf("GET /docs/ = %q, want docs/index.html", body)
	}

	resp = getIfNoneMatch(t, h, "/docs/", "gzip", resp.Header.Get("ETag"))
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("GET /docs/ If-None-Match = %d, want %d", resp.StatusCode, http.StatusNotModified)
	}

	// FileServer behavior is kept for a missing trailing slash and for a
	// directory without an index
	resp = get(t, h, "/docs", "gzip")
	resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("GET /docs = %d, want %d", resp.StatusCode, http.StatusMovedPermanently)
	}

	resp = get(t, h, "/novnc/app/", "gzip")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /novnc/app/ = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if listing := readBody(t, resp); !strings.Contains(listing, "readme.txt") {
		t.Errorf("GET /novnc/app/ = %q, want a directory listing with readme.txt", listing)
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
				t.Errorf("GET %s = %d, want %d", c.target, resp.StatusCode, c.status)
			}

			if got := resp.Header.Get("Cache-Control"); got != "" {
				t.Errorf("GET %s Cache-Control = %q, want none", c.target, got)
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
			t.Errorf("acceptsGzip(Accept-Encoding %q) = %v, want %v", header, got, want)
		}
	}
}

// genAssetsCoding draws one Accept-Encoding entry for coding, with optional
// whitespace and a q weight; zero reports whether the weight refuses it.
func genAssetsCoding(coding string) *rapid.Generator[assetsCoding] {
	return rapid.Custom(func(t *rapid.T) assetsCoding {
		ows := rapid.SampledFrom([]string{"", " ", "\t"})
		entry := ows.Draw(t, "before") + coding + ows.Draw(t, "after")

		zeros := []string{"0", "0.", "0.0", "0.000"}
		positives := []string{"1", "1.0", "0.001", "0.5"}
		q := rapid.SampledFrom(append(append([]string{""}, zeros...), positives...)).Draw(t, "q")

		if q != "" {
			entry += ";" + ows.Draw(t, "semicolon") + rapid.SampledFrom([]string{"q", "Q"}).Draw(t, "qname") + "=" + q
		}

		return assetsCoding{entry: entry, zero: slices.Contains(zeros, q)}
	})
}

type assetsCoding struct {
	entry string
	zero  bool
}

// Other codings, case and optional whitespace leave the verdict to the gzip
// entry alone: accepted unless its weight is zero.
func TestAcceptsGzipProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		other := rapid.SampledFrom([]string{"br", "deflate", "identity", "x-gzip", "*", "gzip2"})
		entries := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) assetsCoding {
			return genAssetsCoding(other.Draw(t, "coding")).Draw(t, "other")
		}), 0, 4).Draw(t, "others")

		want := false

		if rapid.Bool().Draw(t, "hasGzip") {
			gz := genAssetsCoding(rapid.SampledFrom([]string{"gzip", "GZIP", "Gzip"}).Draw(t, "gzip")).Draw(t, "gzipEntry")
			at := rapid.IntRange(0, len(entries)).Draw(t, "at")
			entries = slices.Insert(entries, at, gz)
			want = !gz.zero
		}

		parts := make([]string, 0, len(entries))
		for _, e := range entries {
			parts = append(parts, e.entry)
		}

		header := strings.Join(parts, ",")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", header)

		if got := acceptsGzip(req); got != want {
			t.Fatalf("acceptsGzip(Accept-Encoding %q) = %v, want %v", header, got, want)
		}
	})
}
