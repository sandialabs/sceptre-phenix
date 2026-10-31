package partition_test

import (
	"testing"

	"pgregory.net/rapid"

	"phenix/util/partition"
)

// genMBREntry draws an MBR or extended boot record entry: often a plausible one inside a disk of sectors sectors,
// sometimes any values.
func genMBREntry(sectors uint32) *rapid.Generator[mbrEntry] {
	return rapid.Custom(func(t *rapid.T) mbrEntry {
		if rapid.IntRange(0, 4).Draw(t, "wild") == 0 {
			return mbrEntry{
				boot:    rapid.Byte().Draw(t, "boot"),
				typ:     rapid.Byte().Draw(t, "type"),
				first:   rapid.Uint32().Draw(t, "first"),
				sectors: rapid.Uint32().Draw(t, "sectors"),
			}
		}

		return mbrEntry{
			boot:    rapid.SampledFrom([]byte{0, 0x80}).Draw(t, "boot"),
			typ:     rapid.SampledFrom([]byte{0, 0x83, 0x82, 0x05, 0x0f, 0x85, 0x07, 0xee}).Draw(t, "type"),
			first:   rapid.Uint32Range(0, sectors+16).Draw(t, "first"),
			sectors: rapid.Uint32Range(0, sectors).Draw(t, "sectors"),
		}
	})
}

// genGPTEntry draws a GPT entry, mostly inside a disk of sectors sectors.
func genGPTEntry(sectors uint64) *rapid.Generator[gptEntry] {
	return rapid.Custom(func(t *rapid.T) gptEntry {
		var typ [16]byte
		if rapid.Bool().Draw(t, "used") {
			typ = guidBytes(rapid.SampledFrom([]string{efiType, linuxType, diskGUID}).Draw(t, "type"))
		}

		return gptEntry{
			typ:   typ,
			first: rapid.Uint64Range(0, sectors+16).Draw(t, "first"),
			last:  rapid.Uint64Range(0, sectors+16).Draw(t, "last"),
			attrs: rapid.Uint64().Draw(t, "attrs"),
			name:  rapid.StringN(0, 36, -1).Draw(t, "name"),
		}
	})
}

// genDisk draws a disk with an MBR and extended boot records, a GPT, or neither, and file system magic and random
// bytes anywhere in it.
func genDisk() *rapid.Generator[*testDisk] {
	return rapid.Custom(func(t *rapid.T) *testDisk {
		size := rapid.Int64Range(0, 1<<34).Draw(t, "size")
		sectors := uint32(min(size/512, 1<<32-1))

		var disk *testDisk

		switch rapid.IntRange(0, 2).Draw(t, "scheme") {
		case 0:
			disk = newDisk(size).write(0, mbr(0, rapid.SliceOfN(genMBREntry(sectors), 0, 4).Draw(t, "primaries")...))

			for range rapid.IntRange(0, 6).Draw(t, "ebrs") {
				off := int64(rapid.Uint32Range(0, sectors).Draw(t, "ebr")) * 512
				disk.write(off, mbr(0, rapid.SliceOfN(genMBREntry(sectors), 0, 4).Draw(t, "logicals")...))
			}
		case 1:
			ss := rapid.SampledFrom([]int64{512, 4096}).Draw(t, "sectorSize")
			if size < 64*ss {
				size = 64 * ss
			}

			disk = gptDisk(size, ss, rapid.SliceOfN(genGPTEntry(uint64(size/ss)), 0, 8).Draw(t, "entries")...)
		default:
			disk = newDisk(size)
		}

		for range rapid.IntRange(0, 4).Draw(t, "extras") {
			off := rapid.Int64Range(0, max(size, 1)).Draw(t, "at")
			data := rapid.SampledFrom([][]byte{
				extSuperblock(4, 0x42, 0, "kali"), at(0x8001, "CD001"), fatBootSector(true, 1<<20, "EFI"),
				[]byte("XFSB"), []byte("EFI PART"), {0x55, 0xAA},
			}).Draw(t, "magic")

			if rapid.Bool().Draw(t, "random") {
				data = rapid.SliceOfN(rapid.Byte(), 1, 1024).Draw(t, "bytes")
			}

			disk.write(off, data)
		}

		return disk
	})
}

// checkTable fails t unless the table read from disk keeps the invariants: every read and partition inside the
// disk, distinct numbers as Linux gives them, and a file system only where Inspect reads one.
func checkTable(t interface {
	Fatalf(format string, args ...any)
}, disk *testDisk, table partition.Table, inspected bool,
) {
	if len(disk.outside) != 0 {
		t.Fatalf("read outside the disk of %d bytes %d times", disk.size, len(disk.outside))
	}

	if len(table.Partitions) > partition.MaxPartitions {
		t.Fatalf("%d partitions, want at most %d", len(table.Partitions), partition.MaxPartitions)
	}

	if table.Scheme != partition.SchemeMBR && table.Scheme != partition.SchemeGPT && len(table.Partitions) != 0 {
		t.Fatalf("scheme %q with %d partitions, want none", table.Scheme, len(table.Partitions))
	}

	if (table.Filesystem != nil) != (inspected && table.Scheme == partition.SchemeNone) {
		t.Fatalf("scheme %q has whole-disk file system %v", table.Scheme, table.Filesystem)
	}

	seen := make(map[int]bool)
	probed := 0

	for _, p := range table.Partitions {
		switch {
		case p.Start < 0 || p.Size <= 0 || p.Start > disk.size-p.Size:
			t.Fatalf("partition %d at %d of %d bytes is outside the disk of %d bytes", p.Number, p.Start, p.Size, disk.size)
		case p.Start%table.SectorSize != 0 || p.Size%table.SectorSize != 0:
			t.Fatalf("partition %d at %d of %d bytes is not in whole sectors of %d", p.Number, p.Start, p.Size, table.SectorSize)
		case seen[p.Number] || p.Number < 1:
			t.Fatalf("partition number %d is repeated or below 1", p.Number)
		case table.Scheme == partition.SchemeMBR && p.Logical != (p.Number >= 5):
			t.Fatalf("MBR partition %d logical = %t", p.Number, p.Logical)
		case p.Extended && p.Filesystem != nil:
			t.Fatalf("extended partition %d has a file system", p.Number)
		}

		seen[p.Number] = true

		if p.Filesystem != nil {
			probed++
		}

		if inspected && !p.Extended && p.Filesystem == nil && probed < partition.MaxProbes {
			t.Fatalf("partition %d has no file system after Inspect", p.Number)
		}
	}
}

// Read and Inspect never panic, keep every read inside the disk, and return partitions inside it, whatever the disk
// holds.
func TestReadProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		disk := genDisk().Draw(t, "disk")

		table, err := partition.Read(disk, disk.size)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}

		checkTable(t, disk, table, false)

		inspected, err := partition.Inspect(disk, disk.size)
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}

		checkTable(t, disk, inspected, true)

		if len(inspected.Partitions) != len(table.Partitions) {
			t.Fatalf("Inspect() found %d partitions, Read() %d", len(inspected.Partitions), len(table.Partitions))
		}
	})
}
