package vm_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/vm"
	ifaces "phenix/types/interfaces"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// headMM is minimega as mmtest fakes it, on a cluster whose headnode is "head".
type headMM struct {
	mm.Minimega
}

func (headMM) Headnode() string { return "head" }

func (headMM) IsHeadnode(node string) bool { return node == "head" }

// useMinimega sends minimega commands to a fake minimega that answers with
// handle until the test ends. It returns a function that lists the commands
// received since.
func useMinimega(t *testing.T, handle mmtest.Handler) func() []mmtest.Command {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = headMM{} //nolint:reassign // install test double

	return mmtest.Use(t, handle)
}

// checkSent returns a diff of want and the bases of sent, or an error for a
// command sent in a namespace other than ns.
func checkSent(sent []mmtest.Command, ns string, want []string) error {
	if diff := cmp.Diff(want, mmtest.Bases(sent), cmpopts.EquateEmpty()); diff != "" {
		return fmt.Errorf("minimega commands mismatch (-want +got):\n%s", diff)
	}

	for _, cmd := range sent {
		if cmd.Namespace != ns {
			return fmt.Errorf("sent %q in namespace %q, want %q", cmd.Base, cmd.Namespace, ns)
		}
	}

	return nil
}

// stateRow is minimega's answer to a `vm info summary` query of a VM's state.
func stateRow(state string) []*minicli.Response {
	return []*minicli.Response{mmtest.Tabular("compute1", []string{"state"}, []string{state})}
}

// failOn answers every command whose base starts with prefix with a
// minimega error, and every other command with an empty success.
func failOn(prefix string) mmtest.Handler {
	return func(cmd mmtest.Command) []*minicli.Response {
		if prefix != "" && strings.HasPrefix(cmd.Base, prefix) {
			return []*minicli.Response{mmtest.Error("compute1", "minimega failed")}
		}

		return nil
	}
}

// canceled is a context that is already done.
func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	return ctx
}

const (
	systemReset     = `vm qmp vm1 '{ "execute": "system_reset" }'`
	systemPowerdown = `vm qmp vm1 '{ "execute": "system_powerdown" }'`
)

// Pause, Resume and Kill each send the VM's minimega command, and stop at an
// error from minimega.
//
//nolint:paralleltest // replaces minimega
func TestPauseResumeKill(t *testing.T) {
	tests := []struct {
		name    string
		call    func(exp, name string) error
		exp, vm string
		failOn  string
		wantErr bool
		want    []string
	}{
		{name: "Pause stops the VM's captures and the VM", call: vm.Pause, exp: testExp, vm: "vm1",
			want: []string{"capture", "vm stop vm1"}},
		{name: "Pause reports a failed stop", call: vm.Pause, exp: testExp, vm: "vm1", failOn: "vm stop",
			wantErr: true, want: []string{"capture", "vm stop vm1"}},
		{name: "Pause rejects an empty experiment name", call: vm.Pause, vm: "vm1", wantErr: true},
		{name: "Pause rejects an empty VM name", call: vm.Pause, exp: testExp, wantErr: true},
		{name: "Resume starts the VM", call: vm.Resume, exp: testExp, vm: "vm1", want: []string{"vm start vm1"}},
		{name: "Resume reports a failed start", call: vm.Resume, exp: testExp, vm: "vm1", failOn: "vm start",
			wantErr: true, want: []string{"vm start vm1"}},
		{name: "Resume rejects an empty experiment name", call: vm.Resume, vm: "vm1", wantErr: true},
		{name: "Resume rejects an empty VM name", call: vm.Resume, exp: testExp, wantErr: true},
		{name: "Kill kills and flushes the VM", call: vm.Kill, exp: testExp, vm: "vm1",
			want: []string{"vm kill vm1", "vm flush vm1"}},
		{name: "Kill does not flush a VM it could not kill", call: vm.Kill, exp: testExp, vm: "vm1",
			failOn: "vm kill", wantErr: true, want: []string{"vm kill vm1"}},
		{name: "Kill rejects an empty experiment name", call: vm.Kill, vm: "vm1", wantErr: true},
		{name: "Kill rejects an empty VM name", call: vm.Kill, exp: testExp, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := useMinimega(t, failOn(test.failOn))

			err := test.call(test.exp, test.vm)
			if gotErr := err != nil; gotErr != test.wantErr {
				t.Errorf("call(%q, %q) error = %v, want error %t", test.exp, test.vm, err, test.wantErr)
			}

			if err := checkSent(received(), test.exp, test.want); err != nil {
				t.Errorf("call(%q, %q) %v", test.exp, test.vm, err)
			}
		})
	}
}

// Pause stops the packet captures of the VM before it stops the VM, and does
// not stop a VM whose captures it could not stop.
//
//nolint:paralleltest // replaces the store and minimega
func TestPauseStopsCapturesFirst(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("capture delete fails %t", fail), func(t *testing.T) {
			useExperiment(t, startTime, vmNode("vm1"))

			received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch {
				case cmd.Base == "capture":
					return []*minicli.Response{mmtest.Tabular("compute1", []string{"interface", "path"},
						[]string{"vm1:0", testExp + "/files/vm1-0.pcap"})}
				case fail && strings.HasPrefix(cmd.Base, "capture pcap delete"):
					return []*minicli.Response{mmtest.Error("compute1", "capture busy")}
				}

				return nil
			})

			want := []string{"capture", "capture pcap delete vm vm1", "vm stop vm1"}
			if fail {
				want = want[:2]
			}

			err := vm.Pause(testExp, "vm1")
			if gotErr := err != nil; gotErr != fail {
				t.Errorf("Pause(%q, %q) error = %v, want error %t", testExp, "vm1", err, fail)
			}

			if err := checkSent(received(), testExp, want); err != nil {
				t.Errorf("Pause(%q, %q) %v", testExp, "vm1", err)
			}
		})
	}
}

// Restart resets a running VM through QEMU and starts a VM that has quit,
// which QEMU cannot reset.
//
//nolint:paralleltest // replaces minimega
func TestRestart(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context //nolint:containedctx // a case's input
		exp, vm string
		state   string // the state minimega reports, "" for no VM
		failOn  string
		wantErr error
		want    []string
	}{
		{name: "resets a running VM", exp: testExp, vm: "vm1", state: "RUNNING",
			want: []string{"vm info summary", systemReset}},
		{name: "starts a VM that has quit", exp: testExp, vm: "vm1", state: "QUIT",
			want: []string{"vm info summary", "vm start vm1"}},
		{name: "reports a failed reset", exp: testExp, vm: "vm1", state: "RUNNING", failOn: "vm qmp",
			wantErr: errAny, want: []string{"vm info summary", systemReset}},
		{name: "reports a VM minimega does not have", exp: testExp, vm: "vm1",
			wantErr: errAny, want: []string{"vm info summary"}},
		{name: "sends nothing once its context is done", ctx: canceled(), exp: testExp, vm: "vm1",
			state: "RUNNING", wantErr: context.Canceled},
		{name: "rejects an empty experiment name", vm: "vm1", state: "RUNNING", wantErr: errAny},
		{name: "rejects an empty VM name", exp: testExp, state: "RUNNING", wantErr: errAny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == "vm info summary" {
					if test.state == "" {
						return []*minicli.Response{mmtest.Tabular("compute1", []string{"state"})}
					}

					return stateRow(test.state)
				}

				return failOn(test.failOn)(cmd)
			})

			ctx := test.ctx
			if ctx == nil {
				ctx = t.Context()
			}

			err := vm.Restart(ctx, test.exp, test.vm)
			if !errMatches(err, test.wantErr) {
				t.Errorf("Restart(%q, %q) error = %v, want %v", test.exp, test.vm, err, test.wantErr)
			}

			if err := checkSent(received(), test.exp, test.want); err != nil {
				t.Errorf("Restart(%q, %q) %v", test.exp, test.vm, err)
			}
		})
	}
}

// Shutdown asks QEMU to power the VM down and waits for it to quit. It kills a
// VM that QEMU could not power down or that does not quit in time.
//
//nolint:paralleltest // replaces minimega
func TestShutdown(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context //nolint:containedctx // a case's input
		// the states successive state queries report; the last repeats
		states  []string
		failOn  string
		wantErr error
		want    []string
	}{
		{
			name:   "powers the VM down and waits for it to quit",
			states: []string{"RUNNING", "RUNNING", "QUIT"},
			want:   []string{"vm info summary", "capture", systemPowerdown, "vm info summary", "vm info summary"},
		},
		{
			name:   "leaves a VM that has quit",
			states: []string{"QUIT"},
			want:   []string{"vm info summary"},
		},
		{
			name:   "kills a VM that QEMU could not power down",
			states: []string{"RUNNING", "QUIT"},
			failOn: "vm qmp",
			want:   []string{"vm info summary", "capture", systemPowerdown, "vm kill vm1", "vm info summary"},
		},
		{
			name:    "reports a VM it could neither power down nor kill",
			states:  []string{"RUNNING"},
			failOn:  "vm ",
			wantErr: errAny,
			want:    []string{"vm info summary", "capture", systemPowerdown, "vm kill vm1"},
		},
		{
			name:    "sends nothing once its context is done",
			ctx:     canceled(),
			states:  []string{"RUNNING"},
			wantErr: context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var queries atomic.Int32

			received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == "vm info summary" {
					n := min(int(queries.Add(1)), len(test.states)) - 1

					return stateRow(test.states[n])
				}

				return failOn(test.failOn)(cmd)
			})

			ctx := test.ctx
			if ctx == nil {
				ctx = t.Context()
			}

			err := vm.ShutdownWithTiming(ctx, testExp, "vm1", time.Millisecond, time.Minute)
			if !errMatches(err, test.wantErr) {
				t.Errorf("Shutdown(%q, %q) error = %v, want %v", testExp, "vm1", err, test.wantErr)
			}

			if err := checkSent(received(), testExp, test.want); err != nil {
				t.Errorf("Shutdown(%q, %q) %v", testExp, "vm1", err)
			}
		})
	}
}

// Shutdown kills a VM that is still running when the time to power down ends.
//
//nolint:paralleltest // replaces minimega
func TestShutdownKillsAVMThatDoesNotQuit(t *testing.T) {
	received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "vm info summary" {
			return stateRow("RUNNING")
		}

		return nil
	})

	if err := vm.ShutdownWithTiming(t.Context(), testExp, "vm1", time.Millisecond, 20*time.Millisecond); err != nil {
		t.Errorf("Shutdown(%q, %q) error = %v, want nil", testExp, "vm1", err)
	}

	sent := mmtest.Bases(received())

	start := []string{"vm info summary", "capture", systemPowerdown}
	if len(sent) < len(start)+1 || !slices.Equal(sent[:len(start)], start) || sent[len(sent)-1] != "vm kill vm1" {
		t.Fatalf("Shutdown(%q, %q) sent %q, want %q, state queries, then %q", testExp, "vm1", sent, start, "vm kill vm1")
	}

	for _, base := range sent[len(start) : len(sent)-1] {
		if base != "vm info summary" {
			t.Errorf("Shutdown(%q, %q) sent %q while it waited, want only state queries", testExp, "vm1", base)
		}
	}
}

// Shutdown rejects a call without an experiment or VM name.
//
//nolint:paralleltest // replaces minimega
func TestShutdownRejectsEmptyNames(t *testing.T) {
	received := useMinimega(t, nil)

	for _, names := range [][2]string{{"", "vm1"}, {testExp, ""}} {
		if err := vm.Shutdown(t.Context(), names[0], names[1]); err == nil {
			t.Errorf("Shutdown(%q, %q) error = nil, want an error", names[0], names[1])
		}
	}

	if sent := mmtest.Bases(received()); len(sent) != 0 {
		t.Errorf("Shutdown with an empty name sent %q, want nothing", sent)
	}
}

// ResetDiskState kills the VM, replaces its disk with a new snapshot of the
// image it started from (on the VM's node), and starts it again.
//
//nolint:paralleltest // replaces minimega
func TestResetDiskState(t *testing.T) {
	const (
		disk    = "/phenix/images/" + testExp + "_vm1_snapshot"
		tmpSnap = testExp + "_vm1_disk-0.qcow2"
	)

	columns := []string{"host", "name", "id", "state", "disks", "snapshot"}

	tests := []struct {
		name     string
		ctx      context.Context //nolint:containedctx // a case's input
		row      []string        // minimega's vm info row, nil for none
		failOn   string
		wantErr  error
		wantSent func(dst string) []string // dst is the VM's disk on its node
	}{
		{
			name: "kills a running VM and resets its disk on its node",
			row:  []string{"compute1", "vm1", "5", "RUNNING", disk + ",writeback", "true"},
			wantSent: func(dst string) []string {
				return []string{
					"vm info", "capture", "vm kill vm1",
					"mesh send compute1 disk snapshot " + disk + " " + tmpSnap,
					"mesh send compute1 shell mv /phenix/images/" + tmpSnap + " " + dst,
					"vm start vm1",
				}
			},
		},
		{
			name: "resets the disk of a VM that has quit on the headnode",
			row:  []string{"head", "vm1", "5", "QUIT", disk, "true"},
			wantSent: func(dst string) []string {
				// with no mesh prefix, the commands start with a space
				return []string{
					"vm info", " disk snapshot " + disk + " " + tmpSnap,
					" shell mv /phenix/images/" + tmpSnap + " " + dst, "vm start vm1",
				}
			},
		},
		{
			name:     "rejects a VM without a snapshot disk",
			row:      []string{"compute1", "vm1", "5", "RUNNING", disk, "false"},
			wantErr:  errAny,
			wantSent: func(string) []string { return []string{"vm info"} },
		},
		{
			name:     "reports a VM minimega does not have",
			wantErr:  errAny,
			wantSent: func(string) []string { return []string{"vm info"} },
		},
		{
			name:     "does not reset the disk of a VM it could not kill",
			row:      []string{"compute1", "vm1", "5", "RUNNING", disk, "true"},
			failOn:   "vm kill",
			wantErr:  errAny,
			wantSent: func(string) []string { return []string{"vm info", "capture", "vm kill vm1"} },
		},
		{
			name:    "does not move a snapshot it could not make",
			row:     []string{"compute1", "vm1", "5", "QUIT", disk, "true"},
			failOn:  "mesh send compute1 disk snapshot",
			wantErr: errAny,
			wantSent: func(string) []string {
				return []string{"vm info", "mesh send compute1 disk snapshot " + disk + " " + tmpSnap}
			},
		},
		{
			name:     "sends nothing once its context is done",
			ctx:      canceled(),
			row:      []string{"compute1", "vm1", "5", "RUNNING", disk, "true"},
			wantErr:  context.Canceled,
			wantSent: func(string) []string { return nil },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == "vm info" {
					if test.row == nil {
						return []*minicli.Response{mmtest.Tabular("compute1", columns)}
					}

					return []*minicli.Response{mmtest.Tabular(test.row[0], columns, test.row)}
				}

				return failOn(test.failOn)(cmd)
			})

			ctx := test.ctx
			if ctx == nil {
				ctx = t.Context()
			}

			err := vm.ResetDiskState(ctx, testExp, "vm1")
			if !errMatches(err, test.wantErr) {
				t.Errorf("ResetDiskState(%q, %q) error = %v, want %v", testExp, "vm1", err, test.wantErr)
			}

			want := test.wantSent(common.MinimegaBase + "/5/disk-0.qcow2")
			if err := checkSent(received(), testExp, want); err != nil {
				t.Errorf("ResetDiskState(%q, %q) %v", testExp, "vm1", err)
			}
		})
	}
}

// ResetDiskState rejects a call without an experiment or VM name.
//
//nolint:paralleltest // replaces minimega
func TestResetDiskStateRejectsEmptyNames(t *testing.T) {
	received := useMinimega(t, nil)

	for _, names := range [][2]string{{"", "vm1"}, {testExp, ""}} {
		if err := vm.ResetDiskState(t.Context(), names[0], names[1]); err == nil {
			t.Errorf("ResetDiskState(%q, %q) error = nil, want an error", names[0], names[1])
		}
	}

	if sent := mmtest.Bases(received()); len(sent) != 0 {
		t.Errorf("ResetDiskState with an empty name sent %q, want nothing", sent)
	}
}

// injectNode is the topology node of vm1, whose drive gets files injected
// into partition 2.
func injectNode() map[string]any {
	node := vmNode("vm1")
	node["hardware"].(map[string]any)["drives"] = []map[string]any{ //nolint:forcetypeassert // vmNode's hardware
		{"image": "/images/vm1.qc2", "inject_partition": 2},
	}
	node["injections"] = []map[string]any{
		{"src": "/src/a", "dst": "/dst/a"},
		{"src": "/src/b", "dst": "/dst/b"},
	}

	return node
}

// Redeploy relaunches the VM with the options' changes, and with Inject it
// injects the topology's files into a new snapshot of the topology's image.
//
//nolint:paralleltest // replaces the store and minimega
func TestRedeploy(t *testing.T) {
	const snapshot = testExp + "_vm1_snapshot"

	var (
		kill   = []string{"vm info", "vm config clone vm1", "clear vm config state", "vm kill vm1", "vm flush vm1"}
		launch = []string{"vm launch kvm vm1", "vm launch", "vm start vm1"}
	)

	tests := []struct {
		name     string
		exp, vm  string
		opts     []vm.RedeployOption
		disks    string // the VM's disks, as minimega reports them
		noDrives bool   // the topology node has no drives
		noPart   bool   // the topology node's drive sets no inject partition
		wantErr  error
		want     []string
	}{
		{
			name: "relaunches the VM as it was",
			exp:  testExp, vm: "vm1", disks: "/images/vm1.qc2",
			want: slices.Concat(kill, launch),
		},
		{
			name: "relaunches the VM with new CPUs, memory and disk",
			exp:  testExp, vm: "vm1", disks: "/images/vm1.qc2",
			opts: []vm.RedeployOption{vm.CPU(4), vm.Memory(2048), vm.Disk("/images/new.qc2")},
			want: slices.Concat(kill, []string{"vm config vcpus 4", "vm config mem 2048", "vm config disk /images/new.qc2"},
				launch),
		},
		{
			name: "injects the topology's files into a new snapshot of its image",
			exp:  testExp, vm: "vm1", disks: "/phenix/images/" + snapshot + ",writeback",
			opts: []vm.RedeployOption{vm.Inject(true)},
			want: slices.Concat(kill, []string{
				"mesh send all file delete " + snapshot,
				"file delete " + snapshot,
				"disk snapshot /images/vm1.qc2 " + snapshot,
				"disk inject " + snapshot + ":2 files /src/a:/dst/a /src/b:/dst/b",
				"vm config disk /phenix/images/" + snapshot + ",writeback",
			}, launch),
		},
		{
			name: "injects into the partition the options name",
			exp:  testExp, vm: "vm1", disks: "/phenix/images/" + snapshot,
			opts: []vm.RedeployOption{vm.Inject(true), vm.Disk("/images/new.qc2"), vm.InjectPartition(3)},
			want: slices.Concat(kill, []string{
				"mesh send all file delete " + snapshot,
				"file delete " + snapshot,
				"disk snapshot /images/new.qc2 " + snapshot,
				"disk inject " + snapshot + ":3 files /src/a:/dst/a /src/b:/dst/b",
				"vm config disk /phenix/images/" + snapshot,
			}, launch),
		},
		{
			name: "injects into partition 1 for a drive that sets no partition",
			exp:  testExp, vm: "vm1", disks: "/phenix/images/" + snapshot, noPart: true,
			opts: []vm.RedeployOption{vm.Inject(true)},
			want: slices.Concat(kill, []string{
				"mesh send all file delete " + snapshot,
				"file delete " + snapshot,
				"disk snapshot /images/vm1.qc2 " + snapshot,
				"disk inject " + snapshot + ":1 files /src/a:/dst/a /src/b:/dst/b",
				"vm config disk /phenix/images/" + snapshot,
			}, launch),
		},
		{
			name: "relaunches the VM with a disk spec that sets an interface and a cache mode",
			exp:  testExp, vm: "vm1", disks: "/images/vm1.qc2,ide",
			opts: []vm.RedeployOption{vm.Disk("/images/new.qc2,virtio,none")},
			want: slices.Concat(kill, []string{"vm config disk /images/new.qc2,virtio,none"}, launch),
		},
		{
			name: "injects into a new snapshot of the path of a disk spec",
			exp:  testExp, vm: "vm1", disks: "/phenix/images/" + snapshot + ",ide",
			opts: []vm.RedeployOption{vm.Inject(true), vm.Disk("/images/new.qc2,virtio")},
			want: slices.Concat(kill, []string{
				"mesh send all file delete " + snapshot,
				"file delete " + snapshot,
				"disk snapshot /images/new.qc2 " + snapshot,
				"disk inject " + snapshot + ":1 files /src/a:/dst/a /src/b:/dst/b",
				"vm config disk /phenix/images/" + snapshot + ",virtio",
			}, launch),
		},
		{
			name: "rejects a disk minimega would split",
			exp:  testExp, vm: "vm1", disks: "/images/vm1.qc2",
			opts:    []vm.RedeployOption{vm.Disk("/images/my disk.qc2")},
			wantErr: vm.ErrInvalidDisk,
		},
		{
			name: "rejects a disk spec minimega would not parse",
			exp:  testExp, vm: "vm1", disks: "/images/vm1.qc2",
			opts:    []vm.RedeployOption{vm.Disk("/images/new.qc2,bogus")},
			wantErr: mm.ErrMalformedDiskSpec,
		},
		{
			name: "rejects a disk spec whose path minimega would expand",
			exp:  testExp, vm: "vm1", disks: "/images/vm1.qc2",
			opts:    []vm.RedeployOption{vm.Disk("/images/$HOME.qc2,virtio")},
			wantErr: vm.ErrInvalidDisk,
		},
		{
			name: "rejects injections into a node with no drives",
			exp:  testExp, vm: "vm1", disks: "/images/vm1.qc2", noDrives: true,
			opts:    []vm.RedeployOption{vm.Inject(true)},
			wantErr: errAny,
		},
		{
			name: "reports an experiment the store does not have",
			exp:  "other", vm: "vm1", opts: []vm.RedeployOption{vm.Inject(true)},
			wantErr: errAny,
		},
		{name: "rejects an empty experiment name", vm: "vm1", wantErr: errAny},
		{name: "rejects an empty VM name", exp: testExp, wantErr: errAny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := injectNode()
			if test.noDrives {
				delete(node["hardware"].(map[string]any), "drives") //nolint:forcetypeassert // vmNode's hardware
			}

			if test.noPart {
				hardware := node["hardware"].(map[string]any) //nolint:forcetypeassert // vmNode's hardware
				hardware["drives"] = []map[string]any{{"image": "/images/vm1.qc2"}}
			}

			useExperiment(t, startTime, node)

			received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == "vm info" {
					return []*minicli.Response{
						mmtest.Tabular("compute1", []string{"name", "type", "disks"}, []string{"vm1", "kvm", test.disks}),
					}
				}

				return nil
			})

			err := vm.Redeploy(t.Context(), test.exp, test.vm, test.opts...)
			if !errMatches(err, test.wantErr) {
				t.Errorf("Redeploy(%q, %q) error = %v, want %v", test.exp, test.vm, err, test.wantErr)
			}

			sent := received()

			if diff := cmp.Diff(test.want, mmtest.Bases(sent), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Redeploy(%q, %q) minimega commands mismatch (-want +got):\n%s", test.exp, test.vm, diff)
			}
		})
	}
}

// ChangeOpticalDisc and EjectOpticalDisc send the VM's cdrom command, and
// ChangeOpticalDisc rejects a path minimega would not take as one argument.
//
//nolint:paralleltest // replaces minimega
func TestOpticalDisc(t *testing.T) {
	change := func(iso string) func(context.Context, string, string) error {
		return func(ctx context.Context, exp, name string) error { return vm.ChangeOpticalDisc(ctx, exp, name, iso) }
	}

	tests := []struct {
		name    string
		call    func(ctx context.Context, exp, name string) error
		ctx     context.Context //nolint:containedctx // a case's input
		exp, vm string
		failOn  string
		wantErr error
		want    []string
	}{
		{name: "changes the disc", call: change("/isos/tools.iso"), exp: testExp, vm: "vm1",
			want: []string{"vm cdrom change vm1 /isos/tools.iso"}},
		{name: "reports a failed change", call: change("/isos/tools.iso"), exp: testExp, vm: "vm1",
			failOn: "vm cdrom", wantErr: errAny, want: []string{"vm cdrom change vm1 /isos/tools.iso"}},
		{name: "rejects a disc path with a space", call: change("/isos/my tools.iso"), exp: testExp, vm: "vm1",
			wantErr: vm.ErrInvalidDisk},
		{name: "rejects a disc path minimega would expand", call: change("/isos/$HOME.iso"), exp: testExp,
			vm: "vm1", wantErr: vm.ErrInvalidDisk},
		{name: "rejects an empty disc path", call: change(""), exp: testExp, vm: "vm1", wantErr: vm.ErrNoDiscPath},
		{name: "rejects a disc path that is a disk spec", call: change("/isos/tools.iso,ide"), exp: testExp,
			vm: "vm1", wantErr: vm.ErrInvalidDisk},
		{name: "change rejects an empty experiment name", call: change("/isos/tools.iso"), vm: "vm1",
			wantErr: errAny},
		{name: "change rejects an empty VM name", call: change("/isos/tools.iso"), exp: testExp,
			wantErr: errAny},
		{name: "change sends nothing once its context is done", call: change("/isos/tools.iso"), ctx: canceled(),
			exp: testExp, vm: "vm1", wantErr: context.Canceled},
		{name: "ejects the disc", call: vm.EjectOpticalDisc, exp: testExp, vm: "vm1",
			want: []string{"vm cdrom eject vm1"}},
		{name: "reports a failed eject", call: vm.EjectOpticalDisc, exp: testExp, vm: "vm1", failOn: "vm cdrom",
			wantErr: errAny, want: []string{"vm cdrom eject vm1"}},
		{name: "eject rejects an empty experiment name", call: vm.EjectOpticalDisc, vm: "vm1", wantErr: errAny},
		{name: "eject rejects an empty VM name", call: vm.EjectOpticalDisc, exp: testExp, wantErr: errAny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := useMinimega(t, failOn(test.failOn))

			ctx := test.ctx
			if ctx == nil {
				ctx = t.Context()
			}

			err := test.call(ctx, test.exp, test.vm)
			if !errMatches(err, test.wantErr) {
				t.Errorf("call(%q, %q) error = %v, want %v", test.exp, test.vm, err, test.wantErr)
			}

			if err := checkSent(received(), test.exp, test.want); err != nil {
				t.Errorf("call(%q, %q) %v", test.exp, test.vm, err)
			}
		})
	}
}

// Screenshot returns the image minimega takes of the VM, and reports a VM
// that minimega does not have.
//
//nolint:paralleltest // replaces minimega
func TestScreenshot(t *testing.T) {
	tests := []struct {
		name    string
		resp    *minicli.Response
		want    []byte
		wantErr error
	}{
		{
			name: "returns the decoded image",
			resp: &minicli.Response{Host: "compute1", Data: base64.StdEncoding.EncodeToString([]byte("png"))},
			want: []byte("png"),
		},
		{
			name:    "reports a VM minimega does not have",
			resp:    mmtest.Error("compute1", "vm not found: vm1"),
			wantErr: mm.ErrVMNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := useMinimega(t, func(mmtest.Command) []*minicli.Response {
				return []*minicli.Response{test.resp}
			})

			got, err := vm.Screenshot(testExp, "vm1", "215")
			if !errMatches(err, test.wantErr) {
				t.Errorf("Screenshot(%q, %q, %q) error = %v, want %v", testExp, "vm1", "215", err, test.wantErr)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Screenshot(%q, %q, %q) mismatch (-want +got):\n%s", testExp, "vm1", "215", diff)
			}

			if err := checkSent(received(), testExp, []string{"vm screenshot vm1 file /dev/null 215"}); err != nil {
				t.Errorf("Screenshot(%q, %q, %q) %v", testExp, "vm1", "215", err)
			}
		})
	}
}

// Count counts the nodes of the experiment's topology.
//
//nolint:paralleltest // replaces the store
func TestCount(t *testing.T) {
	useExperiment(t, "", vmNode("vm1"), vmNode("vm2"))

	if got, err := vm.Count(testExp); got != 2 || err != nil {
		t.Errorf("Count(%q) = %d, %v, want 2, nil", testExp, got, err)
	}

	for _, name := range []string{"", "other"} {
		if got, err := vm.Count(name); err == nil {
			t.Errorf("Count(%q) = %d, nil, want an error", name, got)
		}
	}
}

// partitionDrive is a topology drive that sets the inject partition part, or
// no partition when part is nil. Its other methods panic.
type partitionDrive struct {
	ifaces.NodeDrive

	part *int
}

func (d partitionDrive) InjectPartition() *int { return d.part }

// Redeploy and the VM list inject into partition 1 of a drive that sets no
// inject partition, as experiment start does, and do not dereference nil.
func TestInjectPartitionOf(t *testing.T) {
	t.Parallel()

	three := 3

	tests := []struct {
		name  string
		drive ifaces.NodeDrive
		want  int
	}{
		{name: "uses the drive's partition", drive: partitionDrive{part: &three}, want: 3},
		{name: "uses partition 1 for a drive that sets none", drive: partitionDrive{}, want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := vm.InjectPartitionOf(test.drive); got != test.want {
				t.Errorf("InjectPartitionOf(drive) = %d, want %d", got, test.want)
			}
		})
	}
}
