package soh

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"

	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog"
)

// probeRetries is how many times a probe re-runs before its failure stands.
const probeRetries = 5

// hostCheck describes one class of per-host probe: the targets configured for
// each host, how to probe one, and where its result is recorded.
type hostCheck[T any] struct {
	// kind keys the target in result metadata and logs, e.g. "service".
	kind string
	// targets to probe on each host, keyed by hostname.
	targets map[string][]T
	// profile picks this check's targets out of an app's SoH profile; nil
	// when the check has no profile form.
	profile func(sohProfile) []T
	// label names a target in metadata; nil means its string form.
	label func(T) string
	// test schedules one probe on the state group.
	test    func(context.Context, *mm.StateGroup, string, ifaces.NodeSpec, T)
	waitMsg string
	failMsg string
	// record adds a result to the host's state.
	record func(*HostState, State)
}

// runHostCheck probes every configured target in parallel and records the
// results, reporting whether any probe failed.
func runHostCheck[T any](ctx context.Context, s *SOH, ns string, c hostCheck[T]) bool {
	var (
		logger = plog.LoggerFromContext(ctx, plog.TypeSoh)
		wg     = new(mm.StateGroup)
		label  = c.label
	)

	if label == nil {
		label = func(t T) string { return fmt.Sprint(t) }
	}

	probe := func(host string, target T) {
		meta := map[string]any{"host": host, c.kind: label(target)}

		if s.skipHost(wg, host, meta) {
			logger.Debug("skipping host per config", "host", host)

			return
		}

		logger.Debug("running check on host", "host", host, c.kind, meta[c.kind])
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

	if waitAll(ctx, wg, c.waitMsg) {
		return true
	}

	for _, state := range wg.States {
		host, _ := state.Meta["host"].(string)

		st := s.newState(state)
		if st.Error != "" {
			logger.Error(c.failMsg, "host", host, c.kind, state.Meta[c.kind])
		}

		s.updateHost(host, func(h *HostState) { c.record(h, st) })
	}

	return wg.ErrCount > 0
}

// appProfiles yields the SoH profile each app declares for each of its hosts.
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
					logger.Warn("incorrect SoH profile for host in app", "host", host.Hostname(), "app", app.Name())

					continue
				}

				if !yield(host.Hostname(), profile) {
					return
				}
			}
		}
	}
}

// waitAll blocks until every probe on wg has reported, logging progress
// meanwhile. It reports whether the run was cancelled, in which case there is
// nothing worth recording.
func waitAll(ctx context.Context, wg *mm.StateGroup, msg string) bool {
	cancel := periodicallyNotify(ctx, msg)

	wg.Wait()
	cancel()

	return ctx.Err() != nil
}

// newState converts a probe result into a recorded state, dropping a host
// whose C2 client went away from the checks that follow.
func (s *SOH) newState(state mm.GroupStateError) State {
	st := State{ //nolint:exhaustruct // partial initialization
		Metadata:  state.Meta,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if state.Err == nil {
		st.Success = state.Msg

		return st
	}

	if errors.Is(state.Err, mm.ErrC2ClientNotActive) {
		host, _ := state.Meta["host"].(string)
		s.markC2Dead(host)
	}

	st.Error = state.Err.Error()

	return st
}

// updateHost applies fn to the recorded state of host, creating it on first
// use.
func (s *SOH) updateHost(host string, fn func(*HostState)) {
	hs, ok := s.status[host]
	if !ok {
		hs = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
	}

	fn(&hs)
	s.status[host] = hs
}

// schedule runs exec on host through C2 in the background and hands the
// output to expected, which reports the outcome on wg.
func (s SOH) schedule(
	ctx context.Context,
	wg *mm.StateGroup,
	ns, host, exec string,
	meta map[string]any,
	expected func(string) error,
) {
	mm.ScheduleC2ParallelCommand(ctx, &mm.C2ParallelCommand{ //nolint:exhaustruct // partial initialization
		Wait:     wg,
		Limiter:  s.limiter,
		Options:  append(s.c2Options(ns, host), mm.C2Command(exec)),
		Meta:     meta,
		Expected: expected,
	})
}

// failAfterRetries returns a func that re-runs the probe a few times before
// letting err stand.
func failAfterRetries() func(error) error {
	attempts := probeRetries

	return func(err error) error {
		if attempts > 0 {
			attempts--

			return mm.C2RetryError{Delay: c2RetryDelay}
		}

		return err
	}
}

// failAfterDeadline returns a func that re-runs the probe until deadline
// passes, then lets err stand.
func failAfterDeadline(deadline time.Time) func(error) error {
	return func(err error) error {
		if time.Now().After(deadline) {
			return err
		}

		return mm.C2RetryError{Delay: c2RetryDelay}
	}
}

func isWindows(node ifaces.NodeSpec) bool {
	return strings.EqualFold(node.Hardware().OSType(), "windows")
}

func pingCommand(node ifaces.NodeSpec, target string) string {
	if isWindows(node) {
		return "ping -n 1 " + target
	}

	return "ping -c 1 " + target
}

// pingFailed reports whether a single ping got no reply.
func pingFailed(node ifaces.NodeSpec, resp string) bool {
	if isWindows(node) {
		return strings.Contains(resp, "Destination host unreachable")
	}

	return strings.Contains(resp, "0 received")
}
