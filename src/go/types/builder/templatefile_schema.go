package builder

import (
	"encoding/json"
	"fmt"
	"strings"
)

// defTemplateFileTemplate is the definition of one template of a template
// file, which has no identifier.
const defTemplateFileTemplate = "templateFileTemplate"

// exampleCollectionName names the collection the examples of the template
// file schema hold.
const exampleCollectionName = "Plant floor"

// TemplateFileSchema returns the JSON Schema of a template file (see
// [TemplateFile]) as a freshly built map. Like [Schema], it is self
// contained. It holds under $defs the phenix v1 component schemas that the
// spec of a template is checked against. It also holds every Builder
// definition that the parts of the file refer to, directly or through a
// different definition. Every part that the Builder owns has a title, a
// description and examples. [TemplateFile.Validate] checks what JSON Schema
// cannot express: names that differ only in case, and that the bytes of an
// icon are an accepted PNG.
func TemplateFileSchema() (map[string]any, error) {
	defs, err := PhenixDefs()
	if err != nil {
		return nil, err
	}

	pool := builderDefs()
	pool[defTemplateFileTemplate] = templateFileTemplateDef()

	root := objectDef([]string{schemaKey, keyName, keyTemplates}, templateFileProperties())

	if err := addReferencedDefs(defs, pool, root); err != nil {
		return nil, fmt.Errorf("building the template file schema: %w", err)
	}

	root[schemaKey] = SchemaDialect
	root["$id"] = TemplateFileSchemaURI
	root[keyTitle] = "phenix Builder template file"
	root[keyDescription] = "One collection of Builder device templates. The Builder exports and imports it, and " +
		"phenix reads it from its template directory at start."
	root["$defs"] = defs

	return root, nil
}

// addReferencedDefs copies into defs each definition of pool that a local
// reference ("#/$defs/<name>") in value names, and each definition that
// those definitions name in turn. Thus a schema built from parts of the
// document schema holds every definition it refers to, whatever fields the
// parts gain. A name that defs already holds stays as it is. A reference
// that names a definition that neither map holds is an error.
func addReferencedDefs(defs, pool map[string]any, value any) error {
	pending := localRefs(value, nil)

	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		if _, held := defs[name]; held {
			continue
		}

		def, found := pool[name]
		if !found {
			return fmt.Errorf("a reference names $defs/%s, which no definition has", name)
		}

		defs[name] = def
		pending = localRefs(def, pending)
	}

	return nil
}

// localRefs appends to names the definition name of every local reference
// in value, in no particular order.
func localRefs(value any, names []string) []string {
	switch typed := value.(type) {
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			if name, found := strings.CutPrefix(reference, localDefRef); found {
				names = append(names, name)
			}
		}

		for _, child := range typed {
			names = localRefs(child, names)
		}
	case []any:
		for _, child := range typed {
			names = localRefs(child, names)
		}
	}

	return names
}

// TemplateFileSchemaJSON returns [TemplateFileSchema] as deterministic,
// indented JSON.
func TemplateFileSchemaJSON() ([]byte, error) {
	schema, err := TemplateFileSchema()
	if err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling the template file schema: %w", err)
	}

	return data, nil
}

// templateFileProperties returns the schemas of the fields of a template
// file.
func templateFileProperties() map[string]any {
	name := stringDef()
	name["minLength"] = 1
	name["maxLength"] = MaxTemplateNameBytes
	name["pattern"] = noControlPattern

	description := stringDef()
	description["maxLength"] = MaxTemplateDescriptionBytes
	description["pattern"] = noControlPattern

	templates := arrayDef(ref(defTemplateFileTemplate))
	templates["minItems"] = 1
	templates["maxItems"] = MaxTemplateFileTemplates

	icons := map[string]any{
		keyType:                "object",
		"maxProperties":        MaxDocumentIcons,
		"propertyNames":        map[string]any{"pattern": `^` + iconNameSchemaPattern() + `$`},
		"additionalProperties": ref(keyIcon),
	}

	return map[string]any{
		schemaKey: documented(
			constDef(TemplateFileSchemaURI), "Schema URI",
			"Identifies the template file format. A template file must name it exactly.",
			[]any{TemplateFileSchemaURI},
		),
		keyName: documented(
			name, "Collection Name",
			fmt.Sprintf(
				"Name of the collection that the file holds, 1 to %d bytes on one line. An import of the file makes "+
					"a collection of this name. phenix lists a file that it reads at start under this name.",
				MaxTemplateNameBytes,
			),
			[]any{exampleCollectionName},
		),
		keyDescription: documented(
			description, "Collection Description",
			fmt.Sprintf("Text shown with the collection, at most %d bytes on one line.", MaxTemplateDescriptionBytes),
			[]any{"Devices of the plant's control network."},
		),
		keyTemplates: documented(
			templates, "Templates",
			fmt.Sprintf(
				"Templates of the collection, 1 to %d, in order. Their names are different, even when case is ignored.",
				MaxTemplateFileTemplates,
			),
			[]any{[]any{exampleTemplateFileTemplate()}},
		),
		keyIcons: documented(
			icons, "Custom Icons",
			fmt.Sprintf(
				"Copies of the custom icons that the templates name, at most %d, each by its icon name. A template "+
					"can also name an icon that the icon library of the server holds and the file does not hold.",
				MaxDocumentIcons,
			),
			[]any{map[string]any{exampleIconName: exampleIcon()}},
		),
	}
}

// templateFileTemplateDef builds the schema of one template of a template
// file, bounded the way [Template.Issues] bounds one. maxLength counts
// characters, as it does for the document name.
func templateFileTemplateDef() map[string]any {
	name := stringDef()
	name["minLength"] = 1
	name["maxLength"] = MaxTemplateNameBytes
	name["pattern"] = noControlPattern

	description := stringDef()
	description["maxLength"] = MaxTemplateDescriptionBytes
	description["pattern"] = noControlPattern

	return documented(
		objectDef(
			[]string{keyName, "device"},
			map[string]any{
				keyName: documented(
					name, "Template Name",
					fmt.Sprintf(
						"Name under which the editor offers the template, 1 to %d bytes on one line. No other template "+
							"of the file has this name, ignoring case.",
						MaxTemplateNameBytes,
					),
					[]any{"Edge router"},
				),
				keyDescription: documented(
					description, "Template Description",
					fmt.Sprintf(
						"Text shown with the template, at most %d bytes on one line. It is never written into a node.",
						MaxTemplateDescriptionBytes,
					),
					[]any{"VyOS router with one static interface"},
				),
				"device": documentedRef(
					"templateDevice", "Template Device", "Fields that the template fills in on a device made from it.",
					[]any{exampleTemplateDevice()},
				),
			},
		),
		"Template File Template",
		"Named set of prefilled fields for a device node, without an identifier. The place that keeps it gives it one.",
		[]any{exampleTemplateFileTemplate()},
	)
}

// exampleTemplateFileTemplate returns a template of the router as a template
// file holds it.
func exampleTemplateFileTemplate() map[string]any {
	return map[string]any{
		keyName:        "Edge router",
		keyDescription: "VyOS router with one static interface",
		"device":       exampleTemplateDevice(),
	}
}
