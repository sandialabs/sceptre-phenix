package job

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// A job that finishes within the drain period completes; one still running
// then is canceled, and the registry starts no more jobs.
func TestShutdownContextDrainsThenCancels(t *testing.T) {
	t.Parallel()

	const drain = 200 * time.Millisecond

	r := newTestRegistry(t, WithLimits(1, 2))

	quick, err := r.Start(Spec{
		Kind: "test", Owner: "alice", Cancelable: true,
		Run: func(ctx context.Context, _ *Run) error {
			select {
			case <-time.After(drain / 4):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatalf("Start(quick job) error = %v", err)
	}

	canceledAfter := make(chan time.Duration, 1)
	began := time.Now()

	slow, err := r.Start(Spec{
		Kind: "test", Owner: "bob", Cancelable: false,
		Run: func(ctx context.Context, _ *Run) error {
			<-ctx.Done()
			canceledAfter <- time.Since(began)

			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("Start(slow job) error = %v", err)
	}

	// waits behind the quick job, alice's one slot
	queued, err := r.Start(Spec{Kind: "test", Owner: "alice", Cancelable: true, Run: outcomes(nil, nil)})
	if err != nil {
		t.Fatalf("Start(queued job) error = %v", err)
	}

	if left := r.ShutdownContext(context.Background(), drain); len(left) != 0 {
		t.Errorf("ShutdownContext(drain %s) left %d jobs running, want none", drain, len(left))
	}

	got := map[string]Status{}
	for name, id := range map[string]string{"quick": quick.ID, "slow": slow.ID, "queued": queued.ID} {
		j, _ := r.Get(id)
		got[name] = j.Status
	}

	want := map[string]Status{"quick": StatusSucceeded, "slow": StatusCanceled, "queued": StatusCanceled}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ShutdownContext(drain %s) job statuses mismatch (-want +got):\n%s", drain, diff)
	}

	if d := <-canceledAfter; d < drain {
		t.Errorf("ShutdownContext(drain %s) canceled the running job after %s, want at least %s", drain, d, drain)
	}

	if _, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: outcomes(nil, nil)}); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("Start() after ShutdownContext error = %v, want %v", err, ErrShuttingDown)
	}
}

// A job that ignores being canceled is returned as still running once ctx
// ends, and ShutdownContext does not wait for it.
func TestShutdownContextGivesUpAtTheDeadline(t *testing.T) {
	t.Parallel()

	const deadline = 150 * time.Millisecond

	r := newTestRegistry(t)
	release := make(chan struct{})

	// before the registry's cleanup, which waits for every job
	t.Cleanup(func() { close(release) })

	stubborn, err := r.Start(Spec{
		Kind: "stubborn", Owner: "alice", Cancelable: true,
		Run: func(context.Context, *Run) error {
			<-release

			return nil
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	began := time.Now()
	left := r.ShutdownContext(ctx, 0)
	took := time.Since(began)

	if took > deadline+time.Second {
		t.Errorf("ShutdownContext(%s deadline) took %s, want about %s", deadline, took, deadline)
	}

	ids := make([]string, len(left))
	for i, j := range left {
		ids[i] = j.ID
	}

	if diff := cmp.Diff([]string{stubborn.ID}, ids); diff != "" {
		t.Errorf("ShutdownContext(%s deadline) jobs left mismatch (-want +got):\n%s", deadline, diff)
	}
}

// ShutdownContext does not wait out the drain period once every job has
// finished, or once ctx ends; a second call returns at once.
func TestShutdownContextReturnsBeforeTheDrainEnds(t *testing.T) {
	t.Parallel()

	const drain = time.Minute

	for name, tc := range map[string]struct {
		// the job ignores being canceled until the test ends
		stubborn bool
		// ctx has ended before ShutdownContext is called
		ended bool
		left  bool
	}{
		"every job finishes":     {},
		"ctx ends while waiting": {stubborn: true, ended: true, left: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newTestRegistry(t)
			release := make(chan struct{})

			// before the registry's cleanup, which waits for every job
			t.Cleanup(func() { close(release) })

			started, err := r.Start(Spec{Kind: "test", Owner: "alice", Cancelable: true, Run: func(ctx context.Context, _ *Run) error {
				if tc.stubborn {
					<-release

					return nil
				}

				<-ctx.Done()

				return ctx.Err()
			}})
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			if !tc.stubborn {
				// finishes the job, as if it ended on its own within the drain
				if _, err := r.Cancel(started.ID); err != nil {
					t.Fatalf("Cancel() error = %v", err)
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			if tc.ended {
				cancel()
			}

			defer cancel()

			began := time.Now()
			left := r.ShutdownContext(ctx, drain)

			if took := time.Since(began); took > drain/2 {
				t.Errorf("ShutdownContext(drain %s) took %s, want it to return early", drain, took)
			}

			if (len(left) != 0) != tc.left {
				t.Errorf("ShutdownContext(drain %s) left %d jobs running, want some %t", drain, len(left), tc.left)
			}

			if again := r.ShutdownContext(ctx, 0); len(again) != len(left) {
				t.Errorf("second ShutdownContext() left %d jobs running, want %d as the first", len(again), len(left))
			}
		})
	}
}
