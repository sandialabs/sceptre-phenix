package soh

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

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

func TestWaitForDHCPPollsOnceForAllInterfaces(t *testing.T) { //nolint:paralleltest // replaces package globals
	fake := &dhcpMM{replies: [][]string{
		nil,
		{"", "10.0.0.2", "10.0.1.1"}, // idx 1 is static; no address for idx 3 yet
		{"", "10.0.0.2", "10.0.1.1"}, // still none
		{"", "10.0.0.2", "10.0.1.1", "10.0.2.1"},
	}}

	originalMM, originalInterval := mm.DefaultMM, dhcpPollInterval

	mm.DefaultMM = fake //nolint:reassign // install test double
	dhcpPollInterval = time.Millisecond

	t.Cleanup(func() {
		mm.DefaultMM = originalMM //nolint:reassign // restore default
		dhcpPollInterval = originalInterval
	})

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

	if fake.calls != 4 {
		t.Fatalf("asked minimega %d times, want 4 (one per poll for both interfaces)", fake.calls)
	}

	if wg.ErrCount != 0 || len(wg.States) != 2 {
		t.Fatalf("states = %+v, want two successes", wg.States)
	}

	want := map[string]string{"eth2": "10.0.1.1", "eth3": "10.0.2.1"}
	if got := s.hostIPs["host"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("hostIPs = %v, want %v", got, want)
	}

	if s.addrHosts["10.0.1.1"] != "host" || s.addrHosts["10.0.2.1"] != "host" {
		t.Fatalf("addrHosts = %v", s.addrHosts)
	}

	if !slices.Equal(s.vlans["A"], []string{"10.0.1.1"}) || !slices.Equal(s.vlans["B"], []string{"10.0.2.1"}) {
		t.Fatalf("vlans = %v", s.vlans)
	}
}

func TestWaitForDHCPTimesOutPerInterface(t *testing.T) { //nolint:paralleltest // replaces package globals
	fake := &dhcpMM{replies: [][]string{{"10.0.0.1"}}}

	originalMM, originalInterval := mm.DefaultMM, dhcpPollInterval

	mm.DefaultMM = fake //nolint:reassign // install test double
	dhcpPollInterval = time.Millisecond

	t.Cleanup(func() {
		mm.DefaultMM = originalMM //nolint:reassign // restore default
		dhcpPollInterval = originalInterval
	})

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

	if wg.ErrCount != 2 || len(wg.States) != 3 {
		t.Fatalf("states = %+v, want eth0's success and a timeout for each of the others", wg.States)
	}
}
