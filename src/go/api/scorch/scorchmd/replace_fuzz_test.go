package scorchmd

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// applyModel replaces the keys in s in one pass from the left, the longest
// key first where keys start at the same place. Empty keys are not replaced.
func applyModel(s string, resolved ResolvedReplacements) string {
	keys := make([]string, 0, len(resolved))
	for k := range resolved {
		if k != "" {
			keys = append(keys, k)
		}
	}

	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(len(b)-len(a), strings.Compare(a, b)) })

	var out strings.Builder

	for i := 0; i < len(s); {
		at := slices.IndexFunc(keys, func(k string) bool { return strings.HasPrefix(s[i:], k) })
		if at < 0 {
			out.WriteByte(s[i])
			i++

			continue
		}

		fmt.Fprint(&out, resolved[keys[at]])
		i += len(keys[at])
	}

	return out.String()
}

// For any string and two replacement keys, ApplyReplacements gives the key's
// value for a string that is a key, and otherwise replaces each key in one
// pass, as the model does, so the result is the same each time.
func FuzzApplyReplacements(f *testing.F) {
	f.Add("http://{{HOST}}:{{PORT}}/", "{{HOST}}", "web", "{{PORT}}", "8080")
	f.Add("{{A}}", "{{A}}", "{{B}}", "{{B}}", "x")
	f.Add("x{{A}}", "{{A}}", "{{B}}", "{{B}}", "x")
	f.Add("$AB", "$A", "1", "$AB", "2")
	f.Add("aaa", "a", "", "aa", "b")
	f.Add("", "", "empty", "k", "v")
	f.Add("abc", "", "-", "b", "B")

	f.Fuzz(func(t *testing.T, s, k1, v1, k2, v2 string) {
		resolved := ResolvedReplacements{k1: v1, k2: v2}

		want := any(applyModel(s, resolved))
		if v, ok := resolved[s]; ok {
			want = v
		}

		for range 3 {
			got := ApplyReplacements(ComponentMetadata{"x": s}, resolved)["x"]
			if got != want {
				t.Fatalf("ApplyReplacements(%q, %q) = %q, want %q", s, resolved, got, want)
			}
		}
	})
}
