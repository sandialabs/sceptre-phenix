package experiment

import (
	"fmt"
	"slices"
)

// NetflowFormats returns the formats that a recorded netflow capture downloads as.
func NetflowFormats() []string {
	return []string{NetflowFormatText, NetflowFormatCSV, NetflowFormatJSONL}
}

// ValidateNetflowFormat returns ErrInvalidNetflowFormat unless format is one of NetflowFormats.
func ValidateNetflowFormat(format string) error {
	if !slices.Contains(NetflowFormats(), format) {
		return fmt.Errorf("%w, not %q", ErrInvalidNetflowFormat, format)
	}

	return nil
}

// ValidateNetflowCaptureID returns an error wrapping ErrInvalidNetflowCapture unless id has the form of a capture
// ID: the capture's UTC start time, such as 20261028T153012.345Z.
func ValidateNetflowCaptureID(id string) error {
	if !netflowIDPattern.MatchString(id) {
		return fmt.Errorf("%w %q: an ID is a start time such as 20261028T153012.345Z", ErrInvalidNetflowCapture, id)
	}

	return nil
}
