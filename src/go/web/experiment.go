package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"inet.af/netaddr"

	"phenix/api/vm"
	"phenix/util/cache"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

// GetExperimentTopology - GET /experiments/{name}/topology[?ignore=MGMT].
func GetExperimentTopology(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetExperimentTopology")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = vars["name"]

		query  = r.URL.Query()
		ignore = query["ignore"]
	)

	if !role.Allowed("experiments/topology", "get", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment topology not allowed",
			"user",
			user,
			"experiment",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	topo, err := vm.Topology(name, ignore)
	if err != nil {
		http.Error(w, "unable to get experiment topology", http.StatusBadRequest)
	}

	body, err := json.Marshal(topo)
	if err != nil {
		http.Error(w, "unable to convert topology", http.StatusInternalServerError)
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// SearchExperimentTopology - GET /experiments/{name}/topology/search?hostname=xyz&vlan=abc.
func SearchExperimentTopology(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "SearchExperimentTopology")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = vars["name"]

		query = r.URL.Query()
	)

	if !role.Allowed("experiments/topology", "get", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"searching experiment topology not allowed",
			"user",
			user,
			"experiment",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	cacheKey := fmt.Sprintf("experiment|%s|search", name)

	val, ok := cache.Get(cacheKey)
	if !ok {
		if _, err := vm.Topology(name, nil); err != nil {
			http.Error(w, "error getting experiment topology", http.StatusBadRequest)

			return
		}

		val, _ = cache.Get(cacheKey)
	}

	var (
		search, _ = val.(vm.TopologySearch)
		nodes     []int
	)

	//nolint:godox // TODO
	// TODO: how to handle multiple terms? AND or OR?

	for term, values := range query {
		value := values[0]

		switch strings.ToLower(term) {
		case "hostname":
			if node, ok := search.Hostname[value]; ok {
				nodes = append(nodes, node)
			}
		case "disk":
			nodes = append(nodes, search.Disk[value]...)
		case "node-type":
			nodes = append(nodes, search.Type[value]...)
		case "os-type":
			nodes = append(nodes, search.OSType[value]...)
		case "label":
			nodes = append(nodes, search.Label[value]...)
		case "annotation":
			nodes = append(nodes, search.Annotation[value]...)
		case "vlan":
			nodes = append(nodes, search.VLAN[value]...)
		case "ip":
			if net, err := netaddr.ParseIPPrefix(value); err == nil {
				for k, v := range search.IP {
					ip, ipErr := netaddr.ParseIP(k)
					if ipErr != nil {
						continue
					}

					if net.Contains(ip) {
						nodes = append(nodes, v...)
					}
				}
			} else {
				nodes = append(nodes, search.IP[value]...)
			}
		}
	}

	body, err := json.Marshal(util.WithRoot("nodes", nodes))
	if err != nil {
		http.Error(w, "error marshaling search results", http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}
