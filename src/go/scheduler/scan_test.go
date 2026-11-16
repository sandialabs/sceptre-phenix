package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/store"
	"phenix/types"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/testenv"
)

// scanMeshDisk is the prefix of the command that measures the disk usage of
// every mesh peer.
const scanMeshDisk = "mesh send all shell "

// scanHeadDisk is the prefix of the command that measures the disk usage of
// the headnode.
const scanHeadDisk = "shell "

// scanMesh answers the commands mm.Minimega sends to read a cluster of the
// headnode head and compute nodes compute1 and compute2. The answer to a
// command that starts with hold waits until release is closed.
type scanMesh struct {
	hold    string
	release chan struct{}
	// silent peers do not answer `mesh send all host`
	silent bool
}

func (f scanMesh) reply(cmd mmtest.Command) []*minicli.Response {
	if f.hold != "" && strings.HasPrefix(cmd.Base, f.hold) {
		<-f.release
	}

	header := []string{"name", "cpus", "memtotal", "vms"}

	switch {
	case cmd.Base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular("head", header, []string{"head", "8", "8192", "0"})}
	case cmd.Base == "mesh list peers":
		return []*minicli.Response{mmtest.Text("head", "compute1,compute2")}
	case cmd.Base == "mesh send all host" && f.silent:
		return nil
	case cmd.Base == "mesh send all host":
		return []*minicli.Response{
			mmtest.Tabular("compute1", header, []string{"compute1", "16", "65536", "0"}),
			mmtest.Tabular("compute2", header, []string{"compute2", "16", "65536", "0"}),
		}
	case strings.HasPrefix(cmd.Base, scanMeshDisk):
		return []*minicli.Response{mmtest.Text("compute1", "0=11% 1=12%\n"), mmtest.Text("compute2", "0=21% 1=22%\n")}
	case strings.HasPrefix(cmd.Base, scanHeadDisk):
		return []*minicli.Response{mmtest.Text("head", "0=1% 1=2%\n")}
	}

	return nil
}

// useScanMinimega makes the minimega client, talking to a fake minimega that
// answers as mesh does, the one the schedulers ask until the test ends, and
// returns a function listing the commands the fake received.
func useScanMinimega(t *testing.T, mesh scanMesh) func() []mmtest.Command {
	t.Helper()

	received := mmtest.Use(t, mesh.reply)

	if mesh.release != nil {
		t.Cleanup(func() { close(mesh.release) })
	}

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)
	useClusterMM(t, mm.Minimega{})

	return received
}

// useUserScheduler puts the user scheduler phenix-scheduler-<name>, a shell
// script of the given body, on the PATH until the test ends.
func useUserScheduler(t *testing.T, name, body string) {
	t.Helper()

	testenv.RequireCommands(t, "sh", "cat", "sleep")

	dir := t.TempDir()

	script := filepath.Join(dir, "phenix-scheduler-"+name)

	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("writing user scheduler %s error = %v", name, err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The built-in schedulers read the cluster hosts with no disk usage command.
func TestBuiltinSchedulersReadNoDiskUsage(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for _, name := range []string{"round-robin", "subnet-compute", "isolate-experiment"} {
		t.Run(name, func(t *testing.T) {
			received := useScanMinimega(t, scanMesh{})

			spec := builtinSpec(t, []builtinVM{{name: "vm1", vlan: "EXP", memory: 512}}, nil)
			if _, err := Schedule(t.Context(), name, spec); err != nil {
				t.Fatalf("Schedule(%s) error = %v", name, err)
			}

			cmds := received()

			for prefix, want := range map[string]int{"mesh send all host": 1, scanMeshDisk: 0, scanHeadDisk: 0} {
				if got := mmtest.Count(cmds, prefix); got != want {
					t.Errorf("Schedule(%s) sent %d %q commands, want %d", name, got, prefix, want)
				}
			}
		})
	}
}

// Each scheduler fails with mm.ErrMeshUnavailable, not ErrNoSchedulableHosts,
// when the cluster has mesh peers and none of them answers.
func TestSchedulersWhenThePeersDoNotAnswer(t *testing.T) { //nolint:paralleltest // shares the fake minimega and sets PATH
	useUserScheduler(t, "any", `cat > /dev/null; echo '{}'`)

	for _, name := range []string{"round-robin", "subnet-compute", "isolate-experiment", "any"} {
		t.Run(name, func(t *testing.T) {
			useScanMinimega(t, scanMesh{silent: true})

			_, err := Schedule(t.Context(), name, schedulerSpec(t, nil))
			if !errors.Is(err, mm.ErrMeshUnavailable) || errors.Is(err, ErrNoSchedulableHosts) {
				t.Errorf("Schedule(%s) with silent mesh peers error = %v, want %v", name, err, mm.ErrMeshUnavailable)
			}
		})
	}
}

// A user scheduler gets the hosts with their disk usage, measured with one
// command for the mesh peers, and none for the headnode, which gets no VMs.
func TestUserSchedulerReadsDiskUsage(t *testing.T) { //nolint:paralleltest // shares the fake minimega and sets PATH
	received := useScanMinimega(t, scanMesh{})

	input := filepath.Join(t.TempDir(), "input.json")
	useUserScheduler(t, "disk", `cat > '`+input+`'; echo '{"spec":{"schedules":{"vm1":"compute2"}}}'`)

	spec := schedulerSpec(t, nil)
	if _, err := Schedule(t.Context(), "disk", spec); err != nil {
		t.Fatalf("Schedule(disk) error = %v", err)
	}

	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatalf("reading the user scheduler input error = %v", err)
	}

	var got struct {
		Hosts []struct {
			Name      string       `json:"name"`
			DiskUsage mm.DiskUsage `json:"diskusage"`
		} `json:"hosts"`
	}

	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decoding the user scheduler input %s error = %v", data, err)
	}

	want := map[string]mm.DiskUsage{"compute1": {Phenix: 11, Minimega: 12}, "compute2": {Phenix: 21, Minimega: 22}}
	gotUsage := make(map[string]mm.DiskUsage, len(got.Hosts))

	for _, host := range got.Hosts {
		gotUsage[host.Name] = host.DiskUsage
	}

	if diff := cmp.Diff(want, gotUsage); diff != "" {
		t.Errorf("user scheduler input disk usage by host mismatch (-want +got):\n%s", diff)
	}

	cmds := received()

	for prefix, want := range map[string]int{scanMeshDisk: 1, scanHeadDisk: 0, "mesh send compute": 0} {
		if got := mmtest.Count(cmds, prefix); got != want {
			t.Errorf("Schedule(disk) sent %d %q commands, want %d", got, prefix, want)
		}
	}

	if diff := cmp.Diff(map[string]string{"vm1": "compute2"}, spec.Schedules()); diff != "" {
		t.Errorf("Schedule(disk) schedules mismatch (-want +got):\n%s", diff)
	}
}

// Once its deadline passes, Schedule gives up at once with ErrTimeout, both
// while minimega does not answer and while a user scheduler runs.
func TestScheduleDeadline(t *testing.T) { //nolint:paralleltest // shares the fake minimega and sets PATH
	for name, tc := range map[string]struct {
		mesh      scanMesh
		scheduler string
	}{
		"minimega does not answer": {
			mesh: scanMesh{hold: "mesh send all host", release: make(chan struct{})}, scheduler: "round-robin",
		},
		"the user scheduler does not finish": {scheduler: "slow"},
	} {
		t.Run(name, func(t *testing.T) {
			useScanMinimega(t, tc.mesh)
			useUserScheduler(t, "slow", "sleep 30")

			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()

			start := time.Now()

			_, err := Schedule(ctx, tc.scheduler, schedulerSpec(t, nil))
			if !errors.Is(err, ErrTimeout) {
				t.Errorf("Schedule(%s) after its deadline error = %v, want %v", tc.scheduler, err, ErrTimeout)
			}

			if took := time.Since(start); took > 10*time.Second {
				t.Errorf("Schedule(%s) took %v with a 300ms deadline, want it to give up at once", tc.scheduler, took)
			}
		})
	}
}

// deadlineMM records the deadline of the context it reads the hosts with.
type deadlineMM struct {
	mm.MM

	deadline *time.Time
}

//nolint:unparam // mm's signature
func (m deadlineMM) GetClusterHostsObserved(ctx context.Context, _, _ bool) (mm.Hosts, freshness.Observation, error) {
	*m.deadline, _ = ctx.Deadline()

	return mm.Hosts{{Name: "h1", MemTotal: 4096, CPUs: 4}}, freshness.Observed(time.Now()), nil
}

// Schedule gives each run its limit: the read of the hosts and a user
// scheduler's command get a deadline BuiltinTimeout after the call for a
// built-in scheduler, and UserTimeout after it for a user scheduler.
//
//nolint:paralleltest // replaces mm.DefaultMM and shell.DefaultShell
func TestScheduleAppliesTimeout(t *testing.T) {
	var hostsDeadline time.Time

	useClusterMM(t, deadlineMM{deadline: &hostsDeadline})

	fake := &schedulerFakeShell{commands: []string{"phenix-scheduler-custom"}, out: []byte(`{}`)}
	useSchedulerShell(t, fake)

	for name, timeout := range map[string]time.Duration{"round-robin": BuiltinTimeout, "custom": UserTimeout} {
		start := time.Now()

		if _, err := Schedule(t.Context(), name, schedulerSpec(t, nil)); err != nil {
			t.Fatalf("Schedule(%s) error = %v", name, err)
		}

		end := time.Now()

		deadlines := map[string]time.Time{"cluster hosts": hostsDeadline}
		if name == "custom" {
			deadlines["user scheduler command"] = fake.deadline
		}

		for what, deadline := range deadlines {
			if deadline.Before(start.Add(timeout)) || deadline.After(end.Add(timeout)) {
				t.Errorf("Schedule(%s) %s deadline = %v, want %v after the call", name, what, deadline, timeout)
			}
		}
	}
}

// Schedule refuses a scheduler name that is empty or has a path separator,
// and looks for no command then.
//
//nolint:paralleltest // replaces shell.DefaultShell
func TestScheduleRefusesInvalidNames(t *testing.T) {
	for _, name := range []string{"", "x/../../bin/sh", "/bin/sh", "a\x00b"} {
		fake := &schedulerFakeShell{}
		useSchedulerShell(t, fake)

		if _, err := Schedule(t.Context(), name, schedulerSpec(t, nil)); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Schedule(%q) error = %v, want %v", name, err, ErrInvalidName)
		}

		if len(fake.asked) != 0 {
			t.Errorf("Schedule(%q) looked for commands %q, want none", name, fake.asked)
		}
	}
}

// Schedule keeps what a scheduler sets also for a spec stored without
// schedules, whose Schedules gives a new map on each call.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestScheduleSpecWithoutSchedules(t *testing.T) {
	useClusterMM(t, clusterMM{hosts: mm.Hosts{{Name: "h1", MemTotal: 4096, CPUs: 4}}})

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"topology": map[string]any{"nodes": []map[string]any{{
				"type": "VirtualMachine", "general": map[string]any{"hostname": "vm1"},
			}}},
		},
	})
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig() error = %v", err)
	}

	if _, err := Schedule(t.Context(), "round-robin", exp.Spec); err != nil {
		t.Fatalf("Schedule(round-robin) error = %v", err)
	}

	if diff := cmp.Diff(map[string]string{"vm1": "h1"}, exp.Spec.Schedules()); diff != "" {
		t.Errorf("Schedule(round-robin) schedules of a spec without schedules mismatch (-want +got):\n%s", diff)
	}
}
