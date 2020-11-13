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

	SetAssetDir(string)
	SetMetadata(map[string]interface{})
	SetHosts([]ScenarioAppHost)
}

type ScenarioAppHost interface {
	Hostname() string
	Metadata() map[string]interface{}
}
