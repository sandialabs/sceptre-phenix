package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"

	"phenix/api/runtimesettings"
)

// settingsCmdTypedRoot returns a root command with phenix settings over v and
// a flag of each type, bound as phenix binds them: root flags at their names,
// ui flags at ui.<name>, and the ui flag unix-socket-gid at its own name.
func settingsCmdTypedRoot(v *viper.Viper) *cobra.Command {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("log.level", "info", "")
	root.PersistentFlags().Int("log.system.max-size", 100, "")
	root.PersistentFlags().Bool("use-gre-mesh", false, "")

	ui := &cobra.Command{Use: "ui", Run: func(*cobra.Command, []string) {}}
	ui.Flags().String("jwt-signing-key", "", "")
	ui.Flags().Duration("jwt-lifetime", time.Hour, "")
	ui.Flags().StringSlice("users", nil, "")
	ui.Flags().Float64("ratio", 0, "")
	ui.Flags().Int("unix-socket-gid", -1, "")

	_ = v.BindPFlag("unix-socket-gid", ui.Flags().Lookup("unix-socket-gid"))

	root.AddCommand(ui, newSettingsCommands(v))

	return root
}

// settingsCmdRunTyped runs "phenix settings <args>" under settingsCmdTypedRoot.
func settingsCmdRunTyped(v *viper.Viper, args ...string) error {
	root := settingsCmdTypedRoot(v)
	root.SetArgs(append([]string{"settings"}, args...))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	_, err := root.ExecuteC()

	return err
}

// settings set writes the value of a known setting with the type of its
// flag, as the flag parses it, and refuses text that the flag refuses. Only
// a key that no flag sets gets the type inferType gives it.
func TestSettingsCmdSetKnownSettingTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, key, value string
		want             any
		wantErr          error
	}{
		{name: "string that looks like an int", key: "ui.jwt-signing-key", value: "0123", want: "0123"},
		{name: "string that looks like a float", key: "ui.jwt-signing-key", value: "1.50", want: "1.50"},
		{name: "string that looks like a bool", key: "log.level", value: "TRUE", want: "TRUE"},
		{name: "key in other case", key: "UI.JWT-Signing-Key", value: "007", want: "007"},
		{name: "int", key: "log.system.max-size", value: "200", want: 200},
		{name: "int in hex as the flag reads it", key: "log.system.max-size", value: "0x10", want: 16},
		{name: "int refuses a word", key: "log.system.max-size", value: "abc", wantErr: runtimesettings.ErrSettingType},
		{name: "int refuses a float", key: "log.system.max-size", value: "1.5", wantErr: runtimesettings.ErrSettingType},
		{name: "bool", key: "use-gre-mesh", value: "true", want: true},
		{name: "bool as a digit", key: "use-gre-mesh", value: "0", want: false},
		{name: "bool refuses yes", key: "use-gre-mesh", value: "yes", wantErr: runtimesettings.ErrSettingType},
		{name: "duration keeps its text", key: "ui.jwt-lifetime", value: "24h", want: "24h"},
		{name: "duration refuses a number with no unit", key: "ui.jwt-lifetime", value: "3600", wantErr: runtimesettings.ErrSettingType},
		{name: "list", key: "ui.users", value: "a:b:admin,c:d:user", want: []any{"a:b:admin", "c:d:user"}},
		{name: "list with a quoted comma", key: "ui.users", value: `"a,b",c`, want: []any{"a,b", "c"}},
		{name: "empty list", key: "ui.users", value: "", want: []any{}},
		{name: "ui flag at its own name", key: "unix-socket-gid", value: "100", want: 100},
		{name: "ui flag at its own name refuses a word", key: "unix-socket-gid", value: "x", wantErr: runtimesettings.ErrSettingType},
		{name: "type settings set cannot write", key: "ui.ratio", value: "1.5", wantErr: runtimesettings.ErrSettingType},
		{name: "unknown key", key: "app.ratio", value: "1.5", want: 1.5},
		{name: "unknown ui key", key: "ui.nope", value: "7", want: 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const original = "log:\n  level: info\n"

			path := writeTestFile(t, t.TempDir(), "config.yaml", original)
			err := settingsCmdRunTyped(settingsCmdViper(t, path), "set", tc.key, tc.value)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("settings set %s %q error = %v, want %v", tc.key, tc.value, err, tc.wantErr)
				}

				if got, _ := os.ReadFile(path); string(got) != original {
					t.Errorf("settings set %s %q changed the file to %q", tc.key, tc.value, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("settings set %s %q error = %v", tc.key, tc.value, err)
			}

			var got any = settingsCmdReadYAML(t, path)
			for part := range strings.SplitSeq(strings.ToLower(tc.key), ".") {
				if m, ok := got.(map[string]any); ok {
					got = settingsCmdLookupFold(m, part)
				}
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("settings set %s %q: value in the file mismatch (-want +got):\n%s", tc.key, tc.value, diff)
			}
		})
	}
}

// settingsCmdLookupFold returns the value in m of the key that matches key
// without regard to case.
func settingsCmdLookupFold(m map[string]any, key string) any {
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}

	return nil
}

// settingFlag finds each setting of phenix as the root command binds it,
// and settingValue gives each the type of its flag.
func TestSettingsCmdPhenixSettingTypes(t *testing.T) { //nolint:paralleltest // reads rootCmd and the global viper
	tests := []struct {
		key, value string
		want       any
	}{
		{"ui.jwt-signing-key", "0123", "0123"},
		{"ui.jwt-signing-key", "1.50", "1.50"},
		{"log.level", "true", "true"},
		{"log.system.max-size", "200", 200},
		{"ui.shutdown-grace-period", "5", 5},
		{"use-gre-mesh", "true", true},
		{"ui.minimega-console", "false", false},
		{"ui.jwt-lifetime", "90s", "90s"},
		{"ui.users", "a:b:admin,c:d:user", []string{"a:b:admin", "c:d:user"}},
		{"ui.features", "vm-mount", []string{"vm-mount"}},
		{"unix-socket-gid", "100", 100},
		{"no.such.setting", "1.5", 1.5},
	}

	for _, tc := range tests {
		got, err := runtimesettings.ParseValue(settingFlag(rootCmd, viper.GetViper(), tc.key), tc.key, tc.value)
		if err != nil {
			t.Errorf("ParseValue(%s, %q) error = %v", tc.key, tc.value, err)

			continue
		}

		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("ParseValue(%s, %q) mismatch (-want +got):\n%s", tc.key, tc.value, diff)
		}
	}

	if _, err := runtimesettings.ParseValue(
		settingFlag(rootCmd, viper.GetViper(), "ui.jwt-lifetime"),
		"ui.jwt-lifetime",
		"3600",
	); !errors.Is(
		err,
		runtimesettings.ErrSettingType,
	) {
		t.Errorf("ParseValue(ui.jwt-lifetime, 3600) error = %v, want %v", err, runtimesettings.ErrSettingType)
	}
}

// settings set and unset change only the node of the key: comments, key
// order, indent and the other keys stay as they are.
func TestSettingsCmdSetUnsetKeepFile(t *testing.T) {
	t.Parallel()

	const original = "# phenix settings\nlog:\n  # the level\n  level: info # default\n  system:\n    max-size: 100\n" +
		"ui:\n  users: [a]\n"

	tests := []struct {
		name string
		data string
		args []string
		want string
	}{
		{
			name: "set a key in place", data: original, args: []string{"set", "log.level", "debug"},
			want: "# phenix settings\nlog:\n  # the level\n  level: debug # default\n  system:\n    max-size: 100\n" +
				"ui:\n  users: [a]\n",
		},
		{
			name: "set a new key at the end of its mapping", data: original, args: []string{"set", "log.console", "stdout"},
			want: "# phenix settings\nlog:\n  # the level\n  level: info # default\n  system:\n    max-size: 100\n" +
				"  console: stdout\nui:\n  users: [a]\n",
		},
		{
			name: "set a new section", data: original, args: []string{"set", "base.dir", "/x"},
			want: original + "base:\n  dir: /x\n",
		},
		{
			name: "set text that looks like a number", data: "a: 1\n", args: []string{"set", "ui.jwt-signing-key", "0123"},
			want: "a: 1\nui:\n    jwt-signing-key: \"0123\"\n",
		},
		{
			name: "set in a file of only comments", data: "# nothing yet\n", args: []string{"set", "log.level", "debug"},
			want: "# nothing yet\nlog:\n    level: debug\n",
		},
		{
			name: "set in a file of only comments with no final newline", data: "# nothing yet",
			args: []string{"set", "log.level", "debug"},
			want: "# nothing yet\nlog:\n    level: debug\n",
		},
		{
			name: "set in a file with a nested two-space indent", data: "a:\n x: 1\n y:\n   z: 1\n", args: []string{"set", "b", "1"},
			want: "a:\n  x: 1\n  y:\n    z: 1\nb: 1\n",
		},
		{
			name: "set in a null file", data: "null\n", args: []string{"set", "log.level", "debug"},
			want: "log:\n    level: debug\n",
		},
		{
			name: "set in a file with four-space indent", data: "log:\n    level: info\n", args: []string{"set", "log.x", "1"},
			want: "log:\n    level: info\n    x: 1\n",
		},
		{
			name: "unset a key", data: original, args: []string{"unset", "log.level"},
			want: "# phenix settings\nlog:\n  system:\n    max-size: 100\nui:\n  users: [a]\n",
		},
		{
			name: "unset the last key of two sections", data: original, args: []string{"unset", "log.system.max-size"},
			want: "# phenix settings\nlog:\n  # the level\n  level: info # default\nui:\n  users: [a]\n",
		},
		{
			name: "set through an alias keeps the anchor", data: "a: &x\n  k: 1\nb: *x\n", args: []string{"set", "b.k", "2"},
			want: "a: &x\n  k: 1\nb:\n  k: 2\n",
		},
		{
			name: "unset through an alias keeps the anchor", data: "a: &x\n  k: 1\n  j: 2\nb: *x\n", args: []string{"unset", "b.k"},
			want: "a: &x\n  k: 1\n  j: 2\nb:\n  j: 2\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeTestFile(t, t.TempDir(), "config.yaml", tc.data)

			if err := settingsCmdRunTyped(settingsCmdViper(t, path), tc.args...); err != nil {
				t.Fatalf("settings %v error = %v", tc.args, err)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("os.ReadFile() error = %v", err)
			}

			if diff := cmp.Diff(tc.want, string(got)); diff != "" {
				t.Errorf("settings %v: config file mismatch (-want +got):\n%s", tc.args, diff)
			}
		})
	}
}

// settings set and unset return an error, and leave the file, when they
// cannot read, remove or write the config file.
func TestSettingsCmdFileErrors(t *testing.T) {
	t.Parallel()

	t.Run("read a folder", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "config.yaml")
		v := settingsCmdViper(t, path)

		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("os.Mkdir() error = %v", err)
		}

		for _, args := range [][]string{{"set", "log.level", "debug"}, {"unset", "log.level"}} {
			if err := settingsCmdRunTyped(v, args...); err == nil {
				t.Errorf("settings %v of the folder %s error = nil, want an error", args, path)
			}
		}
	})

	t.Run("remove a folder that is not empty", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "config.yaml")
		v := settingsCmdViper(t, path)

		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("os.Mkdir() error = %v", err)
		}

		writeTestFile(t, path, "keep", "")

		if err := settingsCmdRunTyped(v, "unset", "--all"); err == nil {
			t.Errorf("settings unset --all of the folder %s error = nil, want an error", path)
		}

		if _, err := os.Stat(filepath.Join(path, "keep")); err != nil {
			t.Errorf("settings unset --all removed the folder %s: %v", path, err)
		}
	})

	t.Run("write a read-only file", func(t *testing.T) {
		t.Parallel()

		if os.Geteuid() == 0 {
			t.Skip("root writes a read-only file")
		}

		const original = "log:\n  level: info\n"

		for _, args := range [][]string{{"set", "log.level", "debug"}, {"unset", "log.level"}} {
			path := writeTestFile(t, t.TempDir(), "config.yaml", original)

			if err := os.Chmod(path, 0o400); err != nil {
				t.Fatalf("os.Chmod() error = %v", err)
			}

			if err := settingsCmdRunTyped(settingsCmdViper(t, path), args...); err == nil {
				t.Errorf("settings %v of a read-only file error = nil, want an error", args)
			}

			if got, _ := os.ReadFile(path); string(got) != original {
				t.Errorf("settings %v changed the read-only file to %q", args, got)
			}
		}
	})
}

// settings unset of a key that is not in the file does not write the file,
// and logs that the key is not set in it.
func TestSettingsCmdUnsetAbsentKey(t *testing.T) { //nolint:paralleltest // captures the global logger
	const original = "# keep\nlog:\n  level: info\nui:\n  logs: {}\n"

	logs := captureLogs(t)

	for _, key := range []string{"log.console", "ui.logs.level", "log.level.x", "nope"} {
		path := writeTestFile(t, t.TempDir(), "config.yaml", original)

		// a read-only file makes a write fail
		if err := os.Chmod(path, 0o400); err != nil {
			t.Fatalf("os.Chmod() error = %v", err)
		}

		if err := settingsCmdRunTyped(settingsCmdViper(t, path), "unset", key); err != nil {
			t.Errorf("settings unset %s error = %v", key, err)
		}

		if got, _ := os.ReadFile(path); string(got) != original {
			t.Errorf("settings unset %s changed the file to %q", key, got)
		}

		var found bool

		for _, record := range logs.all(t, "configuration value not set in the config file; the file did not change") {
			found = found || (record["key"] == key && record["file"] == path)
		}

		if !found {
			t.Errorf("settings unset %s logged no record that it is not set in %s", key, path)
		}
	}

	if got := logs.all(t, "configuration value unset"); len(got) != 0 {
		t.Errorf("settings unset of absent keys logged %v, want no configuration value unset record", got)
	}
}

// settingsCmdGenCommentedConfig generates a block mapping with distinct keys,
// a head comment on some keys and a line comment on some scalar values.
func settingsCmdGenCommentedConfig(depth int) *rapid.Generator[*yaml.Node] {
	return rapid.Custom(func(t *rapid.T) *yaml.Node {
		keys := rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"a", "b", "log", "ui"}), 1, 3, rapid.ID).Draw(t, "keys")
		comment := rapid.SampledFrom([]string{"", "# note", "# two words"})
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

		for _, key := range keys {
			k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, HeadComment: comment.Draw(t, "head")}

			var v *yaml.Node
			if depth > 0 && rapid.Bool().Draw(t, "nested") {
				v = settingsCmdGenCommentedConfig(depth-1).Draw(t, "child")
			} else {
				v = &yaml.Node{}
				if err := v.Encode(rapid.SampledFrom([]any{1, 500, "info", "a b", true}).Draw(t, "value")); err != nil {
					t.Fatalf("Encode() error = %v", err)
				}

				v.LineComment = comment.Draw(t, "line")
			}

			m.Content = append(m.Content, k, v)
		}

		return m
	})
}

// settingsCmdFreeNodePath reports whether the mapping m has no value at path
// and no value that is not a mapping at a prefix of path.
func settingsCmdFreeNodePath(m *yaml.Node, path []string) bool {
	for i, part := range path {
		j := settingsCmdKeyIndex(m, part)
		if j < 0 {
			return true
		}

		if m.Content[j+1].Kind != yaml.MappingNode || i == len(path)-1 {
			return false
		}

		m = m.Content[j+1]
	}

	return true
}

// For a file with comments and a key that the file does not have, settings
// set and then settings unset give back the same bytes.
func TestSettingsCmdSetUnsetKeepsBytes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	rapid.Check(t, func(rt *rapid.T) {
		config := settingsCmdGenCommentedConfig(2).Draw(rt, "config")
		indent := rapid.SampledFrom([]int{2, 4}).Draw(rt, "indent")
		path := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "B", "log", "Ui", "x", "level"}), 1, 3).
			Filter(func(p []string) bool { return settingsCmdFreeNodePath(config, p) }).
			Draw(rt, "path")
		value := rapid.SampledFrom([]string{"debug", "200", "true", "0123", "a b", ""}).Draw(rt, "value")
		key := strings.Join(path, ".")

		var buf bytes.Buffer

		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(indent)

		if err := enc.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{config}}); err != nil {
			rt.Fatalf("Encode() error = %v", err)
		}

		original := buf.String()

		file := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
			rt.Fatalf("os.WriteFile() error = %v", err)
		}

		if _, err := settingsCmdRun(settingsCmdViper(rt, file), "set", key, value); err != nil {
			rt.Fatalf("settings set %s %q error = %v", key, value, err)
		}

		if _, err := settingsCmdRun(settingsCmdViper(rt, file), "unset", key); err != nil {
			rt.Fatalf("settings unset %s error = %v", key, err)
		}

		got, err := os.ReadFile(file)
		if err != nil {
			rt.Fatalf("os.ReadFile() error = %v", err)
		}

		if diff := cmp.Diff(original, string(got)); diff != "" {
			rt.Errorf("settings set %s %q, then unset %s: file mismatch (-want +got):\n%s", key, value, key, diff)
		}
	})
}

// settingsCmdKeyIndex returns the index in m.Content of the key that matches
// key without regard to case, or -1 when m has no such key.
func settingsCmdKeyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && strings.EqualFold(m.Content[i].Value, key) {
			return i
		}
	}

	return -1
}
