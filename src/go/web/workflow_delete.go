package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/workflow"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// workflowDeleteFailed is the message of a delete that the mapped experiments refuse.
const workflowDeleteFailed = "unable to delete phenix workflow deployment"

// DeleteWorkflow - DELETE /workflow/apply/{branch} deletes the experiment that
// a workflow apply mapped to the branch, and stops it first when force is true.
// With dryRun=true it changes nothing. The response names the other
// experiments that inject files from the injects directory, which the client
// deletes after the request.
func DeleteWorkflow(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "DeleteWorkflow")

	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		branch  = mux.Vars(r)["branch"]
		deleted = audit.Fields{Extra: []any{"phenix.workflow.branch", branch}}
	)

	if !role.Allowed("workflow", "delete") {
		audit.Denied(r, audit.WorkflowDelete, "deleting phenix workflow deployment not allowed", deleted)

		err := weberror.NewWebError(
			nil,
			"deleting phenix workflow deployment is not allowed for user %s",
			middleware.UserFromContext(ctx),
		)

		return err.SetStatus(http.StatusForbidden)
	}

	result, force, err := deleteWorkflow(r, branch)

	deleted.DryRun = result.DryRun
	deleted.Experiment = result.Experiment

	if force {
		deleted.Extra = append(deleted.Extra, "phenix.workflow.force", true)
	}

	if result.Stopped && !result.DryRun {
		deleted.Extra = append(deleted.Extra, "phenix.workflow.stopped", true)
	}

	if err != nil {
		audit.Failure(r, audit.WorkflowDelete, "deleting phenix workflow deployment failed", deleted, err)

		return err
	}

	done := "deleted phenix workflow deployment"
	if result.DryRun {
		done = "checked phenix workflow deployment"
	}

	audit.Success(r, audit.WorkflowDelete, done, deleted)

	// A DeleteResult holds only strings, bools and a list of strings, so marshaling it cannot fail.
	body, _ := json.Marshal(result)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// deleteWorkflow checks the request and plans the delete of the experiment
// mapped to branch and, unless it is a dry run, carries the plan out. It also
// returns whether force is set. The result holds as much as is known, also on
// an error.
func deleteWorkflow(r *http.Request, branch string) (workflow.DeleteResult, bool, error) {
	result := workflow.DeleteResult{Branch: branch, InjectsUsedBy: []string{}} //nolint:exhaustruct // set below

	// r.URL.Query drops a pair it cannot parse, which could turn a dry run into a delete.
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return result, false, weberror.NewWebError(err, "invalid query string")
	}

	dryRun, dryRunErr := parseDryRun(q)
	result.DryRun = dryRun && dryRunErr == nil

	force, forceErr := parseForce(q)
	force = force && forceErr == nil

	if err := workflow.ValidateName(branch); err != nil {
		return result, force, weberror.NewWebError(err, "invalid branch name")
	}

	if dryRunErr != nil {
		return result, force, dryRunErr
	}

	if forceErr != nil {
		return result, force, forceErr
	}

	dir, err := injectsDirQuery(q, branch)
	if err != nil {
		return result, force, err
	}

	result.InjectsDir = dir

	exps, err := experiment.List()
	if err != nil {
		err := weberror.NewWebError(err, "unable to get list of experiments")

		return result, force, err.SetStatus(http.StatusInternalServerError)
	}

	plan, err := workflow.NewDeletePlan(branch, workflow.Mapped(exps, branch), force)
	result.Experiment = plan.Experiment

	if err != nil {
		if errors.Is(err, workflow.ErrRunning) {
			return result, force, weberror.NewWebError(err, workflowDeleteFailed).SetStatus(http.StatusConflict)
		}

		return result, force, workflowWebError(err, workflowDeleteFailed)
	}

	result.InjectsUsedBy = workflow.InjectsUsers(exps, dir, plan.Experiment)

	if dryRun || plan.Experiment == "" {
		result.Stopped = plan.Stop

		return result, force, nil
	}

	if plan.Stop {
		// as phenix experiment delete -f does: stopped also when minimega cannot kill the VMs
		stopped, err := stopExperiment(plan.Experiment, true, false)

		switch {
		case errors.Is(err, experiment.ErrStopNotRunning), errors.Is(err, experiment.ErrExperimentNotFound):
			plog.Info(plog.TypeSystem, "another request already stopped or deleted the experiment", "exp", plan.Experiment)
		case err != nil:
			return result, force, err
		default:
			result.Stopped = true

			for _, warning := range stopped.warnings {
				plog.Warn(plog.TypeSystem, warning, "exp", plan.Experiment)
			}
		}
	}

	if err := deleteMappedExperiment(plan.Experiment); err != nil {
		var werr *weberror.WebError
		if result.Stopped && errors.As(err, &werr) {
			werr.Message = fmt.Sprintf("experiment %s was stopped; %s", plan.Experiment, werr.Message)
		}

		return result, force, err
	}

	result.InjectsUsedBy, err = injectsUsersAfterDelete(dir, plan.Experiment)

	return result, force, err
}

// injectsUsersAfterDelete returns the experiments, except name, that inject
// files from dir, from a new list of experiments. The stop before the delete
// can take minutes, so the list from before it can lack a user. When the new
// list fails, the error tells the client to keep the directory.
func injectsUsersAfterDelete(dir, name string) ([]string, error) {
	exps, err := experiment.List()
	if err != nil {
		plog.Error(plog.TypeSystem, "listing experiments after the delete", "exp", name, "err", err)

		werr := weberror.NewWebError(
			err,
			"experiment %s was deleted, but phenix cannot tell which experiments use the injects directory %s; "+
				"keep the directory and send the delete again",
			name, dir,
		)

		return nil, werr.SetStatus(http.StatusInternalServerError)
	}

	return workflow.InjectsUsers(exps, dir, name), nil
}

// injectsDirQuery is the cleaned injects query parameter, which must be an
// absolute path, or the branch's directory in the injects base when the query
// does not have it.
func injectsDirQuery(q url.Values, branch string) (string, error) {
	values, ok := q["injects"]

	switch {
	case !ok:
		return filepath.Join(common.InjectsBase, branch), nil
	case len(values) > 1:
		return "", weberror.NewWebError(nil, "injects must be given at most once")
	case !filepath.IsAbs(values[0]):
		return "", weberror.NewWebError(nil, "injects must be an absolute path, not %q", values[0])
	default:
		return filepath.Clean(values[0]), nil
	}
}

// deleteMappedExperiment deletes the stopped experiment name as DELETE
// /experiments/{name} does. An experiment that another request deleted first
// is not an error.
func deleteMappedExperiment(name string) error {
	if err := cache.LockExperimentForDeletion(name); err != nil {
		plog.Error(plog.TypeSystem, "locking experiment", "exp", name, "action", "deletion", "err", err)

		err := weberror.NewWebError(err, "unable to lock experiment %s for deletion", name)

		return err.SetStatus(http.StatusConflict)
	}

	defer cache.UnlockExperiment(name)

	err := experiment.Delete(name, experiment.DeleteWithBaseDirInside(experiment.BaseDirRoot()))
	if errors.Is(err, experiment.ErrExperimentNotFound) {
		plog.Info(plog.TypeSystem, "experiment already deleted by another request", "exp", name)

		return nil
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "deleting experiment", "exp", name, "err", err)

		return deleteWebError(name, err)
	}

	broadcastExperimentDeleted(name)

	return nil
}
