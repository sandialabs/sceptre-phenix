package routing_test

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/web/routing"
)

// named answers with its name in X-Handler and the template of the matched
// route in X-Template.
func named(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", name)
		w.Header().Set("X-Template", routing.Template(r))
		w.WriteHeader(http.StatusOK)
	})
}

// sent returns the headers that rec sent: those set before the status.
func sent(rec *httptest.ResponseRecorder) http.Header {
	res := rec.Result()
	_ = res.Body.Close()

	return res.Header
}

// trail is middleware that adds its name to X-Trail.
func trail(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("X-Trail", name)
			next.ServeHTTP(w, r)
		})
	}
}

// testMux has a root group, an API group with every flag, and a group with
// no handlers of its own.
func testMux(t *testing.T) *routing.Mux {
	t.Helper()

	m, err := routing.NewMux(
		routing.Group{
			Prefix: "",
			Routes: []routing.Route{
				routing.Row(http.MethodGet, "/page", named("page")),
				routing.Row("", "/files/", named("files")),
			},
			Middleware: []func(http.Handler) http.Handler{trail("root")},
			Options:    nil, NotFound: named("root-404"), MethodNotAllowed: nil,
		},
		routing.Group{
			Prefix: "/api/v1",
			Routes: []routing.Route{
				routing.Row(http.MethodGet, "/items", named("list")),
				routing.Row(http.MethodPost, "/items", named("create")),
				routing.Row(http.MethodGet, "/items/{id}", named("item")),
				routing.Row(http.MethodHead, "/items/{id}", named("item-head")),
				routing.Row(http.MethodGet, "/any/json", named("any-json")),
				routing.Row("", "/any/", named("any")),
				routing.Row(http.MethodGet, "/vlans/{id}", named("vlan")),
				routing.Row(http.MethodGet, "/vlans/free", named("free")),
			},
			Middleware:       []func(http.Handler) http.Handler{trail("a"), trail("b")},
			Options:          named("options"),
			NotFound:         named("api-404"),
			MethodNotAllowed: named("api-405"),
		},
		routing.Group{
			Prefix:     "/bare",
			Routes:     []routing.Route{routing.Row(http.MethodGet, "/x", named("bare"))},
			Middleware: []func(http.Handler) http.Handler{trail("c")},
			Options:    nil, NotFound: nil, MethodNotAllowed: nil,
		},
	)
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	return m
}

// answer is what a test reads of a response.
type answer struct {
	Status   int
	Handler  string
	Template string
	Trail    []string
	Allow    string
}

func TestMuxServeHTTP(t *testing.T) {
	t.Parallel()

	m := testMux(t)

	tests := []struct {
		method, target string
		want           answer
	}{
		{http.MethodGet, "/api/v1/items", answer{200, "list", "/api/v1/items", []string{"a", "b"}, ""}},
		{http.MethodPost, "/api/v1/items", answer{200, "create", "/api/v1/items", []string{"a", "b"}, ""}},
		{http.MethodDelete, "/api/v1/items", answer{200, "api-405", "", nil, "GET, POST, OPTIONS"}},
		{http.MethodOptions, "/api/v1/items", answer{200, "options", "/api/v1/items", []string{"a", "b"}, "GET, POST, OPTIONS"}},
		{http.MethodGet, "/api/v1/items?q=x", answer{200, "list", "/api/v1/items", []string{"a", "b"}, ""}},
		{http.MethodTrace, "/api/v1/items", answer{200, "api-405", "", nil, "GET, POST, OPTIONS"}},
		{http.MethodGet, "/api/v1/items/7", answer{200, "item", "/api/v1/items/{id}", []string{"a", "b"}, ""}},
		{http.MethodHead, "/api/v1/items/7", answer{200, "item-head", "/api/v1/items/{id}", []string{"a", "b"}, ""}},
		{http.MethodPut, "/api/v1/items/7", answer{200, "api-405", "", nil, "GET, HEAD, OPTIONS"}},
		{http.MethodDelete, "/api/v1/any/x/y", answer{200, "any", "/api/v1/any/", []string{"a", "b"}, ""}},
		{http.MethodOptions, "/api/v1/any/x", answer{200, "any", "/api/v1/any/", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v1/any/json", answer{200, "any-json", "/api/v1/any/json", []string{"a", "b"}, ""}},
		{http.MethodPost, "/api/v1/any/json", answer{200, "any", "/api/v1/any/", []string{"a", "b"}, ""}},
		{http.MethodHead, "/api/v1/any/json", answer{200, "any", "/api/v1/any/", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v1/any", answer{200, "api-404", "", nil, ""}},
		{http.MethodGet, "/api/v1/vlans/free", answer{200, "free", "/api/v1/vlans/free", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v1/vlans/7", answer{200, "vlan", "/api/v1/vlans/{id}", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v1/missing", answer{200, "api-404", "", nil, ""}},
		{http.MethodGet, "/api/v1", answer{200, "api-404", "", nil, ""}},
		{http.MethodGet, "/api/v1x", answer{200, "api-404", "", nil, ""}},
		{http.MethodGet, "/bare", answer{200, "root-404", "", nil, ""}},
		{http.MethodGet, "/page", answer{200, "page", "/page", []string{"root"}, ""}},
		{http.MethodPost, "/page", answer{http.StatusMethodNotAllowed, "", "", nil, "GET"}},
		{http.MethodGet, "/files/a/b", answer{200, "files", "/files/", []string{"root"}, ""}},
		{http.MethodPatch, "/files/a", answer{200, "files", "/files/", []string{"root"}, ""}},
		{http.MethodGet, "/missing", answer{200, "root-404", "", nil, ""}},
		{http.MethodGet, "/bare/x", answer{200, "bare", "/bare/x", []string{"c"}, ""}},
		{http.MethodPost, "/bare/x", answer{http.StatusMethodNotAllowed, "", "", nil, "GET"}},
		{http.MethodGet, "/bare/missing", answer{200, "root-404", "", nil, ""}},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))

			header := sent(rec)

			got := answer{
				Status: rec.Code, Handler: header.Get("X-Handler"), Template: header.Get("X-Template"),
				Trail: header.Values("X-Trail"), Allow: header.Get("Allow"),
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", tt.method, tt.target, diff)
			}
		})
	}
}

// A path with a trailing "/" that no route has gets the NotFound of its
// group, without a redirect and without the middleware of the group.
func TestMuxTrailingSlashIsNotFound(t *testing.T) {
	t.Parallel()

	m := testMux(t)

	tests := []struct {
		method, target string
		want           answer
	}{
		{http.MethodGet, "/api/v1/items/", answer{200, "api-404", "", nil, ""}},
		{http.MethodOptions, "/api/v1/items/", answer{200, "api-404", "", nil, ""}},
		{http.MethodGet, "/page/", answer{200, "root-404", "", nil, ""}},
		{http.MethodGet, "/bare/x/", answer{200, "root-404", "", nil, ""}},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))

			header := sent(rec)

			got := answer{
				Status: rec.Code, Handler: header.Get("X-Handler"), Template: header.Get("X-Template"),
				Trail: header.Values("X-Trail"), Allow: header.Get("Allow"),
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", tt.method, tt.target, diff)
			}

			if location := header.Get("Location"); location != "" {
				t.Errorf("%s %s Location = %q, want none", tt.method, tt.target, location)
			}
		})
	}
}

// A path that is not clean gets a 301 to its clean form, with its query,
// before any route or middleware.
func TestMuxRedirectsToTheCleanPath(t *testing.T) {
	t.Parallel()

	m := testMux(t)

	tests := map[string]string{
		"/api/v1//items":         "/api/v1/items",
		"/api/v1/x/../items?q=1": "/api/v1/items?q=1",
		"/api/v1/items/%2E%2E/x": "/api/v1/x",
		"/./page":                "/page",
	}

	for target, want := range tests {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

		header := sent(rec)

		got := []any{rec.Code, header.Get("Location"), header.Values("X-Trail")}
		if diff := cmp.Diff([]any{http.StatusMovedPermanently, want, []string(nil)}, got); diff != "" {
			t.Errorf("GET %s mismatch (-want +got):\n%s", target, diff)
		}
	}
}

// rawRequest returns the request of a raw request line, with its target as
// a client sent it.
func rawRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()

	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(method + " " + target + " HTTP/1.1\r\nHost: x\r\n\r\n")))
	if err != nil {
		t.Fatalf("http.ReadRequest(%s %s) error = %v", method, target, err)
	}

	return r
}

// Routes match the decoded path: an escaped "/" separates segments as "/"
// does, so no {name} variable holds a "/".
func TestMuxMatchesTheDecodedPath(t *testing.T) {
	t.Parallel()

	m := testMux(t)

	tests := []struct {
		method, target string
		want           answer
	}{
		{http.MethodGet, "/api/v1/items%2F7", answer{200, "item", "/api/v1/items/{id}", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v1/items%2f7", answer{200, "item", "/api/v1/items/{id}", []string{"a", "b"}, ""}},
		{http.MethodHead, "/api/v1/items%2F7", answer{200, "item-head", "/api/v1/items/{id}", []string{"a", "b"}, ""}},
		{http.MethodPut, "/api/v1/items%2F7", answer{200, "api-405", "", nil, "GET, HEAD, OPTIONS"}},
		{http.MethodGet, "/api/v1/items/a%2Fb", answer{200, "api-404", "", nil, ""}},
		{http.MethodGet, "/api/v1/items/a%2fb", answer{200, "api-404", "", nil, ""}},
		{http.MethodGet, "/api/v1/items/a%2Fb|", answer{200, "api-404", "", nil, ""}},
		{http.MethodGet, "/api/v1/items%2F7|", answer{200, "item", "/api/v1/items/{id}", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v1%2Fitems", answer{200, "list", "/api/v1/items", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api%2Fv1/items", answer{200, "list", "/api/v1/items", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v%31/items", answer{200, "list", "/api/v1/items", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v1/any/a%2Fb", answer{200, "any", "/api/v1/any/", []string{"a", "b"}, ""}},
		{http.MethodGet, "/api/v1/any%2Fa", answer{200, "any", "/api/v1/any/", []string{"a", "b"}, ""}},
		{http.MethodGet, "/files%2Fa", answer{200, "files", "/files/", []string{"root"}, ""}},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, rawRequest(t, tt.method, tt.target))

			header := sent(rec)

			got := answer{
				Status: rec.Code, Handler: header.Get("X-Handler"), Template: header.Get("X-Template"),
				Trail: header.Values("X-Trail"), Allow: header.Get("Allow"),
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", tt.method, tt.target, diff)
			}
		})
	}
}

// A handler gets the decoded values of the segments that its route matched:
// an escaped "/" is a separator, and an escaped "%2F" is the text "%2F".
func TestMuxPathValuesOfTheDecodedPath(t *testing.T) {
	t.Parallel()

	value := func(names ...string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, name := range names {
				w.Header().Add("X-Value", r.PathValue(name))
			}
		})
	}

	m, err := routing.NewMux(routing.Group{
		Prefix: "/api",
		Routes: []routing.Route{
			routing.Row(http.MethodGet, "/{name}", value("name")),
			routing.Row(http.MethodGet, "/{name}/apps", value("name")),
			routing.Row(http.MethodGet, "/configs/{kind}/{name}", value("kind", "name")),
		},
		Middleware: nil, Options: nil, NotFound: named("api-404"), MethodNotAllowed: nil,
	})
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	tests := map[string][]string{
		"/api/a%2Fapps":             {"a"},
		"/api/a%2fapps":             {"a"},
		"/api/configs%2Ftopo%2Ffoo": {"topo", "foo"},
		"/api/a%252Fb":              {"a%2Fb"},
		"/api/a%20b":                {"a b"},
		"/api/a%25b":                {"a%b"},
		"/api/%C3%A9":               {"é"},
		"/api/a%3Fb":                {"a?b"},
		"/api/a%2Fb":                nil,
		"/api/a%2Fb|":               nil,
	}

	for target, want := range tests {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, rawRequest(t, http.MethodGet, target))

			if diff := cmp.Diff(want, sent(rec).Values("X-Value")); diff != "" {
				t.Errorf("GET %s path values mismatch (-want +got):\n%s", target, diff)
			}
		})
	}
}

// A request with an escaped "/" under a subtree route goes to that route,
// where a route with a variable takes the same path with a "/".
func TestMuxEscapedSlashBelowASubtree(t *testing.T) {
	t.Parallel()

	m, err := routing.NewMux(routing.Group{
		Prefix: "",
		Routes: []routing.Route{
			routing.Row(http.MethodGet, "/x/{name}", named("name")),
			routing.Row("", "/x/", named("subtree")),
		},
		Middleware: nil, Options: nil, NotFound: named("root-404"), MethodNotAllowed: nil,
	})
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	for target, want := range map[string]answer{
		"/x/a%2Fb": {200, "subtree", "/x/", nil, ""},
		"/x%2Fa":   {200, "name", "/x/{name}", nil, ""},
	} {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, rawRequest(t, http.MethodGet, target))

		header := sent(rec)

		got := answer{
			Status: rec.Code, Handler: header.Get("X-Handler"), Template: header.Get("X-Template"),
			Trail: header.Values("X-Trail"), Allow: header.Get("Allow"),
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GET %s mismatch (-want +got):\n%s", target, diff)
		}
	}
}

// A handler gets the values of the variables of its route, unescaped.
func TestMuxPathValues(t *testing.T) {
	t.Parallel()

	var got []string

	m, err := routing.NewMux(routing.Group{
		Prefix: "/api",
		Routes: []routing.Route{
			routing.Row(http.MethodGet, "/{kind}/{name}", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = []string{r.PathValue("kind"), r.PathValue("name")}
			})),
		},
		Middleware: nil, Options: nil, NotFound: nil, MethodNotAllowed: nil,
	})
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/topology/a%20b", nil))

	if diff := cmp.Diff([]string{"topology", "a b"}, got); diff != "" {
		t.Errorf("GET /api/topology/a%%20b path values mismatch (-want +got):\n%s", diff)
	}
}

// The Allow header of an OPTIONS request that Options serves is set before
// the middleware runs, so a middleware can answer it.
func TestMuxSetsAllowBeforeMiddleware(t *testing.T) {
	t.Parallel()

	answerOptions := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)

				return
			}

			next.ServeHTTP(w, r)
		})
	}

	m, err := routing.NewMux(routing.Group{
		Prefix: "", Routes: []routing.Route{routing.Row(http.MethodGet, "/x", named("x"))},
		Middleware: []func(http.Handler) http.Handler{answerOptions},
		Options:    named("options"), NotFound: nil, MethodNotAllowed: nil,
	})
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/x", nil))

	if got := []any{rec.Code, sent(rec).Get("Allow")}; !cmp.Equal(got, []any{http.StatusNoContent, "GET, OPTIONS"}) {
		t.Errorf("OPTIONS /x = %v, want 204 and Allow GET, OPTIONS", got)
	}
}

// Without a group of prefix "", a request that no group takes gets [http.NotFound].
func TestMuxNotFoundWithoutRoot(t *testing.T) {
	t.Parallel()

	m, err := routing.NewMux(routing.Group{
		Prefix: "/api", Routes: []routing.Route{routing.Row(http.MethodGet, "/x", named("x"))},
		Middleware: nil, Options: nil, NotFound: nil, MethodNotAllowed: nil,
	})
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	for _, target := range []string{"/elsewhere", "/api/missing"} {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

		if got := []any{rec.Code, rec.Body.String()}; !cmp.Equal(got, []any{http.StatusNotFound, "404 page not found\n"}) {
			t.Errorf("GET %s = %v, want 404 and the body of http.NotFound", target, got)
		}
	}
}

// A path of a prefixed group that no route matches gets the NotFound of the
// "" group, not a route of the "" group that also matches the path.
func TestMuxNotFoundStaysInGroup(t *testing.T) {
	t.Parallel()

	m, err := routing.NewMux(
		routing.Group{
			Prefix: "/api", Routes: []routing.Route{routing.Row(http.MethodGet, "/x", named("x"))},
			Middleware: nil, Options: nil, NotFound: nil, MethodNotAllowed: nil,
		},
		routing.Group{
			Prefix: "", Routes: []routing.Route{routing.Row("", "/", named("every"))},
			Middleware: nil, Options: nil, NotFound: named("root-404"), MethodNotAllowed: nil,
		},
	)
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/missing", nil))

	if got := sent(rec).Get("X-Handler"); got != "root-404" {
		t.Errorf("GET /api/missing handler = %q, want root-404", got)
	}
}

// Without a group of prefix "", a path outside every group gets
// [http.NotFound], not the NotFound of another group.
func TestMuxNotFoundOutsideEveryGroup(t *testing.T) {
	t.Parallel()

	m, err := routing.NewMux(routing.Group{
		Prefix: "/api", Routes: []routing.Route{routing.Row(http.MethodGet, "/x", named("x"))},
		Middleware: nil, Options: nil, NotFound: named("api-404"), MethodNotAllowed: nil,
	})
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/elsewhere", nil))

	got := []any{rec.Code, sent(rec).Get("X-Handler"), rec.Body.String()}
	if diff := cmp.Diff([]any{http.StatusNotFound, "", "404 page not found\n"}, got); diff != "" {
		t.Errorf("GET /elsewhere mismatch (-want +got):\n%s", diff)
	}
}

// A Mux of no groups answers every request with [http.NotFound].
func TestMuxWithoutGroups(t *testing.T) {
	t.Parallel()

	m, err := routing.NewMux()
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if got := []any{rec.Code, rec.Body.String()}; !cmp.Equal(got, []any{http.StatusNotFound, "404 page not found\n"}) {
		t.Errorf("GET /x = %v, want 404 and the body of http.NotFound", got)
	}
}

// A request whose path is "" gets a 301 to "/".
func TestMuxRedirectsAnEmptyPath(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/page", nil)
	r.URL.Path = ""

	rec := httptest.NewRecorder()
	testMux(t).ServeHTTP(rec, r)

	if got := []any{rec.Code, sent(rec).Get("Location")}; !cmp.Equal(got, []any{http.StatusMovedPermanently, "/"}) {
		t.Errorf("GET with path \"\" = %v, want 301 and Location /", got)
	}
}

// NewMux does not change the slice of groups that it gets.
func TestNewMuxKeepsTheGroups(t *testing.T) {
	t.Parallel()

	group := func(prefix string) routing.Group {
		return routing.Group{
			Prefix: prefix, Routes: []routing.Route{routing.Row(http.MethodGet, "/x", named(prefix))},
			Middleware: nil, Options: nil, NotFound: nil, MethodNotAllowed: nil,
		}
	}

	all := []routing.Group{group("/a"), group("/b"), group("/c")}

	if _, err := routing.NewMux(all[:2]...); err != nil {
		t.Fatalf("NewMux(/a, /b) error = %v", err)
	}

	got := make([]string, 0, len(all))
	for _, g := range all {
		got = append(got, g.Prefix)
	}

	if diff := cmp.Diff([]string{"/a", "/b", "/c"}, got); diff != "" {
		t.Errorf("prefixes after NewMux(all[:2]...) mismatch (-want +got):\n%s", diff)
	}
}

// The Options of a group serves OPTIONS at each path, also where a {$} or a
// {name...} variable has the place of a {name} variable.
func TestMuxOptionsOfEachPath(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		routes  []routing.Route
		targets map[string]answer
	}{
		"{$} and {id}": {
			routes: []routing.Route{
				routing.Row(http.MethodGet, "/x/{$}", named("end")),
				routing.Row(http.MethodGet, "/x/{id}", named("id")),
			},
			targets: map[string]answer{
				"/x/":    {http.StatusOK, "options", "/x/{$}", nil, "GET, OPTIONS"},
				"/x/abc": {http.StatusOK, "options", "/x/{id}", nil, "GET, OPTIONS"},
			},
		},
		"{id} and {rest...}": {
			routes: []routing.Route{
				routing.Row(http.MethodGet, "/x/{id}", named("id")),
				routing.Row(http.MethodGet, "/x/{rest...}", named("rest")),
			},
			targets: map[string]answer{
				"/x/abc":     {http.StatusOK, "options", "/x/{id}", nil, "GET, OPTIONS"},
				"/x/abc/def": {http.StatusOK, "options", "/x/{rest...}", nil, "GET, OPTIONS"},
				"/x/":        {http.StatusOK, "options", "/x/{rest...}", nil, "GET, OPTIONS"},
			},
		},
		"{a} and {b} of two methods": {
			routes: []routing.Route{
				routing.Row(http.MethodGet, "/x/{a}", named("get")),
				routing.Row(http.MethodPost, "/x/{b}", named("post")),
			},
			targets: map[string]answer{
				"/x/1": {http.StatusOK, "options", "/x/{a}", nil, "GET, POST, OPTIONS"},
			},
		},
		"{a} and an OPTIONS route of {b}": {
			routes: []routing.Route{
				routing.Row(http.MethodGet, "/x/{a}", named("get")),
				routing.Row(http.MethodOptions, "/x/{b}", named("own-options")),
			},
			targets: map[string]answer{
				"/x/1": {http.StatusOK, "own-options", "/x/{b}", nil, ""},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m, err := routing.NewMux(routing.Group{
				Prefix: "", Routes: tt.routes, Middleware: nil, Options: named("options"), NotFound: nil, MethodNotAllowed: nil,
			})
			if err != nil {
				t.Fatalf("NewMux() error = %v", err)
			}

			for target, want := range tt.targets {
				rec := httptest.NewRecorder()
				m.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, target, nil))

				header := sent(rec)

				got := answer{
					Status: rec.Code, Handler: header.Get("X-Handler"), Template: header.Get("X-Template"),
					Trail: header.Values("X-Trail"), Allow: header.Get("Allow"),
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("OPTIONS %s mismatch (-want +got):\n%s", target, diff)
				}
			}
		})
	}
}

func TestMuxAllowed(t *testing.T) {
	t.Parallel()

	m := testMux(t)

	tests := map[string][]string{
		"/api/v1/items":     {http.MethodGet, http.MethodPost, http.MethodOptions},
		"/api/v1/items/1":   {http.MethodGet, http.MethodHead, http.MethodOptions},
		"/api/v1/items/":    nil,
		"/api/v1/items%2F1": {http.MethodGet, http.MethodHead, http.MethodOptions},
		"/api/v1/missing":   nil,
		"/page":             {http.MethodGet},
		"/missing":          nil,
		"/files/x": {
			http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
		},
	}

	for target, want := range tests {
		got := m.Allowed(httptest.NewRequest(http.MethodGet, target, nil))
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Allowed(%s) mismatch (-want +got):\n%s", target, diff)
		}
	}
}

func TestMuxRoutes(t *testing.T) {
	t.Parallel()

	routes := testMux(t).Routes()
	got := make([]string, 0, len(routes))

	for _, route := range routes {
		got = append(got, route.Method+" "+route.Path)
	}

	want := []string{
		"GET /page", " /files/",
		"GET /api/v1/items", "POST /api/v1/items", "GET /api/v1/items/{id}", "HEAD /api/v1/items/{id}",
		"GET /api/v1/any/json", " /api/v1/any/", "GET /api/v1/vlans/{id}", "GET /api/v1/vlans/free",
		"GET /bare/x",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Routes() mismatch (-want +got):\n%s", diff)
	}
}

func TestTemplateWithoutARoute(t *testing.T) {
	t.Parallel()

	if got := routing.Template(httptest.NewRequest(http.MethodGet, "/x", nil)); got != "" {
		t.Errorf("Template() of a request no router matched = %q, want \"\"", got)
	}
}

func TestNewMuxRefusesABadTable(t *testing.T) {
	t.Parallel()

	group := func(prefix string, routes ...routing.Route) routing.Group {
		return routing.Group{
			Prefix: prefix, Routes: routes, Middleware: nil, Options: nil, NotFound: nil, MethodNotAllowed: nil,
		}
	}

	withMiddleware := group("/api", routing.Row(http.MethodGet, "/x", named("x")))
	withMiddleware.Middleware = []func(http.Handler) http.Handler{trail("a"), nil}

	tests := map[string][]routing.Group{
		"a path without a leading /":     {group("", routing.Row(http.MethodGet, "x", named("x")))},
		"an empty path":                  {group("/api", routing.Row(http.MethodGet, "", named("x")))},
		"an unbalanced variable":         {group("", routing.Row(http.MethodGet, "/x/{id", named("x")))},
		"a method Allowed does not name": {group("", routing.Row(http.MethodTrace, "/x", named("x")))},
		"a lower-case method":            {group("", routing.Row("get", "/x", named("x")))},
		"a repeated route": {
			group("", routing.Row(http.MethodGet, "/x", named("x")), routing.Row(http.MethodGet, "/x", named("y"))),
		},
		"a repeated route of every method": {
			group("", routing.Row("", "/x", named("x")), routing.Row("", "/x", named("y"))),
		},
		"routes of the same paths": {
			group("", routing.Row(http.MethodGet, "/x/{a}", named("x")), routing.Row(http.MethodGet, "/x/{b}", named("y"))),
		},
		"routes that overlap": {
			group("", routing.Row(http.MethodGet, "/x/{a}", named("x")), routing.Row("", "/x/y", named("y"))),
		},
		"a repeated HEAD route": {
			group("", routing.Row(http.MethodHead, "/x", named("x")), routing.Row(http.MethodHead, "/x", named("y"))),
		},
		"a route without a handler":    {group("", routing.Row(http.MethodGet, "/x", nil))},
		"a nil middleware":             {withMiddleware},
		"a prefix without a leading /": {group("api")},
		"a prefix with a trailing /":   {group("/api/")},
		"the prefix /":                 {group("/")},
		"a repeated prefix":            {group("/api"), group("/b"), group("/api")},
		"a repeated empty prefix":      {group(""), group("")},
	}

	for name, groups := range tests {
		if m, err := routing.NewMux(groups...); err == nil || m != nil {
			t.Errorf("NewMux(%s) = %v, %v, want nil and an error", name, m, err)
		}
	}
}

// A request goes to the group with the longest prefix of its path, in either
// order of the groups.
func TestMuxLongestPrefix(t *testing.T) {
	t.Parallel()

	v1 := routing.Group{
		Prefix: "/api/v1", Routes: []routing.Route{routing.Row(http.MethodGet, "/y", named("v1"))},
		Middleware: nil, Options: nil, NotFound: named("v1-404"), MethodNotAllowed: nil,
	}
	api := routing.Group{
		Prefix: "/api", Routes: []routing.Route{routing.Row(http.MethodGet, "/y", named("api"))},
		Middleware: nil, Options: nil, NotFound: named("api-404"), MethodNotAllowed: nil,
	}

	for name, groups := range map[string][]routing.Group{"shorter first": {api, v1}, "longer first": {v1, api}} {
		m, err := routing.NewMux(groups...)
		if err != nil {
			t.Fatalf("NewMux(%s) error = %v", name, err)
		}

		for target, want := range map[string]string{
			"/api/v1/y": "v1", "/api/v1/z": "v1-404", "/api/y": "api", "/api/z": "api-404",
		} {
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

			if got := sent(rec).Get("X-Handler"); got != want {
				t.Errorf("GET %s with the %s handler = %q, want %q", target, name, got, want)
			}
		}
	}
}

// A route of OPTIONS serves OPTIONS at its path in a group with Options,
// before or after the other routes of the path.
func TestMuxRouteOfOptions(t *testing.T) {
	t.Parallel()

	get := routing.Row(http.MethodGet, "/x", named("get"))
	own := routing.Row(http.MethodOptions, "/x", named("own-options"))

	for name, routes := range map[string][]routing.Route{"after": {get, own}, "before": {own, get}} {
		m, err := routing.NewMux(routing.Group{
			Prefix: "", Routes: routes, Middleware: nil, Options: named("options"), NotFound: nil, MethodNotAllowed: nil,
		})
		if err != nil {
			t.Fatalf("NewMux(OPTIONS route %s) error = %v", name, err)
		}

		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/x", nil))

		if got := sent(rec).Get("X-Handler"); got != "own-options" {
			t.Errorf("OPTIONS /x with the OPTIONS route %s handler = %q, want own-options", name, got)
		}
	}
}

// Allowed probes the methods of a path without a change to the request, so
// that MethodNotAllowed sees the method of the request.
func TestMuxAllowedKeepsTheMethod(t *testing.T) {
	t.Parallel()

	var refused string

	m, err := routing.NewMux(routing.Group{
		Prefix: "", Routes: []routing.Route{routing.Row(http.MethodGet, "/x", named("x"))},
		Middleware: nil, Options: named("options"), NotFound: nil,
		MethodNotAllowed: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { refused = r.Method }),
	})
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	r := httptest.NewRequest(http.MethodPatch, "/x", nil)
	_ = m.Allowed(r)

	if r.Method != http.MethodPatch {
		t.Errorf("method after Allowed(PATCH /x) = %q, want PATCH", r.Method)
	}

	m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPatch, "/x", nil))

	if refused != http.MethodPatch {
		t.Errorf("method in MethodNotAllowed of PATCH /x = %q, want PATCH", refused)
	}
}
