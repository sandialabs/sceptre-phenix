package app_test

import (
	"strings"
	"testing"

	"phenix/app"
)

// FuzzParseAction checks ParseAction against the model of the stage names and
// shorthands: text that trims and lower-cases to one parses to its stage, and
// any other text is refused with an error that quotes it and lists the stages.
func FuzzParseAction(f *testing.F) {
	names := actionNames()

	for name := range names {
		f.Add(name)
		f.Add(strings.ToUpper(name))
		f.Add(" " + name + "\t")
	}

	for _, seed := range []string{"", "all", "start", "pre start", "Ｒun"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		want, known := names[strings.ToLower(strings.TrimSpace(value))]
		if !known {
			if err := parseActionRefusal(value); err != nil {
				t.Fatal(err)
			}

			return
		}

		if got, err := app.ParseAction(value); err != nil || got != want {
			t.Fatalf("ParseAction(%q) = %q, %v; want %q, nil", value, got, err, want)
		}
	})
}
