package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/disk"
	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
)

// The disk-formats check names the images in an old or unsupported format, or that qemu refuses, and in a second row
// the images that need a later qemu than the local qemu-img.
func TestDiskFormatsChecks(t *testing.T) {
	t.Parallel()

	scanOf := func(scan disk.ChainScan) func(context.Context, string) (disk.ChainScan, error) {
		return func(context.Context, string) (disk.ChainScan, error) { return scan, nil }
	}

	tests := map[string]struct {
		setup     func(t *testing.T, files string)
		images    func(context.Context, string) (disk.ChainScan, error) // in place of disk.BrokenImages
		qemuImg   string                                                // the qemu-img --version line
		want      func(files string) []doctorCheck
		wantHints []string
	}{
		"no images": {
			want: func(files string) []doctorCheck { return []doctorCheck{passCheck("no images in " + files)} },
		},
		"current formats": {
			setup: func(t *testing.T, files string) {
				t.Helper()
				dct.Chain(t, filepath.Join(files, "kali.qc2"), filepath.Join(files, "ro_kali.qc2"))
				dct.WriteQcow2(t, filepath.Join(files, "zstd.qc2"), dct.Qcow2{Zstd: true})
			},
			want: func(files string) []doctorCheck {
				return []doctorCheck{passCheck("3 images in " + files + ", all qcow2 version 3 or raw")}
			},
		},
		"old, unsupported and refused formats": {
			setup: func(t *testing.T, files string) {
				t.Helper()
				dct.WriteQcow2(t, filepath.Join(files, "kali.qc2"), dct.Qcow2{Version: 2})
				dct.WriteQcow(t, filepath.Join(files, "old.qc2"), dct.Qcow{})
				dct.WriteQcow2(t, filepath.Join(files, "aes.qc2"), dct.Qcow2{Encryption: 1})
				dct.WriteFile(t, filepath.Join(files, "win.qcow2"), []byte("KDMV"))
				dct.WriteQcow2(t, filepath.Join(files, "v3.qc2"), dct.Qcow2{})
			},
			want: func(files string) []doctorCheck {
				return []doctorCheck{warnCheck(fmt.Sprintf("4 of 5 images in %[1]s use an old or unsupported format: "+
					"aes.qc2 (%[1]s/aes.qc2 uses the old qcow AES encryption, which qemu does not open for VMs), "+
					"kali.qc2 (qcow2 version 2), old.qc2 (qcow version 1), win.qcow2 (vmdk)", files))}
			},
			wantHints: []string{
				"upgrade each version 2 image with qemu-img amend -f qcow2 -o compat=1.1 <image>. Convert each qcow " +
					"version 1 image with qemu-img convert -f qcow -O qcow2 <image> <new image>. See " + formatsDocs +
					" for the other fixes",
			},
		},
		"one image": {
			setup: func(t *testing.T, files string) {
				t.Helper()
				dct.WriteQcow2(t, filepath.Join(files, "kali.qc2"), dct.Qcow2{Features: 1 << 1})
			},
			want: func(files string) []doctorCheck {
				return []doctorCheck{warnCheck(fmt.Sprintf(
					"1 of 1 image in %[1]s uses an old or unsupported format: kali.qc2 (qemu marked %[1]s/kali.qc2 as corrupt)",
					files))}
			},
			wantHints: []string{"see " + formatsDocs + " for the fix of each format"},
		},
		"more than five": {
			images: scanOf(disk.ChainScan{Images: 7, Checked: 7, Formats: []disk.FormatImage{
				{Path: "a", Kind: diskchain.Qcow2V2}, {Path: "b", Kind: diskchain.Qcow2V2}, {Path: "c", Kind: diskchain.QED},
				{Path: "d", Kind: diskchain.Qcow2V2}, {Path: "e", Kind: diskchain.Qcow2V2}, {Path: "f", Kind: diskchain.Qcow2V2},
				{Path: "g", Kind: diskchain.Qcow2V2},
			}}),
			want: func(files string) []doctorCheck {
				return []doctorCheck{warnCheck("7 of 7 images in " + files + " use an old or unsupported format: " +
					"a (qcow2 version 2), b (qcow2 version 2), c (qed), d (qcow2 version 2), e (qcow2 version 2), and 2 more")}
			},
			wantHints: []string{"upgrade each version 2 image"},
		},
		"a qemu-img older than the features": {
			setup: func(t *testing.T, files string) {
				t.Helper()
				dct.WriteQcow2(t, filepath.Join(files, "kali.qc2"), dct.Qcow2{Zstd: true, Features: 1 << 4})
				dct.WriteQcow2(t, filepath.Join(files, "l2.qc2"), dct.Qcow2{Features: 1 << 4})
				dct.WriteQcow2(t, filepath.Join(files, "data.qc2"), dct.Qcow2{DataFile: true})
			},
			qemuImg: "qemu-img version 5.1.0 (Debian 1:5.1+dfsg-4)",
			want: func(files string) []doctorCheck {
				return []doctorCheck{
					passCheck("3 images in " + files + ", all qcow2 version 3 or raw"),
					warnCheck("qemu-img is version 5.1, and 2 of the images in " + files + " need a later qemu: " +
						"kali.qc2 (qemu 5.2 or later for extended L2 entries), l2.qc2 (qemu 5.2 or later for extended L2 entries)"),
				}
			},
			wantHints: []string{"", "update qemu on each node"},
		},
		"a qemu-img older than an external data file": {
			setup: func(t *testing.T, files string) {
				t.Helper()
				dct.WriteQcow2(t, filepath.Join(files, "data.qc2"), dct.Qcow2{DataFile: true})
			},
			qemuImg: "qemu-img version 3.1.0",
			want: func(files string) []doctorCheck {
				return []doctorCheck{
					passCheck("1 image in " + files + ", all qcow2 version 3 or raw"),
					warnCheck("qemu-img is version 3.1, and 1 of the images in " + files + " needs a later qemu: " +
						"data.qc2 (qemu 4.0 or later for an external data file)"),
				}
			},
			wantHints: []string{"", "update qemu on each node"},
		},
		"a qemu-img version phenix cannot read": {
			setup: func(t *testing.T, files string) {
				t.Helper()
				dct.WriteQcow2(t, filepath.Join(files, "kali.qc2"), dct.Qcow2{Zstd: true})
			},
			qemuImg: "qemu-img (no version)",
			want: func(files string) []doctorCheck {
				return []doctorCheck{passCheck("1 image in " + files + ", all qcow2 version 3 or raw")}
			},
		},
		"out of time": {
			images: scanOf(disk.ChainScan{Images: 3, Checked: 1, Formats: []disk.FormatImage{{Path: "a", Kind: diskchain.QcowV1}}}),
			want: func(files string) []doctorCheck {
				return []doctorCheck{warnCheck("checked 1 of 3 images in " + files + " within 1s, and found 1 in an old or " +
					"unsupported format: a (qcow version 1)")}
			},
			wantHints: []string{"increase --timeout to check every image"},
		},
		"out of time, none found": {
			images: scanOf(disk.ChainScan{Images: 3, Checked: 1}),
			want: func(files string) []doctorCheck {
				return []doctorCheck{warnCheck("checked 1 of 3 images in " + files + " within 1s, and found none in an old " +
					"or unsupported format")}
			},
			wantHints: []string{"increase --timeout to check every image"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)
			files := filepath.Join(dir, "files")

			if tt.setup != nil {
				tt.setup(t, files)
			}

			in := fakeDoctorInput(dir, nil)
			in.found = &doctorFound{minimegaUp: true, minimegaArgs: doctorMinimegaArgs(dir), images: nil}

			if tt.images != nil {
				in.images = tt.images
			}

			if tt.qemuImg != "" {
				in.qemuImg = func(context.Context, string) (string, error) { return tt.qemuImg, nil }
			}

			got := diskFormatsChecks(t.Context(), in)
			if diff := cmp.Diff(tt.want(files), got); diff != "" {
				t.Errorf("diskFormatsChecks() mismatch (-want +got):\n%s", diff)
			}

			for i, check := range got {
				var want string
				if i < len(tt.wantHints) {
					want = tt.wantHints[i]
				}

				if check.Status == doctorPass {
					continue
				}

				if hint := diskFormatsHint(check); !strings.HasPrefix(hint, want) || want == "" {
					t.Errorf("diskFormatsHint(row %d) = %q, want it to start with %q", i, hint, want)
				}
			}
		})
	}
}

// disk-chains and disk-formats share one scan of the images folder; disk-formats scans by itself when disk-chains is
// skipped.
func TestDiskChecksShareOneScan(t *testing.T) {
	t.Parallel()

	for _, chains := range []bool{true, false} {
		dir := newDoctorTestDir(t)
		in := fakeDoctorInput(dir, nil)
		in.found = &doctorFound{minimegaUp: true, minimegaArgs: doctorMinimegaArgs(dir), images: nil}

		scans := 0
		in.images = func(context.Context, string) (disk.ChainScan, error) {
			scans++

			return disk.ChainScan{Images: 1, Checked: 1}, nil
		}

		if chains {
			_ = diskChainsCheck(t.Context(), in)
		}

		_ = diskFormatsChecks(t.Context(), in)

		if scans != 1 {
			t.Errorf("with disk-chains %t: %d scans, want 1", chains, scans)
		}
	}
}

// parseQemuVersion reads the major and minor version of any qemu-img --version line, and olderQemu orders them as
// numbers, not text.
func TestParseQemuVersionProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		major := rapid.IntRange(0, 99).Draw(t, "major")
		minor := rapid.IntRange(0, 99).Draw(t, "minor")
		suffix := rapid.SampledFrom([]string{"", ".0", ".13 (Debian 1:10.0.13+ds-0+deb13u1)", " (qemu-kvm-8.2.0)"}).
			Draw(t, "suffix")

		got, ok := parseQemuVersion(fmt.Sprintf("qemu-img version %d.%d%s", major, minor, suffix))
		if !ok || got != [2]int{major, minor} {
			t.Fatalf("parseQemuVersion() = %v, %t, want %d.%d", got, ok, major, minor)
		}

		other := [2]int{rapid.IntRange(0, 99).Draw(t, "other major"), rapid.IntRange(0, 99).Draw(t, "other minor")}
		want := major*1000+minor < other[0]*1000+other[1]

		if olderQemu(got, other) != want {
			t.Fatalf("olderQemu(%v, %v) = %t, want %t", got, other, !want, want)
		}
	})
}
