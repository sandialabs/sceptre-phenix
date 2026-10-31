package experiment

import (
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"phenix/util/file"
)

// cleanFilePath accepts exactly the paths io/fs calls valid but the root, whatever the bytes of each name,
// and every path it accepts stays inside the files directory.
func FuzzCleanFilePath(f *testing.F) {
	for _, seed := range []string{
		"scorch/run-0/out.json", "remote.log", "old.pcap", "sub/live.pcap", "..a.log", "a..b/c",
		"../../etc/passwd", "scorch/../../x", "/scorch/run-0/out.json", "", "scorch//run-0/out.json",
		"a/", ".", "./a", "a/.", "..", `..\evil`, "a*.log", "x #y", "a\x00b",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, filePath string) {
		got, err := cleanFilePath(filePath)

		// io/fs also wants UTF-8, which a Linux file name need not be
		if want := fs.ValidPath(strings.ToValidUTF8(filePath, "x")) && filePath != "."; (err == nil) != want {
			t.Fatalf("cleanFilePath(%q) = %q, %v, want accepted %t", filePath, got, err, want)
		}

		if err != nil {
			if !errors.Is(err, ErrInvalidFilePath) || got != "" {
				t.Fatalf("cleanFilePath(%q) = %q, %v, want \"\", %v", filePath, got, err, ErrInvalidFilePath)
			}

			return
		}

		const dir = "/phenix/images/exp/files"

		joined := path.Join(dir, got)
		if got != filePath || !filepath.IsLocal(got) || joined == dir || !file.WithinDir(dir, joined) {
			t.Fatalf("cleanFilePath(%q) = %q, want the same local path, under %s once joined (%q)", filePath, got, dir, joined)
		}
	})
}
