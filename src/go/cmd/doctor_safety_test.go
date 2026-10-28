package cmd

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.etcd.io/bbolt"
	"pgregory.net/rapid"
)

// lookPathDirs finds a program as [exec.LookPath] does, in dirs only: the first regular file of the name with an
// execute bit.
func lookPathDirs(dirs ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, dir := range dirs {
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
				return path, nil
			}
		}

		return "", exec.ErrNotFound
	}
}

// writeProgram writes data to the file name in dir with mode, and returns its path.
func writeProgram(t *testing.T, dir, name, data string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("os.Chmod(%s) error = %v", path, err)
	}

	return path
}

// app-runtimes warns about each app or SCORCH component on PATH that phenix cannot run: one that is not executable,
// and one whose #! line names an interpreter that does not exist, is not executable or has a carriage return after
// it, or runs a program through env that is not on PATH.
func TestAppRuntimesCheck(t *testing.T) {
	t.Parallel()

	bin, other := t.TempDir(), t.TempDir()
	interp := writeProgram(t, other, "notrun", "#!/bin/sh\n", 0o644)

	writeProgram(t, bin, "phenix-app-good", "#!/bin/sh\necho\n", 0o755)
	writeProgram(t, bin, "phenix-app-envok", "#!/usr/bin/env -S sh -e\necho\n", 0o755)
	writeProgram(t, bin, "phenix-app-envunset", "#!/usr/bin/env -S -u PYTHONPATH -C / sh\necho\n", 0o755)
	writeProgram(t, bin, "phenix-app-tab", "#!/bin/sh\t-e\necho\n", 0o755)
	writeProgram(t, bin, "phenix-app-binary", "\x7fELF\x02\x01", 0o755)
	writeProgram(t, bin, "phenix-app-noexec", "#!/bin/sh\n", 0o644)
	writeProgram(t, bin, "phenix-app-gone", "#!/nowhere/python3.11\n", 0o755)
	writeProgram(t, bin, "phenix-app-notrun", "#!"+interp+"\n", 0o755)
	writeProgram(t, bin, "phenix-app-crlf", "#!/bin/sh\r\necho\r\n", 0o755)
	writeProgram(t, bin, "phenix-scorch-component-env", "#!/usr/bin/env PYTHONPATH=/x nosuchprogram -u\n", 0o755)
	writeProgram(t, bin, "phenix-scorch-component-rel", "#! python3\n", 0o755)
	writeProgram(t, other, "phenix-app-good", "not used, since the first one wins\n", 0o644)
	writeProgram(t, bin, "unrelated", "#!/nowhere\n", 0o755)

	in := fakeDoctorInput(newDoctorTestDir(t), nil)
	vars := fakeDoctorEnv(map[string]string{"PATH": strings.Join([]string{bin, other, "/bin", "/usr/bin"}, ":")}, false)
	in.env.lookup = vars.lookup
	in.env.lookPath = lookPathDirs(bin, other, "/bin", "/usr/bin")

	got := appRuntimesCheck(in)
	got.Detail = strings.ReplaceAll(strings.ReplaceAll(got.Detail, bin, "BIN"), other, "OTHER")

	want := row("", doctorWarn, "the #! line of BIN/phenix-app-crlf ends in a carriage return (Windows line ends), "+
		"so the kernel cannot find its interpreter, the #! line of BIN/phenix-app-gone names /nowhere/python3.11, "+
		"which does not exist, BIN/phenix-app-noexec is not executable by this user, so phenix does not find "+
		"phenix-app-noexec, the #! line of BIN/phenix-app-notrun names OTHER/notrun, which this user cannot run, "+
		"the #! line of BIN/phenix-scorch-component-env runs nosuchprogram through env, but nosuchprogram is not "+
		"a program on PATH, and 1 more")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("appRuntimesCheck() mismatch (-want +got):\n%s", diff)
	}

	wantHint := "run chmod +x <path> on the program, or install its interpreter, or install the app package again " +
		"in a working environment. Change Windows line ends to Unix line ends with dos2unix <path>"
	if hint := doctorHintOf(got, appRuntimesHint); hint != wantHint {
		t.Errorf("appRuntimesHint() = %q, want %q", hint, wantHint)
	}

	if problem := in.env.shebangProblem(filepath.Join(bin, "phenix-scorch-component-rel")); problem !=
		"the #! line of "+filepath.Join(bin, "phenix-scorch-component-rel")+" names python3, which is not an absolute path" {
		t.Errorf("shebangProblem() of a relative interpreter = %q", problem)
	}

	for _, name := range []string{"phenix-app-good", "phenix-app-envok", "phenix-app-envunset", "phenix-app-tab", "phenix-app-binary"} {
		if problem := in.env.shebangProblem(filepath.Join(bin, name)); problem != "" {
			t.Errorf("shebangProblem(%s) = %q, want none", name, problem)
		}
	}

	in.env.lookup = fakeDoctorEnv(map[string]string{"PATH": other + ":/bin"}, false).lookup
	in.env.lookPath = lookPathDirs(other, "/bin")

	if got := appRuntimesCheck(in); got.Status != doctorWarn {
		t.Errorf("appRuntimesCheck() with only a file that is not executable = %v, want a warning", got)
	}

	in.env.lookup = fakeDoctorEnv(map[string]string{"PATH": "/bin"}, false).lookup
	if got := appRuntimesCheck(in); got != row("", doctorPass,
		"no phenix-app-* or phenix-scorch-component-* programs on PATH") {
		t.Errorf("appRuntimesCheck() with no apps = %v, want a pass", got)
	}

	good := t.TempDir()
	writeProgram(t, good, "phenix-app-good", "#!/bin/sh\necho\n", 0o755)
	writeProgram(t, good, "phenix-scorch-component-binary", "\x7fELF\x02\x01", 0o755)

	if err := os.Mkdir(filepath.Join(good, "phenix-app-dir"), 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}

	in.env.lookup = fakeDoctorEnv(map[string]string{"PATH": good + "::" + filepath.Join(good, "missing")}, false).lookup
	in.env.lookPath = lookPathDirs(good)

	if diff := cmp.Diff(row("", doctorPass, "this user can run each of the 2 programs, and each interpreter of a #! line exists"),
		appRuntimesCheck(in)); diff != "" {
		t.Errorf("appRuntimesCheck() with good apps mismatch (-want +got):\n%s", diff)
	}
}

// doctorSafetyEnv is an env whose files are the contents in files, and whose existing paths are those in infos;
// access refuses execution of the paths in noExec, and lookPath finds the programs in found.
func doctorSafetyEnv(files map[string]string, infos map[string]os.FileInfo, noExec, found []string) doctorEnv {
	env := fakeDoctorEnv(nil, false)
	env.readAt = func(path string, _ int64, n int) ([]byte, error) {
		data, ok := files[path]
		if !ok {
			return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
		}

		return []byte(data[:min(n, len(data))]), nil
	}
	env.stat = func(path string) (os.FileInfo, error) {
		if info, ok := infos[path]; ok {
			if info == nil {
				return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrPermission}
			}

			return info, nil
		}

		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	}
	env.access = func(path string, _ uint32) error {
		if slices.Contains(noExec, path) {
			return fs.ErrPermission
		}

		return nil
	}
	paths := make(map[string]string, len(found))
	for _, name := range found {
		paths[name] = "/usr/bin/" + name
	}

	env.lookPath = lookPathIn(paths)

	return env
}

// shebangProblem names a #! line without an interpreter, an interpreter it cannot stat or that is a directory,
// and a program that env runs by path; a file it cannot read, or env without a program, has no problem.
func TestShebangProblemCases(t *testing.T) {
	t.Parallel()

	dirInfo, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	fileInfo, err := os.Stat(writeProgram(t, t.TempDir(), "f", "", 0o755))
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	infos := map[string]os.FileInfo{
		"/bin/sh": fileInfo, "/usr/bin/env": fileInfo, "/opt/py": fileInfo, "/bin": dirInfo, "/secret/sh": nil,
	}

	tests := map[string]struct {
		head string // none when "-"
		want string
	}{
		"no interpreter": {head: "#!\n", want: "the #! line of /app names no interpreter"},
		"only spaces":    {head: "#!  \t\n", want: "the #! line of /app names no interpreter"},
		"interpreter not read": {
			head: "#!/secret/sh\n", want: "the #! line of /app names /secret/sh, which cannot be read (permission denied)",
		},
		"interpreter directory": {head: "#!/bin -e\n", want: "the #! line of /app names /bin, which is a directory"},
		"interpreter not run": {
			head: "#!/opt/py\n", want: "the #! line of /app names /opt/py, which this user cannot run",
		},
		"interpreter":          {head: "#! /bin/sh -e\n"},
		"env, program by path": {head: "#!/usr/bin/env /bin/sh\n"},
		"env, no program":      {head: "#!/usr/bin/env -S -u X\n"},
		"env, program on PATH": {head: "#!/usr/bin/env -S python3 -u\n"},
		"file not read":        {head: "-"},
		"no line end":          {head: "#!/bin/sh"},
		"empty file":           {head: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{"/app": tc.head}
			if tc.head == "-" {
				files = nil
			}

			env := doctorSafetyEnv(files, infos, []string{"/opt/py"}, []string{"python3"})
			if got := env.shebangProblem("/app"); got != tc.want {
				t.Errorf("shebangProblem() of %q = %q, want %q", tc.head, got, tc.want)
			}
		})
	}
}

// A program that env runs by a path that it cannot run is named as a problem in a sentence that reads.
func TestShebangProblemEnvPath(t *testing.T) {
	t.Parallel()

	dirInfo, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	fileInfo, err := os.Stat(writeProgram(t, t.TempDir(), "f", "", 0o755))
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	infos := map[string]os.FileInfo{
		"/usr/bin/env": fileInfo, "/opt/dir": dirInfo, "/opt/noexec": fileInfo, "/secret/py": nil,
	}

	tests := map[string]struct {
		program string
		want    string
	}{
		"missing":    {program: "/nowhere/py", want: "which does not exist"},
		"not read":   {program: "/secret/py", want: "which cannot be read (permission denied)"},
		"directory":  {program: "/opt/dir", want: "which is a directory"},
		"not run":    {program: "/opt/noexec", want: "which this user cannot run"},
		"can be run": {program: "/usr/bin/env"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := doctorSafetyEnv(map[string]string{"/app": "#!/usr/bin/env " + tc.program + " -u\n"}, infos,
				[]string{"/opt/noexec"}, nil)

			want := ""
			if tc.want != "" {
				want = "the #! line of /app runs " + tc.program + " through env, " + tc.want
			}

			if diff := cmp.Diff(want, env.shebangProblem("/app")); diff != "" {
				t.Errorf("shebangProblem() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// runnableProblem says why this user cannot run a path: it does not exist, cannot be read, is a directory, or has
// no execute permission for this user.
func TestRunnableProblem(t *testing.T) {
	t.Parallel()

	dirInfo, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	fileInfo, err := os.Stat(writeProgram(t, t.TempDir(), "f", "", 0o755))
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	env := doctorSafetyEnv(nil,
		map[string]os.FileInfo{"/ok": fileInfo, "/dir": dirInfo, "/noexec": fileInfo, "/secret/x": nil},
		[]string{"/noexec"}, nil)

	tests := map[string]string{
		"/ok":       "",
		"/missing":  "which does not exist",
		"/secret/x": "which cannot be read (permission denied)",
		"/dir":      "which is a directory",
		"/noexec":   "which this user cannot run",
	}

	for path, want := range tests {
		if got := env.runnableProblem(path); got != want {
			t.Errorf("runnableProblem(%s) = %q, want %q", path, got, want)
		}
	}
}

// envProgram skips the options of env, the arguments of -u and -C, and the variables, and gives the program.
func TestEnvProgram(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args string
		want string
	}{
		"program":         {args: "python3", want: "python3"},
		"split options":   {args: "-S -i python3 -u", want: "python3"},
		"variables":       {args: "A=1 B=2 sh -e", want: "sh"},
		"unset":           {args: "-S -u PYTHONPATH python3", want: "python3"},
		"long unset":      {args: "-S --unset PYTHONPATH --unset=HOME python3", want: "python3"},
		"chdir":           {args: "-S -C /tmp --chdir /srv sh", want: "sh"},
		"joined unset":    {args: "-S -uPYTHONPATH sh", want: "sh"},
		"no program":      {args: "-S -u PYTHONPATH", want: ""},
		"only a variable": {args: "A=1", want: ""},
		"tabs and spaces": {args: "\t-S\t sh", want: "sh"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := envProgram(tc.args); got != tc.want {
				t.Errorf("envProgram(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// envProgram gives one of the arguments, never an option or a variable; shebangProblem never panics on any file
// start, and finds no problem in a file that does not start with #!.
func TestShebangProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		args := rapid.StringMatching(`[ a-z=/.-]{0,40}`).Draw(t, "args")

		program := envProgram(args)
		if program != "" && (strings.HasPrefix(program, "-") || strings.Contains(program, "=") ||
			!strings.Contains(" "+args+" ", " "+program+" ") && !strings.Contains(args, program)) {
			t.Fatalf("envProgram(%q) = %q, want an argument", args, program)
		}

		head := rapid.SliceOfN(rapid.Byte(), 0, shebangRead).Draw(t, "head")
		env := fakeDoctorEnv(nil, false)
		env.readAt = func(string, int64, int) ([]byte, error) { return head, nil }
		env.stat = func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }

		problem := env.shebangProblem("/p")
		if !strings.HasPrefix(string(head), "#!") && problem != "" {
			t.Fatalf("shebangProblem() of %q = %q, want none without #!", head, problem)
		}
	})
}

// doctorBigFile is the file information of a store file of size bytes.
type doctorBigFile struct {
	os.FileInfo

	size int64
}

func (f doctorBigFile) Size() int64 { return f.size }

// doctorFreeBolt is the four pages of doctorFilesBolt with a free page list of count pages on page 2.
func doctorFreeBolt(count int) []byte {
	data := doctorFilesBolt(doctorFilesPageSize)
	page := data[2*doctorFilesPageSize:]
	order := binary.NativeEndian

	order.PutUint64(page[0:], 2)
	order.PutUint16(page[8:], boltFreelistFlag)

	if count >= boltCountOverflow {
		order.PutUint16(page[10:], boltCountOverflow)
		order.PutUint64(page[boltPageHeader:], uint64(count))
	} else {
		order.PutUint16(page[10:], uint16(count))
	}

	return data
}

// store-fs warns about a store directory on a network or FUSE filesystem, and about a store file over 100 MiB with
// more than half its pages free, which it reads without the lock. It skips when it can check neither.
func TestStoreFSCheck(t *testing.T) {
	t.Parallel()

	const big = 200 << 20

	tests := map[string]struct {
		endpoint string // the store.endpoint, when not the store of the test directory
		fsType   string
		fsErr    error
		size     int64
		data     []byte
		want     doctorCheck
		wantHnt  string
	}{
		"local": {fsType: "ext4", want: row("", doctorPass, "DIR is on ext4")},
		"not a bolt endpoint": {
			endpoint: "etcd://localhost:2379", fsType: "ext4",
			want: row("", doctorSkip, "not checked, because store.endpoint is not valid (the store.endpoint row checks it)"),
		},
		"nfs": {
			fsType: "nfs",
			want: row("", doctorWarn, "DIR is on nfs, a network filesystem, where the file locks and fsync that "+
				"BoltDB needs can fail"),
			wantHnt: "set store.endpoint to a path on a local disk",
		},
		"fuse": {
			fsType: "fuse",
			want: row("", doctorWarn, "DIR is on a FUSE filesystem, such as sshfs, a filesystem, where the file locks "+
				"and fsync that BoltDB needs can fail"),
			wantHnt: "set store.endpoint to a path on a local disk",
		},
		"unknown type": {
			fsErr: errors.New("only Linux gives the filesystem type"),
			want: row("", doctorSkip, "not checked, because doctor cannot read the filesystem type of DIR: only Linux "+
				"gives the filesystem type"),
		},
		"small store": {fsType: "xfs", size: 1 << 20, data: doctorFreeBolt(3), want: row("", doctorPass, "DIR is on xfs")},
		"big store, few free pages": {
			fsType: "xfs", size: big, data: doctorFreeBolt(1),
			want: row("", doctorPass, "DIR is on xfs. DIR/store.bdb is 200.0 MiB, and 1 of its 4 pages are free"),
		},
		"big store, half the pages free": {
			fsType: "xfs", size: big, data: doctorFreeBolt(2),
			want: row("", doctorPass, "DIR is on xfs. DIR/store.bdb is 200.0 MiB, and 2 of its 4 pages are free"),
		},
		"store of exactly 100 MiB": {
			fsType: "xfs", size: compactMinBytes, data: doctorFreeBolt(3), want: row("", doctorPass, "DIR is on xfs"),
		},
		"big store, many free pages": {
			fsType: "ext4", size: big, data: doctorFreeBolt(3),
			want: row("", doctorWarn, "DIR is on ext4. DIR/store.bdb is 200.0 MiB, and 75% of its pages are free"),
			wantHnt: "stop phenix ui, run bbolt compact -o <new file> <store file> (go install " +
				"go.etcd.io/bbolt/cmd/bbolt@latest), keep a backup, then replace the store file with the new file",
		},
		"big store, overflow count": {
			fsType: "ext4", size: big, data: doctorFreeBolt(boltCountOverflow + 1),
			want: row("", doctorPass, "DIR is on ext4. The free pages of DIR/store.bdb are not checked: the free page "+
				"list gives 65536 pages, more than the 4 of the file"),
		},
		"big store, no free page list": {
			fsType: "ext4", size: big,
			data: func() []byte {
				data := doctorFreeBolt(0)
				for i := range 2 {
					binary.NativeEndian.PutUint64(data[i*doctorFilesPageSize+boltPageHeader+32:], boltNoFreelist)
					doctorFilesResum(data[i*doctorFilesPageSize:])
				}

				return data
			}(),
			want: row("", doctorPass, "DIR is on ext4. The free pages of DIR/store.bdb are not checked: the file keeps "+
				"no free page list, as earlier phenix releases wrote it"),
		},
		"big store, unknown type": {
			fsErr: errors.New("only Linux gives the filesystem type"), size: big, data: doctorFreeBolt(3),
			want: row("", doctorWarn, "doctor cannot read the filesystem type of DIR: only Linux gives the "+
				"filesystem type. DIR/store.bdb is 200.0 MiB, and 75% of its pages are free"),
			wantHnt: "stop phenix ui, run bbolt compact -o <new file> <store file> (go install " +
				"go.etcd.io/bbolt/cmd/bbolt@latest), keep a backup, then replace the store file with the new file",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)
			in := fakeDoctorInput(dir, nil)
			path := filepath.Join(dir, "store.bdb")

			if tc.endpoint != "" {
				in.settings["store.endpoint"] = resolvedSetting{value: tc.endpoint, text: tc.endpoint, source: sourceFlag}
			}

			if tc.data != nil {
				writeSized(t, path, len(tc.data))
			}

			in.env.fsType = func(p string) (string, error) {
				if p != dir {
					return "", errors.New("wrong directory " + p)
				}

				return tc.fsType, tc.fsErr
			}
			stat := in.env.stat
			in.env.stat = func(p string) (os.FileInfo, error) {
				info, err := stat(p)
				if err == nil && p == path {
					return doctorBigFile{FileInfo: info, size: tc.size}, nil
				}

				return info, err
			}
			in.env.readAt = doctorFilesReadAt(tc.data)

			got := storeFSCheck(in)
			got.Detail = strings.ReplaceAll(got.Detail, dir, "DIR")

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("storeFSCheck() mismatch (-want +got):\n%s", diff)
			}

			if hint := doctorHintOf(got, storeFSHint); hint != tc.wantHnt {
				t.Errorf("storeFSHint() = %q, want %q", hint, tc.wantHnt)
			}
		})
	}
}

// The free page count that boltFreePages reads without the lock is the count bbolt gives, and the pages are the
// pages of the file, for a store with free pages and for an empty store.
func TestBoltFreePagesMatchesBbolt(t *testing.T) {
	t.Parallel()

	for name, deletes := range map[string]int{"free pages": 300, "no free pages": 0} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "store.bdb")

			db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
			if err != nil {
				t.Fatalf("bbolt.Open() error = %v", err)
			}

			value := make([]byte, 4096)

			err = db.Update(func(tx *bbolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists([]byte("Experiment"))
				if err != nil {
					return err
				}

				for i := range 400 {
					if err := b.Put(binary.BigEndian.AppendUint32(nil, uint32(i)), value); err != nil {
						return err
					}
				}

				return nil
			})
			if err != nil {
				t.Fatalf("db.Update() error = %v", err)
			}

			err = db.Update(func(tx *bbolt.Tx) error {
				for i := range deletes {
					if err := tx.Bucket([]byte("Experiment")).Delete(binary.BigEndian.AppendUint32(nil, uint32(i))); err != nil {
						return err
					}
				}

				return nil
			})
			if err != nil {
				t.Fatalf("db.Update() error = %v", err)
			}

			if err := db.Close(); err != nil {
				t.Fatalf("db.Close() error = %v", err)
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("os.Stat() error = %v", err)
			}

			env := fakeDoctorEnv(nil, false)
			env.pageSize = os.Getpagesize()

			free, pages, why := env.boltFreePages(path, info.Size())
			if why != "" {
				t.Fatalf("boltFreePages() why = %q", why)
			}

			// a writable open loads the free page list
			db, err = bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
			if err != nil {
				t.Fatalf("bbolt.Open() error = %v", err)
			}

			defer func() { _ = db.Close() }()

			stats := db.Stats()
			if want := uint64(stats.FreePageN + stats.PendingPageN); free != want {
				t.Errorf("boltFreePages() free = %d, want %d from bbolt", free, want)
			}

			err = db.View(func(tx *bbolt.Tx) error {
				if want := uint64(tx.Size()) / uint64(db.Info().PageSize); pages != want {
					t.Errorf("boltFreePages() pages = %d, want %d from bbolt", pages, want)
				}

				return nil
			})
			if err != nil {
				t.Fatalf("db.View() error = %v", err)
			}

			if deletes > 0 && free == 0 {
				t.Errorf("boltFreePages() free = 0 after %d deletes, want free pages", deletes)
			}
		})
	}
}

// boltFreePages never panics and never gives more free pages than pages, for any file start.
func TestBoltFreePagesProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		data := doctorFreeBolt(rapid.IntRange(0, 4).Draw(t, "count"))
		for range rapid.IntRange(0, 8).Draw(t, "flips") {
			i := rapid.IntRange(0, len(data)-1).Draw(t, "at")
			data[i] = rapid.Byte().Draw(t, "byte")
		}

		env := fakeDoctorEnv(nil, false)
		env.readAt = doctorFilesReadAt(data)

		free, pages, why := env.boltFreePages("/store.bdb", rapid.Int64Range(0, 1<<40).Draw(t, "size"))
		if why == "" && free > pages {
			t.Fatalf("boltFreePages() = %d free of %d pages", free, pages)
		}
	})
}

// secret-files warns about each file that holds a secret and that its group or others can read, or that belongs to
// a user other than root or the user that runs phenix. The store file gets only the mode check.
func TestSecretFilesCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		modes   map[string]os.FileMode // by file: key, config, users, store
		owner   uint32
		os      string
		want    string
		status  doctorStatus
		wantHnt string
	}{
		"private": {
			modes: map[string]os.FileMode{"key": 0o600, "config": 0o600, "users": 0o400, "store": 0o600}, owner: 1000,
			status: doctorPass, want: "only the owner can read DIR/tls.key, DIR/secrets.yaml, DIR/users.yaml, DIR/store.bdb",
		},
		"readable": {
			modes: map[string]os.FileMode{"key": 0o640, "config": 0o644, "users": 0o600, "store": 0o604}, owner: 0,
			status: doctorWarn,
			want: "DIR/tls.key (the TLS private key) is readable by its group or by others (mode 0640), DIR/secrets.yaml " +
				"(holds ui.jwt-signing-key) is readable by its group or by others (mode 0644), DIR/store.bdb (the store, " +
				"with password hashes and tokens) is readable by its group or by others (mode 0604)",
			wantHnt: "run chmod 600 <path> on each file that the row names",
		},
		"another owner": {
			modes: map[string]os.FileMode{"key": 0o600, "users": 0o600, "store": 0o600}, owner: 4242,
			status: doctorWarn,
			want: "DIR/tls.key (the TLS private key) belongs to uid 4242, DIR/users.yaml (holds ui.users) belongs " +
				"to uid 4242",
			wantHnt: "run chown <user> <path> to give each file to the user that runs phenix",
		},
		"nothing set": {modes: map[string]os.FileMode{}, status: doctorPass, want: "no file that holds a secret is set or exists"},
		"windows": {
			modes: map[string]os.FileMode{"key": 0o644}, os: "windows", status: doctorSkip,
			want: "not checked, because phenix doctor checks file modes only on systems with Unix modes",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)
			files := map[string]string{
				"key": filepath.Join(dir, "tls.key"), "config": filepath.Join(dir, "secrets.yaml"),
				"users": filepath.Join(dir, "users.yaml"), "store": filepath.Join(dir, "store.bdb"),
			}

			for file, mode := range tc.modes {
				writeProgram(t, dir, filepath.Base(files[file]), "S3CR3T-CONTENT", mode)
			}

			settings := map[string]string{}
			if _, ok := tc.modes["key"]; ok {
				settings["ui.tls-key"] = files["key"]
			}

			in := fakeDoctorInput(dir, settings)
			in.config.files = map[string][]string{
				"ui.jwt-signing-key": {files["config"]}, "ui.users": {files["users"], files["users"]},
			}

			if tc.os != "" {
				in.system.os = tc.os
			}

			stat := in.env.stat
			in.env.stat = func(p string) (os.FileInfo, error) {
				info, err := stat(p)
				if err != nil {
					return nil, err
				}

				return doctorFilesOwned{FileInfo: info, uid: tc.owner}, nil
			}

			got := secretFilesCheck(in)
			got.Detail = strings.ReplaceAll(got.Detail, dir, "DIR")

			if diff := cmp.Diff(row("", tc.status, tc.want), got); diff != "" {
				t.Errorf("secretFilesCheck() mismatch (-want +got):\n%s", diff)
			}

			if strings.Contains(got.Detail, "S3CR3T") {
				t.Errorf("secretFilesCheck() detail %q shows the content of a file", got.Detail)
			}

			if hint := doctorHintOf(got, secretFilesHint); hint != tc.wantHnt {
				t.Errorf("secretFilesHint() = %q, want %q", hint, tc.wantHnt)
			}
		})
	}
}

// The Linux magic numbers name the filesystems store-fs warns about, and an unknown one gives its number.
func TestLinuxFilesystemName(t *testing.T) {
	t.Parallel()

	tests := map[uint32]string{0x6969: "nfs", 0xff534d42: "cifs", 0x65735546: "fuse", 0xef53: "ext4", 0x1234: "type 0x1234"}

	for magic, want := range tests {
		if got := linuxFilesystemName(magic); got != want {
			t.Errorf("linuxFilesystemName(%#x) = %q, want %q", magic, got, want)
		}
	}
}

// doctorSafetyReadAt reads from data as doctorFilesReadAt does, and fails at the offset failAt.
func doctorSafetyReadAt(data []byte, failAt int64) func(string, int64, int) ([]byte, error) {
	read := doctorFilesReadAt(data)

	return func(path string, off int64, n int) ([]byte, error) {
		if off == failAt {
			return nil, errors.New("read error")
		}

		return read(path, off, n)
	}
}

// doctorSafetyMeta sets the free page list and the page count of both meta pages of data, and their checksums.
func doctorSafetyMeta(data []byte, freelist, pages uint64) []byte {
	for i := range 2 {
		meta := data[i*doctorFilesPageSize+boltPageHeader:]
		binary.NativeEndian.PutUint64(meta[32:], freelist)
		binary.NativeEndian.PutUint64(meta[40:], pages)
		doctorFilesResum(data[i*doctorFilesPageSize:])
	}

	return data
}

// boltFreePages says why it gives no count for a file it cannot read, two corrupt meta pages, a page size below
// 512, or a free page list outside the file, cut short or not marked as one; it reads the other meta page when one
// is corrupt.
func TestBoltFreePagesEdges(t *testing.T) {
	t.Parallel()

	const page = doctorFilesPageSize

	corrupt := func(data []byte, i int) []byte {
		data[i*page+boltPageHeader+20] ^= 0xff

		return data
	}

	zeroPageSize := func(data []byte) []byte {
		binary.NativeEndian.PutUint32(data[boltPageHeader+8:], 0)
		doctorFilesResum(data)

		return data
	}

	tests := map[string]struct {
		data        []byte
		failAt      int64 // the offset of a read that fails, or -1
		size        int64 // the file size, or len(data) when 0
		free, pages uint64
		why         string
	}{
		"free pages":           {data: doctorFreeBolt(1), failAt: -1, free: 1, pages: 4},
		"first read fails":     {data: doctorFreeBolt(1), failAt: 0, why: "read error"},
		"second read fails":    {data: doctorFreeBolt(1), failAt: page, why: "read error"},
		"free list read fails": {data: doctorFreeBolt(1), failAt: 2 * page, why: "read error"},
		"both corrupt": {
			data: make([]byte, 4*page), failAt: -1, why: "both meta pages are corrupt (the store-file row checks them)",
		},
		"first corrupt":           {data: corrupt(doctorFreeBolt(2), 0), failAt: -1, free: 2, pages: 4},
		"second corrupt":          {data: corrupt(doctorFreeBolt(2), 1), failAt: -1, free: 2, pages: 4},
		"list past the pages":     {data: doctorSafetyMeta(doctorFreeBolt(1), 4, 4), failAt: -1, why: outsidePhrase},
		"list past the file size": {data: doctorFreeBolt(1), failAt: -1, size: page, why: outsidePhrase},
		"list cut short":          {data: doctorFreeBolt(1)[:2*page+8], failAt: -1, size: 4 * page, why: outsidePhrase},
		"count cut short": {
			data: doctorFreeBolt(boltCountOverflow + 1)[:2*page+20], failAt: -1, size: 4 * page, why: outsidePhrase,
		},
		"not the free list": {
			data: doctorFilesBolt(page), failAt: -1,
			why: "page 2 is not the free page list, such as after a write that moved it",
		},
		"negative size": {data: doctorFreeBolt(1), failAt: -1, size: -1, why: outsidePhrase},
		"zero page size": {
			data: zeroPageSize(doctorFreeBolt(1)), failAt: -1,
			why: "the page size 0 is not a BoltDB page size (the store-file row checks it)",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			env.pageSize = page
			env.readAt = doctorSafetyReadAt(tc.data, tc.failAt)

			size := tc.size
			if size == 0 {
				size = int64(len(tc.data))
			}

			free, pages, why := env.boltFreePages("/store.bdb", size)
			if free != tc.free || pages != tc.pages || why != tc.why {
				t.Errorf("boltFreePages() = %d, %d, %q, want %d, %d, %q", free, pages, why, tc.free, tc.pages, tc.why)
			}
		})
	}
}

// A secret file may belong to root, to this user, or to the user phenix resolved under sudo.
func TestSecretOwnerOK(t *testing.T) {
	t.Parallel()

	in := fakeDoctorInput(newDoctorTestDir(t), nil)
	in.uid, in.phenixUID = 1000, "1001"

	for owner, want := range map[int]bool{0: true, 1000: true, 1001: true, 1002: false, -1: false} {
		if got := in.secretOwnerOK(owner); got != want {
			t.Errorf("secretOwnerOK(%d) = %t, want %t", owner, got, want)
		}
	}

	in.phenixUID = ""
	if in.secretOwnerOK(1001) {
		t.Error("secretOwnerOK(1001) without sudo = true, want false")
	}
}
