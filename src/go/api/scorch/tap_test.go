//nolint:testpackage // testing internals
package scorch

import (
	"context"
	"errors"
	"testing"

	"phenix/api/scorch/scorchmd"
	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/tap"
)

type fakeTapMM struct {
	mm.Minimega

	tapVLANCalls int
}

func (f *fakeTapMM) TapVLAN(opts ...mm.TapOption) error {
	f.tapVLANCalls++

	return nil
}

func (*fakeTapMM) MeshShell(_, _ string) error {
	return nil
}

func (fakeTapMM) Headnode() string {
	return "headnode"
}

func (fakeTapMM) IsHeadnode(node string) bool {
	return node == "headnode"
}

type fakeStore struct {
	store.Store
}

func (fakeStore) Get(*store.Config) error {
	return nil
}

func (fakeStore) Update(*store.Config) error {
	return nil
}

func TestTapTypeAndInit(t *testing.T) {
	var tapComp Tap

	if err := tapComp.Init(); err != nil {
		t.Fatalf("unexpected error initializing Tap: %v", err)
	}

	if tapComp.Type() != "tap" {
		t.Fatalf("expected type 'tap', got '%s'", tapComp.Type())
	}

	if err := tapComp.Configure(context.Background()); err != nil {
		t.Fatalf("expected Configure to return nil, got: %v", err)
	}
}

func TestTapCleanupNoopWhenStatusMissing(t *testing.T) {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-cleanup-test"})
	exp.Spec.SetExperimentName("tap-cleanup-test")

	var tapComp Tap
	_ = tapComp.Init(Experiment(*exp), Name("test-tap"))

	if err := tapComp.Cleanup(context.Background()); err != nil {
		t.Fatalf("expected no error cleaning up tap with no recorded status, got: %v", err)
	}
}

func TestTapCleanupNoopWhenTapMissing(t *testing.T) {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-cleanup-test"})
	exp.Spec.SetExperimentName("tap-cleanup-test")

	status := scorchmd.ScorchStatus{
		RunID: 0,
		Taps:  make(map[string]*tap.Tap),
	}
	exp.Status.SetAppStatus("scorch", status)

	var tapComp Tap
	_ = tapComp.Init(Experiment(*exp), Name("test-tap"))

	if err := tapComp.Cleanup(context.Background()); err != nil {
		t.Fatalf("expected no error cleaning up non-existent tap, got: %v", err)
	}
}

func TestTapStopAndCleanupIdempotence(t *testing.T) {
	originalStore := store.DefaultStore
	store.DefaultStore = fakeStore{}                         //nolint:reassign // install test double
	t.Cleanup(func() { store.DefaultStore = originalStore }) //nolint:reassign // restore test double

	originalMM := mm.DefaultMM
	fake := &fakeTapMM{}
	mm.DefaultMM = fake                             //nolint:reassign // install test double
	t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double

	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-cleanup-test"})
	exp.Spec.SetExperimentName("tap-cleanup-test")

	testTap := &tap.Tap{
		Name: "test-tapcomp",
		VLAN: "MGMT",
	}

	status := scorchmd.ScorchStatus{
		RunID: 0,
		Taps: map[string]*tap.Tap{
			"test-tap": testTap,
		},
	}
	exp.Status.SetAppStatus("scorch", status)

	var tapComp Tap
	_ = tapComp.Init(Experiment(*exp), Name("test-tap"))

	// Stop should delete the tap and remove it from status
	if err := tapComp.Stop(context.Background()); err != nil {
		t.Fatalf("unexpected error stopping tap: %v", err)
	}

	if fake.tapVLANCalls != 1 {
		t.Fatalf("expected 1 TapVLAN call, got %d", fake.tapVLANCalls)
	}

	var updatedStatus scorchmd.ScorchStatus
	if err := exp.Status.ParseAppStatus("scorch", &updatedStatus); err != nil {
		t.Fatalf("failed to parse updated status: %v", err)
	}

	if _, exists := updatedStatus.Taps["test-tap"]; exists {
		t.Fatalf("expected 'test-tap' to be removed from status.Taps")
	}

	// Subsequent Cleanup should be a no-op and not call TapVLAN again
	if err := tapComp.Cleanup(context.Background()); err != nil {
		t.Fatalf("unexpected error during idempotent cleanup: %v", err)
	}

	if fake.tapVLANCalls != 1 {
		t.Fatalf("expected TapVLAN calls to remain 1, got %d", fake.tapVLANCalls)
	}
}

func TestTapCleanupDirectly(t *testing.T) {
	originalStore := store.DefaultStore
	store.DefaultStore = fakeStore{}                         //nolint:reassign // install test double
	t.Cleanup(func() { store.DefaultStore = originalStore }) //nolint:reassign // restore test double

	originalMM := mm.DefaultMM
	fake := &fakeTapMM{}
	mm.DefaultMM = fake                             //nolint:reassign // install test double
	t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double

	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-cleanup-test"})
	exp.Spec.SetExperimentName("tap-cleanup-test")

	testTap := &tap.Tap{
		Name: "test-tapcomp",
		VLAN: "MGMT",
	}

	status := scorchmd.ScorchStatus{
		RunID: 0,
		Taps: map[string]*tap.Tap{
			"test-tap": testTap,
		},
	}
	exp.Status.SetAppStatus("scorch", status)

	var tapComp Tap
	_ = tapComp.Init(Experiment(*exp), Name("test-tap"))

	// Cleanup called directly (without Stop) should delete the tap
	if err := tapComp.Cleanup(context.Background()); err != nil {
		t.Fatalf("unexpected error cleaning up tap: %v", err)
	}

	if fake.tapVLANCalls != 1 {
		t.Fatalf("expected 1 TapVLAN call, got %d", fake.tapVLANCalls)
	}

	var updatedStatus scorchmd.ScorchStatus
	if err := exp.Status.ParseAppStatus("scorch", &updatedStatus); err != nil {
		t.Fatalf("failed to parse updated status: %v", err)
	}

	if _, exists := updatedStatus.Taps["test-tap"]; exists {
		t.Fatalf("expected 'test-tap' to be removed from status.Taps")
	}
}

// errListStore is a store that cannot list configs.
type errListStore struct {
	store.Store
}

var errTapList = errors.New("list failed")

func (errListStore) List(...string) (store.Configs, error) { return nil, errTapList }

// discoverUsedPairs finds no tap subnets when it cannot list the experiments.
//
//nolint:paralleltest // replaces the store
func TestDiscoverUsedPairsListError(t *testing.T) {
	original := store.DefaultStore
	store.DefaultStore = errListStore{}                 //nolint:reassign // install test double
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	if pairs := discoverUsedPairs(); pairs != nil {
		t.Errorf("discoverUsedPairs() = %v, want nil", pairs)
	}
}

// A tap component fails to clean up when minimega cannot delete its tap.
//
//nolint:paralleltest // replaces minimega
func TestTapCleanupDeleteError(t *testing.T) {
	errTap := errors.New("tap delete failed")
	useBuiltinTapMM(t, errTap)

	exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})
	exp.Spec.SetExperimentName("exp")
	exp.Status.SetAppStatus("scorch", scorchmd.ScorchStatus{Taps: map[string]*tap.Tap{"tap": {Name: "t", VLAN: "MGMT"}}})

	var tapComp Tap
	_ = tapComp.Init(Experiment(*exp), Name("tap"))

	if err := tapComp.Cleanup(context.Background()); !errors.Is(err, errTap) {
		t.Errorf("Tap.Cleanup() error = %v, want %v", err, errTap)
	}
}
