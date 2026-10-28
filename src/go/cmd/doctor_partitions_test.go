package cmd

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/api/disk"
	"phenix/store"
	v1 "phenix/types/version/v1"
	dct "phenix/util/diskchain/diskchaintest"
	"phenix/util/testenv"
)

const (
	testSector  = 512
	linuxFSGUID = "0FC63DAF-8483-4772-8E79-3D69D8477DE4"

	okInjects = "the 1 inject partition holds a file system that minimega can write files to"
	// mbrParts lists the partitions of the image writeMBRDisk writes.
	mbrParts = "partitions 1 (Linux, ext4, 2.0 MiB), 2 (Linux swap / Solaris, swap, 1.0 MiB), 3 (Extended, 3.0 MiB), " +
		"4 (Linux LVM, LVM2_member, 1.0 MiB), 5 (Linux, ext4, 1.0 MiB), 6 (Linux, unknown, 512.0 KiB); " +
		"try inject_partition: 1"
	gptParts = "partitions 1 (Linux filesystem, ext4, 512.0 MiB), 2 (Linux filesystem, ext4, 2.0 GiB); " +
		"try inject_partition: 2"
)

// testPart is an entry of a test MBR or extended boot record: its type byte, first sector and sector count.
type testPart struct {
	typ            byte
	first, sectors uint32
}

// bootRecord is an MBR or extended boot record with the entries parts.
func bootRecord(parts ...testPart) []byte {
	b := make([]byte, testSector)

	for i, p := range parts {
		e := b[446+16*i:]
		e[4] = p.typ
		binary.LittleEndian.PutUint32(e[8:], p.first)
		binary.LittleEndian.PutUint32(e[12:], p.sectors)
	}

	b[510], b[511] = 0x55, 0xAA

	return b
}

// ext4Start is the start of an ext4 file system: its superblock with the magic and an ext4 feature bit.
func ext4Start() []byte {
	b := make([]byte, 2048)
	binary.LittleEndian.PutUint16(b[1024+0x38:], 0xEF53)
	binary.LittleEndian.PutUint32(b[1024+0x60:], 0x42)

	return b
}

// lvmStart is the start of an LVM physical volume: its label in the second sector.
func lvmStart() []byte {
	b := make([]byte, 2*testSector)
	copy(b[testSector:], "LABELONE")
	copy(b[testSector+24:], "LVM2 001")

	return b
}

// writeTestDisk writes a sparse raw disk of size bytes at path, with each data at its offset.
func writeTestDisk(t *testing.T, path string, size int64, data map[int64][]byte) {
	t.Helper()

	dct.WriteFile(t, path, nil)

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile(%s) error = %v", path, err)
	}

	defer func() { _ = f.Close() }()

	for off, b := range data {
		if _, err := f.WriteAt(b, off); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	if err := f.Truncate(size); err != nil {
		t.Fatalf("truncating %s: %v", path, err)
	}
}

// writeMBRDisk writes a raw disk of 8 MiB at path with an MBR: 1 ext4, 2 swap, 3 extended with the logical
// partitions 5 ext4 and 6 with no file system, and 4 an LVM physical volume.
func writeMBRDisk(t *testing.T, path string) {
	t.Helper()

	sector := func(n int64) int64 { return n * testSector }

	writeTestDisk(t, path, 8<<20, map[int64][]byte{
		0: bootRecord(
			testPart{typ: 0x83, first: 2048, sectors: 4096}, testPart{typ: 0x82, first: 6144, sectors: 2048},
			testPart{typ: 0x05, first: 8192, sectors: 6144}, testPart{typ: 0x8e, first: 14336, sectors: 2048},
		),
		sector(2048):        ext4Start(),
		sector(6144) + 4086: []byte("SWAPSPACE2"),
		// a logical partition starts after its EBR; the link to the next EBR is from the extended partition's start
		sector(8192):  bootRecord(testPart{typ: 0x83, first: 2048, sectors: 2048}, testPart{typ: 0x05, first: 4096, sectors: 2048}),
		sector(10240): ext4Start(),
		sector(12288): bootRecord(testPart{typ: 0x83, first: 1024, sectors: 1024}),
		sector(14336): lvmStart(),
	})
}

// guidOnDisk is the GUID s in the mixed byte order of a GPT.
func guidOnDisk(s string) []byte {
	raw := make([]byte, 0, 16)

	for hex := strings.ReplaceAll(s, "-", ""); len(hex) > 0; hex = hex[2:] {
		var v byte

		for _, c := range hex[:2] {
			v = v<<4 | byte(strings.IndexRune("0123456789ABCDEF", c))
		}

		raw = append(raw, v)
	}

	g := make([]byte, 16)
	binary.LittleEndian.PutUint32(g[0:], binary.BigEndian.Uint32(raw[0:]))
	binary.LittleEndian.PutUint16(g[4:], binary.BigEndian.Uint16(raw[4:]))
	binary.LittleEndian.PutUint16(g[6:], binary.BigEndian.Uint16(raw[6:]))
	copy(g[8:], raw[8:])

	return g
}

// writeGPT writes a sparse raw disk of size bytes at path with a GPT of Linux partitions, each its first sector and
// sector count, and each data at its offset.
func writeGPT(t *testing.T, path string, size int64, parts [][2]uint64, data map[int64][]byte) {
	t.Helper()

	last := uint64(size/testSector - 1)
	count := max(4, len(parts))
	entries := make([]byte, (count*128+testSector-1)/testSector*testSector)

	for i, p := range parts {
		e := entries[128*i:]
		copy(e, guidOnDisk(linuxFSGUID))
		copy(e[16:], guidOnDisk(fmt.Sprintf("11111111-2222-3333-4444-%012X", i+1)))
		binary.LittleEndian.PutUint64(e[32:], p[0])
		binary.LittleEndian.PutUint64(e[40:], p[0]+p[1]-1)
	}

	h := make([]byte, testSector)
	copy(h, "EFI PART")
	binary.LittleEndian.PutUint32(h[8:], 0x00010000)
	binary.LittleEndian.PutUint32(h[12:], 92)
	binary.LittleEndian.PutUint64(h[24:], 1)
	binary.LittleEndian.PutUint64(h[32:], last)
	binary.LittleEndian.PutUint64(h[40:], 34)
	binary.LittleEndian.PutUint64(h[48:], last-33)
	binary.LittleEndian.PutUint64(h[72:], 2)
	binary.LittleEndian.PutUint32(h[80:], uint32(count))
	binary.LittleEndian.PutUint32(h[84:], 128)
	binary.LittleEndian.PutUint32(h[88:], crc32.ChecksumIEEE(entries[:count*128]))
	binary.LittleEndian.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))

	data[0] = bootRecord(testPart{typ: 0xEE, first: 1, sectors: uint32(last)})
	data[testSector] = h
	data[2*testSector] = entries

	writeTestDisk(t, path, size, data)
}

// writeGPTDisk writes a sparse raw disk of 3 GiB at path with a GPT of two ext4 partitions: 1 of 512 MiB, such as
// /boot, and 2 of 2 GiB.
func writeGPTDisk(t *testing.T, path string) {
	t.Helper()

	writeGPT(t, path, 3<<30, [][2]uint64{{2048, 1 << 20}, {2048 + 1<<20, 4 << 20}}, map[int64][]byte{
		2048 * testSector:           ext4Start(),
		(2048 + 1<<20) * testSector: ext4Start(),
	})
}

// writeManyPartitions writes a raw disk of 8 MiB at path with a GPT of 70 ext4 partitions of 64 KiB, more than the
// file systems that phenix reads.
func writeManyPartitions(t *testing.T, path string) {
	t.Helper()

	parts, data := make([][2]uint64, 0, 70), make(map[int64][]byte)

	for i := range uint64(70) {
		first := 2048 + 128*i
		parts = append(parts, [2]uint64{first, 128})
		data[int64(first)*testSector] = ext4Start()
	}

	writeGPT(t, path, 8<<20, parts, data)
}

// injectVM is a VM of an experiment that injects files into partition of image.
func injectVM(name, image string, partition int) doctorVM {
	return doctorVM{name: name, snapshot: true, images: []string{image}, injectPartition: partition, injectImage: image}
}

// injectInput is a doctor input for the stored experiments, whose images are in files, with qemu-img at qemuImg or
// none for "". It counts the images inspectDisk reads.
func injectInput(t *testing.T, experiments []doctorExperiment, qemuImg string) (*doctorInput, string, map[string]int) {
	t.Helper()

	dir := newDoctorTestDir(t)
	files := filepath.Join(dir, "files")
	in := fakeDoctorInput(dir, nil)
	in.found = &doctorFound{
		minimegaUp: true, minimegaArgs: map[string]string{"filepath": files},
		experiments: &doctorExperiments{list: experiments, why: ""},
	}

	in.env.lookPath = lookPathIn(nil)
	if qemuImg != "" {
		in.env.lookPath = lookPathIn(map[string]string{"qemu-img": qemuImg})
	}

	reads := make(map[string]int)
	in.inspectDisk = func(ctx context.Context, path, qemuImg string) disk.Inspection {
		reads[path]++

		return disk.InspectPath(ctx, path, qemuImg)
	}

	return in, files, reads
}

// inject-partitions reads the partition table of each image that gets injects, and warns when the inject partition
// of a VM is missing, extended, swap, LVM, holds no file system, or is a small boot partition, or when the disk has no
// partition 1, with the partitions and a suggestion. It reads MBR logical partitions with the numbers Linux gives
// them, and GPT.
func TestInjectPartitionsCheck(t *testing.T) {
	t.Parallel()

	problem := func(files, image string, partition int, why, parts string) string {
		return "minimega cannot inject files into 1 inject partition of 1 at a start: e1/a (image " +
			filepath.Join(files, image) + ", inject_partition " + string(rune('0'+partition)) + "): " + why + "; " + parts
	}

	tests := map[string]struct {
		image     string
		partition int
		want      func(files string) doctorCheck
	}{
		"good ext4":    {image: "mbr.img", partition: 1, want: func(string) doctorCheck { return row("", doctorPass, okInjects) }},
		"logical ext4": {image: "mbr.img", partition: 5, want: func(string) doctorCheck { return row("", doctorPass, okInjects) }},
		"gpt data":     {image: "gpt.img", partition: 2, want: func(string) doctorCheck { return row("", doctorPass, okInjects) }},
		"missing partition": {
			image: "mbr.img", partition: 7,
			want: func(files string) doctorCheck {
				return row("", doctorWarn, problem(files, "mbr.img", 7,
					"inject_partition 7 names no partition (desired partition 7 not found)", mbrParts))
			},
		},
		"swap": {
			image: "mbr.img", partition: 2,
			want: func(files string) doctorCheck {
				return row("", doctorWarn, problem(files, "mbr.img", 2,
					"partition 2 holds Linux swap, not a file system that minimega can write files to", mbrParts))
			},
		},
		"extended": {
			image: "mbr.img", partition: 3,
			want: func(files string) doctorCheck {
				return row("", doctorWarn, problem(files, "mbr.img", 3, "partition 3 is an extended partition, which "+
					"holds no file system, so it is not a file system that minimega can write files to", mbrParts))
			},
		},
		"lvm": {
			image: "mbr.img", partition: 4,
			want: func(files string) doctorCheck {
				return row("", doctorWarn, problem(files, "mbr.img", 4,
					"partition 4 holds an LVM physical volume, not a file system that minimega can write files to", mbrParts))
			},
		},
		"no file system": {
			image: "mbr.img", partition: 6,
			want: func(files string) doctorCheck {
				return row("", doctorWarn, problem(files, "mbr.img", 6, "partition 6 holds no file system that doctor "+
					"knows, not a file system that minimega can write files to", mbrParts))
			},
		},
		"gpt boot partition": {
			image: "gpt.img", partition: 1,
			want: func(files string) doctorCheck {
				return row("", doctorWarn, problem(files, "gpt.img", 1, "partition 1 is a small boot partition "+
					"(512.0 MiB), so the files go to the wrong file system or the copy fails", gptParts))
			},
		},
		"no partition table": {
			image: "whole.img", partition: 1,
			want: func(files string) doctorCheck {
				return row("", doctorWarn, problem(files, "whole.img", 1, "the disk has no partition table, so minimega "+
					"finds no partition (no partitions found on image)",
					"the disk has no partitions; set inject_partition: 0 to turn off injects"))
			},
		},
		"no partition 1": {
			image: "nofirst.img", partition: 2,
			want: func(files string) doctorCheck {
				return row("", doctorWarn, problem(files, "nofirst.img", 2, "the disk has no partition 1, and minimega "+
					"waits for partition 1 before it opens any partition, so it finds no partition (no partitions found "+
					"on image)", "partitions 2 (Linux, ext4, 2.0 MiB), 3 (Linux, ext4, 2.0 MiB); minimega injects into "+
					"no partition of this disk, so set inject_partition: 0 to turn off injects"))
			},
		},
		"file system not read": {
			image: "many.img", partition: 70,
			want: func(files string) doctorCheck {
				return row("", doctorSkip, "not checked, because doctor checked no inject partition: "+
					filepath.Join(files, "many.img")+" partition 70 (doctor read only part of the disk, and not its file system)")
			},
		},
		"many partitions": {
			image: "many.img", partition: 64, want: func(string) doctorCheck { return row("", doctorPass, okInjects) },
		},
		"missing image": {
			image: "gone.img", partition: 1,
			want: func(files string) doctorCheck {
				return row("", doctorSkip, "not checked, because doctor checked no inject partition: "+
					filepath.Join(files, "gone.img")+" (it does not exist)")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, files, _ := injectInput(t, []doctorExperiment{{name: "e1", vms: []doctorVM{injectVM("a", tc.image, tc.partition)}}}, "")
			writeMBRDisk(t, filepath.Join(files, "mbr.img"))
			writeGPTDisk(t, filepath.Join(files, "gpt.img"))
			writeTestDisk(t, filepath.Join(files, "whole.img"), 4<<20, map[int64][]byte{0: ext4Start()})
			writeManyPartitions(t, filepath.Join(files, "many.img"))
			writeTestDisk(t, filepath.Join(files, "nofirst.img"), 8<<20, map[int64][]byte{
				0: bootRecord(testPart{}, testPart{typ: 0x83, first: 2048, sectors: 4096},
					testPart{typ: 0x83, first: 6144, sectors: 4096}),
				2048 * testSector: ext4Start(),
				6144 * testSector: ext4Start(),
			})

			got := injectPartitionsCheck(t.Context(), in)
			if diff := cmp.Diff(tc.want(files), got); diff != "" {
				t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// inject-partitions reads an image once, however many VMs and partitions use it, and lists every VM that injects
// into a partition.
func TestInjectPartitionsReadsEachImageOnce(t *testing.T) {
	t.Parallel()

	in, files, reads := injectInput(t, []doctorExperiment{
		{name: "e1", vms: []doctorVM{injectVM("a", "mbr.img", 2), injectVM("b", "mbr.img", 1)}},
		{name: "e2", vms: []doctorVM{injectVM("c", "mbr.img", 2), injectVM("d", "/abs/none.img", 0)}},
	}, "")
	writeMBRDisk(t, filepath.Join(files, "mbr.img"))

	got := injectPartitionsCheck(t.Context(), in)
	want := row("", doctorWarn, "minimega cannot inject files into 1 inject partition of 2 at a start: e1/a, e2/c (image "+
		filepath.Join(files, "mbr.img")+", inject_partition 2): partition 2 holds Linux swap, not a file system that "+
		"minimega can write files to; "+mbrParts)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]int{filepath.Join(files, "mbr.img"): 1}, reads); diff != "" {
		t.Errorf("injectPartitionsCheck() reads mismatch (-want +got):\n%s", diff)
	}
}

// fakeQemuDD is a `qemu-img dd` that reads the disk of the image named in its raw node from the raw file
// <image>.disk next to it, in the 64 KiB blocks that phenix reads.
const fakeQemuDD = `#!/bin/sh
for arg; do
	case $arg in
	if=*) opts=${arg#if=} ;;
	of=*) out=${arg#of=} ;;
	esac
done
offset=$(echo "$opts" | sed 's/.*offset=\([0-9]*\).*/\1/')
size=$(echo "$opts" | sed 's/.*,size=\([0-9]*\).*/\1/')
file=$(echo "$opts" | sed 's/.*file.file.filename=//; s/,,/,/g')
exec dd if="$file.disk" of="$out" bs=65536 skip=$((offset / 65536)) count=$((size / 65536)) 2>/dev/null
`

// inject-partitions reads a qcow2 image through its backing chain with qemu-img, and skips a qcow2 image when
// qemu-img is not installed. A raw image needs no qemu-img.
func TestInjectPartitionsQcow2(t *testing.T) {
	t.Parallel()

	qemuImg := filepath.Join(t.TempDir(), "qemu-img")
	dct.WriteFile(t, qemuImg, []byte(fakeQemuDD))

	if err := os.Chmod(qemuImg, 0o700); err != nil {
		t.Fatalf("os.Chmod() error = %v", err)
	}

	experiments := []doctorExperiment{{name: "e1", vms: []doctorVM{injectVM("a", "top.qc2", 2)}}}

	t.Run("chain", func(t *testing.T) {
		t.Parallel()

		in, files, _ := injectInput(t, experiments, qemuImg)
		// the fake qemu-img starts a shell for each block, which can take more than 1s on a busy host
		in.timeout = 30 * time.Second
		top := filepath.Join(files, "top.qc2")
		dct.Chain(t, top, filepath.Join(files, "base.qc2"), filepath.Join(files, "base.raw"))
		writeMBRDisk(t, top+".disk")

		if err := os.Truncate(top+".disk", 1<<30); err != nil {
			t.Fatalf("os.Truncate() error = %v", err)
		}

		got := injectPartitionsCheck(t.Context(), in)
		want := row("", doctorWarn, "minimega cannot inject files into 1 inject partition of 1 at a start: e1/a (image "+
			top+", inject_partition 2): partition 2 holds Linux swap, not a file system that minimega can write files "+
			"to; "+mbrParts)

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no qemu-img", func(t *testing.T) {
		t.Parallel()

		in, files, _ := injectInput(t, append(experiments,
			doctorExperiment{name: "e2", vms: []doctorVM{injectVM("b", "raw.img", 1)}}), "")
		top := filepath.Join(files, "top.qc2")
		dct.WriteQcow2(t, top, dct.Qcow2{})
		writeMBRDisk(t, filepath.Join(files, "raw.img"))

		got := injectPartitionsCheck(t.Context(), in)
		want := row("", doctorPass, okInjects+". Not checked: "+top+" (qemu-img is not installed)")

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
		}

		if hint := injectPartitionsHint(got); !strings.Contains(hint, "install qemu-img") {
			t.Errorf("injectPartitionsHint() = %q, want the qemu-img install hint", hint)
		}

		in.found.experiments = &doctorExperiments{list: experiments, why: ""}

		got = injectPartitionsCheck(t.Context(), in)
		want = row("", doctorSkip, "not checked, because doctor checked no inject partition: "+top+
			" (qemu-img is not installed)")

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("injectPartitionsCheck() without a raw image mismatch (-want +got):\n%s", diff)
		}
	})
}

// A VM gets injects when the stored experiment has injects, deletions or startup scripts for it, snapshot is on,
// and its first drive has an inject partition other than 0. inject-partitions checks no other VM.
func TestInjectPartitionsStoredVMs(t *testing.T) {
	t.Parallel()

	no, zero, swap := false, 0, 2

	in, dir, _ := storedInput(t, nil, nil)
	files := filepath.Join(dir, "files")
	in.found.minimegaArgs = map[string]string{"filepath": files}
	writeMBRDisk(t, filepath.Join(files, "good.qc2"))
	writeMBRDisk(t, filepath.Join(files, "bad.qc2"))

	// the startup app injects its scripts into partition 1 of good.qc2; the others name swap, or 0
	startup := snapshotNode("good")
	startup.HardwareF.OSTypeF = "linux"
	noSnapshot, noPartition, noInjects := snapshotNode("ns", "/x.txt"), snapshotNode("np", "/x.txt"), snapshotNode("ni")
	noSnapshot.GeneralF.SnapshotF = &no

	for _, n := range []*v1.Node{noSnapshot, noPartition, noInjects} {
		n.HardwareF.DrivesF[0].ImageF, n.HardwareF.DrivesF[0].InjectPartitionF = "bad.qc2", &swap
	}

	noPartition.HardwareF.DrivesF[0].InjectPartitionF = &zero

	exp := experimentConfig(t, "e1", filepath.Join(dir, "experiments", "e1"), []*v1.Node{
		startup, noSnapshot, noPartition, noInjects,
	}, nil)
	in.storeConfigs = func(_ context.Context, _, kind string, _ time.Duration) (store.Configs, error) {
		return map[string]store.Configs{"Experiment": {exp}}[kind], nil
	}

	if diff := cmp.Diff(row("", doctorPass, okInjects), injectPartitionsCheck(t.Context(), in)); diff != "" {
		t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
	}
}

// inject-partitions skips without the store, and passes when no VM gets injects.
func TestInjectPartitionsNothingToCheck(t *testing.T) {
	t.Parallel()

	in, _, _ := injectInput(t, nil, "")
	if diff := cmp.Diff(
		row("", doctorPass, "no VM of a stored experiment gets injects"),
		injectPartitionsCheck(t.Context(), in),
	); diff != "" {
		t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
	}

	in.found.experiments = &doctorExperiments{list: nil, why: "this user cannot read /phenix/store.bdb"}

	want := row("", doctorSkip, "not checked, because this user cannot read /phenix/store.bdb")
	if diff := cmp.Diff(want, injectPartitionsCheck(t.Context(), in)); diff != "" {
		t.Errorf("injectPartitionsCheck() without the store mismatch (-want +got):\n%s", diff)
	}
}

// The hint of a row that warns tells how to set inject_partition, and a skip row has none.
func TestInjectPartitionsHint(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		check doctorCheck
		want  string
	}{
		"wrong partition": {
			check: warnCheck("x: inject_partition 7 names no partition (desired partition 7 not found)"),
			want: "set inject_partition of the first drive of the VM in the topology " +
				"(hardware.drives[0].inject_partition) to the partition the row suggests, or to 0 to turn off injects " +
				"(see Troubleshooting)",
		},
		"no table": {
			check: warnCheck("x: the disk has no partition table, so minimega finds no partition"),
			want:  "set inject_partition: 0 for the VM, because minimega injects files only into a partition",
		},
		"no partition 1": {
			check: warnCheck("x: the disk has no partition 1, and minimega waits for partition 1"),
			want:  "set inject_partition: 0 for the VM, or use an image that has a partition 1",
		},
		"skip": {check: hostSkip("doctor read no disk image that receives injects: x (qemu-img is not installed)")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := injectPartitionsHint(tc.check); got != tc.want {
				t.Errorf("injectPartitionsHint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// With a real qemu-img, inject-partitions reads the partitions of a qcow2 overlay through its backing chain. CI
// installs qemu-img.
func TestInjectPartitionsRealQemu(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")

	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		t.Fatalf("exec.LookPath() error = %v", err)
	}

	in, files, _ := injectInput(t, []doctorExperiment{{name: "e1", vms: []doctorVM{
		injectVM("a", "top.qc2", 1), injectVM("b", "top.qc2", 2),
	}}}, qemuImg)

	raw, base, top := filepath.Join(files, "disk.raw"), filepath.Join(files, "base.qc2"), filepath.Join(files, "top.qc2")
	writeMBRDisk(t, raw)

	for _, args := range [][]string{
		{"convert", "-f", "raw", "-O", "qcow2", raw, base},
		{"create", "-f", "qcow2", "-b", base, "-F", "qcow2", top},
	} {
		if out, err := exec.CommandContext(t.Context(), qemuImg, args...).CombinedOutput(); err != nil {
			t.Fatalf("qemu-img %v: %v\n%s", args, err, out)
		}
	}

	got := injectPartitionsCheck(t.Context(), in)
	want := row("", doctorWarn, "minimega cannot inject files into 1 inject partition of 2 at a start: e1/b (image "+
		top+", inject_partition 2): partition 2 holds Linux swap, not a file system that minimega can write files to; "+
		mbrParts)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
	}
}

// doctorPartitionsPart is a partition of number, size bytes and file system fs:
// "" for none read, "extended" for an extended partition.
func doctorPartitionsPart(number int, typ, fs string, size int64) disk.Partition {
	p := disk.Partition{Number: number, Type: typ, Size: size}

	switch fs {
	case "":
	case "extended":
		p.Extended = true
	default:
		p.Filesystem = &disk.Filesystem{Type: fs}
	}

	return p
}

// injectProblem names why minimega cannot inject files into a partition from
// the partition table alone, and gives a note when doctor cannot tell.
func TestDoctorPartitionsInjectProblem(t *testing.T) {
	t.Parallel()

	const mib, gib = 1 << 20, 1 << 30

	boot := []disk.Partition{
		doctorPartitionsPart(1, "EFI System", "vfat", 512*mib), doctorPartitionsPart(2, "Linux", "ext4", 2*gib),
	}
	small := []disk.Partition{
		doctorPartitionsPart(1, "EFI System", "vfat", 100*mib), doctorPartitionsPart(2, "Linux", "ext4", 500*mib),
	}

	tests := map[string]struct {
		found                 disk.Inspection
		number                int
		wantProblem, wantNote string
	}{
		"ext4": {
			found:  disk.Inspection{PartitionTable: "mbr", Partitions: boot},
			number: 2,
		},
		"table not read": {
			found:  disk.Inspection{PartitionTable: "", Partitions: boot},
			number: 2,
		},
		"no table": {
			found:       disk.Inspection{PartitionTable: "none"},
			number:      1,
			wantProblem: "the disk has no partition table, so minimega finds no partition (no partitions found on image)",
		},
		"unknown table": {
			found:    disk.Inspection{PartitionTable: "unknown", PartitionNotes: []string{"bad CRC", "overlap"}},
			number:   1,
			wantNote: "doctor cannot read its partition table: bad CRC, overlap",
		},
		"no partition 1": {
			found:  disk.Inspection{PartitionTable: "gpt", Partitions: boot[1:]},
			number: 2,
			wantProblem: "the disk has no partition 1, and minimega waits for partition 1 before it opens any " +
				"partition, so it finds no partition (no partitions found on image)",
		},
		"missing partition": {
			found:       disk.Inspection{PartitionTable: "gpt", Partitions: boot},
			number:      3,
			wantProblem: "inject_partition 3 names no partition (desired partition 3 not found)",
		},
		"extended": {
			found: disk.Inspection{PartitionTable: "mbr", Partitions: append(slices.Clone(boot),
				doctorPartitionsPart(3, "Extended", "extended", mib))},
			number: 3,
			wantProblem: "partition 3 is an extended partition, which holds no file system, so it is not a file " +
				"system that minimega can write files to",
		},
		"file system not read": {
			found: disk.Inspection{PartitionTable: "mbr", Partitions: append(slices.Clone(boot),
				doctorPartitionsPart(3, "Linux", "", mib))},
			number:   3,
			wantNote: "doctor read only part of the disk, and not its file system",
		},
		"LUKS": {
			found: disk.Inspection{PartitionTable: "gpt", Partitions: append(slices.Clone(boot),
				doctorPartitionsPart(3, "Linux", "crypto_LUKS", gib))},
			number:      3,
			wantProblem: "partition 3 holds a LUKS volume, not a file system that minimega can write files to",
		},
		"boot partition": {
			found:  disk.Inspection{PartitionTable: "gpt", Partitions: boot},
			number: 1,
			wantProblem: "partition 1 is a small boot partition (512.0 MiB), so the files go to the wrong file " +
				"system or the copy fails",
		},
		// no partition is large enough to tell a boot partition from a data partition
		"small disk": {
			found:  disk.Inspection{PartitionTable: "gpt", Partitions: small},
			number: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			problem, note := injectProblem(tc.found, tc.number)
			if problem != tc.wantProblem || note != tc.wantNote {
				t.Errorf("injectProblem(%d) = %q, %q; want %q, %q", tc.number, problem, note, tc.wantProblem, tc.wantNote)
			}
		})
	}
}

// The summary lists each partition with its type, file system and size, at
// most eight of them, and suggests the largest Linux or NTFS partition.
func TestDoctorPartitionsSummary(t *testing.T) {
	t.Parallel()

	const mib = 1 << 20

	many := make([]disk.Partition, 0, 10)
	for i := 1; i <= 10; i++ {
		many = append(many, doctorPartitionsPart(i, "Linux", "ext4", mib))
	}

	tests := map[string]struct {
		partitions []disk.Partition
		want       string
	}{
		"none": {want: "the disk has no partitions; set inject_partition: 0 to turn off injects"},
		"kinds": {
			partitions: []disk.Partition{
				doctorPartitionsPart(1, "", "vfat", mib), doctorPartitionsPart(2, "Extended", "extended", 2*mib),
				doctorPartitionsPart(5, "Linux", "", mib), doctorPartitionsPart(6, "Linux", "xfs", 3*mib),
			},
			want: "partitions 1 (unknown type, vfat, 1.0 MiB), 2 (Extended, 2.0 MiB), 5 (Linux, file system not read, " +
				"1.0 MiB), 6 (Linux, xfs, 3.0 MiB); try inject_partition: 6",
		},
		"nothing to suggest": {
			partitions: []disk.Partition{doctorPartitionsPart(1, "Linux swap", "swap", mib)},
			want: "partitions 1 (Linux swap, swap, 1.0 MiB); no Linux or NTFS partition to suggest, so set " +
				"inject_partition: 0 to turn off injects",
		},
		"no partition 1": {
			partitions: []disk.Partition{doctorPartitionsPart(2, "Linux", "ext4", mib)},
			want: "partitions 2 (Linux, ext4, 1.0 MiB); minimega injects into no partition of this disk, so set " +
				"inject_partition: 0 to turn off injects",
		},
		"more than eight": {
			partitions: many,
			want: "partitions 1 (Linux, ext4, 1.0 MiB), 2 (Linux, ext4, 1.0 MiB), 3 (Linux, ext4, 1.0 MiB), 4 (Linux, " +
				"ext4, 1.0 MiB), 5 (Linux, ext4, 1.0 MiB), 6 (Linux, ext4, 1.0 MiB), 7 (Linux, ext4, 1.0 MiB), 8 (Linux, " +
				"ext4, 1.0 MiB), and 2 more; try inject_partition: 1",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, partitionSummary(disk.Inspection{Partitions: tc.partitions})); diff != "" {
				t.Errorf("partitionSummary() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// suggestedPartition is the largest Linux or NTFS data partition, the first of
// equal ones, or none.
func TestDoctorPartitionsSuggested(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		partitions []disk.Partition
		want       int // the number; 0 for none
	}{
		"none":    {},
		"no data": {partitions: []disk.Partition{doctorPartitionsPart(1, "", "vfat", 9), doctorPartitionsPart(2, "", "swap", 9)}},
		"largest": {
			partitions: []disk.Partition{
				doctorPartitionsPart(1, "", "ext4", 5), doctorPartitionsPart(2, "", "ntfs", 7),
				doctorPartitionsPart(3, "", "xfs", 6), doctorPartitionsPart(4, "", "vfat", 100),
			},
			want: 2,
		},
		"first of equal": {
			partitions: []disk.Partition{doctorPartitionsPart(1, "", "btrfs", 5), doctorPartitionsPart(2, "", "ext3", 5)},
			want:       1,
		},
		"not extended or unread": {
			partitions: []disk.Partition{
				doctorPartitionsPart(1, "", "extended", 50), doctorPartitionsPart(2, "", "", 40),
				doctorPartitionsPart(3, "", "ext2", 1),
			},
			want: 3,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := 0
			if p := suggestedPartition(tc.partitions); p != nil {
				got = p.Number
			}

			if got != tc.want {
				t.Errorf("suggestedPartition() = partition %d, want %d", got, tc.want)
			}

			if has := hasPartition(tc.partitions, 1); has != (len(tc.partitions) > 0) {
				t.Errorf("hasPartition(1) = %t with %d partitions numbered from 1", has, len(tc.partitions))
			}
		})
	}
}

// capList keeps at most n items and counts the rest, without changing items.
func TestDoctorPartitionsCapList(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		items []string
		n     int
		want  []string
	}{
		"empty":     {n: 2},
		"fewer":     {items: []string{"a"}, n: 2, want: []string{"a"}},
		"exactly n": {items: []string{"a", "b"}, n: 2, want: []string{"a", "b"}},
		"one more":  {items: []string{"a", "b", "c"}, n: 2, want: []string{"a", "b", "and 1 more"}},
		"many more": {items: []string{"a", "b", "c", "d", "e"}, n: 1, want: []string{"a", "and 4 more"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			items := slices.Clone(tc.items)

			if diff := cmp.Diff(tc.want, capList(items, tc.n)); diff != "" {
				t.Errorf("capList(%q, %d) mismatch (-want +got):\n%s", tc.items, tc.n, diff)
			}

			if diff := cmp.Diff(tc.items, items); diff != "" {
				t.Errorf("capList(%q, %d) changed its items (-want +got):\n%s", tc.items, tc.n, diff)
			}
		})
	}
}

// The row warns with at most five problems, skips when doctor checked
// nothing, and passes with the images it did not check.
func TestDoctorPartitionsRow(t *testing.T) {
	t.Parallel()

	letters := func(n int) []string {
		items := make([]string, n)
		for i := range items {
			items[i] = string(rune('a' + i))
		}

		return items
	}

	tests := map[string]struct {
		problems, notChecked []string
		checked, targets     int
		want                 doctorCheck
	}{
		"pass": {checked: 2, targets: 2, want: passCheck(
			"each of the 2 inject partitions holds a file system that minimega can write files to")},
		"pass with notes": {notChecked: []string{"x (it does not exist)"}, checked: 1, targets: 2, want: passCheck(
			okInjects + ". Not checked: x (it does not exist)")},
		"skip": {notChecked: letters(7), targets: 7, want: hostSkip(
			"doctor checked no inject partition: a; b; c; d; e; and 2 more")},
		"problems": {problems: letters(6), notChecked: []string{"y"}, checked: 6, targets: 7, want: warnCheck(
			"minimega cannot inject files into 6 inject partitions of 7 at a start: a. b. c. d. e. and 1 more. " +
				"Not checked: y")},
		"one problem": {problems: []string{"a"}, checked: 1, targets: 1, want: warnCheck(
			"minimega cannot inject files into 1 inject partition of 1 at a start: a")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := injectPartitionsRow(tc.problems, tc.notChecked, tc.checked, tc.targets)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("injectPartitionsRow() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// readInjectImage says why it did not read an image: it does not exist,
// doctor cannot stat it, the time ran out, a qcow2 image without qemu-img, or
// what keeps phenix from reading its partitions.
func TestDoctorPartitionsReadInjectImage(t *testing.T) {
	t.Parallel()

	read := disk.Inspection{PartitionTable: "mbr", Partitions: []disk.Partition{doctorPartitionsPart(1, "Linux", "ext4", 1)}}

	tests := map[string]struct {
		statErr  error
		canceled string // "before" or "during" the read; "" for neither
		qemuImg  string
		found    disk.Inspection
		wantWhy  string
		wantRead bool // inspectDisk ran
	}{
		"read":    {found: read, wantRead: true},
		"missing": {statErr: &fs.PathError{Op: "stat", Path: "/i.img", Err: fs.ErrNotExist}, wantWhy: "it does not exist"},
		"no access": {
			statErr: &fs.PathError{Op: "stat", Path: "/i.img", Err: fs.ErrPermission},
			wantWhy: "doctor cannot read it: permission denied",
		},
		"time ran out before": {canceled: "before", wantWhy: "doctor stopped reading images after 1s"},
		"time ran out during": {canceled: "during", found: read, wantWhy: "doctor stopped reading images after 1s", wantRead: true},
		"qcow2 without qemu-img": {
			found: disk.Inspection{Qcow: &disk.QcowHeader{}}, wantWhy: noQemuImgPhrase, wantRead: true,
		},
		"qcow2 with qemu-img": {
			qemuImg: "/usr/bin/qemu-img", found: disk.Inspection{Qcow: &disk.QcowHeader{}, PartitionTable: "mbr"}, wantRead: true,
		},
		"partitions unavailable": {
			found:   disk.Inspection{PartitionsUnavailable: "phenix cannot read the partitions: qemu-img failed"},
			wantWhy: "qemu-img failed", wantRead: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(newDoctorTestDir(t), nil)
			in.env.stat = func(string) (os.FileInfo, error) { return nil, tc.statErr }

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if tc.canceled == "before" {
				cancel()
			}

			var gotQemu string

			ran := false
			in.inspectDisk = func(_ context.Context, path, qemuImg string) disk.Inspection {
				ran, gotQemu = true, qemuImg

				if path != "/i.img" {
					t.Errorf("inspectDisk(%q), want /i.img", path)
				}

				if tc.canceled == "during" {
					cancel()
				}

				return tc.found
			}

			got := in.readInjectImage(ctx, "/i.img", tc.qemuImg)

			if got.why != tc.wantWhy || ran != tc.wantRead || gotQemu != tc.qemuImg {
				t.Errorf("readInjectImage() why %q, read %t with qemu-img %q; want %q, %t, %q",
					got.why, ran, gotQemu, tc.wantWhy, tc.wantRead, tc.qemuImg)
			}

			if tc.wantRead && tc.wantWhy == "" {
				if diff := cmp.Diff(tc.found, got.found); diff != "" {
					t.Errorf("readInjectImage() found mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
