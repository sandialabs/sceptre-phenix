package builder

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

const (
	// SchemaDialect is the JSON Schema dialect the generated bundle is written
	// against.
	SchemaDialect = "https://json-schema.org/draft/2020-12/schema"

	// PhenixDefPrefix namespaces the phenix v1 OpenAPI component schemas bundled
	// into the builder schema's $defs. Topology node and property forms resolve
	// against these definitions.
	PhenixDefPrefix = "phenix.v1."

	// PhenixV2DefPrefix namespaces the phenix v2 OpenAPI component schemas
	// bundled into the builder schema's $defs. Scenario content is validated
	// against these definitions, because the latest stored scenario version is
	// v2 (see [ScenarioAPIVersion]); the v1 Scenario schema only models
	// experiment app names and cannot validate current scenario content.
	PhenixV2DefPrefix = "phenix.v2."

	// openAPIRefPrefix is the reference prefix used by the phenix OpenAPI
	// documents, rewritten to local $defs references during bundling.
	openAPIRefPrefix = "#/components/schemas/"

	// localDefRef is the local reference prefix of the generated bundle.
	localDefRef = "#/$defs/"
)

// Schema returns the standalone Builder v1 JSON Schema as a freshly built map.
//
// The bundle is self contained: the phenix v1 and v2 OpenAPI component schemas
// are embedded under $defs (namespaced with [PhenixDefPrefix] and
// [PhenixV2DefPrefix]) and every "#/components/schemas/..." reference is
// rewritten to a local "#/$defs/..." reference, so device specs, property
// forms, and scenario content resolve without a network fetch. It is suitable
// for a web schema endpoint and for JSON Forms.
//
// Object shapes that [Decode] rejects unknown fields for are marked
// "additionalProperties": false. Free-form phenix payloads (device specs,
// scenario content) keep their own schemas.
//
// Referential integrity between edges, handles, nodes, and networks cannot be
// expressed in JSON Schema; it is structurally represented (typed identifier
// references and required fields) and enforced by [Document.Validate].
func Schema() (map[string]any, error) {
	defs, err := PhenixDefs()
	if err != nil {
		return nil, err
	}

	v2Defs, err := PhenixV2Defs()
	if err != nil {
		return nil, err
	}

	maps.Copy(defs, v2Defs)
	maps.Copy(defs, builderDefs())

	root := objectDef(
		"Library independent document model of the phenix topology builder.",
		[]string{
			schemaKey, revisionKey, keyID, keyNodes, "networks", "edges",
			keyViewport, keyGrid,
		},
		map[string]any{
			schemaKey: withDescription(
				constDef(SchemaURI),
				"Identifies the builder document schema.",
			),
			revisionKey: withDescription(
				constDef(SchemaRevision),
				"Revision of the builder document schema.",
			),
			keyID:          ref("identifier"),
			keyName:        stringDef(""),
			keyDescription: stringDef(""),
			keyNodes:       arrayDef(ref("node")),
			"networks":     arrayDef(ref("network")),
			"edges":        arrayDef(ref("edge")),
			"viewport":     ref("viewport"),
			"grid":         ref("grid"),
			"scenario":     ref("scenario"),
			"source":       ref("source"),
		},
	)

	root[schemaKey] = SchemaDialect
	root["$id"] = SchemaURI
	root["title"] = "phenix builder document"
	root["$defs"] = defs

	return root, nil
}

// SchemaJSON returns the Builder v1 JSON Schema as deterministic, indented
// JSON. Object keys are sorted by encoding/json, so repeated calls are byte
// identical.
func SchemaJSON() ([]byte, error) {
	schema, err := Schema()
	if err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling builder schema: %w", err)
	}

	return data, nil
}

// PhenixDefs returns the phenix v1 OpenAPI component schemas prepared for
// inclusion in a JSON Schema $defs map: keys are namespaced with
// [PhenixDefPrefix] and OpenAPI component references are rewritten to local
// $defs references.
func PhenixDefs() (map[string]any, error) {
	return BundleOpenAPIDefs(v1.OpenAPI, PhenixDefPrefix)
}

// PhenixV2Defs returns the phenix v2 OpenAPI component schemas prepared for
// inclusion in a JSON Schema $defs map, namespaced with [PhenixV2DefPrefix].
func PhenixV2Defs() (map[string]any, error) {
	return BundleOpenAPIDefs(v2.OpenAPI, PhenixV2DefPrefix)
}

// BundleOpenAPIDefs converts the component schemas of an OpenAPI document into
// a JSON Schema $defs map. Definition names are prefixed with prefix and every
// "#/components/schemas/<name>" reference is rewritten to
// "#/$defs/<prefix><name>". The conversion is deterministic and does not
// perform any I/O.
func BundleOpenAPIDefs(document []byte, prefix string) (map[string]any, error) {
	var parsed struct {
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}

	if err := yaml.Unmarshal(document, &parsed); err != nil {
		return nil, fmt.Errorf("parsing OpenAPI document: %w", err)
	}

	defs := make(map[string]any, len(parsed.Components.Schemas))

	for _, name := range slices.Sorted(maps.Keys(parsed.Components.Schemas)) {
		schema, err := normalizeSpec(parsed.Components.Schemas[name])
		if err != nil {
			return nil, fmt.Errorf("reading OpenAPI schema %q: %w", name, err)
		}

		defs[prefix+name] = convertOpenAPISchema(schema, prefix)
	}

	return defs, nil
}

// convertOpenAPISchema rewrites OpenAPI references and converts nullable fields
// into JSON Schema 2020-12 unions.
func convertOpenAPISchema(value any, prefix string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		nullable, _ := typed["nullable"].(bool)

		for key, val := range typed {
			if key == "nullable" || key == "pattern" && val == nil {
				continue
			}

			if key == "$ref" {
				if asString, ok := val.(string); ok {
					out[key] = rewriteRef(asString, prefix)

					continue
				}
			}

			out[key] = convertOpenAPISchema(val, prefix)
		}

		if !nullable {
			return out
		}

		switch schemaType := out["type"].(type) {
		case string:
			out["type"] = []any{schemaType, "null"}

			return out
		case []any:
			if !slices.Contains(schemaType, any("null")) {
				out["type"] = append(schemaType, "null")
			}

			return out
		}

		return map[string]any{
			"anyOf": []any{
				out,
				map[string]any{"type": "null"},
			},
		}
	case []any:
		out := make([]any, len(typed))

		for i, val := range typed {
			out[i] = convertOpenAPISchema(val, prefix)
		}

		return out
	default:
		return value
	}
}

func rewriteRef(reference, prefix string) string {
	name, found := strings.CutPrefix(reference, openAPIRefPrefix)
	if !found {
		return reference
	}

	return localDefRef + prefix + name
}

func ref(name string) map[string]any {
	return map[string]any{"$ref": localDefRef + name}
}

// keys repeated across the schema document.
const (
	// schemaKey is the JSON Schema dialect keyword, which is also the name of
	// the builder document root field.
	schemaKey = "$schema"
	// revisionKey names the builder document revision field.
	revisionKey = "revision"

	keyKind        = "kind"
	keyNodes       = "nodes"
	keyGrid        = "grid"
	keyViewport    = "viewport"
	keySpec        = "spec"
	keyAPIVersion  = "apiVersion"
	keyDigest      = "digest"
	keyType        = "type"
	keyProperties  = "properties"
	keyRequired    = "required"
	keyDescription = "description"
	keyName        = "name"
	keyID          = "id"
	keyColor       = "color"
	keyPosition    = "position"
	keySize        = "size"
	keyNetworkID   = "networkId"

	// uuidPattern matches the canonical RFC 4122 UUID text form with a known
	// version and the RFC 4122 variant, mirroring [IsUUID].
	uuidPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-` +
		`[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`
)

// objectDef builds an object schema that forbids unknown properties, matching
// the strict decoding performed by [Decode].
func objectDef(description string, required []string, properties map[string]any) map[string]any {
	def := map[string]any{
		keyType:                "object",
		"additionalProperties": false,
		keyProperties:          properties,
	}

	if description != "" {
		def[keyDescription] = description
	}

	if len(required) > 0 {
		def[keyRequired] = anyStrings(required)
	}

	return def
}

// stringDef builds a string schema.
func stringDef(description string) map[string]any {
	def := map[string]any{keyType: "string"}

	if description != "" {
		def[keyDescription] = description
	}

	return def
}

// nameDef builds a schema for a non-empty, whitespace-free name.
func nameDef(description string) map[string]any {
	def := stringDef(description)
	def["minLength"] = 1
	def["pattern"] = `^\S+$`

	return def
}

// numberDef builds an unbounded number schema.
func numberDef() map[string]any {
	return map[string]any{keyType: "number"}
}

// positiveNumberDef builds a number schema restricted to strictly positive
// values, matching the strict positivity enforced by [Document.Validate].
func positiveNumberDef() map[string]any {
	return map[string]any{keyType: "number", "exclusiveMinimum": 0}
}

// boolDef builds a boolean schema.
func boolDef() map[string]any {
	return map[string]any{keyType: "boolean"}
}

// digestDef builds a schema for a "sha256:<hex>" content digest.
func digestDef(description string) map[string]any {
	def := stringDef(description)
	def["pattern"] = `^sha256:[0-9a-f]{64}$`

	return def
}

// arrayDef builds an array schema over the given item schema.
func arrayDef(items map[string]any) map[string]any {
	return map[string]any{keyType: "array", "items": items}
}

// enumDef builds a string schema restricted to the given values.
func enumDef(description string, values []any) map[string]any {
	def := stringDef(description)
	def["enum"] = values

	return def
}

// refDef wraps a reference so that sibling keywords such as descriptions are
// honoured by every JSON Schema implementation.
func refDef(name, description string) map[string]any {
	return map[string]any{
		"allOf":       []any{ref(name)},
		"description": description,
	}
}

// anyStrings converts a string slice into the []any form JSON Schema keywords
// such as required and enum expect.
func anyStrings(values []string) []any {
	out := make([]any, len(values))

	for i, value := range values {
		out[i] = value
	}

	return out
}

// nodeKindKeys returns the node kinds in their canonical order.
func nodeKindKeys() []NodeKind {
	return []NodeKind{NodeKindDevice, NodeKindSwitch, NodeKindNote, NodeKindGroup}
}

// builderDefs returns the builder specific definitions of the schema bundle.
func builderDefs() map[string]any {
	defs := map[string]any{
		"identifier":      identifierDef(),
		"iconKey":         enumDef("Builder local icon hint; empty selects the default icon.", iconKeyEnum()),
		keyPosition:       positionDef(),
		keySize:           sizeDef(),
		keyViewport:       viewportDef(),
		keyGrid:           gridDef(),
		"interfaceHandle": interfaceHandleDef(),
		"node":            nodeDef(),
		"network":         networkDef(),
		"edge":            edgeDef(),
		"scenario":        scenarioDef(),
		"source":          sourceDef(),
	}

	defs[string(NodeKindDevice)] = deviceDef()
	defs[string(NodeKindSwitch)] = switchDef()
	defs[string(NodeKindNote)] = noteDef()
	defs[string(NodeKindGroup)] = groupDef()

	return defs
}

func identifierDef() map[string]any {
	def := stringDef(
		"Stable RFC 4122 UUID. Identifiers are compared case-insensitively; " +
			"the front end mints new identifiers with crypto.randomUUID.",
	)
	def["format"] = "uuid"
	def["pattern"] = uuidPattern

	return def
}

func positionDef() map[string]any {
	return objectDef("", []string{"x", "y"}, map[string]any{
		"x": numberDef(),
		"y": numberDef(),
	})
}

func sizeDef() map[string]any {
	return objectDef("", []string{"width", "height"}, map[string]any{
		"width":  positiveNumberDef(),
		"height": positiveNumberDef(),
	})
}

func viewportDef() map[string]any {
	return objectDef("", []string{"x", "y", "zoom"}, map[string]any{
		"x":    numberDef(),
		"y":    numberDef(),
		"zoom": positiveNumberDef(),
	})
}

func gridDef() map[string]any {
	return objectDef("", []string{"enabled", keySize, "snap"}, map[string]any{
		"enabled": boolDef(),
		keySize:   positiveNumberDef(),
		"snap":    boolDef(),
	})
}

func interfaceHandleDef() map[string]any {
	return objectDef(
		"Stable mapping of a canvas handle onto a named interface of the device spec.",
		[]string{keyID, keyName, "index"},
		map[string]any{
			keyID:   ref("identifier"),
			keyName: nameDef(""),
			"index": map[string]any{keyType: "integer", "minimum": 0},
		},
	)
}

func deviceDef() map[string]any {
	return objectDef("", []string{"hostname", keySpec, "interfaces"}, map[string]any{
		"hostname": nameDef(""),
		"iconKey":  ref("iconKey"),
		keySpec: map[string]any{
			keyDescription: "Complete phenix topology node spec.",
			"oneOf": []any{
				ref(PhenixDefPrefix + "minimega_node"),
				ref(PhenixDefPrefix + "external_node"),
			},
		},
		"interfaces": arrayDef(ref("interfaceHandle")),
	})
}

func switchDef() map[string]any {
	return objectDef(
		"Visual hub bound to exactly one network.",
		[]string{keyNetworkID},
		map[string]any{keyNetworkID: ref("identifier")},
	)
}

func noteDef() map[string]any {
	return objectDef("", []string{"text"}, map[string]any{
		"text":   stringDef(""),
		keyColor: stringDef(""),
	})
}

func groupDef() map[string]any {
	return objectDef("", nil, map[string]any{
		"title":     stringDef(""),
		keyColor:    stringDef(""),
		"collapsed": boolDef(),
	})
}

func nodeDef() map[string]any {
	kinds := nodeKindKeys()

	enum := make([]any, len(kinds))
	discriminated := make([]any, len(kinds))

	for i, kind := range kinds {
		enum[i] = string(kind)
		discriminated[i] = kindBranch(kind, kinds)
	}

	def := objectDef(
		"Canvas node. The payload key must match the node kind.",
		[]string{keyID, keyKind, keyPosition},
		map[string]any{
			keyID:       ref("identifier"),
			keyKind:     enumDef("", enum),
			"label":     stringDef(""),
			keyPosition: ref(keyPosition),
			keySize:     ref(keySize),
			"parentId":  refDef("identifier", "Identifier of the group node this node belongs to."),

			string(NodeKindDevice): ref(string(NodeKindDevice)),
			string(NodeKindSwitch): ref(string(NodeKindSwitch)),
			string(NodeKindNote):   ref(string(NodeKindNote)),
			string(NodeKindGroup):  ref(string(NodeKindGroup)),
		},
	)

	def["allOf"] = discriminated

	return def
}

// kindBranch builds the if/then branch requiring a node of the given kind to
// carry its own payload and no other.
func kindBranch(kind NodeKind, kinds []NodeKind) map[string]any {
	forbidden := make([]any, 0, len(kinds)-1)

	for _, other := range kinds {
		if other == kind {
			continue
		}

		forbidden = append(forbidden, map[string]any{keyRequired: []any{string(other)}})
	}

	return map[string]any{
		"if": map[string]any{
			keyRequired:   []any{keyKind},
			keyProperties: map[string]any{keyKind: constDef(string(kind))},
		},
		"then": map[string]any{
			keyRequired: []any{string(kind)},
			"not":       map[string]any{"anyOf": forbidden},
		},
	}
}

// constDef builds a schema matching exactly one value.
func constDef(value any) map[string]any {
	return map[string]any{"const": value}
}

func networkDef() map[string]any {
	return objectDef(
		"Canonical phenix network (VLAN).",
		[]string{keyID, keyName},
		map[string]any{
			keyID:   ref("identifier"),
			keyName: nameDef(""),
			"alias": map[string]any{
				keyType:        "integer",
				"minimum":      1,
				"maximum":      maxVLANAlias,
				keyDescription: "Optional integer VLAN alias published to an experiment.",
			},
			keyDescription: stringDef(""),
			keyColor:       stringDef(""),
		},
	)
}

func edgeDef() map[string]any {
	return objectDef(
		"Attaches a device interface handle to a switch hub. "+
			"Reference targets are validated by the server, not by this schema.",
		[]string{keyID, "sourceNodeId", "targetNodeId", keyNetworkID},
		map[string]any{
			keyID:            ref("identifier"),
			"sourceNodeId":   ref("identifier"),
			"sourceHandleId": refDef("identifier", "Handle of the source node, when it is a device."),
			"targetNodeId":   ref("identifier"),
			"targetHandleId": refDef("identifier", "Handle of the target node, when it is a device."),
			keyNetworkID:     ref("identifier"),
			"label":          stringDef(""),
		},
	)
}

func scenarioDef() map[string]any {
	def := objectDef(
		"Optional stored or uploaded scenario reference.",
		[]string{keyKind, keyAPIVersion, keyDigest},
		map[string]any{
			keyKind: enumDef("", []any{
				string(ScenarioRefStored),
				string(ScenarioRefUploaded),
			}),
			keyName:       stringDef(""),
			"content":     ref(PhenixV2DefPrefix + "Scenario"),
			keyAPIVersion: stringDef(""),
			keyDigest: digestDef(
				"Digest of the scenario content. Required on every scenario reference, " +
					"and must match the content when the content is present.",
			),
		},
	)

	def["allOf"] = []any{
		scenarioKindBranch(ScenarioRefStored, []string{keyName, keyAPIVersion, keyDigest}),
		scenarioKindBranch(ScenarioRefUploaded, []string{"content", keyAPIVersion, keyDigest}),
	}

	return def
}

// scenarioKindBranch requires additional fields for one scenario reference kind.
func scenarioKindBranch(kind ScenarioRefKind, required []string) map[string]any {
	return map[string]any{
		"if": map[string]any{
			keyRequired:   []any{keyKind},
			keyProperties: map[string]any{keyKind: constDef(string(kind))},
		},
		"then": map[string]any{keyRequired: anyStrings(required)},
	}
}

func sourceDef() map[string]any {
	return objectDef(
		"Document provenance and generation warnings.",
		[]string{keyKind},
		map[string]any{
			keyKind: enumDef("", []any{
				string(SourceKindManual),
				string(SourceKindTopology),
				string(SourceKindExperiment),
			}),
			keyName:       stringDef(""),
			keyAPIVersion: stringDef(""),
			"topology":    stringDef(""),
			"importedAt":  stringDef(""),
			keyDigest:     digestDef("Digest of the source config identity and spec."),
			"updatedAt":   stringDef("metadata.updated of the source config at import time."),
			"warnings":    arrayDef(stringDef("")),
		},
	)
}

// iconKeyEnum returns the icon key enum, including the empty default.
func iconKeyEnum() []any {
	enum := make([]any, 0, len(iconKeys)+1)
	enum = append(enum, "")

	for _, key := range iconKeys {
		enum = append(enum, key)
	}

	return enum
}

// withDescription annotates a schema fragment.
func withDescription(def map[string]any, description string) map[string]any {
	def[keyDescription] = description

	return def
}
