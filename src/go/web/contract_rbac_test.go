package web

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/web/routing"
)

// servedRoute is one method of a route the router serves: its path template
// and the query parameters its handler requires.
type servedRoute struct {
	method, path string
	queries      []string
	// ofGet is set for the HEAD that a GET route serves
	ofGet bool
}

func (r servedRoute) String() string { return r.method + " " + r.path }

// key is the route's key in handlerQueries, rbacExempt and rbacHidden: a HEAD
// that a GET route serves takes the entries of the GET.
func (r servedRoute) key() string {
	if r.ofGet {
		return http.MethodGet + " " + r.path
	}

	return r.String()
}

// servedRoutes are the router's operations that name their methods, and the
// HEAD of each GET route that serves it, with every feature on, OPTIONS left
// out.
func servedRoutes(t *testing.T) []servedRoute {
	t.Helper()

	server := serveContract(t, nil)

	router, ok := server.Config.Handler.(*routing.Mux)
	if !ok {
		t.Fatalf("contract server handler = %T, want *routing.Mux", server.Config.Handler)
	}

	var routes []servedRoute

	for _, route := range router.Routes() {
		// a route of any method, which TestRouterMatchesOpenAPI checks
		if route.Method == "" {
			continue
		}

		queries := handlerQueries[route.Method+" "+route.Path]
		routes = append(routes, servedRoute{method: route.Method, path: route.Path, queries: queries, ofGet: false})

		if route.Method == http.MethodGet && !route.RefusesHead() {
			routes = append(routes, servedRoute{method: http.MethodHead, path: route.Path, queries: queries, ofGet: true})
		}
	}

	return routes
}

// handlerQueries are the query parameters that a route's handler requires,
// for the HEAD of a GET route too.
//
//nolint:gochecknoglobals // constant table
var handlerQueries = map[string][]string{
	"POST /api/v1/disks/snapshot":                             {"disk", "new"},
	"POST /api/v1/disks/rebase":                               {"disk", "backing", "unsafe"},
	"POST /api/v1/disks/resize":                               {"disk", "size"},
	"POST /api/v1/disks/commit":                               {"disk"},
	"POST /api/v1/disks/clone":                                {"disk", "new"},
	"POST /api/v1/disks/clone/jobs":                           {"disk", "new"},
	"DELETE /api/v1/disks":                                    {"disk"},
	"POST /api/v1/disks/rename":                               {"disk", "new"},
	"GET /api/v1/disks/download":                              {"disk"},
	"GET /api/v1/disks/checksum":                              {"disk"},
	"GET /api/v1/disks/details":                               {"disk"},
	"GET /api/v1/experiments/{exp}/vms/{name}/files":          {"path"},
	"GET /api/v1/experiments/{exp}/vms/{name}/files/download": {"path"},
	"PUT /api/v1/experiments/{exp}/vms/{name}/files/upload":   {"path"},
	"POST /api/v1/console/{pid}/size":                         {"cols", "rows"},
}

// routeParams are the contract fixture's value of each path and query
// parameter, keyed by the segments before it where they decide.
//
//nolint:gochecknoglobals // constant table
var routeParams = map[string]string{
	"experiments/{name}": contractExp, "vms/{name}": contractVM, "topologies/{name}": contractTopo,
	"topology/{name}": contractTopo, "{exp}": contractExp, "{kind}": "topology", "{version}": "v1",
	"{topo}": contractTopo, "{username}": "bob", "{pid}": "1", "{id}": "{job}", "{snapshot}": "snap",
	"{iface}": "0", "{host}": "headnode", "{port}": "22", "{filename}": "a.log", "{branch}": "main",
	"{run}": "0", "{loop}": "0", "{stage}": "configure", "{cmp}": "cmp", "{token}": "pcs_none",
	"{name}": "none", "disk": contractDisk, "new": "copy", "backing": contractDisk, "unsafe": "false",
	"size": "1G", "path": "etc/hostname", "cols": "80", "rows": "24", "capture": "none",
	"{draft}": "d1", "{document}": "doc1", "{topology}": contractTopo, "{icon}": "plc", "{template}": "t1",
	"{collection}": "c1", "{layerset}": "l1",
	"vlans/{id}": "100", "blacklist/{id}": "100", "{namespace}": "old-demo", "{alias}": "EXP_1",
	"{key}": "log.level",
}

// concrete is the route's path and query for the contract fixture, with
// {job} standing for bob's job as in contractRoute.
func (r servedRoute) concrete() string {
	segments := strings.Split(r.path, "/")

	for i, segment := range segments {
		if !strings.HasPrefix(segment, "{") {
			continue
		}

		name := strings.Trim(segment, "{}")

		value, ok := routeParams[segments[i-1]+"/{"+name+"}"]
		if !ok {
			value = routeParams["{"+name+"}"]
		}

		segments[i] = value
	}

	query := make([]string, 0, len(r.queries))
	for _, name := range r.queries {
		query = append(query, name+"="+routeParams[name])
	}

	path := strings.Join(segments, "/")
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}

	return path
}

// rbacExempt are the routes a role of no permissions may call, with why: those outside /api/v1 and its auth,
// and API routes every signed-in user may call or that act only on what the role may see, for the HEAD of a GET route too.
//
//nolint:gochecknoglobals // constant table
var rbacExempt = map[string]string{
	"GET /features":                                       "outside API auth: the features the UI offers",
	"GET /version":                                        "outside API auth: the server's build",
	"GET /healthz":                                        "outside API auth: liveness probe",
	"GET /readyz":                                         "outside API auth: readiness probe",
	"GET /docs/openapi.yml":                               "outside API auth: this API's description",
	"GET /downloads/tunneler":                             "outside API auth: tunneler builds to download",
	"GET /downloads/tunneler/{name}":                      "outside API auth: a tunneler build",
	"GET /webshark/streams/{token}":                       "outside API auth: the capture grant in the path is the credential",
	"GET /webshark/json":                                  "outside API auth: the capture grant in the query is the credential",
	"GET /webshark/captures/{name}":                       "outside API auth: the capture grant in the path is the credential",
	"GET /webshark/watch":                                 "outside API auth: the capture grant in the query is the credential",
	"POST /api/v1/signup":                                 "creates an account, before the caller has a role",
	"GET /api/v1/login":                                   "signs in, before the caller has a role",
	"POST /api/v1/login":                                  "signs in, before the caller has a role",
	"POST /api/v1/logout":                                 "signs the caller's own session out",
	"GET /api/v1/ws":                                      "the broker sends each message only to the roles its policy allows",
	"GET /api/v1/settings":                                "the web UI settings every signed-in user reads",
	"GET /api/v1/settings/password":                       "the password rules the sign-up and password forms check",
	"GET /api/v1/settings/timeout":                        "when the UI signs an idle user out",
	"GET /api/v1/jobs":                                    "lists the caller's own jobs and those the role may list",
	"PATCH /api/v1/experiments/{exp}/vms":                 "checks vms patch on each VM and leaves out those the role lacks",
	"GET /api/v1/experiments/{exp}/captureTokens":         "lists only the caller's own tokens",
	"DELETE /api/v1/experiments/{exp}/captureTokens/{id}": "revokes only the caller's own token, 404 for another's",
}

// rbacHidden are the routes that refuse with 404, so as not to reveal
// another user's job, for the HEAD of a GET route too.
//
//nolint:gochecknoglobals // constant table
var rbacHidden = []string{
	"GET /api/v1/jobs/{id}",
	"POST /api/v1/jobs/{id}/cancel",
	"DELETE /api/v1/jobs/{id}",
	"GET /api/v1/jobs/{id}/artifact",
}

// Every route, and the HEAD of each GET route, refuses a role of no permissions with 401 or 403 (404 if rbacHidden)
// unless rbacExempt lists it; requests carry contractRoutes' body, so the refusal is RBAC's rather than the body's.
func TestEveryRouteChecksPermissions(t *testing.T) {
	routes := servedRoutes(t)

	bodies := map[string]contractRoute{}
	for _, c := range contractRoutes() {
		bodies[c.method+" "+c.path] = c
	}

	served := map[string]bool{}

	for _, route := range routes {
		served[route.key()] = true

		if rbacExempt[route.key()] != "" {
			continue
		}

		t.Run(route.String(), func(t *testing.T) {
			f := useContractFixture(t)
			server := serveContractAs(t, combinedRole(), func(o *serverOptions) { o.minimegaConsole = true }).URL

			c := contractRoute{method: route.method, path: route.concrete(), body: "{}"}
			if known, ok := bodies[c.String()]; ok {
				c = known
			} else if known, ok := bodies[http.MethodGet+" "+c.path]; ok && route.ofGet {
				c, c.method = known, http.MethodHead
			}

			resp := do(t, c.method, c.url(t, server, f, "alice"), c.body, c.header...)

			want := []int{http.StatusUnauthorized, http.StatusForbidden}
			if slices.Contains(rbacHidden, route.key()) {
				want = []int{http.StatusNotFound}
			}

			if !slices.Contains(want, resp.StatusCode) {
				t.Errorf("%s with no permissions = %d, want one of %v: %s",
					c, resp.StatusCode, want, readBody(t, resp))
			}
		})
	}

	for key := range rbacExempt {
		if !served[key] {
			t.Errorf("rbacExempt lists %s, which the router does not serve", key)
		}
	}

	for _, key := range rbacHidden {
		if !served[key] {
			t.Errorf("rbacHidden lists %s, which the router does not serve", key)
		}
	}

	for key := range handlerQueries {
		if !served[key] {
			t.Errorf("handlerQueries lists %s, which the router does not serve", key)
		}
	}
}

// concrete fills each path parameter, and each query parameter, with the
// contract fixture's value; the segment before a parameter, as filled,
// decides first.
func TestServedRouteConcrete(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		route servedRoute
		want  string
	}{
		"no parameters": {servedRoute{path: "/version"}, "/version"},
		"experiment":    {servedRoute{path: "/api/v1/experiments/{name}"}, "/api/v1/experiments/" + contractExp},
		"VM of an experiment": {
			servedRoute{path: "/api/v1/experiments/{exp}/vms/{name}"},
			"/api/v1/experiments/" + contractExp + "/vms/" + contractVM,
		},
		"after a filled parameter": {servedRoute{path: "/api/v1/configs/{kind}/{name}"}, "/api/v1/configs/topology/" + contractTopo},
		"unknown segment":          {servedRoute{path: "/api/v1/hosts/{name}"}, "/api/v1/hosts/none"},
		"job":                      {servedRoute{path: "/api/v1/jobs/{id}"}, "/api/v1/jobs/{job}"},
		"queries": {
			servedRoute{path: "/api/v1/console/{pid}/size", queries: []string{"cols", "rows"}},
			"/api/v1/console/1/size?cols=80&rows=24",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.route.concrete(); got != tc.want {
				t.Errorf("servedRoute{%s %v}.concrete() = %q, want %q", tc.route.path, tc.route.queries, got, tc.want)
			}
		})
	}
}

// stubAnswer is what TestEveryGetRouteAnswersHead reads of an answer of a
// stubMux.
type stubAnswer struct {
	// row is the method and path of the row that answered, or "options",
	// "404" or "405"
	row, method, allow string
}

// stubMux is a Mux of the rows of mux, each WithoutHead as in mux, in a group
// of prefix "" and one of /api/v1 with Options when apiOptions is set. Each
// row answers with its method and path, and the method of the request.
func stubMux(t *testing.T, mux *routing.Mux, apiOptions bool) *routing.Mux {
	t.Helper()

	stub := func(row string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Row", row)
			w.Header().Set("X-Method", r.Method)
		})
	}

	root := routing.Group{Prefix: "", NotFound: stub("404"), MethodNotAllowed: stub("405")}
	api := routing.Group{Prefix: "/api/v1", NotFound: stub("404"), MethodNotAllowed: stub("405")}

	if apiOptions {
		api.Options = stub("options")
	}

	for _, row := range mux.Routes() {
		group, path := &root, row.Path
		if rest, ok := strings.CutPrefix(row.Path, api.Prefix); ok {
			group, path = &api, rest
		}

		copied := routing.Row(row.Method, path, stub(row.Method+" "+row.Path))
		if row.RefusesHead() {
			copied = copied.WithoutHead()
		}

		group.Routes = append(group.Routes, copied)
	}

	stubs, err := routing.NewMux(root, api)
	if err != nil {
		t.Fatalf("NewMux() of the stubs error = %v", err)
	}

	return stubs
}

// answerOf is the answer of stubs to a request with method to target.
func answerOf(stubs *routing.Mux, method, target string) stubAnswer {
	rec := httptest.NewRecorder()
	stubs.ServeHTTP(rec, httptest.NewRequest(method, target, nil))

	return stubAnswer{row: rec.Header().Get("X-Row"), method: rec.Header().Get("X-Method"), allow: rec.Header().Get("Allow")}
}

// namesHead reports whether the value of an Allow header names HEAD.
func namesHead(allow string) bool {
	return slices.Contains(strings.Split(allow, ", "), http.MethodHead)
}

// Each GET route of the router and of the Unix socket serves HEAD, except a
// route WithoutHead. A Mux of the same rows with a stub for each shows
// where a HEAD request goes: to its GET route, with the method HEAD, or for a
// route WithoutHead to MethodNotAllowed or a route of every method, with an
// Allow that names HEAD only where a route serves it. The router answers HEAD
// with the status of GET.
func TestEveryGetRouteAnswersHead(t *testing.T) {
	useContractFixture(t)

	// as a role that allows everything, so that each handler runs
	server := serveContractAs(t, adminRole(), nil)

	socketMux, ok := socketRouter(t).(*routing.Mux)
	if !ok {
		t.Fatal("socketRouter() is not a *routing.Mux")
	}

	socket := httptest.NewServer(socketMux)
	t.Cleanup(socket.Close)

	router, ok := server.Config.Handler.(*routing.Mux)
	if !ok {
		t.Fatalf("contract server handler = %T, want *routing.Mux", server.Config.Handler)
	}

	// the router's API group has Options, and the Unix socket's has none
	for _, tt := range []struct {
		base       string
		mux        *routing.Mux
		apiOptions bool
	}{{server.URL, router, true}, {socket.URL, socketMux, false}} {
		stubs := stubMux(t, tt.mux, tt.apiOptions)

		for _, row := range tt.mux.Routes() {
			if row.Method != http.MethodGet {
				continue
			}

			t.Run(tt.base+" "+row.Path, func(t *testing.T) {
				testHeadOfRow(t, tt.base, tt.mux, stubs, row)
			})
		}
	}
}

// testHeadOfRow tests the HEAD of the GET route row of mux, served at base,
// through stubs, a stubMux of mux.
func testHeadOfRow(t *testing.T, base string, mux, stubs *routing.Mux, row routing.Route) {
	t.Helper()

	var (
		self   = row.Method + " " + row.Path
		route  = servedRoute{method: http.MethodHead, path: row.Path, queries: handlerQueries[self], ofGet: true}
		target = route.concrete()
		req    = httptest.NewRequest(http.MethodGet, target, nil)
	)

	if got := answerOf(stubs, http.MethodGet, target).row; got != self {
		t.Fatalf("GET %s reaches %q, want %q", target, got, self)
	}

	if diff := cmp.Diff(mux.Allowed(req), stubs.Allowed(req)); diff != "" {
		t.Fatalf("Allowed(%s) of the stubs mismatch (-router +stubs):\n%s", target, diff)
	}

	head := answerOf(stubs, http.MethodHead, target)
	options := answerOf(stubs, http.MethodOptions, target)
	served := head.row != "405"

	if head.method != http.MethodHead {
		t.Errorf("HEAD %s reaches %q with the method %q, want HEAD", target, head.row, head.method)
	}

	if allowed := strings.Join(stubs.Allowed(req), ", "); namesHead(allowed) != served {
		t.Errorf("Allowed(%s) = %q, and HEAD reaches %q", target, allowed, head.row)
	}

	for _, answer := range []stubAnswer{head, options} {
		if answer.allow != "" && namesHead(answer.allow) != served {
			t.Errorf("%s %s Allow = %q, and HEAD reaches %q", answer.method, target, answer.allow, head.row)
		}
	}

	if row.RefusesHead() {
		// a route of every method has no method in its row
		if served && !strings.HasPrefix(head.row, " ") {
			t.Errorf("HEAD %s of a route WithoutHead reaches %q, want 405 or a route of every method", target, head.row)
		}

		return
	}

	if head.row != self {
		t.Errorf("HEAD %s reaches %q, want %q", target, head.row, self)
	}

	headStatus := do(t, http.MethodHead, base+target, "").StatusCode
	if getStatus := do(t, http.MethodGet, base+target, "").StatusCode; headStatus != getStatus {
		t.Errorf("HEAD %s = %d, want the status of GET %d", target, headStatus, getStatus)
	}
}
