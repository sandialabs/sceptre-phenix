package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// deprecationMonths is how long a deprecated alias route is served, from its
// deprecation date to its sunset, after which it is removed.
const deprecationMonths = 6

// deprecatedAlias is the handler of a deprecated alias route: the successor's
// handler, which DeprecationHeaders marks as deprecated on every answer.
type deprecatedAlias struct {
	http.Handler

	// successor is the path template, under /api/v1, of the route to use instead
	successor string
	// since is when the alias was deprecated
	since time.Time
}

// sunset is when the alias is removed.
func (d deprecatedAlias) sunset() time.Time { return d.since.AddDate(0, deprecationMonths, 0) }

// successorLocation is the successor's path for r, as a client behind the
// proxy reaches it, with r's path variables and query.
func (d deprecatedAlias) successorLocation(r *http.Request) string {
	path := d.successor

	for name, value := range mux.Vars(r) {
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}

	location := apiLocation(strings.TrimPrefix(path, "/"))
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}

	return location
}

// aliasRoute serves handler at the deprecated path too, for methods (OPTIONS
// added), as an alias of the successor path deprecated since the date.
func aliasRoute(api *mux.Router, path, successor string, since time.Time, handler http.Handler, methods ...string) {
	api.Handle(path, deprecatedAlias{Handler: handler, successor: successor, since: since}).
		Methods(append(methods, http.MethodOptions)...)
}

// markDeprecated sets Deprecation (RFC 9745), Sunset (RFC 8594) and a Link to the successor on w.
func (d deprecatedAlias) markDeprecated(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Deprecation", "@"+strconv.FormatInt(d.since.Unix(), 10))
	h.Set("Sunset", d.sunset().UTC().Format(http.TimeFormat))
	h.Add("Link", "<"+d.successorLocation(r)+`>; rel="successor-version"`)
}

// DeprecationHeaders marks every answer of a deprecated alias route, refusals included.
func DeprecationHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if route := mux.CurrentRoute(r); route != nil {
			if alias, ok := route.GetHandler().(deprecatedAlias); ok {
				alias.markDeprecated(w, r)
			}
		}

		next.ServeHTTP(w, r)
	})
}

// errAliasFound stops the walk of api's routes once an alias matches.
var errAliasFound = errors.New("alias found")

// deprecatedMethodNotAllowed marks a 405 for a deprecated alias's path, which mux answers without middleware.
func deprecatedMethodNotAllowed(api *mux.Router, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = api.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
			alias, ok := route.GetHandler().(deprecatedAlias)
			if !ok {
				return nil
			}

			methods, err := route.GetMethods()
			if err != nil || len(methods) == 0 {
				return nil //nolint:nilerr // a route without methods is no alias
			}

			probe := r.Clone(r.Context())
			probe.Method = methods[0]

			var match mux.RouteMatch
			if !route.Match(probe, &match) {
				return nil
			}

			alias.markDeprecated(w, mux.SetURLVars(r, match.Vars))

			return errAliasFound
		})

		next.ServeHTTP(w, r)
	})
}
