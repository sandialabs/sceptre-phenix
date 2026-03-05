package v1

type ScenarioSpec struct {
	AppsF *Apps `json:"apps" mapstructure:"apps" structs:"apps" yaml:"apps"`
}

type Apps struct {
	ExperimentF []ExperimentApp `json:"experiment" mapstructure:"experiment" structs:"experiment" yaml:"experiment"`
	HostF       []HostApp       `json:"host"       mapstructure:"host"       structs:"host"       yaml:"host"`
}

type ExperimentApp struct {
	NameF     string         `json:"name"     mapstructure:"name"     structs:"name"     yaml:"name"`
	AssetDirF string         `json:"assetDir" mapstructure:"assetDir" structs:"assetDir" yaml:"assetDir"`
	MetadataF map[string]any `json:"metadata" mapstructure:"metadata" structs:"metadata" yaml:"metadata"`
}

type HostApp struct {
	NameF     string `json:"name"     mapstructure:"name"     structs:"name"     yaml:"name"`
	AssetDirF string `json:"assetDir" mapstructure:"assetDir" structs:"assetDir" yaml:"assetDir"`
	HostsF    []Host `json:"hosts"    mapstructure:"hosts"    structs:"hosts"    yaml:"hosts"`
}

type Host struct {
	HostnameF string         `json:"hostname" mapstructure:"hostname" structs:"hostname" yaml:"hostname"`
	MetadataF map[string]any `json:"metadata" mapstructure:"metadata" structs:"metadata" yaml:"metadata"`
}
