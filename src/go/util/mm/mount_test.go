package mm

import (
	"errors"
	"testing"
)

func TestMountFilesystem(t *testing.T) {
	mountErr := errors.New("mount failed")

	tests := []struct {
		name    string
		execErr error
		wantErr error
	}{
		{name: "success"},
		{
			name:    "already mounted",
			execErr: errors.New("mount: already connected"),
		},
		{
			name:    "failure",
			execErr: mountErr,
			wantErr: mountErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mounted *bool
			exec := func(opts ...C2Option) (string, error) {
				mounted = NewC2Options(opts...).mount

				return "", tt.execErr
			}

			err := mountFilesystem(exec, C2NS("exp1"), C2VM("vm1"))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("mountFilesystem() error = %v, want %v", err, tt.wantErr)
			}

			if mounted == nil || !*mounted {
				t.Fatal("mountFilesystem() did not set the C2 mount option")
			}
		})
	}
}

func TestUnmountFilesystem(t *testing.T) {
	unmountErr := errors.New("unmount failed")
	var mounted *bool

	exec := func(opts ...C2Option) (string, error) {
		mounted = NewC2Options(opts...).mount

		return "", unmountErr
	}

	err := unmountFilesystem(exec, C2NS("exp1"), C2VM("vm1"))
	if !errors.Is(err, unmountErr) {
		t.Fatalf("unmountFilesystem() error = %v, want %v", err, unmountErr)
	}

	if mounted == nil || *mounted {
		t.Fatal("unmountFilesystem() did not set the C2 unmount option")
	}
}
