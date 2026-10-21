package soh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/mitchellh/mapstructure"

	"phenix/app"
	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/plog"
)

const (
	testExpName    = "soh-test"
	testLoadAvg    = "0.10 0.20 0.30 1/100 1234"
	testCPULoad    = "0.10"
	testWinCPULoad = "12"
	barrierTimeout = 30 * time.Second
)

// fakeMM answers the minimega calls soh makes. soh calls it from many
// goroutines at once, so everything it changes is guarded.
type fakeMM struct {
	mm.MM

	// vms is what GetVMInfo returns, by VM name. It must not change while soh
	// runs.
	vms map[string]mm.VM
	// responses maps a command prefix to the response for the command.
	responses map[string]string
	// barrier, if set, holds each WaitForC2Response call until all of them have
	// arrived, so the goroutines that make them overlap.
	barrier *barrier
	// noC2 holds the VMs whose C2 client never becomes active.
	noC2 map[string]bool

	ids      atomic.Int64
	commands sync.Map // command ID -> command

	mu       sync.Mutex
	attempts map[string][]string // VM -> commands sent to it
	scripts  []string            // scripts read into minimega
}

func (f *fakeMM) GetVMInfo(opts ...mm.Option) mm.VMs {
	vm, ok := f.vms[optionField(mm.NewOptions(opts...), "vm")]
	if !ok {
		return mm.VMs{}
	}

	return mm.VMs{vm}
}

func (f *fakeMM) ExecC2Command(opts ...mm.C2Option) (string, error) {
	o := mm.NewC2Options(opts...)
	vm, command := optionField(o, "vm"), optionField(o, "command")

	f.mu.Lock()

	if f.attempts == nil {
		f.attempts = make(map[string][]string)
	}

	f.attempts[vm] = append(f.attempts[vm], command)
	f.mu.Unlock()

	if f.noC2[vm] {
		// What minimega returns once c2Timeout passes with no C2 client.
		return "", fmt.Errorf("cannot execute command: %w", mm.ErrC2ClientNotActive)
	}

	id := strconv.FormatInt(f.ids.Add(1), 10)
	f.commands.Store(id, command)

	return id, nil
}

func (f *fakeMM) GetC2Response(opts ...mm.C2Option) (string, error) {
	id := optionField(mm.NewC2Options(opts...), "commandID")

	v, ok := f.commands.Load(id)
	if !ok {
		return "", fmt.Errorf("unknown C2 command ID %q", id)
	}

	command, _ := v.(string)

	for prefix, resp := range f.responses {
		if strings.HasPrefix(command, prefix) {
			return resp, nil
		}
	}

	return "", fmt.Errorf("no response for C2 command %q", command)
}

// sent returns the commands sent to vm.
func (f *fakeMM) sent(vm string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.attempts[vm])
}

func (f *fakeMM) WaitForC2Response(opts ...mm.C2Option) (string, error) {
	if f.barrier != nil {
		if err := f.barrier.wait(); err != nil {
			return "", err
		}
	}

	return f.GetC2Response(opts...)
}

func (*fakeMM) Headnode() string {
	return "headnode"
}

func (f *fakeMM) ReadScriptFromFile(_, filename string) error {
	script, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.scripts = append(f.scripts, string(script))

	return nil
}

// readScripts returns the scripts read into minimega.
func (f *fakeMM) readScripts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.scripts)
}

// optionField reads a string field of the options that mm.NewOptions or
// mm.NewC2Options builds. mm does not export these fields.
func optionField(opts any, name string) string {
	field := reflect.ValueOf(opts).FieldByName(name)
	if field.Kind() != reflect.String {
		panic(fmt.Sprintf("mm options have no string field %q", name))
	}

	return field.String()
}

// barrier releases its callers when n of them are waiting.
type barrier struct {
	n       int64
	arrived atomic.Int64
	release chan struct{}
}

func newBarrier(n int) *barrier {
	return &barrier{n: int64(n), release: make(chan struct{})}
}

func (b *barrier) wait() error {
	if b.arrived.Add(1) == b.n {
		close(b.release)
	}

	select {
	case <-b.release:
		return nil
	case <-time.After(barrierTimeout):
		return fmt.Errorf("only %d of %d callers reached the barrier", b.arrived.Load(), b.n)
	}
}

// useFakes installs f as the minimega client and a store that holds no
// experiments, so soh's status writes do nothing.
func useFakes(t *testing.T, f *fakeMM) {
	t.Helper()

	ctrl := gomock.NewController(t)
	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).Return(errors.New("store unavailable")).AnyTimes()

	origStore, origMM := store.DefaultStore, mm.DefaultMM
	store.DefaultStore = m //nolint:reassign // monkey patching for test
	mm.DefaultMM = f       //nolint:reassign // install test double

	t.Cleanup(func() {
		store.DefaultStore = origStore //nolint:reassign // restore after test
		mm.DefaultMM = origMM          //nolint:reassign // restore after test
	})
}

// newTestExperiment builds an experiment with the given topology nodes, soh
// metadata and other scenario apps.
func newTestExperiment(
	t *testing.T,
	nodes []map[string]any,
	md map[string]any,
	apps ...map[string]any,
) *types.Experiment {
	t.Helper()

	scenarioApps := append([]map[string]any{{"name": appName, "metadata": md}}, apps...)

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: testExpName},
		Spec: map[string]any{
			"experimentName": testExpName,
			"topology":       map[string]any{"nodes": nodes},
			"scenario":       map[string]any{"apps": scenarioApps},
		},
	}

	exp, err := types.DecodeExperimentFromConfig(c)
	if err != nil {
		t.Fatalf("decoding experiment: %v", err)
	}

	return exp
}

func linuxNode(host string, ifaces ...map[string]any) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": host},
		"hardware": map[string]any{"os_type": osTypeLinux},
		"network":  map[string]any{"interfaces": ifaces},
	}
}

func dhcpIface(name, vlan string) map[string]any {
	return map[string]any{"name": name, "type": "ethernet", "vlan": vlan, "proto": "dhcp"}
}

func staticIface(name, addr, gateway string) map[string]any {
	return map[string]any{
		"name":    name,
		"type":    "ethernet",
		"vlan":    "EXP",
		"proto":   "static",
		"address": addr,
		"mask":    24,
		"gateway": gateway,
	}
}

// checksContext returns a context that runs only the given checks, or the
// default checks if none are given, and drops soh's logs.
func checksContext(t *testing.T, checks ...string) context.Context {
	t.Helper()

	ctx := plog.ContextWithLogger(t.Context(), slog.New(slog.DiscardHandler), plog.TypeSoh)

	if len(checks) == 0 {
		return ctx
	}

	return app.SetContextMetadata(ctx, map[string]any{"checks": checks})
}

func sortedValues(m map[string][]string) map[string][]string {
	sorted := make(map[string][]string, len(m))

	for k, v := range m {
		sorted[k] = slices.Sorted(slices.Values(v))
	}

	return sorted
}

// TestRunChecksRecordsEveryDHCPLease drives the DHCP goroutines alongside the
// static and external hosts that runChecks handles on its own goroutine. Under
// -race this fails if any of them write the shared IP maps concurrently, and
// the counts catch lost appends.
func TestRunChecksRecordsEveryDHCPLease(t *testing.T) {
	const dhcpHosts, staticHosts = 8, 4

	var (
		nodes     = make([]map[string]any, 0, dhcpHosts+staticHosts+2) // plus mixed and plc
		vms       = make(map[string]mm.VM)
		staticIPs = make([]string, 0, staticHosts+1)
		wantVLANs = map[string][]string{}
		wantIPs   = map[string]map[string]string{}
	)

	for i := range dhcpHosts {
		host := fmt.Sprintf("dhcp-%d", i)
		addrs := []string{fmt.Sprintf("10.1.0.%d", i+1), fmt.Sprintf("10.2.0.%d", i+1)}

		nodes = append(nodes, linuxNode(host, dhcpIface("IF0", "EXP"), dhcpIface("IF1", "EXP2")))
		vms[host] = mm.VM{Name: host, IPv4: addrs}
		wantVLANs["EXP"] = append(wantVLANs["EXP"], addrs[0])
		wantVLANs["EXP2"] = append(wantVLANs["EXP2"], addrs[1])
		wantIPs[host] = map[string]string{"IF0": addrs[0], "IF1": addrs[1]}
	}

	// A host with one DHCP and one static interface shares an inner hostIPs map
	// between its DHCP goroutine and the static path.
	nodes = append(nodes, linuxNode(
		"mixed",
		dhcpIface("IF0", "EXP"),
		staticIface("IF1", "10.0.0.100", ""),
	))
	vms["mixed"] = mm.VM{Name: "mixed", IPv4: []string{"10.1.0.100", "10.0.0.100"}}
	staticIPs = append(staticIPs, "10.0.0.100/24")
	wantVLANs["EXP"] = append(wantVLANs["EXP"], "10.1.0.100", "10.0.0.100")
	wantIPs["mixed"] = map[string]string{"IF0": "10.1.0.100", "IF1": "10.0.0.100"}

	for i := range staticHosts {
		host := fmt.Sprintf("static-%d", i)
		addr := fmt.Sprintf("10.0.0.%d", i+1)

		nodes = append(nodes, linuxNode(host, staticIface("IF0", addr, "")))
		staticIPs = append(staticIPs, addr+"/24")
		wantVLANs["EXP"] = append(wantVLANs["EXP"], addr)
		wantIPs[host] = map[string]string{"IF0": addr}
	}

	external := linuxNode("plc", staticIface("IF0", "10.0.0.200", ""))
	external["external"] = true
	nodes = append(nodes, external)
	wantVLANs["EXP"] = append(wantVLANs["EXP"], "10.0.0.200")
	wantIPs["plc"] = map[string]string{"IF0": "10.0.0.200"}

	useFakes(t, &fakeMM{
		vms:       vms,
		responses: map[string]string{"ip addr": "inet " + strings.Join(staticIPs, "\ninet ")},
	})

	exp := newTestExperiment(t, nodes, map[string]any{"c2Timeout": "10s"})
	s := newSOH()

	if err := s.Running(checksContext(t, "network-config"), exp); err != nil {
		t.Fatalf("Running() = %v, want nil", err)
	}

	if got, want := sortedValues(s.vlans), sortedValues(wantVLANs); !reflect.DeepEqual(got, want) {
		t.Errorf("vlans = %v, want %v", got, want)
	}

	if !reflect.DeepEqual(s.hostIPs, wantIPs) {
		t.Errorf("hostIPs = %v, want %v", s.hostIPs, wantIPs)
	}

	for host, ips := range wantIPs {
		for _, addr := range ips {
			if got := s.addrHosts[addr]; got != host {
				t.Errorf("addrHosts[%s] = %q, want %q", addr, got, host)
			}
		}
	}

	for _, host := range slices.Sorted(maps.Keys(vms)) {
		for _, st := range s.status[host].Networking {
			if st.Error != "" {
				t.Errorf("%s networking error: %s", host, st.Error)
			}
		}
	}
}

// TestWaitForCPULoadRecordsEveryHost holds every CPU load goroutine at a
// barrier so they all record their results at once. Under -race this fails if
// any of them write the host status map concurrently.
func TestWaitForCPULoadRecordsEveryHost(t *testing.T) {
	const hosts = 50

	nodes := make([]map[string]any, 0, hosts)

	for i := range hosts {
		nodes = append(nodes, linuxNode(fmt.Sprintf("host-%d", i)))
	}

	// Windows hosts report their CPU load through a different command.
	nodes[0]["hardware"] = map[string]any{"os_type": osTypeWindows}

	useFakes(t, &fakeMM{
		responses: map[string]string{"cat /proc/loadavg": testLoadAvg, "powershell ": testWinCPULoad},
		barrier:   newBarrier(hosts),
	})

	exp := newTestExperiment(t, nodes, map[string]any{"c2Timeout": "10s"})
	s := newSOH()

	if err := s.Running(checksContext(t, "cpu-load"), exp); err != nil {
		t.Fatalf("Running() = %v, want nil", err)
	}

	if len(s.status) != hosts {
		t.Errorf("status has %d hosts, want %d", len(s.status), hosts)
	}

	for i := range hosts {
		host, want := fmt.Sprintf("host-%d", i), testCPULoad
		if i == 0 {
			want = testWinCPULoad
		}

		if got := s.status[host].CPULoad; got != want {
			t.Errorf("%s CPU load = %q, want %q", host, got, want)
		}
	}
}

const (
	goodHost = "good-host"
	noC2Host = "rtu-1"

	// What the web UI and REST API show for a check that C2 kept from running.
	c2NotActive = "c2 not active on host"
)

// noC2Experiment builds two hosts that are configured for every check group,
// some through another app's SoH profile. Only goodHost has C2.
func noC2Experiment(t *testing.T, md map[string]any) (*types.Experiment, *fakeMM) {
	t.Helper()

	nodes := []map[string]any{
		linuxNode(goodHost, staticIface("IF0", "10.0.0.1", "10.0.0.254")),
		linuxNode(noC2Host, staticIface("IF0", "10.0.0.2", "10.0.0.254")),
	}

	customTest := map[string]any{"name": "ready", "testScript": "echo ok", "testStdout": "ok"}

	soh := map[string]any{
		"c2Timeout":        "10s",
		"testReachability": "full",
		"hostFiles": map[string]any{
			goodHost: []string{"/etc/good.conf"},
			noC2Host: []string{"/etc/rtu.conf", "/opt/rtu/config.xml"},
		},
		"hostFilesAbsent":  map[string]any{noC2Host: []string{"/tmp/rtu.lock"}},
		"hostServices":     map[string]any{goodHost: []string{"sshd"}, noC2Host: []string{"rtu"}},
		"hostProcesses":    map[string]any{goodHost: []string{"sshd"}, noC2Host: []string{"rtud"}},
		"hostListeners":    map[string]any{goodHost: []string{":22"}, noC2Host: []string{":502"}},
		"dockerContainers": map[string]any{goodHost: []string{"web"}, noC2Host: []string{"historian"}},
		"hostCustomTests":  map[string]any{noC2Host: []any{customTest}},
	}

	maps.Copy(soh, md)

	profile := map[string]any{
		"processes":   []string{"rtu-agent"},
		"listeners":   []string{":20000"},
		"customTests": []any{map[string]any{"name": "profile", "testScript": "true", "testStdout": "x"}},
	}

	other := map[string]any{
		"name":  "rtu-app",
		"hosts": []any{map[string]any{"hostname": noC2Host, "metadata": map[string]any{"sohProfile": profile}}},
	}

	f := &fakeMM{
		noC2: map[string]bool{noC2Host: true},
		responses: map[string]string{
			"ip addr":             "inet 10.0.0.1/24\ninet 10.0.0.2/24",
			"ip route":            "default via 10.0.0.254 dev eth0",
			"ping -c 1 ":          "1 packets transmitted, 1 received",
			"stat -c present ":    "present",
			"systemctl is-active": "active",
			"pgrep -f ":           "1234",
			"ss -lntu ":           "Netid State\ntcp LISTEN",
			"docker inspect ":     "running",
			"cat /proc/loadavg":   testLoadAvg,
		},
	}

	useFakes(t, f)

	return newTestExperiment(t, nodes, soh, other), f
}

// reportedStatus decodes the host states soh stored in exp the way Get does
// for the web UI and REST API.
func reportedStatus(t *testing.T, exp *types.Experiment) map[string]HostState {
	t.Helper()

	data, _ := exp.Status.AppStatus()[appName].(map[string]any)

	var states []HostState

	if err := mapstructure.Decode(data["hosts"], &states); err != nil {
		t.Fatalf("decoding soh host states: %v", err)
	}

	hosts := make(map[string]HostState, len(states))

	for _, state := range states {
		hosts[state.Hostname] = state
	}

	return hosts
}

// wantFailures checks that states holds one failure with the error errText for
// each item, which the metadata key names.
func wantFailures(t *testing.T, group string, states []State, key, errText string, items ...string) {
	t.Helper()

	got := make([]string, 0, len(states))

	for _, st := range states {
		if !strings.Contains(st.Error, errText) {
			t.Errorf("%s %v: error = %q, want %q", group, st.Metadata, st.Error, errText)
		}

		item, _ := st.Metadata[key].(string)
		got = append(got, item)
	}

	slices.Sort(got)

	if want := slices.Sorted(slices.Values(items)); !slices.Equal(got, want) {
		t.Errorf("%s %s = %q, want %q", group, key, got, want)
	}
}

// wantNoC2Failures checks that every check configured for noC2Host after the
// network checks has one "c2 not active on host" failure.
func wantNoC2Failures(t *testing.T, rtu HostState) {
	t.Helper()

	wantFailures(t, "filesAbsent", rtu.FilesAbsent, "path", c2NotActive, "/tmp/rtu.lock")
	wantFailures(t, "services", rtu.Services, hostKey, c2NotActive, noC2Host)
	wantFailures(t, "services", rtu.Services, "service", c2NotActive, "rtu")
	wantFailures(t, "processes", rtu.Processes, "proc", c2NotActive, "rtud", "rtu-agent")
	wantFailures(t, "listeners", rtu.Listeners, "port", c2NotActive, ":502", ":20000")
	wantFailures(t, "containers", rtu.Containers, "container", c2NotActive, "historian")
	wantFailures(t, "customTests", rtu.CustomTests, "test", c2NotActive, "ready", "profile")

	if rtu.CPULoad != c2NotActive {
		t.Errorf("CPU load = %q, want %q", rtu.CPULoad, c2NotActive)
	}
}

// wantHealthy checks that goodHost ran its checks and passed them.
func wantHealthy(t *testing.T, good HostState) {
	t.Helper()

	for _, st := range good.AllStates() {
		// Reachability results also show on the target host.
		if st.Metadata[hostKey] != goodHost {
			continue
		}

		if st.Error != "" {
			t.Errorf("%s %v failed: %s", goodHost, st.Metadata, st.Error)
		}
	}

	if len(good.Files) != 1 || len(good.Services) != 1 || len(good.Processes) != 1 ||
		len(good.Listeners) != 1 || len(good.Containers) != 1 {
		t.Errorf("%s did not run every check: %+v", goodHost, good)
	}

	if good.CPULoad != testCPULoad {
		t.Errorf("%s CPU load = %q, want %q", goodHost, good.CPULoad, testCPULoad)
	}
}

// TestNoC2HostReportsEveryCheck covers a host whose C2 never starts. The
// network check finds that out, and every check after it must still show up in
// the host's status as failed, without another C2 command to the host.
func TestNoC2HostReportsEveryCheck(t *testing.T) {
	exp, f := noC2Experiment(t, nil)

	if err := newSOH().Running(checksContext(t), exp); err == nil {
		t.Fatal("Running() = nil, want an error")
	}

	hosts := reportedStatus(t, exp)
	rtu := hosts[noC2Host]

	wantFailures(t, "networking", rtu.Networking, hostKey, mm.ErrC2ClientNotActive.Error(), noC2Host)

	var toGood []State

	for _, st := range rtu.Reachability {
		if st.Metadata[hostKey] == noC2Host && st.Metadata[targetKey] == goodHost {
			toGood = append(toGood, st)
		}
	}

	wantFailures(t, "reachability", toGood, "ip", c2NotActive, "10.0.0.1")
	wantFailures(t, "files", rtu.Files, "path", c2NotActive, "/etc/rtu.conf", "/opt/rtu/config.xml")
	wantNoC2Failures(t, rtu)
	wantHealthy(t, hosts[goodHost])

	if got := f.sent(noC2Host); !slices.Equal(got, []string{"ip addr"}) {
		t.Errorf("C2 commands sent to %s = %q, want only the first network check", noC2Host, got)
	}
}

// TestNoC2HostReportsEveryCheckWithoutNetworkChecks covers the same host when
// the network checks are skipped, so the file checks are the first to find
// that C2 is not active.
func TestNoC2HostReportsEveryCheckWithoutNetworkChecks(t *testing.T) {
	exp, f := noC2Experiment(t, map[string]any{"skipInitialNetworkConfigTests": true})

	if err := newSOH().Running(checksContext(t), exp); err == nil {
		t.Fatal("Running() = nil, want an error")
	}

	hosts := reportedStatus(t, exp)
	rtu := hosts[noC2Host]

	if len(rtu.Networking) != 0 || len(rtu.Reachability) != 0 {
		t.Errorf("%s has network results with network checks skipped: %+v", noC2Host, rtu)
	}

	wantFailures(t, "files", rtu.Files, "path", mm.ErrC2ClientNotActive.Error(), "/etc/rtu.conf", "/opt/rtu/config.xml")
	wantNoC2Failures(t, rtu)
	wantHealthy(t, hosts[goodHost])

	// Both file checks start before either one finds that C2 is not active.
	got := f.sent(noC2Host)
	if len(got) != len(rtu.Files) {
		t.Errorf("C2 commands sent to %s = %q, want only its file checks", noC2Host, got)
	}

	for _, command := range got {
		if !strings.HasPrefix(command, "stat ") {
			t.Errorf("%s was sent %q after its file checks found no C2", noC2Host, command)
		}
	}
}

// TestSkippedHostReportsNothing checks that a host in skipHosts still gets no
// status entries and no C2 commands.
func TestSkippedHostReportsNothing(t *testing.T) {
	exp, f := noC2Experiment(t, map[string]any{"skipHosts": []string{noC2Host}})

	if err := newSOH().Running(checksContext(t), exp); err != nil {
		t.Fatalf("Running() = %v, want nil", err)
	}

	hosts := reportedStatus(t, exp)

	if rtu, ok := hosts[noC2Host]; ok {
		t.Errorf("skipped host %s has status %+v", noC2Host, rtu)
	}

	wantHealthy(t, hosts[goodHost])

	if got := f.sent(noC2Host); len(got) != 0 {
		t.Errorf("C2 commands sent to skipped host %s = %q", noC2Host, got)
	}
}
