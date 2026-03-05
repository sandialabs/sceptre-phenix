package scheduler

import (
	ifaces "phenix/types/interfaces"
	"phenix/util/shell"
)

var schedulers = make(map[string]Scheduler) //nolint:gochecknoglobals // global registry

// Scheduler is the interface that identifies all the required functionality for
// a phenix scheduler.
type Scheduler interface {
	// Init is used to initialize a phenix scheduler with options generic to all
	// schedulers.
	Init(...Option) error

	// Name returns the name of the phenix scheduler.
	Name() string

	// Schedule runs the phenix scheduler algorithm against the given experiment.
	Schedule(ifaces.ExperimentSpec) error
}

func List() []string {
	names := make([]string, 0, len(schedulers))

	for name := range schedulers {
		names = append(names, name)
	}

	names = append(names, shell.FindCommandsWithPrefix("phenix-scheduler-")...)

	return names
}

func Schedule(name string, spec ifaces.ExperimentSpec) error {
	scheduler, ok := schedulers[name]
	if !ok {
		scheduler = new(userScheduler)
		_ = scheduler.Init(Name(name))
	}

	return scheduler.Schedule(spec)
}
