package experiment

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm"
)

func TestParseNetflowLine(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		ok    bool
		src   string
		sport int
		dst   string
		dport int
	}{
		{
			name:  "IPv4 flow",
			line:  "0 0 6 10.0.0.1:1234 <-> 10.0.0.2:443 2 2048",
			ok:    true,
			src:   "10.0.0.1",
			dst:   "10.0.0.2",
			sport: 1234,
			dport: 443,
		},
		{
			name:  "IPv6 flow",
			line:  "0 0 17 [2001:db8::1]:53 <-> [2001:db8::2]:5353 3 4096",
			ok:    true,
			src:   "2001:db8::1",
			dst:   "2001:db8::2",
			sport: 53,
			dport: 5353,
		},
		{
			name:  "unbracketed IPv6 flow",
			line:  "0 0 6 fe80::1:22 <-> fe80::2:443 2 2048",
			ok:    true,
			src:   "fe80::1",
			dst:   "fe80::2",
			sport: 22,
			dport: 443,
		},
		{name: "truncated flow", line: "0 0 6 10.0.0.1:1234", ok: false},
		{name: "wrong delimiter", line: "0 0 6 10.0.0.1:1234 => 10.0.0.2:443 2 2048", ok: false},
		{name: "invalid endpoint", line: "0 0 6 broken <-> 10.0.0.2:443 2 2048", ok: false},
		{name: "non-IP hostname endpoint", line: "0 0 6 example.com:80 <-> 10.0.0.2:443 2 2048", ok: false},
		{name: "out of range port", line: "0 0 6 10.0.0.1:70000 <-> 10.0.0.2:443 2 2048", ok: false},
		{name: "negative port", line: "0 0 6 10.0.0.1:-1 <-> 10.0.0.2:443 2 2048", ok: false},
		{name: "invalid proto", line: "0 0 300 10.0.0.1:1234 <-> 10.0.0.2:443 2 2048", ok: false},
		{name: "negative packet count", line: "0 0 6 10.0.0.1:1234 <-> 10.0.0.2:443 -1 2048", ok: false},
		{name: "invalid count", line: "0 0 6 10.0.0.1:1234 <-> 10.0.0.2:443 packets 2048", ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flow, ok := parseNetflowLine(test.line)
			if ok != test.ok {
				t.Fatalf("parseNetflowLine() ok = %v, want %v", ok, test.ok)
			}
			if !ok {
				return
			}
			if flow["src"] != test.src || flow["dst"] != test.dst || flow["sport"] != test.sport || flow["dport"] != test.dport {
				t.Fatalf("parseNetflowLine() endpoints = %v:%v -> %v:%v, want %s:%d -> %s:%d",
					flow["src"], flow["sport"], flow["dst"], flow["dport"], test.src, test.sport, test.dst, test.dport)
			}
		})
	}
}

func TestNetflowPublishDoesNotBlockOnSlowConsumer(t *testing.T) {
	flow := NewNetflow("bridge", &net.UDPConn{})
	consumer := flow.NewChannel("slow")
	for range netflowChannelBufferSize {
		consumer <- map[string]any{"bytes": 1}
	}

	done := make(chan struct{})
	go func() {
		flow.Publish(map[string]any{"bytes": 2})
		close(done)
	}()

	select {
	case <-done:
		// The newest message is deliberately dropped for this slow consumer.
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full consumer channel")
	}

	if len(consumer) != netflowChannelBufferSize {
		t.Fatalf("consumer queue length = %d, want %d", len(consumer), netflowChannelBufferSize)
	}
}

// meshMM is a minimega whose mesh commands wait on a gate, telling entered
// when one starts.
type meshMM struct {
	mm.MM

	entered chan string
	gate    chan struct{}
	release func()
}

func (m *meshMM) Headnode() string { return "head" }

func (m *meshMM) MeshSend(exp, _, _ string) error {
	m.entered <- exp
	<-m.gate

	return nil
}

// useNetflows installs a store in which the named experiments are running, a
// gated minimega, and an empty netflow registry.
func useNetflows(t *testing.T, running ...string) *meshMM {
	t.Helper()

	storetest.Use(t)

	for _, name := range running {
		c, _ := store.NewConfig("experiment/" + name)
		c.Spec = map[string]any{"experimentName": name, "defaultBridge": "br0"}
		c.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z", "schedules": map[string]any{"vm": "node1"}}

		if err := store.Create(c); err != nil {
			t.Fatal(err)
		}
	}

	fake := &meshMM{entered: make(chan string, 16), gate: make(chan struct{})}
	fake.release = sync.OnceFunc(func() { close(fake.gate) })

	originalMM := mm.DefaultMM
	mm.DefaultMM = fake //nolint:reassign // install test double

	netflowMu.Lock()
	saved := netflows
	netflows = make(map[string]*netflowEntry)
	netflowMu.Unlock()

	t.Cleanup(func() {
		fake.release()

		netflowMu.Lock()
		for _, entry := range netflows {
			if entry.flow != nil {
				entry.flow.Close()
			}
		}

		netflows = saved
		netflowMu.Unlock()

		mm.DefaultMM = originalMM //nolint:reassign // restore test double
	})

	return fake
}

// within fails the test if fn does not return within 2 seconds.
func within(t *testing.T, what string, fn func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		fn()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s took longer than 2s", what)
	}
}

// stopHook runs the netflow stop hook for exp.
func stopHook(exp string) {
	for _, hook := range hooks["stop"] {
		hook("stop", exp)
	}
}

// While one experiment's capture waits on the mesh to start, other
// experiments' captures can be read and stopped, and a second start of the
// same capture is refused.
//
//nolint:paralleltest // replaces the store, minimega and the netflow registry
func TestStartingOneNetflowDoesNotBlockOthers(t *testing.T) {
	fake := useNetflows(t, "a", "b")

	started := make(chan error, 1)

	go func() { started <- StartNetflow("a") }()

	within(t, "the start asking the mesh", func() { <-fake.entered })

	within(t, "reading and stopping the other experiment's capture", func() {
		if GetNetflow("b") != nil || GetNetflow("a") != nil {
			t.Error("a capture that is not running was returned")
		}

		stopHook("b")
	})

	if err := StartNetflow("a"); !errors.Is(err, ErrNetflowAlreadyStarted) {
		t.Errorf("second start returned %v, want %v", err, ErrNetflowAlreadyStarted)
	}

	fake.release()

	var err error

	within(t, "the start finishing", func() { err = <-started })

	if err != nil {
		t.Fatalf("starting: %v", err)
	}

	if GetNetflow("a") == nil {
		t.Error("the started capture is not returned")
	}
}

// An experiment stopping while its capture starts abandons the capture.
//
//nolint:paralleltest // replaces the store, minimega and the netflow registry
func TestStopHookDuringStartAbandonsIt(t *testing.T) {
	fake := useNetflows(t, "a")

	started := make(chan error, 1)

	go func() { started <- StartNetflow("a") }()

	within(t, "the start asking the mesh", func() { <-fake.entered })
	within(t, "the stop hook", func() { stopHook("a") })

	fake.release()

	var err error

	within(t, "the start finishing", func() { err = <-started })

	if !errors.Is(err, ErrExperimentNotRunning) {
		t.Errorf("start returned %v, want %v", err, ErrExperimentNotRunning)
	}

	netflowMu.RLock()
	defer netflowMu.RUnlock()

	if len(netflows) != 0 {
		t.Errorf("netflows = %v, want none", netflows)
	}
}
