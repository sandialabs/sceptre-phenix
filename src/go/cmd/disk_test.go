package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/disk"
	"phenix/api/disk/disktest"
	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

func TestParseDiskKinds(t *testing.T) {
	t.Parallel()

	all := disk.VMImage | disk.ContainerImage | disk.ISOImage | disk.UNKNOWN

	tests := map[string]struct {
		list    string
		want    disk.Kind
		wantErr error
	}{
		"empty":            {list: "", want: all},
		"spaces":           {list: "  ", want: all},
		"one":              {list: "vm", want: disk.VMImage},
		"several":          {list: "vm,iso", want: disk.VMImage | disk.ISOImage},
		"case and spaces":  {list: " ISO , Container ", want: disk.ISOImage | disk.ContainerImage},
		"unknown kind":     {list: "unknown", want: disk.UNKNOWN},
		"repeated":         {list: "vm,vm", want: disk.VMImage},
		"not a kind":       {list: "vm,qcow2", wantErr: errUnknownDiskKind},
		"empty name":       {list: "vm,", wantErr: errUnknownDiskKind},
		"only a separator": {list: ",", wantErr: errUnknownDiskKind},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := parseDiskKinds(tt.list)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("parseDiskKinds(%q) error = %v, want %v", tt.list, err, tt.wantErr)
			}

			if err == nil && got != tt.want {
				t.Errorf("parseDiskKinds(%q) = %v, want %v", tt.list, got, tt.want)
			}
		})
	}
}

// A list of kind names in any case, with spaces around them, selects exactly
// those kinds; a list with any other name is refused.
func TestParseDiskKindsProperties(t *testing.T) {
	t.Parallel()

	kinds := map[string]disk.Kind{
		"vm": disk.VMImage, "container": disk.ContainerImage, "iso": disk.ISOImage, "unknown": disk.UNKNOWN,
	}

	rapid.Check(t, func(t *rapid.T) {
		names := rapid.SliceOfN(rapid.SampledFrom([]string{"vm", "container", "iso", "unknown"}), 1, 6).Draw(t, "names")
		bad := rapid.StringMatching(`[a-z0-9]{0,6}`).Draw(t, "bad")

		var (
			parts = make([]string, 0, len(names))
			want  disk.Kind
		)

		for _, n := range names {
			want |= kinds[n]

			if rapid.Bool().Draw(t, "upper") {
				n = strings.ToUpper(n)
			}

			parts = append(parts, rapid.SampledFrom([]string{"", " "}).Draw(t, "pad")+n)
		}

		got, err := parseDiskKinds(strings.Join(parts, ","))
		if err != nil || got != want {
			t.Fatalf("parseDiskKinds(%q) = %v, %v, want %v", strings.Join(parts, ","), got, err, want)
		}

		if _, known := kinds[bad]; known {
			return
		}

		list := strings.Join(append(parts, bad), ",")
		if _, err := parseDiskKinds(list); !errors.Is(err, errUnknownDiskKind) {
			t.Fatalf("parseDiskKinds(%q) error = %v, want %v", list, err, errUnknownDiskKind)
		}
	})
}

// useDiskCmdFiles makes a new <PhenixBase>/images the minimega files directory, with an empty store, a fake
// qemu-img and a fake minimega that answers every command with an empty success, and returns the directory.
func useDiskCmdFiles(t *testing.T) string {
	t.Helper()

	expCmdUseStore(t)
	mmtest.Use(t, nil)
	disktest.UseFakeQemuImg(t)

	disk.ClearCache()
	t.Cleanup(disk.ClearCache)

	dir := filepath.Join(common.PhenixBase, "images")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("making the files directory: %v", err)
	}

	return dir
}

// runDiskCmd runs phenix disk with args and returns its output and error.
func runDiskCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := newDiskCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

// writeDiskFile writes contents to the file at path.
func writeDiskFile(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestDiskListAndChecksum(t *testing.T) {
	dir := useDiskCmdFiles(t)

	writeDiskFile(t, dir+"/base.qc2", "base")
	writeDiskFile(t, dir+"/boot.iso", "boot")

	out, err := runDiskCmd(t, "list")
	if err != nil {
		t.Fatalf("disk list error = %v", err)
	}

	for _, want := range []string{"base.qc2", "boot.iso", "VM", "ISO"} {
		if !strings.Contains(out, want) {
			t.Errorf("disk list = %q, want it to contain %q", out, want)
		}
	}

	out, err = runDiskCmd(t, "list", "--type", "iso")
	if err != nil {
		t.Fatalf("disk list --type iso error = %v", err)
	}

	if !strings.Contains(out, "boot.iso") || strings.Contains(out, "base.qc2") {
		t.Errorf("disk list --type iso = %q, want boot.iso and not base.qc2", out)
	}

	if _, err := runDiskCmd(t, "list", "--type", "qcow2"); !errors.Is(err, errUnknownDiskKind) {
		t.Errorf("disk list --type qcow2 error = %v, want %v", err, errUnknownDiskKind)
	}

	out, err = runDiskCmd(t, "checksum", "base.qc2")
	if err != nil {
		t.Fatalf("disk checksum base.qc2 error = %v", err)
	}

	sum := sha256.Sum256([]byte("base"))
	if want := hex.EncodeToString(sum[:]) + "  " + dir + "/base.qc2\n"; out != want {
		t.Errorf("disk checksum base.qc2 = %q, want %q", out, want)
	}

	if _, err := runDiskCmd(t, "checksum", "../outside.qc2"); !errors.Is(err, disk.ErrNotManaged) {
		t.Errorf("disk checksum ../outside.qc2 error = %v, want %v", err, disk.ErrNotManaged)
	}
}

func TestDiskTablePath(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		d    disk.Details
		want string
	}{
		"regular file":     {d: disk.Details{RelativePath: "base.qc2", FullPath: "/files/base.qc2"}, want: "base.qc2"},
		"outside the dir":  {d: disk.Details{FullPath: "/other/base.qc2"}, want: "/other/base.qc2"},
		"relative symlink": {d: disk.Details{RelativePath: "link.qc2", LinkTarget: "win/win10.qc2"}, want: "link.qc2 -> win/win10.qc2"},
		"absolute symlink": {
			d:    disk.Details{FullPath: "/other/link.qc2", LinkTarget: "/files/base.qc2"},
			want: "/other/link.qc2 -> /files/base.qc2",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := diskTablePath(tt.d); got != tt.want {
				t.Errorf("diskTablePath(%+v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// diskTableColumn reads the table of disk list into the value of one column for each image name.
func diskTableColumn(t *testing.T, table, column string) map[string]string {
	t.Helper()

	var (
		header []string
		got    = map[string]string{}
	)

	for line := range strings.Lines(table) {
		if !strings.HasPrefix(line, "|") {
			continue
		}

		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}

		if header == nil {
			header = cells

			continue
		}

		if i := slices.Index(header, column); i >= 0 && i < len(cells) {
			got[cells[0]] = cells[i]
		}
	}

	return got
}

// The Path column of disk list shows what an image that is a symlink points to, and only the path of a regular file.
func TestDiskListSymlink(t *testing.T) {
	dir := useDiskCmdFiles(t)

	writeDiskFile(t, dir+"/base.qc2", "base")

	if err := os.Symlink("base.qc2", dir+"/link.qc2"); err != nil {
		t.Fatalf("making the symlink: %v", err)
	}

	out, err := runDiskCmd(t, "list")
	if err != nil {
		t.Fatalf("disk list error = %v", err)
	}

	want := map[string]string{"base.qc2": "base.qc2", "link.qc2": "link.qc2 -> base.qc2"}
	if diff := cmp.Diff(want, diskTableColumn(t, out, "PATH")); diff != "" {
		t.Errorf("disk list Path column mismatch (-want +got):\n%s", diff)
	}
}

func TestDiskCloneRenameDelete(t *testing.T) {
	dir := useDiskCmdFiles(t)

	writeDiskFile(t, dir+"/base.qc2", "base")
	writeDiskFile(t, dir+"/snap.qc2", "snap")
	// the fake qemu-img reports snap.qc2 as backed by base.qc2
	writeDiskFile(t, dir+"/snap.qc2.backing", "base.qc2")

	out, err := runDiskCmd(t, "clone", "base.qc2", "copy")
	if err != nil {
		t.Fatalf("disk clone base.qc2 copy error = %v", err)
	}

	if want := dir + "/copy.qcow2\n"; out != want {
		t.Errorf("disk clone base.qc2 copy = %q, want %q", out, want)
	}

	if b, err := os.ReadFile(dir + "/copy.qcow2"); err != nil || string(b) != "base" {
		t.Errorf("reading the clone = %q, %v, want %q", b, err, "base")
	}

	if _, err := runDiskCmd(t, "clone", "base.qc2", "copy.qcow2"); !errors.Is(err, disk.ErrExists) {
		t.Errorf("disk clone base.qc2 copy.qcow2 (exists) error = %v, want %v", err, disk.ErrExists)
	}

	if _, err := runDiskCmd(t, "clone", "base.qc2", "copy.iso"); !errors.Is(err, disk.ErrWrongExtension) {
		t.Errorf("disk clone base.qc2 copy.iso error = %v, want %v", err, disk.ErrWrongExtension)
	}

	if _, err := runDiskCmd(t, "rename", "copy.qcow2", "moved"); err != nil {
		t.Fatalf("disk rename copy.qcow2 moved error = %v", err)
	}

	if _, err := os.Stat(dir + "/moved.qcow2"); err != nil {
		t.Errorf("after disk rename, stat moved.qcow2 error = %v, want none", err)
	}

	_, err = runDiskCmd(t, "delete", "base.qc2")

	var inUse *disk.InUseError
	if !errors.As(err, &inUse) || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("disk delete base.qc2 (backs snap.qc2) error = %v, want a *disk.InUseError that names --force", err)
	}

	if _, err := runDiskCmd(t, "delete", "base.qc2", "--force"); err != nil {
		t.Fatalf("disk delete base.qc2 --force error = %v", err)
	}

	if _, err := os.Stat(dir + "/base.qc2"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after disk delete --force, stat base.qc2 error = %v, want %v", err, os.ErrNotExist)
	}
}

func TestDiskArgumentErrors(t *testing.T) {
	dir := useDiskCmdFiles(t)

	writeDiskFile(t, dir+"/a.qc2", "a")

	tests := map[string]struct {
		args    []string
		wantErr error
	}{
		"resize to a size without a unit": {args: []string{"resize", "a.qc2", "10"}, wantErr: disk.ErrInvalidSize},
		"resize to a size with a command": {args: []string{"resize", "a.qc2", "10G;ls"}, wantErr: disk.ErrInvalidSize},
		"snapshot to a folder":            {args: []string{"snapshot", "a.qc2", "sub/"}, wantErr: disk.ErrNotManaged},
		"snapshot outside the files":      {args: []string{"snapshot", "a.qc2", "../../b"}, wantErr: disk.ErrNotManaged},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := runDiskCmd(t, tt.args...); !errors.Is(err, tt.wantErr) {
				t.Errorf("disk %q error = %v, want %v", tt.args, err, tt.wantErr)
			}
		})
	}

	for _, args := range [][]string{
		{"rebase", "a.qc2"},
		{"rebase", "a.qc2", "b.qc2", "--independent"},
		{"details"},
		{"delete", "a.qc2", "b.qc2"},
	} {
		if _, err := runDiskCmd(t, args...); err == nil {
			t.Errorf("disk %q error = nil, want an error", args)
		}
	}
}
