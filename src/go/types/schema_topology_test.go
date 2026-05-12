package types_test

import (
	"errors"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types"
)

// validNode returns the minimal internal (minimega) node the topology schema
// accepts. Tests clone and mutate it to exercise individual constraints.
func validNode() map[string]any {
	return map[string]any{
		"type":    "VirtualMachine",
		"general": map[string]any{"hostname": "valid-node"},
		"hardware": map[string]any{
			"os_type": "linux",
			"drives":  []any{map[string]any{"image": "ubuntu.qc2"}},
		},
	}
}

// staticInterface returns a minimal static interface the schema accepts.
func staticInterface() map[string]any {
	return map[string]any{
		"name":    "eth0",
		"vlan":    "EXP",
		"type":    "ethernet",
		"proto":   "static",
		"address": "10.0.0.2",
		"mask":    24,
	}
}

func topologyConfig(node map[string]any) store.Config {
	return store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Topology",
		Metadata: store.ConfigMetadata{
			Name: "schema-test",
		},
		Spec: map[string]any{
			"nodes": []any{node},
		},
	}
}

func withHostname(node map[string]any, hostname string) map[string]any {
	node["general"].(map[string]any)["hostname"] = hostname
	return node
}

// TestTopologySchema is a regression guard on the embedded OpenAPI schema. It
// documents constraints the schema is expected to enforce so a hand-edit that
// drops one is caught here rather than in the field.
func TestTopologySchema(t *testing.T) {
	tests := []struct {
		name    string
		node    map[string]any
		wantErr bool
	}{
		{
			name: "baseline node is valid",
			node: validNode(),
		},
		{
			name:    "hostname over 63 chars is rejected",
			node:    withHostname(validNode(), strings.Repeat("a", 64)),
			wantErr: true,
		},
		{
			name:    "hostname with underscore is rejected",
			node:    withHostname(validNode(), "bad_node"),
			wantErr: true,
		},
		{
			name:    "hostname with leading hyphen is rejected",
			node:    withHostname(validNode(), "-node"),
			wantErr: true,
		},
		{
			name:    "hostname with trailing hyphen is rejected",
			node:    withHostname(validNode(), "node-"),
			wantErr: true,
		},
		{
			name:    "empty hostname is rejected",
			node:    withHostname(validNode(), ""),
			wantErr: true,
		},
		{
			name: "invalid vm_type is rejected",
			node: func() map[string]any {
				n := validNode()
				n["general"].(map[string]any)["vm_type"] = "bogus"
				return n
			}(),
			wantErr: true,
		},
		{
			name: "invalid os_type is rejected",
			node: func() map[string]any {
				n := validNode()
				n["hardware"].(map[string]any)["os_type"] = "bogus"
				return n
			}(),
			wantErr: true,
		},
		{
			name: "node with no drives is rejected",
			node: func() map[string]any {
				n := validNode()
				n["hardware"].(map[string]any)["drives"] = []any{}
				return n
			}(),
			wantErr: true,
		},
		{
			name: "static interface is valid",
			node: func() map[string]any {
				n := validNode()
				n["network"] = map[string]any{"interfaces": []any{staticInterface()}}
				return n
			}(),
		},
		{
			name: "mask above 32 is rejected",
			node: func() map[string]any {
				n := validNode()
				iface := staticInterface()
				iface["mask"] = 33
				n["network"] = map[string]any{"interfaces": []any{iface}}
				return n
			}(),
			wantErr: true,
		},
		{
			name: "malformed MAC is rejected",
			node: func() map[string]any {
				n := validNode()
				iface := staticInterface()
				iface["mac"] = "not-a-mac"
				n["network"] = map[string]any{"interfaces": []any{iface}}
				return n
			}(),
			wantErr: true,
		},
		{
			name: "well-formed MAC is accepted",
			node: func() map[string]any {
				n := validNode()
				iface := staticInterface()
				iface["mac"] = "00:11:22:33:44:55"
				n["network"] = map[string]any{"interfaces": []any{iface}}
				return n
			}(),
		},
		{
			// An interface with no MAC round-trips through the Go structs as
			// mac:"" (the field has no omitempty), so the pattern must accept "".
			name: "empty MAC is accepted",
			node: func() map[string]any {
				n := validNode()
				iface := staticInterface()
				iface["mac"] = ""
				n["network"] = map[string]any{"interfaces": []any{iface}}
				return n
			}(),
		},
		{
			name: "ruleset name with invalid characters is rejected",
			node: func() map[string]any {
				n := validNode()
				iface := staticInterface()
				iface["ruleset_in"] = "bad name!"
				n["network"] = map[string]any{"interfaces": []any{iface}}
				return n
			}(),
			wantErr: true,
		},
		{
			name: "valid ruleset name is accepted",
			node: func() map[string]any {
				n := validNode()
				iface := staticInterface()
				iface["ruleset_in"] = "In-From_Inet"
				n["network"] = map[string]any{"interfaces": []any{iface}}
				return n
			}(),
		},
		{
			// Same round-trip concern as the empty MAC case above.
			name: "empty ruleset name is accepted",
			node: func() map[string]any {
				n := validNode()
				iface := staticInterface()
				iface["ruleset_in"] = ""
				n["network"] = map[string]any{"interfaces": []any{iface}}
				return n
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := types.ValidateConfigSpec(topologyConfig(tt.node))

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected a validation error, got nil")
				}

				if !errors.Is(err, types.ErrValidationFailed) {
					t.Fatalf("expected ErrValidationFailed, got %v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
