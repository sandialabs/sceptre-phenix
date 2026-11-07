package cmd

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"phenix/api/config"
	"phenix/store/storetest"
	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

// completionNameFlags are the flags that name a thing that phenix knows, such
// as an experiment or a host. Each such flag needs a completion.
func completionNameFlags() []string {
	return []string{
		"alias", "disk", "experiment", "host", "interface", "kind", "namespace", "role", "scenario", "topology", "user",
	}
}

// completionFreeText lists the arguments and flags that take text that phenix
// cannot complete, with the reason. A path argument keeps the file completion
// of the shell; a new name gets no completion.
func completionFreeText() map[string]string {
	return map[string]string{
		"phenix admin role create":        "a new role name",
		"phenix admin user create":        "a new user name",
		"phenix config apply":             "paths of config files",
		"phenix config create":            "paths of config files",
		"phenix config diff":              "paths of config files",
		"phenix experiment create":        "a new experiment name",
		"phenix image create":             "a new image name",
		"phenix image inject-miniexe":     "paths of files",
		"phenix workflow apply":           "a path of a directory, or a workflow name",
		"phenix vlan blacklist add":       "a new VLAN ID",
		"phenix vm forward add --host":    "a destination host name or address",
		"phenix vm forward remove --host": "a destination host name or address",
		"phenix vm set --interface":       "an <index>=<vlan> change",
	}
}

// completionPending lists the arguments and flags that need a completion
// and do not have one yet. Remove an entry when its completion is added; the
// test fails for an entry that has one.
func completionPending() []string {
	return nil
}

// missingCompletions returns each argument and name flag of the commands
// below root that has no completion: "<command path>" for the positional
// arguments, and "<command path> --<flag>" for a flag. It skips hidden
// commands. A command takes positional arguments when its Use names them.
func missingCompletions(root *cobra.Command) []string {
	var missing []string

	var walk func(*cobra.Command)

	walk = func(cmd *cobra.Command) {
		if cmd.Hidden || cmd.Name() == "help" {
			return
		}

		if strings.Contains(strings.TrimSpace(cmd.Use), " ") && cmd.ValidArgsFunction == nil && len(cmd.ValidArgs) == 0 {
			missing = append(missing, cmd.CommandPath())
		}

		cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || !slices.Contains(completionNameFlags(), f.Name) {
				return
			}

			if _, ok := cmd.GetFlagCompletionFunc(f.Name); !ok {
				missing = append(missing, cmd.CommandPath()+" --"+f.Name)
			}
		})

		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}

	walk(root)

	return missing
}

// Each positional argument and name flag of each phenix command has a
// completion, or is in completionFreeText or completionPending.
func TestCommandsHaveCompletion(t *testing.T) { //nolint:paralleltest // LocalNonPersistentFlags changes the flags of rootCmd
	missing := missingCompletions(rootCmd)

	for _, item := range missing {
		if _, free := completionFreeText()[item]; !free && !slices.Contains(completionPending(), item) {
			t.Errorf("%s has no completion: add one from complete.go, or add it to completionFreeText with the reason", item)
		}
	}

	for _, item := range completionPending() {
		if !slices.Contains(missing, item) {
			t.Errorf("%s is in completionPending, but it has a completion or does not exist: remove it from the list", item)
		}
	}

	for item := range completionFreeText() {
		if !slices.Contains(missing, item) {
			t.Errorf("%s is in completionFreeText, but it has a completion or does not exist: remove it from the list", item)
		}
	}
}

// completionTestCase is one call of a completion function and what it gives.
type completionTestCase struct {
	name       string
	fn         cobra.CompletionFunc
	args       []string
	toComplete string
	want       []string
	directive  cobra.ShellCompDirective
}

// runCompletionTests calls the completion function of each case.
func runCompletionTests(t *testing.T, tests []completionTestCase) {
	t.Helper()

	for _, tt := range tests {
		got, directive := tt.fn(&cobra.Command{}, tt.args, tt.toComplete)

		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("%s(%q, %q) mismatch (-want +got):\n%s", tt.name, tt.args, tt.toComplete, diff)
		}

		if directive != tt.directive {
			t.Errorf("%s(%q, %q) directive = %v, want %v", tt.name, tt.args, tt.toComplete, directive, tt.directive)
		}
	}
}

func TestCompletePositional(t *testing.T) {
	t.Parallel()

	noFile := cobra.ShellCompDirectiveNoFileComp
	first, second, rest := completeValues("a1", "a2", "b"), completeValues("c"), completeValues("r", "r", "s")

	runCompletionTests(t, []completionTestCase{
		{name: "completeArgs first", fn: completeArgs(first, second), toComplete: "a", want: []string{"a1", "a2"}, directive: noFile},
		{name: "completeArgs second", fn: completeArgs(first, second), args: []string{"x"}, want: []string{"c"}, directive: noFile},
		{name: "completeArgs after the last", fn: completeArgs(first, second), args: []string{"x", "y"}, directive: noFile},
		{
			name: "completePositional rest", fn: completePositional(rest, first), args: []string{"x", "y"},
			want: []string{"r", "s"}, directive: noFile,
		},
		{name: "completePositional nil rest", fn: completePositional(nil, first), args: []string{"x"}, directive: noFile},
		{name: "completeValues no match", fn: first, toComplete: "z", directive: noFile},
		{name: "completeLogFiles", fn: completeLogFiles, toComplete: "m", want: []string{"minimega"}, directive: noFile},
	})
}

func TestCompleteExperimentsAndVMs(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	expCmdFixture(t, nil)

	noFile := cobra.ShellCompDirectiveNoFileComp

	runCompletionTests(t, []completionTestCase{
		{name: "any", fn: completeExperiments(expAny, false), want: []string{"dry", "running", "stopped"}, directive: noFile},
		{name: "running", fn: completeExperiments(expRunning, false), want: []string{"dry", "running"}, directive: noFile},
		{name: "stopped", fn: completeExperiments(expStopped, false), want: []string{"stopped"}, directive: noFile},
		{
			name: "all", fn: completeExperiments(expAny, true),
			want: []string{"all", "dry", "running", "stopped"}, directive: noFile,
		},
		{name: "all, prefix", fn: completeExperiments(expAny, true), toComplete: "s", want: []string{"stopped"}, directive: noFile},
		{name: "VMs", fn: completeVMs, args: []string{"stopped"}, want: []string{"web", "db"}, directive: noFile},
		{name: "VMs, no experiment yet", fn: completeVMs, directive: noFile},
		{name: "VMs of a missing experiment", fn: completeVMs, args: []string{"missing"}, directive: noFile},
		{name: "interfaces", fn: completeVMInterfaces, args: []string{"stopped", "db"}, want: []string{"IF0", "IF1"}, directive: noFile},
		{
			name: "interfaces, prefix", fn: completeVMInterfaces, args: []string{"stopped", "db"}, toComplete: "IF1",
			want: []string{"IF1"}, directive: noFile,
		},
		{name: "interfaces of a missing VM", fn: completeVMInterfaces, args: []string{"stopped", "none"}, directive: noFile},
	})
}

func TestCompleteStoreNames(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)
	storeConfigTestConfigs(t, "topology/alpha", "topology/beta", "image/alpine")

	for _, name := range []string{"global-admin", "vm-viewer"} {
		if _, err := config.CreateDefault("role", name); err != nil {
			t.Fatalf("config.CreateDefault(role, %s) error = %v", name, err)
		}
	}

	noFile := cobra.ShellCompDirectiveNoFileComp

	runCompletionTests(t, []completionTestCase{
		{name: "topologies", fn: completeConfigNamesOf("Topology"), want: []string{"alpha", "beta"}, directive: noFile},
		{name: "images", fn: completeConfigNamesOf("Image"), toComplete: "al", want: []string{"alpine"}, directive: noFile},
		{name: "config names", fn: completeConfigNames, toComplete: "topology/b", want: []string{"topology/beta"}, directive: noFile},
		{name: "roles", fn: completeRoles, toComplete: "Global", want: []string{"Global Admin"}, directive: noFile},
		{name: "users", fn: completeUsers, directive: noFile},
	})
}

// A completion that cannot reach minimega or the server gives no completions,
// and no error directive.
func TestCompleteUnreachable(t *testing.T) { //nolint:paralleltest // replaces minimega and common.UnixSocket
	mmtest.Use(t, nil)

	original := common.UnixSocket
	common.UnixSocket = filepath.Join(t.TempDir(), "none.sock") //nolint:reassign // no server

	t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore

	noFile := cobra.ShellCompDirectiveNoFileComp

	runCompletionTests(t, []completionTestCase{
		{name: "job IDs", fn: completeJobIDs, directive: noFile},
		{name: "cluster hosts", fn: completeClusterHosts, directive: noFile},
		{name: "snapshots", fn: completeVMSnapshots, args: []string{"exp", "web"}, directive: noFile},
		{name: "disks", fn: completeDisks, directive: noFile},
	})
}

func TestCompleteClusterHosts(t *testing.T) { //nolint:paralleltest // replaces minimega
	utilCmdUseMM(t, utilCmdMM{})

	runCompletionTests(t, []completionTestCase{
		{
			name:       "hosts",
			fn:         completeClusterHosts,
			toComplete: "comp",
			want:       []string{"compute1"},
			directive:  cobra.ShellCompDirectiveNoFileComp,
		},
	})
}

func TestCompleteSettingsKeys(t *testing.T) {
	t.Parallel()

	v := viper.New()
	v.Set("log.level", "info")
	v.Set("log.system.max-size", 1)
	v.Set("bridge-mode", "manual")

	runCompletionTests(t, []completionTestCase{
		{
			name: "keys", fn: completeSettingsKeys(v), toComplete: "log",
			want: []string{"log.level", "log.system.max-size"}, directive: cobra.ShellCompDirectiveNoFileComp,
		},
	})
}
