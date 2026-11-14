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

type ExperimentSpec interface { //nolint:interfacebloat // legacy interface
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

type ExperimentStatus interface { //nolint:interfacebloat // legacy interface
	Init() error

	StartTime() string
	AppStatus() map[string]any
	AppFrequency() map[string]string
	AppRunning() map[string]bool
	AppCleanup() map[string]bool
	VLANs() map[string]int
	Schedules() map[string]string

	SetStartTime(string)
	SetAppStatus(string, any)
	SetAppFrequency(string, string)
	SetAppRunning(string, bool)
	SetAppCleanup(string, bool)
	SetVLANs(map[string]int)
	SetSchedule(map[string]string)

	ParseAppStatus(string, any) error
	ResetAppStatus()

	// Pending starts: each timer-delayed VM's due time (RFC 3339), each C2-delayed VM's awaited VMs (true to
	// match the C2 client by UUID), whether post-start apps must run, and why phenix could not resume them.
	DelayedTimers() map[string]string
	DelayedC2() map[string]map[string]bool
	PostStartPending() bool
	Interrupted() string

	SetDelayedStarts(map[string]string, map[string]map[string]bool, bool)
	SetInterrupted(string)

	// The start in progress, and the process that runs it; the zero marker means no start is in progress.
	Starting() StartMarker
	SetStarting(StartMarker)

	// The stop in progress, and the process that runs it; the zero marker means no stop is in progress.
	Stopping() StopMarker
	SetStopping(StopMarker)
}

// StartMarker records a start in progress: when it began (RFC 3339, UTC), the process ID and host name of the
// process that runs it, and how far the start is. The zero value records no start.
type StartMarker struct {
	Since    string            `json:"since"             mapstructure:"since"    structs:"since"              yaml:"since"`
	PID      int               `json:"pid"               mapstructure:"pid"      structs:"pid"                yaml:"pid"`
	Host     string            `json:"host"              mapstructure:"host"     structs:"host"               yaml:"host"`
	Progress OperationProgress `json:"progress,omitzero" mapstructure:"progress" structs:"progress,omitempty" yaml:"progress,omitempty"`
}

// IsZero reports whether m records no start.
func (m StartMarker) IsZero() bool {
	return m.Since == "" && m.PID == 0 && m.Host == ""
}

// Same reports whether m and o record the same operation: the same time, process and host. Their progress can be
// different.
func (m StartMarker) Same(o StartMarker) bool {
	return m.Since == o.Since && m.PID == o.PID && m.Host == o.Host
}

// StopMarker records a stop in progress, with the same fields as a start marker.
type StopMarker = StartMarker

// OperationProgress tells how far a start or stop of an experiment is.
type OperationProgress struct {
	// Operation is "start" or "stop".
	Operation string `json:"operation" mapstructure:"operation" structs:"operation" yaml:"operation"`
	// Percent is the part of the operation that is done, from 0 to 1.
	Percent float64 `json:"percent" mapstructure:"percent" structs:"percent" yaml:"percent"`
	// Phase is the identifier of the current phase, or "done" when the operation is complete.
	Phase string `json:"phase" mapstructure:"phase" structs:"phase" yaml:"phase"`
	// Done and Total are the steps of the phase that are done, and all its steps.
	Done  int `json:"done"  mapstructure:"done"  structs:"done"  yaml:"done"`
	Total int `json:"total" mapstructure:"total" structs:"total" yaml:"total"`
	// Step is the name of the current step, such as an app name, or empty.
	Step string `json:"step" mapstructure:"step" structs:"step" yaml:"step"`
}
