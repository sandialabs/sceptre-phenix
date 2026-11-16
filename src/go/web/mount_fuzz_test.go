package web

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// However a request names a path in the mount, openInMount and statInMount
// never reach the folder beside it, through ".." or a symlink, and a file
// either opens or is refused as statInMount says.
func FuzzOpenInMount(f *testing.F) {
	for _, seed := range []string{
		"etc/hostname", "etc", "gone", "escape/secret", "escape", "../outside/secret", "..", ".", "",
		"etc/../../outside/secret", "escape/../etc/hostname", "/etc/passwd", "etc//hostname", "etc/hostname/",
	} {
		f.Add(seed)
	}

	base := mountTree(f)
	outside := filepath.Join(filepath.Dir(base), "outside")

	f.Fuzz(func(t *testing.T, p string) {
		if strings.ContainsRune(p, 0) {
			return
		}

		path := filepath.Join(base, p)

		info, statErr := statInMount(base, path)

		file, err := openInMount(base, path, os.O_RDONLY, 0)
		if (err == nil) != (statErr == nil) {
			t.Fatalf("openInMount(%q) error = %v, but statInMount error = %v, want both to agree", p, err, statErr)
		}

		if err != nil {
			return
		}

		defer file.Close()

		if rel, rerr := filepath.Rel(base, path); rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("openInMount(%q) opened %s, which is outside the mount", p, path)
		}

		if info.IsDir() {
			return
		}

		b, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("reading openInMount(%q): %v", p, err)
		}

		if strings.Contains(string(b), "not yours") {
			t.Fatalf("openInMount(%q) read %s's secret, want the folder beside the mount out of reach", p, outside)
		}
	})
}
