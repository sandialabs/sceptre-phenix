package tmpl_test

import (
	"os"
	"path/filepath"
	"testing"

	"phenix/tmpl"
)

// TestRestoreAssetNested checks that an asset in a template subdirectory is
// restored into a directory that does not have that subdirectory yet.
func TestRestoreAssetNested(t *testing.T) {
	dir := t.TempDir()
	name := "miniccc/miniccc-scheduler.cmd"

	if err := tmpl.RestoreAsset(dir, name); err != nil {
		t.Fatalf("RestoreAsset(%q) error = %v", name, err)
	}

	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Errorf("RestoreAsset(%q) did not write the asset: %v", name, err)
	}
}
