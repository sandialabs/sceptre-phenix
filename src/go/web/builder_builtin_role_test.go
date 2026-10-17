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

	if recorder := alice.do(builderRequest{method: http.MethodGet, path: "/schemas/builder/v1"}); recorder.Code != http.StatusOK {
		t.Errorf("GET /schemas/builder/v1: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var sources map[string][]builderSourceResponse

	alice.doDecode(builderRequest{
		method: http.MethodGet, path: "/builder/sources",
	}, http.StatusOK, &sources)

	for key, want := range map[string]string{"topologies": "lab", "experiments": "exp", "scenarios": "sc"} {
		if !slices.ContainsFunc(sources[key], func(source builderSourceResponse) bool { return source.Name == want }) {
			t.Errorf("GET /builder/sources: %s = %v, want %s among them", key, sources[key], want)
		}
	}

	for _, source := range []string{"Topology/lab", "Experiment/exp"} {
		if recorder := alice.do(builderRequest{
			method: http.MethodPost, path: "/builder/generate", body: `{"source":"` + source + `"}`,
		}); recorder.Code != http.StatusOK {
			t.Errorf("POST /builder/generate of %s: status = %d, want %d: %s", source, recorder.Code, http.StatusOK, recorder.Body)
		}
	}

	if recorder := alice.do(builderRequest{
		method: http.MethodPost, path: "/builder/generate",
		body: `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: upload\nspec:\n  nodes: []\n"}`,
	}); recorder.Code != http.StatusOK {
		t.Errorf("POST /builder/generate of an upload: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	if recorder := alice.do(builderRequest{
		method: http.MethodPost, path: "/builder/legacy",
		body: builderJSON(t, map[string]string{"content": string(legacySampleFile(t, "sample.xml"))}),
	}); recorder.Code != http.StatusOK {
		t.Errorf("POST /builder/legacy: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

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

	if _, err := harness.getConfig("Topology/plant"); err != nil {
		t.Errorf("after publishing, getConfig(Topology/plant) error: %v", err)
	}

	if harness.experimentWrites != 1 {
		t.Errorf("after publishing, experiment writes = %d, want 1", harness.experimentWrites)
	}

	if published.Experiment == nil {
		t.Errorf("publish answer experiment = nil, want plant-exp: %+v", published)
	}

	if annotated, err := harness.getConfig("Scenario/sc"); err != nil {
		t.Errorf("after publishing, getConfig(Scenario/sc) error: %v", err)
	} else if got := annotated.Metadata.Annotations["topology"]; got != "plant" {
		t.Errorf("after publishing, scenario sc topology annotation = %q, want plant", got)
	}

	if recorder := alice.do(builderRequest{method: http.MethodGet, path: "/builder/documents"}); recorder.Code != http.StatusOK {
		t.Errorf("GET /builder/documents: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	// The libraries, through one icon.
	if recorder := alice.do(builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, "plc", builderIconPNG(t, 1, 1, 31)),
	}); recorder.Code != http.StatusCreated {
		t.Errorf("POST %s: status = %d, want %d: %s", builderIconsRoute, recorder.Code, http.StatusCreated, recorder.Body)
	}
}
