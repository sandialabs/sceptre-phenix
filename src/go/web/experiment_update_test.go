package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"phenix/store"
)

// Updating a stopped experiment's VLANs answers 204 and changes only existing aliases;
// unknown experiment 404, bad body 400.
func TestUpdateExperimentVLANs(t *testing.T) {
	tests := map[string]struct {
		name, body string
		want       int
		aliases    string // the stored aliases afterwards, as JSON
	}{
		"known and unknown aliases": {
			name: "stopped", body: `{"EXP_1": 201, "OTHER": 300}`, want: http.StatusNoContent,
			aliases: `{"EXP_1":201,"EXP_2":102}`,
		},
		"unknown experiment": {name: "none", body: `{"EXP_1": 201}`, want: http.StatusNotFound},
		"malformed body":     {name: "stopped", body: `["EXP_1"]`, want: http.StatusBadRequest},
		"running experiment": {name: "running", body: `{"EXP_1": 201}`, want: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stopped := namedExperiment(t, "stopped", "")
			stopped.Spec["vlans"] = map[string]any{"aliases": map[string]any{"EXP_1": 101, "EXP_2": 102}}

			useTestStore(t, stopped, namedExperiment(t, "running", "2024-01-01T00:00:00Z"))

			server := serveAs(t, "alice", testRole("experiments", "patch", tc.name))

			resp := do(t, "PATCH", server.URL+"/api/v1/experiments/"+tc.name, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.want, readBody(t, resp))
			}

			if tc.aliases == "" {
				return
			}

			c, _ := store.NewConfig("experiment/stopped")
			if err := store.Get(c); err != nil {
				t.Fatal(err)
			}

			vlans, _ := c.Spec["vlans"].(map[string]any)
			if got, _ := json.Marshal(vlans["aliases"]); string(got) != tc.aliases {
				t.Errorf("aliases = %s, want %s", got, tc.aliases)
			}
		})
	}
}
