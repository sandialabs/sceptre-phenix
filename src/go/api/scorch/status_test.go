package scorch

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/scorch/scorchmd"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/tap"
)

// Each execution of a component type gets a component of its own, so that
// concurrent runs do not share the options that Init keeps.
func TestNewComponentReturnsFreshInstances(t *testing.T) { //nolint:paralleltest // reads the registry tests change
	for _, typ := range append(BuiltinComponents(), "user-shell", "phenix-scorch-component-x") {
		first, second := newComponent(typ), newComponent(typ)

		if first == second {
			t.Errorf("newComponent(%s) returned the same component %p twice, want two", typ, first)
		}
	}

	if got := newComponent("phenix-scorch-component-x").Type(); got != newComponent("user-shell").Type() {
		t.Errorf("newComponent(an unknown type) type = %q, want the user-shell type", got)
	}
}

// statusStored returns experiment exp as the store holds it.
func statusStored(t *testing.T) *types.Experiment {
	t.Helper()

	exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})
	if err := exp.Reload(); err != nil {
		t.Fatalf("Reload() of exp error = %v", err)
	}

	return exp
}

// statusTaps returns the names of the SCORCH taps that exp holds, sorted.
func statusTaps(t *testing.T, exp *types.Experiment) []string {
	t.Helper()

	var status scorchmd.ScorchStatus
	if err := exp.Status.ParseAppStatus("scorch", &status); err != nil {
		t.Fatalf("ParseAppStatus(scorch) error = %v", err)
	}

	return slices.Sorted(maps.Keys(status.Taps))
}

// statusSOHStored returns the SoH app status in the store.
func statusSOHStored(t *testing.T) map[string]any {
	t.Helper()

	var status map[string]any

	_ = statusStored(t).Status.ParseAppStatus("soh", &status)

	return status
}

// The tap and break components record their taps in the store without
// overwriting what was stored after the run took its copy, such as SoH
// results, and in the status that the run's other components share.
func TestTapsKeepTheStoredStatus(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	storetest.UseBolt(t)
	useBuiltinTapMM(t, nil)

	run := builtinExperiment()
	run.Status.SetAppStatus("scorch", scorchmd.ScorchStatus{RunID: 4})
	builtinStore(t, run)

	later := statusStored(t)
	later.Status.SetAppStatus("soh", map[string]any{"initialized": true})

	if err := later.WriteToStore(true); err != nil {
		t.Fatalf("WriteToStore(true) error = %v", err)
	}

	wantSOH := map[string]any{"initialized": true}

	var tp Tap
	_ = tp.Init(Experiment(*run), Name("tp"), Metadata(scorchmd.ComponentMetadata{"vlan": "EXP"}))

	if err := tp.Start(context.Background()); err != nil {
		t.Fatalf("Tap.Start() error = %v", err)
	}

	var b Break
	_ = b.Init(Experiment(*run), Name("brk"), Metadata(scorchmd.ComponentMetadata{"tap": map[string]any{"vlan": "EXP"}}))

	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("Break.Start() error = %v", err)
	}

	for what, exp := range map[string]*types.Experiment{"stored": statusStored(t), "shared": run} {
		if diff := cmp.Diff([]string{"brk", "tp"}, statusTaps(t, exp)); diff != "" {
			t.Errorf("%s taps after Tap.Start() and Break.Start() mismatch (-want +got):\n%s", what, diff)
		}
	}

	if diff := cmp.Diff(wantSOH, statusSOHStored(t)); diff != "" {
		t.Errorf("stored SoH status after Tap.Start() and Break.Start() mismatch (-want +got):\n%s", diff)
	}

	if err := tp.Stop(context.Background()); err != nil {
		t.Fatalf("Tap.Stop() error = %v", err)
	}

	for what, exp := range map[string]*types.Experiment{"stored": statusStored(t), "shared": run} {
		if diff := cmp.Diff([]string{"brk"}, statusTaps(t, exp)); diff != "" {
			t.Errorf("%s taps after Tap.Stop() mismatch (-want +got):\n%s", what, diff)
		}
	}

	if diff := cmp.Diff(wantSOH, statusSOHStored(t)); diff != "" {
		t.Errorf("stored SoH status after Tap.Stop() mismatch (-want +got):\n%s", diff)
	}

	var stored scorchmd.ScorchStatus
	if err := statusStored(t).Status.ParseAppStatus("scorch", &stored); err != nil || stored.RunID != 4 {
		t.Errorf("stored SCORCH run ID after the taps = %d (%v), want 4", stored.RunID, err)
	}
}

// setScorchStatus still sets the shared status when the store fails, and
// reports the store error.
func TestSetScorchStatusStoreError(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)

	exp := builtinExperiment()
	shared := exp.Status

	err := setScorchStatus(exp, func(s *scorchmd.ScorchStatus) { s.Taps["tp"] = &tap.Tap{Name: "t"} })
	if !errors.Is(err, store.ErrNotExist) {
		t.Errorf("setScorchStatus() of an experiment not stored error = %v, want %v", err, store.ErrNotExist)
	}

	if exp.Status != shared {
		t.Error("setScorchStatus() replaced the shared status")
	}

	if diff := cmp.Diff([]string{"tp"}, statusTaps(t, exp)); diff != "" {
		t.Errorf("shared taps after setScorchStatus() mismatch (-want +got):\n%s", diff)
	}
}

// getsStore is a store whose Get number n (from 1) calls get when it is set.
type getsStore struct {
	store.Store

	mu  sync.Mutex
	n   int
	get func(n int, c *store.Config) error
}

func (s *getsStore) Get(c *store.Config) error {
	if err := s.Store.Get(c); err != nil {
		return err //nolint:wrapcheck // test double
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.n++

	return s.get(s.n, c)
}

// The SoH component fails when it cannot read the experiment back for the
// results of its checks.
func TestSOHResultsNeedTheExperiment(t *testing.T) { //nolint:paralleltest // replaces the store
	errGone := errors.New("experiment gone")

	st := &getsStore{Store: storetest.NewBolt(t)}
	st.get = func(n int, c *store.Config) error {
		switch n {
		case 2: // ApplyApps finds the app running, so it skips the checks
			c.Status["appRunningStageStatus"] = map[string]any{"soh": true}
		case 3:
			return errGone
		}

		return nil
	}

	storetest.Use(t, st)

	exp := builtinExperiment("soh")
	exp.Status.SetAppStatus("soh", map[string]any{"initialized": true})
	builtinStore(t, exp)

	var s SOH
	_ = s.Init(Experiment(*exp), Name("soh"), Type("soh"))

	if err := s.Start(context.Background()); !errors.Is(err, errGone) {
		t.Errorf("SOH.Start() with the experiment gone after the checks error = %v, want %v", err, errGone)
	}
}
