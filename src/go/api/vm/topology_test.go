package vm_test

import (
	"context"
	"errors"
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

	// node IDs change when VLANs are ignored, so a topology that ignores one leaves the index alone
	if fake.sets != 0 {
		t.Errorf("search index cache sets after Topology(%q, [MGMT]) = %d, want 0", testExp, fake.sets)
	}

	if _, err := vm.Topology(t.Context(), testExp, nil); err != nil {
		t.Errorf("Topology(%q, nil) error = %v", testExp, err)
	}

	// keyed by the experiment's revision, with the node IDs of the whole topology
	const key = "experiment|" + testExp + "|search|r7"

	search, _ := fake.vals[key].(vm.TopologySearch)
	if diff := cmp.Diff(map[string][]int{"EXP_1": {0, 3}, "MGMT": {0}}, search.VLAN); diff != "" {
		t.Errorf("cached search index VLAN mismatch (-want +got):\n%s", diff)
	}

	// VM details change when the experiment starts or stops or its VMs are
	// edited, so the index expires soon
	if ttl := fake.ttls[key]; ttl <= 0 || ttl > time.Minute {
		t.Errorf("search index cache expiry = %v, want more than 0 and at most %v", ttl, time.Minute)
	}

	if _, err := vm.Topology(t.Context(), testExp, []string{"MGMT"}); err != nil {
		t.Errorf("Topology(%q, [MGMT]) error = %v", testExp, err)
	}

	got, err := vm.TopologySearchIndex(testExp)
	if err != nil {
		t.Errorf("TopologySearchIndex(%q) error = %v", testExp, err)
	}

	if diff := cmp.Diff(search, got); diff != "" {
		t.Errorf("TopologySearchIndex(%q) after Topology(%q, [MGMT]) mismatch (-want +got):\n%s", testExp, testExp, diff)
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
// switch of that VLAN. In either call order, the search index finds each VM of the whole topology by name and VLAN.
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
		rc := &recordingCache{vals: map[string]any{}, ttls: map[string]time.Duration{}}
		cache.DefaultCache = rc //nolint:reassign // a fresh index

		var search vm.TopologySearch

		indexFirst := rapid.Bool().Draw(t, "indexFirst")
		if indexFirst {
			var err error

			search, err = vm.TopologySearchIndex(testExp)
			if err != nil {
				t.Fatalf("TopologySearchIndex(%q) error = %v", testExp, err)
			}
		}

		topo, err := vm.Topology(t.Context(), testExp, ignore)
		if err != nil {
			t.Fatalf("Topology(%q, %q) error = %v", testExp, ignore, err)
		}

		if !indexFirst {
			search, err = vm.TopologySearchIndex(testExp)
			if err != nil {
				t.Fatalf("TopologySearchIndex(%q) error = %v", testExp, err)
			}
		}

		full, err := vm.Topology(t.Context(), testExp, nil)
		if err != nil {
			t.Fatalf("Topology(%q, nil) error = %v", testExp, err)
		}

		if rc.sets != 1 {
			t.Errorf("search index cache sets = %d, want 1", rc.sets)
		}

		var (
			wantVMs   = make([]string, 0, len(vms))
			wantEdges []string // VM name > switch name
			gotVMs    []string
			gotEdges  = make([]string, 0, len(topo.Edges))
			switches  = map[string]int{}
			wantVLANs = map[string][]int{}
			// VLANs with an interface that is not ignored
			switchVLANs = map[string]bool{}
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

			id := slices.IndexFunc(full.Nodes, func(n mm.VM) bool { return n.Name == v.name })
			if got, ok := search.Hostname[v.name]; !ok || got != id {
				t.Errorf("TopologySearchIndex(%q).Hostname[%s] = %d, %t; want %d, true", testExp, v.name, got, ok, id)
			}

			for _, vlan := range v.vlans {
				wantVLANs[vlan] = append(wantVLANs[vlan], id)

				if !slices.Contains(ignore, vlan) {
					wantEdges = append(wantEdges, v.name+">"+vlan)
					switchVLANs[vlan] = true
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
			if n != 1 || !switchVLANs[vlan] {
				t.Errorf("Topology(%q, %q) has %d switches for VLAN %s with %d interfaces, want 1",
					testExp, ignore, n, vlan, len(wantVLANs[vlan]))
			}
		}

		if diff := cmp.Diff(wantVLANs, search.VLAN, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("TopologySearchIndex(%q).VLAN mismatch (-want +got):\n%s", testExp, diff)
		}
	})
}

// Topology and TopologySearchIndex fail for an experiment the store lacks, and Topology fails once its context is
// done, wrapping the context's error and caching no search index.
func TestTopologyErrors(t *testing.T) {
	useExperiment(t, startTime, vmNode("a", vmIface("IF0", "EXP_1", "10.0.0.1")))
	useMM(t, &fakeMM{vms: mm.VMs{runningVM("a", "EXP_1 (101)")}})

	fake := &recordingCache{vals: map[string]any{}, ttls: map[string]time.Duration{}}
	original := cache.DefaultCache
	cache.DefaultCache = fake //nolint:reassign // install test double

	t.Cleanup(func() { cache.DefaultCache = original }) //nolint:reassign // restore

	if _, err := vm.Topology(t.Context(), "missing", nil); err == nil {
		t.Error("Topology(missing, nil) error = nil, want an error")
	}

	if _, err := vm.TopologySearchIndex("missing"); err == nil {
		t.Error("TopologySearchIndex(missing) error = nil, want an error")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	topo, err := vm.Topology(ctx, testExp, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Topology(canceled, %q, nil) error = %v, want %v", testExp, err, context.Canceled)
	}

	if len(topo.Nodes) != 0 || len(topo.Edges) != 0 {
		t.Errorf("Topology(canceled, %q, nil) = %+v, want an empty topology", testExp, topo)
	}

	if fake.sets != 0 {
		t.Errorf("search index cache sets after failed calls = %d, want 0", fake.sets)
	}
}

// topologyIndexOf returns the search index TopologySearchIndex builds for the VM a with the given cache entries.
func topologyIndexOf(t *testing.T, cached map[string]any) vm.TopologySearch {
	t.Helper()

	stored := useExperiment(t, startTime, vmNode("a", vmIface("IF0", "EXP_1", "10.0.0.1")))
	stored.experiment.Metadata.Revision = 3

	running := runningVM("a", "EXP_1 (101)")
	running.Disk = "/phenix/images/a.qc2" // as minimega reports it

	useMM(t, &fakeMM{vms: mm.VMs{running}})

	original := cache.DefaultCache
	cache.DefaultCache = &recordingCache{vals: cached, ttls: map[string]time.Duration{}} //nolint:reassign // install test double

	t.Cleanup(func() { cache.DefaultCache = original }) //nolint:reassign // restore

	search, err := vm.TopologySearchIndex(testExp)
	if err != nil {
		t.Fatalf("TopologySearchIndex(%q) error = %v", testExp, err)
	}

	return search
}

// topologyWantIndex is the search index of the VM a that topologyIndexOf stores.
func topologyWantIndex() vm.TopologySearch {
	return vm.TopologySearch{
		Hostname: map[string]int{"a": 0},
		Disk:     map[string][]int{"/phenix/images/a.qc2": {0}},
		Type:     map[string][]int{"VirtualMachine": {0}},
		OSType:   map[string][]int{"linux": {0}},
		VLAN:     map[string][]int{"EXP_1": {0}},
		IP:       map[string][]int{"10.0.0.1": {0}},
	}
}

// A new search index finds a VM by its name, minimega's disk, type, OS type, VLANs and addresses.
func TestTopologySearchIndexFields(t *testing.T) {
	search := topologyIndexOf(t, map[string]any{})

	if diff := cmp.Diff(topologyWantIndex(), search); diff != "" {
		t.Errorf("TopologySearchIndex(%q) mismatch (-want +got):\n%s", testExp, diff)
	}
}

// A cached value that is not a search index is replaced by one built anew.
func TestTopologySearchIndexReplacesOtherCachedValue(t *testing.T) {
	key := "experiment|" + testExp + "|search|r3"
	cached := map[string]any{key: "not an index"}

	search := topologyIndexOf(t, cached)

	if diff := cmp.Diff(topologyWantIndex(), search); diff != "" {
		t.Errorf("TopologySearchIndex(%q) mismatch (-want +got):\n%s", testExp, diff)
	}

	if diff := cmp.Diff(any(topologyWantIndex()), cached[key]); diff != "" {
		t.Errorf("cached search index after TopologySearchIndex(%q) mismatch (-want +got):\n%s", testExp, diff)
	}
}

// The search index finds a VM by each of its topology labels and annotations.
func TestTopologySearchIndexFindsLabels(t *testing.T) {
	node := vmNode("a")
	node["labels"] = map[string]string{"role": "edge"}
	node["annotations"] = map[string]any{"vncBanner": "x"}

	useExperiment(t, startTime, node)
	useMM(t, &fakeMM{vms: mm.VMs{runningVM("a")}})

	original := cache.DefaultCache
	cache.DefaultCache = &recordingCache{vals: map[string]any{}, ttls: map[string]time.Duration{}} //nolint:reassign // install test double

	t.Cleanup(func() { cache.DefaultCache = original }) //nolint:reassign // restore

	search, err := vm.TopologySearchIndex(testExp)
	if err != nil {
		t.Fatalf("TopologySearchIndex(%q) error = %v", testExp, err)
	}

	if diff := cmp.Diff(map[string][]int{"vncBanner": {0}}, search.Annotation); diff != "" {
		t.Errorf("TopologySearchIndex(%q).Annotation mismatch (-want +got):\n%s", testExp, diff)
	}

	if diff := cmp.Diff(map[string][]int{"role=edge": {0}}, search.Label); diff != "" {
		t.Errorf("TopologySearchIndex(%q).Label mismatch (-want +got):\n%s", testExp, diff)
	}
}
