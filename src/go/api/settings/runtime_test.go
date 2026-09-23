package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func TestEnvVarForRuntimeKey(t *testing.T) {
	t.Parallel()

	got := EnvVarForRuntimeKey("ui.logs.minimega-path")
	want := "PHENIX_UI_LOGS_MINIMEGA_PATH"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestRuntimeEnvironmentMasksSensitiveValues(t *testing.T) {
	t.Setenv("PHENIX_UI_JWT_SIGNING_KEY", "secret")

	env := GetRuntimeEnvironment()
	for _, entry := range env {
		if entry.Name != "PHENIX_UI_JWT_SIGNING_KEY" {
			continue
		}
		if entry.Value != "********" {
			t.Fatalf("expected masked value, got %q", entry.Value)
		}
		if !entry.Sensitive {
			t.Fatal("expected sensitive env var")
		}
		return
	}

	t.Fatal("expected PHENIX_UI_JWT_SIGNING_KEY env var")
}

func TestSetAndUnsetRuntimeSetting(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	resetViper(t, configFile)

	err := SetRuntimeSetting(RuntimeSettingUpdate{
		Key:   "log.level",
		Value: "debug",
	})
	if err != nil {
		t.Fatalf("setting runtime setting: %v", err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("reading config file: %v", err)
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshaling config: %v", err)
	}

	logConfig, ok := config["log"].(map[string]any)
	if !ok {
		t.Fatalf("expected log config, got %#v", config["log"])
	}
	if logConfig["level"] != "debug" {
		t.Fatalf("expected debug log level, got %#v", logConfig["level"])
	}

	if err := UnsetRuntimeSetting("log.level"); err != nil {
		t.Fatalf("unsetting runtime setting: %v", err)
	}

	data, err = os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("reading config file after unset: %v", err)
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshaling config after unset: %v", err)
	}

	logConfig, ok = config["log"].(map[string]any)
	if !ok {
		t.Fatalf("expected log config after unset, got %#v", config["log"])
	}
	if _, ok := logConfig["level"]; ok {
		t.Fatal("expected log.level to be unset")
	}
}

func resetViper(t *testing.T, configFile string) {
	t.Helper()

	viper.Reset()
	viper.SetEnvPrefix("PHENIX")
	viper.SetConfigFile(configFile)

	t.Cleanup(func() {
		viper.Reset()
	})
}
