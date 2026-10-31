package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresPaths(t *testing.T) {
	for _, args := range [][2]string{{"", ""}, {"out", ""}, {"", "mkdocs.yml"}} {
		if err := run(args[0], args[1]); err == nil {
			t.Errorf("run(%q, %q) = nil, want an error", args[0], args[1])
		}
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	mkdocs := filepath.Join(dir, "mkdocs.yml")

	if err := os.WriteFile(mkdocs, []byte(filesTestNav), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run(filepath.Join(dir, "cli"), mkdocs); err != nil {
		t.Fatalf("run() = %v, want no error", err)
	}

	config, err := os.ReadFile(mkdocs)
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{indexFile, "experiment.md", "vm.md"} {
		if _, err := os.Stat(filepath.Join(dir, "cli", file)); err != nil {
			t.Errorf("run() did not write %s: %v", file, err)
		}

		if !strings.Contains(string(config), ": cli/"+file+"\n") {
			t.Errorf("run() did not list cli/%s in the nav:\n%s", file, config)
		}
	}
}
