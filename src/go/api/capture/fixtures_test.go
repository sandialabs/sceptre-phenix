package capture

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/store"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

const testExp = "exp1"

// namespaceHeader is the header of minimega 3.4.0's capture pcap namespace listing.
//
//nolint:gochecknoglobals // read-only test table
var namespaceHeader = []string{
	"id", "host", "bridge", "state", "ports", "packets", "bytes", "kdrops", "pdrops", "qdrops", "lost", "late", "lag",
	"skew", "size", "path", "vlans", "filter", "start", "error",
}

// mirrorHeader is the header of the bridge mirrors listing.
//
//nolint:gochecknoglobals // read-only test table
var mirrorHeader = []string{"bridge", "mirror", "output", "namespace", "kind"}

// fakeCluster is minimega for the tests: the real mm commands, sent to the fake minimega, with the VM info and head
// node that it holds. compute1 is the head node.
type fakeCluster struct {
	mm.Minimega

	vms      mm.VMs
	vmErr    error  // of reading the VM info
	onVMInfo func() // called at each read of the VM info, nil for nothing

	mu       sync.Mutex
	captures [][]string          // the listing's rows, without the header
	mirrors  map[string][]string // the mirrors of each host, as bridge/namespace pairs
	failing  string              // a command base that minimega fails
	startID  string              // the ID that a start gives
	started  map[string]string   // the summary row that a start adds to the listing, nil for none
	noFinal  bool                // a stop answers no final rows
}

func (*fakeCluster) Headnode() string { return "compute1" }

func (*fakeCluster) IsHeadnode(host string) bool { return host == "compute1" }

func (c *fakeCluster) GetVMInfo(...mm.Option) mm.VMs { return c.vms }

func (c *fakeCluster) GetVMInfoObserved(...mm.Option) (mm.VMs, freshness.Observation) {
	if c.onVMInfo != nil {
		c.onVMInfo()
	}

	if c.vmErr != nil {
		return c.vms, freshness.Observation{At: time.Now(), Stale: false, Known: false, Err: c.vmErr}
	}

	return c.vms, freshness.Observed(time.Now())
}

// capture adds a capture to the listing: its summary row with the given column values.
func (c *fakeCluster) capture(values map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.captures = append(c.captures, captureRow(values))
}

// captureRow is the listing's row with the given column values.
func captureRow(values map[string]string) []string {
	row := make([]string, len(namespaceHeader))
	for i, column := range namespaceHeader {
		row[i] = values[column]
	}

	return row
}

// mirrorsOn gives host count mirrors on bridge, of the namespace ns.
func (c *fakeCluster) mirrorsOn(host, bridge, ns string, count int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for range count {
		c.mirrors[host] = append(c.mirrors[host], bridge+"/"+ns)
	}
}

// mirrorRows are host's mirrors as bridge mirrors lists them.
func (c *fakeCluster) mirrorRows(host string) [][]string {
	rows := make([][]string, 0, len(c.mirrors[host]))

	for i, mirror := range c.mirrors[host] {
		bridge, ns, _ := strings.Cut(mirror, "/")

		kind := "capture"
		if ns == "" {
			kind = "external"
		}

		rows = append(rows, []string{bridge, fmt.Sprintf("m%d", i), fmt.Sprintf("tap%d", i), ns, kind})
	}

	return rows
}

func (c *fakeCluster) reply(cmd mmtest.Command) []*minicli.Response {
	c.mu.Lock()
	defer c.mu.Unlock()

	base := cmd.Base

	switch {
	case c.failing != "" && strings.HasPrefix(base, c.failing):
		return []*minicli.Response{mmtest.Error("compute1", "boom")}
	case base == "capture pcap namespace":
		return []*minicli.Response{mmtest.Tabular("compute1", namespaceHeader, c.captures...)}
	case strings.HasPrefix(base, "capture pcap delete namespace "):
		id := strings.TrimPrefix(base, "capture pcap delete namespace ")

		var final [][]string

		for _, row := range c.captures {
			if row[0] == id && !c.noFinal {
				row = slices.Clone(row)
				row[3] = "stopped"
				final = append(final, row)
			}
		}

		return []*minicli.Response{mmtest.Tabular("compute1", namespaceHeader, final...)}
	case strings.HasPrefix(base, "capture pcap namespace "):
		if c.started != nil {
			c.captures = append(c.captures, captureRow(c.started))
		}

		return []*minicli.Response{mmtest.Text("compute1", c.startID)}
	case base == "bridge mirrors":
		return []*minicli.Response{mmtest.Tabular("compute1", mirrorHeader, c.mirrorRows("compute1")...)}
	case strings.HasPrefix(base, "mesh send ") && strings.HasSuffix(base, " bridge mirrors"):
		hosts := strings.Split(strings.Fields(base)[2], ",")
		resps := make([]*minicli.Response, 0, len(hosts))

		for _, host := range hosts {
			resps = append(resps, mmtest.Tabular(host, mirrorHeader, c.mirrorRows(host)...))
		}

		return resps
	}

	return []*minicli.Response{mmtest.Error("compute1", "unexpected command "+base)}
}

// useCluster makes minimega the fake cluster, with experiment capture support, until the test ends, and returns the
// commands that minimega received since, without their namespace.
func useCluster(t *testing.T, c *fakeCluster) func() []string {
	t.Helper()

	if c.mirrors == nil {
		c.mirrors = make(map[string][]string)
	}

	if c.startID == "" {
		c.startID = "4"
	}

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = c //nolint:reassign // install the test double

	mm.SetCaptureFeaturesForced(true)
	t.Cleanup(func() { mm.SetCaptureFeaturesForced(false) })

	received := mmtest.Use(t, c.reply)

	return func() []string {
		cmds := received()
		bases := make([]string, 0, len(cmds))

		for _, cmd := range cmds {
			bases = append(bases, cmd.Base)
		}

		return bases
	}
}

// fakeStore holds one experiment. Other store.Store methods panic.
type fakeStore struct {
	store.Store

	experiment store.Config
}

func (s *fakeStore) Get(cfg *store.Config) error {
	if cfg.Kind != s.experiment.Kind || cfg.Metadata.Name != s.experiment.Metadata.Name {
		return fmt.Errorf("%s %s: %w", cfg.Kind, cfg.Metadata.Name, store.ErrNotExist)
	}

	*cfg = s.experiment

	return nil
}

// stoppingStore is a fakeStore whose experiment stops after its first read, as when it stops while a start waits.
type stoppingStore struct {
	*fakeStore

	mu    sync.Mutex
	reads int
}

func (s *stoppingStore) Get(cfg *store.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.fakeStore.Get(cfg)

	s.reads++
	if s.reads == 1 {
		status := make(map[string]any, len(s.experiment.Status))
		for k, v := range s.experiment.Status {
			if k != "startTime" {
				status[k] = v
			}
		}

		s.experiment.Status = status
	}

	return err
}

func (s *stoppingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reads
}

// vanishingStore is a fakeStore whose experiment is deleted after its first read, as when it is deleted while a start
// waits.
type vanishingStore struct {
	*fakeStore

	mu    sync.Mutex
	reads int
}

func (s *vanishingStore) Get(cfg *store.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.reads++
	if s.reads > 1 {
		return fmt.Errorf("%s %s: %w", cfg.Kind, cfg.Metadata.Name, store.ErrNotExist)
	}

	return s.fakeStore.Get(cfg)
}

// testNode is a topology node of the VM name with the interfaces ifaces.
func testNode(name string, ifaces ...map[string]any) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": name},
		"hardware": map[string]any{"os_type": "linux", "drives": []map[string]any{{"image": "a.qc2"}}},
		"network":  map[string]any{"interfaces": ifaces},
	}
}

// useExperiment makes the store hold exp1, running unless stopped is set: VM a on EXP_1 (bridge phenix) and EXP_2
// (bridge mega_bridge_exp1), VM b on EXP_2, with the VLANs EXP_1 and EXP_2.
func useExperiment(t *testing.T, stopped bool) {
	t.Helper()

	useExperimentNodes(t, stopped,
		testNode("a",
			map[string]any{"name": "IF0", "vlan": "EXP_1", "bridge": "phenix"},
			map[string]any{"name": "IF1", "vlan": "EXP_2"},
		),
		testNode("b", map[string]any{"name": "IF0", "vlan": "EXP_2"}),
	)
}

// useExperimentNodes makes the store hold exp1 with the topology nodes, running unless stopped is set, with the VLANs
// EXP_1 and EXP_2 and the default bridge mega_bridge_exp1.
func useExperimentNodes(t *testing.T, stopped bool, nodes ...map[string]any) {
	t.Helper()

	useExperimentSpec(t, stopped, map[string]any{
		"experimentName": testExp,
		"defaultBridge":  "mega_bridge_exp1",
		"topology":       map[string]any{"nodes": nodes},
	})
}

// useExperimentSpec makes the store hold exp1 with the spec, running unless stopped is set, with the VLANs EXP_1 and
// EXP_2.
func useExperimentSpec(t *testing.T, stopped bool, spec map[string]any) {
	t.Helper()

	exp := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: testExp},
		Spec:     spec,
		Status:   map[string]any{"vlans": map[string]any{"EXP_1": 101, "EXP_2": 102}},
	}

	if !stopped {
		exp.Status["startTime"] = "2026-11-05T14:22:33Z"
	}

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore the default

	store.DefaultStore = &fakeStore{experiment: exp} //nolint:reassign // install the test double
}

// vmsOn are VMs a on compute1 and b on compute2, as minimega runs them.
func vmsOn() mm.VMs {
	return mm.VMs{
		{Name: "a", Host: "compute1", Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}},
		{Name: "b", Host: "compute2", Networks: []string{"EXP_2 (102)"}},
	}
}

// errAny is the wanted error of a case that expects an error of any kind.
var errAny = errors.New("any error")

// errMatches reports whether err is want: nil for none, errAny for any error, else [errors.Is].
func errMatches(err, want error) bool {
	if want == nil || errors.Is(want, errAny) {
		return (err != nil) == (want != nil)
	}

	return errors.Is(err, want)
}
