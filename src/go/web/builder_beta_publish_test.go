package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
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
		response.Stages[1].Status != "failed" {
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
		response.Stages[2].Status != "failed" {
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
		{name: "update", existing: scenarioConfig(t, "sc", map[string]any{"apps": []any{}}),
			action: builderPublishActionUpdate, expectedCode: http.StatusOK},
		{name: "stale update", existing: scenarioConfig(t, "sc", map[string]any{"apps": []any{}}),
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
}

func scenarioConfig(t *testing.T, name string, spec map[string]any) *store.Config {
	t.Helper()

	scenario, err := store.NewConfig("Scenario/" + name)
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

func TestBuilderBetaPublishResumesAfterScenarioFailure(t *testing.T) { //nolint:paralleltest // mutates feature options
	scenario := scenarioConfig(t, "sc", map[string]any{"apps": []any{}})
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
	if response.Status != builderPublishSucceeded ||
		!strings.Contains(strings.Join(response.Warnings, " "), "already complete") {
		t.Fatalf("retry response = %#v", response)
	}
	if harness.configWrites != 1 {
		t.Fatalf("config writes = %d, want 1", harness.configWrites)
	}
}
