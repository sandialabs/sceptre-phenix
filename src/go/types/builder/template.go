package builder

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Bounds on device templates. The API layer bounds the other counts and
// sizes of a diagram. [Document.Validate] checks these bounds, as the editor
// does. A template is made in the editor, so the editor must tell which
// template is too large. The server must not be the first to refuse the
// save.
const (
	// MaxTemplates is the most templates a document may carry (see
	// [Document.Templates]).
	MaxTemplates = 50

	// MaxTemplateNameBytes bounds a template's name.
	MaxTemplateNameBytes = 128

	// MaxTemplateDescriptionBytes bounds a template's description.
	MaxTemplateDescriptionBytes = 1024

	// MaxTemplateDeviceBytes bounds the JSON encoding of a template's
	// device (16 KiB).
	MaxTemplateDeviceBytes = 16 << 10
)

// The keys of a node spec that hold the hostname a device made from a
// template is named after.
const (
	specGeneral  = "general"
	specHostname = "hostname"
)

// Template is a named set of prefilled fields for a device node. A device
// made from one is an ordinary device that keeps no link to it.
type Template struct {
	// ID identifies the template among those it is kept with. In a
	// document it is a UUID.
	ID string `json:"id"`
	// Name is what the editor's palette lists the template as.
	Name string `json:"name"`
	// Description is the template's own text, which the palette shows. It
	// is never written into a node, whose description is in the spec.
	Description string         `json:"description,omitempty"`
	Device      TemplateDevice `json:"device"`
}

// TemplateDevice is what a template fills in: every field of a [Device]
// other than its hostname, its interface handles and where it was included
// from, with the same names. The device's hostname comes from the spec.
type TemplateDevice struct {
	IconKey      string `json:"iconKey,omitempty"`
	Icon         string `json:"icon,omitempty"`
	IconSize     string `json:"iconSize,omitempty"`
	OutlineColor string `json:"outlineColor,omitempty"`
	FillColor    string `json:"fillColor,omitempty"`
	PurdueLevel  string `json:"purdueLevel,omitempty"`
	// Spec is a complete phenix node spec, as a device holds it. Its
	// general.hostname is what a device made from the template is named
	// after, and its interfaces are kept.
	Spec map[string]any `json:"spec"`
}

// Issues returns what makes the template unusable, with paths under path:
//
//   - a name that is blank, longer than [MaxTemplateNameBytes] or holds
//     control characters,
//   - a description longer than [MaxTemplateDescriptionBytes] or holding
//     control characters,
//   - a device without a spec, or whose spec has no general.hostname, a
//     blank one or one with whitespace,
//   - an icon key outside the icon key registry (see [IsIconKey]), a custom
//     icon that is not an icon name (see [IconNameProblem]), an icon size
//     outside [IconSizes], a color that is not "#rrggbb", or a Purdue
//     level outside [PurdueLevels],
//   - a device whose JSON encoding is longer than [MaxTemplateDeviceBytes].
//
// The spec is not checked against the phenix schema, as a device's is not.
// Neither is the id, whose form depends on where the template is kept:
// [Document.Validate] checks it for a document's templates. The custom icon
// names an icon of the server's icon library, or one a document carries.
// Each issue carries the code of the rule it breaks.
func (t *Template) Issues(path string) []Issue {
	var issues []Issue

	addf := func(code Code, at, format string, args ...any) {
		issues = append(issues, NewIssue(code, path+at, fmt.Sprintf(format, args...)))
	}

	switch {
	case strings.TrimSpace(t.Name) == "":
		addf(CodeTemplateNameRequired, ".name", "template name is required")
	case len(t.Name) > MaxTemplateNameBytes:
		addf(CodeTemplateNameTooLong, ".name", "template name must be at most %d bytes", MaxTemplateNameBytes)
	case strings.ContainsFunc(t.Name, isControl):
		addf(CodeTemplateNameControl, ".name", "template name must not contain control characters")
	}

	switch {
	case len(t.Description) > MaxTemplateDescriptionBytes:
		addf(
			CodeTemplateDescriptionTooLong, ".description",
			"template description must be at most %d bytes", MaxTemplateDescriptionBytes,
		)
	case strings.ContainsFunc(t.Description, isControl):
		addf(CodeTemplateDescriptionControl, ".description", "template description must not contain control characters")
	}

	device := &t.Device

	if device.Spec == nil {
		addf(CodeTemplateSpecRequired, ".device.spec", "template device spec is required")
	} else {
		hostname := specString(device.Spec, specGeneral, specHostname)

		switch {
		case strings.TrimSpace(hostname) == "":
			addf(CodeTemplateHostnameRequired, ".device.spec.general.hostname", "template hostname is required")
		case strings.ContainsAny(hostname, " \t\n"):
			addf(
				CodeTemplateHostnameWhitespace, ".device.spec.general.hostname",
				"template hostname %q must not contain whitespace", truncate(hostname),
			)
		}
	}

	if problem := iconKeyProblem(device.IconKey); problem != "" {
		addf(CodeTemplateIconKeyUnknown, ".device.iconKey", "%s", problem)
	}

	if device.Icon != "" {
		if problem := IconNameProblem(device.Icon); problem != "" {
			addf(CodeTemplateIconInvalid, ".device.icon", "%s", problem)
		}
	}

	if problem := iconSizeProblem(device.IconSize); problem != "" {
		addf(CodeTemplateIconSizeUnknown, ".device.iconSize", "%s", problem)
	}

	if problem := colorProblem(device.OutlineColor); problem != "" {
		addf(CodeTemplateColorInvalid, ".device.outlineColor", "%s", problem)
	}

	if problem := colorProblem(device.FillColor); problem != "" {
		addf(CodeTemplateColorInvalid, ".device.fillColor", "%s", problem)
	}

	if problem := purdueLevelProblem(device.PurdueLevel); problem != "" {
		addf(CodeTemplatePurdueLevelUnknown, ".device.purdueLevel", "%s", problem)
	}

	encoded, err := json.Marshal(device)

	switch {
	case err != nil:
		addf(CodeTemplateDeviceUnencodable, ".device", "template device is not encodable: %v", err)
	case len(encoded) > MaxTemplateDeviceBytes:
		addf(
			CodeTemplateDeviceTooLarge, ".device",
			"template device must take at most %d bytes as JSON, not %d", MaxTemplateDeviceBytes, len(encoded),
		)
	}

	return issues
}

// normalize makes the device spec of the template canonical, as for a device
// node (see normalizeDocument).
func (t *Template) normalize() error {
	if t.Device.Spec == nil {
		return nil
	}

	spec, err := normalizeSpecMap(t.Device.Spec)
	if err != nil {
		return err
	}

	t.Device.Spec = spec

	return nil
}

// BuiltinTemplates returns the templates every template library starts
// with, each call a copy of its own. Their ids are short names, not UUIDs:
// they are a library's, never a document's.
//
// The spec of each is what the editor writes for a new device of that kind,
// named after the template's id. The vrouter app of phenix configures
// routing and rulesets only on nodes of type Router or Firewall, and only
// through their router OS types (minirouter, vyatta or vyos). Thus the
// Router and Firewall templates give both the type and the OS type.
func BuiltinTemplates() []Template {
	return []Template{
		builtinTemplate("server", "Server", "Generic Linux server", IconServer,
			kvmSpec("VirtualMachine", "linux", "ubuntu.qc2")),
		builtinTemplate("workstation", "Workstation", "Operator workstation", "desktop",
			kvmSpec("VirtualMachine", "windows", "windows10.qc2")),
		builtinTemplate("router", "Router", "Layer 3 router", iconRouter,
			kvmSpec("Router", "minirouter", "minirouter.qc2")),
		builtinTemplate("firewall", "Firewall", "Perimeter firewall", "firewall",
			kvmSpec("Firewall", "vyos", "vyos.qc2")),
		builtinTemplate("external", "External device", "Hardware in the loop device", iconExternal,
			map[string]any{"external": true, keyType: "HIL", specGeneral: map[string]any{}}),
	}
}

// builtinTemplate builds a built-in template from the part of a spec that
// is its kind's own. Its device is named after its id, has no description
// (the template's is never written into a node) and no interfaces.
func builtinTemplate(id, name, description, iconKey string, spec map[string]any) Template {
	if general, ok := spec[specGeneral].(map[string]any); ok {
		general[specHostname] = id
		general[keyDescription] = ""
	}

	spec["network"] = map[string]any{"interfaces": []any{}}

	return Template{
		ID:          id,
		Name:        name,
		Description: description,
		Device: TemplateDevice{
			IconKey: iconKey, Icon: "", IconSize: "", OutlineColor: "", FillColor: "", PurdueLevel: "",
			Spec: spec,
		},
	}
}

// kvmSpec returns what a spec says of a KVM node of the given type, running
// the given OS from the given disk image.
func kvmSpec(nodeType, osType, image string) map[string]any {
	return map[string]any{
		keyType:     nodeType,
		specGeneral: map[string]any{"vm_type": "kvm"},
		"hardware": map[string]any{
			"os_type": osType,
			"drives":  []any{map[string]any{"image": image}},
		},
	}
}
