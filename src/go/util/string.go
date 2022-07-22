// Credit: https://stackoverflow.com/a/22892986/451664

package util

import (
	"math/rand"
	"time"
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
