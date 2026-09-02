package experiment

import (
	"net"
	"testing"
	"time"
)

func TestParseNetflowLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		ok   bool
		src  string
		dst  string
	}{
		{
			name: "IPv4 flow",
			line: "0 0 6 10.0.0.1:1234 -> 10.0.0.2:443 2 2048",
			ok:   true,
			src:  "10.0.0.1",
			dst:  "10.0.0.2",
		},
		{
			name: "IPv6 flow",
			line: "0 0 17 [2001:db8::1]:53 -> [2001:db8::2]:5353 3 4096",
			ok:   true,
			src:  "2001:db8::1",
			dst:  "2001:db8::2",
		},
		{name: "truncated flow", line: "0 0 6 10.0.0.1:1234", ok: false},
		{name: "wrong delimiter", line: "0 0 6 10.0.0.1:1234 => 10.0.0.2:443 2 2048", ok: false},
		{name: "invalid endpoint", line: "0 0 6 broken -> 10.0.0.2:443 2 2048", ok: false},
		{name: "non-IP hostname endpoint", line: "0 0 6 example.com:80 -> 10.0.0.2:443 2 2048", ok: false},
		{name: "out of range port", line: "0 0 6 10.0.0.1:70000 -> 10.0.0.2:443 2 2048", ok: false},
		{name: "negative port", line: "0 0 6 10.0.0.1:-1 -> 10.0.0.2:443 2 2048", ok: false},
		{name: "invalid proto", line: "0 0 300 10.0.0.1:1234 -> 10.0.0.2:443 2 2048", ok: false},
		{name: "negative packet count", line: "0 0 6 10.0.0.1:1234 -> 10.0.0.2:443 -1 2048", ok: false},
		{name: "invalid count", line: "0 0 6 10.0.0.1:1234 -> 10.0.0.2:443 packets 2048", ok: false},
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
			if flow["src"] != test.src || flow["dst"] != test.dst {
				t.Fatalf("parseNetflowLine() endpoints = %v -> %v, want %s -> %s", flow["src"], flow["dst"], test.src, test.dst)
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
