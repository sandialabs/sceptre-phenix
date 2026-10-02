package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"phenix/types"
	ifaces "phenix/types/interfaces"
	putil "phenix/util"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/pubsub"
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
	}

	allowed := []*proto.Experiment{}

	busy := anyExperimentLocked(experiments)

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
			vms := listExperimentVMs(exp, busy && size == "")

			if exp.Running() && size != "" {
				addVMScreenshots(exp.Spec.ExperimentName(), vms, size)
			}

			pb = util.ExperimentToProtobuf(exp, status, vms)
		}

		pb.Percent = percent
		allowed = append(allowed, pb)
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

	_, _ = w.Write(body)
}

// addVMScreenshots sets each running VM's screenshot, at the given size, in
// place.
func addVMScreenshots(expName string, vms []mm.VM, size string) {
	for i, v := range vms {
		if !v.Running {
			continue
		}

		screenshot, err := util.GetScreenshot(expName, v.Name, size)
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
// minimega is busy.
func listExperimentVMs(exp types.Experiment, busy bool) []mm.VM {
	if busy {
		return vm.ListConfigured(exp)
	}

	return vm.ListFor(&exp)
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

	vms := vm.ListFor(exp)

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
		err := weberror.NewWebError(err, "unable to get experiment %s details", name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	if exp.Running() {
		err := weberror.NewWebError(err, "cannot update running experiment %s", name)

		return err.SetStatus(http.StatusBadRequest)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		err := weberror.NewWebError(err, "unable to parse update request for experiment %s", name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	var vlans map[string]int

	if err := json.Unmarshal(body, &vlans); err != nil {
		err := weberror.NewWebError(err, "unable to parse update request for experiment %s", name)

		return err.SetStatus(http.StatusInternalServerError)
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

	var vms []mm.VM

	if noVMs {
		vms = vm.ListConfigured(*exp)
	} else {
		vms = vm.ListFor(exp)
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
		addVMScreenshots(name, allowed, size)
	}

	experiment := util.ExperimentToProtobuf(*exp, status, allowed)
	experiment.VmCount = uint32(total) //nolint:gosec // integer overflow conversion int -> uint32

	if noVMs {
		experiment.Vms = nil
	}

	body, err := marshaler.Marshal(experiment)
	if err != nil {
		err := weberror.NewWebError(err, "marshaling experiment %s - %v", name, err)

		return err.SetStatus(http.StatusInternalServerError)
	}

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

	body, err := marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vm.ListFor(exp)))
	if err != nil {
		return weberror.NewWebError(err, "unable to marshal experiment %s", name)
	}

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
		captures = mm.GetExperimentCaptures(mm.NS(name))
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

	files, err := experiment.Files(name, clientFilter)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting list of files for experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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

	if !role.Allowed("experiments/files", "get", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment file not allowed",
			"user",
			user,
			"exp",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	contents, err := experiment.File(name, path)
	if err != nil {
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

	if r.Header.Get("Accept") == "text/plain" {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(contents) //nolint:gosec // XSS via taint analysis

		return
	}

	w.Header().Set("Content-Disposition", util.Attachment(file))
	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"downloaded file",
		"user",
		user,
		"exp",
		name,
		"file",
		path,
	)
	http.ServeContent(w, r, "", time.Now(), bytes.NewReader(contents))
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

	allowed, total := util.SelectVMs(expName, vm.ListFor(exp), exp.Spec.Topology(), vmQuery, role)

	if size != "" {
		addVMScreenshots(expName, allowed, size)
	}

	resp := util.VMListToProtobuf(expName, allowed, total, exp.Spec.Topology())

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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

	vm, err := vm.GetFor(exp, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	if vm.Running && size != "" {
		screenshot, err := util.GetScreenshot(expName, name, size)
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
		opts = append(opts, vm.UpdateWithAnnotations(req.GetAnnotations().AsMap()))
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

	vm, err := vm.GetFor(exp, name)
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

	resp := &proto.VMList{Total: req.GetTotal()} //nolint:exhaustruct // partial initialization
	resp.Vms = make([]*proto.VM, int(req.GetTotal()))

	for index, vmRequest := range req.GetVms() {
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

		exp, err := experiment.Get(expName)
		if err != nil {
			http.Error(w, "unable to get experiment", http.StatusBadRequest)

			return
		}

		vm, err := vm.GetFor(exp, vmRequest.GetName())
		if err != nil {
			http.Error(w, "unable to get VM", http.StatusInternalServerError)

			return
		}

		if vm.Running {
			screenshot, err := util.GetScreenshot(expName, vmRequest.GetName(), defaultScreenshotSize)
			if err != nil {
				plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
			} else {
				vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
					screenshot,
				)
			}
		}

		resp.Vms[index] = util.VMToProtobuf(expName, *vm, exp.Spec.Topology())
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
	}

	body, err = marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// DeleteVM - DELETE /experiments/{exp}/vms/{name}.
func DeleteVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms", "delete", fmt.Sprintf("%s/%s", expName, name)) {
		plog.Warn(
			plog.TypeSecurity,
			"deleting vm not allowed",
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

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if !exp.Running() {
		http.Error(w, "experiment not running", http.StatusBadRequest)

		return
	}

	if err := mm.KillVM(mm.NS(expName), mm.VMName(name)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms", "delete", fmt.Sprintf("%s/%s", expName, name)),
		bt.NewResource("experiment/vm", fmt.Sprintf("%s/%s", expName, name), "delete"),
		nil,
	)

	plog.Info(
		plog.TypeAction,
		"vm deleted",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	w.WriteHeader(http.StatusNoContent)
}

// StartVM - POST /experiments/{exp}/vms/{name}/start.
//
//nolint:funlen // handler
func StartVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/start", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"starting vm not allowed",
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

	if err := cache.LockVMForStarting(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"starting",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/start", "update", fullName),
		bt.NewResource("experiment/vm", fullName, "starting"),
		nil,
	)

	if err := mm.StartVM(mm.NS(expName), mm.VMName(name)); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", fullName, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", fullName, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.GetFor(exp, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", fullName, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	screenshot, err := util.GetScreenshot(expName, name, "215")
	if err != nil {
		plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/start", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "start"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm started",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// StopVM - POST /experiments/{exp}/vms/{name}/stop.
//
//nolint:funlen // handler
func StopVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/stop", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"stopping vm not allowed",
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

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"stopping",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/stop", "update", fullName),
		bt.NewResource("experiment/vm", fullName, "stopping"),
		nil,
	)

	if err := mm.StopVM(mm.NS(expName), mm.VMName(name)); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", fullName, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", fullName, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.GetFor(exp, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", fullName, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/stop", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "stop"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm stopped",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// RestartVM - GET /experiments/{exp}/vms/{name}/restart.
//
//nolint:funlen // handler
func RestartVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/restart", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"restarting vm not allowed",
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

	if err := cache.LockVMForStarting(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"starting",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/restart", "update", fullName),
		bt.NewResource("experiment/vm", fullName, "restarting"),
		nil,
	)

	if err := vm.Restart(expName, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.GetFor(exp, name)
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

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/restart", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "update"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm restarted",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// ShutdownVM - GET /experiments/{exp}/vms/{name}/shutdown.
func ShutdownVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/shutdown", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"shutting down vm not allowed",
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

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"stopping",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	if err := vm.Shutdown(expName, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.GetFor(exp, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	v.Running = false

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/shutdown", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "shutdown"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm shutdown",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// ResetVM - GET /experiments/{exp}/vms/{name}/reset.
func ResetVM(w http.ResponseWriter, r *http.Request) {
	var (
		ctx      = r.Context()
		role     = middleware.RoleFromContext(ctx)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/reset", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"resetting vm not allowed",
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

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error(
			plog.TypeSystem,
			"locking VM",
			"exp",
			expName,
			"vm",
			name,
			"action",
			"stopping",
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusConflict)

		return
	}

	defer cache.UnlockVM(expName, name)

	if err := vm.ResetDiskState(expName, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.GetFor(exp, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/reset", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "reset"),
		body,
	)

	plog.Info(
		plog.TypeAction,
		"vm reset",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		expName,
		"vm",
		name,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
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

	v, err := vm.GetFor(exp, name)
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

	// Get the VM details again since redeploying may have changed them.
	v, err = vm.Get(expName, name)
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

	captures := mm.GetVMCaptures(mm.NS(exp), mm.VMName(name))

	body, err := marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(captures)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"starting capture for VM not allowed",
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

	if err := vm.StartCapture(exp, name, int(req.GetInterface()), req.GetFilename()); err != nil {
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

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"vm capture started",
		"user",
		user,
		"exp",
		exp,
		"vm",
		name,
	)
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"stopping captures for VM not allowed",
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

	var (
		err  error
		body json.RawMessage
	)

	if ifaceStr == "" {
		err = vm.StopCaptures(exp, name)
	} else {
		var iface int

		iface, err = strconv.Atoi(ifaceStr)
		if err != nil {
			http.Error(w, "iface query parameter must be an integer", http.StatusBadRequest)

			return
		}

		if err = vm.StopCapture(exp, name, iface); err == nil {
			body, err = json.Marshal(map[string]int{"interface": iface})
			if err != nil {
				plog.Error(plog.TypeSystem, "marshaling capture stop result", "err", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}
		}
	}

	if err != nil {
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

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"vm capture stopped",
		"user",
		user,
		"exp",
		exp,
		"vm",
		name,
		"iface",
		ifaceStr,
	)
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"starting subnet capture for experiment not allowed",
			"user",
			user,
			"exp",
			exp,
		)
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

	vmCaptures, err := vm.CaptureSubnet(exp, req.GetSubnet(), req.GetVms())
	if err != nil {
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

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"subnet capture started",
		"user",
		user,
		"exp",
		exp,
	)
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"stopping subnet capture for experiment not allowed",
			"user",
			user,
			"exp",
			exp,
		)
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

	vms, err := vm.StopCaptureSubnet(exp, req.GetSubnet(), req.GetVms())
	if err != nil {
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

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"subnet capture stopped",
		"user",
		user,
		"exp",
		exp,
	)
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

	snapshots, err := vm.Snapshots(exp, name)
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"snapshotting VM not allowed",
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

	if err := vm.Snapshot(exp, name, req.GetFilename(), cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/snapshots", "create", fullName),
			bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "errorCreating"),
			nil,
		)

		plog.Error(plog.TypeSystem, "snapshotting VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "create"),
		nil,
	)

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"vm snapshotted",
		"user",
		user,
		"exp",
		exp,
		"vm",
		name,
		"file",
		req.GetFilename(),
	)
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

	if !role.Allowed("vms/snapshots", "update", fullName) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"restoring VM not allowed",
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
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "restore"),
		nil,
	)

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"vm restored",
		"user",
		user,
		"exp",
		exp,
		"vm",
		name,
	)
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"committing VM not allowed",
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

	if _, err = vm.CommitToDisk(expName, name, filename, cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/commit", "create", fullName),
			bt.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		plog.Error(plog.TypeSystem, "committing VM", "exp", expName, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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

	v, err := vm.GetFor(exp, name)
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

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"vm committed",
		"user",
		user,
		"exp",
		expName,
		"vm",
		name,
	)
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"capturing memory snapshot of VM not allowed",
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

	if _, err = vm.MemorySnapshot(exp, name, filename, cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
			bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "errorCommitting"),
			nil,
		)

		plog.Error(plog.TypeSystem, "memory snapshot for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	marshalled, _ := json.Marshal(util.WithRoot("disk", filename))

	broker.Broadcast(
		bt.NewRequestPolicy("vms/memorySnapshot", "create", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "commit"),
		marshalled,
	)

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"vm memory snapshot created",
		"user",
		user,
		"exp",
		exp,
		"vm",
		name,
	)
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
	}

	allowed := []*proto.VM{}

	for _, exp := range exps {
		if !exp.Running() {
			// We only care about getting running VMs, which are only present in
			// running experiments.
			continue
		}

		vms := vm.ListFor(&exp)
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
				screenshot, err := util.GetScreenshot(exp.Metadata.Name, vm.Name, size)
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

	resp := &proto.VMList{Total: uint32(len(allowed)), Vms: allowed} //nolint:gosec // integer overflow conversion int -> uint32

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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

	hosts, err := mm.GetClusterHosts(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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

	err := vm.ChangeOpticalDisc(exp, name, isoPath)
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

	err := vm.EjectOpticalDisc(exp, name)
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
