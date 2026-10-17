package web

import (
	"net/http"
	"slices"
	"testing"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
)

// TestBuiltinBuilderRoleUsesTheBuilder asserts the built-in Builder role,
// which holds configs permissions on three kinds only, uses every part of the Builder.
func TestBuiltinBuilderRoleUsesTheBuilder(t *testing.T) {
	role := builtinBuilderRole(t)

	scenario := scenarioConfig(t, map[string]any{"apps": []any{}})

	harness := newBuilderHarness(t, append(labExperimentFixture(t), *scenario)...)

	// Every request is alice's, with the Builder role.
	alice := harness.as(builderAlice, role)

	alice.expect("getting the schema", builderRequest{method: http.MethodGet, path: "/schemas/builder/v1"}, http.StatusOK)

	var sources map[string][]builderSourceResponse

	alice.doDecode(builderRequest{
		method: http.MethodGet, path: "/builder/sources",
	}, http.StatusOK, &sources)

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

	alice.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", body: `{"document":` + string(data) + `}`,
	}, http.StatusCreated, &draft)

	path := "/builder/drafts/" + draft.ID

	var published builderPublishResponse

	alice.doDecode(builderRequest{
		method: http.MethodPost, path: path + "/publish", ifMatch: draft.ETag,
		body: `{"mode":"topology-experiment","topology":{"name":"plant","action":"create"},` +
			`"scenario":{"name":"sc"},"experiment":{"name":"plant-exp","action":"create"}}`,
	}, http.StatusOK, &published)

	if _, err := harness.getConfig("Topology/plant"); err != nil || harness.experimentWrites != 1 || published.Experiment == nil {
		t.Fatalf("publishing stored the topology (%v) and %d experiments: %+v", err, harness.experimentWrites, published)
	}

	if annotated, err := harness.getConfig("Scenario/sc"); err != nil || annotated.Metadata.Annotations["topology"] != "plant" {
		t.Fatalf("publishing left scenario sc as %+v (%v), want topology plant added", annotated, err)
	}

	alice.expect("listing published diagrams", builderRequest{method: http.MethodGet, path: "/builder/documents"}, http.StatusOK)

	// The libraries, through one icon.
	alice.expect("adding an icon", builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, "plc", builderIconPNG(t, 1, 1, 31)),
	}, http.StatusCreated)
}
