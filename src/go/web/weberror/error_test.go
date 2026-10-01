package weberror

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"phenix/store"
	"phenix/util/plog/plogtest"
)

// TestErrorHandlerOutOfSpace asserts every route answers a write etcd refused
// for lack of space with 507 and the store's plain message, whatever the
// handler returned, and logs the refusal with that message once.
func TestErrorHandlerOutOfSpace(t *testing.T) {
	refused := fmt.Errorf(
		"writing config JSON to Etcd: %w: %w",
		store.ErrNoSpace, errors.New("etcdserver: mvcc: database space exceeded"),
	)

	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "web error", err: NewWebError(refused, "unable to create new config")},
		{name: "server error", err: NewWebError(refused, "unable to save draft").SetStatus(http.StatusInternalServerError)},
		{name: "wrapped web error", err: fmt.Errorf("saving: %w", NewWebError(refused, "unable to save draft"))},
		{name: "plain error", err: fmt.Errorf("storing config: %w", refused)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logs := plogtest.Capture(t)
			recorder := httptest.NewRecorder()

			ErrorHandler(func(http.ResponseWriter, *http.Request) error { return tt.err }).
				ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/configs", nil))

			if recorder.Code != http.StatusInsufficientStorage {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInsufficientStorage)
			}

			var body WebError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding response: %v", err)
			}

			if body.Message != store.ErrNoSpace.Error() || body.Cause != "" {
				t.Fatalf("response = %+v, want only the message %q", body, store.ErrNoSpace.Error())
			}

			records := logs.Records(t, plogtest.Level(slog.LevelError))
			messages := make([]string, 0, len(records))

			for _, record := range records {
				message, _ := record["msg"].(string)
				messages = append(messages, message)
			}

			if len(messages) != 1 || strings.Count(messages[0], store.ErrNoSpace.Error()) != 1 ||
				!strings.Contains(messages[0], "database space exceeded") {
				t.Fatalf("logged %q, want one error holding the message once", messages)
			}
		})
	}
}

// TestErrorHandlerKeepsOtherErrors asserts any other error is answered as the
// handler returned it.
func TestErrorHandlerKeepsOtherErrors(t *testing.T) {
	cause := errors.New("config topology/t1 already exists")

	recorder := httptest.NewRecorder()

	ErrorHandler(func(http.ResponseWriter, *http.Request) error {
		return NewWebError(cause, "config with same name already exists").SetStatus(http.StatusConflict)
	}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/configs", nil))

	var body WebError
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if recorder.Code != http.StatusConflict || body.Message != "config with same name already exists" ||
		body.Cause != cause.Error() {
		t.Fatalf("response = %d %+v, want the handler's", recorder.Code, body)
	}

	recorder = httptest.NewRecorder()

	ErrorHandler(func(http.ResponseWriter, *http.Request) error { return cause }).
		ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/configs", nil))

	if recorder.Code != http.StatusInternalServerError || recorder.Body.Len() != 0 {
		t.Fatalf("response = %d %q, want an empty 500", recorder.Code, recorder.Body)
	}

	if !errors.Is(NewWebError(cause, "unable to create new config"), cause) {
		t.Fatal("a web error must unwrap to its cause")
	}
}
