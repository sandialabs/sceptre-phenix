package audit

import (
	"net/http"
	"slices"
	"sync"
	"time"

	"phenix/util/plog"
	"phenix/web/middleware"
)

const (
	// maxRepeatReason bounds the error text that tells repeated failures apart.
	maxRepeatReason = 256

	// repeatForget is how long an unused key is remembered, with any events
	// it counted.
	repeatForget = time.Hour
)

// repeatKey is one user's action on one experiment and VM, with its outcome
// and, for a failure, its error text.
//
//nolint:unused // a map key, so every field tells keys apart
type repeatKey struct {
	action                       Action
	outcome                      Outcome
	user, experiment, vm, reason string
}

// Repeats audits the successes of a user's action on one experiment and VM at
// most once each success window, and the failures with one error at most once
// each failure window. phenix.suppressed counts the events skipped since the
// last one audited. When it holds max keys and can forget none, it audits all.
type Repeats struct {
	mu      sync.Mutex
	now     func() time.Time
	success time.Duration
	failure time.Duration
	max     int
	swept   time.Time
	counts  map[repeatKey]*windowCount
}

// NewRepeats returns Repeats with the given windows, which remember at most
// maxKeys keys, by the clock now.
func NewRepeats(success, failure time.Duration, maxKeys int, now func() time.Time) *Repeats {
	return &Repeats{
		mu:      sync.Mutex{},
		now:     now,
		success: success,
		failure: failure,
		max:     maxKeys,
		swept:   now(),
		counts:  make(map[repeatKey]*windowCount),
	}
}

// Success audits, as audit.Success does, that the request r's action worked,
// unless its user, action, experiment and VM had a success audited within the
// window. It reports whether it audited the event.
func (rs *Repeats) Success(r *http.Request, a Action, msg string, f Fields) bool {
	suppressed, ok := rs.admit(r, a, OutcomeSuccess, f, "")
	if ok {
		f.Extra = append(slices.Clip(f.Extra), "phenix.suppressed", suppressed)
		Success(r, a, msg, f)
	}

	return ok
}

// Failure audits, as audit.Failure does, that the request r's action failed
// with err, unless its user, action, experiment, VM and error had a failure
// audited within the window. It reports whether it audited the event.
func (rs *Repeats) Failure(r *http.Request, a Action, msg string, f Fields, err error) bool {
	reason := ""
	if err != nil {
		reason = bounded(err.Error(), maxRepeatReason)
	}

	suppressed, ok := rs.admit(r, a, OutcomeFailure, f, reason)
	if ok {
		f.Extra = append(slices.Clip(f.Extra), "phenix.suppressed", suppressed)
		Failure(r, a, msg, f, err)
	}

	return ok
}

// admit reports whether an event is audited now and, if it is, how many were
// skipped since the last; otherwise it counts it as skipped.
func (rs *Repeats) admit(r *http.Request, a Action, outcome Outcome, f Fields, reason string) (int, bool) {
	user := f.User
	if user == "" && r != nil {
		user = middleware.UserFromContext(r.Context())
	}

	key := repeatKey{action: a, outcome: outcome, user: user, experiment: f.Experiment, vm: f.VM, reason: reason}

	suppressed, ok := rs.count(key)
	if !ok {
		plog.Debug(plog.TypeAction, "repeated event not audited again", "action", a, "outcome", outcome,
			"user", user, "exp", f.Experiment, "vm", f.VM)
	}

	return suppressed, ok
}

// count is admit for key, under the lock.
func (rs *Repeats) count(key repeatKey) (int, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	now := rs.now()

	// sweep once a success window, so the map holds only recent keys
	if now.Sub(rs.swept) >= rs.success {
		rs.forget(now)
	}

	c, ok := rs.counts[key]
	if !ok {
		if len(rs.counts) >= rs.max {
			rs.forget(now)
		}

		if len(rs.counts) < rs.max {
			rs.counts[key] = &windowCount{audited: now, suppressed: 0}
		}

		return 0, true
	}

	if now.Sub(c.audited) < rs.window(key.outcome) {
		c.suppressed++

		return 0, false
	}

	suppressed := c.suppressed
	c.audited, c.suppressed = now, 0

	return suppressed, true
}

// window is how long the events of outcome after the one audited are counted
// instead of audited.
func (rs *Repeats) window(outcome Outcome) time.Duration {
	if outcome == OutcomeSuccess {
		return rs.success
	}

	return rs.failure
}

// forget drops the keys whose window has passed with nothing skipped, and
// those unused for repeatForget.
func (rs *Repeats) forget(now time.Time) {
	rs.swept = now

	for key, c := range rs.counts {
		idle := now.Sub(c.audited)
		if idle >= repeatForget || (idle >= rs.window(key.outcome) && c.suppressed == 0) {
			delete(rs.counts, key)
		}
	}
}
