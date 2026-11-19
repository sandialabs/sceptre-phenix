package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

func TestGetTopologyLaunchableVMNames(t *testing.T) {
	tests := []struct {
		name  string
		nodes []any
		// want is the decoded response body, so a null vm_names (nil) and an
		// empty array ([]any{}) are distinct.
		want map[string]any
	}{
		{
			name: "mixed nodes",
			nodes: []any{
				map[string]any{"general": map[string]any{"hostname": "vm"}},
				map[string]any{"general": map[string]any{"hostname": "held", "do_not_boot": true}},
				map[string]any{"external": true, "general": map[string]any{"hostname": "hil"}},
				map[string]any{"general": map[string]any{"hostname": "router"}},
			},
			want: map[string]any{"vm_names": []any{"vm", "router"}},
		},
		{
			name: "no launchable nodes",
			nodes: []any{
				map[string]any{"general": map[string]any{"hostname": "held", "do_not_boot": true}},
				map[string]any{"external": true, "general": map[string]any{"hostname": "hil"}},
			},
			want: map[string]any{"vm_names": []any{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := store.NewMockStore(gomock.NewController(t))
			m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
				if c.FullName() != "Topology/lab" {
					return store.ErrNotExist
				}

				*c = store.Config{
					Version: "phenix.sandia.gov/v1",
					Kind:    "Topology",
					Metadata: store.ConfigMetadata{
						Name: "lab",
					},
					Spec: map[string]any{"nodes": tt.nodes},
				}

				return nil
			}).AnyTimes()

			previous := store.DefaultStore
			store.DefaultStore = m //nolint:reassign // monkey patching for test

			t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // monkey patching for test

			role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
				{Resources: []string{"topologies"}, ResourceNames: []string{"lab"}, Verbs: []string{"get"}},
			}}}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/topologies/lab/launchable-vm-names", nil)
			req = mux.SetURLVars(req, map[string]string{"topo": "lab"})
			ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
			ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

			rec := httptest.NewRecorder()
			weberror.ErrorHandler(GetTopologyLaunchableVMNames).ServeHTTP(rec, req.WithContext(ctx))

			if rec.Code != http.StatusOK {
				t.Fatalf("GET launchable VM names status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
			}

			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding response: %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GET launchable VM names mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetTopologyLaunchableVMNamesRefusals(t *testing.T) {
	tests := []struct {
		name string
		topo string
		role rbac.Role
		want int
	}{
		{
			name: "no permissions",
			topo: "lab",
			role: rbac.Role{Spec: &v1.RoleSpec{}},
			want: http.StatusForbidden,
		},
		{
			name: "topologies get on another topology",
			topo: "lab",
			role: rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
				{Resources: []string{"topologies"}, ResourceNames: []string{"other"}, Verbs: []string{"get"}},
			}}},
			want: http.StatusForbidden,
		},
		{
			name: "topologies list only",
			topo: "lab",
			role: rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
				{Resources: []string{"topologies"}, ResourceNames: []string{"lab"}, Verbs: []string{"list"}},
			}}},
			want: http.StatusForbidden,
		},
		{
			name: "unknown topology",
			topo: "none",
			role: rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
				{Resources: []string{"topologies"}, ResourceNames: []string{"none"}, Verbs: []string{"get"}},
			}}},
			want: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := store.NewMockStore(gomock.NewController(t))
			m.EXPECT().Get(gomock.Any()).Return(store.ErrNotExist).AnyTimes()

			previous := store.DefaultStore
			store.DefaultStore = m //nolint:reassign // monkey patching for test

			t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // monkey patching for test

			req := httptest.NewRequest(http.MethodGet, "/api/v1/topologies/"+tt.topo+"/launchable-vm-names", nil)
			req = mux.SetURLVars(req, map[string]string{"topo": tt.topo})
			ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, tt.role)
			ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

			rec := httptest.NewRecorder()
			weberror.ErrorHandler(GetTopologyLaunchableVMNames).ServeHTTP(rec, req.WithContext(ctx))

			if rec.Code != tt.want {
				t.Errorf("GET launchable VM names of %s status = %d, want %d: %s", tt.topo, rec.Code, tt.want, rec.Body)
			}
		})
	}
}
