package v1

import (
	"strings"
	"testing"
)

func TestTopologyInitHostnames(t *testing.T) {
	// minimal node that survives setDefaults (which dereferences HardwareF).
	node := func(hostname string) *Node {
		return &Node{
			GeneralF:  &General{HostnameF: hostname},
			HardwareF: &Hardware{},
		}
	}

	t.Run("rejects duplicate hostnames", func(t *testing.T) {
		topo := &TopologySpec{
			NodesF: []*Node{node("dup"), node("other"), node("dup")},
		}

		err := topo.Init("phenix")
		if err == nil {
			t.Fatalf("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), `duplicate node hostname "dup"`) {
			t.Fatalf("error %q does not name the duplicate hostname", err.Error())
		}
	})

	t.Run("allows unique hostnames", func(t *testing.T) {
		topo := &TopologySpec{
			NodesF: []*Node{node("a"), node("b"), node("c")},
		}

		if err := topo.Init("phenix"); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}
