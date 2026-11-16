package shell_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/shell"
)

const testPrefix = "phenix-app-"

func writeFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode)
	if err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	// WriteFile applies the umask, so set the mode again.
	err = os.Chmod(path, mode)
	if err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

// FindCommandsWithPrefix walks each PATH folder in Go. It must not depend on
// GNU find, and a missing PATH folder must not hide the other folders.
func TestFindCommandsWithPrefix(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	targets := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	writeFile(t, filepath.Join(first, testPrefix+"alpha"), 0o755)
	writeFile(t, filepath.Join(first, testPrefix+"dup"), 0o700)
	writeFile(t, filepath.Join(first, testPrefix+"noexec"), 0o644)
	writeFile(t, filepath.Join(first, "other-tool"), 0o755)
	writeFile(t, filepath.Join(first, testPrefix), 0o755)

	err := os.Mkdir(filepath.Join(first, testPrefix+"folder"), 0o755)
	if err != nil {
		t.Fatalf("making folder: %v", err)
	}

	writeFile(t, filepath.Join(second, testPrefix+"dup"), 0o755)
	writeFile(t, filepath.Join(second, testPrefix+"bravo"), 0o755)

	// A non-executable file in an earlier folder does not hide a later one.
	writeFile(t, filepath.Join(second, testPrefix+"noexec"), 0o755)

	writeFile(t, filepath.Join(targets, "real"), 0o755)
	writeFile(t, filepath.Join(targets, "real-noexec"), 0o644)

	err = os.Symlink(filepath.Join(targets, "real"), filepath.Join(second, testPrefix+"link"))
	if err != nil {
		t.Fatalf("making symlink: %v", err)
	}

	err = os.Symlink(filepath.Join(targets, "real-noexec"), filepath.Join(second, testPrefix+"link-noexec"))
	if err != nil {
		t.Fatalf("making symlink: %v", err)
	}

	err = os.Symlink(filepath.Join(targets, "gone"), filepath.Join(second, testPrefix+"dangling"))
	if err != nil {
		t.Fatalf("making symlink: %v", err)
	}

	t.Setenv("PATH", strings.Join([]string{missing, first, "", second}, string(os.PathListSeparator)))

	got := shell.FindCommandsWithPrefix(testPrefix)
	want := []string{"alpha", "dup", "bravo", "link", "noexec"}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FindCommandsWithPrefix() mismatch (-want +got):\n%s", diff)
	}
}

// FindCommandsWithPrefix lists only the files that the current user can
// execute. An execute bit for a class that does not include the user is not
// sufficient. Root can execute a file with any execute bit, so the test skips.
func TestFindCommandsWithPrefixUserAccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root passes access(2) X_OK for a file with any execute bit")
	}

	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, testPrefix+"owner"), 0o700)
	writeFile(t, filepath.Join(dir, testPrefix+"group-only"), 0o610)
	writeFile(t, filepath.Join(dir, testPrefix+"other-only"), 0o601)

	t.Setenv("PATH", dir)

	got := shell.FindCommandsWithPrefix(testPrefix)
	want := []string{"owner"}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FindCommandsWithPrefix() mismatch (-want +got):\n%s", diff)
	}
}

// FindCommandsWithPrefix returns nil when PATH is empty or has no match.
func TestFindCommandsWithPrefixNone(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "empty PATH", path: ""},
		{name: "only missing folders", path: filepath.Join(t.TempDir(), "gone")},
		{name: "no match", path: t.TempDir()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PATH", tt.path)

			got := shell.FindCommandsWithPrefix(testPrefix)
			if got != nil {
				t.Errorf("FindCommandsWithPrefix() = %q, want nil", got)
			}
		})
	}
}

// For any set of files across PATH folders, the result holds each executable
// prefixed name one time, in PATH order and then folder (sorted) order.
func TestFindCommandsWithPrefixProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dirCount := rapid.IntRange(1, 3).Draw(rt, "dirs")
		names := rapid.SliceOfDistinct(rapid.StringMatching(`[a-c]{1,2}`), func(s string) string { return s }).
			Draw(rt, "names")

		var want []string

		dirs := make([]string, 0, dirCount)

		seen := make(map[string]bool)

		for range dirCount {
			dir := t.TempDir()
			dirs = append(dirs, dir)

			var exec []string

			for _, name := range names {
				if !rapid.Bool().Draw(rt, "present") {
					continue
				}

				mode := os.FileMode(0o644)
				if rapid.Bool().Draw(rt, "exec") {
					mode = 0o755
					exec = append(exec, name)
				}

				path := filepath.Join(dir, testPrefix+name)

				err := os.WriteFile(path, nil, mode)
				if err == nil {
					err = os.Chmod(path, mode)
				}

				if err != nil {
					rt.Fatalf("writing %s: %v", path, err)
				}
			}

			slices.Sort(exec)

			for _, name := range exec {
				if !seen[name] {
					seen[name] = true
					want = append(want, name)
				}
			}
		}

		t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))

		got := shell.FindCommandsWithPrefix(testPrefix)

		if diff := cmp.Diff(want, got); diff != "" {
			rt.Errorf("FindCommandsWithPrefix() mismatch (-want +got):\n%s", diff)
		}
	})
}
