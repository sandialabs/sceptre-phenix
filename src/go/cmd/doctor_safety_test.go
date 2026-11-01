package cmd

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

// doctorBigFile is the file information of a store file of size bytes, owned by uid.
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
		fsType  string
		fsErr   error
		size    int64
		data    []byte
		want    doctorCheck
		wantHnt string
	}{
		"local": {fsType: "ext4", want: row("", doctorPass, "DIR is on ext4")},
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

			// a read-only open loads no free page list
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
