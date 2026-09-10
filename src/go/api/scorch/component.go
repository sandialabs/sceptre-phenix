package scorch

import (
	"context"
	"fmt"
)

// Action represents the different SCORCH lifecycle hooks.
type Action string

const (
	ActionConfigure Action = "configure"
	ActionStart     Action = "start"
	ActionStop      Action = "stop"
	ActionCleanup   Action = "cleanup"
	ActionDone      Action = "done"
	ActionLoop      Action = "loop"
)

// Component is the interface that identifies all the required functionality
// (SCORCH lifecycle hooks, mainly) for a SCORCH component. Not all lifecycle
// hook functions have to be implemented.  If one (or more) isn't needed for a
// component, it should simply return nil.
type Component interface {
	// Init is used to initialize a SCORCH component with options generic to all
	// components.
	Init(...Option) error

	// Type returns the type of the SCORCH component.
	Type() string

	// Configure is called for a component at the `configure` SCORCH lifecycle
	// phase.
	Configure(context.Context) error

	// Start is called for a component at the `start` SCORCH lifecycle phase.
	Start(context.Context) error

	// Stop is called for a component at the `stop` SCORCH lifecycle phase.
	Stop(context.Context) error

	// Cleanup is called for a component at the `cleanup` SCORCH lifecycle phase.
	Cleanup(context.Context) error
}

// components are constructed per execution: Init stores the run's options on
// the instance, so a shared one would be overwritten by a concurrent run.
var components map[string]func() Component //nolint:gochecknoglobals // global registry

func init() { //nolint:gochecknoinits // component registration
	components = map[string]func() Component{
		"break":      func() Component { return new(Break) },
		"pause":      func() Component { return new(Pause) },
		"soh":        func() Component { return new(SOH) },
		"tap":        func() Component { return new(Tap) },
		"user-shell": func() Component { return new(UserComponent) },
	}
}

//nolint:ireturn // factory function returns interface
func GetComponent(name string) Component {
	factory, ok := components[name]
	if !ok {
		factory = components["user-shell"]
	}

	return factory()
}

func ExecuteComponent(ctx context.Context, opts ...Option) error {
	options := NewOptions(opts...)

	cmp := GetComponent(options.Type)

	_ = cmp.Init(opts...)

	var err error

	switch options.Stage {
	case ActionConfigure:
		err = cmp.Configure(ctx)
	case ActionStart:
		err = cmp.Start(ctx)
	case ActionStop:
		err = cmp.Stop(ctx)
	case ActionCleanup:
		err = cmp.Cleanup(ctx)
	case ActionDone, ActionLoop:
		// no-op
	}

	if err != nil {
		return fmt.Errorf("running %s stage for component %s: %w", options.Stage, options.Type, err)
	}

	return nil
}
