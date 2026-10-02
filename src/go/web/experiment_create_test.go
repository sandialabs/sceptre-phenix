package web

import (
	"errors"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types"
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
		wantExp  map[string]string
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
			wantExp: map[string]string{
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
			wantExp: map[string]string{"topology": "test-topology"},
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

			server := serveAs(t, "alice", testRole("experiments", "create")).URL

			resp := do(t, http.MethodPost, server+"/api/v1/experiments", tc.body)
			if body := readBody(t, resp); resp.StatusCode != tc.status || !strings.Contains(body, tc.message) {
				t.Fatalf("got %d %q, want %d with %q", resp.StatusCode, body, tc.status, tc.message)
			}

			c, _ := store.NewConfig("experiment/test-experiment")

			err := store.Get(c)
			if tc.status != http.StatusNoContent {
				if !errors.Is(err, store.ErrNotExist) {
					t.Fatalf("stored an experiment it refused to create: %v", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if !maps.Equal(c.Metadata.Annotations, tc.wantExp) {
				t.Errorf("experiment annotations = %v, want %v", c.Metadata.Annotations, tc.wantExp)
			}

			exp, err := types.DecodeExperimentFromConfig(*c)
			if err != nil {
				t.Fatal(err)
			}

			nodes := exp.Spec.Topology().Nodes()
			if len(nodes) != 1 {
				t.Fatalf("experiment has %d nodes, want the topology's one", len(nodes))
			}

			if got := nodes[0].Annotations(); !maps.EqualFunc(got, tc.wantNode, reflect.DeepEqual) {
				t.Errorf("node annotations = %#v, want %#v", got, tc.wantNode)
			}
		})
	}
}
