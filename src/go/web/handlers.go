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
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/creack/pty"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/scenario"
	"phenix/api/settings"
	"phenix/api/vm"
	"phenix/app"
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
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

var (
	marshaler   = protojson.MarshalOptions{EmitUnpopulated: true}                      //nolint:gochecknoglobals // global marshaler
	unmarshaler = protojson.UnmarshalOptions{AllowPartial: true, DiscardUnknown: true} //nolint:gochecknoglobals // global unmarshaler

	ptys  = map[int]*os.File{} //nolint:gochecknoglobals // global state
	ptyMu sync.Mutex           //nolint:gochecknoglobals // global lock
)

const sortAsc = "asc"
const consoleResizeSleep = 100 * time.Millisecond
const percentDivisor = 100
const defaultScreenshotSize = "215"

// GetExperiments - GET /experiments.
func GetExperiments(w http.ResponseWriter, r *http.Request) {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		query = r.URL.Query()
		size  = query.Get("screenshot")
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

	for _, exp := range experiments {
		if !role.Allowed("experiments", "list", exp.Metadata.Name) {
			continue
		}

		// This will happen if another handler is currently acting on the
		// experiment.
		status := cache.IsExperimentLocked(exp.Metadata.Name)

		if status == "" {
			if exp.Running() {
				status = cache.StatusStarted
			} else {
				status = cache.StatusStopped
			}
		}

		// TODO: limit per-experiment VMs based on RBAC

		vms, err := vm.List(exp.Spec.ExperimentName())
		if err != nil {
			plog.Error(
				plog.TypeSystem,
				"listing VMs for experiment",
				"exp",
				exp.Spec.ExperimentName(),
				"err",
				err,
			)
		}

		if exp.Running() && size != "" {
			for i, v := range vms {
				if !v.Running {
					continue
				}

				screenshot, err := util.GetScreenshot(exp.Spec.ExperimentName(), v.Name, size)
				if err != nil {
					plog.Error(plog.TypeSystem, "getting screenshot", "err", err)

					continue
				}

				v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
					screenshot,
				)

				vms[i] = v
			}
		}

		allowed = append(allowed, util.ExperimentToProtobuf(exp, status, vms))
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
	}

	if req.GetWorkflowBranch() != "" {
		annotations := map[string]string{"phenix.workflow/branch": req.GetWorkflowBranch()}
		opts = append(opts, experiment.CreateWithAnnotations(annotations))
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

	vms, err := vm.List(req.GetName())
	if err != nil {
		plog.Error(plog.TypeSystem, "listing experiment VMs", "exp", req.GetName(), "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

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
//nolint:funlen // handler
func GetExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx          = r.Context()
		role         = middleware.RoleFromContext(ctx)
		vars         = mux.Vars(r)
		name         = vars["name"]
		query        = r.URL.Query()
		size         = query.Get("screenshot")
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

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	vms, err := vm.List(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "listing VMs for experiment", "exp", name, "err", err)
	}

	// This will happen if another handler is currently acting on the
	// experiment.
	status := cache.IsExperimentLocked(name)
	allowed := mm.VMs{}

	// Build a Boolean expression tree and determine
	// the fields that should be searched
	filterTree := mm.BuildTree(clientFilter)

	for _, vm := range vms {
		if vm.DoNotBoot && !showDNB {
			continue
		}

		// If the filter supplied could not be
		// parsed, do not add the VM
		if len(clientFilter) > 0 {
			if filterTree == nil {
				continue
			} else if !filterTree.Evaluate(&vm) {
				// If the search string could be parsed,
				// determine if the VM should be included
				continue
			}
		}

		if role.Allowed("vms", "list", fmt.Sprintf("%s/%s", name, vm.Name)) {
			if vm.Running && size != "" {
				screenshot, err := util.GetScreenshot(name, vm.Name, size)
				if err != nil {
					plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
						screenshot,
					)
				}
			}

			allowed = append(allowed, vm)
		}
	}

	if sortCol != "" && sortDir != "" {
		allowed.SortBy(sortCol, sortDir == sortAsc)
	}

	totalBeforePaging := len(allowed)

	if pageNum != "" && perPage != "" {
		n, _ := strconv.Atoi(pageNum)
		s, _ := strconv.Atoi(perPage)

		allowed = allowed.Paginate(n, s)
	}

	experiment := util.ExperimentToProtobuf(*exp, status, allowed)
	experiment.VmCount = uint32(totalBeforePaging) //nolint:gosec // integer overflow conversion int -> uint32

	body, err := marshaler.Marshal(experiment)
	if err != nil {
		err := weberror.NewWebError(err, "marshaling experiment %s - %v", name, err)

		return err.SetStatus(http.StatusInternalServerError)
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
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

	body, err := startExperiment(name)
	if err != nil {
		return err
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
		"experiment apps trigger cancelled",
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
		captures = mm.GetExperimentCaptures(mm.NS(name))
		allowed  []mm.Capture
	)

	for _, capture := range captures {
		if role.Allowed("experiments/captures", "list", capture.VM) {
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

	files, err := experiment.Files(name, clientFilter)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting list of files for experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	if sortCol != "" && sortDir != "" {
		files.SortBy(sortCol, sortDir == "asc")
	}

	if pageNum != "" && perPage != "" {
		n, _ := strconv.Atoi(pageNum)
		s, _ := strconv.Atoi(perPage)

		files = files.Paginate(n, s)
	}

	body, err := json.Marshal(util.WithRoot("files", files))
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
		if errors.Is(err, mm.ErrCaptureExists) {
			http.Error(w, "capture still in progress", http.StatusBadRequest)

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

	w.Header().Set("Content-Disposition", "attachment; filename="+file)
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

	exp, err := experiment.Get(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	vms, err := vm.List(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	allowed := mm.VMs{}

	for _, vm := range vms {
		if role.Allowed("vms", "list", fmt.Sprintf("%s/%s", expName, vm.Name)) {
			if vm.Running && size != "" {
				screenshot, err := util.GetScreenshot(expName, vm.Name, size)
				if err != nil {
					plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
						screenshot,
					)
				}
			}

			allowed = append(allowed, vm)
		}
	}

	if sortCol != "" && sortDir != "" {
		allowed.SortBy(sortCol, sortDir == "asc")
	}

	if pageNum != "" && perPage != "" {
		n, _ := strconv.Atoi(pageNum)
		s, _ := strconv.Atoi(perPage)

		allowed = allowed.Paginate(n, s)
	}

	resp := &proto.VMList{
		Total: uint32(len(allowed)), //nolint:gosec // integer overflow conversion int -> uint32
		Vms:   make([]*proto.VM, len(allowed)),
	}
	for i, v := range allowed {
		resp.Vms[i] = util.VMToProtobuf(expName, v, exp.Spec.Topology())
	}

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

	vm, err := vm.Get(expName, name)
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

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *vm, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
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

	vm, err := vm.Get(expName, name)
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

		vm, err := vm.Get(expName, vmRequest.GetName())
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
		bt.NewResource("experiment/vm", name, "starting"),
		nil,
	)

	if err := mm.StartVM(mm.NS(expName), mm.VMName(name)); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/start", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStarting"),
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
		bt.NewResource("experiment/vm", name, "stopping"),
		nil,
	)

	if err := mm.StopVM(mm.NS(expName), mm.VMName(name)); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/stop", "update", fullName),
			bt.NewResource("experiment/vm", name, "errorStopping"),
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
		bt.NewResource("experiment/vm", name, "restarting"),
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

	v, err := vm.Get(expName, name)
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

	v, err := vm.Get(expName, name)
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

	v, err := vm.Get(expName, name)
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
		query    = r.URL.Query()
		inject   = query.Get("replicate-injects") != ""
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

	v, err := vm.Get(expName, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	v.Busy = true

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))

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

		body, err := io.ReadAll(r.Body)
		if err != nil && !errors.Is(err, io.EOF) {
			redeployed <- err

			return
		}

		opts := []vm.RedeployOption{
			vm.CPU(v.CPUs),
			vm.Memory(v.RAM),
			vm.Disk(v.Disk),
			vm.Inject(inject),
		}

		// `body` will be nil if err above was EOF.
		if body != nil {
			var req proto.VMRedeployRequest

			// Update VM struct with values from POST request body.
			err := unmarshaler.Unmarshal(body, &req)
			if err != nil {
				redeployed <- err

				return
			}

			opts = []vm.RedeployOption{
				vm.CPU(int(req.GetCpus())),
				vm.Memory(int(req.GetRam())),
				vm.Disk(req.GetDisk()),
				vm.Inject(req.GetInjects()),
			}
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

// StopVMCaptures - DELETE /experiments/{exp}/vms/{name}/captures.
func StopVMCaptures(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
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

	err := vm.StopCaptures(exp, name)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"stopping captures for VM",
			"exp",
			exp,
			"name",
			name,
			"err",
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "stop"),
		nil,
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

	if !role.Allowed("exp/captureSubnet", "create", exp) {
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
				plog.Debug(plog.TypeSystem, "snapshot percent complete", "percent", progress)

				status := map[string]any{
					"percent": progress / percentDivisor,
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
			plog.Debug(plog.TypeSystem, "VM commit percent complete", "percent", s)

			status := map[string]any{
				"percent": s,
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

	v, err := vm.Get(expName, name)
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
					"percent": progress,
				}

				plog.Info(plog.TypeSystem, "memory snapshot percent complete", "percent", progress)

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

		vms, err := vm.List(exp.Spec.ExperimentName())
		if err != nil {
			plog.Error(
				plog.TypeSystem,
				"listing VMs for experiment",
				"exp",
				exp.Spec.ExperimentName(),
				"err",
				err,
			)
		}

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

			allowed = append(allowed, util.VMToProtobuf(exp.Metadata.Name, vm, exp.Spec.Topology()))
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

// CreateConsole - POST /console.
func CreateConsole(w http.ResponseWriter, r *http.Request) {
	if !o.minimegaConsole {
		plog.Error(plog.TypeSystem, "request made for minimega console, but console not enabled")
		http.Error(w, "'minimega-console' CLI arg not enabled", http.StatusMethodNotAllowed)

		return
	}

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("miniconsole", "post") {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"creating miniconsole not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// create a new console
	phenix, err := os.Executable()
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to get full path to phenix")
		http.Error(w, "", http.StatusInternalServerError)

		return
	}

	cmd := exec.CommandContext(r.Context(), phenix, "mm", "--attach") //nolint:gosec // Command injection via taint analysis

	tty, err := pty.Start(cmd)
	if err != nil {
		http.Error(
			w,
			fmt.Sprintf("could not start terminal: %v", err),
			http.StatusInternalServerError,
		)

		return
	}

	pid := cmd.Process.Pid

	plog.Info(plog.TypeSystem, "spawned new minimega console", "pid", pid)

	ptyMu.Lock()
	ptys[pid] = tty
	ptyMu.Unlock()

	body, _ := json.Marshal(util.WithRoot("pid", pid))

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"miniconsole created",
		"user",
		user,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// ResizeConsole - POST /console/{pid}/size?cols={[0-9]+}&rows={[0-9]+}.
func ResizeConsole(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	if !role.Allowed("miniconsole", "post") {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"resizing miniconsole not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	vars := mux.Vars(r)

	pid, err := strconv.Atoi(vars["pid"])
	if err != nil {
		http.Error(w, "invalid pid", http.StatusBadRequest)

		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)

		return
	}

	ptyMu.Lock()

	tty, ok := ptys[pid]
	if !ok {
		http.Error(w, "pty not found", http.StatusNotFound)

		return
	}

	ptyMu.Unlock()

	rows, err := strconv.ParseUint(r.FormValue("rows"), 10, 16)
	if err != nil {
		http.Error(w, "invalid rows", http.StatusBadRequest)

		return
	}

	cols, err := strconv.ParseUint(r.FormValue("cols"), 10, 16)
	if err != nil {
		http.Error(w, "invalid cols", http.StatusBadRequest)

		return
	}

	plog.Debug(plog.TypeSystem, "resize console", "pid", pid, "cols", cols, "rows", rows)

	ws := struct {
		R, C, X, Y uint16
	}{
		R: uint16(rows), C: uint16(cols), X: 0, Y: 0,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		tty.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)

	if errno != 0 {
		plog.Error(plog.TypeSystem, "unable to set winsize", "err", errno)
		http.Error(w, "set winsize failed", http.StatusInternalServerError)
	}

	// make sure winsize gets processed, hopefully the user isn't typing...
	time.Sleep(consoleResizeSleep)

	_, _ = io.WriteString(tty, "\n")
}

// WsConsole - GET /console/{pid}/ws.
func WsConsole(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	if !role.Allowed("miniconsole", "get") {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting miniconsole not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	vars := mux.Vars(r)

	pid, err := strconv.Atoi(vars["pid"])
	if err != nil {
		http.Error(w, "invalid pid", http.StatusBadRequest)

		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)

		return
	}

	ptyMu.Lock()

	tty, ok := ptys[pid]
	if !ok {
		http.Error(w, "pty not found", http.StatusNotFound)

		return
	}

	ptyMu.Unlock()

	websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = tty.Close() }()

		proc, err := os.FindProcess(pid)
		if err != nil {
			plog.Warn(plog.TypeSystem, "unable to find process", "pid", pid)

			return
		}

		go func() { _, _ = io.Copy(ws, tty) }()

		_, _ = io.Copy(tty, ws)

		plog.Debug(plog.TypeSystem, "killing minimega console", "pid", pid)

		_ = proc.Kill()
		_, _ = proc.Wait()

		ptyMu.Lock()
		delete(ptys, pid)
		ptyMu.Unlock()

		plog.Debug(plog.TypeSystem, "killed minimega console", "pid", pid)
	}).ServeHTTP(w, r)
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
	_ = settings.SetDefaults()

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
