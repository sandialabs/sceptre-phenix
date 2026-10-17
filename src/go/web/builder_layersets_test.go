package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

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

func TestBuilderLayerSetLibrary(t *testing.T) {
	harness := newBuilderHarness(t)
	alice := harness.as(builderAlice, builderUserRole())

	// The library starts with the built-in set only, and no default.
	list := builderLayerSets(t, alice.expect("list", builderRequest{method: http.MethodGet, path: builderLayerSetsRoute}, http.StatusOK))

	if len(list.LayerSets) != 1 || list.LayerSets[0].ID != bdoc.BuiltinPurdueLayerSetID || !list.LayerSets[0].Builtin ||
		list.DefaultID != nil || list.Damaged != 0 || list.Limits.LayerSets != 200 || list.Limits.Matchers != 16 {
		t.Fatalf("the new library is %+v", list)
	}

	created := alice.expect("create", builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "Plant levels"),
	}, http.StatusCreated)

	set := builderLayerSet(t, created)
	if set.ID == "" || set.Builtin || set.Version != 1 || set.ETag != `"1"` || created.Header().Get("ETag") != `"1"` ||
		set.CreatedBy != builderAlice || set.UpdatedBy != builderAlice || set.Created.IsZero() ||
		set.MatchConflict != bdoc.MatchConflictHighest || !set.LayerOutlines || len(set.Layers) != 2 ||
		set.Layers[0].Matchers["general.hostname"] != "^hmi-" || set.Layers[0].Color != "#15803d" {
		t.Fatalf("the created set is %+v", set)
	}

	path := builderLayerSetsRoute + "/" + set.ID

	read := alice.expect("get", builderRequest{method: http.MethodGet, path: path}, http.StatusOK)
	if got := builderLayerSet(t, read); got.ID != set.ID || read.Header().Get("ETag") != `"1"` {
		t.Fatalf("GET returned %+v with ETag %q", got, read.Header().Get("ETag"))
	}
}

func TestBuilderLayerSetChanges(t *testing.T) {
	harness := newBuilderHarness(t)
	alice := harness.as(builderAlice, builderUserRole())
	bob := harness.as(builderBob, builderUserRole())

	set := builderLayerSet(t, alice.expect("create", builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "Plant levels"),
	}, http.StatusCreated))

	path := builderLayerSetsRoute + "/" + set.ID

	// Any user may change a set another user added.
	changed := bob.expect("put", builderRequest{
		method: http.MethodPut, path: path, body: builderLayerSetBody(t, "Plant levels, changed"), ifMatch: `"1"`,
	}, http.StatusOK)

	if got := builderLayerSet(t, changed); got.Version != 2 || got.Name != "Plant levels, changed" ||
		got.UpdatedBy != builderBob || got.CreatedBy != builderAlice || changed.Header().Get("ETag") != `"2"` {
		t.Fatalf("PUT returned %+v with ETag %q", got, changed.Header().Get("ETag"))
	}

	// A stale If-Match is refused with the current entity tag.
	stale := alice.expect("stale put", builderRequest{
		method: http.MethodPut, path: path, body: builderLayerSetBody(t, "Other"), ifMatch: `"1"`,
	}, http.StatusPreconditionFailed)

	if got := builderRefusal(t, stale).Code; got != string(bdoc.CodeLayerSetStale) || stale.Header().Get("ETag") != `"2"` {
		t.Fatalf("a stale PUT has the code %q and ETag %q", got, stale.Header().Get("ETag"))
	}

	alice.expect("put without If-Match", builderRequest{
		method: http.MethodPut, path: path, body: builderLayerSetBody(t, "Other"),
	}, http.StatusBadRequest)

	// The list holds the new set after the built-in one.
	list := builderLayerSets(t, bob.expect("list", builderRequest{method: http.MethodGet, path: builderLayerSetsRoute}, http.StatusOK))
	if len(list.LayerSets) != 2 || list.LayerSets[1].ID != set.ID || list.LayerSets[1].Version != 2 {
		t.Fatalf("the library is %+v", list)
	}

	// Any user may delete a set another user added.
	bob.expect("delete", builderRequest{method: http.MethodDelete, path: path}, http.StatusNoContent)
	alice.expect("get after delete", builderRequest{method: http.MethodGet, path: path}, http.StatusNotFound)
	alice.expect("delete again", builderRequest{method: http.MethodDelete, path: path}, http.StatusNotFound)
}

func TestBuilderBuiltinLayerSetIsReadOnly(t *testing.T) {
	harness := newBuilderHarness(t)
	caller := harness.as(builderAlice, nil)
	path := builderLayerSetsRoute + "/" + bdoc.BuiltinPurdueLayerSetID

	read := caller.expect("get", builderRequest{method: http.MethodGet, path: path}, http.StatusOK)
	if set := builderLayerSet(t, read); !set.Builtin || set.Name != "Purdue Model" || len(set.Layers) != 7 ||
		!set.Created.IsZero() || set.CreatedBy != "" {
		t.Fatalf("the built-in set is %+v", set)
	}

	for _, request := range []builderRequest{
		{method: http.MethodPut, path: path, body: builderLayerSetBody(t, "Mine"), ifMatch: `"1"`},
		{method: http.MethodDelete, path: path},
	} {
		refused := caller.expect(request.method, request, http.StatusForbidden)

		if got := builderRefusal(t, refused).Code; got != string(bdoc.CodeLayerSetReadOnly) {
			t.Fatalf("%s of the built-in set has the code %q", request.method, got)
		}
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

	refused := caller.expect("invalid", builderRequest{method: http.MethodPost, path: builderLayerSetsRoute, body: invalid},
		http.StatusUnprocessableEntity)

	body := builderRefusal(t, refused)
	if body.Code != string(bdoc.CodeLayerSetInvalid) || len(body.Issues) != 2 ||
		body.Issues[0].Code != bdoc.CodeLayerMatcherPatternInvalid ||
		body.Issues[0].Path != "layers[0].matchers.general.hostname" || body.Issues[1].Code != bdoc.CodeLayerIDDuplicate {
		t.Fatalf("an invalid set is refused with %+v", body)
	}

	caller.expect("unknown field", builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: `{"name":"Plant","layers":[],"colour":"red"}`,
	}, http.StatusBadRequest)

	for _, path := range []string{"/missing", "/a.b", "/builtin:other"} {
		caller.expect("get "+path, builderRequest{method: http.MethodGet, path: builderLayerSetsRoute + path}, http.StatusNotFound)
	}

	caller.expect("put missing", builderRequest{
		method: http.MethodPut, path: builderLayerSetsRoute + "/missing", body: builderLayerSetBody(t, "Plant"), ifMatch: `"1"`,
	}, http.StatusNotFound)

	if harness.store.Count("builder.layersets") != 0 {
		t.Fatalf("a refusal stored %v", harness.store.Keys("builder.layersets"))
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

	refused := harness.as(builderAlice, nil).expect("one more", builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "One more"),
	}, http.StatusRequestEntityTooLarge)

	if got := builderRefusal(t, refused).Code; got != string(bdoc.CodeLayerSetLibraryFull) {
		t.Fatalf("a full library refuses with the code %q", got)
	}
}

func TestBuilderDefaultLayerSet(t *testing.T) {
	harness := newBuilderHarness(t)
	alice := harness.as(builderAlice, builderUserRole())
	bob := harness.as(builderBob, builderUserRole())
	path := builderLayerSetsRoute + "/default"

	readDefault := func(caller builderCaller) *string {
		t.Helper()

		var answer builderLayerSetDefault

		recorder := caller.expect("get default", builderRequest{method: http.MethodGet, path: path}, http.StatusOK)
		if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
			t.Fatalf("decoding the default: %v", err)
		}

		return answer.ID
	}

	if got := readDefault(alice); got != nil {
		t.Fatalf("the first default is %q, want null", *got)
	}

	alice.expect("choose", builderRequest{
		method: http.MethodPut, path: path, body: `{"id":"` + bdoc.BuiltinPurdueLayerSetID + `"}`,
	}, http.StatusOK)

	if got := readDefault(alice); got == nil || *got != bdoc.BuiltinPurdueLayerSetID {
		t.Fatalf("the default after a choice is %v", got)
	}

	// Every user reads the one default of the server, in the list too.
	list := builderLayerSets(t, bob.expect("list", builderRequest{method: http.MethodGet, path: builderLayerSetsRoute}, http.StatusOK))
	if list.DefaultID == nil || *list.DefaultID != bdoc.BuiltinPurdueLayerSetID {
		t.Fatalf("the list names the default %v", list.DefaultID)
	}

	if got := readDefault(bob); got == nil || *got != bdoc.BuiltinPurdueLayerSetID {
		t.Fatalf("the default another user reads is %v", got)
	}

	refused := alice.expect("choose missing", builderRequest{method: http.MethodPut, path: path, body: `{"id":"missing"}`},
		http.StatusUnprocessableEntity)
	if got := builderRefusal(t, refused).Code; got != string(bdoc.CodeLayerSetDefaultUnknown) {
		t.Fatalf("a missing default refuses with the code %q", got)
	}

	for _, body := range []string{`{"id":null}`, `{"id":""}`, `{}`} {
		bob.expect("clear "+body, builderRequest{method: http.MethodPut, path: path, body: body}, http.StatusOK)

		if got := readDefault(alice); got != nil {
			t.Fatalf("the default after %s is %q, want null", body, *got)
		}
	}

	// A set that was deleted is no default.
	created := builderLayerSet(t, alice.expect("create", builderRequest{
		method: http.MethodPost, path: builderLayerSetsRoute, body: builderLayerSetBody(t, "Plant"),
	}, http.StatusCreated))

	alice.expect("choose", builderRequest{method: http.MethodPut, path: path, body: `{"id":"` + created.ID + `"}`}, http.StatusOK)
	alice.expect("delete", builderRequest{method: http.MethodDelete, path: builderLayerSetsRoute + "/" + created.ID},
		http.StatusNoContent)

	if got := readDefault(alice); got != nil {
		t.Fatalf("the default after its set was deleted is %q, want null", *got)
	}
}
