package settings_test

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"pgregory.net/rapid"

	"phenix/api/runtimesettings"
	"phenix/api/settings"
	"phenix/store"
	v2 "phenix/types/version/v2"
	"phenix/util/plog"
)

// newRuntime returns the runtime settings of a viper that read the YAML
// config file data, with the log file flags (the args set some of them) and
// the environment env, and the file's path.
func newRuntime(t *testing.T, data string, args []string, env map[string]string) (*runtimesettings.Settings, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	flags := pflag.NewFlagSet("phenix", pflag.ContinueOnError)
	flags.Int(settings.LogMaxSizeKey, 100, "")
	flags.Int(settings.LogMaxBackupsKey, 3, "")
	flags.Int(settings.LogMaxAgeKey, 90, "")

	if err := flags.Parse(args); err != nil {
		t.Fatalf("parsing flags %v: %v", args, err)
	}

	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig(%s) error = %v", path, err)
	}

	if err := v.BindPFlags(flags); err != nil {
		t.Fatalf("BindPFlags() error = %v", err)
	}

	rs := runtimesettings.New(v,
		runtimesettings.WithFlags(flags.Lookup),
		runtimesettings.WithLookupEnv(func(name string) (string, bool) {
			value, ok := env[name]

			return value, ok
		}),
	)

	return rs, path
}

// readFile returns the text of the file path.
func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(data)
}

// GetLoggingSettings gives the limits the system log file uses now, each in
// the int32 range.
func TestGetLoggingSettings(t *testing.T) {
	resetFileLogger(t)

	tests := []struct {
		size, backups, age int
		want               settings.LoggingSettings
	}{
		{want: settings.LoggingSettings{}},
		{size: 50, backups: 2, age: 7, want: settings.LoggingSettings{MaxFileSize: 50, MaxFileRotations: 2, MaxFileAge: 7}},
		{
			size: math.MaxInt32 + 1, backups: math.MinInt32 - 1, age: math.MaxInt32,
			want: settings.LoggingSettings{MaxFileSize: math.MaxInt32, MaxFileRotations: math.MinInt32, MaxFileAge: math.MaxInt32},
		},
	}

	for _, tt := range tests {
		plog.ChangeMaxLogFileSize(tt.size)
		plog.ChangeMaxLogFileBackups(tt.backups)
		plog.ChangeMaxLogFileAge(tt.age)

		if diff := cmp.Diff(tt.want, settings.GetLoggingSettings()); diff != "" {
			t.Errorf("GetLoggingSettings() with %d, %d, %d mismatch (-want +got):\n%s", tt.size, tt.backups, tt.age, diff)
		}
	}
}

// UpdateAllSettings refuses a change to a log file limit that is less than
// 0, that a flag or a PHENIX_ variable sets, or that it has no YAML config
// file for. A refused request changes no setting, no limit and no file.
func TestUpdateAllSettingsLogLimitRefusals(t *testing.T) {
	const config = "log:\n  level: info\n"

	toml := func(t *testing.T) *runtimesettings.Settings {
		t.Helper()

		path := filepath.Join(t.TempDir(), "config.toml")

		return runtimesettings.New(viper.New(), runtimesettings.WithFile(func() string { return path }))
	}

	tests := map[string]struct {
		logging  settings.LoggingSettings
		args     []string
		env      map[string]string
		runtime  func(t *testing.T) *runtimesettings.Settings
		wantErr  error
		wantText string
	}{
		"negative size": {
			logging: settings.LoggingSettings{MaxFileSize: -1}, wantErr: settings.ErrLogLimitNegative,
			wantText: "log.system.max-size must be 0 or more, not -1",
		},
		"negative backups": {
			logging: settings.LoggingSettings{MaxFileRotations: -2}, wantErr: settings.ErrLogLimitNegative,
			wantText: "log.system.max-backups",
		},
		"negative age after a good size": {
			logging: settings.LoggingSettings{MaxFileSize: 10, MaxFileAge: math.MinInt32}, wantErr: settings.ErrLogLimitNegative,
			wantText: "log.system.max-age",
		},
		"size from a flag": {
			logging: settings.LoggingSettings{MaxFileSize: 10}, args: []string{"--log.system.max-size=200"},
			wantErr: settings.ErrLogLimitOverridden, wantText: "--log.system.max-size flag",
		},
		"age from the environment": {
			logging: settings.LoggingSettings{MaxFileAge: 10}, env: map[string]string{"PHENIX_LOG_SYSTEM_MAX_AGE": "30"},
			wantErr: settings.ErrLogLimitOverridden, wantText: "PHENIX_LOG_SYSTEM_MAX_AGE environment variable",
		},
		"backups from the environment after a good size": {
			logging: settings.LoggingSettings{MaxFileSize: 10, MaxFileRotations: 1},
			env:     map[string]string{"PHENIX_LOG_SYSTEM_MAX_BACKUPS": "5"},
			wantErr: settings.ErrLogLimitOverridden, wantText: "PHENIX_LOG_SYSTEM_MAX_BACKUPS",
		},
		"no config file": {
			logging: settings.LoggingSettings{MaxFileSize: 10},
			runtime: func(*testing.T) *runtimesettings.Settings { return runtimesettings.New(viper.New()) },
			wantErr: runtimesettings.ErrNoConfigFile, wantText: "has no config file",
		},
		"config file that is not YAML": {
			logging: settings.LoggingSettings{MaxFileSize: 10}, runtime: toml, wantErr: runtimesettings.ErrNotYAML,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useSettingsStore(t)
			resetFileLogger(t)

			rs, path := newRuntime(t, config, tt.args, tt.env)
			if tt.runtime != nil {
				rs = tt.runtime(t)
			}

			requested := defaultSettings
			requested.PasswordSettings.MinLength = 20
			requested.TimeoutSettings.Enabled = true
			requested.LoggingSettings = tt.logging

			err := settings.UpdateAllSettings(rs, requested)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("UpdateAllSettings(%+v) error = %v, want %v", tt.logging, err, tt.wantErr)
			}

			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("UpdateAllSettings(%+v) error = %q, want it to name %q", tt.logging, err, tt.wantText)
			}

			got, err := settings.GetSettings()
			if err != nil {
				t.Fatalf("GetSettings() error = %v", err)
			}

			if diff := cmp.Diff(&defaultSettings, got); diff != "" {
				t.Errorf("settings after a refused UpdateAllSettings() mismatch (-want +got):\n%s", diff)
			}

			if got := readFile(t, path); got != config {
				t.Errorf("config file after a refused UpdateAllSettings() = %q, want %q", got, config)
			}
		})
	}
}

// UpdateAllSettings writes a log file limit whose value comes from the config
// file or the default, also when a flag or a PHENIX_ variable sets another
// limit or the same value, and leaves out a limit that does not change.
func TestUpdateAllSettingsLogLimitSources(t *testing.T) {
	useSettingsStore(t)
	resetFileLogger(t)

	plog.ChangeMaxLogFileSize(200)
	plog.ChangeMaxLogFileAge(30)

	rs, path := newRuntime(t, "log:\n  system:\n    max-backups: 1\n",
		[]string{"--log.system.max-size=200"}, map[string]string{"PHENIX_LOG_SYSTEM_MAX_AGE": "30"})

	requested := defaultSettings
	requested.LoggingSettings = settings.LoggingSettings{MaxFileSize: 200, MaxFileRotations: 4, MaxFileAge: 30}

	if err := settings.UpdateAllSettings(rs, requested); err != nil {
		t.Fatalf("UpdateAllSettings() error = %v", err)
	}

	if want, got := "log:\n  system:\n    max-backups: 4\n", readFile(t, path); got != want {
		t.Errorf("config file after UpdateAllSettings() = %q, want %q", got, want)
	}

	if diff := cmp.Diff(requested.LoggingSettings, settings.GetLoggingSettings()); diff != "" {
		t.Errorf("GetLoggingSettings() after UpdateAllSettings() mismatch (-want +got):\n%s", diff)
	}
}

// A request either changes each log file limit to its value, in the config
// file and at once, or, when a changed limit is less than 0, changes none.
func TestUpdateAllSettingsLogLimitsProperty(t *testing.T) {
	useSettingsStore(t)
	resetFileLogger(t)

	dir := t.TempDir()
	limit := rapid.OneOf(rapid.Int32Range(-3, 3), rapid.Int32())
	run := 0

	rapid.Check(t, func(rt *rapid.T) {
		run++

		path := filepath.Join(dir, fmt.Sprintf("config-%d.yaml", run))
		rs := runtimesettings.New(viper.New(), runtimesettings.WithFile(func() string { return path }))

		plog.ChangeMaxLogFileSize(100)
		plog.ChangeMaxLogFileBackups(3)
		plog.ChangeMaxLogFileAge(90)

		before := settings.GetLoggingSettings()
		requested := settings.LoggingSettings{
			MaxFileSize:      limit.Draw(rt, "size"),
			MaxFileRotations: limit.Draw(rt, "backups"),
			MaxFileAge:       limit.Draw(rt, "age"),
		}

		refused := (requested.MaxFileSize != before.MaxFileSize && requested.MaxFileSize < 0) ||
			(requested.MaxFileRotations != before.MaxFileRotations && requested.MaxFileRotations < 0) ||
			(requested.MaxFileAge != before.MaxFileAge && requested.MaxFileAge < 0)

		s := defaultSettings
		s.LoggingSettings = requested

		err := settings.UpdateAllSettings(rs, s)
		if refused != errors.Is(err, settings.ErrLogLimitNegative) || (!refused && err != nil) {
			rt.Fatalf("UpdateAllSettings(%+v) error = %v, want refused %t", requested, err, refused)
		}

		want := requested
		if refused {
			want = before
		}

		if diff := cmp.Diff(want, settings.GetLoggingSettings()); diff != "" {
			rt.Fatalf("GetLoggingSettings() after UpdateAllSettings(%+v) mismatch (-want +got):\n%s", requested, diff)
		}

		if requested == before || refused {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				rt.Fatalf("UpdateAllSettings(%+v) wrote %s, want no file", requested, path)
			}

			return
		}

		file := viper.New()
		file.SetConfigFile(path)

		if err := file.ReadInConfig(); err != nil {
			rt.Fatalf("reading %s: %v", path, err)
		}

		for _, limit := range []struct {
			key           string
			before, value int32
		}{
			{settings.LogMaxSizeKey, before.MaxFileSize, requested.MaxFileSize},
			{settings.LogMaxBackupsKey, before.MaxFileRotations, requested.MaxFileRotations},
			{settings.LogMaxAgeKey, before.MaxFileAge, requested.MaxFileAge},
		} {
			changed := limit.value != limit.before
			if file.IsSet(limit.key) != changed {
				rt.Fatalf("config file has %s = %t, want %t", limit.key, file.IsSet(limit.key), changed)
			}

			if got := file.GetInt(limit.key); changed && got != int(limit.value) {
				rt.Fatalf("config file %s = %d, want %d", limit.key, got, limit.value)
			}
		}
	})
}

// legacyLoggingSettings are the Logging settings that older versions stored.
func legacyLoggingSettings() []v2.Setting {
	return []v2.Setting{
		{Category: "Logging", Name: "MaxFileRotations", Type: v2.SettingValueInt, Value: "0"},
		{Category: "Logging", Name: "MaxFileSize", Type: v2.SettingValueInt, Value: "100"},
		{Category: "Logging", Name: "MaxFileAge", Type: v2.SettingValueInt, Value: "90"},
	}
}

// storedCategories returns the category of each setting in the store.
func storedCategories(t *testing.T) []string {
	t.Helper()

	configs, err := store.List("Setting")
	if err != nil {
		t.Fatalf("store.List(Setting) error = %v", err)
	}

	categories := make([]string, 0, len(configs))

	for _, c := range configs {
		category, _ := c.Spec["category"].(string)
		categories = append(categories, category)
	}

	return categories
}

// List hides the Logging settings of an old store, and SetDefaults deletes
// them once and keeps the other settings as they are.
func TestLegacyLoggingSettings(t *testing.T) {
	useMemoryStore(t)

	stored := slices.Clone(settings.DefaultSettings)
	stored[0].Value = "false"

	storeSettings(t, append(legacyLoggingSettings(), stored...)...)

	list, err := settings.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	for _, s := range list {
		if s.Spec.Category == "Logging" {
			t.Errorf("List() has %s, want no Logging settings", s.Metadata.Name)
		}
	}

	if n := len(list); n != len(settings.DefaultSettings) {
		t.Errorf("List() has %d settings, want %d", n, len(settings.DefaultSettings))
	}

	if err := settings.SetDefaults(); err != nil {
		t.Fatalf("SetDefaults() of an old store error = %v", err)
	}

	for _, category := range storedCategories(t) {
		if category == "Logging" {
			t.Errorf("store after SetDefaults() has a Logging setting, want none")
		}
	}

	if n := len(storedCategories(t)); n != len(settings.DefaultSettings) {
		t.Errorf("store after SetDefaults() has %d settings, want %d", n, len(settings.DefaultSettings))
	}

	got, err := settings.GetSetting(stored[0].Category, stored[0].Name)
	if err != nil {
		t.Fatalf("GetSetting(%s, %s) error = %v", stored[0].Category, stored[0].Name, err)
	}

	if diff := cmp.Diff(stored[0], *got.Spec); diff != "" {
		t.Errorf("setting after SetDefaults() mismatch (-want +got):\n%s", diff)
	}

	if err := settings.SetDefaults(); err != nil {
		t.Errorf("SetDefaults() a second time error = %v", err)
	}
}

// The default settings have no Logging category.
func TestDefaultSettingsHaveNoLogging(t *testing.T) {
	t.Parallel()

	for _, s := range settings.DefaultSettings {
		if s.Category == "Logging" {
			t.Errorf("DefaultSettings has %s.%s, want no Logging settings", s.Category, s.Name)
		}
	}
}
