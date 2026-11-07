package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/vm"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
	ft "phenix/web/forward/forwardtypes"
)

// vmDryRunMinimega answers as fake does, and answers the state of the VMs web
// and db: RUNNING.
func vmDryRunMinimega(fake vmCmdMinimega) mmtest.Handler {
	return func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "vm info summary" {
			for _, name := range []string{"web", "db"} {
				if strings.Contains(cmd.Raw, "name="+name) {
					return []*minicli.Response{mmtest.Tabular("head", []string{"state"}, []string{"RUNNING"})}
				}
			}

			return []*minicli.Response{mmtest.Tabular("head", []string{"state"})}
		}

		return fake.handle(cmd)
	}
}

// vmDryRunCase is a vm command line with --dry-run, and what it must print
// or the humanized error that it must return.
type vmDryRunCase struct {
	name      string
	args      []string
	want      string
	humanized string
}

// runVMDryRunCases runs each case against the running vmCmdExperiment exp,
// the stopped one stopped, and the minimega of handler, and checks that it
// sends no change to minimega and logs no change.
func runVMDryRunCases(t *testing.T, handler mmtest.Handler, tests []vmDryRunCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true), vmCmdExperiment(t, "stopped", false))
			logs := plogtest.Capture(t)
			received := mmtest.Use(t, handler)

			args := append(slicesClone(tt.args), "--dry-run")

			out, err := vmCmdOutput(args...)

			switch {
			case tt.humanized != "":
				if !vmCmdHumanized(err, tt.humanized) {
					t.Errorf("phenix vm %q error = %v, want %q", args, err, tt.humanized)
				}
			case err != nil:
				t.Errorf("phenix vm %q error = %v", args, err)
			}

			if diff := cmp.Diff(tt.want, out); diff != "" {
				t.Errorf("phenix vm %q output mismatch (-want +got):\n%s", args, diff)
			}

			if changes := vmCmdChanges(received()); len(changes) != 0 {
				t.Errorf("phenix vm %q sent %q to minimega, want no change", args, changes)
			}

			changed := func(r map[string]any) bool {
				return r["level"] == "INFO" && strings.HasPrefix(r["msg"].(string), "vm ")
			}
			if records := logs.Records(t, changed); len(records) != 0 {
				t.Errorf("phenix vm %q logged %v, want no change", args, records)
			}
		})
	}
}

// slicesClone returns a copy of s, so an append does not change s.
func slicesClone(s []string) []string { return append([]string(nil), s...) }

// kill, shutdown, reset-disk and net disconnect --dry-run check each VM as
// the change does, print what they would change, and change nothing.
func TestVMCmdPowerDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	running := vmDryRunMinimega(vmCmdMinimega{rows: vmCmdRunning()})

	runVMDryRunCases(t, running, []vmDryRunCase{
		{name: "kill", args: []string{"kill", "exp", "web"}, want: "would kill VM web in experiment exp\n"},
		{
			name: "kill every VM", args: []string{"kill", "exp", "--label", "all"},
			want: "would kill VM web in experiment exp\nwould kill VM db in experiment exp\n",
		},
		{name: "kill of a missing VM", args: []string{"kill", "exp", "nope"}, humanized: "Unable to kill the nope VM"},
		{name: "shutdown", args: []string{"shutdown", "exp", "db"}, want: "would shutdown VM db in experiment exp\n"},
		{name: "shutdown of a missing VM", args: []string{"shutdown", "exp", "nope"}, humanized: "Unable to shutdown the nope VM"},
		{name: "reset-disk", args: []string{"reset-disk", "exp", "web"}, want: "would reset the disk of VM web in experiment exp\n"},
		{
			name: "net disconnect", args: []string{"net", "disconnect", "exp", "db", "1"},
			want: "would disconnect interface 1 of VM db in experiment exp\n",
		},
		{
			name: "net disconnect of a missing interface", args: []string{"net", "disconnect", "exp", "db", "5"},
			humanized: "Unable to disconnect the interface on the db VM",
		},
		{
			name: "net disconnect of a missing VM", args: []string{"net", "disconnect", "exp", "nope", "0"},
			humanized: "Unable to disconnect the interface on the nope VM",
		},
	})

	noSnapshot := func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == vmCmdInfoBase {
			return []*minicli.Response{mmtest.Tabular("head", []string{"host", "name", "id", "state", "disks", "snapshot"},
				[]string{"head", "web", "5", "RUNNING", "/phenix/images/web.qc2", "false"})}
		}

		return nil
	}

	runVMDryRunCases(t, noSnapshot, []vmDryRunCase{
		{
			name: "reset-disk without the snapshot flag", args: []string{"reset-disk", "exp", "web"},
			humanized: "Unable to reset the disk of the web VM",
		},
	})

	runVMDryRunCases(t, nil, []vmDryRunCase{
		{
			name: "reset-disk of a VM that minimega does not have", args: []string{"reset-disk", "exp", "web"},
			humanized: "Unable to reset the disk of the web VM",
		},
	})
}

// The capture stop commands with --dry-run list the captures that they would
// stop, give the errors of a real stop, and stop nothing.
func TestVMCmdCaptureDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	fake := vmCmdMinimega{rows: vmCmdRunning(), captures: [][]string{{"db:1", "/p/db1.pcap"}}}
	db := "would stop the packet capture on interface 1 of VM db in experiment exp\n"

	runVMDryRunCases(t, fake.handle, []vmDryRunCase{
		{name: "stop VM", args: []string{"capture", "stop", "exp", "db"}, want: db},
		{name: "stop VM iface", args: []string{"capture", "stop", "exp", "db", "IF1"}, want: db},
		{
			name: "stop VM without captures", args: []string{"capture", "stop", "exp", "web"},
			humanized: "Unable to stop the packet capture(s) on the web VM",
		},
		{
			name: "stop iface without capture", args: []string{"capture", "stop", "exp", "web", "0"},
			humanized: "Unable to stop the packet capture on the web VM",
		},
		{
			name:      "stop unknown VM iface",
			args:      []string{"capture", "stop", "exp", "nope", "0"},
			humanized: "Unable to get details for the nope VM",
		},
		{name: "stop subnet", args: []string{"capture", "stop-subnet", "exp", "10.1.0.0/24"}, want: db},
		{name: "stop subnet with no capture", args: []string{"capture", "stop-subnet", "exp", "10.0.0.0/8", "--filter", "web"}},
		{
			name: "stop subnet stopped experiment", args: []string{"capture", "stop-subnet", "stopped", "10.0.0.0/24"},
			humanized: "Unable to stop the packet capture(s) on the 10.0.0.0/24 ",
		},
		{name: "stop VLAN", args: []string{"capture", "stop-vlan", "exp", "EXP_2"}, want: db},
		{name: "stop VLAN of other captures", args: []string{"capture", "stop-vlan", "exp", "EXP_1"}},
		{
			name: "stop VLAN stopped experiment", args: []string{"capture", "stop-vlan", "stopped", "EXP_2"},
			humanized: "Unable to stop the packet capture(s) on VLAN EXP_2 ",
		},
		{name: "stop all", args: []string{"capture", "stop-all", "exp"}, want: db},
		{
			name: "stop all stopped experiment", args: []string{"capture", "stop-all", "stopped"},
			humanized: "Unable to stop the packet capture(s) for stopped ",
		},
	})
}

// snapshot delete --dry-run checks the snapshot as a delete does, and deletes
// no file.
func TestVMCmdSnapshotDeleteDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store and cluster files
	for _, tt := range []struct {
		snapshot string
		want     string
		wantErr  error
	}{
		{snapshot: "web__s1", want: "would delete snapshot web__s1 of VM web in experiment exp\n"},
		{snapshot: "web__s1.hdd", want: "would delete snapshot web__s1 of VM web in experiment exp\n"},
		{snapshot: "web__s9", wantErr: vm.ErrSnapshotNotFound},
	} {
		// a delete of a file panics: vmSnapshotCmdFiles has no DeleteFile
		vmSnapshotCmdUse(t, false, &vmSnapshotCmdFiles{snapshots: []string{"web__s1"}})

		out, _, err := vmSnapshotCmdOutput("snapshot", "delete", "exp", "web", tt.snapshot, "--dry-run")
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("phenix vm snapshot delete exp web %s --dry-run error = %v, want %v", tt.snapshot, err, tt.wantErr)
		}

		if out != tt.want {
			t.Errorf("phenix vm snapshot delete exp web %s --dry-run = %q, want %q", tt.snapshot, out, tt.want)
		}
	}
}

// forward remove --dry-run finds the forward of global-admin on the server,
// and removes nothing.
func TestVMForwardRemoveDryRun(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string][]ft.Listener{"listeners": {
			{Exp: "exp", VM: "web", SrcPort: 2222, DstHost: "127.0.0.1", DstPort: 22, Owner: socketUser},
			{Exp: "exp", VM: "web", SrcPort: 8080, DstHost: "127.0.0.1", DstPort: 80, Owner: "alice"},
		}})
	})

	out, err := runVMForwardCmd(t, fake.socket, "remove", "exp", "web", "22", "--dry-run")
	if want := "would remove the port forward from port 2222 to 127.0.0.1:22 of VM web in experiment exp\n"; err != nil || out != want {
		t.Errorf("vm forward remove exp web 22 --dry-run = %q, %v, want %q", out, err, want)
	}

	for _, args := range [][]string{{"80"}, {"22", "--host", "10.0.0.5"}} {
		if _, err := runVMForwardCmd(
			t,
			fake.socket,
			append([]string{"remove", "exp", "web", "--dry-run"}, args...)...); !errors.Is(
			err,
			errNoForward,
		) {
			t.Errorf("vm forward remove exp web %q --dry-run error = %v, want %v", args, err, errNoForward)
		}
	}

	for _, r := range fake.recorded() {
		if r.method != http.MethodGet {
			t.Errorf("vm forward remove --dry-run sent %s %s, want only GET", r.method, r.path)
		}
	}
}
