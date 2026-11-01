package web

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/soh"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// GetExperimentSoH handles GET requests for /experiments/{exp}/soh[?statusFilter=<status filter>].
func GetExperimentSoH(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetExperimentSoH")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["name"]

		query        = r.URL.Query()
		statusFilter = query.Get("statusFilter")
	)

	if !role.Allowed("experiments", "get", exp) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment soh not allowed",
			"user",
			user,
			"exp",
			exp,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	state, err := soh.Get(exp, statusFilter)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	hosts, flows, err := soh.GetFlows(exp)
	if err == nil {
		state.Hosts = hosts
		state.HostFlows = flows
	}

	marshalled, err := json.Marshal(state)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(marshalled)

	return nil
}
