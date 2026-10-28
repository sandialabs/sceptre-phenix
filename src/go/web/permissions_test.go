package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/web/rbac"
)

// GET /permissions lists every known resource with its verbs, for a role that
// may list roles, and refuses any other role.
func TestGetPermissions(t *testing.T) {
	tests := map[string]struct {
		role rbac.Role
		want int
	}{
		"may list roles":     {role: testRole("roles", "list"), want: http.StatusOK},
		"may only get roles": {role: testRole("roles", "get", "*"), want: http.StatusForbidden},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodGet, serveAs(t, "alice", tt.role).URL+"/api/v1/permissions", "")
			body := readBody(t, resp)

			if resp.StatusCode != tt.want {
				t.Fatalf("GET /permissions = %d, want %d: %s", resp.StatusCode, tt.want, body)
			}

			if tt.want != http.StatusOK {
				return
			}

			var got struct {
				Permissions []rbac.ResourceVerbs `json:"permissions"`
			}

			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("GET /permissions = %s, not JSON: %v", body, err)
			}

			if diff := cmp.Diff(rbac.KnownPermissions(), got.Permissions); diff != "" {
				t.Errorf("GET /permissions mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
