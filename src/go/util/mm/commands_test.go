package mm

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

// answering returns a handler that answers each command whose base is a key
// of answers with its value, and `host` in the default namespace with the
// headnode "head". It answers other commands with an empty success.
func answering(answers map[string][]*minicli.Response) mmtest.Handler {
	return func(cmd mmtest.Command) []*minicli.Response {
		if resps, ok := answers[cmd.Base]; ok {
			return resps
		}

		if cmd.Base == "host" && cmd.Namespace == "minimega" {
			return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
		}

		return nil
	}
}

// sentRaw lists the commands as sent, without the headnode lookups.
func sentRaw(cmds []mmtest.Command) []string {
	var out []string

	for _, cmd := range cmds {
		if cmd.Base == "host" && cmd.Namespace == "minimega" {
			continue
		}

		out = append(out, cmd.Raw)
	}

	return out
}

// inExp is the command as phenix sends it in namespace exp; bare is the
// command as phenix sends it without a namespace.
func inExp(base string) string { return `.record false namespace "exp" ` + base }

func bare(base string) string { return ".record false " + base }

// failed is the answer of a host that reports msg as an error.
func failed(msg string) []*minicli.Response { return []*minicli.Response{mmtest.Error("head", msg)} }

// commandCase is a call of one of the package's functions, how the fake
// minimega answers it, and what the call must send and return.
type commandCase struct {
	call    func() (any, error)
	answers map[string][]*minicli.Response
	sent    []string // the commands sent, without headnode lookups
	want    any      // the result
	wantIs  error    // an error the call's error must wrap
	wantMsg string   // the minimega error the call's error must hold, "" for none
}

func (tc commandCase) run(t *testing.T) {
	t.Helper()

	received := useFakeMinimega(t, answering(tc.answers))

	got, err := tc.call()

	switch {
	case tc.wantIs == nil && tc.wantMsg == "" && err != nil:
		t.Errorf("error = %v, want nil", err)
	case tc.wantIs != nil && !errors.Is(err, tc.wantIs):
		t.Errorf("error = %v, want %v", err, tc.wantIs)
	case tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)):
		t.Errorf("error = %v, want one holding minimega's %q", err, tc.wantMsg)
	}

	if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(tc.sent, sentRaw(received()), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("sent commands mismatch (-want +got):\n%s", diff)
	}
}

// errOnly adapts a call that returns only an error.
func errOnly(call func() error) func() (any, error) {
	return func() (any, error) { return nil, call() }
}

// Each one-command function of the package sends its command in the right
// namespace, returns what minimega answers, and reports minimega's error.
//
//nolint:maintidx // one table
func TestOneCommandFunctions(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	vnc := inExp(`.columns "vnc_port" .filter name=a .filter type=kvm vm info`)
	host := inExp(`.columns "name" .filter name=a vm info`)
	state := inExp(`.columns "state" .filter name=a vm info summary`)

	for name, tc := range map[string]commandCase{
		"ReadScriptFromFile": {
			call: errOnly(func() error { return ReadScriptFromFile("exp", "/s.mm") }),
			sent: []string{inExp("read /s.mm")},
		},
		"ReadScriptFromFile reports an error": {
			call:    errOnly(func() error { return ReadScriptFromFile("exp", "/s.mm") }),
			answers: map[string][]*minicli.Response{"read /s.mm": failed("no such file")},
			sent:    []string{inExp("read /s.mm")},
			wantMsg: "no such file",
		},
		"ClearNamespace": {
			call: errOnly(func() error { return ClearNamespace("exp") }),
			sent: []string{bare("clear namespace exp")},
		},
		"ClearNamespace reports an error": {
			call:    errOnly(func() error { return ClearNamespace("exp") }),
			answers: map[string][]*minicli.Response{"clear namespace exp": failed("busy")},
			sent:    []string{bare("clear namespace exp")},
			wantMsg: "busy",
		},
		"StartVM": {
			call: errOnly(func() error { return StartVM(NS("exp"), VMName("a")) }),
			sent: []string{inExp("vm start a")},
		},
		"StartVM reports an error": {
			call:    errOnly(func() error { return StartVM(NS("exp"), VMName("a")) }),
			answers: map[string][]*minicli.Response{"vm start a": failed("vm not found: a")},
			sent:    []string{inExp("vm start a")},
			wantMsg: "vm not found: a",
		},
		"StopVM": {
			call: errOnly(func() error { return StopVM(NS("exp"), VMName("a")) }),
			sent: []string{inExp("vm stop a")},
		},
		"StopVM reports an error": {
			call:    errOnly(func() error { return StopVM(NS("exp"), VMName("a")) }),
			answers: map[string][]*minicli.Response{"vm stop a": failed("not running")},
			sent:    []string{inExp("vm stop a")},
			wantMsg: "not running",
		},
		"KillVM reports an error and flushes nothing": {
			call:    errOnly(func() error { return KillVM(NS("exp"), VMName("a")) }),
			answers: map[string][]*minicli.Response{"vm kill a": failed("vm not found: a")},
			sent:    []string{inExp("vm kill a")},
			wantMsg: "vm not found: a",
		},
		"DisconnectVMInterface": {
			call: errOnly(func() error { return DisconnectVMInterface(NS("exp"), VMName("a"), ConnectInterface(2)) }),
			sent: []string{inExp("vm net disconnect a 2")},
		},
		"DisconnectVMInterface reports an error": {
			call:    errOnly(func() error { return DisconnectVMInterface(NS("exp"), VMName("a"), ConnectInterface(2)) }),
			answers: map[string][]*minicli.Response{"vm net disconnect a 2": failed("no such interface")},
			sent:    []string{inExp("vm net disconnect a 2")},
			wantMsg: "no such interface",
		},
		"ConnectVMInterface": {
			call: errOnly(func() error {
				return ConnectVMInterface(NS("exp"), VMName("a"), ConnectInterface(0), ConnectVLAN("EXP_1"))
			}),
			sent: []string{inExp("vm net connect a 0 EXP_1")},
		},
		"ConnectVMInterface reports an error": {
			call: errOnly(func() error {
				return ConnectVMInterface(NS("exp"), VMName("a"), ConnectInterface(0), ConnectVLAN("EXP_1"))
			}),
			answers: map[string][]*minicli.Response{"vm net connect a 0 EXP_1": failed("no such interface")},
			sent:    []string{inExp("vm net connect a 0 EXP_1")},
			wantMsg: "no such interface",
		},
		"CreateBridge": {
			call: errOnly(func() error { return CreateBridge(NS("exp"), Bridge("br0")) }),
			sent: []string{inExp("ns bridge br0 gre")},
		},
		"CreateBridge reports an error": {
			call:    errOnly(func() error { return CreateBridge(NS("exp"), Bridge("br0")) }),
			answers: map[string][]*minicli.Response{"ns bridge br0 gre": failed("bridge exists")},
			sent:    []string{inExp("ns bridge br0 gre")},
			wantMsg: "bridge exists",
		},
		"CreateBridge refuses a bridge minimega would expand": {
			call:   errOnly(func() error { return CreateBridge(NS("exp"), Bridge("$BRIDGE")) }),
			wantIs: ErrUnsafeArg,
		},
		"ClearC2Responses": {
			call: errOnly(func() error { return ClearC2Responses(C2NS("exp")) }),
			sent: []string{inExp("clear cc responses")},
		},
		"ClearC2Responses reports an error": {
			call:    errOnly(func() error { return ClearC2Responses(C2NS("exp")) }),
			answers: map[string][]*minicli.Response{"clear cc responses": failed("busy")},
			sent:    []string{inExp("clear cc responses")},
			wantMsg: "busy",
		},
		"SetVMTags clears the tags and sets each": {
			call: errOnly(func() error { return SetVMTags(NS("exp"), VMName("a"), Tags(map[string]string{"k": "v 1"})) }),
			sent: []string{inExp("clear vm tag a "), inExp(`vm tag a "k" "v 1"`)},
		},
		"SetVMTags reports a failed clear and sets none": {
			call:    errOnly(func() error { return SetVMTags(NS("exp"), VMName("a"), Tags(map[string]string{"k": "v"})) }),
			answers: map[string][]*minicli.Response{"clear vm tag a ": failed("vm not found: a")},
			sent:    []string{inExp("clear vm tag a ")},
			wantMsg: "vm not found: a",
		},
		"SetVMTags reports a failed tag": {
			call:    errOnly(func() error { return SetVMTags(NS("exp"), VMName("a"), Tags(map[string]string{"k": "v"})) }),
			answers: map[string][]*minicli.Response{`vm tag a "k" "v"`: failed("bad tag")},
			sent:    []string{inExp("clear vm tag a "), inExp(`vm tag a "k" "v"`)},
			wantMsg: "bad tag",
		},
		"GetVNCEndpoint": {
			call: func() (any, error) { return GetVNCEndpoint(NS("exp"), VMName("a")) },
			answers: map[string][]*minicli.Response{
				vmInfoCmd: {mmtest.Tabular("compute1", []string{"vnc_port"}, []string{"5901"})},
			},
			sent: []string{vnc},
			want: "compute1:5901",
		},
		"GetVNCEndpoint of a missing VM": {
			call:    func() (any, error) { return GetVNCEndpoint(NS("exp"), VMName("a")) },
			sent:    []string{vnc},
			want:    "",
			wantMsg: "not found",
		},
		"GetVMHost": {
			call: func() (any, error) { return GetVMHost(NS("exp"), VMName("a")) },
			answers: map[string][]*minicli.Response{
				vmInfoCmd: {mmtest.Tabular("compute2", []string{"name"}, []string{"a"})},
			},
			sent: []string{host},
			want: "compute2",
		},
		"GetVMHost of a missing VM": {
			call:    func() (any, error) { return GetVMHost(NS("exp"), VMName("a")) },
			sent:    []string{host},
			want:    "",
			wantMsg: "vm a not found",
		},
		"GetVMState": {
			call: func() (any, error) { return GetVMState(NS("exp"), VMName("a")) },
			answers: map[string][]*minicli.Response{
				vmInfoSummaryCmd: {mmtest.Tabular("head", []string{stateColumn}, []string{"PAUSED"})},
			},
			sent: []string{state},
			want: "PAUSED",
		},
		"GetVMState of a missing VM": {
			call:    func() (any, error) { return GetVMState(NS("exp"), VMName("a")) },
			sent:    []string{state},
			want:    "",
			wantMsg: "vm a not found",
		},
		"GetVLANs": {
			call: func() (any, error) { return GetVLANs(NS("exp")) },
			answers: map[string][]*minicli.Response{
				"vlans": {mmtest.Tabular("head", []string{"namespace", "alias", "vlan"},
					[]string{"exp", "EXP_1", "101"}, []string{"exp", "EXP_2", "102"})},
			},
			sent: []string{inExp("vlans")},
			want: map[string]int{"EXP_1": 101, "EXP_2": 102},
		},
		"GetVLANs reports a VLAN that is not a number": {
			call: func() (any, error) { return GetVLANs(NS("exp")) },
			answers: map[string][]*minicli.Response{
				"vlans": {mmtest.Tabular("head", []string{"alias", "vlan"}, []string{"EXP_1", "x"})},
			},
			sent:   []string{inExp("vlans")},
			want:   map[string]int(nil),
			wantIs: strconv.ErrSyntax,
		},
		"GetMMArgs": {
			call: func() (any, error) { return Minimega{}.GetMMArgs() },
			answers: map[string][]*minicli.Response{
				argsCmd: {mmtest.Tabular("head", []string{"base", "port"}, []string{"/tmp/minimega", "9000"})},
			},
			sent: []string{bare(argsCmd)},
			want: map[string]string{"host": "head", "base": "/tmp/minimega", "port": "9000"},
		},
		"GetMMArgs without an answer": {
			call:    func() (any, error) { return Minimega{}.GetMMArgs() },
			sent:    []string{bare(argsCmd)},
			want:    map[string]string(nil),
			wantMsg: "no args returned",
		},
		"MeshShell on the headnode": {
			call: errOnly(func() error { return MeshShell("head", "ls -l") }),
			sent: []string{bare("shell ls -l")},
		},
		"MeshShell without a host runs on the headnode": {
			call: errOnly(func() error { return MeshShell("", "ls -l") }),
			sent: []string{bare("shell ls -l")},
		},
		"MeshShell on another node": {
			call: errOnly(func() error { return MeshShell("compute1", "ls -l") }),
			sent: []string{bare("mesh send compute1 shell ls -l")},
		},
		"MeshShell reports an error": {
			call:    errOnly(func() error { return MeshShell("compute1", "ls -l") }),
			answers: map[string][]*minicli.Response{"mesh send compute1 shell ls -l": failed("exit status 2")},
			sent:    []string{bare("mesh send compute1 shell ls -l")},
			wantMsg: "exit status 2",
		},
		"MeshShellResponse trims the output": {
			call: func() (any, error) { return MeshShellResponse(t.Context(), "compute1", "hostname") },
			answers: map[string][]*minicli.Response{
				"mesh send compute1 shell hostname": {mmtest.Text("compute1", "  compute1\n")},
			},
			sent: []string{bare("mesh send compute1 shell hostname")},
			want: "compute1",
		},
		"MeshShellResponse takes the answer over another host's error": {
			call: func() (any, error) { return MeshShellResponse(t.Context(), "", "hostname") },
			answers: map[string][]*minicli.Response{
				"shell hostname": {mmtest.Error("compute1", "denied"), mmtest.Text("head", "head\n")},
			},
			sent: []string{bare("shell hostname")},
			want: "head",
		},
		"MeshShellResponse without an answer": {
			call:    func() (any, error) { return MeshShellResponse(t.Context(), "", "hostname") },
			answers: map[string][]*minicli.Response{"shell hostname": failed("denied")},
			sent:    []string{bare("shell hostname")},
			want:    "",
			wantMsg: "error running MeshShellResponse()",
		},
		"LaunchVMs starts every VM": {
			call: errOnly(func() error { return LaunchVMs("exp") }),
			sent: []string{inExp(vmLaunchCmd), inExp("vm start all")},
		},
		"LaunchVMs reports a failed start": {
			call:    errOnly(func() error { return LaunchVMs("exp") }),
			answers: map[string][]*minicli.Response{"vm start all": failed("no VMs")},
			sent:    []string{inExp(vmLaunchCmd), inExp("vm start all")},
			wantMsg: "no VMs",
		},
		"GetLaunchProgress counts the queued VMs": {
			call: func() (any, error) { return GetLaunchProgress("exp", 4) },
			answers: map[string][]*minicli.Response{
				nsQueueCmd: {mmtest.Text("head", "Names: vm[1-3]\nVMs: 3\n")},
			},
			sent: []string{inExp(nsQueueCmd)},
			want: 0.75,
		},
		"GetVMHosts": {
			call: func() (any, error) { return GetVMHosts(NS("exp"), VMName("a")), nil },
			answers: map[string][]*minicli.Response{
				vmInfoCmd: {mmtest.Tabular("compute1", []string{"name"}, []string{"a"})},
			},
			sent: []string{inExp(`.columns "name" .filter name=a vm info`)},
			want: map[string]string{"a": "compute1"},
		},
		"GetVMIPv4": {
			call: func() (any, error) { return GetVMIPv4(NS("exp"), VMName("a")) },
			answers: map[string][]*minicli.Response{
				vmInfoCmd: {mmtest.Tabular("compute1", []string{"ip"}, []string{"[10.0.0.1]"})},
			},
			sent: []string{inExp(`.columns "ip" .filter name=a vm info`)},
			want: []string{"10.0.0.1"},
		},
		"GetVMStates": {
			call: func() (any, error) { return GetVMStates(NS("exp")) },
			answers: map[string][]*minicli.Response{
				vmInfoSummaryCmd: {mmtest.Tabular("head", []string{"name", stateColumn}, []string{"a", "RUNNING"})},
			},
			sent: []string{inExp(`.columns "name","state" vm info summary`)},
			want: map[string]string{"a": "RUNNING"},
		},
		"GetExperimentCaptures leaves out bridge captures and unknown interfaces": {
			call: func() (any, error) { return GetExperimentCaptures(NS("exp")), nil },
			answers: map[string][]*minicli.Response{
				captureCmd: {mmtest.Tabular("head", []string{"interface", "path"},
					[]string{"a:1", "/a1.pcap"}, []string{"", "/bridge.pcap"}, []string{"b", "/b.pcap"})},
			},
			sent: []string{inExp(`.columns "interface","path" capture`)},
			want: []Capture{{VM: "a", Interface: 1, Filepath: "/a1.pcap"}},
		},
		"MeshSend": {
			call: errOnly(func() error { return MeshSend("exp", "compute1", "vm info") }),
			sent: []string{bare(`mesh send compute1 namespace "exp" vm info`)},
		},
		"CreateTunnel on another node": {
			call: errOnly(func() error {
				return CreateTunnel(NS("exp"), VMName("a"), TunnelSourcePort(50001),
					TunnelDestinationHost("127.0.0.1"), TunnelDestinationPort(22))
			}),
			answers: map[string][]*minicli.Response{
				vmInfoCmd: {mmtest.Tabular("compute1", []string{"name"}, []string{"a"})},
			},
			sent: []string{host, inExp("mesh send compute1 namespace exp cc tunnel a 50001 127.0.0.1 22")},
		},
		"IsC2ClientActive": {
			call: errOnly(func() error { return IsC2ClientActive(C2NS("exp"), C2VM("a"), C2IDClientsByUUID()) }),
			answers: map[string][]*minicli.Response{
				vmInfoCmd:   {mmtest.Tabular("head", []string{"name", "uuid"}, []string{"a", "uuid-a"})},
				ccClientCmd: {mmtest.Tabular("head", []string{"uuid"}, []string{"uuid-a"})},
			},
			sent: []string{
				inExp(`.columns "name","uuid" .filter name=a vm info`),
				inExp(`.columns "uuid","hostname" .filter uuid=uuid-a cc client`),
			},
		},
		"GetC2Response without a type": {
			call: func() (any, error) { return GetC2Response(C2NS("exp"), C2CommandID("7")) },
			answers: map[string][]*minicli.Response{
				"cc response 7 raw": {mmtest.Text("head", "hello\n")},
			},
			sent: []string{inExp("cc response 7 raw")},
			want: "hello\n",
		},
		"GetC2Response without a type reports an error": {
			call:    func() (any, error) { return GetC2Response(C2NS("exp"), C2CommandID("7")) },
			answers: map[string][]*minicli.Response{"cc response 7 raw": failed("no responses for 7")},
			sent:    []string{inExp("cc response 7 raw")},
			want:    "",
			wantMsg: "no responses for 7",
		},
	} {
		t.Run(name, tc.run)
	}
}

// GetVMScreenshot decodes the screenshot any host sends, and otherwise tells
// a VM that is not there from a screenshot that is not.
func TestGetVMScreenshot(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	const screenshot = "vm screenshot a file /dev/null 200"

	png := []byte("\x89PNG")
	encoded := base64.StdEncoding.EncodeToString(png)

	var corrupt base64.CorruptInputError

	for name, tc := range map[string]struct {
		answer  []*minicli.Response
		want    []byte
		wantIs  error
		wantAs  any
		wantMsg string
	}{
		"decodes the screenshot": {
			answer: []*minicli.Response{{Host: "compute1", Data: encoded}},
			want:   png,
		},
		"a screenshot wins over another host's error": {
			answer: []*minicli.Response{mmtest.Error("head", "vm not found: a"), {Host: "compute1", Data: encoded}},
			want:   png,
		},
		"a missing VM": {
			answer: []*minicli.Response{mmtest.Error("head", "vm not found: a")},
			wantIs: ErrVMNotFound,
		},
		"a VM that is not running": {
			answer: []*minicli.Response{mmtest.Error("head", "vm not running: a")},
			wantIs: ErrVMNotFound,
		},
		"no screenshot": {
			answer: []*minicli.Response{{Host: "head"}},
			wantIs: ErrScreenshotNotFound,
		},
		"another error": {
			answer: []*minicli.Response{mmtest.Error("head", "vnc failed")},
			wantIs: ErrScreenshotNotFound,
		},
		"a screenshot that is not base64": {
			answer: []*minicli.Response{{Host: "head", Data: "!!"}},
			wantAs: &corrupt,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, answering(map[string][]*minicli.Response{screenshot: tc.answer}))

			got, err := GetVMScreenshot(NS("exp"), VMName("a"), ScreenshotSize("200"))

			switch {
			case tc.wantAs != nil:
				if !errors.As(err, tc.wantAs) {
					t.Errorf("GetVMScreenshot(a) error = %v, want a %T", err, tc.wantAs)
				}
			case !errors.Is(err, tc.wantIs):
				t.Errorf("GetVMScreenshot(a) error = %v, want %v", err, tc.wantIs)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("GetVMScreenshot(a) mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff([]string{inExp(screenshot)}, sentRaw(received())); diff != "" {
				t.Errorf("GetVMScreenshot(a) sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// GetNamespaceHosts reads every field of each host and measures its disks.
func TestGetNamespaceHosts(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{
		"name", "cpus", "cpucommit", "load", "memused", "memtotal", "memcommit", "vms", "tx", "rx", "netcommit", "uptime",
	}

	var disk string

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case cmd.Base == "host" && cmd.Namespace == "exp":
			return []*minicli.Response{mmtest.Tabular("compute1", header,
				[]string{"compute1", "16", "4", "0.1 0.2 0.3", "1024", "4096", "2048", "2", "1.5", "2.5", "3", "1m30s"})}
		case cmd.Base == disk:
			return []*minicli.Response{mmtest.Text("compute1", "0=10% 1=20%\n")}
		}

		return answering(nil)(cmd)
	})

	// the fake minimega sets common.MinimegaBase when it first starts
	disk = "mesh send compute1 shell " + diskUsageCommand(common.PhenixBase, common.MinimegaBase)

	hosts, err := GetNamespaceHosts("exp")
	if err != nil {
		t.Fatalf("GetNamespaceHosts(exp) error = %v", err)
	}

	want := Hosts{{
		Name: "compute1", CPUs: 16, CPUCommit: 4, Load: []string{"0.1", "0.2", "0.3"},
		MemUsed: 1024, MemTotal: 4096, MemCommit: 2048, Tx: 1.5, Rx: 2.5, Bandwidth: "rx: 2.5 / tx: 1.5",
		DiskUsage: DiskUsage{Phenix: 10, Minimega: 20}, NetCommit: 3, VMs: 2, Uptime: 90,
		Schedulable: false, Headnode: false,
	}}

	if diff := cmp.Diff(want, hosts); diff != "" {
		t.Errorf("GetNamespaceHosts(exp) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{inExp("host"), bare(disk)}, sentRaw(received())); diff != "" {
		t.Errorf("GetNamespaceHosts(exp) sent commands mismatch (-want +got):\n%s", diff)
	}
}
