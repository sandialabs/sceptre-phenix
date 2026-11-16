package disk

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/testenv"
)

// run runs the command in dir with stdin, failing the test on an error, and returns its output.
func run(t *testing.T, dir, stdin, name string, args ...string) []byte {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)

	out, err := cmd.Output()
	if err != nil {
		var (
			stderr  []byte
			exitErr *exec.ExitError
		)

		if errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}

		t.Fatalf("%s %v: %v\n%s%s", name, args, err, out, stderr)
	}

	return out
}

// writeFilesystem makes a file system of size bytes with mkfs and its args (the file comes last), and writes it into
// the disk image at path at offset start.
func writeFilesystem(t *testing.T, path string, start, size int64, mkfs string, args ...string) {
	t.Helper()

	dir := t.TempDir()
	part := filepath.Join(dir, "part.img")

	if err := os.WriteFile(part, nil, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	if err := os.Truncate(part, size); err != nil {
		t.Fatalf("os.Truncate() error = %v", err)
	}

	run(t, dir, "", mkfs, append(args, part)...)

	data, err := os.ReadFile(part)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile() error = %v", err)
	}

	defer func() { _ = f.Close() }()

	if _, err := f.WriteAt(data, start); err != nil {
		t.Fatalf("writing the file system into %s: %v", path, err)
	}
}

// sfdiskPartition is a partition as `sfdisk --json` reports it.
type sfdiskPartition struct {
	Node     string `json:"node"`
	Start    int64  `json:"start"`
	Size     int64  `json:"size"`
	Type     string `json:"type"`
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	Attrs    string `json:"attrs"`
	Bootable bool   `json:"bootable"`
}

// fdiskView is what sfdisk and blkid report of a partition, in the fields Inspect shares with them.
type fdiskView struct {
	Number       int
	Start, Size  int64
	TypeID, GUID string
	Name         string
	Bootable     bool
	Filesystem   string
	Label        string
}

// fdiskReport returns what `sfdisk --json` and `blkid -p` report of the partitions of the raw image at path.
func fdiskReport(t *testing.T, path string) (string, []fdiskView) {
	t.Helper()

	var table struct {
		PartitionTable struct {
			Label      string            `json:"label"`
			Partitions []sfdiskPartition `json:"partitions"`
		} `json:"partitiontable"`
	}

	if err := json.Unmarshal(run(t, "", "", "sfdisk", "--json", path), &table); err != nil {
		t.Fatalf("decoding sfdisk --json: %v", err)
	}

	views := make([]fdiskView, 0, len(table.PartitionTable.Partitions))

	for _, p := range table.PartitionTable.Partitions {
		number, err := strconv.Atoi(strings.TrimPrefix(p.Node, path))
		if err != nil {
			t.Fatalf("partition node %q does not end in its number", p.Node)
		}

		view := fdiskView{
			Number: number, Start: p.Start * 512, Size: p.Size * 512, TypeID: strings.ToUpper(p.Type), GUID: p.UUID,
			Name: p.Name, Bootable: p.Bootable || strings.Contains(p.Attrs, "LegacyBIOSBootable"),
		}

		if table.PartitionTable.Label == "dos" {
			view.TypeID = "0X" + view.TypeID
		}

		// an extended partition holds no file system; blkid reports its link sectors
		if view.TypeID != "0X5" {
			view.Filesystem, view.Label = blkid(t, path, view.Start, view.Size)
		}

		views = append(views, view)
	}

	return table.PartitionTable.Label, views
}

// blkid returns the file system type and label blkid finds in the size bytes at start of the image at path.
func blkid(t *testing.T, path string, start, size int64) (string, string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "blkid", "-p", "-o", "export",
		"-O", strconv.FormatInt(start, 10), "-S", strconv.FormatInt(size, 10), path)

	// blkid exits 2 when it finds nothing
	out, _ := cmd.Output()

	fields := make(map[string]string)

	for line := range strings.Lines(string(out)) {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			fields[key] = value
		}
	}

	return fields["TYPE"], fields["LABEL"]
}

// inspectView is what Inspect found, in the fields it shares with sfdisk and blkid.
func inspectView(found Inspection) []fdiskView {
	views := make([]fdiskView, 0, len(found.Partitions))

	for _, p := range found.Partitions {
		view := fdiskView{
			Number: p.Number, Start: p.Start, Size: p.Size, TypeID: strings.ToUpper(p.TypeID), GUID: p.GUID,
			Name: p.Name, Bootable: p.Bootable,
		}

		// sfdisk writes an MBR type without its leading zero
		if rest, ok := strings.CutPrefix(view.TypeID, "0X0"); ok {
			view.TypeID = "0X" + rest
		}

		if p.Filesystem != nil && p.Filesystem.Type != "unknown" {
			view.Filesystem, view.Label = p.Filesystem.Type, p.Filesystem.Label
		}

		views = append(views, view)
	}

	return views
}

// Inspect reads the partition tables that sfdisk writes, with the file systems mkfs makes, as sfdisk and blkid read
// them: in a raw image, the qcow2 and qcow copies of it, and a qcow2 overlay, through qemu-img dd. CI installs
// qemu-img, fdisk and dosfstools.
func TestInspectMatchesFdisk(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img", "qemu-io", "sfdisk", "blkid", "mkfs.ext4", "mkfs.vfat", "mkswap")

	ClearCache()
	t.Cleanup(ClearCache)

	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		t.Fatalf("exec.LookPath(qemu-img) error = %v", err)
	}

	const mib = 1 << 20

	tables := map[string]struct {
		script string
		fs     func(t *testing.T, path string)
		label  string
	}{
		"mbr": {
			label: "dos",
			script: "label: dos\nlabel-id: 0x4b414c49\n" +
				"start=2048, size=32768, type=83, bootable\n" +
				"start=34816, size=16384, type=c\n" +
				"start=51200, type=5\n" +
				"size=16384, type=82\n" +
				"type=83\n",
			fs: func(t *testing.T, path string) {
				t.Helper()
				writeFilesystem(t, path, 2048*512, 16*mib, "mkfs.ext4", "-q", "-F", "-L", "rootfs")
				writeFilesystem(t, path, 34816*512, 8*mib, "mkfs.vfat", "-n", "KALI")
				writeFilesystem(t, path, (51200+2048)*512, 8*mib, "mkswap", "-L", "swap1")
				writeFilesystem(t, path, (51200+2048+16384+2048)*512, 4*mib, "mkfs.ext4", "-q", "-F", "-t", "ext2", "-L", "data")
			},
		},
		"gpt": {
			label: "gpt",
			script: "label: gpt\n" +
				"start=2048, size=16384, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B, name=\"EFI system partition\"\n" +
				"start=18432, size=65536, type=4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709, name=root, attrs=LegacyBIOSBootable\n" +
				"start=83968, size=16384, type=0657FD6D-A4AB-43C4-84E5-0933C84B4F4F, name=swap\n",
			fs: func(t *testing.T, path string) {
				t.Helper()
				writeFilesystem(t, path, 2048*512, 8*mib, "mkfs.vfat", "-F", "32", "-n", "EFI")
				writeFilesystem(t, path, 18432*512, 32*mib, "mkfs.ext4", "-q", "-F", "-L", "kali")
				writeFilesystem(t, path, 83968*512, 8*mib, "mkswap")
			},
		},
	}

	for name, tt := range tables {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			raw := filepath.Join(dir, "kali.raw")

			if err := os.WriteFile(raw, nil, 0o600); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}

			if err := os.Truncate(raw, 64*mib); err != nil {
				t.Fatalf("os.Truncate() error = %v", err)
			}

			run(t, dir, tt.script, "sfdisk", "-q", raw)
			tt.fs(t, raw)

			label, want := fdiskReport(t, raw)
			if label != tt.label {
				t.Fatalf("sfdisk reports a %q table, want %q", label, tt.label)
			}

			run(t, dir, "", "qemu-img", "convert", "-f", "raw", "-O", "qcow2", raw, "kali.qc2")
			run(t, dir, "", "qemu-img", "convert", "-f", "raw", "-O", "qcow", raw, "kali.qcow")
			run(t, dir, "", "qemu-img", "create", "-q", "-f", "qcow2", "-F", "qcow2", "-b", "kali.qc2", "over.qc2")

			for image, format := range map[string]string{"kali.raw": "raw", "kali.qc2": "qcow2", "kali.qcow": "qcow", "over.qc2": "qcow2"} {
				found, _ := inspect(t.Context(), Details{FullPath: filepath.Join(dir, image), Format: format, Kind: VMImage}, qemuImg)
				if found.PartitionsUnavailable != "" {
					t.Fatalf("inspect(%s) partitionsUnavailable = %q", image, found.PartitionsUnavailable)
				}

				if want := map[string]string{"dos": "mbr", "gpt": "gpt"}[label]; found.PartitionTable != want {
					t.Errorf("inspect(%s) partitionTable = %q, want %q", image, found.PartitionTable, want)
				}

				if found.VirtualBytes != 64*mib {
					t.Errorf("inspect(%s) virtualBytes = %d, want %d", image, found.VirtualBytes, 64*mib)
				}

				if diff := cmp.Diff(want, inspectView(found)); diff != "" {
					t.Errorf("inspect(%s) mismatch with sfdisk and blkid (-want +got):\n%s", image, diff)
				}
			}

			// what the overlay writes is what phenix reads, not its backing file
			last := want[len(want)-1]
			run(t, dir, "", "qemu-io", "-f", "qcow2", "-c", "write -z "+strconv.FormatInt(last.Start, 10)+" 64k", "over.qc2")

			found, _ := inspect(t.Context(), Details{FullPath: filepath.Join(dir, "over.qc2"), Format: "qcow2", Kind: VMImage}, qemuImg)
			if got := found.Partitions[len(found.Partitions)-1].Filesystem; got == nil || got.Type != "unknown" {
				t.Errorf("inspect(over.qc2) after the overlay cleared the last partition = %+v, want unknown", got)
			}
		})
	}
}
