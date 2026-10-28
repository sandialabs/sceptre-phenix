package vm_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// shellMM is fakeMM that records each mesh shell command as "host: command"
// and fails the one starting with failOn, if set.
type shellMM struct {
	*fakeMM

	failOn string
	shells []string
}

func (m *shellMM) MeshShell(host, command string) error {
	m.shells = append(m.shells, host+": "+command)

	if m.failOn != "" && strings.HasPrefix(command, m.failOn) {
		return errors.New("shell command failed")
	}

	return nil
}

func TestSnapshot(t *testing.T) {
	const (
		files = "/phenix/images/" + testExp + "/files"
		saved = "/phenix/images/" + testExp + "_vm1__snap"
	)

	moves := []string{
		"compute1: mkdir -p " + files,
		"compute1: mv " + saved + ".state " + files + "/vm1__snap.state",
		"compute1: mv " + saved + ".hdd " + files + "/vm1__snap.hdd",
	}

	tests := []struct {
		name    string
		failOn  string
		wantErr bool
		want    []string // the shell commands run on the VM's node
	}{
		{
			name: "moves the saved state and disk into the experiment files on the VM's node",
			want: moves,
		},
		{
			name:    "stops at an error moving the memory snapshot",
			failOn:  "mv " + saved + ".state",
			wantErr: true,
			want:    moves[:2],
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vm.SetSaveTiming(t, time.Millisecond, time.Minute)
			useExperiment(t, startTime, vmNode("vm1"))

			fake := &shellMM{fakeMM: &fakeMM{vms: mm.VMs{runningVM("vm1")}}, failOn: test.failOn}

			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

			mm.DefaultMM = fake //nolint:reassign // install test double

			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				switch cmd.Base {
				case "vm info":
					return []*minicli.Response{mmtest.Tabular("compute1", []string{"host", "id"}, []string{"compute1", "0"})}
				case "vm save":
					return []*minicli.Response{mmtest.Tabular(
						"compute1", []string{"name", "status", "complete (%)"}, []string{"vm1", "completed", "100"},
					)}
				}

				return nil
			})

			var progress []string

			err := vm.Snapshot(t.Context(), testExp, "vm1", "snap.SNAP", func(p string) { progress = append(progress, p) })

			switch {
			case test.wantErr && err == nil:
				t.Error("Snapshot error = nil, want an error")
			case !test.wantErr && err != nil:
				t.Errorf("Snapshot error = %v, want nil", err)
			}

			if diff := cmp.Diff(test.want, fake.shells, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Snapshot shell commands mismatch (-want +got):\n%s", diff)
			}

			wantSent := []string{"vm info", "vm save vm1 " + testExp + "_vm1__snap", "vm save", "vm start vm1"}
			if diff := cmp.Diff(wantSent, mmtest.Bases(received())); diff != "" {
				t.Errorf("Snapshot minimega commands mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff([]string{"completed"}, progress); diff != "" {
				t.Errorf("Snapshot progress mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// saveStatus is minimega's `vm save` status row for vm1.
func saveStatus(status, complete string) []*minicli.Response {
	return []*minicli.Response{mmtest.Tabular(
		"compute1", []string{"name", "status", "complete (%)"}, []string{"vm1", status, complete},
	)}
}

// Whatever becomes of its memory snapshot, Snapshot resumes the VM, and a
// status query that fails or answers nothing does not end the wait.
func TestSnapshotAlwaysResumesTheVM(t *testing.T) {
	tests := []struct {
		name string
		// the answers to successive `vm save` status queries; the last repeats
		statuses [][]*minicli.Response
		// the answer to `vm start`
		start        []*minicli.Response
		wantErr      string // a substring of the error, "" for none
		wantProgress []string
	}{
		{
			name: "waits through empty and failed status queries",
			statuses: [][]*minicli.Response{
				nil, {mmtest.Error("compute1", "qmp busy")}, saveStatus("active", "50.00"), saveStatus("completed", "100.00"),
			},
			wantProgress: []string{"50.000000", "completed"},
		},
		{
			name:         "fails a failed save",
			statuses:     [][]*minicli.Response{saveStatus("failed", "0.00")},
			wantErr:      "memory snapshot for VM vm1 failed",
			wantProgress: []string{},
		},
		{
			name:         "gives up on a save that never completes",
			statuses:     [][]*minicli.Response{saveStatus("active", "10.00")},
			wantErr:      "did not complete within",
			wantProgress: nil, // as many as polls fit before the deadline
		},
		{
			name:     "gives up on a status that never comes, naming the last error",
			statuses: [][]*minicli.Response{{mmtest.Error("compute1", "qmp busy")}},
			wantErr:  "qmp busy",
		},
		{
			name:         "reports a failed resume",
			statuses:     [][]*minicli.Response{saveStatus("completed", "100.00")},
			start:        []*minicli.Response{mmtest.Error("compute1", "vm gone")},
			wantErr:      "resuming VM vm1 after snapshot",
			wantProgress: []string{"completed"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vm.SetSaveTiming(t, time.Millisecond, 50*time.Millisecond)
			useExperiment(t, startTime, vmNode("vm1"))

			fake := &shellMM{fakeMM: &fakeMM{vms: mm.VMs{runningVM("vm1")}}}

			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

			mm.DefaultMM = fake //nolint:reassign // install test double

			var queries atomic.Int32

			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				switch cmd.Base {
				case "vm info":
					return []*minicli.Response{mmtest.Tabular("compute1", []string{"host", "id"}, []string{"compute1", "0"})}
				case "vm save":
					n := min(int(queries.Add(1)), len(test.statuses)) - 1

					return test.statuses[n]
				case "vm start vm1":
					return test.start
				}

				return nil
			})

			var progress []string

			err := vm.Snapshot(t.Context(), testExp, "vm1", "snap", func(p string) { progress = append(progress, p) })

			switch {
			case test.wantErr == "" && err != nil:
				t.Errorf("Snapshot error = %v, want nil", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Errorf("Snapshot error = %v, want one containing %q", err, test.wantErr)
			}

			if n := mmtest.Count(received(), "vm start vm1"); n != 1 {
				t.Errorf("Snapshot sent `vm start vm1` %d times, want 1", n)
			}

			if test.wantProgress != nil {
				if diff := cmp.Diff(test.wantProgress, progress, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("Snapshot progress mismatch (-want +got):\n%s", diff)
				}
			}

			if test.wantErr != "" && !strings.Contains(test.wantErr, "resuming") && len(fake.shells) != 0 {
				t.Errorf("Snapshot shell commands = %q after a failed save, want none", fake.shells)
			}
		})
	}
}
