package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
	"pgregory.net/rapid"

	"phenix/api/disk"
	"phenix/store"
	"phenix/version"
	"phenix/web/health"
)

const doctorTestMinimega = "minimega 3.0.1 837c9174 2026-07-08"

// newDoctorTestDir makes the phenix base directory, with the directories
// derived from it, and minimega's base directory under a new directory.
func newDoctorTestDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for _, sub := range []string{"phenix/injects", "phenix/topologies", "phenix/mounts", "minimega", "files"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", sub, err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), nil, 0o600); err != nil {
		t.Fatalf("os.WriteFile(config.yaml) error = %v", err)
	}

	return dir
}

// fakeDoctorInput is an input under dir, made by newDoctorTestDir, on which
// every check passes; settings replaces the values of some settings. There is
// no store file, and no store of a phenix ui that runs as root.
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
		"ui.jwt-lifetime":          "24h",
		"ui.users":                 "alice:Alicepass1!:global-admin",
		"ui.listen-endpoint":       "0.0.0.0:3000",
		"ui.file-server-endpoint":  "0",
		"unix-socket":              filepath.Join(dir, "phenix.sock"),
		"unix-socket-gid":          "-1",
	}

	maps.Copy(values, settings)

	known := knownSettings()
	resolved := make(map[string]resolvedSetting, len(known))

	for _, key := range known {
		resolved[key] = resolvedSetting{value: values[key], text: values[key], source: sourceDefault}
	}

	configFile := filepath.Join(dir, "config.yaml")

	env := fakeDoctorEnv(nil, false)
	env.lookPath = func(name string) (string, error) {
		if slices.Contains(append([]string{"vim", "vmdb2", "filebeat"}, doctorHostTools...), name) {
			return "/usr/bin/" + name, nil
		}

		return "", exec.ErrNotFound
	}
	// no tunneler downloads or WebShark build, wherever the tests run
	env.stat = func(path string) (os.FileInfo, error) {
		if !filepath.IsAbs(path) || strings.HasPrefix(path, "/opt/phenix") {
			return nil, os.ErrNotExist
		}

		return os.Stat(path)
	}
	// a Linux host on which the host checks pass, wherever the tests run
	doctorGoodHost().apply(&env)

	return &doctorInput{
		env: env,
		config: doctorConfig{
			checks: map[string]doctorCheck{"config": {Name: "config file", Status: doctorPass, Detail: configFile}},
			keys:   map[string]bool{},
			values: map[string]fileSetting{},
			used:   configFile,
		},
		known:    known,
		settings: resolved,
		probe:    func(string, time.Duration) (string, error) { return doctorTestMinimega, nil },
		minimega: (&doctorMinimega{mu: sync.Mutex{}, args: doctorMinimegaArgs(dir), size: "1", commands: nil}).run,
		server:   doctorServerAnswer(http.StatusOK, version.API(), doctorTestOptions),
		qemuImg: func(context.Context, string) (string, error) {
			return "qemu-img version 8.2.2 (Debian 1:8.2.2+ds-0ubuntu1)", nil
		},
		statfs: func(string) (health.DiskStat, error) {
			return health.DiskStat{Device: 1, Total: 100 << 30, Avail: 50 << 30}, nil
		},
		images: disk.BrokenImages,
		command: doctorCommands(map[string]doctorCommand{
			"ovs-vsctl --timeout=1 show":                               {out: doctorOVSShow, err: nil},
			"ovs-vsctl --timeout=1 --columns=_uuid,name list Mirror":   {out: "", err: nil},
			"ovs-vsctl --timeout=1 --columns=name,mirrors list Bridge": {out: "name : phenix\nmirrors : []\n", err: nil},
		}),
		timeout: time.Second,

		storeConfigs: func(context.Context, string, string, time.Duration) (store.Configs, error) { return nil, nil },
		now:          func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) },
		hostname:     func() (string, error) { return "doctor-test-host", nil },
		addrs:        func() ([]string, error) { return []string{"127.0.0.1/8", "192.0.2.10/24"}, nil },
		system:       doctorSystem{os: "linux", kernel: "6.8.0-49-generic", arch: "amd64"},
		uid:          1000,
		gid:          1000,
		userName:     "alice",
		cwd:          "/home/alice",

		configPaths:  []string{dir},
		settingsUsed: configFile,
		phenixUID:    "1000",
		home:         "/home/alice",
		systemStore:  filepath.Join(dir, "etc", "store.bdb"),
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

// replaceNamedRows returns rows with each row in with in place of the row of the same id and name, for a check with
// more than one row. A row "<old row> => <new row>" replaces the row <old row>.
func replaceNamedRows(rows []string, with ...string) []string {
	rows = slices.Clone(rows)

	for _, w := range with {
		prefix := w[:strings.LastIndex(w, " ")+1]
		if old, renamed, ok := strings.Cut(w, " => "); ok {
			prefix, w = old, renamed
		}

		i := slices.IndexFunc(rows, func(r string) bool { return strings.HasPrefix(r, prefix) })
		rows[i] = w
	}

	return rows
}

// withoutModeRows returns rows without the server's mode rows.
func withoutModeRows(rows []string) []string {
	return slices.DeleteFunc(slices.Clone(rows), func(r string) bool {
		return r == "server server bridge-mode pass" || r == "server server deploy-mode pass"
	})
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
//
//nolint:maintidx // one table
func TestDoctorChecksFromInput(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	allPass := []string{
		"config-file config file pass",
		"config-keys config keys pass",
		"config-paths config paths pass",
		"settings-file settings file pass",
		"env-overrides env overrides pass",
		"bridge-mode bridge-mode pass",
		"deploy-mode deploy-mode pass",
		"experiment.disk-check experiment.disk-check pass",
		"color color pass",
		"log.level log.level pass",
		"log.system.max-size log.system.max-size pass",
		"log.system.max-backups log.system.max-backups pass",
		"log.system.max-age log.system.max-age pass",
		"store.endpoint store.endpoint pass",
		"store-root store root pass",
		"log.system.path log.system.path pass",
		"base-dir.phenix base-dir.phenix pass",
		"base-dir.minimega base-dir.minimega pass",
		"base-dir.injects base-dir.injects pass",
		"base-dir.topologies base-dir.topologies pass",
		"mount-dir mount-dir pass",
		"ui.shutdown-grace-period ui.shutdown-grace-period pass",
		"ui.netflow.max-size ui.netflow.max-size pass",
		"ui.jwt-signing-key ui.jwt-signing-key pass",
		"ui.jwt-lifetime ui.jwt-lifetime pass",
		"ui.users ui.users pass",
		"ui.tls ui.tls pass",
		"ui.listen-endpoint ui.listen-endpoint pass",
		"ui.file-server-endpoint ui.file-server-endpoint pass",
		"unix-socket unix-socket pass",
		"unix-socket-gid unix-socket-gid pass",
		"server server pass",
		"server server bridge-mode pass",
		"server server deploy-mode pass",
		"minimega minimega pass",
		"minimega-env minimega files pass",
		"minimega-env minimega base pass",
		"minimega-env minimega mesh pass",
		"minimega-limits minimega-limits open files skip",
		"minimega-limits minimega-limits memory pass",
		"state-drift state-drift experiments skip",
		"state-drift state-drift namespaces skip",
		"vlans vlans pinned skip",
		"vlans vlans free skip",
		"bridge-trunk bridge-trunk skip",
		"schedule-hosts schedule-hosts skip",
		"cc-clients cc-clients skip",
		"qemu-img qemu-img pass",
		"vmdb2 vmdb2 pass",
		"filebeat filebeat pass",
		"apps apps pass",
		"disk disk phenix-base pass",
		"kernel-modules kernel-modules pass",
		"nbd nbd pass",
		"openvswitch openvswitch pass",
		"image-build image-build tools pass",
		"image-build image-build root pass",
		"image-build image-build loop pass",
		"image-build image-build keyrings pass",
		"disk-chains disk chains pass",
		"disk-formats disk formats pass",
		"writable-images writable-images skip",
		"leftovers leftovers snapshots skip",
		"leftovers leftovers files skip",
		"leftovers leftovers mounts skip",
		"leftovers leftovers taps pass",
		"leftovers leftovers mirrors pass",
		"disk-space disk-space files pass",
		"disk-space disk-space inject dirs pass",
		"image-locks image-locks skip",
		"tunneler-download tunneler-download pass",
		"webshark webshark pass",
		"ui.features ui.features pass",
		"ui.logs.minimega-path ui.logs.minimega-path pass",
	}

	// with a store file, the rows that read the stored experiments pass
	withStore := replaceNamedRows(allPass, "writable-images writable-images pass", "leftovers leftovers snapshots pass",
		"leftovers leftovers files pass", "leftovers leftovers mounts pass", "image-locks image-locks pass",
		"state-drift state-drift experiments pass", "state-drift state-drift namespaces pass", "vlans vlans pinned pass",
		"vlans vlans free pass", "schedule-hosts schedule-hosts pass", "cc-clients cc-clients pass")

	storeFile := filepath.Join(dir, "valid.bdb")
	if err := os.WriteFile(storeFile, doctorFilesBolt(doctorFilesPageSize), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", storeFile, err)
	}

	notStore := writeTestFile(t, dir, "not-a-store.bdb", "hello\n")
	otherConfig := filepath.Join(dir, "other", "config.yaml")
	doctorFilesCreate(t, otherConfig, false)

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
		"store file": {
			settings: map[string]string{"store.endpoint": "bolt://" + storeFile},
			modify:   func(in *doctorInput) { in.uid = os.Getuid() },
			want: slices.Insert(slices.Clone(withStore), slices.Index(allPass, "store.endpoint store.endpoint pass")+1,
				"store-file store file pass", "store-owner store owner pass"),
		},
		"store of another user": {
			settings: map[string]string{"store.endpoint": "bolt://" + storeFile},
			modify: func(in *doctorInput) {
				stat := in.env.stat
				in.env.stat = func(path string) (os.FileInfo, error) {
					info, err := stat(path)
					if err != nil || path != storeFile {
						return info, err
					}

					return doctorFilesOwned{FileInfo: info, uid: 4242}, nil
				}
			},
			want: slices.Insert(slices.Clone(withStore), slices.Index(allPass, "store.endpoint store.endpoint pass")+1,
				"store-file store file pass", "store-owner store owner warn"),
		},
		"not a store": {
			settings: map[string]string{"store.endpoint": "bolt://" + notStore},
			modify:   func(in *doctorInput) { in.uid = os.Getuid() },
			want: slices.Insert(slices.Clone(withStore), slices.Index(allPass, "store.endpoint store.endpoint pass")+1,
				"store-file store file fail", "store-owner store owner pass"),
		},
		"root store exists": {
			modify: func(in *doctorInput) { in.systemStore = notStore },
			want:   replaceRows(allPass, "store-root store root warn"),
		},
		"store endpoint set": {
			modify: func(in *doctorInput) {
				in.settings["store.endpoint"] = resolvedSetting{
					value: in.text("store.endpoint"), text: in.text("store.endpoint"), source: sourceFlag,
				}
				in.systemStore = notStore
			},
			want: slices.DeleteFunc(slices.Clone(allPass), func(r string) bool { return strings.HasPrefix(r, "store-root ") }),
		},
		"settings set writes another file": {
			modify: func(in *doctorInput) { in.settingsUsed, in.home = "", filepath.Join(dir, "home") },
			want:   replaceRows(allPass, "settings-file settings file warn"),
		},
		"config file phenix ignores": {
			modify: func(in *doctorInput) { in.configPaths = []string{dir, filepath.Dir(otherConfig)} },
			want:   replaceRows(allPass, "config-paths config paths warn"),
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
			modify: func(in *doctorInput) {
				in.settings["bridge-mode"] = resolvedSetting{value: "sideways", text: "sideways", source: sourceFlag}
			},
			// modes every command refuses are compared with no server's
			want: replaceRows(withoutModeRows(allPass),
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
		"auth settings phenix ui misreads": {
			settings: map[string]string{"ui.jwt-lifetime": "24", "ui.users": "bob:Bobpass1!", "ui.jwt-signing-key": "dev|bob"},
			want: replaceRows(allPass,
				"ui.jwt-signing-key ui.jwt-signing-key fail",
				"ui.jwt-lifetime ui.jwt-lifetime fail",
				"ui.users ui.users fail",
			),
		},
		"default user": {
			settings: map[string]string{"ui.users": ""},
			want:     with("ui.users", "ui.users ui.users warn", false),
		},
		"proxy JWTs on every address": {
			settings: map[string]string{"ui.jwt-signing-key": proxyJWTKey},
			want:     with("ui.users", "proxy-jwt proxy-jwt warn", true),
		},
		"proxy JWTs on loopback": {
			settings: map[string]string{"ui.jwt-signing-key": proxyJWTKey, "ui.listen-endpoint": "127.0.0.1:3000"},
			want:     with("ui.users", "proxy-jwt proxy-jwt pass", true),
		},
		"proxy header without sign-in": {
			settings: map[string]string{"ui.jwt-signing-key": "", "ui.proxy-auth-header": "X-Forwarded-User"},
			want: replaceRows(
				with("ui.users", "ui.proxy-auth-header ui.proxy-auth-header warn", true),
				"ui.jwt-signing-key ui.jwt-signing-key warn",
			),
		},
		"proxy header with sign-in": {
			settings: map[string]string{"ui.proxy-auth-header": "X-Forwarded-User"},
			want:     with("ui.users", "ui.proxy-auth-header ui.proxy-auth-header pass", true),
		},
		"TLS key without a certificate": {
			settings: map[string]string{"ui.tls-key": filepath.Join(dir, "phenix.key")},
			want:     with("ui.tls", "ui.tls ui.tls fail", false),
		},
		"listeners phenix ui refuses": {
			settings: map[string]string{"ui.listen-endpoint": "3000", "ui.file-server-endpoint": "99999"},
			want: replaceRows(allPass,
				"ui.listen-endpoint ui.listen-endpoint fail",
				"ui.file-server-endpoint ui.file-server-endpoint fail",
			),
		},
		"socket phenix ui cannot set up": {
			settings: map[string]string{"unix-socket": filepath.Join(dir, "missing", "phenix.sock"), "unix-socket-gid": "200"},
			want: replaceRows(allPass,
				"unix-socket unix-socket fail",
				"unix-socket-gid unix-socket-gid fail",
			),
		},
		"no socket": {
			settings: map[string]string{"unix-socket": "", "unix-socket-gid": "200"},
			// no server to ask for its modes
			want: withoutModeRows(allPass),
		},
		"minimega down": {
			modify: func(in *doctorInput) {
				in.probe = func(string, time.Duration) (string, error) { return "", errors.New("refused") }
			},
			// minimega-env checks nothing, in one skip row; no <base-dir.phenix>/images to check or measure
			want: slices.Replace(replaceNamedRows(allPass, "minimega minimega fail", "disk-chains disk chains skip",
				"disk-formats disk formats skip", "leftovers leftovers snapshots skip", "leftovers leftovers taps skip",
				"leftovers leftovers mirrors skip",
				"disk-space disk-space files pass => disk-space disk-space base pass"),
				slices.Index(allPass, "minimega-env minimega files pass"),
				slices.Index(allPass, "minimega-env minimega mesh pass")+1,
				"minimega-env minimega-env skip"),
		},
		"tools missing": {
			modify: func(in *doctorInput) {
				in.env.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
				in.statfs = func(string) (health.DiskStat, error) { return health.DiskStat{Device: 1, Total: 100, Avail: 1}, nil }
			},
			want: replaceNamedRows(allPass,
				"qemu-img qemu-img fail", "vmdb2 vmdb2 fail", "filebeat filebeat warn", "disk disk phenix-base fail",
				"nbd nbd fail", "openvswitch openvswitch fail", "image-build image-build tools warn",
				"leftovers leftovers taps skip", "leftovers leftovers mirrors skip", "disk-space disk-space files warn"),
		},
		"broken disk image": {
			modify: func(in *doctorInput) {
				in.images = func(context.Context, string) (disk.ChainScan, error) {
					return disk.ChainScan{Images: 1, Checked: 1, Broken: []disk.BrokenImage{{Path: "a.qc2", Problem: "x"}}}, nil
				}
			},
			want: replaceRows(allPass, "disk-chains disk chains warn"),
		},
		"features phenix ui turns on by itself": {
			settings: map[string]string{"ui.features": "vm-mount webshark file-server vm-mnt"},
			want:     replaceRows(allPass, "ui.features ui.features fail"),
		},
		"minimega log missing": {
			settings: map[string]string{"ui.logs.minimega-path": filepath.Join(dir, "minimega.log")},
			want:     replaceRows(allPass, "ui.logs.minimega-path ui.logs.minimega-path warn"),
		},
		"server down": {
			modify: func(in *doctorInput) { in.server = doctorServerDialError(syscall.ENOENT) },
			want:   replaceRows(withoutModeRows(allPass), "server server warn"),
		},
		"server modes differ": {
			settings: map[string]string{"deploy-mode": "all"},
			want: slices.Replace(slices.Clone(allPass), slices.Index(allPass, "server server deploy-mode pass"),
				slices.Index(allPass, "server server deploy-mode pass")+1, "server server deploy-mode warn"),
		},
		"phenix ui in auto, bridge-mode not set here": {
			modify: func(in *doctorInput) {
				in.server = doctorServerAnswer(http.StatusOK, version.API(), `{"bridge-mode":"auto","deploy-mode":"no-headnode"}`)
			},
			want: allPass,
		},
		"phenix ui in auto, manual set here": {
			modify: func(in *doctorInput) {
				in.server = doctorServerAnswer(http.StatusOK, version.API(), `{"bridge-mode":"auto","deploy-mode":"no-headnode"}`)
				in.settings["bridge-mode"] = resolvedSetting{value: "manual", text: "manual", source: sourceFlag}
			},
			want: slices.Replace(slices.Clone(allPass), slices.Index(allPass, "server server bridge-mode pass"),
				slices.Index(allPass, "server server bridge-mode pass")+1, "server server bridge-mode warn"),
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
		"environment overrides the config file": {
			modify: func(in *doctorInput) {
				vars := fakeDoctorEnv(map[string]string{"PHENIX_LOG_LEVEL": "debug"}, false)
				in.env.lookup, in.env.environ = vars.lookup, vars.environ
				in.config.keys["log.level"] = true
				in.config.values["log.level"] = fileSetting{file: in.config.used, value: "info"}
				in.settings["log.level"] = resolvedSetting{value: "debug", text: "debug", source: sourceEnv}
			},
			want: replaceRows(
				append(append(slices.Clone(allPass[:4]), "env PHENIX_LOG_LEVEL pass"), allPass[4:]...),
				"env-overrides env overrides warn",
			),
		},
		"environment variables": {
			modify: func(in *doctorInput) {
				vars := fakeDoctorEnv(map[string]string{"PHENIX_LOG_LEVEL": "loud", "PATH": "/usr/bin"}, false)
				in.env.lookup, in.env.environ = vars.lookup, vars.environ
				in.settings["log.level"] = resolvedSetting{value: "loud", text: "loud", source: sourceEnv}
			},
			// after the config checks, in name order
			want: replaceRows(
				append(append(slices.Clone(allPass[:4]), "env PATH pass", "env PHENIX_LOG_LEVEL warn"), allPass[4:]...),
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
			ID: "ui.jwt-signing-key", Group: doctorGroupAuth, Name: "ui.jwt-signing-key", Status: doctorWarn,
			Detail: jwtKeyCheck("", nil).Detail,
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
		"none":   {want: []string{}},
		"one id": {args: []string{"--skip", "ui.jwt-signing-key"}, want: []string{"ui.jwt-signing-key"}},
		"group": {args: []string{"--skip", "minimega"}, want: []string{
			"bridge-trunk", "cc-clients", "minimega", "minimega-env", "minimega-limits", "schedule-hosts", "state-drift", "vlans",
		}},
		"id": {args: []string{"--skip", "minimega-env,server"}, want: []string{"minimega-env", "server"}},
		"group of several": {
			args: []string{"--skip", "config"},
			want: []string{"config-file", "config-keys", "config-paths", "settings-file", "users-file"},
		},
		"settings group": {
			args: []string{"--skip", "settings"},
			want: []string{
				"bridge-mode", "color", "deploy-mode", "experiment.disk-check", "log.level", "log.system.max-age",
				"log.system.max-backups", "log.system.max-size",
			},
		},
		"comma list and repeated": {
			args: []string{"--skip", "env, store.endpoint", "--skip", "ui"},
			want: []string{"env", "env-overrides", "store.endpoint", "ui.netflow.max-size", "ui.shutdown-grace-period"},
		},
		"auth, tls and socket groups": {
			args: []string{"--skip", "auth,tls", "--skip", "socket"},
			want: []string{
				"proxy-jwt", "ui.file-server-endpoint", "ui.jwt-lifetime", "ui.jwt-signing-key", "ui.listen-endpoint",
				"ui.proxy-auth-header", "ui.tls", "ui.users", "unix-socket", "unix-socket-gid",
			},
		},
		"unknown": {
			args:    []string{"--skip", "minimega,mm,cofnig"},
			wantErr: `--skip: unknown check "mm", "cofnig". Checks: config-file, users-file, config-keys, config-paths, settings-file, env,`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd := newDoctorCmd()
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatalf("ParseFlags(%v) error = %v", tc.args, err)
			}

			skip, err := parseDoctorSkip(doctorFlagOptions(cmd.Flags(), io.Discard).skip, doctorChecks())

			if tc.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Fatalf("parseDoctorSkip(%v) error = %v, want prefix %q", tc.args, err, tc.wantErr)
				}

				groups := "Groups: config, env, settings, files, dirs, ui, auth, tls, socket, server, minimega, tools, images, features"
				if !strings.HasSuffix(err.Error(), groups) {
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
				"\nFAIL: 1 failed, 1 warned, 52 passed, 18 skipped. Failed: minimega. Warned: ui.jwt-signing-key\n",
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
			want: []string{
				"```text\nphenix:", "user:", "<user-1> (uid 1000, gid 1000)", "directory:", "/home/<user-1>",
				"time:", "\nSETTING", "\nCHECK", "\nFAIL: ", "```\n",
			},
			wantNot: []string{"hostname:", "doctor-test-host", "alice", "Alicepass1!"},
		},
		"problems only": {
			opts: doctorOptions{output: "text", problems: true},
			want: []string{
				"\nCHECK", "ui.jwt-signing-key", "WARN", "minimega", "FAIL",
				"\nFAIL: 1 failed, 1 warned, 52 passed, 18 skipped. Failed: minimega. Warned: ui.jwt-signing-key. " +
					"70 passing or skipped checks hidden\n",
			},
			wantNot: []string{" PASS ", " SKIP "},
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
// header, and an id and group on every check, and never a hostname. With
// --report, the user is a token.
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

		if _, ok := decoded.Environment["hostname"]; ok {
			t.Errorf("environment = %v, want no hostname", decoded.Environment)
		}

		if user := decoded.Environment["user"]; report && user != "<user-1>" {
			t.Errorf("--report environment user = %v, want <user-1>", user)
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
