package web

import (
	"net/http"
	"testing"

	"phenix/store"
)

// Updating a stopped experiment's VLAN assignments answers 204 and changes
// only the aliases it already has. An experiment that does not exist is a
// 404, and a body that is not a map of aliases to IDs a 400.
func TestUpdateExperimentVLANs(t *testing.T) {
	tests := map[string]struct {
		name, body string
		want       int
		aliases    map[string]any // the stored aliases afterwards
	}{
		"known and unknown aliases": {
			name: "stopped", body: `{"EXP_1": 201, "OTHER": 300}`, want: http.StatusNoContent,
			aliases: map[string]any{"EXP_1": 201, "EXP_2": 102},
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

			if tc.aliases == nil {
				return
			}

			c, _ := store.NewConfig("experiment/stopped")
			if err := store.Get(c); err != nil {
				t.Fatal(err)
			}

			vlans, _ := c.Spec["vlans"].(map[string]any)
			if got := vlans["aliases"]; !sameAliases(got, tc.aliases) {
				t.Errorf("aliases = %v, want %v", got, tc.aliases)
			}
		})
	}
}

// sameAliases reports whether stored VLAN aliases, of whatever map type the
// store left them in, are want.
func sameAliases(got any, want map[string]any) bool {
	var aliases map[string]int

	switch g := got.(type) {
	case map[string]int:
		aliases = g
	case map[string]any:
		aliases = make(map[string]int, len(g))

		for k, v := range g {
			n, ok := v.(int)
			if !ok {
				return false
			}

			aliases[k] = n
		}
	default:
		return false
	}

	if len(aliases) != len(want) {
		return false
	}

	for k, v := range want {
		if aliases[k] != v {
			return false
		}
	}

	return true
}
