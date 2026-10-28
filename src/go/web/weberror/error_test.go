package weberror

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"
	"pgregory.net/rapid"

	"phenix/util/plog"
)

// logLines collects plog's JSON records while a test runs.
type logLines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p)
}

func (l *logLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// captureLogs records every plog record until the test ends.
func captureLogs(t *testing.T) *logLines {
	t.Helper()

	lines := &logLines{}

	plog.AddHandler(t.Name(), slog.NewJSONHandler(lines, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { plog.RemoveHandler(t.Name()) })

	return lines
}

// errorBody is the JSON Error body as a client reads it.
type errorBody struct {
	Message  string            `json:"message"`
	Cause    string            `json:"cause"`
	Code     Code              `json:"code"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// serve runs handler as a route would, and returns its status, Content-Type and decoded body.
func serve(t *testing.T, handler ErrorHandler) (int, string, errorBody) {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/things?token=secret", nil))

	var body errorBody
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("error body %q is not JSON: %v", rec.Body.String(), err)
		}
	}

	return rec.Code, rec.Header().Get("Content-Type"), body
}

// A handler's error that is not a WebError answers 500 with a generic JSON
// Error, and its text is logged with the request's method and path, not sent.
func TestErrorHandlerOtherError(t *testing.T) {
	logs := captureLogs(t)

	status, contentType, body := serve(t, func(http.ResponseWriter, *http.Request) error {
		return errors.New("disk on fire")
	})

	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", status, http.StatusInternalServerError)
	}

	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	if diff := cmp.Diff(errorBody{Message: internalMessage, Code: CodeInternal}, body); diff != "" {
		t.Errorf("body mismatch (-want +got):\n%s", diff)
	}

	logged := logs.String()
	for _, want := range []string{`"level":"ERROR"`, `"err":"disk on fire"`, `"method":"GET"`, `"path":"/api/v1/things"`} {
		if !strings.Contains(logged, want) {
			t.Errorf("logs = %s, want %s in them", logged, want)
		}
	}

	if strings.Contains(logged, "secret") {
		t.Errorf("logs = %s, want the query left out", logged)
	}
}

// A WebError answers its own status with its message, cause and user
// metadata, keeping system metadata out of the body.
func TestErrorHandlerWebError(t *testing.T) {
	status, contentType, body := serve(t, func(http.ResponseWriter, *http.Request) error {
		return NewWebError(errors.New("no such file"), "reading %s", "a.log").
			SetStatus(http.StatusNotFound).
			WithMetadata("file", "a.log", true).
			WithMetadata("dir", "/srv", false)
	})

	if status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", status, http.StatusNotFound)
	}

	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	want := errorBody{
		Message: "reading a.log", Cause: "no such file", Code: CodeNotFound, Metadata: map[string]string{"file": "a.log"},
	}
	if diff := cmp.Diff(want, body); diff != "" {
		t.Errorf("body mismatch (-want +got):\n%s", diff)
	}
}

// A WebError without a message answers its status's text, so the client
// never shows an empty message.
func TestErrorHandlerEmptyMessage(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		status := rapid.SampledFrom([]int{
			http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
			http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable,
		}).Draw(rt, "status")

		got, _, body := serve(t, func(http.ResponseWriter, *http.Request) error {
			return New(status, "")
		})

		want := errorBody{Message: http.StatusText(status), Code: CodeFor(status)}
		if got != status {
			rt.Fatalf("status = %d, want %d", got, status)
		}

		if diff := cmp.Diff(want, body); diff != "" {
			rt.Fatalf("body mismatch (-want +got):\n%s", diff)
		}
	})
}

// A handler that returns no error keeps its own response.
func TestErrorHandlerNoError(t *testing.T) {
	status, contentType, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) error {
		w.Header().Set("Content-Type", "text/csv")
		w.WriteHeader(http.StatusCreated)

		return nil
	})

	if status != http.StatusCreated || contentType != "text/csv" {
		t.Errorf("response = %d %q, want %d %q", status, contentType, http.StatusCreated, "text/csv")
	}
}

// Error answers as [http.Error] would, with the JSON Error body in place of the
// text, dropping a Content-Length meant for other content.
func TestError(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Length", "99")

	Error(rec, "job not found", http.StatusNotFound)

	got := map[string]string{
		"Content-Length":         rec.Header().Get("Content-Length"),
		"Content-Type":           rec.Header().Get("Content-Type"),
		"X-Content-Type-Options": rec.Header().Get("X-Content-Type-Options"),
		"body":                   rec.Body.String(),
	}
	want := map[string]string{
		"Content-Length":         "",
		"Content-Type":           "application/json",
		"X-Content-Type-Options": "nosniff",
		"body":                   `{"cause":"","message":"job not found","code":"not_found"}`,
	}

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("response mismatch (-want +got):\n%s", diff)
	}
}

// A gorilla Upgrader with UpgradeError refuses a request that is no websocket handshake with the JSON Error body.
func TestUpgradeError(t *testing.T) {
	upgrader := websocket.Upgrader{Error: UpgradeError}
	rec := httptest.NewRecorder()

	if _, err := upgrader.Upgrade(rec, httptest.NewRequest(http.MethodGet, "/ws", nil), nil); err == nil {
		t.Fatal("Upgrade(plain GET) error = nil, want a handshake error")
	}

	var body WebError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", rec.Body, err)
	}

	got := map[string]string{
		"Content-Type":          rec.Header().Get("Content-Type"),
		"Sec-Websocket-Version": rec.Header().Get("Sec-Websocket-Version"),
		"code":                  string(body.Code),
		"message has reason":    strconv.FormatBool(strings.Contains(body.Message, "websocket")),
	}
	want := map[string]string{
		"Content-Type":          "application/json",
		"Sec-Websocket-Version": "13",
		"code":                  string(CodeInvalidRequest),
		"message has reason":    "true",
	}

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("handshake refusal mismatch (-want +got):\n%s", diff)
	}
}

// Write logs a server error at error level and a client error at debug level,
// with the error's system metadata.
func TestWriteLogLevel(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusBadRequest:          `"level":"DEBUG"`,
		http.StatusNotFound:            `"level":"DEBUG"`,
		http.StatusInternalServerError: `"level":"ERROR"`,
		http.StatusServiceUnavailable:  `"level":"ERROR"`,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			logs := captureLogs(t)

			Write(httptest.NewRecorder(), New(status, "failed").WithMetadata("exp", "demo", false))

			logged := logs.String()
			if !strings.Contains(logged, want) || !strings.Contains(logged, `"exp":"demo"`) {
				t.Errorf("logs = %s, want %s with exp=demo", logged, want)
			}
		})
	}
}
