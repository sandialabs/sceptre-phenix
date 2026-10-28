package web

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/app"
	putil "phenix/util"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// TriggerExperimentApps - POST /experiments/{name}/trigger[?apps=<foo,bar,baz>].
func TriggerExperimentApps(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]

		query      = r.URL.Query()
		appsFilter = query.Get("apps")
	)

	triggered := audit.Fields{Experiment: name, Extra: []any{"phenix.app.names", appsFilter}}

	if !role.Allowed("experiments/trigger", "create", name) {
		audit.Denied(r, audit.AppTrigger, "triggering experiment apps not allowed", triggered)

		return weberror.New(http.StatusForbidden, "forbidden")
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

			registered := addCanceler(k, cancel)

			err := experiment.TriggerRunning(ctx, name, a)
			if err != nil {
				dropCanceler(k, registered)

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

	audit.Success(r, audit.AppTrigger, "experiment apps triggered", triggered)
	w.WriteHeader(http.StatusNoContent)

	return nil
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

	go func() {
		apps := strings.SplitSeq(appsFilter, ",")

		for a := range apps {
			k := fmt.Sprintf("%s/%s", name, a)

			commonMu.Lock()
			cancels := cancelers[k]
			delete(cancelers, k)
			commonMu.Unlock()

			for _, c := range cancels {
				c.cancel()
			}

			pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
				Experiment: name, Verb: "delete", App: a, State: "success",
			})
		}
	}()

	audit.Success(r, audit.AppTriggerCancel, "experiment apps trigger canceled", canceled)

	w.WriteHeader(http.StatusNoContent)

	return nil
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
