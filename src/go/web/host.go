package web

import (
	"encoding/json"
	"net/http"

	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
)

// GetClusterHosts - GET /hosts.
func GetClusterHosts(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("hosts", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing cluster hosts not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	hosts, err := mm.GetClusterHosts(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	allowed := []mm.Host{}

	for _, host := range hosts {
		if role.Allowed("hosts", "list", host.Name) {
			allowed = append(allowed, host)
		}
	}

	marshalled, err := json.Marshal(mm.Cluster{Hosts: allowed})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(marshalled) //nolint:gosec // XSS via taint analysis
}
