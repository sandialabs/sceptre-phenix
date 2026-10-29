package soh

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
)

// The API reference documents every JSON field the soh endpoint sends (the
// network, its nodes, each VM's results and each check) with its Go type.
func TestOpenAPIDocumentsEverySoHField(t *testing.T) {
	t.Parallel()

	const path = "../../web/public/docs/openapi.yml"

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}

	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatalf("LoadFromData(openapi.yml) error = %v", err)
	}

	for schema, model := range map[string]reflect.Type{
		"SoHNetwork":   reflect.TypeFor[Network](),
		"SoHNode":      reflect.TypeFor[Node](),
		"SoHHostState": reflect.TypeFor[HostState](),
		"SoHState":     reflect.TypeFor[State](),
	} {
		ref := doc.Components.Schemas[schema]
		if ref == nil || ref.Value == nil {
			t.Errorf("openapi.yml has no %s schema", schema)

			continue
		}

		var (
			missing          []string
			wantRequired     []string
			wantTypes, types = map[string]string{}, map[string]string{}
		)

		for index := range model.NumField() {
			field := model.Field(index)

			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}

			prop, ok := ref.Value.Properties[name]
			if !ok {
				missing = append(missing, name)

				continue
			}

			if !strings.Contains(options, "omitempty") {
				wantRequired = append(wantRequired, name)
			}

			wantTypes[name] = jsonType(field.Type)
			types[name] = prop.Value.Type
		}

		if diff := cmp.Diff([]string(nil), missing); diff != "" {
			t.Errorf("openapi.yml %s lacks the %s JSON fields (-want +got):\n%s", schema, model, diff)
		}

		if diff := cmp.Diff(wantTypes, types); diff != "" {
			t.Errorf("openapi.yml %s field types differ from %s (-want +got):\n%s", schema, model, diff)
		}

		// a field without omitempty is always sent, so the schema requires it
		if diff := cmp.Diff(wantRequired, ref.Value.Required); diff != "" {
			t.Errorf("openapi.yml %s required fields differ from %s (-want +got):\n%s", schema, model, diff)
		}
	}
}

// jsonType is the OpenAPI type encoding/json writes a Go value of type typ as.
func jsonType(typ reflect.Type) string {
	switch typ.Kind() { //nolint:exhaustive // the kinds the soh types use
	case reflect.Bool:
		return openapi3.TypeBoolean
	case reflect.Int:
		return openapi3.TypeInteger
	case reflect.String:
		return openapi3.TypeString
	case reflect.Slice:
		return openapi3.TypeArray
	case reflect.Pointer:
		return jsonType(typ.Elem())
	default:
		return openapi3.TypeObject
	}
}
