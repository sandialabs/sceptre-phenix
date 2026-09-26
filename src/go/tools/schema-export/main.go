// Command schema-export writes the JSON Schema files in the repository's
// schemas directory, for editors and SchemaStore: one per phenix config kind,
// each describing a whole config file (apiVersion, kind, metadata and spec)
// the way `phenix config create` validates one, and defs.schema.json, which
// holds the spec schemas they reference, so each is written once.
//
// phenix checks a config's envelope against the Config schema in types.OpenAPI
// and its spec against the component of its kind in the latest embedded
// OpenAPI file (types.ValidateConfigSpec uses version.LATEST_VERSION, whatever
// the config's apiVersion), and reads configs of the kind's stored apiVersion
// (version.StoredVersion). Each schema requires that apiVersion, and checks
// spec against that component.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"phenix/store"
	"phenix/types"
	"phenix/types/version"
)

const (
	jsonTypeNull      = "null"
	schemaDraft202012 = "https://json-schema.org/draft/2020-12/schema"
	repoRawPrefix     = "https://raw.githubusercontent.com/sandialabs/sceptre-phenix/main/schemas"
	componentsPrefix  = "#/components/schemas/"
	defsPrefix        = "#/$defs/"
	schemaFileSuffix  = ".schema.json"
	// defsFile holds, under $defs, the component schemas the per-kind schemas
	// reference by a URI relative to theirs.
	defsFile = "defs" + schemaFileSuffix
)

type openAPIDoc struct {
	Components struct {
		Schemas map[string]any `yaml:"schemas"`
	} `yaml:"components"`
}

func main() {
	var outDir string

	flag.StringVar(&outDir, "out", "../../schemas", "directory where JSON schema files will be written")
	flag.Parse()

	if err := export(outDir); err != nil {
		exitf("%v", err)
	}
}

// export writes one schema per phenix config kind into outDir, and the defs
// file with the components they reference.
func export(outDir string) error {
	envelope, err := configEnvelope()
	if err != nil {
		return err
	}

	specs, err := componentSchemas(version.LATEST_VERSION)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outDir, err)
	}

	defs := map[string]any{}

	for _, kind := range envelope.kinds {
		doc, err := configSchema(kind, envelope.metadata, specs)
		if err != nil {
			return err
		}

		used, err := referenced(kind, specs)
		if err != nil {
			return err
		}

		maps.Copy(defs, used)

		if err := writeSchema(outDir, strings.ToLower(kind)+schemaFileSuffix, doc); err != nil {
			return err
		}
	}

	return writeSchema(outDir, defsFile, defsSchema(defs))
}

func writeSchema(outDir, fileName string, doc map[string]any) error {
	formatted, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schema file %s: %w", fileName, err)
	}

	formatted = append(formatted, '\n')

	if err := os.WriteFile(filepath.Join(outDir, fileName), formatted, 0o644); err != nil {
		return fmt.Errorf("write schema file %s: %w", fileName, err)
	}

	return nil
}

// envelope is what the Config schema in types.OpenAPI says of every config.
type envelope struct {
	// kinds are the config kinds, in the order the schema lists them.
	kinds []string
	// metadata is the schema of metadata.
	metadata map[string]any
}

func configEnvelope() (*envelope, error) {
	doc, err := parseOpenAPIDoc(types.OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("parse config OpenAPI: %w", err)
	}

	config, _ := doc.Components.Schemas["Config"].(map[string]any)
	properties, _ := config["properties"].(map[string]any)
	kind, _ := properties["kind"].(map[string]any)
	enum, _ := kind["enum"].([]any)
	metadata, _ := properties["metadata"].(map[string]any)

	if len(enum) == 0 || metadata == nil {
		return nil, errors.New("the config OpenAPI has no Config kind enum or metadata schema")
	}

	result := &envelope{kinds: make([]string, 0, len(enum)), metadata: rewriteSchema(metadata)}

	for _, value := range enum {
		name, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("config kind %v is not a string", value)
		}

		result.kinds = append(result.kinds, name)
	}

	return result, nil
}

// componentSchemas reads the component schemas of an embedded OpenAPI file,
// rewritten as JSON Schema 2020-12.
func componentSchemas(schemaVersion string) (map[string]any, error) {
	raw, err := version.ReadSchemaFile(schemaVersion)
	if err != nil {
		return nil, fmt.Errorf("read %s OpenAPI: %w", schemaVersion, err)
	}

	doc, err := parseOpenAPIDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s OpenAPI: %w", schemaVersion, err)
	}

	if len(doc.Components.Schemas) == 0 {
		return nil, fmt.Errorf("%s OpenAPI has no components.schemas section", schemaVersion)
	}

	specs := make(map[string]any, len(doc.Components.Schemas))

	for name, schema := range doc.Components.Schemas {
		asMap, ok := schema.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s OpenAPI component %s is not a schema", schemaVersion, name)
		}

		specs[name] = rewriteSchema(asMap)
	}

	return specs, nil
}

// configSchema is the schema of a config file of kind: the envelope, with the
// kind's stored apiVersion, and a spec that references the kind's component in
// the defs file.
func configSchema(kind string, metadata, specs map[string]any) (map[string]any, error) {
	stored, ok := version.StoredVersion[kind]
	if !ok {
		return nil, fmt.Errorf("no stored version for config kind %s", kind)
	}

	if _, ok := specs[kind]; !ok {
		return nil, fmt.Errorf("the %s OpenAPI has no %s schema", version.LATEST_VERSION, kind)
	}

	apiVersion := store.APIGroup + "/" + stored

	return map[string]any{
		"$schema": schemaDraft202012,
		"$id":     repoRawPrefix + "/" + strings.ToLower(kind) + schemaFileSuffix,
		"title":   "phenix " + kind + " config",
		"description": fmt.Sprintf(
			"A phenix %s config file (apiVersion %s), checked as `phenix config create` checks it. "+
				"Generated by src/go/tools/schema-export from the embedded OpenAPI schemas; do not edit.",
			kind, apiVersion,
		),
		"type":     "object",
		"required": []any{"apiVersion", "kind", "metadata", "spec"},
		"properties": map[string]any{
			"apiVersion": map[string]any{"const": apiVersion},
			"kind":       map[string]any{"const": kind},
			"metadata":   metadata,
			"spec":       map[string]any{"$ref": defsFile + defsPrefix + kind},
		},
	}, nil
}

// defsSchema is the defs file: the components the per-kind schemas reference,
// under $defs. On its own it accepts any document.
func defsSchema(defs map[string]any) map[string]any {
	return map[string]any{
		"$schema": schemaDraft202012,
		"$id":     repoRawPrefix + "/" + defsFile,
		"title":   "phenix config specs",
		"description": "The spec of each phenix config kind, and the schemas those reference, " +
			"for the per-kind config schemas beside this file to reference. " +
			"Generated by src/go/tools/schema-export from the embedded OpenAPI schemas; do not edit.",
		"$defs": defs,
	}
}

// referenced returns the component named root and every component it
// references, directly or not.
func referenced(root string, specs map[string]any) (map[string]any, error) {
	defs := map[string]any{}
	pending := []string{root}

	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		if _, done := defs[name]; done {
			continue
		}

		schema, ok := specs[name]
		if !ok {
			return nil, fmt.Errorf("component %s references unknown component %s", root, name)
		}

		defs[name] = schema

		for _, ref := range refs(schema) {
			target, ok := strings.CutPrefix(ref, defsPrefix)
			if !ok {
				return nil, fmt.Errorf("component %s has an unsupported reference %s", name, ref)
			}

			pending = append(pending, target)
		}
	}

	return defs, nil
}

// refs lists the $ref values in a rewritten schema tree.
func refs(node any) []string {
	var found []string

	switch typed := node.(type) {
	case map[string]any:
		if ref, ok := typed["$ref"].(string); ok {
			found = append(found, ref)
		}

		for _, key := range slices.Sorted(maps.Keys(typed)) {
			found = append(found, refs(typed[key])...)
		}
	case []any:
		for _, child := range typed {
			found = append(found, refs(child)...)
		}
	}

	return found
}

func parseOpenAPIDoc(raw []byte) (*openAPIDoc, error) {
	var doc openAPIDoc

	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}

	return &doc, nil
}

// checkedFormats are the string formats kin-openapi, which phenix validates
// configs with, checks by default. It accepts any string for the rest (ipv4),
// and phenix writes configs with "" in such fields, so the exported schemas
// leave those formats out rather than refuse configs phenix accepts.
var checkedFormats = []string{"byte", "date", "date-time"} //nolint:gochecknoglobals // fixed list

// rewriteSchema returns a copy of an OpenAPI 3.0 schema object as JSON Schema
// 2020-12: component references point into $defs, nullable becomes a null
// type (or, with no type, an alternative of null), example becomes examples,
// and formats phenix does not check are dropped. Only schema keywords are
// rewritten, so property names and example data are kept as they are.
func rewriteSchema(schema map[string]any) map[string]any {
	out := make(map[string]any, len(schema))

	for key, value := range schema {
		switch key {
		case "$ref":
			ref, _ := value.(string)
			out[key] = strings.Replace(ref, componentsPrefix, defsPrefix, 1)
		case "properties":
			properties, _ := value.(map[string]any)
			rewritten := make(map[string]any, len(properties))

			for name, property := range properties {
				rewritten[name] = rewriteSubschema(property)
			}

			out[key] = rewritten
		case "items", "additionalProperties", "not":
			out[key] = rewriteSubschema(value)
		case "allOf", "anyOf", "oneOf":
			list, _ := value.([]any)
			rewritten := make([]any, len(list))

			for i, child := range list {
				rewritten[i] = rewriteSubschema(child)
			}

			out[key] = rewritten
		case "nullable", "example":
			// Rewritten below.
		case "format":
			if format, _ := value.(string); slices.Contains(checkedFormats, format) {
				out[key] = value
			}
		default:
			out[key] = value
		}
	}

	if example, ok := schema["example"]; ok {
		out["examples"] = []any{example}
	}

	// A schema with properties but no type (an allOf extending an object
	// schema) is an object schema; saying so keeps strict validators, such as
	// SchemaStore's, from refusing it.
	_, typed := out["type"]
	_, properties := out["properties"]
	_, required := out["required"]

	if !typed && (properties || required) {
		out["type"] = "object"
	}

	if nullable, _ := schema["nullable"].(bool); nullable {
		typ, ok := out["type"]
		if ok {
			out["type"] = addNullType(typ)

			return out
		}

		// A nullable schema with no type (a oneOf of alternatives) takes null
		// too, as kin-openapi validates it, and phenix writes null there.
		nullOr := map[string]any{"anyOf": []any{map[string]any{"type": jsonTypeNull}, out}}

		for _, annotation := range []string{"title", "description"} {
			if text, ok := out[annotation]; ok {
				nullOr[annotation] = text
			}
		}

		return nullOr
	}

	return out
}

// rewriteSubschema rewrites a value in a subschema position, which may also be
// a boolean schema.
func rewriteSubschema(value any) any {
	if schema, ok := value.(map[string]any); ok {
		return rewriteSchema(schema)
	}

	return value
}

func addNullType(rawType any) any {
	switch t := rawType.(type) {
	case string:
		if t == jsonTypeNull {
			return t
		}

		return []any{t, jsonTypeNull}
	case []any:
		if slices.Contains(t, any(jsonTypeNull)) {
			return t
		}

		return append(slices.Clone(t), jsonTypeNull)
	default:
		return rawType
	}
}

func exitf(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}
