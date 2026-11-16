package web

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/vlan"
	"phenix/store"
	"phenix/util/mm/mmtest"
	"phenix/web/rbac"
)

// vlanTestExperiment is the stored config of a stopped experiment with the
// VLAN pins and range.
func vlanTestExperiment(t *testing.T, name string, pins map[string]int, lo, hi int) store.Config {
	t.Helper()

	c := namedExperiment(t, name, "")
	c.Spec["vlans"] = map[string]any{"aliases": pins, "min": lo, "max": hi}

	return c
}

// useVLANCluster stores the stopped experiments alpha (X 102, Y unpinned, in
// 100–199) and beta (Y 102), and a minimega with the unowned namespace
// old-demo on 400 and a blacklist of 999, until the test ends.
func useVLANCluster(t *testing.T) {
	t.Helper()

	useTestStore(t,
		vlanTestExperiment(t, "alpha", map[string]int{"X": 102, "Y": 0}, 100, 199),
		vlanTestExperiment(t, "beta", map[string]int{"Y": 102}, 0, 0),
	)

	vlan.Forget()
	t.Cleanup(vlan.Forget)

	useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case "namespace":
			return []*minicli.Response{
				mmtest.Tabular("head", []string{"namespace", "vlans", "active"}, []string{"old-demo", "", "false"}),
			}
		case "vlans":
			if cmd.Namespace == "old-demo" {
				return []*minicli.Response{mmtest.Tabular("head", []string{"alias", "vlan"}, []string{"P", "400"})}
			}
		case "vlans blacklist":
			return []*minicli.Response{mmtest.Tabular("head", []string{"vlan"}, []string{"999"})}
		}

		return nil
	})
}

// getJSON requests path from server as GET, wants status, and decodes the
// body into v.
func getJSON(t *testing.T, method, url, body string, status int, v any) {
	t.Helper()

	resp := do(t, method, url, body)

	raw := readBody(t, resp)
	if resp.StatusCode != status {
		t.Fatalf("%s %s = %d, want %d: %s", method, url, resp.StatusCode, status, raw)
	}

	if err := json.Unmarshal([]byte(raw), v); err != nil {
		t.Fatalf("%s %s body is not JSON: %v", method, url, err)
	}
}

// GET /vlans shows a role the rows of the experiments it may list VLANs of,
// names the others "another experiment", and only with "*" the blacklist and
// unowned namespaces.
func TestVLANsVisibility(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	type row struct {
		ID         int    `json:"id"`
		Experiment string `json:"experiment"`
		Namespace  string `json:"namespace"`
	}

	type body struct {
		VLANs  []row `json:"vlans"`
		Issues []struct {
			Experiments []string `json:"experiments"`
			Message     string   `json:"message"`
		} `json:"issues"`
		Blacklist *[]int `json:"blacklist"`
	}

	for name, tc := range map[string]struct {
		role          rbac.Role
		wantRows      []row
		wantMessages  []string
		wantBlacklist *[]int
	}{
		"alpha only": {
			role:         testRole("vlans", "list", "alpha"),
			wantRows:     []row{{ID: 102, Experiment: "alpha"}},
			wantMessages: []string{"Also pinned by another experiment."},
		},
		"every name": {
			role: testRole("vlans", "list", "*"),
			wantRows: []row{
				{ID: 102, Experiment: "alpha"}, {ID: 102, Experiment: "beta"},
				{ID: 400, Namespace: "old-demo"}, {ID: 999},
			},
			wantMessages: []string{
				"Also pinned by beta (Y).", "Also pinned by alpha (X).", "No experiment has the namespace old-demo.",
			},
			wantBlacklist: &[]int{999},
		},
		"every name but beta": {
			role:     testRole("vlans", "list", "*", "!beta"),
			wantRows: []row{{ID: 102, Experiment: "alpha"}, {ID: 400, Namespace: "old-demo"}, {ID: 999}},
			wantMessages: []string{
				"Also pinned by another experiment.", "No experiment has the namespace old-demo.",
			},
			wantBlacklist: &[]int{999},
		},
	} {
		t.Run(name, func(t *testing.T) {
			useVLANCluster(t)
			server := serveAs(t, "alice", tc.role).URL

			var got body
			getJSON(t, http.MethodGet, server+"/api/v1/vlans", "", http.StatusOK, &got)

			if diff := cmp.Diff(tc.wantRows, got.VLANs); diff != "" {
				t.Errorf("GET /vlans rows mismatch (-want +got):\n%s", diff)
			}

			messages := make([]string, 0, len(got.Issues))
			for _, i := range got.Issues {
				messages = append(messages, i.Message)
			}

			if diff := cmp.Diff(tc.wantMessages, messages); diff != "" {
				t.Errorf("GET /vlans issue messages mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantBlacklist, got.Blacklist); diff != "" {
				t.Errorf("GET /vlans blacklist mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// GET /vlans?format=csv answers the filtered rows, and the aliases with no ID,
// as a CSV attachment.
func TestVLANsCSV(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	useVLANCluster(t)
	server := serveAs(t, "alice", testRole("vlans", "list", "*")).URL

	resp := do(t, http.MethodGet, server+"/api/v1/vlans?format=csv&experiment=alpha", "")
	raw := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /vlans?format=csv = %d, want 200: %s", resp.StatusCode, raw)
	}

	if got := resp.Header.Get("Content-Disposition"); got != "attachment; filename=vlans.csv" {
		t.Errorf("Content-Disposition = %q, want attachment; filename=vlans.csv", got)
	}

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", got)
	}

	records, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("reading the CSV: %v", err)
	}

	want := [][]string{
		{"id", "alias", "experiment", "namespace", "running", "states", "phenix", "minimega", "vms", "hosts", "issues"},
		{"102", "X", "alpha", "", "false", "pinned", "102", "", "0", "", "duplicate-pin"},
		{"", "Y", "alpha", "", "false", "", "", "", "0", "", ""},
	}

	if diff := cmp.Diff(want, records); diff != "" {
		t.Errorf("CSV mismatch (-want +got):\n%s", diff)
	}

	// a filter on the state, ID or issues leaves out the aliases with no ID
	resp = do(t, http.MethodGet, server+"/api/v1/vlans?format=csv&experiment=alpha&state=pinned", "")
	raw = readBody(t, resp)

	if records, err = csv.NewReader(strings.NewReader(raw)).ReadAll(); err != nil {
		t.Fatalf("reading the CSV: %v", err)
	}

	if diff := cmp.Diff(want[:2], records); diff != "" {
		t.Errorf("CSV with ?state=pinned mismatch (-want +got):\n%s", diff)
	}
}

// A pin that another experiment holds is a 409 with its issues unless
// forced; a forced pin and an unpin answer the experiment's VLANs.
func TestExperimentVLANPins(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	useVLANCluster(t)
	server := serveAs(t, "alice", combinedRole(
		testRole("experiments", verbPatch, "alpha"), testRole("vlans", "list", "alpha"),
	)).URL

	alias := server + "/api/v1/experiments/alpha/vlans/aliases/Y"

	var conflict struct {
		Code   string       `json:"code"`
		Issues []vlan.Issue `json:"issues"`
	}

	getJSON(t, http.MethodPut, alias, `{"id":102}`, http.StatusConflict, &conflict)

	if len(conflict.Issues) != 1 || conflict.Issues[0].Kind != vlan.KindDuplicatePin || conflict.Code != "conflict" {
		t.Errorf("PUT alias Y = 102 conflict = %+v, want one duplicate-pin issue and the code conflict", conflict)
	}

	if diff := cmp.Diff([]string{"alpha", vlan.HiddenExperiment}, conflict.Issues[0].Experiments); diff != "" {
		t.Errorf("PUT alias Y = 102 conflict experiments mismatch (-want +got):\n%s", diff)
	}

	type summary struct {
		Experiment vlan.ExperimentSummary `json:"experiment"`
	}

	var forced summary
	getJSON(t, http.MethodPut, alias+"?force=true", `{"id":102}`, http.StatusOK, &forced)

	wantAliases := []vlan.AliasSummary{{Alias: "X", Pinned: 102}, {Alias: "Y", Pinned: 102}}
	if diff := cmp.Diff(wantAliases, forced.Experiment.Aliases); diff != "" {
		t.Errorf("forced PUT alias Y = 102 aliases mismatch (-want +got):\n%s", diff)
	}

	var unpinned summary
	getJSON(t, http.MethodDelete, alias, "", http.StatusOK, &unpinned)

	wantAliases[1].Pinned = 0
	if diff := cmp.Diff(wantAliases, unpinned.Experiment.Aliases); diff != "" {
		t.Errorf("DELETE alias Y aliases mismatch (-want +got):\n%s", diff)
	}

	var ranged summary
	getJSON(t, http.MethodPut, server+"/api/v1/experiments/alpha/vlans/range", `{"min":101,"max":150}`,
		http.StatusOK, &ranged)

	if ranged.Experiment.Min != 101 || ranged.Experiment.Max != 150 {
		t.Errorf("PUT range = %d–%d, want 101–150", ranged.Experiment.Min, ranged.Experiment.Max)
	}

	// another experiment's pins need experiments patch on it
	if resp := do(t, http.MethodPut, server+"/api/v1/experiments/beta/vlans/aliases/Y", `{"id":120}`); resp.StatusCode !=
		http.StatusForbidden {
		t.Errorf("PUT beta's alias = %d, want 403", resp.StatusCode)
	}
}

// A pin while minimega does not answer is a 409 unless forced.
func TestPinVLANWhileMinimegaDown(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	useTestStore(t, vlanTestExperiment(t, "alpha", map[string]int{"X": 102, "Y": 0}, 100, 199))
	vlan.Forget()
	t.Cleanup(vlan.Forget)
	useMinimega(t, func(mmtest.Command) []*minicli.Response {
		return []*minicli.Response{mmtest.Error("head", "minimega is down")}
	})

	alias := serveAs(t, "alice", combinedRole(
		testRole("experiments", verbPatch, "alpha"), testRole("vlans", "list", "alpha"),
	)).URL + "/api/v1/experiments/alpha/vlans/aliases/Y"

	var refused struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	getJSON(t, http.MethodPut, alias, `{"id":120}`, http.StatusConflict, &refused)

	if refused.Code != "conflict" || !strings.Contains(refused.Message, "minimega did not answer") {
		t.Errorf("PUT alias Y = 120 while minimega is down = %+v, want the code conflict and why", refused)
	}

	if resp := do(t, http.MethodPut, alias+"?force=true", `{"id":120}`); resp.StatusCode != http.StatusOK {
		t.Errorf("forced PUT alias Y = 120 while minimega is down = %d, want 200", resp.StatusCode)
	}
}

// GET /vlans/free skips every used ID, and GET /vlans/{id} lists one ID's
// rows as the role may get them.
func TestVLANsFreeAndOne(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	useVLANCluster(t)
	server := serveAs(t, "alice", combinedRole(testRole("vlans", "list", "*"), testRole("vlans", "get", "beta"))).URL

	var block struct {
		Start int `json:"start"`
		End   int `json:"end"`
	}

	getJSON(t, http.MethodGet, server+"/api/v1/vlans/free?min=101&count=2", "", http.StatusOK, &block)

	if block.Start != 103 || block.End != 104 {
		t.Errorf("GET /vlans/free = %d–%d, want 103–104", block.Start, block.End)
	}

	var one struct {
		ID    int `json:"id"`
		VLANs []struct {
			Experiment string `json:"experiment"`
		} `json:"vlans"`
	}

	getJSON(t, http.MethodGet, server+"/api/v1/vlans/102", "", http.StatusOK, &one)

	if one.ID != 102 || len(one.VLANs) != 1 || one.VLANs[0].Experiment != "beta" {
		t.Errorf("GET /vlans/102 = %+v, want only beta's row", one)
	}
}

// A change to the blacklist and a release of an unowned namespace's IDs are
// audited: refused, failed and done.
func TestVLANChangesAreAudited(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	useVLANCluster(t)

	var (
		events = captureAudit(t)
		alice  = serveAs(t, "alice", combinedRole(
			testRole("vlans/blacklist", "create", "*"), testRole("vlans/blacklist", "delete", "*"),
			testRole("vlans/allocations", "delete", "*"),
		)).URL + "/api/v1/vlans"
		bob = serveAs(t, "bob", testRole("vlans", "list", "*")).URL + "/api/v1/vlans"
	)

	for _, tc := range []struct {
		method, url string
		want        int
	}{
		{http.MethodPut, alice + "/blacklist/500", http.StatusNoContent},
		{http.MethodDelete, alice + "/blacklist/999", http.StatusNotImplemented},
		{http.MethodDelete, alice + "/allocations/old-demo", http.StatusNoContent},
		{http.MethodPut, bob + "/blacklist/500", http.StatusForbidden},
		{http.MethodDelete, bob + "/blacklist/999", http.StatusForbidden},
		{http.MethodDelete, bob + "/allocations/old-demo", http.StatusForbidden},
	} {
		if resp := do(t, tc.method, tc.url, ""); resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.url, resp.StatusCode, tc.want)
		}
	}

	got := events()

	wantAudit(t, got, "vlan-blacklist-add", "success", map[string]any{
		"user.name": "alice", "phenix.vlan.id": 500, "event.category": "configuration", "event.type": "[creation]",
	})
	wantAudit(t, got, "vlan-blacklist-remove", "failure", map[string]any{"user.name": "alice", "phenix.vlan.id": 999})
	wantAudit(t, got, "vlan-release", "success", map[string]any{
		"user.name": "alice", "phenix.vlan.namespace": "old-demo", "event.type": "[deletion]",
	})

	for _, action := range []string{"vlan-blacklist-add", "vlan-blacklist-remove", "vlan-release"} {
		wantAudit(t, got, action, "failure", map[string]any{"user.name": "bob", "event.reason": "forbidden"})
	}
}
