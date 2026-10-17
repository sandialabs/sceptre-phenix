package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/soh"
	"phenix/types"
	"phenix/util/freshness"
	"phenix/util/mm"
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// read the experiment once for both the network and the flows
	stored, err := experiment.Get(exp)
	if err != nil {
		http.Error(w, fmt.Sprintf("unable to get experiment %s: %v", exp, err), http.StatusInternalServerError)

		return
	}

	state, err := soh.GetFor(ctx, stored, statusFilter)
	if requestCanceled(r, err) {
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	hosts, flows, err := soh.GetFlowsFor(stored)
	if err == nil {
		state.Hosts = hosts
		state.HostFlows = flows
	}

	marshalled, err := json.Marshal(state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(marshalled) //nolint:gosec // XSS via taint analysis
}

// maxSummaryProblems caps the failing checks and down VMs GetSoHSummary lists
// per experiment; the experiment's own soh endpoint has them all.
const maxSummaryProblems = 50

// lastVMStates keeps the VM states last read from minimega for each running
// experiment (a vmStatesRead), for summaries asked for while minimega is busy
// starting or stopping an experiment (see anyExperimentLocked). The summary
// reports their age and marks them stale.
var lastVMStates sync.Map //nolint:gochecknoglobals // process-wide cache

// vmStatesRead is one read of an experiment's VM states, and when it started.
type vmStatesRead struct {
	states map[string]string
	at     time.Time
}

// GetSoHSummary - GET /soh.
//
// Summarizes the state of health of every experiment the caller may get: one
// narrow minimega query per running experiment, the rest from the store.
func GetSoHSummary(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetSoHSummary")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("experiments", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(plog.TypeSecurity, "getting state of health summary not allowed", "user", user)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	experiments, err := experiment.List()
	if err != nil {
		// List still returns the experiments it could decode
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)
	}

	var (
		busy      = anyExperimentLocked(experiments)
		summaries = make([]soh.Summary, 0, len(experiments))
		observed  = freshness.Unobserved()
	)

	for _, exp := range experiments {
		name := exp.Metadata.Name

		// the same check as the experiment's own soh endpoint
		if !role.Allowed("experiments", "get", name) {
			continue
		}

		states, obs, vmErr := sohVMStates(ctx, exp, busy)
		if requestCanceled(r, vmErr) {
			return
		}

		observed = observed.Older(obs)

		summary, err := soh.Summarize(&exp, states, maxSummaryProblems)
		if err != nil {
			plog.Error(plog.TypeSoh, "summarizing state of health", "exp", name, "err", err)
			summary.Error = err.Error()
		}

		if vmErr != nil {
			plog.Error(plog.TypeSoh, "getting VM states for state of health", "exp", name, "err", vmErr)
			summary.VMsError = vmErr.Error()
		}

		if !obs.At.IsZero() {
			summary.VMsObservedAt = freshness.FormatTime(obs.At)
			summary.VMsStale = obs.Stale
		}

		status, percent := experimentStatus(exp)
		summary.Status = string(status)
		summary.Percent = percent

		summaries = append(summaries, summary)
	}

	slices.SortFunc(summaries, func(a, b soh.Summary) int { return strings.Compare(a.Name, b.Name) })

	body, err := json.Marshal(map[string]any{"experiments": summaries})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling state of health summary", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	freshness.SetObserved(w, observed)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// sohVMStates returns minimega's VM states for a running experiment, or nil
// when there are none to read, and when they were read. While minimega is busy
// starting or stopping an experiment it returns the states it last read,
// marked stale, rather than wait; with none read yet, they are unknown.
// Its minimega command gives up once ctx is done (see mm.Context).
func sohVMStates(ctx context.Context, exp types.Experiment, busy bool) (map[string]string, freshness.Observation, error) {
	name := exp.Metadata.Name

	// asking minimega about a stopped experiment would recreate its namespace
	if !exp.Running() || exp.DryRun() {
		lastVMStates.Delete(name)

		return nil, freshness.Unobserved(), nil
	}

	if busy {
		if last, ok := lastVMStates.Load(name); ok {
			read, _ := last.(vmStatesRead)

			obs := freshness.Observed(read.at)
			obs.Stale = true

			return read.states, obs, nil
		}

		// unknown until minimega is free
		return nil, freshness.Observation{At: time.Time{}, Stale: true, Known: false, Err: nil}, nil
	}

	at := time.Now()

	states, err := mm.GetVMStates(mm.NS(name), mm.Context(ctx))
	if err != nil {
		return nil, freshness.Observation{At: at, Stale: false, Known: false, Err: err},
			fmt.Errorf("getting VM states: %w", err)
	}

	lastVMStates.Store(name, vmStatesRead{states: states, at: at})

	return states, freshness.Observed(at), nil
}
