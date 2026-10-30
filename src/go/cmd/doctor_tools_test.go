package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/web/health"
)

// lookPathIn finds only the programs named in found.
func lookPathIn(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := found[name]; ok {
			return path, nil
		}

		return "", exec.ErrNotFound
	}
}

// qemu-img passes with its version, and fails when it is missing or its
// version fails, since phenix commits VMs to disk images with it.
func TestQemuImgCheck(t *testing.T) {
	t.Parallel()

	version := func(context.Context, string) (string, error) { return "qemu-img version 8.2.2", nil }
	broken := func(context.Context, string) (string, error) { return "", errors.New("exit status 1") }
	found := lookPathIn(map[string]string{"qemu-img": "/usr/bin/qemu-img"})

	tests := map[string]struct {
		lookPath func(string) (string, error)
		version  func(context.Context, string) (string, error)
		want     doctorCheck
	}{
		"found": {lookPath: found, version: version, want: row("", doctorPass, "/usr/bin/qemu-img: qemu-img version 8.2.2")},
		"missing": {
			lookPath: lookPathIn(nil), version: version,
			want: row("", doctorFail, "qemu-img not found; phenix inspects disk images through minimega instead; "+
				"phenix commits VMs to disk images with it"),
		},
		"broken": {
			lookPath: found, version: broken,
			want: row("", doctorFail,
				"running /usr/bin/qemu-img --version: exit status 1 (phenix commits VMs to disk images with it)"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := qemuImgCheck(t.Context(), time.Second, tc.lookPath, tc.version)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("qemuImgCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A missing tool has the status it is registered with, and says what phenix
// cannot do without it.
func TestToolCheck(t *testing.T) {
	t.Parallel()

	lookPath := lookPathIn(map[string]string{"vmdb2": "/usr/bin/vmdb2"})

	if diff := cmp.Diff(row("", doctorPass, "/usr/bin/vmdb2"), toolCheck(lookPath, "vmdb2", doctorFail, "x")); diff != "" {
		t.Errorf("toolCheck(vmdb2) mismatch (-want +got):\n%s", diff)
	}

	want := row("", doctorWarn, "filebeat is not a program on PATH, so SCORCH starts no Filebeat")
	if diff := cmp.Diff(want, toolCheck(lookPath, "filebeat", doctorWarn, "SCORCH starts no Filebeat")); diff != "" {
		t.Errorf("toolCheck(filebeat) mismatch (-want +got):\n%s", diff)
	}
}

// The apps check lists each user app and SCORCH component on PATH once, by
// the name a config gives it, leaving out what is not a program.
func TestAppsCheck(t *testing.T) {
	t.Parallel()

	first, second := t.TempDir(), t.TempDir()

	write := func(dir, name string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", name, err)
		}
	}

	write(first, "phenix-app-sceptre", 0o755)
	write(first, "phenix-scorch-component-cmd", 0o755)
	write(first, "phenix-app-notes.txt", 0o644)
	write(second, "phenix-app-sceptre", 0o755)
	write(second, "phenix-app-wireguard", 0o755)
	write(second, "minimega", 0o755)

	tests := map[string]struct {
		path string
		want string
	}{
		"both":    {path: first + ":" + second, want: "apps: sceptre, wireguard. SCORCH components: cmd"},
		"apps":    {path: second, want: "apps: sceptre, wireguard. SCORCH components: none"},
		"none":    {path: t.TempDir(), want: "no phenix-app-* or phenix-scorch-component-* programs on PATH"},
		"missing": {path: filepath.Join(first, "missing"), want: "no phenix-app-* or phenix-scorch-component-* programs on PATH"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(map[string]string{"PATH": tc.path}, false)
			env.lookPath = func(name string) (string, error) {
				for _, dir := range filepath.SplitList(tc.path) {
					if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Mode()&0o111 != 0 {
						return filepath.Join(dir, name), nil
					}
				}

				return "", exec.ErrNotFound
			}

			if diff := cmp.Diff(row("", doctorPass, tc.want), env.appsCheck()); diff != "" {
				t.Errorf("appsCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The disk check measures each directory that exists once per filesystem,
// warning at 90% and failing at 98% as the readiness check does.
func TestDiskChecks(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	files := filepath.Join(dir, "files")

	statfs := func(avail map[string]uint64) func(string) (health.DiskStat, error) {
		return func(path string) (health.DiskStat, error) {
			a, ok := avail[path]
			if !ok {
				return health.DiskStat{}, errors.New("no such filesystem")
			}

			return health.DiskStat{Device: a, Total: 100, Avail: a}, nil
		}
	}

	tests := map[string]struct {
		settings map[string]string
		args     map[string]string
		avail    map[string]uint64
		want     []doctorCheck
	}{
		"one filesystem": {
			avail: map[string]uint64{filepath.Join(dir, "phenix"): 40, dir: 40},
			want:  []doctorCheck{row("disk phenix-base", doctorPass, "60.0% used, "+filepath.Join(dir, "phenix")+": 40 bytes free")},
		},
		"files directory nearly full": {
			args:  map[string]string{"filepath": files},
			avail: map[string]uint64{filepath.Join(dir, "phenix"): 40, dir: 40, files: 5},
			want: []doctorCheck{
				row("disk phenix-base", doctorPass, "60.0% used, "+filepath.Join(dir, "phenix")+": 40 bytes free"),
				row("disk files-dir", doctorWarn, "95.0% used, "+files+": 5 bytes free"),
			},
		},
		"full, and a store directory that is missing": {
			settings: map[string]string{"store.endpoint": "bolt://" + filepath.Join(dir, "missing", "store.bdb")},
			avail:    map[string]uint64{filepath.Join(dir, "phenix"): 1},
			want:     []doctorCheck{row("disk phenix-base", doctorFail, "99.0% used, "+filepath.Join(dir, "phenix")+": 1 bytes free")},
		},
		"nothing to measure": {
			settings: map[string]string{"base-dir.phenix": filepath.Join(dir, "missing"), "store.endpoint": "bolt://"},
			want:     []doctorCheck{row("", doctorPass, "no directory to measure")},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(dir, tc.settings)
			in.statfs = statfs(tc.avail)
			in.found = &doctorFound{minimegaUp: true, minimegaArgs: tc.args}

			if diff := cmp.Diff(tc.want, diskChecks(t.Context(), in)); diff != "" {
				t.Errorf("diskChecks() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// statOf stats each path in isDir as a directory or a regular file, and no
// other path.
func statOf(t *testing.T, isDir map[string]bool) func(string) (os.FileInfo, error) {
	t.Helper()

	dir := t.TempDir()
	file := filepath.Join(dir, "file")

	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	return func(path string) (os.FileInfo, error) {
		d, ok := isDir[path]

		switch {
		case !ok:
			return nil, os.ErrNotExist
		case d:
			return os.Stat(dir)
		}

		return os.Stat(file)
	}
}

// Tunneler downloads and WebShark turn on when phenix ui finds what each
// needs, relative to the working directory it starts in.
func TestOptionalFeatureChecks(t *testing.T) {
	t.Parallel()

	const cwd = "/home/alice"

	tests := map[string]struct {
		files        map[string]bool
		wantTunneler doctorCheck
		wantShark    doctorCheck
	}{
		"neither": {
			wantTunneler: row("", doctorPass,
				"off: no downloads/tunneler (under /home/alice), and phenix ui looks in the directory it starts in"),
			wantShark: row("", doctorPass, "off: no index.html in webshark (under /home/alice) or /opt/phenix/webshark"),
		},
		"both": {
			files:        map[string]bool{"downloads/tunneler": true, "webshark/index.html": false},
			wantTunneler: row("", doctorPass, "on: /home/alice/downloads/tunneler"),
			wantShark:    row("", doctorPass, "on: the build in /home/alice/webshark"),
		},
		"tunneler file, installed WebShark": {
			files: map[string]bool{"downloads/tunneler": false, "/opt/phenix/webshark/index.html": false},
			wantTunneler: row("", doctorWarn,
				"on, but downloads/tunneler (under /home/alice) is not a directory, so listing tunneler downloads fails"),
			wantShark: row("", doctorPass, "on: the build in /opt/phenix/webshark"),
		},
		"WebShark directory without a build": {
			files: map[string]bool{"webshark/index.html": true},
			wantTunneler: row("", doctorPass,
				"off: no downloads/tunneler (under /home/alice), and phenix ui looks in the directory it starts in"),
			wantShark: row("", doctorPass, "off: no index.html in webshark (under /home/alice) or /opt/phenix/webshark"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			env.stat = statOf(t, tc.files)

			if diff := cmp.Diff(tc.wantTunneler, env.tunnelerDownloadCheck(cwd)); diff != "" {
				t.Errorf("tunnelerDownloadCheck() mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantShark, env.webSharkCheck(cwd)); diff != "" {
				t.Errorf("webSharkCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ui.features warns about an unknown name with a suggestion and about a
// feature phenix ui turns on itself set without what it needs, and fails for
// webshark without a build, which phenix ui refuses.
func TestFeaturesSettingCheck(t *testing.T) {
	t.Parallel()

	const cwd = "/opt/phenix"

	tests := map[string]struct {
		features   []string
		files      map[string]bool
		fileServer string
		want       doctorCheck
		wantHint   string
	}{
		"none":     {want: row("", doctorPass, "none set")},
		"vm-mount": {features: []string{"vm-mount"}, want: row("", doctorPass, "vm-mount")},
		"misspelled": {
			features: []string{"vm-mount", "vmmount", "ksjdhf"},
			want:     row("", doctorWarn, `unknown feature "vmmount" (did you mean vm-mount?). unknown feature "ksjdhf"`),
			wantHint: "remove or fix the unknown features",
		},
		"comma": {
			features: []string{"vm-mount,webshark"},
			want: row("", doctorWarn,
				`unknown feature "vm-mount,webshark". Separate features with spaces, or list them in YAML`),
			wantHint: "remove or fix the unknown features",
		},
		"webshark without a build": {
			features: []string{"webshark"},
			want: row("", doctorFail, "webshark is set by hand without a WebShark build in webshark (under /opt/phenix) "+
				"or /opt/phenix/webshark, so phenix ui refuses to start"),
			wantHint: "keep only vm-mount",
		},
		"auto features with what they need": {
			features:   []string{"webshark", "tunneler-download", "file-server"},
			files:      map[string]bool{"/opt/phenix/webshark/index.html": false, "downloads/tunneler": true},
			fileServer: "8080",
			want:       row("", doctorPass, "webshark tunneler-download file-server"),
		},
		"auto features without": {
			features:   []string{"tunneler-download", "file-server", "webshark"},
			fileServer: "0",
			want: row("", doctorFail, "tunneler-download is set by hand without downloads/tunneler (under /opt/phenix), "+
				"so the download menu lists no tunnelers. file-server is set by hand without ui.file-server-endpoint, "+
				"so no file server runs. webshark is set by hand without a WebShark build in webshark (under /opt/phenix) "+
				"or /opt/phenix/webshark, so phenix ui refuses to start"),
			wantHint: "keep only vm-mount",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			env.stat = statOf(t, tc.files)

			if diff := cmp.Diff(tc.want, env.featuresSettingCheck(tc.features, cwd, tc.fileServer)); diff != "" {
				t.Errorf("featuresSettingCheck(%v) mismatch (-want +got):\n%s", tc.features, diff)
			}

			if tc.wantHint != "" && !strings.HasPrefix(featuresHint(tc.want), tc.wantHint) {
				t.Errorf("featuresHint() = %q, want prefix %q", featuresHint(tc.want), tc.wantHint)
			}
		})
	}
}

// Every known feature passes as a value phenix ui knows, and any one edit
// away from one, that is no feature itself, warns and suggests one.
func TestUnknownFeatureProperty(t *testing.T) {
	t.Parallel()

	known := append([]string{"vm-mount"}, autoFeatures...)

	rapid.Check(t, func(t *rapid.T) {
		feature := rapid.SampledFrom(known).Draw(t, "feature")
		i := rapid.IntRange(0, len(feature)-1).Draw(t, "at")
		typo := feature[:i] + feature[i+1:]

		env := fakeDoctorEnv(nil, false)
		env.stat = func(string) (os.FileInfo, error) { return os.Stat(".") }

		if check := env.featuresSettingCheck([]string{"vm-mount"}, "/", ""); check.Status != doctorPass {
			t.Fatalf("featuresSettingCheck(vm-mount) = %+v", check)
		}

		if slices.Contains(known, typo) {
			return
		}

		check := env.featuresSettingCheck([]string{typo}, "/", "")
		if check.Status != doctorWarn || !strings.Contains(check.Detail, "did you mean ") {
			t.Fatalf("featuresSettingCheck(%q) = %+v, want a warning with a suggestion", typo, check)
		}
	})
}

// ui.logs.minimega-path warns when it names no readable file, which phenix ui
// waits for silently, and when it is unset while minimega logs to a file.
func TestMinimegaLogCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := filepath.Join(dir, "minimega.log")

	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	missing := filepath.Join(dir, "missing.log")

	tests := map[string]struct {
		path, minimegaLog string
		vars              map[string]string
		want              doctorCheck
	}{
		"set": {path: log, want: row("", doctorPass, log)},
		"missing": {
			path: missing,
			want: row("", doctorWarn, missing+" does not exist, so phenix ui waits for it without a warning, "+
				"and the Logs page shows no minimega logs"),
		},
		"unset": {want: row("", doctorPass, "not set, so the Logs page shows no minimega logs")},
		"unset, minimega logs": {
			minimegaLog: "/var/log/phenix/minimega.log",
			want: row("", doctorWarn, "not set, so the Logs page shows no minimega logs, but "+
				"minimega logs to /var/log/phenix/minimega.log (its -logfile)"),
		},
		"unset, MM_LOGFILE": {
			vars: map[string]string{"MM_LOGFILE": "/var/log/phenix/minimega.log"},
			want: row("", doctorWarn, "not set, so the Logs page shows no minimega logs, but "+
				"minimega logs to /var/log/phenix/minimega.log (MM_LOGFILE)"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(tc.vars, false)
			if diff := cmp.Diff(tc.want, env.minimegaLogCheck(tc.path, tc.minimegaLog)); diff != "" {
				t.Errorf("minimegaLogCheck(%q, %q) mismatch (-want +got):\n%s", tc.path, tc.minimegaLog, diff)
			}
		})
	}
}
