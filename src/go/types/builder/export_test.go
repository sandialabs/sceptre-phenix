package builder

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"gopkg.in/yaml.v3"
)

// IconKeyForSpec exposes iconKeyForSpec to the external tests.
func IconKeyForSpec(spec map[string]any) string {
	return iconKeyForSpec(spec)
}

// FoldKey exposes foldKey to the external tests.
func FoldKey(value string) string {
	return foldKey(value)
}

// The functions below are used only by tests.

// NetworkByName returns the network with the given name, or nil. Names are
// matched exactly, as minimega matches VLAN names.
func (d *Document) NetworkByName(name string) *Network {
	for i := range d.Networks {
		if d.Networks[i].Name == name {
			return &d.Networks[i]
		}
	}

	return nil
}

// DeviceNodes returns all device nodes in document order.
func (d *Document) DeviceNodes() []*Node {
	return d.nodesOfKind(NodeKindDevice)
}

// SwitchNodes returns all switch nodes in document order.
func (d *Document) SwitchNodes() []*Node {
	return d.nodesOfKind(NodeKindSwitch)
}

func (d *Document) nodesOfKind(kind NodeKind) []*Node {
	var nodes []*Node

	for i := range d.Nodes {
		if d.Nodes[i].Kind == kind {
			nodes = append(nodes, &d.Nodes[i])
		}
	}

	return nodes
}

// IconKeys returns the sorted registry of icon keys a device may declare.
func IconKeys() []string {
	return slices.Clone(iconKeys)
}

// Cells returns how many cells the diagram holds.
func (d *LegacyDiagram) Cells() int {
	return len(d.cells)
}

// Codes returns every code, in the order of the registry: grouped by what
// each is about.
func Codes() []CodeInfo {
	return slices.Clone(codeRegistry)
}

// codeEntry is what [CodesJSON] says of one code.
type codeEntry struct {
	Severity    Severity `json:"severity"`
	Description string   `json:"description"`
}

// CodesJSON returns the registry as the web UI bundles it
// (src/js/src/builder/schema/codes.json): an object of each code's default
// severity and description by code, keys sorted, indented by two spaces,
// with a final newline.
func CodesJSON() ([]byte, error) {
	entries := make(map[Code]codeEntry, len(codeRegistry))

	for _, info := range codeRegistry {
		entries[info.Code] = codeEntry{Severity: info.Severity, Description: info.Description}
	}

	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the code registry: %w", err)
	}

	return append(encoded, '\n'), nil
}

// IsNameSpace exposes isNameSpace to the external tests.
func IsNameSpace(r rune) bool {
	return isNameSpace(r)
}

// LegacyName exposes legacyName to the external tests.
func LegacyName(text string) string {
	return legacyName(text)
}

// NestedRepeat exposes nestedRepeat to the external tests.
func NestedRepeat(pattern string) bool {
	return nestedRepeat(pattern)
}

// UnboundedRepeats exposes unboundedRepeats to the external tests.
func UnboundedRepeats(pattern string) int {
	return unboundedRepeats(pattern)
}

// Truncate exposes truncate to the external tests.
func Truncate(value string) string {
	return truncate(value)
}

// YAMLValue exposes yamlValue to the external tests.
func YAMLValue(node *yaml.Node) (any, error) {
	return yamlValue(node)
}

// YAMLKey exposes yamlKey to the external tests.
func YAMLKey(node *yaml.Node) (string, error) {
	return yamlKey(node)
}

// DecodeProblem is a decodeProblem with exported fields.
type DecodeProblem struct {
	Path    string
	Key     string
	Unknown bool
}

// WalkDecode decodes data as JSON and returns the problems decodeWalker finds
// in it for the Go type target.
func WalkDecode(target reflect.Type, data []byte) ([]DecodeProblem, error) {
	var value any

	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decoding the walked JSON: %w", err)
	}

	var walker decodeWalker

	walker.walk(target, value, "", "", "")

	problems := make([]DecodeProblem, 0, len(walker.problems))

	for _, problem := range walker.problems {
		problems = append(problems, DecodeProblem{Path: problem.path, Key: problem.key, Unknown: problem.unknown})
	}

	return problems, nil
}

// SpecSetString exposes specSetString to the external tests.
func SpecSetString(spec map[string]any, value string, keys ...string) bool {
	return specSetString(spec, value, keys...)
}

// ShapeFieldNames returns the sorted keys of the fields that shapeOf finds in
// the struct type typ.
func ShapeFieldNames(typ reflect.Type) []string {
	return slices.Sorted(maps.Keys(shapeOf(typ, reflect.Value{}).fields))
}

// AddReferencedDefs exposes addReferencedDefs to the external tests.
func AddReferencedDefs(defs, pool map[string]any, value any) error {
	return addReferencedDefs(defs, pool, value)
}
