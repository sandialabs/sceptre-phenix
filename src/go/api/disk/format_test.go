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

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/diskchain"
	"phenix/util/diskchain/diskchaintest"
	"phenix/util/mm/mmtest"
	"phenix/util/testenv"
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
	testenv.RequireCommands(t, "sh")

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

// headerIssue gives the issue of a problem that qemu refuses or phenix cannot read first, then a corrupt mark, then
// the first note that needs care, and nil when nothing does.
func TestHeaderIssue(t *testing.T) {
	t.Parallel()

	const path = "/f/my kali.qc2"

	quoted := "'" + path + "'"
	note := func(kind diskchain.NoteKind) diskchain.Note { return diskchain.Note{Path: path, Kind: kind} }
	encrypted := "Write a copy without encryption. The Image Format Versions section of the disks docs shows how"
	convert := "qemu-img convert -O qcow2 -o compat=1.1 " + quoted + " <new image>"
	repair := "qemu-img check -r all " + quoted

	tests := map[string]struct {
		res  diskchain.Result
		want *FormatIssue
	}{
		"a whole image": {res: diskchain.Result{Members: []string{path}}},
		"a version qemu refuses": {
			res: diskchain.Result{Problem: diskchain.BadVersion, At: path, Detail: "4"},
			want: &FormatIssue{
				Level: IssueError, Problem: path + " is a qcow2 version 4 image, which qemu cannot open",
				Fix: "If a newer qemu made it, write a copy with the qemu-img of that qemu", Command: convert,
			},
		},
		"unknown features": {
			res: diskchain.Result{Problem: diskchain.UnknownFeatures, At: path, Detail: "0x20"},
			want: &FormatIssue{
				Level:   IssueError,
				Problem: path + " uses qcow2 features that qemu does not know (incompatible feature bits 0x20)",
				Fix:     "A newer qemu made it. Write a copy with the qemu-img of that qemu", Command: convert,
			},
		},
		"AES encryption": {
			res: diskchain.Result{Problem: diskchain.AESEncrypted, At: path},
			want: &FormatIssue{
				Level: IssueError, Problem: path + " uses the old qcow AES encryption, which qemu does not open for VMs",
				Fix: encrypted,
			},
		},
		"LUKS encryption": {
			res: diskchain.Result{Problem: diskchain.LUKSEncrypted, At: path},
			want: &FormatIssue{
				Level: IssueError, Problem: path + " uses LUKS encryption, and phenix cannot give qemu its key", Fix: encrypted,
			},
		},
		"an unreadable header": {
			res: diskchain.Result{Problem: diskchain.Unreadable, At: path, Detail: "cluster bits 30"},
			want: &FormatIssue{
				Level: IssueWarning, Problem: "phenix cannot read the header of " + path + " (cluster bits 30)",
				Fix: "Examine it with qemu-img", Command: "qemu-img check " + quoted,
			},
		},
		"a corrupt mark wins over an earlier note": {
			res: diskchain.Result{Notes: []diskchain.Note{note(diskchain.Dirty), note(diskchain.Corrupt)}},
			want: &FormatIssue{
				Level: IssueError, Problem: "qemu marked " + path + " as corrupt", Fix: "Copy the image, then repair it",
				Command: repair,
			},
		},
		"the first note that needs care wins": {
			res: diskchain.Result{Notes: []diskchain.Note{note(diskchain.Zstd), note(diskchain.Qcow2V2), note(diskchain.Dirty)}},
			want: &FormatIssue{
				Level: IssueWarning, Problem: path + " is a qcow2 version 2 image, an old version of the format",
				Fix:     "Stop each VM that uses it, then upgrade it to version 3",
				Command: "qemu-img amend -f qcow2 -o compat=1.1 " + quoted,
			},
		},
		"a qcow version 1 image": {
			res: diskchain.Result{Problem: diskchain.Missing, At: "/f/gone", Notes: []diskchain.Note{note(diskchain.QcowV1)}},
			want: &FormatIssue{
				Level:   IssueWarning,
				Problem: path + " is a qcow version 1 image, an old format that some qemu builds cannot open",
				Fix:     "Convert it to qcow2, then rebase each image that it backs onto the copy",
				Command: "qemu-img convert -f qcow -O qcow2 " + quoted + " <new image>",
			},
		},
		"a dirty image": {
			res: diskchain.Result{Notes: []diskchain.Note{note(diskchain.Dirty)}},
			want: &FormatIssue{
				Level: IssueWarning, Problem: "qemu did not close " + path + " cleanly",
				Fix: "Copy the image, then repair it", Command: repair,
			},
		},
		"a qed image": {
			res:  diskchain.Result{Notes: []diskchain.Note{note(diskchain.QED)}},
			want: uncheckedIssue(path, quoted, "qed"),
		},
		"a vmdk image": {
			res:  diskchain.Result{Notes: []diskchain.Note{note(diskchain.VMDK)}},
			want: uncheckedIssue(path, quoted, "vmdk"),
		},
		"only recent features": {
			res: diskchain.Result{Problem: diskchain.DataFile, At: path, Notes: []diskchain.Note{note(diskchain.ExtendedL2)}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, headerIssue(path, quoted, tt.res)); diff != "" {
				t.Errorf("headerIssue(%s) mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// formatIssue prefers what the header shows, and otherwise warns about a format whose chain phenix does not check.
func TestFormatIssue(t *testing.T) {
	t.Parallel()

	const path = "/f/a.img"

	dirty := imageHeader{
		header: diskchain.Header{Format: diskchain.FormatQcow2, Version: 3},
		check:  diskchain.Result{Notes: []diskchain.Note{{Path: path, Kind: diskchain.Dirty}}},
		read:   true,
	}
	// a check that was not read is not looked at
	unread := imageHeader{check: dirty.check}

	tests := map[string]struct {
		format string
		ih     imageHeader
		want   *FormatIssue
	}{
		"an unread raw image":          {format: "raw", ih: unread},
		"an unread image of no format": {format: "", ih: unread},
		"an unread qcow image":         {format: "qcow", ih: unread},
		"an unread vdi image":          {format: "vdi", ih: unread, want: uncheckedIssue(path, path, "vdi")},
		"a dirty vdi image":            {format: "vdi", ih: dirty, want: headerIssue(path, path, dirty.check)},
		"a read raw image":             {format: "raw", ih: imageHeader{read: true}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, formatIssue(path, tt.format, tt.ih)); diff != "" {
				t.Errorf("formatIssue(%s, %q) mismatch (-want +got):\n%s", path, tt.format, diff)
			}
		})
	}
}

// setFormat keeps the format qemu-img or minimega gave, takes the header's when none was given, and sets the version
// only from a header it read.
func TestSetFormat(t *testing.T) {
	t.Parallel()

	v2 := imageHeader{header: diskchain.Header{Format: diskchain.FormatQcow2, Version: 2}, read: true}

	tests := map[string]struct {
		format string
		ih     imageHeader
		want   formatFields
	}{
		"no format given": {
			ih: v2,
			want: formatFields{Format: "qcow2", FormatVersion: 2, FormatIssue: &FormatIssue{
				Level: IssueWarning, Problem: "/f/a.qc2 is a qcow2 version 2 image, an old version of the format",
				Fix:     "Stop each VM that uses it, then upgrade it to version 3",
				Command: "qemu-img amend -f qcow2 -o compat=1.1 /f/a.qc2",
			}},
		},
		"a format given": {format: "raw", ih: imageHeader{read: true}, want: formatFields{Format: "raw"}},
		"no header read": {
			format: "qcow2",
			ih:     imageHeader{header: v2.header},
			want:   formatFields{Format: "qcow2", FormatVersion: 9},
		},
		"no format given or header read": {ih: imageHeader{header: v2.header}, want: formatFields{FormatVersion: 9}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := Details{FullPath: "/f/a.qc2", FormatVersion: 9}
			if tt.ih.read {
				tt.ih.check = diskchain.Check(d.FullPath, tt.ih.header, nil)
			}

			d.setFormat(tt.format, tt.ih)

			if diff := cmp.Diff(tt.want, formatOfDetails(d)); diff != "" {
				t.Errorf("setFormat(%q) mismatch (-want +got):\n%s", tt.format, diff)
			}
		})
	}
}

// qcowFormat names the two formats whose images have a backing chain, as qemu-img names them.
func TestQcowFormat(t *testing.T) {
	t.Parallel()

	for format, want := range map[string]bool{
		"qcow2": true, "qcow": true, "raw": false, "": false, "QCOW2": false, "qed": false, "vmdk": false,
	} {
		if got := qcowFormat(format); got != want {
			t.Errorf("qcowFormat(%q) = %t, want %t", format, got, want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"/f/win10.qcow2":       "/f/win10.qcow2",
		"/f/a_b-c+d:e@f%g=h,i": "/f/a_b-c+d:e@f%g=h,i",
		"":                     "''",
		"/f/my kali.qc2":       "'/f/my kali.qc2'",
		"/f/it's.qc2":          `'/f/it'\''s.qc2'`,
		"/f/$HOME;rm":          "'/f/$HOME;rm'",
		"/f/a\nb":              "'/f/a\nb'",
		"''":                   `''\'''\'''`,
	}

	for s, want := range tests {
		if got := shellQuote(s); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", s, got, want)
		}
	}
}

// setRefusedBacking records a backing image that qemu refuses, unless the image already has an error of its own or
// the refusal is the image's own.
func TestSetRefusedBacking(t *testing.T) {
	t.Parallel()

	const image, base = "/f/a.qc2", "/f/base.qc2"

	refused := diskchain.Result{Problem: diskchain.AESEncrypted, At: base}
	warning := &FormatIssue{Level: IssueWarning, Problem: "old"}
	own := &FormatIssue{Level: IssueError, Problem: "own"}

	tests := map[string]struct {
		issue *FormatIssue
		res   diskchain.Result
		want  *FormatIssue
	}{
		"a refused backing image":   {res: refused, want: headerIssue(base, base, refused)},
		"over a warning of its own": {issue: warning, res: refused, want: headerIssue(base, base, refused)},
		"under an error of its own": {issue: own, res: refused, want: own},
		"a refusal of the image itself": {
			issue: warning,
			res:   diskchain.Result{Problem: diskchain.AESEncrypted, At: image},
			want:  warning,
		},
		"a backing image that is not refused": {issue: warning, res: diskchain.Result{Problem: diskchain.Missing, At: base}, want: warning},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := Details{FullPath: image, FormatIssue: tt.issue}
			d.setRefusedBacking(tt.res)

			if diff := cmp.Diff(tt.want, d.FormatIssue); diff != "" {
				t.Errorf("setRefusedBacking(%s) mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// setRefusedBackings gives each image of a chain the nearest refused image below it, and the base nothing.
func TestSetRefusedBackings(t *testing.T) {
	t.Parallel()

	paths := []string{"/f/top.qc2", "/f/mid.qc2", "/f/low.qc2", "/f/base.qc2"}
	refuse := func(path string, problem diskchain.Kind) imageHeader {
		return imageHeader{check: diskchain.Result{Members: []string{path}, Problem: problem, At: path}, read: true}
	}

	headers := []imageHeader{
		{read: true},
		refuse(paths[1], diskchain.LUKSEncrypted),
		// a header that was not read is not looked at
		{check: diskchain.Result{Problem: diskchain.AESEncrypted, At: paths[2]}},
		refuse(paths[3], diskchain.AESEncrypted),
	}

	chain := make([]Details, len(paths))
	for i, p := range paths {
		chain[i] = Details{FullPath: p}
	}

	setRefusedBackings(chain, headers)

	got := make([]*FormatIssue, len(chain))
	for i, d := range chain {
		got[i] = d.FormatIssue
	}

	want := []*FormatIssue{
		headerIssue(paths[1], paths[1], headers[1].check),
		headerIssue(paths[3], paths[3], headers[3].check),
		headerIssue(paths[3], paths[3], headers[3].check),
		nil,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("setRefusedBackings() issues mismatch (-want +got):\n%s", diff)
	}

	setRefusedBackings(nil, nil)
}

// The warning about an unchecked format quotes the format in its command, as it does the path.
func TestUncheckedIssue(t *testing.T) {
	t.Parallel()

	want := &FormatIssue{
		Level:   IssueWarning,
		Problem: "/f/a b is a my fmt image, a format whose backing chain phenix does not check",
		Fix:     "Convert it to qcow2",
		Command: "qemu-img convert -f 'my fmt' -O qcow2 -o compat=1.1 '/f/a b' <new image>",
	}

	if diff := cmp.Diff(want, uncheckedIssue("/f/a b", "'/f/a b'", "my fmt")); diff != "" {
		t.Errorf("uncheckedIssue() mismatch (-want +got):\n%s", diff)
	}
}

// readHeader reads the header of a file it can open, a header it cannot parse included, and nothing else.
func TestReadHeader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }

	diskchaintest.WriteQcow2(t, path("v2.qc2"), diskchaintest.Qcow2{Version: 2})
	diskchaintest.WriteQcow2(t, path("v9.qc2"), diskchaintest.Qcow2{Version: 9})
	diskchaintest.WriteFile(t, path("bad.qc2"), badClusterBits())
	diskchaintest.WriteFile(t, path("raw.img"), []byte("raw"))

	if err := os.Mkdir(path("sub"), 0o750); err != nil {
		t.Fatalf("os.Mkdir(sub) error = %v", err)
	}

	type view struct {
		Read    bool
		Format  diskchain.Format
		Version int
		Problem diskchain.Kind
		At      string
	}

	tests := map[string]view{
		"v2.qc2":  {Read: true, Format: diskchain.FormatQcow2, Version: 2, At: ""},
		"v9.qc2":  {Read: true, Format: diskchain.FormatQcow2, Version: 9, Problem: diskchain.BadVersion, At: "/shown"},
		"bad.qc2": {Read: true, Format: diskchain.FormatQcow2, Version: 3, Problem: diskchain.Unreadable, At: "/shown"},
		"raw.img": {Read: true, Format: diskchain.FormatOther},
		"gone":    {},
		"sub":     {},
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ih := readHeader(path(name), "/shown")
			got := view{Read: ih.read, Format: ih.header.Format, Version: ih.header.Version, Problem: ih.check.Problem, At: ih.check.At}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("readHeader(%s) mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// refusedImage lists, from its header alone, only an image that qemu refuses, marked in use when its inode is locked.
func TestRefusedImage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }

	diskchaintest.WriteQcow2(t, path("new.qc2"), diskchaintest.Qcow2{Features: 1 << 6})
	diskchaintest.WriteQcow2(t, path("ok.qc2"), diskchaintest.Qcow2{})
	diskchaintest.WriteFile(t, path("bad.qc2"), badClusterBits())

	stamp, err := stat(path("new.qc2"))
	if err != nil {
		t.Fatalf("stat(new.qc2) error = %v", err)
	}

	for name, locked := range map[string]map[uint64]bool{"free": nil, "in use": {stamp.inode: true}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := refusedImage(path("new.qc2"), locked)
			want := Details{
				Kind: VMImage, Name: "new.qc2", FullPath: path("new.qc2"), Size: humanReadableBytes(stamp.size),
				BackingImages: []string{}, InUse: locked != nil, Format: "qcow2", FormatVersion: 3,
				FormatIssue: &FormatIssue{
					Level:   IssueError,
					Problem: path("new.qc2") + " uses qcow2 features that qemu does not know (incompatible feature bits 0x40)",
					Fix:     "A newer qemu made it. Write a copy with the qemu-img of that qemu",
					Command: "qemu-img convert -O qcow2 -o compat=1.1 " + path("new.qc2") + " <new image>",
				},
			}

			if diff := cmp.Diff(want, got); diff != "" || !ok {
				t.Errorf("refusedImage(new.qc2) = _, %t, want true; mismatch (-want +got):\n%s", ok, diff)
			}
		})
	}

	for _, name := range []string{"ok.qc2", "bad.qc2", "gone.qc2"} {
		if got, ok := refusedImage(path(name), nil); ok || !cmp.Equal(Details{}, got) {
			t.Errorf("refusedImage(%s) = %+v, %t; want nothing, false", name, got, ok)
		}
	}
}
