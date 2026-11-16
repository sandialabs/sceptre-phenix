package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"phenix/api/experiment"
	"phenix/api/experiment/startopts"
	"phenix/api/job"
	"phenix/app"
	"phenix/types"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// startAllExperiments answers POST /experiments/all/start: it starts, one after
// another in a background job, each stopped experiment that the role may
// start, as phenix experiment start all does, and answers 202 with the job.
// The job leaves out the experiments the role may not start, and a cancel
// stops it before its next experiment. A role that may start none of the
// experiments gets 403. The start options of the body apply to each start; an
// item's result holds what a start with options reports (see allStartResult).
func startAllExperiments(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())
	all := audit.Fields{Experiment: experiment.ReservedName}

	if !role.Allowed("experiments/start", "update") {
		audit.Denied(r, audit.ExperimentStart, "starting experiments not allowed", all)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	opts, err := startRequestOptions(w, r)
	if err != nil {
		return err
	}

	all.Extra = opts.Fields()
	owner := middleware.UserFromContext(r.Context())

	experiments, err := experiment.List()
	if err != nil {
		audit.Failure(r, audit.ExperimentStart, "starting experiments failed", all, err)

		return weberror.New(storeStatus(err), "unable to list experiments")
	}

	var (
		names     = make([]string, 0, len(experiments))
		permitted = len(experiments) == 0
	)

	for _, exp := range experiments {
		if !role.Allowed("experiments/start", "update", exp.Metadata.Name) {
			continue
		}

		permitted = true

		if !exp.Running() {
			names = append(names, exp.Metadata.Name)
		}
	}

	if !permitted {
		audit.Denied(r, audit.ExperimentStart, "starting experiments not allowed", all)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	return startJob(w, r, job.Spec{
		Kind:       job.KindExperimentsStart,
		Owner:      "",
		Experiment: "",
		Title:      "Start " + countOf(len(names), "experiment"),
		Params:     startJobParams(opts),
		Items:      names,
		BytesTotal: 0,
		Cancelable: true,
		Run: func(ctx context.Context, run *job.Run) error {
			for _, name := range names {
				if err := ctx.Err(); err != nil {
					return fmt.Errorf("starting experiments: %w", err)
				}

				run.ItemStarted(name)

				outcome, err := startExperiment(name, owner, opts)

				switch {
				case err == nil:
					run.ItemSucceeded(name, newAllStartResult(opts, outcome))
				case errors.Is(err, experiment.ErrExperimentRunning):
					run.ItemSkipped(name, "the experiment is already running")
				default:
					run.ItemFailed(name, err)
				}
			}

			return nil
		},
	})
}

// allStartResult is the result of an item of a job of job.KindExperimentsStart: what a start with options reports.
type allStartResult struct {
	DryRun   bool     `json:"dryRun,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	MMScript string   `json:"mmScript,omitempty"`
	// the job that runs the apps of the experiment on their schedule
	PeriodicJob string `json:"periodicJob,omitempty"`
}

// newAllStartResult is the result of a start with opts that outcome reports, or nil when it has nothing to tell.
func newAllStartResult(opts startopts.Options, outcome startOutcome) any {
	result := allStartResult{DryRun: opts.DryRun, Warnings: nil, MMScript: "", PeriodicJob: ""}

	if opts.DryRun || opts.TreatMinimegaErrorsAsWarnings {
		result.Warnings = outcome.warnings
	}

	if opts.DryRun {
		result.MMScript = outcome.mmScript
	}

	if outcome.periodic != nil {
		result.PeriodicJob = outcome.periodic.ID
	}

	if !opts.DryRun && !opts.TreatMinimegaErrorsAsWarnings && result.PeriodicJob == "" {
		return nil
	}

	return result
}

// startJobParams are the params of a job of job.KindExperimentsStart: the options that are set, by their JSON names.
func startJobParams(opts startopts.Options) map[string]string {
	params := map[string]string{}

	if opts.DryRun {
		params["dryRun"] = "true"
	}

	if opts.TreatMinimegaErrorsAsWarnings {
		params["treatMinimegaErrorsAsWarnings"] = "true"
	}

	if opts.HonorRunPeriodically != nil {
		params["honorRunPeriodically"] = strconv.FormatBool(*opts.HonorRunPeriodically)
	}

	if opts.VLANMin != 0 {
		params["vlanMin"] = strconv.Itoa(opts.VLANMin)
	}

	if opts.VLANMax != 0 {
		params["vlanMax"] = strconv.Itoa(opts.VLANMax)
	}

	if len(params) == 0 {
		return nil
	}

	return params
}

// triggerAllExperiments answers POST /experiments/all/trigger: it runs the
// stage of the apps, one experiment after another in a background job, in
// each experiment that the role may trigger, as phenix experiment trigger
// does for all. The running stage runs only in running experiments. The job
// skips an experiment that does not have an app, or for which the stage does
// not apply to one, and leaves out the experiments the role may not trigger.
// A cancel stops the apps that run, and the job before its next experiment. A
// role that may trigger none of the experiments gets 403.
func triggerAllExperiments(w http.ResponseWriter, r *http.Request, stageParam string, query url.Values) error {
	var (
		role       = middleware.RoleFromContext(r.Context())
		appsFilter = query.Get("apps")
		triggered  = audit.Fields{
			Experiment: experiment.ReservedName,
			Extra:      []any{"phenix.app.names", appsFilter, "phenix.app.stage", cmp.Or(stageParam, string(app.ActionRunning))},
		}
	)

	if !role.Allowed("experiments/trigger", "create") {
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

	experiments, err := experiment.List()
	if err != nil {
		audit.Failure(r, audit.AppTrigger, "triggering experiment apps failed", triggered, err)

		return weberror.New(storeStatus(err), "unable to list experiments")
	}

	var (
		targets   = make([]types.Experiment, 0, len(experiments))
		permitted = len(experiments) == 0
	)

	for _, exp := range experiments {
		if !role.Allowed("experiments/trigger", "create", exp.Metadata.Name) {
			continue
		}

		permitted = true

		if stage != app.ActionRunning || exp.Running() {
			targets = append(targets, exp)
		}
	}

	if !permitted {
		audit.Denied(r, audit.AppTrigger, "triggering experiment apps not allowed", triggered)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	names := make([]string, 0, len(targets))
	for _, exp := range targets {
		names = append(names, exp.Metadata.Name)
	}

	return startJob(w, r, job.Spec{
		Kind:       job.KindExperimentsTrigger,
		Owner:      "",
		Experiment: "",
		Title:      "Trigger the " + string(stage) + " stage in " + countOf(len(names), "experiment"),
		Params:     map[string]string{"stage": string(stage), "apps": strings.Join(apps, ",")},
		Items:      names,
		BytesTotal: 0,
		Cancelable: true,
		Run: func(ctx context.Context, run *job.Run) error {
			for i := range targets {
				exp := &targets[i]
				name := exp.Metadata.Name

				if err := ctx.Err(); err != nil {
					return fmt.Errorf("triggering experiment apps: %w", err)
				}

				if werr := triggerStageError(exp, stage, apps); werr != nil {
					run.ItemSkipped(name, werr.Message)

					continue
				}

				run.ItemStarted(name)

				if err := runTriggeredApps(ctx, name, stage, apps, query); err != nil {
					run.ItemFailed(name, err)

					continue
				}

				run.ItemSucceeded(name, nil)
			}

			return nil
		},
	})
}

// auditExperimentsJob audits, as the user who started it, what a finished
// job of job.KindExperimentsStart or job.KindExperimentsTrigger did to each of its
// experiments.
func auditExperimentsJob(j job.Job) {
	action, done := audit.ExperimentStart, "experiment started"

	var extra []any

	if j.Kind == job.KindExperimentsTrigger {
		action, done = audit.AppTrigger, "experiment apps triggered"
		extra = []any{"phenix.app.names", j.Params["apps"], "phenix.app.stage", j.Params["stage"]}
	}

	for _, item := range j.Items {
		fields := audit.Fields{Experiment: item.Name, Extra: append(slices.Clone(extra), "phenix.job.id", j.ID)}

		switch item.Status {
		case job.ItemSucceeded:
			audit.Background(j.Owner, action, done, audit.OutcomeSuccess, fields)
		case job.ItemFailed:
			audit.BackgroundFailure(j.Owner, action, string(action)+" failed", fields, errors.New(item.Error))
		case job.ItemCanceled:
			audit.BackgroundFailure(j.Owner, action, string(action)+" failed", fields, errors.New("canceled"))
		case job.ItemSkipped, job.ItemPending, job.ItemRunning:
		}
	}
}
