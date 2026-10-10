package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"phenix/types/builder"
)

// templateFileYAML is a template file as the Builder exports one: two
// templates, one naming the icon the file carries.
const templateFileYAML = `$schema: https://phenix.sandia.gov/schemas/builder/templates/v1
name: Plant floor
description: Devices of the plant's control network.
templates:
  - name: PLC
    description: Programmable logic controller
    device:
      iconKey: server
      icon: plc-icon
      spec:
        type: VirtualMachine
        general:
          hostname: plc
          vm_type: kvm
        hardware:
          vcpus: 2
          drives:
            - image: plc.qc2
  - name: HMI
    device:
      spec:
        type: VirtualMachine
        general:
          hostname: hmi
icons:
  plc-icon:
    data: ` + iconFixtureData + "\n"

// templateFileValue returns a valid template file as a JSON value, to be
// changed by a test.
func templateFileValue() map[string]any {
	return map[string]any{
		"$schema": builder.TemplateFileSchemaURI,
		"name":    "Plant floor",
		"templates": []any{
			map[string]any{
				"name": "PLC",
				"device": map[string]any{
					"icon": iconFixtureName,
					"spec": map[string]any{"type": "VirtualMachine", "general": map[string]any{"hostname": "plc"}},
				},
			},
		},
		"icons": map[string]any{iconFixtureName: map[string]any{"data": iconFixtureData}},
	}
}

// templateFileJSON encodes a template file value.
func templateFileJSON(t *testing.T, value map[string]any) []byte {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding the template file: %v", err)
	}

	return data
}

// templateFileIssues returns the issues ParseTemplateFile finds, each as
// "path: message", and fails the test when it refuses the text for any
// other reason.
func templateFileIssues(t *testing.T, text []byte) []string {
	t.Helper()

	_, err := builder.ParseTemplateFile(text)

	var refused *builder.TemplateFileError
	if !errors.As(err, &refused) {
		t.Fatalf("ParseTemplateFile error = %v, want a TemplateFileError", err)
	}

	if !errors.Is(err, builder.ErrInvalidTemplateFile) {
		t.Fatalf("ParseTemplateFile error %v does not match ErrInvalidTemplateFile", err)
	}

	issues := make([]string, 0, len(refused.Issues))
	for _, issue := range refused.Issues {
		issues = append(issues, issue.String())
	}

	return issues
}

func TestParseTemplateFileReadsYAMLAndJSONAlike(t *testing.T) {
	fromYAML, err := builder.ParseTemplateFile([]byte(templateFileYAML))
	if err != nil {
		t.Fatalf("ParseTemplateFile(YAML) returned error: %v", err)
	}

	if fromYAML.Name != "Plant floor" || fromYAML.Description != "Devices of the plant's control network." {
		t.Fatalf("collection = %q, %q", fromYAML.Name, fromYAML.Description)
	}

	if len(fromYAML.Templates) != 2 || fromYAML.Templates[0].Name != "PLC" || fromYAML.Templates[1].Name != "HMI" {
		t.Fatalf("templates = %+v, want PLC then HMI", fromYAML.Templates)
	}

	// Numbers come back as a document's do: an integer is an int.
	hardware, _ := fromYAML.Templates[0].Device.Spec["hardware"].(map[string]any)
	if vcpus, ok := hardware["vcpus"].(int); !ok || vcpus != 2 {
		t.Fatalf("hardware.vcpus = %#v, want the int 2", hardware["vcpus"])
	}

	if fromYAML.Icons[iconFixtureName].Data != iconFixtureData {
		t.Fatalf("icons = %v, want the fixture icon", fromYAML.Icons)
	}

	encoded, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatalf("encoding the file: %v", err)
	}

	fromJSON, err := builder.ParseTemplateFile(encoded)
	if err != nil {
		t.Fatalf("ParseTemplateFile(JSON) returned error: %v", err)
	}

	again, err := json.Marshal(fromJSON)
	if err != nil {
		t.Fatalf("encoding the file again: %v", err)
	}

	if string(again) != string(encoded) {
		t.Fatalf("JSON and YAML read differently:\n%s\n%s", again, encoded)
	}

	template := fromJSON.Templates[0].Template("t1")
	if template.ID != "t1" || template.Name != "PLC" || template.Device.Icon != iconFixtureName {
		t.Fatalf("Template = %+v", template)
	}
}

func TestParseTemplateFileRefusesWhatYAMLMayNotHold(t *testing.T) {
	for name, text := range map[string]string{
		"an alias": "$schema: " + builder.TemplateFileSchemaURI + "\nname: &n Plant\ndescription: *n\n" +
			"templates: []\n",
		"a merge key": "$schema: " + builder.TemplateFileSchemaURI + "\nname: Plant\nbase: &b {a: 1}\n" +
			"templates:\n  - <<: *b\n",
		"a second document": templateFileYAML + "---\nname: other\n",
		"an empty file":     " \n",
		"a key used twice":  "name: a\nname: b\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := builder.ParseTemplateFile([]byte(text))
			if !errors.Is(err, builder.ErrInvalidTemplateFile) {
				t.Fatalf("ParseTemplateFile error = %v, want ErrInvalidTemplateFile", err)
			}
		})
	}

	_, err := builder.ParseTemplateFile([]byte("$schema: x\nname: &n Plant\n"))
	if !errors.Is(err, builder.ErrUnsupportedYAML) {
		t.Fatalf("an anchor: error = %v, want ErrUnsupportedYAML", err)
	}
}

func TestParseTemplateFileRefusesUnknownFields(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"at the root": func(file map[string]any) { file["author"] = "alice" },
		"in a template": func(file map[string]any) {
			file["templates"].([]any)[0].(map[string]any)["id"] = "0d5f8f4e-6a57-4b53-9d0a-5c0f4e3b2a11"
		},
		"in a device": func(file map[string]any) {
			file["templates"].([]any)[0].(map[string]any)["device"].(map[string]any)["hostname"] = "plc"
		},
		"in an icon": func(file map[string]any) {
			file["icons"].(map[string]any)[iconFixtureName].(map[string]any)["owner"] = "alice"
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := templateFileValue()
			change(value)

			_, err := builder.ParseTemplateFile(templateFileJSON(t, value))

			var refused *builder.TemplateFileError

			switch {
			case !errors.Is(err, builder.ErrInvalidTemplateFile):
				t.Fatalf("ParseTemplateFile error = %v, want ErrInvalidTemplateFile", err)
			case errors.As(err, &refused):
				t.Fatalf("ParseTemplateFile refused an unknown field as invalid content: %v", err)
			case !strings.Contains(err.Error(), "unknown field"):
				t.Fatalf("ParseTemplateFile error = %v, want it to name the unknown field", err)
			}
		})
	}
}

func TestParseTemplateFileValidates(t *testing.T) {
	template := func(name string) map[string]any {
		return map[string]any{
			"name": name,
			"device": map[string]any{
				"spec": map[string]any{"general": map[string]any{"hostname": "h"}},
			},
		}
	}

	many := make([]any, 0, builder.MaxTemplateFileTemplates+1)
	for i := range builder.MaxTemplateFileTemplates + 1 {
		many = append(many, template(fmt.Sprintf("T%d", i)))
	}

	tests := []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{
			name:   "another schema",
			change: func(file map[string]any) { file["$schema"] = builder.SchemaURI },
			want:   `$schema: template file schema must be "` + builder.TemplateFileSchemaURI + `"`,
		},
		{
			name:   "no schema",
			change: func(file map[string]any) { delete(file, "$schema") },
			want:   "$schema: template file schema must be",
		},
		{
			name:   "a blank name",
			change: func(file map[string]any) { file["name"] = "  " },
			want:   "name: collection name is required",
		},
		{
			name:   "a long name",
			change: func(file map[string]any) { file["name"] = strings.Repeat("n", builder.MaxTemplateNameBytes+1) },
			want:   "name: collection name must be at most 128 bytes",
		},
		{
			name:   "a name on two lines",
			change: func(file map[string]any) { file["name"] = "Plant\nfloor" },
			want:   "name: collection name must not contain control characters",
		},
		{
			name: "a long description",
			change: func(file map[string]any) {
				file["description"] = strings.Repeat("d", builder.MaxTemplateDescriptionBytes+1)
			},
			want: "description: collection description must be at most 1024 bytes",
		},
		{
			name:   "no template",
			change: func(file map[string]any) { file["templates"] = []any{} },
			want:   "templates: a template file holds at least one template",
		},
		{
			name:   "null templates",
			change: func(file map[string]any) { file["templates"] = nil },
			want:   "templates: a template file holds at least one template",
		},
		{
			name:   "too many templates",
			change: func(file map[string]any) { file["templates"] = many },
			want:   "templates: a template file holds at most 200 templates, not 201",
		},
		{
			name: "two names that differ in case",
			change: func(file map[string]any) {
				file["templates"] = []any{template("Router"), template("HMI"), template("router")}
			},
			want: `templates[2].name: template name "router" is also the name of templates[0], ignoring case`,
		},
		{
			name: "a template without a spec",
			change: func(file map[string]any) {
				file["templates"] = []any{map[string]any{"name": "x", "device": map[string]any{}}}
			},
			want: "templates[0].device.spec: template device spec is required",
		},
		{
			name: "a bad custom icon name",
			change: func(file map[string]any) {
				file["templates"].([]any)[0].(map[string]any)["device"].(map[string]any)["icon"] = "a b"
			},
			want: `templates[0].device.icon: icon name "a b" must be 1 to 64`,
		},
		{
			name: "an unknown icon size",
			change: func(file map[string]any) {
				file["templates"].([]any)[0].(map[string]any)["device"].(map[string]any)["iconSize"] = "huge"
			},
			want: "templates[0].device.iconSize: ",
		},
		{
			name: "an icon that is not a PNG",
			change: func(file map[string]any) {
				file["icons"] = map[string]any{iconFixtureName: map[string]any{"data": "aGVsbG8="}}
			},
			want: `icons: icon "plc-icon" is not an accepted PNG`,
		},
		{
			name: "an icon whose name is not one",
			change: func(file map[string]any) {
				file["icons"] = map[string]any{"..": map[string]any{"data": iconFixtureData}}
			},
			want: `icons: icon name ".." must not be "." or ".."`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := templateFileValue()
			tt.change(value)

			issues := templateFileIssues(t, templateFileJSON(t, value))

			if !slices.ContainsFunc(issues, func(issue string) bool { return strings.HasPrefix(issue, tt.want) }) {
				t.Fatalf("issues = %q, want one starting %q", issues, tt.want)
			}
		})
	}
}

// TestTemplateFileNamesCorpus checks the template names a template file may
// not hold twice against testdata/template-file-names.json, which the front
// end's template-file.test.js reads too, so both compare names alike.
func TestTemplateFileNamesCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "template-file-names.json"))
	if err != nil {
		t.Fatalf("reading the corpus: %v", err)
	}

	var corpus struct {
		Cases []struct {
			Name      string   `json:"name"`
			Names     []string `json:"names"`
			Duplicate bool     `json:"duplicate"`
		} `json:"cases"`
	}

	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decoding the corpus: %v", err)
	}

	if len(corpus.Cases) == 0 {
		t.Fatal("the corpus holds no case")
	}

	for _, tt := range corpus.Cases {
		t.Run(tt.Name, func(t *testing.T) {
			templates := make([]any, 0, len(tt.Names))

			for _, name := range tt.Names {
				templates = append(templates, map[string]any{
					"name": name,
					"device": map[string]any{
						"spec": map[string]any{"general": map[string]any{"hostname": "h"}},
					},
				})
			}

			value := templateFileValue()
			value["templates"] = templates

			last := len(tt.Names) - 1
			want := fmt.Sprintf("templates[%d].name: template name %q is also the name of", last, tt.Names[last])

			_, err := builder.ParseTemplateFile(templateFileJSON(t, value))

			if !tt.Duplicate {
				if err != nil {
					t.Fatalf("ParseTemplateFile returned error: %v, want the names taken as different", err)
				}

				return
			}

			issues := templateFileIssues(t, templateFileJSON(t, value))

			if !slices.ContainsFunc(issues, func(issue string) bool { return strings.HasPrefix(issue, want) }) {
				t.Fatalf("issues = %q, want one starting %q", issues, want)
			}
		})
	}
}

func TestParseTemplateFileTakesIconsTheFileDoesNotCarry(t *testing.T) {
	value := templateFileValue()
	delete(value, "icons")

	file, err := builder.ParseTemplateFile(templateFileJSON(t, value))
	if err != nil {
		t.Fatalf("ParseTemplateFile returned error: %v", err)
	}

	if file.Templates[0].Device.Icon != iconFixtureName || len(file.Icons) != 0 {
		t.Fatalf("file = %+v, want the template to name an icon the file does not carry", file)
	}
}

func TestParseTemplateFileRefusesTooMuchText(t *testing.T) {
	text := []byte(templateFileYAML + "# " + strings.Repeat("x", builder.MaxTemplateFileBytes) + "\n")

	_, err := builder.ParseTemplateFile(text)
	if !errors.Is(err, builder.ErrInvalidTemplateFile) || !strings.Contains(err.Error(), "the limit is") {
		t.Fatalf("ParseTemplateFile error = %v, want the size refused", err)
	}
}

func TestTemplateFileIsNoDocument(t *testing.T) {
	if builder.IsDocumentText([]byte(templateFileYAML)) {
		t.Fatal("IsDocumentText takes a template file for a Builder document")
	}

	if !builder.IsDocumentText([]byte(`{"$schema": "` + builder.SchemaURI + `"}`)) {
		t.Fatal("IsDocumentText no longer recognizes a Builder document")
	}
}

func TestDocsTemplateFileExampleLoads(t *testing.T) {
	file, err := builder.ParseTemplateFile(readDocsExample(t, "node-templates.yaml"))
	if err != nil {
		t.Fatalf("the docs example node-templates.yaml does not load: %v", err)
	}

	if len(file.Templates) == 0 {
		t.Fatal("the docs example holds no template")
	}
}

// TestTemplateFileSchemaDocumentsEveryProperty holds the template file
// schema to the documentation convention of the document schema (see
// TestSchemaDocumentsEveryProperty).
func TestTemplateFileSchemaDocumentsEveryProperty(t *testing.T) {
	schema, err := builder.TemplateFileSchema()
	if err != nil {
		t.Fatalf("TemplateFileSchema returned error: %v", err)
	}

	if schema["$id"] != builder.TemplateFileSchemaURI {
		t.Fatalf("$id = %v, want %s", schema["$id"], builder.TemplateFileSchemaURI)
	}

	var missing []string

	for name, property := range mapAt(t, schema, "properties") {
		missing = append(missing, undocumented("properties."+name, property, true)...)
	}

	for name, def := range mapAt(t, schema, "$defs") {
		if strings.HasPrefix(name, builder.PhenixDefPrefix) {
			continue
		}

		missing = append(missing, undocumented("$defs."+name, def, true)...)
	}

	if len(missing) > 0 {
		slices.Sort(missing)
		t.Fatalf("these lack a title, a description or examples:\n  %s", strings.Join(missing, "\n  "))
	}
}

// schemaRefs adds to found every "$ref" in value, keyed by where it is.
func schemaRefs(value any, at string, found map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if reference, ok := child.(string); ok && key == "$ref" {
				found[at] = reference

				continue
			}

			schemaRefs(child, at+"/"+key, found)
		}
	case []any:
		for i, child := range typed {
			schemaRefs(child, fmt.Sprintf("%s/%d", at, i), found)
		}
	}
}

// TestTemplateFileSchemaReferencesResolve asserts every reference of the
// template file schema, as GET /schemas/builder/templates/v1 serves it, in
// its properties and in each definition it holds, names a definition it
// holds: the definitions the file's parts refer to are copied by following
// the references, so a field a template device gains (such as its icon
// size) brings its definition along.
func TestTemplateFileSchemaReferencesResolve(t *testing.T) {
	encoded, err := builder.TemplateFileSchemaJSON()
	if err != nil {
		t.Fatalf("TemplateFileSchemaJSON returned error: %v", err)
	}

	var schema map[string]any

	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("decoding the template file schema: %v", err)
	}

	defs := mapAt(t, schema, "$defs")
	refs := map[string]string{}

	schemaRefs(schema, "#", refs)

	if len(refs) == 0 {
		t.Fatal("the template file schema holds no reference")
	}

	for at, reference := range refs {
		name, local := strings.CutPrefix(reference, "#/$defs/")

		switch _, held := defs[name]; {
		case !local:
			t.Errorf("%s refers to %q, outside the schema", at, reference)
		case !held:
			t.Errorf("%s refers to $defs/%s, which the schema does not hold", at, name)
		}
	}

	// The template device refers to the icon size, which the file schema
	// holds, as it does the definitions the icon size has no part in.
	for _, name := range []string{"templateDevice", "iconSize", "iconKey", "iconRef", "hexColor"} {
		if _, held := defs[name]; !held {
			t.Errorf("the schema does not hold $defs/%s", name)
		}
	}
}

func TestParseTemplateFileKeepsIconSize(t *testing.T) {
	value := templateFileValue()
	value["templates"].([]any)[0].(map[string]any)["device"].(map[string]any)["iconSize"] = builder.IconSizeLarge

	fromJSON, err := builder.ParseTemplateFile(templateFileJSON(t, value))
	if err != nil {
		t.Fatalf("ParseTemplateFile(JSON) returned error: %v", err)
	}

	if got := fromJSON.Templates[0].Template("t1").Device.IconSize; got != builder.IconSizeLarge {
		t.Fatalf("iconSize = %q, want %q", got, builder.IconSizeLarge)
	}

	const icon = "      icon: plc-icon\n"

	text := strings.Replace(templateFileYAML, icon, icon+"      iconSize: medium\n", 1)

	fromYAML, err := builder.ParseTemplateFile([]byte(text))
	if err != nil {
		t.Fatalf("ParseTemplateFile(YAML) returned error: %v", err)
	}

	if got := fromYAML.Templates[0].Device.IconSize; got != builder.IconSizeMedium {
		t.Fatalf("iconSize = %q, want %q", got, builder.IconSizeMedium)
	}

	// It is written back as it was read.
	encoded, err := json.Marshal(fromYAML.Templates[0].Device)
	if err != nil || !strings.Contains(string(encoded), `"iconSize":"medium"`) {
		t.Fatalf("the device encodes as %s, %v; want its icon size", encoded, err)
	}
}
