package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/web/rbac"
)

// builderBetaConfig returns a minimal stored config of the given kind.
func builderBetaConfig(t *testing.T, kind, name string) store.Config {
	t.Helper()

	body := `{
		"apiVersion": "phenix.sandia.gov/v1",
		"kind": "` + kind + `",
		"metadata": {"name": "` + name + `"},
		"spec": {"nodes": [], "vlans": {"aliases": {}}}
	}`

	config, err := store.NewConfigFromJSON([]byte(body))
	if err != nil {
		t.Fatalf("NewConfigFromJSON returned error: %v", err)
	}

	return *config
}

// builderBetaSourceGroups is the grouped JSON view GET /builder/sources
// returns.
type builderBetaSourceGroups struct {
	Topologies  []builderSourceResponse `json:"topologies"`
	Experiments []builderSourceResponse `json:"experiments"`
	Scenarios   []builderSourceResponse `json:"scenarios"`
	Images      []builderSourceResponse `json:"images"`
}

// listSources requests the source listing with the given role.
func builderBetaListSources(
	t *testing.T,
	harness *builderBetaHarness,
	role *rbac.Role,
) builderBetaSourceGroups {
	t.Helper()

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/sources",
		user:   builderBetaTestOwner,
		role:   role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var groups builderBetaSourceGroups

	harness.decode(recorder, &groups)

	return groups
}

// builderBetaSourceNames returns the full names of a source group.
func builderBetaSourceNames(sources []builderSourceResponse) []string {
	names := make([]string, 0, len(sources))

	for _, source := range sources {
		names = append(names, source.FullName)
	}

	return names
}

func TestBuilderBetaListSources(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t,
		builderBetaConfig(t, "Topology", "visible"),
		builderBetaConfig(t, "Topology", "hidden"),
		builderBetaConfig(t, "Experiment", "exp"),
		builderBetaConfig(t, "Scenario", "scenario"),
		builderBetaConfig(t, "Image", "image"),
	)

	// The role may only list one of the topologies, and holds no experiment or
	// scenario permission at all.
	role := builderBetaRole(
		builderBetaPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderBetaPolicy([]string{"topologies"}, []string{"visible"}, []string{"list"}),
	)

	groups := builderBetaListSources(t, harness, &role)

	if names := builderBetaSourceNames(groups.Topologies); len(names) != 1 ||
		names[0] != "Topology/visible" {
		t.Errorf("topologies = %v, want only Topology/visible", names)
	}

	if len(groups.Experiments) != 0 {
		t.Errorf("experiments = %v, want none without the experiments permission",
			builderBetaSourceNames(groups.Experiments))
	}

	if len(groups.Scenarios) != 0 {
		t.Errorf("scenarios = %v, want none without the scenarios permission",
			builderBetaSourceNames(groups.Scenarios))
	}

	// Image configs have no kind specific vocabulary, so the config permission
	// alone admits them.
	if names := builderBetaSourceNames(groups.Images); len(names) != 1 || names[0] != "Image/image" {
		t.Errorf("images = %v, want only Image/image", names)
	}

	if strings.Contains(recorderlessBody(t, harness, &role), "hidden") {
		t.Error("listing leaks a config the caller may not list")
	}
}

// recorderlessBody returns the raw source listing body for leak assertions.
func recorderlessBody(t *testing.T, harness *builderBetaHarness, role *rbac.Role) string {
	t.Helper()

	return harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/sources",
		user:   builderBetaTestOwner,
		role:   role,
	}).Body.String()
}

// TestBuilderBetaListSourcesFull asserts every offered kind is reported for a
// caller holding the permissions for all of them.
func TestBuilderBetaListSourcesFull(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t,
		builderBetaConfig(t, "Topology", "topo"),
		builderBetaConfig(t, "Experiment", "exp"),
		builderBetaConfig(t, "Scenario", "scenario"),
		builderBetaConfig(t, "Image", "image"),
	)

	groups := builderBetaListSources(t, harness, nil)

	checks := []struct {
		name        string
		sources     []builderSourceResponse
		want        string
		generatable bool
	}{
		{name: "topologies", sources: groups.Topologies, want: "Topology/topo", generatable: true},
		{name: "experiments", sources: groups.Experiments, want: "Experiment/exp", generatable: true},
		{name: "scenarios", sources: groups.Scenarios, want: "Scenario/scenario", generatable: false},
		{name: "images", sources: groups.Images, want: "Image/image", generatable: false},
	}

	for _, check := range checks {
		if len(check.sources) != 1 || check.sources[0].FullName != check.want {
			t.Errorf("%s = %v, want only %q",
				check.name, builderBetaSourceNames(check.sources), check.want)

			continue
		}

		if check.sources[0].Generatable != check.generatable {
			t.Errorf("%s generatable = %t, want %t",
				check.name, check.sources[0].Generatable, check.generatable)
		}
	}
}

// TestBuilderBetaListSourcesKindPermission asserts the kind specific list
// permission is required in addition to the config permission.
func TestBuilderBetaListSourcesKindPermission(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t,
		builderBetaConfig(t, "Topology", "topo"),
		builderBetaConfig(t, "Scenario", "allowed"),
		builderBetaConfig(t, "Scenario", "denied"),
	)

	role := builderBetaRole(
		builderBetaPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderBetaPolicy([]string{"scenarios"}, []string{"allowed"}, []string{"list"}),
	)

	groups := builderBetaListSources(t, harness, &role)

	if names := builderBetaSourceNames(groups.Scenarios); len(names) != 1 ||
		names[0] != "Scenario/allowed" {
		t.Errorf("scenarios = %v, want only Scenario/allowed", names)
	}

	if len(groups.Topologies) != 0 {
		t.Errorf("topologies = %v, want none without the topologies permission",
			builderBetaSourceNames(groups.Topologies))
	}
}

// TestBuilderBetaListSourcesIgnoresOtherKinds asserts kinds the builder does
// not offer are never reported.
func TestBuilderBetaListSourcesIgnoresOtherKinds(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t,
		builderBetaConfig(t, "Topology", "topo"),
		builderBetaConfig(t, "User", "someone"),
		builderBetaConfig(t, "Role", "somerole"),
	)

	body := recorderlessBody(t, harness, nil)

	for _, unwanted := range []string{"User/", "Role/", "vlans"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("listing reports %q", unwanted)
		}
	}
}

func TestBuilderBetaGenerateFromStoredSource(t *testing.T) { //nolint:paralleltest // mutates package options
	stored := builderBetaConfig(t, "Topology", "topo")
	harness := newBuilderBetaHarness(t, stored)

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/generate",
		body:   `{"source":"Topology/topo"}`,
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderGenerateResponse

	harness.decode(recorder, &response)

	if response.Source.FullName != "Topology/topo" {
		t.Errorf("source = %q, want %q", response.Source.FullName, "Topology/topo")
	}
	if !response.Source.Stored {
		t.Error("stored source was not marked stored")
	}

	if response.Warnings == nil {
		t.Error("warnings = null, want an array")
	}

	// The generated document round-trips through the draft service, which is
	// the only thing the client can do with it.
	created := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   `{"sourceToken":"Topology/topo","document":` + string(response.Document) + `}`,
		user:   builderBetaTestOwner,
	})

	if created.Code != http.StatusCreated {
		t.Fatalf("draft status = %d, want %d: %s", created.Code, http.StatusCreated, created.Body)
	}

	// Generating never writes a config.
	if len(harness.configs) != 1 || harness.configs[0].FullName() != stored.FullName() {
		t.Errorf("configs = %+v, want the single stored config unchanged", harness.configs)
	}
}

func TestBuilderBetaGenerateFromUpload(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)

	uploads := []struct {
		name    string
		content string
	}{
		{
			name: "json",
			content: `{\"apiVersion\":\"phenix.sandia.gov/v1\",\"kind\":\"Topology\",` +
				`\"metadata\":{\"name\":\"uploaded\"},\"spec\":{\"nodes\":[]}}`,
		},
		{
			name: "yaml",
			content: `apiVersion: phenix.sandia.gov/v1\nkind: Topology\n` +
				`metadata:\n  name: uploaded\nspec:\n  nodes: []\n`,
		},
	}

	for _, upload := range uploads {
		t.Run(upload.name, func(t *testing.T) {
			recorder := harness.do(builderBetaRequest{
				method: http.MethodPost,
				path:   "/builder/generate",
				body:   `{"content":"` + upload.content + `"}`,
				user:   builderBetaTestOwner,
			})

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
			}

			var response builderGenerateResponse

			harness.decode(recorder, &response)

			if response.Source.Name != "uploaded" {
				t.Errorf("source = %q, want %q", response.Source.Name, "uploaded")
			}
			if response.Source.Stored {
				t.Error("uploaded source was marked stored")
			}
		})
	}

	// Nothing was stored.
	if len(harness.configs) != 0 {
		t.Errorf("configs = %+v, want none", harness.configs)
	}
}

func TestBuilderBetaGenerateRequests(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t,
		builderBetaConfig(t, "Topology", "topo"),
		builderBetaConfig(t, "Scenario", "scenario"),
		builderBetaConfig(t, "Image", "image"),
	)

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "neither", body: `{}`, status: http.StatusBadRequest},
		{
			name:   "both",
			body:   `{"source":"Topology/topo","content":"kind: Topology"}`,
			status: http.StatusBadRequest,
		},
		{name: "unnamed source", body: `{"source":"topo"}`, status: http.StatusBadRequest},
		{name: "unknown kind", body: `{"source":"Nope/topo"}`, status: http.StatusBadRequest},
		{name: "missing source", body: `{"source":"Topology/missing"}`, status: http.StatusNotFound},
		{
			name:   "scenario source",
			body:   `{"source":"Scenario/scenario"}`,
			status: http.StatusUnprocessableEntity,
		},
		{
			name:   "image source",
			body:   `{"source":"Image/image"}`,
			status: http.StatusUnprocessableEntity,
		},
		{name: "unparsable upload", body: `{"content":"\tnot: [valid"}`, status: http.StatusUnprocessableEntity},
		{
			name:   "oversized upload",
			body:   `{"content":"` + strings.Repeat("x", bapi.MaxDocumentBytes+1) + `"}`,
			status: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderBetaRequest{
				method: http.MethodPost,
				path:   "/builder/generate",
				body:   tt.body,
				user:   builderBetaTestOwner,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}
		})
	}
}

// TestBuilderBetaGenerateRequiresKindPermission asserts the kind specific list
// permission gates generation as well as listing, so a config the caller
// cannot see is not reachable by naming it directly.
func TestBuilderBetaGenerateRequiresKindPermission(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t, builderBetaConfig(t, "Topology", "topo"))

	role := builderBetaRole(builderBetaPolicy(
		[]string{"configs"},
		[]string{"*", "*/*"},
		[]string{"list", "get"},
	))

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/generate",
		body:   `{"source":"Topology/topo"}`,
		user:   builderBetaTestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusForbidden, recorder.Body)
	}
}

// TestBuilderBetaGenerateFromUnauthorizedSource asserts a caller cannot read a
// config through the builder that it cannot read through /configs.
func TestBuilderBetaGenerateFromUnauthorizedSource(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t, builderBetaConfig(t, "Topology", "secret"))

	role := builderBetaRole(builderBetaPolicy(
		[]string{"configs"},
		[]string{"Topology/public"},
		[]string{"list", "get"},
	))

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/generate",
		body:   `{"source":"Topology/secret"}`,
		user:   builderBetaTestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

// builderBetaPublish stores a published document and its target config,
// standing in for the publish endpoint exercised separately.
func builderBetaPublish(t *testing.T, harness *builderBetaHarness, target string) *bapi.PublishedDocument {
	t.Helper()

	document, err := harness.service.PutPublishedDocument(
		context.Background(),
		bapi.PutPublishedDocumentRequest{
			Target:     target,
			Kind:       "Topology",
			Actor:      builderBetaTestOwner,
			Document:   builderBetaDocument(t, target),
			DraftID:    "",
			SnapshotID: "",
		},
	)
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	reference, err := document.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	config := builderBetaConfig(t, builderBetaKindTopology, target)
	config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
	harness.configs = append(harness.configs, config)

	return document
}

func TestBuilderBetaListDocuments(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	visible := builderBetaPublish(t, harness, "visible")
	hidden := builderBetaPublish(t, harness, "hidden")

	role := builderBetaRole(builderBetaPolicy(
		[]string{"configs"},
		[]string{"Topology/visible"},
		[]string{"list", "get"},
	))

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/documents",
		user:   builderBetaTestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.decode(recorder, &response)

	if len(response.Documents) != 1 || response.Documents[0].ID != visible.ID {
		t.Fatalf("documents = %+v, want only %q", response.Documents, visible.ID)
	}

	if response.Documents[0].Document != nil {
		t.Error("listing carries document bytes")
	}

	if strings.Contains(recorder.Body.String(), hidden.ID) {
		t.Error("listing leaks a document the caller may not read")
	}
}

func TestBuilderBetaGetDocument(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	document := builderBetaPublish(t, harness, "topo")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet,
		path:   "/builder/documents/" + document.ID,
		user:   builderBetaTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderDocumentResponse

	harness.decode(recorder, &response)

	if response.Config != "Topology/topo" {
		t.Errorf("config = %q, want %q", response.Config, "Topology/topo")
	}

	if !strings.Contains(string(response.Document), "topo") {
		t.Error("response does not carry the published document")
	}
}

// TestBuilderBetaGetDocumentNoLeak asserts a document belonging to a config the
// caller may not read is indistinguishable from one that does not exist.
func TestBuilderBetaGetDocumentNoLeak(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderBetaHarness(t)
	document := builderBetaPublish(t, harness, "secret")

	role := builderBetaRole(builderBetaPolicy(
		[]string{"configs"},
		[]string{"Topology/public"},
		[]string{"list", "get"},
	))

	paths := []string{"/builder/documents/" + document.ID, "/builder/documents/missing"}

	for _, path := range paths {
		recorder := harness.do(builderBetaRequest{
			method: http.MethodGet,
			path:   path,
			user:   builderBetaTestOwner,
			role:   &role,
		})

		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}
