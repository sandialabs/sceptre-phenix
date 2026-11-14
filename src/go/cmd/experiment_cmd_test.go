package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"phenix/api/experiment"
	"phenix/app"
	"phenix/scheduler"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
)

const falseFlagDefault = "false"

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
		{name: "trigger", newCommand: newExperimentTriggerCmd},
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

func TestExperimentCommandAliases(t *testing.T) {
	tests := []struct {
		alias      string
		command    string
		newCommand func() *cobra.Command
	}{
		{alias: deleteAlias, command: "delete", newCommand: newExperimentDeleteCmd},
		{alias: "trig", command: "trigger-running", newCommand: newExperimentTriggerRunningCmd},
		{alias: restartAlias, command: "restart", newCommand: newExperimentRestartCmd},
		{alias: "rec", command: "reconfigure", newCommand: newExperimentReconfigureCmd},
	}

	for _, test := range tests {
		t.Run(test.alias, func(t *testing.T) {
			root := &cobra.Command{Use: "phenix"}
			experimentCmd := newExperimentCmd()
			experimentCmd.AddCommand(test.newCommand())
			root.AddCommand(experimentCmd)

			command, _, err := root.Find([]string{"exp", test.alias})
			if err != nil {
				t.Fatalf("expected alias to resolve: %v", err)
			}
			if command.Name() != test.command {
				t.Fatalf("expected alias to resolve to %q, got %q", test.command, command.Name())
			}
		})
	}
}

func TestExperimentTriggerUsageDescribesArgumentOrder(t *testing.T) {
	cmd := newExperimentTriggerCmd()

	err := cmd.ValidateArgs(nil)
	if err == nil {
		t.Fatal("expected missing argument error")
	}

	want := "Usage:\n  trigger <experiment name> <lifecycle> [<app name> ...]"
	if got := err.Error(); !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got:\n%s", want, got)
	}
}

func TestExperimentTriggerRejectsInvalidLifecycleStage(t *testing.T) {
	cmd := newExperimentTriggerCmd()

	err := cmd.ValidateArgs([]string{"test-exp", "invalid"})
	if err == nil {
		t.Fatal("expected invalid lifecycle stage error")
	}

	want := "valid lifecycles and shorthands: configure (config), pre-start (pre), post-start (post), running (run), cleanup (clean)"
	if got := err.Error(); !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got:\n%s", want, got)
	}
}

func TestExperimentTriggerAcceptsLifecycleStages(t *testing.T) {
	cmd := newExperimentTriggerCmd()

	for _, stage := range app.Actions() {
		for _, value := range []string{string(stage), stage.Shorthand()} {
			if err := cmd.ValidateArgs([]string{"test-exp", value}); err != nil {
				t.Errorf("expected lifecycle %q to be accepted, got: %v", value, err)
			}
		}
	}
}

func TestExperimentTriggerDocumentsLifecycleValues(t *testing.T) {
	cmd := newExperimentTriggerCmd()

	want := "Valid lifecycles and shorthands: configure (config), pre-start (pre), post-start (post), running (run), cleanup (clean)."
	if !strings.Contains(cmd.Long, want) {
		t.Fatalf("expected help to contain %q, got:\n%s", want, cmd.Long)
	}
}

func TestExperimentTriggerRunningIsDeprecated(t *testing.T) {
	cmd := newExperimentTriggerRunningCmd()

	want := "phenix experiment trigger <experiment name> running [<app name> ...]"
	if got := cmd.Annotations[annotationDeprecated]; got != want || !cmd.Hidden {
		t.Fatalf("trigger-running replacement = %q, hidden = %t; want %q, true", got, cmd.Hidden, want)
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

	if flag.DefValue != falseFlagDefault {
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

	originalStore := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = originalStore }) //nolint:reassign // restore test double

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
	// the stop marks each phase and app, then records the stop
	m.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		cfg = *c
		return nil
	}).Times(6)
	m.EXPECT().Delete(gomock.Any()).Return(nil).Times(1)

	originalStore := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = originalStore }) //nolint:reassign // restore test double

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

func TestExperimentStopKeepInjectionSnapshotsFlag(t *testing.T) {
	cmd := newExperimentStopCmd()
	flag := cmd.Flags().Lookup("keep-injection-snapshots")

	if flag == nil {
		t.Fatal("expected keep-injection-snapshots flag")
	}

	if flag.DefValue != falseFlagDefault {
		t.Fatalf("expected flag to default to false, got %q", flag.DefValue)
	}
	if flag.Shorthand != "K" {
		t.Fatalf("expected flag shorthand to be %q, got %q", "K", flag.Shorthand)
	}
}

// expCmdUseStore makes a new BoltDB that holds configs the phenix store until
// the test ends, and points store.endpoint at it, as completion functions
// open the store at that endpoint. Experiment files go to a temporary
// directory.
func expCmdUseStore(t *testing.T, configs ...store.Config) {
	t.Helper()

	endpoint := "bolt://" + filepath.Join(t.TempDir(), "store.bdb")

	db := new(store.BoltDB)
	if err := db.Init(store.Endpoint(endpoint)); err != nil {
		t.Fatalf("BoltDB.Init(%s) error = %v", endpoint, err)
	}

	storetest.Use(t, db)

	for i := range configs {
		if err := db.Create(&configs[i]); err != nil {
			t.Fatalf("store.Create(%s/%s) error = %v", configs[i].Kind, configs[i].Metadata.Name, err)
		}
	}

	t.Setenv("PHENIX_STORE_ENDPOINT", endpoint)

	// a completion must never open the store of the person who runs the tests
	if got := viper.GetString("store.endpoint"); got != endpoint {
		t.Fatalf("viper.GetString(store.endpoint) = %q, want %q", got, endpoint)
	}

	originalBase := common.PhenixBase
	common.PhenixBase = t.TempDir() //nolint:reassign // experiment files go to a temporary tree

	t.Cleanup(func() { common.PhenixBase = originalBase }) //nolint:reassign // restore
}

// expCmdNoServer points the commands at a unix socket that no phenix ui
// listens on, until the test ends.
func expCmdNoServer(t *testing.T) {
	t.Helper()

	expCmdUseSocket(t, filepath.Join(t.TempDir(), "none.sock"))
}

// expCmdUseSocket points the commands at the phenix ui unix socket at path,
// until the test ends.
func expCmdUseSocket(t *testing.T, path string) {
	t.Helper()

	original := common.UnixSocket
	common.UnixSocket = path //nolint:reassign // the server of the test

	t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore
}

// expCmdDryRun returns the stored config of the vmCmdExperiment named name,
// running in dry-run mode.
func expCmdDryRun(t *testing.T, name string) store.Config {
	t.Helper()

	cfg := vmCmdExperiment(t, name, true)
	cfg.Status["startTime"] = "2024-01-01T00:00:00Z-DRYRUN"

	return cfg
}

// expCmdFixture stores the experiments stopped, running and dry (running in
// dry-run mode), and answers minimega commands with fail as vmCmdMinimega
// does.
func expCmdFixture(t *testing.T, fail map[string]string) {
	t.Helper()

	expCmdUseStore(t, vmCmdExperiment(t, "stopped", false), vmCmdExperiment(t, "running", true), expCmdDryRun(t, "dry"))
	mmtest.Use(t, vmCmdMinimega{fail: fail}.handle)
}

// expCmdRun runs phenix experiment with args and returns the error that the
// command returns.
func expCmdRun(args ...string) error {
	_, err := expCmdOutput(args...)

	return err
}

// expCmdOutput runs phenix experiment with args and returns what it writes to
// the command writer.
func expCmdOutput(args ...string) (string, error) {
	stdout, _, err := expCmdOutputs(args...)

	return stdout, err
}

// expCmdOutputs runs phenix experiment with args and returns what it writes to
// the command writer and to the error writer.
func expCmdOutputs(args ...string) (string, string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	experimentCmd := newExperimentCmd()
	experimentCmd.AddCommand(
		newExperimentListCmd(), newExperimentAppsCmd(), newExperimentSchedulersCmd(), newExperimentCreateCmd(),
		newExperimentEditCmd(), newExperimentDeleteCmd(), newExperimentScheduleCmd(), newExperimentStartCmd(),
		newExperimentStopCmd(), newExperimentRestartCmd(), newExperimentReconfigureCmd(), newExperimentTriggerCmd(),
		newExperimentTriggerRunningCmd(), newExperimentScorchCmd(),
	)
	root.AddCommand(experimentCmd)
	root.SetArgs(append([]string{"experiment"}, args...))

	var output, errOutput bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&errOutput)

	_, err := root.ExecuteC()

	return output.String(), errOutput.String(), err
}

// expCmdState returns "running", "dry-run", "stopped" or "deleted" for the
// stored experiment named name.
func expCmdState(t *testing.T, name string) string {
	t.Helper()

	exp, err := experiment.Get(name)

	switch {
	case err != nil:
		return "deleted"
	case exp.DryRun():
		return "dry-run"
	case exp.Running():
		return "running"
	default:
		return "stopped"
	}
}

// expCmdLogged returns the experiment of each record that logs msg, in log
// order.
func expCmdLogged(t *testing.T, logs *plogtest.Logs, msg string) []string {
	t.Helper()

	records := logs.Records(t, plogtest.Message(msg))
	exps := make([]string, 0, len(records))

	for _, record := range records {
		exps = append(exps, record["exp"].(string))
	}

	return exps
}

// expCmdCheckErr reports how err differs from wantErr, or, when humanized is
// set, from a humanized error that starts with humanized.
func expCmdCheckErr(err error, wantErr, humanized string) string {
	switch {
	case humanized != "":
		if !vmCmdHumanized(err, humanized) {
			return fmt.Sprintf("error = %v, want %q", err, humanized)
		}
	case wantErr != "":
		if err == nil || err.Error() != wantErr {
			return fmt.Sprintf("error = %v, want %q", err, wantErr)
		}
	case err != nil:
		return fmt.Sprintf("error = %v, want nil", err)
	}

	return ""
}

// TestExpCmdNameCompletion checks that the experiment commands complete the
// names of stored experiments, and "all" where the command accepts it.
func TestExpCmdNameCompletion(t *testing.T) { //nolint:paralleltest // replaces the phenix store and sets PHENIX_STORE_ENDPOINT
	expCmdUseStore(t, vmCmdExperiment(t, "alpha", false), vmCmdExperiment(t, "beta", false))

	tests := []struct {
		name       string
		includeAll bool
		args       []string
		toComplete string
		want       []string
	}{
		{name: "every experiment and all", includeAll: true, want: []string{"all", "alpha", "beta"}},
		{name: "prefix with all", includeAll: true, toComplete: "a", want: []string{"all", "alpha"}},
		{name: "prefix without all", toComplete: "a", want: []string{"alpha"}},
		{name: "all does not match", includeAll: true, toComplete: "b", want: []string{"beta"}},
		{name: "second argument", includeAll: true, args: []string{"alpha"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, directive := expNameCompletion(tt.includeAll)(nil, tt.args, tt.toComplete)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("expNameCompletion(%t)(%q, %q) mismatch (-want +got):\n%s", tt.includeAll, tt.args, tt.toComplete, diff)
			}

			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("expNameCompletion(%t)(%q, %q) directive = %v, want NoFileComp", tt.includeAll, tt.args, tt.toComplete, directive)
			}
		})
	}
}

// TestExpCmdScheduleCompletion checks that experiment schedule completes an
// experiment name and then a scheduler name.
func TestExpCmdScheduleCompletion(t *testing.T) { //nolint:paralleltest // replaces the phenix store and sets PHENIX_STORE_ENDPOINT
	expCmdUseStore(t, vmCmdExperiment(t, "alpha", false))

	schedule := newExperimentScheduleCmd()

	tests := []struct {
		args       []string
		toComplete string
		want       []string
	}{
		{want: []string{"alpha"}},
		{args: []string{"alpha"}, toComplete: "round", want: []string{"round-robin"}},
		{args: []string{"alpha"}, toComplete: "iso", want: []string{"isolate-experiment"}},
		{args: []string{"alpha", "round-robin"}},
	}

	for _, tt := range tests {
		got, _ := schedule.ValidArgsFunction(schedule, tt.args, tt.toComplete)

		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("schedule completion(%q, %q) mismatch (-want +got):\n%s", tt.args, tt.toComplete, diff)
		}
	}
}

// TestExpCmdTriggerCompletion checks that experiment trigger completes the
// lifecycle, then the experiment, then the apps that the stage applies to.
func TestExpCmdTriggerCompletion(t *testing.T) { //nolint:paralleltest // replaces the phenix store and sets PATH
	expCmdUseStore(t, vmCmdExperiment(t, "alpha", false))
	// no phenix-app-* executables, so only the built-in apps complete
	t.Setenv("PATH", t.TempDir())

	tests := []struct {
		name       string
		stageArg   bool
		args       []string
		toComplete string
		want       []string
	}{
		{name: "experiment", stageArg: true, toComplete: "a", want: []string{"all", "alpha"}},
		{
			name:       "lifecycles",
			stageArg:   true,
			args:       []string{"alpha"},
			toComplete: "p",
			want:       []string{"pre-start", "pre", "post-start", "post"},
		},
		{name: "unknown lifecycle", stageArg: true, args: []string{"alpha", "bogus"}},
		{
			name:     "default apps of an experiment",
			stageArg: true,
			args:     []string{"alpha", "config"},
			want:     []string{"ntp", "serial", "startup", "vrouter"},
		},
		{name: "running stage skips default apps", stageArg: true, args: []string{"alpha", "running"}},
		{name: "apps of every experiment", stageArg: true, args: []string{"all", "configure"}, toComplete: "v", want: []string{"vrouter"}},
		{name: "running apps of every experiment", stageArg: true, args: []string{"all", "run"}, toComplete: "ta", want: []string{"tap"}},
		{name: "unknown experiment", stageArg: true, args: []string{"missing", "configure"}},
		{name: "trigger-running experiment", args: nil, toComplete: "al", want: []string{"all", "alpha"}},
		{name: "trigger-running apps", args: []string{"all"}, toComplete: "v"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, directive := experimentTriggerCompletion(tt.stageArg)(nil, tt.args, tt.toComplete)
			slices.Sort(got)

			want := slices.Clone(tt.want)
			slices.Sort(want)

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("experimentTriggerCompletion(%t)(%q, %q) mismatch (-want +got):\n%s", tt.stageArg, tt.args, tt.toComplete, diff)
			}

			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf(
					"experimentTriggerCompletion(%t)(%q, %q) directive = %v, want NoFileComp",
					tt.stageArg,
					tt.args,
					tt.toComplete,
					directive,
				)
			}
		})
	}
}

// TestExpCmdListAppsSchedulers checks what experiment list, apps and
// schedulers print.
func TestExpCmdListAppsSchedulers(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	expCmdUseStore(t)
	logs := plogtest.Capture(t)

	if output, err := expCmdOutput("list"); err != nil || output != "" {
		t.Errorf("experiment list with no experiments = %q, %v, want no output", output, err)
	}

	if got := logs.Records(t, plogtest.Message("no experiments available")); len(got) != 1 {
		t.Errorf("experiment list with no experiments logged %d warnings, want 1", len(got))
	}

	expCmdUseStore(t, vmCmdExperiment(t, "alpha", false), vmCmdExperiment(t, "beta", true))

	output, err := expCmdOutput("list")
	if err != nil || !vmCmdTableHas(output, "alpha") || !vmCmdTableHas(output, "beta") || !vmCmdTableHas(output, "2024-01-01T00:00:00Z") {
		t.Errorf("experiment list = %v, output:\n%s\nwant a row for alpha and for beta, which started at 2024-01-01T00:00:00Z", err, output)
	}

	lists := []struct {
		args    []string
		prefix  string
		want    []string
		wantOut []string
	}{
		{
			args:    []string{"apps"},
			prefix:  "Apps: ",
			want:    []string{"tap", "scorch"},
			wantOut: []string{"ntp", "serial", "startup", "vrouter", "user-shell"},
		},
	}

	for _, list := range lists {
		output, err := expCmdOutput(list.args...)

		names, ok := strings.CutPrefix(strings.TrimSuffix(output, "\n"), list.prefix)
		if err != nil || !ok {
			t.Errorf("experiment %s = %q, %v, want a line that starts with %q", list.args, output, err, list.prefix)

			continue
		}

		got := strings.Split(names, ", ")

		for _, name := range list.want {
			if !slices.Contains(got, name) {
				t.Errorf("experiment %s = %q, want %s in it", list.args, got, name)
			}
		}

		for _, name := range list.wantOut {
			if slices.Contains(got, name) {
				t.Errorf("experiment %s = %q, want no %s in it", list.args, got, name)
			}
		}
	}
}

// experiment schedulers prints each scheduler's name and kind, with what it
// does below it, wrapped and indented.
func TestExpCmdSchedulers(t *testing.T) { //nolint:paralleltest // shares the scheduler registry
	output, err := expCmdOutput("schedulers")
	if err != nil {
		t.Fatalf("experiment schedulers error = %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")

	var names []string

	for _, line := range lines {
		if text, ok := strings.CutPrefix(line, "    "); ok {
			if len(text) > schedulerDescriptionWidth {
				t.Errorf("description line %q is longer than %d", text, schedulerDescriptionWidth)
			}

			continue
		}

		names = append(names, line)
	}

	want := []string{"isolate-experiment (built-in)", "round-robin (built-in)", "subnet-compute (built-in)"}
	if diff := cmp.Diff(want, names[:len(want)]); diff != "" {
		t.Errorf("experiment schedulers names mismatch (-want +got):\n%s\noutput:\n%s", diff, output)
	}

	if !strings.Contains(strings.Join(strings.Fields(output), " "), "so that the VMs spread evenly") {
		t.Errorf("experiment schedulers = %q, want round-robin's description", output)
	}
}

// printSchedulers says what a user scheduler runs, and wraps a long
// description at a word.
func TestPrintSchedulers(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	printSchedulers(&out, []scheduler.Info{
		{Name: "wide", Builtin: true, Description: strings.Repeat("word ", 20) + "end."},
		{Name: "mine"},
	})

	want := "wide (built-in)\n" +
		"    " + strings.TrimSpace(strings.Repeat("word ", 14)) + "\n" +
		"    " + strings.Repeat("word ", 6) + "end.\n" +
		"mine (user)\n" +
		"    Runs phenix-scheduler-mine on this host.\n"
	if diff := cmp.Diff(want, out.String()); diff != "" {
		t.Errorf("printSchedulers mismatch (-want +got):\n%s", diff)
	}
}

// TestExpCmdCreate checks that experiment create makes an experiment from a
// stored topology or from topology and scenario files, with the flag values.
func TestExpCmdCreate(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	writeFile := func(t *testing.T, body string) string {
		t.Helper()

		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", path, err)
		}

		return path
	}

	topology := func(name string) string {
		return "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: " + name + "\nspec:\n  nodes:\n" +
			"  - type: VirtualMachine\n    general:\n      hostname: vm\n    hardware:\n      os_type: linux\n" +
			"      drives:\n      - image: u.qc2\n    network:\n      interfaces: []\n"
	}

	scenario := "apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: scn\n  annotations:\n" +
		"    topology: file-topo\nspec:\n  apps:\n  - name: tap\n  - name: soh\n"

	var (
		notConfig    = writeFile(t, "not: a config\n")
		topologyFile = writeFile(t, topology("file-topo"))
		scenarioFile = writeFile(t, scenario)
	)

	type created struct {
		Topology, Scenario, BaseDir, Bridge string
		VLANMin, VLANMax                    int
		Apps                                map[string]bool
	}

	tests := []struct {
		name      string
		args      []string
		want      created
		wantErr   string
		humanized string
	}{
		{
			name: "stored topology and flags",
			args: []string{"-t", "stored-topo", "-d", "/tmp/new", "-b", "br1", "--vlan-min", "100", "--vlan-max", "200"},
			want: created{Topology: "stored-topo", BaseDir: "/tmp/new", Bridge: "br1", VLANMin: 100, VLANMax: 200},
		},
		{
			name: "topology and scenario files",
			args: []string{"-t", topologyFile, "-s", scenarioFile, "--disabled-apps", " soh "},
			want: created{Topology: "file-topo", Scenario: "scn", Bridge: "phenix", Apps: map[string]bool{"tap": false, "soh": true}},
		},
		{name: "no topology", args: nil, wantErr: `required flag(s) "topology" not set`},
		{
			name:      "unknown topology",
			args:      []string{"-t", "missing"},
			humanized: "Unable to create the new experiment",
		},
		{
			name:      "topology file that is not a config",
			args:      []string{"-t", notConfig},
			humanized: "Unable to create configuration from " + notConfig,
		},
		{
			name:      "scenario file that does not exist",
			args:      []string{"-t", "stored-topo", "-s", "/does/not/exist.yaml"},
			humanized: "Unable to create configuration from /does/not/exist.yaml",
		},
		{
			name:    "bad annotation",
			args:    []string{"-t", "stored-topo", "--annotation", "x"},
			wantErr: `Annotation "x" is not of the form key=value`,
		},
		{
			name:    "bad node annotation",
			args:    []string{"-t", "stored-topo", "--node-annotation", "=x"},
			wantErr: `Annotation "=x" is not of the form key=value`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := store.Config{
				Version:  "phenix.sandia.gov/v1",
				Kind:     "Topology",
				Metadata: store.ConfigMetadata{Name: "stored-topo"},
			}
			if err := yaml.Unmarshal([]byte(topology("stored-topo")), &stored); err != nil {
				t.Fatalf("yaml.Unmarshal(topology) error = %v", err)
			}

			expCmdUseStore(t, stored)

			args := tt.args
			err := expCmdRun(append([]string{"create", "new"}, args...)...)

			if msg := expCmdCheckErr(err, tt.wantErr, tt.humanized); msg != "" {
				t.Fatalf("experiment create new %q %s", args, msg)
			}

			if tt.wantErr != "" || tt.humanized != "" {
				if state := expCmdState(t, "new"); state != "deleted" {
					t.Errorf("experiment create new %q stored the experiment, want none", args)
				}

				return
			}

			exp, err := experiment.Get("new")
			if err != nil {
				t.Fatalf("experiment.Get(new) error = %v", err)
			}

			got := created{
				Topology: exp.Metadata.Annotations["topology"],
				Scenario: exp.Metadata.Annotations["scenario"],
				Bridge:   exp.Spec.DefaultBridge(),
				VLANMin:  exp.Spec.VLANs().Min(),
				VLANMax:  exp.Spec.VLANs().Max(),
			}

			if tt.want.BaseDir != "" {
				got.BaseDir = exp.Spec.BaseDir()
			}

			for _, a := range exp.Apps() {
				if got.Apps == nil {
					got.Apps = map[string]bool{}
				}

				got.Apps[a.Name()] = a.Disabled()
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("experiment create new %q mismatch (-want +got):\n%s", args, diff)
			}
		})
	}
}

// TestExpCmdEdit checks that experiment edit saves what the editor changes,
// and refuses a running experiment without --force.
func TestExpCmdEdit(t *testing.T) { //nolint:paralleltest // replaces the phenix store and sets EDITOR
	editor := filepath.Join(t.TempDir(), "editor.sh")

	script := "#!/bin/sh\nsed 's/defaultBridge: phenix/defaultBridge: edited/' \"$1\" > \"$1.new\" && mv \"$1.new\" \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", editor, err)
	}

	unchanged, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no true command: %v", err)
	}

	tests := []struct {
		name       string
		editor     string
		args       []string
		wantBridge string
		wantLog    string
		humanized  string
	}{
		{name: "changed", editor: editor, args: []string{"stopped"}, wantBridge: "edited", wantLog: "experiment updated"},
		{name: "not changed", editor: unchanged, args: []string{"stopped"}, wantBridge: "phenix", wantLog: "experiment not updated"},
		{name: "running", editor: editor, args: []string{"running"}, humanized: "Unable to edit the running experiment"},
		{name: "running with force", editor: unchanged, args: []string{"running", "--force"}, wantLog: "experiment not updated"},
		{name: "missing", editor: editor, args: []string{"missing"}, humanized: "Unable to edit the missing experiment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expCmdFixture(t, nil)
			t.Setenv("EDITOR", tt.editor)

			logs := plogtest.Capture(t)

			if msg := expCmdCheckErr(expCmdRun(append([]string{"edit"}, tt.args...)...), "", tt.humanized); msg != "" {
				t.Fatalf("experiment edit %q %s", tt.args, msg)
			}

			if tt.wantLog != "" {
				if got := expCmdLogged(t, logs, tt.wantLog); !slices.Equal(got, tt.args[:1]) {
					t.Errorf("experiment edit %q logged %q for %q, want %q", tt.args, tt.wantLog, got, tt.args[:1])
				}
			}

			if tt.wantBridge != "" {
				exp, err := experiment.Get(tt.args[0])
				if err != nil {
					t.Fatalf("experiment.Get(%s) error = %v", tt.args[0], err)
				}

				if got := exp.Spec.DefaultBridge(); got != tt.wantBridge {
					t.Errorf("experiment edit %q default bridge = %q, want %q", tt.args, got, tt.wantBridge)
				}
			}
		})
	}
}

// TestExpCmdLifecycle checks which experiments delete, start, stop, restart
// and reconfigure change, what each logs, and the state they leave the
// experiments in.
func TestExpCmdLifecycle(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		args      []string
		fail      map[string]string
		wantLogs  map[string][]string
		wantState map[string]string
		wantErr   string
		humanized string
	}{
		{
			name:      "delete a stopped experiment",
			args:      []string{"delete", "stopped"},
			wantLogs:  map[string][]string{"experiment deleted": {"stopped"}},
			wantState: map[string]string{"stopped": "deleted", "running": "running", "dry": "dry-run"},
		},
		{
			name:      "delete all keeps the running experiments",
			args:      []string{"delete", "all"},
			wantLogs:  map[string][]string{"experiment deleted": {"stopped"}, "not deleting running experiment": {"dry", "running"}},
			wantState: map[string]string{"stopped": "deleted", "running": "running", "dry": "dry-run"},
		},
		{name: "delete missing", args: []string{"delete", "missing"}, humanized: "Unable to delete the missing experiment"},
		{
			name:      "start in dry-run mode",
			args:      []string{"start", "stopped", "--dry-run"},
			wantLogs:  map[string][]string{"experiment started": {"stopped"}},
			wantState: map[string]string{"stopped": "dry-run", "running": "running", "dry": "dry-run"},
		},
		{
			name: "start all skips running experiments",
			args: []string{"start", "all", "--dry-run"},
			wantLogs: map[string][]string{
				"experiment started":                      {"stopped"},
				"not starting already running experiment": {"dry", "running"},
			},
			wantState: map[string]string{"stopped": "dry-run", "running": "running", "dry": "dry-run"},
		},
		{name: "start missing", args: []string{"start", "missing"}, humanized: "Unable to start the missing experiment"},
		{
			name:      "stop",
			args:      []string{"stop", "dry"},
			wantLogs:  map[string][]string{"experiment stopped": {"dry"}},
			wantState: map[string]string{"stopped": "stopped", "running": "running", "dry": "stopped"},
		},
		{
			name: "stop all",
			args: []string{"stop", "all"},
			wantLogs: map[string][]string{
				"experiment stopped":                      {"dry", "running"},
				"not stopping already stopped experiment": {"stopped"},
			},
			wantState: map[string]string{"stopped": "stopped", "running": "stopped", "dry": "stopped"},
		},
		{
			name:      "stop when minimega cannot kill the VMs",
			args:      []string{"stop", "running"},
			fail:      map[string]string{"clear namespace running": "busy"},
			humanized: "Problem encountered while stopping the running experiment (it is still running: try again, or pass --force to mark it stopped anyway)",
			wantState: map[string]string{"running": "running"},
		},
		{
			name:      "stop with force when minimega cannot kill the VMs",
			args:      []string{"stop", "running", "--force"},
			fail:      map[string]string{"clear namespace running": "busy"},
			wantLogs:  map[string][]string{"experiment stopped": {"running"}},
			wantState: map[string]string{"running": "stopped"},
		},
		{name: "stop missing", args: []string{"stop", "missing"}, humanized: "Unable to stop the missing experiment"},
		{
			name:      "restart in dry-run mode",
			args:      []string{"restart", "dry", "--dry-run"},
			wantLogs:  map[string][]string{"experiment restarted": {"dry"}},
			wantState: map[string]string{"stopped": "stopped", "dry": "dry-run"},
		},
		{
			name:      "restart all skips stopped experiments",
			args:      []string{"restart", "all", "--dry-run"},
			wantLogs:  map[string][]string{"experiment restarted": {"dry", "running"}, "not restarting stopped experiment": {"stopped"}},
			wantState: map[string]string{"stopped": "stopped", "running": "dry-run", "dry": "dry-run"},
		},
		{
			name:      "restart when minimega cannot kill the VMs",
			args:      []string{"restart", "running"},
			fail:      map[string]string{"clear namespace running": "busy"},
			humanized: "Unable to stop the running experiment (it is still running: try again, or pass --force to mark it stopped anyway)",
			wantState: map[string]string{"running": "running"},
		},
		{
			name:      "restart with force when minimega cannot kill the VMs",
			args:      []string{"restart", "running", "--force", "--dry-run"},
			fail:      map[string]string{"clear namespace running": "busy"},
			wantLogs:  map[string][]string{"experiment restarted": {"running"}},
			wantState: map[string]string{"running": "dry-run"},
		},
		{name: "restart missing", args: []string{"restart", "missing"}, humanized: "Unable to restart the missing experiment"},
		{
			name: "reconfigure all skips running experiments",
			args: []string{"reconfigure", "all"},
			wantLogs: map[string][]string{
				"experiment reconfigured":              {"stopped"},
				"not reconfiguring running experiment": {"dry", "running"},
			},
			wantState: map[string]string{"stopped": "stopped", "running": "running", "dry": "dry-run"},
		},
		{name: "reconfigure missing", args: []string{"reconfigure", "missing"}, humanized: "Unable to reconfigure the missing experiment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expCmdFixture(t, tt.fail)
			logs := plogtest.Capture(t)

			if msg := expCmdCheckErr(expCmdRun(tt.args...), tt.wantErr, tt.humanized); msg != "" {
				t.Errorf("experiment %q %s", tt.args, msg)
			}

			for msg, want := range tt.wantLogs {
				got := expCmdLogged(t, logs, msg)
				slices.Sort(got)

				if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("experiment %q logged %q mismatch (-want +got):\n%s", tt.args, msg, diff)
				}
			}

			// each start and stop gives how long it took
			for _, msg := range []string{"experiment started", "experiment stopped"} {
				for _, r := range logs.Records(t, plogtest.Message(msg)) {
					if d, _ := r["duration"].(string); d == "" || !strings.HasSuffix(d, "s") {
						t.Errorf("experiment %q logged %s with duration %v, want a duration", tt.args, msg, r["duration"])
					}
				}
			}

			for name, want := range tt.wantState {
				if got := expCmdState(t, name); got != want {
					t.Errorf("experiment %q left %s %s, want %s", tt.args, name, got, want)
				}
			}
		})
	}
}

// TestExpCmdTriggerAndSchedule checks what experiment trigger, trigger-running,
// schedule and scorch log and return.
func TestExpCmdTriggerAndSchedule(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		args      []string
		wantLogs  map[string][]string
		wantWarn  string // the deprecation warning on stderr
		humanized string
	}{
		{
			name:     "configure",
			args:     []string{"trigger", "stopped", "config"},
			wantLogs: map[string][]string{"configure stage triggered for apps": {"stopped"}},
		},
		{
			name: "configure in the deprecated argument order",
			args: []string{"trigger", "config", "stopped"},
			wantLogs: map[string][]string{
				"configure stage triggered for apps": {"stopped"},
			},
			wantWarn: "phenix: warning: phenix experiment trigger <lifecycle> <experiment name> [<app name> ...] is " +
				"deprecated and will be removed in a future release; use phenix experiment trigger stopped configure instead.\n",
		},
		{
			name:     "running stage of a stopped experiment",
			args:     []string{"trigger", "stopped", "running"},
			wantLogs: map[string][]string{"not triggering the running stage for apps in stopped experiment": {"stopped"}},
		},
		{
			name:     "running stage of every experiment",
			args:     []string{"trigger-running", "all", "tap"},
			wantLogs: map[string][]string{"not triggering the running stage for apps in stopped experiment": {"stopped"}},
			wantWarn: "phenix: warning: phenix experiment trigger-running <experiment name> [<app name> ...] is " +
				"deprecated and will be removed in a future release; use phenix experiment trigger <experiment name> " +
				"running [<app name> ...] instead.\n",
		},
		{
			name:      "app not in the experiment",
			args:      []string{"trigger", "stopped", "cleanup", "bogus"},
			humanized: "Unable to trigger the cleanup stage for apps in the stopped experiment",
		},
		{
			name:      "missing",
			args:      []string{"trigger", "missing", "pre"},
			humanized: "Unable to trigger the pre-start stage for apps in the missing experiment",
		},
		{
			name:      "unknown scheduler",
			args:      []string{"schedule", "stopped", "bogus"},
			humanized: "Unable to schedule the stopped experiment with the bogus algorithm",
		},
		{
			name:      "scorch without configuration",
			args:      []string{"scorch", "stopped", "--run", "3"},
			humanized: "Unable to start Scorch run 3 for stopped experiment",
		},
		{
			name:      "scorch for a missing experiment",
			args:      []string{"scorch", "missing"},
			humanized: "Unable to start Scorch run 0 for missing experiment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expCmdUseStore(t, vmCmdExperiment(t, "stopped", false))
			mmtest.Use(t, nil)
			expCmdNoServer(t)
			// no phenix-scheduler-* or phenix-app-* executables
			t.Setenv("PATH", t.TempDir())

			logs := plogtest.Capture(t)

			_, stderr, err := expCmdOutputs(tt.args...)
			if msg := expCmdCheckErr(err, "", tt.humanized); msg != "" {
				t.Errorf("experiment %q %s", tt.args, msg)
			}

			if diff := cmp.Diff(tt.wantWarn, stderr); diff != "" {
				t.Errorf("experiment %q stderr mismatch (-want +got):\n%s", tt.args, diff)
			}

			for msg, want := range tt.wantLogs {
				if diff := cmp.Diff(want, expCmdLogged(t, logs, msg), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("experiment %q logged %q mismatch (-want +got):\n%s", tt.args, msg, diff)
				}
			}
		})
	}
}
