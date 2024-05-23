package v1

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"

	ifaces "phenix/types/interfaces"
	v2 "phenix/types/version/v2"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/notes"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"
)

type VLANSpec struct {
	AliasesF map[string]int `json:"aliases" yaml:"aliases" structs:"aliases" mapstructure:"aliases"`
	MinF     int            `json:"min" yaml:"min" structs:"min" mapstructure:"min"`
	MaxF     int            `json:"max" yaml:"max" structs:"max" mapstructure:"max"`
}

func (this *VLANSpec) Init() error {
	if this.AliasesF == nil {
		this.AliasesF = make(map[string]int)
	}

	return nil
}

func (this VLANSpec) Aliases() map[string]int {
	if this.AliasesF == nil {
		return make(map[string]int)
	}

	return this.AliasesF
}

func (this VLANSpec) Min() int {
	return this.MinF
}

func (this VLANSpec) Max() int {
	return this.MaxF
}

func (this *VLANSpec) SetAliases(a map[string]int) {
	this.AliasesF = a
}

func (this *VLANSpec) SetMin(m int) {
	this.MinF = m
}

func (this *VLANSpec) SetMax(m int) {
	this.MaxF = m
}

func (this VLANSpec) Validate() error {
	for k, v := range this.AliasesF {
		if this.MinF != 0 && v < this.MinF {
			return fmt.Errorf("topology VLAN %s (VLAN ID %d) is less than experiment min VLAN ID of %d", k, v, this.MinF)
		}

		if this.MaxF != 0 && v > this.MaxF {
			return fmt.Errorf("topology VLAN %s (VLAN ID %d) is greater than experiment min VLAN ID of %d", k, v, this.MaxF)
		}
	}

	return nil
}

type ExperimentSpec struct {
	ExperimentNameF string            `json:"experimentName,omitempty" yaml:"experimentName,omitempty" structs:"experimentName" mapstructure:"experimentName"`
	BaseDirF        string            `json:"baseDir" yaml:"baseDir" structs:"baseDir" mapstructure:"baseDir"`
	DefaultBridgeF  string            `json:"defaultBridge" yaml:"defaultBridge" structs:"defaultBridge" mapstructure:"defaultBridge"`
	TopologyF       *TopologySpec     `json:"topology" yaml:"topology" structs:"topology" mapstructure:"topology"`
	ScenarioF       *v2.ScenarioSpec  `json:"scenario" yaml:"scenario" structs:"scenario" mapstructure:"scenario"`
	VLANsF          *VLANSpec         `json:"vlans" yaml:"vlans" structs:"vlans" mapstructure:"vlans"`
	SchedulesF      map[string]string `json:"schedules" yaml:"schedules" structs:"schedules" mapstructure:"schedules"`
	DeployModeF     string            `json:"deployMode" yaml:"deployMode" structs:"deployMode" mapstructure:"deployMode"`
	UseGREMeshF     bool              `json:"useGREMesh" yaml:"useGREMesh" structs:"useGREMesh" mapstructure:"useGREMesh"`
}

func (this *ExperimentSpec) Init() error {
	if this.BaseDirF == "" {
		this.BaseDirF = common.PhenixBase + "/experiments/" + this.ExperimentNameF
	}

	if this.DefaultBridgeF == "" {
		this.DefaultBridgeF = "phenix"
	}

	if !filepath.IsAbs(this.BaseDirF) {
		if absPath, err := filepath.Abs(this.BaseDirF); err == nil {
			this.BaseDirF = absPath
		}
	}

	if this.VLANsF == nil {
		this.VLANsF = new(VLANSpec)
		this.VLANsF.Init()
	}

	if this.VLANsF.AliasesF == nil {
		this.VLANsF.AliasesF = make(map[string]int)
	}

	if this.SchedulesF == nil {
		this.SchedulesF = make(map[string]string)
	}

	if this.TopologyF != nil {
		if err := this.TopologyF.Init(this.DefaultBridgeF); err != nil {
			return fmt.Errorf("initializing topology: %w", err)
		}

		for _, n := range this.TopologyF.NodesF {
			if n.NetworkF == nil {
				continue
			}

			for _, i := range n.NetworkF.InterfacesF {
				if _, ok := this.VLANsF.AliasesF[i.VLANF]; !ok {
					this.VLANsF.AliasesF[i.VLANF] = 0
				}
			}
		}
	}

	return nil
}

func (this ExperimentSpec) ExperimentName() string {
	return this.ExperimentNameF
}

func (this ExperimentSpec) BaseDir() string {
	return this.BaseDirF
}

func (this ExperimentSpec) DefaultBridge() string {
	return this.DefaultBridgeF
}

func (this ExperimentSpec) Topology() ifaces.TopologySpec {
	if this.TopologyF == nil {
		return new(TopologySpec)
	}

	return this.TopologyF
}

func (this ExperimentSpec) Scenario() ifaces.ScenarioSpec {
	if this.ScenarioF == nil {
		return new(v2.ScenarioSpec)
	}

	return this.ScenarioF
}

func (this ExperimentSpec) VLANs() ifaces.VLANSpec {
	if this.VLANsF == nil {
		return new(VLANSpec)
	}

	return this.VLANsF
}

func (this ExperimentSpec) Schedules() map[string]string {
	if this.SchedulesF == nil {
		return make(map[string]string)
	}

	return this.SchedulesF
}

func (this ExperimentSpec) DeployMode() string {
	return this.DeployModeF
}

func (this *ExperimentSpec) SetDeployMode(mode string) {
	this.DeployModeF = mode
}

func (this *ExperimentSpec) SetExperimentName(name string) {
	this.ExperimentNameF = name
}

func (this *ExperimentSpec) SetBaseDir(dir string) {
	this.BaseDirF = dir
}

func (this *ExperimentSpec) SetDefaultBridge(bridge string) {
	this.DefaultBridgeF = bridge
}

func (this ExperimentSpec) UseGREMesh() bool {
	return this.UseGREMeshF
}

func (this *ExperimentSpec) SetVLANAlias(a string, i int, f bool) error {
	if this.VLANsF == nil {
		this.VLANsF = &VLANSpec{AliasesF: make(map[string]int)}
	}

	_, ok := this.VLANsF.AliasesF[a]
	if ok && !f {
		return fmt.Errorf("VLAN alias %s already exists for experiment %s", a, this.ExperimentNameF)
	}

	if this.VLANsF.MinF != 0 && i < this.VLANsF.MinF {
		return fmt.Errorf("VLAN ID %d is less than experiment min VLAN ID of %d", i, this.VLANsF.MinF)
	}

	if this.VLANsF.MaxF != 0 && i > this.VLANsF.MaxF {
		return fmt.Errorf("VLAN ID %d is greater than experiment max VLAN ID of %d", i, this.VLANsF.MaxF)
	}

	this.VLANsF.AliasesF[a] = i

	return nil
}

func (this *ExperimentSpec) SetVLANRange(min, max int, f bool) error {
	if this.VLANsF == nil {
		this.VLANsF = &VLANSpec{AliasesF: make(map[string]int)}
	}

	if this.VLANsF.MinF != 0 && this.VLANsF.MaxF != 0 && !f {
		return fmt.Errorf("VLAN range %d-%d already exists for experiment %s", this.VLANsF.MinF, this.VLANsF.MaxF, this.ExperimentNameF)
	}

	for k, v := range this.VLANsF.AliasesF {
		if min != 0 && v < min {
			return fmt.Errorf("topology VLAN %s (VLAN ID %d) is less than proposed experiment min VLAN ID of %d", k, v, min)
		}

		if max != 0 && v > max {
			return fmt.Errorf("topology VLAN %s (VLAN ID %d) is greater than proposed experiment min VLAN ID of %d", k, v, max)
		}
	}

	this.VLANsF.MinF = min
	this.VLANsF.MaxF = max

	return nil
}

func (this *ExperimentSpec) SetSchedule(s map[string]string) {
	this.SchedulesF = s
}

func (this *ExperimentSpec) SetTopology(topo ifaces.TopologySpec) {
	this.TopologyF = topo.(*TopologySpec)
}

func (this *ExperimentSpec) SetScenario(scenario ifaces.ScenarioSpec) {
	this.ScenarioF = scenario.(*v2.ScenarioSpec)
}

func (this *ExperimentSpec) SetUseGREMesh(g bool) {
	this.UseGREMeshF = g
}

func (this ExperimentSpec) VerifyScenario(ctx context.Context) error {
	if this.ScenarioF == nil {
		return nil
	}

	hosts := make(map[string]struct{})

	for _, node := range this.TopologyF.NodesF {
		hosts[node.GeneralF.HostnameF] = struct{}{}
	}

	for _, app := range this.ScenarioF.AppsF {
		for _, host := range app.HostsF {
			if _, ok := hosts[host.HostnameF]; !ok {
				notes.AddWarnings(ctx, false, fmt.Errorf("host %s in app %s not in topology", host.HostnameF, app.NameF))
			}
		}
	}

	return nil
}

func (this *ExperimentSpec) ScheduleNode(node, host string) error {
	this.SchedulesF[node] = host
	return nil
}

func (this ExperimentSpec) SnapshotName(node string) string {
	return fmt.Sprintf("%s_%s_%s_snapshot", mm.Headnode(), this.ExperimentNameF, node)
}

type ExperimentStatus struct {
	StartTimeF string            `json:"startTime" yaml:"startTime" structs:"startTime" mapstructure:"startTime"`
	SchedulesF map[string]string `json:"schedules" yaml:"schedules" structs:"schedules" mapstructure:"schedules"`
	AppsF      map[string]any    `json:"apps" yaml:"apps" structs:"apps" mapstructure:"apps"`
	VLANsF     map[string]int    `json:"vlans" yaml:"vlans" structs:"vlans" mapstructure:"vlans"`

	// Used to track details of an app's running stage. Requires special attention
	// since it can be run periodically in the background and/or triggered
	// manually via the CLI or UI.
	FrequencyF map[string]string `json:"appRunningStageFrequency,omitempty" yaml:"appRunningStageFrequency,omitempty" structs:"appRunningStageFrequency" mapstructure:"appRunningStageFrequency"`
	RunningF   map[string]bool   `json:"appRunningStageStatus,omitempty" yaml:"appRunningStageStatus,omitempty" structs:"appRunningStageStatus" mapstructure:"appRunningStageStatus"`
}

func (this *ExperimentStatus) Init() error {
	if this.SchedulesF == nil {
		this.SchedulesF = make(map[string]string)
	}

	if this.AppsF == nil {
		this.AppsF = make(map[string]any)
	}

	if this.VLANsF == nil {
		this.VLANsF = make(map[string]int)
	}

	return nil
}

func (this ExperimentStatus) StartTime() string {
	return this.StartTimeF
}

func (this ExperimentStatus) AppStatus() map[string]any {
	if this.AppsF == nil {
		return make(map[string]any)
	}

	return this.AppsF
}

func (this ExperimentStatus) AppFrequency() map[string]string {
	if this.FrequencyF == nil {
		return make(map[string]string)
	}

	return this.FrequencyF
}

func (this ExperimentStatus) AppRunning() map[string]bool {
	if this.RunningF == nil {
		return make(map[string]bool)
	}

	return this.RunningF
}

func (this ExperimentStatus) VLANs() map[string]int {
	if this.VLANsF == nil {
		return make(map[string]int)
	}

	return this.VLANsF
}

func (this ExperimentStatus) Schedules() map[string]string {
	if this.SchedulesF == nil {
		return make(map[string]string)
	}

	return this.SchedulesF
}

func (this *ExperimentStatus) SetStartTime(t string) {
	this.StartTimeF = t
}

func (this *ExperimentStatus) SetAppStatus(a string, s any) {
	if this.AppsF == nil {
		this.AppsF = make(map[string]any)
	}

	if s == nil {
		delete(this.AppsF, a)
		return
	}

	switch v := reflect.ValueOf(s); v.Kind() {
	case reflect.Struct:
		this.AppsF[a] = structs.MapDefaultCase(s, structs.CASESNAKE)
	default:
		this.AppsF[a] = s
	}
}

func (this *ExperimentStatus) SetAppFrequency(a, f string) {
	if this.FrequencyF == nil {
		this.FrequencyF = make(map[string]string)
	}

	if f == "" {
		delete(this.FrequencyF, a)
		return
	}

	this.FrequencyF[a] = f
}

func (this *ExperimentStatus) SetAppRunning(a string, r bool) {
	if this.RunningF == nil {
		this.RunningF = make(map[string]bool)
	}

	this.RunningF[a] = r
}

func (this *ExperimentStatus) SetVLANs(v map[string]int) {
	if this.VLANsF == nil {
		this.VLANsF = make(map[string]int)
	}

	this.VLANsF = v
}

func (this *ExperimentStatus) SetSchedule(s map[string]string) {
	if this.SchedulesF == nil {
		this.SchedulesF = make(map[string]string)
	}

	this.SchedulesF = s
}

func (this ExperimentStatus) ParseAppStatus(name string, status any) error {
	if this.AppsF == nil {
		return fmt.Errorf("missing status for app %s", name)
	}

	app, ok := this.AppsF[name]
	if !ok {
		return fmt.Errorf("missing status for app %s", name)
	}

	if err := mapstructure.Decode(app, status); err != nil {
		return fmt.Errorf("decoding status for app %s: %w", name, err)
	}

	return nil
}

func (this *ExperimentStatus) ResetAppStatus() {
	this.AppsF = make(map[string]any)

	this.FrequencyF = nil
	this.RunningF = nil
}
