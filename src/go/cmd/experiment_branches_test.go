package cmd

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/cobra"

	"phenix/store"
	"phenix/util/plog/plogtest"
)

// The experiment commands given "all" return a humanized error when the store
// cannot list the experiments.
func TestExpCmdAllStoreError(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	tests := []struct {
		args      []string
		humanized string
	}{
		{args: []string{"list"}, humanized: "Unable to list known experiments"},
		{args: []string{"delete", "all"}, humanized: "Unable to delete all experiments"},
		{args: []string{"start", "all"}, humanized: "Unable to start all experiments"},
		{args: []string{"stop", "all"}, humanized: "Unable to stop all experiments"},
		{args: []string{"restart", "all"}, humanized: "Unable to restart all experiments"},
		{args: []string{"reconfigure", "all"}, humanized: "Unable to reconfigure all experiments"},
		{args: []string{"trigger", "all", "config"}, humanized: "Unable to trigger the configure stage for apps in all experiments"},
	}

	for _, tt := range tests {
		t.Run(tt.args[0], func(t *testing.T) {
			vlanCmdUseFailingStore(t)

			if msg := expCmdCheckErr(expCmdRun(tt.args...), "", tt.humanized); msg != "" {
				t.Errorf("experiment %q %s", tt.args, msg)
			}
		})
	}
}

// experiment reconfigure of one stopped experiment reconfigures it, and
// experiment start --honor-run-periodically starts an experiment with no
// periodic apps and returns.
func TestExpCmdOneExperiment(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		args      []string
		wantLogs  map[string][]string
		wantState string
	}{
		{
			name:      "reconfigure",
			args:      []string{"reconfigure", "stopped"},
			wantLogs:  map[string][]string{"experiment reconfigured": {"stopped"}},
			wantState: "stopped",
		},
		{
			name:      "start honoring periodic apps",
			args:      []string{"start", "stopped", "--dry-run", "--honor-run-periodically"},
			wantLogs:  map[string][]string{"experiment started": {"stopped"}, "honor-run-periodically flag was passed": nil},
			wantState: "dry-run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expCmdFixture(t, nil)
			logs := plogtest.Capture(t)

			if err := expCmdRun(tt.args...); err != nil {
				t.Fatalf("experiment %q error = %v", tt.args, err)
			}

			for msg, want := range tt.wantLogs {
				if want == nil {
					if len(logs.Records(t, plogtest.Message(msg))) != 1 {
						t.Errorf("experiment %q did not log %q once", tt.args, msg)
					}

					continue
				}

				got := expCmdLogged(t, logs, msg)
				slices.Sort(got)

				if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("experiment %q logged %q mismatch (-want +got):\n%s", tt.args, msg, diff)
				}
			}

			if got := expCmdState(t, "stopped"); got != tt.wantState {
				t.Errorf("experiment %q left stopped %s, want %s", tt.args, got, tt.wantState)
			}
		})
	}
}

// experiment schedule applies a scheduler to the VMs of an experiment, and
// logs each warning of the scheduler as a warning.
func TestExpCmdSchedule(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	expCmdUseStore(t, vmCmdExperiment(t, "stopped", false))
	utilCmdUseMM(t, utilCmdMM{})

	logs := plogtest.Capture(t)

	if err := expCmdRun("schedule", "stopped", "round-robin"); err != nil {
		t.Fatalf("experiment schedule stopped round-robin error = %v", err)
	}

	if diff := cmp.Diff([]string{"stopped"}, expCmdLogged(t, logs, "experiment scheduled")); diff != "" {
		t.Errorf("experiment schedule logged mismatch (-want +got):\n%s", diff)
	}

	records := logs.Records(t, plogtest.Message("schedule warning"))
	warnings := make([]map[string]any, 0, len(records))

	for _, r := range records {
		warnings = append(warnings, map[string]any{
			"level": r["level"], "exp": r["exp"], "algorithm": r["algorithm"], "warning": r["warning"],
		})
	}

	want := []map[string]any{{
		"level": "WARN", "exp": "stopped", "algorithm": "round-robin",
		"warning": "VM db stays on host compute2, which is not a schedulable cluster host",
	}}
	if diff := cmp.Diff(want, warnings); diff != "" {
		t.Errorf("experiment schedule warnings mismatch (-want +got):\n%s", diff)
	}
}

// The experiment name completion and the mm --namespace completion fail when
// the store holds an experiment that does not decode.
func TestExpCmdCompletionStoreError(t *testing.T) { //nolint:paralleltest // replaces the phenix store and sets PHENIX_STORE_ENDPOINT
	bad := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "bad"},
		Spec:     map[string]any{"experimentName": []any{1}},
	}

	expCmdUseStore(t, bad)

	if _, directive := expNameCompletion(true)(&cobra.Command{}, nil, ""); directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expNameCompletion() directive = %d, want %d", directive, cobra.ShellCompDirectiveNoFileComp)
	}

	if _, directive := mmNamespaceCompletion(&cobra.Command{}, nil, ""); directive != cobra.ShellCompDirectiveError {
		t.Errorf("mmNamespaceCompletion() directive = %d, want %d", directive, cobra.ShellCompDirectiveError)
	}
}

// mm --namespace completes the names of running experiments only.
func TestMMNamespaceCompletion(t *testing.T) { //nolint:paralleltest // replaces the phenix store and sets PHENIX_STORE_ENDPOINT
	expCmdUseStore(t,
		vmCmdExperiment(t, "stopped", false),
		vmCmdExperiment(t, "run-a", true),
		vmCmdExperiment(t, "run-b", true),
		vmCmdExperiment(t, "other", true),
	)

	for toComplete, want := range map[string][]string{"": {"other", "run-a", "run-b"}, "run": {"run-a", "run-b"}, "st": nil} {
		got, directive := mmNamespaceCompletion(&cobra.Command{}, nil, toComplete)
		slices.Sort(got)

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("mmNamespaceCompletion(%q) mismatch (-want +got):\n%s", toComplete, diff)
		}

		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("mmNamespaceCompletion(%q) directive = %d, want %d", toComplete, directive, cobra.ShellCompDirectiveNoFileComp)
		}
	}
}
