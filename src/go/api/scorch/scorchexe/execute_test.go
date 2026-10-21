//nolint:testpackage // exercise controller reservation and completion internals
package scorchexe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/store"
	"phenix/types"
)

type fixtureApp struct {
	app.UserApp

	work func(context.Context, *types.Experiment) error
}

func (f *fixtureApp) Running(ctx context.Context, e *types.Experiment) error { return f.work(ctx, e) }
func (f *fixtureApp) RunManaged(ctx context.Context, e *types.Experiment) error {
	return Execute(ctx, e, MustRunID(ctx))
}

func TestIndependentRunsAndFencedCancellation(t *testing.T) {
	previous := store.DefaultStore
	t.Cleanup(func() {
		store.DefaultStore = previous //nolint:reassign // restore test-isolated store
	})
	if err := store.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "store.db"))); err != nil {
		t.Fatal(err)
	}
	cfg, _ := store.NewConfig("experiment/concurrent")
	cfg.Spec = map[string]any{
		"experimentName": "concurrent",
		"scenario": map[string]any{
			"apps": []any{
				map[string]any{
					"name": "scorch",
					"metadata": map[string]any{
						"runs": []any{
							map[string]any{"name": "setup", "start": []string{"break"}},
							map[string]any{"name": "work", "start": []string{"pause"}},
						},
						"components": []any{
							map[string]any{"name": "break", "type": "break"},
							map[string]any{"name": "pause", "type": "pause"},
						},
					},
				},
			},
		},
	}
	cfg.Status = map[string]any{"startTime": "running"}
	if err := store.Create(cfg); err != nil {
		t.Fatal(err)
	}
	get := func() *types.Experiment {
		t.Helper()
		if err := store.Get(cfg); err != nil {
			t.Fatal(err)
		}
		e, err := types.DecodeExperimentFromConfig(*cfg)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	started := []chan struct{}{make(chan struct{}), make(chan struct{})}
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	work := func(ctx context.Context, e *types.Experiment) error {
		run := MustRunID(ctx)
		if err := State(ctx, e.Metadata.Name, scorchmd.StateWaiting, "start", "break", 0, 0, "breakpoint", ""); err != nil {
			return err
		}
		close(started[run])
		select {
		case <-ctx.Done():
		case <-release[run]:
		}
		if err := State(context.WithoutCancel(ctx), e.Metadata.Name, scorchmd.StateFinalizing, "cleanup", "", 0, 0, "", ""); err != nil {
			return err
		}
		return ctx.Err()
	}
	if err := app.RegisterUserApp("scorch", func() app.App { return &fixtureApp{work: work} }); err != nil {
		t.Fatal(err)
	}
	setup, err := Prepare(app.SetContextTriggerCLI(context.Background()), get(), 0)
	if err != nil {
		t.Fatal(err)
	}
	setupResult := make(chan error, 1)
	go func() { setupResult <- setup.Run() }()
	<-started[0]
	task, err := Prepare(app.SetContextTriggerCLI(context.Background()), get(), 1)
	if err != nil {
		t.Fatal(err)
	}
	workResult := make(chan error, 1)
	go func() { workResult <- task.Run() }()
	<-started[1]
	if _, err := Prepare(app.SetContextTriggerCLI(context.Background()), get(), 0); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate reservation accepted: %v", err)
	}
	s, err := scorchmd.Status(get())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ActiveRuns()) != 2 || s.RunID != -1 {
		t.Fatalf("bad aggregate status: %+v", s)
	}
	if err := RequestCancel("concurrent", 1, "stale"); !errors.Is(err, scorchmd.ErrStaleExecution) {
		t.Fatalf("stale cancel accepted: %v", err)
	}
	if err := RequestCancel("concurrent", 1, task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-workResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not join worker")
	}
	s, _ = scorchmd.Status(get())
	if s.Executions["0"].State != scorchmd.StateWaiting || s.Executions["1"].State != scorchmd.StateCanceled {
		t.Fatalf("cancel leaked across runs: %+v", s)
	}
	close(release[0])
	if err := <-setupResult; err != nil {
		t.Fatal(err)
	}
	s, _ = scorchmd.Status(get())
	if len(s.ActiveRuns()) != 0 || s.Executions[strconv.Itoa(0)].State != scorchmd.StateSucceeded {
		t.Fatalf("bad final status: %+v", s)
	}
	rerun, err := Prepare(app.SetContextTriggerCLI(context.Background()), get(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if rerun.ID == task.ID {
		t.Fatal("execution ID reused")
	}
	if err := RequestCancel("concurrent", 1, task.ID); !errors.Is(err, scorchmd.ErrStaleExecution) {
		t.Fatal("old cancellation affected rerun")
	}
	_ = rerun.finish(context.Canceled)
	rerun.cancel()
	executionsMu.Lock()
	delete(executions, "concurrent/1")
	executionsMu.Unlock()
	verifyInterruptedRecovery(t, get)
}

func verifyInterruptedRecovery(t *testing.T, get func() *types.Experiment) {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if err := scorchmd.Mutate("concurrent", func(_ *types.Experiment, s *scorchmd.ScorchStatus) error {
		s.Executions["0"] = &scorchmd.Execution{
			ID:        "orphan",
			Run:       0,
			State:     scorchmd.StateRunning,
			Owner:     host,
			PID:       2147483647,
			Updated:   time.Now().UTC().Format(time.RFC3339Nano),
			Resources: []string{"*"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile("concurrent"); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(app.SetContextTriggerCLI(context.Background()), get(), 1); !errors.Is(err, ErrResourceConflict) {
		t.Fatal("orphan resources released", err)
	}
	if err := Drain(context.Background(), "concurrent"); err == nil {
		t.Fatal("stop proceeded with interrupted owner")
	}
	if err := Recover("concurrent", 0, "orphan", false); err == nil {
		t.Fatal("recovery lacked cleanup acknowledgement")
	}
	if err := Recover("concurrent", 0, "stale", true); !errors.Is(err, scorchmd.ErrStaleExecution) {
		t.Fatal("stale recovery accepted", err)
	}
	if err := Recover("concurrent", 0, "orphan", true); err != nil {
		t.Fatal(err)
	}
	if err := Drain(context.Background(), "concurrent"); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(app.SetContextTriggerCLI(context.Background()), get(), 1); !errors.Is(err, ErrStopping) {
		t.Fatal("new work admitted during stop", err)
	}
}
