package diskchain_test

import (
	"errors"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
)

// errHeaderRead is what headerFailingReader returns for a read it fails.
var errHeaderRead = errors.New("device gone")

// headerFailingReader holds data and fails every read that starts at or past failAt.
type headerFailingReader struct {
	data   []byte
	failAt int64
}

func (r headerFailingReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.failAt {
		return 0, errHeaderRead
	}

	n := copy(p, r.data[min(off, int64(len(r.data))):])
	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

// A header that cannot be read, or a backing name past the bytes read that cannot be read, is an error that wraps
// the read error; only the backing name read is a bad header.
func TestParseHeaderReadError(t *testing.T) {
	t.Parallel()

	far := dct.Qcow{Backing: "b.img"}.Bytes()
	far[15] = 0xff // the backing name offset, past the bytes read

	tests := map[string]struct {
		r       headerFailingReader
		want    diskchain.Header
		wantErr string
		bad     bool
	}{
		"the header": {
			r:       headerFailingReader{data: dct.Qcow2{}.Bytes(), failAt: 0},
			want:    diskchain.Header{},
			wantErr: "reading image header: device gone",
		},
		"a qcow version 1 backing name past the header": {
			r:       headerFailingReader{data: far, failAt: 1},
			want:    diskchain.Header{Format: diskchain.FormatQcow, Version: 1},
			wantErr: "invalid qcow2 header: reading the backing file name: device gone",
			bad:     true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := diskchain.ParseHeader(tc.r)
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("ParseHeader() error = %v, want %q", err, tc.wantErr)
			}

			if !errors.Is(err, errHeaderRead) || errors.Is(err, diskchain.ErrBadHeader) != tc.bad {
				t.Errorf("ParseHeader() error = %v, want it to wrap the read error, and ErrBadHeader %t", err, tc.bad)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ParseHeader() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
