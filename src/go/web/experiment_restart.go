package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"phenix/api/experiment"
	"phenix/api/experiment/startopts"
	"phenix/util"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// restartOptions are the options of a restart: those of its stop and those of its start.
type restartOptions struct {
	startopts.Options

	Force                  bool `json:"force"`
	KeepInjectionSnapshots bool `json:"keepInjectionSnapshots"`
}

// fields are the options as key/value pairs for an audit event, without the options that are not set.
func (o restartOptions) fields() []any {
	fields := o.Options.Fields()

	if o.Force {
		fields = append(fields, "phenix.experiment.force", true)
	}

	if o.KeepInjectionSnapshots {
		fields = append(fields, "phenix.experiment.keep_injection_snapshots", true)
	}

	return fields
}

// restartRequestOptions reads the restart options of the request body, or returns a 400. An empty body, null and {}
// are a restart with the default options. A restart cannot be a dry run.
func restartRequestOptions(w http.ResponseWriter, r *http.Request) (restartOptions, error) {
	var opts restartOptions

	err := util.DecodeJSONStrict(http.MaxBytesReader(w, r.Body, maxFilesRequestBytes), &opts)

	switch {
	case errors.Is(err, io.EOF):
		return restartOptions{}, nil
	case err != nil:
		return restartOptions{}, weberror.New(
			http.StatusBadRequest, "the body must be a JSON object of restart options: "+err.Error(),
		)
	}

	if err := opts.Check(); err != nil {
		return restartOptions{}, weberror.New(http.StatusBadRequest, err.Error())
	}

	if opts.DryRun {
		return restartOptions{}, weberror.New(
			http.StatusBadRequest, "a restart cannot be a dry run: remove dryRun, or use the start route for a dry run",
		)
	}

	return opts, nil
}

// RestartExperiment - POST /experiments/{name}/restart stops the running experiment and starts it again. It needs
// both experiments/stop update and experiments/start update. The optional body holds the start options without
// dryRun, and the stop options force and keepInjectionSnapshots. It answers as StartExperiment does, with a
// startResult also when force is set or the restart has warnings.
func RestartExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		user = middleware.UserFromContext(ctx)
		name = r.PathValue("name")
	)

	if name == experiment.ReservedName {
		return weberror.New(http.StatusBadRequest, "the name all is not supported: restart one experiment at a time")
	}

	fields := audit.Fields{Experiment: name}

	if !role.Allowed("experiments/stop", "update", name) || !role.Allowed("experiments/start", "update", name) {
		audit.Denied(r, audit.ExperimentRestart, "restarting experiment not allowed", fields)

		return weberror.NewWebError(nil, "restarting experiment %s not allowed for %s", name, user).
			SetStatus(http.StatusForbidden)
	}

	opts, err := restartRequestOptions(w, r)
	if err != nil {
		return err
	}

	fields.Extra = opts.fields()

	begun := time.Now()

	outcome, err := restartExperiment(ctx, name, user, opts)
	if err != nil {
		audit.Failure(r, audit.ExperimentRestart, "restarting experiment failed", fields, err)

		return err
	}

	fields.Extra = append(fields.Extra, "phenix.duration", time.Since(begun).Round(time.Millisecond).String())

	if outcome.periodic != nil {
		fields.Extra = append(fields.Extra, "phenix.job.id", outcome.periodic.ID)
	}

	audit.Success(r, audit.ExperimentRestart, "experiment restarted", fields)

	asResult := opts.Force || opts.TreatMinimegaErrorsAsWarnings || len(outcome.warnings) > 0

	return writeStartedAnswer(ctx, w, name, opts.Options, outcome, asResult)
}

// restartExperiment checks the disks of the running experiment name as phenix experiment restart does, stops it and
// starts it again, as owner. A refused disk check or a failed stop leaves it running. The outcome's warnings are
// those of the disk check, the stop and the start, each text once.
func restartExperiment(ctx context.Context, name, owner string, opts restartOptions) (startOutcome, error) {
	exp, err := experiment.Get(name)
	if err != nil {
		return startOutcome{}, weberror.NewWebError(err, "unable to restart experiment %s", name).
			SetStatus(storeStatus(err))
	}

	if !exp.Running() {
		return startOutcome{}, weberror.NewWebError(experiment.ErrStopNotRunning, "unable to restart experiment %s", name).
			SetStatus(http.StatusConflict)
	}

	checked, err := experiment.CheckDisksBeforeStop(
		ctx, exp.Spec, experiment.CheckAsWarnings(opts.TreatMinimegaErrorsAsWarnings),
	)
	for _, w := range checked {
		plog.Warn(plog.TypeSystem, w.Error(), "exp", name)
	}

	if err != nil {
		return startOutcome{}, startWebError(err, "unable to restart experiment %s, which is still running", name)
	}

	stopped, err := stopExperiment(name, opts.Force, opts.KeepInjectionSnapshots)
	if err != nil {
		return startOutcome{}, err
	}

	outcome, err := startExperiment(name, owner, opts.Options)
	if err != nil {
		return startOutcome{}, restartStartError(err, name, stopped.warnings)
	}

	outcome.warnings = uniqueTexts(noteTexts(checked), stopped.warnings, outcome.warnings)

	return outcome, nil
}

// restartStartError is the error of a restart whose start failed with err after the stop, which warned of warnings.
// A 409 is a start that another caller made or holds in the gap after the stop, so it keeps its own message. Else
// the message says that the experiment is stopped, followed by the warnings, which details.warnings also lists.
func restartStartError(err error, name string, warnings []string) error {
	var werr *weberror.WebError
	if !errors.As(err, &werr) {
		werr = weberror.NewWebError(err, "").SetStatus(http.StatusInternalServerError)
	}

	if werr.Status == http.StatusConflict {
		return werr
	}

	werr.Message = fmt.Sprintf("experiment %s is stopped: the restart stopped it, but its start failed", name)

	if len(warnings) > 0 {
		werr.Message += ". The stop warned: " + strings.Join(warnings, "; ")
		werr = werr.WithDetail("warnings", warnings)
	}

	return werr
}

// uniqueTexts joins the lists, in order, without a text that came before; it gives an empty list for none.
func uniqueTexts(lists ...[]string) []string {
	var (
		seen  = make(map[string]bool)
		texts = make([]string, 0)
	)

	for _, list := range lists {
		for _, text := range list {
			if !seen[text] {
				seen[text] = true
				texts = append(texts, text)
			}
		}
	}

	return texts
}
