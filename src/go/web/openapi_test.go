package web

import (
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

// openAPISpec is what the tests read of openapi.yml: each operation's
// responses, by scope ("api" for a path under /api/v1, "root" for one a
// path-level server puts at the server root), method and path.
type openAPISpec map[string][]string

// openAPIKey is an operation's key in an openAPISpec: scope, method and the
// path with every parameter written {}.
func openAPIKey(scope, method, path string) string {
	return scope + " " + strings.ToUpper(method) + " " + pathParams.ReplaceAllString(path, "{}")
}

// pathParams are the parameters of a path template, as OpenAPI and gorilla
// write them (gorilla's with an optional pattern).
var pathParams = regexp.MustCompile(`\{[^}]*\}`)

// loadOpenAPI reads the operations of public/docs/openapi.yml.
func loadOpenAPI(t *testing.T) openAPISpec {
	t.Helper()

	raw, err := os.ReadFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}

	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing openapi.yml: %v", err)
	}

	spec := openAPISpec{}

	for path, item := range doc.Paths {
		scope := "api"

		if servers, ok := item["servers"]; ok {
			var list []struct {
				URL string `yaml:"url"`
			}

			if err := servers.Decode(&list); err != nil {
				t.Fatalf("%s: servers: %v", path, err)
			}

			if len(list) > 0 && !strings.HasSuffix(list[0].URL, "/api/v1") {
				scope = "root"
			}
		}

		for method, node := range item {
			if method == "servers" || method == "parameters" {
				continue
			}

			var op struct {
				Responses map[string]any `yaml:"responses"`
			}

			if err := node.Decode(&op); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}

			statuses := make([]string, 0, len(op.Responses))
			for status := range op.Responses {
				statuses = append(statuses, status)
			}

			spec[openAPIKey(scope, method, path)] = statuses
		}
	}

	return spec
}

// routerOperations are the operations of the server's routes, keyed as in an
// openAPISpec, with every optional feature on. Routes of no method (the UI's
// files and the WebShark proxy, served by prefix) and OPTIONS, which every API
// route takes for CORS preflights, are left out.
func routerOperations(t *testing.T) map[string]bool {
	t.Helper()

	server := serveContract(t, func(o *serverOptions) {
		o.features = map[string]bool{"webshark": true, "vm-mount": true, "tunneler-download": true}
	})

	router, ok := server.Config.Handler.(*mux.Router)
	if !ok {
		t.Fatalf("the server's handler is a %T", server.Config.Handler)
	}

	ops := map[string]bool{}

	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil {
			return nil //nolint:nilerr // a route of no path, such as a subrouter's
		}

		methods, err := route.GetMethods()
		if err != nil {
			return nil //nolint:nilerr // a route of any method
		}

		scope := "root"
		if rest, ok := strings.CutPrefix(path, "/api/v1"); ok {
			scope, path = "api", rest
		}

		for _, method := range methods {
			if method != http.MethodOptions {
				ops[openAPIKey(scope, method, path)] = true
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return ops
}

// undocumentedRoutes are the routes openapi.yml leaves out on purpose, each
// with the reason.
//
//nolint:gochecknoglobals // constant table
var undocumentedRoutes = map[string]string{
	// pages and forms of the UI, not an API
	"root GET /builder":       "the Topology Builder page",
	"root POST /builder/save": "the Topology Builder's file download form",
	"root GET /version":       "the build the UI shows",
	// WebShark's own requests, which only the bundled WebShark makes, with
	// the capture name POST /experiments/{exp}/webshark gives it
	"root GET /webshark/json":         "WebShark's capture listing and download",
	"root GET /webshark/captures/{}":  "WebShark's capture file",
	"root HEAD /webshark/captures/{}": "WebShark's capture file",
	"root GET /webshark/watch":        "WebShark's capture growth events",
	// websockets, which an OpenAPI 3.0 operation cannot describe
	"api GET /ws":                                              "the UI's event websocket",
	"api GET /experiments/{}/netflow/ws":                       "the netflow websocket",
	"api GET /experiments/{}/vms/{}/forwards/{}/{}/ws":         "the port forward websocket",
	"api GET /experiments/{}/scorch/components/{}/{}/{}/{}/ws": "the SCORCH component output websocket",
	"api GET /experiments/{}/scorch/terminals/{}/ws/{}":        "the SCORCH terminal websocket",
}

// undocumentedBeforeContract are the API routes openapi.yml has never
// described. They predate the API contract tests; a new route may not join
// them.
//
//nolint:gochecknoglobals // constant table
var undocumentedBeforeContract = []string{
	"api DELETE /experiments/{}/netflow",
	"api DELETE /experiments/{}/trigger",
	"api DELETE /experiments/{}/vms/{}/cdrom",
	"api DELETE /experiments/{}/vms/{}/forwards",
	"api DELETE /experiments/{}/vms/{}/unmount",
	"api GET /experiments/{}/apps",
	"api GET /experiments/{}/netflow",
	"api GET /experiments/{}/scorch/components/{}/{}/{}/{}",
	"api GET /experiments/{}/scorch/terminals",
	"api GET /experiments/{}/scorch/terminals/{}",
	"api GET /experiments/{}/scorch/terminals/{}/{}/{}/{}",
	"api GET /experiments/{}/soh",
	"api GET /experiments/{}/topology",
	"api GET /experiments/{}/topology/search",
	"api GET /experiments/{}/vms/{}/files",
	"api GET /experiments/{}/vms/{}/forwards",
	"api GET /experiments/{}/vms/{}/reset",
	"api GET /experiments/{}/vms/{}/restart",
	"api GET /experiments/{}/vms/{}/shutdown",
	"api GET /experiments/{}/vms/{}/vnc",
	"api GET /logs",
	"api GET /roles",
	"api GET /settings",
	"api GET /settings/password",
	"api GET /settings/timeout",
	"api GET /topologies/{}/scenarios",
	"api PATCH /experiments/{}",
	"api PATCH /experiments/{}/vms",
	"api POST /configs/download",
	"api POST /experiments/{}/captureSubnet",
	"api POST /experiments/{}/netflow",
	"api POST /experiments/{}/scorch/terminals/{}/exit/{}",
	"api POST /experiments/{}/stopCaptureSubnet",
	"api POST /experiments/{}/trigger",
	"api POST /experiments/{}/vms/{}/cdrom",
	"api POST /experiments/{}/vms/{}/commit",
	"api POST /experiments/{}/vms/{}/files/copy",
	"api POST /experiments/{}/vms/{}/forwards",
	"api POST /experiments/{}/vms/{}/memorySnapshot",
	"api POST /experiments/{}/vms/{}/mount",
	"api POST /settings",
	"api POST /users/{}/tokens",
	"api PUT /experiments/{}/vms/{}/files/upload",
}

// Every operation the router serves is in openapi.yml, and every operation
// openapi.yml describes is one the router serves, except the routes left out
// on purpose. The routes the Unix socket serves are among the router's.
func TestRouterMatchesOpenAPI(t *testing.T) {
	var (
		spec   = loadOpenAPI(t)
		router = routerOperations(t)
	)

	for _, r := range socketRoutes() {
		for _, method := range r.methods {
			if key := openAPIKey("api", method, r.path); !router[key] {
				t.Errorf("socket route %s is not an HTTP API route", key)
			}
		}
	}

	var missing, extra []string

	for key := range router {
		if _, ok := spec[key]; !ok && undocumentedRoutes[key] == "" && !slices.Contains(undocumentedBeforeContract, key) {
			missing = append(missing, key)
		}
	}

	for key := range spec {
		if !router[key] {
			extra = append(extra, key)
		}
	}

	slices.Sort(missing)
	slices.Sort(extra)

	for _, key := range missing {
		t.Errorf("openapi.yml does not describe the router's %s", key)
	}

	for _, key := range extra {
		t.Errorf("openapi.yml describes %s, which the router does not serve", key)
	}

	for key := range undocumentedRoutes {
		if _, ok := spec[key]; ok || !router[key] {
			t.Errorf("%s is described in openapi.yml, or no longer a route: take it off undocumentedRoutes", key)
		}
	}

	for _, key := range undocumentedBeforeContract {
		if _, ok := spec[key]; ok || !router[key] {
			t.Errorf("%s is described in openapi.yml, or no longer a route: take it off undocumentedBeforeContract", key)
		}
	}
}
