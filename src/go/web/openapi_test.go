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

// openAPISpec maps each openapi.yml operation, keyed by scope (api or root), method and path,
// to its response statuses.
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
		t.Fatalf("reading openapi.yml: %v", err)
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
				t.Fatalf("decoding openapi.yml %s servers: %v", path, err)
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
				t.Fatalf("decoding openapi.yml %s %s: %v", method, path, err)
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

// routerOperations are the router's operations, keyed as in an openAPISpec, with all features on,
// plus the paths of any-method routes (OPTIONS left out).
func routerOperations(t *testing.T) (map[string]bool, []string) {
	t.Helper()

	server := serveContract(t, func(o *serverOptions) {
		o.features = map[string]bool{"webshark": true, "vm-mount": true, "tunneler-download": true}
	})

	router, ok := server.Config.Handler.(*mux.Router)
	if !ok {
		t.Fatalf("contract server handler = %T, want *mux.Router", server.Config.Handler)
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
		t.Fatalf("Router.Walk() error = %v", err)
	}

	return ops, anyMethod
}

// routesOfAnyMethod are routes taking any method: UI files openapi.yml omits, and the API subrouter.
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

// Every router operation is in openapi.yml and vice versa, except any-method routes;
// the Unix socket's routes are among the router's.
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
		if _, ok := spec[key]; !ok {
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
}
