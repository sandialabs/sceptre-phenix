package scheduler

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/shell"
)

// clusterMM fakes minimega's schedulable cluster hosts; other mm.MM methods panic.
type clusterMM struct {
	mm.MM

	hosts mm.Hosts
}

func (m clusterMM) GetClusterHosts(bool) (mm.Hosts, error) {
	return append(mm.Hosts(nil), m.hosts...), nil
}

// schedulerSpec is an experiment spec with VMs vm1 and vm2 on VLAN EXP, and
// the given schedules.
func schedulerSpec(t *testing.T, schedules map[string]any) ifaces.ExperimentSpec {
	t.Helper()

	node := func(name string) map[string]any {
		return map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": name},
			"hardware": map[string]any{"vcpus": 1, "memory": 512, "os_type": "linux"},
			"network":  map[string]any{"interfaces": []map[string]any{{"name": "eth0", "vlan": "EXP"}}},
		}
	}

	if schedules == nil {
		schedules = map[string]any{}
	}

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"schedules":      schedules,
			"topology":       map[string]any{"nodes": []map[string]any{node("vm1"), node("vm2")}},
		},
	})
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig() error = %v", err)
	}

	return exp.Spec
}

func TestSchedulersWithoutSchedulableHosts(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM
	head := mm.Host{Name: "head", MemTotal: 4096, CPUs: 4}

	tests := []struct {
		name         string
		hosts        mm.Hosts
		schedules    map[string]any
		wantErr      error
		want         map[string]string
		wantWarnings []string
	}{
		{name: "fails with no hosts", wantErr: ErrNoSchedulableHosts},
		{
			name:  "schedules every VM on the only host",
			hosts: mm.Hosts{head},
			want:  map[string]string{"vm1": "head", "vm2": "head"},
		},
		{
			name:      "needs no host when every VM is scheduled",
			schedules: map[string]any{"vm1": "compute1", "vm2": "compute1"},
			want:      map[string]string{"vm1": "compute1", "vm2": "compute1"},
			wantWarnings: []string{
				"VM vm1 stays on host compute1, which is not a schedulable cluster host",
				"VM vm2 stays on host compute1, which is not a schedulable cluster host",
			},
		},
	}

	for _, name := range []string{"round-robin", "subnet-compute", "isolate-experiment"} {
		for _, test := range tests {
			if name == "isolate-experiment" && test.schedules != nil {
				continue // isolates the experiment again on a free host
			}

			t.Run(name+"/"+test.name, func(t *testing.T) {
				original := mm.DefaultMM
				t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

				mm.DefaultMM = clusterMM{hosts: test.hosts} //nolint:reassign // install test double

				spec := schedulerSpec(t, test.schedules)

				warnings, err := Schedule(t.Context(), name, spec)
				if !errors.Is(err, test.wantErr) || (test.wantErr == nil && err != nil) {
					t.Fatalf("Schedule(%s) error = %v, want %v", name, err, test.wantErr)
				}

				if test.wantErr != nil {
					return
				}

				if diff := cmp.Diff(test.want, spec.Schedules()); diff != "" {
					t.Errorf("Schedule(%s) schedules mismatch (-want +got):\n%s", name, diff)
				}

				if diff := cmp.Diff(test.wantWarnings, warnings, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("Schedule(%s) warnings mismatch (-want +got):\n%s", name, diff)
				}
			})
		}
	}
}

// schedulerFakeShell finds the user schedulers named in commands and answers
// each run with out and err. Other shell.Shell methods panic.
type schedulerFakeShell struct {
	shell.Shell

	commands []string
	out      []byte
	err      error

	mu sync.Mutex
	// the commands looked for
	asked []string
	// the deadline of the last command run's context
	deadline time.Time
}

func (s *schedulerFakeShell) FindCommandsWithPrefix(prefix string) []string {
	var names []string

	for _, c := range s.commands {
		if name, ok := strings.CutPrefix(c, prefix); ok {
			names = append(names, name)
		}
	}

	return names
}

func (s *schedulerFakeShell) CommandExists(name string) bool {
	s.mu.Lock()
	s.asked = append(s.asked, name)
	s.mu.Unlock()

	return slices.Contains(s.commands, name)
}

func (s *schedulerFakeShell) ExecCommand(ctx context.Context, _ ...shell.Option) ([]byte, []byte, error) {
	s.mu.Lock()
	s.deadline, _ = ctx.Deadline()
	s.mu.Unlock()

	return s.out, nil, s.err
}

// useSchedulerShell installs fake as the shell until the test ends.
func useSchedulerShell(t *testing.T, fake *schedulerFakeShell) {
	t.Helper()

	original := shell.DefaultShell
	t.Cleanup(func() { shell.DefaultShell = original }) //nolint:reassign // restore test double

	shell.DefaultShell = fake //nolint:reassign // install test double
}

// List names the built-in schedulers and the user schedulers on the PATH.
//
//nolint:paralleltest // replaces shell.DefaultShell
func TestList(t *testing.T) {
	builtIn := []string{"isolate-experiment", "round-robin", "subnet-compute"}

	for name, tc := range map[string]struct {
		commands []string
		want     []string
	}{
		"built-in only": {want: builtIn},
		"user scheduler": {
			commands: []string{"phenix-scheduler-custom", "phenix-other"},
			want:     append([]string{"custom"}, builtIn...),
		},
	} {
		t.Run(name, func(t *testing.T) {
			useSchedulerShell(t, &schedulerFakeShell{commands: tc.commands})

			got := List()
			slices.Sort(got)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("List() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Schedule runs a scheduler it does not know as the user scheduler
// phenix-scheduler-<name>, and takes the schedules it answers.
//
//nolint:paralleltest // replaces shell.DefaultShell and mm.DefaultMM
func TestScheduleRunsUserSchedulers(t *testing.T) {
	for name, tc := range map[string]struct {
		installed bool
		out       string
		err       error
		fails     bool
		wantErr   error
		want      map[string]string
		warnings  []string
	}{
		"not installed":   {fails: true, wantErr: ErrUserSchedulerNotFound},
		"fails":           {installed: true, err: errors.New("exit status 1"), fails: true},
		"answers no JSON": {installed: true, out: "scheduled", fails: true},
		"schedules the VMs": {
			installed: true, out: `{"spec":{"schedules":{"vm1":"compute1","vm2":"compute2"}}}`,
			want: map[string]string{"vm1": "compute1", "vm2": "compute2"},
		},
		"answers warnings": {
			installed: true, out: `{"spec":{"schedules":{"vm1":"compute1"}},"warnings":["compute1 is busy"]}`,
			want: map[string]string{"vm1": "compute1"}, warnings: []string{"compute1 is busy"},
		},
		// only the schedules and warnings of the answer are taken
		"changes the rest of the spec": {
			installed: true,
			out:       `{"spec":{"experimentName":"other","topology":{"nodes":[]},"schedules":{"vm2":"compute2"}}}`,
			want:      map[string]string{"vm2": "compute2"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

			mm.DefaultMM = clusterMM{hosts: mm.Hosts{{Name: "compute1"}}} //nolint:reassign // install test double

			fake := &schedulerFakeShell{out: []byte(tc.out), err: tc.err}
			if tc.installed {
				fake.commands = []string{"phenix-scheduler-custom"}
			}

			useSchedulerShell(t, fake)

			spec := schedulerSpec(t, nil)
			warnings, err := Schedule(t.Context(), "custom", spec)

			if (err != nil) != tc.fails || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("Schedule(custom) error = %v, want error %t (%v)", err, tc.fails, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, spec.Schedules(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Schedule(custom) schedules mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.warnings, warnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Schedule(custom) warnings mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff([]string{"phenix-scheduler-custom"}, fake.asked); diff != "" {
				t.Errorf("Schedule(custom) looked for commands mismatch (-want +got):\n%s", diff)
			}

			if got := spec.ExperimentName(); got != "exp" {
				t.Errorf("Schedule(custom) experiment name = %q, want %q", got, "exp")
			}

			if got := len(spec.Topology().Nodes()); got != 2 {
				t.Errorf("Schedule(custom) left %d topology nodes, want 2", got)
			}
		})
	}
}

// A user scheduler has the name it is given, and a run whose context was
// canceled fails with an error that wraps both the command's error and the
// cancellation, and that is not ErrTimeout.
//
//nolint:paralleltest // replaces shell.DefaultShell and mm.DefaultMM
func TestUserSchedulerCanceled(t *testing.T) {
	us := new(userScheduler)
	_ = us.Init(Name("custom"))

	if got := us.Name(); got != "custom" {
		t.Errorf("userScheduler.Name() = %q, want %q", got, "custom")
	}

	useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "compute1"}}})

	killed := errors.New("signal: killed")
	useSchedulerShell(t, &schedulerFakeShell{commands: []string{"phenix-scheduler-custom"}, err: killed})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := Schedule(ctx, "custom", schedulerSpec(t, nil))
	if !errors.Is(err, killed) || !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
		t.Errorf("Schedule(custom) with a canceled context error = %v, want %v and %v, not %v",
			err, killed, context.Canceled, ErrTimeout)
	}
}

// Available lists the built-in schedulers, then the user schedulers on the
// PATH, each once and sorted, leaving out a user scheduler a built-in shadows.
//
//nolint:paralleltest // replaces shell.DefaultShell
func TestAvailable(t *testing.T) {
	builtIn := []Info{
		{Name: "isolate-experiment", Builtin: true, Description: descriptions["isolate-experiment"]},
		{Name: "round-robin", Builtin: true, Description: descriptions["round-robin"]},
		{Name: "subnet-compute", Builtin: true, Description: descriptions["subnet-compute"]},
	}

	for name, tc := range map[string]struct {
		commands []string
		want     []Info
	}{
		"built-in only": {want: builtIn},
		"user schedulers": {
			commands: []string{
				"phenix-scheduler-zeta", "phenix-scheduler-alpha", "phenix-scheduler-alpha",
				"phenix-scheduler-round-robin", "phenix-other",
			},
			want: append(slices.Clone(builtIn), Info{Name: "alpha"}, Info{Name: "zeta"}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			useSchedulerShell(t, &schedulerFakeShell{commands: tc.commands})

			if diff := cmp.Diff(tc.want, Available()); diff != "" {
				t.Errorf("Available() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Each built-in scheduler has a description, and only the built-in ones.
func TestDescriptions(t *testing.T) {
	t.Parallel()

	got := slices.Sorted(maps.Keys(descriptions))
	if diff := cmp.Diff(slices.Sorted(maps.Keys(schedulers)), got); diff != "" {
		t.Errorf("described schedulers mismatch (-built-in +described):\n%s", diff)
	}

	for name, text := range descriptions {
		if !strings.HasSuffix(text, ".") || len(text) > 300 {
			t.Errorf("description of %s = %q, want one to three sentences that end in a period", name, text)
		}
	}
}

// Schedule runs a built-in scheduler also when its name has underscores for
// dashes, and looks for no user scheduler then. Other names stay as they are.
//
//nolint:paralleltest // replaces shell.DefaultShell and mm.DefaultMM
func TestScheduleTakesUnderscoredBuiltinNames(t *testing.T) {
	for _, name := range []string{"round_robin", "isolate_experiment", "subnet_compute"} {
		t.Run(name, func(t *testing.T) {
			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

			mm.DefaultMM = clusterMM{hosts: mm.Hosts{{Name: "head", MemTotal: 4096, CPUs: 4}}} //nolint:reassign // install test double

			fake := &schedulerFakeShell{}
			useSchedulerShell(t, fake)

			spec := schedulerSpec(t, nil)
			if _, err := Schedule(t.Context(), name, spec); err != nil {
				t.Fatalf("Schedule(%s) error = %v", name, err)
			}

			want := map[string]string{"vm1": "head", "vm2": "head"}
			if diff := cmp.Diff(want, spec.Schedules()); diff != "" {
				t.Errorf("Schedule(%s) schedules mismatch (-want +got):\n%s", name, diff)
			}

			if len(fake.asked) != 0 {
				t.Errorf("Schedule(%s) looked for user schedulers %q, want none", name, fake.asked)
			}
		})
	}

	t.Run("a user scheduler keeps its underscores", func(t *testing.T) {
		fake := &schedulerFakeShell{}
		useSchedulerShell(t, fake)

		if _, err := Schedule(t.Context(), "my_sched", schedulerSpec(t, nil)); !errors.Is(err, ErrUserSchedulerNotFound) {
			t.Errorf("Schedule(my_sched) error = %v, want %v", err, ErrUserSchedulerNotFound)
		}

		if diff := cmp.Diff([]string{"phenix-scheduler-my_sched"}, fake.asked); diff != "" {
			t.Errorf("Schedule(my_sched) looked for commands mismatch (-want +got):\n%s", diff)
		}
	})
}
