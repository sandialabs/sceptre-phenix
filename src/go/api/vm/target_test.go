package vm_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/vm"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// CheckTarget finds the VM in the experiment's topology and, when asked,
// checks that the experiment runs.
//
//nolint:paralleltest // replaces the store
func TestCheckTarget(t *testing.T) {
	tests := []struct {
		name    string
		started string
		exp, vm string
		running bool
		wantErr error
	}{
		{name: "a VM of a running experiment", started: startTime, exp: testExp, vm: "vm1", running: true},
		{name: "a VM of a stopped experiment that need not run", exp: testExp, vm: "vm1"},
		{
			name: "a stopped experiment that must run", exp: testExp, vm: "vm1", running: true,
			wantErr: vm.ErrExperimentNotRunning,
		},
		{name: "a VM the topology does not have", started: startTime, exp: testExp, vm: "vm2", wantErr: mm.ErrVMNotFound},
		{name: "an experiment the store does not have", exp: "missing", vm: "vm1", wantErr: errAny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, test.started, vmNode("vm1"))

			exp, err := vm.CheckTarget(test.exp, test.vm, test.running)
			if !errMatches(err, test.wantErr) {
				t.Fatalf("CheckTarget(%q, %q, %t) error = %v, want %v", test.exp, test.vm, test.running, err, test.wantErr)
			}

			if test.wantErr == nil && exp.Metadata.Name != testExp {
				t.Errorf("CheckTarget(%q, %q, %t) = experiment %q, want %q",
					test.exp, test.vm, test.running, exp.Metadata.Name, testExp)
			}
		})
	}
}

// Snapshot refuses, before it saves anything, a VM whose disk minimega writes
// in place and a VM with more than one disk.
//
//nolint:paralleltest // replaces the store and minimega
func TestSnapshotRefusesDisksItCannotSave(t *testing.T) {
	tests := []struct {
		name     string
		disks    string
		snapshot string
		wantErr  error
	}{
		{name: "snapshot flag not set", disks: "/phenix/images/vm1.qc2", snapshot: "false", wantErr: vm.ErrSnapshotDisabled},
		{
			name:  "two disks",
			disks: "/tmp/minimega/0/disk-0.qcow2,virtio /phenix/images/data.qc2", snapshot: "true",
			wantErr: vm.ErrMultipleDisks,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, startTime, vmNode("vm1"))

			fake := &shellMM{fakeMM: &fakeMM{vms: mm.VMs{runningVM("vm1")}}}
			useMMDouble(t, fake)

			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == "vm info" {
					return []*minicli.Response{mmtest.Tabular("compute1", []string{"host", "id", "disks", "snapshot"},
						[]string{"compute1", "0", test.disks, test.snapshot})}
				}

				return nil
			})

			err := vm.Snapshot(t.Context(), testExp, "vm1", "snap", nil, vm.WithSaveTiming(time.Millisecond, time.Minute))
			if !errMatches(err, test.wantErr) {
				t.Errorf("Snapshot error = %v, want %v", err, test.wantErr)
			}

			if diff := cmp.Diff([]string{"vm info"}, mmtest.Bases(received())); diff != "" {
				t.Errorf("Snapshot minimega commands mismatch (-want +got):\n%s", diff)
			}

			if len(fake.shells) != 0 {
				t.Errorf("Snapshot shell commands = %q, want none", fake.shells)
			}
		})
	}
}

// useMMDouble makes fake answer for minimega's Go API for the rest of the test.
func useMMDouble(t *testing.T, fake mm.MM) {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double
}

// CommitToDisk refuses a VM whose snapshot flag is not set before it copies,
// stops or writes anything.
//
//nolint:paralleltest // replaces the store, minimega, cluster files, PATH and the phenix base directory
func TestCommitToDiskRefusesAVMWithoutSnapshot(t *testing.T) {
	base := commitTree(t)

	node := vmNode("vm1")
	hardware := node["hardware"].(map[string]any) //nolint:forcetypeassert // vmNode's hardware
	hardware["drives"] = []map[string]any{{"image": "vm1.qc2"}}
	useExperiment(t, startTime, node)

	originalFiles := file.DefaultClusterFiles
	t.Cleanup(func() { file.DefaultClusterFiles = originalFiles }) //nolint:reassign // restore test double

	file.DefaultClusterFiles = &commitFiles{} //nolint:reassign // install test double

	runs := useQemuImg(t, "")
	chain := fmt.Sprintf(`[{"filename":%q,"backing-filename":%q},{"filename":%q}]`,
		base+"/images/vm1.qc2", base+"/images/base.qc2", base+"/images/base.qc2")
	answer := commitMinimega(chain, nil, nil)

	received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "vm info" {
			return []*minicli.Response{mmtest.Tabular("compute1", []string{"host", "name", "id", "state", "snapshot"},
				[]string{"compute1", "vm1", "5", "RUNNING", "false"})}
		}

		return answer(cmd)
	})

	if _, err := vm.CommitToDisk(t.Context(), testExp, "vm1", "new.qc2", nil); !errMatches(err, vm.ErrSnapshotDisabled) {
		t.Errorf("CommitToDisk error = %v, want %v", err, vm.ErrSnapshotDisabled)
	}

	if diff := cmp.Diff(stopQueries(base)[:3], mmtest.Bases(received())); diff != "" {
		t.Errorf("CommitToDisk minimega commands mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string(nil), runs(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("CommitToDisk qemu-img runs mismatch (-want +got):\n%s", diff)
	}

	if got := filesIn(t, base+"/images"); !cmp.Equal([]string{"base.qc2", "vm1.qc2"}, got) {
		t.Errorf("CommitToDisk left images %q, want only base.qc2 and vm1.qc2", got)
	}
}
