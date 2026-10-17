package web

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
)

const (
	builderTemplatesRoute = "/builder/templates"

	// builderTemplateSecret is in the spec of every template the tests
	// make, so a log or an error that repeats a template is seen.
	builderTemplateSecret = "s3cret-template-setting"
)

// The ids of the built-in templates, in the order the listing gives them.
var builderBuiltinIDs = []string{"server", "workstation", "router", "firewall", "external"} //nolint:gochecknoglobals // test fixture

// builderTemplateContentOf returns a valid template whose device is named
// after name, and which names the given custom icon when icon is not "".
func builderTemplateContentOf(name, icon string) builderTemplateContent {
	return builderTemplateContent{
		Name:        name,
		Description: "made by a test",
		Device: bdoc.TemplateDevice{
			IconKey: bdoc.IconServer,
			Icon:    icon,
			Spec: map[string]any{
				"type":    "VirtualMachine",
				"general": map[string]any{"hostname": "host-" + strings.Join(strings.Fields(strings.ToLower(name)), "-")},
				"labels":  map[string]any{"note": builderTemplateSecret},
			},
		},
	}
}

// builderTemplateIconName is the custom icon the template PLC of the fixture
// names: a name the icon library resolves.
const builderTemplateIconName = "plc-icon"

// builderLibraryPath returns a path below the template library.
func builderLibraryPath(rest string) string {
	return builderTemplatesRoute + rest
}

// library makes a request of the template library and checks it is JSON.
func (h *builderHarness) library(method, path, user, body, ifMatch string) *httptest.ResponseRecorder {
	h.t.Helper()

	recorder := h.do(builderRequest{method: method, path: path, body: body, user: user, ifMatch: ifMatch})

	assertBuilderJSON(h.t, method+" "+path, recorder)

	return recorder
}

// post makes a POST below the library, as alice.
func (h *builderHarness) post(rest, body string) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.library(http.MethodPost, builderLibraryPath(rest), builderAlice, body, "")
}

// put makes a PUT below the library, as alice.
func (h *builderHarness) put(rest, body, ifMatch string) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.library(http.MethodPut, builderLibraryPath(rest), builderAlice, body, ifMatch)
}

// templates returns what the listing answers the user.
func (h *builderHarness) templates(user string) builderTemplateLibraryResponse {
	h.t.Helper()

	recorder := h.library(http.MethodGet, builderTemplatesRoute, user, "", "")
	if recorder.Code != http.StatusOK {
		h.t.Fatalf("listing templates: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var list builderTemplateLibraryResponse

	h.decode(recorder, &list)

	return list
}

// addTemplates adds templates with the given names to the library.
func (h *builderHarness) addTemplates(names ...string) []builderTemplateRef {
	h.t.Helper()

	request := builderTemplateCreateRequest{Templates: nil, Collection: nil}
	for _, name := range names {
		request.Templates = append(request.Templates, builderTemplateContentOf(name, ""))
	}

	recorder := h.library(http.MethodPost, builderLibraryPath("/items"), builderAlice, builderJSON(h.t, request), "")
	if recorder.Code != http.StatusCreated {
		h.t.Fatalf("adding templates: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	var created builderTemplateCreateResponse

	h.decode(recorder, &created)

	return created.Created
}

// builderTemplateIDs returns the ids of the listed templates, in order.
func builderTemplateIDs(list builderTemplateLibraryResponse) []string {
	ids := make([]string, 0, len(list.Templates))

	for _, template := range list.Templates {
		ids = append(ids, template.ID)
	}

	return ids
}

// TestBuilderTemplateLibraryStartsEmpty asserts the whole listing of a new
// library: empty, the five built-in templates apart, and nothing written.
func TestBuilderTemplateLibraryStartsEmpty(t *testing.T) {
	harness := newBuilderHarness(t)

	recorder := harness.library(http.MethodGet, builderTemplatesRoute, builderAlice, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("listing templates: status = %d: %s", recorder.Code, recorder.Body)
	}

	var raw map[string]json.RawMessage

	harness.decode(recorder, &raw)

	// The icon library holds the icons: a template listing carries none.
	if keys := slices.Sorted(maps.Keys(raw)); !slices.Equal(
		keys, []string{"builtins", "collections", "damaged", "limits", "preloaded", "templates"},
	) {
		t.Fatalf("the listing has the fields %q: %s", keys, recorder.Body)
	}

	for field, want := range map[string]string{
		"templates": "[]", "collections": "[]", "preloaded": "[]", "damaged": "false",
		"limits": `{"templates":200,"collections":50,"nameBytes":128,"descriptionBytes":1024,"deviceBytes":16384}`,
	} {
		if got := string(raw[field]); got != want {
			t.Errorf("%s = %s, want %s", field, got, want)
		}
	}

	var builtins []json.RawMessage

	if err := json.Unmarshal(raw["builtins"], &builtins); err != nil || len(builtins) != len(builderBuiltinIDs) {
		t.Fatalf("the built-in templates = %s, want the five built-in ones", raw["builtins"])
	}

	// A built-in template has no times and no users, and every list is a
	// list.
	if got, want := string(builtins[0]), `{"id":"server","source":"builtin","name":"Server",`+
		`"description":"Generic Linux server","device":{"iconKey":"server","spec":{"general":{"description":"",`+
		`"hostname":"server","vm_type":"kvm"},"hardware":{"drives":[{"image":"ubuntu.qc2"}],"os_type":"linux"},`+
		`"network":{"interfaces":[]},"type":"VirtualMachine"}},"version":1,"etag":"\"1\"","collections":[]}`; got != want {
		t.Errorf("the first built-in template = %s, want %s", got, want)
	}

	list := harness.templates(builderBob)
	expected := bdoc.BuiltinTemplates()

	for i, template := range list.Builtins {
		if template.ID != builderBuiltinIDs[i] || template.Name != expected[i].Name || template.Source != "builtin" ||
			template.Version != 1 || template.ETag != `"1"` || !template.Created.IsZero() || template.CreatedBy != "" {
			t.Errorf("built-in template %d = %+v, want the built-in %q", i, template, builderBuiltinIDs[i])
		}
	}

	if got := harness.store.Count(bapi.NamespaceTemplates); got != 0 {
		t.Fatalf("listing wrote %d library records, want 0", got)
	}
}

// builderTemplateFixture is a library of the templates PLC (with the icon
// builderTemplateIconName) and HMI, and their collection "Plant floor".
type builderTemplateFixture struct {
	harness         *builderHarness
	logs            *plogtest.Logs
	plc, hmi, floor string
}

// newBuilderTemplateFixture makes the fixture with one request.
func newBuilderTemplateFixture(t *testing.T) *builderTemplateFixture {
	t.Helper()

	fixture := &builderTemplateFixture{harness: newBuilderHarness(t), logs: plogtest.Capture(t)}

	request := builderTemplateCreateRequest{
		Templates: []builderTemplateContent{
			builderTemplateContentOf("PLC", builderTemplateIconName), builderTemplateContentOf("HMI", ""),
		},
		Collection: nil,
	}

	body := strings.Replace(builderJSON(t, request), `"collection":null`, `"collection":{"name":"Plant floor","description":"line 1"}`, 1)

	created := fixture.harness.post("/items", body)
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != "" {
		t.Fatalf("adding templates = %d %s, want 201 and no entity tag", created.Code, created.Body)
	}

	var made builderTemplateCreateResponse

	fixture.harness.decode(created, &made)

	if len(made.Created) != 2 || made.Collection == nil || made.Created[0].ETag != `"1"` || made.Collection.ETag != `"1"` ||
		made.Created[0].ID == made.Created[1].ID || !bapi.ValidID(made.Created[0].ID) || !bapi.ValidID(made.Collection.ID) {
		t.Fatalf("adding templates answered %s", created.Body)
	}

	fixture.plc, fixture.hmi, fixture.floor = made.Created[0].ID, made.Created[1].ID, made.Collection.ID

	return fixture
}

// assertLogged asserts what was done is logged by id, and that the log
// holds nothing a template or a collection holds.
func (f *builderTemplateFixture) assertLogged(t *testing.T, messages ...string) {
	t.Helper()

	for _, message := range messages {
		if len(f.logs.Records(t, plogtest.Message(message))) == 0 {
			t.Errorf("the log does not say %q", message)
		}
	}

	if text := f.logs.String(); strings.Contains(text, builderTemplateSecret) || strings.Contains(text, "Plant floor") {
		t.Errorf("the log holds what a template or a collection holds: %s", text)
	}
}

// TestBuilderTemplateCreate adds templates and their collection in one
// request, and lists them with their custom icons.
func TestBuilderTemplateCreate(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	list := fixture.harness.templates(builderAlice)

	// The new templates are in the order sent.
	want := []string{fixture.plc, fixture.hmi}

	if got := builderTemplateIDs(list); !slices.Equal(got, want) {
		t.Fatalf("the listing holds %q, want %q", got, want)
	}

	plc := list.Templates[0]

	if plc.Name != "PLC" || plc.Description != "made by a test" || plc.Device.Icon != builderTemplateIconName ||
		plc.Device.IconKey != bdoc.IconServer || plc.Version != 1 || plc.Created.IsZero() || plc.Source != "library" ||
		!plc.Created.Equal(plc.Updated) || !slices.Equal(plc.Collections, []string{fixture.floor}) {
		t.Fatalf("the new template = %+v", plc)
	}

	if len(list.Collections) != 1 {
		t.Fatalf("the listing holds %d collections, want 1", len(list.Collections))
	}

	floor := list.Collections[0]

	if floor.ID != fixture.floor || floor.Name != "Plant floor" || floor.Description != "line 1" ||
		!slices.Equal(floor.TemplateIDs, []string{fixture.plc, fixture.hmi}) || floor.CreatedBy != builderAlice ||
		floor.Source != "library" || floor.Version != 1 || floor.Created.IsZero() {
		t.Fatalf("the new collection = %+v", floor)
	}

	fixture.assertLogged(t, "added builder templates")
}

// TestBuilderTemplateReplace asserts replacing a template needs the entity
// tag of its content, which moves on only when the content changes.
func TestBuilderTemplateReplace(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness

	content := builderJSON(t, builderTemplateUpdateRequest{builderTemplateContent: builderTemplateContentOf("PLC two", "")})
	path := "/items/" + fixture.plc

	replaced := harness.put(path, content, `"1"`)

	var template builderTemplateResponse

	harness.decode(replaced, &template)

	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != `"2"` || template.ETag != `"2"` ||
		template.ID != fixture.plc || template.Name != "PLC two" || template.Version != 2 || template.Device.Icon != "" ||
		!template.Updated.After(template.Created) || !slices.Equal(template.Collections, []string{fixture.floor}) {
		t.Fatalf("replacing a template = %d %s", replaced.Code, replaced.Body)
	}

	// The same content again changes nothing and keeps the tag.
	again := harness.put(path, content, `"2"`)
	if again.Code != http.StatusOK || again.Header().Get("ETag") != `"2"` {
		t.Fatalf("replacing with the same content = %d %v", again.Code, again.Header())
	}

	// The tag that was replaced no longer is the content's.
	stale := harness.put(path, content, `"1"`)
	if stale.Code != http.StatusPreconditionFailed || stale.Header().Get("ETag") != `"2"` ||
		builderRefusal(t, stale).Code != string(bdoc.CodeTemplateItemStale) {
		t.Fatalf("replacing with the old tag = %d %v %s, want 412 with the current tag", stale.Code, stale.Header(), stale.Body)
	}

	// A replacement may name any custom icon, which the icon library
	// resolves.
	drawn := builderJSON(t, builderTemplateUpdateRequest{
		builderTemplateContent: builderTemplateContentOf("PLC three", "other-icon"),
	})

	back := harness.put(path, drawn, `"2"`)

	var named builderTemplateResponse

	harness.decode(back, &named)

	if back.Code != http.StatusOK || back.Header().Get("ETag") != `"3"` || named.Device.Icon != "other-icon" {
		t.Fatalf("replacing with an icon = %d %s", back.Code, back.Body)
	}

	fixture.assertLogged(t, "changed builder template")
}

// TestBuilderTemplateCollections asserts a collection is added and replaced
// like a template, under an entity tag of its own.
func TestBuilderTemplateCollections(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness
	floor := "/collections/" + fixture.floor

	added := harness.post("/collections", `{"name":"Spares","description":"","templateIds":["`+fixture.hmi+`"]}`)

	var spares builderTemplateCollectionResponse

	harness.decode(added, &spares)

	if added.Code != http.StatusCreated || added.Header().Get("ETag") != `"1"` || spares.Name != "Spares" || spares.Version != 1 ||
		!slices.Equal(spares.TemplateIDs, []string{fixture.hmi}) || spares.CreatedBy != builderAlice ||
		spares.Source != "library" || spares.ID == fixture.floor {
		t.Fatalf("adding a collection = %d %s", added.Code, added.Body)
	}

	// A template may be in several collections.
	list := harness.templates(builderAlice)

	if hmi := list.Templates[1]; !slices.Equal(hmi.Collections, []string{fixture.floor, spares.ID}) {
		t.Fatalf("the template is listed in the collections %q", hmi.Collections)
	}

	body := `{"name":"Plant floor","description":"line 1","templateIds":["` + fixture.hmi + `"]}`
	changed := harness.put(floor, body, `"1"`)

	var collection builderTemplateCollectionResponse

	harness.decode(changed, &collection)

	if changed.Code != http.StatusOK || changed.Header().Get("ETag") != `"2"` || collection.ETag != `"2"` ||
		!slices.Equal(collection.TemplateIDs, []string{fixture.hmi}) || !collection.Updated.After(collection.Created) {
		t.Fatalf("replacing a collection = %d %s", changed.Code, changed.Body)
	}

	// The same content again changes nothing; the old tag is stale.
	if again := harness.put(floor, body, `"2"`); again.Code != http.StatusOK || again.Header().Get("ETag") != `"2"` {
		t.Fatalf("replacing with the same content = %d %v", again.Code, again.Header())
	}

	if stale := harness.put(floor, body, `"1"`); stale.Code != http.StatusPreconditionFailed ||
		stale.Header().Get("ETag") != `"2"` {
		t.Fatalf("replacing with the old tag = %d %v, want 412 with the current tag", stale.Code, stale.Header())
	}

	// A collection is not a change of its templates.
	if plc := harness.templates(builderAlice).Templates[0]; plc.ETag != `"1"` || len(plc.Collections) != 0 {
		t.Fatalf("the template left out of the collection = %+v", plc)
	}

	fixture.assertLogged(t, "added builder template collection", "changed builder template collection")
}

// TestBuilderTemplateDelete asserts templates and collections are deleted in
// one request, which is harmless to repeat.
func TestBuilderTemplateDelete(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness

	added := harness.post("/collections", `{"name":"Spares","templateIds":["`+fixture.hmi+`"]}`)

	var spares builderTemplateCollectionResponse

	harness.decode(added, &spares)

	selection := `{"templates":["` + fixture.plc + `","no-such"],"collections":["` + spares.ID + `"]}`

	deleted := harness.post("/delete", selection)
	if got := strings.TrimSpace(deleted.Body.String()); deleted.Code != http.StatusOK ||
		got != `{"deleted":{"templates":1,"collections":1}}` {
		t.Fatalf("deleting = %d %s", deleted.Code, got)
	}

	list := harness.templates(builderAlice)

	if got := builderTemplateIDs(list); !slices.Equal(got, []string{fixture.hmi}) {
		t.Fatalf("after the delete the listing holds %q, want %q", got, []string{fixture.hmi})
	}

	// The collection that held the deleted template lost it, and its tag
	// moved on. The deleted collection's template stays.
	if len(list.Collections) != 1 || list.Collections[0].ID != fixture.floor || list.Collections[0].ETag != `"2"` ||
		!slices.Equal(list.Collections[0].TemplateIDs, []string{fixture.hmi}) {
		t.Fatalf("after the delete the collections = %+v", list.Collections)
	}

	// The same delete again is harmless.
	if again := harness.post("/delete", selection); again.Code != http.StatusOK ||
		strings.TrimSpace(again.Body.String()) != `{"deleted":{"templates":0,"collections":0}}` {
		t.Fatalf("deleting again = %d %s", again.Code, again.Body)
	}

	// Deleting its last template leaves an empty collection, as a list.
	emptied := harness.post("/delete", `{"templates":["`+fixture.hmi+`"]}`)
	if emptied.Code != http.StatusOK {
		t.Fatalf("deleting the last template = %d %s", emptied.Code, emptied.Body)
	}

	if list := harness.templates(builderAlice); len(list.Collections) != 1 || list.Collections[0].TemplateIDs == nil ||
		len(list.Collections[0].TemplateIDs) != 0 || list.Collections[0].ETag != `"3"` {
		t.Fatalf("after deleting its last template the collection = %+v", list.Collections)
	}

	fixture.assertLogged(t, "deleted builder templates")
}

// TestBuilderTemplateReadOnly refuses each change of a built-in template
// with 409 and a code.
func TestBuilderTemplateReadOnly(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness

	const message = `template router ("Router") is built in and cannot be changed. Duplicate it into the library to change it`

	for _, request := range []builderRequest{
		{
			method: http.MethodPut, path: builderLibraryPath("/items/router"),
			body: builderJSON(t, builderTemplateContentOf("Edge", "")), ifMatch: `"1"`,
		},
		{method: http.MethodPost, path: builderLibraryPath("/delete"), body: `{"templates":["` + fixture.plc + `","router"]}`},
		{method: http.MethodPost, path: builderLibraryPath("/collections"), body: `{"name":"X","templateIds":["router"]}`},
		{
			method: http.MethodPut, path: builderLibraryPath("/collections/" + fixture.floor),
			body: `{"name":"X","templateIds":["router"]}`, ifMatch: `"1"`,
		},
	} {
		recorder := harness.library(request.method, request.path, builderAlice, request.body, request.ifMatch)

		if recorder.Code != http.StatusConflict || builderRefusal(t, recorder).Message != message ||
			builderRefusal(t, recorder).Code != string(bdoc.CodeTemplateItemReadOnly) {
			t.Errorf("%s %s = %d %s, want 409 %q", request.method, request.path, recorder.Code, recorder.Body, message)
		}
	}

	list := harness.templates(builderAlice)

	if got := builderTemplateIDs(list); !slices.Equal(got, []string{fixture.plc, fixture.hmi}) || len(list.Builtins) != 5 ||
		list.Collections[0].ETag != `"1"` {
		t.Fatalf("after the refusals the listing = %+v", list)
	}
}

// builderTemplatePermissionRequests returns a harness whose library holds a
// template and a collection of bob's, and a request of each template route.
func builderTemplatePermissionRequests(t *testing.T) (*builderHarness, []builderRequest) {
	t.Helper()

	harness := newBuilderHarness(t)

	var template, collection string

	_, err := harness.service.UpdateLibrary(context.Background(), builderBob,
		func(library *bapi.TemplateLibrary) error {
			ids, err := library.AddTemplates(
				[]bdoc.Template{{ID: "", Name: "Of bob", Description: "", Device: builderTemplateContentOf("Of bob", "").Device}},
				harness.service.NewID,
			)
			if err != nil {
				return err
			}

			template = ids[0]
			collection, err = library.AddCollection(
				bapi.CollectionContent{Name: "Floor", Description: "", TemplateIDs: nil}, harness.service.NewID,
			)

			return err
		})
	if err != nil {
		t.Fatalf("adding the template and the collection: %v", err)
	}

	item := builderJSON(t, builderTemplateContentOf("Changed", ""))
	items := builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("New", "")}, Collection: nil,
	})

	return harness, []builderRequest{
		{method: http.MethodGet, path: builderTemplatesRoute},
		{method: http.MethodPost, path: builderLibraryPath("/items"), body: items},
		{method: http.MethodPut, path: builderLibraryPath("/items/" + template), body: item, ifMatch: `"1"`},
		{method: http.MethodPost, path: builderLibraryPath("/collections"), body: `{"name":"More"}`},
		{
			method: http.MethodPut, path: builderLibraryPath("/collections/" + collection),
			body: `{"name":"Floor two"}`, ifMatch: `"1"`,
		},
		{method: http.MethodPost, path: builderLibraryPath("/delete"), body: `{"templates":["` + template + `"]}`},
	}
}

// builderTemplateRefusal is one request of alice's that is refused, and what
// it is answered with.
type builderTemplateRefusal struct {
	name    string
	method  string
	path    string
	body    string
	ifMatch string
	status  int
	message string
}

// builderTemplateRefusals returns the refused requests of a library that
// holds template and collection, both at version 1.
func builderTemplateRefusals(t *testing.T, template, collection string) []builderTemplateRefusal {
	t.Helper()

	var (
		items       = builderLibraryPath("/items")
		item        = items + "/" + template
		collections = builderLibraryPath("/collections")
		one         = collections + "/" + collection
		remove      = builderLibraryPath("/delete")
		valid       = builderJSON(t, builderTemplateContentOf("Fine", ""))
		wrap        = func(templates string) string { return `{"templates":[` + templates + `]}` }
	)

	const (
		malformed = "request body is not a valid Builder request"
		post      = http.MethodPost
		put       = http.MethodPut
	)

	noName := builderTemplateContentOf("", "")

	return []builderTemplateRefusal{
		// Malformed requests.
		{name: "items: not JSON", method: post, path: items, body: `{"templates":`, status: 400, message: malformed},
		{
			name:    "items: an unknown field",
			method:  post,
			path:    items,
			body:    `{"templates":[],"extra":1}`,
			status:  400,
			message: malformed,
		},
		{
			name: "items: an id of the caller's choosing", method: post, path: items,
			body: wrap(`{"id":"server","name":"X","device":{"spec":{}}}`), status: 400, message: malformed,
		},
		{
			name: "items: two values", method: post, path: items, body: wrap(valid) + ` {}`, status: 400,
			message: "request body carries more than one JSON value",
		},
		{
			name:    "items: no template",
			method:  post,
			path:    items,
			body:    `{"templates":[]}`,
			status:  400,
			message: "at least one template is required",
		},
		{name: "items: nothing", method: post, path: items, body: `{}`, status: 400, message: "at least one template is required"},
		{
			name:    "item: an unknown field",
			method:  put,
			path:    item,
			body:    `{"name":"X","version":3}`,
			ifMatch: `"1"`,
			status:  400,
			message: malformed,
		},
		{
			name: "item: no If-Match", method: put, path: item, body: valid, status: 400,
			message: "an If-Match header is required for this request",
		},
		{
			name: "item: a weak tag", method: put, path: item, body: valid, ifMatch: `W/"1"`, status: 400,
			message: "the If-Match header must be a single quoted entity tag",
		},
		{
			name: "item: a wildcard", method: put, path: item, body: valid, ifMatch: `*`, status: 400,
			message: "the If-Match header must be a single quoted entity tag",
		},
		{
			name:    "collection: an unknown field",
			method:  post,
			path:    collections,
			body:    `{"name":"X","templates":[]}`,
			status:  400,
			message: malformed,
		},
		{
			name: "collection: no If-Match", method: put, path: one, body: `{"name":"X"}`, status: 400,
			message: "an If-Match header is required for this request",
		},
		{
			name: "delete: nothing named", method: post, path: remove, body: `{"templates":[],"collections":[]}`, status: 400,
			message: "at least one template or collection is required",
		},
		{name: "delete: an unknown field", method: post, path: remove, body: `{"ids":["server"]}`, status: 400, message: malformed},

		// Not found: an id the library does not hold, or that is none.
		{
			name: "item: unknown", method: put, path: items + "/no-such", body: valid, ifMatch: `"1"`, status: 404,
			message: "template no-such not found",
		},
		{
			name: "item: not an id", method: put, path: items + "/a%20b", body: valid, ifMatch: `"1"`, status: 404,
			message: "template a b not found",
		},
		{
			name: "collection: unknown", method: put, path: collections + "/no-such", body: `{"name":"X"}`, ifMatch: `"1"`, status: 404,
			message: "collection no-such not found",
		},

		// A stale entity tag.
		{
			name: "item: a stale tag", method: put, path: item, body: valid, ifMatch: `"7"`, status: 412,
			message: "template " + template + " changed after you read it. Reload the template library and try again",
		},
		{
			name: "collection: a stale tag", method: put, path: one, body: `{"name":"X"}`, ifMatch: `"7"`, status: 412,
			message: "collection " + collection + " changed after you read it. Reload the template library and try again",
		},

		// Well formed and refused: the service tests hold the other refusals.
		{
			name: "items: no name", method: post, path: items, body: wrap(valid + "," + builderJSON(t, noName)), status: 422,
			message: "templates[1].name: template name is required",
		},
		{
			// The icon library holds the icons: a request carries none.
			name: "items: icons carried", method: post, path: items,
			body:    `{"templates":[` + valid + `],"icons":{"plc":{"data":"bm90IGEgUE5H"}}}`,
			status:  400,
			message: malformed,
		},
		{
			// Only a document's template names a source.
			name: "items: a source", method: post, path: items,
			body:    wrap(`{"name":"X","device":{"spec":{}},"source":{"kind":"builtin","id":"router","version":1}}`),
			status:  400,
			message: malformed,
		},
		{
			name: "items: a collection with no name", method: post, path: items,
			body: `{"templates":[` + valid + `],"collection":{"name":""}}`, status: 422, message: "collection name is required",
		},
		{name: "collection: no name", method: post, path: collections, body: `{}`, status: 422, message: "collection name is required"},

		// Too large: a body, or what the library would hold.
		{
			name: "item: a body over 1 MiB", method: put, path: item, ifMatch: `"1"`,
			body:    `{"name":"` + strings.Repeat("n", builderTemplateItemBytes) + `"}`,
			status:  413,
			message: "request body is larger than 1048576 bytes",
		},
		{
			name: "collection: a body over 64 KiB", method: post, path: collections,
			body:    `{"name":"` + strings.Repeat("n", builderTemplateRequestBytes) + `"}`,
			status:  413,
			message: "request body is larger than 65536 bytes",
		},
		{
			name: "collection: replaced with a body over 64 KiB", method: put, path: one, ifMatch: `"1"`,
			body:    `{"name":"` + strings.Repeat("n", builderTemplateRequestBytes) + `"}`,
			status:  413,
			message: "request body is larger than 65536 bytes",
		},
		{
			name: "delete: a body over 64 KiB", method: post, path: remove,
			body:    `{"templates":["` + strings.Repeat("n", builderTemplateRequestBytes) + `"]}`,
			status:  413,
			message: "request body is larger than 65536 bytes",
		},
		{
			name: "items: more templates than a library holds", method: post, path: items,
			body:    wrap(strings.TrimSuffix(strings.Repeat(valid+",", bapi.MaxLibraryTemplates), ",")),
			status:  413,
			message: "a library holds at most 200 templates",
		},
	}
}

// TestBuilderTemplateRequests asserts what each refused request is answered
// with: the status, and the words.
func TestBuilderTemplateRequests(t *testing.T) {
	harness := newBuilderHarness(t)

	made := harness.addTemplates("PLC")

	var collection string

	if _, err := harness.service.UpdateLibrary(context.Background(), builderAlice,
		func(library *bapi.TemplateLibrary) error {
			var err error

			collection, err = library.AddCollection(
				bapi.CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{made[0].ID}}, harness.service.NewID,
			)

			return err
		}); err != nil {
		t.Fatalf("adding the collection: %v", err)
	}

	before, err := harness.store.GetRecord(bapi.NamespaceTemplates, bapi.LibraryKey)
	if err != nil {
		t.Fatalf("reading the library record: %v", err)
	}

	for _, tt := range builderTemplateRefusals(t, made[0].ID, collection) {
		t.Run(tt.name, func(t *testing.T) {
			logs := plogtest.Capture(t)
			recorder := harness.library(tt.method, tt.path, builderAlice, tt.body, tt.ifMatch)

			if recorder.Code != tt.status || builderRefusal(t, recorder).Message != tt.message {
				t.Fatalf("answered %d %.300s, want %d %q", recorder.Code, recorder.Body, tt.status, tt.message)
			}

			// A stale tag is answered with the current one; nothing else is.
			if got, want := recorder.Header().Get("ETag"), map[bool]string{true: `"1"`, false: ""}[tt.status == 412]; got != want {
				t.Errorf("ETag = %q, want %q", got, want)
			}

			if strings.Contains(recorder.Body.String(), builderTemplateSecret) || strings.Contains(logs.String(), builderTemplateSecret) {
				t.Errorf("the answer or the log repeats a template: %.300s", recorder.Body)
			}
		})
	}

	after, err := harness.store.GetRecord(bapi.NamespaceTemplates, bapi.LibraryKey)
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("a refused request wrote the library: revision %d, was %d (%v)", after.Revision, before.Revision, err)
	}
}

// TestBuilderTemplateBodyLimits accepts a request to add templates as large
// as a Builder request may be.
func TestBuilderTemplateBodyLimits(t *testing.T) {
	harness := newBuilderHarness(t)

	// White space is a large request and a small write.
	large := strings.Replace(builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("Carried", "")}, Collection: nil,
	}), "{", "{"+strings.Repeat(" ", 2<<20), 1)

	if recorder := harness.post("/items", large); recorder.Code != http.StatusCreated {
		t.Fatalf("adding a template with a %d byte request = %d %.200s", len(large), recorder.Code, recorder.Body)
	}
}

// builderCountingStore records the store calls of one namespace.
type builderCountingStore struct {
	*memrecord.Store

	namespace string
	calls     []string
}

func (s *builderCountingStore) note(call, namespace, key string) {
	if namespace == s.namespace {
		s.calls = append(s.calls, call+" "+key)
	}
}

func (s *builderCountingStore) ListRecords(namespace, prefix string) (store.Records, error) {
	s.note("ListRecords", namespace, prefix)

	return s.Store.ListRecords(namespace, prefix) //nolint:wrapcheck // the wrapped store's answer
}

func (s *builderCountingStore) ListRecordKeys(namespace, prefix string) ([]string, error) {
	s.note("ListRecordKeys", namespace, prefix)

	return s.Store.ListRecordKeys(namespace, prefix) //nolint:wrapcheck // the wrapped store's answer
}

func (s *builderCountingStore) GetRecord(namespace, key string) (store.Record, error) {
	s.note("GetRecord", namespace, key)

	return s.Store.GetRecord(namespace, key) //nolint:wrapcheck // the wrapped store's answer
}

func (s *builderCountingStore) CreateRecord(namespace, key string, value []byte) (store.Record, error) {
	s.note("CreateRecord", namespace, key)

	return s.Store.CreateRecord(namespace, key, value) //nolint:wrapcheck // the wrapped store's answer
}

func (s *builderCountingStore) UpdateRecord(namespace, key string, value []byte, revision int64) (store.Record, error) {
	s.note("UpdateRecord", namespace, key)

	return s.Store.UpdateRecord(namespace, key, value, revision) //nolint:wrapcheck // the wrapped store's answer
}

func (s *builderCountingStore) DeleteRecord(namespace, key string, revision int64) error {
	s.note("DeleteRecord", namespace, key)

	return s.Store.DeleteRecord(namespace, key, revision) //nolint:wrapcheck // the wrapped store's answer
}

func (s *builderCountingStore) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	s.note("DeleteRecordPrefix", namespace, prefix)

	return s.Store.DeleteRecordPrefix(namespace, prefix) //nolint:wrapcheck // the wrapped store's answer
}

// TestBuilderTemplateListingReadsOneRecord asserts a listing reads only the
// library record, and a change reads and writes only that record.
func TestBuilderTemplateListingReadsOneRecord(t *testing.T) {
	counting := &builderCountingStore{Store: memrecord.New(), namespace: bapi.NamespaceTemplates, calls: nil}
	ids := new(atomic.Int64)

	service, err := bapi.New(
		bapi.WithStore(counting),
		bapi.WithClock(func() time.Time { return memrecord.Time(ids.Add(1)) }),
		bapi.WithIDSource(func() (string, error) { return "id-" + strconv.FormatInt(ids.Add(1), 10), nil }),
	)
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	harness := newBuilderHarnessWith(t, []builderOption{withBuilderService(service)})
	harness.service, harness.store = service, counting.Store

	if len(counting.calls) != 0 {
		t.Fatalf("registering the routes made the calls %q", counting.calls)
	}

	harness.addTemplates("PLC", "HMI")

	for _, user := range []string{builderAlice, builderBob} {
		counting.calls = nil

		if names := builderTemplateIDs(harness.templates(user)); len(names) != 2 {
			t.Fatalf("%s's listing holds %q, want both templates", user, names)
		}

		if want := []string{"GetRecord " + bapi.LibraryKey}; !slices.Equal(counting.calls, want) {
			t.Fatalf("%s's listing made the calls %q, want %q", user, counting.calls, want)
		}
	}

	counting.calls = nil
	harness.addTemplates("RTU")

	if want := []string{"GetRecord " + bapi.LibraryKey, "UpdateRecord " + bapi.LibraryKey}; !slices.Equal(counting.calls, want) {
		t.Fatalf("a change made the calls %q, want %q", counting.calls, want)
	}
}

// TestBuilderTemplateDamagedLibrary lists an unreadable library as damaged
// and empty, and refuses every change of it.
func TestBuilderTemplateDamagedLibrary(t *testing.T) {
	harness := newBuilderHarness(t)
	made := harness.addTemplates("PLC")
	key := bapi.LibraryKey

	record, err := harness.store.GetRecord(bapi.NamespaceTemplates, key)
	if err != nil {
		t.Fatalf("reading the library record: %v", err)
	}

	// A field this version does not know.
	value := strings.Replace(string(record.Value), `{"templates"`, `{"layout":"`+builderTemplateSecret+`","templates"`, 1)
	harness.store.SetValue(bapi.NamespaceTemplates, key, []byte(value))

	logs := plogtest.Capture(t)
	list := harness.templates(builderAlice)

	if !list.Damaged || len(list.Templates) != 0 || list.Templates == nil || len(list.Builtins) != len(builderBuiltinIDs) ||
		len(list.Collections) != 0 || list.Collections == nil || list.Limits.Templates != bapi.MaxLibraryTemplates {
		t.Fatalf("the listing of a damaged library = %+v", list)
	}

	if len(logs.Records(t, plogtest.Message("builder template library is unreadable"))) != 1 {
		t.Errorf("the damaged library is not logged: %s", logs)
	}

	const message = "this version of phenix cannot read the template library"

	item := builderJSON(t, builderTemplateContentOf("Changed", ""))
	items := builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("New", "")}, Collection: nil,
	})

	for _, request := range []builderRequest{
		{method: http.MethodPost, path: builderLibraryPath("/items"), body: items},
		{method: http.MethodPut, path: builderLibraryPath("/items/" + made[0].ID), body: item, ifMatch: `"1"`},
		{method: http.MethodPost, path: builderLibraryPath("/collections"), body: `{"name":"X"}`},
		{method: http.MethodPut, path: builderLibraryPath("/collections/x"), body: `{"name":"X"}`, ifMatch: `"1"`},
		{method: http.MethodPost, path: builderLibraryPath("/delete"), body: `{"templates":["` + made[0].ID + `"]}`},
	} {
		recorder := harness.library(request.method, request.path, builderAlice, request.body, request.ifMatch)

		if recorder.Code != http.StatusConflict || builderRefusal(t, recorder).Message != message {
			t.Errorf("%s %s on a damaged library = %d %s, want 409 %q", request.method, request.path, recorder.Code, recorder.Body, message)
		}

		if strings.Contains(recorder.Body.String(), builderTemplateSecret) {
			t.Errorf("%s %s repeats what the record holds: %s", request.method, request.path, recorder.Body)
		}
	}

	if after, err := harness.store.GetRecord(bapi.NamespaceTemplates, key); err != nil || string(after.Value) != value {
		t.Fatalf("a damaged library was written: %v", err)
	}
}

// TestBuilderTemplateBusy asserts a library that keeps changing under a
// write is answered 503, with when to try again.
func TestBuilderTemplateBusy(t *testing.T) {
	harness := newBuilderHarness(t)
	made := harness.addTemplates("PLC")
	selection := `{"templates":["` + made[0].ID + `"]}`

	racing := false
	round := 0

	// Another request lands before every write of this one.
	harness.store.BeforeUpdate = func(string, string) error {
		if racing {
			return nil
		}

		racing = true
		defer func() { racing = false }()

		round++

		_, err := harness.service.UpdateLibrary(context.Background(), builderBob,
			func(library *bapi.TemplateLibrary) error {
				_, err := library.AddCollection(
					bapi.CollectionContent{Name: "Racer " + strconv.Itoa(round), Description: "", TemplateIDs: nil}, harness.service.NewID,
				)

				return err
			})

		return err
	}

	recorder := harness.post("/delete", selection)

	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != "1" ||
		builderRefusal(t, recorder).Message != "unable to delete the templates" || round != 5 {
		t.Fatalf(
			"a busy library = %d %v %s after %d rounds, want 503 with Retry-After: 1 after 5",
			recorder.Code,
			recorder.Header(),
			recorder.Body,
			round,
		)
	}

	harness.store.BeforeUpdate = nil

	// Nothing of the request was applied, and it works when tried again.
	if got := builderTemplateIDs(harness.templates(builderAlice)); !slices.Contains(got, made[0].ID) {
		t.Fatalf("a request that gave up deleted the template: %q", got)
	}

	if again := harness.post("/delete", selection); again.Code != http.StatusOK {
		t.Fatalf("trying again = %d %s", again.Code, again.Body)
	}
}

// TestBuilderTemplateRacingReplace refuses a replacement whose entity tag
// went stale between its read and its write.
func TestBuilderTemplateRacingReplace(t *testing.T) {
	harness := newBuilderHarness(t)
	made := harness.addTemplates("PLC")
	path := "/items/" + made[0].ID

	harness.store.BeforeUpdate = func(string, string) error {
		harness.store.BeforeUpdate = nil

		winner := harness.put(path, builderJSON(t, builderTemplateContentOf("Winner", "")), `"1"`)
		if winner.Code != http.StatusOK {
			t.Errorf("the racing replacement = %d %s", winner.Code, winner.Body)
		}

		return nil
	}

	loser := harness.put(path, builderJSON(t, builderTemplateContentOf("Loser", "")), `"1"`)

	if loser.Code != http.StatusPreconditionFailed || loser.Header().Get("ETag") != `"2"` {
		t.Fatalf("the replacement that lost = %d %v %s, want 412 with the current tag", loser.Code, loser.Header(), loser.Body)
	}

	if list := harness.templates(builderAlice); list.Templates[0].Name != "Winner" {
		t.Fatalf("the template = %+v, want the winner's", list.Templates[0])
	}
}
