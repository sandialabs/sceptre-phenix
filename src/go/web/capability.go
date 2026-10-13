package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"

	"phenix/api/experiment"
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

	// How often an open stream or watch checks its grant when
	// capRecheckInterval is shorter.
	minOpenGrantRecheck = 10 * time.Millisecond

	viewOps = []capability.Op{capability.OpView, capability.OpDownload, capability.OpWatch}
)

// newCaptureSecret returns a new random token: for a stream, or, for
// WebShark, a name that keeps the readable file name base WebShark shows
// and saves the capture as: router.pcap becomes router-<random>.pcap.
func newCaptureSecret(stream bool, base string) string {
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

	token := newCaptureSecret(stream, sharkBaseName(target))
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
		revokeCaptureGrant(g, reason, "op", op)

		return g, http.StatusNotFound
	}

	captureGrants.Update(g.ID, func(g *capability.Grant) { g.Checked = time.Now() })

	return g, 0
}

// revokeCaptureGrant ends the grant, whose user may no longer read its
// capture for the reason, and logs it with fields.
func revokeCaptureGrant(g capability.Grant, reason string, fields ...any) {
	captureGrants.RevokeWhere(func(other capability.Grant) bool { return other.ID == g.ID })

	plog.Warn(plog.TypeSecurity, "capture grant revoked", append([]any{
		"reason", reason, "user", g.User, "exp", g.Target.Exp, "id", g.ID,
	}, fields...)...)
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

//nolint:gochecknoinits // experiment hooks are registered once, before any runs
func init() {
	experiment.RegisterHook("delete", revokeOnExperimentDelete)
	experiment.RegisterHook("stop", revokeOnExperimentStop)
}

// revokeOnExperimentDelete ends every capture grant of a deleted experiment.
func revokeOnExperimentDelete(_, exp string) {
	revokeCaptureGrants("experiment deleted", func(g capability.Grant) bool {
		return g.Target.Exp == exp
	}, "exp", exp)
}

// revokeOnExperimentStop ends the stream grants of a stopped experiment,
// whose captures have stopped. WebShark's grants go on showing the capture
// files the captures leave.
func revokeOnExperimentStop(_, exp string) {
	revokeCaptureGrants("experiment stopped", func(g capability.Grant) bool {
		return g.Target.Exp == exp && g.Allows(capability.OpStream)
	}, "exp", exp)
}

// revokeExperimentFileGrants ends the capture grants of an experiment file
// that has been deleted: a copy of it fetched from a mesh node may remain.
func revokeExperimentFileGrants(exp, file string) {
	revokeCaptureGrants("file deleted", func(g capability.Grant) bool {
		return g.Target.Exp == exp && g.Target.File == file
	}, "exp", exp, "file", file)
}

// revokeCaptureGrants ends the capture grants match reports true for, and
// logs why with fields when there were any.
func revokeCaptureGrants(reason string, match func(capability.Grant) bool, fields ...any) {
	n := captureGrants.RevokeWhere(match)
	if n == 0 {
		return
	}

	plog.Info(plog.TypeSecurity, "revoked capture grants", append([]any{"reason", reason, "count", n}, fields...)...)
}

// untilRevoked returns a context that is also canceled when the grant g
// ends. A stream or watch is a single use of its grant that may run for
// hours, so while it runs the grant is checked again every
// capRecheckInterval, as each use is, and revoked when its user may no
// longer read the capture.
func (c grantChecker) untilRevoked(ctx context.Context, g capability.Grant) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	// a ticker needs a positive interval; tests set capRecheckInterval to 0
	recheck := time.NewTicker(max(capRecheckInterval, minOpenGrantRecheck))

	go func() {
		defer recheck.Stop()

		for {
			select {
			case <-g.Done:
				cancel()

				return
			case <-ctx.Done():
				return
			case <-recheck.C:
			}

			reason, err := c.recheck(g)

			switch {
			case err != nil:
				// keep streaming: the store may be back by the next check
				plog.Error(plog.TypeSystem, "checking capture grant", "exp", g.Target.Exp, "id", g.ID, "err", err)
			case reason != "":
				revokeCaptureGrant(g, reason, "open", true)
				cancel()

				return
			default:
				captureGrants.Update(g.ID, func(g *capability.Grant) { g.Checked = time.Now() })
			}
		}
	}()

	return ctx, cancel
}

type captureTokenRequest struct {
	sharkCaptureRequest

	Operations []capability.Op `json:"operations"`
}

type captureTokenTarget struct {
	Path      string `json:"path,omitempty"`
	VM        string `json:"vm,omitempty"`
	Interface *int   `json:"interface,omitempty"`
}

// captureToken is a capture grant as the capture-tokens routes show it. The
// token itself is shown only when the grant is given.
type captureToken struct {
	ID         string             `json:"id"`
	Token      string             `json:"token,omitempty"`
	Operations []capability.Op    `json:"operations"`
	Expires    string             `json:"expires"`
	UseBy      string             `json:"useBy,omitempty"`
	LastUsed   string             `json:"lastUsed"`
	Uses       int                `json:"uses"`
	Experiment string             `json:"experiment"`
	Target     captureTokenTarget `json:"target"`
	// where to use the token, relative to the UI's base path
	URLs map[string]string `json:"urls,omitempty"`
}

func newCaptureToken(g capability.Grant) captureToken {
	t := captureToken{
		ID:         g.ID,
		Token:      "",
		Operations: g.Ops,
		Expires:    g.Expires.UTC().Format(time.RFC3339),
		UseBy:      "",
		LastUsed:   g.LastUsed.UTC().Format(time.RFC3339),
		Uses:       g.Uses,
		Experiment: g.Target.Exp,
		Target:     captureTokenTarget{Path: g.Target.File, VM: "", Interface: nil},
		URLs:       nil,
	}

	if !g.UseBy.IsZero() {
		t.UseBy = g.UseBy.UTC().Format(time.RFC3339)
	}

	if g.Target.VM != "" {
		iface := g.Target.Interface
		t.Target.VM, t.Target.Interface = g.Target.VM, &iface
	}

	return t
}

// captureOps checks the operations a capture token is asked for: stream
// alone, or any of WebShark's.
func captureOps(ops []capability.Op) ([]capability.Op, error) {
	if len(ops) == 0 {
		return nil, errors.New(`give the "operations" the token allows`)
	}

	var granted []capability.Op

	for _, op := range ops {
		switch {
		case op == capability.OpStream && len(ops) == 1:
		case op == capability.OpStream:
			return nil, errors.New("a stream token allows only stream")
		case !slices.Contains(viewOps, op):
			return nil, fmt.Errorf("unknown operation %q", op)
		}

		if !slices.Contains(granted, op) {
			granted = append(granted, op)
		}
	}

	return granted, nil
}

// CreateCaptureToken - POST /experiments/{exp}/capture-tokens
//
// Gives the user a capture token: for a stream of a running capture
// ({"operations": ["stream"]}), which a command outside the browser streams
// it with at /webshark/streams/{token}, or for WebShark to open a capture,
// as POST /experiments/{exp}/webshark does. The capture is named as that
// route takes it, and the user needs the same permissions.
func CreateCaptureToken(w http.ResponseWriter, r *http.Request) {
	var (
		exp = mux.Vars(r)["exp"]
		req captureTokenRequest
	)

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSharkRequestBytes)).Decode(&req)
	if err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)

		return
	}

	ops, err := captureOps(req.Operations)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	stream := ops[0] == capability.OpStream

	if !stream && !o.featured("webshark") {
		http.Error(w, "WebShark is not installed on this phēnix server", http.StatusNotImplemented)

		return
	}

	target, ok := resolveSharkTarget(w, r, exp, req.sharkCaptureRequest)
	if !ok {
		return
	}

	if stream && !target.Live {
		http.Error(w, "only a running capture streams; this file is no longer being captured", http.StatusBadRequest)

		return
	}

	g, token := mintCaptureGrant(r, target, ops)

	resp := newCaptureToken(g)
	resp.Token = token

	if stream {
		resp.URLs = map[string]string{"stream": "webshark/streams/" + url.PathEscape(token)}
	} else if g.Allows(capability.OpView) {
		resp.URLs = map[string]string{"view": "webshark/embed?capture=" + url.QueryEscape(token)}
	}

	body, _ := json.Marshal(resp)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)
}

// ListCaptureTokens - GET /experiments/{exp}/capture-tokens
//
// Lists the user's own capture tokens for the experiment that have not
// ended, without the tokens themselves.
func ListCaptureTokens(w http.ResponseWriter, r *http.Request) {
	var (
		exp    = mux.Vars(r)["exp"]
		user   = middleware.UserFromContext(r.Context())
		grants = captureGrants.List(user, exp)
		tokens = make([]captureToken, 0, len(grants))
	)

	for _, g := range grants {
		tokens = append(tokens, newCaptureToken(g))
	}

	body, _ := json.Marshal(map[string]any{"tokens": tokens})

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// DeleteCaptureToken - DELETE /experiments/{exp}/capture-tokens/{id}
//
// Revokes one of the user's own capture tokens, ending what it opened. A
// token that is not the user's is not found, like one that has ended.
func DeleteCaptureToken(w http.ResponseWriter, r *http.Request) {
	var (
		vars = mux.Vars(r)
		user = middleware.UserFromContext(r.Context())
		id   = vars["id"]
	)

	grants := captureGrants.List(user, vars["exp"])
	if !slices.ContainsFunc(grants, func(g capability.Grant) bool { return g.ID == id }) || !captureGrants.Revoke(id, user) {
		http.Error(w, "capture token not found", http.StatusNotFound)

		return
	}

	plog.Info(plog.TypeSecurity, "capture grant revoked", "reason", "revoked by user", "user", user, "exp", vars["exp"], "id", id)

	w.WriteHeader(http.StatusNoContent)
}
