package web

import (
	"context"
	"encoding/json"
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
// The stage query parameter names the app lifecycle stage to run, running when it is absent.
func TriggerExperimentApps(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
		stageParam = query.Get("stage")
		stage      = app.ActionRunning
	)

	if stageParam != "" {
		stage = app.Action(stageParam)
	}

	triggered := audit.Fields{
		Experiment: name, Extra: []any{"phenix.app.names", appsFilter, "phenix.app.stage", string(stage)},
	}

	if !role.Allowed("experiments/trigger", "create", name) {
		audit.Denied(r, audit.AppTrigger, "triggering experiment apps not allowed", triggered)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if !stage.Valid() {
		return weberror.New(http.StatusBadRequest, fmt.Sprintf(
			"stage must be one of configure, pre-start, post-start, running or cleanup, not %q", stageParam,
		))
	}

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

	go runTriggeredApps(name, stage, apps, query)

	audit.Success(r, audit.AppTrigger, "experiment apps triggered", triggered)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// runTriggeredApps runs the stage of each of the apps of the experiment name, one after another, until one fails, and
// publishes the progress of each. It passes each query parameter to the apps as metadata.
func runTriggeredApps(name string, stage app.Action, apps []string, query url.Values) {
	md := make(map[string]any)

	for k, v := range query {
		md[k] = v
	}

	for _, a := range apps {
		pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
			Experiment: name, App: a, Stage: string(stage), State: "start",
		})

		k := fmt.Sprintf("%s/%s", name, a)

		// We don't want to use the HTTP request's context here.
		ctx, cancel := context.WithCancel(context.Background())
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

			return
		}

		pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
			Experiment: name, App: a, Stage: string(stage), State: "success",
		})
	}
}

// CancelTriggeredExperimentApps - DELETE /experiments/{name}/trigger[?apps=<foo,bar,baz>].
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

	apps, werr := triggerApps(appsFilter)
	if werr != nil {
		return werr
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
