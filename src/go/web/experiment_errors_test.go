package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/web/middleware"
	"phenix/web/weberror"
)

// TestCreateExperimentRejectsInvalidBody checks that a body that is not a
// CreateExperimentRequest gets a 400, not a 500.
func TestCreateExperimentRejectsInvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/experiments", strings.NewReader("{not json"))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, testRole("experiments", "create"))
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(CreateExperiment).ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var got weberror.WebError
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}

	want := weberror.WebError{Message: "invalid request body", Code: weberror.CodeInvalidRequest}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(weberror.WebError{}, "Cause")); diff != "" {
		t.Errorf("body mismatch (-want +got):\n%s", diff)
	}

	if got.Cause == "" {
		t.Error("cause is empty, want the parse error")
	}
}
