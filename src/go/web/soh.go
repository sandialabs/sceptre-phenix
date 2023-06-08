package web

import (
	"encoding/json"
	"net/http"

	"phenix/api/soh"
	"phenix/util/plog"
	"phenix/web/rbac"

	"github.com/gorilla/mux"
)

// GET /experiments/{exp}/soh[?statusFilter=<status filter>]
func GetExperimentSoH(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetExperimentSoH")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["name"]

		query        = r.URL.Query()
		statusFilter = query.Get("statusFilter")
	)

	if !role.Allowed("vms", "list") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	state, err := soh.Get(exp, statusFilter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hosts, flows, err := soh.GetFlows(exp)
	if err == nil {
		state.Hosts = hosts
		state.HostFlows = flows
	}

	marshalled, err := json.Marshal(state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(marshalled)
}
