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
	t.Parallel()

	for name, body := range map[string]string{
		"not JSON":          "{not json",
		"empty":             "",
		"a list":            "[]",
		"a name not a text": `{"name": 1}`,
		"truncated":         `{"name": "exp", "topology": `,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/api/v1/experiments", strings.NewReader(body))
			ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, testRole("experiments", "create"))
			ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

			rec := httptest.NewRecorder()
			weberror.ErrorHandler(CreateExperiment).ServeHTTP(rec, req.WithContext(ctx))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST /api/v1/experiments %q = %d, want %d: %s", body, rec.Code, http.StatusBadRequest, rec.Body.String())
			}

			var got weberror.WebError
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
			}

			want := weberror.WebError{Message: "invalid request body", Code: weberror.CodeInvalidRequest}
			opts := []cmp.Option{cmpopts.IgnoreFields(weberror.WebError{}, "Cause"), cmpopts.IgnoreUnexported(weberror.WebError{})}
			if diff := cmp.Diff(want, got, opts...); diff != "" {
				t.Errorf("POST /api/v1/experiments %q body mismatch (-want +got):\n%s", body, diff)
			}

			if got.Cause == "" {
				t.Errorf("POST /api/v1/experiments %q cause is empty, want the parse error", body)
			}
		})
	}
}
