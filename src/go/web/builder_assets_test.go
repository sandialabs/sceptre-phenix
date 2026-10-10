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
)

// The list the UI build writes, and the files and compressed copies of a
// build: the ELK worker with both copies, elk-api with only a gzip copy (as
// when a build left the Brotli copy out), the view's styles with none, the
// view's chunk missing, and autosave, which the rest of the UI loads too.
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

func TestBuilderFilesAreTheBuildsList(t *testing.T) {
	files, err := builderFiles(builderTestAssets(builderTestList))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"assets/Builder-A.css",
		"assets/Builder-A.js",
		"assets/elk-api-A.js",
		"assets/elk-worker.min-B.js",
	}

	if !slices.Equal(files, want) {
		t.Errorf("builderFiles = %q, want %q", files, want)
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
		if files, err := builderFiles(builderTestAssets(list)); err == nil {
			t.Errorf("%s: builderFiles = %q, want an error", name, files)
		}
	}
}

func TestBuilderAssetHandler(t *testing.T) {
	const cached = builderAssetCacheControl

	// accept holds the request's Accept-Encoding lines, one per line of its own.
	cases := []struct {
		name, list, path, accept string
		status                   int
		encoding, cache          string
	}{
		{"a browser's", builderTestList, builderTestWorker, "gzip, deflate, br, zstd", http.StatusOK, "br", cached},
		{"br", builderTestList, builderTestWorker, "br", http.StatusOK, "br", cached},
		{"gzip", builderTestList, builderTestWorker, "gzip", http.StatusOK, "gzip", cached},
		{"br before gzip", builderTestList, builderTestWorker, "gzip, br", http.StatusOK, "br", cached},
		{"gzip weighed higher", builderTestList, builderTestWorker, "br;q=0.5, gzip", http.StatusOK, "gzip", cached},
		{"br weighed higher", builderTestList, builderTestWorker, "br;q=1.0, gzip;q=0.5", http.StatusOK, "br", cached},
		{"br refused", builderTestList, builderTestWorker, "br;q=0, gzip", http.StatusOK, "gzip", cached},
		{"both refused", builderTestList, builderTestWorker, "br;q=0, gzip;q=0", http.StatusOK, "", cached},
		{"any coding", builderTestList, builderTestWorker, "*", http.StatusOK, "br", cached},
		{"any but br", builderTestList, builderTestWorker, "br;q=0, *", http.StatusOK, "gzip", cached},
		{"no coding", builderTestList, builderTestWorker, "*;q=0", http.StatusOK, "", cached},
		{"identity", builderTestList, builderTestWorker, "identity", http.StatusOK, "", cached},
		{"others", builderTestList, builderTestWorker, "deflate, zstd", http.StatusOK, "", cached},
		{"no Accept-Encoding", builderTestList, builderTestWorker, "", http.StatusOK, "", cached},
		{"br refused on a later line", builderTestList, builderTestWorker, "*\nbr;q=0", http.StatusOK, "gzip", cached},
		{"br on a later line", builderTestList, builderTestWorker, "gzip;q=0.5\nbr", http.StatusOK, "br", cached},
		{"both refused on later lines", builderTestList, builderTestWorker, "*\nbr;q=0\ngzip;q=0", http.StatusOK, "", cached},
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
			t.Fatal(err)
		}

		res.Body.Close()

		if res.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d", c.name, res.StatusCode, c.status)

			continue
		}

		if got := res.Header.Get("Content-Encoding"); got != c.encoding {
			t.Errorf("%s: Content-Encoding %q, want %q", c.name, got, c.encoding)
		}

		if got := res.Header.Get("Cache-Control"); got != c.cache {
			t.Errorf("%s: Cache-Control %q, want %q", c.name, got, c.cache)
		}

		// A Builder file, whatever its encoding, varies with Accept-Encoding.
		vary := ""
		if c.cache != "" {
			vary = "Accept-Encoding"
		}

		if got := res.Header.Get("Vary"); got != vary {
			t.Errorf("%s: Vary %q, want %q", c.name, got, vary)
		}

		if c.status != http.StatusOK {
			continue
		}

		want := builderTestScript()
		if c.encoding != "" {
			want = builderTestCopy(c.encoding)
		}

		if string(body) != want {
			t.Errorf("%s: a body of %d bytes, want the %d of the %q copy", c.name, len(body), len(want), c.encoding)
		}

		if got := res.Header.Get("Content-Length"); got != strconv.Itoa(len(want)) {
			t.Errorf("%s: Content-Length %q, want %d", c.name, got, len(want))
		}

		// The type is the file's own, whatever its encoding.
		contentType := "text/javascript"
		if strings.HasSuffix(c.path, ".css") {
			contentType = "text/css"
		}

		if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, contentType) {
			t.Errorf("%s: Content-Type %q, want %s", c.name, got, contentType)
		}
	}
}

// [http.ServeContent] leaves Content-Length out once Content-Encoding is set,
// so the handler sets it for a whole compressed file, and only then. Ranges
// and preconditions apply to the encoding sent.
func TestBuilderAssetHandlerContentLength(t *testing.T) {
	handler := builderAssetHandler(builderTestAssets(builderTestList))

	serve := func(method string, header map[string]string) (*http.Response, []byte) {
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
			t.Fatal(err)
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
		res, raw := serve(c.method, c.header)

		if res.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d", c.name, res.StatusCode, c.status)

			continue
		}

		// A response without content has no Content-Encoding.
		if got := res.Header.Get("Content-Encoding"); got != c.encoding {
			t.Errorf("%s: Content-Encoding %q, want %q", c.name, got, c.encoding)
		}

		if got := res.Header.Get("Content-Length"); got != c.length {
			t.Errorf("%s: Content-Length %q, want %q", c.name, got, c.length)
		}

		if c.body != "" && string(raw) != c.body {
			t.Errorf("%s: body %q, want %q", c.name, raw, c.body)
		}
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
		{"identity", both, ""},
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
		if got := preferredEncoding(c.header, c.offered); got != c.want {
			t.Errorf("preferredEncoding(%q, %q) = %q, want %q", c.header, c.offered, got, c.want)
		}
	}
}
