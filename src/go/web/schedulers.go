package web

import (
	"net/http"

	"phenix/scheduler"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// schedulersResponse is the schedulers that POST /experiments/{name}/schedule can run.
type schedulersResponse struct {
	Schedulers []scheduler.Info `json:"schedulers"`
}

// GetSchedulers - GET /schedulers: the built-in schedulers, then the user
// schedulers on the server's PATH, for a role that may schedule any experiment.
func GetSchedulers(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	if !middleware.RoleFromContext(ctx).Allowed("experiments/schedule", "create") {
		plog.Warn(plog.TypeSecurity, "listing schedulers not allowed", "user", middleware.UserFromContext(ctx))

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	return writeJSONBody(w, schedulersResponse{Schedulers: scheduler.Available()})
}
