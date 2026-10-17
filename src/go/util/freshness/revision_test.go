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
	if got := freshness.ETag(0); got != "" {
		t.Errorf("ETag(0) = %q, want none", got)
	}

	if got := freshness.ETag(12); got != `"r12"` {
		t.Errorf(`ETag(12) = %q, want "r12"`, got)
	}
}

func TestMatchRevision(t *testing.T) {
	tests := []struct {
		ifMatch string
		rev     uint64
		match   bool
		bad     bool
	}{
		{ifMatch: `"r5"`, rev: 5, match: true},
		{ifMatch: `"r5"`, rev: 6},
		{ifMatch: `W/"r5"`, rev: 5},
		{ifMatch: `*`, rev: 5, match: true},
		{ifMatch: ` "r4" , "r5"`, rev: 5, match: true},
		{ifMatch: `"r4", W/"r5"`, rev: 5},
		{ifMatch: `"abc"`, rev: 5},
		{ifMatch: `"r0"`, rev: 0},
		{ifMatch: `"r007"`, rev: 7},
		{ifMatch: `r5`, rev: 5, bad: true},
		{ifMatch: `"r5`, rev: 5, bad: true},
		{ifMatch: `"r5", *`, rev: 5, bad: true},
		{ifMatch: ``, rev: 5, bad: true},
	}

	for _, tt := range tests {
		if got := freshness.MatchRevision(tt.ifMatch, tt.rev); got != tt.match {
			t.Errorf("MatchRevision(%q, %d) = %v, want %v", tt.ifMatch, tt.rev, got, tt.match)
		}

		if _, _, err := freshness.ParseIfMatch(tt.ifMatch); (err != nil) != tt.bad {
			t.Errorf("ParseIfMatch(%q) error = %v, want malformed %t", tt.ifMatch, err, tt.bad)
		}
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
