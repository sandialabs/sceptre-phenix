package rbac

import "unicode/utf8"

// validPattern reports whether all of pattern is valid [path/filepath.Match]
// syntax on Linux and macOS. Match checks only the part of a pattern that it
// reads before the match fails, so a probe name cannot find every bad pattern.
// A bad part that no name reaches, as in "[b-a]*[", still makes it invalid.
func validPattern(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
			if i == len(pattern) {
				return false
			}
		case '[':
			n := classLen(pattern[i+1:])
			if n < 0 {
				return false
			}

			i += n
		}
	}

	return true
}

// classLen returns the length of the character class at the start of s (the
// text after its '['), through the closing ']', or -1 if the class is bad.
func classLen(s string) int {
	i := 0
	if i < len(s) && s[i] == '^' {
		i++
	}

	for items := 0; ; items++ {
		if i < len(s) && s[i] == ']' && items > 0 {
			return i + 1
		}

		n := classChar(s[i:])
		if n < 0 {
			return -1
		}

		i += n

		if i < len(s) && s[i] == '-' {
			n = classChar(s[i+1:])
			if n < 0 {
				return -1
			}

			i += 1 + n
		}
	}
}

// classChar returns the length of the possibly escaped character at the start
// of s, or -1 if it is missing, an unescaped '-' or ']', or invalid UTF-8.
func classChar(s string) int {
	if s == "" || s[0] == '-' || s[0] == ']' {
		return -1
	}

	n := 0
	if s[0] == '\\' {
		n = 1
	}

	r, size := utf8.DecodeRuneInString(s[n:])
	if size == 0 || r == utf8.RuneError && size == 1 {
		return -1
	}

	return n + size
}
