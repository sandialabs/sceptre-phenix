package cmd

import (
	"encoding/binary"
	"hash/fnv"
	"math/bits"
	"slices"
	"testing"
)

// doctorFilesFuzzMetaOK is the model of a meta page that bbolt accepts: the
// magic number, version 2, and a checksum of 0 or the FNV-1a of its bytes.
func doctorFilesFuzzMetaOK(page []byte) bool {
	if len(page) < boltPageHeader+boltMetaSize {
		return false
	}

	order := binary.NativeEndian
	meta := page[boltPageHeader:]
	hash := fnv.New64a()
	_, _ = hash.Write(meta[:boltMetaSummed])
	sum := order.Uint64(meta[boltMetaSummed:])

	return order.Uint32(meta) == boltMagic && order.Uint32(meta[4:]) == boltVersion && (sum == 0 || sum == hash.Sum64())
}

// doctorFilesFuzzModel is the status storeFileCheck gives data, on a host of
// hostPage-byte pages.
func doctorFilesFuzzModel(data []byte, hostPage int) doctorStatus {
	size := len(data)
	if size == 0 {
		return doctorPass
	}

	if size < boltPageHeader+boltMetaSize ||
		binary.NativeEndian.Uint32(data[boltPageHeader:]) == bits.ReverseBytes32(boltMagic) {
		return doctorFail
	}

	first := doctorFilesFuzzMetaOK(data)
	pageSize := hostPage

	if first {
		pageSize = int(binary.NativeEndian.Uint32(data[boltPageHeader+8:]))
		if pageSize < boltMinPageSize || pageSize&(pageSize-1) != 0 {
			return doctorFail
		}
	}

	if size < 2*pageSize || size < boltFirstRead {
		return doctorFail
	}

	switch second := doctorFilesFuzzMetaOK(data[pageSize:]); {
	case first && second:
		return doctorPass
	case first || second:
		return doctorWarn
	}

	return doctorFail
}

// storeFileCheck gives the status of the model for any file, and reads only
// the first bytes bbolt reads and the second meta page.
func FuzzStoreFileCheck(f *testing.F) {
	valid := doctorFilesBolt(doctorFilesPageSize)
	firstZeroed := slices.Clone(valid)
	clear(firstZeroed[:doctorFilesPageSize])
	secondBad := slices.Clone(valid)
	secondBad[doctorFilesPageSize+boltPageHeader+48]++

	for _, seed := range [][]byte{
		valid, firstZeroed, secondBad, valid[:doctorFilesPageSize], valid[:doctorFilesPageSize+100],
		doctorFilesBolt(1024)[:3000], doctorFilesBolt(1024), doctorFilesBolt(8192), []byte("hello\n"), {},
		make([]byte, 5000),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		type read struct {
			off int64
			n   int
		}

		var reads []read

		env := fakeDoctorEnv(nil, false)
		readAt := doctorFilesReadAt(data)
		env.readAt = func(path string, off int64, n int) ([]byte, error) {
			reads = append(reads, read{off: off, n: n})

			return readAt(path, off, n)
		}

		got := env.storeFileCheck("store.bdb", int64(len(data)))

		if want := doctorFilesFuzzModel(data, env.pageSize); got.Status != want {
			t.Fatalf("storeFileCheck() = %+v, want %s", got, want)
		}

		for i, r := range reads {
			first := i == 0 && r.off == 0 && r.n == boltFirstRead
			second := i == 1 && r.off >= boltMinPageSize && r.off&(r.off-1) == 0 && r.n == boltPageHeader+boltMetaSize

			if !first && !second {
				t.Fatalf("storeFileCheck() read %d bytes at %d (read %d), beyond the meta pages", r.n, r.off, i)
			}
		}
	})
}
