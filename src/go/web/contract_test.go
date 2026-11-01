package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// The API contract drives each contractRoutes route through phenix ui's router in each auth mode, with and without
// its permissions, behind a base path and with refused inputs; a new route adds a row there and its openapi.yml path.

// The experiment, VM and disk image of the contract fixture, and an
// experiment it does not have.
const (
	contractExp   = "test-experiment"
	contractVM    = "test-vm"
	contractDisk  = "test-disk.qc2"
	contractOther = "other-experiment"
)

// serveContractAs is serveContract with every request taken as one from alice
// with role, as dev authentication does.
func serveContractAs(t *testing.T, role rbac.Role) *httptest.Server {
	t.Helper()

	key := devKey(t, "alice", role)

	return serveContract(t, func(o *serverOptions) { o.jwtKey = key })
}

// contractFixture: running test-experiment with test-vm mounted (etc/hostname), the user bob, and on-demand consoles.
type contractFixture struct {
	consoles *consoleRegistry
}

// useContractFixture sets up the contract fixture until the test ends.
func useContractFixture(t *testing.T) contractFixture {
	t.Helper()

	useExperimentFilesDir(t, nil)
	useTestExperiment(t)
	useContractMount(t)

	// another user, for routes that act on a user other than the requester
	useTestStore(t, store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "User", Metadata: store.ConfigMetadata{Name: "bob"},
		Spec: map[string]any{"username": "bob", "rbac": map[string]any{"roleName": "test-role", "policies": []any{}}},
	})

	return contractFixture{consoles: useTestConsoles(t, time.Minute)}
}

// useContractMount mounts test-vm of test-experiment, with the file
// etc/hostname, under the files directory's parent until the test ends.
func useContractMount(t *testing.T) {
	t.Helper()

	original := common.MountBase
	common.MountBase = "" //nolint:reassign // mounts go under the temporary PhenixBase

	t.Cleanup(func() { common.MountBase = original }) //nolint:reassign // restore

	base := mm.GetLocalMountPath(contractExp, contractVM)
	hostname := filepath.Join(base, "etc", "hostname")

	if err := os.MkdirAll(filepath.Dir(hostname), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(hostname, []byte(contractVM), 0o600); err != nil {
		t.Fatal(err)
	}

	activeMountsMu.Lock()
	activeMounts[base] = &MountInfo{users: 1, lock: &sync.RWMutex{}}
	activeMountsMu.Unlock()

	t.Cleanup(func() {
		activeMountsMu.Lock()
		delete(activeMounts, base)
		activeMountsMu.Unlock()
	})
}

// console starts a console of user's, and returns its pid.
func (f contractFixture) console(t *testing.T, user string) string {
	t.Helper()

	console, err := f.consoles.start(context.WithoutCancel(t.Context()), user)
	if err != nil {
		t.Fatalf("starting a console of %s: %v", user, err)
	}

	return strconv.Itoa(console.pid)
}

// serveContract serves newRouter's routes over testPublic as phenix ui does,
// with the server options set changes. The options stay in effect until the
// test ends, as handlers read some per request.
func serveContract(t *testing.T, set func(*serverOptions)) *httptest.Server {
	t.Helper()

	original := o
	t.Cleanup(func() { o = original })

	o = newServerOptions()
	o.features = map[string]bool{"vm-mount": true, "tunneler-download": true}

	if set != nil {
		set(&o)
	}

	public := testPublic()

	router := newRouter(http.FS(public))

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return server
}

// contractRoute is one API route of the contract, as a request the fixture
// answers past RBAC.
type contractRoute struct {
	method string
	// path is the request's path and query; {console} stands for a console
	// of the user's
	path string
	body string
	// header is the request's headers, as name and value pairs
	header []string
	// need is each permission the route needs, one policy per role; none
	// for a route that acts only on the user's own things
	need []rbac.Role
	// hidden is the status a role without need gets when the route answers
	// as if the thing did not exist, rather than 403
	hidden int
	// want is the statuses the route answers once past authentication and
	// RBAC; when empty, anything but 401, 403 and its hidden status, short of
	// a server error
	want []int
}

func (c contractRoute) String() string { return c.method + " " + c.path }

// url is the route's URL on server for the fixture, when user sends it.
func (c contractRoute) url(t *testing.T, server string, f contractFixture, user string) string {
	t.Helper()

	path := c.path

	if strings.Contains(path, "{console}") {
		path = strings.ReplaceAll(path, "{console}", f.console(t, user))
	}

	return server + path
}

// passed reports whether the route answered past authentication and RBAC
// with status.
func (c contractRoute) passed(status int) bool {
	if len(c.want) > 0 {
		return slices.Contains(c.want, status)
	}

	return status != http.StatusUnauthorized && status != http.StatusForbidden &&
		(c.hidden == 0 || status != c.hidden) && status < http.StatusInternalServerError
}

// denied is the status the route answers a role without need.
func (c contractRoute) denied() int {
	if c.hidden != 0 {
		return c.hidden
	}

	return http.StatusForbidden
}

// contractRoutes are the API routes of the contract: the routes phenix ui
// gained on this branch, and those whose contract it changed.
func contractRoutes() []contractRoute {
	const (
		exp = "/api/v1/experiments/" + contractExp
		vm  = exp + "/vms/" + contractVM
	)

	var (
		diskGet = testRole("disks", "get", contractDisk)
		mount   = testRole("vms/mount", "get", contractExp+"/"+contractVM)
		one     = func(r rbac.Role) []rbac.Role { return []rbac.Role{r} }
		ok      = []int{http.StatusOK}
		done    = []int{http.StatusNoContent}
	)

	return []contractRoute{
		// VMs
		{method: "GET", path: vm + "/vnc/ws", need: one(testRole("vms/vnc", "get", contractExp+"/"+contractVM))},
		{method: "GET", path: vm + "/files/download?path=etc/hostname", need: one(mount), want: ok},
		{method: "HEAD", path: vm + "/files/download?path=etc/hostname", need: one(mount), want: ok},

		// disks
		{method: "GET", path: "/api/v1/disks/download?disk=" + contractDisk, need: one(diskGet)},
		{method: "HEAD", path: "/api/v1/disks/download?disk=" + contractDisk, need: one(diskGet)},

		// users: a token for another user also needs users/tokens create
		{
			method: "POST", path: "/api/v1/users/bob/tokens", body: `{"lifetime":"1h"}`, want: ok,
			need: []rbac.Role{testRole(resourceUsers, verbPatch, "bob"), testRole("users/tokens", "create", "bob")},
		},

		// configs
		{
			method: "GET", path: "/api/v1/configs/experiment/" + contractExp,
			need: one(testRole("configs", "get", "Experiment/"+contractExp)),
		},

		// consoles: a user reaches only their own
		{method: "GET", path: "/api/v1/console/{console}", need: one(testRole("miniconsole", "get")), want: ok},
		{method: "DELETE", path: "/api/v1/console/{console}", need: one(testRole("miniconsole", "post")), want: done},

		// state of health
		{method: "GET", path: "/api/v1/soh", need: one(testRole("experiments", "list"))},
	}
}

// Each route refuses a role missing any needed permission, or holding one only for another experiment, VM, disk or config,
// with 403 (or its hidden status) and its error body, and lets exactly those permissions past RBAC with a sequence number.
func TestContractRBAC(t *testing.T) {
	for _, route := range contractRoutes() {
		if len(route.need) == 0 {
			continue
		}

		t.Run(route.String(), func(t *testing.T) {
			denied := otherNames(route.need)

			for i := range route.need {
				rest := make([]rbac.Role, 0, len(route.need)-1)
				rest = append(rest, route.need[:i]...)
				rest = append(rest, route.need[i+1:]...)

				denied["without "+route.need[i].Spec.Policies[0].Resources[0]] = combinedRole(rest...)
			}

			for name, role := range denied {
				t.Run(name, func(t *testing.T) {
					f := useContractFixture(t)
					server := serveContractAs(t, role).URL

					url := route.url(t, server, f, "alice")

					resp := do(t, route.method, url, route.body, route.header...)
					if resp.StatusCode != route.denied() {
						t.Fatalf("%s %s = %d, want %d", route, name, resp.StatusCode, route.denied())
					}

					wantDocumentedBody(t, route.method, strings.TrimPrefix(url, server), resp)
					wantErrorBody(t, route, resp)
				})
			}

			t.Run("with exactly what it needs", func(t *testing.T) {
				f := useContractFixture(t)
				server := serveContractAs(t, combinedRole(route.need...)).URL

				url := route.url(t, server, f, "alice")

				resp := do(t, route.method, url, route.body, route.header...)
				if !route.passed(resp.StatusCode) {
					t.Errorf("%s with exactly what it needs = %d, want it past RBAC: %s", route, resp.StatusCode, readBody(t, resp))
				}

				wantDocumentedBody(t, route.method, strings.TrimPrefix(url, server), resp)

				// ordered against websocket messages, as every API route's
				if resp.Header.Get("X-Phenix-Seq") == "" {
					t.Errorf("%s with exactly what it needs: X-Phenix-Seq is missing", route)
				}
			})
		})
	}
}

// otherNames returns, for each role naming the fixture's things, the roles with its names changed to another experiment,
// disk or config, and, if it names test-vm, to another VM of the experiment; keyed by that role's resource and the change.
func otherNames(roles []rbac.Role) map[string]rbac.Role {
	changes := map[string]*strings.Replacer{
		"another experiment, disk or config": strings.NewReplacer(
			contractExp, contractOther, contractDisk, "other-disk.qc2",
		),
		"another VM": strings.NewReplacer(contractVM, "other-vm"),
	}

	other := map[string]rbac.Role{}

	for i, role := range roles {
		for change, replacer := range changes {
			renamed, ok := renameRole(role, replacer)
			if !ok {
				continue
			}

			all := slices.Clone(roles)
			all[i] = renamed

			other[role.Spec.Policies[0].Resources[0]+" for "+change] = combinedRole(all...)
		}
	}

	return other
}

// renameRole returns role with its policies' resource names changed by
// replacer, and whether any changed.
func renameRole(role rbac.Role, replacer *strings.Replacer) (rbac.Role, bool) {
	var (
		spec    = &v1.RoleSpec{}
		changed bool
	)

	for _, p := range role.Spec.Policies {
		copied := *p
		copied.ResourceNames = nil

		for _, name := range p.ResourceNames {
			renamed := replacer.Replace(name)
			changed = changed || renamed != name
			copied.ResourceNames = append(copied.ResourceNames, renamed)
		}

		spec.Policies = append(spec.Policies, &copied)
	}

	return rbac.Role{Spec: spec}, changed
}

// wantErrorBody fails the test unless resp has the JSON Error body, with a
// message and its status's code.
func wantErrorBody(t *testing.T, route contractRoute, resp *http.Response) {
	t.Helper()

	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("%s error: Content-Type = %q, want application/json", route, got)
	}

	if route.method == http.MethodHead {
		return
	}

	var body struct {
		Message string        `json:"message"`
		Code    weberror.Code `json:"code"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Errorf("%s error body is not JSON: %v", route, err)

		return
	}

	if body.Message == "" {
		t.Errorf("%s error body message = %q, want one", route, body.Message)
	}

	if want := weberror.CodeFor(resp.StatusCode); body.Code != want {
		t.Errorf("%s error body code = %q, want %q", route, body.Code, want)
	}
}

// useGlobalAdmin stores the global-admin role every request has with auth off and over the Unix socket;
// the server keeps that role for 30 seconds across tests, so every test stores this one.
func useGlobalAdmin(t *testing.T) {
	t.Helper()

	useTestStore(t, store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Role",
		Metadata: store.ConfigMetadata{Name: "global-admin"},
		Spec:     map[string]any{"roleName": "Global Admin", "policies": rolePolicies(adminRole())},
	})
}

// contractErrorCase is a request TestContractErrors makes and the status it wants.
type contractErrorCase struct {
	method, path, body string
	header             []string
	// prepare returns the path to request when it depends on the fixture
	prepare func(t *testing.T, f contractFixture) string
	want    int
}

const contractExpPath = "/api/v1/experiments/" + contractExp

// fileAndVMErrorCases are TestContractErrors' file, VM and console cases.
func fileAndVMErrorCases() map[string]contractErrorCase {
	const exp = contractExpPath

	bobsConsole := func(t *testing.T, f contractFixture) string {
		t.Helper()

		return "/api/v1/console/" + f.console(t, "bob")
	}

	return map[string]contractErrorCase{
		"unknown console":            {method: "GET", path: "/api/v1/console/1", want: http.StatusNotFound},
		"another user's console":     {method: "GET", prepare: bobsConsole, want: http.StatusNotFound},
		"end another user's console": {method: "DELETE", prepare: bobsConsole, want: http.StatusNotFound},
		"mount download of missing file": {
			method: "GET", path: exp + "/vms/" + contractVM + "/files/download?path=etc/none", want: http.StatusNotFound,
		},
		"mount download of directory": {
			method: "GET", path: exp + "/vms/" + contractVM + "/files/download?path=etc", want: http.StatusBadRequest,
		},
	}
}

// configAndRouteErrorCases are TestContractErrors' user and routing cases.
func configAndRouteErrorCases() map[string]contractErrorCase {
	const exp = contractExpPath

	return map[string]contractErrorCase{
		"stop with a force that is not a boolean": {
			method: "POST", path: exp + "/stop?force=maybe", want: http.StatusBadRequest,
		},
		"user with unknown role": {
			method: "POST", path: "/api/v1/users", body: createUserBody("carol", "No Such Role"), want: http.StatusBadRequest,
		},
		"schedule of running experiment": {
			method: "POST", path: exp + "/schedule", body: `{"algorithm":"round-robin"}`, want: http.StatusConflict,
		},
		"schedule of unknown experiment": {
			method: "POST", path: "/api/v1/experiments/none/schedule", body: `{"algorithm":"round-robin"}`,
			want: http.StatusNotFound,
		},
		"unknown API path":     {method: "GET", path: "/api/v1/no-such-route", want: http.StatusNotFound},
		"method SOH takes not": {method: "PATCH", path: "/api/v1/soh", want: http.StatusMethodNotAllowed},
		"VM restart by GET":    {method: "GET", path: exp + "/vms/" + contractVM + "/restart", want: http.StatusMethodNotAllowed},
		"VM shutdown by GET":   {method: "GET", path: exp + "/vms/" + contractVM + "/shutdown", want: http.StatusMethodNotAllowed},
		"VM disk reset by GET": {method: "GET", path: exp + "/vms/" + contractVM + "/reset", want: http.StatusMethodNotAllowed},
		"logout by GET":        {method: "GET", path: "/api/v1/logout", want: http.StatusMethodNotAllowed},
	}
}

// Refusals get openapi.yml's status for the route (405 and unknown paths' 404 in its errors text) and the JSON Error body:
// unknown things and others' consoles 404, bad input 400.
func TestContractErrors(t *testing.T) {
	spec := loadOpenAPI(t)

	for _, cases := range []map[string]contractErrorCase{fileAndVMErrorCases(), configAndRouteErrorCases()} {
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) { checkContractError(t, spec, name, tc) })
		}
	}
}

// checkContractError makes tc's request as an admin and checks its status and body.
func checkContractError(t *testing.T, spec openAPISpec, name string, tc contractErrorCase) {
	t.Helper()

	f := useContractFixture(t)
	server := serveContractAs(t, adminRole()).URL

	path := tc.path
	if tc.prepare != nil {
		path = tc.prepare(t, f)
	}

	resp := do(t, tc.method, server+path, tc.body, tc.header...)
	if resp.StatusCode != tc.want {
		t.Fatalf("%s %s = %d, want %d: %s", tc.method, path, resp.StatusCode, tc.want, readBody(t, resp))
	}

	// openapi.yml's errors text documents these for every path
	everyPath := name == "unknown API path" || tc.want == http.StatusMethodNotAllowed

	if !everyPath {
		wantDocumentedBody(t, tc.method, path, resp)
	}

	wantErrorBody(t, contractRoute{method: tc.method, path: path}, resp)

	if !everyPath && !spec.documents(tc.method, path, tc.want) {
		t.Errorf("openapi.yml documents %d for %s %s = false, want true", tc.want, tc.method, path)
	}
}

// The routes that answer JSON say so in Content-Type, as openapi.yml
// documents, rather than leaving the server to sniff text/plain.
func TestContractJSONContentType(t *testing.T) {
	const (
		exp = "/api/v1/experiments/" + contractExp
		vm  = exp + "/vms/" + contractVM
	)

	for _, path := range []string{
		exp,
		exp + "/schedule",
		exp + "/captures",
		exp + "/topology/search?hostname=" + contractVM,
		exp + "/vms",
		vm,
		vm + "/captures",
		exp + "/scorch/terminals",
		"/api/v1/vms",
		"/api/v1/applications",
		"/api/v1/topologies",
		"/api/v1/users",
		"/api/v1/roles",
	} {
		t.Run(path, func(t *testing.T) {
			useContractFixture(t)
			server := serveContractAs(t, adminRole()).URL

			resp := do(t, http.MethodGet, server+path, "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %d, want %d: %s", path, resp.StatusCode, http.StatusOK, readBody(t, resp))
			}

			if got := resp.Header.Get("Content-Type"); got != mimeJSON {
				t.Errorf("GET %s Content-Type = %q, want %s", path, got, mimeJSON)
			}

			wantDocumentedBody(t, http.MethodGet, path, resp)
		})
	}
}

// Routes outside API auth answer without credentials, with their own statuses: features, version and tunneler
// downloads.
func TestContractPublicRoutes(t *testing.T) {
	useContractFixture(t)

	server := serveContract(t, func(o *serverOptions) { o.jwtKey = "test-signing-key" }).URL

	for path, tc := range map[string]struct {
		want        []int
		contentType string
	}{
		"/features": {[]int{http.StatusOK}, "application/json"},
		"/version":  {[]int{http.StatusOK}, "application/json"},
		// the tunneler builds there are to download
		"/downloads/tunneler": {[]int{http.StatusOK}, "application/json"},
	} {
		t.Run(path, func(t *testing.T) {
			resp := do(t, http.MethodGet, server+path, "")
			if !slices.Contains(tc.want, resp.StatusCode) {
				t.Errorf("GET %s without credentials = %d, want one of %v: %s", path, resp.StatusCode, tc.want, readBody(t, resp))
			}

			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
				t.Errorf("GET %s Content-Type = %q, want %s", path, got, tc.contentType)
			}
		})
	}
}
