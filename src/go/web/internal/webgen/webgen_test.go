package webgen_test

import (
	"math"
	"strconv"
	"testing"
	"time"

	"pgregory.net/rapid"

	"phenix/web/internal/webgen"
)

// The models agree with the standard library parsers they stand for.
func TestModelsMatchTheStandardLibrary(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		b := webgen.BoolText().Draw(t, "bool")

		want, wantErr := strconv.ParseBool(b)
		if got, ok := webgen.BoolModel(b); ok != (wantErr == nil) || ok && got != want {
			t.Fatalf("BoolModel(%q) = %t, %t; strconv.ParseBool gives %t, %v", b, got, ok, want, wantErr)
		}

		n := webgen.IntText(math.MinInt64, math.MaxInt64).Draw(t, "int")

		wantN, wantErr := strconv.ParseInt(n, 10, 64)
		if got, ok := webgen.IntModel(n, math.MinInt64, math.MaxInt64); ok != (wantErr == nil) || ok && got != wantN {
			t.Fatalf("IntModel(%q) = %d, %t; strconv.ParseInt gives %d, %v", n, got, ok, wantN, wantErr)
		}

		s := webgen.TimeText().Draw(t, "time")

		wantT, wantErr := time.Parse(time.RFC3339, s)
		if got, ok := webgen.TimeModel(s); ok != (wantErr == nil) || ok && !got.Equal(wantT) {
			t.Fatalf("TimeModel(%q) = %v, %t; time.Parse gives %v, %v", s, got, ok, wantT, wantErr)
		}
	})
}

// Query draws each name absent, bare or with values.
func TestQueryDrawsEachForm(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	rapid.Check(t, func(t *rapid.T) {
		q := webgen.Query(webgen.BoolText(), "a").Draw(t, "query")

		switch {
		case !q.Has("a"):
			seen["absent"] = true
		case len(q["a"]) == 1 && q.Get("a") == "":
			seen["empty"] = true
		default:
			seen["values"] = true
		}
	})

	if len(seen) != 3 {
		t.Errorf("Query forms drawn = %v, want absent, empty and values", seen)
	}
}
