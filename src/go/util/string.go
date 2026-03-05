// Credit: https://stackoverflow.com/a/22892986/451664

package util

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var chars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") //nolint:gochecknoglobals // global constant

func RandomString(n int) string {
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1)) //nolint:gosec // weak random number generator
	b := make([]rune, n)

	for i := range b {
		b[i] = chars[rng.IntN(len(chars))]
	}

	return string(b)
}

// QuoteJoin joins elements from s with sep, quoting any element containing a
// space.
func QuoteJoin(s []string, sep string) string {
	str := make([]string, len(s))

	for i := range s {
		if strings.IndexFunc(s[i], unicode.IsSpace) > -1 {
			str[i] = strconv.Quote(s[i])
		} else {
			str[i] = s[i]
		}
	}

	return strings.Join(str, sep)
}
