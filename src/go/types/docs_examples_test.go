package types_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/types"
	"phenix/types/version"
)

// exampleDocs are the Markdown files whose YAML config examples must validate.
var exampleDocs = []string{ //nolint:gochecknoglobals // test table
	"README.md",
	"docs/content/*.md",
	"skills/*/SKILL.md",
	"skills/*/references/*.md",
}

var yamlFence = regexp.MustCompile("^(\\s*)(```+)\\s*(?:yaml|yml)\\s*$")

// configHeader matches a block that starts a config, so a block of loose YAML fragments that
// does not parse is not reported.
var configHeader = regexp.MustCompile(`(?m)^apiVersion:`)

// yamlExample is one fenced YAML block, with the file and line its fence opens on.
type yamlExample struct {
	where string
	body  string
}

// TestDocsConfigExamplesValidate validates each docs YAML document with an apiVersion and kind as
// `phenix config create` or `phenix workflow apply` would, skipping ones that elide with "...".
func TestDocsConfigExamplesValidate(t *testing.T) {
	var checked int

	for _, ex := range yamlExamples(t) {
		dec := yaml.NewDecoder(strings.NewReader(ex.body))

		for {
			var node yaml.Node

			err := dec.Decode(&node)
			if errors.Is(err, io.EOF) {
				break
			}

			if err != nil {
				if configHeader.MatchString(ex.body) {
					t.Errorf("%s: parsing YAML: %v", ex.where, err)
				}

				break
			}

			var doc map[string]any
			if err := node.Decode(&doc); err != nil || doc["apiVersion"] == nil || doc["kind"] == nil ||
				elided(&node) {
				continue
			}

			checked++

			if err := validateExample(&node, doc); err != nil {
				t.Errorf("%s: %v", ex.where, err)
			}
		}
	}

	if checked == 0 {
		t.Fatal("found no YAML config examples; did the docs move?")
	}

	t.Logf("validated %d YAML config examples", checked)
}

func validateExample(node *yaml.Node, doc map[string]any) error {
	if doc["kind"] == "Workflow" {
		return workflow.ValidateDocument(doc)
	}

	body, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	c, err := store.NewConfigFromYAML(body)
	if err != nil {
		return err
	}

	if want, ok := version.StoredVersion[c.Kind]; ok && c.APIVersion() != want {
		return fmt.Errorf("%s uses apiVersion %s, but only %s loads", c.Kind, c.Version, want)
	}

	return types.ValidateConfigSpec(*c)
}

// elided reports whether n holds a "..." standing for content left out of the example.
func elided(n *yaml.Node) bool {
	if n.Kind == yaml.ScalarNode && n.Value == "..." {
		return true
	}

	return slices.ContainsFunc(n.Content, elided)
}

// yamlExamples returns the fenced YAML blocks in exampleDocs, with each block's indentation
// (inside a list item or an admonition) removed.
func yamlExamples(t *testing.T) []yamlExample {
	t.Helper()

	var examples []yamlExample

	for _, pattern := range exampleDocs {
		paths, err := filepath.Glob(filepath.Join(repoRoot, pattern))
		if err != nil || len(paths) == 0 {
			t.Fatalf("no files match %s (err %v)", pattern, err)
		}

		for _, path := range paths {
			examples = append(examples, fileExamples(t, path)...)
		}
	}

	return examples
}

func fileExamples(t *testing.T, path string) []yamlExample {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}

	defer func() { _ = f.Close() }()

	var (
		examples      []yamlExample
		indent, fence string
		start         int
		body          []string
		inBlock       bool
		scanner       = bufio.NewScanner(f)
		name, _       = filepath.Rel(repoRoot, path)
	)

	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()

		switch {
		case !inBlock:
			if m := yamlFence.FindStringSubmatch(text); m != nil {
				inBlock, indent, fence, start, body = true, m[1], m[2], line, nil
			}
		case strings.TrimSpace(text) == fence:
			inBlock = false

			examples = append(examples, yamlExample{
				where: fmt.Sprintf("%s:%d", name, start),
				body:  strings.Join(body, "\n"),
			})
		default:
			body = append(body, strings.TrimPrefix(text, indent))
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return examples
}
