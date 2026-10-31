package disk

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/disk/disktest"
	dct "phenix/util/diskchain/diskchaintest"
)

// fakeQemuImg writes an executable script named qemu-img in a new folder and returns its path.
func fakeQemuImg(t *testing.T, script string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "qemu-img")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

// qemuDD is a fake `qemu-img dd` that reads the image named in its raw node as a raw file, and counts its runs in
// the file runs next to it.
const qemuDD = `dir=$(dirname "$0")
echo run >> "$dir/runs"
for arg; do
	case $arg in
	if=*) opts=${arg#if=} ;;
	of=*) out=${arg#of=} ;;
	esac
done
offset=$(echo "$opts" | sed 's/.*offset=\([0-9]*\).*/\1/')
size=$(echo "$opts" | sed 's/.*,size=\([0-9]*\).*/\1/')
file=$(echo "$opts" | sed 's/.*file.file.filename=//; s/,,/,/g')
tail -c +$((offset + 1)) "$file" | head -c "$size" > "$out"
`

func TestInspectReasons(t *testing.T) {
	dir := t.TempDir()

	qcow2 := filepath.Join(dir, "kali.qc2")
	dct.WriteQcow2(t, qcow2, dct.Qcow2{})

	broken := filepath.Join(dir, "broken.qc2")
	dct.WriteQcow2(t, broken, dct.Qcow2{Backing: "missing.qc2", BackingFormat: "qcow2"})

	luks := filepath.Join(dir, "luks.qc2")
	dct.WriteQcow2(t, luks, dct.Qcow2{Encryption: 2})

	vmdk := filepath.Join(dir, "kali.vmdk")
	writeFile(t, vmdk, "KDMV")

	tgz := filepath.Join(dir, "kali_rootfs.tgz")
	writeFile(t, tgz, "archive")

	fails := fakeQemuImg(t, "echo \"qemu-img: Could not open 'kali.qc2'\" >&2\nexit 1\n")

	tests := map[string]struct {
		image   Details
		qemuImg string
		want    func(found Inspection) string
	}{
		"a file phenix cannot open": {
			image: Details{FullPath: filepath.Join(dir, "gone.qc2"), Format: "qcow2", Kind: VMImage},
			want: func(found Inspection) string {
				return found.FileUnavailable + " | " + found.PartitionsUnavailable
			},
		},
		"a container image": {
			image: Details{FullPath: tgz, Format: "raw", Kind: ContainerImage},
			want:  func(found Inspection) string { return found.PartitionsUnavailable },
		},
		"no qemu-img": {
			image: Details{FullPath: qcow2, Format: "qcow2", Kind: VMImage},
			want:  func(found Inspection) string { return found.PartitionsUnavailable },
		},
		"a broken chain": {
			image:   Details{FullPath: broken, Format: "qcow2", Kind: VMImage},
			qemuImg: fails,
			want:    func(found Inspection) string { return found.PartitionsUnavailable },
		},
		"an image qemu refuses": {
			image:   Details{FullPath: luks, Format: "qcow2", Kind: VMImage},
			qemuImg: fails,
			want:    func(found Inspection) string { return found.PartitionsUnavailable },
		},
		"a format phenix does not read": {
			image: Details{FullPath: vmdk, Format: "vmdk", Kind: VMImage},
			want:  func(found Inspection) string { return found.PartitionsUnavailable },
		},
		"a header that is not the listed format": {
			image: Details{FullPath: vmdk, Format: "qcow2", Kind: VMImage},
			want:  func(found Inspection) string { return found.PartitionsUnavailable },
		},
		"a read error": {
			image:   Details{FullPath: qcow2, Format: "qcow2", Kind: VMImage},
			qemuImg: fails,
			want:    func(found Inspection) string { return found.PartitionsUnavailable },
		},
	}

	want := map[string]string{
		"a file phenix cannot open": "phenix cannot open the image (no such file or directory). If minimega runs on a different " +
			"host, only minimega can read it | phenix cannot open the image (no such file or directory). If minimega " +
			"runs on a different host, only minimega can read it",
		"a container image": "a container image holds a file system archive, not a disk with partitions",
		"no qemu-img":       "qemu-img is not installed where phenix runs, so phenix cannot read the partitions of a qcow2 image",
		"a broken chain":    "phenix cannot read the partitions: backing file " + dir + "/missing.qc2 is missing",
		"an image qemu refuses": "phenix cannot read the partitions: " + luks +
			" uses LUKS encryption, and phenix cannot give qemu its key",
		"a format phenix does not read":          "phenix reads partitions only in raw, qcow2 and qcow images, not in vmdk images",
		"a header that is not the listed format": "phenix cannot read the partitions: the image does not have a qcow2 header",
		"a read error":                           "phenix cannot read the partitions: qemu-img: Could not open 'kali.qc2'",
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			found, _ := inspect(t.Context(), tt.image, tt.qemuImg)

			if diff := cmp.Diff(want[name], tt.want(found)); diff != "" {
				t.Errorf("inspect(%s) reason mismatch (-want +got):\n%s", tt.image.FullPath, diff)
			}

			if found.PartitionTable != "" || len(found.Partitions) != 0 {
				t.Errorf(
					"inspect(%s) = table %q with %d partitions, want none",
					tt.image.FullPath,
					found.PartitionTable,
					len(found.Partitions),
				)
			}
		})
	}
}

// A run cut short by its deadline says so and is not kept.
func TestInspectTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kali.qc2")
	dct.WriteQcow2(t, path, dct.Qcow2{})

	slow := fakeQemuImg(t, "exec sleep 30\n")

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	found, stamps := inspect(ctx, Details{FullPath: path, Format: "qcow2", Kind: VMImage}, slow)

	if want := "phenix stopped reading the partitions after 30s"; found.PartitionsUnavailable != want {
		t.Errorf("inspect() of a qemu-img that hangs reason = %q, want %q", found.PartitionsUnavailable, want)
	}

	if stamps != nil {
		t.Errorf("inspect() of a qemu-img that hangs stamps = %v, want nil so it is not kept", stamps)
	}

	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("inspect() of a qemu-img that hangs took %s, want it stopped at the deadline", elapsed)
	}
}

func TestInspectRawImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kali.hdd")
	disktest.WriteMBRImage(t, path)

	found, stamps := inspect(t.Context(), Details{FullPath: path, Format: "raw", Kind: VMImage}, "")
	if len(stamps) != 1 {
		t.Errorf("inspect() stamps = %v, want the image's", stamps)
	}

	ext4, swap := &Filesystem{Type: "ext4", Label: "rootfs"}, &Filesystem{Type: "swap"}
	want := Inspection{
		FullPath: path, Format: "raw", FileBytes: 8 << 20, VirtualBytes: 8 << 20,
		PartitionTable: "mbr", DiskID: "0xdeadbeef", SectorSize: 512,
		Partitions: []Partition{
			{Number: 1, Type: "Linux", TypeID: "0x83", Start: 2048 * 512, Size: 4096 * 512, Bootable: true, Filesystem: ext4},
			{Number: 2, Type: "Linux swap / Solaris", TypeID: "0x82", Start: 6144 * 512, Size: 16384*512 - 6144*512, Filesystem: swap},
		},
		PartitionNotes: []string{"partition 2 ends past the end of the disk, so phenix shows it cut"},
	}

	opts := cmp.Options{cmpopts.IgnoreFields(Inspection{}, "AllocatedBytes", "Modified"), cmpopts.EquateEmpty()}
	if diff := cmp.Diff(want, found, opts); diff != "" {
		t.Errorf("inspect() mismatch (-want +got):\n%s", diff)
	}

	if _, err := time.Parse(time.RFC3339, found.Modified); err != nil {
		t.Errorf("inspect() Modified = %q, not RFC 3339: %v", found.Modified, err)
	}
}

// An inspection is kept until a file it read changes, and the disk list's refresh forgets it.
func TestInspectIsCached(t *testing.T) {
	ClearCache()
	t.Cleanup(ClearCache)

	path := filepath.Join(t.TempDir(), "kali.hdd")
	disktest.WriteMBRImage(t, path)

	image := Details{FullPath: path, Format: "raw", Kind: VMImage}
	if got := len(Inspect(t.Context(), image, nil).Partitions); got != 2 {
		t.Fatalf("Inspect() = %d partitions, want 2", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	// the same size and time: the kept result is used
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile() error = %v", err)
	}

	if _, err := f.WriteAt(make([]byte, 512), 0); err != nil {
		t.Fatalf("clearing the MBR: %v", err)
	}

	_ = f.Close()

	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("os.Chtimes() error = %v", err)
	}

	if got := Inspect(t.Context(), image, nil).PartitionTable; got != "mbr" {
		t.Errorf("Inspect() of an unchanged stamp = %q, want the kept mbr", got)
	}

	ClearCache()

	if got := Inspect(t.Context(), image, nil).PartitionTable; got != "none" {
		t.Errorf("Inspect() after ClearCache = %q, want none", got)
	}

	// a new time: read again
	disktest.WriteMBRImage(t, path)

	if err := os.Chtimes(path, info.ModTime().Add(time.Minute), info.ModTime().Add(time.Minute)); err != nil {
		t.Fatalf("os.Chtimes() error = %v", err)
	}

	if got := Inspect(t.Context(), image, nil).PartitionTable; got != "mbr" {
		t.Errorf("Inspect() of a changed image = %q, want mbr", got)
	}
}

func TestOverlays(t *testing.T) {
	images := []Details{
		{FullPath: "/f/z.qc2", BackingImages: []string{"/f/base.qc2"}},
		{FullPath: "/f/a.qc2", BackingImages: []string{"/f/mid.qc2", "/f/base.qc2"}},
		{FullPath: "/f/mid.qc2", BackingImages: []string{"/f/base.qc2"}},
		{FullPath: "/f/base.qc2", BackingImages: []string{}},
		{FullPath: "/f/other.qc2", BackingImages: []string{"/f/else.qc2"}},
	}

	want := []Overlay{{FullPath: "/f/a.qc2"}, {FullPath: "/f/mid.qc2", Direct: true}, {FullPath: "/f/z.qc2", Direct: true}}
	if diff := cmp.Diff(want, overlays("/f/base.qc2", images)); diff != "" {
		t.Errorf("overlays() mismatch (-want +got):\n%s", diff)
	}

	if got := overlays("/f/a.qc2", images); len(got) != 0 {
		t.Errorf("overlays() of an image nothing is backed by = %v, want none", got)
	}
}

func TestVMUses(t *testing.T) {
	dir := useFilesDir(t)

	first := testExperiment(t, "exp1", "2026-10-30T00:00:00Z", "kali.qc2", dir+"/data.qc2")
	second := testExperiment(t, "exp2", "", "other.qc2", "kali.qc2")
	none := 0
	second.Spec.Topology().Nodes()[0].Hardware().Drives()[0].SetInjectPartition(&none)
	storeExperiments(t, first, second)

	got, err := VMUses(dir + "/kali.qc2")
	if err != nil {
		t.Fatalf("VMUses() error = %v", err)
	}

	want := []VMUse{
		{Experiment: "exp1", Running: true, VM: "host", Drive: 0, InjectPartition: 1},
		{Experiment: "exp2", Running: false, VM: "host", Drive: 1, InjectPartition: 0},
	}

	if diff := cmp.Diff(want, got, cmpopts.SortSlices(func(a, b VMUse) bool { return a.Experiment < b.Experiment })); diff != "" {
		t.Errorf("VMUses() mismatch (-want +got):\n%s", diff)
	}
}

func TestQemuReader(t *testing.T) {
	dir := t.TempDir()

	image := filepath.Join(dir, "kali,1.img")
	data := make([]byte, 3*readBlock+1000)

	for i := range data {
		data[i] = byte(i * 7)
	}

	if err := os.WriteFile(image, data, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	fake := fakeQemuImg(t, qemuDD)
	runs := func() int {
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(fake), "runs"))

		return strings.Count(string(b), "run")
	}

	budget := &readBudget{left: 2 * readBlock}
	r := newQemuReader(t.Context(), fake, image, "raw", int64(len(data)), t.TempDir(), budget)

	read := func(off int64, n int) ([]byte, error) {
		p := make([]byte, n)
		got, err := r.ReadAt(p, off)

		return p[:got], err
	}

	// two reads in the first block run qemu-img once
	for _, off := range []int64{0, 512} {
		got, err := read(off, 512)
		if err != nil || !cmp.Equal(got, data[off:off+512]) {
			t.Fatalf("ReadAt(512 at %d) = %d bytes, %v, want the image's bytes", off, len(got), err)
		}
	}

	if got := runs(); got != 1 {
		t.Errorf("qemu-img ran %d times for two reads in one block, want 1", got)
	}

	// the last, short block ends in io.EOF
	got, err := read(3*readBlock+500, 1000)
	if !errors.Is(err, io.EOF) || !cmp.Equal(got, data[3*readBlock+500:]) {
		t.Errorf("ReadAt(past the end) = %d bytes, %v, want the last 500 bytes and io.EOF", len(got), err)
	}

	if _, err := read(int64(len(data)), 10); !errors.Is(err, io.EOF) {
		t.Errorf("ReadAt(at the end) error = %v, want io.EOF", err)
	}

	// the budget of two blocks is spent
	if _, err := read(readBlock, readBlock); !errors.Is(err, errReadLimit) {
		t.Errorf("ReadAt(past the budget) error = %v, want %v", err, errReadLimit)
	}
}

// A kept inspection of an image with a missing backing file ends once the file appears.
func TestInspectNoticesAMissingBackingFile(t *testing.T) {
	ClearCache()
	t.Cleanup(ClearCache)

	dir := t.TempDir()
	path := filepath.Join(dir, "kali.qc2")
	dct.WriteQcow2(t, path, dct.Qcow2{Backing: "base.img", BackingFormat: "raw"})

	// an old folder time, so the new file changes it even on a file system with coarse times
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("os.Chtimes() error = %v", err)
	}

	fails := fakeQemuImg(t, "echo 'qemu-img: no data' >&2\nexit 1\n")
	image := Details{FullPath: path, Format: "qcow2", Kind: VMImage}

	missing := "phenix cannot read the partitions: backing file " + dir + "/base.img is missing"
	for range 2 {
		if got := cachedInspection(t.Context(), image, fails).PartitionsUnavailable; got != missing {
			t.Fatalf("cachedInspection() reason = %q, want %q", got, missing)
		}
	}

	writeFile(t, filepath.Join(dir, "base.img"), "kali")

	want := "phenix cannot read the partitions: qemu-img: no data"
	if got := cachedInspection(t.Context(), image, fails).PartitionsUnavailable; got != want {
		t.Errorf("cachedInspection() after the backing file appeared reason = %q, want %q", got, want)
	}
}

// A missing backing file in a folder that does not exist is not kept, as phenix cannot watch the folder.
func TestInspectDoesNotKeepAnUnwatchedMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kali.qc2")
	missing := filepath.Join(dir, "gone", "base.img")
	dct.WriteQcow2(t, path, dct.Qcow2{Backing: missing, BackingFormat: "raw"})

	fails := fakeQemuImg(t, "echo 'qemu-img: no data' >&2\nexit 1\n")

	found, stamps := inspect(t.Context(), Details{FullPath: path, Format: "qcow2", Kind: VMImage}, fails)

	if want := "phenix cannot read the partitions: backing file " + missing + " is missing"; found.PartitionsUnavailable != want {
		t.Errorf("inspect() reason = %q, want %q", found.PartitionsUnavailable, want)
	}

	if stamps != nil {
		t.Errorf("inspect() stamps = %v, want nil so it is not kept", stamps)
	}
}

// limitedFile reads f as a disk, and returns errReadLimit for a read at limit or after it.
type limitedFile struct {
	f     *os.File
	limit int64
}

func (l limitedFile) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > l.limit {
		return 0, errReadLimit
	}

	return l.f.ReadAt(p, off)
}

func TestReadPartitionsReadLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kali.hdd")
	disktest.WriteMBRImage(t, path)

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open() error = %v", err)
	}

	defer func() { _ = f.Close() }()

	tests := map[string]struct {
		limit int64
		want  Inspection
	}{
		"before the partition table": {
			limit: 0,
			want: Inspection{
				PartitionsUnavailable: "phenix read 16 MiB of the disk, its limit, before it found the partitions",
			},
		},
		"after the partition table": {
			limit: 512,
			want: Inspection{
				PartitionTable: "mbr", DiskID: "0xdeadbeef", SectorSize: 512,
				Partitions: []Partition{
					{Number: 1, Type: "Linux", TypeID: "0x83", Start: 2048 * 512, Size: 4096 * 512, Bootable: true},
					{Number: 2, Type: "Linux swap / Solaris", TypeID: "0x82", Start: 6144 * 512, Size: 16384*512 - 6144*512},
				},
				PartitionNotes: []string{
					"partition 2 ends past the end of the disk, so phenix shows it cut",
					"phenix read 16 MiB of the disk, its limit, so it shows only some file systems",
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			found := emptyInspection()
			readPartitions(t.Context(), &found, limitedFile{f: f, limit: tt.limit}, 8<<20)

			if diff := cmp.Diff(tt.want, found, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("readPartitions() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// phenix says why when it cannot make a folder for the qemu-img output.
func TestInspectNoTempFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kali.qc2")
	dct.WriteQcow2(t, path, dct.Qcow2{})

	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))

	fails := fakeQemuImg(t, "exit 1\n")

	found, _ := inspect(t.Context(), Details{FullPath: path, Format: "qcow2", Kind: VMImage}, fails)

	if want := "phenix cannot read the partitions: no such file or directory"; found.PartitionsUnavailable != want {
		t.Errorf("inspect() reason = %q, want %q", found.PartitionsUnavailable, want)
	}
}
