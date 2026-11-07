package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

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

	resp = get(t, h, "/docs/images/", "gzip")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /docs/images/ = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if listing := readBody(t, resp); !strings.Contains(listing, "readme.txt") {
		t.Errorf("GET /docs/images/ = %q, want a directory listing with readme.txt", listing)
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
	t.Parallel()

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
		"gzip;q=abc":       false,
		"gzip;level=1":     true,
		" gzip ; Q=1 ":     true,
		"gzip;q=0, gzip":   false,
		",,gzip":           true,
	}

	for header, want := range cases {
		t.Run(header, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", header)

			if got := acceptsGzip(req); got != want {
				t.Errorf("acceptsGzip(Accept-Encoding %q) = %v, want %v", header, got, want)
			}
		})
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

// assetsTestFS is a file system whose files fail to stat or read when their
// names are in statErr or readErr.
type assetsTestFS struct {
	http.FileSystem

	statErr, readErr map[string]bool
}

func (a assetsTestFS) Open(name string) (http.File, error) {
	f, err := a.FileSystem.Open(name)
	if err != nil {
		return nil, err //nolint:wrapcheck // test double
	}

	return assetsTestFile{File: f, statErr: a.statErr[name], readErr: a.readErr[name]}, nil
}

type assetsTestFile struct {
	http.File

	statErr, readErr bool
}

func (f assetsTestFile) Stat() (fs.FileInfo, error) {
	if f.statErr {
		return nil, errors.New("stat failed")
	}

	return f.File.Stat() //nolint:wrapcheck // test double
}

func (f assetsTestFile) Read(p []byte) (int, error) {
	if f.readErr {
		return 0, errors.New("read failed")
	}

	return f.File.Read(p) //nolint:wrapcheck // test double
}

// assetsTestErrReader is a reader that always fails.
type assetsTestErrReader struct{}

func (assetsTestErrReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// assetsTestGunzip decompresses gz, failing the test if it is not gzip.
func assetsTestGunzip(t *testing.T, gz []byte) []byte {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}

	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("decompressing: %v", err)
	}

	return plain
}

// The embedded file system is rooted at public/, and holds the API docs.
func TestGetAssets(t *testing.T) {
	t.Parallel()

	assets, err := GetAssets()
	if err != nil {
		t.Fatalf("GetAssets() error = %v", err)
	}

	f, err := assets.Open("/docs/openapi.yml")
	if err != nil {
		t.Fatalf("GetAssets().Open(/docs/openapi.yml) error = %v", err)
	}
	defer f.Close()

	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("reading /docs/openapi.yml: %v", err)
	}

	if !bytes.Contains(body, []byte("openapi:")) {
		t.Errorf("/docs/openapi.yml = %.40q..., want an OpenAPI document", body)
	}

	if _, err := assets.Open("/public/docs/openapi.yml"); err == nil {
		t.Error("GetAssets().Open(/public/docs/openapi.yml) error = nil, want the file system rooted at public/")
	}
}

// A memAsset's ETag is the first half of the content's SHA-256 in hex, and
// its compressed copy, made only when asked for, decompresses to the content.
func TestNewMemAsset(t *testing.T) {
	t.Parallel()

	mod := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

	for name, raw := range map[string][]byte{
		"nil":   nil,
		"empty": {},
		"text":  []byte(strings.Repeat("phenix ", 100)),
		"bytes": {0, 1, 2, 0xff},
	} {
		for _, compress := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s compress=%t", name, compress), func(t *testing.T) {
				t.Parallel()

				asset, err := newMemAsset(raw, mod, compress)
				if err != nil {
					t.Fatalf("newMemAsset() error = %v", err)
				}

				sum := sha256.Sum256(raw)
				if want := hex.EncodeToString(sum[:16]); asset.etag != want {
					t.Errorf("newMemAsset() etag = %q, want %q", asset.etag, want)
				}

				if !asset.mod.Equal(mod) || !bytes.Equal(asset.raw, raw) {
					t.Errorf("newMemAsset() mod, raw = %v, %q, want %v, %q", asset.mod, asset.raw, mod, raw)
				}

				if !compress {
					if asset.gz != nil {
						t.Errorf("newMemAsset(compress=false) gz = %d bytes, want nil", len(asset.gz))
					}

					return
				}

				if got := assetsTestGunzip(t, asset.gz); !bytes.Equal(got, raw) {
					t.Errorf("newMemAsset() gz decompresses to %q, want %q", got, raw)
				}
			})
		}
	}
}

// serve sends the compressed bytes to a client that accepts gzip, else the
// raw ones, and sends nothing when it holds no raw bytes for the client.
func TestMemAssetServe(t *testing.T) {
	t.Parallel()

	raw := []byte(strings.Repeat("phenix ", 100))

	asset, err := newMemAsset(raw, time.Time{}, true)
	if err != nil {
		t.Fatalf("newMemAsset() error = %v", err)
	}

	noRaw := *asset
	noRaw.raw = nil

	plain, err := newMemAsset(raw, time.Time{}, false)
	if err != nil {
		t.Fatalf("newMemAsset() error = %v", err)
	}

	tests := map[string]struct {
		asset          *memAsset
		acceptEncoding string
		served         bool
		encoding, etag string
		body           []byte
	}{
		"gzip": {
			asset:          asset,
			acceptEncoding: "gzip",
			served:         true,
			encoding:       "gzip",
			etag:           `"` + asset.etag + `-gz"`,
			body:           asset.gz,
		},
		"raw":                   {asset: asset, served: true, etag: `"` + asset.etag + `"`, body: raw},
		"uncompressed for gzip": {asset: plain, acceptEncoding: "gzip", served: true, etag: `"` + plain.etag + `"`, body: raw},
		"no raw, gzip": {
			asset:          &noRaw,
			acceptEncoding: "gzip",
			served:         true,
			encoding:       "gzip",
			etag:           `"` + asset.etag + `-gz"`,
			body:           asset.gz,
		},
		"no raw, plain": {asset: &noRaw, served: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/a.txt", nil)
			if tc.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			}

			w := httptest.NewRecorder()

			if got := tc.asset.serve(w, req, "a.txt"); got != tc.served {
				t.Fatalf("serve() = %t, want %t", got, tc.served)
			}

			if !tc.served {
				if w.Body.Len() != 0 || len(w.Header()) != 0 {
					t.Errorf("serve() = false, but wrote headers %v and %d bytes", w.Header(), w.Body.Len())
				}

				return
			}

			got := map[string]string{
				"Content-Encoding": w.Header().Get("Content-Encoding"),
				"ETag":             w.Header().Get("ETag"),
				"Content-Type":     w.Header().Get("Content-Type"),
			}
			want := map[string]string{"Content-Encoding": tc.encoding, "ETag": tc.etag, "Content-Type": "text/plain; charset=utf-8"}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("serve() headers mismatch (-want +got):\n%s", diff)
			}

			if !bytes.Equal(w.Body.Bytes(), tc.body) {
				t.Errorf("serve() body = %d bytes, want %d", w.Body.Len(), len(tc.body))
			}
		})
	}
}

// loadAsset keeps the ETag and compressed bytes, but not the raw ones, and
// fails when the file cannot be read.
func TestLoadAsset(t *testing.T) {
	t.Parallel()

	raw := []byte("console.log('phenix');")

	for _, compressible := range []bool{false, true} {
		t.Run(fmt.Sprintf("compressible=%t", compressible), func(t *testing.T) {
			t.Parallel()

			asset, err := loadAsset(bytes.NewReader(raw), time.Time{}, compressible)
			if err != nil {
				t.Fatalf("loadAsset() error = %v", err)
			}

			want, _ := newMemAsset(raw, time.Time{}, compressible)

			if asset.raw != nil || asset.etag != want.etag || (asset.gz != nil) != compressible {
				t.Errorf("loadAsset() = raw %q, etag %q, gz %d bytes; want no raw, etag %q, gz %t",
					asset.raw, asset.etag, len(asset.gz), want.etag, compressible)
			}
		})
	}

	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()

		if asset, err := loadAsset(assetsTestErrReader{}, time.Time{}, true); err == nil {
			t.Errorf("loadAsset(unreadable) = %+v, want an error", asset)
		}
	})
}

// A file the handler cannot stat or read is left to the file server, and is
// never marked immutable.
func TestStaticHandlerFileErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		target           string
		statErr, readErr string
		wantStatus       int
		wantBody         string
	}{
		// the file server stats the file again and fails too
		"stat fails": {
			target: "/assets/index-abc123.js", statErr: "/assets/index-abc123.js",
			wantStatus: http.StatusInternalServerError, wantBody: "500 Internal Server Error",
		},
		// the file server reads the file again: headers go out, the body is lost
		"read fails": {
			target: "/assets/index-abc123.js", readErr: "/assets/index-abc123.js",
			wantStatus: http.StatusOK, wantBody: "",
		},
		"index stat fails": {
			target: "/docs/", statErr: "/docs/index.html",
			wantStatus: http.StatusOK, wantBody: `<a href="index.html">index.html</a>`,
		},
		"index missing": {
			target:     "/docs/images/",
			wantStatus: http.StatusOK, wantBody: `<a href="readme.txt">readme.txt</a>`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := StaticHandler(assetsTestFS{
				FileSystem: testAssets(),
				statErr:    map[string]bool{tc.statErr: true},
				readErr:    map[string]bool{tc.readErr: true},
			}, true)

			resp := get(t, h, tc.target, "gzip")
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("GET %s = %d, want %d", tc.target, resp.StatusCode, tc.wantStatus)
			}

			if got := resp.Header.Get("Cache-Control"); got != "" {
				t.Errorf("GET %s Cache-Control = %q, want none", tc.target, got)
			}

			if got := resp.Header.Get("Content-Encoding"); got != "" {
				t.Errorf("GET %s Content-Encoding = %q, want none from the file server", tc.target, got)
			}

			body := readBody(t, resp)

			ok := strings.Contains(body, tc.wantBody)
			if tc.wantBody == "" {
				ok = body == ""
			}

			if !ok {
				t.Errorf("GET %s body = %q, want %q", tc.target, body, tc.wantBody)
			}
		})
	}
}

// gzipBytes compresses any bytes to gzip that decompresses to them.
func TestGzipBytesRoundTrip(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		raw := rapid.SliceOfN(rapid.Byte(), 0, 4096).Draw(rt, "raw")

		gz, err := gzipBytes(raw)
		if err != nil {
			rt.Fatalf("gzipBytes() error = %v", err)
		}

		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			rt.Fatalf("gzip.NewReader() error = %v", err)
		}

		got, err := io.ReadAll(zr)
		if err != nil || !bytes.Equal(got, raw) {
			rt.Fatalf("gzipBytes(%x) decompresses to %x (error %v)", raw, got, err)
		}
	})
}
