package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
)

// builtinBuilderRole returns the built-in Builder role, as a new store
// holds it. The store, which holds nothing else, stays the config store
// until the test ends.
func builtinBuilderRole(t *testing.T) *rbac.Role {
	t.Helper()

	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	previous := store.DefaultStore
	store.DefaultStore = db //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store

	if _, err := config.CreateDefault("Role", "builder"); err != nil {
		t.Fatalf("creating the built-in Builder role: %v", err)
	}

	role, err := rbac.RoleFromConfig("Builder")
	if err != nil {
		t.Fatalf("RoleFromConfig returned error: %v", err)
	}

	return role
}

// TestBuiltinBuilderRoleUsesTheBuilder asserts a user with the built-in
// Builder role can use every part of the Builder although the role holds
// configs permissions on topologies, scenarios and experiments only:
// import, upload and legacy conversion, drafts and their sharing,
// publishing a topology with a scenario and an experiment, other users'
// drafts, the template library with its sharing and server-wide
// publishing, and icons.
func TestBuiltinBuilderRoleUsesTheBuilder(t *testing.T) {
	role := builtinBuilderRole(t)

	scenario := scenarioConfig(t, map[string]any{"apps": []any{}})

	digest, err := bdoc.ContentDigest(scenario.Spec)
	if err != nil {
		t.Fatalf("ContentDigest returned error: %v", err)
	}

	harness := newBuilderHarness(t, append(labExperimentFixture(t), *scenario)...)

	for _, user := range []string{builderTestOwner, builderTestPeer} {
		harness.setUser(user, builderShareCreated)
	}

	// do sends request as alice with the Builder role, and fails the test
	// unless it is answered with status.
	do := func(what string, request builderRequest, status int) *httptest.ResponseRecorder {
		t.Helper()

		request.user, request.role = builderTestOwner, role

		recorder := harness.do(request)
		if recorder.Code != status {
			t.Fatalf("%s: status = %d, want %d: %s", what, recorder.Code, status, recorder.Body)
		}

		return recorder
	}

	do("getting the schema", builderRequest{method: http.MethodGet, path: "/schemas/builder/v1"}, http.StatusOK)

	var sources map[string][]builderSourceResponse

	harness.decode(do("listing sources", builderRequest{method: http.MethodGet, path: "/builder/sources"}, http.StatusOK), &sources)

	for key, want := range map[string]string{"topologies": "lab", "experiments": "exp", "scenarios": "sc"} {
		if !slices.ContainsFunc(sources[key], func(source builderSourceResponse) bool { return source.Name == want }) {
			t.Errorf("the %s sources are %v, want %s among them", key, sources[key], want)
		}
	}

	for _, source := range []string{"Topology/lab", "Experiment/exp"} {
		do("importing "+source, builderRequest{
			method: http.MethodPost, path: "/builder/generate", body: `{"source":"` + source + `"}`,
		}, http.StatusOK)
	}

	do("uploading a topology", builderRequest{
		method: http.MethodPost, path: "/builder/generate",
		body: `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: upload\nspec:\n  nodes: []\n"}`,
	}, http.StatusOK)

	do("converting a legacy diagram", builderRequest{
		method: http.MethodPost, path: "/builder/legacy",
		body: asBuilderJSON(t, map[string]string{"content": string(legacySampleFile(t, "sample.xml"))}),
	}, http.StatusOK)

	// A new diagram, or an uploaded one, that uses the stored scenario.
	document := bdoc.NewDocument("plant")
	document.Scenario = &bdoc.ScenarioRef{
		Kind: bdoc.ScenarioRefStored, Name: "sc", Content: nil, APIVersion: scenario.Version, Digest: digest,
	}

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	var draft builderDraftResponse

	harness.decode(do("creating a draft", builderRequest{
		method: http.MethodPost, path: "/builder/drafts", body: `{"document":` + string(data) + `}`,
	}, http.StatusCreated), &draft)

	path := "/builder/drafts/" + builderTestOwner + "/" + draft.ID

	var published builderPublishResponse

	harness.decode(do("publishing a topology, a scenario and an experiment", builderRequest{
		method: http.MethodPost, path: path + "/publish", ifMatch: draft.ETag,
		body: `{"mode":"topology-experiment","topology":{"name":"plant","action":"create"},` +
			`"scenario":{"name":"sc","action":"use"},"experiment":{"name":"plant-exp","action":"create"}}`,
	}, http.StatusOK), &published)

	if _, err := harness.getConfig("Topology/plant"); err != nil || harness.experimentWrites != 1 || published.Experiment == nil {
		t.Fatalf("publishing stored the topology (%v) and %d experiments: %+v", err, harness.experimentWrites, published)
	}

	do("listing published diagrams", builderRequest{method: http.MethodGet, path: "/builder/documents"}, http.StatusOK)

	shares := do("reading who the draft is shared with", builderRequest{method: http.MethodGet, path: path + "/shares"}, http.StatusOK)

	do("sharing the draft", builderRequest{
		method: http.MethodPut, path: path + "/shares", body: builderShareBody(t, builderTestPeer+":edit"),
		ifMatch: shares.Header().Get("ETag"),
	}, http.StatusOK)

	// Another user's draft, which the role lists and opens through
	// builder-drafts.
	other := harness.createDraft(builderTestPeer, "bobs")

	var drafts struct {
		Shared []builderDraftResponse `json:"shared"`
	}

	harness.decode(do("listing drafts", builderRequest{method: http.MethodGet, path: "/builder/drafts"}, http.StatusOK), &drafts)

	if !slices.ContainsFunc(drafts.Shared, func(listed builderDraftResponse) bool { return listed.ID == other.ID }) {
		t.Errorf("the listing does not hold bob's draft: %+v", drafts.Shared)
	}

	do("opening another user's draft", builderRequest{
		method: http.MethodGet, path: "/builder/drafts/" + builderTestPeer + "/" + other.ID,
	}, http.StatusOK)

	// The template library: a template of alice's own, shared with bob and
	// published server-wide.
	iconID, icon := builderTemplateIcon(t, 31)

	var made builderTemplateCreateResponse

	harness.decode(do("adding a template", builderRequest{
		method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/items"),
		body: builderJSON(t, builderTemplateCreateRequest{
			Templates:  []builderTemplateContent{builderTemplateContentOf("PLC", iconID)},
			Collection: nil,
			Icons:      map[string]bdoc.Icon{iconID: icon},
		}),
	}, http.StatusCreated), &made)

	if len(made.Created) != 1 {
		t.Fatalf("adding a template made %+v", made)
	}

	id := made.Created[0].ID

	for what, request := range map[string]builderRequest{
		"sharing the template": {
			method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/share"),
			body: `{"templates":["` + id + `"],"add":["` + builderTestPeer + `"]}`,
		},
		"publishing the template server-wide": {
			method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/publish"),
			body: `{"templates":["` + id + `"],"serverWide":true}`,
		},
	} {
		if recorder := do(what, request, http.StatusOK); recorder.Body.String() != `{"failed":[]}` {
			t.Fatalf("%s answered %s", what, recorder.Body)
		}
	}

	var library builderTemplateLibraryResponse

	listing := do("listing the templates", builderRequest{method: http.MethodGet, path: builderTemplatesRoute}, http.StatusOK)
	harness.decode(listing, &library)

	if !library.CanShare || !library.CanPublish {
		t.Errorf("the library says canShare %t and canPublish %t, want both", library.CanShare, library.CanPublish)
	}

	if peer := harness.templates(builderTestPeer); !slices.ContainsFunc(peer.Templates, func(template builderTemplateResponse) bool {
		return template.ID == id && template.Owner == builderTestOwner
	}) {
		t.Errorf("bob does not list alice's template: %+v", peer.Templates)
	}

	do("adding an icon", builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, "plc", builderIconPNG(t, 1, 1, 31)),
	}, http.StatusCreated)
	do("listing the icons", builderRequest{method: http.MethodGet, path: builderIconsRoute}, http.StatusOK)
}
