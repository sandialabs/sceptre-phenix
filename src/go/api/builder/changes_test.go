package builder

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
)

// changesConfig returns a config of kind named name with the spec and the
// annotations given.
func changesConfig(t *testing.T, kind, name string, spec map[string]any, annotations store.Annotations) *store.Config {
	t.Helper()

	config, err := store.NewConfig(kind + "/" + name)
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	config.Spec = spec
	config.Metadata.Annotations = annotations

	return config
}

// changesNode is a topology node spec named hostname whose drives use the
// images given.
func changesNode(hostname string, images ...string) map[string]any {
	drives := make([]any, 0, len(images))

	for _, image := range images {
		drives = append(drives, map[string]any{"image": image})
	}

	return map[string]any{
		"general":  map[string]any{"hostname": hostname},
		"hardware": map[string]any{"drives": drives},
	}
}

// changesSpec is a topology spec of the nodes given, including the
// topologies given.
func changesSpec(includes []string, nodes ...map[string]any) map[string]any {
	list := make([]any, 0, len(nodes))

	for _, node := range nodes {
		list = append(list, node)
	}

	spec := map[string]any{"nodes": list}
	if includes != nil {
		spec["includeTopologies"] = includes
	}

	return spec
}

func describeChanges(t *testing.T, state PublishState) *PublishChanges {
	t.Helper()

	changes, err := DescribePublishChanges(state)
	if err != nil {
		t.Fatalf("DescribePublishChanges returned error: %v", err)
	}

	return changes
}

func boolPointer(value bool) *bool { return &value }

func intPointer(value int) *int { return &value }

// TestDescribePublishChangesOfANewTopology describes a topology to be created:
// every include and image is added, and the server's images are unknown.
func TestDescribePublishChangesOfANewTopology(t *testing.T) {
	t.Parallel()

	changes := describeChanges(t, PublishState{
		TopologyName: "riverside",
		Spec: changesSpec([]string{"plant-a"},
			changesNode("web-2", "ubuntu.qc2"), changesNode("web-1", "ubuntu.qc2"), changesNode("fw", "vyos.qc2", ""),
		),
		VLANAliases:    map[string]int{"ot": 101},
		StoredTopology: nil,
		TopologyHeld:   false,
		Scenarios:      nil,
		Experiment:     nil,
		ServerImages:   nil,
	})

	want := &PublishChanges{
		Topology:  ConfigChange{Name: "riverside", Action: ChangeCreate},
		Includes:  []IncludeChange{{Name: "plant-a", Change: ItemAdded}},
		Scenarios: []ScenarioAnnotation{},
		Images: []ImageChange{
			{Name: "ubuntu.qc2", Change: ItemAdded, Devices: []string{"web-1", "web-2"}, OnServer: nil},
			{Name: "vyos.qc2", Change: ItemAdded, Devices: []string{"fw"}, OnServer: nil},
		},
		Experiment:  nil,
		VLANAliases: nil,
	}

	if diff := cmp.Diff(want, changes); diff != "" {
		t.Errorf("DescribePublishChanges(new topology) mismatch (-want +got):\n%s", diff)
	}

	// A topology-only publication names no experiment and no VLAN alias, and
	// an image whose presence is unknown says so with null.
	encoded, err := json.Marshal(changes)
	if err != nil {
		t.Fatalf("json.Marshal(changes) returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%s) returned error: %v", encoded, err)
	}

	wantJSON := map[string]any{
		"topology":  map[string]any{"name": "riverside", "action": "create"},
		"includes":  []any{map[string]any{"name": "plant-a", "change": "added"}},
		"scenarios": []any{},
		"images": []any{
			map[string]any{"name": "ubuntu.qc2", "change": "added", "devices": []any{"web-1", "web-2"}, "onServer": nil},
			map[string]any{"name": "vyos.qc2", "change": "added", "devices": []any{"fw"}, "onServer": nil},
		},
	}
	if diff := cmp.Diff(wantJSON, decoded); diff != "" {
		t.Errorf("JSON of DescribePublishChanges(new topology) mismatch (-want +got):\n%s", diff)
	}
}

// TestDescribePublishChangesOfAnUpdate compares the stored includes and images
// with those the publication writes, and names the images the server has.
func TestDescribePublishChangesOfAnUpdate(t *testing.T) {
	t.Parallel()

	stored := changesConfig(t, "Topology", "riverside", changesSpec([]string{"plant-a", "plant-b"},
		changesNode("web-1", "ubuntu.qc2"), changesNode("db", "old.qc2"),
	), nil)

	changes := describeChanges(t, PublishState{
		TopologyName: "riverside",
		Spec: changesSpec([]string{"plant-c", "plant-a"},
			changesNode("web-1", "ubuntu.qc2"), changesNode("web-2", "/phenix/images/centos.qc2"),
		),
		VLANAliases:    nil,
		StoredTopology: stored,
		TopologyHeld:   false,
		Scenarios:      nil,
		Experiment:     nil,
		ServerImages:   []string{"ubuntu.qc2", "centos.qc2"},
	})

	if diff := cmp.Diff(ConfigChange{Name: "riverside", Action: ChangeUpdate}, changes.Topology); diff != "" {
		t.Errorf("DescribePublishChanges(update).Topology mismatch (-want +got):\n%s", diff)
	}

	wantIncludes := []IncludeChange{
		{Name: "plant-a", Change: ItemKept},
		{Name: "plant-b", Change: ItemRemoved},
		{Name: "plant-c", Change: ItemAdded},
	}
	if diff := cmp.Diff(wantIncludes, changes.Includes); diff != "" {
		t.Errorf("DescribePublishChanges(update).Includes mismatch (-want +got):\n%s", diff)
	}

	wantImages := []ImageChange{
		{Name: "/phenix/images/centos.qc2", Change: ItemAdded, Devices: []string{"web-2"}, OnServer: boolPointer(true)},
		{Name: "old.qc2", Change: ItemRemoved, Devices: []string{"db"}, OnServer: boolPointer(false)},
		{Name: "ubuntu.qc2", Change: ItemKept, Devices: []string{"web-1"}, OnServer: boolPointer(true)},
	}
	if diff := cmp.Diff(wantImages, changes.Images); diff != "" {
		t.Errorf("DescribePublishChanges(update).Images mismatch (-want +got):\n%s", diff)
	}

	// A topology that already holds the publication is left as it is.
	held := describeChanges(t, PublishState{
		TopologyName: "riverside", Spec: stored.Spec, VLANAliases: nil, StoredTopology: stored, TopologyHeld: true,
		Scenarios: nil, Experiment: nil, ServerImages: nil,
	})

	wantHeld := &PublishChanges{
		Topology:  ConfigChange{Name: "riverside", Action: ChangeUnchanged},
		Includes:  []IncludeChange{{Name: "plant-a", Change: ItemKept}, {Name: "plant-b", Change: ItemKept}},
		Scenarios: []ScenarioAnnotation{},
		Images: []ImageChange{
			{Name: "old.qc2", Change: ItemKept, Devices: []string{"db"}, OnServer: nil},
			{Name: "ubuntu.qc2", Change: ItemKept, Devices: []string{"web-1"}, OnServer: nil},
		},
		Experiment:  nil,
		VLANAliases: nil,
	}
	if diff := cmp.Diff(wantHeld, held); diff != "" {
		t.Errorf("DescribePublishChanges(held topology) mismatch (-want +got):\n%s", diff)
	}
}

// TestDescribePublishChangesOfScenarios says which listed scenarios gain the
// topology in their annotation, by the names the annotation holds, trimmed.
func TestDescribePublishChangesOfScenarios(t *testing.T) {
	t.Parallel()

	changes := describeChanges(t, PublishState{
		TopologyName: "riverside", Spec: changesSpec(nil), VLANAliases: nil, StoredTopology: nil, TopologyHeld: false,
		Scenarios: []*store.Config{
			changesConfig(t, "Scenario", "water-ops", nil, store.Annotations{"topology": "riverside-old"}),
			changesConfig(t, "Scenario", "drill", nil, store.Annotations{"topology": "other , riverside"}),
			changesConfig(t, "Scenario", "empty", nil, nil),
		},
		Experiment: nil, ServerImages: nil,
	})

	want := []ScenarioAnnotation{
		{Name: "drill", Change: ScenarioUnchanged},
		{Name: "empty", Change: ScenarioAnnotate},
		{Name: "water-ops", Change: ScenarioAnnotate},
	}
	if diff := cmp.Diff(want, changes.Scenarios); diff != "" {
		t.Errorf("DescribePublishChanges(scenarios).Scenarios mismatch (-want +got):\n%s", diff)
	}
}

// TestDescribePublishChangesOfAnExperiment adds every VLAN alias to an
// experiment that is created, and compares those of one that is updated.
func TestDescribePublishChangesOfAnExperiment(t *testing.T) {
	t.Parallel()

	aliases := map[string]int{"ot": 120, "corp": 120, "it": 7}

	created := describeChanges(t, PublishState{
		TopologyName: "riverside", Spec: changesSpec(nil), VLANAliases: aliases, StoredTopology: nil,
		TopologyHeld: false, Scenarios: nil, Experiment: &ExperimentState{Name: "lab", Stored: nil, Held: false},
		ServerImages: nil,
	})

	wantCreated := []VLANAliasChange{
		{Name: "corp", From: nil, To: intPointer(120), Change: ItemAdded},
		{Name: "it", From: nil, To: intPointer(7), Change: ItemAdded},
		{Name: "ot", From: nil, To: intPointer(120), Change: ItemAdded},
	}
	if diff := cmp.Diff(&ConfigChange{Name: "lab", Action: ChangeCreate}, created.Experiment); diff != "" {
		t.Errorf("DescribePublishChanges(new experiment).Experiment mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(wantCreated, created.VLANAliases); diff != "" {
		t.Errorf("DescribePublishChanges(new experiment).VLANAliases mismatch (-want +got):\n%s", diff)
	}

	// Aliases read from JSON are numbers of any kind.
	stored := changesConfig(t, "Experiment", "lab", map[string]any{
		"vlans": map[string]any{"aliases": map[string]any{"ot": float64(101), "dmz": 5, "corp": 120}},
	}, nil)

	updated := describeChanges(t, PublishState{
		TopologyName: "riverside", Spec: changesSpec(nil), VLANAliases: aliases, StoredTopology: nil,
		TopologyHeld: false, Scenarios: nil, Experiment: &ExperimentState{Name: "lab", Stored: stored, Held: false},
		ServerImages: nil,
	})

	wantUpdated := []VLANAliasChange{
		{Name: "corp", From: intPointer(120), To: intPointer(120), Change: ItemKept},
		{Name: "dmz", From: intPointer(5), To: nil, Change: ItemRemoved},
		{Name: "it", From: nil, To: intPointer(7), Change: ItemAdded},
		{Name: "ot", From: intPointer(101), To: intPointer(120), Change: ItemChanged},
	}
	if diff := cmp.Diff(&ConfigChange{Name: "lab", Action: ChangeUpdate}, updated.Experiment); diff != "" {
		t.Errorf("DescribePublishChanges(stored experiment).Experiment mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(wantUpdated, updated.VLANAliases); diff != "" {
		t.Errorf("DescribePublishChanges(stored experiment).VLANAliases mismatch (-want +got):\n%s", diff)
	}

	if updated.RemovedHostPins != nil {
		t.Errorf("DescribePublishChanges(stored experiment).RemovedHostPins = %v, want nil", updated.RemovedHostPins)
	}

	pins := []HostPin{{Hostname: "plc-2", Host: "compute3", RenamedTo: "plc-3"}}
	pruned := describeChanges(t, PublishState{
		TopologyName: "riverside", Spec: changesSpec(nil), VLANAliases: aliases, StoredTopology: nil,
		TopologyHeld: false, Scenarios: nil,
		Experiment:   &ExperimentState{Name: "lab", Stored: stored, Held: false, RemovedHostPins: pins},
		ServerImages: nil,
	})

	if diff := cmp.Diff(pins, pruned.RemovedHostPins); diff != "" {
		t.Errorf("DescribePublishChanges(experiment losing pins).RemovedHostPins mismatch (-want +got):\n%s", diff)
	}

	held := describeChanges(t, PublishState{
		TopologyName: "riverside", Spec: changesSpec(nil), VLANAliases: nil, StoredTopology: nil,
		TopologyHeld: false, Scenarios: nil, Experiment: &ExperimentState{Name: "lab", Stored: stored, Held: true},
		ServerImages: nil,
	})

	if diff := cmp.Diff(&ConfigChange{Name: "lab", Action: ChangeUnchanged}, held.Experiment); diff != "" {
		t.Errorf("DescribePublishChanges(held experiment).Experiment mismatch (-want +got):\n%s", diff)
	}
}
