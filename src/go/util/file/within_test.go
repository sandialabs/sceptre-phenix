package file

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestWithinDir(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		dir, path string
		want      bool
	}{
		{dir: "/f", path: "/f", want: true},
		{dir: "/f", path: "/f/a.qc2", want: true},
		{dir: "/f/", path: "/f/win/10/a.qc2", want: true},
		{dir: "/f", path: "/f/win/../a.qc2", want: true},
		{dir: "/f", path: "/f/..a.qc2", want: true},
		{dir: "/f", path: "/f/../a.qc2", want: false},
		{dir: "/f", path: "/f/win/../../a.qc2", want: false},
		{dir: "/f", path: "/f-other/a.qc2", want: false},
		{dir: "/f", path: "/", want: false},
		{dir: "/f", path: "a.qc2", want: false},
	} {
		if got := WithinDir(tc.dir, tc.path); got != tc.want {
			t.Errorf("WithinDir(%q, %q) = %t, want %t", tc.dir, tc.path, got, tc.want)
		}
	}
}

// genWithinSegments draws path segments that clean to themselves, to nothing or
// to a step up, and names that share a prefix with them.
func genWithinSegments(minLen, maxLen int) *rapid.Generator[[]string] {
	return rapid.SliceOfN(rapid.SampledFrom([]string{"f", "g", "f-g", "..f", "..", ".", ""}), minLen, maxLen)
}

// withinResolve applies segs to an absolute path's segments as [filepath.Clean] does.
func withinResolve(segs []string) []string {
	out := []string{}

	for _, s := range segs {
		switch s {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, s)
		}
	}

	return out
}

func TestWithinDirMatchesSegmentModel(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		dirSegs := genWithinSegments(0, 4).Draw(t, "dir")
		pathSegs := genWithinSegments(0, 8).Draw(t, "path")

		dir := "/" + strings.Join(dirSegs, "/")
		path := "/" + strings.Join(pathSegs, "/")
		resolvedDir, resolvedPath := withinResolve(dirSegs), withinResolve(pathSegs)
		want := len(resolvedPath) >= len(resolvedDir) && slices.Equal(resolvedPath[:len(resolvedDir)], resolvedDir)

		if got := WithinDir(dir, path); got != want {
			t.Fatalf("WithinDir(%q, %q) = %t, want %t", dir, path, got, want)
		}
	})
}
