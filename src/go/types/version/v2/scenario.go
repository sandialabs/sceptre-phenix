package v2

import (
	"fmt"
	ifaces "phenix/types/interfaces"

	"github.com/mitchellh/mapstructure"
)

type ScenarioSpec struct {
	AppsF []*ScenarioApp `json:"apps" yaml:"apps" structs:"apps" mapstructure:"apps"`
}

func (this *ScenarioSpec) Apps() []ifaces.ScenarioApp {
	if this == nil {
		return nil
	}

	apps := make([]ifaces.ScenarioApp, len(this.AppsF))

	for i, a := range this.AppsF {
		apps[i] = a
	}

	return apps
}

func (this *ScenarioSpec) App(name string) ifaces.ScenarioApp {
	if this == nil {
		return nil
	}

	for _, a := range this.AppsF {
		if a.NameF == name {
			return a
		}
	}

	return nil
}

type ScenarioApp struct {
	NameF            string             `json:"name" yaml:"name" structs:"name" mapstructure:"name"`
	FromScenarioF    string             `json:"fromScenario,omitempty" yaml:"fromScenario,omitempty" structs:"fromScenario" mapstructure:"fromScenario"`
	AssetDirF        string             `json:"assetDir,omitempty" yaml:"assetDir,omitempty" structs:"assetDir" mapstructure:"assetDir"`
	MetadataF        map[string]any     `json:"metadata,omitempty" yaml:"metadata,omitempty" structs:"metadata" mapstructure:"metadata"`
	HostsF           []*ScenarioAppHost `json:"hosts,omitempty" yaml:"hosts,omitempty" structs:"hosts" mapstructure:"hosts"`
	RunPeriodicallyF string             `json:"runPeriodically,omitempty" yaml:"runPeriodically,omitempty" structs:"runPeriodically" mapstructure:"runPeriodically"`
	DisabledF        bool               `json:"disabled,omitempty" yaml:"disabled,omitempty" structs:"disabled" mapstructure:"disabled"`
}

func (this ScenarioApp) Name() string {
	return this.NameF
}

func (this ScenarioApp) FromScenario() string {
	return this.FromScenarioF
}

func (this ScenarioApp) AssetDir() string {
	return this.AssetDirF
}

func (this ScenarioApp) Metadata() map[string]interface{} {
	return this.MetadataF
}

func (this ScenarioApp) Hosts() []ifaces.ScenarioAppHost {
	hosts := make([]ifaces.ScenarioAppHost, len(this.HostsF))

	for i, h := range this.HostsF {
		hosts[i] = h
	}

	return hosts
}

func (this ScenarioApp) RunPeriodically() string {
	return this.RunPeriodicallyF
}

func (this ScenarioApp) Disabled() bool {
	return this.DisabledF
}

func (this *ScenarioApp) SetAssetDir(dir string) {
	this.AssetDirF = dir
}

func (this *ScenarioApp) SetMetadata(md map[string]interface{}) {
	this.MetadataF = md
}

func (this *ScenarioApp) SetHosts(hosts []ifaces.ScenarioAppHost) {
	h := make([]*ScenarioAppHost, len(hosts))

	for i, j := range hosts {
		h[i] = j.(*ScenarioAppHost)
	}

	this.HostsF = h
}

func (this *ScenarioApp) SetRunPeriodically(d string) {
	this.RunPeriodicallyF = d
}

func (this *ScenarioApp) SetDisabled(d bool) {
	this.DisabledF = d
}

func (this ScenarioApp) ParseMetadata(md any) error {
	if this.MetadataF == nil {
		return fmt.Errorf("missing metadata for app %s", this.NameF)
	}

	if err := mapstructure.Decode(this.MetadataF, md); err != nil {
		return fmt.Errorf("decoding metadata for app %s: %w", this.NameF, err)
	}

	return nil
}

func (this ScenarioApp) ParseHostMetadata(name string, md any) error {
	if len(this.HostsF) == 0 {
		return fmt.Errorf("missing host %s for app %s", name, this.NameF)
	}

	for _, host := range this.HostsF {
		if host.HostnameF == name {
			return host.ParseMetadata(md)
		}
	}

	return fmt.Errorf("missing host %s for app %s", name, this.NameF)
}

type ScenarioAppHost struct {
	HostnameF string         `json:"hostname" yaml:"hostname" structs:"hostname" mapstructure:"hostname"`
	MetadataF map[string]any `json:"metadata" yaml:"metadata" structs:"metadata" mapstructure:"metadata"`
}

func (this ScenarioAppHost) Hostname() string {
	return this.HostnameF
}

func (this ScenarioAppHost) Metadata() map[string]any {
	return this.MetadataF
}

func (this ScenarioAppHost) ParseMetadata(md any) error {
	if this.MetadataF == nil {
		return fmt.Errorf("missing metadata for host %s", this.HostnameF)
	}

	if err := mapstructure.Decode(this.MetadataF, md); err != nil {
		return fmt.Errorf("decoding metadata for host %s: %w", this.HostnameF, err)
	}

	return nil
}
