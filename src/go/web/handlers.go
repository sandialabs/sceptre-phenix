package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"phenix/api/config"
	"phenix/api/disk"
	"phenix/api/experiment"
	"phenix/api/scenario"
	"phenix/api/settings"
	"phenix/api/vm"
	"phenix/app"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	putil "phenix/util"
	"phenix/util/common"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/util"
	"phenix/web/weberror"
)

var (
	marshaler   = protojson.MarshalOptions{EmitUnpopulated: true}                      //nolint:gochecknoglobals // global marshaler
	unmarshaler = protojson.UnmarshalOptions{AllowPartial: true, DiscardUnknown: true} //nolint:gochecknoglobals // global unmarshaler
)

const sortAsc = "asc"
const percentDivisor = 100
const defaultScreenshotSize = "215"

// GetExperiments - GET /experiments.
//
// With vms=false the VMs are left out and nothing is asked of minimega: the
// counts come from each topology.
func GetExperiments(w http.ResponseWriter, r *http.Request) {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		query = r.URL.Query()
		size  = query.Get("screenshot")
		noVMs = query.Get("vms") == "false" && size == ""
	)

	if !role.Allowed("experiments", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing experiments not allowed",
			"user",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	experiments, err := experiment.List()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)

		// an empty list would read as no experiments
		if len(experiments) == 0 {
			http.Error(w, "unable to read experiments from the store", http.StatusInternalServerError)

			return
		}
	}

	allowed := []*proto.Experiment{}

	busy := anyExperimentLocked(experiments)

	// when minimega was read for the VMs listed, and whether it answered
	observed := freshness.Unobserved()

	// some experiments could not be read, so the list is incomplete
	observed.Stale = err != nil

	for _, exp := range experiments {
		if !role.Allowed("experiments", "list", exp.Metadata.Name) {
			continue
		}

		status, percent := experimentStatus(exp)

		var pb *proto.Experiment

		if noVMs {
			pb = util.ExperimentSummaryToProtobuf(exp, status)
		} else {
			// TODO: limit per-experiment VMs based on RBAC
			vms, obs := listExperimentVMs(ctx, exp, busy && size == "")
			observed = observed.Older(obs)

			if exp.Running() && size != "" {
				addVMScreenshots(ctx, exp.Spec.ExperimentName(), vms, size)
			}

			pb = util.ExperimentToProtobuf(exp, status, vms)
			pb.VmsUnknown = !obs.Known
		}

		pb.Percent = percent
		allowed = append(allowed, pb)
	}

	if requestGone(r) {
		return
	}

	body, err := marshaler.Marshal(&proto.ExperimentList{Experiments: allowed})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling experiments", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	freshness.SetObserved(w, observed)
	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)
}

// addVMScreenshots sets each running VM's screenshot, at the given size, in
// place, until ctx is done.
func addVMScreenshots(ctx context.Context, expName string, vms []mm.VM, size string) {
	for i, v := range vms {
		if !v.Running {
			continue
		}

		screenshot, err := util.GetScreenshotContext(ctx, expName, v.Name, size)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			plog.Error(plog.TypeSystem, "getting screenshot", "err", err)

			continue
		}

		vms[i].Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
			screenshot,
		)
	}
}

// anyExperimentLocked reports whether a handler holds the lock of any of the
// experiments, as while one starts or stops. Starting or stopping keeps
// minimega busy for as long as it takes, so asking it about VMs meanwhile
// would stall the experiment list until it finishes.
func anyExperimentLocked(experiments []types.Experiment) bool {
	for _, exp := range experiments {
		if cache.IsExperimentLocked(exp.Metadata.Name) != "" {
			return true
		}
	}

	return false
}

// listExperimentVMs lists the experiment's VMs, from its topology alone when
// minimega is busy, and how minimega was read for them. A running
// experiment's VMs listed from the topology are unknown, and stale.
func listExperimentVMs(ctx context.Context, exp types.Experiment, busy bool) ([]mm.VM, freshness.Observation) {
	if busy {
		obs := freshness.Unobserved()

		if exp.Running() {
			obs.Known, obs.Stale = false, true
		}

		return vm.ListConfigured(exp), obs
	}

	return vm.ListForObserved(ctx, &exp)
}

// CreateExperiment - POST /experiments.
//
//nolint:funlen // handler
func CreateExperiment(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("experiments", "create") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"creating experiments not allowed",
			"user",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	var req proto.CreateExperimentRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	annotations, nodeAnnotations, err := createAnnotations(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if _, ok := nodeAnnotations[autotunnelAnnotation]; ok {
		vms, err := topologyVMs(req.GetTopology())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		if denied := forwardDeniedVM(role, req.GetName(), vms); denied != "" {
			plog.Warn(
				plog.TypeSecurity,
				"creating port forwards not allowed",
				"user",
				middleware.UserFromContext(ctx),
				"exp",
				req.GetName(),
				"vm",
				denied,
			)
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}
	}

	if err := cache.LockExperimentForCreation(req.GetName()); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking experiment",
			"exp",
			req.GetName(),
			"action",
			"creation",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockExperiment(req.GetName())

	deployMode, err := common.ParseDeployMode(req.GetDeployMode())
	if err != nil {
		plog.Warn(
			plog.TypeSystem,
			fmt.Sprintf(
				"error parsing experiment deploy mode ('%s') - using default of '%s'",
				req.GetDeployMode(),
				common.DeployMode,
			),
		)
		deployMode = common.DeployMode
	}

	opts := []experiment.CreateOption{
		experiment.CreateWithName(req.GetName()),
		experiment.CreateWithTopology(req.GetTopology()),
		experiment.CreateWithScenario(req.GetScenario()),
		experiment.CreateWithVLANMin(int(req.GetVlanMin())),
		experiment.CreateWithVLANMax(int(req.GetVlanMax())),
		experiment.CreatedWithDisabledApplications(req.GetDisabledApps()),
		experiment.CreateWithDeployMode(deployMode),
		experiment.CreateWithDefaultBridge(req.GetDefaultBridge()),
		experiment.CreateWithGREMesh(req.GetUseGreMesh()),
		experiment.CreateWithAnnotations(annotations),
		experiment.CreateWithNodeAnnotations(nodeAnnotations),
	}

	if err := experiment.Create(ctx, opts...); err != nil {
		plog.Error(plog.TypeSystem, "creating experiment", "exp", req.GetName(), "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if warns := notes.Warnings(ctx, true); warns != nil {
		for _, warn := range warns {
			plog.Warn(plog.TypeSystem, "creating experiment", "warnings", warn)
		}
	}

	exp, err := experiment.Get(req.GetName())
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment", "exp", req.GetName(), "err", err)
		http.Error(w, "", http.StatusInternalServerError)

		return
	}

	// broadcast to every client, so read even once this request's client left
	vms := vm.ListFor(context.WithoutCancel(ctx), exp)

	body, err = marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vms))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling experiment", "err", req.GetName(), "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", req.GetName()),
		bt.NewResource("experiment", req.GetName(), "create"),
		body,
	)

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"new experiment created",
		"user",
		user,
		"experiment",
		req.GetName(),
	)
	w.WriteHeader(http.StatusNoContent)
}

// createAnnotations returns the experiment and node annotations a create
// request asks for. The workflow branch is stored as an experiment annotation,
// so the request may not also give that annotation.
func createAnnotations(
	req *proto.CreateExperimentRequest,
) (map[string]string, map[string]any, error) {
	const branchKey = "phenix.workflow/branch"

	annotations := maps.Clone(req.GetAnnotations())

	if branch := req.GetWorkflowBranch(); branch != "" {
		if _, ok := annotations[branchKey]; ok {
			return nil, nil, fmt.Errorf(
				"experiment annotation %q is set by workflow_branch; give only one of them",
				branchKey,
			)
		}

		if annotations == nil {
			annotations = make(map[string]string, 1)
		}

		annotations[branchKey] = branch
	}

	var nodeAnnotations map[string]any

	if len(req.GetNodeAnnotations()) > 0 {
		nodeAnnotations = make(map[string]any, len(req.GetNodeAnnotations()))

		for key, value := range req.GetNodeAnnotations() {
			nodeAnnotations[key] = value.AsInterface()
		}
	}

	return annotations, nodeAnnotations, nil
}

// UpdateExperiment - PATCH /experiments/{name}.
func UpdateExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments", "patch", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"updating experiment not allowed",
			"user",
			user,
			"experiment",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"updating experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotExist) {
			status = http.StatusNotFound
		}

		return weberror.NewWebError(err, "unable to get experiment %s details", name).SetStatus(status)
	}

	if exp.Running() {
		err := weberror.NewWebError(nil, "cannot update running experiment %s", name)

		return err.SetStatus(http.StatusBadRequest)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		err := weberror.NewWebError(err, "unable to read update request for experiment %s", name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	var vlans map[string]int

	// a malformed body is the client's mistake
	if err := json.Unmarshal(body, &vlans); err != nil {
		return weberror.NewWebError(err, "unable to parse update request for experiment %s", name)
	}

	aliases := exp.Spec.VLANs().Aliases()

	if len(vlans) > 0 {
		for alias, id := range vlans {
			if _, ok := aliases[alias]; ok {
				aliases[alias] = id
			}
		}

		exp.Spec.VLANs().SetAliases(aliases)

		err := exp.WriteToStore(false)
		if err != nil {
			err := weberror.NewWebError(err, "unable to write updated experiment %s", name)

			return err.SetStatus(http.StatusInternalServerError)
		}
	}

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment updated",
		"user",
		user,
		"experiment",
		name,
	)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// GetExperiment - GET /experiments/{name}.
//
// With vms=false the VMs are left out and nothing is asked of minimega:
// vm_count and delayed_vms come from the topology. The running experiment page
// uses it, as its VM rows arrive over the websocket (experiment/vms list).
func GetExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx          = r.Context()
		role         = middleware.RoleFromContext(ctx)
		vars         = mux.Vars(r)
		name         = vars["name"]
		query        = r.URL.Query()
		size         = query.Get("screenshot")
		noVMs        = query.Get("vms") == "false" && size == ""
		sortCol      = query.Get("sortCol")
		sortDir      = query.Get("sortDir")
		pageNum      = query.Get("pageNum")
		perPage      = query.Get("perPage")
		showDNB      = query.Get("show_dnb") != ""
		clientFilter = query.Get("filter")
	)

	if !role.Allowed("experiments", "get", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting  experiment not allowed",
			"user",
			user,
			"experiment",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"getting experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	page, pageSize, err := util.ParsePage(pageNum, perPage)
	if err != nil {
		return weberror.NewWebError(nil, "%v", err).SetStatus(http.StatusBadRequest)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	var (
		vms      []mm.VM
		observed = freshness.Unobserved()
	)

	if noVMs {
		vms = vm.ListConfigured(*exp)
	} else {
		vms, observed = vm.ListForObserved(ctx, exp)
	}

	// This will happen if another handler is currently acting on the
	// experiment.
	status := cache.IsExperimentLocked(name)

	vmQuery := util.VMQuery{
		Filter:  clientFilter,
		ShowDNB: showDNB,
		SortCol: sortColumn(sortCol, sortDir),
		SortAsc: sortDir == sortAsc,
		Page:    page,
		Size:    pageSize,
	}

	allowed, total := util.SelectVMs(name, vms, exp.Spec.Topology(), vmQuery, role)

	if size != "" {
		addVMScreenshots(ctx, name, allowed, size)
	}

	if requestGone(r) {
		return nil
	}

	experiment := util.ExperimentToProtobuf(*exp, status, allowed)
	experiment.VmCount = uint32(total) //nolint:gosec // integer overflow conversion int -> uint32

	if noVMs {
		experiment.Vms = nil
	} else {
		experiment.VmsUnknown = !observed.Known
		freshness.SetObserved(w, observed)
	}

	body, err := marshaler.Marshal(experiment)
	if err != nil {
		err := weberror.NewWebError(err, "marshaling experiment %s - %v", name, err)

		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// sortColumn is the column a REST list is sorted by: none unless both sortCol
// and sortDir are given.
func sortColumn(col, dir string) string {
	if dir == "" {
		return ""
	}

	return col
}

// DeleteExperiment - DELETE /experiments/{name}.
func DeleteExperiment(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments", "delete", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"deleting experiment not allowed",
			"user",
			user,
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := cache.LockExperimentForDeletion(name)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking experiment",
			"exp",
			name,
			"action",
			"deletion",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockExperiment(name)

	err = experiment.Delete(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "deleting experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "delete", name),
		bt.NewResource("experiment", name, "delete"),
		nil,
	)

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"deleted experiment",
		"user",
		user,
		"exp",
		name,
	)
	w.WriteHeader(http.StatusNoContent)
}

// StartExperiment - POST /experiments/{name}/start.
//

func StartExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/start", "update", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"starting experiment not allowed",
			"user",
			user,
			"exp",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"starting experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	if err := startExperiment(name); err != nil {
		return err
	}

	// The broadcast leaves the VM list out; API callers still get it.
	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s", name)
	}

	body, err := marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vm.ListFor(ctx, exp)))
	if err != nil {
		return weberror.NewWebError(err, "unable to marshal experiment %s", name)
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment started",
		"user",
		user,
		"exp",
		name,
	)

	return nil
}

// StopExperiment - POST /experiments/{name}/stop.
//

func StopExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/stop", "update", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"stopping experiment not allowed",
			"user",
			user,
			"exp",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"stopping experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	body, err := stopExperiment(name)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment stopped",
		"user",
		user,
		"exp",
		name,
	)

	return nil
}

// TriggerExperimentApps - POST /experiments/{name}/trigger[?apps=<foo,bar,baz>].
func TriggerExperimentApps(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
	)

	if !role.Allowed("experiments/trigger", "create", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"triggering experiment apps not allowed",
			"user",
			user,
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	go func() {
		var (
			md   = make(map[string]any)
			apps = strings.Split(appsFilter, ",")
		)

		for k, v := range query {
			md[k] = v
		}

		for _, a := range apps {
			pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
				Experiment: name, App: a, State: "start",
			})

			k := fmt.Sprintf("%s/%s", name, a)

			// We don't want to use the HTTP request's context here.
			ctx, cancel := context.WithCancel(context.Background())
			ctx = app.SetContextTriggerUI(ctx)
			ctx = app.SetContextMetadata(ctx, md)

			commonMu.Lock()
			cancelers[k] = append(cancelers[k], cancel)
			commonMu.Unlock()

			err := experiment.TriggerRunning(ctx, name, a)
			if err != nil {
				cancel() // avoid leakage
				commonMu.Lock()
				delete(cancelers, k)
				commonMu.Unlock()

				humanized := putil.HumanizeError(
					err,
					"Unable to trigger running stage for %s app in %s experiment",
					a,
					name,
				)
				pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
					Experiment: name, App: a, State: "error", Error: humanized,
				})

				plog.Error(
					plog.TypeSystem,
					"triggering experiment app",
					"exp",
					name,
					"app",
					a,
					"err",
					err,
				)

				return
			}

			pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
				Experiment: name, App: a, State: "success",
			})
		}
	}()

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment apps triggered",
		"user",
		user,
		"exp",
		name,
		"appsFilter",
		appsFilter,
	)
	w.WriteHeader(http.StatusNoContent)
}

// CancelTriggeredExperimentApps - DELETE /experiments/{name}/trigger[?apps=<foo,bar,baz>].
func CancelTriggeredExperimentApps(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
	)

	if !role.Allowed("experiments/trigger", "delete", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"canceling triggered experiment apps not allowed",
			"user",
			user,
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	go func() {
		apps := strings.SplitSeq(appsFilter, ",")

		for a := range apps {
			k := fmt.Sprintf("%s/%s", name, a)

			commonMu.Lock()
			cancels := cancelers[k]
			delete(cancelers, k)
			commonMu.Unlock()

			for _, cancel := range cancels {
				cancel()
			}

			pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
				Experiment: name, Verb: "delete", App: a, State: "success",
			})
		}
	}()

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment apps trigger canceled",
		"user",
		user,
		"exp",
		name,
		"appsFilter",
		appsFilter,
	)

	w.WriteHeader(http.StatusNoContent)
}

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

	w.Header().Set("Content-Type", mimeJSON)
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

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetExperimentCaptures - GET /experiments/{name}/captures.
func GetExperimentCaptures(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/captures", "list", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing experiment captures not allowed",
			"user",
			user,
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	var (
		captures []mm.Capture
		allowed  []mm.Capture
	)

	// A stopped experiment has no captures, and asking minimega would
	// recreate its namespace (see experiment.Running).
	if experiment.Running(name) {
		captures = mm.GetExperimentCaptures(mm.NS(name), mm.Context(ctx))
	}

	if requestGone(r) {
		return
	}

	for _, capture := range captures {
		if role.Allowed("experiments/captures", "list", name+"/"+capture.VM) {
			allowed = append(allowed, capture)
		}
	}

	body, err := marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(allowed)})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling captures for experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	//nolint:gosec // XSS via taint analysis
	_, _ = w.Write(body)
}

// GetExperimentFiles - GET /experiments/{name}/files.
func GetExperimentFiles(w http.ResponseWriter, r *http.Request) {
	var (
		ctx          = r.Context()
		role         = middleware.RoleFromContext(ctx)
		vars         = mux.Vars(r)
		name         = vars["name"]
		query        = r.URL.Query()
		sortCol      = query.Get("sortCol")
		sortDir      = query.Get("sortDir")
		pageNum      = query.Get("pageNum")
		perPage      = query.Get("perPage")
		clientFilter = query.Get("filter")
	)

	if !role.Allowed("experiments/files", "list", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing experiment files not allowed",
			"user",
			user,
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	page, pageSize, err := util.ParsePage(pageNum, perPage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	files, observed, err := experiment.FilesObserved(ctx, name, clientFilter)
	if requestCanceled(r, err) {
		return
	}

	if err != nil {
		// minimega did not list every node's files; an empty or partial list
		// would read as files missing
		plog.Error(plog.TypeSystem, "getting list of files for experiment", "exp", name, "err", err)
		http.Error(w, "could not list files: "+err.Error(), http.StatusBadGateway)

		return
	}

	freshness.SetObserved(w, observed)

	if sortCol != "" && sortDir != "" {
		files.SortBy(sortCol, sortDir == "asc")
	}

	total := len(files)

	if page > 0 {
		files = files.Paginate(page, pageSize)
	}

	body, err := json.Marshal(map[string]any{"files": files, "total": total})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling file list for experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)
}

// GetExperimentFile - GET /experiments/{name}/files/{filename}.
func GetExperimentFile(w http.ResponseWriter, r *http.Request) {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		vars  = mux.Vars(r)
		name  = vars["name"]
		file  = vars["filename"]
		query = r.URL.Query()
		path  = query.Get("path")
	)

	// the Files tab's viewer shows the file inline as text
	var (
		preview = r.Header.Get("Accept") == "text/plain"
		fields  = audit.Fields{
			Experiment: name, File: path,
			Extra: []any{"phenix.file.capture", isCaptureFile(path), "phenix.file.preview", preview},
		}
	)

	if !role.Allowed("experiments/files", "get", name) {
		audit.Denied(r, audit.FileDownload, "getting experiment file not allowed", fields)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	local, err := experiment.LocalFile(name, path)
	if err != nil {
		audit.Failure(r, audit.FileDownload, "getting experiment file failed", fields, err)

		if status := fileErrorStatus(err); status != http.StatusInternalServerError {
			http.Error(w, err.Error(), status)

			return
		}

		plog.Error(
			plog.TypeSystem,
			"getting file for experiment",
			"exp",
			name,
			"file",
			path,
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// a download, streamed from disk and resumable; or the viewer's text
	d := util.Download{Path: local, Name: file, ContentType: "", MaxSize: 0}
	if preview {
		d.Name = ""
		d.ContentType = "text/plain; charset=utf-8"
		d.MaxSize = maxViewBytes
	}

	served, err := util.ServeDownload(w, r, d)
	if err != nil {
		audit.Failure(r, audit.FileDownload, "getting experiment file failed", fields, err)

		switch {
		case errors.Is(err, util.ErrTooLarge):
			http.Error(w, path+" is too large to view (over 10 MiB); download it instead", http.StatusRequestEntityTooLarge)
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, util.ErrNotRegular):
			http.Error(w, path+": "+experiment.ErrFileNotFound.Error(), http.StatusNotFound)
		default:
			plog.Error(plog.TypeSystem, "sending file for experiment", "exp", name, "file", path, "err", err)
			http.Error(w, "unable to read "+path, http.StatusInternalServerError)
		}

		return
	}

	// once per download or view, not per resumed range or HEAD
	if served.Started {
		if rng := r.Header.Get("Range"); rng != "" {
			fields.Extra = append(fields.Extra, "http.request.range", rng)
		}

		audit.Success(r, audit.FileDownload, "downloaded file", fields)
	}
}

// GetExperimentApps - GET /experiments/{name}/apps.
func GetExperimentApps(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		name = mux.Vars(r)["name"]
	)

	if !role.Allowed("experiments/apps", "get", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment apps not allowed",
			"user",
			user,
			"exp",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"getting experiment apps for %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	apps := make(map[string]bool)

	for _, app := range exp.Apps() {
		apps[app.Name()] = false
	}

	maps.Copy(apps, exp.Status.AppRunning())

	body, _ := json.Marshal(apps)

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// GetVMs - GET /experiments/{exp}/vms.
func GetVMs(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		query   = r.URL.Query()
		size    = query.Get("screenshot")
		sortCol = query.Get("sortCol")
		sortDir = query.Get("sortDir")
		pageNum = query.Get("pageNum")
		perPage = query.Get("perPage")
	)

	if !role.Allowed("vms", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting vms file not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	page, pageSize, err := util.ParsePage(pageNum, perPage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	vmQuery := util.VMQuery{ //nolint:exhaustruct // this route does not filter
		ShowDNB: true,
		SortCol: sortColumn(sortCol, sortDir),
		SortAsc: sortDir == sortAsc,
		Page:    page,
		Size:    pageSize,
	}

	vms, observed := vm.ListForObserved(ctx, exp)
	allowed, total := util.SelectVMs(expName, vms, exp.Spec.Topology(), vmQuery, role)

	if size != "" {
		addVMScreenshots(ctx, expName, allowed, size)
	}

	if requestGone(r) {
		return
	}

	resp := util.VMListToProtobuf(expName, allowed, total, exp.Spec.Topology())
	util.SetVMListObservation(resp, observed)
	freshness.SetObserved(w, observed)

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetVM - GET /experiments/{exp}/vms/{name}.
func GetVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
		query   = r.URL.Query()
		size    = query.Get("screenshot")
	)

	if !role.Allowed("vms", "get", fmt.Sprintf("%s/%s", expName, name)) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting vm not allowed",
			"user",
			user,
			"exp",
			expName,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	vm, err := vm.GetFor(ctx, exp, name)
	if requestCanceled(r, err) {
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	if vm.Running && size != "" {
		screenshot, err := util.GetScreenshotContext(ctx, expName, name, size)
		if requestCanceled(r, err) {
			return
		}

		if err != nil {
			plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
		} else {
			vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		}
	}

	body, err := marshaler.Marshal(vmWithAnnotations(expName, *vm, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// vmWithAnnotations converts a VM for a response about that one VM, which,
// unlike VM lists and broadcasts, carries its node's annotations.
func vmWithAnnotations(expName string, v mm.VM, topo ifaces.TopologySpec) *proto.VM {
	pb := util.VMToProtobuf(expName, v, topo)

	annotations, err := util.AnnotationsToProtobuf(v.Annotations)
	if err != nil {
		plog.Error(plog.TypeSystem, "converting VM annotations", "exp", expName, "vm", v.Name, "err", err)

		return pb
	}

	pb.Annotations = annotations

	return pb
}

// UpdateVM - PATCH /experiments/{exp}/vms/{name}.
//
//nolint:funlen // handler
func UpdateVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, name)) {
		plog.Warn(
			plog.TypeSecurity,
			"updating vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.UpdateVMRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	opts := []vm.UpdateOption{
		vm.UpdateExperiment(expName),
		vm.UpdateVM(name),
		vm.UpdateWithCPU(int(req.GetCpus())),
		vm.UpdateWithMem(int(req.GetRam())),
		vm.UpdateWithDisk(req.GetDisk()),
		vm.UpdateWithPartition(int(req.GetInjectPartition())),
	}

	if req.GetInterface() != nil {
		opts = append(
			opts,
			vm.UpdateWithInterface(
				int(req.GetInterface().GetIndex()),
				req.GetInterface().GetVlan(),
			),
		)
	}

	switch req.GetTagUpdateMode() {
	case proto.TagUpdateMode_SET:
		opts = append(opts, vm.UpdateWithTags(req.GetTags(), false))
	case proto.TagUpdateMode_ADD:
		opts = append(opts, vm.UpdateWithTags(req.GetTags(), true))
	case proto.TagUpdateMode_NONE:
		// do nothing
	}

	if req.GetBoot() != nil {
		opts = append(opts, vm.UpdateWithDNB(req.GetDoNotBoot()))
	}

	if req.GetClusterHost() != nil {
		opts = append(opts, vm.UpdateWithHost(req.GetHost()))
	}

	if req.GetSnapshotOption() != nil {
		opts = append(opts, vm.UpdateWithSnapshot(req.GetSnapshot()))
	}

	if req.GetAnnotations() != nil {
		annotations := req.GetAnnotations().AsMap()

		if changesAutotunnel(expName, name, annotations) && forwardDeniedVM(role, expName, []string{name}) != "" {
			plog.Warn(
				plog.TypeSecurity,
				"creating port forwards not allowed",
				"user",
				middleware.UserFromContext(ctx),
				"exp",
				expName,
				"vm",
				name,
			)
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}

		opts = append(opts, vm.UpdateWithAnnotations(annotations))
	}

	if err := vm.Update(opts...); err != nil {
		if errors.Is(err, vm.ErrInvalidAnnotations) || errors.Is(err, vm.ErrInvalidDisk) {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		plog.Error(plog.TypeSystem, "updating VM", "err", err)
		http.Error(w, "unable to update VM", http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, "unable to get experiment", http.StatusBadRequest)

		return
	}

	// broadcast to every client, so read even once this request's client left
	vm, err := vm.GetFor(context.WithoutCancel(ctx), exp, name)
	if err != nil {
		http.Error(w, "unable to get VM", http.StatusInternalServerError)

		return
	}

	if vm.Running {
		screenshot, err := util.GetScreenshot(expName, name, defaultScreenshotSize)
		if err != nil {
			plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
		} else {
			vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		}
	}

	body, err = marshaler.Marshal(util.VMToProtobuf(expName, *vm, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms", "patch", fmt.Sprintf("%s/%s", expName, name)),
		bt.NewResource("experiment/vm", fmt.Sprintf("%s/%s", expName, name), "update"),
		body,
	)

	// a caller who may also read the VM gets its annotations
	if role.Allowed("vms", "get", fmt.Sprintf("%s/%s", expName, name)) {
		body, err = marshaler.Marshal(vmWithAnnotations(expName, *vm, exp.Spec.Topology()))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}
	}

	plog.Info(
		plog.TypeAction,
		"vm updated",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// UpdateVMs - PATCH /experiments/{exp}/vms.
//
//nolint:funlen // handler
func UpdateVMs(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
	)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.UpdateVMRequestList
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	// refused before any VM changes, as minimega could not boot from the disk
	for _, vmRequest := range req.GetVms() {
		if err := disk.ValidateMinimegaPath(vmRequest.GetDisk()); err != nil {
			http.Error(w, fmt.Sprintf("VM %s: %v", vmRequest.GetName(), err), http.StatusBadRequest)

			return
		}
	}

	// the VMs updated, which leaves out those the role may not patch
	resp := &proto.VMList{Vms: make([]*proto.VM, 0, len(req.GetVms()))} //nolint:exhaustruct // partial initialization

	for _, vmRequest := range req.GetVms() {
		// Skip any vms that are not allowed to be updated
		if !role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, vmRequest.GetName())) {
			plog.Warn(
				plog.TypeSecurity,
				"updating vm is not allowed",
				"user",
				middleware.UserFromContext(ctx),
				"exp",
				expName,
				"vm",
				vmRequest.GetName(),
			)

			continue
		}

		opts := []vm.UpdateOption{
			vm.UpdateExperiment(expName),
			vm.UpdateVM(vmRequest.GetName()),
			vm.UpdateWithCPU(int(vmRequest.GetCpus())),
			vm.UpdateWithMem(int(vmRequest.GetRam())),
			vm.UpdateWithDisk(vmRequest.GetDisk()),
		}

		if vmRequest.GetInterface() != nil {
			opts = append(
				opts,
				vm.UpdateWithInterface(
					int(vmRequest.GetInterface().GetIndex()),
					vmRequest.GetInterface().GetVlan(),
				),
			)
		}

		if vmRequest.GetBoot() != nil {
			opts = append(opts, vm.UpdateWithDNB(vmRequest.GetDoNotBoot()))
		}

		if vmRequest.GetClusterHost() != nil {
			opts = append(opts, vm.UpdateWithHost(vmRequest.GetHost()))
		}

		if vmRequest.GetSnapshotOption() != nil {
			opts = append(opts, vm.UpdateWithSnapshot(vmRequest.GetSnapshot()))
		}

		if err := vm.Update(opts...); err != nil {
			plog.Error(plog.TypeSystem, "updating VM", "err", err)
			http.Error(w, "unable to update VM", http.StatusInternalServerError)

			return
		}

		plog.Info(
			plog.TypeAction,
			"vm updated",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			vmRequest.GetName(),
		)

		// The rest only reads the VM back for the response. Once the client
		// has left, the remaining VMs are still updated, but not read back.
		if requestGone(r) {
			continue
		}

		exp, err := experiment.Get(expName)
		if err != nil {
			http.Error(w, "unable to get experiment", http.StatusBadRequest)

			return
		}

		vm, err := vm.GetFor(ctx, exp, vmRequest.GetName())
		if requestCanceled(r, err) {
			continue
		}

		if err != nil {
			http.Error(w, "unable to get VM", http.StatusInternalServerError)

			return
		}

		if vm.Running {
			screenshot, err := util.GetScreenshotContext(ctx, expName, vmRequest.GetName(), defaultScreenshotSize)
			if requestCanceled(r, err) {
				continue
			}

			if err != nil {
				plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
			} else {
				vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
					screenshot,
				)
			}
		}

		resp.Vms = append(resp.Vms, util.VMToProtobuf(expName, *vm, exp.Spec.Topology()))
	}

	if requestGone(r) {
		return
	}

	resp.Total = uint32(len(resp.GetVms())) //nolint:gosec // as many VMs as the request named

	body, err = marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// DeleteVM - DELETE /experiments/{exp}/vms/{name}.
func DeleteVM(w http.ResponseWriter, r *http.Request) {
	serveVMAction(w, r, "kill")
}

// StartVM - POST /experiments/{exp}/vms/{name}/start.
func StartVM(w http.ResponseWriter, r *http.Request) {
	serveVMAction(w, r, "start")
}

// StopVM - POST /experiments/{exp}/vms/{name}/stop.
func StopVM(w http.ResponseWriter, r *http.Request) {
	serveVMAction(w, r, "pause")
}

// RestartVM - GET /experiments/{exp}/vms/{name}/restart.
func RestartVM(w http.ResponseWriter, r *http.Request) {
	serveVMAction(w, r, "restart")
}

// ShutdownVM - GET /experiments/{exp}/vms/{name}/shutdown.
func ShutdownVM(w http.ResponseWriter, r *http.Request) {
	serveVMAction(w, r, "shutdown")
}

// ResetVM - GET /experiments/{exp}/vms/{name}/reset.
func ResetVM(w http.ResponseWriter, r *http.Request) {
	serveVMAction(w, r, "resetDisk")
}

// RedeployVM - POST /experiments/{exp}/vms/{name}/redeploy.
//
//nolint:funlen // handler
func RedeployVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/redeploy", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"reploying vm not allowed",
			"user",
			middleware.UserFromContext(ctx),
			"exp",
			expName,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.VMRedeployRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	// refused before the VM is touched, as minimega could not boot it
	if err := disk.ValidateMinimegaPath(req.GetDisk()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if err := cache.LockVMForRedeploying(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"redeploying",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	// the redeploy starts only once this has answered, so a client that left
	// before then does not have it done
	v, err := vm.GetFor(ctx, exp, name)
	if requestCanceled(r, err) {
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	v.Busy = true

	body, err = marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/redeploy", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "redeploying"),
		body,
	)

	redeployed := make(chan error)

	go func() {
		defer close(redeployed)

		opts := []vm.RedeployOption{
			vm.CPU(int(req.GetCpus())),
			vm.Memory(int(req.GetRam())),
			vm.Disk(req.GetDisk()),
			vm.Inject(req.GetInjects()),
		}

		if err := vm.Redeploy(expName, name, opts...); err != nil {
			redeployed <- err
		}

		v.Busy = false
	}()

	// HACK: mandatory sleep time to make it seem like a redeploy is
	// happening client-side, even when the redeploy is fast (like for
	// Linux VMs).
	time.Sleep(5 * time.Second) //nolint:mnd // sleep duration

	err = <-redeployed
	if err != nil {
		plog.Error(plog.TypeSystem, "redeploying VM", "exp", expName, "vm", name, "err", err)

		broker.Broadcast(
			bt.NewRequestPolicy("vms/redeploy", "update", fullName),
			bt.NewResource("experiment/vm", expName+"/"+name, "errorRedeploying"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// Get the VM details again since redeploying may have changed them. They
	// are broadcast to every client, so read even once this request's left.
	v, err = vm.Get(context.WithoutCancel(ctx), expName, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	screenshot, err := util.GetScreenshot(expName, name, defaultScreenshotSize)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, _ = marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))

	broker.Broadcast(
		bt.NewRequestPolicy("vms/redeploy", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "redeployed"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm redeployed",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetScreenshot - GET /experiments/{exp}/vms/{name}/screenshot.png.
func GetScreenshot(w http.ResponseWriter, r *http.Request) {
	var (
		ctx    = r.Context()
		role   = middleware.RoleFromContext(ctx)
		vars   = mux.Vars(r)
		exp    = vars["exp"]
		name   = vars["name"]
		query  = r.URL.Query()
		size   = query.Get("size")
		encode = query.Get("base64") != ""
	)

	if !role.Allowed("vms/screenshot", "get", exp+"/"+name) {
		plog.Warn(
			plog.TypeSecurity,
			"screenshotting vm not allowed",
			"user",

			ctx.Value(middleware.ContextKeyUser),
			"exp",
			exp,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if size == "" {
		size = defaultScreenshotSize
	}

	screenshot, err := util.GetScreenshot(exp, name, size)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if encode {
		encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		_, _ = w.Write([]byte(encoded)) //nolint:gosec // XSS via taint analysis

		return
	}

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(screenshot) //nolint:gosec // XSS via taint analysis
}

// GetVMCaptures - GET /experiments/{exp}/vms/{name}/captures.
func GetVMCaptures(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "list", fmt.Sprintf("%s/%s", exp, name)) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting captures for VM not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	captures := mm.GetVMCaptures(mm.NS(exp), mm.VMName(name), mm.Context(ctx))
	if requestGone(r) {
		return
	}

	body, err := marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(captures)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// StartVMCapture - POST /experiments/{exp}/vms/{name}/captures.
func StartVMCapture(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)) {
		audit.Denied(r, audit.CaptureStart, "starting capture for VM not allowed", audit.Fields{Experiment: exp, VM: name})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.StartCaptureRequest

	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	iface := int(req.GetInterface())
	captured := audit.Fields{Experiment: exp, VM: name, Interface: &iface, File: req.GetFilename()}

	if err := vm.StartCapture(ctx, exp, name, iface, req.GetFilename()); err != nil {
		audit.Failure(r, audit.CaptureStart, "starting capture for VM failed", captured, err)

		if requestCanceled(r, err) {
			return
		}

		plog.Error(plog.TypeSystem, "starting capture for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// the capture writes to the experiment's files directory
	experiment.InvalidateFiles(exp)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "start"),
		body,
	)

	audit.Success(r, audit.CaptureStart, "vm capture started", captured)
	w.WriteHeader(http.StatusNoContent)
}

// StopVMCaptures - DELETE /experiments/{exp}/vms/{name}/captures[?iface={iface}].
//
// If the iface query parameter is provided, only the packet capture running
// on that interface is stopped, leaving any other running captures for the
// VM untouched. Otherwise, all packet captures for the VM are stopped.
func StopVMCaptures(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		ifaceStr = r.URL.Query().Get("iface")
	)

	if !role.Allowed("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)) {
		audit.Denied(r, audit.CaptureStop, "stopping captures for VM not allowed", audit.Fields{Experiment: exp, VM: name})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	var (
		err     error
		body    json.RawMessage
		stopped = audit.Fields{Experiment: exp, VM: name}
	)

	if ifaceStr == "" {
		err = vm.StopCaptures(ctx, exp, name)
	} else {
		var iface int

		iface, err = strconv.Atoi(ifaceStr)
		if err != nil {
			http.Error(w, "iface query parameter must be an integer", http.StatusBadRequest)

			return
		}

		stopped.Interface = &iface

		if err = vm.StopCapture(ctx, exp, name, iface); err == nil {
			body, err = json.Marshal(map[string]int{"interface": iface})
			if err != nil {
				plog.Error(plog.TypeSystem, "marshaling capture stop result", "err", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}
		}
	}

	if err != nil {
		audit.Failure(r, audit.CaptureStop, "stopping captures for VM failed", stopped, err)

		if requestCanceled(r, err) {
			return
		}

		plog.Error(
			plog.TypeSystem,
			"stopping captures for VM",
			"exp",
			exp,
			"name",
			name,
			"iface",
			ifaceStr,
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// the stopped captures' files are now complete
	experiment.InvalidateFiles(exp)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "stop"),
		body,
	)

	audit.Success(r, audit.CaptureStop, "vm capture stopped", stopped)
	w.WriteHeader(http.StatusNoContent)
}

// StartCaptureSubnet - POST /experiments/{exp}/captureSubnet.
func StartCaptureSubnet(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
	)

	if !role.Allowed("exp/captureSubnet", "create", exp) {
		audit.Denied(r, audit.CaptureStart, "starting subnet capture for experiment not allowed", audit.Fields{Experiment: exp})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.CaptureSubnetRequest

	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	subnet := []any{"phenix.capture.subnet", req.GetSubnet()}

	vmCaptures, err := vm.CaptureSubnet(ctx, exp, req.GetSubnet(), req.GetVms())
	if err != nil {
		audit.Failure(r, audit.CaptureStart, "starting subnet capture failed", audit.Fields{Experiment: exp, Extra: subnet}, err)

		if requestCanceled(r, err) {
			return
		}

		plog.Error(plog.TypeSystem, "unable to start subnet capture", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	body, err = marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(vmCaptures)})
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to marshal vm capture list", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	for _, c := range vmCaptures {
		audit.Success(r, audit.CaptureStart, "subnet capture started", audit.Fields{
			Experiment: exp, VM: c.VM, Interface: &c.Interface, File: c.Filepath, Extra: subnet,
		})
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// StopCaptureSubnet - POST /experiments/{exp}/stopCaptureSubnet.
func StopCaptureSubnet(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
	)

	if !role.Allowed("exp/captureSubnet", "delete", exp) {
		audit.Denied(r, audit.CaptureStop, "stopping subnet capture for experiment not allowed", audit.Fields{Experiment: exp})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.CaptureSubnetRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	subnet := []any{"phenix.capture.subnet", req.GetSubnet()}

	vms, err := vm.StopCaptureSubnet(ctx, exp, req.GetSubnet(), req.GetVms())
	if err != nil {
		audit.Failure(r, audit.CaptureStop, "stopping subnet capture failed", audit.Fields{Experiment: exp, Extra: subnet}, err)

		if requestCanceled(r, err) {
			return
		}

		plog.Error(plog.TypeSystem, "unable to stop subnet capture", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	body, err = marshaler.Marshal(&proto.VMNameList{Vms: vms})
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to marshal vm capture list", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	for _, name := range vms {
		audit.Success(r, audit.CaptureStop, "subnet capture stopped", audit.Fields{Experiment: exp, VM: name, Extra: subnet})
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetVMSnapshots - GET /experiments/{exp}/vms/{name}/snapshots.
func GetVMSnapshots(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/snapshots", "list", fmt.Sprintf("%s/%s", exp, name)) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing snapshots for VM not allowed",
			"user",
			user,
			"exp",
			exp,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	snapshots, err := vm.Snapshots(ctx, exp, name)
	if requestCanceled(r, err) {
		return
	}

	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"getting list of snapshots for VM",
			"exp",
			exp,
			"vm",
			name,
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	body, err := marshaler.Marshal(&proto.SnapshotList{Snapshots: snapshots})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// SnapshotVM - POST /experiments/{exp}/vms/{name}/snapshots.
//
//nolint:funlen // handler
func SnapshotVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/snapshots", "create", fullName) {
		audit.Denied(r, audit.VMSnapshot, "snapshotting VM not allowed", audit.Fields{Experiment: exp, VM: name})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var req proto.SnapshotRequest

	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if err := cache.LockVMForSnapshotting(exp, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			exp,
			"vm",
			name,
			"action",
			"snapshotting",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(exp, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "creating"),
		nil,
	)

	status := make(chan string)

	go func() {
		for {
			s := <-status

			if s == "completed" {
				return
			}

			progress, err := strconv.ParseFloat(s, 64)
			if err == nil {
				plog.Debug(plog.TypeSystem, "snapshot percent complete", statusKeyPercent, progress)

				status := map[string]any{
					statusKeyPercent: progress / percentDivisor,
				}

				marshalled, _ := json.Marshal(status)

				broker.Broadcast(
					bt.NewRequestPolicy("vms/snapshots", "create", fullName),
					bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "progress"),
					marshalled,
				)
			}
		}
	}()

	cb := func(s string) { status <- s }
	snapshotted := audit.Fields{Experiment: exp, VM: name, Target: req.GetFilename()}

	if err := vm.Snapshot(exp, name, req.GetFilename(), cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/snapshots", "create", fullName),
			bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "errorCreating"),
			nil,
		)

		plog.Error(plog.TypeSystem, "snapshotting VM", "exp", exp, "vm", name, "err", err)
		audit.Failure(r, audit.VMSnapshot, "snapshotting VM failed", snapshotted, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "create"),
		nil,
	)

	audit.Success(r, audit.VMSnapshot, "vm snapshotted", snapshotted)
	w.WriteHeader(http.StatusNoContent)
}

// RestoreVM - POST /experiments/{exp}/vms/{name}/snapshots/{snapshot}.
func RestoreVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
		snap     = vars["snapshot"]
	)

	restored := audit.Fields{Experiment: exp, VM: name, File: snap}

	if !role.Allowed("vms/snapshots", "update", fullName) {
		audit.Denied(r, audit.VMRestore, "restoring VM not allowed", restored)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := cache.LockVMForRestoring(exp, name)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			exp,
			"vm",
			name,
			"action",
			"restoring",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(exp, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", fmt.Sprintf("%s/%s", exp, name), "restoring"),
		nil,
	)

	err = vm.Restore(exp, name, snap)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/snapshots", "create", fullName),
			bt.NewResource(
				"experiment/vm/snapshot",
				fmt.Sprintf("%s/%s", exp, name),
				"errorRestoring",
			),
			nil,
		)

		plog.Error(plog.TypeSystem, "restoring VM", "exp", exp, "vm", name, "err", err)
		audit.Failure(r, audit.VMRestore, "restoring VM failed", restored, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "restore"),
		nil,
	)

	audit.Success(r, audit.VMRestore, "vm restored", restored)
	w.WriteHeader(http.StatusNoContent)
}

// CommitVM - POST /experiments/{exp}/vms/{name}/commit.
//
//nolint:funlen // handler
func CommitVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/commit", "create", fullName) {
		audit.Denied(r, audit.VMCommit, "committing VM not allowed", audit.Fields{Experiment: expName, VM: name})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var filename string

	// If user provided body to this request, expect it to specify the
	// filename to use for the commit. If no body was provided, pass an
	// empty string to `api.CommitToDisk` to let it create a copy based on
	// the existing file name for the base image.
	if len(body) != 0 {
		var req proto.BackingImageRequest

		err = unmarshaler.Unmarshal(body, &req)
		if err != nil {
			plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		if req.GetFilename() == "" {
			plog.Error(plog.TypeSystem, "missing filename for commit")
			http.Error(w, "missing 'filename' key", http.StatusBadRequest)

			return
		}

		filename = req.GetFilename()
	}

	if err := cache.LockVMForCommitting(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"committing",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	if filename == "" {
		/*
			if filename, err = api.GetNewDiskName(exp, name); err != nil {
				log.Error("failure getting new disk name for commit")
				http.Error(w, "failure getting new disk name for commit", http.StatusInternalServerError)
				return
			}
		*/

		http.Error(w, "must provide new disk name for commit", http.StatusBadRequest)

		return
	}

	payload := &proto.BackingImageResponse{Disk: filename} //nolint:exhaustruct // partial initialization
	body, _ = marshaler.Marshal(payload)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/commit", "create", fullName),
		bt.NewResource("experiment/vm/commit", expName+"/"+name, "committing"),
		body,
	)

	status := make(chan float64)

	go func() {
		for s := range status {
			plog.Debug(plog.TypeSystem, "VM commit percent complete", statusKeyPercent, s)

			status := map[string]any{
				statusKeyPercent: s,
			}

			marshalled, _ := json.Marshal(status)

			broker.Broadcast(
				bt.NewRequestPolicy("vms/commit", "create", fullName),
				bt.NewResource("experiment/vm/commit", expName+"/"+name, "progress"),
				marshalled,
			)
		}
	}()

	cb := func(s float64) { status <- s }
	committed := audit.Fields{Experiment: expName, VM: name, Target: filename}

	if _, err = vm.CommitToDisk(expName, name, filename, cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/commit", "create", fullName),
			bt.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		plog.Error(plog.TypeSystem, "committing VM", "exp", expName, "vm", name, "err", err)
		audit.Failure(r, audit.VMCommit, "committing VM failed", committed, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	// committed, whether or not the VM can be read back for the response
	audit.Success(r, audit.VMCommit, "vm committed", committed)

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/commit", "create", fullName),
			bt.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	// broadcast to every client, so read even once this request's client left
	v, err := vm.GetFor(context.WithoutCancel(ctx), exp, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/commit", "create", fullName),
			bt.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	payload.Vm = util.VMToProtobuf(expName, *v, exp.Spec.Topology())
	body, _ = marshaler.Marshal(payload)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/commit", "create", fmt.Sprintf("%s/%s", expName, name)),
		bt.NewResource("experiment/vm/commit", expName+"/"+name, "commit"),
		body,
	)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// CreateVMMemorySnapshot - POST /experiments/{exp}/vms/{name}/memorySnapshot.
//
//nolint:funlen // handler
func CreateVMMemorySnapshot(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/memorySnapshot", "create", fullName) {
		audit.Denied(r, audit.VMMemorySnapshot, "capturing memory snapshot of VM not allowed", audit.Fields{
			Experiment: exp, VM: name,
		})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var filename string

	// If user provided body to this request, expect it to specify the
	// filename to use for capturing a memory snapshot.
	if len(body) != 0 {
		var req proto.MemorySnapshotRequest

		err = unmarshaler.Unmarshal(body, &req)
		if err != nil {
			plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		if req.GetFilename() == "" {
			plog.Error(plog.TypeSystem, "missing filename for memory snapshot")
			http.Error(w, "missing 'filename' key", http.StatusBadRequest)

			return
		}

		filename = req.GetFilename()
	}

	if err := cache.LockVMForMemorySnapshotting(exp, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			exp,
			"vm",
			name,
			"action",
			"memory snapshotting",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(exp, name)

	if filename == "" {
		http.Error(w, "must provide new disk name for memory snapshot", http.StatusBadRequest)

		return
	}

	payload := &proto.MemorySnapshotResponse{Disk: filename} //nolint:exhaustruct // partial initialization
	body, _ = marshaler.Marshal(payload)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
		bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "committing"),
		body,
	)

	status := make(chan string)

	go func() {
		defer close(status)

		for {
			s := <-status
			if s == "failed" || s == "completed" {
				return
			}

			progress, err := strconv.ParseFloat(s, 64)
			if err == nil {
				status := map[string]any{
					statusKeyPercent: progress,
				}

				plog.Info(plog.TypeSystem, "memory snapshot percent complete", statusKeyPercent, progress)

				marshalled, _ := json.Marshal(status)

				broker.Broadcast(
					bt.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
					bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "progress"),
					marshalled,
				)
			}
		}
	}()

	cb := func(s string) { status <- s }
	snapshotted := audit.Fields{Experiment: exp, VM: name, Target: filename}

	if _, err = vm.MemorySnapshot(exp, name, filename, cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
			bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "errorCommitting"),
			nil,
		)

		plog.Error(plog.TypeSystem, "memory snapshot for VM", "exp", exp, "vm", name, "err", err)
		audit.Failure(r, audit.VMMemorySnapshot, "memory snapshot of VM failed", snapshotted, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	marshalled, _ := json.Marshal(util.WithRoot("disk", filename))

	broker.Broadcast(
		bt.NewRequestPolicy("vms/memorySnapshot", "create", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "commit"),
		marshalled,
	)

	audit.Success(r, audit.VMMemorySnapshot, "vm memory snapshot created", snapshotted)
	w.WriteHeader(http.StatusNoContent)
}

// GetAllVMs - GET /vms.
func GetAllVMs(w http.ResponseWriter, r *http.Request) {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		query = r.URL.Query()
		size  = query.Get("screenshot")
	)

	if !role.Allowed("vms", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing vms not allowed",
			"user",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	exps, err := experiment.List()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)

		// an empty list would read as no running VMs
		if len(exps) == 0 {
			http.Error(w, "unable to read experiments from the store", http.StatusInternalServerError)

			return
		}
	}

	allowed := []*proto.VM{}
	observed := freshness.Unobserved()

	for _, exp := range exps {
		if !exp.Running() {
			// We only care about getting running VMs, which are only present in
			// running experiments.
			continue
		}

		vms, obs := vm.ListForObserved(ctx, &exp)
		observed = observed.Older(obs)
		nodes := util.IndexTopology(exp.Spec.Topology())

		for _, vm := range vms {
			id := exp.Metadata.Name + "/" + vm.Name

			if !role.Allowed("vms", "list", id) {
				continue
			}

			if !vm.Running {
				// We only care about running VMs.
				continue
			}

			if size != "" {
				screenshot, err := util.GetScreenshotContext(ctx, exp.Metadata.Name, vm.Name, size)
				if requestGone(r) {
					return
				}

				if err != nil {
					plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
						screenshot,
					)
				}
			}

			allowed = append(allowed, util.VMToProtobufIndexed(exp.Metadata.Name, vm, nodes))
		}
	}

	if requestGone(r) {
		return
	}

	resp := &proto.VMList{Total: uint32(len(allowed)), Vms: allowed} //nolint:gosec,exhaustruct // integer overflow conversion int -> uint32
	util.SetVMListObservation(resp, observed)
	freshness.SetObserved(w, observed)

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetApplications - GET /applications.
func GetApplications(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("applications", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing applications not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	allowed := []string{}

	for _, app := range app.List() {
		if role.Allowed("applications", "list", app) {
			allowed = append(allowed, app)
		}
	}

	body, err := marshaler.Marshal(&proto.AppList{Applications: allowed})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetTopologies - GET /topologies.
func GetTopologies(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("topologies", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing topologies not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	topologies, err := config.List("topology")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	allowed := []string{}

	for _, topo := range topologies {
		if role.Allowed("topologies", "list", topo.Metadata.Name) {
			allowed = append(allowed, topo.Metadata.Name)
		}
	}

	body, err := marshaler.Marshal(&proto.TopologyList{Topologies: allowed})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetScenarios - GET /topologies/{topo}/scenarios.
func GetScenarios(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		topo = vars["topo"]
	)

	if !role.Allowed("scenarios", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing scenarios not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	scenarios, err := config.List("scenario")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	allowed := make(map[string]*structpb.ListValue)

	for _, s := range scenarios {
		var (
			// A scenario can be associated with more than one topology.
			topos = strings.Split(s.Metadata.Annotations["topology"], ",")
			found bool
		)

		if slices.Contains(topos, topo) {
			found = true
		}

		if !found {
			continue
		}

		if role.Allowed("scenarios", "list", s.Metadata.Name) {
			apps, err := scenario.AppList(s.Metadata.Name)
			if err != nil {
				plog.Error(
					plog.TypeSystem,
					"getting apps for scenario",
					"scenario",
					s.Metadata.Name,
					"err",
					err,
				)

				continue
			}

			list := make([]any, len(apps))
			for i, a := range apps {
				list[i] = a
			}

			val, _ := structpb.NewList(list)
			allowed[s.Metadata.Name] = val
		}
	}

	body, err := marshaler.Marshal(&proto.ScenarioList{Scenarios: allowed})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

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

	hosts, observed, err := mm.GetClusterHostsObserved(ctx, false)
	if requestCanceled(r, err) {
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(marshalled) //nolint:gosec // XSS via taint analysis
}

// ChangeOpticalDisc - POST /experiments/{exp}/vms/{name}/cdrom.
func ChangeOpticalDisc(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		query    = r.URL.Query()
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		isoPath  = query.Get("isoPath")
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/cdrom", "update", fullName) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"changing optical disk not allowed",
			user,
			"exp",
			exp,
			"vm",
			name,
			"iso",
			isoPath,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := vm.ChangeOpticalDisc(ctx, exp, name, isoPath)
	if requestCanceled(r, err) {
		return
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "changing disc for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/cdrom", "update", fullName),
		bt.NewResource("experiment/vm", fullName, "cdrom-inserted"),
		nil,
	)

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"optical disk changed",
		user,
		"exp",
		exp,
		"vm",
		name,
		"iso",
		isoPath,
	)
	w.WriteHeader(http.StatusNoContent)
}

// EjectOpticalDisc - DELETE /experiments/{exp}/vms/{name}/cdrom.
func EjectOpticalDisc(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/cdrom", "delete", fullName) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"ejecting optical disk not allowed",
			user,
			"exp",
			exp,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := vm.EjectOpticalDisc(ctx, exp, name)
	if requestCanceled(r, err) {
		return
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "ejecting disc for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/cdrom", "delete", fullName),
		bt.NewResource("experiment/vm", fullName, "cdrom-ejected"),
		nil,
	)

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"optical disk ejected",
		user,
		"exp",
		exp,
		"vm",
		name,
	)
	w.WriteHeader(http.StatusNoContent)
}

// GetSettings - GET /settings.
func GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := settings.GetSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting proto settings", "err:", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(settings)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling settings", "err:", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// SetSettings - POST /settings.
func SetSettings(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("settings", "update") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSystem,
			"setting settings not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	s := &settings.Settings{} //nolint:exhaustruct // partial initialization

	err = json.Unmarshal(body, s)
	if err != nil {
		plog.Error(plog.TypeSystem, "Unmarshaling request body", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

	err = settings.UpdateAllSettings(*s)
	if errors.Is(err, settings.ErrPasswordMinLength) {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "Updating all settings", "err", err)
		http.Error(w, "Error updating settings", http.StatusInternalServerError)

		return
	}

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeSystem,
		"settings changed",
		"user",
		user,
	)
}

// GetPasswordRequirements - GET /settings/password.
func GetPasswordRequirements(w http.ResponseWriter, r *http.Request) {
	ensureSettingDefaults()

	passwordReqs, err := settings.GetPasswordSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Getting password settings:", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(passwordReqs)
	if err != nil {
		plog.Error(plog.TypeSystem, "Marshalling password reqs:", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// settingDefaultsOnce stores the default settings once per process: the
// settings pages ask for them often, storing them opens the store for writing,
// and settings.List already fills in any defaults missing afterwards.
var settingDefaultsOnce sync.Once //nolint:gochecknoglobals // process-wide once

func ensureSettingDefaults() {
	settingDefaultsOnce.Do(func() { _ = settings.SetDefaults() })
}

// GetTimeoutSettings - GET /settings/timeout.
func GetTimeoutSettings(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetTimeoutSettings")

	ensureSettingDefaults()

	timeoutReqs, err := settings.GetTimeoutSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Getting timeout settings:", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(timeoutReqs)
	if err != nil {
		plog.Error(plog.TypeSystem, "Marshalling timeout reqs:", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}
