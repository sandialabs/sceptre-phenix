package mm

import "testing"

func TestDiskConfigString(t *testing.T) {
	tests := []struct {
		name  string
		spec  string
		iface string
		cache string
		want  string
	}{
		{name: "path only", spec: "snap", want: "snap"},
		{name: "keeps cache", spec: "snap,writeback", want: "snap,writeback"},
		{name: "keeps interface and cache", spec: "snap,virtio,writeback", want: "snap,virtio,writeback"},
		{name: "keeps interface without cache", spec: "snap,virtio", want: "snap,virtio"},
		{name: "overrides cache", spec: "snap,virtio,writeback", cache: "none", want: "snap,virtio,none"},
		{name: "overrides interface", spec: "snap,ide,writeback", iface: "virtio", want: "snap,virtio,writeback"},
		{name: "adds interface", spec: "snap,writeback", iface: "virtio", want: "snap,virtio,writeback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newDiskConfig(tt.spec).string(tt.iface, tt.cache); got != tt.want {
				t.Errorf("newDiskConfig(%q).string(%q, %q) = %q, want %q", tt.spec, tt.iface, tt.cache, got, tt.want)
			}
		})
	}
}

func TestMergeRedeployDisks(t *testing.T) {
	tests := []struct {
		name      string
		current   []string
		requested []string
		want      string
	}{
		{
			name:      "single disk VM",
			current:   []string{"os_snapshot,writeback"},
			requested: []string{"new.qc2"},
			want:      "new.qc2",
		},
		{
			name:      "single requested disk keeps additional disks",
			current:   []string{"os_snapshot,writeback", "os_snapshot_1,virtio,writeback"},
			requested: []string{"new.qc2"},
			want:      "new.qc2 os_snapshot_1,virtio,writeback",
		},
		{
			name:      "multiple requested disks replace all disks",
			current:   []string{"os_snapshot,writeback", "os_snapshot_1,writeback"},
			requested: []string{"new.qc2", "data.qc2,virtio"},
			want:      "new.qc2 data.qc2,virtio",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeRedeployDisks(tt.current, tt.requested); got != tt.want {
				t.Errorf("mergeRedeployDisks(%q, %q) = %q, want %q", tt.current, tt.requested, got, tt.want)
			}
		})
	}
}
