package builder

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

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

// TestDescribePublishChangesOfANewTopology describes a topology that does not
// exist yet: it is created, with every include and image added, and an image
// says nothing of the server when the server's images are unknown.
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

	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}

	// A topology-only publication names no experiment and no VLAN alias, and
	// an image whose presence is unknown says so with null.
	encoded, err := json.Marshal(changes)
	if err != nil {
		t.Fatalf("encoding the changes: %v", err)
	}

	for _, part := range []string{`"onServer":null`, `"scenarios":[]`} {
		if !strings.Contains(string(encoded), part) {
			t.Errorf("JSON %s does not hold %s", encoded, part)
		}
	}

	for _, part := range []string{`"experiment"`, `"vlanAliases"`} {
		if strings.Contains(string(encoded), part) {
			t.Errorf("JSON %s holds %s, want it left out without an experiment", encoded, part)
		}
	}
}

// TestDescribePublishChangesOfAnUpdate compares the includes and images of
// the stored topology with those the publication writes, and says which
// images the server has, by file name.
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

	if changes.Topology != (ConfigChange{Name: "riverside", Action: ChangeUpdate}) {
		t.Errorf("topology = %+v, want an update", changes.Topology)
	}

	wantIncludes := []IncludeChange{
		{Name: "plant-a", Change: ItemKept},
		{Name: "plant-b", Change: ItemRemoved},
		{Name: "plant-c", Change: ItemAdded},
	}
	if !reflect.DeepEqual(changes.Includes, wantIncludes) {
		t.Errorf("includes = %+v, want %+v", changes.Includes, wantIncludes)
	}

	wantImages := []ImageChange{
		{Name: "/phenix/images/centos.qc2", Change: ItemAdded, Devices: []string{"web-2"}, OnServer: boolPointer(true)},
		{Name: "old.qc2", Change: ItemRemoved, Devices: []string{"db"}, OnServer: boolPointer(false)},
		{Name: "ubuntu.qc2", Change: ItemKept, Devices: []string{"web-1"}, OnServer: boolPointer(true)},
	}
	if !reflect.DeepEqual(changes.Images, wantImages) {
		t.Errorf("images = %s, want %s", asJSON(t, changes.Images), asJSON(t, wantImages))
	}

	// A topology that already holds the publication is left as it is.
	held := describeChanges(t, PublishState{
		TopologyName: "riverside", Spec: stored.Spec, VLANAliases: nil, StoredTopology: stored, TopologyHeld: true,
		Scenarios: nil, Experiment: nil, ServerImages: nil,
	})

	if held.Topology.Action != ChangeUnchanged ||
		slices.ContainsFunc(held.Includes, func(include IncludeChange) bool { return include.Change != ItemKept }) ||
		slices.ContainsFunc(held.Images, func(image ImageChange) bool { return image.Change != ItemKept }) {
		t.Errorf("changes of a held topology = %s, want it unchanged with everything kept", asJSON(t, held))
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
	if !reflect.DeepEqual(changes.Scenarios, want) {
		t.Fatalf("scenarios = %+v, want %+v", changes.Scenarios, want)
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
	if created.Experiment == nil || *created.Experiment != (ConfigChange{Name: "lab", Action: ChangeCreate}) ||
		!reflect.DeepEqual(created.VLANAliases, wantCreated) {
		t.Errorf("created = %s, want experiment lab created with every alias added", asJSON(t, created))
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
	if updated.Experiment == nil || updated.Experiment.Action != ChangeUpdate ||
		!reflect.DeepEqual(updated.VLANAliases, wantUpdated) {
		t.Errorf("updated = %s, want experiment lab updated with %s", asJSON(t, updated), asJSON(t, wantUpdated))
	}

	held := describeChanges(t, PublishState{
		TopologyName: "riverside", Spec: changesSpec(nil), VLANAliases: nil, StoredTopology: nil,
		TopologyHeld: false, Scenarios: nil, Experiment: &ExperimentState{Name: "lab", Stored: stored, Held: true},
		ServerImages: nil,
	})

	if held.Experiment.Action != ChangeUnchanged {
		t.Errorf("held experiment = %+v, want it unchanged", held.Experiment)
	}
}

// TestPublishChangesLines words each change as the Publish dialog does.
func TestPublishChangesLines(t *testing.T) {
	t.Parallel()

	changes := &PublishChanges{
		Topology:  ConfigChange{Name: "riverside", Action: ChangeCreate},
		Includes:  []IncludeChange{{Name: "plant-a", Change: ItemRemoved}, {Name: "plant-b", Change: ItemAdded}},
		Scenarios: []ScenarioAnnotation{{Name: "water-ops", Change: ScenarioAnnotate}, {Name: "drill", Change: ScenarioUnchanged}},
		Images: []ImageChange{
			{Name: "ubuntu.qc2", Change: ItemAdded, Devices: []string{"web-1", "web-2"}, OnServer: boolPointer(false)},
			{
				Name: "centos.qc2", Change: ItemKept, Devices: []string{"a", "b", "c", "d", "e", "f", "g"},
				OnServer: boolPointer(true),
			},
			{Name: "old.qc2", Change: ItemRemoved, Devices: []string{"db"}, OnServer: nil},
		},
		Experiment: &ConfigChange{Name: "lab", Action: ChangeUpdate},
		VLANAliases: []VLANAliasChange{
			{Name: "ot", From: intPointer(101), To: intPointer(120), Change: ItemChanged},
			{Name: "it", From: nil, To: intPointer(7), Change: ItemAdded},
			{Name: "dmz", From: intPointer(5), To: nil, Change: ItemRemoved},
			{Name: "corp", From: intPointer(9), To: intPointer(9), Change: ItemKept},
		},
	}

	want := []string{
		"Creates Topology config riverside",
		"Updates Experiment config lab",
		"Removes included topology plant-a",
		"Adds included topology plant-b",
		"Adds topology riverside to Scenario water-ops",
		"Scenario drill already names topology riverside",
		"Disk image ubuntu.qc2 is new (used by web-1 and web-2); the server does not have it",
		"Disk image centos.qc2 is still used (by a, b, c, d, e and 2 more); the server has it",
		"Disk image old.qc2 is no longer used (was used by db)",
		"VLAN alias for network ot changes from 101 to 120",
		"VLAN alias for network it is set to 7",
		"VLAN alias 5 for network dmz is removed",
		"VLAN alias for network corp stays 9",
	}

	if got := changes.Lines(); !slices.Equal(got, want) {
		t.Fatalf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	unchanged := &PublishChanges{
		Topology: ConfigChange{Name: "riverside", Action: ChangeUnchanged}, Includes: nil, Scenarios: nil,
		Images: nil, Experiment: nil, VLANAliases: nil,
	}

	wantUnchanged := []string{
		"Topology config riverside is unchanged: it already holds this diagram",
		"Nothing outside the Topology changes",
	}
	if got := unchanged.Lines(); !slices.Equal(got, wantUnchanged) {
		t.Fatalf("lines = %q, want %q", got, wantUnchanged)
	}
}

func asJSON(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}

	return string(encoded)
}
