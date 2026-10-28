package builder

import (
	"encoding/json"
	"fmt"
)

// defLayerSetSource is the definition of the layer set a document's layers
// are linked to.
const defLayerSetSource = "layerSetSource"

// The examples of the layer set schemas.
const (
	exampleLayerColor    = "#15803d"
	exampleLayerSetID    = "6f1c2a8e-4b7d-4e2a-9c3f-1d2e3f4a5b6c"
	exampleLayerSetName  = "Plant levels"
	exampleMatcherField  = "general.hostname"
	exampleMatcherRegexp = "^(hmi|scada)-"

	exampleLayerSetVersion = 3
)

// layerMatchersDef builds the schema of the node matchers of a layer,
// bounded the way [Document.Validate] bounds them. maxLength counts
// characters, as it does for the document name. JSON Schema cannot express
// that a pattern compiles.
func layerMatchersDef() map[string]any {
	field := stringDef()
	field["maxLength"] = MaxMatcherPathBytes
	field["pattern"] = matcherPathPattern

	pattern := stringDef()
	pattern["minLength"] = 1
	pattern["maxLength"] = MaxMatcherPatternBytes

	return documented(
		map[string]any{
			keyType:                "object",
			"maxProperties":        MaxLayerMatchers,
			"propertyNames":        field,
			"additionalProperties": pattern,
		},
		"Node Matchers",
		fmt.Sprintf(
			"At most %d node matchers. If a device has no chosen layer, the editor puts it in the first layer with "+
				"a matcher that matches it. The match conflict rule decides which layer is first. Each key is a path of "+
				"keys joined by dots into the spec of the device, such as general.hostname. A key of digits selects an "+
				"item of a list. A path "+
				"to a list matches when an item matches. Each value is a regular expression of 1 to %d bytes, which "+
				"can match any part of the value. Numbers and booleans match as text. Objects and missing fields "+
				"never match.",
			MaxLayerMatchers, MaxMatcherPatternBytes,
		),
		[]any{exampleMatchers()},
	)
}

// matchConflictDef builds the schema of the rule that decides the layer of
// a device that the matchers of more than one layer match.
func matchConflictDef() map[string]any {
	return documented(
		enumDef(styleEnum(matchConflicts)), "Match Conflict Rule",
		"Layer that a device gets when the node matchers of more than one layer match it. The rule picks the "+
			"layer of the lowest ID or of the highest ID. Empty is lowest.",
		[]any{MatchConflictLowest},
	)
}

// layerSetNameDef builds the schema of the name of a layer set, bounded the
// way [LayerSet.Issues] bounds it. minLength is 1 for a set, and 0 for the
// options of a document, whose layers may have come from no set.
func layerSetNameDef(minLength int) map[string]any {
	name := stringDef()
	name["minLength"] = minLength
	name["maxLength"] = MaxLayerSetNameBytes
	name["pattern"] = noControlPattern

	return name
}

// documentLayerSetDef builds the schema of the layer set options of a
// document (see [DocumentLayerSet]).
func documentLayerSetDef() map[string]any {
	return documented(
		objectDef(nil, map[string]any{
			keyName: documented(
				layerSetNameDef(0), "Layer Set Name",
				fmt.Sprintf(
					"Name of the layer set that the layers came from, at most %d bytes on one line. Empty for "+
						"layers that no layer set gave.",
					MaxLayerSetNameBytes,
				),
				[]any{exampleLayerSetName},
			),
			keyMatchConflict: documentedRef(
				keyMatchConflict, "Match Conflict Rule",
				"Layer that a device gets when the node matchers of more than one layer match it.",
				[]any{MatchConflictHighest},
			),
			keyLayerOutlines: documented(
				boolDef(), "Layer Outlines",
				"When true, the editor draws the outline of a node in the color of its layer. It does not do this when "+
					"the node has an outline color of its own or the layer has no color.",
				[]any{true},
			),
			keySource: documentedRef(
				defLayerSetSource, "Linked Layer Set",
				"Layer set of the server, and the version of it, that the editor last copied the layers from or saved "+
					"them to. The editor offers an update when the layer set has a later version.",
				[]any{exampleLayerSetSource()},
			),
		}),
		"Layer Set Options",
		"Options of the layer set that the diagram copied its layers from. The diagram keeps a full copy of "+
			"the layers, and a change to them changes only that copy.",
		[]any{exampleDocumentLayerSet()},
	)
}

// layerSetSourceDef builds the schema of the layer set a document's layers
// are linked to (see [LayerSetSource]).
func layerSetSourceDef() map[string]any {
	id := stringDef()
	id["minLength"] = 1
	id["maxLength"] = MaxLayerSetIDBytes
	id["pattern"] = layerSetIDPattern

	return documented(
		objectDef([]string{keyID, keyVersion}, map[string]any{
			keyID: documented(
				id, "Layer Set ID",
				fmt.Sprintf(
					`ID of a layer set of the server: 1 to %d letters, digits, ".", "_", ":" or "-". The ID of the `+
						"built-in Purdue Model set is %s.",
					MaxLayerSetIDBytes, BuiltinPurdueLayerSetID,
				),
				[]any{exampleLayerSetID},
			),
			keyVersion: documented(
				map[string]any{keyType: "integer", "minimum": 1}, "Layer Set Version",
				"Version of the layer set that the editor last copied the layers from or saved them to.",
				[]any{exampleLayerSetVersion},
			),
		}),
		"Linked Layer Set", "Layer set of the server and one version of it.",
		[]any{exampleLayerSetSource()},
	)
}

// exampleMatchers returns node matchers that put the HMI and SCADA devices
// in a layer.
func exampleMatchers() map[string]any {
	return map[string]any{exampleMatcherField: exampleMatcherRegexp, "hardware.os_type": "^windows$"}
}

// exampleMatchedLayer returns a layer with an icon, a color and node
// matchers.
func exampleMatchedLayer() map[string]any {
	layer := exampleLayer(exampleLayerID, exampleLayerLabel)
	layer[keyIconKey] = "desktop"
	layer[keyColor] = exampleLayerColor
	layer[keyMatchers] = exampleMatchers()

	return layer
}

// exampleLayerSetSource returns the link of a document to version 3 of a
// layer set.
func exampleLayerSetSource() map[string]any {
	return map[string]any{keyID: exampleLayerSetID, keyVersion: exampleLayerSetVersion}
}

// exampleDocumentLayerSet returns the layer set options of a document whose
// layers are linked to a layer set.
func exampleDocumentLayerSet() map[string]any {
	return map[string]any{
		keyName:          exampleLayerSetName,
		keyMatchConflict: MatchConflictLowest,
		keyLayerOutlines: true,
		keySource:        exampleLayerSetSource(),
	}
}

// LayerSetFileSchema returns the JSON Schema of a layer set file (see
// [LayerSetFileSchemaURI]) as a freshly built map. Like [TemplateFileSchema], it is
// self contained: it holds every Builder definition that the parts of the
// file refer to. The editor checks what JSON Schema cannot express: layer IDs
// that repeat, patterns that do not compile, and that an icon is an accepted PNG.
func LayerSetFileSchema() (map[string]any, error) {
	defs := map[string]any{}

	root := objectDef([]string{schemaKey, keyName, keyLayers}, layerSetFileProperties())

	if err := addReferencedDefs(defs, builderDefs(), root); err != nil {
		return nil, fmt.Errorf("building the layer set file schema: %w", err)
	}

	root[schemaKey] = SchemaDialect
	root["$id"] = LayerSetFileSchemaURI
	root[keyTitle] = "phenix Builder layer set file"
	root[keyDescription] = "One Builder layer set: its layers and their options. The Builder downloads and uploads it."
	root["$defs"] = defs

	return root, nil
}

// LayerSetFileSchemaJSON returns [LayerSetFileSchema] as deterministic,
// indented JSON.
func LayerSetFileSchemaJSON() ([]byte, error) {
	schema, err := LayerSetFileSchema()
	if err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling the layer set file schema: %w", err)
	}

	return data, nil
}

// layerSetFileProperties returns the schemas of the fields of a layer set
// file.
func layerSetFileProperties() map[string]any {
	description := stringDef()
	description["maxLength"] = MaxLayerSetDescriptionBytes
	description["pattern"] = noControlPattern

	layers := arrayDef(ref(keyLayer))
	layers["minItems"] = 1
	layers["maxItems"] = MaxLayers

	icons := map[string]any{
		keyType:                "object",
		"maxProperties":        MaxDocumentIcons,
		"propertyNames":        map[string]any{"pattern": `^` + iconNameSchemaPattern() + `$`},
		"additionalProperties": ref(keyIcon),
	}

	return map[string]any{
		schemaKey: documented(
			constDef(LayerSetFileSchemaURI), "Schema URI",
			"Identifies the layer set file format. A layer set file must name it exactly.",
			[]any{LayerSetFileSchemaURI},
		),
		keyName: documented(
			layerSetNameDef(1), "Layer Set Name",
			fmt.Sprintf(
				"Name of the layer set, 1 to %d bytes on one line. An upload of the file makes a layer set of this name.",
				MaxLayerSetNameBytes,
			),
			[]any{exampleLayerSetName},
		),
		keyDescription: documented(
			description, "Layer Set Description",
			fmt.Sprintf("Text shown with the layer set, at most %d bytes on one line.", MaxLayerSetDescriptionBytes),
			[]any{"Levels of the water plant network, from the field devices to the office."},
		),
		keyIconKey: documentedRef(
			keyIconKey, "Layer Set Icon Key", "Built-in icon shown with the layer set. Empty for none.",
			[]any{"plc"},
		),
		keyIcon: documentedRef(
			defIconRef, "Layer Set Custom Icon",
			"Custom icon shown with the layer set in place of its built-in icon. Empty for none.",
			[]any{exampleIconName},
		),
		keyMatchConflict: documentedRef(
			keyMatchConflict, "Match Conflict Rule",
			"Layer that a device gets when the node matchers of more than one layer match it.",
			[]any{MatchConflictLowest},
		),
		keyLayerOutlines: documented(
			boolDef(), "Layer Outlines",
			"When true, the editor draws the outline of a node in the color of its layer. It does not do this when "+
				"the node has an outline color of its own or the layer has no color.",
			[]any{false},
		),
		keyLayers: documented(
			layers, "Layers",
			fmt.Sprintf(
				"Layers of the set, 1 to %d, each with an ID that no other layer has. A diagram that uses the set "+
					"gets a copy of them.",
				MaxLayers,
			),
			[]any{[]any{exampleMatchedLayer(), exampleLayer(exampleLayerID-1, "Field devices")}},
		),
		keyIcons: documented(
			icons, "Custom Icons",
			fmt.Sprintf(
				"Copies of the custom icons that the set and its layers name, at most %d, each by its icon name. The "+
					"set can also name an icon that the icon library of the server holds and the file does not hold.",
				MaxDocumentIcons,
			),
			[]any{map[string]any{exampleIconName: exampleIcon()}},
		),
	}
}
