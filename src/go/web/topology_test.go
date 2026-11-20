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
			Spec: map[string]any{
				"nodes": []any{
					map[string]any{"general": map[string]any{"hostname": "vm"}},
					map[string]any{"general": map[string]any{"hostname": "held", "do_not_boot": true}},
					map[string]any{"external": true, "general": map[string]any{"hostname": "hil"}},
					map[string]any{"general": map[string]any{"hostname": "router"}},
				},
			},
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

	var got struct {
		VMNames []string `json:"vm_names"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if diff := cmp.Diff([]string{"vm", "router"}, got.VMNames); diff != "" {
		t.Errorf("GET launchable VM names mismatch (-want +got):\n%s", diff)
	}
}

func TestGetTopologyLaunchableVMNamesRequiresTopologyGet(t *testing.T) {
	role := rbac.Role{Spec: &v1.RoleSpec{}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/topologies/lab/launchable-vm-names", nil)
	req = mux.SetURLVars(req, map[string]string{"topo": "lab"})
	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(GetTopologyLaunchableVMNames).ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusForbidden {
		t.Errorf("GET launchable VM names without permission status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
