package experiment

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

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
	flow := newNetflow("bridge", &net.UDPConn{}, nil)
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
	fail    error // returned by every mesh command
}

func (m *meshMM) Headnode() string { return "head" }

func (m *meshMM) MeshSend(exp, _, _ string) error {
	m.entered <- exp
	<-m.gate

	return m.fail
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

	go func() { started <- StartNetflow("a", 0) }()

	polltest.Within(t, "the start asking the mesh", func() { <-fake.entered })

	polltest.Within(t, "reading and stopping the other experiment's capture", func() {
		for _, exp := range []string{"a", "b"} {
			if flow := GetNetflow(exp); flow != nil {
				t.Errorf("GetNetflow(%s) of a capture not running = %v, want nil", exp, flow)
			}
		}

		stopHook("b")
	})

	if err := StartNetflow("a", 0); !errors.Is(err, ErrNetflowAlreadyStarted) {
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

	go func() { started <- StartNetflow("a", 0) }()

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

// genNetflowNumber draws a number either side of 0 and hi, or between.
func genNetflowNumber(hi int) *rapid.Generator[int] {
	return rapid.OneOf(rapid.IntRange(-2, hi+2), rapid.SampledFrom([]int{-1, 0, hi, hi + 1}))
}

// genNetflowEndpoint draws an IPv4 or IPv6 host and its port in each form a netflow line gives them.
func genNetflowEndpoint() *rapid.Generator[[3]string] {
	return rapid.Custom(func(t *rapid.T) [3]string {
		var host string

		if rapid.Bool().Draw(t, "v6") {
			host = netip.AddrFrom16([16]byte(rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(t, "ip"))).String()
		} else {
			host = netip.AddrFrom4([4]byte(rapid.SliceOfN(rapid.Byte(), 4, 4).Draw(t, "ip"))).String()
		}

		port := strconv.Itoa(genNetflowNumber(65535).Draw(t, "port"))
		addr := host + ":" + port

		if strings.Contains(host, ":") && rapid.Bool().Draw(t, "bracketed") {
			addr = "[" + host + "]:" + port
		}

		return [3]string{addr, host, port}
	})
}

// A flow line parses to its fields, however it is spaced, exactly when every
// number in it is in range.
func TestParseNetflowLineProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var (
			proto    = genNetflowNumber(255).Draw(t, "proto")
			src      = genNetflowEndpoint().Draw(t, "src")
			dst      = genNetflowEndpoint().Draw(t, "dst")
			packets  = genNetflowNumber(1<<40).Draw(t, "packets")
			bytes    = genNetflowNumber(1<<40).Draw(t, "bytes")
			space    = rapid.SampledFrom([]string{" ", "  ", "\t", " \t "})
			fields   = []string{"0", "1", strconv.Itoa(proto), src[0], "<->", dst[0], strconv.Itoa(packets), strconv.Itoa(bytes)}
			sport, _ = strconv.Atoi(src[2])
			dport, _ = strconv.Atoi(dst[2])
			line     strings.Builder
			inRange  = func(n, hi int) bool { return n >= 0 && n <= hi }
			wantOK   = inRange(proto, 255) && inRange(sport, 65535) && inRange(dport, 65535) && packets >= 0 && bytes >= 0
			want     map[string]any
		)

		line.WriteString(space.Draw(t, "lead"))

		for i, f := range fields {
			line.WriteString(f + space.Draw(t, "space"+strconv.Itoa(i)))
		}

		if wantOK {
			want = map[string]any{
				"proto": proto, "src": src[1], "sport": sport, "dst": dst[1], "dport": dport, "packets": packets, "bytes": bytes,
			}
		}

		got, ok := parseNetflowLine(line.String())
		if ok != wantOK {
			t.Fatalf("parseNetflowLine(%q) ok = %t, want %t", line.String(), ok, wantOK)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("parseNetflowLine(%q) mismatch (-want +got):\n%s", line.String(), diff)
		}
	})
}
