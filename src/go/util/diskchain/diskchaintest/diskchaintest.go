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
	zstdLength   = 112
	refcountBits = 4

	qcowLength      = 48
	qcowClusterBits = 12
	qcowL2Bits      = 9
)

// Qcow2 describes the qcow2 header Bytes writes. Only the fields that name the backing chain and the format are
// meaningful; qemu cannot open the image, which has no tables.
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
	// Encryption is the crypt_method: 0 none, 1 AES, 2 LUKS.
	Encryption uint32
	// Features are more incompatible feature bits of a version 3 header, such as 1 for the dirty bit.
	Features uint64
	// Zstd writes a version 3 header with the zstd compression type and its feature bit; a HeaderLength of 0 then
	// writes 112.
	Zstd bool
}

// Qcow describes the qcow version 1 header Bytes writes. qemu cannot open the image, which has no tables.
type Qcow struct {
	// Backing is the backing file name, or "" for none.
	Backing string
	// Encryption is the crypt_method: 0 none, 1 AES.
	Encryption uint32
}

// Bytes returns the header q describes: the header, its extensions, then the backing file name.
func (q Qcow2) Bytes() []byte {
	version, length := q.Version, v2Length
	if version == 0 {
		version = 3
	}

	if version != v2 {
		length = q.HeaderLength

		switch {
		case length == 0 && q.Zstd:
			length = zstdLength
		case length == 0:
			length = v3Length
		}
	}

	header := make([]byte, length)
	be := binary.BigEndian

	copy(header, "QFI\xfb")
	be.PutUint32(header[4:], uint32(version))
	be.PutUint32(header[20:], clusterBits)
	be.PutUint64(header[24:], uint64(virtualSize))
	be.PutUint32(header[32:], q.Encryption)

	if version != v2 && length >= v3Length {
		features := q.Features
		if q.DataFile {
			features |= 1 << 2 //nolint:mnd // incompatible_features: external data file
		}

		if q.Zstd && length > v3Length {
			features |= 1 << 3   //nolint:mnd // incompatible_features: compression type
			header[v3Length] = 1 // compression_type: zstd
		}

		be.PutUint64(header[72:], features)
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

// Bytes returns the header q describes, then the backing file name.
func (q Qcow) Bytes() []byte {
	header := make([]byte, qcowLength)
	be := binary.BigEndian

	copy(header, "QFI\xfb")
	be.PutUint32(header[4:], 1)
	be.PutUint64(header[24:], uint64(virtualSize))
	header[32], header[33] = qcowClusterBits, qcowL2Bits
	be.PutUint32(header[36:], q.Encryption)

	buf := append([]byte(nil), header...)

	if q.Backing != "" {
		be.PutUint64(buf[8:], uint64(len(buf)))
		be.PutUint32(buf[16:], uint32(len(q.Backing))) //nolint:gosec // G115 a test value
		buf = append(buf, q.Backing...)
	}

	return buf
}

// WriteQcow writes the qcow version 1 header q describes to path, creating its folder.
func WriteQcow(tb testing.TB, path string, q Qcow) {
	tb.Helper()

	WriteFile(tb, path, q.Bytes())
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

		WriteQcow2(tb, path, Qcow2{
			Version: 0, Backing: paths[i+1], BackingFormat: "", HeaderLength: 0, DataFile: false,
			Encryption: 0, Features: 0, Zstd: false,
		})
	}
}
