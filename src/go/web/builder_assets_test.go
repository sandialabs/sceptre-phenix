package web

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
)

// The list the UI build writes. The worker has both copies, elk-api only gzip,
// the styles none, the view's chunk is missing, and the rest of the UI loads autosave.
const builderTestList = `[
  "assets/Builder-A.css",
  "assets/Builder-A.js",
  "assets/elk-api-A.js",
  "assets/elk-worker.min-B.js"
]`

const (
	builderTestWorker = "/assets/elk-worker.min-B.js"
	builderTestAPI    = "/assets/elk-api-A.js"
	builderTestStyles = "/assets/Builder-A.css"
)

// When the build wrote the files, which [http.Dir] gives as Last-Modified.
func builderTestModTime() time.Time {
	return time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
}

// The content of every test file. The compressed copies only need to differ
// from it: the server sends them as they are.
func builderTestScript() string {
	return strings.Repeat("postMessage({ layout: 'layered' });\n", 200)
}

func builderTestCopy(coding string) string {
	return coding + " copy of the script"
}

// builderTestAssets holds the list, unless it is "", and the build's files.
func builderTestAssets(list string) http.FileSystem {
	file := func(content string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(content), ModTime: builderTestModTime()}
	}

	script := file(builderTestScript())

	files := fstest.MapFS{
		"assets/elk-worker.min-B.js":    script,
		"assets/elk-worker.min-B.js.br": file(builderTestCopy("br")),
		"assets/elk-worker.min-B.js.gz": file(builderTestCopy("gzip")),
		"assets/elk-api-A.js":           script,
		"assets/elk-api-A.js.gz":        file(builderTestCopy("gzip")),
		"assets/Builder-A.css":          script,
		"assets/autosave-A.js":          script,
	}

	if list != "" {
		files[builderAssetList] = &fstest.MapFile{Data: []byte(list)}
	}

	return http.FS(files)
}

// builderTestHeader returns the values header holds of each key of want.
func builderTestHeader(header http.Header, want map[string]string) map[string]string {
	got := make(map[string]string, len(want))
	for key := range want {
		got[key] = header.Get(key)
	}

	return got
}

func TestBuilderFilesAreTheBuildsList(t *testing.T) {
	files, err := builderFiles(builderTestAssets(builderTestList))
	if err != nil {
		t.Fatalf("builderFiles(the build's list) error: %v", err)
	}

	want := []string{
		"assets/Builder-A.css",
		"assets/Builder-A.js",
		"assets/elk-api-A.js",
		"assets/elk-worker.min-B.js",
	}

	if !slices.Equal(files, want) {
		t.Errorf("builderFiles(the build's list) = %q, want %q", files, want)
	}
}

func TestBuilderFilesRefusesAMissingOrBadList(t *testing.T) {
	for name, list := range map[string]string{
		"no list":        "",
		"not JSON":       "[",
		"empty":          "[]",
		"null":           "null",
		"not an array":   `{"assets/Builder-A.js": ["br"]}`,
		"not file names": `[1, 2]`,
	} {
		t.Run(name, func(t *testing.T) {
			if files, err := builderFiles(builderTestAssets(list)); err == nil {
				t.Errorf("builderFiles(%q) = %q, want an error", list, files)
			}
		})
	}
}

func TestBuilderAssetHandler(t *testing.T) {
	const cached = builderAssetCacheControl

	// accept holds the request's Accept-Encoding lines. TestPreferredEncoding
	// holds the q-value cases.
	cases := []struct {
		name, list, path, accept string
		status                   int
		encoding, cache          string
	}{
		{"br", builderTestList, builderTestWorker, "br", http.StatusOK, "br", cached},
		{"gzip", builderTestList, builderTestWorker, "gzip", http.StatusOK, "gzip", cached},
		{"no Accept-Encoding", builderTestList, builderTestWorker, "", http.StatusOK, "", cached},
		{"br on a later line", builderTestList, builderTestWorker, "gzip;q=0.5\nbr", http.StatusOK, "br", cached},
		{"no Brotli copy", builderTestList, builderTestAPI, "br, gzip", http.StatusOK, "gzip", cached},
		{"no Brotli copy, br", builderTestList, builderTestAPI, "br", http.StatusOK, "", cached},
		{"no copies", builderTestList, builderTestStyles, "br, gzip", http.StatusOK, "", cached},
		{"shared with the rest", builderTestList, "/assets/autosave-A.js", "br, gzip", http.StatusOK, "", ""},
		{"missing", builderTestList, "/assets/Builder-A.js", "br, gzip", http.StatusNotFound, "", ""},
		{"Brotli copy itself", builderTestList, builderTestWorker + ".br", "br", http.StatusNotFound, "", ""},
		{"Brotli copy itself, no br", builderTestList, builderTestWorker + ".br", "", http.StatusNotFound, "", ""},
		{"gzip copy itself", builderTestList, builderTestWorker + ".gz", "gzip", http.StatusNotFound, "", ""},
		{"gzip copy, path not clean", builderTestList, "/assets/./elk-api-A.js.gz", "gzip", http.StatusNotFound, "", ""},
		{"no list", "", builderTestWorker, "br, gzip", http.StatusOK, "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handler := builderAssetHandler(builderTestAssets(c.list))

			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			if c.accept != "" {
				for line := range strings.SplitSeq(c.accept, "\n") {
					req.Header.Add("Accept-Encoding", line)
				}
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			res := rec.Result()

			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("reading the body of GET %s: %v", c.path, err)
			}

			res.Body.Close()

			if res.StatusCode != c.status {
				t.Fatalf("GET %s: status = %d, want %d", c.path, res.StatusCode, c.status)
			}

			// A Builder file, whatever its encoding, varies with Accept-Encoding.
			vary := ""
			if c.cache != "" {
				vary = "Accept-Encoding"
			}

			wantHeader := map[string]string{"Content-Encoding": c.encoding, "Cache-Control": c.cache, "Vary": vary}

			want := builderTestScript()
			if c.encoding != "" {
				want = builderTestCopy(c.encoding)
			}

			if c.status == http.StatusOK {
				wantHeader["Content-Length"] = strconv.Itoa(len(want))
			}

			if diff := cmp.Diff(wantHeader, builderTestHeader(res.Header, wantHeader)); diff != "" {
				t.Errorf("GET %s headers mismatch (-want +got):\n%s", c.path, diff)
			}

			if c.status != http.StatusOK {
				return
			}

			if diff := cmp.Diff(want, string(body)); diff != "" {
				t.Errorf("GET %s body mismatch, want the %q copy (-want +got):\n%s", c.path, c.encoding, diff)
			}

			// The type is the file's own, whatever its encoding.
			contentType := "text/javascript"
			if strings.HasSuffix(c.path, ".css") {
				contentType = "text/css"
			}

			if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, contentType) {
				t.Errorf("GET %s: Content-Type = %q, want %s", c.path, got, contentType)
			}
		})
	}
}

// TestBuilderAssetHandlerContentLength sets Content-Length for a whole
// compressed file only. Ranges and preconditions apply to the encoding sent.
func TestBuilderAssetHandlerContentLength(t *testing.T) {
	handler := builderAssetHandler(builderTestAssets(builderTestList))

	serve := func(t *testing.T, method string, header map[string]string) (*http.Response, []byte) {
		t.Helper()

		req := httptest.NewRequest(method, builderTestWorker, nil)
		for key, value := range header {
			req.Header.Set(key, value)
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		res := rec.Result()
		defer res.Body.Close()

		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("reading the body of %s %s: %v", method, builderTestWorker, err)
		}

		return res, raw
	}

	brotli := map[string]string{"Accept-Encoding": "br, gzip"}
	gzipped := map[string]string{"Accept-Encoding": "gzip"}

	with := func(key, value string) map[string]string {
		header := map[string]string{key: value}
		maps.Copy(header, brotli)

		return header
	}

	since := func(modTime time.Time) map[string]string {
		return with("If-Modified-Since", modTime.Format(http.TimeFormat))
	}

	var (
		brLength   = strconv.Itoa(len(builderTestCopy("br")))
		gzipLength = strconv.Itoa(len(builderTestCopy("gzip")))
		built      = builderTestModTime()
	)

	cases := []struct {
		name, method string
		header       map[string]string
		status       int
		encoding     string
		length       string
		body         string
	}{
		{"Brotli", http.MethodGet, brotli, http.StatusOK, "br", brLength, builderTestCopy("br")},
		{"gzipped", http.MethodGet, gzipped, http.StatusOK, "gzip", gzipLength, builderTestCopy("gzip")},
		{"HEAD", http.MethodHead, brotli, http.StatusOK, "br", brLength, ""},
		{"not compressed", http.MethodGet, nil, http.StatusOK, "", strconv.Itoa(len(builderTestScript())), builderTestScript()},
		{"range", http.MethodGet, with("Range", "bytes=0-9"), http.StatusPartialContent, "br", "10", builderTestCopy("br")[:10]},
		{"range past the end", http.MethodGet, with("Range", "bytes=100000-"), http.StatusRequestedRangeNotSatisfiable, "", "", ""},
		{"precondition failed", http.MethodGet, with("If-Match", `"other"`), http.StatusPreconditionFailed, "", "", ""},
		{"not modified", http.MethodGet, since(built), http.StatusNotModified, "", "", ""},
		{"modified", http.MethodGet, since(built.Add(-time.Hour)), http.StatusOK, "br", brLength, builderTestCopy("br")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, raw := serve(t, c.method, c.header)

			if res.StatusCode != c.status {
				t.Fatalf("%s %s: status = %d, want %d", c.method, builderTestWorker, res.StatusCode, c.status)
			}

			// A response without content has no Content-Encoding.
			wantHeader := map[string]string{"Content-Encoding": c.encoding, "Content-Length": c.length}

			if diff := cmp.Diff(wantHeader, builderTestHeader(res.Header, wantHeader)); diff != "" {
				t.Errorf("%s %s headers mismatch (-want +got):\n%s", c.method, builderTestWorker, diff)
			}

			if c.body == "" {
				return
			}

			if diff := cmp.Diff(c.body, string(raw)); diff != "" {
				t.Errorf("%s %s body mismatch (-want +got):\n%s", c.method, builderTestWorker, diff)
			}
		})
	}
}

func TestPreferredEncoding(t *testing.T) {
	both := []string{"br", "gzip"}

	cases := []struct {
		header  string
		offered []string
		want    string
	}{
		{"", both, ""},
		{"br", both, "br"},
		{"BR", both, "br"},
		{"gzip", both, "gzip"},
		{"GZIP", both, "gzip"},
		{"gzip, deflate, br, zstd", both, "br"},
		{"gzip, br", both, "br"},
		{"br;q=0.5, gzip", both, "gzip"},
		{"br;q=1.0, gzip;q=0.5", both, "br"},
		{"br; q=0.001, gzip; Q=0.0", both, "br"},
		{"br;q=0, gzip", both, "gzip"},
		{"br;q=0.0, gzip;q=0", both, ""},
		{"*", both, "br"},
		{"*;q=0", both, ""},
		{"br;q=0, *", both, "gzip"},
		{"gzip;q=0, *;q=0.5", both, "br"},
		{"deflate, zstd", both, ""},
		{"br;q=x, gzip", both, "gzip"},
		{"br;q=2, gzip", both, "gzip"},
		{"br;q=NaN", both, ""},
		{"br;level=1;q=0, gzip", both, "gzip"},
		{"br;q=0, br", both, ""},
		{" , br , ", both, "br"},
		{"br, gzip", []string{"gzip"}, "gzip"},
		{"br", []string{"gzip"}, ""},
		{"br, gzip", nil, ""},
	}

	for _, c := range cases {
		name := c.header + " offering " + strings.Join(c.offered, " and ")
		if c.header == "" {
			name = "no header offering " + strings.Join(c.offered, " and ")
		}

		t.Run(name, func(t *testing.T) {
			if got := preferredEncoding(c.header, c.offered); got != c.want {
				t.Errorf("preferredEncoding(%q, %q) = %q, want %q", c.header, c.offered, got, c.want)
			}
		})
	}
}
