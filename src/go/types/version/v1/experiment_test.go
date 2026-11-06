package v1

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	ifaces "phenix/types/interfaces"
	v2 "phenix/types/version/v2"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/notes"
)

// testBridge is the default bridge that ExperimentSpec.Init sets.
const testBridge = "phenix"

func TestVLANSpecValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    VLANSpec
		wantErr string
	}{
		{name: "no range", spec: VLANSpec{AliasesF: map[string]int{"a": 5000}}},
		{name: "inside range", spec: VLANSpec{AliasesF: map[string]int{"a": 150}, MinF: 100, MaxF: 200}},
		{name: "unassigned alias", spec: VLANSpec{AliasesF: map[string]int{"a": 0, "b": 150}, MinF: 100, MaxF: 200}},
		{
			name:    "below min",
			spec:    VLANSpec{AliasesF: map[string]int{"a": 50}, MinF: 100, MaxF: 200},
			wantErr: "less than experiment min VLAN ID of 100",
		},
		{
			name:    "above max",
			spec:    VLANSpec{AliasesF: map[string]int{"a": 250}, MinF: 100, MaxF: 200},
			wantErr: "greater than experiment max VLAN ID of 200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.spec.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() error = %v, want nil", err)
				}

				return
			}

			// The text is checked because B9 fixed a max check that said "min".
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestVLANSpecAccessors(t *testing.T) {
	t.Parallel()

	var v VLANSpec

	if got := v.Aliases(); got == nil || len(got) != 0 {
		t.Errorf("Aliases() of a zero VLANSpec = %v, want an empty map", got)
	}

	if err := v.Init(); err != nil || v.AliasesF == nil {
		t.Errorf("Init() = %v with aliases %v, want nil and an empty map", err, v.AliasesF)
	}

	v.SetAliases(map[string]int{"a": 1})
	v.SetMin(10)
	v.SetMax(20)

	if diff := cmp.Diff(VLANSpec{AliasesF: map[string]int{"a": 1}, MinF: 10, MaxF: 20}, v); diff != "" {
		t.Errorf("VLANSpec after setters (-want +got):\n%s", diff)
	}

	if v.Min() != 10 || v.Max() != 20 || v.Aliases()["a"] != 1 {
		t.Errorf("Min(), Max(), Aliases() = %d, %d, %v; want 10, 20, map[a:1]", v.Min(), v.Max(), v.Aliases())
	}
}

func TestExperimentSpecInitDefaults(t *testing.T) {
	t.Parallel()

	spec := &ExperimentSpec{
		ExperimentNameF: "exp",
		TopologyF: &TopologySpec{NodesF: []*Node{{
			GeneralF:  &General{HostnameF: "host"},
			HardwareF: &Hardware{},
			NetworkF:  &Network{InterfacesF: []*Interface{{NameF: "eth0", VLANF: "EXP"}, {NameF: "eth1", VLANF: "MGMT"}}},
		}, {
			GeneralF:  &General{HostnameF: "nonet"},
			HardwareF: &Hardware{},
		}}},
		VLANsF: &VLANSpec{AliasesF: map[string]int{"EXP": 101}},
	}

	if err := spec.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if got, want := spec.BaseDir(), common.PhenixBase+"/experiments/exp"; got != want {
		t.Errorf("BaseDir() = %q, want %q", got, want)
	}

	if got := spec.DefaultBridge(); got != testBridge {
		t.Errorf("DefaultBridge() = %q, want phenix", got)
	}

	if diff := cmp.Diff(map[string]int{"EXP": 101, "MGMT": 0}, spec.VLANs().Aliases()); diff != "" {
		t.Errorf("VLAN aliases after Init() (-want +got):\n%s", diff)
	}

	if got := spec.Schedules(); got == nil || len(got) != 0 {
		t.Errorf("Schedules() after Init() = %v, want an empty map", got)
	}

	if got := spec.TopologyF.NodesF[0].NetworkF.InterfacesF[0].BridgeF; got != testBridge {
		t.Errorf("interface bridge after Init() = %q, want phenix", got)
	}
}

func TestExperimentSpecInitPaths(t *testing.T) {
	t.Parallel()

	abs, err := filepath.Abs("relative/dir")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		spec       ExperimentSpec
		wantDir    string
		wantBridge string
	}{
		{name: "relative base dir", spec: ExperimentSpec{BaseDirF: "relative/dir"}, wantDir: abs, wantBridge: testBridge},
		{
			name:       "absolute base dir and bridge",
			spec:       ExperimentSpec{BaseDirF: "/abs", DefaultBridgeF: "br0"},
			wantDir:    "/abs",
			wantBridge: "br0",
		},
		{
			name:       "VLAN spec without aliases",
			spec:       ExperimentSpec{BaseDirF: "/abs", VLANsF: &VLANSpec{MinF: 1}},
			wantDir:    "/abs",
			wantBridge: testBridge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := tt.spec
			if err := spec.Init(); err != nil {
				t.Fatalf("Init() error = %v", err)
			}

			if spec.BaseDir() != tt.wantDir || spec.DefaultBridge() != tt.wantBridge {
				t.Errorf("Init() gives base dir %q and bridge %q, want %q and %q",
					spec.BaseDir(), spec.DefaultBridge(), tt.wantDir, tt.wantBridge)
			}

			if spec.VLANsF == nil || spec.VLANsF.AliasesF == nil {
				t.Errorf("Init() gives VLANs %+v, want a spec with an aliases map", spec.VLANsF)
			}
		})
	}
}

func TestExperimentSpecInitTopologyError(t *testing.T) {
	t.Parallel()

	spec := &ExperimentSpec{
		BaseDirF: "/abs",
		TopologyF: &TopologySpec{NodesF: []*Node{
			{GeneralF: &General{HostnameF: "dup"}, HardwareF: &Hardware{}},
			{GeneralF: &General{HostnameF: "dup"}, HardwareF: &Hardware{}},
		}},
	}

	if err := spec.Init(); err == nil {
		t.Error("Init() with duplicate hostnames error = nil, want an error")
	}
}

func TestExperimentSpecAccessors(t *testing.T) {
	t.Parallel()

	var spec ExperimentSpec

	if spec.Topology() == nil || spec.Scenario() == nil || spec.VLANs() == nil || spec.Schedules() == nil {
		t.Fatal("accessors of a zero ExperimentSpec return nil, want empty values")
	}

	topo := &TopologySpec{}
	scenario := &v2.ScenarioSpec{}

	spec.SetExperimentName("exp")
	spec.SetBaseDir("/base")
	spec.SetDefaultBridge("br")
	spec.SetDeployMode("all")
	spec.SetSchedule(map[string]string{"a": "h1"})
	spec.SetTopology(topo)
	spec.SetScenario(scenario)
	spec.SetUseGREMesh(true)

	if err := spec.ScheduleNode("b", "h2"); err != nil {
		t.Fatalf("ScheduleNode() error = %v", err)
	}

	want := ExperimentSpec{
		ExperimentNameF: "exp",
		BaseDirF:        "/base",
		DefaultBridgeF:  "br",
		DeployModeF:     "all",
		SchedulesF:      map[string]string{"a": "h1", "b": "h2"},
		TopologyF:       topo,
		ScenarioF:       scenario,
		UseGREMeshF:     true,
	}

	if diff := cmp.Diff(want, spec); diff != "" {
		t.Errorf("ExperimentSpec after setters (-want +got):\n%s", diff)
	}

	if spec.ExperimentName() != "exp" || spec.DeployMode() != "all" || !spec.UseGREMesh() {
		t.Errorf("ExperimentName(), DeployMode(), UseGREMesh() = %q, %q, %t", spec.ExperimentName(), spec.DeployMode(), spec.UseGREMesh())
	}

	if spec.Topology() != ifaces.TopologySpec(topo) || spec.Scenario() != ifaces.ScenarioSpec(scenario) {
		t.Error("Topology() or Scenario() does not return the spec that was set")
	}
}

// headnodeMM fakes the minimega head node name; other mm.MM methods panic.
type headnodeMM struct{ mm.MM }

func (headnodeMM) Headnode() string { return "head" }

func TestExperimentSpecSnapshotName(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM
	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = headnodeMM{} //nolint:reassign // install test double

	spec := ExperimentSpec{ExperimentNameF: "exp"}
	if got, want := spec.SnapshotName("node"), "head_exp_node_snapshot"; got != want {
		t.Errorf("SnapshotName(node) = %q, want %q", got, want)
	}
}

func TestExperimentSpecSetVLANAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		vlans   *VLANSpec
		alias   string
		id      int
		force   bool
		want    map[string]int
		wantErr string
	}{
		{name: "nil VLAN spec", alias: "a", id: 5, want: map[string]int{"a": 5}},
		{
			name:  "new alias in range",
			vlans: &VLANSpec{AliasesF: map[string]int{}, MinF: 1, MaxF: 10},
			alias: "a",
			id:    5,
			want:  map[string]int{"a": 5},
		},
		{name: "existing alias", vlans: &VLANSpec{AliasesF: map[string]int{"a": 1}}, alias: "a", id: 5, wantErr: "already exists"},
		{
			name:  "existing alias forced",
			vlans: &VLANSpec{AliasesF: map[string]int{"a": 1}},
			alias: "a",
			id:    5,
			force: true,
			want:  map[string]int{"a": 5},
		},
		{name: "below min", vlans: &VLANSpec{AliasesF: map[string]int{}, MinF: 10}, alias: "a", id: 5, wantErr: "less than experiment min"},
		{
			name:    "above max",
			vlans:   &VLANSpec{AliasesF: map[string]int{}, MaxF: 10},
			alias:   "a",
			id:      50,
			wantErr: "greater than experiment max",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := ExperimentSpec{ExperimentNameF: "exp", VLANsF: tt.vlans}

			err := spec.SetVLANAlias(tt.alias, tt.id, tt.force)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("SetVLANAlias(%q, %d, %t) error = %v, want one containing %q", tt.alias, tt.id, tt.force, err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("SetVLANAlias(%q, %d, %t) error = %v", tt.alias, tt.id, tt.force, err)
			}

			if diff := cmp.Diff(tt.want, spec.VLANsF.AliasesF); diff != "" {
				t.Errorf("aliases after SetVLANAlias (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExperimentSpecSetVLANRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		vlans    *VLANSpec
		min, max int
		force    bool
		wantErr  string
	}{
		{name: "nil VLAN spec", min: 100, max: 200},
		{name: "aliases inside", vlans: &VLANSpec{AliasesF: map[string]int{"a": 150}}, min: 100, max: 200},
		{name: "unassigned alias", vlans: &VLANSpec{AliasesF: map[string]int{"a": 0, "b": 150}}, min: 100, max: 200},
		{name: "existing range", vlans: &VLANSpec{MinF: 1, MaxF: 2}, min: 100, max: 200, wantErr: "already exists"},
		{name: "existing range forced", vlans: &VLANSpec{MinF: 1, MaxF: 2}, min: 100, max: 200, force: true},
		{
			name:    "alias below min",
			vlans:   &VLANSpec{AliasesF: map[string]int{"a": 50}},
			min:     100,
			max:     200,
			wantErr: "less than proposed experiment min",
		},
		{
			name:    "alias above max",
			vlans:   &VLANSpec{AliasesF: map[string]int{"a": 250}},
			min:     100,
			max:     200,
			wantErr: "greater than proposed experiment max",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := ExperimentSpec{ExperimentNameF: "exp", VLANsF: tt.vlans}

			err := spec.SetVLANRange(tt.min, tt.max, tt.force)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("SetVLANRange(%d, %d, %t) error = %v, want one containing %q", tt.min, tt.max, tt.force, err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("SetVLANRange(%d, %d, %t) error = %v", tt.min, tt.max, tt.force, err)
			}

			if spec.VLANsF.MinF != tt.min || spec.VLANsF.MaxF != tt.max {
				t.Errorf("range after SetVLANRange = %d-%d, want %d-%d", spec.VLANsF.MinF, spec.VLANsF.MaxF, tt.min, tt.max)
			}
		})
	}
}

// A VLAN alias set with SetVLANAlias passes Validate, and an ID that
// SetVLANAlias refuses is outside the range.
func TestExperimentSpecSetVLANAliasProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		minVal := rapid.IntRange(0, 100).Draw(t, "min")
		maxVal := rapid.IntRange(0, 200).Draw(t, "max")
		id := rapid.IntRange(1, 300).Draw(t, "id")

		spec := ExperimentSpec{VLANsF: &VLANSpec{AliasesF: map[string]int{}, MinF: minVal, MaxF: maxVal}}

		err := spec.SetVLANAlias("a", id, false)
		inRange := (minVal == 0 || id >= minVal) && (maxVal == 0 || id <= maxVal)

		if inRange != (err == nil) {
			t.Fatalf("SetVLANAlias(a, %d) with range %d-%d error = %v, want error %t", id, minVal, maxVal, err, !inRange)
		}

		if err == nil {
			if verr := spec.VLANsF.Validate(); verr != nil {
				t.Fatalf("Validate() after SetVLANAlias(a, %d) with range %d-%d error = %v", id, minVal, maxVal, verr)
			}
		}
	})
}

func TestExperimentSpecVerifyScenario(t *testing.T) {
	t.Parallel()

	spec := ExperimentSpec{
		TopologyF: &TopologySpec{NodesF: []*Node{{GeneralF: &General{HostnameF: "host"}}}},
		ScenarioF: &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{
			NameF:  "app",
			HostsF: []*v2.ScenarioAppHost{{HostnameF: "host"}, {HostnameF: "ghost"}},
		}}},
	}

	ctx := notes.Context(context.Background(), false)

	if err := spec.VerifyScenario(ctx); err != nil {
		t.Fatalf("VerifyScenario() error = %v", err)
	}

	warnings := notes.Warnings(ctx, true)

	got := make([]string, 0, len(warnings))
	for _, w := range warnings {
		got = append(got, w.Error())
	}

	if diff := cmp.Diff([]string{"host ghost in app app not in topology"}, got); diff != "" {
		t.Errorf("VerifyScenario() warnings (-want +got):\n%s", diff)
	}

	if err := (ExperimentSpec{}).VerifyScenario(ctx); err != nil {
		t.Errorf("VerifyScenario() without a scenario error = %v, want nil", err)
	}
}

func TestExperimentStatusAccessors(t *testing.T) {
	t.Parallel()

	var zero ExperimentStatus

	for name, n := range map[string]int{
		"AppStatus":     len(zero.AppStatus()),
		"AppFrequency":  len(zero.AppFrequency()),
		"AppRunning":    len(zero.AppRunning()),
		"AppCleanup":    len(zero.AppCleanup()),
		"VLANs":         len(zero.VLANs()),
		"Schedules":     len(zero.Schedules()),
		"DelayedTimers": len(zero.DelayedTimers()),
		"DelayedC2":     len(zero.DelayedC2()),
	} {
		if n != 0 {
			t.Errorf("%s() of a zero ExperimentStatus has %d entries, want 0", name, n)
		}
	}

	if zero.AppStatus() == nil || zero.DelayedC2() == nil || zero.Schedules() == nil {
		t.Error("map accessors of a zero ExperimentStatus return nil, want empty maps")
	}

	var s ExperimentStatus
	if err := s.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	s.SetStartTime("now")
	s.SetVLANs(map[string]int{"EXP": 101})
	s.SetSchedule(map[string]string{"n": "h"})
	s.SetAppFrequency("app", "5m")
	s.SetAppFrequency("gone", "1m")
	s.SetAppFrequency("gone", "")
	s.SetAppRunning("app", true)
	s.SetAppCleanup("app", true)
	s.SetAppCleanup("gone", true)
	s.SetAppCleanup("gone", false)
	s.SetInterrupted("restart")
	s.SetStarting(ifaces.StartMarker{PID: 7})
	s.SetDelayedStarts(map[string]string{"n": "5m"}, map[string]map[string]bool{"n": {"h": true}}, true)

	want := ExperimentStatus{
		StartTimeF:        "now",
		SchedulesF:        map[string]string{"n": "h"},
		AppsF:             map[string]any{},
		VLANsF:            map[string]int{"EXP": 101},
		FrequencyF:        map[string]string{"app": "5m"},
		RunningF:          map[string]bool{"app": true},
		CleanupF:          map[string]bool{"app": true},
		DelayedTimersF:    map[string]string{"n": "5m"},
		DelayedC2F:        map[string]map[string]bool{"n": {"h": true}},
		PostStartPendingF: true,
		InterruptedF:      "restart",
		StartingF:         ifaces.StartMarker{PID: 7},
	}

	if diff := cmp.Diff(want, s); diff != "" {
		t.Errorf("ExperimentStatus after setters (-want +got):\n%s", diff)
	}

	if s.StartTime() != "now" || s.Interrupted() != "restart" || !s.PostStartPending() || s.Starting().PID != 7 {
		t.Errorf("StartTime(), Interrupted(), PostStartPending(), Starting() = %q, %q, %t, %+v",
			s.StartTime(), s.Interrupted(), s.PostStartPending(), s.Starting())
	}

	if s.AppFrequency()["app"] != "5m" || !s.AppRunning()["app"] || !s.AppCleanup()["app"] ||
		s.VLANs()["EXP"] != 101 || s.Schedules()["n"] != "h" || s.DelayedTimers()["n"] != "5m" || !s.DelayedC2()["n"]["h"] {
		t.Error("map accessors do not return the values that were set")
	}

	s.SetDelayedStarts(map[string]string{}, map[string]map[string]bool{}, false)

	if s.DelayedTimersF != nil || s.DelayedC2F != nil || s.PostStartPendingF {
		t.Errorf("SetDelayedStarts(empty, empty, false) gives %v, %v, %t; want nil, nil, false",
			s.DelayedTimersF, s.DelayedC2F, s.PostStartPendingF)
	}

	var unset ExperimentStatus

	unset.SetVLANs(map[string]int{"a": 1})
	unset.SetSchedule(map[string]string{"a": "b"})
	unset.SetAppFrequency("a", "")
	unset.SetAppRunning("a", false)
	unset.SetAppCleanup("a", false)

	if unset.FrequencyF == nil || unset.RunningF == nil || unset.CleanupF == nil || len(unset.CleanupF) != 0 {
		t.Errorf("setters on a zero ExperimentStatus give %+v, want made maps", unset)
	}
}

type appStatus struct {
	Host  string
	Count int
}

func TestExperimentStatusAppStatus(t *testing.T) {
	t.Parallel()

	var s ExperimentStatus

	if err := s.ParseAppStatus("app", new(appStatus)); err == nil {
		t.Error("ParseAppStatus() with no status error = nil, want an error")
	}

	s.SetAppStatus("app", appStatus{Host: "h", Count: 2})
	s.SetAppStatus("raw", map[string]any{"host": "x"})
	s.SetAppStatus("gone", "x")
	s.SetAppStatus("gone", nil)

	wantApps := map[string]any{
		"app": map[string]any{"host": "h", "count": 2},
		"raw": map[string]any{"host": "x"},
	}

	if diff := cmp.Diff(wantApps, s.AppStatus()); diff != "" {
		t.Errorf("AppStatus() (-want +got):\n%s", diff)
	}

	var got appStatus
	if err := s.ParseAppStatus("app", &got); err != nil {
		t.Fatalf("ParseAppStatus(app) error = %v", err)
	}

	if diff := cmp.Diff(appStatus{Host: "h", Count: 2}, got); diff != "" {
		t.Errorf("ParseAppStatus(app) (-want +got):\n%s", diff)
	}

	if err := s.ParseAppStatus("missing", &got); err == nil {
		t.Error("ParseAppStatus(missing) error = nil, want an error")
	}

	if err := s.ParseAppStatus("app", new(int)); err == nil {
		t.Error("ParseAppStatus(app) into an int error = nil, want an error")
	}

	s.SetAppFrequency("app", "1m")
	s.SetAppRunning("app", true)
	s.SetAppCleanup("app", true)
	s.ResetAppStatus()

	want := ExperimentStatus{AppsF: map[string]any{}, CleanupF: map[string]bool{"app": true}}
	if diff := cmp.Diff(want, s, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("ExperimentStatus after ResetAppStatus() (-want +got):\n%s", diff)
	}
}
