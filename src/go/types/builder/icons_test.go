package builder_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

func TestIconKeysRegistry(t *testing.T) {
	// The keys of the built-in icons, as catalog.js ICON_KEYS lists them.
	want := []string{
		"alpine", "android", "apple", "blackhat", "bsd", "cell-tower", "centos",
		"container", "desktop", "distribution", "external", "fedora", "firewall",
		"fox", "hacker", "human", "kali", "laptop", "linux", "lizard", "minimega",
		"ntp", "opensuse", "pfsense", "phenix", "plc", "printer", "redhat",
		"relay", "robot", "router", "rtac", "rtu", "server", "smartphone",
		"switch", "terminal", "transmission", "ubuntu", "vlan", "windows",
		"wireshark", "zeek",
	}

	if diff := cmp.Diff(want, builder.IconKeys()); diff != "" {
		t.Errorf("IconKeys() mismatch (-want +got):\n%s", diff)
	}

	for _, key := range want {
		if !builder.IsIconKey(key) {
			t.Errorf("IsIconKey(%q) = false, want true", key)
		}
	}

	if builder.IsIconKey("") {
		t.Error(`IsIconKey("") = true, want false`)
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
					t.Errorf("Validate() with device iconKey %q error = %v, want nil", test.key, err)
				}

				return
			}

			if err == nil {
				t.Fatalf("Validate() with device iconKey %q error = nil, want an error", test.key)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Errorf("Validate() with device iconKey %q error = %q, want one containing %q", test.key, err.Error(), test.wantMsg)
			}

			if !strings.Contains(err.Error(), "iconKey") {
				t.Errorf("Validate() with device iconKey %q error = %q, want one naming the iconKey path", test.key, err.Error())
			}
		})
	}
}

// A group's icon key is drawn from the same registry as a device's.
func TestValidateGroupIconKeys(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantMsg string
	}{
		{name: "empty is the default", key: ""},
		{name: "registry key", key: "container"},
		{name: "unknown key", key: "rack", wantMsg: `unknown icon key "rack"`},
		{name: "remote url", key: "https://example.com/icon.svg", wantMsg: "not a URL or path"},
		{name: "data uri", key: "data:image/png;base64,AAAA", wantMsg: "not a URL or path"},
		{name: "a custom icon id", key: iconFixtureID, wantMsg: "not a URL or path"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			doc.NodeByID(idGrpRack).Group.IconKey = test.key

			err := doc.Validate()

			if test.wantMsg == "" {
				if err != nil {
					t.Errorf("Validate() with group iconKey %q error = %v, want nil", test.key, err)
				}

				return
			}

			if err == nil {
				t.Fatalf("Validate() with group iconKey %q error = nil, want an error", test.key)
			}

			if want := "nodes[0].group.iconKey: "; !strings.Contains(err.Error(), want) {
				t.Errorf("Validate() with group iconKey %q error = %q, want one at %q", test.key, err.Error(), want)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Errorf("Validate() with group iconKey %q error = %q, want one containing %q", test.key, err.Error(), test.wantMsg)
			}
		})
	}
}

func TestGeneratedIconKeysAreRegistryMembers(t *testing.T) {
	doc, _, err := builder.FromConfig(loadConfig(t, "topology.json"))
	if err != nil {
		t.Fatalf("FromConfig(topology.json): %v", err)
	}

	for _, node := range doc.DeviceNodes() {
		key := node.Device.IconKey
		if key != "" && !builder.IsIconKey(key) {
			t.Errorf("FromConfig(topology.json) device %q iconKey = %q, want a registry key", node.Device.Hostname, key)
		}
	}
}

// The front end derives icon keys from the same table (catalog.contract.test.js), so a
// node gets the same icon whether it was made in the editor or generated.
func TestIconKeyForSpecMatchesFrontEnd(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "icon-keys.json"))
	if err != nil {
		t.Fatalf("reading icon key table: %v", err)
	}

	var table struct {
		Cases []struct {
			Spec    map[string]any `json:"spec"`
			IconKey string         `json:"iconKey"`
		} `json:"cases"`
	}

	if err := json.Unmarshal(data, &table); err != nil {
		t.Fatalf("decoding icon key table: %v", err)
	}

	for _, test := range table.Cases {
		spec, err := json.Marshal(test.Spec)
		if err != nil {
			t.Fatalf("encoding spec: %v", err)
		}

		t.Run(string(spec), func(t *testing.T) {
			got := builder.IconKeyForSpec(test.Spec)
			if got == "" {
				// No key is the front end's default icon.
				got = builder.IconServer
			}

			if got != test.IconKey {
				t.Errorf("IconKeyForSpec(%s) = %q, want %q", spec, got, test.IconKey)
			}
		})
	}
}
