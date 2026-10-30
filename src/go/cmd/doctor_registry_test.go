package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
	"pgregory.net/rapid"
)

const doctorTestMinimega = "minimega 3.0.1 837c9174 2026-07-08"

// newDoctorTestDir makes the phenix base directory, with the directories
// derived from it, and minimega's base directory under a new directory.
func newDoctorTestDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for _, sub := range []string{"phenix/injects", "phenix/topologies", "phenix/mounts", "minimega"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", sub, err)
		}
	}

	return dir
}

// fakeDoctorInput is an input under dir, made by newDoctorTestDir, on which
// every check passes; settings replaces the values of some settings.
func fakeDoctorInput(dir string, settings map[string]string) *doctorInput {
	values := map[string]string{
		"base-dir.phenix":          filepath.Join(dir, "phenix"),
		"base-dir.minimega":        filepath.Join(dir, "minimega"),
		"store.endpoint":           "bolt://" + filepath.Join(dir, "store.bdb"),
		"log.system.path":          filepath.Join(dir, "phenix.log"),
		"log.console":              consoleStderr,
		"log.level":                "info",
		"log.system.max-size":      "100",
		"log.system.max-backups":   "3",
		"log.system.max-age":       "90",
		"bridge-mode":              "manual",
		"ui.jwt-signing-key":       "doctor-test-key",
		"ui.shutdown-grace-period": "5",
		"ui.netflow.max-size":      "100",
	}

	maps.Copy(values, settings)

	known := knownSettings()
	resolved := make(map[string]resolvedSetting, len(known))

	for _, key := range known {
		resolved[key] = resolvedSetting{value: values[key], text: values[key], source: sourceDefault}
	}

	configFile := filepath.Join(dir, "config.yaml")

	return &doctorInput{
		env: fakeDoctorEnv(nil, false),
		config: doctorConfig{
			checks: map[string]doctorCheck{"config": {Name: "config file", Status: doctorPass, Detail: configFile}},
			keys:   map[string]bool{},
			used:   configFile,
		},
		known:    known,
		settings: resolved,
		probe:    func(string, time.Duration) (string, error) { return doctorTestMinimega, nil },
		timeout:  time.Second,
		now:      func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) },
		hostname: func() (string, error) { return "doctor-test-host", nil },
		system:   doctorSystem{os: "linux", kernel: "6.8.0-49-generic", arch: "amd64"},
		uid:      1000,
		gid:      1000,
		userName: "alice",
		cwd:      "/home/alice",
	}
}

// replaceRows returns rows with each row in with in place of the row of the
// same id.
func replaceRows(rows []string, with ...string) []string {
	rows = slices.Clone(rows)

	for _, w := range with {
		id, _, _ := strings.Cut(w, " ")
		i := slices.IndexFunc(rows, func(r string) bool { return strings.HasPrefix(r, id+" ") })
		rows[i] = w
	}

	return rows
}

// rowsOf returns each result as "id name status".
func rowsOf(results []doctorResult) []string {
	rows := make([]string, 0, len(results))
	for _, r := range results {
		rows = append(rows, r.ID+" "+r.Name+" "+string(r.Status))
	}

	return rows
}

// Each registered check reads only its input: with every value good they
// all pass, and each bad value fails or warns only the check that reads it.
func TestDoctorChecksFromInput(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	allPass := []string{
		"config-file config file pass",
		"config-keys config keys pass",
		"bridge-mode bridge-mode pass",
		"deploy-mode deploy-mode pass",
		"log.level log.level pass",
		"log.system.max-size log.system.max-size pass",
		"log.system.max-backups log.system.max-backups pass",
		"log.system.max-age log.system.max-age pass",
		"store.endpoint store.endpoint pass",
		"log.system.path log.system.path pass",
		"base-dir.phenix base-dir.phenix pass",
		"base-dir.minimega base-dir.minimega pass",
		"base-dir.injects base-dir.injects pass",
		"base-dir.topologies base-dir.topologies pass",
		"mount-dir mount-dir pass",
		"ui.jwt-signing-key ui.jwt-signing-key pass",
		"ui.shutdown-grace-period ui.shutdown-grace-period pass",
		"ui.netflow.max-size ui.netflow.max-size pass",
		"minimega minimega pass",
	}

	// with replaces the row of id in allPass with row, or adds row after id
	with := func(id, row string, add bool) []string {
		rows := slices.Clone(allPass)
		i := slices.IndexFunc(rows, func(r string) bool { return strings.HasPrefix(r, id+" ") })

		if add {
			return slices.Insert(rows, i+1, row)
		}

		rows[i] = row

		return rows
	}

	tests := map[string]struct {
		settings map[string]string
		modify   func(*doctorInput)
		want     []string
	}{
		"all pass": {want: allPass},
		"store directory missing": {
			settings: map[string]string{"store.endpoint": "bolt://" + filepath.Join(dir, "missing", "store.bdb")},
			want:     with("store.endpoint", "store.endpoint store.endpoint fail", false),
		},
		"console log file": {
			settings: map[string]string{"log.console": filepath.Join(dir, "console.log")},
			want:     with("log.system.path", "log.console log.console pass", true),
		},
		"derived directory missing": {
			settings: map[string]string{"base-dir.phenix": dir},
			want: replaceRows(allPass,
				"base-dir.injects base-dir.injects warn",
				"base-dir.topologies base-dir.topologies warn",
				"mount-dir mount-dir warn",
			),
		},
		"settings every command reads": {
			settings: map[string]string{
				"bridge-mode": "sideways", "deploy-mode": "some", "log.level": "loud",
				"log.system.max-size": "-1", "log.system.max-backups": "five", "log.system.max-age": "-3",
			},
			want: replaceRows(allPass,
				"bridge-mode bridge-mode fail",
				"deploy-mode deploy-mode fail",
				"log.level log.level warn",
				"log.system.max-size log.system.max-size fail",
				"log.system.max-backups log.system.max-backups warn",
				"log.system.max-age log.system.max-age warn",
			),
		},
		"no jwt key": {
			settings: map[string]string{"ui.jwt-signing-key": ""},
			want:     with("ui.jwt-signing-key", "ui.jwt-signing-key ui.jwt-signing-key warn", false),
		},
		"bad grace period": {
			settings: map[string]string{"ui.shutdown-grace-period": "5s"},
			want:     with("ui.shutdown-grace-period", "ui.shutdown-grace-period ui.shutdown-grace-period fail", false),
		},
		"bad netflow size": {
			settings: map[string]string{"ui.netflow.max-size": "-1"},
			want:     with("ui.netflow.max-size", "ui.netflow.max-size ui.netflow.max-size fail", false),
		},
		"minimega down": {
			modify: func(in *doctorInput) {
				in.probe = func(string, time.Duration) (string, error) { return "", errors.New("refused") }
			},
			want: with("minimega", "minimega minimega fail", false),
		},
		"no config file": {
			modify: func(in *doctorInput) {
				in.config.checks["config"] = doctorCheck{Name: "config file", Status: doctorWarn, Detail: "none"}
			},
			want: with("config-file", "config-file config file warn", false),
		},
		"users file and unknown key": {
			modify: func(in *doctorInput) {
				in.config.checks["users"] = doctorCheck{Name: "users file", Status: doctorFail, Detail: "bad"}
				in.config.keys["ui.listen-endpiont"] = true
			},
			want: append([]string{allPass[0], "users-file users file fail", "config-keys config keys warn"}, allPass[2:]...),
		},
		"environment variables": {
			modify: func(in *doctorInput) {
				in.env = fakeDoctorEnv(map[string]string{"PHENIX_LOG_LEVEL": "loud", "PATH": "/usr/bin"}, false)
				in.settings["log.level"] = resolvedSetting{value: "loud", text: "loud", source: sourceEnv}
			},
			// after config-keys, in name order
			want: replaceRows(
				append(append(slices.Clone(allPass[:2]), "env PATH pass", "env PHENIX_LOG_LEVEL warn"), allPass[2:]...),
				"log.level log.level warn",
			),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(dir, tc.settings)
			if tc.modify != nil {
				tc.modify(in)
			}

			got := runDoctorChecks(t.Context(), in, doctorChecks(), nil)
			if diff := cmp.Diff(tc.want, rowsOf(got)); diff != "" {
				t.Errorf("runDoctorChecks() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A skipped check gives one skip row in its place, every row carries its
// check's id and group, and only rows that warn or fail get a hint.
func TestRunDoctorChecksSkipAndHints(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	in := fakeDoctorInput(dir, map[string]string{"ui.jwt-signing-key": ""})
	in.probe = func(string, time.Duration) (string, error) { return "", errors.New("refused") }

	got := runDoctorChecks(t.Context(), in, doctorChecks(), map[string]bool{"base-dir.phenix": true, "minimega": true})

	byID := make(map[string]doctorResult)
	for _, r := range got {
		byID[r.ID] = r
	}

	store := in.env.storeCheck(in.text("store.endpoint"))
	want := map[string]doctorResult{
		"base-dir.phenix": {
			ID: "base-dir.phenix", Group: doctorGroupDirs, Name: "base-dir.phenix", Status: doctorSkip,
			Detail: "skipped by --skip",
		},
		"minimega": {
			ID: "minimega", Group: doctorGroupMinimega, Name: "minimega", Status: doctorSkip, Detail: "skipped by --skip",
		},
		"ui.jwt-signing-key": {
			ID: "ui.jwt-signing-key", Group: doctorGroupUI, Name: "ui.jwt-signing-key", Status: doctorWarn,
			Detail: jwtKeyCheck("").Detail,
			Hint:   "set ui.jwt-signing-key to a long random secret to require sign-in",
		},
		"store.endpoint": {
			ID: "store.endpoint", Group: doctorGroupFiles, Name: "store.endpoint", Status: store.Status, Detail: store.Detail,
		},
	}

	for id, w := range want {
		if diff := cmp.Diff(w, byID[id]); diff != "" {
			t.Errorf("result %s mismatch (-want +got):\n%s", id, diff)
		}
	}
}

// --skip takes check ids and groups, repeated or comma-separated, and an
// unknown name fails with the valid ones.
func TestParseDoctorSkip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args    []string
		want    []string
		wantErr string
	}{
		"none":  {want: []string{}},
		"id":    {args: []string{"--skip", "ui.jwt-signing-key"}, want: []string{"ui.jwt-signing-key"}},
		"group": {args: []string{"--skip", "minimega"}, want: []string{"minimega"}},
		"group of several": {
			args: []string{"--skip", "config"},
			want: []string{"config-file", "config-keys", "users-file"},
		},
		"settings group": {
			args: []string{"--skip", "settings"},
			want: []string{
				"bridge-mode", "deploy-mode", "log.level", "log.system.max-age", "log.system.max-backups", "log.system.max-size",
			},
		},
		"comma list and repeated": {
			args: []string{"--skip", "env, store.endpoint", "--skip", "ui"},
			want: []string{"env", "store.endpoint", "ui.jwt-signing-key", "ui.netflow.max-size", "ui.shutdown-grace-period"},
		},
		"unknown": {
			args:    []string{"--skip", "minimega,mm,cofnig"},
			wantErr: `--skip: unknown check "mm", "cofnig"; checks: config-file, users-file, config-keys, env,`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd := newDoctorCmd()
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatalf("ParseFlags(%v) error = %v", tc.args, err)
			}

			skip, err := parseDoctorSkip(doctorFlagOptions(cmd.Flags()).skip, doctorChecks())

			if tc.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Fatalf("parseDoctorSkip(%v) error = %v, want prefix %q", tc.args, err, tc.wantErr)
				}

				if !strings.HasSuffix(err.Error(), "groups: config, env, settings, files, dirs, ui, minimega") {
					t.Errorf("parseDoctorSkip(%v) error = %v, want the groups listed", tc.args, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseDoctorSkip(%v) error = %v", tc.args, err)
			}

			got := make([]string, 0, len(skip))
			for id := range skip {
				got = append(got, id)
			}

			slices.Sort(got)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parseDoctorSkip(%v) mismatch (-want +got):\n%s", tc.args, diff)
			}
		})
	}
}

// Skipping any mix of ids and groups skips exactly the checks they name, and
// any other name fails.
func TestParseDoctorSkipProperty(t *testing.T) {
	t.Parallel()

	defs := doctorChecks()

	names := make([]string, 0, 2*len(defs))
	for _, def := range defs {
		names = append(names, def.id, def.group)
	}

	rapid.Check(t, func(t *rapid.T) {
		picked := rapid.SliceOf(rapid.SampledFrom(names)).Draw(t, "names")

		want := make(map[string]bool)

		for _, name := range picked {
			for _, def := range defs {
				if def.id == name || def.group == name {
					want[def.id] = true
				}
			}
		}

		got, err := parseDoctorSkip(picked, defs)
		if err != nil {
			t.Fatalf("parseDoctorSkip(%v) error = %v", picked, err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("parseDoctorSkip(%v) mismatch (-want +got):\n%s", picked, diff)
		}

		other := rapid.StringMatching(`[a-z.-]{1,12}`).Filter(func(s string) bool {
			return !slices.Contains(names, s)
		}).Draw(t, "other")

		if _, err := parseDoctorSkip(append(picked, other), defs); err == nil {
			t.Fatalf("parseDoctorSkip(%v) accepts %q", picked, other)
		}
	})
}

// doctor exits 1 on a failure, and with --strict on a warning too.
func TestDoctorExitError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status doctorStatus
		strict bool
		want   error
	}{
		"pass":          {status: doctorPass},
		"pass, strict":  {status: doctorPass, strict: true},
		"warn":          {status: doctorWarn},
		"warn, strict":  {status: doctorWarn, strict: true, want: errDoctorWarned},
		"fail":          {status: doctorFail, want: errDoctorFailed},
		"fail, strict":  {status: doctorFail, strict: true, want: errDoctorFailed},
		"skip, strict":  {status: doctorSkip, strict: true},
		"warn, default": {status: doctorWarn, strict: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := doctorExitError(tc.status, tc.strict); !errors.Is(got, tc.want) || (got == nil) != (tc.want == nil) {
				t.Errorf("doctorExitError(%s, %t) = %v, want %v", tc.status, tc.strict, got, tc.want)
			}

			if code := exitCode(doctorExitError(tc.status, tc.strict)); tc.want != nil && code != 1 {
				t.Errorf("exitCode() = %d, want 1", code)
			}
		})
	}
}

// The text output is the header, the settings unless -x, the checks, then a
// summary naming each failed and warned check, with a hint for each failure.
func TestDoctorTextOutput(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	in := fakeDoctorInput(dir, map[string]string{"ui.jwt-signing-key": ""})
	in.probe = func(string, time.Duration) (string, error) { return "", errors.New("refused") }

	tests := map[string]struct {
		opts      doctorOptions
		want      []string // in this order
		wantNot   []string
		wantHints []string
	}{
		"default": {
			opts: doctorOptions{output: "text"},
			want: []string{
				"phenix:", "minimega:", "os:", "linux 6.8.0-49-generic (amd64)", "user:", "alice (uid 1000, gid 1000)",
				"directory:", "/home/alice", "config:", filepath.Join(dir, "config.yaml"), "container:", "no",
				"time:", "2026-10-30T12:00:00Z",
				"\nSETTING", "store.endpoint", "\nCHECK", "minimega", "FAIL",
				"\nFAIL: 1 failed, 1 warned, 17 passed, 0 skipped; failed: minimega; warned: ui.jwt-signing-key\n",
				"  minimega: start minimega",
			},
			wantNot: []string{"hostname:", "doctor-test-host", "```", "  ui.jwt-signing-key: set"},
		},
		"no settings": {
			opts:    doctorOptions{output: "text", noSettings: true},
			want:    []string{"phenix:", "\nCHECK", "\nFAIL: "},
			wantNot: []string{"SETTING"},
		},
		"strict": {
			opts: doctorOptions{output: "text", strict: true},
			want: []string{"\nFAIL: ", "  ui.jwt-signing-key: set ui.jwt-signing-key", "  minimega: start minimega"},
		},
		"report": {
			opts: doctorOptions{output: "text", report: true},
			want: []string{"```text\nphenix:", "hostname:", "doctor-test-host", "time:", "\nSETTING", "\nCHECK", "\nFAIL: ", "```\n"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			report := runDoctor(t.Context(), in, doctorChecks(), nil, tc.opts)

			var out bytes.Buffer
			if err := writeDoctorReport(&out, report, tc.opts); err != nil {
				t.Fatalf("writeDoctorReport() error = %v", err)
			}

			text := out.String()
			at := 0

			for _, w := range tc.want {
				i := strings.Index(text[at:], w)
				if i < 0 {
					t.Fatalf("output lacks %q after offset %d:\n%s", w, at, text)
				}

				at += i + len(w)
			}

			for _, w := range tc.wantNot {
				if strings.Contains(text, w) {
					t.Errorf("output contains %q:\n%s", w, text)
				}
			}
		})
	}
}

// The JSON report has a schema version, the version and environment of the
// header, and an id and group on every check; the hostname only with --report.
func TestDoctorJSONOutput(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	in := fakeDoctorInput(dir, nil)

	for _, report := range []bool{false, true} {
		opts := doctorOptions{output: FormatJSON, report: report}
		got := runDoctor(t.Context(), in, doctorChecks(), map[string]bool{"env": true}, opts)

		var out bytes.Buffer
		if err := writeDoctorReport(&out, got, opts); err != nil {
			t.Fatalf("writeDoctorReport() error = %v", err)
		}

		text := out.String()
		if report {
			if !strings.HasPrefix(text, "```json\n") || !strings.HasSuffix(text, "```\n") {
				t.Fatalf("--report output is not a fenced JSON block:\n%s", text)
			}

			text = strings.TrimSuffix(strings.TrimPrefix(text, "```json\n"), "```\n")
		}

		var decoded struct {
			SchemaVersion int               `json:"schemaVersion"`
			Status        string            `json:"status"`
			Time          string            `json:"time"`
			Version       map[string]string `json:"version"`
			Environment   map[string]any    `json:"environment"`
			Checks        []map[string]any  `json:"checks"`
			Settings      []map[string]any  `json:"settings"`
		}

		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			t.Fatalf("json.Unmarshal() error = %v\n%s", err, text)
		}

		if decoded.SchemaVersion != 1 || decoded.Status != "pass" || decoded.Time != "2026-10-30T12:00:00Z" {
			t.Errorf("report = %d %s %s, want schemaVersion 1, pass and the time", decoded.SchemaVersion, decoded.Status, decoded.Time)
		}

		if decoded.Version["minimega"] != doctorTestMinimega || decoded.Version["phenix"] == "" {
			t.Errorf("version = %v, want phenix and minimega's", decoded.Version)
		}

		for _, key := range []string{"os", "kernel", "arch", "uid", "gid", "user", "workingDir", "configFile", "container"} {
			if _, ok := decoded.Environment[key]; !ok {
				t.Errorf("environment = %v, missing %s", decoded.Environment, key)
			}
		}

		if _, ok := decoded.Environment["hostname"]; ok != report {
			t.Errorf("environment hostname present = %t, want %t", ok, report)
		}

		for _, c := range decoded.Checks {
			for _, key := range []string{"id", "group", "name", "status", "detail"} {
				if _, ok := c[key]; !ok {
					t.Errorf("check %v has no %s", c, key)
				}
			}
		}

		if len(decoded.Settings) == 0 {
			t.Error("settings is empty")
		}
	}
}

// A marker file or a container runtime in /proc/1/cgroup shows doctor runs
// in a container.
func TestContainerHint(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files map[string]string
		want  string
	}{
		"none":            {files: map[string]string{"/proc/1/cgroup": "0::/\n"}, want: ""},
		"docker":          {files: map[string]string{"/.dockerenv": ""}, want: "/.dockerenv"},
		"podman":          {files: map[string]string{"/run/.containerenv": ""}, want: "/run/.containerenv"},
		"cgroup":          {files: map[string]string{"/proc/1/cgroup": "12:pids:/docker/abc\n"}, want: "cgroup docker"},
		"no proc":         {files: map[string]string{}, want: ""},
		"marker and proc": {files: map[string]string{"/.dockerenv": "", "/proc/1/cgroup": "0::/kubepods/x"}, want: "/.dockerenv"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			env.stat = func(path string) (os.FileInfo, error) {
				if _, ok := tc.files[path]; ok {
					return os.Stat(".")
				}

				return nil, os.ErrNotExist
			}
			env.readFile = func(path string) ([]byte, error) {
				if data, ok := tc.files[path]; ok {
					return []byte(data), nil
				}

				return nil, os.ErrNotExist
			}

			if got := containerHint(env); got != tc.want {
				t.Errorf("containerHint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The check table in settings.md lists every registered check, by id, with
// its group, and nothing else.
func TestDoctorChecksMatchDocs(t *testing.T) {
	t.Parallel()

	got := make(map[string]string)

	for _, row := range docTable(t, settingsDocPath, "Checking the Configuration", 4) {
		group := firstSpan(row[1])
		for _, m := range codeSpan.FindAllStringSubmatch(row[0], -1) {
			got[m[1]] = group
		}
	}

	want := make(map[string]string)
	for _, def := range doctorChecks() {
		want[def.id] = def.group
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("%s check table does not match the registered checks (-registry +docs):\n%s", settingsDocPath, diff)
	}
}

// -x belongs to --no-print-settings, and no two of doctor's flags, its own or
// inherited, share a shorthand.
func TestDoctorFlagShorthands(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"doctor"})
	if err != nil {
		t.Fatalf("rootCmd.Find(doctor) error = %v", err)
	}

	owners := make(map[string][]string)
	visit := func(f *pflag.Flag) {
		if f.Shorthand != "" {
			owners[f.Shorthand] = append(owners[f.Shorthand], f.Name)
		}
	}

	cmd.LocalFlags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)

	for shorthand, names := range owners {
		if len(names) > 1 {
			t.Errorf("-%s is the shorthand of %v", shorthand, names)
		}
	}

	if diff := cmp.Diff([]string{"no-print-settings"}, owners["x"]); diff != "" {
		t.Errorf("-x owners mismatch (-want +got):\n%s", diff)
	}
}
