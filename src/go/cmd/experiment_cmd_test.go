package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"

	"phenix/store"
	"phenix/types"
	"phenix/util/file"
	"phenix/util/mm"
)

// fakeClusterFiles is a test double for file.ClusterFiles that no-ops
// DeleteFile calls; any other method invocation will panic since the
// embedded interface is left nil.
type fakeClusterFiles struct {
	file.ClusterFiles
}

func (fakeClusterFiles) DeleteFile(string) error { return nil }

// fakeMM is a test double for mm.MM that no-ops the calls exercised when
// deleting a dry-run experiment; any other method invocation will panic
// since the embedded interface is left nil.
type fakeMM struct {
	mm.MM
}

func (fakeMM) Headnode() string            { return "test-headnode" }
func (fakeMM) ClearNamespace(string) error { return nil }

// newRunningDryRunExperimentConfig builds a store.Config for an experiment
// that is running in dry-run mode, so that experiment.Stop can be exercised
// in tests without requiring a real minimega cluster.
func newRunningDryRunExperimentConfig(name string, baseDir string) store.Config {
	exp := types.NewExperiment(store.ConfigMetadata{Name: name})
	exp.Spec.SetExperimentName(name)
	exp.Spec.SetBaseDir(baseDir)
	exp.Status.SetStartTime("2024-01-01T00:00:00-DRYRUN")

	return store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name: name,
		},
		Spec:   structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status: structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	}
}

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

func TestExperimentDeleteForceFlagDefaultsToFalse(t *testing.T) {
	cmd := newExperimentDeleteCmd()

	flag := cmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected delete command to have a force flag")
	}

	if flag.Shorthand != "f" {
		t.Fatalf("expected force flag shorthand to be %q, got %q", "f", flag.Shorthand)
	}

	if flag.DefValue != "false" {
		t.Fatalf("expected force flag to default to false, got %q", flag.DefValue)
	}
}

func TestExperimentDeleteSkipsRunningExperimentWithoutForce(t *testing.T) {
	name := "running-exp"
	cfg := newRunningDryRunExperimentConfig(name, t.TempDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg
		return nil
	}).Times(1)

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	root := &cobra.Command{Use: "phenix", SilenceUsage: true}
	experimentCmd := newExperimentCmd()
	experimentCmd.AddCommand(newExperimentDeleteCmd())
	root.AddCommand(experimentCmd)
	root.SetArgs([]string{"experiment", "delete", name})

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)

	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExperimentDeleteForceStopsRunningExperimentBeforeDeleting(t *testing.T) {
	name := "running-exp-force"
	cfg := newRunningDryRunExperimentConfig(name, t.TempDir())

	originalMM := mm.DefaultMM
	originalClusterFiles := file.DefaultClusterFiles
	t.Cleanup(func() {
		mm.DefaultMM = originalMM                       //nolint:reassign // restore test double
		file.DefaultClusterFiles = originalClusterFiles //nolint:reassign // restore test double
	})

	mm.DefaultMM = fakeMM{}                       //nolint:reassign // install test double
	file.DefaultClusterFiles = fakeClusterFiles{} //nolint:reassign // install test double

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg
		return nil
	}).AnyTimes()
	m.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		cfg = *c
		return nil
	}).Times(1)
	m.EXPECT().Delete(gomock.Any()).Return(nil).Times(1)

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	root := &cobra.Command{Use: "phenix", SilenceUsage: true}
	experimentCmd := newExperimentCmd()
	experimentCmd.AddCommand(newExperimentDeleteCmd())
	root.AddCommand(experimentCmd)
	root.SetArgs([]string{"experiment", "delete", "--force", name})

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)

	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
