// Package diskchaintest writes image headers for tests of disk backing chains. It is imported only by tests, as
// net/http/httptest is.
package diskchaintest

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

const (
	v2           = 2
	clusterBits  = 16
	virtualSize  = 1 << 30
	v2Length     = 72
	v3Length     = 104
	refcountBits = 4
)

// Qcow2 describes the qcow2 header Bytes writes. Only the fields that name the backing chain are meaningful; qemu
// cannot open the image, which has no tables.
type Qcow2 struct {
	// Version is 2 or 3; 0 writes 3.
	Version int
	// Backing is the backing file name, or "" for none.
	Backing string
	// BackingFormat, when not "", is written as the backing format header extension.
	BackingFormat string
	// HeaderLength is a version 3 header's length; 0 writes 104.
	HeaderLength int
	// DataFile sets the external data file bit of a version 3 header.
	DataFile bool
}

// Bytes returns the header q describes: the header, its extensions, then the backing file name.
func (q Qcow2) Bytes() []byte {
	version, length := q.Version, v2Length
	if version == 0 {
		version = 3
	}

	if version != v2 {
		length = q.HeaderLength
		if length == 0 {
			length = v3Length
		}
	}

	header := make([]byte, length)
	be := binary.BigEndian

	copy(header, "QFI\xfb")
	be.PutUint32(header[4:], uint32(version))
	be.PutUint32(header[20:], clusterBits)
	be.PutUint64(header[24:], uint64(virtualSize))

	if version != v2 && length >= v3Length {
		if q.DataFile {
			be.PutUint64(header[72:], 1<<2) //nolint:mnd // incompatible_features: external data file
		}

		be.PutUint32(header[96:], refcountBits)
		be.PutUint32(header[100:], uint32(length))
	}

	buf := append([]byte(nil), header...)

	if q.BackingFormat != "" {
		buf = appendExtension(buf, 0xE2792ACA, []byte(q.BackingFormat)) //nolint:mnd // backing format
	}

	buf = appendExtension(buf, 0, nil)

	if q.Backing != "" {
		be.PutUint64(buf[8:], uint64(len(buf)))
		be.PutUint32(buf[16:], uint32(len(q.Backing))) //nolint:gosec // G115 a test value
		buf = append(buf, q.Backing...)
	}

	return buf
}

// appendExtension appends a header extension of type typ holding data, padded to 8 bytes.
func appendExtension(buf []byte, typ uint32, data []byte) []byte {
	head := make([]byte, 8) //nolint:mnd // type and length
	binary.BigEndian.PutUint32(head, typ)
	binary.BigEndian.PutUint32(head[4:], uint32(len(data))) //nolint:gosec // G115 a test value

	buf = append(buf, head...)
	buf = append(buf, data...)

	for len(buf)%8 != 0 {
		buf = append(buf, 0)
	}

	return buf
}

// WriteQcow2 writes the header q describes to path, creating its folder.
func WriteQcow2(tb testing.TB, path string, q Qcow2) {
	tb.Helper()

	WriteFile(tb, path, q.Bytes())
}

// WriteFile writes content to path, creating its folder.
func WriteFile(tb testing.TB, path string, content []byte) {
	tb.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		tb.Fatalf("creating the folder of %s: %v", path, err)
	}

	if err := os.WriteFile(path, content, 0o600); err != nil {
		tb.Fatalf("writing %s: %v", path, err)
	}
}

// Chain writes a qcow2 image at each of paths, each backed by the next by its absolute path, and the last a raw
// image.
func Chain(tb testing.TB, paths ...string) {
	tb.Helper()

	for i, path := range paths {
		if i == len(paths)-1 {
			WriteFile(tb, path, []byte("raw base image"))

			break
		}

		WriteQcow2(tb, path, Qcow2{Version: 0, Backing: paths[i+1], BackingFormat: "", HeaderLength: 0, DataFile: false})
	}
}
