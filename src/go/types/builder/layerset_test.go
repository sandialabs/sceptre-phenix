package builder_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"phenix/types/builder"
)

// layerSetFileYAML is a layer set file as the Builder downloads one.
const layerSetFileYAML = `$schema: https://phenix.sandia.gov/schemas/builder/layersets/v1
name: Plant levels
layers:
  - id: 1
    label: Field devices
`

// TestLayerSetFileIsNoDocument tells a layer set file, YAML or JSON, from a
// Builder document, a template file, a package and a phenix config.
func TestLayerSetFileIsNoDocument(t *testing.T) {
	encoded, err := builder.JSONFromYAML([]byte(layerSetFileYAML))
	if err != nil {
		t.Fatalf("JSONFromYAML returned error: %v", err)
	}

	for name, text := range map[string][]byte{"YAML": []byte(layerSetFileYAML), "JSON": encoded} {
		if !builder.IsLayerSetFileText(text) {
			t.Errorf("%s: IsLayerSetFileText does not recognize a layer set file", name)
		}

		if builder.IsDocumentText(text) || builder.IsTemplateFileText(text) || builder.IsPackageText(text) {
			t.Errorf("%s: a layer set file is taken for a document, a template file or a package", name)
		}
	}

	for name, text := range map[string]string{
		"a Builder document": `{"$schema": "` + builder.SchemaURI + `"}`,
		"a template file":    `{"$schema": "` + builder.TemplateFileSchemaURI + `"}`,
		"a package":          `{"$schema": "` + builder.PackageSchemaURI + `"}`,
		"a kind":             layerSetFileYAML + "kind: Topology\n",
		"a topology config":  "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: x\n",
	} {
		if builder.IsLayerSetFileText([]byte(text)) {
			t.Errorf("%s: IsLayerSetFileText = true, want false", name)
		}
	}
}

func TestLayerSetIssues(t *testing.T) {
	valid := func() builder.LayerSet {
		return builder.LayerSet{
			Name: "Plant levels", Description: "", IconKey: "", Icon: "", MatchConflict: "", LayerOutlines: false,
			Layers: []builder.Layer{{ID: layerID(1), Label: "Control"}},
		}
	}

	set := valid()
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate refused a valid set: %v", err)
	}

	for name, test := range map[string]struct {
		change func(*builder.LayerSet)
		code   builder.Code
		path   string
	}{
		"long name": {
			func(s *builder.LayerSet) { s.Name = strings.Repeat("a", builder.MaxLayerSetNameBytes+1) },
			builder.CodeLayerSetNameTooLong, "name",
		},
		"name with a newline": {
			func(s *builder.LayerSet) { s.Name = "a\nb" }, builder.CodeLayerSetNameControl, "name",
		},
		"long description": {
			func(s *builder.LayerSet) { s.Description = strings.Repeat("a", builder.MaxLayerSetDescriptionBytes+1) },
			builder.CodeLayerSetDescriptionTooLong, "description",
		},
		"description with a tab": {
			func(s *builder.LayerSet) { s.Description = "a\tb" }, builder.CodeLayerSetDescriptionControl, "description",
		},
		"unknown icon key": {
			func(s *builder.LayerSet) { s.IconKey = "tower" }, builder.CodeLayerSetIconKeyUnknown, "iconKey",
		},
		"custom icon that is no icon name": {
			func(s *builder.LayerSet) { s.Icon = ".." }, builder.CodeLayerSetIconInvalid, "icon",
		},
		"unknown match conflict rule": {
			func(s *builder.LayerSet) { s.MatchConflict = "middle" }, builder.CodeLayerSetMatchConflictUnknown, "matchConflict",
		},
		"no layers": {
			func(s *builder.LayerSet) { s.Layers = nil }, builder.CodeLayerSetLayersEmpty, "layers",
		},
		"too many layers": {
			func(s *builder.LayerSet) {
				s.Layers = nil
				for id := range builder.MaxLayers + 1 {
					s.Layers = append(s.Layers, builder.Layer{ID: layerID(id), Label: "L"})
				}
			},
			builder.CodeLayerListTooMany, "layers",
		},
		"layer color": {
			func(s *builder.LayerSet) { s.Layers[0].Color = "#abc" }, builder.CodeLayerColorInvalid, "layers[0].color",
		},
	} {
		t.Run(name, func(t *testing.T) {
			set := valid()
			test.change(&set)

			issues := builder.ErrorIssues(set.Validate())
			if len(issues) != 1 || issues[0].Code != test.code || issues[0].Path != test.path {
				t.Fatalf("issues %v, want one %s at %s", issues, test.code, test.path)
			}
		})
	}

	// A set inside another object has its issues under the path given.
	set = valid()
	set.Name = ""

	if issues := set.Issues("set"); len(issues) != 1 || issues[0].Path != "set.name" {
		t.Fatalf("issues %v, want one at set.name", issues)
	}
}

// TestMatcherPatternsFollowGo pins patterns only one of Go and JavaScript compiles: the server (Go)
// decides, so a JavaScript-only pattern is refused and a Go-only one is accepted.
func TestMatcherPatternsFollowGo(t *testing.T) {
	for pattern, valid := range map[string]bool{
		"(?i)plc":   true,
		`\Aplc\z`:   true,
		"plc(?=-1)": false,
		`(a)\1`:     false,
		"plc(?!-1)": false,
		"[":         false,
	} {
		doc := loadDocumentFixture(t, "document.json")
		doc.Layers = []builder.Layer{{ID: layerID(1), Label: "Control", Matchers: map[string]string{"general.hostname": pattern}}}

		if err := doc.Validate(); (err == nil) != valid {
			t.Errorf("pattern %q: Validate error = %v, want valid = %v", pattern, err, valid)
		}
	}
}

func TestBuiltinLayerSets(t *testing.T) {
	sets := builder.BuiltinLayerSets()
	if len(sets) != 1 || sets[0].ID != builder.BuiltinPurdueLayerSetID {
		t.Fatalf("built-in sets %v, want the Purdue Model only", sets)
	}

	purdue := sets[0]
	if err := purdue.Validate(); err != nil {
		t.Fatalf("the Purdue Model set does not validate: %v", err)
	}

	if !builder.IsLayerSetID(purdue.ID) {
		t.Fatalf("%q is not a layer set ID", purdue.ID)
	}

	labels := make([]string, 0, len(purdue.Layers))
	colors := map[string]bool{}

	for i, layer := range purdue.Layers {
		if *layer.ID != len(purdue.Layers)-1-i {
			t.Fatalf("layer %d has ID %d, want the highest ID first", i, *layer.ID)
		}

		if layer.IconKey == "" || layer.Color == "" || layer.Matchers != nil {
			t.Fatalf("layer %d %+v lacks an icon or a color, or has matchers", i, layer)
		}

		labels = append(labels, layer.Label)
		colors[layer.Color] = true
	}

	want := []string{
		"L5 Enterprise", "L4 Business", "L3.5 Industrial DMZ", "L3 Operations",
		"L2 Supervisory control", "L1 Basic control", "L0 Process",
	}
	if !slices.Equal(labels, want) || len(colors) != len(want) {
		t.Fatalf("labels %v and %d colors, want %v, each with its own color", labels, len(colors), want)
	}

	if purdue.MatchConflict != builder.MatchConflictLowest || purdue.LayerOutlines {
		t.Fatalf("options %q, %v, want lowest and no layer outlines", purdue.MatchConflict, purdue.LayerOutlines)
	}

	// Each call is a copy of its own.
	*sets[0].Layers[0].ID = 50
	if *builder.BuiltinLayerSets()[0].Layers[0].ID != 6 {
		t.Fatal("BuiltinLayerSets returned shared layers")
	}
}

func TestIsLayerSetID(t *testing.T) {
	for id, want := range map[string]bool{
		builder.BuiltinPurdueLayerSetID:        true,
		"6f1c2a8e-4b7d-4e2a-9c3f-1d2e3f4a5b6c": true,
		"set_1.a":                              true,
		"":                                     false,
		"a b":                                  false,
		"a/b":                                  false,
		strings.Repeat("a", builder.MaxLayerSetIDBytes):   true,
		strings.Repeat("a", builder.MaxLayerSetIDBytes+1): false,
	} {
		if got := builder.IsLayerSetID(id); got != want {
			t.Errorf("IsLayerSetID(%q) = %v, want %v", id, got, want)
		}
	}
}

// TestLayerSetFileSchemaPropertiesMatchLayerSet checks that the properties of a layer set file
// are the fields of a set, its schema and its icons.
func TestLayerSetFileSchemaPropertiesMatchLayerSet(t *testing.T) {
	schema, err := builder.LayerSetFileSchema()
	if err != nil {
		t.Fatalf("LayerSetFileSchema returned error: %v", err)
	}

	fields := jsonFields(t, builder.LayerSet{})
	want := make([]string, 0, len(fields)+2)
	want = append(want, "$schema", "icons")

	for name := range fields {
		want = append(want, name)
	}

	slices.Sort(want)

	if got := propertyNames(t, schema); !slices.Equal(got, want) {
		t.Fatalf("the schema has the properties %v, and a layer set file the fields %v", got, want)
	}
}

func TestSchemaLayerMatchers(t *testing.T) {
	schema := mustSchema(t)
	layer := mapAt(t, mapAt(t, schema, "$defs"), "layer")
	matchers := mapAt(t, mapAt(t, layer, "properties"), "matchers")

	if matchers["maxProperties"] != builder.MaxLayerMatchers {
		t.Fatalf("matchers allow %v, want %d", matchers["maxProperties"], builder.MaxLayerMatchers)
	}

	pattern := mapAt(t, matchers, "additionalProperties")
	if pattern["minLength"] != 1 || pattern["maxLength"] != builder.MaxMatcherPatternBytes {
		t.Fatalf("a matcher pattern is not 1 to %d characters: %v", builder.MaxMatcherPatternBytes, pattern)
	}

	field := mapAt(t, matchers, "propertyNames")
	matcher := regexp.MustCompile(field["pattern"].(string))

	// The pattern accepts the fields Document.Validate accepts.
	for path, valid := range map[string]bool{
		"general.hostname": true, "network.interfaces.0.vlan": true, "a_b-c": true,
		"": false, ".a": false, "a.": false, "a..b": false, "a b": false, "a/b": false,
	} {
		doc := loadDocumentFixture(t, "document.json")
		doc.Layers = []builder.Layer{{ID: layerID(1), Label: "Control", Matchers: map[string]string{path: "x"}}}

		if matcher.MatchString(path) != valid || (doc.Validate() == nil) != valid {
			t.Errorf("matcher field %q: the pattern or Validate does not say valid = %v", path, valid)
		}
	}

	options := mapAt(t, mapAt(t, schema, "$defs"), "layerSet")
	if options["additionalProperties"] != false {
		t.Fatalf("the layer set options allow unknown keys: %v", options)
	}
}
