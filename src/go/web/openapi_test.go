package web

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gorilla/mux"
)

var errRoutesInspected = errors.New("routes inspected")

// operation is the subset of a route's shape that the router and the OpenAPI
// document must agree on.
type operation struct {
	pathParams  []string
	queryParams []string
}

func TestOpenAPI(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("load OpenAPI document: %v", err)
	}

	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI document: %v", err)
	}

	registered := registeredAPIRoutes(t)
	documented := documentedAPIRoutes(t, document)

	for route, want := range registered {
		got, ok := documented[route]
		if !ok {
			t.Errorf("route is not documented: %s", route)

			continue
		}

		if !slices.Equal(want.pathParams, got.pathParams) {
			t.Errorf(
				"%s: documented path parameters %v do not match the route's %v",
				route, got.pathParams, want.pathParams,
			)
		}

		// The router matches on these, so a request without them 404s. Documenting
		// them as optional (or not at all) would be wrong.
		if !slices.Equal(want.queryParams, got.queryParams) {
			t.Errorf(
				"%s: documented required query parameters %v do not match the route's %v",
				route, got.queryParams, want.queryParams,
			)
		}
	}

	for route := range documented {
		if _, ok := registered[route]; !ok {
			t.Errorf("documented route is not registered: %s", route)
		}
	}
}

// TestOpenAPIOperationsAreDescribed keeps every operation groupable and linkable
// in the rendered docs.
func TestOpenAPIOperationsAreDescribed(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("load OpenAPI document: %v", err)
	}

	ids := make(map[string]string)

	for path, item := range document.Paths {
		for _, method := range httpMethods() {
			op := item.GetOperation(method)
			if op == nil {
				continue
			}

			route := method + " " + path

			if op.Summary == "" {
				t.Errorf("%s: missing summary", route)
			}

			if len(op.Tags) == 0 {
				t.Errorf("%s: missing tags", route)
			}

			if op.OperationID == "" {
				t.Errorf("%s: missing operationId", route)

				continue
			}

			if other, ok := ids[op.OperationID]; ok {
				t.Errorf("%s: operationId %q is also used by %s", route, op.OperationID, other)
			}

			ids[op.OperationID] = route
		}
	}
}

// registeredAPIRoutes walks the routes the server registers under /api/v1.
//
// Routes served outside that subrouter are deliberately out of scope: the
// top-level /features, /version, /builder, /builder/save and
// /downloads/tunneler/{name} handlers, and the separate file server listener's
// /, /login and /upload handlers.
func registeredAPIRoutes(t *testing.T) map[string]operation {
	t.Helper()

	routes := make(map[string]operation)
	err := start(func(router *mux.Router) error {
		err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
			path, err := route.GetPathTemplate()
			if err != nil {
				return err
			}

			methods, err := route.GetMethods()
			if err != nil {
				return err
			}

			queries, err := route.GetQueriesTemplates()
			if err != nil {
				// A route without queries reports an error rather than an empty list.
				queries = nil
			}

			op := operation{
				pathParams:  muxPathParams(path),
				queryParams: muxQueryParams(queries),
			}

			path = strings.TrimPrefix(path, "/api/v1")
			for _, method := range methods {
				if method != "OPTIONS" {
					routes[method+" "+canonicalPath(path)] = op
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		return errRoutesInspected
	}, ServeUnbundled(), ServeWithFeatures([]string{"vm-mount", "tunneler-download"}))
	if !errors.Is(err, errRoutesInspected) {
		t.Fatalf("inspect registered API routes: %v", err)
	}

	return routes
}

func documentedAPIRoutes(t *testing.T, document *openapi3.T) map[string]operation {
	t.Helper()

	routes := make(map[string]operation)
	seen := make(map[string]string)

	for path, item := range document.Paths {
		// canonicalPath erases parameter names, so two paths that differ only in
		// those names would silently overwrite each other below.
		if other, ok := seen[canonicalPath(path)]; ok {
			t.Errorf("documented paths %s and %s are the same route", other, path)
		}

		seen[canonicalPath(path)] = path

		for _, method := range httpMethods() {
			op := item.GetOperation(method)
			if op == nil {
				continue
			}

			params := append(openapi3.Parameters{}, item.Parameters...)
			params = append(params, op.Parameters...)

			// The template is what a client actually substitutes into, so it is
			// what gets compared against the router; the declarations only
			// describe it.
			templateParams := muxPathParams(path)

			declared := documentedParams(params, openapi3.ParameterInPath, false)
			if !slices.Equal(templateParams, declared) {
				t.Errorf(
					"%s %s: declared path parameters %v do not match the path template %v",
					method, path, declared, templateParams,
				)
			}

			routes[method+" "+canonicalPath(path)] = operation{
				pathParams:  templateParams,
				queryParams: documentedParams(params, openapi3.ParameterInQuery, true),
			}
		}
	}

	return routes
}

// documentedParams returns the sorted names of parameters declared for the given
// location, optionally limited to required ones.
func documentedParams(params openapi3.Parameters, in string, requiredOnly bool) []string {
	var names []string

	for _, ref := range params {
		if ref.Value == nil || ref.Value.In != in {
			continue
		}

		if requiredOnly && !ref.Value.Required {
			continue
		}

		names = append(names, ref.Value.Name)
	}

	slices.Sort(names)

	return slices.Compact(names)
}

// muxPathParams returns the sorted variable names in a mux path template,
// dropping any pattern suffix (e.g. "{cols:[0-9]+}" is the "cols" variable).
func muxPathParams(path string) []string {
	var names []string

	for {
		start := strings.IndexByte(path, '{')
		if start == -1 {
			break
		}

		end := strings.IndexByte(path[start:], '}')
		if end == -1 {
			break
		}

		names = append(names, muxVarName(path[start+1:start+end]))
		path = path[start+end+1:]
	}

	slices.Sort(names)

	return slices.Compact(names)
}

// muxQueryParams returns the sorted keys of a route's query templates
// (e.g. "disk={disk}" is the "disk" key).
func muxQueryParams(queries []string) []string {
	var names []string

	for _, query := range queries {
		key, _, found := strings.Cut(query, "=")
		if !found {
			continue
		}

		names = append(names, key)
	}

	slices.Sort(names)

	return slices.Compact(names)
}

// muxVarName strips the optional pattern from a mux variable declaration.
func muxVarName(v string) string {
	name, _, _ := strings.Cut(v, ":")

	return name
}

func httpMethods() []string {
	return []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "CONNECT", "OPTIONS", "TRACE"}
}

func canonicalPath(path string) string {
	var result strings.Builder

	for {
		start := strings.IndexByte(path, '{')
		if start == -1 {
			result.WriteString(path)
			break
		}

		result.WriteString(path[:start])
		end := strings.IndexByte(path[start:], '}')
		if end == -1 {
			result.WriteString(path[start:])
			break
		}

		result.WriteString("{}")
		path = path[start+end+1:]
	}

	return result.String()
}
