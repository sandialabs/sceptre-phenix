package web

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/soh"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// GetExperimentSoH handles GET requests for /experiments/{exp}/soh[?statusFilter=<status filter>].
func GetExperimentSoH(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetExperimentSoH")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["name"]

		query        = r.URL.Query()
		statusFilter = query.Get("statusFilter")
	)

	if !role.Allowed("vms", "list") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment soh not allowed",
			"user",
			user,
			"exp",
			exp,
		)
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

	_, _ = w.Write(marshalled) //nolint:gosec // XSS via taint analysis
}
