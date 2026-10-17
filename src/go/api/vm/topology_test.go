package vm_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

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
