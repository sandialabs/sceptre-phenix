package v2

import (
	"fmt"

	"github.com/mitchellh/mapstructure"

	ifaces "phenix/types/interfaces"
)

type ScenarioSpec struct {
	AppsF []*ScenarioApp `json:"apps" mapstructure:"apps" structs:"apps" yaml:"apps"`
}

func (ss *ScenarioSpec) Apps() []ifaces.ScenarioApp {
	if ss == nil {
		return nil
	}

	apps := make([]ifaces.ScenarioApp, len(ss.AppsF))

	for i, a := range ss.AppsF {
		apps[i] = a
	}

	return apps
}

func (ss *ScenarioSpec) App(name string) ifaces.ScenarioApp { //nolint:ireturn // returns interface
	if ss == nil {
		return nil
	}

	for _, a := range ss.AppsF {
		if a.NameF == name {
			return a
		}
	}

	return nil
}

func (ss *ScenarioSpec) AddApp(name string) ifaces.ScenarioApp { //nolint:ireturn // returns interface
	a := &ScenarioApp{ //nolint:exhaustruct // partial initialization
		NameF: name,
	}

	ss.AppsF = append(ss.AppsF, a)

	return a
}

type ScenarioApp struct {
	NameF            string             `json:"name"                      mapstructure:"name"            structs:"name"            yaml:"name"`
	FromScenarioF    string             `json:"fromScenario,omitempty"    mapstructure:"fromScenario"    structs:"fromScenario"    yaml:"fromScenario,omitempty"`
	AssetDirF        string             `json:"assetDir,omitempty"        mapstructure:"assetDir"        structs:"assetDir"        yaml:"assetDir,omitempty"`
	MetadataF        map[string]any     `json:"metadata,omitempty"        mapstructure:"metadata"        structs:"metadata"        yaml:"metadata,omitempty"`
	HostsF           []*ScenarioAppHost `json:"hosts,omitempty"           mapstructure:"hosts"           structs:"hosts"           yaml:"hosts,omitempty"`
	RunPeriodicallyF string             `json:"runPeriodically,omitempty" mapstructure:"runPeriodically" structs:"runPeriodically" yaml:"runPeriodically,omitempty"`
	DisabledF        bool               `json:"disabled,omitempty"        mapstructure:"disabled"        structs:"disabled"        yaml:"disabled,omitempty"`
}

func (sa ScenarioApp) Name() string {
	return sa.NameF
}

func (sa ScenarioApp) FromScenario() string {
	return sa.FromScenarioF
}

func (sa ScenarioApp) AssetDir() string {
	return sa.AssetDirF
}

func (sa ScenarioApp) Metadata() map[string]any {
	return sa.MetadataF
}

func (sa ScenarioApp) Hosts() []ifaces.ScenarioAppHost {
	hosts := make([]ifaces.ScenarioAppHost, len(sa.HostsF))

	for i, h := range sa.HostsF {
		hosts[i] = h
	}

	return hosts
}

func (sa ScenarioApp) RunPeriodically() string {
	return sa.RunPeriodicallyF
}

func (sa ScenarioApp) Disabled() bool {
	return sa.DisabledF
}

func (sa *ScenarioApp) SetAssetDir(dir string) {
	sa.AssetDirF = dir
}

func (sa *ScenarioApp) SetMetadata(md map[string]any) {
	sa.MetadataF = md
}

func (sa *ScenarioApp) SetHosts(hosts []ifaces.ScenarioAppHost) {
	h := make([]*ScenarioAppHost, len(hosts))

	for i, j := range hosts {
		host, _ := j.(*ScenarioAppHost)
		h[i] = host
	}

	sa.HostsF = h
}

func (sa *ScenarioApp) AddHost(hostname string) ifaces.ScenarioAppHost { //nolint:ireturn // returns interface
	h := &ScenarioAppHost{ //nolint:exhaustruct // partial initialization
		HostnameF: hostname,
	}

	sa.HostsF = append(sa.HostsF, h)

	return h
}

func (sa *ScenarioApp) SetRunPeriodically(d string) {
	sa.RunPeriodicallyF = d
}

func (sa *ScenarioApp) SetDisabled(d bool) {
	sa.DisabledF = d
}

func (sa ScenarioApp) ParseMetadata(md any) error {
	if sa.MetadataF == nil {
		return fmt.Errorf("missing metadata for app %s", sa.NameF)
	}

	err := mapstructure.Decode(sa.MetadataF, md)
	if err != nil {
		return fmt.Errorf("decoding metadata for app %s: %w", sa.NameF, err)
	}

	return nil
}

func (sa ScenarioApp) ParseHostMetadata(name string, md any) error {
	if len(sa.HostsF) == 0 {
		return fmt.Errorf("missing host %s for app %s", name, sa.NameF)
	}

	for _, host := range sa.HostsF {
		if host.HostnameF == name {
			return host.ParseMetadata(md)
		}
	}

	return fmt.Errorf("missing host %s for app %s", name, sa.NameF)
}

type ScenarioAppHost struct {
	HostnameF string         `json:"hostname" mapstructure:"hostname" structs:"hostname" yaml:"hostname"`
	MetadataF map[string]any `json:"metadata" mapstructure:"metadata" structs:"metadata" yaml:"metadata"`
}

func (sah ScenarioAppHost) Hostname() string {
	return sah.HostnameF
}

func (sah ScenarioAppHost) Metadata() map[string]any {
	return sah.MetadataF
}

func (sah *ScenarioAppHost) SetMetadata(md map[string]any) {
	sah.MetadataF = md
}

func (sah ScenarioAppHost) ParseMetadata(md any) error {
	if sah.MetadataF == nil {
		return fmt.Errorf("missing metadata for host %s", sah.HostnameF)
	}

	err := mapstructure.Decode(sah.MetadataF, md)
	if err != nil {
		return fmt.Errorf("decoding metadata for host %s: %w", sah.HostnameF, err)
	}

	return nil
}
