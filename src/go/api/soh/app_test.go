package soh

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/app"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/mm"
)

// c2Call is a C2 command as the soh app gives it to minimega.
type c2Call struct {
	NS, VM, Command, TestConn string
	Send                      string // the `cc send` file text, sent before Command
	UUID                      bool   // the VM's C2 client is found by its UUID
}

func (c c2Call) String() string {
	cmd := c.Command
	if c.TestConn != "" {
		cmd = "test-conn " + c.TestConn
	}

	if c.Send != "" {
		cmd = "send " + c.Send + "; " + cmd
	}

	if c.UUID {
		cmd += " (uuid)"
	}

	return fmt.Sprintf("%s/%s: %s", c.NS, c.VM, cmd)
}

// c2Wait is how the soh app waits for a C2 response.
type c2Wait struct {
	VM                                string
	Timeout, AppearGrace, ClientGrace time.Duration
}

// c2Options reads the named fields of the options mm makes from opts.
// mm keeps them unexported, and the fake below needs them to answer.
func c2Options(opts []mm.C2Option, names ...string) []reflect.Value {
	o := reflect.ValueOf(mm.NewC2Options(opts...))
	fields := make([]reflect.Value, 0, len(names))

	for _, name := range names {
		f := o.FieldByName(name)
		if !f.IsValid() {
			panic("mm.NewC2Options has no field " + name)
		}

		fields = append(fields, f)
	}

	return fields
}

// sohMM is a minimega whose VMs answer each C2 command with answer, and
// whose GetVMIPv4 answers as dhcpMM does.
type sohMM struct {
	dhcpMM

	answer    func(c2Call) (string, error)
	waitErr   error // returned by WaitForC2Response when set
	clearErr  error // returned by ClearC2Responses when set
	scriptErr error // returned by ReadScriptFromFile when set

	execs     []c2Call
	waits     []c2Wait
	responses []string // by command ID, from 1
	cleared   []string // namespaces whose C2 responses were cleared
	scripts   []string // "namespace file" of each script read
}

func (m *sohMM) ExecC2Command(opts ...mm.C2Option) (string, error) {
	f := c2Options(opts, "ns", "vm", "command", "testConn", "sendFile", "idByUUID")
	call := c2Call{
		NS: f[0].String(), VM: f[1].String(), Command: f[2].String(), TestConn: f[3].String(),
		Send: f[4].String(), UUID: f[5].Bool(),
	}

	resp, err := m.answer(call)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.execs = append(m.execs, call)

	if err != nil {
		return "", err
	}

	m.responses = append(m.responses, resp)

	return strconv.Itoa(len(m.responses)), nil
}

func (m *sohMM) GetC2Response(opts ...mm.C2Option) (string, error) {
	id, err := strconv.Atoi(c2Options(opts, "commandID")[0].String())

	m.mu.Lock()
	defer m.mu.Unlock()

	if err != nil || id < 1 || id > len(m.responses) {
		return "", fmt.Errorf("no C2 command %d", id)
	}

	return m.responses[id-1], nil
}

func (m *sohMM) WaitForC2Response(opts ...mm.C2Option) (string, error) {
	f := c2Options(opts, "vm", "timeout", "appearGrace", "clientGrace")
	wait := c2Wait{
		VM: f[0].String(), Timeout: time.Duration(f[1].Int()),
		AppearGrace: time.Duration(f[2].Int()), ClientGrace: time.Duration(f[3].Int()),
	}

	m.mu.Lock()
	m.waits = append(m.waits, wait)
	m.mu.Unlock()

	if m.waitErr != nil {
		return "", m.waitErr
	}

	return m.GetC2Response(opts...)
}

func (m *sohMM) ClearC2Responses(opts ...mm.C2Option) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cleared = append(m.cleared, c2Options(opts, "ns")[0].String())

	return m.clearErr
}

func (m *sohMM) ReadScriptFromFile(ns, filename string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.scripts = append(m.scripts, ns+" "+filename)

	return m.scriptErr
}

// Headnode names the head node in snapshot names.
func (*sohMM) Headnode() string { return "head" }

// commands lists the C2 commands sent, sorted.
func (m *sohMM) commands() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmds := make([]string, 0, len(m.execs))
	for _, c := range m.execs {
		cmds = append(cmds, c.String())
	}

	slices.Sort(cmds)

	return cmds
}

// checkSent checks that each command in once was sent one time, each command
// in retried was sent one or more times, and no other command was sent.
func checkSent(t *testing.T, call string, fake *sohMM, once, retried []string) {
	t.Helper()

	got := map[string]int{}
	for _, c := range fake.commands() {
		got[c]++
	}

	for _, c := range retried {
		if got[c] < 1 {
			t.Errorf("%s sent %q %d times, want 1 or more", call, c, got[c])
		}

		delete(got, c)
	}

	want := map[string]int{}
	for _, c := range once {
		want[c] = 1
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("%s C2 command counts mismatch (-want +got):\n%s", call, diff)
	}
}

// useSOHMM makes minimega the fake sohMM, answering with answer and the
// GetVMIPv4 replies, until the test ends.
func useSOHMM(t *testing.T, answer func(c2Call) (string, error), replies ...[]string) *sohMM {
	t.Helper()

	fake := &sohMM{dhcpMM: dhcpMM{replies: replies}, answer: answer}
	originalMM := mm.DefaultMM

	mm.DefaultMM = fake //nolint:reassign // install test double

	t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore default

	return fake
}

// answerFrom answers each C2 command, by its String, from answers.
func answerFrom(answers map[string]string) func(c2Call) (string, error) {
	return func(c c2Call) (string, error) {
		if resp, ok := answers[c.String()]; ok {
			return resp, nil
		}

		return "", fmt.Errorf("unexpected C2 command %s", c)
	}
}

func sohIface(name, vlan, proto, addr, gateway string) *v1.Interface {
	return &v1.Interface{NameF: name, VLANF: vlan, ProtoF: proto, AddressF: addr, MaskF: 24, GatewayF: gateway}
}

func sohNode(name, osType string, ifs ...*v1.Interface) *v1.Node {
	return &v1.Node{
		TypeF:     "VirtualMachine",
		GeneralF:  &v1.General{HostnameF: name},
		HardwareF: &v1.Hardware{OSTypeF: osType, DrivesF: []*v1.Drive{{ImageF: "img.qc2"}}},
		NetworkF:  &v1.Network{InterfacesF: ifs},
	}
}

// sohExperiment is experiment exp in the test's store, with the soh app
// configured by md (none when nil) and the other scenario apps.
func sohExperiment(t *testing.T, md map[string]any, nodes []*v1.Node, apps ...*v2.ScenarioApp) *types.Experiment {
	t.Helper()

	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "exp",
			BaseDirF:        t.TempDir(),
			DefaultBridgeF:  "phenix",
			TopologyF:       &v1.TopologySpec{NodesF: nodes},
			ScenarioF:       &v2.ScenarioSpec{AppsF: append([]*v2.ScenarioApp{{NameF: "soh", MetadataF: md}}, apps...)},
		},
		Status: &v1.ExperimentStatus{},
	}

	useStoredExperiment(t, exp)

	return exp
}

// storedExperiment reads experiment exp back from the store.
func storedExperiment(t *testing.T) *types.Experiment {
	t.Helper()

	c, err := store.NewConfig("experiment/exp")
	if err != nil {
		t.Fatalf("store.NewConfig(experiment/exp) error = %v", err)
	}

	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(experiment/exp) error = %v", err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig(experiment/exp) error = %v", err)
	}

	return exp
}

// hostResult is one host's soh results: its CPU load and each check as
// "kind map[metadata] = outcome", sorted.
type hostResult struct {
	CPULoad string
	Checks  []string
}

// storedResults returns the soh results in the store, by host.
func storedResults(t *testing.T) map[string]hostResult {
	t.Helper()

	states, err := hostStates(storedExperiment(t))
	if err != nil {
		t.Fatalf("hostStates(stored exp) error = %v", err)
	}

	results := make(map[string]hostResult, len(states))

	for _, h := range states {
		var checks []string

		for _, kind := range checkKinds(*h) {
			for _, st := range kind.states {
				if _, err := time.Parse(time.RFC3339, st.Timestamp); err != nil {
					t.Errorf("stored %s check %v timestamp %q is not RFC 3339: %v", kind.kind, st.Metadata, st.Timestamp, err)
				}

				outcome := st.Success
				if st.Error != "" {
					outcome = "error: " + st.Error
				}

				checks = append(checks, fmt.Sprintf("%s %v = %s", kind.kind, st.Metadata, outcome))
			}
		}

		slices.Sort(checks)
		results[h.Hostname] = hostResult{CPULoad: h.CPULoad, Checks: checks}
	}

	return results
}

const (
	windowsCPULoad = `powershell -command "Get-WmiObject Win32_Processor | Measure-Object -Property LoadPercentage -Average | Select -ExpandProperty Average"`
	dockerFormat   = `'--format={{if .State.Health}}{{.State.Health.Status}}{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}'`
)

// Running runs every check on each host that boots and is not skipped, and
// stores each result and CPU load for the UI. Reachability pings each address
// of each VLAN from each host, and custom tests reach a host's interface by name.
func TestRunningRecordsEveryCheck(t *testing.T) { //nolint:paralleltest // replaces package globals
	ext, dnb := true, true

	exp := sohExperiment(t, map[string]any{
		"c2Timeout":                 "1m",
		"testReachability":          "full",
		"skipHosts":                 []any{"old"},
		"hostsToUseUUIDForC2Active": "win",
		"testCustomReachability": []any{
			map[string]any{"src": "web", "dst": "win|eth0", "proto": "tcp", "port": 80},
		},
		"hostFiles":        map[string]any{"web": []any{"/etc/motd"}, "old": []any{"/etc/old"}},
		"hostFilesAbsent":  map[string]any{"web": []any{"/tmp/gone"}},
		"hostServices":     map[string]any{"web": []any{"sshd"}},
		"hostProcesses":    map[string]any{"win": []any{"svchost"}, "web": []any{"python3 server.py"}},
		"hostListeners":    map[string]any{"web": []any{":22"}},
		"dockerContainers": map[string]any{"web": []any{"nginx"}},
	}, []*v1.Node{
		{
			TypeF: "VirtualMachine", GeneralF: &v1.General{HostnameF: "ext"}, ExternalF: &ext,
			NetworkF: &v1.Network{InterfacesF: []*v1.Interface{sohIface("eth0", "A", "static", "10.0.0.9", "")}},
		},
		sohNode("web", "linux",
			sohIface("eth0", "A", "static", "10.0.0.1", "10.0.0.254"),
			sohIface("mgmt", "MGMT", "static", "", ""),
			&v1.Interface{NameF: "serial0", TypeF: "serial", VLANF: "S"},
		),
		sohNode("win", "windows", sohIface("eth0", "A", "dhcp", "", "")),
		func() *v1.Node {
			n := sohNode("spare", "linux", sohIface("eth0", "A", "static", "10.0.0.5", ""))
			n.GeneralF.DoNotBootF = &dnb

			return n
		}(),
		sohNode("old", "linux", sohIface("eth0", "A", "static", "10.0.0.6", "")),
	}, &v2.ScenarioApp{NameF: "other", HostsF: []*v2.ScenarioAppHost{{
		HostnameF: "web",
		MetadataF: map[string]any{"sohProfile": map[string]any{"processes": []any{"nginx"}, "listeners": []any{"10.0.0.1:443"}}},
	}}})

	const (
		received = "1 packets transmitted, 1 received"
		listen   = "Netid State\ntcp LISTEN"
	)

	answers := map[string]string{
		"exp/web: ip addr":                                             "inet 10.0.0.1/24 scope global eth0",
		"exp/web: ip route":                                            "default via 10.0.0.254 dev eth0",
		"exp/web: ping -c 1 10.0.0.254":                                received,
		"exp/web: ping -c 1 10.0.0.9":                                  received,
		"exp/web: ping -c 1 10.0.0.1":                                  received,
		"exp/web: ping -c 1 10.0.0.2":                                  received,
		"exp/web: test-conn tcp 10.0.0.2 80 wait 5s":                   "success",
		"exp/web: stat -c present -- /etc/motd":                        "present\n",
		"exp/web: stat -c present -- /tmp/gone":                        "",
		"exp/web: systemctl is-active -- sshd":                         "active",
		"exp/web: pgrep -f -- nginx":                                   "321",
		"exp/web: pgrep -f -- 'python3 server.py'":                     "322",
		"exp/web: ss -lntu state all 'sport = 22'":                     listen,
		"exp/web: ss -lntu state all 'src = 10.0.0.1 and sport = 443'": listen,
		"exp/web: docker inspect " + dockerFormat + " -- nginx":        "healthy",
		"exp/web: cat /proc/loadavg":                                   "0.25 0.30 0.35 1/200 4242",
		"exp/win: ping -n 1 10.0.0.9 (uuid)":                           "Reply from 10.0.0.9",
		"exp/win: ping -n 1 10.0.0.1 (uuid)":                           "Reply from 10.0.0.1",
		"exp/win: ping -n 1 10.0.0.2 (uuid)":                           "Reply from 10.0.0.2",
		`exp/win: powershell -NoProfile -Command "Get-Process -Name 'svchost' -ErrorAction SilentlyContinue` + psID + `" (uuid)`: "412\r\n",
		"exp/win: " + windowsCPULoad + " (uuid)": "7",
	}

	fake := useSOHMM(t, answerFrom(answers), []string{"10.0.0.2"})

	if err := newSOH().Running(t.Context(), exp); err != nil {
		t.Fatalf("Running(exp) error = %v", err)
	}

	// each command once: no check needed a retry
	if diff := cmp.Diff(slices.Sorted(maps.Keys(answers)), fake.commands()); diff != "" {
		t.Errorf("Running(exp) C2 commands mismatch (-want +got):\n%s", diff)
	}

	ping := func(from, to, ip string) string {
		return fmt.Sprintf("reachability map[host:%s ip:%s target:%s] = pinging %s succeeded", from, ip, to, ip)
	}

	conn := "reachability map[host:web ip:10.0.0.2 port:80 proto:tcp target:win] = connection to tcp://10.0.0.2:80 succeeded"

	want := map[string]hostResult{
		"web": {CPULoad: "0.25", Checks: sorted(
			"network map[host:web] = IP 10.0.0.1 configured",
			"network map[host:web] = gateway 10.0.0.254 configured",
			"network map[host:web] = gateway 10.0.0.254 is up",
			ping("web", "ext", "10.0.0.9"), ping("web", "web", "10.0.0.1"), ping("web", "web", "10.0.0.1"),
			ping("web", "win", "10.0.0.2"), ping("win", "web", "10.0.0.1"), conn,
			"file map[host:web path:/etc/motd] = file found",
			"file absent map[host:web path:/tmp/gone] = file not found",
			"service map[host:web service:sshd] = service running",
			"process map[host:web proc:nginx] = process running",
			"process map[host:web proc:python3 server.py] = process running",
			"listener map[host:web port::22] = listening on port",
			"listener map[host:web port:10.0.0.1:443] = listening on port",
			"container map[container:nginx host:web] = container healthy",
		)},
		"win": {CPULoad: "7", Checks: sorted(
			"network map[host:win] = IP 10.0.0.2 configured via DHCP",
			ping("win", "ext", "10.0.0.9"), ping("win", "web", "10.0.0.1"), ping("win", "win", "10.0.0.2"),
			ping("win", "win", "10.0.0.2"), ping("web", "win", "10.0.0.2"), conn,
			"process map[host:win proc:svchost] = process running",
		)},
		"ext": {Checks: sorted(ping("web", "ext", "10.0.0.9"), ping("win", "ext", "10.0.0.9"))},
	}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("Running(exp) stored results mismatch (-want +got):\n%s", diff)
	}

	if !Initialized(storedExperiment(t)) {
		t.Error("Running(exp) stored soh status is not initialized, want initialized")
	}
}

func sorted(s ...string) []string {
	slices.Sort(s)

	return s
}

// runChecks records each failed check, records the later checks of a host whose
// C2 is not active as failed without sending them, skips reachability from and
// to a host whose networking failed, and reports that checks failed.
func TestRunChecksRecordsFailures(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{
		"c2Timeout":        "50ms",
		"testReachability": "full",
		"hostFiles":        map[string]any{"web": []any{"/missing"}, "down": []any{"/x"}},
		"hostServices":     map[string]any{"web": []any{"cron"}},
		"hostProcesses":    map[string]any{"web": []any{"ghost", "$HOME/app"}},
		"hostListeners":    map[string]any{"web": []any{"1:2:3", "80 or sport = 22"}},
		"dockerContainers": map[string]any{"web": []any{"db"}},
		"hostCustomTests": map[string]any{"web": []any{
			map[string]any{"name": "t1"},
			map[string]any{"name": "t2", "testScript": "echo hi"},
		}},
	}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "10.0.0.254")),
		sohNode("down", "linux", sohIface("eth0", "A", "static", "10.0.0.3", "")),
		sohNode("lost", "linux", sohIface("eth0", "A", "static", "10.0.0.4", "")),
	})

	answers := map[string]string{
		"exp/web: ip addr":                                   "inet 10.0.0.1/24",
		"exp/web: ip route":                                  "default via 10.0.0.254",
		"exp/web: ping -c 1 10.0.0.254":                      "1 received",
		"exp/web: ping -c 1 10.0.0.1":                        "1 received",
		"exp/web: ping -c 1 10.0.0.3":                        "1 packets transmitted, 0 received",
		"exp/web: stat -c present -- /missing":               "",
		"exp/web: systemctl is-active -- cron":               "inactive",
		"exp/web: pgrep -f -- ghost":                         "",
		"exp/web: docker inspect " + dockerFormat + " -- db": "unhealthy",
		"exp/web: cat /proc/loadavg":                         "busy",
		"exp/lost: ip addr":                                  "inet 10.0.0.9/24",
		"exp/lost: cat /proc/loadavg":                        "0.50 0.40 0.30 1/90 77",
	}

	answer := answerFrom(answers)
	fake := useSOHMM(t, func(c c2Call) (string, error) {
		if c.VM == "down" {
			return "", fmt.Errorf("cannot execute command: %w", mm.ErrC2ClientNotActive)
		}

		return answer(c)
	})

	s := newSOH(withRetryDelay(time.Millisecond))
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	if err := s.runChecks(t.Context(), exp); err == nil {
		t.Error("runChecks(exp with failing checks) error = nil, want an error")
	}

	// Failed checks that can pass later are retried until c2Timeout.
	retried := []string{
		"exp/lost: ip addr",
		"exp/web: pgrep -f -- ghost",
		"exp/web: stat -c present -- /missing",
		"exp/web: systemctl is-active -- cron",
	}

	// down is tried once, then left out of the later checks
	once := []string{"exp/down: ip addr"}
	for c := range answers {
		if !slices.Contains(retried, c) {
			once = append(once, c)
		}
	}

	checkSent(t, "runChecks(exp)", fake, once, retried)

	reach := func(from, to, ip, outcome string) string {
		return fmt.Sprintf("reachability map[host:%s ip:%s target:%s] = %s", from, ip, to, outcome)
	}

	var (
		noC2      = "error: c2 not active on host"
		noNetHost = "error: networking not configured on host"
		noNetDst  = "error: networking not configured on target"
		pinged    = "pinging 10.0.0.1 succeeded"
		noPings   = "error: no successful pings"
	)

	want := map[string]hostResult{
		"web": {CPULoad: "invalid response for command 'cat /proc/loadavg': busy", Checks: sorted(
			"network map[host:web] = IP 10.0.0.1 configured",
			"network map[host:web] = gateway 10.0.0.254 configured",
			"network map[host:web] = gateway 10.0.0.254 is up",
			reach("web", "web", "10.0.0.1", pinged), reach("web", "web", "10.0.0.1", pinged),
			reach("web", "down", "10.0.0.3", noPings), reach("web", "lost", "10.0.0.4", noNetDst),
			reach("down", "web", "10.0.0.1", noC2), reach("lost", "web", "10.0.0.1", noNetHost),
			"file map[host:web path:/missing] = error: file not found",
			"service map[host:web service:cron] = error: service not active",
			"process map[host:web proc:ghost] = error: process not running",
			"listener map[host:web port:1:2:3] = error: invalid port 1:2:3 provided",
			"process map[host:web proc:$HOME/app] = error: "+errUnsafeC2Arg.Error()+
				`: minimega expands the $ in "$HOME/app" as an environment variable`,
			"listener map[host:web port:80 or sport = 22] = error: "+errUnsafeC2Arg.Error()+
				`: listener "80 or sport = 22" may contain only letters, digits and the characters -_.%*/`,
			"container map[container:db host:web] = error: container unhealthy",
			"custom test map[host:web test:t1] = error: no test script provided",
			"custom test map[host:web test:t2] = error: no output test strings or validators provided",
		)},
		"down": {CPULoad: "c2 not active on host", Checks: sorted(
			"network map[host:down] = error: executing C2 command: cannot execute command: "+mm.ErrC2ClientNotActive.Error(),
			"file map[host:down path:/x] = "+noC2,
			reach("web", "down", "10.0.0.3", noPings),
			reach("down", "web", "10.0.0.1", noC2),
			reach("down", "down", "10.0.0.3", noC2), reach("down", "down", "10.0.0.3", noC2),
			reach("down", "lost", "10.0.0.4", noNetDst),
			reach("lost", "down", "10.0.0.3", noNetHost),
		)},
		"lost": {CPULoad: "0.50", Checks: sorted(
			"network map[host:lost] = error: retry time expired waiting for IP to be set",
			reach("web", "lost", "10.0.0.4", noNetDst),
			reach("down", "lost", "10.0.0.4", noNetDst),
			reach("lost", "web", "10.0.0.1", noNetHost),
			reach("lost", "down", "10.0.0.3", noNetHost),
			reach("lost", "lost", "10.0.0.4", noNetDst), reach("lost", "lost", "10.0.0.4", noNetDst),
		)},
	}

	// lost still has C2, so its CPU load is read
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("runChecks(exp) stored results mismatch (-want +got):\n%s", diff)
	}
}

// A run started with a list of checks runs only those checks, with the C2
// timeout it was started with.
func TestRunChecksRunsOnlyTheChecksAsked(t *testing.T) { //nolint:paralleltest // replaces package globals
	elastic := sohNode("elastic", "linux")
	elastic.LabelsF = map[string]string{"soh-elastic-server": "true"}

	exp := sohExperiment(t, map[string]any{
		"testReachability": "full",
		"hostFiles":        map[string]any{"web": []any{"/etc/motd"}},
		"hostServices":     map[string]any{"web": []any{"sshd"}},
	}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
		elastic,
	})

	fake := useSOHMM(t, answerFrom(map[string]string{
		"exp/web: stat -c present -- /etc/motd": "present",
		"exp/elastic: query-flows.sh":           flowsResponse(flowHit{"10.0.0.1", 30, "10.0.0.2", 20}),
	}))

	s := newSOH()
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"files", "flows"}, "c2Timeout": "42s"})

	if err := s.runChecks(ctx, exp); err != nil {
		t.Fatalf("runChecks(files, flows) error = %v", err)
	}

	wantCmds := []string{"exp/elastic: query-flows.sh", "exp/web: stat -c present -- /etc/motd"}
	if diff := cmp.Diff(wantCmds, fake.commands()); diff != "" {
		t.Errorf("runChecks(files, flows) C2 commands mismatch (-want +got):\n%s", diff)
	}

	if s.md.c2Timeout != 42*time.Second {
		t.Errorf("runChecks(c2Timeout 42s) C2 timeout = %v, want 42s", s.md.c2Timeout)
	}

	want := map[string]hostResult{"web": {Checks: []string{"file map[host:web path:/etc/motd] = file found"}}}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("runChecks(files, flows) stored results mismatch (-want +got):\n%s", diff)
	}

	hosts, flows, err := GetFlowsFor(storedExperiment(t))
	if err != nil {
		t.Fatalf("GetFlowsFor(stored exp) error = %v", err)
	}

	wantFlows := map[string]map[string]int{"10.0.0.1": {"10.0.0.1": 0, "10.0.0.2": 30}, "10.0.0.2": {"10.0.0.1": 20, "10.0.0.2": 0}}
	if diff := cmp.Diff(wantFlows, flowMatrix(hosts, flows)); diff != "" {
		t.Errorf("GetFlowsFor(stored exp) flows mismatch (-want +got):\n%s", diff)
	}
}

// runChecks lists each address of a host once in its VLAN, however many
// static interfaces the host has.
func TestRunChecksListsEachAddressOnce(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{}, []*v1.Node{
		sohNode("web", "linux",
			sohIface("eth0", "A", "static", "10.0.0.1", ""),
			sohIface("eth1", "B", "static", "10.0.1.1", ""),
			sohIface("eth2", "B", "static", "10.0.1.2", ""),
		),
	})

	useSOHMM(t, answerFrom(map[string]string{"exp/web: ip addr": "inet 10.0.0.1/24 inet 10.0.1.1/24 inet 10.0.1.2/24"}))

	s := newSOH()
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"network-config"}})

	if err := s.runChecks(ctx, exp); err != nil {
		t.Fatalf("runChecks(network-config) error = %v", err)
	}

	want := map[string][]string{"A": {"10.0.0.1"}, "B": {"10.0.1.1", "10.0.1.2"}}
	if diff := cmp.Diff(want, s.vlans); diff != "" {
		t.Errorf("runChecks(web with three static interfaces) vlans mismatch (-want +got):\n%s", diff)
	}
}

// runChecks stops, with the context's error, once the context is done.
func TestRunChecksStopsWhenTheContextEnds(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{"hostFiles": map[string]any{"web": []any{"/etc/motd"}}}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
	})

	fake := useSOHMM(t, answerFrom(map[string]string{"exp/web: ip addr": "inet 10.0.0.1/24"}))

	s := newSOH()
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := s.runChecks(ctx, exp); !errors.Is(err, context.Canceled) {
		t.Errorf("runChecks(done context) error = %v, want %v", err, context.Canceled)
	}

	if diff := cmp.Diff([]string{"exp/web: ip addr"}, fake.commands()); diff != "" {
		t.Errorf("runChecks(done context) C2 commands mismatch (-want +got):\n%s", diff)
	}
}

// flowHit is one flow that Elasticsearch reports.
type flowHit struct {
	src      string
	srcBytes int
	dst      string
	dstBytes int
}

// flowsResponse is the Elasticsearch search result query-flows.sh prints for hits.
func flowsResponse(hits ...flowHit) string {
	sources := make([]string, 0, len(hits))
	for _, h := range hits {
		sources = append(sources, fmt.Sprintf(
			`{"_source":{"source":{"ip":%q,"bytes":%d},"destination":{"ip":%q,"bytes":%d}}}`,
			h.src, h.srcBytes, h.dst, h.dstBytes,
		))
	}

	return `{"hits":{"total":{"value":1},"hits":[` + strings.Join(sources, ",") + `]}}`
}

// flowMatrix returns flows as bytes sent from each host to each host.
func flowMatrix(hosts []string, flows [][]int) map[string]map[string]int {
	if hosts == nil && flows == nil {
		return nil
	}

	matrix := make(map[string]map[string]int, len(hosts))

	for i, src := range hosts {
		matrix[src] = make(map[string]int, len(hosts))

		for j, dst := range hosts {
			if i < len(flows) && j < len(flows[i]) {
				matrix[src][dst] = flows[i][j]
			}
		}
	}

	return matrix
}

// packetFlows returns the flows getFlows recorded, nil when it recorded none.
func packetFlows(t *testing.T, s *SOH) map[string]map[string]int {
	t.Helper()

	if len(s.packetCapture) == 0 {
		return nil
	}

	hosts, _ := s.packetCapture["hosts"].([]string)
	flows, _ := s.packetCapture["flows"].([][]int)

	if len(hosts) != len(flows) {
		t.Errorf("getFlows() recorded %d hosts and %d rows of flows, want as many rows as hosts", len(hosts), len(flows))
	}

	return flowMatrix(hosts, flows)
}

// getFlows asks the Elastic server for the flows and records the bytes each
// host sent to each host, waiting while the server's C2 is not active.
func TestGetFlows(t *testing.T) { //nolint:paralleltest // replaces package globals
	errExec := errors.New("exec failed")
	notActive := fmt.Errorf("cannot execute command: %w", mm.ErrC2ClientNotActive)

	tests := map[string]struct {
		noServer bool
		answers  []any // each call's answer, the last one repeated: a response or an error
		waitErr  error
		want     map[string]map[string]int
		calls    int
	}{
		"flows of each pair": {
			answers: []any{flowsResponse(
				flowHit{"10.0.0.1", 100, "10.0.0.2", 40},
				flowHit{"10.0.0.1", 5, "10.0.0.3", 7},
				flowHit{"10.0.0.2", 1, "10.0.0.1", 2},
				flowHit{"10.0.0.3", 9, "10.0.0.3", 4},
			)},
			want: map[string]map[string]int{
				"10.0.0.1": {"10.0.0.1": 0, "10.0.0.2": 102, "10.0.0.3": 5},
				"10.0.0.2": {"10.0.0.1": 41, "10.0.0.2": 0, "10.0.0.3": 0},
				"10.0.0.3": {"10.0.0.1": 7, "10.0.0.2": 0, "10.0.0.3": 13},
			},
			calls: 1,
		},
		"after C2 becomes active": {
			answers: []any{notActive, notActive, flowsResponse(flowHit{"a", 1, "b", 2})},
			want:    map[string]map[string]int{"a": {"a": 0, "b": 1}, "b": {"a": 2, "b": 0}},
			calls:   3,
		},
		"no Elastic server":         {noServer: true, calls: 0},
		"command fails":             {answers: []any{errExec}, calls: 1},
		"no response":               {answers: []any{"{}"}, waitErr: errExec, calls: 1},
		"response is not JSON":      {answers: []any{"not json"}, calls: 1},
		"no hits":                   {answers: []any{"{}"}, calls: 1},
		"empty hits":                {answers: []any{`{"hits":{"hits":[]}}`}, calls: 1},
		"hit source is not a flow":  {answers: []any{`{"hits":{"hits":[{"_source":"flow"}]}}`}, calls: 1},
		"hit without a flow source": {answers: []any{`{"hits":{"hits":[{"_id":"1"}]}}`}, calls: 1},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var nodes []*v1.Node
			if !tc.noServer {
				elastic := sohNode("elastic", "linux")
				elastic.LabelsF = map[string]string{"soh-elastic-server": "true"}
				nodes = append(nodes, elastic)
			}

			exp := sohExperiment(t, map[string]any{"hostsToUseUUIDForC2Active": "elastic"}, nodes)

			fake := useSOHMM(t, nil)
			fake.waitErr = tc.waitErr
			fake.answer = func(c2Call) (string, error) {
				answer := tc.answers[min(len(fake.execs), len(tc.answers)-1)]
				if err, ok := answer.(error); ok {
					return "", err
				}

				return answer.(string), nil //nolint:forcetypeassert // every answer is an error or a string
			}

			s := newSOH(withRetryDelay(time.Millisecond))
			if err := s.decodeMetadata(exp); err != nil {
				t.Fatalf("decodeMetadata(exp) error = %v", err)
			}

			s.getFlows(t.Context(), exp)

			if diff := cmp.Diff(tc.want, packetFlows(t, s)); diff != "" {
				t.Errorf("getFlows() flows mismatch (-want +got):\n%s", diff)
			}

			wantCmds := slices.Repeat([]string{"exp/elastic: query-flows.sh (uuid)"}, tc.calls)
			if diff := cmp.Diff(wantCmds, fake.commands(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("getFlows() C2 commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// getFlows stops waiting for the Elastic server's C2 once the context is done.
func TestGetFlowsStopsWhenTheContextEnds(t *testing.T) { //nolint:paralleltest // replaces package globals
	elastic := sohNode("elastic", "linux")
	elastic.LabelsF = map[string]string{"soh-elastic-server": "true"}
	exp := sohExperiment(t, map[string]any{}, []*v1.Node{elastic})

	ctx, cancel := context.WithCancel(t.Context())

	fake := useSOHMM(t, func(c2Call) (string, error) {
		cancel()

		return "", mm.ErrC2ClientNotActive
	})

	done := make(chan struct{})

	go func() {
		defer close(done)

		newSOH(withRetryDelay(time.Hour)).getFlows(ctx, exp)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("getFlows(ctx done while C2 is not active) did not return")
	}

	if got := len(fake.commands()); got != 1 {
		t.Errorf("getFlows(ctx done while C2 is not active) sent %d C2 commands, want 1", got)
	}
}

// soh app metadata with one capture host, web's eth0.
func captureMetadata(ip string) map[string]any {
	return map[string]any{
		"packetCapture": map[string]any{
			"elasticImage":    "elastic.qc2",
			"packetBeatImage": "packetbeat.qc2",
			"elasticServer":   map[string]any{"hostname": "elastic", "ipAddress": ip, "vlan": "A"},
			"captureHosts":    map[string]any{"web": []any{"eth0"}},
		},
	}
}

// PostStart deploys packet capture and runs the checks once, failing only for
// exitOnError, and runs nothing but the capture script for a dry run. A
// capture host that is not in the topology gets no monitor and no error.
func TestPostStart(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := map[string]struct {
		md        map[string]any
		dryRun    bool
		scriptErr bool // minimega cannot read the capture script
		wantErr   bool
		cmds      []string // C2 commands sent, once each
		retried   []string // C2 commands sent one or more times
		written   bool     // whether the capture script is written
		read      bool     // whether the capture script is read
		results   map[string]hostResult
	}{
		"dry run": {
			md: captureMetadata("10.1.0.1/24"), dryRun: true, written: true,
		},
		"runs the checks and deploys capture": {
			md:      merge(captureMetadata("10.1.0.1/24"), map[string]any{"hostFiles": map[string]any{"web": []any{"/etc/motd"}}}),
			cmds:    []string{"exp/web: cat /proc/loadavg", "exp/web: stat -c present -- /etc/motd"},
			written: true,
			read:    true,
			results: map[string]hostResult{"web": {CPULoad: "0.1", Checks: []string{"file map[host:web path:/etc/motd] = file found"}}},
		},
		"keeps going after failed checks": {
			md:      map[string]any{"hostFiles": map[string]any{"web": []any{"/missing"}}},
			cmds:    []string{"exp/web: cat /proc/loadavg"},
			retried: []string{"exp/web: stat -c present -- /missing"},
			results: map[string]hostResult{
				"web": {CPULoad: "0.1", Checks: []string{"file map[host:web path:/missing] = error: file not found"}},
			},
		},
		"fails after failed checks with exitOnError": {
			md:      map[string]any{"exitOnError": true, "hostFiles": map[string]any{"web": []any{"/missing"}}},
			wantErr: true,
			cmds:    []string{"exp/web: cat /proc/loadavg"},
			retried: []string{"exp/web: stat -c present -- /missing"},
			results: map[string]hostResult{
				"web": {CPULoad: "0.1", Checks: []string{"file map[host:web path:/missing] = error: file not found"}},
			},
		},
		"skips a capture host not in the topology": {
			md: merge(captureMetadata("10.1.0.1/24"), map[string]any{"exitOnError": true, "packetCapture": map[string]any{
				"elasticServer": map[string]any{"ipAddress": "10.1.0.1/24"},
				"captureHosts":  map[string]any{"nosuch": []any{"eth0"}},
			}}),
			cmds:    []string{"exp/web: cat /proc/loadavg"},
			results: map[string]hostResult{"web": {CPULoad: "0.1"}},
		},
		"keeps going after failed capture": {
			md:        captureMetadata("10.1.0.1/24"),
			scriptErr: true,
			cmds:      []string{"exp/web: cat /proc/loadavg"},
			written:   true,
			read:      true,
			results:   map[string]hostResult{"web": {CPULoad: "0.1"}},
		},
		"fails after failed capture with exitOnError": {
			md:        merge(captureMetadata("10.1.0.1/24"), map[string]any{"exitOnError": true}),
			scriptErr: true,
			wantErr:   true,
			written:   true,
			read:      true,
		},
		"waits the startup delay": {
			md:      map[string]any{"startupDelay": "1ms"},
			cmds:    []string{"exp/web: cat /proc/loadavg"},
			results: map[string]hostResult{"web": {CPULoad: "0.1"}},
		},
		"no soh metadata": {wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			md := tc.md
			if md != nil {
				md = merge(md, map[string]any{"skipInitialNetworkConfigTests": true})
			}

			exp := sohExperiment(t, md, []*v1.Node{sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", ""))})
			fake := useSOHMM(t, answerFrom(map[string]string{
				"exp/web: stat -c present -- /etc/motd": "present",
				"exp/web: stat -c present -- /missing":  "",
				"exp/web: cat /proc/loadavg":            "0.1 0.2 0.3 1/2 3",
			}))
			if tc.scriptErr {
				fake.scriptErr = errors.New("read failed")
			}

			s := newSOH(withRetryDelay(time.Millisecond))
			if err := s.Init(app.DryRun(tc.dryRun)); err != nil {
				t.Fatalf("Init(DryRun(%t)) error = %v", tc.dryRun, err)
			}

			if err := s.PostStart(t.Context(), exp); (err != nil) != tc.wantErr {
				t.Errorf("PostStart(exp) error = %v, want error %t", err, tc.wantErr)
			}

			checkSent(t, "PostStart(exp)", fake, tc.cmds, tc.retried)

			script := filepath.Join(exp.Spec.BaseDir(), "mm_files", "exp-monitor.mm")

			var wantScripts []string
			if tc.read {
				wantScripts = []string{"exp " + script}
			}

			if diff := cmp.Diff(wantScripts, fake.scripts); diff != "" {
				t.Errorf("PostStart(exp) scripts read mismatch (-want +got):\n%s", diff)
			}

			if tc.results != nil {
				if diff := cmp.Diff(tc.results, storedResults(t)); diff != "" {
					t.Errorf("PostStart(exp) stored results mismatch (-want +got):\n%s", diff)
				}
			}

			body, err := os.ReadFile(script)
			if (err == nil) != tc.written {
				t.Fatalf("PostStart(exp) capture script written = %t, want %t (%v)", err == nil, tc.written, err)
			}

			for _, line := range []string{"vm launch kvm web-soh-monitor", "tap mirror web 0 web-soh-monitor 1"} {
				if tc.written && !slices.Contains(strings.Split(string(body), "\n"), line) {
					t.Errorf("PostStart(exp) capture script %s does not have the line %q:\n%s", script, line, body)
				}
			}
		})
	}
}

// merge returns a copy of a with the entries of b.
func merge(a, b map[string]any) map[string]any {
	m := maps.Clone(a)
	maps.Copy(m, b)

	return m
}

// Configure adds the Elastic server for packet capture, or removes it when
// there is nothing to capture, and adds or removes the SoH ICMP rules.
func TestConfigure(t *testing.T) { //nolint:paralleltest // replaces package globals
	sohRule := &v1.Rule{IDF: 10, DescriptionF: "[SOH ICMP ALL] Allow all ICMP for SoH reachability", ActionF: "accept", ProtocolF: "icmp",
		SourceF: &v1.AddrPort{AddressF: "0.0.0.0/0"}, DestinationF: &v1.AddrPort{AddressF: "0.0.0.0/0"}}
	userRule := &v1.Rule{IDF: 20, DescriptionF: "user", ActionF: "drop"}

	tests := map[string]struct {
		md      map[string]any
		server  bool // the topology already has an Elastic server
		rules   []*v1.Rule
		want    []string // nodes after Configure
		wantIDs []int    // rule IDs after Configure
		wantErr bool
		addrErr bool // the error is errElasticServerAddress
	}{
		"adds the Elastic server": {
			md: captureMetadata("10.1.0.1/24"), rules: []*v1.Rule{userRule},
			want: []string{"web", "elastic"}, wantIDs: []int{20},
		},
		"keeps the Elastic server": {
			md: captureMetadata("10.1.0.1/24"), server: true, rules: []*v1.Rule{userRule},
			want: []string{"web", "elastic"}, wantIDs: []int{20},
		},
		"removes the Elastic server": {
			md: map[string]any{}, server: true, rules: []*v1.Rule{sohRule, userRule},
			want: []string{"web"}, wantIDs: []int{20},
		},
		"adds ICMP rules": {
			md: map[string]any{"testReachability": "full", "injectICMPAllow": true}, rules: []*v1.Rule{userRule},
			want: []string{"web"}, wantIDs: []int{10, 20},
		},
		"keeps ICMP rules": {
			md: map[string]any{"testReachability": "full", "injectICMPAllow": true}, rules: []*v1.Rule{sohRule, userRule},
			want: []string{"web"}, wantIDs: []int{10, 20},
		},
		"no room for ICMP rules": {
			md: map[string]any{"testReachability": "full", "injectICMPAllow": true}, rules: []*v1.Rule{{IDF: 1}},
			want: []string{"web"}, wantIDs: []int{1}, wantErr: true,
		},
		"bad Elastic server address": {
			md: captureMetadata("10.1.0.1"), want: []string{"web"}, wantErr: true, addrErr: true,
		},
		"IPv6 Elastic server address": {
			md: captureMetadata("fd00::1/64"), want: []string{"web"}, wantErr: true, addrErr: true,
		},
		"IPv4-mapped Elastic server address": {
			md: captureMetadata("::ffff:10.1.0.1/120"), want: []string{"web"}, wantErr: true, addrErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			web := sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", ""))
			web.NetworkF.RulesetsF = []*v1.Ruleset{{NameF: "in", RulesF: slices.Clone(tc.rules)}}

			nodes := []*v1.Node{web}
			if tc.server {
				elastic := sohNode("elastic", "linux")
				elastic.LabelsF = map[string]string{"soh-elastic-server": "true"}
				nodes = append(nodes, elastic)
			}

			exp := sohExperiment(t, tc.md, nodes)

			err := newSOH().Configure(t.Context(), exp)
			if (err != nil) != tc.wantErr || tc.addrErr && !errors.Is(err, errElasticServerAddress) {
				t.Fatalf("Configure(exp) error = %v, want error %t (address error %t)", err, tc.wantErr, tc.addrErr)
			}

			got := make([]string, 0, len(tc.want))
			for _, n := range exp.Spec.Topology().Nodes() {
				got = append(got, n.General().Hostname())
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Configure(exp) nodes mismatch (-want +got):\n%s", diff)
			}

			ids := make([]int, 0, len(tc.wantIDs))
			for _, r := range web.NetworkF.RulesetsF[0].RulesF {
				ids = append(ids, r.IDF)
			}

			if diff := cmp.Diff(tc.wantIDs, ids, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Configure(exp) rule IDs mismatch (-want +got):\n%s", diff)
			}

			if name == "adds ICMP rules" {
				if diff := cmp.Diff(sohRule, web.NetworkF.RulesetsF[0].RulesF[0]); diff != "" {
					t.Errorf("Configure(exp) ICMP rule mismatch (-want +got):\n%s", diff)
				}
			}

			if name != "adds the Elastic server" {
				return
			}

			server := exp.Spec.Topology().FindNodeByName("elastic")
			if server.Labels()["soh-elastic-server"] != "true" {
				t.Errorf("Configure(exp) Elastic server labels = %v, want soh-elastic-server", server.Labels())
			}

			hw := server.Hardware()
			if hw.VCPU() != 4 || hw.Memory() != 4096 || hw.OSType() != "linux" || hw.Drives()[0].Image() != "elastic.qc2" {
				t.Errorf("Configure(exp) Elastic server hardware = %d CPUs, %d MB, %s, %s; want 4, 4096, linux, elastic.qc2",
					hw.VCPU(), hw.Memory(), hw.OSType(), hw.Drives()[0].Image())
			}

			iface := server.Network().Interfaces()[0]
			gotIface := []any{iface.Name(), iface.VLAN(), iface.Address(), iface.Mask(), iface.Proto(), iface.Bridge()}
			if diff := cmp.Diff([]any{"IF0", "MGMT", "10.1.0.1", 24, "static", "phenix"}, gotIface); diff != "" {
				t.Errorf("Configure(exp) Elastic server interface mismatch (-want +got):\n%s", diff)
			}

			for _, file := range []string{"elastic-elasticsearch.yml", "elastic-kibana.yml"} {
				if _, err := os.Stat(filepath.Join(exp.Spec.BaseDir(), "startup", file)); err != nil {
					t.Errorf("Configure(exp) did not write startup/%s: %v", file, err)
				}
			}
		})
	}
}

// Cleanup clears the experiment's C2 responses.
func TestCleanup(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := &types.Experiment{Spec: &v1.ExperimentSpec{ExperimentNameF: "exp"}}

	fake := useSOHMM(t, nil)

	if err := newSOH().Cleanup(t.Context(), exp); err != nil {
		t.Errorf("Cleanup(exp) error = %v", err)
	}

	fake.clearErr = errors.New("clear failed")

	if err := newSOH().Cleanup(t.Context(), exp); !errors.Is(err, fake.clearErr) {
		t.Errorf("Cleanup(exp) error = %v, want %v", err, fake.clearErr)
	}

	if diff := cmp.Diff([]string{"exp", "exp"}, fake.cleared); diff != "" {
		t.Errorf("Cleanup(exp) cleared namespaces mismatch (-want +got):\n%s", diff)
	}
}

// The app is named soh and does nothing before the experiment starts.
func TestSOHNameAndPreStart(t *testing.T) {
	t.Parallel()

	s := newSOH()
	if got := s.Name(); got != "soh" {
		t.Errorf("Name() = %q, want %q", got, "soh")
	}

	if err := s.PreStart(t.Context(), &types.Experiment{}); err != nil {
		t.Errorf("PreStart() error = %v", err)
	}
}

// The soh app registers itself with phenix under its name.
func TestSOHIsRegistered(t *testing.T) {
	t.Parallel()

	if got, ok := app.GetApp("soh").(*SOH); !ok || got.Name() != "soh" {
		t.Errorf("app.GetApp(soh) = %T, want *SOH", app.GetApp("soh"))
	}
}

// gatherNodeIPs records each address of a node for its host, its VLAN (when
// it has one) and its interface.
func TestGatherNodeIPs(t *testing.T) {
	t.Parallel()

	s := newSOH()
	s.vlans["A"] = []string{"10.0.0.9"}
	s.hostIPs["web"] = map[string]string{"old": "10.9.9.9"}

	var node ifaces.NodeSpec = sohNode("web", "linux",
		sohIface("eth0", "A", "static", "10.0.0.1", ""),
		sohIface("eth1", "B", "dhcp", "", ""),
		sohIface("eth2", "", "static", "10.0.2.1", ""),
	)

	s.gatherNodeIPs(node)

	if diff := cmp.Diff(map[string]string{"10.0.0.1": "web", "10.0.2.1": "web"}, s.addrHosts); diff != "" {
		t.Errorf("gatherNodeIPs(web) addrHosts mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string][]string{"A": {"10.0.0.9", "10.0.0.1"}}, s.vlans); diff != "" {
		t.Errorf("gatherNodeIPs(web) vlans mismatch (-want +got):\n%s", diff)
	}

	wantIPs := map[string]map[string]string{"web": {"old": "10.9.9.9", "eth0": "10.0.0.1", "eth2": "10.0.2.1"}}
	if diff := cmp.Diff(wantIPs, s.hostIPs); diff != "" {
		t.Errorf("gatherNodeIPs(web) hostIPs mismatch (-want +got):\n%s", diff)
	}
}

// writeResults stores the host results and packet capture flows, keeping the
// rest of the soh status, and writes no hosts before any results exist.
func TestWriteResults(t *testing.T) { //nolint:paralleltest // replaces the default store
	exp := summaryExperiment(map[string]any{"initialized": true, "custom": "kept"})
	useStoredExperiment(t, exp)

	s := newSOH()
	s.writeResults(exp)

	status, _ := storedExperiment(t).Status.AppStatus()["soh"].(map[string]any)
	if diff := cmp.Diff(map[string]any{"initialized": true, "custom": "kept"}, status); diff != "" {
		t.Errorf("writeResults() without results soh status mismatch (-want +got):\n%s", diff)
	}

	s.status["web"] = HostState{Hostname: "web", CPULoad: "0.5", Files: []State{{Success: "file found", Timestamp: "2026-11-03T00:00:00Z"}}}
	s.packetCapture["hosts"] = []string{"a"}
	s.packetCapture["flows"] = [][]int{{1}}
	s.writeResults(exp)

	stored := storedExperiment(t)
	status, _ = stored.Status.AppStatus()["soh"].(map[string]any)

	if !Initialized(stored) || status["custom"] != "kept" {
		t.Errorf("writeResults() soh status = %v, want initialized and custom kept", status)
	}

	want := map[string]hostResult{"web": {CPULoad: "0.5", Checks: []string{"file map[] = file found"}}}
	if diff := cmp.Diff(want, storedResults(t)); diff != "" {
		t.Errorf("writeResults() stored results mismatch (-want +got):\n%s", diff)
	}

	hosts, flows, err := GetFlowsFor(stored)
	if err != nil || !slices.Equal(hosts, []string{"a"}) || !cmp.Equal(flows, [][]int{{1}}) {
		t.Errorf("GetFlowsFor(stored) = %v, %v, %v; want [a], [[1]], nil", hosts, flows, err)
	}
}
