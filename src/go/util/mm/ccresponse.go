package mm

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"phenix/util/mm/mmcli"
)

// responsePollInterval is how often `cc commands` is polled while any command
// is waited on.
var responsePollInterval = responseWaitInterval //nolint:gochecknoglobals // shortened by tests

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
}

// responseWaiter is one waitForResponse call.
type responseWaiter struct {
	id     string
	poller *responsePoller
	done   chan struct{} // closed once the command has a response
}

// waitForResponse waits until the command with the given ID has a response
// from at least one client, the context is done, or the timeout passes.
func waitForResponse(ctx context.Context, ns, id string, timeout time.Duration) error {
	after := time.After(timeout)

	// Give up without polling if that has already happened.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-after:
		return fmt.Errorf("timeout waiting for response for command %s", id)
	default:
	}

	w := responsePollers.add(ns, id)
	defer responsePollers.remove(w)

	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-after:
		return fmt.Errorf("timeout waiting for response for command %s", id)
	}
}

// add registers a waiter for the command with the given ID, starting the
// namespace's poller if it isn't running. A new poller polls straight away.
func (s *responsePollerSet) add(ns, id string) *responseWaiter {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pollers == nil {
		s.pollers = make(map[string]*responsePoller)
	}

	p, ok := s.pollers[ns]
	if !ok {
		p = &responsePoller{
			ns:       ns,
			interval: responsePollInterval,
			waiters:  make(map[*responseWaiter]struct{}),
		}

		s.pollers[ns] = p

		go s.run(p)
	}

	w := &responseWaiter{id: id, poller: p, done: make(chan struct{})}

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

		if !s.release(p, respondedCommands(p.ns, ids)) {
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
		ids = append(ids, w.id)
	}

	slices.Sort(ids)

	return slices.Compact(ids), true
}

// release hands every waiter whose command has a response its answer. It
// returns false, having retired p, if no waiters are left.
func (s *responsePollerSet) release(p *responsePoller, responded map[string]bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for w := range p.waiters {
		if responded[w.id] {
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

// respondedCommands returns which of the given commands have a response from
// at least one client.
func respondedCommands(ns string, ids []string) map[string]bool {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = ccCommandsCmd
	cmd.Columns = []string{"id", "responses"}

	// minimega ANDs filters, so several commands can only be looked up with
	// none: one listing of all the namespace's commands.
	if len(ids) == 1 {
		cmd.Filters = []string{"id=" + ids[0]}
	}

	// There is one row per command per cluster host. Only the host running the
	// command's VM has a response, and a row from any one host is sufficient.
	responded := make(map[string]bool)

	for _, row := range mmcli.RunTabular(cmd) {
		if row["responses"] != "0" {
			responded[row["id"]] = true
		}
	}

	return responded
}
