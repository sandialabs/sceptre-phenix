package types_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	ifaces "phenix/types/interfaces"
)

func topologyNode(hostname string) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": hostname},
		"hardware": map[string]any{"os_type": "linux"},
	}
}

func topoConfig(name string, includes []string, hostnames ...string) store.Config {
	nodes := make([]any, len(hostnames))
	for i, h := range hostnames {
		nodes[i] = topologyNode(h)
	}

	spec := map[string]any{"nodes": nodes}
	if includes != nil {
		spec["includeTopologies"] = includes
	}

	return store.Config{
		Version:  store.APIGroup + "/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     spec,
	}
}

func storeTopology(t *testing.T, c store.Config) {
	t.Helper()

	if err := store.Create(&c); err != nil {
		t.Fatalf("store.Create(%s) error = %v", c.Metadata.Name, err)
	}
}

func nodeHostnames(spec ifaces.TopologySpec) []string {
	names := make([]string, 0, len(spec.Nodes()))
	for _, n := range spec.Nodes() {
		names = append(names, n.General().Hostname())
	}

	return names
}

func TestDecodeTopologyFromConfig(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	storeTopology(t, topoConfig("base", nil, "router"))
	storeTopology(t, topoConfig("middle", []string{"base"}, "fw"))
	storeTopology(t, topoConfig("dup", nil, "web"))
	storeTopology(t, topoConfig("cycle-a", []string{"cycle-b"}, "a"))
	storeTopology(t, topoConfig("cycle-b", []string{"cycle-a"}, "b"))
	storeTopology(t, store.Config{
		Version:  store.APIGroup + "/v9",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "future"},
	})

	tests := []struct {
		name    string
		config  store.Config
		want    []string
		wantErr bool
	}{
		{name: "no includes", config: topoConfig("plain", nil, "web", "db"), want: []string{"web", "db"}},
		{name: "nested includes", config: topoConfig("top", []string{"middle"}, "web"), want: []string{"web", "fw", "router"}},
		{name: "duplicate hostname in include", config: topoConfig("top", []string{"dup"}, "web"), wantErr: true},
		{name: "cyclic include", config: topoConfig("cycle-a", []string{"cycle-b"}, "a"), wantErr: true},
		{name: "self include", config: topoConfig("base", []string{"base"}, "x"), wantErr: true},
		{name: "missing include", config: topoConfig("top", []string{"missing"}, "web"), wantErr: true},
		{name: "include of unknown version", config: topoConfig("top", []string{"future"}, "web"), wantErr: true},
		{
			name:    "unknown version",
			config:  store.Config{Version: store.APIGroup + "/v2", Kind: "Topology"},
			wantErr: true,
		},
		{
			name:    "kind without a versioned spec",
			config:  store.Config{Version: store.APIGroup + "/v1", Kind: "Image"},
			wantErr: true,
		},
		{
			name:    "spec that is not a topology",
			config:  store.Config{Version: store.APIGroup + "/v1", Kind: "Node", Spec: map[string]any{}},
			wantErr: true,
		},
		{
			name:    "spec that does not decode",
			config:  store.Config{Version: store.APIGroup + "/v1", Kind: "Topology", Spec: map[string]any{"nodes": 5}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := types.DecodeTopologyFromConfig(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Errorf("DecodeTopologyFromConfig(%s) = %v, want an error", tt.config.Metadata.Name, nodeHostnames(got))
				}

				return
			}

			if err != nil {
				t.Fatalf("DecodeTopologyFromConfig(%s) error = %v", tt.config.Metadata.Name, err)
			}

			if diff := cmp.Diff(tt.want, nodeHostnames(got)); diff != "" {
				t.Errorf("DecodeTopologyFromConfig(%s) hostnames (-want +got):\n%s", tt.config.Metadata.Name, diff)
			}
		})
	}
}

func TestDecodeTopologyFromConfigFileInclude(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	path := filepath.Join(t.TempDir(), "child.yaml")
	body := `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: child
spec:
  nodes:
  - type: VirtualMachine
    general:
      hostname: from-file
    hardware:
      os_type: linux
`

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := types.DecodeTopologyFromConfig(topoConfig("top", []string{path}, "web"))
	if err != nil {
		t.Fatalf("DecodeTopologyFromConfig() error = %v", err)
	}

	if diff := cmp.Diff([]string{"web", "from-file"}, nodeHostnames(got)); diff != "" {
		t.Errorf("DecodeTopologyFromConfig() hostnames (-want +got):\n%s", diff)
	}
}

func TestLoadTopology(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)
	storeTopology(t, topoConfig("stored", nil, "web"))

	dir := t.TempDir()

	file := filepath.Join(dir, "file.yaml")
	if err := os.WriteFile(file, []byte("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("kind: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		source   string
		wantName string
		wantErr  bool
	}{
		{name: "from store", source: "stored", wantName: "stored"},
		{name: "from file", source: file, wantName: "file"},
		{name: "file is not YAML", source: bad, wantErr: true},
		{name: "path is a directory", source: dir, wantErr: true},
		{name: "not found", source: "missing", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := types.LoadTopology(tt.source)
			if tt.wantErr {
				if err == nil {
					t.Errorf("LoadTopology(%s) = %+v, want an error", tt.source, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("LoadTopology(%s) error = %v", tt.source, err)
			}

			if got.Kind != "Topology" || got.Metadata.Name != tt.wantName {
				t.Errorf("LoadTopology(%s) = %s/%s, want Topology/%s", tt.source, got.Kind, got.Metadata.Name, tt.wantName)
			}
		})
	}
}
