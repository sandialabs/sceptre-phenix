package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/job"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

// jobs is the registry of the server's background jobs; tests replace it.
var jobs = job.Default //nolint:gochecknoglobals // replaced in tests

// artifactServed are called when a download of a job's artifact begins, by
// job kind, to log what the artifact holds.
var artifactServed = map[string]func(r *http.Request, j job.Job){} //nolint:gochecknoglobals // filled in by the job kinds

// artifactAllowed are checked, by job kind, each time a job's artifact is
// downloaded, so a role that has since lost the permission the job's work
// needed no longer gets what it produced.
var artifactAllowed = map[string]func(role rbac.Role, j job.Job) bool{} //nolint:gochecknoglobals // filled in by the job kinds

// jobFinished are called once a job has finished, by job kind, to audit what
// its work did as the user who started it.
var jobFinished = map[string]func(j job.Job){} //nolint:gochecknoglobals // filled in by the job kinds

// jobAccess checks whether a role may act on a job of another user.
type jobAccess func(role rbac.Role, j job.Job) bool

// The checks for each verb on another user's job, written out so the RBAC
// policy generator finds them. A job of an experiment needs the jobs
// permission on its experiment; a job of none, such as a disk clone, needs
// jobs/global, which an experiment-scoped role's patterns, even "*", do not
// reach.
//
//nolint:gochecknoglobals // constant checks
var (
	mayListJob jobAccess = func(role rbac.Role, j job.Job) bool {
		if j.Experiment == "" {
			return role.Allowed("jobs/global", "list")
		}

		return role.Allowed("jobs", "list", j.Experiment)
	}
	mayGetJob jobAccess = func(role rbac.Role, j job.Job) bool {
		if j.Experiment == "" {
			return role.Allowed("jobs/global", "get")
		}

		return role.Allowed("jobs", "get", j.Experiment)
	}
	mayCancelJob jobAccess = func(role rbac.Role, j job.Job) bool {
		if j.Experiment == "" {
			return role.Allowed("jobs/global", "update")
		}

		return role.Allowed("jobs", "update", j.Experiment)
	}
	mayRemoveJob jobAccess = func(role rbac.Role, j job.Job) bool {
		if j.Experiment == "" {
			return role.Allowed("jobs/global", "delete")
		}

		return role.Allowed("jobs", "delete", j.Experiment)
	}
)

// ConfigureJobs points the job registry at the server: it tells websocket
// clients of job changes, and keeps artifacts in <PhenixBase>/jobs, where it
// first deletes the job folders a previous run left.
func ConfigureJobs() {
	dir := filepath.Join(common.PhenixBase, "jobs")

	jobs.Configure(job.WithNotifier(notifyJob), job.WithArtifactDir(dir))

	removed, err := jobs.RemoveStaleArtifacts()
	if err != nil {
		plog.Warn(plog.TypeSystem, "removing stale job artifacts", "dir", dir, "err", err)
	}

	if removed > 0 {
		plog.Info(plog.TypeSystem, "removed stale job artifacts", "dir", dir, "count", removed)
	}
}

// notifyJob tells websocket clients of a change to a job, and logs its end.
func notifyJob(j job.Job, removed bool) {
	broker.PublishJob(j, removed)
	logJobEnd(j, removed)
}

// logJobEnd logs a job's end, and audits its work, when the change to it is
// its finishing.
func logJobEnd(j job.Job, removed bool) {
	// a job's final state is told once
	if removed || !j.Status.Terminal() {
		return
	}

	succeeded, failed, skipped, _ := j.Counts()

	// none for a job canceled while queued
	var duration time.Duration
	if j.Started != nil && j.Finished != nil {
		duration = j.Finished.Sub(*j.Started)
	}

	plog.Info(
		plog.TypeAction, "job finished",
		"job", j.ID, "kind", j.Kind, "user", j.Owner, "exp", j.Experiment, "status", string(j.Status),
		"succeeded", succeeded, "failed", failed, "skipped", skipped, "duration", duration.String(),
	)

	if auditJob, ok := jobFinished[j.Kind]; ok {
		auditJob(j)
	}
}

// jobError is why a finished job, or its first failed item, failed.
func jobError(j job.Job) error {
	if j.Status == job.StatusCanceled {
		return errors.New("canceled")
	}

	for _, item := range j.Items {
		if item.Status == job.ItemFailed && item.Error != "" {
			return errors.New(item.Error)
		}
	}

	if j.Error != "" {
		return errors.New(j.Error)
	}

	return fmt.Errorf("job %s", j.Status)
}

// startJob starts a job for the request's user, logs it, and answers 202 with
// the job and its location, under the base path.
func startJob(w http.ResponseWriter, r *http.Request, spec job.Spec) {
	spec.Owner = middleware.UserFromContext(r.Context())

	j, err := jobs.Start(spec)
	if errors.Is(err, job.ErrQueueFull) {
		plog.Warn(plog.TypeSystem, "job queue full", "kind", spec.Kind, "user", spec.Owner)
		w.Header().Set("Retry-After", "60")
		http.Error(w, err.Error(), http.StatusTooManyRequests)

		return
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "starting job", "kind", spec.Kind, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	plog.Info(
		plog.TypeAction, "job started",
		"job", j.ID, "kind", j.Kind, "user", j.Owner, "exp", j.Experiment, "items", len(j.Items),
	)

	w.Header().Set("Location", apiLocation("jobs/"+j.ID))
	writeJob(w, http.StatusAccepted, j)
}

// countOf is n and noun, plural unless n is 1, as in "3 VMs".
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

// writeJob answers with the job as JSON.
func writeJob(w http.ResponseWriter, status int, j job.Job) {
	body, err := json.Marshal(j)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ownJob returns the job the request names, if the user started it or may act
// on it. Anyone else is answered 404, as for a job that does not exist.
func ownJob(w http.ResponseWriter, r *http.Request, may jobAccess) (job.Job, bool) {
	return ownJobOr(w, r, may, nil)
}

// ownJobOr is ownJob, calling denied, if given, with a job the user may not
// act on before answering 404.
func ownJobOr(w http.ResponseWriter, r *http.Request, may jobAccess, denied func(job.Job)) (job.Job, bool) {
	var (
		id   = mux.Vars(r)["id"]
		ctx  = r.Context()
		user = middleware.UserFromContext(ctx)
		role = middleware.RoleFromContext(ctx)
	)

	j, ok := jobs.Get(id)
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)

		return job.Job{}, false
	}

	if j.Owner != user && !may(role, j) {
		plog.Warn(plog.TypeSecurity, "accessing another user's job not allowed", "user", user, "job", id, "method", r.Method)

		if denied != nil {
			denied(j)
		}

		http.Error(w, "job not found", http.StatusNotFound)

		return job.Job{}, false
	}

	return j, true
}

// GetJobs - GET /jobs?experiment=&kind=&active=.
//
// Lists the user's jobs, and those of other users the role may list, newest
// first, without their items' results, which GET /jobs/{id} returns.
func GetJobs(w http.ResponseWriter, r *http.Request) {
	var (
		ctx   = r.Context()
		user  = middleware.UserFromContext(ctx)
		role  = middleware.RoleFromContext(ctx)
		query = r.URL.Query()
	)

	active, _ := strconv.ParseBool(query.Get("active"))

	all := jobs.List(job.Filter{
		Owner:      "",
		Experiment: query.Get("experiment"),
		Kind:       query.Get("kind"),
		Active:     active,
	})

	visible := make([]job.Job, 0, len(all))

	for _, j := range all {
		if j.Owner == user || mayListJob(role, j) {
			for i := range j.Items {
				j.Items[i].Result = nil
			}

			visible = append(visible, j)
		}
	}

	body, err := json.Marshal(map[string][]job.Job{"jobs": visible})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// GetJob - GET /jobs/{id}.
func GetJob(w http.ResponseWriter, r *http.Request) {
	j, ok := ownJob(w, r, mayGetJob)
	if !ok {
		return
	}

	writeJob(w, http.StatusOK, j)
}

// CancelJob - POST /jobs/{id}/cancel.
//
// Asks the job to stop and answers 202 with it: a queued job is canceled at
// once, a running one when its work notices.
func CancelJob(w http.ResponseWriter, r *http.Request) {
	j, ok := ownJob(w, r, mayCancelJob)
	if !ok {
		return
	}

	j, err := jobs.Cancel(j.ID)

	switch {
	case errors.Is(err, job.ErrNotFound):
		http.Error(w, "job not found", http.StatusNotFound)

		return
	case errors.Is(err, job.ErrFinished), errors.Is(err, job.ErrNotCancelable):
		http.Error(w, err.Error(), http.StatusConflict)

		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	plog.Info(plog.TypeAction, "job cancel requested", "job", j.ID, "user", middleware.UserFromContext(r.Context()))

	writeJob(w, http.StatusAccepted, j)
}

// DeleteJob - DELETE /jobs/{id}.
//
// Dismisses a finished job and deletes its artifact.
func DeleteJob(w http.ResponseWriter, r *http.Request) {
	j, ok := ownJob(w, r, mayRemoveJob)
	if !ok {
		return
	}

	err := jobs.Remove(j.ID)

	switch {
	case errors.Is(err, job.ErrNotFound):
		http.Error(w, "job not found", http.StatusNotFound)
	case errors.Is(err, job.ErrActive):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// GetJobArtifact - GET /jobs/{id}/artifact.
//
// Sends the file the job produced, with byte ranges, so an interrupted
// download resumes (see util.ServeDownload).
func GetJobArtifact(w http.ResponseWriter, r *http.Request) {
	deniedArtifact := func(j job.Job) {
		audit.Denied(r, audit.FileDownload, "downloading job artifact not allowed", audit.Fields{
			Experiment: j.Experiment, Extra: []any{"phenix.job.id", j.ID},
		})
	}

	j, ok := ownJobOr(w, r, mayGetJob, deniedArtifact)
	if !ok {
		return
	}

	if j.Artifact == nil {
		http.Error(w, "job has no artifact", http.StatusNotFound)

		return
	}

	if allowed, ok := artifactAllowed[j.Kind]; ok && !allowed(middleware.RoleFromContext(r.Context()), j) {
		deniedArtifact(j)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	served, err := util.ServeDownload(w, r, util.Download{
		Path:        j.Artifact.Path,
		Name:        j.Artifact.Name,
		ContentType: "",
		MaxSize:     0,
	})
	if err != nil {
		plog.Error(plog.TypeSystem, "serving job artifact", "job", j.ID, "err", err)
		http.Error(w, "job artifact is gone", http.StatusNotFound)

		return
	}

	if served.Started {
		if logServed, ok := artifactServed[j.Kind]; ok {
			logServed(r, j)
		}
	}
}
