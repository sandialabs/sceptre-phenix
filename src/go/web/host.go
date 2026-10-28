package web

import (
	"encoding/json"
	"net/http"

	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// GetClusterHosts - GET /hosts.
func GetClusterHosts(w http.ResponseWriter, r *http.Request) error {
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

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	hosts, observed, err := mm.GetClusterHostsObserved(ctx, false)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	freshness.SetObserved(w, observed)

	allowed := []mm.Host{}

	for _, host := range hosts {
		if role.Allowed("hosts", "list", host.Name) {
			allowed = append(allowed, host)
		}
	}

	marshalled, err := json.Marshal(mm.Cluster{Hosts: allowed})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(marshalled)

	return nil
}
