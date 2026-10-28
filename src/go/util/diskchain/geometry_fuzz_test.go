package diskchain_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"testing"

	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
)

// FuzzParseGeometry checks that ParseGeometry never panics on an untrusted image header, and that it reads each
// header ParseHeader accepts as the header's own fields say (see geometryCheck).
func FuzzParseGeometry(f *testing.F) {
	for _, seed := range headerSeeds() {
		f.Add(seed)
	}

	v3 := dct.Qcow2{}.Bytes()
	binary.BigEndian.PutUint32(v3[96:], 6)
	f.Add(v3)

	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := diskchain.ParseHeader(bytes.NewReader(data))
		if err != nil {
			return
		}

		g, err := diskchain.ParseGeometry(bytes.NewReader(data), h)
		if problem := geometryCheck(data, h, g, err); problem != nil {
			t.Fatalf("ParseGeometry(%q): %v", data, problem)
		}
	})
}

// geometryCheck fails when g and err, what ParseGeometry read from data whose header is h, differ from the fields of
// the header: its virtual size, cluster size and snapshot count, and the version 3 compatible bits and reference count
// width of 1 to 64 bits, or the version 2 width of 16 bits.
func geometryCheck(data []byte, h diskchain.Header, g diskchain.Geometry, err error) error {
	qcow, qcow2 := h.Format == diskchain.FormatQcow, h.Format == diskchain.FormatQcow2
	be := binary.BigEndian

	switch {
	case !qcow && !qcow2:
		if !errors.Is(err, diskchain.ErrBadHeader) {
			return fmt.Errorf("error %w for a %q image, want ErrBadHeader", err, h.Format)
		}

		return nil
	case err != nil:
		return fmt.Errorf("error %w for a header ParseHeader accepts", err)
	case g.VirtualSize != be.Uint64(data[24:]):
		return fmt.Errorf("virtual size %d, want %d", g.VirtualSize, be.Uint64(data[24:]))
	case qcow && (g.ClusterSize != 1<<data[32] || g.RefcountBits != 0):
		return fmt.Errorf("qcow cluster size %d and refcount bits %d", g.ClusterSize, g.RefcountBits)
	case qcow2 && g.ClusterSize != 1<<be.Uint32(data[20:]):
		return fmt.Errorf("qcow2 cluster size %d, want %d", g.ClusterSize, uint64(1)<<be.Uint32(data[20:]))
	case qcow2 && (g.RefcountBits < 1 || g.RefcountBits > 64 || bits.OnesCount64(g.RefcountBits) != 1):
		return fmt.Errorf("qcow2 refcount bits %d", g.RefcountBits)
	case qcow2 && h.Version == 2 && (g.RefcountBits != 16 || g.Compatible != 0):
		return fmt.Errorf("qcow2 version 2 refcount bits %d and compatible bits 0x%x", g.RefcountBits, g.Compatible)
	case qcow2 && g.Snapshots != be.Uint32(data[60:]):
		return fmt.Errorf("qcow2 snapshots %d, want %d", g.Snapshots, be.Uint32(data[60:]))
	case qcow2 && h.Version >= 3 && len(data) < 104:
		return fmt.Errorf("qcow2 version 3 header of %d bytes, want at least 104", len(data))
	case qcow2 && h.Version >= 3 && g.RefcountBits != uint64(1)<<min(be.Uint32(data[96:]), 6):
		return fmt.Errorf("qcow2 refcount bits %d, want %d", g.RefcountBits, uint64(1)<<min(be.Uint32(data[96:]), 6))
	case qcow2 && h.Version >= 3 && g.Compatible != be.Uint64(data[80:]):
		return fmt.Errorf("qcow2 compatible bits 0x%x, want 0x%x", g.Compatible, be.Uint64(data[80:]))
	}

	return nil
}
