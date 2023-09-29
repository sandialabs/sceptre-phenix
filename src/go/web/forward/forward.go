package forward

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/app"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/broker"
	"phenix/web/rbac"
	"phenix/web/util"

	bt "phenix/web/broker/brokertypes"
	ft "phenix/web/forward/forwardtypes"

	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"
)

var (
	forwards   = make(map[string]ft.Listener)
	forwardsMu sync.Mutex
)

func init() {
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
			ticker       = time.NewTicker(10 * time.Second)
			createTunnel = pubsub.Subscribe("create-tunnel")
		)

		for {
			select {
			case <-ticker.C:
				forwardsMu.Lock()
				reapForwards()
				forwardsMu.Unlock()
			case pub := <-createTunnel:
				tunnel := pub.(app.CreateTunnel)

				if err := createPortForward(tunnel.Experiment, tunnel.VM, tunnel.Sport, tunnel.Dhost, tunnel.Dport, tunnel.User); err != nil {
					plog.Error("adding port forward", "exp", tunnel.Experiment, "vm", tunnel.VM, "sport", tunnel.Sport, "host", tunnel.Dhost, "dport", tunnel.Dport, "err", err)
				}
			}
		}
	}()
}

func createPortForward(exp, vm, src, host, dst, user string) error {
	var err error

	listener := ft.Listener{Exp: exp, VM: vm, Owner: user}

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
		endpoint, err := mm.GetVNCEndpoint(mm.NS(exp), mm.VMName(vm))
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
		listener.ClusterPort = 50000 + rand.Intn(15000)

		for {
			err = mm.CreateTunnel(mm.NS(exp), mm.VMName(vm), mm.TunnelSourcePort(listener.ClusterPort), mm.TunnelDestinationPort(listener.DstPort), mm.TunnelDestinationHost(host))
			if err != nil {
				if strings.Contains(err.Error(), "bind: address already in use") {
					listener.ClusterPort = 50000 + rand.Intn(15000) // retry with a different port
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

// GET /experiments/{exp}/vms/{name}/forwards
func GetPortForwards(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetPortForwards")

	var (
		ctx  = r.Context()
		user = ctx.Value("user").(string)
		role = ctx.Value("role").(rbac.Role)

		vars = mux.Vars(r)
		exp  = vars["exp"]
		vm   = vars["name"]
	)

	if !role.Allowed("vms/forwards", "list", fmt.Sprintf("%s/%s", exp, vm)) {
		plog.Warn("listing port forwards not allowed", "user", user, "exp", exp, "vm", vm)

		http.Error(w, "forbidden", http.StatusForbidden)
		return
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
	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/forwards?src=<int>&host=<ip>&dst=<int>
func CreatePortForward(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "CreatePortForward")

	var (
		ctx  = r.Context()
		user = ctx.Value("user").(string)
		role = ctx.Value("role").(rbac.Role)

		vars = mux.Vars(r)
		exp  = vars["exp"]
		vm   = vars["name"]

		query = r.URL.Query()
		src   = query.Get("src")
		host  = query.Get("host")
		dst   = query.Get("dst")
	)

	if !role.Allowed("vms/forwards", "create", fmt.Sprintf("%s/%s", exp, vm)) {
		plog.Warn("creating port forwards not allowed", "user", user, "exp", exp, "vm", vm)

		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := createPortForward(exp, vm, src, host, dst, user); err != nil {
		plog.Error("creating port forward", "exp", exp, "vm", vm, "src", src, "host", host, "dst", dst, "user", user, "err", err)

		http.Error(w, "unable to create tunnel to vm", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DELETE /experiments/{exp}/vms/{name}/forwards?host=<ip>&dst=<int>
func DeletePortForward(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "DeletePortForward")

	var (
		ctx  = r.Context()
		user = ctx.Value("user").(string)
		role = ctx.Value("role").(rbac.Role)

		vars = mux.Vars(r)
		exp  = vars["exp"]
		vm   = vars["name"]

		query = r.URL.Query()
		host  = query.Get("host")
		dst   = query.Get("dst")
	)

	if !role.Allowed("vms/forwards", "delete", fmt.Sprintf("%s/%s", exp, vm)) {
		plog.Warn("deleting port forwards not allowed", "user", user, "exp", exp, "vm", vm)

		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if host == "" {
		host = "127.0.0.1"
	}

	info := mm.GetVMInfo(mm.NS(exp), mm.VMName(vm))
	if len(info) == 0 {
		http.Error(w, "vm not found", http.StatusNotFound)
		return
	}

	remoteDst, err := strconv.Atoi(dst)
	if err != nil {
		plog.Error("parsing destination port for forward", "port", dst, "err", err)

		http.Error(w, "invalid destination port", http.StatusBadRequest)
		return
	}

	key := fmt.Sprintf("%s:%s:%s:%d:%s", exp, vm, host, remoteDst, user)

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	reapForwards()

	if l, ok := forwards[key]; ok {
		// TODO: how would we go about allowing admins to close all port forwards?
		if l.Owner != user {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if !l.QEMU {
			err := mm.CloseTunnel(mm.NS(exp), mm.VMName(vm), mm.TunnelDestinationPort(remoteDst), mm.TunnelDestinationHost(host))
			if err != nil {
				plog.Error("closing tunnel", "err", err)

				http.Error(w, "unable to close tunnel to vm", http.StatusInternalServerError)
				return
			}
		}

		deleteForward(l)

		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Error(w, "forward not found (are you the owner?)", http.StatusBadRequest)
}

// GET /experiments/{exp}/vms/{name}/forwards/{host}/{port}/ws
func GetPortForwardWebSocket(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetPortForwardWebSocket")

	var (
		ctx  = r.Context()
		user = ctx.Value("user").(string)
		role = ctx.Value("role").(rbac.Role)

		vars = mux.Vars(r)
		exp  = vars["exp"]
		vm   = vars["name"]
		host = vars["host"]
		port = vars["port"]
	)

	if !role.Allowed("vms/forwards", "get", fmt.Sprintf("%s/%s", exp, vm)) {
		plog.Warn("accessing port forwards not allowed", "user", user, "exp", exp, "vm", vm)

		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	key := fmt.Sprintf("%s:%s:%s:%s:%s", exp, vm, host, port, user)

	forwardsMu.Lock()
	reapForwards()

	// Check to see if a forward exists that this user created.
	if listener, ok := forwards[key]; ok {
		forwardsMu.Unlock()

		websocket.Handler(util.ConnectWSHandler(listener.ClusterEndpoint())).ServeHTTP(w, r)
		return
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

		plog.Error("listener not found for forward", "key", key)
		http.Error(w, "unknown forward", http.StatusBadRequest)
		return
	}

	var (
		rsrc  = rand.NewSource(time.Now().Unix())
		rando = rand.New(rsrc)
	)

	// Use a random forward so the same one doesn't get overwhelmed.
	key = matches[rando.Intn(len(matches))]
	listener := forwards[key]

	forwardsMu.Unlock()

	websocket.Handler(util.ConnectWSHandler(listener.ClusterEndpoint())).ServeHTTP(w, r)
}
