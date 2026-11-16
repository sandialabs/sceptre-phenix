package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"phenix/api/disk"
)

// diskOutputImage is the part of an image of GET /disks that the tests check.
type diskOutputImage struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	RelativePath string `json:"relativePath"`
}

// diskOutputChecksum is the part of the body of GET /disks/checksum that the
// tests check.
type diskOutputChecksum struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// diskOutputDetails is the part of the body of GET /disks/details that the
// tests check.
type diskOutputDetails struct {
	FullPath string `json:"fullPath"`
}

// disk list, details and checksum print their tables as before, and the
// bodies of the API with -o json and -o yaml.
func TestDiskOutput(t *testing.T) {
	dir := useDiskCmdFiles(t)

	writeDiskFile(t, dir+"/base.qc2", "base")
	writeDiskFile(t, dir+"/boot.iso", "boot")

	run := func(args ...string) func(...string) (string, error) {
		return func(more ...string) (string, error) {
			return runDiskCmd(t, append(append([]string{}, args...), more...)...)
		}
	}

	images := []diskOutputImage{
		{Kind: "VM", Name: "base.qc2", RelativePath: "base.qc2"}, {Kind: "ISO", Name: "boot.iso", RelativePath: "boot.iso"},
	}

	if table := checkOutputFormats(t, "phenix disk list", run("list"), images); !strings.Contains(table, "base.qc2") {
		t.Errorf("phenix disk list = %q, want base.qc2", table)
	}

	if table := checkOutputFormats(t, "phenix disk list --type container", run("list", "--type", "container"),
		[]diskOutputImage{}); table != "No disk images\n" {
		t.Errorf("phenix disk list --type container = %q, want %q", table, "No disk images\n")
	}

	sum := sha256.Sum256([]byte("base"))
	checksum := diskOutputChecksum{Path: dir + "/base.qc2", Size: 4, SHA256: hex.EncodeToString(sum[:])}

	table := checkOutputFormats(t, "phenix disk checksum base.qc2", run("checksum", "base.qc2"), checksum)
	if want := checksum.SHA256 + "  " + checksum.Path + "\n"; table != want {
		t.Errorf("phenix disk checksum base.qc2 = %q, want %q", table, want)
	}

	details := checkOutputFormats(t, "phenix disk details base.qc2", run("details", "base.qc2"),
		diskOutputDetails{FullPath: dir + "/base.qc2"})
	if !strings.Contains(details, "FIELD") || !strings.Contains(details, dir+"/base.qc2") {
		t.Errorf("phenix disk details base.qc2 = %q, want the table of fields", details)
	}
}

// The disk changes with --dry-run check what the change checks, print what
// they would change, and change no file.
func TestDiskDryRun(t *testing.T) {
	dir := useDiskCmdFiles(t)

	writeDiskFile(t, dir+"/base.qc2", "base")
	writeDiskFile(t, dir+"/snap.qc2", "snap")
	// the fake qemu-img reports snap.qc2 as backed by base.qc2
	writeDiskFile(t, dir+"/snap.qc2.backing", "base.qc2")

	var inUse *disk.InUseError

	tests := []struct {
		args    []string
		want    string
		wantErr error
		wantAs  any
	}{
		{args: []string{"delete", "snap.qc2"}, want: "would delete disk image " + dir + "/snap.qc2\n"},
		{args: []string{"delete", "base.qc2"}, wantAs: &inUse},
		{args: []string{"delete", "base.qc2", "--force"}, want: "would delete disk image " + dir + "/base.qc2\n"},
		{args: []string{"delete", "none.qc2"}, wantErr: os.ErrNotExist},
		{args: []string{"rename", "snap.qc2", "moved"}, want: "would rename disk image " + dir + "/snap.qc2 to " + dir + "/moved.qcow2\n"},
		{args: []string{"rename", "snap.qc2", "base.qc2"}, wantErr: disk.ErrExists},
		{
			args: []string{"commit", "snap.qc2"},
			want: "would commit disk image " + dir + "/snap.qc2 into its backing image " + dir + "/base.qc2\n",
		},
		{
			args: []string{"rebase", "snap.qc2", "base.qc2"},
			want: "would rebase disk image " + dir + "/snap.qc2 onto " + dir + "/base.qc2\n",
		},
		{
			args: []string{"rebase", "snap.qc2", "--independent"},
			want: "would remove the backing image of disk image " + dir + "/snap.qc2\n",
		},
		{args: []string{"rebase", "snap.qc2", "../outside.qc2"}, wantErr: disk.ErrNotManaged},
		{args: []string{"resize", "base.qc2", "+1G"}, want: "would resize disk image " + dir + "/base.qc2 by +1G\n"},
		{args: []string{"resize", "none.qc2", "+1G"}, wantErr: os.ErrNotExist},
	}

	for _, tt := range tests {
		args := append(append([]string{}, tt.args...), "--dry-run")

		out, err := runDiskCmd(t, args...)

		switch {
		case tt.wantAs != nil:
			if !errors.As(err, tt.wantAs) {
				t.Errorf("phenix disk %q error = %v, want %T", args, err, tt.wantAs)
			}
		case !errors.Is(err, tt.wantErr):
			t.Errorf("phenix disk %q error = %v, want %v", args, err, tt.wantErr)
		}

		if err == nil && out != tt.want {
			t.Errorf("phenix disk %q = %q, want %q", args, out, tt.want)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if got, want := strings.Join(names, " "), "base.qc2 snap.qc2 snap.qc2.backing"; got != want {
		t.Errorf("after the dry runs, %s holds %s, want %s", dir, got, want)
	}

	if b, _ := os.ReadFile(dir + "/snap.qc2.backing"); string(b) != "base.qc2" {
		t.Errorf("after the dry runs, snap.qc2 is backed by %q, want base.qc2", b)
	}
}
