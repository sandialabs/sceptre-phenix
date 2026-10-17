package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var bigJSON = `{"vms":[` + strings.Repeat(`{"name":"host","state":"RUNNING"},`, 200) + `{}]}` //nolint:gochecknoglobals // test fixture

func apiHandler(status int, contentType, body string, setLength bool) http.Handler {
	return CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}

		if r.URL.Query().Has("attachment") {
			w.Header().Set("Content-Disposition", `attachment; filename="vms.txt"`)
		}

		if setLength {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}

		if status != 0 {
			w.WriteHeader(status)
		}

		_, _ = io.WriteString(w, body)
	}))
}

func TestCompressResponses(t *testing.T) {
	cases := []struct {
		name           string
		status         int
		contentType    string
		body           string
		setLength      bool
		acceptEncoding string
		wantGzip       bool
	}{
		{"json", 0, "application/json", bigJSON, false, "gzip, deflate", true},
		{"json with length", 0, "application/json; charset=utf-8", bigJSON, true, "gzip", true},
		{"yaml", 0, "application/x-yaml", bigJSON, false, "gzip", true},
		{"sniffed", 0, "", bigJSON, false, "gzip", true},
		{"client refuses gzip", 0, "application/json", bigJSON, false, "gzip;q=0", false},
		{"no accept-encoding", 0, "application/json", bigJSON, false, "", false},
		{"small with length", 0, "application/json", `{"ok":true}`, true, "gzip", false},
		{"binary download", 0, "application/octet-stream", bigJSON, false, "gzip", false},
		{"zip download", 0, "application/zip", bigJSON, false, "gzip", false},
		// a download reaches the client byte for byte, whatever its type
		{"text attachment", 0, "text/plain", bigJSON, false, "gzip", false},
		{"no content", http.StatusNoContent, "application/json", "", false, "gzip", false},
		{"not modified", http.StatusNotModified, "application/json", "", false, "gzip", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := "/api/v1/vms"
			if strings.Contains(tc.name, "attachment") {
				target += "?attachment"
			}

			resp := get(t, apiHandler(tc.status, tc.contentType, tc.body, tc.setLength), target, tc.acceptEncoding)

			if got := resp.Header.Get("Content-Encoding") == "gzip"; got != tc.wantGzip {
				t.Fatalf("gzip = %v, want %v", got, tc.wantGzip)
			}

			if tc.wantGzip && resp.Header.Get("Content-Length") != "" {
				t.Errorf("compressed response kept the uncompressed Content-Length")
			}

			if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
				t.Errorf("Vary = %q, want Accept-Encoding", resp.Header.Get("Vary"))
			}

			if got := readBody(t, resp); got != tc.body {
				t.Errorf("body does not round-trip: got %d bytes, want %d", len(got), len(tc.body))
			}
		})
	}
}

func TestCompressResponsesPassesWebsocketUpgrades(t *testing.T) {
	var hijackable bool

	h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, hijackable = w.(http.Hijacker)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, bigJSON)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")

	// httptest.ResponseRecorder is not a Hijacker; the point is that the
	// handler receives the original writer, not a compressing wrapper
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if hijackable {
		t.Fatal("handler got a wrapped writer for an upgrade request")
	}

	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q on an upgrade request", enc)
	}
}

func TestCompressResponsesFlushes(t *testing.T) {
	h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "first chunk\n")

		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("compressing writer is not a Flusher")
		}

		f.Flush()
		_, _ = io.WriteString(w, "second chunk\n")
	}))

	resp := get(t, h, "/api/v1/logs", "gzip")
	if got := readBody(t, resp); got != "first chunk\nsecond chunk\n" {
		t.Errorf("body = %q", got)
	}
}

// A response without a Content-Type is sniffed from its body's start however many writes that takes:
// a template whose output starts with a newline is still HTML.
func TestCompressResponsesSniffsWholePrefix(t *testing.T) {
	page := "<!DOCTYPE html>\n<html lang=\"en-US\"><head><title>vnc</title></head><body>" +
		strings.Repeat("<p>x</p>", 200) + "</body></html>"

	h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.WriteString(w, "\n")
		_, _ = io.WriteString(w, page)
	}))

	req := httptest.NewRequest(http.MethodGet, "/vnc", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}

	if got := readBody(t, resp); got != "\n"+page {
		t.Fatalf("body changed: got %d bytes, want %d", len(got), len(page)+1)
	}
}

func TestCompressResponsesHeldBackStatus(t *testing.T) {
	cases := []struct {
		name  string
		code  int
		body  string
		flush bool
	}{
		{"short body", http.StatusCreated, "created", false},
		{"no body", http.StatusAccepted, "", false},
		{"no content", http.StatusNoContent, "", false},
		{"flushed", http.StatusOK, "partial", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)

				if tc.flush {
					w.(http.Flusher).Flush()
				}
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", "gzip")

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			resp := rec.Result()
			defer resp.Body.Close()

			if resp.StatusCode != tc.code {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.code)
			}

			if tc.flush && !rec.Flushed {
				t.Fatal("Flush did not reach the client")
			}

			if got := readBody(t, resp); got != tc.body {
				t.Fatalf("body = %q, want %q", got, tc.body)
			}
		})
	}
}

// A compressed response is not the bytes a strong ETag, a byte range or a
// Repr-Digest would refer to.
func TestCompressResponsesWeakensValidators(t *testing.T) {
	h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Repr-Digest", "sha-256=:AAAA:")
		http.ServeContent(w, r, "vms.json", time.Time{}, strings.NewReader(bigJSON))
	}))

	resp := get(t, h, "/api/v1/vms", "gzip")
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("response was not compressed")
	}

	if got := resp.Header.Get("ETag"); got != `W/"abc"` {
		t.Errorf("ETag = %q, want the weak W/\"abc\"", got)
	}

	if got := resp.Header.Get("Accept-Ranges"); got != "" {
		t.Errorf("Accept-Ranges = %q on a compressed response", got)
	}

	if got := resp.Header.Get("Repr-Digest"); got != "" {
		t.Errorf("Repr-Digest = %q on a compressed response", got)
	}

	if got := readBody(t, resp); got != bigJSON {
		t.Errorf("body does not round-trip")
	}
}

// A config's ETag names its revision, not its bytes, so it stays strong when
// the config is compressed and can be sent back in If-Match.
func TestCompressResponsesKeepsRevisionETag(t *testing.T) {
	h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		setRevisionHeaders(w, 5)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, bigJSON)
	}))

	resp := get(t, h, "/api/v1/configs/topology/demo", "gzip")
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("response was not compressed")
	}

	if got := resp.Header.Get("ETag"); got != `"r5"` {
		t.Errorf(`ETag = %q, want the strong "r5"`, got)
	}

	_ = readBody(t, resp)
}

// A range of a text file is the uncompressed bytes asked for, even for a client accepting gzip
// (set explicitly, as Go's client drops gzip when it sends a Range).
func TestCompressResponsesRangeOnTextFile(t *testing.T) {
	text := strings.Repeat("phenix log line\n", 256)

	h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader(text))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/experiments/exp/files/a.log", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Range", "bytes=0-99")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}

	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q on a range", enc)
	}

	if got := readBody(t, resp); got != text[:100] {
		t.Errorf("body = %q, want the first 100 bytes", got)
	}
}

// readerFromRecorder is a ResponseRecorder that, like net/http's own writer,
// can send from a reader itself.
type readerFromRecorder struct {
	*httptest.ResponseRecorder

	readFrom int
}

func (r *readerFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.readFrom++

	return io.Copy(r.ResponseRecorder, src)
}

// A response that is not compressed reaches the underlying writer's ReadFrom,
// which net/http uses to send files with sendfile.
func TestCompressWriterReadFromPassesThrough(t *testing.T) {
	data := bytes.Repeat([]byte{0, 1, 2, 3}, 4096)

	cases := []struct {
		name         string
		contentType  string
		wantReadFrom bool
		wantGzip     bool
	}{
		{"binary", "application/octet-stream", true, false},
		{"compressed", "application/json", false, true},
		{"sniffed", "", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}

				rf, ok := w.(io.ReaderFrom)
				if !ok {
					t.Fatal("compressing writer is not a ReaderFrom")
				}

				_, _ = rf.ReadFrom(bytes.NewReader(data))
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/disks/download", nil)
			req.Header.Set("Accept-Encoding", "gzip")

			rec := &readerFromRecorder{ResponseRecorder: httptest.NewRecorder(), readFrom: 0}
			h.ServeHTTP(rec, req)

			if got := rec.readFrom > 0; got != tc.wantReadFrom {
				t.Errorf("underlying ReadFrom used = %v, want %v", got, tc.wantReadFrom)
			}

			resp := rec.Result()
			defer resp.Body.Close()

			if got := resp.Header.Get("Content-Encoding") == "gzip"; got != tc.wantGzip {
				t.Errorf("gzip = %v, want %v", got, tc.wantGzip)
			}

			if got := readBody(t, resp); got != string(data) {
				t.Errorf("body changed: got %d bytes, want %d", len(got), len(data))
			}
		})
	}
}
