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
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/job"
	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/web/capability"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// The API contract drives each contractRoutes route through phenix ui's router in each auth mode, with and without
// its permissions, behind a base path and with refused inputs; a new route adds a row there and its openapi.yml path.

// The experiment, VM, disk image and topology of the contract fixture, the
// topology's revision, and an experiment it does not have.
const (
	contractExp      = "test-experiment"
	contractVM       = "test-vm"
	contractDisk     = "test-disk.qc2"
	contractTopo     = "demo"
	contractRevision = 5
	contractOther    = "other-experiment"
	contractNetflow  = "20261028T153012.345Z"
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

// contractFixture: running test-experiment with test-vm mounted (etc/hostname), files a.log, a.pcap and a netflow capture, test-disk.qc2,
// demo at revision 5, bob's finished job (3-byte artifact), on-demand capture tokens and consoles; fake minimega says ok.
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

	if err := os.MkdirAll(filepath.Join(expFiles, "netflow"), 0o750); err != nil {
		t.Fatal(err)
	}

	for name, contents := range map[string]string{
		"a.log": "alpha", "a.pcap": string(testPcapHeader()),
		"netflow/" + contractNetflow + ".txt": "2026-10-28T15:30:12Z\t1s\t6\t10.0.0.1:1\t<->\t10.0.0.2:2\t1\t1\n",
	} {
		if err := os.WriteFile(filepath.Join(expFiles, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	useCaptureGrants(t)
	useContractMount(t)

	// another user, for routes that act on a user other than the requester
	useTestStore(t, store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "User", Metadata: store.ConfigMetadata{Name: "bob"},
		Spec: map[string]any{"username": "bob", "rbac": map[string]any{"roleName": "test-role", "policies": []any{}}},
	})

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
		t.Fatalf("starting a console of %s: %v", user, err)
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
	o.systemLog = contractSystemLog(t)

	if set != nil {
		set(&o)
	}

	public := testPublic()

	router := newRouter(http.FS(public))

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return server
}

// contractSystemLog writes a system log file of one entry, and returns the
// systemLog server option that names it.
func contractSystemLog(t *testing.T) func() string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "phenix.log")
	if err := os.WriteFile(path, []byte(`{"level":"INFO","msg":"contract"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return func() string { return path }
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
		filesGet   = testRole("experiments/files", "get", contractExp)
		netflowGet = testRole("experiments/netflow", "get", contractExp)
		captures   = testRole("vms/captures", "list", contractExp+"/"+contractVM)
		trigger    = testRole("experiments/trigger", "create", contractExp)
		diskGet    = testRole("disks", "get", contractDisk)
		mount      = testRole("vms/mount", "get", contractExp+"/"+contractVM)
		one        = func(r rbac.Role) []rbac.Role { return []rbac.Role{r} }
		jobs       = func(verb string) []rbac.Role { return one(testRole("jobs", verb, contractExp)) }
		configs    = func(verb string) []rbac.Role { return one(testRole("configs", verb, "Topology/"+contractTopo)) }
		current    = []string{"If-Match", freshness.ETag(contractRevision), "Content-Type", mimeJSON}
		ok         = []int{http.StatusOK}
		done       = []int{http.StatusNoContent}
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
		{method: "GET", path: exp + "/captureTokens", want: ok},
		{method: "POST", path: exp + "/captureTokens", body: `{"operations":["view"],"path":"a.pcap"}`, need: one(filesGet)},
		{method: "DELETE", path: exp + "/captureTokens/{token}", want: done},
		{method: "POST", path: exp + "/webshark", body: `{"path":"a.pcap"}`, need: one(filesGet)},
		{method: "GET", path: vm + "/captures/0/stream", need: []rbac.Role{captures, filesGet}},

		// recorded netflow captures
		{method: "GET", path: exp + "/netflow/captures", need: one(netflowGet), want: ok},
		{method: "GET", path: exp + "/netflow/captures/" + contractNetflow + "?format=csv", need: one(netflowGet), want: ok},
		{method: "HEAD", path: exp + "/netflow/captures/" + contractNetflow, need: one(netflowGet), want: ok},

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
			method: "POST", path: exp + "/scorch/pipelines/0/cleanup",
			need: []rbac.Role{trigger, testRole("experiments", "get", contractExp)},
		},
		{method: "POST", path: exp + "/scorch/pipelines/0/clear", need: one(trigger)},

		// VMs: a bulk update refuses a role that may patch none of its VMs
		{
			method: "PATCH", path: exp + "/vms", body: `{"vms":[{"name":"test-vm","interface":{"index":0,"vlan":"EXP"}}]}`,
			need: one(testRole("vms", "patch", contractExp+"/"+contractVM)),
		},
		{
			method: "POST", path: exp + "/vms/actions", body: `{"action":"start","vms":["test-vm"]}`,
			need: one(testRole("vms/start", "update", contractExp+"/"+contractVM)),
		},
		{method: "GET", path: vm + "/vnc/ws", need: one(testRole("vms/vnc", "get", contractExp+"/"+contractVM))},
		{method: "GET", path: vm + "/files/download?path=etc/hostname", need: one(mount), want: ok},
		{method: "HEAD", path: vm + "/files/download?path=etc/hostname", need: one(mount), want: ok},

		// disks
		{method: "GET", path: "/api/v1/disks/checksum?disk=" + contractDisk, need: one(diskGet)},
		{method: "GET", path: "/api/v1/disks/details?disk=" + contractDisk, need: one(testRole("disks", "list", contractDisk))},
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

		// the server's log files
		{method: "GET", path: "/api/v1/logs/files", need: one(testRole("logs", "get")), want: ok},
		{method: "GET", path: "/api/v1/logs/files/phenix", need: one(testRole("logs", "get")), want: ok},
		{method: "HEAD", path: "/api/v1/logs/files/phenix", need: one(testRole("logs", "get")), want: ok},

		// users: a token for another user also needs users/tokens create
		{
			method: "POST", path: "/api/v1/users/bob/tokens", body: `{"lifetime":"1h"}`, want: ok,
			need: []rbac.Role{testRole(resourceUsers, verbPatch, "bob"), testRole("users/tokens", "create", "bob")},
		},

		// configs, read and written at a revision
		{
			method: "GET", path: "/api/v1/configs/experiment/" + contractExp,
			need: one(testRole("configs", "get", "Experiment/"+contractExp)),
		},
		{method: "GET", path: config, need: configs("get"), want: ok},
		{
			method: "PUT", path: config, body: revisionTestTopology, header: current,
			need: configs("update"), want: done,
		},
		{method: "DELETE", path: config, header: current, need: configs("delete"), want: done},
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

// Each route needs a held session: no token is 403, an untrusted one 401, and a --proxy-auth-header proxy must name the
// token's user; proxy-jwt needs the token in the header naming a user who holds it; with auth off, every request passes.
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
							t.Errorf("%s with %s = %d, want it past auth and RBAC: %s", route, name, resp.StatusCode, readBody(t, resp))
						}
					case resp.StatusCode != req.refuse:
						t.Errorf("%s with %s = %d, want %d", route, name, resp.StatusCode, req.refuse)
					default:
						wantErrorBody(t, route, resp)
					}
				})
			}
		})
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
					server := serveContractAs(t, role, nil).URL

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
				server := serveContractAs(t, combinedRole(route.need...), nil).URL

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
				t.Fatalf("GET /api/v1/jobs = %d, want %d", resp.StatusCode, http.StatusOK)
			}

			var body struct {
				Jobs []job.Job `json:"jobs"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decoding GET /api/v1/jobs: %v", err)
			}

			ids := make([]string, 0, len(body.Jobs))
			for _, j := range body.Jobs {
				ids = append(ids, j.ID)
			}

			var want []string
			if tc.want {
				want = []string{f.bobsJob.ID}
			}

			if diff := cmp.Diff(want, ids, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GET /api/v1/jobs job IDs mismatch (-want +got):\n%s", diff)
			}
		})
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

const (
	contractExpPath    = "/api/v1/experiments/" + contractExp
	contractConfigPath = "/api/v1/configs/topology/" + contractTopo
)

// jobAndCaptureErrorCases are TestContractErrors' job, capture and WebShark cases.
func jobAndCaptureErrorCases() map[string]contractErrorCase {
	const exp = contractExpPath

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

	return map[string]contractErrorCase{
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
		"capture token of malformed body": {method: "POST", path: exp + "/captureTokens", body: "{", want: http.StatusBadRequest},
		"capture token of unknown operation": {
			method: "POST", path: exp + "/captureTokens", body: `{"operations":["fly"],"path":"a.pcap"}`,
			want: http.StatusBadRequest,
		},
		"stream token of saved capture": {
			method: "POST", path: exp + "/captureTokens", body: `{"operations":["stream"],"path":"a.pcap"}`,
			want: http.StatusBadRequest,
		},
		"revoke unknown capture token": {method: "DELETE", path: exp + "/captureTokens/none", want: http.StatusNotFound},
		"revoke another user's capture token": {
			method: "DELETE", want: http.StatusNotFound,
			prepare: func(_ *testing.T, f contractFixture) string { return exp + "/captureTokens/" + f.captureToken("bob") },
		},
		"WebShark without capture": {method: "POST", path: exp + "/webshark", body: "{}", want: http.StatusBadRequest},
		"WebShark of non-capture":  {method: "POST", path: exp + "/webshark", body: `{"path":"a.log"}`, want: http.StatusBadRequest},
		"WebShark of missing capture": {
			method: "POST", path: exp + "/webshark", body: `{"path":"none.pcap"}`, want: http.StatusNotFound,
		},
		"stream of VM without capture": {
			method: "GET", path: exp + "/vms/" + contractVM + "/captures/0/stream", want: http.StatusNotFound,
		},
		"method a job takes not": {method: "PUT", prepare: bobs(""), want: http.StatusMethodNotAllowed},
	}
}

// fileAndVMErrorCases are TestContractErrors' file, disk, VM and console cases.
func fileAndVMErrorCases() map[string]contractErrorCase {
	const exp = contractExpPath

	bobsConsole := func(t *testing.T, f contractFixture) string {
		t.Helper()

		return "/api/v1/console/" + f.console(t, "bob")
	}

	return map[string]contractErrorCase{
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
		"stop with a force that is not a boolean": {
			method: "POST", path: exp + "/stop?force=maybe", want: http.StatusBadRequest,
		},
		"VM action unknown": {
			method: "POST", path: exp + "/vms/actions", body: `{"action":"fly","vms":["test-vm"]}`, want: http.StatusBadRequest,
		},
		"VM action of no VMs": {
			method: "POST", path: exp + "/vms/actions", body: `{"action":"start","vms":[]}`, want: http.StatusBadRequest,
		},
		"netflow that is not running": {method: "GET", path: exp + "/netflow", want: http.StatusNotFound},
		"netflow captures of unknown experiment": {
			method: "GET", path: "/api/v1/experiments/" + contractOther + "/netflow/captures", want: http.StatusNotFound,
		},
		"netflow capture in unknown format": {
			method: "GET", path: exp + "/netflow/captures/" + contractNetflow + "?format=pcap", want: http.StatusBadRequest,
		},
		"netflow capture with invalid id": {
			method: "GET", path: exp + "/netflow/captures/latest", want: http.StatusBadRequest,
		},
		"netflow capture that is missing": {
			method: "GET", path: exp + "/netflow/captures/20200101T000000.000Z?format=jsonl", want: http.StatusNotFound,
		},
		"checksum of missing disk": {method: "GET", path: "/api/v1/disks/checksum?disk=none.qc2", want: http.StatusNotFound},
		"checksum of disk outside": {method: "GET", path: "/api/v1/disks/checksum?disk=../none.qc2", want: http.StatusBadRequest},
		"details of missing disk":  {method: "GET", path: "/api/v1/disks/details?disk=none.qc2", want: http.StatusNotFound},
		"details of disk outside":  {method: "GET", path: "/api/v1/disks/details?disk=../none.qc2", want: http.StatusBadRequest},
		"details of no disk":       {method: "GET", path: "/api/v1/disks/details?disk=", want: http.StatusBadRequest},
		"clone onto existing disk": {
			method: "POST", path: "/api/v1/disks/clone/jobs?disk=" + contractDisk + "&new=" + contractDisk,
			want: http.StatusConflict,
		},
		"clone to another image extension": {
			method: "POST", path: "/api/v1/disks/clone/jobs?disk=" + contractDisk + "&new=copy.iso",
			want: http.StatusBadRequest,
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
	}
}

// configAndRouteErrorCases are TestContractErrors' config, SCORCH, user and routing cases.
func configAndRouteErrorCases() map[string]contractErrorCase {
	const (
		exp    = contractExpPath
		config = contractConfigPath
	)

	var (
		current = freshness.ETag(contractRevision)
		stale   = freshness.ETag(contractRevision - 1)
	)

	return map[string]contractErrorCase{
		"unknown config":   {method: "GET", path: "/api/v1/configs/topology/none", want: http.StatusNotFound},
		"unknown log file": {method: "GET", path: "/api/v1/logs/files/none", want: http.StatusNotFound},
		"unconfigured minimega log file": {
			method: "GET", path: "/api/v1/logs/files/minimega", want: http.StatusNotFound,
		},
		"current config": {method: "GET", path: config, header: []string{"If-None-Match", current}, want: http.StatusNotModified},
		"update of stale config": {
			method: "PUT", path: config, body: revisionTestTopology, want: http.StatusPreconditionFailed,
			header: []string{"If-Match", stale, "Content-Type", mimeJSON},
		},
		"update of malformed revision": {
			method: "PUT", path: config, body: revisionTestTopology, want: http.StatusBadRequest,
			header: []string{"If-Match", "r5", "Content-Type", mimeJSON},
		},
		"update of missing config at a revision": {
			method: "PUT", path: "/api/v1/configs/topology/none", body: revisionTestTopology,
			want: http.StatusPreconditionFailed, header: []string{"If-Match", current, "Content-Type", mimeJSON},
		},
		"update of missing config": {
			method: "PUT", path: "/api/v1/configs/topology/none", body: revisionTestTopology,
			want: http.StatusNotFound, header: []string{"Content-Type", mimeJSON},
		},
		"delete of missing config": {method: "DELETE", path: "/api/v1/configs/topology/none", want: http.StatusNotFound},
		"delete of stale config": {
			method: "DELETE", path: config, header: []string{"If-Match", stale}, want: http.StatusPreconditionFailed,
		},
		"clear run that is not a number": {
			method: "POST", path: exp + "/scorch/pipelines/x/clear", want: http.StatusBadRequest,
		},
		"clear run the experiment lacks": {
			method: "POST", path: exp + "/scorch/pipelines/0/clear", want: http.StatusNotFound,
		},
		"clean up run the experiment lacks": {
			method: "POST", path: exp + "/scorch/pipelines/0/cleanup", want: http.StatusBadRequest,
		},
		"user with unknown role": {
			method: "POST", path: "/api/v1/users", body: createUserBody("carol", "No Such Role"), want: http.StatusBadRequest,
		},
		"user with taken username": {
			method: "POST", path: "/api/v1/users", body: createUserBody("bob", "Global Admin"), want: http.StatusConflict,
			prepare: func(t *testing.T, _ contractFixture) string {
				t.Helper()

				useGlobalAdmin(t)

				return "/api/v1/users"
			},
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
// unknown things, others' tokens or consoles 404, bad input 400, conflict 409, stale revision 412, bad range 416; current revision 304.
func TestContractErrors(t *testing.T) {
	spec := loadOpenAPI(t)

	for _, cases := range []map[string]contractErrorCase{
		jobAndCaptureErrorCases(), fileAndVMErrorCases(), configAndRouteErrorCases(),
	} {
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) { checkContractError(t, spec, name, tc) })
		}
	}
}

// checkContractError makes tc's request as an admin and checks its status and body.
func checkContractError(t *testing.T, spec openAPISpec, name string, tc contractErrorCase) {
	t.Helper()

	f := useContractFixture(t)
	server := serveContractAs(t, adminRole(), nil).URL

	path := tc.path
	if tc.prepare != nil {
		path = tc.prepare(t, f)
	}

	resp := do(t, tc.method, server+path, tc.body, tc.header...)
	if resp.StatusCode != tc.want {
		t.Fatalf("%s %s = %d, want %d: %s", tc.method, path, resp.StatusCode, tc.want, readBody(t, resp))
	}

	// a 304 has no body
	if tc.want == http.StatusNotModified {
		return
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
		"/api/v1/experiments",
		exp,
		exp + "/schedule",
		exp + "/captures",
		exp + "/topology",
		exp + "/topology/search?hostname=" + contractVM,
		exp + "/soh",
		exp + "/vms",
		vm,
		vm + "/captures",
		vm + "/forwards",
		vm + "/files?path=etc",
		exp + "/scorch/terminals",
		"/api/v1/vms",
		"/api/v1/disks",
		"/api/v1/applications",
		"/api/v1/topologies",
		"/api/v1/topologies/" + contractTopo + "/scenarios",
		"/api/v1/users",
		"/api/v1/roles",
	} {
		t.Run(path, func(t *testing.T) {
			useContractFixture(t)
			server := serveContractAs(t, adminRole(), nil).URL

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

// Behind a base-path proxy that strips it, server-given URLs work through the proxy: job and config locations carry the
// base path, a capture token's relative URL opens WebShark on the capture, and WebShark's pages name the base path.
func TestContractBasePath(t *testing.T) {
	const base = "/phenix/"

	useContractFixture(t)
	server := serveContractAs(t, adminRole(), func(o *serverOptions) {
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
			t.Errorf("POST %s = %d, want %d: %s", start.path, resp.StatusCode, http.StatusAccepted, readBody(t, resp))

			continue
		}

		var started job.Job
		if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
			t.Errorf("decoding POST %s: %v", start.path, err)

			continue
		}

		location := resp.Header.Get("Location")
		if want := base + "api/v1/jobs/" + started.ID; location != want {
			t.Errorf("POST %s Location = %q, want %q", start.path, location, want)

			continue
		}

		if got := proxied(t, http.MethodGet, location); got.StatusCode != http.StatusOK {
			t.Errorf("GET %s through the proxy = %d, want %d", location, got.StatusCode, http.StatusOK)
		}
	}

	// a config the workflow routes upsert
	upserted := do(t, http.MethodPost, server+"/api/v1/workflow/configs/main", upsertTestTopology, "Content-Type", mimeYAML)
	if upserted.StatusCode != http.StatusCreated {
		t.Errorf("POST /api/v1/workflow/configs/main = %d, want %d: %s",
			upserted.StatusCode, http.StatusCreated, readBody(t, upserted))
	} else if location, want := upserted.Header.Get("Location"), base+"api/v1/configs/topology/main-topo"; location != want {
		t.Errorf("POST /api/v1/workflow/configs/main Location = %q, want %q", location, want)
	} else if got := proxied(t, http.MethodGet, location); got.StatusCode != http.StatusOK {
		t.Errorf("GET %s through the proxy = %d, want %d", location, got.StatusCode, http.StatusOK)
	}

	resp := do(t, http.MethodPost, server+exp+"/captureTokens", `{"operations":["view"],"path":"a.pcap"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s/captureTokens = %d, want %d: %s", exp, resp.StatusCode, http.StatusCreated, readBody(t, resp))
	}

	var token captureToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		t.Fatalf("decoding POST %s/captureTokens: %v", exp, err)
	}

	// the view URL, resolved against the page phenix is served at, and the
	// capture WebShark's page then asks for
	view, err := url.Parse(base + token.URLs["view"])
	if err != nil {
		t.Fatalf("capture token view URL %q: %v", token.URLs["view"], err)
	}

	if got := proxied(t, http.MethodGet, view.String()); got.StatusCode != http.StatusOK {
		t.Errorf("GET %s through the proxy = %d, want %d", view, got.StatusCode, http.StatusOK)
	}

	capture := base + "webshark/captures/" + view.Query().Get("capture")
	if got := proxied(t, http.MethodGet, capture); got.StatusCode != http.StatusOK {
		t.Errorf("GET %s through the proxy = %d, want %d", capture, got.StatusCode, http.StatusOK)
	}

	if page := readBody(t, proxied(t, http.MethodGet, base+"webshark/")); !strings.Contains(page, `"`+base+`webshark/"`) {
		t.Errorf("GET %swebshark/ through the proxy = %s, want the base path named", base, page)
	}
}

// Routes outside API auth answer without credentials, with their own statuses: health, features, version, tunneler
// downloads, and WebShark, whose capture name or stream token is the route's credential.
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
		"/webshark/streams/pcs_none": {[]int{http.StatusNotFound}, "application/json"},
		// WebShark's own requests, answered as WebShark's server would
		"/webshark/captures/none.pcap":                {[]int{http.StatusNotFound}, "application/json"},
		"/webshark/json?method=download&capture=none": {[]int{http.StatusNotFound}, "application/json"},
		"/webshark/watch?capture=none":                {[]int{http.StatusNotFound}, "application/json"},
		"/webshark/json?method=files":                 {[]int{http.StatusOK}, "application/json"},
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
				t.Errorf("socket %s %s = %d, want %d: %s", tc.method, tc.path, resp.StatusCode, tc.want, readBody(t, resp))
			}

			if tc.route != "" && resp.Header.Get("X-Phenix-Seq") == "" {
				t.Errorf("socket %s %s: X-Phenix-Seq is missing", tc.method, tc.path)
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
