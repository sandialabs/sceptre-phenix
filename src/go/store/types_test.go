package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
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

// TestStructuredAnnotationIsAMapOnTheWire loads a config whose builder-doc
// annotation is a map from JSON and from YAML: both give the same string in
// memory, both encodings write the map back, and what they write loads as
// the config.
func TestStructuredAnnotationIsAMapOnTheWire(t *testing.T) {
	t.Parallel()

	fromJSON, err := NewConfigFromJSON([]byte(structuredJSON))
	if err != nil {
		t.Fatalf("NewConfigFromJSON returned error: %v", err)
	}

	fromYAML, err := NewConfigFromYAML([]byte(structuredYAML))
	if err != nil {
		t.Fatalf("NewConfigFromYAML returned error: %v", err)
	}

	want := Annotations{"keep": "v", "builder-doc": structuredReference}

	for name, config := range map[string]*Config{"JSON": fromJSON, "YAML": fromYAML} {
		if !reflect.DeepEqual(config.Metadata.Annotations, want) {
			t.Fatalf("annotations loaded from %s = %v, want %v", name, config.Metadata.Annotations, want)
		}
	}

	nested := map[string]any{
		"keep": "v",
		"builder-doc": map[string]any{
			"digest": "sha256:aa", "id": "bb", "path": "/phenix/topologies/site/builder.yaml",
		},
	}

	// The config and a pointer to it write the map, and what they write
	// loads as the config.
	for _, value := range []any{*fromJSON, fromYAML} {
		jsonOut, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal(%T) returned error: %v", value, err)
		}

		yamlOut, err := yaml.Marshal(value)
		if err != nil {
			t.Fatalf("yaml.Marshal(%T) returned error: %v", value, err)
		}

		if got := wireAnnotations(t, jsonOut, json.Unmarshal); !reflect.DeepEqual(got, nested) {
			t.Fatalf("JSON of %T has annotations %v, want the nested map\n%s", value, got, jsonOut)
		}

		if got := wireAnnotations(t, yamlOut, yaml.Unmarshal); !reflect.DeepEqual(got, nested) {
			t.Fatalf("YAML of %T has annotations %v, want the nested map\n%s", value, got, yamlOut)
		}

		var back Config
		if err := json.Unmarshal(jsonOut, &back); err != nil || !reflect.DeepEqual(back, *fromJSON) {
			t.Fatalf("JSON of %T loads as %+v (%v), want the config\n%s", value, back, err, jsonOut)
		}

		back = Config{}
		if err := yaml.Unmarshal(yamlOut, &back); err != nil || !reflect.DeepEqual(back.Metadata, fromJSON.Metadata) {
			t.Fatalf("YAML of %T loads as %+v (%v), want the config\n%s", value, back, err, yamlOut)
		}
	}

	// So do the metadata and the annotations on their own, which other
	// resources embed.
	for _, value := range []any{fromJSON.Metadata, fromJSON.Metadata.Annotations} {
		wrap := map[string]any{"annotations": value}
		if metadata, ok := value.(ConfigMetadata); ok {
			wrap = map[string]any{"metadata": metadata}
		}

		jsonOut, err := json.Marshal(wrap)
		if err != nil {
			t.Fatalf("json.Marshal(%T) returned error: %v", value, err)
		}

		yamlOut, err := yaml.Marshal(wrap)
		if err != nil {
			t.Fatalf("yaml.Marshal(%T) returned error: %v", value, err)
		}

		for encoding, got := range map[string]map[string]any{
			"JSON": decodeAny(t, jsonOut, json.Unmarshal), "YAML": decodeAny(t, yamlOut, yaml.Unmarshal),
		} {
			if metadata, ok := got["metadata"].(map[string]any); ok {
				got = metadata
			}

			if !reflect.DeepEqual(got["annotations"], any(nested)) {
				t.Fatalf("%s of %T has annotations %v, want the nested map", encoding, value, got["annotations"])
			}
		}
	}

	// The YAML is written as a block of plain text sub-keys.
	yamlOut, err := yaml.Marshal(fromYAML)
	if err != nil {
		t.Fatalf("yaml.Marshal returned error: %v", err)
	}

	block := "        builder-doc:\n            digest: sha256:aa\n            id: bb\n" +
		"            path: /phenix/topologies/site/builder.yaml\n        keep: v\n"
	if !strings.Contains(string(yamlOut), block) {
		t.Fatalf("YAML = \n%s\nwant it to hold\n%s", yamlOut, block)
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

// TestStructuredAnnotationValues decodes values of a structured annotation
// and of a plain one. A structured annotation may be a map of text sub-keys
// or a string; a string that is not a map of text is kept and written back
// as the string it is, so that nothing is lost and its validation can report
// it. Any other value fails with an error that names the annotation.
func TestStructuredAnnotationValues(t *testing.T) {
	t.Parallel()

	config := func(annotations string) string {
		return `{"metadata": {"name": "site", "annotations": ` + annotations + `}}`
	}

	// old is the reference an earlier Builder wrote: a JSON object, but not
	// one of text values.
	const old = `{"id":"bb","digest":"sha256:aa","size":10,"chunks":1}`

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
			name: "the reference an earlier Builder wrote",
			json: `{"builder-doc": ` + strconv.Quote(old) + `}`, yaml: "builder-doc: '" + old + "'\n",
			memory: old, wire: old,
		},
		{
			name: "a string with a null sub-key", json: `{"builder-doc": "{\"id\":null}"}`, yaml: `builder-doc: '{"id":null}'` + "\n",
			memory: `{"id":null}`, wire: `{"id":null}`,
		},
		{
			name: "a string with trailing content", json: `{"builder-doc": "{\"id\":\"bb\"} x"}`,
			yaml: `builder-doc: '{"id":"bb"} x'` + "\n", memory: `{"id":"bb"} x`, wire: `{"id":"bb"} x`,
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
					t.Fatalf("builder-doc loaded from %s = %q, want %q", encoding, got, test.memory)
				}

				jsonOut, err := json.Marshal(loaded)
				if err != nil {
					t.Fatalf("json.Marshal returned error: %v", err)
				}

				if got := wireAnnotations(t, jsonOut, json.Unmarshal)["builder-doc"]; !reflect.DeepEqual(got, test.wire) {
					t.Fatalf("builder-doc in JSON = %#v, want %#v\n%s", got, test.wire, jsonOut)
				}

				yamlOut, err := yaml.Marshal(loaded)
				if err != nil {
					t.Fatalf("yaml.Marshal returned error: %v", err)
				}

				if got := wireAnnotations(t, yamlOut, yaml.Unmarshal)["builder-doc"]; !reflect.DeepEqual(got, test.wire) {
					t.Fatalf("builder-doc in YAML = %#v, want %#v\n%s", got, test.wire, yamlOut)
				}

				// What is written loads as what was loaded.
				var back Config
				if err := yaml.Unmarshal(yamlOut, &back); err != nil ||
					asJSON(t, back.Metadata.Annotations) != asJSON(t, loaded.Metadata.Annotations) {
					t.Fatalf("YAML loads as %v (%v), want %v\n%s", back.Metadata.Annotations, err, loaded.Metadata.Annotations, yamlOut)
				}
			}
		})
	}

	// A YAML scalar that is not text becomes its text, in a sub-key as in a
	// plain annotation.
	var coerced Config
	if err := yaml.Unmarshal([]byte("metadata:\n  annotations:\n    plain: 42\n    builder-doc:\n      id: 42\n"), &coerced); err != nil {
		t.Fatalf("YAML does not load: %v", err)
	}

	if want := (Annotations{"plain": "42", "builder-doc": `{"id":"42"}`}); !reflect.DeepEqual(coerced.Metadata.Annotations, want) {
		t.Fatalf("annotations = %v, want %v", coerced.Metadata.Annotations, want)
	}

	// An alias to a map is the map.
	var aliased Config

	withAlias := "ref: &ref\n  id: bb\nmetadata:\n  annotations:\n    builder-doc: *ref\n"
	if err := yaml.Unmarshal([]byte(withAlias), &aliased); err != nil || aliased.Metadata.Annotations["builder-doc"] != `{"id":"bb"}` {
		t.Fatalf("an aliased map loads as %v (%v), want the map", aliased.Metadata.Annotations, err)
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
		var loaded Config

		if err := json.Unmarshal([]byte(config(test.json)), &loaded); err == nil || !strings.Contains(err.Error(), "annotation "+test.key) {
			t.Errorf("%s: JSON error = %v, want one naming annotation %s", name, err, test.key)
		}

		yamlConfig := "metadata:\n  annotations:\n    " + test.yaml
		if err := yaml.Unmarshal([]byte(yamlConfig), &loaded); err == nil || !strings.Contains(err.Error(), "annotation "+test.key) {
			t.Errorf("%s: YAML error = %v, want one naming annotation %s", name, err, test.key)
		}
	}

	// Of several annotations that do not load, the first in key order is named.
	for range 8 {
		var several Config

		err := json.Unmarshal([]byte(config(`{"z": 1, "builder-doc": 2, "a": 3, "m": "text"}`)), &several)
		if err == nil || !strings.Contains(err.Error(), "annotation a:") {
			t.Fatalf("JSON error = %v, want one naming annotation a", err)
		}

		err = yaml.Unmarshal([]byte("metadata:\n  annotations:\n    z: [1]\n    builder-doc: [2]\n    a: [3]\n    m: text\n"), &several)
		if err == nil || !strings.Contains(err.Error(), "annotation a:") {
			t.Fatalf("YAML error = %v, want one naming annotation a", err)
		}
	}

	// Annotations that are not a map at all.
	var loaded Config
	if err := json.Unmarshal([]byte(config(`["a"]`)), &loaded); err == nil {
		t.Error("a list of annotations loads from JSON")
	}

	if err := yaml.Unmarshal([]byte("metadata:\n  annotations: [a]\n"), &loaded); err == nil {
		t.Error("a list of annotations loads from YAML")
	}

	if _, err := NewConfigFromYAML([]byte("metadata:\n  annotations:\n    note:\n      a: b\n")); !errors.Is(err, ErrInvalidFormat) {
		t.Errorf("NewConfigFromYAML error = %v, want ErrInvalidFormat", err)
	}
}

// TestAnnotationsDecodeIntoExistingConfig decodes a config into one that is
// already loaded, as `phenix config edit` and the store's Get do: the
// annotations read are set in those already there, as they are for a plain
// map, and a null leaves none.
func TestAnnotationsDecodeIntoExistingConfig(t *testing.T) {
	t.Parallel()

	loaded := func() Config {
		return Config{
			Metadata: ConfigMetadata{Name: "site", Annotations: Annotations{"old": "x", "builder-doc": `{"id":"aa"}`}},
			Spec:     map[string]any{"gone": 1},
		}
	}

	// plainConfig decodes as a Config did before annotations had a codec.
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
		for encoding, decode := range map[string]func(any) error{
			"JSON": func(target any) error { return json.Unmarshal([]byte(test.json), target) },
			"YAML": func(target any) error { return yaml.Unmarshal([]byte(test.yaml), target) },
		} {
			config := loaded()
			if err := decode(&config); err != nil {
				t.Fatalf("%s from %s: decoding returned error: %v", test.name, encoding, err)
			}

			if !reflect.DeepEqual(config.Metadata.Annotations, test.want) {
				t.Errorf("%s from %s: annotations = %v, want %v", test.name, encoding, config.Metadata.Annotations, test.want)
			}

			// A plain map gives the same keys, where it can decode the text.
			if test.name == "the structured key" {
				continue
			}

			var plain plainConfig

			plain.Metadata.Annotations = map[string]string{"old": "x", "builder-doc": `{"id":"aa"}`}
			if err := decode(&plain); err != nil {
				t.Fatalf("%s from %s: decoding with plain annotations returned error: %v", test.name, encoding, err)
			}

			if !reflect.DeepEqual(Annotations(plain.Metadata.Annotations), config.Metadata.Annotations) {
				t.Errorf("%s from %s: annotations = %v, a plain map gives %v",
					test.name, encoding, config.Metadata.Annotations, plain.Metadata.Annotations)
			}
		}
	}
}

// TestStoredJSONKeepsAnnotationsAsStrings asserts the JSON the store keeps
// for a config holds a structured annotation as the string it is in memory,
// which a struct with a plain map of strings reads, and is otherwise the
// JSON of the config.
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

	if want := map[string]string{"keep": "v", "builder-doc": structuredReference}; !reflect.DeepEqual(plain.Metadata.Annotations, want) {
		t.Fatalf("stored annotations = %v, want %v", plain.Metadata.Annotations, want)
	}

	// The JSON of the config itself is not readable that way: that is why the
	// store keeps another.
	wire, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	if err := json.Unmarshal(wire, &plain); err == nil {
		t.Fatalf("the JSON of the config loads with plain annotations:\n%s", wire)
	}

	var back Config
	if err := json.Unmarshal(stored, &back); err != nil || !reflect.DeepEqual(back, *config) {
		t.Fatalf("the stored JSON loads as %+v (%v), want the config", back, err)
	}

	// Without a structured annotation the stored JSON is the config's JSON,
	// field for field, whatever is empty.
	for _, annotations := range []Annotations{nil, {}, {"keep": "v", "builder-experiment": `{"draftId":"d"}`}} {
		for _, spec := range []map[string]any{nil, {"nodes": []any{}}} {
			other := *config
			other.Metadata.Annotations, other.Spec, other.Status = annotations, spec, spec

			stored, err := other.StoredJSON()
			if err != nil {
				t.Fatalf("StoredJSON returned error: %v", err)
			}

			if want := asJSON(t, other); string(stored) != want {
				t.Fatalf("stored JSON = %s, want the JSON of the config %s", stored, want)
			}
		}
	}

	// Every field of a config is stored.
	if got, want := reflect.TypeFor[storedConfig]().NumField(), reflect.TypeFor[Config]().NumField(); got != want {
		t.Fatalf("storedConfig has %d fields, Config has %d", got, want)
	}

	if got, want := reflect.TypeFor[storedMetadata]().NumField(), reflect.TypeFor[ConfigMetadata]().NumField(); got != want {
		t.Fatalf("storedMetadata has %d fields, ConfigMetadata has %d", got, want)
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
