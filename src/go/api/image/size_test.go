package image_test

import (
	"errors"
	"testing"

	"phenix/api/image"
)

// ValidateSize accepts a positive number, with an optional decimal part,
// followed by M or G. It returns ErrInvalidSize for all other sizes.
func TestValidateSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size string
		want error
	}{
		{"10G", nil},
		{"500M", nil},
		{"1.5G", nil},
		{"0.5G", nil},
		{"1G", nil},
		{"010G", nil},
		{"2048M", nil},
		{"100.25M", nil},
		{"", image.ErrInvalidSize},
		{"M", image.ErrInvalidSize},
		{"G", image.ErrInvalidSize},
		{".G", image.ErrInvalidSize},
		{"10", image.ErrInvalidSize},
		{"10X", image.ErrInvalidSize},
		{"10T", image.ErrInvalidSize},
		{"10GB", image.ErrInvalidSize},
		{"10g", image.ErrInvalidSize},
		{"10m", image.ErrInvalidSize},
		{"-5G", image.ErrInvalidSize},
		{"+5G", image.ErrInvalidSize},
		{"0G", image.ErrInvalidSize},
		{"00M", image.ErrInvalidSize},
		{"0.0G", image.ErrInvalidSize},
		{"1.5.2G", image.ErrInvalidSize},
		{".5G", image.ErrInvalidSize},
		{"5.G", image.ErrInvalidSize},
		{"1,5G", image.ErrInvalidSize},
		{"1e3G", image.ErrInvalidSize},
		{"0x10G", image.ErrInvalidSize},
		{" 10G", image.ErrInvalidSize},
		{"10G ", image.ErrInvalidSize},
		{"1 0G", image.ErrInvalidSize},
		{"10G\n  - shell: id", image.ErrInvalidSize},
		{"١٠G", image.ErrInvalidSize},
		{"10MG", image.ErrInvalidSize},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			t.Parallel()

			if got := image.ValidateSize(tt.size); !errors.Is(got, tt.want) {
				t.Errorf("ValidateSize(%q) = %v, want %v", tt.size, got, tt.want)
			}
		})
	}
}
