package cmd

import (
	"strings"
	"testing"
)

// The redactor never panics on any text, keeps every token the text holds, and gives a text that a second redaction
// does not change, so no known value stays at a word boundary.
func FuzzDoctorRedact(f *testing.F) {
	for _, tc := range doctorRedactCases() {
		f.Add(tc.text)
	}

	// one redactor for every input, as one report shares one; the fuzz function runs one input at a time
	r := newDoctorRedactor(doctorRedactBase().input())

	f.Fuzz(func(t *testing.T, text string) {
		got := r.redact(text)

		if again := r.redact(got); again != got {
			t.Errorf("redact(%q) = %q, but redact of that = %q", text, got, again)
		}

		for _, tok := range redactToken.FindAllString(text, -1) {
			if !strings.Contains(got, tok) {
				t.Errorf("redact(%q) = %q, which drops the token %s", text, got, tok)
			}
		}
	})
}
