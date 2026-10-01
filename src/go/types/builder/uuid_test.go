package builder_test

import (
	"strings"
	"testing"

	"phenix/types/builder"
)

func TestDeterministicIDsAreUUIDs(t *testing.T) {
	// One key for every kind, so the kinds must keep their IDs apart, and
	// handles whose parts join to the same text, or differ only by index.
	generated := map[string]string{
		"DocumentID":                builder.DocumentID("example"),
		"DeviceNodeID":              builder.DeviceNodeID("example"),
		"SwitchNodeID":              builder.SwitchNodeID("example"),
		"NetworkID":                 builder.NetworkID("example"),
		"NoteNodeID":                builder.NoteNodeID("example"),
		"InterfaceHandleID":         builder.InterfaceHandleID("example", "eth0", 0),
		"InterfaceHandleID index 1": builder.InterfaceHandleID("example", "eth0", 1),
		"InterfaceHandleID joined":  builder.InterfaceHandleID("exampleeth0", "", 0),
		"EdgeID": builder.EdgeID(
			builder.DeviceNodeID("example"),
			builder.InterfaceHandleID("example", "eth0", 0),
			builder.SwitchNodeID("example"),
			"",
		),
		"NamespaceUUID": builder.NamespaceUUID(),
	}

	seen := map[string]string{}

	for name, id := range generated {
		if !builder.IsUUID(id) {
			t.Fatalf("%s returned %q, which is not a valid UUID", name, id)
		}

		if id[14] != '5' {
			t.Fatalf("%s returned %q, which is not a version 5 UUID", name, id)
		}

		if variant := id[19]; !strings.ContainsRune("89ab", rune(variant)) {
			t.Fatalf("%s returned %q, which has variant nibble %q", name, id, string(variant))
		}

		if other, ok := seen[id]; ok {
			t.Fatalf("%s and %s both returned %q", name, other, id)
		}

		seen[id] = name
	}
}

func TestDeterministicIDsAreStable(t *testing.T) {
	if got, want := builder.DeviceNodeID("router"), builder.DeviceNodeID("ROUTER"); got != want {
		t.Fatalf("device IDs differ by case: %q != %q", got, want)
	}

	if builder.DeviceNodeID("router") == builder.DeviceNodeID("router2") {
		t.Fatal("distinct hostnames produced the same ID")
	}

	namespace := builder.NamespaceUUID()

	if again := builder.NamespaceUUID(); again != namespace {
		t.Fatalf("namespace UUID is not stable: %q != %q", again, namespace)
	}
}

func TestIsUUID(t *testing.T) {
	valid := []string{
		"49d876d1-571a-5b5c-91b7-aadf5bb5209c",
		"49D876D1-571A-5B5C-91B7-AADF5BB5209C",
		"7f9c2ba4-1e3b-4b1f-9f2e-2b7a5c1d8e4a",
	}

	for _, value := range valid {
		if !builder.IsUUID(value) {
			t.Fatalf("IsUUID(%q) = false", value)
		}
	}

	invalid := []string{
		"",
		"dev-router",
		"49d876d1571a5b5c91b7aadf5bb5209c",
		"49d876d1-571a-5b5c-91b7-aadf5bb5209",
		"49d876d1-571a-5b5c-91b7-aadf5bb5209cc",
		"49d876d1_571a_5b5c_91b7_aadf5bb5209c",
		"49d876d1-571a-0b5c-91b7-aadf5bb5209c", // version 0
		"49d876d1-571a-9b5c-91b7-aadf5bb5209c", // version 9
		"49d876d1-571a-5b5c-11b7-aadf5bb5209c", // wrong variant
		"00000000-0000-0000-0000-000000000000",
		"zzzzzzzz-571a-5b5c-91b7-aadf5bb5209c",
	}

	for _, value := range invalid {
		if builder.IsUUID(value) {
			t.Fatalf("IsUUID(%q) = true", value)
		}
	}
}

func TestGeneratedDocumentsUseUUIDs(t *testing.T) {
	doc, _ := documentFromConfig(t, loadConfig(t, "experiment.json"))

	if !builder.IsUUID(doc.ID) {
		t.Fatalf("document ID %q is not a UUID", doc.ID)
	}

	for _, node := range doc.Nodes {
		if !builder.IsUUID(node.ID) {
			t.Fatalf("node ID %q is not a UUID", node.ID)
		}

		if node.Device == nil {
			continue
		}

		for _, handle := range node.Device.Interfaces {
			if !builder.IsUUID(handle.ID) {
				t.Fatalf("handle ID %q is not a UUID", handle.ID)
			}
		}
	}

	for _, network := range doc.Networks {
		if !builder.IsUUID(network.ID) {
			t.Fatalf("network ID %q is not a UUID", network.ID)
		}
	}

	for _, edge := range doc.Edges {
		if !builder.IsUUID(edge.ID) {
			t.Fatalf("edge ID %q is not a UUID", edge.ID)
		}
	}
}
