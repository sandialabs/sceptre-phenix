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

	data, err := os.ReadFile("../web/public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("read OpenAPI schema: %v", err)
	}

	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatalf("load OpenAPI schema: %v", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI schema: %v", err)
	}

	hostRef := doc.Components.Schemas["Host"]
	if hostRef == nil || hostRef.Value == nil {
		t.Fatal("OpenAPI schema Host is missing")
	}

	hostSchema := hostRef.Value
	assertJSONFieldsInSchema(t, "Host", hostSchema, reflect.TypeFor[mm.Host]())
	if diskUsage := hostSchema.Properties["diskusage"]; diskUsage != nil && diskUsage.Value != nil {
		assertJSONFieldsInSchema(t, "Host.diskusage", diskUsage.Value, reflect.TypeFor[mm.DiskUsage]())
	} else {
		t.Fatal("OpenAPI schema Host.diskusage is missing")
	}

	if load := hostSchema.Properties["load"]; load == nil || load.Value == nil || load.Value.Type != openapi3.TypeArray {
		t.Error("Host.load is not an array")
	}

	experimentRef := doc.Components.Schemas["Experiment"]
	if experimentRef == nil || experimentRef.Value == nil {
		t.Fatal("OpenAPI schema Experiment is missing")
	}

	experiment := experimentRef.Value
	if vms := experiment.Properties["vms"]; vms == nil || vms.Value == nil || vms.Value.Type != openapi3.TypeArray {
		t.Error("Experiment.vms is not an array")
	}

	for name, ref := range doc.Components.Schemas {
		if ref == nil || ref.Value == nil {
			t.Errorf("%s has no schema", name)

			continue
		}

		if ref.Value.Title == "" {
			t.Errorf("%s has no title", name)
		}

		if ref.Value.Description == "" {
			t.Errorf("%s has no description", name)
		}
	}

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse OpenAPI schema: %v", err)
	}

	assertOpenAPIPropertiesDocumented(t, "#", raw)
}

// The config schema, embedded from schemas/config.yaml, is valid OpenAPI
// whose properties are documented as openapi.yml's are.
func TestConfigSchemaDocumented(t *testing.T) {
	t.Parallel()

	doc, err := openapi3.NewLoader().LoadFromData(OpenAPI)
	if err != nil {
		t.Fatalf("load config schema: %v", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate config schema: %v", err)
	}

	var raw any
	if err := yaml.Unmarshal(OpenAPI, &raw); err != nil {
		t.Fatalf("parse config schema: %v", err)
	}

	assertOpenAPIPropertiesDocumented(t, "#", raw)
}

func assertJSONFieldsInSchema(t *testing.T, name string, schema *openapi3.Schema, model reflect.Type) {
	t.Helper()

	if schema == nil {
		t.Fatalf("OpenAPI schema %s is missing", name)
	}

	for index := range model.NumField() {
		field := model.Field(index)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			jsonName = field.Name
		}

		if _, ok := schema.Properties[jsonName]; !ok {
			t.Errorf("OpenAPI schema %s is missing JSON field %q", name, jsonName)
		}
	}
}

func assertOpenAPIPropertiesDocumented(t *testing.T, path string, value any) {
	t.Helper()

	switch node := value.(type) {
	case map[string]any:
		if properties, ok := node["properties"].(map[string]any); ok {
			for name, propertyValue := range properties {
				propertyPath := path + "." + name
				property, ok := propertyValue.(map[string]any)
				if !ok {
					t.Errorf("%s is not a schema object", propertyPath)

					continue
				}

				if title, ok := property["title"].(string); !ok || title == "" {
					t.Errorf("%s has no title", propertyPath)
				}

				if description, ok := property["description"].(string); !ok || description == "" {
					t.Errorf("%s has no description", propertyPath)
				}

				switch property["type"] {
				case "string", "integer", "number", "boolean":
					_, hasExample := property["example"]
					_, hasDefault := property["default"]
					_, hasEnum := property["enum"]
					if !hasExample && !hasDefault && !hasEnum {
						t.Errorf("%s has no example, default, or enum", propertyPath)
					}
				}

				assertOpenAPIPropertiesDocumented(t, propertyPath, property)
			}
		}

		for key, child := range node {
			if key != "properties" {
				assertOpenAPIPropertiesDocumented(t, path+"/"+key, child)
			}
		}
	case []any:
		for index, item := range node {
			assertOpenAPIPropertiesDocumented(t, path+"/"+strconv.Itoa(index), item)
		}
	}
}
