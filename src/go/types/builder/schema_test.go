package builder_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

// frontendSchemaBundle is the copy of the builder schema the web UI bundles for
// client-side validation and JSON Forms. It must match [builder.SchemaJSON].
var frontendSchemaBundle = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "js", "src", "builder", "schema", "builder-v1.schema.json",
)

var updateFrontendSchema = flag.Bool( //nolint:gochecknoglobals // test flag
	"update-frontend-schema",
	false,
	"rewrite the web UI builder schema bundle and error codes, and the docs' table of codes, from the builder package",
)

func TestSchemaHeader(t *testing.T) {
	schema := mustSchema(t)

	want := map[string]any{
		"$id":                                 builder.SchemaURI,
		"$schema":                             builder.SchemaDialect,
		"properties.$schema.const":            builder.SchemaURI,
		"properties.revision.const":           builder.SchemaRevision,
		"properties.metadata.$ref":            "#/$defs/metadata",
		"additionalProperties":                false,
		"$defs.metadata.required":             []any{"id"},
		"$defs.metadata.additionalProperties": false,
	}
	if diff := cmp.Diff(want, schemaValues(schema, want)); diff != "" {
		t.Errorf("Schema() header mismatch (-want +got):\n%s", diff)
	}

	for _, required := range []string{
		"$schema", "revision", "metadata", "nodes", "networks", "edges", "viewport", "grid",
	} {
		if !containsAny(schema["required"], required) {
			t.Errorf("Schema() required = %v, want it to include %q", schema["required"], required)
		}
	}
}

// builderSchema is one schema the Builder serves, with the definitions it must hold.
type builderSchema struct {
	name   string
	schema func() (map[string]any, error)
	json   func() ([]byte, error)
	uri    string
	holds  []string
}

func builderSchemas() []builderSchema {
	return []builderSchema{
		{"document", builder.Schema, builder.SchemaJSON, builder.SchemaURI, nil},
		{
			"package", builder.PackageSchema, builder.PackageSchemaJSON, builder.PackageSchemaURI,
			[]string{"packageDocument", "packageConfig", "packageRequirements", "packageImage", "node"},
		},
		// The template device refers to the icon size, which the file schema holds, as it does the
		// definitions the icon size has no part in.
		{
			"template file", builder.TemplateFileSchema, builder.TemplateFileSchemaJSON, builder.TemplateFileSchemaURI,
			[]string{"templateDevice", "iconSize", "iconKey", "iconRef", "hexColor"},
		},
		{
			"layer set file", builder.LayerSetFileSchema, builder.LayerSetFileSchemaJSON, builder.LayerSetFileSchemaURI,
			[]string{"layer", "layerId", "iconKey", "iconRef", "hexColor", "icon", "matchConflict"},
		},
	}
}

// TestSchemaDocumentsEveryProperty checks that each Builder-owned definition and property of
// every Builder schema has a title, a description and an example (schema-examples.contract.test.js checks
// the examples are valid).
func TestSchemaDocumentsEveryProperty(t *testing.T) {
	for _, tc := range builderSchemas() {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := tc.schema()
			if err != nil {
				t.Fatalf("building the schema: %v", err)
			}

			if schema["$id"] != tc.uri {
				t.Errorf("%s schema $id = %v, want %s", tc.name, schema["$id"], tc.uri)
			}

			var missing []string

			for name, property := range mapAt(t, schema, "properties") {
				missing = append(missing, undocumented("properties."+name, property, true)...)
			}

			for name, def := range mapAt(t, schema, "$defs") {
				if strings.HasPrefix(name, builder.PhenixDefPrefix) {
					continue
				}

				missing = append(missing, undocumented("$defs."+name, def, true)...)
			}

			if len(missing) > 0 {
				slices.Sort(missing)
				t.Errorf("%s schema has these without a title, a description or examples:\n  %s",
					tc.name, strings.Join(missing, "\n  "))
			}
		})
	}
}

// undocumented returns the paths under a schema (itself too when own is set) that lack a title,
// a description or examples, walking properties, items and map values but not conditions.
func undocumented(path string, value any, own bool) []string {
	schema, ok := value.(map[string]any)
	if !ok {
		return nil
	}

	var missing []string

	if own {
		title, _ := schema["title"].(string)
		description, _ := schema["description"].(string)
		examples, _ := schema["examples"].([]any)

		if strings.TrimSpace(title) == "" || strings.TrimSpace(description) == "" || len(examples) == 0 {
			missing = append(missing, path)
		}
	}

	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, property := range properties {
			missing = append(missing, undocumented(path+".properties."+name, property, true)...)
		}
	}

	for _, key := range []string{"items", "additionalProperties"} {
		missing = append(missing, undocumented(path+"."+key, schema[key], false)...)
	}

	return missing
}

func TestSchemaDefinesBuilderStructures(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	for _, name := range []string{
		"identifier", "iconKey", "position", "size", "viewport", "grid",
		"interfaceHandle", "device", "switch", "note", "group", "node",
		"network", "edge", "source",
		"hexColor", "lineStyle", "borderStyle", "iconRef", "icon", "template", "templateDevice",
		"shape", "iconNode", "line", "layer", "layerId",
	} {
		def := mapAt(t, defs, name)

		if def["type"] == "object" && def["additionalProperties"] != false {
			t.Errorf("Schema() $defs.%s.additionalProperties = %v, want false", name, def["additionalProperties"])
		}
	}

	edge := mapAt(t, defs, "edge")
	edgeProps := mapAt(t, edge, "properties")

	for _, name := range []string{
		"sourceNodeId", "sourceHandleId", "targetNodeId", "targetHandleId", "networkId",
		"label", "color", "lineStyle", "route",
	} {
		if _, ok := edgeProps[name]; !ok {
			t.Errorf("Schema() $defs.edge has no %q property", name)
		}
	}

	// A list of at least two positions.
	wantRoute := map[string]any{"minItems": 2, "items.$ref": "#/$defs/position"}
	if diff := cmp.Diff(wantRoute, schemaValues(mapAt(t, edgeProps, "route"), wantRoute)); diff != "" {
		t.Errorf("Schema() $defs.edge.properties.route mismatch (-want +got):\n%s", diff)
	}

	// An optional string.
	if layout := mapAt(t, mapAt(t, mustSchema(t), "properties"), "layout"); layout["type"] != "string" {
		t.Errorf("Schema() properties.layout.type = %v, want string", layout["type"])
	}

	if required := mustSchema(t)["required"]; containsAny(required, "layout") {
		t.Errorf("Schema() required = %v, want it without layout", required)
	}

	iconKey := mapAt(t, defs, "iconKey")

	enum, ok := iconKey["enum"].([]any)
	if !ok {
		t.Fatalf("iconKey has no enum: %v", iconKey)
	}

	if len(enum) != len(builder.IconKeys())+1 {
		t.Errorf("Schema() $defs.iconKey.enum = %v, want the registry %v and one more", enum, builder.IconKeys())
	}

	for _, key := range builder.IconKeys() {
		if !containsAny(iconKey["enum"], key) {
			t.Errorf("Schema() $defs.iconKey.enum = %v, want it to include %q", enum, key)
		}
	}
}

// propertyNames returns the sorted property names of an object schema.
func propertyNames(t *testing.T, def map[string]any) []string {
	t.Helper()

	return slices.Sorted(maps.Keys(mapAt(t, def, "properties")))
}

// TestSchemaPropertiesMatchDocument checks that each schema object lists exactly the fields of its
// Go type, as the decoder and the schema each refuse keys they lack.
func TestSchemaPropertiesMatchDocument(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")

	for name, value := range map[string]any{
		"metadata":       builder.Metadata{},
		"device":         builder.Device{},
		"switch":         builder.Switch{},
		"note":           builder.Note{},
		"group":          builder.Group{},
		"shape":          builder.Shape{},
		"iconNode":       builder.IconNode{},
		"line":           builder.Line{},
		"node":           builder.Node{},
		"network":        builder.Network{},
		"edge":           builder.Edge{},
		"source":         builder.Source{},
		"icon":           builder.Icon{},
		"template":       builder.Template{},
		"templateDevice": builder.TemplateDevice{},
		"layer":          builder.Layer{},
		"layerSet":       builder.DocumentLayerSet{},
		"layerSetSource": builder.LayerSetSource{},
	} {
		t.Run(name, func(t *testing.T) {
			fields := jsonFields(t, value)
			want := slices.Sorted(maps.Keys(fields))

			if diff := cmp.Diff(want, propertyNames(t, mapAt(t, defs, name))); diff != "" {
				t.Errorf("Schema() $defs.%s properties mismatch with the %T fields (-want +got):\n%s", name, value, diff)
			}

			if name != "metadata" {
				return
			}

			if wrong := requiredButOmitted(mapAt(t, defs, name), fields); len(wrong) > 0 {
				t.Errorf("Schema() $defs.metadata required = %v, want the fields the struct always writes: %s",
					mapAt(t, defs, name)["required"], strings.Join(wrong, "; "))
			}
		})
	}

	fields := jsonFields(t, builder.Document{})
	want := slices.Sorted(maps.Keys(fields))

	if diff := cmp.Diff(want, propertyNames(t, schema)); diff != "" {
		t.Errorf("Schema() root properties mismatch with the Document fields (-want +got):\n%s", diff)
	}

	if wrong := requiredButOmitted(schema, fields); len(wrong) > 0 {
		t.Errorf("Schema() required = %v, want the fields Document always writes: %s",
			schema["required"], strings.Join(wrong, "; "))
	}
}

// requiredButOmitted returns the fields whose being required in def does not match being written
// always: a field left out when empty cannot be required, and the others are always written.
func requiredButOmitted(def map[string]any, fields map[string]string) []string {
	var wrong []string

	for field, declaration := range fields {
		required, omitted := containsAny(def["required"], field), strings.Contains(declaration, ",omitempty")
		if required == omitted {
			wrong = append(wrong, fmt.Sprintf("%s required = %t, omitempty = %t", field, required, omitted))
		}
	}

	slices.Sort(wrong)

	return wrong
}

// TestSchemaPresentationFields pins the schema of the presentation fields,
// each bounded the way Document.Validate bounds it.
func TestSchemaPresentationFields(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")

	want := map[string]string{
		"device.icon":                 "#/$defs/iconRef",
		"device.outlineColor":         "#/$defs/hexColor",
		"device.fillColor":            "#/$defs/hexColor",
		"switch.outlineColor":         "#/$defs/hexColor",
		"switch.fillColor":            "#/$defs/hexColor",
		"group.borderStyle":           "#/$defs/borderStyle",
		"group.iconKey":               "#/$defs/iconKey",
		"group.icon":                  "#/$defs/iconRef",
		"network.lineStyle":           "#/$defs/lineStyle",
		"template.id":                 "#/$defs/identifier",
		"template.device":             "#/$defs/templateDevice",
		"templateDevice.iconKey":      "#/$defs/iconKey",
		"templateDevice.icon":         "#/$defs/iconRef",
		"templateDevice.outlineColor": "#/$defs/hexColor",
		"templateDevice.fillColor":    "#/$defs/hexColor",
		"templateDevice.layer":        "#/$defs/layerId",
		"device.layer":                "#/$defs/layerId",
		"switch.layer":                "#/$defs/layerId",
		"layer.id":                    "#/$defs/layerId",
		"shape.fillColor":             "#/$defs/hexColor",
		"shape.outlineColor":          "#/$defs/hexColor",
		"shape.borderStyle":           "#/$defs/borderStyle",
		"iconNode.iconKey":            "#/$defs/iconKey",
		"iconNode.icon":               "#/$defs/iconRef",
		"line.color":                  "#/$defs/hexColor",
		"line.lineStyle":              "#/$defs/lineStyle",
		"node.shape":                  "#/$defs/shape",
		"node.icon":                   "#/$defs/iconNode",
		"node.line":                   "#/$defs/line",
	}

	got := make(map[string]string, len(want))

	for path := range want {
		def, property, _ := strings.Cut(path, ".")
		got[path], _ = mapAt(t, mapAt(t, mapAt(t, defs, def), "properties"), property)["$ref"].(string)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Schema() presentation field references mismatch (-want +got):\n%s", diff)
	}

	edgeStyle := mapAt(t, mapAt(t, mapAt(t, defs, "edge"), "properties"), "lineStyle")
	if diff := cmp.Diff([]string{"#/$defs/lineStyle"}, collectRefs(edgeStyle)); diff != "" {
		t.Errorf("Schema() $defs.edge.properties.lineStyle references mismatch (-want +got):\n%s", diff)
	}

	// Free text: a string and nothing that bounds it, besides what documents it.
	description := mapAt(t, mapAt(t, mapAt(t, defs, "group"), "properties"), "description")
	if description["type"] != "string" || len(description) != len([]string{"type", "title", "description", "examples"}) {
		t.Errorf("Schema() $defs.group.properties.description = %v, want free text", description)
	}

	// A list of topology names.
	included := mapAt(t, mapAt(t, mapAt(t, defs, "source"), "properties"), "includeTopologies")
	wantUnresolved := map[string]any{"type": "array", "items.pattern": mapAt(t, included, "items")["pattern"], "items.minLength": 1}
	unresolved := mapAt(t, mapAt(t, mapAt(t, defs, "source"), "properties"), "unresolvedIncludes")

	if diff := cmp.Diff(wantUnresolved, schemaValues(unresolved, wantUnresolved)); diff != "" {
		t.Errorf("Schema() $defs.source.properties.unresolvedIncludes mismatch (-want +got):\n%s", diff)
	}

	// None of them is required.
	for _, name := range []string{"templates", "icons", "layers"} {
		if containsAny(schema["required"], name) {
			t.Errorf("Schema() required = %v, want it without %q", schema["required"], name)
		}
	}
}

// TestSchemaStyles holds the style enums to the lists Document.Validate
// checks against, each with the empty default.
func TestSchemaStyles(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	want := map[string]any{
		"lineStyle.enum":   []any{"", "solid", "dashed", "dotted", "dash-dot"},
		"borderStyle.enum": []any{"", "solid", "dashed", "dotted", "double"},
	}
	if diff := cmp.Diff(want, schemaValues(defs, want)); diff != "" {
		t.Errorf("Schema() style enums mismatch (-want +got):\n%s", diff)
	}
}

// TestSchemaVisualNodes holds shape, icon and line nodes to Document.Validate: a required figure,
// exactly one icon, and a bounded list of points.
func TestSchemaVisualNodes(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	// A required rectangle or circle.
	wantShape := map[string]any{"required": []any{"shape"}, "properties.shape.enum": []any{"rectangle", "circle"}}
	if diff := cmp.Diff(wantShape, schemaValues(mapAt(t, defs, "shape"), wantShape)); diff != "" {
		t.Errorf("Schema() $defs.shape mismatch (-want +got):\n%s", diff)
	}

	branches, ok := mapAt(t, defs, "iconNode")["oneOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("Schema() $defs.iconNode = %v, want a oneOf of two branches", mapAt(t, defs, "iconNode"))
	}

	// Each branch requires a non-empty icon key or icon.
	for i, key := range []string{"iconKey", "icon"} {
		branch, _ := branches[i].(map[string]any)
		want := map[string]any{"required": []any{key}, "properties." + key + ".minLength": 1}

		if diff := cmp.Diff(want, schemaValues(branch, want)); diff != "" {
			t.Errorf("Schema() $defs.iconNode.oneOf branch for %s mismatch (-want +got):\n%s", key, diff)
		}
	}

	// A bounded list of positions.
	wantLine := map[string]any{
		"required":                     []any{"points"},
		"properties.points.minItems":   builder.MinLinePoints,
		"properties.points.maxItems":   builder.MaxLinePoints,
		"properties.points.items.$ref": "#/$defs/position",
	}
	if diff := cmp.Diff(wantLine, schemaValues(mapAt(t, defs, "line"), wantLine)); diff != "" {
		t.Errorf("Schema() $defs.line mismatch (-want +got):\n%s", diff)
	}
}

// TestSchemaPresentationPatterns checks what the patterns of colors, icon
// names and icon data take.
func TestSchemaPresentationPatterns(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")
	root := mapAt(t, schema, "properties")

	patterns := map[string]struct {
		pattern any
		yes, no []string
	}{
		"hexColor": {
			pattern: mapAt(t, defs, "hexColor")["pattern"],
			yes:     []string{"", "#2f6fbf", "#ABCDEF", "#000000"},
			no:      []string{"#abc", "red", "2f6fbf", "#2f6fbf80", "#2f6fbg", " #2f6fbf", "rgb(0,0,0)"},
		},
		"iconRef": {
			pattern: mapAt(t, defs, "iconRef")["pattern"],
			yes:     []string{"", iconFixtureName, "server", "PLC", "a@b.c_d-e", strings.Repeat("n", builder.MaxIconNameBytes)},
			no: []string{
				iconFixtureID, "plc icon", "plc/icon", "data:image/png;base64,AAAA", "ñ",
				strings.Repeat("n", builder.MaxIconNameBytes+1),
			},
		},
		"icons key": {
			pattern: mapAt(t, mapAt(t, root, "icons"), "propertyNames")["pattern"],
			yes:     []string{iconFixtureName, "plc", strings.Repeat("n", builder.MaxIconNameBytes)},
			no:      []string{"", iconFixtureID, "plc icon", strings.Repeat("n", builder.MaxIconNameBytes+1)},
		},
		"icon data": {
			pattern: mapAt(t, mapAt(t, mapAt(t, defs, "icon"), "properties"), "data")["pattern"],
			yes:     []string{iconFixtureData, "AAAA", "AA=="},
			no:      []string{"", "AA\nAA", "AA_A", "data:image/png;base64,AAAA", "<svg/>"},
		},
	}

	for name, test := range patterns {
		t.Run(name, func(t *testing.T) {
			pattern, ok := test.pattern.(string)
			if !ok {
				t.Fatalf("Schema() %s pattern = %v, want a string", name, test.pattern)
			}

			matcher := regexp.MustCompile(pattern)

			for _, text := range test.yes {
				if !matcher.MatchString(text) {
					t.Errorf("%s pattern %q matches %q = false, want true", name, pattern, text)
				}
			}

			for _, text := range test.no {
				if matcher.MatchString(text) {
					t.Errorf("%s pattern %q matches %q = true, want false", name, pattern, text)
				}
			}
		})
	}
}

// TestSchemaBoundsIconsAndTemplates holds the schema of custom icons and
// templates to the limits Document.Validate enforces.
func TestSchemaBoundsIconsAndTemplates(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")
	root := mapAt(t, schema, "properties")

	// A bounded map of icons.
	wantIcons := map[string]any{"maxProperties": builder.MaxDocumentIcons, "additionalProperties.$ref": "#/$defs/icon"}
	if diff := cmp.Diff(wantIcons, schemaValues(mapAt(t, root, "icons"), wantIcons)); diff != "" {
		t.Errorf("Schema() properties.icons mismatch (-want +got):\n%s", diff)
	}

	icon := mapAt(t, defs, "icon")
	iconProps := mapAt(t, icon, "properties")

	if !containsAny(icon["required"], "data") {
		t.Errorf("Schema() $defs.icon.required = %v, want it to include data", icon["required"])
	}

	// The key is the icon's name: an entry holds its data alone.
	if diff := cmp.Diff([]string{"data"}, slices.Sorted(maps.Keys(iconProps))); diff != "" {
		t.Errorf("Schema() $defs.icon properties mismatch (-want +got):\n%s", diff)
	}

	// The base64 text of the largest icon.
	if got := mapAt(t, iconProps, "data")["maxLength"]; got != 54616 {
		t.Errorf("Schema() $defs.icon.properties.data.maxLength = %v, want 54616", got)
	}

	// A bounded list of templates.
	wantTemplates := map[string]any{"maxItems": builder.MaxTemplates, "items.$ref": "#/$defs/template"}
	if diff := cmp.Diff(wantTemplates, schemaValues(mapAt(t, root, "templates"), wantTemplates)); diff != "" {
		t.Errorf("Schema() properties.templates mismatch (-want +got):\n%s", diff)
	}

	template := mapAt(t, defs, "template")
	for _, required := range []string{"id", "name", "device"} {
		if !containsAny(template["required"], required) {
			t.Errorf("Schema() $defs.template.required = %v, want it to include %q", template["required"], required)
		}
	}

	wantTemplate := map[string]any{
		"properties.name.minLength":        1,
		"properties.name.maxLength":        builder.MaxTemplateNameBytes,
		"properties.description.maxLength": builder.MaxTemplateDescriptionBytes,
	}
	if diff := cmp.Diff(wantTemplate, schemaValues(template, wantTemplate)); diff != "" {
		t.Errorf("Schema() $defs.template bounds mismatch (-want +got):\n%s", diff)
	}

	// A template's spec is a device's.
	templateDevice := mapAt(t, defs, "templateDevice")
	if diff := cmp.Diff([]any{"spec"}, templateDevice["required"]); diff != "" {
		t.Errorf("Schema() $defs.templateDevice.required mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(
		mapAt(t, mapAt(t, mapAt(t, defs, "device"), "properties"), "spec"),
		mapAt(t, mapAt(t, templateDevice, "properties"), "spec"),
	); diff != "" {
		t.Errorf("Schema() $defs.templateDevice spec mismatch with $defs.device spec (-want +got):\n%s", diff)
	}
}

func TestSchemaDiscriminatesNodeKinds(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")
	node := mapAt(t, defs, "node")

	branches, ok := node["allOf"].([]any)
	if !ok || len(branches) != 7 {
		t.Errorf("Schema() $defs.node.allOf = %v, want 7 per-kind branches", node["allOf"])
	}

	seen := map[string]bool{}

	for _, entry := range branches {
		branch, ok := entry.(map[string]any)
		if !ok {
			t.Errorf("Schema() $defs.node.allOf branch = %v, want an object", entry)

			continue
		}

		condition := mapAt(t, mapAt(t, branch, "if"), "properties")
		kind, _ := mapAt(t, condition, "kind")["const"].(string)

		then := mapAt(t, branch, "then")
		if !containsAny(then["required"], kind) {
			t.Errorf("Schema() $defs.node branch for kind %q requires %v, want its payload", kind, then["required"])
		}

		seen[kind] = true
	}

	for _, kind := range []string{"device", "switch", "note", "group", "shape", "icon", "line"} {
		if !seen[kind] {
			t.Errorf("Schema() $defs.node has no branch for kind %q", kind)
		}
	}
}

// TestSchemaHasNoUnresolvedReferences checks that every reference of each Builder schema names a
// definition it holds, so a field a definition gains brings its own definitions along.
func TestSchemaHasNoUnresolvedReferences(t *testing.T) {
	for _, tc := range builderSchemas() {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.json()
			if err != nil {
				t.Fatalf("encoding the schema: %v", err)
			}

			if bytes.Contains(data, []byte("#/components/")) {
				t.Errorf("%s schema contains OpenAPI component references, want none", tc.name)
			}

			var schema map[string]any

			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatalf("decoding the schema: %v", err)
			}

			defs := mapAt(t, schema, "$defs")

			refs := collectRefs(schema)
			if len(refs) == 0 {
				t.Errorf("%s schema contains no references, want some", tc.name)
			}

			for _, ref := range refs {
				name, local := strings.CutPrefix(ref, "#/$defs/")
				if _, held := defs[name]; !local || !held {
					t.Errorf("%s schema reference %q does not resolve within the schema", tc.name, ref)
				}
			}

			for _, name := range tc.holds {
				if _, held := defs[name]; !held {
					t.Errorf("%s schema has no $defs/%s", tc.name, name)
				}
			}
		})
	}
}

func TestSchemaJSONIsDeterministicAndValidJSON(t *testing.T) {
	first, err := builder.SchemaJSON()
	if err != nil {
		t.Fatalf("marshaling schema: %v", err)
	}

	second, err := builder.SchemaJSON()
	if err != nil {
		t.Fatalf("marshaling schema: %v", err)
	}

	if diff := cmp.Diff(string(first), string(second)); diff != "" {
		t.Errorf("SchemaJSON() differs between calls (-first +second):\n%s", diff)
	}

	var decoded map[string]any

	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Errorf("json.Unmarshal(SchemaJSON()) error = %v, want nil", err)
	}
}

func TestFrontendSchemaBundleMatchesSchemaJSON(t *testing.T) {
	want, err := builder.SchemaJSON()
	if err != nil {
		t.Fatalf("marshaling schema: %v", err)
	}

	if *updateFrontendSchema {
		if err := os.WriteFile(frontendSchemaBundle, want, 0o600); err != nil {
			t.Fatalf("writing %s: %v", frontendSchemaBundle, err)
		}
	}

	got, err := os.ReadFile(frontendSchemaBundle)
	if err != nil {
		t.Fatalf("reading %s: %v", frontendSchemaBundle, err)
	}

	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf(
			"%s mismatch with SchemaJSON() (-want +got); regenerate it with make generate-builder-schema "+
				"(or make generate) in src/go:\n%s",
			frontendSchemaBundle, diff,
		)
	}
}

func TestSchemaReturnsIndependentCopies(t *testing.T) {
	first := mustSchema(t)
	first["$id"] = "mutated"

	if got := mustSchema(t)["$id"]; got != builder.SchemaURI {
		t.Errorf("Schema() $id after changing an earlier result = %v, want %q", got, builder.SchemaURI)
	}
}

func TestBundleOpenAPIDefsConvertsNullableSchemas(t *testing.T) {
	defs, err := builder.PhenixDefs()
	if err != nil {
		t.Fatalf("bundling phenix defs: %v", err)
	}

	external := mapAt(t, defs, builder.PhenixDefPrefix+"external_node")
	hardware := mapAt(t, mapAt(t, external, "properties"), "hardware")

	for _, kind := range []string{"object", "null"} {
		if !containsAny(hardware["type"], kind) {
			t.Errorf("PhenixDefs() external_node hardware type = %v, want it to include %q", hardware["type"], kind)
		}
	}

	address := mapAt(t, defs, builder.PhenixDefPrefix+"iface_address")
	dns := mapAt(t, mapAt(t, address, "properties"), "dns")

	if variants, ok := dns["anyOf"].([]any); !ok || len(variants) != 2 {
		t.Errorf("PhenixDefs() iface_address dns = %v, want the nullable oneOf wrapped in an anyOf of two", dns)
	}

	if findKey(defs, "nullable") {
		t.Error("PhenixDefs() holds the OpenAPI nullable keyword, want none")
	}
}

// mustSchema builds the builder schema, failing the test on error.
func mustSchema(t *testing.T) map[string]any {
	t.Helper()

	schema, err := builder.Schema()
	if err != nil {
		t.Fatalf("building schema: %v", err)
	}

	return schema
}

// mapAt returns the object stored under key, failing the test when absent.
func mapAt(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is not an object: %v", key, parent[key])
	}

	return value
}

// schemaValues returns the values of a schema object at the dot-separated paths that are the keys of
// want, nil where a path is absent, to compare several keywords at once.
func schemaValues(object map[string]any, want map[string]any) map[string]any {
	got := make(map[string]any, len(want))

	for path := range want {
		var value any = object

		for key := range strings.SplitSeq(path, ".") {
			parent, _ := value.(map[string]any)
			value = parent[key]
		}

		got[path] = value
	}

	return got
}

// containsAny reports whether an any-typed list contains the given string.
func containsAny(list any, want string) bool {
	values, ok := list.([]any)
	if !ok {
		return false
	}

	for _, value := range values {
		if fmt.Sprint(value) == want {
			return true
		}
	}

	return false
}

// collectRefs gathers every $ref string found in a schema tree.
func collectRefs(value any) []string {
	var refs []string

	switch typed := value.(type) {
	case map[string]any:
		for key, val := range typed {
			if key == "$ref" {
				if ref, ok := val.(string); ok {
					refs = append(refs, ref)

					continue
				}
			}

			refs = append(refs, collectRefs(val)...)
		}
	case []any:
		for _, val := range typed {
			refs = append(refs, collectRefs(val)...)
		}
	}

	return refs
}

func findKey(value any, want string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, val := range typed {
			if key == want || findKey(val, want) {
				return true
			}
		}
	case []any:
		for _, val := range typed {
			if findKey(val, want) {
				return true
			}
		}
	}

	return false
}

func TestSchemaGeometryBounds(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	for _, path := range [][2]string{{"size", "width"}, {"size", "height"}, {"viewport", "zoom"}} {
		def := mapAt(t, mapAt(t, mapAt(t, defs, path[0]), "properties"), path[1])

		if _, hasMinimum := def["minimum"]; def["exclusiveMinimum"] != 0 || hasMinimum {
			t.Errorf("Schema() $defs.%s.properties.%s = %v, want exclusiveMinimum 0 and no minimum", path[0], path[1], def)
		}
	}

	// An integer from MinGridSize to MaxGridSize.
	wantSize := map[string]any{"type": "integer", "minimum": builder.MinGridSize, "maximum": builder.MaxGridSize}
	size := mapAt(t, mapAt(t, mapAt(t, defs, "grid"), "properties"), "size")

	if diff := cmp.Diff(wantSize, schemaValues(size, wantSize)); diff != "" {
		t.Errorf("Schema() $defs.grid.properties.size mismatch (-want +got):\n%s", diff)
	}
}

func TestSchemaSourceCarriesDigestAndUpdatedAt(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")
	source := mapAt(t, mapAt(t, defs, "source"), "properties")

	const want = `^sha256:[0-9a-f]{64}$`
	if got := mapAt(t, source, "digest")["pattern"]; got != want {
		t.Errorf("Schema() $defs.source.properties.digest.pattern = %v, want %s", got, want)
	}

	if _, ok := source["updatedAt"]; !ok {
		t.Error("Schema() $defs.source has no updatedAt property")
	}
}

// TestSchemaScenarios pins the scenarios schema: an optional, bounded list of config names.
func TestSchemaScenarios(t *testing.T) {
	schema := mustSchema(t)
	scenarios := mapAt(t, mapAt(t, schema, "properties"), "scenarios")

	if containsAny(schema["required"], "scenarios") {
		t.Errorf("Schema() required = %v, want it without scenarios", schema["required"])
	}

	// A list of distinct names of 1 to MaxScenarioNameBytes characters.
	want := map[string]any{
		"type":            "array",
		"maxItems":        builder.MaxScenarios,
		"uniqueItems":     true,
		"items.type":      "string",
		"items.minLength": 1,
		"items.maxLength": builder.MaxScenarioNameBytes,
	}
	if diff := cmp.Diff(want, schemaValues(scenarios, want)); diff != "" {
		t.Errorf("Schema() properties.scenarios mismatch (-want +got):\n%s", diff)
	}

	name := mapAt(t, scenarios, "items")

	pattern, ok := name["pattern"].(string)
	if !ok {
		t.Fatalf("Schema() properties.scenarios.items.pattern = %v, want a string", name["pattern"])
	}

	matcher := regexp.MustCompile(pattern)

	for _, valid := range []string{"ntp", "Ab_9@x.y-z"} {
		if !matcher.MatchString(valid) || !builder.IsConfigName(valid) {
			t.Errorf("scenario name %q: pattern match = %t, IsConfigName = %t, want both true",
				valid, matcher.MatchString(valid), builder.IsConfigName(valid))
		}
	}

	for _, invalid := range []string{"", "two words", "Scenario/ntp", "ntp\n"} {
		if matcher.MatchString(invalid) || builder.IsConfigName(invalid) {
			t.Errorf("scenario name %q: pattern match = %t, IsConfigName = %t, want both false",
				invalid, matcher.MatchString(invalid), builder.IsConfigName(invalid))
		}
	}
}

func TestSchemaBundlesPhenixV1(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	components := []string{
		"Experiment", "Topology", "minimega_node", "external_node",
		"iface", "iface_address", "iface_rulesets", "static_iface", "dhcp_iface",
		"serial_iface", "Image", "Role", "User",
	}

	for _, name := range components {
		if _, ok := defs[builder.PhenixDefPrefix+name]; !ok {
			t.Errorf("Schema() has no $defs/%s%s", builder.PhenixDefPrefix, name)
		}
	}

	// A document holds no scenario content, which the v2 schemas were
	// bundled for.
	for name := range defs {
		if strings.HasPrefix(name, "phenix.v2.") {
			t.Errorf("Schema() bundles $defs/%s, which no part of a document refers to", name)
		}
	}

	// Device specs stay on the v1 node schemas.
	device := mapAt(t, defs, "device")
	spec := mapAt(t, mapAt(t, device, "properties"), "spec")

	variants, ok := spec["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Errorf("Schema() $defs.device.properties.spec = %v, want a oneOf of two node variants", spec)
	}

	for _, entry := range variants {
		variant, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		if reference, _ := variant["$ref"].(string); !strings.Contains(reference, builder.PhenixDefPrefix) {
			t.Errorf("Schema() device spec variant $ref = %q, want a v1 node schema", reference)
		}
	}
}

func TestPhenixDefsRewritesReferences(t *testing.T) {
	for _, bundle := range []struct {
		prefix string
		defs   func() (map[string]any, error)
	}{
		{prefix: builder.PhenixDefPrefix, defs: builder.PhenixDefs},
	} {
		t.Run(bundle.prefix, func(t *testing.T) {
			defs, err := bundle.defs()
			if err != nil {
				t.Fatalf("bundling phenix defs: %v", err)
			}

			references := 0

			for name, def := range defs {
				if !strings.HasPrefix(name, bundle.prefix) {
					t.Errorf("PhenixDefs() definition %q, want the prefix %q", name, bundle.prefix)
				}

				for _, reference := range collectRefs(def) {
					references++

					want := "#/$defs/" + bundle.prefix
					if !strings.HasPrefix(reference, want) {
						t.Errorf("PhenixDefs() definition %q holds reference %q, want prefix %q", name, reference, want)
					}

					if _, ok := defs[strings.TrimPrefix(reference, "#/$defs/")]; !ok {
						t.Errorf("PhenixDefs() reference %q does not resolve inside the bundle", reference)
					}
				}
			}

			// static_iface, for one, refers to iface through allOf.
			if references == 0 {
				t.Error("PhenixDefs() holds no references, want some")
			}
		})
	}
}

// TestSchemaLayers pins the schema of the layers of the document, and of
// the layer a node names: bounded the way Document.Validate bounds them.
func TestSchemaLayers(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")

	// A list of at most MaxLayers layers.
	wantLayers := map[string]any{"type": "array", "maxItems": builder.MaxLayers, "items.$ref": "#/$defs/layer"}
	if diff := cmp.Diff(wantLayers, schemaValues(mapAt(t, mapAt(t, schema, "properties"), "layers"), wantLayers)); diff != "" {
		t.Errorf("Schema() properties.layers mismatch (-want +got):\n%s", diff)
	}

	// An integer from 0 to MaxLayerID.
	wantID := map[string]any{"type": "integer", "minimum": 0, "maximum": builder.MaxLayerID}
	if diff := cmp.Diff(wantID, schemaValues(mapAt(t, defs, "layerId"), wantID)); diff != "" {
		t.Errorf("Schema() $defs.layerId mismatch (-want +got):\n%s", diff)
	}

	layer := mapAt(t, defs, "layer")
	for _, required := range []string{"id", "label"} {
		if !containsAny(layer["required"], required) {
			t.Errorf("Schema() $defs.layer.required = %v, want it to include %q", layer["required"], required)
		}
	}

	// A string of 1 to MaxLayerLabelBytes characters.
	label := mapAt(t, mapAt(t, layer, "properties"), "label")
	wantLabel := map[string]any{"type": "string", "minLength": 1, "maxLength": builder.MaxLayerLabelBytes}

	if diff := cmp.Diff(wantLabel, schemaValues(label, wantLabel)); diff != "" {
		t.Errorf("Schema() $defs.layer.properties.label mismatch (-want +got):\n%s", diff)
	}

	pattern, ok := label["pattern"].(string)
	if !ok {
		t.Fatalf("Schema() $defs.layer.properties.label.pattern = %v, want a string", label["pattern"])
	}

	matcher := regexp.MustCompile(pattern)

	// The pattern accepts the labels Document.Validate accepts, and refuses
	// the blank ones and those with control characters.
	for name, test := range map[string]struct {
		label string
		valid bool
	}{
		"word":                       {label: "Control", valid: true},
		"padded words":               {label: " Basic control ", valid: true},
		"accented letter":            {label: "Zone \u00e9", valid: true},
		"no-break space then letter": {label: "\u00a0x", valid: true},
		"empty":                      {label: "", valid: false},
		"space":                      {label: " ", valid: false},
		"no-break space":             {label: "\u00a0", valid: false},
		"em space and next line":     {label: "\u2003\u0085", valid: false},
		"tab":                        {label: "Basic\tcontrol", valid: false},
		"newline":                    {label: "a\nb", valid: false},
	} {
		t.Run(name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			doc.Layers = []builder.Layer{{ID: layerID(1), Label: test.label}}

			err := doc.Validate()
			if matcher.MatchString(test.label) != test.valid || (err == nil) != test.valid {
				t.Errorf("layer label %q: pattern match = %t, Validate() error = %v, want valid = %t",
					test.label, matcher.MatchString(test.label), err, test.valid)
			}
		})
	}

	// The messages give the range of a layer ID.
	doc := loadDocumentFixture(t, "document.json")
	doc.Layers = []builder.Layer{{ID: layerID(builder.MaxLayerID + 1), Label: "Top"}}

	want := fmt.Sprintf("from 0 to %d", builder.MaxLayerID)
	if issues := builder.ErrorIssues(doc.Validate()); len(issues) != 1 || !strings.Contains(issues[0].Message, want) {
		t.Errorf("Validate() of layer %d issues = %v, want one that says %q", builder.MaxLayerID+1, issues, want)
	}
}
