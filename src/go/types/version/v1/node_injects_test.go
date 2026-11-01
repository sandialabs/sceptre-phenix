package v1

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
