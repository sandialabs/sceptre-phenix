package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeRuntimeDefaultTheme(t *testing.T) {
	got, err := normalizeRuntimeSetting("ui.default-theme", " DARK ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "dark" {
		t.Fatalf("normalized theme = %q, want dark", got)
	}

	if _, err := normalizeRuntimeSetting("ui.default-theme", "sepia"); err == nil {
		t.Fatal("expected invalid default theme error")
	}
}

func TestDefaultThemePrecedence(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(
		configFile,
		[]byte("ui:\n  default-theme: dark\n"),
		0o600,
	); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	previousConfigFilePath := configFilePath
	configFilePath = configFile
	t.Cleanup(func() { configFilePath = previousConfigFilePath })
	t.Setenv("PHENIX_UI_DEFAULT_THEME", "light")

	// The config file wins over the environment ...
	if got := getEffectiveString("ui.default-theme", false); got != "dark" {
		t.Fatalf("file default theme = %q, want dark", got)
	}
	// ... unless the flag was given, in which case viper's own precedence
	// (flag, then environment) applies.
	if got := getEffectiveString("ui.default-theme", true); got != "light" {
		t.Fatalf("flag default theme = %q, want light", got)
	}

	if got := getRuntimeConfigFilePath(); got != configFile {
		t.Fatalf("runtime config file = %q, want %q", got, configFile)
	}
}

func TestUIUsesExplicitDefaultThemeFlag(t *testing.T) {
	cmd := newUICmd()

	flag := cmd.Flags().Lookup("default-theme")
	if flag == nil {
		t.Fatal("default-theme flag is not registered")
	}
	if flag.DefValue != "system" {
		t.Fatalf("default-theme default = %q, want system", flag.DefValue)
	}
	if cmd.Flags().Lookup("theme") != nil {
		t.Fatal("ambiguous theme flag should not be registered")
	}
}
