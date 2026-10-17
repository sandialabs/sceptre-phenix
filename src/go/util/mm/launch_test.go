package mm

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"

	"phenix/util/mm/mmtest"
)

func TestLaunchVMs(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		vms     []string
		answers map[string][]*minicli.Response // by command; anything else succeeds
		want    []string
		wantErr string // pattern the error matches, "" for none
	}{
		"starts the listed VMs in one command": {
			vms:  []string{"vm1", "vm2", "vm3"},
			want: []string{vmLaunchCmd, "vm start vm1,vm2,vm3"},
		},
		"starts every VM without a list": {
			vms:  nil,
			want: []string{vmLaunchCmd, "vm start all"},
		},
		"starts no VM for an empty list": {
			vms:  []string{},
			want: []string{vmLaunchCmd},
		},
		"names the failed VMs of a batch": {
			vms: []string{"vm1", "vm2", "vm3"},
			answers: map[string][]*minicli.Response{
				"vm start vm1,vm2,vm3": {{Host: "head", Error: "unable to start: boom"}},
				vmInfoSummaryCmd: {mmtest.Tabular("head", []string{"name", "state"},
					[]string{"vm1", "RUNNING"},
					[]string{"vm2", "ERROR"},
					[]string{"vm3", "RUNNING"},
					[]string{"other", "ERROR"},
				)},
			},
			want:    []string{vmLaunchCmd, "vm start vm1,vm2,vm3", vmInfoSummaryCmd},
			wantErr: `(?s)^starting VMs vm2 \(of vm1, vm2, vm3\): .*boom`,
		},
		"stops at the first failed batch": {
			vms: []string{"vm1", "all", "vm2"},
			answers: map[string][]*minicli.Response{
				"vm start all": {{Host: "head", Error: "vm not found: all"}},
			},
			want:    []string{vmLaunchCmd, "vm start vm1", "vm start all"},
			wantErr: `(?s)^starting VM all: .*vm not found: all`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				return tc.answers[cmd.Base]
			})

			err := (Minimega{}).LaunchVMs("exp", tc.vms...)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("LaunchVMs(exp, %q) error = %v, want nil", tc.vms, err)
			case tc.wantErr != "" && (err == nil || !regexp.MustCompile(tc.wantErr).MatchString(err.Error())):
				t.Errorf("LaunchVMs(exp, %q) error = %v, want one matching %q", tc.vms, err, tc.wantErr)
			}

			want := make([]mmtest.Command, 0, len(tc.want))
			for _, base := range tc.want {
				want = append(want, mmtest.Command{Namespace: "exp", Base: base})
			}

			if diff := cmp.Diff(want, received(), ignoreRaw); diff != "" {
				t.Errorf("LaunchVMs(exp, %q) sent commands mismatch (-want +got):\n%s", tc.vms, diff)
			}
		})
	}
}

func TestVMStartBatches(t *testing.T) {
	t.Parallel()

	got := vmStartBatches([]string{"a", "b", "all", "c", "d[1-2]", "e,f", "g h", "h"})
	want := [][]string{{"a", "b"}, {"all"}, {"c"}, {"d[1-2]"}, {"e,f"}, {"g h"}, {"h"}}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("vmStartBatches(names to keep apart) mismatch (-want +got):\n%s", diff)
	}

	many := make([]string, maxVMStartBatch+1)
	for i := range many {
		many[i] = fmt.Sprintf("vm%d", i)
	}

	batches := vmStartBatches(many)

	sizes := make([]int, 0, len(batches))
	for _, batch := range batches {
		sizes = append(sizes, len(batch))
	}

	if diff := cmp.Diff([]int{maxVMStartBatch, 1}, sizes); diff != "" {
		t.Errorf("vmStartBatches(%d names) batch sizes mismatch (-want +got):\n%s", len(many), diff)
	}

	long := []string{strings.Repeat("x", maxVMStartTarget-1), "y"}
	if batches := vmStartBatches(long); len(batches) != 2 {
		t.Errorf("vmStartBatches(names longer than a target list together) = %d batches, want 2", len(batches))
	}
}

func TestGetLaunchProgress(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	// As minimega prints `ns queue`: each batch's VM count, its names
	// compressed into ranges, then its VM config.
	for name, tc := range map[string]struct {
		queue    string
		expected int
		want     float64
		wantCmds []string
	}{
		"counts the queued VMs": {
			queue:    "VMs: 2\nNames: vm[1-2]\nVM Type: kvm\n\nMemory: 2048\n\n",
			expected: 4,
			want:     0.5,
			wantCmds: []string{nsQueueCmd},
		},
		"counts every batch by its VM count": {
			queue: "VMs: 50\nNames: vm[1-50]\nVM Type: kvm\n\nMemory: 2048\n\n" +
				"VMs: 25\nNames: ctr[1-20],web,db[1-4]\nVM Type: container\n\nMemory: 512\n\n",
			expected: 100,
			want:     0.75,
			wantCmds: []string{nsQueueCmd},
		},
		"counts the VMs still building once none are queued": {
			queue:    "",
			expected: 4,
			want:     0.25,
			wantCmds: []string{nsQueueCmd, vmInfoSummaryCmd},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch cmd.Base {
				case nsQueueCmd:
					return []*minicli.Response{mmtest.Text("head", tc.queue)}
				case vmInfoSummaryCmd:
					return []*minicli.Response{mmtest.Tabular("head", []string{"state"},
						[]string{"BUILDING"}, []string{"RUNNING"}, []string{"RUNNING"}, []string{"RUNNING"},
					)}
				}

				return nil
			})

			progress, err := (Minimega{}).GetLaunchProgress("exp", tc.expected)
			if err != nil || progress != tc.want {
				t.Errorf("GetLaunchProgress(exp, %d) = %v, %v; want %v, nil", tc.expected, progress, err, tc.want)
			}

			if diff := cmp.Diff(tc.wantCmds, mmtest.Bases(received())); diff != "" {
				t.Errorf("GetLaunchProgress(exp, %d) sent commands mismatch (-want +got):\n%s", tc.expected, diff)
			}
		})
	}
}
