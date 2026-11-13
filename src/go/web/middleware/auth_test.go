package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/web/weberror"
)

func TestAuthOnlySkipsExactPublicPaths(t *testing.T) {
	tests := map[string]bool{
		"/api/v1/login":                       true,
		"/api/v1/signup":                      true,
		"/login":                              true,
		"/api/v1/experiments/login":           false,
		"/api/v1/users/signup":                false,
		"/api/v1/configs/topology/login":      false,
		"/api/v1/workflow/apply/login":        false,
		"/api/v1/experiments/exp/files/login": false,
		"/api/v1/login/extra":                 false,
	}

	for path, public := range tests {
		t.Run(path, func(t *testing.T) {
			var reached bool

			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
			handler := Auth("test-signing-key", "")(next)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if reached != public {
				t.Fatalf("handler reached = %t without a token, want %t (status %d)", reached, public, rec.Code)
			}

			if !public && rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", rec.Code)
			}
		})
	}
}

// TestAuthRejectsTokenWithoutUsername checks that a signed token without a
// username claim gets a 401 whose message says the token is invalid.
func TestAuthRejectsTokenWithoutUsername(t *testing.T) {
	const key = "test-signing-key"

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	}).SignedString([]byte(key))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	var reached bool

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/experiments", nil)
	req.Header.Set("X-Phenix-Auth-Token", "Bearer "+signed)

	rec := httptest.NewRecorder()
	Auth(key, "")(next).ServeHTTP(rec, req)

	if reached {
		t.Fatal("handler reached with a token that has no username")
	}

	var got weberror.WebError
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}

	want := weberror.WebError{Status: 0, Message: "invalid token", Code: weberror.CodeUnauthorized}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	if diff := cmp.Diff(want, got, cmpopts.IgnoreUnexported(weberror.WebError{})); diff != "" {
		t.Errorf("body mismatch (-want +got):\n%s", diff)
	}
}
