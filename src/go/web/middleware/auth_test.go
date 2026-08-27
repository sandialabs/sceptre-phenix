package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

func TestFromPhenixAuthTokenForm(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(
		http.MethodPost,
		"/builder/save",
		strings.NewReader("token=test-token&filename=topology.xml"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	token, err := fromPhenixAuthTokenForm(req)
	if err != nil {
		t.Fatalf("extracting form token: %v", err)
	}

	if token != "test-token" {
		t.Fatalf("unexpected token: got %q, want %q", token, "test-token")
	}

	if req.FormValue("filename") != "topology.xml" {
		t.Fatal("extracting token consumed other form values")
	}
}

func TestAuthTokenFromFormSetsHeader(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(
		http.MethodPost,
		"/builder/save",
		strings.NewReader("token=test-token&filename=topology.xml"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler := AuthTokenFromForm(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Phenix-Auth-Token"); got != "bearer test-token" {
			t.Fatalf("unexpected auth header: got %q", got)
		}
	}))

	handler.ServeHTTP(rec, req)
}

func TestAuthTokenFromFormPreservesHeader(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(
		http.MethodPost,
		"/builder/save",
		strings.NewReader("token=form-token"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Phenix-Auth-Token", "bearer header-token")
	rec := httptest.NewRecorder()
	handler := AuthTokenFromForm(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Phenix-Auth-Token"); got != "bearer header-token" {
			t.Fatalf("unexpected auth header: got %q", got)
		}
	}))

	handler.ServeHTTP(rec, req)
}

func TestRequirePermissionAllowsMatchingRole(t *testing.T) {
	t.Parallel()

	role := rbac.Role{Spec: &v1.RoleSpec{
		Policies: []*v1.PolicySpec{{Resources: []string{"scorch"}, Verbs: []string{"get"}}},
	}}
	ctx := context.WithValue(context.Background(), ContextKeyRole, role)
	req := httptest.NewRequest(http.MethodGet, "/scorch", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	called := false
	handler := RequirePermission("scorch", "get")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("authorized handler was not called")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequirePermissionRejectsMissingPermission(t *testing.T) {
	t.Parallel()

	role := rbac.Role{Spec: &v1.RoleSpec{
		Policies: []*v1.PolicySpec{{Resources: []string{"scorch"}, Verbs: []string{"get"}}},
	}}
	ctx := context.WithValue(context.Background(), ContextKeyRole, role)
	ctx = context.WithValue(ctx, ContextKeyUser, "test-user")
	req := httptest.NewRequest(http.MethodPost, "/scorch", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	called := false
	handler := RequirePermission("scorch", "post")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("unauthorized handler was called")
	}

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRequirePermissionRejectsMissingRole(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/builder", nil)
	rec := httptest.NewRecorder()
	handler := RequirePermission("builder", "get")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler without role was called")
	}))

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}
