// Package routing routes the requests of the phenix server by a table of
// routes. It owns the table, the registration of its rows, and the answers for
// a request that matches no route.
package routing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/gorilla/mux"
)

var (
	// errNoHandler is the error of a row that has no handler.
	errNoHandler = errors.New("route has no handler")
	// errRepeatedPrefix is the error of a group whose prefix an earlier group has.
	errRepeatedPrefix = errors.New("another group has the same prefix")
	// errBadPrefix is the error of a group prefix that is not "" and does not
	// start with "/", or that ends with "/".
	errBadPrefix = errors.New("the prefix must start with / and must not end with /")
	// errShadowedPrefix is the error of a group whose paths an earlier group takes.
	errShadowedPrefix = errors.New("the prefix of an earlier group is a prefix of this prefix")
	// errNilMiddleware is the error of a group with a nil middleware.
	errNilMiddleware = errors.New("the group has a nil middleware")
	// errBadMethod is the error of a row whose method Allowed does not name.
	errBadMethod = errors.New("the method is not \"\" or one of " + strings.Join(methods(), ", "))
	// errBadPath is the error of a row whose path does not start with "/".
	errBadPath = errors.New("the path does not start with /")
	// errDeadRoute is the error of a row that an earlier row serves in its place.
	errDeadRoute = errors.New("an earlier route takes every request of this route")
)

// variableName matches the name of a {name} or {name:pattern} variable of a
// path, which two paths of the same requests can differ by.
var variableName = regexp.MustCompile(`\{[^}:]*`)

// methods are the methods that Allowed names, in the order it names them.
func methods() []string {
	return []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	}
}

// Route is one row of a route table.
type Route struct {
	// Method is one of the methods that Allowed names, in upper case, or ""
	// for every method.
	Method string
	// Path is the path below the group prefix, with {name} variables. It
	// starts with "/". A trailing "/" also matches every path below it.
	Path    string
	Handler http.Handler

	queries   []string
	noOptions bool
}

// Row returns the route of one method and path. Rows are written with Row,
// not with struct literals: the exhaustruct linter is on.
func Row(method, path string, handler http.Handler) Route {
	return Route{Method: method, Path: path, Handler: handler, queries: nil, noOptions: false}
}

// WithQueries returns the route that matches only a request with the query
// pairs, as a gorilla query matcher: each pair is a name and a value
// template, such as "path", "{path}".
func (r Route) WithQueries(pairs ...string) Route {
	r.queries = slices.Clone(pairs)

	return r
}

// Queries returns the query pairs of WithQueries.
func (r Route) Queries() []string {
	return slices.Clone(r.queries)
}

// WithoutOptions returns the route whose path does not take OPTIONS, in a
// group whose Options serves OPTIONS for every other path.
func (r Route) WithoutOptions() Route {
	r.noOptions = true

	return r
}

// Group is a set of routes below one path prefix, with shared middleware and
// refusals.
type Group struct {
	// Prefix is "" or a path such as "/api/v1", without a trailing "/". It is a
	// string prefix: "/api/v1" also takes "/api/v1x". Each group has its own,
	// and a prefix other than "" is not the start of a later group's prefix.
	Prefix string
	Routes []Route
	// Middleware runs for a matched route only, also when the answer is the
	// redirect of a path with or without a trailing "/". The first is the
	// outermost of the group. None is nil.
	Middleware []func(http.Handler) http.Handler
	// Options, when not nil, serves OPTIONS behind Middleware for each path of
	// the group, with Allow set. A route of every method serves OPTIONS itself,
	// and so does a route of OPTIONS, for its path.
	Options http.Handler
	// NotFound answers a path of the group that no route matches. When nil, the
	// NotFound of the group with prefix "" answers, and without one,
	// http.NotFound.
	NotFound http.Handler
	// MethodNotAllowed answers a method that no route of the path takes, with
	// Allow set. When nil, the answer is a 405 without a body.
	MethodNotAllowed http.Handler
}

// Mux serves requests by the routes of its groups.
type Mux struct {
	router *mux.Router
	routes []Route
	// matched describes each registered route, which the middleware of a
	// request finds by mux.CurrentRoute.
	matched map[*mux.Route]matchedRoute
}

// NewMux returns the Mux of the groups, which it matches in the order given,
// and each group's routes in the order given. It returns an error for a group
// or a route that breaks the rules of [Group] and [Route], for a route that an
// earlier route of its group serves in its place, and for a table the router
// refuses.
func NewMux(groups ...Group) (*Mux, error) {
	m := &Mux{router: mux.NewRouter().StrictSlash(true), routes: nil, matched: map[*mux.Route]matchedRoute{}}

	notFound := http.NotFoundHandler()

	for i, g := range groups {
		if err := checkGroup(g, groups[:i]); err != nil {
			return nil, err
		}

		if g.Prefix == "" && g.NotFound != nil {
			notFound = g.NotFound
		}
	}

	for _, g := range groups {
		if err := m.add(g, notFound); err != nil {
			return nil, err
		}
	}

	return m, nil
}

// checkGroup returns an error for g when it breaks a rule of [Group] or of
// [Route], with the groups before it.
func checkGroup(g Group, earlier []Group) error {
	if g.Prefix != "" && (!strings.HasPrefix(g.Prefix, "/") || strings.HasSuffix(g.Prefix, "/")) {
		return fmt.Errorf("group %q: %w", g.Prefix, errBadPrefix)
	}

	for _, e := range earlier {
		switch {
		case e.Prefix == g.Prefix:
			return fmt.Errorf("group %q: %w", g.Prefix, errRepeatedPrefix)
		case e.Prefix != "" && strings.HasPrefix(g.Prefix, e.Prefix):
			return fmt.Errorf("group %q after group %q: %w", g.Prefix, e.Prefix, errShadowedPrefix)
		}
	}

	if slices.ContainsFunc(g.Middleware, func(mw func(http.Handler) http.Handler) bool { return mw == nil }) {
		return fmt.Errorf("group %q: %w", g.Prefix, errNilMiddleware)
	}

	for i, row := range g.Routes {
		if err := checkRow(row, g.Routes[:i]); err != nil {
			return fmt.Errorf("route %s %s%s: %w", row.Method, g.Prefix, row.Path, err)
		}
	}

	return nil
}

// checkRow returns an error for row when it breaks a rule of [Route], or when
// one of the earlier rows of its group takes each request that it matches.
func checkRow(row Route, earlier []Route) error {
	switch {
	case row.Method != "" && !slices.Contains(methods(), row.Method):
		return errBadMethod
	case !strings.HasPrefix(row.Path, "/"):
		return errBadPath
	case row.Handler == nil:
		return errNoHandler
	}

	for _, e := range earlier {
		if (e.Method == "" || e.Method == row.Method) && samePath(e.Path, row.Path) && covers(e.queries, row.queries) {
			return errDeadRoute
		}
	}

	return nil
}

// samePath reports whether paths a and b match the same requests: they differ
// only by the names of their variables.
func samePath(a, b string) bool {
	return variableName.ReplaceAllString(a, "{") == variableName.ReplaceAllString(b, "{")
}

// covers reports whether a request with the query pairs of b always has the
// query pairs of a.
func covers(a, b []string) bool {
	all := pairs(b)

	for _, pair := range pairs(a) {
		if !slices.Contains(all, pair) {
			return false
		}
	}

	return true
}

// pairs returns the name and value pairs of query matcher pairs.
func pairs(queries []string) [][2]string {
	var all [][2]string

	for i := 0; i+1 < len(queries); i += 2 {
		all = append(all, [2]string{queries[i], queries[i+1]})
	}

	return all
}

// ServeHTTP serves r by the route that matches it.
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.router.ServeHTTP(w, r)
}

// Routes returns the rows of the table as given, in order, each Path with the
// prefix of its group.
func (m *Mux) Routes() []Route {
	return slices.Clone(m.routes)
}

// Allowed returns the methods that a route takes at the path of r, in the
// order GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS.
func (m *Mux) Allowed(r *http.Request) []string {
	var allowed []string

	for _, method := range methods() {
		probe := r.Clone(r.Context())
		probe.Method = method

		var match mux.RouteMatch

		if m.router.Match(probe, &match) && match.MatchErr == nil {
			allowed = append(allowed, method)
		}
	}

	return allowed
}

// matchedRoute is what the Mux keeps of a registered route.
type matchedRoute struct {
	template string
	options  bool
}

// add registers the routes of g on a subrouter of their own, so that only
// they get the middleware of g. notFound answers in place of a nil NotFound.
func (m *Mux) add(g Group, notFound http.Handler) error {
	var router *mux.Router

	if g.Prefix == "" {
		// a subrouter without a NotFound or MethodNotAllowed handler leaves
		// those answers to the root router
		router = m.router.NewRoute().Subrouter()
		m.router.NotFoundHandler = g.NotFound
		m.router.MethodNotAllowedHandler = m.refusal(g.MethodNotAllowed)
	} else {
		prefix := m.router.PathPrefix(g.Prefix)
		if err := prefix.GetError(); err != nil {
			return fmt.Errorf("group %s: %w", g.Prefix, err)
		}

		router = prefix.Subrouter()
		router.NotFoundHandler = g.NotFound
		router.MethodNotAllowedHandler = m.refusal(g.MethodNotAllowed)

		if g.NotFound == nil {
			router.NotFoundHandler = notFound
		}
	}

	// gorilla wraps the handler of the matched route in these, also when it
	// is the redirect of a trailing "/"
	router.Use(m.describe)

	for _, mw := range g.Middleware {
		router.Use(mw)
	}

	for _, row := range g.Routes {
		// a path with a route of OPTIONS takes OPTIONS only by that route
		ownOptions := slices.ContainsFunc(g.Routes, func(other Route) bool {
			return other.Method == http.MethodOptions && samePath(other.Path, row.Path)
		})

		if err := m.register(router, g, row, ownOptions); err != nil {
			return err
		}
	}

	return nil
}

// register adds row of g to router. ownOptions is set when a route of g
// serves OPTIONS at the path of row.
func (m *Mux) register(router *mux.Router, g Group, row Route, ownOptions bool) error {
	route := router.NewRoute()

	if strings.HasSuffix(row.Path, "/") {
		route.PathPrefix(row.Path)
	} else {
		route.Path(row.Path)
	}

	options := g.Options != nil && !row.noOptions && !ownOptions && row.Method != "" && row.Method != http.MethodOptions

	switch {
	case options:
		route.Methods(row.Method, http.MethodOptions)
	case row.Method != "":
		route.Methods(row.Method)
	}

	if len(row.queries) > 0 {
		route.Queries(row.queries...)
	}

	row.Path = g.Prefix + row.Path

	if options {
		route.Handler(serve(row.Handler, g.Options))
	} else {
		route.Handler(row.Handler)
	}

	if err := route.GetError(); err != nil {
		return fmt.Errorf("route %s %s: %w", row.Method, row.Path, err)
	}

	m.matched[route] = matchedRoute{template: row.Path, options: options}
	m.routes = append(m.routes, row)

	return nil
}

// describe is the outermost middleware of every group. It gives the request
// the template of its route, and an OPTIONS request that Options serves its
// Allow header.
func (m *Mux) describe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		matched, ok := m.matched[mux.CurrentRoute(r)]
		if !ok {
			next.ServeHTTP(w, r)

			return
		}

		r = r.WithContext(context.WithValue(r.Context(), templateKey{}, matched.template))

		if matched.options && r.Method == http.MethodOptions {
			m.setAllow(w, r)
		}

		next.ServeHTTP(w, r)
	})
}

// refusal returns the handler of a method that no route of the path takes:
// refuse, or a bare 405, with Allow set.
func (m *Mux) refusal(refuse http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.setAllow(w, r)

		if refuse == nil {
			w.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		refuse.ServeHTTP(w, r)
	})
}

// setAllow sets the Allow header of the answer to r, when a method is allowed.
func (m *Mux) setAllow(w http.ResponseWriter, r *http.Request) {
	if allowed := m.Allowed(r); len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
	}
}

// serve returns the handler of a route whose OPTIONS requests options serves.
func serve(handler, options http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			options.ServeHTTP(w, r)

			return
		}

		handler.ServeHTTP(w, r)
	})
}

// templateKey is the context key of the path template of the matched route.
type templateKey struct{}

// Template returns the path template of the route that matched r, with the
// prefix of its group, or "" when no route matched.
func Template(r *http.Request) string {
	template, _ := r.Context().Value(templateKey{}).(string)

	return template
}
