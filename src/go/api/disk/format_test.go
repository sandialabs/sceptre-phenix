package disk

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/diskchain"
	"phenix/util/diskchain/diskchaintest"
	"phenix/util/mm/mmtest"
)

// formatFields are the Details fields that tell an image's format.
type formatFields struct {
	Format        string
	FormatVersion int
	FormatIssue   *FormatIssue
}

func formatOfDetails(d Details) formatFields {
	return formatFields{Format: d.Format, FormatVersion: d.FormatVersion, FormatIssue: d.FormatIssue}
}

// badClusterBits is a qcow2 header whose cluster bits qemu refuses.
func badClusterBits() []byte {
	buf := diskchaintest.Qcow2{}.Bytes()
	buf[23] = 30

	return buf
}

func TestGetImageReportsFormat(t *testing.T) {
	tests := map[string]struct {
		// name is the image's file name; its extension sets the format the fake qemu-img reports
		name  string
		write func(t *testing.T, path string)
		want  func(path string) formatFields
	}{
		"a qcow2 version 3 image needs no care": {
			name:  "kali.qc2",
			write: func(t *testing.T, path string) { t.Helper(); diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{}) },
			want:  func(string) formatFields { return formatFields{Format: "qcow2", FormatVersion: 3} },
		},
		"a qcow2 version 2 image is old, and is upgraded in place": {
			name: "kali.qc2",
			write: func(t *testing.T, path string) {
				t.Helper()
				diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Version: 2})
			},
			want: func(path string) formatFields {
				return formatFields{Format: "qcow2", FormatVersion: 2, FormatIssue: &FormatIssue{
					Level: IssueWarning, Problem: path + " is a qcow2 version 2 image, an old version of the format",
					Fix:     "Stop each VM that uses it, then upgrade it to version 3",
					Command: "qemu-img amend -f qcow2 -o compat=1.1 " + path,
				}}
			},
		},
		"a qcow version 1 image is old, and is converted to a copy": {
			name:  "kali.qcow",
			write: func(t *testing.T, path string) { t.Helper(); diskchaintest.WriteQcow(t, path, diskchaintest.Qcow{}) },
			want: func(path string) formatFields {
				return formatFields{Format: "qcow", FormatVersion: 1, FormatIssue: &FormatIssue{
					Level:   IssueWarning,
					Problem: path + " is a qcow version 1 image, an old format that some qemu builds cannot open",
					Fix:     "Convert it to qcow2, then rebase each image that it backs onto the copy",
					Command: "qemu-img convert -f qcow -O qcow2 " + path + " <new image>",
				}}
			},
		},
		"the old AES encryption is an error, with no single command to fix it": {
			name: "kali.qc2",
			write: func(t *testing.T, path string) {
				t.Helper()
				diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Encryption: 1})
			},
			want: func(path string) formatFields {
				return formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{
					Level: IssueError, Problem: path + " uses the old qcow AES encryption, which qemu does not open for VMs",
					Fix:     "Write a copy without encryption. The Image Format Versions section of the disks docs shows how",
					Command: "",
				}}
			},
		},
		"LUKS encryption is an error": {
			name: "kali.qc2",
			write: func(t *testing.T, path string) {
				t.Helper()
				diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Encryption: 2})
			},
			want: func(path string) formatFields {
				return formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{
					Level: IssueError, Problem: path + " uses LUKS encryption, and phenix cannot give qemu its key",
					Fix:     "Write a copy without encryption. The Image Format Versions section of the disks docs shows how",
					Command: "",
				}}
			},
		},
		"unknown feature bits are an error, fixed with a newer qemu-img": {
			name: "kali.qc2",
			write: func(t *testing.T, path string) {
				t.Helper()
				diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Features: 1 << 5})
			},
			want: func(path string) formatFields {
				return formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{
					Level:   IssueError,
					Problem: path + " uses qcow2 features that qemu does not know (incompatible feature bits 0x20)",
					Fix:     "A newer qemu made it. Write a copy with the qemu-img of that qemu",
					Command: "qemu-img convert -O qcow2 -o compat=1.1 " + path + " <new image>",
				}}
			},
		},
		"a corrupt image is an error, even when it is also dirty": {
			name: "kali.qc2",
			write: func(t *testing.T, path string) {
				t.Helper()
				diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Features: 3})
			},
			want: func(path string) formatFields {
				return formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{
					Level: IssueError, Problem: "qemu marked " + path + " as corrupt",
					Fix: "Copy the image, then repair it", Command: "qemu-img check -r all " + path,
				}}
			},
		},
		"an image that qemu did not close cleanly is a warning": {
			name: "kali.qc2",
			write: func(t *testing.T, path string) {
				t.Helper()
				diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Features: 1})
			},
			want: func(path string) formatFields {
				return formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{
					Level: IssueWarning, Problem: "qemu did not close " + path + " cleanly",
					Fix: "Copy the image, then repair it", Command: "qemu-img check -r all " + path,
				}}
			},
		},
		"zstd compression needs no care on the Disks page": {
			name: "kali.qc2",
			write: func(t *testing.T, path string) {
				t.Helper()
				diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Zstd: true})
			},
			want: func(string) formatFields { return formatFields{Format: "qcow2", FormatVersion: 3} },
		},
		"a header phenix cannot read is a warning": {
			name:  "kali.qc2",
			write: func(t *testing.T, path string) { t.Helper(); diskchaintest.WriteFile(t, path, badClusterBits()) },
			want: func(path string) formatFields {
				return formatFields{Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{
					Level:   IssueWarning,
					Problem: "phenix cannot read the header of " + path + " (invalid qcow2 header: cluster bits 30)",
					Fix:     "Examine it with qemu-img", Command: "qemu-img check " + path,
				}}
			},
		},
		"a vmdk image is a format phenix does not check": {
			name:  "kali.vmdk",
			write: func(t *testing.T, path string) { t.Helper(); diskchaintest.WriteFile(t, path, []byte("KDMV")) },
			want: func(path string) formatFields {
				return formatFields{Format: "vmdk", FormatIssue: &FormatIssue{
					Level:   IssueWarning,
					Problem: path + " is a vmdk image, a format whose backing chain phenix does not check",
					Fix:     "Convert it to qcow2",
					Command: "qemu-img convert -f vmdk -O qcow2 -o compat=1.1 " + path + " <new image>",
				}}
			},
		},
		"another format that qemu-img names is a format phenix does not check": {
			name:  "kali.vdi",
			write: func(t *testing.T, path string) { t.Helper(); diskchaintest.WriteFile(t, path, []byte("vdi")) },
			want: func(path string) formatFields {
				return formatFields{Format: "vdi", FormatIssue: &FormatIssue{
					Level:   IssueWarning,
					Problem: path + " is a vdi image, a format whose backing chain phenix does not check",
					Fix:     "Convert it to qcow2",
					Command: "qemu-img convert -f vdi -O qcow2 -o compat=1.1 " + path + " <new image>",
				}}
			},
		},
		"a raw image needs no care": {
			name:  "tools.iso",
			write: func(t *testing.T, path string) { t.Helper(); diskchaintest.WriteFile(t, path, []byte("CD001")) },
			want:  func(string) formatFields { return formatFields{Format: "raw"} },
		},
		"a command quotes a path with a space": {
			name: "my kali.qc2",
			write: func(t *testing.T, path string) {
				t.Helper()
				diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Version: 2})
			},
			want: func(path string) formatFields {
				return formatFields{Format: "qcow2", FormatVersion: 2, FormatIssue: &FormatIssue{
					Level: IssueWarning, Problem: path + " is a qcow2 version 2 image, an old version of the format",
					Fix:     "Stop each VM that uses it, then upgrade it to version 3",
					Command: "qemu-img amend -f qcow2 -o compat=1.1 '" + path + "'",
				}}
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useFakeQemuImg(t)

			path := filepath.Join(t.TempDir(), tt.name)
			tt.write(t, path)

			if diff := cmp.Diff(tt.want(path), formatOfDetails(getImage(t, path))); diff != "" {
				t.Errorf("GetImage(%s) format mismatch (-want +got):\n%s", path, diff)
			}
		})
	}
}

// Each image of a chain has the format of its own header, read once with the chain: a cached chain is not read
// again until a file changes, such as when an image is upgraded in place.
func TestGetImageReadsFormatsWithTheCachedChain(t *testing.T) {
	calls := useFakeQemuImg(t)

	dir := t.TempDir()
	over, base := filepath.Join(dir, "over.qc2"), filepath.Join(dir, "base.qc2")

	writeOverlay(t, over, "base.qc2", "base.qc2")
	diskchaintest.WriteQcow2(t, base, diskchaintest.Qcow2{Version: 2})

	// each image's format and version, and the level of its issue, if any
	resolve := func() map[string]formatFields {
		details := make(map[string]Details)
		resolveLocal([]string{over}, details)

		got := make(map[string]formatFields, len(details))

		for path, d := range details {
			f := formatOfDetails(d)
			if f.FormatIssue != nil {
				f.FormatIssue = &FormatIssue{Level: f.FormatIssue.Level, Problem: "", Fix: "", Command: ""}
			}

			got[path] = f
		}

		return got
	}

	old := map[string]formatFields{
		over: {Format: "qcow2", FormatVersion: 3},
		base: {Format: "qcow2", FormatVersion: 2, FormatIssue: &FormatIssue{Level: IssueWarning}},
	}

	for range 2 {
		if diff := cmp.Diff(old, resolve()); diff != "" {
			t.Errorf("resolveLocal(chain with a version 2 base) mismatch (-want +got):\n%s", diff)
		}
	}

	if n := calls(); n != 1 {
		t.Errorf("qemu-img runs for a chain listed twice = %d, want 1", n)
	}

	// qemu-img amend writes the header in place
	diskchaintest.WriteQcow2(t, base, diskchaintest.Qcow2{})

	upgraded := map[string]formatFields{
		over: {Format: "qcow2", FormatVersion: 3},
		base: {Format: "qcow2", FormatVersion: 3},
	}

	if diff := cmp.Diff(upgraded, resolve()); diff != "" {
		t.Errorf("resolveLocal(chain once its base is upgraded) mismatch (-want +got):\n%s", diff)
	}
}

// An image that qemu-img cannot open because qemu refuses its version is still listed, read from its header alone.
func TestGetImagesListsImagesQemuRefuses(t *testing.T) {
	useFakeQemuImg(t)

	dir := useFilesDir(t)
	path := filepath.Join(dir, "kali.qc2")

	diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Features: 1 << 6})
	writeFile(t, path+".refuse", "")

	// a refused image is listed only when its header shows why
	writeImages(t, filepath.Join(dir, "other.qc2"))
	writeFile(t, filepath.Join(dir, "other.qc2.refuse"), "")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%s) error = %v", path, err)
	}

	want := map[string]Details{
		path: {
			Kind: VMImage, Name: "kali.qc2", FullPath: path, RelativePath: "kali.qc2",
			Size: humanReadableBytes(info.Size()), BackingImages: []string{}, Experiments: nil,
			Format: "qcow2", FormatVersion: 3, FormatIssue: &FormatIssue{
				Level:   IssueError,
				Problem: path + " uses qcow2 features that qemu does not know (incompatible feature bits 0x40)",
				Fix:     "A newer qemu made it. Write a copy with the qemu-img of that qemu",
				Command: "qemu-img convert -O qcow2 -o compat=1.1 " + path + " <new image>",
			},
		},
	}

	if diff := cmp.Diff(want, listed(t, "")); diff != "" {
		t.Errorf("GetImages() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want[path], getImage(t, path)); diff != "" {
		t.Errorf("GetImage(%s) mismatch (-want +got):\n%s", path, diff)
	}

	// a commit says why qemu refuses the image, not that it has no backing image
	err = CommitDisk(path)
	if got := fmt.Sprint(err); got != want[path].FormatIssue.Problem {
		t.Errorf("CommitDisk(%s) error = %q, want %q", path, got, want[path].FormatIssue.Problem)
	}
}

// An image backed by an image that qemu refuses shows that refusal, with the fix for the backing image. It comes from
// the walk when qemu-img cannot read the chain, which is cached, and from each header when qemu-img can.
func TestGetImageShowsARefusedBackingImage(t *testing.T) {
	tests := map[string]struct {
		base diskchaintest.Qcow2
		// refuse makes qemu-img fail on the base, as it does for a version 4 image
		refuse bool
		// problem is the problem, after the base's path, of the overlay and the image between
		problem string
		// runs are the qemu-img runs for the overlay and the image between, both listed twice
		runs int
	}{
		"a version qemu-img refuses": {
			base: diskchaintest.Qcow2{Version: 4}, refuse: true,
			problem: " is a qcow2 version 4 image, which qemu cannot open", runs: 4,
		},
		"an encryption qemu-img opens": {
			base:    diskchaintest.Qcow2{Encryption: 1},
			problem: " uses the old qcow AES encryption, which qemu does not open for VMs", runs: 2,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			calls := useFakeQemuImg(t)

			dir := t.TempDir()
			over, mid, base := filepath.Join(dir, "over.qc2"), filepath.Join(dir, "mid.qc2"), filepath.Join(dir, "base.qc2")

			writeOverlay(t, over, "mid.qc2", "mid.qc2")
			writeOverlay(t, mid, "base.qc2", "base.qc2")
			diskchaintest.WriteQcow2(t, base, tt.base)

			if tt.refuse {
				writeFile(t, base+".refuse", "")
			}

			want := map[string]string{over: base + tt.problem, mid: base + tt.problem}

			for range 2 {
				details := make(map[string]Details)
				resolveLocal([]string{over, mid}, details)

				got := make(map[string]string, len(want))

				for _, path := range []string{over, mid} {
					if issue := details[path].FormatIssue; issue != nil && issue.Level == IssueError {
						got[path] = issue.Problem
					}
				}

				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("resolveLocal(%s, %s) problems mismatch (-want +got):\n%s", over, mid, diff)
				}

				if diff := cmp.Diff([]string{mid, base}, details[over].BackingImages); diff != "" {
					t.Errorf("resolveLocal(%s) backing images mismatch (-want +got):\n%s", over, diff)
				}
			}

			if n := calls(); n != tt.runs {
				t.Errorf("qemu-img runs for two images listed twice = %d, want %d", n, tt.runs)
			}
		})
	}
}

// Through minimega, the format is the one minimega reports, and the version and issue come from the header.
func TestGetImageThroughMinimegaReportsFormat(t *testing.T) {
	dir := useFilesDir(t)
	t.Setenv("PATH", t.TempDir())

	diskchaintest.WriteQcow2(t, dir+"/old.qc2", diskchaintest.Qcow2{Version: 2})
	diskchaintest.WriteQcow2(t, dir+"/new.qc2", diskchaintest.Qcow2{Features: 1 << 7})

	info := []string{"image", "format", "virtualsize", "disksize", "backingfile", "inuse"}
	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "disk info "+dir+"/old.qc2 recursive" {
			return []*minicli.Response{
				mmtest.Tabular("head", info, []string{dir + "/old.qc2", "qcow2", "1G", "2K", "", "false"}),
			}
		}

		return nil
	})

	want := formatFields{Format: "qcow2", FormatVersion: 2, FormatIssue: &FormatIssue{
		Level: IssueWarning, Problem: dir + "/old.qc2 is a qcow2 version 2 image, an old version of the format",
		Fix:     "Stop each VM that uses it, then upgrade it to version 3",
		Command: "qemu-img amend -f qcow2 -o compat=1.1 " + dir + "/old.qc2",
	}}

	if diff := cmp.Diff(want, formatOfDetails(getImage(t, "old.qc2"))); diff != "" {
		t.Errorf("GetImage(old.qc2) through minimega mismatch (-want +got):\n%s", diff)
	}

	// minimega cannot inspect an image qemu refuses, so phenix reads its header
	got := getImage(t, "new.qc2")
	if got.FormatIssue == nil || got.FormatIssue.Level != IssueError || got.FormatVersion != 3 {
		t.Errorf("GetImage(new.qc2) through minimega = %+v, want a qcow2 version 3 image with an error", got)
	}
}

// Each issue names its image, and its command names the image as one shell word, whatever the path holds.
func TestFormatIssueNamesItsImage(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to read the commands")
	}

	rapid.Check(t, func(rt *rapid.T) {
		path := "/" + rapid.StringMatching(`[a-z '"$;\\ .-]{1,12}`).Draw(rt, "name")
		q := diskchaintest.Qcow2{
			Version:    rapid.SampledFrom([]int{2, 3}).Draw(rt, "version"),
			Encryption: uint32(rapid.IntRange(0, 2).Draw(rt, "encryption")),
			Features:   rapid.Uint64Range(0, 1<<6).Draw(rt, "features") &^ (1 << 3),
		}
		format := rapid.SampledFrom([]string{"qcow2", "vmdk", "vdi"}).Draw(rt, "format")

		h, err := diskchain.ParseHeader(bytes.NewReader(q.Bytes()))
		issue := formatIssue(path, format, imageHeader{header: h, check: diskchain.Check(path, h, err), read: true})

		// only the external data file and extended L2 bits need no care
		wantIssue := format != "qcow2" || q.Version == 2 || q.Encryption != 0 || q.Features&^(1<<2|1<<4) != 0
		if (issue != nil) != wantIssue {
			rt.Fatalf("formatIssue(%q, %s, %+v) = %+v, want an issue: %t", path, format, q, issue, wantIssue)
		}

		if issue == nil {
			return
		}

		if !strings.Contains(issue.Problem, path) {
			rt.Errorf("Problem %q does not name %q", issue.Problem, path)
		}

		if issue.Command == "" {
			return
		}

		// sh prints each word of the command on a line of its own; <new image> is a placeholder, not a redirection
		command := strings.ReplaceAll(issue.Command, "<new image>", "new")

		out, err := exec.CommandContext(t.Context(), "sh", "-c", "printf '%s\\n' "+command).Output()
		if err != nil {
			rt.Fatalf("sh -c %q error = %v", command, err)
		}

		if words := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); !slices.Contains(words, path) {
			rt.Errorf("command %q has no word %q: %q", issue.Command, path, words)
		}
	})
}
