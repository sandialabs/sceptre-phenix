package tmpl_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/tmpl"
)

// RestoreAsset makes the folders of an asset in a subfolder, so the file is written in an empty
// folder too.
func TestRestoreAssetMakesFolders(t *testing.T) {
	for _, name := range []string{"miniccc/miniccc-scheduler.cmd", "phenix-startup.ps1"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()

			if err := tmpl.RestoreAsset(dir, name); err != nil {
				t.Fatalf("RestoreAsset(%q, %q) = %v, want nil", dir, name, err)
			}

			got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil {
				t.Fatalf("reading the restored asset: %v", err)
			}

			want, err := os.ReadFile(filepath.Join("templates", filepath.FromSlash(name)))
			if err != nil {
				t.Fatalf("reading the template source: %v", err)
			}

			if diff := cmp.Diff(string(want), string(got)); diff != "" {
				t.Errorf("RestoreAsset(%q, %q) content mismatch (-want +got):\n%s", dir, name, diff)
			}
		})
	}
}

// RestoreAsset refuses a name that is not an embedded asset and writes nothing.
func TestRestoreAssetUnknownName(t *testing.T) {
	dir := t.TempDir()

	err := tmpl.RestoreAsset(dir, "missing/none.cmd")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("RestoreAsset(%q, missing/none.cmd) = %v, want fs.ErrNotExist", dir, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) = %v", dir, err)
	}

	if len(entries) != 0 {
		t.Errorf("RestoreAsset of an unknown name left %d entries in %q, want 0", len(entries), dir)
	}
}

// RestoreAsset fails when a folder in the path of the asset is a file.
func TestRestoreAssetFolderIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("writing a file: %v", err)
	}

	if err := tmpl.RestoreAsset(file, "miniccc/miniccc-scheduler.cmd"); err == nil {
		t.Errorf("RestoreAsset(%q, miniccc/miniccc-scheduler.cmd) = nil, want an error", file)
	}
}
