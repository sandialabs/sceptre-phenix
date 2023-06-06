package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
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
	putil "phenix/util"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/web/broker"
	"phenix/web/cache"
	"phenix/web/proto"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/creack/pty"
	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"
	"golang.org/x/sync/errgroup"
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
	log.Debug("GetExperiments HTTP handler called")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		query = r.URL.Query()
		size  = query.Get("screenshot")
	)

	if !role.Allowed("experiments", "list") {
		log.Warn("listing experiments not allowed for %s", ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	experiments, err := experiment.List()
	if err != nil {
		log.Error("getting experiments - %v", err)
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
					log.Error("getting screenshot - %v", err)
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
		log.Error("marshaling experiments - %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /experiments
func CreateExperiment(w http.ResponseWriter, r *http.Request) {
	log.Debug("CreateExperiment HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("experiments", "create") {
		log.Warn("creating experiments not allowed for %s", ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	var req proto.CreateExperimentRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		log.Error("unmashaling request body - %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if err := cache.LockExperimentForCreation(req.Name); err != nil {
		log.Warn(err.Error())
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
	}

	if req.WorkflowBranch != "" {
		annotations := map[string]string{"phenix.workflow/branch": req.WorkflowBranch}
		opts = append(opts, experiment.CreateWithAnnotations(annotations))
	}

	if err := experiment.Create(ctx, opts...); err != nil {
		log.Error("creating experiment %s - %v", req.Name, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if warns := notes.Warnings(ctx, true); warns != nil {
		for _, warn := range warns {
			log.Warn("%v", warn)
		}
	}

	exp, err := experiment.Get(req.Name)
	if err != nil {
		log.Error("getting experiment %s - %v", req.Name, err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	vms, err := vm.List(req.Name)
	if err != nil {
		// TODO
		log.Error("listing VMs in experiment %s - %v", req.Name, err)
	}

	body, err = marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vms))
	if err != nil {
		log.Error("marshaling experiment %s - %v", req.Name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("experiments", "get", req.Name),
		broker.NewResource("experiment", req.Name, "create"),
		body,
	)

	w.WriteHeader(http.StatusNoContent)
}

// PUT /experiments/{name}
func UpdateExperiment(w http.ResponseWriter, r *http.Request) error {
	log.Debug("UpdateExperiment HTTP handler called")

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

	body, err := ioutil.ReadAll(r.Body)
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
	log.Debug("GetExperiment HTTP handler called")

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
					log.Error("getting screenshot: %v", err)
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
	log.Debug("DeleteExperiment HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments", "delete", name) {
		log.Warn("deleting experiment %s not allowed for %s", name, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockExperimentForDeletion(name); err != nil {
		log.Warn(err.Error())
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockExperiment(name)

	if err := experiment.Delete(name); err != nil {
		log.Error("deleting experiment %s - %v", name, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("experiments", "delete", name),
		broker.NewResource("experiment", name, "delete"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{name}/start
func StartExperiment(w http.ResponseWriter, r *http.Request) error {
	log.Debug("StartExperiment HTTP handler called")

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
	log.Debug("StopExperiment HTTP handler called")

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
	log.Debug("TriggerExperimentApps HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
	)

	if !role.Allowed("experiments/trigger", "create", name) {
		log.Warn("triggering experiment %s apps not allowed for %s", name, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("experiments/trigger", "create", name),
		broker.NewResource("experiment/apps", name, "triggered"),
		nil,
	)

	go func() {
		var (
			md   = make(map[string]any)
			apps = strings.Split(appsFilter, ",")
		)

		for k, v := range query {
			md[k] = v
		}

		for _, a := range apps {
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

				broker.Broadcast(
					broker.NewRequestPolicy("experiments/trigger", "create", name),
					broker.NewResource("experiment/apps", name, "triggerError"),
					[]byte(humanized.Humanize()),
				)

				log.Error("triggering experiment %s app %s - %v", name, a, err)
				return
			}
		}

		broker.Broadcast(
			broker.NewRequestPolicy("experiments/trigger", "create", name),
			broker.NewResource("experiment/apps", name, "triggerSuccess"),
			notes.ToJSON(ctx),
		)
	}()

	w.WriteHeader(http.StatusNoContent)
}

// DELETE /experiments/{name}/trigger[?apps=<foo,bar,baz>]
func CancelTriggeredExperimentApps(w http.ResponseWriter, r *http.Request) {
	log.Debug("CancelTriggeredExperimentApps HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
	)

	if !role.Allowed("experiments/trigger", "delete", name) {
		log.Warn("canceling triggered experiment %s apps not allowed for %s", name, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("experiments/trigger", "delete", name),
		broker.NewResource("experiment/apps", name, "cancelTrigger"),
		nil,
	)

	go func() {
		apps := strings.Split(appsFilter, ",")

		for _, a := range apps {
			k := fmt.Sprintf("%s/%s", name, a)

			cancels := cancelers[k]

			for _, cancel := range cancels {
				cancel()
			}

			delete(cancelers, k)
		}

		broker.Broadcast(
			broker.NewRequestPolicy("experiments/trigger", "delete", name),
			broker.NewResource("experiment/apps", name, "cancelTriggerSuccess"),
			notes.ToJSON(ctx),
		)
	}()

	w.WriteHeader(http.StatusNoContent)
}

// GET /experiments/{name}/schedule
func GetExperimentSchedule(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetExperimentSchedule HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/schedule", "get", name) {
		log.Warn("getting experiment schedule for %s not allowed for %s", name, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if status := cache.IsExperimentLocked(name); status != "" {
		msg := fmt.Sprintf("experiment %s is cache.Locked with status %s", name, status)

		log.Warn(msg)
		http.Error(w, msg, http.StatusConflict)

		return
	}

	exp, err := experiment.Get(name)
	if err != nil {
		log.Error("getting experiment %s - %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err := marshaler.Marshal(util.ExperimentScheduleToProtobuf(*exp))
	if err != nil {
		log.Error("marshaling schedule for experiment %s - %v", name, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /experiments/{name}/schedule
func ScheduleExperiment(w http.ResponseWriter, r *http.Request) {
	log.Debug("ScheduleExperiment HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/schedule", "create", name) {
		log.Warn("creating experiment schedule for %s not allowed for %s", name, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if status := cache.IsExperimentLocked(name); status != "" {
		msg := fmt.Sprintf("experiment %s is cache.Locked with status %s", name, status)

		log.Warn(msg)
		http.Error(w, msg, http.StatusConflict)

		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.UpdateScheduleRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		log.Error("unmarshaling request body - %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = experiment.Schedule(experiment.ScheduleForName(name), experiment.ScheduleWithAlgorithm(req.Algorithm))
	if err != nil {
		log.Error("scheduling experiment %s using %s - %v", name, req.Algorithm, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exp, err := experiment.Get(name)
	if err != nil {
		log.Error("getting experiment %s - %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err = marshaler.Marshal(util.ExperimentScheduleToProtobuf(*exp))
	if err != nil {
		log.Error("marshaling schedule for experiment %s - %v", name, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("experiments/schedule", "create", name),
		broker.NewResource("experiment", name, "schedule"),
		body,
	)

	w.Write(body)
}

// GET /experiments/{name}/captures
func GetExperimentCaptures(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetExperimentCaptures HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/captures", "list", name) {
		log.Warn("listing experiment captures for %s not allowed for %s", name, ctx.Value("user").(string))
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
		log.Error("marshaling captures for experiment %s - %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /experiments/{name}/files
func GetExperimentFiles(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetExperimentFiles HTTP handler called")

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
		log.Warn("listing experiment files for %s not allowed for %s", name, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	files, err := experiment.Files(name, clientFilter)
	if err != nil {
		log.Error("getting list of files for experiment %s - %v", name, err)
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
		log.Error("marshaling file list for experiment %s - %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /experiments/{name}/files/{filename}
func GetExperimentFile(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetExperimentFile HTTP handler called")

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
		log.Warn("getting experiment file for %s not allowed for %s", name, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	contents, err := experiment.File(name, path)
	if err != nil {
		if errors.Is(err, mm.ErrCaptureExists) {
			http.Error(w, "capture still in progress", http.StatusBadRequest)
			return
		}

		log.Error("getting file %s for experiment %s - %v", path, name, err)
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
	log.Debug("GetExperimentApps HTTP handler called")

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
	log.Debug("GetVMs HTTP handler called")

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
					log.Error("getting screenshot: %v", err)
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
	log.Debug("GetVM HTTP handler called")

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
			log.Error("getting screenshot: %v", err)
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
	log.Debug("UpdateVM HTTP handler called")
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

	body, err := ioutil.ReadAll(r.Body)
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
		log.Error("updating VM: %v", err)
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
			log.Error("getting screenshot: %v", err)
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
		broker.NewRequestPolicy("vms", "patch", fmt.Sprintf("%s/%s", expName, name)),
		broker.NewResource("experiment/vm", fmt.Sprintf("%s/%s", expName, name), "update"),
		body,
	)

	w.Write(body)
}

// PATCH /experiments/{exp}/vms
func UpdateVMs(w http.ResponseWriter, r *http.Request) {
	log.Debug("UpdateVMs HTTP handler called")
	var (
		ctx     = r.Context()
		role    = ctx.Value("role").(rbac.Role)
		vars    = mux.Vars(r)
		expName = vars["exp"]
	)

	body, err := ioutil.ReadAll(r.Body)
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
			log.Error("%s/%s is forbidden", expName, vmRequest.Name)
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
			log.Error("updating VM: %v", err)
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
				log.Error("getting screenshot: %v", err)
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
	log.Debug("DeleteVM HTTP handler called")

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
		broker.NewRequestPolicy("vms", "delete", fmt.Sprintf("%s/%s", expName, name)),
		broker.NewResource("experiment/vm", fmt.Sprintf("%s/%s", expName, name), "delete"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{exp}/vms/{name}/start
func StartVM(w http.ResponseWriter, r *http.Request) {
	log.Debug("StartVM HTTP handler called")

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
		log.Warn(err.Error())
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		broker.NewRequestPolicy("vms/start", "update", fullName),
		broker.NewResource("experiment/vm", name, "starting"),
		nil,
	)

	if err := mm.StartVM(mm.NS(expName), mm.VMName(name)); err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/start", "update", fullName),
			broker.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/start", "update", fullName),
			broker.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/start", "update", fullName),
			broker.NewResource("experiment/vm", name, "errorStarting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	screenshot, err := util.GetScreenshot(expName, name, "215")
	if err != nil {
		log.Error("getting screenshot - %v", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("vms/start", "update", fullName),
		broker.NewResource("experiment/vm", expName+"/"+name, "start"),
		body,
	)

	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/stop
func StopVM(w http.ResponseWriter, r *http.Request) {
	log.Debug("StopVM HTTP handler called")

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
		log.Warn(err.Error())
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		broker.NewRequestPolicy("vms/stop", "update", fullName),
		broker.NewResource("experiment/vm", name, "stopping"),
		nil,
	)

	if err := mm.StopVM(mm.NS(expName), mm.VMName(name)); err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/stop", "update", fullName),
			broker.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/stop", "update", fullName),
			broker.NewResource("experiment/vm", name, "errorStopping"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/stop", "update", fullName),
			broker.NewResource("experiment/vm", name, "errorStopping"),
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
		broker.NewRequestPolicy("vms/stop", "update", fullName),
		broker.NewResource("experiment/vm", expName+"/"+name, "stop"),
		body,
	)

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/restart
func RestartVM(w http.ResponseWriter, r *http.Request) {
	log.Debug("RestartVM HTTP handler called")

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
		log.Warn(err.Error())
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockVM(expName, name)

	broker.Broadcast(
		broker.NewRequestPolicy("vms/restart", "update", fullName),
		broker.NewResource("experiment/vm", name, "restarting"),
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
		log.Error("getting screenshot - %v", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("vms/restart", "update", fullName),
		broker.NewResource("experiment/vm", expName+"/"+name, "update"),
		body,
	)

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/shutdown
func ShutdownVM(w http.ResponseWriter, r *http.Request) {
	log.Debug("ShutdownVM HTTP handler called")

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
		log.Warn(err.Error())
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
		broker.NewRequestPolicy("vms/shutdown", "update", fullName),
		broker.NewResource("experiment/vm", expName+"/"+name, "shutdown"),
		body,
	)

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/reset
func ResetVM(w http.ResponseWriter, r *http.Request) {
	log.Debug("ResetVM HTTP handler called")

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
		log.Warn(err.Error())
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
		broker.NewRequestPolicy("vms/reset", "update", fullName),
		broker.NewResource("experiment/vm", expName+"/"+name, "reset"),
		body,
	)

	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/redeploy
func RedeployVM(w http.ResponseWriter, r *http.Request) {
	log.Debug("RedeployVM HTTP handler called")

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
		log.Warn(err.Error())
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
		broker.NewRequestPolicy("vms/redeploy", "update", fullName),
		broker.NewResource("experiment/vm", expName+"/"+name, "redeploying"),
		body,
	)

	redeployed := make(chan error)

	go func() {
		defer close(redeployed)

		body, err := ioutil.ReadAll(r.Body)
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
		log.Error("redeploying VM %s - %v", fullName, err)

		broker.Broadcast(
			broker.NewRequestPolicy("vms/redeploy", "update", fullName),
			broker.NewResource("experiment/vm", expName+"/"+name, "errorRedeploying"),
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
		log.Error("getting screenshot - %v", err)
	} else {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
	}

	body, _ = marshaler.Marshal(util.VMToProtobuf(expName, *v, exp.Spec.Topology()))

	broker.Broadcast(
		broker.NewRequestPolicy("vms/redeploy", "update", fullName),
		broker.NewResource("experiment/vm", expName+"/"+name, "redeployed"),
		body,
	)

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/screenshot.png
func GetScreenshot(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetScreenshot HTTP handler called")

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
	log.Debug("GetVMCaptures HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "list", fmt.Sprintf("%s/%s", exp, name)) {
		log.Warn("getting captures for VM %s in experiment %s not allowed for %s", name, exp, ctx.Value("user").(string))
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
	log.Debug("StartVMCapture HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)) {
		log.Warn("starting capture for VM %s in experiment %s not allowed for %s", name, exp, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.StartCaptureRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		log.Error("unmarshaling request body - %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := vm.StartCapture(exp, name, int(req.Interface), req.Filename); err != nil {
		log.Error("starting VM capture for VM %s in experiment %s - %v", name, exp, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("vms/captures", "create", fmt.Sprintf("%s/%s", exp, name)),
		broker.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "start"),
		body,
	)

	w.WriteHeader(http.StatusNoContent)
}

// DELETE /experiments/{exp}/vms/{name}/captures
func StopVMCaptures(w http.ResponseWriter, r *http.Request) {
	log.Debug("StopVMCaptures HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)) {
		log.Warn("stopping capture for VM %s in experiment %s not allowed for %s", name, exp, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := vm.StopCaptures(exp, name); err != nil {
		log.Error("stopping VM capture for VM %s in experiment %s - %v", name, exp, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("vms/captures", "delete", fmt.Sprintf("%s/%s", exp, name)),
		broker.NewResource("experiment/vm/capture", fmt.Sprintf("%s/%s", exp, name), "stop"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{exp}/captureSubnet
func StartCaptureSubnet(w http.ResponseWriter, r *http.Request) {
	log.Debug("StartCaptureSubnet HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
	)

	if !role.Allowed("exp/captureSubnet", "create", exp) {
		log.Warn("starting subnet capture for experiment %s is not allowed for %s", exp, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.CaptureSubnetRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		log.Error("unmarshaling request body - %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Info("Exp:%v Subnet:%v VMS:%v", exp, req.Subnet, req.Vms)
	vmCaptures, err := vm.CaptureSubnet(exp, req.Subnet, req.Vms)

	if err != nil {
		log.Error("Unable to start packet subnet capture - %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err = marshaler.Marshal(&proto.CaptureList{Captures: util.CapturesToProtobuf(vmCaptures)})
	if err != nil {
		log.Error("Unable to marshal vm capture list %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /experiments/{exp}/stopCaptureSubnet
func StopCaptureSubnet(w http.ResponseWriter, r *http.Request) {
	log.Debug("StartCaptureSubnet HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
	)

	if !role.Allowed("exp/captureSubnet", "create", exp) {
		log.Warn("starting subnet capture for experiment %s is not allowed for %s", exp, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.CaptureSubnetRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		log.Error("unmarshaling request body - %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Info("Exp:%v Subnet:%v VMS:%v", exp, req.Subnet, req.Vms)

	if err != nil {
		log.Error("Unable to stop packet subnet capture - %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	vms, _ := vm.StopCaptureSubnet(exp, req.Subnet, req.Vms)

	body, err = marshaler.Marshal(&proto.VMNameList{Vms: vms})
	if err != nil {
		log.Error("Unable to marshal vm capture list %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// GET /experiments/{exp}/vms/{name}/snapshots
func GetVMSnapshots(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetVMSnapshots HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/snapshots", "list", fmt.Sprintf("%s/%s", exp, name)) {
		log.Warn("listing snapshots for VM %s in experiment %s not allowed for %s", name, exp, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	snapshots, err := vm.Snapshots(exp, name)
	if err != nil {
		log.Error("getting list of snapshots for VM %s in experiment %s: %v", name, exp, err)
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
	log.Debug("SnapshotVM HTTP handler called")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/snapshots", "create", fullName) {
		log.Warn("snapshotting VM %s in experiment %s not allowed for %s", name, exp, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.SnapshotRequest
	err = unmarshaler.Unmarshal(body, &req)
	if err != nil {
		log.Error("unmarshaling request body - %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := cache.LockVMForSnapshotting(exp, name); err != nil {
		log.Warn(err.Error())
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockVM(exp, name)

	broker.Broadcast(
		broker.NewRequestPolicy("vms/snapshots", "create", fullName),
		broker.NewResource("experiment/vm/snapshot", exp+"/"+name, "creating"),
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
				log.Info("snapshot percent complete: %v", progress)

				status := map[string]interface{}{
					"percent": progress / 100,
				}

				marshalled, _ := json.Marshal(status)

				broker.Broadcast(
					broker.NewRequestPolicy("vms/snapshots", "create", fullName),
					broker.NewResource("experiment/vm/snapshot", exp+"/"+name, "progress"),
					marshalled,
				)
			}
		}
	}()

	cb := func(s string) { status <- s }

	if err := vm.Snapshot(exp, name, req.Filename, cb); err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/snapshots", "create", fullName),
			broker.NewResource("experiment/vm/snapshot", exp+"/"+name, "errorCreating"),
			nil,
		)

		log.Error("snapshotting VM %s in experiment %s - %v", name, exp, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("vms/snapshots", "create", fullName),
		broker.NewResource("experiment/vm/snapshot", exp+"/"+name, "create"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{exp}/vms/{name}/snapshots/{snapshot}
func RestoreVM(w http.ResponseWriter, r *http.Request) {
	log.Debug("RestoreVM HTTP handler called")

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
		log.Warn("restoring VM %s in experiment %s not allowed for %s", name, exp, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := cache.LockVMForRestoring(exp, name); err != nil {
		log.Warn(err.Error())
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	defer cache.UnlockVM(exp, name)

	broker.Broadcast(
		broker.NewRequestPolicy("vms/snapshots", "create", fullName),
		broker.NewResource("experiment/vm/snapshot", fmt.Sprintf("%s/%s", exp, name), "restoring"),
		nil,
	)

	if err := vm.Restore(exp, name, snap); err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/snapshots", "create", fullName),
			broker.NewResource("experiment/vm/snapshot", fmt.Sprintf("%s/%s", exp, name), "errorRestoring"),
			nil,
		)

		log.Error("restoring VM %s in experiment %s - %v", name, exp, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("vms/snapshots", "create", fullName),
		broker.NewResource("experiment/vm/snapshot", exp+"/"+name, "restore"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /experiments/{exp}/vms/{name}/commit
func CommitVM(w http.ResponseWriter, r *http.Request) {
	log.Debug("CommitVM HTTP handler called")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		expName  = vars["exp"]
		name     = vars["name"]
		fullName = expName + "/" + name
	)

	if !role.Allowed("vms/commit", "create", fullName) {
		log.Warn("committing VM %s in experiment %s not allowed for %s", name, expName, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
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
			log.Error("unmarshaling request body - %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Filename == "" {
			log.Error("missing filename for commit")
			http.Error(w, "missing 'filename' key", http.StatusBadRequest)
			return
		}

		filename = req.Filename
	}

	if err := cache.LockVMForCommitting(expName, name); err != nil {
		log.Warn(err.Error())
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
		broker.NewRequestPolicy("vms/commit", "create", fullName),
		broker.NewResource("experiment/vm/commit", expName+"/"+name, "committing"),
		body,
	)

	status := make(chan float64)

	go func() {
		for s := range status {
			log.Info("VM commit percent complete: %v", s)

			status := map[string]interface{}{
				"percent": s,
			}

			marshalled, _ := json.Marshal(status)

			broker.Broadcast(
				broker.NewRequestPolicy("vms/commit", "create", fullName),
				broker.NewResource("experiment/vm/commit", expName+"/"+name, "progress"),
				marshalled,
			)
		}
	}()

	cb := func(s float64) { status <- s }

	if _, err = vm.CommitToDisk(expName, name, filename, cb); err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/commit", "create", fullName),
			broker.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		log.Error("committing VM %s in experiment %s - %v", name, expName, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/commit", "create", fullName),
			broker.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	v, err := vm.Get(expName, name)
	if err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/commit", "create", fullName),
			broker.NewResource("experiment/vm/commit", expName+"/"+name, "errorCommitting"),
			nil,
		)

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	payload.Vm = util.VMToProtobuf(expName, *v, exp.Spec.Topology())
	body, _ = marshaler.Marshal(payload)

	broker.Broadcast(
		broker.NewRequestPolicy("vms/commit", "create", fmt.Sprintf("%s/%s", expName, name)),
		broker.NewResource("experiment/vm/commit", expName+"/"+name, "commit"),
		body,
	)

	w.Write(body)
}

// POST /experiments/{exp}/vms/{name}/memorySnapshot
func CreateVMMemorySnapshot(w http.ResponseWriter, r *http.Request) {
	log.Debug("CreateVMMemorySnapshot HTTP handler called")

	var (
		ctx      = r.Context()
		role     = ctx.Value("role").(rbac.Role)
		vars     = mux.Vars(r)
		exp      = vars["exp"]
		name     = vars["name"]
		fullName = exp + "/" + name
	)

	if !role.Allowed("vms/memorySnapshot", "create", fullName) {
		log.Warn("Capturing memory snapshot of VM %s in experiment %s not allowed for %s", name, exp, ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
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
			log.Error("unmarshaling request body - %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Filename == "" {
			log.Error("missing filename for memory snapshot")
			http.Error(w, "missing 'filename' key", http.StatusBadRequest)
			return
		}

		filename = req.Filename
	}

	if err := cache.LockVMForMemorySnapshotting(exp, name); err != nil {
		log.Warn(err.Error())
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
		broker.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
		broker.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "committing"),
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

				log.Info("%s/%s memory snapshot percent complete: %v", exp, name, progress)

				marshalled, _ := json.Marshal(status)

				broker.Broadcast(
					broker.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
					broker.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "progress"),
					marshalled,
				)
			}
		}
	}()

	cb := func(s string) { status <- s }

	if _, err = vm.MemorySnapshot(exp, name, filename, cb); err != nil {
		broker.Broadcast(
			broker.NewRequestPolicy("vms/memorySnapshot", "create", fullName),
			broker.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "errorCommitting"),
			nil,
		)

		log.Error("memory snapshot for VM %s in experiment %s - %v", name, exp, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("vms/memorySnapshot", "create", fmt.Sprintf("%s/%s", exp, name)),
		broker.NewResource("experiment/vm/memorySnapshot", exp+"/"+name, "commit"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// GET /vms
func GetAllVMs(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetAllVMs HTTP handler called")

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
		log.Error("getting experiments: %v", err)
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
					log.Error("getting screenshot: %v", err)
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
	log.Debug("GetApplications HTTP handler called")

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
	log.Debug("GetTopologies HTTP handler called")

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
	log.Debug("GetScenarios HTTP handler called")

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
				log.Error("getting apps for scenario %s: %v", s.Metadata.Name, err)
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
	log.Debug("GetDisks HTTP handler called")

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
	log.Debug("GetClusterHosts HTTP handler called")

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

// GET /logs
func GetLogs(w http.ResponseWriter, r *http.Request) {
	if !o.publishLogs {
		w.WriteHeader(http.StatusNotImplemented)
	}

	type LogLine struct {
		Source    string `json:"source"`
		Timestamp string `json:"timestamp"`
		Epoch     int64  `json:"epoch"`
		Level     string `json:"level"`
		Log       string `json:"log"`

		// Not exported so it doesn't get included in serialized JSON.
		ts time.Time
	}

	var (
		since time.Duration
		limit int

		logs    = make(map[int][]LogLine)
		logChan = make(chan LogLine)
		done    = make(chan struct{})
		wait    errgroup.Group

		logFiles = map[string]string{
			"minimega": o.minimegaLogs,
			"phenix":   o.phenixLogs,
		}
	)

	// If no since duration is provided, or the value provided is not a
	// valid duration string, since will default to 1h.
	if err := parseDuration(r.URL.Query().Get("since"), &since); err != nil {
		since = 1 * time.Hour
	}

	// If no limit is provided, or the value provided is not an int, limit
	// will default to 0.
	parseInt(r.URL.Query().Get("limit"), &limit)

	go func() {
		for l := range logChan {
			ts := int(l.ts.Unix())

			tl := logs[ts]
			tl = append(tl, l)

			logs[ts] = tl
		}

		close(done)
	}()

	for k := range logFiles {
		name := k
		path := logFiles[k]

		wait.Go(func() error {
			f, err := os.Open(path)
			if err != nil {
				// This *most likely* means the log file doesn't exist yet, so just exit
				// out of the Goroutine cleanly.
				return nil
			}

			defer f.Close()

			var (
				scanner = bufio.NewScanner(f)
				// Used to detect multi-line logs in tailed log files.
				body *LogLine
			)

			for scanner.Scan() {
				parts := logLineRegex.FindStringSubmatch(scanner.Text())

				if len(parts) == 4 {
					ts, err := time.ParseInLocation("2006/01/02 15:04:05", parts[1], time.Local)
					if err != nil {
						continue
					}

					if time.Since(ts) > since {
						continue
					}

					if parts[2] == "WARNING" {
						parts[2] = "WARN"
					}

					body = &LogLine{
						Source:    name,
						Timestamp: parts[1],
						Epoch:     ts.Unix(),
						Level:     parts[2],
						Log:       parts[3],

						ts: ts,
					}
				} else if body != nil {
					body.Log = scanner.Text()
				} else {
					continue
				}

				logChan <- *body
			}

			if err := scanner.Err(); err != nil {
				return fmt.Errorf("scanning %s log file at %s: %w", name, path, err)
			}

			return nil
		})
	}

	if err := wait.Wait(); err != nil {
		http.Error(w, "error reading logs", http.StatusInternalServerError)
		return
	}

	// Close log channel, marking it as done.
	close(logChan)
	// Wait for Goroutine processing logs from log channel to be done.
	<-done

	var (
		idx, offset int
		ts          = make([]int, len(logs))
		limited     []LogLine
	)

	// Put log timestamps into slice so they can be sorted.
	for k := range logs {
		ts[idx] = k
		idx++
	}

	// Sort log timestamps.
	sort.Ints(ts)

	// Determine if final log slice should be limited.
	if limit != 0 && limit < len(ts) {
		offset = len(ts) - limit
	}

	// Loop through sorted, limited log timestamps and grab actual logs
	// for given timestamp.
	for _, k := range ts[offset:] {
		limited = append(limited, logs[k]...)
	}

	marshalled, _ := json.Marshal(util.WithRoot("logs", limited))
	w.Write(marshalled)
}

// GET /users
func GetUsers(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetUsers HTTP handler called")

	var (
		ctx   = r.Context()
		uname = ctx.Value("user").(string)
		role  = ctx.Value("role").(rbac.Role)
	)

	var resp []*proto.User

	if role.Allowed("users", "list") {
		users, err := rbac.GetUsers()
		if err != nil {
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		for _, u := range users {
			if role.Allowed("users", "list", u.Username()) {
				resp = append(resp, util.UserToProtobuf(*u))
			}
		}
	} else if role.Allowed("users", "get", uname) {
		user, err := rbac.GetUser(uname)
		if err != nil {
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		resp = append(resp, util.UserToProtobuf(*user))
	} else {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := marshaler.Marshal(&proto.UserList{Users: resp})
	if err != nil {
		log.Error("marshaling users: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// POST /users
func CreateUser(w http.ResponseWriter, r *http.Request) {
	log.Debug("CreateUser HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("users", "create") {
		log.Warn("creating users not allowed for %s", ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	var req proto.CreateUserRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		log.Error("unmashaling request body - %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	user := rbac.NewUser(req.GetUsername(), req.GetPassword())

	user.Spec.FirstName = req.GetFirstName()
	user.Spec.LastName = req.GetLastName()

	uRole, err := rbac.RoleFromConfig(req.GetRoleName())
	if err != nil {
		log.Error("role name not found - %s", req.GetRoleName())
		http.Error(w, "role not found", http.StatusBadRequest)
		return
	}

	uRole.SetResourceNames(req.GetResourceNames()...)

	// allow user to get and update their own user details
	uRole.AddPolicy(
		[]string{"users"},
		[]string{req.GetUsername()},
		[]string{"get", "patch"},
	)

	user.SetRole(uRole)

	resp := util.UserToProtobuf(*user)

	body, err = marshaler.Marshal(resp)
	if err != nil {
		log.Error("marshaling user %s: %v", user.Username(), err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("users", "create", ""),
		broker.NewResource("user", req.GetUsername(), "create"),
		body,
	)

	w.Write(body)
}

// GET /users/{username}
func GetUser(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetUser HTTP handler called")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		vars  = mux.Vars(r)
		uname = vars["username"]
	)

	if !role.Allowed("users", "get", uname) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	user, err := rbac.GetUser(uname)
	if err != nil {
		http.Error(w, "unable to get user", http.StatusInternalServerError)
		return
	}

	resp := util.UserToProtobuf(*user)

	body, err := marshaler.Marshal(resp)
	if err != nil {
		log.Error("marshaling user %s: %v", user.Username(), err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// PATCH /users/{username}
func UpdateUser(w http.ResponseWriter, r *http.Request) {
	log.Debug("UpdateUser HTTP handler called")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		vars  = mux.Vars(r)
		uname = vars["username"]
	)

	if !role.Allowed("users", "patch", uname) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req proto.UpdateUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	u, err := rbac.GetUser(uname)
	if err != nil {
		http.Error(w, "unable to get user", http.StatusInternalServerError)
		return
	}

	if req.FirstName != "" {
		if err := u.UpdateFirstName(req.FirstName); err != nil {
			log.Error("updating first name for user %s: %v", uname, err)
			http.Error(w, "unable to update user", http.StatusInternalServerError)
			return
		}
	}

	if req.LastName != "" {
		if err := u.UpdateLastName(req.LastName); err != nil {
			log.Error("updating last name for user %s: %v", uname, err)
			http.Error(w, "unable to update user", http.StatusInternalServerError)
			return
		}
	}

	if req.RoleName != "" && role.Allowed("users/roles", "patch", uname) {
		uRole, err := rbac.RoleFromConfig(req.GetRoleName())
		if err != nil {
			log.Error("role name not found - %s", req.GetRoleName())
			http.Error(w, "role not found", http.StatusBadRequest)
			return
		}

		uRole.SetResourceNames(req.GetResourceNames()...)

		// allow user to get their own user details
		uRole.AddPolicy(
			[]string{"users"},
			[]string{uname},
			[]string{"get", "patch"},
		)

		u.SetRole(uRole)
	}

	if req.NewPassword != "" {
		if req.Password == "" {
			log.Error("new password provided without old password for user %s", uname)
			http.Error(w, "cannot change password without password", http.StatusBadRequest)
			return
		}

		if err := u.UpdatePassword(req.Password, req.NewPassword); err != nil {
			log.Error("updating password for user %s: %v", uname, err)
			http.Error(w, "unable to update password", http.StatusBadRequest)
			return
		}
	}

	resp := util.UserToProtobuf(*u)

	body, err = marshaler.Marshal(resp)
	if err != nil {
		log.Error("marshaling user %s: %v", uname, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("users", "patch", uname),
		broker.NewResource("user", uname, "update"),
		body,
	)

	w.Write(body)
}

// DELETE /users/{username}
func DeleteUser(w http.ResponseWriter, r *http.Request) {
	log.Debug("DeleteUser HTTP handler called")

	var (
		ctx   = r.Context()
		user  = ctx.Value("user").(string)
		role  = ctx.Value("role").(rbac.Role)
		vars  = mux.Vars(r)
		uname = vars["username"]
	)

	if user == uname {
		http.Error(w, "you cannot delete your own user", http.StatusForbidden)
		return
	}

	if !role.Allowed("users", "delete", uname) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := config.Delete("user/" + uname); err != nil {
		log.Error("deleting user %s: %v", uname, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broker.Broadcast(
		broker.NewRequestPolicy("users", "delete", uname),
		broker.NewResource("user", uname, "delete"),
		nil,
	)

	w.WriteHeader(http.StatusNoContent)
}

// POST /users/{username}/tokens
func CreateUserToken(w http.ResponseWriter, r *http.Request) {
	log.Debug("CreateUserToken HTTP handler called")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		vars  = mux.Vars(r)
		uname = vars["username"]
	)

	if !role.Allowed("users", "patch", uname) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	u, err := rbac.GetUser(uname)
	if err != nil {
		http.Error(w, "unable to get user", http.StatusInternalServerError)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var data map[string]string

	if err := json.Unmarshal(body, &data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dur, err := time.ParseDuration(data["lifetime"])
	if err != nil {
		http.Error(w, "invalid token lifetime provided", http.StatusBadRequest)
		return
	}

	exp := time.Now().Add(dur)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": u.Username(),
		"exp": exp.Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		http.Error(w, "failed to sign JWT", http.StatusInternalServerError)
		return
	}

	note := fmt.Sprintf("manually generated - %s", time.Now().Format(time.RFC3339))
	if desc := data["desc"]; desc != "" {
		note = data["desc"]
	}

	if err := u.AddToken(signed, note); err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	resp := map[string]string{
		"token": signed,
		"desc":  note,
		"exp":   exp.Format(time.RFC3339),
	}

	body, _ = json.Marshal(resp)
	w.Write(body)
}

// GET /roles
func GetRoles(w http.ResponseWriter, r *http.Request) {
	log.Debug("ListRoles HTTP handler called")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("roles", "list") {
		http.Error(w, "forbidden to list roles", http.StatusForbidden)
		return
	}

	var resp []*proto.Role

	roles, err := rbac.GetRoles()
	for _, r := range roles {
		resp = append(resp, util.RoleToProtobuf(*r))
	}
	if err != nil {
		log.Error("error retrieving roles - %v", err)
		http.Error(w, "Error retrieving roles", http.StatusInternalServerError)
		return
	}


	body, err := marshaler.Marshal(&proto.RoleList{Roles: resp})
	if err != nil {
		log.Error("marshaling roles: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

func Signup(w http.ResponseWriter, r *http.Request) {
	log.Debug("Signup HTTP handler called")

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Error("reading request body - %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	var req proto.SignupUserRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		log.Error("unmashaling request body - %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if o.proxyAuthHeader != "" {
		if user := r.Header.Get(o.proxyAuthHeader); user != req.GetUsername() {
			http.Error(w, "proxy user mismatch", http.StatusUnauthorized)
			return
		}
	}

	u := rbac.NewUser(req.GetUsername(), req.GetPassword())

	u.Spec.FirstName = req.GetFirstName()
	u.Spec.LastName = req.GetLastName()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": u.Username(),
		"exp": time.Now().Add(o.jwtLifetime).Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		http.Error(w, "failed to sign JWT", http.StatusInternalServerError)
		return
	}

	if err := u.AddToken(signed, time.Now().Format(time.RFC3339)); err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	resp := &proto.LoginResponse{
		User:  util.UserToProtobuf(*u),
		Token: signed,
	}

	body, err = marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

func Login(w http.ResponseWriter, r *http.Request) {
	log.Debug("Login HTTP handler called")

	var (
		user, pass string
		proxied    bool
	)

	switch r.Method {
	case "GET":
		if o.proxyAuthHeader == "" {
			query := r.URL.Query()

			user = query.Get("user")
			if user == "" {
				http.Error(w, "no username provided", http.StatusBadRequest)
				return
			}

			pass = query.Get("pass")
			if pass == "" {
				http.Error(w, "no password provided", http.StatusBadRequest)
				return
			}
		} else {
			user = r.Header.Get(o.proxyAuthHeader)

			if user == "" {
				http.Error(w, "proxy authentication failed", http.StatusUnauthorized)
				return
			}

			proxied = true
		}
	case "POST":
		if o.proxyAuthHeader != "" {
			http.Error(w, "proxy auth enabled -- must login via GET request", http.StatusBadRequest)
			return
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "no data provided in POST", http.StatusBadRequest)
			return
		}

		var req proto.LoginRequest
		if err := unmarshaler.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid data provided in POST", http.StatusBadRequest)
			return
		}

		if user = req.User; user == "" {
			http.Error(w, "invalid username provided in POST", http.StatusBadRequest)
			return
		}

		if pass = req.Pass; pass == "" {
			http.Error(w, "invalid password provided in POST", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "invalid method", http.StatusBadRequest)
		return
	}

	u, err := rbac.GetUser(user)
	if err != nil {
		http.Error(w, user, http.StatusNotFound)
		return
	}

	if !proxied {
		if err := u.ValidatePassword(pass); err != nil {
			http.Error(w, "invalid creds", http.StatusUnauthorized)
			return
		}
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": u.Username(),
		"exp": time.Now().Add(o.jwtLifetime).Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		http.Error(w, "failed to sign JWT", http.StatusInternalServerError)
		return
	}

	if err := u.AddToken(signed, time.Now().Format(time.RFC3339)); err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	resp := &proto.LoginResponse{
		User:  util.UserToProtobuf(*u),
		Token: signed,
	}

	body, err := marshaler.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

func Logout(w http.ResponseWriter, r *http.Request) {
	log.Debug("Logout HTTP handler called")

	var (
		ctx   = r.Context()
		user  = ctx.Value("user").(string)
		token = ctx.Value("jwt").(string)
	)

	u, err := rbac.GetUser(user)
	if err != nil {
		http.Error(w, "cannot find user", http.StatusBadRequest)
		return
	}

	if err := u.DeleteToken(token); err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /errors/{uuid}
func GetError(w http.ResponseWriter, r *http.Request) error {
	log.Debug("GetError HTTP handler called")

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
	if !o.minimegaConsole {
		log.Error("request made for minimega console, but console not enabled")
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
		log.Error("unable to get full path to phenix")
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

	log.Info("spawned new minimega console, pid = %v", pid)

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

	log.Debug("resize console %v to %d x %d", pid, cols, rows)

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
		log.Error("unable to set winsize: %v", syscall.Errno(errno))
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
			log.Error("unable to find process: %v", pid)
			return
		}

		go io.Copy(ws, tty)
		io.Copy(tty, ws)

		log.Debug("Killing minimega console: %v", pid)

		proc.Kill()
		proc.Wait()

		ptyMu.Lock()
		delete(ptys, pid)
		ptyMu.Unlock()

		log.Debug("Finished killing minimega console: %v", pid)

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
