package image_test

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/image"
	"phenix/tmpl"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// injected is a file of a disk inject as the fake minimega saw it.
type injected struct {
	Src     string // with $TMP for the temporary directory of the inject
	Content string
	Link    string // the target, for a symbolic link
	Mode    fs.FileMode
}

// diskInject is one disk inject command as the fake minimega saw it.
type diskInject struct {
	Base  string // with $TMP for the temporary directory of the inject
	Image string
	Files map[string]injected // by destination
}

// tmpDir matches the temporary directory of an inject.
func tmpDir() *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(os.TempDir()+"/phenix/") + `[^/"]+`)
}

// useDiskInject registers the minimega disk inject pattern with minicli until
// the test ends.
func useDiskInject(tb testing.TB) {
	tb.Helper()

	minicli.Reset()
	tb.Cleanup(minicli.Reset)
	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"disk <inject,> <image> files <files like /path/to/src:/path/to/dst>..."},
	})
}

// fakeInjects starts a fake minimega that answers each disk inject with
// answer(n), n counting from 0. The returned function lists the injects and
// the temporary directories they used.
func fakeInjects(t *testing.T, answer func(n int) []*minicli.Response) func() ([]diskInject, []string) {
	t.Helper()

	useDiskInject(t)

	var (
		mu      sync.Mutex
		injects []diskInject
		dirs    []string
	)

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		mu.Lock()
		defer mu.Unlock()

		got := diskInject{Base: tmpDir().ReplaceAllString(cmd.Base, "$$TMP"), Files: map[string]injected{}}
		dirs = append(dirs, tmpDir().FindAllString(cmd.Base, 1)...)

		c, err := minicli.Compile(cmd.Base)
		if err != nil {
			t.Errorf("minicli.Compile(%q) error = %v", cmd.Base, err)

			return nil
		}

		got.Image = c.StringArgs["image"]

		for _, pair := range c.ListArgs["files"] {
			src, dst, _ := strings.Cut(pair, ":")
			f := injected{Src: tmpDir().ReplaceAllString(src, "$$TMP")}

			if info, err := os.Lstat(src); err == nil {
				f.Mode = info.Mode()
			}

			if f.Mode&fs.ModeSymlink != 0 {
				// the bits of a link differ between systems and are not used
				f.Mode = fs.ModeSymlink
				f.Link, _ = os.Readlink(src)
			} else if b, err := os.ReadFile(src); err == nil {
				f.Content = string(b)
			}

			got.Files[dst] = f
		}

		injects = append(injects, got)

		return answer(len(injects) - 1)
	})

	return func() ([]diskInject, []string) {
		mu.Lock()
		defer mu.Unlock()

		return injects, dirs
	}
}

// asset returns the embedded template file name.
func asset(t *testing.T, name string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/"+name[:strings.LastIndex(name, "/")], 0o750); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}

	if err := tmpl.RestoreAsset(dir, name); err != nil {
		t.Fatalf("tmpl.RestoreAsset(%s) error = %v", name, err)
	}

	b, err := os.ReadFile(dir + "/" + name)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", name, err)
	}

	return string(b)
}

// exeFile writes a fake executable named name and returns its path.
func exeFile(t *testing.T, dir, name string) (string, injected) {
	t.Helper()

	p := dir + "/" + name
	if err := os.WriteFile(p, []byte("exe "+name), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", p, err)
	}

	return p, injected{Src: p, Content: "exe " + name, Mode: 0o600}
}

// InjectMiniExe injects the executable and the boot files of its init system
// with one minimega disk inject, into partition 1 when the disk names none.
// A minirouter inject injects miniccc first.
//
//nolint:paralleltest // uses the fake minimega
func TestInjectMiniExe(t *testing.T) {
	dir := t.TempDir()
	miniccc, minicccFile := exeFile(t, dir, "miniccc")
	minirouter, minirouterFile := exeFile(t, dir, "minirouter")
	protonuke, protonukeFile := exeFile(t, dir, "protonuke")
	minicccExe, minicccExeFile := exeFile(t, dir, "miniccc.exe")
	protonukeExe, protonukeExeFile := exeFile(t, dir, "protonuke.exe")
	upperExe, upperExeFile := exeFile(t, t.TempDir(), "MiniCCC.EXE") // own folder: APFS ignores case

	systemd := func(base, exe string, exeFile injected) diskInject {
		return diskInject{
			Base: `disk inject "disk.qc2:1" files "$TMP/` + base + `/` + base + `.service":"/etc/systemd/system/` + base +
				`.service" "$TMP/` + base + `/symlinks/` + base + `.service":"/etc/systemd/system/multi-user.target.wants/` +
				base + `.service" "` + exe + `":"/usr/local/bin/` + base + `"`,
			Image: "disk.qc2:1",
			Files: map[string]injected{
				"/etc/systemd/system/" + base + ".service": {
					Src: "$TMP/" + base + "/" + base + ".service", Content: asset(t, base+"/"+base+".service"), Mode: 0o600,
				},
				"/etc/systemd/system/multi-user.target.wants/" + base + ".service": {
					Src: "$TMP/" + base + "/symlinks/" + base + ".service", Link: "../" + base + ".service",
					Mode: fs.ModeSymlink,
				},
				"/usr/local/bin/" + base: exeFile,
			},
		}
	}

	tests := []struct {
		name, exe, disk, svc string
		want                 []diskInject
	}{
		{name: "linux systemd", exe: miniccc, disk: "disk.qc2", svc: "systemd", want: []diskInject{
			systemd("miniccc", miniccc, minicccFile),
		}},
		{name: "linux sysinitv on partition 2", exe: protonuke, disk: "disk.qc2:2", svc: "sysinitv", want: []diskInject{{
			Base: `disk inject "disk.qc2:2" files "$TMP/protonuke/protonuke.init":"/etc/init.d/protonuke" ` +
				`"$TMP/protonuke/symlinks/S99-protonuke":"/etc/rc5.d/S99-protonuke" "` + protonuke + `":"/usr/local/bin/protonuke"`,
			Image: "disk.qc2:2",
			Files: map[string]injected{
				"/etc/init.d/protonuke": {
					Src: "$TMP/protonuke/protonuke.init", Content: asset(t, "protonuke/protonuke.init"), Mode: 0o750,
				},
				"/etc/rc5.d/S99-protonuke": {
					Src: "$TMP/protonuke/symlinks/S99-protonuke", Link: "../init.d/protonuke", Mode: fs.ModeSymlink,
				},
				"/usr/local/bin/protonuke": protonukeFile,
			},
		}}},
		{name: "minirouter injects miniccc first", exe: minirouter, disk: "disk.qc2", svc: "systemd", want: []diskInject{
			systemd("miniccc", miniccc, minicccFile),
			systemd("minirouter", minirouter, minirouterFile),
		}},
		{name: "windows miniccc startup", exe: minicccExe, disk: "win.qc2", svc: "startup", want: []diskInject{{
			Base: `disk inject "win.qc2:1" files "$TMP/miniccc/miniccc-scheduler.cmd":` +
				`"/ProgramData/Microsoft/Windows/Start Menu/Programs/Startup/miniccc-scheduler.cmd" "` +
				minicccExe + `":"/minimega/miniccc.exe"`,
			Image: "win.qc2:1",
			Files: map[string]injected{
				"/ProgramData/Microsoft/Windows/Start Menu/Programs/Startup/miniccc-scheduler.cmd": {
					Src: "$TMP/miniccc/miniccc-scheduler.cmd", Content: asset(t, "miniccc/miniccc-scheduler.cmd"), Mode: 0o600,
				},
				"/minimega/miniccc.exe": minicccExeFile,
			},
		}}},
		{name: "windows miniccc without startup", exe: minicccExe, disk: "win.qc2", svc: "systemd", want: []diskInject{{
			Base:  `disk inject "win.qc2:1" files "` + minicccExe + `":"/minimega/miniccc.exe"`,
			Image: "win.qc2:1",
			Files: map[string]injected{"/minimega/miniccc.exe": minicccExeFile},
		}}},
		{name: "windows name in other letter case", exe: upperExe, disk: "win.qc2", svc: "systemd", want: []diskInject{{
			Base:  `disk inject "win.qc2:1" files "` + upperExe + `":"/minimega/miniccc.exe"`,
			Image: "win.qc2:1",
			Files: map[string]injected{"/minimega/miniccc.exe": upperExeFile},
		}}},
		{name: "windows protonuke has no startup file", exe: protonukeExe, disk: "win.qc2", svc: "startup", want: []diskInject{{
			Base:  `disk inject "win.qc2:1" files "` + protonukeExe + `":"/minimega/protonuke.exe"`,
			Image: "win.qc2:1",
			Files: map[string]injected{"/minimega/protonuke.exe": protonukeExeFile},
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := fakeInjects(t, func(int) []*minicli.Response { return nil })

			if err := image.InjectMiniExe(tt.exe, tt.disk, tt.svc); err != nil {
				t.Fatalf("image.InjectMiniExe(%s, %s, %s) error = %v", tt.exe, tt.disk, tt.svc, err)
			}

			got, dirs := received()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("image.InjectMiniExe(%s, %s, %s) injects mismatch (-want +got):\n%s", tt.exe, tt.disk, tt.svc, diff)
			}

			for _, d := range dirs {
				if _, err := os.Stat(d); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("temporary directory %s after the inject: os.Stat error = %v, want %v", d, err, fs.ErrNotExist)
				}
			}
		})
	}
}

// InjectMiniExe refuses an executable it does not support, an unknown Linux
// init system and a path minimega cannot take, and sends no command.
//
//nolint:paralleltest // uses the fake minimega
func TestInjectMiniExeRefuses(t *testing.T) {
	tests := []struct {
		name, exe, disk, svc string
		wantErr              error
	}{
		{name: "windows exe", exe: "/bin/putty.exe", disk: "d.qc2", svc: "startup"},
		{name: "linux exe", exe: "/bin/bash", disk: "d.qc2", svc: "systemd"},
		{name: "init system", exe: "/bin/miniccc", disk: "d.qc2", svc: "upstart"},
		{name: "quote in exe", exe: `/b"in/miniccc`, disk: "d.qc2", svc: "systemd", wantErr: mm.ErrUnsafeArg},
		{name: "dollar in exe", exe: "/$HOME/miniccc", disk: "d.qc2", svc: "systemd", wantErr: mm.ErrUnsafeArg},
		{name: "colon in exe", exe: "/a:b/miniccc", disk: "d.qc2", svc: "systemd", wantErr: mm.ErrUnsafeArg},
		{name: "backslash in disk", exe: "/bin/miniccc", disk: `d\.qc2`, svc: "systemd", wantErr: mm.ErrUnsafeArg},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := fakeInjects(t, func(int) []*minicli.Response { return nil })

			err := image.InjectMiniExe(tt.exe, tt.disk, tt.svc)
			if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Errorf("image.InjectMiniExe(%s, %s, %s) error = %v, want an error (%v)", tt.exe, tt.disk, tt.svc, err, tt.wantErr)
			}

			if got, _ := received(); len(got) != 0 {
				t.Errorf("image.InjectMiniExe(%s, %s, %s) sent %+v, want no command", tt.exe, tt.disk, tt.svc, got)
			}
		})
	}
}

// InjectMiniExe refuses a temporary directory that minimega cannot take in a
// quoted source path, and sends no command.
//
//nolint:paralleltest // uses the fake minimega and sets TMPDIR
func TestInjectMiniExeRefusesTempDir(t *testing.T) {
	exe, _ := exeFile(t, t.TempDir(), "miniccc")

	for _, sub := range []string{"a:b", "a$b", `a"b`, `a\b`} {
		t.Run(sub, func(t *testing.T) {
			received := fakeInjects(t, func(int) []*minicli.Response { return nil })
			tmpdir := t.TempDir() + "/" + sub
			t.Setenv("TMPDIR", tmpdir)

			if err := image.InjectMiniExe(exe, "d.qc2", "systemd"); !errors.Is(err, mm.ErrUnsafeArg) {
				t.Errorf("image.InjectMiniExe(%s) with TMPDIR %s error = %v, want %v", exe, tmpdir, err, mm.ErrUnsafeArg)
			}

			if got, _ := received(); len(got) != 0 {
				t.Errorf("image.InjectMiniExe(%s) with TMPDIR %s sent %+v, want no command", exe, tmpdir, got)
			}

			if _, err := os.Stat(tmpdir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("TMPDIR %s after the refusal: os.Stat error = %v, want %v", tmpdir, err, fs.ErrNotExist)
			}
		})
	}
}

// InjectMiniExe returns the error minimega gives for an inject. When the
// miniccc inject of a minirouter inject fails, it does not inject minirouter.
//
//nolint:paralleltest // uses the fake minimega
func TestInjectMiniExeMinimegaError(t *testing.T) {
	received := fakeInjects(t, func(int) []*minicli.Response {
		return []*minicli.Response{mmtest.Error("host", "no nbd")}
	})

	exe, _ := exeFile(t, t.TempDir(), "minirouter")
	if err := image.InjectMiniExe(exe, "d.qc2", "systemd"); err == nil {
		t.Errorf("image.InjectMiniExe(%s) error = nil, want the minimega error", exe)
	}

	got, _ := received()
	if len(got) != 1 || !strings.Contains(got[0].Base, "/usr/local/bin/miniccc") {
		t.Errorf("image.InjectMiniExe(%s) sent %+v, want only the miniccc inject", exe, got)
	}
}
