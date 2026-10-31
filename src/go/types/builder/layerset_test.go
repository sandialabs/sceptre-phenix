package builder_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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
		t.Fatalf("JSONFromYAML(layer set file): %v", err)
	}

	for name, text := range map[string][]byte{"YAML": []byte(layerSetFileYAML), "JSON": encoded} {
		t.Run(name, func(t *testing.T) {
			if got := builder.FileKindOf(text); got != builder.FileKindLayerSetFile {
				t.Errorf("FileKindOf(%s layer set file) = %q, want %q", name, got, builder.FileKindLayerSetFile)
			}
		})
	}

	for name, test := range map[string]struct {
		text string
		want builder.FileKind
	}{
		"a Builder document": {`{"$schema": "` + builder.SchemaURI + `"}`, builder.FileKindDocument},
		"a template file":    {`{"$schema": "` + builder.TemplateFileSchemaURI + `"}`, builder.FileKindTemplateFile},
		"a package":          {`{"$schema": "` + builder.PackageSchemaURI + `"}`, builder.FileKindPackage},
		"a kind":             {layerSetFileYAML + "kind: Topology\n", builder.FileKindOther},
		"a topology config":  {"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: x\n", builder.FileKindOther},
	} {
		t.Run(name, func(t *testing.T) {
			if got := builder.FileKindOf([]byte(test.text)); got != test.want {
				t.Errorf("FileKindOf(%s) = %q, want %q", name, got, test.want)
			}
		})
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
		t.Fatalf("LayerSet.Validate() of a valid set = %v, want nil", err)
	}

	// The cases name the code and path of the issue, not its message.
	codeAndPath := cmpopts.IgnoreFields(builder.Issue{}, "Severity", "Message", "NodeID", "EdgeID", "NetworkID", "Field")

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

			want := []builder.Issue{{Code: test.code, Path: test.path}}
			if diff := cmp.Diff(want, builder.ErrorIssues(set.Validate()), codeAndPath); diff != "" {
				t.Errorf("LayerSet.Validate() issues mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// A set inside another object has its issues under the path given.
	set = valid()
	set.Name = ""

	issues := set.Issues("set")
	paths := make([]string, 0, len(issues))

	for _, issue := range issues {
		paths = append(paths, issue.Path)
	}

	if diff := cmp.Diff([]string{"set.name"}, paths); diff != "" {
		t.Errorf("LayerSet.Issues(\"set\") of a set without a name paths mismatch (-want +got):\n%s", diff)
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
		t.Run(pattern, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			doc.Layers = []builder.Layer{{ID: layerID(1), Label: "Control", Matchers: map[string]string{"general.hostname": pattern}}}

			err := doc.Validate()

			switch {
			case valid && err != nil:
				t.Errorf("Validate() with matcher pattern %q error = %v, want nil", pattern, err)
			case !valid && err == nil:
				t.Errorf("Validate() with matcher pattern %q error = nil, want an error", pattern)
			}
		})
	}
}

func TestBuiltinLayerSets(t *testing.T) {
	sets := builder.BuiltinLayerSets()
	if len(sets) != 1 || sets[0].ID != builder.BuiltinPurdueLayerSetID {
		t.Fatalf("BuiltinLayerSets() = %v, want the Purdue Model only", sets)
	}

	purdue := sets[0]
	if err := purdue.Validate(); err != nil {
		t.Errorf("Validate() of the Purdue Model set = %v, want nil", err)
	}

	if !builder.IsLayerSetID(purdue.ID) {
		t.Errorf("IsLayerSetID(%q) = false, want true", purdue.ID)
	}

	labels := make([]string, 0, len(purdue.Layers))
	colors := map[string]bool{}

	for i, layer := range purdue.Layers {
		if want := len(purdue.Layers) - 1 - i; *layer.ID != want {
			t.Errorf("Purdue Model layer %q ID = %d, want %d (the highest ID first)", layer.Label, *layer.ID, want)
		}

		if layer.IconKey == "" || layer.Color == "" || layer.Matchers != nil {
			t.Errorf("Purdue Model layer = %+v, want one with an icon and a color and no matchers", layer)
		}

		labels = append(labels, layer.Label)
		colors[layer.Color] = true
	}

	want := []string{
		"L5 Enterprise", "L4 Business", "L3.5 Industrial DMZ", "L3 Operations",
		"L2 Supervisory control", "L1 Basic control", "L0 Process",
	}
	if diff := cmp.Diff(want, labels); diff != "" {
		t.Errorf("Purdue Model layer labels mismatch (-want +got):\n%s", diff)
	}

	if len(colors) != len(want) {
		t.Errorf("Purdue Model layers have %d colors, want %d, each with its own color", len(colors), len(want))
	}

	type options struct {
		MatchConflict string
		LayerOutlines bool
	}

	gotOptions := options{MatchConflict: purdue.MatchConflict, LayerOutlines: purdue.LayerOutlines}
	if diff := cmp.Diff(options{MatchConflict: builder.MatchConflictLowest}, gotOptions); diff != "" {
		t.Errorf("Purdue Model options mismatch (-want +got):\n%s", diff)
	}

	// Each call is a copy of its own.
	*sets[0].Layers[0].ID = 50
	if got := *builder.BuiltinLayerSets()[0].Layers[0].ID; got != 6 {
		t.Errorf("BuiltinLayerSets() after a change to an earlier result: first layer ID = %d, want 6", got)
	}
}

func TestIsLayerSetID(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
		want bool
	}{
		{"the Purdue Model set", builder.BuiltinPurdueLayerSetID, true},
		{"a UUID", "6f1c2a8e-4b7d-4e2a-9c3f-1d2e3f4a5b6c", true},
		{"underscores and dots", "set_1.a", true},
		{"empty", "", false},
		{"a space", "a b", false},
		{"a slash", "a/b", false},
		{"the longest", strings.Repeat("a", builder.MaxLayerSetIDBytes), true},
		{"one byte too long", strings.Repeat("a", builder.MaxLayerSetIDBytes+1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := builder.IsLayerSetID(test.id); got != test.want {
				t.Errorf("IsLayerSetID(%q) = %v, want %v", test.id, got, test.want)
			}
		})
	}
}

// TestLayerSetFileSchemaPropertiesMatchLayerSet checks that the properties of a layer set file
// are the fields of a set, its schema and its icons.
func TestLayerSetFileSchemaPropertiesMatchLayerSet(t *testing.T) {
	schema, err := builder.LayerSetFileSchema()
	if err != nil {
		t.Fatalf("LayerSetFileSchema(): %v", err)
	}

	fields := jsonFields(t, builder.LayerSet{})
	want := make([]string, 0, len(fields)+2)
	want = append(want, "$schema", "icons")

	for name := range fields {
		want = append(want, name)
	}

	slices.Sort(want)

	if diff := cmp.Diff(want, propertyNames(t, schema)); diff != "" {
		t.Errorf("LayerSetFileSchema() properties mismatch with the layer set file fields (-want +got):\n%s", diff)
	}
}

func TestSchemaLayerMatchers(t *testing.T) {
	schema := mustSchema(t)
	layer := mapAt(t, mapAt(t, schema, "$defs"), "layer")
	matchers := mapAt(t, mapAt(t, layer, "properties"), "matchers")

	if matchers["maxProperties"] != builder.MaxLayerMatchers {
		t.Errorf("Schema() layer matchers maxProperties = %v, want %d", matchers["maxProperties"], builder.MaxLayerMatchers)
	}

	value := mapAt(t, matchers, "additionalProperties")
	wantBounds := map[string]any{"minLength": 1, "maxLength": builder.MaxMatcherPatternBytes}
	gotBounds := map[string]any{"minLength": value["minLength"], "maxLength": value["maxLength"]}

	if diff := cmp.Diff(wantBounds, gotBounds); diff != "" {
		t.Errorf("Schema() layer matcher pattern bounds mismatch (-want +got):\n%s", diff)
	}

	field := mapAt(t, matchers, "propertyNames")

	pattern, ok := field["pattern"].(string)
	if !ok {
		t.Fatalf("Schema() layer matcher propertyNames pattern = %v, want a string", field["pattern"])
	}

	matcher := regexp.MustCompile(pattern)

	// The pattern accepts the fields Document.Validate accepts.
	for path, valid := range map[string]bool{
		"general.hostname": true, "network.interfaces.0.vlan": true, "a_b-c": true,
		"": false, ".a": false, "a.": false, "a..b": false, "a b": false, "a/b": false,
	} {
		t.Run(path, func(t *testing.T) {
			if got := matcher.MatchString(path); got != valid {
				t.Errorf("Schema() matcher field pattern MatchString(%q) = %v, want %v", path, got, valid)
			}

			doc := loadDocumentFixture(t, "document.json")
			doc.Layers = []builder.Layer{{ID: layerID(1), Label: "Control", Matchers: map[string]string{path: "x"}}}

			err := doc.Validate()

			switch {
			case valid && err != nil:
				t.Errorf("Validate() with matcher field %q error = %v, want nil", path, err)
			case !valid && err == nil:
				t.Errorf("Validate() with matcher field %q error = nil, want an error", path)
			}
		})
	}

	options := mapAt(t, mapAt(t, schema, "$defs"), "layerSet")
	if options["additionalProperties"] != false {
		t.Errorf("Schema() layerSet additionalProperties = %v, want false", options["additionalProperties"])
	}
}

// TestNestedRepeat checks which patterns repeat a group with a repeat or "|" in
// it. testdata/validation-corpus.json holds the editor to the same rule.
func TestNestedRepeat(t *testing.T) {
	for pattern, want := range map[string]bool{
		`(a+)+`:              true,
		`(a*)*`:              true,
		`(a|aa)+`:            true,
		`(?:a|b)+?`:          true,
		`((a+)?b)+`:          true,
		`(\d{1,3}\.){3}`:     true,
		`(a{10}){100}`:       true,
		`((a{100}){0}){100}`: true,
		`(a{0,}){1000}`:      true,
		`(a*){1000}`:         true,
		`(?P<n>a+)+`:         true,
		`(?i:a|b)*`:          true,
		`(?i)(a+)+`:          true,
		`^plc-`:              false,
		`linux|windows`:      false,
		`^(hmi|scada)-\d+$`:  false,
		`(?:ab)+?`:           false,
		`(a+)?`:              false,
		`(a|b){0,1}`:         false,
		`(a+){1}`:            false,
		`[(a+)]+`:            false,
		`[]a|]+`:             false,
		`[[:alpha:]|]+`:      false,
		`\(a+\)+`:            false,
		`\Q(a+)+\E`:          false,
		`\x{28}a+\x{29}+`:    false,
		`\pL+`:               false,
		`\p{Greek}+`:         false,
		`(a){2,}`:            false,
		`a{2}{`:              false,
		`(?i)a+`:             false,
	} {
		if got := builder.NestedRepeat(pattern); got != want {
			t.Errorf("NestedRepeat(%q) = %v, want %v", pattern, got, want)
		}
	}
}

// TestUnboundedRepeats checks how many *, + and {n,} repeats a pattern has.
// testdata/validation-corpus.json holds the editor to the same count.
func TestUnboundedRepeats(t *testing.T) {
	for pattern, want := range map[string]int{
		`^plc-`:               0,
		`^plc-.*-\d+$`:        2,
		`.*foo.*bar.*`:        3,
		`a*a*a*a*b`:           4,
		`a*?b+?`:              2,
		`a{2,}b{0,}`:          2,
		`a{2,5}b{3}c?`:        0,
		`a{,5}`:               0,
		`a{2}{`:               0,
		`\*\+`:                0,
		`[*+]+`:               1,
		`[]*+]`:               0,
		`[[:alpha:]*]`:        0,
		`(a*)(b+)`:            2,
		`(?:a*|b+)?`:          2,
		`(?P<n>a+)`:           1,
		`(?i)a*`:              1,
		`\Qa*b+\E`:            0,
		`\pL+`:                1,
		`\x{2a}+`:             1,
		`(a+)+`:               2,
		`a*a*a*a*a*a*a*a*a*b`: 9,
	} {
		if got := builder.UnboundedRepeats(pattern); got != want {
			t.Errorf("UnboundedRepeats(%q) = %d, want %d", pattern, got, want)
		}
	}
}
