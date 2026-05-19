package store

import "testing"

func TestNewConfigKindCaseInsensitive(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		expectKind string
	}{
		{name: "lowercase", input: "topology/test-topo", expectKind: "Topology"},
		{name: "capitalized", input: "Topology/test-topo", expectKind: "Topology"},
		{name: "uppercase", input: "TOPOLOGY/test-topo", expectKind: "Topology"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := NewConfig(tt.input)
			if err != nil {
				t.Fatalf("NewConfig(%q) returned error: %v", tt.input, err)
			}

			if cfg.Kind != tt.expectKind {
				t.Fatalf("NewConfig(%q) kind = %q, want %q", tt.input, cfg.Kind, tt.expectKind)
			}
		})
	}
}

func TestConfigFullNameKindCaseInsensitive(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		expect string
	}{
		{name: "single-arg lowercase", input: []string{"topology/test-topo"}, expect: "Topology/test-topo"},
		{name: "single-arg uppercase", input: []string{"TOPOLOGY/test-topo"}, expect: "Topology/test-topo"},
		{name: "two-arg lowercase", input: []string{"topology", "test-topo"}, expect: "Topology/test-topo"},
		{name: "two-arg uppercase", input: []string{"TOPOLOGY", "test-topo"}, expect: "Topology/test-topo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConfigFullName(tt.input...)
			if got != tt.expect {
				t.Fatalf("ConfigFullName(%v) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestNewConfigRejectsUnknownKind(t *testing.T) {
	_, err := NewConfig("notakind/test")
	if err == nil {
		t.Fatal("NewConfig should reject unknown kinds")
	}
}
