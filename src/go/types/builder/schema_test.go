package builder_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

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
	"rewrite the web UI builder schema bundle from builder.SchemaJSON",
)

func TestSchemaHeader(t *testing.T) {
	schema := mustSchema(t)

	if got := schema["$id"]; got != builder.SchemaURI {
		t.Fatalf("$id = %v, want %q", got, builder.SchemaURI)
	}

	if got := schema["$schema"]; got != builder.SchemaDialect {
		t.Fatalf("$schema = %v, want %q", got, builder.SchemaDialect)
	}

	properties := mapAt(t, schema, "properties")

	schemaProperty := mapAt(t, properties, "$schema")
	if got := schemaProperty["const"]; got != builder.SchemaURI {
		t.Fatalf("properties.$schema.const = %v, want %q", got, builder.SchemaURI)
	}

	if _, ok := properties["schema"]; ok {
		t.Fatal("schema still exposes a legacy schema property")
	}

	revision := mapAt(t, properties, "revision")
	if got := revision["const"]; got != builder.SchemaRevision {
		t.Fatalf("properties.revision.const = %v, want %d", got, builder.SchemaRevision)
	}

	if got := schema["additionalProperties"]; got != false {
		t.Fatalf("additionalProperties = %v, want false", got)
	}

	for _, required := range []string{
		"$schema", "revision", "metadata", "nodes", "networks", "edges", "viewport", "grid",
	} {
		if !containsAny(schema["required"], required) {
			t.Fatalf("required does not include %q: %v", required, schema["required"])
		}
	}

	metadata := mapAt(t, mapAt(t, schema, "$defs"), "metadata")
	if mapAt(t, properties, "metadata")["$ref"] != "#/$defs/metadata" || !reflect.DeepEqual(metadata["required"], []any{"id"}) ||
		metadata["additionalProperties"] != false {
		t.Fatalf("metadata is not a required object that needs only its id: %v", metadata)
	}
}

// TestSchemaDocumentsEveryProperty holds every part of the schema the
// Builder owns to the documentation its readers rely on: each definition and
// each property has a title, a description and at least one example. The
// phenix config schemas bundled under $defs keep their own documentation and
// are left out. That every example is valid against its schema is checked
// with ajv by the front end's schema-examples.test.js.
func TestSchemaDocumentsEveryProperty(t *testing.T) {
	schema := mustSchema(t)

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
		t.Fatalf("these lack a title, a description or examples:\n  %s", strings.Join(missing, "\n  "))
	}
}

// undocumented walks a Builder-owned schema and returns the paths of the
// parts it finds without a title, a description or a non-empty list of
// examples: the schema itself when own is set, as it is for a definition or
// a property, then the properties it declares, also those of the items of a
// list and of the values of a map. Conditions (if, then, not) declare no
// properties of their own and are not walked.
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
	} {
		def := mapAt(t, defs, name)

		if def["type"] == "object" && def["additionalProperties"] != false {
			t.Fatalf("$defs.%s does not forbid additional properties", name)
		}
	}

	edge := mapAt(t, defs, "edge")
	edgeProps := mapAt(t, edge, "properties")

	for _, name := range []string{
		"sourceNodeId", "sourceHandleId", "targetNodeId", "targetHandleId", "networkId",
		"label", "color", "lineStyle", "route",
	} {
		if _, ok := edgeProps[name]; !ok {
			t.Fatalf("edge schema has no %q property", name)
		}
	}

	route := mapAt(t, edgeProps, "route")
	if route["minItems"] != 2 || mapAt(t, route, "items")["$ref"] != "#/$defs/position" {
		t.Fatalf("edge route is not a list of at least two positions: %v", route)
	}

	layout := mapAt(t, mapAt(t, mustSchema(t), "properties"), "layout")
	if layout["type"] != "string" || containsAny(mustSchema(t)["required"], "layout") {
		t.Fatalf("document layout is not an optional string: %v", layout)
	}

	iconKey := mapAt(t, defs, "iconKey")

	enum, ok := iconKey["enum"].([]any)
	if !ok {
		t.Fatalf("iconKey has no enum: %v", iconKey)
	}

	if len(enum) != len(builder.IconKeys())+1 {
		t.Fatalf("iconKey enum %v does not match registry %v", enum, builder.IconKeys())
	}

	for _, key := range builder.IconKeys() {
		if !containsAny(iconKey["enum"], key) {
			t.Fatalf("iconKey enum is missing %q", key)
		}
	}
}

// propertyNames returns the sorted property names of an object schema.
func propertyNames(t *testing.T, def map[string]any) []string {
	t.Helper()

	return slices.Sorted(maps.Keys(mapAt(t, def, "properties")))
}

// TestSchemaPropertiesMatchDocument holds every object of the document to
// the fields its Go type has: the decoder refuses a key the type lacks, and
// the schema one it does not list, so the two must name the same.
func TestSchemaPropertiesMatchDocument(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")

	for name, value := range map[string]any{
		"metadata":       builder.Metadata{},
		"device":         builder.Device{},
		"switch":         builder.Switch{},
		"note":           builder.Note{},
		"group":          builder.Group{},
		"node":           builder.Node{},
		"network":        builder.Network{},
		"edge":           builder.Edge{},
		"source":         builder.Source{},
		"icon":           builder.Icon{},
		"template":       builder.Template{},
		"templateDevice": builder.TemplateDevice{},
	} {
		want := slices.Sorted(maps.Keys(jsonFields(t, value)))

		if got := propertyNames(t, mapAt(t, defs, name)); !slices.Equal(got, want) {
			t.Fatalf("$defs.%s has the properties %v, and its Go type the fields %v", name, got, want)
		}
	}

	want := slices.Sorted(maps.Keys(jsonFields(t, builder.Document{})))

	if got := propertyNames(t, schema); !slices.Equal(got, want) {
		t.Fatalf("the schema has the root properties %v, and Document the fields %v", got, want)
	}
}

// TestSchemaPresentationFields pins the schema of the presentation fields,
// each bounded the way Document.Validate bounds it.
func TestSchemaPresentationFields(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")

	for path, want := range map[string]string{
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
	} {
		def, property, _ := strings.Cut(path, ".")

		if got := mapAt(t, mapAt(t, mapAt(t, defs, def), "properties"), property)["$ref"]; got != want {
			t.Fatalf("%s refers to %v, want %s", path, got, want)
		}
	}

	edgeStyle := mapAt(t, mapAt(t, mapAt(t, defs, "edge"), "properties"), "lineStyle")
	if got := collectRefs(edgeStyle); !slices.Equal(got, []string{"#/$defs/lineStyle"}) {
		t.Fatalf("edge.lineStyle refers to %v", got)
	}

	// Free text: a string and nothing that bounds it, besides what documents
	// it.
	if got := mapAt(t, mapAt(t, mapAt(t, defs, "group"), "properties"), "description"); got["type"] != "string" ||
		len(got) != len([]string{"type", "title", "description", "examples"}) {
		t.Fatalf("group.description is not free text: %v", got)
	}

	unresolved := mapAt(t, mapAt(t, mapAt(t, defs, "source"), "properties"), "unresolvedIncludes")
	included := mapAt(t, mapAt(t, mapAt(t, defs, "source"), "properties"), "includeTopologies")

	if unresolved["type"] != "array" || mapAt(t, unresolved, "items")["pattern"] != mapAt(t, included, "items")["pattern"] ||
		mapAt(t, unresolved, "items")["minLength"] != 1 {
		t.Fatalf("unresolvedIncludes is not a list of topology names: %v", unresolved)
	}

	// None of them is required.
	for _, name := range []string{"templates", "icons"} {
		if containsAny(schema["required"], name) {
			t.Fatalf("the schema requires %q", name)
		}
	}
}

// TestSchemaStyles holds the style enums to the lists Document.Validate
// checks against, each with the empty default.
func TestSchemaStyles(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	for name, values := range map[string][]string{"lineStyle": builder.LineStyles(), "borderStyle": builder.BorderStyles()} {
		want := append([]any{""}, anyOf(values)...)

		if got := mapAt(t, defs, name)["enum"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s enum = %v, want %v", name, got, want)
		}
	}

	if got, want := strings.Join(builder.LineStyles(), " "), "solid dashed dotted dash-dot"; got != want {
		t.Fatalf("line styles are %q, want %q", got, want)
	}

	if got, want := strings.Join(builder.BorderStyles(), " "), "solid dashed dotted double"; got != want {
		t.Fatalf("border styles are %q, want %q", got, want)
	}

	// Each call returns a list of its own.
	builder.LineStyles()[0] = "changed"
	builder.BorderStyles()[0] = "changed"

	if builder.LineStyles()[0] != "solid" || builder.BorderStyles()[0] != "solid" {
		t.Fatal("the style lists are shared state")
	}
}

// TestSchemaPresentationPatterns checks what the patterns of colors, icon
// ids and icon data take.
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
			yes:     []string{"", iconFixtureID},
			no:      []string{"sha256:abc", strings.ToUpper(iconFixtureID), "server", "data:image/png;base64,AAAA"},
		},
		"icons key": {
			pattern: mapAt(t, mapAt(t, root, "icons"), "propertyNames")["pattern"],
			yes:     []string{iconFixtureID},
			no:      []string{"", "plc", iconFixtureID + "0"},
		},
		"icon data": {
			pattern: mapAt(t, mapAt(t, mapAt(t, defs, "icon"), "properties"), "data")["pattern"],
			yes:     []string{iconFixtureData, "AAAA", "AA=="},
			no:      []string{"", "AA\nAA", "AA_A", "data:image/png;base64,AAAA", "<svg/>"},
		},
	}

	for name, test := range patterns {
		pattern, ok := test.pattern.(string)
		if !ok {
			t.Fatalf("%s has no pattern", name)
		}

		matcher := regexp.MustCompile(pattern)

		for _, text := range test.yes {
			if !matcher.MatchString(text) {
				t.Fatalf("the %s pattern rejects %q", name, text)
			}
		}

		for _, text := range test.no {
			if matcher.MatchString(text) {
				t.Fatalf("the %s pattern accepts %q", name, text)
			}
		}
	}
}

// TestSchemaBoundsIconsAndTemplates holds the schema of custom icons and
// templates to the limits Document.Validate enforces.
func TestSchemaBoundsIconsAndTemplates(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")
	root := mapAt(t, schema, "properties")

	icons := mapAt(t, root, "icons")
	if icons["maxProperties"] != builder.MaxDocumentIcons || mapAt(t, icons, "additionalProperties")["$ref"] != "#/$defs/icon" {
		t.Fatalf("icons is not a bounded map of icons: %v", icons)
	}

	icon := mapAt(t, defs, "icon")
	iconProps := mapAt(t, icon, "properties")

	if !containsAny(icon["required"], "data") || containsAny(icon["required"], "name") {
		t.Fatalf("an icon does not require only its data: %v", icon["required"])
	}

	if got := mapAt(t, iconProps, "name")["maxLength"]; got != builder.MaxIconNameBytes {
		t.Fatalf("icon name maxLength = %v, want %d", got, builder.MaxIconNameBytes)
	}

	// The base64 text of the largest icon.
	if got := mapAt(t, iconProps, "data")["maxLength"]; got != 54616 {
		t.Fatalf("icon data maxLength = %v, want 54616", got)
	}

	templates := mapAt(t, root, "templates")
	if templates["maxItems"] != builder.MaxTemplates || mapAt(t, templates, "items")["$ref"] != "#/$defs/template" {
		t.Fatalf("templates is not a bounded list of templates: %v", templates)
	}

	template := mapAt(t, defs, "template")
	for _, required := range []string{"id", "name", "device"} {
		if !containsAny(template["required"], required) {
			t.Fatalf("a template does not require %q: %v", required, template["required"])
		}
	}

	templateProps := mapAt(t, template, "properties")
	if name := mapAt(t, templateProps, "name"); name["minLength"] != 1 || name["maxLength"] != builder.MaxTemplateNameBytes {
		t.Fatalf("template name is not bounded: %v", name)
	}

	if got := mapAt(t, templateProps, "description")["maxLength"]; got != builder.MaxTemplateDescriptionBytes {
		t.Fatalf("template description maxLength = %v, want %d", got, builder.MaxTemplateDescriptionBytes)
	}

	// A template's spec is a device's.
	templateDevice := mapAt(t, defs, "templateDevice")
	if !reflect.DeepEqual(templateDevice["required"], []any{"spec"}) {
		t.Fatalf("a template device does not require only its spec: %v", templateDevice["required"])
	}

	if got, want := mapAt(t, mapAt(t, templateDevice, "properties"), "spec"),
		mapAt(t, mapAt(t, mapAt(t, defs, "device"), "properties"), "spec"); !reflect.DeepEqual(got, want) {
		t.Fatalf("a template's spec is not a device's:\nwant: %v\ngot:  %v", want, got)
	}
}

// anyOf converts strings to the []any form a decoded schema has.
func anyOf(values []string) []any {
	out := make([]any, len(values))

	for i, value := range values {
		out[i] = value
	}

	return out
}

func TestSchemaDiscriminatesNodeKinds(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")
	node := mapAt(t, defs, "node")

	branches, ok := node["allOf"].([]any)
	if !ok || len(branches) != 4 {
		t.Fatalf("node schema has no per-kind branches: %v", node["allOf"])
	}

	seen := map[string]bool{}

	for _, entry := range branches {
		branch, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("branch is not an object: %v", entry)
		}

		condition := mapAt(t, mapAt(t, branch, "if"), "properties")
		kind, _ := mapAt(t, condition, "kind")["const"].(string)

		then := mapAt(t, branch, "then")
		if !containsAny(then["required"], kind) {
			t.Fatalf("branch for kind %q does not require its payload: %v", kind, then)
		}

		seen[kind] = true
	}

	for _, kind := range []string{"device", "switch", "note", "group"} {
		if !seen[kind] {
			t.Fatalf("node schema does not discriminate kind %q", kind)
		}
	}
}

func TestSchemaHasNoUnresolvedReferences(t *testing.T) {
	schema := mustSchema(t)
	defs := mapAt(t, schema, "$defs")

	data, err := builder.SchemaJSON()
	if err != nil {
		t.Fatalf("marshaling schema: %v", err)
	}

	if bytes.Contains(data, []byte("#/components/")) {
		t.Fatal("schema still contains OpenAPI component references")
	}

	refs := collectRefs(schema)
	if len(refs) == 0 {
		t.Fatal("schema contains no references")
	}

	for _, ref := range refs {
		name, found := strings.CutPrefix(ref, "#/$defs/")
		if !found {
			t.Fatalf("reference %q is not local", ref)
		}

		if _, ok := defs[name]; !ok {
			t.Fatalf("reference %q does not resolve", ref)
		}
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

	if !bytes.Equal(first, second) {
		t.Fatal("SchemaJSON is not deterministic")
	}

	var decoded map[string]any

	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
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

	if !bytes.Equal(got, want) {
		t.Fatalf(
			"%s is out of date; regenerate it with make generate-builder-schema (or make generate) in src/go",
			frontendSchemaBundle,
		)
	}
}

func TestSchemaReturnsIndependentCopies(t *testing.T) {
	first := mustSchema(t)
	first["$id"] = "mutated"

	if got := mustSchema(t)["$id"]; got != builder.SchemaURI {
		t.Fatalf("Schema returned shared state: $id = %v", got)
	}
}

func TestBundleOpenAPIDefsConvertsNullableSchemas(t *testing.T) {
	defs, err := builder.PhenixDefs()
	if err != nil {
		t.Fatalf("bundling phenix defs: %v", err)
	}

	external := mapAt(t, defs, builder.PhenixDefPrefix+"external_node")
	hardware := mapAt(t, mapAt(t, external, "properties"), "hardware")

	if !containsAny(hardware["type"], "object") ||
		!containsAny(hardware["type"], "null") {
		t.Fatalf("nullable object type was not converted: %v", hardware["type"])
	}

	address := mapAt(t, defs, builder.PhenixDefPrefix+"iface_address")
	dns := mapAt(t, mapAt(t, address, "properties"), "dns")
	variants, ok := dns["anyOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("nullable oneOf schema was not wrapped: %v", dns)
	}

	if findKey(defs, "nullable") {
		t.Fatal("bundled schema still contains the OpenAPI nullable keyword")
	}
}

func TestBundleOpenAPIDefsDropsNullPatterns(t *testing.T) {
	defs, err := builder.PhenixDefs()
	if err != nil {
		t.Fatalf("bundling phenix defs: %v", err)
	}

	serial := mapAt(t, defs, builder.PhenixDefPrefix+"serial_iface")
	device := mapAt(t, mapAt(t, serial, "properties"), "device")

	if _, ok := device["pattern"]; ok {
		t.Fatalf("serial device retains an invalid null pattern: %v", device)
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

func TestSchemaIdentifierIsUUID(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")
	identifier := mapAt(t, defs, "identifier")

	if got := identifier["format"]; got != "uuid" {
		t.Fatalf("identifier format = %v, want uuid", got)
	}

	pattern, ok := identifier["pattern"].(string)
	if !ok {
		t.Fatalf("identifier has no pattern: %v", identifier)
	}

	matcher := regexp.MustCompile(pattern)

	for _, valid := range []string{
		builder.NamespaceUUID(),
		builder.DeviceNodeID("router"),
		"7f9c2ba4-1e3b-4b1f-9f2e-2b7a5c1d8e4a",
	} {
		if !matcher.MatchString(valid) {
			t.Fatalf("identifier pattern rejects %q", valid)
		}
	}

	for _, invalid := range []string{
		"dev-router",
		"",
		"00000000-0000-0000-0000-000000000000",
		"49d876d1-571a-5b5c-11b7-aadf5bb5209c",
	} {
		if matcher.MatchString(invalid) {
			t.Fatalf("identifier pattern accepts %q", invalid)
		}
	}
}

func TestSchemaGeometryMinimaAreStrict(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	cases := map[string][2]string{
		"size.width":    {"size", "width"},
		"size.height":   {"size", "height"},
		"viewport.zoom": {"viewport", "zoom"},
		"grid.size":     {"grid", "size"},
	}

	for name, path := range cases {
		def := mapAt(t, mapAt(t, mapAt(t, defs, path[0]), "properties"), path[1])

		if got, ok := def["exclusiveMinimum"]; !ok || got != 0 {
			t.Fatalf("%s exclusiveMinimum = %v, want 0", name, got)
		}

		if _, ok := def["minimum"]; ok {
			t.Fatalf("%s still allows zero via minimum", name)
		}
	}
}

func TestSchemaSourceCarriesDigestAndUpdatedAt(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")
	source := mapAt(t, mapAt(t, defs, "source"), "properties")

	digest := mapAt(t, source, "digest")
	if got := digest["pattern"]; got != `^sha256:[0-9a-f]{64}$` {
		t.Fatalf("source digest pattern = %v", got)
	}

	if _, ok := source["updatedAt"]; !ok {
		t.Fatal("source schema has no updatedAt property")
	}
}

// TestSchemaScenarios pins the schema of the document's scenarios: an
// optional list of config names, bounded the way Document.Validate bounds
// it.
func TestSchemaScenarios(t *testing.T) {
	schema := mustSchema(t)
	scenarios := mapAt(t, mapAt(t, schema, "properties"), "scenarios")

	if containsAny(schema["required"], "scenarios") {
		t.Fatal("scenarios is required")
	}

	if scenarios["type"] != "array" || scenarios["maxItems"] != builder.MaxScenarios || scenarios["uniqueItems"] != true {
		t.Fatalf("scenarios is not a list of at most %d distinct items: %v", builder.MaxScenarios, scenarios)
	}

	name := mapAt(t, scenarios, "items")
	if name["type"] != "string" || name["minLength"] != 1 || name["maxLength"] != builder.MaxScenarioNameBytes {
		t.Fatalf("a scenario name is not a string of 1 to %d characters: %v", builder.MaxScenarioNameBytes, name)
	}

	pattern, ok := name["pattern"].(string)
	if !ok {
		t.Fatalf("a scenario name has no pattern: %v", name)
	}

	matcher := regexp.MustCompile(pattern)

	for _, valid := range []string{"ntp", "Ab_9@x.y-z"} {
		if !matcher.MatchString(valid) || !builder.IsConfigName(valid) {
			t.Errorf("scenario name %q is refused", valid)
		}
	}

	for _, invalid := range []string{"", "two words", "Scenario/ntp", "ntp\n"} {
		if matcher.MatchString(invalid) || builder.IsConfigName(invalid) {
			t.Errorf("scenario name %q is accepted", invalid)
		}
	}

	if _, ok := mapAt(t, schema, "$defs")["scenario"]; ok {
		t.Fatal("$defs still defines the scenario reference documents no longer hold")
	}
}

func TestSchemaBundlesPhenixV1(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	components := []string{
		"Scenario", "Experiment", "Topology", "minimega_node", "external_node",
		"iface", "iface_address", "iface_rulesets", "static_iface", "dhcp_iface",
		"serial_iface", "Image", "Role", "User",
	}

	for _, name := range components {
		if _, ok := defs[builder.PhenixDefPrefix+name]; !ok {
			t.Fatalf("$defs is missing %s%s", builder.PhenixDefPrefix, name)
		}
	}

	// A document holds no scenario content, which the v2 schemas were
	// bundled for.
	for name := range defs {
		if strings.HasPrefix(name, "phenix.v2.") {
			t.Fatalf("$defs bundles %s, which no part of a document refers to", name)
		}
	}

	// Device specs stay on the v1 node schemas.
	device := mapAt(t, defs, "device")
	spec := mapAt(t, mapAt(t, device, "properties"), "spec")

	variants, ok := spec["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("device spec is not a node variant union: %v", spec)
	}

	for _, entry := range variants {
		variant, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		if reference, _ := variant["$ref"].(string); !strings.Contains(reference, builder.PhenixDefPrefix) {
			t.Fatalf("device spec variant %q is not a v1 node schema", reference)
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
					t.Fatalf("definition %q is not namespaced", name)
				}

				for _, reference := range collectRefs(def) {
					references++

					want := "#/$defs/" + bundle.prefix
					if !strings.HasPrefix(reference, want) {
						t.Fatalf("definition %q holds reference %q, want prefix %q", name, reference, want)
					}

					if _, ok := defs[strings.TrimPrefix(reference, "#/$defs/")]; !ok {
						t.Fatalf("reference %q does not resolve inside the bundle", reference)
					}
				}
			}

			// static_iface, for one, refers to iface through allOf.
			if references == 0 {
				t.Fatal("the bundle holds no references")
			}
		})
	}
}
