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

	"github.com/google/go-cmp/cmp"
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
				t.Errorf("GET %s with Accept-Encoding %q: gzipped = %v, want %v", target, tc.acceptEncoding, got, tc.wantGzip)
			}

			if got := resp.Header.Get("Content-Length"); tc.wantGzip && got != "" {
				t.Errorf("GET %s gzipped: Content-Length = %s, want none", target, got)
			}

			if got := resp.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
				t.Errorf("GET %s Vary = %q, want Accept-Encoding", target, got)
			}

			if diff := cmp.Diff(tc.body, readBody(t, resp)); diff != "" {
				t.Errorf("GET %s body mismatch (-want +got):\n%s", target, diff)
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
		t.Error("CompressResponses gave a websocket upgrade's handler a wrapped writer, want the original")
	}

	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("websocket upgrade Content-Encoding = %q, want none", enc)
	}
}

func TestCompressResponsesFlushes(t *testing.T) {
	h := CompressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "first chunk\n")

		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("CompressResponses writer is not an http.Flusher")
		}

		f.Flush()
		_, _ = io.WriteString(w, "second chunk\n")
	}))

	resp := get(t, h, "/api/v1/logs", "gzip")
	if got, want := readBody(t, resp), "first chunk\nsecond chunk\n"; got != want {
		t.Errorf("GET /api/v1/logs flushed between chunks = %q, want %q", got, want)
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
		t.Errorf("GET /vnc written in two parts: Content-Type = %q, want text/html", ct)
	}

	if diff := cmp.Diff("\n"+page, readBody(t, resp)); diff != "" {
		t.Errorf("GET /vnc body mismatch (-want +got):\n%s", diff)
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
				t.Errorf("GET / written as %d = %d, want %d", tc.code, resp.StatusCode, tc.code)
			}

			if tc.flush && !rec.Flushed {
				t.Error("GET / flushed: Flush did not reach the client")
			}

			if got := readBody(t, resp); got != tc.body {
				t.Errorf("GET / written as %d %q: body = %q, want %q", tc.code, tc.body, got, tc.body)
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
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("GET /api/v1/vms Content-Encoding = %q, want gzip", got)
	}

	if got := resp.Header.Get("ETag"); got != `W/"abc"` {
		t.Errorf(`GET /api/v1/vms gzipped: ETag = %q, want the weak W/"abc"`, got)
	}

	if got := resp.Header.Get("Accept-Ranges"); got != "" {
		t.Errorf("GET /api/v1/vms gzipped: Accept-Ranges = %q, want none", got)
	}

	if got := resp.Header.Get("Repr-Digest"); got != "" {
		t.Errorf("GET /api/v1/vms gzipped: Repr-Digest = %q, want none", got)
	}

	if diff := cmp.Diff(bigJSON, readBody(t, resp)); diff != "" {
		t.Errorf("GET /api/v1/vms body mismatch (-want +got):\n%s", diff)
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
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("GET /api/v1/configs/topology/demo Content-Encoding = %q, want gzip", got)
	}

	if got := resp.Header.Get("ETag"); got != `"r5"` {
		t.Errorf(`GET /api/v1/configs/topology/demo gzipped: ETag = %q, want the strong "r5"`, got)
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
		t.Errorf("GET a.log Range bytes=0-99 = %d, want %d", resp.StatusCode, http.StatusPartialContent)
	}

	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("GET a.log Range bytes=0-99: Content-Encoding = %q, want none", enc)
	}

	if diff := cmp.Diff(text[:100], readBody(t, resp)); diff != "" {
		t.Errorf("GET a.log Range bytes=0-99 body mismatch (-want +got):\n%s", diff)
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
					t.Fatal("CompressResponses writer is not an io.ReaderFrom")
				}

				_, _ = rf.ReadFrom(bytes.NewReader(data))
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/disks/download", nil)
			req.Header.Set("Accept-Encoding", "gzip")

			rec := &readerFromRecorder{ResponseRecorder: httptest.NewRecorder(), readFrom: 0}
			h.ServeHTTP(rec, req)

			if got := rec.readFrom > 0; got != tc.wantReadFrom {
				t.Errorf("ReadFrom of a %q response used the underlying ReadFrom = %v, want %v",
					tc.contentType, got, tc.wantReadFrom)
			}

			resp := rec.Result()
			defer resp.Body.Close()

			if got := resp.Header.Get("Content-Encoding") == "gzip"; got != tc.wantGzip {
				t.Errorf("ReadFrom of a %q response gzipped = %v, want %v", tc.contentType, got, tc.wantGzip)
			}

			if diff := cmp.Diff(data, []byte(readBody(t, resp))); diff != "" {
				t.Errorf("ReadFrom of a %q response body mismatch (-want +got):\n%s", tc.contentType, diff)
			}
		})
	}
}
