package web

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"phenix/api/vlan"
	"phenix/web/internal/webgen"
	"phenix/web/weberror"
)

// badRequest returns "" when err is a *weberror.WebError with status 400, and
// otherwise why not.
func badRequest(err error) string {
	var werr *weberror.WebError
	if !errors.As(err, &werr) {
		return "not a *weberror.WebError"
	}

	if werr.Status != http.StatusBadRequest {
		return "status " + http.StatusText(werr.Status)
	}

	return ""
}

// boolQueryModel is the value of the boolean query parameter name of q, and
// whether it is valid: def when q does not have it, else every value must be
// one that [strconv.ParseBool] takes, and the first counts.
func boolQueryModel(q url.Values, name string, def bool) (bool, bool) {
	if !q.Has(name) {
		return def, true
	}

	for _, value := range q[name] {
		if _, ok := webgen.BoolModel(value); !ok {
			return false, false
		}
	}

	return webgen.BoolModel(q.Get(name))
}

// Each boolean query parameter takes exactly the spellings [strconv.ParseBool]
// takes, and refuses any other value, also an empty one or a bare name, with a
// 400 invalid_request that names true and false.
func TestBoolQueryParamsProperties(t *testing.T) {
	t.Parallel()

	params := []struct {
		name  string
		def   bool
		parse func(url.Values) (bool, error)
	}{
		{name: "force", parse: parseForce},
		{name: "dryRun", parse: parseDryRun},
		{name: "active", parse: func(q url.Values) (bool, error) { return parseBoolQuery(q, "active") }},
		{name: "vms", def: true, parse: func(q url.Values) (bool, error) { return boolQueryOr(q, "vms", true) }},
	}

	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, p.name)
	}

	rapid.Check(t, func(t *rapid.T) {
		q := webgen.Query(webgen.BoolText(), names...).Draw(t, "query")

		for _, p := range params {
			want, wantOK := boolQueryModel(q, p.name, p.def)

			got, err := p.parse(q)
			if (err == nil) != wantOK || wantOK && got != want {
				t.Fatalf("%s of %q = %t, %v; want %t, ok %t", p.name, q.Encode(), got, err, want, wantOK)
			}

			if err != nil {
				if why := badRequest(err); why != "" || !strings.Contains(err.Error(), "true or false") {
					t.Fatalf("%s of %q error = %v, want a 400 that names true and false: %s", p.name, q.Encode(), err, why)
				}
			}
		}
	})
}

// enumQuery takes exactly the allowed values, also when repeated, and refuses
// any other value, also an empty one or a bare name, with a 400 that lists
// them. parseSort sorts by a column only when sortDir is given.
func TestEnumQueryParamsProperties(t *testing.T) {
	t.Parallel()

	allowed := []string{"name", "date", "a-b"}
	value := rapid.OneOf(rapid.SampledFrom(allowed), rapid.SampledFrom([]string{"", "Name", " name", "asc", "desc", "x"}))

	rapid.Check(t, func(t *rapid.T) {
		q := webgen.Query(value, "e", "sortCol", "sortDir").Draw(t, "query")

		wantOK := true
		for _, v := range q["e"] {
			wantOK = wantOK && slices.Contains(allowed, v)
		}

		want := "def"
		if q.Has("e") {
			want = q.Get("e")
		}

		got, err := enumQuery(q, "e", "def", allowed...)
		if (err == nil) != wantOK || wantOK && got != want {
			t.Fatalf("enumQuery(%q) = %q, %v; want %q, ok %t", q.Encode(), got, err, want, wantOK)
		}

		if err != nil && (badRequest(err) != "" || !strings.Contains(err.Error(), strings.Join(allowed, ", "))) {
			t.Fatalf("enumQuery(%q) error = %v, want a 400 that lists %q", q.Encode(), err, allowed)
		}

		sortOK := true
		for _, v := range q["sortCol"] {
			sortOK = sortOK && slices.Contains(allowed, v)
		}

		for _, v := range q["sortDir"] {
			sortOK = sortOK && (v == "asc" || v == "desc")
		}

		col, asc, err := parseSort(q, allowed)
		if (err == nil) != sortOK {
			t.Fatalf("parseSort(%q) error = %v, want ok %t", q.Encode(), err, sortOK)
		}

		wantCol := ""
		if sortOK && q.Get("sortDir") != "" {
			wantCol = q.Get("sortCol")
		}

		if sortOK && (col != wantCol || asc != (q.Get("sortDir") == "asc")) {
			t.Fatalf("parseSort(%q) = %q, %t; want %q, %t", q.Encode(), col, asc, wantCol, q.Get("sortDir") == "asc")
		}
	})
}

// parseVLANID, qosIndex and queryInt take exactly the decimal whole numbers in
// their range, as [strconv.Atoi] writes them, and refuse any other text with 400.
func TestIntQueryParamsProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		raw := webgen.IntText(vlan.MinID, vlan.MaxID).Draw(t, "vlan")

		want, wantOK := webgen.IntModel(raw, vlan.MinID, vlan.MaxID)
		if got, err := parseVLANID(raw); (err == nil) != wantOK || wantOK && int64(got) != want {
			t.Fatalf("parseVLANID(%q) = %d, %v; want %d, ok %t", raw, got, err, want, wantOK)
		} else if err != nil && badRequest(err) != "" {
			t.Fatalf("parseVLANID(%q) error = %v, want a 400: %s", raw, err, badRequest(err))
		}

		raw = webgen.IntText(0, math.MaxInt64).Draw(t, "index")

		want, wantOK = webgen.IntModel(raw, 0, math.MaxInt64)
		if got, err := qosIndex(raw); (err == nil) != wantOK || wantOK && int64(got) != want || !wantOK && got != -1 {
			t.Fatalf("qosIndex(%q) = %d, %v; want %d, ok %t", raw, got, err, want, wantOK)
		} else if err != nil && badRequest(err) != "" {
			t.Fatalf("qosIndex(%q) error = %v, want a 400: %s", raw, err, badRequest(err))
		}

		lo := rapid.IntRange(-1000, 1000).Draw(t, "lo")
		hi := lo + rapid.IntRange(0, 1000).Draw(t, "span")
		def := rapid.Int().Draw(t, "default")
		q := webgen.Query(webgen.IntText(int64(lo), int64(hi)), "n").Draw(t, "query")

		want, wantOK = int64(def), true
		if s := q.Get("n"); s != "" {
			want, wantOK = webgen.IntModel(s, int64(lo), int64(hi))
		}

		if got, err := queryInt(q, "n", def, lo, hi); (err == nil) != wantOK || wantOK && int64(got) != want {
			t.Fatalf("queryInt(%q, %d, %d–%d) = %d, %v; want %d, ok %t", q.Encode(), def, lo, hi, got, err, want, wantOK)
		} else if err != nil && badRequest(err) != "" {
			t.Fatalf("queryInt(%q) error = %v, want a 400: %s", q.Encode(), err, badRequest(err))
		}
	})
}

// parseConsoleSize takes exactly the decimal whole numbers in 0–65535, without
// a sign, as rows and cols, and refuses any other text with 400.
func TestParseConsoleSizeProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		rows, cols := webgen.IntText(0, math.MaxUint16).Draw(t, "rows"), webgen.IntText(0, math.MaxUint16).Draw(t, "cols")

		wantRows, rowsOK := webgen.IntModel(rows, 0, math.MaxUint16)
		wantCols, colsOK := webgen.IntModel(cols, 0, math.MaxUint16)
		// strconv.ParseUint takes no sign
		ok := rowsOK && colsOK && !strings.ContainsAny(rows+cols, "+-")

		gotRows, gotCols, err := parseConsoleSize(rows, cols)
		if (err == nil) != ok || ok && (int64(gotRows) != wantRows || int64(gotCols) != wantCols) {
			t.Fatalf("parseConsoleSize(%q, %q) = %d, %d, %v; want %d, %d, ok %t",
				rows, cols, gotRows, gotCols, err, wantRows, wantCols, ok)
		}

		if err != nil && badRequest(err) != "" {
			t.Fatalf("parseConsoleSize(%q, %q) error = %v, want a 400: %s", rows, cols, err, badRequest(err))
		}
	})
}
