package types

import (
	"context"
	"strconv"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// The config schema, embedded from schemas/config.yaml, is valid OpenAPI. Each property has a
// title, a description and, for a scalar, an example, a default or an enum.
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
