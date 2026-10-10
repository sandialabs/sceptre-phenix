package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
)

// shareTemplates sends a share request of the user's own library, as that
// user.
func (h *builderHarness) shareTemplates(user string, request builderTemplateShareRequest) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.library(http.MethodPost, builderLibraryPath(user, "/share"), user, builderJSON(h.t, request), "")
}

// mustShareTemplates shares, and fails the test unless every item took it.
func (h *builderHarness) mustShareTemplates(user string, request builderTemplateShareRequest) {
	h.t.Helper()

	recorder := h.shareTemplates(user, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"failed":[]}` {
		h.t.Fatalf("sharing as %s = %d %s, want 200 and no failure", user, recorder.Code, recorder.Body)
	}
}

// publishTemplates sends a publish request of owner's library, as user with
// the given role.
func (h *builderHarness) publishTemplates(owner, user string, role *rbac.Role, body string) *httptest.ResponseRecorder {
	h.t.Helper()

	recorder := h.do(builderRequest{
		method: http.MethodPost, path: builderLibraryPath(owner, "/publish"), body: body, user: user, role: role,
	})

	assertTemplateHeaders(h.t, "publish", recorder)

	return recorder
}

// mustPublishTemplates publishes templates, or takes them back, and fails
// the test unless every item took it.
func (h *builderHarness) mustPublishTemplates(owner, user string, role *rbac.Role, templates []string, serverWide bool) {
	h.t.Helper()

	body := builderJSON(h.t, map[string]any{"templates": templates, "serverWide": serverWide})

	recorder := h.publishTemplates(owner, user, role, body)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"failed":[]}` {
		h.t.Fatalf("publishing as %s = %d %s, want 200 and no failure", user, recorder.Code, recorder.Body)
	}
}

// listedTemplate returns the listed template with the given ID, or nil.
func listedTemplate(list builderTemplateLibraryResponse, id string) *builderTemplateResponse {
	for i := range list.Templates {
		if list.Templates[i].ID == id {
			return &list.Templates[i]
		}
	}

	return nil
}

// listedCollection returns the listed collection with the given ID, or nil.
func listedCollection(list builderTemplateLibraryResponse, id string) *builderTemplateCollectionResponse {
	for i := range list.Collections {
		if list.Collections[i].ID == id {
			return &list.Collections[i]
		}
	}

	return nil
}

// othersItems returns the listed items of other owners as
// "owner:source:id", templates then collections.
func othersItems(list builderTemplateLibraryResponse) []string {
	var items []string

	for _, template := range list.Templates {
		if template.Source != builderTemplateOwn {
			items = append(items, template.Owner+":"+template.Source+":"+template.ID)
		}
	}

	for _, collection := range list.Collections {
		if collection.Source != builderTemplateOwn {
			items = append(items, collection.Owner+":"+collection.Source+":collection "+collection.ID)
		}
	}

	return items
}

// builderSharedFixture is a library of alice's (the template fixture: PLC,
// with a custom icon, HMI and the collection "Plant floor" of the two) in a
// harness where alice, bob, carol, dave and erin all have accounts.
type builderSharedFixture struct {
	*builderTemplateFixture
}

func newBuilderSharedFixture(t *testing.T) *builderSharedFixture {
	t.Helper()

	fixture := &builderSharedFixture{builderTemplateFixture: newBuilderTemplateFixture(t)}

	for _, user := range []string{builderTestOwner, builderTestPeer, builderShareCarol, builderShareDave, builderShareErin} {
		fixture.harness.setUser(user, builderShareCreated)
	}

	return fixture
}

// revision returns the revision of alice's library record.
func (f *builderSharedFixture) revision(t *testing.T) int64 {
	t.Helper()

	record, err := f.harness.store.GetRecord(bapi.NamespaceTemplates, bapi.LibraryKey(builderTestOwner))
	if err != nil {
		t.Fatalf("reading the library record: %v", err)
	}

	return record.Revision
}

// TestBuilderTemplateShare asserts the owner shares a template with one
// user and a collection with another, and each recipient lists what it was
// given, read only and as it is now, naming the icon it names.
func TestBuilderTemplateShare(t *testing.T) {
	fixture := newBuilderSharedFixture(t)
	harness := fixture.harness

	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: nil, Add: []string{builderTestPeer}, Remove: nil,
	})
	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: nil, Collections: []string{fixture.floor}, Add: []string{builderShareCarol}, Remove: nil,
	})

	// Each recipient has a hint naming alice's library.
	for _, recipient := range []string{builderTestPeer, builderShareCarol} {
		key := "in/" + bapi.OwnerScope(recipient) + "/" + bapi.OwnerScope(builderTestOwner)

		if _, err := harness.store.GetRecord(bapi.NamespaceTemplates, key); err != nil {
			t.Fatalf("the hint for %s: %v", recipient, err)
		}
	}

	// Bob sees PLC alone, as alice's, with its icon, and not who else has it.
	bob := harness.templates(builderTestPeer)

	if got, want := othersItems(bob), []string{"alice:shared:" + fixture.plc}; !slices.Equal(got, want) {
		t.Fatalf("bob's listing holds %q, want %q", got, want)
	}

	plc := listedTemplate(bob, fixture.plc)

	if plc.Name != "PLC" || plc.Shares != nil || len(plc.Collections) != 0 || plc.Collections == nil || plc.ServerWide ||
		plc.Device.Icon != builderTemplateIconName || !bob.CanShare || bob.CanPublish {
		t.Fatalf("bob's view of PLC = %+v, canShare %v", plc, bob.CanShare)
	}

	// Carol sees the collection and the templates in it, which name it.
	carol := harness.templates(builderShareCarol)

	want := []string{
		"alice:shared:" + fixture.plc, "alice:shared:" + fixture.hmi, "alice:shared:collection " + fixture.floor,
	}

	if got := othersItems(carol); !slices.Equal(got, want) {
		t.Fatalf("carol's listing holds %q, want %q", got, want)
	}

	if hmi := listedTemplate(carol, fixture.hmi); !slices.Equal(hmi.Collections, []string{fixture.floor}) {
		t.Fatalf("carol's view of HMI = %+v", hmi)
	}

	if floor := listedCollection(carol, fixture.floor); floor.Shares != nil ||
		!slices.Equal(floor.TemplateIDs, []string{fixture.plc, fixture.hmi}) {
		t.Fatalf("carol's view of the collection = %+v", floor)
	}

	// The response never names the account a share is bound to, nor, to a
	// recipient, who else it is shared with.
	recorder := harness.library(http.MethodGet, builderTemplatesRoute, builderShareCarol, "", "")
	if body := recorder.Body.String(); strings.Contains(body, "userCreated") || strings.Contains(body, builderTestPeer) {
		t.Fatalf("carol's listing names the shares: %s", body)
	}

	// Alice sees whom she shared with.
	if shares := listedTemplate(harness.templates(builderTestOwner), fixture.plc).Shares; len(shares) != 1 ||
		shares[0].User != builderTestPeer || shares[0].Stale || shares[0].GrantedAt.IsZero() {
		t.Fatalf("alice's view of PLC's shares = %+v", shares)
	}

	// A change by the owner is what the recipient sees next.
	edited := builderJSON(t, builderTemplateUpdateRequest{
		builderTemplateContent: builderTemplateContentOf("PLC two", builderTemplateIconName),
	})
	if replaced := harness.put("/items/"+fixture.plc, edited, `"1"`); replaced.Code != http.StatusOK {
		t.Fatalf("replacing PLC = %d %s", replaced.Code, replaced.Body)
	}

	if plc := listedTemplate(harness.templates(builderTestPeer), fixture.plc); plc.Name != "PLC two" || plc.Version != 2 {
		t.Fatalf("bob's view of PLC after alice's edit = %+v", plc)
	}
}

// TestBuilderTemplateSharedIsReadOnly asserts nothing a recipient sends
// changes the owner's library, and that the recipient can copy what it was
// given into its own.
func TestBuilderTemplateSharedIsReadOnly(t *testing.T) {
	fixture := newBuilderSharedFixture(t)
	harness := fixture.harness

	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: []string{fixture.floor}, Add: []string{builderTestPeer}, Remove: nil,
	})

	before := fixture.revision(t)
	edited := builderJSON(t, builderTemplateContentOf("Taken over", ""))
	plc := `{"templates":["` + fixture.plc + `"]`

	for _, request := range []builderRequest{
		{method: http.MethodPut, path: builderLibraryPath(builderTestOwner, "/items/"+fixture.plc), body: edited, ifMatch: `"1"`},
		{method: http.MethodPut, path: builderLibraryPath(builderTestOwner, "/collections/"+fixture.floor), body: `{"name":"X"}`, ifMatch: `"1"`},
		{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/delete"), body: plc + `}`},
		{method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/share"), body: plc + `,"add":["` + builderShareDave + `"]}`},
		{
			method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/publish"),
			body: plc + `,"serverWide":true}`, role: builderPublisherRole(),
		},
	} {
		request.user = builderTestPeer

		if recorder := harness.do(request); recorder.Code != http.StatusNotFound {
			t.Errorf("%s %s as bob = %d %s, want 404", request.method, request.path, recorder.Code, recorder.Body)
		}
	}

	// Taking an item back from every user needs the permission to publish.
	refused := harness.publishTemplates(builderTestOwner, builderTestPeer, nil, plc+`,"serverWide":false}`)
	if refused.Code != http.StatusForbidden {
		t.Errorf("bob taking alice's PLC back = %d %s, want 403", refused.Code, refused.Body)
	}

	if fixture.revision(t) != before {
		t.Fatal("a request of a recipient changed the owner's library")
	}

	// Bob copies PLC into his own library, naming its icon: the copy is his.
	bob := harness.templates(builderTestPeer)
	shared := listedTemplate(bob, fixture.plc)

	copied := builderJSON(t, builderTemplateCreateRequest{
		Templates:  []builderTemplateContent{{Name: shared.Name, Description: shared.Description, Device: shared.Device}},
		Collection: nil,
	})

	recorder := harness.library(http.MethodPost, builderLibraryPath(builderTestPeer, "/items"), builderTestPeer, copied, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("bob copying PLC = %d %s", recorder.Code, recorder.Body)
	}

	var made builderTemplateCreateResponse

	harness.decode(recorder, &made)

	if own := listedTemplate(harness.templates(builderTestPeer), made.Created[0].ID); own == nil || own.Source != builderTemplateOwn ||
		own.Owner != builderTestPeer || own.Device.Icon != builderTemplateIconName {
		t.Fatalf("bob's copy = %+v", own)
	}
}

// TestBuilderTemplateShareIsAChange asserts a share request changes each
// list rather than replacing it: repeating it writes nothing, removing a
// user takes the item from that user alone, and only a change that changed
// something is logged.
func TestBuilderTemplateShareIsAChange(t *testing.T) {
	fixture := newBuilderSharedFixture(t)
	harness := fixture.harness

	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: nil, Add: []string{builderTestPeer, builderShareCarol}, Remove: nil,
	})

	before := fixture.revision(t)

	// Sharing again, and removing someone it is not shared with, writes
	// nothing.
	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: nil, Add: []string{builderTestPeer}, Remove: []string{builderShareErin},
	})

	if fixture.revision(t) != before {
		t.Fatal("a share that changes nothing wrote the library")
	}

	// Removing bob takes PLC from him, not from carol; the hint stays.
	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: nil, Add: nil, Remove: []string{builderTestPeer},
	})

	if got := othersItems(harness.templates(builderTestPeer)); len(got) != 0 {
		t.Fatalf("after removing bob his listing holds %q", got)
	}

	if got := othersItems(harness.templates(builderShareCarol)); !slices.Equal(got, []string{"alice:shared:" + fixture.plc}) {
		t.Fatalf("after removing bob carol's listing holds %q", got)
	}

	hint := "in/" + bapi.OwnerScope(builderTestPeer) + "/" + bapi.OwnerScope(builderTestOwner)
	if _, err := harness.store.GetRecord(bapi.NamespaceTemplates, hint); err != nil {
		t.Fatalf("removing bob removed his hint: %v", err)
	}

	fixture.assertLogged(t, "builder template sharing changed")

	records := fixture.logs.Records(t, plogtest.Message("builder template sharing changed"))
	if len(records) != 2 {
		t.Fatalf("%d share changes are logged, want 2: those that changed something", len(records))
	}

	if got := records[0]; got["owner"] != builderTestOwner || got["templates"] != fixture.plc || got["added"] != "bob,carol" ||
		got["removed"] != "" {
		t.Errorf("the first share is logged as %v", got)
	}
}

// TestBuilderTemplateShareRefusals asserts each malformed or refused share
// request is answered with what is wrong, and that none of them writes the
// library or a hint.
func TestBuilderTemplateShareRefusals(t *testing.T) {
	fixture := newBuilderSharedFixture(t)
	harness := fixture.harness

	harness.setUser("no-created", "")

	many := make([]string, 0, bapi.MaxShares+1)
	for i := range bapi.MaxShares + 1 {
		many = append(many, fmt.Sprintf("user-%02d", i))
	}

	share := func(add, remove []string) string {
		return builderJSON(t, builderTemplateShareRequest{
			Templates: []string{fixture.plc}, Collections: nil, Add: add, Remove: remove,
		})
	}

	unprocessable := func(errs ...builderShareError) string {
		return builderJSON(t, builderShareErrorsResponse{
			Message: "Some people could not be added.", Cause: "", Code: string(bdoc.CodeShareUsersRefused), Errors: errs,
		})
	}

	tests := []struct {
		name   string
		user   string
		owner  string
		role   *rbac.Role
		body   string
		status int
		// want is the whole body of a 422, or the message of another status.
		want string
	}{
		{
			name: "unknown user", body: share([]string{"zed", builderTestPeer}, nil), status: http.StatusUnprocessableEntity,
			want: unprocessable(builderShareError{User: "zed", Reason: builderShareUnknownUser}),
		},
		{
			name: "account without a creation time", body: share([]string{"no-created"}, nil), status: http.StatusUnprocessableEntity,
			want: unprocessable(builderShareError{User: "no-created", Reason: builderShareUnknownUser}),
		},
		{
			name: "the owner", body: share([]string{builderTestOwner}, nil), status: http.StatusUnprocessableEntity,
			want: unprocessable(builderShareError{User: builderTestOwner, Reason: builderShareOwner}),
		},
		{
			name: "not a user name", body: share([]string{"a/b", "\u202ebob"}, []string{"c\u0007"}), status: http.StatusUnprocessableEntity,
			want: unprocessable(
				builderShareError{User: "a/b", Reason: builderShareInvalidUser},
				builderShareError{User: "\u202ebob", Reason: builderShareInvalidUser},
				builderShareError{User: "c\u0007", Reason: builderShareInvalidUser},
			),
		},
		{
			name:   "added and removed",
			body:   share([]string{builderTestPeer}, []string{builderTestPeer}),
			status: http.StatusUnprocessableEntity,
			want:   unprocessable(builderShareError{User: builderTestPeer, Reason: builderShareDuplicate}),
		},
		{
			name: "too many", body: share(many, nil), status: http.StatusUnprocessableEntity,
			want: unprocessable(builderShareError{User: "", Reason: builderShareTooMany}),
		},
		{
			name: "no item", body: `{"add":["bob"]}`, status: http.StatusBadRequest,
			want: "at least one template or collection is required",
		},
		{
			name: "no user", body: `{"templates":["` + fixture.plc + `"],"add":[],"remove":[]}`, status: http.StatusBadRequest,
			want: "at least one user to add or remove is required",
		},
		{
			name: "unknown field", body: `{"templates":["x"],"add":["bob"],"access":"edit"}`, status: http.StatusBadRequest,
			want: "request body is not a valid Builder request",
		},
		{
			name: "too large", body: `{"templates":["` + strings.Repeat("x", builderTemplateRequestBytes) + `"],"add":["bob"]}`,
			status: http.StatusRequestEntityTooLarge, want: "request body is larger than 65536 bytes",
		},
		{
			name: "another owner", user: builderTestPeer, body: share([]string{builderShareCarol}, nil),
			status: http.StatusNotFound, want: "template library alice not found",
		},
		{
			name: "no config update", role: builderShareRole([]string{"list", "get", "create", "delete"}),
			body: share([]string{builderTestPeer}, nil), status: http.StatusForbidden,
			want: "sharing builder templates not allowed for alice",
		},
	}

	before := fixture.revision(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := builderTestOwner
			if tt.user != "" {
				user = tt.user
			}

			recorder := harness.do(builderRequest{
				method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/share"), body: tt.body, user: user, role: tt.role,
			})

			assertTemplateHeaders(t, tt.name, recorder)

			got := recorder.Body.String()
			if tt.status != http.StatusUnprocessableEntity {
				got = builderMessage(t, recorder)
			}

			if recorder.Code != tt.status || got != tt.want {
				t.Fatalf("status = %d, body %s, want %d %s", recorder.Code, recorder.Body, tt.status, tt.want)
			}
		})
	}

	if fixture.revision(t) != before || harness.store.Count(bapi.NamespaceTemplates) != 1 {
		t.Fatalf("a refused share wrote %q", harness.store.Keys(bapi.NamespaceTemplates))
	}

	// Names that are no account are logged, as a possible probe.
	if got := len(fixture.logs.Records(t, plogtest.Message("builder template share names an unknown user"))); got != 2 {
		t.Errorf("%d unknown users are logged, want 2", got)
	}

	// Without an account of the caller's own, as with authentication off,
	// there is no sharing.
	harness.removeUser(builderTestOwner)

	recorder := harness.shareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: nil, Add: []string{builderTestPeer}, Remove: nil,
	})

	if want := "sharing builder templates without a user account not allowed for alice"; recorder.Code != http.StatusForbidden ||
		builderMessage(t, recorder) != want {
		t.Fatalf("sharing without an account = %d %s, want 403 %q", recorder.Code, recorder.Body, want)
	}
}

// TestBuilderTemplateShareFailures asserts an item that would be shared with
// more than 25 users keeps its list, and an ID the library does not hold is
// ignored, both named in the answer, while the other items are changed.
func TestBuilderTemplateShareFailures(t *testing.T) {
	fixture := newBuilderSharedFixture(t)
	harness := fixture.harness

	full := make([]string, 0, bapi.MaxShares)
	for i := range bapi.MaxShares {
		user := fmt.Sprintf("user-%02d", i)
		full = append(full, user)
		harness.setUser(user, builderShareCreated)
	}

	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: nil, Add: full, Remove: nil,
	})

	recorder := harness.shareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates:   []string{fixture.plc, fixture.hmi, "missing"},
		Collections: []string{fixture.floor, "gone"},
		Add:         []string{builderTestPeer},
		Remove:      nil,
	})

	want := `{"failed":[{"kind":"template","id":"` + fixture.plc + `","reason":"too-many","code":"template.shares.too-many"},` +
		`{"kind":"template","id":"missing","reason":"not-found","code":"template.item.not-found"},` +
		`{"kind":"collection","id":"gone","reason":"not-found","code":"template.item.not-found"}]}`

	if recorder.Code != http.StatusOK || recorder.Body.String() != want {
		t.Fatalf("sharing past the limit = %d %s, want 200 %s", recorder.Code, recorder.Body, want)
	}

	if got := othersItems(harness.templates(builderTestPeer)); !slices.Equal(got, []string{
		"alice:shared:" + fixture.plc, "alice:shared:" + fixture.hmi, "alice:shared:collection " + fixture.floor,
	}) {
		t.Fatalf("bob's listing holds %q: the collection and HMI, and PLC through the collection", got)
	}

	if shares := listedTemplate(harness.templates(builderTestOwner), fixture.plc).Shares; len(shares) !=
		bapi.MaxShares || slices.ContainsFunc(shares, func(share builderTemplateShareResponse) bool {
		return share.User == builderTestPeer
	}) {
		t.Fatalf("PLC is shared with %d users, bob among them", len(shares))
	}
}

// TestBuilderTemplateShareAccounts asserts a share applies to the account
// it was made for only: a recipient whose account was made again sees
// nothing, the owner sees the share as stale, and adding the user again
// binds it to the new account.
func TestBuilderTemplateShareAccounts(t *testing.T) {
	fixture := newBuilderSharedFixture(t)
	harness := fixture.harness

	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: []string{fixture.floor}, Add: []string{builderTestPeer}, Remove: nil,
	})

	harness.setUser(builderTestPeer, builderShareRecreated)

	if got := othersItems(harness.templates(builderTestPeer)); len(got) != 0 {
		t.Fatalf("a new account under bob's name sees %q", got)
	}

	alice := harness.templates(builderTestOwner)
	stale := listedTemplate(alice, fixture.plc).Shares

	if len(stale) != 1 || !stale[0].Stale || !listedCollection(alice, fixture.floor).Shares[0].Stale {
		t.Fatalf("alice's view of a share whose account was made again = %+v", stale)
	}

	// A user without an account sees nothing either.
	harness.removeUser(builderTestPeer)

	if got := othersItems(harness.templates(builderTestPeer)); len(got) != 0 {
		t.Fatalf("bob without an account sees %q", got)
	}

	harness.setUser(builderTestPeer, builderShareRecreated)

	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: nil, Add: []string{builderTestPeer}, Remove: nil,
	})

	renewed := listedTemplate(harness.templates(builderTestOwner), fixture.plc).Shares

	if len(renewed) != 1 || renewed[0].Stale || !renewed[0].GrantedAt.After(stale[0].GrantedAt) {
		t.Fatalf("after adding bob again PLC's shares = %+v", renewed)
	}

	if got := othersItems(harness.templates(builderTestPeer)); !slices.Equal(got, []string{"alice:shared:" + fixture.plc}) {
		t.Fatalf("bob's new account sees %q, want PLC alone", got)
	}
}

// TestBuilderTemplateShareCandidates asserts the candidates are every user
// with an account a share would be accepted for, other than the caller, and
// that only a caller that may share may ask.
func TestBuilderTemplateShareCandidates(t *testing.T) {
	harness := newBuilderHarness(t)

	for _, user := range []string{builderTestOwner, builderShareCarol, builderTestPeer, "\u202ebob"} {
		harness.setUser(user, builderShareCreated)
	}

	harness.setUser("no-created", "")

	for i := range harness.configs {
		if harness.configs[i].Metadata.Name == builderShareCarol {
			harness.configs[i].Spec = map[string]any{"first_name": " Carol ", "last_name": "Stone"}
		}
	}

	path := builderTemplatesRoute + "/candidates"

	recorder := harness.library(http.MethodGet, path, builderTestOwner, "", "")

	if want := `{"users":[{"username":"bob","name":""},{"username":"carol","name":"Carol Stone"}]}`; recorder.Code != http.StatusOK ||
		recorder.Body.String() != want {
		t.Fatalf("candidates = %d %s, want %s", recorder.Code, recorder.Body, want)
	}

	for name, request := range map[string]builderRequest{
		"no config update": {
			method: http.MethodGet, path: path, user: builderTestOwner, role: builderShareRole([]string{"list", "get", "create", "delete"}),
		},
		"no account":  {method: http.MethodGet, path: path, user: "global-admin"},
		"no identity": {method: http.MethodGet, path: path},
	} {
		if recorder := harness.do(request); recorder.Code != http.StatusForbidden {
			t.Errorf("candidates with %s = %d %s, want 403", name, recorder.Code, recorder.Body)
		}
	}
}

// TestBuilderTemplatePublish asserts who may publish items to every user
// and take them back, that everyone then lists them, as the items
// themselves or through a collection, and that each change is logged.
func TestBuilderTemplatePublish(t *testing.T) {
	fixture := newBuilderSharedFixture(t)
	harness := fixture.harness
	publisher := builderPublisherRole()

	body := func(serverWide bool, templates, collections []string) string {
		return builderJSON(t, map[string]any{"templates": templates, "collections": collections, "serverWide": serverWide})
	}

	before := fixture.revision(t)

	// The owner needs the permission to publish; anyone else may not know
	// whether the library exists.
	for name, request := range map[string]struct {
		user   string
		role   *rbac.Role
		status int
	}{
		"the owner without the permission":    {user: builderTestOwner, role: builderConfigsRole(), status: http.StatusForbidden},
		"the owner without config update":     {user: builderTestOwner, role: builderShareRole([]string{"list"}), status: http.StatusForbidden},
		"another user with the permission":    {user: builderTestPeer, role: publisher, status: http.StatusNotFound},
		"another user without the permission": {user: builderTestPeer, role: builderConfigsRole(), status: http.StatusNotFound},
		"another user without anything":       {user: builderTestPeer, role: builderShareRole(nil), status: http.StatusForbidden},
		"the owner with only the permission":  {user: builderTestOwner, role: publisherOnly(), status: http.StatusForbidden},
	} {
		recorder := harness.publishTemplates(builderTestOwner, request.user, request.role, body(true, []string{fixture.plc}, nil))
		if recorder.Code != request.status {
			t.Errorf("publishing as %s = %d %s, want %d", name, recorder.Code, recorder.Body, request.status)
		}
	}

	if fixture.revision(t) != before || harness.store.Count(bapi.NamespaceTemplates) != 1 {
		t.Fatalf("a refused publication wrote %q", harness.store.Keys(bapi.NamespaceTemplates))
	}

	recorder := harness.publishTemplates(
		builderTestOwner, builderTestOwner, publisher, body(true, []string{fixture.plc, "missing"}, []string{fixture.floor}),
	)

	if want := `{"failed":[{"kind":"template","id":"missing","reason":"not-found","code":"template.item.not-found"}]}`; recorder.Code != http.StatusOK ||
		recorder.Body.String() != want {
		t.Fatalf("publishing = %d %s, want 200 %s", recorder.Code, recorder.Body, want)
	}

	if _, err := harness.store.GetRecord(bapi.NamespaceTemplates, "pub/"+bapi.OwnerScope(builderTestOwner)); err != nil {
		t.Fatalf("publishing wrote no hint: %v", err)
	}

	// Anyone with config list sees the items, also without an account.
	harness.removeUser(builderShareDave)

	for _, user := range []string{builderShareCarol, builderShareDave} {
		list := harness.templates(user)

		want := []string{
			"alice:server:" + fixture.plc, "alice:server:" + fixture.hmi, "alice:server:collection " + fixture.floor,
		}

		if got := othersItems(list); !slices.Equal(got, want) {
			t.Fatalf("%s's listing holds %q, want %q", user, got, want)
		}

		plc, hmi := listedTemplate(list, fixture.plc), listedTemplate(list, fixture.hmi)

		if !plc.ServerWide || plc.PublishedBy != builderTestOwner || plc.PublishedAt.IsZero() || hmi.ServerWide ||
			!listedCollection(list, fixture.floor).ServerWide || plc.Device.Icon != builderTemplateIconName {
			t.Fatalf("%s's view of PLC = %+v and of HMI = %+v", user, plc, hmi)
		}
	}

	// An item shared with a user and published is listed to the user as
	// shared.
	harness.mustShareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{fixture.hmi}, Collections: nil, Add: []string{builderShareCarol}, Remove: nil,
	})

	if hmi := listedTemplate(harness.templates(builderShareCarol), fixture.hmi); hmi.Source != "shared" {
		t.Fatalf("carol's view of HMI, shared and published = %+v", hmi)
	}

	// Publishing again keeps who published it and when.
	published := listedTemplate(harness.templates(builderTestOwner), fixture.plc)
	revision := fixture.revision(t)

	harness.mustPublishTemplates(builderTestOwner, builderTestOwner, publisher, []string{fixture.plc}, true)

	if fixture.revision(t) != revision {
		t.Fatal("publishing a published item wrote the library")
	}

	// Anyone with the permission takes any user's item back.
	harness.mustPublishTemplates(builderTestOwner, builderTestPeer, publisher, []string{fixture.plc}, false)

	if got := othersItems(harness.templates(builderShareDave)); !slices.Equal(got, []string{
		"alice:server:" + fixture.plc, "alice:server:" + fixture.hmi, "alice:server:collection " + fixture.floor,
	}) {
		t.Fatalf("after PLC alone was taken back, dave lists %q: PLC stays through the collection", got)
	}

	// The owner takes her own back with config update alone.
	recorder = harness.publishTemplates(builderTestOwner, builderTestOwner, builderConfigsRole(), body(false, nil, []string{fixture.floor}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("alice taking her collection back = %d %s", recorder.Code, recorder.Body)
	}

	if got := othersItems(harness.templates(builderShareDave)); len(got) != 0 {
		t.Fatalf("after everything was taken back dave lists %q", got)
	}

	if published.PublishedBy != builderTestOwner {
		t.Errorf("PLC was published by %q, want alice", published.PublishedBy)
	}

	fixture.assertLogged(t, "builder template publication changed")

	records := fixture.logs.Records(t, plogtest.Message("builder template publication changed"))
	if len(records) != 3 {
		t.Fatalf("%d publication changes are logged, want 3", len(records))
	}

	if got := records[1]; got["user"] != builderTestPeer || got["owner"] != builderTestOwner {
		t.Errorf("taking alice's item back is logged as %v", got)
	}
}

// TestBuilderTemplatePublishRequests asserts what is wrong with a publish
// request is said before anything is written, and that publishing needs no
// account, so it works with authentication off.
func TestBuilderTemplatePublishRequests(t *testing.T) {
	harness := newBuilderHarness(t)
	publisher := builderPublisherRole()
	publish := builderLibraryPath(builderTestOwner, "/publish")

	for _, tt := range []struct {
		owner, body string
		status      int
		message     string
	}{
		{owner: builderTestOwner, body: `{"templates":["server"]}`, status: http.StatusBadRequest, message: "serverWide is required"},
		{
			owner: builderTestOwner, body: `{"serverWide":true}`, status: http.StatusBadRequest,
			message: "at least one template or collection is required",
		},
		{
			owner: builderTestOwner, body: `{"templates":["server"],"serverWide":true,"add":["bob"]}`, status: http.StatusBadRequest,
			message: "request body is not a valid Builder request",
		},
		{
			owner: builderTestOwner, body: `{"templates":["server"],"serverWide":true}{}`, status: http.StatusBadRequest,
			message: "request body carries more than one JSON value",
		},
		{
			owner: "\u202eeve", body: `{"templates":["server"],"serverWide":false}`, status: http.StatusNotFound,
			message: "template library \u202eeve not found",
		},
	} {
		path := publish
		if tt.owner != builderTestOwner {
			path = builderLibraryPath(tt.owner, "/publish")
		}

		recorder := harness.do(builderRequest{method: http.MethodPost, path: path, body: tt.body, user: builderTestOwner, role: publisher})

		if recorder.Code != tt.status || builderMessage(t, recorder) != tt.message {
			t.Errorf("%s = %d %s, want %d %q", tt.body, recorder.Code, recorder.Body, tt.status, tt.message)
		}
	}

	if harness.store.Count(bapi.NamespaceTemplates) != 0 {
		t.Fatalf("a refused request wrote %q", harness.store.Keys(bapi.NamespaceTemplates))
	}

	// The one user of authentication off has no account, and publishes.
	harness.mustPublishTemplates("global-admin", "global-admin", publisher, []string{"server"}, true)

	list := harness.templates(builderTestPeer)

	if got := othersItems(list); !slices.Equal(got, []string{"global-admin:server:server"}) {
		t.Fatalf("bob's listing holds %q", got)
	}

	// Taking an item back from a library that has no record changes
	// nothing.
	recorder := harness.publishTemplates(builderShareDave, "global-admin", publisher, `{"templates":["server"],"serverWide":false}`)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"failed":[]}` || harness.store.Count(bapi.NamespaceTemplates) != 2 {
		t.Fatalf("taking back from a library that was never written = %d %s", recorder.Code, recorder.Body)
	}
}

// TestBuilderTemplateFlags asserts canShare and canPublish say what the
// caller may do: share with config update and an account, publish with
// config update and builder-templates publish.
func TestBuilderTemplateFlags(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.setUser(builderTestOwner, builderShareCreated)

	withPublish := func(verbs ...string) *rbac.Role {
		role := builderRole(
			builderPolicy([]string{"configs"}, []string{"*", "*/*"}, verbs),
			builderPolicy([]string{"builder-templates"}, nil, []string{"publish"}),
		)

		return &role
	}

	for _, tt := range []struct {
		name                 string
		user                 string
		role                 *rbac.Role
		canShare, canPublish bool
	}{
		{name: "every config verb", user: builderTestOwner, role: builderConfigsRole(), canShare: true},
		{name: "list only", user: builderTestOwner, role: builderShareRole([]string{"list"})},
		{name: "publish and update", user: builderTestOwner, role: withPublish("list", "update"), canShare: true, canPublish: true},
		{name: "publish without update", user: builderTestOwner, role: withPublish("list")},
		{name: "global admin", user: builderTestOwner, role: builderPublisherRole(), canShare: true, canPublish: true},
		{name: "no account", user: "global-admin", role: builderPublisherRole(), canPublish: true},
	} {
		recorder := harness.do(builderRequest{method: http.MethodGet, path: builderTemplatesRoute, user: tt.user, role: tt.role})

		var list builderTemplateLibraryResponse

		harness.decode(recorder, &list)

		if recorder.Code != http.StatusOK || list.CanShare != tt.canShare || list.CanPublish != tt.canPublish {
			t.Errorf("%s: %d canShare %v canPublish %v, want %v %v", tt.name, recorder.Code, list.CanShare, list.CanPublish,
				tt.canShare, tt.canPublish)
		}
	}
}

// TestBuilderTemplateListingSkipsUnreadableLibraries asserts another user's
// library that is gone or cannot be read is left out of the listing, which
// still lists the rest.
func TestBuilderTemplateListingSkipsUnreadableLibraries(t *testing.T) {
	harness := newBuilderHarness(t)
	logs := plogtest.Capture(t)

	for _, user := range []string{builderTestOwner, builderTestPeer, builderShareCarol} {
		harness.setUser(user, builderShareCreated)
	}

	bob := harness.addTemplates(builderTestPeer, "Of bob")
	carol := harness.addTemplates(builderShareCarol, "Of carol")

	for user, id := range map[string]string{builderTestPeer: bob[0].ID, builderShareCarol: carol[0].ID} {
		harness.mustShareTemplates(user, builderTemplateShareRequest{
			Templates: []string{id}, Collections: nil, Add: []string{builderTestOwner}, Remove: nil,
		})
	}

	// Bob's record is one a later phenix wrote; dave's hint names a
	// library that is not there.
	key := bapi.LibraryKey(builderTestPeer)

	record, err := harness.store.GetRecord(bapi.NamespaceTemplates, key)
	if err != nil {
		t.Fatalf("reading bob's library: %v", err)
	}

	harness.store.SetValue(bapi.NamespaceTemplates, key, []byte(strings.Replace(string(record.Value), `{"owner"`, `{"later":1,"owner"`, 1)))

	if err := harness.service.NoteShared(context.Background(), builderShareDave, []string{builderTestOwner}); err != nil {
		t.Fatalf("NoteShared returned error: %v", err)
	}

	if got := othersItems(harness.templates(builderTestOwner)); !slices.Equal(got, []string{"carol:shared:" + carol[0].ID}) {
		t.Fatalf("the listing holds %q, want carol's template alone", got)
	}

	if len(logs.Records(t, plogtest.Message("skipping a builder template library that is unreadable"))) != 1 {
		t.Errorf("the unreadable library is not logged: %s", logs)
	}
}

// TestBuilderTemplatesPublishPermissionIsKnown asserts the generated list of
// the permissions phenix checks names the one to publish templates, so a
// role editor offers it.
func TestBuilderTemplatesPublishPermissionIsKnown(t *testing.T) {
	if !slices.Contains(rbac.Permissions, rbac.Permission{Resource: "builder-templates", Verb: "publish"}) {
		t.Fatal("web/rbac/known_policy.go does not hold builder-templates publish: run make generate")
	}

	// The check is the role's: the role of global-admin holds it, an
	// editor's does not.
	if !builderTemplatesPublishAllowed(*builderPublisherRole()) || builderTemplatesPublishAllowed(*builderConfigsRole()) {
		t.Fatal("builderTemplatesPublishAllowed does not follow the role")
	}
}

// TestBuilderTemplateShareAndPublishFailures asserts a share or a
// publication of a library this server cannot read is refused with 409, one
// etcd has no room for with 507, and one of a library that keeps changing
// with 503.
func TestBuilderTemplateShareAndPublishFailures(t *testing.T) {
	fixture := newBuilderSharedFixture(t)
	harness := fixture.harness
	publisher := builderPublisherRole()

	share := builderJSON(t, builderTemplateShareRequest{
		Templates: []string{fixture.plc}, Collections: nil, Add: []string{builderTestPeer}, Remove: nil,
	})
	publish := `{"templates":["` + fixture.plc + `"],"serverWide":true}`

	send := func() (*httptest.ResponseRecorder, *httptest.ResponseRecorder) {
		return harness.library(http.MethodPost, builderLibraryPath(builderTestOwner, "/share"), builderTestOwner, share, ""),
			harness.publishTemplates(builderTestOwner, builderTestOwner, publisher, publish)
	}

	// What the Etcd store returns once etcd is out of space: for the write
	// of the library, and for the write of a hint, which goes first.
	full := func(namespace, key string) error {
		return fmt.Errorf("writing record %s/%s in Etcd: %w", namespace, key, store.ErrNoSpace)
	}

	for _, refused := range []string{"library", "hint"} {
		if refused == "library" {
			harness.store.BeforeUpdate = full
		} else {
			harness.store.BeforeCreate = full
		}

		shared, published := send()

		for what, recorder := range map[string]*httptest.ResponseRecorder{"share": shared, "publish": published} {
			if recorder.Code != http.StatusInsufficientStorage || builderMessage(t, recorder) != store.ErrNoSpace.Error() {
				t.Errorf("%s with no room for the %s = %d %s, want 507", what, refused, recorder.Code, recorder.Body)
			}
		}

		harness.store.BeforeCreate, harness.store.BeforeUpdate = nil, nil
		harness.store.Drop(bapi.NamespaceTemplates, "in/"+bapi.OwnerScope(builderTestPeer)+"/"+bapi.OwnerScope(builderTestOwner))
		harness.store.Drop(bapi.NamespaceTemplates, "pub/"+bapi.OwnerScope(builderTestOwner))
	}

	if got := othersItems(harness.templates(builderTestPeer)); len(got) != 0 {
		t.Fatalf("after the refusals bob lists %q", got)
	}

	// Another request of the owner lands before every write of this one.
	racing := false

	harness.store.BeforeUpdate = func(string, string) error {
		if racing {
			return nil
		}

		racing = true
		defer func() { racing = false }()

		if recorder := harness.post("/collections", `{"name":"Racing"}`); recorder.Code != http.StatusCreated {
			return fmt.Errorf("the racing request = %d", recorder.Code)
		}

		return nil
	}

	shared, published := send()

	for what, recorder := range map[string]*httptest.ResponseRecorder{"share": shared, "publish": published} {
		if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != "1" {
			t.Errorf("%s of a library that keeps changing = %d %s, want 503 with Retry-After", what, recorder.Code, recorder.Body)
		}
	}

	harness.store.BeforeUpdate = nil

	// A library this server cannot read is not changed.
	key := bapi.LibraryKey(builderTestOwner)

	record, err := harness.store.GetRecord(bapi.NamespaceTemplates, key)
	if err != nil {
		t.Fatalf("reading the library: %v", err)
	}

	harness.store.SetValue(bapi.NamespaceTemplates, key, []byte(strings.Replace(string(record.Value), `{"owner"`, `{"later":1,"owner"`, 1)))

	shared, published = send()
	taken := harness.publishTemplates(builderTestOwner, builderTestPeer, publisher, `{"templates":["x"],"serverWide":false}`)

	for what, recorder := range map[string]*httptest.ResponseRecorder{"share": shared, "publish": published, "take back": taken} {
		if recorder.Code != http.StatusConflict {
			t.Errorf("%s of a damaged library = %d %s, want 409", what, recorder.Code, recorder.Body)
		}
	}
}

// builderTemplateResultShape decodes a share or publish answer strictly.
func builderTemplateResultShape(t *testing.T, recorder *httptest.ResponseRecorder) builderTemplateResult {
	t.Helper()

	var result builderTemplateResult

	decoder := json.NewDecoder(recorder.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decoding %s: %v", recorder.Body, err)
	}

	return result
}

// TestBuilderTemplateResultShape asserts a share answer is a list of
// failures, empty when every item took the change.
func TestBuilderTemplateResultShape(t *testing.T) {
	fixture := newBuilderSharedFixture(t)

	recorder := fixture.harness.shareTemplates(builderTestOwner, builderTemplateShareRequest{
		Templates: []string{"missing"}, Collections: nil, Add: []string{builderTestPeer}, Remove: nil,
	})

	result := builderTemplateResultShape(t, recorder)

	if want := []builderTemplateFailure{
		{Kind: "template", ID: "missing", Reason: "not-found", Code: string(bdoc.CodeTemplateItemNotFound)},
	}; !slices.Equal(result.Failed, want) {
		t.Fatalf("failed = %+v, want %+v", result.Failed, want)
	}
}
