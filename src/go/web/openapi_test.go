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
