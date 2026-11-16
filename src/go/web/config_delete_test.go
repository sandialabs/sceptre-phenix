package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/config"
	"phenix/store"
	"phenix/web/rbac"
)

// storeDeleteConfigs stores topologies alpha and beta, and the experiment
// running, which runs. A stopped experiment is left out: its delete hook asks
// minimega to delete its files.
func storeDeleteConfigs(t *testing.T) {
	t.Helper()

	useTestStore(t,
		store.Config{Version: "phenix.sandia.gov/v1", Kind: "Topology", Metadata: store.ConfigMetadata{Name: "alpha"}},
		store.Config{Version: "phenix.sandia.gov/v1", Kind: "Topology", Metadata: store.ConfigMetadata{Name: "beta"}},
		namedExperiment(t, "running", "2024-01-01T00:00:00Z"),
	)
}

// configsDeleteBody is the body of POST /configs/delete.
type configsDeleteBody struct {
	Deleted []string              `json:"deleted"`
	Failed  []configDeleteFailure `json:"failed"`
}

// POST /configs/delete deletes the configs of the kind it names that the role
// may delete, and lists those it could not delete.
func TestDeleteConfigs(t *testing.T) {
	var (
		all        = testRole("configs", "delete", "*/*")
		topologies = testRole("configs", "delete", "Topology/*")
		errRunning = config.ErrRunningExperiment.Error()
	)

	tests := map[string]struct {
		role  rbac.Role
		body  string
		want  int
		reply configsDeleteBody
		left  []string
	}{
		"one kind": {
			role: all, body: `{"kind":"topology"}`, want: http.StatusOK,
			reply: configsDeleteBody{Deleted: []string{"Topology/alpha", "Topology/beta"}},
			left:  []string{"Experiment/running"},
		},
		"every kind, not a running experiment": {
			role: all, body: `{"kind":"all"}`, want: http.StatusOK,
			reply: configsDeleteBody{
				Deleted: []string{"Topology/alpha", "Topology/beta"},
				Failed:  []configDeleteFailure{{Config: "Experiment/running", Error: errRunning}},
			},
			left: []string{"Experiment/running"},
		},
		"only what the role may delete": {
			role: topologies, body: `{"kind":"all"}`, want: http.StatusOK,
			reply: configsDeleteBody{Deleted: []string{"Topology/alpha", "Topology/beta"}},
			left:  []string{"Experiment/running"},
		},
		"none the role may delete": {
			role: topologies, body: `{"kind":"experiment"}`, want: http.StatusForbidden,
			left: []string{"Experiment/running", "Topology/alpha", "Topology/beta"},
		},
		"a kind without configs": {
			role: topologies, body: `{"kind":"scenario"}`, want: http.StatusOK,
			left: []string{"Experiment/running", "Topology/alpha", "Topology/beta"},
		},
		"no kind": {
			role: all, body: `{}`, want: http.StatusBadRequest,
			left: []string{"Experiment/running", "Topology/alpha", "Topology/beta"},
		},
		"unknown kind": {
			role: all, body: `{"kind":"vm"}`, want: http.StatusBadRequest,
			left: []string{"Experiment/running", "Topology/alpha", "Topology/beta"},
		},
		"not a body": {
			role: all, body: `{`, want: http.StatusBadRequest,
			left: []string{"Experiment/running", "Topology/alpha", "Topology/beta"},
		},
		"no delete permission": {
			role: testRole("configs", "list", "*"), body: `{"kind":"all"}`, want: http.StatusForbidden,
			left: []string{"Experiment/running", "Topology/alpha", "Topology/beta"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			storeDeleteConfigs(t)

			server := serveAs(t, "alice", tt.role).URL
			events := captureAudit(t)

			resp := do(t, http.MethodPost, server+"/api/v1/configs/delete", tt.body)
			body := readBody(t, resp)

			if resp.StatusCode != tt.want {
				t.Fatalf("POST /configs/delete %s = %d, want %d: %s", tt.body, resp.StatusCode, tt.want, body)
			}

			if tt.want == http.StatusOK {
				var got configsDeleteBody
				if err := json.Unmarshal([]byte(body), &got); err != nil {
					t.Fatalf("POST /configs/delete %s = %s, not JSON: %v", tt.body, body, err)
				}

				// the role of the test user is a stored config too
				got.Deleted = slices.DeleteFunc(got.Deleted, func(name string) bool { return strings.HasPrefix(name, "Role/") })

				if diff := cmp.Diff(tt.reply, got, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("POST /configs/delete %s mismatch (-want +got):\n%s", tt.body, diff)
				}

				for _, deleted := range tt.reply.Deleted {
					wantAudit(t, events(), "config-delete", "success", map[string]any{"phenix.config.name": deleted})
				}
			}

			left := []string{}

			for _, kind := range []string{"topology", "experiment"} {
				configs, err := config.List(kind)
				if err != nil {
					t.Fatalf("config.List(%s) error = %v", kind, err)
				}

				for _, c := range configs {
					left = append(left, c.FullName())
				}
			}

			if diff := cmp.Diff(tt.left, left, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("configs after POST /configs/delete %s mismatch (-want +got):\n%s", tt.body, diff)
			}
		})
	}
}
