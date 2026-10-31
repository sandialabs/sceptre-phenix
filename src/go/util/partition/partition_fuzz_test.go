package partition_test

import (
	"testing"

	"phenix/util/partition"
)

// Inspect keeps its invariants (see checkTable) for any disk image.
func FuzzInspect(f *testing.F) {
	const size = 16 << 10

	f.Add([]byte{})
	f.Add(newDisk(4096).write(0, mbr(0, mbrEntry{boot: 0x80, typ: 0x83, first: 1, sectors: 6})).bytes())
	f.Add(newDisk(size).
		write(0, mbr(0, mbrEntry{typ: 0x05, first: 8, sectors: 24})).
		write(8*512, mbr(0, mbrEntry{typ: 0x83, first: 1, sectors: 4}, mbrEntry{typ: 0x05, first: 16, sectors: 8})).
		write(24*512, mbr(0, mbrEntry{typ: 0x82, first: 1, sectors: 4})).bytes())
	f.Add(gptDisk(size, 512,
		gptEntry{typ: guidBytes(efiType), first: 4, last: 9, name: "EFI"},
		gptEntry{typ: guidBytes(linuxType), first: 10, last: 29, name: "kali"},
	).write(4*512, fatBootSector(true, 6, "EFI")).write(10*512, extSuperblock(4, 0x42, 0, "kali")).bytes())
	f.Add(newDisk(0x8800).write(0x8001, []byte("CD001")).bytes())
	f.Add([]byte("LUKS\xba\xbe"))
	f.Add([]byte("LUKS\xba\xbe\x00"))

	f.Fuzz(func(t *testing.T, data []byte) {
		disk := newDisk(int64(len(data))).write(0, data)

		table, err := partition.Inspect(disk, disk.size)
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}

		checkTable(t, disk, table, true)
	})
}
