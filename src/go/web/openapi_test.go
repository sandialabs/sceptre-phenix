package web

import (
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"

	"phenix/version"
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

	// a literal path such as /vlans/free wins over a template such as /vlans/{}
	var (
		best  []string
		found = false
		holes = 0
	)

	for key, statuses := range s {
		template, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}

		pattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(template), `\{\}`, `[^/]+`) + "$"
		if n := strings.Count(template, "{}"); regexp.MustCompile(pattern).MatchString(path) && (!found || n < holes) {
			best, found, holes = statuses, true, n
		}
	}

	return found && slices.Contains(best, strconv.Itoa(status))
}

// routerOperations are the router's operations, keyed as in an openAPISpec, with all features on,
// each with the query parameters it needs, plus the paths of any-method routes (OPTIONS left out).
func routerOperations(t *testing.T) (map[string][]string, []string) {
	t.Helper()

	server := serveContract(t, func(o *serverOptions) {
		o.features = map[string]bool{"webshark": true, "vm-mount": true, "tunneler-download": true}
	})

	router, ok := server.Config.Handler.(*mux.Router)
	if !ok {
		t.Fatalf("contract server handler = %T, want *mux.Router", server.Config.Handler)
	}

	return walkOperations(t, router)
}

// walkOperations are the operations of router, as routerOperations returns them.
func walkOperations(t *testing.T, router *mux.Router) (map[string][]string, []string) {
	t.Helper()

	var (
		ops       = map[string][]string{}
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

		// a route of no query templates gives an error
		queries, _ := route.GetQueriesTemplates()
		names := make([]string, 0, len(queries))

		for _, query := range queries {
			name, _, _ := strings.Cut(query, "=")
			names = append(names, name)
		}

		for _, method := range methods {
			if method == http.MethodOptions {
				continue
			}

			key := openAPIKey(scope, method, path)

			cur := slices.Clone(names)

			// two routes of one key need only the queries that both need
			if prior, ok := ops[key]; ok {
				cur = slices.DeleteFunc(cur, func(n string) bool { return !slices.Contains(prior, n) })
			}

			ops[key] = cur
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Router.Walk() error = %v", err)
	}

	return ops, anyMethod
}

// Each method of a route gets its own queries, cut only by an earlier route of
// that method and path.
func TestWalkOperationsQueriesPerMethod(t *testing.T) {
	router := mux.NewRouter()
	router.Methods(http.MethodGet).Path("/x").Queries("a", "{a}")
	router.Methods(http.MethodGet, http.MethodPost).Path("/x").Queries("a", "{a}", "b", "{b}")

	ops, _ := walkOperations(t, router)

	want := map[string][]string{"root GET /x": {"a"}, "root POST /x": {"a", "b"}}
	if diff := cmp.Diff(want, ops); diff != "" {
		t.Errorf("walkOperations() mismatch (-want +got):\n%s", diff)
	}
}

// routesOfAnyMethod are routes taking any method: UI files openapi.yml omits, and the API subrouter.
//
//nolint:gochecknoglobals // constant table
var routesOfAnyMethod = map[string]string{
	"/api/v1":      "the API's subrouter; its routes are walked one by one",
	"/webshark/":   "WebShark's pages and scripts, behind its own routes",
	"/docs/":       "the documentation site",
	"/novnc/":      "noVNC, which the VNC page loads",
	"/xterm.js/":   "xterm.js, which the console page loads",
	"/assets/":     "the UI's hashed scripts, styles and images",
	"/favicon.ico": "the UI's icon",
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
			if key := openAPIKey("api", method, r.path); router[key] == nil {
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
		if _, ok := router[key]; !ok {
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

// TestRouterPathParametersMatchOpenAPI asserts that openapi.yml names the
// path parameters of each route as the router does, in the same order, which
// TestRouterMatchesOpenAPI does not compare.
func TestRouterPathParametersMatchOpenAPI(t *testing.T) {
	spec := map[string][]string{}

	for path := range loadOpenAPIDocument(t).Paths {
		spec[pathParams.ReplaceAllString(path, "{}")] = templateNames(path)
	}

	server := serveContract(t, func(o *serverOptions) {
		o.features = map[string]bool{"webshark": true, "vm-mount": true, "tunneler-download": true}
	})

	router, ok := server.Config.Handler.(*mux.Router)
	if !ok {
		t.Fatalf("contract server handler = %T, want *mux.Router", server.Config.Handler)
	}

	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil {
			return nil //nolint:nilerr // a route of no path, such as a subrouter's
		}

		path = strings.TrimPrefix(path, "/api/v1")

		want, ok := spec[pathParams.ReplaceAllString(path, "{}")]
		if !ok {
			return nil
		}

		if got := templateNames(path); !slices.Equal(got, want) {
			t.Errorf("route %s path parameters = %v, openapi.yml names them %v", path, got, want)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Router.Walk() error = %v", err)
	}
}

// templateNames are the names of the variables of a path template, in order,
// without a gorilla pattern such as {id:[0-9]+}.
func templateNames(path string) []string {
	matches := pathParams.FindAllString(path, -1)
	names := make([]string, 0, len(matches))

	for _, match := range matches {
		name, _, _ := strings.Cut(strings.Trim(match, "{}"), ":")
		names = append(names, name)
	}

	return names
}

// GET /docs/openapi.yml serves the spec with info.version set to the build,
// leaving every other line as it is, and with the build in X-Phenix-Version.
func TestOpenAPISpecHandler(t *testing.T) {
	original := version.Tag
	version.Tag = "v1.2.3" //nolint:reassign // a release build

	t.Cleanup(func() { version.Tag = original }) //nolint:reassign // restore

	const spec = "openapi: 3.0.0\ninfo:\n  version: 0.0.0\n  title: phenix\npaths:\n" +
		"  /a:\n    get:\n      parameters:\n        - name: version\n          example:\n            version: 0.0.0\n"

	h := OpenAPISpecHandler(http.FS(fstest.MapFS{"docs/openapi.yml": {Data: []byte(spec)}}))

	resp := get(t, h, "/docs/openapi.yml", "gzip")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /docs/openapi.yml = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	want := strings.Replace(spec, "  version: 0.0.0\n", "  version: 1.2.3\n", 1)
	if diff := cmp.Diff(want, readBody(t, resp)); diff != "" {
		t.Errorf("GET /docs/openapi.yml mismatch (-want +got):\n%s", diff)
	}

	if got := resp.Header.Get("X-Phenix-Version"); got != "1.2.3" {
		t.Errorf("GET /docs/openapi.yml X-Phenix-Version = %q, want 1.2.3", got)
	}

	if got := resp.Header.Get("Content-Type"); got != "application/yaml" {
		t.Errorf("GET /docs/openapi.yml Content-Type = %q, want application/yaml", got)
	}

	resp = getIfNoneMatch(t, h, "/docs/openapi.yml", "gzip", resp.Header.Get("ETag"))
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("GET /docs/openapi.yml If-None-Match = %d, want %d", resp.StatusCode, http.StatusNotModified)
	}

	missing := OpenAPISpecHandler(http.FS(fstest.MapFS{}))
	if resp := get(t, missing, "/docs/openapi.yml", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /docs/openapi.yml without one = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// The served openapi.yml's info.version is the build's, and it is still the
// same document otherwise.
func TestOpenAPISpecHandlerStampsTheSpec(t *testing.T) {
	assets, err := GetAssets()
	if err != nil {
		t.Fatal(err)
	}

	resp := get(t, OpenAPISpecHandler(assets), "/docs/openapi.yml", "")

	var served, source struct {
		Info  map[string]any `yaml:"info"`
		Paths map[string]any `yaml:"paths"`
	}

	if err := yaml.Unmarshal([]byte(readBody(t, resp)), &served); err != nil {
		t.Fatalf("parsing the served openapi.yml: %v", err)
	}

	raw, err := os.ReadFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatal(err)
	}

	if err := yaml.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}

	if got := served.Info["version"]; got != version.API() {
		t.Errorf("served info.version = %v, want %q", got, version.API())
	}

	source.Info["version"] = version.API()
	if diff := cmp.Diff(source, served); diff != "" {
		t.Errorf("served openapi.yml mismatch apart from info.version (-want +got):\n%s", diff)
	}
}

func loadOpenAPIDocument(t *testing.T) *openapi3.T {
	t.Helper()

	doc, err := openapi3.NewLoader().LoadFromFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("loading openapi.yml: %v", err)
	}

	return doc
}

// TestOpenAPIDocumentIsValid asserts the served API reference is a valid
// OpenAPI 3 document.
func TestOpenAPIDocumentIsValid(t *testing.T) {
	doc := loadOpenAPIDocument(t)

	if err := doc.Validate(t.Context()); err != nil {
		t.Errorf("openapi.yml is not a valid OpenAPI document: %v", err)
	}
}

// TestOpenAPIPathParametersMatchTemplates asserts every operation names the
// variables of its path template, which Validate checks only by count.
func TestOpenAPIPathParametersMatchTemplates(t *testing.T) {
	variable := regexp.MustCompile(`\{([^}]+)\}`)

	for path, item := range loadOpenAPIDocument(t).Paths {
		matches := variable.FindAllStringSubmatch(path, -1)
		want := make([]string, 0, len(matches))

		for _, match := range matches {
			want = append(want, match[1])
		}

		slices.Sort(want)

		for method, op := range item.Operations() {
			var got []string

			for _, params := range []openapi3.Parameters{item.Parameters, op.Parameters} {
				for _, ref := range params {
					if ref.Value != nil && ref.Value.In == openapi3.ParameterInPath {
						got = append(got, ref.Value.Name)
					}
				}
			}

			slices.Sort(got)

			if !slices.Equal(got, want) {
				t.Errorf("%s %s path parameters = %v, want %v", method, path, got, want)
			}
		}
	}
}

// TestOpenAPIOperationsAreDescribed asserts every operation has a summary, a
// tag and an operationId that no other operation has.
func TestOpenAPIOperationsAreDescribed(t *testing.T) {
	ids := map[string]string{}

	for path, item := range loadOpenAPIDocument(t).Paths {
		for method, op := range item.Operations() {
			name := method + " " + path

			if op.Summary == "" {
				t.Errorf("%s has no summary", name)
			}

			if len(op.Tags) == 0 {
				t.Errorf("%s has no tags", name)
			}

			if op.OperationID == "" {
				t.Errorf("%s has no operationId", name)

				continue
			}

			if other, ok := ids[op.OperationID]; ok {
				t.Errorf("%s and %s have the same operationId %q", name, other, op.OperationID)
			}

			ids[op.OperationID] = name
		}
	}
}

// TestRouterQueriesAreRequiredParameters asserts openapi.yml makes each query
// parameter that a route needs (gorilla's Queries) a required parameter, since
// the router answers 404 without it.
func TestRouterQueriesAreRequiredParameters(t *testing.T) {
	router, _ := routerOperations(t)
	required := map[string][]string{}

	for path, item := range loadOpenAPIDocument(t).Paths {
		scope := "api"
		if len(item.Servers) > 0 && !strings.HasSuffix(item.Servers[0].URL, "/api/v1") {
			scope = "root"
		}

		for method, op := range item.Operations() {
			var names []string

			for _, params := range []openapi3.Parameters{item.Parameters, op.Parameters} {
				for _, ref := range params {
					if ref.Value != nil && ref.Value.In == openapi3.ParameterInQuery && ref.Value.Required {
						names = append(names, ref.Value.Name)
					}
				}
			}

			required[openAPIKey(scope, method, path)] = names
		}
	}

	for key, queries := range router {
		for _, name := range queries {
			if !slices.Contains(required[key], name) {
				t.Errorf("openapi.yml does not make %s query parameter %q required, which the router needs", key, name)
			}
		}
	}
}
