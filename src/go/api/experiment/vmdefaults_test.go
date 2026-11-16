package experiment_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/store"
)

// vmDefaultsTopology is the topology settings, of the VMs server (sized and
// placed by hand) and client (as the topology defaults it), and an external
// node.
func vmDefaultsTopology() *store.Config {
	return &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "settings"},
		Spec: map[string]any{
			"nodes": []any{
				map[string]any{
					"type":    "VirtualMachine",
					"general": map[string]any{"hostname": "server", "snapshot": true, "do_not_boot": true},
					"hardware": map[string]any{
						"os_type": "linux", "vcpus": 4, "memory": 4096,
						"drives": []any{map[string]any{"image": "server.qc2", "inject_partition": 2}},
					},
				},
				map[string]any{
					"type":     "VirtualMachine",
					"general":  map[string]any{"hostname": "client"},
					"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "client.qc2"}}},
				},
				map[string]any{
					"type":     "Router",
					"external": true,
					"general":  map[string]any{"hostname": "outside"},
					"hardware": map[string]any{"os_type": "linux"},
				},
			},
		},
	}
}

// createVMDefaultsExperiment creates experiment exp from the settings topology, with server on node1.
func createVMDefaultsExperiment(t *testing.T) {
	t.Helper()

	useTestStore(t)

	if err := store.Create(vmDefaultsTopology()); err != nil {
		t.Fatalf("storing topology settings: %v", err)
	}

	err := experiment.Create(
		t.Context(),
		experiment.CreateWithName("exp"),
		experiment.CreateWithTopology("settings"),
		experiment.CreateWithSchedules(map[string]string{"server": "node1"}),
	)
	if err != nil {
		t.Fatalf("Create(exp) error = %v", err)
	}
}

// storedVMDefaults returns the saved VM settings of the stored experiment name.
func storedVMDefaults(t *testing.T, name string) map[string]experiment.VMDefaults {
	t.Helper()

	exp, err := experiment.Get(name)
	if err != nil {
		t.Fatalf("Get(%s) error = %v", name, err)
	}

	saved, err := experiment.VMDefaultsOf(exp.Metadata.Annotations)
	if err != nil {
		t.Fatalf("VMDefaultsOf(%s) error = %v", name, err)
	}

	return saved
}

// wantVMDefaults is the saved settings of the settings topology's VMs, with
// the defaults that phenix gives client when it creates the experiment.
func wantVMDefaults() map[string]experiment.VMDefaults {
	server, client := 2, 1

	return map[string]experiment.VMDefaults{
		"server": {
			CPUs: 4, RAM: 4096, Disk: "server.qc2", InjectPartition: &server,
			Snapshot: true, DNB: true, Host: "node1",
		},
		"client": {CPUs: 1, RAM: 512, Disk: "client.qc2", InjectPartition: &client, Snapshot: true},
	}
}

// Create saves the settings of each VM, but not of an external node.
func TestCreateSavesTheVMSettings(t *testing.T) { //nolint:paralleltest // replaces package state
	createVMDefaultsExperiment(t)

	if diff := cmp.Diff(wantVMDefaults(), storedVMDefaults(t, "exp")); diff != "" {
		t.Errorf("saved VM settings after Create(exp) mismatch (-want +got):\n%s", diff)
	}
}

// A config update keeps the saved settings that the stored experiment has,
// whether it changes, drops or keeps the annotation, and so does a rename.
func TestUpdatesKeepTheSavedVMSettings(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := []struct {
		name     string
		rename   string
		specName string // the spec.experimentName the update gives, when not empty
		edit     func(store.Annotations)
	}{
		{name: "an update that changes them", edit: func(a store.Annotations) {
			a[experiment.VMDefaultsAnnotation] = `{"server":{"cpus":64}}`
		}},
		{name: "an update that drops them", edit: func(a store.Annotations) { delete(a, experiment.VMDefaultsAnnotation) }},
		{name: "an update that keeps them", edit: func(store.Annotations) {}},
		{name: "a rename", rename: "lab", edit: func(store.Annotations) {}},
		{name: "a rename that also renames the spec", rename: "lab", specName: "lab", edit: func(store.Annotations) {}},
		{name: "a spec that names another experiment", specName: "other", edit: func(store.Annotations) {}},
		{name: "a rename to a spec of another experiment", rename: "lab", specName: "other", edit: func(store.Annotations) {}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			createVMDefaultsExperiment(t)

			// another experiment, with server on another host
			err := experiment.Create(
				t.Context(),
				experiment.CreateWithName("other"),
				experiment.CreateWithTopology("settings"),
				experiment.CreateWithSchedules(map[string]string{"server": "node2"}),
			)
			if err != nil {
				t.Fatalf("Create(other) error = %v", err)
			}

			c, err := config.Get("experiment/exp")
			if err != nil {
				t.Fatalf("config.Get(experiment/exp) error = %v", err)
			}

			tc.edit(c.Metadata.Annotations)

			if tc.specName != "" {
				c.Spec["experimentName"] = tc.specName
			}

			name := "exp"
			if tc.rename != "" {
				name = tc.rename
				c.Metadata.Name = tc.rename
			}

			if err := config.Update("experiment/exp", c); err != nil {
				t.Fatalf("config.Update(experiment/exp) error = %v", err)
			}

			if diff := cmp.Diff(wantVMDefaults(), storedVMDefaults(t, name)); diff != "" {
				t.Errorf("saved VM settings of %s after the update mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// Writing the experiment back, as phenix does to save its status or spec, keeps the saved settings.
func TestWriteToStoreKeepsTheSavedVMSettings(t *testing.T) { //nolint:paralleltest // replaces package state
	createVMDefaultsExperiment(t)

	exp, err := experiment.Get("exp")
	if err != nil {
		t.Fatalf("Get(exp) error = %v", err)
	}

	exp.Spec.Topology().FindNodeByName("server").Hardware().SetVCPU(8)

	if err := exp.WriteToStore(false); err != nil {
		t.Fatalf("WriteToStore() error = %v", err)
	}

	if diff := cmp.Diff(wantVMDefaults(), storedVMDefaults(t, "exp")); diff != "" {
		t.Errorf("saved VM settings after WriteToStore() mismatch (-want +got):\n%s", diff)
	}
}

// An experiment config created with its own saved settings gets those of its VMs instead.
func TestConfigCreateReplacesGivenVMSettings(t *testing.T) { //nolint:paralleltest // replaces package state
	useTestStore(t)

	c := &store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name:        "forged",
			Annotations: store.Annotations{experiment.VMDefaultsAnnotation: `{"vm":{"cpus":64}}`},
		},
		Spec: map[string]any{
			"experimentName": "forged",
			"baseDir":        t.TempDir(),
			"topology":       vmDefaultsTopology().Spec,
		},
	}

	if _, err := config.Create(config.CreateFromConfig(c)); err != nil {
		t.Fatalf("config.Create(experiment forged) error = %v", err)
	}

	want := wantVMDefaults()
	server := want["server"]
	server.Host = ""
	want["server"] = server

	if diff := cmp.Diff(want, storedVMDefaults(t, "forged")); diff != "" {
		t.Errorf("saved VM settings of forged mismatch (-want +got):\n%s", diff)
	}
}

func TestVMDefaultsOf(t *testing.T) {
	t.Parallel()

	if got, err := experiment.VMDefaultsOf(map[string]string{"topology": "t"}); got != nil || err != nil {
		t.Errorf("VMDefaultsOf(no saved settings) = %v, %v, want nil, nil", got, err)
	}

	if _, err := experiment.VMDefaultsOf(map[string]string{experiment.VMDefaultsAnnotation: "{"}); err == nil {
		t.Error("VMDefaultsOf(settings that do not decode) error = nil, want an error")
	}
}
