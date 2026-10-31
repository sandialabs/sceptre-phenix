package builder_test

import (
	"encoding/json"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types"
	"phenix/types/builder"
)

// residueTopology is a topology spec that sets no field to the value phenix
// reads as unset, and no field to its Init default except os_type, which the
// schema requires. It has the fields the schema requires where a zero value
// is valid, and bridges that Init keeps.
func residueTopology() map[string]any {
	return map[string]any{"nodes": []any{
		map[string]any{
			"type":    "Router",
			"general": map[string]any{"hostname": "router", "description": "core router"},
			"hardware": map[string]any{
				"os_type": "vyatta", "vcpus": 2, "memory": 2048,
				"drives": []any{map[string]any{"image": "vyatta.qc2", "interface": "virtio", "inject_partition": 0}},
			},
			"network": map[string]any{
				"interfaces": []any{
					map[string]any{
						"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "static",
						"address": "10.0.0.1", "mask": 24,
					},
					map[string]any{
						"name": "eth1", "vlan": "UPLINK", "type": "ethernet", "proto": "static",
						"address": "10.9.0.1", "mask": 0, "bridge": "uplink",
					},
					map[string]any{
						"name": "eth2", "vlan": "MGMT", "type": "ethernet", "proto": "dhcp", "bridge": "lab",
					},
				},
				"routes": []any{map[string]any{"destination": "0.0.0.0/0", "next": "10.9.0.254", "cost": 0}},
				"ospf": map[string]any{
					"router_id": "0.0.0.1",
					"areas":     []any{map[string]any{"area_id": 0, "area_networks": []any{}}},
				},
				"rulesets": []any{map[string]any{
					"name": "in", "default": "drop",
					"rules": []any{map[string]any{
						"id": 0, "action": "accept", "protocol": "all",
						"source": map[string]any{"address": "10.0.0.0/24"},
					}},
				}, map[string]any{"name": "out", "default": "accept", "rules": []any{}}},
			},
		},
		map[string]any{
			"type":    "VirtualMachine",
			"general": map[string]any{"hostname": "host-a"},
			"labels":  map[string]any{"role": "web"},
			"hardware": map[string]any{
				"os_type": "linux",
				"drives":  []any{map[string]any{"image": "ubuntu.qc2"}},
			},
			"network": map[string]any{"interfaces": []any{
				map[string]any{
					"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "static",
					"address": "10.0.0.2", "mask": 24, "gateway": "10.0.0.1",
				},
				map[string]any{
					"name": "s0", "vlan": "SERIAL", "type": "serial", "proto": "static",
					"address": "10.1.0.2", "mask": 30, "udp_port": 0, "baud_rate": 9600, "device": "/dev/ttyS0",
				},
			}},
			"commands": []any{"exec df -h"},
			"delay":    map[string]any{"timer": "30s"},
		},
		map[string]any{
			"type":    "VirtualMachine",
			"general": map[string]any{"hostname": "isolated", "snapshot": false},
			"hardware": map[string]any{
				"os_type": "windows",
				"drives":  []any{map[string]any{"image": "win.qc2"}},
			},
			"network": map[string]any{"interfaces": []any{}},
		},
		map[string]any{
			"external": true,
			"type":     "HIL",
			"general":  map[string]any{"hostname": "plc"},
			"network": map[string]any{"interfaces": []any{
				map[string]any{
					"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "static",
					"address": "10.0.0.9", "mask": 24,
				},
			}},
		},
	}}
}

// initializedExperiment returns an experiment config as phenix stores it when
// it creates the experiment from topology: Init runs, and every field of the
// spec is stored (see hookCreate in api/experiment).
func initializedExperiment(t *testing.T, topology map[string]any, defaultBridge string) store.Config {
	t.Helper()

	config := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "lab-experiment", Annotations: map[string]string{"topology": "lab"}},
		Spec: map[string]any{
			"experimentName": "lab-experiment",
			"defaultBridge":  defaultBridge,
			"topology":       jsonValue(t, topology),
		},
	}

	experiment, err := types.DecodeExperimentFromConfig(config)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig(): %v", err)
	}

	if err := experiment.Spec.Init(); err != nil {
		t.Fatalf("ExperimentSpec.Init(): %v", err)
	}

	config.Spec = structs.MapDefaultCase(experiment.Spec, structs.CASESNAKE)

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("encoding experiment: %v", err)
	}

	var stored store.Config
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decoding experiment: %v", err)
	}

	return stored
}

// An experiment that phenix created from a topology imports as that topology:
// the import removes what Init added, and nothing else.
func TestFromExperimentDropsInitResidue(t *testing.T) {
	source := residueTopology()
	if err := types.ValidateSchema("Topology", "v1", source); err != nil {
		t.Fatalf("ValidateSchema(residueTopology()) = %v, want a valid source topology", err)
	}

	stored := initializedExperiment(t, source, "lab")

	// The stored experiment holds what Init added, or the test checks nothing.
	storedNodes := jsonValue(t, stored.Spec["topology"].(map[string]any)["nodes"])
	if diff := cmp.Diff(jsonValue(t, source["nodes"]), storedNodes); diff == "" {
		t.Fatal("initializedExperiment() nodes equal the source nodes, want what Init added")
	}

	doc, warnings := documentFromConfig(t, stored)

	for _, node := range doc.DeviceNodes() {
		network, _ := node.Device.Spec["network"].(map[string]any)
		ifaces, _ := network["interfaces"].([]any)

		for _, entry := range ifaces {
			if flag, ok := entry.(map[string]any)["bridge_set_in_topo"]; ok {
				t.Errorf("device %q interface %v bridge_set_in_topo = %v, want none", node.Device.Hostname, entry, flag)
			}
		}
	}

	if containsSubstring(warnings, `VLAN alias name ""`) {
		t.Errorf("FromConfig(lab-experiment) warnings = %q, want none about the VLAN alias \"\"", warnings)
	}

	config, _, err := doc.ToTopologyConfig("lab")
	if err != nil {
		t.Fatalf("ToTopologyConfig(lab): %v", err)
	}

	if diff := cmp.Diff(jsonValue(t, source["nodes"]), jsonValue(t, config.Spec["nodes"])); diff != "" {
		t.Errorf("ToTopologyConfig(lab) nodes mismatch, want the source topology (-want +got):\n%s", diff)
	}

	if err := types.ValidateConfigSpec(*config); err != nil {
		t.Errorf("ValidateConfigSpec() of the published topology = %v, want nil", err)
	}
}

// Each value that phenix's Init gives an unset field is removed from an
// imported experiment, also when the topology set it.
func TestFromExperimentDropsInitDefaults(t *testing.T) {
	node := map[string]any{
		"type": "VirtualMachine",
		"general": map[string]any{
			"hostname": "host-a", "description": "", "vm_type": "kvm", "snapshot": true, "do_not_boot": false,
		},
		"hardware": map[string]any{
			"os_type": "linux", "cpu": "Broadwell", "vcpus": 1, "memory": 512,
			"drives": []any{map[string]any{"image": "ubuntu.qc2", "cache_mode": ""}},
		},
		"network": map[string]any{"interfaces": []any{map[string]any{
			"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "dhcp",
			"autostart": false, "mtu": 0, "mac": "", "dns": nil,
		}}},
		"advanced":  map[string]any{},
		"overrides": map[string]any{},
		"external":  nil,
		"delay":     nil,
	}

	want := map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": "host-a"},
		"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "ubuntu.qc2"}}},
		"network": map[string]any{"interfaces": []any{map[string]any{
			"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "dhcp",
		}}},
	}

	config := loadConfig(t, "experiment.json")
	config.Spec["topology"] = map[string]any{"nodes": []any{node}}

	doc, _ := documentFromConfig(t, config)

	if diff := cmp.Diff(jsonValue(t, want), jsonValue(t, nodeByHostname(t, doc, "host-a").Device.Spec)); diff != "" {
		t.Errorf("FromConfig(builder-experiment) host-a spec mismatch (-want +got):\n%s", diff)
	}
}

// An external node gets no Init defaults, so the import keeps the values it
// sets and removes only the fields phenix reads as unset.
func TestFromExperimentKeepsExternalNodeValues(t *testing.T) {
	node := map[string]any{
		"external": true,
		"type":     "HIL",
		"general":  map[string]any{"hostname": "plc", "vm_type": "kvm", "snapshot": nil},
		"hardware": map[string]any{"os_type": "linux", "cpu": "Broadwell", "vcpus": 1, "memory": 512, "drives": nil},
		"labels":   nil,
	}

	want := map[string]any{
		"external": true,
		"type":     "HIL",
		"general":  map[string]any{"hostname": "plc", "vm_type": "kvm"},
		"hardware": map[string]any{"os_type": "linux", "cpu": "Broadwell", "vcpus": 1, "memory": 512},
	}

	config := loadConfig(t, "experiment.json")
	config.Spec["topology"] = map[string]any{"nodes": []any{node}}

	doc, _ := documentFromConfig(t, config)

	if diff := cmp.Diff(jsonValue(t, want), jsonValue(t, nodeByHostname(t, doc, "plc").Device.Spec)); diff != "" {
		t.Errorf("FromConfig(builder-experiment) plc spec mismatch (-want +got):\n%s", diff)
	}
}

// The import removes bridge_set_in_topo, and the bridge that Init gave an
// interface whose topology set none.
func TestFromExperimentDropsBridgeDefaults(t *testing.T) {
	for _, tt := range []struct {
		name       string
		bridge     string
		flag       any
		wantBridge string
	}{
		{name: "default bridge from Init", bridge: "lab", flag: false, wantBridge: ""},
		{name: "phenix bridge from Init", bridge: "phenix", flag: false, wantBridge: ""},
		{name: "other bridge without the topology", bridge: "other", flag: false, wantBridge: "other"},
		{name: "bridge set in the topology", bridge: "other", flag: true, wantBridge: "other"},
		{name: "default bridge set in the topology", bridge: "lab", flag: nil, wantBridge: "lab"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			iface := map[string]any{"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "dhcp", "bridge": tt.bridge}
			if tt.flag != nil {
				iface["bridge_set_in_topo"] = tt.flag
			}

			config := loadConfig(t, "experiment.json")
			config.Spec["defaultBridge"] = "lab"
			config.Spec["topology"] = map[string]any{"nodes": []any{map[string]any{
				"type":     "VirtualMachine",
				"general":  map[string]any{"hostname": "host-a"},
				"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "ubuntu.qc2"}}},
				"network":  map[string]any{"interfaces": []any{iface}},
			}}}

			doc, _ := documentFromConfig(t, config)
			spec := nodeByHostname(t, doc, "host-a").Device.Spec

			want := map[string]any{"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "dhcp"}
			if tt.wantBridge != "" {
				want["bridge"] = tt.wantBridge
			}

			got := spec["network"].(map[string]any)["interfaces"].([]any)[0]
			if diff := cmp.Diff(jsonValue(t, want), jsonValue(t, got)); diff != "" {
				t.Errorf("FromConfig() interface mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A bridge changed in a draft imported from an experiment stays when phenix
// creates an experiment from the published topology.
func TestPublishedExperimentImportKeepsChangedBridge(t *testing.T) {
	doc, _ := documentFromConfig(t, initializedExperiment(t, residueTopology(), "lab"))

	iface := nodeByHostname(t, doc, "host-a").Device.Spec["network"].(map[string]any)["interfaces"].([]any)[0]
	iface.(map[string]any)["bridge"] = "other"

	topology, err := doc.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology(): %v", err)
	}

	spec, err := topology.SpecV1()
	if err != nil {
		t.Fatalf("SpecV1(): %v", err)
	}

	if err := spec.Init("lab"); err != nil {
		t.Fatalf("TopologySpec.Init(lab): %v", err)
	}

	for hostname, want := range map[string]string{"host-a": "other", "router": "lab"} {
		got := spec.FindNodeByName(hostname).Network().Interfaces()[0].Bridge()
		if got != want {
			t.Errorf("TopologySpec.Init(lab) %s eth0 bridge = %q, want %q", hostname, got, want)
		}
	}
}

// A topology imports verbatim, with the values that Init would give it.
func TestFromTopologyKeepsInitDefaults(t *testing.T) {
	config := loadConfig(t, "topology.json")
	node := storedNode(t, config.Spec, "host-a")
	node["general"].(map[string]any)["vm_type"] = "kvm"
	node["hardware"].(map[string]any)["memory"] = 512
	node["advanced"] = map[string]any{}

	want := jsonValue(t, node)

	doc, _ := documentFromConfig(t, config)

	if diff := cmp.Diff(want, jsonValue(t, nodeByHostname(t, doc, "host-a").Device.Spec)); diff != "" {
		t.Errorf("FromConfig(builder-fixture) host-a spec mismatch, want it verbatim (-want +got):\n%s", diff)
	}
}

// The digest of an experiment import covers the stored experiment, with what
// Init added, so a publish compares it with the stored experiment.
func TestFromExperimentDigestCoversStoredSpec(t *testing.T) {
	stored := initializedExperiment(t, residueTopology(), "lab")
	doc, _ := documentFromConfig(t, stored)

	want, err := builder.ImportDigest(stored)
	if err != nil {
		t.Fatalf("ImportDigest(): %v", err)
	}

	if doc.Source.Digest != want {
		t.Errorf("FromConfig(lab-experiment) digest = %q, want %q of the stored experiment", doc.Source.Digest, want)
	}
}
