// Credit: https://stackoverflow.com/a/22892986/451664

package util

import "math/rand/v2"

var chars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") //nolint:gochecknoglobals // global constant

func RandomString(n int) string {
	b := make([]rune, n)

	for i := range b {
		b[i] = chars[rand.IntN(len(chars))] //nolint:gosec // G404 names, not secrets
	}

	return string(b)
}
