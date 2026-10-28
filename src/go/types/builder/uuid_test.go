package builder_test

import (
	"regexp"
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
		if other, ok := seen[id]; ok {
			t.Errorf("%s = %q, want an ID distinct from %s, which returned it too", name, id, other)
		}

		seen[id] = name

		if !builder.IsUUID(id) {
			t.Errorf("%s = %q, want a valid UUID", name, id)

			continue
		}

		if id[14] != '5' {
			t.Errorf("%s = %q, want a version 5 UUID", name, id)
		}

		if variant := id[19]; !strings.ContainsRune("89ab", rune(variant)) {
			t.Errorf("%s = %q with variant nibble %q, want 8, 9, a or b", name, id, string(variant))
		}
	}
}

func TestDeterministicIDsAreStable(t *testing.T) {
	if got, want := builder.DeviceNodeID("ROUTER"), builder.DeviceNodeID("router"); got != want {
		t.Errorf("DeviceNodeID(ROUTER) = %q, want DeviceNodeID(router) = %q", got, want)
	}

	if got := builder.DeviceNodeID("router2"); got == builder.DeviceNodeID("router") {
		t.Errorf("DeviceNodeID(router2) = %q, want an ID distinct from DeviceNodeID(router)", got)
	}

	namespace := builder.NamespaceUUID()

	if again := builder.NamespaceUUID(); again != namespace {
		t.Errorf("NamespaceUUID() = %q, then %q, want the same each time", namespace, again)
	}
}

// TestIsUUID also holds the schema's identifier pattern to IsUUID.
func TestIsUUID(t *testing.T) {
	identifier := mapAt(t, mapAt(t, mustSchema(t), "$defs"), "identifier")
	pattern, _ := identifier["pattern"].(string)
	matcher := regexp.MustCompile(pattern)

	if identifier["format"] != "uuid" {
		t.Errorf("schema identifier format = %v, want uuid", identifier["format"])
	}

	for _, test := range []struct {
		desc, value string
		want        bool
	}{
		{desc: "the namespace", value: builder.NamespaceUUID(), want: true},
		{desc: "version 5", value: "49d876d1-571a-5b5c-91b7-aadf5bb5209c", want: true},
		{desc: "upper case", value: "49D876D1-571A-5B5C-91B7-AADF5BB5209C", want: true},
		{desc: "version 4", value: "7f9c2ba4-1e3b-4b1f-9f2e-2b7a5c1d8e4a", want: true},
		{desc: "empty", value: "", want: false},
		{desc: "a legacy ID", value: "dev-router", want: false},
		{desc: "no hyphens", value: "49d876d1571a5b5c91b7aadf5bb5209c", want: false},
		{desc: "one digit short", value: "49d876d1-571a-5b5c-91b7-aadf5bb5209", want: false},
		{desc: "one digit long", value: "49d876d1-571a-5b5c-91b7-aadf5bb5209cc", want: false},
		{desc: "underscores", value: "49d876d1_571a_5b5c_91b7_aadf5bb5209c", want: false},
		{desc: "version 0", value: "49d876d1-571a-0b5c-91b7-aadf5bb5209c", want: false},
		{desc: "version 9", value: "49d876d1-571a-9b5c-91b7-aadf5bb5209c", want: false},
		{desc: "wrong variant", value: "49d876d1-571a-5b5c-11b7-aadf5bb5209c", want: false},
		{desc: "nil UUID", value: "00000000-0000-0000-0000-000000000000", want: false},
		{desc: "not hexadecimal", value: "zzzzzzzz-571a-5b5c-91b7-aadf5bb5209c", want: false},
	} {
		t.Run(test.desc, func(t *testing.T) {
			if got := builder.IsUUID(test.value); got != test.want {
				t.Errorf("IsUUID(%q) = %t, want %t", test.value, got, test.want)
			}

			if got := matcher.MatchString(test.value); got != test.want {
				t.Errorf("schema identifier pattern matches %q = %t, want %t", test.value, got, test.want)
			}
		})
	}
}

// TestUUIDv5KnownIDs pins the IDs that UUIDv5 returns, because stored
// documents hold them.
func TestUUIDv5KnownIDs(t *testing.T) {
	const (
		dnsNamespace = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
		urlNamespace = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	)

	for _, test := range []struct {
		desc, namespace, name, want string
	}{
		{desc: "RFC 4122 DNS example", namespace: dnsNamespace, name: "www.example.com", want: "2ed6657d-e927-568b-95e1-2665a8aea6a2"},
		{desc: "the Builder namespace", namespace: urlNamespace, name: builder.SchemaURI, want: "75128dad-cb9e-5d9b-b969-a021ae8f9103"},
		{desc: "upper case namespace", namespace: strings.ToUpper(urlNamespace), name: builder.SchemaURI, want: "75128dad-cb9e-5d9b-b969-a021ae8f9103"},
		{desc: "a device", namespace: builder.NamespaceUUID(), name: "device:router", want: "ae220341-9afe-5f17-91f5-c7ea1f960b1e"},
		{desc: "an empty name", namespace: builder.NamespaceUUID(), name: "", want: "f792c93a-02e4-57e5-97a9-c066bfb2e56b"},
		{desc: "a name that is not ASCII", namespace: builder.NamespaceUUID(), name: "réseau-Ω", want: "4a006a40-41c3-5a65-95aa-61a1e48c211f"},
		{desc: "a namespace that is not a UUID", namespace: "not-a-uuid", name: "x", want: ""},
		{desc: "the nil namespace", namespace: "00000000-0000-0000-0000-000000000000", name: "x", want: ""},
	} {
		t.Run(test.desc, func(t *testing.T) {
			if got := builder.UUIDv5(test.namespace, test.name); got != test.want {
				t.Errorf("UUIDv5(%q, %q) = %q, want %q", test.namespace, test.name, got, test.want)
			}
		})
	}

	if got, want := builder.NamespaceUUID(), "75128dad-cb9e-5d9b-b969-a021ae8f9103"; got != want {
		t.Errorf("NamespaceUUID() = %q, want %q", got, want)
	}
}
