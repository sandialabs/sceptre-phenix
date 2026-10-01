package version

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestEmbeddedOpenAPISchemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		kind    string
	}{
		{name: "v0 topology", version: "v0", kind: "Topology"},
		{name: "v0 workflow", version: "v0", kind: "Workflow"},
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

// TestWorkflowIsSchemaOnly proves the v0 Workflow schema is a validation
// component only: it is not a stored or decodable config kind, and it is not
// part of the latest schemas that config validation and the UI use.
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

	if _, err := GetVersionedValidatorForKind("Workflow", LATEST_VERSION); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("GetVersionedValidatorForKind(Workflow, %s) error = %v, want ErrInvalidKind", LATEST_VERSION, err)
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

// TestWorkflowSchemaDocumented proves the Workflow schema and every property
// under it carry a title and a description, which the API docs and the schema
// served at GET /api/v1/schemas/workflow/v0 show. The count pins that the walk
// reaches the nested auto and vlanRange properties.
func TestWorkflowSchemaDocumented(t *testing.T) {
	t.Parallel()

	root, err := GetVersionedValidatorForKind("Workflow", "v0")
	if err != nil {
		t.Fatalf("GetVersionedValidatorForKind(Workflow, v0) error = %v", err)
	}

	var (
		visited int
		walk    func(path string, schema *openapi3.Schema)
	)

	walk = func(path string, schema *openapi3.Schema) {
		visited++

		if schema.Title == "" {
			t.Errorf("%s has no title", path)
		}

		if schema.Description == "" {
			t.Errorf("%s has no description", path)
		}

		for name, ref := range schema.Properties {
			if ref == nil || ref.Value == nil {
				t.Errorf("%s.%s has no schema", path, name)

				continue
			}

			walk(path+"."+name, ref.Value)
		}
	}

	walk("Workflow", root)

	// The schema itself, its 4 top-level properties, the 9 spec properties,
	// the 3 auto properties and the 2 vlanRange properties.
	if want := 1 + 4 + 9 + 3 + 2; visited != want {
		t.Errorf("walked %d schemas, want %d", visited, want)
	}
}
