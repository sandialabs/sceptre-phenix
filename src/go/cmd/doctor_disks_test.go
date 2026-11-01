package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/disk"
	dct "phenix/util/diskchain/diskchaintest"
)

// The disk-chains check reads the images in minimega's files directory, or <base-dir.phenix>/images without one, and
// warns about each image with a broken backing chain, naming at most five.
func TestDiskChainsCheck(t *testing.T) {
	t.Parallel()

	overlay := func(t *testing.T, path, backing string) {
		t.Helper()
		dct.WriteQcow2(t, path, dct.Qcow2{Backing: backing})
	}

	scanOf := func(scan disk.ChainScan, err error) func(context.Context, string) (disk.ChainScan, error) {
		return func(context.Context, string) (disk.ChainScan, error) { return scan, err }
	}

	tests := map[string]struct {
		setup      func(t *testing.T, files, phenix string)
		noMinimega bool                                                  // minimega gave no flags
		images     func(context.Context, string) (disk.ChainScan, error) // in place of disk.BrokenImages
		want       func(files, phenix string) doctorCheck
		wantHint   string
	}{
		"no images": {
			want: func(files, _ string) doctorCheck { return passCheck("no images in " + files) },
		},
		"every chain whole": {
			setup: func(t *testing.T, files, _ string) {
				t.Helper()
				dct.Chain(t, filepath.Join(files, "a.qc2"), filepath.Join(files, "base.qc2"))
				dct.WriteFile(t, filepath.Join(files, "notes.txt"), []byte("not an image"))
			},
			want: func(files, _ string) doctorCheck {
				return passCheck("2 images in " + files + ", and every backing chain is whole")
			},
		},
		"one broken": {
			setup: func(t *testing.T, files, _ string) {
				t.Helper()
				overlay(t, filepath.Join(files, "kali.qc2"), filepath.Join(files, "ro_kali.qc2"))
			},
			want: func(files, _ string) doctorCheck {
				return warnCheck(fmt.Sprintf("1 of 1 image in %s has a broken backing chain: "+
					"kali.qc2 (backing file %s/ro_kali.qc2 is missing)", files, files))
			},
			wantHint: "restore missing backing files",
		},
		// a relative backing name is found next to the image naming it
		"nested folder and a loop": {
			setup: func(t *testing.T, files, _ string) {
				t.Helper()
				overlay(t, filepath.Join(files, "win", "overlay.qc2"), "base.qc2")
				overlay(t, filepath.Join(files, "a.qc2"), filepath.Join(files, "b.qc2"))
				overlay(t, filepath.Join(files, "b.qc2"), filepath.Join(files, "a.qc2"))
			},
			want: func(files, _ string) doctorCheck {
				return warnCheck(fmt.Sprintf("3 of 3 images in %[1]s have a broken backing chain: "+
					"a.qc2 (backing chain loops back to %[1]s/a.qc2), b.qc2 (backing chain loops back to %[1]s/b.qc2), "+
					"win/overlay.qc2 (backing file %[1]s/win/base.qc2 is missing)", files))
			},
			wantHint: "restore missing backing files",
		},
		"more than five broken": {
			setup: func(t *testing.T, files, _ string) {
				t.Helper()
				for i := range 7 {
					overlay(t, filepath.Join(files, fmt.Sprintf("b%d.qc2", i)), "/gone.qc2")
				}

				dct.WriteFile(t, filepath.Join(files, "whole.qc2"), []byte("raw"))
			},
			want: func(files, _ string) doctorCheck {
				items := make([]string, 0, maxListedImages)
				for i := range 5 {
					items = append(items, fmt.Sprintf("b%d.qc2 (backing file /gone.qc2 is missing)", i))
				}

				return warnCheck(fmt.Sprintf("7 of 8 images in %s have a broken backing chain: %s, and 2 more",
					files, strings.Join(items, ", ")))
			},
			wantHint: "restore missing backing files",
		},
		"no files directory from minimega": {
			noMinimega: true,
			setup: func(t *testing.T, _, phenix string) {
				t.Helper()
				overlay(t, filepath.Join(phenix, "images", "a.qc2"), "/gone.qc2")
			},
			want: func(_, phenix string) doctorCheck {
				return warnCheck(fmt.Sprintf("1 of 1 image in %s/images has a broken backing chain: "+
					"a.qc2 (backing file /gone.qc2 is missing)", phenix))
			},
			wantHint: "restore missing backing files",
		},
		"images directory missing": {
			noMinimega: true,
			want: func(_, phenix string) doctorCheck {
				return doctorCheck{Name: "", Status: doctorSkip, Detail: "not checked, because " + phenix + "/images does not exist"}
			},
		},
		"out of time, none broken": {
			images: scanOf(disk.ChainScan{Images: 3, Checked: 1, Broken: nil}, nil),
			want: func(files, _ string) doctorCheck {
				return warnCheck("checked 1 of 3 images in " + files + " within 1s, and found none broken")
			},
			wantHint: "increase --timeout to check every image",
		},
		"out of time, one broken": {
			images: scanOf(disk.ChainScan{Images: 3, Checked: 2, Broken: []disk.BrokenImage{{Path: "a.qc2", Problem: "x"}}}, nil),
			want: func(files, _ string) doctorCheck {
				return warnCheck("checked 2 of 3 images in " + files + " within 1s, and found 1 broken: a.qc2 (x)")
			},
			wantHint: "increase --timeout to check every image",
		},
		"listing out of time": {
			images: scanOf(disk.ChainScan{}, fmt.Errorf("listing: %w", context.DeadlineExceeded)),
			want: func(files, _ string) doctorCheck {
				return warnCheck("did not list the images in " + files + " within 1s")
			},
			wantHint: "increase --timeout to check every image",
		},
		"cannot list": {
			images: scanOf(disk.ChainScan{}, fmt.Errorf("listing: %w", &fs.PathError{Op: "open", Path: "x", Err: fs.ErrPermission})),
			want: func(files, _ string) doctorCheck {
				return warnCheck("cannot list " + files + ": permission denied")
			},
			wantHint: "run phenix doctor as a user that can read the images folder",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)
			files, phenix := filepath.Join(dir, "files"), filepath.Join(dir, "phenix")

			if tt.setup != nil {
				tt.setup(t, files, phenix)
			}

			in := fakeDoctorInput(dir, nil)
			in.found = &doctorFound{minimegaUp: !tt.noMinimega, minimegaArgs: nil}

			if !tt.noMinimega {
				in.found.minimegaArgs = doctorMinimegaArgs(dir)
			}

			if tt.images != nil {
				in.images = tt.images
			}

			got := diskChainsCheck(t.Context(), in)

			if diff := cmp.Diff(tt.want(files, phenix), got); diff != "" {
				t.Errorf("diskChainsCheck() mismatch (-want +got):\n%s", diff)
			}

			if got.Status == doctorPass {
				return
			}

			if hint := diskChainsHint(got); !strings.HasPrefix(hint, tt.wantHint) || (tt.wantHint == "") != (hint == "") {
				t.Errorf("diskChainsHint() = %q, want it to start with %q", hint, tt.wantHint)
			}
		})
	}
}

// A files directory this user cannot read warns, naming the cause.
func TestDiskChainsCheckUnreadable(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}

	dir := newDoctorTestDir(t)
	files := filepath.Join(dir, "files")

	if err := os.Chmod(files, 0); err != nil {
		t.Fatalf("os.Chmod(%s) error = %v", files, err)
	}

	t.Cleanup(func() { _ = os.Chmod(files, 0o750) })

	in := fakeDoctorInput(dir, nil)
	in.found = &doctorFound{minimegaUp: true, minimegaArgs: doctorMinimegaArgs(dir)}

	want := warnCheck("cannot list " + files + ": permission denied")
	if diff := cmp.Diff(want, diskChainsCheck(t.Context(), in)); diff != "" {
		t.Errorf("diskChainsCheck() mismatch (-want +got):\n%s", diff)
	}
}

// The list names each of the first five images with its problem, then counts the rest.
func TestBrokenImagesListProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "n")

		images := make([]disk.BrokenImage, n)
		for i := range images {
			images[i] = disk.BrokenImage{Path: fmt.Sprintf("i%d.qc2", i), Problem: "p"}
		}

		got := strings.Split(brokenImagesList(images), ", ")

		named := min(n, maxListedImages)
		for i, item := range got[:named] {
			if want := fmt.Sprintf("i%d.qc2 (p)", i); item != want {
				t.Fatalf("item %d = %q, want %q", i, item, want)
			}
		}

		switch rest := got[named:]; {
		case n <= maxListedImages && len(rest) != 0:
			t.Fatalf("list of %d has extra items %q", n, rest)
		case n > maxListedImages && (len(rest) != 1 || rest[0] != fmt.Sprintf("and %d more", n-maxListedImages)):
			t.Fatalf("list of %d ends with %q", n, rest)
		}
	})
}
