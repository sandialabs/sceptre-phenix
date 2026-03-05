package v1

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"

	ifaces "phenix/types/interfaces"
	v2 "phenix/types/version/v2"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/notes"
)

type VLANSpec struct {
	AliasesF map[string]int `json:"aliases" mapstructure:"aliases" structs:"aliases" yaml:"aliases"`
	MinF     int            `json:"min"     mapstructure:"min"     structs:"min"     yaml:"min"`
	MaxF     int            `json:"max"     mapstructure:"max"     structs:"max"     yaml:"max"`
}

func (v *VLANSpec) Init() error {
	if v.AliasesF == nil {
		v.AliasesF = make(map[string]int)
	}

	return nil
}

func (v VLANSpec) Aliases() map[string]int {
	if v.AliasesF == nil {
		return make(map[string]int)
	}

	return v.AliasesF
}

func (v VLANSpec) Min() int {
	return v.MinF
}

func (v VLANSpec) Max() int {
	return v.MaxF
}

func (v *VLANSpec) SetAliases(a map[string]int) {
	v.AliasesF = a
}

func (v *VLANSpec) SetMin(m int) {
	v.MinF = m
}

func (v *VLANSpec) SetMax(m int) {
	v.MaxF = m
}

func (v VLANSpec) Validate() error {
	for k, val := range v.AliasesF {
		if v.MinF != 0 && val < v.MinF {
			return fmt.Errorf(
				"topology VLAN %s (VLAN ID %d) is less than experiment min VLAN ID of %d",
				k,
				val,
				v.MinF,
			)
		}

		if v.MaxF != 0 && val > v.MaxF {
			return fmt.Errorf(
				"topology VLAN %s (VLAN ID %d) is greater than experiment min VLAN ID of %d",
				k,
				val,
				v.MaxF,
			)
		}
	}

	return nil
}

type ExperimentSpec struct {
	ExperimentNameF string            `json:"experimentName,omitempty" mapstructure:"experimentName" structs:"experimentName" yaml:"experimentName,omitempty"`
	BaseDirF        string            `json:"baseDir"                  mapstructure:"baseDir"        structs:"baseDir"        yaml:"baseDir"`
	DefaultBridgeF  string            `json:"defaultBridge"            mapstructure:"defaultBridge"  structs:"defaultBridge"  yaml:"defaultBridge"`
	TopologyF       *TopologySpec     `json:"topology"                 mapstructure:"topology"       structs:"topology"       yaml:"topology"`
	ScenarioF       *v2.ScenarioSpec  `json:"scenario"                 mapstructure:"scenario"       structs:"scenario"       yaml:"scenario"`
	VLANsF          *VLANSpec         `json:"vlans"                    mapstructure:"vlans"          structs:"vlans"          yaml:"vlans"`
	SchedulesF      map[string]string `json:"schedules"                mapstructure:"schedules"      structs:"schedules"      yaml:"schedules"`
	DeployModeF     string            `json:"deployMode"               mapstructure:"deployMode"     structs:"deployMode"     yaml:"deployMode"`
	UseGREMeshF     bool              `json:"useGREMesh"               mapstructure:"useGREMesh"     structs:"useGREMesh"     yaml:"useGREMesh"`
}

func (e *ExperimentSpec) Init() error {
	if e.BaseDirF == "" {
		e.BaseDirF = common.PhenixBase + "/experiments/" + e.ExperimentNameF
	}

	if e.DefaultBridgeF == "" {
		e.DefaultBridgeF = "phenix"
	}

	if !filepath.IsAbs(e.BaseDirF) {
		if absPath, err := filepath.Abs(e.BaseDirF); err == nil {
			e.BaseDirF = absPath
		}
	}

	if e.VLANsF == nil {
		e.VLANsF = new(VLANSpec)
		_ = e.VLANsF.Init()
	}

	if e.VLANsF.AliasesF == nil {
		e.VLANsF.AliasesF = make(map[string]int)
	}

	if e.SchedulesF == nil {
		e.SchedulesF = make(map[string]string)
	}

	if e.TopologyF != nil {
		err := e.TopologyF.Init(e.DefaultBridgeF)
		if err != nil {
			return fmt.Errorf("initializing topology: %w", err)
		}

		for _, n := range e.TopologyF.NodesF {
			if n.NetworkF == nil {
				continue
			}

			for _, i := range n.NetworkF.InterfacesF {
				if _, ok := e.VLANsF.AliasesF[i.VLANF]; !ok {
					e.VLANsF.AliasesF[i.VLANF] = 0
				}
			}
		}
	}

	return nil
}

func (e ExperimentSpec) ExperimentName() string {
	return e.ExperimentNameF
}

func (e ExperimentSpec) BaseDir() string {
	return e.BaseDirF
}

func (e ExperimentSpec) DefaultBridge() string {
	return e.DefaultBridgeF
}

func (e ExperimentSpec) Topology() ifaces.TopologySpec { //nolint:ireturn // interface
	if e.TopologyF == nil {
		return new(TopologySpec)
	}

	return e.TopologyF
}

func (e ExperimentSpec) Scenario() ifaces.ScenarioSpec { //nolint:ireturn // interface
	if e.ScenarioF == nil {
		return new(v2.ScenarioSpec)
	}

	return e.ScenarioF
}

func (e ExperimentSpec) VLANs() ifaces.VLANSpec { //nolint:ireturn // interface
	if e.VLANsF == nil {
		return new(VLANSpec)
	}

	return e.VLANsF
}

func (e ExperimentSpec) Schedules() map[string]string {
	if e.SchedulesF == nil {
		return make(map[string]string)
	}

	return e.SchedulesF
}

func (e ExperimentSpec) DeployMode() string {
	return e.DeployModeF
}

func (e *ExperimentSpec) SetDeployMode(mode string) {
	e.DeployModeF = mode
}

func (e *ExperimentSpec) SetExperimentName(name string) {
	e.ExperimentNameF = name
}

func (e *ExperimentSpec) SetBaseDir(dir string) {
	e.BaseDirF = dir
}

func (e *ExperimentSpec) SetDefaultBridge(bridge string) {
	e.DefaultBridgeF = bridge
}

func (e ExperimentSpec) UseGREMesh() bool {
	return e.UseGREMeshF
}

func (e *ExperimentSpec) SetVLANAlias(a string, i int, f bool) error {
	if e.VLANsF == nil {
		e.VLANsF = &VLANSpec{AliasesF: make(map[string]int)} //nolint:exhaustruct // partial initialization
	}

	_, ok := e.VLANsF.AliasesF[a]
	if ok && !f {
		return fmt.Errorf("vlan alias %s already exists for experiment %s", a, e.ExperimentNameF)
	}

	if e.VLANsF.MinF != 0 && i < e.VLANsF.MinF {
		return fmt.Errorf("vlan ID %d is less than experiment min VLAN ID of %d", i, e.VLANsF.MinF)
	}

	if e.VLANsF.MaxF != 0 && i > e.VLANsF.MaxF {
		return fmt.Errorf(
			"vlan ID %d is greater than experiment max VLAN ID of %d",
			i,
			e.VLANsF.MaxF,
		)
	}

	e.VLANsF.AliasesF[a] = i

	return nil
}

func (e *ExperimentSpec) SetVLANRange(minVal, maxVal int, f bool) error {
	if e.VLANsF == nil {
		e.VLANsF = &VLANSpec{AliasesF: make(map[string]int)} //nolint:exhaustruct // partial initialization
	}

	if e.VLANsF.MinF != 0 && e.VLANsF.MaxF != 0 && !f {
		return fmt.Errorf(
			"vlan range %d-%d already exists for experiment %s",
			e.VLANsF.MinF,
			e.VLANsF.MaxF,
			e.ExperimentNameF,
		)
	}

	for k, v := range e.VLANsF.AliasesF {
		if minVal != 0 && v < minVal {
			return fmt.Errorf(
				"topology VLAN %s (VLAN ID %d) is less than proposed experiment min VLAN ID of %d",
				k,
				v,
				minVal,
			)
		}

		if maxVal != 0 && v > maxVal {
			return fmt.Errorf(
				"topology VLAN %s (VLAN ID %d) is greater than proposed experiment min VLAN ID of %d",
				k,
				v,
				maxVal,
			)
		}
	}

	e.VLANsF.MinF = minVal
	e.VLANsF.MaxF = maxVal

	return nil
}

func (e *ExperimentSpec) SetSchedule(s map[string]string) {
	e.SchedulesF = s
}

func (e *ExperimentSpec) SetTopology(topo ifaces.TopologySpec) {
	t, _ := topo.(*TopologySpec)
	e.TopologyF = t
}

func (e *ExperimentSpec) SetScenario(scenario ifaces.ScenarioSpec) {
	s, _ := scenario.(*v2.ScenarioSpec)
	e.ScenarioF = s
}

func (e *ExperimentSpec) SetUseGREMesh(g bool) {
	e.UseGREMeshF = g
}

func (e ExperimentSpec) VerifyScenario(ctx context.Context) error {
	if e.ScenarioF == nil {
		return nil
	}

	hosts := make(map[string]struct{})

	for _, node := range e.TopologyF.NodesF {
		hosts[node.GeneralF.HostnameF] = struct{}{}
	}

	for _, app := range e.ScenarioF.AppsF {
		for _, host := range app.HostsF {
			if _, ok := hosts[host.HostnameF]; !ok {
				notes.AddWarnings(
					ctx,
					false,
					fmt.Errorf("host %s in app %s not in topology", host.HostnameF, app.NameF),
				)
			}
		}
	}

	return nil
}

func (e *ExperimentSpec) ScheduleNode(node, host string) error {
	e.SchedulesF[node] = host

	return nil
}

func (e ExperimentSpec) SnapshotName(node string) string {
	return fmt.Sprintf("%s_%s_%s_snapshot", mm.Headnode(), e.ExperimentNameF, node)
}

type ExperimentStatus struct {
	StartTimeF string            `json:"startTime" mapstructure:"startTime" structs:"startTime" yaml:"startTime"`
	SchedulesF map[string]string `json:"schedules" mapstructure:"schedules" structs:"schedules" yaml:"schedules"`
	AppsF      map[string]any    `json:"apps"      mapstructure:"apps"      structs:"apps"      yaml:"apps"`
	VLANsF     map[string]int    `json:"vlans"     mapstructure:"vlans"     structs:"vlans"     yaml:"vlans"`

	// Used to track details of an app's running stage. Requires special attention
	// since it can be run periodically in the background and/or triggered
	// manually via the CLI or UI.
	FrequencyF map[string]string `json:"appRunningStageFrequency,omitempty" mapstructure:"appRunningStageFrequency" structs:"appRunningStageFrequency" yaml:"appRunningStageFrequency,omitempty"`
	RunningF   map[string]bool   `json:"appRunningStageStatus,omitempty"    mapstructure:"appRunningStageStatus"    structs:"appRunningStageStatus"    yaml:"appRunningStageStatus,omitempty"`
}

func (s *ExperimentStatus) Init() error {
	if s.SchedulesF == nil {
		s.SchedulesF = make(map[string]string)
	}

	if s.AppsF == nil {
		s.AppsF = make(map[string]any)
	}

	if s.VLANsF == nil {
		s.VLANsF = make(map[string]int)
	}

	return nil
}

func (s ExperimentStatus) StartTime() string {
	return s.StartTimeF
}

func (s ExperimentStatus) AppStatus() map[string]any {
	if s.AppsF == nil {
		return make(map[string]any)
	}

	return s.AppsF
}

func (s ExperimentStatus) AppFrequency() map[string]string {
	if s.FrequencyF == nil {
		return make(map[string]string)
	}

	return s.FrequencyF
}

func (s ExperimentStatus) AppRunning() map[string]bool {
	if s.RunningF == nil {
		return make(map[string]bool)
	}

	return s.RunningF
}

func (s ExperimentStatus) VLANs() map[string]int {
	if s.VLANsF == nil {
		return make(map[string]int)
	}

	return s.VLANsF
}

func (s ExperimentStatus) Schedules() map[string]string {
	if s.SchedulesF == nil {
		return make(map[string]string)
	}

	return s.SchedulesF
}

func (s *ExperimentStatus) SetStartTime(t string) {
	s.StartTimeF = t
}

func (s *ExperimentStatus) SetAppStatus(a string, status any) {
	if s.AppsF == nil {
		s.AppsF = make(map[string]any)
	}

	if status == nil {
		delete(s.AppsF, a)

		return
	}

	switch v := reflect.ValueOf(status); v.Kind() { //nolint:exhaustive // only struct needs special handling
	case reflect.Struct:
		s.AppsF[a] = structs.MapDefaultCase(status, structs.CASESNAKE)
	default:
		s.AppsF[a] = status
	}
}

func (s *ExperimentStatus) SetAppFrequency(a, f string) {
	if s.FrequencyF == nil {
		s.FrequencyF = make(map[string]string)
	}

	if f == "" {
		delete(s.FrequencyF, a)

		return
	}

	s.FrequencyF[a] = f
}

func (s *ExperimentStatus) SetAppRunning(a string, r bool) {
	if s.RunningF == nil {
		s.RunningF = make(map[string]bool)
	}

	s.RunningF[a] = r
}

func (s *ExperimentStatus) SetVLANs(v map[string]int) {
	if s.VLANsF == nil {
		s.VLANsF = make(map[string]int)
	}

	s.VLANsF = v
}

func (s *ExperimentStatus) SetSchedule(sched map[string]string) {
	if s.SchedulesF == nil {
		s.SchedulesF = make(map[string]string)
	}

	s.SchedulesF = sched
}

func (s ExperimentStatus) ParseAppStatus(name string, status any) error {
	if s.AppsF == nil {
		return fmt.Errorf("missing status for app %s", name)
	}

	app, ok := s.AppsF[name]
	if !ok {
		return fmt.Errorf("missing status for app %s", name)
	}

	err := mapstructure.Decode(app, status)
	if err != nil {
		return fmt.Errorf("decoding status for app %s: %w", name, err)
	}

	return nil
}

func (s *ExperimentStatus) ResetAppStatus() {
	s.AppsF = make(map[string]any)

	s.FrequencyF = nil
	s.RunningF = nil
}
