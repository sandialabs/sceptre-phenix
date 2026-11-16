package runtimesettings

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNotYAML is the error for a config file that phenix cannot rewrite.
var ErrNotYAML = errors.New("phenix rewrites only YAML config files")

// CheckYAMLFile returns an error that wraps ErrNotYAML unless path names a
// YAML file, which SetInFile and UnsetInFile can rewrite.
func CheckYAMLFile(path string) error {
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
		return nil
	}

	return fmt.Errorf("%w, so edit %s by hand", ErrNotYAML, path)
}

// ErrTextNotYAML is the error for text that yaml.v3 writes as YAML that reads
// back as other text, or that does not parse, such as text that starts with a
// newline or has a tab before a newline.
var ErrTextNotYAML = errors.New("phenix cannot write this text to a YAML config file")

// SetInFile sets the dotted key to val in the YAML config file path, creating the
// file and its directory when they do not exist. It changes only the YAML
// node of the key, so comments, key order and the other keys stay.
func SetInFile(path, key string, val any) error {
	if err := CheckYAMLFile(path); err != nil {
		return err
	}

	texts, _ := val.([]string)
	if text, ok := val.(string); ok {
		texts = []string{text}
	}

	for _, text := range texts {
		var back map[string]string

		data, err := yaml.Marshal(map[string]string{"v": text})
		if err != nil || yaml.Unmarshal(data, &back) != nil || back["v"] != text {
			return fmt.Errorf("%w, so edit %s by hand: %q", ErrTextNotYAML, path, text)
		}
	}

	var node yaml.Node
	if err := node.Encode(val); err != nil {
		return fmt.Errorf("encoding config value: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config file: %w", err)
	}

	doc, err := parseConfigDoc(data)
	if err != nil {
		return err
	}

	setConfigNode(doc.root, strings.Split(key, "."), &node)

	return doc.write(path)
}

// configDoc is a YAML config file that phenix settings set and unset change
// in place.
type configDoc struct {
	data []byte     // the file as it was
	doc  *yaml.Node // the document node, or nil when data has no YAML content
	root *yaml.Node // the mapping at the top of the file
}

// errConfigNotMapping is the error for a config file that is not a YAML mapping.
var errConfigNotMapping = errors.New("config file is not a YAML mapping of settings")

// parseConfigDoc parses the YAML config file data. Data with no YAML content,
// such as only comments, or a null document, gives an empty mapping.
func parseConfigDoc(data []byte) (*configDoc, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if doc.Kind == 0 || len(doc.Content) == 0 {
		return &configDoc{data: data, doc: nil, root: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}}, nil
	}

	top := doc.Content[0]

	switch {
	case top.Kind == yaml.MappingNode:
	case top.Kind == yaml.ScalarNode && top.Tag == "!!null":
		doc.Content[0] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keepComments(doc.Content[0], top)
	default:
		return nil, fmt.Errorf("parsing config file: %w", errConfigNotMapping)
	}

	return &configDoc{data: data, doc: &doc, root: doc.Content[0]}, nil
}

// write writes the config to path. A file with no YAML content keeps its
// text, such as its comments, before the new settings.
func (c *configDoc) write(path string) error {
	var buf bytes.Buffer

	node := c.doc
	if node == nil {
		node = c.root

		if len(bytes.TrimSpace(c.data)) > 0 {
			buf.Write(c.data)

			if !bytes.HasSuffix(c.data, []byte("\n")) {
				buf.WriteByte('\n')
			}
		}
	}

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(configIndent(c.root))

	if err := enc.Encode(node); err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := enc.Close(); err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

const (
	defaultConfigIndent = 4 // the indent of yaml.Marshal
	maxConfigIndent     = 9 // the largest indent yaml.v3 writes
	seqItemOffset       = 2 // the "- " before a sequence item
	nodePair            = 2 // a key node and its value node in a mapping
)

// configIndent returns the indent of the first nested block mapping or
// sequence in the mapping m, or defaultConfigIndent when it has none.
func configIndent(m *yaml.Node) int {
	for i := 0; i+1 < len(m.Content); i += nodePair {
		key, value := m.Content[i], m.Content[i+1]

		if value.Style&yaml.FlowStyle != 0 || len(value.Content) == 0 {
			continue
		}

		indent := 0

		switch value.Kind { //nolint:exhaustive // only block collections show the indent
		case yaml.MappingNode:
			indent = value.Content[0].Column - key.Column
		case yaml.SequenceNode:
			indent = value.Content[0].Column - key.Column - seqItemOffset
		}

		if indent >= seqItemOffset && indent <= maxConfigIndent {
			return indent
		}

		if value.Kind == yaml.MappingNode {
			if nested := configIndent(value); nested != defaultConfigIndent {
				return nested
			}
		}
	}

	return defaultConfigIndent
}

// configKeyIndex returns the index in m.Content of the key that matches key
// without regard to case, or -1 when m has no such key.
func configKeyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += nodePair {
		if m.Content[i].Kind == yaml.ScalarNode && strings.EqualFold(m.Content[i].Value, key) {
			return i
		}
	}

	return -1
}

// copyConfigNode returns a deep copy of n without anchors, so a change to
// the copy of an alias does not change the anchor.
func copyConfigNode(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return copyConfigNode(n.Alias)
	}

	c := *n
	c.Anchor = ""
	c.Content = make([]*yaml.Node, len(n.Content))

	for i, child := range n.Content {
		c.Content[i] = copyConfigNode(child)
	}

	return &c
}

// keepComments gives to the comments of from, the node that to replaces.
func keepComments(to, from *yaml.Node) {
	to.HeadComment, to.LineComment, to.FootComment = from.HeadComment, from.LineComment, from.FootComment
}

// setConfigNode sets the value at the dotted key parts in the mapping m to
// value. It appends a missing key at the end of its mapping, and replaces a
// value on the path that is not a mapping with a mapping. An empty mapping
// on the path, such as "{}", becomes a block mapping.
func setConfigNode(m *yaml.Node, parts []string, value *yaml.Node) {
	for {
		if len(m.Content) == 0 {
			m.Style &^= yaml.FlowStyle
		}

		i := configKeyIndex(m, parts[0])

		if i < 0 {
			key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: parts[0]}
			next := value

			if len(parts) > 1 {
				next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}

			m.Content = append(m.Content, key, next)
			i = len(m.Content) - nodePair
		}

		old := m.Content[i+1]

		if len(parts) == 1 {
			if old != value {
				keepComments(value, old)
				m.Content[i+1] = value
			}

			return
		}

		next := old
		if next.Kind == yaml.AliasNode && next.Alias != nil && next.Alias.Kind == yaml.MappingNode {
			next = copyConfigNode(next)
			keepComments(next, old)
		}

		if next.Kind != yaml.MappingNode {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			keepComments(next, old)
		}

		m.Content[i+1] = next
		m, parts = next, parts[1:]
	}
}

// unsetConfigNode removes the dotted key parts from the mapping m, and each
// mapping that the removal leaves empty. It reports whether m had the key.
func unsetConfigNode(m *yaml.Node, parts []string) bool {
	i := configKeyIndex(m, parts[0])
	if i < 0 {
		return false
	}

	if len(parts) > 1 {
		old := m.Content[i+1]

		next := old
		if next.Kind == yaml.AliasNode && next.Alias != nil {
			next = copyConfigNode(next)
			keepComments(next, old)
		}

		if next.Kind != yaml.MappingNode || !unsetConfigNode(next, parts[1:]) {
			return false
		}

		m.Content[i+1] = next

		if len(next.Content) > 0 {
			return true
		}
	}

	m.Content = slices.Delete(m.Content, i, i+nodePair)

	return true
}

// UnsetInFile removes the dotted key from the YAML config file path, and each
// mapping that the removal leaves empty. Comments, key order and the other
// keys stay. It reports whether the file had the key; a file without it does
// not change. A file that does not exist returns an error that wraps
// [os.ErrNotExist].
func UnsetInFile(path, key string) (bool, error) {
	return unsetInFile(path, key, true)
}

// HasInFile reports whether the YAML config file path has the dotted key, as
// UnsetInFile would find it, and changes nothing. It returns the errors of
// UnsetInFile.
func HasInFile(path, key string) (bool, error) {
	return unsetInFile(path, key, false)
}

// unsetInFile removes key from the config file path, and writes the file
// only when write is set. It reports whether the file had the key.
func unsetInFile(path, key string, write bool) (bool, error) {
	if err := CheckYAMLFile(path); err != nil {
		return false, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading config file: %w", err)
	}

	doc, err := parseConfigDoc(data)
	if err != nil {
		return false, err
	}

	if !unsetConfigNode(doc.root, strings.Split(key, ".")) {
		return false, nil
	}

	if !write {
		return true, nil
	}

	return true, doc.write(path)
}
