package scorch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/scorch/scorchmd"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/mm"
	"phenix/util/plog/plogtest"
	"phenix/util/tap"
)

// builtinExperiment is experiment exp whose scenario has the named apps.
func builtinExperiment(apps ...string) *types.Experiment {
	scenario := new(v2.ScenarioSpec)
	for _, name := range apps {
		scenario.AppsF = append(scenario.AppsF, &v2.ScenarioApp{NameF: name})
	}

	return &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec:     &v1.ExperimentSpec{ExperimentNameF: "exp", ScenarioF: scenario},
		Status:   new(v1.ExperimentStatus),
	}
}

// builtinStore puts exp in the test's store.
func builtinStore(t *testing.T, exp *types.Experiment) {
	t.Helper()

	err := store.Create(&store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: exp.Metadata,
		Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	})
	if err != nil {
		t.Fatalf("store.Create(%s) error = %v", exp.Metadata.Name, err)
	}
}

// builtinTapMM fakes the minimega tap commands; each fails with err.
type builtinTapMM struct {
	mm.Minimega

	calls int
	err   error
}

func (f *builtinTapMM) TapVLAN(...mm.TapOption) error {
	f.calls++

	return f.err
}

func (*builtinTapMM) Headnode() string { return "headnode" }

// useBuiltinTapMM installs a builtinTapMM whose commands fail with err.
func useBuiltinTapMM(t *testing.T, err error) *builtinTapMM {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	fake := &builtinTapMM{err: err}
	mm.DefaultMM = fake //nolint:reassign // install test double

	return fake
}

// The SoH component fails before it runs the checks when the experiment is not
// stored, the SoH app is not configured or not initialized, or its metadata
// does not decode. When the app already runs, it fails only with failOnError.
// It reads the app status from the store, not from the run's older copy.
//
//nolint:paralleltest // the SoH checks run in background goroutines
func TestSOHGuards(t *testing.T) {
	tests := map[string]struct {
		apps        []string
		initialized bool
		running     bool
		stale       bool // the run's copy has the app neither initialized nor running
		notStored   bool
		meta        scorchmd.ComponentMetadata
		wantErr     bool
	}{
		"not stored":      {apps: []string{"soh"}, initialized: true, notStored: true, wantErr: true},
		"not configured":  {wantErr: true},
		"not initialized": {apps: []string{"soh"}, wantErr: true},
		"a run copy older than the store": {
			apps: []string{"soh"}, initialized: true, running: true, stale: true,
		},
		"bad metadata": {
			apps: []string{"soh"}, initialized: true, running: true,
			meta: scorchmd.ComponentMetadata{"checks": 5}, wantErr: true,
		},
		"already running": {apps: []string{"soh"}, initialized: true, running: true},
		"already running with failOnError": {
			apps: []string{"soh"}, initialized: true, running: true,
			meta: scorchmd.ComponentMetadata{"failOnError": true}, wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			exp := builtinExperiment(tc.apps...)
			if tc.initialized {
				exp.Status.SetAppStatus("soh", map[string]any{"initialized": true})
			}

			exp.Status.SetAppRunning("soh", tc.running)

			storetest.UseBolt(t)

			if !tc.notStored {
				builtinStore(t, exp)
			}

			if tc.stale {
				exp = builtinExperiment(tc.apps...)
			}

			var s SOH
			_ = s.Init(Experiment(*exp), Name("soh"), Type("soh"), Metadata(tc.meta))

			stages := []struct {
				stage Action
				run   func(context.Context) error
			}{
				{ActionConfigure, s.Configure}, {ActionStart, s.Start}, {ActionStop, s.Stop}, {ActionCleanup, s.Cleanup},
			}

			for _, st := range stages {
				if err := st.run(context.Background()); (err != nil) != tc.wantErr {
					t.Errorf("SOH.%s() error = %v, want error %t", st.stage, err, tc.wantErr)
				}
			}
		})
	}
}

// The SoH component does nothing for a canceled context. A background
// component returns at once and its end stage removes it; its context is
// done, so its checks end before they read the store that later tests replace.
//
//nolint:paralleltest // replaces the background component state
func TestSOHContextAndBackground(t *testing.T) {
	var s SOH
	_ = s.Init(Experiment(*builtinExperiment()), Name("soh"))

	if s.Type() != "soh" {
		t.Errorf("SOH.Type() = %q, want soh", s.Type())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("SOH.Start(canceled) error = %v, want %v", err, context.Canceled)
	}

	var bg SOH
	_ = bg.Init(Experiment(*builtinExperiment()), Name("soh-bg"), Background())

	for _, pair := range [][2]func(context.Context) error{{bg.Configure, bg.Cleanup}, {bg.Start, bg.Stop}} {
		if err := pair[0](ctx); err != nil {
			t.Errorf("background SOH stage error = %v, want nil", err)
		}

		if err := pair[1](context.Background()); err != nil {
			t.Errorf("background SOH end stage error = %v, want nil", err)
		}
	}

	for stage, names := range backgrounded {
		if _, ok := names["exp/"+string(stage)+"/soh-bg"]; ok {
			t.Errorf("backgrounded[%s] keeps soh-bg after its end stage", stage)
		}
	}
}

// PauseMetadata.Validate defaults the duration to 10s and keeps a given one.
func TestPauseMetadataValidate(t *testing.T) {
	t.Parallel()

	for given, want := range map[string]string{"": "10s", "2m": "2m"} {
		md := PauseMetadata{Duration: given}
		md.Validate()

		if md.Duration != want {
			t.Errorf("PauseMetadata{Duration: %q}.Validate() duration = %q, want %q", given, md.Duration, want)
		}
	}
}

// The pause component waits its duration, fails the stages it is told to
// fail, and stops early when the context ends.
//
//nolint:paralleltest // replaces the background component state
func TestPause(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := map[string]struct {
		ctx     func(t *testing.T) context.Context
		meta    scorchmd.ComponentMetadata
		failed  []Action
		wantErr error
	}{
		"no wait": {meta: scorchmd.ComponentMetadata{"duration": "0s"}},
		"fail stages": {
			meta:   scorchmd.ComponentMetadata{"duration": "0s", "failStages": []string{"start", "cleanup"}},
			failed: []Action{ActionStart, ActionCleanup},
		},
		"bad duration": {
			meta:   scorchmd.ComponentMetadata{"duration": "soon"},
			failed: []Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup},
		},
		"bad metadata": {
			meta:   scorchmd.ComponentMetadata{"duration": []string{"1s"}},
			failed: []Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup},
		},
		"canceled context": {
			ctx:     func(*testing.T) context.Context { return canceled },
			meta:    scorchmd.ComponentMetadata{"duration": "0s"},
			failed:  []Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup},
			wantErr: context.Canceled,
		},
		"context ends while paused": {
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
				t.Cleanup(cancel)

				return ctx
			},
			meta:    scorchmd.ComponentMetadata{"duration": "1h"},
			failed:  []Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup},
			wantErr: context.DeadlineExceeded,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var p Pause
			_ = p.Init(Experiment(*builtinExperiment()), Name("pause"), Metadata(tc.meta))

			if p.Type() != "pause" {
				t.Errorf("Pause.Type() = %q, want pause", p.Type())
			}

			ctx := context.Background()
			if tc.ctx != nil {
				ctx = tc.ctx(t)
			}

			stages := []struct {
				stage Action
				run   func(context.Context) error
			}{
				{ActionConfigure, p.Configure}, {ActionStart, p.Start}, {ActionStop, p.Stop}, {ActionCleanup, p.Cleanup},
			}

			for _, s := range stages {
				err := s.run(ctx)

				wantFail := false

				for _, f := range tc.failed {
					wantFail = wantFail || f == s.stage
				}

				if (err != nil) != wantFail || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
					t.Errorf("Pause.%s() error = %v, want error %t (%v)", s.stage, err, wantFail, tc.wantErr)
				}
			}
		})
	}
}

// A pause shorter than 1 s waits about its duration, not a full second, and
// logs the pause with its attributes.
//
//nolint:paralleltest // captures the plog records
func TestPauseShortDuration(t *testing.T) {
	logs := plogtest.Capture(t)

	var p Pause
	_ = p.Init(
		Experiment(*builtinExperiment()), Name("nap"), Metadata(scorchmd.ComponentMetadata{"duration": "50ms"}),
		RunID(2), CurrentLoop(1), LoopCount(3),
	)

	start := time.Now()
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Pause.Start() error = %v", err)
	}

	if got := time.Since(start); got < 50*time.Millisecond || got >= 500*time.Millisecond {
		t.Errorf("Pause.Start() with duration 50ms took %v, want 50ms to 500ms", got)
	}

	records := logs.Records(t, plogtest.Message("pausing"))
	if len(records) != 1 {
		t.Fatalf("Pause.Start() logged %d pausing records, want 1: %s", len(records), logs.String())
	}

	want := map[string]any{
		"type": "SCORCH", "exp": "exp", "component": "nap", "stage": "start",
		"run": 2.0, "loop": 1.0, "count": 3.0, "duration": float64(50 * time.Millisecond),
	}
	if diff := cmp.Diff(want, records[0], cmpopts.IgnoreMapEntries(func(k string, _ any) bool {
		_, ok := want[k]
		return !ok
	})); diff != "" {
		t.Errorf("Pause.Start() pausing record mismatch (-want +got):\n%s", diff)
	}
}

// A background pause returns at once, and its end stage cancels it.
//
//nolint:paralleltest // replaces the background component state
func TestPauseInBackground(t *testing.T) {
	var p Pause
	_ = p.Init(
		Experiment(*builtinExperiment()), Name("pause-bg"), Background(),
		Metadata(scorchmd.ComponentMetadata{"duration": "1h"}),
	)

	for _, stages := range [][2]Action{{ActionConfigure, ActionCleanup}, {ActionStart, ActionStop}} {
		begin, end := p.Configure, p.Cleanup
		if stages[0] == ActionStart {
			begin, end = p.Start, p.Stop
		}

		if err := begin(context.Background()); err != nil {
			t.Errorf("background Pause.%s() error = %v, want nil", stages[0], err)
		}

		name := "exp/" + string(stages[0]) + "/pause-bg"
		if _, ok := backgrounded[stages[0]][name]; !ok {
			t.Errorf("after background Pause.%s(), backgrounded[%s] has no %s", stages[0], stages[0], name)
		}

		if err := end(context.Background()); err != nil {
			t.Errorf("background Pause.%s() error = %v, want nil", stages[1], err)
		}

		if _, ok := backgrounded[stages[0]][name]; ok {
			t.Errorf("after Pause.%s(), backgrounded[%s] keeps %s", stages[1], stages[0], name)
		}
	}
}

// A break without a tap runs no terminal outside the CLI and UI; metadata that
// does not decode fails each stage.
//
//nolint:paralleltest // TMPDIR is shared
func TestBreakWithoutTap(t *testing.T) {
	tests := map[string]struct {
		meta    scorchmd.ComponentMetadata
		wantErr bool
	}{
		"shell":        {},
		"README":       {meta: scorchmd.ComponentMetadata{"readme": "# Look around"}},
		"bad metadata": {meta: scorchmd.ComponentMetadata{"tap": "eth0"}, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var b Break
			_ = b.Init(Experiment(*builtinExperiment()), Name("brk"), Metadata(tc.meta))

			if b.Type() != "break" {
				t.Errorf("Break.Type() = %q, want break", b.Type())
			}

			stages := []struct {
				stage Action
				run   func(context.Context) error
			}{
				{ActionConfigure, b.Configure}, {ActionStart, b.Start}, {ActionStop, b.Stop}, {ActionCleanup, b.Cleanup},
			}

			for _, st := range stages {
				if err := st.run(context.Background()); (err != nil) != tc.wantErr {
					t.Errorf("Break.%s() error = %v, want error %t", st.stage, err, tc.wantErr)
				}
			}
		})
	}
}

// A break with a tap records the tap in the SCORCH status, also at the start
// of a run when the status has no taps yet, and deletes the tap when it ends,
// also when it fails after it creates the tap.
//
//nolint:paralleltest // replaces the store and minimega
func TestBreakWithTap(t *testing.T) {
	tests := map[string]struct {
		status    any
		tapErr    error
		wantErr   bool
		wantCalls int
		wantTaps  []string
	}{
		"no taps at run start": {status: map[string]any{"runID": 1}, wantCalls: 2, wantTaps: []string{"brk"}},
		"other taps": {
			status:    scorchmd.ScorchStatus{Taps: map[string]*tap.Tap{"other": {Name: "x"}}},
			wantCalls: 2, wantTaps: []string{"brk", "other"},
		},
		"no SCORCH status": {wantErr: true, wantCalls: 2},
		"tap not created": {
			status: scorchmd.ScorchStatus{}, tapErr: errors.New("no bridge"),
			wantErr: true, wantCalls: 2,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			storetest.UseBolt(t)
			fake := useBuiltinTapMM(t, tc.tapErr)

			exp := builtinExperiment()
			if tc.status != nil {
				exp.Status.SetAppStatus("scorch", tc.status)
			}

			meta := scorchmd.ComponentMetadata{"tap": map[string]any{"vlan": "EXP", "internetAccess": false}}

			var b Break
			_ = b.Init(Experiment(*exp), Name("brk"), Metadata(meta))

			err := b.Start(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Break.Start() error = %v, want error %t", err, tc.wantErr)
			}

			if fake.calls != tc.wantCalls {
				t.Errorf("Break.Start() ran %d tap commands, want %d", fake.calls, tc.wantCalls)
			}

			if tc.wantErr {
				return
			}

			var status scorchmd.ScorchStatus
			if err := exp.Status.ParseAppStatus("scorch", &status); err != nil {
				t.Fatalf("ParseAppStatus(scorch) error = %v", err)
			}

			var got []string
			for name := range status.Taps {
				got = append(got, name)
			}

			if diff := cmp.Diff(tc.wantTaps, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("Break.Start() status taps mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The tap component records the tap it creates in the SCORCH status, and
// fails when its metadata does not decode, the tap is not created, or the
// experiment has no SCORCH status.
//
//nolint:paralleltest // replaces the store and minimega
func TestTapStart(t *testing.T) {
	tests := map[string]struct {
		meta    scorchmd.ComponentMetadata
		status  any
		tapErr  error
		wantErr bool
	}{
		"records the tap": {
			meta:   scorchmd.ComponentMetadata{"vlan": "EXP", "internetAccess": false},
			status: scorchmd.ScorchStatus{},
		},
		"bad metadata":     {meta: scorchmd.ComponentMetadata{"vlan": []string{"EXP"}}, wantErr: true},
		"no SCORCH status": {meta: scorchmd.ComponentMetadata{"vlan": "EXP"}, wantErr: true},
		"tap not created": {
			meta: scorchmd.ComponentMetadata{"vlan": "EXP"}, status: scorchmd.ScorchStatus{},
			tapErr: errors.New("no bridge"), wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			storetest.UseBolt(t)
			useBuiltinTapMM(t, tc.tapErr)

			exp := builtinExperiment()
			if tc.status != nil {
				exp.Status.SetAppStatus("scorch", tc.status)
			}

			var tp Tap
			_ = tp.Init(Experiment(*exp), Name("tp"), Metadata(tc.meta))

			err := tp.Start(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Tap.Start() error = %v, want error %t", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			var status scorchmd.ScorchStatus
			if err := exp.Status.ParseAppStatus("scorch", &status); err != nil {
				t.Fatalf("ParseAppStatus(scorch) error = %v", err)
			}

			if got := status.Taps["tp"]; got == nil || got.VLAN != "EXP" {
				t.Errorf("Tap.Start() status tap tp = %+v, want one on VLAN EXP", got)
			}
		})
	}
}

// discoverUsedPairs collects the tap subnets that the SCORCH and tap apps of
// running experiments record, and skips subnets that do not parse.
//
//nolint:paralleltest // replaces the store
func TestDiscoverUsedPairs(t *testing.T) {
	storetest.UseBolt(t)

	status := map[string]any{
		"startTime": "2026-11-04T00:00:00Z",
		"apps": map[string]any{
			"scorch": map[string]any{"taps": map[string]any{
				"a": map[string]any{"subnet": "10.1.0.0/30"}, "b": map[string]any{"subnet": "bad"},
			}},
			"tap": map[string]any{"taps": []any{map[string]any{"subnet": "10.2.0.0/30"}}},
		},
	}

	err := store.Create(&store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "running"},
		Spec:     map[string]any{"experimentName": "running", "topology": map[string]any{"nodes": []any{}}},
		Status:   status,
	})
	if err != nil {
		t.Fatalf("store.Create(experiment/running) error = %v", err)
	}

	pairs := discoverUsedPairs()

	got := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		got = append(got, pair.String())
	}

	if diff := cmp.Diff(
		[]string{"10.1.0.0/30", "10.2.0.0/30"},
		got,
		cmpopts.SortSlices(func(a, b string) bool { return a < b }),
	); diff != "" {
		t.Errorf("discoverUsedPairs() mismatch (-want +got):\n%s", diff)
	}
}
