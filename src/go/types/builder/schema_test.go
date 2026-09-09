package builder_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"phenix/types/builder"
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
		"$schema", "revision", "id", "nodes", "networks", "edges", "viewport", "grid",
	} {
		if !containsAny(schema["required"], required) {
			t.Fatalf("required does not include %q: %v", required, schema["required"])
		}
	}
}

func TestSchemaDefinesBuilderStructures(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	for _, name := range []string{
		"identifier", "iconKey", "position", "size", "viewport", "grid",
		"interfaceHandle", "device", "switch", "note", "group", "node",
		"network", "edge", "scenario", "source",
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
	} {
		if _, ok := edgeProps[name]; !ok {
			t.Fatalf("edge schema has no %q property", name)
		}
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

func TestSchemaBundlesPhenixDefinitions(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	for _, name := range []string{
		"minimega_node", "external_node", "Topology", "Scenario", "iface",
		"iface_address", "iface_rulesets", "static_iface", "dhcp_iface", "serial_iface",
	} {
		if _, ok := defs[builder.PhenixDefPrefix+name]; !ok {
			t.Fatalf("$defs is missing %s%s", builder.PhenixDefPrefix, name)
		}
	}

	device := mapAt(t, defs, "device")
	spec := mapAt(t, mapAt(t, device, "properties"), "spec")

	variants, ok := spec["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("device spec is not a node variant union: %v", spec)
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

func TestSchemaReturnsIndependentCopies(t *testing.T) {
	first := mustSchema(t)
	first["$id"] = "mutated"

	if got := mustSchema(t)["$id"]; got != builder.SchemaURI {
		t.Fatalf("Schema returned shared state: $id = %v", got)
	}
}

func TestBundleOpenAPIDefsRewritesReferences(t *testing.T) {
	defs, err := builder.PhenixDefs()
	if err != nil {
		t.Fatalf("bundling phenix defs: %v", err)
	}

	iface := mapAt(t, defs, builder.PhenixDefPrefix+"static_iface")

	variants, ok := iface["allOf"].([]any)
	if !ok || len(variants) == 0 {
		t.Fatalf("static_iface has no allOf: %v", iface)
	}

	for _, entry := range variants {
		variant, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		ref, ok := variant["$ref"].(string)
		if !ok {
			continue
		}

		if !strings.HasPrefix(ref, "#/$defs/"+builder.PhenixDefPrefix) {
			t.Fatalf("reference %q was not rewritten", ref)
		}
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

func hasSchemaType(value any, want string) bool {
	if value == want {
		return true
	}

	return containsAny(value, want)
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

func TestSchemaScenarioRequiresAPIVersionAndDigest(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")
	scenario := mapAt(t, defs, "scenario")

	for _, required := range []string{"kind", "apiVersion", "digest"} {
		if !containsAny(scenario["required"], required) {
			t.Fatalf("scenario schema does not require %q: %v", required, scenario["required"])
		}
	}

	branches, ok := scenario["allOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("scenario schema has no per-kind branches: %v", scenario["allOf"])
	}

	want := map[string][]string{
		"stored":   {"name", "apiVersion", "digest"},
		"uploaded": {"content", "apiVersion", "digest"},
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

		for _, required := range want[kind] {
			if !containsAny(then["required"], required) {
				t.Fatalf("%s scenario branch does not require %q: %v", kind, required, then)
			}
		}

		if kind == "uploaded" && containsAny(then["required"], "name") {
			t.Fatal("uploaded scenario branch must keep name optional")
		}

		seen[kind] = true
	}

	for kind := range want {
		if !seen[kind] {
			t.Fatalf("scenario schema does not discriminate kind %q", kind)
		}
	}
}

func TestSchemaBundlesBothPhenixVersions(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")

	components := []string{
		"Scenario", "Experiment", "Topology", "minimega_node", "external_node",
		"iface", "iface_address", "iface_rulesets", "static_iface", "dhcp_iface",
		"serial_iface", "Image", "Role", "User",
	}

	for _, prefix := range []string{builder.PhenixDefPrefix, builder.PhenixV2DefPrefix} {
		for _, name := range components {
			if _, ok := defs[prefix+name]; !ok {
				t.Fatalf("$defs is missing %s%s", prefix, name)
			}
		}
	}

	if builder.PhenixDefPrefix == builder.PhenixV2DefPrefix {
		t.Fatal("phenix definition prefixes collide")
	}

	// Device specs stay on the v1 node schemas.
	device := mapAt(t, defs, "device")
	spec := mapAt(t, mapAt(t, device, "properties"), "spec")

	variants, _ := spec["oneOf"].([]any)
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

func TestSchemaScenarioContentUsesCurrentScenarioVersion(t *testing.T) {
	defs := mapAt(t, mustSchema(t), "$defs")
	scenario := mapAt(t, defs, "scenario")
	content := mapAt(t, mapAt(t, scenario, "properties"), "content")

	wantRef := "#/$defs/" + builder.PhenixV2DefPrefix + "Scenario"
	if got := content["$ref"]; got != wantRef {
		t.Fatalf("scenario content $ref = %v, want %q", got, wantRef)
	}

	if !strings.HasSuffix(builder.ScenarioAPIVersion(), "/v2") {
		t.Fatalf("ScenarioAPIVersion() = %q; the bundled scenario schema must follow it",
			builder.ScenarioAPIVersion())
	}

	// The bundled scenario schema must cover complete scenario content, not
	// just the v1 experiment app name list.
	scenarioDef := mapAt(t, defs, builder.PhenixV2DefPrefix+"Scenario")
	apps := mapAt(t, mapAt(t, scenarioDef, "properties"), "apps")

	if !hasSchemaType(apps["type"], "array") {
		t.Fatalf("v2 scenario apps is not an array: %v", apps)
	}

	app := mapAt(t, mapAt(t, apps, "items"), "properties")

	for _, field := range []string{"name", "hosts", "metadata", "assetDir", "disabled"} {
		if _, ok := app[field]; !ok {
			t.Fatalf("v2 scenario app has no %q property: %v", field, app)
		}
	}

	host := mapAt(t, mapAt(t, mapAt(t, app, "hosts"), "items"), "properties")

	for _, field := range []string{"hostname", "metadata"} {
		if _, ok := host[field]; !ok {
			t.Fatalf("v2 scenario app host has no %q property: %v", field, host)
		}
	}

	if settings := mapAt(t, host, "metadata"); settings["additionalProperties"] != true {
		t.Fatalf("v2 scenario host metadata does not accept free-form settings: %v", settings)
	}

	// The v1 scenario schema is retained but must not be what content points at.
	v1Apps := mapAt(t, mapAt(t, mapAt(t, defs, builder.PhenixDefPrefix+"Scenario"), "properties"), "apps")
	if v1Apps["type"] != "object" {
		t.Fatalf("unexpected v1 scenario apps shape: %v", v1Apps)
	}
}

func TestPhenixV2DefsRewritesReferences(t *testing.T) {
	defs, err := builder.PhenixV2Defs()
	if err != nil {
		t.Fatalf("bundling phenix v2 defs: %v", err)
	}

	for name, def := range defs {
		if !strings.HasPrefix(name, builder.PhenixV2DefPrefix) {
			t.Fatalf("definition %q is not namespaced", name)
		}

		for _, reference := range collectRefs(def) {
			want := "#/$defs/" + builder.PhenixV2DefPrefix
			if !strings.HasPrefix(reference, want) {
				t.Fatalf("definition %q holds reference %q, want prefix %q", name, reference, want)
			}

			if _, ok := defs[strings.TrimPrefix(reference, "#/$defs/")]; !ok {
				t.Fatalf("reference %q does not resolve inside the v2 bundle", reference)
			}
		}
	}
}
