package freshness_test

import (
	"testing"

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
			t.Errorf("ParseIfMatch(%q) err = %v, want malformed %v", tt.ifMatch, err, tt.bad)
		}
	}
}
