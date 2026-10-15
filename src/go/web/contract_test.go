package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"

	"phenix/api/job"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

// The API contract: each route contractRoutes lists is driven through the
// router phenix ui serves, in each auth mode, with and without the
// permissions it needs, behind a base path, and with the inputs it refuses.
// A new API route adds a row there, and its path to openapi.yml, which
// TestRouterMatchesOpenAPI checks.

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
func serveContractAs(t *testing.T, role rbac.Role, set func(*serverOptions)) *httptest.Server {
	t.Helper()

	key := devKey(t, "alice", role)

	return serveContract(t, func(o *serverOptions) {
		o.jwtKey = key

		if set != nil {
			set(o)
		}
	})
}

// contractFixture is what the contract's routes act on: test-experiment,
// running, with test-vm and the files a.log and a.pcap; the disk image
// test-disk.qc2; and bob's job on test-experiment, finished, with a 3-byte
// artifact. minimega is the fake, answering every command with an empty
// success.
type contractFixture struct {
	jobs    *job.Registry
	bobsJob job.Job
}

// useContractFixture sets up the contract fixture until the test ends.
func useContractFixture(t *testing.T) contractFixture {
	t.Helper()

	files := useDiskFilesDir(t)
	writeDisks(t, filepath.Join(files.dir, contractDisk))

	useTestExperiment(t)
	useTestFiles(t, "a.log", "a.pcap")

	expFiles := filepath.Join(files.dir, contractExp, "files")
	if err := os.MkdirAll(expFiles, 0o750); err != nil {
		t.Fatal(err)
	}

	for name, contents := range map[string]string{"a.log": "alpha", "a.pcap": string(testPcapHeader())} {
		if err := os.WriteFile(filepath.Join(expFiles, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	useCaptureGrants(t)

	registry := useJobs(t)
	bobs := waitJob(t, registry, startTestJob(t, registry, "bob", contractExp, nil, "zip").ID)

	return contractFixture{jobs: registry, bobsJob: bobs}
}

// serveContract serves newRouter's routes over testPublic as phenix ui does,
// with WebShark installed and the server options set changes. The options
// stay in effect until the test ends, as handlers read some per request.
func serveContract(t *testing.T, set func(*serverOptions)) *httptest.Server {
	t.Helper()

	original := o
	t.Cleanup(func() { o = original })

	o = newServerOptions()
	o.features = map[string]bool{"webshark": true}
	o.websharkDir = t.TempDir()

	if set != nil {
		set(&o)
	}

	public := testPublic()

	router, err := newRouter(http.FS(public), public)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return server
}

// contractRoute is one API route of the contract, as a request the fixture
// answers past RBAC.
type contractRoute struct {
	method string
	// path is the request's path and query; {job} stands for bob's job
	path string
	body string
	// need is each permission the route needs, one policy per role; none
	// for a route that acts only on the user's own things
	need []rbac.Role
	// hidden is the status a role without need gets when the route answers
	// as if the thing did not exist, rather than 403
	hidden int
	// jsonErrors is set for a route that answers errors as weberror JSON
	// rather than text
	jsonErrors bool
}

func (c contractRoute) String() string { return c.method + " " + c.path }

// url is the route's URL on server for the fixture.
func (c contractRoute) url(server string, f contractFixture) string {
	return server + strings.ReplaceAll(c.path, "{job}", f.bobsJob.ID)
}

// passed reports whether the route answered past authentication and RBAC
// with status: anything but 401, 403 and its hidden status, short of a
// server error.
func (c contractRoute) passed(status int) bool {
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
		exp   = "/api/v1/experiments/" + contractExp
		vm    = exp + "/vms/" + contractVM
		files = `{"paths":["a.log"]}`
	)

	var (
		filesGet = testRole("experiments/files", "get", contractExp)
		captures = testRole("vms/captures", "list", contractExp+"/"+contractVM)
		trigger  = testRole("experiments/trigger", "create", contractExp)
		diskGet  = testRole("disks", "get", contractDisk)
		one      = func(r rbac.Role) []rbac.Role { return []rbac.Role{r} }
		jobs     = func(verb string) []rbac.Role { return one(testRole("jobs", verb, contractExp)) }
	)

	return []contractRoute{
		// jobs: another user's job is hidden without a jobs permission on
		// its experiment; GET /jobs lists only what the role may see
		{method: "GET", path: "/api/v1/jobs"},
		{method: "GET", path: "/api/v1/jobs/{job}", need: jobs("get"), hidden: http.StatusNotFound},
		{method: "POST", path: "/api/v1/jobs/{job}/cancel", need: jobs("update"), hidden: http.StatusNotFound},
		{method: "DELETE", path: "/api/v1/jobs/{job}", need: jobs("delete"), hidden: http.StatusNotFound},
		{method: "GET", path: "/api/v1/jobs/{job}/artifact", need: jobs("get"), hidden: http.StatusNotFound},
		{method: "HEAD", path: "/api/v1/jobs/{job}/artifact", need: jobs("get"), hidden: http.StatusNotFound},

		// captures and WebShark
		{method: "GET", path: exp + "/capture-tokens"},
		{method: "POST", path: exp + "/capture-tokens", body: `{"operations":["view"],"path":"a.pcap"}`, need: one(filesGet)},
		{method: "DELETE", path: exp + "/capture-tokens/none"},
		{method: "POST", path: exp + "/webshark", body: `{"path":"a.pcap"}`, need: one(filesGet)},
		{method: "GET", path: vm + "/captures/0/stream", need: []rbac.Role{captures, filesGet}},

		// experiment files
		{method: "GET", path: exp + "/files/a.log?path=a.log", need: one(filesGet)},
		{method: "HEAD", path: exp + "/files/a.log?path=a.log", need: one(filesGet)},
		{method: "GET", path: exp + "/files/a.log/checksum?path=a.log", need: one(filesGet)},
		{method: "POST", path: exp + "/files/archive", body: files, need: one(filesGet)},
		{method: "POST", path: exp + "/files/download", body: files, need: one(filesGet)},
		{
			method: "POST", path: exp + "/files/delete", body: files,
			need: one(testRole("experiments/files", "delete", contractExp)),
		},

		// SCORCH
		{
			method: "POST", path: exp + "/scorch/pipelines/0/cleanup", jsonErrors: true,
			need: []rbac.Role{trigger, testRole("experiments", "get", contractExp)},
		},
		{method: "POST", path: exp + "/scorch/pipelines/0/clear", need: one(trigger), jsonErrors: true},

		// VMs
		{
			method: "POST", path: exp + "/vms/actions", body: `{"action":"start","vms":["test-vm"]}`,
			need: one(testRole("vms/start", "update", contractExp+"/"+contractVM)),
		},
		{method: "GET", path: vm + "/vnc/ws", need: one(testRole("vms/vnc", "get", contractExp+"/"+contractVM))},

		// disks
		{method: "GET", path: "/api/v1/disks/checksum?disk=" + contractDisk, need: one(diskGet)},
		{method: "GET", path: "/api/v1/disks/download?disk=" + contractDisk, need: one(diskGet)},
		{method: "HEAD", path: "/api/v1/disks/download?disk=" + contractDisk, need: one(diskGet)},
		{
			method: "POST", path: "/api/v1/disks/clone/jobs?disk=" + contractDisk + "&new=copy",
			need: one(testRole("disks", "create", "copy.qcow2")),
		},

		// consoles: another user's console is hidden whatever the role
		{method: "GET", path: "/api/v1/console/1", need: one(testRole("miniconsole", "get"))},
		{method: "DELETE", path: "/api/v1/console/1", need: one(testRole("miniconsole", "post"))},

		// state of health and configs
		{method: "GET", path: "/api/v1/soh", need: one(testRole("experiments", "list"))},
		{
			method: "GET", path: "/api/v1/configs/experiment/" + contractExp, jsonErrors: true,
			need: one(testRole("configs", "get", "Experiment/"+contractExp)),
		},
	}
}

// signedJWT is a session JWT for user, signed with key, that expires at exp.
func signedJWT(t *testing.T, user, key string, exp time.Time) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		jwtClaimSub: user,
		jwtClaimExp: exp.Unix(),
	}).SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}

	return signed
}

// Each route needs a session of a user who still holds it: without a token
// it answers 403, and with a token it cannot trust 401. Behind a proxy that
// names its user (--proxy-auth-header), the proxy must name the token's.
func TestContractAuth(t *testing.T) {
	bearer := func(token string) []string { return []string{"X-Phenix-Auth-Token", "Bearer " + token} }

	type request struct {
		proxy  bool // the server takes the user from X-Remote-User
		send   func(t *testing.T, route contractRoute, url, session string) *http.Response
		refuse int // the status it is refused with, or 0 when it passes
	}

	session := func(_ *testing.T, session string) []string { return bearer(session) }

	header := func(h func(t *testing.T, session string) []string) func(*testing.T, contractRoute, string, string) *http.Response {
		return func(t *testing.T, route contractRoute, url, session string) *http.Response {
			t.Helper()

			return do(t, route.method, url, route.body, h(t, session)...)
		}
	}

	requests := map[string]request{
		"no token": {false, header(func(*testing.T, string) []string { return nil }), http.StatusForbidden},
		"malformed token": {false, header(func(*testing.T, string) []string {
			return bearer("not.a.jwt")
		}), http.StatusUnauthorized},
		"not a bearer token": {false, header(func(_ *testing.T, session string) []string {
			return []string{"X-Phenix-Auth-Token", session}
		}), http.StatusUnauthorized},
		"token of another key": {false, header(func(t *testing.T, _ string) []string {
			t.Helper()

			return bearer(signedJWT(t, "alice", "another-key", time.Now().Add(time.Hour)))
		}), http.StatusUnauthorized},
		"expired token": {false, header(func(t *testing.T, _ string) []string {
			t.Helper()

			return bearer(signedJWT(t, "alice", testJWTKey, time.Now().Add(-time.Minute)))
		}), http.StatusUnauthorized},
		"signed-out token": {false, header(func(t *testing.T, _ string) []string {
			t.Helper()

			return bearer(signedJWT(t, "alice", testJWTKey, time.Now().Add(2*time.Hour)))
		}), http.StatusUnauthorized},
		"unknown user": {false, header(func(t *testing.T, _ string) []string {
			t.Helper()

			return bearer(signedJWT(t, "mallory", testJWTKey, time.Now().Add(time.Hour)))
		}), http.StatusUnauthorized},
		"another proxy user": {true, header(func(_ *testing.T, session string) []string {
			return append(bearer(session), "X-Remote-User", "bob")
		}), http.StatusUnauthorized},
		"no proxy user": {true, header(session), http.StatusUnauthorized},
		"token header":  {false, header(session), 0},
		"token parameter": {false, func(t *testing.T, route contractRoute, url, session string) *http.Response {
			t.Helper()

			sep := "?"
			if strings.Contains(url, "?") {
				sep = "&"
			}

			return do(t, route.method, url+sep+"token="+session, route.body)
		}, 0},
		"the token's proxy user": {true, header(func(_ *testing.T, session string) []string {
			return append(bearer(session), "X-Remote-User", "alice")
		}), 0},
	}

	for _, route := range contractRoutes() {
		t.Run(route.String(), func(t *testing.T) {
			for name, req := range requests {
				t.Run(name, func(t *testing.T) {
					f := useContractFixture(t)
					session := useTestUser(t, combinedRole(route.need...))
					server := serveContract(t, func(o *serverOptions) {
						o.jwtKey = testJWTKey
						if req.proxy {
							o.proxyAuthHeader = "X-Remote-User"
						}
					}).URL

					resp := req.send(t, route, route.url(server, f), session)

					switch {
					case req.refuse == 0:
						if !route.passed(resp.StatusCode) {
							t.Fatalf("status %d: %s", resp.StatusCode, readBody(t, resp))
						}
					case resp.StatusCode != req.refuse:
						t.Fatalf("status %d, want %d", resp.StatusCode, req.refuse)
					case !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain"):
						t.Errorf("Content-Type %q, want text/plain", resp.Header.Get("Content-Type"))
					}
				})
			}
		})
	}
}

// Each route refuses a role that lacks any one of the permissions it needs,
// or has them for another experiment or disk, with 403 (or the route's
// hidden status) and the route's error body, and lets a role with exactly
// those permissions past RBAC, with the response's sequence number.
func TestContractRBAC(t *testing.T) {
	for _, route := range contractRoutes() {
		if len(route.need) == 0 {
			continue
		}

		t.Run(route.String(), func(t *testing.T) {
			denied := map[string]rbac.Role{}

			for i := range route.need {
				rest := make([]rbac.Role, 0, len(route.need)-1)
				rest = append(rest, route.need[:i]...)
				rest = append(rest, route.need[i+1:]...)

				denied["without "+route.need[i].Spec.Policies[0].Resources[0]] = combinedRole(rest...)
			}

			if other, ok := forOtherNames(route.need); ok {
				denied["for another experiment or disk"] = other
			}

			for name, role := range denied {
				t.Run(name, func(t *testing.T) {
					f := useContractFixture(t)
					server := serveContractAs(t, role, nil).URL

					resp := do(t, route.method, route.url(server, f), route.body)
					if resp.StatusCode != route.denied() {
						t.Fatalf("status %d, want %d", resp.StatusCode, route.denied())
					}

					wantErrorBody(t, route, resp)
				})
			}

			t.Run("with exactly what it needs", func(t *testing.T) {
				f := useContractFixture(t)
				server := serveContractAs(t, combinedRole(route.need...), nil).URL

				resp := do(t, route.method, route.url(server, f), route.body)
				if !route.passed(resp.StatusCode) {
					t.Fatalf("status %d: %s", resp.StatusCode, readBody(t, resp))
				}

				// ordered against websocket messages, as every API route's
				if resp.Header.Get("X-Phenix-Seq") == "" {
					t.Error("no X-Phenix-Seq")
				}
			})
		})
	}
}

// forOtherNames returns the roles' policies with their resource names
// changed from the fixture's experiment and disk images to others, and
// whether any named one.
func forOtherNames(roles []rbac.Role) (rbac.Role, bool) {
	replacer := strings.NewReplacer(contractExp, contractOther, contractDisk, "other-disk.qc2", "copy.qcow2", "other.qcow2")

	var (
		other = combinedRole(roles...)
		named bool
	)

	for i, p := range other.Spec.Policies {
		copied := *p
		copied.ResourceNames = nil

		for _, name := range p.ResourceNames {
			copied.ResourceNames = append(copied.ResourceNames, replacer.Replace(name))
			named = true
		}

		other.Spec.Policies[i] = &copied
	}

	return other, named
}

// wantErrorBody fails the test unless resp has the route's error body: text,
// or a weberror's JSON with a message.
func wantErrorBody(t *testing.T, route contractRoute, resp *http.Response) {
	t.Helper()

	got := resp.Header.Get("Content-Type")

	if !route.jsonErrors {
		if !strings.HasPrefix(got, "text/plain") {
			t.Errorf("Content-Type %q, want text/plain", got)
		}

		return
	}

	if got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}

	if route.method == http.MethodHead {
		return
	}

	var body struct {
		Message string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Message == "" {
		t.Errorf("error body has no message: %v", err)
	}
}

// GET /jobs lists another user's job only for a role that may list the jobs
// of its experiment.
func TestContractJobListing(t *testing.T) {
	for name, tc := range map[string]struct {
		role rbac.Role
		want bool
	}{
		"jobs list":                     {testRole("jobs", "list", contractExp), true},
		"jobs list on other experiment": {testRole("jobs", "list", contractOther), false},
		"jobs get":                      {testRole("jobs", "get", contractExp), false},
	} {
		t.Run(name, func(t *testing.T) {
			f := useContractFixture(t)
			server := serveContractAs(t, tc.role, nil).URL

			resp := do(t, http.MethodGet, server+"/api/v1/jobs", "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}

			var body struct {
				Jobs []job.Job `json:"jobs"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}

			listed := len(body.Jobs) == 1 && body.Jobs[0].ID == f.bobsJob.ID
			if listed != tc.want || len(body.Jobs) > 1 {
				t.Errorf("listed %+v, want bob's job listed %t", body.Jobs, tc.want)
			}
		})
	}
}

// contractAdmin may do anything, as Global Admin may.
func contractAdmin() rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{{
		Resources: []string{"*", "*/*"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"*"},
	}}}}
}

// The routes answer the requests they refuse with the documented status and
// body: unknown things 404, methods a path does not take 405, bad input 400,
// conflicts 409 and unsatisfiable ranges 416, as text or a weberror's JSON.
// Each status is one openapi.yml documents for the route, or, for the 404 of
// an unknown path and the 405, in its description of errors.
func TestContractErrors(t *testing.T) {
	const exp = "/api/v1/experiments/" + contractExp

	type errorCase struct {
		method, path, body string
		header             []string
		// prepare returns the path to request when it depends on the fixture
		prepare func(t *testing.T, f contractFixture) string
		want    int
		json    bool // a weberror's JSON rather than text
	}

	// a job of alice's that runs until the test ends
	running := func(t *testing.T, f contractFixture) string {
		t.Helper()

		release := make(chan struct{})
		t.Cleanup(func() { close(release) })

		return "/api/v1/jobs/" + startTestJob(t, f.jobs, "alice", contractExp, release, "").ID
	}

	bobs := func(suffix string) func(*testing.T, contractFixture) string {
		return func(_ *testing.T, f contractFixture) string { return "/api/v1/jobs/" + f.bobsJob.ID + suffix }
	}

	cases := map[string]errorCase{
		"unknown job":             {method: "GET", path: "/api/v1/jobs/none", want: http.StatusNotFound},
		"cancel unknown job":      {method: "POST", path: "/api/v1/jobs/none/cancel", want: http.StatusNotFound},
		"dismiss unknown job":     {method: "DELETE", path: "/api/v1/jobs/none", want: http.StatusNotFound},
		"artifact of unknown job": {method: "GET", path: "/api/v1/jobs/none/artifact", want: http.StatusNotFound},
		"cancel finished job":     {method: "POST", prepare: bobs("/cancel"), want: http.StatusConflict},
		"dismiss running job":     {method: "DELETE", prepare: running, want: http.StatusConflict},
		"artifact range past its end": {
			method: "GET", prepare: bobs("/artifact"), header: []string{"Range", "bytes=10-"},
			want: http.StatusRequestedRangeNotSatisfiable,
		},
		"artifact of job without one": {
			method: "GET", want: http.StatusNotFound,
			prepare: func(t *testing.T, f contractFixture) string {
				t.Helper()

				return "/api/v1/jobs/" + waitJob(t, f.jobs, startTestJob(t, f.jobs, "alice", "", nil, "").ID).ID + "/artifact"
			},
		},
		"capture token of malformed body": {method: "POST", path: exp + "/capture-tokens", body: "{", want: http.StatusBadRequest},
		"capture token of unknown operation": {
			method: "POST", path: exp + "/capture-tokens", body: `{"operations":["fly"],"path":"a.pcap"}`,
			want: http.StatusBadRequest,
		},
		"stream token of saved capture": {
			method: "POST", path: exp + "/capture-tokens", body: `{"operations":["stream"],"path":"a.pcap"}`,
			want: http.StatusBadRequest,
		},
		"revoke unknown capture token": {method: "DELETE", path: exp + "/capture-tokens/none", want: http.StatusNotFound},
		"WebShark without capture":     {method: "POST", path: exp + "/webshark", body: "{}", want: http.StatusBadRequest},
		"WebShark of non-capture":      {method: "POST", path: exp + "/webshark", body: `{"path":"a.log"}`, want: http.StatusBadRequest},
		"WebShark of missing capture": {
			method: "POST", path: exp + "/webshark", body: `{"path":"none.pcap"}`, want: http.StatusNotFound,
		},
		"stream of VM without capture": {
			method: "GET", path: exp + "/vms/" + contractVM + "/captures/0/stream", want: http.StatusNotFound,
		},
		"checksum of missing file": {
			method: "GET", path: exp + "/files/none.log/checksum?path=none.log", want: http.StatusNotFound,
		},
		"archive of no files": {method: "POST", path: exp + "/files/archive", body: "{}", want: http.StatusBadRequest},
		"archive of missing file": {
			method: "POST",
			path:   exp + "/files/archive",
			body:   `{"paths":["none.log"]}`,
			want:   http.StatusNotFound,
		},
		"VM action unknown": {
			method: "POST", path: exp + "/vms/actions", body: `{"action":"fly","vms":["test-vm"]}`, want: http.StatusBadRequest,
		},
		"VM action of no VMs": {
			method: "POST", path: exp + "/vms/actions", body: `{"action":"start","vms":[]}`, want: http.StatusBadRequest,
		},
		"checksum of missing disk": {method: "GET", path: "/api/v1/disks/checksum?disk=none.qc2", want: http.StatusNotFound},
		"checksum of disk outside": {method: "GET", path: "/api/v1/disks/checksum?disk=../none.qc2", want: http.StatusBadRequest},
		"clone onto existing disk": {
			method: "POST", path: "/api/v1/disks/clone/jobs?disk=" + contractDisk + "&new=" + contractDisk,
			want: http.StatusConflict,
		},
		"unknown console": {method: "GET", path: "/api/v1/console/1", want: http.StatusNotFound},
		"clear run that is not a number": {
			method: "POST", path: exp + "/scorch/pipelines/x/clear", want: http.StatusBadRequest, json: true,
		},
		"clear run the experiment lacks": {
			method: "POST", path: exp + "/scorch/pipelines/0/clear", want: http.StatusNotFound, json: true,
		},
		"clean up run the experiment lacks": {
			method: "POST", path: exp + "/scorch/pipelines/0/cleanup", want: http.StatusBadRequest, json: true,
		},
		"unknown API path":       {method: "GET", path: "/api/v1/no-such-route", want: http.StatusNotFound},
		"method a job takes not": {method: "PUT", prepare: bobs(""), want: http.StatusMethodNotAllowed},
		"method SOH takes not":   {method: "PATCH", path: "/api/v1/soh", want: http.StatusMethodNotAllowed},
	}

	spec := loadOpenAPI(t)

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := useContractFixture(t)
			server := serveContractAs(t, contractAdmin(), nil).URL

			path := tc.path
			if tc.prepare != nil {
				path = tc.prepare(t, f)
			}

			resp := do(t, tc.method, server+path, tc.body, tc.header...)
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.want, readBody(t, resp))
			}

			// gorilla's 405 has no body
			if tc.want == http.StatusMethodNotAllowed {
				return
			}

			wantErrorBody(t, contractRoute{method: tc.method, jsonErrors: tc.json}, resp)

			if name == "unknown API path" {
				return
			}

			if !spec.documents(tc.method, path, tc.want) {
				t.Errorf("openapi.yml does not document %d for %s %s", tc.want, tc.method, path)
			}
		})
	}
}

// Behind a proxy that serves phenix under a base path, and strips it from
// requests, the URLs the server gives clients reach it through the proxy:
// job locations carry the base path, capture token URLs are relative to it,
// and WebShark's pages name it.
func TestContractBasePath(t *testing.T) {
	const base = "/phenix/"

	useContractFixture(t)
	server := serveContractAs(t, contractAdmin(), func(o *serverOptions) {
		o.basePath = base

		page := `<html><head><base href="/webshark/"></head></html>`
		if err := os.WriteFile(filepath.Join(o.websharkDir, "index.html"), []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}).URL

	// through the proxy, which strips the base path
	proxied := func(t *testing.T, method, url string) *http.Response {
		t.Helper()

		rest, ok := strings.CutPrefix(url, base)
		if !ok {
			t.Fatalf("%s is not under the base path %s", url, base)
		}

		return do(t, method, server+"/"+rest, "")
	}

	const exp = "/api/v1/experiments/" + contractExp

	for _, start := range []struct{ path, body string }{
		{exp + "/files/archive", `{"paths":["a.log"]}`},
		{exp + "/vms/actions", `{"action":"start","vms":["test-vm"]}`},
		{"/api/v1/disks/clone/jobs?disk=" + contractDisk + "&new=copy", ""},
	} {
		resp := do(t, http.MethodPost, server+start.path, start.body)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST %s: status %d: %s", start.path, resp.StatusCode, readBody(t, resp))
		}

		var started job.Job
		if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
			t.Fatal(err)
		}

		location := resp.Header.Get("Location")
		if want := base + "api/v1/jobs/" + started.ID; location != want {
			t.Errorf("POST %s: Location %q, want %q", start.path, location, want)

			continue
		}

		if got := proxied(t, http.MethodGet, location); got.StatusCode != http.StatusOK {
			t.Errorf("GET %s through the proxy: status %d", location, got.StatusCode)
		}
	}

	resp := do(t, http.MethodPost, server+exp+"/capture-tokens", `{"operations":["view"],"path":"a.pcap"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("giving a capture token: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var token captureToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}

	if view := token.URLs["view"]; !strings.HasPrefix(view, "webshark/") {
		t.Errorf("capture token view URL %q is not relative to the base path", view)
	}

	if page := readBody(t, proxied(t, http.MethodGet, base+"webshark/")); !strings.Contains(page, `"`+base+`webshark/"`) {
		t.Errorf("WebShark's page does not name the base path: %s", page)
	}
}

// The routes outside the API's authentication answer without credentials,
// with their own statuses: health and features, and the WebShark routes,
// whose capture name or stream token is their credential.
func TestContractPublicRoutes(t *testing.T) {
	useContractFixture(t)

	server := serveContract(t, func(o *serverOptions) { o.jwtKey = testJWTKey }).URL

	for path, tc := range map[string]struct {
		want        []int
		contentType string
	}{
		"/healthz":  {[]int{http.StatusOK}, "application/health+json"},
		"/readyz":   {[]int{http.StatusOK, http.StatusServiceUnavailable}, "application/health+json"},
		"/features": {[]int{http.StatusOK}, "application/json"},
		"/version":  {[]int{http.StatusOK}, "application/json"},
		// a capture name or stream token that is not one
		"/webshark/streams/pcs_none": {[]int{http.StatusNotFound}, "text/plain"},
		// WebShark's own requests, answered as WebShark's server would
		"/webshark/captures/none.pcap":                {[]int{http.StatusNotFound}, "application/json"},
		"/webshark/json?method=download&capture=none": {[]int{http.StatusNotFound}, "application/json"},
		"/webshark/watch?capture=none":                {[]int{http.StatusNotFound}, "application/json"},
		"/webshark/json?method=files":                 {[]int{http.StatusOK}, "application/json"},
	} {
		t.Run(path, func(t *testing.T) {
			resp := do(t, http.MethodGet, server+path, "")
			if !slices.Contains(tc.want, resp.StatusCode) {
				t.Fatalf("status %d, want one of %v: %s", resp.StatusCode, tc.want, readBody(t, resp))
			}

			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
				t.Errorf("Content-Type %q, want %s", got, tc.contentType)
			}
		})
	}
}
