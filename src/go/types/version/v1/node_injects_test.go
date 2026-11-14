package v1

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

type injectsTB interface {
	Helper()
	Fatal(args ...any)
}

func injectedMode(t injectsTB, src, perms string) fs.FileMode {
	t.Helper()

	if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}

	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	node := Node{InjectionsF: []*Injection{{SrcF: src, DstF: "/inject", PermissionsF: perms}}}
	node.FileInjects("/unused")

	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}

	return info.Mode().Perm()
}

// TestFileInjectsPermissions checks that FileInjects applies an inject's octal
// permissions to its source file and leaves the file alone when they do not
// parse as an unsigned octal number.
func TestFileInjectsPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		perms string
		want  fs.FileMode
	}{
		{name: "three digits", perms: "755", want: 0o755},
		{name: "leading zero", perms: "0640", want: 0o640},
		{name: "plus sign", perms: "+755", want: 0o755},
		{name: "empty", perms: "", want: 0o600},
		{name: "negative", perms: "-755", want: 0o600},
		{name: "plus and minus", perms: "+-75", want: 0o600},
		{name: "not octal", perms: "999", want: 0o600},
		{name: "too long", perms: "00755", want: 0o600},
		{name: "four digits", perms: "0444", want: 0o444},
		{name: "no access", perms: "0", want: 0},
		{name: "hex", perms: "0x1ff", want: 0o600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := filepath.Join(t.TempDir(), "inject")
			if got := injectedMode(t, src, tt.perms); got != tt.want {
				t.Errorf("FileInjects() with permissions %q left mode %o, want %o", tt.perms, got, tt.want)
			}
		})
	}
}

// TestFileInjectsPermissionsProperty checks that any permission bits written
// in octal, with or without a leading plus sign, end up on the source file.
func TestFileInjectsPermissionsProperty(t *testing.T) {
	t.Parallel()

	src := filepath.Join(t.TempDir(), "inject")

	rapid.Check(t, func(rt *rapid.T) {
		want := fs.FileMode(rapid.Uint32Range(0, 0o777).Draw(rt, "mode"))
		sign := rapid.SampledFrom([]string{"", "+"}).Draw(rt, "sign")
		perms := fmt.Sprintf("%s%o", sign, uint32(want))

		if got := injectedMode(rt, src, perms); got != want {
			rt.Fatalf("FileInjects() with permissions %q left mode %o, want %o", perms, got, want)
		}
	})
}

// TestNodeInjectsCommandArgs checks the minimega inject arguments FileInjects
// returns: a relative source is under baseDir, an absolute one is as given,
// and a source that is missing or has no permissions is still listed.
func TestNodeInjectsCommandArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		injects []*Injection
		want    string
	}{
		{name: "none", injects: nil, want: ""},
		{
			name:    "relative source",
			injects: []*Injection{{SrcF: "files/a.txt", DstF: "/etc/a.txt"}},
			want:    `"/base/files/a.txt":"/etc/a.txt"`,
		},
		{
			name:    "absolute source",
			injects: []*Injection{{SrcF: "/abs/b.txt", DstF: "/b.txt", PermissionsF: "0644"}},
			want:    `"/abs/b.txt":"/b.txt"`,
		},
		{
			name: "several, in order",
			injects: []*Injection{
				{SrcF: "a", DstF: "/a"},
				{SrcF: "/b", DstF: "/b dir/b"},
			},
			want: `"/base/a":"/a" "/b":"/b dir/b"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			node := Node{InjectionsF: tt.injects}
			if got := node.FileInjects("/base"); got != tt.want {
				t.Errorf("FileInjects(/base) = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestNodeInjectsRelativeSourcePermissions checks that FileInjects applies the
// permissions of a relative inject to baseDir/src, the path in the minimega
// inject command, and not to src under the working directory.
func TestNodeInjectsRelativeSourcePermissions(t *testing.T) {
	t.Parallel()

	const rel = "phenix-relative-inject-test/inject"

	if _, err := os.Stat(rel); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("relative path %s must not exist in the working directory: %v", rel, err)
	}

	tests := []struct {
		name  string
		perms string
		want  fs.FileMode
	}{
		{name: "three digits", perms: "755", want: 0o755},
		{name: "plus sign", perms: "+750", want: 0o750},
		{name: "four digits", perms: "0444", want: 0o444},
		{name: "empty", perms: "", want: 0o600},
		{name: "not octal", perms: "999", want: 0o600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			baseDir := t.TempDir()
			src := filepath.Join(baseDir, rel)

			if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(src, nil, 0o600); err != nil {
				t.Fatal(err)
			}

			node := Node{InjectionsF: []*Injection{{SrcF: rel, DstF: "/inject", PermissionsF: tt.perms}}}

			wantArgs := fmt.Sprintf(`"%s":"/inject"`, src)
			if diff := cmp.Diff(wantArgs, node.FileInjects(baseDir)); diff != "" {
				t.Errorf("FileInjects() mismatch (-want +got):\n%s", diff)
			}

			info, err := os.Stat(src)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, info.Mode().Perm()); diff != "" {
				t.Errorf("mode of %s mismatch (-want +got):\n%s", src, diff)
			}
		})
	}
}
