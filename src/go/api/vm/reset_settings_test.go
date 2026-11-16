package vm_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
)

// resetSettings is the settings of a VM in a saved experiment spec.
type resetSettings struct {
	CPUs, RAM, Partition int
	Disk                 string
	Snapshot, DNB        bool
	Host                 string
	Scheduled            bool
}

// savedSettings decodes the settings of the VM name from a saved experiment spec.
func savedSettings(t *testing.T, spec map[string]any, name string) resetSettings {
	t.Helper()

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: testExp},
		Spec:     spec,
	})
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig(saved experiment) error = %v", err)
	}

	node := exp.Spec.Topology().FindNodeByName(name)
	drive := node.Hardware().Drives()[0]
	host, scheduled := exp.Spec.Schedules()[name]

	got := resetSettings{
		CPUs: node.Hardware().VCPU(), RAM: node.Hardware().Memory(), Disk: drive.Image(),
		Snapshot: *node.General().Snapshot(), DNB: *node.General().DoNotBoot(), Host: host, Scheduled: scheduled,
	}

	if p := drive.InjectPartition(); p != nil {
		got.Partition = *p
	}

	return got
}

// ResetSettings restores the saved settings of each VM it can in one save of
// the experiment, and names each VM it cannot reset. It saves nothing when it
// can reset no VM, and refuses a running experiment as a whole.
func TestResetSettings(t *testing.T) {
	const saved = `{"web":{"cpus":1,"ram":1024,"disk":"/images/base.qc2","inject_partition":2,` +
		`"snapshot":true,"dnb":false,"host":"node1"},` +
		`"db":{"cpus":4,"ram":2048,"disk":"/images/db.qc2","snapshot":false,"dnb":true,"host":""}}`

	var (
		changedWeb = resetSettings{CPUs: 2, RAM: 512, Partition: 3, Disk: "/images/other.qc2", DNB: true, Host: "node9", Scheduled: true}
		changedDB  = resetSettings{CPUs: 2, RAM: 512, Partition: 1, Disk: "/images/db.qc2", Host: "node9", Scheduled: true}
		savedWeb   = resetSettings{
			CPUs: 1, RAM: 1024, Partition: 2, Disk: "/images/base.qc2", Snapshot: true, Host: "node1", Scheduled: true,
		}
		savedDB = resetSettings{CPUs: 4, RAM: 2048, Partition: 1, Disk: "/images/db.qc2", DNB: true}
		errSave = errors.New("save failed")
	)

	tests := []struct {
		name        string
		vms         []string
		running     bool
		annotations map[string]string
		saveErr     error
		wantErr     error            // the error of the experiment as a whole
		wantVMErrs  map[string]error // the error of each VM it cannot reset
		want        map[string]resetSettings
	}{
		{
			name: "restores the settings and sets the host", vms: []string{"web"},
			annotations: map[string]string{experiment.VMDefaultsAnnotation: saved},
			want:        map[string]resetSettings{"web": savedWeb, "db": changedDB},
		},
		{
			name: "restores the settings and clears the host", vms: []string{"db"},
			annotations: map[string]string{experiment.VMDefaultsAnnotation: saved},
			want:        map[string]resetSettings{"web": changedWeb, "db": savedDB},
		},
		{
			name: "restores every VM in one save", vms: []string{"web", "db"},
			annotations: map[string]string{experiment.VMDefaultsAnnotation: saved},
			want:        map[string]resetSettings{"web": savedWeb, "db": savedDB},
		},
		{
			name: "restores the VMs it can", vms: []string{"web", "nope", "db"},
			annotations: map[string]string{experiment.VMDefaultsAnnotation: `{"web":` + `{"cpus":1,"ram":1024,` +
				`"disk":"/images/base.qc2","inject_partition":2,"snapshot":true,"dnb":false,"host":"node1"}}`},
			wantVMErrs: map[string]error{"nope": mm.ErrVMNotFound, "db": experiment.ErrNoVMDefaults},
			want:       map[string]resetSettings{"web": savedWeb, "db": changedDB},
		},
		{
			name: "refuses a running experiment", vms: []string{"web"}, running: true,
			annotations: map[string]string{experiment.VMDefaultsAnnotation: saved}, wantErr: experiment.ErrExperimentRunning,
		},
		{name: "refuses a VM the topology lacks", vms: []string{"nope"}, wantVMErrs: map[string]error{"nope": mm.ErrVMNotFound}},
		{
			name: "refuses an experiment that saved nothing", vms: []string{"web"},
			wantVMErrs: map[string]error{"web": experiment.ErrNoVMDefaults},
		},
		{
			name: "refuses saved settings that do not decode", vms: []string{"web"},
			annotations: map[string]string{experiment.VMDefaultsAnnotation: "{"}, wantErr: errAny,
		},
		{
			name: "fails when the experiment cannot be saved", vms: []string{"web"},
			annotations: map[string]string{experiment.VMDefaultsAnnotation: saved}, saveErr: errSave, wantErr: errSave,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			started := ""
			if tc.running {
				started = startTime
			}

			web := vmNode("web")
			web["general"] = map[string]any{"hostname": "web", "snapshot": false, "do_not_boot": true}
			web["hardware"].(map[string]any)["drives"] = []map[string]any{{"image": "/images/other.qc2", "inject_partition": 3}}

			db := vmNode("db")
			db["general"] = map[string]any{"hostname": "db", "snapshot": false, "do_not_boot": false}

			fake := useExperiment(t, started, web, db)
			fake.experiment.Metadata.Annotations = tc.annotations
			fake.experiment.Spec["schedules"] = map[string]any{"web": "node9", "db": "node9"}
			fake.updateErr = tc.saveErr

			vmErrs, err := vm.ResetSettings(testExp, tc.vms)
			if !errMatches(err, tc.wantErr) {
				t.Fatalf("ResetSettings(%v) error = %v, want %v", tc.vms, err, tc.wantErr)
			}

			for name, want := range tc.wantVMErrs {
				if !errors.Is(vmErrs[name], want) {
					t.Errorf("ResetSettings(%v) error of VM %s = %v, want %v", tc.vms, name, vmErrs[name], want)
				}
			}

			if len(vmErrs) != len(tc.wantVMErrs) {
				t.Errorf("ResetSettings(%v) VM errors = %v, want %d", tc.vms, vmErrs, len(tc.wantVMErrs))
			}

			if tc.want == nil {
				if len(fake.saved) != 0 {
					t.Errorf("ResetSettings(%v) saved the experiment %d times, want none", tc.vms, len(fake.saved))
				}

				return
			}

			if len(fake.saved) != 1 {
				t.Fatalf("ResetSettings(%v) saved the experiment %d times, want once", tc.vms, len(fake.saved))
			}

			got := map[string]resetSettings{}
			for name := range tc.want {
				got[name] = savedSettings(t, fake.saved[0], name)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ResetSettings(%v) saved VMs mismatch (-want +got):\n%s", tc.vms, diff)
			}
		})
	}
}
