package types_test

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot is the repository root, relative to this package's directory, in
// which go test runs.
const repoRoot = "../../.."

// TestRepoExampleConfigsValidate proves each example config file in the
// repository passes config validation as the docs examples do: the default
// configs phenix stores on startup and the skill's example files. Each file
// must hold at least one complete config.
func TestRepoExampleConfigsValidate(t *testing.T) {
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

			name := relative(t, file)
			if checkExamples(t, name, string(data), map[string]bool{}) == 0 {
				t.Errorf("%s: found no complete config", name)
			}
		}
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
