package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"phenix/api/cluster"
	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/scenario"
	"phenix/api/vm"
	"phenix/app"
	"phenix/store"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/broker"
	"phenix/web/cache"
	"phenix/web/proto"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"

	putil "phenix/util"
	bt "phenix/web/broker/brokertypes"

	"github.com/creack/pty"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

var (
	marshaler   = protojson.MarshalOptions{EmitUnpopulated: true}
	unmarshaler = protojson.UnmarshalOptions{AllowPartial: true, DiscardUnknown: true}

	ptys  = map[int]*os.File{}
	ptyMu sync.Mutex
)

// GET /experiments
func GetExperiments(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetExperiments")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		query = r.URL.Query()
		size  = query.Get("screenshot")
	)

	if !role.Allowed("experiments", "list") {
		plog.Warn("listing experiments not allowed", "user", ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	experiments, err := experiment.List()
	if err != nil {
		plog.Error("getting experiments", "err", err)
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
			// TODO
		}

		if exp.Running() && size != "" {
			for i, v := range vms {
				if !v.Running {
					continue
				}

				screenshot, err := util.GetScreenshot(exp.Spec.ExperimentName(), v.Name, size)
				if err != nil {
					plog.Error("getting screenshot", "err", err)
					continue
				}

				v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)

				vms[i] = v
			}
		}

		allowed = append(allowed, util.ExperimentToProtobuf(exp, status, vms))
	}

	body, err := marshaler.Marshal(&proto.ExperimentList{Experiments: allowed})
	if err != nil {
		plog.Error("marshaling experiments", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /experiments
func CreateExperiment(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "CreateExperiment")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("experiments", "create") {
		plog.Warn("creating experiments not allowed", "user", ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	var req proto.CreateExperimentRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		plog.Error("unmashaling request body", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if err := cache.LockExperimentForCreation(req.Name); err != nil {
		plog.Error("locking experiment", "exp", req.Name, "action", "creation", "err", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockExperiment(req.Name)

	opts := []experiment.CreateOption{
		experiment.CreateWithName(req.Name),
		experiment.CreateWithTopology(req.Topology),
		experiment.CreateWithScenario(req.Scenario),
		experiment.CreateWithVLANMin(int(req.VlanMin)),
		experiment.CreateWithVLANMax(int(req.VlanMax)),
		experiment.CreatedWithDisabledApplications(req.DisabledApps),
	}

	if req.WorkflowBranch != "" {
		annotations := map[string]string{"phenix.workflow/branch": req.WorkflowBranch}
		opts = append(opts, experiment.CreateWithAnnotations(annotations))
	}

	if err := experiment.Create(ctx, opts...); err != nil {
		plog.Error("creating experiment", "exp", req.Name, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if warns := notes.Warnings(ctx, true); warns != nil {
		for _, warn := range warns {
			plog.Warn("creating experiment", "warnings", warn)
		}
	}

	exp, err := experiment.Get(req.Name)
	if err != nil {
		plog.Error("getting experiment", "exp", req.Name, "err", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	vms, err := vm.List(req.Name)
	if err != nil {
		plog.Error("listing experiment VMs", "exp", req.Name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err = marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vms))
	if err != nil {
		plog.Error("marshaling experiment", "err", req.Name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", req.Name),
		bt.NewResource("experiment", req.Name, "create"),
		body,
	)

	w.WriteHeader(http.StatusNoContent)
}

// PUT /experiments/{name}
func UpdateExperiment(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "UpdateExperiment")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments", "patch", name) {
		err := weberror.NewWebError(nil, "updating experiment %s not allowed for %s", name, ctx.Value("user").(string))
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

		if err := exp.WriteToStore(false); err != nil {
			err := weberror.NewWebError(err, "unable to write updated experiment %s", name)
			return err.SetStatus(http.StatusInternalServerError)
		}
	}

	return nil
}

// GET /experiments/{name}
func GetExperiment(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "GetExperiment")

	var (
		ctx          = r.Context()
		role         = ctx.Value("role").(rbac.Role)
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
		err := weberror.NewWebError(nil, "getting experiment %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	vms, err := vm.List(name)
	if err != nil {
		// TODO
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
			} else {
				// If the search string could be parsed,
				// determine if the VM should be included
				if !filterTree.Evaluate(&vm) {
					continue
				}
			}
		}

		if role.Allowed("vms", "list", fmt.Sprintf("%s/%s", name, vm.Name)) {
			if vm.Running && size != "" {
				screenshot, err := util.GetScreenshot(name, vm.Name, size)
				if err != nil {
					plog.Error("getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
				}
			}

			allowed = append(allowed, vm)
		}
	}

	if sortCol != "" && sortDir != "" {
		allowed.SortBy(sortCol, sortDir == "asc")
	}

	totalBeforePaging := len(allowed)

	if pageNum != "" && perPage != "" {
		n, _ := strconv.Atoi(pageNum)
		s, _ := strconv.Atoi(perPage)

		allowed = allowed.Paginate(n, s)
	}

	experiment := util.ExperimentToProtobuf(*exp, status, allowed)
	experiment.VmCount = uint32(totalBeforePaging)
	body, err := marshaler.Marshal(experiment)

	if err != nil {
		err := weberror.NewWebError(err, "marshaling experiment %s - %v", name, err)
		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Write(body)
	return nil
}

// DELETE /experiments/{name}
func DeleteExperiment(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "DeleteExperiment")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments", "delete", name) {
		plog.Warn("deleting experiment not allowed", "user", ctx.Value("user").(string), "exp", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockExperimentForDeletion(name); err != nil {
		plog.Error("locking experiment", "exp", name, "action", "deletion", "err", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockExperiment(name)

	if err := experiment.Delete(name); err != nil {
		plog.Error("deleting experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "delete", name),
		bt.NewResource("experiment", name, "delete"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{name}/start
func StartExperiment(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "StartExperiment")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/start", "update", name) {
		err := weberror.NewWebError(nil, "starting experiment %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	body, err := startExperiment(name)
	if err != nil {
		return err
	}

	w.Write(body)
	return nil
}

// POST /experiments/{name}/stop
func StopExperiment(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "StopExperiment")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/stop", "update", name) {
		err := weberror.NewWebError(nil, "stopping experiment %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	body, err := stopExperiment(name)
	if err != nil {
		return err
	}

	w.Write(body)
	return nil
}

// POST /experiments/{name}/trigger[?apps=<foo,bar,baz>]
func TriggerExperimentApps(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "TriggerExperimentApps")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
	)

	if !role.Allowed("experiments/trigger", "create", name) {
		plog.Warn("triggering experiment apps not allowed", "user", ctx.Value("user").(string), "exp", name)
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
			pubsub.Publish("trigger-app", app.TriggerPublication{
				Experiment: name, App: a, State: "start",
			})

			k := fmt.Sprintf("%s/%s", name, a)

			// We don't want to use the HTTP request's context here.
			ctx, cancel := context.WithCancel(context.Background())
			ctx = app.SetContextTriggerUI(ctx)
			ctx = app.SetContextMetadata(ctx, md)
			cancelers[k] = append(cancelers[k], cancel)

			if err := experiment.TriggerRunning(ctx, name, a); err != nil {
				cancel() // avoid leakage
				delete(cancelers, k)

				humanized := putil.HumanizeError(err, "Unable to trigger running stage for %s app in %s experiment", a, name)
				pubsub.Publish("trigger-app", app.TriggerPublication{
					Experiment: name, App: a, State: "error", Error: humanized,
				})

				plog.Error("triggering experiment app", "exp", name, "app", a, "err", err)
				return
			}

			pubsub.Publish("trigger-app", app.TriggerPublication{
				Experiment: name, App: a, State: "success",
			})
		}
	}()

	w.WriteHeader(http.StatusNoContent)
}

// DELETE /experiments/{name}/trigger[?apps=<foo,bar,baz>]
func CancelTriggeredExperimentApps(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "CancelTriggeredExperimentApps")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
	)

	if !role.Allowed("experiments/trigger", "delete", name) {
		plog.Warn("canceling triggered experiment apps not allowed", "user", ctx.Value("user").(string), "exp", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	go func() {
		apps := strings.Split(appsFilter, ",")

		for _, a := range apps {
			k := fmt.Sprintf("%s/%s", name, a)

			cancels := cancelers[k]

			for _, cancel := range cancels {
				cancel()
			}

			delete(cancelers, k)

			pubsub.Publish("trigger-app", app.TriggerPublication{
				Experiment: name, Verb: "delete", App: a, State: "success",
			})
		}
	}()

	w.WriteHeader(http.StatusNoContent)
}

// GET /experiments/{name}/schedule
func GetExperimentSchedule(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetExperimentSchedule")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/schedule", "get", name) {
		plog.Warn("getting experiment schedule not allowed", "user", ctx.Value("user").(string), "exp", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if status := cache.IsExperimentLocked(name); status != "" {
		plog.Warn("experiment locked", "exp", name, "status", status)
		http.Error(w, fmt.Sprintf("experiment %s is cache.Locked with status %s", name, status), http.StatusConflict)

		return
	}

	exp, err := experiment.Get(name)
	if err != nil {
		plog.Error("getting experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err := marshaler.Marshal(util.ExperimentScheduleToProtobuf(*exp))
	if err != nil {
		plog.Error("marshaling schedule for experiment", "exp", name, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /experiments/{name}/schedule
func ScheduleExperiment(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "ScheduleExperiment")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/schedule", "create", name) {
		plog.Warn("creating experiment schedule not allowed", "user", ctx.Value("user").(string), "exp", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if status := cache.IsExperimentLocked(name); status != "" {
		plog.Warn("experiment locked", "exp", name, "status", status)
		http.Error(w, fmt.Sprintf("experiment %s is cache.Locked with status %s", name, status), http.StatusConflict)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.UpdateScheduleRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error("unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = experiment.Schedule(experiment.ScheduleForName(name), experiment.ScheduleWithAlgorithm(req.Algorithm))
	if err != nil {
		plog.Error("scheduling experiment", "exp", name, "algorithm", req.Algorithm, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exp, err := experiment.Get(name)
	if err != nil {
		plog.Error("getting experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err = marshaler.Marshal(util.ExperimentScheduleToProtobuf(*exp))
	if err != nil {
		plog.Error("marshaling schedule for experiment", "exp", name, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/schedule", "create", name),
		bt.NewResource("experiment", name, "schedule"),
		body,
	)

	w.Write(body)
}

// GET /experiments/{name}/captures
func GetExperimentCaptures(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetExperimentCaptures")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/captures", "list", name) {
		plog.Warn("listing experiment captures not allowed", "user", ctx.Value("user").(string), "exp", name)
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
		plog.Error("marshaling captures for experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /experiments/{name}/files
func GetExperimentFiles(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetExperimentFiles")

	var (
		ctx          = r.Context()
		role         = ctx.Value("role").(rbac.Role)
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
		plog.Warn("listing experiment files not allowed", "user", ctx.Value("user").(string), "exp", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	files, err := experiment.Files(name, clientFilter)
	if err != nil {
		plog.Error("getting list of files for experiment", "exp", name, "err", err)
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
		plog.Error("marshaling file list for experiment", "exp", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /experiments/{name}/files/{filename}
func GetExperimentFile(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetExperimentFile")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		vars  = mux.Vars(r)
		name  = vars["name"]
		file  = vars["filename"]
		query = r.URL.Query()
		path  = query.Get("path")
	)

	if !role.Allowed("experiments/files", "get", name) {
		plog.Warn("getting experiment file not allowed", "user", ctx.Value("user").(string), "exp", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	contents, err := experiment.File(name, path)
	if err != nil {
		if errors.Is(err, mm.ErrCaptureExists) {
			http.Error(w, "capture still in progress", http.StatusBadRequest)
			return
		}

		plog.Error("getting file for experiment", "exp", name, "file", path, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("Accept") == "text/plain" {
		w.Header().Set("Content-Type", "text/plain")
		w.Write(contents)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+file)
	http.ServeContent(w, r, "", time.Now(), bytes.NewReader(contents))
}

// GET /experiments/{name}/apps
func GetExperimentApps(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "GetExperimentApps")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		name = mux.Vars(r)["name"]
	)

	if !role.Allowed("experiments/apps", "get", name) {
		err := weberror.NewWebError(nil, "getting experiment apps for %s not allowed for %s", name, ctx.Value("user").(string))
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

	for app, running := range exp.Status.AppRunning() {
		apps[app] = running
	}

	body, _ := json.Marshal(apps)

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)

	return nil
}

// GET /experiments/{exp}/vms
func GetVMs(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetVMs")

	var (
		ctx     = r.Context()
		role    = ctx.Value("role").(rbac.Role)
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
					plog.Error("getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
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

	resp := &proto.VMList{Total: uint32(len(allowed))}

	resp.Vms = make([]*proto.VM, len(allowed))
	for i, v := range allowed {
		resp.Vms[i] = util.VMToProtobuf(expName, v, exp.Spec.Topology())
	}

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}
func GetVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetVM")

	var (
		ctx     = r.Context()
		role    = ctx.Value("role").(rbac.Role)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
		query   = r.URL.Query()
		size    = query.Get("screenshot")
	)

	if !role.Allowed("vms", "get", fmt.Sprintf("%s/%s", expName, name)) {
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
			plog.Error("getting screenshot", "err", err)
		} else {
			vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		}
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *vm, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// PATCH /experiments/{exp}/vms/{name}
func UpdateVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "UpdateVM")

	var (
		ctx     = r.Context()
		role    = ctx.Value("role").(rbac.Role)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, name)) {
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
		vm.UpdateWithCPU(int(req.Cpus)),
		vm.UpdateWithMem(int(req.Ram)),
		vm.UpdateWithDisk(req.Disk),
	}

	if req.Interface != nil {
		opts = append(opts, vm.UpdateWithInterface(int(req.Interface.Index), req.Interface.Vlan))
	}

	switch req.Boot.(type) {
	case *proto.UpdateVMRequest_DoNotBoot:
		opts = append(opts, vm.UpdateWithDNB(req.GetDoNotBoot()))
	}

	switch req.ClusterHost.(type) {
	case *proto.UpdateVMRequest_Host:
		opts = append(opts, vm.UpdateWithHost(req.GetHost()))
	}

	if err := vm.Update(opts...); err != nil {
		plog.Error("updating VM", "err", err)
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
		screenshot, err := util.GetScreenshot(expName, name, "215")
		if err != nil {
			plog.Error("getting screenshot", "err", err)
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

	w.Write(body)
}

// PATCH /experiments/{exp}/vms
func UpdateVMs(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "UpdateVMs")

	var (
		ctx     = r.Context()
		role    = ctx.Value("role").(rbac.Role)
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

	resp := &proto.VMList{Total: req.Total}
	resp.Vms = make([]*proto.VM, int(req.Total))

	for index, vmRequest := range req.Vms {
		// Skip any vms that are not allowed to be updated
		if !role.Allowed("vms", "patch", fmt.Sprintf("%s/%s", expName, vmRequest.Name)) {
			plog.Error("%s/%s is forbidden", expName, vmRequest.Name)
			continue
		}

		opts := []vm.UpdateOption{
			vm.UpdateExperiment(expName),
			vm.UpdateVM(vmRequest.Name),
			vm.UpdateWithCPU(int(vmRequest.Cpus)),
			vm.UpdateWithMem(int(vmRequest.Ram)),
			vm.UpdateWithDisk(vmRequest.Disk),
		}

		if vmRequest.Interface != nil {
			opts = append(opts, vm.UpdateWithInterface(int(vmRequest.Interface.Index), vmRequest.Interface.Vlan))
		}

		switch vmRequest.Boot.(type) {
		case *proto.UpdateVMRequest_DoNotBoot:
			opts = append(opts, vm.UpdateWithDNB(vmRequest.GetDoNotBoot()))
		}

		switch vmRequest.ClusterHost.(type) {
		case *proto.UpdateVMRequest_Host:
			opts = append(opts, vm.UpdateWithHost(vmRequest.GetHost()))
		}

		if err := vm.Update(opts...); err != nil {
			plog.Error("updating VM", "err", err)
			http.Error(w, "unable to update VM", http.StatusInternalServerError)
			return
		}

		exp, err := experiment.Get(expName)
		if err != nil {
			http.Error(w, "unable to get experiment", http.StatusBadRequest)
			return
		}

		vm, err := vm.Get(expName, vmRequest.Name)
		if err != nil {
			http.Error(w, "unable to get VM", http.StatusInternalServerError)
			return
		}

		if vm.Running {
			screenshot, err := util.GetScreenshot(expName, vmRequest.Name, "215")
			if err != nil {
				plog.Error("getting screenshot", "err", err)
			} else {
				vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
			}
		}

		resp.Vms[index] = util.VMToProtobuf(expName, *vm, exp.Spec.Topology())
	}

	body, err = marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// DELETE /experiments/{exp}/vms/{name}
func DeleteVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "DeleteVM")

	var (
		ctx     = r.Context()
		role    = ctx.Value("role").(rbac.Role)
		vars    = mux.Vars(r)
		expName = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms", "delete", fmt.Sprintf("%s/%s", expName, name)) {
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

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{exp}/vms/{name}/start
func StartVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "StartVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/start", "update", fullName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockVMForStarting(expName, name); err != nil {
		plog.Error("locking VM", "exp", expName, "vm", name, "action", "starting", "err", err)
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
		plog.Error("getting screenshot", "err", err)
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

	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/stop
func StopVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "StopVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/stop", "update", fullName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error("locking VM", "exp", expName, "vm", name, "action", "stopping", "err", err)
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

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/restart
func RestartVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "RestartVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/restart", "update", fullName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockVMForStarting(expName, name); err != nil {
		plog.Error("locking VM", "exp", expName, "vm", name, "action", "starting", "err", err)
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

	screenshot, err := util.GetScreenshot(expName, name, "215")
	if err != nil {
		plog.Error("getting screenshot", "err", err)
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

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/shutdown
func ShutdownVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "ShutdownVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/shutdown", "update", fullName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error("locking VM", "exp", expName, "vm", name, "action", "stopping", "err", err)
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

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/reset
func ResetVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "ResetVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/reset", "update", fullName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockVMForStopping(expName, name); err != nil {
		plog.Error("locking VM", "exp", expName, "vm", name, "action", "stopping", "err", err)
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

	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/redeploy
func RedeployVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "RedeployVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
		query    = r.URL.Query()
		inject   = query.Get("replicate-injects") != ""
	)

	if !role.Allowed("vms/redeploy", "update", fullName) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockVMForRedeploying(expName, name); err != nil {
		plog.Error("locking VM", "exp", expName, "vm", name, "action", "redeploying", "err", err)
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

	body, _ := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
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
		if err != nil && err != io.EOF {
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
			if err := unmarshaler.Unmarshal(body, &req); err != nil {
				redeployed <- err
				return
			}

			opts = []vm.RedeployOption{
				vm.CPU(int(req.Cpus)),
				vm.Memory(int(req.Ram)),
				vm.Disk(req.Disk),
				vm.Inject(req.Injects),
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
	time.Sleep(5 * time.Second)

	err = <-redeployed
	if err != nil {
		plog.Error("redeploying VM", "exp", expName, "vm", name, "err", err)

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

	screenshot, err := util.GetScreenshot(expName, name, "215")
	if err != nil {
		plog.Error("getting screenshot", "err", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, _ = marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))

	broker.Broadcast(
		bt.NewRequestPolicy("vms/redeploy", "update", fullName),
		bt.NewResource("experiment/vm", expName+"/"+name, "redeployed"),
		body,
	)

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/screenshot.png
func GetScreenshot(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetScreenshot")

	var (
		ctx    = r.Context()
		role   = ctx.Value("role").(rbac.Role)
		vars   = mux.Vars(r)
		exp    = vars["exp"]
		name   = vars["name"]
		query  = r.URL.Query()
		size   = query.Get("size")
		encode = query.Get("base64") != ""
	)

	if !role.Allowed("vms/screenshot", "get", exp+"/"+name) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if size == "" {
		size = "215"
	}

	screenshot, err := util.GetScreenshot(exp, name, size)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if encode {
		encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
		w.Write([]byte(encoded))
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(screenshot)
}

// GET /experiments/{exp}/vms/{name}/captures
func GetVMCaptures(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetVMCaptures")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "list", fmt.Sprintf("%s/%s", exp, name)) {
		plog.Warn("getting captures for VM not allowed", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	captures := mm.GetVMCaptures(mm.NS(exp), mm.VMName(name))

	body, err := marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(captures)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/captures
func StartVMCapture(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "StartVMCapture")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)) {
		plog.Warn("starting capture for VM not allowed", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.StartCaptureRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error("unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := vm.StartCapture(exp, name, int(req.Interface), req.Filename); err != nil {
		plog.Error("starting capture for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "start"),
		body,
	)

	w.WriteHeader(http.StatusNoContent)
}

// DELETE /experiments/{exp}/vms/{name}/captures
func StopVMCaptures(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "StopVMCaptures")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)) {
		plog.Warn("stopping captures for VM not allowed", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := vm.StopCaptures(exp, name); err != nil {
		plog.Error("stopping captures for VM", "exp", exp, "name", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "stop"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{exp}/captureSubnet
func StartCaptureSubnet(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "StartCaptureSubnet")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
	)

	if !role.Allowed("exp/captureSubnet", "create", exp) {
		plog.Warn("starting subnet capture for experiment not allowed", "user", ctx.Value("user").(string), "exp", exp)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.CaptureSubnetRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error("unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	vmCaptures, err := vm.CaptureSubnet(exp, req.Subnet, req.Vms)

	if err != nil {
		plog.Error("unable to start subnet capture", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err = marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(vmCaptures)})
	if err != nil {
		plog.Error("unable to marshal vm capture list", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /experiments/{exp}/stopCaptureSubnet
func StopCaptureSubnet(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "StopCaptureSubnet")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
	)

	if !role.Allowed("exp/captureSubnet", "create", exp) {
		plog.Warn("stopping subnet capture for experiment not allowed", "user", ctx.Value("user").(string), "exp", exp)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.CaptureSubnetRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		plog.Error("unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	vms, err := vm.StopCaptureSubnet(exp, req.Subnet, req.Vms)
	if err != nil {
		plog.Error("unable to stop subnet capture", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err = marshaler.Marshal(&proto.VMNameList{Vms: vms})
	if err != nil {
		plog.Error("unable to marshal vm capture list", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/snapshots
func GetVMSnapshots(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetVMSnapshots")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/snapshots", "list", fmt.Sprintf("%s/%s", exp, name)) {
		plog.Warn("listing snapshots for VM not allowed", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	snapshots, err := vm.Snapshots(exp, name)
	if err != nil {
		plog.Error("getting list of snapshots for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err := marshaler.Marshal(&proto.SnapshotList{Snapshots: snapshots})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/snapshots
func SnapshotVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "SnapshotVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/snapshots", "create", fullName) {
		plog.Warn("snapshotting VM not allowed", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.SnapshotRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		plog.Error("unmarshaling request body", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := cache.LockVMForSnapshotting(exp, name); err != nil {
		plog.Error("locking VM", "exp", exp, "vm", name, "action", "snapshotting", "err", err)
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
				plog.Debug("snapshot percent complete", "percent", progress)

				status := map[string]interface{}{
					"percent": progress / 100,
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

	if err := vm.Snapshot(exp, name, req.Filename, cb); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/snapshots", "create", fullName),
			bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "errorCreating"),
			nil,
		)

		plog.Error("snapshotting VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "create"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{exp}/vms/{name}/snapshots/{snapshot}
func RestoreVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "RestoreVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
		snap     = vars["snapshot"]
	)

	if !role.Allowed("vms/snapshots", "update", fullName) {
		plog.Warn("restoring VM not allowed", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockVMForRestoring(exp, name); err != nil {
		plog.Error("locking VM", "exp", exp, "vm", name, "action", "restoring", "err", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockVM(exp, name)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", fmt.Sprintf("%s/%s", exp, name), "restoring"),
		nil,
	)

	if err := vm.Restore(exp, name, snap); err != nil {
		broker.Broadcast(
			bt.NewRequestPolicy("vms/snapshots", "create", fullName),
			bt.NewResource("experiment/vm/snapshot", fmt.Sprintf("%s/%s", exp, name), "errorRestoring"),
			nil,
		)

		plog.Error("restoring VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/snapshots", "create", fullName),
		bt.NewResource("experiment/vm/snapshot", exp+"/"+name, "restore"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{exp}/vms/{name}/commit
func CommitVM(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "CommitVM")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/commit", "create", fullName) {
		plog.Warn("committing VM not allowed", "user", ctx.Value("user").(string), "exp", expName, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
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
			plog.Error("unmarshaling request body", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Filename == "" {
			plog.Error("missing filename for commit")
			http.Error(w, "missing 'filename' key", http.StatusBadRequest)
			return
		}

		filename = req.Filename
	}

	if err := cache.LockVMForCommitting(expName, name); err != nil {
		plog.Error("locking VM", "exp", expName, "vm", name, "action", "committing", "err", err)
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

		// TODO

		http.Error(w, "must provide new disk name for commit", http.StatusBadRequest)
		return
	}

	payload := &proto.BackingImageResponse{Disk: filename}
	body, _ = marshaler.Marshal(payload)

	broker.Broadcast(
		bt.NewRequestPolicy("vms/commit", "create", fullName),
		bt.NewResource("experiment/vm/commit", expName+"/"+name, "committing"),
		body,
	)

	status := make(chan float64)

	go func() {
		for s := range status {
			plog.Info("VM commit percent complete", "percent", s)

			status := map[string]interface{}{
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

		plog.Error("committing VM", "exp", expName, "vm", name, "err", err)
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

	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/memorySnapshot
func CreateVMMemorySnapshot(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "CreateVMMemorySnapshot")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/memorySnapshot", "create", fullName) {
		plog.Warn("capturing memory snapshot of VM not allowed", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
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
			plog.Error("unmarshaling request body", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Filename == "" {
			plog.Error("missing filename for memory snapshot")
			http.Error(w, "missing 'filename' key", http.StatusBadRequest)
			return
		}

		filename = req.Filename
	}

	if err := cache.LockVMForMemorySnapshotting(exp, name); err != nil {
		plog.Error("locking VM", "exp", exp, "vm", name, "action", "memory snapshotting", "err", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockVM(exp, name)

	if filename == "" {

		http.Error(w, "must provide new disk name for memory snapshot", http.StatusBadRequest)
		return
	}

	payload := &proto.MemorySnapshotResponse{Disk: filename}
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
				status := map[string]interface{}{
					"percent": progress,
				}

				plog.Info("memory snapshot percent complete", "percent", progress)

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

		plog.Error("memory snapshot for VM", "exp", exp, "vm", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		bt.NewRequestPolicy("vms/memorySnapshot", "create", fmt.Sprintf("%s/%s", exp, name)),
		bt.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "commit"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// GET /vms
func GetAllVMs(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetAllVMs")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		query = r.URL.Query()
		size  = query.Get("screenshot")
	)

	if !role.Allowed("vms", "list") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	exps, err := experiment.List()
	if err != nil {
		plog.Error("getting experiments", "err", err)
	}

	allowed := []*proto.VM{}

	for _, exp := range exps {
		if !exp.Running() {
			// We only care about getting running VMs, which are only present in
			// running experiments.
			continue
		}

		// TODO: handle error
		vms, _ := vm.List(exp.Spec.ExperimentName())

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
					plog.Error("getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
				}
			}

			allowed = append(allowed, util.VMToProtobuf(exp.Metadata.Name, vm, exp.Spec.Topology()))
		}
	}

	resp := &proto.VMList{Total: uint32(len(allowed)), Vms: allowed}

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /applications
func GetApplications(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetApplications")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("applications", "list") {
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

	w.Write(body)
}

// GET /topologies
func GetTopologies(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetTopologies")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("topologies", "list") {
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

	w.Write(body)
}

// GET /topologies/{topo}/scenarios
func GetScenarios(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetScenarios")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		topo = vars["topo"]
	)

	if !role.Allowed("scenarios", "list") {
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

		for _, t := range topos {
			// We only care about scenarios pertaining to the given topology.
			if t == topo {
				found = true
				break
			}
		}

		if !found {
			continue
		}

		if role.Allowed("scenarios", "list", s.Metadata.Name) {
			apps, err := scenario.AppList(s.Metadata.Name)
			if err != nil {
				plog.Error("getting apps for scenario", "scenario", s.Metadata.Name, "err", err)
				continue
			}

			list := make([]interface{}, len(apps))
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

	w.Write(body)
}

// GET /disks
func GetDisks(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetDisks")

	var (
		ctx     = r.Context()
		role    = ctx.Value("role").(rbac.Role)
		query   = r.URL.Query()
		expName = query.Get("expName")
	)

	if !role.Allowed("disks", "list") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	disks, err := cluster.GetImages(expName, cluster.VM_IMAGE)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	allowed := []string{}
	for _, disk := range disks {
		if role.Allowed("disks", "list", disk.Name) {
			allowed = append(allowed, disk.FullPath)
		}
	}

	sort.Strings(allowed)

	body, err := marshaler.Marshal(&proto.DiskList{Disks: allowed})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /hosts
func GetClusterHosts(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetClusterHosts")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("hosts", "list") {
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

	w.Write(marshalled)
}

// GET /errors/{uuid}
func GetError(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "GetError")

	var (
		uuid  = mux.Vars(r)["uuid"]
		event = store.Event{ID: uuid}
	)

	if err := store.GetEvent(&event); err != nil {
		return weberror.NewWebError(err, "error %s not found", uuid)
	}

	w.Header().Set("Content-Type", "application/json")

	body, _ := json.Marshal(event)
	w.Write(body)

	return nil
}

// POST /console
func CreateConsole(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "CreateConsole")

	if !o.minimegaConsole {
		plog.Error("request made for minimega console, but console not enabled")
		http.Error(w, "'minimega-console' CLI arg not enabled", http.StatusMethodNotAllowed)
		return
	}

	role := r.Context().Value("role").(rbac.Role)
	if !role.Allowed("miniconsole", "post") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// create a new console
	phenix, err := os.Executable()
	if err != nil {
		plog.Error("unable to get full path to phenix")
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	cmd := exec.Command(phenix, "mm", "--attach")

	tty, err := pty.Start(cmd)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not start terminal: %v", err), http.StatusInternalServerError)
		return
	}

	pid := cmd.Process.Pid

	plog.Info("spawned new minimega console", "pid", pid)

	ptyMu.Lock()
	ptys[pid] = tty
	ptyMu.Unlock()

	body, _ := json.Marshal(util.WithRoot("pid", pid))
	w.Write(body)
}

// POST /console/{pid}/size?cols={[0-9]+}&rows={[0-9]+}
func ResizeConsole(w http.ResponseWriter, r *http.Request) {
	role := r.Context().Value("role").(rbac.Role)

	if !role.Allowed("miniconsole", "post") {
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

	plog.Debug("resize console", "pid", pid, "cols", cols, "rows", rows)

	ws := struct {
		R, C, X, Y uint16
	}{
		R: uint16(rows), C: uint16(cols),
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		tty.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)

	if errno != 0 {
		plog.Error("unable to set winsize", "err", syscall.Errno(errno))
		http.Error(w, "set winsize failed", http.StatusInternalServerError)
	}

	// make sure winsize gets processed, hopefully the user isn't typing...
	time.Sleep(100 * time.Millisecond)
	io.WriteString(tty, "\n")
}

// GET /console/{pid}/ws
func WsConsole(w http.ResponseWriter, r *http.Request) {
	role := r.Context().Value("role").(rbac.Role)

	if !role.Allowed("miniconsole", "get") {
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
		defer tty.Close()

		proc, err := os.FindProcess(pid)
		if err != nil {
			plog.Warn("unable to find process", "pid", pid)
			return
		}

		go io.Copy(ws, tty)
		io.Copy(tty, ws)

		plog.Debug("killing minimega console", "pid", pid)

		proc.Kill()
		proc.Wait()

		ptyMu.Lock()
		delete(ptys, pid)
		ptyMu.Unlock()

		plog.Debug("killed minimega console", "pid", pid)

	}).ServeHTTP(w, r)
}

func parseDuration(v string, d *time.Duration) error {
	var err error
	*d, err = time.ParseDuration(v)
	return err
}

func parseInt(v string, d *int) error {
	var err error
	*d, err = strconv.Atoi(v)
	return err
}
