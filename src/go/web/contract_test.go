package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"

	"phenix/api/job"
	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/web/capability"
	"phenix/web/rbac"
)

// The API contract: each route contractRoutes lists is driven through the
// router phenix ui serves, in each auth mode, with and without the
// permissions it needs, behind a base path, and with the inputs it refuses.
// A new API route adds a row there, and its path to openapi.yml, which
// TestRouterMatchesOpenAPI checks.

// The experiment, VM, disk image and topology of the contract fixture, the
// topology's revision, and an experiment it does not have.
const (
	contractExp      = "test-experiment"
	contractVM       = "test-vm"
	contractDisk     = "test-disk.qc2"
	contractTopo     = "demo"
	contractRevision = 5
	contractOther    = "other-experiment"
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
// running, with test-vm, mounted with the file etc/hostname, and the files
// a.log and a.pcap; the disk image test-disk.qc2; the demo topology at
// revision 5; bob's job on test-experiment, finished, with a 3-byte
// artifact; and, made on demand, each user's capture token and console.
// minimega is the fake, answering every command with an empty success.
type contractFixture struct {
	jobs     *job.Registry
	bobsJob  job.Job
	consoles *consoleRegistry
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
	useContractMount(t)

	topology, err := store.NewConfigFromJSON([]byte(revisionTestTopology))
	if err != nil {
		t.Fatal(err)
	}

	topology.Metadata.Revision = contractRevision
	useTestStore(t, *topology)

	registry := useJobs(t)
	bobs := waitJob(t, registry, startTestJob(t, registry, "bob", contractExp, nil, "zip").ID)

	return contractFixture{jobs: registry, bobsJob: bobs, consoles: useTestConsoles(t, time.Minute)}
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

// captureToken gives user a capture token for a.pcap, and returns its ID.
func (f contractFixture) captureToken(user string) string {
	return captureGrants.Mint("contract-"+user+".pcap", capability.Grant{
		User: user, Target: capability.Target{Exp: contractExp, File: "a.pcap"},
		Ops: viewOps, Expires: time.Now().Add(time.Hour),
	}).ID
}

// console starts a console of user's, and returns its pid.
func (f contractFixture) console(t *testing.T, user string) string {
	t.Helper()

	console, err := f.consoles.start(context.WithoutCancel(t.Context()), user)
	if err != nil {
		t.Fatal(err)
	}

	return strconv.Itoa(console.pid)
}

// serveContract serves newRouter's routes over testPublic as phenix ui does,
// with WebShark installed and the server options set changes. The options
// stay in effect until the test ends, as handlers read some per request.
func serveContract(t *testing.T, set func(*serverOptions)) *httptest.Server {
	t.Helper()

	original := o
	t.Cleanup(func() { o = original })

	o = newServerOptions()
	o.features = map[string]bool{"webshark": true, "vm-mount": true, "tunneler-download": true}
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
	// path is the request's path and query; {job} stands for bob's job, and
	// {token} and {console} for a capture token and a console of the user's
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
	// jsonErrors is set for a route that answers errors as weberror JSON
	// rather than text
	jsonErrors bool
}

func (c contractRoute) String() string { return c.method + " " + c.path }

// url is the route's URL on server for the fixture, when user sends it.
func (c contractRoute) url(t *testing.T, server string, f contractFixture, user string) string {
	t.Helper()

	path := strings.ReplaceAll(c.path, "{job}", f.bobsJob.ID)

	if strings.Contains(path, "{token}") {
		path = strings.ReplaceAll(path, "{token}", f.captureToken(user))
	}

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
		exp    = "/api/v1/experiments/" + contractExp
		vm     = exp + "/vms/" + contractVM
		files  = `{"paths":["a.log"]}`
		config = "/api/v1/configs/topology/" + contractTopo
	)

	var (
		filesGet = testRole("experiments/files", "get", contractExp)
		captures = testRole("vms/captures", "list", contractExp+"/"+contractVM)
		trigger  = testRole("experiments/trigger", "create", contractExp)
		diskGet  = testRole("disks", "get", contractDisk)
		mount    = testRole("vms/mount", "get", contractExp+"/"+contractVM)
		one      = func(r rbac.Role) []rbac.Role { return []rbac.Role{r} }
		jobs     = func(verb string) []rbac.Role { return one(testRole("jobs", verb, contractExp)) }
		configs  = func(verb string) []rbac.Role { return one(testRole("configs", verb, "Topology/"+contractTopo)) }
		current  = []string{"If-Match", freshness.ETag(contractRevision), "Content-Type", mimeJSON}
		ok       = []int{http.StatusOK}
		done     = []int{http.StatusNoContent}
	)

	return []contractRoute{
		// jobs: another user's job is hidden without a jobs permission on
		// its experiment; GET /jobs lists only what the role may see
		{method: "GET", path: "/api/v1/jobs", want: ok},
		{method: "GET", path: "/api/v1/jobs/{job}", need: jobs("get"), hidden: http.StatusNotFound},
		{method: "POST", path: "/api/v1/jobs/{job}/cancel", need: jobs("update"), hidden: http.StatusNotFound},
		{method: "DELETE", path: "/api/v1/jobs/{job}", need: jobs("delete"), hidden: http.StatusNotFound},
		{method: "GET", path: "/api/v1/jobs/{job}/artifact", need: jobs("get"), hidden: http.StatusNotFound},
		{method: "HEAD", path: "/api/v1/jobs/{job}/artifact", need: jobs("get"), hidden: http.StatusNotFound},

		// captures and WebShark: a user lists and revokes only their own
		// capture tokens
		{method: "GET", path: exp + "/capture-tokens", want: ok},
		{method: "POST", path: exp + "/capture-tokens", body: `{"operations":["view"],"path":"a.pcap"}`, need: one(filesGet)},
		{method: "DELETE", path: exp + "/capture-tokens/{token}", want: done},
		{method: "POST", path: exp + "/webshark", body: `{"path":"a.pcap"}`, need: one(filesGet)},
		{method: "GET", path: vm + "/captures/0/stream", need: []rbac.Role{captures, filesGet}},

		// experiment files
		{method: "GET", path: exp + "/files", need: one(testRole("experiments/files", "list", contractExp)), want: ok},
		{method: "GET", path: exp + "/files/a.log?path=a.log", need: one(filesGet)},
		{method: "HEAD", path: exp + "/files/a.log?path=a.log", need: one(filesGet)},
		{method: "GET", path: exp + "/files/a.log/checksum?path=a.log", need: one(filesGet)},
		{method: "POST", path: exp + "/files/archive", body: files, need: one(filesGet)},
		{method: "POST", path: exp + "/files/download", body: files, need: one(filesGet)},
		{
			method: "POST", path: exp + "/files/delete", body: files,
			need: one(testRole("experiments/files", "delete", contractExp)),
		},
		{
			method: "DELETE", path: exp + "/files/a.log?path=a.log",
			need: one(testRole("experiments/files", "delete", contractExp)), want: done,
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
		{method: "GET", path: vm + "/files/download?path=etc/hostname", need: one(mount), want: ok},
		{method: "HEAD", path: vm + "/files/download?path=etc/hostname", need: one(mount), want: ok},

		// disks
		{method: "GET", path: "/api/v1/disks/checksum?disk=" + contractDisk, need: one(diskGet)},
		{method: "GET", path: "/api/v1/disks/download?disk=" + contractDisk, need: one(diskGet)},
		{method: "HEAD", path: "/api/v1/disks/download?disk=" + contractDisk, need: one(diskGet)},
		{
			method: "POST", path: "/api/v1/disks/clone/jobs?disk=" + contractDisk + "&new=copy",
			need: []rbac.Role{testRole("disks", "create", "copy.qcow2"), diskGet},
		},

		// consoles: a user reaches only their own
		{method: "GET", path: "/api/v1/console/{console}", need: one(testRole("miniconsole", "get")), want: ok},
		{method: "DELETE", path: "/api/v1/console/{console}", need: one(testRole("miniconsole", "post")), want: done},

		// state of health
		{method: "GET", path: "/api/v1/soh", need: one(testRole("experiments", "list"))},

		// configs, read and written at a revision
		{
			method: "GET", path: "/api/v1/configs/experiment/" + contractExp, jsonErrors: true,
			need: one(testRole("configs", "get", "Experiment/"+contractExp)),
		},
		{method: "GET", path: config, need: configs("get"), want: ok, jsonErrors: true},
		{
			method: "PUT", path: config, body: revisionTestTopology, header: current,
			need: configs("update"), want: done, jsonErrors: true,
		},
		{method: "DELETE", path: config, header: current, need: configs("delete"), want: done, jsonErrors: true},
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
// Behind a proxy that checks the token itself (proxy-jwt), the token must be
// in the header and name a user who holds it, and with authentication off,
// every request passes.
func TestContractAuth(t *testing.T) {
	bearer := func(token string) []string { return []string{"X-Phenix-Auth-Token", "Bearer " + token} }

	type request struct {
		// the server's --jwt-signing-key, testJWTKey when empty, and whether
		// it takes the user from X-Remote-User
		key   string
		proxy bool
		send  func(t *testing.T, route contractRoute, url, session string) *http.Response
		// the status it is refused with, or 0 when it passes
		refuse int
	}

	session := func(_ *testing.T, session string) []string { return bearer(session) }

	header := func(h func(t *testing.T, session string) []string) func(*testing.T, contractRoute, string, string) *http.Response {
		return func(t *testing.T, route contractRoute, url, session string) *http.Response {
			t.Helper()

			return do(t, route.method, url, route.body, append(slices.Clone(route.header), h(t, session)...)...)
		}
	}

	parameter := func(t *testing.T, route contractRoute, url, session string) *http.Response {
		t.Helper()

		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}

		return do(t, route.method, url+sep+"token="+session, route.body, route.header...)
	}

	none := header(func(*testing.T, string) []string { return nil })

	unknownUser := header(func(t *testing.T, _ string) []string {
		t.Helper()

		return bearer(signedJWT(t, "mallory", testJWTKey, time.Now().Add(time.Hour)))
	})

	requests := map[string]request{
		"no token": {send: none, refuse: http.StatusForbidden},
		"malformed token": {send: header(func(*testing.T, string) []string {
			return bearer("not.a.jwt")
		}), refuse: http.StatusUnauthorized},
		"not a bearer token": {send: header(func(_ *testing.T, session string) []string {
			return []string{"X-Phenix-Auth-Token", session}
		}), refuse: http.StatusUnauthorized},
		"token of another key": {send: header(func(t *testing.T, _ string) []string {
			t.Helper()

			return bearer(signedJWT(t, "alice", "another-key", time.Now().Add(time.Hour)))
		}), refuse: http.StatusUnauthorized},
		"expired token": {send: header(func(t *testing.T, _ string) []string {
			t.Helper()

			return bearer(signedJWT(t, "alice", testJWTKey, time.Now().Add(-time.Minute)))
		}), refuse: http.StatusUnauthorized},
		"signed-out token": {send: header(func(t *testing.T, _ string) []string {
			t.Helper()

			return bearer(signedJWT(t, "alice", testJWTKey, time.Now().Add(2*time.Hour)))
		}), refuse: http.StatusUnauthorized},
		"unknown user": {send: unknownUser, refuse: http.StatusUnauthorized},
		"another proxy user": {proxy: true, send: header(func(_ *testing.T, session string) []string {
			return append(bearer(session), "X-Remote-User", "bob")
		}), refuse: http.StatusUnauthorized},
		"no proxy user":   {proxy: true, send: header(session), refuse: http.StatusUnauthorized},
		"token header":    {send: header(session)},
		"token parameter": {send: parameter},
		"the token's proxy user": {proxy: true, send: header(func(_ *testing.T, session string) []string {
			return append(bearer(session), "X-Remote-User", "alice")
		})},
		"proxy-jwt without token":   {key: "proxy-jwt", send: none, refuse: http.StatusBadRequest},
		"proxy-jwt token parameter": {key: "proxy-jwt", send: parameter, refuse: http.StatusBadRequest},
		"proxy-jwt malformed token": {
			key:    "proxy-jwt",
			send:   header(func(*testing.T, string) []string { return bearer("x") }),
			refuse: http.StatusBadRequest,
		},
		"proxy-jwt unknown user":       {key: "proxy-jwt", send: unknownUser, refuse: http.StatusUnauthorized},
		"proxy-jwt token header":       {key: "proxy-jwt", send: header(session)},
		"authentication off, no token": {key: "off", send: none},
	}

	for _, route := range contractRoutes() {
		t.Run(route.String(), func(t *testing.T) {
			for name, req := range requests {
				t.Run(name, func(t *testing.T) {
					f := useContractFixture(t)
					session := useTestUser(t, combinedRole(route.need...))

					key, user := testJWTKey, "alice"

					switch req.key {
					case "off":
						key, user = "", "global-admin"
						useGlobalAdmin(t)
					case "proxy-jwt":
						key = req.key
					}

					server := serveContract(t, func(o *serverOptions) {
						o.jwtKey = key
						if req.proxy {
							o.proxyAuthHeader = "X-Remote-User"
						}
					}).URL

					resp := req.send(t, route, route.url(t, server, f, user), session)

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
// or has one of them only for another experiment, VM, disk image or config,
// with 403 (or the route's hidden status) and the route's error body, and
// lets a role with exactly those permissions past RBAC, with the response's
// sequence number.
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
					server := serveContractAs(t, role, nil).URL

					resp := do(t, route.method, route.url(t, server, f, "alice"), route.body, route.header...)
					if resp.StatusCode != route.denied() {
						t.Fatalf("status %d, want %d", resp.StatusCode, route.denied())
					}

					wantErrorBody(t, route, resp)
				})
			}

			t.Run("with exactly what it needs", func(t *testing.T) {
				f := useContractFixture(t)
				server := serveContractAs(t, combinedRole(route.need...), nil).URL

				resp := do(t, route.method, route.url(t, server, f, "alice"), route.body, route.header...)
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

// otherNames returns, for each of the roles that names the fixture's things,
// the roles with only that one's names changed: to another experiment, disk
// image or config, and, when it names test-vm, to another VM of the
// experiment. Each is keyed by the changed role's resource and the change.
func otherNames(roles []rbac.Role) map[string]rbac.Role {
	changes := map[string]*strings.Replacer{
		"another experiment, disk or config": strings.NewReplacer(
			contractExp, contractOther, contractDisk, "other-disk.qc2", "copy.qcow2", "other.qcow2",
			"/"+contractTopo, "/other-topology",
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

// useGlobalAdmin stores the global-admin role, which every request has with
// authentication off and over the Unix socket. The server keeps that role
// for 30 seconds across tests, so every test stores this one.
func useGlobalAdmin(t *testing.T) {
	t.Helper()

	useTestStore(t, store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Role",
		Metadata: store.ConfigMetadata{Name: "global-admin"},
		Spec:     map[string]any{"roleName": "Global Admin", "policies": rolePolicies(contractAdmin())},
	})
}

// The routes answer the requests they refuse with the documented status and
// body: unknown things, and other users' tokens and consoles, 404, methods a
// path does not take 405, bad input 400, conflicts 409, stale config
// revisions 412 and unsatisfiable ranges 416, as text or a weberror's JSON;
// and a current config revision 304. Each status is one openapi.yml documents
// for the route, or, for the 404 of an unknown path and the 405, in its
// description of errors.
func TestContractErrors(t *testing.T) {
	const (
		exp    = "/api/v1/experiments/" + contractExp
		config = "/api/v1/configs/topology/" + contractTopo
	)

	var (
		current = freshness.ETag(contractRevision)
		stale   = freshness.ETag(contractRevision - 1)
	)

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

	bobsConsole := func(t *testing.T, f contractFixture) string {
		t.Helper()

		return "/api/v1/console/" + f.console(t, "bob")
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
		"revoke another user's capture token": {
			method: "DELETE", want: http.StatusNotFound,
			prepare: func(_ *testing.T, f contractFixture) string { return exp + "/capture-tokens/" + f.captureToken("bob") },
		},
		"WebShark without capture": {method: "POST", path: exp + "/webshark", body: "{}", want: http.StatusBadRequest},
		"WebShark of non-capture":  {method: "POST", path: exp + "/webshark", body: `{"path":"a.log"}`, want: http.StatusBadRequest},
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
		"unknown console":            {method: "GET", path: "/api/v1/console/1", want: http.StatusNotFound},
		"another user's console":     {method: "GET", prepare: bobsConsole, want: http.StatusNotFound},
		"end another user's console": {method: "DELETE", prepare: bobsConsole, want: http.StatusNotFound},
		"delete missing file": {
			method: "DELETE", path: exp + "/files/none.log?path=none.log", want: http.StatusNotFound,
		},
		"mount download of missing file": {
			method: "GET", path: exp + "/vms/" + contractVM + "/files/download?path=etc/none", want: http.StatusNotFound,
		},
		"mount download of directory": {
			method: "GET", path: exp + "/vms/" + contractVM + "/files/download?path=etc", want: http.StatusBadRequest,
		},
		"unknown config": {method: "GET", path: "/api/v1/configs/topology/none", want: http.StatusNotFound, json: true},
		"current config": {method: "GET", path: config, header: []string{"If-None-Match", current}, want: http.StatusNotModified},
		"update of stale config": {
			method: "PUT", path: config, body: revisionTestTopology, want: http.StatusPreconditionFailed, json: true,
			header: []string{"If-Match", stale, "Content-Type", mimeJSON},
		},
		"update of malformed revision": {
			method: "PUT", path: config, body: revisionTestTopology, want: http.StatusBadRequest, json: true,
			header: []string{"If-Match", "r5", "Content-Type", mimeJSON},
		},
		"update of missing config at a revision": {
			method: "PUT", path: "/api/v1/configs/topology/none", body: revisionTestTopology,
			want: http.StatusPreconditionFailed, json: true, header: []string{"If-Match", current, "Content-Type", mimeJSON},
		},
		"update of missing config": {
			method: "PUT", path: "/api/v1/configs/topology/none", body: revisionTestTopology,
			want: http.StatusNotFound, json: true, header: []string{"Content-Type", mimeJSON},
		},
		"delete of missing config": {method: "DELETE", path: "/api/v1/configs/topology/none", want: http.StatusNotFound, json: true},
		"delete of stale config": {
			method: "DELETE", path: config, header: []string{"If-Match", stale}, want: http.StatusPreconditionFailed, json: true,
		},
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

			// gorilla's 405 and a 304 have no body
			if tc.want == http.StatusMethodNotAllowed || tc.want == http.StatusNotModified {
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
// job and config locations carry the base path, a capture token's URL,
// relative to it, opens WebShark on the capture, and WebShark's pages name
// it.
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

	// a config the workflow routes upsert
	upserted := do(t, http.MethodPost, server+"/api/v1/workflow/configs/main", upsertTestTopology, "Content-Type", mimeYAML)
	if upserted.StatusCode != http.StatusCreated {
		t.Fatalf("upserting a config: status %d: %s", upserted.StatusCode, readBody(t, upserted))
	}

	if location, want := upserted.Header.Get("Location"), base+"api/v1/configs/topology/main-topo"; location != want {
		t.Errorf("upserted config: Location %q, want %q", location, want)
	} else if got := proxied(t, http.MethodGet, location); got.StatusCode != http.StatusOK {
		t.Errorf("GET %s through the proxy: status %d", location, got.StatusCode)
	}

	resp := do(t, http.MethodPost, server+exp+"/capture-tokens", `{"operations":["view"],"path":"a.pcap"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("giving a capture token: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var token captureToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}

	// the view URL, resolved against the page phenix is served at, and the
	// capture WebShark's page then asks for
	view, err := url.Parse(base + token.URLs["view"])
	if err != nil {
		t.Fatal(err)
	}

	if got := proxied(t, http.MethodGet, view.String()); got.StatusCode != http.StatusOK {
		t.Errorf("GET %s through the proxy: status %d", view, got.StatusCode)
	}

	capture := base + "webshark/captures/" + view.Query().Get("capture")
	if got := proxied(t, http.MethodGet, capture); got.StatusCode != http.StatusOK {
		t.Errorf("GET %s through the proxy: status %d", capture, got.StatusCode)
	}

	if page := readBody(t, proxied(t, http.MethodGet, base+"webshark/")); !strings.Contains(page, `"`+base+`webshark/"`) {
		t.Errorf("WebShark's page does not name the base path: %s", page)
	}
}

// The routes outside the API's authentication answer without credentials,
// with their own statuses: health, features, version and tunneler downloads,
// and the WebShark routes, whose capture name or stream token is their
// credential.
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
		// the tunneler builds there are to download
		"/downloads/tunneler": {[]int{http.StatusOK}, "application/json"},
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

// The Unix socket serves its routes to every local client, as global-admin,
// with the response's sequence number, and no other API route: each socket
// route answers, and an HTTP-only route or method is not there.
func TestContractSocket(t *testing.T) {
	f := useContractFixture(t)
	useGlobalAdmin(t)

	original := o
	t.Cleanup(func() { o = original })

	o = newServerOptions()

	server := httptest.NewServer(newSocketRouter())
	t.Cleanup(server.Close)

	covered := map[string]bool{}

	for _, tc := range []struct {
		method, path, body, contentType string
		// route is the socket route's path template, or empty for a route or
		// method only HTTP serves
		route string
		want  int
	}{
		{method: "GET", path: "/options", route: "/options", want: http.StatusOK},
		{method: "GET", path: "/jobs", route: "/jobs", want: http.StatusOK},
		{method: "GET", path: "/jobs/{job}", route: "/jobs/{id}", want: http.StatusOK},
		{method: "POST", path: "/jobs/{job}/cancel", route: "/jobs/{id}/cancel", want: http.StatusConflict},
		{
			method: "POST", path: "/workflow/configs/main?dryRun=true", body: upsertTestTopology, contentType: mimeYAML,
			route: "/workflow/configs/{branch}", want: http.StatusOK,
		},
		// without a workflow config's content type
		{
			method: "POST", path: "/workflow/apply/main?dryRun=true", body: "{}",
			route: "/workflow/apply/{branch}", want: http.StatusBadRequest,
		},
		{method: "DELETE", path: "/jobs/{job}", want: http.StatusMethodNotAllowed},
		{method: "OPTIONS", path: "/jobs", want: http.StatusMethodNotAllowed},
		{method: "GET", path: "/jobs/{job}/artifact", want: http.StatusNotFound},
		{method: "GET", path: "/soh", want: http.StatusNotFound},
	} {
		if tc.route != "" {
			covered[tc.method+" "+tc.route] = true
		}

		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var header []string
			if tc.contentType != "" {
				header = []string{"Content-Type", tc.contentType}
			}

			resp := do(t, tc.method, server.URL+"/api/v1"+strings.ReplaceAll(tc.path, "{job}", f.bobsJob.ID), tc.body, header...)
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.want, readBody(t, resp))
			}

			if tc.route != "" && resp.Header.Get("X-Phenix-Seq") == "" {
				t.Error("no X-Phenix-Seq")
			}
		})
	}

	for _, r := range socketRoutes() {
		for _, method := range r.methods {
			if !covered[method+" "+r.path] {
				t.Errorf("socket route %s %s has no case", method, r.path)
			}
		}
	}
}
