package image

import (
	"errors"
	"strings"
)

// ErrInvalidSize is the error that ValidateSize returns for a disk size that
// is not a positive number followed by M or G.
var ErrInvalidSize = errors.New("give a number followed by M or G, such as 10G")

// ValidateSize returns ErrInvalidSize unless size is a positive number,
// with an optional decimal part, followed by M (megabytes) or G (gigabytes).
// For example, 500M, 10G and 1.5G are correct. vmdb2 gives the size to
// qemu-img, which accepts these forms.
func ValidateSize(size string) error {
	number, ok := strings.CutSuffix(size, "M")
	if !ok {
		number, ok = strings.CutSuffix(size, "G")
	}

	if !ok {
		return ErrInvalidSize
	}

	whole, fraction, decimal := strings.Cut(number, ".")
	if !isDigits(whole) || (decimal && !isDigits(fraction)) || strings.Trim(whole+fraction, "0") == "" {
		return ErrInvalidSize
	}

	return nil
}

// isDigits reports whether s has one or more characters, and all of them are
// the digits 0 to 9.
func isDigits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}
