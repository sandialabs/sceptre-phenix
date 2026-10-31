package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.etcd.io/bbolt"
	"pgregory.net/rapid"

	"phenix/store"
)

// doctorHostPrefixes are the paths a doctorHostFS answers for, in place of the real file system.
//
//nolint:gochecknoglobals // constant list
var doctorHostPrefixes = []string{
	"/.dockerenv", "/proc", "/sys", "/dev", "/lib/modules", "/var/lock", "/var/run", "/run",
	"/usr/share/debootstrap", "/usr/share/keyrings",
}

// doctorHostFS is a fake Linux host: the content of each file by its path. A path that ends in / is a directory.
type doctorHostFS map[string]string

// apply makes env read the paths under doctorHostPrefixes from h, and every other path as before.
func (h doctorHostFS) apply(env *doctorEnv) {
	m := fstest.MapFS{}

	for path, data := range h {
		if dir, ok := strings.CutSuffix(path, "/"); ok {
			m[strings.TrimPrefix(dir, "/")] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}

			continue
		}

		m[strings.TrimPrefix(path, "/")] = &fstest.MapFile{Data: []byte(data), Mode: 0o644}
	}

	owned := func(path string) bool {
		return slices.ContainsFunc(doctorHostPrefixes, func(p string) bool {
			return path == p || strings.HasPrefix(path, p+"/")
		})
	}
	name := func(path string) string { return strings.TrimPrefix(filepath.Clean(path), "/") }

	stat, lstat, readFile, readDir := env.stat, env.lstat, env.readFile, env.readDir
	env.stat = func(path string) (os.FileInfo, error) {
		if owned(path) {
			return fs.Stat(m, name(path))
		}

		return stat(path)
	}
	env.lstat = func(path string) (os.FileInfo, error) {
		if owned(path) {
			return fs.Stat(m, name(path))
		}

		return lstat(path)
	}
	env.readFile = func(path string) ([]byte, error) {
		if owned(path) {
			return fs.ReadFile(m, name(path))
		}

		return readFile(path)
	}
	env.readDir = func(path string) ([]os.DirEntry, error) {
		if owned(path) {
			return fs.ReadDir(m, name(path))
		}

		return readDir(path)
	}
}

// doctorGoodHost is a Linux host on which the host checks pass: nbd loaded with max_part=10 and two free devices,
// Open vSwitch with ovs-vswitchd as pid 42, a free loop device, root's capabilities and the modules of its kernel.
func doctorGoodHost() doctorHostFS {
	return doctorHostFS{
		"/proc/modules":                         "nbd 65536 0 - Live 0x0\nopenvswitch 200704 1 - Live 0x0\n",
		"/proc/self/status":                     "Name:\tphenix\nCapEff:\t000001ffffffffff\n",
		"/proc/mounts":                          "proc /proc proc rw 0 0\n",
		"/proc/42/":                             "",
		"/sys/module/nbd/parameters/max_part":   "10\n",
		"/sys/module/openvswitch/":              "",
		"/dev/nbd0":                             "",
		"/dev/nbd1":                             "",
		"/dev/loop0":                            "",
		"/dev/loop-control":                     "",
		"/lib/modules/6.8.0-49-generic/":        "",
		"/var/run/openvswitch/ovs-vswitchd.pid": "42\n",
	}
}

// doctorOVSShow is what ovs-vsctl show prints with the bridge phenix.
const doctorOVSShow = `0b9d3ad4-6f8e-4b8e-9a3c-6a8f1d3e2c11
    Bridge phenix
        Port phenix
            Interface phenix
                type: internal
    ovs_version: "3.3.0"
`

// doctorCommand is what a fake program prints and returns.
type doctorCommand struct {
	out string
	err error
}

// doctorCommands runs each program by its command line in commands, and fails for any other.
func doctorCommands(commands map[string]doctorCommand) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		c, ok := commands[strings.Join(append([]string{name}, args...), " ")]
		if !ok {
			return nil, exec.ErrNotFound
		}

		return []byte(c.out), c.err
	}
}

// doctorHostTools are the programs on PATH of a host on which the host checks pass.
//
//nolint:gochecknoglobals // constant list
var doctorHostTools = []string{"qemu-nbd", "ovs-vsctl", "debootstrap", "kpartx", "losetup", "qemu-img"}

// hostInput is an input on host, with the programs found on PATH.
func hostInput(t *testing.T, host doctorHostFS, found []string) *doctorInput {
	t.Helper()

	in := fakeDoctorInput(newDoctorTestDir(t), nil)
	in.env = fakeDoctorEnv(nil, false)
	host.apply(&in.env)

	paths := make(map[string]string, len(found))
	for _, name := range found {
		paths[name] = "/usr/bin/" + name
	}

	in.env.lookPath = lookPathIn(paths)

	return in
}

// doctorHintOf is the hint runDoctorChecks gives c: none for a row that passes.
func doctorHintOf(c doctorCheck, hint func(doctorCheck) string) string {
	if c.Status == doctorPass {
		return ""
	}

	return hint(c)
}

// kernel-modules warns when modprobe finds no modules for the running kernel, or a reboot is pending, and skips a
// container without /lib/modules.
func TestKernelModulesCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		modify func(doctorHostFS)
		os     string
		kernel string
		want   doctorCheck
	}{
		"modules found": {want: row("", doctorPass, "/lib/modules/6.8.0-49-generic exists, and no reboot is pending")},
		"not linux": {
			os: "darwin", want: row("", doctorSkip, "not checked, because kernel modules are a Linux feature"),
		},
		"modules removed": {
			modify: func(h doctorHostFS) {
				delete(h, "/lib/modules/6.8.0-49-generic/")
				h["/lib/modules/6.8.0-51-generic/"] = ""
			},
			want: row("", doctorWarn, "/lib/modules/6.8.0-49-generic does not exist, so modprobe cannot load modules "+
				"such as nbd and openvswitch. A package upgrade can remove the modules of the running kernel"),
		},
		"kernel release not known": {
			kernel: "unknown", want: row("", doctorSkip, "not checked, because the release of the running kernel is not known"),
		},
		"reboot pending": {
			modify: func(h doctorHostFS) { h[rebootRequired] = "*** System restart required ***\n" },
			want:   row("", doctorWarn, "/var/run/reboot-required exists, so the host waits for a reboot after an upgrade"),
		},
		"container without modules": {
			modify: func(h doctorHostFS) {
				delete(h, "/lib/modules/6.8.0-49-generic/")
				h["/.dockerenv"] = ""
			},
			want: row("", doctorSkip, "not checked, because this container has no /lib/modules. Mount it from the host to check it"),
		},
		"host without modules": {
			modify: func(h doctorHostFS) { delete(h, "/lib/modules/6.8.0-49-generic/") },
			want: row("", doctorWarn, "/lib/modules/6.8.0-49-generic does not exist, so modprobe cannot load modules "+
				"such as nbd and openvswitch. A package upgrade can remove the modules of the running kernel"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			if tc.modify != nil {
				tc.modify(host)
			}

			in := hostInput(t, host, doctorHostTools)
			if tc.os != "" {
				in.system.os = tc.os
			}

			if tc.kernel != "" {
				in.system.kernel = tc.kernel
			}

			got := kernelModulesCheck(in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("kernelModulesCheck() mismatch (-want +got):\n%s", diff)
			}

			wantHint := ""
			if got.Status == doctorWarn {
				wantHint = "reboot the host into the installed kernel"
			}

			if hint := doctorHintOf(got, kernelModulesHint); hint != wantHint {
				t.Errorf("kernelModulesHint() = %q, want %q", hint, wantHint)
			}
		})
	}
}

// nbd fails without qemu-nbd, and warns about each reason minimega disk inject fails: the module, max_part, no free
// device and the devices a failed inject leaves. Each problem gets its fix in the hint.
func TestNBDCheck(t *testing.T) {
	t.Parallel()

	const (
		notLoaded = "the nbd module is not loaded. minimega loads it with modprobe nbd max_part=10 only when it runs " +
			"as root (see the kernel-modules row)"
		noPartitions = ", so the kernel makes no partition devices, such as /dev/nbd0p1, and minimega disk inject " +
			"fails (no partitions found on image)"
		reload = "load the module again as root while no nbd device is connected: " +
			"modprobe -r nbd && modprobe nbd max_part=10"
	)

	tests := map[string]struct {
		modify   func(doctorHostFS)
		os       string
		found    []string
		want     doctorCheck
		wantHint string
	}{
		"free devices": {want: row("", doctorPass, "2 of 2 nbd devices free, max_part=10")},
		"not linux": {
			os: "darwin", want: row("", doctorSkip, "not checked, because minimega uses nbd devices only on Linux"),
		},
		"no qemu-nbd": {
			found: []string{"ovs-vsctl"},
			want: row("", doctorFail, "qemu-nbd is not a program on PATH, so minimega disk inject fails "+
				"(injects at an experiment start and phenix image inject-miniexe)"),
			wantHint: "install qemu-nbd (the qemu-utils package on Debian and Ubuntu)",
		},
		"module not loaded": {
			modify: func(h doctorHostFS) {
				delete(h, "/sys/module/nbd/parameters/max_part")
				delete(h, "/dev/nbd0")
				delete(h, "/dev/nbd1")
				h["/proc/modules"] = "openvswitch 200704 1 - Live 0x0\n"
			},
			want:     row("", doctorWarn, notLoaded),
			wantHint: "load the module as root with modprobe nbd max_part=10",
		},
		"no qemu-nbd and no module": {
			found: []string{},
			modify: func(h doctorHostFS) {
				delete(h, "/sys/module/nbd/parameters/max_part")
				h["/proc/modules"] = ""
			},
			want: row("", doctorFail, "qemu-nbd is not a program on PATH, so minimega disk inject fails "+
				"(injects at an experiment start and phenix image inject-miniexe). "+notLoaded),
			wantHint: "install qemu-nbd (the qemu-utils package on Debian and Ubuntu). " +
				"Load the module as root with modprobe nbd max_part=10",
		},
		"part of the kernel": {
			modify: func(h doctorHostFS) { h["/proc/modules"] = "openvswitch 200704 1 - Live 0x0\n" },
			want: row("", doctorWarn, "nbd is part of the kernel, not a module, so lsmod does not show it and "+
				"minimega disk inject fails (add module 'nbd')"),
			wantHint: "use a kernel that has nbd as a module, because minimega looks for it with lsmod",
		},
		"max_part 0": {
			modify:   func(h doctorHostFS) { h["/sys/module/nbd/parameters/max_part"] = "0\n" },
			want:     row("", doctorWarn, "the nbd module has max_part=0"+noPartitions),
			wantHint: reload,
		},
		"no max_part": {
			modify: func(h doctorHostFS) {
				delete(h, "/sys/module/nbd/parameters/max_part")
				h["/sys/module/nbd/"] = ""
			},
			want:     row("", doctorWarn, "the nbd module has no max_part parameter"+noPartitions),
			wantHint: reload,
		},
		"every device in use": {
			modify: func(h doctorHostFS) {
				h["/sys/block/nbd0/pid"] = "42\n"
				h["/sys/block/nbd1/pid"] = "42\n"
				h["/proc/42/cmdline"] = "nbd-client\x00server\x00/dev/nbd0\x00"
			},
			want: row("", doctorWarn, "no free nbd device: each of the 2 devices has a pid or a lock file, so "+
				"minimega disk inject fails (no available nbds found)"),
			wantHint: "disconnect a device that no inject uses with qemu-nbd -d /dev/nbdN",
		},
		"devices a failed inject leaves": {
			modify: func(h doctorHostFS) {
				h["/dev/nbd2"], h["/dev/nbd2p1"], h["/dev/nbd3"] = "", "", ""
				h["/var/lock/qemu-nbd-nbd0"] = ""
				h["/sys/block/nbd1/pid"] = "77\n"
				h["/sys/block/nbd2/pid"] = "42\n"
				h["/proc/42/cmdline"] = "qemu-nbd\x00-c\x00/dev/nbd2\x00/phenix/images/a b.qc2\x00"
				h["/proc/mounts"] = "proc /proc proc rw 0 0\n/dev/nbd2p1 /tmp/minimega/dstImg123\\040x ext4 rw 0 0\n"
			},
			want: row("", doctorWarn, "devices that a failed inject can leave, if no inject runs now: "+
				"nbd0 has the lock file /var/lock/qemu-nbd-nbd0 but no connection, "+
				"nbd1 belongs to pid 77, which is not running, "+
				"nbd2 holds /phenix/images/a b.qc2 (qemu-nbd pid 42), mounted at /tmp/minimega/dstImg123 x"),
			wantHint: "remove the lock file of a device that no qemu-nbd uses. " +
				"Disconnect the device with qemu-nbd -d /dev/nbdN. " +
				"When no inject runs, unmount the partition with umount <mount point>, then disconnect the device " +
				"with qemu-nbd -d /dev/nbdN",
		},
		"qemu-nbd with more arguments": {
			modify: func(h doctorHostFS) {
				h["/sys/block/nbd1/pid"] = "42\n"
				h["/proc/42/cmdline"] = "qemu-nbd\x00-c\x00/dev/nbd1\x00disk.qc2\x00--object\x00secret,id=s,data=hunter2\x00"
			},
			want: row("", doctorWarn, "devices that a failed inject can leave, if no inject runs now: "+
				"nbd1 holds an image (qemu-nbd pid 42)"),
			wantHint: "when no inject runs, unmount the partition with umount <mount point>, then disconnect the device " +
				"with qemu-nbd -d /dev/nbdN",
		},
		"pid of another process namespace": {
			modify: func(h doctorHostFS) {
				h["/.dockerenv"] = ""
				h["/sys/block/nbd1/pid"] = "77\n"
			},
			want: row("", doctorWarn, "devices that a failed inject can leave, if no inject runs now: "+
				"nbd1 belongs to pid 77, which is not running in this container"),
			wantHint: "disconnect the device with qemu-nbd -d /dev/nbdN",
		},
		"container without devices": {
			modify: func(h doctorHostFS) {
				h["/.dockerenv"] = ""
				delete(h, "/dev/nbd0")
				delete(h, "/dev/nbd1")
			},
			want:     row("", doctorWarn, "this container has no /dev/nbd* device, so minimega disk inject in this container fails"),
			wantHint: "run the container privileged with /dev from the host (docker run --privileged -v /dev:/dev)",
		},
		"host without devices": {
			modify: func(h doctorHostFS) {
				delete(h, "/dev/nbd0")
				delete(h, "/dev/nbd1")
			},
			want:     row("", doctorWarn, "the nbd module is loaded, but /dev has no nbd device"),
			wantHint: "load the module again as root: modprobe -r nbd && modprobe nbd max_part=10",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			if tc.modify != nil {
				tc.modify(host)
			}

			found := doctorHostTools
			if tc.found != nil {
				found = tc.found
			}

			in := hostInput(t, host, found)
			if tc.os != "" {
				in.system.os = tc.os
			}

			got := nbdCheck(in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("nbdCheck() mismatch (-want +got):\n%s", diff)
			}

			if hint := doctorHintOf(got, nbdHint); hint != tc.wantHint {
				t.Errorf("nbdHint() = %q, want %q", hint, tc.wantHint)
			}
		})
	}
}

// openvswitch fails without ovs-vsctl or an answer of ovsdb-server, warns when only this user cannot connect, when
// ovs-vswitchd does not run or the module is not loaded, and reports the version and the experiment bridge.
func TestOpenvswitchCheck(t *testing.T) {
	t.Parallel()

	const show = "ovs-vsctl --timeout=1 show"

	noBridge := strings.ReplaceAll(doctorOVSShow, "phenix", "br0")

	tests := map[string]struct {
		modify   func(doctorHostFS)
		os       string
		found    []string
		mode     string
		commands map[string]doctorCommand
		command  func(context.Context, string, ...string) ([]byte, error)
		want     doctorCheck
		wantHint string
	}{
		"bridge exists": {want: row("", doctorPass, "Open vSwitch 3.3.0, bridge phenix exists")},
		"no bridge yet": {
			commands: map[string]doctorCommand{show: {out: noBridge}},
			want: row("", doctorPass, "Open vSwitch 3.3.0, no bridge phenix yet. minimega makes it when it "+
				"launches the first VM on it"),
		},
		"bridge-mode auto": {
			mode: "auto",
			want: row("", doctorPass, "Open vSwitch 3.3.0, bridge-mode auto, so each experiment uses a bridge with its "+
				"own name (1 bridges now)"),
		},
		"no version": {
			commands: map[string]doctorCommand{show: {out: "0b9d\n    Bridge \"phenix\"\n"}},
			want:     row("", doctorPass, "Open vSwitch (version not known), bridge phenix exists"),
		},
		"not linux": {os: "darwin", want: row("", doctorSkip, "not checked, because minimega runs only on Linux")},
		"no ovs-vsctl": {
			found:    []string{},
			want:     row("", doctorFail, "ovs-vsctl is not a program on PATH, so minimega cannot make bridges and taps for VMs"),
			wantHint: "install Open vSwitch (the openvswitch-switch package on Debian and Ubuntu)",
		},
		"no ovs-vsctl in a container": {
			found:  []string{},
			modify: func(h doctorHostFS) { h["/.dockerenv"] = "" },
			want: row("", doctorSkip, "not checked, because ovs-vsctl is not on PATH in this container. minimega runs "+
				"ovs-vsctl where it runs, such as in its own container"),
		},
		"permission denied": {
			commands: map[string]doctorCommand{show: {
				out: "ovs-vsctl: unix:/var/run/openvswitch/db.sock: database connection failed (Permission denied)\n",
				err: errors.New("exit status 1"),
			}},
			want: row("", doctorWarn, "this user cannot connect to ovsdb-server (Permission denied). minimega runs "+
				"ovs-vsctl as root"),
			wantHint: "run phenix doctor as root, as minimega runs",
		},
		"no ovsdb-server": {
			commands: map[string]doctorCommand{show: {
				out: "ovs-vsctl: unix:/var/run/openvswitch/db.sock: database connection failed (No such file or directory)\n",
				err: errors.New("exit status 1"),
			}},
			want: row("", doctorFail, "ovsdb-server does not answer ovs-vsctl show: ovs-vsctl: "+
				"unix:/var/run/openvswitch/db.sock: database connection failed (No such file or directory)"),
			wantHint: "start Open vSwitch (systemctl start openvswitch-switch on Debian and Ubuntu), or increase --timeout",
		},
		"no output": {
			commands: map[string]doctorCommand{show: {err: errors.New("signal: killed")}},
			want:     row("", doctorFail, "ovsdb-server does not answer ovs-vsctl show: signal: killed"),
			wantHint: "start Open vSwitch (systemctl start openvswitch-switch on Debian and Ubuntu), or increase --timeout",
		},
		"no answer in time": {
			command: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
				<-ctx.Done()

				return nil, ctx.Err()
			},
			want:     row("", doctorFail, "ovsdb-server did not answer ovs-vsctl show within 1s"),
			wantHint: "start Open vSwitch (systemctl start openvswitch-switch on Debian and Ubuntu), or increase --timeout",
		},
		"ovs-vswitchd stopped": {
			modify: func(h doctorHostFS) { delete(h, "/proc/42/") },
			want: row("", doctorWarn, "ovsdb-server answers, but ovs-vswitchd does not run, so the bridges pass no "+
				"traffic. Open vSwitch 3.3.0, bridge phenix exists"),
			wantHint: "restart Open vSwitch (systemctl restart openvswitch-switch on Debian and Ubuntu)",
		},
		"module not loaded": {
			modify: func(h doctorHostFS) {
				delete(h, "/sys/module/openvswitch/")
				delete(h, "/var/run/openvswitch/ovs-vswitchd.pid")
			},
			want: row("", doctorWarn, "ovsdb-server answers, but ovs-vswitchd does not run, so the bridges pass no "+
				"traffic. the openvswitch kernel module is not loaded, so ovs-vswitchd cannot make kernel bridges "+
				"(see the kernel-modules row). Open vSwitch 3.3.0, bridge phenix exists"),
			wantHint: "restart Open vSwitch (systemctl restart openvswitch-switch on Debian and Ubuntu). " +
				"Load the module as root with modprobe openvswitch",
		},
		"no ovsdb-server and no module": {
			modify: func(h doctorHostFS) { delete(h, "/sys/module/openvswitch/") },
			commands: map[string]doctorCommand{show: {
				out: "ovs-vsctl: database connection failed (No such file or directory)\n", err: errors.New("exit status 1"),
			}},
			want: row("", doctorFail, "ovsdb-server does not answer ovs-vsctl show: ovs-vsctl: database connection "+
				"failed (No such file or directory). the openvswitch kernel module is not loaded, so ovs-vswitchd "+
				"cannot make kernel bridges (see the kernel-modules row)"),
			wantHint: "start Open vSwitch (systemctl start openvswitch-switch on Debian and Ubuntu), or increase " +
				"--timeout. Load the module as root with modprobe openvswitch",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			if tc.modify != nil {
				tc.modify(host)
			}

			found := doctorHostTools
			if tc.found != nil {
				found = tc.found
			}

			in := hostInput(t, host, found)
			if tc.os != "" {
				in.system.os = tc.os
			}

			if tc.mode != "" {
				in.settings["bridge-mode"] = resolvedSetting{value: tc.mode, text: tc.mode, source: sourceFlag}
			}

			commands := map[string]doctorCommand{show: {out: doctorOVSShow}}
			maps.Copy(commands, tc.commands)

			in.command = doctorCommands(commands)
			if tc.command != nil {
				in.command = tc.command
			}

			got := openvswitchCheck(t.Context(), in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("openvswitchCheck() mismatch (-want +got):\n%s", diff)
			}

			if hint := doctorHintOf(got, openvswitchHint); hint != tc.wantHint {
				t.Errorf("openvswitchHint() = %q, want %q", hint, tc.wantHint)
			}
		})
	}
}

// imageConfig is a stored image config of name with release.
func imageConfig(name, release string) store.Config {
	return store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Image",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     map[string]any{"release": release},
		Status:   nil, IfRevision: 0,
	}
}

// image-build warns, one row each, about missing programs, a user without CAP_SYS_ADMIN, no free loop device, and the
// debootstrap script and keyring of each stored release.
func TestImageBuildChecks(t *testing.T) {
	t.Parallel()

	const (
		gutsy = "case \"$ARCH\" in\nesac\n\tkeyring /usr/share/keyrings/ubuntu-archive-keyring.gpg\nelse\n" +
			"\tkeyring /usr/share/keyrings/ubuntu-archive-removed-keys.gpg\n"
		sid = "mirror_style release\nkeyring /usr/share/keyrings/debian-archive-keyring.gpg\n"
	)

	scripts := func(h doctorHostFS) {
		h["/usr/share/debootstrap/scripts/noble"] = gutsy
		h["/usr/share/debootstrap/scripts/bookworm"] = sid
		h["/usr/share/keyrings/ubuntu-archive-keyring.gpg"] = ""
	}

	passRows := []doctorCheck{
		row(imageBuildToolsRow, doctorPass, "debootstrap, kpartx, losetup, qemu-img are on PATH"),
		row(imageBuildRootRow, doctorPass, "uid 1000 with CAP_SYS_ADMIN"),
		row(imageBuildLoopRow, doctorPass, "1 of 1 loop devices free"),
		row(imageBuildKeyringsRow, doctorPass, "no image configs, because the store file does not exist"),
	}

	with := func(i int, c doctorCheck) []doctorCheck {
		rows := slices.Clone(passRows)
		rows[i] = c

		return rows
	}

	tests := map[string]struct {
		modify    func(doctorHostFS)
		os        string
		found     []string
		root      bool
		store     bool
		configs   store.Configs
		configErr error
		want      []doctorCheck
		wantHints []string
	}{
		"all found": {want: passRows},
		"not linux": {
			os:   "darwin",
			want: []doctorCheck{row("", doctorSkip, "not checked, because phenix image build runs only on Linux")},
		},
		"programs missing": {
			found: []string{"debootstrap", "qemu-img"},
			want: with(0, row(imageBuildToolsRow, doctorWarn,
				"not on PATH: kpartx (package kpartx), losetup (package util-linux), so phenix image build fails")),
			wantHints: []string{"install the packages that the row names"},
		},
		"not root": {
			modify: func(h doctorHostFS) { h["/proc/self/status"] = "CapEff:\t0000000000000000\n" },
			want: with(1, row(imageBuildRootRow, doctorWarn,
				"phenix runs as uid 1000, not root, so vmdb2 cannot use loop devices, mount or chroot")),
			wantHints: []string{"run phenix image build as root, or in a privileged container"},
		},
		"root in a container that is not privileged": {
			root:   true,
			modify: func(h doctorHostFS) { h["/proc/self/status"] = "CapEff:\t00000000a80425fb\n" },
			want: with(1, row(imageBuildRootRow, doctorWarn, "root does not have CAP_SYS_ADMIN here, such as in a "+
				"container that is not privileged, so vmdb2 cannot use loop devices or mount")),
			wantHints: []string{"run phenix image build as root, or in a privileged container"},
		},
		"no capabilities to read": {
			root:   true,
			modify: func(h doctorHostFS) { delete(h, "/proc/self/status") },
			want:   with(1, row(imageBuildRootRow, doctorPass, "uid 0 with CAP_SYS_ADMIN")),
		},
		"every loop device in use, with loop-control": {
			modify: func(h doctorHostFS) { h["/sys/block/loop0/loop/backing_file"] = "/tmp/a.img\n" },
			want: with(2, row(imageBuildLoopRow, doctorPass,
				"none of 1 loop devices free, and the kernel makes one through /dev/loop-control")),
		},
		"no loop device": {
			modify: func(h doctorHostFS) {
				h["/sys/block/loop0/loop/backing_file"] = "/tmp/a.img\n"
				delete(h, "/dev/loop-control")
			},
			want: with(2, row(imageBuildLoopRow, doctorWarn,
				"no free /dev/loop* device and no /dev/loop-control, so vmdb2 cannot attach the image")),
			wantHints: []string{
				"load the loop module as root (modprobe loop), or run the container privileged with /dev from the host",
			},
		},
		"no loop device in a container": {
			modify: func(h doctorHostFS) {
				delete(h, "/dev/loop0")
				delete(h, "/dev/loop-control")
				h["/.dockerenv"] = ""
			},
			want: with(2, row(imageBuildLoopRow, doctorWarn, "no free /dev/loop* device and no /dev/loop-control in "+
				"this container, so vmdb2 cannot attach the image")),
			wantHints: []string{
				"load the loop module as root (modprobe loop), or run the container privileged with /dev from the host",
			},
		},
		"keyrings found": {
			modify: scripts, store: true,
			configs: store.Configs{imageConfig("ubuntu", "noble"), imageConfig("ubuntu-gui", "noble"), imageConfig("old", "")},
			want:    with(3, row(imageBuildKeyringsRow, doctorPass, "debootstrap scripts and keyrings found for noble")),
		},
		"no configs with a release": {
			modify: scripts, store: true, configs: store.Configs{},
			want: with(3, row(imageBuildKeyringsRow, doctorPass, "no image configs with a release in the store")),
		},
		"script or keyring missing": {
			modify: scripts, store: true,
			configs: store.Configs{
				imageConfig("kali", "kali"), imageConfig("deb", "bookworm"), imageConfig("ubuntu", "noble"),
				imageConfig("bad", "../../etc/shadow"),
			},
			want: with(3, row(imageBuildKeyringsRow, doctorWarn, `the release "../../etc/shadow" of bad is not a `+
				"debootstrap script name. /usr/share/keyrings/debian-archive-keyring.gpg for bookworm (deb) does not "+
				"exist, so debootstrap cannot check the Release signature. debootstrap has no script for "+
				"kali-rolling (kali)")),
			wantHints: []string{
				"install a later debootstrap, or add a script for the release to /usr/share/debootstrap/scripts " +
					"(for Kali, see the phenix-image skill). Install the package that holds the keyring: " +
					"debian-archive-keyring, ubuntu-keyring or kali-archive-keyring. Set the release of the image " +
					"config to a debootstrap release name",
			},
		},
		"no debootstrap scripts": {
			store: true, configs: store.Configs{imageConfig("ubuntu", "noble")},
			want: with(3, row(imageBuildKeyringsRow, doctorSkip, "not checked, because /usr/share/debootstrap/scripts "+
				"does not exist (the image-build tools row checks debootstrap)")),
		},
		"store locked": {
			modify: scripts, store: true, configErr: bbolt.ErrTimeout,
			want: with(3, row(imageBuildKeyringsRow, doctorSkip,
				"not checked, because another process held the lock of STORE for 1s")),
		},
		"store unreadable": {
			modify: scripts, store: true, configErr: &fs.PathError{Op: "open", Path: "STORE", Err: fs.ErrPermission},
			want: with(3, row(imageBuildKeyringsRow, doctorSkip, "not checked, because this user cannot read STORE")),
		},
		"store broken": {
			modify: scripts, store: true, configErr: errors.New("invalid database"),
			want: with(3, row(imageBuildKeyringsRow, doctorSkip,
				"not checked, because cannot read STORE: invalid database")),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			if tc.modify != nil {
				tc.modify(host)
			}

			found := doctorHostTools
			if tc.found != nil {
				found = tc.found
			}

			in := hostInput(t, host, found)
			if tc.root {
				in.uid = 0
			}

			if tc.os != "" {
				in.system.os = tc.os
			}

			path := strings.TrimPrefix(in.text("store.endpoint"), "bolt://")
			if tc.store {
				if err := os.WriteFile(path, []byte("store"), 0o600); err != nil {
					t.Fatalf("os.WriteFile(%s) error = %v", path, err)
				}
			}

			in.storeConfigs = func(_ context.Context, p, kind string, timeout time.Duration) (store.Configs, error) {
				if p != path || kind != "Image" || timeout != in.timeout {
					return nil, fmt.Errorf("storeConfigs(%s, %s, %s)", p, kind, timeout)
				}

				return tc.configs, tc.configErr
			}

			got := imageBuildChecks(t.Context(), in)
			for i := range got {
				got[i].Detail = strings.ReplaceAll(got[i].Detail, path, "STORE")
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("imageBuildChecks() mismatch (-want +got):\n%s", diff)
			}

			var hints []string

			for _, c := range got {
				if hint := doctorHintOf(c, imageBuildHint); hint != "" {
					hints = append(hints, hint)
				}
			}

			if diff := cmp.Diff(tc.wantHints, hints); diff != "" {
				t.Errorf("imageBuildHint() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// readStoreConfigs reads the configs of one kind from a BoltDB file read-only. While another open holds the lock, it
// gives up with bbolt.ErrTimeout, also for a timeout of 0, or with the error of a context that ends.
func TestReadStoreConfigs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "store.bdb")

	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bbolt.Open() error = %v", err)
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		for _, c := range []store.Config{imageConfig("ubuntu", "noble"), imageConfig("kali", "kali")} {
			bucket, err := tx.CreateBucketIfNotExists([]byte(c.Kind))
			if err != nil {
				return err
			}

			data, err := json.Marshal(c)
			if err != nil {
				return err
			}

			if err := bucket.Put([]byte(c.Metadata.Name), data); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("db.Update() error = %v", err)
	}

	locked, err := readStoreConfigs(t.Context(), path, "Image", 50*time.Millisecond)
	if !errors.Is(err, bbolt.ErrTimeout) {
		t.Errorf("readStoreConfigs() while locked = %v, %v, want error %v", locked, err, bbolt.ErrTimeout)
	}

	if _, err := readStoreConfigs(t.Context(), path, "Image", 0); !errors.Is(err, bbolt.ErrTimeout) {
		t.Errorf("readStoreConfigs() with timeout 0 while locked error = %v, want %v", err, bbolt.ErrTimeout)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := readStoreConfigs(ctx, path, "Image", time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("readStoreConfigs() with an ending context while locked error = %v, want %v", err,
			context.DeadlineExceeded)
	}

	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("readStoreConfigs() with an ending context took %s, want less than the timeout", elapsed)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	configs, err := readStoreConfigs(t.Context(), path, "Image", time.Second)
	if err != nil {
		t.Fatalf("readStoreConfigs() error = %v", err)
	}

	names := make([]string, 0, len(configs))
	for _, c := range configs {
		names = append(names, c.Metadata.Name+"="+fmt.Sprint(c.Spec["release"]))
	}

	if diff := cmp.Diff([]string{"kali=kali", "ubuntu=noble"}, names); diff != "" {
		t.Errorf("readStoreConfigs() mismatch (-want +got):\n%s", diff)
	}

	none, err := readStoreConfigs(t.Context(), path, "Topology", time.Second)
	if err != nil || len(none) != 0 {
		t.Errorf("readStoreConfigs(Topology) = %v, %v, want no configs", none, err)
	}
}

// escapeMount escapes s as /proc/mounts does: space, tab, newline and backslash as octal.
func escapeMount(s string) string {
	var b strings.Builder

	for i := range len(s) {
		switch c := s[i]; c {
		case ' ', '\t', '\n', '\\':
			fmt.Fprintf(&b, `\%03o`, c)
		default:
			b.WriteByte(c)
		}
	}

	return b.String()
}

// The parsers of host files read back what they are given, and never panic on other input.
func TestHostParsersProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "s")
		if got := unescapeMount(escapeMount(s)); got != s {
			t.Fatalf("unescapeMount(escapeMount(%q)) = %q", s, got)
		}

		_ = unescapeMount(s)

		name := rapid.StringMatching(`[A-Za-z0-9_.-]{1,15}`)
		bridges := rapid.SliceOf(name).Draw(t, "bridges")
		version := rapid.StringMatching(`[0-9]{1,2}\.[0-9]{1,2}\.[0-9]{1,2}`).Draw(t, "version")

		var show strings.Builder

		show.WriteString("0b9d\n")

		for _, b := range bridges {
			fmt.Fprintf(&show, "    Bridge %q\n        Port %s\n", b, b)
		}

		fmt.Fprintf(&show, "    ovs_version: %q\n", version)

		gotVersion, gotBridges := parseOVSShow(show.String())
		if gotVersion != version || !slices.Equal(gotBridges, bridges) && len(bridges) > 0 {
			t.Fatalf("parseOVSShow() = %q, %q, want %q, %q", gotVersion, gotBridges, version, bridges)
		}

		keyring := scriptKeyring(s)
		if keyring != "" && (!filepath.IsAbs(keyring) || !strings.Contains(s, keyring)) {
			t.Fatalf("scriptKeyring(%q) = %q, not an absolute path in the script", s, keyring)
		}
	})
}
