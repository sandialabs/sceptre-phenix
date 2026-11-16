package cmd

import (
	"bytes"
	"encoding/binary"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// boltFreePages never panics on a store file it reads without the lock. When it gives a count, the free pages are
// at most the pages, the pages are those of the newer valid meta page, and the free pages are the free page list count.
func FuzzBoltFreePages(f *testing.F) {
	for _, count := range []int{0, 1, 3, boltCountOverflow + 1} {
		f.Add(doctorFreeBolt(count), int64(4*doctorFilesPageSize))
	}

	f.Add(doctorFilesBolt(doctorFilesPageSize), int64(4*doctorFilesPageSize))
	f.Add(doctorFreeBolt(1)[:2*doctorFilesPageSize+8], int64(4*doctorFilesPageSize))
	f.Add([]byte{}, int64(0))

	zeroPageSize := doctorFreeBolt(1)
	binary.NativeEndian.PutUint32(zeroPageSize[boltPageHeader+8:], 0)
	doctorFilesResum(zeroPageSize)
	f.Add(zeroPageSize, int64(len(zeroPageSize)))

	f.Fuzz(func(t *testing.T, data []byte, size int64) {
		env := fakeDoctorEnv(nil, false)
		env.pageSize = doctorFilesPageSize
		env.readAt = doctorFilesReadAt(data)

		free, pages, why := env.boltFreePages("/store.bdb", size)
		if why != "" {
			if free != 0 || pages != 0 {
				t.Fatalf("boltFreePages() = %d, %d with the reason %q, want no counts", free, pages, why)
			}

			return
		}

		if free > pages {
			t.Fatalf("boltFreePages() = %d free of %d pages", free, pages)
		}

		first, _ := parseBoltMeta(data)

		at := uint64(doctorFilesPageSize)
		if first.valid() {
			at = uint64(first.pageSize)
		}

		second, _ := parseBoltMeta(data[min(uint64(len(data)), at):])

		meta := second
		if !second.valid() || first.valid() && first.txid >= second.txid {
			meta = first
		}

		if pages != meta.pages {
			t.Fatalf("boltFreePages() pages = %d, want %d of the newer valid meta page", pages, meta.pages)
		}

		list := data[min(uint64(len(data)), meta.freelist*at):]
		if len(list) < boltPageHeader {
			t.Fatalf("boltFreePages() = %d, %d without a free page list header at page %d", free, pages, meta.freelist)
		}

		want := uint64(binary.NativeEndian.Uint16(list[10:]))
		if want == boltCountOverflow && len(list) >= boltFreelistRead {
			want = binary.NativeEndian.Uint64(list[boltPageHeader:])
		}

		if free != want {
			t.Fatalf("boltFreePages() free = %d, want %d from the free page list", free, want)
		}
	})
}

// shebangProblem never panics on the start of a program file, finds no problem in a file that does not start with
// #!, and names the file in any problem it finds.
func FuzzShebangProblem(f *testing.F) {
	for _, head := range []string{
		"", "#!", "#!\n", "#!/bin/sh\n", "#!/bin/sh\r\n", "#! python3\n", "#!/usr/bin/env -S -u X -C / sh -e\n",
		"#!/usr/bin/env A=1 nosuch\n", "#!/usr/bin/env /nowhere/py\n", "\x7fELF\x02\x01",
	} {
		f.Add([]byte(head))
	}

	f.Fuzz(func(t *testing.T, head []byte) {
		env := fakeDoctorEnv(nil, false)
		env.readAt = func(string, int64, int) ([]byte, error) { return head[:min(len(head), shebangRead)], nil }
		env.stat = func(path string) (os.FileInfo, error) {
			if path == "/bin/sh" || path == "/usr/bin/env" {
				return os.Stat(".")
			}

			return nil, fs.ErrNotExist
		}

		problem := env.shebangProblem("/app")

		switch {
		case !bytes.HasPrefix(head, []byte("#!")) && problem != "":
			t.Fatalf("shebangProblem(%q) = %q, want none without #!", head, problem)
		case problem != "" && !strings.HasPrefix(problem, "/app ") && !strings.HasPrefix(problem, "the #! line of /app "):
			t.Fatalf("shebangProblem(%q) = %q, want it to name /app", head, problem)
		}
	})
}
