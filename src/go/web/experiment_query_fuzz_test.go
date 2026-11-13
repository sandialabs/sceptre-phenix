package web

import (
	"net/url"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/web/util"
)

// parseVMQuery takes a raw VM list query exactly when its page parses, each
// label is key=value, no host is empty, show_dnb is a boolean and sortCol and
// sortDir are known values; it refuses any other with 400, and keeps each
// filter as the query gives it.
func FuzzParseVMQuery(f *testing.F) {
	for _, seed := range []string{
		"", "pageNum=2&perPage=10", "pageNum=0", "perPage=-1", "pageNum=x", "filter=name:a%20and%20state:RUNNING",
		"label=role=server", "label=role", "label==x", "label=a=b&label=c=d", "host=a&host=b", "host=", "host",
		"sortCol=name&sortDir=asc", "sortCol=cpus&sortDir=desc", "sortCol=name", "sortDir=asc", "show_dnb=true",
		"show_dnb", "show_dnb=yes", "sortCol=bogus&sortDir=asc", "sortDir=up", "pageNum=2147483648", "label=%00=%00",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		q, err := url.ParseQuery(raw)
		if err != nil {
			t.Skip("the router refuses a query that does not parse")
		}

		page, size, pageErr := util.ParsePage(q.Get("pageNum"), q.Get("perPage"))
		showDNB, showOK := boolQueryModel(q, "show_dnb", false)

		want := util.VMQuery{
			Filter: q.Get("filter"), Labels: nil, Hosts: q["host"], ShowDNB: showDNB,
			SortCol: "", SortAsc: q.Get("sortDir") == sortAsc, Page: page, Size: size,
		}
		wantOK := pageErr == nil && showOK && !slices.Contains(q["host"], "")

		for _, col := range q["sortCol"] {
			wantOK = wantOK && slices.Contains(vmSortColumns, col)
		}

		for _, dir := range q["sortDir"] {
			wantOK = wantOK && (dir == sortAsc || dir == sortDesc)
		}

		if q.Get("sortDir") != "" {
			want.SortCol = q.Get("sortCol")
		}

		for _, raw := range q["label"] {
			label, ok := util.ParseVMLabel(raw)
			wantOK = wantOK && ok
			want.Labels = append(want.Labels, label)
		}

		got, err := parseVMQuery(q)
		if (err == nil) != wantOK {
			t.Fatalf("parseVMQuery(%q) error = %v, want ok %t", raw, err, wantOK)
		}

		if err != nil {
			if why := badRequest(err); why != "" {
				t.Fatalf("parseVMQuery(%q) error = %v, want a 400: %s", raw, err, why)
			}

			return
		}

		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("parseVMQuery(%q) mismatch (-want +got):\n%s", raw, diff)
		}
	})
}
