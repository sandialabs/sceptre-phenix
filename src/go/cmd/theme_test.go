package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
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

func TestDefaultThemeFlagUsesExplicitPrecedence(t *testing.T) {
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
	viper.Set("ui.default-theme", "light")
	t.Cleanup(func() {
		configFilePath = previousConfigFilePath
		viper.Set("ui.default-theme", "system")
	})

	if got := getEffectiveString("ui.default-theme", false); got != "dark" {
		t.Fatalf("file default theme = %q, want dark", got)
	}
	if got := getEffectiveString("ui.default-theme", true); got != "light" {
		t.Fatalf("flag default theme = %q, want light", got)
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
