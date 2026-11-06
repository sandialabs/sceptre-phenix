package file

import "testing"

// captureFormatOf agrees with the model for any first bytes of a file.
func FuzzCaptureFormatOf(f *testing.F) {
	for b := range captureFormatSeeds {
		f.Add([]byte(b))
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		if got, want := captureFormatOf(b), modelCaptureFormat(b); got != want {
			t.Fatalf("captureFormatOf(%q) = %q, want %q", b, got, want)
		}
	})
}
