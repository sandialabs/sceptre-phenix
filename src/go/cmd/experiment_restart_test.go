package cmd

import (
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"

	"phenix/api/experiment"
	"phenix/util/file"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
)

// flagDefinition is what a user sees of a flag.
type flagDefinition struct {
	Shorthand, Usage, DefValue, Type string
}

// definedFlags returns the definition of each flag of fs.
func definedFlags(fs *pflag.FlagSet) map[string]flagDefinition {
	flags := map[string]flagDefinition{}

	fs.VisitAll(func(f *pflag.Flag) {
		flags[f.Name] = flagDefinition{f.Shorthand, f.Usage, f.DefValue, f.Value.Type()}
	})

	return flags
}

// experiment restart takes every flag of experiment stop and experiment
// start, as they define them.
func TestExperimentRestartTakesStopAndStartFlags(t *testing.T) {
	t.Parallel()

	restart := definedFlags(newExperimentRestartCmd().Flags())

	for name, cmd := range map[string]func() map[string]flagDefinition{
		"stop":  func() map[string]flagDefinition { return definedFlags(newExperimentStopCmd().Flags()) },
		"start": func() map[string]flagDefinition { return definedFlags(newExperimentStartCmd().Flags()) },
	} {
		for flag, want := range cmd() {
			if diff := cmp.Diff(want, restart[flag]); diff != "" {
				t.Errorf("experiment restart --%s mismatch with experiment %s (-want +got):\n%s", flag, name, diff)
			}
		}
	}
}

// restartClusterFiles records the files that a stop deletes. Any other
// method panics, as the embedded interface is nil.
type restartClusterFiles struct {
	file.ClusterFiles

	mu      sync.Mutex
	deleted []string
}

func (f *restartClusterFiles) DeleteFile(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleted = append(f.deleted, name)

	return nil
}

// snapshots returns the deleted files that are VM snapshots.
func (f *restartClusterFiles) snapshots() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var found []string

	for _, name := range f.deleted {
		if strings.HasSuffix(name, "_snapshot") {
			found = append(found, name)
		}
	}

	return found
}

// experiment restart passes its stop flags to the stop and its start flags
// to the start.
func TestExpCmdRestartFlags(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name          string
		args          []string
		wantSnapshots bool
		wantVLANs     [2]int
		wantLogs      []string
	}{
		{name: "defaults", args: nil, wantSnapshots: true},
		{name: "keep injection snapshots", args: []string{"-K"}},
		{name: "keep injection snapshots long", args: []string{"--keep-injection-snapshots"}},
		{name: "VLAN range", args: []string{"--vlan-min", "200", "--vlan-max", "300"}, wantSnapshots: true, wantVLANs: [2]int{200, 300}},
		{
			name: "honor run periodically", args: []string{"--honor-run-periodically"}, wantSnapshots: true,
			wantLogs: []string{"honor-run-periodically flag was passed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := vmCmdExperiment(t, "running", true)
			cfg.Spec["vlans"] = map[string]any{"aliases": map[string]any{"EXP_1": 101, "EXP_2": 102}}

			expCmdUseStore(t, cfg)
			mmtest.Use(t, vmCmdMinimega{}.handle)

			files := &restartClusterFiles{}
			original := file.DefaultClusterFiles
			file.DefaultClusterFiles = files //nolint:reassign // record the deleted files

			t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore

			logs := plogtest.Capture(t)

			args := append([]string{"restart", "running", "--dry-run"}, tt.args...)
			if err := expCmdRun(args...); err != nil {
				t.Fatalf("experiment %q error = %v, want nil", args, err)
			}

			if got := files.snapshots(); (len(got) > 0) != tt.wantSnapshots {
				t.Errorf("experiment %q deleted the snapshots %q, want some deleted: %t", args, got, tt.wantSnapshots)
			}

			exp, err := experiment.Get("running")
			if err != nil {
				t.Fatalf("experiment.Get(running) error = %v", err)
			}

			if tt.wantVLANs != [2]int{} {
				if got := [2]int{exp.Spec.VLANs().Min(), exp.Spec.VLANs().Max()}; got != tt.wantVLANs {
					t.Errorf("experiment %q VLAN range = %v, want %v", args, got, tt.wantVLANs)
				}
			}

			for _, msg := range append([]string{"experiment restarted"}, tt.wantLogs...) {
				if records := logs.Records(t, plogtest.Message(msg)); len(records) != 1 {
					t.Errorf("experiment %q logged %d %q records, want 1", args, len(records), msg)
				}
			}

			if !exp.DryRun() {
				t.Errorf("experiment %q left the experiment not in dry-run mode", args)
			}
		})
	}
}
