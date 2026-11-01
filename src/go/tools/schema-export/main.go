// Command schema-export writes the JSON Schema files in the repository's
// schemas directory, for editors and SchemaStore: one per phenix config kind,
// each describing a whole config file (apiVersion, kind, metadata and spec)
// the way `phenix config create` validates one, and defs.schema.json, which
// holds the spec schemas they reference, so each is written once. It also
// writes workflow.schema.json, for the workflow config (phenix.yml) that
// `phenix workflow apply` reads, and phenix.schema.json, which picks one of
// the others by the apiVersion and kind of a file instead of by its name.
//
// phenix checks a config's envelope against the Config schema in types.OpenAPI
// and its spec against the component of its kind in the embedded OpenAPI file
// of its apiVersion (types.ValidateConfigSpec), and reads configs of the kind's
// stored apiVersion (version.StoredVersion). Each schema requires that
// apiVersion, and checks spec against the component in that file. A workflow
// config is no config kind: phenix checks the whole file against the Workflow
// component of the v0 file (api/workflow.ValidateDocument).
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	// workflowKind and workflowVersion name the component, and the embedded
	// OpenAPI file, that api/workflow validates workflow configs against.
	workflowKind    = "Workflow"
	workflowVersion = "v0"
	// dispatchFile applies the schema of a file's kind to any file whose
	// apiVersion is in the phenix API group, and accepts every other file.
	dispatchFile = "phenix" + schemaFileSuffix
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

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outDir, err)
	}

	defs := map[string]any{}
	specsByVersion := map[string]map[string]any{}

	for _, kind := range envelope.kinds {
		stored, ok := version.StoredVersion[kind]
		if !ok {
			return fmt.Errorf("no stored version for config kind %s", kind)
		}

		specs, ok := specsByVersion[stored]
		if !ok {
			specs, err = componentSchemas(stored)
			if err != nil {
				return err
			}

			specsByVersion[stored] = specs
		}

		doc, err := configSchema(kind, stored, envelope, specs)
		if err != nil {
			return err
		}

		used, err := referenced(kind, specs)
		if err != nil {
			return err
		}

		for name, schema := range used {
			if existing, ok := defs[name]; ok && !reflect.DeepEqual(existing, schema) {
				return fmt.Errorf("component %s differs between the embedded OpenAPI files", name)
			}

			defs[name] = schema
		}

		if err := writeSchema(outDir, strings.ToLower(kind)+schemaFileSuffix, doc); err != nil {
			return err
		}
	}

	if err := writeSchema(outDir, defsFile, defsSchema(defs)); err != nil {
		return err
	}

	workflow, err := workflowSchema()
	if err != nil {
		return err
	}

	if err := writeSchema(outDir, strings.ToLower(workflowKind)+schemaFileSuffix, workflow); err != nil {
		return err
	}

	return writeSchema(outDir, dispatchFile, dispatchSchema(append(slices.Clone(envelope.kinds), workflowKind)))
}

func writeSchema(outDir, fileName string, doc map[string]any) error {
	var formatted bytes.Buffer

	encoder := json.NewEncoder(&formatted)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("marshal schema file %s: %w", fileName, err)
	}

	if err := os.WriteFile(filepath.Join(outDir, fileName), formatted.Bytes(), 0o644); err != nil {
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
	// spec is the schema of spec, which each kind's component narrows.
	spec map[string]any
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
	spec, _ := properties["spec"].(map[string]any)

	if len(enum) == 0 || metadata == nil || spec == nil {
		return nil, errors.New("the config OpenAPI has no Config kind enum, metadata schema or spec schema")
	}

	result := &envelope{
		kinds:    make([]string, 0, len(enum)),
		metadata: rewriteSchema(metadata),
		spec:     rewriteSchema(spec),
	}

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
// kind's stored apiVersion, and a spec that is both the envelope's spec and the
// kind's component in the defs file. The envelope keeps a whole config's spec
// an object where the component also takes null, as Scenario does for an
// Experiment's scenario. specs are the components of the stored version's file.
func configSchema(kind, stored string, env *envelope, specs map[string]any) (map[string]any, error) {
	if _, ok := specs[kind]; !ok {
		return nil, fmt.Errorf("the %s OpenAPI has no %s schema", stored, kind)
	}

	apiVersion := store.APIGroup + "/" + stored

	spec := maps.Clone(env.spec)
	spec["$ref"] = defsFile + defsPrefix + kind

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
			"metadata":   env.metadata,
			"spec":       spec,
		},
	}, nil
}

// workflowSchema is the schema of a workflow config file: the Workflow
// component, which describes the whole file, with its one allowed apiVersion
// and kind written as const.
func workflowSchema() (map[string]any, error) {
	specs, err := componentSchemas(workflowVersion)
	if err != nil {
		return nil, err
	}

	component, ok := specs[workflowKind].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the %s OpenAPI has no %s schema", workflowVersion, workflowKind)
	}

	if found := refs(component); len(found) > 0 {
		return nil, fmt.Errorf("the %s schema has references %v, which the export does not support", workflowKind, found)
	}

	properties, ok := component["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the %s schema has no properties", workflowKind)
	}

	doc := maps.Clone(component)
	properties = maps.Clone(properties)

	var apiVersion string

	for _, key := range []string{"apiVersion", "kind"} {
		property, _ := properties[key].(map[string]any)
		enum, _ := property["enum"].([]any)

		if len(enum) != 1 {
			return nil, fmt.Errorf("the %s schema allows %d values of %s, not one", workflowKind, len(enum), key)
		}

		property = maps.Clone(property)
		delete(property, "enum")
		property["const"] = enum[0]
		properties[key] = property

		if key == "apiVersion" {
			apiVersion, _ = enum[0].(string)
		}
	}

	description, _ := component["description"].(string)

	doc["properties"] = properties
	doc["$schema"] = schemaDraft202012
	doc["$id"] = repoRawPrefix + "/" + strings.ToLower(workflowKind) + schemaFileSuffix
	doc["title"] = "phenix " + workflowKind + " config"
	doc["description"] = fmt.Sprintf(
		"%s A phenix.yml file (apiVersion %s), checked as `phenix workflow apply` checks it. "+
			"Generated by src/go/tools/schema-export from the embedded OpenAPI schemas; do not edit.",
		description, apiVersion,
	)

	return doc, nil
}

// dispatchSchema is the schema that applies the schema of each of kinds to a
// file of that kind. It checks only files whose apiVersion is in the phenix API
// group, so it can be applied to every YAML file in a directory: an editor
// cannot tell a phenix config by its file name.
func dispatchSchema(kinds []string) map[string]any {
	enum := make([]any, 0, len(kinds))
	branches := make([]any, 0, len(kinds))

	for _, kind := range kinds {
		enum = append(enum, kind)
		branches = append(branches, map[string]any{
			"if": map[string]any{
				"type":       "object",
				"required":   []any{"kind"},
				"properties": map[string]any{"kind": map[string]any{"const": kind}},
			},
			"then": map[string]any{"$ref": strings.ToLower(kind) + schemaFileSuffix},
		})
	}

	return map[string]any{
		"$schema": schemaDraft202012,
		"$id":     repoRawPrefix + "/" + dispatchFile,
		"title":   "phenix config",
		"description": "Any phenix config file, checked against the schema of its kind. " +
			"A document whose apiVersion is not in the " + store.APIGroup + " API group is not checked. " +
			"Generated by src/go/tools/schema-export from the embedded OpenAPI schemas; do not edit.",
		"if": map[string]any{
			"type":     "object",
			"required": []any{"apiVersion"},
			"properties": map[string]any{
				"apiVersion": map[string]any{"type": "string", "pattern": "^" + regexp.QuoteMeta(store.APIGroup) + "/"},
			},
		},
		"then": map[string]any{
			"type":       "object",
			"required":   []any{"kind"},
			"properties": map[string]any{"kind": map[string]any{"enum": enum}},
			"allOf":      branches,
		},
	}
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
		case "pattern":
			if pattern, ok := value.(string); ok {
				out[key] = ecmaPattern(pattern)
			} else {
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

// ecmaPattern rewrites the case-insensitive groups, (?i:...), of a Go regexp,
// which ECMA-262 regular expressions do not have, as non-capturing groups that
// match each ASCII letter in either case. A group holding anything but
// letters, digits, - and | is kept, for the export tests to report.
func ecmaPattern(pattern string) string {
	const open = "(?i:"

	var out strings.Builder

	rest := pattern

	for {
		start := strings.Index(rest, open)
		if start < 0 {
			break
		}

		end := strings.IndexByte(rest[start:], ')')
		if end < 0 {
			break
		}

		group := rest[start+len(open) : start+end]
		if strings.Trim(group, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-|") != "" {
			break
		}

		out.WriteString(rest[:start])
		out.WriteString("(?:")

		for _, char := range group {
			lower, upper := strings.ToLower(string(char)), strings.ToUpper(string(char))
			if lower == upper {
				out.WriteRune(char)
			} else {
				out.WriteString("[" + lower + upper + "]")
			}
		}

		out.WriteString(")")

		rest = rest[start+end+1:]
	}

	out.WriteString(rest)

	return out.String()
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
