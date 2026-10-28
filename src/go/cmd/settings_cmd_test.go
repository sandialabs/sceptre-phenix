package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"

	"phenix/api/settings"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/printer"
)

// settingsCmdTB is the part of [testing.TB] and [*rapid.T] that the settings
// command helpers use.
type settingsCmdTB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// settingsCmdViper returns a new viper that uses the config file path, and
// reads it when it exists, as phenix reads its config file.
func settingsCmdViper(t settingsCmdTB, path string) *viper.Viper {
	t.Helper()

	v := viper.New()
	v.SetConfigFile(path)

	if _, err := os.Stat(path); err == nil {
		if err := v.ReadInConfig(); err != nil {
			t.Fatalf("ReadInConfig(%s) error = %v", path, err)
		}
	}

	return v
}

// settingsCmdRun runs "phenix settings <args>" over the settings of v and
// returns what the command wrote to its output.
func settingsCmdRun(v *viper.Viper, args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newSettingsCommands(v))
	root.SetArgs(append([]string{"settings"}, args...))

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})

	_, err := root.ExecuteC()

	return out.String(), err
}

// settingsCmdReadYAML decodes the YAML file path.
func settingsCmdReadYAML(t settingsCmdTB, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}

	got := map[string]any{}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal(%s) error = %v", path, err)
	}

	return got
}

// settings set writes the value at the dotted key in the config file. It
// keeps the other keys and matches an existing key without regard to case.
// The root command of settingsCmdRun has no flags, so no key is a known
// setting and each value gets the type inferType gives it.
func TestSettingsCmdSetWritesValue(t *testing.T) {
	t.Parallel()

	const original = "log:\n  level: info\n  system:\n    max-size: 100\nui:\n  users: [a]\n"

	tests := []struct {
		name       string
		key, value string
		want       map[string]any
	}{
		{
			name: "string", key: "log.level", value: "debug",
			want: map[string]any{
				"log": map[string]any{"level": "debug", "system": map[string]any{"max-size": 100}},
				"ui":  map[string]any{"users": []any{"a"}},
			},
		},
		{
			name: "int in a new section", key: "log.system.max-age", value: "7",
			want: map[string]any{
				"log": map[string]any{"level": "info", "system": map[string]any{"max-size": 100, "max-age": 7}},
				"ui":  map[string]any{"users": []any{"a"}},
			},
		},
		{
			name: "key in other case", key: "LOG.Level", value: "TRUE",
			want: map[string]any{
				"log": map[string]any{"level": true, "system": map[string]any{"max-size": 100}},
				"ui":  map[string]any{"users": []any{"a"}},
			},
		},
		{
			name: "new nested key", key: "ui.logs.level", value: "1.5",
			want: map[string]any{
				"log": map[string]any{"level": "info", "system": map[string]any{"max-size": 100}},
				"ui":  map[string]any{"users": []any{"a"}, "logs": map[string]any{"level": 1.5}},
			},
		},
		{
			name: "leaf becomes a section", key: "log.level.x", value: "false",
			want: map[string]any{
				"log": map[string]any{"level": map[string]any{"x": false}, "system": map[string]any{"max-size": 100}},
				"ui":  map[string]any{"users": []any{"a"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeTestFile(t, t.TempDir(), "config.yaml", original)

			if _, err := settingsCmdRun(settingsCmdViper(t, path), "set", tc.key, tc.value); err != nil {
				t.Fatalf("settings set %s %s error = %v", tc.key, tc.value, err)
			}

			if diff := cmp.Diff(tc.want, settingsCmdReadYAML(t, path)); diff != "" {
				t.Errorf("settings set %s %s: config file mismatch (-want +got):\n%s", tc.key, tc.value, diff)
			}
		})
	}
}

// settings set writes block YAML into an empty mapping such as "{}", and
// keeps the flow style of a mapping that has keys.
func TestSettingsCmdSetEmptyFlowMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, original string
		sets           [][2]string
		want           string
	}{
		{
			name: "empty root", original: "{}\n",
			sets: [][2]string{{"ui.jwt-signing-key", "k0123"}, {"ui.users", "a b"}},
			want: "ui:\n    jwt-signing-key: k0123\n    users: a b\n",
		},
		{
			name: "empty section", original: "log:\n  level: info\nui: {}\n",
			sets: [][2]string{{"ui.x", "1"}},
			want: "log:\n  level: info\nui:\n  x: 1\n",
		},
		{
			name: "flow root with keys", original: "{a: 1}\n",
			sets: [][2]string{{"b", "2"}},
			want: "{a: 1, b: 2}\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeTestFile(t, t.TempDir(), "config.yaml", tc.original)

			for _, set := range tc.sets {
				if _, err := settingsCmdRun(settingsCmdViper(t, path), "set", set[0], set[1]); err != nil {
					t.Fatalf("settings set %s %s error = %v", set[0], set[1], err)
				}
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("os.ReadFile(%s) error = %v", path, err)
			}

			if diff := cmp.Diff(tc.want, string(got)); diff != "" {
				t.Errorf("settings set on %q: config file mismatch (-want +got):\n%s", tc.original, diff)
			}
		})
	}
}

// settings set creates the config file and its directory when they do not
// exist, and logs the file it changed.
func TestSettingsCmdSetCreatesFile(t *testing.T) { //nolint:paralleltest // captures the global logger
	logs := captureLogs(t)
	path := filepath.Join(t.TempDir(), "new", "dir", "config.yaml")

	if _, err := settingsCmdRun(settingsCmdViper(t, path), "set", "log.level", "debug"); err != nil {
		t.Fatalf("settings set log.level debug error = %v", err)
	}

	want := map[string]any{"log": map[string]any{"level": "debug"}}
	if diff := cmp.Diff(want, settingsCmdReadYAML(t, path)); diff != "" {
		t.Errorf("new config file mismatch (-want +got):\n%s", diff)
	}

	records := logs.all(t, "configuration updated")
	files := make([]any, 0, len(records))

	for _, record := range records {
		files = append(files, record["file"])
	}

	if diff := cmp.Diff([]any{path}, files); diff != "" {
		t.Errorf("configuration updated logs mismatch (-want +got):\n%s", diff)
	}
}

// settings set refuses a config file that is not YAML or does not parse, and
// leaves the file as it was.
func TestSettingsCmdSetRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, file, data string
		args             []string
		wantErr          error
	}{
		{name: "toml", file: "config.toml", data: "[log]\nlevel = \"info\"\n", args: []string{"log.level", "debug"}, wantErr: errNotYAML},
		{name: "bad yaml", file: "config.yaml", data: "log: [\n", args: []string{"log.level", "debug"}},
		{name: "not a map", file: "config.yaml", data: "- a\n- b\n", args: []string{"log.level", "debug"}},
		{name: "one arg", file: "config.yaml", data: "log:\n  level: info\n", args: []string{"log.level"}},
		{name: "three args", file: "config.yaml", data: "log:\n  level: info\n", args: []string{"log.level", "a", "b"}},
		{
			name: "text yaml cannot hold", file: "config.yaml", data: "log:\n  level: info\n",
			args: []string{"log.level", "\t\n1"}, wantErr: errTextNotYAML,
		},
		{
			name: "text that starts with a newline", file: "config.yaml", data: "log:\n  level: info\n",
			args: []string{"log.level", "\n0"}, wantErr: errTextNotYAML,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeTestFile(t, t.TempDir(), tc.file, tc.data)
			v := viper.New()
			v.SetConfigFile(path)

			_, err := settingsCmdRun(v, append([]string{"set"}, tc.args...)...)
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Errorf("settings set %v error = %v, want an error that is %v", tc.args, err, tc.wantErr)
			}

			if got, _ := os.ReadFile(path); string(got) != tc.data {
				t.Errorf("settings set %v changed the file to %q, want %q", tc.args, got, tc.data)
			}
		})
	}
}

// settings unset removes the key from the config file, matching it without
// regard to case, and removes each section that the delete leaves empty. A
// key that is not in the file changes nothing.
func TestSettingsCmdUnsetRemovesKey(t *testing.T) {
	t.Parallel()

	const original = "log:\n  level: info\n  system:\n    max-size: 100\nui:\n  logs: {}\n  users: [a]\n"

	tests := []struct {
		name string
		key  string
		want map[string]any
	}{
		{
			name: "leaf with a sibling", key: "log.level",
			want: map[string]any{
				"log": map[string]any{"system": map[string]any{"max-size": 100}},
				"ui":  map[string]any{"logs": map[string]any{}, "users": []any{"a"}},
			},
		},
		{
			name: "last leaf empties two sections", key: "LOG.System.Max-Size",
			want: map[string]any{
				"log": map[string]any{"level": "info"},
				"ui":  map[string]any{"logs": map[string]any{}, "users": []any{"a"}},
			},
		},
		{
			name: "whole section", key: "ui",
			want: map[string]any{
				"log": map[string]any{"level": "info", "system": map[string]any{"max-size": 100}},
			},
		},
		{
			name: "missing key keeps an empty section", key: "ui.logs.level",
			want: map[string]any{
				"log": map[string]any{"level": "info", "system": map[string]any{"max-size": 100}},
				"ui":  map[string]any{"logs": map[string]any{}, "users": []any{"a"}},
			},
		},
		{
			name: "path through a leaf", key: "log.level.x",
			want: map[string]any{
				"log": map[string]any{"level": "info", "system": map[string]any{"max-size": 100}},
				"ui":  map[string]any{"logs": map[string]any{}, "users": []any{"a"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeTestFile(t, t.TempDir(), "config.yaml", original)

			if _, err := settingsCmdRun(settingsCmdViper(t, path), "unset", tc.key); err != nil {
				t.Fatalf("settings unset %s error = %v", tc.key, err)
			}

			if diff := cmp.Diff(tc.want, settingsCmdReadYAML(t, path)); diff != "" {
				t.Errorf("settings unset %s: config file mismatch (-want +got):\n%s", tc.key, diff)
			}
		})
	}
}

// settings unset --all deletes the config file, also one that is not YAML.
// Wrong arguments, a missing file, or a key in a file that is not YAML or
// does not parse give an error and leave the file as it was.
func TestSettingsCmdUnsetAllAndRefusals(t *testing.T) {
	t.Parallel()

	t.Run("all", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{"config.yaml", "config.toml"} {
			path := writeTestFile(t, t.TempDir(), name, "log:\n  level: info\n")
			v := viper.New()
			v.SetConfigFile(path)

			if _, err := settingsCmdRun(v, "unset", "--all"); err != nil {
				t.Errorf("settings unset --all (%s) error = %v", name, err)
			}

			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("after settings unset --all, os.Stat(%s) error = %v, want not exist", name, err)
			}
		}
	})

	tests := []struct {
		name, file, data string
		args             []string
		wantErr          error
	}{
		{name: "no key", file: "config.yaml", data: "a: 1\n"},
		{name: "two keys", file: "config.yaml", data: "a: 1\n", args: []string{"a", "b"}},
		{name: "all with key", file: "config.yaml", data: "a: 1\n", args: []string{"--all", "a"}},
		{name: "toml", file: "config.toml", data: "a = 1\n", args: []string{"a"}, wantErr: errNotYAML},
		{name: "bad yaml", file: "config.yaml", data: "a: [\n", args: []string{"a"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeTestFile(t, t.TempDir(), tc.file, tc.data)
			v := viper.New()
			v.SetConfigFile(path)

			_, err := settingsCmdRun(v, append([]string{"unset"}, tc.args...)...)
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Errorf("settings unset %v error = %v, want an error that is %v", tc.args, err, tc.wantErr)
			}

			if got, _ := os.ReadFile(path); string(got) != tc.data {
				t.Errorf("settings unset %v changed the file to %q, want %q", tc.args, got, tc.data)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		for _, args := range [][]string{{"a"}, {"--all"}} {
			path := filepath.Join(t.TempDir(), "config.yaml")
			v := viper.New()
			v.SetConfigFile(path)

			if _, err := settingsCmdRun(v, append([]string{"unset"}, args...)...); err == nil {
				t.Errorf("settings unset %v with no config file error = nil, want an error", args)
			}

			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("settings unset %v created %s (os.Stat error = %v)", args, path, err)
			}
		}
	})
}

// settingsCmdGenConfig generates a config without empty sections, with keys
// from a small set so that generated keys often share a section with it.
func settingsCmdGenConfig(depth int) *rapid.Generator[map[string]any] {
	leaf := rapid.OneOf(
		rapid.Map(rapid.IntRange(-5, 500), func(i int) any { return i }),
		rapid.Map(rapid.SampledFrom([]string{"info", "debug", "/tmp/x", "a b"}), func(s string) any { return s }),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
	)

	value := leaf
	if depth > 0 {
		value = rapid.OneOf(leaf, rapid.Map(settingsCmdGenConfig(depth-1), func(m map[string]any) any { return m }))
	}

	return rapid.MapOfN(rapid.SampledFrom([]string{"a", "b", "log", "ui"}), value, 1, 3)
}

// settingsCmdFreePath reports whether config has no value at path and no leaf
// at a prefix of path, matching keys without regard to case.
func settingsCmdFreePath(config map[string]any, path []string) bool {
	m := config

	for i, part := range path {
		var (
			next  any
			found bool
		)

		for k, v := range m {
			if strings.EqualFold(k, part) {
				next, found = v, true
			}
		}

		if !found {
			return true
		}

		child, isMap := next.(map[string]any)
		if !isMap || i == len(path)-1 {
			return false
		}

		m = child
	}

	return true
}

// For a config and a key that the config does not have, settings set and then
// settings unset give back the config. After the set, phenix reads the value
// at the key with the type inferType gives it.
func TestSettingsCmdSetUnsetRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	rapid.Check(t, func(rt *rapid.T) {
		config := settingsCmdGenConfig(2).Draw(rt, "config")
		path := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "B", "log", "Ui", "x", "level"}), 1, 3).
			Filter(func(p []string) bool { return settingsCmdFreePath(config, p) }).
			Draw(rt, "path")
		value := rapid.SampledFrom([]string{"debug", "200", "true", "False", "1.5", "1e5", "007", ""}).Draw(rt, "value")
		key := strings.Join(path, ".")

		data, err := yaml.Marshal(config)
		if err != nil {
			rt.Fatalf("yaml.Marshal(%v) error = %v", config, err)
		}

		file := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(file, data, 0o600); err != nil {
			rt.Fatalf("os.WriteFile() error = %v", err)
		}

		want := settingsCmdReadYAML(rt, file)

		if _, err := settingsCmdRun(settingsCmdViper(rt, file), "set", key, value); err != nil {
			rt.Fatalf("settings set %s %q error = %v", key, value, err)
		}

		if got := settingsCmdViper(rt, file).Get(key); !cmp.Equal(got, inferType(value)) {
			rt.Errorf("after settings set %s %q, Get(%s) = %#v, want %#v", key, value, key, got, inferType(value))
		}

		if _, err := settingsCmdRun(settingsCmdViper(rt, file), "unset", key); err != nil {
			rt.Fatalf("settings unset %s error = %v", key, err)
		}

		if diff := cmp.Diff(want, settingsCmdReadYAML(rt, file)); diff != "" {
			rt.Errorf("settings set %s %q, then unset %s: config mismatch (-want +got):\n%s", key, value, key, diff)
		}
	})
}

// settingsCmdListViper returns a viper with a config file, a flag default and
// a list from a flag, as phenix ui binds ui.users.
func settingsCmdListViper(t *testing.T) *viper.Viper {
	t.Helper()

	path := writeTestFile(t, t.TempDir(), "config.yaml", "log:\n  level: debug\n  system:\n    max-size: 50\nui:\n  features: [vm-mount]\n")
	v := settingsCmdViper(t, path)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("bridge-mode", "manual", "")
	flags.StringSlice("users", []string{"admin:pw:Global Admin", "bob:pw:VM Viewer"}, "")

	if err := v.BindPFlag("bridge-mode", flags.Lookup("bridge-mode")); err != nil {
		t.Fatalf("BindPFlag(bridge-mode) error = %v", err)
	}

	if err := v.BindPFlag("ui.users", flags.Lookup("users")); err != nil {
		t.Fatalf("BindPFlag(ui.users) error = %v", err)
	}

	return v
}

// settingsCmdTable renders settings as the settings commands print a table.
func settingsCmdTable(settings map[string]any) string {
	var out bytes.Buffer

	printer.PrintTableOfRuntimeSettings(&out, settings)

	return out.String()
}

// settings get prints one setting, or all of them, in YAML (the default), JSON
// or a table. A list prints as a YAML list, also one from a flag.
func TestSettingsCmdGet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "yaml scalar", args: []string{"log.level"}, want: "debug\n"},
		{name: "yaml section", args: []string{"log.system"}, want: "max-size: 50\n"},
		{name: "yaml list from file", args: []string{"ui.features"}, want: "- vm-mount\n"},
		{name: "yaml list from flag", args: []string{"ui.users"}, want: "- admin:pw:Global Admin\n- bob:pw:VM Viewer\n"},
		{name: "yaml flag default", args: []string{"bridge-mode"}, want: "manual\n"},
		{name: "json scalar", args: []string{"log.system.max-size", "--json"}, want: "50\n"},
		{name: "json format", args: []string{"log.system", "-f", "json"}, want: "{\n  \"max-size\": 50\n}\n"},
		{
			name: "table scalar", args: []string{"log.level", "-f", "table"},
			want: settingsCmdTable(map[string]any{"log.level": "debug"}),
		},
		{
			name: "table section", args: []string{"log", "-f", "table"},
			want: settingsCmdTable(map[string]any{"level": "debug", "system": map[string]any{"max-size": 50}}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := settingsCmdRun(settingsCmdListViper(t), append([]string{"get"}, tc.args...)...)
			if err != nil {
				t.Fatalf("settings get %v error = %v", tc.args, err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("settings get %v mismatch (-want +got):\n%s", tc.args, diff)
			}
		})
	}
}

// settings get with no key prints every setting, and refuses a key with no
// value, an unknown format and more than one key.
func TestSettingsCmdGetAllAndRefusals(t *testing.T) {
	t.Parallel()

	v := settingsCmdListViper(t)
	all := map[string]any{
		"bridge-mode": "manual",
		"log":         map[string]any{"level": "debug", "system": map[string]any{"max-size": 50}},
		"ui": map[string]any{
			"features": []any{"vm-mount"},
			"users":    []any{"admin:pw:Global Admin", "bob:pw:VM Viewer"},
		},
	}

	out, err := settingsCmdRun(v, "get")
	if err != nil {
		t.Fatalf("settings get error = %v", err)
	}

	var got map[string]any
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("settings get = %q, want YAML: %v", out, err)
	}

	if diff := cmp.Diff(all, got); diff != "" {
		t.Errorf("settings get mismatch (-want +got):\n%s", diff)
	}

	if _, err := settingsCmdRun(v, "get", "log.nothing"); !errors.Is(err, errSettingNotFound) {
		t.Errorf("settings get log.nothing error = %v, want %v", err, errSettingNotFound)
	}

	for _, args := range [][]string{{"log.level", "-f", "xml"}, {"log.level", "bridge-mode"}} {
		if out, err := settingsCmdRun(v, append([]string{"get"}, args...)...); err == nil {
			t.Errorf("settings get %v = %q, nil; want an error", args, out)
		}
	}
}

// settings list prints every setting, or those whose dotted key starts with
// --filter, in a table (the default), JSON or YAML.
func TestSettingsCmdList(t *testing.T) {
	t.Parallel()

	all := map[string]any{
		"bridge-mode": "manual",
		"log":         map[string]any{"level": "debug", "system": map[string]any{"max-size": 50}},
		"ui": map[string]any{
			"features": []string{"vm-mount"},
			"users":    []string{"admin:pw:Global Admin", "bob:pw:VM Viewer"},
		},
	}
	logOnly := map[string]any{"log.level": "debug", "log.system.max-size": 50}

	type codec struct {
		encode func(any) ([]byte, error)
		decode func([]byte, any) error
	}

	var (
		jsonCodec = &codec{encode: json.Marshal, decode: json.Unmarshal}
		yamlCodec = &codec{encode: yaml.Marshal, decode: yaml.Unmarshal}
	)

	tests := []struct {
		name  string
		args  []string
		codec *codec // nil for a table
		want  map[string]any
	}{
		{name: "table", want: all},
		{name: "table filter", args: []string{"--filter", "LOG."}, want: logOnly},
		{name: "json", args: []string{"--json"}, codec: jsonCodec, want: all},
		{name: "json format filter", args: []string{"-f", "json", "--filter", "log"}, codec: jsonCodec, want: logOnly},
		{name: "yaml", args: []string{"-f", "yaml"}, codec: yamlCodec, want: all},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := settingsCmdRun(settingsCmdListViper(t), append([]string{"list"}, tc.args...)...)
			if err != nil {
				t.Fatalf("settings list %v error = %v", tc.args, err)
			}

			if tc.codec == nil {
				if diff := cmp.Diff(settingsCmdTable(tc.want), out); diff != "" {
					t.Errorf("settings list %v mismatch (-want +got):\n%s", tc.args, diff)
				}

				return
			}

			// encoding and decoding want as well gives the types that decoding gives
			var got, want map[string]any
			if err := tc.codec.decode([]byte(out), &got); err != nil {
				t.Fatalf("settings list %v = %q, cannot decode: %v", tc.args, out, err)
			}

			encoded, err := tc.codec.encode(tc.want)
			if err != nil {
				t.Fatalf("encoding %v: %v", tc.want, err)
			}

			if err := tc.codec.decode(encoded, &want); err != nil {
				t.Fatalf("decoding %q: %v", encoded, err)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("settings list %v mismatch (-want +got):\n%s", tc.args, diff)
			}
		})
	}

	if out, err := settingsCmdRun(settingsCmdListViper(t), "list", "-f", "xml"); err == nil {
		t.Errorf("settings list -f xml = %q, nil; want an error", out)
	}
}

// The key completion of settings get, set and unset gives the dotted keys
// that start with the text typed, and only for the first argument.
func TestSettingsCmdKeyCompletion(t *testing.T) {
	t.Parallel()

	complete := settingsKeyCompletion(settingsCmdListViper(t))

	tests := []struct {
		args       []string
		toComplete string
		want       []string
	}{
		{toComplete: "", want: []string{"bridge-mode", "log.level", "log.system.max-size", "ui.features", "ui.users"}},
		{toComplete: "log.", want: []string{"log.level", "log.system.max-size"}},
		{toComplete: "ui.u", want: []string{"ui.users"}},
		{toComplete: "nothing"},
		{args: []string{"log.level"}, toComplete: "log"},
	}

	for _, tc := range tests {
		got, directive := complete(nil, tc.args, tc.toComplete)
		slices.Sort(got)

		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("complete(%v, %q) mismatch (-want +got):\n%s", tc.args, tc.toComplete, diff)
		}

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("complete(%v, %q) directive = %v, want %v", tc.args, tc.toComplete, directive, cobra.ShellCompDirectiveNoFileComp)
		}
	}
}

// settingsCmdDBNames returns the store names of settings, sorted.
func settingsCmdDBNames(list []types.Setting) []string {
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, s.Metadata.Name)
	}

	slices.Sort(names)

	return names
}

// settings db list prints the settings of the store, or those of one category
// without regard to case, as JSON or a table. A category with no settings
// gives an empty JSON list.
func TestSettingsCmdDBList(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)

	all, err := settings.List()
	if err != nil {
		t.Fatalf("settings.List() error = %v", err)
	}

	var timeout []types.Setting

	for _, s := range all {
		if s.Spec.Category == "Timeout" {
			timeout = append(timeout, s)
		}
	}

	tests := []struct {
		args []string
		want []string
	}{
		{args: []string{"--json"}, want: settingsCmdDBNames(all)},
		{args: []string{"timeout", "--json"}, want: []string{"Timeout.Enabled", "Timeout.TimeoutMin", "Timeout.WarningMin"}},
		{args: []string{"Bogus", "--json"}, want: []string{}},
	}

	for _, tc := range tests {
		out, err := settingsCmdRun(viper.New(), append([]string{"db", "list"}, tc.args...)...)
		if err != nil {
			t.Fatalf("settings db list %v error = %v", tc.args, err)
		}

		var got []types.Setting
		if err := json.Unmarshal([]byte(out), &got); err != nil || got == nil {
			t.Fatalf("settings db list %v = %q, want a JSON list (error %v)", tc.args, out, err)
		}

		if diff := cmp.Diff(tc.want, settingsCmdDBNames(got)); diff != "" {
			t.Errorf("settings db list %v mismatch (-want +got):\n%s", tc.args, diff)
		}
	}

	out, err := settingsCmdRun(viper.New(), "db", "list", "Timeout")
	if err != nil {
		t.Fatalf("settings db list Timeout error = %v", err)
	}

	var want bytes.Buffer
	printer.PrintTableOfSettings(&want, timeout)

	if diff := cmp.Diff(want.String(), out); diff != "" {
		t.Errorf("settings db list Timeout mismatch (-want +got):\n%s", diff)
	}
}

// settings db edit stores a value of the setting's type, refuses one of
// another type or wrong arguments, and warns that Logging settings are
// ignored.
func TestSettingsCmdDBEdit(t *testing.T) { //nolint:paralleltest // replaces the store and captures the global logger
	storetest.UseBolt(t)

	if err := settings.SetDefaults(); err != nil {
		t.Fatalf("settings.SetDefaults() error = %v", err)
	}

	logs := captureLogs(t)

	const deprecated = "Logging settings via 'phenix settings db edit' are deprecated and will be ignored. " +
		"Please use 'phenix settings set' (e.g. phenix settings set log.system.max-size 100)."

	tests := []struct {
		args     []string
		ok       bool
		want     string // the stored value of args[0].args[1] after the edit
		warnings int
	}{
		{args: []string{"Password", "MinLength", "20"}, ok: true, want: "20"},
		{args: []string{"Password", "MinLength", "abc"}, want: "20"},
		{args: []string{"Timeout", "Enabled", "true"}, ok: true, want: "true"},
		{args: []string{"Logging", "MaxFileSize", "200"}, ok: true, want: "200", warnings: 1},
		{args: []string{"Password", "Nothing", "1"}},
		{args: []string{"Password", "MinLength", "21", "extra"}, want: "20"},
		{args: []string{"Password", "MinLength"}, want: "20"},
		{args: []string{"Password"}},
		{},
	}

	for _, tc := range tests {
		_, err := settingsCmdRun(viper.New(), append([]string{"db", "edit"}, tc.args...)...)
		if (err == nil) != tc.ok {
			t.Errorf("settings db edit %v error = %v, want error %t", tc.args, err, !tc.ok)
		}

		if tc.want != "" {
			s, err := settings.GetSetting(tc.args[0], tc.args[1])
			if err != nil {
				t.Fatalf("settings.GetSetting(%s, %s) error = %v", tc.args[0], tc.args[1], err)
			}

			if s.Spec.Value != tc.want {
				t.Errorf("after settings db edit %v, %s.%s = %q, want %q", tc.args, tc.args[0], tc.args[1], s.Spec.Value, tc.want)
			}
		}

		if got := len(logs.all(t, deprecated)); got != tc.warnings {
			t.Errorf("settings db edit %v logged %d deprecation warnings, want %d", tc.args, got, tc.warnings)
		}

		logs.mu.Lock()
		logs.buf.Reset()
		logs.mu.Unlock()
	}
}

// inferType gives a bool for true or false in any case, an int for a base 10
// integer, a float64 for a number with a decimal point, and else the text.
func TestSettingsCmdInferType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want any
	}{
		{"true", true}, {"TRUE", true}, {"False", false},
		{"0", 0}, {"200", 200}, {"-7", -7}, {"+3", 3}, {"007", 7},
		{"1.5", 1.5}, {".5", 0.5}, {"-2.", -2.0}, {"1.5e3", 1500.0},
		{"1e5", "1e5"}, {"NaN", "NaN"}, {"Inf", "Inf"}, {"1.2.3", "1.2.3"}, {"yes", "yes"}, {"", ""},
		{"99999999999999999999", "99999999999999999999"}, {"0x10", "0x10"}, {" 1", " 1"}, {"debug", "debug"},
	}

	for _, tc := range tests {
		if got := inferType(tc.in); !cmp.Equal(got, tc.want) {
			t.Errorf("inferType(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}
