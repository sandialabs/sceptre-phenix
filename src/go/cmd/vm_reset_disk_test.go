package cmd

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/common"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
)

// TestVMCmdResetDisk checks that vm reset-disk kills a running VM without a
// flush, replaces its disk snapshot with a new one of its base disk, and
// starts it again; and that it refuses a VM without its snapshot flag set.
func TestVMCmdResetDisk(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		snapshot  string
		want      []string // MINIMEGA for the minimega base folder
		humanized string
	}{
		{
			name: "a running VM", snapshot: "true",
			want: []string{
				"vm kill web",
				"mesh send compute1 disk snapshot /phenix/images/web.qc2 exp_web_disk-0.qcow2",
				"mesh send compute1 shell mv /phenix/images/exp_web_disk-0.qcow2 MINIMEGA/5/disk-0.qcow2",
				"vm start web",
			},
		},
		{name: "a VM without its snapshot flag", snapshot: "false", humanized: "Unable to reset the disk of the web VM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))
			logs := plogtest.Capture(t)

			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == vmCmdInfoBase {
					header := []string{"uuid", "host", "name", "id", "state", "disks", "snapshot"}
					row := []string{"uuid-web", "compute1", "web", "5", "RUNNING", "/phenix/images/web.qc2", tt.snapshot}

					return []*minicli.Response{mmtest.Tabular("compute1", header, row)}
				}

				return vmCmdMinimega{}.handle(cmd)
			})

			err := vmCmdRun("reset-disk", "exp", "web")

			switch {
			case tt.humanized == "" && err != nil:
				t.Fatalf("phenix vm reset-disk exp web error = %v", err)
			case tt.humanized != "" && !vmCmdHumanized(err, tt.humanized):
				t.Fatalf("phenix vm reset-disk exp web error = %v, want %q", err, tt.humanized)
			}

			want := make([]string, 0, len(tt.want))
			for _, base := range tt.want {
				want = append(want, strings.ReplaceAll(base, "MINIMEGA", common.MinimegaBase))
			}

			if diff := cmp.Diff(want, vmCmdChanges(received()), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("phenix vm reset-disk exp web minimega commands mismatch (-want +got):\n%s", diff)
			}

			if got, want := len(expCmdLogged(t, logs, "vm disk reset")), map[bool]int{true: 1, false: 0}[tt.humanized == ""]; got != want {
				t.Errorf("phenix vm reset-disk exp web logged %d disk reset records, want %d", got, want)
			}
		})
	}
}
