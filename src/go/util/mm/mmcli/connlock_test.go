package mmcli

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// Commands waiting for the shared connection take it in the order they asked,
// background ones (screenshots) after every other.
func TestBackgroundCommandsWaitBehindOthers(t *testing.T) {
	unhold, received := holdShared(t)

	var wg sync.WaitGroup

	queue := func(name string, background bool, waiting [2]int) {
		cmd := command(name)
		cmd.Background = background

		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = ErrorResponse(RunContext(context.Background(), cmd))
		}()

		polltest.Until(t, name+" waiting", func() bool {
			others, bg := shared.waiters()

			return [2]int{others, bg} == waiting
		})
	}

	queue("screenshot1", true, [2]int{0, 1})
	queue("start", false, [2]int{1, 1})
	queue("screenshot2", true, [2]int{1, 2})
	queue("stop", false, [2]int{2, 2})

	unhold()
	wg.Wait()

	want := []string{"hold", "start", "stop", "screenshot1", "screenshot2"}
	if diff := cmp.Diff(want, mmtest.Bases(received())); diff != "" {
		t.Errorf("commands sent mismatch (-want +got):\n%s", diff)
	}
}

// However commands ask, give up and let go, one holds the lock at a time and
// every one that does not give up gets it.
func TestConnLockExcludes(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var (
			lock    connLock
			holders atomic.Int32
			wg      sync.WaitGroup
			waiters = rapid.SliceOfN(rapid.Bool(), 1, 12).Draw(t, "background")
			patient = rapid.SliceOfN(rapid.Bool(), len(waiters), len(waiters)).Draw(t, "patient")
			got     = make([]bool, len(waiters))
		)

		for i, background := range waiters {
			wg.Add(1)

			go func() {
				defer wg.Done()

				ctx := context.Background()

				if !patient[i] {
					var cancel context.CancelFunc

					ctx, cancel = context.WithTimeout(ctx, time.Duration(i)*50*time.Microsecond)
					defer cancel()
				}

				if !lock.acquire(ctx, background) {
					return
				}

				got[i] = true

				if n := holders.Add(1); n != 1 {
					t.Errorf("%d holders of the lock at once, want 1", n)
				}

				time.Sleep(10 * time.Microsecond)
				holders.Add(-1)
				lock.release()
			}()
		}

		wg.Wait()

		for i := range waiters {
			if patient[i] && !got[i] {
				t.Fatalf("waiter %d without a deadline never got the lock", i)
			}
		}

		if others, background := lock.waiters(); others != 0 || background != 0 || lock.held {
			t.Fatalf("after every waiter left: waiting %d and %d, held %t; want none", others, background, lock.held)
		}
	})
}

// A free lock is taken even with a done context; a held one is not waited for
// with a done context, and the waiter leaves no trace.
func TestConnLockAcquireWithADoneContext(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		held       bool
		background bool
		want       bool
	}{
		{name: "free", want: true},
		{name: "free, background", background: true, want: true},
		{name: "held", held: true, want: false},
		{name: "held, background", held: true, background: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var lock connLock

			if tc.held && !lock.acquire(context.Background(), false) {
				t.Fatal("acquire of a free lock = false, want true")
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			if got := lock.acquire(ctx, tc.background); got != tc.want {
				t.Errorf("acquire(done ctx, background %t) = %t, want %t", tc.background, got, tc.want)
			}

			others, background := lock.waiters()
			if others != 0 || background != 0 || !lock.held {
				t.Errorf("after acquire(done ctx): waiting %d and %d, held %t; want none waiting, held", others, background, lock.held)
			}

			lock.release()

			if lock.held {
				t.Error("after release with no one waiting, held = true, want false")
			}
		})
	}
}

// A waiter handed the lock just as its context ends either keeps it or passes
// it on to the next waiter: the lock is never lost. Repeated, as the two
// endings race.
func TestConnLockPassesOnALockHandedAsCtxEnds(t *testing.T) {
	t.Parallel()

	for range 50 {
		var lock connLock

		lock.acquire(context.Background(), false)

		ctx, cancel := context.WithCancel(context.Background())
		quitter := make(chan bool, 1)
		patient := make(chan bool, 1)

		go func() { quitter <- lock.acquire(ctx, false) }()

		polltest.Until(t, "the quitter waiting", func() bool { o, _ := lock.waiters(); return o == 1 })

		go func() { patient <- lock.acquire(context.Background(), true) }()

		polltest.Until(t, "the patient waiter waiting", func() bool { _, b := lock.waiters(); return b == 1 })

		// end the quitter's context while it cannot yet leave the queue, then
		// most likely hand it the lock before it does
		lock.mu.Lock()
		cancel()
		time.Sleep(time.Millisecond)
		lock.mu.Unlock()
		lock.release()

		if <-quitter {
			lock.release()
		}

		if !<-patient {
			t.Fatal("the patient waiter never got the lock")
		}

		lock.release()

		if others, background := lock.waiters(); others != 0 || background != 0 || lock.held {
			t.Fatalf("after every waiter left: waiting %d and %d, held %t; want none", others, background, lock.held)
		}
	}
}
