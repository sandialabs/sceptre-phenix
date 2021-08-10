package v1

import (
	"context"
	"fmt"
	"path/filepath"

	"phenix/internal/common"
	"phenix/internal/mm"
	ifaces "phenix/types/interfaces"
	v2 "phenix/types/version/v2"
	"phenix/util/notes"
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
	TopologyF       *TopologySpec     `json:"topology" yaml:"topology" structs:"topology" mapstructure:"topology"`
	ScenarioF       *v2.ScenarioSpec  `json:"scenario" yaml:"scenario" structs:"scenario" mapstructure:"scenario"`
	VLANsF          *VLANSpec         `json:"vlans" yaml:"vlans" structs:"vlans" mapstructure:"vlans"`
	SchedulesF      map[string]string `json:"schedules" yaml:"schedules" structs:"schedules" mapstructure:"schedules"`
	RunLocalF       bool              `json:"runLocal" yaml:"runLocal" structs:"runLocal" mapstructure:"runLocal"`
}

func (this *ExperimentSpec) Init() error {
	if this.BaseDirF == "" {
		this.BaseDirF = common.PhenixBase + "/experiments/" + this.ExperimentNameF
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
		this.TopologyF.SetDefaults()

		for _, n := range this.TopologyF.NodesF {
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

func (this ExperimentSpec) RunLocal() bool {
	return this.RunLocalF
}

func (this *ExperimentSpec) SetExperimentName(name string) {
	this.ExperimentNameF = name
}

func (this *ExperimentSpec) SetBaseDir(dir string) {
	this.BaseDirF = dir
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
				notes.AddWarnings(ctx, fmt.Errorf("host %s in app %s not in topology", host.HostnameF, app.NameF))
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
	StartTimeF string                 `json:"startTime" yaml:"startTime" structs:"startTime" mapstructure:"startTime"`
	SchedulesF map[string]string      `json:"schedules" yaml:"schedules" structs:"schedules" mapstructure:"schedules"`
	AppsF      map[string]interface{} `json:"apps" yaml:"apps" structs:"apps" mapstructure:"apps"`
	VLANsF     map[string]int         `json:"vlans" yaml:"vlans" structs:"vlans" mapstructure:"vlans"`

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
		this.AppsF = make(map[string]interface{})
	}

	if this.VLANsF == nil {
		this.VLANsF = make(map[string]int)
	}

	return nil
}

func (this ExperimentStatus) StartTime() string {
	return this.StartTimeF
}

func (this ExperimentStatus) AppStatus() map[string]interface{} {
	return this.AppsF
}

func (this ExperimentStatus) AppFrequency() map[string]string {
	return this.FrequencyF
}

func (this ExperimentStatus) AppRunning() map[string]bool {
	return this.RunningF
}

func (this ExperimentStatus) VLANs() map[string]int {
	return this.VLANsF
}

func (this ExperimentStatus) Schedules() map[string]string {
	return this.SchedulesF
}

func (this *ExperimentStatus) SetStartTime(t string) {
	this.StartTimeF = t
}

func (this *ExperimentStatus) SetAppStatus(a string, s interface{}) {
	if this.AppsF == nil {
		this.AppsF = make(map[string]interface{})
	}

	if s == nil {
		delete(this.AppsF, a)
		return
	}

	this.AppsF[a] = s
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
	this.VLANsF = v
}

func (this *ExperimentStatus) SetSchedule(s map[string]string) {
	this.SchedulesF = s
}

func (this *ExperimentStatus) ResetAppStatus() {
	this.AppsF = make(map[string]interface{})

	this.FrequencyF = nil
	this.RunningF = nil
}
