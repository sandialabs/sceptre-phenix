package weberror

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/websocket"
	"pgregory.net/rapid"

	"phenix/util/plog"
	"phenix/util/plog/plogtest"
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
	Details  map[string]any    `json:"details,omitempty"`
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

// A WebError, even wrapped, answers its own status with its message, cause, user metadata and details, keeping
// system metadata out of the body; its own code wins over its status's.
func TestErrorHandlerWebError(t *testing.T) {
	tests := map[string]struct {
		err        error
		wantStatus int
		want       errorBody
	}{
		"metadata": {
			err: NewWebError(errors.New("no such file"), "reading %s", "a.log").
				SetStatus(http.StatusNotFound).
				WithMetadata("file", "a.log", true).
				WithMetadata("dir", "/srv", false),
			wantStatus: http.StatusNotFound,
			want: errorBody{
				Message: "reading a.log", Cause: "no such file", Code: CodeNotFound, Metadata: map[string]string{"file": "a.log"},
			},
		},
		"own code": {
			err:        NewWebError(nil, "minimega is busy").SetStatus(http.StatusConflict).WithCode(CodeUnavailable),
			wantStatus: http.StatusConflict,
			want:       errorBody{Message: "minimega is busy", Code: CodeUnavailable},
		},
		"details": {
			err: NewWebError(nil, "broken disks").SetStatus(http.StatusConflict).WithCode(CodeBrokenDisks).
				WithDetail("disks", []string{"a.qc2"}).WithDetail("vms", 1),
			wantStatus: http.StatusConflict,
			want: errorBody{
				Message: "broken disks", Code: CodeBrokenDisks,
				Details: map[string]any{"disks": []any{"a.qc2"}, "vms": float64(1)},
			},
		},
		"default status": {
			err:        NewWebError(nil, "bad %d", 7),
			wantStatus: http.StatusBadRequest,
			want:       errorBody{Message: "bad 7", Code: CodeInvalidRequest},
		},
		"wrapped": {
			err:        fmt.Errorf("handling: %w", New(http.StatusForbidden, "not yours")),
			wantStatus: http.StatusForbidden,
			want:       errorBody{Message: "not yours", Code: CodeForbidden},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			status, contentType, body := serve(t, func(http.ResponseWriter, *http.Request) error { return tc.err })

			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}

			if contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", contentType)
			}

			if diff := cmp.Diff(tc.want, body); diff != "" {
				t.Errorf("body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// An error's text is its message, followed by its cause when it has one.
func TestWebErrorError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *WebError
		want string
	}{
		"message and cause": {err: NewWebError(errors.New("no such file"), "reading %s", "a.log"), want: "reading a.log: no such file"},
		"message only":      {err: NewWebError(nil, "reading a.log"), want: "reading a.log"},
		"empty":             {err: New(http.StatusNotFound, ""), want: ""},
		"an empty cause":    {err: NewWebError(errors.New(""), "unable to save"), want: "unable to save"},
		"Unicode":           {err: NewWebError(errors.New("añadir falló"), "nœud %q", "é"), want: `nœud "é": añadir falló`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Metadata is always kept for the log, and for the user only when asked; a later value for a key replaces the
// earlier one.
func TestWithMetadata(t *testing.T) {
	t.Parallel()

	err := New(http.StatusNotFound, "missing").
		WithMetadata("exp", "demo", false).
		WithMetadata("vm", "vm1", true).
		WithMetadata("vm", "vm2", true)

	wantSystem := map[string]string{"exp": "demo", "vm": "vm2"}
	if diff := cmp.Diff(wantSystem, err.SystemMetadata); diff != "" {
		t.Errorf("SystemMetadata mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]string{"vm": "vm2"}, err.UserMetadata); diff != "" {
		t.Errorf("UserMetadata mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]any(nil), err.Details); diff != "" {
		t.Errorf("Details mismatch (-want +got):\n%s", diff)
	}
}

// Details are returned to the user and not logged.
func TestWithDetailNotLogged(t *testing.T) {
	logs := captureLogs(t)

	err := New(http.StatusInternalServerError, "start failed").WithDetail("disks", "secret-disk.qc2")
	if diff := cmp.Diff(map[string]any{"disks": "secret-disk.qc2"}, err.Details); diff != "" {
		t.Errorf("Details mismatch (-want +got):\n%s", diff)
	}

	rec := httptest.NewRecorder()
	Write(rec, err)

	if !strings.Contains(rec.Body.String(), "secret-disk.qc2") {
		t.Errorf("body = %s, want the detail in it", rec.Body)
	}

	if logged := logs.String(); !strings.Contains(logged, "start failed") || strings.Contains(logged, "secret-disk.qc2") {
		t.Errorf("logs = %s, want the error without its detail", logged)
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

// TestErrorHandlerAnswersTheHandlerError asserts an error is answered as the
// handler returned it.
func TestErrorHandlerAnswersTheHandlerError(t *testing.T) {
	cause := errors.New("config topology/t1 already exists")

	t.Run("web error", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		ErrorHandler(func(http.ResponseWriter, *http.Request) error {
			return NewWebError(cause, "config with same name already exists").SetStatus(http.StatusConflict)
		}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/configs", nil))

		if recorder.Code != http.StatusConflict {
			t.Errorf("ServeHTTP(web error) status = %d, want %d", recorder.Code, http.StatusConflict)
		}

		var got WebError
		if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal(%q) returned error: %v", recorder.Body, err)
		}

		want := WebError{Message: "config with same name already exists", Cause: cause.Error(), Code: CodeConflict}
		if diff := cmp.Diff(want, got, cmpopts.IgnoreUnexported(WebError{})); diff != "" {
			t.Errorf("ServeHTTP(web error) body mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("plain error", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		ErrorHandler(func(http.ResponseWriter, *http.Request) error { return cause }).
			ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/configs", nil))

		if recorder.Code != http.StatusInternalServerError {
			t.Errorf("ServeHTTP(plain error) status = %d, want %d", recorder.Code, http.StatusInternalServerError)
		}

		if got := recorder.Body.String(); strings.Contains(got, cause.Error()) {
			t.Errorf("ServeHTTP(plain error) body = %q, want the cause left out", got)
		}
	})

	t.Run("wrapped web error with metadata", func(t *testing.T) {
		logs := plogtest.Capture(t)
		recorder := httptest.NewRecorder()

		ErrorHandler(func(http.ResponseWriter, *http.Request) error {
			web := NewWebError(cause, "draft %s refused", "d1").
				WithMetadata("draft", "d1", true).
				WithMetadata("token", "secret", false).
				WithCode("draft-refused").
				SetStatus(http.StatusUnprocessableEntity)
			web.Issues = []map[string]string{{"path": "nodes[0]"}}

			return fmt.Errorf("publishing: %w", web)
		}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/builder/drafts", nil))

		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("ServeHTTP(wrapped web error) status = %d, want %d", recorder.Code, http.StatusUnprocessableEntity)
		}

		if got := recorder.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("ServeHTTP(wrapped web error) Content-Type = %q, want application/json", got)
		}

		var got map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal(%q) returned error: %v", recorder.Body, err)
		}

		// The system metadata is logged, but never answered.
		want := map[string]any{
			"cause": cause.Error(), "message": "draft d1 refused", "code": "draft-refused",
			"metadata": map[string]any{"draft": "d1"}, "issues": []any{map[string]any{"path": "nodes[0]"}},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("ServeHTTP(wrapped web error) body mismatch (-want +got):\n%s", diff)
		}

		logged := logs.Records(t, plogtest.Message("draft d1 refused: "+cause.Error()))
		if len(logged) != 1 || logged[0]["token"] != "secret" || logged[0]["draft"] != "d1" || logged[0]["level"] != "DEBUG" {
			t.Errorf("ServeHTTP(wrapped web error) logged %v, want one debug record with the system metadata", logged)
		}
	})

	t.Run("no error", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		ErrorHandler(func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusNoContent)

			return nil
		}).ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/configs/topology/t1", nil))

		if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
			t.Errorf("ServeHTTP(no error) = %d %q, want the handler's own %d and no body",
				recorder.Code, recorder.Body, http.StatusNoContent)
		}
	})

	t.Run("unwraps to cause", func(t *testing.T) {
		if !errors.Is(NewWebError(cause, "unable to create new config"), cause) {
			t.Error("errors.Is(NewWebError(cause, ...), cause) = false, want true")
		}
	})
}

func TestWebErrorBuilders(t *testing.T) {
	got := NewWebError(nil, "draft %s refused", "d1").
		WithMetadata("draft", "d1", true).
		WithMetadata("token", "secret", false).
		WithMetadata("token", "rotated", false).
		WithCode("draft-refused").
		SetStatus(http.StatusConflict)

	want := &WebError{
		Message:        "draft d1 refused",
		Status:         http.StatusConflict,
		SystemMetadata: map[string]string{"draft": "d1", "token": "rotated"},
		UserMetadata:   map[string]string{"draft": "d1"},
		Code:           "draft-refused",
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreUnexported(WebError{})); diff != "" {
		t.Errorf("WebError builders mismatch (-want +got):\n%s", diff)
	}

	if status := NewWebError(nil, "refused").Status; status != http.StatusBadRequest {
		t.Errorf("NewWebError status = %d, want %d", status, http.StatusBadRequest)
	}

	if metadata := NewWebError(nil, "refused").WithMetadata("token", "secret", false).UserMetadata; metadata != nil {
		t.Errorf("WithMetadata(token, secret, false) user metadata = %v, want nil", metadata)
	}
}
