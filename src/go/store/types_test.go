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

// TestWorkflowIsNotAStoredKind proves the Workflow schema did not make
// workflow configs addressable in the store.
func TestWorkflowIsNotAStoredKind(t *testing.T) {
	for _, name := range []string{"workflow/wf", "Workflow/wf", "WORKFLOW/wf"} {
		if _, err := NewConfig(name); err == nil {
			t.Errorf("NewConfig(%q) returned no error", name)
		}

		if got := ConfigFullName(name); got != "" {
			t.Errorf("ConfigFullName(%q) = %q, want empty", name, got)
		}
	}

	if got := ConfigFullName("Workflow", "wf"); got != "" {
		t.Errorf(`ConfigFullName("Workflow", "wf") = %q, want empty`, got)
	}
}

// Clients cannot set a config's revision: the store assigns it.
func TestNewConfigClearsRevision(t *testing.T) {
	body := `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"a","revision":7}}`

	c, err := NewConfigFromJSON([]byte(body))
	if err != nil {
		t.Errorf("NewConfigFromJSON(revision 7) error = %v", err)
	} else if c.Metadata.Revision != 0 {
		t.Errorf("NewConfigFromJSON(revision 7) revision = %d, want 0", c.Metadata.Revision)
	}

	c, err = NewConfigFromYAML([]byte("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: a\n  revision: 7\n"))
	if err != nil {
		t.Errorf("NewConfigFromYAML(revision 7) error = %v", err)
	} else if c.Metadata.Revision != 0 {
		t.Errorf("NewConfigFromYAML(revision 7) revision = %d, want 0", c.Metadata.Revision)
	}
}
