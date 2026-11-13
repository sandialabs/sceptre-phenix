// Package envsubst replaces ${NAME} and ${NAME:default} placeholders in config
// text. $${NAME} is the escape for the literal text ${NAME}.
package envsubst

import (
	"bytes"
	"regexp"
)

// Submatch index pairs of placeholder: the name, then the default.
const (
	nameStart = 2 + iota
	nameEnd
	defaultStart
	defaultEnd
)

// placeholder matches ${NAME}, ${NAME:default} and the escaped form $${...}.
var placeholder = regexp.MustCompile(`\$?\$\{(\w+)(?::([^}]*))?\}`)

// Lookup returns the value of a variable and whether the variable is set.
type Lookup func(name string) (string, bool)

// Token is one placeholder of a text. Start and End are byte offsets, and Line
// counts from 1. Raw is the text as written. Escaped marks the $${...} form.
type Token struct {
	Start, End, Line int
	Raw              string
	Name, Default    string
	HasDefault       bool
	Escaped          bool
}

// Literal returns the text that an escaped token stands for: Raw without its
// first "$".
func (t Token) Literal() []byte {
	return []byte(t.Raw[1:])
}

// Tokens returns every placeholder of text, in order.
func Tokens(text []byte) []Token {
	matches := placeholder.FindAllSubmatchIndex(text, -1)
	tokens := make([]Token, 0, len(matches))
	line, at := 1, 0

	for _, m := range matches {
		start, end := m[0], m[1]

		line += bytes.Count(text[at:start], []byte("\n"))
		at = start

		tok := Token{
			Start:      start,
			End:        end,
			Line:       line,
			Raw:        string(text[start:end]),
			Name:       string(text[m[nameStart]:m[nameEnd]]),
			Default:    "",
			HasDefault: m[defaultStart] >= 0,
			Escaped:    text[start+1] == '$',
		}

		if tok.HasDefault {
			tok.Default = string(text[m[defaultStart]:m[defaultEnd]])
		}

		tokens = append(tokens, tok)
	}

	return tokens
}

// Replace returns text with each token replaced by the bytes that replace
// returns for it. A token for which replace returns false stays as written.
func Replace(text []byte, replace func(Token) ([]byte, bool)) []byte {
	out := make([]byte, 0, len(text))
	last := 0

	for _, tok := range Tokens(text) {
		with, ok := replace(tok)
		if !ok {
			continue
		}

		out = append(out, text[last:tok.Start]...)
		out = append(out, with...)
		last = tok.End
	}

	return append(out, text[last:]...)
}

// Expand replaces each placeholder with the value from lookup. A variable
// that is not set gives its default, or the empty string. It never fails.
func Expand(text []byte, lookup Lookup) []byte {
	return Replace(text, func(tok Token) ([]byte, bool) {
		return expandToken(tok, lookup), true
	})
}

// ExpandOnly is [Expand] for the one variable name. It keeps every other
// placeholder, and every other escape, as written.
func ExpandOnly(text []byte, name string, lookup Lookup) []byte {
	return Replace(text, func(tok Token) ([]byte, bool) {
		if tok.Name != name {
			return nil, false
		}

		return expandToken(tok, lookup), true
	})
}

func expandToken(tok Token, lookup Lookup) []byte {
	if tok.Escaped {
		return tok.Literal()
	}

	if value, ok := lookup(tok.Name); ok {
		return []byte(value)
	}

	return []byte(tok.Default)
}
