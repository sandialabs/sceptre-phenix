package vm_test

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/api/vm"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// snapshotFiles lists the cluster's snapshots. Any other file.ClusterFiles
// method panics through the nil embedded interface.
type snapshotFiles struct {
	file.ClusterFiles

	snapshots []string
}

func (f snapshotFiles) GetExperimentSnapshots(string) ([]string, error) {
	return f.snapshots, nil
}

func TestRestore(t *testing.T) {
	relaunch := []string{
		"vm config clone vm1",
		"vm config uuid uuid-vm1",
		"vm config state exp/files/vm1__snap.state",
		"vm config disk exp/files/vm1__snap.hdd,writeback",
		"vm kill vm1",
		"vm flush vm1",
		"vm launch kvm vm1",
		"vm launch",
		"vm start vm1",
	}

	tests := []struct {
		name    string
		snap    string
		flush   []*minicli.Response
		wantErr bool
		want    []string // the commands sent to minimega
	}{
		{
			name: "relaunches the VM from its snapshot on whichever node runs it",
			snap: "vm1__snap.SNAP",
			flush: []*minicli.Response{
				{Host: "head"},
				{Host: "compute1", Error: "vm not found: vm1"},
			},
			want: relaunch,
		},
		{
			name: "stops at an error flushing the VM",
			snap: "vm1__snap.SNAP",
			flush: []*minicli.Response{
				{Host: "head", Error: "vm is busy"},
				{Host: "compute1", Error: "vm not found: vm1"},
			},
			wantErr: true,
			want:    relaunch[:6],
		},
		{
			name:    "rejects a snapshot the cluster does not have",
			snap:    "vm1__other.SNAP",
			wantErr: true,
		},
	}

	originalFiles := file.DefaultClusterFiles
	file.DefaultClusterFiles = snapshotFiles{snapshots: []string{"vm1__snap", "vm2__snap"}} //nolint:reassign // install test double

	t.Cleanup(func() { file.DefaultClusterFiles = originalFiles }) //nolint:reassign // restore test double

	useMM(t, &fakeMM{vms: mm.VMs{{Name: "vm1", UUID: "uuid-vm1"}}})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var (
				received func() []mmtest.Command
				other    sync.WaitGroup
				lockedAt = -1
			)

			// Once Restore clones the VM's config, another VM config sequence
			// waits for the namespace's lock and records how many commands
			// minimega had received when it got it.
			received = mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				switch {
				case strings.HasPrefix(cmd.Base, "vm flush "):
					return test.flush
				case strings.HasPrefix(cmd.Base, "vm config clone "):
					other.Add(1)

					go func() {
						defer other.Done()
						defer mm.LockVMConfig("exp")()

						lockedAt = len(received())
					}()
				}

				return nil
			})

			err := vm.Restore("exp", "vm1", test.snap)
			if (err != nil) != test.wantErr {
				t.Fatalf("Restore returned %v, want error %t", err, test.wantErr)
			}

			other.Wait()

			sent := received()

			if got := mmtest.Bases(sent); !slices.Equal(got, test.want) {
				t.Fatalf("sent %q, want %q", got, test.want)
			}

			for _, cmd := range sent {
				if cmd.Namespace != "exp" {
					t.Errorf("sent %q in namespace %q, want exp", cmd.Base, cmd.Namespace)
				}
			}

			// no other VM config sequence runs in the namespace from the clone
			// until Restore returns
			if len(sent) > 0 && lockedAt != len(sent) {
				t.Errorf("another VM config sequence locked the namespace after %d of %d commands",
					lockedAt, len(sent))
			}
		})
	}
}
