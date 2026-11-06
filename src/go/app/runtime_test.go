package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/app"
	"phenix/types"
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

// runtimeMM is a minimega whose host query fails with hostErr, counting the
// queries made.
type runtimeMM struct {
	countingMM

	hostErr error
}

func (m *runtimeMM) GetClusterHosts(schedOnly bool) (mm.Hosts, error) {
	if m.hostErr != nil {
		m.hostCalls.Add(1)

		return nil, m.hostErr
	}

	return m.countingMM.GetClusterHosts(schedOnly)
}

// runtimeUseMM installs m as mm.DefaultMM for the test.
func runtimeUseMM(t *testing.T, m mm.MM) {
	t.Helper()

	saved := mm.DefaultMM
	mm.DefaultMM = m //nolint:reassign // install test double

	t.Cleanup(func() { mm.DefaultMM = saved }) //nolint:reassign // restore test double
}

// runtimeOf returns the hosts and VMs of exp by name.
func runtimeOf(exp *types.Experiment) givenRuntime {
	got := givenRuntime{}

	for _, h := range exp.Hosts {
		got.Hosts = append(got.Hosts, named{Name: h.Name})
	}

	for _, v := range exp.VMs {
		got.VMs = append(got.VMs, named{Name: v.Name})
	}

	return got
}

// PopulateRuntime and populateStageRuntime give an experiment minimega's
// hosts and VMs, or the host query's error with the experiment unchanged.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestPopulateRuntime(t *testing.T) {
	hostErr := errors.New("minimega is down")

	populate := map[string]func(*types.Experiment) error{
		"PopulateRuntime": app.PopulateRuntime,
		"populateStageRuntime outside a stage": func(exp *types.Experiment) error {
			return app.PopulateStageRuntime(context.Background(), exp)
		},
		"populateStageRuntime in a stage": func(exp *types.Experiment) error {
			return app.PopulateStageRuntime(app.WithStageRuntime(context.Background()), exp)
		},
	}

	tests := map[string]struct {
		hostErr error
		want    givenRuntime
	}{
		"minimega answers":     {want: wantRuntime},
		"the host query fails": {hostErr: hostErr},
	}

	for fn, call := range populate {
		for name, tc := range tests {
			t.Run(fn+"/"+name, func(t *testing.T) {
				fake := &runtimeMM{hostErr: tc.hostErr}
				runtimeUseMM(t, fake)

				exp := runningStageExperiment(t)

				err := call(exp)
				if !errors.Is(err, tc.hostErr) || (tc.hostErr == nil) != (err == nil) {
					t.Fatalf("%s() error = %v, want %v", fn, err, tc.hostErr)
				}

				if err != nil && !strings.Contains(err.Error(), "getting cluster hosts") {
					t.Errorf("%s() error = %q, want it to tell of the cluster hosts", fn, err)
				}

				if diff := cmp.Diff(tc.want, runtimeOf(exp)); diff != "" {
					t.Errorf("%s() runtime mismatch (-want +got):\n%s", fn, diff)
				}

				// the VM query runs only once the hosts are read
				if got, want := fake.vmCalls.Load(), map[bool]int32{true: 1, false: 0}[tc.hostErr == nil]; got != want {
					t.Errorf("%s() made %d VM queries, want %d", fn, got, want)
				}
			})
		}
	}
}

// The apps of one stage, run at once, share one reading of the runtime; a
// failed reading is not kept, so the next app reads again.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestPopulateStageRuntimeReadsOnce(t *testing.T) {
	fake := &runtimeMM{hostErr: errors.New("minimega is down")}
	runtimeUseMM(t, fake)

	ctx := app.WithStageRuntime(context.Background())

	if err := app.PopulateStageRuntime(ctx, runningStageExperiment(t)); err == nil {
		t.Fatal("populateStageRuntime() with the host query failing error = nil, want an error")
	}

	fake.hostErr = nil

	const apps = 8

	exps := make([]*types.Experiment, apps)
	for i := range exps {
		exps[i] = runningStageExperiment(t)
	}

	var wg sync.WaitGroup

	errs := make([]error, apps)

	for i := range apps {
		wg.Add(1)

		go func() {
			defer wg.Done()

			errs[i] = app.PopulateStageRuntime(ctx, exps[i])
		}()
	}

	wg.Wait()

	for i := range apps {
		if errs[i] != nil {
			t.Fatalf("populateStageRuntime() app %d error = %v", i, errs[i])
		}

		if diff := cmp.Diff(wantRuntime, runtimeOf(exps[i])); diff != "" {
			t.Errorf("populateStageRuntime() app %d runtime mismatch (-want +got):\n%s", i, diff)
		}
	}

	// one failed host query, then one reading for every app
	if hosts, vms := fake.hostCalls.Load(), fake.vmCalls.Load(); hosts != 2 || vms != 1 {
		t.Errorf("populateStageRuntime() for %d apps made %d host and %d VM queries, want 2 and 1", apps, hosts, vms)
	}
}
