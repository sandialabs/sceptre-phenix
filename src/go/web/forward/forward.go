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
	"phenix/util/mm"
	"phenix/util/plog"
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
		for range time.Tick(10 * time.Second) {
			forwardsMu.Lock()
			reapForwards()
			forwardsMu.Unlock()
		}
	}()
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

	if host == "" {
		host = "127.0.0.1"
	}

	info := mm.GetVMInfo(mm.NS(exp), mm.VMName(vm))
	if len(info) == 0 {
		http.Error(w, "vm not found", http.StatusNotFound)
		return
	}

	localSrc, err := strconv.Atoi(src)
	if err != nil {
		plog.Error("parsing source port for forward", "port", src, "err", err)

		http.Error(w, "invalid source port", http.StatusBadRequest)
		return
	}

	remoteDst, err := strconv.Atoi(dst)
	if err != nil {
		plog.Error("parsing destination port for forward", "port", dst, "err", err)

		http.Error(w, "invalid destination port", http.StatusBadRequest)
		return
	}

	listener := ft.Listener{
		Exp: exp,
		VM:  vm,

		SrcPort: localSrc,
		DstHost: host,
		DstPort: remoteDst,
		Owner:   user,

		ClusterHost: info[0].Host,
	}

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	reapForwards()

	if _, ok := forwards[listener.ToKey()]; ok {
		http.Error(w, "forward already exists for user", http.StatusBadRequest)
		return
	}

	remoteSrc := 50000 + rand.Intn(15000)

	for {
		err = mm.CreateTunnel(mm.NS(exp), mm.VMName(vm), mm.TunnelSourcePort(remoteSrc), mm.TunnelDestinationPort(remoteDst), mm.TunnelDestinationHost(host))
		if err != nil {
			if strings.Contains(err.Error(), "bind: address already in use") {
				remoteSrc = 50000 + rand.Intn(15000) // retry with a different port
				continue
			} else {
				plog.Error("creating tunnel", "err", err)

				http.Error(w, "unable to create tunnel to vm", http.StatusInternalServerError)
				return
			}
		}

		break
	}

	listener.ClusterPort = remoteSrc
	forwards[listener.ToKey()] = listener

	// TODO: when an experiment is stopped, grab all keys starting with `<exp>:`
	// and broadcast a `delete` action for the forward so tunnelers can stop their
	// local listeners. Do something similar when specific tunnels are stopped by
	// a user.

	body, _ := json.Marshal(listener)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/forwards", "create", fmt.Sprintf("%s/%s", exp, vm)),
		bt.NewResource("experiment/vm/forward", fmt.Sprintf("%s/%s", exp, vm), "create"),
		body,
	)

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

		err := mm.CloseTunnel(mm.NS(exp), mm.VMName(vm), mm.TunnelDestinationPort(remoteDst), mm.TunnelDestinationHost(host))
		if err != nil {
			plog.Error("closing tunnel", "err", err)

			http.Error(w, "unable to close tunnel to vm", http.StatusInternalServerError)
			return
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
