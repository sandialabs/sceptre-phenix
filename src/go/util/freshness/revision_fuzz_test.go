package freshness_test

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/freshness"
)

var (
	revisionFuzzTag    = regexp.MustCompile(`^(W/)?"[^"]*"$`)
	revisionFuzzStrong = regexp.MustCompile(`^"r([1-9][0-9]*)"$`)
)

// revisionFuzzModel parses an If-Match value as RFC 9110 describes it: "*", or
// a list of optionally weak quoted tags, of which only strong "r<n>" tags name revision n.
func revisionFuzzModel(value string) ([]uint64, bool, bool) {
	value = strings.TrimSpace(value)
	if value == "*" {
		return nil, true, true
	}

	if value == "" {
		return nil, false, false
	}

	var revs []uint64

	for tag := range strings.SplitSeq(value, ",") {
		tag = strings.TrimSpace(tag)
		if !revisionFuzzTag.MatchString(tag) {
			return nil, false, false
		}

		if m := revisionFuzzStrong.FindStringSubmatch(tag); m != nil {
			if rev, err := strconv.ParseUint(m[1], 10, 64); err == nil {
				revs = append(revs, rev)
			}
		}
	}

	return revs, false, true
}

// ParseIfMatch agrees with revisionFuzzModel on any header value, and MatchRevision
// matches exactly the revisions it names, or every revision for "*".
func FuzzParseIfMatch(f *testing.F) {
	for _, seed := range []string{
		`"r5"`, `W/"r5"`, `*`, " \t* ", ` "r4" , "r5"`, `"r4", W/"r5"`, `"abc"`, `""`, `"r0"`, `"r007"`, `"r+7"`,
		`"r18446744073709551616"`, `"r18446744073709551615"`, `r5`, `"r5`, `"r"5"`, `"`, `W/`, `w/"r5"`,
		`"r5", *`, `"r5",`, ``, "  ", "\"r5\" ",
	} {
		f.Add(seed, uint64(5))
	}

	f.Fuzz(func(t *testing.T, value string, rev uint64) {
		revs, anyTag, err := freshness.ParseIfMatch(value)
		wantRevs, wantAny, ok := revisionFuzzModel(value)

		if ok != (err == nil) || !ok && !errors.Is(err, freshness.ErrBadIfMatch) {
			t.Fatalf("ParseIfMatch(%q) error = %v, want well-formed %t", value, err, ok)
		}

		if diff := cmp.Diff(wantRevs, revs, cmpopts.EquateEmpty()); diff != "" || anyTag != wantAny {
			t.Fatalf("ParseIfMatch(%q) = %v, %t; want %v, %t", value, revs, anyTag, wantRevs, wantAny)
		}

		want := ok && (wantAny || slices.Contains(wantRevs, rev))
		if got := freshness.MatchRevision(value, rev); got != want {
			t.Fatalf("MatchRevision(%q, %d) = %t, want %t", value, rev, got, want)
		}
	})
}
