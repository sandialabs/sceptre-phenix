package web

import (
	"regexp"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func loadOpenAPIDocument(t *testing.T) *openapi3.T {
	t.Helper()

	doc, err := openapi3.NewLoader().LoadFromFile("public/docs/openapi.yml")
	if err != nil {
		t.Fatalf("loading openapi.yml: %v", err)
	}

	return doc
}

// TestOpenAPIDocumentIsValid asserts the served API reference is a valid
// OpenAPI 3 document.
func TestOpenAPIDocumentIsValid(t *testing.T) {
	doc := loadOpenAPIDocument(t)

	if err := doc.Validate(t.Context()); err != nil {
		t.Errorf("openapi.yml is not a valid OpenAPI document: %v", err)
	}
}

// TestOpenAPIPathParametersMatchTemplates asserts every operation names the
// variables of its path template, which Validate checks only by count.
func TestOpenAPIPathParametersMatchTemplates(t *testing.T) {
	variable := regexp.MustCompile(`\{([^}]+)\}`)

	for path, item := range loadOpenAPIDocument(t).Paths {
		matches := variable.FindAllStringSubmatch(path, -1)
		want := make([]string, 0, len(matches))

		for _, match := range matches {
			want = append(want, match[1])
		}

		slices.Sort(want)

		for method, op := range item.Operations() {
			var got []string

			for _, params := range []openapi3.Parameters{item.Parameters, op.Parameters} {
				for _, ref := range params {
					if ref.Value != nil && ref.Value.In == openapi3.ParameterInPath {
						got = append(got, ref.Value.Name)
					}
				}
			}

			slices.Sort(got)

			if !slices.Equal(got, want) {
				t.Errorf("%s %s path parameters = %v, want %v", method, path, got, want)
			}
		}
	}
}
