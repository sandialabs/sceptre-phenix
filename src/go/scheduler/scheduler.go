package scheduler

import (
	"errors"
	"maps"
	"slices"
	"strings"

	ifaces "phenix/types/interfaces"
	"phenix/util/shell"
)

var schedulers = make(map[string]Scheduler) //nolint:gochecknoglobals // global registry

// descriptions says in one or two sentences what each built-in scheduler does.
var descriptions = map[string]string{ //nolint:gochecknoglobals // global registry
	"isolate-experiment": "Puts all VMs on one host that has no VMs: the host that you set for the first VM " +
		"if that host is empty, else the empty host with the most free memory. Replaces the hosts that you set.",
	"round-robin": "Puts each VM that has no host on the host that has the fewest VMs at that time, " +
		"so that the VMs spread evenly. Keeps the hosts that you set.",
	"subnet-compute": "Puts VMs whose first interface is on the same VLAN on the same host while it has " +
		"free memory, and starts each new VLAN on the host with the least committed memory. " +
		"Keeps the hosts that you set.",
}

// ErrNoSchedulableHosts is returned when a VM needs a cluster host and the
// cluster has none that VMs can be scheduled on.
var ErrNoSchedulableHosts = errors.New("no schedulable cluster hosts")

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

// Info names one scheduler that phenix can run, and tells whether phenix
// has it built in or runs it as a phenix-scheduler-<name> executable.
type Info struct {
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
	// Description says what a built-in scheduler does; a user one has none.
	Description string `json:"description,omitempty"`
}

// Available returns the built-in schedulers, sorted by name, then the user
// schedulers that PATH has, sorted by name. A user scheduler with the name of
// a built-in one is left out, as Schedule runs the built-in one.
func Available() []Info {
	builtin := slices.Sorted(maps.Keys(schedulers))
	infos := make([]Info, 0, len(builtin))

	for _, name := range builtin {
		infos = append(infos, Info{Name: name, Builtin: true, Description: descriptions[name]})
	}

	user := shell.FindCommandsWithPrefix("phenix-scheduler-")
	slices.Sort(user)

	for _, name := range slices.Compact(user) {
		if _, ok := schedulers[name]; !ok {
			infos = append(infos, Info{Name: name, Builtin: false, Description: ""})
		}
	}

	return infos
}

// builtinName returns the built-in scheduler name names, also when name
// spells it with underscores for dashes, as older clients do (such as
// round_robin). It returns name as it is for any other name.
func builtinName(name string) string {
	if _, ok := schedulers[name]; ok {
		return name
	}

	if dashed := strings.ReplaceAll(name, "_", "-"); dashed != name {
		if _, ok := schedulers[dashed]; ok {
			return dashed
		}
	}

	return name
}

// Schedule runs the scheduler name on spec: the built-in scheduler of that
// name (see builtinName), else the user scheduler phenix-scheduler-<name>.
func Schedule(name string, spec ifaces.ExperimentSpec) error {
	scheduler, ok := schedulers[builtinName(name)]
	if !ok {
		scheduler = new(userScheduler)
		_ = scheduler.Init(Name(name))
	}

	return scheduler.Schedule(spec)
}
