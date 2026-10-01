package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"phenix/api/workflow"
	"phenix/util/plog"
)

const elapsedPrecision = time.Millisecond

// applyOptions is what runWorkflowApply needs. The apply command fills it
// from its flags and the global settings; tests fill it directly. An empty
// Name means the base name of the topology directory, and Config is relative
// to that directory unless absolute.
type applyOptions struct {
	Name                string
	Config              string
	ConfigExplicit      bool
	DryRun              bool
	Force               bool
	Socket              string
	InjectsBase         string
	TopologiesBase      string
	InjectsBaseExplicit bool
	Now                 func() time.Time
}

// configFile is a config file as read. Body goes to the server unchanged, so
// the line numbers in its explained validation errors match the file.
type configFile struct {
	Path        string
	Kind        string
	ContentType string
	Body        []byte
}

// applyRun is one workflow apply of a resolved topology directory. injects,
// configs and wfConfig are "" when the directory lacks them; staged and
// upserted record what the run has changed so far.
type applyRun struct {
	opts     applyOptions
	client   *workflowClient
	started  time.Time
	dir      string
	name     string
	injects  string
	configs  string
	wfConfig string
	staged   bool
	upserted int
}

// preflightResult is what the preflight found. wfConfig is nil when the
// directory has no workflow config.
type preflightResult struct {
	injectsBase string
	configs     []configFile
	wfConfig    *configFile
}

// runWorkflowApply deploys the topology directory named by arg through the
// phenix server at opts.Socket, in the steps the help describes: preflight,
// plan, injects, configs and apply. Nothing changes before the injects step,
// a dry run stops after the plan, and an error after the first change says
// what was already changed.
func runWorkflowApply(ctx context.Context, opts applyOptions, arg string) error {
	run, err := newApplyRun(opts, arg)
	if err != nil {
		return err
	}

	pre, err := run.preflight(ctx)
	if err != nil {
		return err
	}

	pending, err := run.dryRunConfigs(ctx, pre.configs)
	if err != nil {
		return err
	}

	plan, err := run.dryRunWorkflow(ctx, pre.wfConfig, pending)
	if err != nil {
		return err
	}

	if opts.DryRun {
		plog.Info(
			plog.TypeSystem, "dry run complete; nothing was changed",
			"step", stepPlan, "name", run.name, "dir", run.dir, "elapsed", run.elapsed(),
		)

		return nil
	}

	// Staging does not watch the context, so an interrupt that came during
	// the dry runs stops the run here, and one that came during the staging
	// stops it right after, before anything more is sent.
	if ctx.Err() != nil {
		return fmt.Errorf("interrupted before staging; nothing was changed: %w", context.Cause(ctx))
	}

	if err := run.stage(pre.injectsBase); err != nil {
		return err
	}

	if run.staged && ctx.Err() != nil {
		return run.changedError(fmt.Errorf("interrupted after staging; nothing more was sent: %w", context.Cause(ctx)))
	}

	if err := run.upsert(ctx, pre.configs); err != nil {
		return run.changedError(err)
	}

	if err := run.apply(ctx, pre.wfConfig, plan); err != nil {
		return run.changedError(err)
	}

	plog.Info(
		plog.TypeSystem, "workflow apply complete",
		"step", stepApply, "name", run.name, "dir", run.dir, "elapsed", run.elapsed(),
	)

	return nil
}

// dryRunConfigs dry-runs every config and returns their pending refs, as the
// server answered them, in upsert order. Once every config was dry-run, it
// fails when any was rejected, or when two files define the same config,
// since the later upsert would replace the earlier.
func (r *applyRun) dryRunConfigs(ctx context.Context, configs []configFile) ([]string, error) {
	var pending, clashes []string

	// definedBy maps each Kind/name to the first file that defines it.
	definedBy := map[string]string{}

	rejected, err := r.forEachConfig(ctx, stepPlan, "dry run of", configs, func(cfg configFile, file string) error {
		result, err := r.client.configDryRun(ctx, r.name, cfg.Body, cfg.ContentType)
		if err != nil {
			return err
		}

		ref := result.Kind + "/" + result.Name

		plog.Info(
			plog.TypeSystem, "config checked",
			"step", stepPlan, "file", file, "kind", result.Kind, "name", result.Name, "action", string(result.Action),
		)

		if first, ok := definedBy[ref]; ok {
			clashes = append(clashes, fmt.Sprintf("%s and %s both define %s", first, file, ref))

			return nil
		}

		definedBy[ref] = file
		pending = append(pending, result.Pending)

		return nil
	})
	if err != nil {
		return nil, err
	}

	var problems []string

	if rejected > 0 {
		problems = append(problems, fmt.Sprintf("%d of %d configs rejected by the phenix server", rejected, len(configs)))
	}

	problems = append(problems, clashes...)

	if len(problems) > 0 {
		return nil, fmt.Errorf("%s; nothing was changed", strings.Join(problems, "; "))
	}

	return pending, nil
}

// dryRunWorkflow dry-runs the workflow config with pending as its pending
// configs and logs the plan. Outside a dry run, a restart needs opts.Force.
func (r *applyRun) dryRunWorkflow(ctx context.Context, wfConfig *configFile, pending []string) (workflow.Plan, error) {
	if wfConfig == nil {
		plog.Warn(plog.TypeSystem, "no workflow config; skipping the dry run", "step", stepPlan, "dir", r.dir)

		return workflow.Plan{}, nil
	}

	file := r.rel(wfConfig.Path)

	result, err := r.client.apply(ctx, r.name, wfConfig.Body, wfConfig.ContentType, nil, true, pending, "")
	if err != nil {
		logValidation(stepPlan, file, wfConfig.Kind, err)

		return workflow.Plan{}, r.requestError("dry run of "+file, err)
	}

	plog.Info(
		plog.TypeSystem, "workflow plan",
		"step", stepPlan, "file", file, "action", string(result.Action), "experiment", result.Experiment, "reason", result.Reason,
	)

	if !r.opts.DryRun && !r.opts.Force && result.Action == workflow.ActionRestart {
		return workflow.Plan{}, fmt.Errorf(
			"experiment %s is running; apply would stop, reconfigure and restart it; re-run with -f",
			result.Experiment,
		)
	}

	return result.Plan, nil
}

// forEachConfig calls send with each config and its path relative to the
// topology directory, logs every rejection at step, and returns how many
// configs were rejected. An error that ends the run, such as an interrupt or
// a server that stopped answering, is returned at once, described as what
// and the file; a *notSentError is returned as it is.
func (r *applyRun) forEachConfig(
	ctx context.Context,
	step, what string,
	configs []configFile,
	send func(cfg configFile, file string) error,
) (int, error) {
	rejected := 0

	for _, cfg := range configs {
		file := r.rel(cfg.Path)

		err := send(cfg, file)
		if err == nil {
			continue
		}

		var notSent *notSentError
		if errors.As(err, &notSent) {
			return 0, err
		}

		// The remaining configs would fail the same way, and a server that
		// ignores dryRun would store them.
		if ctx.Err() != nil || errors.Is(err, errServerUnreachable) || errors.Is(err, errNoConfigDryRun) {
			return 0, r.requestError(what+" "+file, err)
		}

		if !logValidation(step, file, cfg.Kind, err) {
			plog.Error(plog.TypeSystem, "config rejected", "step", step, "file", file, "kind", cfg.Kind, "err", err)
		}

		rejected++
	}

	return rejected, nil
}

// upsert sends every config to the server and fails when any was rejected.
func (r *applyRun) upsert(ctx context.Context, configs []configFile) error {
	if len(configs) == 0 {
		plog.Warn(plog.TypeSystem, "no configs to upsert; skipping", "step", stepConfigs, "dir", r.dir)

		return nil
	}

	rejected, err := r.forEachConfig(ctx, stepConfigs, "upserting", configs, func(cfg configFile, file string) error {
		if err := interruptedBefore(ctx, "upserting "+file); err != nil {
			return err
		}

		if err := r.client.upsertConfig(ctx, r.name, cfg.Body, cfg.ContentType); err != nil {
			return mayStillFinish(ctx, err)
		}

		r.upserted++

		plog.Info(plog.TypeSystem, "config upserted", "step", stepConfigs, "file", file, "kind", cfg.Kind)

		return nil
	})
	if err != nil {
		return err
	}

	if rejected > 0 {
		return fmt.Errorf("%d of %d configs rejected by the phenix server", rejected, len(configs))
	}

	return nil
}

// apply applies the workflow config, tagged with where it came from, with the
// dry run's action as expect, so that the server changes nothing when its
// plan changed since. A result of none is logged as a warning, since a zero
// exit status alone cannot tell it from a deploy.
func (r *applyRun) apply(ctx context.Context, wfConfig *configFile, plan workflow.Plan) error {
	if wfConfig == nil {
		plog.Warn(plog.TypeSystem, "no workflow config; skipping apply", "step", stepApply, "dir", r.dir)

		return nil
	}

	file := r.rel(wfConfig.Path)
	tags := workflowTags(ctx, r.dir, r.name, r.opts.Now())

	if err := interruptedBefore(ctx, "applying "+file); err != nil {
		return err
	}

	plog.Info(
		plog.TypeSystem, "applying workflow config",
		"step", stepApply, "file", file, "action", string(plan.Action), "experiment", plan.Experiment,
	)
	plog.Debug(plog.TypeSystem, "workflow tags", "step", stepApply, "tags", strings.Join(tags, ","))

	result, err := r.client.apply(ctx, r.name, wfConfig.Body, wfConfig.ContentType, tags, false, nil, plan.Action)
	if err != nil {
		return mayStillFinish(ctx, r.requestError("applying "+file, err))
	}

	if result.Action == workflow.ActionNone {
		plog.Warn(
			plog.TypeSystem, "workflow config applied; no experiment changed",
			"step", stepApply, "file", file, "action", string(result.Action), "experiment", result.Experiment, "reason", result.Reason,
		)

		return nil
	}

	plog.Info(
		plog.TypeSystem, "workflow config applied",
		"step", stepApply, "file", file, "action", string(result.Action), "experiment", result.Experiment,
	)

	return nil
}

// notSentError is an interrupt that came before the request what describes
// was sent, so the server never received it.
type notSentError struct {
	what  string
	cause error
}

// Error says what the run was interrupted before.
func (e *notSentError) Error() string {
	return "interrupted before " + e.what
}

// Unwrap returns the cause of the interrupt.
func (e *notSentError) Unwrap() error {
	return e.cause
}

// interruptedBefore returns a *notSentError once ctx has ended, else nil.
func interruptedBefore(ctx context.Context, what string) error {
	if ctx.Err() == nil {
		return nil
	}

	return &notSentError{what: what, cause: context.Cause(ctx)}
}

// mayStillFinish adds to err, from a request sent while ctx was alive, that
// the server may still carry the request out when ctx has ended since.
func mayStillFinish(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		return err
	}

	return fmt.Errorf("%w; the phenix server may still finish the request; check the experiment", err)
}

// changedError ends err with what the run had already changed, so that the
// user knows the state it leaves. A server that did not answer may have
// received the request, and the error says so first.
func (r *applyRun) changedError(err error) error {
	if errors.Is(err, errServerUnreachable) {
		err = fmt.Errorf("%w; the phenix server may have received the request; check the experiment", err)
	}

	var changed []string

	if r.staged {
		changed = append(changed, "injects staged")
	}

	switch {
	case r.upserted == 1:
		changed = append(changed, "1 config upserted")
	case r.upserted > 1:
		changed = append(changed, fmt.Sprintf("%d configs upserted", r.upserted))
	}

	if len(changed) == 0 {
		return err
	}

	return fmt.Errorf("%w; already changed: %s; fix the cause and run the command again", err, strings.Join(changed, ", "))
}

// requestError describes a failed request made while doing what, naming the
// socket when the server did not answer.
func (r *applyRun) requestError(what string, err error) error {
	if errors.Is(err, errServerUnreachable) {
		return fmt.Errorf("%s via %s: %w", what, r.opts.Socket, err)
	}

	return fmt.Errorf("%s: %w", what, err)
}

// rel returns path relative to the topology directory, for logs.
func (r *applyRun) rel(path string) string {
	relative, _ := filepath.Rel(r.dir, path)

	return relative
}

// elapsed returns the time since the run started, for logs.
func (r *applyRun) elapsed() string {
	return r.opts.Now().Sub(r.started).Round(elapsedPrecision).String()
}

// logValidation logs each explained validation line the server sent for a
// rejected file as its own error record, and reports whether there were any.
func logValidation(step, file, kind string, err error) bool {
	var apiErr *workflowAPIError
	if !errors.As(err, &apiErr) || apiErr.Validation == "" {
		return false
	}

	for line := range strings.SplitSeq(apiErr.Validation, "\n") {
		plog.Error(plog.TypeSystem, line, "step", step, "file", file, "kind", kind)
	}

	return true
}

// workflowTags returns the tags of the real apply: how, when, from where and
// under which name the workflow was applied, and the git commit when dir is
// in a git work tree.
func workflowTags(ctx context.Context, dir, branch string, now time.Time) []string {
	tags := []string{
		"method=workflow",
		"dir=" + dir,
		"branch=" + branch,
		"workflow_date=" + now.Format(workflowDateLayout),
	}

	if ref := gitCommitRef(ctx, dir); ref != "" {
		tags = append(tags, "commit="+ref)
	}

	return tags
}
