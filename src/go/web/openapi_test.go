package web

import (
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
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

// documents reports whether the spec documents status for a request: the
// path, with its query, is concrete, as sent to the server.
func (s openAPISpec) documents(method, path string, status int) bool {
	path, _, _ = strings.Cut(path, "?")

	scope := "root"
	if rest, ok := strings.CutPrefix(path, "/api/v1"); ok {
		scope, path = "api", rest
	}

	prefix := scope + " " + method + " "

	for key, statuses := range s {
		template, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}

		pattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(template), `\{\}`, `[^/]+`) + "$"
		if regexp.MustCompile(pattern).MatchString(path) {
			return slices.Contains(statuses, strconv.Itoa(status))
		}
	}

	return false
}

// routerOperations are the operations of the server's routes, keyed as in an
// openAPISpec, with every optional feature on, and the paths of the routes of
// any method. OPTIONS, which every API route takes for CORS preflights, is
// left out.
func routerOperations(t *testing.T) (map[string]bool, []string) {
	t.Helper()

	server := serveContract(t, func(o *serverOptions) {
		o.features = map[string]bool{"webshark": true, "vm-mount": true, "tunneler-download": true}
	})

	router, ok := server.Config.Handler.(*mux.Router)
	if !ok {
		t.Fatalf("the server's handler is a %T", server.Config.Handler)
	}

	var (
		ops       = map[string]bool{}
		anyMethod []string
	)

	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil {
			return nil //nolint:nilerr // a route of no path, such as a subrouter's
		}

		methods, err := route.GetMethods()
		if err != nil {
			anyMethod = append(anyMethod, path)

			return nil //nolint:nilerr // a route of any method, which the caller checks
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

	return ops, anyMethod
}

// routesOfAnyMethod are the routes, by path or path prefix, that take any
// method: the UI's files, which openapi.yml leaves out, and the API's
// subrouter, whose routes each name their methods.
//
//nolint:gochecknoglobals // constant table
var routesOfAnyMethod = map[string]string{
	"/api/v1":                        "the API's subrouter; its routes are walked one by one",
	"/webshark/":                     "WebShark's pages and scripts, behind its own routes",
	"/docs/":                         "the documentation site",
	"/novnc/":                        "noVNC, which the VNC page loads",
	"/xterm.js/":                     "xterm.js, which the console page loads",
	"/assets/":                       "the UI's hashed scripts, styles and images",
	"/grapheditor/builder.bundle.js": "the Topology Builder's scripts, bundled",
	"/grapheditor/":                  "the Topology Builder's other files",
	"/favicon.ico":                   "the UI's icon",
}

// undocumentedRoutes are the routes openapi.yml leaves out on purpose, each
// with the reason.
//
//nolint:gochecknoglobals // constant table
var undocumentedRoutes = map[string]string{
	// pages and forms of the UI, not an API
	"root GET /builder":       "the Topology Builder page",
	"root POST /builder/save": "the Topology Builder's file download form",
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
// described, each with what it does. They predate the API contract tests,
// and leave openapi.yml as they are documented; a new route may not join them.
//
//nolint:gochecknoglobals // constant table
var undocumentedBeforeContract = map[string]string{
	"api DELETE /experiments/{}/netflow":                    "stops the experiment's netflow capture",
	"api DELETE /experiments/{}/trigger":                    "cancels the experiment's triggered apps",
	"api DELETE /experiments/{}/vms/{}/cdrom":               "ejects a VM's CD-ROM",
	"api DELETE /experiments/{}/vms/{}/forwards":            "removes a VM's port forward",
	"api DELETE /experiments/{}/vms/{}/unmount":             "unmounts a VM's disk",
	"api GET /experiments/{}/apps":                          "lists the experiment's apps",
	"api GET /experiments/{}/netflow":                       "the experiment's netflow capture",
	"api GET /experiments/{}/scorch/components/{}/{}/{}/{}": "a SCORCH component's output",
	"api GET /experiments/{}/scorch/terminals":              "lists the SCORCH break terminals",
	"api GET /experiments/{}/scorch/terminals/{}":           "a SCORCH break terminal",
	"api GET /experiments/{}/scorch/terminals/{}/{}/{}/{}":  "a SCORCH component's break terminal",
	"api GET /experiments/{}/soh":                           "the experiment's state of health",
	"api GET /experiments/{}/topology":                      "the experiment's network graph",
	"api GET /experiments/{}/topology/search":               "searches the experiment's network graph",
	"api GET /experiments/{}/vms/{}/files":                  "lists a mounted VM disk's files",
	"api GET /experiments/{}/vms/{}/forwards":               "lists a VM's port forwards",
	"api GET /experiments/{}/vms/{}/reset":                  "resets a VM's disk state",
	"api GET /experiments/{}/vms/{}/restart":                "restarts a VM",
	"api GET /experiments/{}/vms/{}/shutdown":               "shuts a VM down",
	"api GET /experiments/{}/vms/{}/vnc":                    "a VM's VNC page",
	"api GET /logs":                                         "streams the server's logs",
	"api GET /roles":                                        "lists the roles",
	"api GET /settings":                                     "the server's settings",
	"api GET /settings/password":                            "the password requirements",
	"api GET /settings/timeout":                             "the session timeout",
	"api GET /topologies/{}/scenarios":                      "lists a topology's scenarios",
	"api PATCH /experiments/{}/vms":                         "updates several VMs",
	"api POST /configs/download":                            "downloads several configs as an archive",
	"api POST /experiments/{}/captureSubnet":                "captures a subnet's VM interfaces",
	"api POST /experiments/{}/netflow":                      "starts a netflow capture",
	"api POST /experiments/{}/scorch/terminals/{}/exit/{}":  "exits a SCORCH break terminal",
	"api POST /experiments/{}/stopCaptureSubnet":            "stops a subnet's captures",
	"api POST /experiments/{}/trigger":                      "triggers the experiment's apps",
	"api POST /experiments/{}/vms/{}/cdrom":                 "changes a VM's CD-ROM",
	"api POST /experiments/{}/vms/{}/commit":                "commits a VM's disk to a new image",
	"api POST /experiments/{}/vms/{}/files/copy":            "copies an experiment file into a mounted VM disk",
	"api POST /experiments/{}/vms/{}/forwards":              "adds a VM port forward",
	"api POST /experiments/{}/vms/{}/memorySnapshot":        "snapshots a VM's memory",
	"api POST /experiments/{}/vms/{}/mount":                 "mounts a VM's disk",
	"api POST /settings":                                    "changes the server's settings",
	"api POST /users/{}/tokens":                             "creates a user's API token",
	"api PUT /experiments/{}/vms/{}/files/upload":           "uploads a file into a mounted VM disk",
}

// Every operation the router serves is in openapi.yml, and every operation
// openapi.yml describes is one the router serves, except the routes left out
// on purpose. The routes the Unix socket serves are among the router's.
func TestRouterMatchesOpenAPI(t *testing.T) {
	spec := loadOpenAPI(t)
	router, anyMethod := routerOperations(t)

	// an API route must name its methods to be compared with openapi.yml
	for _, path := range anyMethod {
		if routesOfAnyMethod[path] == "" {
			t.Errorf("route %s takes any method: give it Methods(), or add it to routesOfAnyMethod", path)
		}
	}

	for path := range routesOfAnyMethod {
		if !slices.Contains(anyMethod, path) {
			t.Errorf("%s is no longer a route of any method: take it off routesOfAnyMethod", path)
		}
	}

	for _, r := range socketRoutes() {
		for _, method := range r.methods {
			if key := openAPIKey("api", method, r.path); !router[key] {
				t.Errorf("socket route %s is not an HTTP API route", key)
			}
		}
	}

	var missing, extra []string

	for key := range router {
		if _, ok := spec[key]; !ok && undocumentedRoutes[key] == "" && undocumentedBeforeContract[key] == "" {
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

	for key := range undocumentedBeforeContract {
		if _, ok := spec[key]; ok || !router[key] {
			t.Errorf("%s is described in openapi.yml, or no longer a route: take it off undocumentedBeforeContract", key)
		}
	}
}
