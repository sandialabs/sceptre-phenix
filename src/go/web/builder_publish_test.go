package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
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
	harness *builderHarness,
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

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   string(body),
		user:   builderTestOwner,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("creating publish draft: status %d: %s", recorder.Code, recorder.Body.String())
	}

	var draft builderDraftResponse
	harness.decode(recorder, &draft)

	return draft
}

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
	if updated.Metadata.Annotations["keep"] != "value" {
		t.Fatal("unrelated annotation was not preserved")
	}
	if _, err := bapi.DecodeReference(updated.Metadata.Annotations[bapi.DocumentAnnotation]); err != nil {
		t.Fatalf("builder document annotation invalid: %v", err)
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
	if refusal != "topology legacy is not the source this draft was loaded from" {
		t.Fatalf("refusal = %q, want the draft refused as not loaded from the topology", refusal)
	}

	if harness.configs[0].Metadata.Annotations[bdoc.LegacyXMLAnnotation] != "<mxfile/>" ||
		harness.configs[0].HasAnnotation(bapi.DocumentAnnotation) {
		t.Fatalf("annotations = %v, want the legacy diagram untouched", harness.configs[0].Metadata.Annotations)
	}

	role := builderRole(builderPolicy(
		[]string{"configs"},
		[]string{"*"},
		[]string{"update"},
	))
	publishBuilderDraftAs(t, harness, draft, &role,
		`{"mode":"topology","topology":{"name":"new","action":"create"}}`, http.StatusForbidden)
	if harness.configWrites != 0 {
		t.Fatalf("unauthorized publish wrote %d configs", harness.configWrites)
	}
}

func TestBuilderPublishSharedDraftRecordsActor(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderTestPeer, "shared")

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"shared","action":"create"}}`, http.StatusOK)

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}
	if meta.LastModifiedBy != builderTestOwner ||
		meta.Publication == nil ||
		meta.Publication.PublishedBy != builderTestOwner {
		t.Fatalf("publication audit = %#v", meta)
	}
}

func TestBuilderPublishReportsBroadcastWarning(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.failBroadcastKind = builderKindTopology
	draft := harness.createDraft(builderTestOwner, "broadcast")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"broadcast","action":"create"}}`, http.StatusOK)

	if !strings.Contains(strings.Join(bdoc.IssueMessages(response.Warnings), " "), "broadcast") ||
		!slices.ContainsFunc(response.Warnings, func(warning bdoc.Issue) bool {
			return warning.Code == bdoc.CodePublishBroadcastFailed && warning.Severity == bdoc.SeverityWarning
		}) {
		t.Fatalf("warnings = %#v, want a broadcast warning", response.Warnings)
	}
	if response.Draft.Publication == nil {
		t.Fatal("broadcast failure prevented draft publication state")
	}
}

func TestBuilderPublishReportsPartialFailure(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderTestOwner, "partial")
	harness.failConfigKind = builderKindTopology

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"partial","action":"create"}}`,
		user:   builderTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
	}

	var response builderPublishResponse
	harness.decode(recorder, &response)
	if response.Status != "partial" || len(response.Stages) != 2 ||
		len(response.Errors) != 1 || response.Errors[0].Code != bdoc.CodePublishStageFailed ||
		response.Errors[0].Message != response.Stages[1].Message {
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

	// A config write etcd refused for lack of space says so plainly.
	harness.failConfigErr = fmt.Errorf(
		"writing config JSON to Etcd: %w: %w",
		store.ErrNoSpace, errors.New("etcdserver: mvcc: database space exceeded"),
	)

	recorder = harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"partial","action":"create"}}`,
		user:   builderTestOwner, ifMatch: meta.ETag(),
	})
	if recorder.Code != http.StatusInsufficientStorage {
		t.Fatalf("out of space: status = %d, want %d: %s", recorder.Code, http.StatusInsufficientStorage, recorder.Body)
	}

	var refused builderPublishResponse
	harness.decode(recorder, &refused)
	if want := "topology publication failed: " + store.ErrNoSpace.Error(); len(refused.Errors) != 1 ||
		refused.Errors[0].Message != want || refused.Errors[0].Code != bdoc.CodeServerStorageFull {
		t.Fatalf("out of space: errors = %+v, want %q", refused.Errors, want)
	}
}

func TestBuilderPublishReportsExperimentPartialFailure(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.failExperiment = true
	draft := harness.createDraft(builderTestOwner, "experiment-partial")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"partial-topology","action":"create"},`+
			`"experiment":{"name":"partial-experiment","action":"create"}}`, http.StatusInternalServerError)

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

func TestBuilderPublishValidatesIntentBeforeWriting(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderTestOwner, "invalid-intent")

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
		// A scenario is named only for the experiment of a
		// topology-and-experiment publication, by its name alone.
		`{"mode":"topology","topology":{"name":"topo","action":"create"},"scenario":{"name":"sc"}}`,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
			`"scenario":{"name":"sc","action":"use"},"experiment":{"name":"exp","action":"create"}}`,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
			`"scenario":{"name":"my sc"},"experiment":{"name":"exp","action":"create"}}`,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
			`"scenario":{"name":""},"experiment":{"name":"exp","action":"create"}}`,
		`{"mode":"topology","topology":{"name":"topo","action":"update","expectedDigest":"sha256:` +
			strings.Repeat("0", 64) + `"}}`,
	}

	for _, body := range tests {
		publishBuilderDraft(t, harness, draft, body, http.StatusBadRequest)
	}

	if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
		t.Fatal("invalid publication intent had side effects")
	}
}

// TestBuilderPublishNamesInterfacesWithoutVLAN refuses interfaces with no
// VLAN, and names them in the message, which is what the Publish dialog shows,
// not only in the cause: the first few, by position where the name does not
// tell them apart, and then how many more there are.
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

	if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
		t.Fatal("a refused publication had side effects")
	}
}

// TestBuilderPublishNamesSharedAddresses refuses interfaces that use one
// IP or MAC address, however each is written, and names the first few
// addresses and their interfaces in the message. The draft holding them saves.
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

	want := `topology shared cannot be published: ` +
		`IP address 10.0.0.1 on VLAN "EXP" is used by ` + users("eth0") + `; ` +
		`MAC address 00:00:00:00:00:01 on VLAN "EXP" is used by ` + users("eth0") + `; ` +
		`IP address 10.0.0.2 on VLAN "EXP" is used by ` + users("eth1") + `; ` +
		`1 more address is used more than once`
	if body.Message != want {
		t.Fatalf("message = %q, want %q", body.Message, want)
	}

	// The cause still lists every one.
	if !strings.Contains(body.Cause, `MAC address 00:00:00:00:00:02 on VLAN "EXP" is used by `+users("eth1")) {
		t.Fatalf("cause %q does not name the second MAC address", body.Cause)
	}

	if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
		t.Fatal("a refused publication had side effects")
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

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"generated","action":"create"}}`, http.StatusConflict)
	if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
		t.Fatal("stale source publication had side effects")
	}
}

// TestBuilderPublishAnnotatesListedScenarios publishes a draft listing four
// scenarios, in either mode: the topology is added to the "topology"
// annotation of each that does not name it exactly (a name that only
// contains it does not count), after the names it has, which are kept as
// they are, and one that already names it is not written. The scenario
// stage says which it changed.
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
					t.Fatalf("scenario %s missing: %v", name, err)
				}

				if got := scenario.Metadata.Annotations["topology"]; got != want {
					t.Errorf("scenario %s topology annotation = %q, want %q", name, got, want)
				}

				if scenario.Metadata.Annotations["keep"] != "yes" {
					t.Errorf("scenario %s lost its other annotations: %v", name, scenario.Metadata.Annotations)
				}
			}

			// The topology and the three scenarios that changed.
			if harness.configWrites != 4 {
				t.Fatalf("config writes = %d, want 4", harness.configWrites)
			}

			stage := builderStageNamed(t, response, builderPublishStageScenario)
			want := builderPublishStage{
				Name: builderPublishStageScenario, Status: "updated", Config: "",
				Message: "added topology topo to scenarios sc-a, sc-b, sc-d; scenario sc-c already names it",
			}
			if stage != want {
				t.Fatalf("scenario stage = %+v, want %+v", stage, want)
			}

			if response.Scenario != nil {
				t.Fatalf("response scenario = %+v, want none: no experiment scenario was picked", response.Scenario)
			}
		})
	}
}

// TestBuilderPublishScenarioStageReports checks the scenario stage of a
// draft that lists one scenario, which it names as its config, and of one
// whose scenarios all name the topology already, which it skips.
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

			if stage := builderStageNamed(t, response, builderPublishStageScenario); stage != test.want {
				t.Fatalf("scenario stage = %+v, want %+v", stage, test.want)
			}
		})
	}

	// A draft that lists no scenario has no scenario stage.
	harness := newBuilderHarness(t, namedScenario(t, "sc", ""))
	draft := harness.createDraft(builderTestOwner, "plain")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"plain","action":"create"}}`, http.StatusOK)

	for _, stage := range response.Stages {
		if stage.Name == builderPublishStageScenario {
			t.Fatalf("stages = %+v, want no scenario stage", response.Stages)
		}
	}

	if scenario, err := harness.getConfig("Scenario/sc"); err != nil || scenario.Metadata.Annotations["topology"] != "" {
		t.Fatalf("an unlisted scenario was changed: %+v (%v)", scenario, err)
	}
}

// TestBuilderPublishRefusesScenarioProblems refuses, before anything is
// written, a listed scenario that does not exist or that the caller may not
// read (alike, so its existence is not disclosed), one the caller may not
// update whose annotation must change, and an experiment scenario the draft
// does not list.
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
			listed: []string{"sc", "secret"}, role: &hidden, body: fmt.Sprintf(experiment, "sc"),
			status: http.StatusUnprocessableEntity, want: "scenario secret does not exist",
		},
		"not updatable": {
			listed: []string{"sc"}, role: &readOnly,
			body:   `{"mode":"topology","topology":{"name":"topo","action":"create"}}`,
			status: http.StatusForbidden, want: "adding topology topo to scenario sc not allowed",
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
				t.Fatalf("refusal = %q, want %q", refusal.Message, test.want)
			}

			if harness.configWrites != 0 || harness.experimentWrites != 0 ||
				harness.store.Count(bapi.NamespacePublished) != 0 {
				t.Fatal("a refused publication had side effects")
			}
		})
	}

	// A scenario that names the topology already needs no update permission.
	harness := newBuilderHarness(t, namedScenario(t, "sc", "topo"))

	document := bdoc.NewDocument("annotated")
	document.Scenarios = []string{"sc"}
	draft := createBuilderPublishDraft(t, harness, document)

	publishBuilderDraftAs(t, harness, draft, &readOnly,
		`{"mode":"topology","topology":{"name":"topo","action":"create"}}`, http.StatusOK)
}

// namedScenario returns a Scenario config named name with one app, the
// comma-separated topology annotation topologies (left out when empty) and
// another annotation, keep.
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
	draft := harness.createDraft(builderTestOwner, "unrelated")

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"new-topology","action":"create"},`+
			`"experiment":{"name":"existing-exp","action":"update"}}`, http.StatusConflict)
	if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
		t.Fatal("unrelated experiment update had side effects")
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
	if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
		t.Fatal("running experiment update had side effects")
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

// TestBuilderPublishExperimentUsesPickedScenario publishes a draft listing
// two scenarios with an experiment created with the second: the experiment
// is created with that scenario, after the topology was added to both.
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

	if created.Metadata.Annotations["scenario"] != "sc-b" || harness.experimentWrites != 1 {
		t.Fatalf("experiment annotations = %v after %d writes, want scenario sc-b",
			created.Metadata.Annotations, harness.experimentWrites)
	}

	if response.Scenario == nil || response.Scenario.Name != "sc-b" {
		t.Fatalf("response scenario = %+v, want sc-b", response.Scenario)
	}

	meta, err := harness.service.GetDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if meta.Publication == nil || meta.Publication.ScenarioTarget != "sc-b" {
		t.Fatalf("publication = %+v, want scenario target sc-b", meta.Publication)
	}

	// The stages run in order: the scenarios are annotated before the
	// experiment, which phenix creates only with a scenario that names its
	// topology.
	names := make([]string, len(response.Stages))
	for i, stage := range response.Stages {
		names[i] = stage.Name
	}

	if want := []string{"document", "topology", "scenario", "experiment", "draft"}; !slices.Equal(names, want) {
		t.Fatalf("stages = %q, want %q", names, want)
	}
}

// TestBuilderExperimentScenarioRoundTrip generates a draft from an
// experiment whose scenario is stored, which lists it, then publishes it
// back with that scenario and then with none. The experiment holds the
// stored scenario, as phenix merges it, and then none.
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

	if !reflect.DeepEqual(document.Scenarios, []string{"sc"}) {
		t.Fatalf("scenarios = %q with warnings %q, want [sc]", document.Scenarios, warnings)
	}

	// A device added in the editor, so the update has a topology to write:
	// an experiment that already holds the topology and names the scenario
	// is left as it is.
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

	if updated.Metadata.Annotations["scenario"] != "sc" {
		t.Fatalf("experiment annotations = %#v", updated.Metadata.Annotations)
	}

	if apps := experimentAppNames(t, updated); !slices.Equal(apps, []string{"app"}) {
		t.Fatalf("experiment apps = %q, want the stored scenario's", apps)
	}

	if scenario, err := harness.getConfig("Scenario/sc"); err != nil || scenario.Metadata.Annotations["topology"] != "topo" {
		t.Fatalf("scenario = %+v (%v), want topology topo added", scenario, err)
	}

	// Published again without a scenario, the experiment has none.
	publishBuilderDraft(t, harness, response.Draft,
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"update"},`+
			`"experiment":{"name":"exp","action":"update"}}`, http.StatusOK)

	cleared, err := harness.getConfig("Experiment/exp")
	if err != nil {
		t.Fatalf("experiment missing: %v", err)
	}

	if _, ok := cleared.Metadata.Annotations["scenario"]; ok || len(experimentAppNames(t, cleared)) != 0 {
		t.Fatalf("experiment = %v with apps %q, want no scenario", cleared.Metadata.Annotations, experimentAppNames(t, cleared))
	}
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

// TestBuilderGenerateExperimentScenario imports experiments: one whose
// scenario is a Scenario config the caller may list lists it; one whose
// scenario is missing or hidden from the caller lists none, with a warning,
// and holds nothing of the scenario content the experiment carries. An
// experiment file is held to the same rule, and a warning says that the
// scenario listed is this server's, not the file's copy.
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

	upload := asBuilderJSON(t, map[string]string{"content": string(content)})

	hidden := builderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get", "create"}),
		builderPolicy([]string{"experiments"}, []string{"*"}, []string{"list"}),
	)

	const notStored = `the experiment's scenario "sc" is not a stored Scenario config and was not attached`

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
			configs: []store.Config{experiment, *stored}, role: &hidden, body: `{"source":"Experiment/exp"}`,
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
				t.Fatalf("scenarios = %q, want %q", document.Scenarios, test.listed)
			}

			if test.warning == "" && slices.ContainsFunc(warnings, func(warning string) bool {
				return strings.Contains(warning, "scenario")
			}) {
				t.Fatalf("warnings = %q, want none about the scenario", warnings)
			}

			if test.warning != "" && !slices.Contains(warnings, test.warning) {
				t.Fatalf("warnings = %q, want %q", warnings, test.warning)
			}

			// The document leaves out warnings it has none of.
			if !slices.Equal(document.Source.Warnings, warnings) {
				t.Fatalf("source warnings = %q, want %q", document.Source.Warnings, warnings)
			}

			data, err := bapi.EncodeDocument(document)
			if err != nil {
				t.Fatalf("EncodeDocument returned error: %v", err)
			}

			if strings.Contains(string(data), "embedded-app") {
				t.Fatalf("the document holds the experiment's scenario content:\n%s", data)
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

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/generate", body: body,
		user: builderTestOwner, role: role,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("generate status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var response builderGenerateResponse
	harness.decode(recorder, &response)

	document, err := bdoc.Parse(response.Document)
	if err != nil {
		t.Fatalf("generated document: %v", err)
	}

	return document, response.Warnings
}

// TestBuilderPublishResumesAfterScenarioFailure fails the scenario stage,
// which leaves the topology written and the draft unpublished, then
// publishes again, which skips the topology, annotates the scenarios and
// creates the experiment. A retry of the publication that completed answers
// with every stage skipped, the scenario stage among them.
func TestBuilderPublishResumesAfterScenarioFailure(t *testing.T) {
	harness := newBuilderHarness(t, namedScenario(t, "sc-a", ""), namedScenario(t, "sc-b", ""))

	document := bdoc.NewDocument("resume")
	document.Scenarios = []string{"sc-a", "sc-b"}
	draft := createBuilderPublishDraft(t, harness, document)
	body := `{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},` +
		`"scenario":{"name":"sc-a"},"experiment":{"name":"exp","action":"create"}}`

	harness.failConfigKind = builderKindScenario
	failed, _ := publishBuilderDraft(t, harness, draft, body, http.StatusInternalServerError)

	if stage := failed.Stages[len(failed.Stages)-1]; failed.Status != bapi.PublishPartial ||
		stage.Name != builderPublishStageScenario || stage.Status != bapi.PublishFailed ||
		stage.Message != "scenario publication failed" {
		t.Fatalf("failed publication = %+v, want the scenario stage failed last", failed)
	}

	if harness.experimentWrites != 0 {
		t.Fatalf("experiment writes = %d after the scenario stage failed, want none", harness.experimentWrites)
	}

	harness.failConfigKind = ""
	published, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	if stage := builderStageNamed(t, published, builderPublishStageTopology); stage.Status != bapi.PublishSkipped {
		t.Fatalf("topology stage = %+v, want it skipped", stage)
	}

	if stage := builderStageNamed(t, published, builderPublishStageScenario); stage.Status != "updated" {
		t.Fatalf("scenario stage = %+v, want it updated", stage)
	}

	retried, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	for _, stage := range retried.Stages {
		if stage.Status != bapi.PublishSkipped {
			t.Fatalf("retry stages = %+v, want every stage skipped", retried.Stages)
		}
	}

	if stage := builderStageNamed(t, retried, builderPublishStageScenario); stage.Config != "" {
		t.Fatalf("retried scenario stage = %+v, want no one config named for two scenarios", stage)
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
	if count := harness.store.Count(bapi.NamespacePublished); count != 1 {
		t.Fatalf("published document count = %d, want 1", count)
	}
	// The topology once, then each scenario once.
	if harness.configWrites != 3 || harness.experimentWrites != 1 {
		t.Fatalf("writes after retry = configs %d, experiments %d", harness.configWrites, harness.experimentWrites)
	}

	for _, name := range []string{"sc-a", "sc-b"} {
		if scenario, err := harness.getConfig("Scenario/" + name); err != nil || scenario.Metadata.Annotations["topology"] != "topo" {
			t.Fatalf("scenario %s = %+v (%v), want topology topo added", name, scenario, err)
		}
	}
}

// TestBuilderPublishReportsScenariosWrittenBeforeFailure fails the write of
// the second of two listed scenarios that both need the topology: the first
// is written, a warning names it, and the scenario stage fails before the
// experiment. Publishing again leaves the first as it is, writes the second,
// and creates the experiment.
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

	if stage := failed.Stages[len(failed.Stages)-1]; failed.Status != bapi.PublishPartial ||
		stage.Name != builderPublishStageScenario || stage.Status != bapi.PublishFailed {
		t.Fatalf("failed publication = %+v, want the scenario stage failed last", failed)
	}

	warning := bdoc.NewIssue(bdoc.CodePublishScenarioPartial, "",
		"topology topo was added to scenario sc-a before the scenario stage failed")
	if !slices.Contains(failed.Warnings, warning) {
		t.Fatalf("warnings = %+v, want %+v", failed.Warnings, warning)
	}

	if got, want := annotations(), []string{"other,topo", ""}; !slices.Equal(got, want) || harness.experimentWrites != 0 {
		t.Fatalf("topology annotations = %q with %d experiment writes after the failure, want %q and none",
			got, harness.experimentWrites, want)
	}

	harness.failConfigName = ""
	published, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	stage := builderStageNamed(t, published, builderPublishStageScenario)
	want := builderPublishStage{
		Name: builderPublishStageScenario, Status: "updated", Config: "",
		Message: "added topology topo to scenario sc-b; scenario sc-a already names it",
	}
	if stage != want {
		t.Fatalf("scenario stage = %+v, want %+v", stage, want)
	}

	if got, want := annotations(), []string{"other,topo", "topo"}; !slices.Equal(got, want) {
		t.Fatalf("topology annotations = %q after the retry, want %q", got, want)
	}

	// The topology and each scenario once, then the experiment.
	if harness.configWrites != 3 || harness.experimentWrites != 1 {
		t.Fatalf("writes after retry = configs %d, experiments %d", harness.configWrites, harness.experimentWrites)
	}

	if created, err := harness.getConfig("Experiment/exp"); err != nil || created.Metadata.Annotations["scenario"] != "sc-b" {
		t.Fatalf("experiment = %+v (%v), want scenario sc-b", created, err)
	}
}

// TestBuilderPublishSwitchesExperimentScenario publishes a draft listing two
// scenarios with the experiment it was imported from, picking the first, then
// again picking the second: the experiment then names the second scenario and
// holds its apps in place of the first's.
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
		t.Fatalf("scenarios = %q with warnings %q, want [sc-a]", document.Scenarios, warnings)
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
		t.Fatalf("experiment scenario = %s with apps %q, want sc-a with its app", name, apps)
	}

	second, _ := publishBuilderDraft(t, harness, first.Draft, fmt.Sprintf(body, "update", "sc-b"), http.StatusOK)

	if name, apps := scenarioOf(); name != "sc-b" || !slices.Equal(apps, []string{"sc-b-app"}) {
		t.Fatalf("experiment scenario = %s with apps %q, want sc-b with its app", name, apps)
	}

	if stage := builderStageNamed(t, second, builderPublishStageExperiment); stage.Status != "updated" {
		t.Fatalf("experiment stage = %+v, want it updated", stage)
	}

	// Both scenarios named the topology since the first publication.
	if stage := builderStageNamed(t, second, builderPublishStageScenario); stage.Status != bapi.PublishSkipped {
		t.Fatalf("scenario stage = %+v, want it skipped", stage)
	}

	if second.Scenario == nil || second.Scenario.Name != "sc-b" {
		t.Fatalf("response scenario = %+v, want sc-b", second.Scenario)
	}
}

// TestAddTopologyAnnotation adds a topology to a scenario's topology
// annotation only when no name of it is the topology, after the names it
// has, which are kept byte for byte.
func TestAddTopologyAnnotation(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"", "topo"},
		{"other", "other,topo"},
		{" a , ,a,", " a , ,a,,topo"},
		{"xtopo,topology", "xtopo,topology,topo"},
		{"a, topo ,b", "a, topo ,b"},
		{"topo", "topo"},
	} {
		if got := addTopologyAnnotation(test.value, "topo"); got != test.want {
			t.Errorf("addTopologyAnnotation(%q, %q) = %q, want %q", test.value, "topo", got, test.want)
		}
	}
}

func TestBuilderPublishRetryIsIdempotent(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderTestOwner, "retry")
	body := `{"mode":"topology","topology":{"name":"retry","action":"create"}}`

	publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	tooStale := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderTestOwner, ifMatch: `"0"`,
	})
	if tooStale.Code != http.StatusPreconditionFailed {
		t.Fatalf("unrelated stale ETag status = %d, want %d", tooStale.Code, http.StatusPreconditionFailed)
	}

	response, _ := publishBuilderDraft(t, harness, draft, body, http.StatusOK)

	if response.Status != bapi.PublishSucceeded ||
		!strings.Contains(strings.Join(bdoc.IssueMessages(response.Warnings), " "), "already complete") ||
		response.Warnings[0].Code != bdoc.CodePublishRetryComplete {
		t.Fatalf("retry response = %#v", response)
	}
	if harness.configWrites != 1 {
		t.Fatalf("config writes = %d, want 1", harness.configWrites)
	}
}

// TestBuilderPublishExperimentWithIncludedTopology round trips an
// experiment whose topology includes another: phenix merged the included
// node into the experiment, generation marks it, and publishing writes the
// topology with its include rather than the node, while the experiment keeps
// the node exactly once.
func TestBuilderPublishExperimentWithIncludedTopology(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)
	document := generateBuilderDocument(t, harness, "Experiment/exp")

	if included := document.FindDevice("inc-host"); included == nil || included.Device.IncludedFrom != "shared" {
		t.Fatalf("inc-host is not marked as included: %s", asBuilderJSON(t, document))
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

// includedTopologyFixture returns topology "shared" with node inc-host,
// topology "root" with node web including the given topologies, and
// experiment "exp" created from root, holding both nodes as phenix merged
// them.
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

func asBuilderJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}

	return string(data)
}

// TestBuilderPublishRejectsIncludedHostnameClash refuses, before writing
// anything, to publish a topology whose included topology gained a node
// named like one of its own after the draft was imported: phenix would
// refuse to create an experiment from it.
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

			want := "topology root cannot be published: node WEB is defined both here and in its included topology shared"
			if !strings.Contains(reason, want) {
				t.Fatalf("refusal = %q, want %q", reason, want)
			}

			if harness.configWrites != 0 || harness.experimentWrites != 0 {
				t.Fatalf("writes = %d configs and %d experiments, want none", harness.configWrites, harness.experimentWrites)
			}
		})
	}
}

// TestBuilderPublishCopyCreatesNewTopology publishes a draft of a copy: it
// creates the new topology, with the includes of the one it was copied
// from, and never updates that one, which stays as it was.
func TestBuilderPublishCopyCreatesNewTopology(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)

	original, err := harness.getConfig("Topology/root")
	if err != nil {
		t.Fatalf("topology root missing: %v", err)
	}

	document, _ := postBuilderGenerate(t, harness, nil, `{"source":"Topology/root","copy":true}`)

	// The draft of a copy has no source token, as the editor creates it. A
	// client that sends the token of the topology all the same is refused
	// too: the document names no config.
	for _, token := range []string{"", "Topology/root"} {
		draft := createBuilderPublishDraft(t, harness, document, token)

		_, refusal := publishBuilderDraft(t, harness, draft,
			`{"mode":"topology","topology":{"name":"root","action":"update"}}`, http.StatusConflict)
		if refusal != "topology root is not the source this draft was loaded from" {
			t.Fatalf("token %q: refusal = %q, want the copy refused for the topology it was copied from", token, refusal)
		}

		if harness.configWrites != 0 {
			t.Fatalf("token %q: the refused update wrote %d configs", token, harness.configWrites)
		}
	}

	draft := createBuilderPublishDraft(t, harness, document, "")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"root-copy","action":"create"}}`, http.StatusOK)
	if response.Topology == nil || response.Topology.Name != "root-copy" {
		t.Fatalf("publish response = %+v, want topology root-copy", response)
	}

	created, err := harness.getConfig("Topology/root-copy")
	if err != nil {
		t.Fatalf("topology root-copy missing: %v", err)
	}

	// The included node stays included: the copy holds the topology's own
	// node and the reference.
	if got := topologyHostnames(t, harness, "root-copy"); !reflect.DeepEqual(got, []string{"web"}) ||
		!reflect.DeepEqual(created.Spec["includeTopologies"], []string{"shared"}) {
		t.Errorf("topology root-copy = %v, want web and the shared include", created.Spec)
	}

	if _, err := bapi.DecodeReference(created.Metadata.Annotations[bapi.DocumentAnnotation]); err != nil {
		t.Errorf("topology root-copy has no builder document: %v", err)
	}

	after, err := harness.getConfig("Topology/root")
	if err != nil {
		t.Fatalf("topology root missing: %v", err)
	}

	if !reflect.DeepEqual(after, original) {
		t.Errorf("topology root = %+v, want it unchanged: %+v", after, original)
	}

	if harness.configWrites != 1 {
		t.Errorf("config writes = %d, want only the new topology", harness.configWrites)
	}
}

// TestBuilderPublishCombinedCreatesNewTopology publishes a draft of a
// combined import: the new topology holds every node, includes nothing, and
// the topologies it was combined from stay as they were.
func TestBuilderPublishCombinedCreatesNewTopology(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)
	before := asBuilderJSON(t, harness.configs)

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

	if got := topologyHostnames(t, harness, "root-combined"); !reflect.DeepEqual(got, []string{"inc-host", "web"}) {
		t.Errorf("nodes of root-combined = %v, want inc-host and web", got)
	}

	if _, included := created.Spec["includeTopologies"]; included {
		t.Errorf("root-combined includes %v, want nothing", created.Spec["includeTopologies"])
	}

	if after := asBuilderJSON(t, harness.configs[:3]); after != before {
		t.Errorf("the configs it was combined from = %s, want them unchanged: %s", after, before)
	}
}

// TestBuilderPublishCombinedRefusesKeptIncludeClash refuses, before writing
// anything, a combined draft whose kept include defines a hostname of the
// document: the include could not be read when the draft was imported, so it
// stayed a reference, and phenix would refuse to merge it.
func TestBuilderPublishCombinedRefusesKeptIncludeClash(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared", "later")...)

	document, warnings := postBuilderGenerate(t, harness, nil, `{"source":"Topology/root","includes":"combine"}`)

	if !reflect.DeepEqual(document.Source.IncludeTopologies, []string{"later"}) ||
		!slices.Contains(warnings, "Included topology later was not combined and stays in includeTopologies: "+
			"publishing keeps the reference.") {
		t.Fatalf("includes = %v, warnings = %q, want only the include that cannot be read kept",
			document.Source.IncludeTopologies, warnings)
	}

	draft := createBuilderPublishDraft(t, harness, document, "")

	// The topology appears, with a node named like one the draft copied.
	later := builderConfig(t, builderKindTopology, "later")
	later.Spec = map[string]any{"nodes": []any{includeNode("INC-HOST")}}
	harness.configs = append(harness.configs, later)

	_, refusal := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"root-combined","action":"create"}}`, http.StatusConflict)

	want := "topology root-combined cannot be published: node INC-HOST is defined both here and in its included topology later"
	if !strings.Contains(refusal, want) {
		t.Errorf("refusal = %q, want %q", refusal, want)
	}

	if harness.configWrites != 0 {
		t.Errorf("the refused publish wrote %d configs", harness.configWrites)
	}

	// Once the clash is gone the draft publishes, and keeps the reference.
	harness.configs[len(harness.configs)-1].Spec = map[string]any{"nodes": []any{includeNode("later-host")}}

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"root-combined","action":"create"}}`, http.StatusOK)

	created, err := harness.getConfig("Topology/root-combined")
	if err != nil {
		t.Fatalf("topology root-combined missing: %v", err)
	}

	if got := topologyHostnames(t, harness, "root-combined"); !reflect.DeepEqual(got, []string{"inc-host", "web"}) ||
		!reflect.DeepEqual(created.Spec["includeTopologies"], []string{"later"}) {
		t.Errorf("topology root-combined = %v, want inc-host, web and the later include", created.Spec)
	}
}

// TestBuilderPublishRefusesIncludedHostnamePhenixRefuses refuses, before
// writing anything, an experiment whose included topology gained a node
// named "all" after the draft was imported, or holds a node named with a
// single character, as an older phenix stored it: phenix refuses the
// experiment only once the topology is written. A topology publishes, as
// phenix stores it.
func TestBuilderPublishRefusesIncludedHostnamePhenixRefuses(t *testing.T) {
	for _, refused := range []struct{ hostname, reason string }{
		{hostname: "all", reason: "hostname 'all' is reserved: "},
		{hostname: "x", reason: "hostname 'x' is 1 character long: "},
	} {
		for _, test := range []struct {
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
		} {
			t.Run(refused.hostname+"/"+test.name, func(t *testing.T) {
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

				_, reason := publishBuilderDraft(t, harness, draft, test.body, test.code)

				if test.code == http.StatusOK {
					return
				}

				want := "experiment " + test.experiment + " cannot be published: in included topology shared, " + refused.reason
				if !strings.Contains(reason, want) {
					t.Fatalf("refusal = %q, want %q", reason, want)
				}

				if harness.configWrites != 0 || harness.experimentWrites != 0 {
					t.Fatalf("writes = %d configs and %d experiments, want none", harness.configWrites, harness.experimentWrites)
				}
			})
		}
	}
}

// TestBuilderPublishExperimentUpdateChecksIncludes merges the includes
// into an updated experiment only the way import reads them: a topology the
// caller may not read, or a file path, stops the update before any write.
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
			harness := newBuilderHarness(t, includedTopologyFixture(t, test.includes...)...)
			document := generateBuilderDocument(t, harness, "Experiment/exp")
			draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")

			_, refusal := publishBuilderDraftAs(t, harness, draft, test.role,
				`{"mode":"topology-experiment","topology":{"name":"root","action":"update"},`+
					`"experiment":{"name":"exp","action":"update"}}`, test.status)
			if !strings.Contains(refusal.Message, test.want) {
				t.Fatalf("refusal = %q, want %q", refusal.Message, test.want)
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
	harness *builderHarness,
	draft builderDraftResponse,
	body string,
	status int,
) (builderPublishResponse, string) {
	t.Helper()

	response, refusal := publishBuilderDraftAs(t, harness, draft, nil, body, status)

	return response, refusal.Message
}

// builderPublishRefusal is what the server says of a publication it refused:
// the code of the refusal, its words, and the issues it is made of.
type builderPublishRefusal struct {
	Code     string            `json:"code"`
	Message  string            `json:"message"`
	Cause    string            `json:"cause"`
	Metadata map[string]string `json:"metadata"`
	Issues   []bdoc.Issue      `json:"issues"`
}

// publishBuilderDraftAs is [publishBuilderDraft] with the caller holding role
// (the full role when nil), returning all of a refusal.
func publishBuilderDraftAs(
	t *testing.T,
	harness *builderHarness,
	draft builderDraftResponse,
	role *rbac.Role,
	body string,
	status int,
) (builderPublishResponse, builderPublishRefusal) {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderTestOwner, role: role, ifMatch: draft.ETag,
	})
	if recorder.Code != status {
		t.Fatalf("publish %s: status = %d, want %d: %s", body, recorder.Code, status, recorder.Body.String())
	}

	var (
		response builderPublishResponse
		refusal  builderPublishRefusal
	)

	harness.decode(recorder, &response)
	harness.decode(recorder, &refusal)

	return response, refusal
}

// editBuilderDraft adds a device to the document and saves it as the draft's
// next snapshot, as an edit in the editor does, and returns the draft after
// it.
func editBuilderDraft(
	t *testing.T,
	harness *builderHarness,
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

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/snapshots",
		body:   `{"summary":"added ` + hostname + `","document":` + string(data) + `}`,
		user:   builderTestOwner, ifMatch: draft.ETag,
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

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/documents/" + ref.ID, user: builderTestOwner,
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

// TestBuilderPublishAgainAfterEdits publishes a draft, edits it and
// publishes it again, twice over, as the editor does. A new diagram, one
// imported from the topology and one opened from its published diagram each
// update the topology again: it holds what the draft last published. A change
// anyone else made to the topology since is refused, not overwritten, and a
// draft with no part in the topology still may not update it.
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

				if published.Stages[1].Status != "updated" {
					t.Fatalf("stages = %#v, want the topology updated", published.Stages)
				}

				draft = published.Draft
			}

			if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"aa", "bb", "cc"}) {
				t.Fatalf("topology nodes = %v, want aa, bb and cc", got)
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

			if _, reason := publishBuilderDraft(t, harness, draft, update, http.StatusConflict); reason !=
				"topology lab changed after this draft published it" {
				t.Fatalf("refusal = %q, want the topology changed", reason)
			}

			other := harness.createDraft(builderTestOwner, "other")
			if _, reason := publishBuilderDraft(t, harness, other, update, http.StatusConflict); reason !=
				"topology lab is not the source this draft was loaded from" {
				t.Fatalf("refusal = %q, want another draft refused", reason)
			}

			// The topology still references its published diagram, which does
			// not have their node. A draft opened from that diagram only now,
			// after their change, may not overwrite it either.
			opened := openPublishedBuilderDocument(t, harness, "lab")
			reopened := editBuilderDraft(t, harness,
				createBuilderPublishDraft(t, harness, opened.document, "builder-doc/"+opened.id), opened.document, "ee")

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

// TestBuilderPublishAgainAfterTopologyDeleted deletes a published topology
// from the drafts page, then publishes it again from the draft imported from
// it, and from a draft opened from its published diagram. Neither is refused
// because its source is gone: each creates the topology again.
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

	for _, draft := range []builderDraftResponse{published.Draft, opened} {
		current := openPublishedBuilderDocument(t, harness, "range")

		deleted := harness.do(builderRequest{
			method: http.MethodDelete, path: "/builder/documents/" + current.id, user: builderTestOwner,
		})
		if deleted.Code != http.StatusNoContent {
			t.Fatalf("deleting the topology: status = %d: %s", deleted.Code, deleted.Body.String())
		}

		again, _ := publishBuilderDraft(t, harness, draft, create, http.StatusOK)
		if again.Stages[1].Status != "created" {
			t.Fatalf("stages = %#v, want the topology created", again.Stages)
		}

		if got := topologyHostnames(t, harness, "range"); !slices.Equal(got, []string{"aa", "bb"}) {
			t.Fatalf("topology nodes = %v, want aa and bb", got)
		}
	}
}

// forkBuilderDraft creates a draft for the user that forks the draft named
// forkOf ("<owner>/<draft id>"), as saving the editor's history as a new
// draft does, and returns the answer.
func forkBuilderDraft(
	t *testing.T,
	harness *builderHarness,
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

	return harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", body: string(body), user: user, role: role,
	})
}

// TestBuilderPublishForkUpdatesWhatItsDraftPublished saves a draft's
// edited history as a new draft, as the editor does when the draft changed
// on the server, and publishes it. The fork updates the topology the draft
// published, and the experiment with it, for the draft's owner and for
// another user who may read the draft, but not what the draft publishes
// after the fork. Nobody who may not read the draft can fork it, and so
// claim what it published.
func TestBuilderPublishForkUpdatesWhatItsDraftPublished(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`

	// Draft "original" publishes topology lab with node a, with the body
	// given or the topology alone, and the fork starts from what it
	// published.
	start := func(t *testing.T, body ...string) (*builderHarness, builderDraftResponse, *bdoc.Document) {
		t.Helper()

		body = append(body, `{"mode":"topology","topology":{"name":"lab","action":"create"}}`)
		harness := newBuilderHarness(t)
		document := bdoc.NewDocument("lab")
		original := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
		published, _ := publishBuilderDraft(t, harness, original, body[0], http.StatusOK)

		return harness, published.Draft, document
	}

	// Forks the draft as the user. The fork keeps the draft's source token,
	// so opening the published diagram does not find it, and records what
	// the draft last published.
	fork := func(
		t *testing.T, harness *builderHarness, user string, original builderDraftResponse, document *bdoc.Document,
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

		path := "/builder/drafts/" + forked.Owner + "/" + forked.ID
		recorder := harness.do(builderRequest{
			method: http.MethodPost, path: path + "/snapshots", body: `{"summary":"added b","document":` + string(data) + `}`,
			user: forked.Owner, ifMatch: forked.ETag,
		})
		if recorder.Code != http.StatusCreated {
			t.Fatalf("saving the fork's edit: status = %d: %s", recorder.Code, recorder.Body.String())
		}

		harness.decode(recorder, &forked)

		recorder = harness.do(builderRequest{
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

	for _, user := range []string{builderTestOwner, builderTestPeer} {
		t.Run("forked by "+user, func(t *testing.T) {
			harness, original, document := start(t)

			publishFork(t, harness, fork(t, harness, user, original, document), document, update, http.StatusOK)

			if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"aa", "bb"}) {
				t.Fatalf("topology nodes = %v, want aa and bb", got)
			}
		})
	}

	t.Run("with the experiment the draft published", func(t *testing.T) {
		harness, original, document := start(t, `{"mode":"topology-experiment",`+
			`"topology":{"name":"lab","action":"create"},"experiment":{"name":"exp","action":"create"}}`)

		publishFork(t, harness, fork(t, harness, builderTestOwner, original, document), document,
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
		draft := fork(t, harness, builderTestOwner, original, &forked)

		// The original draft publishes again, so lab no longer holds what it
		// published when it was forked.
		edited := editBuilderDraft(t, harness, original, document, "cc")
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
		owner := builderOwnerRole()

		for _, forkOf := range []string{
			original.Owner + "/" + original.ID,
			original.Owner + "/id-missing",
			original.ID,
		} {
			recorder := forkBuilderDraft(t, harness, builderTestPeer, &owner, forkOf, document)
			if recorder.Code != http.StatusNotFound {
				t.Errorf("fork of %q: status = %d, want %d: %s",
					forkOf, recorder.Code, http.StatusNotFound, recorder.Body.String())
			}
		}

		recorder := harness.do(builderRequest{
			method: http.MethodGet, path: "/builder/drafts", user: builderTestPeer, role: &owner,
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

func TestBuilderPublishAfterThePublishedSnapshotAgedOut(t *testing.T) {
	const update = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
		`"experiment":{"name":"exp","action":"update"}}`

	// Publishes lab and exp, saves once and deletes the published snapshot.
	// That leaves the draft as the published snapshot ageing out of its
	// history does (see TestPruningKeepsAnAgedOutPublication in api/builder):
	// it keeps its publication and is dirty.
	aged := func(t *testing.T) (*builderHarness, builderDraftResponse, *bdoc.Document) {
		t.Helper()

		harness := newBuilderHarness(t)
		document := bdoc.NewDocument("lab")
		draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
		response, _ := publishBuilderDraft(t, harness, draft, `{"mode":"topology-experiment",`+
			`"topology":{"name":"lab","action":"create"},"experiment":{"name":"exp","action":"create"}}`, http.StatusOK)
		published := response.Draft.Publication
		draft = response.Draft

		draft = editBuilderDraft(t, harness, draft, document, "bb")

		deleted := harness.do(builderRequest{
			method: http.MethodDelete, user: builderTestOwner, ifMatch: draft.ETag,
			path: "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/snapshots/" + published.SnapshotID,
		})
		if deleted.Code != http.StatusOK {
			t.Fatalf("deleting the published snapshot: status = %d: %s", deleted.Code, deleted.Body.String())
		}

		harness.decode(deleted, &draft)

		switch {
		case draft.Snapshots != 2:
			t.Fatalf("snapshots = %d, want 2", draft.Snapshots)
		case draft.Publication == nil || draft.Publication.SnapshotID != published.SnapshotID ||
			draft.Publication.DocumentID != published.DocumentID:
			t.Fatalf("publication = %+v, want it kept as %+v", draft.Publication, published)
		case !draft.Dirty:
			t.Fatal("a draft whose published snapshot aged out must be dirty")
		}

		recorder := harness.do(builderRequest{
			method: http.MethodGet, user: builderTestOwner,
			path: "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/snapshots/" + published.SnapshotID,
		})
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("published snapshot: status = %d, want it gone: %s", recorder.Code, recorder.Body.String())
		}

		return harness, draft, document
	}

	t.Run("by the draft", func(t *testing.T) {
		harness, draft, _ := aged(t)

		response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)

		switch {
		case !slices.Equal(harness.reconfigured, []string{"exp"}):
			t.Fatalf("configured = %v, want exp updated", harness.reconfigured)
		case response.Draft.Dirty || response.Draft.Publication.SnapshotID != response.Draft.SnapshotID:
			t.Fatalf("publication = %+v, want the current snapshot published", response.Draft.Publication)
		}
	})

	t.Run("by a draft that forks it", func(t *testing.T) {
		harness, draft, document := aged(t)

		recorder := forkBuilderDraft(t, harness, builderTestOwner, nil, draft.Owner+"/"+draft.ID, document)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("fork: status = %d: %s", recorder.Code, recorder.Body.String())
		}

		var forked builderDraftResponse
		harness.decode(recorder, &forked)

		if forked.Forked == nil || forked.Forked.DocumentID != draft.Publication.DocumentID {
			t.Fatalf("fork's forked publication = %+v, want the draft's %+v", forked.Forked, draft.Publication)
		}

		publishBuilderDraft(t, harness, editBuilderDraft(t, harness, forked, document, "cc"), update, http.StatusOK)

		if !slices.Equal(harness.reconfigured, []string{"exp"}) {
			t.Fatalf("configured = %v, want exp updated", harness.reconfigured)
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
// experiment, edits and publishes both again, twice over: from a draft
// imported from the experiment, and from a new diagram that created them.
// Each update runs the apps' configure stage, as every other update of an
// experiment does, and a failed one leaves the experiment as it was, so
// publishing again retries it.
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
					t.Fatalf("create: %d experiments created and %v reconfigured, want 1 created, which configures it",
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

			if last := failed.Stages[len(failed.Stages)-1]; failed.Status != bapi.PublishPartial ||
				last.Name != builderPublishStageExperiment || last.Status != bapi.PublishFailed {
				t.Fatalf("failed configure stage = %#v, want a partial publication with the experiment failed", failed)
			}

			if after, _ := harness.getConfig("Experiment/exp"); !reflect.DeepEqual(after.Spec, before.Spec) {
				t.Fatalf("experiment after a failed configure stage = %v, want it as it was", after.Spec)
			}

			for _, hostname := range []string{"", "cc"} {
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

			if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"aa", "bb", "cc"}) {
				t.Fatalf("topology nodes = %v, want aa, bb and cc", got)
			}
		})
	}
}

// TestBuilderPublishRepairsDocumentDespiteCleanupFailure publishes again
// a document whose stored copy was damaged: the copy is repaired, and a
// failure to remove the damaged content is a warning, not a failed publish
// that skipped the topology.
func TestBuilderPublishRepairsDocumentDespiteCleanupFailure(t *testing.T) {
	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("rep")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
	published, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"rep","action":"create"}}`, http.StatusOK)

	// One chunk of the stored document goes missing.
	for _, key := range harness.store.Keys(bapi.NamespaceChunks) {
		if strings.HasPrefix(key, "published/") {
			harness.store.Drop(bapi.NamespaceChunks, key)

			break
		}
	}

	harness.store.FailPrefixDelete = builderRefusePrefixDelete
	defer func() { harness.store.FailPrefixDelete = nil }()

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + published.Draft.Owner + "/" + published.Draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"rep","action":"update"}}`,
		user:   builderTestOwner, ifMatch: published.Draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("publish again: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderPublishResponse
	harness.decode(recorder, &response)

	if builderWarning(recorder) == "" || len(response.Warnings) != 1 {
		t.Fatalf("warning header %q and warnings %v, want the cleanup failure reported",
			builderWarning(recorder), response.Warnings)
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
		t.Fatalf("the topology's document after the repair: %v", err)
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
// experiment this draft did not publish, or that anyone else has changed
// since it did, however the experiment's topology is tied to the draft.
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
			reason: "builder source Experiment/exp changed after this draft was imported",
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
			reason: "experiment other is not the source this draft was loaded from",
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
				t.Fatalf("refusal = %q, want %q", reason, test.reason)
			}

			if harness.configWrites != writes || len(harness.reconfigured) != 0 {
				t.Fatalf("the refused publication wrote %d configs and configured %v",
					harness.configWrites-writes, harness.reconfigured)
			}

			if exp, _ := harness.getConfig("Experiment/" + test.experiment); !reflect.DeepEqual(
				exp.Spec["schedules"], map[string]any{"aa": "theirs"}) {
				t.Fatalf("experiment schedules = %v, want theirs kept", exp.Spec["schedules"])
			}
		})
	}
}

// TestBuilderPublishKeepsExperimentStartedWhileConfiguring leaves an
// experiment started while its configure stage ran, which the CLI can do
// without the web lock, as it is when that stage fails: restoring the
// experiment as it was before the update would write back its old status.
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

	if !reflect.DeepEqual(exp.Status, started) {
		t.Fatalf("experiment status = %v, want the started one kept", exp.Status)
	}
}

// TestBuilderPublishExperimentUpdateRereadsUnderLock reads the
// experiment again once its lock is held: one started since preflight read it
// is refused, and one otherwise changed keeps what changed, its status
// included, rather than having it overwritten with the preflight copy.
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
		t.Fatalf("updated experiment topology = %v, want nodes aa and bb", topology)
	}
}

// TestBuilderPublishRefusesExperimentNamesBeforeWriting refuses an
// experiment name experiment.Create would refuse, before the document or the
// topology is written: the reserved name "all", and in auto bridge mode, a
// name longer than a bridge name.
func TestBuilderPublishRefusesExperimentNamesBeforeWriting(t *testing.T) { //nolint:paralleltest // mutates bridge mode
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
			harness := newBuilderHarness(t)
			draft := harness.createDraft(builderTestOwner, "names")

			_, reason := publishBuilderDraft(t, harness, draft,
				`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
					`"experiment":{"name":"`+test.experiment+`","action":"create"}}`, http.StatusUnprocessableEntity)
			if !strings.HasPrefix(reason, test.want) {
				t.Fatalf("refusal = %q, want %q", reason, test.want)
			}

			if harness.configWrites != 0 || harness.store.Count(bapi.NamespacePublished) != 0 {
				t.Fatal("a refused experiment name had side effects")
			}
		})
	}

	// Fifteen characters are allowed.
	setBridgeMode(common.BridgeModeAuto)
	harness := newBuilderHarness(t)
	publishBuilderDraft(t, harness, harness.createDraft(builderTestOwner, "names"),
		`{"mode":"topology-experiment","topology":{"name":"topo","action":"create"},`+
			`"experiment":{"name":"fifteen-chars-x","action":"create"}}`, http.StatusOK)
}
