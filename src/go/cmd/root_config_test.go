package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/viper"
	"pgregory.net/rapid"
)

// newSearchViper returns a viper that searches dir for the config file.
func newSearchViper(dir string) *viper.Viper {
	v := viper.New()
	v.SetConfigName("config")
	v.AddConfigPath(dir)

	return v
}

// writeTestFile writes data to name under dir and returns its path.
func writeTestFile(t *testing.T, dir, name, data string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}

	return path
}

// A config file that reads cleanly is used without a warning, and one that
// does not parse is ignored with a warning naming it, rather than silently.
func TestReadConfigReportsBadFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data     string
		wantRead bool
		wantWarn []string
	}{
		"valid": {data: "log:\n  level: debug\n", wantRead: true},
		"bad yaml": {
			data:     "log:\n  level: debug\n bad indent: [\n",
			wantWarn: []string{"phenix: warning: ignoring config file", "config.yaml", ": it does not parse at line 2\n"},
		},
		"a password where yaml quotes the line": {
			data:     "bo:hunter2:global-admin\n",
			wantWarn: []string{"config.yaml: it does not parse at line 1\n"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeTestFile(t, dir, "config.yaml", tc.data)

			var warn bytes.Buffer

			v := newSearchViper(dir)

			if got := readConfig(v, dir, &warn); got != tc.wantRead {
				t.Errorf("readConfig() = %t, want %t (warning %q)", got, tc.wantRead, warn.String())
			}

			if len(tc.wantWarn) == 0 && warn.Len() != 0 {
				t.Errorf("readConfig() warned %q, want no warning", warn.String())
			}

			for _, want := range tc.wantWarn {
				if !strings.Contains(warn.String(), want) {
					t.Errorf("readConfig() warning %q does not contain %q", warn.String(), want)
				}
			}

			if strings.Contains(warn.String(), "hunt") {
				t.Errorf("readConfig() warning %q quotes the file", warn.String())
			}
		})
	}
}

// With no config file, readConfig creates an empty one in the given
// directory, which it then reads, so the watcher has a file to watch.
func TestReadConfigCreatesMissingFile(t *testing.T) {
	t.Parallel()

	var (
		search = t.TempDir()
		create = filepath.Join(t.TempDir(), "phenix")
		warn   bytes.Buffer
	)

	v := newSearchViper(search)
	v.AddConfigPath(create)

	if !readConfig(v, create, &warn) {
		t.Fatalf("readConfig() = false, want true (warning %q)", warn.String())
	}

	if warn.Len() != 0 {
		t.Errorf("readConfig() warned %q, want no warning", warn.String())
	}

	want := filepath.Join(create, "config.yaml")

	if diff := cmp.Diff(want, v.ConfigFileUsed()); diff != "" {
		t.Errorf("ConfigFileUsed() mismatch (-want +got):\n%s", diff)
	}
}

// When the config directory cannot be created, readConfig says so instead of
// carrying on without a word.
func TestReadConfigReportsUncreatableDirectory(t *testing.T) {
	t.Parallel()

	var (
		dir  = t.TempDir()
		file = writeTestFile(t, dir, "not-a-dir", "")
		warn bytes.Buffer
	)

	v := newSearchViper(dir)

	if readConfig(v, filepath.Join(file, "phenix"), &warn) {
		t.Fatalf("readConfig() = true, want false")
	}

	if !strings.Contains(warn.String(), "phenix: warning: creating config directory") {
		t.Errorf("readConfig() warning = %q, want one about the config directory", warn.String())
	}
}

// A missing users file is normal and silent; one that does not parse is
// ignored with a warning, and still returned so it can be watched.
func TestMergeUsersConfigReportsBadFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data      string // no file when empty
		wantUsers []string
		wantFile  bool
		wantWarn  string // after the path; no warning when empty
	}{
		"missing": {},
		"valid": {
			data: "ui:\n  users: [admin:secret:global-admin]\n", wantUsers: []string{"admin:secret:global-admin"}, wantFile: true,
		},
		"bad": {data: "ui: [\n", wantFile: true, wantWarn: "it does not parse at line 1"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			var wantFile string
			if tc.data != "" {
				path := writeTestFile(t, dir, "users.yaml", tc.data)
				if tc.wantFile {
					wantFile = path
				}
			}

			var warn bytes.Buffer

			v := viper.New()

			if got := mergeUsersConfig(v, []string{dir}, &warn); got != wantFile {
				t.Errorf("mergeUsersConfig() = %q, want %q", got, wantFile)
			}

			if diff := cmp.Diff(tc.wantUsers, v.GetStringSlice("ui.users"), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ui.users mismatch (-want +got):\n%s", diff)
			}

			want := ""
			if tc.wantWarn != "" {
				want = "phenix: warning: ignoring config file " + filepath.Join(dir, "users.yaml") + ": " + tc.wantWarn + "\n"
			}

			if diff := cmp.Diff(want, warn.String()); diff != "" {
				t.Errorf("mergeUsersConfig() warning mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// newConfigViper is a viper set up as the root command sets up phenix's own:
// PHENIX_ variables, then the config files in dir.
func newConfigViper(dir string) *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("PHENIX")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	v.AddConfigPath(dir)

	return v
}

// Loading the config and users files keeps the config file read, so settings
// commands and the watcher use it, and a change to either file reloads both:
// the users stay after a config change, a users change is read, and a
// PHENIX_ variable still wins over the config file after each reload.
func TestConfigFilesReload(t *testing.T) {
	t.Setenv("PHENIX_LOG_SYSTEM_MAX_AGE", "7")
	// empty is unset to viper, so the caller's environment cannot win over the files
	t.Setenv("PHENIX_LOG_LEVEL", "")
	t.Setenv("PHENIX_UI_USERS", "")

	var (
		dir    = t.TempDir()
		config = writeTestFile(t, dir, "config.yaml", "log:\n  level: info\n  system:\n    max-age: 30\n")
		users  = writeTestFile(t, dir, "users.yaml", "ui:\n  users: [admin:pw:global-admin]\n")
		v      = newConfigViper(dir)
		warn   bytes.Buffer
	)

	watch, usersFile := loadConfigFiles(v, []string{dir}, dir, &warn)
	if !watch || usersFile != users {
		t.Fatalf("loadConfigFiles() = %t, %q, want true, %q", watch, usersFile, users)
	}

	type state struct {
		File, Level string
		MaxAge      int
		Users       []string
	}

	read := func() state {
		return state{
			File: v.ConfigFileUsed(), Level: v.GetString("log.level"),
			MaxAge: v.GetInt("log.system.max-age"), Users: v.GetStringSlice("ui.users"),
		}
	}

	want := state{File: config, Level: "info", MaxAge: 7, Users: []string{"admin:pw:global-admin"}}
	if diff := cmp.Diff(want, read()); diff != "" {
		t.Fatalf("after loading (-want +got):\n%s", diff)
	}

	// the config watcher reads the config file itself, then calls the handler
	writeTestFile(t, dir, "config.yaml", "log:\n  level: debug\n  system:\n    max-age: 60\n")

	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig() error = %v", err)
	}

	reloadConfigFiles(v, []string{dir}, &warn)

	want.Level = "debug"
	if diff := cmp.Diff(want, read()); diff != "" {
		t.Errorf("after a config change (-want +got):\n%s", diff)
	}

	writeTestFile(t, dir, "users.yaml", "ui:\n  users: [bob:pw:viewer]\n")
	reloadConfigFiles(v, []string{dir}, &warn)

	want.Users = []string{"bob:pw:viewer"}
	if diff := cmp.Diff(want, read()); diff != "" {
		t.Errorf("after a users change (-want +got):\n%s", diff)
	}

	if warn.Len() != 0 {
		t.Errorf("warned %q, want no warning", warn.String())
	}
}

// phenix settings set writes the config file phenix read, and only when it
// read none the config.yaml in the user's config directory.
func TestSettingsSetWritesFileRead(t *testing.T) {
	t.Parallel()

	var (
		dir  = t.TempDir()
		home = t.TempDir()
		file = writeTestFile(t, dir, "config.yaml", "log:\n  level: info\n")
		v    = newSearchViper(dir)
		warn bytes.Buffer
	)

	if !readConfig(v, filepath.Join(home, ".config", appName), &warn) {
		t.Fatalf("readConfig() = false (warning %q)", warn.String())
	}

	target := settingsFile(v.ConfigFileUsed(), "1000", home)
	if target != file {
		t.Fatalf("settingsFile() = %q, want the file read, %q", target, file)
	}

	if err := setConfigValue(target, "log.level", "debug"); err != nil {
		t.Fatalf("setConfigValue() error = %v", err)
	}

	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	if diff := cmp.Diff("log:\n    level: debug\n", string(got)); diff != "" {
		t.Errorf("config file mismatch (-want +got):\n%s", diff)
	}

	if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Errorf("os.Stat(%s/.config) error = %v, want it not to exist", home, err)
	}

	fallbacks := map[string]string{
		"1000": filepath.Join(home, ".config", appName, "config.yaml"),
		"0":    filepath.Join(configDir, "config.yaml"),
	}

	for uid, want := range fallbacks {
		if got := settingsFile("", uid, home); got != want {
			t.Errorf("settingsFile(\"\", %s) = %q, want %q", uid, got, want)
		}
	}

	if err := setConfigValue(filepath.Join(dir, "config.toml"), "log.level", "debug"); !errors.Is(err, errNotYAML) {
		t.Errorf("setConfigValue(config.toml) error = %v, want %v", err, errNotYAML)
	}
}

// A config or users file's parse error gives only the line it names, never
// the error's text, which quotes the file.
func TestConfigParseError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want string
	}{
		"yaml syntax": {
			err:  errors.New("While parsing config: yaml: line 3: did not find expected key"),
			want: "does not parse at line 3",
		},
		"yaml unmarshal": {
			err: errors.New("While parsing config: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str " +
				"`bo:hunt...` into map[string]interface {}"),
			want: "does not parse at line 1",
		},
		"toml":       {err: errors.New("While parsing config: (12, 5): unexpected token"), want: "does not parse at line 12"},
		"no line":    {err: errors.New("While parsing config: invalid character 'b' looking for value"), want: "does not parse"},
		"line later": {err: errors.New("x `line 9: secret` y"), want: "does not parse at line 9"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := configParseError(tc.err); got != tc.want {
				t.Errorf("configParseError(%q) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// A file that cannot be read is named with the error, which quotes no
// content; one that does not parse gives only its line.
func TestConfigReadProblem(t *testing.T) {
	t.Parallel()

	if got, want := configReadProblem(os.ErrPermission), "is not readable: permission denied"; got != want {
		t.Errorf("configReadProblem(os.ErrPermission) = %q, want %q", got, want)
	}

	path := writeTestFile(t, t.TempDir(), "config.yaml", "bo:hunter2:global-admin\n")

	v := viper.New()
	v.SetConfigFile(path)

	if got, want := configReadProblem(v.ReadInConfig()), "does not parse at line 1"; got != want {
		t.Errorf("configReadProblem(parse error) = %q, want %q", got, want)
	}
}

// Whatever a config or users file holds, the warning for one that does not
// parse names the file and at most a line number, so never a secret in it.
func TestConfigWarningsNeverShowContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		content := rapid.StringMatching(`[A-Za-z0-9:\[\]{} -]{1,40}(\n[A-Za-z0-9: -]{0,20}){0,3}`).Draw(t, "content")

		path := filepath.Join(dir, "users.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		var warn bytes.Buffer

		mergeUsersConfig(viper.New(), []string{dir}, &warn)

		if warn.Len() == 0 {
			return
		}

		rest, ok := strings.CutPrefix(warn.String(), "phenix: warning: ignoring config file "+path+": it does not parse")
		if !ok || !regexp.MustCompile(`^( at line \d+)?\n$`).MatchString(rest) {
			t.Fatalf("warning %q for %q gives more than the path and a line", warn.String(), content)
		}

		config := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		warn.Reset()

		v := viper.New()
		v.SetConfigName("config")
		v.AddConfigPath(dir)
		readConfig(v, dir, &warn)

		rest, ok = strings.CutPrefix(warn.String(), "phenix: warning: ignoring config file "+config+": it does not parse")
		if !ok || !regexp.MustCompile(`^( at line \d+)?\n$`).MatchString(rest) {
			t.Fatalf("readConfig() warning %q for %q gives more than the path and a line", warn.String(), content)
		}
	})
}
