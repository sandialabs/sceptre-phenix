package web

import (
	"context"
	"testing"

	"phenix/api/job"
)

// useJobs gives the test a job registry of its own, keeping artifacts in a
// temporary folder.
func useJobs(t *testing.T) *job.Registry {
	t.Helper()

	return useJobsIn(t, t.TempDir())
}

// useJobsIn is useJobs with artifacts in dir and further options; its jobs are
// logged and audited as they finish, but not broadcast.
func useJobsIn(t *testing.T, dir string, opts ...job.Option) *job.Registry {
	t.Helper()

	r := job.NewRegistry(append([]job.Option{job.WithArtifactDir(dir), job.WithNotifier(logJobEnd)}, opts...)...)

	original := jobs
	jobs = r

	t.Cleanup(func() {
		r.ShutdownContext(context.Background(), 0)

		jobs = original
	})

	return r
}
