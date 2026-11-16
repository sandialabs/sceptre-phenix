package soh

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"

	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

// hostCheck is one kind of check that soh sends to each host: the targets of
// each host, how to probe one target, and where the result goes.
type hostCheck[T any] struct {
	// kind is the result metadata key of a target, for example "service".
	kind string
	// targets to probe, by hostname.
	targets map[string][]T
	// profile gets the targets of this check from an app's SoH profile; nil
	// when the check has none.
	profile func(sohProfile) []T
	// label names a target in the metadata; nil uses its string form.
	label func(T) string
	// test schedules the probe of one target on the state group.
	test    func(context.Context, *mm.StateGroup, string, ifaces.NodeSpec, T)
	waitMsg string
	failMsg string
	// record adds a result to the host's state.
	record func(*HostState, State)
}

// runHostCheck probes each target of c on each host, from the settings and the
// apps' SoH profiles, records the results, and reports whether a probe failed.
func runHostCheck[T any](ctx context.Context, s *SOH, ns string, c hostCheck[T]) bool {
	var (
		logger = sohLogger(ctx, ns)
		wg     = new(mm.StateGroup)
		label  = c.label
	)

	if label == nil {
		label = func(t T) string { return fmt.Sprint(t) }
	}

	probe := func(host string, target T) {
		if s.c2Unavailable(logger, wg, host, map[string]any{hostKey: host, c.kind: label(target)}) {
			return
		}

		logger.Debug("running check on host", hostKey, host, c.kind, label(target))
		c.test(ctx, wg, ns, s.nodes[host], target)
	}

	for host, targets := range c.targets {
		for _, target := range targets {
			probe(host, target)
		}
	}

	if c.profile != nil {
		for host, profile := range s.appProfiles(logger) {
			for _, target := range c.profile(profile) {
				probe(host, target)
			}
		}
	}

	cancel := periodicallyNotify(ctx, logger, c.waitMsg, notifyInterval)

	wg.Wait()
	cancel()

	for _, state := range wg.States {
		host, _ := state.Meta[hostKey].(string)

		st := s.newState(host, state)
		if st.Error != "" {
			logger.Error(c.failMsg, hostKey, host, c.kind, state.Meta[c.kind], "err", st.Error)
		}

		s.updateHost(host, func(h *HostState) { c.record(h, st) })
	}

	return wg.ErrCount > 0
}

// appProfiles gives the SoH profile of each host of each app that has one.
func (s *SOH) appProfiles(logger *slog.Logger) iter.Seq2[string, sohProfile] {
	return func(yield func(string, sohProfile) bool) {
		for _, app := range s.apps {
			for _, host := range app.Hosts() {
				raw, ok := host.Metadata()[s.md.AppProfileKey]
				if !ok {
					continue
				}

				var profile sohProfile

				if err := mapstructure.Decode(raw, &profile); err != nil {
					logger.Warn("incorrect SoH profile for host in app", hostKey, host.Hostname(), "app", app.Name())

					continue
				}

				if !yield(host.Hostname(), profile) {
					return
				}
			}
		}
	}
}

// newState makes the recorded state of a probe result of host. A host whose C2
// client is not active gets no more checks in this run.
func (s *SOH) newState(host string, state mm.GroupState) State {
	st := State{ //nolint:exhaustruct // partial initialization
		Metadata:  state.Meta,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if state.Err == nil {
		st.Success = state.Msg

		return st
	}

	if errors.Is(state.Err, mm.ErrC2ClientNotActive) {
		s.markNoC2(host)
	}

	st.Error = state.Err.Error()

	return st
}

// updateHost applies fn to the recorded state of host, which it makes on
// first use.
func (s *SOH) updateHost(host string, fn func(*HostState)) {
	hs, ok := s.status[host]
	if !ok {
		hs = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
	}

	fn(&hs)
	s.status[host] = hs
}

// retryDeadline is the time limit of a chain of C2 probes. It starts when the
// first probe gets a limiter slot. Later waits for a slot do not count.
type retryDeadline struct {
	mu    sync.Mutex
	now   func() time.Time
	limit time.Duration
	end   time.Time // zero until the first probe gets a slot
}

func newRetryDeadline(now func() time.Time, limit time.Duration) *retryDeadline {
	return &retryDeadline{now: now, limit: limit} //nolint:exhaustruct // end is set later
}

// queued records that a probe got a slot after it waited for waited.
func (d *retryDeadline) queued(waited time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.end.IsZero() {
		d.end = d.now().Add(d.limit)

		return
	}

	d.end = d.end.Add(waited)
}

// passed reports whether the time limit has passed.
func (d *retryDeadline) passed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	return !d.end.IsZero() && d.now().After(d.end)
}

// c2Options gives the options that address host for a C2 command.
func (s SOH) c2Options(ns, host string) []mm.C2Option {
	opts := []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Timeout(s.md.c2Timeout)}

	if s.md.c2AppearGrace != nil {
		opts = append(opts, mm.C2AppearGrace(*s.md.c2AppearGrace))
	}

	if s.md.c2ClientGrace != nil {
		opts = append(opts, mm.C2ClientGrace(*s.md.c2ClientGrace))
	}

	if s.md.useUUIDForC2Active(host) {
		opts = append(opts, mm.C2IDClientsByUUID())
	}

	return opts
}
