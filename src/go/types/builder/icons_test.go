package builder_test

import (
	"slices"
	"strings"
	"testing"

	"phenix/types/builder"
)

func TestIconKeysRegistry(t *testing.T) {
	keys := builder.IconKeys()

	for _, want := range []string{
		"linux", "windows", "redhat", "centos", "router", "firewall",
		"printer", "external", "vlan", "server", "desktop", "switch", "container",
	} {
		if !slices.Contains(keys, want) {
			t.Fatalf("icon key %q missing from registry %v", want, keys)
		}

		if !builder.IsIconKey(want) {
			t.Fatalf("IsIconKey(%q) = false", want)
		}
	}

	if !slices.IsSorted(keys) {
		t.Fatalf("icon keys are not sorted: %v", keys)
	}

	if builder.IsIconKey("") {
		t.Fatal("the empty icon key must not be a registry member")
	}

	keys[0] = "mutated"

	if slices.Contains(builder.IconKeys(), "mutated") {
		t.Fatal("IconKeys returned a shared slice")
	}
}

func TestValidateIconKeys(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantMsg string
	}{
		{name: "empty is the default", key: ""},
		{name: "registry key", key: "firewall"},
		{name: "unknown key", key: "toaster", wantMsg: "unknown icon key"},
		{name: "remote url", key: "https://example.com/icon.svg", wantMsg: "not a URL or path"},
		{name: "absolute path", key: "/assets/icon.svg", wantMsg: "not a URL or path"},
		{name: "relative path", key: "./icon.svg", wantMsg: "not a URL or path"},
		{name: "traversal", key: "..icon", wantMsg: "not a URL or path"},
		{name: "windows path", key: `c:\icons\a.svg`, wantMsg: "not a URL or path"},
		{name: "data uri", key: "data:image/png;base64,AAAA", wantMsg: "not a URL or path"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")

			device := nodeByHostname(t, doc, "router").Device
			device.IconKey = test.key

			err := doc.Validate()

			if test.wantMsg == "" {
				if err != nil {
					t.Fatalf("icon key %q was rejected: %v", test.key, err)
				}

				return
			}

			if err == nil {
				t.Fatalf("icon key %q was accepted", test.key)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.wantMsg)
			}

			if !strings.Contains(err.Error(), "iconKey") {
				t.Fatalf("error %q does not name the iconKey path", err.Error())
			}
		})
	}
}

func TestGeneratedIconKeysAreRegistryMembers(t *testing.T) {
	doc, _, err := builder.FromConfig(loadConfig(t, "topology.json"))
	if err != nil {
		t.Fatalf("generating document: %v", err)
	}

	for _, node := range doc.DeviceNodes() {
		key := node.Device.IconKey
		if key != "" && !builder.IsIconKey(key) {
			t.Fatalf("device %q has icon key %q outside the registry",
				node.Device.Hostname, key)
		}
	}
}
