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
