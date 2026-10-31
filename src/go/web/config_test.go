package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/gorilla/mux"

	"phenix/store"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// topologyConfigJSON returns a valid Topology config with the given name,
// as JSON.
func topologyConfigJSON(name string) string {
	return `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"` + name + `"},` +
		`"spec":{"nodes":[{"type":"VirtualMachine","general":{"hostname":"host"},` +
		`"hardware":{"os_type":"linux","drives":[{"image":"host.qc2"}]}}]}}`
}

// TestConfigWritesCheckTheConfigTheyWrite asserts a role limited to some
// configs writes no other through any config write route, whatever the body names.
func TestConfigWritesCheckTheConfigTheyWrite(t *testing.T) {
	const (
		user = `{"apiVersion":"phenix.sandia.gov/v1","kind":"User","metadata":{"name":"mallory"},` +
			`"spec":{"username":"mallory","first_name":"Mallory","last_name":"M","rbac":{"roleName":"Global Admin",` +
			`"policies":[{"resources":["*"],"resourceNames":["*"],"verbs":["*"]}]}}}`
		role = `{"apiVersion":"phenix.sandia.gov/v1","kind":"Role","metadata":{"name":"root"},` +
			`"spec":{"roleName":"Root","policies":[{"resources":["*"],"resourceNames":["*"],"verbs":["*"]}]}}`
	)

	var written []string

	write := func(c *store.Config) error {
		written = append(written, c.FullName())

		return nil
	}

	// The store holds Topology/lab, and only the topologies created are
	// written: a refused request writes nothing.
	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Create(gomock.Any()).DoAndReturn(write).AnyTimes()
	m.EXPECT().Update(gomock.Any()).DoAndReturn(write).AnyTimes()
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.FullName() != "Topology/lab" {
			return store.ErrNotExist
		}

		return nil
	}).AnyTimes()

	previous := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // monkey patching for test

	var (
		topologies = configsRoleOn([]string{"Topology/*"}, "create", "update")
		everything = configsRole("create", "update")
	)

	for _, tt := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request) error
		method  string
		vars    map[string]string
		body    string
		role    rbac.Role
		status  int
	}{
		{"POST /configs of a User", CreateConfig, http.MethodPost, nil, user, topologies, http.StatusForbidden},
		{"POST /configs of a Role", CreateConfig, http.MethodPost, nil, role, topologies, http.StatusForbidden},
		{
			"PUT /configs/topology/lab of a User", UpdateConfig, http.MethodPut,
			map[string]string{"kind": "topology", "name": "lab"}, user, topologies, http.StatusForbidden,
		},
		{
			"POST /workflow/configs/main of a User", WorkflowUpsertConfig, http.MethodPost,
			map[string]string{"branch": "main"}, user, topologies, http.StatusForbidden,
		},
		{"POST /configs of a Topology", CreateConfig, http.MethodPost, nil, topologyConfigJSON("plant"), topologies, http.StatusCreated},
		{
			"POST /configs of a Topology, with every config", CreateConfig, http.MethodPost, nil,
			topologyConfigJSON("site"), everything, http.StatusCreated,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/configs", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", mimeJSON)

			if tt.vars != nil {
				req = mux.SetURLVars(req, tt.vars)
			}

			ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, tt.role)
			ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

			rec := httptest.NewRecorder()
			weberror.ErrorHandler(tt.handler).ServeHTTP(rec, req.WithContext(ctx))

			if rec.Code != tt.status {
				t.Errorf("%s status = %d, want %d: %s", tt.name, rec.Code, tt.status, rec.Body)
			}
		})
	}

	if want := []string{"Topology/plant", "Topology/site"}; !slices.Equal(written, want) {
		t.Errorf("configs written = %q, want %q", written, want)
	}
}
