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
		t.Fatalf("ServeDownload: %v", err)
	}

	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	if served.Status != resp.StatusCode {
		t.Errorf("Served.Status = %d, response status %d", served.Status, resp.StatusCode)
	}

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
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}

			if served.Started != tc.wantStarted {
				t.Errorf("Started = %v, want %v", served.Started, tc.wantStarted)
			}

			if got := resp.Header.Get("Content-Range"); got != tc.wantRange {
				t.Errorf("Content-Range = %q, want %q", got, tc.wantRange)
			}

			if tc.wantStatus == http.StatusRequestedRangeNotSatisfiable {
				return
			}

			if got := resp.Header.Get("ETag"); got != etag {
				t.Errorf("ETag = %q, want %q", got, etag)
			}

			if got := body(t, resp); got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
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
				t.Errorf("started = %v, want %v", got, tc.wantStarted)
			}
		})
	}
}

func TestServeDownloadHeaders(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "hello")

	resp, served := serveDownload(t, http.MethodGet, Download{Path: path, Name: "a b.json"})

	want := map[string]string{
		"Accept-Ranges":          "bytes",
		"Cache-Control":          "private, no-cache",
		"Content-Disposition":    `attachment; filename="a b.json"`,
		"Content-Length":         "5",
		"Content-Type":           "application/json",
		"X-Content-Type-Options": "nosniff",
		"Last-Modified":          served.Info.ModTime().UTC().Format(http.TimeFormat),
	}

	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	if etag := resp.Header.Get("ETag"); !strings.HasPrefix(etag, `"`) || etag != FileETag(served.Info) {
		t.Errorf("ETag = %q, want the strong %q", etag, FileETag(served.Info))
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
				t.Errorf("Content-Type = %q, want %q", got, tc.want)
			}

			if got := resp.Header.Get("Content-Disposition"); (got != "") != (d.Name != "") {
				t.Errorf("Content-Disposition = %q for name %q", got, d.Name)
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
				t.Fatalf("ServeDownload = %v, want %v", err, tc.want)
			}

			if rec.Body.Len() != 0 || len(rec.Header()) != 0 {
				t.Errorf("sent a response before refusing: %v %q", rec.Header(), rec.Body.String())
			}
		})
	}

	if _, err := ServeDownload(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/download", nil),
		Download{Path: path, MaxSize: 10}); err != nil {
		t.Errorf("a file of exactly MaxSize: %v", err)
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
		t.Fatalf("ETag of an unchanged file changed: %s, then %s", etag, again)
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
		t.Errorf("ETag unchanged after an append: %s", etag)
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
		t.Errorf("ETag unchanged after the file was replaced: %s", appended)
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

	if err != nil || served.Status != http.StatusOK {
		t.Fatalf("ServeDownload = %d, %v", served.Status, err)
	}

	// io.Copy's buffer is 32 KiB
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > size/16 {
		t.Errorf("serving a %d byte file allocated %d bytes", size, allocated)
	}

	resp, _ := serveDownload(t, http.MethodHead, d)
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(size) {
		t.Errorf("Content-Length = %s, want %d", got, size)
	}
}

// discardWriter is a ResponseWriter that throws the body away.
type discardWriter struct {
	header http.Header
}

func (w discardWriter) Header() http.Header         { return w.header }
func (w discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w discardWriter) WriteHeader(int)             {}
