package builder_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

// docsExamples holds the Builder documents the documentation links to, which
// a reader uploads as they are.
var docsExamples = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "..", "docs", "content", "builder", "examples",
)

// docsCodes is the docs page whose table lists every code.
var docsCodes = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "..", "docs", "content", "builder", "error-codes.md",
)

// The markers around the table of codes in [docsCodes].
const (
	codeTableStart = "<!-- codes:start -->"
	codeTableEnd   = "<!-- codes:end -->"
)

// readDocsExample returns a file of docs/content/builder/examples.
func readDocsExample(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(docsExamples, name))
	if err != nil {
		t.Fatalf("reading the docs example: %v", err)
	}

	return data
}

// TestDocsExamplesParse checks that every Builder document the documentation
// offers for upload is one this package accepts.
func TestDocsExamplesParse(t *testing.T) {
	// docs/ is in this repository, so a missing directory is a moved path.
	if _, err := os.Stat(docsExamples); err != nil {
		t.Fatalf("os.Stat(%s) error = %v, want the documentation examples", docsExamples, err)
	}

	examples, err := filepath.Glob(filepath.Join(docsExamples, "*.builder.json"))
	if err != nil {
		t.Fatalf("listing %s: %v", docsExamples, err)
	}

	if len(examples) == 0 {
		t.Fatalf("filepath.Glob(%s/*.builder.json) = none, want Builder documents", docsExamples)
	}

	for _, example := range examples {
		t.Run(filepath.Base(example), func(t *testing.T) {
			data, err := os.ReadFile(example)
			if err != nil {
				t.Fatalf("reading: %v", err)
			}

			if _, err := builder.Parse(data); err != nil {
				t.Errorf("Parse(%s) error = %v, want nil", filepath.Base(example), err)
			}
		})
	}
}

func TestDocsPackageExampleLoads(t *testing.T) {
	data, err := builder.JSONFromText(readDocsExample(t, "pump-station.package.yaml"))
	if err != nil {
		t.Fatalf("JSONFromText(pump-station.package.yaml): %v", err)
	}

	pkg, err := builder.DecodePackage(data)
	if err == nil {
		err = pkg.Validate()
	}

	if err != nil {
		t.Fatalf("DecodePackage(pump-station.package.yaml) and Validate(): %v", err)
	}

	if len(pkg.Scenarios) != 1 || len(pkg.Requirements.Images) == 0 {
		t.Errorf("DecodePackage(pump-station.package.yaml) = %s, want a scenario and disk images", asJSON(t, pkg))
	}

	// It lists what the package of its own document lists.
	made := builder.NewPackage(pkg.Document, builder.PackageContents{
		Scenarios:      pkg.Scenarios,
		CarryScenarios: true,
		Topologies:     nil,
		Images:         true,
	})

	if diff := cmp.Diff(made.Requirements, pkg.Requirements); diff != "" {
		t.Errorf("pump-station.package.yaml requirements mismatch with NewPackage() of its document (-want +got):\n%s", diff)
	}
}

func TestDocsTemplateFileExampleLoads(t *testing.T) {
	file, err := builder.ParseTemplateFile(readDocsExample(t, "node-templates.yaml"))
	if err != nil {
		t.Fatalf("the docs example node-templates.yaml does not load: %v", err)
	}

	if len(file.Templates) == 0 {
		t.Error("ParseTemplateFile(node-templates.yaml) templates = none, want some")
	}
}

// codeTable is the docs' table of every code, in the order of the registry.
func codeTable() string {
	var table strings.Builder

	table.WriteString("| Code | Severity | Rule |\n|---|---|---|\n")

	for _, info := range builder.Codes() {
		table.WriteString("| `" + string(info.Code) + "` | " + string(info.Severity) + " | " + info.Description + " |\n")
	}

	return table.String()
}

// The docs list every code, as the registry has them, between the table's
// markers.
func TestDocsCodeTableMatchesRegistry(t *testing.T) {
	page, err := os.ReadFile(docsCodes)
	if err != nil {
		t.Fatalf("reading %s: %v", docsCodes, err)
	}

	text := string(page)

	before, rest, found := strings.Cut(text, codeTableStart+"\n")
	if !found {
		t.Fatalf("%s has no %s line", docsCodes, codeTableStart)
	}

	table, after, found := strings.Cut(rest, codeTableEnd)
	if !found {
		t.Fatalf("%s has no %s after %s", docsCodes, codeTableEnd, codeTableStart)
	}

	want := codeTable()

	if *updateFrontendSchema && table != "\n"+want+"\n" {
		updated := before + codeTableStart + "\n\n" + want + "\n" + codeTableEnd + after
		if err := os.WriteFile(docsCodes, []byte(updated), 0o600); err != nil {
			t.Fatalf("writing %s: %v", docsCodes, err)
		}

		table = "\n" + want + "\n"
	}

	if diff := cmp.Diff("\n"+want+"\n", table); diff != "" {
		t.Errorf("the table of codes in %s is out of date; regenerate it with make generate-builder-schema in src/go "+
			"(-want +got):\n%s", docsCodes, diff)
	}
}
