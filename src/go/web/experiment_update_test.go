package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
)

// Updating a stopped experiment's VLANs answers 204 and changes only existing aliases;
// unknown experiment 404, bad body 400.
func TestUpdateExperimentVLANs(t *testing.T) {
	tests := map[string]struct {
		name, body string
		want       int
		aliases    map[string]int // the stored aliases afterwards
	}{
		"known and unknown aliases": {
			name: "stopped", body: `{"EXP_1": 201, "OTHER": 300}`, want: http.StatusNoContent,
			aliases: map[string]int{"EXP_1": 201, "EXP_2": 102},
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
			path := "/api/v1/experiments/" + tc.name

			resp := do(t, http.MethodPatch, server.URL+path, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("PATCH %s %s = %d, want %d: %s", path, tc.body, resp.StatusCode, tc.want, readBody(t, resp))
			}

			if tc.aliases == nil {
				return
			}

			c, _ := store.NewConfig("experiment/stopped")
			if err := store.Get(c); err != nil {
				t.Fatalf("store.Get(experiment/stopped) error = %v", err)
			}

			// the stored numbers' Go type is the handler's choice, so compare them as JSON decodes them
			vlans, _ := c.Spec["vlans"].(map[string]any)

			raw, err := json.Marshal(vlans["aliases"])
			if err != nil {
				t.Fatalf("json.Marshal(stored aliases) error = %v", err)
			}

			var got map[string]int
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", raw, err)
			}

			if diff := cmp.Diff(tc.aliases, got); diff != "" {
				t.Errorf("PATCH %s %s stored aliases mismatch (-want +got):\n%s", path, tc.body, diff)
			}
		})
	}
}
