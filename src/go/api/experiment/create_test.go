package experiment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/common"
)

func TestCreateRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []experiment.CreateOption
		want string // in the error, naming what was rejected
	}{
		{name: "the all name", opts: withName("all"), want: "cannot use 'all'"},
		{name: "minimega's default namespace", opts: withName("minimega"), want: "cannot use 'minimega'"},
		{name: "a reserved name in any case", opts: withName("MiniMega"), want: "cannot use 'MiniMega'"},
		{name: "phenix's cluster namespace", opts: withName("__phenix__"), want: "cannot use '__phenix__'"},
		{name: "a reserved namespace in any case", opts: withName("__PHENIX__"), want: "cannot use '__PHENIX__'"},
		{name: "a topology annotation", opts: withAnnotation("topology"), want: `"topology"`},
		{name: "a scenario annotation", opts: withAnnotation("scenario"), want: `"scenario"`},
		{name: "a blank annotation key", opts: withAnnotation(" "), want: "keys cannot be empty"},
		{name: "a blank node annotation key", opts: withNodeAnnotation(" ", "x"), want: "keys cannot be empty"},
		{name: "a node annotation without a value", opts: withNodeAnnotation("custom/key", nil), want: `"custom/key"`},
		{
			name: "default apps not a bool",
			opts: withNodeAnnotation("phenix/default-apps", "false"),
			want: `"phenix/default-apps"`,
		},
		{
			name: "autotunnel not a list",
			opts: withNodeAnnotation("phenix/startup-autotunnel", "8080"),
			want: `"phenix/startup-autotunnel"`,
		},
		{
			name: "autotunnel not a list of strings",
			opts: withNodeAnnotation("phenix/startup-autotunnel", []any{"8080", 9090.0}),
			want: `"phenix/startup-autotunnel"`,
		},
		{
			name: "vyos password not a string",
			opts: withNodeAnnotation("vrouter/vyos-password", 1234.0),
			want: `"vrouter/vyos-password"`,
		},
		{name: "enable ssh not a string", opts: withNodeAnnotation("vrouter/enable-ssh", true), want: `"vrouter/enable-ssh"`},
		// a blank value would turn on C2 startup delivery for every VM
		{name: "startup via C2 blank", opts: withNodeAnnotation("phenix/startup-via-cc", " "), want: `"phenix/startup-via-cc"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// rejected before the store is read
			err := experiment.Create(context.Background(), tc.opts...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Create(%s) error = %v, want one containing %s", tc.name, err, tc.want)
			}
		})
	}
}

func withName(name string) []experiment.CreateOption {
	return []experiment.CreateOption{experiment.CreateWithName(name), experiment.CreateWithTopology("topo")}
}

func withAnnotation(key string) []experiment.CreateOption {
	return append(withName("exp"), experiment.CreateWithAnnotations(map[string]string{key: "x"}))
}

func withNodeAnnotation(key string, value any) []experiment.CreateOption {
	return append(withName("exp"), experiment.CreateWithNodeAnnotations(map[string]any{key: value}))
}

// useTestStore points the store at a new BoltDB file for the test.
func useTestStore(t *testing.T) {
	t.Helper()

	storetest.Use(t)

	originalBase := common.PhenixBase
	common.PhenixBase = t.TempDir() //nolint:reassign // experiment files go to a temporary tree

	t.Cleanup(func() { common.PhenixBase = originalBase }) //nolint:reassign // restore
}

func annotationTestTopology() *store.Config {
	vm := func(hostname string, annotations map[string]any) map[string]any {
		node := map[string]any{
			"type":    "VirtualMachine",
			"general": map[string]any{"hostname": hostname},
			"hardware": map[string]any{
				"os_type": "linux",
				"drives":  []any{map[string]any{"image": "ubuntu.qc2"}},
			},
			"network": map[string]any{
				"interfaces": []any{
					map[string]any{"name": "IF0", "type": "ethernet", "vlan": "EXP-1", "proto": "dhcp"},
				},
			},
		}

		if annotations != nil {
			node["annotations"] = annotations
		}

		return node
	}

	return &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "annotated"},
		Spec: map[string]any{
			"nodes": []any{
				vm("server", nil),
				vm("client", map[string]any{"phenix/default-apps": true}),
			},
		},
	}
}

func TestCreateAnnotatesEveryVMAndTheExperiment(t *testing.T) { //nolint:paralleltest // replaces package state
	useTestStore(t)

	if err := store.Create(annotationTestTopology()); err != nil {
		t.Fatalf("storing topology annotated: %v", err)
	}

	err := experiment.Create(
		context.Background(),
		experiment.CreateWithName("annotated"),
		experiment.CreateWithTopology("annotated"),
		experiment.CreateWithAnnotations(map[string]string{"phenix.workflow/tags": "run=1"}),
		experiment.CreateWithNodeAnnotations(map[string]any{
			"phenix/default-apps":       false,
			"phenix/startup-autotunnel": []any{"8080:80"},
		}),
	)
	if err != nil {
		t.Fatalf("Create(annotated) error = %v", err)
	}

	exp, err := experiment.Get("annotated")
	if err != nil {
		t.Fatalf("Get(annotated) error = %v", err)
	}

	if got := exp.Metadata.Annotations["phenix.workflow/tags"]; got != "run=1" {
		t.Errorf("Get(annotated) annotation phenix.workflow/tags = %q, want %q", got, "run=1")
	}

	if got := exp.Metadata.Annotations["topology"]; got != "annotated" {
		t.Errorf("Get(annotated) annotation topology = %q, want %q", got, "annotated")
	}

	// a node's own annotation wins over the one given for every node
	want := map[string]nodeAnnotations{
		"server": {DefaultApps: false, Autotunnel: []any{"8080:80"}},
		"client": {DefaultApps: true, Autotunnel: []any{"8080:80"}},
	}
	if diff := cmp.Diff(want, annotationsByNode(exp.Spec.Topology().Nodes())); diff != "" {
		t.Errorf("Get(annotated) node annotations mismatch (-want +got):\n%s", diff)
	}

	// the topology config itself is left as it was
	topoC, err := store.NewConfig("topology/annotated")
	if err != nil {
		t.Fatalf("store.NewConfig(topology/annotated) error = %v", err)
	}

	if err := store.Get(topoC); err != nil {
		t.Fatalf("store.Get(topology/annotated) error = %v", err)
	}

	topo, err := types.DecodeTopologyFromConfig(*topoC)
	if err != nil {
		t.Fatalf("types.DecodeTopologyFromConfig(topology/annotated) error = %v", err)
	}

	wantTopo := map[string]nodeAnnotations{"server": {}, "client": {DefaultApps: true}}
	if diff := cmp.Diff(wantTopo, annotationsByNode(topo.Nodes())); diff != "" {
		t.Errorf("topology/annotated node annotations after Create() mismatch (-want +got):\n%s", diff)
	}
}

// nodeAnnotations holds the annotations Create sets on a node, nil when the
// node has none.
type nodeAnnotations struct {
	DefaultApps any
	Autotunnel  any
}

// annotationsByNode returns each node's nodeAnnotations by hostname.
func annotationsByNode(nodes []ifaces.NodeSpec) map[string]nodeAnnotations {
	got := make(map[string]nodeAnnotations, len(nodes))

	for _, node := range nodes {
		defaultApps, _ := node.GetAnnotation("phenix/default-apps")
		autotunnel, _ := node.GetAnnotation("phenix/startup-autotunnel")

		got[node.General().Hostname()] = nodeAnnotations{DefaultApps: defaultApps, Autotunnel: autotunnel}
	}

	return got
}
