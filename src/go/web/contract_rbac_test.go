package web

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// servedRoute is one method of a route the router serves: its path template
// and its query templates.
type servedRoute struct {
	method, path string
	queries      []string
}

func (r servedRoute) String() string { return r.method + " " + r.path }

// servedRoutes are the router's operations that name their methods, with
// every feature on, OPTIONS left out.
func servedRoutes(t *testing.T) []servedRoute {
	t.Helper()

	server := serveContract(t, nil)

	router, ok := server.Config.Handler.(*mux.Router)
	if !ok {
		t.Fatalf("contract server handler = %T, want *mux.Router", server.Config.Handler)
	}

	var routes []servedRoute

	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil {
			return nil //nolint:nilerr // a route of no path
		}

		methods, err := route.GetMethods()
		if err != nil {
			return nil //nolint:nilerr // a route of any method, which TestRouterMatchesOpenAPI checks
		}

		templates, _ := route.GetQueriesTemplates()

		for _, method := range methods {
			queries := templates
			if len(queries) == 0 {
				queries = handlerQueries[method+" "+path]
			}

			if method != http.MethodOptions {
				routes = append(routes, servedRoute{method: method, path: path, queries: queries})
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Router.Walk() error = %v", err)
	}

	return routes
}

// handlerQueries are the query parameters that a route's handler, not the router, requires.
//
//nolint:gochecknoglobals // constant table
var handlerQueries = map[string][]string{
	"POST /api/v1/disks/snapshot":   {"disk", "new"},
	"POST /api/v1/disks/rebase":     {"disk", "backing", "unsafe"},
	"POST /api/v1/disks/resize":     {"disk", "size"},
	"POST /api/v1/disks/commit":     {"disk"},
	"POST /api/v1/disks/clone":      {"disk", "new"},
	"POST /api/v1/disks/clone/jobs": {"disk", "new"},
	"DELETE /api/v1/disks":          {"disk"},
	"POST /api/v1/disks/rename":     {"disk", "new"},
	"GET /api/v1/disks/download":    {"disk"},
	"HEAD /api/v1/disks/download":   {"disk"},
	"GET /api/v1/disks/checksum":    {"disk"},
	"GET /api/v1/disks/details":     {"disk"},
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

		name, _, _ := strings.Cut(strings.Trim(segment, "{}"), ":")

		value, ok := routeParams[segments[i-1]+"/{"+name+"}"]
		if !ok {
			value = routeParams["{"+name+"}"]
		}

		segments[i] = value
	}

	query := make([]string, 0, len(r.queries))
	for _, q := range r.queries {
		name, _, _ := strings.Cut(q, "=")
		query = append(query, name+"="+routeParams[name])
	}

	path := strings.Join(segments, "/")
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}

	return path
}

// rbacExempt are the routes a role of no permissions may call, with why: those outside /api/v1 and its auth,
// and API routes every signed-in user may call or that act only on what the role may see.
//
//nolint:gochecknoglobals // constant table
var rbacExempt = map[string]string{
	"GET /features":                                       "outside API auth: the features the UI offers",
	"GET /version":                                        "outside API auth: the server's build",
	"GET /healthz":                                        "outside API auth: liveness probe",
	"HEAD /healthz":                                       "outside API auth: liveness probe",
	"GET /readyz":                                         "outside API auth: readiness probe",
	"HEAD /readyz":                                        "outside API auth: readiness probe",
	"GET /docs/openapi.yml":                               "outside API auth: this API's description",
	"HEAD /docs/openapi.yml":                              "outside API auth: this API's description",
	"GET /downloads/tunneler":                             "outside API auth: tunneler builds to download",
	"GET /downloads/tunneler/{name}":                      "outside API auth: a tunneler build",
	"HEAD /downloads/tunneler/{name}":                     "outside API auth: a tunneler build",
	"GET /webshark/streams/{token}":                       "outside API auth: the capture grant in the path is the credential",
	"GET /webshark/json":                                  "outside API auth: the capture grant in the query is the credential",
	"GET /webshark/captures/{name}":                       "outside API auth: the capture grant in the path is the credential",
	"HEAD /webshark/captures/{name}":                      "outside API auth: the capture grant in the path is the credential",
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
// another user's job.
//
//nolint:gochecknoglobals // constant table
var rbacHidden = []string{
	"GET /api/v1/jobs/{id}",
	"POST /api/v1/jobs/{id}/cancel",
	"DELETE /api/v1/jobs/{id}",
	"GET /api/v1/jobs/{id}/artifact",
	"HEAD /api/v1/jobs/{id}/artifact",
}

// Every route refuses a role of no permissions with 401 or 403 (404 if rbacHidden) unless rbacExempt lists it;
// requests carry contractRoutes' body, so the refusal is RBAC's rather than the body's.
func TestEveryRouteChecksPermissions(t *testing.T) {
	routes := servedRoutes(t)

	bodies := map[string]contractRoute{}
	for _, c := range contractRoutes() {
		bodies[c.method+" "+c.path] = c
	}

	served := map[string]bool{}

	for _, route := range routes {
		served[route.String()] = true

		if rbacExempt[route.String()] != "" {
			continue
		}

		t.Run(route.String(), func(t *testing.T) {
			f := useContractFixture(t)
			server := serveContractAs(t, combinedRole(), func(o *serverOptions) { o.minimegaConsole = true }).URL

			c := contractRoute{method: route.method, path: route.concrete(), body: "{}"}
			if known, ok := bodies[c.String()]; ok {
				c = known
			}

			resp := do(t, c.method, c.url(t, server, f, "alice"), c.body, c.header...)

			want := []int{http.StatusUnauthorized, http.StatusForbidden}
			if slices.Contains(rbacHidden, route.String()) {
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
		"pattern dropped":          {servedRoute{path: "/api/v1/console/{pid:[0-9]+}"}, "/api/v1/console/1"},
		"job":                      {servedRoute{path: "/api/v1/jobs/{id}"}, "/api/v1/jobs/{job}"},
		"queries": {
			servedRoute{path: "/api/v1/console/{pid}/size", queries: []string{"cols={cols:[0-9]+}", "rows={rows:[0-9]+}"}},
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
