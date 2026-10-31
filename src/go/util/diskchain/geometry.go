package diskchain

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// FeatureLazyRefcounts is the compatible feature bit of a qcow2 version 3 image whose reference counts qemu writes
// late.
const FeatureLazyRefcounts uint64 = 1

const (
	geometryRead      = 112
	qcowClusterBitsAt = 32
	refcountOrderAt   = 96
	maxRefcountOrder  = 6
	v2RefcountOrder   = 4
)

// Geometry is the layout of a qcow or qcow2 image, from its header (see ParseGeometry).
type Geometry struct {
	// VirtualSize is the size in bytes of the disk the image holds.
	VirtualSize uint64
	// ClusterSize is the cluster size of the image in bytes.
	ClusterSize uint64
	// RefcountBits is the width of a qcow2 reference count in bits, or 0 for qcow.
	RefcountBits uint64
	// Compatible are the compatible feature bits of a qcow2 version 3 image (see FeatureLazyRefcounts).
	Compatible uint64
	// Snapshots is the number of internal snapshots in a qcow2 image.
	Snapshots uint32
}

// ParseGeometry reads the layout of the qcow or qcow2 image r holds, whose header ParseHeader read as h with no
// error. It returns an error when r cannot be read or h is of another format.
func ParseGeometry(r io.ReaderAt, h Header) (Geometry, error) {
	g := Geometry{VirtualSize: 0, ClusterSize: 0, RefcountBits: 0, Compatible: 0, Snapshots: 0}

	if h.Format != FormatQcow && h.Format != FormatQcow2 {
		return g, fmt.Errorf("%w: not a qcow or qcow2 image", ErrBadHeader)
	}

	buf := make([]byte, geometryRead)

	n, err := r.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return g, fmt.Errorf("reading image header: %w", err)
	}

	be := binary.BigEndian

	if h.Format == FormatQcow {
		if n < qcowHeaderLength {
			return g, fmt.Errorf("%w: %d bytes long", ErrBadHeader, n)
		}

		g.VirtualSize = be.Uint64(buf[24:])
		g.ClusterSize = 1 << min(buf[qcowClusterBitsAt], maxQcowClusterBits)

		return g, nil
	}

	if n < qcow2V2HeaderLength {
		return g, fmt.Errorf("%w: %d bytes long", ErrBadHeader, n)
	}

	g.VirtualSize = be.Uint64(buf[24:])

	g.ClusterSize = 1 << min(be.Uint32(buf[20:]), maxClusterBits)
	g.Snapshots = be.Uint32(buf[60:])
	order := uint32(v2RefcountOrder)

	if h.Version >= 3 && n >= qcow2V3HeaderLength {
		g.Compatible = be.Uint64(buf[80:])
		order = min(be.Uint32(buf[refcountOrderAt:]), maxRefcountOrder)
	}

	g.RefcountBits = 1 << order

	return g, nil
}
