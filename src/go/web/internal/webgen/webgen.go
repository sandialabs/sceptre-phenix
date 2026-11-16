// Package webgen holds rapid generators of the request input that the web API
// tests send, such as query values, numbers, booleans and times, and models of
// how the standard library parses them. Only tests use it.
package webgen

import (
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"pgregory.net/rapid"
)

const (
	decimal = 10
	// maxJunk is the most characters of a near miss that a generator draws.
	maxJunk = 8
	// boundDelta is how far from a bound IntText draws a number near it.
	boundDelta = 2
	// maxValues is the most values that Query draws for a name.
	maxValues = 3
	// maxOffset is the largest time zone offset, in minutes, that RFC 3339 writes.
	maxOffset = 23*60 + 59
	// minUnix and maxUnix are the seconds of 0001-01-01 and 9999-12-31T23:59:59 UTC.
	minUnix = -62135596800
	maxUnix = 253402300799
)

// boolSpellings are the texts that [strconv.ParseBool] takes, the true ones
// first.
var boolSpellings = []string{ //nolint:gochecknoglobals // constant table
	"1", "t", "T", "TRUE", "true", "True", "0", "f", "F", "FALSE", "false", "False",
}

// BoolModel is the value that [strconv.ParseBool] gives for s, and false for a
// text it refuses.
func BoolModel(s string) (bool, bool) {
	for i, spelling := range boolSpellings {
		if s == spelling {
			return i < len(boolSpellings)/2, true
		}
	}

	return false, false
}

// BoolText draws the text of a boolean parameter: a spelling that
// [strconv.ParseBool] takes, or a near miss such as yes, tRUE or " true".
func BoolText() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom(boolSpellings),
		rapid.SampledFrom([]string{"", "yes", "no", "on", "off", "tRUE", "fAlse", " true", "true ", "2", "-1", "00", "t\x00"}),
		rapid.StringOfN(rapid.SampledFrom([]rune("01tfTFrueals \t")), 0, maxJunk, -1),
	)
}

// wholeNumber is the syntax of [strconv.Atoi] and [strconv.ParseInt] in base 10.
var wholeNumber = regexp.MustCompile(`^[+-]?[0-9]+$`)

// IntModel is the number that [strconv.Atoi] gives for s, and whether it is a
// whole number in lo–hi.
func IntModel(s string, lo, hi int64) (int64, bool) {
	if !wholeNumber.MatchString(s) {
		return 0, false
	}

	n, ok := new(big.Int).SetString(s, decimal)
	if !ok || n.Cmp(big.NewInt(lo)) < 0 || n.Cmp(big.NewInt(hi)) > 0 {
		return 0, false
	}

	return n.Int64(), true
}

// IntText draws the text of a whole-number parameter: decimal numbers of any
// size, with a sign or leading zeros, numbers near the bounds lo and hi, and
// near misses such as 1.5, 0x10, " 1" or digits that are not ASCII.
func IntText(lo, hi int64) *rapid.Generator[string] {
	near := rapid.Custom(func(t *rapid.T) string {
		base := rapid.SampledFrom([]int64{lo, hi, 0}).Draw(t, "bound")
		delta := rapid.Int64Range(-boundDelta, boundDelta).Draw(t, "delta")

		return new(big.Int).Add(big.NewInt(base), big.NewInt(delta)).String()
	})

	return rapid.OneOf(
		near,
		rapid.Map(rapid.Int64(), func(n int64) string { return strconv.FormatInt(n, decimal) }),
		rapid.StringMatching(`[+-]?0{0,3}[0-9]{1,25}`),
		rapid.SampledFrom([]string{"", " 1", "1 ", "1.5", "1e3", "0x10", "1_000", "١", "+", "-", "--1", "+-1"}),
		rapid.StringOfN(rapid.SampledFrom([]rune("0123456789+-. xe")), 0, maxJunk, -1),
	)
}

// TimeModel is the time that [time.Parse] gives for s in the RFC 3339 layout,
// and whether it takes s.
func TimeModel(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)

	return t, err == nil
}

// TimeText draws the text of an RFC 3339 time parameter: times with any offset
// and fraction, and near misses such as a date alone, a space for the T or an
// hour of 24.
func TimeText() *rapid.Generator[string] {
	valid := rapid.Custom(func(t *rapid.T) string {
		sec := rapid.Int64Range(minUnix, maxUnix).Draw(t, "unix")
		nsec := rapid.Int64Range(0, int64(time.Second-1)).Draw(t, "nanos")
		offset := time.Duration(rapid.IntRange(-maxOffset, maxOffset).Draw(t, "offset")) * time.Minute
		layout := rapid.SampledFrom([]string{time.RFC3339, time.RFC3339Nano}).Draw(t, "layout")

		return time.Unix(sec, nsec).In(time.FixedZone("", int(offset/time.Second))).Format(layout)
	})

	mangled := rapid.Custom(func(t *rapid.T) string {
		s := valid.Draw(t, "time")
		i := rapid.IntRange(0, len(s)-1).Draw(t, "at")

		return rapid.SampledFrom([]string{
			s[:i],
			s[:i] + s[i+1:],
			s[:i] + rapid.SampledFrom([]string{" ", "T", "Z", "9", ":", "+"}).Draw(t, "with") + s[i+1:],
		}).Draw(t, "how")
	})

	return rapid.OneOf(valid, mangled, rapid.SampledFrom([]string{
		"", "2024-01-01", "2024-01-01 00:00:00Z", "2024-01-01T24:00:00Z", "2024-02-30T00:00:00Z",
		"2024-01-01T00:00:00", "2024-01-01T00:00:00+2400", "now", "0",
	}))
}

// Query draws a query in which each name is absent, empty (?name=) or holds
// one or more values that value draws.
func Query(value *rapid.Generator[string], names ...string) *rapid.Generator[url.Values] {
	return rapid.Custom(func(t *rapid.T) url.Values {
		q := url.Values{}

		for _, name := range names {
			switch rapid.SampledFrom([]string{"absent", "empty", "values"}).Draw(t, name+" form") {
			case "empty":
				q[name] = []string{""}
			case "values":
				q[name] = rapid.SliceOfN(value, 1, maxValues).Draw(t, name)
			}
		}

		return q
	})
}
