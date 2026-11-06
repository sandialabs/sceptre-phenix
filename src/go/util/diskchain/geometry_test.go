package diskchain_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
	"phenix/util/testenv"
)

// ParseGeometry reads the layout of a qcow or qcow2 header, keeping cluster sizes and reference count widths within
// what qemu accepts, and fails for another format, a short header or a read error.
func TestParseGeometry(t *testing.T) {
	t.Parallel()

	v3 := dct.Qcow2{}.Bytes()
	binary.BigEndian.PutUint64(v3[80:], diskchain.FeatureLazyRefcounts)
	binary.BigEndian.PutUint32(v3[96:], 6)
	binary.BigEndian.PutUint32(v3[60:], 2)

	// a version 3 header with cluster bits and a refcount order past what qemu accepts
	wide := dct.Qcow2{}.Bytes()
	binary.BigEndian.PutUint32(wide[20:], 30)
	binary.BigEndian.PutUint32(wide[96:], 9)

	qcowWide := dct.Qcow{}.Bytes()
	qcowWide[32] = 20

	qcow := diskchain.Header{Format: diskchain.FormatQcow, Version: 1}
	qcow2v3 := diskchain.Header{Format: diskchain.FormatQcow2, Version: 3}

	tests := map[string]struct {
		data    []byte
		header  *diskchain.Header // nil for what ParseHeader reads from data
		want    diskchain.Geometry
		wantErr string
	}{
		"qcow2 version 3": {
			data: v3,
			want: diskchain.Geometry{
				VirtualSize: 1 << 30, ClusterSize: 64 << 10, RefcountBits: 64,
				Compatible: diskchain.FeatureLazyRefcounts, Snapshots: 2,
			},
		},
		"qcow2 version 2 has 16-bit reference counts": {
			data: dct.Qcow2{Version: 2}.Bytes(),
			want: diskchain.Geometry{VirtualSize: 1 << 30, ClusterSize: 64 << 10, RefcountBits: 16},
		},
		"qcow version 1": {
			data: dct.Qcow{}.Bytes(),
			want: diskchain.Geometry{VirtualSize: 1 << 30, ClusterSize: 4 << 10},
		},
		"qcow2 cluster bits and refcount order past their limits": {
			data: wide, header: &qcow2v3,
			want: diskchain.Geometry{VirtualSize: 1 << 30, ClusterSize: 2 << 20, RefcountBits: 64},
		},
		"qcow cluster bits past their limit": {
			data: qcowWide, header: &qcow,
			want: diskchain.Geometry{VirtualSize: 1 << 30, ClusterSize: 64 << 10},
		},
		"a version 3 header cut short reads as version 2": {
			data: v3[:80], header: &qcow2v3,
			want: diskchain.Geometry{VirtualSize: 1 << 30, ClusterSize: 64 << 10, RefcountBits: 16, Snapshots: 2},
		},
		"raw": {data: []byte("kali"), wantErr: "invalid qcow2 header: not a qcow or qcow2 image"},
		"a qcow2 header shorter than 72 bytes": {
			data: v3[:71], header: &qcow2v3, wantErr: "invalid qcow2 header: 71 bytes long",
		},
		"a qcow header shorter than 48 bytes": {
			data: dct.Qcow{}.Bytes()[:47], header: &qcow, wantErr: "invalid qcow2 header: 47 bytes long",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h, err := diskchain.ParseHeader(bytes.NewReader(tt.data))
			if tt.header != nil {
				h = *tt.header
			} else if err != nil {
				t.Fatalf("ParseHeader() error = %v", err)
			}

			got, err := diskchain.ParseGeometry(bytes.NewReader(tt.data), h)

			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}

			if gotErr != tt.wantErr || errors.Is(err, diskchain.ErrBadHeader) != (tt.wantErr != "") {
				t.Fatalf("ParseGeometry() error = %v, want %q wrapping ErrBadHeader", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseGeometry() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A header that cannot be read is an error that wraps the read error and is not a bad header.
func TestParseGeometryReadError(t *testing.T) {
	t.Parallel()

	r := headerFailingReader{data: dct.Qcow2{}.Bytes(), failAt: 0}

	got, err := diskchain.ParseGeometry(r, diskchain.Header{Format: diskchain.FormatQcow2, Version: 3})
	if !errors.Is(err, errHeaderRead) || errors.Is(err, diskchain.ErrBadHeader) {
		t.Errorf("ParseGeometry() error = %v, want only the read error %v", err, errHeaderRead)
	}

	if diff := cmp.Diff(diskchain.Geometry{}, got); diff != "" {
		t.Errorf("ParseGeometry() mismatch (-want +got):\n%s", diff)
	}
}

// ParseGeometry reads what `qemu-img info` reports for the images qemu-img writes. CI installs qemu-img.
func TestParseGeometryMatchesQemuImg(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")
	t.Parallel()

	dir := t.TempDir()

	tests := map[string][]string{
		"v3.qc2":       {"-f", "qcow2"},
		"v2.qc2":       {"-f", "qcow2", "-o", "compat=0.10,cluster_size=4096"},
		"lazy.qc2":     {"-f", "qcow2", "-o", "lazy_refcounts=on,refcount_bits=64,cluster_size=2M"},
		"refcount.qc2": {"-f", "qcow2", "-o", "refcount_bits=1"},
		"kali.qcow":    {"-f", "qcow"},
	}

	for name, args := range tests {
		qemuImg(t, dir, append(append([]string{"create", "-q"}, args...), name, "3G")...)

		var info struct {
			VirtualSize uint64 `json:"virtual-size"`
			ClusterSize uint64 `json:"cluster-size"`
			Specific    struct {
				Data struct {
					Compat        string `json:"compat"`
					RefcountBits  uint64 `json:"refcount-bits"`
					LazyRefcounts bool   `json:"lazy-refcounts"`
				} `json:"data"`
			} `json:"format-specific"`
		}

		if err := json.Unmarshal(qemuImg(t, dir, "info", "--output=json", name), &info); err != nil {
			t.Fatalf("decoding qemu-img info of %s: %v", name, err)
		}

		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}

		h, err := diskchain.ParseHeader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("ParseHeader(%s) error = %v", name, err)
		}

		got, err := diskchain.ParseGeometry(bytes.NewReader(data), h)
		if err != nil {
			t.Fatalf("ParseGeometry(%s) error = %v", name, err)
		}

		want := diskchain.Geometry{
			VirtualSize:  info.VirtualSize,
			ClusterSize:  info.ClusterSize,
			RefcountBits: info.Specific.Data.RefcountBits,
		}
		if info.Specific.Data.LazyRefcounts {
			want.Compatible = diskchain.FeatureLazyRefcounts
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("ParseGeometry(%s) mismatch with qemu-img info (-want +got):\n%s", name, diff)
		}
	}
}
