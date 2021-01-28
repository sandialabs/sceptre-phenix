package app

// Option is a function that configures options for a phenix app. It is used in
// `app.Init`.
type Option func(*Options)

// Options represents a set of options generic to all apps.
type Options struct {
	Stage  Action
	Name   string // used to set the app name
	DryRun bool
	Filter map[string]struct{}
}

// NewOptions returns an Options struct initialized with the given option list.
func NewOptions(opts ...Option) Options {
	o := Options{
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

// Filter adds an app(s) to the list of filtered apps.
func FilterApp(a ...string) Option {
	return func(o *Options) {
		for _, n := range a {
			o.Filter[n] = struct{}{}
		}
	}
}
