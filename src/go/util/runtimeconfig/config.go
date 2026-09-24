// Package runtimeconfig edits the phēnix runtime configuration file
// (config.yaml) in place, preserving comments, key order, and unrelated keys.
package runtimeconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	keyParts   = 2
	fileMode   = 0o600
	dirMode    = 0o750
	yamlIndent = 2
)

var (
	// ErrEmptyConfigPath is returned when no configuration file path is known.
	ErrEmptyConfigPath = errors.New("runtime configuration file path is empty")
	// ErrNotMapping is returned when the configuration file's top level is not
	// a YAML mapping.
	ErrNotMapping = errors.New("configuration file top level is not a mapping")
)

// Set stores value under the dotted key (for example "ui.default-theme") in
// the YAML file at configFile, creating the file and its directory when they
// do not exist. Comments, key order, and unrelated keys in an existing file
// are preserved. The file is replaced atomically so a concurrent reader, such
// as the viper watcher, never observes a partially written file.
func Set(configFile, key string, value any) error {
	if configFile == "" {
		return ErrEmptyConfigPath
	}

	data, err := os.ReadFile(configFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config file: %w", err)
	}

	doc, err := parseDocument(data)
	if err != nil {
		return err
	}

	valueNode := &yaml.Node{}
	if err := valueNode.Encode(value); err != nil {
		return fmt.Errorf("encoding value: %w", err)
	}

	setNested(doc.Content[0], key, valueNode)

	var buf bytes.Buffer

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(yamlIndent)

	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(configFile), dirMode); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	return writeAtomic(configFile, buf.Bytes())
}

// parseDocument returns the YAML document node for data, creating an empty
// mapping document when data is empty or holds only comments.
func parseDocument(data []byte) (*yaml.Node, error) {
	doc := &yaml.Node{}

	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, doc); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{newMapping()}
	}

	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, ErrNotMapping
	}

	return doc, nil
}

func newMapping() *yaml.Node {
	node := &yaml.Node{}
	node.Kind = yaml.MappingNode
	node.Tag = "!!map"

	return node
}

func newKey(name string) *yaml.Node {
	node := &yaml.Node{}
	node.Kind = yaml.ScalarNode
	node.Tag = "!!str"
	node.Value = name

	return node
}

// setNested stores value under the dotted key inside mapping, matching
// existing keys case-insensitively and appending new keys at the end so the
// surrounding document keeps its order and comments.
func setNested(mapping *yaml.Node, key string, value *yaml.Node) {
	// A mapping written in flow style (for example `{}`) would pull the new
	// keys onto one line; switch it to block style once it is edited.
	mapping.Style = 0

	parts := strings.SplitN(key, ".", keyParts)
	target := parts[0]

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		existing := mapping.Content[i]
		if !strings.EqualFold(existing.Value, target) {
			continue
		}

		if len(parts) == 1 {
			value.LineComment = mapping.Content[i+1].LineComment
			mapping.Content[i+1] = value

			return
		}

		next := mapping.Content[i+1]
		if next.Kind != yaml.MappingNode {
			next = newMapping()
			mapping.Content[i+1] = next
		}

		setNested(next, parts[1], value)

		return
	}

	if len(parts) == 1 {
		mapping.Content = append(mapping.Content, newKey(target), value)

		return
	}

	next := newMapping()
	mapping.Content = append(mapping.Content, newKey(target), next)

	setNested(next, parts[1], value)
}

// writeAtomic writes data to a temporary file next to path and renames it over
// path, keeping the existing file's permissions when it already exists.
func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(fileMode)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temporary config file: %w", err)
	}

	tmpName := tmp.Name()

	defer func() { _ = os.Remove(tmpName) }()

	if err := writeAndClose(tmp, data, mode); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing config file: %w", err)
	}

	return nil
}

func writeAndClose(file *os.File, data []byte, mode os.FileMode) error {
	defer func() { _ = file.Close() }()

	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("setting config file permissions: %w", err)
	}

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}
