package cmd

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/api/disk"
	dct "phenix/util/diskchain/diskchaintest"
	"phenix/util/partition"
	pt "phenix/util/partition/partitiontest"
)

// writeVolumeDisk writes a raw disk of 4 MiB at path with an MBR partition 1 of 1 MiB at sector 2048, of MBR type
// typ, that starts with volume.
func writeVolumeDisk(t *testing.T, path string, typ byte, volume []byte) {
	t.Helper()

	writeTestDisk(t, path, 4<<20, map[int64][]byte{
		0:                 bootRecord(testPart{typ: typ, first: 2048, sectors: 2048}),
		2048 * testSector: volume,
	})
}

// inject-partitions warns when the NTFS volume of an inject partition is marked dirty or its log is not clean, or
// when an ext file system has errors, and names the VM, the image, the partition and the reason. It notes a volume
// it cannot read, and still counts the partition as checked.
func TestInjectPartitionsVolumeState(t *testing.T) {
	t.Parallel()

	const (
		ntfsResult = "so the mount at a start fails (wrong fs type, bad superblock) or gives a read-only file system"
		extResult  = "so Linux can make it read-only while minimega writes the files"
	)

	warn := func(image, why string) string {
		return "minimega cannot inject files into 1 inject partition of 1 at a start: e1/a (image " + image +
			", inject_partition 1): partition 1 holds " + why
	}
	notClean := pt.Restart{LSN: 3, InUse: true}
	badLog := pt.NTFS{}.Bytes()
	copy(badLog[pt.NTFSLog:], "XXXX")
	copy(badLog[pt.NTFSLog+pt.NTFSPageSize:], "XXXX")

	tests := map[string]struct {
		typ    byte
		volume []byte
		want   func(image string) doctorCheck
	}{
		"clean NTFS": {
			typ: 0x07, volume: pt.NTFS{}.Bytes(),
			want: func(string) doctorCheck { return row("", doctorPass, okInjects) },
		},
		"dirty NTFS": {
			typ: 0x07, volume: pt.NTFS{Dirty: true}.Bytes(),
			want: func(image string) doctorCheck {
				return row("", doctorWarn, warn(image, "NTFS, but the NTFS volume is marked dirty, "+ntfsResult))
			},
		},
		"NTFS log not clean": {
			typ: 0x07, volume: pt.NTFS{First: notClean}.Bytes(),
			want: func(image string) doctorCheck {
				return row("", doctorWarn, warn(image, "NTFS, but Windows did not shut down fully: the NTFS log is not "+
					"clean (hibernation or Fast Startup), "+ntfsResult))
			},
		},
		"NTFS log not read": {
			typ: 0x07, volume: badLog,
			want: func(image string) doctorCheck {
				return row("", doctorPass, okInjects+". Not checked: "+image+" partition 1 (doctor cannot read the "+
					"state of its NTFS file system: $LogFile: not a valid NTFS volume: no valid restart page)")
			},
		},
		"clean ext4": {
			typ: 0x83, volume: pt.Ext(1),
			want: func(string) doctorCheck { return row("", doctorPass, okInjects) },
		},
		"ext4 with errors": {
			typ: 0x83, volume: pt.Ext(3),
			want: func(image string) doctorCheck {
				return row("", doctorWarn, warn(image, "ext4, but the file system is marked with errors that e2fsck "+
					"did not repair, "+extResult))
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, files, _ := injectInput(t, []doctorExperiment{{name: "e1", vms: []doctorVM{injectVM("a", "w.img", 1)}}}, "")
			image := filepath.Join(files, "w.img")
			writeVolumeDisk(t, image, tc.typ, tc.volume)

			got := injectPartitionsCheck(t.Context(), in)
			if diff := cmp.Diff(tc.want(image), got); diff != "" {
				t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// inject-partitions reads the NTFS state of a qcow2 image through its backing chain with qemu-img, past the first
// 16 MiB of the disk.
func TestInjectPartitionsVolumeStateQcow2(t *testing.T) {
	t.Parallel()

	qemuImg := filepath.Join(t.TempDir(), "qemu-img")
	dct.WriteFile(t, qemuImg, []byte(fakeQemuDD))

	if err := os.Chmod(qemuImg, 0o700); err != nil {
		t.Fatalf("os.Chmod() error = %v", err)
	}

	in, files, _ := injectInput(t, []doctorExperiment{{name: "e1", vms: []doctorVM{injectVM("a", "top.qc2", 1)}}}, qemuImg)
	// the fake qemu-img starts a shell for each block, which can take more than 1s on a busy host
	in.timeout = 30 * time.Second
	top := filepath.Join(files, "top.qc2")
	dct.Chain(t, top, filepath.Join(files, "base.raw"))

	// move the MFT and the log 32 MiB into the volume, past the 16 MiB that disk.InspectPath reads
	const (
		start = 2048 * testSector
		far   = 32 << 20
	)

	v := pt.NTFS{First: pt.Restart{LSN: 3, InUse: true}}.Bytes()
	boot := slices.Clone(v[:testSector])
	binary.LittleEndian.PutUint64(boot[0x30:], (far+pt.NTFSMFT)/pt.NTFSCluster)

	// 16 clusters at cluster (far + NTFSLog) / NTFSCluster, 0x010010
	logRun := []byte{0x32, 16, 0, 0x10, 0x00, 0x01}

	writeTestDisk(t, top+".disk", 1<<30, map[int64][]byte{
		0:                            bootRecord(testPart{typ: 0x07, first: 2048, sectors: 1 << 20}),
		start:                        boot,
		start + far + pt.NTFSMFT:     v[pt.NTFSMFT:pt.NTFSLogFile],
		start + far + pt.NTFSLogFile: pt.FileRecord(pt.NonResident(0x80, logRun)),
		start + far + pt.NTFSVolume:  v[pt.NTFSVolume : pt.NTFSVolume+pt.NTFSRecordSize],
		start + far + pt.NTFSLog:     v[pt.NTFSLog:],
	})

	got := injectPartitionsCheck(t.Context(), in)
	want := row("", doctorWarn, "minimega cannot inject files into 1 inject partition of 1 at a start: e1/a (image "+
		top+", inject_partition 1): partition 1 holds NTFS, but Windows did not shut down fully: the NTFS log is not "+
		"clean (hibernation or Fast Startup), so the mount at a start fails (wrong fs type, bad superblock) or gives a "+
		"read-only file system")

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("injectPartitionsCheck() mismatch (-want +got):\n%s", diff)
	}
}

// imageBlocks reads each block once, stops at volumeReadMax bytes, and gives [io.EOF] at the end of the disk.
func TestImageBlocks(t *testing.T) {
	t.Parallel()

	const size = 20*volumeReadBlock + 100

	var reads []int64

	b := &imageBlocks{
		size: size, blocks: map[int64][]byte{}, left: volumeReadMax, dir: "",
		read: func(off int64, n int) ([]byte, error) {
			reads = append(reads, off)

			return []byte(strings.Repeat("x", n)), nil
		},
	}

	p := make([]byte, 10)
	for _, off := range []int64{0, 5, volumeReadBlock - 5, 0} {
		if n, err := b.ReadAt(p, off); n != len(p) || err != nil {
			t.Errorf("ReadAt(%d) = %d, %v; want %d, nil", off, n, err, len(p))
		}
	}

	if diff := cmp.Diff([]int64{0, volumeReadBlock}, reads); diff != "" {
		t.Errorf("ReadAt() block reads mismatch (-want +got):\n%s", diff)
	}

	if n, err := b.ReadAt(p, size-4); n != 4 || !errors.Is(err, io.EOF) {
		t.Errorf("ReadAt(end - 4) = %d, %v; want 4, io.EOF", n, err)
	}

	if n, err := b.ReadAt(p, size); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("ReadAt(end) = %d, %v; want 0, io.EOF", n, err)
	}

	if _, err := b.ReadAt(p, -1); err == nil {
		t.Error("ReadAt(-1) error = nil, want an error")
	}

	var err error
	for i := int64(2); i < 20 && err == nil; i++ {
		_, err = b.ReadAt(p, i*volumeReadBlock)
	}

	if !errors.Is(err, errVolumeReadLimit) {
		t.Errorf("ReadAt() past %d bytes error = %v, want %v", volumeReadMax, err, errVolumeReadLimit)
	}

	if got := int64(len(b.blocks)) * volumeReadBlock; got > volumeReadMax {
		t.Errorf("imageBlocks read %d bytes, more than %d", got, volumeReadMax)
	}
}

// The hint for a dirty NTFS volume or a log that is not clean tells how to shut Windows down fully, and the hint for
// ext errors tells how to repair the file system.
func TestInjectPartitionsVolumeHint(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		reason string
		want   []string
	}{
		"dirty":     {reason: partition.ReasonNTFSDirty, want: []string{"chkdsk /f", "powercfg /h off", "shutdown /s /t 0"}},
		"log":       {reason: partition.ReasonNTFSLog, want: []string{"powercfg /h off", "shutdown /s /t 0", "rebuild"}},
		"extErrors": {reason: partition.ReasonExtHasErrors, want: []string{"e2fsck -f", "rebuild"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hint := injectPartitionsHint(warnCheck("x: partition 1 holds a file system, but " + tc.reason))
			for _, w := range tc.want {
				if !strings.Contains(hint, w) {
					t.Errorf("injectPartitionsHint() = %q, want it to name %q", hint, w)
				}
			}
		})
	}
}

// volumeProblem reads no file system state for a partition that holds no NTFS or ext file system.
func TestVolumeProblemOtherFilesystems(t *testing.T) {
	t.Parallel()

	in := fakeDoctorInput(t.TempDir(), nil)
	image := &injectImage{found: disk.Inspection{Partitions: []disk.Partition{
		doctorPartitionsPart(1, "Linux", partition.FSXFS, 1<<30), doctorPartitionsPart(2, "Linux", "", 1<<30),
	}}}

	for _, number := range []int{1, 2, 3} {
		if problem, note := in.volumeProblem(t.Context(), image, "/i.img", number, ""); problem != "" || note != "" {
			t.Errorf("volumeProblem(%d) = %q, %q; want none", number, problem, note)
		}
	}

	if image.blocks != nil {
		t.Error("volumeProblem() made a reader for no NTFS or ext file system")
	}
}
