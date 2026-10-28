package soh

import (
	"testing"

	v1 "phenix/types/version/v1"
)

// skipHosts skips a host by its hostname, or by the file name of its first disk when the entry has a qc2, qcow2 or
// qcow extension.
func TestSkip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		image  string // the host's first disk, win/old.qcow if empty
		toSkip []string
		want   bool
	}{
		"hostname":            {toSkip: []string{"host-01"}, want: true},
		"qcow image":          {toSkip: []string{"old.qcow"}, want: true},
		"qcow2 image":         {image: "new.qcow2", toSkip: []string{"new.qcow2"}, want: true},
		"qc2 image":           {image: "/phenix/images/new.qc2", toSkip: []string{"new.qc2"}, want: true},
		"other qcow image":    {toSkip: []string{"new.qcow"}, want: false},
		"image with a folder": {toSkip: []string{"win/old.qcow"}, want: false},
		"image of other kind": {image: "old.img", toSkip: []string{"old.img"}, want: false},
		"later entry":         {toSkip: []string{"host-02", "old.qcow"}, want: true},
		"no match":            {toSkip: []string{"host-02", "old.qc2"}, want: false},
		"empty entry":         {toSkip: []string{""}, want: false},
		"none":                {toSkip: nil, want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			image := tt.image
			if image == "" {
				image = "win/old.qcow"
			}

			node := &v1.Node{
				GeneralF:  &v1.General{HostnameF: "host-01"},
				HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: image}}},
			}

			if got := skip(node, tt.toSkip); got != tt.want {
				t.Errorf("skip(host-01 with %s, %q) = %t, want %t", image, tt.toSkip, got, tt.want)
			}
		})
	}
}
