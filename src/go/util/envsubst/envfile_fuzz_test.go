package envsubst_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/envsubst"
)

// An env file that ParseEnvFile accepts gives valid names, values with no
// line break and rising line numbers, and reads back the same after it is
// written again. Any other file gives a *ParseError with a line of the file.
func FuzzParseEnvFile(f *testing.F) {
	for _, seed := range envfileCases {
		f.Add(seed.text)
	}

	for _, seed := range envfileBadLines {
		f.Add(seed.text)
	}

	f.Fuzz(func(t *testing.T, text string) {
		entries, err := envsubst.ParseEnvFile(strings.NewReader(text))
		lines := strings.Count(text, "\n") + 1

		if err != nil {
			var parseErr *envsubst.ParseError
			if !errors.As(err, &parseErr) || parseErr.Line < 1 || parseErr.Line > lines || parseErr.Reason == "" {
				t.Fatalf("ParseEnvFile(%q) error = %v, want a *ParseError for a line from 1 to %d", text, err, lines)
			}

			return
		}

		last := 0

		for _, entry := range entries {
			if !envsubst.ValidName(entry.Name) || strings.ContainsAny(entry.Value, "\r\n") {
				t.Fatalf("ParseEnvFile(%q) gave the entry %+v, want a valid name and no line break", text, entry)
			}

			if entry.Line <= last || entry.Line > lines {
				t.Fatalf("ParseEnvFile(%q) gave line %d after line %d, want rising lines up to %d", text, entry.Line, last, lines)
			}

			last = entry.Line
		}

		again, err := envsubst.ParseEnvFile(strings.NewReader(envfileFormat(entries)))
		if err != nil {
			t.Fatalf("ParseEnvFile of the written entries %+v error = %v", entries, err)
		}

		ignoreLine := cmpopts.IgnoreFields(envsubst.Entry{}, "Line")
		if diff := cmp.Diff(entries, again, ignoreLine, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("ParseEnvFile(%q) round trip mismatch (-first +second):\n%s", text, diff)
		}
	})
}
