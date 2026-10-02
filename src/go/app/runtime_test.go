package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

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

		//nolint:gosec // the test app must be executable
		if err := os.WriteFile(filepath.Join(bin, app.UserAppPrefix+name), []byte(script), 0o755); err != nil {
			t.Fatalf("writing user app %s: %v", name, err)
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

// checkRuntimeGiven checks the user app saved an experiment holding the
// runtime countingMM reports.
func checkRuntimeGiven(t *testing.T, out, name string) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(out, name+".json"))
	if err != nil {
		t.Fatalf("reading what %s was given: %v", name, err)
	}

	var given struct {
		Hosts []struct {
			Name string `json:"name"`
		} `json:"hosts"`
		VMs []struct {
			Name string `json:"name"`
		} `json:"vms"`
	}

	if err := json.Unmarshal(data, &given); err != nil {
		t.Fatalf("decoding what %s was given: %v", name, err)
	}

	if len(given.Hosts) != 1 || given.Hosts[0].Name != "compute-0" ||
		len(given.VMs) != 1 || given.VMs[0].Name != "router" {
		t.Fatalf("%s was given %s", name, data)
	}
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
			t.Fatalf("applying apps: %v", err)
		}

		if hosts, vms := counting.hostCalls.Load(), counting.vmCalls.Load(); hosts != int32(run) || vms != int32(run) {
			t.Fatalf("after %d runs of %d apps: %d host and %d VM queries, want %d of each",
				run, len(names), hosts, vms, run)
		}
	}

	for _, name := range names {
		checkRuntimeGiven(t, out, name)
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
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ {
		if err := alone.Running(context.Background(), exp); err != nil {
			t.Fatalf("running %s: %v", name, err)
		}

		if hosts, vms := counting.hostCalls.Load(), counting.vmCalls.Load(); hosts != int32(run) || vms != int32(run) {
			t.Fatalf("after %d runs: %d host and %d VM queries, want %d of each", run, hosts, vms, run)
		}
	}

	checkRuntimeGiven(t, out, name)
}
