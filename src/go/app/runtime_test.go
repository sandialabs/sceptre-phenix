package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/app"
	"phenix/util/mm"
)

// countingMM answers the runtime queries external apps are given, counting
// them.
type countingMM struct {
	mm.MM

	hostCalls atomic.Int32
	vmCalls   atomic.Int32
}

func (m *countingMM) GetClusterHosts(schedOnly bool) (mm.Hosts, error) {
	m.hostCalls.Add(1)

	if !schedOnly {
		panic("apps are given the schedulable hosts")
	}

	return mm.Hosts{{Name: "compute-0", Schedulable: true}}, nil
}

func (m *countingMM) GetVMInfo(...mm.Option) mm.VMs {
	m.vmCalls.Add(1)

	return mm.VMs{{Name: "router", Host: "compute-0"}}
}

// installUserApps puts external apps with the given names on the PATH. Each
// saves the experiment it is given to $PHENIX_TEST_OUT/<name>.json.
func installUserApps(t *testing.T, names ...string) string {
	t.Helper()

	bin, out := t.TempDir(), t.TempDir()

	for _, name := range names {
		script := "#!/bin/sh\ncat > \"$PHENIX_TEST_OUT/" + name + ".json\"\n"

		if err := os.WriteFile(filepath.Join(bin, app.UserAppPrefix+name), []byte(script), 0o755); err != nil {
			t.Fatalf("os.WriteFile(%s%s) error = %v", app.UserAppPrefix, name, err)
		}
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PHENIX_TEST_OUT", out)

	return out
}

// useCountingMM installs a countingMM as mm.DefaultMM for the test.
func useCountingMM(t *testing.T) *countingMM {
	t.Helper()

	counting := new(countingMM)

	saved := mm.DefaultMM
	mm.DefaultMM = counting //nolint:reassign // install test double

	t.Cleanup(func() { mm.DefaultMM = saved }) //nolint:reassign // restore test double

	return counting
}

// named is a host or VM as a user app is given it, by name alone.
type named struct {
	Name string `json:"name"`
}

// givenRuntime is the runtime part of the experiment a user app is given.
type givenRuntime struct {
	Hosts []named `json:"hosts"`
	VMs   []named `json:"vms"`
}

// wantRuntime is the runtime countingMM reports.
var wantRuntime = givenRuntime{ //nolint:gochecknoglobals // shared expectation
	Hosts: []named{{Name: "compute-0"}},
	VMs:   []named{{Name: "router"}},
}

// readGiven returns the runtime of the experiment the user app name saved.
func readGiven(t *testing.T, out, name string) givenRuntime {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(out, name+".json"))
	if err != nil {
		t.Fatalf("os.ReadFile(what %s was given) error = %v", name, err)
	}

	var given givenRuntime
	if err := json.Unmarshal(data, &given); err != nil {
		t.Fatalf("json.Unmarshal(what %s was given) error = %v", name, err)
	}

	return given
}

func TestApplyAppsReadsRuntimeOncePerRun(t *testing.T) {
	names := []string{"test-runtime-first", "test-runtime-second", "test-runtime-third"}
	out := installUserApps(t, names...)
	counting := useCountingMM(t)
	exp := runningStageExperiment(t, names...)

	for run := 1; run <= 2; run++ {
		err := app.ApplyApps(
			context.Background(),
			exp,
			app.Stage(app.ActionPostStart),
			app.FilterApp(names...),
		)
		if err != nil {
			t.Fatalf("ApplyApps(post-start, %q) run %d error = %v", names, run, err)
		}

		if got := counting.hostCalls.Load(); got != int32(run) {
			t.Errorf("host queries after %d ApplyApps(post-start, %d apps) = %d, want %d", run, len(names), got, run)
		}

		if got := counting.vmCalls.Load(); got != int32(run) {
			t.Errorf("VM queries after %d ApplyApps(post-start, %d apps) = %d, want %d", run, len(names), got, run)
		}
	}

	for _, name := range names {
		if diff := cmp.Diff(wantRuntime, readGiven(t, out, name)); diff != "" {
			t.Errorf("runtime given to %s mismatch (-want +got):\n%s", name, diff)
		}
	}
}

// A user app run outside ApplyApps, as PeriodicallyRunApps runs an app's
// running stage, reads the runtime itself each time.
func TestUserAppRunAloneReadsRuntime(t *testing.T) {
	name := "test-runtime-alone"
	out := installUserApps(t, name)
	counting := useCountingMM(t)
	exp := runningStageExperiment(t, name)

	alone := app.GetApp(name)
	if err := alone.Init(app.Name(name)); err != nil {
		t.Fatalf("Init(Name(%s)) error = %v", name, err)
	}

	for run := 1; run <= 2; run++ {
		if err := alone.Running(context.Background(), exp); err != nil {
			t.Fatalf("Running(%s) run %d error = %v", name, run, err)
		}

		if got := counting.hostCalls.Load(); got != int32(run) {
			t.Errorf("host queries after %d Running(%s) = %d, want %d", run, name, got, run)
		}

		if got := counting.vmCalls.Load(); got != int32(run) {
			t.Errorf("VM queries after %d Running(%s) = %d, want %d", run, name, got, run)
		}
	}

	if diff := cmp.Diff(wantRuntime, readGiven(t, out, name)); diff != "" {
		t.Errorf("runtime given to %s mismatch (-want +got):\n%s", name, diff)
	}
}
