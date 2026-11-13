package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// formatText is the output format of a command whose default output is a
// report or a line of text, not a table, such as phenix doctor.
const formatText = "text"

// outputFileFlag is the flag of a command that writes data to a file in
// place of stdout: -O/--output-file. -o/--output always names an output
// format.
const outputFileFlag = "output-file"

// tableJSONYAML are the output formats of a command that shows data that the
// API also gives: a table, and the JSON of the API, also as YAML.
func tableJSONYAML() []string {
	return []string{formatTable, FormatJSON, FormatYAML}
}

// printAPI writes data as print does, but its YAML has the keys and values of
// its JSON: the names of the json tags, and what a MarshalJSON method gives.
// Use it for data whose types have only json tags, such as the types of the
// API.
func (o *outputFlag) printAPI(cmd *cobra.Command, data any, views outputViews) error {
	if _, ok := views[FormatYAML]; !ok {
		if views == nil {
			views = outputViews{}
		}

		views[FormatYAML] = func(w io.Writer) error { return writeJSONKeyedYAML(w, data) }
	}

	return o.print(cmd, data, views)
}

// nonNil returns s, or an empty slice for nil, so JSON shows a list.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}

// nonNilMap returns m, or an empty map for nil, so JSON shows an object.
func nonNilMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}

	return m
}

// writeJSONKeyedYAML writes v as YAML with the keys and values of the JSON
// that writeJSONOutput makes of it, in the same order.
func writeJSONKeyedYAML(w io.Writer, v any) error {
	var buf bytes.Buffer

	if err := writeJSONOutput(&buf, v); err != nil {
		return err
	}

	dec := json.NewDecoder(&buf)
	dec.UseNumber()

	doc, err := jsonYAMLNode(dec)
	if err != nil {
		return fmt.Errorf("writing YAML: %w", err)
	}

	b, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("writing YAML: %w", err)
	}

	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("writing YAML: %w", err)
	}

	return nil
}

// jsonYAMLNode reads the next JSON value from dec, which uses numbers, and
// returns it as a YAML node. The YAML encoder chooses the style of each node,
// and quotes a string that reads as another type.
func jsonYAMLNode(dec *json.Decoder) (*yaml.Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("reading JSON: %w", err)
	}

	switch v := tok.(type) {
	case json.Delim:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if v == '{' {
			n = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}

		for dec.More() {
			if n.Kind == yaml.MappingNode {
				key, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("reading JSON: %w", err)
				}

				n.Content = append(n.Content, yamlString(fmt.Sprint(key)))
			}

			child, err := jsonYAMLNode(dec)
			if err != nil {
				return nil, err
			}

			n.Content = append(n.Content, child)
		}

		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("reading JSON: %w", err)
		}

		return n, nil
	case string:
		return yamlString(v), nil
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			return yamlScalar("!!float", v.String()), nil
		}

		return yamlScalar("!!int", v.String()), nil
	case bool:
		return yamlScalar("!!bool", strconv.FormatBool(v)), nil
	default:
		return yamlScalar("!!null", "null"), nil
	}
}

// yamlString returns a YAML string node of s. The YAML encoder loses text in
// some styles, so a string with white space at an end or a character that is
// not printable, other than a newline, gets double quotes. So does "<<", which
// YAML reads as a merge key. Other text with newlines stays a literal block.
func yamlString(s string) *yaml.Node {
	n := yamlScalar("!!str", s)

	notPrintable := func(r rune) bool { return r != '\n' && !unicode.IsPrint(r) }

	if strings.ContainsFunc(s, notPrintable) || strings.TrimSpace(s) != s || s == "<<" {
		n.Style = yaml.DoubleQuotedStyle
	}

	return n
}

// yamlScalar returns a YAML scalar node of the tag and the value.
func yamlScalar(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}
