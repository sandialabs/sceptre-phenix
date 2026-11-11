package mm

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

// responsePollInterval is how often `cc commands` is polled while any command
// is waited on.
var responsePollInterval = responseWaitInterval //nolint:gochecknoglobals // shortened by tests

// clientPollInterval is how often `cc clients` is polled while a command
// waits with a C2 client grace.
var clientPollInterval = 10 * time.Second //nolint:gochecknoglobals // shortened by tests

// responsePollers holds one poller per namespace with commands waited on.
var responsePollers responsePollerSet //nolint:gochecknoglobals // shared by all waiters

// responsePollerSet shares `cc commands` polling between everything waiting on
// commands in the same namespace. `cc commands` runs on every node in the
// namespace, so a poll per waiter per second (state of health waits on many
// commands at once) would keep minimega's command socket and mesh busy.
type responsePollerSet struct {
	mu      sync.Mutex
	pollers map[string]*responsePoller // by namespace
}

// responsePoller polls one namespace's commands while it has waiters.
type responsePoller struct {
	ns       string
	interval time.Duration
	waiters  map[*responseWaiter]struct{} // guarded by responsePollerSet.mu
	clients  time.Time                    // next `cc clients` poll; guarded by responsePollerSet.mu
}

// ccCommand is a posted cc command: its ID on the cluster host whose cc server
// has the VM's client, and the VM's UUID. Each host numbers its cc commands
// itself, so another host can have a command of the same ID for another VM. An
// empty host or UUID is not known: any host's row with the ID then counts.
// The graces are those of C2AppearGrace and C2ClientGrace; zero disables one.
type ccCommand struct {
	ns, id, host, uuid       string
	appearGrace, clientGrace time.Duration
}

// responseWaiter is one waitForResponse call. Only its poller uses the fields
// after done.
type responseWaiter struct {
	cmd    ccCommand
	poller *responsePoller
	done   chan struct{} // closed once the command has a response or err is set
	err    error         // set before done is closed
	start  time.Time     // when the wait started
	listed bool          // the command showed on its host
	seen   time.Time     // when the VM's C2 client last showed
}

// ccPoll is what one poll of a namespace found.
type ccPoll struct {
	at      time.Time
	rows    []map[string]string // `cc commands` rows
	partial bool                // a host did not answer `cc commands`
	clients map[string]bool     // lowercase UUIDs in `cc clients`; nil if not known
}

// waitForResponse waits until the command has a response from at least one
// client, the context is done, or the timeout passes. It fails at once with
// ErrC2CommandMismatch when the command's ID on the VM's host names a command
// that does not target the VM. It fails early when a grace of cmd passes.
func waitForResponse(ctx context.Context, cmd ccCommand, timeout time.Duration) error {
	after := time.After(timeout)

	// Give up without polling if that has already happened.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-after:
		return fmt.Errorf("timeout waiting for response for command %s", cmd.id)
	default:
	}

	w := responsePollers.add(cmd)
	defer responsePollers.remove(w)

	select {
	case <-w.done:
		return w.err
	case <-ctx.Done():
		return ctx.Err()
	case <-after:
		return fmt.Errorf("timeout waiting for response for command %s", cmd.id)
	}
}

// add registers a waiter for the command, starting the namespace's poller if
// it isn't running. A new poller polls straight away.
func (s *responsePollerSet) add(cmd ccCommand) *responseWaiter {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pollers == nil {
		s.pollers = make(map[string]*responsePoller)
	}

	p, ok := s.pollers[cmd.ns]
	if !ok {
		p = &responsePoller{
			ns:       cmd.ns,
			interval: responsePollInterval,
			waiters:  make(map[*responseWaiter]struct{}),
			clients:  time.Now().Add(clientPollInterval),
		}

		s.pollers[cmd.ns] = p

		go s.run(p)
	}

	now := time.Now()
	w := &responseWaiter{cmd: cmd, poller: p, done: make(chan struct{}), err: nil, start: now, listed: false, seen: now}

	p.waiters[w] = struct{}{}

	return w
}

// remove unregisters a waiter, if its poller hasn't already released it.
func (s *responsePollerSet) remove(w *responseWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(w.poller.waiters, w)
}

// run polls p's namespace until p has no waiters left.
func (s *responsePollerSet) run(p *responsePoller) {
	for {
		ids, ok := s.pending(p)
		if !ok {
			return
		}

		poll := ccPoll{at: time.Now(), rows: nil, partial: false, clients: nil}
		poll.rows, poll.partial = commandRows(p.ns, ids)

		if s.clientsDue(p, poll.at) {
			poll.clients = clientUUIDs(p.ns)
		}

		if !s.release(p, poll) {
			return
		}

		time.Sleep(p.interval)
	}
}

// pending returns the distinct command IDs p's waiters wait on. With none left,
// it retires p (so the next waiter starts a new poller) and returns false.
func (s *responsePollerSet) pending(p *responsePoller) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(p.waiters) == 0 {
		s.retire(p)

		return nil, false
	}

	ids := make([]string, 0, len(p.waiters))

	for w := range p.waiters {
		ids = append(ids, w.cmd.id)
	}

	slices.Sort(ids)

	return slices.Compact(ids), true
}

// clientsDue reports whether p must poll `cc clients` at now: a waiter has a
// C2 client grace and the interval since the last poll has passed.
func (s *responsePollerSet) clientsDue(p *responsePoller, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now.Before(p.clients) {
		return false
	}

	for w := range p.waiters {
		if w.cmd.uuid != "" && w.cmd.clientGrace > 0 {
			p.clients = now.Add(clientPollInterval)

			return true
		}
	}

	return false
}

// release hands every waiter whose wait the poll ends its answer. It returns
// false, having retired p, if no waiters are left.
func (s *responsePollerSet) release(p *responsePoller, poll ccPoll) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for w := range p.waiters {
		if ended, err := w.check(poll); ended {
			w.err = err
			close(w.done)
			delete(p.waiters, w)
		}
	}

	if len(p.waiters) == 0 {
		s.retire(p)

		return false
	}

	return true
}

// retire stops handing new waiters to p. The caller must hold s.mu.
func (s *responsePollerSet) retire(p *responsePoller) {
	if s.pollers[p.ns] == p {
		delete(s.pollers, p.ns)
	}
}

// commandRows lists the given commands in `cc commands`, one row per command
// per cluster host. It also reports whether a host did not answer.
func commandRows(ns string, ids []string) ([]map[string]string, bool) {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = ccCommandsCmd
	cmd.Columns = []string{hostColumn, "id", "responses", "filter"}

	// minimega ANDs filters, so several commands can only be looked up with
	// none: one listing of all the namespace's commands.
	if len(ids) == 1 {
		cmd.Filters = []string{"id=" + ids[0]}
	}

	rows, err := mmcli.RunTabularReadOnlyChecked(context.Background(), nsLine(ns), cmd, nil)
	if err != nil {
		plog.Error(plog.TypeSystem, "error running mm cmd", "cmd", cmd.Command, "error", err)
	}

	return rows, err != nil
}

// clientUUIDs returns the lowercase UUIDs of the C2 clients in ns, or nil if
// a host did not answer.
func clientUUIDs(ns string) map[string]bool {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = ccClientCmd
	cmd.Columns = []string{"uuid"}

	rows, err := mmcli.RunTabularReadOnlyChecked(context.Background(), nsLine(ns), cmd, nil)
	if err != nil {
		plog.Error(plog.TypeSystem, "error running mm cmd", "cmd", cmd.Command, "error", err)

		return nil
	}

	uuids := make(map[string]bool, len(rows))

	for _, row := range rows {
		uuids[strings.ToLower(row["uuid"])] = true
	}

	return uuids
}

// check reports whether poll ends w's wait, and with which error. The wait
// fails when the command does not show on its host within the appear grace, or
// when the VM's C2 client stays absent for longer than the client grace. A
// poll that a host did not answer does not count as absence.
func (w *responseWaiter) check(poll ccPoll) (bool, error) {
	c := w.cmd

	if answered, err := c.answer(poll.rows); answered {
		return true, err
	}

	if c.listed(poll.rows) {
		w.listed = true
	}

	if !w.listed && !poll.partial && c.host != "" && c.appearGrace > 0 && poll.at.Sub(w.start) >= c.appearGrace {
		return true, fmt.Errorf("command %s not on host %s after %s: %w", c.id, c.host, c.appearGrace, ErrC2CommandMismatch)
	}

	if poll.clients == nil || c.uuid == "" || c.clientGrace <= 0 {
		return false, nil
	}

	if poll.clients[strings.ToLower(c.uuid)] {
		w.seen = poll.at

		return false, nil
	}

	if absent := poll.at.Sub(w.seen); absent > c.clientGrace {
		return true, fmt.Errorf("C2 client of VM %s absent for %s while waiting for command %s: %w",
			c.uuid, absent.Round(time.Second), c.id, ErrC2ClientNotActive)
	}

	return false, nil
}

// listed reports whether the rows hold c's ID on c's host.
func (c ccCommand) listed(rows []map[string]string) bool {
	for _, row := range rows {
		if row["id"] == c.id && row[hostColumn] == c.host {
			return true
		}
	}

	return false
}

// answer reports whether the `cc commands` rows end the wait for c: with a
// response from a client on c's host, or with ErrC2CommandMismatch when the
// row of c's ID on that host does not target the VM. Rows of other hosts with
// the ID are other commands.
func (c ccCommand) answer(rows []map[string]string) (bool, error) {
	for _, row := range rows {
		if row["id"] != c.id || (c.host != "" && row[hostColumn] != c.host) {
			continue
		}

		if c.uuid != "" && !filterTargets(row["filter"], c.uuid) {
			if c.host == "" {
				continue
			}

			return true, fmt.Errorf("command %s on host %s targets %q, not VM %s: %w",
				c.id, c.host, row["filter"], c.uuid, ErrC2CommandMismatch)
		}

		if row["responses"] != "0" {
			return true, nil
		}
	}

	return false, nil
}

// filterTargets reports whether a `cc commands` filter column, terms such as
// `uuid=<uuid>` joined by " && ", names the client with uuid.
func filterTargets(filter, uuid string) bool {
	for term := range strings.SplitSeq(filter, "&&") {
		if strings.EqualFold(strings.TrimSpace(term), "uuid="+uuid) {
			return true
		}
	}

	return false
}
