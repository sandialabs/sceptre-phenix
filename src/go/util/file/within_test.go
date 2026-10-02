package file

import "testing"

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
