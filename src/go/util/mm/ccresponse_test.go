package mm

import (
	"context"
	"errors"
	"strconv"
	"strings"
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

			errs[i] = waitForResponse(context.Background(), ccCommand{ns: "exp", id: id}, 10*time.Second)
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

	if err := waitForResponse(context.Background(), ccCommand{ns: "exp", id: "7"}, time.Second); err != nil {
		t.Errorf("waitForResponse(exp, 7) error = %v", err)
	}

	want := []mmtest.Command{{
		Raw:       `.record false namespace "exp" .columns "id","responses","filter" .filter id=7 cc commands`,
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
	go func() { errc <- waitForResponse(context.Background(), ccCommand{ns: "exp", id: "2"}, time.Minute) }()

	polltest.Until(t, "the other command's waiter waits", func() bool { return waiters("exp") == 1 })

	err := waitForResponse(context.Background(), ccCommand{ns: "exp", id: "1"}, 50*time.Millisecond)
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

	go func() { errc <- waitForResponse(ctx, ccCommand{ns: "exp", id: "1"}, time.Minute) }()

	polltest.Until(t, "the poller polled twice", func() bool { return fake.polls.Load() > 1 })
	cancel()

	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Errorf("waitForResponse(exp, 1) canceled while waiting = %v, want %v", err, context.Canceled)
	}

	waitForNoPollers(t)

	// Already canceled or timed out: minimega isn't asked.
	sent := len(received())

	if err := waitForResponse(ctx, ccCommand{ns: "exp", id: "1"}, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("waitForResponse(canceled ctx, exp, 1) = %v, want %v", err, context.Canceled)
	}

	err := waitForResponse(context.Background(), ccCommand{ns: "exp", id: "1"}, 0)
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

			if err := waitForResponse(context.Background(), ccCommand{ns: ns, id: "1"}, time.Second); err != nil {
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

// ccresponseSet returns a poller set holding one poller for namespace exp, with
// a waiter for each of ids, none of them started.
func ccresponseSet(ids ...string) (*responsePollerSet, *responsePoller, []*responseWaiter) {
	const ns = "exp"

	p := &responsePoller{ns: ns, interval: time.Hour, waiters: make(map[*responseWaiter]struct{})}
	s := &responsePollerSet{pollers: map[string]*responsePoller{ns: p}}

	ws := make([]*responseWaiter, 0, len(ids))

	for _, id := range ids {
		w := &responseWaiter{cmd: ccCommand{ns: ns, id: id}, poller: p, done: make(chan struct{})}
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

			s, p, _ := ccresponseSet(tc.ids...)

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

	row := func(id, responses string) map[string]string {
		return map[string]string{hostColumn: "head", "id": id, "responses": responses}
	}

	for name, tc := range map[string]struct {
		ids       []string
		responded []map[string]string
		wantDone  []bool
		wantOK    bool
	}{
		"releases only answered waiters": {
			ids: []string{"1", "2", "2"}, responded: []map[string]string{row("1", "0"), row("2", "1")},
			wantDone: []bool{false, true, true}, wantOK: true,
		},
		"ignores answers nobody waits on": {
			ids: []string{"1"}, responded: []map[string]string{row("9", "1"), row("1", "0")},
			wantDone: []bool{false}, wantOK: true,
		},
		"nothing answered": {ids: []string{"1"}, responded: nil, wantDone: []bool{false}, wantOK: true},
		"retires once every waiter is answered": {
			ids: []string{"1", "2"}, responded: []map[string]string{row("1", "1"), row("2", "3")},
			wantDone: []bool{true, true}, wantOK: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, p, ws := ccresponseSet(tc.ids...)

			if ok := s.release(p, ccPoll{at: time.Now(), rows: tc.responded}); ok != tc.wantOK {
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

	s, old, ws := ccresponseSet("1")

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

	s.release(old, ccPoll{at: time.Now(), rows: []map[string]string{{"id": "1", "responses": "1"}}})
	s.remove(ws[0])
	s.remove(ws[0])

	if len(old.waiters) != 0 {
		t.Errorf("poller has %d waiters after its only one was released and removed, want 0", len(old.waiters))
	}
}

func TestCommandRows(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"id", "responses", "filter"}

	for name, tc := range map[string]struct {
		ids         []string
		answer      []*minicli.Response
		want        []map[string]string
		wantPartial bool
		wantRaw     string
	}{
		"one command is filtered": {
			ids:    []string{"7"},
			answer: []*minicli.Response{mmtest.Tabular("head", header, []string{"7", "2", "uuid=u1"})},
			want: []map[string]string{
				{hostColumn: "head", "id": "7", "responses": "2", "filter": "uuid=u1"},
			},
			wantRaw: `.record false namespace "exp" .columns "id","responses","filter" .filter id=7 cc commands`,
		},
		"several commands are listed together, with the host of each": {
			ids: []string{"1", "2"},
			answer: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"1", "0", "uuid=u1"}, []string{"2", "0", "uuid=u2"}),
				mmtest.Tabular("compute1", header, []string{"2", "1", "uuid=u3"}),
			},
			want: []map[string]string{
				{hostColumn: "head", "id": "1", "responses": "0", "filter": "uuid=u1"},
				{hostColumn: "head", "id": "2", "responses": "0", "filter": "uuid=u2"},
				{hostColumn: "compute1", "id": "2", "responses": "1", "filter": "uuid=u3"},
			},
			wantRaw: `.record false namespace "exp" .columns "id","responses","filter" cc commands`,
		},
		"no answer": {
			ids:         []string{"1", "2"},
			answer:      []*minicli.Response{mmtest.Error("head", "boom")},
			want:        []map[string]string{},
			wantPartial: true,
			wantRaw:     `.record false namespace "exp" .columns "id","responses","filter" cc commands`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == ccCommandsCmd {
					return tc.answer
				}

				return nil
			})

			rows, partial := commandRows("exp", tc.ids)
			if diff := cmp.Diff(tc.want, rows); diff != "" {
				t.Errorf("commandRows(exp, %q) mismatch (-want +got):\n%s", tc.ids, diff)
			}

			if partial != tc.wantPartial {
				t.Errorf("commandRows(exp, %q) partial = %t, want %t", tc.ids, partial, tc.wantPartial)
			}

			want := []mmtest.Command{{Raw: tc.wantRaw, Namespace: "exp", Base: ccCommandsCmd}}
			if diff := cmp.Diff(want, received()); diff != "" {
				t.Errorf("commandRows(exp, %q) sent commands mismatch (-want +got):\n%s", tc.ids, diff)
			}
		})
	}
}

// useFastClientPolling shortens the `cc clients` poll interval until the test
// ends.
func useFastClientPolling(t *testing.T, interval time.Duration) {
	t.Helper()

	saved := clientPollInterval
	clientPollInterval = interval

	t.Cleanup(func() { clientPollInterval = saved })
}

func TestResponseWaiterCheck(t *testing.T) {
	t.Parallel()

	var (
		start  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		cmd    = ccCommand{ns: "exp", id: "4", host: "compute1", uuid: "UUID-A", appearGrace: time.Minute, clientGrace: time.Minute}
		listed = []map[string]string{{hostColumn: "compute1", "id": "4", "responses": "0", "filter": "uuid=uuid-a"}}
		other  = []map[string]string{{hostColumn: "head", "id": "4", "responses": "0", "filter": "uuid=uuid-b"}}
		later  = start.Add(2 * time.Minute)
	)

	for name, tc := range map[string]struct {
		cmd        ccCommand
		listed     bool // the command showed in an earlier poll
		poll       ccPoll
		wantEnded  bool
		wantErr    error
		wantListed bool
		wantSeen   time.Time
	}{
		"answered": {
			cmd: cmd, poll: ccPoll{at: later, rows: []map[string]string{
				{hostColumn: "compute1", "id": "4", "responses": "1", "filter": "uuid=uuid-a"},
			}},
			wantEnded: true, wantSeen: start,
		},
		"listed within the appear grace": {
			cmd: cmd, poll: ccPoll{at: start.Add(time.Second), rows: listed}, wantListed: true, wantSeen: start,
		},
		"not listed within the appear grace": {
			cmd: cmd, poll: ccPoll{at: start.Add(time.Second), rows: other}, wantSeen: start,
		},
		"not listed after the appear grace": {
			cmd: cmd, poll: ccPoll{at: later, rows: other}, wantEnded: true, wantErr: ErrC2CommandMismatch, wantSeen: start,
		},
		"listed before, not now": {
			cmd: cmd, listed: true, poll: ccPoll{at: later, rows: other}, wantListed: true, wantSeen: start,
		},
		"a host did not answer": {
			cmd: cmd, poll: ccPoll{at: later, rows: other, partial: true}, wantSeen: start,
		},
		"host not known": {
			cmd: ccCommand{ns: "exp", id: "4", appearGrace: time.Minute}, poll: ccPoll{at: later}, wantSeen: start,
		},
		"appear grace disabled": {
			cmd: ccCommand{ns: "exp", id: "4", host: "compute1"}, poll: ccPoll{at: later}, wantSeen: start,
		},
		"client present": {
			cmd: cmd, poll: ccPoll{at: later, rows: listed, clients: map[string]bool{"uuid-a": true}},
			wantListed: true, wantSeen: later,
		},
		"client absent within the client grace": {
			cmd: cmd, poll: ccPoll{at: start.Add(time.Second), rows: listed, clients: map[string]bool{}},
			wantListed: true, wantSeen: start,
		},
		"client absent after the client grace": {
			cmd: cmd, poll: ccPoll{at: later, rows: listed, clients: map[string]bool{"uuid-b": true}},
			wantEnded: true, wantErr: ErrC2ClientNotActive, wantListed: true, wantSeen: start,
		},
		"clients not known": {
			cmd: cmd, poll: ccPoll{at: later, rows: listed}, wantListed: true, wantSeen: start,
		},
		"client grace disabled": {
			cmd:  ccCommand{ns: "exp", id: "4", host: "compute1", uuid: "UUID-A"},
			poll: ccPoll{at: later, rows: listed, clients: map[string]bool{}}, wantListed: true, wantSeen: start,
		},
		"UUID not known": {
			cmd:  ccCommand{ns: "exp", id: "4", host: "compute1", clientGrace: time.Minute},
			poll: ccPoll{at: later, rows: listed, clients: map[string]bool{}}, wantListed: true, wantSeen: start,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := &responseWaiter{cmd: tc.cmd, start: start, seen: start, listed: tc.listed}

			ended, err := w.check(tc.poll)
			if ended != tc.wantEnded || !errors.Is(err, tc.wantErr) || (err == nil) != (tc.wantErr == nil) {
				t.Errorf("check(%+v) = %t, %v, want %t, %v", tc.poll, ended, err, tc.wantEnded, tc.wantErr)
			}

			if w.listed != tc.wantListed || !w.seen.Equal(tc.wantSeen) {
				t.Errorf("check(%+v) left listed %t, seen %v, want %t, %v", tc.poll, w.listed, w.seen, tc.wantListed, tc.wantSeen)
			}
		})
	}
}

func TestResponsePollerSetClientsDue(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	s, p, ws := ccresponseSet("1", "2")
	p.clients = now

	if s.clientsDue(p, now) {
		t.Error("clientsDue() with no waiter that has a client grace = true, want false")
	}

	ws[1].cmd.uuid, ws[1].cmd.clientGrace = "uuid-a", time.Minute

	if s.clientsDue(p, now.Add(-time.Second)) {
		t.Error("clientsDue() before the next poll time = true, want false")
	}

	if !s.clientsDue(p, now) {
		t.Fatal("clientsDue() at the next poll time = false, want true")
	}

	if want := now.Add(clientPollInterval); !p.clients.Equal(want) {
		t.Errorf("clientsDue() set the next poll to %v, want %v", p.clients, want)
	}

	if s.clientsDue(p, now) {
		t.Error("clientsDue() just after a poll = true, want false")
	}
}

func TestClientUUIDs(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"uuid"}

	for name, tc := range map[string]struct {
		answer []*minicli.Response
		want   map[string]bool
	}{
		"each client of each host": {
			answer: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"UUID-A"}),
				mmtest.Tabular("compute1", header, []string{"uuid-b"}, []string{"uuid-c"}),
			},
			want: map[string]bool{"uuid-a": true, "uuid-b": true, "uuid-c": true},
		},
		"no clients":            {answer: []*minicli.Response{mmtest.Tabular("head", header)}, want: map[string]bool{}},
		"a host did not answer": {answer: []*minicli.Response{mmtest.Error("compute1", "boom")}, want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == ccClientCmd {
					return tc.answer
				}

				return nil
			})

			if diff := cmp.Diff(tc.want, clientUUIDs("exp")); diff != "" {
				t.Errorf("clientUUIDs(exp) mismatch (-want +got):\n%s", diff)
			}

			want := []mmtest.Command{{Raw: `.record false namespace "exp" .columns "uuid" cc client`, Namespace: "exp", Base: ccClientCmd}}
			if diff := cmp.Diff(want, received()); diff != "" {
				t.Errorf("clientUUIDs(exp) sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A wait for a response ends early when the command does not show on the VM's
// host, or when the VM's C2 client goes away.
func TestC2ResponseWaitGraces(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	t.Run("command not on the host", func(t *testing.T) {
		useFakeMinimega(t, newMeshC2().reply)
		useFastPolling(t, time.Millisecond)

		_, err := (Minimega{}).WaitForC2Response(
			C2NS("exp"), C2VM("vm1"), C2CommandID("9"), C2AppearGrace(10*time.Millisecond), C2Timeout(time.Minute),
		)
		if !errors.Is(err, ErrC2CommandMismatch) {
			t.Errorf("WaitForC2Response(vm1, 9 not on compute1) error = %v, want %v", err, ErrC2CommandMismatch)
		}
	})

	t.Run("client gone", func(t *testing.T) {
		var (
			mesh = newMeshC2()
			mu   sync.Mutex
			gone bool
		)

		received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
			mu.Lock()
			defer mu.Unlock()

			if strings.HasPrefix(cmd.Base, "cc exec ") {
				gone = true
			}

			if cmd.Base == ccClientCmd && gone {
				return []*minicli.Response{mmtest.Tabular("compute1", []string{"uuid"})}
			}

			return mesh.reply(cmd)
		})
		useFastPolling(t, time.Millisecond)
		useFastClientPolling(t, time.Millisecond)

		_, err := (Minimega{}).ExecC2Command(
			C2NS("exp"), C2VM("vm1"), C2Command("hostname"), C2Wait(),
			C2ClientGrace(10*time.Millisecond), C2Timeout(time.Minute),
		)
		if !errors.Is(err, ErrC2ClientNotActive) {
			t.Errorf("ExecC2Command(vm1, wait, client gone) error = %v, want %v", err, ErrC2ClientNotActive)
		}

		if got := mmtest.Count(received(), meshDelete("1")); got != 1 {
			t.Errorf("ExecC2Command(vm1, wait, client gone) sent %d %q, want 1", got, meshDelete("1"))
		}
	})
}
