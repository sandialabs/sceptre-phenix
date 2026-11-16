package settings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/viper"

	"phenix/api/runtimesettings"
	"phenix/api/settings"
	"phenix/util/mm/mmcli"
)

// The minimega settings name the limit in use and where it comes from: a
// flag or a PHENIX_ variable wins over the config file.
func TestGetMinimegaSettings(t *testing.T) { //nolint:paralleltest // reads the process-wide limit
	for name, tc := range map[string]struct {
		config string
		args   []string
		env    map[string]string
		want   string
	}{
		"default":     {want: runtimesettings.SourceDefault},
		"config file": {config: "minimega:\n  read-connections: 9\n", want: runtimesettings.SourceFile},
		"flag": {
			config: "minimega:\n  read-connections: 9\n", args: []string{"--minimega.read-connections=20"},
			want: runtimesettings.SourceFlag,
		},
		"environment": {
			env: map[string]string{"PHENIX_MINIMEGA_READ_CONNECTIONS": "12"}, want: runtimesettings.SourceEnv,
		},
	} {
		t.Run(name, func(t *testing.T) {
			rs, _ := newRuntime(t, tc.config, tc.args, tc.env)

			want := settings.MinimegaSettings{ReadConnections: int32(mmcli.ReadOnlyLimit()), Source: tc.want}
			if diff := cmp.Diff(want, settings.GetMinimegaSettings(rs)); diff != "" {
				t.Errorf("GetMinimegaSettings() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	if got := settings.GetMinimegaSettings(nil).Source; got != runtimesettings.SourceDefault {
		t.Errorf("GetMinimegaSettings(nil).Source = %q, want %q", got, runtimesettings.SourceDefault)
	}
}

// UpdateAllSettings refuses a read_connections outside 3 to 64, one that a
// flag or a PHENIX_ variable sets, and one it has no config file for. A
// refused request changes neither the limit nor the file. 0, as from a client
// that does not know the setting, changes nothing.
func TestUpdateAllSettingsMinimegaRefusals(t *testing.T) { //nolint:paralleltest // changes the process-wide limit
	const config = "log:\n  level: info\n"

	for name, tc := range map[string]struct {
		value    int32
		args     []string
		env      map[string]string
		runtime  func() *runtimesettings.Settings
		wantErr  error
		wantText string
	}{
		"too few":  {value: 2, wantErr: settings.ErrReadConnectionsRange, wantText: "3 to 64, not 2"},
		"too many": {value: 65, wantErr: settings.ErrReadConnectionsRange, wantText: "not 65"},
		"from a flag": {
			value: 9, args: []string{"--minimega.read-connections=20"},
			wantErr: settings.ErrReadConnectionsOverridden, wantText: "--minimega.read-connections flag",
		},
		"from the environment": {
			value: 9, env: map[string]string{"PHENIX_MINIMEGA_READ_CONNECTIONS": "12"},
			wantErr: settings.ErrReadConnectionsOverridden, wantText: "PHENIX_MINIMEGA_READ_CONNECTIONS",
		},
		"no config file": {
			value: 9, runtime: func() *runtimesettings.Settings { return runtimesettings.New(viper.New()) },
			wantErr: runtimesettings.ErrNoConfigFile, wantText: "has no config file",
		},
	} {
		t.Run(name, func(t *testing.T) {
			useSettingsStore(t)
			resetFileLogger(t)

			rs, path := newRuntime(t, config, tc.args, tc.env)
			if tc.runtime != nil {
				rs = tc.runtime()
			}

			requested := defaultSettings
			requested.MinimegaSettings.ReadConnections = tc.value

			err := settings.UpdateAllSettings(rs, requested)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("UpdateAllSettings(read_connections %d) error = %v, want %v", tc.value, err, tc.wantErr)
			}

			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("UpdateAllSettings(read_connections %d) error = %q, want it to name %q", tc.value, err, tc.wantText)
			}

			if got := mmcli.ReadOnlyLimit(); got != mmcli.DefaultReadOnlyLimit {
				t.Errorf("mmcli.ReadOnlyLimit() after a refused change = %d, want %d", got, mmcli.DefaultReadOnlyLimit)
			}

			if got := readFile(t, path); got != config {
				t.Errorf("config file after a refused change = %q, want %q", got, config)
			}
		})
	}

	t.Run("unknown to the client", func(t *testing.T) {
		useSettingsStore(t)
		resetFileLogger(t)

		rs, path := newRuntime(t, config, nil, nil)

		requested := defaultSettings
		requested.MinimegaSettings = settings.MinimegaSettings{}

		if err := settings.UpdateAllSettings(rs, requested); err != nil {
			t.Fatalf("UpdateAllSettings(no read_connections) error = %v", err)
		}

		if got := readFile(t, path); got != config {
			t.Errorf("config file after UpdateAllSettings(no read_connections) = %q, want %q", got, config)
		}
	})
}
