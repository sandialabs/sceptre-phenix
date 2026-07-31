package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExperimentCommandsShowUsageForMissingArguments(t *testing.T) {
	tests := []struct {
		name       string
		newCommand func() *cobra.Command
	}{
		{name: "create", newCommand: newExperimentCreateCmd},
		{name: "edit", newCommand: newExperimentEditCmd},
		{name: "delete", newCommand: newExperimentDeleteCmd},
		{name: "schedule", newCommand: newExperimentScheduleCmd},
		{name: "start", newCommand: newExperimentStartCmd},
		{name: "stop", newCommand: newExperimentStopCmd},
		{name: "restart", newCommand: newExperimentRestartCmd},
		{name: "reconfigure", newCommand: newExperimentReconfigureCmd},
		{name: "trigger-running", newCommand: newExperimentTriggerRunningCmd},
		{name: "scorch", newCommand: newExperimentScorchCmd},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := &cobra.Command{
				Use:          "phenix",
				SilenceUsage: true,
			}
			experimentCmd := newExperimentCmd()
			experimentCmd.AddCommand(test.newCommand())
			root.AddCommand(experimentCmd)
			root.SetArgs([]string{"experiment", test.name})

			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)

			if _, err := root.ExecuteC(); err == nil {
				t.Fatal("expected missing argument error")
			}

			want := "Usage:\n  phenix experiment " + test.name
			if got := output.String(); !strings.Contains(got, want) {
				t.Fatalf("expected output to contain %q, got:\n%s", want, got)
			}
		})
	}
}

func TestExperimentScheduleUsageDescribesArgumentOrder(t *testing.T) {
	cmd := newExperimentScheduleCmd()

	err := cmd.ValidateArgs(nil)
	if err == nil {
		t.Fatal("expected missing argument error")
	}

	want := "Usage:\n  schedule <experiment name> <algorithm>"
	if got := err.Error(); !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got:\n%s", want, got)
	}
}
