package settings_test

import (
	"errors"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/spf13/viper"

	"phenix/api/runtimesettings"
	"phenix/api/settings"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v2 "phenix/types/version/v2"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
	"phenix/util/plog/plogtest"
)

// defaultSettings are the settings of a new store in a process that added no
// log file handler, whose log file limits are 0.
var defaultSettings = settings.Settings{ //nolint:gochecknoglobals // test fixture
	PasswordSettings: defaultPasswordSettings,
	LoggingSettings:  settings.LoggingSettings{MaxFileRotations: 0, MaxFileSize: 0, MaxFileAge: 0},
	TimeoutSettings: settings.TimeoutSettings{
		Enabled:    false,
		TimeoutMin: settings.DefaultTimeoutMin,
		WarningMin: settings.DefaultWarningMin,
	},
	MinimegaSettings: settings.MinimegaSettings{
		ReadConnections: mmcli.DefaultReadOnlyLimit,
		Source:          runtimesettings.SourceDefault,
	},
}

// setting makes a setting of the given category, name and value.
func setting(category, name, value string) types.Setting {
	return types.Setting{Spec: &v2.Setting{Category: category, Name: name, Value: value}}
}

// storeSettings stores each setting in the phenix store as SetDefaults does.
func storeSettings(t *testing.T, specs ...v2.Setting) {
	t.Helper()

	for _, spec := range specs {
		c := store.Config{
			Version:  "phenix.sandia.gov/v2",
			Kind:     "Setting",
			Metadata: store.ConfigMetadata{Name: settings.GetStoreName(spec.Category, spec.Name)},
			Spec:     structs.MapDefaultCase(&spec, structs.CASESNAKE),
		}

		if err := store.Create(&c); err != nil {
			t.Fatalf("store.Create(%s) error = %v", c.Metadata.Name, err)
		}
	}
}

// defaultsExcept returns the default settings without the one of the given
// category and name.
func defaultsExcept(category, name string) []v2.Setting {
	var specs []v2.Setting

	for _, spec := range settings.DefaultSettings {
		if spec.Category != category || spec.Name != name {
			specs = append(specs, spec)
		}
	}

	return specs
}

// resetFileLogger sets the plog file logger limits back to zero, as a new
// process has them, when the test ends.
func resetFileLogger(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		plog.ChangeMaxLogFileBackups(0)
		plog.ChangeMaxLogFileSize(0)
		plog.ChangeMaxLogFileAge(0)
	})
}

// GetTimeoutSettingsFromList reads only the timeout settings with known
// names.
func TestGetTimeoutSettingsFromList(t *testing.T) {
	t.Parallel()

	list := []types.Setting{
		setting("Timeout", "Enabled", "true"),
		setting("Timeout", "TimeoutMin", "12.5"),
		setting("Timeout", "WarningMin", "2"),
		setting("Timeout", "Unknown", "x"),
		setting("Logging", "Enabled", "x"),
	}

	got, err := settings.GetTimeoutSettingsFromList(list)
	if err != nil {
		t.Fatalf("GetTimeoutSettingsFromList() error = %v", err)
	}

	want := settings.TimeoutSettings{Enabled: true, TimeoutMin: 12.5, WarningMin: 2}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetTimeoutSettingsFromList() mismatch (-want +got):\n%s", diff)
	}
}

// GetTimeoutSettingsFromList returns an error for a value it cannot parse.
func TestGetTimeoutSettingsFromListParseErrors(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Enabled", "TimeoutMin", "WarningMin"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			list := []types.Setting{setting("Timeout", name, "not a value")}
			if _, err := settings.GetTimeoutSettingsFromList(list); err == nil {
				t.Errorf("GetTimeoutSettingsFromList(%s = not a value) error = nil, want an error", name)
			}
		})
	}
}

// GetSettings and GetTimeoutSettings give the default settings of a new
// store.
func TestGetSettingsDefaults(t *testing.T) {
	useSettingsStore(t)

	got, err := settings.GetSettings(nil)
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}

	if diff := cmp.Diff(&defaultSettings, got); diff != "" {
		t.Errorf("GetSettings() mismatch (-want +got):\n%s", diff)
	}

	timeout, err := settings.GetTimeoutSettings()
	if err != nil {
		t.Fatalf("GetTimeoutSettings() error = %v", err)
	}

	if diff := cmp.Diff(defaultSettings.TimeoutSettings, timeout); diff != "" {
		t.Errorf("GetTimeoutSettings() mismatch (-want +got):\n%s", diff)
	}
}

// GetSettings and GetTimeoutSettings return the error of a store that cannot
// list settings.
func TestGetSettingsStoreError(t *testing.T) {
	useFailingStore(t)

	if _, err := settings.GetSettings(nil); err == nil {
		t.Error("GetSettings() error = nil, want an error")
	}

	if _, err := settings.GetTimeoutSettings(); err == nil {
		t.Error("GetTimeoutSettings() error = nil, want an error")
	}
}

// GetSettings returns an error when a stored setting of any category does
// not parse.
func TestGetSettingsParseErrors(t *testing.T) {
	for _, test := range []struct{ category, name string }{
		{"Password", "MinLength"},
		{"Timeout", "Enabled"},
	} {
		t.Run(test.category, func(t *testing.T) {
			useSettingsStore(t)

			if err := settings.Update(test.category, test.name, "not a value"); err != nil {
				t.Fatalf("Update(%s, %s) error = %v", test.category, test.name, err)
			}

			if _, err := settings.GetSettings(nil); err == nil {
				t.Errorf("GetSettings() with a bad %s.%s error = nil, want an error", test.category, test.name)
			}
		})
	}
}

// UpdateAllSettings stores every changed setting, writes the log file limits
// to the config file, applies them at once, and logs that they changed.
func TestUpdateAllSettings(t *testing.T) {
	useSettingsStore(t)
	resetFileLogger(t)

	logs := plogtest.Capture(t)
	rs, path := newRuntime(t, "# kept\n", nil, nil)

	want := settings.Settings{
		PasswordSettings: settings.PasswordSettings{MinLength: 12, UppercaseReq: true},
		LoggingSettings:  settings.LoggingSettings{MaxFileRotations: 4, MaxFileSize: 20, MaxFileAge: 5},
		TimeoutSettings:  settings.TimeoutSettings{Enabled: true, TimeoutMin: 45.5, WarningMin: 1},
		MinimegaSettings: settings.MinimegaSettings{ReadConnections: 9, Source: runtimesettings.SourceDefault},
	}

	t.Cleanup(func() { mmcli.SetReadOnlyLimit(mmcli.DefaultReadOnlyLimit) })

	if err := settings.UpdateAllSettings(rs, want); err != nil {
		t.Fatalf("UpdateAllSettings() error = %v", err)
	}

	if got := mmcli.ReadOnlyLimit(); got != 9 {
		t.Errorf("mmcli.ReadOnlyLimit() after UpdateAllSettings() = %d, want 9", got)
	}

	wantFile := "# kept\nlog:\n    system:\n        max-size: 20\n        max-backups: 4\n        max-age: 5\n" +
		"minimega:\n    read-connections: 9\n"
	if got := readFile(t, path); got != wantFile {
		t.Errorf("config file after UpdateAllSettings() = %q, want %q", got, wantFile)
	}

	wantLimits := plog.FileHandlerOpts{MaxSize: 20, MaxAge: 5, MaxBackups: 4}
	if got := plog.LogFileLimits(); got != wantLimits {
		t.Errorf("plog.LogFileLimits() after UpdateAllSettings() = %+v, want %+v", got, wantLimits)
	}

	got, err := settings.GetSettings(nil)
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}

	if diff := cmp.Diff(&want, got); diff != "" {
		t.Errorf("GetSettings() after UpdateAllSettings() mismatch (-want +got):\n%s", diff)
	}

	if n := len(logs.Records(t, plogtest.Message("Updated logging settings successfully"))); n != 1 {
		t.Errorf("UpdateAllSettings() logged %d logging updates, want 1", n)
	}

	// The same settings again change nothing, also without a config file.
	if err := settings.UpdateAllSettings(runtimesettings.New(viper.New()), want); err != nil {
		t.Fatalf("UpdateAllSettings() a second time error = %v", err)
	}

	if n := len(logs.Records(t, plogtest.Message("Updated logging settings successfully"))); n != 1 {
		t.Errorf("UpdateAllSettings() of the same settings logged %d logging updates in all, want 1", n)
	}
}

// UpdateAllSettings returns an error when a setting it must update is not
// stored.
func TestUpdateAllSettingsMissingSetting(t *testing.T) {
	for _, test := range []struct{ category, name string }{
		{"Password", "NumberReq"},
		{"Timeout", "Enabled"},
		{"Timeout", "TimeoutMin"},
		{"Timeout", "WarningMin"},
	} {
		t.Run(test.category+"."+test.name, func(t *testing.T) {
			useMemoryStore(t)
			resetFileLogger(t)
			storeSettings(t, defaultsExcept(test.category, test.name)...)

			changed := defaultSettings
			changed.TimeoutSettings.WarningMin = 2

			if err := settings.UpdateAllSettings(runtimesettings.New(viper.New()), changed); err == nil {
				t.Errorf("UpdateAllSettings() without %s.%s error = nil, want an error", test.category, test.name)
			}
		})
	}
}

// UpdateWithVerification stores a value only when it parses as the type of
// the setting.
func TestUpdateWithVerification(t *testing.T) {
	tests := []struct {
		category, name, value string
		wantErr               bool
	}{
		{category: "Password", name: "NumberReq", value: "true"},
		{category: "Password", name: "NumberReq", value: "false"},
		{category: "Password", name: "NumberReq", value: "maybe", wantErr: true},
		{category: "Password", name: "MinLength", value: "10"},
		{category: "Password", name: "MinLength", value: "ten", wantErr: true},
		{category: "Timeout", name: "TimeoutMin", value: "2.5"},
		{category: "Timeout", name: "TimeoutMin", value: "two", wantErr: true},
		{category: "Test", name: "Text", value: "any text"},
		{category: "Test", name: "Missing", value: "x", wantErr: true},
	}

	useSettingsStore(t)
	storeSettings(t, v2.Setting{Category: "Test", Name: "Text", Type: v2.SettingValueString, Value: ""})

	for _, test := range tests {
		t.Run(test.category+"."+test.name+"="+test.value, func(t *testing.T) {
			before, _ := settings.GetSetting(test.category, test.name)

			err := settings.UpdateWithVerification(test.category, test.name, test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("UpdateWithVerification(%s, %s, %q) error = nil, want an error", test.category, test.name, test.value)
				}

				after, _ := settings.GetSetting(test.category, test.name)
				if diff := cmp.Diff(before, after); diff != "" {
					t.Errorf("setting after a refused update mismatch (-want +got):\n%s", diff)
				}

				return
			}

			if err != nil {
				t.Fatalf("UpdateWithVerification(%s, %s, %q) error = %v", test.category, test.name, test.value, err)
			}

			got, err := settings.GetSetting(test.category, test.name)
			if err != nil {
				t.Fatalf("GetSetting(%s, %s) error = %v", test.category, test.name, err)
			}

			if got.Spec.Value != test.value {
				t.Errorf("GetSetting(%s, %s).Spec.Value = %q, want %q", test.category, test.name, got.Spec.Value, test.value)
			}
		})
	}
}

// UpdateWithVerification of the stored value writes nothing, and a store
// that cannot write returns an error.
func TestUpdateWithVerificationStore(t *testing.T) {
	errStore := errors.New("injected store failure")

	stored := store.Config{
		Version:  "phenix.sandia.gov/v2",
		Kind:     "Setting",
		Metadata: store.ConfigMetadata{Name: "Password.MinLength"},
		Spec:     map[string]any{"category": "Password", "name": "MinLength", "type": "int", "value": "8"},
	}

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = stored

		return nil
	}).Times(2)
	m.EXPECT().Update(gomock.Any()).Return(errStore)

	storetest.Use(t, m)

	if err := settings.UpdateWithVerification("Password", "MinLength", "8"); err != nil {
		t.Errorf("UpdateWithVerification(Password, MinLength, 8) of the stored value error = %v, want nil", err)
	}

	if err := settings.UpdateWithVerification("Password", "MinLength", "9"); !errors.Is(err, errStore) {
		t.Errorf("UpdateWithVerification(Password, MinLength, 9) error = %v, want %v", err, errStore)
	}
}

// GetSetting returns an error for a stored spec that does not decode, and
// for a name the store cannot hold.
func TestGetSettingErrors(t *testing.T) {
	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		c.Spec = map[string]any{"value": map[string]any{"not": "a string"}}

		return nil
	})

	storetest.Use(t, m)

	if _, err := settings.GetSetting("Password", "MinLength"); err == nil {
		t.Error("GetSetting() of a spec that does not decode error = nil, want an error")
	}

	if _, err := settings.GetSetting("a/b", "c"); err == nil {
		t.Error("GetSetting(a/b, c) error = nil, want an error")
	}
}
