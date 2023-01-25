// Credit: https://stackoverflow.com/a/22892986/451664

package util

import (
	"math/rand"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var chars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func RandomString(n int) string {
	rand.Seed(time.Now().UnixNano())
	b := make([]rune, n)

	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
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
