package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// builderV2Config returns a minimal stored config of the given kind.
func builderV2Config(t *testing.T, kind, name string) store.Config {
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

// builderV2SourceGroups is the grouped JSON view GET /builder-v2/sources
// returns.
type builderV2SourceGroups struct {
	Topologies  []builderSourceResponse `json:"topologies"`
	Experiments []builderSourceResponse `json:"experiments"`
	Scenarios   []builderSourceResponse `json:"scenarios"`
	Images      []builderSourceResponse `json:"images"`
}

// listSources requests the source listing with the given role.
func builderV2ListSources(
	t *testing.T,
	harness *builderV2Harness,
	role *rbac.Role,
) builderV2SourceGroups {
	t.Helper()

	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/sources",
		user:   builderV2TestOwner,
		role:   role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var groups builderV2SourceGroups

	harness.decode(recorder, &groups)

	return groups
}

// builderV2SourceNames returns the full names of a source group.
func builderV2SourceNames(sources []builderSourceResponse) []string {
	names := make([]string, 0, len(sources))

	for _, source := range sources {
		names = append(names, source.FullName)
	}

	return names
}

func TestBuilderV2ListSources(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t,
		builderV2Config(t, "Topology", "visible"),
		builderV2Config(t, "Topology", "hidden"),
		builderV2Config(t, "Experiment", "exp"),
		builderV2Config(t, "Scenario", "scenario"),
		builderV2Config(t, "Image", "image"),
	)

	// The role may only list one of the topologies, and holds no experiment or
	// scenario permission at all.
	role := builderV2Role(
		builderV2Policy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderV2Policy([]string{"topologies"}, []string{"visible"}, []string{"list"}),
	)

	groups := builderV2ListSources(t, harness, &role)

	if names := builderV2SourceNames(groups.Topologies); len(names) != 1 ||
		names[0] != "Topology/visible" {
		t.Errorf("topologies = %v, want only Topology/visible", names)
	}

	if len(groups.Experiments) != 0 {
		t.Errorf("experiments = %v, want none without the experiments permission",
			builderV2SourceNames(groups.Experiments))
	}

	if len(groups.Scenarios) != 0 {
		t.Errorf("scenarios = %v, want none without the scenarios permission",
			builderV2SourceNames(groups.Scenarios))
	}

	// Image configs have no kind specific vocabulary, so the config permission
	// alone admits them.
	if names := builderV2SourceNames(groups.Images); len(names) != 1 || names[0] != "Image/image" {
		t.Errorf("images = %v, want only Image/image", names)
	}

	if strings.Contains(recorderlessBody(t, harness, &role), "hidden") {
		t.Error("listing leaks a config the caller may not list")
	}
}

// recorderlessBody returns the raw source listing body for leak assertions.
func recorderlessBody(t *testing.T, harness *builderV2Harness, role *rbac.Role) string {
	t.Helper()

	return harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/sources",
		user:   builderV2TestOwner,
		role:   role,
	}).Body.String()
}

// TestBuilderV2ListSourcesFull asserts every offered kind is reported for a
// caller holding the permissions for all of them.
func TestBuilderV2ListSourcesFull(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t,
		builderV2Config(t, "Topology", "topo"),
		builderV2Config(t, "Experiment", "exp"),
		builderV2Config(t, "Scenario", "scenario"),
		builderV2Config(t, "Image", "image"),
	)

	groups := builderV2ListSources(t, harness, nil)

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
				check.name, builderV2SourceNames(check.sources), check.want)

			continue
		}

		if check.sources[0].Generatable != check.generatable {
			t.Errorf("%s generatable = %t, want %t",
				check.name, check.sources[0].Generatable, check.generatable)
		}
	}
}

// TestBuilderV2ListSourcesKindPermission asserts the kind specific list
// permission is required in addition to the config permission.
func TestBuilderV2ListSourcesKindPermission(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t,
		builderV2Config(t, "Topology", "topo"),
		builderV2Config(t, "Scenario", "allowed"),
		builderV2Config(t, "Scenario", "denied"),
	)

	role := builderV2Role(
		builderV2Policy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderV2Policy([]string{"scenarios"}, []string{"allowed"}, []string{"list"}),
	)

	groups := builderV2ListSources(t, harness, &role)

	if names := builderV2SourceNames(groups.Scenarios); len(names) != 1 ||
		names[0] != "Scenario/allowed" {
		t.Errorf("scenarios = %v, want only Scenario/allowed", names)
	}

	if len(groups.Topologies) != 0 {
		t.Errorf("topologies = %v, want none without the topologies permission",
			builderV2SourceNames(groups.Topologies))
	}
}

// TestBuilderV2ListSourcesIgnoresOtherKinds asserts kinds the builder does
// not offer are never reported.
func TestBuilderV2ListSourcesIgnoresOtherKinds(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t,
		builderV2Config(t, "Topology", "topo"),
		builderV2Config(t, "User", "someone"),
		builderV2Config(t, "Role", "somerole"),
	)

	body := recorderlessBody(t, harness, nil)

	for _, unwanted := range []string{"User/", "Role/", "vlans"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("listing reports %q", unwanted)
		}
	}
}

func TestBuilderV2GenerateFromStoredSource(t *testing.T) { //nolint:paralleltest // mutates package options
	stored := builderV2Config(t, "Topology", "topo")
	stored.Metadata.Annotations = store.Annotations{
		"builder-xml": "<mxGraphModel/>",
		"owner":       "alice",
	}
	harness := newBuilderV2Harness(t, stored)

	before := time.Now().UTC().Truncate(time.Second)
	recorder := harness.do(builderV2Request{
		method: http.MethodPost,
		path:   "/builder-v2/generate",
		body:   `{"source":"Topology/topo"}`,
		user:   builderV2TestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderGenerateResponse

	harness.decode(recorder, &response)

	document, err := bdoc.Decode(response.Document)
	if err != nil {
		t.Fatalf("decoding the generated document: %v", err)
	}

	// The source keeps the config's own annotations, and says when it was
	// imported.
	if want := map[string]string{"owner": "alice"}; !reflect.DeepEqual(document.Source.Annotations, want) {
		t.Errorf("annotations = %v, want %v", document.Source.Annotations, want)
	}

	imported, err := time.Parse(time.RFC3339, document.Source.ImportedAt)
	if err != nil || imported.Before(before) || imported.After(time.Now().UTC()) {
		t.Errorf("importedAt = %q, want the time of the request", document.Source.ImportedAt)
	}

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
	created := harness.do(builderV2Request{
		method: http.MethodPost,
		path:   "/builder-v2/drafts",
		body:   `{"sourceToken":"Topology/topo","document":` + string(response.Document) + `}`,
		user:   builderV2TestOwner,
	})

	if created.Code != http.StatusCreated {
		t.Fatalf("draft status = %d, want %d: %s", created.Code, http.StatusCreated, created.Body)
	}

	// Generating never writes a config.
	if len(harness.configs) != 1 || harness.configs[0].FullName() != stored.FullName() {
		t.Errorf("configs = %+v, want the single stored config unchanged", harness.configs)
	}
}

func TestBuilderV2GenerateFromUpload(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

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
				`metadata:\n  name: uploaded\n  annotations:\n    builder-doc: x\n    owner: alice\n` +
				`spec:\n  nodes: []\n`,
		},
	}

	for _, upload := range uploads {
		t.Run(upload.name, func(t *testing.T) {
			recorder := harness.do(builderV2Request{
				method: http.MethodPost,
				path:   "/builder-v2/generate",
				body:   `{"content":"` + upload.content + `"}`,
				user:   builderV2TestOwner,
			})

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
			}

			var response builderGenerateResponse

			harness.decode(recorder, &response)

			if response.Source.Name != "uploaded" {
				t.Errorf("source = %q, want %q", response.Source.Name, "uploaded")
			}

			document, err := bdoc.Decode(response.Document)
			if err != nil {
				t.Fatalf("decoding the generated document: %v", err)
			}

			if upload.name == "yaml" && !reflect.DeepEqual(document.Source.Annotations, map[string]string{"owner": "alice"}) {
				t.Errorf("annotations = %v, want only owner", document.Source.Annotations)
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

// TestBuilderV2GenerateUploadRequiresCreate asserts an uploaded config needs
// the permission POST /configs needs. Uploads are parsed with the same ${NAME}
// environment substitution, so a role that may only read configs, such as the
// default Global Viewer, must not be able to read the server's environment
// through a generated document. Generating from a stored config keeps needing
// only the read permission.
func TestBuilderV2GenerateUploadRequiresCreate(t *testing.T) { //nolint:paralleltest // mutates package options and the environment
	const secret = "builder-upload-secret-value"

	t.Setenv("PHENIX_BUILDER_TEST_SECRET", secret)

	harness := newBuilderV2Harness(t, builderV2Config(t, "Topology", "topo"))

	var (
		// The list/get policy of api/config/default/global-viewer.yml.
		viewer = builderV2Role(builderV2Policy(
			[]string{"*", "*/*"},
			[]string{"*", "*/*"},
			[]string{"list", "get"},
		))
		creator = builderV2Role(builderV2Policy(
			[]string{"configs"},
			[]string{"*", "*/*"},
			[]string{"list", "get", "create"},
		))
		upload = `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Topology\n` +
			`metadata:\n  name: \"${PHENIX_BUILDER_TEST_SECRET}\"\nspec:\n  nodes: []\n"}`
	)

	tests := []struct {
		name   string
		role   rbac.Role
		body   string
		status int
	}{
		{name: "viewer upload", role: viewer, body: upload, status: http.StatusForbidden},
		{name: "viewer stored source", role: viewer, body: `{"source":"Topology/topo"}`, status: http.StatusOK},
		{name: "creator upload", role: creator, body: upload, status: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderV2Request{
				method: http.MethodPost,
				path:   "/builder-v2/generate",
				body:   tt.body,
				user:   builderV2TestOwner,
				role:   &tt.role,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}

			if tt.status == http.StatusForbidden && strings.Contains(recorder.Body.String(), secret) {
				t.Error("a refused upload disclosed the server environment")
			}
		})
	}
}

func TestBuilderV2GenerateRequests(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t,
		builderV2Config(t, "Topology", "topo"),
		builderV2Config(t, "Scenario", "scenario"),
		builderV2Config(t, "Image", "image"),
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
			recorder := harness.do(builderV2Request{
				method: http.MethodPost,
				path:   "/builder-v2/generate",
				body:   tt.body,
				user:   builderV2TestOwner,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}
		})
	}
}

// TestBuilderV2GenerateRequiresKindPermission asserts the kind specific list
// permission gates generation as well as listing, so a config the caller
// cannot see is not reachable by naming it directly.
func TestBuilderV2GenerateRequiresKindPermission(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t, builderV2Config(t, "Topology", "topo"))

	role := builderV2Role(builderV2Policy(
		[]string{"configs"},
		[]string{"*", "*/*"},
		[]string{"list", "get"},
	))

	recorder := harness.do(builderV2Request{
		method: http.MethodPost,
		path:   "/builder-v2/generate",
		body:   `{"source":"Topology/topo"}`,
		user:   builderV2TestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusForbidden, recorder.Body)
	}
}

// TestBuilderV2GenerateFromUnauthorizedSource asserts a caller cannot read a
// config through the builder that it cannot read through /configs.
func TestBuilderV2GenerateFromUnauthorizedSource(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t, builderV2Config(t, "Topology", "secret"))

	role := builderV2Role(builderV2Policy(
		[]string{"configs"},
		[]string{"Topology/public"},
		[]string{"list", "get"},
	))

	recorder := harness.do(builderV2Request{
		method: http.MethodPost,
		path:   "/builder-v2/generate",
		body:   `{"source":"Topology/secret"}`,
		user:   builderV2TestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

// TestBuilderV2GenerateResolvesIncludes asserts included topologies are
// resolved from the store under the caller's own permissions: a topology the
// caller may not read, or a file path, is reported instead of shown.
func TestBuilderV2GenerateResolvesIncludes(t *testing.T) { //nolint:paralleltest // mutates package options
	node := func(hostname string) map[string]any {
		return map[string]any{
			"type":    "VirtualMachine",
			"general": map[string]any{"hostname": hostname},
			"network": map[string]any{
				"interfaces": []any{map[string]any{"name": "eth0", "vlan": "EXP"}},
			},
		}
	}

	root := builderV2Config(t, "Topology", "root")
	root.Spec = map[string]any{
		"nodes":             []any{node("root-host")},
		"includeTopologies": []any{"visible", "secret", "../outside.yml"},
	}

	visible := builderV2Config(t, "Topology", "visible")
	visible.Spec = map[string]any{"nodes": []any{node("visible-host")}}

	secret := builderV2Config(t, "Topology", "secret")
	secret.Spec = map[string]any{"nodes": []any{node("secret-host")}}

	harness := newBuilderV2Harness(t, root, visible, secret)

	role := builderV2Role(
		builderV2Policy([]string{"configs"}, []string{"Topology/root", "Topology/visible"}, []string{"list", "get"}),
		builderV2Policy([]string{"topologies"}, []string{"root", "visible", "secret"}, []string{"list"}),
	)

	recorder := harness.do(builderV2Request{
		method: http.MethodPost,
		path:   "/builder-v2/generate",
		body:   `{"source":"Topology/root"}`,
		user:   builderV2TestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderGenerateResponse

	harness.decode(recorder, &response)

	body := string(response.Document)
	if !strings.Contains(body, `"includedFrom":"visible"`) || strings.Contains(body, "secret-host") {
		t.Fatalf("document = %s, want visible-host included and secret-host left out", body)
	}

	for _, want := range []string{
		`included topology "secret" could not be read and its nodes are not shown: you are not allowed to read it`,
		`included topology "../outside.yml" could not be read and its nodes are not shown: ` +
			`the Builder reads included topologies from the config store only, not from files`,
		"Added 1 node from included topology visible (1 node).",
	} {
		if !strings.Contains(strings.Join(response.Warnings, "\n"), want) {
			t.Errorf("warnings = %q, want one containing %q", response.Warnings, want)
		}
	}
}

// builderV2Publish stores a published document and its target config,
// standing in for the publish endpoint exercised separately.
func builderV2Publish(t *testing.T, harness *builderV2Harness, target string) *bapi.PublishedDocument {
	t.Helper()

	document, err := harness.service.PutPublishedDocument(
		context.Background(),
		bapi.PutPublishedDocumentRequest{
			Target:     target,
			Kind:       "Topology",
			Actor:      builderV2TestOwner,
			Document:   builderV2Document(t, target),
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

	config := builderV2Config(t, builderV2KindTopology, target)
	config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
	harness.configs = append(harness.configs, config)

	return document
}

func TestBuilderV2ListDocuments(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	visible := builderV2Publish(t, harness, "visible")
	hidden := builderV2Publish(t, harness, "hidden")

	role := builderV2Role(builderV2Policy(
		[]string{"configs"},
		[]string{"Topology/visible"},
		[]string{"list", "get"},
	))

	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/documents",
		user:   builderV2TestOwner,
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

// TestBuilderV2ListDocumentsReadsEachKindOnce lists current documents among
// many stale and orphaned ones: the listing lists each kind of config once
// and gets none, and lists exactly the documents a get answers.
func TestBuilderV2ListDocumentsReadsEachKindOnce(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	const leftovers = 20

	targets := []string{"a", "b", "c", "hidden"}
	documents := make([]*bapi.PublishedDocument, 0, len(targets)+1+2*leftovers)
	current := map[string]bool{}

	for _, target := range targets {
		document := builderV2Publish(t, harness, target)
		documents = append(documents, document)
		current[document.ID] = target != "hidden"
	}

	experiment := builderV2PutDocument(t, harness, kindExperiment, "exp", "exp")
	documents = append(documents, experiment)
	current[experiment.ID] = true

	reference, err := experiment.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	config := builderV2Config(t, kindExperiment, "exp")
	config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
	harness.configs = append(harness.configs, config)

	for i := range leftovers {
		stale := builderV2PutDocument(t, harness, builderV2KindTopology, "a", fmt.Sprintf("a-%d", i))
		orphaned := builderV2PutDocument(t, harness, builderV2KindTopology, fmt.Sprintf("gone-%d", i), "gone")
		documents = append(documents, stale, orphaned)
	}

	role := builderV2Role(builderV2Policy(
		[]string{"configs"},
		[]string{"Topology/a", "Topology/b", "Topology/c", "Topology/gone-*", "Experiment/exp"},
		[]string{"list", "get"},
	))

	harness.configLists, harness.configGets = nil, 0

	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/documents",
		user:   builderV2TestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	lists := slices.Sorted(slices.Values(harness.configLists))
	if !slices.Equal(lists, []string{kindExperiment, builderV2KindTopology}) || harness.configGets != 0 {
		t.Fatalf("the listing listed %q and got %d configs, want each kind listed once and none got",
			harness.configLists, harness.configGets)
	}

	var response struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.decode(recorder, &response)

	listed := map[string]bool{}
	for _, document := range response.Documents {
		listed[document.ID] = true
	}

	for _, document := range documents {
		got := harness.do(builderV2Request{
			method: http.MethodGet,
			path:   "/builder-v2/documents/" + document.ID,
			user:   builderV2TestOwner,
			role:   &role,
		}).Code

		if listed[document.ID] != current[document.ID] || (got == http.StatusOK) != current[document.ID] {
			t.Errorf("document %s of %s: listed %t, get %d; want listed and got only when current and allowed",
				document.ID, document.Target, listed[document.ID], got)
		}
	}
}

func TestBuilderV2GetDocument(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	document := builderV2Publish(t, harness, "topo")

	recorder := harness.do(builderV2Request{
		method: http.MethodGet,
		path:   "/builder-v2/documents/" + document.ID,
		user:   builderV2TestOwner,
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

// TestBuilderV2GetDocumentNoLeak asserts a document belonging to a config the
// caller may not read is indistinguishable from one that does not exist.
func TestBuilderV2GetDocumentNoLeak(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	document := builderV2Publish(t, harness, "secret")

	role := builderV2Role(builderV2Policy(
		[]string{"configs"},
		[]string{"Topology/public"},
		[]string{"list", "get"},
	))

	paths := []string{"/builder-v2/documents/" + document.ID, "/builder-v2/documents/missing"}

	for _, path := range paths {
		recorder := harness.do(builderV2Request{
			method: http.MethodGet,
			path:   path,
			user:   builderV2TestOwner,
			role:   &role,
		})

		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}

// builderV2PutDocument stores a published document of a target that no
// config references yet.
func builderV2PutDocument(t *testing.T, harness *builderV2Harness, kind, target, content string) *bapi.PublishedDocument {
	t.Helper()

	document, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
		Target:     target,
		Kind:       kind,
		Actor:      builderV2TestOwner,
		Document:   builderV2Document(t, content),
		DraftID:    "",
		SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	return document
}

// TestBuilderV2DeleteDocument deletes a published topology: the config
// the document is current for and every document of that topology are gone,
// and nothing of another topology is touched.
func TestBuilderV2DeleteDocument(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	earlier := builderV2PutDocument(t, harness, builderV2KindTopology, "topo", "earlier")
	document := builderV2Publish(t, harness, "topo")
	other := builderV2Publish(t, harness, "other")

	remove := func() int {
		return harness.do(builderV2Request{
			method: http.MethodDelete,
			path:   "/builder-v2/documents/" + document.ID,
			user:   builderV2TestOwner,
		}).Code
	}

	if status := remove(); status != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", status, http.StatusNoContent)
	}

	if _, err := harness.getConfig("Topology/topo"); !errors.Is(err, store.ErrNotExist) {
		t.Fatalf("deleted topology: error = %v, want it gone", err)
	}

	for _, id := range []string{document.ID, earlier.ID} {
		if _, err := harness.service.GetPublishedDocument(t.Context(), id); !errors.Is(err, bapi.ErrNotFound) {
			t.Fatalf("document %s: error = %v, want it gone", id, err)
		}
	}

	if _, err := harness.getConfig("Topology/other"); err != nil {
		t.Fatalf("another topology was deleted: %v", err)
	}

	if _, err := harness.service.GetPublishedDocument(t.Context(), other.ID); err != nil {
		t.Fatalf("another topology's document was deleted: %v", err)
	}

	if status := remove(); status != http.StatusNotFound {
		t.Fatalf("deleting again: status = %d, want %d", status, http.StatusNotFound)
	}
}

// builderV2TestStore is a phenix store that keeps configs in a BoltDB and
// records in memory, so a test can fail the removal of a document's content.
type builderV2TestStore struct {
	*memrecord.Store
	builderV2TestConfigs
}

// builderV2TestConfigs gives builderV2TestStore its config methods. Embedded
// a level deeper than the record store, it never has its record methods
// promoted.
type builderV2TestConfigs struct{ store.Store }

// newBuilderV2StoreHarness returns a harness serving the Builder v2 routes,
// and the /configs routes that list, get, create, update and delete a
// config, from a BoltDB phenix store of its own, where the config hooks run
// as they do in production. With records, the store keeps its records there
// instead.
func newBuilderV2StoreHarness(t *testing.T, records *memrecord.Store) *builderV2Harness {
	t.Helper()

	builderV2SetFeatures(t, builderV2Feature)

	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	phenix := db
	if records != nil {
		phenix = &builderV2TestStore{Store: records, builderV2TestConfigs: builderV2TestConfigs{Store: db}}
	}

	previous := store.DefaultStore
	store.DefaultStore = phenix //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store

	service, err := bapi.New()
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	router, api := newBuilderV2Router()
	api.Handle("/configs", weberror.ErrorHandler(GetConfigs)).Methods(http.MethodGet)
	api.Handle("/configs", weberror.ErrorHandler(CreateConfig)).Methods(http.MethodPost)
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(GetConfig)).Methods(http.MethodGet)
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(UpdateConfig)).Methods(http.MethodPut)
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(DeleteConfig)).Methods(http.MethodDelete)

	files := filepath.Join(t.TempDir(), "phenix")

	if err := registerBuilderV2Routes(api, withBuilderV2DocumentFiles(files, filepath.Join(files, "mounts"))); err != nil {
		t.Fatalf("registerBuilderV2Routes returned error: %v", err)
	}

	return &builderV2Harness{t: t, router: router, service: service, store: records, files: files}
}

// publishTopology stores two documents of target in the phenix store and the
// topology referencing the second, as publishing twice does.
func (h *builderV2Harness) publishTopology(target string) []*bapi.PublishedDocument {
	h.t.Helper()

	contents := []string{target + "-v1", target + "-v2"}
	documents := make([]*bapi.PublishedDocument, 0, len(contents))

	for _, content := range contents {
		document, err := h.service.PutPublishedDocument(h.t.Context(), bapi.PutPublishedDocumentRequest{
			Target: target, Kind: builderV2KindTopology, Actor: builderV2TestOwner,
			Document: builderV2Document(h.t, content),
		})
		if err != nil {
			h.t.Fatalf("PutPublishedDocument returned error: %v", err)
		}

		documents = append(documents, document)
	}

	reference, err := documents[1].Reference().EncodeReference()
	if err != nil {
		h.t.Fatalf("EncodeReference returned error: %v", err)
	}

	config := builderV2Config(h.t, builderV2KindTopology, target)
	config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}

	if err := store.Create(&config); err != nil {
		h.t.Fatalf("storing topology %s returned error: %v", target, err)
	}

	return documents
}

// assertDocumentsGone fails unless none of the published documents is stored.
func (h *builderV2Harness) assertDocumentsGone(documents []*bapi.PublishedDocument) {
	h.t.Helper()

	for _, document := range documents {
		if _, err := h.service.GetPublishedDocument(h.t.Context(), document.ID); !errors.Is(err, bapi.ErrNotFound) {
			h.t.Errorf("document %s of %s: error = %v, want it gone", document.ID, document.Target, err)
		}
	}
}

// renameBody returns the body of the PUT /configs that renames the stored
// topology name as renamed, keeping everything else it holds.
func (h *builderV2Harness) renameBody(name, renamed string) string {
	h.t.Helper()

	stored, err := config.Get(builderV2KindTopology+"/"+name, false)
	if err != nil {
		h.t.Fatalf("getting topology %s returned error: %v", name, err)
	}

	stored.Metadata.Name = renamed

	body, err := json.Marshal(stored)
	if err != nil {
		h.t.Fatalf("encoding topology %s returned error: %v", name, err)
	}

	return string(body)
}

// TestBuilderV2DocumentsDeletedWithTopology deletes published topologies
// from a BoltDB phenix store, through DELETE /configs and through the Builder
// v2 route, which has the Topology config hook leave the documents to it:
// either way the documents are gone, and the route answers without a warning.
func TestBuilderV2DocumentsDeletedWithTopology(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	harness := newBuilderV2StoreHarness(t, nil)

	viaConfigs := harness.publishTopology("via-configs")
	viaBuilder := harness.publishTopology("via-builder")
	kept := harness.publishTopology("kept")

	recorder := harness.do(builderV2Request{
		method: http.MethodDelete, path: "/configs/topology/via-configs", user: builderV2TestOwner,
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE /configs: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	harness.assertDocumentsGone(viaConfigs)

	recorder = harness.do(builderV2Request{
		method: http.MethodDelete, path: "/builder-v2/documents/" + viaBuilder[1].ID, user: builderV2TestOwner,
	})
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Warning") != "" {
		t.Fatalf("DELETE /builder-v2/documents: status = %d, warning %q; want %d and none: %s",
			recorder.Code, recorder.Header().Get("Warning"), http.StatusNoContent, recorder.Body)
	}

	harness.assertDocumentsGone(viaBuilder)

	for _, name := range []string{"Topology/via-configs", "Topology/via-builder"} {
		if _, err := config.Get(name, false); !errors.Is(err, store.ErrNotExist) {
			t.Errorf("%s: error = %v, want it deleted", name, err)
		}
	}

	if _, _, err := harness.service.GetPublishedDocumentData(t.Context(), kept[1].ID); err != nil {
		t.Errorf("another topology's document: %v, want it kept", err)
	}
}

// TestBuilderV2DeleteDocumentWarnsWithConfigHook deletes a published topology
// through the Builder v2 route, with the real config delete and its Topology
// config hook, when the documents' content cannot be removed: the topology is
// gone, and the answer carries the warning, which the hook alone would only
// log.
func TestBuilderV2DeleteDocumentWarnsWithConfigHook(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	records := memrecord.New()
	harness := newBuilderV2StoreHarness(t, records)
	documents := harness.publishTopology("topo")

	records.FailPrefixDelete = func(string, string) error { return errors.New("injected content delete failure") }

	recorder := harness.do(builderV2Request{
		method: http.MethodDelete, path: "/builder-v2/documents/" + documents[1].ID, user: builderV2TestOwner,
	})

	const warning = `199 phenix "delete published topology succeeded but removing superseded content failed"`

	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Warning") != warning {
		t.Fatalf("status = %d, warning %q; want %d and %q: %s",
			recorder.Code, recorder.Header().Get("Warning"), http.StatusNoContent, warning, recorder.Body)
	}

	if _, err := config.Get("Topology/topo", false); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("topology: error = %v, want it deleted", err)
	}

	harness.assertDocumentsGone(documents)

	if records.Count(bapi.NamespaceChunks) == 0 {
		t.Error("no content is left: the injected failure did not fail its removal")
	}

	// The hook removes the documents of a topology deleted any other way.
	records.FailPrefixDelete = nil
	other := harness.publishTopology("other")

	recorder = harness.do(builderV2Request{
		method: http.MethodDelete, path: "/configs/topology/other", user: builderV2TestOwner,
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE /configs: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	harness.assertDocumentsGone(other)
}

// TestBuilderV2DocumentsDeletedWithRenamedTopology renames a published
// topology with PUT /configs, which stores it under the new name and deletes
// it under the old one: the documents of the old name go, and the renamed
// topology names none, so it is a topology like any other until it is
// published again.
func TestBuilderV2DocumentsDeletedWithRenamedTopology(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	harness := newBuilderV2StoreHarness(t, nil)

	documents := harness.publishTopology("topo")
	kept := harness.publishTopology("kept")

	recorder := harness.do(builderV2Request{
		method: http.MethodPut, path: "/configs/topology/topo", user: builderV2TestOwner,
		body: harness.renameBody("topo", "renamed"), contentType: "application/json",
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("PUT /configs: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if _, err := config.Get("Topology/topo", false); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("topology under its old name: error = %v, want it gone", err)
	}

	renamed, err := config.Get("Topology/renamed", false)
	if err != nil {
		t.Fatalf("getting the renamed topology returned error: %v", err)
	}

	if renamed.HasAnnotation(bapi.DocumentAnnotation) {
		t.Errorf("the renamed topology names a document: %q", renamed.Metadata.Annotations[bapi.DocumentAnnotation])
	}

	harness.assertDocumentsGone(documents)

	// A PUT that keeps the name keeps the topology published.
	recorder = harness.do(builderV2Request{
		method: http.MethodPut, path: "/configs/topology/kept", user: builderV2TestOwner,
		body: harness.renameBody("kept", "kept"), contentType: "application/json",
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("PUT /configs: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	recorder = harness.do(builderV2Request{method: http.MethodGet, path: "/builder-v2/documents", user: builderV2TestOwner})

	var listed struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.decode(recorder, &listed)

	if len(listed.Documents) != 1 || listed.Documents[0].ID != kept[1].ID {
		t.Errorf("listed documents = %+v, want only %s of the topology kept", listed.Documents, kept[1].ID)
	}

	sources := harness.do(builderV2Request{method: http.MethodGet, path: "/builder-v2/sources", user: builderV2TestOwner})

	var groups builderV2SourceGroups

	harness.decode(sources, &groups)

	for _, source := range groups.Topologies {
		if want := map[string]string{"kept": bapi.DocumentAnnotation, "renamed": ""}[source.Name]; source.Builder != want {
			t.Errorf("source %s builder = %q, want %q", source.Name, source.Builder, want)
		}
	}
}

// TestConfigRoutesWaitForBuilderPublishing asserts that deleting a topology
// and renaming one through /configs wait for the Builder v2 publish lock, so
// no publication of this process is in flight while the topology's documents
// are removed. A config of another kind does not wait.
func TestConfigRoutesWaitForBuilderPublishing(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	harness := newBuilderV2StoreHarness(t, nil)

	harness.publishTopology("deleted")
	harness.publishTopology("topo")

	scenario := builderV2Config(t, "Scenario", "scenario")
	if err := store.Create(&scenario); err != nil {
		t.Fatalf("storing the scenario returned error: %v", err)
	}

	for _, test := range []struct {
		name    string
		request builderV2Request
		waits   bool
	}{
		{
			name:    "deleting a topology",
			request: builderV2Request{method: http.MethodDelete, path: "/configs/topology/deleted"},
			waits:   true,
		},
		{
			name: "renaming a topology",
			request: builderV2Request{
				method: http.MethodPut, path: "/configs/topology/topo",
				body: harness.renameBody("topo", "renamed"), contentType: "application/json",
			},
			waits: true,
		},
		{
			name: "updating a topology",
			request: builderV2Request{
				method: http.MethodPut, path: "/configs/topology/renamed",
				body: harness.renameBody("topo", "renamed"), contentType: "application/json",
			},
		},
		{
			name:    "deleting a scenario",
			request: builderV2Request{method: http.MethodDelete, path: "/configs/scenario/scenario"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.request.user = builderV2TestOwner
			answered := make(chan *httptest.ResponseRecorder, 1)

			builderPublishLock.Lock()

			go func() { answered <- harness.do(test.request) }()

			// A request that waits gets a moment to answer all the same; one
			// that does not wait gets as long as a slow machine may need.
			patience := time.Minute
			if test.waits {
				patience = 250 * time.Millisecond
			}

			var recorder *httptest.ResponseRecorder

			select {
			case recorder = <-answered:
				builderPublishLock.Unlock()

				if test.waits {
					t.Fatalf("answered %d while a publication held the lock", recorder.Code)
				}
			case <-time.After(patience):
				builderPublishLock.Unlock()

				if !test.waits {
					t.Fatal("waited for the publish lock")
				}

				recorder = <-answered
			}

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
			}
		})
	}
}

// TestBuilderV2DeleteDocumentRefusals asserts a published topology is
// deleted only with configs delete for it, only while its document is
// current, and a published experiment never.
func TestBuilderV2DeleteDocumentRefusals(t *testing.T) { //nolint:paralleltest // mutates package options
	readers := builderV2Role(builderV2Policy(
		[]string{"configs"}, []string{"*/*"}, []string{"list", "get"},
	))
	otherDeleter := builderV2Role(builderV2Policy(
		[]string{"configs"}, []string{"*/*"}, []string{"list", "get"},
	), builderV2Policy(
		[]string{"configs"}, []string{"Topology/other"}, []string{"delete"},
	))
	hidden := builderV2Role(builderV2Policy(
		[]string{"configs"}, []string{"Topology/public"}, []string{"list", "get", "delete"},
	))

	for _, test := range []struct {
		name   string
		role   *rbac.Role
		stale  bool
		kind   string
		status int
		reason string
	}{
		{name: "no configs delete", role: &readers, status: http.StatusForbidden},
		{name: "configs delete for another topology", role: &otherDeleter, status: http.StatusForbidden},
		{name: "hidden", role: &hidden, status: http.StatusNotFound},
		{name: "published again since", stale: true, status: http.StatusNotFound},
		{
			name: "experiment", kind: kindExperiment, status: http.StatusUnprocessableEntity,
			reason: "Only published topologies can be deleted here. Delete experiments from the Experiments page.",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderV2Harness(t)
			kind := builderV2KindTopology

			if test.kind != "" {
				kind = test.kind
			}

			document := builderV2PutDocument(t, harness, kind, "topo", "topo")
			current := document

			if test.stale {
				current = builderV2PutDocument(t, harness, kind, "topo", "republished")
			}

			reference, err := current.Reference().EncodeReference()
			if err != nil {
				t.Fatalf("EncodeReference returned error: %v", err)
			}

			config := builderV2Config(t, kind, "topo")
			config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
			harness.configs = append(harness.configs, config)

			recorder := harness.do(builderV2Request{
				method: http.MethodDelete,
				path:   "/builder-v2/documents/" + document.ID,
				user:   builderV2TestOwner,
				role:   test.role,
			})

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body)
			}

			var refusal struct {
				Message string `json:"message"`
			}

			harness.decode(recorder, &refusal)

			if test.reason != "" && refusal.Message != test.reason {
				t.Errorf("message = %q, want %q", refusal.Message, test.reason)
			}

			if _, err := harness.getConfig(config.FullName()); err != nil {
				t.Errorf("a refused delete deleted %s: %v", config.FullName(), err)
			}

			if _, err := harness.service.GetPublishedDocument(t.Context(), document.ID); err != nil {
				t.Errorf("a refused delete deleted document %s: %v", document.ID, err)
			}
		})
	}
}
