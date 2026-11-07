package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/experiment/startopts"
	"phenix/api/vm"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/util"
	"phenix/web/weberror"
)

// errDryRunPeriodic is the 400 of a start body that asks for a dry run and for the apps' scheduled runs, which need
// a running experiment.
var errDryRunPeriodic = errors.New(
	"a dry run leaves the experiment stopped, so it cannot run the apps' running stage on its schedule: " +
		"remove honorRunPeriodically or dryRun",
)

// startResult is the 200 body of a start with dryRun or treatMinimegaErrorsAsWarnings.
type startResult struct {
	Experiment json.RawMessage `json:"experiment"`
	DryRun     bool            `json:"dryRun,omitempty"`
	Warnings   []string        `json:"warnings"`
	MMScript   string          `json:"mmScript,omitempty"`
}

// startRequestOptions reads the start options of the request body, as phenix experiment start reads its flags (see
// startopts.Decode), or returns a 400.
func startRequestOptions(w http.ResponseWriter, r *http.Request) (startopts.Options, error) {
	opts, err := startopts.Decode(http.MaxBytesReader(w, r.Body, maxFilesRequestBytes))
	if err != nil {
		return startopts.Options{}, weberror.New(http.StatusBadRequest, err.Error())
	}

	if opts.DryRun && opts.Periodic(false) {
		return startopts.Options{}, weberror.New(http.StatusBadRequest, errDryRunPeriodic.Error())
	}

	return opts, nil
}

// StartExperiment - POST /experiments/{name}/start. The name all starts every
// stopped experiment in a background job (see startAllExperiments).
//
// The optional body holds the start options (see startopts.Options). Without
// options, the answer is 200 with the experiment. With honorRunPeriodically
// true, it is 202 with the job that runs the apps on their schedule. Else,
// with dryRun or treatMinimegaErrorsAsWarnings, it is 200 with a startResult.
func StartExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		name = mux.Vars(r)["name"]
	)

	if name == experiment.ReservedName {
		return startAllExperiments(w, r)
	}

	started := audit.Fields{Experiment: name}

	if !role.Allowed("experiments/start", "update", name) {
		user := middleware.UserFromContext(ctx)
		audit.Denied(r, audit.ExperimentStart, "starting experiment not allowed", started)
		err := weberror.NewWebError(
			nil,
			"starting experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	opts, err := startRequestOptions(w, r)
	if err != nil {
		return err
	}

	started.Extra = opts.Fields()

	begun := time.Now()

	outcome, err := startExperiment(name, middleware.UserFromContext(ctx), opts)
	if err != nil {
		audit.Failure(r, audit.ExperimentStart, "starting experiment failed", started, err)

		return err
	}

	started.Extra = append(started.Extra, "phenix.duration", time.Since(begun).Round(time.Millisecond).String())

	if outcome.periodic != nil {
		started.Extra = append(started.Extra, "phenix.job.id", outcome.periodic.ID)
	}

	audit.Success(r, audit.ExperimentStart, "experiment started", started)

	return writeStartAnswer(ctx, w, name, opts, outcome)
}

// writeStartAnswer answers a start of the experiment name with opts that outcome reports, as StartExperiment says.
func writeStartAnswer(
	ctx context.Context, w http.ResponseWriter, name string, opts startopts.Options, outcome startOutcome,
) error {
	if opts.Periodic(false) && outcome.periodic != nil {
		w.Header().Set("Location", apiLocation("jobs/"+outcome.periodic.ID))

		return writeJob(w, http.StatusAccepted, *outcome.periodic)
	}

	// The broadcast leaves the VM list out; API callers still get it.
	body, err := marshaler.Marshal(util.ExperimentToProtobuf(*outcome.exp, "", vm.ListFor(ctx, outcome.exp)))
	if err != nil {
		return weberror.NewWebError(err, "unable to marshal experiment %s", name).SetStatus(http.StatusInternalServerError)
	}

	if opts.DryRun || opts.TreatMinimegaErrorsAsWarnings {
		result := startResult{Experiment: body, DryRun: opts.DryRun, Warnings: outcome.warnings, MMScript: ""}
		if opts.DryRun {
			result.MMScript = outcome.mmScript
		}

		if body, err = json.Marshal(result); err != nil {
			return weberror.NewWebError(err, "unable to marshal the start of %s", name).
				SetStatus(http.StatusInternalServerError)
		}
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
