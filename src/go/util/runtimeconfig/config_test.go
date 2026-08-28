package runtimeconfig

import (
	"os"
	"path/filepath"
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
