package store

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"phenix/types/version"
)

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

// genTypesKindSpelling spells a stored kind in any mix of cases.
func genTypesKindSpelling(kind string) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		var b strings.Builder

		for _, r := range kind {
			if rapid.Bool().Draw(t, "upper") {
				b.WriteString(strings.ToUpper(string(r)))
			} else {
				b.WriteString(strings.ToLower(string(r)))
			}
		}

		return b.String()
	})
}

// NewConfig and ConfigFullName agree on which names are configs: a stored kind in any case,
// "/", and a name with no "/" in it. Both name the config by its canonical kind.
func TestConfigNameProperties(t *testing.T) {
	t.Parallel()

	kinds := slices.Sorted(maps.Keys(version.StoredVersion))

	rapid.Check(t, func(t *rapid.T) {
		kind := rapid.SampledFrom(kinds).Draw(t, "kind")
		spelled := genTypesKindSpelling(kind).Draw(t, "spelled")

		if rapid.IntRange(0, 4).Draw(t, "unknown") == 0 {
			kind, spelled = "", spelled+rapid.SampledFrom([]string{"s", "x", "_"}).Draw(t, "suffix")
		}

		name := rapid.StringMatching(`[a-z/-]{0,4}`).Draw(t, "name")
		full := spelled + "/" + name

		want := ""
		if kind != "" && !strings.Contains(name, "/") {
			want = kind + "/" + name
		}

		if got := ConfigFullName(full); got != want {
			t.Fatalf("ConfigFullName(%q) = %q, want %q", full, got, want)
		}

		if want != "" {
			if got := ConfigFullName(spelled, name); got != kind+"/"+name {
				t.Fatalf("ConfigFullName(%q, %q) = %q, want %q", spelled, name, got, kind+"/"+name)
			}
		}

		c, err := NewConfig(full)
		if (err == nil) != (want != "") {
			t.Fatalf("NewConfig(%q) error = %v, want error %t", full, err, want == "")
		}

		if err == nil && (c.FullName() != want || c.APIVersion() != version.StoredVersion[kind] || c.APIGroup() != APIGroup) {
			t.Fatalf("NewConfig(%q) = %s %s, want %s at %s/%s", full, c.FullName(), c.Version, want, APIGroup, version.StoredVersion[kind])
		}
	})
}
