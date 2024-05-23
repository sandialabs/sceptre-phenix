package ifaces

import "context"

type VLANSpec interface {
	Init() error

	Aliases() map[string]int
	Min() int
	Max() int

	SetAliases(map[string]int)
	SetMin(int)
	SetMax(int)
}

type ExperimentSpec interface {
	Init() error

	ExperimentName() string
	BaseDir() string
	DefaultBridge() string
	Topology() TopologySpec
	Scenario() ScenarioSpec
	VLANs() VLANSpec
	Schedules() map[string]string
	DeployMode() string
	UseGREMesh() bool

	SetExperimentName(string)
	SetBaseDir(string)
	SetDefaultBridge(string)
	SetVLANAlias(string, int, bool) error
	SetVLANRange(int, int, bool) error
	SetSchedule(map[string]string)
	SetTopology(TopologySpec)
	SetScenario(ScenarioSpec)
	SetDeployMode(string)
	SetUseGREMesh(bool)

	VerifyScenario(context.Context) error
	ScheduleNode(string, string) error
}

type ExperimentStatus interface {
	Init() error

	StartTime() string
	AppStatus() map[string]any
	AppFrequency() map[string]string
	AppRunning() map[string]bool
	VLANs() map[string]int
	Schedules() map[string]string

	SetStartTime(string)
	SetAppStatus(string, any)
	SetAppFrequency(string, string)
	SetAppRunning(string, bool)
	SetVLANs(map[string]int)
	SetSchedule(map[string]string)

	ParseAppStatus(string, any) error
	ResetAppStatus()
}
