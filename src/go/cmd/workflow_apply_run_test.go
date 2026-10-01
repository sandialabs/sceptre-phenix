package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"phenix/api/workflow"
)

func TestWorkflowTags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	want := []string{"method=workflow", "dir=" + dir, "branch=foo", "workflow_date=20260923120000UTC"}
	if got := workflowTags(t.Context(), dir, "foo", applyTestNow()); !slices.Equal(got, want) {
		t.Errorf("workflowTags() = %q, want %q", got, want)
	}

	mountain := time.FixedZone("MST", -7*60*60)

	got := workflowTags(t.Context(), dir, "foo", time.Date(2026, time.January, 2, 3, 4, 5, 0, mountain))
	if got[3] != "workflow_date=20260102030405MST" {
		t.Errorf("workflow_date tag = %q, want workflow_date=20260102030405MST", got[3])
	}
}

func TestRunWorkflowApplySucceeds(t *testing.T) {
	logs := captureLogs(t)
	dir := newTopologyDir(t, "foo", fullTopology())
	injects := t.TempDir()
	localInjects := t.TempDir()
	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(injects),
		plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
	})

	if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, localInjects), dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	checkRequests(t, fake, fullRun("foo", workflow.ActionCreateAndStart)...)

	// The config dry runs and the upserts send the files as read, topology
	// first.
	reqs := fake.recorded()
	for _, i := range []int{1, 4} {
		if reqs[i].body != wfTopologyYAML || reqs[i+1].body != wfScenarioYAML || reqs[i].contentType != mimeYAML {
			t.Errorf("configs were not sent verbatim, topology first: %q, %q", reqs[i].body, reqs[i+1].body)
		}
	}

	if tags := reqs[3].query["tag"]; len(tags) != 0 || reqs[3].body != wfWorkflowYAML {
		t.Errorf("dry run tags = %q body = %q, want no tags and the workflow config", tags, reqs[3].body)
	}

	wantTags := []string{"method=workflow", "dir=" + dir, "branch=foo", "workflow_date=20260923120000UTC"}
	if got := reqs[6].query["tag"]; !slices.Equal(got, wantTags) || reqs[6].body != wfWorkflowYAML {
		t.Errorf("apply tags = %q body = %q, want %q and the workflow config", got, reqs[6].body, wantTags)
	}

	staged, err := os.ReadFile(filepath.Join(injects, "foo", "scripts", "hello.sh"))
	if err != nil || string(staged) != "echo hello\n" {
		t.Errorf("staged inject = %q, %v; want %q", staged, err, "echo hello\n")
	}

	assertEmptyDir(t, localInjects)

	checkFields(t, logs.first(t, "applying workflow"), map[string]string{
		"level": "INFO", "type": "SYSTEM", "step": "preflight", "dir": dir, "name": "foo",
	})
	checkFields(t, logs.first(t, "phenix server ready"), map[string]string{
		"step": "preflight", "socket": fake.socket, "injects": injects,
	})
	checkFields(t, logs.first(t, "workflow config parsed"), map[string]string{
		"step": "preflight", "file": workflowConfigName, "kind": "Workflow",
	})

	checked := logs.all(t, "config checked")
	if len(checked) != 2 {
		t.Fatalf("got %d config checked records, want 2", len(checked))
	}

	checkFields(t, checked[0], map[string]string{
		"level":  "INFO",
		"step":   "plan",
		"file":   filepath.Join(configsDirName, "topology.yml"),
		"kind":   kindTopology,
		"name":   "foo",
		"action": "create",
	})
	checkFields(t, checked[1], map[string]string{
		"step": "plan", "file": filepath.Join(configsDirName, "scenario.yml"), "kind": "Scenario", "name": "foo", "action": "create",
	})
	checkFields(t, logs.first(t, "workflow plan"), map[string]string{
		"step": "plan", "file": workflowConfigName, "action": "createAndStart", "experiment": "foo", "reason": "",
	})
	checkFields(t, logs.first(t, "injects staged"), map[string]string{
		"step": "injects", "dir": filepath.Join(injects, "foo"), "files": "1",
	})

	upserted := logs.all(t, "config upserted")
	if len(upserted) != 2 {
		t.Fatalf("got %d config upserted records, want 2", len(upserted))
	}

	checkFields(t, upserted[0], map[string]string{
		"step": "configs", "file": filepath.Join(configsDirName, "topology.yml"), "kind": kindTopology,
	})
	checkFields(t, upserted[1], map[string]string{
		"step": "configs", "file": filepath.Join(configsDirName, "scenario.yml"), "kind": "Scenario",
	})
	checkFields(t, logs.first(t, "workflow config applied"), map[string]string{
		"level": "INFO", "step": "apply", "file": workflowConfigName, "action": "createAndStart", "experiment": "foo",
	})
	checkFields(t, logs.first(t, "workflow apply complete"), map[string]string{
		"level": "INFO", "step": "apply", "name": "foo", "dir": dir, "elapsed": "0s",
	})
}

// TestRunWorkflowApplySendsFilesAsRead checks that every file reaches the
// server as read, placeholders included, since the server fills them in.
func TestRunWorkflowApplySendsFilesAsRead(t *testing.T) {
	topology := wfTopologyYAML + "  vlans: {mgmt: ${VLAN}}\n  image: ${IMG: ubuntu.qc2}\n"
	image := `{"apiVersion": "phenix.sandia.gov/v1", "kind": "Image", "metadata": {"name": "img"}, "spec": {"size": ${SIZE}}}`
	wfConfig := wfWorkflowYAML + "  vlans: {mgmt: ${VLAN}}\n"

	dir := newTopologyDir(t, "foo", map[string]string{
		"phenix-configs/topology.yml": topology,
		"phenix-configs/image.json":   image,
		"phenix-configs/scenario.yml": wfScenarioYAML,
		workflowConfigName:            wfConfig,
	})
	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(t.TempDir()),
		plan:    workflow.Plan{Action: workflow.ActionCreate, Experiment: "foo", Reason: ""},
	})

	if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	checkRequests(t, fake,
		reqOptions, reqConfigDryRun("foo"), reqConfigDryRun("foo"), reqConfigDryRun("foo"),
		reqDryRun("foo", "Topology/foo", "Image/img", "Scenario/foo"),
		reqConfig("foo"), reqConfig("foo"), reqConfig("foo"), reqApply("foo", workflow.ActionCreate))

	reqs := fake.recorded()
	for i, want := range []string{"", topology, image, wfScenarioYAML, wfConfig, topology, image, wfScenarioYAML, wfConfig} {
		if reqs[i].body != want {
			t.Errorf("request %d body = %q, want the file as read, %q", i, reqs[i].body, want)
		}
	}

	if reqs[2].contentType != mimeJSON {
		t.Errorf("image.json Content-Type = %q, want %q", reqs[2].contentType, mimeJSON)
	}
}

// TestRunWorkflowApplyServerLost loses the connection of one request, as
// when the server stops. After the first change, the server may have
// received the request, and the error says so before what was changed.
func TestRunWorkflowApplyServerLost(t *testing.T) {
	changed := "; the phenix server may have received the request; check the experiment; already changed: "

	tests := []struct {
		name    string
		drop    string
		want    []string
		wantErr string
		suffix  string
		staged  bool
	}{
		{
			name:    "during the config dry runs",
			drop:    reqConfigDryRun("foo"),
			want:    []string{reqOptions, reqConfigDryRun("foo")},
			wantErr: "dry run of " + filepath.Join(configsDirName, "topology.yml") + " via ",
		},
		{
			name:    "during the upserts",
			drop:    reqConfig("foo"),
			want:    append(fullPreflight("foo"), reqConfig("foo")),
			wantErr: "upserting " + filepath.Join(configsDirName, "topology.yml") + " via ",
			suffix:  changed + "injects staged; fix the cause and run the command again",
			staged:  true,
		},
		{
			name:    "during the apply",
			drop:    reqApply("foo", workflow.ActionUpdate),
			want:    fullRun("foo", workflow.ActionUpdate),
			wantErr: "applying phenix.yml via ",
			suffix:  changed + "injects staged, 2 configs upserted; fix the cause and run the command again",
			staged:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", fullTopology())
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions(injects),
				plan:    workflow.Plan{Action: workflow.ActionUpdate, Experiment: "foo", Reason: ""},
				drop:    tt.drop,
			})

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)

			prefix := tt.wantErr + fake.socket + ": phenix server unreachable: "
			if !errors.Is(err, errServerUnreachable) ||
				!strings.HasPrefix(err.Error(), prefix) || !strings.HasSuffix(err.Error(), tt.suffix) {
				t.Fatalf("runWorkflowApply() error = %v, want %q, then %q", err, prefix, tt.suffix)
			}

			if strings.Contains(err.Error(), "may still finish") {
				t.Errorf("runWorkflowApply() error = %v, want no note about an interrupted request", err)
			}

			checkRequests(t, fake, tt.want...)
			checkNotLogged(t, logs, "config rejected")

			if _, statErr := os.Stat(filepath.Join(injects, "foo")); (statErr == nil) != tt.staged {
				t.Errorf("injects staged = %v, want %v", statErr == nil, tt.staged)
			}
		})
	}
}

// TestRunWorkflowApplyFailsWithNothingChanged runs a directory with only a
// workflow config whose real apply is refused: the error names no change.
func TestRunWorkflowApplyFailsWithNothingChanged(t *testing.T) {
	dir := newTopologyDir(t, "foo", map[string]string{workflowConfigName: wfWorkflowYAML})
	fake := newFakePhenix(t, fakePhenix{
		options:     serverOptions(t.TempDir()),
		plan:        workflow.Plan{Action: workflow.ActionUpdate, Experiment: "foo", Reason: ""},
		applyStatus: http.StatusConflict,
	})

	err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)

	want := "applying phenix.yml: phenix server returned 409 Conflict: fake message: fake cause"
	if err == nil || err.Error() != want {
		t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
	}

	checkRequests(t, fake, reqOptions, reqDryRun("foo"), reqApply("foo", workflow.ActionUpdate))
}

// TestRunWorkflowApplySkipsMissingInputs runs directories that lack some
// inputs: each skipped step logs a warning, and so does a real apply whose
// result is none, since it changed no experiment.
func TestRunWorkflowApplySkipsMissingInputs(t *testing.T) {
	// No experiment is mapped to the branch and auto.create is not set, so the
	// server's plan is none.
	noCreate := strings.Replace(wfWorkflowYAML, "  auto:\n    create: foo\n", "", 1)

	tests := []struct {
		name    string
		files   map[string]string
		want    []string
		warns   []string
		staged  bool
		applied bool
	}{
		{
			name:   "only injects",
			files:  map[string]string{"phenix-injects/a.txt": "a"},
			want:   []string{reqOptions},
			warns:  []string{"plan", "configs", "apply"},
			staged: true,
		},
		{
			name:  "only configs",
			files: map[string]string{"phenix-configs/topology.yml": wfTopologyYAML},
			want:  []string{reqOptions, reqConfigDryRun("foo"), reqConfig("foo")},
			warns: []string{"plan", "injects", "apply"},
		},
		{
			name:    "only a workflow config",
			files:   map[string]string{workflowConfigName: noCreate},
			want:    []string{reqOptions, reqDryRun("foo"), reqApply("foo", workflow.ActionNone)},
			warns:   []string{"injects", "configs", "apply"},
			applied: true,
		},
		{
			name: "only files that are not configs in phenix-configs",
			files: map[string]string{
				"phenix-configs/.hidden.yml": "\tnot yaml",
				"phenix-configs/README.md":   "# notes\n",
				workflowConfigName:           noCreate,
			},
			want:    []string{reqOptions, reqDryRun("foo"), reqApply("foo", workflow.ActionNone)},
			warns:   []string{"injects", "configs", "apply"},
			applied: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", tt.files)
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions(injects),
				plan: workflow.Plan{
					Action:     workflow.ActionNone,
					Experiment: "",
					Reason:     "no experiment mapped and auto.create not set",
				},
			})

			if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
				t.Fatalf("runWorkflowApply() error = %v", err)
			}

			checkRequests(t, fake, tt.want...)

			var warned []string

			for _, record := range logs.records(t) {
				if record["level"] == "WARN" {
					warned = append(warned, fmt.Sprint(record["step"]))
				}
			}

			if !slices.Equal(warned, tt.warns) {
				t.Errorf("warned steps = %q, want %q", warned, tt.warns)
			}

			if _, err := os.Stat(filepath.Join(injects, "foo", "a.txt")); (err == nil) != tt.staged {
				t.Errorf("injects staged = %v, want %v", err == nil, tt.staged)
			}

			if !tt.applied {
				return
			}

			// The apply that changed nothing is a warning with the server's
			// reason, not the info record of an apply that changed something.
			checkFields(t, logs.first(t, "workflow config applied; no experiment changed"), map[string]string{
				"level":      "WARN",
				"type":       "SYSTEM",
				"step":       "apply",
				"file":       workflowConfigName,
				"action":     "none",
				"experiment": "",
				"reason":     "no experiment mapped and auto.create not set",
			})
			checkNotLogged(t, logs, "workflow config applied")
		})
	}
}

// TestRunWorkflowApplyInterrupted interrupts the run while the server holds
// a request of the configs or apply step: the server may still finish it,
// and the error says so before what was changed.
func TestRunWorkflowApplyInterrupted(t *testing.T) {
	tests := []struct {
		name   string
		hangOn string
		want   []string
		prefix string
		suffix string
	}{
		{
			name:   "while upserting",
			hangOn: reqConfig("foo"),
			want:   append(fullPreflight("foo"), reqConfig("foo")),
			prefix: "upserting " + filepath.Join(configsDirName, "topology.yml") + ": contacting phenix server: ",
			suffix: "; the phenix server may still finish the request; check the experiment" +
				"; already changed: injects staged; fix the cause and run the command again",
		},
		{
			name:   "while applying",
			hangOn: reqApply("foo", workflow.ActionCreateAndStart),
			want:   fullRun("foo", workflow.ActionCreateAndStart),
			prefix: "applying phenix.yml: contacting phenix server: ",
			suffix: "; the phenix server may still finish the request; check the experiment" +
				"; already changed: injects staged, 2 configs upserted; fix the cause and run the command again",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", fullTopology())
			injects := t.TempDir()

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions(injects),
				plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
				hangOn:  tt.hangOn,
				cancel:  cancel,
			})

			err := runWorkflowApply(ctx, testApplyOptions(fake.socket, t.TempDir()), dir)
			if !errors.Is(err, context.Canceled) || errors.Is(err, errServerUnreachable) {
				t.Fatalf("runWorkflowApply() error = %v, want context.Canceled", err)
			}

			if !strings.HasPrefix(err.Error(), tt.prefix) || !strings.HasSuffix(err.Error(), tt.suffix) {
				t.Fatalf("runWorkflowApply() error = %v, want %q, then %q", err, tt.prefix, tt.suffix)
			}

			checkRequests(t, fake, tt.want...)
			checkNotLogged(t, logs, "config rejected")

			if _, statErr := os.Stat(filepath.Join(injects, "foo", "scripts", "hello.sh")); statErr != nil {
				t.Errorf("injects not staged: %v", statErr)
			}
		})
	}
}

// TestRunWorkflowApplyInterruptedBeforeStaging interrupts the run once the
// workflow plan is logged, after the last request of the preflight.
func TestRunWorkflowApplyInterruptedBeforeStaging(t *testing.T) {
	logs := captureLogs(t)
	dir := newTopologyDir(t, "foo", fullTopology())
	injects := t.TempDir()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	actOnLog(t, "workflow plan", cancel)

	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(injects),
		plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
	})

	err := runWorkflowApply(ctx, testApplyOptions(fake.socket, t.TempDir()), dir)

	want := "interrupted before staging; nothing was changed: context canceled"
	if !errors.Is(err, context.Canceled) || err.Error() != want {
		t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
	}

	checkRequests(t, fake, fullPreflight("foo")...)
	assertEmptyDir(t, injects)
	checkNotLogged(t, logs, "staging injects")
}

// TestRunWorkflowApplyInterruptedDuringStaging interrupts the run once it
// logs that it starts staging. The staging runs to its end, and the run then
// stops before it sends anything more, so the error does not say that the
// server may still finish a request.
func TestRunWorkflowApplyInterruptedDuringStaging(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{name: "a full topology directory", files: fullTopology(), want: fullPreflight("foo")},
		{
			name:  "injects only",
			files: map[string]string{"phenix-injects/scripts/hello.sh": "echo hello\n"},
			want:  []string{reqOptions},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", tt.files)
			injects := t.TempDir()

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			actOnLog(t, "staging injects", cancel)

			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions(injects),
				plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
			})

			err := runWorkflowApply(ctx, testApplyOptions(fake.socket, t.TempDir()), dir)

			want := "interrupted after staging; nothing more was sent: context canceled" +
				"; already changed: injects staged; fix the cause and run the command again"
			if !errors.Is(err, context.Canceled) || err.Error() != want {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
			}

			checkRequests(t, fake, tt.want...)

			if _, statErr := os.Stat(filepath.Join(injects, "foo", "scripts", "hello.sh")); statErr != nil {
				t.Errorf("injects not staged: %v", statErr)
			}

			checkNotLogged(t, logs, "config upserted", "applying workflow config", "workflow apply complete")
		})
	}
}

// TestRunWorkflowApplyInterruptedBeforeARequest interrupts the run between
// two requests of the configs and apply steps, and, for a directory without
// phenix-injects, once the run skips the staging. The run stops before it
// sends the next request, and says what it had changed.
func TestRunWorkflowApplyInterruptedBeforeARequest(t *testing.T) {
	configsOnly := map[string]string{
		"phenix-configs/scenario.yml": wfScenarioYAML,
		"phenix-configs/topology.yml": wfTopologyYAML,
		workflowConfigName:            wfWorkflowYAML,
	}

	tests := []struct {
		name    string
		files   map[string]string
		on      string
		want    []string
		wantErr string
	}{
		{
			name: "between two upserts",
			on:   "config upserted",
			want: append(fullPreflight("foo"), reqConfig("foo")),
			wantErr: "interrupted before upserting " + filepath.Join(configsDirName, "scenario.yml") +
				"; already changed: injects staged, 1 config upserted; fix the cause and run the command again",
		},
		{
			name: "before the real apply, while git runs for the tags",
			on:   "leaving out the commit tag: git found no usable repository",
			want: append(fullPreflight("foo"), reqConfig("foo"), reqConfig("foo")),
			wantErr: "interrupted before applying phenix.yml" +
				"; already changed: injects staged, 2 configs upserted; fix the cause and run the command again",
		},
		{
			name:    "no phenix-injects, before the first upsert",
			files:   configsOnly,
			on:      "no phenix-injects directory; skipping",
			want:    fullPreflight("foo"),
			wantErr: "interrupted before upserting phenix-configs/topology.yml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := tt.files
			if files == nil {
				files = fullTopology()
			}

			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", files)

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			actOnLog(t, tt.on, cancel)

			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions(t.TempDir()),
				plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
			})

			err := runWorkflowApply(ctx, testApplyOptions(fake.socket, t.TempDir()), dir)
			if !errors.Is(err, context.Canceled) || err.Error() != tt.wantErr {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, tt.wantErr)
			}

			checkRequests(t, fake, tt.want...)
			checkNotLogged(t, logs, "applying workflow config", "workflow apply complete")
		})
	}
}
