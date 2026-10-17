package builder

import (
	"encoding/json"
	"fmt"
	"maps"
)

// The definitions of the package schema, in addition to those of the
// document schema.
const (
	defPackageDocument     = "packageDocument"
	defPackageConfig       = "packageConfig"
	defPackageRequirements = "packageRequirements"
	defPackageImage        = "packageImage"
)

// PackageSchema returns the JSON Schema of a Builder package (see [Package])
// as a freshly built map. Like [Schema], it is self contained. It checks the
// document of a package against the document schema, and it holds the
// definitions of that schema and the bundled phenix v1 component schemas
// under $defs. Every part that the Builder owns has a title, a description
// and examples. [Package.Validate] checks what JSON Schema cannot express:
// that the requirements name each carried config, and the checks of
// [Document.Validate] on the document.
func PackageSchema() (map[string]any, error) {
	defs, err := PhenixDefs()
	if err != nil {
		return nil, err
	}

	maps.Copy(defs, builderDefs())

	defs[defPackageDocument] = packageDocumentDef()
	defs[defPackageConfig] = packageConfigDef()
	defs[defPackageRequirements] = packageRequirementsDef()
	defs[defPackageImage] = packageImageDef()

	root := objectDef([]string{schemaKey, keyDocument, keyRequirements}, packageProperties())

	root[schemaKey] = SchemaDialect
	root["$id"] = PackageSchemaURI
	root[keyTitle] = "phenix Builder package"
	root[keyDescription] = "One file that holds a Builder document and the list of what the diagram needs on a " +
		"phenix server. It also holds the configs and icons that the document names and that the user selected."
	root["$defs"] = defs

	return root, nil
}

// PackageSchemaJSON returns [PackageSchema] as deterministic, indented JSON.
func PackageSchemaJSON() ([]byte, error) {
	schema, err := PackageSchema()
	if err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling the package schema: %w", err)
	}

	return data, nil
}

// packageProperties returns the schemas of the fields of a package.
func packageProperties() map[string]any {
	return map[string]any{
		schemaKey: documented(
			constDef(PackageSchemaURI), "Schema URI",
			"Identifies the Builder package format. A package must name it exactly.",
			[]any{PackageSchemaURI},
		),
		keyDocument: documentedRef(
			defPackageDocument, "Document",
			"The Builder document that the package holds, with copies of the custom icons that the document holds.",
			[]any{examplePackageDocument()},
		),
		keyScenarios: documented(
			packageConfigsDef(PackageKindScenario, MaxScenarios), "Scenario Configs",
			fmt.Sprintf(
				"Scenario configs that the document names and that the package holds, at most %d, each under its name. "+
					"An upload of the package creates a config only when the user selects it and the server has no "+
					"config of that name.",
				MaxScenarios,
			),
			[]any{map[string]any{exampleScenarioName: examplePackageScenario()}},
		),
		keyTopologies: documented(
			packageConfigsDef(PackageKindTopology, MaxPackageTopologies), "Topology Configs",
			fmt.Sprintf(
				"Topology configs that the document includes and that the package holds, at most %d, each under its "+
					"name. An upload of the package creates a config only when the user selects it and the server has "+
					"no config of that name.",
				MaxPackageTopologies,
			),
			[]any{map[string]any{exampleInclude: examplePackageTopology()}},
		),
		keyRequirements: documentedRef(
			defPackageRequirements, "Requirements",
			"What the diagram needs on a phenix server, whether the package holds it or not.",
			[]any{examplePackageRequirements()},
		),
	}
}

// packageDocumentDef builds the schema of the document a package holds: a
// Builder document, as the document schema describes it.
func packageDocumentDef() map[string]any {
	return documented(
		objectDef(
			[]string{
				schemaKey, revisionKey, keyMetadata, keyNodes, keyNetworks, keyEdges,
				keyViewport, keyGrid,
			},
			rootProperties(),
		),
		"Builder Document",
		"Builder document, as the document schema "+SchemaURI+" describes it.",
		[]any{examplePackageDocument()},
	)
}

// packageConfigsDef builds the schema of one list of the configs a package
// carries: an object of configs of one kind, each under its name.
func packageConfigsDef(kind string, limit int) map[string]any {
	return map[string]any{
		keyType:         "object",
		"maxProperties": limit,
		"propertyNames": map[string]any{"pattern": configNamePattern, "maxLength": MaxScenarioNameBytes},
		"additionalProperties": map[string]any{
			"allOf": []any{
				ref(defPackageConfig),
				map[string]any{keyProperties: map[string]any{keyKind: constDef(kind)}},
			},
		},
	}
}

// packageConfigDef builds the schema of a config a package carries.
func packageConfigDef() map[string]any {
	apiVersion := stringDef()
	apiVersion["pattern"] = packageAPIVersion.String()

	name := stringDef()
	name["minLength"] = 1
	name["maxLength"] = MaxScenarioNameBytes
	name["pattern"] = configNamePattern

	annotations := map[string]any{keyType: "object", "additionalProperties": stringDef()}

	metadata := objectDef([]string{keyName}, map[string]any{
		keyName: documented(
			name, "Config Name", "Name of the config, which is also its key in the package.",
			[]any{exampleScenarioName},
		),
		keyAnnotations: documented(
			annotations, "Annotations",
			"Annotations of the config, without the Builder's own (builder-*).",
			[]any{map[string]any{exampleAnnotationKey: exampleTopology}},
		),
	})

	return documented(
		objectDef([]string{keyAPIVersion, keyKind, keyMetadata, keySpec}, map[string]any{
			keyAPIVersion: documented(
				apiVersion, "API Version", "apiVersion of the config: phenix.sandia.gov/ and a version.",
				[]any{exampleAPIVersion},
			),
			keyKind: documented(
				enumDef([]any{PackageKindScenario, PackageKindTopology}), "Kind",
				"Kind of the config: Scenario in the scenarios of the package, Topology in its topologies.",
				[]any{PackageKindScenario},
			),
			keyMetadata: documented(
				metadata, "Config Metadata", "Name and annotations of the config.",
				[]any{map[string]any{keyName: exampleScenarioName}},
			),
			keySpec: documented(
				map[string]any{keyType: "object"}, "Spec",
				"Spec of the config, as the source server stores it. The server that creates the config checks it.",
				[]any{examplePackageScenarioSpec()},
			),
		}),
		"Packaged Config",
		"phenix config that a package holds, as data. A server that creates it applies its own config checks.",
		[]any{examplePackageScenario()},
	)
}

// requirementDef builds the schema of one entry of a list of the
// requirements: a name, a path or a hostname.
func requirementDef() map[string]any {
	def := stringDef()
	def["minLength"] = 1
	def["maxLength"] = MaxRequirementBytes
	def["pattern"] = noControlPattern

	return def
}

// requirementListDef builds the schema of one list of the requirements.
func requirementListDef(items map[string]any) map[string]any {
	def := arrayDef(items)
	def["maxItems"] = MaxPackageRequirements

	return def
}

// packageRequirementsDef builds the schema of the requirements of a package.
func packageRequirementsDef() map[string]any {
	properties := map[string]any{
		keyScenarios: documented(
			requirementListDef(requirementDef()), "Scenarios", "Names of the Scenario configs the document names.",
			[]any{[]any{exampleScenarioName}},
		),
		keyTopologies: documented(
			requirementListDef(requirementDef()), "Included Topologies",
			"Names of the topologies the document includes.",
			[]any{[]any{exampleInclude}},
		),
		keyTemplates: documented(
			requirementListDef(requirementDef()), "Templates", "Names of the device templates the document carries.",
			[]any{[]any{"Edge router"}},
		),
		keyIcons: documented(
			requirementListDef(requirementDef()), "Custom Icons",
			"Names of the custom icons that the nodes and templates of the document name.",
			[]any{[]any{exampleIconName}},
		),
		keyImages: documented(
			requirementListDef(ref(defPackageImage)), "Disk Images",
			"Disk images that the devices boot from, each with the devices that use it. Empty unless the package "+
				"was made to list them.",
			[]any{[]any{examplePackageImage()}},
		),
		keyApps: documented(
			requirementListDef(requirementDef()), "Apps",
			"Names of the apps of the readable Scenario configs that the document names.",
			[]any{[]any{examplePackageApp}},
		),
		keyFiles: documented(
			requirementListDef(requirementDef()), "Files",
			"Paths on the server that the devices of the document and the packaged configs name, such as injection "+
				"sources. The package never holds their content.",
			[]any{[]any{examplePackageFile}},
		),
	}

	return documented(
		objectDef(
			[]string{keyScenarios, keyTopologies, keyTemplates, keyIcons, keyImages, keyApps, keyFiles},
			properties,
		),
		"Package Requirements",
		fmt.Sprintf(
			"What the diagram needs on a phenix server. Each list is present, and empty when it has no entries. "+
				"Each list holds at most %d entries of at most %d bytes.",
			MaxPackageRequirements, MaxRequirementBytes,
		),
		[]any{examplePackageRequirements()},
	)
}

// packageImageDef builds the schema of one disk image of the requirements.
func packageImageDef() map[string]any {
	return documented(
		objectDef([]string{keyName, keyUsedBy}, map[string]any{
			keyName: documented(
				requirementDef(), "Image", "Disk image, as the drives of the devices name it.", []any{exampleImage},
			),
			keyUsedBy: documented(
				requirementListDef(requirementDef()), "Used By", "Hostnames of the devices that use the image.",
				[]any{[]any{exampleHostname}},
			),
		}),
		"Disk Image Requirement", "Disk image that the devices of the diagram need, with the devices that use it.",
		[]any{examplePackageImage()},
	)
}

// The values of the package schema's examples.
const (
	exampleImage         = "vyos.qc2"
	examplePackageFile   = "/phenix/injects/router-config.boot"
	examplePackageApp    = "ntp"
	exampleAnnotationKey = "topology"
)

// examplePackageDocument returns a small document as a package holds it.
func examplePackageDocument() map[string]any {
	return map[string]any{
		schemaKey:    SchemaURI,
		revisionKey:  SchemaRevision,
		keyMetadata:  exampleMetadata(),
		keyNodes:     []any{exampleDeviceNode()},
		keyNetworks:  []any{exampleNetwork()},
		keyEdges:     []any{},
		keyViewport:  exampleViewport(),
		keyGrid:      exampleGrid(),
		keyScenarios: []any{exampleScenarioName},
	}
}

// examplePackageScenarioSpec returns the spec of a Scenario config with one
// app.
func examplePackageScenarioSpec() map[string]any {
	return map[string]any{
		keyApps: []any{map[string]any{
			keyName: examplePackageApp,
			"hosts": []any{map[string]any{"hostname": exampleHostname, "metadata": map[string]any{}}},
		}},
	}
}

// examplePackageScenario returns a Scenario config as a package carries it.
func examplePackageScenario() map[string]any {
	return map[string]any{
		keyAPIVersion: "phenix.sandia.gov/v2",
		keyKind:       PackageKindScenario,
		keyMetadata: map[string]any{
			keyName:        exampleScenarioName,
			keyAnnotations: map[string]any{exampleAnnotationKey: exampleTopology},
		},
		keySpec: examplePackageScenarioSpec(),
	}
}

// examplePackageTopology returns a Topology config as a package carries it.
func examplePackageTopology() map[string]any {
	return map[string]any{
		keyAPIVersion: exampleAPIVersion,
		keyKind:       PackageKindTopology,
		keyMetadata:   map[string]any{keyName: exampleInclude},
		keySpec:       map[string]any{keyNodes: []any{exampleSpec()}},
	}
}

// examplePackageImage returns the disk image of the router.
func examplePackageImage() map[string]any {
	return map[string]any{keyName: exampleImage, keyUsedBy: []any{exampleHostname}}
}

// examplePackageRequirements returns what the example document needs.
func examplePackageRequirements() map[string]any {
	return map[string]any{
		keyScenarios:  []any{exampleScenarioName},
		keyTopologies: []any{exampleInclude},
		keyTemplates:  []any{},
		keyIcons:      []any{},
		keyImages:     []any{examplePackageImage()},
		keyApps:       []any{examplePackageApp},
		keyFiles:      []any{examplePackageFile},
	}
}
