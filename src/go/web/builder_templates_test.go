package web

import (
	"context"
	"encoding/base64"
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

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
)

const (
	builderTemplatesRoute = "/builder/templates"

	// builderTemplateSecret is in the spec of every template the tests
	// make, so a log or an error that repeats a template is seen.
	builderTemplateSecret = "s3cret-template-setting"
)

// The ids of the built-in templates, in the order a library starts with.
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

// builderJSON encodes a request body.
func builderJSON(t *testing.T, value any) string {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	return string(body)
}

// builderTemplateIcon returns the id and the entry of a custom icon whose
// image depends on seed.
func builderTemplateIcon(t *testing.T, seed int) (string, bdoc.Icon) {
	t.Helper()

	png := builderIconPNG(t, 2, 2, seed)

	return bdoc.IconID(png), bdoc.Icon{Name: "icon " + strconv.Itoa(seed), Data: base64.StdEncoding.EncodeToString(png)}
}

// builderLibraryPath returns a path below the library of owner.
func builderLibraryPath(owner, rest string) string {
	return builderTemplatesRoute + "/" + owner + rest
}

// assertTemplateHeaders asserts a response of the template library tells
// the browser not to guess a content type, and is JSON.
func assertTemplateHeaders(t *testing.T, what string, recorder *httptest.ResponseRecorder) {
	t.Helper()

	if got := recorder.Header().Get("X-Content-Type-Options"); got != builderNoSniff {
		t.Errorf("%s: X-Content-Type-Options = %q, want %q", what, got, builderNoSniff)
	}

	if got := recorder.Header().Get("Content-Type"); recorder.Body.Len() != 0 && got != mimeJSON {
		t.Errorf("%s: Content-Type = %q, want %q", what, got, mimeJSON)
	}
}

// library makes a request of the template library and checks its headers.
func (h *builderHarness) library(method, path, user, body, ifMatch string) *httptest.ResponseRecorder {
	h.t.Helper()

	recorder := h.do(builderRequest{method: method, path: path, body: body, user: user, ifMatch: ifMatch})

	assertTemplateHeaders(h.t, method+" "+path, recorder)

	return recorder
}

// post makes a POST below alice's library, as alice.
func (h *builderHarness) post(rest, body string) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.library(http.MethodPost, builderLibraryPath(builderTestOwner, rest), builderTestOwner, body, "")
}

// put makes a PUT below alice's library, as alice.
func (h *builderHarness) put(rest, body, ifMatch string) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.library(http.MethodPut, builderLibraryPath(builderTestOwner, rest), builderTestOwner, body, ifMatch)
}

// templates returns what the listing answers the user.
func (h *builderHarness) templates(user string) builderTemplateLibraryResponse {
	h.t.Helper()

	recorder := h.library(http.MethodGet, builderTemplatesRoute, user, "", "")
	if recorder.Code != http.StatusOK {
		h.t.Fatalf("listing templates: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	// The account a share is bound to is never sent.
	if strings.Contains(recorder.Body.String(), "userCreated") {
		h.t.Fatalf("the listing names the account of a share: %s", recorder.Body)
	}

	var list builderTemplateLibraryResponse

	h.decode(recorder, &list)

	return list
}

// addTemplates adds templates with the given names to the user's library.
func (h *builderHarness) addTemplates(user string, names ...string) []builderTemplateRef {
	h.t.Helper()

	request := builderTemplateCreateRequest{Templates: nil, Collection: nil, Icons: nil}
	for _, name := range names {
		request.Templates = append(request.Templates, builderTemplateContentOf(name, ""))
	}

	recorder := h.library(http.MethodPost, builderLibraryPath(user, "/items"), user, builderJSON(h.t, request), "")
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

// TestBuilderTemplateLibraryStartsWithBuiltins asserts the whole shape of
// the listing for a user who never changed the library: the five built-in
// templates as ordinary templates of the user's own, and nothing written.
func TestBuilderTemplateLibraryStartsWithBuiltins(t *testing.T) {
	harness := newBuilderHarness(t)

	recorder := harness.library(http.MethodGet, builderTemplatesRoute, builderTestOwner, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("listing templates: status = %d: %s", recorder.Code, recorder.Body)
	}

	var raw struct {
		Owner       string            `json:"owner"`
		Templates   []json.RawMessage `json:"templates"`
		Collections json.RawMessage   `json:"collections"`
		Icons       json.RawMessage   `json:"icons"`
		CanShare    *bool             `json:"canShare"`
		CanPublish  *bool             `json:"canPublish"`
		Damaged     *bool             `json:"damaged"`
		Limits      json.RawMessage   `json:"limits"`
	}

	harness.decode(recorder, &raw)

	if raw.Owner != builderTestOwner || string(raw.Collections) != "[]" || raw.Icons != nil {
		t.Fatalf("the listing = %s, want the caller's, with an empty list of collections and no icons", recorder.Body)
	}

	// Sharing and publishing are not offered, and the flags are sent: alice
	// has no account, and her role cannot publish to every user.
	for name, flag := range map[string]*bool{"canShare": raw.CanShare, "canPublish": raw.CanPublish, "damaged": raw.Damaged} {
		if flag == nil || *flag {
			t.Errorf("%s = %v, want it sent and false", name, flag)
		}
	}

	if got, want := string(raw.Limits),
		`{"templates":200,"collections":50,"shares":25,"nameBytes":128,"descriptionBytes":1024,"deviceBytes":16384,"icons":50}`; got != want {
		t.Errorf("limits = %s, want %s", got, want)
	}

	if len(raw.Templates) != len(builderBuiltinIDs) {
		t.Fatalf("the listing holds %d templates, want the five built-in ones", len(raw.Templates))
	}

	// A built-in template never changed has no times, and every list is a
	// list.
	if got, want := string(raw.Templates[0]), `{"id":"server","owner":"alice","source":"own","name":"Server",`+
		`"description":"Generic Linux server","device":{"iconKey":"server","spec":{"general":{"description":"",`+
		`"hostname":"server","vm_type":"kvm"},"hardware":{"drives":[{"image":"ubuntu.qc2"}],"os_type":"linux"},`+
		`"network":{"interfaces":[]},"type":"VirtualMachine"}},"version":1,"etag":"\"1\"","serverWide":false,`+
		`"collections":[],"shares":[]}`; got != want {
		t.Errorf("the first template = %s, want %s", got, want)
	}

	list := harness.templates(builderTestOwner)
	builtins := bdoc.BuiltinTemplates()

	for i, template := range list.Templates {
		if template.ID != builderBuiltinIDs[i] || template.Name != builtins[i].Name || template.Owner != builderTestOwner ||
			template.Source != "own" || template.Version != 1 || template.ETag != `"1"` || template.ServerWide ||
			!template.Created.IsZero() || !template.Updated.IsZero() || template.PublishedBy != "" {
			t.Errorf("template %d = %+v, want the built-in %q as the caller's own", i, template, builderBuiltinIDs[i])
		}
	}

	if got := harness.store.Count(bapi.NamespaceTemplates); got != 0 {
		t.Fatalf("listing wrote %d library records, want 0", got)
	}
}

// builderTemplateFixture is a library of alice's that holds, after the
// built-in templates, the templates PLC, which names a custom icon, and HMI,
// and the collection "Plant floor" of the two.
type builderTemplateFixture struct {
	harness         *builderHarness
	logs            *plogtest.Logs
	plc, hmi, floor string
	iconID          string
	icon            bdoc.Icon
}

// newBuilderTemplateFixture makes the fixture with one request, which also
// carries an icon no template names.
func newBuilderTemplateFixture(t *testing.T) *builderTemplateFixture {
	t.Helper()

	fixture := &builderTemplateFixture{harness: newBuilderHarness(t), logs: plogtest.Capture(t)}
	fixture.iconID, fixture.icon = builderTemplateIcon(t, 21)
	spareID, spare := builderTemplateIcon(t, 22)

	request := builderTemplateCreateRequest{
		Templates:  []builderTemplateContent{builderTemplateContentOf("PLC", fixture.iconID), builderTemplateContentOf("HMI", "")},
		Collection: nil,
		Icons:      map[string]bdoc.Icon{fixture.iconID: fixture.icon, spareID: spare},
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
// holds nothing a template, a collection or an icon holds.
func (f *builderTemplateFixture) assertLogged(t *testing.T, messages ...string) {
	t.Helper()

	for _, message := range messages {
		if len(f.logs.Records(t, plogtest.Message(message))) == 0 {
			t.Errorf("the log does not say %q", message)
		}
	}

	if text := f.logs.String(); strings.Contains(text, builderTemplateSecret) || strings.Contains(text, f.icon.Data[:24]) ||
		strings.Contains(text, "Plant floor") {
		t.Errorf("the log holds what a template, a collection or an icon holds: %s", text)
	}
}

// TestBuilderTemplateCreate asserts templates, a collection of them and the
// icon they name are added in one request, and listed as the caller's own.
func TestBuilderTemplateCreate(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	list := fixture.harness.templates(builderTestOwner)

	// The new templates go after the built-in ones, in the order sent.
	want := append(slices.Clone(builderBuiltinIDs), fixture.plc, fixture.hmi)

	if got := builderTemplateIDs(list); !slices.Equal(got, want) {
		t.Fatalf("the listing holds %q, want %q", got, want)
	}

	// Only the icon a template names is kept and listed.
	if len(list.Icons) != 1 || list.Icons[fixture.iconID] != fixture.icon {
		t.Fatalf("the listing carries the icons %v, want only %s", list.Icons, fixture.iconID)
	}

	plc := list.Templates[len(builderBuiltinIDs)]

	if plc.Name != "PLC" || plc.Description != "made by a test" || plc.Device.Icon != fixture.iconID ||
		plc.Device.IconKey != bdoc.IconServer || plc.Version != 1 || plc.Created.IsZero() ||
		!plc.Created.Equal(plc.Updated) || !slices.Equal(plc.Collections, []string{fixture.floor}) || plc.Shares == nil {
		t.Fatalf("the new template = %+v", plc)
	}

	if len(list.Collections) != 1 {
		t.Fatalf("the listing holds %d collections, want 1", len(list.Collections))
	}

	floor := list.Collections[0]

	if floor.ID != fixture.floor || floor.Name != "Plant floor" || floor.Description != "line 1" ||
		!slices.Equal(floor.TemplateIDs, []string{fixture.plc, fixture.hmi}) || floor.Owner != builderTestOwner ||
		floor.Source != "own" || floor.ServerWide || floor.Version != 1 || floor.Created.IsZero() {
		t.Fatalf("the new collection = %+v", floor)
	}

	fixture.assertLogged(t, "added builder templates")
}

// TestBuilderTemplateReplace asserts replacing a template needs the entity
// tag of its content, which moves on only when the content changes.
func TestBuilderTemplateReplace(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness

	content := builderJSON(t, builderTemplateUpdateRequest{builderTemplateContent: builderTemplateContentOf("PLC two", ""), Icons: nil})
	path := "/items/" + fixture.plc

	replaced := harness.put(path, content, `"1"`)

	var template builderTemplateResponse

	harness.decode(replaced, &template)

	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != `"2"` || template.ETag != `"2"` ||
		template.ID != fixture.plc || template.Name != "PLC two" || template.Version != 2 || template.Device.Icon != "" ||
		!template.Updated.After(template.Created) || !slices.Equal(template.Collections, []string{fixture.floor}) {
		t.Fatalf("replacing a template = %d %s", replaced.Code, replaced.Body)
	}

	// The icon no template names any more left the library.
	if list := harness.templates(builderTestOwner); len(list.Icons) != 0 {
		t.Fatalf("after the replacement the listing carries the icons %v", list.Icons)
	}

	// The same content again changes nothing and keeps the tag.
	again := harness.put(path, content, `"2"`)
	if again.Code != http.StatusOK || again.Header().Get("ETag") != `"2"` {
		t.Fatalf("replacing with the same content = %d %v", again.Code, again.Header())
	}

	// The tag that was replaced no longer is the content's.
	stale := harness.put(path, content, `"1"`)
	if stale.Code != http.StatusPreconditionFailed || stale.Header().Get("ETag") != `"2"` {
		t.Fatalf("replacing with the old tag = %d %v, want 412 with the current tag", stale.Code, stale.Header())
	}

	// A replacement may bring the icon it names.
	drawn := builderJSON(t, builderTemplateUpdateRequest{
		builderTemplateContent: builderTemplateContentOf("PLC three", fixture.iconID),
		Icons:                  map[string]bdoc.Icon{fixture.iconID: fixture.icon},
	})

	if back := harness.put(path, drawn, `"2"`); back.Code != http.StatusOK ||
		back.Header().Get("ETag") != `"3"` {
		t.Fatalf("replacing with an icon = %d %s", back.Code, back.Body)
	}

	if list := harness.templates(builderTestOwner); len(list.Icons) != 1 || list.Icons[fixture.iconID] != fixture.icon {
		t.Fatalf("after bringing the icon back the listing carries %v", list.Icons)
	}

	fixture.assertLogged(t, "changed builder template")
}

// TestBuilderTemplateCollections asserts a collection is added and replaced
// like a template, under an entity tag of its own.
func TestBuilderTemplateCollections(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness
	floor := "/collections/" + fixture.floor

	added := harness.post("/collections", `{"name":"Spares","description":"","templateIds":["server","`+fixture.hmi+`"]}`)

	var spares builderTemplateCollectionResponse

	harness.decode(added, &spares)

	if added.Code != http.StatusCreated || added.Header().Get("ETag") != `"1"` || spares.Name != "Spares" || spares.Version != 1 ||
		!slices.Equal(spares.TemplateIDs, []string{"server", fixture.hmi}) || spares.Owner != builderTestOwner ||
		spares.Source != "own" || spares.Shares == nil || spares.ID == fixture.floor {
		t.Fatalf("adding a collection = %d %s", added.Code, added.Body)
	}

	// A template may be in several collections.
	list := harness.templates(builderTestOwner)

	if hmi := list.Templates[len(builderBuiltinIDs)+1]; !slices.Equal(hmi.Collections, []string{fixture.floor, spares.ID}) {
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
	if plc := harness.templates(builderTestOwner).Templates[len(builderBuiltinIDs)]; plc.ETag != `"1"` || len(plc.Collections) != 0 {
		t.Fatalf("the template left out of the collection = %+v", plc)
	}

	fixture.assertLogged(t, "added builder template collection", "changed builder template collection")
}

// TestBuilderTemplateDelete asserts templates, built-in ones too, and
// collections are deleted in one request, which is harmless to repeat.
func TestBuilderTemplateDelete(t *testing.T) {
	fixture := newBuilderTemplateFixture(t)
	harness := fixture.harness

	added := harness.post("/collections", `{"name":"Spares","templateIds":["server"]}`)

	var spares builderTemplateCollectionResponse

	harness.decode(added, &spares)

	selection := `{"templates":["` + fixture.plc + `","router","no-such"],"collections":["` + spares.ID + `"]}`

	deleted := harness.post("/delete", selection)
	if got := strings.TrimSpace(deleted.Body.String()); deleted.Code != http.StatusOK ||
		got != `{"deleted":{"templates":2,"collections":1}}` {
		t.Fatalf("deleting = %d %s", deleted.Code, got)
	}

	list := harness.templates(builderTestOwner)

	want := []string{"server", "workstation", "firewall", "external", fixture.hmi}

	if got := builderTemplateIDs(list); !slices.Equal(got, want) {
		t.Fatalf("after the delete the listing holds %q, want %q", got, want)
	}

	// The collection that held the deleted template lost it, and its tag
	// moved on. The template's icon left with it. The deleted collection's
	// template stays.
	if len(list.Collections) != 1 || list.Collections[0].ID != fixture.floor || list.Collections[0].ETag != `"2"` ||
		!slices.Equal(list.Collections[0].TemplateIDs, []string{fixture.hmi}) || len(list.Icons) != 0 {
		t.Fatalf("after the delete the collections = %+v and the icons %v", list.Collections, list.Icons)
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

	if list := harness.templates(builderTestOwner); len(list.Collections) != 1 || list.Collections[0].TemplateIDs == nil ||
		len(list.Collections[0].TemplateIDs) != 0 || list.Collections[0].ETag != `"3"` {
		t.Fatalf("after deleting its last template the collection = %+v", list.Collections)
	}

	fixture.assertLogged(t, "deleted builder templates")
}

// TestBuilderDeletedBuiltinTemplateStaysDeleted asserts a built-in template
// a user deleted is not added again, by a read or by any later write.
func TestBuilderDeletedBuiltinTemplateStaysDeleted(t *testing.T) {
	harness := newBuilderHarness(t)

	if deleted := harness.post("/delete", `{"templates":["server","external"]}`); deleted.Code != http.StatusOK {
		t.Fatalf("deleting two built-in templates = %d %s", deleted.Code, deleted.Body)
	}

	want := []string{"workstation", "router", "firewall"}

	if got := builderTemplateIDs(harness.templates(builderTestOwner)); !slices.Equal(got, want) {
		t.Fatalf("after the delete the listing holds %q, want %q", got, want)
	}

	added := harness.addTemplates(builderTestOwner, "PLC")

	if got := builderTemplateIDs(harness.templates(builderTestOwner)); !slices.Equal(got, append(want, added[0].ID)) {
		t.Fatalf("after a later write the listing holds %q", got)
	}

	// Another user still starts with all five.
	if got := builderTemplateIDs(harness.templates(builderTestPeer)); !slices.Equal(got, builderBuiltinIDs) {
		t.Fatalf("another user's listing holds %q, want %q", got, builderBuiltinIDs)
	}
}

// TestBuilderTemplatePermissions asserts each route needs the base config
// permission of its verb, and nothing else: sharing also needs an account,
// which the caller has, and taking an item back from every user is the
// owner's with config update alone.
func TestBuilderTemplatePermissions(t *testing.T) {
	role := func(verbs ...string) *rbac.Role {
		return builderShareRole(verbs)
	}

	// Every permission there is but on configs.
	noConfigs := builderRole(builderPolicy(
		[]string{builderDraftsResource, "builder-templates", "schemas", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		append(slices.Clone(builderShareConfigVerbs), "publish"),
	))

	const (
		ok        = http.StatusOK
		made      = http.StatusCreated
		forbidden = http.StatusForbidden
	)

	// none refuses every request; allow returns want with the given
	// requests, by index, answered with the given statuses.
	none := [9]int{forbidden, forbidden, forbidden, forbidden, forbidden, forbidden, forbidden, forbidden, forbidden}
	allow := func(want [9]int, pairs ...int) [9]int {
		for i := 0; i+1 < len(pairs); i += 2 {
			want[pairs[i]] = pairs[i+1]
		}

		return want
	}

	tests := []struct {
		name      string
		role      *rbac.Role
		anonymous bool
		// list, add items, replace an item, add a collection, replace a
		// collection, delete, share candidates, share, take back from
		// every user.
		want [9]int
	}{
		{name: "no identity", anonymous: true, want: none},
		{name: "no permission", role: role(), want: none},
		{name: "everything but configs", role: &noConfigs, want: none},
		{name: "configs list", role: role("list"), want: allow(none, 0, ok)},
		{name: "configs get", role: role("get"), want: none},
		{name: "configs create", role: role("create"), want: allow(none, 1, made, 3, made)},
		{name: "configs update", role: role("update"), want: allow(none, 2, ok, 4, ok, 6, ok, 7, ok, 8, ok)},
		{name: "configs delete", role: role("delete"), want: allow(none, 5, ok)},
		{name: "every config verb", role: role(builderShareConfigVerbs...), want: [9]int{ok, made, ok, made, ok, ok, ok, ok, ok}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderHarness(t)

			harness.setUser(builderTestOwner, builderShareCreated)
			harness.setUser(builderTestPeer, builderShareCreated)

			// The library holds a collection, so a replacement that is
			// allowed finds it.
			var collection string

			_, err := harness.service.UpdateLibrary(context.Background(), builderTestOwner, builderTestOwner,
				func(library *bapi.TemplateLibrary) error {
					var err error

					collection, err = library.AddCollection(
						bapi.CollectionContent{Name: "Floor", Description: "", TemplateIDs: nil}, harness.service.NewID,
					)

					return err
				})
			if err != nil {
				t.Fatalf("adding the collection: %v", err)
			}

			user := builderTestOwner
			if tt.anonymous {
				user = ""
			}

			item := builderJSON(t, builderTemplateContentOf("Changed", ""))
			items := builderJSON(t, builderTemplateCreateRequest{
				Templates: []builderTemplateContent{builderTemplateContentOf("New", "")}, Collection: nil, Icons: nil,
			})

			for i, request := range []builderRequest{
				{method: http.MethodGet, path: builderTemplatesRoute},
				{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/items"), body: items},
				{method: http.MethodPut, path: builderLibraryPath(builderTestOwner, "/items/server"), body: item, ifMatch: `"1"`},
				{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/collections"), body: `{"name":"More"}`},
				{
					method: http.MethodPut, path: builderLibraryPath(builderTestOwner, "/collections/"+collection),
					body: `{"name":"Floor two"}`, ifMatch: `"1"`,
				},
				{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/delete"), body: `{"templates":["router"]}`},
				{method: http.MethodGet, path: builderTemplatesRoute + "/candidates"},
				{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/share"), body: `{"templates":["server"],"add":["bob"]}`},
				{
					method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/publish"),
					body: `{"templates":["server"],"serverWide":false}`,
				},
			} {
				request.user = user
				request.role = tt.role

				recorder := harness.do(request)

				assertTemplateHeaders(t, request.method, recorder)

				if recorder.Code != tt.want[i] {
					t.Errorf("%s %s: status = %d, want %d: %s", request.method, request.path, recorder.Code, tt.want[i], recorder.Body)
				}
			}
		})
	}
}

// TestBuilderTemplateLibraryIsTheCallersOwn asserts no role reaches the
// library of another user: its write routes answer 404, as for a library
// that does not exist, and the listing holds nothing of it.
func TestBuilderTemplateLibraryIsTheCallersOwn(t *testing.T) {
	harness := newBuilderHarness(t)
	logs := plogtest.Capture(t)

	mine := harness.addTemplates(builderTestOwner, "Of alice")

	var collection string

	if _, err := harness.service.UpdateLibrary(context.Background(), builderTestOwner, builderTestOwner,
		func(library *bapi.TemplateLibrary) error {
			var err error

			collection, err = library.AddCollection(
				bapi.CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{mine[0].ID}}, harness.service.NewID,
			)

			return err
		}); err != nil {
		t.Fatalf("adding the collection: %v", err)
	}

	revision := func() int64 {
		record, err := harness.store.GetRecord(bapi.NamespaceTemplates, bapi.LibraryKey(builderTestOwner))
		if err != nil {
			t.Fatalf("reading the library record: %v", err)
		}

		return record.Revision
	}

	before := revision()
	item := builderJSON(t, builderTemplateContentOf("Taken over", ""))
	items := builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("Planted", "")}, Collection: nil, Icons: nil,
	})

	// The peer's role holds every permission there is, on other users'
	// drafts too.
	for _, owner := range []string{builderTestOwner, "nobody"} {
		for _, request := range []builderRequest{
			{method: http.MethodPost, path: builderLibraryPath(owner, "/items"), body: items},
			{method: http.MethodPut, path: builderLibraryPath(owner, "/items/"+mine[0].ID), body: item, ifMatch: `"1"`},
			{method: http.MethodPut, path: builderLibraryPath(owner, "/items/server"), body: item, ifMatch: `"1"`},
			// Refused before the missing If-Match is.
			{method: http.MethodPut, path: builderLibraryPath(owner, "/items/server"), body: item},
			{method: http.MethodPost, path: builderLibraryPath(owner, "/collections"), body: `{"name":"Planted"}`},
			{
				method: http.MethodPut, path: builderLibraryPath(owner, "/collections/"+collection),
				body: `{"name":"Taken over"}`, ifMatch: `"1"`,
			},
			{method: http.MethodPost, path: builderLibraryPath(owner, "/delete"), body: `{"templates":["server"]}`},
			// Refused before the body is read.
			{method: http.MethodPost, path: builderLibraryPath(owner, "/delete"), body: `not JSON`},
		} {
			request.user = builderTestPeer

			recorder := harness.do(request)

			assertTemplateHeaders(t, request.method, recorder)

			if want := "template library " + owner + " not found"; recorder.Code != http.StatusNotFound ||
				builderMessage(t, recorder) != want || recorder.Header().Get("ETag") != "" {
				t.Errorf("%s %s as another user = %d %s, want 404 %q", request.method, request.path, recorder.Code, recorder.Body, want)
			}
		}
	}

	if revision() != before || harness.store.Count(bapi.NamespaceTemplates) != 1 {
		t.Fatal("a request of another user wrote a library")
	}

	// Each refusal is logged as a possible probe.
	if got := len(logs.Records(t, plogtest.Message("builder template library request for another user not allowed"))); got != 16 {
		t.Errorf("%d refusals are logged, want 16", got)
	}

	// The peer's listing is its own library: nothing of the owner's.
	list := harness.templates(builderTestPeer)

	if list.Owner != builderTestPeer || !slices.Equal(builderTemplateIDs(list), builderBuiltinIDs) || len(list.Collections) != 0 {
		t.Fatalf("another user's listing = %+v", list)
	}

	for _, template := range list.Templates {
		if template.Owner != builderTestPeer || template.Source != "own" {
			t.Errorf("another user's listing holds %+v", template)
		}
	}

	// And the owner's is untouched.
	want := append(slices.Clone(builderBuiltinIDs), mine[0].ID)

	if got := builderTemplateIDs(harness.templates(builderTestOwner)); !slices.Equal(got, want) {
		t.Fatalf("the owner's listing holds %q, want %q", got, want)
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

// builderTemplateRefusals returns the refused requests of
// TestBuilderTemplateRequests, for a library that holds the given template
// and the given collection, both at version 1.
func builderTemplateRefusals(t *testing.T, template, collection string) []builderTemplateRefusal {
	t.Helper()

	var (
		items       = builderLibraryPath(builderTestOwner, "/items")
		item        = items + "/" + template
		collections = builderLibraryPath(builderTestOwner, "/collections")
		one         = collections + "/" + collection
		remove      = builderLibraryPath(builderTestOwner, "/delete")
		valid       = builderJSON(t, builderTemplateContentOf("Fine", ""))
		missingIcon = "sha256:" + strings.Repeat("a", 64)
		wrap        = func(templates string) string { return `{"templates":[` + templates + `]}` }
	)

	const (
		malformed = "request body is not a valid Builder request"
		post      = http.MethodPost
		put       = http.MethodPut
	)

	noName := builderTemplateContentOf("", "")
	badColor := builderTemplateContentOf("Colored", "")
	badColor.Device.FillColor = "red"
	noSpec := `{"name":"Bare","device":{}}`
	drawn := builderJSON(t, builderTemplateContentOf("Drawn", missingIcon))

	return []builderTemplateRefusal{
		// Malformed requests.
		{name: "items: not JSON", method: post, path: items, body: `{"templates":`, status: 400, message: malformed},
		{
			name:    "items: an unknown field",
			method:  post,
			path:    items,
			body:    `{"templates":[],"owner":"bob"}`,
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
			message: "template " + template + " has changed since it was last read",
		},
		{
			name: "collection: a stale tag", method: put, path: one, body: `{"name":"X"}`, ifMatch: `"7"`, status: 412,
			message: "collection " + collection + " has changed since it was last read",
		},

		// Well formed, and refused in words that name the template.
		{
			name: "items: no name", method: post, path: items, body: wrap(valid + "," + builderJSON(t, noName)), status: 422,
			message: "templates[1].name: template name is required",
		},
		{
			name: "items: no spec", method: post, path: items, body: wrap(noSpec), status: 422,
			message: "templates[0].device.spec: template device spec is required",
		},
		{
			name: "items: a color that is none", method: post, path: items, body: wrap(builderJSON(t, badColor)), status: 422,
			message: `templates[0].device.fillColor: color "red" must be a hex color such as #2f6fbf`,
		},
		{
			name: "items: an icon the request does not carry", method: post, path: items, body: wrap(valid + "," + drawn), status: 422,
			message: `template 1 names custom icon "` + missingIcon + `", which the request does not carry`,
		},
		{
			name: "items: an icon that is not a PNG", method: post, path: items,
			body:    `{"templates":[` + drawn + `],"icons":{"` + missingIcon + `":{"data":"bm90IGEgUE5H"}}}`,
			status:  422,
			message: `icons: icon "sha256:` + strings.Repeat("a", 57) + `..." is not an accepted PNG: the image is not a PNG`,
		},
		{
			name: "items: a collection with no name", method: post, path: items,
			body: `{"templates":[` + valid + `],"collection":{"name":""}}`, status: 422, message: "collection name is required",
		},
		{
			name: "item: no name", method: put, path: item, body: builderJSON(t, noName), ifMatch: `"1"`, status: 422,
			message: "template.name: template name is required",
		},
		{
			name: "item: an icon the request does not carry", method: put, path: item, body: drawn, ifMatch: `"1"`, status: 422,
			message: `template 0 names custom icon "` + missingIcon + `", which the request does not carry`,
		},
		{name: "collection: no name", method: post, path: collections, body: `{}`, status: 422, message: "collection name is required"},
		{
			name: "collection: a template of no library", method: post, path: collections, body: `{"name":"X","templateIds":["nowhere"]}`,
			status: 422, message: `collection names template "nowhere", which this library does not hold`,
		},
		{
			name: "collection: a template twice", method: put, path: one, body: `{"name":"X","templateIds":["server","server"]}`,
			ifMatch: `"1"`, status: 422, message: `collection names template "server" more than once`,
		},

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

	made := harness.addTemplates(builderTestOwner, "PLC")

	var collection string

	if _, err := harness.service.UpdateLibrary(context.Background(), builderTestOwner, builderTestOwner,
		func(library *bapi.TemplateLibrary) error {
			var err error

			collection, err = library.AddCollection(
				bapi.CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{"server"}}, harness.service.NewID,
			)

			return err
		}); err != nil {
		t.Fatalf("adding the collection: %v", err)
	}

	before, err := harness.store.GetRecord(bapi.NamespaceTemplates, bapi.LibraryKey(builderTestOwner))
	if err != nil {
		t.Fatalf("reading the library record: %v", err)
	}

	for _, tt := range builderTemplateRefusals(t, made[0].ID, collection) {
		t.Run(tt.name, func(t *testing.T) {
			logs := plogtest.Capture(t)
			recorder := harness.library(tt.method, tt.path, builderTestOwner, tt.body, tt.ifMatch)

			if recorder.Code != tt.status || builderMessage(t, recorder) != tt.message {
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

	after, err := harness.store.GetRecord(bapi.NamespaceTemplates, bapi.LibraryKey(builderTestOwner))
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("a refused request wrote the library: revision %d, was %d (%v)", after.Revision, before.Revision, err)
	}
}

// TestBuilderTemplateBodyLimits asserts the route that adds templates takes
// a body as large as a Builder request may be, and the others far less.
func TestBuilderTemplateBodyLimits(t *testing.T) {
	harness := newBuilderHarness(t)

	// Icons nothing names are dropped, so a large request is a small write.
	icons := make(map[string]bdoc.Icon, 40)
	filler := strings.Repeat("A", 54_000)

	for i := range 40 {
		icons["sha256:"+fmt.Sprintf("%064x", i)] = bdoc.Icon{Name: "", Data: filler}
	}

	large := builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("Carried", "")}, Collection: nil, Icons: icons,
	})

	if len(large) < 2<<20 {
		t.Fatalf("the request is %d bytes, want it over 2 MiB", len(large))
	}

	if recorder := harness.post("/items", large); recorder.Code != http.StatusCreated {
		t.Fatalf("adding a template with a %d byte request = %d %.200s", len(large), recorder.Code, recorder.Body)
	}

	if list := harness.templates(builderTestOwner); len(list.Icons) != 0 || len(list.Templates) != len(builderBuiltinIDs)+1 {
		t.Fatalf("the listing = %d templates and %d icons", len(list.Templates), len(list.Icons))
	}

	over := `{"templates":[],"icons":{"x":{"data":"` + strings.Repeat("A", builderMaxRequestBytes) + `"}}}`

	recorder := harness.post("/items", over)
	want := fmt.Sprintf("request body is larger than %d bytes", builderMaxRequestBytes)

	if recorder.Code != http.StatusRequestEntityTooLarge || builderMessage(t, recorder) != want {
		t.Fatalf("a request over the limit = %d %.200s, want 413 %q", recorder.Code, recorder.Body, want)
	}
}

// TestBuilderTemplateLibraryLimits asserts the bounds of a library that only
// several requests reach: its templates, its collections, its icons and the
// size of its record.
func TestBuilderTemplateLibraryLimits(t *testing.T) {
	harness := newBuilderHarness(t)

	names := make([]string, 0, bapi.MaxLibraryTemplates)
	for i := range bapi.MaxLibraryTemplates - len(builderBuiltinIDs) {
		names = append(names, "T"+strconv.Itoa(i))
	}

	harness.addTemplates(builderTestOwner, names...)

	one := builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("One more", "")}, Collection: nil, Icons: nil,
	})

	recorder := harness.post("/items", one)
	if recorder.Code != http.StatusRequestEntityTooLarge || builderMessage(t, recorder) != "a library holds at most 200 templates" {
		t.Fatalf("the 201st template = %d %s", recorder.Code, recorder.Body)
	}

	for i := range bapi.MaxLibraryCollections {
		if added := harness.post("/collections", `{"name":"C`+strconv.Itoa(i)+`"}`); added.Code != http.StatusCreated {
			t.Fatalf("collection %d = %d %s", i, added.Code, added.Body)
		}
	}

	recorder = harness.post("/collections", `{"name":"One more"}`)
	if recorder.Code != http.StatusRequestEntityTooLarge || builderMessage(t, recorder) != "a library holds at most 50 collections" {
		t.Fatalf("the 51st collection = %d %s", recorder.Code, recorder.Body)
	}

	// Room for templates again, each with an icon of its own.
	everything := builderTemplateSelection{Templates: builderTemplateIDs(harness.templates(builderTestOwner)), Collections: nil}

	if cleared := harness.post("/delete", builderJSON(t, everything)); cleared.Code != http.StatusOK {
		t.Fatalf("emptying the library = %d %s", cleared.Code, cleared.Body)
	}

	drawn := builderTemplateCreateRequest{Templates: nil, Collection: nil, Icons: map[string]bdoc.Icon{}}

	for i := range bapi.MaxLibraryTemplateIcons + 1 {
		id, icon := builderTemplateIcon(t, 300+i)
		drawn.Icons[id] = icon
		drawn.Templates = append(drawn.Templates, builderTemplateContentOf("Drawn "+strconv.Itoa(i), id))
	}

	recorder = harness.post("/items", builderJSON(t, drawn))
	if recorder.Code != http.StatusRequestEntityTooLarge || builderMessage(t, recorder) != "a library holds at most 50 custom icons" {
		t.Fatalf("51 icons = %d %.300s", recorder.Code, recorder.Body)
	}

	drawn.Templates = drawn.Templates[:bapi.MaxLibraryTemplateIcons]

	if added := harness.post("/items", builderJSON(t, drawn)); added.Code != http.StatusCreated {
		t.Fatalf("50 icons = %d %.300s", added.Code, added.Body)
	}

	list := harness.templates(builderTestOwner)
	if len(list.Icons) != bapi.MaxLibraryTemplateIcons || list.Limits.Icons != len(list.Icons) {
		t.Fatalf("the listing carries %d icons, want %d", len(list.Icons), bapi.MaxLibraryTemplateIcons)
	}

	// The record itself: templates of close to 16 KiB each fill 512 KiB.
	heavy := builderTemplateCreateRequest{Templates: nil, Collection: nil, Icons: nil}

	for i := range 40 {
		content := builderTemplateContentOf("Heavy "+strconv.Itoa(i), "")
		content.Device.Spec["padding"] = strings.Repeat("p", bdoc.MaxTemplateDeviceBytes-512)
		heavy.Templates = append(heavy.Templates, content)
	}

	recorder = harness.post("/items", builderJSON(t, heavy))
	if message := builderMessage(t, recorder); recorder.Code != http.StatusRequestEntityTooLarge ||
		!strings.HasPrefix(message, "a library takes at most 524288 bytes, and this one would take ") {
		t.Fatalf("a library over 512 KiB = %d %q", recorder.Code, message)
	}

	if strings.Contains(recorder.Body.String(), "pppp") {
		t.Fatalf("the refusal repeats the templates: %.300s", recorder.Body)
	}
}

// builderCountingStore counts the record store calls of one namespace.
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

// TestBuilderTemplateListingReadsOnlyHintedLibraries asserts the listing
// reads the caller's own library, lists the keys of the hints, and reads the
// library of each owner a hint names, and no other: it lists no library and
// never reaches the library of a user who shared nothing with the caller and
// published nothing. Starting the server reads and removes nothing in the
// namespace either.
func TestBuilderTemplateListingReadsOnlyHintedLibraries(t *testing.T) {
	harness := newBuilderHarness(t)

	for _, user := range []string{builderTestOwner, builderTestPeer, builderShareCarol, builderShareDave, builderShareErin} {
		harness.setUser(user, builderShareCreated)
	}

	harness.addTemplates(builderTestOwner, "Of alice")
	bob := harness.addTemplates(builderTestPeer, "Of bob")
	carol := harness.addTemplates(builderShareCarol, "Of carol")
	harness.addTemplates(builderShareDave, "Of dave")
	erin := harness.addTemplates(builderShareErin, "Of erin")

	// Bob shares with alice; carol publishes; erin shared with alice and
	// stopped, which leaves her hint; dave does neither.
	harness.mustShareTemplates(builderTestPeer, builderTemplateShareRequest{
		Templates: []string{bob[0].ID}, Collections: nil, Add: []string{builderTestOwner}, Remove: nil,
	})
	harness.mustShareTemplates(builderShareErin, builderTemplateShareRequest{
		Templates: []string{erin[0].ID}, Collections: nil, Add: []string{builderTestOwner}, Remove: nil,
	})
	harness.mustShareTemplates(builderShareErin, builderTemplateShareRequest{
		Templates: []string{erin[0].ID}, Collections: nil, Add: nil, Remove: []string{builderTestOwner},
	})
	harness.mustPublishTemplates(builderShareCarol, builderShareCarol, builderPublisherRole(), []string{carol[0].ID}, true)

	counting := &builderCountingStore{Store: harness.store, namespace: bapi.NamespaceTemplates, calls: nil}
	ids := new(atomic.Int64)

	service, err := bapi.New(
		bapi.WithStore(counting),
		bapi.WithClock(func() time.Time { return memrecord.Time(ids.Add(1)) }),
		bapi.WithIDSource(func() (string, error) { return "other-" + strconv.FormatInt(ids.Add(1), 10), nil }),
	)
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	harness.router, harness.api = newBuilderRouter()
	harness.service = service

	if err := registerBuilderRoutes(
		harness.api,
		withBuilderService(service),
		withBuilderConfigs(harness.listConfigs, harness.getConfig),
		withBuilderPublishOps(harness.publishOps()),
	); err != nil {
		t.Fatalf("registerBuilderRoutes returned error: %v", err)
	}

	// The cleanup a server start runs does not touch the namespace: five
	// libraries and three hints.
	if len(counting.calls) != 0 || harness.store.Count(bapi.NamespaceTemplates) != 8 {
		t.Fatalf("registering the routes made the calls %q", counting.calls)
	}

	sortedCalls := func(calls ...string) []string {
		return slices.Sorted(slices.Values(calls))
	}

	list := harness.templates(builderTestOwner)

	want := sortedCalls(
		"GetRecord "+bapi.LibraryKey(builderTestOwner),
		"ListRecordKeys in/"+bapi.OwnerScope(builderTestOwner)+"/",
		"ListRecordKeys pub/",
		"GetRecord "+bapi.LibraryKey(builderTestPeer),
		"GetRecord "+bapi.LibraryKey(builderShareCarol),
		"GetRecord "+bapi.LibraryKey(builderShareErin),
	)

	if got := sortedCalls(counting.calls...); !slices.Equal(got, want) {
		t.Fatalf("the listing made the calls %q, want %q", got, want)
	}

	// The caller's own first, then the others by owner; nothing of dave's
	// or of erin's.
	names := make([]string, 0, len(list.Templates))
	for _, template := range list.Templates[len(builderBuiltinIDs):] {
		names = append(names, template.Owner+":"+template.Source+":"+template.Name)
	}

	if want := []string{"alice:own:Of alice", "bob:shared:Of bob", "carol:server:Of carol"}; !slices.Equal(names, want) {
		t.Fatalf("the listing holds %q, want %q", names, want)
	}

	// A user nothing is shared with reads the published libraries alone.
	counting.calls = nil

	if list := harness.templates(builderShareDave); len(list.Templates) != len(builderBuiltinIDs)+2 ||
		list.Templates[len(list.Templates)-1].Name != "Of carol" {
		t.Fatalf("dave's listing holds %q", builderTemplateIDs(list))
	}

	want = sortedCalls(
		"GetRecord "+bapi.LibraryKey(builderShareDave),
		"ListRecordKeys in/"+bapi.OwnerScope(builderShareDave)+"/",
		"ListRecordKeys pub/",
		"GetRecord "+bapi.LibraryKey(builderShareCarol),
	)

	if got := sortedCalls(counting.calls...); !slices.Equal(got, want) {
		t.Fatalf("dave's listing made the calls %q, want %q", got, want)
	}

	// A change reads and writes the caller's record, and no other.
	counting.calls = nil
	harness.addTemplates(builderTestOwner, "Another")

	key := bapi.LibraryKey(builderTestOwner)

	if want := []string{"GetRecord " + key, "UpdateRecord " + key}; !slices.Equal(counting.calls, want) {
		t.Fatalf("a change made the calls %q, want %q", counting.calls, want)
	}
}

// TestBuilderTemplateDamagedLibrary asserts a library record this server
// cannot read is reported as damaged, with none of its items, and that no
// change of it is accepted.
func TestBuilderTemplateDamagedLibrary(t *testing.T) {
	harness := newBuilderHarness(t)
	made := harness.addTemplates(builderTestOwner, "PLC")
	key := bapi.LibraryKey(builderTestOwner)

	record, err := harness.store.GetRecord(bapi.NamespaceTemplates, key)
	if err != nil {
		t.Fatalf("reading the library record: %v", err)
	}

	// What a later version of phenix may write: a field this one does not
	// know.
	value := strings.Replace(string(record.Value), `{"owner"`, `{"layout":"`+builderTemplateSecret+`","owner"`, 1)
	harness.store.SetValue(bapi.NamespaceTemplates, key, []byte(value))

	logs := plogtest.Capture(t)
	list := harness.templates(builderTestOwner)

	if !list.Damaged || list.Owner != builderTestOwner || len(list.Templates) != 0 || list.Templates == nil ||
		len(list.Collections) != 0 || list.Collections == nil || len(list.Icons) != 0 || list.Limits.Templates != bapi.MaxLibraryTemplates {
		t.Fatalf("the listing of a damaged library = %+v", list)
	}

	if len(logs.Records(t, plogtest.Message("builder template library is unreadable"))) != 1 {
		t.Errorf("the damaged library is not logged: %s", logs)
	}

	const message = "this library cannot be read by this version of phenix"

	item := builderJSON(t, builderTemplateContentOf("Changed", ""))
	items := builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("New", "")}, Collection: nil, Icons: nil,
	})

	for _, request := range []builderRequest{
		{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/items"), body: items},
		{method: http.MethodPut, path: builderLibraryPath(builderTestOwner, "/items/"+made[0].ID), body: item, ifMatch: `"1"`},
		{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/collections"), body: `{"name":"X"}`},
		{method: http.MethodPut, path: builderLibraryPath(builderTestOwner, "/collections/x"), body: `{"name":"X"}`, ifMatch: `"1"`},
		{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/delete"), body: `{"templates":["server"]}`},
	} {
		recorder := harness.library(request.method, request.path, builderTestOwner, request.body, request.ifMatch)

		if recorder.Code != http.StatusConflict || builderMessage(t, recorder) != message {
			t.Errorf("%s %s on a damaged library = %d %s, want 409 %q", request.method, request.path, recorder.Code, recorder.Body, message)
		}

		if strings.Contains(recorder.Body.String(), builderTemplateSecret) {
			t.Errorf("%s %s repeats what the record holds: %s", request.method, request.path, recorder.Body)
		}
	}

	if after, err := harness.store.GetRecord(bapi.NamespaceTemplates, key); err != nil || string(after.Value) != value {
		t.Fatalf("a damaged library was written: %v", err)
	}

	// Another user's library is not affected.
	if list := harness.templates(builderTestPeer); list.Damaged || len(list.Templates) != len(builderBuiltinIDs) {
		t.Fatalf("another user's listing = %+v", list)
	}
}

// TestBuilderTemplateSharesAndPublication asserts the listing reports who
// an item of the caller's library is shared with and that it is published,
// as the record says, and marks a share whose account is gone.
func TestBuilderTemplateSharesAndPublication(t *testing.T) {
	harness := newBuilderHarness(t)

	harness.setUser(builderTestPeer, builderShareCreated)
	harness.setUser(builderShareCarol, builderShareRecreated)

	made := harness.addTemplates(builderTestOwner, "PLC", "HMI")
	granted := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)

	// Bob has the account the share was made for; carol's was made again
	// under the same name; dave has none.
	shares := []bapi.TemplateShare{
		{User: builderTestPeer, UserCreated: builderShareCreated, GrantedAt: granted},
		{User: builderShareCarol, UserCreated: builderShareCreated, GrantedAt: granted},
		{User: builderShareDave, UserCreated: builderShareCreated, GrantedAt: granted},
	}

	var collection string

	if _, err := harness.service.UpdateLibrary(context.Background(), builderTestOwner, builderTestOwner,
		func(library *bapi.TemplateLibrary) error {
			template := library.Template(made[0].ID)
			template.Shares = slices.Clone(shares)
			template.Public = &bapi.TemplatePublished{At: granted, By: builderTestOwner}

			var err error

			collection, err = library.AddCollection(
				bapi.CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{made[1].ID}}, harness.service.NewID,
			)
			if err != nil {
				return err
			}

			library.Collection(collection).Shares = slices.Clone(shares[:2])
			library.Collection(collection).Public = &bapi.TemplatePublished{At: granted, By: builderTestPeer}

			return nil
		}); err != nil {
		t.Fatalf("sharing in the record: %v", err)
	}

	harness.configGets = 0

	list := harness.templates(builderTestOwner)

	want := []builderTemplateShareResponse{
		{User: builderTestPeer, GrantedAt: granted, Stale: false},
		{User: builderShareCarol, GrantedAt: granted, Stale: true},
		{User: builderShareDave, GrantedAt: granted, Stale: true},
	}

	shared := list.Templates[len(builderBuiltinIDs)]

	if !slices.Equal(shared.Shares, want) || !shared.ServerWide || !shared.PublishedAt.Equal(granted) ||
		shared.PublishedBy != builderTestOwner || shared.Version != 1 || shared.ETag != `"1"` {
		t.Fatalf("the shared template = %+v, want the shares %+v", shared, want)
	}

	// Sharing is not content: the template's tag did not move.
	plain := list.Templates[len(builderBuiltinIDs)+1]

	if len(plain.Shares) != 0 || plain.Shares == nil || plain.ServerWide || !plain.PublishedAt.IsZero() ||
		!slices.Equal(plain.Collections, []string{collection}) {
		t.Fatalf("the other template = %+v", plain)
	}

	if got := list.Collections[0]; !slices.Equal(got.Shares, want[:2]) || !got.ServerWide || got.PublishedBy != builderTestPeer {
		t.Fatalf("the shared collection = %+v", got)
	}

	// Each account is read once, however many items name it, and the
	// caller's own once, to know whether it may share.
	if harness.configGets != 4 {
		t.Errorf("the listing read %d accounts, want 4", harness.configGets)
	}

	// Neither sharing nor publishing is offered: alice has no account, and
	// her role cannot publish to every user.
	if list.CanShare || list.CanPublish {
		t.Errorf("canShare = %v, canPublish = %v, want both false", list.CanShare, list.CanPublish)
	}

	// Replacing the template keeps who it is shared with, and says so.
	content := builderJSON(t, builderTemplateContentOf("PLC two", ""))

	replaced := harness.put("/items/"+made[0].ID, content, `"1"`)

	var template builderTemplateResponse

	harness.decode(replaced, &template)

	if replaced.Code != http.StatusOK || !slices.Equal(template.Shares, want) || !template.ServerWide ||
		strings.Contains(replaced.Body.String(), "userCreated") {
		t.Fatalf("replacing a shared template = %d %s", replaced.Code, replaced.Body)
	}

	// An account that cannot be read fails the listing rather than marking
	// the share one way or the other.
	broken := errors.New("the config store is down")
	harness.router, harness.api = newBuilderRouter()

	if err := registerBuilderRoutes(
		harness.api,
		withBuilderService(harness.service),
		withBuilderConfigs(harness.listConfigs, func(string) (*store.Config, error) { return nil, broken }),
	); err != nil {
		t.Fatalf("registerBuilderRoutes returned error: %v", err)
	}

	failed := harness.library(http.MethodGet, builderTemplatesRoute, builderTestOwner, "", "")
	if failed.Code != http.StatusInternalServerError || builderMessage(t, failed) != "unable to list the templates" {
		t.Fatalf("a listing that cannot read an account = %d %s", failed.Code, failed.Body)
	}
}

// TestBuilderTemplateOutOfSpace asserts a change etcd refused for lack of
// space says so plainly, on every write route.
func TestBuilderTemplateOutOfSpace(t *testing.T) {
	harness := newBuilderHarness(t)
	made := harness.addTemplates(builderTestOwner, "PLC")

	var collection string

	if _, err := harness.service.UpdateLibrary(context.Background(), builderTestOwner, builderTestOwner,
		func(library *bapi.TemplateLibrary) error {
			var err error

			collection, err = library.AddCollection(
				bapi.CollectionContent{Name: "Floor", Description: "", TemplateIDs: nil},
				harness.service.NewID,
			)

			return err
		}); err != nil {
		t.Fatalf("adding the collection: %v", err)
	}

	item := builderJSON(t, builderTemplateContentOf("Changed", ""))
	items := builderJSON(t, builderTemplateCreateRequest{
		Templates: []builderTemplateContent{builderTemplateContentOf("New", "")}, Collection: nil, Icons: nil,
	})

	requests := []builderRequest{
		{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/items"), body: items},
		{method: http.MethodPut, path: builderLibraryPath(builderTestOwner, "/items/"+made[0].ID), body: item, ifMatch: `"1"`},
		{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/collections"), body: `{"name":"X"}`},
		{
			method:  http.MethodPut,
			path:    builderLibraryPath(builderTestOwner, "/collections/"+collection),
			body:    `{"name":"X"}`,
			ifMatch: `"1"`,
		},
		{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/delete"), body: `{"templates":["server"]}`},
	}

	// What the Etcd store returns once etcd is out of space.
	harness.store.BeforeUpdate = func(namespace, key string) error {
		return fmt.Errorf("updating record %s/%s in Etcd: %w", namespace, key, store.ErrNoSpace)
	}

	for _, request := range requests {
		recorder := harness.library(request.method, request.path, builderTestOwner, request.body, request.ifMatch)

		if recorder.Code != http.StatusInsufficientStorage || builderMessage(t, recorder) != store.ErrNoSpace.Error() {
			t.Errorf(
				"%s %s out of space = %d %s, want 507 and the store's message",
				request.method,
				request.path,
				recorder.Code,
				recorder.Body,
			)
		}
	}

	// The first write of a library creates its record.
	harness.store.BeforeCreate = harness.store.BeforeUpdate

	first := harness.library(http.MethodPost, builderLibraryPath(builderTestPeer, "/items"), builderTestPeer, items, "")
	if first.Code != http.StatusInsufficientStorage {
		t.Errorf("a first write out of space = %d %s, want 507", first.Code, first.Body)
	}

	harness.store.BeforeUpdate, harness.store.BeforeCreate = nil, nil

	for _, request := range requests {
		recorder := harness.library(request.method, request.path, builderTestOwner, request.body, request.ifMatch)
		if recorder.Code/100 != 2 {
			t.Errorf("%s %s after freeing space = %d %s", request.method, request.path, recorder.Code, recorder.Body)
		}
	}
}

// TestBuilderTemplateBusy asserts a library that keeps changing under a
// write is answered 503, with when to try again.
func TestBuilderTemplateBusy(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.addTemplates(builderTestOwner, "PLC")

	racing := false
	round := 0

	// Another request of the owner lands before every write of this one.
	harness.store.BeforeUpdate = func(string, string) error {
		if racing {
			return nil
		}

		racing = true
		defer func() { racing = false }()

		round++

		_, err := harness.service.UpdateLibrary(context.Background(), builderTestOwner, builderTestOwner,
			func(library *bapi.TemplateLibrary) error {
				_, err := library.AddCollection(
					bapi.CollectionContent{Name: "Racer " + strconv.Itoa(round), Description: "", TemplateIDs: nil}, harness.service.NewID,
				)

				return err
			})

		return err
	}

	recorder := harness.post("/delete", `{"templates":["server"]}`)

	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != "1" ||
		builderMessage(t, recorder) != "unable to delete the templates" || round != 5 {
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
	if got := builderTemplateIDs(harness.templates(builderTestOwner)); !slices.Contains(got, "server") {
		t.Fatalf("a request that gave up deleted the template: %q", got)
	}

	if again := harness.post("/delete", `{"templates":["server"]}`); again.Code != http.StatusOK {
		t.Fatalf("trying again = %d %s", again.Code, again.Body)
	}
}

// TestBuilderTemplateRacingReplace asserts a replacement whose entity tag
// went stale between its read and its write is refused, not applied over
// the other change.
func TestBuilderTemplateRacingReplace(t *testing.T) {
	harness := newBuilderHarness(t)
	made := harness.addTemplates(builderTestOwner, "PLC")
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

	if list := harness.templates(builderTestOwner); list.Templates[len(builderBuiltinIDs)].Name != "Winner" {
		t.Fatalf("the template = %+v, want the winner's", list.Templates[len(builderBuiltinIDs)])
	}
}
