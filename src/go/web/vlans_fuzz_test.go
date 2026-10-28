package web

import (
	"net/url"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/vlan"
	"phenix/web/internal/webgen"
)

// vlanQueryModel is the query of GET /vlans that parseVLANQuery reads from
// q, and whether it takes q, from the models of webgen: state and issues are
// strict, so an empty or bare one is refused.
func vlanQueryModel(q url.Values) (vlan.Query, bool) {
	want := vlan.Query{Experiment: q.Get("experiment"), VLAN: 0, State: q.Get("state"), IssuesOnly: false}
	ok := true

	if raw := q.Get("vlan"); raw != "" {
		id, idOK := webgen.IntModel(raw, vlan.MinID, vlan.MaxID)
		want.VLAN, ok = int(id), idOK
	}

	for _, state := range q["state"] {
		ok = ok && slices.Contains([]string{vlan.StatePinned, vlan.StateAllocated, vlan.StateBlacklisted}, state)
	}

	issues, issuesOK := boolQueryModel(q, "issues", false)
	want.IssuesOnly, ok = issues, ok && issuesOK

	return want, ok
}

// parseVLANQuery takes a raw GET /vlans query exactly when vlanQueryModel
// does, gives the same filters, and refuses any other with 400.
func FuzzParseVLANQuery(f *testing.F) {
	for _, seed := range []string{
		"", "vlan=1", "vlan=4094", "vlan=0", "vlan=4095", "vlan=-1", "vlan=%2B7", "vlan=007", "vlan=1.5", "vlan=x",
		"state=pinned", "state=allocated", "state=blacklisted", "state=Pinned", "state=", "issues=true",
		"issues=1", "issues=yes", "issues", "experiment=lab&vlan=10&state=pinned&issues=false",
		"vlan=1&vlan=x", "vlan=%00", "experiment=%ZZ",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		q, err := url.ParseQuery(raw)
		if err != nil {
			t.Skip("the router refuses a query that does not parse")
		}

		want, wantOK := vlanQueryModel(q)

		got, err := parseVLANQuery(q)
		if (err == nil) != wantOK {
			t.Fatalf("parseVLANQuery(%q) error = %v, want ok %t", raw, err, wantOK)
		}

		if err != nil {
			if why := badRequest(err); why != "" {
				t.Fatalf("parseVLANQuery(%q) error = %v, want a 400: %s", raw, err, why)
			}

			return
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("parseVLANQuery(%q) mismatch (-want +got):\n%s", raw, diff)
		}
	})
}
