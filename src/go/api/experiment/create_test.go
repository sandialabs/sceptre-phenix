package experiment_test

import (
	"context"
	"strings"
	"testing"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// rejected before the store is read
			err := experiment.Create(context.Background(), tc.opts...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Create error = %v, want one containing %s", err, tc.want)
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
		t.Fatal(err)
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
		t.Fatalf("creating experiment: %v", err)
	}

	exp, err := experiment.Get("annotated")
	if err != nil {
		t.Fatal(err)
	}

	if got := exp.Metadata.Annotations["phenix.workflow/tags"]; got != "run=1" {
		t.Errorf("experiment annotation phenix.workflow/tags = %q, want run=1", got)
	}

	if got := exp.Metadata.Annotations["topology"]; got != "annotated" {
		t.Errorf("experiment annotation topology = %q, want annotated", got)
	}

	want := map[string]bool{"server": false, "client": true}

	for _, node := range exp.Spec.Topology().Nodes() {
		hostname := node.General().Hostname()

		defaultApps, _ := node.GetAnnotation("phenix/default-apps")
		if defaultApps != want[hostname] {
			t.Errorf("%s: phenix/default-apps = %v, want %v", hostname, defaultApps, want[hostname])
		}

		if tunnels, ok := node.GetAnnotation("phenix/startup-autotunnel"); !ok {
			t.Errorf("%s: phenix/startup-autotunnel missing", hostname)
		} else if list, _ := tunnels.([]any); len(list) != 1 || list[0] != "8080:80" {
			t.Errorf("%s: phenix/startup-autotunnel = %v", hostname, tunnels)
		}
	}

	// the topology config itself is left as it was
	topoC, _ := store.NewConfig("topology/annotated")
	if err := store.Get(topoC); err != nil {
		t.Fatal(err)
	}

	topo, err := types.DecodeTopologyFromConfig(*topoC)
	if err != nil {
		t.Fatal(err)
	}

	for _, node := range topo.Nodes() {
		if _, ok := node.GetAnnotation("phenix/startup-autotunnel"); ok {
			t.Errorf("topology config node %s was annotated", node.General().Hostname())
		}
	}
}
