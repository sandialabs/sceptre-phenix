package builder_test

import (
	"strings"
	"testing"

	"phenix/types/builder"
)

func TestDeterministicIDsAreUUIDs(t *testing.T) {
	generated := map[string]string{
		"DocumentID":        builder.DocumentID("my topology"),
		"DeviceNodeID":      builder.DeviceNodeID("router"),
		"SwitchNodeID":      builder.SwitchNodeID("EXP"),
		"NetworkID":         builder.NetworkID("EXP"),
		"NoteNodeID":        builder.NoteNodeID("note-key"),
		"GroupNodeID":       builder.GroupNodeID("group-key"),
		"InterfaceHandleID": builder.InterfaceHandleID("router", "eth0", 0),
		"EdgeID": builder.EdgeID(
			builder.DeviceNodeID("router"),
			builder.InterfaceHandleID("router", "eth0", 0),
			builder.SwitchNodeID("EXP"),
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

func TestValidateRequiresUUIDIdentifiers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*builder.Document)
		want   string
	}{
		{
			name:   "document",
			mutate: func(d *builder.Document) { d.ID = "doc-fixture" },
			want:   "document ID \"doc-fixture\" is not a valid UUID",
		},
		{
			name:   "node",
			mutate: func(d *builder.Document) { d.Nodes[0].ID = "grp-rack" },
			want:   "node ID \"grp-rack\" is not a valid UUID",
		},
		{
			name:   "network",
			mutate: func(d *builder.Document) { d.Networks[0].ID = "net-exp" },
			want:   "network ID \"net-exp\" is not a valid UUID",
		},
		{
			name:   "edge",
			mutate: func(d *builder.Document) { d.Edges[0].ID = "e-router-eth0" },
			want:   "edge ID \"e-router-eth0\" is not a valid UUID",
		},
		{
			name: "interface handle",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "router").Device.Interfaces[0].ID = "h-router-eth0"
			},
			want: "interface handle ID \"h-router-eth0\" is not a valid UUID",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			test.mutate(doc)

			err := doc.Validate()
			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.want)
			}
		})
	}
}

func TestValidateKeepsCaseInsensitiveDuplicateChecks(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Nodes[0].ID = strings.ToUpper(doc.Nodes[1].ID)

	err := doc.Validate()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), "duplicate node ID") {
		t.Fatalf("error %q does not report a duplicate node ID", err.Error())
	}

	doc = loadDocumentFixture(t, "document.json")
	router := nodeWithHostname(doc, "router")
	host := nodeWithHostname(doc, "host-a")
	host.Device.Interfaces[0].ID = strings.ToUpper(router.Device.Interfaces[0].ID)

	err = doc.Validate()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), "duplicate interface handle ID") {
		t.Fatalf("error %q does not report a duplicate handle ID", err.Error())
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
