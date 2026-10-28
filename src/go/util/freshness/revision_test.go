package freshness_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/freshness"
)

func TestETag(t *testing.T) {
	t.Parallel()

	for rev, want := range map[uint64]string{0: "", 1: `"r1"`, 12: `"r12"`, 1<<64 - 1: `"r18446744073709551615"`} {
		if got := freshness.ETag(rev); got != want {
			t.Errorf("ETag(%d) = %q, want %q", rev, got, want)
		}
	}
}

// ParseIfMatch returns the revisions of an If-Match list's strong "r<n>" tags,
// in order, or that the value is "*"; MatchRevision matches only those.
func TestMatchRevision(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		ifMatch string
		rev     uint64
		revs    []uint64
		anyTag  bool
		match   bool
		bad     bool
	}{
		"strong tag":              {ifMatch: `"r5"`, rev: 5, revs: []uint64{5}, match: true},
		"another revision":        {ifMatch: `"r5"`, rev: 6, revs: []uint64{5}},
		"weak tag":                {ifMatch: `W/"r5"`, rev: 5},
		"any":                     {ifMatch: `*`, rev: 5, anyTag: true, match: true},
		"any, padded":             {ifMatch: " \t* ", rev: 0, anyTag: true, match: true},
		"list with spaces":        {ifMatch: ` "r4" , "r5"`, rev: 5, revs: []uint64{4, 5}, match: true},
		"list, first named":       {ifMatch: `"r4","r5"`, rev: 4, revs: []uint64{4, 5}, match: true},
		"strong and weak":         {ifMatch: `"r4", W/"r5"`, rev: 5, revs: []uint64{4}},
		"other opaque tag":        {ifMatch: `"abc"`, rev: 5},
		"empty opaque tag":        {ifMatch: `""`, rev: 0},
		"revision 0":              {ifMatch: `"r0"`, rev: 0},
		"zero-padded":             {ifMatch: `"r007"`, rev: 7},
		"signed":                  {ifMatch: `"r+7"`, rev: 7},
		"r alone":                 {ifMatch: `"r"`, rev: 0},
		"past uint64":             {ifMatch: `"r18446744073709551616"`, rev: 0},
		"largest revision":        {ifMatch: `"r18446744073709551615"`, rev: 1<<64 - 1, revs: []uint64{1<<64 - 1}, match: true},
		"repeated":                {ifMatch: `"r3", "r3"`, rev: 3, revs: []uint64{3, 3}, match: true},
		"unquoted":                {ifMatch: `r5`, rev: 5, bad: true},
		"unterminated":            {ifMatch: `"r5`, rev: 5, bad: true},
		"quote inside":            {ifMatch: `"r"5"`, rev: 5, bad: true},
		"one quote":               {ifMatch: `"`, rev: 0, bad: true},
		"weak prefix alone":       {ifMatch: `W/`, rev: 0, bad: true},
		"lower-case weak prefix":  {ifMatch: `w/"r5"`, rev: 5, bad: true},
		"any in a list":           {ifMatch: `"r5", *`, rev: 5, bad: true},
		"empty element":           {ifMatch: `"r5",`, rev: 5, bad: true},
		"empty":                   {ifMatch: ``, rev: 5, bad: true},
		"blank":                   {ifMatch: "  ", rev: 5, bad: true},
		"malformed after a match": {ifMatch: `"r5", x`, rev: 5, bad: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			revs, anyTag, err := freshness.ParseIfMatch(tc.ifMatch)
			if tc.bad != errors.Is(err, freshness.ErrBadIfMatch) || !tc.bad && err != nil {
				t.Errorf("ParseIfMatch(%q) error = %v, want malformed %t", tc.ifMatch, err, tc.bad)
			}

			if diff := cmp.Diff(tc.revs, revs, cmpopts.EquateEmpty()); diff != "" || anyTag != tc.anyTag {
				t.Errorf("ParseIfMatch(%q) = %v, %t; want %v, %t", tc.ifMatch, revs, anyTag, tc.revs, tc.anyTag)
			}

			if got := freshness.MatchRevision(tc.ifMatch, tc.rev); got != tc.match {
				t.Errorf("MatchRevision(%q, %d) = %v, want %v", tc.ifMatch, tc.rev, got, tc.match)
			}
		})
	}
}

// revisionTag is one entity tag of an If-Match list and the revision it names (0 for none).
type revisionTag struct {
	text string
	rev  uint64
}

// genRevisionTag draws a well-formed entity tag: a strong revision tag as ETag writes it,
// or one that names no revision (weak, revision 0, a zero-padded number, or another opaque value).
func genRevisionTag() *rapid.Generator[revisionTag] {
	return rapid.Custom(func(t *rapid.T) revisionTag {
		rev := rapid.OneOf(rapid.Uint64Range(1, 4), rapid.Uint64Min(1)).Draw(t, "rev")

		return rapid.SampledFrom([]revisionTag{
			{text: freshness.ETag(rev), rev: rev},
			{text: "W/" + freshness.ETag(rev)},
			{text: `"r0"`},
			{text: `"r0` + strconv.FormatUint(rev, 10) + `"`},
			{text: `"rx"`},
			{text: `"abc"`},
			{text: `""`},
		}).Draw(t, "tag")
	})
}

// genRevisionSep draws a list separator with optional whitespace around the comma.
func genRevisionSep() *rapid.Generator[string] {
	return rapid.StringMatching(`[ \t]{0,2},[ \t]{0,2}`)
}

// A well-formed list names exactly its strong revision tags, in order, and matches only
// those revisions; one malformed element makes the whole list malformed.
func TestParseIfMatchProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		tags := rapid.SliceOfN(genRevisionTag(), 1, 5).Draw(t, "tags")

		var (
			value strings.Builder
			want  []uint64
		)

		for i, tag := range tags {
			if i > 0 {
				value.WriteString(genRevisionSep().Draw(t, "sep"))
			}

			value.WriteString(tag.text)

			if tag.rev > 0 {
				want = append(want, tag.rev)
			}
		}

		ifMatch := rapid.StringMatching(`[ \t]{0,2}`).Draw(t, "pad") + value.String()

		revs, anyTag, err := freshness.ParseIfMatch(ifMatch)
		if err != nil || anyTag {
			t.Fatalf("ParseIfMatch(%q) = _, %t, %v; want a tag list", ifMatch, anyTag, err)
		}

		if diff := cmp.Diff(want, revs, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("ParseIfMatch(%q) revisions mismatch (-want +got):\n%s", ifMatch, diff)
		}

		rev := rapid.Uint64Range(0, 5).Draw(t, "rev")
		if len(want) > 0 && rapid.Bool().Draw(t, "named") {
			rev = rapid.SampledFrom(want).Draw(t, "named rev")
		}

		if got := freshness.MatchRevision(ifMatch, rev); got != slices.Contains(want, rev) {
			t.Fatalf("MatchRevision(%q, %d) = %t, want %t", ifMatch, rev, got, !got)
		}

		bad := rapid.SampledFrom([]string{"", "r1", `"r1`, `r1"`, `"r"1"`, "*", "W/r1"}).Draw(t, "bad")
		at := rapid.IntRange(0, len(tags)).Draw(t, "at")
		parts := slices.Insert(slices.Clone(tags), at, revisionTag{text: bad})

		texts := make([]string, len(parts))
		for i, part := range parts {
			texts[i] = part.text
		}

		malformed := strings.Join(texts, ", ")
		if _, _, err := freshness.ParseIfMatch(malformed); !errors.Is(err, freshness.ErrBadIfMatch) {
			t.Fatalf("ParseIfMatch(%q) error = %v, want ErrBadIfMatch", malformed, err)
		}
	})
}
