package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/soh"
	"phenix/types"
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

	state, err := soh.GetFor(stored, statusFilter)
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

	_, _ = w.Write(marshalled) //nolint:gosec // XSS via taint analysis
}

// maxSummaryProblems caps the failing checks and down VMs GetSoHSummary lists
// per experiment; the experiment's own soh endpoint has them all.
const maxSummaryProblems = 50

// lastVMStates keeps the VM states last read from minimega for each running
// experiment, for summaries asked for while minimega is busy starting or
// stopping an experiment (see anyExperimentLocked).
var lastVMStates sync.Map //nolint:gochecknoglobals // process-wide cache

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
	)

	for _, exp := range experiments {
		name := exp.Metadata.Name

		// the same check as the experiment's own soh endpoint
		if !role.Allowed("experiments", "get", name) {
			continue
		}

		states, vmErr := sohVMStates(exp, busy)

		summary, err := soh.Summarize(&exp, states, maxSummaryProblems)
		if err != nil {
			plog.Error(plog.TypeSoh, "summarizing state of health", "exp", name, "err", err)
			summary.Error = err.Error()
		}

		if vmErr != nil {
			plog.Error(plog.TypeSoh, "getting VM states for state of health", "exp", name, "err", vmErr)
			summary.VMsError = vmErr.Error()
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
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// sohVMStates returns minimega's VM states for a running experiment, or nil
// when there are none to read. While minimega is busy starting or stopping an
// experiment it returns the states it last read rather than wait.
func sohVMStates(exp types.Experiment, busy bool) (map[string]string, error) {
	name := exp.Metadata.Name

	// asking minimega about a stopped experiment would recreate its namespace
	if !exp.Running() || exp.DryRun() {
		lastVMStates.Delete(name)

		return nil, nil //nolint:nilnil // no states is not an error
	}

	if busy {
		if states, ok := lastVMStates.Load(name); ok {
			cached, _ := states.(map[string]string)

			return cached, nil
		}

		return nil, nil //nolint:nilnil // unknown until minimega is free
	}

	states, err := mm.GetVMStates(mm.NS(name))
	if err != nil {
		return nil, fmt.Errorf("getting VM states: %w", err)
	}

	lastVMStates.Store(name, states)

	return states, nil
}
