package v0

import "testing"

func TestNodeRouterNamePreservesHostname(t *testing.T) {
	const hostname = "Branch-Router-01"

	node := Node{
		TypeF:    "router",
		GeneralF: &General{HostnameF: hostname},
	}

	if got := node.RouterName(); got != hostname {
		t.Fatalf("RouterName() = %q, want %q", got, hostname)
	}
}
