package web

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/types"
	bdoc "phenix/types/builder"
	"phenix/util/common"
	"phenix/web/rbac"
)

func createBuilderPublishDraft(
	t *testing.T,
	harness *builderBetaHarness,
	document *bdoc.Document,
	sourceToken ...string,
) builderDraftResponse {
	t.Helper()

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	request := map[string]any{"document": json.RawMessage(data)}
	if len(sourceToken) != 0 {
		request["sourceToken"] = sourceToken[0]
	}

	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encoding draft request: %v", err)
	}

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   string(body),
		user:   builderBetaTestOwner,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("creating publish draft: status %d: %s", recorder.Code, recorder.Body.String())
	}

	var draft builderDraftResponse
	harness.decode(recorder, &draft)

	return draft
}

func TestBuilderBetaPublishUpdatePreservesAnnotations(t *testing.T) { //nolint:paralleltest // mutates feature options
	topology := builderBetaConfig(t, builderBetaKindTopology, "existing")
	topology.Metadata.Annotations = store.Annotations{"keep": "value"}

	digest, err := bdoc.SourceDigest(topology)
	if err != nil {
		t.Fatalf("SourceDigest returned error: %v", err)
	}

	document := bdoc.NewDocument("existing")
	document.Source = &bdoc.Source{
		Kind: bdoc.SourceKindTopology, Name: "existing", APIVersion: topology.Version,
		Digest: digest, UpdatedAt: topology.Metadata.Updated, ImportedAt: "", Topology: "", Warnings: nil,
	}

	harness := newBuilderBetaHarness(t, topology)
	draft := createBuilderPublishDraft(t, harness, document)

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"existing","action":"update"}}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}

	updated, err := harness.getConfig("Topology/existing")
	if err != nil {
		t.Fatalf("updated topology missing: %v", err)
	}
	if updated.Metadata.Annotations["keep"] != "value" {
		t.Fatal("unrelated annotation was not preserved")
	}
	if _, err := bapi.DecodeReference(updated.Metadata.Annotations[bapi.DocumentAnnotation]); err != nil {
		t.Fatalf("builder document annotation invalid: %v", err)
	}
}

func TestBuilderBetaPublishRejectsLegacyAndUnauthorizedTargets(t *testing.T) { //nolint:paralleltest // mutates feature options
	legacy := builderBetaConfig(t, builderBetaKindTopology, "legacy")
	legacy.Metadata.Annotations = store.Annotations{builderBetaXMLAnnotation: "<mxfile/>"}

	harness := newBuilderBetaHarness(t, legacy)
	document := bdoc.NewDocument("legacy")
	draft := createBuilderPublishDraft(t, harness, document)

	legacyResponse := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"legacy","action":"update"}}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if legacyResponse.Code != http.StatusConflict {
		t.Fatalf("legacy status = %d, want %d", legacyResponse.Code, http.StatusConflict)
	}

	role := builderBetaRole(builderBetaPolicy(
		[]string{"configs"},
		[]string{"*"},
		[]string{"update"},
	))
	denied := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"new","action":"create"}}`,
		user:   builderBetaTestOwner, role: &role, ifMatch: draft.ETag,
	})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d, want %d", denied.Code, http.StatusForbidden)
	}
	if harness.configWrites != 0 {
		t.Fatalf("unauthorized publish wrote %d configs", harness.configWrites)
	}
}

func TestBuilderBetaPublishSharedDraftRecordsActor(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestPeer, "shared")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"shared","action":"create"}}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}
	if meta.LastModifiedBy != builderBetaTestOwner ||
		meta.Publication == nil ||
		meta.Publication.PublishedBy != builderBetaTestOwner {
		t.Fatalf("publication audit = %#v", meta)
	}
}

func TestBuilderBetaPublishReportsBroadcastWarning(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t)
	harness.failBroadcastKind = builderBetaKindTopology
	draft := harness.createDraft(builderBetaTestOwner, "broadcast")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"broadcast","action":"create"}}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var response builderPublishResponse
	harness.decode(recorder, &response)
	if !strings.Contains(strings.Join(response.Warnings, " "), "broadcast") {
		t.Fatalf("warnings = %#v, want a broadcast warning", response.Warnings)
	}
	if response.Draft.Publication == nil {
		t.Fatal("broadcast failure prevented draft publication state")
	}
}

func TestBuilderBetaPublishReportsPartialFailure(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "partial")
	harness.failConfigKind = builderBetaKindTopology

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"partial","action":"create"}}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
	}

	var response builderPublishResponse
	harness.decode(recorder, &response)
	if response.Status != "partial" || len(response.Stages) != 2 {
		t.Fatalf("partial response = %#v", response)
	}
	if response.Stages[0].Name != builderPublishStageDocument ||
		response.Stages[1].Status != bapi.PublishFailed {
		t.Fatalf("partial stages = %#v", response.Stages)
	}
	if strings.Contains(recorder.Body.String(), "injected config write failure") {
		t.Fatal("partial response exposed an internal error")
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}
	if meta.Publication != nil {
		t.Fatal("partial publication marked draft clean")
	}
}

func TestBuilderBetaPublishReportsExperimentPartialFailure(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t)
	harness.failExperiment = true
	draft := harness.createDraft(builderBetaTestOwner, "experiment-partial")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body: `{"mode":"topology-experiment","topology":{"name":"partial-topology","action":"create"},` +
			`"experiment":{"name":"partial-experiment","action":"create"}}`,
		user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
	}

	var response builderPublishResponse
	harness.decode(recorder, &response)
	if response.Status != "partial" ||
		len(response.Stages) != 3 ||
		response.Stages[1].Name != builderPublishStageTopology ||
		response.Stages[2].Name != builderPublishStageExperiment ||
		response.Stages[2].Status != bapi.PublishFailed {
		t.Fatalf("partial response = %#v", response)
	}

	if _, err := harness.getConfig("Topology/partial-topology"); err != nil {
		t.Fatalf("successful topology stage was not retained: %v", err)
	}
	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}
	if meta.Publication != nil {
		t.Fatal("experiment partial failure marked draft clean")
	}
}

func TestBuilderBetaPublishValidatesIntentBeforeWriting(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "invalid-intent")

	tests := []string{
		`{}`,
		`{"mode":"unknown","topology":{"name":"topo","action":"create"}}`,
		`{"mode":"topology","topology":{"name":"topo","action":"delete"}}`,
		`{"mode":"topology","topology":{"name":"topo","action":"create"},"experiment":{"name":"exp","action":"create"}}`,
		`{"mode":"topology","topology":{"name":"topo","action":"create","unexpected":true}}`,
		// Names outside the config naming rule, which the experiment would
		// otherwise refuse only after the topology is written.
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
			`"experiment":{"name":"my exp","action":"create"}}`,
		`{"mode":"topology","topology":{"name":"topo/x","action":"create"}}`,
	}

	for _, body := range tests {
		recorder := harness.do(builderBetaRequest{
			method: http.MethodPost,
			path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
			body:   body,
			user:   builderBetaTestOwner, ifMatch: draft.ETag,
		})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want %d: %s", body, recorder.Code, http.StatusBadRequest, recorder.Body.String())
		}
	}

	if harness.configWrites != 0 || harness.store.count(bapi.NamespacePublished) != 0 {
		t.Fatal("invalid publication intent had side effects")
	}
}

// TestBuilderBetaPublishNamesInterfacesWithoutVLAN refuses interfaces with no
// VLAN, and names them in the message, which is what the Publish dialog shows,
// not only in the cause: the first few, by position where the name does not
// tell them apart, and then how many more there are.
func TestBuilderBetaPublishNamesInterfacesWithoutVLAN(t *testing.T) { //nolint:paralleltest // mutates feature options
	document := bdoc.NewDocument("no-vlan")
	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID("host"), Kind: bdoc.NodeKindDevice, Label: "host",
		Device: &bdoc.Device{
			Hostname: "host",
			Spec: map[string]any{
				"type":    "VirtualMachine",
				"general": map[string]any{"hostname": "host"},
				"network": map[string]any{"interfaces": []any{
					map[string]any{"name": "eth0", "vlan": ""},
					map[string]any{"name": ""},
					map[string]any{"name": "eth0"},
					map[string]any{"name": "eth1", "vlan": "  "},
					map[string]any{"name": "eth2"},
					map[string]any{"name": "eth3", "vlan": "EXP"},
				}},
			},
			Interfaces: []bdoc.InterfaceHandle{},
		},
	})

	harness := newBuilderBetaHarness(t)
	draft := createBuilderPublishDraft(t, harness, document)

	refused := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"no-vlan","action":"create"}}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d: %s", refused.Code, http.StatusUnprocessableEntity, refused.Body.String())
	}

	var body struct {
		Message string `json:"message"`
		Cause   string `json:"cause"`
	}
	harness.decode(refused, &body)

	const fix = " has no VLAN: connect it to a network, or type a VLAN for it"

	want := `topology no-vlan cannot be published: ` +
		`interface "eth0" (#1) of device "host"` + fix + `; ` +
		`interface #2 of device "host"` + fix + `; ` +
		`interface "eth0" (#3) of device "host"` + fix + `; ` +
		`2 more interfaces have no VLAN`
	if body.Message != want {
		t.Fatalf("message = %q, want %q", body.Message, want)
	}

	// The cause still lists every one.
	if !strings.Contains(body.Cause, `interface "eth2" of device "host"`+fix) {
		t.Fatalf("cause %q does not name eth2", body.Cause)
	}

	if harness.configWrites != 0 || harness.store.count(bapi.NamespacePublished) != 0 {
		t.Fatal("a refused publication had side effects")
	}
}

func TestBuilderBetaPublishRejectsStaleSource(t *testing.T) { //nolint:paralleltest // mutates feature options
	source := builderBetaConfig(t, builderBetaKindTopology, "source")

	digest, err := bdoc.SourceDigest(source)
	if err != nil {
		t.Fatalf("SourceDigest returned error: %v", err)
	}

	document := bdoc.NewDocument("generated")
	document.Source = &bdoc.Source{
		Kind: bdoc.SourceKindTopology, Name: "source", APIVersion: source.Version,
		Digest: digest, UpdatedAt: source.Metadata.Updated, ImportedAt: "", Topology: "", Warnings: nil,
	}

	harness := newBuilderBetaHarness(t, source)
	draft := createBuilderPublishDraft(t, harness, document)
	harness.configs[0].Spec["changed"] = true

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"generated","action":"create"}}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	if harness.configWrites != 0 || harness.store.count(bapi.NamespacePublished) != 0 {
		t.Fatal("stale source publication had side effects")
	}
}

func TestBuilderBetaPublishUploadedScenarioCreateAndUpdate(t *testing.T) { //nolint:paralleltest // mutates feature options
	tests := []struct {
		name         string
		existing     *store.Config
		action       string
		expected     string
		expectedCode int
	}{
		{name: "create", existing: nil, action: builderPublishActionCreate, expected: "", expectedCode: http.StatusOK},
		{name: "update", existing: scenarioConfig(t, map[string]any{"apps": []any{}}),
			action: builderPublishActionUpdate, expectedCode: http.StatusOK},
		{name: "stale update", existing: scenarioConfig(t, map[string]any{"apps": []any{}}),
			action: builderPublishActionUpdate, expected: "sha256:" + strings.Repeat("0", 64), expectedCode: http.StatusConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := map[string]any{"apps": []any{map[string]any{"name": "ntp"}}}
			contentDigest, err := bdoc.ContentDigest(content)
			if err != nil {
				t.Fatalf("ContentDigest returned error: %v", err)
			}

			configs := []store.Config{}
			expected := tt.expected
			if tt.existing != nil {
				configs = append(configs, *tt.existing)
				if expected == "" {
					expected, err = bdoc.ContentDigest(tt.existing.Spec)
					if err != nil {
						t.Fatalf("ContentDigest returned error: %v", err)
					}
				}
			}

			document := bdoc.NewDocument("with-upload")
			document.Scenario = &bdoc.ScenarioRef{
				Kind: bdoc.ScenarioRefUploaded, Name: "sc", Content: content,
				APIVersion: bdoc.ScenarioAPIVersion(), Digest: contentDigest,
			}

			harness := newBuilderBetaHarness(t, configs...)
			draft := createBuilderPublishDraft(t, harness, document)
			expectedField := ""
			if expected != "" {
				expectedField = `,"expectedDigest":"` + expected + `"`
			}

			recorder := harness.do(builderBetaRequest{
				method: http.MethodPost,
				path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
				body: `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
					`"scenario":{"name":"sc","action":"` + tt.action + `"` + expectedField + `},` +
					`"experiment":{"name":"exp","action":"create"}}`,
				user: builderBetaTestOwner, ifMatch: draft.ETag,
			})
			if recorder.Code != tt.expectedCode {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.expectedCode, recorder.Body.String())
			}

			if tt.expectedCode != http.StatusOK {
				if harness.configWrites != 0 {
					t.Fatalf("stale update wrote %d configs", harness.configWrites)
				}

				return
			}

			scenario, getErr := harness.getConfig("Scenario/sc")
			if getErr != nil {
				t.Fatalf("published scenario missing: %v", getErr)
			}
			if scenario.Metadata.Annotations["topology"] != "topo" {
				t.Fatalf("scenario topology annotation = %q", scenario.Metadata.Annotations["topology"])
			}
			if digest, digestErr := bdoc.ContentDigest(scenario.Spec); digestErr != nil || digest != contentDigest {
				t.Fatalf("published scenario digest = %q, want %q (err: %v)", digest, contentDigest, digestErr)
			}
		})
	}
}

func TestBuilderBetaPublishRejectsUnrelatedExperimentUpdate(t *testing.T) { //nolint:paralleltest // mutates feature options
	experiment := builderBetaConfig(t, kindExperiment, "existing-exp")
	harness := newBuilderBetaHarness(t, experiment)
	draft := harness.createDraft(builderBetaTestOwner, "unrelated")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body: `{"mode":"topology-experiment","topology":{"name":"new-topology","action":"create"},` +
			`"experiment":{"name":"existing-exp","action":"update"}}`,
		user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	if harness.configWrites != 0 || harness.store.count(bapi.NamespacePublished) != 0 {
		t.Fatal("unrelated experiment update had side effects")
	}
}

func TestBuilderBetaPublishRejectsRunningExperimentUpdateBeforeWriting(t *testing.T) { //nolint:paralleltest // mutates feature options
	experiment := builderBetaConfig(t, kindExperiment, "running-exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{}},
		"vlans":    map[string]any{"aliases": map[string]any{"old": 100}},
	}
	experiment.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z"}
	experiment.Metadata.Annotations = store.Annotations{"topology": "source-topology"}

	digest, err := bdoc.SourceDigest(experiment)
	if err != nil {
		t.Fatalf("SourceDigest returned error: %v", err)
	}

	document := bdoc.NewDocument("running")
	document.Source = &bdoc.Source{
		Kind: bdoc.SourceKindExperiment, Name: "running-exp", APIVersion: experiment.Version,
		Digest: digest, UpdatedAt: experiment.Metadata.Updated, ImportedAt: "",
		Topology: "source-topology", Warnings: nil,
	}

	harness := newBuilderBetaHarness(t, experiment)
	draft := createBuilderPublishDraft(t, harness, document, "Experiment/running-exp")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body: `{"mode":"topology-experiment","topology":{"name":"source-topology","action":"create"},` +
			`"experiment":{"name":"running-exp","action":"update"}}`,
		user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	if harness.configWrites != 0 || harness.store.count(bapi.NamespacePublished) != 0 {
		t.Fatal("running experiment update had side effects")
	}
}

func TestBuilderBetaPublishUpdatesSourceExperiment(t *testing.T) { //nolint:paralleltest // mutates feature options
	experiment := builderBetaConfig(t, kindExperiment, "source-exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{}},
		"vlans":    map[string]any{"aliases": map[string]any{}},
	}
	experiment.Metadata.Annotations = store.Annotations{"topology": "source-topology", "keep": "yes"}

	digest, err := bdoc.SourceDigest(experiment)
	if err != nil {
		t.Fatalf("SourceDigest returned error: %v", err)
	}

	document := bdoc.NewDocument("experiment-update")
	document.Source = &bdoc.Source{
		Kind: bdoc.SourceKindExperiment, Name: "source-exp", APIVersion: experiment.Version,
		Digest: digest, UpdatedAt: experiment.Metadata.Updated, ImportedAt: "",
		Topology: "source-topology", Warnings: nil,
	}

	// The schema allows a string for memory, which phenix decodes weakly, and
	// so does the update.
	spec := includeNode("host")
	spec["hardware"].(map[string]any)["memory"] = "4096"
	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID("host"), Kind: bdoc.NodeKindDevice, Label: "host",
		Device: &bdoc.Device{Hostname: "host", Spec: spec, Interfaces: []bdoc.InterfaceHandle{}},
	})

	harness := newBuilderBetaHarness(t, experiment)
	draft := createBuilderPublishDraft(t, harness, document, "Experiment/source-exp")

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body: `{"mode":"topology-experiment","topology":{"name":"source-topology","action":"create"},` +
			`"experiment":{"name":"source-exp","action":"update"}}`,
		user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}

	updated, err := harness.getConfig("Experiment/source-exp")
	if err != nil {
		t.Fatalf("updated experiment missing: %v", err)
	}
	if updated.Metadata.Annotations["topology"] != "source-topology" ||
		updated.Metadata.Annotations["keep"] != "yes" {
		t.Fatalf("updated experiment annotations = %#v", updated.Metadata.Annotations)
	}

	exp, err := types.DecodeExperimentFromConfig(*updated)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig returned error: %v", err)
	}

	if node := exp.Spec.Topology().FindNodeByName("host"); node == nil || node.Hardware().Memory() != 4096 {
		t.Fatalf("experiment node host = %#v, want 4096 MB of memory", node)
	}
}

// scenarioConfig returns a Scenario config named "sc" with the given spec.
func scenarioConfig(t *testing.T, spec map[string]any) *store.Config {
	t.Helper()

	scenario, err := store.NewConfig("Scenario/sc")
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	scenario.Version = bdoc.ScenarioAPIVersion()
	scenario.Spec = spec

	return scenario
}

func TestBuilderBetaPublishStoredScenarioAndExperiment(t *testing.T) { //nolint:paralleltest // mutates feature options
	scenario, err := store.NewConfig("Scenario/sc")
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}
	scenario.Version = bdoc.ScenarioAPIVersion()
	scenario.Spec = map[string]any{"apps": []any{}}
	scenario.Metadata.Annotations = store.Annotations{"topology": "other, other", "keep": "yes"}

	digest, err := bdoc.ContentDigest(scenario.Spec)
	if err != nil {
		t.Fatalf("ContentDigest returned error: %v", err)
	}

	document := bdoc.NewDocument("with-scenario")
	document.Scenario = &bdoc.ScenarioRef{
		Kind: bdoc.ScenarioRefStored, Name: "sc", Content: nil,
		APIVersion: scenario.Version, Digest: digest,
	}

	harness := newBuilderBetaHarness(t, *scenario)
	draft := createBuilderPublishDraft(t, harness, document)

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body: `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
			`"scenario":{"name":"sc","action":"use"},` +
			`"experiment":{"name":"exp","action":"create"}}`,
		user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}

	updated, err := harness.getConfig("Scenario/sc")
	if err != nil {
		t.Fatalf("scenario missing: %v", err)
	}
	if got := updated.Metadata.Annotations["topology"]; got != "other,topo" {
		t.Fatalf("topology annotation = %q, want %q", got, "other,topo")
	}
	if updated.Metadata.Annotations["keep"] != "yes" {
		t.Fatal("scenario annotation was not preserved")
	}
	if harness.experimentWrites != 1 {
		t.Fatalf("experiment writes = %d, want 1", harness.experimentWrites)
	}
}

// TestBuilderBetaExperimentScenarioRoundTrip generates a draft from an
// experiment whose scenario is stored, then publishes it back. The experiment
// embeds its own merged copy of the scenario, which never matches the stored
// config, so generation must reference the stored config instead. An uploaded
// experiment keeps its own copy.
func TestBuilderBetaExperimentScenarioRoundTrip(t *testing.T) { //nolint:paralleltest // mutates feature options
	stored := scenarioConfig(t, map[string]any{
		"apps": []any{map[string]any{"name": "app", "metadata": map[string]any{"k": "v"}}},
	})

	experiment := builderBetaConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{}},
		"vlans":    map[string]any{"aliases": map[string]any{}},
		"scenario": map[string]any{"apps": []any{map[string]any{
			"name": "app", "disabled": false, "metadata": map[string]any{"k": "v"},
		}}},
	}
	experiment.Metadata.Annotations = store.Annotations{"topology": "topo", "scenario": "sc"}

	t.Run("stored scenario", func(t *testing.T) {
		harness := newBuilderBetaHarness(t, experiment, *stored)
		document, _ := generateBuilderBetaDocument(t, harness, nil, `{"source":"Experiment/exp"}`)

		digest, err := bdoc.ContentDigest(stored.Spec)
		if err != nil {
			t.Fatalf("ContentDigest returned error: %v", err)
		}

		want := bdoc.ScenarioRef{
			Kind: bdoc.ScenarioRefStored, Name: "sc", Content: nil,
			APIVersion: stored.Version, Digest: digest,
		}
		if document.Scenario == nil || !reflect.DeepEqual(*document.Scenario, want) {
			t.Fatalf("scenario = %+v, want %+v", document.Scenario, want)
		}

		draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")
		recorder := harness.do(builderBetaRequest{
			method: http.MethodPost,
			path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
			body: `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
				`"scenario":{"name":"sc","action":"use"},` +
				`"experiment":{"name":"exp","action":"update"}}`,
			user: builderBetaTestOwner, ifMatch: draft.ETag,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("publish status = %d: %s", recorder.Code, recorder.Body.String())
		}

		updated, err := harness.getConfig("Experiment/exp")
		if err != nil {
			t.Fatalf("experiment missing: %v", err)
		}
		if updated.Metadata.Annotations["scenario"] != "sc" {
			t.Fatalf("experiment annotations = %#v", updated.Metadata.Annotations)
		}
	})

	// Without the stored config, or without permission to list it, the
	// experiment's copy is all there is: it is attached as an uploaded scenario
	// under the same name, and a warning says so.
	hidden := builderBetaRole(
		builderBetaPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderBetaPolicy([]string{"experiments"}, []string{"*"}, []string{"list"}),
	)

	for name, setup := range map[string]struct {
		configs []store.Config
		role    *rbac.Role
	}{
		"missing scenario": {configs: []store.Config{experiment}, role: nil},
		"hidden scenario":  {configs: []store.Config{experiment, *stored}, role: &hidden},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newBuilderBetaHarness(t, setup.configs...)
			document, warnings := generateBuilderBetaDocument(t, harness, setup.role, `{"source":"Experiment/exp"}`)

			ref := document.Scenario
			if ref == nil || ref.Kind != bdoc.ScenarioRefUploaded || ref.Name != "sc" || len(ref.Content) == 0 {
				t.Fatalf("scenario = %+v, want the experiment's copy as uploaded scenario sc", ref)
			}

			if !slices.ContainsFunc(warnings, func(warning string) bool {
				return strings.Contains(warning, `scenario "sc" is not available`)
			}) {
				t.Fatalf("warnings = %v, want one about scenario sc", warnings)
			}

			if !reflect.DeepEqual(document.Source.Warnings, warnings) {
				t.Fatalf("source warnings = %v, want %v", document.Source.Warnings, warnings)
			}
		})
	}

	// An uploaded experiment was not built from this server's scenario of the
	// same name, which here differs, so its own copy is kept even though the
	// stored scenario is listable, and publishing writes that copy.
	t.Run("uploaded experiment", func(t *testing.T) {
		checkUploadedExperimentScenario(t, experiment, stored)
	})
}

// checkUploadedExperimentScenario generates a draft from an upload of
// experiment carrying a scenario that differs from stored, then publishes it.
func checkUploadedExperimentScenario(t *testing.T, experiment store.Config, stored *store.Config) {
	t.Helper()

	uploadedScenario := map[string]any{"apps": []any{map[string]any{"name": "uploaded-app"}}}

	upload := experiment
	upload.Spec = map[string]any{
		"topology": experiment.Spec["topology"],
		"vlans":    experiment.Spec["vlans"],
		"scenario": uploadedScenario,
	}

	content, err := json.Marshal(upload)
	if err != nil {
		t.Fatalf("encoding uploaded experiment: %v", err)
	}

	body, err := json.Marshal(map[string]string{"content": string(content)})
	if err != nil {
		t.Fatalf("encoding generate request: %v", err)
	}

	harness := newBuilderBetaHarness(t, *stored)
	document, warnings := generateBuilderBetaDocument(t, harness, nil, string(body))

	uploadedDigest, err := bdoc.ContentDigest(uploadedScenario)
	if err != nil {
		t.Fatalf("ContentDigest returned error: %v", err)
	}

	ref := document.Scenario
	if ref == nil || ref.Kind != bdoc.ScenarioRefUploaded || ref.Name != "sc" || ref.Digest != uploadedDigest {
		t.Fatalf("scenario = %+v, want the uploaded experiment's copy as uploaded scenario sc", ref)
	}

	if !slices.ContainsFunc(warnings, func(warning string) bool {
		return strings.Contains(warning, `uploaded experiment's copy of scenario "sc"`)
	}) {
		t.Fatalf("warnings = %v, want one about scenario sc", warnings)
	}

	if !reflect.DeepEqual(document.Source.Warnings, warnings) {
		t.Fatalf("source warnings = %v, want %v", document.Source.Warnings, warnings)
	}

	storedDigest, err := bdoc.ContentDigest(stored.Spec)
	if err != nil {
		t.Fatalf("ContentDigest returned error: %v", err)
	}

	draft := createBuilderPublishDraft(t, harness, document, "uploaded/exp")
	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body: `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
			`"scenario":{"name":"sc","action":"update","expectedDigest":"` + storedDigest + `"},` +
			`"experiment":{"name":"exp","action":"create"}}`,
		user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("publish status = %d: %s", recorder.Code, recorder.Body.String())
	}

	published, err := harness.getConfig("Scenario/sc")
	if err != nil {
		t.Fatalf("scenario missing: %v", err)
	}

	if digest, err := bdoc.ContentDigest(published.Spec); err != nil || digest != uploadedDigest {
		t.Fatalf("published scenario = %#v, want the uploaded experiment's copy", published.Spec)
	}
}

// generateBuilderBetaDocument posts body to /builder/generate and returns the
// generated document and its warnings.
func generateBuilderBetaDocument(
	t *testing.T,
	harness *builderBetaHarness,
	role *rbac.Role,
	body string,
) (*bdoc.Document, []string) {
	t.Helper()

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost, path: "/builder/generate", body: body,
		user: builderBetaTestOwner, role: role,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("generate status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var response builderGenerateResponse
	harness.decode(recorder, &response)

	var document bdoc.Document
	if err := json.Unmarshal(response.Document, &document); err != nil {
		t.Fatalf("decoding generated document: %v", err)
	}

	return &document, response.Warnings
}

func TestBuilderBetaPublishResumesAfterScenarioFailure(t *testing.T) { //nolint:paralleltest // mutates feature options
	scenario := scenarioConfig(t, map[string]any{"apps": []any{}})
	digest, err := bdoc.ContentDigest(scenario.Spec)
	if err != nil {
		t.Fatalf("ContentDigest returned error: %v", err)
	}

	document := bdoc.NewDocument("resume")
	document.Scenario = &bdoc.ScenarioRef{
		Kind: bdoc.ScenarioRefStored, Name: "sc", Content: nil,
		APIVersion: scenario.Version, Digest: digest,
	}

	harness := newBuilderBetaHarness(t, *scenario)
	draft := createBuilderPublishDraft(t, harness, document)
	body := `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
		`"scenario":{"name":"sc","action":"use"},"experiment":{"name":"exp","action":"create"}}`

	harness.failConfigKind = builderBetaKindScenario
	first := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want %d: %s", first.Code, http.StatusInternalServerError, first.Body.String())
	}

	harness.failConfigKind = ""
	retry := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d: %s", retry.Code, retry.Body.String())
	}

	topology, err := harness.getConfig("Topology/topo")
	if err != nil {
		t.Fatalf("published topology missing: %v", err)
	}

	ref, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("DecodeReference returned error: %v", err)
	}
	if _, _, err := harness.service.GetPublishedDocumentData(context.Background(), ref.ID); err != nil {
		t.Fatalf("referenced published document is unreadable: %v", err)
	}
	if count := harness.store.count(bapi.NamespacePublished); count != 1 {
		t.Fatalf("published document count = %d, want 1", count)
	}
	if harness.configWrites != 2 || harness.experimentWrites != 1 {
		t.Fatalf("writes after retry = configs %d, experiments %d", harness.configWrites, harness.experimentWrites)
	}
}

func TestBuilderBetaPublishRetryIsIdempotent(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t)
	draft := harness.createDraft(builderBetaTestOwner, "retry")
	body := `{"mode":"topology","topology":{"name":"retry","action":"create"}}`

	first := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d: %s", first.Code, first.Body.String())
	}

	tooStale := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderBetaTestOwner, ifMatch: `"0"`,
	})
	if tooStale.Code != http.StatusPreconditionFailed {
		t.Fatalf("unrelated stale ETag status = %d, want %d", tooStale.Code, http.StatusPreconditionFailed)
	}

	retry := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d: %s", retry.Code, retry.Body.String())
	}

	var response builderPublishResponse
	harness.decode(retry, &response)
	if response.Status != bapi.PublishSucceeded ||
		!strings.Contains(strings.Join(response.Warnings, " "), "already complete") {
		t.Fatalf("retry response = %#v", response)
	}
	if harness.configWrites != 1 {
		t.Fatalf("config writes = %d, want 1", harness.configWrites)
	}
}

// TestBuilderBetaPublishExperimentWithIncludedTopology round trips an
// experiment whose topology includes another: phenix merged the included
// node into the experiment, generation marks it, and publishing writes the
// topology with its include rather than the node, while the experiment keeps
// the node exactly once.
func TestBuilderBetaPublishExperimentWithIncludedTopology(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t, includedTopologyFixture(t, "shared")...)
	document := generateBuilderDocument(t, harness, "Experiment/exp")

	if included := document.FindDevice("inc-host"); included == nil || included.Device.IncludedFrom != "shared" {
		t.Fatalf("inc-host is not marked as included: %s", asBuilderJSON(t, document))
	}

	draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")

	published := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body: `{"mode":"topology-experiment","topology":{"name":"root","action":"update"},` +
			`"experiment":{"name":"exp","action":"update"}}`,
		user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if published.Code != http.StatusOK {
		t.Fatalf("publish status = %d: %s", published.Code, published.Body.String())
	}

	hostnames := func(topology any) []string {
		spec, _ := topology.(map[string]any)
		nodes, _ := spec["nodes"].([]any)
		names := make([]string, 0, len(nodes))

		for _, entry := range nodes {
			general, _ := entry.(map[string]any)["general"].(map[string]any)
			name, _ := general["hostname"].(string)
			names = append(names, name)
		}

		return names
	}

	topology, err := harness.getConfig("Topology/root")
	if err != nil {
		t.Fatalf("published topology missing: %v", err)
	}

	if got := hostnames(topology.Spec); !slices.Equal(got, []string{"web"}) ||
		!reflect.DeepEqual(topology.Spec["includeTopologies"], []string{"shared"}) {
		t.Fatalf("published topology = %v, want web and the shared include", topology.Spec)
	}

	updated, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("updated experiment missing: %v", err)
	}

	if got := hostnames(updated.Spec["topology"]); !slices.Equal(got, []string{"web", "inc-host"}) {
		t.Fatalf("experiment nodes = %v, want web and the merged inc-host once", got)
	}
}

// includeNode is a complete node spec named hostname, for include tests.
func includeNode(hostname string) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": hostname, "vm_type": "kvm"},
		"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "miniccc.qc2"}}},
		"network": map[string]any{"interfaces": []any{map[string]any{
			"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "static",
			"address": "10.0.0.1", "mask": 24,
		}}},
	}
}

// includedTopologyFixture returns topology "shared" with node inc-host,
// topology "root" with node web including the given topologies, and
// experiment "exp" created from root, holding both nodes as phenix merged
// them.
func includedTopologyFixture(t *testing.T, includes ...any) []store.Config {
	t.Helper()

	shared := builderBetaConfig(t, builderBetaKindTopology, "shared")
	shared.Spec = map[string]any{"nodes": []any{includeNode("inc-host")}}

	root := builderBetaConfig(t, builderBetaKindTopology, "root")
	root.Spec = map[string]any{"nodes": []any{includeNode("web")}, "includeTopologies": includes}

	experiment := builderBetaConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{
			"nodes":             []any{includeNode("web"), includeNode("inc-host")},
			"includeTopologies": includes,
		},
		"vlans": map[string]any{"aliases": map[string]any{}},
	}
	experiment.Metadata.Annotations = store.Annotations{"topology": "root"}

	return []store.Config{shared, root, experiment}
}

// generateBuilderDocument generates a document from a stored config.
func generateBuilderDocument(t *testing.T, harness *builderBetaHarness, source string) *bdoc.Document {
	t.Helper()

	generated := harness.do(builderBetaRequest{
		method: http.MethodPost, path: "/builder/generate", body: `{"source":"` + source + `"}`,
		user: builderBetaTestOwner,
	})
	if generated.Code != http.StatusOK {
		t.Fatalf("generate status = %d: %s", generated.Code, generated.Body.String())
	}

	var response builderGenerateResponse
	harness.decode(generated, &response)

	document, err := bdoc.Parse(response.Document)
	if err != nil {
		t.Fatalf("generated document: %v", err)
	}

	return document
}

func asBuilderJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}

	return string(data)
}

// TestBuilderBetaPublishRejectsIncludedHostnameClash refuses, before writing
// anything, to publish a topology whose included topology gained a node
// named like one of its own after the draft was imported: phenix would
// refuse to create an experiment from it.
func TestBuilderBetaPublishRejectsIncludedHostnameClash(t *testing.T) { //nolint:paralleltest // mutates feature options
	for _, test := range []struct{ source, body string }{
		{source: "Topology/root", body: `{"mode":"topology","topology":{"name":"root","action":"update"}}`},
		// Refused on the topology, before the experiment update fails to
		// merge the includes for the same reason.
		{
			source: "Experiment/exp",
			body: `{"mode":"topology-experiment","topology":{"name":"root","action":"update"},` +
				`"experiment":{"name":"exp","action":"update"}}`,
		},
	} {
		t.Run(test.source, func(t *testing.T) {
			harness := newBuilderBetaHarness(t, includedTopologyFixture(t, "shared")...)
			document := generateBuilderDocument(t, harness, test.source)
			draft := createBuilderPublishDraft(t, harness, document, test.source)

			for i := range harness.configs {
				if harness.configs[i].FullName() == "Topology/shared" {
					harness.configs[i].Spec = map[string]any{"nodes": []any{includeNode("inc-host"), includeNode("WEB")}}
				}
			}

			published := harness.do(builderBetaRequest{
				method: http.MethodPost,
				path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
				body:   test.body,
				user:   builderBetaTestOwner, ifMatch: draft.ETag,
			})

			want := "topology root cannot be published: node WEB is defined both here and in its included topology shared"
			if published.Code != http.StatusConflict || !strings.Contains(published.Body.String(), want) {
				t.Fatalf("publish = %d %s, want 409 with %q", published.Code, published.Body.String(), want)
			}

			if harness.configWrites != 0 || harness.experimentWrites != 0 {
				t.Fatalf("writes = %d configs and %d experiments, want none", harness.configWrites, harness.experimentWrites)
			}
		})
	}
}

// TestBuilderBetaPublishExperimentUpdateChecksIncludes merges the includes
// into an updated experiment only the way import reads them: a topology the
// caller may not read, or a file path, stops the update before any write.
func TestBuilderBetaPublishExperimentUpdateChecksIncludes(t *testing.T) { //nolint:paralleltest // mutates feature options
	everything := []string{"list", "get", "create", "update", "delete"}
	noShared := builderBetaRole(
		builderBetaPolicy([]string{"configs", "schemas", "experiments", "scenarios"}, []string{"*", "*/*"}, everything),
		builderBetaPolicy([]string{"topologies"}, []string{"root"}, everything),
	)

	for _, test := range []struct {
		name     string
		includes []any
		role     *rbac.Role
		status   int
		want     string
	}{
		{
			name: "forbidden", includes: []any{"shared"}, role: &noShared, status: http.StatusForbidden,
			want: "merging included topology shared into experiment exp not allowed",
		},
		{
			name: "file", includes: []any{"shared", "/srv/extra.yml"}, role: nil, status: http.StatusUnprocessableEntity,
			want: "experiment exp cannot be updated: included topology /srv/extra.yml cannot be merged: " +
				"the Builder reads included topologies from the config store only",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderBetaHarness(t, includedTopologyFixture(t, test.includes...)...)
			document := generateBuilderDocument(t, harness, "Experiment/exp")
			draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")

			published := harness.do(builderBetaRequest{
				method: http.MethodPost,
				path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
				body: `{"mode":"topology-experiment","topology":{"name":"root","action":"update"},` +
					`"experiment":{"name":"exp","action":"update"}}`,
				user: builderBetaTestOwner, ifMatch: draft.ETag, role: test.role,
			})

			if published.Code != test.status || !strings.Contains(published.Body.String(), test.want) {
				t.Fatalf("publish = %d %s, want %d with %q", published.Code, published.Body.String(), test.status, test.want)
			}

			if harness.configWrites != 0 || harness.experimentWrites != 0 {
				t.Fatalf("writes = %d configs and %d experiments, want none", harness.configWrites, harness.experimentWrites)
			}
		})
	}
}

// publishBuilderDraft posts a publish intent for the draft, which must be
// answered with the given status, and returns the publish response, or for a
// refusal, which has none, the reason the server gave.
func publishBuilderDraft(
	t *testing.T,
	harness *builderBetaHarness,
	draft builderDraftResponse,
	body string,
	status int,
) (builderPublishResponse, string) {
	t.Helper()

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != status {
		t.Fatalf("publish %s: status = %d, want %d: %s", body, recorder.Code, status, recorder.Body.String())
	}

	var (
		response builderPublishResponse
		refusal  struct {
			Message string `json:"message"`
		}
	)

	harness.decode(recorder, &response)
	harness.decode(recorder, &refusal)

	return response, refusal.Message
}

// editBuilderDraft adds a device to the document and saves it as the draft's
// next snapshot, as an edit in the editor does, and returns the draft after
// it.
func editBuilderDraft(
	t *testing.T,
	harness *builderBetaHarness,
	draft builderDraftResponse,
	document *bdoc.Document,
	hostname string,
) builderDraftResponse {
	t.Helper()

	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID(hostname), Kind: bdoc.NodeKindDevice, Label: hostname,
		Device: &bdoc.Device{Hostname: hostname, Spec: includeNode(hostname), Interfaces: []bdoc.InterfaceHandle{}},
	})

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/snapshots",
		body:   `{"summary":"added ` + hostname + `","document":` + string(data) + `}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("saving the edit: status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var edited builderDraftResponse
	harness.decode(recorder, &edited)

	return edited
}

// openedBuilderDocument is a published document as the editor opens it.
type openedBuilderDocument struct {
	id       string
	document *bdoc.Document
}

// openPublishedBuilderDocument reads the document a stored topology
// references through GET /builder/documents/{id}, as opening a topology's
// published diagram does.
func openPublishedBuilderDocument(t *testing.T, harness *builderBetaHarness, topology string) openedBuilderDocument {
	t.Helper()

	config, err := harness.getConfig(builderBetaKindTopology + "/" + topology)
	if err != nil {
		t.Fatalf("topology %s missing: %v", topology, err)
	}

	ref, err := bapi.DecodeReference(config.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("DecodeReference returned error: %v", err)
	}

	recorder := harness.do(builderBetaRequest{
		method: http.MethodGet, path: "/builder/documents/" + ref.ID, user: builderBetaTestOwner,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("opening document %s: status = %d: %s", ref.ID, recorder.Code, recorder.Body.String())
	}

	var response builderDocumentResponse
	harness.decode(recorder, &response)

	document, err := bdoc.Decode(response.Document)
	if err != nil {
		t.Fatalf("decoding document %s: %v", ref.ID, err)
	}

	return openedBuilderDocument{id: ref.ID, document: document}
}

// topologyHostnames returns the hostnames of a stored topology's nodes.
func topologyHostnames(t *testing.T, harness *builderBetaHarness, name string) []string {
	t.Helper()

	topology, err := harness.getConfig(builderBetaKindTopology + "/" + name)
	if err != nil {
		t.Fatalf("topology %s missing: %v", name, err)
	}

	nodes, _ := topology.Spec["nodes"].([]any)
	names := make([]string, 0, len(nodes))

	for _, entry := range nodes {
		node, _ := entry.(map[string]any)
		general, _ := node["general"].(map[string]any)
		hostname, _ := general["hostname"].(string)
		names = append(names, hostname)
	}

	slices.Sort(names)

	return names
}

// TestBuilderBetaPublishAgainAfterEdits publishes a draft, edits it and
// publishes it again, twice over, as the editor does. A new diagram, one
// imported from the topology and one opened from its published diagram each
// update the topology again: it holds what the draft last published. A change
// anyone else made to the topology since is refused, not overwritten, and a
// draft with no part in the topology still may not update it.
func TestBuilderBetaPublishAgainAfterEdits(t *testing.T) { //nolint:paralleltest // mutates feature options
	const update = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`

	for _, test := range []struct {
		name  string
		start func(*testing.T, *builderBetaHarness) (builderDraftResponse, *bdoc.Document)
	}{
		{
			name: "new diagram",
			start: func(t *testing.T, harness *builderBetaHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "a")
				published, _ := publishBuilderDraft(t, harness, draft,
					`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)

				return published.Draft, document
			},
		},
		{
			name: "imported topology",
			start: func(t *testing.T, harness *builderBetaHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				lab := builderBetaConfig(t, builderBetaKindTopology, "lab")
				lab.Spec = map[string]any{"nodes": []any{includeNode("a")}}
				harness.configs = append(harness.configs, lab)

				document := generateBuilderDocument(t, harness, "Topology/lab")

				return createBuilderPublishDraft(t, harness, document, "Topology/lab"), document
			},
		},
		{
			name: "published diagram",
			start: func(t *testing.T, harness *builderBetaHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				first := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "a")
				publishBuilderDraft(t, harness, first,
					`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)

				topology, err := harness.getConfig("Topology/lab")
				if err != nil {
					t.Fatalf("published topology missing: %v", err)
				}

				ref, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
				if err != nil {
					t.Fatalf("DecodeReference returned error: %v", err)
				}

				return createBuilderPublishDraft(t, harness, document, "builder-doc/"+ref.ID), document
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderBetaHarness(t)
			draft, document := test.start(t, harness)

			for _, hostname := range []string{"b", "c"} {
				draft = editBuilderDraft(t, harness, draft, document, hostname)
				published, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)

				if published.Stages[1].Status != "updated" {
					t.Fatalf("stages = %#v, want the topology updated", published.Stages)
				}

				draft = published.Draft
			}

			if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"a", "b", "c"}) {
				t.Fatalf("topology nodes = %v, want a, b and c", got)
			}

			// Someone else changes the topology, keeping its annotation.
			for i := range harness.configs {
				if harness.configs[i].FullName() == "Topology/lab" {
					harness.configs[i].Spec = maps.Clone(harness.configs[i].Spec)
					harness.configs[i].Spec["nodes"] = append(slices.Clone(harness.configs[i].Spec["nodes"].([]any)),
						includeNode("theirs"))
				}
			}

			draft = editBuilderDraft(t, harness, draft, document, "d")
			writes := harness.configWrites

			if _, reason := publishBuilderDraft(t, harness, draft, update, http.StatusConflict); reason !=
				"topology lab changed after this draft published it" {
				t.Fatalf("refusal = %q, want the topology changed", reason)
			}

			other := harness.createDraft(builderBetaTestOwner, "other")
			if _, reason := publishBuilderDraft(t, harness, other, update, http.StatusConflict); reason !=
				"topology lab is not the source this draft was loaded from" {
				t.Fatalf("refusal = %q, want another draft refused", reason)
			}

			// The topology still references its published diagram, which does
			// not have their node. A draft opened from that diagram only now,
			// after their change, may not overwrite it either.
			opened := openPublishedBuilderDocument(t, harness, "lab")
			reopened := editBuilderDraft(t, harness,
				createBuilderPublishDraft(t, harness, opened.document, "builder-doc/"+opened.id), opened.document, "e")

			if _, reason := publishBuilderDraft(t, harness, reopened, update, http.StatusConflict); reason !=
				"topology lab changed after this draft published it" {
				t.Fatalf("refusal = %q, want the topology changed", reason)
			}

			if harness.configWrites != writes {
				t.Fatalf("refused publications wrote %d configs", harness.configWrites-writes)
			}
		})
	}
}

// forkBuilderDraft creates a draft for the user that forks the draft named
// forkOf ("<owner>/<draft id>"), as saving the editor's history as a new
// draft does, and returns the answer.
func forkBuilderDraft(
	t *testing.T,
	harness *builderBetaHarness,
	user string,
	role *rbac.Role,
	forkOf string,
	document *bdoc.Document,
) *httptest.ResponseRecorder {
	t.Helper()

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	body, err := json.Marshal(map[string]any{"forkOf": forkOf, "document": json.RawMessage(data)})
	if err != nil {
		t.Fatalf("encoding fork request: %v", err)
	}

	return harness.do(builderBetaRequest{
		method: http.MethodPost, path: "/builder/drafts", body: string(body), user: user, role: role,
	})
}

// TestBuilderBetaPublishForkUpdatesWhatItsDraftPublished saves a draft's
// edited history as a new draft, as the editor does when the draft changed
// on the server, and publishes it. The fork updates the topology the draft
// published, and the experiment with it, for the draft's owner and for
// another user who may read the draft, but not what the draft publishes
// after the fork. Nobody who may not read the draft can fork it, and so
// claim what it published.
func TestBuilderBetaPublishForkUpdatesWhatItsDraftPublished(t *testing.T) { //nolint:paralleltest // mutates feature options
	const update = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`

	// Draft "original" publishes topology lab with node a, with the body
	// given or the topology alone, and the fork starts from what it
	// published.
	start := func(t *testing.T, body ...string) (*builderBetaHarness, builderDraftResponse, *bdoc.Document) {
		t.Helper()

		body = append(body, `{"mode":"topology","topology":{"name":"lab","action":"create"}}`)
		harness := newBuilderBetaHarness(t)
		document := bdoc.NewDocument("lab")
		original := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "a")
		published, _ := publishBuilderDraft(t, harness, original, body[0], http.StatusOK)

		return harness, published.Draft, document
	}

	// Forks the draft as the user. The fork keeps the draft's source token,
	// so opening the published diagram does not find it, and records what
	// the draft last published.
	fork := func(
		t *testing.T, harness *builderBetaHarness, user string, original builderDraftResponse, document *bdoc.Document,
	) builderDraftResponse {
		t.Helper()

		recorder := forkBuilderDraft(t, harness, user, nil, original.Owner+"/"+original.ID, document)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("fork: status = %d: %s", recorder.Code, recorder.Body.String())
		}

		var forked builderDraftResponse
		harness.decode(recorder, &forked)

		if forked.SourceToken != original.SourceToken {
			t.Fatalf("fork source token = %q, want the draft's %q", forked.SourceToken, original.SourceToken)
		}

		if want := original.Publication; forked.Forked == nil || *forked.Forked != (bapi.ForkedPublication{
			DocumentID: want.DocumentID, TopologyTarget: want.TopologyTarget, ExperimentTarget: want.ExperimentTarget,
		}) {
			t.Fatalf("fork's forked publication = %+v, want the draft's %+v", forked.Forked, want)
		}

		return forked
	}

	// Adds node b to the fork and publishes it with the body, as the fork's
	// owner, which must be answered with the status; returns the reason for
	// a refusal.
	publishFork := func(
		t *testing.T, harness *builderBetaHarness, forked builderDraftResponse, document *bdoc.Document,
		body string, status int,
	) string {
		t.Helper()

		document.Nodes = append(document.Nodes, bdoc.Node{
			ID: bdoc.DeviceNodeID("b"), Kind: bdoc.NodeKindDevice, Label: "b",
			Device: &bdoc.Device{Hostname: "b", Spec: includeNode("b"), Interfaces: []bdoc.InterfaceHandle{}},
		})

		data, err := bapi.EncodeDocument(document)
		if err != nil {
			t.Fatalf("EncodeDocument returned error: %v", err)
		}

		path := "/builder/drafts/" + forked.Owner + "/" + forked.ID
		recorder := harness.do(builderBetaRequest{
			method: http.MethodPost, path: path + "/snapshots", body: `{"summary":"added b","document":` + string(data) + `}`,
			user: forked.Owner, ifMatch: forked.ETag,
		})
		if recorder.Code != http.StatusCreated {
			t.Fatalf("saving the fork's edit: status = %d: %s", recorder.Code, recorder.Body.String())
		}

		harness.decode(recorder, &forked)

		recorder = harness.do(builderBetaRequest{
			method: http.MethodPost, path: path + "/publish", body: body, user: forked.Owner, ifMatch: forked.ETag,
		})
		if recorder.Code != status {
			t.Fatalf("publishing the fork: status = %d, want %d: %s", recorder.Code, status, recorder.Body.String())
		}

		var refusal struct {
			Message string `json:"message"`
		}
		harness.decode(recorder, &refusal)

		return refusal.Message
	}

	for _, user := range []string{builderBetaTestOwner, builderBetaTestPeer} {
		t.Run("forked by "+user, func(t *testing.T) {
			harness, original, document := start(t)

			publishFork(t, harness, fork(t, harness, user, original, document), document, update, http.StatusOK)

			if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"a", "b"}) {
				t.Fatalf("topology nodes = %v, want a and b", got)
			}
		})
	}

	t.Run("with the experiment the draft published", func(t *testing.T) {
		harness, original, document := start(t, `{"mode":"topology-experiment",`+
			`"topology":{"name":"lab","action":"create"},"experiment":{"name":"exp","action":"create"}}`)

		publishFork(t, harness, fork(t, harness, builderBetaTestOwner, original, document), document,
			`{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},`+
				`"experiment":{"name":"exp","action":"update"}}`, http.StatusOK)

		if !slices.Equal(harness.reconfigured, []string{"exp"}) {
			t.Fatalf("configured = %v, want exp updated", harness.reconfigured)
		}
	})

	t.Run("published again after the fork", func(t *testing.T) {
		harness, original, document := start(t)
		forked := *document
		forked.Nodes = slices.Clone(document.Nodes)
		draft := fork(t, harness, builderBetaTestOwner, original, &forked)

		// The original draft publishes again, so lab no longer holds what it
		// published when it was forked.
		edited := editBuilderDraft(t, harness, original, document, "c")
		publishBuilderDraft(t, harness, edited, update, http.StatusOK)

		writes := harness.configWrites
		if reason := publishFork(t, harness, draft, &forked, update, http.StatusConflict); reason !=
			"topology lab is not the source this draft was loaded from" {
			t.Fatalf("refusal = %q, want the fork refused", reason)
		}

		if harness.configWrites != writes {
			t.Fatalf("the refused fork wrote %d configs", harness.configWrites-writes)
		}
	})

	t.Run("refused to a user who may not read the draft", func(t *testing.T) {
		harness, original, document := start(t)
		owner := builderBetaOwnerRole()

		for _, forkOf := range []string{
			original.Owner + "/" + original.ID,
			original.Owner + "/id-missing",
			original.ID,
		} {
			recorder := forkBuilderDraft(t, harness, builderBetaTestPeer, &owner, forkOf, document)
			if recorder.Code != http.StatusNotFound {
				t.Errorf("fork of %q: status = %d, want %d: %s",
					forkOf, recorder.Code, http.StatusNotFound, recorder.Body.String())
			}
		}

		recorder := harness.do(builderBetaRequest{
			method: http.MethodGet, path: "/builder/drafts", user: builderBetaTestPeer, role: &owner,
		})

		var listing struct {
			Drafts []builderDraftResponse `json:"drafts"`
		}
		harness.decode(recorder, &listing)

		if len(listing.Drafts) != 0 {
			t.Fatalf("drafts = %+v, want none created by a refused fork", listing.Drafts)
		}
	})
}

// labExperimentFixture returns topology "lab" with node a, and experiment
// "exp" built from it.
func labExperimentFixture(t *testing.T) []store.Config {
	t.Helper()

	lab := builderBetaConfig(t, builderBetaKindTopology, "lab")
	lab.Spec = map[string]any{"nodes": []any{includeNode("a")}}

	experiment := builderBetaConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{includeNode("a")}},
		"vlans":    map[string]any{"aliases": map[string]any{}},
	}
	experiment.Metadata.Annotations = store.Annotations{"topology": "lab"}

	return []store.Config{lab, experiment}
}

// TestBuilderBetaPublishExperimentAgainAfterEdits publishes a topology and an
// experiment, edits and publishes both again, twice over: from a draft
// imported from the experiment, and from a new diagram that created them.
// Each update runs the apps' configure stage, as every other update of an
// experiment does, and a failed one leaves the experiment as it was, so
// publishing again retries it.
func TestBuilderBetaPublishExperimentAgainAfterEdits(t *testing.T) { //nolint:paralleltest // mutates feature options
	const update = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
		`"experiment":{"name":"exp","action":"update"}}`

	for _, test := range []struct {
		name  string
		start func(*testing.T, *builderBetaHarness) (builderDraftResponse, *bdoc.Document)
	}{
		{
			name: "imported experiment",
			start: func(t *testing.T, harness *builderBetaHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				harness.configs = append(harness.configs, labExperimentFixture(t)...)
				document := generateBuilderDocument(t, harness, "Experiment/exp")

				return createBuilderPublishDraft(t, harness, document, "Experiment/exp"), document
			},
		},
		{
			name: "new diagram",
			start: func(t *testing.T, harness *builderBetaHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "a")
				published, _ := publishBuilderDraft(t, harness, draft,
					`{"mode":"topology-experiment","topology":{"name":"lab","action":"create"},`+
						`"experiment":{"name":"exp","action":"create"}}`, http.StatusOK)

				if harness.experimentWrites != 1 || len(harness.reconfigured) != 0 {
					t.Fatalf("create: %d experiments created and %v reconfigured, want 1 created, which configures it",
						harness.experimentWrites, harness.reconfigured)
				}

				return published.Draft, document
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderBetaHarness(t)
			draft, document := test.start(t, harness)

			// The configure stage changes the experiment's spec, as apps do, so
			// publishing again compares the experiment with its digest after it.
			harness.configuring = func(name string) {
				setExperimentSpec(harness, name, "schedules", map[string]any{"a": fmt.Sprintf("host%d", len(harness.reconfigured))})
			}

			draft = editBuilderDraft(t, harness, draft, document, "b")
			before, err := harness.getConfig("Experiment/exp")
			if err != nil {
				t.Fatalf("experiment missing: %v", err)
			}

			harness.failReconfigure = true
			failed, _ := publishBuilderDraft(t, harness, draft, update, http.StatusInternalServerError)
			harness.failReconfigure = false

			if last := failed.Stages[len(failed.Stages)-1]; failed.Status != bapi.PublishPartial ||
				last.Name != builderPublishStageExperiment || last.Status != bapi.PublishFailed {
				t.Fatalf("failed configure stage = %#v, want a partial publication with the experiment failed", failed)
			}

			if after, _ := harness.getConfig("Experiment/exp"); !reflect.DeepEqual(after.Spec, before.Spec) {
				t.Fatalf("experiment after a failed configure stage = %v, want it as it was", after.Spec)
			}

			for _, hostname := range []string{"", "c"} {
				if hostname != "" {
					draft = editBuilderDraft(t, harness, draft, document, hostname)
				}

				published, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)
				if last := published.Stages[len(published.Stages)-2]; last.Name != builderPublishStageExperiment ||
					last.Status != "updated" {
					t.Fatalf("stages = %#v, want the experiment updated", published.Stages)
				}

				draft = published.Draft
			}

			if !slices.Equal(harness.reconfigured, []string{"exp", "exp"}) {
				t.Fatalf("configured = %v, want exp after each update", harness.reconfigured)
			}

			if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"a", "b", "c"}) {
				t.Fatalf("topology nodes = %v, want a, b and c", got)
			}
		})
	}
}

// TestBuilderBetaPublishRepairsDocumentDespiteCleanupFailure publishes again
// a document whose stored copy was damaged: the copy is repaired, and a
// failure to remove the damaged content is a warning, not a failed publish
// that skipped the topology.
func TestBuilderBetaPublishRepairsDocumentDespiteCleanupFailure(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t)
	document := bdoc.NewDocument("rep")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "a")
	published, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"rep","action":"create"}}`, http.StatusOK)

	// One chunk of the stored document goes missing.
	harness.store.mu.Lock()
	for key := range harness.store.records[bapi.NamespaceChunks] {
		if strings.HasPrefix(key, "published/") {
			delete(harness.store.records[bapi.NamespaceChunks], key)

			break
		}
	}
	harness.store.mu.Unlock()

	harness.store.failPrefixDelete = true
	defer func() { harness.store.failPrefixDelete = false }()

	recorder := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + published.Draft.Owner + "/" + published.Draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"rep","action":"update"}}`,
		user:   builderBetaTestOwner, ifMatch: published.Draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("publish again: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderPublishResponse
	harness.decode(recorder, &response)

	if builderBetaWarning(recorder) == "" || len(response.Warnings) != 1 {
		t.Fatalf("warning header %q and warnings %v, want the cleanup failure reported",
			builderBetaWarning(recorder), response.Warnings)
	}

	topology, err := harness.getConfig("Topology/rep")
	if err != nil {
		t.Fatalf("topology missing: %v", err)
	}

	ref, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("DecodeReference returned error: %v", err)
	}

	if _, err := harness.service.VerifyPublishedDocument(context.Background(), ref); err != nil {
		t.Fatalf("the topology's document after the repair: %v", err)
	}
}

// setExperimentSpec changes one field of a stored experiment's spec, as
// someone else, or an app, does.
func setExperimentSpec(harness *builderBetaHarness, name, key string, value any) {
	for i := range harness.configs {
		if harness.configs[i].FullName() == "Experiment/"+name {
			harness.configs[i].Spec = maps.Clone(harness.configs[i].Spec)
			harness.configs[i].Spec[key] = value
		}
	}
}

// TestBuilderBetaPublishExperimentRefusesOthersChanges refuses to update an
// experiment this draft did not publish, or that anyone else has changed
// since it did, however the experiment's topology is tied to the draft.
func TestBuilderBetaPublishExperimentRefusesOthersChanges(t *testing.T) { //nolint:paralleltest // mutates feature options
	const (
		topologyUpdate = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`
		bothUpdate     = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
			`"experiment":{"name":"%s","action":"update"}}`
	)

	for _, test := range []struct {
		name, experiment, reason string
		start                    func(*testing.T, *builderBetaHarness) (builderDraftResponse, *bdoc.Document)
	}{
		{
			// It published only the topology of the experiment it was imported
			// from, which someone has changed since the import.
			name: "imported experiment", experiment: "exp",
			reason: "builder source Experiment/exp changed after this draft was imported",
			start: func(t *testing.T, harness *builderBetaHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				harness.configs = append(harness.configs, labExperimentFixture(t)...)
				document := generateBuilderDocument(t, harness, "Experiment/exp")
				draft := editBuilderDraft(t, harness,
					createBuilderPublishDraft(t, harness, document, "Experiment/exp"), document, "b")
				published, _ := publishBuilderDraft(t, harness, draft, topologyUpdate, http.StatusOK)

				return published.Draft, document
			},
		},
		{
			// Someone else built an experiment from the topology it published.
			name: "experiment of its topology", experiment: "other",
			reason: "experiment other is not the source this draft was loaded from",
			start: func(t *testing.T, harness *builderBetaHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "a")
				published, _ := publishBuilderDraft(t, harness, draft,
					`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)

				other := builderBetaConfig(t, kindExperiment, "other")
				other.Spec = map[string]any{
					"topology": map[string]any{"nodes": []any{includeNode("a")}},
					"vlans":    map[string]any{"aliases": map[string]any{}},
				}
				other.Metadata.Annotations = store.Annotations{"topology": "lab"}
				harness.configs = append(harness.configs, other)

				return published.Draft, document
			},
		},
		{
			// It published the experiment, which someone has changed since.
			name: "published experiment", experiment: "exp",
			reason: "experiment exp changed after this draft published it",
			start: func(t *testing.T, harness *builderBetaHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "a")
				published, _ := publishBuilderDraft(t, harness, draft,
					`{"mode":"topology-experiment","topology":{"name":"lab","action":"create"},`+
						`"experiment":{"name":"exp","action":"create"}}`, http.StatusOK)

				return published.Draft, document
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderBetaHarness(t)
			draft, document := test.start(t, harness)

			setExperimentSpec(harness, test.experiment, "schedules", map[string]any{"a": "theirs"})
			draft = editBuilderDraft(t, harness, draft, document, "c")
			writes := harness.configWrites

			if _, reason := publishBuilderDraft(t, harness, draft,
				fmt.Sprintf(bothUpdate, test.experiment), http.StatusConflict); reason != test.reason {
				t.Fatalf("refusal = %q, want %q", reason, test.reason)
			}

			if harness.configWrites != writes || len(harness.reconfigured) != 0 {
				t.Fatalf("the refused publication wrote %d configs and configured %v",
					harness.configWrites-writes, harness.reconfigured)
			}

			if exp, _ := harness.getConfig("Experiment/" + test.experiment); !reflect.DeepEqual(
				exp.Spec["schedules"], map[string]any{"a": "theirs"}) {
				t.Fatalf("experiment schedules = %v, want theirs kept", exp.Spec["schedules"])
			}
		})
	}
}

// TestBuilderBetaPublishKeepsExperimentStartedWhileConfiguring leaves an
// experiment started while its configure stage ran, which the CLI can do
// without the web lock, as it is when that stage fails: restoring the
// experiment as it was before the update would write back its old status.
func TestBuilderBetaPublishKeepsExperimentStartedWhileConfiguring(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t, labExperimentFixture(t)...)
	document := generateBuilderDocument(t, harness, "Experiment/exp")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document, "Experiment/exp"), document, "b")

	started := map[string]any{"startTime": "2026-01-01T00:00:00Z"}
	harness.failReconfigure = true
	harness.configuring = func(name string) {
		for i := range harness.configs {
			if harness.configs[i].FullName() == "Experiment/"+name {
				harness.configs[i].Status = started
			}
		}
	}

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},`+
			`"experiment":{"name":"exp","action":"update"}}`, http.StatusInternalServerError)

	exp, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("experiment missing: %v", err)
	}

	if !reflect.DeepEqual(exp.Status, started) {
		t.Fatalf("experiment status = %v, want the started one kept", exp.Status)
	}
}

// TestBuilderBetaPublishExperimentUpdateRereadsUnderLock reads the
// experiment again once its lock is held: one started since preflight read it
// is refused, and one otherwise changed keeps what changed, its status
// included, rather than having it overwritten with the preflight copy.
func TestBuilderBetaPublishExperimentUpdateRereadsUnderLock(t *testing.T) { //nolint:paralleltest // mutates feature options
	const update = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
		`"experiment":{"name":"exp","action":"update"}}`

	harness := newBuilderBetaHarness(t, labExperimentFixture(t)...)
	document := generateBuilderDocument(t, harness, "Experiment/exp")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document, "Experiment/exp"), document, "b")

	// change sets the experiment's status and, unless deployMode is "", its
	// deploy mode.
	change := func(status map[string]any, deployMode string) func(string) {
		return func(name string) {
			for i := range harness.configs {
				if harness.configs[i].FullName() == "Experiment/"+name {
					harness.configs[i].Status = status

					if deployMode != "" {
						harness.configs[i].Spec = maps.Clone(harness.configs[i].Spec)
						harness.configs[i].Spec["deployMode"] = deployMode
					}
				}
			}
		}
	}

	harness.lockedExperiment = change(map[string]any{"startTime": "2026-01-01T00:00:00Z"}, "")
	running, _ := publishBuilderDraft(t, harness, draft, update, http.StatusConflict)

	if last := running.Stages[len(running.Stages)-1]; running.Status != bapi.PublishPartial ||
		last.Name != builderPublishStageExperiment || last.Status != bapi.PublishFailed ||
		last.Message != "experiment publication failed: a running experiment cannot be updated" {
		t.Fatalf("publish to a started experiment = %#v, want the experiment stage failed as running", running)
	}

	if len(harness.reconfigured) != 0 {
		t.Fatalf("a started experiment was configured: %v", harness.reconfigured)
	}

	// Stopped again, it is changed once more after preflight.
	change(nil, "")("exp")
	harness.lockedExperiment = change(map[string]any{"apps": map[string]any{"kept": true}}, "no-headnode")
	publishBuilderDraft(t, harness, draft, update, http.StatusOK)

	updated, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("experiment missing: %v", err)
	}

	if !reflect.DeepEqual(updated.Status, map[string]any{"apps": map[string]any{"kept": true}}) ||
		updated.Spec["deployMode"] != "no-headnode" {
		t.Fatalf("updated experiment status %v and deploy mode %v, want the ones set under the lock",
			updated.Status, updated.Spec["deployMode"])
	}

	topology, _ := updated.Spec["topology"].(map[string]any)
	if nodes, _ := topology["nodes"].([]any); len(nodes) != 2 {
		t.Fatalf("updated experiment topology = %v, want nodes a and b", topology)
	}
}

// TestBuilderBetaPublishRefusesExperimentNamesBeforeWriting refuses an
// experiment name experiment.Create would refuse, before the document or the
// topology is written: the reserved name "all", and in auto bridge mode, a
// name longer than a bridge name.
func TestBuilderBetaPublishRefusesExperimentNamesBeforeWriting(t *testing.T) { //nolint:paralleltest // mutates bridge mode
	mode := string(common.BridgeMode)
	t.Cleanup(func() { _ = common.SetBridgeMode(mode) })

	setBridgeMode := func(mode common.BridgingMode) {
		if err := common.SetBridgeMode(string(mode)); err != nil {
			t.Fatalf("SetBridgeMode returned error: %v", err)
		}
	}

	for _, test := range []struct {
		name, experiment string
		bridge           common.BridgingMode
		want             string
	}{
		{name: "reserved", experiment: "ALL", bridge: common.BridgeModeManual, want: "experiment ALL is reserved"},
		{
			name: "auto bridge", experiment: "sixteen-chars-xy", bridge: common.BridgeModeAuto,
			want: "experiment sixteen-chars-xy has a name longer than 15 characters",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			setBridgeMode(test.bridge)
			harness := newBuilderBetaHarness(t)
			draft := harness.createDraft(builderBetaTestOwner, "names")

			_, reason := publishBuilderDraft(t, harness, draft,
				`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
					`"experiment":{"name":"`+test.experiment+`","action":"create"}}`, http.StatusUnprocessableEntity)
			if !strings.HasPrefix(reason, test.want) {
				t.Fatalf("refusal = %q, want %q", reason, test.want)
			}

			if harness.configWrites != 0 || harness.store.count(bapi.NamespacePublished) != 0 {
				t.Fatal("a refused experiment name had side effects")
			}
		})
	}

	// Fifteen characters are allowed.
	setBridgeMode(common.BridgeModeAuto)
	harness := newBuilderBetaHarness(t)
	publishBuilderDraft(t, harness, harness.createDraft(builderBetaTestOwner, "names"),
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
			`"experiment":{"name":"fifteen-chars-x","action":"create"}}`, http.StatusOK)
}
