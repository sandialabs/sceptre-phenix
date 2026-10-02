package mm

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// fakeCommands answers `cc commands` for the given command IDs, which have a
// response on compute1 once answered is set. The headnode never has one.
type fakeCommands struct {
	ids      []string
	answered atomic.Bool
	polls    atomic.Int32
}

func (f *fakeCommands) reply(cmd mmtest.Command) []*minicli.Response {
	if cmd.Base != "cc commands" {
		return nil
	}

	f.polls.Add(1)

	responses := "0"
	if f.answered.Load() {
		responses = "1"
	}

	header := []string{"id", "responses"}
	head := make([][]string, 0, len(f.ids))
	compute := make([][]string, 0, len(f.ids))

	for _, id := range f.ids {
		head = append(head, []string{id, "0"})
		compute = append(compute, []string{id, responses})
	}

	return []*minicli.Response{mmtest.Tabular("head", header, head...), mmtest.Tabular("compute1", header, compute...)}
}

// useFastPolling shortens the poll interval, and waits for every poller to
// stop once the test is done.
func useFastPolling(t *testing.T, interval time.Duration) {
	t.Helper()

	waitForNoPollers(t)

	saved := responsePollInterval
	responsePollInterval = interval

	t.Cleanup(func() {
		waitForNoPollers(t)

		responsePollInterval = saved
	})
}

// waitForNoPollers waits until every poller has stopped.
func waitForNoPollers(t *testing.T) {
	t.Helper()

	polltest.Until(t, "every poller stopped", func() bool {
		responsePollers.mu.Lock()
		defer responsePollers.mu.Unlock()

		return len(responsePollers.pollers) == 0
	})
}

func waiters(ns string) int {
	responsePollers.mu.Lock()
	defer responsePollers.mu.Unlock()

	if p, ok := responsePollers.pollers[ns]; ok {
		return len(p.waiters)
	}

	return 0
}

func TestWaitForResponseSharesPolling(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	const (
		count    = 40
		interval = 20 * time.Millisecond
	)

	fake := new(fakeCommands)

	for i := range count {
		fake.ids = append(fake.ids, strconv.Itoa(i))
	}

	useFakeMinimega(t, fake.reply)
	useFastPolling(t, interval)

	var (
		wg    sync.WaitGroup
		errs  = make([]error, count)
		start = time.Now()
	)

	for i, id := range fake.ids {
		wg.Add(1)

		go func() {
			defer wg.Done()

			errs[i] = waitForResponse(context.Background(), "exp", id, 10*time.Second)
		}()
	}

	polltest.Until(t, "every caller waits", func() bool { return waiters("exp") == count })

	fake.answered.Store(true)
	wg.Wait()

	elapsed := time.Since(start)

	for i, err := range errs {
		if err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}

	polls := int(fake.polls.Load())

	// at most one poll per interval for all the waiters together
	if limit := int(elapsed/interval) + 2; polls > limit || polls >= count/2 {
		t.Fatalf("%d waiters polled %d times in %v, want at most %d", count, polls, elapsed, limit)
	}

	t.Logf("%d waiters, %d polls in %v", count, polls, elapsed)

	// the poller stops once nothing is waiting
	waitForNoPollers(t)
}

func TestWaitForResponseSingleCommandIsFiltered(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	fake := &fakeCommands{ids: []string{"7"}}
	fake.answered.Store(true)

	received := useFakeMinimega(t, fake.reply)
	useFastPolling(t, time.Millisecond)

	if err := waitForResponse(context.Background(), "exp", "7", time.Second); err != nil {
		t.Fatalf("waitForResponse: %v", err)
	}

	cmds := received()

	want := `.record false namespace "exp" .columns "id","responses" .filter id=7 cc commands`
	if len(cmds) != 1 || cmds[0].Raw != want {
		t.Fatalf("sent %v, want only %q", cmds, want)
	}
}

func TestWaitForResponseTimesOut(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	fake := &fakeCommands{ids: []string{"1", "2"}}

	useFakeMinimega(t, fake.reply)
	useFastPolling(t, 5*time.Millisecond)

	errc := make(chan error, 1)

	// A waiter for another command keeps the poller running meanwhile.
	go func() { errc <- waitForResponse(context.Background(), "exp", "2", time.Minute) }()

	polltest.Until(t, "the other command's waiter waits", func() bool { return waiters("exp") == 1 })

	err := waitForResponse(context.Background(), "exp", "1", 50*time.Millisecond)
	if err == nil || err.Error() != "timeout waiting for response for command 1" {
		t.Fatalf("waitForResponse = %v, want a timeout", err)
	}

	if got := waiters("exp"); got != 1 {
		t.Fatalf("%d waiters left, want only the other command's", got)
	}

	fake.answered.Store(true)

	if err := <-errc; err != nil {
		t.Fatalf("other waiter: %v", err)
	}
}

func TestWaitForResponseCanceled(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	fake := &fakeCommands{ids: []string{"1"}}

	received := useFakeMinimega(t, fake.reply)
	useFastPolling(t, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	errc := make(chan error, 1)

	go func() { errc <- waitForResponse(ctx, "exp", "1", time.Minute) }()

	polltest.Until(t, "the poller polled twice", func() bool { return fake.polls.Load() > 1 })
	cancel()

	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForResponse = %v, want context.Canceled", err)
	}

	waitForNoPollers(t)

	// Already canceled or timed out: minimega isn't asked.
	sent := len(received())

	if err := waitForResponse(ctx, "exp", "1", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForResponse = %v, want context.Canceled", err)
	}

	err := waitForResponse(context.Background(), "exp", "1", 0)
	if err == nil || err.Error() != "timeout waiting for response for command 1" {
		t.Fatalf("waitForResponse = %v, want a timeout", err)
	}

	if got := len(received()); got != sent {
		t.Fatalf("sent %d commands after giving up", got-sent)
	}
}

func TestWaitForResponseNamespacesPollSeparately(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	fake := &fakeCommands{ids: []string{"1"}}
	fake.answered.Store(true)

	received := useFakeMinimega(t, fake.reply)
	useFastPolling(t, time.Millisecond)

	var wg sync.WaitGroup

	for _, ns := range []string{"a", "b"} {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := waitForResponse(context.Background(), ns, "1", time.Second); err != nil {
				t.Errorf("waitForResponse(%s): %v", ns, err)
			}
		}()
	}

	wg.Wait()

	seen := make(map[string]bool)

	for _, cmd := range received() {
		seen[cmd.Namespace] = true
	}

	if !seen["a"] || !seen["b"] {
		t.Fatalf("polled namespaces %v, want a and b", seen)
	}
}
