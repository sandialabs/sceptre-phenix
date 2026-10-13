package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dgrijalva/jwt-go"

	"phenix/store"
	"phenix/util/plog"
	"phenix/web/capability"
	"phenix/web/middleware"
)

// Capture grants let what cannot carry a user's session read one capture:
// WebShark, whose requests carry only the capture's name, and a command that
// streams a capture into a local Wireshark. Each grant is bound to the user
// and session it was given under, and each use checks that the user may
// still read the capture.
const (
	// A WebShark grant ends once unused this long, or this long after it
	// was given, or when its session ends, whichever comes first.
	viewGrantIdle     = 2 * time.Hour
	viewGrantLifetime = 12 * time.Hour

	// A stream grant starts streams for this long, and what it started ends
	// with the session, or this long after the grant was given when the
	// server does not sign users in.
	streamGrantWindow   = 5 * time.Minute
	streamGrantLifetime = 24 * time.Hour

	maxCaptureGrants        = 1024
	maxCaptureGrantsPerUser = 64

	// Starts every stream token, so secret scanners and log redaction can
	// spot one.
	streamTokenPrefix = "pcs_"
)

//nolint:gochecknoglobals // one registry per process; a variable for tests
var (
	captureGrants = capability.New(maxCaptureGrants, maxCaptureGrantsPerUser, nil)

	// How long a use of a grant trusts the last check of the user's access.
	capRecheckInterval = 5 * time.Second

	viewOps = []capability.Op{capability.OpView, capability.OpDownload, capability.OpWatch}
)

// newCaptureToken returns a new random token: for a stream, or, for
// WebShark, a name that keeps the readable file name base WebShark shows
// and saves the capture as: router.pcap becomes router-<random>.pcap.
func newCaptureToken(stream bool, base string) string {
	secret := strings.ToLower(rand.Text())

	if stream {
		return streamTokenPrefix + secret
	}

	stem, ext := base, ""

	for _, e := range captureExtensions {
		for _, suffix := range []string{e + ".gz", e} {
			if len(base) > len(suffix) && strings.EqualFold(base[len(base)-len(suffix):], suffix) {
				stem, ext = base[:len(base)-len(suffix)], base[len(base)-len(suffix):]

				break
			}
		}

		if ext != "" {
			break
		}
	}

	return stem + "-" + secret + ext
}

// mintCaptureGrant gives the user of the request a grant of the operations
// on the capture target, which the request's handler has checked the user's
// role may read, and returns the grant and its token.
func mintCaptureGrant(r *http.Request, target capability.Target, ops []capability.Op) (capability.Grant, string) {
	var (
		ctx     = r.Context()
		now     = time.Now()
		session = middleware.JWTFromContext(ctx)
		ends    = sessionExpiry(session)
		stream  = len(ops) == 1 && ops[0] == capability.OpStream
		g       = capability.Grant{ //nolint:exhaustruct // the rest set by Mint
			User:    middleware.UserFromContext(ctx),
			Session: session,
			Target:  target,
			Ops:     ops,
		}
	)

	if stream {
		g.Expires = now.Add(streamGrantLifetime)
		if !ends.IsZero() {
			g.Expires = ends
		}

		g.UseBy = minTime(now.Add(streamGrantWindow), g.Expires)
	} else {
		g.Expires, g.IdleTimeout = now.Add(viewGrantLifetime), viewGrantIdle
		if !ends.IsZero() {
			g.Expires = minTime(g.Expires, ends)
		}
	}

	token := newCaptureToken(stream, sharkBaseName(target))
	g = captureGrants.Mint(token, g)

	plog.Info(
		plog.TypeSecurity, "capture grant given",
		"user", g.User, "exp", target.Exp, "id", g.ID, "ops", ops, "expires", g.Expires,
	)

	return g, token
}

// sessionExpiry is when the session JWT raw expires, or zero when it has no
// expiry (or there is no session). The auth middleware has verified it.
func sessionExpiry(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}

	token, _, err := new(jwt.Parser).ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		return time.Time{}
	}

	claims, _ := token.Claims.(jwt.MapClaims)

	exp, ok := claims[jwtClaimExp].(float64)
	if !ok {
		return time.Time{}
	}

	return time.Unix(int64(exp), 0)
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}

	return a
}

// grantChecker checks each use of a capture grant as the server's auth mode
// requires. The routes that take a grant are outside the API's auth
// middleware: the grant is their credential.
type grantChecker struct {
	jwtKey          string
	proxyAuthHeader string
}

// use returns the grant token names, if it allows op and the user it was
// given to may still use it, or else the HTTP status to refuse the request
// with. A grant whose user may no longer read its capture, or has signed
// out, is revoked. The token is never logged.
func (c grantChecker) use(r *http.Request, token string, op capability.Op) (capability.Grant, int) {
	g, err := captureGrants.Use(token, op)

	switch {
	case errors.Is(err, capability.ErrOperation):
		plog.Warn(
			plog.TypeSecurity, "capture grant used for an operation it does not allow",
			"user", g.User, "exp", g.Target.Exp, "id", g.ID, "op", op,
		)

		return g, http.StatusForbidden
	case err != nil:
		return g, http.StatusNotFound
	}

	// A proxy that names the user on these requests must name the grant's.
	// One that names users only on API requests leaves the header out.
	if c.proxyAuthHeader != "" {
		if user := r.Header.Get(c.proxyAuthHeader); user != "" && user != g.User {
			plog.Warn(
				plog.TypeSecurity, "capture grant used by another proxy user",
				"user", g.User, "proxy_user", user, "exp", g.Target.Exp, "id", g.ID, "op", op,
			)

			return g, http.StatusForbidden
		}
	}

	if time.Since(g.Checked) < capRecheckInterval {
		return g, 0
	}

	reason, err := c.recheck(g)
	if err != nil {
		plog.Error(plog.TypeSystem, "checking capture grant", "exp", g.Target.Exp, "id", g.ID, "err", err)

		return g, http.StatusServiceUnavailable
	}

	if reason != "" {
		captureGrants.RevokeWhere(func(other capability.Grant) bool { return other.ID == g.ID })

		plog.Warn(
			plog.TypeSecurity, "capture grant revoked",
			"reason", reason, "user", g.User, "exp", g.Target.Exp, "id", g.ID, "op", op,
		)

		return g, http.StatusNotFound
	}

	captureGrants.Update(g.ID, func(g *capability.Grant) { g.Checked = time.Now() })

	return g, 0
}

// recheck returns why the grant's user may no longer read its capture, or
// "" when the user may, or an error when it cannot tell.
func (c grantChecker) recheck(g capability.Grant) (string, error) {
	role, err := middleware.ResolveRole(c.jwtKey, g.User, g.Session)
	if err != nil {
		return "session ended: " + err.Error(), nil //nolint:nilerr // the reason to revoke the grant
	}

	if g.Target.VM != "" {
		if !captureReadAllowed(role, g.Target.Exp, g.Target.VM) {
			return "capture no longer allowed", nil
		}
	} else if !role.Allowed("experiments/files", "get", g.Target.Exp) {
		return "file no longer allowed", nil
	}

	exp, err := store.NewConfig("experiment/" + g.Target.Exp)
	if err != nil {
		return "", fmt.Errorf("naming experiment: %w", err)
	}

	err = store.Get(exp)

	switch {
	case errors.Is(err, store.ErrNotExist):
		return "experiment deleted", nil
	case err != nil:
		return "", fmt.Errorf("getting experiment: %w", err)
	}

	return "", nil
}

// untilRevoked returns a context that is also canceled when the grant whose
// Done channel done is ends.
func untilRevoked(ctx context.Context, done <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, cancel
}
