package v1

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
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

	t.Run("rejects hostnames differing only in case", func(t *testing.T) {
		topo := &TopologySpec{
			NodesF: []*Node{node("ADServer"), node("adserver")},
		}

		err := topo.Init("phenix")
		if err == nil {
			t.Fatalf("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), `duplicate node hostname "adserver"`) {
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

// Init reports each node whose hostname repeats an earlier one's, ignoring case, and only those.
func TestTopologyInitHostnamesProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		hostnames := rapid.SliceOfN(rapid.SampledFrom([]string{"ab", "Ab", "AB", "cd", "Cd"}), 1, 5).Draw(t, "hostnames")

		var (
			nodes = make([]*Node, len(hostnames))
			seen  = make(map[string]bool)
			dups  []string
		)

		for i, hostname := range hostnames {
			nodes[i] = &Node{GeneralF: &General{HostnameF: hostname}, HardwareF: &Hardware{}}

			if seen[strings.ToLower(hostname)] {
				dups = append(dups, hostname)
			}

			seen[strings.ToLower(hostname)] = true
		}

		err := (&TopologySpec{NodesF: nodes}).Init("phenix")
		if (err != nil) != (len(dups) > 0) {
			t.Fatalf("Init(hostnames %q) = %v, want duplicates %q", hostnames, err, dups)
		}

		if err == nil {
			return
		}

		if got := strings.Count(err.Error(), "duplicate node hostname"); got != len(dups) {
			t.Fatalf("Init(hostnames %q) reports %d duplicates, want %q:\n%v", hostnames, got, dups, err)
		}

		for _, dup := range dups {
			if !strings.Contains(err.Error(), `duplicate node hostname "`+dup+`"`) {
				t.Fatalf("Init(hostnames %q) = %v, does not name duplicate %q", hostnames, err, dup)
			}
		}
	})
}
