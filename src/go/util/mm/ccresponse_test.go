package mm

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

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
			t.Errorf("waitForResponse(exp, %s) error = %v", fake.ids[i], err)
		}
	}

	polls := int(fake.polls.Load())

	// at most one poll per interval for all the waiters together
	if limit := min(int(elapsed/interval)+2, count/2-1); polls > limit {
		t.Errorf("%d waitForResponse callers polled %d times in %v, want at most %d", count, polls, elapsed, limit)
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
		t.Errorf("waitForResponse(exp, 7) error = %v", err)
	}

	want := []mmtest.Command{{
		Raw:       `.record false namespace "exp" .columns "id","responses" .filter id=7 cc commands`,
		Namespace: "exp",
		Base:      "cc commands",
	}}

	if diff := cmp.Diff(want, received()); diff != "" {
		t.Errorf("waitForResponse(exp, 7) sent commands mismatch (-want +got):\n%s", diff)
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
		t.Errorf("waitForResponse(exp, 1, 50ms) error = %v, want a timeout", err)
	}

	if got := waiters("exp"); got != 1 {
		t.Errorf("waiters(exp) after waitForResponse(exp, 1) timed out = %d, want 1", got)
	}

	fake.answered.Store(true)

	if err := <-errc; err != nil {
		t.Errorf("waitForResponse(exp, 2) error = %v", err)
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
		t.Errorf("waitForResponse(exp, 1) canceled while waiting = %v, want %v", err, context.Canceled)
	}

	waitForNoPollers(t)

	// Already canceled or timed out: minimega isn't asked.
	sent := len(received())

	if err := waitForResponse(ctx, "exp", "1", time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("waitForResponse(canceled ctx, exp, 1) = %v, want %v", err, context.Canceled)
	}

	err := waitForResponse(context.Background(), "exp", "1", 0)
	if err == nil || err.Error() != "timeout waiting for response for command 1" {
		t.Errorf("waitForResponse(exp, 1, 0 timeout) error = %v, want a timeout", err)
	}

	if got := len(received()); got != sent {
		t.Errorf("waitForResponse with a canceled ctx or no time left sent %d commands, want 0", got-sent)
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
				t.Errorf("waitForResponse(%s, 1) error = %v", ns, err)
			}
		}()
	}

	wg.Wait()

	seen := make(map[string]bool)

	for _, cmd := range received() {
		seen[cmd.Namespace] = true
	}

	if diff := cmp.Diff(map[string]bool{"a": true, "b": true}, seen); diff != "" {
		t.Errorf("waitForResponse(a, 1) and waitForResponse(b, 1) polled namespaces mismatch (-want +got):\n%s", diff)
	}
}

// ccresponseSet returns a poller set holding one poller for ns, with a waiter
// for each of ids, none of them started.
func ccresponseSet(ns string, ids ...string) (*responsePollerSet, *responsePoller, []*responseWaiter) {
	p := &responsePoller{ns: ns, interval: time.Hour, waiters: make(map[*responseWaiter]struct{})}
	s := &responsePollerSet{pollers: map[string]*responsePoller{ns: p}}

	ws := make([]*responseWaiter, 0, len(ids))

	for _, id := range ids {
		w := &responseWaiter{id: id, poller: p, done: make(chan struct{})}
		p.waiters[w] = struct{}{}
		ws = append(ws, w)
	}

	return s, p, ws
}

// ccresponseDone reports which of ws have been given their answer.
func ccresponseDone(ws []*responseWaiter) []bool {
	done := make([]bool, len(ws))

	for i, w := range ws {
		select {
		case <-w.done:
			done[i] = true
		default:
		}
	}

	return done
}

func TestResponsePollerSetPending(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		ids     []string
		want    []string
		wantOK  bool
		retired bool
	}{
		"distinct sorted IDs":           {ids: []string{"3", "1", "2"}, want: []string{"1", "2", "3"}, wantOK: true},
		"one ID for waiters that share": {ids: []string{"5", "5", "4"}, want: []string{"4", "5"}, wantOK: true},
		"no waiters retires the poller": {ids: nil, want: nil, wantOK: false, retired: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, p, _ := ccresponseSet("exp", tc.ids...)

			ids, ok := s.pending(p)
			if ok != tc.wantOK {
				t.Errorf("pending() ok = %t, want %t", ok, tc.wantOK)
			}

			if diff := cmp.Diff(tc.want, ids); diff != "" {
				t.Errorf("pending() IDs mismatch (-want +got):\n%s", diff)
			}

			if _, kept := s.pollers["exp"]; kept == tc.retired {
				t.Errorf("pending() left the poller registered = %t, want %t", kept, !tc.retired)
			}
		})
	}
}

func TestResponsePollerSetRelease(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		ids       []string
		responded map[string]bool
		wantDone  []bool
		wantOK    bool
	}{
		"releases only answered waiters": {
			ids: []string{"1", "2", "2"}, responded: map[string]bool{"2": true},
			wantDone: []bool{false, true, true}, wantOK: true,
		},
		"ignores answers nobody waits on": {
			ids: []string{"1"}, responded: map[string]bool{"9": true, "1": false},
			wantDone: []bool{false}, wantOK: true,
		},
		"nothing answered": {ids: []string{"1"}, responded: nil, wantDone: []bool{false}, wantOK: true},
		"retires once every waiter is answered": {
			ids: []string{"1", "2"}, responded: map[string]bool{"1": true, "2": true},
			wantDone: []bool{true, true}, wantOK: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, p, ws := ccresponseSet("exp", tc.ids...)

			if ok := s.release(p, tc.responded); ok != tc.wantOK {
				t.Errorf("release(%v) = %t, want %t", tc.responded, ok, tc.wantOK)
			}

			if diff := cmp.Diff(tc.wantDone, ccresponseDone(ws)); diff != "" {
				t.Errorf("release(%v) answered waiters mismatch (-want +got):\n%s", tc.responded, diff)
			}

			left := 0
			for _, done := range tc.wantDone {
				if !done {
					left++
				}
			}

			if len(p.waiters) != left {
				t.Errorf("release(%v) left %d waiters, want %d", tc.responded, len(p.waiters), left)
			}

			if _, kept := s.pollers["exp"]; kept != tc.wantOK {
				t.Errorf("release(%v) left the poller registered = %t, want %t", tc.responded, kept, tc.wantOK)
			}
		})
	}
}

// A retired poller never removes the newer poller that replaced it, and removing
// a waiter already released is harmless.
func TestResponsePollerSetRetireAndRemove(t *testing.T) {
	t.Parallel()

	s, old, ws := ccresponseSet("exp", "1")

	newer := &responsePoller{ns: "exp", interval: time.Hour, waiters: make(map[*responseWaiter]struct{})}
	s.pollers["exp"] = newer

	s.retire(old)

	if s.pollers["exp"] != newer {
		t.Error("retire(old poller) removed the namespace's newer poller")
	}

	s.retire(newer)

	if _, kept := s.pollers["exp"]; kept {
		t.Error("retire(current poller) left it registered")
	}

	s.release(old, map[string]bool{"1": true})
	s.remove(ws[0])
	s.remove(ws[0])

	if len(old.waiters) != 0 {
		t.Errorf("poller has %d waiters after its only one was released and removed, want 0", len(old.waiters))
	}
}

func TestRespondedCommands(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"id", "responses"}

	for name, tc := range map[string]struct {
		ids     []string
		answer  []*minicli.Response
		want    map[string]bool
		wantRaw string
	}{
		"one command is filtered": {
			ids:     []string{"7"},
			answer:  []*minicli.Response{mmtest.Tabular("head", header, []string{"7", "2"})},
			want:    map[string]bool{"7": true},
			wantRaw: `.record false namespace "exp" .columns "id","responses" .filter id=7 cc commands`,
		},
		"several commands are listed together": {
			ids: []string{"1", "2", "3"},
			answer: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"1", "0"}, []string{"2", "0"}, []string{"3", "0"}, []string{"4", "1"}),
				mmtest.Tabular("compute1", header, []string{"1", "0"}, []string{"2", "1"}, []string{"3", "0"}),
			},
			want:    map[string]bool{"2": true, "4": true},
			wantRaw: `.record false namespace "exp" .columns "id","responses" cc commands`,
		},
		"no answer": {
			ids:     []string{"1", "2"},
			answer:  []*minicli.Response{mmtest.Error("head", "boom")},
			want:    map[string]bool{},
			wantRaw: `.record false namespace "exp" .columns "id","responses" cc commands`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == ccCommandsCmd {
					return tc.answer
				}

				return nil
			})

			if diff := cmp.Diff(tc.want, respondedCommands("exp", tc.ids)); diff != "" {
				t.Errorf("respondedCommands(exp, %q) mismatch (-want +got):\n%s", tc.ids, diff)
			}

			want := []mmtest.Command{{Raw: tc.wantRaw, Namespace: "exp", Base: ccCommandsCmd}}
			if diff := cmp.Diff(want, received()); diff != "" {
				t.Errorf("respondedCommands(exp, %q) sent commands mismatch (-want +got):\n%s", tc.ids, diff)
			}
		})
	}
}
