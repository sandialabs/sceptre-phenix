package vlan

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types"
)

// fromExperimentNode is a topology node with the given interfaces, each a name and a VLAN alias.
// Any external key makes a node external, so only an external node has one.
func fromExperimentNode(hostname string, external bool, ifaces ...[2]string) map[string]any {
	interfaces := make([]map[string]any, 0, len(ifaces))
	for _, iface := range ifaces {
		interfaces = append(interfaces, map[string]any{"name": iface[0], "vlan": iface[1]})
	}

	node := map[string]any{
		"type":    "VirtualMachine",
		"general": map[string]any{"hostname": hostname},
		"network": map[string]any{"interfaces": interfaces},
	}

	if external {
		node["external"] = true
	}

	return node
}

// FromExperiment gives an experiment's range, a copy of its pins and the VLAN
// interfaces of its VMs, and skips external nodes, nodes with no hostname and
// interfaces with no VLAN.
func TestFromExperiment(t *testing.T) {
	tests := []struct {
		name   string
		spec   map[string]any
		status map[string]any
		want   ExperimentInput
	}{
		{
			name: "no spec",
			want: ExperimentInput{Name: "exp", Pins: map[string]int{}},
		},
		{
			name: "no VLANs or topology",
			spec: map[string]any{"experimentName": "exp"},
			want: ExperimentInput{Name: "exp", Pins: map[string]int{}},
		},
		{
			name: "range, pins and VM interfaces of a running experiment",
			spec: map[string]any{
				"experimentName": "exp",
				"vlans":          map[string]any{"aliases": map[string]int{"A": 101, "B": 0}, "min": 100, "max": 199},
				"topology": map[string]any{"nodes": []map[string]any{
					fromExperimentNode("web", false, [2]string{"eth0", "A"}, [2]string{"eth1", ""}),
					fromExperimentNode("ext", true, [2]string{"eth0", "A"}),
					fromExperimentNode("", false, [2]string{"eth0", "A"}),
					fromExperimentNode("db", false, [2]string{"eth0", "B"}, [2]string{"eth1", "A"}),
					fromExperimentNode("none", false),
				}},
			},
			status: map[string]any{"startTime": "2024-01-01T00:00:00Z"},
			want: ExperimentInput{
				Name: "exp", Running: true, Min: 100, Max: 199, Pins: map[string]int{"A": 101, "B": 0},
				VMs: []VMInterface{
					{VM: "web", Interface: "eth0", Alias: "A"},
					{VM: "db", Interface: "eth0", Alias: "B"},
					{VM: "db", Interface: "eth1", Alias: "A"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exp, err := types.DecodeExperimentFromConfig(store.Config{
				Version:  "phenix.sandia.gov/v1",
				Kind:     "Experiment",
				Metadata: store.ConfigMetadata{Name: "exp"},
				Spec:     test.spec,
				Status:   test.status,
			})
			if err != nil {
				t.Fatalf("types.DecodeExperimentFromConfig() error = %v", err)
			}

			if test.spec == nil {
				exp.Spec = nil
			}

			got := FromExperiment(*exp)
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Fatalf("FromExperiment() mismatch (-want +got):\n%s", diff)
			}

			// the pins are a copy, so a change to them leaves the experiment as it was
			got.Pins["NEW"] = 1

			if exp.Spec != nil {
				if _, ok := exp.Spec.VLANs().Aliases()["NEW"]; ok {
					t.Errorf("FromExperiment().Pins is the experiment's own alias map, want a copy")
				}
			}
		})
	}
}
