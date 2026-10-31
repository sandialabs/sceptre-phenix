package weberror

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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

	t.Run("unwraps to cause", func(t *testing.T) {
		if !errors.Is(NewWebError(cause, "unable to create new config"), cause) {
			t.Error("errors.Is(NewWebError(cause, ...), cause) = false, want true")
		}
	})
}
