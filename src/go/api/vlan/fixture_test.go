package vlan

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// frontendFixture is the GET /vlans body that the web UI's unit and browser
// tests use (vlansFixture.js reads it). It must match [sampleClusterReport].
var frontendFixture = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "js", "test", "helpers", "vlansFixture.json",
)

var updateFrontendFixture = flag.Bool( //nolint:gochecknoglobals // test flag
	"update-frontend-fixture",
	false,
	"rewrite the web UI's VLANs test fixture from the Merge of the sample cluster",
)

// sampleVMs are n VMs of prefix on alias, on host with the live ID vlan (0
// and no host for a stopped experiment's topology).
func sampleVMs(prefix, alias string, n int, host string, vlan int) []VMInterface {
	vms := make([]VMInterface, 0, n)

	for i := range n {
		vms = append(vms, VMInterface{
			VM: fmt.Sprintf("%s-%d", prefix, i+1), Host: host, Interface: "0", Alias: alias, VLAN: vlan,
		})
	}

	return vms
}

// sampleCluster is the cluster of the VLANs page design: two running
// experiments, three stopped ones, a namespace that no experiment has and a
// blacklist. It has each kind of issue.
func sampleCluster() Inputs {
	substation := make([]VMInterface, 0, 12+8+21+6)
	redTeam := make([]VMInterface, 0, 3)
	ics := make([]VMInterface, 0, 4)

	for _, a := range []struct {
		alias string
		n, id int
	}{{"MGMT", 12, 101}, {"SCADA", 8, 102}, {"FIELD", 21, 103}, {"CORP", 6, 110}} {
		substation = append(substation, sampleVMs("sub-"+a.alias, a.alias, a.n, "compute1", a.id)...)
	}

	for i, alias := range []string{"RT_C2", "RT_TARGET", "TAP_TEMP"} {
		redTeam = append(redTeam, sampleVMs("rt-"+alias, alias, 1, "compute2", []int{301, 302, 330}[i])...)
	}

	for _, alias := range []string{"ENG", "OPS", "DMZ", "LOG"} {
		ics = append(ics, sampleVMs("ics-"+alias, alias, 1, "", 0)...)
	}

	live := up([]int{100, 999},
		ns("substation-a", map[string]int{"MGMT": 101, "SCADA": 102, "FIELD": 103, "CORP": 110}, substation...),
		ns("red-team-2", map[string]int{"RT_C2": 301, "RT_TARGET": 302, "TAP_TEMP": 330}, redTeam...),
		ns("old-demo", map[string]int{"LAN": 400, "WAN": 401, "DMZ": 402, "OOB": 403}),
	)
	live.Read = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	live.BlacklistRemoval = true

	return Inputs{
		Experiments: []ExperimentInput{
			exp("substation-a", true, 100, 199, map[string]int{"MGMT": 101, "SCADA": 102, "FIELD": 103, "CORP": 104}),
			exp("red-team-2", true, 300, 399, map[string]int{"RT_C2": 301, "RT_TARGET": 302}),
			exp("ics-training", false, 150, 249, map[string]int{"ENG": 150, "OPS": 151, "DMZ": 152, "LOG": 250}, ics...),
			exp("scratch", false, 0, 0, map[string]int{"EXP": 102}, sampleVMs("scratch", "EXP", 3, "", 0)...),
			exp("lab", false, 0, 0, map[string]int{"MGMT": 100}, sampleVMs("lab", "MGMT", 1, "", 0)...),
		},
		Minimega: live,
	}
}

// sampleClusterReport is the Merge of the sample cluster for a viewer who
// sees all, as the indented JSON that GET /vlans sends.
func sampleClusterReport(t *testing.T) []byte {
	t.Helper()

	body, err := json.MarshalIndent(Merge(sampleCluster(), seeAll), "", "  ")
	if err != nil {
		t.Fatalf("marshaling the sample cluster report: %v", err)
	}

	return append(body, '\n')
}

// The web UI's VLANs fixture is what Merge gives for the sample cluster, so
// the UI tests cannot drift from the server. To write it again, run
// go test ./api/vlan -run TestFrontendFixtureMatchesMerge -update-frontend-fixture.
func TestFrontendFixtureMatchesMerge(t *testing.T) {
	t.Parallel()

	want := sampleClusterReport(t)

	if *updateFrontendFixture {
		if err := os.WriteFile(frontendFixture, want, 0o600); err != nil {
			t.Fatalf("writing %s: %v", frontendFixture, err)
		}
	}

	file, err := os.ReadFile(frontendFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", frontendFixture, err)
	}

	var got, wantData any
	if err := json.Unmarshal(file, &got); err != nil {
		t.Fatalf("decoding %s: %v", frontendFixture, err)
	}

	if err := json.Unmarshal(want, &wantData); err != nil {
		t.Fatalf("decoding the sample cluster report: %v", err)
	}

	if diff := cmp.Diff(wantData, got); diff != "" {
		t.Errorf("%s is out of date; run this test with -update-frontend-fixture (-want +got):\n%s",
			frontendFixture, diff)
	}
}
