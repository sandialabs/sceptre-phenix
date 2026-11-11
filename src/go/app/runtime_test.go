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
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/app"
	"phenix/store"
	"phenix/types"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
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

// populateRuntimeCalls returns the functions that add the runtime details to
// an experiment for an external app, by name.
func populateRuntimeCalls() map[string]func(context.Context, *types.Experiment) error {
	return map[string]func(context.Context, *types.Experiment) error{
		"PopulateRuntime":                      app.PopulateRuntime,
		"populateStageRuntime outside a stage": app.PopulateStageRuntime,
		"populateStageRuntime in a stage": func(ctx context.Context, exp *types.Experiment) error {
			return app.PopulateStageRuntime(app.WithStageRuntime(ctx), exp)
		},
	}
}

// PopulateRuntime and populateStageRuntime give an experiment minimega's
// hosts and VMs, or the host query's error with the experiment unchanged.
//
//nolint:paralleltest // replaces mm.DefaultMM
func TestPopulateRuntime(t *testing.T) {
	hostErr := errors.New("minimega is down")

	tests := map[string]struct {
		hostErr error
		want    givenRuntime
	}{
		"minimega answers":     {want: wantRuntime},
		"the host query fails": {hostErr: hostErr},
	}

	for fn, call := range populateRuntimeCalls() {
		for name, tc := range tests {
			t.Run(fn+"/"+name, func(t *testing.T) {
				fake := &runtimeMM{hostErr: tc.hostErr}
				runtimeUseMM(t, fake)

				exp := runningStageExperiment(t)

				err := call(t.Context(), exp)
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

// observedMM answers the runtime queries of a request: hosts, or hostErr,
// and the VMs, which minimega did not report in full when vmErr is set.
type observedMM struct {
	mm.MM

	hostErr  error
	vmErr    error
	unknown  bool
	schedule []bool
}

func (m *observedMM) GetClusterHosts(schedOnly bool) (mm.Hosts, error) {
	m.schedule = append(m.schedule, schedOnly)

	return mm.Hosts{{Name: "compute-0", Schedulable: true}}, m.hostErr
}

func (m *observedMM) GetVMInfoObserved(...mm.Option) (mm.VMs, freshness.Observation) {
	if m.unknown {
		return mm.VMs{{Name: "router"}}, freshness.Observation{Err: m.vmErr}
	}

	return mm.VMs{{Name: "router", Host: "compute-0"}}, freshness.Observed(time.Now())
}

// PopulateObservedRuntime adds the schedulable hosts and the experiment's VMs,
// and refuses to add a partial list when minimega fails.
func TestPopulateObservedRuntime(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM
	errHosts := errors.New("no cluster hosts")
	errVMs := errors.New("node compute-1 did not answer")

	tests := map[string]struct {
		mm        *observedMM
		wantErr   []error
		wantHosts mm.Hosts
		wantVMs   mm.VMs
	}{
		"minimega answers": {
			mm:        &observedMM{},
			wantHosts: mm.Hosts{{Name: "compute-0", Schedulable: true}},
			wantVMs:   mm.VMs{{Name: "router", Host: "compute-0"}},
		},
		"no hosts":                    {mm: &observedMM{hostErr: errHosts}, wantErr: []error{errHosts}},
		"VMs not reported":            {mm: &observedMM{unknown: true, vmErr: errVMs}, wantErr: []error{app.ErrRuntimeUnknown, errVMs}},
		"VMs not reported, no reason": {mm: &observedMM{unknown: true}, wantErr: []error{app.ErrRuntimeUnknown}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			saved := mm.DefaultMM
			mm.DefaultMM = tc.mm //nolint:reassign // install test double

			t.Cleanup(func() { mm.DefaultMM = saved }) //nolint:reassign // restore test double

			exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})
			exp.Spec.SetExperimentName("exp")

			err := app.PopulateObservedRuntime(t.Context(), exp)
			for _, want := range tc.wantErr {
				if !errors.Is(err, want) {
					t.Errorf("PopulateObservedRuntime() error = %v, want it to wrap %v", err, want)
				}
			}

			if tc.wantErr == nil && err != nil {
				t.Fatalf("PopulateObservedRuntime() error = %v, want nil", err)
			}

			if diff := cmp.Diff(tc.wantHosts, exp.Hosts); diff != "" {
				t.Errorf("PopulateObservedRuntime() hosts mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantVMs, exp.VMs); diff != "" {
				t.Errorf("PopulateObservedRuntime() VMs mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff([]bool{true}, tc.mm.schedule); diff != "" {
				t.Errorf("PopulateObservedRuntime() asked for hosts with schedOnly mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// runtimeMinimega answers the commands of a host read for the headnode "head"
// and its mesh peer "compute1", with their disk usage, and an empty `vm info`.
func runtimeMinimega(cmd mmtest.Command) []*minicli.Response {
	header := []string{"name", "cpus"}

	switch {
	case cmd.Base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular("head", header, []string{"head", "8"})}
	case cmd.Base == "mesh list peers":
		return []*minicli.Response{mmtest.Text("head", "compute1")}
	case cmd.Base == "mesh send all host":
		return []*minicli.Response{mmtest.Tabular("compute1", header, []string{"compute1", "16"})}
	case strings.HasPrefix(cmd.Base, "mesh send all shell "):
		return []*minicli.Response{mmtest.Text("compute1", "0=10% 1=20%\n")}
	case strings.HasPrefix(cmd.Base, "shell "):
		return []*minicli.Response{mmtest.Text("head", "0=30% 1=40%\n")}
	}

	return nil
}

// useRuntimeMinimega points mm.DefaultMM at the fake minimega, which answers
// with handle, and forgets the cluster details read before and after the test.
func useRuntimeMinimega(t *testing.T, handle mmtest.Handler) func() []mmtest.Command {
	t.Helper()

	received := mmtest.Use(t, handle)

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	runtimeUseMM(t, mm.Minimega{})

	return received
}

// givenHost is a host as a user app is given it, by name and disk usage.
type givenHost struct {
	Name      string       `json:"name"`
	DiskUsage mm.DiskUsage `json:"diskusage"`
}

// An external app is given the schedulable hosts, without the headnode, with
// their disk usage.
func TestApplyAppsGivesHostDiskUsage(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	name := "test-runtime-disk"
	out := installUserApps(t, name)
	useRuntimeMinimega(t, runtimeMinimega)
	exp := runningStageExperiment(t, name)

	err := app.ApplyApps(t.Context(), exp, app.Stage(app.ActionPostStart), app.FilterApp(name))
	if err != nil {
		t.Fatalf("ApplyApps(post-start, %s) error = %v", name, err)
	}

	data, err := os.ReadFile(filepath.Join(out, name+".json"))
	if err != nil {
		t.Fatalf("os.ReadFile(what %s was given) error = %v", name, err)
	}

	var given struct {
		Hosts []givenHost `json:"hosts"`
	}

	if err := json.Unmarshal(data, &given); err != nil {
		t.Fatalf("json.Unmarshal(what %s was given) error = %v", name, err)
	}

	want := []givenHost{{Name: "compute1", DiskUsage: mm.DiskUsage{Phenix: 10, Minimega: 20}}}
	if diff := cmp.Diff(want, given.Hosts); diff != "" {
		t.Errorf("hosts given to %s mismatch (-want +got):\n%s", name, diff)
	}
}

// runKey marks the context of one app run.
type runKey struct{}

// contextMM is a minimega that records the runKey value of the context of each
// host read.
type contextMM struct {
	countingMM

	mu   sync.Mutex
	runs []any
}

func (m *contextMM) GetClusterHostsObserved(
	ctx context.Context,
	schedOnly bool,
) (mm.Hosts, freshness.Observation, error) {
	m.mu.Lock()
	m.runs = append(m.runs, ctx.Value(runKey{}))
	m.mu.Unlock()

	hosts, err := m.GetClusterHosts(schedOnly)

	return hosts, freshness.Observed(time.Now()), err
}

// runtimeContextCalls returns the functions that read the runtime for an
// external app named name, by name: populateRuntimeCalls and ApplyApps.
func runtimeContextCalls(name string) map[string]func(context.Context, *types.Experiment) error {
	calls := populateRuntimeCalls()
	calls["ApplyApps"] = func(ctx context.Context, exp *types.Experiment) error {
		return app.ApplyApps(ctx, exp, app.Stage(app.ActionPostStart), app.FilterApp(name))
	}

	return calls
}

// The host read for an external app gets the context of the app's run.
func TestPopulateRuntimePassesItsContext(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM
	name := "test-runtime-context"
	installUserApps(t, name)

	for fn, call := range runtimeContextCalls(name) {
		t.Run(fn, func(t *testing.T) {
			fake := new(contextMM)
			runtimeUseMM(t, fake)

			exp := runningStageExperiment(t, name)

			if err := call(context.WithValue(t.Context(), runKey{}, fn), exp); err != nil {
				t.Fatalf("%s() error = %v", fn, err)
			}

			if diff := cmp.Diff([]any{fn}, fake.runs); diff != "" {
				t.Errorf("%s() host read contexts mismatch (-want +got):\n%s", fn, diff)
			}
		})
	}
}

// With the context of the app's run canceled, the host read for an external
// app fails with the context's error, and no VMs are read. The read can send
// its first command before the shared read sees the cancel.
func TestPopulateRuntimeStopsWithItsContext(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	name := "test-runtime-canceled"
	installUserApps(t, name)

	for fn, call := range runtimeContextCalls(name) {
		t.Run(fn, func(t *testing.T) {
			received := useRuntimeMinimega(t, runtimeMinimega)
			exp := runningStageExperiment(t, name)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := call(ctx, exp); !errors.Is(err, context.Canceled) {
				t.Errorf("%s() with its context canceled error = %v, want %v", fn, err, context.Canceled)
			}

			if got := mmtest.Count(received(), "vm info"); got != 0 {
				t.Errorf("%s() with its context canceled sent %d vm info, want 0", fn, got)
			}

			if diff := cmp.Diff(givenRuntime{}, runtimeOf(exp)); diff != "" {
				t.Errorf("%s() with its context canceled runtime mismatch (-want +got):\n%s", fn, diff)
			}
		})
	}
}
