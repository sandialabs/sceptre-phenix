package web

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/types"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
)

// pinsNodes returns a node spec for each hostname.
func pinsNodes(hostnames []string) []any {
	nodes := make([]any, 0, len(hostnames))
	for _, hostname := range hostnames {
		nodes = append(nodes, includeNode(hostname))
	}

	return nodes
}

// pinsExperiment returns experiment exp of topology lab with the nodes and
// the host pins (spec.schedules) given. A nil schedules has no pins.
func pinsExperiment(t *testing.T, schedules map[string]any, hostnames ...string) store.Config {
	t.Helper()

	nodes := pinsNodes(hostnames)

	experiment := builderConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": nodes},
		"vlans":    map[string]any{"aliases": map[string]any{}},
	}

	if schedules != nil {
		experiment.Spec["schedules"] = schedules
	}

	experiment.Metadata.Annotations = store.Annotations{"topology": "lab"}

	return experiment
}

// experimentSchedules returns the host pins of a stored experiment config.
func experimentSchedules(t *testing.T, config *store.Config) map[string]string {
	t.Helper()

	exp, err := types.DecodeExperimentFromConfig(*config)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig returned error: %v", err)
	}

	return exp.Spec.Schedules()
}

// TestUpdatedExperimentConfigPrunesHostPins removes the host pins of VMs that
// the new topology no longer has, matched by exact hostname as phenix
// matches them, and keeps every other pin as it is.
func TestUpdatedExperimentConfigPrunesHostPins(t *testing.T) {
	for _, test := range []struct {
		name        string
		schedules   map[string]any
		before      []string
		after       []string
		wantPins    map[string]string
		wantRemoved []bapi.HostPin
	}{
		{
			name:        "unchanged",
			schedules:   map[string]any{"plc-1": "compute1", "hmi": "compute2"},
			before:      []string{"plc-1", "hmi", "historian"},
			after:       []string{"plc-1", "hmi", "historian"},
			wantPins:    map[string]string{"plc-1": "compute1", "hmi": "compute2"},
			wantRemoved: nil,
		},
		{
			name:        "renamed",
			schedules:   map[string]any{"plc-1": "compute1", "hmi": "compute2"},
			before:      []string{"plc-1", "hmi"},
			after:       []string{"plc-1b", "hmi"},
			wantPins:    map[string]string{"hmi": "compute2"},
			wantRemoved: []bapi.HostPin{{Hostname: "plc-1", Host: "compute1", RenamedTo: ""}},
		},
		{
			name:      "removed",
			schedules: map[string]any{"plc-1": "compute1", "plc-2": "compute3", "hmi": "compute2"},
			before:    []string{"plc-1", "plc-2", "hmi"},
			after:     []string{"hmi"},
			wantPins:  map[string]string{"hmi": "compute2"},
			wantRemoved: []bapi.HostPin{
				{Hostname: "plc-1", Host: "compute1", RenamedTo: ""},
				{Hostname: "plc-2", Host: "compute3", RenamedTo: ""},
			},
		},
		{
			name:      "case differs",
			schedules: map[string]any{"plc-1": "compute1", "HMI": "compute2"},
			before:    []string{"plc-1", "HMI"},
			after:     []string{"PLC-1", "HMI"},
			wantPins:  map[string]string{"HMI": "compute2"},
			wantRemoved: []bapi.HostPin{
				{Hostname: "plc-1", Host: "compute1", RenamedTo: ""},
			},
		},
		{
			name:        "no pins",
			schedules:   nil,
			before:      []string{"plc-1"},
			after:       []string{"plc-2"},
			wantPins:    map[string]string{},
			wantRemoved: nil,
		},
		{
			name:        "empty pins",
			schedules:   map[string]any{},
			before:      []string{"plc-1"},
			after:       []string{"plc-2"},
			wantPins:    map[string]string{},
			wantRemoved: nil,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			existing := pinsExperiment(t, test.schedules, test.before...)
			projection := &bdoc.Topology{
				Spec: map[string]any{"nodes": pinsNodes(test.after)}, VLANAliases: map[string]int{}, Warnings: nil,
			}

			spec, err := projection.SpecV1()
			if err != nil {
				t.Fatalf("SpecV1 returned error: %v", err)
			}

			updated, removed, err := updatedExperimentConfig(&existing, spec, projection, "lab", "", nil)
			if err != nil {
				t.Fatalf("updatedExperimentConfig(%v to %q) returned error: %v", test.schedules, test.after, err)
			}

			if diff := cmp.Diff(test.wantRemoved, removed); diff != "" {
				t.Errorf("updatedExperimentConfig(%v to %q) removed mismatch (-want +got):\n%s",
					test.schedules, test.after, diff)
			}

			if diff := cmp.Diff(test.wantPins, experimentSchedules(t, updated)); diff != "" {
				t.Errorf("updatedExperimentConfig(%v to %q) schedules mismatch (-want +got):\n%s",
					test.schedules, test.after, diff)
			}

			if test.schedules != nil && !cmp.Equal(existing.Spec["schedules"], any(test.schedules)) {
				t.Errorf("updatedExperimentConfig(%v to %q) changed the existing schedules to %v, want them kept",
					test.schedules, test.after, existing.Spec["schedules"])
			}
		})
	}
}

// TestHostPinsWarning names each removed pin, its host and a new hostname
// only when the caller may see the schedule, and else counts them.
func TestHostPinsWarning(t *testing.T) {
	pin := bapi.HostPin{Hostname: "plc-2", Host: "compute3", RenamedTo: ""}
	renamed := bapi.HostPin{Hostname: "plc-1", Host: "compute1", RenamedTo: "plc-1b"}

	for _, test := range []struct {
		name    string
		removed []bapi.HostPin
		visible bool
		want    string
	}{
		{
			name: "one", removed: []bapi.HostPin{pin}, visible: true,
			want: "Publish removed the host pin of a VM that is no longer in the topology: plc-2 (on compute3).",
		},
		{
			name: "renamed", removed: []bapi.HostPin{renamed}, visible: true,
			want: "Publish removed the host pin of a VM that is no longer in the topology: plc-1 (on compute1, renamed to plc-1b).",
		},
		{
			name: "several", removed: []bapi.HostPin{renamed, pin}, visible: true,
			want: "Publish removed the host pins of VMs that are no longer in the topology: " +
				"plc-1 (on compute1, renamed to plc-1b), plc-2 (on compute3).",
		},
		{
			name: "one hidden", removed: []bapi.HostPin{renamed}, visible: false,
			want: "Publish removed the host pin of 1 VM that is no longer in the topology.",
		},
		{
			name: "several hidden", removed: []bapi.HostPin{renamed, pin}, visible: false,
			want: "Publish removed the host pins of 2 VMs that are no longer in the topology.",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := bdoc.NewIssue(bdoc.CodePublishExperimentPins, "", test.want)

			if diff := cmp.Diff(want, hostPinsWarning(test.removed, test.visible)); diff != "" {
				t.Errorf("hostPinsWarning(%v, %t) mismatch (-want +got):\n%s", test.removed, test.visible, diff)
			}
		})
	}
}

// renameBuilderDevice renames a device of the document, keeping its node
// ID as the editor does, saves the document as the draft's next snapshot,
// and returns the draft.
func renameBuilderDevice(
	t *testing.T,
	harness *builderHarness,
	draft builderDraftResponse,
	document *bdoc.Document,
	from, to string,
) builderDraftResponse {
	t.Helper()

	node := document.FindDevice(from)
	if node == nil {
		t.Fatalf("device %s is not in the document", from)
	}

	node.Label = to
	node.Device.Hostname = to
	node.Device.Spec["general"].(map[string]any)["hostname"] = to

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.ID + "/snapshots",
		body:   `{"summary":"renamed ` + from + `","document":` + string(data) + `}`,
		user:   builderAlice, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("saving the rename of %s: status = %d: %s", from, recorder.Code, recorder.Body.String())
	}

	var renamed builderDraftResponse
	decodeBuilderAnswer(t, recorder, &renamed)

	return renamed
}

// pinsRole may use the Builder and change every config, but may not see an
// experiment's schedule.
func pinsRole() *rbac.Role {
	return builderRole(builderConfigPolicy(builderConfigVerbs))
}

// TestBuilderPublishRemovesStaleHostPins updates an imported experiment
// whose diagram renamed one pinned device and no longer has another. The
// update removes their pins and keeps the others. The result and the dry run
// name the removed pins, and the new hostname, only for a caller that may
// see the schedule.
func TestBuilderPublishRemovesStaleHostPins(t *testing.T) {
	const (
		update = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
			`"experiment":{"name":"exp","action":"update"}}`
		preview = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
			`"experiment":{"name":"exp","action":"update"},"dryRun":true}`
	)

	pins := []bapi.HostPin{
		{Hostname: "aa", Host: "compute1", RenamedTo: "aa-2"},
		{Hostname: "gone", Host: "compute3", RenamedTo: ""},
	}
	named := bdoc.NewIssue(bdoc.CodePublishExperimentPins, "",
		"Publish removed the host pins of VMs that are no longer in the topology: aa (on compute1, renamed to aa-2), gone (on compute3).")
	counted := bdoc.NewIssue(bdoc.CodePublishExperimentPins, "",
		"Publish removed the host pins of 2 VMs that are no longer in the topology.")

	for _, test := range []struct {
		name        string
		role        *rbac.Role
		wantPins    []bapi.HostPin
		wantWarning bdoc.Issue
	}{
		{name: "schedule visible", role: nil, wantPins: pins, wantWarning: named},
		{name: "schedule hidden", role: pinsRole(), wantPins: nil, wantWarning: counted},
	} {
		t.Run(test.name, func(t *testing.T) {
			lab := builderConfig(t, builderKindTopology, "lab")
			lab.Spec = map[string]any{"nodes": []any{includeNode("aa"), includeNode("bb")}}
			experiment := pinsExperiment(t,
				map[string]any{"aa": "compute1", "bb": "compute2", "gone": "compute3"}, "aa", "bb")

			harness := newBuilderHarness(t, lab, experiment)
			document := generateBuilderDocument(t, harness, "Experiment/exp")
			draft := createBuilderPublishDraft(t, harness, document, "Experiment/exp")
			draft = renameBuilderDevice(t, harness, draft, document, "aa", "aa-2")

			recorder, dryRun := previewBuilderDraft(t, harness, draft, test.role, preview)
			if recorder.Code != http.StatusOK {
				t.Fatalf("dry run: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
			}

			if dryRun.Changes == nil {
				t.Fatalf("dry run: changes = nil, want them: errors %v", dryRun.Errors)
			}

			if diff := cmp.Diff(test.wantPins, dryRun.Changes.RemovedHostPins); diff != "" {
				t.Errorf("dry run: removedHostPins mismatch (-want +got):\n%s", diff)
			}

			if !issuesContain(dryRun.Warnings, test.wantWarning) {
				t.Errorf("dry run: warnings = %v, want %v among them", dryRun.Warnings, test.wantWarning)
			}

			if got := experimentSchedules(t, mustBuilderConfig(t, harness, "Experiment/exp")); len(got) != 3 {
				t.Errorf("dry run: schedules = %v, want the 3 pins kept", got)
			}

			published, _ := publishBuilderDraftAs(t, harness, draft, test.role, update, http.StatusOK)

			if diff := cmp.Diff(test.wantPins, published.RemovedHostPins); diff != "" {
				t.Errorf("publishing: removedHostPins mismatch (-want +got):\n%s", diff)
			}

			if !issuesContain(published.Warnings, test.wantWarning) {
				t.Errorf("publishing: warnings = %v, want %v among them", published.Warnings, test.wantWarning)
			}

			want := map[string]string{"bb": "compute2"}
			if diff := cmp.Diff(want, experimentSchedules(t, mustBuilderConfig(t, harness, "Experiment/exp"))); diff != "" {
				t.Errorf("publishing: schedules mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestBuilderPublishNamesRenameFromPublishedDocument tells a device renamed
// since the draft last published the experiment by its node ID in that
// published document. The editor gave the device a random ID, which no import
// gives.
func TestBuilderPublishNamesRenameFromPublishedDocument(t *testing.T) {
	const update = `{"mode":"topology-experiment","topology":{"name":"lab","action":"update"},` +
		`"experiment":{"name":"exp","action":"update"}}`

	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("lab")
	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: "6f1c2b8e-3d4a-4c5b-9e7f-0a1b2c3d4e5f", Kind: bdoc.NodeKindDevice, Label: "aa",
		Device: &bdoc.Device{Hostname: "aa", Spec: includeNode("aa"), Interfaces: []bdoc.InterfaceHandle{}},
	})

	draft := createBuilderPublishDraft(t, harness, document)
	published, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"lab","action":"create"},`+
			`"experiment":{"name":"exp","action":"create"}}`, http.StatusOK)

	// An app pins aa while the next update configures the experiment, so
	// the experiment records its digest with the pin.
	harness.configuring = func(name string) {
		setExperimentSpec(harness, name, "schedules", map[string]any{"aa": "compute1"})
	}

	draft = editBuilderDraft(t, harness, published.Draft, document, "bb")
	published, _ = publishBuilderDraft(t, harness, draft, update, http.StatusOK)

	if published.RemovedHostPins != nil {
		t.Errorf("publishing with bb added: removedHostPins = %v, want none", published.RemovedHostPins)
	}

	harness.configuring = nil
	draft = renameBuilderDevice(t, harness, published.Draft, document, "aa", "aa-renamed")
	published, _ = publishBuilderDraft(t, harness, draft, update, http.StatusOK)

	want := []bapi.HostPin{{Hostname: "aa", Host: "compute1", RenamedTo: "aa-renamed"}}
	if diff := cmp.Diff(want, published.RemovedHostPins); diff != "" {
		t.Errorf("publishing with aa renamed: removedHostPins mismatch (-want +got):\n%s", diff)
	}
}

// issuesContain reports whether issues holds want.
func issuesContain(issues []bdoc.Issue, want bdoc.Issue) bool {
	for _, issue := range issues {
		if cmp.Equal(issue, want) {
			return true
		}
	}

	return false
}

// mustBuilderConfig returns a config the harness stores.
func mustBuilderConfig(t *testing.T, harness *builderHarness, name string) *store.Config {
	t.Helper()

	config, err := harness.getConfig(name)
	if err != nil {
		t.Fatalf("config %s missing: %v", name, err)
	}

	return config
}
