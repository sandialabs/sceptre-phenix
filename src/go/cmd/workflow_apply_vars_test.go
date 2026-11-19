package cmd

import (
	"errors"
	"path/filepath"
	"testing"

	"phenix/api/workflow"
)

// TestRunWorkflowApplyDefersUnusedFlags checks that a -e variable that no
// local file uses is accepted when the apply dry run says that the server used
// it, and stops the run before anything changes when no one used it. Without a
// workflow config there is no apply dry run, and the preflight stops the run.
func TestRunWorkflowApplyDefersUnusedFlags(t *testing.T) {
	create := workflow.Plan{Action: workflow.ActionCreate, Experiment: "foo", Reason: ""}

	tests := []struct {
		name      string
		files     map[string]string
		dryRun    bool
		variables []string // that the apply dry run used
		want      []string
		wantErr   bool
	}{
		{
			name:      "used by the server",
			files:     fullTopology(),
			variables: []string{"INC_ONLY"},
			want:      fullRun("foo", workflow.ActionCreate),
		},
		{
			name:      "used by the server, dry run",
			files:     fullTopology(),
			dryRun:    true,
			variables: []string{"INC_ONLY"},
			want:      fullPreflight("foo"),
		},
		{
			name:    "used by no one",
			files:   fullTopology(),
			want:    fullPreflight("foo"),
			wantErr: true,
		},
		{
			name:    "used by no one, dry run",
			files:   fullTopology(),
			dryRun:  true,
			want:    fullPreflight("foo"),
			wantErr: true,
		},
		{
			name:      "the server used another variable",
			files:     fullTopology(),
			variables: []string{"OTHER"},
			want:      fullPreflight("foo"),
			wantErr:   true,
		},
		{
			name:      "no workflow config",
			files:     map[string]string{"phenix-configs/topology.yml": wfTopologyYAML},
			variables: []string{"INC_ONLY"},
			want:      []string{reqOptions},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", tt.files)
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects), plan: create, variables: tt.variables})

			opts := testApplyOptions(fake.socket, t.TempDir())
			opts.Env, opts.DryRun = []string{"INC_ONLY=x"}, tt.dryRun

			err := runWorkflowApply(t.Context(), opts, dir)

			checkRequests(t, fake, tt.want...)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("runWorkflowApply() error = %v", err)
				}

				checkNotLogged(t, logs, "no file uses the variable that -e sets")

				return
			}

			if !errors.Is(err, errVariables) {
				t.Fatalf("runWorkflowApply() error = %v, want errVariables", err)
			}

			step := stepPlan
			if tt.files[workflowConfigName] == "" {
				step = stepPreflight
			}

			checkFields(t, logs.first(t, "no file uses the variable that -e sets"), map[string]string{
				"level": "ERROR", "step": step, "variable": "INC_ONLY",
			})
			assertEmptyDir(t, injects)
		})
	}
}

// TestRunWorkflowApplyLogsServerWarnings checks that each warning of a server
// answer is logged at warning level: of a config dry run, of the workflow dry
// run and of the real apply.
func TestRunWorkflowApplyLogsServerWarnings(t *testing.T) {
	logs := captureLogs(t)
	dir := newTopologyDir(t, "foo", fullTopology())

	const (
		configWarning = "Topology/foo line 4: the request has no variable U and the placeholder has no default; " +
			"phenix kept the text ${U}"
		planWarning  = "/srv/inc.yml line 3: the request has no variable SITE; phenix used the value from the server environment"
		applyWarning = "/srv/inc.yml line 5: no variable NONE in the request or in the server environment, " +
			"and the placeholder has no default; phenix used an empty string"
	)

	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(t.TempDir()),
		plan:    workflow.Plan{Action: workflow.ActionCreate, Experiment: "foo", Reason: ""},
		results: []workflow.ConfigResult{{
			Action: workflow.ActionCreate, Kind: kindTopology, Name: "foo", DryRun: true, Pending: "Topology/foo",
			Variables: nil, Warnings: []string{configWarning},
		}},
		warnings:      []string{planWarning},
		applyWarnings: []string{applyWarning},
	})

	if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	checkFields(t, logs.first(t, configWarning), map[string]string{
		"level": "WARN", "step": stepPlan, "file": filepath.Join(configsDirName, "topology.yml"),
	})
	checkFields(t, logs.first(t, planWarning), map[string]string{"level": "WARN", "step": stepPlan, "file": workflowConfigName})
	checkFields(t, logs.first(t, applyWarning), map[string]string{"level": "WARN", "step": stepApply, "file": workflowConfigName})
}
