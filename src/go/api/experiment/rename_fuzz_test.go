package experiment

import (
	"slices"
	"strings"
	"testing"
)

// renamedBaseDir changes only the parts of the path that are the old name: a path without that part stays as it is,
// the other parts stay as they are, no part of the old name is left, and renaming back gives the path again.
func FuzzRenamedBaseDir(f *testing.F) {
	for _, seed := range [][3]string{
		{"/phenix/experiments/exp", "exp", "lab"}, {"/phenix/experiments/e", "e", "b"}, {"/exp/exp", "exp", "lab"},
		{"/data/exp-files", "exp", "lab"}, {"relative/exp/", "exp", "exp2"}, {"/a//b/", "", "x"}, {"", "", ""},
		{"/phenix/experiments/exp", "exp", "a/b"}, {"/x/lab/exp", "exp", "lab"},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}

	f.Fuzz(func(t *testing.T, dir, old, name string) {
		got := renamedBaseDir(dir, old, name)

		parts := strings.Split(dir, "/")

		if old == "" || !slices.Contains(parts, old) {
			if got != dir {
				t.Fatalf("renamedBaseDir(%q, %q, %q) = %q, want the path as it is", dir, old, name, got)
			}

			return
		}

		// a name that is empty, holds a slash or is already in the path cannot be told apart once in it
		if name == "" || name == old || strings.Contains(name, "/") || slices.Contains(parts, name) {
			return
		}

		gotParts := strings.Split(got, "/")
		if slices.Contains(gotParts, old) || len(gotParts) != len(parts) {
			t.Fatalf("renamedBaseDir(%q, %q, %q) = %q, want as many parts and no part %q left", dir, old, name, got, old)
		}

		for i, part := range parts {
			if part != old && gotParts[i] != part {
				t.Fatalf("renamedBaseDir(%q, %q, %q) = %q, want part %d %q as it is", dir, old, name, got, i, part)
			}
		}

		if back := renamedBaseDir(got, name, old); back != dir {
			t.Fatalf("renamedBaseDir(renamedBaseDir(%q, %q, %q) = %q, back) = %q, want %q", dir, old, name, got, back, dir)
		}
	})
}
