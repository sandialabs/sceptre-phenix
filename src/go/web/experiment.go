package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"phenix/api/vm"
	"phenix/util/cache"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/rbac"
	"phenix/web/util"

	"github.com/gorilla/mux"
	"inet.af/netaddr"
)

type topology struct {
	Nodes   []mm.VM `json:"nodes"`
	Edges   []edge  `json:"edges"`
	Running bool    `json:"running"`
}

type edge struct {
	ID     int `json:"id"`
	Source int `json:"source"`
	Target int `json:"target"`
	Length int `json:"length"`
}

// GET /experiments/{name}/topology[?ignore=MGMT]
func GetExperimentTopology(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetExperimentTopology")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]

		query  = r.URL.Query()
		ignore = query["ignore"]
	)

	if !role.Allowed("experiments/topology", "get", name) {
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

	w.Write(body)
}

// GET /experiments/{name}/topology/search?hostname=xyz&vlan=abc
func SearchExperimentTopology(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "SearchExperimentTopology")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]

		query = r.URL.Query()
	)

	if !role.Allowed("experiments/topology", "get", name) {
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
		search = val.(vm.TopologySearch)
		nodes  []int
	)

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
					ip, err := netaddr.ParseIP(k)
					if err != nil {
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

	w.Write(body)
}
