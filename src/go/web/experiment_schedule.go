package web

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/util"
)

// GetExperimentSchedule - GET /experiments/{name}/schedule.
func GetExperimentSchedule(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/schedule", "get", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment schedule not allowed",
			"user",
			user,
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if status := cache.IsExperimentLocked(name); status != "" {
		plog.Warn(plog.TypeSystem, "experiment locked", "exp", name, "status", status)
		http.Error(
			w,
			fmt.Sprintf("experiment %s is cache.Locked with status %s", name, status),
			http.StatusConflict,
		)

		return
	}

	exp, err := experiment.Get(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	body, err := marshaler.Marshal(util.ExperimentScheduleToProtobuf(*exp))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling schedule for experiment", "exp", name, "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	//nolint:gosec // XSS via taint analysis
	_, _ = w.Write(body)
}

// ScheduleExperiment - POST /experiments/{name}/schedule.
//
//nolint:funlen // handler
func ScheduleExperiment(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/schedule", "create", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"creating experiment schedule not allowed",
			"user",
			user,
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if status := cache.IsExperimentLocked(name); status != "" {
		plog.Warn(plog.TypeSystem, "experiment locked", "exp", name, "status", status)
		http.Error(
			w,
			fmt.Sprintf("experiment %s is cache.Locked with status %s", name, status),
			http.StatusConflict,
		)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.UpdateScheduleRequest

	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	err = experiment.Schedule(
		experiment.ScheduleForName(name),
		experiment.ScheduleWithAlgorithm(req.GetAlgorithm()),
	)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"scheduling experiment",
			"exp",
			name,
			"algorithm",
			req.GetAlgorithm(),
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	body, err = marshaler.Marshal(util.ExperimentScheduleToProtobuf(*exp))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling schedule for experiment", "exp", name, "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/schedule", "create", name),
		bt.NewResource("experiment", name, "schedule"),
		body,
	)
	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment schedule created",
		"user",
		user,
		"exp",
		name,
		"algorithm",
		req.GetAlgorithm(),
	)

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}
