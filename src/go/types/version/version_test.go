package version

import (
	"errors"
	"testing"
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
