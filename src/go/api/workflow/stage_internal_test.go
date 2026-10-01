package workflow

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// swapTestWrite creates path, and any missing parents, with content.
func swapTestWrite(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// swapTestFailRenames replaces rename until the test ends. The calls numbered
// in fail, counting from 1, return an [os.LinkError] that wraps [syscall.EIO]
// without renaming anything. Every other call runs [os.Rename].
func swapTestFailRenames(t *testing.T, fail ...int) {
	t.Helper()

	orig := rename
	t.Cleanup(func() { rename = orig })

	calls := 0
	rename = func(oldpath, newpath string) error {
		calls++

		if slices.Contains(fail, calls) {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EIO}
		}

		return os.Rename(oldpath, newpath)
	}
}

// swapTestFailRemovals replaces removeAll until the test ends. A call for a
// path that contains part returns an [os.PathError] that wraps [syscall.EIO]
// without removing anything. Every other call runs [os.RemoveAll].
func swapTestFailRemovals(t *testing.T, part string) {
	t.Helper()

	orig := removeAll
	t.Cleanup(func() { removeAll = orig })

	removeAll = func(path string) error {
		if strings.Contains(path, part) {
			return &os.PathError{Op: "unlinkat", Path: path, Err: syscall.EIO}
		}

		return os.RemoveAll(path)
	}
}

// TestStageInjectsReportsFailedCleanup fails a staging after the copy has
// started, once in the copy and once in the swap, with and without a failure
// to remove the staging copy. A failed removal is added to the error, on the
// same line, and the error names the copy left behind; a removal that works
// leaves the error as it is.
func TestStageInjectsReportsFailedCleanup(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, src string)
		wantMsg string // part of the error that failed the staging
	}{
		{
			name: "the copy fails",
			setup: func(t *testing.T, src string) {
				t.Helper()

				if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantMsg: "unsupported file type",
		},
		{
			name: "the swap fails",
			setup: func(t *testing.T, _ string) {
				t.Helper()
				swapTestFailRenames(t, 2)
			},
			wantMsg: "moving staged injects to ",
		},
	}

	for _, tt := range tests {
		for _, failRemoval := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, removal fails=%t", tt.name, failRemoval), func(t *testing.T) {
				root := t.TempDir()
				base := filepath.Join(root, "injects")
				src := filepath.Join(root, "phenix-injects")
				swapTestWrite(t, filepath.Join(base, "foo", "keep.txt"), "keep")
				swapTestWrite(t, filepath.Join(src, "new.txt"), "new")
				tt.setup(t, src)

				if failRemoval {
					swapTestFailRemovals(t, ".foo.tmp-")
				}

				files, err := StageInjects(src, base, "foo")
				if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
					t.Fatalf("StageInjects err = %v, want an error containing %q", err, tt.wantMsg)
				}

				if strings.Contains(err.Error(), "\n") {
					t.Errorf("StageInjects err = %q, want one line", err)
				}

				if files != 0 {
					t.Errorf("files = %d, want 0 on error", files)
				}

				if keep, readErr := os.ReadFile(filepath.Join(base, "foo", "keep.txt")); readErr != nil || string(keep) != "keep" {
					t.Errorf("base/foo/keep.txt = %q (err %v), want the previous copy in place", keep, readErr)
				}

				tmps, _ := filepath.Glob(filepath.Join(base, ".foo.tmp-*"))

				if !failRemoval {
					if strings.Contains(err.Error(), "removing the staging copy") {
						t.Errorf("StageInjects err = %v, want no word of a removal that worked", err)
					}

					if len(tmps) != 0 {
						t.Errorf("staging copies left in base: %v", tmps)
					}

					return
				}

				if !errors.Is(err, syscall.EIO) {
					t.Errorf("StageInjects err = %v, want errors.Is(err, syscall.EIO)", err)
				}

				if len(tmps) != 1 {
					t.Fatalf("staging copies in base = %v, want exactly the one that was not removed", tmps)
				}

				// The error holds the resolved path, so compare base names.
				if want := "; removing the staging copy "; !strings.Contains(err.Error(), want) ||
					!strings.Contains(err.Error(), filepath.Base(tmps[0])+": ") {
					t.Errorf("StageInjects err = %v, want it to contain %q and name %s", err, want, filepath.Base(tmps[0]))
				}
			})
		}
	}
}

// TestStageInjectsReportsKeptPreviousCopy fails the removal of the previous
// copy, which root, unlike a test user, cannot be made to fail through file
// permissions. The new tree is then in place, so the count is returned
// together with an error that names the previous copy.
func TestStageInjectsReportsKeptPreviousCopy(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "injects")
	src := filepath.Join(root, "phenix-injects")
	swapTestWrite(t, filepath.Join(base, "foo", "keep.txt"), "keep")
	swapTestWrite(t, filepath.Join(src, "new.txt"), "new")
	swapTestWrite(t, filepath.Join(src, "sub", "more.txt"), "more")
	swapTestFailRemovals(t, ".foo.old-")

	files, err := StageInjects(src, base, "foo")
	if !errors.Is(err, ErrPreviousCopyKept) || !errors.Is(err, syscall.EIO) {
		t.Fatalf("StageInjects err = %v, want an EIO error that wraps ErrPreviousCopyKept", err)
	}

	if files != 2 {
		t.Errorf("files = %d, want 2", files)
	}

	if got, readErr := os.ReadFile(filepath.Join(base, "foo", "new.txt")); readErr != nil || string(got) != "new" {
		t.Errorf("base/foo/new.txt = %q (err %v), want the new copy in place", got, readErr)
	}

	if tmps, _ := filepath.Glob(filepath.Join(base, ".foo.tmp-*")); len(tmps) != 0 {
		t.Errorf("staging copies left in base: %v", tmps)
	}

	olds, _ := filepath.Glob(filepath.Join(base, ".foo.old-*"))
	if len(olds) != 1 {
		t.Fatalf("previous copies in base = %v, want exactly one", olds)
	}

	if got, readErr := os.ReadFile(filepath.Join(olds[0], "keep.txt")); readErr != nil || string(got) != "keep" {
		t.Errorf("%s/keep.txt = %q (err %v), want the previous copy", olds[0], got, readErr)
	}

	// The error holds the resolved path, so compare base names.
	for _, part := range []string{"staged injects in ", "but could not remove the previous injects copy ", filepath.Base(olds[0]) + ": "} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("StageInjects err = %v, want it to contain %q", err, part)
		}
	}
}

// TestSwapDirRenameFailures fails the renames that a test user cannot make
// fail in a directory it has just written to. With a previous copy,
// StageInjects renames base/foo to .foo.old-*, then .foo.tmp-* to base/foo,
// and only if that fails, .foo.old-* back to base/foo. Without one, the first
// rename finds nothing to move and the second moves the copy into place.
func TestSwapDirRenameFailures(t *testing.T) {
	tests := []struct {
		name     string
		existing bool     // base/foo holds keep.txt before staging
		fail     []int    // rename calls that fail, counting from 1
		wantMsg  []string // parts of the error message
		wantKeep bool     // base/foo still holds keep.txt afterwards
		wantOld  bool     // keep.txt is left in a .foo.old-* sibling
	}{
		{
			name:     "moving the previous copy aside",
			existing: true,
			fail:     []int{1},
			wantMsg:  []string{"foo aside: "},
			wantKeep: true,
		},
		{
			name:     "moving the staged copy into place",
			existing: true,
			fail:     []int{2},
			wantMsg:  []string{"moving staged injects to "},
			wantKeep: true,
		},
		{
			name:     "putting the previous copy back",
			existing: true,
			fail:     []int{2, 3},
			wantMsg: []string{
				"moving staged injects to ",
				": input/output error; putting the previous copy back failed; it is kept in ",
			},
			wantOld: true,
		},
		{
			name:    "moving the first staged copy into place",
			fail:    []int{2},
			wantMsg: []string{"moving staged injects to ", "/foo: rename "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			base := filepath.Join(root, "injects")
			src := filepath.Join(root, "phenix-injects")
			swapTestWrite(t, filepath.Join(src, "new.txt"), "new")

			if tt.existing {
				swapTestWrite(t, filepath.Join(base, "foo", "keep.txt"), "keep")
			}

			swapTestFailRenames(t, tt.fail...)

			files, err := StageInjects(src, base, "foo")
			if !errors.Is(err, syscall.EIO) {
				t.Fatalf("StageInjects err = %v, want an EIO error", err)
			}

			if strings.Contains(err.Error(), "\n") {
				t.Errorf("StageInjects err = %q, want one line", err)
			}

			for _, part := range tt.wantMsg {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("StageInjects err = %v, want it to contain %q", err, part)
				}
			}

			if files != 0 {
				t.Errorf("files = %d, want 0 on error", files)
			}

			keep, readErr := os.ReadFile(filepath.Join(base, "foo", "keep.txt"))
			if tt.wantKeep && (readErr != nil || string(keep) != "keep") {
				t.Errorf("base/foo/keep.txt = %q (err %v), want the previous copy in place", keep, readErr)
			}

			if !tt.wantKeep && !errors.Is(readErr, fs.ErrNotExist) {
				t.Errorf("base/foo/keep.txt err = %v, want base/foo to be missing", readErr)
			}

			if _, err := os.Lstat(filepath.Join(base, "foo", "new.txt")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("part of the failed stage reached base/foo (err %v)", err)
			}

			if tmps, _ := filepath.Glob(filepath.Join(base, ".foo.tmp-*")); len(tmps) != 0 {
				t.Errorf("staging copies left in base: %v", tmps)
			}

			olds, _ := filepath.Glob(filepath.Join(base, ".foo.old-*"))
			if !tt.wantOld {
				if len(olds) != 0 {
					t.Errorf("previous copies left in base: %v", olds)
				}

				return
			}

			if len(olds) != 1 {
				t.Fatalf("previous copies in base = %v, want exactly one", olds)
			}

			if got, err := os.ReadFile(filepath.Join(olds[0], "keep.txt")); err != nil || string(got) != "keep" {
				t.Errorf("%s/keep.txt = %q (err %v), want the previous copy", olds[0], got, err)
			}

			// The error must say where the previous copy is. It holds the
			// resolved path (/private/var/… on macOS), so compare base names.
			if !strings.Contains(err.Error(), filepath.Base(olds[0])) {
				t.Errorf("StageInjects err = %v, want it to name %s", err, filepath.Base(olds[0]))
			}
		})
	}
}

// TestRandomSuffixMatchesRandText checks that isRandomSuffix accepts what
// [rand.Text] returns, so StagingLeftovers finds the siblings StageInjects
// names with it.
func TestRandomSuffixMatchesRandText(t *testing.T) {
	for range 100 {
		if text := rand.Text(); !isRandomSuffix(text) {
			t.Fatalf("isRandomSuffix(%q) = false, want true for a rand.Text result", text)
		}
	}
}

// TestWithSecondError checks that a staging error with a second error stays
// on one line, and that [errors.Is] and [errors.As] find both.
func TestWithSecondError(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: "a", Err: fs.ErrPermission}
	second := &os.LinkError{Op: "rename", Old: "b", New: "c", Err: syscall.EIO}

	if got := withSecondError(cause, nil); !errors.Is(got, fs.ErrPermission) || got.Error() != "open a: permission denied" {
		t.Errorf("withSecondError(cause, nil) = %v, want the cause as it is", got)
	}

	err := withSecondError(cause, second)

	if want := "open a: permission denied; rename b c: input/output error"; err.Error() != want {
		t.Errorf("withSecondError() = %q, want %q", err, want)
	}

	var pathErr *os.PathError

	var linkErr *os.LinkError

	if !errors.As(err, &pathErr) || !errors.As(err, &linkErr) {
		t.Errorf("withSecondError() = %v, want errors.As to find both the *os.PathError and the *os.LinkError", err)
	}

	if !errors.Is(err, fs.ErrPermission) || !errors.Is(err, syscall.EIO) {
		t.Errorf("withSecondError() = %v, want errors.Is to find both fs.ErrPermission and syscall.EIO", err)
	}
}

// TestWithSecondErrorJoinsSeveral checks the form copyFile uses: the errors
// that are not nil, the cause among them, stay on one line in order, and
// [errors.Is] finds each. A copy that worked with a close that failed gives
// the close error alone, and no error at all gives nil.
func TestWithSecondErrorJoinsSeveral(t *testing.T) {
	copyErr := &os.PathError{Op: "write", Path: "a", Err: syscall.ENOSPC}
	closeErr := &os.PathError{Op: "close", Path: "a", Err: syscall.EIO}
	chmodErr := &os.PathError{Op: "chmod", Path: "a", Err: fs.ErrPermission}

	err := withSecondError(copyErr, closeErr, chmodErr)

	want := "write a: no space left on device; close a: input/output error; chmod a: permission denied"
	if err == nil || err.Error() != want {
		t.Fatalf("withSecondError() = %v, want %q", err, want)
	}

	for _, target := range []error{syscall.ENOSPC, syscall.EIO, fs.ErrPermission} {
		if !errors.Is(err, target) {
			t.Errorf("withSecondError() = %v, want errors.Is to find %v", err, target)
		}
	}

	if got := withSecondError(nil, closeErr, nil); !errors.Is(got, syscall.EIO) || got.Error() != "close a: input/output error" {
		t.Errorf("withSecondError(nil, closeErr, nil) = %v, want the close error as it is", got)
	}

	if got := withSecondError(nil, nil, nil); got != nil {
		t.Errorf("withSecondError(nil, nil, nil) = %v, want nil", got)
	}
}
