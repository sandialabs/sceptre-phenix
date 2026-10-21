package soh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"

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

	ids      atomic.Int64
	commands sync.Map // command ID -> command
}

func (f *fakeMM) GetVMInfo(opts ...mm.Option) mm.VMs {
	vm, ok := f.vms[optionField(mm.NewOptions(opts...), "vm")]
	if !ok {
		return mm.VMs{}
	}

	return mm.VMs{vm}
}

func (f *fakeMM) ExecC2Command(opts ...mm.C2Option) (string, error) {
	command := optionField(mm.NewC2Options(opts...), "command")

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

func (f *fakeMM) WaitForC2Response(opts ...mm.C2Option) (string, error) {
	if f.barrier != nil {
		if err := f.barrier.wait(); err != nil {
			return "", err
		}
	}

	return f.GetC2Response(opts...)
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

func staticIface(name, vlan, addr, gateway string) map[string]any {
	return map[string]any{
		"name":    name,
		"type":    "ethernet",
		"vlan":    vlan,
		"proto":   "static",
		"address": addr,
		"mask":    24,
		"gateway": gateway,
	}
}

// checksContext returns a context that runs only the given checks and drops
// soh's logs.
func checksContext(t *testing.T, checks ...string) context.Context {
	t.Helper()

	ctx := plog.ContextWithLogger(t.Context(), slog.New(slog.DiscardHandler), plog.TypeSoh)

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
		staticIface("IF1", "EXP", "10.0.0.100", ""),
	))
	vms["mixed"] = mm.VM{Name: "mixed", IPv4: []string{"10.1.0.100", "10.0.0.100"}}
	staticIPs = append(staticIPs, "10.0.0.100/24")
	wantVLANs["EXP"] = append(wantVLANs["EXP"], "10.1.0.100", "10.0.0.100")
	wantIPs["mixed"] = map[string]string{"IF0": "10.1.0.100", "IF1": "10.0.0.100"}

	for i := range staticHosts {
		host := fmt.Sprintf("static-%d", i)
		addr := fmt.Sprintf("10.0.0.%d", i+1)

		nodes = append(nodes, linuxNode(host, staticIface("IF0", "EXP", addr, "")))
		staticIPs = append(staticIPs, addr+"/24")
		wantVLANs["EXP"] = append(wantVLANs["EXP"], addr)
		wantIPs[host] = map[string]string{"IF0": addr}
	}

	external := linuxNode("plc", staticIface("IF0", "EXP", "10.0.0.200", ""))
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
