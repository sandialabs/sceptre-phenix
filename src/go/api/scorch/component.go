package scorch

import (
	"context"
	"fmt"
)

// Action represents the different SCORCH lifecycle hooks.
type Action string

const (
	ACTIONCONFIG  Action = "configure"
	ACTIONSTART   Action = "start"
	ACTIONSTOP    Action = "stop"
	ACTIONCLEANUP Action = "cleanup"
	ACTIONDONE    Action = "done"
	ACTIONLOOP    Action = "loop"
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

var components = make(map[string]Component)

func init() {
	components["break"] = new(Break)
	components["pause"] = new(Pause)
	components["tap"] = new(Tap)
	components["user-shell"] = new(UserComponent)
}

func GetComponent(name string) Component {
	cmp, ok := components[name]
	if !ok {
		cmp = components["user-shell"]
	}

	return cmp
}

func ExecuteComponent(ctx context.Context, opts ...Option) error {
	options := NewOptions(opts...)

	cmp, ok := components[options.Type]
	if !ok {
		cmp = components["user-shell"]
	}

	cmp.Init(opts...)

	var err error

	switch options.Stage {
	case ACTIONCONFIG:
		err = cmp.Configure(ctx)
	case ACTIONSTART:
		err = cmp.Start(ctx)
	case ACTIONSTOP:
		err = cmp.Stop(ctx)
	case ACTIONCLEANUP:
		err = cmp.Cleanup(ctx)
	}

	if err != nil {
		return fmt.Errorf("running %s stage for component %s: %w", options.Stage, options.Type, err)
	}

	return nil
}
