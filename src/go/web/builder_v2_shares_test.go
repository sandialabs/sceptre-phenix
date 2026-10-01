package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	v1 "phenix/types/version/v1"
	"phenix/util/plog"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
)

const (
	// builderShareCreated is when every test account was created, and
	// builderShareRecreated when an account deleted and created again under
	// the same name was.
	builderShareCreated   = "2026-09-26T15:04:05Z"
	builderShareRecreated = "2026-09-27T08:00:00Z"

	builderShareCarol = "carol"
	builderShareDave  = "dave"
	builderShareErin  = "erin"
)

// builderShareConfigVerbs is every config verb.
var builderShareConfigVerbs = []string{"list", "get", "create", "update", "delete"} //nolint:gochecknoglobals // test fixture

// builderShareRole returns a role holding the given config verbs, and the
// given "builder-drafts" verbs on every draft. The config resources are
// enumerated so no wildcard grants "builder-drafts" by accident.
func builderShareRole(configVerbs []string, draftVerbs ...string) *rbac.Role {
	policies := []*v1.PolicySpec{builderV2Policy(
		[]string{"configs", "schemas", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		configVerbs,
	)}

	if len(draftVerbs) != 0 {
		policies = append(policies, builderV2Policy(
			[]string{builderV2DraftsResource}, []string{"*", "*/*"}, draftVerbs,
		))
	}

	role := builderV2Role(policies...)

	return &role
}

// builderShareUser returns the User config of an account created at created.
func builderShareUser(name, created string) store.Config {
	return store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "User",
		Metadata: store.ConfigMetadata{
			Name: name, Created: created, Updated: created, Annotations: nil,
		},
		Spec:   nil,
		Status: nil,
	}
}

// setUser gives the named user an account created at created, replacing
// any it had, as deleting a user and creating one of the same name does.
func (h *builderV2Harness) setUser(name, created string) {
	h.removeUser(name)
	h.configs = append(h.configs, builderShareUser(name, created))
}

// removeUser deletes the account of the named user.
func (h *builderV2Harness) removeUser(name string) {
	h.configs = slices.DeleteFunc(h.configs, func(config store.Config) bool {
		return config.Kind == "User" && config.Metadata.Name == name
	})
}

// builderShareFixture is a draft owned by alice in a harness where alice,
// bob, carol, dave and erin all have accounts.
type builderShareFixture struct {
	t       *testing.T
	harness *builderV2Harness
	id      string
	path    string
}

func newBuilderShareFixture(t *testing.T) *builderShareFixture {
	t.Helper()

	harness := newBuilderV2Harness(t)

	for _, user := range []string{
		builderV2TestOwner, builderV2TestPeer, builderShareCarol, builderShareDave, builderShareErin,
	} {
		harness.setUser(user, builderShareCreated)
	}

	draft := harness.createDraft(builderV2TestOwner, "shared")

	return &builderShareFixture{
		t:       t,
		harness: harness,
		id:      draft.ID,
		path:    "/builder-v2/drafts/" + builderV2TestOwner + "/" + draft.ID,
	}
}

// meta returns the draft as stored.
func (f *builderShareFixture) meta() *bapi.DraftMetadata {
	f.t.Helper()

	meta, err := f.harness.service.GetDraft(context.Background(), f.id)
	if err != nil {
		f.t.Fatalf("GetDraft returned error: %v", err)
	}

	return meta
}

// builderShareBody returns the body of a request replacing who a draft is
// shared with by the given "user:access" entries.
func builderShareBody(t *testing.T, entries ...string) string {
	t.Helper()

	shares := make([]builderShareRequestEntry, 0, len(entries))

	for _, entry := range entries {
		user, access, _ := strings.Cut(entry, ":")
		shares = append(shares, builderShareRequestEntry{User: user, Access: access})
	}

	body, err := json.Marshal(builderSharesRequest{Shares: &shares})
	if err != nil {
		t.Fatalf("encoding share request: %v", err)
	}

	return string(body)
}

// put makes a request, as the owner, replacing who the draft is shared with.
func (f *builderShareFixture) put(ifMatch, body string) *httptest.ResponseRecorder {
	f.t.Helper()

	return f.harness.do(builderV2Request{
		method:  http.MethodPut,
		path:    f.path + "/shares",
		body:    body,
		user:    builderV2TestOwner,
		role:    builderShareRole(builderShareConfigVerbs),
		ifMatch: ifMatch,
	})
}

// share replaces who the draft is shared with by the given "user:access"
// entries, as the owner, and returns the answer.
func (f *builderShareFixture) share(entries ...string) builderSharesResponse {
	f.t.Helper()

	recorder := f.put(f.meta().SharesETag(), builderShareBody(f.t, entries...))
	if recorder.Code != http.StatusOK {
		f.t.Fatalf("sharing %v: status = %d: %s", entries, recorder.Code, recorder.Body)
	}

	var response builderSharesResponse

	f.harness.decode(recorder, &response)

	return response
}

// as makes a request for the draft, or one of its routes, as the user.
func (f *builderShareFixture) as(user string, role *rbac.Role, method, route string) *httptest.ResponseRecorder {
	f.t.Helper()

	request := builderV2Request{method: method, path: f.path + route, user: user, role: role}

	switch {
	case method == http.MethodPost && route == "/snapshots":
		request.body = `{"document":` + string(builderV2Document(f.t, "second")) + `}`
		request.ifMatch = f.meta().ETag()
	case route == "/cursor":
		request.body = `{"index":0}`
		request.ifMatch = f.meta().ETag()
	case route == "/publish":
		request.body = `{"mode":"topology","topology":{"name":"shared","action":"create"}}`
		request.ifMatch = f.meta().ETag()
	case method == http.MethodDelete:
		request.ifMatch = f.meta().ETag()
	case method == http.MethodPut && route == "/shares":
		request.body = builderShareBody(f.t)
		request.ifMatch = f.meta().SharesETag()
	}

	return f.harness.do(request)
}

// builderShareRoutes are the routes of one draft, in the order of
// [builderShareCaller.statuses].
var builderShareRoutes = []struct{ method, route string }{ //nolint:gochecknoglobals // test fixture
	{http.MethodGet, ""},
	{http.MethodGet, "/snapshots"},
	{http.MethodGet, "/snapshots/current"},
	{http.MethodPost, "/snapshots"},
	{http.MethodPatch, "/cursor"},
	{http.MethodPost, "/publish"},
	{http.MethodDelete, ""},
	{http.MethodGet, "/shares"},
	{http.MethodPut, "/shares"},
	// Deleting the current version is refused once access is granted.
	{http.MethodDelete, "/snapshots/current"},
	{http.MethodGet, "/shares/candidates"},
}

// builderShareCaller is someone asking for alice's draft: who, with what
// role, what the draft is shared with first, and what then changes.
type builderShareCaller struct {
	name  string
	user  string
	role  *rbac.Role
	share []string
	then  func(*builderShareFixture)
	// statuses answer [builderShareRoutes], in order.
	statuses [11]int
	// access, via, readOnly and canShare are what GET of the draft reports.
	access, via        string
	readOnly, canShare bool
}

func builderShareCallers() []builderShareCaller {
	var (
		all      = builderShareConfigVerbs
		readOnly = []string{"list", "get"}
		ok       = [11]int{200, 200, 200, 201, 200, 200, 204, 200, 200, 409, 200}
		edit     = [11]int{200, 200, 200, 201, 200, 200, 403, 403, 403, 409, 403}
		view     = [11]int{200, 200, 200, 403, 403, 403, 403, 403, 403, 403, 403}
		hidden   = [11]int{404, 404, 404, 404, 404, 404, 404, 404, 404, 404, 404}
		peer     = builderV2TestPeer
		bobEdit  = []string{peer + ":edit"}
		bobView  = []string{peer + ":view"}
	)

	return []builderShareCaller{
		{name: "owner", user: builderV2TestOwner, role: builderShareRole(all), statuses: ok, access: "owner", canShare: true},
		{
			// Each operation authorizes its own verb: a read-only role reads
			// its owner's draft and changes nothing.
			name: "owner with a read-only role", user: builderV2TestOwner, role: builderShareRole(readOnly),
			statuses: [11]int{200, 200, 200, 403, 403, 403, 403, 200, 403, 403, 403}, access: "owner", readOnly: true,
		},
		{name: "edit share", user: peer, role: builderShareRole(all), share: bobEdit, statuses: edit, access: "edit", via: "share"},
		{
			name: "view share", user: peer, role: builderShareRole(all), share: bobView,
			statuses: view, access: "view", via: "share", readOnly: true,
		},
		{
			name: "role list", user: peer, role: builderShareRole(all, "list"),
			statuses: [11]int{403, 403, 403, 403, 403, 403, 403, 403, 403, 403, 403},
		},
		{
			name: "role get", user: peer, role: builderShareRole(all, "list", "get"),
			statuses: view, access: "view", via: "role", readOnly: true,
		},
		{
			name: "role update", user: peer, role: builderShareRole(all, "list", "get", "update"),
			statuses: edit, access: "edit", via: "role",
		},
		{
			name: "role delete", user: peer, role: builderShareRole(all, "list", "get", "delete"),
			statuses: [11]int{200, 200, 200, 403, 403, 403, 204, 403, 403, 403, 403}, access: "view", via: "role", readOnly: true,
		},
		{name: "none", user: peer, role: builderShareRole(all), statuses: hidden},
		{
			name: "share of an account created again", user: peer, role: builderShareRole(all), share: bobEdit,
			then: func(f *builderShareFixture) { f.harness.setUser(peer, builderShareRecreated) }, statuses: hidden,
		},
		{
			name: "share of a deleted account", user: peer, role: builderShareRole(all), share: bobEdit,
			then: func(f *builderShareFixture) { f.harness.removeUser(peer) }, statuses: hidden,
		},
		{
			name: "edit share without config update", user: peer, role: builderShareRole(readOnly), share: bobEdit,
			statuses: view, access: "edit", via: "share", readOnly: true,
		},
		{
			name: "view share with role update", user: peer, role: builderShareRole(all, "list", "get", "update"), share: bobView,
			statuses: edit, access: "edit", via: "share",
		},
		{
			name: "edit share with role delete", user: peer, role: builderShareRole(all, "delete"), share: bobEdit,
			statuses: [11]int{200, 200, 200, 201, 200, 200, 204, 403, 403, 409, 403}, access: "edit", via: "share",
		},
	}
}

// TestBuilderV2ShareMatrix asks for every route of a draft as every kind of
// caller: its owner, users it is shared with, users whose role grants access,
// and users with no access, including through a share whose account is gone.
func TestBuilderV2ShareMatrix(t *testing.T) { //nolint:paralleltest // mutates package options
	for _, caller := range builderShareCallers() {
		for i, route := range builderShareRoutes {
			t.Run(caller.name+" "+route.method+" "+route.route, func(t *testing.T) {
				fixture := newBuilderShareFixture(t)

				if len(caller.share) != 0 {
					fixture.share(caller.share...)
				}

				if caller.then != nil {
					caller.then(fixture)
				}

				recorder := fixture.as(caller.user, caller.role, route.method, route.route)
				if recorder.Code != caller.statuses[i] {
					t.Fatalf("status = %d, want %d: %s", recorder.Code, caller.statuses[i], recorder.Body)
				}

				if route.method == http.MethodGet && route.route == "" && recorder.Code == http.StatusOK {
					checkBuilderShareAccess(t, fixture.harness, recorder, caller)
				}
			})
		}
	}
}

// checkBuilderShareAccess checks what GET of a draft reports the caller may
// do with it.
func checkBuilderShareAccess(
	t *testing.T,
	harness *builderV2Harness,
	recorder *httptest.ResponseRecorder,
	caller builderShareCaller,
) {
	t.Helper()

	var draft builderDraftResponse

	harness.decode(recorder, &draft)

	owner := caller.access == "owner"

	if draft.Access != caller.access || draft.Via != caller.via || draft.ReadOnly != caller.readOnly ||
		draft.CanShare != caller.canShare || (!owner && draft.Shares != nil) {
		t.Fatalf("access %q via %q, readOnly %t, canShare %t, shares %v; want %q via %q, readOnly %t, canShare %t",
			draft.Access, draft.Via, draft.ReadOnly, draft.CanShare, draft.Shares,
			caller.access, caller.via, caller.readOnly, caller.canShare)
	}
}

// builderShareDenied is what the security log records of a refused request.
const builderShareDenied = "builder v2 cross-user draft request not allowed"

// TestBuilderV2ShareNoLeak asserts a draft the caller cannot see, because
// it is not shared with the caller or the share's account is gone, is
// answered exactly as a draft that does not exist, on every route.
func TestBuilderV2ShareNoLeak(t *testing.T) { //nolint:paralleltest // mutates package options
	role := builderShareRole(builderShareConfigVerbs)

	for _, route := range builderShareRoutes {
		t.Run(route.method+" "+route.route, func(t *testing.T) {
			fixture := newBuilderShareFixture(t)

			// The same request for a draft that does not exist.
			missing := *fixture
			missing.path = strings.Replace(fixture.path, fixture.id, "id-missing", 1)
			want := missing.as(builderShareErin, role, route.method, route.route)

			// A draft shared with nobody answers another user the same way.
			checkBuilderShareSameAsMissing(t, "unshared draft", fixture.id,
				fixture.as(builderShareErin, role, route.method, route.route), want)

			fixture.share(builderShareCarol+":edit", builderShareDave+":edit")
			fixture.harness.setUser(builderShareCarol, builderShareRecreated)
			fixture.harness.removeUser(builderShareDave)

			logs := plogtest.Capture(t)

			for _, caller := range []struct{ user, reason string }{
				{builderShareErin, "no-access"},
				{builderShareCarol, "stale-share"},
				{builderShareDave, "stale-share"},
			} {
				checkBuilderShareSameAsMissing(t, caller.user, fixture.id,
					fixture.as(caller.user, role, route.method, route.route), want)

				records := logs.Take(t, plogtest.Message(builderShareDenied))
				if len(records) != 1 || records[0]["reason"] != caller.reason || records[0]["user"] != caller.user {
					t.Errorf("%s: denials logged %v, want one with reason %q", caller.user, records, caller.reason)
				}
			}

			if meta := fixture.meta(); len(meta.History) != 1 || meta.SharingVersion() != 1 {
				t.Errorf("history %d, sharing version %d; want the draft untouched", len(meta.History), meta.SharingVersion())
			}
		})
	}
}

func builderShareSameHeaders(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}

	for key, values := range a {
		if !slices.Equal(values, b[key]) {
			return false
		}
	}

	return true
}

// damage makes the draft's record one this server cannot read, as if a newer
// phenix wrote it, and returns its ETag.
func (f *builderShareFixture) damage() string {
	f.t.Helper()

	return f.harness.damageDraft(f.id)
}

// damageDraft adds a field this server does not know to a draft's record, as
// a newer phenix may write, so its metadata no longer decodes. It returns the
// record's new ETag.
func (h *builderV2Harness) damageDraft(id string) string {
	h.t.Helper()

	record, err := h.store.GetRecord(bapi.NamespaceDrafts, id)
	if err != nil {
		h.t.Fatalf("reading draft %s: %v", id, err)
	}

	value := append(bytes.TrimSuffix(record.Value, []byte("}")), `,"future":true}`...)

	damaged, err := h.store.UpdateRecord(bapi.NamespaceDrafts, id, value, record.Revision)
	if err != nil {
		h.t.Fatalf("damaging draft %s: %v", id, err)
	}

	return bapi.RevisionETag(damaged.Revision)
}

// TestBuilderV2ShareDamagedDraft asserts a share grants nothing on a draft
// whose record this server can no longer read: the share is never read.
func TestBuilderV2ShareDamagedDraft(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	fixture.share(builderV2TestPeer + ":edit")

	harness := fixture.harness
	etag := fixture.damage()

	var (
		all      = builderShareConfigVerbs
		listOnly = builderShareRole(all, "list")
	)

	for _, caller := range []struct {
		name    string
		user    string
		role    *rbac.Role
		listed  bool
		get     int
		deleted int
	}{
		{name: "share", user: builderV2TestPeer, role: builderShareRole(all), get: 404, deleted: 404},
		{name: "none", user: builderShareErin, role: builderShareRole(all), get: 404, deleted: 404},
		{name: "role list", user: builderShareDave, role: listOnly, listed: true, get: 500, deleted: 403},
	} {
		recorder := harness.do(builderV2Request{method: http.MethodGet, path: "/builder-v2/drafts", user: caller.user, role: caller.role})

		var listing struct {
			Shared  []builderDraftResponse        `json:"shared"`
			Damaged []builderDamagedDraftResponse `json:"damaged"`
		}

		harness.decode(recorder, &listing)

		if len(listing.Shared) != 0 || (len(listing.Damaged) == 1) != caller.listed {
			t.Errorf("%s: listing = %+v, want the damaged draft listed: %t", caller.name, listing, caller.listed)
		}

		if code := fixture.as(caller.user, caller.role, http.MethodGet, "").Code; code != caller.get {
			t.Errorf("%s: GET status = %d, want %d", caller.name, code, caller.get)
		}

		recorder = harness.do(builderV2Request{
			method: http.MethodDelete, path: fixture.path, user: caller.user, role: caller.role, ifMatch: etag,
		})
		if recorder.Code != caller.deleted {
			t.Errorf("%s: DELETE status = %d, want %d: %s", caller.name, recorder.Code, caller.deleted, recorder.Body)
		}
	}

	recorder := harness.do(builderV2Request{
		method: http.MethodDelete, path: fixture.path, user: builderV2TestOwner, role: builderShareRole(all), ifMatch: etag,
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("owner DELETE status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}
}

// TestBuilderV2ShareDamagedDraftOtherOwner asserts a caller naming itself,
// or an owner its role covers, in the path of another user's damaged draft is
// answered exactly as for a draft that does not exist.
func TestBuilderV2ShareDamagedDraftOtherOwner(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	fixture.share(builderShareCarol + ":edit")

	harness := fixture.harness
	etag := fixture.damage()

	all := builderShareRole(builderShareConfigVerbs)
	scoped := builderV2Role(
		builderV2Policy([]string{"configs"}, []string{"*", "*/*"}, builderShareConfigVerbs),
		builderV2Policy([]string{builderV2DraftsResource}, []string{builderShareErin + "/*"}, []string{"list", "get", "update"}),
	)

	snapshot := `{"document":` + string(builderV2Document(t, "second")) + `}`

	for _, caller := range []struct {
		name, user, owner string
		role              *rbac.Role
	}{
		{name: "stranger naming itself", user: builderShareErin, owner: builderShareErin, role: all},
		{name: "share holder naming itself", user: builderShareCarol, owner: builderShareCarol, role: all},
		{name: "role scoped to another owner", user: builderShareDave, owner: builderShareErin, role: &scoped},
	} {
		for _, route := range []struct{ method, route, body string }{
			{http.MethodGet, "", ""},
			{http.MethodGet, "/snapshots", ""},
			{http.MethodGet, "/shares", ""},
			{http.MethodPost, "/snapshots", snapshot},
		} {
			request := func(id string) *httptest.ResponseRecorder {
				return harness.do(builderV2Request{
					method:  route.method,
					path:    "/builder-v2/drafts/" + caller.owner + "/" + id + route.route,
					body:    route.body,
					user:    caller.user,
					role:    caller.role,
					ifMatch: etag,
				})
			}

			checkBuilderShareSameAsMissing(t, caller.name+" "+route.method+" "+route.route, fixture.id,
				request(fixture.id), request("id-missing"))
		}

		fork := func(id string) *httptest.ResponseRecorder {
			return forkBuilderDraft(t, harness, caller.user, caller.role, caller.owner+"/"+id, bdoc.NewDocument("fork"))
		}

		checkBuilderShareSameAsMissing(t, caller.name+" forkOf", fixture.id, fork(fixture.id), fork("id-missing"))
	}
}

// checkBuilderShareSameAsMissing asserts the answer for the draft id is the
// 404 given for a draft that does not exist, "id-missing".
func checkBuilderShareSameAsMissing(t *testing.T, name, id string, got, missing *httptest.ResponseRecorder) {
	t.Helper()

	if missing.Code != http.StatusNotFound {
		t.Fatalf("%s: missing draft status = %d, want %d", name, missing.Code, http.StatusNotFound)
	}

	wantBody := strings.ReplaceAll(missing.Body.String(), "id-missing", id)

	if got.Code != missing.Code || got.Body.String() != wantBody || !builderShareSameHeaders(got.Header(), missing.Header()) {
		t.Errorf("%s: %d %v %s, want %d %v %s", name, got.Code, got.Header(), got.Body, missing.Code, missing.Header(), wantBody)
	}
}

// builderShareListing is the JSON view of GET /builder-v2/drafts.
type builderShareListing struct {
	Drafts []builderDraftResponse `json:"drafts"`
	Shared []builderDraftResponse `json:"shared"`
}

// list returns the drafts the user may see.
func (f *builderShareFixture) list(user string, role *rbac.Role) builderShareListing {
	f.t.Helper()

	recorder := f.harness.do(builderV2Request{method: http.MethodGet, path: "/builder-v2/drafts", user: user, role: role})
	if recorder.Code != http.StatusOK {
		f.t.Fatalf("listing as %s: status = %d: %s", user, recorder.Code, recorder.Body)
	}

	var listing builderShareListing

	f.harness.decode(recorder, &listing)

	return listing
}

// builderShareListed describes the drafts of a listing as "id access via
// readOnly" strings, sorted.
func builderShareListed(drafts []builderDraftResponse) []string {
	listed := make([]string, 0, len(drafts))

	for _, draft := range drafts {
		listed = append(listed, draft.ID+" "+draft.Access+" "+draft.Via+" "+strconv.FormatBool(draft.ReadOnly))
	}

	slices.Sort(listed)

	return listed
}

// TestBuilderV2ShareListing asserts the listing separates drafts shared
// with the caller from those its role lets it list, and tells only owners
// who their drafts are shared with.
func TestBuilderV2ShareListing(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	fixture.share(builderV2TestPeer+":edit", builderShareCarol+":view")

	harness := fixture.harness
	unshared := harness.createDraft(builderV2TestOwner, "unshared").ID
	carols := harness.createDraft(builderShareCarol, "carols").ID
	bobs := harness.createDraft(builderV2TestPeer, "bobs").ID

	recorder := harness.do(builderV2Request{
		method: http.MethodPut, path: "/builder-v2/drafts/" + builderShareCarol + "/" + carols + "/shares",
		user: builderShareCarol, role: builderShareRole(builderShareConfigVerbs), ifMatch: `"shares-0"`,
		body: builderShareBody(t, builderV2TestPeer+":view"),
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("sharing carol's draft: status = %d: %s", recorder.Code, recorder.Body)
	}

	var (
		all      = builderShareConfigVerbs
		readOnly = []string{"list", "get"}
	)

	for _, test := range []struct {
		name     string
		role     *rbac.Role
		shared   []string
		canShare bool
	}{
		{name: "shares", role: builderShareRole(all), canShare: true, shared: []string{
			fixture.id + " edit share false", carols + " view share true",
		}},
		{name: "shares without config update", role: builderShareRole(readOnly), shared: []string{
			fixture.id + " edit share true", carols + " view share true",
		}},
		{name: "shares and role list", role: builderShareRole(all, "list"), canShare: true, shared: []string{
			fixture.id + " edit share false", carols + " view share true", unshared + " view role true",
		}},
	} {
		listing := fixture.list(builderV2TestPeer, test.role)

		slices.Sort(test.shared)

		if shared := builderShareListed(listing.Shared); !slices.Equal(shared, test.shared) {
			t.Errorf("%s: shared = %v, want %v", test.name, shared, test.shared)
		}

		for _, draft := range listing.Shared {
			if draft.CanShare || draft.Shares != nil {
				t.Errorf("%s: %s tells a recipient canShare %t, shares %v", test.name, draft.ID, draft.CanShare, draft.Shares)
			}
		}

		if len(listing.Drafts) != 1 || listing.Drafts[0].ID != bobs || listing.Drafts[0].CanShare != test.canShare {
			t.Errorf("%s: drafts = %+v, want %s with canShare %t", test.name, listing.Drafts, bobs, test.canShare)
		}
	}

	checkBuilderShareOwnerListing(t, fixture, unshared)

	// A share whose account was deleted, or replaced, lists nothing.
	for _, change := range []func(){
		func() { harness.removeUser(builderV2TestPeer) },
		func() { harness.setUser(builderV2TestPeer, builderShareRecreated) },
	} {
		change()

		if listing := fixture.list(builderV2TestPeer, builderShareRole(all)); len(listing.Shared) != 0 {
			t.Errorf("shared = %v, want nothing through a share of a gone account", builderShareListed(listing.Shared))
		}
	}
}

// checkBuilderShareOwnerListing checks the owner is told whom its drafts are
// shared with, and may share them only with config update and an account.
func checkBuilderShareOwnerListing(t *testing.T, fixture *builderShareFixture, unshared string) {
	t.Helper()

	for _, test := range []struct {
		name     string
		role     *rbac.Role
		account  bool
		canShare bool
	}{
		{name: "owner", role: builderShareRole(builderShareConfigVerbs), account: true, canShare: true},
		{name: "owner without config update", role: builderShareRole([]string{"list", "get"}), account: true},
		{name: "owner without an account", role: builderShareRole(builderShareConfigVerbs)},
	} {
		if !test.account {
			fixture.harness.removeUser(builderV2TestOwner)
		}

		listing := fixture.list(builderV2TestOwner, test.role)

		if len(listing.Drafts) != 2 || len(listing.Shared) != 0 {
			t.Fatalf("%s: listing = %+v, want its two drafts only", test.name, listing)
		}

		for _, draft := range listing.Drafts {
			var want []builderDraftShare
			if draft.ID != unshared {
				want = []builderDraftShare{{User: builderV2TestPeer, Access: "edit"}, {User: builderShareCarol, Access: "view"}}
			}

			if draft.Access != "owner" || draft.Via != "" || draft.CanShare != test.canShare || !slices.Equal(draft.Shares, want) {
				t.Errorf("%s: %s is %q via %q, canShare %t, shares %v; want owner, canShare %t, shares %v",
					test.name, draft.ID, draft.Access, draft.Via, draft.CanShare, draft.Shares, test.canShare, want)
			}
		}
	}

	fixture.harness.setUser(builderV2TestOwner, builderShareCreated)
}

// shares returns who the draft is shared with, as the owner sees it.
func (f *builderShareFixture) shares() (builderSharesResponse, *httptest.ResponseRecorder) {
	f.t.Helper()

	recorder := f.as(builderV2TestOwner, builderShareRole(builderShareConfigVerbs), http.MethodGet, "/shares")
	if recorder.Code != http.StatusOK {
		f.t.Fatalf("GET shares: status = %d: %s", recorder.Code, recorder.Body)
	}

	var response builderSharesResponse

	f.harness.decode(recorder, &response)

	return response, recorder
}

// builderShareUsers describes a share list as "user:access" or
// "user:access:stale" strings, in order.
func builderShareUsers(shares []builderShareResponse) []string {
	users := make([]string, 0, len(shares))

	for _, share := range shares {
		user := share.User + ":" + share.Access
		if share.Stale {
			user += ":stale"
		}

		users = append(users, user)
	}

	return users
}

// TestBuilderV2GetShares asserts only the owner learns who a draft is
// shared with, with the list's own entity tag, and which shares no longer
// grant anything.
func TestBuilderV2GetShares(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)

	response, recorder := fixture.shares()
	if len(response.Shares) != 0 || response.Shares == nil || response.SharesETag != `"shares-0"` ||
		recorder.Header().Get("ETag") != `"shares-0"` || response.MaxShares != bapi.MaxShares || response.Draft != nil {
		t.Fatalf("never shared: %s with ETag %s", recorder.Body, recorder.Header().Get("ETag"))
	}

	fixture.share(builderShareCarol+":view", builderV2TestPeer+":edit", builderShareDave+":view")
	fixture.harness.removeUser(builderShareCarol)
	fixture.harness.setUser(builderShareDave, builderShareRecreated)

	response, recorder = fixture.shares()

	want := []string{builderV2TestPeer + ":edit", builderShareCarol + ":view:stale", builderShareDave + ":view:stale"}
	if users := builderShareUsers(response.Shares); !slices.Equal(users, want) ||
		response.SharesETag != `"shares-1"` || recorder.Header().Get("ETag") != `"shares-1"` ||
		response.Shares[0].GrantedAt.IsZero() {
		t.Fatalf("shares = %v at %s (ETag %s), want %v at \"shares-1\"",
			users, response.SharesETag, recorder.Header().Get("ETag"), want)
	}

	for _, field := range []string{"userCreated", "grantedBy", "updatedBy"} {
		if strings.Contains(recorder.Body.String(), field) {
			t.Errorf("the share list exposes %s", field)
		}
	}

	all := builderShareConfigVerbs

	for _, caller := range []struct {
		user   string
		role   *rbac.Role
		status int
	}{
		{user: builderV2TestPeer, role: builderShareRole(all), status: http.StatusForbidden},
		{user: builderShareErin, role: builderShareRole(all, "list", "get", "update", "delete"), status: http.StatusForbidden},
		{user: builderShareErin, role: builderShareRole(all), status: http.StatusNotFound},
		{user: builderV2TestOwner, role: builderShareRole([]string{"list"}), status: http.StatusForbidden},
	} {
		if recorder := fixture.as(caller.user, caller.role, http.MethodGet, "/shares"); recorder.Code != caller.status {
			t.Errorf("%s: status = %d, want %d: %s", caller.user, recorder.Code, caller.status, recorder.Body)
		}
	}
}

// TestBuilderV2ShareCandidates lists who alice may share her draft with:
// every user a share would be accepted for, whatever her role lets her do
// with users.
func TestBuilderV2ShareCandidates(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	harness := fixture.harness

	// Carol is shared with already and stays listed; Dave was shared with
	// through an account since replaced, which a share would be refused for.
	fixture.share(builderShareCarol+":edit", builderShareDave+":view")
	harness.setUser(builderShareDave, builderShareRecreated)

	for i := range harness.configs {
		switch harness.configs[i].Metadata.Name {
		case builderV2TestPeer:
			harness.configs[i].Spec = map[string]any{"first_name": "Bob", "last_name": "Builder"}
		case builderShareCarol:
			harness.configs[i].Spec = map[string]any{"first_name": " Carol ", "last_name": ""}
		}
	}

	// An account without a creation time cannot be shared with either.
	harness.configs = append(harness.configs, builderShareUser("frank", ""))

	candidates := func(role *rbac.Role) ([]builderShareCandidate, string) {
		t.Helper()

		recorder := fixture.as(builderV2TestOwner, role, http.MethodGet, "/shares/candidates")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
		}

		var response builderShareCandidatesResponse

		harness.decode(recorder, &response)

		return response.Users, recorder.Body.String()
	}

	viewing := func(names ...string) *rbac.Role {
		role := builderShareRole(builderShareConfigVerbs)
		role.Spec.Policies = append(role.Spec.Policies, builderV2Policy([]string{"users"}, names, []string{"list"}))

		return role
	}

	want := []builderShareCandidate{
		{Username: builderV2TestPeer, Name: "Bob Builder"},
		{Username: builderShareCarol, Name: "Carol"},
		{Username: builderShareErin, Name: ""},
	}

	// Every one of them is listed without users list, and a users list
	// naming only some of them lists the rest too.
	for _, role := range []*rbac.Role{builderShareRole(builderShareConfigVerbs), viewing("carol"), viewing("*")} {
		if got, _ := candidates(role); !slices.Equal(got, want) {
			t.Fatalf("candidates = %+v, want %+v", got, want)
		}
	}

	// A share with every one of them is accepted. It drops Dave's, so his new
	// account can be shared with now.
	fixture.share(builderV2TestPeer+":view", builderShareCarol+":edit", builderShareErin+":view")

	withDave := slices.Insert(slices.Clone(want), 2, builderShareCandidate{Username: builderShareDave, Name: ""})
	if got, _ := candidates(builderShareRole(builderShareConfigVerbs)); !slices.Equal(got, withDave) {
		t.Fatalf("candidates = %+v, want %+v", got, withDave)
	}

	// Once only the owner's and Frank's accounts are left, none is listed.
	for _, user := range []string{builderV2TestPeer, builderShareCarol, builderShareDave, builderShareErin} {
		harness.removeUser(user)
	}

	if got, body := candidates(builderShareRole(builderShareConfigVerbs)); len(got) != 0 ||
		!strings.Contains(body, `"users":[]`) {
		t.Fatalf("candidates without other accounts = %s, want none", body)
	}
}

// builderShareRefusal is the 422 of a refused share list.
type builderShareRefusal struct {
	Message string              `json:"message"`
	Cause   string              `json:"cause"`
	Errors  []builderShareError `json:"errors"`
}

// TestBuilderV2PutSharesRequests asserts malformed, stale and refused
// requests replacing who a draft is shared with change nothing.
func TestBuilderV2PutSharesRequests(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	draftETag := fixture.meta().ETag()
	owner := builderV2TestOwner

	tooMany := make([]string, 0, bapi.MaxShares+1)
	for i := range bapi.MaxShares + 1 {
		tooMany = append(tooMany, "user-"+strconv.Itoa(i)+":view")
	}

	for _, test := range []struct {
		name    string
		ifMatch string
		body    string
		status  int
		etag    string
		refused []builderShareError
	}{
		{name: "no If-Match", status: 400},
		{name: "wildcard If-Match", ifMatch: "*", status: 400},
		{name: "weak If-Match", ifMatch: `W/"shares-0"`, status: 400},
		{name: "stale If-Match", ifMatch: `"shares-5"`, status: 412, etag: `"shares-0"`},
		{name: "draft If-Match", ifMatch: draftETag, status: 412, etag: `"shares-0"`},
		{name: "no body", ifMatch: `"shares-0"`, status: 400},
		{name: "bad JSON", ifMatch: `"shares-0"`, body: `{"shares":`, status: 400},
		{name: "no list", ifMatch: `"shares-0"`, body: `{}`, status: 400},
		{name: "unknown field", ifMatch: `"shares-0"`, body: `{"shares":[],"public":true}`, status: 400},
		{name: "unknown entry field", ifMatch: `"shares-0"`, body: `{"shares":[{"user":"bob","access":"view","role":"x"}]}`, status: 400},
		{name: "trailing data", ifMatch: `"shares-0"`, body: `{"shares":[]}{}`, status: 400},
		{name: "trailing brace", ifMatch: `"shares-0"`, body: `{"shares":[]}}`, status: 400},
		{name: "trailing bracket", ifMatch: `"shares-0"`, body: `{"shares":[]}]`, status: 400},
		{
			name: "too large", ifMatch: `"shares-0"`, status: 413,
			body: `{"shares":[{"user":"` + strings.Repeat("a", builderV2SharesMaxBytes) + `","access":"view"}]}`,
		},
		{
			// The limit is reached only while looking for trailing content.
			name: "too large after the value", ifMatch: `"shares-0"`, status: 413,
			body: `{"shares":[]}` + strings.Repeat(" ", builderV2SharesMaxBytes),
		},
		{
			name: "owner", ifMatch: `"shares-0"`, body: builderShareBody(t, owner+":view"), status: 422,
			refused: []builderShareError{{User: owner, Reason: "owner"}},
		},
		{
			name: "duplicate", ifMatch: `"shares-0"`, body: builderShareBody(t, "bob:view", "bob:edit", "bob:view"), status: 422,
			refused: []builderShareError{{User: "bob", Reason: "duplicate"}},
		},
		{
			name: "invalid access", ifMatch: `"shares-0"`, body: builderShareBody(t, "bob:admin"), status: 422,
			refused: []builderShareError{{User: "bob", Reason: "invalid-access"}},
		},
		{
			name: "invalid users", ifMatch: `"shares-0"`, status: 422,
			body: builderShareBody(t, ":view", "a/b:view", "bob\x01:view", strings.Repeat("a", 257)+":view",
				"c1\u009bESC:view", "bob\u202e:view"),
			refused: []builderShareError{
				{User: "", Reason: "invalid-user"}, {User: "a/b", Reason: "invalid-user"},
				{User: "bob\x01", Reason: "invalid-user"}, {User: strings.Repeat("a", 257), Reason: "invalid-user"},
				{User: "c1\u009bESC", Reason: "invalid-user"}, {User: "bob\u202e", Reason: "invalid-user"},
			},
		},
		{
			name: "too many", ifMatch: `"shares-0"`, body: builderShareBody(t, tooMany...), status: 422,
			refused: []builderShareError{{User: "", Reason: "too-many"}},
		},
		{
			name: "unknown user", ifMatch: `"shares-0"`, body: builderShareBody(t, "bob:view", "nobody:edit"), status: 422,
			refused: []builderShareError{{User: "nobody", Reason: "unknown-user"}},
		},
	} {
		recorder := fixture.put(test.ifMatch, test.body)

		if recorder.Code != test.status || recorder.Header().Get("ETag") != test.etag {
			t.Errorf("%s: status = %d with ETag %q, want %d with %q: %s",
				test.name, recorder.Code, recorder.Header().Get("ETag"), test.status, test.etag, recorder.Body)

			continue
		}

		if test.refused != nil {
			var refusal builderShareRefusal

			fixture.harness.decode(recorder, &refusal)

			if refusal.Message == "" || !slices.Equal(refusal.Errors, test.refused) {
				t.Errorf("%s: refusal = %+v, want errors %+v", test.name, refusal, test.refused)
			}
		}
	}

	if meta := fixture.meta(); meta.Sharing != nil || meta.ETag() != draftETag {
		t.Fatalf("sharing = %+v at %s, want the draft untouched at %s", meta.Sharing, meta.ETag(), draftETag)
	}

	// Only an owner with config update and an account of its own may share.
	body := builderShareBody(t, builderV2TestPeer+":view")

	for _, test := range []struct {
		name string
		role *rbac.Role
		then func()
	}{
		{name: "without config update", role: builderShareRole([]string{"list", "get"}), then: func() {}},
		{name: "without an account", role: builderShareRole(builderShareConfigVerbs), then: func() {
			fixture.harness.removeUser(owner)
		}},
	} {
		test.then()

		recorder := fixture.harness.do(builderV2Request{
			method: http.MethodPut, path: fixture.path + "/shares", body: body, user: owner, role: test.role, ifMatch: `"shares-0"`,
		})
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want %d: %s", test.name, recorder.Code, http.StatusForbidden, recorder.Body)
		}
	}
}

// TestBuilderV2PutShares replaces who a draft is shared with: the list has
// its own entity tag, and changing it changes the draft's.
func TestBuilderV2PutShares(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	before := fixture.meta()

	recorder := fixture.put(`"shares-0"`, builderShareBody(t, builderShareCarol+":view", builderV2TestPeer+":edit"))
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"shares-1"` {
		t.Fatalf("status = %d with ETag %q, want 200 with \"shares-1\": %s",
			recorder.Code, recorder.Header().Get("ETag"), recorder.Body)
	}

	var fields map[string]json.RawMessage

	fixture.harness.decode(recorder, &fields)

	if _, ok := fields["etag"]; ok || len(fields) != 4 {
		t.Errorf("response fields = %v, want shares, sharesEtag, maxShares and draft only", slices.Sorted(maps.Keys(fields)))
	}

	var response builderSharesResponse

	fixture.harness.decode(recorder, &response)

	after := fixture.meta()
	draft := response.Draft

	switch users := builderShareUsers(response.Shares); {
	case !slices.Equal(users, []string{"bob:edit", "carol:view"}) || response.SharesETag != `"shares-1"`:
		t.Fatalf("shares = %v at %s, want bob:edit, carol:view at \"shares-1\"", users, response.SharesETag)
	case draft == nil || draft.ETag == before.ETag() || draft.ETag != after.ETag():
		t.Fatalf("draft = %+v, want its new ETag %s", draft, after.ETag())
	case draft.Access != "owner" || !draft.CanShare || len(draft.Shares) != 2 || draft.Document != nil || draft.History != nil:
		t.Fatalf("draft = %+v, want the owner's view without document or history", draft)
	case !after.Updated.Equal(before.Updated) || after.LastModifiedBy != before.LastModifiedBy:
		t.Fatalf("updated %v by %s, want the content timestamps untouched", after.Updated, after.LastModifiedBy)
	}

	// The same list again, in another order, changes nothing.
	recorder = fixture.put(`"shares-1"`, builderShareBody(t, builderV2TestPeer+":edit", builderShareCarol+":view"))

	fixture.harness.decode(recorder, &response)

	if recorder.Code != http.StatusOK || response.SharesETag != `"shares-1"` || response.Draft.ETag != after.ETag() ||
		fixture.meta().Revision != after.Revision {
		t.Fatalf("no-op: status = %d, shares %s, draft %s; want nothing changed", recorder.Code, response.SharesETag, response.Draft.ETag)
	}

	// A save at the draft's ETag before the change is stale.
	recorder = fixture.harness.do(builderV2Request{
		method: http.MethodPost, path: fixture.path + "/snapshots", user: builderV2TestPeer,
		role:    builderShareRole(builderShareConfigVerbs),
		body:    `{"document":` + string(builderV2Document(t, "second")) + `}`,
		ifMatch: before.ETag(),
	})
	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("save at the old ETag: status = %d, want %d", recorder.Code, http.StatusPreconditionFailed)
	}

	// Emptying the list is a change too.
	if emptied := fixture.share(); len(emptied.Shares) != 0 || emptied.SharesETag != `"shares-2"` {
		t.Fatalf("emptied = %+v, want no shares at \"shares-2\"", emptied)
	}

	// A draft that keeps changing is busy.
	fixture.harness.store.BeforeUpdate = func(namespace, key string) error {
		if namespace != bapi.NamespaceDrafts {
			return nil
		}

		return store.NewRecordConflictError(namespace, key, 0, 0)
	}

	recorder = fixture.put(`"shares-2"`, builderShareBody(t, builderV2TestPeer+":view"))
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != "1" {
		t.Fatalf("busy: status = %d, Retry-After %q; want %d, 1: %s",
			recorder.Code, recorder.Header().Get("Retry-After"), http.StatusServiceUnavailable, recorder.Body)
	}
}

// TestBuilderV2ShareLifecycle follows a share through what happens to its
// recipient's account and role, and to its owner and draft.
func TestBuilderV2ShareLifecycle(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	fixture.share(builderV2TestPeer + ":edit")

	var (
		harness = fixture.harness
		bob     = builderV2TestPeer
		all     = builderShareRole(builderShareConfigVerbs)
		reads   = builderShareRole([]string{"list", "get"})
		none    = builderShareRole([]string{"create"})
	)

	expect := func(step, user string, role *rbac.Role, method, route string, status int) {
		t.Helper()

		if recorder := fixture.as(user, role, method, route); recorder.Code != status {
			t.Fatalf("%s: %s %s as %s: status = %d, want %d: %s", step, method, route, user, recorder.Code, status, recorder.Body)
		}
	}

	// A recipient whose role loses config permissions loses what they allow,
	// and gets it back with them: the share is kept.
	expect("no config get", bob, none, http.MethodGet, "", http.StatusForbidden)
	expect("no config update", bob, reads, http.MethodPost, "/snapshots", http.StatusForbidden)
	expect("config permissions back", bob, all, http.MethodPost, "/snapshots", http.StatusCreated)

	// A deleted account, or one created again under the same name, gets
	// nothing through the share, which the owner is shown as stale.
	for _, change := range []func(){
		func() { harness.removeUser(bob) },
		func() { harness.setUser(bob, builderShareRecreated) },
	} {
		change()
		expect("account gone", bob, all, http.MethodGet, "", http.StatusNotFound)

		if response, _ := fixture.shares(); !slices.Equal(builderShareUsers(response.Shares), []string{"bob:edit:stale"}) {
			t.Fatalf("shares = %v, want bob stale", builderShareUsers(response.Shares))
		}
	}

	// The share is never moved to the new account: the owner removes it,
	// saves, and adds the user again.
	recorder := fixture.put(`"shares-1"`, builderShareBody(t, bob+":view"))

	var refusal builderShareRefusal

	harness.decode(recorder, &refusal)

	if recorder.Code != http.StatusUnprocessableEntity ||
		!slices.Equal(refusal.Errors, []builderShareError{{User: bob, Reason: "account-removed"}}) {
		t.Fatalf("keeping the stale share: status = %d: %s", recorder.Code, recorder.Body)
	}

	fixture.share()
	fixture.share(bob + ":edit")
	expect("shared again", bob, all, http.MethodGet, "", http.StatusOK)

	// An owner who may no longer update configs may no longer share, but
	// what is shared keeps working; so it does once the owner is deleted.
	checkBuilderShareOwnerChange(t, fixture, builderV2TestOwner, reads)
	expect("owner without config update", bob, all, http.MethodPost, "/snapshots", http.StatusCreated)

	harness.removeUser(builderV2TestOwner)
	checkBuilderShareOwnerChange(t, fixture, builderV2TestOwner, all)
	expect("owner deleted", bob, all, http.MethodPost, "/snapshots", http.StatusCreated)

	// Nobody but the owner, or a role that may delete it, deletes it. Its
	// shares go with it.
	expect("recipient delete", bob, all, http.MethodDelete, "", http.StatusForbidden)
	expect("owner delete", builderV2TestOwner, all, http.MethodDelete, "", http.StatusNoContent)
	expect("deleted", bob, all, http.MethodGet, "", http.StatusNotFound)
	expect("deleted", builderV2TestOwner, all, http.MethodGet, "/shares", http.StatusNotFound)
}

// checkBuilderShareOwnerChange checks the owner, with the given role, may not
// share its draft but is still told whom it is shared with.
func checkBuilderShareOwnerChange(t *testing.T, fixture *builderShareFixture, owner string, role *rbac.Role) {
	t.Helper()

	recorder := fixture.as(owner, role, http.MethodGet, "")

	var draft builderDraftResponse

	fixture.harness.decode(recorder, &draft)

	if recorder.Code != http.StatusOK || draft.CanShare || len(draft.Shares) != 1 {
		t.Fatalf("owner GET: status = %d, canShare %t, shares %v; want its shares, and no sharing",
			recorder.Code, draft.CanShare, draft.Shares)
	}
}

// raceBuilderShares runs race once, the next time the draft record is about
// to be written, between the read of the draft that authorized that write
// and the write itself.
func raceBuilderShares(fixture *builderShareFixture, race func()) *bool {
	raced := new(bool)

	fixture.harness.store.BeforeUpdate = func(namespace, key string) error {
		if namespace != bapi.NamespaceDrafts || key != fixture.id || *raced {
			return nil
		}

		*raced = true

		race()

		return nil
	}

	return raced
}

// TestBuilderV2ShareRevokeRacesSave removes a share while its recipient's
// save, or undo, is being written: it fails, and the draft is left as the
// removal left it.
func TestBuilderV2ShareRevokeRacesSave(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	all := builderShareRole(builderShareConfigVerbs)

	// Two snapshots, so there is something to undo.
	if code := fixture.as(builderV2TestOwner, all, http.MethodPost, "/snapshots").Code; code != http.StatusCreated {
		t.Fatalf("owner save: status = %d, want %d", code, http.StatusCreated)
	}

	for _, route := range []struct{ method, route string }{
		{http.MethodPost, "/snapshots"},
		{http.MethodPatch, "/cursor"},
	} {
		fixture.harness.store.BeforeUpdate = nil
		fixture.share(builderV2TestPeer + ":edit")

		var revoked *bapi.DraftMetadata

		raced := raceBuilderShares(fixture, func() {
			if recorder := fixture.put(fixture.meta().SharesETag(), builderShareBody(t)); recorder.Code != http.StatusOK {
				t.Errorf("revoke: status = %d: %s", recorder.Code, recorder.Body)
			}

			revoked = fixture.meta()
		})

		recorder := fixture.as(builderV2TestPeer, all, route.method, route.route)

		meta := fixture.meta()

		if !*raced || recorder.Code != http.StatusConflict || meta.Revision != revoked.Revision ||
			len(meta.History) != 2 || meta.Cursor != 1 {
			t.Fatalf("%s: raced %t, status = %d, history %d at %d; want a conflict and the draft as revoked: %s",
				route.route, *raced, recorder.Code, len(meta.History), meta.Cursor, recorder.Body)
		}

		if code := fixture.as(builderV2TestPeer, all, http.MethodGet, "").Code; code != http.StatusNotFound {
			t.Fatalf("after the revoke: status = %d, want %d", code, http.StatusNotFound)
		}
	}
}

// TestBuilderV2ShareRacesSave changes who a draft is shared with while a
// recipient's save lands: both are kept, and the change needs no retry from
// the owner. Two changes of the list racing each other are a conflict.
func TestBuilderV2ShareRacesSave(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	fixture.share(builderV2TestPeer + ":edit")

	all := builderShareRole(builderShareConfigVerbs)

	raced := raceBuilderShares(fixture, func() {
		if code := fixture.as(builderV2TestPeer, all, http.MethodPost, "/snapshots").Code; code != http.StatusCreated {
			t.Errorf("save: status = %d, want %d", code, http.StatusCreated)
		}
	})

	recorder := fixture.put(`"shares-1"`, builderShareBody(t, builderV2TestPeer+":edit", builderShareCarol+":view"))

	var response builderSharesResponse

	fixture.harness.decode(recorder, &response)

	meta := fixture.meta()

	if !*raced || recorder.Code != http.StatusOK || len(meta.History) != 2 || meta.SharingVersion() != 2 ||
		response.Draft == nil || response.Draft.ETag != meta.ETag() {
		t.Fatalf("raced %t, status = %d, history %d, sharing version %d; want both kept: %s",
			*raced, recorder.Code, len(meta.History), meta.SharingVersion(), recorder.Body)
	}

	raced = raceBuilderShares(fixture, func() { fixture.share(builderShareCarol + ":edit") })

	recorder = fixture.put(`"shares-2"`, builderShareBody(t))
	if !*raced || recorder.Code != http.StatusPreconditionFailed || recorder.Header().Get("ETag") != `"shares-3"` {
		t.Fatalf("raced %t, status = %d with ETag %q, want %d with \"shares-3\": %s",
			*raced, recorder.Code, recorder.Header().Get("ETag"), http.StatusPreconditionFailed, recorder.Body)
	}

	if users := builderShareUsers(fixture.share(builderShareCarol + ":edit").Shares); len(users) != 1 {
		t.Fatalf("shares = %v, want the change that won kept", users)
	}
}

// TestBuilderV2ShareEditorPublishes asserts an editor publishes a shared
// draft under the editor's own config permissions, recorded as the editor.
func TestBuilderV2ShareEditorPublishes(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	fixture.share(builderV2TestPeer + ":edit")

	recorder := fixture.as(builderV2TestPeer, builderShareRole([]string{"list", "get", "update"}), http.MethodPost, "/publish")
	if recorder.Code != http.StatusForbidden || fixture.harness.configWrites != 0 {
		t.Fatalf("without config create: status = %d, %d configs written; want %d, none",
			recorder.Code, fixture.harness.configWrites, http.StatusForbidden)
	}

	recorder = fixture.as(builderV2TestPeer, builderShareRole(builderShareConfigVerbs), http.MethodPost, "/publish")

	meta := fixture.meta()

	if recorder.Code != http.StatusOK || meta.Publication == nil || meta.Publication.PublishedBy != builderV2TestPeer ||
		meta.LastModifiedBy != builderV2TestPeer {
		t.Fatalf("status = %d, publication %+v; want published by %s: %s",
			recorder.Code, meta.Publication, builderV2TestPeer, recorder.Body)
	}
}

// TestBuilderV2ShareFork asserts a draft may be saved as a new one by
// anyone it is shared with, and by nobody who cannot see it.
func TestBuilderV2ShareFork(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	fixture.share(builderV2TestPeer+":view", builderShareCarol+":view")
	fixture.harness.removeUser(builderShareCarol)

	forkOf := builderV2TestOwner + "/" + fixture.id
	all := builderShareRole(builderShareConfigVerbs)

	for user, status := range map[string]int{
		builderV2TestPeer: http.StatusCreated,
		builderShareCarol: http.StatusNotFound,
		builderShareErin:  http.StatusNotFound,
	} {
		recorder := forkBuilderDraft(t, fixture.harness, user, all, forkOf, bdoc.NewDocument("fork"))
		if recorder.Code != status {
			t.Errorf("%s: status = %d, want %d: %s", user, recorder.Code, status, recorder.Body)
		}
	}

	// Saving it as a new draft still takes config create.
	noCreate := builderShareRole([]string{"list", "get", "update"})

	recorder := forkBuilderDraft(t, fixture.harness, builderV2TestPeer, noCreate, forkOf, bdoc.NewDocument("fork"))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("without config create: status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

// TestBuilderV2ShareAuditLog asserts changes of who a draft is shared with,
// refused recipients, and opening a draft of someone else are logged.
func TestBuilderV2ShareAuditLog(t *testing.T) { //nolint:paralleltest // mutates package options
	fixture := newBuilderShareFixture(t)
	logs := plogtest.Capture(t)
	all := builderShareRole(builderShareConfigVerbs)

	const changed = "builder draft sharing changed"

	fixture.share(builderV2TestPeer+":edit", builderShareCarol+":view")
	fixture.share(builderV2TestPeer+":view", builderShareDave+":edit")
	fixture.share(builderShareDave+":edit", builderV2TestPeer+":view")

	records := logs.Take(t, plogtest.Message(changed))
	if len(records) != 2 {
		t.Fatalf("sharing changes logged %v, want two: a no-op is not logged", records)
	}

	for i, want := range []map[string]any{
		{"version": 1.0, "added": "bob:edit,carol:view", "removed": "", "changed": ""},
		{"version": 2.0, "added": "dave:edit", "removed": "carol", "changed": "bob:edit->view"},
	} {
		record := records[i]

		want["level"], want["type"] = slog.LevelInfo.String(), string(plog.TypeSecurity)
		want["user"], want["owner"], want["draft"] = builderV2TestOwner, builderV2TestOwner, fixture.id

		for key, value := range want {
			if record[key] != value {
				t.Errorf("change %d: %s = %v, want %v", i+1, key, record[key], value)
			}
		}
	}

	fixture.put(`"shares-2"`, builderShareBody(t, "nobody:view"))

	if records := logs.Take(t, plogtest.Message("builder draft share names an unknown user")); len(records) != 1 ||
		records[0]["level"] != slog.LevelWarn.String() || records[0]["type"] != string(plog.TypeSecurity) ||
		records[0]["recipient"] != "nobody" || records[0]["user"] != builderV2TestOwner {
		t.Errorf("unknown recipient logged %v", records)
	}

	fixture.as(builderV2TestOwner, all, http.MethodGet, "")
	fixture.as(builderV2TestPeer, all, http.MethodGet, "")

	if records := logs.Take(t, plogtest.Message("opened shared builder draft")); len(records) != 1 ||
		records[0]["type"] != string(plog.TypeAction) || records[0]["user"] != builderV2TestPeer ||
		records[0]["access"] != "view" || records[0]["via"] != "share" || records[0]["draft"] != fixture.id {
		t.Errorf("opening logged %v, want bob's open only", records)
	}

	for _, denied := range []struct {
		user, method, route, reason string
		role                        *rbac.Role
	}{
		{builderV2TestPeer, http.MethodPost, "/snapshots", "view-only", all},
		{builderV2TestPeer, http.MethodDelete, "", "not-owner", all},
		{builderV2TestPeer, http.MethodGet, "/shares", "not-owner", all},
		{builderShareDave, http.MethodPut, "/shares", "not-owner", all},
		{builderShareErin, http.MethodGet, "", "rbac", builderShareRole(builderShareConfigVerbs, "list")},
		{builderShareErin, http.MethodGet, "", "no-access", all},
	} {
		fixture.as(denied.user, denied.role, denied.method, denied.route)

		records := logs.Take(t, plogtest.Message(builderShareDenied))
		if len(records) != 1 || records[0]["level"] != slog.LevelWarn.String() || records[0]["type"] != string(plog.TypeSecurity) ||
			records[0]["reason"] != denied.reason || records[0]["user"] != denied.user {
			t.Errorf("%s %s %s: logged %v, want reason %q", denied.user, denied.method, denied.route, records, denied.reason)
		}
	}
}
