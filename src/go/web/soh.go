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
	"golang.org/x/sync/errgroup"

	"phenix/api/experiment"
	"phenix/api/soh"
	"phenix/types"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
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

		query = r.URL.Query()
	)

	if !role.Allowed("experiments", "get", exp) || !role.Allowed("vms", "list") {
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

	statusFilter, err := enumQuery(query, "statusFilter", "", soh.StatusFilters()...)
	if err != nil {
		return err
	}

	// read the experiment once for both the network and the flows
	stored, err := experiment.Get(exp)
	if err != nil {
		return weberror.New(storeStatus(err), "unable to get experiment "+exp)
	}

	state, err := soh.GetFor(ctx, stored, statusFilter)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	hosts, flows, err := soh.GetFlowsFor(stored)
	if err == nil {
		state.Hosts = append(state.Hosts, hosts...)
		state.HostFlows = append(state.HostFlows, flows...)
	}

	marshalled, err := json.Marshal(state)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(marshalled)

	return nil
}

// maxSummaryProblems caps the failing checks and down VMs GetSoHSummary lists
// per experiment; the experiment's own soh endpoint has them all.
const maxSummaryProblems = 50

// lastVMStates keeps the VM states last read from minimega for each running
// experiment, for summaries asked for while minimega answers slowly, such as
// while it starts or stops an experiment (see sohStateReads). The summary
// reports their age and marks them stale.
var lastVMStates = vmStatesCache{ //nolint:gochecknoglobals // process-wide cache
	mu: sync.Mutex{}, reads: map[string]vmStatesRead{}, gens: map[string]uint64{},
}

// vmStatesCache holds the last vmStatesRead of each experiment. forget bumps
// the experiment's generation, so a read that started before it stores
// nothing.
type vmStatesCache struct {
	mu    sync.Mutex
	reads map[string]vmStatesRead
	gens  map[string]uint64
}

func (c *vmStatesCache) load(name string) (vmStatesRead, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	read, ok := c.reads[name]

	return read, ok
}

// generation is the generation of name, for a read that starts now.
func (c *vmStatesCache) generation(name string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.gens[name]
}

// store keeps read for name if name is still at generation gen.
func (c *vmStatesCache) store(name string, gen uint64, read vmStatesRead) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.gens[name] == gen {
		c.reads[name] = read
	}
}

// forget removes the states of name and bumps its generation.
func (c *vmStatesCache) forget(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.reads, name)
	c.gens[name]++
}

// sohStateReads runs one read of the VM states of each experiment at a time,
// for the summaries (see liveReads).
var sohStateReads = newLiveReads[vmStatesResult]() //nolint:gochecknoglobals // shared by every summary

// vmStatesResult is one read of an experiment's VM states, with its error.
type vmStatesResult struct {
	vmStatesRead

	err error
}

// vmStatesRead is one read of an experiment's VM states, and when it started.
type vmStatesRead struct {
	states map[string]string
	at     time.Time
}

// GetSoHSummary - GET /soh.
//
// Summarizes the state of health of every experiment the caller may get: one
// narrow minimega query per running experiment, the rest from the store.
func GetSoHSummary(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetSoHSummary")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	// the summaries name down VMs, so they also need the vms list of each experiment's own soh endpoint
	if !role.Allowed("experiments", "list") || !role.Allowed("vms", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(plog.TypeSecurity, "getting state of health summary not allowed", "user", user)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	experiments, err := experiment.List()
	if err != nil {
		// List still returns the experiments it could decode
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)
	}

	var (
		summaries = make([]soh.Summary, 0, len(experiments))
		observed  = freshness.Unobserved()
	)

	// the experiments get check of the experiment's own soh endpoint
	experiments = slices.DeleteFunc(experiments, func(exp types.Experiment) bool {
		return !role.Allowed("experiments", "get", exp.Metadata.Name)
	})

	reads := readSoHVMStates(ctx, experiments)

	for i, exp := range experiments {
		name := exp.Metadata.Name

		states, obs, vmErr := reads[i].states, reads[i].obs, reads[i].err
		if requestCanceled(r, vmErr) {
			return nil
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

		status, progress := experimentStatus(exp)
		summary.Status = string(status)
		summary.Progress = progress

		if progress != nil {
			summary.Percent = progress.Percent
		}

		summaries = append(summaries, summary)
	}

	slices.SortFunc(summaries, func(a, b soh.Summary) int { return strings.Compare(a.Name, b.Name) })

	body, err := json.Marshal(map[string]any{"experiments": summaries})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling state of health summary", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	w.Header().Set("Content-Type", "application/json")
	freshness.SetObserved(w, observed)
	_, _ = w.Write(body)

	return nil
}

// sohStateRead is one result of sohVMStates.
type sohStateRead struct {
	states map[string]string
	obs    freshness.Observation
	err    error
}

// readSoHVMStates reads the VM states of the experiments with sohVMStates, in
// their order, up to mmcli.ReadOnlyLimit() at once, waiting at most one wait in all.
func readSoHVMStates(ctx context.Context, experiments []types.Experiment) []sohStateRead {
	var (
		reads = make([]sohStateRead, len(experiments))
		group errgroup.Group
	)

	group.SetLimit(mmcli.ReadOnlyLimit())

	ctx, cancel := sohStateReads.waitContext(ctx)
	defer cancel()

	for i, exp := range experiments {
		group.Go(func() error {
			reads[i].states, reads[i].obs, reads[i].err = sohVMStates(ctx, exp)

			return nil
		})
	}

	_ = group.Wait()

	return reads
}

// sohVMStates reads the VM states of exp for a summary, waiting for minimega
// at most liveReadWait. Past it, it gives the states last read, marked stale,
// or unknown states, while the read goes on and keeps its states for later
// summaries (see sohStateReads). It asks nothing about an experiment that is
// stopped or a dry run.
func sohVMStates(ctx context.Context, exp types.Experiment) (map[string]string, freshness.Observation, error) {
	name := exp.Metadata.Name

	// asking minimega about a stopped experiment would recreate its namespace
	if !exp.Running() || exp.DryRun() {
		lastVMStates.forget(name)

		return nil, freshness.Unobserved(), nil
	}

	read, ok, panicErr := sohStateReads.get(ctx, name, func(ctx context.Context) vmStatesResult {
		at, gen := time.Now(), lastVMStates.generation(name)

		states, err := mm.GetVMStates(mm.NS(name), mm.Context(ctx))
		if err == nil {
			lastVMStates.store(name, gen, vmStatesRead{states: states, at: at})
		}

		return vmStatesResult{vmStatesRead: vmStatesRead{states: states, at: at}, err: err}
	})
	if panicErr != nil {
		read.err = panicErr
	}

	switch {
	case ok && read.err != nil:
		return nil, freshness.Observation{At: read.at, Stale: false, Known: false, Err: read.err},
			fmt.Errorf("getting VM states: %w", read.err)
	case ok:
		return read.states, freshness.Observed(read.at), nil
	case ctx.Err() != nil && !waitPassed(ctx):
		return nil, freshness.Observation{At: time.Time{}, Stale: false, Known: false, Err: ctx.Err()},
			fmt.Errorf("getting VM states: %w", ctx.Err())
	}

	if earlier, found := lastVMStates.load(name); found {
		obs := freshness.Observed(earlier.at)
		obs.Stale = true

		return earlier.states, obs, nil
	}

	// unknown until minimega answers
	return nil, freshness.Observation{At: time.Time{}, Stale: true, Known: false, Err: nil}, nil
}
