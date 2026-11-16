package mm

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
)

// LaunchVMs sends only `vm launch`, and reports its failure.
func TestLaunchVMs(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		answers map[string][]*minicli.Response
		wantErr string // pattern the error matches, "" for none
	}{
		"launches the queued VMs": {},
		"reports a failed launch": {
			answers: map[string][]*minicli.Response{vmLaunchCmd: {{Host: "head", Error: "no queued VMs"}}},
			wantErr: `(?s)^launching VMs: .*no queued VMs`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				return tc.answers[cmd.Base]
			})

			err := (Minimega{}).LaunchVMs("exp")

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("LaunchVMs(exp) error = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !regexp.MustCompile(tc.wantErr).MatchString(err.Error())):
				t.Errorf("LaunchVMs(exp) error = %v, want one matching %q", err, tc.wantErr)
			}

			want := []mmtest.Command{{Namespace: "exp", Base: vmLaunchCmd}}
			if diff := cmp.Diff(want, received(), ignoreRaw); diff != "" {
				t.Errorf("LaunchVMs(exp) sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStartLaunchedVMs(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		vms     []string
		answers map[string][]*minicli.Response // by command; anything else succeeds
		want    []string
		wantErr string // pattern the error matches, "" for none
	}{
		"starts the listed VMs in one command": {
			vms:  []string{"vm1", "vm2", "vm3"},
			want: []string{"vm start vm1,vm2,vm3"},
		},
		"starts every VM without a list": {
			vms:  nil,
			want: []string{"vm start all"},
		},
		"starts no VM for an empty list": {
			vms:  []string{},
			want: []string{},
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
			want:    []string{"vm start vm1,vm2,vm3", vmInfoSummaryCmd},
			wantErr: `(?s)^starting VMs vm2 \(of vm1, vm2, vm3\): .*boom`,
		},
		"stops at the first failed batch": {
			vms: []string{"vm1", "all", "vm2"},
			answers: map[string][]*minicli.Response{
				"vm start all": {{Host: "head", Error: "vm not found: all"}},
			},
			want:    []string{"vm start vm1", "vm start all"},
			wantErr: `(?s)^starting VM all: .*vm not found: all`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				return tc.answers[cmd.Base]
			})

			err := (Minimega{}).StartLaunchedVMs("exp", tc.vms...)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("StartLaunchedVMs(exp, %q) error = %v, want nil", tc.vms, err)
			case tc.wantErr != "" && (err == nil || !regexp.MustCompile(tc.wantErr).MatchString(err.Error())):
				t.Errorf("StartLaunchedVMs(exp, %q) error = %v, want one matching %q", tc.vms, err, tc.wantErr)
			}

			want := make([]mmtest.Command, 0, len(tc.want))
			for _, base := range tc.want {
				want = append(want, mmtest.Command{Namespace: "exp", Base: base})
			}

			if diff := cmp.Diff(want, received(), ignoreRaw, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("StartLaunchedVMs(exp, %q) sent commands mismatch (-want +got):\n%s", tc.vms, diff)
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

// genLaunchVMNames draws VM name lists that reach the batch bounds: many short
// names, long names that fill a target list in a few, or a mix with unbatchable ones.
func genLaunchVMNames() *rapid.Generator[[]string] {
	short := rapid.StringMatching(`[a-z][a-z0-9-]{0,7}`)
	long := rapid.Map(rapid.IntRange(maxVMStartTarget/8, maxVMStartTarget/2), func(n int) string {
		return strings.Repeat("x", n)
	})
	unbatchable := rapid.SampledFrom([]string{"", "all", "e,f", "d[1-2]", "g h", "it's"})

	return rapid.Custom(func(t *rapid.T) []string {
		name := rapid.SampledFrom([]*rapid.Generator[string]{short, long, rapid.OneOf(short, long, unbatchable)}).Draw(t, "kind")
		minLen := rapid.SampledFrom([]int{0, maxVMStartBatch}).Draw(t, "minLen")

		return rapid.SliceOfN(name, minLen, 3*maxVMStartBatch).Draw(t, "names")
	})
}

func TestVMStartBatchesProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		names := genLaunchVMNames().Draw(t, "names")
		batches := vmStartBatches(names)

		flat := make([]string, 0, len(names))
		for _, batch := range batches {
			flat = append(flat, batch...)
		}

		if diff := cmp.Diff(names, flat, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("vmStartBatches(names) flattened mismatch (-want +got):\n%s", diff)
		}

		for i, batch := range batches {
			target := strings.Join(batch, ",")

			switch {
			case len(batch) == 0 || len(batch) > maxVMStartBatch:
				t.Fatalf("vmStartBatches(names) batch %d has %d VMs, want 1 to %d", i, len(batch), maxVMStartBatch)
			case len(batch) > 1 && !launchAllBatchable(batch):
				t.Fatalf("vmStartBatches(names) batch %d = %q, want an unbatchable name alone", i, batch)
			case len(batch) > 1 && len(target) >= maxVMStartTarget:
				t.Fatalf("vmStartBatches(names) batch %d target list is %d bytes, want under %d", i, len(target), maxVMStartTarget)
			}

			// a batch is cut short only when the next name would not fit in it
			if i+1 < len(batches) && launchAllBatchable(batch) && batchableVMName(batches[i+1][0]) {
				grown := len(target) + 1 + len(batches[i+1][0])
				if len(batch) < maxVMStartBatch && grown < maxVMStartTarget {
					t.Fatalf("vmStartBatches(names) split batch %d (%d VMs, %d bytes) before %q, which fits",
						i, len(batch), len(target), batches[i+1][0])
				}
			}
		}
	})
}

func launchAllBatchable(names []string) bool {
	for _, name := range names {
		if !batchableVMName(name) {
			return false
		}
	}

	return true
}

// GetLaunchProgress counts the VMs it is given that minimega lists with a process or in a state other than
// BUILDING, on every node. It leaves out delayed VMs it is not given, and reports a node that cannot answer.
func TestGetLaunchProgress(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"name", "state", "pid"}

	for name, tc := range map[string]struct {
		info    []*minicli.Response // the answer to `vm info`, one per node
		vms     []string
		want    int
		wantErr bool
	}{
		"counts a VM with a process": {
			info: []*minicli.Response{mmtest.Tabular("head", header, []string{"a", "BUILDING", "41"}, []string{"b", "BUILDING", "0"})},
			vms:  []string{"a", "b"},
			want: 1,
		},
		"counts a VM in a state other than BUILDING": {
			info: []*minicli.Response{mmtest.Tabular("head", header, []string{"a", "RUNNING", "0"}, []string{"b", "ERROR", "0"})},
			vms:  []string{"a", "b"},
			want: 2,
		},
		"counts every node and leaves out the VMs not given": {
			info: []*minicli.Response{
				mmtest.Tabular("compute1", header, []string{"a", "BUILDING", "7"}, []string{"delayed", "BUILDING", "8"}),
				mmtest.Tabular("compute2", header, []string{"b", "BUILDING", "9"}),
			},
			vms:  []string{"a", "b", "c"},
			want: 2,
		},
		"counts nothing before the launch": {
			info: []*minicli.Response{mmtest.Tabular("head", header)},
			vms:  []string{"a"},
			want: 0,
		},
		"reports a node that cannot answer": {
			info: []*minicli.Response{
				mmtest.Tabular("compute1", header, []string{"a", "BUILDING", "7"}),
				mmtest.Error("compute2", "boom"),
			},
			vms:     []string{"a"},
			wantErr: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == vmInfoCmd {
					return tc.info
				}

				return nil
			})

			got, err := (Minimega{}).GetLaunchProgress(t.Context(), "exp", tc.vms)
			if (err != nil) != tc.wantErr {
				t.Errorf("GetLaunchProgress(exp, %q) error = %v, want error %t", tc.vms, err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("GetLaunchProgress(exp, %q) = %d, want %d", tc.vms, got, tc.want)
			}

			if diff := cmp.Diff([]string{vmInfoCmd}, mmtest.Bases(received())); diff != "" {
				t.Errorf("GetLaunchProgress(exp, %q) sent commands mismatch (-want +got):\n%s", tc.vms, diff)
			}
		})
	}
}
