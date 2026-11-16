package experiment

import (
	"errors"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestValidateNetflowFormat(t *testing.T) {
	t.Parallel()

	for _, format := range NetflowFormats() {
		if err := ValidateNetflowFormat(format); err != nil {
			t.Errorf("ValidateNetflowFormat(%q) error = %v, want none", format, err)
		}
	}

	for _, format := range []string{"", "TEXT", "pcap", "csv "} {
		if err := ValidateNetflowFormat(format); !errors.Is(err, ErrInvalidNetflowFormat) {
			t.Errorf("ValidateNetflowFormat(%q) error = %v, want %v", format, err, ErrInvalidNetflowFormat)
		}
	}
}

// Every capture start time formats as a valid ID, and an ID cut short, with other characters added, is refused.
func TestValidateNetflowCaptureIDProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		started := time.Unix(rapid.Int64Range(0, 25e10).Draw(t, "seconds"), rapid.Int64Range(0, 999).Draw(t, "ms")*1e6)
		id := started.UTC().Format(netflowIDLayout)

		if err := ValidateNetflowCaptureID(id); err != nil {
			t.Fatalf("ValidateNetflowCaptureID(%q) error = %v, want none", id, err)
		}

		// the added characters are never the digits or the Z that an ID ends in
		cut := rapid.IntRange(0, len(id)-1).Draw(t, "cut")
		changed := id[:cut] + rapid.StringMatching(`[./a-z]{0,3}`).Draw(t, "added")

		if err := ValidateNetflowCaptureID(changed); !errors.Is(err, ErrInvalidNetflowCapture) {
			t.Fatalf("ValidateNetflowCaptureID(%q) error = %v, want %v", changed, err, ErrInvalidNetflowCapture)
		}
	})
}
