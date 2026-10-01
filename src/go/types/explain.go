package types

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// schemaLeaf is one validation error to explain: err, the schema error in it
// (nil when there is none) and prefix, the pointer of the allOf errors around
// it, whose parts report pointers relative to the allOf value.
type schemaLeaf struct {
	err    error
	se     *openapi3.SchemaError
	prefix []string
}

// raw is the leaf's text as the validator reported it.
func (l schemaLeaf) raw() string {
	if len(l.prefix) == 0 {
		return l.err.Error()
	}

	return `Error at "/` + strings.Join(l.prefix, "/") + `": ` + l.err.Error()
}

// step is one JSON pointer segment resolved in the source: the value it
// names, the line of its mapping key or list item, and whether it is a list
// index.
type step struct {
	node  *yaml.Node
	line  int
	index bool
}

// ExplainValidationError rewrites a config validation error as readable lines,
// one per schema error, located in src, the JSON or YAML document the config
// was parsed from. For example:
//
//	nodes[1] "ADServer" (line 21): property "image" is missing (at hardware.drives[0].image)
//	  hint: "image:" is on line 19 under nodes[1].hardware, but the schema expects it at nodes[1].hardware.drives[0].image
//
// A line names the list item or top-level key that holds the error, labels a
// list item with its general.hostname or name, gives the source line when
// src spans more than one, then the validator's reason and the path inside
// the item. A missing key found elsewhere in the item adds a "  hint:" line.
// An error whose pointer does not resolve in src keeps its original text, so
// no error is hidden; identical lines appear once; characters that are not
// printable are written in their Go escape form so a line stays one line.
func ExplainValidationError(src []byte, err error) []string {
	if err == nil {
		return nil
	}

	var (
		root      = documentRoot(src)
		multiline = bytes.ContainsRune(bytes.TrimSpace(src), '\n')
		leaves    = schemaLeaves(err, nil)
		lines     = make([]string, 0, len(leaves))
		seen      = make(map[string]bool, len(leaves))
	)

	for _, leaf := range leaves {
		group := explainLeaf(root, multiline, leaf)

		if seen[group[0]] {
			continue
		}

		seen[group[0]] = true

		for _, line := range group {
			lines = append(lines, escapeNonPrintable(line))
		}
	}

	return lines
}

// escapeNonPrintable writes each rune of s that [unicode.IsPrint] rejects in
// its Go escape form, such as \n or \x1b.
func escapeNonPrintable(s string) string {
	notPrintable := func(r rune) bool { return !unicode.IsPrint(r) }
	if strings.IndexFunc(s, notPrintable) < 0 {
		return s
	}

	var b strings.Builder

	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)

		if notPrintable(r) {
			quoted := strconv.QuoteRune(r)
			b.WriteString(quoted[1 : len(quoted)-1])
		} else {
			b.WriteString(s[:size])
		}

		s = s[size:]
	}

	return b.String()
}

// schemaLeaves splits err into the single errors it reports: each failing
// branch of a oneOf and the failing part of an allOf.
func schemaLeaves(err error, prefix []string) []schemaLeaf {
	var se *openapi3.SchemaError
	if !errors.As(err, &se) {
		return []schemaLeaf{{err: err, se: nil, prefix: prefix}}
	}

	// A multi-error holding se lists the failing branches of a oneOf.
	var (
		multi openapi3.MultiError
		first *openapi3.SchemaError
	)

	if errors.As(err, &multi) && errors.As(multi, &first) && errors.Is(first, se) {
		leaves := make([]schemaLeaf, 0, len(multi))
		for _, e := range multi {
			leaves = append(leaves, schemaLeaves(e, prefix)...)
		}

		return leaves
	}

	// An allOf error's cause is its failing part's error.
	var part *openapi3.SchemaError
	if errors.As(se.Origin, &part) {
		return schemaLeaves(se.Origin, slices.Concat(prefix, se.JSONPointer()))
	}

	return []schemaLeaf{{err: err, se: se, prefix: prefix}}
}

// documentRoot returns the top-level node of src, or nil when src does not
// parse or is empty. JSON parses as YAML.
func documentRoot(src []byte) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}

	return doc.Content[0]
}

// explainLeaf explains one error as an error line and, for a missing key found
// elsewhere, a hint line. It keeps the error's text when its pointer cannot
// be placed in the source.
func explainLeaf(root *yaml.Node, multiline bool, leaf schemaLeaf) []string {
	se := leaf.se
	if root == nil || se == nil {
		return []string{leaf.raw()}
	}

	own := se.JSONPointer()
	what := strings.TrimPrefix(se.Error(), `Error at "/`+strings.Join(own, "/")+`": `)

	// A kind's spec schema reports pointers relative to spec.
	path := slices.Concat(leaf.prefix, own)
	if !fromRoot(path, se.Schema) {
		path = slices.Concat([]string{"spec"}, path)
	}

	// A missing key is not in the source, so its line is its parent's; an
	// unsupported key is named only by the reason.
	missing := se.SchemaField == "required"
	locate := path

	if missing {
		locate = path[:len(path)-1]
	}

	if se.SchemaField == "properties" {
		key, _ := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(se.Reason, "property "), " is unsupported"))
		path = append(path, key)
		locate = path
	}

	steps, ok := walk(root, locate)
	if !ok {
		return []string{leaf.raw()}
	}

	return describe(root, path, steps, what, missing, multiline)
}

// fromRoot reports whether path, a schema error's pointer, starts at the
// document root rather than at spec: when it starts with a document key, or
// is empty and the failing schema is a whole-document one, which declares
// apiVersion.
func fromRoot(path []string, schema *openapi3.Schema) bool {
	if len(path) == 0 {
		return schema != nil && schema.Properties["apiVersion"] != nil
	}

	return slices.Contains([]string{"apiVersion", "kind", "metadata", "spec", "status"}, path[0])
}

// describe writes the error line for an error at path, which steps resolve in
// full or up to a missing key, then its hint line, if any.
func describe(root *yaml.Node, path []string, steps []step, what string, missing, multiline bool) []string {
	line := root.Line
	if len(steps) > 0 {
		line = steps[len(steps)-1].line
	}

	// The item is the first list element on the path, named without the
	// document key (nodes[1], not spec.nodes[1]), else the top-level key, else
	// config. path[from:] is the path inside it and path[shown:] what a hint
	// shows.
	var (
		item  = "config"
		label string
		scope = root
		trail string
		from  int
		shown int
	)

	first := slices.IndexFunc(steps, func(s step) bool { return s.index })

	switch {
	case first >= 0:
		shown, from = 1, first+1
		item = render(path[shown:from], steps[shown:from])
		label, scope, trail = itemLabel(steps[first].node), steps[first].node, item
	case len(steps) > 0:
		from = 1
		item, scope, trail = path[0], steps[0].node, path[0]
	}

	head := item
	if label != "" {
		head += " " + strconv.Quote(label)
	}

	if multiline {
		head += fmt.Sprintf(" (line %d)", line)
	}

	head += ": " + what

	if sub := render(path[from:], steps[from:]); sub != "" {
		head += " (at " + sub + ")"
	}

	if !missing {
		return []string{head}
	}

	hint, found := misplacedHint(scope, path[from:], trail, render(path[shown:], steps[shown:]), multiline)
	if !found {
		return []string{head}
	}

	return []string{head, hint}
}

// walk resolves path from root, one step per segment.
func walk(root *yaml.Node, path []string) ([]step, bool) {
	steps := make([]step, 0, len(path))
	node := root

	for _, seg := range path {
		st, ok := child(node, seg)
		if !ok {
			return nil, false
		}

		steps = append(steps, st)
		node = st.node
	}

	return steps, true
}

// child resolves one segment under node: a mapping key or a list index. A
// repeated JSON key resolves to its last value, the one encoding/json keeps.
func child(node *yaml.Node, seg string) (step, bool) {
	node = deref(node)

	if node.Kind == yaml.MappingNode {
		var (
			last  step
			found bool
		)

		for i := 0; i+1 < len(node.Content); i += 2 {
			if key := node.Content[i]; key.Value == seg {
				last, found = step{node: node.Content[i+1], line: key.Line, index: false}, true
			}
		}

		if found {
			return last, true
		}
	}

	if node.Kind == yaml.SequenceNode {
		if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(node.Content) {
			item := node.Content[i]

			return step{node: item, line: item.Line, index: true}, true
		}
	}

	return step{}, false
}

// deref follows YAML aliases to the node they name.
func deref(node *yaml.Node) *yaml.Node {
	for node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	return node
}

// itemLabel names a list item by its general.hostname, else its name.
func itemLabel(item *yaml.Node) string {
	for _, path := range [][]string{{"general", "hostname"}, {"name"}} {
		if steps, ok := walk(item, path); ok {
			if value := deref(steps[len(steps)-1].node).Value; value != "" {
				return value
			}
		}
	}

	return ""
}

// render formats pointer segments as a dotted path with list indexes in
// brackets, such as hardware.drives[0].image.
func render(path []string, steps []step) string {
	var b strings.Builder

	for i, seg := range path {
		switch {
		case i < len(steps) && steps[i].index:
			b.WriteString("[" + seg + "]")
		case b.Len() > 0:
			b.WriteString("." + seg)
		default:
			b.WriteString(seg)
		}
	}

	return b.String()
}

// misplacedHint looks under scope for the missing key that ends want, the
// path below scope where the schema expects it. It finds nothing when the key
// already sits where it belongs, as an empty spec does.
func misplacedHint(scope *yaml.Node, want []string, trail, expected string, multiline bool) (string, bool) {
	key := want[len(want)-1]

	line, parent, ok := findKey(scope, key, want, trail, make(map[*yaml.Node]bool))
	if !ok || joinKey(parent, key) == expected {
		return "", false
	}

	on := ""
	if multiline {
		on = fmt.Sprintf(" on line %d", line)
	}

	return fmt.Sprintf("  hint: %q is%s under %s, but the schema expects it at %s", key+":", on, parent, expected), true
}

// findKey returns the line of the first mapping key named key under node and
// the rendered path of the mapping holding it. List items off the expected
// path want are skipped, so a sibling's key is not reported. seen keeps YAML
// aliases, which can reach a node twice or make it hold itself, from being
// searched again.
func findKey(node *yaml.Node, key string, want []string, at string, seen map[*yaml.Node]bool) (int, string, bool) {
	node = deref(node)

	if len(want) == 0 {
		if seen[node] {
			return 0, "", false
		}

		seen[node] = true
	}

	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			name := node.Content[i]
			if name.Value == key {
				return name.Line, at, true
			}

			if line, parent, ok := findKey(node.Content[i+1], key, onPath(want, name.Value), joinKey(at, name.Value), seen); ok {
				return line, parent, true
			}
		}
	}

	if node.Kind == yaml.SequenceNode {
		for i, item := range node.Content {
			index := strconv.Itoa(i)
			if len(want) > 0 && want[0] != index {
				continue
			}

			if line, parent, ok := findKey(item, key, onPath(want, index), at+"["+index+"]", seen); ok {
				return line, parent, true
			}
		}
	}

	return 0, "", false
}

// onPath returns the rest of want below seg, or nil off the expected path.
func onPath(want []string, seg string) []string {
	if len(want) > 0 && want[0] == seg {
		return want[1:]
	}

	return nil
}

// joinKey appends a mapping key to a rendered path.
func joinKey(at, key string) string {
	if at == "" {
		return key
	}

	return at + "." + key
}
