package types_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types"
)

// repoRoot is the repository root, relative to this package's directory, in
// which go test runs.
const repoRoot = "../../.."

// fencedConfig matches a fenced yaml or yml code block in Markdown whose body
// starts with a phenix apiVersion, capturing the fence's indent and the body.
var fencedConfig = regexp.MustCompile(
	"(?ms)^([ \t]*)```ya?ml[^\n]*\n(apiVersion: phenix\\.sandia\\.gov/.*?)^[ \t]*```",
)

// invalidExamples names the docs examples, by file and config name, that
// show a validation failure on purpose.
var invalidExamples = map[string]bool{ //nolint:gochecknoglobals // test table
	"docs/content/workflow.md helloworld": true, // explains a misplaced image
}

// TestRepoExampleConfigsValidate proves every complete example config in the
// repository passes config validation: the default configs phenix stores on
// startup, the skill's example files, and the config examples in the docs and
// skills Markdown. Fragments are skipped: an example that elides part of
// itself with "..." or has no spec. So are Workflow documents, which are not
// configs, and the examples in invalidExamples.
func TestRepoExampleConfigsValidate(t *testing.T) {
	examples := map[string][]byte{}

	for _, pattern := range []string{
		"src/go/api/config/default/*.yml",
		"skills/*/examples/*.yaml",
	} {
		files, err := filepath.Glob(filepath.Join(repoRoot, pattern))
		if err != nil {
			t.Fatalf("globbing %s: %v", pattern, err)
		}

		if len(files) == 0 {
			t.Fatalf("no files match %s", pattern)
		}

		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("reading %s: %v", file, err)
			}

			examples[relative(t, file)] = data
		}
	}

	exampleFiles := len(examples)

	for _, dir := range []string{"docs/content", "skills"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
				return err
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			for _, m := range fencedConfig.FindAllSubmatchIndex(data, -1) {
				line := 1 + strings.Count(string(data[:m[0]]), "\n")
				indent := string(data[m[2]:m[3]])
				body := strings.ReplaceAll("\n"+string(data[m[4]:m[5]]), "\n"+indent, "\n")[1:]

				examples[relative(t, path)+":"+strconv.Itoa(line)] = []byte(body)
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	validated := 0

	for name, data := range examples {
		if strings.Contains(string(data), "...") {
			continue
		}

		c, err := store.NewConfigFromYAML(data)
		if err != nil {
			t.Errorf("%s: parsing config: %v", name, err)

			continue
		}

		file, _, _ := strings.Cut(name, ":")
		if c.Kind == "Workflow" || c.Spec == nil || invalidExamples[file+" "+c.Metadata.Name] {
			continue
		}

		validated++

		if err := types.ValidateConfigSpec(*c); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Pins that the walk finds the docs examples, not only the files.
	if minimum := exampleFiles + 10; validated < minimum {
		t.Errorf("validated %d example configs, want at least %d", validated, minimum)
	}
}

func relative(t *testing.T, path string) string {
	t.Helper()

	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		t.Fatalf("relative path of %s: %v", path, err)
	}

	return rel
}
