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

// Credit: https://github.com/go-yaml/yaml/issues/100#issuecomment-901604971
//
// Allow yaml parsing for an array of strings or a single inlined element:
//
//	field: value
//
//	  or
//
//	field:
//	  - value1
//	  - value2
type StringArray []string

func (a *StringArray) UnmarshalYAML(value *yaml.Node) error {
	var multi []string
	err := value.Decode(&multi)
	if err != nil {
		var single string
		err := value.Decode(&single)
		if err != nil {
			return err
		}
		*a = []string{single}
	} else {
		*a = multi
	}
	return nil
}
