package disk

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"pgregory.net/rapid"
)

// nameCases are the cases in testdata/minimega_names.json, which the UI's
// minimegaSafe in src/js/src/utils/diskChain.js is tested against too.
type nameCases struct {
	Refused []struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"refused"`
	Paths struct {
		Accepted []struct {
			Path string `json:"path"`
		} `json:"accepted"`
		Refused []struct {
			Path string `json:"path"`
		} `json:"refused"`
	} `json:"paths"`
}

// loadNameCases returns the shared cases and whether they refuse a code point.
func loadNameCases(t *testing.T) (nameCases, func(rune) bool) {
	t.Helper()

	data, err := os.ReadFile("testdata/minimega_names.json")
	if err != nil {
		t.Fatal(err)
	}

	var cases nameCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}

	codePoint := func(s string) rune {
		n, err := strconv.ParseUint(strings.TrimPrefix(s, "U+"), 16, 32)
		if err != nil || !strings.HasPrefix(s, "U+") {
			t.Fatalf("testdata/minimega_names.json: bad code point %q", s)
		}

		return rune(n)
	}

	refused := map[rune]bool{}

	for _, span := range cases.Refused {
		from, to := codePoint(span.From), codePoint(span.From)
		if span.To != "" {
			to = codePoint(span.To)
		}

		for r := from; r <= to; r++ {
			refused[r] = true
		}
	}

	return cases, func(r rune) bool { return refused[r] }
}

// TestMinimegaSafeSharedCodePoints checks that minimegaSafe refuses a path
// holding any code point the shared cases refuse, and accepts any other.
func TestMinimegaSafeSharedCodePoints(t *testing.T) {
	t.Parallel()

	_, refused := loadNameCases(t)

	wrong := 0

	for r := range rune(utf8.MaxRune + 1) {
		if !utf8.ValidRune(r) {
			continue
		}

		p := "/f/a" + string(r) + "b.qc2"
		if got := minimegaSafe(p); got == refused(r) {
			t.Errorf("minimegaSafe(%q) = %v, want %v (U+%04X)", p, got, !got, r)

			if wrong++; wrong == 20 {
				t.Fatal("too many wrong code points")
			}
		}
	}
}

// TestNameCasesASCII checks that the shared cases spell every non-ASCII
// character as a \u escape, so no editor can strip or normalize one unseen.
func TestNameCasesASCII(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/minimega_names.json")
	if err != nil {
		t.Fatal(err)
	}

	line := 1

	for _, r := range string(data) {
		switch {
		case r == '\n':
			line++
		case r > unicode.MaxASCII || r < ' ' && r != '\t' || r == 0x7f:
			t.Errorf("testdata/minimega_names.json:%d holds raw %U, want it written as a \\u escape", line, r)
		}
	}
}

func TestValidateMinimegaPathSharedPaths(t *testing.T) {
	t.Parallel()

	cases, _ := loadNameCases(t)

	for _, c := range cases.Paths.Accepted {
		if err := ValidateMinimegaPath(c.Path); err != nil {
			t.Errorf("ValidateMinimegaPath(%q) = %v, want nil", c.Path, err)
		}
	}

	for _, c := range cases.Paths.Refused {
		if err := ValidateMinimegaPath(c.Path); !errors.Is(err, ErrUnsafeName) {
			t.Errorf("ValidateMinimegaPath(%q) = %v, want %v", c.Path, err, ErrUnsafeName)
		}
	}
}

// TestMinimegaSafeSharedProperty checks that a path is safe exactly when none
// of its code points is one the shared cases refuse.
func TestMinimegaSafeSharedProperty(t *testing.T) {
	t.Parallel()

	_, refused := loadNameCases(t)

	rapid.Check(t, func(t *rapid.T) {
		p := rapid.String().Draw(t, "path")

		if got, want := minimegaSafe(p), !strings.ContainsFunc(p, refused); got != want {
			t.Fatalf("minimegaSafe(%q) = %v, want %v", p, got, want)
		}
	})
}
