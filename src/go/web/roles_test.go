package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"
	"pgregory.net/rapid"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/polltest"
	"phenix/web/broker"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// errRoleStore is the error of a roleTestStore operation that fails.
var errRoleStore = errors.New("role test store failed")

// roleTestStore is a BoltDB whose operation op ("list", "create", "update" or
// "delete") on the config of kind and name fails with the error fail gives.
// For "list", name is empty.
type roleTestStore struct {
	store.Store

	fail func(op, kind, name string) error
}

func (s *roleTestStore) failed(op, kind, name string) error {
	if s.fail == nil {
		return nil
	}

	return s.fail(op, kind, name)
}

func (s *roleTestStore) List(kinds ...string) (store.Configs, error) {
	for _, kind := range kinds {
		if err := s.failed("list", kind, ""); err != nil {
			return nil, err
		}
	}

	return s.Store.List(kinds...) //nolint:wrapcheck // the store's own error
}

func (s *roleTestStore) Create(c *store.Config) error {
	if err := s.failed("create", c.Kind, c.Metadata.Name); err != nil {
		return err
	}

	return s.Store.Create(c) //nolint:wrapcheck // the store's own error
}

func (s *roleTestStore) Update(c *store.Config) error {
	if err := s.failed("update", c.Kind, c.Metadata.Name); err != nil {
		return err
	}

	return s.Store.Update(c) //nolint:wrapcheck // the store's own error
}

func (s *roleTestStore) Delete(c *store.Config) error {
	if err := s.failed("delete", c.Kind, c.Metadata.Name); err != nil {
		return err
	}

	return s.Store.Delete(c) //nolint:wrapcheck // the store's own error
}

// failOn makes the store fail op on configs of kind, and of name unless it is
// empty, with err.
func (s *roleTestStore) failOn(op, kind, name string, err error) {
	s.fail = func(o, k, n string) error {
		if o == op && strings.EqualFold(k, kind) && (name == "" || n == name) {
			return err
		}

		return nil
	}
}

// labPolicies are the policies of the custom role Lab of useRoleTestStore.
func labPolicies() []*v1.PolicySpec {
	return []*v1.PolicySpec{
		{Resources: []string{"experiments"}, Verbs: []string{"get", "list"}},
		{Resources: []string{"vms"}, Verbs: []string{"list"}},
		{Resources: []string{"disks"}, ResourceNames: []string{"shared.qc2"}, Verbs: []string{"list"}},
	}
}

// useRoleTestStore makes the store a BoltDB of the built-in roles, after the
// startup migrations, the custom roles lab (Lab) and spare (Spare), and the
// users bob (Lab for exp1 and exp2/*), carol (Lab for every name), dave
// (Global Admin) and erin (no role).
func useRoleTestStore(t *testing.T) *roleTestStore {
	t.Helper()

	useDefaultRoles(t)

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	for name, spec := range map[string]*v1.RoleSpec{
		"lab":   {Name: "Lab", Policies: labPolicies()},
		"spare": {Name: "Spare", Policies: []*v1.PolicySpec{}},
	} {
		storeTestConfig(t, "Role", name, spec)
	}

	assignTestRole(t, "bob", "lab", "exp1", "exp2/*")
	assignTestRole(t, "carol", "lab")
	assignTestRole(t, "dave", "global-admin")
	storeTestConfig(t, "User", "erin", &v1.UserSpec{Username: "erin"})

	s := &roleTestStore{Store: store.DefaultStore}
	storetest.Use(t, s)

	return s
}

// storeTestConfig stores the config of kind and name with spec.
func storeTestConfig(t *testing.T, kind, name string, spec any) {
	t.Helper()

	c := &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     kind,
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     structs.MapDefaultCase(spec, structs.CASESNAKE),
	}

	if err := store.Create(c); err != nil {
		t.Fatalf("storing %s/%s: %v", kind, name, err)
	}
}

// assignTestRole stores the user username with the copy of the role that
// POST /users gives it for names.
func assignTestRole(t *testing.T, username, role string, names ...string) {
	t.Helper()

	r, err := rbac.RoleFromConfig(role)
	if err != nil {
		t.Fatalf("RoleFromConfig(%s) error = %v", role, err)
	}

	if err := r.SetResourceNames(names...); err != nil {
		t.Fatalf("SetResourceNames(%q) error = %v", names, err)
	}

	r.AddPolicy([]string{resourceUsers}, []string{username}, []string{verbGet, verbPatch})

	storeTestConfig(t, "User", username, &v1.UserSpec{Username: username, Role: r.Spec})
}

// storedUserRole returns the policies of the user's stored copy of its role.
func storedUserRole(t *testing.T, username string) *v1.RoleSpec {
	t.Helper()

	u, err := rbac.GetUser(username)
	if err != nil {
		t.Fatalf("GetUser(%s) error = %v", username, err)
	}

	return u.Spec.Role
}

// errorDetails returns the details of the JSON Error body of rec.
func errorDetails(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var e weberror.WebError
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decoding error body %q: %v", rec.Body.String(), err)
	}

	return e.Details
}

// failingBody is a request body whose read fails.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errRoleStore }

// A body that cannot be read is a 500 for each route that reads one.
func TestRoleRoutesBodyReadError(t *testing.T) {
	for name, h := range map[string]func(http.ResponseWriter, *http.Request) error{
		"POST /roles":         CreateRole,
		"PATCH /roles/{name}": UpdateRole,
	} {
		t.Run(name, func(t *testing.T) {
			req := roleRequest(http.MethodPost, "/roles/lab", "", map[string]string{"name": "lab"}, adminRole())
			req.Body = io.NopCloser(failingBody{})

			want := handlerAnswer{http.StatusInternalServerError, "unable to read request body"}
			if got, _ := serveHandler(h, req); got != want {
				t.Errorf("%s with a failing body = %+v, want %+v", name, got, want)
			}
		})
	}
}

// POST /roles stores the role under its metadataName or the slug of its
// name, answers 201 with its Location and the role, and audits it.
func TestCreateRole(t *testing.T) {
	tests := map[string]struct {
		body     string
		role     rbac.Role
		location string
		want     Role
	}{
		"a slug of the name": {
			body:     `{"name":"Lab Admin (night)","policies":[{"resources":["vms"],"resourceNames":[],"verbs":["list"]}]}`,
			role:     adminRole(),
			location: "/api/v1/roles/lab-admin-night",
			want:     Role{Name: "Lab Admin (night)", Policies: []Policy{{Resources: []string{"vms"}, Verbs: []string{"list"}}}},
		},
		"a metadataName and no policies": {
			body:     `{"name":"Night Shift","metadataName":"night","policies":[]}`,
			role:     adminRole(),
			location: "/api/v1/roles/night",
			want:     Role{Name: "Night Shift", Policies: []Policy{}},
		},
		"what the requester holds": {
			body:     `{"name":"Viewer of exp1","policies":[{"resources":["vms"],"resourceNames":["exp1/*"],"verbs":["list"]}]}`,
			role:     combinedRole(testRole(resourceRoles, "create"), testRole("vms", "list", "exp1/*")),
			location: "/api/v1/roles/viewer-of-exp1",
			want: Role{
				Name:     "Viewer of exp1",
				Policies: []Policy{{Resources: []string{"vms"}, ResourceNames: []string{"exp1/*"}, Verbs: []string{"list"}}},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useRoleTestStore(t)
			events := captureAudit(t)

			got, rec := serveHandler(CreateRole, roleRequest(http.MethodPost, "/roles", tc.body, nil, tc.role))
			if got.Status != http.StatusCreated {
				t.Fatalf("POST /roles = %+v, want 201: %s", got, rec.Body)
			}

			if loc := rec.Header().Get("Location"); loc != tc.location {
				t.Errorf("POST /roles Location = %q, want %q", loc, tc.location)
			}

			var role Role
			if err := json.Unmarshal(rec.Body.Bytes(), &role); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tc.want, role); diff != "" {
				t.Errorf("POST /roles body mismatch (-want +got):\n%s", diff)
			}

			configName := strings.TrimPrefix(tc.location, "/api/v1/roles/")

			stored, err := rbac.RoleFromConfig(configName)
			if err != nil {
				t.Fatalf("RoleFromConfig(%s) error = %v", configName, err)
			}

			if diff := cmp.Diff(tc.want, roleFromRBAC(*stored)); diff != "" {
				t.Errorf("stored role mismatch (-want +got):\n%s", diff)
			}

			wantAudit(t, events(), "role-create", "success", map[string]any{
				"phenix.config.name": "Role/" + configName, "phenix.role.name": tc.want.Name,
			})
		})
	}
}

// POST /roles answers 400 for a bad body, 403 without roles create or for a
// role that grants more than the requester holds, 409 for a name in use, and
// 500 when the store fails.
func TestCreateRoleErrors(t *testing.T) {
	const vmsList = `"policies":[{"resources":["vms"],"verbs":["list"]}]`

	tests := map[string]struct {
		body    string
		role    rbac.Role
		prepare func(*roleTestStore)
		want    handlerAnswer
		reason  string
	}{
		"forbidden": {
			body: `{"name":"x",` + vmsList + `}`, role: testRole(resourceRoles, "get"),
			want: handlerAnswer{http.StatusForbidden, "forbidden to create roles"}, reason: "forbidden",
		},
		"a grant the requester does not hold": {
			body: `{"name":"x",` + vmsList + `}`, role: combinedRole(testRole(resourceRoles, "create"), testRole("vms", "list", "exp1")),
			want:   handlerAnswer{http.StatusForbidden, "the role would grant permissions that you do not hold: list on vms for *"},
			reason: reasonEscalation,
		},
		"a malformed body": {body: `{`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"an unknown field": {body: `{"name":"x","policy":[]}`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"no name":          {body: `{` + vmsList + `}`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"a bad config name": {
			body: `{"name":"x","metadataName":"a b",` + vmsList + `}`, want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"no policies": {body: `{"name":"x"}`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"a policy on roles": {
			body: `{"name":"x","policies":[{"resources":["roles"],"verbs":["create"]}]}`,
			want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"a policy on every resource": {
			body: `{"name":"x","policies":[{"resources":["*"],"resourceNames":["lab"],"verbs":["*"]}]}`,
			want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"a policy of too many resources": {
			body: `{"name":"x","policies":[{"resources":[` + strings.Repeat(`"vms",`, maxPolicyItems) + `"vms"],"verbs":["list"]}]}`,
			want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"a body over 1 MiB": {
			body: `{"name":"x","policies":[],"metadataName":"` + strings.Repeat("a", maxRoleBody) + `"}`,
			want: handlerAnswer{http.StatusRequestEntityTooLarge, "the request body is larger than 1 MiB"},
		},
		"a name with a zero-width space": {
			body: "{\"name\":\"Lab\u200b\",\"metadataName\":\"lab-zw\",\"policies\":[]}",
			want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"a built-in name in capitals": {
			body: `{"name":"GLOBAL ADMIN","metadataName":"other","policies":[]}`,
			want: handlerAnswer{http.StatusConflict, `the name "GLOBAL ADMIN" belongs to a built-in role`},
		},
		"a taken name in capitals": {
			body: `{"name":"LAB","metadataName":"lab3","policies":[]}`,
			want: handlerAnswer{http.StatusConflict, `the role "lab" already has the name "LAB"`},
		},
		"a bad policy": {
			body: `{"name":"x","policies":[{"resources":["rockets"],"verbs":["get"]}]}`,
			want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"a taken config name": {
			body: `{"name":"Lab 2","metadataName":"lab",` + vmsList + `}`,
			want: handlerAnswer{http.StatusConflict, `the role "lab" already has the name "lab"`},
		},
		"a taken name": {
			body: `{"name":"Lab","metadataName":"lab2",` + vmsList + `}`,
			want: handlerAnswer{http.StatusConflict, `the role "lab" already has the name "Lab"`},
		},
		"a name that is a config name": {
			body: `{"name":"spare","metadataName":"other",` + vmsList + `}`,
			want: handlerAnswer{http.StatusConflict, `the role "spare" already has the name "spare"`},
		},
		"a config name that is a name": {
			body: `{"name":"Other","metadataName":"Spare",` + vmsList + `}`,
			want: handlerAnswer{http.StatusConflict, `the role "spare" already has the name "Spare"`},
		},
		"a built-in name": {
			body: `{"name":"Global Admin","metadataName":"other",` + vmsList + `}`,
			want: handlerAnswer{http.StatusConflict, `the name "Global Admin" belongs to a built-in role`},
		},
		"the name of a deleted built-in role": {
			body: `{"name":"VM Admin",` + vmsList + `}`,
			prepare: func(s *roleTestStore) {
				s.fail = nil

				if err := config.Delete("role/vm-admin"); err != nil {
					s.failOn("list", "Role", "", err)
				}
			},
			want: handlerAnswer{http.StatusConflict, `the name "vm-admin" belongs to a built-in role`},
		},
		"roles that cannot be listed": {
			body: `{"name":"x",` + vmsList + `}`, prepare: func(s *roleTestStore) { s.failOn("list", "Role", "", errRoleStore) },
			want: handlerAnswer{http.StatusInternalServerError, "error retrieving roles"},
		},
		"a store that fails": {
			body: `{"name":"x",` + vmsList + `}`, prepare: func(s *roleTestStore) { s.failOn("create", "Role", "", errRoleStore) },
			want: handlerAnswer{http.StatusInternalServerError, "error storing the role"},
		},
		"a config created meanwhile": {
			body: `{"name":"x",` + vmsList + `}`, prepare: func(s *roleTestStore) { s.failOn("create", "Role", "", store.ErrExist) },
			want: handlerAnswer{http.StatusConflict, "a role config of that name already exists"},
		},
		"a config the store refuses": {
			body: `{"name":"x",` + vmsList + `}`,
			prepare: func(s *roleTestStore) {
				s.failOn("create", "Role", "", fmt.Errorf("refused: %w", types.ErrValidationFailed))
			},
			want: handlerAnswer{Status: http.StatusBadRequest},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useRoleTestStore(t)
			if tc.prepare != nil {
				tc.prepare(s)
			}

			events := captureAudit(t)

			role := tc.role
			if role.Spec == nil {
				role = adminRole()
			}

			got, rec := serveHandler(CreateRole, roleRequest(http.MethodPost, "/roles", tc.body, nil, role))
			if got.Status != tc.want.Status || (tc.want.Message != "" && got.Message != tc.want.Message) {
				t.Errorf("POST /roles %s = %+v, want %+v: %s", tc.body, got, tc.want, rec.Body)
			}

			if tc.reason != "" {
				wantAudit(t, events(), "role-create", "failure", map[string]any{"event.reason": tc.reason})
			}

			if roles, _ := rbac.GetRoles(); slices.ContainsFunc(roles, func(r *rbac.Role) bool { return r.Spec.Name == "x" }) {
				t.Errorf("POST /roles %s stored the role", tc.body)
			}
		})
	}
}

// PATCH /roles/{name}, found by either name, replaces the policies of the role
// and of each user's copy of it, keeping the user's names and own policy.
func TestUpdateRole(t *testing.T) {
	const body = `{"name":"Lab","policies":[` +
		`{"resources":["experiments"],"verbs":["get"]},` +
		`{"resources":["vms/*"],"verbs":["update"]},` +
		`{"resources":["hosts"],"resourceNames":["node1"],"verbs":["list"]}]}`

	newPolicies := []*v1.PolicySpec{
		{Resources: []string{"experiments"}, Verbs: []string{"get"}},
		{Resources: []string{"vms/*"}, Verbs: []string{"update"}},
		{Resources: []string{"hosts"}, ResourceNames: []string{"node1"}, Verbs: []string{"list"}},
	}

	userCopy := func(username string, names ...string) *v1.RoleSpec {
		copied := &v1.RoleSpec{Name: "Lab"}

		for _, p := range newPolicies {
			c := *p
			if c.ResourceNames == nil {
				c.ResourceNames = names
			}

			copied.Policies = append(copied.Policies, &c)
		}

		copied.Policies = append(copied.Policies, &v1.PolicySpec{
			Resources: []string{resourceUsers}, ResourceNames: []string{username}, Verbs: []string{verbGet, verbPatch},
		})

		return copied
	}

	for _, name := range []string{"lab", "Lab"} {
		t.Run(name, func(t *testing.T) {
			useRoleTestStore(t)
			events := captureAudit(t)

			dave := storedUserRole(t, "dave")

			got, rec := serveHandler(
				UpdateRole,
				roleRequest(http.MethodPatch, "/roles/"+name, body, map[string]string{"name": name}, adminRole()),
			)
			if got.Status != http.StatusOK {
				t.Fatalf("PATCH /roles/%s = %+v, want 200: %s", name, got, rec.Body)
			}

			var resp RoleUpdate
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}

			want := RoleUpdate{
				Role:  roleFromRBAC(rbac.Role{Spec: &v1.RoleSpec{Name: "Lab", Policies: newPolicies}}),
				Users: []string{"bob", "carol"},
				Count: 2,
			}

			if diff := cmp.Diff(want, resp); diff != "" {
				t.Errorf("PATCH /roles/%s body mismatch (-want +got):\n%s", name, diff)
			}

			stored, _ := rbac.RoleFromConfig("lab")
			if diff := cmp.Diff(newPolicies, stored.Spec.Policies); diff != "" {
				t.Errorf("stored role mismatch (-want +got):\n%s", diff)
			}

			for username, wantRole := range map[string]*v1.RoleSpec{
				"bob":   userCopy("bob", "exp1", "exp2/*"),
				"carol": userCopy("carol", "*"),
				"dave":  dave,
				"erin":  nil,
			} {
				if diff := cmp.Diff(wantRole, storedUserRole(t, username)); diff != "" {
					t.Errorf("%s's copy of the role mismatch (-want +got):\n%s", username, diff)
				}
			}

			wantAudit(t, events(), "role-update", "success", map[string]any{
				"phenix.config.name": "Role/lab", "phenix.role.name": name, "phenix.role.users": []any{"bob", "carol"},
			})
		})
	}
}

// PATCH /roles/{name} answers 400 for a bad body or a new name, 403 without
// roles patch on the name or for a grant the requester does not hold, 404 for
// an unknown role, 409 for a built-in role or a stale one, and 500 when the
// store fails; only then does it change anything.
func TestUpdateRoleErrors(t *testing.T) {
	const policies = `"policies":[{"resources":["vms"],"verbs":["list"]}]`

	tests := map[string]struct {
		name    string
		body    string
		role    rbac.Role
		prepare func(*roleTestStore)
		want    handlerAnswer
		reason  string
	}{
		"forbidden": {
			name: "lab", body: `{` + policies + `}`, role: testRole(resourceRoles, verbPatch, "spare"),
			want: handlerAnswer{http.StatusForbidden, "forbidden to update role"}, reason: "forbidden",
		},
		"a grant the requester does not hold": {
			name: "lab",
			body: `{` + policies + `}`,
			role: testRole(resourceRoles, verbPatch, "lab"),
			want: handlerAnswer{
				http.StatusForbidden,
				"the role would grant permissions that you do not hold: list on vms, list on vms for *",
			},
			reason: reasonEscalation,
		},
		"a malformed body": {name: "lab", body: `{"policies":`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"an unknown field": {name: "lab", body: `{"roleName":"Lab",` + policies + `}`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"no policies":      {name: "lab", body: `{"name":"Lab"}`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"null policies":    {name: "lab", body: `{"policies":null}`, want: handlerAnswer{Status: http.StatusBadRequest}},
		"a policy that changes roles": {
			name: "lab", body: `{"policies":[{"resources":["r*"],"verbs":["delete"]}]}`, want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"too many policies": {
			name: "lab", body: `{"policies":[` + strings.Repeat(`{"resources":["vms"],"verbs":["list"]},`, maxRolePolicies) +
				`{"resources":["vms"],"verbs":["list"]}]}`,
			want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"a body over 1 MiB": {
			name: "lab", body: `{"policies":[],"name":"` + strings.Repeat("a", maxRoleBody) + `"}`,
			want: handlerAnswer{http.StatusRequestEntityTooLarge, "the request body is larger than 1 MiB"},
		},
		"a bad policy": {
			name: "lab", body: `{"policies":[{"resources":["vms"],"verbs":["fly"]}]}`, want: handlerAnswer{Status: http.StatusBadRequest},
		},
		"a new name": {
			name: "lab", body: `{"name":"Lab 2",` + policies + `}`,
			want: handlerAnswer{http.StatusBadRequest, "a role cannot be renamed; name must be Lab"},
		},
		"an unknown role": {name: "nobody", body: `{` + policies + `}`, want: handlerAnswer{http.StatusNotFound, "role not found"}},
		"a built-in role": {
			name: "global-admin", body: `{` + policies + `}`,
			want: handlerAnswer{http.StatusConflict, "the role global-admin is a built-in role and cannot be changed"},
		},
		"a built-in role by its name": {
			name: "Experiment Viewer", body: `{` + policies + `}`,
			want: handlerAnswer{http.StatusConflict, "the role experiment-viewer is a built-in role and cannot be changed"},
		},
		"roles that cannot be listed": {
			name: "lab", body: `{` + policies + `}`, prepare: func(s *roleTestStore) { s.failOn("list", "Role", "", errRoleStore) },
			want: handlerAnswer{http.StatusInternalServerError, "error retrieving role"},
		},
		"users that cannot be listed": {
			name: "lab", body: `{` + policies + `}`, prepare: func(s *roleTestStore) { s.failOn("list", "User", "", errRoleStore) },
			want: handlerAnswer{http.StatusInternalServerError, "error retrieving users"},
		},
		"a role the store fails to update": {
			name: "lab", body: `{` + policies + `}`, prepare: func(s *roleTestStore) { s.failOn("update", "Role", "lab", errRoleStore) },
			want: handlerAnswer{http.StatusInternalServerError, "error storing the role"},
		},
		"a role changed meanwhile": {
			name:    "lab",
			body:    `{` + policies + `}`,
			prepare: func(s *roleTestStore) { s.failOn("update", "Role", "lab", store.ErrConflict) },
			want:    handlerAnswer{http.StatusConflict, "the role changed while the request ran; send it again"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useRoleTestStore(t)
			if tc.prepare != nil {
				tc.prepare(s)
			}

			events := captureAudit(t)
			bob := storedUserRole(t, "bob")

			role := tc.role
			if role.Spec == nil {
				role = adminRole()
			}

			req := roleRequest(http.MethodPatch, "/roles/"+url.PathEscape(tc.name), tc.body, map[string]string{"name": tc.name}, role)

			got, rec := serveHandler(UpdateRole, req)
			if got.Status != tc.want.Status || (tc.want.Message != "" && got.Message != tc.want.Message) {
				t.Errorf("PATCH /roles/%s %s = %+v, want %+v: %s", tc.name, tc.body, got, tc.want, rec.Body)
			}

			if tc.reason != "" {
				wantAudit(t, events(), "role-update", "failure", map[string]any{"event.reason": tc.reason})
			}

			s.fail = nil

			if stored, _ := rbac.RoleFromConfig("lab"); !cmp.Equal(labPolicies(), stored.Spec.Policies) {
				t.Errorf("PATCH /roles/%s %s changed the role to %v", tc.name, tc.body, stored.Spec.Policies)
			}

			if diff := cmp.Diff(bob, storedUserRole(t, "bob")); diff != "" {
				t.Errorf("PATCH /roles/%s %s changed bob's role (-want +got):\n%s", tc.name, tc.body, diff)
			}
		})
	}
}

// When a user's copy of the role cannot be stored, PATCH /roles/{name} still
// updates the role and the other users, answers 500 naming the user, and a
// second request updates the user.
func TestUpdateRoleUserFailure(t *testing.T) {
	s := useRoleTestStore(t)
	carol := storedUserRole(t, "carol")
	s.failOn("update", "User", "carol", errRoleStore)

	events := captureAudit(t)
	body := `{"policies":[{"resources":["experiments"],"verbs":["get"]}]}`
	vars := map[string]string{"name": "lab"}

	got, rec := serveHandler(UpdateRole, roleRequest(http.MethodPatch, "/roles/lab", body, vars, adminRole()))

	want := handlerAnswer{
		http.StatusInternalServerError,
		"the role lab is updated, but the copy of the role of 1 users is not; send the request again",
	}
	if got != want {
		t.Fatalf("PATCH /roles/lab with carol failing = %+v, want %+v", got, want)
	}

	if diff := cmp.Diff(map[string]any{"users": []any{"carol"}}, errorDetails(t, rec)); diff != "" {
		t.Errorf("PATCH /roles/lab error details mismatch (-want +got):\n%s", diff)
	}

	wantAudit(t, events(), "role-update", "failure", map[string]any{"phenix.role.users": []any{"carol"}})

	experimentsGet := []*v1.PolicySpec{
		{Resources: []string{"experiments"}, ResourceNames: []string{"exp1", "exp2/*"}, Verbs: []string{"get"}},
	}
	if diff := cmp.Diff(experimentsGet, storedUserRole(t, "bob").Policies[:1]); diff != "" {
		t.Errorf("bob's copy of the role mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(carol, storedUserRole(t, "carol")); diff != "" {
		t.Errorf("carol's copy of the role changed (-want +got):\n%s", diff)
	}

	s.fail = nil

	got, rec = serveHandler(UpdateRole, roleRequest(http.MethodPatch, "/roles/lab", body, vars, adminRole()))
	if got.Status != http.StatusOK {
		t.Fatalf("PATCH /roles/lab again = %+v, want 200", got)
	}

	var resp RoleUpdate
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"bob", "carol"}, resp.Users); diff != "" {
		t.Errorf("PATCH /roles/lab again users mismatch (-want +got):\n%s", diff)
	}

	if got := storedUserRole(t, "carol").Policies[0].Verbs; !slices.Equal(got, []string{"get"}) {
		t.Errorf("carol's verbs after the second request = %v, want [get]", got)
	}
}

// DELETE /roles/{name}, found by either name, deletes a custom role that no
// user has, and audits it.
func TestDeleteRole(t *testing.T) {
	for _, name := range []string{"spare", "Spare"} {
		t.Run(name, func(t *testing.T) {
			useRoleTestStore(t)
			events := captureAudit(t)

			got, rec := serveHandler(
				DeleteRole,
				roleRequest(http.MethodDelete, "/roles/"+name, "", map[string]string{"name": name}, adminRole()),
			)
			if got.Status != http.StatusNoContent {
				t.Fatalf("DELETE /roles/%s = %+v, want 204: %s", name, got, rec.Body)
			}

			if _, err := rbac.RoleFromConfig("spare"); !errors.Is(err, rbac.ErrRoleNotFound) {
				t.Errorf("RoleFromConfig(spare) after DELETE error = %v, want %v", err, rbac.ErrRoleNotFound)
			}

			wantAudit(t, events(), "role-delete", "success", map[string]any{"phenix.config.name": "Role/spare"})
		})
	}
}

// DELETE /roles/{name} answers 403 without roles delete on the name, 404 for
// an unknown role, 409 for a built-in role or one that users have, which it
// names, and 500 when the store fails.
func TestDeleteRoleErrors(t *testing.T) {
	tests := map[string]struct {
		name    string
		role    rbac.Role
		prepare func(*roleTestStore)
		want    handlerAnswer
		users   []any
	}{
		"forbidden": {
			name: "spare", role: testRole(resourceRoles, "delete", "lab"),
			want: handlerAnswer{http.StatusForbidden, "forbidden to delete role"},
		},
		"an unknown role": {name: "nobody", want: handlerAnswer{http.StatusNotFound, "role not found"}},
		"a built-in role": {
			name: "Disabled", want: handlerAnswer{http.StatusConflict, "the role disabled is a built-in role and cannot be changed"},
		},
		"a role users have": {
			name:  "Lab",
			want:  handlerAnswer{http.StatusConflict, "the role lab is assigned to 2 users; give them another role first"},
			users: []any{"bob", "carol"},
		},
		"users that cannot be listed": {
			name: "spare", prepare: func(s *roleTestStore) { s.failOn("list", "User", "", errRoleStore) },
			want: handlerAnswer{http.StatusInternalServerError, "error retrieving users"},
		},
		"a store that fails": {
			name: "spare", prepare: func(s *roleTestStore) { s.failOn("delete", "Role", "spare", errRoleStore) },
			want: handlerAnswer{http.StatusInternalServerError, "error storing the role"},
		},
		"a role deleted meanwhile": {
			name: "spare", prepare: func(s *roleTestStore) { s.failOn("delete", "Role", "spare", store.ErrNotExist) },
			want: handlerAnswer{http.StatusNotFound, "role not found"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := useRoleTestStore(t)
			if tc.prepare != nil {
				tc.prepare(s)
			}

			events := captureAudit(t)

			role := tc.role
			if role.Spec == nil {
				role = adminRole()
			}

			got, rec := serveHandler(
				DeleteRole,
				roleRequest(http.MethodDelete, "/roles/"+url.PathEscape(tc.name), "", map[string]string{"name": tc.name}, role),
			)
			if got != tc.want {
				t.Fatalf("DELETE /roles/%s = %+v, want %+v", tc.name, got, tc.want)
			}

			if tc.users != nil {
				if diff := cmp.Diff(map[string]any{"users": tc.users}, errorDetails(t, rec)); diff != "" {
					t.Errorf("DELETE /roles/%s error details mismatch (-want +got):\n%s", tc.name, diff)
				}
			}

			if tc.want.Status == http.StatusForbidden {
				wantDenied(t, events(), "role-delete")
			}

			s.fail = nil

			for _, n := range []string{"lab", "spare", "disabled"} {
				if _, err := rbac.RoleFromConfig(n); err != nil {
					t.Errorf("RoleFromConfig(%s) after a refused DELETE error = %v, want nil", n, err)
				}
			}
		})
	}
}

// Of the built-in roles, after the startup migrations, only Global Admin may
// create, update or delete roles.
func TestOnlyGlobalAdminChangesRoles(t *testing.T) {
	useRoleTestStore(t)

	configs, err := config.BuiltinRoleConfigs()
	if err != nil {
		t.Fatalf("BuiltinRoleConfigs() error = %v", err)
	}

	if len(configs) < 2 {
		t.Fatalf("BuiltinRoleConfigs() = %d roles, want them all", len(configs))
	}

	for _, c := range configs {
		role, err := rbac.RoleFromConfig(c.Metadata.Name)
		if err != nil {
			t.Fatalf("RoleFromConfig(%s) error = %v", c.Metadata.Name, err)
		}

		for _, verb := range []string{"create", verbPatch, "delete"} {
			want := c.Metadata.Name == "global-admin"

			if got := role.Allowed(resourceRoles, verb) || role.Allowed(resourceRoles, verb, "lab"); got != want {
				t.Errorf("built-in role %s may %s roles = %t, want %t", c.Metadata.Name, verb, got, want)
			}
		}
	}
}

// roleSlug gives a config name, or nothing, and leaves a slug as it is.
func TestRoleSlug(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"Lab Admin": "lab-admin", "  Night -- Shift!  ": "night-shift", "ops@site.v2": "ops@site.v2", "!!!": "", "Lab Ω 2": "lab-2",
	} {
		if got := roleSlug(name); got != want {
			t.Errorf("roleSlug(%q) = %q, want %q", name, got, want)
		}
	}

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringN(0, maxRoleName, -1).Draw(t, "name")
		slug := roleSlug(name)

		if slug != "" && !config.NameRegex.MatchString(slug) {
			t.Fatalf("roleSlug(%q) = %q, not a config name", name, slug)
		}

		if again := roleSlug(slug); again != slug {
			t.Fatalf("roleSlug(roleSlug(%q)) = %q, want %q", name, again, slug)
		}
	})
}

// checkRoleName takes a name of one URL path segment without control
// characters or space at either end.
func TestCheckRoleName(t *testing.T) {
	t.Parallel()

	for name, ok := range map[string]bool{
		"Lab Admin": true, "Café": true, "": false, " Lab": false, "Lab ": false, "a/b": false, ".": false, "..": false,
		"tab\there": false, "\xff": false, strings.Repeat("a", maxRoleName): true, strings.Repeat("a", maxRoleName+1): false,
	} {
		if err := checkRoleName(name); (err == nil) != ok || err != nil && !errors.Is(err, errRoleRequest) {
			t.Errorf("checkRoleName(%q) = %v, want ok %t", name, err, ok)
		}
	}
}

// replaceUserRoles skips a user whose copy is no longer of the role, and
// names, and wraps the store's error for, a user it cannot update.
func TestReplaceUserRoles(t *testing.T) {
	s := useRoleTestStore(t)

	old, err := rbac.RoleFromConfig("lab")
	if err != nil {
		t.Fatal(err)
	}

	users, err := rbac.GetUsers()
	if err != nil {
		t.Fatal(err)
	}

	updated := &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{{Resources: []string{"vms"}, Verbs: []string{"get"}}}}

	s.failOn("update", "User", "carol", errRoleStore)

	changed, err := replaceUserRoles(users, old.Spec, updated)
	if diff := cmp.Diff([]string{"bob"}, changed); diff != "" {
		t.Errorf("replaceUserRoles() changed mismatch (-want +got):\n%s", diff)
	}

	var failed *userRolesError
	if !errors.As(err, &failed) || !errors.Is(err, errRoleStore) {
		t.Fatalf("replaceUserRoles() error = %v, want a *userRolesError wrapping %v", err, errRoleStore)
	}

	if diff := cmp.Diff([]string{"carol"}, failed.users); diff != "" {
		t.Errorf("replaceUserRoles() failed users mismatch (-want +got):\n%s", diff)
	}
}

// Giving a user a role needs what the role grants with the names given, as a
// role change does, beyond what the user already holds: a form saved as it is
// passes, and wider names or another role are refused before any change.
func TestUserRoleAssignmentRefusesEscalation(t *testing.T) {
	assigner := combinedRole(
		testRole(resourceUsers, "create"), testRole(resourceUsers, verbPatch, "bob"), testRole("users/roles", verbPatch, "bob"),
	)

	tests := map[string]struct {
		handler func(http.ResponseWriter, *http.Request) error
		method  string
		body    string
		want    int
		action  string
	}{
		"the role bob holds, saved again": {
			handler: UpdateUser, method: http.MethodPatch, want: http.StatusOK,
			body: `{"first_name":"Bob","role_name":"Lab","resource_names":["exp1","exp2/*"]}`,
		},
		"bob's role for every name": {
			handler: UpdateUser, method: http.MethodPatch, want: http.StatusForbidden, action: "user-role-change",
			body: `{"first_name":"Bob","role_name":"Lab","resource_names":[]}`,
		},
		"Global Admin for bob": {
			handler: UpdateUser, method: http.MethodPatch, want: http.StatusForbidden, action: "user-role-change",
			body: `{"first_name":"Bob","role_name":"Global Admin"}`,
		},
		"a new user with a role the requester does not hold": {
			handler: CreateUser, method: http.MethodPost, want: http.StatusForbidden, action: "user-create",
			body: createUserBody("frank", "Lab"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useRoleTestStore(t)
			events := captureAudit(t)
			bob := storedUserRole(t, "bob")

			req := roleRequest(tc.method, "/api/v1/users/bob", tc.body, map[string]string{"username": "bob"}, assigner)

			got, rec := serveHandler(tc.handler, req)
			if got.Status != tc.want {
				t.Fatalf("%s = %+v, want %d: %s", name, got, tc.want, rec.Body)
			}

			if tc.action == "" {
				return
			}

			wantAudit(t, events(), tc.action, "failure", map[string]any{"event.reason": reasonEscalation})

			if diff := cmp.Diff(bob, storedUserRole(t, "bob")); diff != "" {
				t.Errorf("%s changed bob's role (-want +got):\n%s", name, diff)
			}

			if u, err := rbac.GetUser("bob"); err != nil || u.FirstName() != "" {
				t.Errorf("%s changed bob's first name to %q (%v)", name, u.FirstName(), err)
			}

			if _, err := rbac.GetUser("frank"); err == nil {
				t.Errorf("%s created frank", name)
			}
		})
	}
}

// PATCH /roles/{name} with no policies narrows the role, and each user's copy,
// to nothing but the user's own users policy.
func TestUpdateRoleToNoPolicies(t *testing.T) {
	useRoleTestStore(t)

	vars := map[string]string{"name": "lab"}

	got, rec := serveHandler(UpdateRole, roleRequest(http.MethodPatch, "/roles/lab", `{"policies":[]}`, vars, adminRole()))
	if got.Status != http.StatusOK {
		t.Fatalf("PATCH /roles/lab with no policies = %+v, want 200: %s", got, rec.Body)
	}

	if stored, _ := rbac.RoleFromConfig("lab"); len(stored.Spec.Policies) != 0 {
		t.Errorf("stored policies = %v, want none", stored.Spec.Policies)
	}

	want := &v1.RoleSpec{Name: "Lab", Policies: []*v1.PolicySpec{
		{Resources: []string{resourceUsers}, ResourceNames: []string{"bob"}, Verbs: []string{verbGet, verbPatch}},
	}}
	if diff := cmp.Diff(want, storedUserRole(t, "bob")); diff != "" {
		t.Errorf("bob's copy of the role mismatch (-want +got):\n%s", diff)
	}
}

// The role routes name only the users that the requester may list, and
// otherwise give only how many users there are.
func TestRoleRoutesHideUsers(t *testing.T) {
	const body = `{"policies":[{"resources":["experiments"],"verbs":["get"]}]}`

	experimentsGet := testRole("experiments", "get", "*")

	for name, tc := range map[string]struct {
		lists []string
		want  []string
	}{
		"no users list":     {want: []string{}},
		"users list of bob": {lists: []string{"bob"}, want: []string{"bob"}},
	} {
		t.Run(name, func(t *testing.T) {
			useRoleTestStore(t)

			viewer := testRole(resourceUsers, "list", tc.lists...)
			if tc.lists == nil {
				viewer = testRole(resourceUsers, "get", "*")
			}

			patcher := combinedRole(testRole(resourceRoles, verbPatch, "lab"), experimentsGet, viewer)
			deleter := combinedRole(testRole(resourceRoles, "delete", "lab"), viewer)
			vars := map[string]string{"name": "lab"}

			got, rec := serveHandler(DeleteRole, roleRequest(http.MethodDelete, "/roles/lab", "", vars, deleter))
			if got.Status != http.StatusConflict {
				t.Fatalf("DELETE /roles/lab = %+v, want 409", got)
			}

			if diff := cmp.Diff(map[string]any{"users": anySlice(tc.want)}, errorDetails(t, rec)); diff != "" {
				t.Errorf("DELETE /roles/lab details mismatch (-want +got):\n%s", diff)
			}

			got, rec = serveHandler(UpdateRole, roleRequest(http.MethodPatch, "/roles/lab", body, vars, patcher))
			if got.Status != http.StatusOK {
				t.Fatalf("PATCH /roles/lab = %+v, want 200: %s", got, rec.Body)
			}

			var resp RoleUpdate
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}

			if resp.Count != 2 || !cmp.Equal(tc.want, resp.Users) {
				t.Errorf("PATCH /roles/lab users = %v, count %d, want %v, count 2", resp.Users, resp.Count, tc.want)
			}
		})
	}
}

// anySlice is names as JSON decodes them into an any.
func anySlice(names []string) []any {
	list := make([]any, len(names))
	for i, n := range names {
		list[i] = n
	}

	return list
}

// DELETE /roles/{name} refuses a role that a default user of ui.users or the
// development JWT key names, as phenix ui would give it again or use it.
func TestDeleteRoleNamedBySettings(t *testing.T) {
	for name, set := range map[string]func(*serverOptions){
		"a default user":             func(o *serverOptions) { o.users = []string{"bad", "zed:secret:Spare"} },
		"a default user's role name": func(o *serverOptions) { o.users = []string{"zed:secret:spare:exp1"} },
		"the development key":        func(o *serverOptions) { o.jwtKey = "dev|zed|spare" },
	} {
		t.Run(name, func(t *testing.T) {
			useRoleTestStore(t)

			original := o
			t.Cleanup(func() { o = original })

			set(&o)

			got, _ := serveHandler(
				DeleteRole,
				roleRequest(http.MethodDelete, "/roles/spare", "", map[string]string{"name": "spare"}, adminRole()),
			)
			if got.Status != http.StatusConflict || !strings.Contains(got.Message, "ui.") {
				t.Errorf("DELETE /roles/spare with %s naming it = %+v, want 409 naming the setting", name, got)
			}

			if _, err := rbac.RoleFromConfig("spare"); err != nil {
				t.Errorf("RoleFromConfig(spare) after the refused DELETE error = %v", err)
			}
		})
	}
}

// useBroker runs the websocket broker until the test ends, and serves its
// clients, each as the user its URL's user parameter names.
func useBroker(t *testing.T) func(user string) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		broker.Start(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	upgrader := websocket.Upgrader{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		broker.NewClient(adminRole(), r.URL.Query().Get("user"), conn).Go()
	}))
	t.Cleanup(srv.Close)

	return func(user string) *websocket.Conn {
		conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/?user="+user, nil)
		if err != nil {
			t.Fatalf("dialing the broker as %s: %v", user, err)
		}

		t.Cleanup(func() { _ = resp.Body.Close() })
		t.Cleanup(func() { _ = conn.Close() })

		return conn
	}
}

// closeCode reads conn until the broker closes it, and returns its close code,
// or 0 if it stays open for a second.
func closeCode(t *testing.T, conn *websocket.Conn) int {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))

	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}

		var closed *websocket.CloseError
		if errors.As(err, &closed) {
			return closed.Code
		}

		return 0
	}
}

// A role update, and a role change through PATCH /users/{username}, close
// the websockets of the users they change, which hold the old role.
func TestRoleChangesCloseWebsockets(t *testing.T) {
	for name, change := range map[string]func(t *testing.T){
		"a role update": func(t *testing.T) {
			t.Helper()

			body := `{"policies":[{"resources":["experiments"],"verbs":["get"]}]}`
			if got, _ := serveHandler(UpdateRole, roleRequest(http.MethodPatch, "/roles/lab", body, map[string]string{"name": "lab"}, adminRole())); got.Status != http.StatusOK {
				t.Fatalf("PATCH /roles/lab = %+v, want 200", got)
			}
		},
		"a user's new role": func(t *testing.T) {
			t.Helper()

			body := `{"role_name":"Spare"}`
			if got, _ := serveHandler(UpdateUser, roleRequest(http.MethodPatch, "/users/gina", body, map[string]string{"username": "gina"}, adminRole())); got.Status != http.StatusOK {
				t.Fatalf("PATCH /users/gina = %+v, want 200", got)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			useRoleTestStore(t)
			assignTestRole(t, "gina", "lab", "exp9")

			dial := useBroker(t)
			gina, henry := dial("gina"), dial("henry")

			polltest.Until(t, "the broker has both clients", func() bool { return broker.CurrentStats().Clients == 2 })

			change(t)

			if code := closeCode(t, gina); code != websocket.CloseServiceRestart {
				t.Errorf("gina's websocket close code = %d, want %d", code, websocket.CloseServiceRestart)
			}

			if code := closeCode(t, henry); code != 0 {
				t.Errorf("henry's websocket closed with %d, want it open", code)
			}
		})
	}
}
