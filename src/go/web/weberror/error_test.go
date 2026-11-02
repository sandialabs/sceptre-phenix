package weberror

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/plog/plogtest"
)

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

		want := WebError{Message: "config with same name already exists", Cause: cause.Error()}
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

		if got := recorder.Body.String(); got != "" {
			t.Errorf("ServeHTTP(plain error) body = %q, want empty", got)
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
		if len(logged) != 1 || logged[0]["token"] != "secret" || logged[0]["draft"] != "d1" || logged[0]["level"] != "ERROR" {
			t.Errorf("ServeHTTP(wrapped web error) logged %v, want one error with the system metadata", logged)
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

func TestWebErrorError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  *WebError
		want string
	}{
		{name: "no cause", err: NewWebError(nil, "config %s not found", "topology/t1"), want: "config topology/t1 not found"},
		{name: "a cause", err: NewWebError(errors.New("disk full"), "unable to save"), want: "unable to save: disk full"},
		{name: "an empty cause", err: NewWebError(errors.New(""), "unable to save"), want: "unable to save"},
		{name: "Unicode", err: NewWebError(errors.New("añadir falló"), "nœud %q", "é"), want: `nœud "é": añadir falló`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
