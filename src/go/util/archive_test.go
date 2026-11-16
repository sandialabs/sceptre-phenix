package util_test

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util"
)

// readArchive returns the regular files of the gzipped tar archive at path, by name.
func readArchive(t *testing.T, path string) map[string]string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening archive: %v", err)
	}

	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("reading gzip header: %v", err)
	}

	files := map[string]string{}
	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}

		if err != nil {
			t.Fatalf("reading tar header: %v", err)
		}

		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading %s from the archive: %v", header.Name, err)
		}

		files[header.Name] = string(body)
	}
}

// CreateArchive keeps the path of each file below root and leaves out folders.
func TestCreateArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	want := map[string]string{
		"top.txt":           "top",
		"empty.txt":         "",
		"sub/nested.txt":    "nested",
		"sub/deeper/a.conf": "key = value\n",
	}

	for name, body := range want {
		path := filepath.Join(root, filepath.FromSlash(name))

		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating folder for %s: %v", name, err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	if err := os.Mkdir(filepath.Join(root, "no-files"), 0o750); err != nil {
		t.Fatalf("creating an empty folder: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.tgz")

	if err := util.CreateArchive(root, out); err != nil {
		t.Fatalf("CreateArchive(%q, %q) = %v, want nil", root, out, err)
	}

	if diff := cmp.Diff(want, readArchive(t, out)); diff != "" {
		t.Errorf("CreateArchive(%q) files mismatch (-want +got):\n%s", root, diff)
	}
}

func TestCreateArchiveEmptyRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.tgz")

	if err := util.CreateArchive(root, out); err != nil {
		t.Fatalf("CreateArchive(%q, %q) = %v, want nil", root, out, err)
	}

	if diff := cmp.Diff(map[string]string{}, readArchive(t, out)); diff != "" {
		t.Errorf("CreateArchive(%q) files mismatch (-want +got):\n%s", root, diff)
	}
}

func TestCreateArchiveFails(t *testing.T) {
	t.Parallel()

	unreadable := t.TempDir()
	if err := os.WriteFile(filepath.Join(unreadable, "secret"), []byte("x"), 0o000); err != nil {
		t.Fatalf("writing an unreadable file: %v", err)
	}

	tests := []struct {
		name  string
		root  string
		out   string
		root0 bool // the case cannot fail for the root user
	}{
		{name: "output folder missing", root: t.TempDir(), out: filepath.Join(t.TempDir(), "missing", "out.tgz")},
		{name: "root missing", root: filepath.Join(t.TempDir(), "missing"), out: filepath.Join(t.TempDir(), "out.tgz")},
		{name: "file not readable", root: unreadable, out: filepath.Join(t.TempDir(), "out.tgz"), root0: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.root0 && os.Geteuid() == 0 {
				t.Skip("the root user reads every file")
			}

			if err := util.CreateArchive(tt.root, tt.out); err == nil {
				t.Errorf("CreateArchive(%q, %q) = nil, want an error", tt.root, tt.out)
			}
		})
	}
}
