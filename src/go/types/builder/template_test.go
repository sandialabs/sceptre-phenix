package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"phenix/types/builder"
)

// Identifiers of the templates tests add to a document.
const (
	idTemplate  = "0d5f8f4e-6a57-4b53-9d0a-5c0f4e3b2a11"
	idTemplate2 = "7c1a2b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b"
)

// sampleTemplate returns a template that uses every field, the custom icon
// being the fixture's (see iconFixtureName).
func sampleTemplate() builder.Template {
	return builder.Template{
		ID:          idTemplate,
		Name:        "PLC",
		Description: "Programmable logic controller",
		Device: builder.TemplateDevice{
			IconKey:      "server",
			Icon:         iconFixtureName,
			OutlineColor: "#2f6fbf",
			FillColor:    "#EEF4FB",
			Spec: map[string]any{
				"type":    "VirtualMachine",
				"general": map[string]any{"hostname": "plc", "vm_type": "kvm"},
				"hardware": map[string]any{
					"os_type": "linux",
					"vcpus":   2,
					"drives":  []any{map[string]any{"image": "plc.qc2"}},
				},
				"network": map[string]any{"interfaces": []any{
					map[string]any{"name": "eth0", "type": "ethernet", "proto": "dhcp", "vlan": "EXP"},
				}},
			},
		},
	}
}

// deviceOfBytes returns a template device whose JSON encoding is exactly
// size bytes long, by the length of its spec's description.
func deviceOfBytes(t *testing.T, size int) builder.TemplateDevice {
	t.Helper()

	device := builder.TemplateDevice{
		IconKey: "server",
		Spec: map[string]any{
			"general": map[string]any{"hostname": "big", "description": ""},
		},
	}

	empty, err := json.Marshal(device)
	if err != nil {
		t.Fatalf("encoding the device: %v", err)
	}

	device.Spec["general"].(map[string]any)["description"] = strings.Repeat("x", size-len(empty)) //nolint:forcetypeassert // set above

	encoded, err := json.Marshal(device)
	if err != nil || len(encoded) != size {
		t.Fatalf("the device encodes to %d bytes (%v), want %d", len(encoded), err, size)
	}

	return device
}

func TestTemplateIssuesAccepts(t *testing.T) {
	tests := map[string]func(*builder.Template){
		"every field": func(*builder.Template) {},
		"no description, icon or colors": func(tpl *builder.Template) {
			tpl.Description = ""
			tpl.Device = builder.TemplateDevice{Spec: tpl.Device.Spec}
		},
		// The id is the document's to check, and the custom icon is a name
		// a server's icon library resolves, whatever carries it.
		"an id that is no UUID":          func(tpl *builder.Template) { tpl.ID = "server" },
		"no id":                          func(tpl *builder.Template) { tpl.ID = "" },
		"a custom icon nothing carries":  func(tpl *builder.Template) { tpl.Device.Icon = "elsewhere" },
		"a custom icon name of 64 bytes": func(tpl *builder.Template) { tpl.Device.Icon = strings.Repeat("n", 64) },
		"a name of 128 bytes":            func(tpl *builder.Template) { tpl.Name = strings.Repeat("é", 64) },
		"a description of 1024 bytes":    func(tpl *builder.Template) { tpl.Description = strings.Repeat("d", 1024) },
		"a device of 16384 bytes":        func(tpl *builder.Template) { tpl.Device = deviceOfBytes(t, builder.MaxTemplateDeviceBytes) },
		// As for a device node, the spec is not checked against the phenix
		// schema: only that it names the device.
		"a spec that is only a hostname": func(tpl *builder.Template) {
			tpl.Device.Spec = map[string]any{"general": map[string]any{"hostname": "x"}}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			template := sampleTemplate()
			mutate(&template)

			if issues := template.Issues("templates[0]"); len(issues) != 0 {
				t.Fatalf("unexpected issues: %v", issues)
			}
		})
	}
}

func TestTemplateIssuesRefuses(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*builder.Template)
		wantPath string
		wantMsg  string
	}{
		{
			name:     "no name",
			mutate:   func(tpl *builder.Template) { tpl.Name = "" },
			wantPath: ".name",
			wantMsg:  "template name is required",
		},
		{
			name:     "a blank name",
			mutate:   func(tpl *builder.Template) { tpl.Name = "   " },
			wantPath: ".name",
			wantMsg:  "template name is required",
		},
		{
			name:     "a name of 129 bytes",
			mutate:   func(tpl *builder.Template) { tpl.Name = strings.Repeat("é", 64) + "x" },
			wantPath: ".name",
			wantMsg:  "template name must be at most 128 bytes",
		},
		{
			name:     "a name with a tab",
			mutate:   func(tpl *builder.Template) { tpl.Name = "PLC\tmodel" },
			wantPath: ".name",
			wantMsg:  "template name must not contain control characters",
		},
		{
			name:     "a description of 1025 bytes",
			mutate:   func(tpl *builder.Template) { tpl.Description = strings.Repeat("d", 1025) },
			wantPath: ".description",
			wantMsg:  "template description must be at most 1024 bytes",
		},
		{
			name:     "a description of two lines",
			mutate:   func(tpl *builder.Template) { tpl.Description = "one\ntwo" },
			wantPath: ".description",
			wantMsg:  "template description must not contain control characters",
		},
		{
			name:     "no spec",
			mutate:   func(tpl *builder.Template) { tpl.Device.Spec = nil },
			wantPath: ".device.spec",
			wantMsg:  "template device spec is required",
		},
		{
			name:     "no device",
			mutate:   func(tpl *builder.Template) { tpl.Device = builder.TemplateDevice{} },
			wantPath: ".device.spec",
			wantMsg:  "template device spec is required",
		},
		{
			name:     "a spec without a hostname",
			mutate:   func(tpl *builder.Template) { tpl.Device.Spec = map[string]any{"type": "VirtualMachine"} },
			wantPath: ".device.spec.general.hostname",
			wantMsg:  "template hostname is required",
		},
		{
			name: "a blank hostname",
			mutate: func(tpl *builder.Template) {
				tpl.Device.Spec["general"] = map[string]any{"hostname": " "}
			},
			wantPath: ".device.spec.general.hostname",
			wantMsg:  "template hostname is required",
		},
		{
			name: "a hostname that is not text",
			mutate: func(tpl *builder.Template) {
				tpl.Device.Spec["general"] = map[string]any{"hostname": 7}
			},
			wantPath: ".device.spec.general.hostname",
			wantMsg:  "template hostname is required",
		},
		{
			name: "a hostname with a space",
			mutate: func(tpl *builder.Template) {
				tpl.Device.Spec["general"] = map[string]any{"hostname": "plc one"}
			},
			wantPath: ".device.spec.general.hostname",
			wantMsg:  `template hostname "plc one" must not contain whitespace`,
		},
		{
			name:     "an unknown icon key",
			mutate:   func(tpl *builder.Template) { tpl.Device.IconKey = "toaster" },
			wantPath: ".device.iconKey",
			wantMsg:  `unknown icon key "toaster"`,
		},
		{
			name:     "an icon key that is a URL",
			mutate:   func(tpl *builder.Template) { tpl.Device.IconKey = "https://example.com/icon.svg" },
			wantPath: ".device.iconKey",
			wantMsg:  "must be one of the built-in keys, not a URL or path",
		},
		{
			name:     "a custom icon that is an icon id",
			mutate:   func(tpl *builder.Template) { tpl.Device.Icon = iconFixtureID },
			wantPath: ".device.icon",
			wantMsg:  `icon name "` + iconFixtureID[:64] + `..." must be 1 to 64 letters, digits, "_", "@", "." or "-"`,
		},
		{
			name:     "a custom icon name of 65 bytes",
			mutate:   func(tpl *builder.Template) { tpl.Device.Icon = strings.Repeat("n", 65) },
			wantPath: ".device.icon",
			wantMsg:  `must be 1 to 64 letters`,
		},
		{
			name:     "a custom icon named two dots",
			mutate:   func(tpl *builder.Template) { tpl.Device.Icon = ".." },
			wantPath: ".device.icon",
			wantMsg:  `icon name ".." must not be "." or ".."`,
		},
		{
			name:     "a named outline color",
			mutate:   func(tpl *builder.Template) { tpl.Device.OutlineColor = "red" },
			wantPath: ".device.outlineColor",
			wantMsg:  `color "red" must be a hex color such as #2f6fbf`,
		},
		{
			name:     "a short fill color",
			mutate:   func(tpl *builder.Template) { tpl.Device.FillColor = "#abc" },
			wantPath: ".device.fillColor",
			wantMsg:  `color "#abc" must be a hex color such as #2f6fbf`,
		},
		{
			name:     "a device of 16385 bytes",
			mutate:   func(tpl *builder.Template) { tpl.Device = deviceOfBytes(t, builder.MaxTemplateDeviceBytes+1) },
			wantPath: ".device",
			wantMsg:  "template device must take at most 16384 bytes as JSON, not 16385",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := sampleTemplate()
			test.mutate(&template)

			issues := template.Issues("library.templates[3]")
			if len(issues) != 1 {
				t.Fatalf("got %d issues, want 1: %v", len(issues), issues)
			}

			if want := "library.templates[3]" + test.wantPath; issues[0].Path != want {
				t.Fatalf("issue path = %q, want %q", issues[0].Path, want)
			}

			if !strings.Contains(issues[0].Message, test.wantMsg) {
				t.Fatalf("issue %q does not contain %q", issues[0].Message, test.wantMsg)
			}
		})
	}
}

// withTemplates returns the fixture document with the fixture icon and the
// given templates.
func withTemplates(t *testing.T, templates ...builder.Template) *builder.Document {
	t.Helper()

	doc := loadDocumentFixture(t, "document.json")
	doc.Icons = map[string]builder.Icon{iconFixtureName: {Data: iconFixtureData}}
	doc.Templates = templates

	return doc
}

func TestValidateTemplates(t *testing.T) {
	second := sampleTemplate()
	second.ID = idTemplate2

	if err := withTemplates(t, sampleTemplate(), second).Validate(); err != nil {
		t.Fatalf("two templates are refused: %v", err)
	}

	most := make([]builder.Template, builder.MaxTemplates)
	for i := range most {
		most[i] = sampleTemplate()
		most[i].ID = builder.DeviceNodeID(fmt.Sprintf("template-%d", i))
	}

	if err := withTemplates(t, most...).Validate(); err != nil {
		t.Fatalf("%d templates are refused: %v", len(most), err)
	}

	tests := []struct {
		name     string
		doc      func() *builder.Document
		wantPath string
		wantMsg  string
	}{
		{
			name: "one template too many",
			doc: func() *builder.Document {
				return withTemplates(t, slices.Concat(most, []builder.Template{sampleTemplate()})...)
			},
			wantPath: "templates",
			wantMsg:  "at most 50 templates are allowed, not 51",
		},
		{
			name: "an id that is no UUID",
			doc: func() *builder.Document {
				template := sampleTemplate()
				template.ID = "server"

				return withTemplates(t, template)
			},
			wantPath: "templates[0].id",
			wantMsg:  `template ID "server" is not a valid UUID`,
		},
		{
			name: "no id",
			doc: func() *builder.Document {
				template := sampleTemplate()
				template.ID = ""

				return withTemplates(t, template)
			},
			wantPath: "templates[0].id",
			wantMsg:  "template ID is required",
		},
		{
			name: "an id used twice, in another case",
			doc: func() *builder.Document {
				twin := sampleTemplate()
				twin.ID = strings.ToUpper(idTemplate)

				return withTemplates(t, sampleTemplate(), twin)
			},
			wantPath: "templates[1].id",
			wantMsg:  "duplicate template ID",
		},
		{
			// A node may have the id of a template: they are looked up apart.
			name: "what Template.Issues refuses",
			doc: func() *builder.Document {
				template := sampleTemplate()
				template.ID = idDevRouter
				template.Name = ""

				return withTemplates(t, template)
			},
			wantPath: "templates[0].name",
			wantMsg:  "template name is required",
		},
		{
			// Reported once, by Template.Issues.
			name: "a custom icon that is not an icon name",
			doc: func() *builder.Document {
				template := sampleTemplate()
				template.Device.Icon = "plc icon"

				return withTemplates(t, template)
			},
			wantPath: "templates[0].device.icon",
			wantMsg:  `icon name "plc icon" must be 1 to 64 letters, digits, "_", "@", "." or "-"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.doc().Validate()

			var invalid *builder.ValidationError

			if !errors.As(err, &invalid) {
				t.Fatalf("expected a *ValidationError, got %v", err)
			}

			if len(invalid.Issues) != 1 {
				t.Fatalf("got %d issues, want 1: %v", len(invalid.Issues), invalid.Issues)
			}

			if invalid.Issues[0].Path != test.wantPath {
				t.Fatalf("issue path = %q, want %q", invalid.Issues[0].Path, test.wantPath)
			}

			if !strings.Contains(invalid.Issues[0].Message, test.wantMsg) {
				t.Fatalf("issue %q does not contain %q", invalid.Issues[0].Message, test.wantMsg)
			}
		})
	}
}

// A decoded template spec holds whole numbers as int, as a device spec does,
// so a template compares equal to one made in Go.
func TestDecodeNormalizesTemplateSpecs(t *testing.T) {
	data, err := builder.Encode(withTemplates(t, sampleTemplate()))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	hardware, _ := decoded.Templates[0].Device.Spec["hardware"].(map[string]any)
	if vcpus, ok := hardware["vcpus"].(int); !ok || vcpus != 2 {
		t.Fatalf("vcpus = %#v, want the int 2", hardware["vcpus"])
	}

	if want := sampleTemplate(); !reflect.DeepEqual(decoded.Templates[0], want) {
		t.Fatalf("the decoded template differs:\nwant: %s\ngot:  %s", asJSON(t, want), asJSON(t, decoded.Templates[0]))
	}
}

// jsonFields returns the fields of a struct type by JSON name, each as its
// type and its whole json tag.
func jsonFields(t *testing.T, value any) map[string]string {
	t.Helper()

	typ := reflect.TypeOf(value)
	fields := make(map[string]string, typ.NumField())

	for i := range typ.NumField() {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")

		if name == "" || name == "-" {
			t.Fatalf("%s.%s has no JSON name", typ.Name(), field.Name)
		}

		fields[name] = fmt.Sprintf("%s %s `json:%q`", field.Name, field.Type, tag)
	}

	return fields
}

// A template fills in a device, so a field added to Device must be added to
// TemplateDevice too, with the same name, type and tag, or a template
// silently loses it. The front end derives its list the same way
// (TEMPLATE_DEVICE_KEYS in decode.js).
func TestTemplateDeviceFollowsDevice(t *testing.T) {
	want := jsonFields(t, builder.Device{})

	// What a device has of its own: its name, its canvas handles, and where
	// it was included from.
	for _, own := range []string{"hostname", "interfaces", "includedFrom"} {
		if _, ok := want[own]; !ok {
			t.Fatalf("Device has no %q field any more", own)
		}

		delete(want, own)
	}

	if got := jsonFields(t, builder.TemplateDevice{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("TemplateDevice does not follow Device:\nDevice (less its own): %v\nTemplateDevice:        %v", want, got)
	}

	for _, name := range []string{"iconKey", "icon", "outlineColor", "fillColor", "spec"} {
		if _, ok := want[name]; !ok {
			t.Fatalf("Device has no %q field", name)
		}
	}
}

func TestBuiltinTemplatesMatchFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "builtin-templates.json"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}

	var fixture struct {
		Templates []builder.Template `json:"templates"`
	}

	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}

	got := builder.BuiltinTemplates()

	if !reflect.DeepEqual(got, fixture.Templates) {
		t.Fatalf("BuiltinTemplates() differs from the fixture:\nfixture: %s\ngot:     %s",
			asJSON(t, fixture.Templates), asJSON(t, got))
	}

	// Also as each is written, since DeepEqual takes an absent key and an
	// empty one for the same.
	var raw struct {
		Templates []json.RawMessage `json:"templates"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}

	for i, template := range got {
		var want, have any

		encoded, err := json.Marshal(template)
		if err != nil {
			t.Fatalf("encoding %q: %v", template.ID, err)
		}

		if json.Unmarshal(raw.Templates[i], &want) != nil || json.Unmarshal(encoded, &have) != nil ||
			!reflect.DeepEqual(have, want) {
			t.Fatalf("built-in template %q encodes as %s, and the fixture has %s", template.ID, encoded, raw.Templates[i])
		}
	}
}

func TestBuiltinTemplates(t *testing.T) {
	templates := builder.BuiltinTemplates()

	ids := make([]string, len(templates))
	for i, template := range templates {
		ids[i] = template.ID

		if issues := template.Issues("builtin"); len(issues) != 0 {
			t.Fatalf("built-in template %q is not valid: %v", template.ID, issues)
		}

		// A device made from one is named after it.
		general, _ := template.Device.Spec["general"].(map[string]any)
		if general["hostname"] != template.ID {
			t.Fatalf("built-in template %q names its device %v", template.ID, general["hostname"])
		}

		// The template's description is its own: nothing copies it into
		// the node.
		if general["description"] != "" {
			t.Fatalf("built-in template %q writes the description %q", template.ID, general["description"])
		}

		if template.Device.IconKey == "" || !builder.IsIconKey(template.Device.IconKey) {
			t.Fatalf("built-in template %q has the icon key %q", template.ID, template.Device.IconKey)
		}
	}

	if got, want := strings.Join(ids, " "), "server workstation router firewall external"; got != want {
		t.Fatalf("built-in templates are %q, want %q", got, want)
	}

	firewall := templates[3].Device.Spec
	hardware, _ := firewall["hardware"].(map[string]any)
	drives, _ := hardware["drives"].([]any)

	if firewall["type"] != "Firewall" || hardware["os_type"] != "vyos" || len(drives) != 1 ||
		!reflect.DeepEqual(drives[0], map[string]any{"image": "vyos.qc2"}) {
		t.Fatalf("the Firewall template is %s", asJSON(t, firewall))
	}

	// Each call returns templates of its own.
	general, _ := templates[0].Device.Spec["general"].(map[string]any)
	general["hostname"] = "changed"
	templates[1].Name = "changed"

	if !reflect.DeepEqual(builder.BuiltinTemplates()[0].Device.Spec["general"], map[string]any{
		"hostname": "server", "description": "", "vm_type": "kvm",
	}) || builder.BuiltinTemplates()[1].Name != "Workstation" {
		t.Fatal("BuiltinTemplates returns shared state")
	}
}
