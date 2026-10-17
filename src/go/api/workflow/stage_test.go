package workflow_test

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/workflow"
)

// stageFile creates path, and any missing parents, with content and exactly
// the permissions perm.
func stageFile(t *testing.T, path, content string, perm fs.FileMode) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

// stageMkdir creates the directory path, and any missing parents, with
// exactly the permissions perm. Owner access is restored when the test ends,
// so the temporary directory can be removed.
func stageMkdir(t *testing.T, path string, perm fs.FileMode) {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// stageSymlink creates link pointing at target.
func stageSymlink(t *testing.T, target, link string) {
	t.Helper()

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// stageRead returns the content of the file at path.
func stageRead(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// stageLeftovers returns the hidden .foo.* staging and backup entries in
// base, which StageInjects must never leave behind.
func stageLeftovers(t *testing.T, base string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(base, ".foo.*"))
	if err != nil {
		t.Fatal(err)
	}

	return matches
}

// stageSkipIfRoot skips a test that needs a permission error, which root
// never gets.
func stageSkipIfRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
}

func TestStageInjectsCopiesTree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "topo", "phenix-injects")
	stageFile(t, filepath.Join(src, "a.txt"), "alpha", 0o644)
	stageFile(t, filepath.Join(src, "bin", "run.sh"), "#!/bin/sh\n", 0o755)
	stageFile(t, filepath.Join(src, "conf", "deep", "c.cfg"), "gamma", 0o600)
	stageMkdir(t, filepath.Join(src, "empty"), 0o755)

	// Neither the base directory nor its parent exists yet.
	base := filepath.Join(root, "srv", "injects")

	files, err := workflow.StageInjects(src, base, "foo")
	if err != nil {
		t.Fatalf("StageInjects: %v", err)
	}

	if files != 3 {
		t.Errorf("files = %d, want 3", files)
	}

	want := map[string]string{
		"a.txt":           "alpha",
		"bin/run.sh":      "#!/bin/sh\n",
		"conf/deep/c.cfg": "gamma",
	}

	for rel, content := range want {
		if got := stageRead(t, filepath.Join(base, "foo", filepath.FromSlash(rel))); got != content {
			t.Errorf("%s = %q, want %q", rel, got, content)
		}
	}

	if info, err := os.Stat(filepath.Join(base, "foo", "empty")); err != nil || !info.IsDir() {
		t.Errorf("empty directory was not copied: %v", err)
	}

	if got := stageRead(t, filepath.Join(src, "a.txt")); got != "alpha" {
		t.Errorf("source a.txt = %q, want it unchanged", got)
	}

	if left := stageLeftovers(t, base); len(left) != 0 {
		t.Errorf("leftover entries in base: %v", left)
	}
}

func TestStageInjectsEmptySource(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "injects")
	stageFile(t, filepath.Join(base, "foo", "old.txt"), "old", 0o644)

	src := filepath.Join(root, "phenix-injects")
	stageMkdir(t, src, 0o755)

	files, err := workflow.StageInjects(src, base, "foo")
	if err != nil {
		t.Fatalf("StageInjects: %v", err)
	}

	if files != 0 {
		t.Errorf("files = %d, want 0", files)
	}

	entries, err := os.ReadDir(filepath.Join(base, "foo"))
	if err != nil || len(entries) != 0 {
		t.Errorf("base/foo entries = %v (err %v), want an empty directory", entries, err)
	}
}

func TestStageInjectsPreservesModes(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "phenix-injects")
	stageMkdir(t, src, 0o750)
	stageFile(t, filepath.Join(src, "read-write"), "rw", 0o644)
	stageFile(t, filepath.Join(src, "private"), "private", 0o600)
	stageFile(t, filepath.Join(src, "script"), "#!/bin/sh\n", 0o755)
	stageFile(t, filepath.Join(src, "group-exec"), "group", 0o750)
	stageFile(t, filepath.Join(src, "read-only"), "ro", 0o444)
	stageFile(t, filepath.Join(src, "group-dir", "inner"), "inner", 0o640)
	stageMkdir(t, filepath.Join(src, "group-dir"), 0o750)
	stageFile(t, filepath.Join(src, "locked-dir", "inner"), "locked", 0o400)
	stageMkdir(t, filepath.Join(src, "locked-dir"), 0o555)

	base := filepath.Join(root, "injects")

	if _, err := workflow.StageInjects(src, base, "foo"); err != nil {
		t.Fatalf("StageInjects: %v", err)
	}

	tests := []struct {
		path string
		want fs.FileMode
	}{
		{path: ".", want: 0o750},
		{path: "read-write", want: 0o644},
		{path: "private", want: 0o600},
		{path: "script", want: 0o755},
		{path: "group-exec", want: 0o750},
		{path: "read-only", want: 0o444},
		{path: "group-dir", want: 0o750},
		{path: "group-dir/inner", want: 0o640},
		{path: "locked-dir", want: 0o755}, // directories always get owner access
		{path: "locked-dir/inner", want: 0o400},
	}

	for _, tt := range tests {
		info, err := os.Lstat(filepath.Join(base, "foo", filepath.FromSlash(tt.path)))
		if err != nil {
			t.Errorf("%s: %v", tt.path, err)

			continue
		}

		if got := info.Mode().Perm(); got != tt.want {
			t.Errorf("%s mode = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestStageInjectsCopiesSymlinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "phenix-injects")
	stageFile(t, filepath.Join(src, "v1", "payload.bin"), "v1", 0o644)

	links := map[string]string{
		"current":  "v1",                         // relative, to a directory
		"payload":  "v1/payload.bin",             // relative, to a file
		"absolute": "/nonexistent/phenix/target", // absolute and dangling
		"dangling": "missing.txt",                // relative and dangling
	}

	for link, target := range links {
		stageSymlink(t, target, filepath.Join(src, link))
	}

	base := filepath.Join(root, "injects")

	files, err := workflow.StageInjects(src, base, "foo")
	if err != nil {
		t.Fatalf("StageInjects: %v", err)
	}

	if files != 1 {
		t.Errorf("files = %d, want 1 (symlinks are neither counted nor followed)", files)
	}

	for link, want := range links {
		path := filepath.Join(base, "foo", link)

		info, err := os.Lstat(path)
		if err != nil {
			t.Errorf("%s: %v", link, err)

			continue
		}

		if info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("%s mode = %v, want a symlink", link, info.Mode())

			continue
		}

		if got, err := os.Readlink(path); err != nil || got != want {
			t.Errorf("%s -> %q (err %v), want %q", link, got, err, want)
		}
	}
}

func TestStageInjectsReplacesExisting(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "injects")
	stageFile(t, filepath.Join(base, "foo", "old.txt"), "old", 0o644)
	stageFile(t, filepath.Join(base, "foo", "olddir", "stale.txt"), "stale", 0o600)
	stageFile(t, filepath.Join(base, "bar", "other.txt"), "other", 0o644)

	src := filepath.Join(root, "phenix-injects")
	stageFile(t, filepath.Join(src, "new.txt"), "new", 0o640)
	stageFile(t, filepath.Join(src, "olddir", "fresh.txt"), "fresh", 0o644)

	files, err := workflow.StageInjects(src, base, "foo")
	if err != nil {
		t.Fatalf("StageInjects: %v", err)
	}

	if files != 2 {
		t.Errorf("files = %d, want 2", files)
	}

	for _, gone := range []string{"old.txt", "olddir/stale.txt"} {
		if _, err := os.Lstat(filepath.Join(base, "foo", filepath.FromSlash(gone))); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still present (err %v)", gone, err)
		}
	}

	if got := stageRead(t, filepath.Join(base, "foo", "new.txt")); got != "new" {
		t.Errorf("new.txt = %q, want %q", got, "new")
	}

	if got := stageRead(t, filepath.Join(base, "foo", "olddir", "fresh.txt")); got != "fresh" {
		t.Errorf("olddir/fresh.txt = %q, want %q", got, "fresh")
	}

	if got := stageRead(t, filepath.Join(base, "bar", "other.txt")); got != "other" {
		t.Errorf("sibling bar/other.txt = %q, want it unchanged", got)
	}

	if left := stageLeftovers(t, base); len(left) != 0 {
		t.Errorf("leftover entries in base: %v", left)
	}
}

func TestStageInjectsReplacesSymlinkedDestination(t *testing.T) {
	root := t.TempDir()
	elsewhere := filepath.Join(root, "elsewhere")
	stageFile(t, filepath.Join(elsewhere, "keep.txt"), "keep", 0o600)

	base := filepath.Join(root, "injects")
	stageMkdir(t, base, 0o755)
	stageSymlink(t, elsewhere, filepath.Join(base, "foo"))

	src := filepath.Join(root, "phenix-injects")
	stageFile(t, filepath.Join(src, "new.txt"), "new", 0o644)

	if _, err := workflow.StageInjects(src, base, "foo"); err != nil {
		t.Fatalf("StageInjects: %v", err)
	}

	info, err := os.Lstat(filepath.Join(base, "foo"))
	if err != nil || !info.IsDir() {
		t.Fatalf("base/foo is not a directory after staging (err %v)", err)
	}

	if got := stageRead(t, filepath.Join(elsewhere, "keep.txt")); got != "keep" {
		t.Errorf("symlink target keep.txt = %q, want it unchanged", got)
	}

	if _, err := os.Lstat(filepath.Join(elsewhere, "new.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("staged file was written through the old symlink (err %v)", err)
	}
}

func TestStageInjectsFailedCopyKeepsExisting(t *testing.T) {
	tests := []struct {
		name    string
		nonRoot bool
		setup   func(t *testing.T, src string)
		check   func(err error) bool
	}{
		{
			name:    "unreadable file",
			nonRoot: true,
			setup: func(t *testing.T, src string) {
				t.Helper()
				stageFile(t, filepath.Join(src, "secret"), "secret", 0o000)
			},
			check: func(err error) bool { return errors.Is(err, fs.ErrPermission) },
		},
		{
			name:    "unreadable directory",
			nonRoot: true,
			setup: func(t *testing.T, src string) {
				t.Helper()
				stageMkdir(t, filepath.Join(src, "locked"), 0o000)
			},
			check: func(err error) bool { return errors.Is(err, fs.ErrPermission) },
		},
		{
			// Readable but not searchable: listing works, but lstat of an entry
			// fails.
			name:    "unsearchable directory",
			nonRoot: true,
			setup: func(t *testing.T, src string) {
				t.Helper()
				stageFile(t, filepath.Join(src, "noexec", "inner"), "inner", 0o644)
				stageMkdir(t, filepath.Join(src, "noexec"), 0o444)
			},
			check: func(err error) bool { return errors.Is(err, fs.ErrPermission) },
		},
		{
			name: "named pipe",
			setup: func(t *testing.T, src string) {
				t.Helper()

				if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(err error) bool { return strings.Contains(err.Error(), "unsupported file type") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.nonRoot {
				stageSkipIfRoot(t)
			}

			root := t.TempDir()
			base := filepath.Join(root, "injects")
			stageFile(t, filepath.Join(base, "foo", "keep.txt"), "keep", 0o644)

			// a.txt sorts first, so it is copied before the failing entry.
			src := filepath.Join(root, "phenix-injects")
			stageFile(t, filepath.Join(src, "a.txt"), "a", 0o644)
			tt.setup(t, src)

			files, err := workflow.StageInjects(src, base, "foo")
			if err == nil || !tt.check(err) {
				t.Fatalf("StageInjects err = %v, want a %s error", err, tt.name)
			}

			if files != 0 {
				t.Errorf("files = %d, want 0 on error", files)
			}

			if got := stageRead(t, filepath.Join(base, "foo", "keep.txt")); got != "keep" {
				t.Errorf("existing keep.txt = %q, want it unchanged", got)
			}

			if _, err := os.Lstat(filepath.Join(base, "foo", "a.txt")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("part of the failed copy reached base/foo (err %v)", err)
			}

			if left := stageLeftovers(t, base); len(left) != 0 {
				t.Errorf("leftover entries in base: %v", left)
			}
		})
	}
}

func TestStageInjectsRejectsInvalidNames(t *testing.T) {
	src := filepath.Join(t.TempDir(), "phenix-injects")
	stageFile(t, filepath.Join(src, "a.txt"), "a", 0o644)

	tests := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "dot", value: "."},
		{name: "dot dot", value: ".."},
		{name: "separator", value: "a/b"},
		{name: "parent escape", value: "../escape"},
		{name: "absolute", value: "/abs"},
		{name: "hidden", value: ".hidden"},
		{name: "leading dash", value: "-dash"},
		{name: "space", value: "has space"},
		{name: "trailing newline", value: "foo\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "injects")

			files, err := workflow.StageInjects(src, base, tt.value)
			if !errors.Is(err, workflow.ErrInvalidName) {
				t.Fatalf("StageInjects(%q) err = %v, want ErrInvalidName", tt.value, err)
			}

			if files != 0 {
				t.Errorf("files = %d, want 0 on error", files)
			}

			if _, err := os.Stat(base); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("base was created for an invalid name (err %v)", err)
			}
		})
	}
}

func TestStageInjectsRejectsUnsafeDestinations(t *testing.T) {
	// Every path is relative to the test's temporary directory, which holds
	// injects/foo/keep.txt and topo/phenix-injects/a.txt.
	tests := []struct {
		name    string
		src     string
		base    string
		protect []string
		dirs    []string
		links   map[string]string // link -> target
	}{
		{
			name: "source is the destination",
			src:  "injects/foo",
			base: "injects",
		},
		{
			name: "source inside the destination",
			src:  "injects/foo/nested",
			base: "injects",
			dirs: []string{"injects/foo/nested"},
		},
		{
			name: "destination inside the source",
			src:  "topo",
			base: "topo/injects",
		},
		{
			name:  "source reached through a symlink",
			src:   "src-link",
			base:  "injects",
			links: map[string]string{"src-link": "injects/foo"},
		},
		{
			name:    "protected path is the destination",
			src:     "topo/phenix-injects",
			base:    "injects",
			protect: []string{"topo", "injects/foo"},
		},
		{
			name:    "protected path inside the destination",
			src:     "topo/phenix-injects",
			base:    "injects",
			protect: []string{"injects/foo/sub"},
			dirs:    []string{"injects/foo/sub"},
		},
		{
			name:    "protected path reached through a symlink",
			src:     "topo/phenix-injects",
			base:    "injects",
			protect: []string{"cwd-link"},
			dirs:    []string{"injects/foo/sub"},
			links:   map[string]string{"cwd-link": "injects/foo/sub"},
		},
		{
			name:    "base reached through a symlink",
			src:     "topo/phenix-injects",
			base:    "base-link",
			protect: []string{"injects/foo/sub"},
			dirs:    []string{"injects/foo/sub"},
			links:   map[string]string{"base-link": "injects"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			stageFile(t, filepath.Join(root, "injects", "foo", "keep.txt"), "keep", 0o600)
			stageFile(t, filepath.Join(root, "topo", "phenix-injects", "a.txt"), "a", 0o644)

			for _, dir := range tt.dirs {
				stageMkdir(t, filepath.Join(root, filepath.FromSlash(dir)), 0o755)
			}

			for link, target := range tt.links {
				stageSymlink(t, filepath.Join(root, filepath.FromSlash(target)), filepath.Join(root, link))
			}

			protect := make([]string, 0, len(tt.protect))
			for _, p := range tt.protect {
				protect = append(protect, filepath.Join(root, filepath.FromSlash(p)))
			}

			src := filepath.Join(root, filepath.FromSlash(tt.src))
			base := filepath.Join(root, filepath.FromSlash(tt.base))

			files, err := workflow.StageInjects(src, base, "foo", protect...)
			if !errors.Is(err, workflow.ErrUnsafeDestination) {
				t.Fatalf("StageInjects err = %v, want ErrUnsafeDestination", err)
			}

			if files != 0 {
				t.Errorf("files = %d, want 0 on error", files)
			}

			if got := stageRead(t, filepath.Join(root, "injects", "foo", "keep.txt")); got != "keep" {
				t.Errorf("injects/foo/keep.txt = %q, want it unchanged", got)
			}

			if left := stageLeftovers(t, base); len(left) != 0 {
				t.Errorf("leftover entries in base: %v", left)
			}
		})
	}
}

func TestStageInjectsAllowsDestinationInsideProtectedPaths(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "topo", "phenix-injects")
	stageFile(t, filepath.Join(src, "a.txt"), "a", 0o644)

	base := filepath.Join(root, "injects")
	sibling := filepath.Join(base, "foobar") // starts with "foo", but is not base/foo
	stageMkdir(t, sibling, 0o750)

	// A working directory is often an ancestor of base, such as / or the
	// phenix base directory.
	protect := []string{string(filepath.Separator), root, base, filepath.Join(root, "topo"), sibling}

	files, err := workflow.StageInjects(src, base, "foo", protect...)
	if err != nil {
		t.Fatalf("StageInjects: %v", err)
	}

	if files != 1 {
		t.Errorf("files = %d, want 1", files)
	}
}

func TestStageInjectsPathErrors(t *testing.T) {
	// Every path is relative to the test's temporary directory, which holds
	// phenix-injects/a.txt, the regular file file.txt and the read-only
	// directory read-only.
	tests := []struct {
		name    string
		src     string
		base    string
		protect []string
		nonRoot bool
		wantIs  error
		wantMsg string
	}{
		{
			name:    "missing source",
			src:     "missing",
			base:    "injects",
			wantIs:  fs.ErrNotExist,
			wantMsg: "reading injects source",
		},
		{
			name:    "source is a file",
			src:     "file.txt",
			base:    "injects",
			wantMsg: "is not a directory",
		},
		{
			name:    "base under a file",
			src:     "phenix-injects",
			base:    "file.txt/injects",
			wantMsg: "creating injects directory",
		},
		{
			name:    "missing protected path",
			src:     "phenix-injects",
			base:    "injects",
			protect: []string{"missing"},
			wantIs:  fs.ErrNotExist,
			wantMsg: "resolving",
		},
		{
			name:    "base not writable",
			src:     "phenix-injects",
			base:    "read-only",
			nonRoot: true,
			wantIs:  fs.ErrPermission,
			wantMsg: ".foo.tmp-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.nonRoot {
				stageSkipIfRoot(t)
			}

			root := t.TempDir()
			stageFile(t, filepath.Join(root, "phenix-injects", "a.txt"), "a", 0o644)
			stageFile(t, filepath.Join(root, "file.txt"), "file", 0o644)
			stageMkdir(t, filepath.Join(root, "read-only"), 0o555)

			protect := make([]string, 0, len(tt.protect))
			for _, p := range tt.protect {
				protect = append(protect, filepath.Join(root, p))
			}

			files, err := workflow.StageInjects(
				filepath.Join(root, tt.src),
				filepath.Join(root, filepath.FromSlash(tt.base)),
				"foo",
				protect...,
			)
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("StageInjects err = %v, want an error containing %q", err, tt.wantMsg)
			}

			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("StageInjects err = %v, want errors.Is(err, %v)", err, tt.wantIs)
			}

			if files != 0 {
				t.Errorf("files = %d, want 0 on error", files)
			}
		})
	}
}

func TestStageInjectsReportsUnremovablePreviousCopy(t *testing.T) {
	stageSkipIfRoot(t)

	root := t.TempDir()
	base := filepath.Join(root, "injects")
	stageFile(t, filepath.Join(base, "foo", "locked", "old.txt"), "old", 0o644)
	stageMkdir(t, filepath.Join(base, "foo", "locked"), 0o555)

	// The locked directory moves to .foo.old-*, so restore access there too.
	t.Cleanup(func() {
		matches, _ := filepath.Glob(filepath.Join(base, ".foo.old-*", "locked"))
		for _, m := range matches {
			_ = os.Chmod(m, 0o755)
		}
	})

	src := filepath.Join(root, "phenix-injects")
	stageFile(t, filepath.Join(src, "new.txt"), "new", 0o644)

	files, err := workflow.StageInjects(src, base, "foo")
	if !errors.Is(err, workflow.ErrPreviousCopyKept) {
		t.Fatalf("StageInjects err = %v, want ErrPreviousCopyKept", err)
	}

	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("StageInjects err = %v, want errors.Is(err, fs.ErrPermission)", err)
	}

	// The new tree is in place, so the count is returned with the error.
	if files != 1 {
		t.Errorf("files = %d, want 1", files)
	}

	if got := stageRead(t, filepath.Join(base, "foo", "new.txt")); got != "new" {
		t.Errorf("new.txt = %q, want the new copy in place", got)
	}

	olds, _ := filepath.Glob(filepath.Join(base, ".foo.old-*"))
	if len(olds) != 1 {
		t.Fatalf("previous copies in base = %v, want exactly one", olds)
	}

	// The error must say where the previous copy is. It holds the resolved
	// path, so compare base names.
	for _, part := range []string{"but could not remove the previous injects copy ", filepath.Base(olds[0]) + ": "} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("StageInjects err = %v, want it to contain %q", err, part)
		}
	}
}

func TestPathWithin(t *testing.T) {
	tests := []struct {
		target string
		dir    string
		want   bool
	}{
		{target: "/a/b", dir: "/a/b", want: true},
		{target: "/a/b/c", dir: "/a/b", want: true},
		{target: "/a/bc", dir: "/a/b", want: false},
		{target: "/a", dir: "/a/b", want: false},
		{target: "/a/b/../c", dir: "/a/b", want: false},
		{target: "/", dir: "/", want: true},
		{target: "/a", dir: "/", want: true},
	}

	for _, tt := range tests {
		if got := workflow.PathWithin(filepath.FromSlash(tt.target), filepath.FromSlash(tt.dir)); got != tt.want {
			t.Errorf("PathWithin(%q, %q) = %v, want %v", tt.target, tt.dir, got, tt.want)
		}
	}
}

func TestStagingLeftovers(t *testing.T) {
	// The random suffixes that StageInjects gives its hidden siblings are 26
	// characters from A to Z and 2 to 7.
	const (
		tokenA = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		tokenB = "234567ABCDEFGHIJKLMNOPQRST"
	)

	// Not leftovers of foo: other names, suffixes of the wrong length or outside A-Z and 2-7, and foo.old-x's
	// staging copy, whose lowercase suffix matches no token even on a case-insensitive file system.
	others := []string{
		"foo/keep.txt", "bar/keep.txt", ".bar.tmp-" + tokenA + "/a", ".bar.old-" + tokenB + "/a",
		".foobar.tmp-" + tokenA + "/a", ".foo.other/a", "foo.tmp-" + tokenA + "/a",
		".foo.tmp-AAAA/a", ".foo.old-" + tokenA + "x/a", ".foo.tmp-ABCDEFGHIJKLMNOPQRSTUVWXY1/a",
		".foo.old-ABCDEFGHIJKLMNOPQRSTUVWXY8/a", ".foo.tmp-zyxwvutsrqponmlkjihgfedcba/a",
		".foo.old-x.tmp-" + tokenB + "/a",
	}

	tests := []struct {
		name      string
		leftovers []string // entries in injects that are leftovers of foo
		linked    bool     // injects is a symbolic link to the directory that holds the entries
	}{
		{name: "none"},
		{name: "one of each kind", leftovers: []string{".foo.tmp-" + tokenA, ".foo.old-" + tokenB}},
		{name: "base is a symbolic link", leftovers: []string{".foo.tmp-" + tokenB, ".foo.old-" + tokenA}, linked: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			base := filepath.Join(root, "injects")
			target := base

			if tt.linked {
				target = filepath.Join(root, "real-injects")
				stageMkdir(t, target, 0o755)
				stageSymlink(t, target, base)
			}

			for _, entry := range others {
				stageFile(t, filepath.Join(target, filepath.FromSlash(entry)), "x", 0o644)
			}

			for _, entry := range tt.leftovers {
				stageFile(t, filepath.Join(target, entry, "a.txt"), "x", 0o644)
			}

			got, err := workflow.StagingLeftovers(base, "foo")
			if err != nil {
				t.Fatalf("StagingLeftovers(%q, foo) error = %v", base, err)
			}

			// The paths lie under the directory that holds the entries, with
			// every symlink resolved, and are sorted.
			resolved, err := filepath.EvalSymlinks(target)
			if err != nil {
				t.Fatalf("filepath.EvalSymlinks(%q) error = %v", target, err)
			}

			want := make([]string, 0, len(tt.leftovers))
			for _, entry := range tt.leftovers {
				want = append(want, filepath.Join(resolved, entry))
			}

			slices.Sort(want)

			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("StagingLeftovers(%q, foo) mismatch (-want +got):\n%s", base, diff)
			}

			for _, path := range got {
				if !strings.HasPrefix(path, resolved+string(filepath.Separator)) {
					t.Errorf("StagingLeftovers(%q, foo) returned %s, want a path under the resolved target %s", base, path, resolved)
				}
			}

			// Nothing is removed.
			for _, entry := range tt.leftovers {
				if _, err := os.Lstat(filepath.Join(base, entry)); err != nil {
					t.Errorf("os.Lstat(%q) after StagingLeftovers() error = %v, want the leftover kept", entry, err)
				}
			}
		})
	}
}

func TestStagingLeftoversErrors(t *testing.T) {
	root := t.TempDir()
	stageFile(t, filepath.Join(root, "file.txt"), "file", 0o644)
	stageMkdir(t, filepath.Join(root, "injects"), 0o755)

	tests := []struct {
		name   string
		base   string
		value  string
		wantIs error
	}{
		{name: "invalid name", base: "injects", value: "../foo", wantIs: workflow.ErrInvalidName},
		{name: "missing base", base: "missing", value: "foo", wantIs: fs.ErrNotExist},
		{name: "base is a file", base: "file.txt", value: "foo", wantIs: syscall.ENOTDIR},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workflow.StagingLeftovers(filepath.Join(root, tt.base), tt.value)
			if !errors.Is(err, tt.wantIs) {
				t.Fatalf("StagingLeftovers err = %v, want errors.Is(err, %v)", err, tt.wantIs)
			}

			if got != nil {
				t.Errorf("StagingLeftovers = %q, want nil on error", got)
			}
		})
	}
}

// stageNode is a directory entry of a generated injects tree: a file with its content, a symlink with its
// target, or a directory with its entries.
type stageNode struct {
	kind string
	perm fs.FileMode
	data string
	kids map[string]stageNode
}

// genStageTree draws the entries of a directory, with directories nested up to depth more levels.
func genStageTree(depth int) *rapid.Generator[map[string]stageNode] {
	kinds := []string{"file", "link"}
	if depth > 0 {
		kinds = append(kinds, "dir")
	}

	return rapid.Custom(func(t *rapid.T) map[string]stageNode {
		names := rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"a", "b.txt", ".hidden", "d e"}), 0, 3, rapid.ID).Draw(t, "names")
		tree := make(map[string]stageNode, len(names))

		for _, name := range names {
			switch kind := rapid.SampledFrom(kinds).Draw(t, "kind"); kind {
			case "file":
				tree[name] = stageNode{
					kind: kind,
					perm: rapid.SampledFrom([]fs.FileMode{0o400, 0o600, 0o640, 0o644, 0o755}).Draw(t, "filePerm"),
					data: rapid.StringMatching(`[a-z\n]{0,8}`).Draw(t, "content"),
				}
			case "link":
				tree[name] = stageNode{kind: kind, data: rapid.SampledFrom([]string{"a", "../b.txt", "/nonexistent"}).Draw(t, "target")}
			default:
				tree[name] = stageNode{
					kind: kind,
					perm: rapid.SampledFrom([]fs.FileMode{0o500, 0o555, 0o700, 0o750, 0o755}).Draw(t, "dirPerm"),
					kids: genStageTree(depth-1).Draw(t, "kids"),
				}
			}
		}

		return tree
	})
}

// stageWriteTree creates tree in the existing directory dir, setting each directory's mode after its entries.
func stageWriteTree(dir string, tree map[string]stageNode) error {
	for name, node := range tree {
		p := filepath.Join(dir, name)

		var err error

		switch node.kind {
		case "file":
			if err = os.WriteFile(p, []byte(node.data), 0o600); err == nil {
				err = os.Chmod(p, node.perm)
			}
		case "link":
			err = os.Symlink(node.data, p)
		default:
			if err = os.Mkdir(p, 0o700); err == nil {
				err = stageWriteTree(p, node.kids)
			}

			if err == nil {
				err = os.Chmod(p, node.perm)
			}
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// stageWant adds what staging tree under rel must give to want, and returns the number of regular files in it.
// A directory gains owner access.
func stageWant(tree map[string]stageNode, rel string, want map[string]string) int {
	files := 0

	for name, node := range tree {
		p := path.Join(rel, name)

		switch node.kind {
		case "file":
			want[p] = node.perm.String() + " " + node.data
			files++
		case "link":
			want[p] = "-> " + node.data
		default:
			want[p] = (node.perm | 0o700 | fs.ModeDir).String()
			files += stageWant(node.kids, p, want)
		}
	}

	return files
}

// stageSnapshot describes each entry under root as stageWant does.
func stageSnapshot(root string) (map[string]string, error) {
	got := map[string]string{}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}

		rel, _ := filepath.Rel(root, p)

		info, err := d.Info()
		if err != nil {
			return err
		}

		switch mode := info.Mode(); {
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			got[filepath.ToSlash(rel)] = "-> " + target

			return err
		case mode.IsDir():
			got[filepath.ToSlash(rel)] = mode.String()
		default:
			data, err := os.ReadFile(p)
			got[filepath.ToSlash(rel)] = mode.String() + " " + string(data)

			return err
		}

		return nil
	})

	return got, err
}

// stageRemove removes the tree at root, first giving each of its directories owner access.
func stageRemove(root string) error {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}

		return nil
	})

	return os.RemoveAll(root)
}

// StageInjects copies any tree exactly, and a later staging of another tree replaces it, leaving nothing else
// in base.
func TestStageInjectsProperties(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	checks := 0

	rapid.Check(t, func(t *rapid.T) {
		checks++

		dir := filepath.Join(root, strconv.Itoa(checks))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}

		defer func() { _ = stageRemove(dir) }()

		name := rapid.SampledFrom([]string{"foo", "v1.2", "a_b"}).Draw(t, "name")
		base := filepath.Join(dir, "injects")

		for i, label := range []string{"first", "second"} {
			tree := genStageTree(2).Draw(t, label)
			perm := rapid.SampledFrom([]fs.FileMode{0o700, 0o750, 0o755}).Draw(t, label+"Perm")
			src := filepath.Join(dir, label)

			if err := os.Mkdir(src, 0o700); err != nil {
				t.Fatal(err)
			}

			if err := stageWriteTree(src, tree); err != nil {
				t.Fatalf("writing tree %d: %v", i+1, err)
			}

			if err := os.Chmod(src, perm); err != nil {
				t.Fatal(err)
			}

			files, err := workflow.StageInjects(src, base, name)
			if err != nil {
				t.Fatalf("StageInjects(tree %d, %q) error = %v", i+1, name, err)
			}

			want := map[string]string{}
			if wantFiles := stageWant(tree, "", want); files != wantFiles {
				t.Errorf("StageInjects(tree %d, %q) = %d, want %d files", i+1, name, files, wantFiles)
			}

			got, err := stageSnapshot(filepath.Join(base, name))
			if err != nil {
				t.Fatalf("reading staged tree %d: %v", i+1, err)
			}

			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("StageInjects(tree %d, %q) staged tree mismatch (-want +got):\n%s", i+1, name, diff)
			}

			if info, err := os.Stat(filepath.Join(base, name)); err != nil || info.Mode().Perm() != perm {
				t.Fatalf("StageInjects(tree %d, %q) staged directory = %v, %v; want mode %v", i+1, name, info, err, perm)
			}

			entries, err := os.ReadDir(base)
			if err != nil || len(entries) != 1 || entries[0].Name() != name {
				t.Fatalf("StageInjects(tree %d, %q) left base holding %v (err %v), want only %s", i+1, name, entries, err, name)
			}
		}
	})
}

// PathWithin agrees with a segment prefix model over clean absolute paths.
func TestPathWithinProperties(t *testing.T) {
	t.Parallel()

	segments := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "ab", "a.b", "..a", "a..", "...", "b c"}), 0, 3)

	rapid.Check(t, func(t *rapid.T) {
		dir := "/" + path.Join(segments.Draw(t, "dir")...)
		target := "/" + path.Join(segments.Draw(t, "target")...)

		if rapid.Bool().Draw(t, "below") {
			target = path.Join(dir, path.Join(segments.Draw(t, "below")...))
		}

		want := target == dir || dir == "/" || strings.HasPrefix(target, dir+"/")
		if got := workflow.PathWithin(filepath.FromSlash(target), filepath.FromSlash(dir)); got != want {
			t.Fatalf("PathWithin(%q, %q) = %t, want %t", target, dir, got, want)
		}
	})
}

// StagingLeftovers lists exactly the entries a pattern for name's hidden staging siblings matches.
func TestStagingLeftoversProperties(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	prefix := rapid.SampledFrom([]string{".foo", ".foo.old-x", ".fo", ".foobar", "foo", ".v1.2"})
	infix := rapid.SampledFrom([]string{".tmp-", ".old-", "-tmp-", ".tmp"})
	suffix := rapid.OneOf(
		rapid.StringMatching(`[A-Z2-7]{26}`),
		rapid.StringMatching(`[A-Z2-7]{25,27}`),
		rapid.StringMatching(`[a-z0-9]{26}`),
		rapid.StringMatching(`[A-Z2-7]{26}[.x]`),
	)
	entry := rapid.Custom(func(t *rapid.T) string {
		return prefix.Draw(t, "prefix") + infix.Draw(t, "infix") + suffix.Draw(t, "suffix")
	})

	checks := 0

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom([]string{"foo", "foo.old-x", "v1.2"}).Draw(t, "name")
		entries := rapid.SliceOfNDistinct(entry, 0, 6, strings.ToLower).Draw(t, "entries")

		checks++

		base := filepath.Join(root, strconv.Itoa(checks))
		if err := os.Mkdir(base, 0o700); err != nil {
			t.Fatal(err)
		}

		defer func() { _ = os.RemoveAll(base) }()

		pattern := regexp.MustCompile(`^\.` + regexp.QuoteMeta(name) + `\.(tmp|old)-[A-Z2-7]{26}$`)
		want := []string{}

		for _, e := range entries {
			if err := os.Mkdir(filepath.Join(base, e), 0o700); err != nil {
				t.Fatal(err)
			}

			if pattern.MatchString(e) {
				want = append(want, filepath.Join(base, e))
			}
		}

		slices.Sort(want)

		got, err := workflow.StagingLeftovers(base, name)
		if err != nil {
			t.Fatalf("StagingLeftovers(%q) error = %v", name, err)
		}

		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("StagingLeftovers(%q) mismatch (-want +got):\n%s", name, diff)
		}
	})
}
