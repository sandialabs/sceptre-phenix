package ifaces

type ScenarioSpec interface {
	Apps() []ScenarioApp
}

type ScenarioApp interface {
	Name() string
	FromScenario() string
	AssetDir() string
	Metadata() map[string]interface{}
	Hosts() []ScenarioAppHost
	RunPeriodically() string

	SetAssetDir(string)
	SetMetadata(map[string]interface{})
	SetHosts([]ScenarioAppHost)
	SetRunPeriodically(string)
}

type ScenarioAppHost interface {
	Hostname() string
	Metadata() map[string]interface{}
}
