package runtimesettings_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"phenix/api/runtimesettings"
)

// newSettings returns the Settings of a viper that read the YAML config file
// data, with the flags of flags, no environment, and the file's path.
func newSettings(t *testing.T, data string, flags *pflag.FlagSet) (*runtimesettings.Settings, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig(%s) error = %v", path, err)
	}

	if err := v.BindPFlags(flags); err != nil {
		t.Fatalf("BindPFlags() error = %v", err)
	}

	s := runtimesettings.New(v,
		runtimesettings.WithFlags(flags.Lookup),
		runtimesettings.WithLookupEnv(func(name string) (string, bool) {
			return map[string]string{"PHENIX_LOG_CONSOLE": "stdout"}[name], name == "PHENIX_LOG_CONSOLE"
		}),
	)

	return s, path
}

// testFlags are a few phenix flags, one of them set on the command line.
func testFlags(t *testing.T) *pflag.FlagSet {
	t.Helper()

	flags := pflag.NewFlagSet("phenix", pflag.ContinueOnError)
	flags.String("log.level", "info", "")
	flags.String("log.console", "stderr", "")
	flags.Int("log.system.max-size", 100, "")
	flags.String("ui.jwt-signing-key", "", "")
	flags.String("ui.listen-endpoint", ":3000", "")

	if err := flags.Parse([]string{"--ui.listen-endpoint=:3080"}); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}

	return flags
}

// List gives each setting's value, default, source and whether a change
// needs a restart, and hides a secret that is set.
func TestList(t *testing.T) {
	t.Parallel()

	s, _ := newSettings(t, "log:\n  level: debug\nui:\n  jwt-signing-key: hush\nother: 1\n", testFlags(t))

	want := []runtimesettings.Setting{
		{Key: "log.console", Value: "stderr", Default: "stderr", Source: runtimesettings.SourceEnv, Restart: false},
		{Key: "log.level", Value: "debug", Default: "info", Source: runtimesettings.SourceFile, Restart: false},
		{Key: "log.system.max-size", Value: 100, Default: "100", Source: runtimesettings.SourceDefault, Restart: false},
		{Key: "other", Value: 1, Default: nil, Source: runtimesettings.SourceFile, Restart: true},
		{
			Key: "ui.jwt-signing-key", Value: runtimesettings.Redacted, Default: "",
			Source: runtimesettings.SourceFile, Restart: true,
		},
		{Key: "ui.listen-endpoint", Value: ":3080", Default: ":3000", Source: runtimesettings.SourceFlag, Restart: true},
	}

	if diff := cmp.Diff(want, s.List()); diff != "" {
		t.Errorf("List() mismatch (-want +got):\n%s", diff)
	}
}

// Get gives one setting, and ErrNotFound for a key without a value or a flag.
func TestGet(t *testing.T) {
	t.Parallel()

	s, _ := newSettings(t, "log:\n  level: debug\n", testFlags(t))

	got, err := s.Get("LOG.LEVEL")
	if err != nil {
		t.Fatalf("Get(LOG.LEVEL) error = %v", err)
	}

	want := runtimesettings.Setting{Key: "log.level", Value: "debug", Default: "info", Source: runtimesettings.SourceFile}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Get(LOG.LEVEL) mismatch (-want +got):\n%s", diff)
	}

	if _, err := s.Get("no.such"); !errors.Is(err, runtimesettings.ErrNotFound) {
		t.Errorf("Get(no.such) error = %v, want %v", err, runtimesettings.ErrNotFound)
	}
}

// Set writes the value parsed as the flag does, refuses one the flag does
// not take, and Unset removes it; each says whether a restart is needed.
func TestSetUnset(t *testing.T) {
	t.Parallel()

	s, path := newSettings(t, "# kept\nlog:\n  level: debug\n", testFlags(t))

	change, err := s.Set("log.system.max-size", "200")
	if err != nil {
		t.Fatalf("Set(log.system.max-size, 200) error = %v", err)
	}

	if diff := cmp.Diff(runtimesettings.Change{Key: "log.system.max-size", File: path, Changed: true}, change); diff != "" {
		t.Errorf("Set(log.system.max-size) mismatch (-want +got):\n%s", diff)
	}

	if _, err := s.Set("log.system.max-size", "big"); !errors.Is(err, runtimesettings.ErrSettingType) {
		t.Errorf("Set(log.system.max-size, big) error = %v, want %v", err, runtimesettings.ErrSettingType)
	}

	change, err = s.Set("ui.listen-endpoint", ":3090")
	if err != nil || !change.Restart {
		t.Errorf("Set(ui.listen-endpoint) = %+v, %v; want a change that needs a restart", change, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	want := "# kept\nlog:\n  level: debug\n  system:\n    max-size: 200\nui:\n  listen-endpoint: :3090\n"
	if got := string(data); got != want {
		t.Errorf("config file after Set = %q, want %q", got, want)
	}

	for _, tt := range []struct {
		key     string
		changed bool
	}{{key: "log.system.max-size", changed: true}, {key: "log.system.max-size", changed: false}} {
		change, err := s.Unset(tt.key)
		if err != nil || change.Changed != tt.changed {
			t.Errorf("Unset(%s) = %+v, %v; want changed %t", tt.key, change, err, tt.changed)
		}
	}
}

// Unset with UnsetDryRun and HasInFile report whether the file has the key,
// and change nothing.
func TestUnsetDryRun(t *testing.T) {
	t.Parallel()

	const original = "# kept\nlog:\n  level: debug\n"

	s, path := newSettings(t, original, testFlags(t))

	for _, tt := range []struct {
		key     string
		changed bool
	}{{key: "log.level", changed: true}, {key: "log.system.max-size", changed: false}} {
		change, err := s.Unset(tt.key, runtimesettings.UnsetDryRun())
		if err != nil || change.Changed != tt.changed || change.File != path {
			t.Errorf("Unset(%s, UnsetDryRun()) = %+v, %v; want changed %t in %s", tt.key, change, err, tt.changed, path)
		}

		if has, err := runtimesettings.HasInFile(path, tt.key); err != nil || has != tt.changed {
			t.Errorf("HasInFile(%s) = %t, %v; want %t", tt.key, has, err, tt.changed)
		}
	}

	if data, err := os.ReadFile(path); err != nil || string(data) != original {
		t.Errorf("config file after a dry run = %q, %v; want %q", data, err, original)
	}
}

// Unset refuses a config file that does not exist, and Set and Unset one
// that is not YAML.
func TestFileRefusals(t *testing.T) {
	t.Parallel()

	missing := runtimesettings.New(viper.New(), runtimesettings.WithFile(func() string {
		return filepath.Join(t.TempDir(), "none.yaml")
	}))

	if _, err := missing.Unset("log.level"); !errors.Is(err, runtimesettings.ErrNoConfigFile) {
		t.Errorf("Unset(no file) error = %v, want %v", err, runtimesettings.ErrNoConfigFile)
	}

	toml := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(toml, []byte("a = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	other := runtimesettings.New(viper.New(), runtimesettings.WithFile(func() string { return toml }))

	if _, err := other.Set("a", "2"); !errors.Is(err, runtimesettings.ErrNotYAML) {
		t.Errorf("Set(toml) error = %v, want %v", err, runtimesettings.ErrNotYAML)
	}

	if _, err := other.Unset("a"); !errors.Is(err, runtimesettings.ErrNotYAML) {
		t.Errorf("Unset(toml) error = %v, want %v", err, runtimesettings.ErrNotYAML)
	}
}

// File names the config file that the viper read, the one WithFile gives, or
// none for an empty viper.
func TestFile(t *testing.T) {
	t.Parallel()

	_, path := newSettings(t, "", testFlags(t))
	read := viper.New()
	read.SetConfigFile(path)

	given := runtimesettings.New(viper.New(), runtimesettings.WithFile(func() string { return "/a.yaml" }))

	tests := map[string]struct {
		settings *runtimesettings.Settings
		want     string
	}{
		"read":  {settings: runtimesettings.New(read), want: path},
		"given": {settings: given, want: "/a.yaml"},
		"none":  {settings: runtimesettings.New(viper.New()), want: ""},
	}

	for name, tt := range tests {
		if got := tt.settings.File(); got != tt.want {
			t.Errorf("%s: File() = %q, want %q", name, got, tt.want)
		}
	}
}

// Live, Secret, Redact and EnvVar know phenix's settings.
func TestKnownSettings(t *testing.T) {
	t.Parallel()

	for key, want := range map[string]bool{
		"log.level": true, "log.system.max-size": true, "ui.logs.level": true, "ui.users": true,
		"experiment.disk-check": true, "ui.listen-endpoint": false, "logs": false, "ui.logs.minimega-path": false,
	} {
		if got := runtimesettings.Live(key); got != want {
			t.Errorf("Live(%s) = %t, want %t", key, got, want)
		}
	}

	for _, tt := range []struct {
		key   string
		value any
		want  any
	}{
		{key: "ui.jwt-signing-key", value: "hush", want: runtimesettings.Redacted},
		{key: "ui.jwt-signing-key", value: "", want: ""},
		{key: "ui.users", value: []string{"a:b:Global Admin"}, want: runtimesettings.Redacted},
		{key: "ui.users", value: []any{}, want: []any{}},
		{key: "log.level", value: "debug", want: "debug"},
	} {
		if diff := cmp.Diff(tt.want, runtimesettings.Redact(tt.key, tt.value)); diff != "" {
			t.Errorf("Redact(%s, %v) mismatch (-want +got):\n%s", tt.key, tt.value, diff)
		}
	}

	if got := runtimesettings.EnvVar("log.system.max-size"); got != "PHENIX_LOG_SYSTEM_MAX_SIZE" {
		t.Errorf("EnvVar(log.system.max-size) = %q, want PHENIX_LOG_SYSTEM_MAX_SIZE", got)
	}
}
