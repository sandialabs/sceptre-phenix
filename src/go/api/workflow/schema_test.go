package workflow_test

import (
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"phenix/api/experiment"
	"phenix/api/workflow"
	"phenix/types"
	"phenix/types/version"
	"phenix/util/common"
)

// workflowDocHeader is the envelope of a valid workflow config.
const workflowDocHeader = "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata: {}\n"

// rejectedPrefix starts every schema error that ValidateDocument returns.
const rejectedPrefix = "validating workflow config: config validation failed: "

// The schema keywords a rejection is attributed to, as [openapi3.SchemaError]
// names them in SchemaField.
const (
	fieldType       = "type"
	fieldRequired   = "required"
	fieldProperties = "properties"
	fieldEnum       = "enum"
	fieldNullable   = "nullable"
	fieldMaxLength  = "maxLength"
	fieldPattern    = "pattern"
)

// rejectCase is an invalid workflow config and the schema error it must
// produce: the JSON pointer of the value at fault, the schema keyword it
// breaks and, when set, a fragment of the reason such as the key at fault.
type rejectCase struct {
	name    string
	src     string
	pointer []string
	field   string
	reason  string
}

// parseDoc parses a YAML workflow config into the document ValidateDocument
// takes, the way the apply endpoint does. An empty source leaves it nil.
func parseDoc(t *testing.T, src string) map[string]any {
	t.Helper()

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parsing workflow config: %v\n%s", err, src)
	}

	return doc
}

// docWithSpec returns a workflow config whose spec is body. The first line of
// body sits at spec level; nested lines carry their own indentation.
func docWithSpec(body string) string {
	return workflowDocHeader + "spec:\n  " + body + "\n"
}

// schemaError returns the schema error in err, which must wrap
// [types.ErrValidationFailed].
func schemaError(t *testing.T, err error) *openapi3.SchemaError {
	t.Helper()

	if !errors.Is(err, types.ErrValidationFailed) {
		t.Fatalf("ValidateDocument() error = %v, want ErrValidationFailed", err)
	}

	var schemaErr *openapi3.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("ValidateDocument() error %v carries no *openapi3.SchemaError", err)
	}

	return schemaErr
}

// assertRejected checks that err rejects a document as tc describes.
func assertRejected(t *testing.T, err error, tc rejectCase) {
	t.Helper()

	schemaErr := schemaError(t, err)

	if !strings.HasPrefix(err.Error(), rejectedPrefix) {
		t.Errorf("ValidateDocument() error = %q, want the prefix %q", err, rejectedPrefix)
	}

	if got := schemaErr.JSONPointer(); !slices.Equal(got, tc.pointer) {
		t.Errorf("JSONPointer() = %q, want %q", got, tc.pointer)
	}

	if schemaErr.SchemaField != tc.field {
		t.Errorf("SchemaField = %q, want %q", schemaErr.SchemaField, tc.field)
	}

	if !strings.Contains(schemaErr.Reason, tc.reason) {
		t.Errorf("Reason = %q, want it to contain %q", schemaErr.Reason, tc.reason)
	}
}

// checkRejected validates each case and checks its schema error.
func checkRejected(t *testing.T, cases []rejectCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRejected(t, workflow.ValidateDocument(parseDoc(t, tc.src)), tc)
		})
	}
}

// schemaProperties returns the properties of the object schema that props
// holds under name.
func schemaProperties(t *testing.T, props map[string]any, name string) map[string]any {
	t.Helper()

	prop, _ := props[name].(map[string]any)

	out, ok := prop["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema property %q has no properties", name)
	}

	return out
}

// TestValidateDocumentAccepts covers valid configs, whose spec must also
// decode, because apply runs Decode after the schema check.
func TestValidateDocumentAccepts(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "envelope and an empty spec",
			src:  "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nspec: {}\n",
		},
		{
			name: "empty metadata and spec",
			src:  workflowDocHeader + "spec: {}\n",
		},
		{
			name: "null metadata",
			src:  "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata:\nspec: {}\n",
		},
		{
			name: "metadata takes any key",
			src:  "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata:\n  name: helloworld\n  labels: x\nspec: {}\n",
		},
		{
			name: "every field",
			src: `apiVersion: phenix.sandia.gov/v0
kind: Workflow
metadata:
  name: helloworld
spec:
  auto:
    create: helloworld
    update: true
    restart: false
  topology: helloworld
  scenario: helloworld-apps
  vlans:
    EXP: 101
    MGMT: 200
  schedules:
    host-00: compute1
  deployMode: no-headnode
  useGREMesh: true
  vlanRange:
    min: 100
    max: 200
  defaultBridge: phenix
`,
		},
		{
			name: "every optional field null",
			src: docWithSpec(
				"auto:\n  topology:\n  scenario:\n  vlans:\n  schedules:\n  deployMode:\n  useGREMesh:\n  vlanRange:\n  defaultBridge:",
			),
		},
		{
			name: "null auto and vlanRange members",
			src:  docWithSpec("auto:\n    create:\n    update:\n    restart:\n  vlanRange:\n    min:\n    max:"),
		},
		{
			name: "empty objects",
			src:  docWithSpec("auto: {}\n  vlans: {}\n  schedules: {}\n  vlanRange: {}"),
		},
		{
			name: "vlans and schedules take any key",
			src:  docWithSpec("vlans:\n    Any_alias.1: 5\n  schedules:\n    any-host.example: compute9"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseDoc(t, tc.src)

			if err := workflow.ValidateDocument(doc); err != nil {
				t.Fatalf("ValidateDocument() error = %v", err)
			}

			spec, _ := doc["spec"].(map[string]any)

			if _, err := workflow.Decode(spec); err != nil {
				t.Errorf("Decode() error = %v", err)
			}
		})
	}
}

// TestValidateDocumentJSON covers JSON bodies, whose numbers arrive as float64.
func TestValidateDocumentJSON(t *testing.T) {
	valid := `{"apiVersion":"phenix.sandia.gov/v0","kind":"Workflow","spec":{"vlans":{"EXP":101},"vlanRange":{"min":100}}}`

	var doc map[string]any
	if err := json.Unmarshal([]byte(valid), &doc); err != nil {
		t.Fatalf("parsing workflow config: %v", err)
	}

	if err := workflow.ValidateDocument(doc); err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}

	fractional := `{"apiVersion":"phenix.sandia.gov/v0","kind":"Workflow","spec":{"vlans":{"EXP":101.5}}}`

	if err := json.Unmarshal([]byte(fractional), &doc); err != nil {
		t.Fatalf("parsing workflow config: %v", err)
	}

	assertRejected(t, workflow.ValidateDocument(doc), rejectCase{
		name:    "fractional VLAN ID",
		src:     fractional,
		pointer: []string{"spec", "vlans", "EXP"},
		field:   fieldType,
	})
}

// TestValidateDocumentRejectsEnvelope covers a wrong or missing apiVersion or
// kind, and a metadata or spec that is not a mapping. The document is
// validated as parsed, so a missing key is reported as missing, and an empty
// source is validated as an empty document.
func TestValidateDocumentRejectsEnvelope(t *testing.T) {
	checkRejected(t, []rejectCase{
		{
			name:    "apiVersion v1",
			src:     "apiVersion: phenix.sandia.gov/v1\nkind: Workflow\nspec: {}\n",
			pointer: []string{"apiVersion"},
			field:   fieldEnum,
		},
		{
			name:    "apiVersion without a version",
			src:     "apiVersion: phenix.sandia.gov\nkind: Workflow\nspec: {}\n",
			pointer: []string{"apiVersion"},
			field:   fieldEnum,
		},
		{
			name:    "apiVersion missing",
			src:     "kind: Workflow\nspec: {}\n",
			pointer: []string{"apiVersion"},
			field:   fieldRequired,
		},
		{
			name:    "kind in lowercase",
			src:     "apiVersion: phenix.sandia.gov/v0\nkind: workflow\nspec: {}\n",
			pointer: []string{"kind"},
			field:   fieldEnum,
		},
		{
			name:    "kind of another config",
			src:     "apiVersion: phenix.sandia.gov/v0\nkind: Topology\nspec: {}\n",
			pointer: []string{"kind"},
			field:   fieldEnum,
		},
		{
			name:    "kind missing",
			src:     "apiVersion: phenix.sandia.gov/v0\nspec: {}\n",
			pointer: []string{"kind"},
			field:   fieldRequired,
		},
		{
			name:    "empty document",
			src:     "",
			pointer: []string{"apiVersion"},
			field:   fieldRequired,
		},
		{
			name:    "metadata not a mapping",
			src:     "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata: wf\nspec: {}\n",
			pointer: []string{"metadata"},
			field:   fieldType,
		},
		{
			name:    "spec not a mapping",
			src:     workflowDocHeader + "spec: helloworld\n",
			pointer: []string{"spec"},
			field:   fieldType,
		},
	})
}

// TestValidateDocumentRejectsTopLevelMistakes covers mistakes outside spec.
// The document is validated as parsed, so spec keys that lost their
// indentation, or a misspelled spec, are rejected instead of being dropped and
// applied as the empty spec. An unknown top-level key is reported on the
// document itself, with an empty pointer, and the keys are checked in sorted
// order before the required ones.
func TestValidateDocumentRejectsTopLevelMistakes(t *testing.T) {
	checkRejected(t, []rejectCase{
		{
			name:   "spec keys at the top level",
			src:    workflowDocHeader + "spec:\nauto:\n  update: true\ntopology: helloworld\nvlans:\n  EXP: 101\n",
			field:  fieldProperties,
			reason: `"auto"`,
		},
		{
			name:   "one spec key at the top level",
			src:    workflowDocHeader + "spec:\n  topology: helloworld\nvlans:\n  EXP: 101\n",
			field:  fieldProperties,
			reason: `"vlans"`,
		},
		{
			name:   "spec misspelled",
			src:    workflowDocHeader + "sepc:\n  topology: helloworld\n",
			field:  fieldProperties,
			reason: `"sepc"`,
		},
		{
			name:   "spec capitalized",
			src:    workflowDocHeader + "Spec:\n  topology: helloworld\n",
			field:  fieldProperties,
			reason: `"Spec"`,
		},
		{
			name:    "spec missing",
			src:     workflowDocHeader,
			pointer: []string{"spec"},
			field:   fieldRequired,
		},
		{
			name:    "spec null",
			src:     workflowDocHeader + "spec:\n",
			pointer: []string{"spec"},
			field:   fieldNullable,
		},
	})
}

// TestValidateDocumentTopLevelKeyError pins what the validation-error
// explainer needs to place an unknown top-level key: its pointer is empty,
// like that of an error at the top of a kind's spec, but the failing schema
// is the whole Workflow schema, which declares apiVersion.
func TestValidateDocumentTopLevelKeyError(t *testing.T) {
	err := workflow.ValidateDocument(parseDoc(t, workflowDocHeader+"spec:\nauto:\n  update: true\n"))

	schemaErr := schemaError(t, err)

	if got := schemaErr.JSONPointer(); len(got) != 0 {
		t.Errorf("JSONPointer() = %v, want empty", got)
	}

	if schemaErr.SchemaField != fieldProperties {
		t.Errorf("SchemaField = %q, want %q", schemaErr.SchemaField, fieldProperties)
	}

	if schemaErr.Schema == nil || schemaErr.Schema.Properties["apiVersion"] == nil {
		t.Error("the failing schema does not declare apiVersion, so it is not the whole Workflow schema")
	}
}

// TestValidateDocumentRejectsWrongTypes gives each spec field a value of the
// wrong type.
func TestValidateDocumentRejectsWrongTypes(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		pointer []string
	}{
		{name: "auto", body: "auto: enabled", pointer: []string{"auto"}},
		{name: "auto.create", body: "auto:\n    create: 5", pointer: []string{"auto", "create"}},
		{name: "auto.update", body: "auto:\n    update: \"true\"", pointer: []string{"auto", "update"}},
		{name: "auto.restart", body: "auto:\n    restart: 1", pointer: []string{"auto", "restart"}},
		{name: "topology", body: "topology: [helloworld]", pointer: []string{"topology"}},
		{name: "scenario", body: "scenario: 7", pointer: []string{"scenario"}},
		{name: "vlans", body: "vlans: [EXP]", pointer: []string{"vlans"}},
		{name: "vlans value", body: "vlans:\n    EXP: \"101\"", pointer: []string{"vlans", "EXP"}},
		{name: "fractional vlans value", body: "vlans:\n    EXP: 101.5", pointer: []string{"vlans", "EXP"}},
		{name: "schedules", body: "schedules: compute1", pointer: []string{"schedules"}},
		{name: "schedules value", body: "schedules:\n    host-00: 3", pointer: []string{"schedules", "host-00"}},
		{name: "deployMode", body: "deployMode: 1", pointer: []string{"deployMode"}},
		{name: "useGREMesh", body: "useGREMesh: \"yes\"", pointer: []string{"useGREMesh"}},
		{name: "vlanRange", body: "vlanRange: 100-200", pointer: []string{"vlanRange"}},
		{name: "vlanRange.min", body: "vlanRange:\n    min: low", pointer: []string{"vlanRange", "min"}},
		{name: "vlanRange.max", body: "vlanRange:\n    max: 200.5", pointer: []string{"vlanRange", "max"}},
		{name: "defaultBridge", body: "defaultBridge: 5", pointer: []string{"defaultBridge"}},
	}

	rejects := make([]rejectCase, 0, len(cases))
	for _, tc := range cases {
		rejects = append(rejects, rejectCase{
			name:    tc.name,
			src:     docWithSpec(tc.body),
			pointer: slices.Concat([]string{"spec"}, tc.pointer),
			field:   fieldType,
		})
	}

	checkRejected(t, rejects)
}

// TestValidateDocumentRejectsUnknownKeys puts an unknown key at each level
// that the schema closes. The pointer names the parent object and the reason
// the key.
func TestValidateDocumentRejectsUnknownKeys(t *testing.T) {
	checkRejected(t, []rejectCase{
		{
			name:    "under spec",
			src:     docWithSpec("topologyName: helloworld"),
			pointer: []string{"spec"},
			field:   fieldProperties,
			reason:  `"topologyName"`,
		},
		{
			name:    "spec key in the wrong case",
			src:     docWithSpec("Topology: helloworld"),
			pointer: []string{"spec"},
			field:   fieldProperties,
			reason:  `"Topology"`,
		},
		{
			name:    "under auto",
			src:     docWithSpec("auto:\n    start: true"),
			pointer: []string{"spec", "auto"},
			field:   fieldProperties,
			reason:  `"start"`,
		},
		{
			name:    "under vlanRange",
			src:     docWithSpec("vlanRange:\n    minimum: 100"),
			pointer: []string{"spec", "vlanRange"},
			field:   fieldProperties,
			reason:  `"minimum"`,
		},
	})
}

// TestValidateDocumentDeployMode proves the schema accepts exactly the
// deployMode values that the experiment code accepts, in any case.
func TestValidateDocumentDeployMode(t *testing.T) {
	cases := []struct {
		mode  string
		valid bool
	}{
		{mode: "", valid: true},
		{mode: "all", valid: true},
		{mode: "ALL", valid: true},
		{mode: "No-Headnode", valid: true},
		{mode: "only-headnode", valid: true},
		{mode: "none", valid: false},
		{mode: "headnode", valid: false},
		{mode: "all-headnode", valid: false},
		{mode: " all", valid: false},
		{mode: "all\n", valid: false},
	}

	for _, tc := range cases {
		t.Run(strconv.Quote(tc.mode), func(t *testing.T) {
			// The schema must agree with the parser the experiment code uses.
			if _, err := common.ParseDeployMode(tc.mode); (err == nil) != tc.valid {
				t.Fatalf("ParseDeployMode(%q) error = %v, want valid = %t", tc.mode, err, tc.valid)
			}

			src := docWithSpec("deployMode: " + strconv.Quote(tc.mode))
			err := workflow.ValidateDocument(parseDoc(t, src))

			if tc.valid {
				if err != nil {
					t.Fatalf("ValidateDocument() error = %v, want nil", err)
				}

				return
			}

			assertRejected(t, err, rejectCase{
				name:    tc.mode,
				src:     src,
				pointer: []string{"spec", "deployMode"},
				field:   fieldPattern,
			})
		})
	}
}

// TestValidateDocumentDefaultBridgeLength checks that the schema's bridge name
// limit is the experiment package's.
func TestValidateDocumentDefaultBridgeLength(t *testing.T) {
	longest := strings.Repeat("b", experiment.MaxBridgeNameLength)

	if err := workflow.ValidateDocument(parseDoc(t, docWithSpec("defaultBridge: "+longest))); err != nil {
		t.Fatalf("ValidateDocument(%d character bridge) error = %v", len(longest), err)
	}

	checkRejected(t, []rejectCase{
		{
			name:    "one character too long",
			src:     docWithSpec("defaultBridge: " + longest + "b"),
			pointer: []string{"spec", "defaultBridge"},
			field:   fieldMaxLength,
		},
	})
}

// TestValidateDocumentErrorChain checks what the web error helper relies on:
// one unwrap drops ValidateDocument's own prefix.
func TestValidateDocumentErrorChain(t *testing.T) {
	err := workflow.ValidateDocument(parseDoc(t, docWithSpec("auto:\n    start: true")))

	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatal("unwrapping the ValidateDocument() error returned nil")
	}

	if !errors.Is(cause, types.ErrValidationFailed) || !strings.HasPrefix(cause.Error(), types.ErrValidationFailed.Error()) {
		t.Errorf("unwrapped error = %q, want it to start with %q", cause, types.ErrValidationFailed)
	}
}

// TestSchemaCoversSpecFields proves the Workflow schema and the spec types
// name the same keys. The schema rejects unknown keys, so a spec field with no
// schema property could never be set, and a property with no field would be
// accepted and then ignored.
func TestSchemaCoversSpecFields(t *testing.T) {
	schema, err := version.GetVersionedSchemaForKind("Workflow", "v0")
	if err != nil {
		t.Fatalf("getting the Workflow schema: %v", err)
	}

	root, _ := schema["properties"].(map[string]any)
	spec := schemaProperties(t, root, "spec")

	cases := []struct {
		name  string
		props map[string]any
		typ   reflect.Type
	}{
		{name: "spec", props: spec, typ: reflect.TypeFor[workflow.Spec]()},
		{name: "spec.auto", props: schemaProperties(t, spec, "auto"), typ: reflect.TypeFor[workflow.Auto]()},
		{name: "spec.vlanRange", props: schemaProperties(t, spec, "vlanRange"), typ: reflect.TypeFor[workflow.VLANRange]()},
	}

	for _, tc := range cases {
		tags := make([]string, 0, tc.typ.NumField())
		for i := range tc.typ.NumField() {
			tags = append(tags, tc.typ.Field(i).Tag.Get("mapstructure"))
		}

		slices.Sort(tags)

		if got := slices.Sorted(maps.Keys(tc.props)); !slices.Equal(got, tags) {
			t.Errorf("%s: schema properties %v, mapstructure tags %v", tc.name, got, tags)
		}
	}
}
