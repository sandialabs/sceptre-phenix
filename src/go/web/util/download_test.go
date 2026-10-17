package util

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// downloadFile writes contents to a new file in a temporary directory and
// returns its path.
func downloadFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "capture.pcap")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// serveDownload sends a request for d with the given headers (name, value
// pairs) and returns the response and what ServeDownload reported.
func serveDownload(t *testing.T, method string, d Download, header ...string) (*http.Response, Served) {
	t.Helper()

	req := httptest.NewRequest(method, "/download", nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}

	rec := httptest.NewRecorder()

	served, err := ServeDownload(rec, req, d)
	if err != nil {
		t.Fatalf("ServeDownload(%s %q) = %v, want nil", method, d.Path, err)
	}

	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	return resp, served
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestServeDownload(t *testing.T) {
	t.Parallel()

	contents := strings.Repeat("0123456789", 10)
	path := downloadFile(t, contents)
	d := Download{Path: path, Name: "out.pcap"}

	full, _ := serveDownload(t, http.MethodGet, d)
	etag := full.Header.Get("ETag")
	lastModified := full.Header.Get("Last-Modified")

	tests := map[string]struct {
		method      string
		header      []string
		wantStatus  int
		wantBody    string
		wantRange   string
		wantStarted bool
	}{
		"whole file": {
			method: http.MethodGet, wantStatus: http.StatusOK, wantBody: contents, wantStarted: true,
		},
		"head": {method: http.MethodHead, wantStatus: http.StatusOK},
		"first range": {
			method: http.MethodGet, header: []string{"Range", "bytes=0-9"},
			wantStatus: http.StatusPartialContent, wantBody: contents[:10], wantRange: "bytes 0-9/100", wantStarted: true,
		},
		"resumed range": {
			method: http.MethodGet, header: []string{"Range", "bytes=10-19"},
			wantStatus: http.StatusPartialContent, wantBody: contents[10:20], wantRange: "bytes 10-19/100",
		},
		"if-range matches": {
			method: http.MethodGet, header: []string{"Range", "bytes=90-", "If-Range", etag},
			wantStatus: http.StatusPartialContent, wantBody: contents[90:], wantRange: "bytes 90-99/100",
		},
		"if-range is stale": {
			method: http.MethodGet, header: []string{"Range", "bytes=90-", "If-Range", `"0-0-0"`},
			wantStatus: http.StatusOK, wantBody: contents, wantStarted: true,
		},
		// net/http compares If-Range strongly, so a weak ETag never matches
		"if-range is weak": {
			method: http.MethodGet, header: []string{"Range", "bytes=90-", "If-Range", "W/" + etag},
			wantStatus: http.StatusOK, wantBody: contents, wantStarted: true,
		},
		"if-range date": {
			method: http.MethodGet, header: []string{"Range", "bytes=90-", "If-Range", lastModified},
			wantStatus: http.StatusPartialContent, wantBody: contents[90:], wantRange: "bytes 90-99/100",
		},
		"if-none-match": {
			method: http.MethodGet, header: []string{"If-None-Match", etag}, wantStatus: http.StatusNotModified,
		},
		"if-match mismatch": {
			method: http.MethodGet, header: []string{"If-Match", `"0-0-0"`}, wantStatus: http.StatusPreconditionFailed,
		},
		"unsatisfiable range": {
			method: http.MethodGet, header: []string{"Range", "bytes=999999-"},
			wantStatus: http.StatusRequestedRangeNotSatisfiable, wantRange: "bytes */100",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resp, served := serveDownload(t, tc.method, d, tc.header...)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("ServeDownload(%s, header %q) status = %d, want %d", tc.method, tc.header, resp.StatusCode, tc.wantStatus)
			}

			if served.Started != tc.wantStarted {
				t.Errorf("ServeDownload(%s, header %q) Started = %v, want %v", tc.method, tc.header, served.Started, tc.wantStarted)
			}

			if got := resp.Header.Get("Content-Range"); got != tc.wantRange {
				t.Errorf("ServeDownload(%s, header %q) Content-Range = %q, want %q", tc.method, tc.header, got, tc.wantRange)
			}

			if tc.wantStatus == http.StatusRequestedRangeNotSatisfiable {
				return
			}

			if got := resp.Header.Get("ETag"); got != etag {
				t.Errorf("ServeDownload(%s, header %q) ETag = %q, want %q", tc.method, tc.header, got, etag)
			}

			if got := body(t, resp); got != tc.wantBody {
				t.Errorf("ServeDownload(%s, header %q) body = %q, want %q", tc.method, tc.header, got, tc.wantBody)
			}
		})
	}
}

// A multipart response has no Content-Range, so started is called directly
// (ServeContent's multi-range support varies by Go release).
func TestStartedMultipart(t *testing.T) {
	t.Parallel()

	multipart := http.Header{"Content-Type": {"multipart/byteranges; boundary=x"}}

	tests := map[string]struct {
		ranges      string
		wantStarted bool
	}{
		"whole file":        {ranges: "bytes=0-9,10-", wantStarted: true},
		"first byte later":  {ranges: "bytes=50-59, 0-9", wantStarted: true},
		"suffix whole file": {ranges: "bytes=-100,0-0", wantStarted: true},
		"resumed":           {ranges: "bytes=10-19,30-39"},
		"short suffix":      {ranges: "bytes=-10,20-29"},
		"not bytes":         {ranges: "items=0-9,10-19"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/download", nil)
			req.Header.Set("Range", tc.ranges)

			if got := started(req, http.StatusPartialContent, multipart, 100); got != tc.wantStarted {
				t.Errorf("started(Range %q, 206 multipart, 100) = %v, want %v", tc.ranges, got, tc.wantStarted)
			}
		})
	}
}

func TestServeDownloadHeaders(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "hello")

	resp, _ := serveDownload(t, http.MethodGet, Download{Path: path, Name: "a b.json"})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) = %v, want nil", path, err)
	}

	want := map[string]string{
		"Accept-Ranges":          "bytes",
		"Cache-Control":          "private, no-cache",
		"Content-Disposition":    `attachment; filename="a b.json"`,
		"Content-Length":         "5",
		"Content-Type":           "application/json",
		"X-Content-Type-Options": "nosniff",
		"Last-Modified":          info.ModTime().UTC().Format(http.TimeFormat),
	}

	got := make(map[string]string, len(want))
	for k := range want {
		got[k] = resp.Header.Get(k)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ServeDownload(%q) headers mismatch (-want +got):\n%s", path, diff)
	}

	if etag := resp.Header.Get("ETag"); !strings.HasPrefix(etag, `"`) || etag != FileETag(info) {
		t.Errorf("ServeDownload(%q) ETag = %q, want the strong %q", path, etag, FileETag(info))
	}
}

func TestServeDownloadContentType(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		d    Download
		want string
	}{
		"given":              {d: Download{Name: "a.bin", ContentType: "text/plain; charset=utf-8"}, want: "text/plain; charset=utf-8"},
		"by name":            {d: Download{Name: "a.json"}, want: "application/json"},
		"unknown attachment": {d: Download{Name: "disk.qc2"}, want: "application/octet-stream"},
		// sniffed by net/http: the file holds text
		"unknown inline": {d: Download{}, want: "text/plain; charset=utf-8"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := tc.d
			d.Path = filepath.Join(t.TempDir(), "file.unknownext")

			if err := os.WriteFile(d.Path, []byte("plain text"), 0o600); err != nil {
				t.Fatal(err)
			}

			resp, _ := serveDownload(t, http.MethodGet, d)
			if got := resp.Header.Get("Content-Type"); got != tc.want {
				t.Errorf("ServeDownload(%+v) Content-Type = %q, want %q", tc.d, got, tc.want)
			}

			if got := resp.Header.Get("Content-Disposition"); (got != "") != (d.Name != "") {
				t.Errorf("ServeDownload(%+v) Content-Disposition = %q, want one only when Name is set", tc.d, got)
			}
		})
	}
}

func TestServeDownloadRefuses(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "0123456789")

	tests := map[string]struct {
		d    Download
		want error
	}{
		"directory": {d: Download{Path: filepath.Dir(path)}, want: ErrNotRegular},
		"missing":   {d: Download{Path: path + ".missing"}, want: os.ErrNotExist},
		"too large": {d: Download{Path: path, MaxSize: 9}, want: ErrTooLarge},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()

			_, err := ServeDownload(rec, httptest.NewRequest(http.MethodGet, "/download", nil), tc.d)
			if !errors.Is(err, tc.want) {
				t.Errorf("ServeDownload(%+v) = %v, want %v", tc.d, err, tc.want)
			}

			if rec.Body.Len() != 0 || len(rec.Header()) != 0 {
				t.Errorf("ServeDownload(%+v) wrote header %v and body %q, want nothing", tc.d, rec.Header(), rec.Body.String())
			}
		})
	}

	exact := Download{Path: path, MaxSize: 10}
	if _, err := ServeDownload(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/download", nil), exact); err != nil {
		t.Errorf("ServeDownload(%+v) of a 10 byte file = %v, want nil", exact, err)
	}
}

func TestFileETagChangesWithTheFile(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "0123456789")

	stat := func() os.FileInfo {
		t.Helper()

		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		return info
	}

	before := stat()
	etag := FileETag(before)

	if again := FileETag(stat()); again != etag {
		t.Errorf("FileETag(%q) of an unchanged file = %s, want %s", path, again, etag)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = f.WriteString("more")
	_ = f.Close()

	// keep the old modification time: the size alone must change the ETag
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}

	appended := FileETag(stat())
	if appended == etag {
		t.Errorf("FileETag(%q) after an append = %s, want a new ETag", path, appended)
	}

	// replace the file with one of the same size and modification time: only
	// its inode differs
	replacement := path + ".new"
	if err := os.WriteFile(replacement, []byte("0123456789more"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(replacement, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	if fileInode(stat()) != 0 && FileETag(stat()) == appended {
		t.Errorf("FileETag(%q) after the file was replaced = %s, want a new ETag", path, appended)
	}
}

// The download is streamed: serving a large file allocates nowhere near its
// size. Not parallel, so other tests' allocations do not count.
func TestServeDownloadStreams(t *testing.T) {
	const size = 64 << 20

	path := filepath.Join(t.TempDir(), "disk.qc2")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	// sparse: no disk space needed
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}

	_ = f.Close()

	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	d := Download{Path: path, Name: "disk.qc2"}

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)

	served, err := ServeDownload(discardWriter{header: http.Header{}}, req, d)

	runtime.ReadMemStats(&after)

	if err != nil || !served.Started {
		t.Fatalf("ServeDownload(%q) = %+v, %v; want a started download, nil", path, served, err)
	}

	// io.Copy's buffer is 32 KiB
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > size/16 {
		t.Errorf("ServeDownload(%q) of %d bytes allocated %d bytes, want at most %d", path, size, allocated, size/16)
	}

	resp, _ := serveDownload(t, http.MethodHead, d)
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(size) {
		t.Errorf("ServeDownload(HEAD %q) Content-Length = %s, want %d", path, got, size)
	}
}

// discardWriter is a ResponseWriter that throws the body away.
type discardWriter struct {
	header http.Header
}

func (w discardWriter) Header() http.Header         { return w.header }
func (w discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w discardWriter) WriteHeader(int)             {}
