package routing_test

import (
	"net/http"
	"net/http/httptest"
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
				routing.Row(http.MethodGet, "/ws", named("ws")).WithoutOptions(),
				routing.Row(http.MethodGet, "/search", named("search")).WithQueries("q", "{q}"),
				routing.Row("", "/any/", named("any")),
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
		{http.MethodGet, "/api/v1/items/7", answer{200, "item", "/api/v1/items/{id}", []string{"a", "b"}, ""}},
		{http.MethodHead, "/api/v1/items/7", answer{200, "item-head", "/api/v1/items/{id}", []string{"a", "b"}, ""}},
		{http.MethodPut, "/api/v1/items/7", answer{200, "api-405", "", nil, "GET, HEAD, OPTIONS"}},
		{http.MethodGet, "/api/v1/ws", answer{200, "ws", "/api/v1/ws", []string{"a", "b"}, ""}},
		{http.MethodOptions, "/api/v1/ws", answer{200, "api-405", "", nil, "GET"}},
		{http.MethodGet, "/api/v1/search?q=x", answer{200, "search", "/api/v1/search", []string{"a", "b"}, ""}},
		{http.MethodPost, "/api/v1/search?q=x", answer{200, "api-405", "", nil, "GET, OPTIONS"}},
		{http.MethodGet, "/api/v1/search", answer{200, "api-404", "", nil, ""}},
		{http.MethodDelete, "/api/v1/any/x/y", answer{200, "any", "/api/v1/any/", []string{"a", "b"}, ""}},
		{http.MethodOptions, "/api/v1/any/x", answer{200, "any", "/api/v1/any/", []string{"a", "b"}, ""}},
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

// A path that differs from a route's template by a trailing "/" gets the 301
// to the template's form behind the middleware of the group, as gorilla
// replaces the route's handler with the redirect.
func TestMuxRedirectsBehindMiddleware(t *testing.T) {
	t.Parallel()

	m := testMux(t)

	type redirect struct {
		Status   int
		Location string
		Trail    []string
		Allow    string
	}

	tests := []struct {
		method, target string
		want           redirect
	}{
		{http.MethodGet, "/api/v1/items/", redirect{http.StatusMovedPermanently, "/api/v1/items", []string{"a", "b"}, ""}},
		{
			http.MethodOptions, "/api/v1/items/",
			redirect{http.StatusMovedPermanently, "/api/v1/items", []string{"a", "b"}, "GET, POST, OPTIONS"},
		},
		{http.MethodGet, "/page/", redirect{http.StatusMovedPermanently, "/page", []string{"root"}, ""}},
		{http.MethodGet, "/bare/x/", redirect{http.StatusMovedPermanently, "/bare/x", []string{"c"}, ""}},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))

			header := sent(rec)

			got := redirect{
				Status: rec.Code, Location: header.Get("Location"),
				Trail: header.Values("X-Trail"), Allow: header.Get("Allow"),
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", tt.method, tt.target, diff)
			}
		})
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

	for _, target := range []string{"/x", "/x/"} {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, target, nil))

		if got := []any{rec.Code, sent(rec).Get("Allow")}; !cmp.Equal(got, []any{http.StatusNoContent, "GET, OPTIONS"}) {
			t.Errorf("OPTIONS %s = %v, want 204 and Allow GET, OPTIONS", target, got)
		}
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

func TestMuxAllowed(t *testing.T) {
	t.Parallel()

	m := testMux(t)

	tests := map[string][]string{
		"/api/v1/items":      {http.MethodGet, http.MethodPost, http.MethodOptions},
		"/api/v1/items/1":    {http.MethodGet, http.MethodHead, http.MethodOptions},
		"/api/v1/ws":         {http.MethodGet},
		"/api/v1/search":     nil,
		"/api/v1/search?q=x": {http.MethodGet, http.MethodOptions},
		"/api/v1/missing":    nil,
		"/page":              {http.MethodGet},
		"/missing":           nil,
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
		row := route.Method + " " + route.Path
		if queries := route.Queries(); len(queries) == 2 {
			row += "?" + queries[0] + "=" + queries[1]
		}

		got = append(got, row)
	}

	want := []string{
		"GET /page", " /files/",
		"GET /api/v1/items", "POST /api/v1/items", "GET /api/v1/items/{id}", "HEAD /api/v1/items/{id}",
		"GET /api/v1/ws", "GET /api/v1/search?q={q}", " /api/v1/any/",
		"GET /bare/x",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Routes() mismatch (-want +got):\n%s", diff)
	}
}

// A route keeps its fields through the modifiers, which change only a copy.
func TestRouteModifiers(t *testing.T) {
	t.Parallel()

	route := routing.Row(http.MethodGet, "/x", named("x"))
	queried := route.WithQueries("a", "{a}")

	if diff := cmp.Diff([]string{"a", "{a}"}, queried.Queries()); diff != "" {
		t.Errorf("WithQueries(a, {a}).Queries() mismatch (-want +got):\n%s", diff)
	}

	without := queried.WithoutOptions()
	if got := []string{without.Method, without.Path}; !cmp.Equal(got, []string{http.MethodGet, "/x"}) {
		t.Errorf("WithoutOptions() method and path = %v, want GET /x", got)
	}

	if diff := cmp.Diff([]string{"a", "{a}"}, without.Queries()); diff != "" {
		t.Errorf("WithoutOptions() queries mismatch (-want +got):\n%s", diff)
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
		"an empty path":                  {group("", routing.Row(http.MethodGet, "", named("x")))},
		"an unbalanced variable":         {group("", routing.Row(http.MethodGet, "/x/{id", named("x")))},
		"a route without a handler":      {group("", routing.Row(http.MethodGet, "/x", nil))},
		"a method Allowed does not name": {group("", routing.Row(http.MethodTrace, "/x", named("x")))},
		"a lower-case method":            {group("", routing.Row("get", "/x", named("x")))},
		"a repeated route": {
			group("", routing.Row(http.MethodGet, "/x", named("x")), routing.Row(http.MethodGet, "/x", named("y"))),
		},
		"routes of the same paths": {
			group("", routing.Row(http.MethodGet, "/x/{a}", named("x")), routing.Row(http.MethodGet, "/x/{b}", named("y"))),
		},
		"a route after a route of every method": {
			group("", routing.Row("", "/x", named("x")), routing.Row(http.MethodGet, "/x", named("y"))),
		},
		"a route after one with fewer queries": {
			group("",
				routing.Row(http.MethodGet, "/x", named("x")).WithQueries("a", "{a}"),
				routing.Row(http.MethodGet, "/x", named("y")).WithQueries("b", "{b}", "a", "{a}"),
			),
		},
		"a nil middleware":             {withMiddleware},
		"a prefix without a leading /": {group("api")},
		"a prefix with a trailing /":   {group("/api/")},
		"the prefix /":                 {group("/")},
		"a prefix after a shorter one": {group(""), group("/api"), group("/api/v1")},
		"a repeated prefix":            {group("/api"), group("/b"), group("/api")},
		"a repeated empty prefix":      {group(""), group("")},
	}

	for name, groups := range tests {
		if m, err := routing.NewMux(groups...); err == nil || m != nil {
			t.Errorf("NewMux(%s) = %v, %v, want nil and an error", name, m, err)
		}
	}
}

// A group whose prefix starts with the prefix of a later group takes its own
// paths, and a route with queries can come before one with fewer.
func TestNewMuxTakesAGoodTable(t *testing.T) {
	t.Parallel()

	m, err := routing.NewMux(
		routing.Group{
			Prefix: "/api/v1",
			Routes: []routing.Route{
				routing.Row(http.MethodGet, "/y", named("v1")).WithQueries("a", "{a}"),
				routing.Row(http.MethodGet, "/y", named("v1-no-query")),
			},
			Middleware: nil, Options: nil, NotFound: named("v1-404"), MethodNotAllowed: nil,
		},
		routing.Group{
			Prefix: "/api", Routes: []routing.Route{routing.Row(http.MethodGet, "/y", named("api"))},
			Middleware: nil, Options: nil, NotFound: named("api-404"), MethodNotAllowed: nil,
		},
	)
	if err != nil {
		t.Fatalf("NewMux() error = %v", err)
	}

	for target, want := range map[string]string{
		"/api/v1/y?a=1": "v1", "/api/v1/y": "v1-no-query", "/api/v1/z": "v1-404", "/api/y": "api", "/api/z": "api-404",
	} {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

		if got := sent(rec).Get("X-Handler"); got != want {
			t.Errorf("GET %s handler = %q, want %q", target, got, want)
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
