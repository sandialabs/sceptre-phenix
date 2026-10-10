package web

import (
	"context"
	"net/http"
	"slices"
	"testing"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
)

// TestBuiltinBuilderRoleUsesTheBuilder asserts a user with the built-in
// Builder role can use every part of the Builder although the role holds
// configs permissions on topologies, scenarios and experiments only:
// import, upload and legacy conversion, drafts and their sharing,
// publishing a topology with a scenario and an experiment, other users'
// drafts, the template library with its sharing and server-wide
// publishing, and icons, also renaming and deleting another user's.
func TestBuiltinBuilderRoleUsesTheBuilder(t *testing.T) {
	role := builtinBuilderRole(t)

	scenario := scenarioConfig(t, map[string]any{"apps": []any{}})

	harness := newBuilderHarness(t, append(labExperimentFixture(t), *scenario)...)

	for _, user := range []string{builderTestOwner, builderTestPeer} {
		harness.setUser(user, builderShareCreated)
	}

	// Every request is alice's, with the Builder role.
	alice := harness.as(builderTestOwner, role)

	alice.expect("getting the schema", builderRequest{method: http.MethodGet, path: "/schemas/builder/v1"}, http.StatusOK)

	var sources map[string][]builderSourceResponse

	harness.decode(alice.expect("listing sources", builderRequest{
		method: http.MethodGet, path: "/builder/sources",
	}, http.StatusOK), &sources)

	for key, want := range map[string]string{"topologies": "lab", "experiments": "exp", "scenarios": "sc"} {
		if !slices.ContainsFunc(sources[key], func(source builderSourceResponse) bool { return source.Name == want }) {
			t.Errorf("the %s sources are %v, want %s among them", key, sources[key], want)
		}
	}

	for _, source := range []string{"Topology/lab", "Experiment/exp"} {
		alice.expect("importing "+source, builderRequest{
			method: http.MethodPost, path: "/builder/generate", body: `{"source":"` + source + `"}`,
		}, http.StatusOK)
	}

	alice.expect("uploading a topology", builderRequest{
		method: http.MethodPost, path: "/builder/generate",
		body: `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: upload\nspec:\n  nodes: []\n"}`,
	}, http.StatusOK)

	alice.expect("converting a legacy diagram", builderRequest{
		method: http.MethodPost, path: "/builder/legacy",
		body: builderJSON(t, map[string]string{"content": string(legacySampleFile(t, "sample.xml"))}),
	}, http.StatusOK)

	// A new diagram, or an uploaded one, that uses the stored scenario.
	document := bdoc.NewDocument("plant")
	document.Scenarios = []string{"sc"}

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	var draft builderDraftResponse

	harness.decode(alice.expect("creating a draft", builderRequest{
		method: http.MethodPost, path: "/builder/drafts", body: `{"document":` + string(data) + `}`,
	}, http.StatusCreated), &draft)

	path := "/builder/drafts/" + builderTestOwner + "/" + draft.ID

	var published builderPublishResponse

	harness.decode(alice.expect("publishing a topology, a scenario and an experiment", builderRequest{
		method: http.MethodPost, path: path + "/publish", ifMatch: draft.ETag,
		body: `{"mode":"topology-experiment","topology":{"name":"plant","action":"create"},` +
			`"scenario":{"name":"sc"},"experiment":{"name":"plant-exp","action":"create"}}`,
	}, http.StatusOK), &published)

	if _, err := harness.getConfig("Topology/plant"); err != nil || harness.experimentWrites != 1 || published.Experiment == nil {
		t.Fatalf("publishing stored the topology (%v) and %d experiments: %+v", err, harness.experimentWrites, published)
	}

	if annotated, err := harness.getConfig("Scenario/sc"); err != nil || annotated.Metadata.Annotations["topology"] != "plant" {
		t.Fatalf("publishing left scenario sc as %+v (%v), want topology plant added", annotated, err)
	}

	alice.expect("listing published diagrams", builderRequest{method: http.MethodGet, path: "/builder/documents"}, http.StatusOK)

	shares := alice.expect("reading who the draft is shared with", builderRequest{
		method: http.MethodGet, path: path + "/shares",
	}, http.StatusOK)

	alice.expect("sharing the draft", builderRequest{
		method: http.MethodPut, path: path + "/shares", body: builderShareBody(t, builderTestPeer+":edit"),
		ifMatch: shares.Header().Get("ETag"),
	}, http.StatusOK)

	// Another user's draft, which the role lists and opens through
	// builder-drafts.
	other := harness.createDraft(builderTestPeer, "bobs")

	var drafts struct {
		Shared []builderDraftResponse `json:"shared"`
	}

	harness.decode(alice.expect("listing drafts", builderRequest{
		method: http.MethodGet, path: "/builder/drafts",
	}, http.StatusOK), &drafts)

	if !slices.ContainsFunc(drafts.Shared, func(listed builderDraftResponse) bool { return listed.ID == other.ID }) {
		t.Errorf("the listing does not hold bob's draft: %+v", drafts.Shared)
	}

	alice.expect("opening another user's draft", builderRequest{
		method: http.MethodGet, path: "/builder/drafts/" + builderTestPeer + "/" + other.ID,
	}, http.StatusOK)

	// The template library: a template of alice's own, shared with bob and
	// published server-wide.
	var made builderTemplateCreateResponse

	harness.decode(alice.expect("adding a template", builderRequest{
		method: http.MethodPost, path: builderLibraryPath(builderTestOwner, "/items"),
		body: builderJSON(t, builderTemplateCreateRequest{
			Templates:  []builderTemplateContent{builderTemplateContentOf("PLC", "plc")},
			Collection: nil,
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
		if recorder := alice.expect(what, request, http.StatusOK); recorder.Body.String() != `{"failed":[]}` {
			t.Fatalf("%s answered %s", what, recorder.Body)
		}
	}

	var library builderTemplateLibraryResponse

	harness.decode(alice.expect("listing the templates", builderRequest{
		method: http.MethodGet, path: builderTemplatesRoute,
	}, http.StatusOK), &library)

	if !library.CanShare || !library.CanPublish {
		t.Errorf("the library says canShare %t and canPublish %t, want both", library.CanShare, library.CanPublish)
	}

	if peer := harness.templates(builderTestPeer); !slices.ContainsFunc(peer.Templates, func(template builderTemplateResponse) bool {
		return template.ID == id && template.Owner == builderTestOwner
	}) {
		t.Errorf("bob does not list alice's template: %+v", peer.Templates)
	}

	alice.expect("adding an icon", builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, "plc", builderIconPNG(t, 1, 1, 31)),
	}, http.StatusCreated)
	alice.expect("listing the icons", builderRequest{method: http.MethodGet, path: builderIconsRoute}, http.StatusOK)
	alice.expect("reading the icon", builderRequest{method: http.MethodGet, path: builderIconsRoute + "/PLC"}, http.StatusOK)

	// Any user's icon, through builder-icons update and delete.
	if _, _, err := harness.service.AddIcon(context.Background(), builderTestPeer, "hmi", builderIconPNG(t, 1, 1, 32)); err != nil {
		t.Fatalf("adding bob's icon: %v", err)
	}

	alice.expect("renaming another user's icon", builderRequest{
		method: http.MethodPut, path: builderIconsRoute + "/hmi", body: `{"name":"hmi-2"}`,
	}, http.StatusOK)
	alice.expect("deleting another user's icon", builderRequest{
		method: http.MethodDelete, path: builderIconsRoute + "/hmi",
	}, http.StatusNoContent)
}
