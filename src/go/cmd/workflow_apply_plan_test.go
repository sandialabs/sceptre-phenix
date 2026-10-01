package cmd

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"phenix/api/workflow"
)

func TestRunWorkflowApplyDryRun(t *testing.T) {
	logs := captureLogs(t)
	dir := newTopologyDir(t, "helloworld", fullTopology())
	injects := t.TempDir()
	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(injects),
		plan:    workflow.Plan{Action: workflow.ActionRestart, Experiment: "helloworld", Reason: ""},
	})

	opts := testApplyOptions(fake.socket, t.TempDir())
	opts.DryRun = true

	if err := runWorkflowApply(t.Context(), opts, dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v, want the restart check skipped by -n", err)
	}

	checkRequests(t, fake, fullPreflight("helloworld")...)
	assertEmptyDir(t, injects)

	for _, msg := range []string{"config parsed", "config checked"} {
		if records := logs.all(t, msg); len(records) != 2 {
			t.Errorf("got %d %q records, want 2: -n checks every config", len(records), msg)
		}
	}

	if errs := logs.messages(t, "ERROR"); len(errs) != 0 {
		t.Errorf("dry run logged error records: %q", errs)
	}

	checkFields(t, logs.first(t, "workflow plan"), map[string]string{"action": "restart", "experiment": "helloworld"})
	checkFields(t, logs.first(t, "dry run complete; nothing was changed"), map[string]string{
		"level": "INFO", "step": "plan", "name": "helloworld", "dir": dir, "elapsed": "0s",
	})
	checkNotLogged(t, logs, "injects staged", "config upserted", "applying workflow config", "workflow apply complete")

	// --log.level debug shows request and response detail.
	requests := logs.all(t, "phenix server request")
	responses := logs.all(t, "phenix server response")

	if len(requests) != 4 || len(responses) != 4 {
		t.Fatalf("got %d request and %d response records, want 4 of each", len(requests), len(responses))
	}

	checkFields(t, requests[1], map[string]string{
		"level": "DEBUG", "method": http.MethodPost, "url": "http://unix/api/v1/workflow/configs/helloworld?dryRun=true",
	})
	checkFields(t, responses[3], map[string]string{"level": "DEBUG", "status": "200"})
}

// TestRunWorkflowApplyForwardsPendingRefs checks that the workflow dry run
// names each config as pending by the ref its config dry run answered, in
// upsert order, and that the real apply names none: only the server knows
// what a config's placeholders expand to.
func TestRunWorkflowApplyForwardsPendingRefs(t *testing.T) {
	logs := captureLogs(t)
	image := "apiVersion: phenix.sandia.gov/v1\nkind: Image\nmetadata:\n  name: base\nspec: {}\n"
	dir := newTopologyDir(t, "foo", map[string]string{
		"phenix-configs/image.yml":    image,
		"phenix-configs/scenario.yml": scenarioNamed("${BRANCH_NAME}", ""),
		"phenix-configs/topology.yml": strings.Replace(wfTopologyYAML, "name: foo", "name: ${SITE}-${BRANCH_NAME}", 1),
		workflowConfigName:            wfWorkflowYAML,
	})
	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(t.TempDir()),
		plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
		results: []workflow.ConfigResult{
			{Action: workflow.ActionCreate, Kind: kindTopology, Name: "lab-foo", DryRun: true, Pending: "Topology/lab-foo"},
			{Action: workflow.ActionUpdate, Kind: "Image", Name: "base", DryRun: true, Pending: "Image/base"},
			{Action: workflow.ActionCreate, Kind: "Scenario", Name: "foo", DryRun: true, Pending: "Scenario/foo?topology=lab-foo"},
		},
	})

	if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	checkRequests(t, fake,
		reqOptions, reqConfigDryRun("foo"), reqConfigDryRun("foo"), reqConfigDryRun("foo"),
		reqDryRun("foo", "Topology/lab-foo", "Image/base", "Scenario/foo?topology=lab-foo"),
		reqConfig("foo"), reqConfig("foo"), reqConfig("foo"), reqApply("foo", workflow.ActionCreateAndStart))

	// Each pending ref is one query value, with its "?" and "=" escaped.
	wantURL := "http://unix/api/v1/workflow/apply/foo?dryRun=true" +
		"&pending=Topology%2Flab-foo&pending=Image%2Fbase&pending=Scenario%2Ffoo%3Ftopology%3Dlab-foo"
	checkFields(t, logs.all(t, "phenix server request")[4], map[string]string{"method": http.MethodPost, "url": wantURL})

	want := []map[string]string{
		{"file": filepath.Join(configsDirName, "topology.yml"), "kind": kindTopology, "name": "lab-foo", "action": "create"},
		{"file": filepath.Join(configsDirName, "image.yml"), "kind": "Image", "name": "base", "action": "update"},
		{"file": filepath.Join(configsDirName, "scenario.yml"), "kind": "Scenario", "name": "foo", "action": "create"},
	}

	checked := logs.all(t, "config checked")
	if len(checked) != len(want) {
		t.Fatalf("got %d config checked records, want %d", len(checked), len(want))
	}

	for i := range want {
		checkFields(t, checked[i], want[i])
	}
}

func TestRunWorkflowApplyRestartNeedsForce(t *testing.T) {
	tests := []struct {
		name    string
		force   bool
		want    []string
		wantErr string
		staged  bool
	}{
		{
			name:    "without -f",
			want:    fullPreflight("helloworld"),
			wantErr: "experiment helloworld is running; apply would stop, reconfigure and restart it; re-run with -f",
		},
		{
			name:   "with -f",
			force:  true,
			want:   fullRun("helloworld", workflow.ActionRestart),
			staged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "helloworld", fullTopology())
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions(injects),
				plan:    workflow.Plan{Action: workflow.ActionRestart, Experiment: "helloworld", Reason: ""},
			})

			opts := testApplyOptions(fake.socket, t.TempDir())
			opts.Force = tt.force

			err := runWorkflowApply(t.Context(), opts, dir)

			if tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr) {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, tt.wantErr)
			}

			if tt.wantErr == "" && err != nil {
				t.Fatalf("runWorkflowApply() error = %v", err)
			}

			checkRequests(t, fake, tt.want...)

			if _, statErr := os.Stat(filepath.Join(injects, "helloworld")); (statErr == nil) != tt.staged {
				t.Errorf("injects staged = %v, want %v", statErr == nil, tt.staged)
			}
		})
	}
}

// TestRunWorkflowApplyPreflightTimeout runs against a server that holds one
// dry run until the client goes away: the run stops at once, as with an
// unreachable server, before anything is staged.
func TestRunWorkflowApplyPreflightTimeout(t *testing.T) {
	lowerPreflightTimeout(t, time.Second)

	tests := []struct {
		name    string
		hangOn  string
		want    []string
		wantErr string
	}{
		{
			name:    "config dry run",
			hangOn:  reqConfigDryRun("foo"),
			want:    []string{reqOptions, reqConfigDryRun("foo")},
			wantErr: "dry run of " + filepath.Join(configsDirName, "topology.yml") + " via ",
		},
		{
			name:    "workflow dry run",
			hangOn:  reqDryRun("foo", "Topology/foo", "Scenario/foo"),
			want:    fullPreflight("foo"),
			wantErr: "dry run of phenix.yml via ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "foo", fullTopology())
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions(injects),
				plan:    workflow.Plan{Action: workflow.ActionUpdate, Experiment: "foo", Reason: ""},
				hangOn:  tt.hangOn,
			})

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)

			want := tt.wantErr + fake.socket + ": phenix server unreachable: no answer within 1s"
			if !errors.Is(err, errServerUnreachable) || err == nil || err.Error() != want {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
			}

			checkRequests(t, fake, tt.want...)
			assertEmptyDir(t, injects)
		})
	}
}

func TestRunWorkflowApplyLogsEveryRejection(t *testing.T) {
	checks := []string{reqOptions, reqConfigDryRun("foo"), reqConfigDryRun("foo"), reqConfigDryRun("foo"), reqConfigDryRun("foo")}
	upserts := []string{reqConfig("foo"), reqConfig("foo"), reqConfig("foo"), reqConfig("foo")}
	plan := reqDryRun("foo", "Topology/foo", "Scenario/a", "Scenario/b", "Scenario/c")

	tests := []struct {
		name       string
		upsertOnly bool
		want       []string
		wantErr    string
		step       string
		staged     bool
	}{
		{
			name:    "config dry runs",
			want:    checks,
			wantErr: "3 of 4 configs rejected by the phenix server; nothing was changed",
			step:    "plan",
		},
		{
			// A config hook that runs only on the real upsert rejects them.
			name:       "upserts",
			upsertOnly: true,
			want:       slices.Concat(checks, []string{plan}, upserts),
			wantErr: "3 of 4 configs rejected by the phenix server; " +
				"already changed: injects staged, 1 config upserted; fix the cause and run the command again",
			step:   "configs",
			staged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", map[string]string{
				"phenix-injects/a.txt":        "a",
				"phenix-configs/a.yml":        scenarioNamed("a", "# reject-me\n"),
				"phenix-configs/b.yml":        scenarioNamed("b", "# reject-me\n"),
				"phenix-configs/c.yml":        scenarioNamed("c", "# fail-me\n"),
				"phenix-configs/topology.yml": wfTopologyYAML,
				workflowConfigName:            wfWorkflowYAML,
			})
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{
				options:    serverOptions(injects),
				plan:       workflow.Plan{Action: workflow.ActionCreate, Experiment: "foo", Reason: ""},
				reject:     "reject-me",
				fail:       "fail-me",
				upsertOnly: tt.upsertOnly,
			})

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, tt.wantErr)
			}

			checkRequests(t, fake, tt.want...)

			for _, line := range []string{cfgExplainedLine, cfgHintLine} {
				records := logs.all(t, line)
				if len(records) != 2 {
					t.Fatalf("got %d records of %q, want 2", len(records), line)
				}

				for i, file := range []string{"a.yml", "b.yml"} {
					checkFields(t, records[i], map[string]string{
						"level": "ERROR", "step": tt.step, "file": filepath.Join(configsDirName, file), "kind": "Scenario",
					})
				}
			}

			checkFields(t, logs.first(t, "config rejected"), map[string]string{
				"level": "ERROR",
				"step":  tt.step,
				"file":  filepath.Join(configsDirName, "c.yml"),
				"kind":  "Scenario",
				"err":   "phenix server returned 500 Internal Server Error",
			})

			if _, statErr := os.Stat(filepath.Join(injects, "foo", "a.txt")); (statErr == nil) != tt.staged {
				t.Errorf("injects staged = %v, want %v", statErr == nil, tt.staged)
			}
		})
	}
}

// TestRunWorkflowApplyRejectsDuplicateConfigs checks that two files that
// define the same config stop the run after the config dry runs, since the
// second upsert would replace the first.
func TestRunWorkflowApplyRejectsDuplicateConfigs(t *testing.T) {
	topology := strings.Replace(wfTopologyYAML, "name: foo", "name: helloworld", 1)
	clash := filepath.Join(configsDirName, "topology-old.yml") + " and " +
		filepath.Join(configsDirName, "topology.yml") + " both define Topology/helloworld"

	tests := []struct {
		name     string
		rejected bool
		want     []string
		wantErr  string
	}{
		{
			name:    "two files",
			want:    []string{reqOptions, reqConfigDryRun("helloworld"), reqConfigDryRun("helloworld")},
			wantErr: clash + "; nothing was changed",
		},
		{
			name:     "and a rejected config",
			rejected: true,
			want: []string{
				reqOptions, reqConfigDryRun("helloworld"), reqConfigDryRun("helloworld"), reqConfigDryRun("helloworld"),
			},
			wantErr: "1 of 3 configs rejected by the phenix server; " + clash + "; nothing was changed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			files := map[string]string{
				"phenix-injects/a.txt":            "a",
				"phenix-configs/topology-old.yml": topology,
				"phenix-configs/topology.yml":     topology,
				workflowConfigName:                wfWorkflowYAML,
			}

			if tt.rejected {
				files["phenix-configs/scenario.yml"] = scenarioNamed("helloworld", "# reject-me\n")
			}

			dir := newTopologyDir(t, "helloworld", files)
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions(injects),
				plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "helloworld", Reason: ""},
				reject:  "reject-me",
			})

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, tt.wantErr)
			}

			checkRequests(t, fake, tt.want...)
			assertEmptyDir(t, injects)

			// The log shows both files with the name they share.
			checked := logs.all(t, "config checked")
			if len(checked) != 2 {
				t.Fatalf("got %d config checked records, want 2", len(checked))
			}

			for i, file := range []string{"topology-old.yml", "topology.yml"} {
				checkFields(t, checked[i], map[string]string{
					"file": filepath.Join(configsDirName, file), "kind": kindTopology, "name": "helloworld", "action": "create",
				})
			}
		})
	}
}

func TestRunWorkflowApplyServerErrors(t *testing.T) {
	tests := []struct {
		name       string
		phenix     fakePhenix
		want       []string
		wantErr    string
		wantLogged bool
		staged     bool
	}{
		{
			name:       "dry run rejects the workflow config",
			phenix:     fakePhenix{planStatus: http.StatusBadRequest, planValidation: wfExplainedLine},
			want:       fullPreflight("foo"),
			wantErr:    "dry run of phenix.yml: phenix server returned 400 Bad Request: " + wfExplainedLine,
			wantLogged: true,
		},
		{
			name:    "dry run conflict",
			phenix:  fakePhenix{planStatus: http.StatusConflict},
			want:    fullPreflight("foo"),
			wantErr: "dry run of phenix.yml: phenix server returned 409 Conflict: fake message: fake cause",
		},
		{
			name:   "server ignores config dry runs",
			phenix: fakePhenix{ignoreDryRun: true},
			want:   []string{reqOptions, reqConfigDryRun("foo")},
			wantErr: "dry run of " + filepath.Join(configsDirName, "topology.yml") +
				": the phenix server did not dry-run the config: it answered 201 Created; " +
				"it may have stored it; upgrade the phenix server",
		},
		{
			name:   "plan changed before the apply",
			phenix: fakePhenix{applyStatus: http.StatusConflict},
			want:   fullRun("foo", workflow.ActionUpdate),
			wantErr: "applying phenix.yml: phenix server returned 409 Conflict: fake message: fake cause; " +
				"already changed: injects staged, 2 configs upserted; fix the cause and run the command again",
			staged: true,
		},
		{
			name:   "apply fails",
			phenix: fakePhenix{applyStatus: http.StatusInternalServerError},
			want:   fullRun("foo", workflow.ActionUpdate),
			wantErr: "applying phenix.yml: phenix server returned 500 Internal Server Error: fake message: fake cause; " +
				"already changed: injects staged, 2 configs upserted; fix the cause and run the command again",
			staged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", fullTopology())
			injects := t.TempDir()

			tt.phenix.options = serverOptions(injects)
			tt.phenix.plan = workflow.Plan{Action: workflow.ActionUpdate, Experiment: "foo", Reason: ""}
			fake := newFakePhenix(t, tt.phenix)

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, tt.wantErr)
			}

			checkRequests(t, fake, tt.want...)
			checkNotLogged(t, logs, "workflow config applied", "workflow apply complete")

			explained := logs.all(t, wfExplainedLine)
			if (len(explained) == 1) != tt.wantLogged {
				t.Fatalf("got %d records of the explained line, want logged = %v", len(explained), tt.wantLogged)
			}

			if tt.wantLogged {
				checkFields(t, explained[0], map[string]string{
					"level": "ERROR", "step": "plan", "file": workflowConfigName, "kind": "Workflow",
				})
			}

			if _, statErr := os.Stat(filepath.Join(injects, "foo")); (statErr == nil) != tt.staged {
				t.Errorf("injects staged = %v, want %v", statErr == nil, tt.staged)
			}
		})
	}
}

// TestRunWorkflowApplyChecksDryRunAnswer runs against a server whose answer
// to the workflow dry run is not a dry run's: the run stops before anything
// is staged.
func TestRunWorkflowApplyChecksDryRunAnswer(t *testing.T) {
	tests := []struct {
		name    string
		phenix  fakePhenix
		wantErr string
	}{
		{
			name: "answered as a real apply",
			phenix: fakePhenix{
				plan:          workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo", Reason: ""},
				planNotDryRun: true,
			},
			wantErr: "dry run of phenix.yml: the phenix server did not dry-run the workflow config; " +
				"it may have applied it; upgrade the phenix server",
		},
		{
			name:   "answered without an action",
			phenix: fakePhenix{plan: workflow.Plan{Action: "", Experiment: "", Reason: ""}},
			wantErr: `dry run of phenix.yml: the phenix server answered the dry run with the unknown action ""; ` +
				"use the same phenix version for the command and the server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", fullTopology())
			injects := t.TempDir()

			tt.phenix.options = serverOptions(injects)
			fake := newFakePhenix(t, tt.phenix)

			opts := testApplyOptions(fake.socket, t.TempDir())
			opts.Force = true

			err := runWorkflowApply(t.Context(), opts, dir)
			if !errors.Is(err, errNoWorkflowDryRun) || err.Error() != tt.wantErr {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, tt.wantErr)
			}

			checkRequests(t, fake, fullPreflight("foo")...)
			assertEmptyDir(t, injects)
			checkNotLogged(t, logs, "workflow plan")
		})
	}
}
