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

// A folder that does not exist fails the scan; an empty folder, or a file given as the folder, holds no image.
func TestBrokenImagesEdgeFolders(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dir     func(t *testing.T) string
		wantErr error
	}{
		"a missing folder": {
			dir:     func(t *testing.T) string { t.Helper(); return filepath.Join(t.TempDir(), "none") },
			wantErr: os.ErrNotExist,
		},
		"an empty folder": {dir: func(t *testing.T) string { t.Helper(); return t.TempDir() }},
		"a file": {
			dir: func(t *testing.T) string {
				t.Helper()

				path := filepath.Join(t.TempDir(), "a.qc2")
				dct.WriteQcow2(t, path, dct.Qcow2{Backing: "/gone"})

				return path
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := BrokenImages(t.Context(), tt.dir(t))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("BrokenImages(%s) error = %v, want %v", name, err, tt.wantErr)
			}

			if diff := cmp.Diff(ChainScan{}, got); diff != "" {
				t.Errorf("BrokenImages(%s) mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// formatOf names only what the image's own header shows: a refusal or external data file at the image, an old or
// unchecked format, corruption and recent features. A dirty image, or anything a backing file shows, is no format issue.
func TestFormatOf(t *testing.T) {
	t.Parallel()

	const (
		image = "/f/a.qc2"
		base  = "/f/base.qc2"
	)

	note := func(path string, kind diskchain.NoteKind) diskchain.Note {
		return diskchain.Note{Path: path, Kind: kind}
	}

	tests := map[string]struct {
		res    diskchain.Result
		want   FormatImage
		wantOK bool
	}{
		"a whole chain": {res: diskchain.Result{Members: []string{image}}},
		"a dirty image": {res: diskchain.Result{Members: []string{image}, Notes: []diskchain.Note{note(image, diskchain.Dirty)}}},
		"a refused image": {
			res:    diskchain.Result{Members: []string{image}, Problem: diskchain.LUKSEncrypted, At: image},
			want:   FormatImage{Problem: image + " uses LUKS encryption, and phenix cannot give qemu its key"},
			wantOK: true,
		},
		"a refused backing file": {
			res: diskchain.Result{Members: []string{image, base}, Problem: diskchain.BadVersion, At: base, Detail: "4"},
		},
		"an external data file": {
			res:    diskchain.Result{Members: []string{image}, Problem: diskchain.DataFile, At: image},
			want:   FormatImage{Needs: []QemuNeed{{Feature: "an external data file", Version: minDataFileQemu}}},
			wantOK: true,
		},
		"an external data file of a backing file": {
			res: diskchain.Result{Members: []string{image, base}, Problem: diskchain.DataFile, At: base},
		},
		"old and unchecked formats": {
			res: diskchain.Result{Members: []string{image}, Notes: []diskchain.Note{
				note(image, diskchain.Qcow2V2), note(base, diskchain.QcowV1),
			}},
			want:   FormatImage{Kind: diskchain.Qcow2V2},
			wantOK: true,
		},
		"a qed image": {
			res:    diskchain.Result{Members: []string{image}, Notes: []diskchain.Note{note(image, diskchain.QED)}},
			want:   FormatImage{Kind: diskchain.QED},
			wantOK: true,
		},
		"a corrupt image that needs a recent qemu": {
			res: diskchain.Result{Members: []string{image}, Notes: []diskchain.Note{
				note(image, diskchain.Corrupt), note(image, diskchain.ExtendedL2), note(base, diskchain.Zstd),
			}},
			want: FormatImage{
				Problem: "qemu marked " + image + " as corrupt",
				Needs:   []QemuNeed{{Feature: "extended L2 entries", Version: "5.2"}},
			},
			wantOK: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := formatOf(image, tt.res)

			tt.want.Path = image
			if diff := cmp.Diff(tt.want, got); diff != "" || ok != tt.wantOK {
				t.Errorf("formatOf(%s) = _, %t, want %t; mismatch (-want +got):\n%s", image, ok, tt.wantOK, diff)
			}
		})
	}
}
