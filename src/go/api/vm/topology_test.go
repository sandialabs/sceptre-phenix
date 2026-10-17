package vm_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/vm"
	"phenix/util/cache"
	"phenix/util/mm"
)

// recordingCache is a cache that records how long each value is kept.
type recordingCache struct {
	vals map[string]any
	ttls map[string]time.Duration
	sets int
}

func (c *recordingCache) Get(key string) (any, bool) {
	v, ok := c.vals[key]

	return v, ok
}

func (c *recordingCache) Set(key string, val any) error {
	return c.SetWithExpire(key, val, -1)
}

func (c *recordingCache) SetWithExpire(key string, val any, exp time.Duration) error {
	c.sets++
	c.vals[key] = val
	c.ttls[key] = exp

	return nil
}

func TestTopology(t *testing.T) {
	stored := useExperiment(t, startTime,
		vmNode("a", vmIface("IF0", "EXP_1", "10.0.0.1"), vmIface("IF1", "MGMT", "172.16.0.1")),
		vmNode("b", vmIface("IF0", "EXP_1", "10.0.0.2")),
	)
	stored.experiment.Metadata.Revision = 7
	useMM(t, &fakeMM{vms: mm.VMs{
		runningVM("a", "EXP_1 (101)", "MGMT (100)"),
		runningVM("b", "EXP_1 (101)"),
	}})

	fake := &recordingCache{vals: map[string]any{}, ttls: map[string]time.Duration{}}
	original := cache.DefaultCache
	cache.DefaultCache = fake //nolint:reassign // install test double

	t.Cleanup(func() { cache.DefaultCache = original }) //nolint:reassign // restore

	topo, err := vm.Topology(t.Context(), testExp, []string{"MGMT"})
	if err != nil {
		t.Fatalf("Topology(%q, [MGMT]) error = %v", testExp, err)
	}

	// each VM and the switch of the VLAN they share, leaving out the ignored one
	nodes := make([]string, 0, len(topo.Nodes))
	for _, node := range topo.Nodes {
		nodes = append(nodes, node.Name)
	}

	if diff := cmp.Diff([]string{"a", "EXP_1", "b"}, nodes); diff != "" {
		t.Errorf("Topology(%q, [MGMT]) node names mismatch (-want +got):\n%s", testExp, diff)
	}

	if !topo.Running {
		t.Errorf("Topology(%q, [MGMT]).Running = false, want true", testExp)
	}

	// both VMs joined to the switch
	targets := make([]int, 0, len(topo.Edges))
	for _, edge := range topo.Edges {
		targets = append(targets, edge.Target)
	}

	if diff := cmp.Diff([]int{1, 1}, targets); diff != "" {
		t.Errorf("Topology(%q, [MGMT]) edge targets mismatch (-want +got):\n%s", testExp, diff)
	}

	// keyed by the experiment's revision
	const key = "experiment|" + testExp + "|search|r7"

	search, _ := fake.vals[key].(vm.TopologySearch)
	if diff := cmp.Diff(map[string][]int{"EXP_1": {0, 2}}, search.VLAN); diff != "" {
		t.Errorf("cached search index VLAN mismatch (-want +got):\n%s", diff)
	}

	// VM details change when the experiment starts or stops or its VMs are
	// edited, so the index expires soon
	if ttl := fake.ttls[key]; ttl <= 0 || ttl > time.Minute {
		t.Errorf("search index cache expiry = %v, want more than 0 and at most %v", ttl, time.Minute)
	}

	if _, err := vm.Topology(t.Context(), testExp, nil); err != nil {
		t.Errorf("Topology(%q, nil) error = %v", testExp, err)
	}

	if fake.sets != 1 {
		t.Errorf("search index cache sets after a second Topology() = %d, want 1", fake.sets)
	}

	search, err = vm.TopologySearchIndex(testExp)
	if err != nil {
		t.Errorf("TopologySearchIndex(%q) error = %v", testExp, err)
	}

	if a, ok := search.Hostname["a"]; !ok || a != 0 {
		t.Errorf("TopologySearchIndex(%q).Hostname[a] = %d, %t; want 0, true", testExp, a, ok)
	}

	if fake.sets != 1 {
		t.Errorf("search index cache sets after TopologySearchIndex() = %d, want 1", fake.sets)
	}

	// an edited experiment has a new revision, so its index is built anew
	stored.experiment.Metadata.Revision = 8

	if _, err := vm.TopologySearchIndex(testExp); err != nil {
		t.Errorf("TopologySearchIndex(%q) after an edit error = %v", testExp, err)
	}

	if fake.sets != 2 {
		t.Errorf("search index cache sets after an edit = %d, want 2", fake.sets)
	}
}

// topologyVM is a VM of a generated topology: its hostname and the VLAN of each interface.
type topologyVM struct {
	name  string
	vlans []string
}

// genTopologyVMs draws up to 5 VMs with distinct names, each with up to 3 interfaces on a few shared VLANs.
func genTopologyVMs() *rapid.Generator[[]topologyVM] {
	vlans := []string{"EXP_1", "EXP_2", "MGMT"}

	return rapid.SliceOfNDistinct(rapid.Custom(func(t *rapid.T) topologyVM {
		return topologyVM{
			name:  rapid.SampledFrom([]string{"a", "b", "c", "d", "e"}).Draw(t, "name"),
			vlans: rapid.SliceOfN(rapid.SampledFrom(vlans), 0, 3).Draw(t, "vlans"),
		}
	}), 0, 5, func(v topologyVM) string { return v.name })
}

// The topology holds each VM in order, joined by one edge per interface not on an ignored VLAN to the one
// switch of that VLAN, and the search index finds each VM by name and by VLAN.
func TestTopologyProperties(t *testing.T) {
	stored := useExperiment(t, startTime)
	fake := useMM(t, &fakeMM{})

	original := cache.DefaultCache
	t.Cleanup(func() { cache.DefaultCache = original }) //nolint:reassign // restore

	rapid.Check(t, func(t *rapid.T) {
		vms := genTopologyVMs().Draw(t, "vms")
		ignore := rapid.SliceOfDistinct(rapid.SampledFrom([]string{"EXP_2", "MGMT"}), rapid.ID).Draw(t, "ignore")

		nodes := make([]map[string]any, 0, len(vms))
		fake.vms = nil

		for _, v := range vms {
			ifaces := make([]map[string]any, 0, len(v.vlans))
			networks := make([]string, 0, len(v.vlans))

			for i, vlan := range v.vlans {
				ifaces = append(ifaces, vmIface("IF"+strconv.Itoa(i), vlan, ""))
				networks = append(networks, vlan+" (10"+strconv.Itoa(i)+")")
			}

			nodes = append(nodes, vmNode(v.name, ifaces...))
			fake.vms = append(fake.vms, runningVM(v.name, networks...))
		}

		stored.experiment.Spec["topology"] = map[string]any{"nodes": nodes}
		cache.DefaultCache = &recordingCache{vals: map[string]any{}, ttls: map[string]time.Duration{}} //nolint:reassign // a fresh index

		topo, err := vm.Topology(t.Context(), testExp, ignore)
		if err != nil {
			t.Fatalf("Topology(%q, %q) error = %v", testExp, ignore, err)
		}

		search, err := vm.TopologySearchIndex(testExp)
		if err != nil {
			t.Fatalf("TopologySearchIndex(%q) error = %v", testExp, err)
		}

		var (
			wantVMs   = make([]string, 0, len(vms))
			wantEdges []string // VM name > switch name
			gotVMs    []string
			gotEdges  = make([]string, 0, len(topo.Edges))
			switches  = map[string]int{}
			wantVLANs = map[string][]int{}
		)

		for i, node := range topo.Nodes {
			if node.ID != i {
				t.Fatalf("Topology(%q, %q) node %d ID = %d, want %d", testExp, ignore, i, node.ID, i)
			}

			if node.Type == "Switch" {
				switches[node.Name]++
			} else {
				gotVMs = append(gotVMs, node.Name)
			}
		}

		for _, v := range vms {
			wantVMs = append(wantVMs, v.name)

			id := slices.IndexFunc(topo.Nodes, func(n mm.VM) bool { return n.Name == v.name })
			if got, ok := search.Hostname[v.name]; !ok || got != id {
				t.Errorf("TopologySearchIndex(%q).Hostname[%s] = %d, %t; want %d, true", testExp, v.name, got, ok, id)
			}

			for _, vlan := range v.vlans {
				if !slices.Contains(ignore, vlan) {
					wantEdges = append(wantEdges, v.name+">"+vlan)
					wantVLANs[vlan] = append(wantVLANs[vlan], id)
				}
			}
		}

		for i, e := range topo.Edges {
			if e.ID != i {
				t.Fatalf("Topology(%q, %q) edge %d ID = %d, want %d", testExp, ignore, i, e.ID, i)
			}

			if target := topo.Nodes[e.Target]; target.Type != "Switch" || len(target.Networks) != 1 || target.Name != target.Networks[0] {
				t.Fatalf("Topology(%q, %q) edge %d targets %+v, want a VLAN's switch", testExp, ignore, i, target)
			}

			gotEdges = append(gotEdges, topo.Nodes[e.Source].Name+">"+topo.Nodes[e.Target].Name)
		}

		if diff := cmp.Diff(wantVMs, gotVMs, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("Topology(%q, %q) VM nodes mismatch (-want +got):\n%s", testExp, ignore, diff)
		}

		if diff := cmp.Diff(wantEdges, gotEdges, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("Topology(%q, %q) edges mismatch (-want +got):\n%s", testExp, ignore, diff)
		}

		for vlan, n := range switches {
			if n != 1 || len(wantVLANs[vlan]) == 0 {
				t.Errorf("Topology(%q, %q) has %d switches for VLAN %s with %d interfaces, want 1",
					testExp, ignore, n, vlan, len(wantVLANs[vlan]))
			}
		}

		if diff := cmp.Diff(wantVLANs, search.VLAN, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("TopologySearchIndex(%q).VLAN mismatch (-want +got):\n%s", testExp, diff)
		}
	})
}
