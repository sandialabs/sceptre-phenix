package experiment

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/mm"
)

// bridgeNode is a VM node with one interface on bridge, "" for the experiment's default bridge.
func bridgeNode(hostname, bridge string) map[string]any {
	iface := map[string]any{"name": "IF0", "type": "ethernet", "vlan": "EXP-1", "proto": "dhcp"}
	if bridge != "" {
		iface["bridge"] = bridge
	}

	return map[string]any{
		"type":    "VirtualMachine",
		"general": map[string]any{"hostname": hostname},
		"network": map[string]any{"interfaces": []any{iface}},
	}
}

// storeBridgeExperiment stores experiment name with default bridge bridge, the given status and nodes.
func storeBridgeExperiment(t *testing.T, name, bridge string, status map[string]any, nodes ...map[string]any) {
	t.Helper()

	list := make([]any, 0, len(nodes))
	for _, node := range nodes {
		list = append(list, node)
	}

	spec := map[string]any{
		"experimentName": name, "baseDir": t.TempDir(), "defaultBridge": bridge,
		"topology": map[string]any{"nodes": list},
	}

	if err := store.Create(hookConfig(name, spec, status)); err != nil {
		t.Fatalf("store.Create(experiment %s) error = %v", name, err)
	}
}

// bridgeExperiment is experiment name with default bridge bridge, decoded as Stop gets it.
func bridgeExperiment(t *testing.T, name, bridge string, status map[string]any, nodes ...map[string]any) *types.Experiment {
	t.Helper()

	storetest.Use(t)
	storeBridgeExperiment(t, name, bridge, status, nodes...)

	return experimentNamed(t, name)
}

// running is the status of an experiment that runs, with VMs on the hosts of schedule.
func running(schedule map[string]any) map[string]any {
	return map[string]any{"startTime": stopTestStart, "schedules": schedule}
}

func TestOwnBridge(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name   string
		mode   common.BridgingMode
		exp    string
		bridge string
		want   string
	}{
		{name: "manual mode with the experiment name", mode: common.BridgeModeManual, exp: "exp", bridge: "exp"},
		{name: "manual mode with another bridge", mode: common.BridgeModeManual, exp: "exp", bridge: "br1"},
		{name: "unset mode", mode: common.BridgeModeUnset, exp: "exp", bridge: "exp"},
		{name: "auto mode with the experiment name", mode: common.BridgeModeAuto, exp: "exp", bridge: "exp", want: "exp"},
		{name: "auto mode with the phenix bridge", mode: common.BridgeModeAuto, exp: "exp", bridge: defaultBridgeName},
		{name: "auto mode with a user bridge", mode: common.BridgeModeAuto, exp: "exp", bridge: "br1"},
		{name: "auto mode with no bridge", mode: common.BridgeModeAuto, exp: "exp"},
		{name: "auto mode, experiment named phenix", mode: common.BridgeModeAuto, exp: defaultBridgeName, bridge: defaultBridgeName},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useBridgeSettings(t, tc.mode, false)

			exp := bridgeExperiment(t, tc.exp, tc.bridge, nil)

			if got := ownBridge(exp); got != tc.want {
				t.Errorf("ownBridge(experiment %s, bridge %q, mode %q) = %q, want %q", tc.exp, tc.bridge, tc.mode, got, tc.want)
			}
		})
	}
}

func TestBridgeUser(t *testing.T) { //nolint:paralleltest // replaces the package store
	tests := []struct {
		name   string
		bridge string // the other experiment's default bridge
		iface  string // the bridge of the other experiment's interface, "" for its default bridge
		want   string
	}{
		{name: "another bridge", bridge: "other", iface: "br1"},
		{name: "the bridge as default bridge", bridge: "exp", want: "other"},
		{name: "the bridge on an interface", bridge: "other", iface: "exp", want: "other"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)
			storeBridgeExperiment(t, "exp", "exp", nil, bridgeNode("vm1", ""))
			storeBridgeExperiment(t, "other", tc.bridge, nil, bridgeNode("vm1", tc.iface))

			others := []*types.Experiment{experimentNamed(t, "exp"), experimentNamed(t, "other")}

			if got := bridgeUser("exp", "exp", others); got != tc.want {
				t.Errorf("bridgeUser(exp) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBridgeHosts(t *testing.T) { //nolint:paralleltest // replaces the package store
	tests := []struct {
		name     string
		headnode string
		status   map[string]any
		want     []string
	}{
		{name: "no VMs", headnode: "head", status: map[string]any{}, want: []string{"head"}},
		{name: "no head node name", status: running(map[string]any{"vm1": "h2"}), want: []string{"h2"}},
		{
			name: "each host once, sorted", headnode: "head",
			status: running(map[string]any{"vm1": "h3", "vm2": "head", "vm3": "h3", "vm4": "h2"}),
			want:   []string{"h2", "h3", "head"},
		},
		{
			name: "the host of the tap app", headnode: "head",
			status: map[string]any{
				"startTime": stopTestStart, "schedules": map[string]any{"vm1": "h2"},
				"apps": map[string]any{"tap": map[string]any{"host": "h4"}},
			},
			want: []string{"h2", "h4", "head"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exp := bridgeExperiment(t, "exp", "exp", tc.status)

			if diff := cmp.Diff(tc.want, bridgeHosts(exp, tc.headnode)); diff != "" {
				t.Errorf("bridgeHosts(exp, %q) mismatch (-want +got):\n%s", tc.headnode, diff)
			}
		})
	}
}

// Stop deletes the experiment's own bridge on each of its hosts in the auto bridge mode, after it clears the
// namespace, and in no other case.
func TestStopDeletesTheOwnBridgeInAutoMode(t *testing.T) { //nolint:paralleltest // replaces package globals
	schedule := map[string]any{"vm1": "h2", "vm2": "head"}
	failed := errors.New("ovs-vsctl failed")

	tests := []struct {
		name          string
		mode          common.BridgingMode
		bridge        string
		start         string // the start time, stopTestStart when ""
		clearErr      error
		destroyErr    error
		listErr       error // the store's List error after the experiments are stored
		force         bool
		other         func(t *testing.T) // stores other experiments
		wantDestroyed []destroyCall
		wantErr       []string
		wantRunning   bool
	}{
		{name: "manual mode with the experiment name", mode: common.BridgeModeManual, bridge: "exp"},
		{
			name: "auto mode with the experiment name", mode: common.BridgeModeAuto, bridge: "exp",
			wantDestroyed: []destroyCall{{Bridge: "exp", Hosts: []string{"h2", "head"}}},
		},
		{name: "auto mode with the phenix bridge", mode: common.BridgeModeAuto, bridge: defaultBridgeName},
		{name: "auto mode with a user bridge", mode: common.BridgeModeAuto, bridge: "br1"},
		{
			name: "auto mode with a running experiment on the bridge", mode: common.BridgeModeAuto, bridge: "exp",
			other: func(t *testing.T) {
				t.Helper()
				storeBridgeExperiment(t, "other", "other", running(nil), bridgeNode("vm1", "exp"))
			},
		},
		{
			name: "auto mode with a running experiment of the bridge as default", mode: common.BridgeModeAuto, bridge: "exp",
			other: func(t *testing.T) {
				t.Helper()
				storeBridgeExperiment(t, "other", "exp", running(nil), bridgeNode("vm1", ""))
			},
		},
		{
			name: "auto mode with a stopped experiment on the bridge", mode: common.BridgeModeAuto, bridge: "exp",
			other: func(t *testing.T) {
				t.Helper()
				storeBridgeExperiment(t, "other", "other", nil, bridgeNode("vm1", "exp"))
			},
			wantDestroyed: []destroyCall{{Bridge: "exp", Hosts: []string{"h2", "head"}}},
		},
		{
			name: "auto mode with a config that does not decode", mode: common.BridgeModeAuto, bridge: "exp",
			other: func(t *testing.T) {
				t.Helper()

				broken := hookConfig("broken", map[string]any{"experimentName": "broken"}, running(nil))
				broken.Version = "phenix.sandia.gov/v9"

				if err := store.Create(broken); err != nil {
					t.Fatalf("store.Create(experiment broken) error = %v", err)
				}
			},
		},
		{
			name: "auto mode with a failed delete", mode: common.BridgeModeAuto, bridge: "exp", destroyErr: failed,
			wantDestroyed: []destroyCall{{Bridge: "exp", Hosts: []string{"h2", "head"}}},
			wantErr:       []string{"deleting experiment bridge exp: ovs-vsctl failed"},
		},
		{
			name: "auto mode with a store that cannot list", mode: common.BridgeModeAuto, bridge: "exp",
			listErr: errors.New("store is locked"),
			wantErr: []string{"deleting experiment bridge exp: getting list of experiment configs from store: store is locked"},
		},
		{
			name: "auto mode with VMs that survive", mode: common.BridgeModeAuto, bridge: "exp", clearErr: failed,
			wantErr: []string{"killing experiment VMs: ovs-vsctl failed"}, wantRunning: true,
		},
		{
			name: "auto mode with VMs that survive a forced stop", mode: common.BridgeModeAuto, bridge: "exp",
			clearErr: failed, force: true,
		},
		{name: "auto mode dry run", mode: common.BridgeModeAuto, bridge: "exp", start: stopTestStart + "-DRYRUN"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := useRecoverStore(t)
			useBridgeSettings(t, tc.mode, false)

			start := tc.start
			if start == "" {
				start = stopTestStart
			}

			storeBridgeExperiment(t, "exp", tc.bridge, map[string]any{"startTime": start, "schedules": schedule},
				bridgeNode("vm1", ""), bridgeNode("vm2", ""))

			if tc.other != nil {
				tc.other(t)
			}

			s.listErr = tc.listErr
			fake := useStartMM(t, &startMM{clearErr: tc.clearErr, destroyErr: tc.destroyErr})
			hooksRun := countStopHooks(t)

			err := Stop("exp", StopWithKeepInjectionSnapshots(true), StopWithForce(tc.force))

			var gotErr []string
			if err != nil {
				gotErr = errorTexts(err)
			}

			if diff := cmp.Diff(tc.wantErr, gotErr, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Stop(exp) errors mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantDestroyed, fake.destroyed, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Stop(exp) bridge deletes mismatch (-want +got):\n%s", diff)
			}

			if got := experimentNamed(t, "exp").Running(); got != tc.wantRunning {
				t.Errorf("experiment running after Stop(exp) = %t, want %t", got, tc.wantRunning)
			}

			if got, want := *hooksRun, map[bool]int{false: 1, true: 0}[tc.wantRunning]; got != want {
				t.Errorf("Stop(exp) ran %d stop hooks, want %d", got, want)
			}
		})
	}
}

// orderMM records the order of the namespace clears and bridge deletes; other mm.MM methods panic.
type orderMM struct {
	mm.MM

	calls []string
}

func (*orderMM) Headnode() string { return "head" }

func (m *orderMM) ClearNamespace(ns string) error {
	m.calls = append(m.calls, "clear namespace "+ns)

	return nil
}

func (m *orderMM) DestroyBridge(bridge string, hosts ...string) error {
	m.calls = append(m.calls, fmt.Sprintf("destroy bridge %s on %v", bridge, hosts))

	return nil
}

// Stop deletes the bridge only after it cleared the namespace, so no VM of the experiment is on it.
func TestStopDeletesTheBridgeAfterClearingTheNamespace(t *testing.T) { //nolint:paralleltest // replaces package globals
	storetest.Use(t)
	useBridgeSettings(t, common.BridgeModeAuto, false)
	storeBridgeExperiment(t, "exp", "exp", running(map[string]any{"vm1": "h2"}), bridgeNode("vm1", ""))

	fake := &orderMM{}
	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore default

	mm.DefaultMM = fake //nolint:reassign // install test double

	if err := Stop("exp", StopWithKeepInjectionSnapshots(true)); err != nil {
		t.Fatalf("Stop(exp) error = %v, want nil", err)
	}

	want := []string{"clear namespace exp", "destroy bridge exp on [h2 head]"}
	if diff := cmp.Diff(want, fake.calls); diff != "" {
		t.Errorf("Stop(exp) minimega calls mismatch (-want +got):\n%s", diff)
	}
}
