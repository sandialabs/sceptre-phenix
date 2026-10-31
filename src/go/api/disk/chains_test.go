package disk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	dct "phenix/util/diskchain/diskchaintest"
)

// BrokenImages counts the images the disk list finds, leaving out folders it skips and files without an image
// extension, and names each one qemu cannot open by its path in the folder.
func TestBrokenImages(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	dct.Chain(t, filepath.Join(dir, "whole.qc2"), filepath.Join(dir, "base.qcow2"))
	dct.WriteQcow2(t, filepath.Join(dir, "sub", "over.qc2"), dct.Qcow2{Backing: "../ro_over.qc2"})
	dct.WriteQcow2(t, filepath.Join(dir, "a.qc2"), dct.Qcow2{Backing: filepath.Join(dir, "gone.qc2")})
	dct.WriteFile(t, filepath.Join(dir, "cd.iso"), []byte("iso"))
	// left out: not an image extension, a hidden folder, an experiment's files folder
	dct.WriteQcow2(t, filepath.Join(dir, "notes.txt"), dct.Qcow2{Backing: "/gone"})
	dct.WriteQcow2(t, filepath.Join(dir, ".trash", "x.qc2"), dct.Qcow2{Backing: "/gone"})
	dct.WriteQcow2(t, filepath.Join(dir, "exp", "files", "y.qc2"), dct.Qcow2{Backing: "/gone"})

	if err := os.Symlink(filepath.Join(dir, "a.qc2"), filepath.Join(dir, "link.qc2")); err != nil {
		t.Fatalf("os.Symlink() error = %v", err)
	}

	got, err := BrokenImages(t.Context(), dir)
	if err != nil {
		t.Fatalf("BrokenImages() error = %v", err)
	}

	want := ChainScan{
		Images:  6,
		Checked: 6,
		Broken: []BrokenImage{
			{Path: "a.qc2", Problem: "backing file " + filepath.Join(dir, "gone.qc2") + " is missing"},
			{Path: "link.qc2", Problem: "backing file " + filepath.Join(dir, "gone.qc2") + " is missing"},
			{Path: "sub/over.qc2", Problem: "backing file " + filepath.Join(dir, "ro_over.qc2") + " is missing"},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("BrokenImages() mismatch (-want +got):\n%s", diff)
	}
}

// A done context ends the scan: either before the listing finishes, or with no image checked.
func TestBrokenImagesWhenCanceled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dct.WriteQcow2(t, filepath.Join(dir, "a.qc2"), dct.Qcow2{Backing: "/gone"})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := BrokenImages(ctx, dir)

	switch {
	case err != nil && !errors.Is(err, context.Canceled):
		t.Errorf("BrokenImages() error = %v, want context.Canceled", err)
	case err == nil && got.Checked != 0:
		t.Errorf("BrokenImages() = %+v, want no image checked", got)
	}
}

// A folder that does not exist fails the scan.
func TestBrokenImagesMissingFolder(t *testing.T) {
	t.Parallel()

	_, err := BrokenImages(t.Context(), filepath.Join(t.TempDir(), "none"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("BrokenImages() error = %v, want os.ErrNotExist", err)
	}
}
