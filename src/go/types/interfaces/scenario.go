package ifaces

type ScenarioSpec interface {
	Apps() []ScenarioApp
	App(string) ScenarioApp

	AddApp(string) ScenarioApp
}

type ScenarioApp interface {
	Name() string
	FromScenario() string
	AssetDir() string
	Metadata() map[string]any
	Hosts() []ScenarioAppHost
	RunPeriodically() string
	Disabled() bool

	SetAssetDir(string)
	SetMetadata(map[string]any)
	SetHosts([]ScenarioAppHost)
	AddHost(string) ScenarioAppHost
	SetRunPeriodically(string)
	SetDisabled(bool)

	ParseMetadata(any) error
	ParseHostMetadata(string, any) error
}

type ScenarioAppHost interface {
	Hostname() string
	Metadata() map[string]any

	SetMetadata(map[string]any)
	ParseMetadata(any) error
}
