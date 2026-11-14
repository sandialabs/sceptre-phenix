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
	"phenix/util/mm"
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

		return weberror.New(storeStatus(err), fmt.Sprintf("unable to get experiment %s", name))
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

// ScheduleExperiment - POST /experiments/{name}/schedule. It holds the
// experiment's lock while it schedules, so no start or other change can begin,
// answers 504 when the scheduler does not finish in time and 503 when the mesh
// peers do not answer. The schedule it answers and broadcasts to the roles
// that may get the schedule carries the scheduler's warnings.
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

	if err := cache.LockExperimentForUpdate(name); err != nil {
		plog.Warn(plog.TypeSystem, "experiment locked", "exp", name, "err", err)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockExperiment(name)

	scheduled := audit.Fields{Experiment: name, Extra: []any{"phenix.schedule.algorithm", req.GetAlgorithm()}}

	exp, warnings, err := experiment.Schedule(
		ctx,
		experiment.ScheduleForName(name),
		experiment.ScheduleWithAlgorithm(req.GetAlgorithm()),
	)

	if requestCanceled(r, err) {
		return nil
	}

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

	schedule := util.ExperimentScheduleToProtobuf(*exp)
	schedule.Warnings = warnings

	body, err = marshaler.Marshal(schedule)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling schedule for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/schedule", "get", name),
		bt.NewResource("experiment", name, "schedule"),
		body,
	)

	if requestGone(r) {
		return nil
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// scheduleErrorStatus is the status a failed experiment.Schedule answers with.
func scheduleErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotExist):
		return http.StatusNotFound
	case errors.Is(err, experiment.ErrExperimentRunning), errors.Is(err, experiment.ErrExperimentStarting),
		errors.Is(err, store.ErrConflict), errors.Is(err, scheduler.ErrNoSchedulableHosts):
		return http.StatusConflict
	case errors.Is(err, scheduler.ErrUserSchedulerNotFound), errors.Is(err, scheduler.ErrInvalidName):
		return http.StatusBadRequest
	case errors.Is(err, scheduler.ErrTimeout):
		return http.StatusGatewayTimeout
	case errors.Is(err, mm.ErrMeshUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
