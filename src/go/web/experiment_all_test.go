package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/job"
	"phenix/web/rbac"
)

// useAllExperiments stores the running experiments up and other, and the
// stopped experiments down and spare.
func useAllExperiments(t *testing.T) {
	t.Helper()

	const started = "2024-01-01T00:00:00Z"

	useTestStore(t,
		namedExperiment(t, "up", started), namedExperiment(t, "other", started),
		namedExperiment(t, "down", ""), namedExperiment(t, "spare", ""),
	)
}

// itemNames are the names of the job's items, in order.
func itemNames(j job.Job) []string {
	names := make([]string, 0, len(j.Items))
	for _, item := range j.Items {
		names = append(names, item.Name)
	}

	return names
}

// postAllJob posts to the URL as alice with role, wants status, and for 202
// returns the job of the answer.
func postAllJob(t *testing.T, role rbac.Role, path string, status int) job.Job {
	t.Helper()

	resp := do(t, http.MethodPost, serveAs(t, "alice", role).URL+path, "")
	body := readBody(t, resp)

	if resp.StatusCode != status {
		t.Fatalf("POST %s = %d, want %d: %s", path, resp.StatusCode, status, body)
	}

	var j job.Job

	if status == http.StatusAccepted {
		if err := json.Unmarshal([]byte(body), &j); err != nil {
			t.Fatalf("POST %s = %s, not a job: %v", path, body, err)
		}
	}

	return j
}

// POST /experiments/all/start starts a job for the stopped experiments that
// the role may start, and refuses a role that may start none.
func TestStartAllExperiments(t *testing.T) {
	tests := map[string]struct {
		role   rbac.Role
		status int
		items  []string
	}{
		"every stopped experiment": {
			role: testRole("experiments/start", "update", "*"), status: http.StatusAccepted, items: []string{"down", "spare"},
		},
		"only those the role may start": {
			role: testRole("experiments/start", "update", "down", "up"), status: http.StatusAccepted, items: []string{"down"},
		},
		"none the role may start": {
			role: testRole("experiments/start", "update", "up"), status: http.StatusAccepted, items: []string{},
		},
		"no start permission": {role: testRole("experiments", "list", "*"), status: http.StatusForbidden},
		"only for other experiments": {
			role: testRole("experiments/start", "update", "elsewhere"), status: http.StatusForbidden,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useAllExperiments(t)

			// the job waits in the queue, so no experiment starts
			useJobsIn(t, t.TempDir(), job.WithLimits(0, 0))

			j := postAllJob(t, tt.role, "/api/v1/experiments/all/start", tt.status)
			if tt.status != http.StatusAccepted {
				return
			}

			if j.Kind != job.KindExperimentsStart || j.Experiment != "" {
				t.Errorf("start all job kind %q, experiment %q; want %q and none", j.Kind, j.Experiment, job.KindExperimentsStart)
			}

			if diff := cmp.Diff(tt.items, itemNames(j)); diff != "" {
				t.Errorf("start all job items mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// POST /experiments/all/trigger runs the stage in each experiment that the
// role may trigger, the running stage only in running ones, and skips an
// experiment that does not have the app.
func TestTriggerAllExperiments(t *testing.T) {
	tests := map[string]struct {
		role   rbac.Role
		query  string
		status int
		items  []string
	}{
		"the running stage in running experiments": {
			role: testRole("experiments/trigger", "create", "*"), query: "?apps=nosuch", status: http.StatusAccepted,
			items: []string{"up", "other"},
		},
		"another stage in every experiment": {
			role: testRole("experiments/trigger", "create", "*"), query: "?apps=nosuch&stage=configure",
			status: http.StatusAccepted, items: []string{"up", "other", "down", "spare"},
		},
		"another stage by its shorthand": {
			role: testRole("experiments/trigger", "create", "*"), query: "?apps=nosuch&stage=CONFIG",
			status: http.StatusAccepted, items: []string{"up", "other", "down", "spare"},
		},
		"only those the role may trigger": {
			role: testRole("experiments/trigger", "create", "up", "down"), query: "?apps=nosuch",
			status: http.StatusAccepted, items: []string{"up"},
		},
		"no apps": {role: testRole("experiments/trigger", "create", "*"), query: "", status: http.StatusBadRequest},
		"an unknown stage": {
			role: testRole("experiments/trigger", "create", "*"), query: "?apps=a&stage=fly", status: http.StatusBadRequest,
		},
		"no trigger permission": {role: testRole("experiments", "list", "*"), query: "?apps=a", status: http.StatusForbidden},
		"only for other experiments": {
			role: testRole("experiments/trigger", "create", "elsewhere"), query: "?apps=a", status: http.StatusForbidden,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useAllExperiments(t)

			registry := useJobs(t)

			j := postAllJob(t, tt.role, "/api/v1/experiments/all/trigger"+tt.query, tt.status)
			if tt.status != http.StatusAccepted {
				return
			}

			if j.Kind != job.KindExperimentsTrigger {
				t.Errorf("trigger all job kind %q, want %q", j.Kind, job.KindExperimentsTrigger)
			}

			done := waitJob(t, registry, j.ID)

			if diff := cmp.Diff(tt.items, itemNames(done)); diff != "" {
				t.Errorf("trigger all job items mismatch (-want +got):\n%s", diff)
			}

			for _, item := range done.Items {
				if item.Status != job.ItemSkipped {
					t.Errorf("trigger all item %s = %s, want skipped: the experiment has no app nosuch", item.Name, item.Status)
				}
			}
		})
	}
}

// A finished start all or trigger all job audits each experiment it started
// or failed to start, as the user who asked for it.
func TestAuditExperimentsJob(t *testing.T) {
	events := captureAudit(t)

	auditExperimentsJob(job.Job{
		ID: "j1", Kind: job.KindExperimentsStart, Owner: "alice",
		Items: []job.Item{
			{Name: "a", Status: job.ItemSucceeded},
			{Name: "b", Status: job.ItemFailed, Error: "boom"},
			{Name: "c", Status: job.ItemSkipped},
		},
	})

	auditExperimentsJob(job.Job{
		ID: "j2", Kind: job.KindExperimentsTrigger, Owner: "alice", Params: map[string]string{"stage": "running", "apps": "x"},
		Items: []job.Item{{Name: "a", Status: job.ItemCanceled}},
	})

	got := events()

	wantAudit(t, got, "experiment-start", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "a", "phenix.job.id": "j1",
	})
	wantAudit(t, got, "experiment-start", "failure", map[string]any{"phenix.experiment.name": "b"})
	wantAudit(t, got, "app-trigger", "failure", map[string]any{
		"phenix.experiment.name": "a", "phenix.app.stage": "running", "phenix.job.id": "j2",
	})

	skipped := map[string]any{"phenix.experiment.name": "c"}
	if n := countAudit(got, "experiment-start", "success", skipped) + countAudit(got, "experiment-start", "failure", skipped); n != 0 {
		t.Errorf("start all job audited the skipped experiment c %d times, want none", n)
	}
}
