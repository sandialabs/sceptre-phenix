package disk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
)

// BrokenImages counts the images the disk list finds, leaving out folders it skips and files without an image
// extension, names each one qemu cannot open by its path in the folder, and gives the backing files it read.
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
		Backing: map[string][]string{"whole.qc2": {filepath.Join(dir, "base.qcow2")}},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("BrokenImages() mismatch (-want +got):\n%s", diff)
	}
}

// BrokenImages names each image whose own header shows an old or unsupported format, a reason qemu refuses it, or a
// feature that needs a recent qemu, and leaves out what only a backing file shows.
func TestBrokenImagesFormats(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }

	dct.WriteQcow2(t, path("kali.qc2"), dct.Qcow2{Version: 2})
	dct.WriteQcow(t, path("ro_old.qc2"), dct.Qcow{})
	dct.WriteQcow2(t, path("old.qc2"), dct.Qcow2{Backing: "ro_old.qc2", BackingFormat: "qcow"})
	dct.WriteFile(t, path("win.qcow2"), []byte("KDMV"))
	dct.WriteQcow2(t, path("aes.qc2"), dct.Qcow2{Encryption: 1})
	dct.WriteQcow2(t, path("corrupt.qc2"), dct.Qcow2{Features: 1<<1 | 1})
	dct.WriteQcow2(t, path("new.qc2"), dct.Qcow2{Zstd: true, Features: 1 << 4})
	dct.WriteQcow2(t, path("data.qc2"), dct.Qcow2{DataFile: true})
	dct.WriteQcow2(t, path("v3.qc2"), dct.Qcow2{})
	dct.WriteFile(t, path("raw.img.qc2"), []byte("raw"))
	dct.WriteQcow(t, path("base.qcow"), dct.Qcow{})
	dct.WriteQcow2(t, path("top.qc2"), dct.Qcow2{Backing: "base.qcow", BackingFormat: "qcow"})

	got, err := BrokenImages(t.Context(), dir)
	if err != nil {
		t.Fatalf("BrokenImages() error = %v", err)
	}

	want := []FormatImage{
		{Path: "aes.qc2", Problem: path("aes.qc2") + " uses the old qcow AES encryption, which qemu does not open for VMs"},
		{Path: "base.qcow", Kind: diskchain.QcowV1},
		{Path: "corrupt.qc2", Problem: "qemu marked " + path("corrupt.qc2") + " as corrupt"},
		{Path: "data.qc2", Needs: []QemuNeed{{Feature: "an external data file", Version: "4.0"}}},
		{Path: "kali.qc2", Kind: diskchain.Qcow2V2},
		{Path: "new.qc2", Needs: []QemuNeed{
			{Feature: "zstd compression", Version: "5.1"}, {Feature: "extended L2 entries", Version: "5.2"},
		}},
		{Path: "ro_old.qc2", Kind: diskchain.QcowV1},
		{Path: "win.qcow2", Kind: diskchain.VMDK},
	}

	if diff := cmp.Diff(want, got.Formats); diff != "" {
		t.Errorf("BrokenImages() formats mismatch (-want +got):\n%s", diff)
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
