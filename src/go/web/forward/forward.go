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
)

// errUnknownForward is why a port forward's websocket cannot attach to it.
var errUnknownForward = errors.New("unknown forward")

var (
	forwards   = make(map[string]ft.Listener) //nolint:gochecknoglobals // global state
	forwardsMu sync.Mutex                     //nolint:gochecknoglobals // global lock
)

func init() { //nolint:gochecknoinits // background worker
	experiment.RegisterHook("stop", func(stage, name string) {
		forwardsMu.Lock()
		defer forwardsMu.Unlock()

		for _, l := range forwards {
			if l.Exp == name {
				deleteForward(l)
			}
		}
	})

	go func() {
		var (
			ticker       = time.NewTicker(forwardTickerInterval)
			createTunnel = pubsub.Subscribe("create-tunnel")
		)

		for {
			select {
			case <-ticker.C:
				forwardsMu.Lock()
				reapForwards()
				forwardsMu.Unlock()
			case pub := <-createTunnel:
				tunnel, _ := pub.(app.CreateTunnel)

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
		}
	}()
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
		info := mm.GetVMInfo(mm.NS(exp), mm.VMName(vm))
		if len(info) == 0 {
			return fmt.Errorf("vm %s not found for experiment %s", vm, exp)
		}

		listener.ClusterHost = info[0].Host

		listener.DstPort, err = strconv.Atoi(dst)
		if err != nil {
			return fmt.Errorf("parsing destination port %s for forward: %w", dst, err)
		}
	}

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	reapForwards()

	if _, ok := forwards[listener.ToKey()]; ok {
		return fmt.Errorf("forward already exists for user %s", user)
	}

	if listener.ClusterPort == 0 {
		listener.ClusterPort = forwardPortBase + rand.IntN(forwardPortRange) //nolint:gosec // weak random number generator

		for {
			err = mm.CreateTunnel(
				mm.NS(exp),
				mm.VMName(vm),
				mm.TunnelSourcePort(listener.ClusterPort),
				mm.TunnelDestinationPort(listener.DstPort),
				mm.TunnelDestinationHost(host),
			)
			if err != nil {
				if strings.Contains(err.Error(), "bind: address already in use") {
					listener.ClusterPort = forwardPortBase + rand.IntN(forwardPortRange) //nolint:gosec // weak random number generator

					continue
				} else {
					return fmt.Errorf("creating tunnel: %w", err)
				}
			}

			break
		}
	}

	forwards[listener.ToKey()] = listener

	body, _ := json.Marshal(listener)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/forwards", "create", fmt.Sprintf("%s/%s", exp, vm)),
		bt.NewResource("experiment/vm/forward", fmt.Sprintf("%s/%s", exp, vm), "create"),
		body,
	)

	return nil
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

	var (
		listeners []ft.Listener
		prefix    = fmt.Sprintf("%s:%s", exp, vm)
	)

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	reapForwards()

	for key := range forwards {
		if strings.HasPrefix(key, prefix) {
			listeners = append(listeners, forwards[key])
		}
	}

	body, _ := json.Marshal(util.WithRoot("listeners", listeners))
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

	err := createPortForward(ctx, exp, vm, src, host, dst, user)
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

	info := mm.GetVMInfo(mm.NS(exp), mm.VMName(vm))
	if len(info) == 0 {
		return weberror.New(http.StatusNotFound, "vm not found")
	}

	remoteDst, err := strconv.Atoi(dst)
	if err != nil {
		plog.Error(plog.TypeSystem, "parsing destination port for forward", "port", dst, "err", err)

		return weberror.New(http.StatusBadRequest, "invalid destination port")
	}

	key := fmt.Sprintf("%s:%s:%s:%d:%s", exp, vm, host, remoteDst, user)

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	reapForwards()

	if l, ok := forwards[key]; ok {
		// TODO: how would we go about allowing admins to close all port forwards?
		if l.Owner != user {
			return weberror.New(http.StatusForbidden, "forbidden")
		}

		if !l.QEMU {
			err := mm.CloseTunnel(
				mm.NS(exp),
				mm.VMName(vm),
				mm.TunnelDestinationPort(remoteDst),
				mm.TunnelDestinationHost(host),
			)
			if err != nil {
				plog.Error(plog.TypeSystem, "closing tunnel", "err", err)

				return weberror.New(http.StatusInternalServerError, "unable to close tunnel to vm")
			}
		}

		deleteForward(l)

		w.WriteHeader(http.StatusNoContent)

		return nil
	}

	return weberror.New(http.StatusBadRequest, "forward not found (are you the owner?)")
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

	forwardsMu.Lock()
	reapForwards()

	// Check to see if a forward exists that this user created.
	if listener, ok := forwards[key]; ok {
		forwardsMu.Unlock()

		serveForward(w, r, listener, fields)

		return nil
	}

	var (
		prefix  = fmt.Sprintf("%s:%s:%s:%s", exp, vm, host, port)
		matches []string
	)

	// Check to see if a forward exists that anyone created.
	for key := range forwards {
		if strings.HasPrefix(key, prefix) {
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
		rsrc  = rand.NewPCG(uint64(time.Now().Unix()), 1) //nolint:gosec // integer overflow conversion int64 -> uint64
		rando = rand.New(rsrc)                            //nolint:gosec // weak random number generator
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
