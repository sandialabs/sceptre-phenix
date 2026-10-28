package util_test

import (
	"net"
	"strconv"
	"testing"

	"phenix/util"
)

// GetFreePort returns a port that is free again once it returns.
func TestGetFreePort(t *testing.T) {
	t.Parallel()

	port, err := util.GetFreePort("127.0.0.1")
	if err != nil {
		t.Fatalf("GetFreePort(127.0.0.1) error = %v, want nil", err)
	}

	if port <= 0 || port > 65535 {
		t.Fatalf("GetFreePort(127.0.0.1) = %d, want a port from 1 to 65535", port)
	}

	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("listening on the port GetFreePort returned: %v", err)
	}

	_ = l.Close()
}

func TestGetFreePortFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ip   string
	}{
		{name: "an address that does not parse", ip: "[::1"},
		{name: "an address of another host", ip: "192.0.2.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if port, err := util.GetFreePort(tt.ip); err == nil {
				t.Errorf("GetFreePort(%q) = %d, nil, want an error", tt.ip, port)
			}
		})
	}
}
