package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

func TestContextAccessors(t *testing.T) {
	t.Parallel()

	role := rbac.Role{Spec: &v1.RoleSpec{Name: "Global Viewer"}}

	type values struct {
		User, JWT, Role string
	}

	tests := map[string]struct {
		ctx  context.Context
		want values
	}{
		"empty": {context.Background(), values{}},
		"set": {
			context.WithValue(context.WithValue(context.WithValue(context.Background(),
				ContextKeyUser, "alice"), ContextKeyJWT, "raw.jwt"), ContextKeyRole, role),
			values{User: "alice", JWT: "raw.jwt", Role: "Global Viewer"},
		},
		"wrong types": {
			context.WithValue(context.WithValue(context.WithValue(context.Background(),
				ContextKeyUser, 1), ContextKeyJWT, []byte("raw.jwt")), ContextKeyRole, &role),
			values{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := values{User: UserFromContext(tc.ctx), JWT: JWTFromContext(tc.ctx)}
			if r := RoleFromContext(tc.ctx); r.Spec != nil {
				got.Role = r.Spec.Name
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("context accessors mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUnixSocket(t *testing.T) {
	t.Parallel()

	var direct, socket bool

	plain := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { direct = FromUnixSocket(r.Context()) })
	marked := UnixSocket(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { socket = FromUnixSocket(r.Context()) }))

	plain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	marked.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if direct {
		t.Error("FromUnixSocket() = true for a request UnixSocket did not mark, want false")
	}

	if !socket {
		t.Error("FromUnixSocket() = false for a request UnixSocket marked, want true")
	}
}
