package audit

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"phenix/web/middleware"
)

const (
	// refusalWindow is how long a source and reason's refusals after the one
	// audited are counted instead of audited.
	refusalWindow = time.Minute

	// maxRefusalSources bounds the sources and reasons remembered; past it,
	// refusals from new ones share one window per reason.
	maxRefusalSources = 1024

	// refusalForget is how long an unused source and reason is remembered,
	// with any refusals it counted.
	refusalForget = time.Hour

	// maxRefusedUser bounds the user a refused token names, which a client
	// can forge in a proxy JWT.
	maxRefusedUser = 128
)

// refusalKey is the source address and reason of refused requests; an empty
// ip stands for every source past maxRefusalSources.
type refusalKey struct {
	ip, reason string
}

// refusalCount is a source and reason's refusals: when the last one was
// audited, and how many have been skipped since.
type refusalCount struct {
	audited    time.Time
	suppressed int
}

// Refusals audits the requests the auth middleware refuses, as RequestAuth
// failures: at most one each window for a source address and reason, with
// phenix.suppressed counting those skipped since the last. It remembers at
// most max sources and reasons.
type Refusals struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Duration
	max    int
	counts map[refusalKey]*refusalCount
}

// NewRefusals returns Refusals that audit one refusal a window for each
// source and reason, remembering at most max of them, by the clock now.
func NewRefusals(window time.Duration, maxSources int, now func() time.Time) *Refusals {
	return &Refusals{
		mu:     sync.Mutex{},
		now:    now,
		window: window,
		max:    maxSources,
		counts: make(map[refusalKey]*refusalCount),
	}
}

// Refused audits the refusal ref of the request r, unless its source and
// reason had a refusal audited within the window. The user its token names is
// user.name only when the token's signature was checked, and otherwise
// phenix.auth.claimed_user. It never logs the token.
func (rs *Refusals) Refused(r *http.Request, ref middleware.Refusal) {
	suppressed, ok := rs.admit(refusalKey{ip: remoteIP(r.RemoteAddr), reason: ref.Reason})
	if !ok {
		return
	}

	fields := Fields{Reason: ref.Reason, Extra: []any{"phenix.suppressed", suppressed}}

	if user := bounded(ref.User, maxRefusedUser); ref.Verified {
		fields.User = user
	} else if user != "" {
		fields.Extra = append(fields.Extra, "phenix.auth.claimed_user", user)
	}

	emit(r, event{
		level: slog.LevelWarn, action: RequestAuth, denied: false, outcome: OutcomeFailure,
		msg: "request authentication refused", err: nil,
	}, fields)
}

// admit reports whether a refusal of key is audited now and, if it is, how
// many were skipped since the last; otherwise it counts it as skipped.
func (rs *Refusals) admit(key refusalKey) (int, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	now := rs.now()

	c, ok := rs.counts[key]
	if !ok {
		if len(rs.counts) >= rs.max {
			rs.forget(now)
		}

		if len(rs.counts) >= rs.max {
			key.ip = ""
			c, ok = rs.counts[key]
		}
	}

	if !ok {
		rs.counts[key] = &refusalCount{audited: now, suppressed: 0}

		return 0, true
	}

	if now.Sub(c.audited) < rs.window {
		c.suppressed++

		return 0, false
	}

	suppressed := c.suppressed
	c.audited, c.suppressed = now, 0

	return suppressed, true
}

// forget drops the sources and reasons whose window has passed with nothing
// skipped, and those unused for refusalForget.
func (rs *Refusals) forget(now time.Time) {
	for key, c := range rs.counts {
		idle := now.Sub(c.audited)
		if key.ip != "" && (idle >= refusalForget || (idle >= rs.window && c.suppressed == 0)) {
			delete(rs.counts, key)
		}
	}
}

//nolint:gochecknoglobals // one limit for every server and router
var refusals = NewRefusals(refusalWindow, maxRefusalSources, time.Now)

// TokenRefused audits, rate limited, the auth middleware's refusal ref of the
// request r; it is the middleware's RefusalFunc.
func TokenRefused(r *http.Request, ref middleware.Refusal) {
	refusals.Refused(r, ref)
}
