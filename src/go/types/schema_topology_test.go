package types_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"phenix/store"
	"phenix/types"
	"phenix/types/version"
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

func withDelayTimer(node map[string]any, timer string) map[string]any {
	node["delay"] = map[string]any{"timer": timer}
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
			name:    "1-character hostname is rejected",
			node:    withHostname(validNode(), "a"),
			wantErr: true,
		},
		{
			name: "2-character hostname is valid",
			node: withHostname(validNode(), "a1"),
		},
		{
			name: "63-character hostname is valid",
			node: withHostname(validNode(), strings.Repeat("a", 63)),
		},
		{
			name: "delay timer in minutes is valid",
			node: withDelayTimer(validNode(), "5m"),
		},
		{
			// The Go structs write an unset timer as timer:"".
			name: "empty delay timer is valid",
			node: withDelayTimer(validNode(), ""),
		},
		{
			name:    "delay timer without a unit is rejected",
			node:    withDelayTimer(validNode(), "5"),
			wantErr: true,
		},
		{
			name:    "delay timer with an unknown unit is rejected",
			node:    withDelayTimer(validNode(), "1x"),
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
			name: "printer node type is rejected",
			node: func() map[string]any {
				n := validNode()
				n["type"] = "Printer"
				return n
			}(),
			wantErr: true,
		},
		{
			name: "server node type is rejected",
			node: func() map[string]any {
				n := validNode()
				n["type"] = "Server"
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

// schemaTopologyHostnameOK is the documented hostname rule: 2 to 63 ASCII letters, digits and
// hyphens, starting and ending with a letter or digit.
func schemaTopologyHostnameOK(hostname string) bool {
	alnum := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }

	if len(hostname) < 2 || len(hostname) > 63 || !alnum(hostname[0]) || !alnum(hostname[len(hostname)-1]) {
		return false
	}

	for i := range len(hostname) {
		if !alnum(hostname[i]) && hostname[i] != '-' {
			return false
		}
	}

	return true
}

// genSchemaTopologyHostname draws hostnames of letters, digits and hyphens near the length
// limits as well as short ones; a third of them get one other character.
func genSchemaTopologyHostname() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		chars := rapid.SampledFrom([]*rapid.Generator[string]{
			rapid.SampledFrom([]string{"a", "Z", "7"}),
			rapid.SampledFrom([]string{"a", "Z", "7", "-"}),
		}).Draw(t, "chars")
		length := rapid.OneOf(rapid.IntRange(0, 4), rapid.IntRange(61, 65)).Draw(t, "length")

		hostname := make([]string, length)
		for i := range hostname {
			hostname[i] = chars.Draw(t, "char")
		}

		if length > 0 && rapid.IntRange(0, 2).Draw(t, "odd") == 0 {
			odd := rapid.SampledFrom([]string{"_", ".", "é", " "}).Draw(t, "odd char")
			hostname[rapid.IntRange(0, length-1).Draw(t, "at")] = odd
		}

		return strings.Join(hostname, "")
	})
}

// The Topology schema accepts a node's hostname exactly when the documented rule does.
func TestTopologySchemaHostnameProperties(t *testing.T) {
	t.Parallel()

	schema, err := version.GetVersionedValidatorForKind("Topology", version.LATEST_VERSION)
	if err != nil {
		t.Fatalf("GetVersionedValidatorForKind(Topology, %s) error = %v", version.LATEST_VERSION, err)
	}

	rapid.Check(t, func(t *rapid.T) {
		hostname := genSchemaTopologyHostname().Draw(t, "hostname")
		spec := map[string]any{"nodes": []any{withHostname(validNode(), hostname)}}

		if err, want := schema.VisitJSON(spec), schemaTopologyHostnameOK(hostname); (err == nil) != want {
			t.Fatalf("Topology schema on hostname %q (%d bytes) error = %v, want valid %t", hostname, len(hostname), err, want)
		}
	})
}

// genSchemaTopologyTimer draws delay timers from a few parts of Go duration
// syntax, mixed with parts it rejects; the numbers stay too small to overflow.
func genSchemaTopologyTimer() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		var timer strings.Builder

		timer.WriteString(rapid.SampledFrom([]string{"", "+", "-"}).Draw(t, "sign"))

		for range rapid.IntRange(0, 3).Draw(t, "parts") {
			timer.WriteString(rapid.SampledFrom([]string{"0", "1", "12", "1.5", ".5", "5.", ".", " "}).Draw(t, "number"))
			timer.WriteString(rapid.SampledFrom([]string{"ns", "us", "µs", "μs", "ms", "s", "m", "h", "", "x", "S"}).Draw(t, "unit"))
		}

		return timer.String()
	})
}

// The Topology schema accepts a delay timer exactly when it is empty or
// [time.ParseDuration] reads it, as the timer delay does.
func TestTopologySchemaDelayTimerProperties(t *testing.T) {
	t.Parallel()

	schema, err := version.GetVersionedValidatorForKind("Topology", version.LATEST_VERSION)
	if err != nil {
		t.Fatalf("GetVersionedValidatorForKind(Topology, %s) error = %v", version.LATEST_VERSION, err)
	}

	rapid.Check(t, func(t *rapid.T) {
		timer := genSchemaTopologyTimer().Draw(t, "timer")
		spec := map[string]any{"nodes": []any{withDelayTimer(validNode(), timer)}}

		_, parseErr := time.ParseDuration(timer)
		if err, want := schema.VisitJSON(spec), timer == "" || parseErr == nil; (err == nil) != want {
			t.Fatalf("Topology schema on delay timer %q error = %v, want valid %t", timer, err, want)
		}
	})
}
