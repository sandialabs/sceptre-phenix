package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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
				t.Errorf("NewConfig(%q) kind = %q, want %q", tt.input, cfg.Kind, tt.expectKind)
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
				t.Errorf("ConfigFullName(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestNewConfigRejectsUnknownKind(t *testing.T) {
	if _, err := NewConfig("notakind/test"); err == nil {
		t.Error(`NewConfig("notakind/test") error = nil, want an error`)
	}
}

// asJSON is value as decoded JSON, so values read from YAML and made in Go
// compare equal when they hold the same numbers.
func asJSON(t *testing.T, value any) any {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}

	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decoding the JSON of %T: %v", value, err)
	}

	return decoded
}

// ExactYAML writes what yaml.Marshal writes, but double quotes each string
// (map keys included) that yaml.v3 would not read back as it is.
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

	if got, err := yaml.Marshal(ExactYAML(plain)); err != nil {
		t.Errorf("yaml.Marshal(ExactYAML(plain)) error = %v", err)
	} else if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("yaml.Marshal(ExactYAML(plain)) mismatch with yaml.Marshal(plain) (-want +got):\n%s", diff)
	}

	changed := map[string]any{
		"\nkey": []any{"\n", "\n a", "\ta\nb"}, "k": map[string]string{"v": "\n\n a\n"}, "a\nb": "c\n",
	}

	got, err := yaml.Marshal(ExactYAML(changed))
	if err != nil {
		t.Fatalf("yaml.Marshal(ExactYAML(changed)) error = %v", err)
	}

	var loaded any
	if err := yaml.Unmarshal(got, &loaded); err != nil {
		t.Fatalf("YAML does not load: %v\n%s", err, got)
	}

	if diff := cmp.Diff(asJSON(t, changed), asJSON(t, loaded)); diff != "" {
		t.Errorf("yaml.Marshal(ExactYAML(changed)) loads back mismatched (-want +got):\n%s\n%s", diff, got)
	}
}

// TestConfigYAMLLoadsAsTheConfig checks that strings starting with a line break or
// tab survive YAML, and that other configs are written as yaml.Marshal writes them.
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

	for _, test := range []struct{ name, text string }{
		{name: "a line break", text: "\n"},
		{name: "a line break then text", text: "\nfirst"},
		{name: "blank lines then indented text", text: "\n\n  indented\n"},
		{name: "a tab then lines", text: "\tfirst\nsecond"},
		{name: "a line break then a tab", text: "\n\tfirst"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			want := config(test.text)

			for _, value := range []any{want, &want} {
				data, err := yaml.Marshal(value)
				if err != nil {
					t.Errorf("yaml.Marshal(%T) error = %v", value, err)

					continue
				}

				var loaded Config
				if err := yaml.Unmarshal(data, &loaded); err != nil {
					t.Errorf("YAML of %T does not load: %v\n%s", value, err, data)

					continue
				}

				if diff := cmp.Diff(asJSON(t, want), asJSON(t, loaded)); diff != "" {
					t.Errorf("YAML of %T loads back mismatched (-want +got):\n%s\n%s", value, diff, data)
				}
			}
		})
	}

	empty := Config{Version: "phenix.sandia.gov/v1", Kind: "Role", Metadata: ConfigMetadata{Name: "r"}}
	emptyMaps := empty
	emptyMaps.Metadata.Annotations, emptyMaps.Spec, emptyMaps.Status = Annotations{}, map[string]any{}, map[string]any{}

	for _, test := range []struct {
		name   string
		config Config
	}{
		{name: "lines", config: config("one\ntwo\n")},
		{name: "plain text", config: config("plain")},
		{name: "empty", config: empty},
		{name: "empty maps", config: emptyMaps},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			want, err := yaml.Marshal(fieldsOnly(test.config))
			if err != nil {
				t.Fatalf("yaml.Marshal(fieldsOnly) error = %v", err)
			}

			got, err := yaml.Marshal(test.config)
			if err != nil {
				t.Fatalf("yaml.Marshal(Config) error = %v", err)
			}

			if diff := cmp.Diff(string(want), string(got)); diff != "" {
				t.Errorf("yaml.Marshal(Config) mismatch with yaml.Marshal(fieldsOnly) (-want +got):\n%s", diff)
			}
		})
	}
}

// The config of a topology that references a Builder document, as a user
// writes it in JSON and in YAML.
const (
	structuredReference = `{"digest":"sha256:aa","id":"bb","path":"/phenix/topologies/site/builder.yaml"}`

	structuredJSON = `{
		"apiVersion": "phenix.sandia.gov/v1",
		"kind": "Topology",
		"metadata": {
			"name": "site",
			"annotations": {
				"keep": "v",
				"builder-doc": {"path": "/phenix/topologies/site/builder.yaml", "id": "bb", "digest": "sha256:aa"}
			}
		},
		"spec": {"nodes": []}
	}`

	structuredYAML = `
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: site
  annotations:
    keep: v
    builder-doc:
      path: /phenix/topologies/site/builder.yaml
      id: bb
      digest: sha256:aa
spec:
  nodes: []
`
)

// wireAnnotations returns the annotations of a config's JSON or YAML as any
// other program reads them.
func wireAnnotations(t *testing.T, data []byte, unmarshal func([]byte, any) error) map[string]any {
	t.Helper()

	var wire struct {
		Metadata struct {
			Annotations map[string]any `json:"annotations" yaml:"annotations"`
		} `json:"metadata" yaml:"metadata"`
	}

	if err := unmarshal(data, &wire); err != nil {
		t.Fatalf("output does not load: %v\n%s", err, data)
	}

	return wire.Metadata.Annotations
}

// TestStructuredAnnotationIsAMapOnTheWire checks that a builder-doc map in JSON or
// YAML is one string in memory and is written back as the map.
func TestStructuredAnnotationIsAMapOnTheWire(t *testing.T) {
	t.Parallel()

	fromJSON, err := DecodeConfigJSON([]byte(structuredJSON))
	if err != nil {
		t.Fatalf("DecodeConfigJSON returned error: %v", err)
	}

	fromYAML, err := NewConfigFromYAML([]byte(structuredYAML))
	if err != nil {
		t.Fatalf("NewConfigFromYAML returned error: %v", err)
	}

	want := Annotations{"keep": "v", "builder-doc": structuredReference}

	for name, config := range map[string]*Config{"JSON": fromJSON, "YAML": fromYAML} {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(want, config.Metadata.Annotations); diff != "" {
				t.Errorf("annotations loaded from %s mismatch (-want +got):\n%s", name, diff)
			}
		})
	}

	nested := map[string]any{
		"keep": "v",
		"builder-doc": map[string]any{
			"digest": "sha256:aa", "id": "bb", "path": "/phenix/topologies/site/builder.yaml",
		},
	}

	// The config and a pointer to it write the map, and what they write
	// loads as the config.
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "config", value: *fromJSON},
		{name: "pointer", value: fromYAML},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			jsonOut, err := json.Marshal(test.value)
			if err != nil {
				t.Fatalf("json.Marshal(%T) error = %v", test.value, err)
			}

			yamlOut, err := yaml.Marshal(test.value)
			if err != nil {
				t.Fatalf("yaml.Marshal(%T) error = %v", test.value, err)
			}

			if diff := cmp.Diff(nested, wireAnnotations(t, jsonOut, json.Unmarshal)); diff != "" {
				t.Errorf("json.Marshal(%T) annotations mismatch (-want +got):\n%s\n%s", test.value, diff, jsonOut)
			}

			if diff := cmp.Diff(nested, wireAnnotations(t, yamlOut, yaml.Unmarshal)); diff != "" {
				t.Errorf("yaml.Marshal(%T) annotations mismatch (-want +got):\n%s\n%s", test.value, diff, yamlOut)
			}

			var back Config
			if err := json.Unmarshal(jsonOut, &back); err != nil {
				t.Errorf("JSON of %T does not load: %v\n%s", test.value, err, jsonOut)
			} else if diff := cmp.Diff(*fromJSON, back); diff != "" {
				t.Errorf("JSON of %T loads back mismatched (-want +got):\n%s", test.value, diff)
			}

			back = Config{}
			if err := yaml.Unmarshal(yamlOut, &back); err != nil {
				t.Errorf("YAML of %T does not load: %v\n%s", test.value, err, yamlOut)
			} else if diff := cmp.Diff(fromJSON.Metadata, back.Metadata); diff != "" {
				t.Errorf("YAML of %T loads back mismatched metadata (-want +got):\n%s", test.value, diff)
			}
		})
	}

	// So do the metadata and the annotations on their own, which other
	// resources embed.
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "metadata", value: fromJSON.Metadata},
		{name: "annotations", value: fromJSON.Metadata.Annotations},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			wrap := map[string]any{"annotations": test.value}
			if metadata, ok := test.value.(ConfigMetadata); ok {
				wrap = map[string]any{"metadata": metadata}
			}

			jsonOut, err := json.Marshal(wrap)
			if err != nil {
				t.Fatalf("json.Marshal(%T) error = %v", test.value, err)
			}

			yamlOut, err := yaml.Marshal(wrap)
			if err != nil {
				t.Fatalf("yaml.Marshal(%T) error = %v", test.value, err)
			}

			for encoding, got := range map[string]map[string]any{
				"JSON": decodeAny(t, jsonOut, json.Unmarshal), "YAML": decodeAny(t, yamlOut, yaml.Unmarshal),
			} {
				if metadata, ok := got["metadata"].(map[string]any); ok {
					got = metadata
				}

				if diff := cmp.Diff(any(nested), got["annotations"]); diff != "" {
					t.Errorf("%s of %T annotations mismatch (-want +got):\n%s", encoding, test.value, diff)
				}
			}
		})
	}

	// The YAML is written as a block of plain text sub-keys.
	yamlOut, err := yaml.Marshal(fromYAML)
	if err != nil {
		t.Fatalf("yaml.Marshal(config) error = %v", err)
	}

	block := "        builder-doc:\n            digest: sha256:aa\n            id: bb\n" +
		"            path: /phenix/topologies/site/builder.yaml\n        keep: v\n"
	if !strings.Contains(string(yamlOut), block) {
		t.Errorf("yaml.Marshal(config) = \n%s\nwant it to hold\n%s", yamlOut, block)
	}
}

// decodeAny loads JSON or YAML as any other program reads it.
func decodeAny(t *testing.T, data []byte, unmarshal func([]byte, any) error) map[string]any {
	t.Helper()

	var value map[string]any
	if err := unmarshal(data, &value); err != nil {
		t.Fatalf("output does not load: %v\n%s", err, data)
	}

	return value
}

// TestStructuredAnnotationValues: a structured annotation is a map of text or a
// string (kept as is). Any other value fails with an error naming the annotation.
func TestStructuredAnnotationValues(t *testing.T) {
	t.Parallel()

	config := func(annotations string) string {
		return `{"metadata": {"name": "site", "annotations": ` + annotations + `}}`
	}

	for _, test := range []struct {
		name string
		// json and yaml are the annotations in each encoding.
		json, yaml string
		// memory is the string held for builder-doc.
		memory string
		// wire is what builder-doc is written back as.
		wire any
	}{
		{
			name: "a map", json: `{"builder-doc": {"id": "bb"}}`, yaml: "builder-doc:\n  id: bb\n",
			memory: `{"id":"bb"}`, wire: map[string]any{"id": "bb"},
		},
		{
			name: "an unknown sub-key", json: `{"builder-doc": {"file": "/x"}}`, yaml: "builder-doc:\n  file: /x\n",
			memory: `{"file":"/x"}`, wire: map[string]any{"file": "/x"},
		},
		{
			name: "an empty map", json: `{"builder-doc": {}}`, yaml: "builder-doc: {}\n",
			memory: `{}`, wire: map[string]any{},
		},
		{
			name: "the string of a map", json: `{"builder-doc": "{\"id\": \"bb\"}"}`, yaml: `builder-doc: '{"id": "bb"}'` + "\n",
			memory: `{"id": "bb"}`, wire: map[string]any{"id": "bb"},
		},
		{
			name: "a string that is not JSON", json: `{"builder-doc": "{"}`, yaml: `builder-doc: "{"` + "\n",
			memory: `{`, wire: `{`,
		},
		{
			name: "an empty string", json: `{"builder-doc": ""}`, yaml: `builder-doc: ""` + "\n",
			memory: ``, wire: ``,
		},
		{
			name: "a string with a null sub-key", json: `{"builder-doc": "{\"id\":null}"}`, yaml: `builder-doc: '{"id":null}'` + "\n",
			memory: `{"id":null}`, wire: `{"id":null}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var fromJSON, fromYAML Config

			if err := json.Unmarshal([]byte(config(test.json)), &fromJSON); err != nil {
				t.Fatalf("JSON does not load: %v", err)
			}

			yamlConfig := "metadata:\n  name: site\n  annotations:\n    " + strings.ReplaceAll(test.yaml, "\n  ", "\n      ")
			if err := yaml.Unmarshal([]byte(yamlConfig), &fromYAML); err != nil {
				t.Fatalf("YAML does not load: %v\n%s", err, yamlConfig)
			}

			for encoding, loaded := range map[string]Config{"JSON": fromJSON, "YAML": fromYAML} {
				if got := loaded.Metadata.Annotations["builder-doc"]; got != test.memory {
					t.Errorf("builder-doc loaded from %s = %q, want %q", encoding, got, test.memory)
				}

				if jsonOut, err := json.Marshal(loaded); err != nil {
					t.Errorf("json.Marshal(config from %s) error = %v", encoding, err)
				} else if diff := cmp.Diff(test.wire, wireAnnotations(t, jsonOut, json.Unmarshal)["builder-doc"]); diff != "" {
					t.Errorf("json.Marshal(config from %s) builder-doc mismatch (-want +got):\n%s\n%s", encoding, diff, jsonOut)
				}

				yamlOut, err := yaml.Marshal(loaded)
				if err != nil {
					t.Errorf("yaml.Marshal(config from %s) error = %v", encoding, err)

					continue
				}

				if diff := cmp.Diff(test.wire, wireAnnotations(t, yamlOut, yaml.Unmarshal)["builder-doc"]); diff != "" {
					t.Errorf("yaml.Marshal(config from %s) builder-doc mismatch (-want +got):\n%s\n%s", encoding, diff, yamlOut)
				}

				// What is written loads as what was loaded.
				var back Config
				if err := yaml.Unmarshal(yamlOut, &back); err != nil {
					t.Errorf("YAML of the config from %s does not load: %v\n%s", encoding, err, yamlOut)
				} else if diff := cmp.Diff(asJSON(t, loaded.Metadata.Annotations), asJSON(t, back.Metadata.Annotations)); diff != "" {
					t.Errorf("YAML of the config from %s loads back mismatched (-want +got):\n%s\n%s", encoding, diff, yamlOut)
				}
			}
		})
	}

	// A YAML scalar that is not text becomes its text, in a sub-key as in a
	// plain annotation.
	var coerced Config
	if err := yaml.Unmarshal([]byte("metadata:\n  annotations:\n    plain: 42\n    builder-doc:\n      id: 42\n"), &coerced); err != nil {
		t.Errorf("YAML with numbers does not load: %v", err)
	} else if diff := cmp.Diff(Annotations{"plain": "42", "builder-doc": `{"id":"42"}`}, coerced.Metadata.Annotations); diff != "" {
		t.Errorf("annotations of YAML with numbers mismatch (-want +got):\n%s", diff)
	}

	// An alias to a map is the map.
	var aliased Config

	withAlias := "ref: &ref\n  id: bb\nmetadata:\n  annotations:\n    builder-doc: *ref\n"
	if err := yaml.Unmarshal([]byte(withAlias), &aliased); err != nil {
		t.Errorf("YAML with an alias does not load: %v", err)
	} else if got := aliased.Metadata.Annotations["builder-doc"]; got != `{"id":"bb"}` {
		t.Errorf("builder-doc of an aliased map = %q, want %q", got, `{"id":"bb"}`)
	}

	for name, test := range map[string]struct{ json, yaml, key string }{
		"a map under another annotation":  {json: `{"note": {"a": "b"}}`, yaml: "note:\n      a: b\n", key: "note"},
		"a list under another annotation": {json: `{"note": ["a"]}`, yaml: "note: [a]\n", key: "note"},
		"a number in JSON":                {json: `{"note": 42}`, yaml: "note: [42]\n", key: "note"},
		"a list":                          {json: `{"builder-doc": ["a"]}`, yaml: "builder-doc: [a]\n", key: "builder-doc"},
		"a number":                        {json: `{"builder-doc": 42}`, yaml: "builder-doc: [42]\n", key: "builder-doc"},
		"a list sub-key":                  {json: `{"builder-doc": {"id": ["a"]}}`, yaml: "builder-doc:\n      id: [a]\n", key: "builder-doc"},
		"a map sub-key":                   {json: `{"builder-doc": {"id": {"a": "b"}}}`, yaml: "builder-doc:\n      id:\n        a: b\n", key: "builder-doc"},
		"a number sub-key in JSON":        {json: `{"builder-doc": {"id": 42}}`, yaml: "builder-doc:\n      id: [42]\n", key: "builder-doc"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var loaded Config

			err := json.Unmarshal([]byte(config(test.json)), &loaded)
			if err == nil || !strings.Contains(err.Error(), "annotation "+test.key) {
				t.Errorf("json.Unmarshal(%s) error = %v, want one naming annotation %s", test.json, err, test.key)
			}

			yamlConfig := "metadata:\n  annotations:\n    " + test.yaml

			err = yaml.Unmarshal([]byte(yamlConfig), &loaded)
			if err == nil || !strings.Contains(err.Error(), "annotation "+test.key) {
				t.Errorf("yaml.Unmarshal(%q) error = %v, want one naming annotation %s", test.yaml, err, test.key)
			}
		})
	}

	// Of several annotations that do not load, the first in key order is named.
	for range 8 {
		var several Config

		failed := false

		err := json.Unmarshal([]byte(config(`{"z": 1, "builder-doc": 2, "a": 3, "m": "text"}`)), &several)
		if err == nil || !strings.Contains(err.Error(), "annotation a:") {
			t.Errorf("json.Unmarshal(several bad annotations) error = %v, want one naming annotation a", err)

			failed = true
		}

		err = yaml.Unmarshal([]byte("metadata:\n  annotations:\n    z: [1]\n    builder-doc: [2]\n    a: [3]\n    m: text\n"), &several)
		if err == nil || !strings.Contains(err.Error(), "annotation a:") {
			t.Errorf("yaml.Unmarshal(several bad annotations) error = %v, want one naming annotation a", err)

			failed = true
		}

		if failed {
			break
		}
	}

	// Annotations that are not a map at all.
	var loaded Config
	if err := json.Unmarshal([]byte(config(`["a"]`)), &loaded); err == nil {
		t.Error("json.Unmarshal(a list of annotations) error = nil, want an error")
	}

	if err := yaml.Unmarshal([]byte("metadata:\n  annotations: [a]\n"), &loaded); err == nil {
		t.Error("yaml.Unmarshal(a list of annotations) error = nil, want an error")
	}

	if _, err := NewConfigFromYAML([]byte("metadata:\n  annotations:\n    note:\n      a: b\n")); !errors.Is(err, ErrInvalidFormat) {
		t.Errorf("NewConfigFromYAML(a map under note) error = %v, want ErrInvalidFormat", err)
	}
}

// TestAnnotationsDecodeIntoExistingConfig checks that decoding into a loaded config
// merges annotations as a plain map does, and a null leaves none.
func TestAnnotationsDecodeIntoExistingConfig(t *testing.T) {
	t.Parallel()

	loaded := func() Config {
		return Config{
			Metadata: ConfigMetadata{Name: "site", Annotations: Annotations{"old": "x", "builder-doc": `{"id":"aa"}`}},
			Spec:     map[string]any{"gone": 1},
		}
	}

	// plainConfig decodes annotations into a plain map of strings.
	type plainConfig struct {
		Metadata struct {
			Name        string            `json:"name"        yaml:"name"`
			Annotations map[string]string `json:"annotations" yaml:"annotations"`
		} `json:"metadata" yaml:"metadata"`
	}

	for _, test := range []struct {
		name       string
		json, yaml string
		want       Annotations
	}{
		{
			name: "another key", json: `{"metadata": {"annotations": {"new": "y"}}}`,
			yaml: "metadata:\n  annotations:\n    new: y\n",
			want: Annotations{"old": "x", "new": "y", "builder-doc": `{"id":"aa"}`},
		},
		{
			name: "the structured key", json: `{"metadata": {"annotations": {"builder-doc": {"path": "/b.yaml"}}}}`,
			yaml: "metadata:\n  annotations:\n    builder-doc:\n      path: /b.yaml\n",
			want: Annotations{"old": "x", "builder-doc": `{"path":"/b.yaml"}`},
		},
		{
			name: "no annotations", json: `{"metadata": {"name": "site"}}`, yaml: "metadata:\n  name: site\n",
			want: Annotations{"old": "x", "builder-doc": `{"id":"aa"}`},
		},
		{
			name: "an empty map", json: `{"metadata": {"annotations": {}}}`, yaml: "metadata:\n  annotations: {}\n",
			want: Annotations{"old": "x", "builder-doc": `{"id":"aa"}`},
		},
		{
			name: "null", json: `{"metadata": {"annotations": null}}`, yaml: "metadata:\n  annotations:\n",
			want: nil,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			for encoding, decode := range map[string]func(any) error{
				"JSON": func(target any) error { return json.Unmarshal([]byte(test.json), target) },
				"YAML": func(target any) error { return yaml.Unmarshal([]byte(test.yaml), target) },
			} {
				config := loaded()
				if err := decode(&config); err != nil {
					t.Errorf("decoding %s into a loaded config error = %v", encoding, err)

					continue
				}

				if diff := cmp.Diff(test.want, config.Metadata.Annotations); diff != "" {
					t.Errorf("annotations decoded from %s mismatch (-want +got):\n%s", encoding, diff)
				}

				// A plain map gives the same keys, where it can decode the text.
				if test.name == "the structured key" {
					continue
				}

				var plain plainConfig

				plain.Metadata.Annotations = map[string]string{"old": "x", "builder-doc": `{"id":"aa"}`}
				if err := decode(&plain); err != nil {
					t.Errorf("decoding %s with plain annotations error = %v", encoding, err)

					continue
				}

				if diff := cmp.Diff(Annotations(plain.Metadata.Annotations), config.Metadata.Annotations); diff != "" {
					t.Errorf("annotations decoded from %s mismatch with a plain map (-plain +got):\n%s", encoding, diff)
				}
			}
		})
	}
}

// TestStoredJSONKeepsAnnotationsAsStrings checks that the stored JSON holds a
// structured annotation as its string and is otherwise the JSON of the config.
func TestStoredJSONKeepsAnnotationsAsStrings(t *testing.T) {
	t.Parallel()

	config, err := NewConfigFromYAML([]byte(structuredYAML))
	if err != nil {
		t.Fatalf("NewConfigFromYAML returned error: %v", err)
	}

	config.Metadata.Created, config.Metadata.Updated = "2026-10-01T10:00:00Z", "2026-10-01T10:00:01Z"
	config.Status = map[string]any{"message": "ok"}

	stored, err := config.StoredJSON()
	if err != nil {
		t.Fatalf("StoredJSON returned error: %v", err)
	}

	var plain struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}

	if err := json.Unmarshal(stored, &plain); err != nil {
		t.Fatalf("the stored JSON does not load with plain annotations: %v\n%s", err, stored)
	}

	if diff := cmp.Diff(map[string]string{"keep": "v", "builder-doc": structuredReference}, plain.Metadata.Annotations); diff != "" {
		t.Errorf("StoredJSON() annotations mismatch (-want +got):\n%s", diff)
	}

	// The JSON of the config itself is not readable that way: that is why the
	// store keeps another.
	wire, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	if err := json.Unmarshal(wire, &plain); err == nil {
		t.Errorf("json.Unmarshal(the JSON of the config, plain annotations) error = nil, want an error\n%s", wire)
	}

	var back Config
	if err := json.Unmarshal(stored, &back); err != nil {
		t.Errorf("the stored JSON does not load: %v", err)
	} else if diff := cmp.Diff(*config, back); diff != "" {
		t.Errorf("the stored JSON loads back mismatched (-want +got):\n%s", diff)
	}

	// Without a structured annotation the stored JSON is the config's JSON,
	// field for field, whatever is empty.
	for _, annotations := range []struct {
		name  string
		value Annotations
	}{
		{name: "nil annotations", value: nil},
		{name: "empty annotations", value: Annotations{}},
		{name: "text annotations", value: Annotations{"keep": "v", "builder-experiment": `{"draftId":"d"}`}},
	} {
		for _, spec := range []struct {
			name  string
			value map[string]any
		}{
			{name: "nil spec", value: nil},
			{name: "a spec", value: map[string]any{"nodes": []any{}}},
		} {
			t.Run(annotations.name+" and "+spec.name, func(t *testing.T) {
				t.Parallel()

				other := *config
				other.Metadata.Annotations, other.Spec, other.Status = annotations.value, spec.value, spec.value

				stored, err := other.StoredJSON()
				if err != nil {
					t.Fatalf("StoredJSON() error = %v", err)
				}

				want, err := json.Marshal(other)
				if err != nil {
					t.Fatalf("json.Marshal(config) error = %v", err)
				}

				if diff := cmp.Diff(string(want), string(stored)); diff != "" {
					t.Errorf("StoredJSON() mismatch with the JSON of the config (-want +got):\n%s", diff)
				}
			})
		}
	}

	// Every field of a config is stored.
	if got, want := reflect.TypeFor[storedConfig]().NumField(), reflect.TypeFor[Config]().NumField(); got != want {
		t.Errorf("storedConfig has %d fields, want %d as Config has", got, want)
	}

	if got, want := reflect.TypeFor[storedMetadata]().NumField(), reflect.TypeFor[ConfigMetadata]().NumField(); got != want {
		t.Errorf("storedMetadata has %d fields, want %d as ConfigMetadata has", got, want)
	}
}

// TestWorkflowIsNotAStoredKind proves the Workflow schema did not make
// workflow configs addressable in the store.
func TestWorkflowIsNotAStoredKind(t *testing.T) {
	for _, test := range []struct{ name, config string }{
		{name: "lowercase", config: "workflow/wf"},
		{name: "capitalized", config: "Workflow/wf"},
		{name: "uppercase", config: "WORKFLOW/wf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewConfig(test.config); err == nil {
				t.Errorf("NewConfig(%q) error = nil, want an error", test.config)
			}

			if got := ConfigFullName(test.config); got != "" {
				t.Errorf("ConfigFullName(%q) = %q, want empty", test.config, got)
			}
		})
	}

	if got := ConfigFullName("Workflow", "wf"); got != "" {
		t.Errorf(`ConfigFullName("Workflow", "wf") = %q, want empty`, got)
	}
}

// TestDecodeConfigKeepsPlaceholders checks that DecodeConfigJSON and
// DecodeConfigYAML keep each ${NAME} placeholder, and that NewConfigFromYAML
// and NewConfigFromFile, which the CLI uses, still expand them.
func TestDecodeConfigKeepsPlaceholders(t *testing.T) {
	t.Setenv("PHENIX_STORE_TEST_SECRET", "server-secret")

	fromFile := func(body []byte) (*Config, error) {
		path := filepath.Join(t.TempDir(), "topology.yml")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) returned error: %v", path, err)
		}

		return NewConfigFromFile(path)
	}

	const (
		set     = "${PHENIX_STORE_TEST_SECRET}"
		unset   = "${PHENIX_STORE_TEST_UNSET:fallback}"
		jsonDoc = `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology",` +
			`"metadata":{"name":"t","annotations":{"set":"` + set + `","unset":"` + unset + `"}}}`
		yamlDoc = "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: t\n" +
			"  annotations:\n    set: \"" + set + "\"\n    unset: \"" + unset + "\"\n"
	)

	verbatim := Annotations{"set": set, "unset": unset}
	expanded := Annotations{"set": "server-secret", "unset": "fallback"}

	tests := []struct {
		name   string
		decode func([]byte) (*Config, error)
		input  string
		want   Annotations
	}{
		{name: "DecodeConfigJSON", decode: DecodeConfigJSON, input: jsonDoc, want: verbatim},
		{name: "DecodeConfigYAML", decode: DecodeConfigYAML, input: yamlDoc, want: verbatim},
		{name: "NewConfigFromYAML", decode: NewConfigFromYAML, input: yamlDoc, want: expanded},
		{name: "NewConfigFromFile", decode: fromFile, input: yamlDoc, want: expanded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := tt.decode([]byte(tt.input))
			if err != nil {
				t.Fatalf("%s(%q) returned error: %v", tt.name, tt.input, err)
			}

			if diff := cmp.Diff(tt.want, config.Metadata.Annotations); diff != "" {
				t.Errorf("%s(%q) annotations mismatch (-want +got):\n%s", tt.name, tt.input, diff)
			}
		})
	}
}

// TestExpandBranchName checks that ExpandBranchName fills in only the
// ${BRANCH_NAME} placeholders and keeps every other one as it is written.
func TestExpandBranchName(t *testing.T) {
	t.Setenv("BRANCH_NAME", "lab")
	t.Setenv("PHENIX_STORE_TEST_SECRET", "server-secret")

	tests := []struct {
		input string
		want  string
	}{
		{input: "${BRANCH_NAME}", want: "lab"},
		{input: "/phenix/injects/${BRANCH_NAME}/motd", want: "/phenix/injects/lab/motd"},
		{input: "${BRANCH_NAME:other}", want: "lab"},
		{input: "${PHENIX_STORE_TEST_SECRET}", want: "${PHENIX_STORE_TEST_SECRET}"},
		{input: "${PHENIX_STORE_TEST_SECRET:x}", want: "${PHENIX_STORE_TEST_SECRET:x}"},
		{input: "$PHENIX_STORE_TEST_SECRET", want: "$PHENIX_STORE_TEST_SECRET"},
		{input: "$BRANCH_NAME", want: "$BRANCH_NAME"},
		{input: "${BRANCH_NAME}-${PHENIX_STORE_TEST_SECRET}", want: "lab-${PHENIX_STORE_TEST_SECRET}"},
	}

	for _, tt := range tests {
		if got := string(ExpandBranchName([]byte(tt.input))); got != tt.want {
			t.Errorf("ExpandBranchName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestExpandBranchNameUnset checks that ExpandBranchName uses the default of
// a ${BRANCH_NAME:default} placeholder when BRANCH_NAME is not set.
func TestExpandBranchNameUnset(t *testing.T) {
	t.Setenv("BRANCH_NAME", "")

	if err := os.Unsetenv("BRANCH_NAME"); err != nil {
		t.Fatalf("os.Unsetenv(BRANCH_NAME) returned error: %v", err)
	}

	tests := []struct {
		input string
		want  string
	}{
		{input: "${BRANCH_NAME:main}", want: "main"},
		{input: "${BRANCH_NAME}", want: ""},
	}

	for _, tt := range tests {
		if got := string(ExpandBranchName([]byte(tt.input))); got != tt.want {
			t.Errorf("ExpandBranchName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
