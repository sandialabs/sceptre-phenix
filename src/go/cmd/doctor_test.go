package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/viper"
	"pgregory.net/rapid"

	"phenix/util/common"
)

// statuses returns each check as "name status".
func statuses(checks []doctorCheck) []string {
	out := make([]string, 0, len(checks))

	for _, c := range checks {
		out = append(out, c.Name+" "+string(c.Status))
	}

	return out
}

// A parsed config file passes with its keys, a missing one warns and a broken one
// fails; a missing users file gets no check and a broken one fails.
func TestDoctorConfigFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config, users string // no file when empty
		want          []string
		wantKeys      map[string]bool
	}{
		"config only": {
			config:   "log:\n  level: debug\nstore:\n  endpoint: bolt:///tmp/s.bdb\n",
			want:     []string{"config file pass"},
			wantKeys: map[string]bool{"log.level": true, "store.endpoint": true},
		},
		"config and users": {
			config:   "log:\n  level: debug\n",
			users:    "ui:\n  users: [admin:secret:global-admin]\n",
			want:     []string{"config file pass", "users file pass"},
			wantKeys: map[string]bool{"log.level": true, "ui.users": true},
		},
		"no config": {
			want:     []string{"config file warn"},
			wantKeys: map[string]bool{},
		},
		"bad config": {
			config:   "log: [\n",
			want:     []string{"config file fail"},
			wantKeys: map[string]bool{},
		},
		"bad users": {
			config:   "log:\n  level: debug\n",
			users:    "ui: [\n",
			want:     []string{"config file pass", "users file fail"},
			wantKeys: map[string]bool{"log.level": true},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			if tc.config != "" {
				writeTestFile(t, dir, "config.yaml", tc.config)
			}

			if tc.users != "" {
				writeTestFile(t, dir, "users.yaml", tc.users)
			}

			config := doctorConfigFiles([]string{dir})

			var checks []doctorCheck

			for _, name := range []string{"config", "users"} {
				if c, ok := config.checks[name]; ok {
					checks = append(checks, c)
				}
			}

			if diff := cmp.Diff(tc.want, statuses(checks)); diff != "" {
				t.Errorf("doctorConfigFiles() checks mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantKeys, config.keys); diff != "" {
				t.Errorf("doctorConfigFiles() keys mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Keys phenix does not read, such as a misspelling, warn by name.
func TestUnknownKeysCheck(t *testing.T) {
	t.Parallel()

	known := []string{"log.level", "store.endpoint", "ui.listen-endpoint"}

	got := unknownKeysCheck(map[string]bool{"log.level": true}, known)
	if got.Status != doctorPass {
		t.Errorf("unknownKeysCheck(known keys) = %+v, want pass", got)
	}

	got = unknownKeysCheck(map[string]bool{"log.level": true, "ui.listen-endpiont": true, "logs.level": true}, known)

	want := doctorCheck{
		Name:   "config keys",
		Status: doctorWarn,
		Detail: "not phenix settings, so phenix ignores them: logs.level, ui.listen-endpiont",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("unknownKeysCheck() mismatch (-want +got):\n%s", diff)
	}
}

// Every setting phenix reads is known, including the ui settings and the
// hidden unix-socket-gid.
func TestKnownSettings(t *testing.T) {
	t.Parallel()

	known := knownSettings()

	for _, key := range []string{"store.endpoint", "log.system.path", "base-dir.injects", "ui.jwt-signing-key", "ui.users", "unix-socket-gid"} {
		if !slices.Contains(known, key) {
			t.Errorf("knownSettings() = %v, missing %s", known, key)
		}
	}
}

// The store and log files must be files this user can write, or creatable
// in the nearest directory above them that exists.
func TestWritableFileCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeTestFile(t, dir, "store.bdb", "")

	tests := map[string]struct {
		path string
		want doctorStatus
	}{
		"existing file":       {path: file, want: doctorPass},
		"missing file":        {path: filepath.Join(dir, "new.bdb"), want: doctorPass},
		"missing directories": {path: filepath.Join(dir, "a", "b", "new.log"), want: doctorPass},
		"directory":           {path: dir, want: doctorFail},
		"under a file":        {path: filepath.Join(file, "sub", "new.log"), want: doctorFail},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := osDoctorEnv().writableFileCheck("store.endpoint", tc.path); got.Status != tc.want {
				t.Errorf("writableFileCheck(%s) = %+v, want %s", tc.path, got, tc.want)
			}
		})
	}
}

// BoltDB does not create the store's directory, so a store whose directory
// is missing fails, unlike a log file.
func TestStoreCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeTestFile(t, dir, "store.bdb", "")

	tests := map[string]struct {
		endpoint string
		want     doctorStatus
	}{
		"existing file":     {endpoint: "bolt://" + file, want: doctorPass},
		"missing file":      {endpoint: "bolt://" + filepath.Join(dir, "new.bdb"), want: doctorPass},
		"missing directory": {endpoint: "bolt://" + filepath.Join(dir, "a", "new.bdb"), want: doctorFail},
		"under a file":      {endpoint: "bolt://" + filepath.Join(file, "new.bdb"), want: doctorFail},
		"wrong scheme":      {endpoint: "etcd://" + file, want: doctorFail},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := osDoctorEnv().storeCheck(tc.endpoint); got.Status != tc.want {
				t.Errorf("storeCheck(%s) = %+v, want %s", tc.endpoint, got, tc.want)
			}
		})
	}
}

// A file that cannot be written, or created, fails. Root can write anything,
// so this needs another user.
func TestWritableFileCheckReadOnly(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only directory")
	}

	dir := t.TempDir()
	file := writeTestFile(t, dir, "store.bdb", "")

	if err := os.Chmod(file, 0o400); err != nil {
		t.Fatalf("os.Chmod(%s) error = %v", file, err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("os.Chmod(%s) error = %v", dir, err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	for _, path := range []string{file, filepath.Join(dir, "new.bdb")} {
		if got := osDoctorEnv().writableFileCheck("store.endpoint", path); got.Status != doctorFail {
			t.Errorf("writableFileCheck(%s) = %+v, want fail", path, got)
		}
	}
}

// A missing or relative base directory warns, and a file where one should be fails.
func TestBaseDirCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeTestFile(t, dir, "file", "")

	tests := map[string]struct {
		path string
		want doctorStatus
	}{
		"directory": {path: dir, want: doctorPass},
		"missing":   {path: filepath.Join(dir, "missing"), want: doctorWarn},
		"file":      {path: file, want: doctorFail},
		"relative":  {path: ".", want: doctorWarn},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := osDoctorEnv().baseDirCheck("base-dir.phenix", tc.path, true); got.Status != tc.want {
				t.Errorf("baseDirCheck(%s) = %+v, want %s", tc.path, got, tc.want)
			}
		})
	}
}

// minimega's version passes, an error fails, and a probe that never answers
// fails once the timeout passes.
func TestMinimegaCheck(t *testing.T) {
	t.Parallel()

	hung := make(chan struct{})
	t.Cleanup(func() { close(hung) })

	tests := map[string]struct {
		probe func(time.Duration) (string, error)
		want  doctorCheck
	}{
		"answers": {
			probe: func(time.Duration) (string, error) { return "minimega 2.9", nil },
			want:  doctorCheck{Name: "minimega", Status: doctorPass, Detail: "minimega 2.9"},
		},
		"error": {
			probe: func(time.Duration) (string, error) { return "", errors.New("connection refused") },
			want: doctorCheck{
				Name:   "minimega",
				Status: doctorFail,
				Detail: "no answer on /tmp/mm/minimega: connection refused",
			},
		},
		"hangs": {
			probe: func(time.Duration) (string, error) { <-hung; return "", nil },
			want: doctorCheck{
				Name:   "minimega",
				Status: doctorFail,
				Detail: "no answer on /tmp/mm/minimega within 50ms",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := minimegaCheck(t.Context(), "/tmp/mm", 50*time.Millisecond, tc.probe)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("minimegaCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A flag wins, then a config value the global settings prefer, then the
// environment, then any other config value, then the default.
func TestSettingSource(t *testing.T) {
	t.Parallel()

	env := map[string]string{"PHENIX_UI_LISTEN_ENDPOINT": ":3080", "PHENIX_LOG_LEVEL": ""}
	lookupEnv := func(name string) (string, bool) {
		v, ok := env[name]

		return v, ok
	}

	tests := map[string]struct {
		key                            string
		flagChanged, fileFirst, inFile bool
		want                           string
	}{
		"flag":             {key: "log.level", flagChanged: true, fileFirst: true, inFile: true, want: sourceFlag},
		"file first":       {key: "ui.listen-endpoint", fileFirst: true, inFile: true, want: sourceFile},
		"env over file":    {key: "ui.listen-endpoint", inFile: true, want: sourceEnv},
		"empty env":        {key: "log.level", inFile: true, want: sourceFile},
		"default":          {key: "log.level", want: sourceDefault},
		"env without file": {key: "ui.listen-endpoint", want: sourceEnv},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := settingSource(tc.key, tc.flagChanged, tc.fileFirst, tc.inFile, lookupEnv)
			if got != tc.want {
				t.Errorf("settingSource(%s) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// The environment variable of a setting is its key in upper case, prefixed
// with PHENIX_, with dots and dashes as underscores.
func TestSettingEnvVar(t *testing.T) {
	t.Parallel()

	if got := settingEnvVar("ui.logs.minimega-path"); got != "PHENIX_UI_LOGS_MINIMEGA_PATH" {
		t.Errorf("settingEnvVar() = %q, want PHENIX_UI_LOGS_MINIMEGA_PATH", got)
	}
}

// Set secrets are redacted, unset ones are left as they are so an empty JWT
// signing key still shows, and other settings are untouched.
func TestRedactSetting(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		key   string
		value any
		want  any
	}{
		"jwt key":       {key: "ui.jwt-signing-key", value: "s3cret", want: redacted},
		"empty jwt key": {key: "ui.jwt-signing-key", value: "", want: ""},
		"users":         {key: "ui.users", value: []any{"admin:pw:global-admin"}, want: redacted},
		"string users":  {key: "ui.users", value: []string{"admin:pw:global-admin"}, want: redacted},
		"no users":      {key: "ui.users", value: []string{}, want: []string{}},
		"nil users":     {key: "ui.users", value: nil, want: nil},
		"not a secret":  {key: "store.endpoint", value: "bolt:///etc/phenix/store.bdb", want: "bolt:///etc/phenix/store.bdb"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, redactSetting(tc.key, tc.value)); diff != "" {
				t.Errorf("redactSetting(%s) mismatch (-want +got):\n%s", tc.key, diff)
			}
		})
	}
}

// The overall status is the worst of the checks.
func TestWorstStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		statuses []doctorStatus
		want     doctorStatus
	}{
		"none":       {want: doctorPass},
		"all pass":   {statuses: []doctorStatus{doctorPass, doctorPass}, want: doctorPass},
		"one warn":   {statuses: []doctorStatus{doctorPass, doctorWarn}, want: doctorWarn},
		"warn, fail": {statuses: []doctorStatus{doctorWarn, doctorFail, doctorPass}, want: doctorFail},
		"skips":      {statuses: []doctorStatus{doctorSkip, doctorSkip}, want: doctorPass},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checks := make([]doctorResult, 0, len(tc.statuses))
			for _, s := range tc.statuses {
				checks = append(checks, doctorResult{Name: "c", Status: s})
			}

			if got := worstStatus(checks); got != tc.want {
				t.Errorf("worstStatus(%v) = %s, want %s", tc.statuses, got, tc.want)
			}
		})
	}
}

// Whatever the JWT signing key and users, in settings or environment variables,
// and whatever a sudo command or unknown PHENIX_ variable holds, neither the
// text nor the JSON report contains them, with or without --report.
func TestDoctorReportNeverShowsSecrets(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)

	rapid.Check(t, func(t *rapid.T) {
		secret := rapid.StringMatching(`[A-Za-z0-9|_-]{12,32}`).Draw(t, "secret")
		user := "admin:" + secret + ":global-admin"
		envUsers := rapid.SampledFrom([]string{
			user, user + " bob:" + secret, "bob:pw:" + secret + "," + user, secret,
		}).Draw(t, "users")

		in := fakeDoctorInput(dir, map[string]string{"ui.jwt-signing-key": secret})
		in.settings["ui.users"] = resolvedSetting{value: []any{user}, text: user, source: sourceFile}
		in.env = fakeDoctorEnv(map[string]string{
			"PHENIX_UI_JWT_SIGNING_KEY": secret,
			"PHENIX_UI_USERS":           envUsers,
			"PHENIX_UI_TOKEN":           secret,
			"SUDO_COMMAND":              "/usr/bin/phenix ui --jwt-signing-key " + secret,
		}, rapid.Bool().Draw(t, "root"))

		for _, output := range []string{"text", FormatJSON} {
			opts := doctorOptions{output: output, report: rapid.Bool().Draw(t, "report")}
			report := runDoctor(context.Background(), in, doctorChecks(), nil, opts)

			var out bytes.Buffer
			if err := writeDoctorReport(&out, report, opts); err != nil {
				t.Fatalf("writeDoctorReport() error = %v", err)
			}

			if strings.Contains(out.String(), secret) {
				t.Fatalf("%s report contains the secret %q:\n%s", output, secret, out.String())
			}
		}
	})
}

// phenix doctor reports each setting's source, redacts the JWT signing key,
// fails when minimega does not answer, and leaves common.MinimegaBase alone.
func TestDoctorCommand(t *testing.T) {
	const key = "doctor-test-signing-key"

	t.Setenv("PHENIX_UI_JWT_SIGNING_KEY", key)
	t.Setenv("PHENIX_BASE_DIR_MINIMEGA", t.TempDir())
	t.Setenv("PHENIX_STORE_ENDPOINT", "bolt://"+filepath.Join(t.TempDir(), "store.bdb"))

	base := common.MinimegaBase

	cmd := newDoctorCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true // as under the root command

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetArgs([]string{"-o", "json", "--timeout", "200ms"})
	cmd.SetContext(t.Context())

	if err := cmd.Execute(); !errors.Is(err, errDoctorFailed) {
		t.Fatalf("doctor error = %v, want %v (no minimega)", err, errDoctorFailed)
	}

	if strings.Contains(out.String(), key) {
		t.Errorf("doctor output contains the JWT signing key:\n%s", out.String())
	}

	if common.MinimegaBase != base {
		t.Errorf("doctor changed common.MinimegaBase from %q to %q", base, common.MinimegaBase)
	}

	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json.Unmarshal(doctor output) error = %v\n%s", err, out.String())
	}

	got := make(map[string]doctorSetting)
	for _, s := range report.Settings {
		got[s.Key] = s
	}

	want := map[string]doctorSetting{
		"ui.jwt-signing-key": {Key: "ui.jwt-signing-key", Value: redacted, Source: sourceEnv},
		"store.endpoint": {
			Key:    "store.endpoint",
			Value:  os.Getenv("PHENIX_STORE_ENDPOINT"),
			Source: sourceEnv,
		},
	}

	for k, w := range want {
		if diff := cmp.Diff(w, got[k]); diff != "" {
			t.Errorf("setting %s mismatch (-want +got):\n%s", k, diff)
		}
	}

	checks := make(map[string]doctorStatus)
	for _, c := range report.Checks {
		checks[c.Name] = c.Status
	}

	for name, status := range map[string]doctorStatus{"minimega": doctorFail, "store.endpoint": doctorPass, "ui.jwt-signing-key": doctorPass} {
		if checks[name] != status {
			t.Errorf("check %s = %q, want %q (checks %v)", name, checks[name], status, report.Checks)
		}
	}
}

// The ui.netflow.max-size check passes for a size phenix ui accepts, naming
// it, and fails for one it refuses, given the value as the registry gives it.
func TestNetflowMaxSizeCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value string
		want  doctorCheck
	}{
		"default": {value: "100", want: doctorCheck{Name: "ui.netflow.max-size", Status: doctorPass, Detail: "100 MiB"}},
		"off": {value: "0", want: doctorCheck{
			Name: "ui.netflow.max-size", Status: doctorPass, Detail: "0, so phenix ui records no netflow captures",
		}},
		"negative": {value: "-5", want: doctorCheck{
			Name: "ui.netflow.max-size", Status: doctorFail, Detail: `invalid ui.netflow.max-size "-5": it must not be negative`,
		}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := netflowMaxSizeCheck(viperWith("ui.netflow.max-size", tt.value))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("netflowMaxSizeCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
