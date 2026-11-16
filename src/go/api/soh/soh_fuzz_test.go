package soh

import (
	"encoding/json"
	"testing"

	"phenix/util/mm"
)

// For any soh app status, GetFor and GetFlowsFor do not panic. Each host's
// results go to the node of the same name, marked with errors when one of
// its results has an error.
func FuzzGetForStatus(f *testing.F) {
	for _, seed := range []string{
		`{"initialized":true,"hosts":[{"hostname":"web","networking":[{"error":"down"}]},{"hostname":"r"}]}`,
		`{"hosts":[{"hostname":"web","files":[{"success":"ok"}],"customTests":[{"error":"x"}]}]}`,
		`{"hosts":5}`,
		`{"hosts":["web"]}`,
		`{"packetCapture":{"hosts":["a","b"],"flows":[[0,1],[2,0]]}}`,
		`{"packetCapture":{"hosts":"a","flows":[[1.5]]}}`,
		`{"packetCapture":"on"}`,
		`"done"`,
		`null`,
	} {
		f.Add(seed)
	}

	useGetForMM(f,
		mm.VM{Name: "r", State: "RUNNING", Running: true, Networks: []string{"LAN (101)"}},
		mm.VM{Name: "web", State: "RUNNING", Running: true, Networks: []string{"LAN (101)"}},
	)

	f.Fuzz(func(t *testing.T, body string) {
		var status any
		if json.Unmarshal([]byte(body), &status) != nil {
			return
		}

		exp := getForRunning(status)

		_, _, _ = GetFlowsFor(exp)

		network, err := GetFor(t.Context(), exp, "")
		if err != nil {
			return
		}

		for _, node := range network.Nodes {
			if node.SOH == nil {
				continue
			}

			if node.SOH.Hostname != node.Label {
				t.Errorf("GetFor(status %s) node %s has the results of host %s", body, node.Label, node.SOH.Hostname)
			}

			var errs bool
			for _, s := range node.SOH.AllStates() {
				errs = errs || s.Error != ""
			}

			if node.SOH.Errors != errs {
				t.Errorf("GetFor(status %s) node %s errors = %t, want %t", body, node.Label, node.SOH.Errors, errs)
			}
		}
	})
}
