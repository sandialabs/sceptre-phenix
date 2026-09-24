package runtimeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSetPreservesExistingConfiguration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := []byte("log:\n  level: debug\nui:\n  listen-endpoint: 127.0.0.1:3000\n")
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	if err := Set(path, "ui.default-theme", "dark"); err != nil {
		t.Fatalf("setting runtime configuration: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("parsing result: %v", err)
	}

	logging := got["log"].(map[string]any)
	if logging["level"] != "debug" {
		t.Fatalf("log.level = %v, want debug", logging["level"])
	}

	ui := got["ui"].(map[string]any)
	if ui["listen-endpoint"] != "127.0.0.1:3000" {
		t.Fatalf("ui.listen-endpoint = %v", ui["listen-endpoint"])
	}
	if ui["default-theme"] != "dark" {
		t.Fatalf("ui.default-theme = %v, want dark", ui["default-theme"])
	}
}

func TestSetPreservesCommentsOrderAndTypes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := "# phenix runtime settings\n" +
		"ui:\n" +
		"  listen-endpoint: 127.0.0.1:3000 # local only\n" +
		"  default-theme: light\n" +
		"log:\n" +
		"  level: debug\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatalf("writing fixture: %v", err)
	}

	if err := Set(path, "UI.default-theme", "dark"); err != nil {
		t.Fatalf("setting existing key: %v", err)
	}
	if err := Set(path, "log.system.max-age", 30); err != nil {
		t.Fatalf("setting nested new key: %v", err)
	}
	if err := Set(path, "ui.base-path", "3000"); err != nil {
		t.Fatalf("setting string that looks numeric: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}

	want := "# phenix runtime settings\n" +
		"ui:\n" +
		"  listen-endpoint: 127.0.0.1:3000 # local only\n" +
		"  default-theme: dark\n" +
		"  base-path: \"3000\"\n" +
		"log:\n" +
		"  level: debug\n" +
		"  system:\n" +
		"    max-age: 30\n"
	if string(data) != want {
		t.Fatalf("config file mismatch\n got:\n%s\nwant:\n%s", data, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat result: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("file mode = %v, want existing 0644 preserved", info.Mode().Perm())
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("listing directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config-") {
			t.Fatalf("temporary file %s was left behind", entry.Name())
		}
	}
}

func TestSetCreatesMissingFileAndDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	if err := Set(path, "ui.default-theme", "dark"); err != nil {
		t.Fatalf("setting runtime configuration: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}
	if string(data) != "ui:\n  default-theme: dark\n" {
		t.Fatalf("unexpected config file:\n%s", data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat result: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSetExpandsFlowStyleEmptyMapping(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	if err := Set(path, "ui.default-theme", "system"); err != nil {
		t.Fatalf("setting runtime configuration: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}
	if string(data) != "ui:\n  default-theme: system\n" {
		t.Fatalf("unexpected config file:\n%s", data)
	}
}

func TestSetRejectsInvalidYAML(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("ui: ["), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	if err := Set(path, "ui.default-theme", "dark"); err == nil {
		t.Fatal("expected invalid YAML error")
	}
}

func TestSetRejectsNonMappingDocument(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("- just\n- a list\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	err := Set(path, "ui.default-theme", "dark")
	if !errors.Is(err, ErrNotMapping) {
		t.Fatalf("error = %v, want ErrNotMapping", err)
	}
}

func TestSetRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	if err := Set("", "ui.default-theme", "dark"); !errors.Is(err, ErrEmptyConfigPath) {
		t.Fatalf("error = %v, want ErrEmptyConfigPath", err)
	}
}
