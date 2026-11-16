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

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	"phenix/util/envsubst"
)

// exampleDocs are the Markdown files whose YAML config examples must validate.
var exampleDocs = []string{ //nolint:gochecknoglobals // test table
	"README.md",
	"docs/content/*.md",
	"docs/content/builder/*.md",
	"docs/content/builder/details/*.md",
	"skills/*/SKILL.md",
	"skills/*/references/*.md",
}

var yamlFence = regexp.MustCompile("^(\\s*)(```+)\\s*(?:yaml|yml)(?:\\s.*)?$")

// invalidExamples names the docs examples, by file and config name, that show a validation
// failure on purpose. They must fail validation.
var invalidExamples = map[string]bool{ //nolint:gochecknoglobals // test table
	"docs/content/workflow.md helloworld": true, // explains a misplaced image
}

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
// Each example in invalidExamples must be found and must fail validation.
func TestDocsConfigExamplesValidate(t *testing.T) {
	var (
		checked int
		invalid = map[string]bool{}
	)

	for _, ex := range yamlExamples(t) {
		checked += checkExamples(t, ex.where, ex.body, invalid)
	}

	// Pins that the extractor finds the docs examples.
	if minimum := 10; checked < minimum {
		t.Fatalf("validated %d YAML config examples, want at least %d; did the docs move?", checked, minimum)
	}

	if diff := cmp.Diff(invalidExamples, invalid); diff != "" {
		t.Errorf("invalid docs examples found mismatch (-want +got):\n%s", diff)
	}

	t.Logf("validated %d YAML config examples", checked)
}

// checkExamples validates each YAML document in body, from where ("file" or "file:line"), that has
// an apiVersion and a kind and does not elide content with "...". A document in invalidExamples
// must fail; checkExamples adds it to invalid. It returns how many documents it validated.
func checkExamples(t *testing.T, where, body string, invalid map[string]bool) int {
	t.Helper()

	var (
		checked    int
		file, _, _ = strings.Cut(where, ":")
		dec        = yaml.NewDecoder(strings.NewReader(body))
	)

	for {
		var node yaml.Node

		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			return checked
		}

		if err != nil {
			if configHeader.MatchString(body) {
				t.Errorf("%s: parsing YAML: %v", where, err)
			}

			return checked
		}

		var doc map[string]any
		if err := node.Decode(&doc); err != nil || doc["apiVersion"] == nil || doc["kind"] == nil ||
			elided(&node) {
			continue
		}

		checked++

		metadata, _ := doc["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		err = validateExample(&node, doc)

		switch key := file + " " + name; {
		case !invalidExamples[key] && err != nil:
			t.Errorf("%s: %v", where, err)
		case invalidExamples[key] && err == nil:
			t.Errorf("%s: config %s passes validation, want it to fail as the docs show", where, name)
		case invalidExamples[key]:
			invalid[key] = true
		}
	}
}

func validateExample(node *yaml.Node, doc map[string]any) error {
	if doc["kind"] == "Workflow" {
		return workflow.ValidateDocument(doc)
	}

	body, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	// A placeholder gets its default, whatever the environment of the test holds.
	unset := func(string) (string, bool) { return "", false }

	c, err := store.DecodeConfigYAML(envsubst.Expand(body, unset))
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

// TestDocsFileExamples checks that fileExamples finds each YAML fence, of
// three or more backticks, indented or not and with or without attributes,
// and strips the fence's indentation from the block.
func TestDocsFileExamples(t *testing.T) {
	t.Parallel()

	doc := strings.Join([]string{
		"# Title",             // 1
		"```yaml",             // 2
		"apiVersion: v1",      // 3
		"```",                 // 4
		"```go",               // 5
		"package main",        // 6
		"```",                 // 7
		"1. A step:",          // 8
		"",                    // 9
		"    ```` yml",        // 10
		"    kind: Topology",  // 11
		"      nested: ```",   // 12
		"    ````",            // 13
		"```yaml title=\"a\"", // 14
		"kind: Role",          // 15
		"```",                 // 16
		"```yamlx",            // 17
		"not yaml",            // 18
		"```",                 // 19
		"```yaml",             // 20
		"never closed",        // 21
	}, "\n")

	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	got := fileExamples(t, path)

	where, bodies := make([]string, 0, len(got)), make([]string, 0, len(got))
	for _, ex := range got {
		where = append(where, ex.where[strings.LastIndex(ex.where, ":")+1:])
		bodies = append(bodies, ex.body)
	}

	if diff := cmp.Diff([]string{"2", "10", "14"}, where); diff != "" {
		t.Errorf("fileExamples() lines mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"apiVersion: v1", "kind: Topology\n  nested: ```", "kind: Role"}, bodies); diff != "" {
		t.Errorf("fileExamples() bodies mismatch (-want +got):\n%s", diff)
	}
}

func TestDocsElided(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"kind: Topology":                false,
		"spec: ...":                     true,
		"spec:\n  nodes:\n  - ...":      true,
		"spec:\n  note: '... and more'": false,
		"spec: [a, ...]":                true,
	}

	for body, want := range tests {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(body), &node); err != nil {
			t.Fatalf("yaml.Unmarshal(%q) error = %v", body, err)
		}

		if got := elided(&node); got != want {
			t.Errorf("elided(%q) = %t, want %t", body, got, want)
		}
	}
}
