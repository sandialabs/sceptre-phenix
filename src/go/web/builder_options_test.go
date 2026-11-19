package web

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	"phenix/api/disk"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/web/routing"
)

// builderCountingRecords is a record store that counts every call made to it.
type builderCountingRecords struct {
	store.RecordStore

	calls atomic.Int32
}

func (c *builderCountingRecords) ListRecords(namespace, prefix string) (store.Records, error) {
	c.calls.Add(1)

	return c.RecordStore.ListRecords(namespace, prefix)
}

func (c *builderCountingRecords) ListRecordKeys(namespace, prefix string) ([]string, error) {
	c.calls.Add(1)

	return c.RecordStore.ListRecordKeys(namespace, prefix)
}

func (c *builderCountingRecords) GetRecord(namespace, key string) (store.Record, error) {
	c.calls.Add(1)

	return c.RecordStore.GetRecord(namespace, key)
}

func (c *builderCountingRecords) CreateRecord(namespace, key string, value []byte) (store.Record, error) {
	c.calls.Add(1)

	return c.RecordStore.CreateRecord(namespace, key, value)
}

func (c *builderCountingRecords) UpdateRecord(
	namespace, key string,
	value []byte,
	expectedRevision int64,
) (store.Record, error) {
	c.calls.Add(1)

	return c.RecordStore.UpdateRecord(namespace, key, value, expectedRevision)
}

func (c *builderCountingRecords) DeleteRecord(namespace, key string, expectedRevision int64) error {
	c.calls.Add(1)

	return c.RecordStore.DeleteRecord(namespace, key, expectedRevision)
}

func (c *builderCountingRecords) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	c.calls.Add(1)

	return c.RecordStore.DeleteRecordPrefix(namespace, prefix)
}

func (c *builderCountingRecords) RecordTxn(fn func(store.RecordTx) error) error {
	c.calls.Add(1)

	return c.RecordStore.RecordTxn(fn)
}

// builderRouteMethods returns, for each path template of the routes, the
// methods of every route whose path matches that path with "x" for each
// variable, as the router matches it.
func builderRouteMethods(routes []routing.Route) map[string][]string {
	type route struct {
		template string
		pattern  *regexp.Regexp
		method   string
	}

	var (
		variable = regexp.MustCompile(`\{[^}]+\}`)
		matched  = make([]route, 0, len(routes))
	)

	for _, r := range routes {
		parts := variable.Split(r.Path, -1)
		for i, part := range parts {
			parts[i] = regexp.QuoteMeta(part)
		}

		pattern := regexp.MustCompile("^" + strings.Join(parts, "[^/]+") + "$")
		matched = append(matched, route{template: r.Path, pattern: pattern, method: r.Method})
	}

	order := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	}
	allowed := map[string][]string{}

	for _, r := range matched {
		path := variable.ReplaceAllString(r.template, "x")

		for _, other := range matched {
			if other.pattern.MatchString(path) {
				allowed[r.template] = append(allowed[r.template], other.method)
			}
		}
	}

	for template, methods := range allowed {
		methods = append(methods, http.MethodOptions)
		slices.SortFunc(methods, func(a, b string) int { return slices.Index(order, a) - slices.Index(order, b) })
		allowed[template] = slices.Compact(methods)
	}

	return allowed
}

// TestBuilderOptionsRunNoHandler asserts OPTIONS on every Builder route
// answers 204 with the methods of its path in Allow, as anyone, and that no
// route handler runs: no record is read or written, no config is read, no
// disk image is listed and no cluster host is read.
func TestBuilderOptionsRunNoHandler(t *testing.T) {
	var (
		records   = &builderCountingRecords{RecordStore: memrecord.New(), calls: atomic.Int32{}}
		diskCalls atomic.Int32
		hostReads atomic.Int32
	)

	service, err := bapi.New(bapi.WithStore(records))
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	harness := newBuilderHarnessWith(t, []builderOption{
		withBuilderService(service),
		withBuilderDisks(func() ([]disk.Details, error) {
			diskCalls.Add(1)

			return nil, nil
		}),
		withBuilderPreflightSources(preflightSources(&hostReads)),
	})

	// The generic schema route that /schemas/builder/v1 also matches,
	// registered after the Builder routes as [Start] registers it.
	harness.handle(routing.Row(http.MethodGet, "/schemas/{kind}/{version}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OPTIONS reached the generic schema route")
		w.WriteHeader(http.StatusTeapot)
	})))

	routes := builderRouteMethods(harness.router.Routes())
	records.calls.Store(0)

	var (
		configLists = len(harness.configLists)
		configGets  = harness.configGets
		fileReads   = harness.fileReads
		variable    = regexp.MustCompile(`\{[^}]+\}`)
		checked     = 0
	)

	for template, methods := range routes {
		path := strings.TrimPrefix(template, builderAPIPrefix)
		if !builderPath(template) || path == "/schemas/{kind}/{version}" {
			continue
		}

		path = variable.ReplaceAllString(path, "x")
		checked++

		for _, user := range []string{builderAlice, ""} {
			t.Run(path+" as "+user, func(t *testing.T) {
				recorder := harness.do(builderRequest{method: http.MethodOptions, path: path, user: user})

				got := map[string]any{
					"status": recorder.Code, "body": recorder.Body.String(),
					"Allow":                  recorder.Header().Get("Allow"),
					"X-Content-Type-Options": recorder.Header().Get("X-Content-Type-Options"),
					"Cache-Control":          recorder.Header().Get("Cache-Control"),
				}
				want := map[string]any{
					"status": http.StatusNoContent, "body": "",
					"Allow":                  strings.Join(methods, ", "),
					"X-Content-Type-Options": builderNoSniff,
					"Cache-Control":          builderNoStore,
				}

				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("OPTIONS %s as %q mismatch (-want +got):\n%s", path, user, diff)
				}
			})
		}
	}

	if checked < 25 {
		t.Errorf("checked OPTIONS on %d Builder paths, want every one (at least 25)", checked)
	}

	if calls := records.calls.Load(); calls != 0 {
		t.Errorf("OPTIONS on every Builder path made %d record store calls, want none", calls)
	}

	if lists, gets := len(harness.configLists)-configLists, harness.configGets-configGets; lists != 0 || gets != 0 {
		t.Errorf("OPTIONS on every Builder path made %d config listings and %d config reads, want none", lists, gets)
	}

	if reads := harness.fileReads - fileReads; reads != 0 {
		t.Errorf("OPTIONS on every Builder path read the Builder files %d times, want never", reads)
	}

	if harness.configWrites != 0 || harness.experimentWrites != 0 || harness.broadcasts != 0 {
		t.Errorf("OPTIONS on every Builder path wrote %d configs, %d experiments and %d broadcasts, want none",
			harness.configWrites, harness.experimentWrites, harness.broadcasts)
	}

	if listings, hosts := diskCalls.Load(), hostReads.Load(); listings != 0 || hosts != 0 {
		t.Errorf("OPTIONS on every Builder path listed the disk images %d times and read the hosts %d times, want never",
			listings, hosts)
	}
}

// TestBuilderOptionsOfAnUnknownPath asserts OPTIONS on a Builder path that
// no route has answers 404, as any other method does.
func TestBuilderOptionsOfAnUnknownPath(t *testing.T) {
	harness := newBuilderHarness(t)

	for _, path := range []string{"/builder/no-such-route", builderIconsRoute + "/x/y"} {
		recorder := harness.do(builderRequest{method: http.MethodOptions, path: path, user: builderAlice})

		if recorder.Code != http.StatusNotFound || recorder.Header().Get("Allow") != "" {
			t.Errorf("OPTIONS %s = %d, Allow %q; want %d and no Allow",
				path, recorder.Code, recorder.Header().Get("Allow"), http.StatusNotFound)
		}
	}
}
