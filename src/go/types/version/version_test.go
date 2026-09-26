package version

import (
	"errors"
	"fmt"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEmbeddedOpenAPISchemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		kind    string
	}{
		{name: "v0 topology", version: "v0", kind: "Topology"},
		{name: "v1 experiment", version: "v1", kind: "Experiment"},
		{name: "v2 scenario", version: "v2", kind: "Scenario"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			schema, err := GetVersionedSchemaForKind(test.kind, test.version)
			if err != nil {
				t.Fatalf("get schema: %v", err)
			}

			if schema["type"] != "object" {
				t.Fatalf("schema type = %v, want object", schema["type"])
			}

			validator, err := GetVersionedValidatorForKind(test.kind, test.version)
			if err != nil {
				t.Fatalf("get validator: %v", err)
			}

			if validator == nil {
				t.Fatal("validator is nil")
			}
		})
	}
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

// An empty YAML key such as `pattern:` decodes as null, which no JSON Schema
// keyword accepts: the schemas GET /api/v1/schemas serves from these files
// would be invalid.
func TestEmbeddedOpenAPISchemasHaveNoEmptyKeywords(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"v0", "v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			data, err := ReadSchemaFile(version)
			if err != nil {
				t.Fatalf("read schema: %v", err)
			}

			var api struct {
				Components struct {
					Schemas map[string]any `yaml:"schemas"`
				} `yaml:"components"`
			}

			if err := yaml.Unmarshal(data, &api); err != nil {
				t.Fatalf("parse schema: %v", err)
			}

			for _, path := range emptyKeywords(api.Components.Schemas, "components.schemas") {
				t.Errorf("%s: empty keyword in the %s schema file", path, version)
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
