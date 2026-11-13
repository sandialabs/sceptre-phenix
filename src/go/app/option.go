package app

import (
	"phenix/types"
	ifaces "phenix/types/interfaces"
)

// Option is a function that configures options for a phenix app. It is used in
// `app.Init`.
type Option func(*Options)

// Options represents a set of options generic to all apps.
type Options struct {
	Stage   Action
	Name    string // used to set the app name
	DryRun  bool
	Filter  map[string]struct{}
	Trigger bool
	// OnApp, when set, is called before each app runs, with the number of apps that ran before it.
	OnApp func(ran int, name string)
}

// NewOptions returns an Options struct initialized with the given option list.
func NewOptions(opts ...Option) Options {
	o := Options{ //nolint:exhaustruct // partial initialization
		Filter: make(map[string]struct{}),
	}

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// Stage sets the stage for the apps.
func Stage(a Action) Option {
	return func(o *Options) {
		o.Stage = a
	}
}

// Name sets the name for the app.
func Name(n string) Option {
	return func(o *Options) {
		o.Name = n
	}
}

// DryRun sets the dryrun setting for the app.
func DryRun(d bool) Option {
	return func(o *Options) {
		o.DryRun = d
	}
}

// FilterApp adds an app(s) to the list of filtered apps.
func FilterApp(a ...string) Option {
	return func(o *Options) {
		for _, n := range a {
			o.Filter[n] = struct{}{}
		}
	}
}

// Trigger marks app execution as a manual lifecycle trigger. When enabled,
// app status is not reset at pre-start, and cleanup is queued for apps
// running configure/pre-start/post-start stages so they can be cleaned up
// when the experiment stops.
func Trigger() Option {
	return func(o *Options) {
		o.Trigger = true
	}
}

// OnApp has ApplyApps call f before each app runs, with the number of apps of the stage that ran before it and the
// app's name. PlannedApps counts the same apps.
func OnApp(f func(ran int, name string)) Option {
	return func(o *Options) {
		o.OnApp = f
	}
}

// matches reports whether the app name passes the filter of o, which an empty filter always does.
func (o Options) matches(name string) bool {
	if len(o.Filter) == 0 {
		return true
	}

	_, ok := o.Filter[name]

	return ok
}

// skipsDisabled reports whether ApplyApps skips the scenario app a of exp because it is disabled. Running-stage
// triggers historically include disabled apps. Explicit filters provide the same override for other lifecycle
// stages, and so does a cleanup that the app still has pending.
func (o Options) skipsDisabled(exp *types.Experiment, a ifaces.ScenarioApp) bool {
	cleanupPending := o.Stage == ActionCleanup && exp.Status.AppCleanup()[a.Name()]

	return a.Disabled() && o.Stage != ActionRunning && len(o.Filter) == 0 && !cleanupPending
}
