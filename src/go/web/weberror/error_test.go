package weberror

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestErrorHandlerAnswersTheHandlerError asserts an error is answered as the
// handler returned it.
func TestErrorHandlerAnswersTheHandlerError(t *testing.T) {
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
