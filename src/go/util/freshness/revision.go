package freshness

import (
	"errors"
	"slices"
	"strconv"
	"strings"
)

// HeaderRevision carries a config's revision as a plain number.
const HeaderRevision = "X-Phenix-Revision"

// ErrBadIfMatch is returned for an If-Match or If-None-Match value that is
// neither "*" nor a list of entity tags.
var ErrBadIfMatch = errors.New("malformed entity tag list")

// ETag returns the strong entity tag naming a config revision, or "" for
// revision 0 (a config not written since revisions were added).
func ETag(rev uint64) string {
	if rev == 0 {
		return ""
	}

	return `"r` + strconv.FormatUint(rev, 10) + `"`
}

// ParseIfMatch checks an If-Match or If-None-Match value: "*" or a
// comma-separated list of entity tags. It returns the revisions named by
// strong "r<n>" tags; weak tags and tags of another form are valid but name no
// revision.
func ParseIfMatch(value string) ([]uint64, bool, error) {
	value = strings.TrimSpace(value)
	if value == "*" {
		return nil, true, nil
	}

	if value == "" {
		return nil, false, ErrBadIfMatch
	}

	var revs []uint64

	for tag := range strings.SplitSeq(value, ",") {
		tag = strings.TrimSpace(tag)

		weak := strings.HasPrefix(tag, "W/")
		tag = strings.TrimPrefix(tag, "W/")

		if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' || strings.Contains(tag[1:len(tag)-1], `"`) {
			return nil, false, ErrBadIfMatch
		}

		opaque := tag[1 : len(tag)-1]

		if weak || !strings.HasPrefix(opaque, "r") {
			continue
		}

		// only the form ETag writes names a revision, so "r007" is not revision 7
		if rev, err := strconv.ParseUint(opaque[1:], 10, 64); err == nil && rev > 0 && ETag(rev) == tag {
			revs = append(revs, rev)
		}
	}

	return revs, false, nil
}

// MatchRevision reports whether an If-Match value matches a config at rev,
// using the strong comparison: "*" matches any config, and a weak tag never
// matches. A malformed value matches nothing.
func MatchRevision(ifMatch string, rev uint64) bool {
	revs, anyTag, err := ParseIfMatch(ifMatch)
	if err != nil {
		return false
	}

	if anyTag {
		return true
	}

	return slices.Contains(revs, rev)
}
