package disk

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/testenv"
)

// runQemuImg runs qemu-img with args, failing the test on an error.
func runQemuImg(t *testing.T, args ...string) {
	t.Helper()

	if out, err := exec.CommandContext(t.Context(), "qemu-img", args...).CombinedOutput(); err != nil {
		t.Fatalf("qemu-img %v: %v\n%s", args, err, out)
	}
}

// patchHeader writes value, big endian, at offset in the header of the image at path.
func patchHeader(t *testing.T, path string, offset int64, value []byte) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile(%s) error = %v", path, err)
	}

	defer func() { _ = f.Close() }()

	if _, err := f.WriteAt(value, offset); err != nil {
		t.Fatalf("writing the header of %s: %v", path, err)
	}
}

func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func be64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

// The Disks page reads the issues of the images that qemu-img makes, and each fix command, run as the page shows it,
// gives an image with no issue. CI installs qemu-img.
func TestFormatIssuesOfQemuImages(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img", "sh")

	secret := []string{"--object", "secret,id=sec0,data=kalipass"}

	tests := map[string]struct {
		name string
		make func(t *testing.T, path string)
		// want are the format, version and issue level qemu-img's image has
		want formatFields
		// fixed is the version of the image the fix command writes, or -1 when it is not run
		fixed int
	}{
		"qcow2 version 2": {
			name: "kali.qc2",
			make: func(t *testing.T, path string) {
				t.Helper()
				runQemuImg(t, "create", "-f", "qcow2", "-o", "compat=0.10", path, "1M")
			},
			want:  formatFields{Format: "qcow2", FormatVersion: 2, FormatIssue: &FormatIssue{Level: IssueWarning}},
			fixed: 3,
		},
		"qcow2 version 2 at a path with a space": {
			name: "my disks/kali.qc2",
			make: func(t *testing.T, path string) {
				t.Helper()
				runQemuImg(t, "create", "-f", "qcow2", "-o", "compat=0.10", path, "1M")
			},
			want:  formatFields{Format: "qcow2", FormatVersion: 2, FormatIssue: &FormatIssue{Level: IssueWarning}},
			fixed: 3,
		},
		"qcow version 1": {
			name:  "kali.qcow",
			make:  func(t *testing.T, path string) { t.Helper(); runQemuImg(t, "create", "-f", "qcow", path, "1M") },
			want:  formatFields{Format: "qcow", FormatVersion: 1, FormatIssue: &FormatIssue{Level: IssueWarning}},
			fixed: 3,
		},
		"vmdk": {
			name:  "kali.vmdk",
			make:  func(t *testing.T, path string) { t.Helper(); runQemuImg(t, "create", "-f", "vmdk", path, "1M") },
			want:  formatFields{Format: "vmdk", FormatVersion: 0, FormatIssue: &FormatIssue{Level: IssueWarning}},
			fixed: 3,
		},
		"qed": {
			name:  "kali.qed",
			make:  func(t *testing.T, path string) { t.Helper(); runQemuImg(t, "create", "-f", "qed", path, "1M") },
			want:  formatFields{Format: "qed", FormatVersion: 0, FormatIssue: &FormatIssue{Level: IssueWarning}},
			fixed: 3,
		},
		"vdi": {
			name:  "kali.vdi",
			make:  func(t *testing.T, path string) { t.Helper(); runQemuImg(t, "create", "-f", "vdi", path, "1M") },
			want:  formatFields{Format: "vdi", FormatVersion: 0, FormatIssue: &FormatIssue{Level: IssueWarning}},
			fixed: 3,
		},
		"not closed cleanly": {
			name: "kali.qc2",
			make: func(t *testing.T, path string) {
				t.Helper()
				runQemuImg(t, "create", "-f", "qcow2", path, "1M")
				patchHeader(t, path, 72, be64(1))
			},
			want:  formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{Level: IssueWarning}},
			fixed: 3,
		},
		"corrupt": {
			name: "kali.qc2",
			make: func(t *testing.T, path string) {
				t.Helper()
				runQemuImg(t, "create", "-f", "qcow2", path, "1M")
				patchHeader(t, path, 72, be64(2))
			},
			want:  formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{Level: IssueError}},
			fixed: 3,
		},
		"old AES encryption": {
			name: "kali.qc2",
			make: func(t *testing.T, path string) {
				t.Helper()
				runQemuImg(t, "create", secret[0], secret[1], "-f", "qcow2", "-o",
					"encrypt.format=aes,encrypt.key-secret=sec0", path, "1M")
			},
			want:  formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{Level: IssueError}},
			fixed: -1,
		},
		"LUKS encryption": {
			name: "kali.qc2",
			make: func(t *testing.T, path string) {
				t.Helper()
				runQemuImg(t, "create", secret[0], secret[1], "-f", "qcow2", "-o",
					"encrypt.format=luks,encrypt.key-secret=sec0", path, "1M")
			},
			want:  formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{Level: IssueError}},
			fixed: -1,
		},
		// qemu-img cannot open these, so the list reads them from the header alone
		"qcow2 version 4": {
			name: "kali.qc2",
			make: func(t *testing.T, path string) {
				t.Helper()
				runQemuImg(t, "create", "-f", "qcow2", path, "1M")
				patchHeader(t, path, 4, be32(4))
			},
			want:  formatFields{Format: "qcow2", FormatVersion: 4, FormatIssue: &FormatIssue{Level: IssueError}},
			fixed: -1,
		},
		"unknown feature bits": {
			name: "kali.qc2",
			make: func(t *testing.T, path string) {
				t.Helper()
				runQemuImg(t, "create", "-f", "qcow2", path, "1M")
				patchHeader(t, path, 72, be64(1<<5))
			},
			want:  formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{Level: IssueError}},
			fixed: -1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ClearCache()
			t.Cleanup(ClearCache)

			path := filepath.Join(t.TempDir(), tt.name)
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatalf("os.MkdirAll(%s) error = %v", filepath.Dir(path), err)
			}

			tt.make(t, path)

			got := getImage(t, path)
			if diff := cmp.Diff(tt.want, levelOnly(got)); diff != "" {
				t.Fatalf("GetImage(%s) format mismatch (-want +got):\n%s", path, diff)
			}

			if tt.fixed < 0 {
				return
			}

			// the copy, for a fix that writes one
			fixed := filepath.Join(filepath.Dir(path), "new image.qc2")
			command := strings.ReplaceAll(got.FormatIssue.Command, "<new image>", shellQuote(fixed))

			if !strings.Contains(command, shellQuote(fixed)) {
				fixed = path
			}

			if out, err := exec.CommandContext(t.Context(), "sh", "-c", command).CombinedOutput(); err != nil {
				t.Fatalf("sh -c %q: %v\n%s", command, err, out)
			}

			want := formatFields{Format: "qcow2", FormatVersion: tt.fixed, FormatIssue: nil}
			if diff := cmp.Diff(want, levelOnly(getImage(t, fixed))); diff != "" {
				t.Errorf("GetImage(%s) after %q mismatch (-want +got):\n%s", fixed, command, diff)
			}
		})
	}
}

// levelOnly is the format of d, with only the level of its issue.
func levelOnly(d Details) formatFields {
	f := formatOfDetails(d)
	if f.FormatIssue != nil {
		f.FormatIssue = &FormatIssue{Level: f.FormatIssue.Level, Problem: "", Fix: "", Command: ""}
	}

	return f
}

// An overlay whose backing image qemu-img refuses shows the refusal of that backing image. CI installs qemu-img.
func TestFormatIssueOfAnOverlayOfARefusedImage(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")
	ClearCache()
	t.Cleanup(ClearCache)

	dir := t.TempDir()
	base, over := filepath.Join(dir, "ro_kali.qc2"), filepath.Join(dir, "kali.qc2")

	runQemuImg(t, "create", "-q", "-f", "qcow2", base, "1M")
	runQemuImg(t, "create", "-q", "-f", "qcow2", "-b", base, "-F", "qcow2", over)
	patchHeader(t, base, 4, be32(4))

	got := getImage(t, over)

	want := formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{
		Level: IssueError, Problem: base + " is a qcow2 version 4 image, which qemu cannot open",
		Fix:     "If a newer qemu made it, write a copy with the qemu-img of that qemu",
		Command: "qemu-img convert -O qcow2 -o compat=1.1 " + base + " <new image>",
	}}

	if diff := cmp.Diff(want, formatOfDetails(got)); diff != "" {
		t.Errorf("GetImage(%s) format mismatch (-want +got):\n%s", over, diff)
	}

	if diff := cmp.Diff([]string{base}, got.BackingImages); diff != "" {
		t.Errorf("GetImage(%s) backing images mismatch (-want +got):\n%s", over, diff)
	}
}
