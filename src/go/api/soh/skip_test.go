package soh

import (
	"testing"

	v1 "phenix/types/version/v1"
)

// skipHosts skips a host by its hostname, or by the file name of its first disk when the entry has a qcow2 or qcow
// extension.
func TestSkip(t *testing.T) {
	t.Parallel()

	node := &v1.Node{
		GeneralF:  &v1.General{HostnameF: "host-01"},
		HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: "win/old.qcow"}}},
	}

	tests := map[string]struct {
		toSkip []string
		want   bool
	}{
		"hostname":            {toSkip: []string{"host-01"}, want: true},
		"qcow image":          {toSkip: []string{"old.qcow"}, want: true},
		"other qcow image":    {toSkip: []string{"new.qcow"}, want: false},
		"image with a folder": {toSkip: []string{"win/old.qcow"}, want: false},
		"no match":            {toSkip: []string{"host-02", "old.qc2"}, want: false},
		"none":                {toSkip: nil, want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := skip(node, tt.toSkip); got != tt.want {
				t.Errorf("skip(host-01 with win/old.qcow, %q) = %t, want %t", tt.toSkip, got, tt.want)
			}
		})
	}
}
