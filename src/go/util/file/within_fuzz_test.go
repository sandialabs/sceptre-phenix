package file

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// For absolute paths WithinDir follows TestWithinDirMatchesSegmentModel's segment model, and it never places
// an absolute path under a relative directory or the reverse.
func FuzzWithinDir(f *testing.F) {
	for _, seed := range [][2]string{
		{"/f", "/f"}, {"/f", "/f/a.qc2"}, {"/f/", "/f/win/10/a.qc2"}, {"/f", "/f/win/../a.qc2"}, {"/f", "/f/..a.qc2"},
		{"/f", "/f/../a.qc2"}, {"/f", "/f/win/../../a.qc2"}, {"/f", "/f-other/a.qc2"}, {"/f", "/"}, {"/f", "a.qc2"},
		{"/", "/a"}, {"/f/./g", "/f//g/h"}, {"f", "f/g"}, {"..", "../x"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, dir, path string) {
		got := WithinDir(dir, path)

		switch dirAbs, pathAbs := filepath.IsAbs(dir), filepath.IsAbs(path); {
		case dirAbs && pathAbs:
			resolvedDir := withinResolve(strings.Split(dir, "/"))
			resolvedPath := withinResolve(strings.Split(path, "/"))

			want := len(resolvedPath) >= len(resolvedDir) && slices.Equal(resolvedPath[:len(resolvedDir)], resolvedDir)
			if got != want {
				t.Fatalf("WithinDir(%q, %q) = %t, want %t", dir, path, got, want)
			}
		case dirAbs != pathAbs:
			if got {
				t.Fatalf("WithinDir(%q, %q) = true, want false for one absolute and one relative path", dir, path)
			}
		}
	})
}
