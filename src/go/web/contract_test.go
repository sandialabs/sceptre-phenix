package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"

	"phenix/api/job"
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
