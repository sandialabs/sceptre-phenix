package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
)

// TestVMCmdMemorySnapshotDump checks that vm memory-snapshot reports a dump
// that QEMU completes, and one that QEMU reports as failed.
func TestVMCmdMemorySnapshotDump(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		status    string
		humanized string // "" for success
	}{
		{name: "completed", status: "completed"},
		{name: "failed", status: "failed", humanized: "Failed to create a memory snapshot for the web VM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))
			logs := plogtest.Capture(t)

			fake := vmCmdMinimega{rows: vmCmdRunning()}
			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if strings.Contains(cmd.Base, `"query-dump"`) {
					return []*minicli.Response{mmtest.Text("head", `{"return":{"status":"`+tt.status+`","completed":4,"total":4}}`)}
				}

				return fake.handle(cmd)
			})

			err := vmCmdRun("memory-snapshot", "exp", "web", "out.elf")

			switch {
			case tt.humanized == "" && err != nil:
				t.Fatalf("phenix vm memory-snapshot exp web out.elf error = %v", err)
			case tt.humanized != "" && !vmCmdHumanized(err, tt.humanized):
				t.Fatalf("phenix vm memory-snapshot exp web out.elf error = %v, want %q", err, tt.humanized)
			}

			dumped := slices.ContainsFunc(vmCmdChanges(received()), func(base string) bool {
				return strings.Contains(base, `"dump-guest-memory"`) && strings.Contains(base, "/images/exp/files/out.elf")
			})
			if !dumped {
				t.Errorf(
					"phenix vm memory-snapshot sent %q to minimega, want a dump of web to out.elf in the experiment files",
					vmCmdChanges(received()),
				)
			}

			created := expCmdLogged(t, logs, "vm memory snapshot created")
			if want := map[bool]int{true: 1, false: 0}[tt.humanized == ""]; len(created) != want {
				t.Errorf("phenix vm memory-snapshot logged %d created records, want %d", len(created), want)
			}
		})
	}
}
