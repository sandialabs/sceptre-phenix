package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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

// templateFileIssues returns the issues ParseTemplateFile finds as "path: message", and fails the
// test when it refuses the text for another reason.
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

	type collection struct{ Name, Description string }

	wantCollection := collection{Name: "Plant floor", Description: "Devices of the plant's control network."}
	if diff := cmp.Diff(wantCollection, collection{fromYAML.Name, fromYAML.Description}); diff != "" {
		t.Errorf("ParseTemplateFile(YAML) collection mismatch (-want +got):\n%s", diff)
	}

	names := make([]string, 0, len(fromYAML.Templates))
	for _, template := range fromYAML.Templates {
		names = append(names, template.Name)
	}

	// The checks below read the first template.
	if diff := cmp.Diff([]string{"PLC", "HMI"}, names); diff != "" {
		t.Fatalf("ParseTemplateFile(YAML) template names mismatch (-want +got):\n%s", diff)
	}

	// Numbers come back as a document's do: an integer is an int.
	hardware, _ := fromYAML.Templates[0].Device.Spec["hardware"].(map[string]any)
	if vcpus, ok := hardware["vcpus"].(int); !ok || vcpus != 2 {
		t.Errorf("ParseTemplateFile(YAML) PLC hardware.vcpus = %#v, want the int 2", hardware["vcpus"])
	}

	if diff := cmp.Diff(map[string]builder.Icon{iconFixtureName: {Data: iconFixtureData}}, fromYAML.Icons); diff != "" {
		t.Errorf("ParseTemplateFile(YAML) icons mismatch (-want +got):\n%s", diff)
	}

	encoded, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatalf("encoding the file: %v", err)
	}

	fromJSON, err := builder.ParseTemplateFile(encoded)
	if err != nil {
		t.Fatalf("ParseTemplateFile(JSON) returned error: %v", err)
	}

	if diff := cmp.Diff(jsonValue(t, fromYAML), jsonValue(t, fromJSON)); diff != "" {
		t.Errorf("ParseTemplateFile(JSON) mismatch with ParseTemplateFile(YAML) (-yaml +json):\n%s", diff)
	}

	type made struct{ ID, Name, Icon string }

	template := fromJSON.Templates[0].Template("t1")
	if diff := cmp.Diff(made{"t1", "PLC", iconFixtureName}, made{template.ID, template.Name, template.Device.Icon}); diff != "" {
		t.Errorf("Template(t1) of the PLC mismatch (-want +got):\n%s", diff)
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
		t.Errorf("ParseTemplateFile(text with an anchor) error = %v, want ErrUnsupportedYAML", err)
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
				t.Errorf("ParseTemplateFile error = %v, want ErrInvalidTemplateFile", err)
			case errors.As(err, &refused):
				t.Errorf("ParseTemplateFile error = %v, a TemplateFileError, want the unknown field refused before validation", err)
			case !strings.Contains(err.Error(), "unknown field"):
				t.Errorf("ParseTemplateFile error = %v, want it to name the unknown field", err)
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
			want:   "name: a collection needs a name",
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
			want: "templates[0].device.spec: a template device needs a spec",
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
			name: "a layer above the largest layer ID",
			change: func(file map[string]any) {
				file["templates"].([]any)[0].(map[string]any)["device"].(map[string]any)["layer"] = 100
			},
			want: "templates[0].device.layer: layer must be an integer from 0 to 99",
		},
		{
			name: "a negative layer",
			change: func(file map[string]any) {
				file["templates"].([]any)[0].(map[string]any)["device"].(map[string]any)["layer"] = -1
			},
			want: "templates[0].device.layer: layer must be an integer from 0 to 99",
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
				t.Errorf("ParseTemplateFile issues = %q, want one starting %q", issues, tt.want)
			}
		})
	}
}

// TestTemplateFileNamesCorpus checks duplicate template names against
// testdata/template-file-names.json, which template-file.test.js reads too.
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
					t.Errorf("ParseTemplateFile(templates named %q) error = %v, want the names taken as different", tt.Names, err)
				}

				return
			}

			issues := templateFileIssues(t, templateFileJSON(t, value))

			if !slices.ContainsFunc(issues, func(issue string) bool { return strings.HasPrefix(issue, want) }) {
				t.Errorf("ParseTemplateFile(templates named %q) issues = %q, want one starting %q", tt.Names, issues, want)
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

	if got := file.Templates[0].Device.Icon; got != iconFixtureName {
		t.Errorf("ParseTemplateFile(file without icons) template icon = %q, want %q", got, iconFixtureName)
	}

	if len(file.Icons) != 0 {
		t.Errorf("ParseTemplateFile(file without icons) icons = %v, want none", file.Icons)
	}
}

func TestParseTemplateFileRefusesTooMuchText(t *testing.T) {
	text := []byte(templateFileYAML + "# " + strings.Repeat("x", builder.MaxTemplateFileBytes) + "\n")

	_, err := builder.ParseTemplateFile(text)
	if !errors.Is(err, builder.ErrInvalidTemplateFile) || !strings.Contains(err.Error(), "the limit is") {
		t.Errorf("ParseTemplateFile(text past the limit) error = %v, want ErrInvalidTemplateFile naming the limit", err)
	}
}

func TestTemplateFileIsNoDocument(t *testing.T) {
	if got := builder.FileKindOf([]byte(templateFileYAML)); got != builder.FileKindTemplateFile {
		t.Errorf("FileKindOf(template file) = %q, want %q", got, builder.FileKindTemplateFile)
	}

	if got := builder.FileKindOf([]byte(`{"$schema": "` + builder.SchemaURI + `"}`)); got != builder.FileKindDocument {
		t.Errorf("FileKindOf(document schema) = %q, want %q", got, builder.FileKindDocument)
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
		t.Errorf("ParseTemplateFile(JSON) template iconSize = %q, want %q", got, builder.IconSizeLarge)
	}

	const icon = "      icon: plc-icon\n"

	text := strings.Replace(templateFileYAML, icon, icon+"      iconSize: medium\n", 1)

	fromYAML, err := builder.ParseTemplateFile([]byte(text))
	if err != nil {
		t.Fatalf("ParseTemplateFile(YAML) returned error: %v", err)
	}

	if got := fromYAML.Templates[0].Device.IconSize; got != builder.IconSizeMedium {
		t.Errorf("ParseTemplateFile(YAML) template iconSize = %q, want %q", got, builder.IconSizeMedium)
	}

	// It is written back as it was read.
	written := writtenObject(t, fromYAML.Templates[0].Device)
	if got := written["iconSize"]; got != "medium" {
		t.Errorf("json.Marshal(device) iconSize = %v, want medium", got)
	}
}

func TestParseTemplateFileKeepsLayer(t *testing.T) {
	// Layer 0 is a layer, not none, so it is kept too.
	for _, layer := range []int{0, 3, builder.MaxLayerID} {
		t.Run(strconv.Itoa(layer), func(t *testing.T) {
			value := templateFileValue()
			value["templates"].([]any)[0].(map[string]any)["device"].(map[string]any)["layer"] = layer

			parsed, err := builder.ParseTemplateFile(templateFileJSON(t, value))
			if err != nil {
				t.Fatalf("ParseTemplateFile returned error: %v", err)
			}

			device := parsed.Templates[0].Template("t1").Device
			if device.Layer == nil {
				t.Errorf("ParseTemplateFile(layer %d) template layer = nil, want %d", layer, layer)
			} else if *device.Layer != layer {
				t.Errorf("ParseTemplateFile(layer %d) template layer = %d, want %d", layer, *device.Layer, layer)
			}

			// It is written back as it was read.
			if got := writtenObject(t, device)["layer"]; got != float64(layer) {
				t.Errorf("json.Marshal(device) layer = %v, want %d", got, layer)
			}
		})
	}

	// A template without a layer writes none.
	parsed, err := builder.ParseTemplateFile(templateFileJSON(t, templateFileValue()))
	if err != nil {
		t.Fatalf("ParseTemplateFile returned error: %v", err)
	}

	if got, ok := writtenObject(t, parsed.Templates[0].Template("t1").Device)["layer"]; ok {
		t.Errorf("json.Marshal(device without a layer) layer = %v, want it left out", got)
	}
}

// writtenObject returns a value as its JSON encoding decodes into an object.
func writtenObject(t *testing.T, value any) map[string]any {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}

	var object map[string]any

	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decoding %s: %v", data, err)
	}

	return object
}
