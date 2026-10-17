package soh

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
	"phenix/util/mm"
)

// dhcpMM answers GetVMIPv4 with each of its replies in turn, then the last.
type dhcpMM struct {
	mm.MM

	mu      sync.Mutex
	replies [][]string
	calls   int
}

func (m *dhcpMM) GetVMIPv4(...mm.Option) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	reply := m.replies[min(m.calls, len(m.replies)-1)]
	m.calls++

	return reply, nil
}

// useDHCP makes minimega answer with replies, as dhcpMM does, polled every millisecond until the test ends.
func useDHCP(t *testing.T, replies ...[]string) *dhcpMM {
	t.Helper()

	fake := &dhcpMM{replies: replies}
	originalMM, originalInterval := mm.DefaultMM, dhcpPollInterval

	mm.DefaultMM = fake //nolint:reassign // install test double
	dhcpPollInterval = time.Millisecond

	t.Cleanup(func() {
		mm.DefaultMM = originalMM //nolint:reassign // restore default
		dhcpPollInterval = originalInterval
	})

	return fake
}

func TestWaitForDHCPPollsOnceForAllInterfaces(t *testing.T) { //nolint:paralleltest // replaces package globals
	fake := useDHCP(t,
		nil,
		[]string{"", "10.0.0.2", "10.0.1.1"}, // idx 1 is static; no address for idx 3 yet
		[]string{"", "10.0.0.2", "10.0.1.1"}, // still none
		[]string{"", "10.0.0.2", "10.0.1.1", "10.0.2.1"},
	)

	s := newSOH()
	s.md.c2Timeout = time.Minute

	var (
		wg    = new(mm.StateGroup)
		ipsMu sync.Mutex
		eth2  = &v1.Interface{NameF: "eth2", VLANF: "A", ProtoF: "dhcp"}
		eth3  = &v1.Interface{NameF: "eth3", VLANF: "B", ProtoF: "dhcp"}
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		s.waitForDHCP(context.Background(), wg, &ipsMu, "exp", "host", []dhcpInterface{
			{idx: 2, iface: eth2}, {idx: 3, iface: eth3},
		})
	}()

	// The IP maps are safe to write while the wait fills them in.
	for range 100 {
		ipsMu.Lock()
		s.addrHosts["192.168.0.1"] = "static"
		ipsMu.Unlock()
	}

	wg.Wait()

	// one poll answers for both interfaces
	if fake.calls != 4 {
		t.Errorf("waitForDHCP(host, eth2, eth3) GetVMIPv4 calls = %d, want 4", fake.calls)
	}

	if diff := cmp.Diff([]bool{false, false}, stateFailures(wg)); diff != "" {
		t.Errorf("waitForDHCP(host, eth2, eth3) states failed mismatch (-want +got):\n%s", diff)
	}

	wantIPs := map[string]map[string]string{"host": {"eth2": "10.0.1.1", "eth3": "10.0.2.1"}}
	if diff := cmp.Diff(wantIPs, s.hostIPs); diff != "" {
		t.Errorf("waitForDHCP(host, eth2, eth3) hostIPs mismatch (-want +got):\n%s", diff)
	}

	wantHosts := map[string]string{"192.168.0.1": "static", "10.0.1.1": "host", "10.0.2.1": "host"}
	if diff := cmp.Diff(wantHosts, s.addrHosts); diff != "" {
		t.Errorf("waitForDHCP(host, eth2, eth3) addrHosts mismatch (-want +got):\n%s", diff)
	}

	wantVLANs := map[string][]string{"A": {"10.0.1.1"}, "B": {"10.0.2.1"}}
	if diff := cmp.Diff(wantVLANs, s.vlans); diff != "" {
		t.Errorf("waitForDHCP(host, eth2, eth3) vlans mismatch (-want +got):\n%s", diff)
	}
}

// stateFailures reports, for each state the group recorded, whether it is an error.
func stateFailures(wg *mm.StateGroup) []bool {
	failures := make([]bool, 0, len(wg.States))
	for _, state := range wg.States {
		failures = append(failures, state.Err != nil)
	}

	return failures
}

func TestWaitForDHCPTimesOutPerInterface(t *testing.T) { //nolint:paralleltest // replaces package globals
	useDHCP(t, []string{"10.0.0.1"})

	s := newSOH()
	s.md.c2Timeout = 20 * time.Millisecond

	var (
		wg    = new(mm.StateGroup)
		ipsMu sync.Mutex
	)

	s.waitForDHCP(context.Background(), wg, &ipsMu, "exp", "host", []dhcpInterface{
		{idx: 0, iface: &v1.Interface{NameF: "eth0", ProtoF: "dhcp"}},
		{idx: 1, iface: &v1.Interface{NameF: "eth1", ProtoF: "dhcp"}},
		{idx: 5, iface: &v1.Interface{NameF: "eth5", ProtoF: "dhcp"}},
	})

	// eth0's success, then a timeout for each of the others
	if diff := cmp.Diff([]bool{false, true, true}, stateFailures(wg)); diff != "" {
		t.Errorf("waitForDHCP(host, eth0, eth1, eth5) states failed mismatch (-want +got):\n%s", diff)
	}

	if wg.ErrCount != 2 {
		t.Errorf("waitForDHCP(host, eth0, eth1, eth5) ErrCount = %d, want 2", wg.ErrCount)
	}
}
