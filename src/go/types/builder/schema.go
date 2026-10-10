package builder

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"phenix/types/version"
)

const (
	// SchemaDialect is the JSON Schema dialect the generated bundle is written
	// against.
	SchemaDialect = "https://json-schema.org/draft/2020-12/schema"

	// PhenixDefPrefix namespaces the phenix v1 OpenAPI component schemas bundled
	// into the builder schema's $defs. Topology node and property forms resolve
	// against these definitions.
	PhenixDefPrefix = "phenix.v1."

	// openAPIRefPrefix is the reference prefix used by the phenix OpenAPI
	// documents, rewritten to local $defs references during bundling.
	openAPIRefPrefix = "#/components/schemas/"

	// localDefRef is the local reference prefix of the generated bundle.
	localDefRef = "#/$defs/"
)

// Schema returns the standalone Builder v1 JSON Schema as a freshly built map.
//
// The bundle is self contained: the phenix v1 OpenAPI component schemas are
// embedded under $defs (namespaced with [PhenixDefPrefix]) and every
// "#/components/schemas/..." reference is rewritten to a local "#/$defs/..."
// reference, so device specs and property forms resolve without a network
// fetch. It is suitable for a web schema endpoint and for JSON Forms.
//
// Object shapes that [Decode] rejects unknown fields for are marked
// "additionalProperties": false. Free-form phenix payloads (device specs)
// keep their own schemas.
//
// Referential integrity between edges, handles, nodes, and networks cannot be
// expressed in JSON Schema; it is structurally represented (typed identifier
// references and required fields) and enforced by [Document.Validate].
//
// Every definition and property the Builder owns has a title, a description
// and examples (see [documented]); the bundled phenix schemas keep their own
// documentation.
func Schema() (map[string]any, error) {
	defs, err := PhenixDefs()
	if err != nil {
		return nil, err
	}

	maps.Copy(defs, builderDefs())

	root := objectDef(
		[]string{
			schemaKey, revisionKey, keyMetadata, keyNodes, keyNetworks, keyEdges,
			keyViewport, keyGrid,
		},
		rootProperties(),
	)

	root[schemaKey] = SchemaDialect
	root["$id"] = SchemaURI
	root[keyTitle] = "phenix builder document"
	root[keyDescription] = "Library independent document model of the phenix topology builder."
	root["$defs"] = defs

	return root, nil
}

// rootProperties returns the schemas of the document's own fields.
func rootProperties() map[string]any {
	return map[string]any{
		schemaKey: documented(
			constDef(SchemaURI), "Schema URI",
			"Identifies the Builder document schema, which a document must name exactly.",
			[]any{SchemaURI},
		),
		revisionKey: documented(
			constDef(SchemaRevision), "Schema Revision",
			"Revision of the Builder document schema the document follows.",
			[]any{SchemaRevision},
		),
		keyMetadata: documented(
			ref(keyMetadata), "Metadata",
			"What the document says of itself: its identifier, name, description, provenance and notes.",
			[]any{exampleMetadata()},
		),
		keyNodes: documented(
			arrayDef(ref("node")), "Nodes",
			"Items on the canvas: devices, switches, notes and groups.",
			[]any{[]any{exampleDeviceNode(), exampleSwitchNode(), exampleNoteNode()}},
		),
		keyNetworks: documented(
			arrayDef(ref("network")), "Networks",
			"Networks (VLANs) the switches and connections of the document belong to.",
			[]any{[]any{exampleNetwork()}},
		),
		keyEdges: documented(
			arrayDef(ref("edge")), "Connections",
			"Connections, each joining one device interface to one switch.",
			[]any{[]any{exampleEdge()}},
		),
		keyViewport: documented(
			ref(keyViewport), "Viewport",
			"Pan and zoom of the canvas when the document was saved.",
			[]any{exampleViewport()},
		),
		keyGrid: documented(
			ref(keyGrid), "Grid",
			"Grid the canvas draws and snaps nodes to.",
			[]any{exampleGrid()},
		),
		keyScenarios: scenariosDef(),
		keySource: documented(
			ref(keySource), "Source",
			"Where the document came from, and the warnings raised while generating it.",
			[]any{exampleSource()},
		),
		keyLayout: documented(
			stringDef(), "Layout",
			"Automatic layout that last laid the document out, which the editor names in its layout menu; "+
				"empty, or an id the editor does not know, means a layout did not make the positions.",
			[]any{"elk"},
		),
		keyTemplates: templatesDef(),
		keyIcons:     iconsDef(),
	}
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
	doc, err := version.ReadSchemaFile("v1")
	if err != nil {
		return nil, err
	}

	return BundleOpenAPIDefs(doc, PhenixDefPrefix)
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
	keyMetadata    = "metadata"
	keyNodes       = "nodes"
	keyNetworks    = "networks"
	keyEdges       = "edges"
	keyGrid        = "grid"
	keyViewport    = "viewport"
	keyScenarios   = "scenarios"
	keySource      = "source"
	keyLayout      = "layout"
	keySpec        = "spec"
	keyAPIVersion  = "apiVersion"
	keyDigest      = "digest"
	keyType        = "type"
	keyProperties  = "properties"
	keyRequired    = "required"
	keyTitle       = "title"
	keyDescription = "description"
	keyExamples    = "examples"
	keyName        = "name"
	keyID          = "id"
	keyColor       = "color"
	keyPosition    = "position"
	keySize        = "size"
	keyNetworkID   = "networkId"
	keyCreatedBy   = "createdBy"
	keyCreatedAt   = "createdAt"
	keyUpdatedBy   = "updatedBy"
	keyUpdatedAt   = "updatedAt"
	keyNotes       = "notes"
	keyTemplates   = "templates"
	keyIcons       = "icons"

	keyIconKey      = "iconKey"
	keyIcon         = "icon"
	keyOutlineColor = "outlineColor"
	keyFillColor    = "fillColor"
	keyLineStyle    = "lineStyle"
	keyBorderStyle  = "borderStyle"

	// The definitions the presentation fields refer to.
	defHexColor   = "hexColor"
	defIconRef    = "iconRef"
	defIdentifier = "identifier"

	// digestPattern matches a digest (see [IsDigest]), which is also the
	// form of an icon id (see [IconID]).
	digestPattern = `sha256:[0-9a-f]{64}`

	// base64Pattern matches standard base64 with padding and no line
	// breaks, as an icon's data is written.
	base64Pattern = `^[A-Za-z0-9+/]+={0,2}$`

	// noControlPattern matches text without control characters, as
	// [Document.Validate] requires of the document name and of the users the
	// metadata names.
	noControlPattern = `^[^\x00-\x1f\x7f]*$`

	// notePattern matches a diagram note: no control characters but the
	// newline and the tab, and something besides white space. White space is
	// what [strings.TrimSpace] trims ([unicode.IsSpace]), as
	// [Document.Validate] finds a note blank: U+0009 to U+000D, U+0020,
	// U+0085 and the Unicode separators \p{Z} (U+00A0, U+1680, U+2000 to
	// U+200A, U+2028, U+2029, U+202F, U+205F and U+3000). It is spelled out
	// rather than written \s, which in a JSON Schema (ECMAScript) pattern
	// leaves out U+0085 and takes U+FEFF, and in Go's regexp is only
	// [\t\n\f\r ]. Go's regexp and ECMAScript's with the u flag JSON Schema
	// asks for read this one alike.
	notePattern = `^[^\x00-\x08\x0b-\x1f\x7f]*[^\x00-\x20\x7f\x85\p{Z}][^\x00-\x08\x0b-\x1f\x7f]*$`

	// configNamePattern matches a name phenix gives a config: at least one
	// letter, number, underscore, at sign, period or hyphen, and nothing else
	// (see phenix/api/config.NameRegex).
	configNamePattern = `^[A-Za-z0-9_@.-]+$`

	// timePattern matches a time in [TimeLayout].
	timePattern = `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`

	// uuidPattern matches the canonical RFC 4122 UUID text form with a known
	// version and the RFC 4122 variant, mirroring [IsUUID].
	uuidPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-` +
		`[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`
)

// objectDef builds an object schema that forbids unknown properties, matching
// the strict decoding performed by [Decode].
func objectDef(required []string, properties map[string]any) map[string]any {
	def := map[string]any{
		keyType:                "object",
		"additionalProperties": false,
		keyProperties:          properties,
	}

	if len(required) > 0 {
		def[keyRequired] = anyStrings(required)
	}

	return def
}

// stringDef builds a string schema.
func stringDef() map[string]any {
	return map[string]any{keyType: "string"}
}

// nameDef builds a schema for a non-empty, whitespace-free name.
func nameDef() map[string]any {
	def := stringDef()
	def["minLength"] = 1
	def["pattern"] = `^\S+$`

	return def
}

// documentNameDef builds the schema of the document name, bounded the way
// [Document.Validate] bounds it. maxLength counts characters, not bytes, so it
// is the looser of the two for names outside ASCII.
func documentNameDef() map[string]any {
	def := stringDef()
	def["maxLength"] = MaxNameBytes
	def["pattern"] = noControlPattern

	return def
}

// userDef builds the schema of a user the document metadata names, bounded
// the way [Document.Validate] bounds it. maxLength counts characters, as it
// does for the document name.
func userDef() map[string]any {
	def := stringDef()
	def["maxLength"] = MaxUserBytes
	def["pattern"] = noControlPattern

	return def
}

// timeDef builds the schema of a time of the document metadata: the one form
// of [TimeLayout]. The pattern is the rule; the format only names what the
// value is.
func timeDef() map[string]any {
	def := stringDef()
	def["format"] = "date-time"
	def["pattern"] = timePattern

	return def
}

// documented gives a schema what every Builder-owned definition and property
// of the bundle carries: a title (a short name in title case), a description
// (one plain sentence saying what it holds) and examples (values valid
// against the schema). Readers of the served schema and of the OpenAPI
// document rely on them; the Inspector sets its own field labels.
func documented(def map[string]any, title, description string, examples []any) map[string]any {
	def[keyTitle] = title
	def[keyDescription] = description
	def[keyExamples] = examples

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
func digestDef() map[string]any {
	def := stringDef()
	def["pattern"] = `^` + digestPattern + `$`

	return def
}

// arrayDef builds an array schema over the given item schema.
func arrayDef(items map[string]any) map[string]any {
	return map[string]any{keyType: "array", "items": items}
}

// enumDef builds a string schema restricted to the given values.
func enumDef(values []any) map[string]any {
	def := stringDef()
	def["enum"] = values

	return def
}

// refDef wraps a reference so that sibling keywords such as descriptions are
// honoured by every JSON Schema implementation.
func refDef(name string) map[string]any {
	return map[string]any{"allOf": []any{ref(name)}}
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
		defIdentifier: identifierDef(),
		keyIconKey: documented(
			enumDef(iconKeyEnum()), "Icon Key",
			"Built-in icon a node shows, from the Builder's icon registry; empty selects the default icon.",
			[]any{exampleIconKey},
		),
		defHexColor: hexColorDef(),
		keyLineStyle: documented(
			enumDef(styleEnum(lineStyles)), "Line Style",
			"Dash pattern of a connection line; empty selects the automatic pattern.",
			[]any{"dashed"},
		),
		keyBorderStyle: documented(
			enumDef(styleEnum(borderStyles)), "Border Style",
			"Border pattern of a group; empty selects the default, dashed.",
			[]any{"double"},
		),
		defIconRef:        iconRefDef(),
		keyIcon:           iconDef(),
		"template":        templateDef(),
		"templateDevice":  templateDeviceDef(),
		keyMetadata:       metadataDef(),
		keyPosition:       positionDef(),
		keySize:           sizeDef(),
		keyViewport:       viewportDef(),
		keyGrid:           gridDef(),
		"interfaceHandle": interfaceHandleDef(),
		"node":            nodeDef(),
		"network":         networkDef(),
		"edge":            edgeDef(),
		keySource:         sourceDef(),
	}

	defs[string(NodeKindDevice)] = deviceDef()
	defs[string(NodeKindSwitch)] = switchDef()
	defs[string(NodeKindNote)] = noteDef()
	defs[string(NodeKindGroup)] = groupDef()

	return defs
}

func identifierDef() map[string]any {
	def := stringDef()
	def["format"] = "uuid"
	def["pattern"] = uuidPattern

	return documented(
		def, "Identifier",
		"Stable RFC 4122 UUID, found duplicate ignoring case but repeated exactly by every reference; "+
			"the front end mints random (version 4) ones.",
		[]any{exampleDeviceID},
	)
}

// documentedRef refers to a definition and documents what it is used for at
// the property that refers to it. JSON Schema 2020-12 applies the keywords
// beside a $ref, and title, description and examples are annotations.
func documentedRef(name, title, description string, examples []any) map[string]any {
	return documented(ref(name), title, description, examples)
}

// notesDef builds the schema of a list of notes, of the diagram or of a
// switch, bounded the way [Document.Validate] bounds them. maxLength counts
// characters, as it does for the document name.
func notesDef() map[string]any {
	note := stringDef()
	note["minLength"] = 1
	note["maxLength"] = MaxDiagramNoteBytes
	note["pattern"] = notePattern

	notes := arrayDef(note)
	notes["maxItems"] = MaxDiagramNotes

	return notes
}

// metadataDef builds the schema of the document metadata, bounded the way
// [Document.Validate] bounds it.
func metadataDef() map[string]any {
	notes := notesDef()

	return documented(
		objectDef([]string{keyID}, map[string]any{
			keyID: documentedRef(
				defIdentifier, "Document ID", "Identifier of the document.", []any{exampleDocumentID},
			),
			keyName: documented(
				documentNameDef(), "Name",
				fmt.Sprintf(
					"Name of the diagram, which a draft takes as its title: at most %d bytes, without control characters.",
					MaxNameBytes,
				),
				[]any{"Pump station"},
			),
			keyDescription: documented(
				stringDef(), "Description", "Free text describing the diagram.",
				[]any{"Water treatment pump station with two PLCs and an HMI."},
			),
			keyCreatedBy: documented(
				userDef(), "Created By",
				"User who first made the document, which the server sets when a draft is created from a document "+
					"that names none and keeps in every later save of that draft.",
				[]any{exampleCreatedBy},
			),
			keyCreatedAt: documented(
				timeDef(), "Created At",
				"When the document was first made, as RFC 3339 in UTC with whole seconds (YYYY-MM-DDTHH:MM:SSZ), "+
					"which the server sets and keeps the way it does createdBy.",
				[]any{exampleCreatedAt},
			),
			keyUpdatedBy: documented(
				userDef(), "Updated By",
				"User whose save stored this content, which the server sets on every save in place of any value sent.",
				[]any{exampleUpdatedBy},
			),
			keyUpdatedAt: documented(
				timeDef(), "Updated At",
				"When this content was saved, as RFC 3339 in UTC with whole seconds, which the server sets on every save; "+
					"it is not source.updatedAt, a time of the source config.",
				[]any{exampleUpdatedAt},
			),
			keyNotes: documented(
				notes, "Notes",
				fmt.Sprintf(
					"Free text notes about the diagram as a whole: at most %d, each not only white space, "+
						"at most %d bytes in UTF-8 (maxLength counts characters, so the byte limit is the stricter one "+
						"outside ASCII), and without control characters but newlines and tabs.",
					MaxDiagramNotes, MaxDiagramNoteBytes,
				),
				[]any{exampleNotes()},
			),
		}),
		"Document Metadata",
		"Identifier, name, description, provenance and notes of a document; none of it is published to a config.",
		[]any{exampleMetadata()},
	)
}

func positionDef() map[string]any {
	return documented(
		objectDef([]string{"x", "y"}, map[string]any{
			"x": documented(numberDef(), "X", "Horizontal canvas coordinate, in pixels.", []any{exampleX}),
			"y": documented(numberDef(), "Y", "Vertical canvas coordinate, in pixels.", []any{exampleY}),
		}),
		"Position", "Point on the canvas, in canvas pixels.",
		[]any{examplePosition(exampleX, exampleY)},
	)
}

func sizeDef() map[string]any {
	return documented(
		objectDef([]string{"width", "height"}, map[string]any{
			"width": documented(
				positiveNumberDef(), "Width", "Width in canvas pixels, greater than zero.", []any{exampleWidth},
			),
			"height": documented(
				positiveNumberDef(), "Height", "Height in canvas pixels, greater than zero.", []any{exampleHeight},
			),
		}),
		"Size", "Explicit width and height of a node on the canvas.",
		[]any{exampleSize()},
	)
}

func viewportDef() map[string]any {
	return documented(
		objectDef([]string{"x", "y", "zoom"}, map[string]any{
			"x": documented(numberDef(), "Pan X", "Horizontal pan of the canvas, in screen pixels.", []any{0}),
			"y": documented(numberDef(), "Pan Y", "Vertical pan of the canvas, in screen pixels.", []any{0}),
			"zoom": documented(
				positiveNumberDef(), "Zoom", "Zoom factor of the canvas, greater than zero, where 1 is 100 percent.",
				[]any{1},
			),
		}),
		"Viewport", "Pan and zoom of the canvas.",
		[]any{exampleViewport()},
	)
}

func gridDef() map[string]any {
	return documented(
		objectDef([]string{"enabled", keySize, "snap"}, map[string]any{
			"enabled": documented(boolDef(), "Grid Shown", "Whether the canvas draws the grid.", []any{true}),
			keySize: documented(
				positiveNumberDef(), "Grid Size", "Spacing of the grid in canvas pixels, greater than zero.",
				[]any{defaultGridSize},
			),
			"snap": documented(boolDef(), "Snap to Grid", "Whether moved nodes snap to the grid.", []any{true}),
		}),
		"Grid", "Grid the canvas draws and snaps nodes to.",
		[]any{exampleGrid()},
	)
}

func interfaceHandleDef() map[string]any {
	index := map[string]any{keyType: "integer", "minimum": 0}

	return documented(
		objectDef([]string{keyID, keyName, "index"}, map[string]any{
			keyID: documentedRef(
				defIdentifier, "Handle ID", "Identifier of the connection handle, which connections name.",
				[]any{exampleHandleID},
			),
			keyName: documented(
				nameDef(), "Interface Name", "Name of the interface in the device spec, such as eth0.",
				[]any{exampleInterface},
			),
			"index": documented(
				index, "Interface Index",
				"Place of the interface in the spec's interface list when the handle was made, for ordering only.",
				[]any{0},
			),
		}),
		"Interface Handle", "Stable mapping of a canvas connection handle onto a named interface of the device spec.",
		[]any{exampleHandle()},
	)
}

// nodeSpecDef builds the schema of a complete phenix topology node spec, as
// a device and a template hold one.
func nodeSpecDef() map[string]any {
	return documented(
		map[string]any{
			"oneOf": []any{
				ref(PhenixDefPrefix + "minimega_node"),
				ref(PhenixDefPrefix + "external_node"),
			},
		},
		"Node Spec",
		"Complete phenix topology node spec, in the stored snake_case form, which publishing writes to the topology.",
		[]any{exampleSpec()},
	)
}

func deviceDef() map[string]any {
	return documented(
		objectDef([]string{"hostname", keySpec, "interfaces"}, map[string]any{
			"hostname": documented(
				nameDef(), "Hostname",
				"Hostname of the device, unique in the document ignoring case and without whitespace.",
				[]any{exampleHostname},
			),
			keyIconKey: documentedRef(
				keyIconKey, "Icon", "Built-in icon the canvas draws for the device.", []any{exampleIconKey},
			),
			keyIcon: documentedRef(
				defIconRef, "Custom Icon", "Custom icon drawn in place of the built-in one; empty for none.",
				[]any{exampleIconID},
			),
			keyOutlineColor: documentedRef(
				defHexColor, "Outline Color", "Border color of the device on the canvas.", []any{exampleOutlineColor},
			),
			keyFillColor: documentedRef(
				defHexColor, "Fill Color", "Background color of the device on the canvas.", []any{exampleFillColor},
			),
			keySpec: nodeSpecDef(),
			"interfaces": documented(
				arrayDef(ref("interfaceHandle")), "Interface Handles",
				"Connection handles of the device, one for each interface a connection can attach to.",
				[]any{[]any{exampleHandle()}},
			),
			"includedFrom": documented(
				nameDef(), "Included From",
				"Topology that defines the device when it came from an included topology, "+
					"which keeps it read only and out of what publishing writes.",
				[]any{exampleInclude},
			),
		}),
		"Device", "Payload of a device node: a phenix topology node and how the canvas draws it.",
		[]any{exampleDevice()},
	)
}

func switchDef() map[string]any {
	return documented(
		objectDef([]string{keyNetworkID}, map[string]any{
			keyNetworkID: documentedRef(
				defIdentifier, "Network ID", "Identifier of the network the switch stands for.",
				[]any{exampleNetworkID},
			),
			keyOutlineColor: documentedRef(
				defHexColor, "Outline Color", "Border color of the switch on the canvas.", []any{exampleOutlineColor},
			),
			keyFillColor: documentedRef(
				defHexColor, "Fill Color", "Background color of the switch on the canvas.", []any{exampleFillColor},
			),
			keyNotes: documented(
				notesDef(), "Notes",
				fmt.Sprintf(
					"Free text notes the canvas shows below the switch, never published: at most %d, each not only white "+
						"space, at most %d bytes in UTF-8 (maxLength counts characters, so the byte limit is the stricter one "+
						"outside ASCII), and without control characters but newlines and tabs.",
					MaxDiagramNotes, MaxDiagramNoteBytes,
				),
				[]any{exampleSwitchNotes()},
			),
		}),
		"Switch", "Payload of a switch node: a visual hub bound to exactly one network.",
		[]any{exampleSwitch()},
	)
}

func noteDef() map[string]any {
	return documented(
		objectDef([]string{"text"}, map[string]any{
			"text": documented(stringDef(), "Text", "Text of the note.", []any{exampleNoteText}),
			keyColor: documented(
				stringDef(), "Color", "Background color of the note, as CSS color text.", []any{"#ffd"},
			),
		}),
		"Note", "Payload of a note node: free text on the canvas, with no phenix meaning.",
		[]any{exampleNote()},
	)
}

func groupDef() map[string]any {
	return documented(
		objectDef(nil, map[string]any{
			"title": documented(
				stringDef(), "Title", "Title shown at the top of the group.", []any{exampleGroupTitle},
			),
			keyDescription: documented(
				stringDef(), "Description", "Free text shown under the group's title.",
				[]any{"Two PLCs and the HMI that controls them."},
			),
			keyColor: documented(
				stringDef(), "Color", "Background color of the group, as CSS color text.", []any{"#eef"},
			),
			keyBorderStyle: documentedRef(
				keyBorderStyle, "Border Pattern", "Pattern of the group's border.", []any{"double"},
			),
			keyIconKey: documentedRef(
				keyIconKey, "Icon", "Built-in icon beside the group's title.", []any{"server"},
			),
			keyIcon: documentedRef(
				defIconRef, "Custom Icon", "Custom icon drawn in place of the built-in one; empty for none.",
				[]any{exampleIconID},
			),
			"collapsed": documented(boolDef(), "Collapsed", "Whether the group is collapsed.", []any{false}),
		}),
		"Group", "Payload of a group node: a visual container other nodes may belong to, with no phenix meaning.",
		[]any{exampleGroup()},
	)
}

// hexColorDef builds the schema of an outline or fill color. The empty
// string is in the pattern, as it is in the iconKey enum, because the editor
// may send it for none.
func hexColorDef() map[string]any {
	def := stringDef()
	def["pattern"] = `^(` + hexColorPattern + `)?$`

	return documented(def, "Hex Color", "Opaque color as #rrggbb; empty for none.", []any{exampleOutlineColor})
}

// styleEnum returns the enum of a line or border style, including the empty
// default.
func styleEnum(styles []string) []any {
	return append([]any{""}, anyStrings(styles)...)
}

// iconRefDef builds the schema of the custom icon a device, a group or a
// template names. That the document carries it is checked by
// [Document.Validate], not by this schema.
func iconRefDef() map[string]any {
	def := stringDef()
	def["pattern"] = `^(` + digestPattern + `)?$`

	return documented(
		def, "Custom Icon Reference", "Custom icon of the document's icons, by icon id; empty for none.",
		[]any{exampleIconID},
	)
}

// iconDef builds the schema of a custom icon. maxLength counts the base64
// text of the largest PNG an icon may be.
func iconDef() map[string]any {
	name := stringDef()
	name["maxLength"] = MaxIconNameBytes
	name["pattern"] = noControlPattern

	data := stringDef()
	data["maxLength"] = base64.StdEncoding.EncodedLen(MaxIconBytes)
	data["pattern"] = base64Pattern
	data["contentEncoding"] = "base64"
	data["contentMediaType"] = "image/png"

	return documented(
		objectDef(
			[]string{"data"},
			map[string]any{
				keyName: documented(
					name, "Icon Name",
					fmt.Sprintf("Name the icon is listed under, at most %d bytes without control characters.", MaxIconNameBytes),
					[]any{exampleIconName},
				),
				"data": documented(
					data, "Icon Data", "The PNG bytes of the icon, in standard base64 with padding and no line breaks.",
					[]any{exampleIconData},
				),
			},
		),
		"Custom Icon",
		fmt.Sprintf(
			"Custom icon: a PNG of at most %d by %d pixels and %d bytes.",
			MaxIconPixels, MaxIconPixels, MaxIconBytes,
		),
		[]any{exampleIcon()},
	)
}

// iconsDef builds the schema of the document's custom icons, bounded the way
// [ValidateIcons] bounds them. That a key is the digest of its icon's bytes,
// and that the bytes are an accepted PNG, JSON Schema cannot express.
func iconsDef() map[string]any {
	return documented(
		map[string]any{
			keyType:                "object",
			"maxProperties":        MaxDocumentIcons,
			"propertyNames":        map[string]any{"pattern": `^` + digestPattern + `$`},
			"additionalProperties": ref(keyIcon),
		},
		"Custom Icons",
		fmt.Sprintf(
			"Custom icons the nodes and templates use, at most %d, each by its icon id (sha256: and the SHA-256 "+
				"of its PNG bytes); never published.",
			MaxDocumentIcons,
		),
		[]any{map[string]any{exampleIconID: exampleIcon()}},
	)
}

// templatesDef builds the schema of the document's templates.
func templatesDef() map[string]any {
	def := arrayDef(ref("template"))
	def["maxItems"] = MaxTemplates

	return documented(
		def, "Templates",
		fmt.Sprintf("Device templates saved with the diagram, at most %d; never published to a config.", MaxTemplates),
		[]any{[]any{exampleTemplate()}},
	)
}

// templateDef builds the schema of a device template, bounded the way
// [Template.Issues] bounds one. maxLength counts characters, as it does for
// the document name.
func templateDef() map[string]any {
	name := stringDef()
	name["minLength"] = 1
	name["maxLength"] = MaxTemplateNameBytes
	name["pattern"] = noControlPattern

	description := stringDef()
	description["maxLength"] = MaxTemplateDescriptionBytes
	description["pattern"] = noControlPattern

	return documented(
		objectDef(
			[]string{keyID, keyName, "device"},
			map[string]any{
				keyID: documentedRef(
					defIdentifier, "Template ID", "Identifier of the template, unique among the document's templates.",
					[]any{exampleTemplateID},
				),
				keyName: documented(
					name, "Template Name",
					fmt.Sprintf("Name the template is offered under, 1 to %d bytes on one line.", MaxTemplateNameBytes),
					[]any{"Edge router"},
				),
				keyDescription: documented(
					description, "Template Description",
					fmt.Sprintf(
						"Text shown with the template, at most %d bytes on one line, which is never written into a node.",
						MaxTemplateDescriptionBytes,
					),
					[]any{"VyOS router with one static interface"},
				),
				"device": documentedRef(
					"templateDevice", "Template Device", "Fields the template fills in on a device made from it.",
					[]any{exampleTemplateDevice()},
				),
			},
		),
		"Template", "Named set of prefilled fields for a device node.",
		[]any{exampleTemplate()},
	)
}

// templateDeviceDef builds the schema of what a template fills in: the
// fields of a device, but its hostname, its interface handles and where it
// was included from.
func templateDeviceDef() map[string]any {
	return documented(
		objectDef(
			[]string{keySpec},
			map[string]any{
				keyIconKey: documentedRef(
					keyIconKey, "Icon", "Built-in icon of the devices made from the template.", []any{exampleIconKey},
				),
				keyIcon: documentedRef(
					defIconRef, "Custom Icon", "Custom icon of the devices made from the template; empty for none.",
					[]any{exampleIconID},
				),
				keyOutlineColor: documentedRef(
					defHexColor, "Outline Color", "Border color of the devices made from the template.",
					[]any{exampleOutlineColor},
				),
				keyFillColor: documentedRef(
					defHexColor, "Fill Color", "Background color of the devices made from the template.",
					[]any{exampleFillColor},
				),
				keySpec: nodeSpecDef(),
			},
		),
		"Template Device",
		fmt.Sprintf("Fields a template fills in, at most %d bytes as JSON.", MaxTemplateDeviceBytes),
		[]any{exampleTemplateDevice()},
	)
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
		[]string{keyID, keyKind, keyPosition},
		map[string]any{
			keyID: documentedRef(
				defIdentifier, "Node ID", "Identifier of the node, unique among the document's nodes.",
				[]any{exampleDeviceID},
			),
			keyKind: documented(
				enumDef(enum), "Node Kind", "What the node is, which names the payload it carries.",
				[]any{string(NodeKindDevice)},
			),
			"label": documented(stringDef(), "Label", "Text shown on the node.", []any{exampleHostname}),
			keyPosition: documentedRef(
				keyPosition, "Position", "Where the node is on the canvas.", []any{examplePosition(exampleX, exampleY)},
			),
			keySize: documentedRef(
				keySize, "Size", "Explicit size of the node; without one the editor sizes the node itself.",
				[]any{exampleSize()},
			),
			"parentId": documented(
				refDef(defIdentifier), "Parent Group", "Identifier of the group node this node belongs to.",
				[]any{exampleGroupID},
			),

			string(NodeKindDevice): documentedRef(
				string(NodeKindDevice), "Device", "Payload of a device node.", []any{exampleDevice()},
			),
			string(NodeKindSwitch): documentedRef(
				string(NodeKindSwitch), "Switch", "Payload of a switch node.", []any{exampleSwitch()},
			),
			string(NodeKindNote): documentedRef(
				string(NodeKindNote), "Note", "Payload of a note node.", []any{exampleNote()},
			),
			string(NodeKindGroup): documentedRef(
				string(NodeKindGroup), "Group", "Payload of a group node.", []any{exampleGroup()},
			),
		},
	)

	def["allOf"] = discriminated

	return documented(
		def, "Node", "Item on the canvas, whose payload key matches its kind.",
		[]any{exampleDeviceNode(), exampleNoteNode()},
	)
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
	alias := map[string]any{keyType: "integer", "minimum": 1, "maximum": maxVLANAlias}

	return documented(
		objectDef(
			[]string{keyID, keyName},
			map[string]any{
				keyID: documentedRef(
					defIdentifier, "Network ID", "Identifier of the network.", []any{exampleNetworkID},
				),
				keyName: documented(
					nameDef(), "Name",
					"VLAN name, unique in the document and without whitespace, which device interfaces name as their vlan.",
					[]any{exampleNetworkName},
				),
				"alias": documented(
					alias, "VLAN Alias",
					fmt.Sprintf("Optional VLAN number published to an experiment's vlans.aliases, from 1 to %d.", maxVLANAlias),
					[]any{exampleAlias},
				),
				keyDescription: documented(
					stringDef(), "Description", "Free text describing the network.", []any{"Experiment network"},
				),
				keyColor: documented(
					stringDef(), "Color", "Color of the network's connection lines, as CSS color text.",
					[]any{exampleOutlineColor},
				),
				keyLineStyle: documentedRef(
					keyLineStyle, "Line Style", "Dash pattern of the network's connection lines.", []any{"dashed"},
				),
			},
		),
		"Network", "Canonical phenix network (VLAN).",
		[]any{exampleNetwork()},
	)
}

func edgeDef() map[string]any {
	return documented(
		objectDef(
			[]string{keyID, "sourceNodeId", "targetNodeId", keyNetworkID},
			map[string]any{
				keyID: documentedRef(
					defIdentifier, "Connection ID", "Identifier of the connection.", []any{exampleEdgeID},
				),
				"sourceNodeId": documentedRef(
					defIdentifier, "Source Node", "Identifier of the node the connection starts at.",
					[]any{exampleDeviceID},
				),
				"sourceHandleId": documented(
					refDef(defIdentifier), "Source Handle", "Interface handle of the source node, when it is a device.",
					[]any{exampleHandleID},
				),
				"targetNodeId": documentedRef(
					defIdentifier, "Target Node", "Identifier of the node the connection ends at.",
					[]any{exampleSwitchID},
				),
				"targetHandleId": documented(
					refDef(defIdentifier), "Target Handle", "Interface handle of the target node, when it is a device.",
					[]any{exampleHandleID},
				),
				keyNetworkID: documentedRef(
					defIdentifier, "Network ID", "Identifier of the network of the switch the connection joins.",
					[]any{exampleNetworkID},
				),
				"label": documented(stringDef(), "Label", "Text shown on the connection.", []any{"uplink"}),
				keyColor: documented(
					stringDef(), "Color", "Line color drawn in place of the network's, as CSS color text.",
					[]any{"#c0392b"},
				),
				keyLineStyle: documented(
					refDef(keyLineStyle), "Line Style", "Dash pattern drawn in place of the network's.",
					[]any{"dotted"},
				),
				"route": routeDef(),
			},
		),
		"Connection",
		"Attaches a device interface handle to a switch, and so to its network; the server checks the nodes, "+
			"handles and network it names, which this schema cannot.",
		[]any{exampleEdge()},
	)
}

// routeDef builds the schema of an edge route: the points an automatic layout
// drew the edge through, from the source handle to the target handle.
func routeDef() map[string]any {
	def := arrayDef(ref(keyPosition))
	def["minItems"] = minRoutePoints

	return documented(
		def, "Route",
		"Path an automatic layout drew, in absolute canvas coordinates from the source handle to the target handle, "+
			"which is never published.",
		[]any{exampleRoute()},
	)
}

// scenariosDef builds the schema of the Scenario configs a document names,
// bounded the way [Document.Validate] bounds them. uniqueItems compares the
// names exactly; that no two differ only by case, JSON Schema cannot
// express. maxLength counts characters, which a config name holds only one
// byte each of.
func scenariosDef() map[string]any {
	name := stringDef()
	name["minLength"] = 1
	name["maxLength"] = MaxScenarioNameBytes
	name["pattern"] = configNamePattern

	def := arrayDef(name)
	def["maxItems"] = MaxScenarios
	def["uniqueItems"] = true

	return documented(
		def, "Scenarios",
		fmt.Sprintf(
			"Names of the Scenario configs on the server the diagram is used with, at most %d, each a config name "+
				"of at most %d bytes and none twice ignoring case; publishing adds the topology to the topology "+
				"annotation of each, and an experiment published with it uses one of them.",
			MaxScenarios, MaxScenarioNameBytes,
		),
		[]any{[]any{exampleScenarioName, exampleScenarioName + "-attack"}},
	)
}

func sourceDef() map[string]any {
	return documented(
		objectDef(
			[]string{keyKind},
			map[string]any{
				keyKind: documented(
					enumDef([]any{
						string(SourceKindManual),
						string(SourceKindTopology),
						string(SourceKindExperiment),
					}),
					"Kind", "What the document was made from: manual for one drawn in the editor, a topology or an experiment.",
					[]any{string(SourceKindTopology)},
				),
				keyName: documented(stringDef(), "Name", "Name of the source config.", []any{exampleTopology}),
				keyAPIVersion: documented(
					stringDef(), "API Version", "apiVersion of the source config.", []any{exampleAPIVersion},
				),
				"topology": documented(
					stringDef(), "Topology", "Topology an imported experiment was built from, when known.",
					[]any{exampleTopology},
				),
				"importedAt": documented(
					stringDef(), "Imported At", "When the document was generated from the source config, as an RFC 3339 time.",
					[]any{exampleCreatedAt},
				),
				keyDigest: documented(
					digestDef(), "Digest",
					"Digest of the source config's identity and spec, which publishing compares with the stored config.",
					[]any{exampleDigest},
				),
				keyUpdatedAt: documented(
					stringDef(), "Source Updated At", "metadata.updated of the source config at import time.",
					[]any{exampleSourceUpdated},
				),
				"includeTopologies": documented(
					arrayDef(nameDef()), "Included Topologies",
					"includeTopologies of the source topology, which publishing writes back in place of their nodes.",
					[]any{[]any{exampleInclude}},
				),
				"unresolvedIncludes": documented(
					arrayDef(nameDef()), "Unresolved Includes",
					"Included topologies, at any depth, whose nodes are not in the document because they could not be read.",
					[]any{[]any{exampleInclude}},
				),
				"annotations": annotationsDef(),
				"warnings": documented(
					arrayDef(stringDef()), "Warnings", "Warnings raised while the document was generated.",
					[]any{[]any{exampleWarning()}},
				),
			},
		),
		"Source", "Where the document came from, and the warnings raised while generating it.",
		[]any{exampleSource()},
	)
}

// annotationsDef builds the schema of the source annotations, bounded the way
// [Document.Validate] bounds them, but for their total size, which JSON
// Schema cannot express.
func annotationsDef() map[string]any {
	key := stringDef()
	key["minLength"] = 1
	key["maxLength"] = MaxNameBytes
	key["pattern"] = `^[^\x00-\x1f\x7f]*\S[^\x00-\x1f\x7f]*$`

	return documented(
		map[string]any{
			keyType:                "object",
			"maxProperties":        MaxAnnotations,
			"propertyNames":        key,
			"additionalProperties": stringDef(),
		},
		"Annotations",
		fmt.Sprintf(
			"metadata.annotations of the source config at import time without the Builder's own (builder-*), "+
				"shown only and never published: at most %d, and %d KiB of keys and values in all.",
			MaxAnnotations, maxAnnotationKiB,
		),
		[]any{map[string]any{"topology": exampleTopology, "scenario": exampleScenarioName}},
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
