package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"
	"pgregory.net/rapid"
)

// routedAlias is one method of a deprecated alias route the router serves.
type routedAlias struct {
	servedRoute

	alias deprecatedAlias
}

// routerAliases are the router's deprecated alias routes, OPTIONS left out,
// and every operation it serves, keyed as in an openAPISpec.
func routerAliases(t *testing.T) ([]routedAlias, map[string]bool) {
	t.Helper()

	router, ok := serveContract(t, nil).Config.Handler.(*mux.Router)
	if !ok {
		t.Fatal("contract server handler is not a *mux.Router")
	}

	var (
		aliases []routedAlias
		ops     = map[string]bool{}
	)

	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil {
			return nil //nolint:nilerr // a route of no path
		}

		methods, err := route.GetMethods()
		if err != nil {
			return nil //nolint:nilerr // a route of any method, never an alias
		}

		alias, isAlias := route.GetHandler().(deprecatedAlias)

		for _, method := range methods {
			if method == http.MethodOptions {
				continue
			}

			ops[openAPIKey("api", method, strings.TrimPrefix(path, "/api/v1"))] = true

			if isAlias {
				aliases = append(aliases, routedAlias{servedRoute{method: method, path: path}, alias})
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Router.Walk() error = %v", err)
	}

	return aliases, ops
}

// Exactly the router's alias routes are deprecated in openapi.yml, and each
// alias's successor serves the same method.
func TestDeprecatedAliasesMatchOpenAPI(t *testing.T) {
	aliases, ops := routerAliases(t)

	if len(aliases) == 0 {
		t.Fatal("the router serves no deprecated alias")
	}

	routed := make([]string, 0, len(aliases))

	for _, a := range aliases {
		key := openAPIKey("api", a.method, strings.TrimPrefix(a.path, "/api/v1"))
		routed = append(routed, key)

		if successor := openAPIKey("api", a.method, a.alias.successor); !ops[successor] {
			t.Errorf("alias %s names successor %s, which the router does not serve", a, successor)
		}
	}

	var documented []string

	for path, item := range contractSpec(t).Paths {
		for method, op := range item.Operations() {
			if op.Deprecated {
				documented = append(documented, openAPIKey("api", method, path))
			}
		}
	}

	slices.Sort(routed)
	slices.Sort(documented)

	if diff := cmp.Diff(documented, routed); diff != "" {
		t.Errorf("openapi.yml deprecated operations mismatch the router's aliases (-openapi.yml +router):\n%s", diff)
	}
}

// Every answer of every alias, a refusal, OPTIONS and a method it does not take included, carries
// Deprecation, a Sunset deprecationMonths later, and a Link to the successor behind the base path.
func TestDeprecatedAliasHeaders(t *testing.T) {
	const base = "/phenix/"

	aliases, _ := routerAliases(t)

	bodies := map[string]contractRoute{}
	for _, c := range contractRoutes() {
		bodies[c.String()] = c
	}

	for _, a := range aliases {
		c := contractRoute{method: a.method, path: a.concrete()}
		if known, ok := bodies[c.String()]; ok {
			c = known
		}

		requests := map[string]func(*testing.T, string, contractFixture) *http.Response{
			"as admin": func(t *testing.T, server string, f contractFixture) *http.Response {
				t.Helper()

				return do(t, c.method, c.url(t, server, f, "alice"), c.body, c.header...)
			},
			"without a token": func(t *testing.T, server string, f contractFixture) *http.Response {
				t.Helper()

				return do(t, c.method, c.url(t, server, f, "alice"), c.body, "X-Phenix-Auth-Token", "")
			},
			"OPTIONS": func(t *testing.T, server string, f contractFixture) *http.Response {
				t.Helper()

				return do(t, http.MethodOptions, c.url(t, server, f, "alice"), "")
			},
			"by a method it does not take": func(t *testing.T, server string, f contractFixture) *http.Response {
				t.Helper()

				resp := do(t, http.MethodPatch, c.url(t, server, f, "alice"), "")
				if resp.StatusCode != http.StatusMethodNotAllowed {
					t.Errorf("PATCH %s = %d, want %d", c.path, resp.StatusCode, http.StatusMethodNotAllowed)
				}

				return resp
			},
		}

		for name, send := range requests {
			t.Run(a.String()+"/"+name, func(t *testing.T) {
				f := useContractFixture(t)

				server := serveContractAs(t, adminRole(), func(o *serverOptions) {
					o.basePath = base

					if name == "without a token" {
						o.jwtKey = testJWTKey
					}
				}).URL

				resp := send(t, server, f)

				want := map[string]string{
					"Deprecation": "@" + strconv.FormatInt(a.alias.since.Unix(), 10),
					"Sunset":      a.alias.since.AddDate(0, deprecationMonths, 0).UTC().Format(http.TimeFormat),
					"Link":        "<" + base + "api/v1" + concreteSuccessor(a, resp.Request.URL) + `>; rel="successor-version"`,
				}

				got := map[string]string{}
				for name := range want {
					got[name] = resp.Header.Get(name)
				}

				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("%s %s (%d) deprecation headers mismatch (-want +got):\n%s",
						resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, diff)
				}
			})
		}
	}
}

// concreteSuccessor is the successor's path and query, under /api/v1, for the
// request the alias answered.
func concreteSuccessor(a routedAlias, req *url.URL) string {
	var (
		template = strings.Split(strings.TrimPrefix(a.path, "/api/v1"), "/")
		path     = strings.Split(strings.TrimPrefix(req.Path, "/api/v1"), "/")
		location = a.alias.successor
	)

	for i, segment := range template {
		if strings.HasPrefix(segment, "{") {
			location = strings.ReplaceAll(location, segment, path[i])
		}
	}

	if req.RawQuery != "" {
		location += "?" + req.RawQuery
	}

	return location
}

// An alias deprecated on 2026-10-27 sunsets on 2027-04-27, deprecationMonths
// later, and says so as an HTTP-date.
func TestDeprecatedAliasSunset(t *testing.T) {
	alias := deprecatedAlias{since: time.Date(2026, time.October, 27, 0, 0, 0, 0, time.UTC)}

	if got, want := alias.sunset().Format(http.TimeFormat), "Tue, 27 Apr 2027 00:00:00 GMT"; got != want {
		t.Errorf("sunset() = %q, want %q", got, want)
	}
}

// The successor's location names the request's path variables, escaped, and
// its query, under the base path.
func TestDeprecatedAliasSuccessorLocation(t *testing.T) {
	original := o
	t.Cleanup(func() { o = original })

	o.basePath = "/base/"

	alias := deprecatedAlias{successor: "/experiments/{exp}/captureTokens/{id}"}

	rapid.Check(t, func(t *rapid.T) {
		var (
			exp   = rapid.StringMatching(`[a-zA-Z0-9 ._%?#/-]{1,12}`).Draw(t, "exp")
			id    = rapid.StringMatching(`[a-z0-9_]{1,8}`).Draw(t, "id")
			query = rapid.SampledFrom([]string{"", "a=1", "a=1&b=%20"}).Draw(t, "query")
		)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/x?"+query, nil)
		req = mux.SetURLVars(req, map[string]string{"exp": exp, "id": id})

		location, err := url.Parse(alias.successorLocation(req))
		if err != nil {
			t.Fatalf("successorLocation() = %q, not a URL: %v", alias.successorLocation(req), err)
		}

		if want := "/base/api/v1/experiments/" + exp + "/captureTokens/" + id; location.Path != want {
			t.Errorf("successorLocation() path = %q, want %q", location.Path, want)
		}

		if location.RawQuery != query {
			t.Errorf("successorLocation() query = %q, want %q", location.RawQuery, query)
		}
	})
}
