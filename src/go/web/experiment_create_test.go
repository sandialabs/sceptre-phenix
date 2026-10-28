package web

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/types"
	"phenix/web/rbac"
)

// testTopology is the stored config of topology test-topology, with one VM.
func testTopology() store.Config {
	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "test-topology"},
		Spec: map[string]any{
			"nodes": []any{map[string]any{
				"type":    "VirtualMachine",
				"general": map[string]any{"hostname": "test-vm"},
				"hardware": map[string]any{
					"os_type": "linux",
					"drives":  []any{map[string]any{"image": "test.qc2"}},
				},
				"network": map[string]any{
					"interfaces": []any{
						map[string]any{"name": "IF0", "type": "ethernet", "vlan": "EXP_1", "proto": "dhcp"},
					},
				},
			}},
		},
	}
}

// Creating an experiment stores the annotations the request gives it: its
// workflow branch as an experiment annotation, and each node annotation, with
// its JSON type, on every VM. The branch may be given only once.
func TestCreateExperimentAnnotations(t *testing.T) {
	tests := map[string]struct {
		body     string
		status   int
		message  string // in the response
		wantExp  store.Annotations
		wantNode map[string]any
	}{
		"workflow branch and typed node annotations": {
			body: `{
				"name": "test-experiment",
				"topology": "test-topology",
				"workflow_branch": "main",
				"annotations": {"phenix.workflow/tags": "run=1"},
				"node_annotations": {
					"phenix/default-apps": false,
					"phenix/startup-autotunnel": ["8080:80"],
					"vrouter/enable-ssh": "IF0"
				}
			}`,
			status: http.StatusNoContent,
			wantExp: store.Annotations{
				"topology":               "test-topology",
				"phenix.workflow/tags":   "run=1",
				"phenix.workflow/branch": "main",
			},
			wantNode: map[string]any{
				"phenix/default-apps":       false,
				"phenix/startup-autotunnel": []any{"8080:80"},
				"vrouter/enable-ssh":        "IF0",
			},
		},
		"none": {
			body:    `{"name": "test-experiment", "topology": "test-topology"}`,
			status:  http.StatusNoContent,
			wantExp: store.Annotations{"topology": "test-topology"},
		},
		"branch given twice": {
			body: `{
				"name": "test-experiment",
				"topology": "test-topology",
				"workflow_branch": "main",
				"annotations": {"phenix.workflow/branch": "other"}
			}`,
			status:  http.StatusBadRequest,
			message: `"phenix.workflow/branch"`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, testTopology())
			useExperimentFilesDir(t, nil)

			role := combinedRole(
				testRole("experiments", "create"),
				testRole("vms/forwards", "create", "test-experiment/*"),
			)
			server := serveAs(t, "alice", role).URL

			resp := do(t, http.MethodPost, server+"/api/v1/experiments", tc.body)

			body := readBody(t, resp)
			if resp.StatusCode != tc.status {
				t.Fatalf("POST /api/v1/experiments = %d, want %d: %s", resp.StatusCode, tc.status, body)
			}

			if resp.StatusCode >= http.StatusBadRequest {
				body = errorMessage(t, resp.StatusCode, body)
			}

			if !strings.Contains(body, tc.message) {
				t.Errorf("POST /api/v1/experiments body = %q, want it to contain %q", body, tc.message)
			}

			c, _ := store.NewConfig("experiment/test-experiment")

			err := store.Get(c)
			if tc.status != http.StatusNoContent {
				if !errors.Is(err, store.ErrNotExist) {
					t.Errorf("store.Get(experiment/test-experiment) after a refused create error = %v, want %v",
						err, store.ErrNotExist)
				}

				return
			}

			if err != nil {
				t.Fatalf("store.Get(experiment/test-experiment) error = %v", err)
			}

			if diff := cmp.Diff(tc.wantExp, c.Metadata.Annotations); diff != "" {
				t.Errorf("POST /api/v1/experiments experiment annotations mismatch (-want +got):\n%s", diff)
			}

			exp, err := types.DecodeExperimentFromConfig(*c)
			if err != nil {
				t.Fatalf("types.DecodeExperimentFromConfig(experiment/test-experiment) error = %v", err)
			}

			nodes := exp.Spec.Topology().Nodes()
			if len(nodes) != 1 {
				t.Fatalf("POST /api/v1/experiments stored %d nodes, want the topology's 1", len(nodes))
			}

			if diff := cmp.Diff(tc.wantNode, nodes[0].Annotations(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("POST /api/v1/experiments node annotations mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Request-supplied autotunnel annotations create port forwards when the
// experiment starts, so they require permission to create forwards to its VMs.
func TestCreateExperimentAutotunnelRequiresForwards(t *testing.T) {
	const (
		autotunnel = `{"name": "test-experiment", "topology": "test-topology",
			"node_annotations": {"phenix/startup-autotunnel": ["9000:127.0.0.1:8080"]}}`
		other = `{"name": "test-experiment", "topology": "test-topology",
			"node_annotations": {"vrouter/enable-ssh": "IF0"}}`
	)

	create := testRole("experiments", "create")

	tests := map[string]struct {
		role   rbac.Role
		body   string
		status int
	}{
		"with forward permission": {
			role:   combinedRole(create, testRole("vms/forwards", "create", "test-experiment/test-vm")),
			body:   autotunnel,
			status: http.StatusNoContent,
		},
		"without forward permission": {
			role:   create,
			body:   autotunnel,
			status: http.StatusForbidden,
		},
		"forward permission for another VM": {
			role:   combinedRole(create, testRole("vms/forwards", "create", "test-experiment/other-vm")),
			body:   autotunnel,
			status: http.StatusForbidden,
		},
		"no autotunnel annotation": {
			role:   create,
			body:   other,
			status: http.StatusNoContent,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, testTopology())
			useExperimentFilesDir(t, nil)

			server := serveAs(t, "alice", tc.role).URL

			resp := do(t, http.MethodPost, server+"/api/v1/experiments", tc.body)
			if body := readBody(t, resp); resp.StatusCode != tc.status {
				t.Fatalf("POST /api/v1/experiments = %d, want %d: %s", resp.StatusCode, tc.status, body)
			}

			c, _ := store.NewConfig("experiment/test-experiment")

			err := store.Get(c)
			if tc.status == http.StatusNoContent && err != nil {
				t.Errorf("store.Get(experiment/test-experiment) after a create error = %v, want nil", err)
			}

			if tc.status != http.StatusNoContent && !errors.Is(err, store.ErrNotExist) {
				t.Errorf("store.Get(experiment/test-experiment) after a refused create error = %v, want %v",
					err, store.ErrNotExist)
			}
		})
	}
}
