package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

	if err := builderJSONError(recorder); err != nil {
		h.t.Errorf("%s %s: %v", method, path, err)
	}

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

// builderETag returns the entity tag of an item at version.
func builderETag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// builderLibraryTemplate returns how the listing gives the template alice
// added from builderTemplateContentOf(name, icon), at version.
func builderLibraryTemplate(id, name, icon string, version int64, collections ...string) builderTemplateResponse {
	if collections == nil {
		collections = []string{}
	}

	return builderTemplateResponse{
		ID: id, Source: builderTemplateLibrary, Name: name, Description: "made by a test",
		Device: builderTemplateContentOf(name, icon).Device, Version: version, ETag: builderETag(version),
		CreatedBy: builderAlice, UpdatedBy: builderAlice, Collections: collections,
	}
}

// builderLibraryCollection returns how the listing gives the collection alice
// added, at version.
func builderLibraryCollection(
	id, name, description string, version int64, templateIDs ...string,
) builderTemplateCollectionResponse {
	if templateIDs == nil {
		templateIDs = []string{}
	}

	return builderTemplateCollectionResponse{
		ID: id, Source: builderTemplateLibrary, Name: name, Description: description, TemplateIDs: templateIDs,
		Version: version, ETag: builderETag(version), CreatedBy: builderAlice, UpdatedBy: builderAlice,
	}
}

// ignoreBuilderLibraryTimes ignores the times of library items, which the
// tests check apart.
func ignoreBuilderLibraryTimes() cmp.Options {
	return cmp.Options{
		cmpopts.IgnoreFields(builderTemplateResponse{}, "Created", "Updated"),
		cmpopts.IgnoreFields(builderTemplateCollectionResponse{}, "Created", "Updated"),
	}
}

// builderTag is the status and the entity tag of an answer.
type builderTag struct {
	Status int
	ETag   string
}

// builderTagOf returns the status and the entity tag of recorder.
func builderTagOf(recorder *httptest.ResponseRecorder) builderTag {
	return builderTag{Status: recorder.Code, ETag: recorder.Header().Get("ETag")}
}

// builderDeleteCounts returns the decoded answer of a delete of templates
// templates and collections collections.
func builderDeleteCounts(templates, collections int) any {
	return map[string]any{"deleted": map[string]any{"templates": float64(templates), "collections": float64(collections)}}
}

// TestBuilderTemplateLibraryStartsEmpty asserts the whole listing of a new
// library: empty, the five built-in templates apart, and nothing written.
func TestBuilderTemplateLibraryStartsEmpty(t *testing.T) {
	harness := newBuilderHarness(t)

	recorder := harness.library(http.MethodGet, builderTemplatesRoute, builderAlice, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want %d: %s", builderTemplatesRoute, recorder.Code, http.StatusOK, recorder.Body)
	}

	var listing map[string]any

	harness.decode(recorder, &listing)

	// The icon library holds the icons: a template listing carries none.
	want := map[string]any{
		"templates": []any{}, "collections": []any{}, "preloaded": []any{}, "damaged": false,
		"limits": map[string]any{
			"templates": 200.0, "collections": 50.0, "nameBytes": 128.0, "descriptionBytes": 1024.0, "deviceBytes": 16384.0,
		},
	}
	ignoreBuiltins := cmpopts.IgnoreMapEntries(func(key string, _ any) bool { return key == "builtins" })

	if diff := cmp.Diff(want, listing, ignoreBuiltins); diff != "" {
		t.Errorf("GET %s mismatch (-want +got):\n%s", builderTemplatesRoute, diff)
	}

	// A built-in template has no times and no users, and every list is a
	// list.
	var first any

	if err := json.Unmarshal([]byte(`{"id":"server","source":"builtin","name":"Server",`+
		`"description":"Generic Linux server","device":{"iconKey":"server","spec":{"general":{"description":"",`+
		`"hostname":"server","vm_type":"kvm"},"hardware":{"drives":[{"image":"ubuntu.qc2"}],"os_type":"linux"},`+
		`"network":{"interfaces":[]},"type":"VirtualMachine"}},"version":1,"etag":"\"1\"","collections":[]}`), &first); err != nil {
		t.Fatalf("decoding the first built-in template: %v", err)
	}

	if builtins, _ := listing["builtins"].([]any); len(builtins) != len(builderBuiltinIDs) {
		t.Errorf(
			"GET %s: builtins = %v, want the %d built-in templates",
			builderTemplatesRoute,
			listing["builtins"],
			len(builderBuiltinIDs),
		)
	} else if diff := cmp.Diff(first, builtins[0]); diff != "" {
		t.Errorf("GET %s: first built-in template mismatch (-want +got):\n%s", builderTemplatesRoute, diff)
	}

	expected := bdoc.BuiltinTemplates()
	builtins := make([]builderTemplateResponse, 0, len(builderBuiltinIDs))

	for i, id := range builderBuiltinIDs {
		builtins = append(builtins, builderTemplateResponse{
			ID: id, Source: builderTemplateBuiltin, Name: expected[i].Name, Description: expected[i].Description,
			Version: 1, ETag: `"1"`, Collections: []string{},
		})
	}

	// The device spec decodes to other types than the built-in one holds.
	if diff := cmp.Diff(builtins, harness.templates(builderBob).Builtins,
		cmpopts.IgnoreFields(builderTemplateResponse{}, "Device")); diff != "" {
		t.Errorf("templates(bob).Builtins mismatch (-want +got):\n%s", diff)
	}

	if got := harness.store.Count(bapi.NamespaceTemplates); got != 0 {
		t.Errorf("listing wrote %d library records, want 0", got)
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

// logProblems returns what the log lacks of messages, and whether it holds
// anything a template or a collection holds, or nil.
func (f *builderTemplateFixture) logProblems(t *testing.T, messages ...string) error {
	t.Helper()

	var problems []error

	for _, message := range messages {
		if len(f.logs.Records(t, plogtest.Message(message))) == 0 {
			problems = append(problems, fmt.Errorf("the log does not say %q", message))
		}
	}

	if text := f.logs.String(); strings.Contains(text, builderTemplateSecret) || strings.Contains(text, "Plant floor") {
		problems = append(problems, fmt.Errorf("the log holds what a template or a collection holds: %s", text))
	}

	return errors.Join(problems...)
}

// TestBuilderTemplateCreate adds templates and their collection in one
// request, and lists them with their custom icons.
func TestBuilderTemplateCreate(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	list := fixture.harness.templates(builderAlice)

	// The new templates are in the order sent.
	want := []builderTemplateResponse{
		builderLibraryTemplate(fixture.plc, "PLC", builderTemplateIconName, 1, fixture.floor),
		builderLibraryTemplate(fixture.hmi, "HMI", "", 1, fixture.floor),
	}

	if diff := cmp.Diff(want, list.Templates, ignoreBuilderLibraryTimes()); diff != "" {
		t.Errorf("templates(alice).Templates mismatch (-want +got):\n%s", diff)
	}

	for _, template := range list.Templates {
		if template.Created.IsZero() || !template.Created.Equal(template.Updated) {
			t.Errorf("template %s: created %v, updated %v, want a time it was created and no later update",
				template.Name, template.Created, template.Updated)
		}
	}

	collections := []builderTemplateCollectionResponse{
		builderLibraryCollection(fixture.floor, "Plant floor", "line 1", 1, fixture.plc, fixture.hmi),
	}

	if diff := cmp.Diff(collections, list.Collections, ignoreBuilderLibraryTimes()); diff != "" {
		t.Errorf("templates(alice).Collections mismatch (-want +got):\n%s", diff)
	}

	for _, collection := range list.Collections {
		if collection.Created.IsZero() {
			t.Errorf("collection %s: created = zero, want the time it was created", collection.Name)
		}
	}

	if err := fixture.logProblems(t, "added builder templates"); err != nil {
		t.Errorf("log after adding templates: %v", err)
	}
}

// TestBuilderTemplateReplace asserts replacing a template needs the entity
// tag of its content, which moves on only when the content changes.
func TestBuilderTemplateReplace(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness

	content := builderJSON(t, builderTemplateUpdateRequest{builderTemplateContent: builderTemplateContentOf("PLC two", "")})
	path := "/items/" + fixture.plc

	replaced := harness.put(path, content, `"1"`)
	if replaced.Code != http.StatusOK {
		t.Fatalf("PUT %s: status = %d, want %d: %s", path, replaced.Code, http.StatusOK, replaced.Body)
	}

	if got := replaced.Header().Get("ETag"); got != `"2"` {
		t.Errorf("PUT %s: ETag = %s, want %s", path, got, `"2"`)
	}

	var template builderTemplateResponse

	harness.decode(replaced, &template)

	if diff := cmp.Diff(builderLibraryTemplate(fixture.plc, "PLC two", "", 2, fixture.floor), template,
		ignoreBuilderLibraryTimes()); diff != "" {
		t.Errorf("PUT %s mismatch (-want +got):\n%s", path, diff)
	}

	if !template.Updated.After(template.Created) {
		t.Errorf("PUT %s: updated %v, want after created %v", path, template.Updated, template.Created)
	}

	// The same content again changes nothing and keeps the tag.
	if got, want := builderTagOf(harness.put(path, content, `"2"`)), (builderTag{http.StatusOK, `"2"`}); got != want {
		t.Errorf("PUT %s with the same content = %+v, want %+v", path, got, want)
	}

	// The tag that was replaced no longer is the content's.
	stale := harness.put(path, content, `"1"`)
	if got, want := builderTagOf(stale), (builderTag{http.StatusPreconditionFailed, `"2"`}); got != want {
		t.Errorf("PUT %s with the old tag = %+v, want %+v: %s", path, got, want, stale.Body)
	}

	if got := builderRefusal(t, stale).Code; got != string(bdoc.CodeTemplateItemStale) {
		t.Errorf("PUT %s with the old tag: code = %q, want %q", path, got, bdoc.CodeTemplateItemStale)
	}

	// A replacement may name any custom icon, which the icon library
	// resolves.
	drawn := builderJSON(t, builderTemplateUpdateRequest{
		builderTemplateContent: builderTemplateContentOf("PLC three", "other-icon"),
	})

	back := harness.put(path, drawn, `"2"`)
	if got, want := builderTagOf(back), (builderTag{http.StatusOK, `"3"`}); got != want {
		t.Fatalf("PUT %s with an icon = %+v, want %+v: %s", path, got, want, back.Body)
	}

	var named builderTemplateResponse

	harness.decode(back, &named)

	if diff := cmp.Diff(builderLibraryTemplate(fixture.plc, "PLC three", "other-icon", 3, fixture.floor), named,
		ignoreBuilderLibraryTimes()); diff != "" {
		t.Errorf("PUT %s with an icon mismatch (-want +got):\n%s", path, diff)
	}

	if err := fixture.logProblems(t, "changed builder template"); err != nil {
		t.Errorf("log after replacing a template: %v", err)
	}
}

// TestBuilderTemplateCollections asserts a collection is added and replaced
// like a template, under an entity tag of its own.
func TestBuilderTemplateCollections(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness
	floor := "/collections/" + fixture.floor

	added := harness.post("/collections", `{"name":"Spares","description":"","templateIds":["`+fixture.hmi+`"]}`)
	if added.Code != http.StatusCreated {
		t.Fatalf("POST /collections: status = %d, want %d: %s", added.Code, http.StatusCreated, added.Body)
	}

	if got := added.Header().Get("ETag"); got != `"1"` {
		t.Errorf("POST /collections: ETag = %s, want %s", got, `"1"`)
	}

	var spares builderTemplateCollectionResponse

	harness.decode(added, &spares)

	if diff := cmp.Diff(builderLibraryCollection(spares.ID, "Spares", "", 1, fixture.hmi), spares,
		ignoreBuilderLibraryTimes()); diff != "" {
		t.Errorf("POST /collections mismatch (-want +got):\n%s", diff)
	}

	if spares.ID == fixture.floor {
		t.Errorf("POST /collections: ID = %q, the ID of Plant floor, want a new one", spares.ID)
	}

	// A template may be in several collections.
	list := harness.templates(builderAlice)

	if got, want := list.Templates[1].Collections, []string{fixture.floor, spares.ID}; !slices.Equal(got, want) {
		t.Errorf("templates(alice): HMI collections = %q, want %q", got, want)
	}

	body := `{"name":"Plant floor","description":"line 1","templateIds":["` + fixture.hmi + `"]}`
	changed := harness.put(floor, body, `"1"`)

	if changed.Code != http.StatusOK {
		t.Fatalf("PUT %s: status = %d, want %d: %s", floor, changed.Code, http.StatusOK, changed.Body)
	}

	if got := changed.Header().Get("ETag"); got != `"2"` {
		t.Errorf("PUT %s: ETag = %s, want %s", floor, got, `"2"`)
	}

	var collection builderTemplateCollectionResponse

	harness.decode(changed, &collection)

	if diff := cmp.Diff(builderLibraryCollection(fixture.floor, "Plant floor", "line 1", 2, fixture.hmi), collection,
		ignoreBuilderLibraryTimes()); diff != "" {
		t.Errorf("PUT %s mismatch (-want +got):\n%s", floor, diff)
	}

	if !collection.Updated.After(collection.Created) {
		t.Errorf("PUT %s: updated %v, want after created %v", floor, collection.Updated, collection.Created)
	}

	// The same content again changes nothing. The old tag is stale.
	if got, want := builderTagOf(harness.put(floor, body, `"2"`)), (builderTag{http.StatusOK, `"2"`}); got != want {
		t.Errorf("PUT %s with the same content = %+v, want %+v", floor, got, want)
	}

	if got, want := builderTagOf(harness.put(floor, body, `"1"`)), (builderTag{http.StatusPreconditionFailed, `"2"`}); got != want {
		t.Errorf("PUT %s with the old tag = %+v, want %+v", floor, got, want)
	}

	// A collection is not a change of its templates.
	if diff := cmp.Diff(builderLibraryTemplate(fixture.plc, "PLC", builderTemplateIconName, 1),
		harness.templates(builderAlice).Templates[0], ignoreBuilderLibraryTimes(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("templates(alice): the template left out of the collection mismatch (-want +got):\n%s", diff)
	}

	if err := fixture.logProblems(t, "added builder template collection", "changed builder template collection"); err != nil {
		t.Errorf("log after changing collections: %v", err)
	}
}

// builderTemplateRefs tags each of ids with etag.
func builderTemplateRefs(etag string, ids ...string) []builderTemplateRef {
	refs := make([]builderTemplateRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, builderTemplateRef{ID: id, ETag: etag})
	}

	return refs
}

// builderTemplateDeleteBody is the body of POST /builder/templates/delete.
func builderTemplateDeleteBody(t *testing.T, templates, collections []builderTemplateRef) string {
	t.Helper()

	return builderJSON(t, builderTemplateSelection{Templates: templates, Collections: collections})
}

// TestBuilderTemplateDelete asserts templates and collections are deleted in
// one request, which is harmless to repeat, and only at the tags the caller
// read.
func TestBuilderTemplateDelete(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness

	added := harness.post("/collections", `{"name":"Spares","templateIds":["`+fixture.hmi+`"]}`)

	var spares builderTemplateCollectionResponse

	harness.decode(added, &spares)

	selection := builderTemplateDeleteBody(t,
		builderTemplateRefs(`"1"`, fixture.plc, "no-such"), builderTemplateRefs(spares.ETag, spares.ID))

	// A tag of another version refuses the whole request with the current
	// tag, and deletes nothing.
	stale := builderTemplateDeleteBody(t,
		builderTemplateRefs(`"1"`, fixture.plc), builderTemplateRefs(`"7"`, spares.ID))

	if got, want := builderTagOf(harness.post("/delete", stale)), (builderTag{http.StatusPreconditionFailed, spares.ETag}); got != want {
		t.Errorf("POST /delete %s = %+v, want %+v", stale, got, want)
	}

	if code := builderRefusal(t, harness.post("/delete", stale)).Code; code != string(bdoc.CodeTemplateItemStale) {
		t.Errorf("POST /delete %s: code = %q, want %q", stale, code, bdoc.CodeTemplateItemStale)
	}

	if got := builderTemplateIDs(harness.templates(builderAlice)); !slices.Contains(got, fixture.plc) {
		t.Errorf("templates(alice) after a stale delete = %q, want it to hold %s", got, fixture.plc)
	}

	deleted := harness.post("/delete", selection)
	if deleted.Code != http.StatusOK {
		t.Fatalf("POST /delete %s: status = %d, want %d: %s", selection, deleted.Code, http.StatusOK, deleted.Body)
	}

	var counts any

	harness.decode(deleted, &counts)

	if diff := cmp.Diff(builderDeleteCounts(1, 1), counts); diff != "" {
		t.Errorf("POST /delete %s mismatch (-want +got):\n%s", selection, diff)
	}

	list := harness.templates(builderAlice)

	if got, want := builderTemplateIDs(list), []string{fixture.hmi}; !slices.Equal(got, want) {
		t.Errorf("templates(alice) after the delete holds %q, want %q", got, want)
	}

	// The collection that held the deleted template lost it, and its tag
	// moved on. The deleted collection's template stays.
	want := []builderTemplateCollectionResponse{builderLibraryCollection(fixture.floor, "Plant floor", "line 1", 2, fixture.hmi)}
	if diff := cmp.Diff(want, list.Collections, ignoreBuilderLibraryTimes()); diff != "" {
		t.Errorf("templates(alice).Collections after the delete mismatch (-want +got):\n%s", diff)
	}

	// The same delete again is harmless.
	if again := harness.post("/delete", selection); again.Code != http.StatusOK {
		t.Errorf("POST /delete %s again: status = %d, want %d: %s", selection, again.Code, http.StatusOK, again.Body)
	} else {
		harness.decode(again, &counts)

		if diff := cmp.Diff(builderDeleteCounts(0, 0), counts); diff != "" {
			t.Errorf("POST /delete %s again mismatch (-want +got):\n%s", selection, diff)
		}
	}

	// Deleting its last template leaves an empty collection, as a list.
	emptied := harness.post("/delete", builderTemplateDeleteBody(t, builderTemplateRefs(`"1"`, fixture.hmi), nil))
	if emptied.Code != http.StatusOK {
		t.Fatalf("POST /delete of HMI: status = %d, want %d: %s", emptied.Code, http.StatusOK, emptied.Body)
	}

	want = []builderTemplateCollectionResponse{builderLibraryCollection(fixture.floor, "Plant floor", "line 1", 3)}
	if diff := cmp.Diff(want, harness.templates(builderAlice).Collections, ignoreBuilderLibraryTimes()); diff != "" {
		t.Errorf("templates(alice).Collections after deleting the last template mismatch (-want +got):\n%s", diff)
	}

	if err := fixture.logProblems(t, "deleted builder templates"); err != nil {
		t.Errorf("log after deleting templates: %v", err)
	}
}

// TestBuilderTemplateReadOnly refuses each change of a built-in template
// with 409 and a code.
func TestBuilderTemplateReadOnly(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness

	const message = `template router ("Router") is built in and is read only. Duplicate it into the library to change it`

	for _, tt := range []struct {
		name    string
		request builderRequest
	}{
		{
			name: "replace router",
			request: builderRequest{
				method: http.MethodPut, path: builderLibraryPath("/items/router"),
				body: builderJSON(t, builderTemplateContentOf("Edge", "")), ifMatch: `"1"`,
			},
		},
		{
			name: "delete router with PLC",
			request: builderRequest{
				method: http.MethodPost, path: builderLibraryPath("/delete"),
				body: builderTemplateDeleteBody(t, builderTemplateRefs(`"1"`, fixture.plc, "router"), nil),
			},
		},
		{
			name: "add a collection of router",
			request: builderRequest{
				method: http.MethodPost, path: builderLibraryPath("/collections"), body: `{"name":"X","templateIds":["router"]}`,
			},
		},
		{
			name: "put router into a collection",
			request: builderRequest{
				method: http.MethodPut, path: builderLibraryPath("/collections/" + fixture.floor),
				body: `{"name":"X","templateIds":["router"]}`, ifMatch: `"1"`,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := tt.request
			recorder := harness.library(request.method, request.path, builderAlice, request.body, request.ifMatch)

			if recorder.Code != http.StatusConflict {
				t.Errorf("%s %s: status = %d, want %d: %s", request.method, request.path, recorder.Code, http.StatusConflict, recorder.Body)
			}

			want := builderErrorBody{Code: string(bdoc.CodeTemplateItemReadOnly), Message: message}
			if diff := cmp.Diff(want, builderRefusal(t, recorder),
				cmpopts.IgnoreFields(builderErrorBody{}, "Cause", "Metadata", "Issues")); diff != "" {
				t.Errorf("%s %s: refusal mismatch (-want +got):\n%s", request.method, request.path, diff)
			}
		})
	}

	list := harness.templates(builderAlice)

	if got, want := builderTemplateIDs(list), []string{fixture.plc, fixture.hmi}; !slices.Equal(got, want) {
		t.Errorf("templates(alice) after the refusals holds %q, want %q", got, want)
	}

	if len(list.Builtins) != len(builderBuiltinIDs) {
		t.Errorf("templates(alice) after the refusals holds %d built-in templates, want %d", len(list.Builtins), len(builderBuiltinIDs))
	}

	if len(list.Collections) == 0 || list.Collections[0].ETag != `"1"` {
		t.Errorf("templates(alice).Collections after the refusals = %+v, want Plant floor at %s", list.Collections, `"1"`)
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
		{
			method: http.MethodPost, path: builderLibraryPath("/delete"),
			body: `{"templates":[{"id":"` + template + `","etag":"\"2\""}]}`,
		},
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
			name: "items: an id the caller chooses", method: post, path: items,
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
			message: "the request needs at least one template",
		},
		{name: "items: nothing", method: post, path: items, body: `{}`, status: 400, message: "the request needs at least one template"},
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
			message: "this request needs an If-Match header",
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
			message: "this request needs an If-Match header",
		},
		{
			name: "delete: nothing named", method: post, path: remove, body: `{"templates":[],"collections":[]}`, status: 400,
			message: "the request needs at least one template or collection",
		},
		{name: "delete: an unknown field", method: post, path: remove, body: `{"ids":["server"]}`, status: 400, message: malformed},
		{
			name: "delete: an id alone", method: post, path: remove, body: `{"templates":["` + template + `"]}`,
			status: 400, message: malformed,
		},
		{
			name: "delete: no etag", method: post, path: remove, body: `{"collections":[{"id":"` + collection + `"}]}`,
			status: 400, message: "each template and collection to delete needs its etag",
		},
		{
			name: "delete: a weak etag", method: post, path: remove,
			body:   `{"templates":[{"id":"` + template + `","etag":"W/\"1\""}]}`,
			status: 400, message: "each template and collection to delete needs its etag",
		},

		// Not found: an id the library does not hold, or that is none.
		{
			name: "item: unknown", method: put, path: items + "/no-such", body: valid, ifMatch: `"1"`, status: 404,
			message: "phenix cannot find template no-such",
		},
		{
			name: "item: not an id", method: put, path: items + "/a%20b", body: valid, ifMatch: `"1"`, status: 404,
			message: "phenix cannot find template a b",
		},
		{
			name: "collection: unknown", method: put, path: collections + "/no-such", body: `{"name":"X"}`, ifMatch: `"1"`, status: 404,
			message: "phenix cannot find collection no-such",
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
			message: "templates[1].name: a template needs a name",
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
			body: `{"templates":[` + valid + `],"collection":{"name":""}}`, status: 422, message: "a collection needs a name",
		},
		{name: "collection: no name", method: post, path: collections, body: `{}`, status: 422, message: "a collection needs a name"},

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
			body:    `{"templates":[{"id":"` + strings.Repeat("n", builderTemplateRequestBytes) + `"}]}`,
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

			if recorder.Code != tt.status {
				t.Errorf("%s %s: status = %d, want %d: %.300s", tt.method, tt.path, recorder.Code, tt.status, recorder.Body)
			}

			if got := builderRefusal(t, recorder).Message; got != tt.message {
				t.Errorf("%s %s: message = %q, want %q", tt.method, tt.path, got, tt.message)
			}

			// A stale tag is answered with the current one. Nothing else is.
			if got, want := recorder.Header().Get("ETag"), map[bool]string{true: `"1"`, false: ""}[tt.status == 412]; got != want {
				t.Errorf("%s %s: ETag = %q, want %q", tt.method, tt.path, got, want)
			}

			if strings.Contains(recorder.Body.String(), builderTemplateSecret) || strings.Contains(logs.String(), builderTemplateSecret) {
				t.Errorf("%s %s: the answer or the log repeats a template: %.300s", tt.method, tt.path, recorder.Body)
			}
		})
	}

	after, err := harness.store.GetRecord(bapi.NamespaceTemplates, bapi.LibraryKey)
	if err != nil {
		t.Fatalf("reading the library record after the refusals: %v", err)
	}

	if after.Revision != before.Revision {
		t.Errorf("library record revision after the refusals = %d, want %d as before", after.Revision, before.Revision)
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
		t.Errorf("POST /items of %d bytes: status = %d, want %d: %.200s", len(large), recorder.Code, http.StatusCreated, recorder.Body)
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
		t.Errorf("registering the routes made the calls %q, want none", counting.calls)
	}

	harness.addTemplates("PLC", "HMI")

	for _, user := range []string{builderAlice, builderBob} {
		t.Run(user, func(t *testing.T) {
			counting.calls = nil

			if names := builderTemplateIDs(harness.templates(user)); len(names) != 2 {
				t.Errorf("templates(%s) holds %q, want both templates", user, names)
			}

			if want := []string{"GetRecord " + bapi.LibraryKey}; !slices.Equal(counting.calls, want) {
				t.Errorf("templates(%s) made the calls %q, want %q", user, counting.calls, want)
			}
		})
	}

	counting.calls = nil
	harness.addTemplates("RTU")

	if want := []string{"GetRecord " + bapi.LibraryKey, "UpdateRecord " + bapi.LibraryKey}; !slices.Equal(counting.calls, want) {
		t.Errorf("addTemplates(RTU) made the calls %q, want %q", counting.calls, want)
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

	want := builderTemplateLibraryResponse{
		Templates: []builderTemplateResponse{}, Collections: []builderTemplateCollectionResponse{},
		Preloaded: []builderPreloadedCollection{}, Damaged: true,
		Limits: builderTemplateLimits{
			Templates: bapi.MaxLibraryTemplates, Collections: 50, NameBytes: 128, DescriptionBytes: 1024, DeviceBytes: 16384,
		},
	}

	if diff := cmp.Diff(want, list, cmpopts.IgnoreFields(builderTemplateLibraryResponse{}, "Builtins")); diff != "" {
		t.Errorf("templates(alice) of a damaged library mismatch (-want +got):\n%s", diff)
	}

	if len(list.Builtins) != len(builderBuiltinIDs) {
		t.Errorf("templates(alice) of a damaged library holds %d built-in templates, want %d", len(list.Builtins), len(builderBuiltinIDs))
	}

	if len(logs.Records(t, plogtest.Message("builder template library is unreadable"))) != 1 {
		t.Errorf("the log does not say the library is unreadable once: %s", logs)
	}

	const message = "this version of phenix cannot read the template library"

	item := builderJSON(t, builderTemplateContentOf("Changed", ""))
	items := builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("New", "")}, Collection: nil,
	})

	for _, tt := range []struct {
		name    string
		request builderRequest
	}{
		{name: "add templates", request: builderRequest{method: http.MethodPost, path: builderLibraryPath("/items"), body: items}},
		{
			name: "replace a template",
			request: builderRequest{
				method: http.MethodPut, path: builderLibraryPath("/items/" + made[0].ID), body: item, ifMatch: `"1"`,
			},
		},
		{
			name:    "add a collection",
			request: builderRequest{method: http.MethodPost, path: builderLibraryPath("/collections"), body: `{"name":"X"}`},
		},
		{
			name: "replace a collection",
			request: builderRequest{
				method: http.MethodPut, path: builderLibraryPath("/collections/x"), body: `{"name":"X"}`, ifMatch: `"1"`,
			},
		},
		{
			name: "delete a template",
			request: builderRequest{
				method: http.MethodPost, path: builderLibraryPath("/delete"),
				body: builderTemplateDeleteBody(t, builderTemplateRefs(`"1"`, made[0].ID), nil),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := tt.request
			recorder := harness.library(request.method, request.path, builderAlice, request.body, request.ifMatch)

			if recorder.Code != http.StatusConflict {
				t.Errorf("%s %s: status = %d, want %d: %s", request.method, request.path, recorder.Code, http.StatusConflict, recorder.Body)
			}

			if got := builderRefusal(t, recorder).Message; got != message {
				t.Errorf("%s %s: message = %q, want %q", request.method, request.path, got, message)
			}

			if strings.Contains(recorder.Body.String(), builderTemplateSecret) {
				t.Errorf("%s %s repeats what the record holds: %s", request.method, request.path, recorder.Body)
			}
		})
	}

	after, err := harness.store.GetRecord(bapi.NamespaceTemplates, key)
	if err != nil {
		t.Fatalf("reading the library record after the refusals: %v", err)
	}

	// The record is to stay exactly as it was.
	if diff := cmp.Diff(value, string(after.Value)); diff != "" {
		t.Errorf("library record after the refusals mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderTemplateBusy asserts a library that keeps changing under a
// write is answered 503, with when to try again.
func TestBuilderTemplateBusy(t *testing.T) {
	harness := newBuilderHarness(t)
	made := harness.addTemplates("PLC")
	selection := builderTemplateDeleteBody(t, builderTemplateRefs(`"1"`, made[0].ID), nil)

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

	type answer struct {
		Status             int
		RetryAfter, Reason string
		Rounds             int
	}

	got := answer{
		Status: recorder.Code, RetryAfter: recorder.Header().Get("Retry-After"),
		Reason: builderRefusal(t, recorder).Message, Rounds: round,
	}
	want := answer{
		Status: http.StatusServiceUnavailable, RetryAfter: "1", Reason: "unable to delete the templates", Rounds: 5,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("POST /delete of a busy library mismatch (-want +got):\n%s", diff)
	}

	harness.store.BeforeUpdate = nil

	// Nothing of the request was applied, and it works when tried again.
	if got := builderTemplateIDs(harness.templates(builderAlice)); !slices.Contains(got, made[0].ID) {
		t.Errorf("templates(alice) after a delete that gave up = %q, want it to hold %s", got, made[0].ID)
	}

	if again := harness.post("/delete", selection); again.Code != http.StatusOK {
		t.Errorf("POST /delete again: status = %d, want %d: %s", again.Code, http.StatusOK, again.Body)
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
			t.Errorf("PUT %s of Winner: status = %d, want %d: %s", path, winner.Code, http.StatusOK, winner.Body)
		}

		return nil
	}

	loser := harness.put(path, builderJSON(t, builderTemplateContentOf("Loser", "")), `"1"`)

	if got, want := builderTagOf(loser), (builderTag{http.StatusPreconditionFailed, `"2"`}); got != want {
		t.Errorf("PUT %s of Loser = %+v, want %+v: %s", path, got, want, loser.Body)
	}

	if got := harness.templates(builderAlice).Templates[0].Name; got != "Winner" {
		t.Errorf("templates(alice): template name = %q, want Winner", got)
	}
}
