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

// jobAccess checks whether a role may act on a job of another user, given
// the job's RBAC resource name (see jobResourceName).
type jobAccess func(role rbac.Role, name string) bool

// The checks for each verb on another user's job, written out so the RBAC
// policy generator finds them.
//
//nolint:gochecknoglobals // constant checks
var (
	mayListJob   jobAccess = func(role rbac.Role, name string) bool { return role.Allowed("jobs", "list", name) }
	mayGetJob    jobAccess = func(role rbac.Role, name string) bool { return role.Allowed("jobs", "get", name) }
	mayCancelJob jobAccess = func(role rbac.Role, name string) bool { return role.Allowed("jobs", "update", name) }
	mayRemoveJob jobAccess = func(role rbac.Role, name string) bool { return role.Allowed("jobs", "delete", name) }
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
}

// startJob starts a job for the request's user, logs it, and answers 202 with
// the job and its location.
func startJob(w http.ResponseWriter, r *http.Request, spec job.Spec) {
	spec.Owner = middleware.UserFromContext(r.Context())

	j, err := jobs.Start(spec)
	if err != nil {
		plog.Error(plog.TypeSystem, "starting job", "kind", spec.Kind, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	plog.Info(
		plog.TypeAction, "job started",
		"job", j.ID, "kind", j.Kind, "user", j.Owner, "exp", j.Experiment, "items", len(j.Items),
	)

	w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
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

// jobResourceName is the RBAC resource name a user who did not start the job
// needs a jobs permission on: its experiment, or "*" for a job of none.
func jobResourceName(j job.Job) string {
	if j.Experiment != "" {
		return j.Experiment
	}

	return "*"
}

// ownJob returns the job the request names, if the user started it or may act
// on it. Anyone else is answered 404, as for a job that does not exist.
func ownJob(w http.ResponseWriter, r *http.Request, may jobAccess) (job.Job, bool) {
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

	if j.Owner != user && !may(role, jobResourceName(j)) {
		plog.Warn(plog.TypeSecurity, "accessing another user's job not allowed", "user", user, "job", id, "method", r.Method)
		http.Error(w, "job not found", http.StatusNotFound)

		return job.Job{}, false
	}

	return j, true
}

// GetJobs - GET /jobs?experiment=&kind=&active=.
//
// Lists the user's jobs, and those of other users the role may list, newest
// first.
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
		if j.Owner == user || mayListJob(role, jobResourceName(j)) {
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
	j, ok := ownJob(w, r, mayGetJob)
	if !ok {
		return
	}

	if j.Artifact == nil {
		http.Error(w, "job has no artifact", http.StatusNotFound)

		return
	}

	served, err := util.ServeDownload(w, r, util.Download{
		Path:        j.Artifact.Path,
		Name:        j.Artifact.Name,
		ContentType: "",
		Live:        false,
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
