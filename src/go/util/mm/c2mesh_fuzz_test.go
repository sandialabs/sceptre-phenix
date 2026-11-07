package mm

import (
	"strings"
	"testing"
)

// FuzzFilterTargets checks filterTargets against minimega's rendering of a
// filter: terms joined by " && ". A filter names the UUID only if one of its
// terms is uuid=<uuid>, ignoring case.
func FuzzFilterTargets(f *testing.F) {
	f.Add("hostname=web\x00uuid=u1", "u1")
	f.Add("uuid=U1\x00os=linux", "u1")
	f.Add("uuid=u10", "u1")
	f.Add("", "")
	f.Add("name=uuid=u1x", "u1")

	f.Fuzz(func(t *testing.T, terms, uuid string) {
		var (
			parts = make([]string, 0, strings.Count(terms, "\x00")+1)
			want  bool
		)

		for term := range strings.SplitSeq(terms, "\x00") {
			term = strings.TrimSpace(strings.ReplaceAll(term, "&&", ""))
			parts = append(parts, term)
			want = want || strings.EqualFold(term, "uuid="+uuid)
		}

		filter := strings.Join(parts, " && ")

		if got := filterTargets(filter, uuid); got != want {
			t.Fatalf("filterTargets(%q, %q) = %t, want %t", filter, uuid, got, want)
		}

		if filterTargets(terms, uuid) && !strings.Contains(strings.ToLower(terms), strings.ToLower("uuid="+uuid)) {
			t.Fatalf("filterTargets(%q, %q) = true, but the filter does not hold uuid=%s", terms, uuid, uuid)
		}
	})
}
