package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/scheduler"
	"phenix/store"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/util"
	"phenix/web/weberror"
)

// GetExperimentSchedule - GET /experiments/{name}/schedule.
func GetExperimentSchedule(w http.ResponseWriter, r *http.Request) error {
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

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if status := cache.IsExperimentLocked(name); status != "" {
		plog.Warn(plog.TypeSystem, "experiment locked", "exp", name, "status", status)

		return weberror.New(http.StatusConflict, fmt.Sprintf("experiment %s is locked with status %s", name, status))
	}

	exp, err := experiment.Get(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err := marshaler.Marshal(util.ExperimentScheduleToProtobuf(*exp))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling schedule for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// ScheduleExperiment - POST /experiments/{name}/schedule.
func ScheduleExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/schedule", "create", name) {
		audit.Denied(r, audit.ExperimentSchedule, "creating experiment schedule not allowed", audit.Fields{Experiment: name})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if status := cache.IsExperimentLocked(name); status != "" {
		plog.Warn(plog.TypeSystem, "experiment locked", "exp", name, "status", status)

		return weberror.New(http.StatusConflict, fmt.Sprintf("experiment %s is locked with status %s", name, status))
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.UpdateScheduleRequest

	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	scheduled := audit.Fields{Experiment: name, Extra: []any{"phenix.schedule.algorithm", req.GetAlgorithm()}}

	err = experiment.Schedule(
		experiment.ScheduleForName(name),
		experiment.ScheduleWithAlgorithm(req.GetAlgorithm()),
	)
	if err != nil {
		audit.Failure(r, audit.ExperimentSchedule, "scheduling experiment failed", scheduled, err)
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

		return weberror.New(scheduleErrorStatus(err), err.Error())
	}

	audit.Success(r, audit.ExperimentSchedule, "experiment scheduled", scheduled)

	exp, err := experiment.Get(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	body, err = marshaler.Marshal(util.ExperimentScheduleToProtobuf(*exp))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling schedule for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/schedule", "create", name),
		bt.NewResource("experiment", name, "schedule"),
		body,
	)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// scheduleErrorStatus is the status a failed experiment.Schedule answers with.
func scheduleErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotExist):
		return http.StatusNotFound
	case errors.Is(err, experiment.ErrExperimentRunning), errors.Is(err, scheduler.ErrNoSchedulableHosts):
		return http.StatusConflict
	case errors.Is(err, scheduler.ErrUserSchedulerNotFound):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
