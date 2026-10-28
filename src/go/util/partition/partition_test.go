package partition_test

import (
	"errors"
	"io"
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/partition"
)

const (
	efiType   = "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
	linuxType = "0FC63DAF-8483-4772-8E79-3D69D8477DE4"
	diskGUID  = "6E1F4A3B-1C2D-4E5F-8A9B-0C1D2E3F4A5B"
	partGUID  = "11111111-2222-4333-8444-555555555555"
	gib       = int64(1) << 30
	mib       = int64(1) << 20
)

// mbrPart is a partition Read returns for an MBR entry.
func mbrPart(number int, first, sectors int64, typeID, name string) partition.Partition {
	return partition.Partition{
		Number: number, Start: first * 512, Size: sectors * 512, Type: name, TypeID: typeID,
	}
}

func TestRead(t *testing.T) {
	t.Parallel()

	extended := func() *testDisk {
		// an extended partition at sector 4096 with logical partitions at 4096+2048 and 8192+2048
		return newDisk(gib).
			write(0, mbr(0x1a2b3c4d,
				mbrEntry{boot: 0x80, typ: 0x83, first: 2048, sectors: 2048},
				mbrEntry{},
				mbrEntry{typ: 0x05, first: 4096, sectors: 8192},
				mbrEntry{typ: 0x82, first: 12288, sectors: 1024},
			)).
			write(4096*512, mbr(0, mbrEntry{typ: 0x83, first: 2048, sectors: 1024}, mbrEntry{typ: 0x05, first: 4096, sectors: 4096})).
			write(8192*512, mbr(0, mbrEntry{typ: 0x8e, first: 2048, sectors: 1024}))
	}

	logical := func(p partition.Partition) partition.Partition { p.Logical = true; return p }
	ext := func(p partition.Partition) partition.Partition { p.Extended = true; return p }
	boot := func(p partition.Partition) partition.Partition { p.Bootable = true; return p }

	tests := map[string]struct {
		disk *testDisk
		want partition.Table
	}{
		"an empty disk has no table": {
			disk: newDisk(gib),
			want: partition.Table{Scheme: partition.SchemeNone},
		},
		"a disk smaller than a sector has no table": {
			disk: newDisk(100).write(0, []byte("kali")),
			want: partition.Table{Scheme: partition.SchemeNone},
		},
		"a FAT file system on the whole disk is not an MBR": {
			disk: newDisk(64*mib).write(0, fatBootSector(true, 131072, "KALI")),
			want: partition.Table{Scheme: partition.SchemeNone},
		},
		"an MBR with primary, extended and logical partitions numbers them as Linux does": {
			disk: extended(),
			want: partition.Table{
				Scheme: partition.SchemeMBR, SectorSize: 512, DiskID: "0x1a2b3c4d",
				Partitions: []partition.Partition{
					boot(mbrPart(1, 2048, 2048, "0x83", "Linux")),
					ext(mbrPart(3, 4096, 8192, "0x05", "Extended")),
					mbrPart(4, 12288, 1024, "0x82", "Linux swap / Solaris"),
					logical(mbrPart(5, 4096+2048, 1024, "0x83", "Linux")),
					logical(mbrPart(6, 8192+2048, 1024, "0x8e", "Linux LVM")),
				},
			},
		},
		"an MBR partition past the end of the disk is cut, and one that starts past it is left out": {
			disk: newDisk(4*mib).write(0, mbr(0,
				mbrEntry{typ: 0x83, first: 2048, sectors: 1 << 20},
				mbrEntry{typ: 0x83, first: 1 << 20, sectors: 2048},
			)),
			want: partition.Table{
				Scheme: partition.SchemeMBR, SectorSize: 512, DiskID: "0x00000000",
				Partitions: []partition.Partition{mbrPart(1, 2048, 8192-2048, "0x83", "Linux")},
				Notes: []string{
					"partition 1 ends past the end of the disk, so phenix shows it cut",
					"partition 2 starts past the end of the disk",
				},
			},
		},
		"an unknown MBR type has no name": {
			disk: newDisk(gib).write(0, mbr(0, mbrEntry{typ: 0x77, first: 2048, sectors: 2048})),
			want: partition.Table{
				Scheme: partition.SchemeMBR, SectorSize: 512, DiskID: "0x00000000",
				Partitions: []partition.Partition{mbrPart(1, 2048, 2048, "0x77", "")},
			},
		},
		"a boot sector with bad boot flags has an unknown table": {
			disk: newDisk(gib).write(0, mbr(0, mbrEntry{boot: 0x12, typ: 0x83, first: 2048, sectors: 2048})),
			want: partition.Table{
				Scheme: partition.SchemeUnknown,
				Notes:  []string{"the first sector has the boot mark but no valid MBR partition table"},
			},
		},
		"logical partitions that loop are read once": {
			disk: newDisk(gib).
				write(0, mbr(0, mbrEntry{typ: 0x0f, first: 2048, sectors: 8192})).
				write(2048*512, mbr(0, mbrEntry{typ: 0x83, first: 63, sectors: 100}, mbrEntry{typ: 0x05, first: 4096, sectors: 2048})).
				write(6144*512, mbr(0, mbrEntry{typ: 0x83, first: 63, sectors: 100}, mbrEntry{typ: 0x05, first: 0, sectors: 2048})),
			want: partition.Table{
				Scheme: partition.SchemeMBR, SectorSize: 512, DiskID: "0x00000000",
				Partitions: []partition.Partition{
					ext(mbrPart(1, 2048, 8192, "0x0f", "W95 Ext'd (LBA)")),
					logical(mbrPart(5, 2048+63, 100, "0x83", "Linux")),
					logical(mbrPart(6, 6144+63, 100, "0x83", "Linux")),
				},
				Notes: []string{"the logical partitions of partition 1 form a loop"},
			},
		},
		"a GPT gives each entry's type, name, GUID and number": {
			disk: gptDisk(gib, 512,
				gptEntry{typ: guidBytes(efiType), id: guidBytes(partGUID), first: 2048, last: 4095, name: "EFI system"},
				gptEntry{},
				gptEntry{typ: guidBytes(linuxType), first: 4096, last: 8191, attrs: 1 << 2, name: "kali root ✓"},
			),
			want: partition.Table{
				Scheme: partition.SchemeGPT, SectorSize: 512, DiskID: diskGUID,
				Partitions: []partition.Partition{
					{
						Number: 1, Start: 2048 * 512, Size: 2048 * 512, Type: "EFI System", TypeID: efiType,
						Name: "EFI system", GUID: partGUID,
					},
					{
						Number: 3, Start: 4096 * 512, Size: 4096 * 512, Type: "Linux filesystem", TypeID: linuxType,
						Bootable: true, Name: "kali root ✓", GUID: "00000000-0000-0000-0000-000000000000",
					},
				},
			},
		},
		"a GPT with 4096-byte sectors": {
			disk: gptDisk(gib, 4096, gptEntry{typ: guidBytes(linuxType), first: 256, last: 511}),
			want: partition.Table{
				Scheme: partition.SchemeGPT, SectorSize: 4096, DiskID: diskGUID,
				Partitions: []partition.Partition{{
					Number: 1, Start: 256 * 4096, Size: 256 * 4096, Type: "Linux filesystem", TypeID: linuxType,
					GUID: "00000000-0000-0000-0000-000000000000",
				}},
			},
		},
		"a damaged primary GPT is read from the backup": {
			disk: gptDisk(gib, 512, gptEntry{typ: guidBytes(linuxType), first: 2048, last: 4095}).
				write(512+60, []byte("damage")),
			want: partition.Table{
				Scheme: partition.SchemeGPT, SectorSize: 512, DiskID: diskGUID,
				Partitions: []partition.Partition{{
					Number: 1, Start: 2048 * 512, Size: 2048 * 512, Type: "Linux filesystem", TypeID: linuxType,
					GUID: "00000000-0000-0000-0000-000000000000",
				}},
				Notes: []string{"the primary GPT is damaged, so phenix read the backup GPT at the end of the disk"},
			},
		},
		"damaged entries in both GPTs give an unknown table": {
			disk: gptDisk(gib, 512, gptEntry{typ: guidBytes(linuxType), first: 2048, last: 4095}).
				write(2*512+40, []byte("damage")).
				write(gib-2*512+40, []byte("damage")),
			want: partition.Table{
				Scheme: partition.SchemeUnknown,
				Notes:  []string{"the disk has a GPT protective MBR but no valid GPT"},
			},
		},
		"a GPT protective MBR on a disk smaller than one 4096-byte sector has no GPT": {
			disk: newDisk(2048).write(0, mbr(0, mbrEntry{typ: 0xEE, first: 1, sectors: 3})),
			want: partition.Table{
				Scheme: partition.SchemeUnknown,
				Notes:  []string{"the disk has a GPT protective MBR but no valid GPT"},
			},
		},
		"a GPT entry that ends before it starts is left out": {
			disk: gptDisk(gib, 512, gptEntry{typ: guidBytes(linuxType), first: 4096, last: 2048}),
			want: partition.Table{
				Scheme: partition.SchemeGPT, SectorSize: 512, DiskID: diskGUID,
				Notes: []string{"partition 1 ends before it starts"},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := partition.Read(tt.disk, tt.disk.size)
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}

			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Read() mismatch (-want +got):\n%s", diff)
			}

			if len(tt.disk.outside) != 0 {
				t.Errorf("Read() read outside the disk %d times", len(tt.disk.outside))
			}
		})
	}
}

func TestProbe(t *testing.T) {
	t.Parallel()

	// zero start, size and disk mean a 128 MiB partition 3 MiB into a 1 GiB disk
	tests := map[string]struct {
		data              []byte
		start, size, disk int64
		want              partition.Filesystem
	}{
		"zeros":          {data: nil, want: partition.Filesystem{Type: "unknown"}},
		"ext2":           {data: extSuperblock(0, 0x2, 0x3, "boot"), want: partition.Filesystem{Type: "ext2", Label: "boot"}},
		"ext3":           {data: extSuperblock(0x4, 0x2, 0x3, ""), want: partition.Filesystem{Type: "ext3"}},
		"ext4":           {data: extSuperblock(0x4, 0x2|0x40, 0x3, "rootfs"), want: partition.Filesystem{Type: "ext4", Label: "rootfs"}},
		"ext4 by ro bit": {data: extSuperblock(0x4, 0x2, 0x400, ""), want: partition.Filesystem{Type: "ext4"}},
		"xfs":            {data: append(at(0, "XFSB"), at(0x6C-4, "kali")...), want: partition.Filesystem{Type: "xfs", Label: "kali"}},
		"btrfs": {
			data: append(at(0x10040, "_BHRfS_M"), at(0xE3, "data")...),
			want: partition.Filesystem{Type: "btrfs", Label: "data"},
		},
		"swap": {
			data: append(at(0x41C, "swap1"), at(4096-10-0x41C-5, "SWAPSPACE2")...),
			want: partition.Filesystem{Type: "swap", Label: "swap1"},
		},
		"swap 64k pages": {data: at(65536-10, "SWAPSPACE2"), want: partition.Filesystem{Type: "swap"}},
		"lvm":            {data: append(at(512, "LABELONE"), at(16, "LVM2 001")...), want: partition.Filesystem{Type: "LVM2_member"}},
		"luks1":          {data: append([]byte("LUKS\xba\xbe"), 0, 1), want: partition.Filesystem{Type: "crypto_LUKS", Version: "1"}},
		"luks2": {
			data: append(append([]byte("LUKS\xba\xbe"), 0, 2), at(16, "secret")...),
			want: partition.Filesystem{Type: "crypto_LUKS", Version: "2", Label: "secret"},
		},
		"iso9660": {
			data: append(at(0x8001, "CD001"), at(0x8028-0x8006, "Kali Live              ")...),
			want: partition.Filesystem{Type: "iso9660", Label: "Kali Live"},
		},
		"squashfs": {data: []byte("hsqs"), want: partition.Filesystem{Type: "squashfs"}},
		"ntfs":     {data: at(3, "NTFS    "), want: partition.Filesystem{Type: "ntfs"}},
		"exfat":    {data: at(3, "EXFAT   "), want: partition.Filesystem{Type: "exfat"}},
		"fat32": {
			data: fatBootSector(true, 1<<20, "EFI"),
			want: partition.Filesystem{Type: "vfat", Version: "FAT32", Label: "EFI"},
		},
		"fat16": {data: fatBootSector(false, 65536, "NO NAME"), want: partition.Filesystem{Type: "vfat", Version: "FAT16"}},
		"fat12": {
			data: fatBootSector(false, 8000, "FLOPPY"),
			want: partition.Filesystem{Type: "vfat", Version: "FAT12", Label: "FLOPPY"},
		},
		"not fat": {data: append([]byte{0xEB, 0x63, 0x90}, make([]byte, 600)...), want: partition.Filesystem{Type: "unknown"}},
		// Probe reads no further than the partition, even when that cuts a magic off
		"a btrfs magic past the end of the partition": {
			data: at(0x10040, "_BHRfS_M"), start: mib, size: 0x10000, want: partition.Filesystem{Type: partition.FSUnknown},
		},
		"a file system less than ProbeSize bytes before math.MaxInt64": {
			data: extSuperblock(0x4, 0x2|0x40, 0x3, "last"), start: math.MaxInt64 - 4096, size: partition.ProbeSize,
			disk: math.MaxInt64, want: partition.Filesystem{Type: partition.FSExt4, Label: "last"},
		},
		// a file that ends inside a magic or the field after it is not that file system
		"a file of the luks magic only": {
			data: []byte("LUKS\xba\xbe"), start: -1, want: partition.Filesystem{Type: partition.FSUnknown},
		},
		"a file of the luks magic and one byte": {
			data: []byte("LUKS\xba\xbe\x00"), start: -1, want: partition.Filesystem{Type: partition.FSUnknown},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			or := func(v, def int64) int64 {
				if v == 0 {
					return def
				}

				return v
			}

			start, size, diskSize := or(tt.start, 3*mib), or(tt.size, 128*mib), or(tt.disk, gib)
			if tt.start == -1 {
				// a file the size of the data, probed whole
				start, size, diskSize = 0, int64(len(tt.data)), int64(len(tt.data))
			}

			disk := newDisk(diskSize).write(start, tt.data)

			got, err := partition.Probe(disk, start, size)
			if err != nil {
				t.Fatalf("Probe() error = %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Probe() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestInspect(t *testing.T) {
	t.Parallel()

	t.Run("partitions", func(t *testing.T) {
		t.Parallel()

		disk := newDisk(gib).
			write(0, mbr(0, mbrEntry{typ: 0x0c, first: 2048, sectors: 2048}, mbrEntry{typ: 0x05, first: 4096, sectors: 8192}, mbrEntry{typ: 0x83, first: 16384, sectors: 8192})).
			write(2048*512, fatBootSector(true, 2048, "EFI")).
			write(4096*512, mbr(0, mbrEntry{typ: 0x82, first: 2048, sectors: 2048})).
			write(6144*512, at(4086, "SWAPSPACE2")).
			write(16384*512, extSuperblock(0x4, 0x2|0x40, 0, "kali"))

		got, err := partition.Inspect(disk, disk.size)
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}

		type fsOf struct {
			Number     int
			Filesystem *partition.Filesystem
		}

		fss := make([]fsOf, 0, len(got.Partitions))
		for _, p := range got.Partitions {
			fss = append(fss, fsOf{Number: p.Number, Filesystem: p.Filesystem})
		}

		want := []fsOf{
			{Number: 1, Filesystem: &partition.Filesystem{Type: "vfat", Version: "FAT32", Label: "EFI"}},
			{Number: 2},
			{Number: 3, Filesystem: &partition.Filesystem{Type: "ext4", Label: "kali"}},
			{Number: 5, Filesystem: &partition.Filesystem{Type: "swap"}},
		}

		if diff := cmp.Diff(want, fss); diff != "" {
			t.Errorf("Inspect() file systems mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("whole disk", func(t *testing.T) {
		t.Parallel()

		disk := newDisk(gib).write(0x8001, []byte("CD001"))

		got, err := partition.Inspect(disk, disk.size)
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}

		want := partition.Table{Scheme: partition.SchemeNone, Filesystem: &partition.Filesystem{Type: "iso9660"}}
		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("Inspect() mismatch (-want +got):\n%s", diff)
		}
	})
}

// failingDisk fails every read at or past failAt.
type failingDisk struct {
	*testDisk

	failAt int64
}

var errDevice = errors.New("device error")

func (d failingDisk) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > d.failAt {
		return 0, errDevice
	}

	return d.testDisk.ReadAt(p, off)
}

// partitionEOFDisk returns [io.EOF] with every read, as an [io.ReaderAt] may at the end of its data.
type partitionEOFDisk struct {
	*testDisk
}

func (d partitionEOFDisk) ReadAt(p []byte, off int64) (int, error) {
	n, _ := d.testDisk.ReadAt(p, off)

	return n, io.EOF
}

// A read error is returned, with what was read before it; [io.EOF] is not an error.
func TestReadErrors(t *testing.T) {
	t.Parallel()

	partitioned := func() *testDisk {
		return newDisk(gib).
			write(0, mbr(0, mbrEntry{typ: 0x83, first: 2048, sectors: 2048}, mbrEntry{typ: 0x83, first: 1 << 20, sectors: 2048})).
			write(2048*512, extSuperblock(0, 0, 0, "first"))
	}

	tests := map[string]struct {
		call    func(io.ReaderAt, int64) (partition.Table, error)
		disk    io.ReaderAt
		size    int64
		wantErr error
		// the file system labels of the partitions, "-" for none read; or of the whole disk
		wantLabels []string
	}{
		"Read fails on the first sector": {
			call: partition.Read, disk: failingDisk{partitioned(), 0}, size: gib, wantErr: errDevice,
		},
		"Inspect fails on the first sector": {
			call: partition.Inspect, disk: failingDisk{partitioned(), 0}, size: gib, wantErr: errDevice,
		},
		"Inspect fails reading a file system on the whole disk": {
			call: partition.Inspect, disk: failingDisk{newDisk(gib), 600}, size: gib, wantErr: errDevice,
		},
		"Inspect fails past 512 MiB": {
			call: partition.Inspect, disk: failingDisk{partitioned(), 1 << 29}, size: gib, wantErr: errDevice,
			wantLabels: []string{"first", "-"},
		},
		"Inspect of a disk whose reads end with io.EOF": {
			call: partition.Inspect, disk: partitionEOFDisk{partitioned()}, size: gib, wantLabels: []string{"first", "-"},
		},
		"Inspect of a 600-byte disk with a partition past its end": {
			call: partition.Inspect, disk: newDisk(600).write(0, mbr(0, mbrEntry{typ: 0x83, first: 1, sectors: 1})),
			size: 600,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.call(tt.disk, tt.size)
			if !errors.Is(err, tt.wantErr) || (err == nil) != (tt.wantErr == nil) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}

			if got.Filesystem != nil {
				t.Errorf("whole-disk file system = %+v, want none after an error", got.Filesystem)
			}

			labels := make([]string, 0, len(got.Partitions))

			for _, p := range got.Partitions {
				label := "-"
				if p.Filesystem != nil && p.Filesystem.Type != partition.FSUnknown {
					label = p.Filesystem.Label
				}

				labels = append(labels, label)
			}

			if diff := cmp.Diff(tt.wantLabels, labels, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("file systems read mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Inspect reads the file systems of the first MaxProbes partitions only, and
// says so.
func TestInspectStopsAtMaxProbes(t *testing.T) {
	t.Parallel()

	entries := make([]gptEntry, partition.MaxProbes+2)
	for i := range entries {
		first := uint64(64 + 2*i)
		entries[i] = gptEntry{typ: guidBytes(linuxType), first: first, last: first + 1}
	}

	disk := gptDisk(4*mib, 512, entries...)

	got, err := partition.Inspect(disk, disk.size)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	if len(got.Partitions) != len(entries) {
		t.Fatalf("Inspect() found %d partitions, want %d", len(got.Partitions), len(entries))
	}

	probed := 0

	for _, p := range got.Partitions {
		if p.Filesystem != nil {
			probed++
		}
	}

	if probed != partition.MaxProbes || got.Partitions[partition.MaxProbes].Filesystem != nil {
		t.Errorf("Inspect() read %d file systems, partition %d's = %v; want %d, none",
			probed, partition.MaxProbes+1, got.Partitions[partition.MaxProbes].Filesystem, partition.MaxProbes)
	}

	want := []string{"phenix read the file systems of the first 64 partitions only"}
	if diff := cmp.Diff(want, got.Notes); diff != "" {
		t.Errorf("Inspect() notes mismatch (-want +got):\n%s", diff)
	}
}
