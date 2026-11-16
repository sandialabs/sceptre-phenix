package web

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
)

// screenshotFixtures holds the API responses the docs screenshot script stands in for, keyed
// by method and /api/v1 path.
const screenshotFixtures = "../../js/e2e/docs-screenshots/fixtures.json"

// TestDocsScreenshotFixturesMatchOpenAPI checks each docs screenshot stand-in response against
// its route's 200 schema and rejects undocumented fields, so no page shows data the server lacks.
func TestDocsScreenshotFixturesMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile(screenshotFixtures)
	if err != nil {
		t.Fatalf("reading %s: %v", screenshotFixtures, err)
	}

	var fixtures map[string]any
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("parsing %s: %v", screenshotFixtures, err)
	}

	doc := contractSpec(t)

	for key, body := range fixtures {
		t.Run(key, func(t *testing.T) {
			method, path, ok := strings.Cut(key, " ")
			if !ok {
				t.Fatalf("fixture key %q is not METHOD /path", key)
			}

			schema := responseSchema(t, doc, method, path)

			if err := schema.VisitJSON(body); err != nil {
				t.Errorf("does not match the 200 response schema: %v", err)
			}

			for _, field := range undocumentedFields("", schema, body) {
				t.Errorf("sets %s, which openapi.yml does not document", field)
			}
		})
	}
}

// responseSchema returns the JSON schema of the 200 response of the operation serving the
// /api/v1 path, chosen as specOperation chooses it.
func responseSchema(t *testing.T, doc *openapi3.T, method, path string) *openapi3.Schema {
	t.Helper()

	template, op := specOperation(doc, method, "/api/v1"+path)
	if op == nil {
		t.Fatalf("openapi.yml documents no %s matching %s", method, path)
	}

	resp := op.Responses.Get(200)
	if resp == nil || resp.Value == nil {
		t.Fatalf("openapi.yml has no 200 response for %s %s", method, template)
	}

	media := resp.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		t.Fatalf("openapi.yml has no JSON 200 response for %s %s", method, template)
	}

	return media.Schema.Value
}

// undocumentedFields lists the object keys in v, written as JSON paths under at, that schema
// neither declares nor allows through additionalProperties.
func undocumentedFields(at string, schema *openapi3.Schema, v any) []string {
	if schema == nil {
		return nil
	}

	var found []string

	switch v := v.(type) {
	case map[string]any:
		props := properties(schema)
		extra := schema.AdditionalProperties

		for key, value := range v {
			field := at + "." + key

			switch {
			case props[key] != nil:
				found = append(found, undocumentedFields(field, props[key], value)...)
			case extra.Schema != nil:
				found = append(found, undocumentedFields(field, extra.Schema.Value, value)...)
			case extra.Has != nil && *extra.Has, len(props) == 0:
			default:
				found = append(found, field)
			}
		}
	case []any:
		if schema.Items != nil {
			for _, item := range v {
				found = append(found, undocumentedFields(at+"[]", schema.Items.Value, item)...)
			}
		}
	}

	slices.Sort(found)

	return slices.Compact(found)
}

// properties returns the properties schema declares, its allOf parts' included.
func properties(schema *openapi3.Schema) map[string]*openapi3.Schema {
	props := make(map[string]*openapi3.Schema)

	for name, ref := range schema.Properties {
		props[name] = ref.Value
	}

	for _, part := range schema.AllOf {
		maps.Copy(props, properties(part.Value))
	}

	return props
}

// undocumentedFields names each key of an object, at any depth, that its
// schema neither declares (allOf included) nor lets through, each once.
func TestUndocumentedFields(t *testing.T) {
	t.Parallel()

	item := openapi3.NewObjectSchema().WithProperty("k", openapi3.NewStringSchema())
	schema := openapi3.NewObjectSchema().
		WithProperty("name", openapi3.NewStringSchema()).
		WithProperty("tags", openapi3.NewArraySchema().WithItems(item)).
		WithProperty("meta", openapi3.NewObjectSchema().WithAnyAdditionalProperties()).
		WithProperty("labels", openapi3.NewObjectSchema().WithAdditionalProperties(item)).
		WithProperty("free", openapi3.NewObjectSchema()).
		WithProperty("closed", openapi3.NewObjectSchema().WithProperty("a", openapi3.NewStringSchema()).
			WithoutAdditionalProperties())
	schema.AllOf = openapi3.SchemaRefs{{Value: openapi3.NewObjectSchema().WithProperty("extra", openapi3.NewIntegerSchema())}}

	tests := map[string]struct {
		schema *openapi3.Schema
		body   string
		want   []string
	}{
		"documented":         {schema, `{"name":"a","tags":[{"k":"v"}],"extra":1}`, nil},
		"unknown key":        {schema, `{"name":"a","bogus":true}`, []string{".bogus"}},
		"in array items":     {schema, `{"tags":[{"k":"v","bad":1},{"bad":2}]}`, []string{".tags[].bad"}},
		"any extra allowed":  {schema, `{"meta":{"anything":{"deep":1}}}`, nil},
		"extra by schema":    {schema, `{"labels":{"x":{"k":"v","bad":1}}}`, []string{".labels.x.bad"}},
		"free-form object":   {schema, `{"free":{"x":1}}`, nil},
		"closed object":      {schema, `{"closed":{"a":"v","b":"w"}}`, []string{".closed.b"}},
		"several, sorted":    {schema, `{"z":1,"a":2}`, []string{".a", ".z"}},
		"scalar body":        {schema, `"text"`, nil},
		"no schema":          {nil, `{"x":1}`, nil},
		"array without item": {openapi3.NewArraySchema(), `[{"x":1}]`, nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var body any
			if err := json.Unmarshal([]byte(tc.body), &body); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", tc.body, err)
			}

			if diff := cmp.Diff(tc.want, undocumentedFields("", tc.schema, body)); diff != "" {
				t.Errorf("undocumentedFields(%s) mismatch (-want +got):\n%s", tc.body, diff)
			}
		})
	}
}
