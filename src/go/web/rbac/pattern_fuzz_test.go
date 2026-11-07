package rbac

import (
	"path/filepath"
	"testing"
	"unicode/utf8"
)

// classEnd returns the index of the first unescaped ']' after the '[' at
// pattern[start], or -1 if there is none.
func classEnd(pattern string, start int) int {
	for j := start + 1; j < len(pattern); j++ {
		switch pattern[j] {
		case '\\':
			j++
		case ']':
			return j
		}
	}

	return -1
}

// classMember returns a character that the class pattern matches, if one of a
// set of candidates does.
func classMember(class string) (string, bool) {
	var candidates []rune

	for _, r := range class {
		if r != utf8.RuneError {
			candidates = append(candidates, r)
		}
	}

	for r := range rune(utf8.RuneSelf) {
		candidates = append(candidates, r)
	}

	candidates = append(candidates, 'é', 0xff, 0x800, 0xfffd, 0x1f600, utf8.MaxRune)

	for _, r := range candidates {
		if matched, _ := filepath.Match(class, string(r)); matched {
			return string(r), true
		}
	}

	return "", false
}

// patternWitness walks pattern term by term and checks each character class
// alone with [filepath.Match]. It returns a name that matches each term before
// the first bad one, whether pattern is valid, and whether the name reaches the
// first bad term: false when no candidate matches an earlier class.
func patternWitness(pattern string) (string, bool, bool) {
	var b []byte

	reached := true

	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
		case '?':
			b = append(b, 'a')
		case '\\':
			if i+1 == len(pattern) {
				return string(b), false, reached
			}

			i++
			b = append(b, pattern[i])
		case '[':
			end := classEnd(pattern, i)
			if end < 0 {
				return string(b), false, reached
			}

			class := pattern[i : end+1]
			if _, err := filepath.Match(class, ""); err != nil {
				return string(b), false, reached
			}

			member, ok := classMember(class)
			reached = reached && ok
			b = append(b, member...)
			i = end
		default:
			b = append(b, c)
		}
	}

	return string(b), true, reached
}

// FuzzValidPattern checks validPattern against [filepath.Match]. A pattern
// that [filepath.Match] rejects for any name is not valid, and an invalid
// pattern makes it fail for a name that reaches its first bad term.
func FuzzValidPattern(f *testing.F) {
	for pattern := range patternCases() {
		f.Add(pattern, "useless")
	}

	f.Add("[b-a]*[", "b")
	f.Add(`[\]*]x[a-\`, "]x")
	f.Add("[^]a]", "a")
	f.Add("[a\xffb]", "a")
	f.Add("a\xff[é-ü]?", "a\xffé")

	f.Fuzz(func(t *testing.T, pattern, s string) {
		if len(pattern)+len(s) > fuzzMaxInput {
			t.Skip("input too large")
		}

		got := validPattern(pattern)

		for _, name := range []string{s, "", "useless", pattern} {
			if _, err := filepath.Match(pattern, name); err != nil && got {
				t.Fatalf("validPattern(%q) = true, but filepath.Match(%q, %q) error = %v", pattern, pattern, name, err)
			}
		}

		witness, valid, reached := patternWitness(pattern)
		if got != valid {
			t.Fatalf("validPattern(%q) = %t, want %t", pattern, got, valid)
		}

		if valid || !reached {
			return
		}

		if _, err := filepath.Match(pattern, witness); err == nil {
			t.Fatalf("validPattern(%q) = false, but filepath.Match(%q, %q) error = nil", pattern, pattern, witness)
		}
	})
}
