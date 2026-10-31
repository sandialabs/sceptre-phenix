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

func TestParseGeometry(t *testing.T) {
	v3 := dct.Qcow2{}.Bytes()
	binary.BigEndian.PutUint64(v3[80:], diskchain.FeatureLazyRefcounts)
	binary.BigEndian.PutUint32(v3[96:], 6)
	binary.BigEndian.PutUint32(v3[60:], 2)

	tests := map[string]struct {
		data    []byte
		want    diskchain.Geometry
		wantErr bool
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
		"raw": {data: []byte("kali"), wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h, err := diskchain.ParseHeader(bytes.NewReader(tt.data))
			if err != nil {
				t.Fatalf("ParseHeader() error = %v", err)
			}

			got, err := diskchain.ParseGeometry(bytes.NewReader(tt.data), h)
			if (err != nil) != tt.wantErr || errors.Is(err, diskchain.ErrBadHeader) != tt.wantErr {
				t.Fatalf("ParseGeometry() error = %v, want an error %t", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseGeometry() mismatch (-want +got):\n%s", diff)
			}
		})
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
