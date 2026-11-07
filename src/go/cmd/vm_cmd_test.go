package cmd

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
)

// vmCmdNode returns a topology node for a VM named host, with one drive and
// an interface on each VLAN.
func vmCmdNode(host string, labels map[string]string, vlans ...string) map[string]any {
	ifaces := make([]map[string]any, 0, len(vlans))
	for i, vlan := range vlans {
		ifaces = append(ifaces, map[string]any{"name": "IF" + strconv.Itoa(i), "type": "ethernet", "proto": "dhcp", "vlan": vlan})
	}

	return map[string]any{
		"type":   "VirtualMachine",
		"labels": labels,
		"general": map[string]any{
			"hostname": host,
		},
		"hardware": map[string]any{
			"vcpus":   1,
			"memory":  512,
			"os_type": "linux",
			"drives":  []map[string]any{{"image": host + ".qc2", "inject_partition": 1}},
		},
		"network": map[string]any{"interfaces": ifaces},
	}
}

// vmCmdExperiment returns the stored config of an experiment named name with
// the VMs web (label web-server=1, scheduled on compute1) and db (label
// db-server=1, scheduled on compute2). A running experiment has a start time.
func vmCmdExperiment(t *testing.T, name string, running bool) store.Config {
	t.Helper()

	status := map[string]any{}
	if running {
		status["startTime"] = "2024-01-01T00:00:00Z"
	}

	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec: map[string]any{
			"experimentName": name,
			"baseDir":        t.TempDir(),
			"defaultBridge":  "phenix",
			"deployMode":     "all",
			"schedules":      map[string]any{"web": "compute1", "db": "compute2"},
			"topology": map[string]any{
				"nodes": []map[string]any{
					vmCmdNode("web", map[string]string{"web-server": "1"}, "EXP_1"),
					vmCmdNode("db", map[string]string{"db-server": "1"}, "EXP_1", "EXP_2"),
				},
			},
		},
		Status: status,
	}
}

// vmCmdUseStore makes a new BoltDB that holds configs the phenix store until
// the test ends.
func vmCmdUseStore(t *testing.T, configs ...store.Config) {
	t.Helper()

	db := storetest.UseBolt(t)

	for i := range configs {
		if err := db.Create(&configs[i]); err != nil {
			t.Fatalf("store.Create(%s/%s) error = %v", configs[i].Kind, configs[i].Metadata.Name, err)
		}
	}
}

// vmCmdRun runs phenix vm with args and returns the error that the command
// returns.
func vmCmdRun(args ...string) error {
	_, err := vmCmdOutput(args...)

	return err
}

// vmCmdOutput runs phenix vm with args and returns what it writes to the
// command writer.
func vmCmdOutput(args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	vmCmd := newVMCmd()
	vmCmd.AddCommand(
		newVMInfoCmd(), newVMPauseCmd(), newVMResumeCmd(), newVMRestartCmd(), newVMResetDiskCmd(),
		newVMRedeployCmd(), newVMShutdownCmd(), newVMKillCmd(), newVMSetCmd(), newVMNetCmd(),
		newVMCaptureCmd(), newVMMemorySnapshotCmd(), newVMMountCmd(), newVMUnmountCmd(), newVMSnapshotCmd(), newVMCommitCmd(),
	)
	root.AddCommand(vmCmd)
	root.SetArgs(append([]string{"vm"}, args...))

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(io.Discard)

	_, err := root.ExecuteC()

	return output.String(), err
}

// vmCmdInfoBase is the base of the minimega command that lists VMs.
const vmCmdInfoBase = "vm info"

// vmCmdMinimega answers as a minimega that runs the VMs of rows (each a
// vmCmdInfoRow) and the captures of captures (each a VM:index and a path).
// A command whose base is a key of fail gets that error message.
type vmCmdMinimega struct {
	rows     [][]string
	captures [][]string
	fail     map[string]string
}

// vmCmdInfoRow returns the vm info row of a running VM named name on the
// VLANs vlans with the addresses ips, both in minimega list form.
func vmCmdInfoRow(name, vlans, ips string) []string {
	return []string{"uuid-" + name, "head", name, "RUNNING", "60", vlans, "[]", ips, "512", "1", name + ".qc2", "true", "", "{}"}
}

// vmCmdRunning returns the vm info rows of the VMs of vmCmdExperiment.
func vmCmdRunning() [][]string {
	return [][]string{
		vmCmdInfoRow("web", "[EXP_1]", "[10.0.0.5]"),
		vmCmdInfoRow("db", "[EXP_1, EXP_2]", "[10.0.0.6, 10.1.0.6]"),
	}
}

// handle is the mmtest.Handler of f.
func (f vmCmdMinimega) handle(cmd mmtest.Command) []*minicli.Response {
	if msg, ok := f.fail[cmd.Base]; ok {
		return []*minicli.Response{mmtest.Error("head", msg)}
	}

	switch cmd.Base {
	case vmCmdInfoBase:
		header := []string{
			"uuid", "host", "name", "state", "uptime", "vlan", "tap", "ip",
			"memory", "vcpus", "disks", "snapshot", "cdrom", "tags",
		}

		return []*minicli.Response{mmtest.Tabular("head", header, f.rows...)}
	case "capture":
		return []*minicli.Response{mmtest.Tabular("head", []string{"interface", "path"}, f.captures...)}
	}

	return nil
}

// vmCmdChanges returns the bases of the commands in cmds that change minimega
// state, with the time in capture file names replaced by TIME.
func vmCmdChanges(cmds []mmtest.Command) []string {
	var (
		reads     = regexp.MustCompile(`^(cc client|vm info|vm info summary|capture|host|args|version|mesh send head disk info .*)$`)
		timestamp = regexp.MustCompile(`_\d{8}_\d{4}\.pcap$`)
		changes   []string
	)

	for _, base := range mmtest.Bases(cmds) {
		if !reads.MatchString(base) {
			changes = append(changes, timestamp.ReplaceAllString(base, "_TIME.pcap"))
		}
	}

	return changes
}

// vmCmdHumanized reports whether err is a humanized error that starts with
// msg, as the CLI prints it.
func vmCmdHumanized(err error, msg string) bool {
	return err != nil && strings.HasPrefix(err.Error(), msg+" (search error logs for ")
}

// vmCmdTableHas reports whether name is a cell of the table in output.
func vmCmdTableHas(output, name string) bool {
	for line := range strings.SplitSeq(output, "\n") {
		for cell := range strings.SplitSeq(line, "|") {
			if strings.TrimSpace(cell) == name {
				return true
			}
		}
	}

	return false
}

// vmCmdNodeSettings returns the stored settings of the VM named host in the
// experiment named exp.
func vmCmdNodeSettings(t *testing.T, host string) map[string]any {
	t.Helper()

	e, err := experiment.Get("exp")
	if err != nil {
		t.Fatalf("experiment.Get(exp) error = %v", err)
	}

	node := e.Spec.Topology().FindNodeByName(host)
	if node == nil {
		t.Fatalf("experiment exp has no VM %s", host)
	}

	drive := node.Hardware().Drives()[0]

	return map[string]any{
		"vcpus":       node.Hardware().VCPU(),
		"memory":      node.Hardware().Memory(),
		"image":       drive.Image(),
		"partition":   *drive.InjectPartition(),
		"do_not_boot": *node.General().DoNotBoot(),
		"snapshot":    *node.General().Snapshot(),
		"labels":      node.Labels(),
	}
}

// TestVMCmdArgsCompletion checks that the VM commands complete experiment
// names first and VM names second.
func TestVMCmdArgsCompletion(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t, vmCmdExperiment(t, "exp1", false), vmCmdExperiment(t, "exp2", false), vmCmdExperiment(t, "other", false))

	tests := []struct {
		name       string
		args       []string
		toComplete string
		want       []string
		directive  cobra.ShellCompDirective
	}{
		{name: "experiments", toComplete: "exp", want: []string{"exp1", "exp2"}, directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "VMs", args: []string{"exp1"}, toComplete: "w", want: []string{"web"}, directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "every VM", args: []string{"exp2"}, want: []string{"web", "db"}, directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "unknown experiment", args: []string{"missing"}, directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "third argument", args: []string{"exp1", "web"}, directive: cobra.ShellCompDirectiveNoFileComp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, directive := vmArgsCompletion(nil, tt.args, tt.toComplete)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("vmArgsCompletion(%q, %q) mismatch (-want +got):\n%s", tt.args, tt.toComplete, diff)
			}

			if directive != tt.directive {
				t.Errorf("vmArgsCompletion(%q, %q) directive = %v, want %v", tt.args, tt.toComplete, directive, tt.directive)
			}
		})
	}
}

// TestVMCmdHostCompletion checks that --host completes the hosts of the VMs
// in the experiment.
func TestVMCmdHostCompletion(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t, vmCmdExperiment(t, "exp", false))

	if got, directive := vmHostCompletion(nil, []string{"exp"}, "compute"); !slices.Equal(got, []string{"compute1", "compute2"}) ||
		directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("vmHostCompletion(exp, compute) = %q, %v, want [compute1 compute2], NoFileComp", got, directive)
	}

	if got, directive := vmHostCompletion(nil, []string{"missing"}, ""); got != nil || directive != cobra.ShellCompDirectiveError {
		t.Errorf("vmHostCompletion(missing) = %q, %v, want nil, Error", got, directive)
	}
}

// TestVMCmdPowerCommands checks the minimega commands that pause, resume,
// kill and restart send for the VMs that the arguments or --label select.
func TestVMCmdPowerCommands(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr string
		wantLog []string
	}{
		{name: "pause", args: []string{"pause", "exp", "web"}, want: []string{"vm stop web"}, wantLog: []string{"web"}},
		{name: "resume", args: []string{"resume", "exp", "db"}, want: []string{"vm start db"}, wantLog: []string{"db"}},
		{
			name:    "kill by label",
			args:    []string{"kill", "exp", "--label", "db-server"},
			want:    []string{"vm kill db", "vm flush db"},
			wantLog: []string{"db"},
		},
		{
			name: "kill every VM", args: []string{"kill", "exp", "--label", "all"},
			want: []string{"vm kill web", "vm flush web", "vm kill db", "vm flush db"}, wantLog: []string{"web", "db"},
		},
		{
			name: "labels in arguments and globs", args: []string{"pause", "exp", "--label", "WEB-*", "db-server"},
			want: []string{"vm stop web", "vm stop db"}, wantLog: []string{"web", "db"},
		},
		{
			name:    "no label matches",
			args:    []string{"kill", "exp", "--label", "web-server=1"},
			wantErr: "no VMs matched label(s): web-server=1",
		},
		{name: "empty label", args: []string{"kill", "exp", "--label", " , "}, wantErr: "must provide at least one VM label filter"},
		{name: "no VM name", args: []string{"kill", "exp"}, wantErr: "must provide an experiment and VM name (or use --label)"},
		{name: "no experiment", args: []string{"resume"}, wantErr: "must provide an experiment name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", false))
			logs := plogtest.Capture(t)
			received := mmtest.Use(t, nil)

			err := vmCmdRun(tt.args...)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("phenix vm %q error = %v, want %q", tt.args, err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("phenix vm %q error = %v", tt.args, err)
			}

			if diff := cmp.Diff(tt.want, vmCmdChanges(received())); diff != "" {
				t.Errorf("phenix vm %q minimega commands mismatch (-want +got):\n%s", tt.args, diff)
			}

			records := logs.Records(t, func(r map[string]any) bool { return strings.HasPrefix(r["msg"].(string), "vm ") })
			logged := make([]string, 0, len(records))

			for _, record := range records {
				logged = append(logged, record["vm"].(string))
			}

			if diff := cmp.Diff(tt.wantLog, logged, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("phenix vm %q logged VMs mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestVMCmdPowerCommandFailure checks that a VM command stops at the first VM
// that minimega refuses and names that VM.
func TestVMCmdPowerCommandFailure(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name string
		args []string
		fail string
		want []string
		msg  string
	}{
		{
			name: "pause",
			args: []string{"pause", "exp", "--label", "all"},
			fail: "vm stop web",
			want: []string{"vm stop web"},
			msg:  "Unable to pause the web VM",
		},
		{
			name: "resume",
			args: []string{"resume", "exp", "db"},
			fail: "vm start db",
			want: []string{"vm start db"},
			msg:  "Unable to resume the db VM",
		},
		{
			name: "kill",
			args: []string{"kill", "exp", "--label", "all"},
			fail: "vm kill db",
			want: []string{"vm kill web", "vm flush web", "vm kill db"},
			msg:  "Unable to kill the db VM",
		},
		{name: "restart", args: []string{"restart", "exp", "web"}, fail: "vm info summary", msg: "Unable to restart the web VM"},
		{name: "shutdown", args: []string{"shutdown", "exp", "web"}, fail: "vm info summary", msg: "Unable to shutdown the web VM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))
			fake := vmCmdMinimega{rows: vmCmdRunning(), fail: map[string]string{tt.fail: "refused"}}
			received := mmtest.Use(t, fake.handle)

			if err := vmCmdRun(tt.args...); !vmCmdHumanized(err, tt.msg) {
				t.Errorf("phenix vm %q error = %v, want %q", tt.args, err, tt.msg)
			}

			if diff := cmp.Diff(tt.want, vmCmdChanges(received())); diff != "" {
				t.Errorf("phenix vm %q minimega commands mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestVMCmdInfo checks which VMs vm info prints for its arguments and flags,
// and the errors it returns.
func TestVMCmdInfo(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t, vmCmdExperiment(t, "exp", false))

	tests := []struct {
		name      string
		args      []string
		want      []string
		wantErr   string
		humanized string
	}{
		{name: "every VM", args: []string{"exp"}, want: []string{"web", "db"}},
		{name: "one VM", args: []string{"exp", "db"}, want: []string{"db"}},
		{name: "label", args: []string{"exp", "--label", "web-server"}, want: []string{"web"}},
		{name: "host", args: []string{"exp", "--host", "compute2"}, want: []string{"db"}},
		{name: "filter", args: []string{"exp", "--filter", "NAME=web"}, want: []string{"web"}},
		{
			name:    "host and label",
			args:    []string{"exp", "--host", "compute2", "--label", "web-server"},
			wantErr: "no VMs matched host(s): compute2",
		},
		{name: "bad filter", args: []string{"exp", "--filter", "NAME"}, wantErr: `invalid VM info filter "NAME": expected COLUMN=VALUE`},
		{name: "empty label", args: []string{"exp", "--label", ","}, wantErr: "must provide at least one VM label filter"},
		{name: "too many arguments", args: []string{"exp", "web", "db"}, wantErr: "invalid argument"},
		{name: "no experiment", wantErr: "must provide an experiment name"},
		{name: "unknown VM", args: []string{"exp", "nope"}, humanized: "Unable to get information for the nope VM"},
		{name: "unknown experiment", args: []string{"missing"}, humanized: "Unable to get a list of VMs"},
		{name: "no label matches", args: []string{"exp", "--label", "none"}, humanized: "Unable to get a filtered list of VMs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := vmCmdOutput(append([]string{"info"}, tt.args...)...)

			switch {
			case tt.humanized != "":
				if !vmCmdHumanized(err, tt.humanized) {
					t.Fatalf("phenix vm info %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			case tt.wantErr != "":
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("phenix vm info %q error = %v, want %q", tt.args, err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("phenix vm info %q error = %v", tt.args, err)
			}

			var shown []string

			for _, name := range []string{"web", "db"} {
				if vmCmdTableHas(output, name) {
					shown = append(shown, name)
				}
			}

			if diff := cmp.Diff(tt.want, shown); diff != "" {
				t.Errorf("phenix vm info %q VMs mismatch (-want +got):\n%s\noutput:\n%s", tt.args, diff, output)
			}
		})
	}
}

// TestVMCmdRedeploy checks the minimega commands that vm redeploy sends, and
// that it refuses CPU and memory values that minimega cannot use.
func TestVMCmdRedeploy(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	relaunch := func(name string, config ...string) []string {
		cmds := make([]string, 0, 4+len(config)+3)
		cmds = append(cmds, "vm config clone "+name, "clear vm config state", "vm kill "+name, "vm flush "+name)
		cmds = append(cmds, config...)

		return append(cmds, "vm launch kvm "+name, "vm launch", "vm start "+name)
	}

	tests := []struct {
		name      string
		args      []string
		want      []string
		wantErr   string
		humanized string
	}{
		{
			name: "new settings", args: []string{"exp", "web", "-c", "2", "-m", "1024", "-d", "new.qc2"},
			want: relaunch("web", "vm config vcpus 2", "vm config mem 1024", "vm config disk new.qc2"),
		},
		{name: "defaults keep the CPUs and memory", args: []string{"exp", "db"}, want: relaunch("db")},
		{
			name: "only a new disk", args: []string{"exp", "web", "-d", "new.qc2"},
			want: relaunch("web", "vm config disk new.qc2"),
		},
		{
			name: "only new memory", args: []string{"exp", "web", "-m", "1536"},
			want: relaunch("web", "vm config mem 1536"),
		},
		{
			name: "zero keeps the CPUs and memory", args: []string{"exp", "web", "-c", "0", "-m", "0"},
			want: relaunch("web"),
		},
		{
			name: "label", args: []string{"exp", "--label", "all", "-c", "4", "-m", "2048"},
			want: slices.Concat(
				relaunch("web", "vm config vcpus 4", "vm config mem 2048"),
				relaunch("db", "vm config vcpus 4", "vm config mem 2048"),
			),
		},
		{name: "too many CPUs", args: []string{"exp", "web", "-c", "9"}, wantErr: "cpus can only be 1-8"},
		{name: "negative CPUs", args: []string{"exp", "web", "-c", "-1"}, wantErr: "cpus can only be 1-8"},
		{
			name: "memory not a multiple of 512", args: []string{"exp", "web", "-m", "1000"},
			wantErr: "memory must be a multiple of 512 from 512 to 16384",
		},
		{
			name: "too much memory", args: []string{"exp", "web", "-m", "16896"},
			wantErr: "memory must be a multiple of 512 from 512 to 16384",
		},
		{name: "no VM name", args: []string{"exp"}, wantErr: "must provide an experiment and VM name (or use --label)"},
		{name: "VM minimega lacks", args: []string{"exp", "gone"}, humanized: "Unable to redeploy the gone VM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))

			// minimega filters vm info by name, so the fake answers with the VM asked for
			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base != vmCmdInfoBase || strings.Contains(cmd.Raw, "name=gone") {
					return nil
				}

				if !strings.Contains(cmd.Raw, ".filter") {
					return vmCmdMinimega{rows: vmCmdRunning()}.handle(cmd)
				}

				name := "web"
				if strings.Contains(cmd.Raw, "name=db") {
					name = "db"
				}

				return []*minicli.Response{mmtest.Tabular("head", []string{"name", "type", "disks"}, []string{name, "kvm", name + ".qc2"})}
			})

			err := vmCmdRun(append([]string{"redeploy"}, tt.args...)...)

			switch {
			case tt.humanized != "":
				if !vmCmdHumanized(err, tt.humanized) {
					t.Fatalf("phenix vm redeploy %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			case tt.wantErr != "":
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("phenix vm redeploy %q error = %v, want %q", tt.args, err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("phenix vm redeploy %q error = %v", tt.args, err)
			}

			if diff := cmp.Diff(tt.want, vmCmdChanges(received())); diff != "" {
				t.Errorf("phenix vm redeploy %q minimega commands mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestVMCmdSetStoppedExperiment checks that vm set saves each given setting of
// the selected VMs in a stopped experiment, and keeps the other VMs as they
// are.
func TestVMCmdSetStoppedExperiment(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	unchanged := func(host string) map[string]any {
		return map[string]any{
			"vcpus": 1, "memory": 512, "image": host + ".qc2", "partition": 1,
			"do_not_boot": false, "snapshot": false, "labels": map[string]string{host + "-server": "1"},
		}
	}

	tests := []struct {
		name    string
		args    []string
		wantWeb map[string]any
		wantDB  map[string]any
	}{
		{
			name: "every setting",
			args: []string{
				"web",
				"-c",
				"2",
				"-m",
				"1024",
				"-d",
				"new.qc2",
				"-p",
				"2",
				"--do-not-boot",
				"--snapshot",
				"-L",
				"tier=front",
				"-L",
				"zone=",
			},
			wantWeb: map[string]any{
				"vcpus": 2, "memory": 1024, "image": "new.qc2", "partition": 2,
				"do_not_boot": true, "snapshot": true, "labels": map[string]string{"tier": "front", "zone": ""},
			},
			wantDB: unchanged("db"),
		},
		{
			name:    "label filter",
			args:    []string{"--label", "db-server", "-c", "4"},
			wantWeb: unchanged("web"),
			wantDB: map[string]any{
				"vcpus": 4, "memory": 512, "image": "db.qc2", "partition": 1,
				"do_not_boot": false, "snapshot": false, "labels": map[string]string{"db-server": "1"},
			},
		},
		{
			name:    "flags set to false",
			args:    []string{"web", "--do-not-boot=false", "--snapshot=false"},
			wantWeb: unchanged("web"),
			wantDB:  unchanged("db"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", false))

			if err := vmCmdRun(append([]string{"set", "exp"}, tt.args...)...); err != nil {
				t.Fatalf("phenix vm set exp %q error = %v", tt.args, err)
			}

			if diff := cmp.Diff(tt.wantWeb, vmCmdNodeSettings(t, "web")); diff != "" {
				t.Errorf("phenix vm set exp %q web settings mismatch (-want +got):\n%s", tt.args, diff)
			}

			if diff := cmp.Diff(tt.wantDB, vmCmdNodeSettings(t, "db")); diff != "" {
				t.Errorf("phenix vm set exp %q db settings mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestVMCmdSetRunningExperiment checks that vm set changes only the labels of
// a VM in a running experiment, in minimega and in the store.
func TestVMCmdSetRunningExperiment(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name       string
		args       []string
		want       []string
		wantLabels map[string]string
	}{
		{
			name: "replace labels", args: []string{"-L", "tier=front"},
			want:       []string{"clear vm tag web ", `vm tag web "tier" "front"`},
			wantLabels: map[string]string{"tier": "front"},
		},
		{
			name: "append labels", args: []string{"-L", "tier=front", "--append-labels"},
			want:       []string{"clear vm tag web ", `vm tag web "tier" "front"`, `vm tag web "web-server" "1"`},
			wantLabels: map[string]string{"web-server": "1", "tier": "front"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))
			received := mmtest.Use(t, nil)

			if err := vmCmdRun(append([]string{"set", "exp", "web"}, tt.args...)...); err != nil {
				t.Fatalf("phenix vm set exp web %q error = %v", tt.args, err)
			}

			got := vmCmdChanges(received())
			// minimega gets one tag command for each label, in map order
			slices.Sort(got[1:])

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("phenix vm set exp web %q minimega commands mismatch (-want +got):\n%s", tt.args, diff)
			}

			if diff := cmp.Diff(tt.wantLabels, vmCmdNodeSettings(t, "web")["labels"]); diff != "" {
				t.Errorf("phenix vm set exp web %q stored labels mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestVMCmdSetErrors checks that vm set refuses values it cannot apply, and
// sends no minimega command.
func TestVMCmdSetErrors(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		running   bool
		args      []string
		wantErr   string
		humanized string
	}{
		{name: "nothing to set", args: []string{"exp", "web"}, wantErr: "no configuration values provided to set"},
		{name: "too many CPUs", args: []string{"exp", "web", "-c", "0"}, wantErr: "cpus can only be 1-8"},
		{
			name: "memory too small", args: []string{"exp", "web", "-m", "256"},
			wantErr: "memory must be a multiple of 512 from 512 to 16384",
		},
		{name: "label without a value", args: []string{"exp", "web", "-L", "tier"}, wantErr: `invalid label "tier" (expected key=value)`},
		{name: "label without a key", args: []string{"exp", "web", "-L", "=front"}, wantErr: `invalid label "=front" (expected key=value)`},
		{
			name:    "label with a blank key",
			args:    []string{"exp", "web", "-L", " =front"},
			wantErr: `invalid label " =front" (expected key=value)`,
		},
		{name: "no VM name", args: []string{"exp"}, wantErr: "must provide an experiment and VM name (or use --label)"},
		{name: "unknown VM", args: []string{"exp", "nope", "-c", "2"}, humanized: "Unable to update the nope VM"},
		{name: "CPUs of a running VM", running: true, args: []string{"exp", "web", "-c", "2"}, humanized: "Unable to update the web VM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", tt.running))
			received := mmtest.Use(t, nil)

			err := vmCmdRun(append([]string{"set"}, tt.args...)...)
			if tt.humanized != "" {
				if !vmCmdHumanized(err, tt.humanized) {
					t.Errorf("phenix vm set %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			} else if err == nil || err.Error() != tt.wantErr {
				t.Errorf("phenix vm set %q error = %v, want %q", tt.args, err, tt.wantErr)
			}

			if got := vmCmdChanges(received()); len(got) != 0 {
				t.Errorf("phenix vm set %q sent %q to minimega, want no change", tt.args, got)
			}

			if diff := cmp.Diff(1, vmCmdNodeSettings(t, "web")["vcpus"]); diff != "" {
				t.Errorf("phenix vm set %q stored CPUs mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// vmCmdHostAndAnnotations returns the stored cluster host and annotations of
// the VM named host in the experiment named exp.
func vmCmdHostAndAnnotations(t *testing.T, host string) (string, map[string]any) {
	t.Helper()

	e, err := experiment.Get("exp")
	if err != nil {
		t.Fatalf("experiment.Get(exp) error = %v", err)
	}

	node := e.Spec.Topology().FindNodeByName(host)
	if node == nil {
		t.Fatalf("experiment exp has no VM %s", host)
	}

	return e.Spec.Schedules()[host], node.Annotations()
}

// TestVMCmdSetHostAnnotationsInterface checks that vm set saves --host,
// --annotation and --partition 0, sends --interface to minimega for a running
// VM, and refuses what it cannot apply.
func TestVMCmdSetHostAnnotationsInterface(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name            string
		running         bool
		args            []string
		wantErr         error
		wantErrText     string
		humanized       string
		wantHost        string
		wantAnnotations map[string]any
		wantPartition   int
		wantMinimega    []string
	}{
		{name: "host", args: []string{"--host", "compute9"}, wantHost: "compute9", wantPartition: 1},
		{name: "clear the host", args: []string{"--host", ""}, wantHost: "", wantPartition: 1},
		{
			name:     "annotations",
			args:     []string{"--annotation", "phenix/default-apps=false", "--annotation", "list=[1, 2]", "--annotation", "n=5"},
			wantHost: "compute1", wantPartition: 1,
			wantAnnotations: map[string]any{"phenix/default-apps": false, "list": []any{1.0, 2.0}, "n": "5"},
		},
		{name: "partition 0", args: []string{"-p", "0"}, wantHost: "compute1", wantPartition: 0},
		{
			name: "annotations of a running VM", running: true, args: []string{"--annotation", "note=x"},
			wantHost: "compute1", wantPartition: 1, wantAnnotations: map[string]any{"note": "x"},
		},
		{
			name: "connect an interface", running: true, args: []string{"--interface", "0=EXP_2"},
			wantHost: "compute1", wantPartition: 1, wantMinimega: []string{"vm net connect web 0 EXP_2 phenix"},
		},
		{
			name: "disconnect an interface", running: true, args: []string{"--interface", "0="},
			wantHost: "compute1", wantPartition: 1, wantMinimega: []string{"vm net disconnect web 0"},
		},
		{
			name: "interface of a stopped VM", args: []string{"--interface", "0=EXP_2"}, wantHost: "compute1", wantPartition: 1,
			wantErr: vm.ErrStoppedInterfaceUpdate,
		},
		{
			name: "interface without an index", args: []string{"--interface", "x=EXP_2"}, running: true,
			wantErr: errBadInterfaceFlag, wantHost: "compute1", wantPartition: 1,
		},
		{
			name: "interface without an equals sign", args: []string{"--interface", "0"}, running: true,
			wantErr: errBadInterfaceFlag, wantHost: "compute1", wantPartition: 1,
		},
		{
			name: "host of a running VM", running: true, args: []string{"--host", "compute9"},
			humanized: "Unable to update the web VM", wantHost: "compute1", wantPartition: 1,
		},
		{
			name: "annotation without a value", args: []string{"--annotation", "note"}, wantHost: "compute1", wantPartition: 1,
			wantErrText: `annotation "note" is not of the form key=value`,
		},
		{
			name: "negative partition", args: []string{"-p", "-1"}, wantHost: "compute1", wantPartition: 1,
			wantErrText: "the inject partition must be 0 or more",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", tt.running))
			received := mmtest.Use(t, vmCmdMinimega{}.handle)

			err := vmCmdRun(append([]string{"set", "exp", "web"}, tt.args...)...)

			switch {
			case tt.humanized != "":
				if !vmCmdHumanized(err, tt.humanized) {
					t.Errorf("phenix vm set exp web %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			case tt.wantErrText != "":
				if err == nil || err.Error() != tt.wantErrText {
					t.Errorf("phenix vm set exp web %q error = %v, want %q", tt.args, err, tt.wantErrText)
				}
			case !errors.Is(err, tt.wantErr):
				t.Errorf("phenix vm set exp web %q error = %v, want %v", tt.args, err, tt.wantErr)
			}

			host, annotations := vmCmdHostAndAnnotations(t, "web")
			if host != tt.wantHost {
				t.Errorf("phenix vm set exp web %q stored host = %q, want %q", tt.args, host, tt.wantHost)
			}

			if diff := cmp.Diff(tt.wantAnnotations, annotations, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("phenix vm set exp web %q stored annotations mismatch (-want +got):\n%s", tt.args, diff)
			}

			if got := vmCmdNodeSettings(t, "web")["partition"]; got != tt.wantPartition {
				t.Errorf("phenix vm set exp web %q stored partition = %v, want %d", tt.args, got, tt.wantPartition)
			}

			if diff := cmp.Diff(tt.wantMinimega, vmCmdChanges(received()), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("phenix vm set exp web %q minimega commands mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestVMCmdNet checks the minimega commands that vm net connect and
// disconnect send, and their errors.
func TestVMCmdNet(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		args      []string
		fail      string
		want      []string
		wantErr   string
		humanized string
	}{
		{name: "connect", args: []string{"connect", "exp", "web", "0", "EXP_2"}, want: []string{"vm net connect web 0 EXP_2 phenix"}},
		{name: "disconnect", args: []string{"disconnect", "exp", "db", "1"}, want: []string{"vm net disconnect db 1"}},
		{
			name: "connect too few arguments", args: []string{"connect", "exp", "web", "0"},
			wantErr: "must provide an experiment name, VM name, iface index, and VLAN ID",
		},
		{
			name: "disconnect too many arguments", args: []string{"disconnect", "exp", "web", "0", "1"},
			wantErr: "must provide an experiment name, VM name, and iface index",
		},
		{
			name:    "connect iface name",
			args:    []string{"connect", "exp", "web", "IF0", "EXP_2"},
			wantErr: `the network interface index must be an integer of 0 or more, not "IF0"`,
		},
		{
			name:    "disconnect iface name",
			args:    []string{"disconnect", "exp", "web", "IF0"},
			wantErr: `the network interface index must be an integer of 0 or more, not "IF0"`,
		},
		{
			name:      "connect unknown experiment",
			args:      []string{"connect", "missing", "web", "0", "EXP_2"},
			humanized: "Unable to modify the connectivity for the web VM",
		},
		{
			name: "disconnect refused", args: []string{"disconnect", "exp", "db", "1"}, fail: "vm net disconnect db 1",
			want: []string{"vm net disconnect db 1"}, humanized: "Unable to disconnect the interface on the db VM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))
			received := mmtest.Use(t, vmCmdMinimega{fail: map[string]string{tt.fail: "refused"}}.handle)

			err := vmCmdRun(append([]string{"net"}, tt.args...)...)

			switch {
			case tt.humanized != "":
				if !vmCmdHumanized(err, tt.humanized) {
					t.Errorf("phenix vm net %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			case tt.wantErr != "":
				if err == nil || err.Error() != tt.wantErr {
					t.Errorf("phenix vm net %q error = %v, want %q", tt.args, err, tt.wantErr)
				}
			case err != nil:
				t.Errorf("phenix vm net %q error = %v", tt.args, err)
			}

			if diff := cmp.Diff(tt.want, vmCmdChanges(received())); diff != "" {
				t.Errorf("phenix vm net %q minimega commands mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestVMCmdCapture checks the minimega commands that the vm capture
// subcommands send for subnets and whole VMs, the table of the minimega
// captures that start-subnet prints, and their errors.
func TestVMCmdCapture(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	fake := vmCmdMinimega{
		rows:     vmCmdRunning(),
		captures: [][]string{{"db:1", "/p/db1.pcap"}},
	}

	tests := []struct {
		name      string
		args      []string
		want      []string
		wantRows  [][3]string
		wantErr   string
		humanized string
		maxSize   string // ui.captures.max-size, "" for the default
	}{
		{
			name: "start subnet", args: []string{"start-subnet", "exp", "10.0.0.0/24"},
			want: []string{
				"mesh send head shell mkdir -p /phenix/images/exp/files", "capture pcap vm web 0 exp/files/web_0_TIME.pcap",
				"mesh send head shell mkdir -p /phenix/images/exp/files", "capture pcap vm db 0 exp/files/db_0_TIME.pcap",
			},
			wantRows: [][3]string{{"db", "1", "/p/db1.pcap"}},
		},
		{
			name: "start subnet with filter", args: []string{"start-subnet", "exp", "10.0.0.0/24", "--filter", "db"},
			want:     []string{"mesh send head shell mkdir -p /phenix/images/exp/files", "capture pcap vm db 0 exp/files/db_0_TIME.pcap"},
			wantRows: [][3]string{{"db", "1", "/p/db1.pcap"}},
		},
		{
			name:    "start subnet filter matches nothing",
			args:    []string{"start-subnet", "exp", "10.0.0.0/24", "--filter", "nothing"},
			wantErr: `no VMs matched filter "nothing"`,
		},
		{
			name:    "start subnet of another format",
			args:    []string{"start-subnet", "exp", "10.0.0.0/24", "--format", "erf"},
			wantErr: `invalid --format "erf": it must be pcapng or pcap`,
		},
		{
			name:    "start subnet with an invalid size limit",
			args:    []string{"start-subnet", "exp", "10.0.0.0/24"},
			maxSize: "-1",
			wantErr: `invalid ui.captures.max-size "-1": it must not be negative`,
		},
		{
			name:    "start invalid subnet",
			args:    []string{"start-subnet", "exp", "subnet"},
			wantErr: "an invalid ipv4 subnet was detected: subnet",
		},
		{
			name:    "start subnet too few arguments",
			args:    []string{"start-subnet", "exp"},
			wantErr: "must provide an experiment name and subnet",
		},
		{
			name:      "start subnet stopped experiment",
			args:      []string{"start-subnet", "stopped", "10.0.0.0/24"},
			humanized: "Unable to start the packet capture(s) for 10.0.0.0/24 ",
		},
		{
			name:      "start subnet filter unknown experiment",
			args:      []string{"start-subnet", "missing", "10.0.0.0/24", "-f", "db"},
			humanized: "Unable to retrieve a list of VMs for missing ",
		},
		{name: "stop subnet", args: []string{"stop-subnet", "exp", "10.1.0.0/24"}, want: []string{"capture pcap delete vm db"}},
		{name: "stop subnet with filter", args: []string{"stop-subnet", "exp", "10.0.0.0/8", "--filter", "web"}},
		{
			name:    "stop subnet filter matches nothing",
			args:    []string{"stop-subnet", "exp", "10.0.0.0/8", "--filter", "nothing"},
			wantErr: `no VMs matched filter "nothing"`,
		},
		{name: "stop invalid subnet", args: []string{"stop-subnet", "exp", "subnet"}, wantErr: "an invalid subnet was detected: subnet"},
		{name: "stop subnet too few arguments", args: []string{"stop-subnet"}, wantErr: "must provide an experiment name and subnet"},
		{
			name:      "stop subnet stopped experiment",
			args:      []string{"stop-subnet", "stopped", "10.0.0.0/24"},
			humanized: "Unable to stop the packet capture(s) on the 10.0.0.0/24 ",
		},
		{
			name: "start VLAN", args: []string{"start-vlan", "exp", "EXP_1"},
			want: []string{
				"mesh send head shell mkdir -p /phenix/images/exp/files", "capture pcap vm web 0 exp/files/web_0_TIME.pcap",
				"mesh send head shell mkdir -p /phenix/images/exp/files", "capture pcap vm db 0 exp/files/db_0_TIME.pcap",
			},
		},
		// db already captures on its EXP_2 interface, so no capture starts
		{name: "start VLAN on a capturing interface", args: []string{"start-vlan", "exp", "EXP_2"}},
		{
			name: "start VLAN with filter", args: []string{"start-vlan", "exp", "EXP_1", "--filter", "web"},
			want: []string{"mesh send head shell mkdir -p /phenix/images/exp/files", "capture pcap vm web 0 exp/files/web_0_TIME.pcap"},
		},
		{
			name:    "start VLAN filter matches nothing",
			args:    []string{"start-vlan", "exp", "EXP_1", "--filter", "nothing"},
			wantErr: `no VMs matched filter "nothing"`,
		},
		{
			name:    "start VLAN of another format",
			args:    []string{"start-vlan", "exp", "EXP_1", "--format", "erf"},
			wantErr: `invalid --format "erf": it must be pcapng or pcap`,
		},
		{name: "start VLAN too few arguments", args: []string{"start-vlan", "exp"}, wantErr: "must provide an experiment name and VLAN"},
		{
			name:      "start VLAN stopped experiment",
			args:      []string{"start-vlan", "stopped", "EXP_1"},
			humanized: "Unable to start the packet capture(s) on VLAN EXP_1 ",
		},
		{name: "stop VLAN", args: []string{"stop-vlan", "exp", "EXP_2"}, want: []string{"capture pcap delete vm db 1"}},
		{name: "stop VLAN leaves other VLANs' captures", args: []string{"stop-vlan", "exp", "EXP_1"}},
		{
			name:    "stop VLAN filter matches nothing",
			args:    []string{"stop-vlan", "exp", "EXP_2", "--filter", "nothing"},
			wantErr: `no VMs matched filter "nothing"`,
		},
		{name: "stop VLAN with filter", args: []string{"stop-vlan", "exp", "EXP_2", "--filter", "web"}},
		{name: "stop VLAN too few arguments", args: []string{"stop-vlan"}, wantErr: "must provide an experiment name and VLAN"},
		{
			name:      "stop VLAN stopped experiment",
			args:      []string{"stop-vlan", "stopped", "EXP_2"},
			humanized: "Unable to stop the packet capture(s) on VLAN EXP_2 ",
		},
		{name: "stop all", args: []string{"stop-all", "exp"}, want: []string{"capture pcap delete vm db"}},
		{name: "stop all too many arguments", args: []string{"stop-all", "exp", "db"}, wantErr: "must provide an experiment name"},
		{
			name:      "stop all stopped experiment",
			args:      []string{"stop-all", "stopped"},
			humanized: "Unable to stop the packet capture(s) for stopped ",
		},
		{name: "stop VM", args: []string{"stop", "exp", "db"}, want: []string{"capture pcap delete vm db"}},
		{
			name:      "stop VM without captures",
			args:      []string{"stop", "exp", "web"},
			humanized: "Unable to stop the packet capture(s) on the web VM",
		},
		{name: "stop unknown VM iface", args: []string{"stop", "exp", "nope", "0"}, humanized: "Unable to get details for the nope VM"},
		{
			name:      "stop iface without capture",
			args:      []string{"stop", "exp", "web", "0"},
			humanized: "Unable to stop the packet capture on the web VM",
		},
		{name: "stream unknown VM", args: []string{"stream", "exp", "nope", "0"}, humanized: "Unable to get details for the nope VM"},
		{
			name:      "start unknown VM",
			args:      []string{"start", "exp", "nope", "0", "out.pcap"},
			humanized: "Unable to get details for the nope VM",
		},
		{
			name:      "start unknown iface",
			args:      []string{"start", "exp", "web", "7", "out.pcap"},
			humanized: "Unable to resolve interface 7 on the web VM",
		},
		{
			name:      "start iface that captures",
			args:      []string{"start", "exp", "db", "IF1", "out.pcap"},
			humanized: "Unable to start a capture on the interface on the db VM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.maxSize != "" {
				t.Setenv("PHENIX_UI_CAPTURES_MAX_SIZE", tt.maxSize)
			}

			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true), vmCmdExperiment(t, "stopped", false))
			received := mmtest.Use(t, fake.handle)

			output, err := vmCmdOutput(append([]string{"capture"}, tt.args...)...)

			switch {
			case tt.humanized != "":
				if !vmCmdHumanized(err, tt.humanized) {
					t.Errorf("phenix vm capture %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			case tt.wantErr != "":
				if err == nil || err.Error() != tt.wantErr {
					t.Errorf("phenix vm capture %q error = %v, want %q", tt.args, err, tt.wantErr)
				}
			case err != nil:
				t.Errorf("phenix vm capture %q error = %v", tt.args, err)
			}

			if diff := cmp.Diff(tt.want, vmCmdChanges(received())); diff != "" {
				t.Errorf("phenix vm capture %q minimega commands mismatch (-want +got):\n%s", tt.args, diff)
			}

			if diff := cmp.Diff(tt.wantRows, vmCmdCaptureRows(output)); diff != "" {
				t.Errorf("phenix vm capture %q table mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// vmCmdCaptureRows returns the VM, interface and file of each row of the
// table of subnet captures in output.
func vmCmdCaptureRows(output string) [][3]string {
	var rows [][3]string

	for line := range strings.Lines(output) {
		cells := strings.Split(line, "|")
		if len(cells) != 5 || strings.TrimSpace(cells[1]) == "NAME" {
			continue
		}

		rows = append(rows, [3]string{strings.TrimSpace(cells[1]), strings.TrimSpace(cells[2]), strings.TrimSpace(cells[3])})
	}

	return rows
}

// TestVMCmdMemorySnapshot checks the errors that vm memory-snapshot returns
// before minimega starts a snapshot.
func TestVMCmdMemorySnapshot(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))

	tests := []struct {
		name      string
		args      []string
		wantErr   string
		humanized string
	}{
		{
			name:    "too few arguments",
			args:    []string{"exp", "web"},
			wantErr: "must provide an experiment name, VM name, and snapshot file path",
		},
		{name: "unknown VM", args: []string{"exp", "nope", "out.elf"}, humanized: "Unable to create a memory snapshot for the nope VM"},
		{
			name:      "VM minimega lacks",
			args:      []string{"exp", "web", "out.elf"},
			humanized: "Unable to create a memory snapshot for the web VM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := mmtest.Use(t, nil)

			err := vmCmdRun(append([]string{"memory-snapshot"}, tt.args...)...)
			if tt.humanized != "" {
				if !vmCmdHumanized(err, tt.humanized) {
					t.Errorf("phenix vm memory-snapshot %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			} else if err == nil || err.Error() != tt.wantErr {
				t.Errorf("phenix vm memory-snapshot %q error = %v, want %q", tt.args, err, tt.wantErr)
			}

			if got := vmCmdChanges(received()); len(got) != 0 {
				t.Errorf("phenix vm memory-snapshot %q sent %q to minimega, want no change", tt.args, got)
			}
		})
	}
}

// vmCmdMountMM is a minimega that runs the VMs of vms, and answers C2
// commands with the errors c2Err (client check) and execErr (mount and
// unmount). Other methods panic.
type vmCmdMountMM struct {
	mm.MM

	vms     mm.VMs
	c2Err   error
	execErr error
	execs   int
}

func (f *vmCmdMountMM) GetVMInfo(...mm.Option) mm.VMs                { return f.vms }
func (f *vmCmdMountMM) IsC2ClientActive(...mm.C2Option) error        { return f.c2Err }
func (f *vmCmdMountMM) ExecC2Command(...mm.C2Option) (string, error) { f.execs++; return "", f.execErr }

// TestVMCmdMount checks what vm mount and unmount print and return, and that
// vm mount uses <mount-dir>/<experiment>/<vm>.
func TestVMCmdMount(t *testing.T) { //nolint:paralleltest // replaces the phenix store, mm.DefaultMM and common.MountBase
	errC2 := errors.New("no client")
	base := filepath.Join(t.TempDir(), "mounts")
	dir := filepath.Join(base, "exp", "web")

	originalBase := common.MountBase
	t.Cleanup(func() { common.MountBase = originalBase }) //nolint:reassign // restore the mount directory

	common.MountBase = base //nolint:reassign // the test's mount directory

	tests := []struct {
		name      string
		args      []string
		fake      vmCmdMountMM
		want      string
		wantExecs int
		wantErr   string
		humanized string
	}{
		{
			name: "mount", args: []string{"mount", "exp", "web"}, fake: vmCmdMountMM{vms: mm.VMs{{Name: "web", Running: true}}},
			want: "VM web in experiment exp mounted at: " + dir + "\n", wantExecs: 1,
		},
		{name: "mount VM that is not running", args: []string{"mount", "exp", "web"}, humanized: "Unable to mount the web VM"},
		{
			name: "mount without C2", args: []string{"mount", "exp", "web"},
			fake: vmCmdMountMM{vms: mm.VMs{{Name: "web", Running: true}}, c2Err: errC2}, humanized: "Unable to mount the web VM",
		},
		{
			name:    "mount too few arguments",
			args:    []string{"mount", "exp"},
			wantErr: "must provide an experiment name and VM name",
		},
		{
			name:    "mount does not take a host path",
			args:    []string{"mount", "exp", "web", filepath.Join(base, "other")},
			wantErr: "must provide an experiment name and VM name",
		},
		{name: "unmount", args: []string{"unmount", "exp", "web"}, want: "VM web in experiment exp unmounted\n", wantExecs: 1},
		{
			name:      "unmount refused",
			args:      []string{"unmount", "exp", "web"},
			fake:      vmCmdMountMM{execErr: errC2},
			wantExecs: 1,
			humanized: "Unable to unmount the web VM",
		},
		{
			name:    "unmount too many arguments",
			args:    []string{"unmount", "exp", "web", "x"},
			wantErr: "must provide an experiment name and VM name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))

			fake := tt.fake
			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore minimega

			mm.DefaultMM = &fake //nolint:reassign // the test's minimega

			output, err := vmCmdOutput(tt.args...)

			switch {
			case tt.humanized != "":
				if !vmCmdHumanized(err, tt.humanized) {
					t.Errorf("phenix vm %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			case tt.wantErr != "":
				if err == nil || err.Error() != tt.wantErr {
					t.Errorf("phenix vm %q error = %v, want %q", tt.args, err, tt.wantErr)
				}
			case err != nil:
				t.Errorf("phenix vm %q error = %v", tt.args, err)
			}

			if output != tt.want {
				t.Errorf("phenix vm %q printed %q, want %q", tt.args, output, tt.want)
			}

			if fake.execs != tt.wantExecs {
				t.Errorf("phenix vm %q sent %d C2 commands, want %d", tt.args, fake.execs, tt.wantExecs)
			}
		})
	}
}
