package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	bdoc "phenix/types/builder"
)

// builderLayerSetsRoute is the path of the layer set library.
const builderLayerSetsRoute = "/builder/layersets"

// builderLayerSetBody returns the body that adds or replaces a layer set of
// the given name: two layers, one with a color and node matchers.
func builderLayerSetBody(t *testing.T, name string) string {
	t.Helper()

	return builderJSON(t, map[string]any{
		"name":          name,
		"description":   "Levels of the plant.",
		"iconKey":       "plc",
		"matchConflict": "highest",
		"layerOutlines": true,
		"layers": []any{
			map[string]any{
				"id": 2, "label": "Supervisory control", "iconKey": "desktop", "color": "#15803d",
				"matchers": map[string]any{"general.hostname": "^hmi-"},
			},
			map[string]any{"id": 1, "label": "Field devices"},
		},
	})
}

// builderLayerSet decodes one layer set of an answer.
func builderLayerSet(t *testing.T, recorder *httptest.ResponseRecorder) builderLayerSetResponse {
	t.Helper()

	var set builderLayerSetResponse

	if err := json.Unmarshal(recorder.Body.Bytes(), &set); err != nil {
		t.Fatalf("decoding the layer set: %v: %s", err, recorder.Body)
	}

	return set
}

// builderLayerSets decodes the layer set library of an answer.
func builderLayerSets(t *testing.T, recorder *httptest.ResponseRecorder) builderLayerSetListResponse {
	t.Helper()

	var list builderLayerSetListResponse

	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding the layer sets: %v: %s", err, recorder.Body)
	}

	return list
}

// builderLayerSetSummary is what the layer set tests check of a library.
type builderLayerSetSummary struct {
	IDs                  []string
	Builtin              []bool
	DefaultID            *string
	Damaged              int
	MaxSets, MaxMatchers int
}

// summarizeBuilderLayerSets returns the summary of list.
func summarizeBuilderLayerSets(list builderLayerSetListResponse) builderLayerSetSummary {
	summary := builderLayerSetSummary{
		DefaultID: list.DefaultID, Damaged: list.Damaged,
		MaxSets: list.Limits.LayerSets, MaxMatchers: list.Limits.Matchers,
	}

	for _, set := range list.LayerSets {
		summary.IDs = append(summary.IDs, set.ID)
		summary.Builtin = append(summary.Builtin, set.Builtin)
	}

	return summary
}

func TestBuilderLayerSetLibrary(t *testing.T) {
	harness := newBuilderHarness(t)
	alice := harness.as(builderAlice, builderUserRole())

	// The library starts with the built-in set only, and no default.
	list := builderLayerSets(t, alice.mustDo("list", builderRequest{method: http.MethodGet, path: builderLayerSetsRoute}, http.StatusOK))

	wantList := builderLayerSetSummary{
		IDs: []string{bdoc.BuiltinPurdueLayerSetID}, Builtin: []bool{true},
		MaxSets: 200, MaxMatchers: 16,
	}
	if diff := cmp.Diff(wantList, summarizeBuilderLayerSets(list)); diff != "" {
		t.Errorf("GET %s of a new library mismatch (-want +got):\n%s", builderLayerSetsRoute, diff)
	}

	created := alice.mustDo("create", builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "Plant levels"),
	}, http.StatusCreated)

	set := builderLayerSet(t, created)
	if set.ID == "" {
		t.Fatalf("POST %s: id = %q, want one", builderLayerSetsRoute, set.ID)
	}

	layerID := func(id int) *int { return &id }
	want := builderLayerSetResponse{
		ID: set.ID, Name: "Plant levels", Description: "Levels of the plant.", IconKey: "plc",
		MatchConflict: bdoc.MatchConflictHighest, LayerOutlines: true,
		Layers: []bdoc.Layer{
			{
				ID: layerID(2), Label: "Supervisory control", IconKey: "desktop", Color: "#15803d",
				Matchers: map[string]string{"general.hostname": "^hmi-"},
			},
			{ID: layerID(1), Label: "Field devices"},
		},
		Version: 1, ETag: `"1"`, CreatedBy: builderAlice, UpdatedBy: builderAlice,
	}

	if diff := cmp.Diff(want, set, cmpopts.IgnoreFields(builderLayerSetResponse{}, "Created", "Updated")); diff != "" {
		t.Errorf("POST %s mismatch (-want +got):\n%s", builderLayerSetsRoute, diff)
	}

	if set.Created.IsZero() {
		t.Errorf("POST %s: created = %v, want a time", builderLayerSetsRoute, set.Created)
	}

	if got := created.Header().Get("ETag"); got != `"1"` {
		t.Errorf("POST %s: ETag header = %q, want %q", builderLayerSetsRoute, got, `"1"`)
	}

	path := builderLayerSetsRoute + "/" + set.ID

	read := alice.mustDo("get", builderRequest{method: http.MethodGet, path: path}, http.StatusOK)
	if got := builderLayerSet(t, read); got.ID != set.ID {
		t.Errorf("GET %s: id = %q, want %q", path, got.ID, set.ID)
	}

	if got := read.Header().Get("ETag"); got != `"1"` {
		t.Errorf("GET %s: ETag header = %q, want %q", path, got, `"1"`)
	}
}

func TestBuilderLayerSetChanges(t *testing.T) {
	harness := newBuilderHarness(t)
	alice := harness.as(builderAlice, builderUserRole())
	bob := harness.as(builderBob, builderUserRole())

	set := builderLayerSet(t, alice.mustDo("create", builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "Plant levels"),
	}, http.StatusCreated))

	path := builderLayerSetsRoute + "/" + set.ID

	// Any user may change a set another user added.
	changed := bob.mustDo("put", builderRequest{
		method: http.MethodPut, path: path, body: builderLayerSetBody(t, "Plant levels, changed"), ifMatch: `"1"`,
	}, http.StatusOK)

	want := set
	want.Name, want.Version, want.ETag, want.UpdatedBy = "Plant levels, changed", 2, `"2"`, builderBob

	if diff := cmp.Diff(want, builderLayerSet(t, changed), cmpopts.IgnoreFields(builderLayerSetResponse{}, "Updated")); diff != "" {
		t.Errorf("PUT %s as bob mismatch (-want +got):\n%s", path, diff)
	}

	if got := changed.Header().Get("ETag"); got != `"2"` {
		t.Errorf("PUT %s as bob: ETag header = %q, want %q", path, got, `"2"`)
	}

	// A stale If-Match is refused with the current entity tag.
	stale := alice.do(builderRequest{
		method: http.MethodPut, path: path, body: builderLayerSetBody(t, "Other"), ifMatch: `"1"`,
	})
	if err := builderStatusError(stale, http.StatusPreconditionFailed); err != nil {
		t.Errorf("PUT %s at a stale If-Match: %v", path, err)
	}

	if got := builderRefusal(t, stale).Code; got != string(bdoc.CodeLayerSetStale) {
		t.Errorf("PUT %s at a stale If-Match: code = %q, want %q", path, got, bdoc.CodeLayerSetStale)
	}

	if got := stale.Header().Get("ETag"); got != `"2"` {
		t.Errorf("PUT %s at a stale If-Match: ETag header = %q, want %q", path, got, `"2"`)
	}

	if err := builderStatusError(alice.do(builderRequest{
		method: http.MethodPut, path: path, body: builderLayerSetBody(t, "Other"),
	}), http.StatusBadRequest); err != nil {
		t.Errorf("PUT %s without If-Match: %v", path, err)
	}

	// The list holds the new set after the built-in one.
	list := builderLayerSets(t, bob.mustDo("list", builderRequest{method: http.MethodGet, path: builderLayerSetsRoute}, http.StatusOK))

	wantList := []string{bdoc.BuiltinPurdueLayerSetID, set.ID}
	if diff := cmp.Diff(wantList, summarizeBuilderLayerSets(list).IDs); diff != "" {
		t.Errorf("GET %s: sets mismatch (-want +got):\n%s", builderLayerSetsRoute, diff)
	}

	if len(list.LayerSets) == 2 && list.LayerSets[1].Version != 2 {
		t.Errorf("GET %s: version of %s = %d, want 2", builderLayerSetsRoute, set.ID, list.LayerSets[1].Version)
	}

	// A delete needs If-Match, and a stale one is refused with the current
	// entity tag.
	if err := builderStatusError(alice.do(builderRequest{method: http.MethodDelete, path: path}), http.StatusBadRequest); err != nil {
		t.Errorf("DELETE %s without If-Match: %v", path, err)
	}

	staleDelete := alice.do(builderRequest{method: http.MethodDelete, path: path, ifMatch: `"1"`})
	if err := builderStatusError(staleDelete, http.StatusPreconditionFailed); err != nil {
		t.Errorf("DELETE %s at a stale If-Match: %v", path, err)
	}

	if got := builderRefusal(t, staleDelete).Code; got != string(bdoc.CodeLayerSetStale) {
		t.Errorf("DELETE %s at a stale If-Match: code = %q, want %q", path, got, bdoc.CodeLayerSetStale)
	}

	if got := staleDelete.Header().Get("ETag"); got != `"2"` {
		t.Errorf("DELETE %s at a stale If-Match: ETag header = %q, want %q", path, got, `"2"`)
	}

	// Any user may delete a set another user added.
	bob.mustDo("delete", builderRequest{method: http.MethodDelete, path: path, ifMatch: `"2"`}, http.StatusNoContent)

	if err := builderStatusError(alice.do(builderRequest{method: http.MethodGet, path: path}), http.StatusNotFound); err != nil {
		t.Errorf("GET %s after a delete: %v", path, err)
	}

	if err := builderStatusError(
		alice.do(builderRequest{method: http.MethodDelete, path: path, ifMatch: `"2"`}), http.StatusNotFound,
	); err != nil {
		t.Errorf("DELETE %s again: %v", path, err)
	}
}

func TestBuilderBuiltinLayerSetIsReadOnly(t *testing.T) {
	harness := newBuilderHarness(t)
	caller := harness.as(builderAlice, nil)
	path := builderLayerSetsRoute + "/" + bdoc.BuiltinPurdueLayerSetID

	read := caller.mustDo("get", builderRequest{method: http.MethodGet, path: path}, http.StatusOK)
	set := builderLayerSet(t, read)

	type builtin struct {
		Builtin   bool
		Name      string
		Layers    int
		Created   time.Time
		CreatedBy string
	}

	want := builtin{Builtin: true, Name: "Purdue Model", Layers: 7}
	got := builtin{Builtin: set.Builtin, Name: set.Name, Layers: len(set.Layers), Created: set.Created, CreatedBy: set.CreatedBy}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET %s mismatch (-want +got):\n%s", path, diff)
	}

	for _, request := range []builderRequest{
		{method: http.MethodPut, path: path, body: builderLayerSetBody(t, "Mine"), ifMatch: `"1"`},
		{method: http.MethodDelete, path: path, ifMatch: `"1"`},
	} {
		t.Run(request.method, func(t *testing.T) {
			refused := caller.do(request)
			if err := builderStatusError(refused, http.StatusForbidden); err != nil {
				t.Errorf("%s %s: %v", request.method, path, err)
			}

			if got := builderRefusal(t, refused).Code; got != string(bdoc.CodeLayerSetReadOnly) {
				t.Errorf("%s %s: code = %q, want %q", request.method, path, got, bdoc.CodeLayerSetReadOnly)
			}
		})
	}
}

func TestBuilderLayerSetRefusals(t *testing.T) {
	harness := newBuilderHarness(t)
	caller := harness.as(builderAlice, builderUserRole())

	invalid := builderJSON(t, map[string]any{
		"name": "Plant",
		"layers": []any{
			map[string]any{"id": 1, "label": "Control", "matchers": map[string]any{"general.hostname": "(plc"}},
			map[string]any{"id": 1, "label": "Again"},
		},
	})

	refused := caller.do(builderRequest{method: http.MethodPost, path: builderLayerSetsRoute, body: invalid})
	if err := builderStatusError(refused, http.StatusUnprocessableEntity); err != nil {
		t.Errorf("POST %s (an invalid set): %v", builderLayerSetsRoute, err)
	}

	body := builderRefusal(t, refused)
	if body.Code != string(bdoc.CodeLayerSetInvalid) {
		t.Errorf("POST %s (an invalid set): code = %q, want %q", builderLayerSetsRoute, body.Code, bdoc.CodeLayerSetInvalid)
	}

	codes := make([]bdoc.Code, 0, len(body.Issues))
	for _, issue := range body.Issues {
		codes = append(codes, issue.Code)
	}

	wantCodes := []bdoc.Code{bdoc.CodeLayerMatcherPatternInvalid, bdoc.CodeLayerIDDuplicate}
	if diff := cmp.Diff(wantCodes, codes); diff != "" {
		t.Errorf("POST %s (an invalid set): issue codes mismatch (-want +got):\n%s", builderLayerSetsRoute, diff)
	}

	const matcherPath = "layers[0].matchers.general.hostname"

	if len(body.Issues) > 0 && body.Issues[0].Path != matcherPath {
		t.Errorf("POST %s (an invalid set): path of the first issue = %q, want %q", builderLayerSetsRoute, body.Issues[0].Path, matcherPath)
	}

	if err := builderStatusError(caller.do(builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: `{"name":"Plant","layers":[],"colour":"red"}`,
	}), http.StatusBadRequest); err != nil {
		t.Errorf("POST %s (an unknown field): %v", builderLayerSetsRoute, err)
	}

	for _, path := range []string{"/missing", "/a.b", "/builtin:other"} {
		if err := builderStatusError(caller.do(builderRequest{
			method: http.MethodGet, path: builderLayerSetsRoute + path,
		}), http.StatusNotFound); err != nil {
			t.Errorf("GET %s%s: %v", builderLayerSetsRoute, path, err)
		}
	}

	if err := builderStatusError(caller.do(builderRequest{
		method: http.MethodPut, path: builderLayerSetsRoute + "/missing", body: builderLayerSetBody(t, "Plant"), ifMatch: `"1"`,
	}), http.StatusNotFound); err != nil {
		t.Errorf("PUT %s/missing: %v", builderLayerSetsRoute, err)
	}

	if harness.store.Count("builder.layersets") != 0 {
		t.Errorf("the refusals stored %v, want nothing", harness.store.Keys("builder.layersets"))
	}
}

func TestBuilderLayerSetLibraryFull(t *testing.T) {
	harness := newBuilderHarness(t)
	set := bdoc.BuiltinLayerSets()[0].LayerSet

	for i := range 200 {
		set.Name = "Set " + strconv.Itoa(i)

		if _, err := harness.service.CreateLayerSet(t.Context(), builderAlice, set); err != nil {
			t.Fatalf("CreateLayerSet returned error: %v", err)
		}
	}

	refused := harness.as(builderAlice, nil).do(builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "One more"),
	})
	if err := builderStatusError(refused, http.StatusRequestEntityTooLarge); err != nil {
		t.Errorf("POST %s (one set more than the library holds): %v", builderLayerSetsRoute, err)
	}

	if got := builderRefusal(t, refused).Code; got != string(bdoc.CodeLayerSetLibraryFull) {
		t.Errorf("POST %s (one set more than the library holds): code = %q, want %q",
			builderLayerSetsRoute, got, bdoc.CodeLayerSetLibraryFull)
	}
}

func TestBuilderDefaultLayerSet(t *testing.T) {
	harness := newBuilderHarness(t)
	alice := harness.as(builderAlice, builderUserRole())
	bob := harness.as(builderBob, builderUserRole())
	path := builderLayerSetsRoute + "/default"

	readDefault := func(t *testing.T, caller builderCaller) builderLayerSetDefaultResponse {
		t.Helper()

		var answer builderLayerSetDefaultResponse

		recorder := caller.do(builderRequest{method: http.MethodGet, path: path})
		if err := builderStatusError(recorder, http.StatusOK); err != nil {
			t.Fatalf("GET %s as %s: %v", path, caller.user, err)
		}

		if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
			t.Fatalf("decoding the default: %v", err)
		}

		if got := recorder.Header().Get("ETag"); got != answer.ETag {
			t.Errorf("GET %s as %s: ETag header = %q, want the etag %q of the body", path, caller.user, got, answer.ETag)
		}

		return answer
	}

	// choose sets the default with the tag of the current choice.
	choose := func(t *testing.T, caller builderCaller, body string) builderLayerSetDefaultResponse {
		t.Helper()

		var answer builderLayerSetDefaultResponse

		caller.doDecode(builderRequest{
			method: http.MethodPut, path: path, body: body, ifMatch: readDefault(t, caller).ETag,
		}, http.StatusOK, &answer)

		return answer
	}

	purdue := bdoc.BuiltinPurdueLayerSetID

	first := readDefault(t, alice)
	if diff := cmp.Diff(builderLayerSetDefaultResponse{ID: nil, ETag: `"0"`}, first); diff != "" {
		t.Errorf("GET %s at first mismatch (-want +got):\n%s", path, diff)
	}

	chosen := choose(t, alice, `{"id":"`+purdue+`"}`)

	if diff := cmp.Diff(chosen, readDefault(t, alice)); diff != "" || chosen.ID == nil || *chosen.ID != purdue {
		t.Errorf("GET %s after a choice of %s mismatch (-want +got):\n%s", path, purdue, diff)
	}

	// Every user reads the one default of the server, in the list too.
	list := builderLayerSets(t, bob.mustDo("list", builderRequest{method: http.MethodGet, path: builderLayerSetsRoute}, http.StatusOK))
	if diff := cmp.Diff(chosen, builderLayerSetDefaultResponse{ID: list.DefaultID, ETag: list.DefaultETag}); diff != "" {
		t.Errorf("GET %s as bob: default mismatch (-want +got):\n%s", builderLayerSetsRoute, diff)
	}

	if diff := cmp.Diff(chosen, readDefault(t, bob)); diff != "" {
		t.Errorf("GET %s as bob mismatch (-want +got):\n%s", path, diff)
	}

	// A change needs If-Match, and the tag of an older choice is refused with
	// the current one.
	if err := builderStatusError(
		alice.do(builderRequest{method: http.MethodPut, path: path, body: `{"id":null}`}), http.StatusBadRequest,
	); err != nil {
		t.Errorf("PUT %s without If-Match: %v", path, err)
	}

	stale := bob.do(builderRequest{method: http.MethodPut, path: path, body: `{"id":null}`, ifMatch: first.ETag})
	if err := builderStatusError(stale, http.StatusPreconditionFailed); err != nil {
		t.Errorf("PUT %s at a stale If-Match: %v", path, err)
	}

	if got := builderRefusal(t, stale).Code; got != string(bdoc.CodeLayerSetStale) {
		t.Errorf("PUT %s at a stale If-Match: code = %q, want %q", path, got, bdoc.CodeLayerSetStale)
	}

	if got := stale.Header().Get("ETag"); got != chosen.ETag {
		t.Errorf("PUT %s at a stale If-Match: ETag header = %q, want %q", path, got, chosen.ETag)
	}

	refused := alice.do(builderRequest{method: http.MethodPut, path: path, body: `{"id":"missing"}`, ifMatch: chosen.ETag})
	if err := builderStatusError(refused, http.StatusUnprocessableEntity); err != nil {
		t.Errorf("PUT %s (a missing set): %v", path, err)
	}

	if got := builderRefusal(t, refused).Code; got != string(bdoc.CodeLayerSetDefaultUnknown) {
		t.Errorf("PUT %s (a missing set): code = %q, want %q", path, got, bdoc.CodeLayerSetDefaultUnknown)
	}

	for _, body := range []string{`{"id":null}`, `{"id":""}`, `{}`} {
		t.Run("clear with "+body, func(t *testing.T) {
			if got := choose(t, bob, body); got.ID != nil {
				t.Errorf("PUT %s %s = %+v, want no choice", path, body, got)
			}

			if got := readDefault(t, alice).ID; got != nil {
				t.Errorf("GET %s after PUT %s: id = %q, want null", path, body, *got)
			}
		})
	}

	// A set that was deleted is no default.
	created := builderLayerSet(t, alice.mustDo("create", builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "Plant"),
	}, http.StatusCreated))

	choose(t, alice, `{"id":"`+created.ID+`"}`)
	alice.mustDo("delete", builderRequest{
		method: http.MethodDelete, path: builderLayerSetsRoute + "/" + created.ID, ifMatch: created.ETag,
	}, http.StatusNoContent)

	if got := readDefault(t, alice).ID; got != nil {
		t.Errorf("GET %s after its set was deleted: id = %q, want null", path, *got)
	}
}
