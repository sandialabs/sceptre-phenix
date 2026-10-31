package partition_test

import (
	"errors"
	"io"
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
	tests := map[string]struct {
		data []byte
		want partition.Filesystem
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
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			const start = 3 * mib

			disk := newDisk(gib).write(start, tt.data)

			got, err := partition.Probe(disk, start, 128*mib)
			if err != nil {
				t.Fatalf("Probe() error = %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Probe() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Probe reads no further than the partition, even when that cuts a magic off.
func TestProbeStaysInThePartition(t *testing.T) {
	disk := newDisk(gib).write(mib, at(0x10040, "_BHRfS_M"))

	got, err := partition.Probe(disk, mib, 0x10000)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if got.Type != partition.FSUnknown {
		t.Errorf("Probe() of a partition that ends before the btrfs magic = %q, want %q", got.Type, partition.FSUnknown)
	}
}

// A file that ends inside a magic or the field after it is not that file system.
func TestProbeShortFile(t *testing.T) {
	tests := map[string][]byte{
		"luks magic only":         []byte("LUKS\xba\xbe"),
		"luks magic and one byte": []byte("LUKS\xba\xbe\x00"),
	}

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			disk := newDisk(int64(len(data))).write(0, data)

			got, err := partition.Probe(disk, 0, disk.size)
			if err != nil {
				t.Fatalf("Probe() error = %v", err)
			}

			if got.Type != partition.FSUnknown {
				t.Errorf("Probe() of a %d-byte file = %q, want %q", len(data), got.Type, partition.FSUnknown)
			}
		})
	}
}

func TestInspect(t *testing.T) {
	t.Run("partitions", func(t *testing.T) {
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

// A read error is returned, with what was read before it; [io.EOF] is not an error.
func TestReadErrors(t *testing.T) {
	disk := newDisk(gib).
		write(0, mbr(0, mbrEntry{typ: 0x83, first: 2048, sectors: 2048}, mbrEntry{typ: 0x83, first: 1 << 20, sectors: 2048})).
		write(2048*512, extSuperblock(0, 0, 0, "first"))

	if _, err := partition.Read(failingDisk{disk, 0}, disk.size); !errors.Is(err, errDevice) {
		t.Errorf("Read() of a disk that fails error = %v, want %v", err, errDevice)
	}

	got, err := partition.Inspect(failingDisk{disk, 1 << 29}, disk.size)
	if !errors.Is(err, errDevice) {
		t.Fatalf("Inspect() of a disk that fails past 512 MiB error = %v, want %v", err, errDevice)
	}

	if got.Partitions[0].Filesystem == nil || got.Partitions[0].Filesystem.Label != "first" || got.Partitions[1].Filesystem != nil {
		t.Errorf("Inspect() of a disk that fails past 512 MiB = %+v, want the first file system only", got.Partitions)
	}

	short := newDisk(600).write(0, mbr(0, mbrEntry{typ: 0x83, first: 1, sectors: 1}))
	if _, err := partition.Inspect(short, 600); errors.Is(err, io.EOF) || err != nil {
		t.Errorf("Inspect() of a short disk error = %v, want nil", err)
	}
}
