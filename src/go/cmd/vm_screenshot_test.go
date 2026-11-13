package cmd

import (
	"errors"
	"strconv"
	"testing"

	"pgregory.net/rapid"
)

// A size of 0 is the full size; 1 to 4096 pass as they are; any other size is refused.
func TestScreenshotSizeProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		size := rapid.IntRange(-10, 5000).Draw(t, "size")

		got, err := screenshotSize(size)

		switch {
		case size == 0:
			if err != nil || got != "" {
				t.Fatalf("screenshotSize(0) = %q, %v, want \"\", nil", got, err)
			}
		case size >= 1 && size <= 4096:
			if err != nil || got != strconv.Itoa(size) {
				t.Fatalf("screenshotSize(%d) = %q, %v, want %q, nil", size, got, err, strconv.Itoa(size))
			}
		default:
			if !errors.Is(err, errBadScreenshotSize) {
				t.Fatalf("screenshotSize(%d) error = %v, want %v", size, err, errBadScreenshotSize)
			}
		}
	})
}
