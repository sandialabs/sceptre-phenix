package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// committedDir is the repository's schemas directory.
var committedDir = filepath.Join("..", "..", "..", "..", "schemas") //nolint:gochecknoglobals // test constant

// The committed schemas are what the exporter writes now: a change to the
// embedded OpenAPI files needs `make generate` (or `make -C src/go
// generate-schemas`) and the regenerated files committed with it.
func TestCommittedSchemasAreCurrent(t *testing.T) {
	out := t.TempDir()

	if err := export(out); err != nil {
		t.Fatalf("export: %v", err)
	}

	want := schemaFiles(t, out)
	got := schemaFiles(t, committedDir)

	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("schemas/ holds %v, want %v: run make -C src/go generate-schemas",
			slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}

	for name, data := range want {
		if !bytes.Equal(got[name], data) {
			t.Errorf("schemas/%s is stale: run make -C src/go generate-schemas", name)
		}
	}
}

// Every exported file is a JSON Schema 2020-12 document, with nothing of
// OpenAPI 3.0 left in it: each per-kind file describes a whole config file and
// references its spec in the defs file, which holds every component once.
// schemas.test.js in src/js/test checks the files against the 2020-12
// metaschema, and configs against them.
func TestExportedSchemasAreConfigSchemas(t *testing.T) {
	out := t.TempDir()

	if err := export(out); err != nil {
		t.Fatalf("export: %v", err)
	}

	files := schemaFiles(t, out)

	var shared map[string]any

	if err := json.Unmarshal(files[defsFile], &shared); err != nil {
		t.Fatalf("%s: %v", defsFile, err)
	}

	defs, _ := shared["$defs"].(map[string]any)
	if len(defs) == 0 {
		t.Fatalf("%s: no $defs", defsFile)
	}

	for _, problem := range schemaProblems(shared, "#", defsPrefix, defs) {
		t.Errorf("%s: %s", defsFile, problem)
	}

	for name, data := range files {
		if name == defsFile {
			continue
		}

		var doc map[string]any

		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if doc["$schema"] != schemaDraft202012 {
			t.Errorf("%s: $schema = %v", name, doc["$schema"])
		}

		if _, own := doc["$defs"]; own {
			t.Errorf("%s: has $defs of its own instead of referencing %s", name, defsFile)
		}

		properties, _ := doc["properties"].(map[string]any)
		for _, key := range []string{"apiVersion", "kind", "metadata", "spec"} {
			if properties[key] == nil {
				t.Errorf("%s: no %s property", name, key)
			}
		}

		kind, _ := properties["kind"].(map[string]any)
		if constant, _ := kind["const"].(string); !strings.EqualFold(constant, strings.TrimSuffix(name, schemaFileSuffix)) {
			t.Errorf("%s: kind = %v", name, kind["const"])
		}

		for _, problem := range schemaProblems(doc, "#", defsFile+defsPrefix, defs) {
			t.Errorf("%s: %s", name, problem)
		}
	}
}

// schemaProblems lists what a JSON Schema 2020-12 validator would refuse, or
// what is left of OpenAPI 3.0, in a schema tree whose references are refPrefix
// followed by the name of one of defs.
func schemaProblems(node any, path, refPrefix string, defs map[string]any) []string {
	var problems []string

	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			at := path + "/" + key

			switch key {
			case "examples", "default", "enum", "const":
				continue
			case "nullable", "example":
				problems = append(problems, at+": OpenAPI keyword")
			case "pattern":
				pattern, ok := value.(string)
				if !ok {
					problems = append(problems, at+": pattern is not a string")
				} else if _, err := regexp.Compile(pattern); err != nil {
					problems = append(problems, at+": "+err.Error())
				}
			case "$ref":
				ref, _ := value.(string)
				if target, ok := strings.CutPrefix(ref, refPrefix); !ok || defs[target] == nil {
					problems = append(problems, at+": unresolved reference "+ref)
				}
			}

			problems = append(problems, schemaProblems(value, at, refPrefix, defs)...)
		}
	case []any:
		for i, value := range typed {
			problems = append(problems, schemaProblems(value, fmt.Sprintf("%s/%d", path, i), refPrefix, defs)...)
		}
	case nil:
		problems = append(problems, path+": null")
	}

	return problems
}

// schemaFiles reads the *.schema.json files under dir, keyed by their path
// relative to it.
func schemaFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	files := map[string][]byte{}

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, schemaFileSuffix) {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		files[filepath.ToSlash(relative)] = data

		return nil
	})
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	return files
}
