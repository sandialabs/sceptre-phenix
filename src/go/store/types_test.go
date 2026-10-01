package store

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
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

// asJSON is value as JSON, so values read from YAML and made in Go compare
// equal when they hold the same numbers.
func asJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}

	return string(data)
}

// A value whose strings yaml.v3 reads back as they are is written as
// yaml.Marshal writes it, and each string it would not read back is double
// quoted, map keys included.
func TestExactYAMLMarshalsLikeYAML(t *testing.T) {
	t.Parallel()

	plain := map[string]any{
		"nodes": []any{map[string]any{
			"a10": 1.5, "a9": true, "b": nil, "multi": "one\ntwo\n", "x": []string{"y"}, "n": 3,
		}},
		"quoted":   []any{"true", "", " lead", "#hash", "a b", "a \nb", "a\n\tb"},
		"empty":    map[string]any{},
		"none":     []any{},
		"nilMap":   map[string]any(nil),
		"nilSlice": []string(nil),
		"strings":  map[string]string{"k": "v"},
	}

	want, err := yaml.Marshal(plain)
	if err != nil {
		t.Fatalf("yaml.Marshal returned error: %v", err)
	}

	got, err := yaml.Marshal(ExactYAML(plain))
	if err != nil || string(got) != string(want) {
		t.Fatalf("YAML = %v\n%s\nwant\n%s", err, got, want)
	}

	changed := map[string]any{
		"\nkey": []any{"\n", "\n a", "\ta\nb"}, "k": map[string]string{"v": "\n\n a\n"}, "a\nb": "c\n",
	}

	got, err = yaml.Marshal(ExactYAML(changed))
	if err != nil {
		t.Fatalf("yaml.Marshal returned error: %v", err)
	}

	var loaded any
	if err := yaml.Unmarshal(got, &loaded); err != nil {
		t.Fatalf("YAML does not load: %v\n%s", err, got)
	}

	if asJSON(t, loaded) != asJSON(t, changed) {
		t.Fatalf("YAML loads as %s, want %s\n%s", asJSON(t, loaded), asJSON(t, changed), got)
	}
}

// TestConfigYAMLLoadsAsTheConfig writes configs as YAML: one whose
// description, labels, annotations, spec and status strings start with a
// line break or a tab loads as the config, and one without such strings is
// written as yaml.Marshal writes its fields.
func TestConfigYAMLLoadsAsTheConfig(t *testing.T) {
	t.Parallel()

	// fieldsOnly is a Config without its MarshalYAML.
	type fieldsOnly Config

	config := func(text string) Config {
		return Config{
			Version: "phenix.sandia.gov/v2",
			Kind:    "Scenario",
			Metadata: ConfigMetadata{
				Name: "exact", Created: "2026-09-29T10:00:00Z", Updated: "",
				Annotations: Annotations{"note": text, "plain": "a"},
			},
			Spec: map[string]any{
				"description": text,
				"apps": []any{map[string]any{
					"name":   "app",
					"labels": map[string]string{"role": text},
					"hosts":  []any{map[string]any{"hostname": "h", "metadata": map[string]any{"script": text, "n": 2}}},
				}},
			},
			Status: map[string]any{"message": text},
		}
	}

	for _, text := range []string{"\n", "\nfirst", "\n\n  indented\n", "\tfirst\nsecond", "\n\tfirst"} {
		want := config(text)

		for _, value := range []any{want, &want} {
			data, err := yaml.Marshal(value)
			if err != nil {
				t.Fatalf("yaml.Marshal returned error: %v", err)
			}

			var loaded Config
			if err := yaml.Unmarshal(data, &loaded); err != nil {
				t.Fatalf("YAML of %q does not load: %v\n%s", text, err, data)
			}

			if asJSON(t, loaded) != asJSON(t, want) {
				t.Fatalf("YAML of %q loads as %s, want %s\n%s", text, asJSON(t, loaded), asJSON(t, want), data)
			}
		}
	}

	empty := Config{Version: "phenix.sandia.gov/v1", Kind: "Role", Metadata: ConfigMetadata{Name: "r"}}
	emptyMaps := empty
	emptyMaps.Metadata.Annotations, emptyMaps.Spec, emptyMaps.Status = Annotations{}, map[string]any{}, map[string]any{}

	for _, c := range []Config{config("one\ntwo\n"), config("plain"), empty, emptyMaps} {
		want, err := yaml.Marshal(fieldsOnly(c))
		if err != nil {
			t.Fatalf("yaml.Marshal returned error: %v", err)
		}

		got, err := yaml.Marshal(c)
		if err != nil || string(got) != string(want) {
			t.Fatalf("YAML = %v\n%s\nwant\n%s", err, got, want)
		}
	}
}
