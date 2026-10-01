package workflow_test

import (
	"errors"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
)

// prepareCase is one row of a [workflow.Prepare] table. pending is passed to
// Prepare as is. setup declares the only store calls Prepare may make; nil
// means none. A nil wantErr means Prepare must succeed with want; otherwise
// its error must wrap wantErr and contain wantMsg.
type prepareCase struct {
	name    string
	spec    workflow.Spec
	action  workflow.Action
	exp     *types.Experiment
	pending []string
	setup   func(m *store.MockStore)
	want    preparedSummary
	wantErr error
	wantMsg string
}

// preparedSummary is the comparable part of a [workflow.Prepared]. The node
// hostnames of its topology and the asset directory of each scenario app
// stand in for the decoded specs.
type preparedSummary struct {
	TopologyName string
	Hostnames    []string
	ScenarioName string
	AppAssetDirs map[string]string
	Aliases      map[string]int
	Schedules    map[string]string
}

// installPrepareStore installs a store mock as the default store until the
// test ends. The mock allows only the calls a test expects, so any other read
// and every write fails the test.
func installPrepareStore(t *testing.T) *store.MockStore {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore the default store

	m := store.NewMockStore(ctrl)
	store.DefaultStore = m //nolint:reassign // install the test double

	return m
}

// prepareKey returns the config that [store.NewConfig] builds for name, such
// as "topology/foo", which is what Prepare passes to the store.
func prepareKey(name string) *store.Config {
	key, _ := store.NewConfig(name)

	return key
}

// expectPrepareRead expects exactly one store read of the config named name
// and answers it with cfg.
func expectPrepareRead(m *store.MockStore, name string, cfg store.Config) {
	m.EXPECT().Get(gomock.Eq(prepareKey(name))).SetArg(0, cfg).Return(nil)
}

// expectPrepareReadFails expects exactly one store read of the config named
// name and fails it with [store.ErrNotExist].
func expectPrepareReadFails(m *store.MockStore, name string) {
	m.EXPECT().Get(gomock.Eq(prepareKey(name))).Return(store.ErrNotExist)
}

// prepareTopology returns the stored v1 topology foo. The linux VMs host-a
// and host-b have a hardware section, as every topology that passed its
// schema has. host-a has interfaces on VLANs EXP and MGMT, host-b has no
// network, and the external node ext-c has an interface on VLAN EXT.
func prepareTopology() store.Config {
	return prepareTopologyWithNodes(
		"foo",
		map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": "host-a"},
			"hardware": map[string]any{"os_type": "linux"},
			"network": map[string]any{
				"interfaces": []any{
					map[string]any{"name": "IF0", "vlan": "EXP"},
					map[string]any{"name": "IF1", "vlan": "MGMT"},
				},
			},
		},
		vmNode("host-b"),
		map[string]any{
			"external": true,
			"general":  map[string]any{"hostname": "ext-c"},
			"network": map[string]any{
				"interfaces": []any{map[string]any{"name": "IF0", "vlan": "EXT"}},
			},
		},
	)
}

// prepareTopologyWithNodes returns a stored v1 topology named name whose
// nodes are the given node maps, as in a config parsed from JSON or YAML.
func prepareTopologyWithNodes(name string, nodes ...map[string]any) store.Config {
	list := make([]any, 0, len(nodes))
	for _, node := range nodes {
		list = append(list, node)
	}

	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     map[string]any{"nodes": list},
	}
}

// vmNode returns a linux VirtualMachine node named hostname, with a hardware
// section and no network.
func vmNode(hostname string) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": hostname},
		"hardware": map[string]any{"os_type": "linux"},
	}
}

// prepareScenario returns a stored v2 scenario named name with the given
// apps. A non-empty topology becomes its topology annotation. The apps are a
// []any, as in a config parsed from JSON or YAML.
func prepareScenario(name, topology string, apps ...map[string]any) store.Config {
	list := make([]any, 0, len(apps))
	for _, app := range apps {
		list = append(list, app)
	}

	cfg := store.Config{
		Version:  "phenix.sandia.gov/v2",
		Kind:     "Scenario",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     map[string]any{"apps": list},
	}

	if topology != "" {
		cfg.Metadata.Annotations = store.Annotations{"topology": topology}
	}

	return cfg
}

// prepareExperiment returns the experiment exp1 with the given annotations.
func prepareExperiment(annotations store.Annotations) *types.Experiment {
	return &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp1", Annotations: annotations},
	}
}

// summarizePrepared returns the comparable part of p.
func summarizePrepared(p workflow.Prepared) preparedSummary {
	s := preparedSummary{
		TopologyName: p.TopologyName,
		ScenarioName: p.ScenarioName,
		Aliases:      p.Aliases,
		Schedules:    p.Schedules,
	}

	if p.Topology != nil {
		nodes := p.Topology.Nodes()
		s.Hostnames = make([]string, 0, len(nodes))

		for _, node := range nodes {
			s.Hostnames = append(s.Hostnames, node.General().Hostname())
		}
	}

	if p.Scenario != nil {
		s.AppAssetDirs = make(map[string]string)

		for _, app := range p.Scenario.Apps() {
			s.AppAssetDirs[app.Name()] = app.AssetDir()
		}
	}

	return s
}

// runPrepareCases runs each case through [workflow.Prepare] with a strict
// store mock.
func runPrepareCases(t *testing.T, cases []prepareCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := installPrepareStore(t)
			if tc.setup != nil {
				tc.setup(m)
			}

			plan := workflow.Plan{Action: tc.action, Experiment: "exp1", Reason: ""}

			got, err := workflow.Prepare(tc.spec, plan, tc.exp, tc.pending...)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Prepare() error = %v, want it to wrap %v", err, tc.wantErr)
				}

				if !strings.Contains(err.Error(), tc.wantMsg) {
					t.Errorf("Prepare() error = %q, want it to contain %q", err, tc.wantMsg)
				}

				if !reflect.DeepEqual(got, workflow.Prepared{}) {
					t.Errorf("Prepare() = %+v on error, want the zero Prepared", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("Prepare() unexpected error: %v", err)
			}

			if s := summarizePrepared(got); !reflect.DeepEqual(s, tc.want) {
				t.Errorf("Prepare() = %+v, want %+v", s, tc.want)
			}
		})
	}
}

// TestPrepareNone checks that a plan that does nothing needs nothing: the
// store mock expects no calls.
func TestPrepareNone(t *testing.T) {
	installPrepareStore(t)

	var (
		spec = workflow.Spec{Topology: "foo", Scenario: "bar"}
		plan = workflow.Plan{Action: workflow.ActionNone, Experiment: "exp1", Reason: "auto.update is false"}
	)

	for _, exp := range []*types.Experiment{nil, prepareExperiment(store.Annotations{"topology": "foo"})} {
		got, err := workflow.Prepare(spec, plan, exp)
		if err != nil {
			t.Fatalf("Prepare() unexpected error: %v", err)
		}

		if !reflect.DeepEqual(got, workflow.Prepared{}) {
			t.Errorf("Prepare() = %+v, want the zero Prepared", got)
		}
	}
}

// TestPrepareRejectsUnknownAction checks that a plan with an action Prepare
// does not know is an invalid config and reads nothing.
func TestPrepareRejectsUnknownAction(t *testing.T) {
	installPrepareStore(t)

	_, err := workflow.Prepare(workflow.Spec{Topology: "foo"}, workflow.Plan{Action: "rebuild"}, nil)
	if !errors.Is(err, workflow.ErrInvalidSpec) || !strings.Contains(err.Error(), `"rebuild"`) {
		t.Fatalf("Prepare() error = %v, want an %v naming the action", err, workflow.ErrInvalidSpec)
	}
}

// TestPrepareCreate checks create and createAndStart plans, which resolve the
// workflow's topology and scenario the way creating an experiment does.
func TestPrepareCreate(t *testing.T) {
	var (
		hostnames = []string{"host-a", "host-b", "ext-c"}
		app1      = map[string]any{"name": "app1"}
		fromBase  = map[string]any{"name": "app1", "fromScenario": "base"}
	)

	create := func(topology, scenario string) workflow.Spec {
		return workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: topology, Scenario: scenario}
	}

	readTopology := func(m *store.MockStore) { expectPrepareRead(m, "topology/foo", prepareTopology()) }

	undecodableTopology := prepareTopology()
	undecodableTopology.Version = "phenix.sandia.gov/v7"

	undecodableScenario := prepareScenario("bar", "foo", app1)
	undecodableScenario.Version = "phenix.sandia.gov/v7"

	runPrepareCases(t, []prepareCase{
		{
			name: "topology only",
			spec: workflow.Spec{
				Auto:      &workflow.Auto{Create: "exp1"},
				Topology:  "foo",
				VLANs:     map[string]int{"EXP": 101},
				Schedules: map[string]string{"host-a": "c1"},
			},
			action: workflow.ActionCreateAndStart,
			setup:  readTopology,
			// A create passes the workflow's own mappings to the new experiment.
			want: preparedSummary{TopologyName: "foo", Hostnames: hostnames},
		},
		{
			name:   "topology and scenario",
			spec:   create("foo", "bar"),
			action: workflow.ActionCreate,
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "foo", app1))
			},
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				ScenarioName: "bar",
				AppAssetDirs: map[string]string{"app1": ""},
			},
		},
		{
			name:   "scenario annotated for several topologies",
			spec:   create("foo", "bar"),
			action: workflow.ActionCreate,
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "foo,other", app1))
			},
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				ScenarioName: "bar",
				AppAssetDirs: map[string]string{"app1": ""},
			},
		},
		{
			name:    "no topology",
			spec:    create("", "bar"),
			action:  workflow.ActionCreate,
			wantErr: workflow.ErrInvalidSpec,
			wantMsg: "auto.create is set but topology is not",
		},
		{
			name:    "topology is not a config name",
			spec:    create("a/b", ""),
			action:  workflow.ActionCreateAndStart,
			wantErr: workflow.ErrUnresolved,
			wantMsg: `topology "a/b": invalid config name provided`,
		},
		{
			name:    "topology does not exist",
			spec:    create("foo", ""),
			action:  workflow.ActionCreate,
			setup:   func(m *store.MockStore) { expectPrepareReadFails(m, "topology/foo") },
			wantErr: workflow.ErrUnresolved,
			wantMsg: `topology "foo": config does not exist`,
		},
		{
			name:    "topology does not decode",
			spec:    create("foo", ""),
			action:  workflow.ActionCreate,
			setup:   func(m *store.MockStore) { expectPrepareRead(m, "topology/foo", undecodableTopology) },
			wantErr: workflow.ErrUnresolved,
			wantMsg: `decoding topology "foo": upgrading topology to v1: unknown version v7`,
		},
		{
			name:   "scenario does not exist",
			spec:   create("foo", "bar"),
			action: workflow.ActionCreate,
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareReadFails(m, "scenario/bar")
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `scenario "bar": config does not exist`,
		},
		{
			name:   "scenario has no topology annotation",
			spec:   create("foo", "bar"),
			action: workflow.ActionCreate,
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "", app1))
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `scenario "bar" has no topology annotation`,
		},
		{
			name:   "scenario is for another topology",
			spec:   create("foo", "bar"),
			action: workflow.ActionCreate,
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "other", app1))
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `scenario "bar" is annotated for topology "other", not "foo"`,
		},
		{
			name:   "scenario does not decode",
			spec:   create("foo", "bar"),
			action: workflow.ActionCreate,
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareRead(m, "scenario/bar", undecodableScenario)
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `decoding scenario "bar": upgrading scenario to v2: unknown version v7`,
		},
		{
			name:   "fromScenario app does not merge",
			spec:   create("foo", "bar"),
			action: workflow.ActionCreateAndStart,
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "foo", fromBase))
				expectPrepareReadFails(m, "scenario/base")
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `merging scenario "bar": scenario base doesn't exist`,
		},
	})
}

// TestPrepareUpdate checks update, updateAndStart and restart plans, which
// take the topology and scenario from the workflow, else from the mapped
// experiment's annotations, and build the VLAN aliases and schedules to set.
func TestPrepareUpdate(t *testing.T) {
	var (
		hostnames      = []string{"host-a", "host-b", "ext-c"}
		defaultAliases = map[string]int{"EXP": 0, "MGMT": 0, "EXT": 0}
		fromBase       = map[string]any{"name": "app1", "fromScenario": "base"}
		baseApp        = map[string]any{"name": "app1", "assetDir": "/phenix/base"}
	)

	annotated := func() *types.Experiment {
		return prepareExperiment(store.Annotations{"topology": "foo", "scenario": "bar"})
	}

	readTopology := func(m *store.MockStore) { expectPrepareRead(m, "topology/foo", prepareTopology()) }

	withInclude := prepareTopology()
	withInclude.Spec["includeTopologies"] = []any{"missing-include"}

	runPrepareCases(t, []prepareCase{
		{
			name: "workflow names win over annotations",
			spec: workflow.Spec{
				Topology:  "foo",
				Scenario:  "bar",
				VLANs:     map[string]int{"EXP": 101, "UNUSED": 7},
				Schedules: map[string]string{"host-a": "c1", "ext-c": "c2", "ghost": "c3"},
			},
			action: workflow.ActionRestart,
			exp:    prepareExperiment(store.Annotations{"topology": "old", "scenario": "old"}),
			setup: func(m *store.MockStore) {
				readTopology(m)
				// An update does not check the scenario's own topology annotation.
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "other", fromBase))
				expectPrepareRead(m, "scenario/base", prepareScenario("base", "foo", baseApp))
			},
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				ScenarioName: "bar",
				AppAssetDirs: map[string]string{"app1": "/phenix/base"},
				Aliases:      map[string]int{"EXP": 101, "MGMT": 0, "EXT": 0},
				Schedules:    map[string]string{"host-a": "c1"},
			},
		},
		{
			name:   "annotations fill in",
			action: workflow.ActionUpdate,
			exp:    annotated(),
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "", map[string]any{"name": "app1"}))
			},
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				ScenarioName: "bar",
				AppAssetDirs: map[string]string{"app1": ""},
				Aliases:      defaultAliases,
				Schedules:    map[string]string{},
			},
		},
		{
			name:   "no scenario",
			spec:   workflow.Spec{Topology: "foo"},
			action: workflow.ActionUpdateAndStart,
			exp:    prepareExperiment(nil),
			setup:  readTopology,
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				Aliases:      defaultAliases,
				Schedules:    map[string]string{},
			},
		},
		{
			name:   "no experiment",
			spec:   workflow.Spec{Topology: "foo"},
			action: workflow.ActionUpdate,
			setup:  readTopology,
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				Aliases:      defaultAliases,
				Schedules:    map[string]string{},
			},
		},
		{
			name:    "no topology in the workflow or the annotations",
			action:  workflow.ActionRestart,
			exp:     prepareExperiment(store.Annotations{"scenario": "bar"}),
			wantErr: workflow.ErrUnresolved,
			wantMsg: "no topology",
		},
		{
			name:    "no topology and no experiment",
			action:  workflow.ActionUpdate,
			wantErr: workflow.ErrUnresolved,
			wantMsg: "no topology",
		},
		{
			name:    "topology does not exist",
			action:  workflow.ActionUpdate,
			exp:     annotated(),
			setup:   func(m *store.MockStore) { expectPrepareReadFails(m, "topology/foo") },
			wantErr: workflow.ErrUnresolved,
			wantMsg: `topology "foo": config does not exist`,
		},
		{
			name:   "included topology does not exist",
			action: workflow.ActionRestart,
			exp:    annotated(),
			setup: func(m *store.MockStore) {
				expectPrepareRead(m, "topology/foo", withInclude)
				expectPrepareReadFails(m, "topology/missing-include")
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: "loading included topology missing-include",
		},
		{
			name:   "scenario does not exist",
			action: workflow.ActionUpdateAndStart,
			exp:    annotated(),
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareReadFails(m, "scenario/bar")
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `scenario "bar": config does not exist`,
		},
		{
			name:   "fromScenario is for another topology",
			action: workflow.ActionRestart,
			exp:    annotated(),
			setup: func(m *store.MockStore) {
				readTopology(m)
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "", fromBase))
				expectPrepareRead(m, "scenario/base", prepareScenario("base", "other", baseApp))
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: "topology mismatch for scenario base",
		},
	})
}

// nodeCheckCase is one stored topology foo that [workflow.Prepare] checks as
// creating or updating an experiment from it would. setup declares the store
// reads besides the one read of topology/foo; nil means none. A non-empty
// wantMsg is the text the unresolved-reference error must contain; otherwise
// Prepare must succeed with the node hostnames and, on an update, the VLAN
// aliases of the merged topology.
type nodeCheckCase struct {
	name      string
	topology  store.Config
	setup     func(m *store.MockStore)
	hostnames []string
	aliases   map[string]int
	wantMsg   string
}

// nodeCheckCases returns the topologies whose nodes Prepare must refuse, with
// the node error's own text, and the ones it must pass. The node rules
// themselves are tested with types/version/v1; a node named all stands for
// them here.
func nodeCheckCases() []nodeCheckCase {
	var (
		unusable   = `topology "foo" cannot be used in an experiment: `
		external   = map[string]any{"external": true, "general": map[string]any{"hostname": "ext-c"}}
		noHardware = map[string]any{"type": "VirtualMachine", "general": map[string]any{"hostname": "host-b"}}
		noGeneral  = map[string]any{"type": "VirtualMachine", "hardware": map[string]any{"os_type": "linux"}}
		withNet    = vmNode("host-a")
		emptyIface = vmNode("host-a")
		withInc    = prepareTopologyWithNodes("foo", vmNode("host-a"))
		emptyNode  = prepareTopologyWithNodes("foo", vmNode("host-a"))
	)

	withNet["network"] = map[string]any{"interfaces": []any{map[string]any{"name": "IF0", "vlan": "EXP"}}}
	emptyIface["network"] = map[string]any{"interfaces": []any{map[string]any{"name": "IF0", "vlan": "EXP"}, nil}}
	withInc.Spec["includeTopologies"] = []any{"inc"}
	// A null list entry, as YAML's "- " or "- null" parses.
	emptyNode.Spec["nodes"] = []any{vmNode("host-a"), nil}

	return []nodeCheckCase{
		{
			name:     "node named all",
			topology: prepareTopologyWithNodes("foo", vmNode("all")),
			wantMsg:  unusable + "validating node all: hostname 'all' is reserved",
		},
		{
			// Every violation is reported, on one line: the second follows
			// the first after "; ".
			name:     "two bad nodes",
			topology: prepareTopologyWithNodes("foo", vmNode("all"), vmNode("42")),
			wantMsg:  "; validating node 42: hostname '42' is all digits",
		},
		{
			name:     "non-external node without a hardware section",
			topology: prepareTopologyWithNodes("foo", vmNode("host-a"), noHardware),
			wantMsg:  unusable + "node host-b (nodes[1]) has no hardware section",
		},
		{
			name:     "node without a general section",
			topology: prepareTopologyWithNodes("foo", vmNode("host-a"), noGeneral),
			wantMsg:  unusable + "nodes[1] has no general section",
		},
		{
			name:     "empty node entry",
			topology: emptyNode,
			wantMsg:  unusable + "nodes[1] is empty",
		},
		{
			name:     "empty interface entry",
			topology: prepareTopologyWithNodes("foo", emptyIface),
			wantMsg:  unusable + "node host-a (nodes[0]): interfaces[1] is empty",
		},
		{
			// The check fails before the second decode, so the include is read
			// once.
			name:     "bad hostname in an included topology",
			topology: withInc,
			setup: func(m *store.MockStore) {
				expectPrepareRead(m, "topology/inc", prepareTopologyWithNodes("inc", vmNode("all")))
			},
			wantMsg: unusable + "validating node all: hostname 'all' is reserved",
		},
		{
			// A failed second decode is an unresolved reference too.
			name:     "included topology that disappears between the two decodes",
			topology: withInc,
			setup: func(m *store.MockStore) {
				expectPrepareRead(m, "topology/inc", prepareTopologyWithNodes("inc", vmNode("inc-a")))
				expectPrepareReadFails(m, "topology/inc")
			},
			wantMsg: `decoding topology "foo": loading included topology inc: could not find topology inc`,
		},
		{
			name: "router and firewall nodes",
			topology: prepareTopologyWithNodes(
				"foo",
				map[string]any{
					"type":     "Router",
					"general":  map[string]any{"hostname": "Router-1"},
					"hardware": map[string]any{"os_type": "vyos"},
				},
				map[string]any{
					"type":     "Firewall",
					"general":  map[string]any{"hostname": "fw-1"},
					"hardware": map[string]any{"os_type": "vyatta"},
				},
			),
			hostnames: []string{"Router-1", "fw-1"},
		},
		{
			// Both hostnames only warn.
			name:      "linux node named phenix and a node named All",
			topology:  prepareTopologyWithNodes("foo", vmNode("phenix"), vmNode("All")),
			hostnames: []string{"phenix", "All"},
		},
		{
			name:      "external node without a hardware section",
			topology:  prepareTopologyWithNodes("foo", vmNode("host-a"), external),
			hostnames: []string{"host-a", "ext-c"},
		},
		{
			name:      "node with a network and a node without",
			topology:  prepareTopologyWithNodes("foo", withNet, vmNode("host-b")),
			hostnames: []string{"host-a", "host-b"},
			aliases:   map[string]int{"EXP": 0},
		},
		{
			// The include is loaded for each of the two decodes.
			name:     "valid included topology",
			topology: withInc,
			setup: func(m *store.MockStore) {
				m.EXPECT().Get(gomock.Eq(prepareKey("topology/inc"))).
					SetArg(0, prepareTopologyWithNodes("inc", vmNode("inc-a"))).Return(nil).Times(2)
			},
			hostnames: []string{"host-a", "inc-a"},
		},
	}
}

// TestPrepareChecksTopologyNodes checks that a stored topology's nodes are
// checked as creating or updating an experiment from it would check them, on
// a create, an update and a restart plan, with the node error's text in an
// unresolved-reference error. The mock allows only the reads that resolving
// the topology makes, so a violation is found before anything changes.
func TestPrepareChecksTopologyNodes(t *testing.T) {
	plans := []struct {
		name   string
		action workflow.Action
		spec   workflow.Spec
		exp    *types.Experiment
		update bool
	}{
		{name: "create", action: workflow.ActionCreate, spec: workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: "foo"}},
		{name: "update", action: workflow.ActionUpdate, spec: workflow.Spec{Topology: "foo"}, exp: prepareExperiment(nil), update: true},
		{name: "restart", action: workflow.ActionRestart, spec: workflow.Spec{Topology: "foo"}, exp: prepareExperiment(nil), update: true},
	}

	for _, plan := range plans {
		t.Run(plan.name, func(t *testing.T) {
			checks := nodeCheckCases()
			cases := make([]prepareCase, 0, len(checks))

			for _, nc := range checks {
				tc := prepareCase{
					name:   nc.name,
					spec:   plan.spec,
					action: plan.action,
					exp:    plan.exp,
					setup: func(m *store.MockStore) {
						expectPrepareRead(m, "topology/foo", nc.topology)
						if nc.setup != nil {
							nc.setup(m)
						}
					},
				}

				if nc.wantMsg != "" {
					tc.wantErr = workflow.ErrUnresolved
					tc.wantMsg = nc.wantMsg
				} else {
					tc.want = preparedSummary{TopologyName: "foo", Hostnames: nc.hostnames}
					if plan.update {
						tc.want.Aliases = map[string]int{}
						tc.want.Schedules = map[string]string{}
						maps.Copy(tc.want.Aliases, nc.aliases)
					}
				}

				cases = append(cases, tc)
			}

			runPrepareCases(t, cases)
		})
	}
}

// TestPrepareLeavesTopologyUntouched checks that the node check does not
// change the topology Prepare returns: it equals a fresh decode of the stored
// config, on which initializing the topology, as an experiment does, fills in
// node defaults that the returned topology does not have.
func TestPrepareLeavesTopologyUntouched(t *testing.T) {
	plans := []struct {
		name   string
		action workflow.Action
		spec   workflow.Spec
		exp    *types.Experiment
	}{
		{name: "create", action: workflow.ActionCreate, spec: workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: "foo"}},
		{name: "update", action: workflow.ActionUpdate, spec: workflow.Spec{Topology: "foo"}, exp: prepareExperiment(nil)},
	}

	for _, plan := range plans {
		t.Run(plan.name, func(t *testing.T) {
			expectPrepareRead(installPrepareStore(t), "topology/foo", prepareTopology())

			got, err := workflow.Prepare(plan.spec, workflow.Plan{Action: plan.action, Experiment: "exp1", Reason: ""}, plan.exp)
			if err != nil {
				t.Fatalf("Prepare() unexpected error: %v", err)
			}

			// The stored topology has no includes, so decoding it reads nothing.
			fresh, err := types.DecodeTopologyFromConfig(prepareTopology())
			if err != nil {
				t.Fatalf("decoding the stored topology: %v", err)
			}

			if !reflect.DeepEqual(got.Topology, fresh) {
				t.Errorf("Prepare() topology = %+v, want the stored config decoded, %+v", got.Topology, fresh)
			}

			if err := fresh.Init("phenix"); err != nil {
				t.Fatalf("initializing the fresh decode: %v", err)
			}

			if reflect.DeepEqual(got.Topology, fresh) {
				t.Error("Prepare() topology has the node defaults an experiment fills in")
			}

			hostA := got.Topology.Nodes()[0]

			var (
				vmType = hostA.General().VMType()
				cpu    = hostA.Hardware().CPU()
				bridge = hostA.Network().Interfaces()[0].Bridge()
			)

			if vmType != "" || cpu != "" || bridge != "" {
				t.Errorf("Prepare() topology node host-a has vm_type %q, cpu %q and bridge %q, want none set", vmType, cpu, bridge)
			}
		})
	}
}

// TestPrepareUpdateChecksAnnotatedTopology checks that the topology an update
// takes from the experiment's annotation is checked as one the workflow names.
func TestPrepareUpdateChecksAnnotatedTopology(t *testing.T) {
	runPrepareCases(t, []prepareCase{
		{
			name:   "annotated topology with a node named all",
			action: workflow.ActionUpdate,
			exp:    prepareExperiment(store.Annotations{"topology": "foo"}),
			setup: func(m *store.MockStore) {
				expectPrepareRead(m, "topology/foo", prepareTopologyWithNodes("foo", vmNode("all")))
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `topology "foo" cannot be used in an experiment: validating node all: hostname 'all' is reserved`,
		},
	})
}

// TestCheckConfig checks the node checks a config dry run and the upsert make
// on a Topology's own nodes, without loading its includes: the store mock
// allows no call. A config of another kind passes, and no error wraps
// [workflow.ErrUnresolved], which is for the apply.
func TestCheckConfig(t *testing.T) {
	var (
		unusable = `topology "foo" cannot be used in an experiment: `
		external = map[string]any{"external": true, "general": map[string]any{"hostname": "ext-c"}}
		router   = map[string]any{
			"type":     "Router",
			"general":  map[string]any{"hostname": "Router-1"},
			"hardware": map[string]any{"os_type": "vyos"},
		}
		noHardware = map[string]any{"type": "VirtualMachine", "general": map[string]any{"hostname": "host-b"}}
		scenario   = prepareScenario("bar", "foo", map[string]any{"name": "app1"})
		withInc    = prepareTopologyWithNodes("foo", vmNode("host-a"))
		badInc     = prepareTopologyWithNodes("foo", vmNode("all"))
		mapInc     = prepareTopologyWithNodes("foo", vmNode("host-a"))
		emptyNode  = prepareTopologyWithNodes("foo", vmNode("host-a"))
		emptyIface = vmNode("host-a")
		emptyExt   = map[string]any{"external": true, "general": map[string]any{"hostname": "ext-c"}}
	)

	scenario.Spec["nodes"] = []any{map[string]any{"general": map[string]any{"hostname": "all"}}}
	withInc.Spec["includeTopologies"] = []any{"not-stored"}
	badInc.Spec["includeTopologies"] = []any{"not-stored"}
	mapInc.Spec["includeTopologies"] = []any{map[string]any{"name": "x"}}
	// A null list entry, as YAML's "- " or "- null" parses.
	emptyNode.Spec["nodes"] = []any{vmNode("host-a"), nil}
	emptyIface["network"] = map[string]any{"interfaces": []any{nil}}
	emptyExt["network"] = map[string]any{"interfaces": []any{map[string]any{"name": "IF0", "vlan": "EXT"}, nil}}

	tests := []struct {
		name    string
		cfg     store.Config
		wantMsg string
	}{
		{name: "scenario whose spec carries nodes", cfg: scenario},
		{
			name:    "node named all",
			cfg:     prepareTopologyWithNodes("foo", vmNode("all")),
			wantMsg: unusable + "validating node all: hostname 'all' is reserved",
		},
		{
			name:    "two bad nodes",
			cfg:     prepareTopologyWithNodes("foo", vmNode("all"), vmNode("42")),
			wantMsg: "; validating node 42: hostname '42' is all digits",
		},
		{name: "router and external node", cfg: prepareTopologyWithNodes("foo", router, external)},
		{
			name:    "node without a hardware section",
			cfg:     prepareTopologyWithNodes("foo", noHardware),
			wantMsg: unusable + "node host-b (nodes[0]) has no hardware section",
		},
		{name: "empty node entry", cfg: emptyNode, wantMsg: unusable + "nodes[1] is empty"},
		{
			name:    "empty interface entry",
			cfg:     prepareTopologyWithNodes("foo", emptyIface),
			wantMsg: unusable + "node host-a (nodes[0]): interfaces[0] is empty",
		},
		{
			// An external node's interfaces are read by an update's VLAN
			// aliases, so its entries are checked too.
			name:    "external node with an empty interface entry",
			cfg:     prepareTopologyWithNodes("foo", vmNode("host-a"), emptyExt),
			wantMsg: unusable + "node ext-c (nodes[1]): interfaces[1] is empty",
		},
		{name: "include that is not stored", cfg: withInc},
		{name: "include that is not stored and a node named all", cfg: badInc, wantMsg: unusable + "validating node all"},
		{name: "topology that does not decode", cfg: mapInc, wantMsg: `decoding topology "foo": `},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			installPrepareStore(t)

			err := workflow.CheckConfig(tc.cfg)

			if tc.wantMsg == "" {
				if err != nil {
					t.Fatalf("CheckConfig() unexpected error: %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("CheckConfig() error = %v, want it to contain %q", err, tc.wantMsg)
			}

			if errors.Is(err, workflow.ErrUnresolved) {
				t.Errorf("CheckConfig() error = %v, want it not to wrap %v", err, workflow.ErrUnresolved)
			}
		})
	}
}

// TestPreparePending checks configs named as pending by bare Kind/name refs.
// A dry run names the configs its client upserts before the real apply: they
// are not read, and every config that is not pending is resolved as usual.
// TestPreparePendingReferences covers refs that describe their references.
func TestPreparePending(t *testing.T) {
	var (
		hostnames = []string{"host-a", "host-b", "ext-c"}
		app1      = map[string]any{"name": "app1"}
		create    = workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: "foo", Scenario: "bar"}
		update    = workflow.Spec{Topology: "foo", Scenario: "bar", VLANs: map[string]int{"EXP": 101}}
	)

	readTopology := func(m *store.MockStore) { expectPrepareRead(m, "topology/foo", prepareTopology()) }

	runPrepareCases(t, []prepareCase{
		{
			// A topology directory deployed for the first time, with the refs
			// its config dry runs returned: nothing is stored yet, so nothing
			// is read.
			name:    "fresh create",
			spec:    create,
			action:  workflow.ActionCreateAndStart,
			pending: []string{"Topology/foo", "Scenario/bar", "Image/base"},
			want:    preparedSummary{TopologyName: "foo", ScenarioName: "bar"},
		},
		{
			name:    "create with a pending topology and a stored scenario",
			spec:    create,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo"},
			setup: func(m *store.MockStore) {
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "foo", app1))
			},
			want: preparedSummary{TopologyName: "foo", ScenarioName: "bar", AppAssetDirs: map[string]string{"app1": ""}},
		},
		{
			name:    "a stored scenario must still be for the pending topology",
			spec:    create,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo"},
			setup: func(m *store.MockStore) {
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "other", app1))
			},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `scenario "bar" is annotated for topology "other", not "foo"`,
		},
		{
			// The stored topology is not read either: the upsert replaces it.
			// Kinds match in any case, as they do in store.NewConfig.
			name:    "restart onto a pending topology",
			spec:    update,
			action:  workflow.ActionRestart,
			exp:     prepareExperiment(nil),
			pending: []string{"TOPOLOGY/foo"},
			setup: func(m *store.MockStore) {
				expectPrepareRead(m, "scenario/bar", prepareScenario("bar", "", app1))
			},
			// Without the topology there are no aliases or schedules to build.
			want: preparedSummary{TopologyName: "foo", ScenarioName: "bar", AppAssetDirs: map[string]string{"app1": ""}},
		},
		{
			name:    "update with a pending scenario",
			spec:    update,
			action:  workflow.ActionUpdate,
			pending: []string{"Scenario/bar"},
			setup:   readTopology,
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				ScenarioName: "bar",
				Aliases:      map[string]int{"EXP": 101, "MGMT": 0, "EXT": 0},
				Schedules:    map[string]string{},
			},
		},
		{
			// Names match exactly, as store keys do.
			name:    "pending name in another case",
			spec:    workflow.Spec{Topology: "foo"},
			action:  workflow.ActionUpdate,
			pending: []string{"Topology/FOO"},
			setup:   readTopology,
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				Aliases:      map[string]int{"EXP": 0, "MGMT": 0, "EXT": 0},
				Schedules:    map[string]string{},
			},
		},
		{
			name:    "pending names other configs",
			spec:    workflow.Spec{Topology: "foo"},
			action:  workflow.ActionUpdateAndStart,
			pending: []string{"Topology/other", "Experiment/foo"},
			setup:   readTopology,
			want: preparedSummary{
				TopologyName: "foo",
				Hostnames:    hostnames,
				Aliases:      map[string]int{"EXP": 0, "MGMT": 0, "EXT": 0},
				Schedules:    map[string]string{},
			},
		},
		{
			// Pending refs are checked before the action, even one that needs
			// nothing.
			name:    "pending is not a config name",
			spec:    create,
			action:  workflow.ActionNone,
			pending: []string{"foo"},
			wantErr: workflow.ErrInvalidSpec,
			wantMsg: `pending config "foo" is not a <kind>/<name> config name`,
		},
		{
			name:    "pending names an unknown kind",
			spec:    create,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo", "Workflow/foo"},
			wantErr: workflow.ErrInvalidSpec,
			wantMsg: `pending config "Workflow/foo"`,
		},
		{
			name:    "pending does not excuse a topology name that is not a config name",
			spec:    workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: "a/b"},
			action:  workflow.ActionCreate,
			pending: []string{"Topology/a"},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `topology "a/b": invalid config name provided`,
		},
	})
}

// TestCheckBridge checks the default bridge an apply gives its experiment,
// which the experiment config hooks would otherwise reject only after a
// running experiment is stopped.
func TestCheckBridge(t *testing.T) {
	bridged := func(name, bridge string) types.Experiment {
		return types.Experiment{
			Metadata: store.ConfigMetadata{Name: name},
			Spec:     &v1.ExperimentSpec{DefaultBridgeF: bridge},
		}
	}

	var (
		exps     = []types.Experiment{bridged("exp1", "br0"), bridged("bar", "br1"), bridged("baz", "phenix")}
		barOnBr1 = `experiment "bar" already uses default bridge "br1"`
		manual   = common.BridgeModeManual
		auto     = common.BridgeModeAuto
	)

	tests := []struct {
		name    string
		bridge  string
		action  workflow.Action
		exp     string
		mode    common.BridgingMode
		wantMsg string
	}{
		{
			name:   "nothing to do",
			bridge: "br1",
			action: workflow.ActionNone,
			exp:    "exp1",
			mode:   manual,
		},
		{
			name:   "shared phenix bridge",
			action: workflow.ActionRestart,
			exp:    "exp1",
			mode:   manual,
		},
		{
			name:   "bridge only its own experiment uses",
			bridge: "br0",
			action: workflow.ActionUpdate,
			exp:    "exp1",
			mode:   manual,
		},
		{
			name:   "unused bridge",
			bridge: "br9",
			action: workflow.ActionCreateAndStart,
			exp:    "new",
			mode:   manual,
		},
		{
			name:    "update onto a bridge another experiment uses",
			bridge:  "br1",
			action:  workflow.ActionRestart,
			exp:     "exp1",
			mode:    manual,
			wantMsg: barOnBr1,
		},
		{
			name:    "create onto a bridge another experiment uses",
			bridge:  "br1",
			action:  workflow.ActionCreate,
			exp:     "new",
			mode:    manual,
			wantMsg: barOnBr1,
		},
		{
			// The auto bridge mode ignores the workflow's default bridge.
			name:   "auto mode names the bridge after the experiment",
			bridge: "br1",
			action: workflow.ActionUpdateAndStart,
			exp:    "exp1",
			mode:   auto,
		},
		{
			name:    "auto mode and a long experiment name",
			action:  workflow.ActionCreateAndStart,
			exp:     "exp-name-over-15",
			mode:    auto,
			wantMsg: `experiment name "exp-name-over-15" must be 15 characters or less when the bridge mode is auto`,
		},
		{
			name:    "auto mode and an experiment named like another's bridge",
			action:  workflow.ActionCreate,
			exp:     "br1",
			mode:    auto,
			wantMsg: barOnBr1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				spec = workflow.Spec{DefaultBridge: tc.bridge}
				plan = workflow.Plan{Action: tc.action, Experiment: tc.exp, Reason: ""}
				err  = workflow.CheckBridge(spec, plan, exps, tc.mode)
			)

			if tc.wantMsg == "" {
				if err != nil {
					t.Fatalf("CheckBridge() unexpected error: %v", err)
				}

				return
			}

			if !errors.Is(err, workflow.ErrInvalidSpec) || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("CheckBridge() error = %v, want an %v containing %q", err, workflow.ErrInvalidSpec, tc.wantMsg)
			}
		})
	}
}

// TestPendingRef checks the ref a config dry run answers with, which names
// the config as pending in an apply dry run. A scenario's ref describes its
// topology annotation and the scenarios its apps take settings from, and a
// topology's describes the topologies it includes. Both are read as the real
// apply decodes the stored config: keys match in any letter case, and a
// topology's includes are decoded weakly.
func TestPendingRef(t *testing.T) {
	var (
		app1        = map[string]any{"name": "app1", "fromScenario": "base"}
		app2        = map[string]any{"name": "app2"}
		app3        = map[string]any{"name": "app3", "fromScenario": "base"}
		app4        = map[string]any{"name": "app4", "fromScenario": "extra"}
		upperFrom   = map[string]any{"name": "app1", "FromScenario": "base"}
		numericFrom = map[string]any{"name": "app1", "fromScenario": 5}
		withInclude = prepareTopology()
		scalar      = prepareTopology()
		upper       = prepareTopology()
		mapInclude  = prepareTopology()
	)

	withInclude.Spec["includeTopologies"] = []any{"inc-a", 7, "/phenix/topologies/b/topology.yml"}
	scalar.Spec["includeTopologies"] = "inc-a"
	upper.Spec["IncludeTopologies"] = []any{"inc-a"}
	mapInclude.Spec["includeTopologies"] = []any{"inc-a", map[string]any{"name": "inc-b"}}

	tests := []struct {
		name string
		cfg  store.Config
		want string
	}{
		{
			// Each fromScenario is listed once, in app order.
			name: "scenario",
			cfg:  prepareScenario("bar", "foo,other", app1, app2, app3, app4),
			want: "Scenario/bar?from=base&from=extra&topology=foo%2Cother",
		},
		{
			// The "?" with nothing after it says the scenario has no topology
			// annotation.
			name: "scenario without a topology annotation or fromScenario",
			cfg:  prepareScenario("bar", "", app2),
			want: "Scenario/bar?",
		},
		{
			// mapstructure matches a key in any letter case, and so does
			// merging the stored scenario.
			name: "scenario app key in another letter case",
			cfg:  prepareScenario("bar", "foo", upperFrom),
			want: "Scenario/bar?from=base&topology=foo",
		},
		{
			// fromScenario is not in the v2 Scenario schema, so 5 passes it,
			// but the scenario does not decode: the real apply fails there,
			// before it merges. The annotation is still described.
			name: "scenario that does not decode",
			cfg:  prepareScenario("bar", "foo", numericFrom),
			want: "Scenario/bar?topology=foo",
		},
		{
			// A v1 scenario keeps its apps in a map, and the upgrader gives
			// them no fromScenario.
			name: "v1 scenario",
			cfg: store.Config{
				Version:  "phenix.sandia.gov/v1",
				Kind:     "Scenario",
				Metadata: store.ConfigMetadata{Name: "old"},
				Spec:     map[string]any{"apps": map[string]any{"experiment": []any{}}},
			},
			want: "Scenario/old?",
		},
		{
			// The weak decode names an include 7 by its decimal string.
			name: "topology that includes others",
			cfg:  withInclude,
			want: "Topology/foo?include=inc-a&include=7&include=%2Fphenix%2Ftopologies%2Fb%2Ftopology.yml",
		},
		{
			// The weak decode makes a single value a one-entry list.
			name: "scalar include",
			cfg:  scalar,
			want: "Topology/foo?include=inc-a",
		},
		{
			name: "include key in another letter case",
			cfg:  upper,
			want: "Topology/foo?include=inc-a",
		},
		{
			// A mapping is no include name, so the topology does not decode:
			// the real apply fails there, before it loads any include.
			name: "topology that does not decode",
			cfg:  mapInclude,
			want: "Topology/foo",
		},
		{
			name: "topology",
			cfg:  prepareTopology(),
			want: "Topology/foo",
		},
		{
			name: "other kinds",
			cfg:  store.Config{Version: "phenix.sandia.gov/v1", Kind: "Image", Metadata: store.ConfigMetadata{Name: "base"}},
			want: "Image/base",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflow.PendingRef(tc.cfg); got != tc.want {
				t.Errorf("PendingRef() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPreparePendingReferences checks pending configs whose refs, as
// [workflow.PendingRef] builds them, describe what they refer to. Prepare
// reads none of the pending configs, but it looks for each fromScenario and
// include that is not pending, as the real apply will.
func TestPreparePendingReferences(t *testing.T) {
	var (
		app1     = map[string]any{"name": "app1"}
		create   = workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: "foo", Scenario: "bar"}
		topoOnly = workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: "foo"}
		barFrom  = "Scenario/bar?from=base&topology=foo"
		fooBar   = preparedSummary{TopologyName: "foo", ScenarioName: "bar"}
		fooOnly  = preparedSummary{TopologyName: "foo"}
	)

	// A path is not a config name, so looking for this include skips the
	// store and finds the file, as decoding the topology would.
	file := filepath.Join(t.TempDir(), "include.yml")
	if err := os.WriteFile(file, []byte("kind: Topology\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", file, err)
	}

	runPrepareCases(t, []prepareCase{
		{
			// A directory deployed for the first time, as its config dry runs
			// describe it: nothing is read.
			name:    "fresh directory",
			spec:    create,
			action:  workflow.ActionCreateAndStart,
			pending: []string{"Topology/foo", barFrom, "Scenario/base?topology=foo%2Cother"},
			want:    fooBar,
		},
		{
			// An update does not check the scenario's own topology annotation.
			name:    "restart onto a pending scenario annotated for another topology",
			spec:    workflow.Spec{Topology: "foo", Scenario: "bar"},
			action:  workflow.ActionRestart,
			exp:     prepareExperiment(nil),
			pending: []string{"Topology/foo", "Scenario/bar?topology=other"},
			want:    fooBar,
		},
		{
			name:    "fromScenario stored",
			spec:    create,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo", barFrom},
			setup: func(m *store.MockStore) {
				expectPrepareRead(m, "scenario/base", prepareScenario("base", "foo", app1))
			},
			want: fooBar,
		},
		{
			// A bare ref says nothing about the scenario it names.
			name:    "fromScenario pending with a bare ref",
			spec:    create,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo", barFrom, "Scenario/base"},
			want:    fooBar,
		},
		{
			// A name the store cannot address is reported as missing, with no
			// store call, as the real apply's merge reports it.
			name:    "fromScenario that is not a config name",
			spec:    create,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo", "Scenario/bar?from=a%2Fb&topology=foo"},
			wantErr: workflow.ErrUnresolved,
			wantMsg: `merging scenario "bar": scenario a/b doesn't exist`,
		},
		{
			name:    "included topology pending",
			spec:    topoOnly,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo?include=inc", "Topology/inc"},
			want:    fooOnly,
		},
		{
			name:    "included topology stored",
			spec:    topoOnly,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo?include=inc"},
			setup:   func(m *store.MockStore) { expectPrepareRead(m, "topology/inc", prepareTopology()) },
			want:    fooOnly,
		},
		{
			name:    "included topology is a file",
			spec:    topoOnly,
			action:  workflow.ActionCreateAndStart,
			pending: []string{"Topology/foo?" + url.Values{"include": {file}}.Encode()},
			want:    fooOnly,
		},
		{
			name:    "references that do not parse",
			spec:    create,
			action:  workflow.ActionCreate,
			pending: []string{"Topology/foo", "Scenario/bar?topology=%zz"},
			wantErr: workflow.ErrInvalidSpec,
			wantMsg: `pending config "Scenario/bar?topology=%zz": invalid URL escape`,
		},
	})
}

// TestPreparePendingMatchesStored checks that a dry run fails exactly as the
// real apply does. The dry run names every config in the directory as
// pending, with the refs [workflow.PendingRef] makes for them. The real apply
// runs after the upsert, with the same configs stored, and reads each of
// them once. The dry run reads only the configs that are neither in the
// directory nor stored, which both runs look for.
func TestPreparePendingMatchesStored(t *testing.T) {
	var (
		app1      = map[string]any{"name": "app1"}
		fromBase  = map[string]any{"name": "app1", "fromScenario": "base"}
		upperFrom = map[string]any{"name": "app1", "FromScenario": "base"}
		create    = workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: "foo", Scenario: "bar"}
		topoOnly  = workflow.Spec{Auto: &workflow.Auto{Create: "exp1"}, Topology: "foo"}
		topology  = prepareTopology()
		notFound  = `decoding topology "foo": loading included topology missing-include: could not find topology missing-include`
	)

	// The includes pass the v2 Topology schema, which does not declare them,
	// in every form the real apply's weak decode accepts.
	withInclude := prepareTopology()
	withInclude.Spec["includeTopologies"] = []any{"missing-include"}

	scalarInclude := prepareTopology()
	scalarInclude.Spec["includeTopologies"] = "missing-include"

	upperInclude := prepareTopology()
	upperInclude.Spec["IncludeTopologies"] = []any{"missing-include"}

	numericInclude := prepareTopology()
	numericInclude.Spec["includeTopologies"] = []any{7}

	tests := []struct {
		name    string
		spec    workflow.Spec
		action  workflow.Action
		configs []store.Config // the directory's configs, in the order the real apply reads them
		missing []string       // configs neither in the directory nor stored
		wantMsg string
	}{
		{
			name:    "scenario annotated for another topology",
			spec:    create,
			action:  workflow.ActionCreate,
			configs: []store.Config{topology, prepareScenario("bar", "other", app1)},
			wantMsg: `scenario "bar" is annotated for topology "other", not "foo"`,
		},
		{
			name:    "scenario without a topology annotation",
			spec:    create,
			action:  workflow.ActionCreateAndStart,
			configs: []store.Config{topology, prepareScenario("bar", "", app1)},
			wantMsg: `scenario "bar" has no topology annotation`,
		},
		{
			name:    "fromScenario that does not exist",
			spec:    create,
			action:  workflow.ActionCreate,
			configs: []store.Config{topology, prepareScenario("bar", "foo", fromBase)},
			missing: []string{"scenario/base"},
			wantMsg: `merging scenario "bar": scenario base doesn't exist`,
		},
		{
			// An update checks the fromScenario's annotation, not the
			// scenario's own.
			name:    "fromScenario annotated for another topology",
			spec:    workflow.Spec{Topology: "foo", Scenario: "bar"},
			action:  workflow.ActionRestart,
			configs: []store.Config{topology, prepareScenario("bar", "", fromBase), prepareScenario("base", "other", app1)},
			wantMsg: `merging scenario "bar": experiment/scenario topology mismatch for scenario base`,
		},
		{
			name:    "fromScenario without a topology annotation",
			spec:    create,
			action:  workflow.ActionCreate,
			configs: []store.Config{topology, prepareScenario("bar", "foo", fromBase), prepareScenario("base", "", app1)},
			wantMsg: `merging scenario "bar": topology annotation missing from scenario base`,
		},
		{
			// Decoding the stored scenario reads FromScenario as fromScenario.
			name:    "fromScenario key in another letter case that does not exist",
			spec:    create,
			action:  workflow.ActionCreate,
			configs: []store.Config{topology, prepareScenario("bar", "foo", upperFrom)},
			missing: []string{"scenario/base"},
			wantMsg: `merging scenario "bar": scenario base doesn't exist`,
		},
		{
			// Neither run finds a file named missing-include in the test's
			// working directory.
			name:    "included topology that does not exist",
			spec:    topoOnly,
			action:  workflow.ActionCreate,
			configs: []store.Config{withInclude},
			missing: []string{"topology/missing-include"},
			wantMsg: notFound,
		},
		{
			name:    "scalar include that does not exist",
			spec:    topoOnly,
			action:  workflow.ActionCreateAndStart,
			configs: []store.Config{scalarInclude},
			missing: []string{"topology/missing-include"},
			wantMsg: notFound,
		},
		{
			name:    "include key in another letter case that does not exist",
			spec:    topoOnly,
			action:  workflow.ActionCreate,
			configs: []store.Config{upperInclude},
			missing: []string{"topology/missing-include"},
			wantMsg: notFound,
		},
		{
			// Nor a file named 7.
			name:    "numeric include that does not exist",
			spec:    topoOnly,
			action:  workflow.ActionCreate,
			configs: []store.Config{numericInclude},
			missing: []string{"topology/7"},
			wantMsg: `decoding topology "foo": loading included topology 7: could not find topology 7`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := workflow.Plan{Action: tc.action, Experiment: "exp1", Reason: ""}

			// The real apply, after the upsert: the directory's configs are
			// stored.
			m := installPrepareStore(t)
			for _, cfg := range tc.configs {
				expectPrepareRead(m, cfg.FullName(), cfg)
			}

			for _, name := range tc.missing {
				expectPrepareReadFails(m, name)
			}

			_, stored := workflow.Prepare(tc.spec, plan, nil)

			// The dry run, before the upsert: the directory's configs are
			// pending.
			m = installPrepareStore(t)
			for _, name := range tc.missing {
				expectPrepareReadFails(m, name)
			}

			refs := make([]string, 0, len(tc.configs))
			for _, cfg := range tc.configs {
				refs = append(refs, workflow.PendingRef(cfg))
			}

			_, dryRun := workflow.Prepare(tc.spec, plan, nil, refs...)

			if !errors.Is(stored, workflow.ErrUnresolved) || !strings.Contains(stored.Error(), tc.wantMsg) {
				t.Fatalf("real apply: Prepare() error = %v, want an %v containing %q", stored, workflow.ErrUnresolved, tc.wantMsg)
			}

			if dryRun == nil || dryRun.Error() != stored.Error() {
				t.Errorf("dry run: Prepare() error = %v, want the real apply's %q", dryRun, stored)
			}
		})
	}
}
