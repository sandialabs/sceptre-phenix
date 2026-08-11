package web

import (
	"encoding/json"
	"net/http"

	"phenix/scheduler"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/util"
)

// GetSchedulers - GET /schedulers.
// Returns the list of available phenix schedulers, analogous to GET /applications.
func GetSchedulers(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetSchedulers")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("schedulers", "list") {
		plog.Warn(
			plog.TypeSecurity,
			"listing schedulers not allowed",
			"user",
			middleware.UserFromContext(ctx),
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	names := scheduler.List()

	body, err := json.Marshal(util.WithRoot("schedulers", names))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling schedulers", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}
