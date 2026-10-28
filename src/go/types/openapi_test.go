package types

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"phenix/util/mm"
)

func TestOpenAPISchemasValid(t *testing.T) {
	t.Parallel()

	const path = "../web/public/docs/openapi.yml"

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}

	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatalf("LoadFromData(openapi.yml) error = %v", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("Validate(openapi.yml) error = %v", err)
	}

	hostRef := doc.Components.Schemas["Host"]
	if hostRef == nil || hostRef.Value == nil {
		t.Fatal("openapi.yml has no Host schema")
	}

	hostSchema := hostRef.Value
	if missing := missingJSONFields(hostSchema, reflect.TypeFor[mm.Host]()); len(missing) != 0 {
		t.Errorf("openapi.yml Host lacks the mm.Host JSON fields %q", missing)
	}

	if diskUsage := hostSchema.Properties["diskusage"]; diskUsage != nil && diskUsage.Value != nil {
		if missing := missingJSONFields(diskUsage.Value, reflect.TypeFor[mm.DiskUsage]()); len(missing) != 0 {
			t.Errorf("openapi.yml Host.diskusage lacks the mm.DiskUsage JSON fields %q", missing)
		}
	} else {
		t.Error("openapi.yml has no Host.diskusage schema")
	}

	if load := hostSchema.Properties["load"]; load == nil || load.Value == nil || load.Value.Type != openapi3.TypeArray {
		t.Error("openapi.yml Host.load is not an array")
	}

	experimentRef := doc.Components.Schemas["Experiment"]
	if experimentRef == nil || experimentRef.Value == nil {
		t.Fatal("openapi.yml has no Experiment schema")
	}

	experiment := experimentRef.Value
	if vms := experiment.Properties["vms"]; vms == nil || vms.Value == nil || vms.Value.Type != openapi3.TypeArray {
		t.Error("openapi.yml Experiment.vms is not an array")
	}

	for name, ref := range doc.Components.Schemas {
		if ref == nil || ref.Value == nil {
			t.Errorf("openapi.yml %s has no schema", name)

			continue
		}

		if ref.Value.Title == "" {
			t.Errorf("openapi.yml %s has no title", name)
		}

		if ref.Value.Description == "" {
			t.Errorf("openapi.yml %s has no description", name)
		}
	}

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("yaml.Unmarshal(openapi.yml) error = %v", err)
	}

	for _, problem := range undocumentedProperties("#", raw) {
		t.Errorf("openapi.yml %s", problem)
	}
}

// The config schema, embedded from schemas/config.yaml, is valid OpenAPI
// whose properties are documented as openapi.yml's are.
func TestConfigSchemaDocumented(t *testing.T) {
	t.Parallel()

	doc, err := openapi3.NewLoader().LoadFromData(OpenAPI)
	if err != nil {
		t.Fatalf("LoadFromData(config.yaml) error = %v", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("Validate(config.yaml) error = %v", err)
	}

	var raw any
	if err := yaml.Unmarshal(OpenAPI, &raw); err != nil {
		t.Fatalf("yaml.Unmarshal(config.yaml) error = %v", err)
	}

	for _, problem := range undocumentedProperties("#", raw) {
		t.Errorf("config.yaml %s", problem)
	}
}

// missingJSONFields returns the JSON name of each field of model that schema
// has no property for.
func missingJSONFields(schema *openapi3.Schema, model reflect.Type) []string {
	var missing []string

	for index := range model.NumField() {
		field := model.Field(index)

		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			jsonName = field.Name
		}

		if _, ok := schema.Properties[jsonName]; !ok {
			missing = append(missing, jsonName)
		}
	}

	return missing
}

// undocumentedProperties describes each property under the decoded YAML value at path
// that lacks a title, a description, or for a scalar an example, default or enum.
func undocumentedProperties(path string, value any) []string {
	var problems []string

	switch node := value.(type) {
	case map[string]any:
		if properties, ok := node["properties"].(map[string]any); ok {
			for name, propertyValue := range properties {
				propertyPath := path + "." + name

				property, ok := propertyValue.(map[string]any)
				if !ok {
					problems = append(problems, propertyPath+" is not a schema object")

					continue
				}

				if title, ok := property["title"].(string); !ok || title == "" {
					problems = append(problems, propertyPath+" has no title")
				}

				if description, ok := property["description"].(string); !ok || description == "" {
					problems = append(problems, propertyPath+" has no description")
				}

				switch property["type"] {
				case "string", "integer", "number", "boolean":
					_, hasExample := property["example"]
					_, hasDefault := property["default"]
					_, hasEnum := property["enum"]
					if !hasExample && !hasDefault && !hasEnum {
						problems = append(problems, propertyPath+" has no example, default, or enum")
					}
				}

				problems = append(problems, undocumentedProperties(propertyPath, property)...)
			}
		}

		for key, child := range node {
			if key != "properties" {
				problems = append(problems, undocumentedProperties(path+"/"+key, child)...)
			}
		}
	case []any:
		for index, item := range node {
			problems = append(problems, undocumentedProperties(path+"/"+strconv.Itoa(index), item)...)
		}
	}

	return problems
}
