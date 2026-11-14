package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/app"
	"phenix/types"
	putil "phenix/util"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// TriggerExperimentApps - POST /experiments/{name}/trigger[?apps=<foo,bar,baz>][&stage=<stage>].
//
// The stage query parameter names the app lifecycle stage to run, running when it is absent. The name all triggers
// the stage in every experiment in a background job (see triggerAllExperiments).
func TriggerExperimentApps(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
		stageParam = query.Get("stage")
	)

	if name == experiment.ReservedName {
		return triggerAllExperiments(w, r, stageParam, query)
	}

	triggered := audit.Fields{
		Experiment: name, Extra: []any{"phenix.app.names", appsFilter, "phenix.app.stage", cmp.Or(stageParam, string(app.ActionRunning))},
	}

	if !role.Allowed("experiments/trigger", "create", name) {
		audit.Denied(r, audit.AppTrigger, "triggering experiment apps not allowed", triggered)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	stage, werr := triggerStage(query)
	if werr != nil {
		return werr
	}

	triggered.Extra = []any{"phenix.app.names", appsFilter, "phenix.app.stage", string(stage)}

	apps, werr := triggerApps(appsFilter)
	if werr != nil {
		return werr
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.New(storeStatus(err), "unable to get experiment "+name)
	}

	// without a stage, the trigger reports these errors as it runs, as it always did
	if stageParam != "" {
		if werr := triggerStageError(exp, stage, apps); werr != nil {
			return werr
		}
	}

	go func() { _ = runTriggeredApps(context.Background(), name, stage, apps, query) }()

	audit.Success(r, audit.AppTrigger, "experiment apps triggered", triggered)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// runTriggeredApps runs the stage of each of the apps of the experiment name, one after another, until one fails, and
// publishes the progress of each. It passes each query parameter to the apps as metadata. Each app runs in a child of
// parent, which DELETE /experiments/{name}/trigger can cancel too. It returns the error of the app that failed.
func runTriggeredApps(parent context.Context, name string, stage app.Action, apps []string, query url.Values) error {
	md := make(map[string]any)

	for k, v := range query {
		md[k] = v
	}

	for _, a := range apps {
		pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
			Experiment: name, App: a, Stage: string(stage), State: "start",
		})

		k := fmt.Sprintf("%s/%s", name, a)

		// not the HTTP request's context, which ends with the response
		ctx, cancel := context.WithCancel(parent)
		ctx = app.SetContextTriggerUI(ctx)
		ctx = app.SetContextMetadata(ctx, md)

		registered := addCanceler(k, cancel)

		err := experiment.Trigger(ctx, name, stage, a)
		if err != nil {
			dropCanceler(k, registered)

			humanized := putil.HumanizeError(
				err,
				"Unable to trigger %s stage for %s app in %s experiment",
				stage,
				a,
				name,
			)
			pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
				Experiment: name, App: a, Stage: string(stage), State: "error", Error: humanized,
			})

			plog.Error(
				plog.TypeSystem,
				"triggering experiment app",
				"exp",
				name,
				"app",
				a,
				"stage",
				stage,
				"err",
				err,
			)

			return humanized
		}

		dropCanceler(k, registered)

		pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
			Experiment: name, App: a, Stage: string(stage), State: "success",
		})
	}

	return nil
}

// CancelTriggeredExperimentApps - DELETE /experiments/{name}/trigger[?apps=<foo,bar,baz>].
//
// Without the apps query parameter, it cancels every app that a trigger runs for the experiment, and ends the job
// that runs the apps of the experiment on their schedule (see startPeriodicJob).
func CancelTriggeredExperimentApps(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
	)

	canceled := audit.Fields{Experiment: name, Extra: []any{"phenix.app.names", appsFilter}}

	if !role.Allowed("experiments/trigger", "delete", name) {
		audit.Denied(r, audit.AppTriggerCancel, "canceling triggered experiment apps not allowed", canceled)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	apps := triggeredApps(name)

	if query.Has("apps") {
		var werr *weberror.WebError

		if apps, werr = triggerApps(appsFilter); werr != nil {
			return werr
		}
	}

	if !query.Has("apps") {
		cancelPeriodicJob(name)
	}

	go func() {
		for _, a := range apps {
			k := fmt.Sprintf("%s/%s", name, a)

			commonMu.Lock()
			cancels := cancelers[k]
			delete(cancelers, k)
			commonMu.Unlock()

			for _, c := range cancels {
				c.cancel()
			}

			pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
				Experiment: name, Verb: "delete", App: a, State: app.TriggerStateCanceled,
			})
		}
	}()

	audit.Success(r, audit.AppTriggerCancel, "experiment apps trigger canceled", canceled)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// triggerStage returns the lifecycle stage that the stage query parameter of
// query names, with app.ParseAction: the running stage when it is absent, or a
// 400 for an empty value or text that names no stage.
func triggerStage(query url.Values) (app.Action, *weberror.WebError) {
	if !query.Has("stage") {
		return app.ActionRunning, nil
	}

	stage, err := app.ParseAction(query.Get("stage"))
	if err != nil {
		return "", weberror.New(http.StatusBadRequest, "stage: "+err.Error())
	}

	return stage, nil
}

// triggerStageError is the error of a trigger of stage for apps of the experiment exp, which experiment.Trigger would
// refuse: 409 for the running stage of an experiment that does not run, 400 for an app that the experiment does not
// have or that the stage does not apply to. It is nil when Trigger can run the stage.
func triggerStageError(exp *types.Experiment, stage app.Action, apps []string) *weberror.WebError {
	if stage == app.ActionRunning && !exp.Running() {
		return weberror.New(http.StatusConflict, "the running stage needs a running experiment")
	}

	known := app.ExperimentApps(exp)

	for _, a := range apps {
		if !slices.Contains(known, a) {
			return weberror.New(http.StatusBadRequest, fmt.Sprintf("app %s is not part of experiment %s", a, exp.Metadata.Name))
		}

		if !app.StageApplicable(stage, a) {
			return weberror.New(http.StatusBadRequest, fmt.Sprintf("the %s stage is not applicable for app %s", stage, a))
		}
	}

	return nil
}

// triggeredApps are the names of the apps that a trigger runs for the experiment name.
func triggeredApps(name string) []string {
	commonMu.Lock()
	defer commonMu.Unlock()

	var apps []string

	for key := range cancelers {
		if a, ok := strings.CutPrefix(key, name+"/"); ok {
			apps = append(apps, a)
		}
	}

	slices.Sort(apps)

	return apps
}

// triggerApps are the app names of the apps query parameter, a comma-separated
// list, or a 400 error when the list is empty or has an empty name.
func triggerApps(filter string) ([]string, *weberror.WebError) {
	apps := strings.Split(filter, ",")
	if slices.Contains(apps, "") {
		return nil, weberror.New(http.StatusBadRequest, "apps must name one or more apps, separated by commas")
	}

	return apps, nil
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
		return weberror.NewWebError(err, "unable to get experiment %s from store", name).SetStatus(storeStatus(err))
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

// GetExperimentAppsInput - GET /experiments/{name}/apps/input. It answers the
// JSON that phenix gives a user app on stdin, as `phenix util app-json` does.
// It needs experiments get, configs get on the experiment's config, and vms
// list on each of its VMs.
func GetExperimentAppsInput(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		name = mux.Vars(r)["name"]
	)

	// the input holds the whole stored config, scenario app metadata included
	if !role.Allowed("experiments", "get", name) || !role.Allowed("configs", "get", "Experiment/"+name) ||
		!role.Allowed("vms", "list", name+"/*") {
		return appInputDenied(r, name)
	}

	exp, err := experiment.AppInput(ctx, name)
	if requestCanceled(r, err) {
		return nil
	}

	if errors.Is(err, experiment.ErrAppRuntime) {
		plog.Error(plog.TypeSystem, "reading the app input of experiment", "exp", name, "err", err)

		// a partial list of hosts or VMs would read as VMs missing
		return weberror.New(http.StatusBadGateway, "could not read the experiment's hosts and VMs from minimega")
	}

	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name).SetStatus(storeStatus(err))
	}

	// a pattern such as "exp/?" or a negated name can allow "<exp>/*" and not every VM
	for _, vm := range appInputVMNames(exp) {
		if !role.Allowed("vms", "list", name+"/"+vm) {
			return appInputDenied(r, name)
		}
	}

	body, err := json.Marshal(exp)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// appInputDenied logs, and returns the 403 for, a request for the app input
// of the experiment name.
func appInputDenied(r *http.Request, name string) error {
	plog.Warn(plog.TypeSecurity, "getting experiment app input not allowed", "user", middleware.UserFromContext(r.Context()), "exp", name)

	return weberror.New(http.StatusForbidden, "getting the app input of experiment "+name+" not allowed")
}

// appInputVMNames returns the names of the experiment's topology nodes and of
// the VMs minimega reports for it.
func appInputVMNames(exp *types.Experiment) []string {
	var names []string

	if topology := exp.Spec.Topology(); topology != nil {
		for _, node := range topology.Nodes() {
			names = append(names, node.General().Hostname())
		}
	}

	for _, vm := range exp.VMs {
		names = append(names, vm.Name)
	}

	return names
}
