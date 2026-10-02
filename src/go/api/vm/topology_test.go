package vm_test

import (
	"reflect"
	"testing"
	"time"

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
	useExperiment(t, startTime,
		vmNode("a", vmIface("IF0", "EXP_1", "10.0.0.1"), vmIface("IF1", "MGMT", "172.16.0.1")),
		vmNode("b", vmIface("IF0", "EXP_1", "10.0.0.2")),
	)
	useMM(t, &fakeMM{vms: mm.VMs{
		runningVM("a", "EXP_1 (101)", "MGMT (100)"),
		runningVM("b", "EXP_1 (101)"),
	}})

	fake := &recordingCache{vals: map[string]any{}, ttls: map[string]time.Duration{}}
	original := cache.DefaultCache
	cache.DefaultCache = fake //nolint:reassign // install test double

	t.Cleanup(func() { cache.DefaultCache = original }) //nolint:reassign // restore

	topo, err := vm.Topology(testExp, []string{"MGMT"})
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}

	// each VM and the switch of the VLAN they share, leaving out the ignored one
	nodes := make([]string, 0, len(topo.Nodes))
	for _, node := range topo.Nodes {
		nodes = append(nodes, node.Name)
	}

	if want := []string{"a", "EXP_1", "b"}; !reflect.DeepEqual(nodes, want) || !topo.Running {
		t.Errorf("Topology has nodes %q (running %t), want %q (running)", nodes, topo.Running, want)
	}

	if len(topo.Edges) != 2 || topo.Edges[0].Target != 1 || topo.Edges[1].Target != 1 {
		t.Errorf("Topology has edges %+v, want both VMs joined to the switch", topo.Edges)
	}

	const key = "experiment|" + testExp + "|search"

	search, _ := fake.vals[key].(vm.TopologySearch)
	if want := []int{0, 2}; !reflect.DeepEqual(search.VLAN["EXP_1"], want) || search.VLAN["MGMT"] != nil {
		t.Errorf("search index has VLANs %v, want EXP_1 on %v only", search.VLAN, want)
	}

	// VM details change when the experiment starts or stops or its VMs are
	// edited, so the index expires soon
	if ttl := fake.ttls[key]; ttl <= 0 || ttl > time.Minute {
		t.Errorf("search index cached for %v, want a short expiry", ttl)
	}

	if _, err := vm.Topology(testExp, nil); err != nil || fake.sets != 1 {
		t.Errorf("second Topology: %v after caching the index %d times, want it reused", err, fake.sets)
	}
}
