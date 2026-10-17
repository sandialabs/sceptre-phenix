package experiment

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm"
	"phenix/util/polltest"
)

func TestParseNetflowLine(t *testing.T) {
	flow := func(proto int, src string, sport int, dst string, dport, packets, bytes int) map[string]any {
		return map[string]any{
			"proto": proto, "src": src, "sport": sport, "dst": dst, "dport": dport, "packets": packets, "bytes": bytes,
		}
	}

	tests := []struct {
		name string
		line string
		want map[string]any // nil when the line is refused
	}{
		{
			name: "IPv4 flow",
			line: "0 0 6 10.0.0.1:1234 <-> 10.0.0.2:443 2 2048",
			want: flow(6, "10.0.0.1", 1234, "10.0.0.2", 443, 2, 2048),
		},
		{
			name: "IPv6 flow",
			line: "0 0 17 [2001:db8::1]:53 <-> [2001:db8::2]:5353 3 4096",
			want: flow(17, "2001:db8::1", 53, "2001:db8::2", 5353, 3, 4096),
		},
		{
			name: "unbracketed IPv6 flow",
			line: "0 0 6 fe80::1:22 <-> fe80::2:443 2 2048",
			want: flow(6, "fe80::1", 22, "fe80::2", 443, 2, 2048),
		},
		{name: "truncated flow", line: "0 0 6 10.0.0.1:1234"},
		{name: "wrong delimiter", line: "0 0 6 10.0.0.1:1234 => 10.0.0.2:443 2 2048"},
		{name: "invalid endpoint", line: "0 0 6 broken <-> 10.0.0.2:443 2 2048"},
		{name: "non-IP hostname endpoint", line: "0 0 6 example.com:80 <-> 10.0.0.2:443 2 2048"},
		{name: "out of range port", line: "0 0 6 10.0.0.1:70000 <-> 10.0.0.2:443 2 2048"},
		{name: "negative port", line: "0 0 6 10.0.0.1:-1 <-> 10.0.0.2:443 2 2048"},
		{name: "invalid proto", line: "0 0 300 10.0.0.1:1234 <-> 10.0.0.2:443 2 2048"},
		{name: "negative packet count", line: "0 0 6 10.0.0.1:1234 <-> 10.0.0.2:443 -1 2048"},
		{name: "invalid count", line: "0 0 6 10.0.0.1:1234 <-> 10.0.0.2:443 packets 2048"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseNetflowLine(test.line)
			if want := test.want != nil; ok != want {
				t.Errorf("parseNetflowLine(%q) ok = %t, want %t", test.line, ok, want)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("parseNetflowLine(%q) mismatch (-want +got):\n%s", test.line, diff)
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
		c, err := store.NewConfig("experiment/" + name)
		if err != nil {
			t.Fatalf("store.NewConfig(experiment/%s) error = %v", name, err)
		}

		c.Spec = map[string]any{"experimentName": name, "defaultBridge": "br0"}
		c.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z", "schedules": map[string]any{"vm": "node1"}}

		if err := store.Create(c); err != nil {
			t.Fatalf("storing experiment %s: %v", name, err)
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

// stopHook runs the netflow stop hook for exp.
func stopHook(exp string) {
	for _, hook := range hooks["stop"] {
		hook("stop", exp)
	}
}

// While one capture waits on the mesh to start, other experiments' captures can be read and stopped,
// and a second start is refused.
//
//nolint:paralleltest // replaces the store, minimega and the netflow registry
func TestStartingOneNetflowDoesNotBlockOthers(t *testing.T) {
	fake := useNetflows(t, "a", "b")

	started := make(chan error, 1)

	go func() { started <- StartNetflow("a") }()

	polltest.Within(t, "the start asking the mesh", func() { <-fake.entered })

	polltest.Within(t, "reading and stopping the other experiment's capture", func() {
		for _, exp := range []string{"a", "b"} {
			if flow := GetNetflow(exp); flow != nil {
				t.Errorf("GetNetflow(%s) of a capture not running = %v, want nil", exp, flow)
			}
		}

		stopHook("b")
	})

	if err := StartNetflow("a"); !errors.Is(err, ErrNetflowAlreadyStarted) {
		t.Errorf("second StartNetflow(a) error = %v, want %v", err, ErrNetflowAlreadyStarted)
	}

	fake.release()

	var err error

	polltest.Within(t, "the start finishing", func() { err = <-started })

	if err != nil {
		t.Fatalf("StartNetflow(a) error = %v", err)
	}

	if GetNetflow("a") == nil {
		t.Error("GetNetflow(a) after StartNetflow(a) = nil, want the started capture")
	}
}

// An experiment stopping while its capture starts abandons the capture.
//
//nolint:paralleltest // replaces the store, minimega and the netflow registry
func TestStopHookDuringStartAbandonsIt(t *testing.T) {
	fake := useNetflows(t, "a")

	started := make(chan error, 1)

	go func() { started <- StartNetflow("a") }()

	polltest.Within(t, "the start asking the mesh", func() { <-fake.entered })
	polltest.Within(t, "the stop hook", func() { stopHook("a") })

	fake.release()

	var err error

	polltest.Within(t, "the start finishing", func() { err = <-started })

	if !errors.Is(err, ErrExperimentNotRunning) {
		t.Errorf("StartNetflow(a) stopped while starting error = %v, want %v", err, ErrExperimentNotRunning)
	}

	netflowMu.RLock()
	defer netflowMu.RUnlock()

	if len(netflows) != 0 {
		t.Errorf("netflow registry after an abandoned StartNetflow(a) = %v, want empty", netflows)
	}
}
