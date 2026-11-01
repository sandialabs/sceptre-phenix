package web

import (
	"encoding/json"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
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

	doc, err := openapi3.NewLoader().LoadFromFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("loading openapi.yml: %v", err)
	}

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

// responseSchema returns the JSON schema of the 200 response of the operation serving path.
func responseSchema(t *testing.T, doc *openapi3.T, method, path string) *openapi3.Schema {
	t.Helper()

	for template, item := range doc.Paths {
		quoted := regexp.QuoteMeta(pathParams.ReplaceAllString(template, "{}"))
		pattern := "^" + strings.ReplaceAll(quoted, `\{\}`, `[^/]+`) + "$"

		if !regexp.MustCompile(pattern).MatchString(path) {
			continue
		}

		op := item.GetOperation(strings.ToUpper(method))
		if op == nil {
			t.Fatalf("openapi.yml has no %s %s", method, template)
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

	t.Fatalf("openapi.yml documents no path matching %s", path)

	return nil
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
