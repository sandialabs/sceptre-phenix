package types_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

// schemaFailure is where a schema error points and the keyword it breaks.
type schemaFailure struct {
	Pointer []string
	Field   string
}

// checkSchemaError returns an error unless err is a validation failure whose
// schema error has the given JSON pointer and breaks the given schema keyword.
func checkSchemaError(err error, pointer []string, field string) error {
	if !errors.Is(err, types.ErrValidationFailed) {
		return fmt.Errorf("error = %w, want ErrValidationFailed", err)
	}

	var schemaErr *openapi3.SchemaError
	if !errors.As(err, &schemaErr) {
		return fmt.Errorf("error %w carries no *openapi3.SchemaError", err)
	}

	want := schemaFailure{Pointer: pointer, Field: field}
	got := schemaFailure{Pointer: schemaErr.JSONPointer(), Field: schemaErr.SchemaField}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		return fmt.Errorf("schema error mismatch (-want +got):\n%s", diff)
	}

	return nil
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
			if err := checkSchemaError(types.ValidateSchema("Workflow", "v0", tc.value), tc.wantPointer, tc.wantField); err != nil {
				t.Errorf("ValidateSchema(Workflow, v0, value): %v", err)
			}
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
			name:        "kind not in that version's schemas",
			component:   "Topology",
			ver:         "v2",
			wantPrefix:  "getting validator for config: invalid kind: no schema definition found for version v2 of Topology",
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

	if err := checkSchemaError(types.ValidateConfigSpec(cfg), []string{"apps", "0", "name"}, "required"); err != nil {
		t.Errorf("ValidateConfigSpec(Scenario without an app name): %v", err)
	}
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

	if err := checkSchemaError(types.ValidateConfigSpec(cfg), []string{"kind"}, "enum"); err != nil {
		t.Errorf("ValidateConfigSpec(Workflow config): %v", err)
	}
}

// TestValidateConfigSpecUsesOwnVersion proves a config validates against the
// schema file of its own apiVersion: each kind has a schema at its stored
// version, and a kind at a version whose file does not hold it is a lookup
// failure, not a validation failure.
func TestValidateConfigSpecUsesOwnVersion(t *testing.T) {
	specs := map[string]map[string]any{
		"Topology":   {"nodes": []any{}},
		"Scenario":   {"apps": []any{map[string]any{"name": "app"}}},
		"Experiment": {"topology": map[string]any{"nodes": []any{}}},
		"Image":      {"format": "qcow2", "mirror": "m", "release": "focal", "size": "10G", "variant": "minbase"},
		"User":       {"first_name": "John", "last_name": "Doe", "username": "john"},
		"Role":       {"roleName": "viewer", "policies": []any{}},
	}

	config := func(kind, ver string) store.Config {
		return store.Config{
			Version:  store.APIGroup + "/" + ver,
			Kind:     kind,
			Metadata: store.ConfigMetadata{Name: "own-version"},
			Spec:     specs[kind],
		}
	}

	for kind := range specs {
		t.Run(kind, func(t *testing.T) {
			stored := version.StoredVersion[kind]
			if err := types.ValidateConfigSpec(config(kind, stored)); err != nil {
				t.Errorf("ValidateConfigSpec(%s %s) error = %v", stored, kind, err)
			}

			other := map[string]string{"v1": "v2", "v2": "v1"}[stored]

			err := types.ValidateConfigSpec(config(kind, other))
			if !errors.Is(err, types.ErrValidationFailed) || !errors.Is(err, version.ErrInvalidKind) {
				t.Errorf("ValidateConfigSpec(%s %s) error = %v, want ErrValidationFailed wrapping ErrInvalidKind", other, kind, err)
			}
		})
	}
}
