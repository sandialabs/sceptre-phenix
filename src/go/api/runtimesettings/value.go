package runtimesettings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

// ErrSettingType is the error for text that does not parse as the type of
// the setting it changes.
var ErrSettingType = errors.New("value does not have the type of the setting")

// Flatten adds each value of the nested settings src to dst, at its dotted
// key after prefix.
func Flatten(prefix string, src map[string]any, dst map[string]any) {
	for k, v := range src {
		newKey := k
		if prefix != "" {
			newKey = prefix + "." + k
		}

		if child, ok := v.(map[string]any); ok {
			Flatten(newKey, child, dst)
		} else {
			dst[newKey] = v
		}
	}
}

// InferType returns the value of text for a key that no flag declares: true
// or false as a boolean, a whole number as an int, a number with a decimal
// point as a float64, and other text as it is.
func InferType(s string) any {
	if strings.ToLower(s) == "true" {
		return true
	}

	if strings.ToLower(s) == "false" {
		return false
	}

	if i, err := strconv.Atoi(s); err == nil {
		return i
	}

	if f, err := strconv.ParseFloat(s, 64); err == nil && strings.Contains(s, ".") {
		return f
	}

	return s
}

// ParseValue returns the value to write for text at key, whose flag is f, or
// nil for a key that no flag declares. It parses text as f does, and keeps a
// duration as its text. For a key without a flag, it uses InferType. Text that
// f refuses returns an error that wraps ErrSettingType.
func ParseValue(f *pflag.Flag, key, text string) (any, error) {
	if f == nil {
		return InferType(text), nil
	}

	typ := f.Value.Type()
	flags := pflag.NewFlagSet(key, pflag.ContinueOnError)

	var want string

	switch typ {
	case "string":
		return text, nil
	case "bool":
		flags.Bool(key, false, "")
		want = "true or false"
	case "int":
		flags.Int(key, 0, "")
		want = "an integer, such as 100"
	case "duration":
		flags.Duration(key, 0, "")
		want = "a duration, such as 90s or 24h"
	case "stringSlice":
		flags.StringSlice(key, nil, "")
		want = "a comma-separated list"
	default:
		return nil, fmt.Errorf(
			"%w: phenix cannot write the %s setting %s, so edit the config file by hand",
			ErrSettingType, typ, key,
		)
	}

	if err := flags.Set(key, text); err != nil {
		return nil, fmt.Errorf("%w: %s is a %s setting and takes %s, not %q", ErrSettingType, key, typ, want, text)
	}

	switch typ {
	case "bool":
		return flags.GetBool(key) //nolint:wrapcheck // the flag was set above
	case "int":
		return flags.GetInt(key) //nolint:wrapcheck // the flag was set above
	case "stringSlice":
		return flags.GetStringSlice(key) //nolint:wrapcheck // the flag was set above
	}

	return text, nil
}
