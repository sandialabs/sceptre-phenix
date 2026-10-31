package forward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"

	"phenix/api/experiment"
	"phenix/app"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	ft "phenix/web/forward/forwardtypes"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	forwardTickerInterval = 10 * time.Second
	forwardPortBase       = 50000
	forwardPortRange      = 15000
	// cluster ports a new tunnel tries before giving up.
	maxTunnelPortAttempts = 20
)

// how long creating or deleting a forward gives minimega, as the web package
// gives a VM change.
var changeTimeout = 5 * time.Minute //nolint:gochecknoglobals // tests shorten it

// errUnknownForward is why a port forward's websocket cannot attach to it.
var errUnknownForward = errors.New("unknown forward")

// forwardsMu guards forwards and pending, and only them: nothing that asks
// minimega or the store, or broadcasts, runs while it is held.
var (
	forwards = make(map[string]ft.Listener) //nolint:gochecknoglobals // global state
	// keys of the forwards being created, by experiment; a stop drops them.
	pending    = make(map[string]string) //nolint:gochecknoglobals // global state
	forwardsMu sync.Mutex                //nolint:gochecknoglobals // global lock
)

func init() { //nolint:gochecknoinits // background worker
	experiment.RegisterHook("stop", func(_, name string) {
		dropExperimentForwards(name)
	})

	go func() {
		var (
			ticker       = time.NewTicker(forwardTickerInterval)
			createTunnel = pubsub.Subscribe("create-tunnel")
		)

		for {
			select {
			case <-ticker.C:
				reapForwards()
			case pub := <-createTunnel:
				tunnel, _ := pub.(app.CreateTunnel)

				// creating asks minimega, so the publisher and the ticker do
				// not wait on it
				go createAppForward(tunnel)
			}
		}
	}()
}

// dropExperimentForwards forgets a stopped experiment's forwards, and the ones
// being created for it.
func dropExperimentForwards(exp string) {
	forwardsMu.Lock()

	var gone []ft.Listener

	for _, l := range forwards {
		if l.Exp == exp {
			removeLocked(l)

			gone = append(gone, l)
		}
	}

	for key, owner := range pending {
		if owner == exp {
			delete(pending, key)
		}
	}

	forwardsMu.Unlock()

	announceDeleted(gone)
}

// createAppForward creates a forward an app asked for.
func createAppForward(tunnel app.CreateTunnel) {
	err := createPortForward(
		context.Background(),
		tunnel.Experiment,
		tunnel.VM,
		tunnel.Sport,
		tunnel.Dhost,
		tunnel.Dport,
		tunnel.User,
	)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"adding port forward",
			"exp",
			tunnel.Experiment,
			"vm",
			tunnel.VM,
			"sport",
			tunnel.Sport,
			"host",
			tunnel.Dhost,
			"dport",
			tunnel.Dport,
			"err",
			err,
		)
	}
}

// createTunnel opens a minimega tunnel from a random free cluster port to the
// VM's destination, and returns the port. It tries maxTunnelPortAttempts ports
// before giving up.
func createTunnel(ctx context.Context, exp, vm, host string, dst int) (int, error) {
	var err error

	for range maxTunnelPortAttempts {
		port := forwardPortBase + rand.IntN(forwardPortRange) //nolint:gosec // G404 picks a port, not a secret

		err = mm.CreateTunnel(
			mm.NS(exp),
			mm.VMName(vm),
			mm.TunnelSourcePort(port),
			mm.TunnelDestinationPort(dst),
			mm.TunnelDestinationHost(host),
			mm.Context(ctx),
		)
		if err == nil {
			return port, nil
		}

		if !strings.Contains(err.Error(), "bind: address already in use") {
			return 0, fmt.Errorf("creating tunnel: %w", err)
		}
	}

	return 0, fmt.Errorf("creating tunnel: no free cluster port in %d tries: %w", maxTunnelPortAttempts, err)
}

// createPortForward creates the user's forward; once ctx is done, a minimega
// command not yet sent gives up (see mm.Context).
func createPortForward(ctx context.Context, exp, vm, src, host, dst, user string) error {
	var err error

	listener := ft.Listener{Exp: exp, VM: vm, Owner: user} //nolint:exhaustruct // partial initialization

	if host == "" {
		listener.DstHost = "127.0.0.1"
	} else {
		listener.DstHost = host
	}

	listener.SrcPort, err = strconv.Atoi(src)
	if err != nil {
		return fmt.Errorf("parsing source port %s for forward: %w", src, err)
	}

	switch strings.ToUpper(dst) {
	case "VNC":
		endpoint, err := mm.GetVNCEndpoint(mm.NS(exp), mm.VMName(vm), mm.Context(ctx))
		if err != nil {
			return fmt.Errorf("getting VNC endpoint for vm %s in experiment %s: %w", vm, exp, err)
		}

		tokens := strings.Split(endpoint, ":")

		listener.ClusterHost = tokens[0]
		listener.ClusterPort, _ = strconv.Atoi(tokens[1])

		listener.DstPort = 5900

		listener.QEMU = true
	default:
		clusterHost, err := mm.GetVMHost(mm.NS(exp), mm.VMName(vm), mm.Context(ctx))
		if err != nil {
			return fmt.Errorf("vm %s not found for experiment %s: %w", vm, exp, err)
		}

		listener.ClusterHost = clusterHost

		listener.DstPort, err = strconv.Atoi(dst)
		if err != nil {
			return fmt.Errorf("parsing destination port %s for forward: %w", dst, err)
		}
	}

	reapVMForwards(exp, vm)

	// the key is reserved, so the tunnel is made without holding forwardsMu
	if !reserve(listener) {
		return fmt.Errorf("forward already exists for user %s", user)
	}

	if listener.ClusterPort == 0 {
		listener.ClusterPort, err = createTunnel(ctx, exp, vm, host, listener.DstPort)
		if err != nil {
			forwardsMu.Lock()
			delete(pending, listener.ToKey())
			forwardsMu.Unlock()

			return err
		}
	}

	// The experiment stopped meanwhile, taking the tunnel with its VMs.
	// Closing it here would recreate the experiment's minimega namespace.
	if !commit(listener) {
		return fmt.Errorf("creating forward: %w", experiment.ErrExperimentNotRunning)
	}

	body, _ := json.Marshal(listener)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/forwards", "create", fmt.Sprintf("%s/%s", exp, vm)),
		bt.NewResource("experiment/vm/forward", fmt.Sprintf("%s/%s", exp, vm), "create"),
		body,
	)

	return nil
}

// reserve claims a new forward's key while its tunnel is made. It reports
// false when the key is taken.
func reserve(l ft.Listener) bool {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	key := l.ToKey()

	_, exists := forwards[key]
	_, creating := pending[key]

	if exists || creating {
		return false
	}

	pending[key] = l.Exp

	return true
}

// commit adds a reserved forward. It reports false when its experiment's stop
// dropped the reservation.
func commit(l ft.Listener) bool {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	key := l.ToKey()

	if _, ok := pending[key]; !ok {
		return false
	}

	delete(pending, key)

	forwards[key] = l

	return true
}

// GetPortForwards - GET /experiments/{exp}/vms/{name}/forwards.
func GetPortForwards(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetPortForwards")

	var (
		ctx     = r.Context()
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
		role    = ctx.Value(middleware.ContextKeyRole).(rbac.Role)

		vars = mux.Vars(r)
		exp  = vars["exp"]
		vm   = vars["name"]
	)

	if !role.Allowed("vms/forwards", "list", fmt.Sprintf("%s/%s", exp, vm)) {
		plog.Warn(
			plog.TypeSecurity,
			"listing port forwards not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			vm,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	var listeners []ft.Listener

	reapVMForwards(exp, vm)

	forwardsMu.Lock()

	// by field, as a key prefix would also match a VM whose name extends this one's
	for _, l := range forwards {
		if l.Exp == exp && l.VM == vm {
			listeners = append(listeners, l)
		}
	}

	forwardsMu.Unlock()

	body, _ := json.Marshal(util.WithRoot("listeners", listeners))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// CreatePortForward - POST /experiments/{exp}/vms/{name}/forwards?src=<int>&host=<ip>&dst=<int>.
func CreatePortForward(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CreatePortForward")

	var (
		ctx     = r.Context()
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
		role    = ctx.Value(middleware.ContextKeyRole).(rbac.Role)

		vars = mux.Vars(r)
		exp  = vars["exp"]
		vm   = vars["name"]

		query = r.URL.Query()
		src   = query.Get("src")
		host  = query.Get("host")
		dst   = query.Get("dst")
	)

	if !role.Allowed("vms/forwards", "create", fmt.Sprintf("%s/%s", exp, vm)) {
		plog.Warn(
			plog.TypeSecurity,
			"creating port forwards not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			vm,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	change, cancel := context.WithTimeout(ctx, changeTimeout)
	defer cancel()

	err := createPortForward(change, exp, vm, src, host, dst, user)
	if ended, endErr := changeEnded(r, change, err); ended {
		return endErr
	}

	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"creating port forward",
			"exp",
			exp,
			"vm",
			vm,
			"src",
			src,
			"host",
			host,
			"dst",
			dst,
			"user",
			user,
			"err",
			err,
		)

		return weberror.New(http.StatusBadRequest, "unable to create tunnel to vm")
	}

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// DeletePortForward - DELETE /experiments/{exp}/vms/{name}/forwards?host=<ip>&dst=<int>.
func DeletePortForward(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "DeletePortForward")

	var (
		ctx     = r.Context()
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
		role    = ctx.Value(middleware.ContextKeyRole).(rbac.Role)

		vars = mux.Vars(r)
		exp  = vars["exp"]
		vm   = vars["name"]

		query = r.URL.Query()
		host  = query.Get("host")
		dst   = query.Get("dst")
	)

	if !role.Allowed("vms/forwards", "delete", fmt.Sprintf("%s/%s", exp, vm)) {
		plog.Warn(
			plog.TypeSecurity,
			"deleting port forwards not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			vm,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if host == "" {
		host = "127.0.0.1"
	}

	change, cancel := context.WithTimeout(ctx, changeTimeout)
	defer cancel()

	if _, err := mm.GetVMHost(mm.NS(exp), mm.VMName(vm), mm.Context(change)); err != nil {
		if ended, endErr := changeEnded(r, change, err); ended {
			return endErr
		}

		return weberror.New(http.StatusNotFound, "vm not found")
	}

	remoteDst, err := strconv.Atoi(dst)
	if err != nil {
		plog.Error(plog.TypeSystem, "parsing destination port for forward", "port", dst, "err", err)

		return weberror.New(http.StatusBadRequest, "invalid destination port")
	}

	key := fmt.Sprintf("%s:%s:%s:%d:%s", exp, vm, host, remoteDst, user)

	reapVMForwards(exp, vm)

	l, status, refusal := takeForward(key, user)
	if status != 0 {
		return weberror.New(status, refusal)
	}

	if !l.QEMU {
		err := mm.CloseTunnel(
			mm.NS(exp),
			mm.VMName(vm),
			mm.TunnelSourcePort(l.ClusterPort),
			mm.TunnelDestinationPort(remoteDst),
			mm.TunnelDestinationHost(host),
			mm.Context(change),
		)
		if err != nil {
			restoreForward(l)

			if ended, endErr := changeEnded(r, change, err); ended {
				return endErr
			}

			plog.Error(plog.TypeSystem, "closing tunnel", "err", err)

			return weberror.New(http.StatusInternalServerError, "unable to close tunnel to vm")
		}
	}

	announceDeleted([]ft.Listener{l})

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// takeForward removes the user's forward under key before its tunnel closes, so no one else uses or closes it. With
// no such forward of the user's, it returns the refusing status and message, else a zero status.
func takeForward(key, user string) (ft.Listener, int, string) {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	l, ok := forwards[key]
	if !ok {
		return l, http.StatusBadRequest, "forward not found (are you the owner?)"
	}

	// TODO: how would we go about allowing admins to close all port forwards?
	if l.Owner != user {
		return l, http.StatusForbidden, "forbidden"
	}

	removeLocked(l)

	return l, 0, ""
}

// changeEnded reports whether err is the forward change giving up because change is done, and the error to answer:
// a 504 at the deadline, none to a client that left.
func changeEnded(r *http.Request, change context.Context, err error) (bool, error) {
	ended := change.Err()
	if err == nil || ended == nil || !errors.Is(err, ended) {
		return false, nil
	}

	uri := middleware.RedactURI(r.RequestURI)

	if r.Context().Err() != nil {
		plog.Debug(plog.TypeHTTP, "request canceled", "method", r.Method, "url", uri, "err", err)

		return true, nil //nolint:nilerr // nothing to answer a client that left
	}

	plog.Error(plog.TypeSystem, "minimega did not answer in time", "method", r.Method, "url", uri, "err", err)

	return true, weberror.New(http.StatusGatewayTimeout, "timed out waiting for minimega")
}

// restoreForward puts back a forward whose tunnel failed to close, unless its
// key was taken or its experiment stopped meanwhile.
func restoreForward(l ft.Listener) {
	running := experiment.Running(l.Exp)

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	key := l.ToKey()

	_, exists := forwards[key]
	_, creating := pending[key]

	if running && !exists && !creating {
		forwards[key] = l
	}
}

// GetPortForwardWebSocket - GET /experiments/{exp}/vms/{name}/forwards/{host}/{port}/ws.
func GetPortForwardWebSocket(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetPortForwardWebSocket")

	var (
		ctx     = r.Context()
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
		role    = ctx.Value(middleware.ContextKeyRole).(rbac.Role)

		vars = mux.Vars(r)
		exp  = vars["exp"]
		vm   = vars["name"]
		host = vars["host"]
		port = vars["port"]
	)

	fields := audit.Fields{Experiment: exp, VM: vm, Extra: []any{"destination.address", host}}
	if p, err := strconv.Atoi(port); err == nil {
		fields.Extra = append(fields.Extra, "destination.port", p)
	}

	if !role.Allowed("vms/forwards", "get", fmt.Sprintf("%s/%s", exp, vm)) {
		audit.Denied(r, audit.PortForwardAttach, "accessing port forwards not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	key := fmt.Sprintf("%s:%s:%s:%s:%s", exp, vm, host, port, user)

	reapVMForwards(exp, vm)

	forwardsMu.Lock()

	// Check to see if a forward exists that this user created.
	if listener, ok := forwards[key]; ok {
		forwardsMu.Unlock()

		serveForward(w, r, listener, fields)

		return nil
	}

	var matches []string

	// Check to see if a forward exists that anyone created: by field, as a
	// key prefix would also match a longer port, such as 2222 for 22.
	for key, l := range forwards {
		if l.Exp == exp && l.VM == vm && l.DstHost == host && strconv.Itoa(l.DstPort) == port {
			matches = append(matches, key)
		}
	}

	if len(matches) == 0 {
		forwardsMu.Unlock()

		plog.Error(plog.TypeSystem, "listener not found for forward", "key", key)
		audit.Failure(r, audit.PortForwardAttach, "port forward attach failed", fields, errUnknownForward)

		return weberror.New(http.StatusBadRequest, "unknown forward")
	}

	var (
		rsrc  = rand.NewPCG(uint64(time.Now().Unix()), 1) //nolint:gosec // G115 the clock is after 1970
		rando = rand.New(rsrc)                            //nolint:gosec // G404 picks a forward, not a secret
	)

	// Use a random forward so the same one doesn't get overwhelmed.
	key = matches[rando.IntN(len(matches))]
	listener := forwards[key]

	forwardsMu.Unlock()

	serveForward(w, r, listener, fields)

	return nil
}

// serveForward proxies the request's websocket to the forward's listener,
// auditing the attach once the websocket is open.
func serveForward(w http.ResponseWriter, r *http.Request, listener ft.Listener, fields audit.Fields) {
	if lifecycle.RefuseWhileStopping(w) {
		return
	}

	fields.Extra = append(fields.Extra, "phenix.forward.owner", listener.Owner)
	proxy := util.ConnectWSHandler(listener.ClusterEndpoint())

	websocket.Handler(func(ws *websocket.Conn) {
		defer lifecycle.TrackConn("port forward", ws)()

		audit.Success(r, audit.PortForwardAttach, "port forward attached", fields)
		proxy(ws)
	}).ServeHTTP(w, r)
}
