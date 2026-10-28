package soh

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
)

// The API reference documents every JSON field the soh endpoint sends: the
// network, its nodes, each VM's results and each check.
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

		var missing []string

		for index := range model.NumField() {
			name, _, _ := strings.Cut(model.Field(index).Tag.Get("json"), ",")
			if _, ok := ref.Value.Properties[name]; !ok && name != "-" {
				missing = append(missing, name)
			}
		}

		if diff := cmp.Diff([]string(nil), missing); diff != "" {
			t.Errorf("openapi.yml %s lacks the %s JSON fields (-want +got):\n%s", schema, model, diff)
		}
	}
}
