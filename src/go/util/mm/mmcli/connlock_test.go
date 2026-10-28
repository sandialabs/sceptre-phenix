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
