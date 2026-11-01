package web

import (
	"maps"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"
)

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
