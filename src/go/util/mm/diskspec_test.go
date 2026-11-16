package mm

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

func TestParseDiskSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		spec    string
		want    diskSpec
		wantErr bool
	}{
		{spec: "/images/a.qc2", want: diskSpec{path: "/images/a.qc2"}},
		{spec: "/images/a.qc2,writeback", want: diskSpec{path: "/images/a.qc2", cache: "writeback"}},
		{spec: "/images/a.qc2,virtio", want: diskSpec{path: "/images/a.qc2", iface: "virtio"}},
		{spec: "/images/a.qc2,ahci", want: diskSpec{path: "/images/a.qc2", iface: "ahci"}},
		{spec: "a.qc2,ide,unsafe", want: diskSpec{path: "a.qc2", iface: "ide", cache: "unsafe"}},
		{spec: "a.qc2,virtio,writeback", want: diskSpec{path: "a.qc2", iface: "virtio", cache: "writeback"}},
		{spec: "a.qc2,writeback,virtio", wantErr: true},
		{spec: "a.qc2,none,none", wantErr: true},
		{spec: "a.qc2,writeback,extra", wantErr: true},
		{spec: "a.qc2,virtio,writeback,none", wantErr: true},
		{spec: "a.qc2,bogus", wantErr: true},
		{spec: "a.qc2,Virtio", wantErr: true},
		{spec: "/images/a.qc2,", wantErr: true},
		{spec: ",virtio", wantErr: true},
		{spec: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			t.Parallel()

			got, err := parseDiskSpec(tt.spec)

			switch {
			case tt.wantErr:
				if !errors.Is(err, ErrMalformedDiskSpec) {
					t.Fatalf("parseDiskSpec(%q) error = %v, want %v", tt.spec, err, ErrMalformedDiskSpec)
				}

				return
			case err != nil:
				t.Fatalf("parseDiskSpec(%q) error = %v", tt.spec, err)
			}

			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(diskSpec{})); diff != "" {
				t.Errorf("parseDiskSpec(%q) mismatch (-want +got):\n%s", tt.spec, diff)
			}

			if s := got.String(); s != tt.spec {
				t.Errorf("parseDiskSpec(%q).String() = %q, want %q", tt.spec, s, tt.spec)
			}
		})
	}
}

func TestParseDiskSpecs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		disks   string
		want    []diskSpec
		wantErr bool
	}{
		{name: "none", disks: "", want: []diskSpec{}},
		{name: "one", disks: "/images/a.qc2,virtio", want: []diskSpec{{path: "/images/a.qc2", iface: "virtio"}}},
		{
			name:  "several",
			disks: "/images/a_snapshot,virtio,writeback  /images/b.qc2 /images/c.qc2,none",
			want: []diskSpec{
				{path: "/images/a_snapshot", iface: "virtio", cache: "writeback"},
				{path: "/images/b.qc2"},
				{path: "/images/c.qc2", cache: "none"},
			},
		},
		{name: "a malformed second disk", disks: "/images/a.qc2 /images/b.qc2,bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseDiskSpecs(tt.disks)

			switch {
			case tt.wantErr:
				if !errors.Is(err, ErrMalformedDiskSpec) {
					t.Fatalf("parseDiskSpecs(%q) error = %v, want %v", tt.disks, err, ErrMalformedDiskSpec)
				}

				return
			case err != nil:
				t.Fatalf("parseDiskSpecs(%q) error = %v", tt.disks, err)
			}

			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(diskSpec{})); diff != "" {
				t.Errorf("parseDiskSpecs(%q) mismatch (-want +got):\n%s", tt.disks, diff)
			}
		})
	}
}

func TestPlanRedeployDisks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		disks   string
		disk    string
		injects bool
		want    redeployDisks
		wantErr bool
	}{
		{
			name: "replaces only the first disk", disks: "/images/a.qc2,virtio /images/b.qc2,ide,none",
			disk: "/images/new.qc2",
			want: redeployDisks{specs: []diskSpec{
				{path: "/images/new.qc2", iface: "virtio"},
				{path: "/images/b.qc2", iface: "ide", cache: "none"},
			}},
		},
		{
			name: "keeps the interface and cache mode without injects", disks: "/images/a.qc2,virtio,writeback",
			disk: "/images/new.qc2",
			want: redeployDisks{specs: []diskSpec{{path: "/images/new.qc2", iface: "virtio", cache: "writeback"}}},
		},
		{
			name: "keeps the interface and cache mode without a snapshot", disks: "/images/a.qc2,virtio,writeback",
			disk: "/images/new.qc2", injects: true,
			want: redeployDisks{specs: []diskSpec{{path: "/images/new.qc2", iface: "virtio", cache: "writeback"}}},
		},
		{
			name: "takes the new interface and cache mode without a snapshot", disks: "/images/a.qc2,ide,none",
			disk: "/images/new.qc2,virtio,unsafe",
			want: redeployDisks{specs: []diskSpec{{path: "/images/new.qc2", iface: "virtio", cache: "unsafe"}}},
		},
		{
			name: "keeps the interface of a snapshot", disks: "/images/exp_a_snapshot,virtio,writeback",
			disk: "/images/new.qc2,none", injects: true,
			want: redeployDisks{
				snapshot: "exp_a_snapshot", source: "/images/new.qc2",
				specs: []diskSpec{{path: "/images/exp_a_snapshot", iface: "virtio", cache: "none"}},
			},
		},
		{
			name: "keeps the cache mode of a snapshot", disks: "/images/exp_a_snapshot,virtio,writeback /images/b.qc2",
			disk: "/images/new.qc2", injects: true,
			want: redeployDisks{
				snapshot: "exp_a_snapshot", source: "/images/new.qc2",
				specs: []diskSpec{{path: "/images/exp_a_snapshot", iface: "virtio", cache: "writeback"}, {path: "/images/b.qc2"}},
			},
		},
		{
			name: "takes the new interface for a snapshot", disks: "/images/exp_a_snapshot,ide",
			disk: "/images/new.qc2,virtio,unsafe", injects: true,
			want: redeployDisks{
				snapshot: "exp_a_snapshot", source: "/images/new.qc2",
				specs: []diskSpec{{path: "/images/exp_a_snapshot", iface: "virtio", cache: "unsafe"}},
			},
		},
		{
			name: "makes no snapshot for a second snapshot disk", disks: "/images/a.qc2 /images/exp_b_snapshot",
			disk: "/images/new.qc2", injects: true,
			want: redeployDisks{specs: []diskSpec{{path: "/images/new.qc2"}, {path: "/images/exp_b_snapshot"}}},
		},
		{
			name: "uses the new disk for a VM with no disks", disks: "", disk: "/images/new.qc2", injects: true,
			want: redeployDisks{specs: []diskSpec{{path: "/images/new.qc2"}}},
		},
		{name: "a malformed new disk", disks: "/images/a.qc2", disk: "/images/new.qc2,bogus", wantErr: true},
		{name: "a malformed current disk", disks: "/images/a.qc2,,", disk: "/images/new.qc2", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := planRedeployDisks(tt.disks, tt.disk, tt.injects)

			switch {
			case tt.wantErr:
				if !errors.Is(err, ErrMalformedDiskSpec) {
					t.Fatalf("planRedeployDisks(%q, %q) error = %v, want %v", tt.disks, tt.disk, err, ErrMalformedDiskSpec)
				}

				return
			case err != nil:
				t.Fatalf("planRedeployDisks(%q, %q) error = %v", tt.disks, tt.disk, err)
			}

			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(redeployDisks{}, diskSpec{})); diff != "" {
				t.Errorf("planRedeployDisks(%q, %q) mismatch (-want +got):\n%s", tt.disks, tt.disk, diff)
			}
		})
	}
}

// genDiskSpecs draws a diskSpec with a path that has no comma or space, and
// that is a snapshot at times.
func genDiskSpecs() *rapid.Generator[diskSpec] {
	return rapid.Custom(func(t *rapid.T) diskSpec {
		return diskSpec{
			path: rapid.StringMatching(`[a-z0-9./-]{1,12}`).Draw(t, "path") +
				rapid.SampledFrom([]string{"", "_snapshot"}).Draw(t, "snapshot"),
			iface: rapid.SampledFrom([]string{"", "ahci", "ide", "scsi", "sd", "mtd", "floppy", "pflash", "virtio"}).Draw(t, "iface"),
			cache: rapid.SampledFrom([]string{"", "none", "writeback", "unsafe", "directsync", "writethrough"}).Draw(t, "cache"),
		}
	})
}

// A list of disk specs reads back as the same list.
func TestDiskSpecsRoundTrip(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		want := rapid.SliceOf(genDiskSpecs()).Draw(t, "specs")

		got, err := parseDiskSpecs(joinDiskSpecs(want))
		if err != nil {
			t.Fatalf("parseDiskSpecs(%q) error = %v", joinDiskSpecs(want), err)
		}

		if diff := cmp.Diff(want, got, cmp.AllowUnexported(diskSpec{})); diff != "" {
			t.Fatalf("parseDiskSpecs(%q) mismatch (-want +got):\n%s", joinDiskSpecs(want), diff)
		}
	})
}

// A redeploy changes only the first disk, and keeps the others as they are.
// The first disk keeps the old interface and cache mode that the new spec
// does not set.
func TestPlanRedeployDisksKeepsTheOtherDisks(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		old := rapid.SliceOfN(genDiskSpecs(), 1, 4).Draw(t, "old")
		disk := genDiskSpecs().Draw(t, "disk")
		injects := rapid.Bool().Draw(t, "injects")

		got, err := planRedeployDisks(joinDiskSpecs(old), disk.String(), injects)
		if err != nil {
			t.Fatalf("planRedeployDisks error = %v", err)
		}

		if diff := cmp.Diff(old[1:], got.specs[1:], cmp.AllowUnexported(diskSpec{})); diff != "" {
			t.Fatalf("planRedeployDisks other disks mismatch (-want +got):\n%s", diff)
		}

		if snapshot := injects && strings.Contains(filepath.Base(old[0].path), "_snapshot"); snapshot != (got.snapshot != "") {
			t.Fatalf("planRedeployDisks snapshot = %q, want a snapshot %t", got.snapshot, snapshot)
		}

		wantFirst := diskSpec{path: disk.path, iface: disk.iface, cache: disk.cache}
		if wantFirst.iface == "" {
			wantFirst.iface = old[0].iface
		}

		if wantFirst.cache == "" {
			wantFirst.cache = old[0].cache
		}

		if got.snapshot != "" {
			wantFirst.path = old[0].path
		}

		if diff := cmp.Diff(wantFirst, got.specs[0], cmp.AllowUnexported(diskSpec{})); diff != "" {
			t.Fatalf("planRedeployDisks first disk mismatch (-want +got):\n%s", diff)
		}
	})
}
