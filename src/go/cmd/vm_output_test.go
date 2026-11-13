package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	ft "phenix/web/forward/forwardtypes"
)

// vmOutputStoppedRow is the vm info row of the VM name of the stopped
// vmCmdExperiment, on host, with the networks and tags given.
func vmOutputStoppedRow(name, host string, tags map[string]string, networks ...string) vmInfoRow {
	return vmInfoRow{
		Name: name, Host: host, IPv4: make([]string, len(networks)), CPUs: 1, RAM: 512,
		Disk: "/phenix/images/" + name + ".qc2", Uptime: 0, Networks: networks, Taps: []string{},
		Captures: []mm.Capture{}, DoNotBoot: false, Running: false, Busy: false, Experiment: "exp", State: "",
		CdRom: "", Tags: tags, CCActive: false, Snapshot: false, InjectPartition: 1, Description: "", OSType: "linux",
	}
}

// vm info prints the table as before, and -o json and -o yaml print the
// rows with the fields of the API.
func TestVMCmdInfoOutput(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t, vmCmdExperiment(t, "exp", false))

	web := vmOutputStoppedRow("web", "compute1", map[string]string{"web-server": "1"}, "EXP_1")
	db := vmOutputStoppedRow("db", "compute2", map[string]string{"db-server": "1"}, "EXP_1", "EXP_2")

	for _, tt := range []struct {
		args []string
		want []vmInfoRow
	}{
		{args: []string{"info", "exp"}, want: []vmInfoRow{web, db}},
		{args: []string{"info", "exp", "db"}, want: []vmInfoRow{db}},
		{args: []string{"info", "exp", "--filter", "NAME=none"}, want: []vmInfoRow{}},
	} {
		run := func(args ...string) (string, error) { return vmCmdOutput(append(tt.args, args...)...) }

		table := checkOutputFormats(t, "phenix vm "+strings.Join(tt.args, " "), run, tt.want)

		for _, row := range tt.want {
			if !vmCmdTableHas(table, row.Name) {
				t.Errorf("phenix vm %q table has no VM %s:\n%s", tt.args, row.Name, table)
			}
		}
	}

	if _, err := vmCmdOutput("info", "exp", "-o", "csv"); err == nil || !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("phenix vm info exp -o csv error = %v, want an unknown output format", err)
	}
}

// vm capture list prints the captures of an experiment or a VM as a table,
// and as rows with the fields of the API.
func TestVMCmdCaptureListOutput(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vmCmdExperiment(t, "exp", true), vmCmdExperiment(t, "stopped", false))

	fake := vmCmdMinimega{rows: vmCmdRunning(), captures: [][]string{{"db:1", "/p/db1.pcap"}}}
	mmtest.Use(t, fake.handle)

	db := []mm.Capture{{VM: "db", Interface: 1, Filepath: "/p/db1.pcap", Format: "pcap"}}

	for _, tt := range []struct {
		args []string
		want []mm.Capture
	}{
		{args: []string{"exp"}, want: db},
		{args: []string{"exp", "db"}, want: db},
		{args: []string{"stopped"}, want: []mm.Capture{}},
	} {
		run := func(args ...string) (string, error) {
			capture := newVMCaptureCmd()
			capture.AddCommand(newVMCaptureListCmd())

			vmCmd := newVMCmd()
			vmCmd.AddCommand(capture)

			out, err := outputRun(vmCmd, append(append([]string{"vm", "capture", "list"}, tt.args...), args...)...)

			return out, err
		}

		table := checkOutputFormats(t, "phenix vm capture list "+strings.Join(tt.args, " "), run, tt.want)

		if got := vmCmdCaptureRows(table); len(got) != len(tt.want) {
			t.Errorf("phenix vm capture list %q table rows = %q, want %d", tt.args, got, len(tt.want))
		}
	}
}

// vm snapshot list prints one name on each line, and the names as a list
// with -o json and -o yaml.
func TestVMCmdSnapshotListOutput(t *testing.T) { //nolint:paralleltest // replaces the phenix store and cluster files
	for _, tt := range []struct {
		snapshots []string
		want      []string
		table     string
	}{
		{snapshots: []string{"web__b", "db__a", "web__a"}, want: []string{"web__a", "web__b"}, table: "web__a\nweb__b\n"},
		{snapshots: []string{"db__a"}, want: []string{}, table: ""},
	} {
		vmSnapshotCmdUse(t, false, &vmSnapshotCmdFiles{snapshots: tt.snapshots})

		run := func(args ...string) (string, error) {
			out, _, err := vmSnapshotCmdOutput(append([]string{"snapshot", "list", "exp", "web"}, args...)...)

			return out, err
		}

		if table := checkOutputFormats(t, "phenix vm snapshot list exp web", run, tt.want); table != tt.table {
			t.Errorf("phenix vm snapshot list exp web = %q, want %q", table, tt.table)
		}
	}
}

// vm qos get prints the link quality of each interface of a VM, or of one,
// with the interface names, as a table and as rows.
func TestVMCmdQoSGetOutput(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vmCmdExperiment(t, "exp", true))

	fake := vmCmdMinimega{rows: vmCmdRunning()}
	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == vmCmdInfoBase && strings.Contains(cmd.Raw, `"qos"`) {
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "vlan", "qos"},
				[]string{"db", "[EXP_1, EXP_2]", "[1: loss 0.5 delay 100ms]"})}
		}

		return fake.handle(cmd)
	})

	all := []qosRow{
		{Interface: 0, Name: "IF0", Loss: "", Delay: "", Rate: ""},
		{Interface: 1, Name: "IF1", Loss: "0.5", Delay: "100ms", Rate: ""},
	}

	for _, tt := range []struct {
		args []string
		want []qosRow
	}{
		{args: []string{"exp", "db"}, want: all},
		{args: []string{"exp", "db", "IF1"}, want: all[1:]},
	} {
		run := func(args ...string) (string, error) {
			qos := newVMQoSCmd()
			vmCmd := newVMCmd()
			vmCmd.AddCommand(qos)

			out, err := outputRun(vmCmd, append(append([]string{"vm", "qos", "get"}, tt.args...), args...)...)

			return out, err
		}

		table := checkOutputFormats(t, "phenix vm qos get "+strings.Join(tt.args, " "), run, tt.want)

		if !strings.Contains(table, "INTERFACE INDEX") || !strings.Contains(table, "IF1") {
			t.Errorf("phenix vm qos get %q table = %q, want the header and IF1", tt.args, table)
		}
	}
}

// vm forward list prints the forwards of the server as a table, and with the
// fields of the API.
func TestVMForwardListOutput(t *testing.T) {
	listeners := []ft.Listener{{Exp: "exp", VM: "web", SrcPort: 2222, DstHost: "127.0.0.1", DstPort: 22, Owner: "alice"}}

	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string][]ft.Listener{"listeners": listeners})
	})

	run := func(args ...string) (string, error) {
		return runVMForwardCmd(t, fake.socket, append([]string{"list", "exp", "web"}, args...)...)
	}

	want := []ft.Listener{{Exp: "exp", VM: "web", SrcPort: 2222, DstHost: "127.0.0.1", DstPort: 22, Owner: "alice"}}

	if table := checkOutputFormats(t, "phenix vm forward list exp web", run, want); !strings.Contains(table, "2222") {
		t.Errorf("phenix vm forward list exp web = %q, want the source port 2222", table)
	}
}
