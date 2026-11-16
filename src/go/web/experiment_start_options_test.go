package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/api/experiment/startopts"
	"phenix/api/job"
	"phenix/store"
	"phenix/types"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
)

// useStartMM answers each minimega command of the real mm.DefaultMM with an
// empty success, and returns what minimega received.
func useStartMM(t *testing.T) func() []mmtest.Command {
	t.Helper()

	return mmtest.Use(t, nil)
}

// periodicScenario gives the experiment c one app whose running stage runs every hour.
func periodicScenario(c store.Config) store.Config {
	c.Spec["scenario"] = map[string]any{
		"apps": []any{map[string]any{"name": "periodic", "runPeriodically": "1h"}},
	}

	return c
}

// A start body that is not start options, or that has a VLAN range phenix
// does not take, is refused with 400 before the start, for a name and for
// all. A role without the permission gets 403 whatever the body.
func TestStartExperimentRefusesBadOptions(t *testing.T) {
	useTestStore(t, namedExperiment(t, "down", ""))
	useJobsIn(t, t.TempDir(), job.WithLimits(0, 0))

	received := useStartMM(t)

	tests := []struct {
		name, body string
		role       []string
		want       int
	}{
		{name: "an unknown field", body: `{"dry": true}`, want: http.StatusBadRequest},
		{name: "a value of the wrong type", body: `{"vlanMin": "100"}`, want: http.StatusBadRequest},
		{name: "a VLAN ID above 4094", body: `{"vlanMax": 5000}`, want: http.StatusBadRequest},
		{name: "a range highest first", body: `{"vlanMin": 300, "vlanMax": 200}`, want: http.StatusBadRequest},
		{
			name: "a dry run with scheduled runs", body: `{"dryRun": true, "honorRunPeriodically": true}`,
			want: http.StatusBadRequest,
		},
		{name: "two objects", body: `{} {}`, want: http.StatusBadRequest},
		{
			name: "no permission", body: `{"dry": true}`, role: []string{"experiments", "list", "*"},
			want: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		for _, exp := range []string{"down", "all"} {
			t.Run(tt.name+" for "+exp, func(t *testing.T) {
				role := testRole("experiments/start", "update", "*")
				if tt.role != nil {
					role = testRole(tt.role[0], tt.role[1], tt.role[2])
				}

				path := "/api/v1/experiments/" + exp + "/start"

				resp := do(t, http.MethodPost, serveAs(t, "alice", role).URL+path, tt.body)
				if body := readBody(t, resp); resp.StatusCode != tt.want {
					t.Errorf("POST %s %s = %d, want %d: %s", path, tt.body, resp.StatusCode, tt.want, body)
				}
			})
		}
	}

	if sent := mmtest.Bases(received()); len(sent) != 0 {
		t.Errorf("refused starts sent minimega %q, want nothing", sent)
	}

	if experiment.Running("down") {
		t.Error("a refused start left the experiment running")
	}
}

// A dry run writes the minimega script, sends minimega nothing, leaves the
// experiment stopped, and answers with the script and the warnings.
func TestStartExperimentDryRun(t *testing.T) {
	c := namedExperiment(t, "down", "")
	useTestStore(t, c)
	useJobs(t)

	received := useStartMM(t)
	logs := plogtest.Capture(t)

	path := "/api/v1/experiments/down/start"
	resp := do(t, http.MethodPost, serveAs(t, "alice", testRole("experiments/start", "update", "down")).URL+path,
		`{"dryRun": true, "vlanMin": 100, "vlanMax": 200}`)

	// the audit record gives how long the start took
	if records := logs.Records(t, plogtest.Message("experiment started")); len(records) != 1 ||
		!strings.HasSuffix(fmt.Sprint(records[0]["phenix.duration"]), "s") {
		t.Errorf("POST %s dry run audit records = %v, want one with phenix.duration", path, records)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s dry run = %d, want %d: %s", path, resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	wantDocumentedBody(t, http.MethodPost, path, resp)

	var got struct {
		Experiment struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
		} `json:"experiment"`
		DryRun   bool     `json:"dryRun"`
		Warnings []string `json:"warnings"`
		MMScript string   `json:"mmScript"`
	}

	if err := json.Unmarshal([]byte(readBody(t, resp)), &got); err != nil {
		t.Fatalf("POST %s dry run body is not a start result: %v", path, err)
	}

	wantScript := filepath.Join(c.Spec["baseDir"].(string), "mm_files", "down.mm") //nolint:forcetypeassert // set above

	if got.Experiment.Name != "down" || got.Experiment.Running || !got.DryRun || got.MMScript != wantScript ||
		got.Warnings == nil {
		t.Errorf("POST %s dry run = %+v, want down stopped, dryRun, script %s and a list of warnings", path, got, wantScript)
	}

	if _, err := os.Stat(wantScript); err != nil {
		t.Errorf("dry run minimega script: %v", err)
	}

	if experiment.Running("down") {
		t.Error("a dry run left the experiment running")
	}

	// the disk check reads the files directory of minimega; a dry run changes nothing there
	for _, sent := range mmtest.Bases(received()) {
		if sent != "args" {
			t.Errorf("a dry run sent minimega %q, want only reads of its files directory", sent)
		}
	}

	if periodicJobOwner("down") != nil {
		t.Error("a dry run started the scheduled runs of the apps")
	}
}

// The job of the scheduled runs of a started experiment belongs to its owner
// and the experiment, is the one such job of the experiment, and ends when
// DELETE /experiments/{name}/trigger without apps, a job cancel or the stop
// path ends it. Without a scheduled app, no job starts unless it is asked for,
// and then it ends at once.
func TestPeriodicJob(t *testing.T) {
	tests := []struct {
		name    string
		apps    bool
		always  bool
		wantJob bool
		end     string
	}{
		{name: "ended by DELETE trigger", apps: true, wantJob: true, end: "trigger"},
		{name: "ended by a job cancel", apps: true, wantJob: true, end: "cancel"},
		{name: "ended by the stop path", apps: true, wantJob: true, end: "stop"},
		{name: "replaced by a later start", apps: true, wantJob: true, end: "later"},
		{name: "no scheduled app", apps: false, wantJob: false},
		{name: "no scheduled app, asked for", apps: false, always: true, wantJob: true, end: "itself"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := namedExperiment(t, "up", "2024-01-01T00:00:00Z")
			if tt.apps {
				c = periodicScenario(c)
			}

			useTestStore(t, c)
			useJobs(t)
			experimentStopClearCancelers(t, "up")
			t.Cleanup(func() { cancelPeriodicJob("up") })

			exp, err := experiment.Get("up")
			if err != nil {
				t.Fatalf("experiment.Get(up) error = %v", err)
			}

			started := startPeriodicJob("alice", exp, tt.always)
			if (started != nil) != tt.wantJob {
				t.Fatalf("startPeriodicJob(alice, up, %t) = %v, want a job %t", tt.always, started, tt.wantJob)
			}

			if !tt.wantJob {
				return
			}

			want := job.Job{Kind: job.KindExperimentsPeriodic, Owner: "alice", Experiment: "up", Cancelable: true}
			got := job.Job{Kind: started.Kind, Owner: started.Owner, Experiment: started.Experiment, Cancelable: started.Cancelable}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("scheduled-runs job mismatch (-want +got):\n%s", diff)
			}

			wantStatus := job.StatusSucceeded

			switch tt.end {
			case "trigger":
				trigger := serveAs(t, "alice", testRole("experiments/trigger", "delete", "up")).URL +
					"/api/v1/experiments/up/trigger"
				if resp := do(t, http.MethodDelete, trigger, ""); resp.StatusCode != http.StatusNoContent {
					t.Fatalf("DELETE %s = %d, want %d: %s", trigger, resp.StatusCode, http.StatusNoContent, readBody(t, resp))
				}

				wantStatus = job.StatusCanceled
			case "cancel":
				if _, err := jobs.Cancel(started.ID); err != nil {
					t.Fatalf("Cancel(%s) error = %v", started.ID, err)
				}

				wantStatus = job.StatusCanceled
			case "stop":
				cancels, wg := takeCancelers("up")
				for _, c := range cancels {
					c.cancel()
				}

				wg.Wait()
			case "later":
				later := startPeriodicJob("bob", exp, false)
				if later == nil || periodicJobOwner("up") == nil || *periodicJobOwner("up") != "bob" {
					t.Fatalf("a later startPeriodicJob(bob, up) = %v, want the one job of up, of bob", later)
				}

				wantStatus = job.StatusCanceled
			}

			if ended := waitJob(t, jobs, started.ID); ended.Status != wantStatus {
				t.Errorf("scheduled-runs job ended %s, want %s", ended.Status, wantStatus)
			}

			if tt.end != "later" && periodicJobOwner("up") != nil {
				t.Errorf("experiment up still has a scheduled-runs job after it ended")
			}
		})
	}
}

// sameStart ends the scheduled runs once the experiment stops, starts again
// or is deleted.
func TestSameStart(t *testing.T) {
	exp := func(started string) store.Config { return namedExperiment(t, "up", started) }

	tests := []struct {
		name   string
		stored []store.Config
		want   bool
	}{
		{name: "the same start", stored: []store.Config{exp("2024-01-01T00:00:00Z")}, want: true},
		{name: "stopped", stored: []store.Config{exp("")}, want: false},
		{name: "started again", stored: []store.Config{exp("2024-02-02T00:00:00Z")}, want: false},
		{name: "deleted", stored: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTestStore(t, tt.stored...)

			running := namedExperiment(t, "up", "2024-01-01T00:00:00Z")

			decoded, err := types.DecodeExperimentFromConfig(running)
			if err != nil {
				t.Fatalf("DecodeExperimentFromConfig(up) error = %v", err)
			}

			if got := sameStart(decoded); got != tt.want {
				t.Errorf("sameStart(up) with %s = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}

// writeStartAnswer answers 202 with the job of the scheduled runs when
// honorRunPeriodically is true, a StartResult with dryRun or
// treatMinimegaErrorsAsWarnings, and the experiment otherwise, each as
// openapi.yml documents it.
func TestWriteStartAnswer(t *testing.T) {
	useTestStore(t, periodicScenario(namedExperiment(t, "up", "2024-01-01T00:00:00Z")))
	useJobs(t)
	experimentStopClearCancelers(t, "up")
	t.Cleanup(func() { cancelPeriodicJob("up") })

	exp, err := experiment.Get("up")
	if err != nil {
		t.Fatalf("experiment.Get(up) error = %v", err)
	}

	periodic := startPeriodicJob("alice", exp, false)
	if periodic == nil {
		t.Fatal("startPeriodicJob(alice, up) = nil, want a job")
	}

	yes := true

	tests := []struct {
		name       string
		opts       startopts.Options
		wantStatus int
		wantKeys   []string
	}{
		{name: "no options", wantStatus: http.StatusOK, wantKeys: []string{"name"}},
		{
			name: "minimega errors as warnings", opts: startopts.Options{TreatMinimegaErrorsAsWarnings: true},
			wantStatus: http.StatusOK, wantKeys: []string{"experiment", "warnings"},
		},
		{
			name: "scheduled runs asked for", opts: startopts.Options{HonorRunPeriodically: &yes},
			wantStatus: http.StatusAccepted, wantKeys: []string{"id", "kind"},
		},
	}

	const path = "/api/v1/experiments/up/start"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			outcome := startOutcome{exp: exp, warnings: []string{"minimega: no"}, mmScript: "", periodic: periodic}

			if err := writeStartAnswer(t.Context(), rec, "up", tt.opts, outcome); err != nil {
				t.Fatalf("writeStartAnswer(%s) error = %v", tt.name, err)
			}

			resp := rec.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("writeStartAnswer(%s) status = %d, want %d", tt.name, resp.StatusCode, tt.wantStatus)
			}

			wantDocumentedBody(t, http.MethodPost, path, resp)

			var body map[string]any
			if err := json.Unmarshal([]byte(readBody(t, resp)), &body); err != nil {
				t.Fatalf("writeStartAnswer(%s) body is not a JSON object: %v", tt.name, err)
			}

			for _, key := range tt.wantKeys {
				if _, ok := body[key]; !ok {
					t.Errorf("writeStartAnswer(%s) body has no %s: %v", tt.name, key, body)
				}
			}

			if tt.wantStatus == http.StatusAccepted && !strings.HasSuffix(resp.Header.Get("Location"), "/jobs/"+periodic.ID) {
				t.Errorf("writeStartAnswer(%s) Location = %q, want the job %s", tt.name, resp.Header.Get("Location"), periodic.ID)
			}
		})
	}
}
