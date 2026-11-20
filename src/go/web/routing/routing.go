// Package routing routes the requests of the phenix server by a table of
// routes. It owns the table, the registration of its rows, and the answers for
// a request that matches no route.
package routing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
)

var (
	// errNoHandler is the error of a row that has no handler.
	errNoHandler = errors.New("route has no handler")
	// errRepeatedPrefix is the error of a group whose prefix an earlier group has.
	errRepeatedPrefix = errors.New("another group has the same prefix")
	// errRefused is the error of a table that [http.ServeMux] refuses.
	errRefused = errors.New("the router refuses the route")
	// errBadPrefix is the error of a group prefix that is not "" and does not
	// start with "/", or that ends with "/" or has "{", "}", "%", a space or a tab.
	errBadPrefix = errors.New("the prefix must start with / and must not end with / or have {, }, %, a space or a tab")
	// errNilMiddleware is the error of a group with a nil middleware.
	errNilMiddleware = errors.New("the group has a nil middleware")
	// errBadMethod is the error of a row whose method Allowed does not name.
	errBadMethod = errors.New("the method is not \"\" or one of " + strings.Join(methods(), ", "))
	// errBadPath is the error of a row whose path does not start with "/", or
	// that has a space or a tab.
	errBadPath = errors.New("the path does not start with / or has a space or a tab")
	// errHeadOfAnotherMethod is the error of a row WithoutHead whose method is
	// not GET.
	errHeadOfAnotherMethod = errors.New("only a GET route can be without HEAD")
)

// variable matches a {name} variable of a path.
var variable = regexp.MustCompile(`\{[^}]*\}`)

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
	// for every method. A GET route also serves HEAD for its path, unless it
	// is WithoutHead or a HEAD route has the same path. A HEAD route whose
	// path overlaps the path of another route, but is not the same, can make
	// NewMux return an error, as two such GET routes do.
	Method string
	// Path is the path below the group prefix, with {name} variables. It
	// starts with "/". A variable has a name only, with no pattern, and holds
	// one segment. A {name...} variable takes the rest of the path and can
	// hold "/". A trailing "/" also matches every path below it.
	Path    string
	Handler http.Handler
	// noHead is set by WithoutHead.
	noHead bool
}

// Row returns the route of one method and path. Rows are written with Row,
// not with struct literals: the exhaustruct linter is on.
func Row(method, path string, handler http.Handler) Route {
	return Route{Method: method, Path: path, Handler: handler, noHead: false}
}

// WithoutHead returns r as a GET route that does not serve HEAD, for a
// handler that changes state, starts a stream or upgrades to a websocket. A
// HEAD request to its path is served by a HEAD route of the same path, else
// by a route of every method that matches the path, else by
// MethodNotAllowed. No other GET route serves it.
func (r Route) WithoutHead() Route {
	r.noHead = true

	return r
}

// RefusesHead reports whether r is WithoutHead.
func (r Route) RefusesHead() bool {
	return r.noHead
}

// Group is a set of routes below one path prefix, with shared middleware and
// refusals.
type Group struct {
	// Prefix is "" or a path such as "/api/v1", without a trailing "/". It is a
	// string prefix: "/api/v1" also takes "/api/v1x". Each group has its own.
	// A row whose full path starts with a longer prefix of another group is
	// never served.
	Prefix string
	Routes []Route
	// Middleware runs for a matched route only. The first is the outermost of
	// the group. None is nil.
	Middleware []func(http.Handler) http.Handler
	// Options, when not nil, serves OPTIONS behind Middleware for each path of
	// the group, with Allow set. A route of OPTIONS serves OPTIONS itself for
	// its path, and so does a route of every method when it is the only route
	// of its path.
	Options http.Handler
	// NotFound answers a path of the group that no route matches. When nil, the
	// NotFound of the group with prefix "" answers, and without one,
	// http.NotFound.
	NotFound http.Handler
	// MethodNotAllowed answers a method that no route of the path takes, with
	// Allow set. When nil, the answer is a 405 without a body.
	MethodNotAllowed http.Handler
}

// Mux serves requests by the routes of its groups. A request goes to the
// group with the longest prefix of its path, and only the routes of that
// group can serve it. A literal path segment wins over a variable. A HEAD
// request goes to the route that a GET request of its path goes to, or to a
// HEAD route of the same path. Routes match the decoded path, so an escaped
// "/" (%2F) separates segments.
type Mux struct {
	// groups are longest prefix first, and the last has prefix "".
	groups []*group
	routes []Route
}

// group is a registered Group. HEAD requests match in head, where each GET
// route is one of HEAD unless a HEAD route has its path, and all other
// requests in rows. every has the routes of every method only, for the HEAD
// of a route WithoutHead.
type group struct {
	prefix            string
	rows, head, every *http.ServeMux
	middleware        []func(http.Handler) http.Handler
	notFound          http.Handler
	methodNotAllowed  http.Handler
}

// NewMux returns the Mux of the groups. It returns an error for a group or a
// route that breaks the rules of [Group] and [Route], for a route
// WithoutHead whose method is not GET, and for a table the router refuses,
// such as a repeated route or two routes whose paths overlap and neither is
// more specific.
func NewMux(groups ...Group) (*Mux, error) {
	m := &Mux{groups: nil, routes: nil}

	var (
		prefixes = map[string]bool{}
		notFound = http.NotFoundHandler()
	)

	for _, g := range groups {
		if prefixes[g.Prefix] {
			return nil, fmt.Errorf("group %q: %w", g.Prefix, errRepeatedPrefix)
		}

		if g.Prefix != "" &&
			(!strings.HasPrefix(g.Prefix, "/") || strings.HasSuffix(g.Prefix, "/") || strings.ContainsAny(g.Prefix, "{}% \t")) {
			return nil, fmt.Errorf("group %q: %w", g.Prefix, errBadPrefix)
		}

		if slices.ContainsFunc(g.Middleware, func(mw func(http.Handler) http.Handler) bool { return mw == nil }) {
			return nil, fmt.Errorf("group %q: %w", g.Prefix, errNilMiddleware)
		}

		prefixes[g.Prefix] = true

		if g.Prefix == "" && g.NotFound != nil {
			notFound = g.NotFound
		}
	}

	if !prefixes[""] {
		groups = append(slices.Clone(groups), Group{
			Prefix: "", Routes: nil, Middleware: nil, Options: nil, NotFound: nil, MethodNotAllowed: nil,
		})
	}

	for _, g := range groups {
		if err := m.add(g, notFound); err != nil {
			return nil, err
		}
	}

	slices.SortStableFunc(m.groups, func(a, b *group) int { return len(b.prefix) - len(a.prefix) })

	return m, nil
}

// ServeHTTP serves r by the route that matches it. A path that is not clean
// gets a 301 to its clean form.
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if clean := cleanPath(r.URL.Path); clean != r.URL.Path {
		u := *r.URL
		u.Path = clean

		w.Header().Set("Location", u.String())
		w.WriteHeader(http.StatusMovedPermanently)

		return
	}

	r = decoded(r)
	g := m.group(r)

	if rows, e := g.lookup(r, r.Method); e != nil {
		rows.ServeHTTP(w, r)

		return
	}

	if !m.setAllow(w, r) {
		g.notFound.ServeHTTP(w, r)

		return
	}

	if g.methodNotAllowed == nil {
		w.WriteHeader(http.StatusMethodNotAllowed)

		return
	}

	g.methodNotAllowed.ServeHTTP(w, r)
}

// Routes returns the rows of the table as given, in order, each Path with the
// prefix of its group.
func (m *Mux) Routes() []Route {
	return slices.Clone(m.routes)
}

// Allowed returns the methods that a route takes at the path of r, in the
// order GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS. HEAD is among them
// where a route serves it: not at the path of a GET route WithoutHead, unless
// a route of every method takes that path.
func (m *Mux) Allowed(r *http.Request) []string {
	r = decoded(r)

	var (
		g       = m.group(r)
		allowed []string
	)

	for _, method := range methods() {
		if _, e := g.lookup(r, method); e != nil {
			allowed = append(allowed, method)
		}
	}

	return allowed
}

// setAllow sets the Allow header of the answer to r, and reports whether a
// method is allowed.
func (m *Mux) setAllow(w http.ResponseWriter, r *http.Request) bool {
	allowed := m.Allowed(r)
	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
	}

	return len(allowed) > 0
}

// decoded returns r with a copy of its URL whose RawPath is "", so that the
// routes match and serve the decoded path.
func decoded(r *http.Request) *http.Request {
	if r.URL.RawPath == "" {
		return r
	}

	u := *r.URL
	u.RawPath = ""

	d := new(http.Request)
	*d = *r
	d.URL = &u

	return d
}

// group returns the group of r: the group with the longest prefix of its path.
func (m *Mux) group(r *http.Request) *group {
	for _, g := range m.groups {
		if strings.HasPrefix(r.URL.Path, g.prefix) {
			return g
		}
	}

	// NewMux adds the group of prefix "" when the table has none
	return m.groups[len(m.groups)-1]
}

// lookup returns the route that serves r with method, and the ServeMux that
// has it, or nil. HEAD goes to the route that GET goes to, and from a route
// WithoutHead on to a route of every method, as a method that no route of
// the path takes.
func (g *group) lookup(r *http.Request, method string) (*http.ServeMux, *entry) {
	if method != http.MethodHead {
		return match(g.rows, r, method)
	}

	if rows, e := match(g.head, r, method); e == nil || !e.refusesHead {
		return rows, e
	}

	return match(g.every, r, method)
}

// match returns the route of rows that serves r with method, and rows, or nil.
func match(rows *http.ServeMux, r *http.Request, method string) (*http.ServeMux, *entry) {
	probe := r
	if method != r.Method {
		probe = r.Clone(r.Context())
		probe.Method = method
	}

	// a redirect of the ServeMux, to the path with a trailing "/", is no match
	handler, _ := rows.Handler(probe)

	e, ok := handler.(*entry)
	if !ok {
		return nil, nil
	}

	return rows, e
}

// add registers the routes of g. notFound answers in place of a nil NotFound.
func (m *Mux) add(g Group, notFound http.Handler) error {
	reg := &group{
		prefix: g.Prefix, rows: http.NewServeMux(), head: http.NewServeMux(), every: http.NewServeMux(),
		middleware: slices.Clone(g.Middleware), notFound: g.NotFound, methodNotAllowed: g.MethodNotAllowed,
	}

	if reg.notFound == nil {
		reg.notFound = notFound
	}

	ownHead := map[string]bool{}

	for _, row := range g.Routes {
		if row.Method == http.MethodHead {
			ownHead[shape(g.Prefix+row.Path)] = true
		}
	}

	for _, row := range g.Routes {
		if err := checkRow(row); err != nil {
			return fmt.Errorf("route %s %s%s: %w", row.Method, g.Prefix, row.Path, err)
		}

		row.Path = g.Prefix + row.Path

		if err := reg.register(row, m.entry(reg, row.Path, row.Handler, false), ownHead[shape(row.Path)]); err != nil {
			return err
		}

		m.routes = append(m.routes, row)
	}

	if g.Options != nil {
		for _, p := range optionsPaths(g.Routes) {
			p = g.Prefix + p

			if err := handle(reg.rows, http.MethodOptions+" "+p, m.entry(reg, p, g.Options, true)); err != nil {
				return err
			}
		}
	}

	m.groups = append(m.groups, reg)

	return nil
}

// register enters the entry e of row in the ServeMuxes of g. ownHead is set
// when a HEAD route of g has the path of row.
func (g *group) register(row Route, e *entry, ownHead bool) error {
	switch row.Method {
	case "":
		return errors.Join(handle(g.rows, row.Path, e), handle(g.head, row.Path, e), handle(g.every, row.Path, e))
	case http.MethodHead:
		return handle(g.head, http.MethodHead+" "+row.Path, e)
	case http.MethodGet:
		if err := handle(g.rows, http.MethodGet+" "+row.Path, e); err != nil || ownHead {
			return err
		}

		if row.noHead {
			refusal := *e
			refusal.refusesHead = true
			e = &refusal
		}

		return handle(g.head, http.MethodHead+" "+row.Path, e)
	default:
		return handle(g.rows, row.Method+" "+row.Path, e)
	}
}

// checkRow returns an error for row when it breaks a rule of [Route].
func checkRow(row Route) error {
	switch {
	case row.Method != "" && !slices.Contains(methods(), row.Method):
		return errBadMethod
	case !strings.HasPrefix(row.Path, "/") || strings.ContainsAny(row.Path, " \t"):
		return errBadPath
	case row.Handler == nil:
		return errNoHandler
	case row.noHead && row.Method != http.MethodGet:
		return errHeadOfAnotherMethod
	}

	return nil
}

// optionsPaths returns the paths of routes whose OPTIONS requests the Options
// of their group serves: one for each path, in the order of the routes.
func optionsPaths(routes []Route) []string {
	var (
		paths   []string
		served  = map[string]bool{}
		ownPath = map[string]bool{}
	)

	for _, row := range routes {
		if row.Method == http.MethodOptions {
			ownPath[shape(row.Path)] = true
		}
	}

	for _, row := range routes {
		key := shape(row.Path)

		if row.Method == "" || row.Method == http.MethodOptions || served[key] || ownPath[key] {
			continue
		}

		served[key] = true
		paths = append(paths, row.Path)
	}

	return paths
}

// shape returns p with its variables unnamed, so that two paths that differ
// only by the names of their variables have the same shape. {$} and
// {name...} keep their kinds.
func shape(p string) string {
	return variable.ReplaceAllStringFunc(p, func(v string) string {
		switch {
		case v == "{$}":
			return v
		case strings.HasSuffix(v, "...}"):
			return "{...}"
		}

		return "{}"
	})
}

// handle registers e at pattern in rows, and returns the error of a pattern
// that rows refuses.
func handle(rows *http.ServeMux, pattern string, e *entry) (err error) {
	defer func() {
		if refusal := recover(); refusal != nil {
			err = fmt.Errorf("route %s: %w: %v", pattern, errRefused, refusal)
		}
	}()

	rows.Handle(pattern, e)

	return nil
}

// entry is a registered route: the handler of its group's middleware and
// its own handler.
type entry struct {
	mux      *Mux
	group    *group
	template string
	handler  http.Handler
	// options is set for the route of an OPTIONS request that Options serves.
	options bool
	// refusesHead is set for the HEAD of a route WithoutHead, which lookup
	// never returns.
	refusesHead bool
}

// entry returns the entry of a route of g at the path template, prefix
// included.
func (m *Mux) entry(g *group, template string, handler http.Handler, options bool) *entry {
	return &entry{mux: m, group: g, template: template, handler: handler, options: options, refusesHead: false}
}

// ServeHTTP gives r the template of its route, and an OPTIONS request that
// Options serves its Allow header, then serves r behind the middleware.
func (e *entry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(context.WithValue(r.Context(), templateKey{}, e.template))

	if e.options {
		e.mux.setAllow(w, r)
	}

	handler := e.handler
	for _, mw := range slices.Backward(e.group.middleware) {
		handler = mw(handler)
	}

	handler.ServeHTTP(w, r)
}

// cleanPath returns the canonical form of p: rooted, without "." and ".."
// elements or repeated "/", and with its trailing "/" kept.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}

	if p[0] != '/' {
		p = "/" + p
	}

	clean := path.Clean(p)
	if p[len(p)-1] == '/' && clean != "/" {
		clean += "/"
	}

	return clean
}

// templateKey is the context key of the path template of the matched route.
type templateKey struct{}

// Template returns the path template of the route that matched r, with the
// prefix of its group, or "" when no route matched.
func Template(r *http.Request) string {
	template, _ := r.Context().Value(templateKey{}).(string)

	return template
}
