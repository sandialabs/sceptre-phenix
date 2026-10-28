package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	bdoc "phenix/types/builder"
	"phenix/util/common"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

func TestBuilderPublishUpdatePreservesAnnotations(t *testing.T) {
	topology := builderConfig(t, builderKindTopology, "existing")
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

	harness := newBuilderHarness(t, topology)
	draft := createBuilderPublishDraft(t, harness, document)

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"existing","action":"update"}}`, http.StatusOK)

	updated, err := harness.getConfig("Topology/existing")
	if err != nil {
		t.Fatalf("updated topology missing: %v", err)
	}
	if got := updated.Metadata.Annotations["keep"]; got != "value" {
		t.Errorf("updating existing: annotation keep = %q, want %q", got, "value")
	}

	if _, err := bapi.DecodeReference(updated.Metadata.Annotations[bapi.DocumentAnnotation]); err != nil {
		t.Errorf("updating existing: DecodeReference(document annotation) returned error: %v", err)
	}
}

func TestBuilderPublishRejectsLegacyAndUnauthorizedTargets(t *testing.T) {
	legacy := builderConfig(t, builderKindTopology, "legacy")
	legacy.Metadata.Annotations = store.Annotations{bdoc.LegacyXMLAnnotation: "<mxfile/>"}

	harness := newBuilderHarness(t, legacy)
	document := bdoc.NewDocument("legacy")
	draft := createBuilderPublishDraft(t, harness, document)

	// A draft that was not imported from the legacy topology does not
	// replace it, and its diagram stays.
	_, refusal := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"legacy","action":"update"}}`, http.StatusConflict)
	if want := "topology legacy is not the source of this draft"; refusal != want {
		t.Errorf("publishing over legacy: refusal = %q, want %q", refusal, want)
	}

	if diff := cmp.Diff(store.Annotations{bdoc.LegacyXMLAnnotation: "<mxfile/>"},
		harness.configs[0].Metadata.Annotations); diff != "" {
		t.Errorf("publishing over legacy: annotations mismatch (-want +got):\n%s", diff)
	}

	role := builderRole(builderPolicy(
		[]string{"configs"},
		[]string{"*"},
		[]string{"update"},
	))
	publishBuilderDraftAs(t, harness, draft, role,
		`{"mode":"topology","topology":{"name":"new","action":"create"}}`, http.StatusForbidden)
	if harness.configWrites != 0 {
		t.Errorf("publishing new without create permission wrote %d configs, want none", harness.configWrites)
	}
}

// TestBuilderPublishReportsBroadcastWarning publishes bob's draft as alice,
// records alice as the publisher, and warns of a failed broadcast.
func TestBuilderPublishReportsBroadcastWarning(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.failBroadcastKind = builderKindTopology
	draft := harness.createDraft(builderBob, "broadcast")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"broadcast","action":"create"}}`, http.StatusOK)

	if messages := strings.Join(bdoc.IssueMessages(response.Warnings), " "); !strings.Contains(messages, "broadcast") {
		t.Errorf("publishing broadcast: warnings = %q, want one about the broadcast", messages)
	}

	if !slices.ContainsFunc(response.Warnings, func(warning bdoc.Issue) bool {
		return warning.Code == bdoc.CodePublishBroadcastFailed && warning.Severity == bdoc.SeverityWarning
	}) {
		t.Errorf("publishing broadcast: warnings = %+v, want a %s warning", response.Warnings, bdoc.CodePublishBroadcastFailed)
	}

	if response.Draft.Publication == nil {
		t.Error("publishing broadcast: draft publication = nil, want it recorded despite the failed broadcast")
	}

	meta := mustDraftMeta(t, harness, draft.ID)
	if meta.LastModifiedBy != builderAlice {
		t.Errorf("publishing broadcast: draft lastModifiedBy = %q, want %q", meta.LastModifiedBy, builderAlice)
	}

	switch {
	case meta.Publication == nil:
		t.Errorf("publishing broadcast: stored publication = nil, want alice as the publisher")
	case meta.Publication.PublishedBy != builderAlice:
		t.Errorf("publishing broadcast: publishedBy = %q, want %q", meta.Publication.PublishedBy, builderAlice)
	}
}

func TestBuilderPublishReportsPartialFailure(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "partial")
	harness.failConfigKind = builderKindTopology

	var response builderPublishResponse

	recorder := harness.doDecode(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"partial","action":"create"}}`,
		user:   builderAlice, ifMatch: draft.ETag,
	}, http.StatusInternalServerError, &response)
	if response.Status != bapi.PublishPartial {
		t.Errorf("publishing partial: status = %q, want %q", response.Status, bapi.PublishPartial)
	}

	if strings.Contains(recorder.Body.String(), "injected config write failure") {
		t.Errorf("publishing partial = %s, want no internal error in it", recorder.Body)
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if meta.Publication != nil {
		t.Errorf("publishing partial: draft publication = %+v, want none", meta.Publication)
	}

	if len(response.Stages) != 2 || len(response.Errors) != 1 {
		t.Fatalf("publishing partial: stages = %+v and errors = %+v, want 2 stages and 1 error", response.Stages, response.Errors)
	}

	wantStages := []builderPublishStage{
		{Name: builderPublishStageDocument, Status: "created"},
		{Name: builderPublishStageTopology, Status: bapi.PublishFailed},
	}
	if diff := cmp.Diff(wantStages, response.Stages, ignorePublishStageDetails()); diff != "" {
		t.Errorf("publishing partial: stages mismatch (-want +got):\n%s", diff)
	}

	// The error is the failed stage's message.
	wantErrors := []issueSummary{{Code: bdoc.CodePublishStageFailed, Message: response.Stages[1].Message}}
	if diff := cmp.Diff(wantErrors, issueSummaries(response.Errors)); diff != "" {
		t.Errorf("publishing partial: errors mismatch (-want +got):\n%s", diff)
	}
}

// ignorePublishStageDetails compares only the name and status of a stage.
func ignorePublishStageDetails() cmp.Options {
	return cmp.Options{cmpopts.IgnoreFields(builderPublishStage{}, "Message", "Config")}
}

func TestBuilderPublishReportsExperimentPartialFailure(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.failExperiment = true
	draft := harness.createDraft(builderAlice, "experiment-partial")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"partial-topology","action":"create"},`+
			`"experiment":{"name":"partial-experiment","action":"create"}}`, http.StatusInternalServerError)

	if response.Status != bapi.PublishPartial {
		t.Errorf("publishing partial-experiment: status = %q, want %q", response.Status, bapi.PublishPartial)
	}

	if _, err := harness.getConfig("Topology/partial-topology"); err != nil {
		t.Errorf("publishing partial-experiment: the published topology = %v, want it kept", err)
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if meta.Publication != nil {
		t.Errorf("publishing partial-experiment: draft publication = %+v, want none", meta.Publication)
	}

	wantStages := []builderPublishStage{
		{Name: builderPublishStageDocument, Status: "created"},
		{Name: builderPublishStageTopology, Status: "created"},
		{Name: builderPublishStageExperiment, Status: bapi.PublishFailed},
	}
	if diff := cmp.Diff(wantStages, response.Stages, ignorePublishStageDetails()); diff != "" {
		t.Errorf("publishing partial-experiment: stages mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderPublishFinishesAfterPartialFailure fails the experiment stage,
// then the stage that marks the draft published. Each partial answer says
// how to finish, and the same request with the same If-Match finishes the
// publication: the stages already done are skipped.
func TestBuilderPublishFinishesAfterPartialFailure(t *testing.T) {
	const body = `{"mode":"topology-experiment","topology":{"name":"resume-topology","action":"create"},` +
		`"experiment":{"name":"resume-experiment","action":"create"}}`

	injected := errors.New("injected draft write failure")
	advice := bdoc.NewIssue(bdoc.CodePublishRetryAvailable, "",
		"Publish again with the same choices to finish. The server skips the steps that are already done.")

	tests := []struct {
		name string
		// fail makes the stage fail, and returns what undoes it.
		fail       func(*builderHarness) func()
		failed     []builderPublishStage
		resumed    []builderPublishStage
		experiment int
	}{
		{
			name: "experiment stage",
			fail: func(h *builderHarness) func() {
				h.failExperiment = true

				return func() { h.failExperiment = false }
			},
			failed: []builderPublishStage{
				{Name: builderPublishStageDocument, Status: "created"},
				{Name: builderPublishStageTopology, Status: "created"},
				{Name: builderPublishStageExperiment, Status: bapi.PublishFailed},
			},
			resumed: []builderPublishStage{
				{Name: builderPublishStageDocument, Status: "created"},
				{Name: builderPublishStageTopology, Status: bapi.PublishSkipped},
				{Name: builderPublishStageExperiment, Status: "created"},
				{Name: builderPublishStageDraft, Status: "ok"},
			},
			experiment: 1,
		},
		{
			name: "draft stage",
			fail: func(h *builderHarness) func() {
				h.store.BeforeUpdate = func(namespace, _ string) error {
					if namespace == bapi.NamespaceDrafts {
						return injected
					}

					return nil
				}

				return func() { h.store.BeforeUpdate = nil }
			},
			failed: []builderPublishStage{
				{Name: builderPublishStageDocument, Status: "created"},
				{Name: builderPublishStageTopology, Status: "created"},
				{Name: builderPublishStageExperiment, Status: "created"},
				{Name: builderPublishStageDraft, Status: bapi.PublishFailed},
			},
			resumed: []builderPublishStage{
				{Name: builderPublishStageDocument, Status: "created"},
				{Name: builderPublishStageTopology, Status: bapi.PublishSkipped},
				{Name: builderPublishStageExperiment, Status: bapi.PublishSkipped},
				{Name: builderPublishStageDraft, Status: "ok"},
			},
			experiment: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderHarness(t)
			draft := harness.createDraft(builderAlice, "resume")
			undo := tt.fail(harness)

			failed, _ := publishBuilderDraft(t, harness, draft, body, http.StatusInternalServerError)

			if failed.Status != bapi.PublishPartial {
				t.Errorf("publish with the %s failing: status = %q, want %q", tt.name, failed.Status, bapi.PublishPartial)
			}

			if diff := cmp.Diff(tt.failed, failed.Stages, ignorePublishStageDetails()); diff != "" {
				t.Errorf("publish with the %s failing: stages mismatch (-want +got):\n%s", tt.name, diff)
			}

			if !slices.Contains(failed.Warnings, advice) {
				t.Errorf("publish with the %s failing: warnings = %+v, want %+v", tt.name, failed.Warnings, advice)
			}

			undo()

			// The same request, with the same If-Match: the draft did not change.
			resumed, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

			if resumed.Status != bapi.PublishSucceeded {
				t.Errorf("publish again after the %s failed: status = %q, want %q", tt.name, resumed.Status, bapi.PublishSucceeded)
			}

			if diff := cmp.Diff(tt.resumed, resumed.Stages, ignorePublishStageDetails()); diff != "" {
				t.Errorf("publish again after the %s failed: stages mismatch (-want +got):\n%s", tt.name, diff)
			}

			if slices.ContainsFunc(resumed.Warnings, func(issue bdoc.Issue) bool {
				return issue.Code == bdoc.CodePublishRetryAvailable
			}) {
				t.Errorf("publish again after the %s failed: warnings = %+v, want no advice to publish again", tt.name, resumed.Warnings)
			}

			// The topology and the experiment were each written once.
			if harness.configWrites != 1 || harness.experimentWrites != tt.experiment {
				t.Errorf("writes after publishing again = %d configs and %d experiments, want 1 and %d",
					harness.configWrites, harness.experimentWrites, tt.experiment)
			}

			meta, err := harness.service.GetDraft(context.Background(), draft.ID)
			if err != nil {
				t.Fatalf("GetDraft returned error: %v", err)
			}

			if meta.Publication == nil || meta.Publication.ExperimentTarget != "resume-experiment" {
				t.Errorf("draft publication after publishing again = %+v, want the experiment resume-experiment", meta.Publication)
			}
		})
	}
}

// TestBuilderPublishOffersNoRetryForChangedDraft asserts a draft that changed
// before it was marked published gets no advice to send the same request:
// its entity tag is stale.
func TestBuilderPublishOffersNoRetryForChangedDraft(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "changed")

	harness.store.BeforeUpdate = func(namespace, _ string) error {
		if namespace == bapi.NamespaceDrafts {
			return store.ErrRecordConflict
		}

		return nil
	}

	failed, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"changed","action":"create"}}`, http.StatusConflict)

	if failed.Status != bapi.PublishPartial {
		t.Errorf("publish of a draft that changed: status = %q, want %q", failed.Status, bapi.PublishPartial)
	}

	if slices.ContainsFunc(failed.Warnings, func(issue bdoc.Issue) bool {
		return issue.Code == bdoc.CodePublishRetryAvailable
	}) {
		t.Errorf("publish of a draft that changed: warnings = %+v, want no advice to publish again", failed.Warnings)
	}
}

func TestBuilderPublishValidatesIntentBeforeWriting(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "invalid-intent")

	tests := []struct{ name, body string }{
		{"empty", `{}`},
		{"unknown mode", `{"mode":"unknown","topology":{"name":"topo","action":"create"}}`},
		{"delete action", `{"mode":"topology","topology":{"name":"topo","action":"delete"}}`},
		{
			"experiment in topology mode",
			`{"mode":"topology","topology":{"name":"topo","action":"create"},"experiment":{"name":"exp","action":"create"}}`,
		},
		{"unknown key", `{"mode":"topology","topology":{"name":"topo","action":"create","unexpected":true}}`},
		// Names outside the config naming rule, which the experiment would
		// otherwise refuse only after the topology is written.
		{
			"experiment name with a space",
			`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
				`"experiment":{"name":"my exp","action":"create"}}`,
		},
		{"topology name with a slash", `{"mode":"topology","topology":{"name":"topo/x","action":"create"}}`},
		// A scenario is named only for the experiment of a
		// topology-and-experiment publication, by its name alone.
		{
			"scenario in topology mode",
			`{"mode":"topology","topology":{"name":"topo","action":"create"},"scenario":{"name":"sc"}}`,
		},
		{
			"scenario with an action",
			`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
				`"scenario":{"name":"sc","action":"use"},"experiment":{"name":"exp","action":"create"}}`,
		},
		{
			"scenario name with a space",
			`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
				`"scenario":{"name":"my sc"},"experiment":{"name":"exp","action":"create"}}`,
		},
		{
			"empty scenario name",
			`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
				`"scenario":{"name":""},"experiment":{"name":"exp","action":"create"}}`,
		},
		{
			"expected digest on update",
			`{"mode":"topology","topology":{"name":"topo","action":"update","expectedDigest":"sha256:` +
				strings.Repeat("0", 64) + `"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publishBuilderDraft(t, harness, draft, test.body, http.StatusBadRequest)
		})
	}

	if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
		t.Errorf("invalid publish requests wrote %d configs and %d published documents, want none", writes, documents)
	}
}

// TestBuilderPublishNamesInterfacesWithoutVLAN names the first interfaces without a
// VLAN in the message the Publish dialog shows, and how many more there are.
func TestBuilderPublishNamesInterfacesWithoutVLAN(t *testing.T) {
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

	harness := newBuilderHarness(t)
	draft := createBuilderPublishDraft(t, harness, document)

	_, body := publishBuilderDraftAs(t, harness, draft, nil,
		`{"mode":"topology","topology":{"name":"no-vlan","action":"create"}}`, http.StatusUnprocessableEntity)

	const fix = " has no VLAN: connect it to a network, or type a VLAN for it"

	want := `phenix cannot publish topology no-vlan: ` +
		`Interface "eth0" (#1) of device "host"` + fix + `. ` +
		`Interface #2 of device "host"` + fix + `. ` +
		`Interface "eth0" (#3) of device "host"` + fix + `. ` +
		`2 more interfaces have no VLAN`
	if diff := cmp.Diff(want, body.Message); diff != "" {
		t.Errorf("publishing no-vlan: message mismatch (-want +got):\n%s", diff)
	}

	// The cause still lists every one.
	if !strings.Contains(body.Cause, `interface "eth2" of device "host"`+fix) {
		t.Errorf("publishing no-vlan: cause = %q, want it to name eth2", body.Cause)
	}

	if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
		t.Errorf("publishing no-vlan wrote %d configs and %d published documents, want none", writes, documents)
	}
}

// TestBuilderPublishNamesSharedAddresses names the first IP or MAC addresses that
// interfaces share, however each is written.
func TestBuilderPublishNamesSharedAddresses(t *testing.T) {
	document := bdoc.NewDocument("shared")

	for _, host := range []struct{ name, separator, prefix string }{{"aa", ":", ""}, {"bb", "-", "/24"}} {
		mac := func(last string) string {
			return strings.Join([]string{"00", "00", "00", "00", "00", last}, host.separator)
		}

		document.Nodes = append(document.Nodes, bdoc.Node{
			ID: bdoc.DeviceNodeID(host.name), Kind: bdoc.NodeKindDevice, Label: host.name,
			Device: &bdoc.Device{
				Hostname: host.name,
				Spec: map[string]any{
					"type":    "VirtualMachine",
					"general": map[string]any{"hostname": host.name},
					"network": map[string]any{"interfaces": []any{
						map[string]any{
							"name": "eth0", "vlan": "EXP", "proto": "static",
							"address": "10.0.0.1" + host.prefix, "mac": mac("01"),
						},
						map[string]any{
							"name": "eth1", "vlan": "EXP", "proto": "static",
							"address": "10.0.0.2" + host.prefix, "mac": mac("02"),
						},
					}},
				},
				Interfaces: []bdoc.InterfaceHandle{},
			},
		})
	}

	harness := newBuilderHarness(t)
	draft := createBuilderPublishDraft(t, harness, document)

	_, body := publishBuilderDraftAs(t, harness, draft, nil,
		`{"mode":"topology","topology":{"name":"shared","action":"create"}}`, http.StatusUnprocessableEntity)

	users := func(name string) string {
		return `interface "` + name + `" of device "aa" and interface "` + name + `" of device "bb"`
	}

	want := `phenix cannot publish topology shared: ` +
		`IP address 10.0.0.1 on VLAN "EXP" is used by ` + users("eth0") + `. ` +
		`MAC address 00:00:00:00:00:01 on VLAN "EXP" is used by ` + users("eth0") + `. ` +
		`IP address 10.0.0.2 on VLAN "EXP" is used by ` + users("eth1") + `. ` +
		`1 more duplicate address`
	if diff := cmp.Diff(want, body.Message); diff != "" {
		t.Errorf("publishing shared: message mismatch (-want +got):\n%s", diff)
	}

	// The cause still lists every one.
	if !strings.Contains(body.Cause, `MAC address 00:00:00:00:00:02 on VLAN "EXP" is used by `+users("eth1")) {
		t.Errorf("publishing shared: cause = %q, want it to name the second MAC address", body.Cause)
	}

	if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
		t.Errorf("publishing shared wrote %d configs and %d published documents, want none", writes, documents)
	}
}

func TestBuilderPublishRejectsStaleSource(t *testing.T) {
	source := builderConfig(t, builderKindTopology, "source")

	digest, err := bdoc.SourceDigest(source)
	if err != nil {
		t.Fatalf("SourceDigest returned error: %v", err)
	}

	document := bdoc.NewDocument("generated")
	document.Source = &bdoc.Source{
		Kind: bdoc.SourceKindTopology, Name: "source", APIVersion: source.Version,
		Digest: digest, UpdatedAt: source.Metadata.Updated, ImportedAt: "", Topology: "", Warnings: nil,
	}

	harness := newBuilderHarness(t, source)
	draft := createBuilderPublishDraft(t, harness, document)
	harness.configs[0].Spec["changed"] = true

	const (
		create = `{"mode":"topology","topology":{"name":"generated","action":"create"}}`
		dryRun = `{"mode":"topology","topology":{"name":"generated","action":"create"},"dryRun":true}`
	)

	want := bdoc.Issue{
		Code: bdoc.CodePublishSourceChanged, Severity: bdoc.SeverityError,
		Message: "this diagram comes from topology source. That topology changed on the server " +
			"after Builder made the diagram. Publish refuses this diagram under any name. To continue, " +
			"make a new draft of topology source: import it again or open its published diagram. " +
			"Then copy your changes to the new draft",
	}

	// The Publish dialog warns of the refusal from the dry run, before Publish is selected.
	recorder, preview := previewBuilderDraft(t, harness, draft, nil, dryRun)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dry run from a stale source: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	if diff := cmp.Diff([]bdoc.Issue{want}, preview.Errors); diff != "" {
		t.Errorf("dry run from a stale source: errors mismatch (-want +got):\n%s", diff)
	}

	_, refusal := publishBuilderDraftAs(t, harness, draft, nil, create, http.StatusConflict)
	if refusal.Code != string(want.Code) || refusal.Message != want.Message {
		t.Errorf("publishing from a stale source: refusal = %s %q, want %s %q",
			refusal.Code, refusal.Message, want.Code, want.Message)
	}

	if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
		t.Errorf("publishing from a stale source wrote %d configs and %d published documents, want none", writes, documents)
	}
}

// TestBuilderPublishAnnotatesListedScenarios adds the topology to the topology
// annotation of each listed scenario that does not name it exactly, in either mode.
func TestBuilderPublishAnnotatesListedScenarios(t *testing.T) {
	for mode, body := range map[string]string{
		"topology": `{"mode":"topology","topology":{"name":"topo","action":"create"}}`,
		"topology-experiment": `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
			`"experiment":{"name":"exp","action":"create"}}`,
	} {
		t.Run(mode, func(t *testing.T) {
			harness := newBuilderHarness(t,
				namedScenario(t, "sc-a", "other , other"),
				namedScenario(t, "sc-b", ""),
				namedScenario(t, "sc-c", "topo-old, topo"),
				namedScenario(t, "sc-d", "topology2,xtopo"),
			)

			document := bdoc.NewDocument("annotated")
			document.Scenarios = []string{"sc-a", "sc-b", "sc-c", "sc-d"}
			draft := createBuilderPublishDraft(t, harness, document)

			response, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

			for name, want := range map[string]string{
				"sc-a": "other , other,topo", "sc-b": "topo", "sc-c": "topo-old, topo", "sc-d": "topology2,xtopo,topo",
			} {
				scenario, err := harness.getConfig("Scenario/" + name)
				if err != nil {
					t.Errorf("getConfig(Scenario/%s) returned error: %v", name, err)

					continue
				}

				if got := scenario.Metadata.Annotations["topology"]; got != want {
					t.Errorf("scenario %s topology annotation = %q, want %q", name, got, want)
				}

				if got := scenario.Metadata.Annotations["keep"]; got != "yes" {
					t.Errorf("scenario %s keep annotation = %q, want %q", name, got, "yes")
				}
			}

			// The topology and the three scenarios that changed.
			if harness.configWrites != 4 {
				t.Errorf("publishing topo: config writes = %d, want 4", harness.configWrites)
			}

			stage := builderStageNamed(t, response, builderPublishStageScenario)
			want := builderPublishStage{
				Name: builderPublishStageScenario, Status: "updated", Config: "",
				Message: "added topology topo to scenarios sc-a, sc-b, sc-d, and scenario sc-c already names it",
			}
			if diff := cmp.Diff(want, stage); diff != "" {
				t.Errorf("publishing topo: scenario stage mismatch (-want +got):\n%s", diff)
			}

			if response.Scenario != nil {
				t.Errorf("publishing topo: scenario = %+v, want none: no experiment scenario was picked", response.Scenario)
			}
		})
	}
}

// TestBuilderPublishScenarioStageReports names the scenario it annotates, and
// skips the stage when every scenario names the topology already.
func TestBuilderPublishScenarioStageReports(t *testing.T) {
	for name, test := range map[string]struct {
		configs []store.Config
		listed  []string
		want    builderPublishStage
	}{
		"one scenario": {
			configs: []store.Config{namedScenario(t, "sc", "")},
			listed:  []string{"sc"},
			want: builderPublishStage{
				Name: builderPublishStageScenario, Status: "updated", Config: "Scenario/sc",
				Message: "added topology topo to scenario sc",
			},
		},
		"every scenario names the topology": {
			configs: []store.Config{namedScenario(t, "sc-a", "topo"), namedScenario(t, "sc-b", "a,topo")},
			listed:  []string{"sc-a", "sc-b"},
			want: builderPublishStage{
				Name: builderPublishStageScenario, Status: bapi.PublishSkipped, Config: "",
				Message: "scenarios sc-a, sc-b already name topology topo",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newBuilderHarness(t, test.configs...)

			document := bdoc.NewDocument("reported")
			document.Scenarios = test.listed
			draft := createBuilderPublishDraft(t, harness, document)

			response, _ := publishBuilderDraft(t, harness, draft,
				`{"mode":"topology","topology":{"name":"topo","action":"create"}}`, http.StatusOK)

			if diff := cmp.Diff(test.want, builderStageNamed(t, response, builderPublishStageScenario)); diff != "" {
				t.Errorf("publishing topo: scenario stage mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// A draft that lists no scenario has no scenario stage.
	harness := newBuilderHarness(t, namedScenario(t, "sc", ""))
	draft := harness.createDraft(builderAlice, "plain")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"plain","action":"create"}}`, http.StatusOK)

	if slices.ContainsFunc(response.Stages, func(stage builderPublishStage) bool {
		return stage.Name == builderPublishStageScenario
	}) {
		t.Errorf("publishing plain: stages = %+v, want no scenario stage", response.Stages)
	}

	scenario, err := harness.getConfig("Scenario/sc")
	if err != nil {
		t.Fatalf("getConfig(Scenario/sc) returned error: %v", err)
	}

	if got := scenario.Metadata.Annotations["topology"]; got != "" {
		t.Errorf("publishing plain: unlisted scenario sc topology annotation = %q, want none", got)
	}
}

// TestBuilderPublishRefusesScenarioProblems refuses, before any write, a scenario
// that is missing, hidden or not updatable, and an unlisted experiment scenario.
func TestBuilderPublishRefusesScenarioProblems(t *testing.T) {
	everything := []string{"list", "get", "create", "update", "delete"}
	others := builderPolicy([]string{"topologies", "experiments", "scenarios", "schemas"}, []string{"*"}, everything)
	allTargets := builderPolicy([]string{"configs"}, []string{"*", "Topology/*", "Experiment/*"}, everything)

	hidden := builderRole(
		allTargets, others,
		builderPolicy([]string{"configs"}, []string{"Scenario/sc"}, everything),
	)
	readOnly := builderRole(
		allTargets, others,
		builderPolicy([]string{"configs"}, []string{"Scenario/*"}, []string{"list", "get"}),
	)

	experiment := `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
		`"scenario":{"name":"%s"},"experiment":{"name":"exp","action":"create"}}`

	for name, test := range map[string]struct {
		listed []string
		role   *rbac.Role
		body   string
		status int
		want   string
	}{
		"missing": {
			listed: []string{"sc", "gone"}, body: fmt.Sprintf(experiment, "sc"),
			status: http.StatusUnprocessableEntity, want: "scenario gone does not exist",
		},
		"hidden": {
			listed: []string{"sc", "secret"}, role: hidden, body: fmt.Sprintf(experiment, "sc"),
			status: http.StatusUnprocessableEntity, want: "scenario secret does not exist",
		},
		"not updatable": {
			listed: []string{"sc"}, role: readOnly,
			body:   `{"mode":"topology","topology":{"name":"topo","action":"create"}}`,
			status: http.StatusForbidden, want: "adding topology topo to scenario sc: user alice does not have permission",
		},
		"not listed": {
			listed: []string{"sc"}, body: fmt.Sprintf(experiment, "secret"),
			status: http.StatusUnprocessableEntity, want: "scenario secret is not one of the scenarios this draft lists",
		},
		"listed by none": {
			listed: nil, body: fmt.Sprintf(experiment, "sc"),
			status: http.StatusUnprocessableEntity, want: "scenario sc is not one of the scenarios this draft lists",
		},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newBuilderHarness(t, namedScenario(t, "sc", "other"), namedScenario(t, "secret", ""))

			document := bdoc.NewDocument("refused")
			document.Scenarios = test.listed
			draft := createBuilderPublishDraft(t, harness, document)

			_, refusal := publishBuilderDraftAs(t, harness, draft, test.role, test.body, test.status)
			if !strings.Contains(refusal.Message, test.want) {
				t.Errorf("publish %s: refusal = %q, want it to say %q", test.body, refusal.Message, test.want)
			}

			if configs, experiments, documents := harness.configWrites, harness.experimentWrites,
				harness.store.Count(bapi.NamespacePublished); configs != 0 || experiments != 0 || documents != 0 {
				t.Errorf("publish %s wrote %d configs, %d experiments and %d published documents, want none",
					test.body, configs, experiments, documents)
			}
		})
	}

	// A scenario that names the topology already needs no update permission.
	harness := newBuilderHarness(t, namedScenario(t, "sc", "topo"))

	document := bdoc.NewDocument("annotated")
	document.Scenarios = []string{"sc"}
	draft := createBuilderPublishDraft(t, harness, document)

	publishBuilderDraftAs(t, harness, draft, readOnly,
		`{"mode":"topology","topology":{"name":"topo","action":"create"}}`, http.StatusOK)
}

// namedScenario returns a Scenario config with one app, the topology annotation
// topologies (left out when empty) and the annotation keep.
func namedScenario(t *testing.T, name, topologies string) store.Config {
	t.Helper()

	scenario, err := store.NewConfig("Scenario/" + name)
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	scenario.Version = builderScenarioVersion
	scenario.Spec = map[string]any{"apps": []any{map[string]any{"name": name + "-app"}}}
	scenario.Metadata.Annotations = store.Annotations{"keep": "yes"}

	if topologies != "" {
		scenario.Metadata.Annotations["topology"] = topologies
	}

	return *scenario
}

// builderStageNamed returns the stage of a publish response named name.
func builderStageNamed(t *testing.T, response builderPublishResponse, name string) builderPublishStage {
	t.Helper()

	for _, stage := range response.Stages {
		if stage.Name == name {
			return stage
		}
	}

	t.Fatalf("stages = %+v, want a %s stage", response.Stages, name)

	return builderPublishStage{Name: "", Status: "", Message: "", Config: ""}
}

func TestBuilderPublishRejectsUnrelatedExperimentUpdate(t *testing.T) {
	experiment := builderConfig(t, kindExperiment, "existing-exp")
	harness := newBuilderHarness(t, experiment)
	draft := harness.createDraft(builderAlice, "unrelated")

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"new-topology","action":"create"},`+
			`"experiment":{"name":"existing-exp","action":"update"}}`, http.StatusConflict)
	if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
		t.Errorf("updating unrelated existing-exp wrote %d configs and %d published documents, want none", writes, documents)
	}
}

func TestBuilderPublishRejectsRunningExperimentUpdateBeforeWriting(t *testing.T) {
	experiment := builderConfig(t, kindExperiment, "running-exp")
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

	harness := newBuilderHarness(t, experiment)
	draft := createBuilderPublishDraft(t, harness, document, "Experiment/running-exp")

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"source-topology","action":"create"},`+
			`"experiment":{"name":"running-exp","action":"update"}}`, http.StatusConflict)
	if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
		t.Errorf("updating running-exp wrote %d configs and %d published documents, want none", writes, documents)
	}
}

func TestBuilderPublishUpdatesSourceExperiment(t *testing.T) {
	experiment := builderConfig(t, kindExperiment, "source-exp")
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

	harness := newBuilderHarness(t, experiment)
	draft := createBuilderPublishDraft(t, harness, document, "Experiment/source-exp")

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"source-topology","action":"create"},`+
			`"experiment":{"name":"source-exp","action":"update"}}`, http.StatusOK)

	updated, err := harness.getConfig("Experiment/source-exp")
	if err != nil {
		t.Fatalf("updated experiment missing: %v", err)
	}
	for key, want := range map[string]string{"topology": "source-topology", "keep": "yes"} {
		if got := updated.Metadata.Annotations[key]; got != want {
			t.Errorf("updating source-exp: annotation %s = %q, want %q", key, got, want)
		}
	}

	exp, err := types.DecodeExperimentFromConfig(*updated)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig returned error: %v", err)
	}

	switch node := exp.Spec.Topology().FindNodeByName("host"); {
	case node == nil:
		t.Errorf("updating source-exp: node host = nil, want it with 4096 MB of memory")
	case node.Hardware().Memory() != 4096:
		t.Errorf("updating source-exp: node host memory = %d, want 4096", node.Hardware().Memory())
	}
}

// builderScenarioVersion is the apiVersion of the Scenario configs the tests
// store, the latest phenix stores scenarios at.
const builderScenarioVersion = "phenix.sandia.gov/v2"

// scenarioConfig returns a Scenario config named "sc" with the given spec.
func scenarioConfig(t *testing.T, spec map[string]any) *store.Config {
	t.Helper()

	scenario, err := store.NewConfig("Scenario/sc")
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	scenario.Version = builderScenarioVersion
	scenario.Spec = spec

	return scenario
}

// TestBuilderPublishExperimentUsesPickedScenario creates the experiment with the
// picked scenario, after the topology was added to both.
func TestBuilderPublishExperimentUsesPickedScenario(t *testing.T) {
	harness := newBuilderHarness(t, namedScenario(t, "sc-a", ""), namedScenario(t, "sc-b", "other"))

	document := bdoc.NewDocument("picked")
	document.Scenarios = []string{"sc-a", "sc-b"}
	draft := createBuilderPublishDraft(t, harness, document)

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
			`"scenario":{"name":"sc-b"},"experiment":{"name":"exp","action":"create"}}`, http.StatusOK)

	created, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("experiment missing: %v", err)
	}

	if got := created.Metadata.Annotations["scenario"]; got != "sc-b" {
		t.Errorf("publishing exp with sc-b: scenario annotation = %q, want %q", got, "sc-b")
	}

	if harness.experimentWrites != 1 {
		t.Errorf("publishing exp with sc-b: experiment writes = %d, want 1", harness.experimentWrites)
	}

	if response.Scenario == nil || response.Scenario.Name != "sc-b" {
		t.Errorf("publishing exp with sc-b: scenario = %+v, want sc-b", response.Scenario)
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if meta.Publication == nil || meta.Publication.ScenarioTarget != "sc-b" {
		t.Errorf("publishing exp with sc-b: draft publication = %+v, want scenario target sc-b", meta.Publication)
	}

	// The scenarios are annotated before the experiment, which phenix creates only
	// with a scenario that names its topology.
	names := make([]string, len(response.Stages))
	for i, stage := range response.Stages {
		names[i] = stage.Name
	}

	if want := []string{"document", "topology", "scenario", "experiment", "draft"}; !slices.Equal(names, want) {
		t.Errorf("publishing exp with sc-b: stages = %q, want %q", names, want)
	}
}

// TestBuilderExperimentScenarioRoundTrip imports an experiment with a stored
// scenario and publishes it back with that scenario, then with none.
func TestBuilderExperimentScenarioRoundTrip(t *testing.T) {
	stored := scenarioConfig(t, map[string]any{
		"apps": []any{map[string]any{"name": "app", "metadata": map[string]any{"k": "v"}}},
	})

	experiment := builderConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{}},
		"vlans":    map[string]any{"aliases": map[string]any{}},
		"scenario": map[string]any{"apps": []any{map[string]any{
			"name": "old-app", "disabled": false,
		}}},
	}
	experiment.Metadata.Annotations = store.Annotations{"topology": "topo", "scenario": "sc"}

	harness := newBuilderHarness(t, experiment, *stored)
	document, warnings := postBuilderGenerate(t, harness, nil, `{"source":"Experiment/exp"}`)

	if !slices.Equal(document.Scenarios, []string{"sc"}) {
		t.Errorf("generating from exp: scenarios = %q with warnings %q, want [sc]", document.Scenarios, warnings)
	}

	// A device added in the editor, so the update has a topology to write.
	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID("host"), Kind: bdoc.NodeKindDevice, Label: "host",
		Device: &bdoc.Device{Hostname: "host", Spec: includeNode("host"), Interfaces: []bdoc.InterfaceHandle{}},
	})

	draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")
	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
			`"scenario":{"name":"sc"},"experiment":{"name":"exp","action":"update"}}`, http.StatusOK)

	updated, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("experiment missing: %v", err)
	}

	if got := updated.Metadata.Annotations["scenario"]; got != "sc" {
		t.Errorf("publishing exp with sc: scenario annotation = %q, want %q", got, "sc")
	}

	if apps := experimentAppNames(t, updated); !slices.Equal(apps, []string{"app"}) {
		t.Errorf("publishing exp with sc: apps = %q, want the stored scenario's [app]", apps)
	}

	scenario, err := harness.getConfig("Scenario/sc")
	if err != nil {
		t.Fatalf("getConfig(Scenario/sc) returned error: %v", err)
	}

	if got := scenario.Metadata.Annotations["topology"]; got != "topo" {
		t.Errorf("publishing exp with sc: scenario sc topology annotation = %q, want %q", got, "topo")
	}

	// Published again without a scenario, the experiment has none.
	publishBuilderDraft(t, harness, response.Draft,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"update"},`+
			`"experiment":{"name":"exp","action":"update"}}`, http.StatusOK)

	cleared, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("experiment missing: %v", err)
	}

	if got, ok := cleared.Metadata.Annotations["scenario"]; ok {
		t.Errorf("publishing exp without a scenario: scenario annotation = %q, want none", got)
	}

	if apps := experimentAppNames(t, cleared); len(apps) != 0 {
		t.Errorf("publishing exp without a scenario: apps = %q, want none", apps)
	}
}

// TestBuilderPublishUnchangedExperimentImport imports an experiment as phenix
// stores it, with the Init defaults, and publishes it back unchanged. The
// import drops the defaults, but the update is already applied.
func TestBuilderPublishUnchangedExperimentImport(t *testing.T) {
	experiment := phenixStoredExperiment(t, "exp", "topo", map[string]any{
		"topology": map[string]any{"nodes": []any{includeNode("host"), includeNode("web")}},
		"vlans":    map[string]any{"aliases": map[string]any{"EXP": 101}},
	})

	harness := newBuilderHarness(t, experiment)
	document, warnings := postBuilderGenerate(t, harness, nil, `{"source":"Experiment/exp"}`)
	draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
			`"experiment":{"name":"exp","action":"update"}}`, http.StatusOK)

	if stage := builderStageNamed(t, response, builderPublishStageExperiment); stage.Status != bapi.PublishSkipped {
		t.Errorf("publishing the unchanged import of exp (warnings %q): experiment stage = %+v, want it skipped",
			warnings, stage)
	}

	stored, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("experiment missing: %v", err)
	}

	if diff := cmp.Diff(experiment.Spec, builderJSONValue(t, stored.Spec)); diff != "" {
		t.Errorf("publishing the unchanged import of exp: experiment spec mismatch (-want +got):\n%s", diff)
	}

	// A changed device is not applied yet.
	document.Nodes[0].Device.Spec["hardware"].(map[string]any)["memory"] = 2048
	changed := createBuilderPublishDraft(t, harness, document, "Experiment/exp")

	response, _ = publishBuilderDraft(t, harness, changed,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"update"},`+
			`"experiment":{"name":"exp","action":"update"}}`, http.StatusOK)

	if stage := builderStageNamed(t, response, builderPublishStageExperiment); stage.Status != "updated" {
		t.Errorf("publishing exp with 2048 MB of memory: experiment stage = %+v, want it updated", stage)
	}
}

// phenixStoredExperiment returns experiment name of topology as phenix stores
// it: it runs Init on spec, and stores every field (see hookCreate in
// api/experiment).
func phenixStoredExperiment(t *testing.T, name, topology string, spec map[string]any) store.Config {
	t.Helper()

	config := builderConfig(t, kindExperiment, name)
	config.Metadata.Annotations = store.Annotations{"topology": topology}
	config.Spec = spec
	config.Spec["experimentName"] = name

	exp, err := types.DecodeExperimentFromConfig(config)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig returned error: %v", err)
	}

	if err := exp.Spec.Init(); err != nil {
		t.Fatalf("ExperimentSpec.Init returned error: %v", err)
	}

	config.Spec = builderJSONValue(t, structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)).(map[string]any)

	return config
}

// experimentAppNames returns the names of the scenario apps an experiment
// config holds.
func experimentAppNames(t *testing.T, config *store.Config) []string {
	t.Helper()

	exp, err := types.DecodeExperimentFromConfig(*config)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig returned error: %v", err)
	}

	var names []string

	if exp.Spec.Scenario() != nil {
		for _, app := range exp.Spec.Scenario().Apps() {
			names = append(names, app.Name())
		}
	}

	return names
}

// TestBuilderGenerateExperimentScenario lists an experiment's scenario only when
// the caller may list its config, and warns otherwise.
func TestBuilderGenerateExperimentScenario(t *testing.T) {
	stored := scenarioConfig(t, map[string]any{"apps": []any{map[string]any{"name": "app"}}})

	experiment := builderConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{}},
		"vlans":    map[string]any{"aliases": map[string]any{}},
		"scenario": map[string]any{"apps": []any{map[string]any{"name": "embedded-app"}}},
	}
	experiment.Metadata.Annotations = store.Annotations{"topology": "topo", "scenario": "sc"}

	content, err := json.Marshal(experiment)
	if err != nil {
		t.Fatalf("encoding the experiment file: %v", err)
	}

	upload := builderJSON(t, map[string]string{"content": string(content)})

	hidden := builderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get", "create"}),
		builderPolicy([]string{"experiments"}, []string{"*"}, []string{"list"}),
	)

	const notStored = `the experiment's scenario "sc" is not a stored Scenario config. The Builder did not attach it`

	for name, test := range map[string]struct {
		configs []store.Config
		role    *rbac.Role
		body    string
		listed  []string
		warning string
	}{
		"stored": {
			configs: []store.Config{experiment, *stored}, body: `{"source":"Experiment/exp"}`,
			listed: []string{"sc"},
		},
		"missing": {
			configs: []store.Config{experiment}, body: `{"source":"Experiment/exp"}`, warning: notStored,
		},
		"hidden": {
			configs: []store.Config{experiment, *stored}, role: hidden, body: `{"source":"Experiment/exp"}`,
			warning: notStored,
		},
		"file with a stored scenario": {
			configs: []store.Config{*stored}, body: upload, listed: []string{"sc"},
			warning: `scenario "sc" is this server's Scenario config of that name, not the copy the experiment file holds`,
		},
		"file without one": {
			configs: nil, body: upload, warning: notStored,
		},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newBuilderHarness(t, test.configs...)
			document, warnings := postBuilderGenerate(t, harness, test.role, test.body)

			if !slices.Equal(document.Scenarios, test.listed) {
				t.Errorf("generating %s: scenarios = %q, want %q", test.body, document.Scenarios, test.listed)
			}

			if test.warning == "" && slices.ContainsFunc(warnings, func(warning string) bool {
				return strings.Contains(warning, "scenario")
			}) {
				t.Errorf("generating %s: warnings = %q, want none about the scenario", test.body, warnings)
			}

			if test.warning != "" && !slices.Contains(warnings, test.warning) {
				t.Errorf("generating %s: warnings = %q, want %q", test.body, warnings, test.warning)
			}

			// The document leaves out warnings it has none of.
			if !slices.Equal(document.Source.Warnings, warnings) {
				t.Errorf("generating %s: source warnings = %q, want %q", test.body, document.Source.Warnings, warnings)
			}

			data, err := bapi.EncodeDocument(document)
			if err != nil {
				t.Fatalf("EncodeDocument returned error: %v", err)
			}

			if strings.Contains(string(data), "embedded-app") {
				t.Errorf("generating %s = %s, want no content of the experiment's scenario", test.body, data)
			}
		})
	}
}

// postBuilderGenerate posts body to /builder/generate and returns the
// generated document and its warnings.
func postBuilderGenerate(
	t *testing.T,
	harness *builderHarness,
	role *rbac.Role,
	body string,
) (*bdoc.Document, []string) {
	t.Helper()

	var response builderGenerateResponse

	harness.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/generate", body: body,
		user: builderAlice, role: role,
	}, http.StatusOK, &response)

	document, err := bdoc.Parse(response.Document)
	if err != nil {
		t.Fatalf("generated document: %v", err)
	}

	return document, response.Warnings
}

// TestBuilderPublishResumesAfterScenarioFailure fails the scenario stage, then
// publishes again from there, and a retry skips every stage.
func TestBuilderPublishResumesAfterScenarioFailure(t *testing.T) {
	harness := newBuilderHarness(t, namedScenario(t, "sc-a", ""), namedScenario(t, "sc-b", ""))

	document := bdoc.NewDocument("resume")
	document.Scenarios = []string{"sc-a", "sc-b"}
	draft := createBuilderPublishDraft(t, harness, document)
	body := `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
		`"scenario":{"name":"sc-a"},"experiment":{"name":"exp","action":"create"}}`

	harness.failConfigKind = builderKindScenario
	failed, _ := publishBuilderDraft(t, harness, draft, body, http.StatusInternalServerError)

	if failed.Status != bapi.PublishPartial {
		t.Errorf("publishing with the scenario write failing: status = %q, want %q", failed.Status, bapi.PublishPartial)
	}

	if len(failed.Stages) == 0 {
		t.Fatalf("publishing with the scenario write failing: stages = none, want the scenario stage failed last")
	}

	wantFailed := builderPublishStage{
		Name: builderPublishStageScenario, Status: bapi.PublishFailed, Message: "scenario publication failed",
	}
	if diff := cmp.Diff(wantFailed, failed.Stages[len(failed.Stages)-1],
		cmpopts.IgnoreFields(builderPublishStage{}, "Config")); diff != "" {
		t.Errorf("publishing with the scenario write failing: last stage mismatch (-want +got):\n%s", diff)
	}

	if harness.experimentWrites != 0 {
		t.Errorf("publishing with the scenario write failing: experiment writes = %d, want none", harness.experimentWrites)
	}

	harness.failConfigKind = ""
	published, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	if stage := builderStageNamed(t, published, builderPublishStageTopology); stage.Status != bapi.PublishSkipped {
		t.Errorf("publishing again: topology stage = %+v, want it skipped", stage)
	}

	if stage := builderStageNamed(t, published, builderPublishStageScenario); stage.Status != "updated" {
		t.Errorf("publishing again: scenario stage = %+v, want it updated", stage)
	}

	retried, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	if slices.ContainsFunc(retried.Stages, func(stage builderPublishStage) bool {
		return stage.Status != bapi.PublishSkipped
	}) {
		t.Errorf("retrying the publication: stages = %+v, want every stage skipped", retried.Stages)
	}

	if stage := builderStageNamed(t, retried, builderPublishStageScenario); stage.Config != "" {
		t.Errorf("retrying the publication: scenario stage = %+v, want no one config named for two scenarios", stage)
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
		t.Errorf("GetPublishedDocumentData(the topology's document) returned error: %v", err)
	}

	if count := harness.store.Count(bapi.NamespacePublished); count != 1 {
		t.Errorf("published documents = %d, want 1", count)
	}

	// The topology once, then each scenario once.
	if harness.configWrites != 3 || harness.experimentWrites != 1 {
		t.Errorf("writes after the retry = %d configs and %d experiments, want 3 and 1",
			harness.configWrites, harness.experimentWrites)
	}

	for _, name := range []string{"sc-a", "sc-b"} {
		scenario, err := harness.getConfig("Scenario/" + name)
		if err != nil {
			t.Errorf("getConfig(Scenario/%s) returned error: %v", name, err)

			continue
		}

		if got := scenario.Metadata.Annotations["topology"]; got != "topo" {
			t.Errorf("scenario %s topology annotation = %q, want %q", name, got, "topo")
		}
	}
}

// TestBuilderPublishReportsScenariosWrittenBeforeFailure warns of the scenario
// written before the next one failed, and finishes when published again.
func TestBuilderPublishReportsScenariosWrittenBeforeFailure(t *testing.T) {
	harness := newBuilderHarness(t, namedScenario(t, "sc-a", "other"), namedScenario(t, "sc-b", ""))

	document := bdoc.NewDocument("partway")
	document.Scenarios = []string{"sc-a", "sc-b"}
	draft := createBuilderPublishDraft(t, harness, document)
	body := `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
		`"scenario":{"name":"sc-b"},"experiment":{"name":"exp","action":"create"}}`

	annotations := func() []string {
		t.Helper()

		values := make([]string, 0, len(document.Scenarios))

		for _, name := range document.Scenarios {
			scenario, err := harness.getConfig("Scenario/" + name)
			if err != nil {
				t.Fatalf("scenario %s missing: %v", name, err)
			}

			values = append(values, scenario.Metadata.Annotations["topology"])
		}

		return values
	}

	harness.failConfigName = "Scenario/sc-b"
	failed, _ := publishBuilderDraft(t, harness, draft, body, http.StatusInternalServerError)

	if failed.Status != bapi.PublishPartial {
		t.Errorf("publishing with sc-b failing: status = %q, want %q", failed.Status, bapi.PublishPartial)
	}

	if len(failed.Stages) == 0 {
		t.Fatalf("publishing with sc-b failing: stages = none, want the scenario stage failed last")
	}

	if stage := failed.Stages[len(failed.Stages)-1]; stage.Name != builderPublishStageScenario || stage.Status != bapi.PublishFailed {
		t.Errorf("publishing with sc-b failing: last stage = %+v, want the scenario stage failed", stage)
	}

	warning := bdoc.NewIssue(bdoc.CodePublishScenarioPartial, "",
		"phenix added topology topo to scenario sc-a before the scenario stage failed")
	if !slices.Contains(failed.Warnings, warning) {
		t.Errorf("publishing with sc-b failing: warnings = %+v, want %+v", failed.Warnings, warning)
	}

	if got, want := annotations(), []string{"other,topo", ""}; !slices.Equal(got, want) {
		t.Errorf("publishing with sc-b failing: topology annotations = %q, want %q", got, want)
	}

	if harness.experimentWrites != 0 {
		t.Errorf("publishing with sc-b failing: experiment writes = %d, want none", harness.experimentWrites)
	}

	harness.failConfigName = ""
	published, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	stage := builderStageNamed(t, published, builderPublishStageScenario)
	want := builderPublishStage{
		Name: builderPublishStageScenario, Status: "updated", Config: "",
		Message: "added topology topo to scenario sc-b, and scenario sc-a already names it",
	}
	if diff := cmp.Diff(want, stage); diff != "" {
		t.Errorf("publishing again: scenario stage mismatch (-want +got):\n%s", diff)
	}

	if got, want := annotations(), []string{"other,topo", "topo"}; !slices.Equal(got, want) {
		t.Errorf("publishing again: topology annotations = %q, want %q", got, want)
	}

	// The topology and each scenario once, then the experiment.
	if harness.configWrites != 3 || harness.experimentWrites != 1 {
		t.Errorf("writes after the retry = %d configs and %d experiments, want 3 and 1",
			harness.configWrites, harness.experimentWrites)
	}

	created, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("getConfig(Experiment/exp) returned error: %v", err)
	}

	if got := created.Metadata.Annotations["scenario"]; got != "sc-b" {
		t.Errorf("publishing again: experiment scenario annotation = %q, want %q", got, "sc-b")
	}
}

// TestBuilderPublishSwitchesExperimentScenario publishes the experiment with one
// scenario, then another, whose apps replace the first's.
func TestBuilderPublishSwitchesExperimentScenario(t *testing.T) {
	experiment := builderConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{}},
		"vlans":    map[string]any{"aliases": map[string]any{}},
	}
	experiment.Metadata.Annotations = store.Annotations{"topology": "topo", "scenario": "sc-a"}

	harness := newBuilderHarness(t, experiment, namedScenario(t, "sc-a", "topo"), namedScenario(t, "sc-b", ""))
	document, warnings := postBuilderGenerate(t, harness, nil, `{"source":"Experiment/exp"}`)

	if !slices.Equal(document.Scenarios, []string{"sc-a"}) {
		t.Errorf("generating from exp: scenarios = %q with warnings %q, want [sc-a]", document.Scenarios, warnings)
	}

	document.Scenarios = append(document.Scenarios, "sc-b")
	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID("host"), Kind: bdoc.NodeKindDevice, Label: "host",
		Device: &bdoc.Device{Hostname: "host", Spec: includeNode("host"), Interfaces: []bdoc.InterfaceHandle{}},
	})

	draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")
	body := `{"mode":"topology-experiment","topology":{"name":"topo","action":"%s"},` +
		`"scenario":{"name":"%s"},"experiment":{"name":"exp","action":"update"}}`

	// The experiment's scenario as the experiment config holds it: the name
	// it is annotated with, and the apps it holds.
	scenarioOf := func() (string, []string) {
		t.Helper()

		updated, err := harness.getConfig("Experiment/exp")
		if err != nil {
			t.Fatalf("experiment missing: %v", err)
		}

		return updated.Metadata.Annotations["scenario"], experimentAppNames(t, updated)
	}

	first, _ := publishBuilderDraft(t, harness, draft, fmt.Sprintf(body, "create", "sc-a"), http.StatusOK)

	if name, apps := scenarioOf(); name != "sc-a" || !slices.Equal(apps, []string{"sc-a-app"}) {
		t.Errorf("publishing exp with sc-a: scenario = %s with apps %q, want sc-a with [sc-a-app]", name, apps)
	}

	second, _ := publishBuilderDraft(t, harness, first.Draft, fmt.Sprintf(body, "update", "sc-b"), http.StatusOK)

	if name, apps := scenarioOf(); name != "sc-b" || !slices.Equal(apps, []string{"sc-b-app"}) {
		t.Errorf("publishing exp with sc-b: scenario = %s with apps %q, want sc-b with [sc-b-app]", name, apps)
	}

	if stage := builderStageNamed(t, second, builderPublishStageExperiment); stage.Status != "updated" {
		t.Errorf("publishing exp with sc-b: experiment stage = %+v, want it updated", stage)
	}

	// Both scenarios named the topology since the first publication.
	if stage := builderStageNamed(t, second, builderPublishStageScenario); stage.Status != bapi.PublishSkipped {
		t.Errorf("publishing exp with sc-b: scenario stage = %+v, want it skipped", stage)
	}

	if second.Scenario == nil || second.Scenario.Name != "sc-b" {
		t.Errorf("publishing exp with sc-b: scenario = %+v, want sc-b", second.Scenario)
	}
}

// TestAddTopologyAnnotation adds a topology to a scenario's annotation unless a
// name of it is the topology, keeping the others byte for byte.
func TestAddTopologyAnnotation(t *testing.T) {
	for _, test := range []struct{ name, value, want string }{
		{"empty", "", "topo"},
		{"another topology", "other", "other,topo"},
		{"blank names", " a , ,a,", " a , ,a,,topo"},
		{"names that contain the topology", "xtopo,topology", "xtopo,topology,topo"},
		{"the topology with spaces", "a, topo ,b", "a, topo ,b"},
		{"the topology", "topo", "topo"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := addTopologyAnnotation(test.value, "topo"); got != test.want {
				t.Errorf("addTopologyAnnotation(%q, %q) = %q, want %q", test.value, "topo", got, test.want)
			}
		})
	}
}

func TestBuilderPublishRetryIsIdempotent(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "retry")
	body := `{"mode":"topology","topology":{"name":"retry","action":"create"}}`

	publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	tooStale := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.ID + "/publish",
		body:   body, user: builderAlice, ifMatch: `"0"`,
	})
	if tooStale.Code != http.StatusPreconditionFailed {
		t.Errorf("publishing retry with If-Match \"0\": status = %d, want %d", tooStale.Code, http.StatusPreconditionFailed)
	}

	response, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	if response.Status != bapi.PublishSucceeded {
		t.Errorf("publishing retry again: status = %q, want %q", response.Status, bapi.PublishSucceeded)
	}

	if messages := strings.Join(bdoc.IssueMessages(response.Warnings), " "); !strings.Contains(messages, "already complete") {
		t.Errorf("publishing retry again: warnings = %q, want one saying it is already complete", messages)
	}

	if len(response.Warnings) == 0 || response.Warnings[0].Code != bdoc.CodePublishRetryComplete {
		t.Errorf("publishing retry again: warnings = %+v, want the first of code %s", response.Warnings, bdoc.CodePublishRetryComplete)
	}

	if harness.configWrites != 1 {
		t.Errorf("publishing retry twice: config writes = %d, want 1", harness.configWrites)
	}
}

// TestBuilderPublishExperimentWithIncludedTopology writes the topology with its
// include, and the experiment keeps the included node once.
func TestBuilderPublishExperimentWithIncludedTopology(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)
	document := generateBuilderDocument(t, harness, "Experiment/exp")

	if included := document.FindDevice("inc-host"); included == nil || included.Device.IncludedFrom != "shared" {
		t.Errorf("generating from exp = %s, want inc-host included from shared", builderJSON(t, document))
	}

	draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"root","action":"update"},`+
			`"experiment":{"name":"exp","action":"update"}}`, http.StatusOK)

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

	if got := hostnames(topology.Spec); !slices.Equal(got, []string{"web"}) {
		t.Errorf("publishing root: nodes = %q, want [web]", got)
	}

	if diff := cmp.Diff(any([]string{"shared"}), topology.Spec["includeTopologies"]); diff != "" {
		t.Errorf("publishing root: includeTopologies mismatch (-want +got):\n%s", diff)
	}

	updated, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("updated experiment missing: %v", err)
	}

	if got := hostnames(updated.Spec["topology"]); !slices.Equal(got, []string{"web", "inc-host"}) {
		t.Errorf("publishing exp: nodes = %q, want web and the merged inc-host once", got)
	}
}

// includeNode is a complete node spec named hostname, for include tests. Its
// address is the hostname's own, as publishing refuses two interfaces with one.
func includeNode(hostname string) map[string]any {
	sum := crc32.ChecksumIEEE([]byte(hostname))

	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": hostname, "vm_type": "kvm"},
		"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "miniccc.qc2"}}},
		"network": map[string]any{"interfaces": []any{map[string]any{
			"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "static",
			"address": fmt.Sprintf("10.%d.%d.%d", byte(sum>>16), byte(sum>>8), byte(sum)), "mask": 8,
		}}},
	}
}

// includedTopologyFixture returns topologies shared (inc-host) and root (web,
// including includes), and experiment exp of root holding both nodes.
func includedTopologyFixture(t *testing.T, includes ...any) []store.Config {
	t.Helper()

	shared := builderConfig(t, builderKindTopology, "shared")
	shared.Spec = map[string]any{"nodes": []any{includeNode("inc-host")}}

	root := builderConfig(t, builderKindTopology, "root")
	root.Spec = map[string]any{"nodes": []any{includeNode("web")}, "includeTopologies": includes}

	experiment := builderConfig(t, kindExperiment, "exp")
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
func generateBuilderDocument(t *testing.T, harness *builderHarness, source string) *bdoc.Document {
	t.Helper()

	document, _ := postBuilderGenerate(t, harness, nil, `{"source":"`+source+`"}`)

	return document
}

// TestBuilderPublishRejectsIncludedHostnameClash refuses, before any write, a
// topology whose included topology gained a node of the same name.
func TestBuilderPublishRejectsIncludedHostnameClash(t *testing.T) {
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
			harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)
			document := generateBuilderDocument(t, harness, test.source)
			draft := createBuilderPublishDraft(t, harness, document, test.source)

			for i := range harness.configs {
				if harness.configs[i].FullName() == "Topology/shared" {
					harness.configs[i].Spec = map[string]any{"nodes": []any{includeNode("inc-host"), includeNode("WEB")}}
				}
			}

			_, reason := publishBuilderDraft(t, harness, draft, test.body, http.StatusConflict)

			want := "phenix cannot publish topology root: node WEB is in this topology and in its included topology shared"
			if !strings.Contains(reason, want) {
				t.Errorf("publish %s: refusal = %q, want it to say %q", test.body, reason, want)
			}

			if harness.configWrites != 0 || harness.experimentWrites != 0 {
				t.Errorf("publish %s wrote %d configs and %d experiments, want none",
					test.body, harness.configWrites, harness.experimentWrites)
			}
		})
	}
}

// TestBuilderPublishCopyCreatesNewTopology creates the copy's topology with the
// includes of the original, and never updates the original.
func TestBuilderPublishCopyCreatesNewTopology(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)

	original, err := harness.getConfig("Topology/root")
	if err != nil {
		t.Fatalf("topology root missing: %v", err)
	}

	document, _ := postBuilderGenerate(t, harness, nil, `{"source":"Topology/root","copy":true}`)

	// A copy's draft has no source token. One sent all the same is refused too.
	for name, token := range map[string]string{"no source token": "", "the source token": "Topology/root"} {
		t.Run(name, func(t *testing.T) {
			draft := createBuilderPublishDraft(t, harness, document, token)

			_, refusal := publishBuilderDraft(t, harness, draft,
				`{"mode":"topology","topology":{"name":"root","action":"update"}}`, http.StatusConflict)
			if want := "topology root is not the source of this draft"; refusal != want {
				t.Errorf("publishing the copy over root: refusal = %q, want %q", refusal, want)
			}

			if harness.configWrites != 0 {
				t.Errorf("publishing the copy over root wrote %d configs, want none", harness.configWrites)
			}
		})
	}

	draft := createBuilderPublishDraft(t, harness, document, "")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"root-copy","action":"create"}}`, http.StatusOK)
	if response.Topology == nil || response.Topology.Name != "root-copy" {
		t.Errorf("publishing the copy as root-copy: topology = %+v, want root-copy", response.Topology)
	}

	created, err := harness.getConfig("Topology/root-copy")
	if err != nil {
		t.Fatalf("topology root-copy missing: %v", err)
	}

	// The included node stays included: the copy holds the topology's own
	// node and the reference.
	if got := topologyHostnames(t, harness, "root-copy"); !slices.Equal(got, []string{"web"}) {
		t.Errorf("publishing the copy as root-copy: nodes = %q, want [web]", got)
	}

	if diff := cmp.Diff(any([]string{"shared"}), created.Spec["includeTopologies"]); diff != "" {
		t.Errorf("publishing the copy as root-copy: includeTopologies mismatch (-want +got):\n%s", diff)
	}

	if _, err := bapi.DecodeReference(created.Metadata.Annotations[bapi.DocumentAnnotation]); err != nil {
		t.Errorf("publishing the copy as root-copy: DecodeReference(document annotation) returned error: %v", err)
	}

	after, err := harness.getConfig("Topology/root")
	if err != nil {
		t.Fatalf("topology root missing: %v", err)
	}

	if diff := cmp.Diff(original, after); diff != "" {
		t.Errorf("publishing the copy as root-copy changed root (-want +got):\n%s", diff)
	}

	if harness.configWrites != 1 {
		t.Errorf("publishing the copy as root-copy: config writes = %d, want only the new topology", harness.configWrites)
	}
}

// TestBuilderPublishCombinedCreatesNewTopology creates a topology of every node,
// with no includes, and leaves the sources as they were.
func TestBuilderPublishCombinedCreatesNewTopology(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)
	before := builderJSONValue(t, harness.configs)

	document, _ := postBuilderGenerate(t, harness, nil, `{"source":"Topology/root","includes":"combine"}`)
	draft := createBuilderPublishDraft(t, harness, document, "")

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"root","action":"update"}}`, http.StatusConflict)
	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"root-combined","action":"create"}}`, http.StatusOK)

	created, err := harness.getConfig("Topology/root-combined")
	if err != nil {
		t.Fatalf("topology root-combined missing: %v", err)
	}

	if got := topologyHostnames(t, harness, "root-combined"); !slices.Equal(got, []string{"inc-host", "web"}) {
		t.Errorf("publishing root-combined: nodes = %q, want [inc-host web]", got)
	}

	if included, ok := created.Spec["includeTopologies"]; ok {
		t.Errorf("publishing root-combined: includeTopologies = %v, want none", included)
	}

	if diff := cmp.Diff(before, builderJSONValue(t, harness.configs[:3])); diff != "" {
		t.Errorf("publishing root-combined changed the configs it was combined from (-want +got):\n%s", diff)
	}
}

// TestBuilderPublishCombinedRefusesKeptIncludeClash refuses a combined draft
// whose kept include defines a hostname of the document.
func TestBuilderPublishCombinedRefusesKeptIncludeClash(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared", "later")...)

	document, warnings := postBuilderGenerate(t, harness, nil, `{"source":"Topology/root","includes":"combine"}`)

	if got := document.Source.IncludeTopologies; !slices.Equal(got, []string{"later"}) {
		t.Errorf("combining root: includes = %q, want only the include that cannot be read, [later]", got)
	}

	const kept = "The Builder did not combine included topology later. It stays in includeTopologies, and publishing keeps the reference."
	if !slices.Contains(warnings, kept) {
		t.Errorf("combining root: warnings = %q, want %q", warnings, kept)
	}

	draft := createBuilderPublishDraft(t, harness, document, "")

	// The topology appears, with a node named like one the draft copied.
	later := builderConfig(t, builderKindTopology, "later")
	later.Spec = map[string]any{"nodes": []any{includeNode("INC-HOST")}}
	harness.configs = append(harness.configs, later)

	_, refusal := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"root-combined","action":"create"}}`, http.StatusConflict)

	want := "phenix cannot publish topology root-combined: node INC-HOST is in this topology and in its included topology later"
	if !strings.Contains(refusal, want) {
		t.Errorf("publishing root-combined with a clash: refusal = %q, want it to say %q", refusal, want)
	}

	if harness.configWrites != 0 {
		t.Errorf("publishing root-combined with a clash wrote %d configs, want none", harness.configWrites)
	}

	// Once the clash is gone the draft publishes, and keeps the reference.
	harness.configs[len(harness.configs)-1].Spec = map[string]any{"nodes": []any{includeNode("later-host")}}

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"root-combined","action":"create"}}`, http.StatusOK)

	created, err := harness.getConfig("Topology/root-combined")
	if err != nil {
		t.Fatalf("topology root-combined missing: %v", err)
	}

	if got := topologyHostnames(t, harness, "root-combined"); !slices.Equal(got, []string{"inc-host", "web"}) {
		t.Errorf("publishing root-combined: nodes = %q, want [inc-host web]", got)
	}

	if diff := cmp.Diff(any([]string{"later"}), created.Spec["includeTopologies"]); diff != "" {
		t.Errorf("publishing root-combined: includeTopologies mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderPublishRefusesIncludedHostnamePhenixRefuses refuses an experiment
// whose included topology holds a hostname phenix refuses. A topology publishes.
func TestBuilderPublishRefusesIncludedHostnamePhenixRefuses(t *testing.T) {
	publications := []struct {
		name, source, body, experiment string
		code                           int
	}{
		{
			name: "topology", source: "Topology/root",
			body: `{"mode":"topology","topology":{"name":"root","action":"update"}}`,
			code: http.StatusOK,
		},
		{
			name: "experiment create", source: "Topology/root",
			body: `{"mode":"topology-experiment","topology":{"name":"root","action":"update"},` +
				`"experiment":{"name":"fresh","action":"create"}}`,
			experiment: "fresh", code: http.StatusUnprocessableEntity,
		},
		{
			name: "experiment update", source: "Experiment/exp",
			body: `{"mode":"topology-experiment","topology":{"name":"root","action":"update"},` +
				`"experiment":{"name":"exp","action":"update"}}`,
			experiment: "exp", code: http.StatusUnprocessableEntity,
		},
	}

	for _, refused := range []struct{ hostname, reason string }{
		{hostname: "all", reason: "hostname 'all' is reserved: "},
		{hostname: "x", reason: "hostname 'x' is 1 character long: "},
	} {
		t.Run(refused.hostname, func(t *testing.T) {
			for _, test := range publications {
				t.Run(test.name, func(t *testing.T) {
					harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)
					document := generateBuilderDocument(t, harness, test.source)
					draft := createBuilderPublishDraft(t, harness, document, test.source)

					for i := range harness.configs {
						if harness.configs[i].FullName() == "Topology/shared" {
							harness.configs[i].Spec = map[string]any{
								"nodes": []any{includeNode("inc-host"), includeNode(refused.hostname)},
							}
						}
					}

					_, refusal := publishBuilderDraft(t, harness, draft, test.body, test.code)

					if test.code == http.StatusOK {
						return
					}

					want := "phenix cannot publish experiment " + test.experiment + ": In included topology shared, " + refused.reason
					if !strings.Contains(refusal, want) {
						t.Errorf("publish %s: refusal = %q, want it to say %q", test.body, refusal, want)
					}

					if harness.configWrites != 0 || harness.experimentWrites != 0 {
						t.Errorf("publish %s wrote %d configs and %d experiments, want none",
							test.body, harness.configWrites, harness.experimentWrites)
					}
				})
			}
		})
	}
}

// TestBuilderPublishExperimentUpdateChecksIncludes stops an experiment update
// before any write at an include the caller may not read, or a file path.
func TestBuilderPublishExperimentUpdateChecksIncludes(t *testing.T) {
	everything := []string{"list", "get", "create", "update", "delete"}
	noShared := builderRole(
		builderPolicy([]string{"configs", "schemas", "experiments", "scenarios"}, []string{"*", "*/*"}, everything),
		builderPolicy([]string{"topologies"}, []string{"root"}, everything),
	)

	for _, test := range []struct {
		name     string
		includes []any
		role     *rbac.Role
		status   int
		want     string
	}{
		{
			name: "forbidden", includes: []any{"shared"}, role: noShared, status: http.StatusForbidden,
			want: "merging included topology shared into experiment exp: user alice does not have permission",
		},
		{
			name: "file", includes: []any{"shared", "/srv/extra.yml"}, role: nil, status: http.StatusUnprocessableEntity,
			want: "phenix cannot update experiment exp: it cannot merge included topology /srv/extra.yml: " +
				"the Builder reads included topologies from the config store only",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderHarness(t, includedTopologyFixture(t, test.includes...)...)
			document := generateBuilderDocument(t, harness, "Experiment/exp")
			draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")

			_, refusal := publishBuilderDraftAs(t, harness, draft, test.role,
				`{"mode":"topology-experiment","topology":{"name":"root","action":"update"},`+
					`"experiment":{"name":"exp","action":"update"}}`, test.status)
			if !strings.Contains(refusal.Message, test.want) {
				t.Errorf("updating exp: refusal = %q, want it to say %q", refusal.Message, test.want)
			}

			if harness.configWrites != 0 || harness.experimentWrites != 0 {
				t.Errorf("updating exp wrote %d configs and %d experiments, want none",
					harness.configWrites, harness.experimentWrites)
			}
		})
	}
}

// openedBuilderDocument is a published document as the editor opens it.
type openedBuilderDocument struct {
	id, etag string
	document *bdoc.Document
}

// openPublishedBuilderDocument reads the published document of a stored
// topology through GET /builder/documents/{id}.
func openPublishedBuilderDocument(t *testing.T, harness *builderHarness, topology string) openedBuilderDocument {
	t.Helper()

	config, err := harness.getConfig(builderKindTopology + "/" + topology)
	if err != nil {
		t.Fatalf("topology %s missing: %v", topology, err)
	}

	ref, err := bapi.DecodeReference(config.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("DecodeReference returned error: %v", err)
	}

	recorder := harness.do(builderRequest{method: http.MethodGet, path: "/builder/documents/" + ref.ID, user: builderAlice})
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /builder/documents/%s: status = %d, want %d: %s", ref.ID, recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderDocumentResponse

	decodeBuilderAnswer(t, recorder, &response)

	document, err := bdoc.Decode(response.Document)
	if err != nil {
		t.Fatalf("decoding document %s: %v", ref.ID, err)
	}

	if got := recorder.Header().Get("ETag"); got == "" || got != response.ETag {
		t.Errorf("GET /builder/documents/%s: ETag header = %q, want the etag %q of the body", ref.ID, got, response.ETag)
	}

	return openedBuilderDocument{id: ref.ID, etag: response.ETag, document: document}
}

// topologyHostnames returns the hostnames of a stored topology's nodes.
func topologyHostnames(t *testing.T, harness *builderHarness, name string) []string {
	t.Helper()

	topology, err := harness.getConfig(builderKindTopology + "/" + name)
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

// TestBuilderPublishAgainAfterEdits publishes edits again from each kind of
// draft, and refuses to overwrite a change made to the topology since.
func TestBuilderPublishAgainAfterEdits(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`

	for _, test := range []struct {
		name  string
		start func(*testing.T, *builderHarness) (builderDraftResponse, *bdoc.Document)
	}{
		{
			name: "new diagram",
			start: func(t *testing.T, harness *builderHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
				published, _ := publishBuilderDraft(t, harness, draft,
					`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)

				return published.Draft, document
			},
		},
		{
			name: "imported topology",
			start: func(t *testing.T, harness *builderHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				lab := builderConfig(t, builderKindTopology, "lab")
				lab.Spec = map[string]any{"nodes": []any{includeNode("aa")}}
				harness.configs = append(harness.configs, lab)

				document := generateBuilderDocument(t, harness, "Topology/lab")

				return createBuilderPublishDraft(t, harness, document, "Topology/lab"), document
			},
		},
		{
			name: "published diagram",
			start: func(t *testing.T, harness *builderHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				first := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
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
			harness := newBuilderHarness(t)
			draft, document := test.start(t, harness)

			for _, hostname := range []string{"bb", "cc"} {
				draft = editBuilderDraft(t, harness, draft, document, hostname)
				published, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)

				if len(published.Stages) < 2 || published.Stages[1].Status != "updated" {
					t.Errorf("publishing lab with %s: stages = %+v, want the topology updated", hostname, published.Stages)
				}

				draft = published.Draft
			}

			if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"aa", "bb", "cc"}) {
				t.Errorf("publishing lab twice: nodes = %q, want [aa bb cc]", got)
			}

			// Someone else changes the topology, keeping its annotation.
			for i := range harness.configs {
				if harness.configs[i].FullName() == "Topology/lab" {
					harness.configs[i].Spec = maps.Clone(harness.configs[i].Spec)
					harness.configs[i].Spec["nodes"] = append(slices.Clone(harness.configs[i].Spec["nodes"].([]any)),
						includeNode("theirs"))
				}
			}

			draft = editBuilderDraft(t, harness, draft, document, "dd")
			writes := harness.configWrites

			const changed = "topology lab changed after this draft published it"

			if _, reason := publishBuilderDraft(t, harness, draft, update, http.StatusConflict); reason != changed {
				t.Errorf("publishing lab after their change: refusal = %q, want %q", reason, changed)
			}

			other := harness.createDraft(builderAlice, "other")
			if _, reason := publishBuilderDraft(t, harness, other, update, http.StatusConflict); reason !=
				"topology lab is not the source of this draft" {
				t.Errorf("publishing another draft over lab: refusal = %q, want %q",
					reason, "topology lab is not the source of this draft")
			}

			// A draft opened from the published diagram after their change may not
			// overwrite it either.
			opened := openPublishedBuilderDocument(t, harness, "lab")
			reopened := editBuilderDraft(t, harness,
				createBuilderPublishDraft(t, harness, opened.document, "builder-doc/"+opened.id), opened.document, "ee")

			if _, reason := publishBuilderDraft(t, harness, reopened, update, http.StatusConflict); reason != changed {
				t.Errorf("publishing a draft reopened after their change: refusal = %q, want %q", reason, changed)
			}

			if got := harness.configWrites - writes; got != 0 {
				t.Errorf("refused publications of lab wrote %d configs, want none", got)
			}
		})
	}
}

// TestBuilderPublishAgainAfterTopologyDeleted creates a deleted topology again
// from an imported draft and from one opened from its diagram.
func TestBuilderPublishAgainAfterTopologyDeleted(t *testing.T) {
	const create = `{"mode":"topology","topology":{"name":"range","action":"create"}}`

	harness := newBuilderHarness(t)

	source := builderConfig(t, builderKindTopology, "range")
	source.Spec = map[string]any{"nodes": []any{includeNode("aa")}}
	harness.configs = append(harness.configs, source)

	document := generateBuilderDocument(t, harness, "Topology/range")
	imported := editBuilderDraft(t, harness,
		createBuilderPublishDraft(t, harness, document, "Topology/range"), document, "bb")
	published, _ := publishBuilderDraft(t, harness, imported,
		`{"mode":"topology","topology":{"name":"range","action":"update"}}`, http.StatusOK)

	diagram := openPublishedBuilderDocument(t, harness, "range")
	opened := createBuilderPublishDraft(t, harness, diagram.document, "builder-doc/"+diagram.id)

	for _, test := range []struct {
		name  string
		draft builderDraftResponse
	}{
		{name: "imported draft", draft: published.Draft},
		{name: "draft opened from the diagram", draft: opened},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := openPublishedBuilderDocument(t, harness, "range")

			deleted := harness.do(builderRequest{
				method: http.MethodDelete, path: "/builder/documents/" + current.id, user: builderAlice, ifMatch: current.etag,
			})
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("DELETE /builder/documents/%s: status = %d, want %d: %s",
					current.id, deleted.Code, http.StatusNoContent, deleted.Body)
			}

			again, _ := publishBuilderDraft(t, harness, test.draft, create, http.StatusOK)
			if len(again.Stages) < 2 || again.Stages[1].Status != "created" {
				t.Errorf("publishing range again: stages = %+v, want the topology created", again.Stages)
			}

			if got := topologyHostnames(t, harness, "range"); !slices.Equal(got, []string{"aa", "bb"}) {
				t.Errorf("publishing range again: nodes = %q, want [aa bb]", got)
			}
		})
	}
}

// TestBuilderPublishForkUpdatesWhatItsDraftPublished publishes a fork of a
// draft over what the draft published, but not over a later publication.
func TestBuilderPublishForkUpdatesWhatItsDraftPublished(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`

	// Draft "original" publishes topology lab with node aa, with the body
	// given or the topology alone.
	start := func(t *testing.T, body ...string) (*builderHarness, builderDraftResponse, *bdoc.Document) {
		t.Helper()

		body = append(body, `{"mode":"topology","topology":{"name":"lab","action":"create"}}`)
		harness := newBuilderHarness(t)
		document := bdoc.NewDocument("lab")
		original := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
		published, _ := publishBuilderDraft(t, harness, original, body[0], http.StatusOK)

		return harness, published.Draft, document
	}

	// Forks the draft as user. The fork keeps the draft's source token and
	// records what the draft last published.
	fork := func(
		t *testing.T, harness *builderHarness, original builderDraftResponse, document *bdoc.Document,
	) builderDraftResponse {
		t.Helper()

		recorder := forkBuilderDraft(t, harness, builderAlice, nil, original.ID, document)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("forking %s: status = %d, want %d: %s", original.ID, recorder.Code, http.StatusCreated, recorder.Body)
		}

		var forked builderDraftResponse
		decodeBuilderAnswer(t, recorder, &forked)

		if forked.SourceToken != original.SourceToken {
			t.Errorf("forking %s: source token = %q, want the draft's %q", original.ID, forked.SourceToken, original.SourceToken)
		}

		published := original.Publication
		if published == nil {
			t.Fatalf("draft %s has no publication to fork", original.ID)
		}

		want := &bapi.ForkedPublication{
			DocumentID: published.DocumentID, TopologyTarget: published.TopologyTarget, ExperimentTarget: published.ExperimentTarget,
		}
		if diff := cmp.Diff(want, forked.Forked); diff != "" {
			t.Errorf("forking %s: forked publication mismatch (-want +got):\n%s", original.ID, diff)
		}

		return forked
	}

	// Adds node bb to the fork and publishes it with body, expecting status.
	// Returns the reason for a refusal.
	publishFork := func(
		t *testing.T, harness *builderHarness, forked builderDraftResponse, document *bdoc.Document,
		body string, status int,
	) string {
		t.Helper()

		document.Nodes = append(document.Nodes, bdoc.Node{
			ID: bdoc.DeviceNodeID("bb"), Kind: bdoc.NodeKindDevice, Label: "bb",
			Device: &bdoc.Device{Hostname: "bb", Spec: includeNode("bb"), Interfaces: []bdoc.InterfaceHandle{}},
		})

		data, err := bapi.EncodeDocument(document)
		if err != nil {
			t.Fatalf("EncodeDocument returned error: %v", err)
		}

		path := "/builder/drafts/" + forked.ID
		recorder := harness.do(builderRequest{
			method: http.MethodPost, path: path + "/snapshots", body: `{"summary":"added b","document":` + string(data) + `}`,
			user: forked.CreatedBy, ifMatch: forked.ETag,
		})
		if recorder.Code != http.StatusCreated {
			t.Fatalf("saving the fork's edit: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
		}

		decodeBuilderAnswer(t, recorder, &forked)

		recorder = harness.do(builderRequest{
			method: http.MethodPost, path: path + "/publish", body: body, user: forked.CreatedBy, ifMatch: forked.ETag,
		})
		if recorder.Code != status {
			t.Fatalf("publishing the fork %s: status = %d, want %d: %s", body, recorder.Code, status, recorder.Body)
		}

		return builderRefusal(t, recorder).Message
	}

	t.Run("the topology the draft published", func(t *testing.T) {
		harness, original, document := start(t)

		publishFork(t, harness, fork(t, harness, original, document), document, update, http.StatusOK)

		if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"aa", "bb"}) {
			t.Errorf("publishing the fork: nodes = %q, want [aa bb]", got)
		}
	})

	t.Run("with the experiment the draft published", func(t *testing.T) {
		harness, original, document := start(t, `{"mode":"topology-experiment",`+
			`"topology":{"name":"lab","action":"create"},"experiment":{"name":"exp","action":"create"}}`)

		publishFork(t, harness, fork(t, harness, original, document), document,
			`{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},`+
				`"experiment":{"name":"exp","action":"update"}}`, http.StatusOK)

		if !slices.Equal(harness.reconfigured, []string{"exp"}) {
			t.Errorf("publishing the fork: configured = %q, want [exp]", harness.reconfigured)
		}
	})

	t.Run("published again after the fork", func(t *testing.T) {
		harness, original, document := start(t)
		forked := *document
		forked.Nodes = slices.Clone(document.Nodes)
		draft := fork(t, harness, original, &forked)

		// The original draft publishes again, so lab no longer holds what it
		// published when it was forked.
		edited := editBuilderDraft(t, harness, original, document, "cc")
		publishBuilderDraft(t, harness, edited, update, http.StatusOK)

		writes := harness.configWrites
		if reason := publishFork(t, harness, draft, &forked, update, http.StatusConflict); reason !=
			"topology lab is not the source of this draft" {
			t.Errorf("publishing the fork: refusal = %q, want %q", reason, "topology lab is not the source of this draft")
		}

		if got := harness.configWrites - writes; got != 0 {
			t.Errorf("publishing the refused fork wrote %d configs, want none", got)
		}
	})

	t.Run("refused for a draft that does not exist", func(t *testing.T) {
		harness, _, document := start(t)
		role := builderUserRole()

		recorder := forkBuilderDraft(t, harness, builderBob, role, "id-missing", document)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("fork: status = %d, want %d: %s", recorder.Code, http.StatusNotFound, recorder.Body.String())
		}

		var listing struct {
			Drafts []builderDraftResponse `json:"drafts"`
		}

		harness.as(builderBob, role).doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts"}, http.StatusOK, &listing)

		if slices.ContainsFunc(listing.Drafts, func(draft builderDraftResponse) bool {
			return draft.CreatedBy == builderBob
		}) {
			t.Errorf("GET /builder/drafts as bob = %+v, want none created by the refused fork", listing.Drafts)
		}
	})
}

func TestBuilderPublishAfterThePublishedSnapshotAgedOut(t *testing.T) {
	const update = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
		`"experiment":{"name":"exp","action":"update"}}`

	// Publishes lab and exp, saves once and deletes the published snapshot, as
	// TestPruningKeepsAnAgedOutPublication in api/builder ages it out.
	aged := func(t *testing.T) (*builderHarness, builderDraftResponse, *bdoc.Document) {
		t.Helper()

		harness := newBuilderHarness(t)
		document := bdoc.NewDocument("lab")
		draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
		response, _ := publishBuilderDraft(t, harness, draft, `{"mode":"topology-experiment",`+
			`"topology":{"name":"lab","action":"create"},"experiment":{"name":"exp","action":"create"}}`, http.StatusOK)
		published := response.Draft.Publication
		if published == nil {
			t.Fatalf("publishing lab and exp: draft publication = nil, want one")
		}

		draft = response.Draft

		draft = editBuilderDraft(t, harness, draft, document, "bb")

		deleted := harness.do(builderRequest{
			method: http.MethodDelete, user: builderAlice, ifMatch: draft.ETag,
			path: "/builder/drafts/" + draft.ID + "/snapshots/" + published.SnapshotID,
		})
		if deleted.Code != http.StatusOK {
			t.Fatalf("deleting the published snapshot: status = %d, want %d: %s", deleted.Code, http.StatusOK, deleted.Body)
		}

		decodeBuilderAnswer(t, deleted, &draft)

		if draft.Snapshots != 2 {
			t.Errorf("deleting the published snapshot: snapshots = %d, want 2", draft.Snapshots)
		}

		if draft.Publication == nil || draft.Publication.SnapshotID != published.SnapshotID ||
			draft.Publication.DocumentID != published.DocumentID {
			t.Errorf("deleting the published snapshot: publication = %+v, want it kept as %+v", draft.Publication, published)
		}

		if !draft.Dirty {
			t.Error("deleting the published snapshot: dirty = false, want true")
		}

		recorder := harness.do(builderRequest{
			method: http.MethodGet, user: builderAlice,
			path: "/builder/drafts/" + draft.ID + "/snapshots/" + published.SnapshotID,
		})
		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET the published snapshot %s: status = %d, want %d: %s",
				published.SnapshotID, recorder.Code, http.StatusNotFound, recorder.Body)
		}

		return harness, draft, document
	}

	t.Run("by the draft", func(t *testing.T) {
		harness, draft, _ := aged(t)

		response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)

		if !slices.Equal(harness.reconfigured, []string{"exp"}) {
			t.Errorf("publishing lab and exp: configured = %q, want [exp]", harness.reconfigured)
		}

		if response.Draft.Dirty {
			t.Error("publishing lab and exp: dirty = true, want false")
		}

		if published := response.Draft.Publication; published == nil || published.SnapshotID != response.Draft.SnapshotID {
			t.Errorf("publishing lab and exp: publication = %+v, want the current snapshot %s published",
				published, response.Draft.SnapshotID)
		}
	})

	t.Run("by a draft that forks it", func(t *testing.T) {
		harness, draft, document := aged(t)

		recorder := forkBuilderDraft(t, harness, builderAlice, nil, draft.ID, document)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("forking %s: status = %d, want %d: %s", draft.ID, recorder.Code, http.StatusCreated, recorder.Body)
		}

		var forked builderDraftResponse
		decodeBuilderAnswer(t, recorder, &forked)

		if forked.Forked == nil || draft.Publication == nil || forked.Forked.DocumentID != draft.Publication.DocumentID {
			t.Errorf("forking %s: forked publication = %+v, want the draft's %+v", draft.ID, forked.Forked, draft.Publication)
		}

		publishBuilderDraft(t, harness, editBuilderDraft(t, harness, forked, document, "cc"), update, http.StatusOK)

		if !slices.Equal(harness.reconfigured, []string{"exp"}) {
			t.Errorf("publishing the fork: configured = %q, want [exp]", harness.reconfigured)
		}
	})
}

// labExperimentFixture returns topology "lab" with node a, and experiment
// "exp" built from it.
func labExperimentFixture(t *testing.T) []store.Config {
	t.Helper()

	lab := builderConfig(t, builderKindTopology, "lab")
	lab.Spec = map[string]any{"nodes": []any{includeNode("aa")}}

	experiment := builderConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{includeNode("aa")}},
		"vlans":    map[string]any{"aliases": map[string]any{}},
	}
	experiment.Metadata.Annotations = store.Annotations{"topology": "lab"}

	return []store.Config{lab, experiment}
}

// TestBuilderPublishExperimentAgainAfterEdits publishes a topology and an
// experiment again. Each update runs configure, and a failed one is retried.
func TestBuilderPublishExperimentAgainAfterEdits(t *testing.T) {
	const update = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
		`"experiment":{"name":"exp","action":"update"}}`

	for _, test := range []struct {
		name  string
		start func(*testing.T, *builderHarness) (builderDraftResponse, *bdoc.Document)
	}{
		{
			name: "imported experiment",
			start: func(t *testing.T, harness *builderHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				harness.configs = append(harness.configs, labExperimentFixture(t)...)
				document := generateBuilderDocument(t, harness, "Experiment/exp")

				return createBuilderPublishDraft(t, harness, document, "Experiment/exp"), document
			},
		},
		{
			name: "new diagram",
			start: func(t *testing.T, harness *builderHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
				published, _ := publishBuilderDraft(t, harness, draft,
					`{"mode":"topology-experiment","topology":{"name":"lab","action":"create"},`+
						`"experiment":{"name":"exp","action":"create"}}`, http.StatusOK)

				if harness.experimentWrites != 1 || len(harness.reconfigured) != 0 {
					t.Errorf("creating exp: %d experiments created and %q configured, want 1 created, which configures it",
						harness.experimentWrites, harness.reconfigured)
				}

				return published.Draft, document
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderHarness(t)
			draft, document := test.start(t, harness)

			// The configure stage changes the experiment's spec, as apps do, so
			// publishing again compares the experiment with its digest after it.
			harness.configuring = func(name string) {
				setExperimentSpec(harness, name, "schedules", map[string]any{"aa": fmt.Sprintf("host%d", len(harness.reconfigured))})
			}

			draft = editBuilderDraft(t, harness, draft, document, "bb")
			before, err := harness.getConfig("Experiment/exp")
			if err != nil {
				t.Fatalf("experiment missing: %v", err)
			}

			harness.failReconfigure = true
			failed, _ := publishBuilderDraft(t, harness, draft, update, http.StatusInternalServerError)
			harness.failReconfigure = false

			if failed.Status != bapi.PublishPartial {
				t.Errorf("publishing with configure failing: status = %q, want %q", failed.Status, bapi.PublishPartial)
			}

			if len(failed.Stages) == 0 {
				t.Fatalf("publishing with configure failing: stages = none, want the experiment stage failed last")
			}

			if last := failed.Stages[len(failed.Stages)-1]; last.Name != builderPublishStageExperiment ||
				last.Status != bapi.PublishFailed {
				t.Errorf("publishing with configure failing: last stage = %+v, want the experiment stage failed", last)
			}

			after, err := harness.getConfig("Experiment/exp")
			if err != nil {
				t.Fatalf("experiment missing: %v", err)
			}

			if diff := cmp.Diff(before.Spec, after.Spec); diff != "" {
				t.Errorf("publishing with configure failing changed the experiment spec (-want +got):\n%s", diff)
			}

			for _, hostname := range []string{"", "cc"} {
				if hostname != "" {
					draft = editBuilderDraft(t, harness, draft, document, hostname)
				}

				published, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)
				if len(published.Stages) < 2 {
					t.Errorf("publishing lab and exp with %q: stages = %+v, want the experiment updated", hostname, published.Stages)
				} else if last := published.Stages[len(published.Stages)-2]; last.Name != builderPublishStageExperiment ||
					last.Status != "updated" {
					t.Errorf("publishing lab and exp with %q: stages = %+v, want the experiment updated", hostname, published.Stages)
				}

				draft = published.Draft
			}

			if !slices.Equal(harness.reconfigured, []string{"exp", "exp"}) {
				t.Errorf("publishing lab and exp twice: configured = %q, want exp after each update", harness.reconfigured)
			}

			if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"aa", "bb", "cc"}) {
				t.Errorf("publishing lab and exp twice: nodes = %q, want [aa bb cc]", got)
			}
		})
	}
}

// TestBuilderPublishRepairsDocument repairs a damaged document copy: the
// metadata and the new content are stored together, so nothing is left to
// clean up and nothing warns.
func TestBuilderPublishRepairsDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("rep")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
	published, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"rep","action":"create"}}`, http.StatusOK)

	// The content of the stored document goes missing.
	for _, key := range harness.store.Keys(bapi.NamespaceContent) {
		if strings.HasPrefix(key, "published/") {
			harness.store.Drop(bapi.NamespaceContent, key)

			break
		}
	}

	var response builderPublishResponse

	recorder := harness.doDecode(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + published.Draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"rep","action":"update"}}`,
		user:   builderAlice, ifMatch: published.Draft.ETag,
	}, http.StatusOK, &response)

	if warning := builderWarning(recorder); warning != "" {
		t.Errorf("republishing rep: Warning header = %q, want none", warning)
	}

	if len(response.Warnings) != 0 {
		t.Errorf("republishing rep: warnings = %+v, want none", response.Warnings)
	}

	topology, err := harness.getConfig("Topology/rep")
	if err != nil {
		t.Fatalf("topology missing: %v", err)
	}

	ref, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("DecodeReference returned error: %v", err)
	}

	if _, _, err := harness.service.GetPublishedDocumentData(context.Background(), ref.StoredID("rep")); err != nil {
		t.Errorf("GetPublishedDocumentData(the repaired document of rep) returned error: %v", err)
	}
}

// setExperimentSpec changes one field of a stored experiment's spec, as
// someone else, or an app, does.
func setExperimentSpec(harness *builderHarness, name, key string, value any) {
	for i := range harness.configs {
		if harness.configs[i].FullName() == "Experiment/"+name {
			harness.configs[i].Spec = maps.Clone(harness.configs[i].Spec)
			harness.configs[i].Spec[key] = value
		}
	}
}

// TestBuilderPublishExperimentRefusesOthersChanges refuses to update an
// experiment this draft did not publish, or one changed since.
func TestBuilderPublishExperimentRefusesOthersChanges(t *testing.T) {
	const (
		topologyUpdate = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`
		bothUpdate     = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
			`"experiment":{"name":"%s","action":"update"}}`
	)

	for _, test := range []struct {
		name, experiment, reason string
		start                    func(*testing.T, *builderHarness) (builderDraftResponse, *bdoc.Document)
	}{
		{
			// It published only the topology of the experiment it was imported
			// from, which someone has changed since the import.
			name: "imported experiment", experiment: "exp",
			reason: "this diagram comes from experiment exp. That experiment changed on the server " +
				"after Builder made the diagram. Publish refuses this diagram under any name. To continue, " +
				"make a new draft of experiment exp: import it again. Then copy your changes to the new draft",
			start: func(t *testing.T, harness *builderHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				harness.configs = append(harness.configs, labExperimentFixture(t)...)
				document := generateBuilderDocument(t, harness, "Experiment/exp")
				draft := editBuilderDraft(t, harness,
					createBuilderPublishDraft(t, harness, document, "Experiment/exp"), document, "bb")
				published, _ := publishBuilderDraft(t, harness, draft, topologyUpdate, http.StatusOK)

				return published.Draft, document
			},
		},
		{
			// Someone else built an experiment from the topology it published.
			name: "experiment of its topology", experiment: "other",
			reason: "experiment other is not the source of this draft",
			start: func(t *testing.T, harness *builderHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
				published, _ := publishBuilderDraft(t, harness, draft,
					`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)

				other := builderConfig(t, kindExperiment, "other")
				other.Spec = map[string]any{
					"topology": map[string]any{"nodes": []any{includeNode("aa")}},
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
			start: func(t *testing.T, harness *builderHarness) (builderDraftResponse, *bdoc.Document) {
				t.Helper()

				document := bdoc.NewDocument("lab")
				draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
				published, _ := publishBuilderDraft(t, harness, draft,
					`{"mode":"topology-experiment","topology":{"name":"lab","action":"create"},`+
						`"experiment":{"name":"exp","action":"create"}}`, http.StatusOK)

				return published.Draft, document
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderHarness(t)
			draft, document := test.start(t, harness)

			setExperimentSpec(harness, test.experiment, "schedules", map[string]any{"aa": "theirs"})
			draft = editBuilderDraft(t, harness, draft, document, "cc")
			writes := harness.configWrites

			if _, reason := publishBuilderDraft(t, harness, draft,
				fmt.Sprintf(bothUpdate, test.experiment), http.StatusConflict); reason != test.reason {
				t.Errorf("updating %s: refusal = %q, want %q", test.experiment, reason, test.reason)
			}

			if harness.configWrites != writes || len(harness.reconfigured) != 0 {
				t.Errorf("updating %s wrote %d configs and configured %q, want none",
					test.experiment, harness.configWrites-writes, harness.reconfigured)
			}

			exp, err := harness.getConfig("Experiment/" + test.experiment)
			if err != nil {
				t.Fatalf("experiment %s missing: %v", test.experiment, err)
			}

			if diff := cmp.Diff(any(map[string]any{"aa": "theirs"}), exp.Spec["schedules"]); diff != "" {
				t.Errorf("updating %s changed their schedules (-want +got):\n%s", test.experiment, diff)
			}
		})
	}
}

// TestBuilderPublishKeepsExperimentStartedWhileConfiguring leaves an experiment
// the CLI started during a failed configure stage as it is.
func TestBuilderPublishKeepsExperimentStartedWhileConfiguring(t *testing.T) {
	harness := newBuilderHarness(t, labExperimentFixture(t)...)
	document := generateBuilderDocument(t, harness, "Experiment/exp")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document, "Experiment/exp"), document, "bb")

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

	if diff := cmp.Diff(started, exp.Status); diff != "" {
		t.Errorf("publishing exp with configure failing: status mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderPublishExperimentUpdateRereadsUnderLock reads the experiment again
// under its lock: a started one is refused, and other changes are kept.
func TestBuilderPublishExperimentUpdateRereadsUnderLock(t *testing.T) {
	const update = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
		`"experiment":{"name":"exp","action":"update"}}`

	harness := newBuilderHarness(t, labExperimentFixture(t)...)
	document := generateBuilderDocument(t, harness, "Experiment/exp")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document, "Experiment/exp"), document, "bb")

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

	if running.Status != bapi.PublishPartial {
		t.Errorf("publishing to a started exp: status = %q, want %q", running.Status, bapi.PublishPartial)
	}

	if len(running.Stages) == 0 {
		t.Fatalf("publishing to a started exp: stages = none, want the experiment stage failed last")
	}

	want := builderPublishStage{
		Name: builderPublishStageExperiment, Status: bapi.PublishFailed,
		Message: "experiment publication failed: phenix cannot update a running experiment",
	}
	if diff := cmp.Diff(want, running.Stages[len(running.Stages)-1],
		cmpopts.IgnoreFields(builderPublishStage{}, "Config")); diff != "" {
		t.Errorf("publishing to a started exp: last stage mismatch (-want +got):\n%s", diff)
	}

	if len(harness.reconfigured) != 0 {
		t.Errorf("publishing to a started exp: configured = %q, want none", harness.reconfigured)
	}

	// Stopped again, it is changed once more after preflight.
	change(nil, "")("exp")
	harness.lockedExperiment = change(map[string]any{"apps": map[string]any{"kept": true}}, "no-headnode")
	publishBuilderDraft(t, harness, draft, update, http.StatusOK)

	updated, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("experiment missing: %v", err)
	}

	if diff := cmp.Diff(map[string]any{"apps": map[string]any{"kept": true}}, updated.Status); diff != "" {
		t.Errorf("publishing exp: status set under the lock mismatch (-want +got):\n%s", diff)
	}

	if got := updated.Spec["deployMode"]; got != "no-headnode" {
		t.Errorf("publishing exp: deployMode = %v, want the no-headnode set under the lock", got)
	}

	topology, _ := updated.Spec["topology"].(map[string]any)
	if nodes, _ := topology["nodes"].([]any); len(nodes) != 2 {
		t.Errorf("publishing exp: topology = %v, want nodes aa and bb", topology)
	}
}

// TestBuilderPublishRefusesExperimentNamesBeforeWriting refuses, before any
// write, the name "all" and, in auto bridge mode, a name too long for a bridge.
func TestBuilderPublishRefusesExperimentNamesBeforeWriting(t *testing.T) {
	mode := string(common.BridgeMode)
	t.Cleanup(func() { _ = common.SetBridgeMode(mode) })

	setBridgeMode := func(t *testing.T, mode common.BridgingMode) {
		t.Helper()

		if err := common.SetBridgeMode(string(mode)); err != nil {
			t.Fatalf("SetBridgeMode(%q) returned error: %v", mode, err)
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
			setBridgeMode(t, test.bridge)
			harness := newBuilderHarness(t)
			draft := harness.createDraft(builderAlice, "names")

			_, reason := publishBuilderDraft(t, harness, draft,
				`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
					`"experiment":{"name":"`+test.experiment+`","action":"create"}}`, http.StatusUnprocessableEntity)
			if !strings.HasPrefix(reason, test.want) {
				t.Errorf("publishing experiment %s: refusal = %q, want it to start with %q", test.experiment, reason, test.want)
			}

			if writes, documents := harness.configWrites, harness.store.Count(bapi.NamespacePublished); writes != 0 || documents != 0 {
				t.Errorf("publishing experiment %s wrote %d configs and %d published documents, want none",
					test.experiment, writes, documents)
			}
		})
	}

	// Fifteen characters are allowed.
	setBridgeMode(t, common.BridgeModeAuto)
	harness := newBuilderHarness(t)
	publishBuilderDraft(t, harness, harness.createDraft(builderAlice, "names"),
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
			`"experiment":{"name":"fifteen-chars-x","action":"create"}}`, http.StatusOK)
}

// TestIncludeClashRefusal checks the refusal for each number of include
// clashes: none, one, a few, and more than it lists by name.
func TestIncludeClashRefusal(t *testing.T) {
	clash := func(hostname, include string) bdoc.HostnameClash {
		return bdoc.HostnameClash{Include: include, Hostname: hostname}
	}

	const rename = ". phenix rejects duplicate hostnames. Rename them here or in those topologies"

	tests := []struct {
		name    string
		clashes []bdoc.HostnameClash
		want    string
	}{
		{name: "none"},
		{
			name:    "one",
			clashes: []bdoc.HostnameClash{clash("web", "shared")},
			want: "phenix cannot publish topology root: node web is in this topology and in its included topology shared. " +
				"phenix rejects duplicate hostnames. Rename the node here or in shared",
		},
		{
			name:    "two",
			clashes: []bdoc.HostnameClash{clash("web", "shared"), clash("db", "other")},
			want: "phenix cannot publish topology root: nodes web (in shared) and db (in other) are in this topology " +
				"and in its included topologies" + rename,
		},
		{
			name: "three",
			clashes: []bdoc.HostnameClash{
				clash("web", "shared"), clash("db", "other"), clash("dns", "shared"),
			},
			want: "phenix cannot publish topology root: nodes web (in shared), db (in other) and dns (in shared) " +
				"are in this topology and in its included topologies" + rename,
		},
		{
			name: "five",
			clashes: []bdoc.HostnameClash{
				clash("web", "shared"), clash("db", "other"), clash("dns", "shared"),
				clash("ntp", "shared"), clash("log", "other"),
			},
			want: "phenix cannot publish topology root: nodes web (in shared), db (in other), dns (in shared) and " +
				"2 more are in this topology and in its included topologies" + rename,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := includeClashRefusal("root", bdoc.IncludeReport{Clashes: tt.clashes})

			if tt.want == "" {
				if err != nil {
					t.Fatalf("includeClashRefusal(root, %d clashes) = %v, want nil", len(tt.clashes), err)
				}

				return
			}

			var werr *weberror.WebError
			if !errors.As(err, &werr) {
				t.Fatalf("includeClashRefusal(root, %d clashes) = %v, want a *weberror.WebError", len(tt.clashes), err)
			}

			got := []any{werr.Status, werr.Code, werr.Message}
			want := []any{http.StatusConflict, string(bdoc.CodePublishIncludeClash), tt.want}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("includeClashRefusal(root, %d clashes) mismatch (-want +got):\n%s", len(tt.clashes), diff)
			}
		})
	}
}

// TestBuilderPublishOpsBroadcast asserts the production publish operations
// broadcast a stored config and a stored experiment that runs no VMs, and
// report an experiment that the store does not hold.
func TestBuilderPublishOpsBroadcast(t *testing.T) {
	storetest.UseBolt(t)

	experiment := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     kindExperiment,
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec:     map[string]any{"experimentName": "exp", "topology": map[string]any{"nodes": []any{}}},
	}
	if err := store.Create(&experiment); err != nil {
		t.Fatalf("store.Create(Experiment/exp) returned error: %v", err)
	}

	ops := newBuilderPublishOps()

	if err := ops.broadcastConfig(&experiment, "create"); err != nil {
		t.Errorf("broadcastConfig(Experiment/exp, create) = %v, want nil", err)
	}

	if err := ops.broadcastExperiment("exp", "update"); err != nil {
		t.Errorf("broadcastExperiment(exp, update) = %v, want nil", err)
	}

	if err := ops.broadcastExperiment("missing", "update"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("broadcastExperiment(missing, update) = %v, want store.ErrNotExist", err)
	}
}
