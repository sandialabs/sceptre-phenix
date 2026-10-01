package types_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"phenix/store"
	"phenix/types"
	"phenix/types/version"
)

// TestValidateSchemaAccepts proves values are converted to JSON types before
// validation, so Go ints, typed maps and tagged structs validate like a
// decoded JSON document. Both values have a spec, which the Workflow schema
// requires.
func TestValidateSchemaAccepts(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{
			name: "map holding Go ints",
			value: map[string]any{
				"apiVersion": "phenix.sandia.gov/v0",
				"kind":       "Workflow",
				"spec": map[string]any{
					"vlans":     map[string]int{"EXP": 101},
					"vlanRange": map[string]any{"min": 100, "max": 200},
				},
			},
		},
		{
			name: "struct with JSON tags",
			value: store.Config{
				Version: "phenix.sandia.gov/v0",
				Kind:    "Workflow",
				Spec:    map[string]any{"topology": "helloworld"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := types.ValidateSchema("Workflow", "v0", tc.value); err != nil {
				t.Fatalf("ValidateSchema() error = %v", err)
			}
		})
	}
}

// assertSchemaError checks that err is a validation failure whose schema error
// has the given JSON pointer and breaks the given schema keyword.
func assertSchemaError(t *testing.T, err error, pointer []string, field string) {
	t.Helper()

	if !errors.Is(err, types.ErrValidationFailed) {
		t.Fatalf("error = %v, want ErrValidationFailed", err)
	}

	var schemaErr *openapi3.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("error %v carries no *openapi3.SchemaError", err)
	}

	if got := schemaErr.JSONPointer(); !slices.Equal(got, pointer) {
		t.Errorf("JSONPointer() = %v, want %v", got, pointer)
	}

	if schemaErr.SchemaField != field {
		t.Errorf("SchemaField = %q, want %q", schemaErr.SchemaField, field)
	}
}

// TestValidateSchemaRejects checks the chain of validation failures, whose
// pointers are relative to the validated value.
func TestValidateSchemaRejects(t *testing.T) {
	cases := []struct {
		name        string
		value       any
		wantPointer []string
		wantField   string
	}{
		{
			name:        "required property missing",
			value:       map[string]any{"apiVersion": "phenix.sandia.gov/v0"},
			wantPointer: []string{"kind"},
			wantField:   "required",
		},
		{
			name: "nested property of the wrong type",
			value: map[string]any{
				"apiVersion": "phenix.sandia.gov/v0",
				"kind":       "Workflow",
				"spec":       map[string]any{"auto": map[string]any{"update": "yes"}},
			},
			wantPointer: []string{"spec", "auto", "update"},
			wantField:   "type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertSchemaError(t, types.ValidateSchema("Workflow", "v0", tc.value), tc.wantPointer, tc.wantField)
		})
	}
}

// TestValidateSchemaLookupErrors proves a missing schema is a lookup failure,
// not a validation failure, so callers can report it as an internal error.
func TestValidateSchemaLookupErrors(t *testing.T) {
	cases := []struct {
		name        string
		component   string
		ver         string
		wantPrefix  string
		invalidKind bool
	}{
		{
			name:        "unknown component",
			component:   "Nope",
			ver:         "v0",
			wantPrefix:  "getting validator for config: invalid kind: no schema definition found for version v0 of Nope",
			invalidKind: true,
		},
		{
			name:        "Workflow is not in the latest schemas",
			component:   "Workflow",
			ver:         version.LATEST_VERSION,
			wantPrefix:  "getting validator for config: invalid kind: no schema definition found for version v2 of Workflow",
			invalidKind: true,
		},
		{
			name:        "unknown version",
			component:   "Workflow",
			ver:         "v9",
			wantPrefix:  "getting validator for config: reading embedded OpenAPI schema schemas/v9.yaml: ",
			invalidKind: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := types.ValidateSchema(tc.component, tc.ver, map[string]any{})
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Fatalf("ValidateSchema() error = %v, want prefix %q", err, tc.wantPrefix)
			}

			if errors.Is(err, types.ErrValidationFailed) {
				t.Errorf("ValidateSchema() error %v wraps ErrValidationFailed", err)
			}

			if got := errors.Is(err, version.ErrInvalidKind); got != tc.invalidKind {
				t.Errorf("error wraps ErrInvalidKind = %t, want %t", got, tc.invalidKind)
			}
		})
	}
}

// TestValidateConfigSpecErrorChain checks a per-kind schema failure, whose
// pointer is relative to the spec, as ValidateConfigSpec reports it through
// ValidateSchema.
func TestValidateConfigSpecErrorChain(t *testing.T) {
	cfg := store.Config{
		Version:  "phenix.sandia.gov/v2",
		Kind:     "Scenario",
		Metadata: store.ConfigMetadata{Name: "apps"},
		Spec:     map[string]any{"apps": []any{map[string]any{}}},
	}

	assertSchemaError(t, types.ValidateConfigSpec(cfg), []string{"apps", "0", "name"}, "required")
}

// TestValidateConfigSpecRejectsWorkflowKind proves a Workflow document cannot
// pass config validation, so phenix config create and POST /configs, which
// validate through config.Create, reject one.
func TestValidateConfigSpecRejectsWorkflowKind(t *testing.T) {
	cfg := store.Config{
		Version:  "phenix.sandia.gov/v0",
		Kind:     "Workflow",
		Metadata: store.ConfigMetadata{Name: "wf"},
		Spec:     map[string]any{"topology": "helloworld"},
	}

	assertSchemaError(t, types.ValidateConfigSpec(cfg), []string{"kind"}, "enum")
}
