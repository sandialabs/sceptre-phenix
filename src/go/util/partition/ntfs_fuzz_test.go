package partition_test

import (
	"encoding/binary"
	"testing"

	pt "phenix/util/partition/partitiontest"
)

// ntfsFuzzBases are the volumes that FuzzCheckNTFS changes.
func ntfsFuzzBases() [][]byte {
	notClean := pt.Restart{LSN: 9, InUse: true}

	return [][]byte{
		pt.NTFS{}.Bytes(),
		pt.NTFS{Dirty: true}.Bytes(),
		pt.NTFS{First: notClean}.Bytes(),
		pt.NTFS{First: pt.Restart{LSN: 1}, Second: notClean}.Bytes(),
		pt.NTFS{EmptyLog: true}.Bytes(),
	}
}

// ntfsEdits returns the edits that make bad from base, in the form ntfsEdit reads, and the bytes cut from the end.
func ntfsEdits(base, bad []byte) ([]byte, uint16) {
	var edits []byte

	for i := range min(len(base), len(bad)) {
		if base[i] != bad[i] {
			edits = binary.LittleEndian.AppendUint16(edits, uint16(i))
			edits = append(edits, bad[i])
		}
	}

	return edits, uint16(max(len(base)-len(bad), 0))
}

// ntfsEdit changes a copy of base with edits, groups of a 2-byte offset and a byte, and cuts cut bytes from its end.
func ntfsEdit(base, edits []byte, cut uint16) []byte {
	b := append([]byte(nil), base...)

	for ; len(edits) >= 3; edits = edits[3:] {
		b[int(binary.LittleEndian.Uint16(edits))%len(b)] = edits[2]
	}

	return b[:len(b)-min(int(cut), len(b))]
}

// CheckNTFS keeps its invariants (see checkNTFSInvariants) for any volume. The input is a short list of edits to a
// valid volume, so the fuzzer changes the structures that CheckNTFS reads and minimizes its inputs quickly.
func FuzzCheckNTFS(f *testing.F) {
	bases := ntfsFuzzBases()

	for i := range bases {
		f.Add(uint8(i), uint16(0), []byte{})
	}

	for _, bad := range ntfsBadVolumes() {
		edits, cut := ntfsEdits(bases[0], bad)
		f.Add(uint8(0), cut, edits)
	}

	f.Fuzz(func(t *testing.T, base uint8, cut uint16, edits []byte) {
		checkNTFSInvariants(t, ntfsEdit(bases[int(base)%len(bases)], edits, cut))
	})
}
