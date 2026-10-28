package version

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

func TestEmbeddedOpenAPISchemas(t *testing.T) {
	t.Parallel()

	// Each config kind at its stored version, and Workflow at v0.
	versions := map[string]string{"Workflow": "v0"}
	for _, kind := range configKinds {
		versions[kind] = StoredVersion[kind]
	}

	for kind, ver := range versions {
		t.Run(ver+" "+kind, func(t *testing.T) {
			t.Parallel()

			schema, err := GetVersionedSchemaForKind(kind, ver)
			if err != nil {
				t.Fatalf("get schema: %v", err)
			}

			if schema["type"] != "object" {
				t.Fatalf("schema type = %v, want object", schema["type"])
			}

			validator, err := GetVersionedValidatorForKind(kind, ver)
			if err != nil {
				t.Fatalf("get validator: %v", err)
			}

			if validator == nil {
				t.Fatal("validator is nil")
			}
		})
	}
}

// configKinds are the config kinds, as the Config schema in package types
// lists them.
var configKinds = []string{"Topology", "Scenario", "Experiment", "Image", "User", "Role"} //nolint:gochecknoglobals // test table

// TestSchemaFilePerVersion proves each schema file holds exactly the config
// kinds stored at its version, plus Workflow in v0, so a config validates
// against the file of its own apiVersion and the UI scaffolds from that same
// file. Lowercase components are sub-schemas, not kinds.
func TestSchemaFilePerVersion(t *testing.T) {
	t.Parallel()

	want := map[string][]string{"v0": {"Workflow"}}
	for _, kind := range configKinds {
		ver, ok := StoredVersion[kind]
		if !ok {
			t.Fatalf("StoredVersion has no %s", kind)
		}

		want[ver] = append(want[ver], kind)
	}

	entries, err := openAPISchemas.ReadDir("schemas")
	if err != nil {
		t.Fatalf("reading schemas: %v", err)
	}

	got := make(map[string][]string, len(entries))

	for _, entry := range entries {
		ver := strings.TrimSuffix(entry.Name(), ".yaml")

		for name := range schemaComponents(t, ver) {
			if unicode.IsUpper(rune(name[0])) {
				got[ver] = append(got[ver], name)
			}
		}
	}

	if len(got) != len(want) {
		t.Errorf("schema files hold kinds for %d versions, want %d: %v", len(got), len(want), got)
	}

	for ver, kinds := range want {
		slices.Sort(kinds)
		slices.Sort(got[ver])

		if !slices.Equal(got[ver], kinds) {
			t.Errorf("%s.yaml kinds = %v, want %v", ver, got[ver], kinds)
		}
	}
}

// TestExperimentScenarioMatchesScenario proves the scenario an Experiment
// embeds, which v1.yaml describes as experiment_scenario, validates exactly
// like a Scenario config, which v2.yaml describes.
func TestExperimentScenarioMatchesScenario(t *testing.T) {
	t.Parallel()

	embedded := schemaComponents(t, "v1")["experiment_scenario"]
	if embedded == nil {
		t.Fatal("v1.yaml has no experiment_scenario component")
	}

	if scenario := schemaComponents(t, "v2")["Scenario"]; !reflect.DeepEqual(embedded, scenario) {
		t.Errorf("v1.yaml experiment_scenario differs from v2.yaml Scenario:\n got %v\nwant %v", embedded, scenario)
	}

	experiment, err := GetVersionedValidatorForKind("Experiment", "v1")
	if err != nil {
		t.Fatalf("GetVersionedValidatorForKind(Experiment, v1) error = %v", err)
	}

	if ref := experiment.Properties["scenario"].Ref; ref != "#/components/schemas/experiment_scenario" {
		t.Errorf("Experiment scenario $ref = %q, want experiment_scenario", ref)
	}
}

// schemaComponents parses the component schemas of the schema file for ver.
func schemaComponents(t *testing.T, ver string) map[string]any {
	t.Helper()

	text, err := ReadSchemaFile(ver)
	if err != nil {
		t.Fatalf("ReadSchemaFile(%s) error = %v", ver, err)
	}

	var api struct {
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}

	if err := yaml.Unmarshal(text, &api); err != nil {
		t.Fatalf("parsing %s.yaml: %v", ver, err)
	}

	return api.Components.Schemas
}

func TestConfigSchemasDocumented(t *testing.T) {
	t.Parallel()

	for _, schemaVersion := range []string{"v0", "v1", "v2"} {
		t.Run(schemaVersion, func(t *testing.T) {
			t.Parallel()

			data, err := ReadSchemaFile(schemaVersion)
			if err != nil {
				t.Fatalf("ReadSchemaFile(%s) error = %v", schemaVersion, err)
			}

			doc, err := openapi3.NewLoader().LoadFromData(data)
			if err != nil {
				t.Fatalf("LoadFromData(%s schema) error = %v", schemaVersion, err)
			}

			if err := doc.Validate(t.Context()); err != nil {
				t.Fatalf("Validate(%s schema) error = %v", schemaVersion, err)
			}

			for name, ref := range doc.Components.Schemas {
				if ref == nil || ref.Value == nil {
					t.Errorf("%s schema %s has no schema", schemaVersion, name)

					continue
				}

				for _, problem := range undocumentedSchema(name, ref.Value) {
					t.Errorf("%s schema %s", schemaVersion, problem)
				}
			}
		})
	}
}

// undocumentedSchema describes what schema, at path, and the properties under
// it lack of a title, a description, or for a scalar an example, default or enum.
func undocumentedSchema(path string, schema *openapi3.Schema) []string {
	var problems []string

	if schema.Title == "" {
		problems = append(problems, path+" has no title")
	}

	if schema.Description == "" {
		problems = append(problems, path+" has no description")
	}

	return append(problems, undocumentedSchemaProperties(path, schema)...)
}

// undocumentedSchemaProperties is undocumentedSchema for the properties, items
// and branches under schema.
func undocumentedSchemaProperties(path string, schema *openapi3.Schema) []string {
	var problems []string

	for name, ref := range schema.Properties {
		propertyPath := path + "." + name

		if ref == nil || ref.Value == nil {
			problems = append(problems, propertyPath+" has no schema")

			continue
		}

		property := ref.Value

		if property.Title == "" {
			problems = append(problems, propertyPath+" has no title")
		}

		if property.Description == "" {
			problems = append(problems, propertyPath+" has no description")
		}

		if property.Type == openapi3.TypeString ||
			property.Type == openapi3.TypeInteger ||
			property.Type == openapi3.TypeNumber ||
			property.Type == openapi3.TypeBoolean {
			if property.Example == nil && property.Default == nil && len(property.Enum) == 0 {
				problems = append(problems, propertyPath+" has no example, default, or enum")
			}
		}

		problems = append(problems, undocumentedSchemaProperties(propertyPath, property)...)
	}

	if schema.Items != nil && schema.Items.Value != nil {
		problems = append(problems, undocumentedSchemaProperties(path+"[]", schema.Items.Value)...)
	}

	for _, branches := range [][]*openapi3.SchemaRef{schema.AllOf, schema.AnyOf, schema.OneOf} {
		for index, ref := range branches {
			if ref != nil && ref.Value != nil {
				problems = append(problems, undocumentedSchemaProperties(path+"["+strconv.Itoa(index)+"]", ref.Value)...)
			}
		}
	}

	return problems
}

func TestGetVersionedSchemaForKindRejectsEmptyKind(t *testing.T) {
	t.Parallel()

	_, err := GetVersionedSchemaForKind("", "v1")
	if !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("error = %v, want ErrInvalidKind", err)
	}
}

func TestReadSchemaFileRejectsUnknownVersion(t *testing.T) {
	t.Parallel()

	if _, err := ReadSchemaFile("unknown"); err == nil {
		t.Fatal("expected an error for an unknown schema version")
	}
}

// TestWorkflowIsSchemaOnly proves the v0 Workflow schema is a validation
// component only: it is not a stored or decodable config kind, and it is not
// part of the v1 or v2 schemas that config validation and the UI use.
func TestWorkflowIsSchemaOnly(t *testing.T) {
	t.Parallel()

	for kind := range StoredVersion {
		if strings.EqualFold(kind, "Workflow") {
			t.Errorf("StoredVersion registers %q", kind)
		}
	}

	if _, err := GetStoredSpecForKind("Workflow"); err == nil {
		t.Error("GetStoredSpecForKind(Workflow) returned no error")
	}

	if _, err := GetVersionedSpecForKind("Workflow", "v0"); err == nil {
		t.Error("GetVersionedSpecForKind(Workflow, v0) returned no error")
	}

	for _, ver := range []string{"v1", "v2"} {
		if _, err := GetVersionedValidatorForKind("Workflow", ver); !errors.Is(err, ErrInvalidKind) {
			t.Errorf("GetVersionedValidatorForKind(Workflow, %s) error = %v, want ErrInvalidKind", ver, err)
		}
	}

	// GET /api/v1/schemas/workflow/v0 capitalizes the kind, so it serves the schema.
	if _, err := GetVersionedSchemaForKind("workflow", "v0"); err != nil {
		t.Errorf("GetVersionedSchemaForKind(workflow, v0) error = %v", err)
	}
}

// TestWorkflowSchemaKeywords proves kin-openapi loads the Workflow component's
// keywords with the meaning workflow validation relies on. Loading the v0 file
// runs T.Validate, which rejects an unknown keyword as an extra sibling field
// and compiles each pattern with Go's regexp package. This test also pins that
// each known keyword sits where it takes effect.
func TestWorkflowSchemaKeywords(t *testing.T) {
	t.Parallel()

	root, err := GetVersionedValidatorForKind("Workflow", "v0")
	if err != nil {
		t.Fatalf("GetVersionedValidatorForKind(Workflow, v0) error = %v", err)
	}

	prop := func(parent *openapi3.Schema, path, name string) *openapi3.Schema {
		t.Helper()

		ref := parent.Properties[name]
		if ref == nil || ref.Value == nil {
			t.Fatalf("%s has no property %q", path, name)
		}

		return ref.Value
	}

	spec := prop(root, "Workflow", "spec")
	auto := prop(spec, "spec", "auto")
	vlanRange := prop(spec, "spec", "vlanRange")

	if want := []string{"apiVersion", "kind", "spec"}; !slices.Equal(root.Required, want) {
		t.Errorf("Workflow required = %v, want %v", root.Required, want)
	}

	closed := map[string]*openapi3.Schema{"Workflow": root, "spec": spec, "spec.auto": auto, "spec.vlanRange": vlanRange}
	for path, schema := range closed {
		if has := schema.AdditionalProperties.Has; has == nil || *has || schema.AdditionalProperties.Schema != nil {
			t.Errorf("%s: additionalProperties is not false", path)
		}
	}

	for name, want := range map[string]bool{"apiVersion": false, "kind": false, "metadata": true, "spec": false} {
		if got := prop(root, "Workflow", name).Nullable; got != want {
			t.Errorf("Workflow.%s nullable = %t, want %t", name, got, want)
		}
	}

	for path, parent := range map[string]*openapi3.Schema{"spec": spec, "spec.auto": auto, "spec.vlanRange": vlanRange} {
		for name := range parent.Properties {
			if !prop(parent, path, name).Nullable {
				t.Errorf("%s.%s is not nullable", path, name)
			}
		}
	}

	for name, want := range map[string]string{"vlans": openapi3.TypeInteger, "schedules": openapi3.TypeString} {
		values := prop(spec, "spec", name).AdditionalProperties.Schema
		if values == nil || values.Value == nil || values.Value.Type != want {
			t.Errorf("spec.%s: the additionalProperties type is not %s", name, want)
		}
	}

	// The limit itself is checked against experiment.MaxBridgeNameLength by
	// the api/workflow schema tests; this package cannot import it.
	if bridge := prop(spec, "spec", "defaultBridge"); bridge.MaxLength == nil {
		t.Error("spec.defaultBridge has no maxLength")
	}

	deployMode := prop(spec, "spec", "deployMode")
	if want := "^(?i:all|no-headnode|only-headnode)?$"; deployMode.Pattern != want {
		t.Errorf("spec.deployMode pattern = %q, want %q", deployMode.Pattern, want)
	}

	// In Go's regexp syntax, (?i:...) is a case-insensitive group.
	if err := deployMode.VisitJSON("No-Headnode"); err != nil {
		t.Errorf("spec.deployMode rejects No-Headnode: %v", err)
	}

	var schemaErr *openapi3.SchemaError
	if err := deployMode.VisitJSON("none"); !errors.As(err, &schemaErr) || schemaErr.SchemaField != "pattern" {
		t.Errorf("spec.deployMode accepts none, or rejects it for another reason: %v", err)
	}
}

// TestEmbeddedOpenAPISchemasHaveNoEmptyKeywords: an empty YAML key such as
// `pattern:` decodes as null, which makes the schemas GET /api/v1/schemas serves invalid JSON Schema.
func TestEmbeddedOpenAPISchemasHaveNoEmptyKeywords(t *testing.T) {
	t.Parallel()

	for _, ver := range []string{"v0", "v1", "v2"} {
		t.Run(ver, func(t *testing.T) {
			t.Parallel()

			for _, path := range emptyKeywords(schemaComponents(t, ver), "components.schemas") {
				t.Errorf("%s.yaml has an empty keyword at %s", ver, path)
			}
		})
	}
}

// emptyKeywords lists the paths of the null values in a schema tree, leaving
// out example and default values, which are data rather than schema.
func emptyKeywords(node any, path string) []string {
	var found []string

	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			switch {
			case key == "example" || key == "default":
			case value == nil:
				found = append(found, path+"."+key)
			default:
				found = append(found, emptyKeywords(value, path+"."+key)...)
			}
		}
	case []any:
		for i, value := range typed {
			if value == nil {
				found = append(found, fmt.Sprintf("%s[%d]", path, i))
			} else {
				found = append(found, emptyKeywords(value, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}

	return found
}
