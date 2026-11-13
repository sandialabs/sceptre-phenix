package v1

import "testing"

func TestHardwareDiskConfig(t *testing.T) {
	tests := []struct {
		name     string
		drives   []*Drive
		snapshot string
		want     string
	}{
		{
			name:   "single drive without snapshot",
			drives: []*Drive{{ImageF: "a.qc2"}},
			want:   "a.qc2",
		},
		{
			name:     "single drive with snapshot",
			drives:   []*Drive{{ImageF: "a.qc2"}},
			snapshot: "snap",
			want:     "snap,writeback",
		},
		{
			name: "multiple drives without snapshot",
			drives: []*Drive{
				{ImageF: "a.qc2"},
				{ImageF: "b.qc2", IfaceF: "virtio", CacheModeF: "none"},
			},
			want: "a.qc2 b.qc2,virtio,none",
		},
		{
			name: "every drive uses its own snapshot",
			drives: []*Drive{
				{ImageF: "a.qc2"},
				{ImageF: "b.qc2", IfaceF: "virtio"},
				{ImageF: "c.qc2", CacheModeF: "none"},
			},
			snapshot: "snap",
			want:     "snap,writeback snap_1,virtio,writeback snap_2,none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := Hardware{DrivesF: tt.drives}

			if got := h.DiskConfig(tt.snapshot); got != tt.want {
				t.Errorf("DiskConfig(%q) = %q, want %q", tt.snapshot, got, tt.want)
			}
		})
	}
}

func TestDriveSnapshotName(t *testing.T) {
	tests := []struct {
		drive int
		want  string
	}{
		{drive: 0, want: "snap"},
		{drive: 1, want: "snap_1"},
		{drive: 2, want: "snap_2"},
	}

	for _, tt := range tests {
		if got := DriveSnapshotName("snap", tt.drive); got != tt.want {
			t.Errorf("DriveSnapshotName(%q, %d) = %q, want %q", "snap", tt.drive, got, tt.want)
		}
	}
}
