package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/pubsub"
	"phenix/util/shell"

	"github.com/fatih/color"
)

// Action represents the different experiment lifecycle hooks.
type Action string

type Publication struct {
	Experiment string
	App        string
	State      string
	Error      error
}

const (
	ACTIONCONFIG    Action = "configure"
	ACTIONPRESTART  Action = "pre-start"
	ACTIONPOSTSTART Action = "post-start"
	ACTIONRUNNING   Action = "running"
	ACTIONCLEANUP   Action = "cleanup"
)

var (
	apps = make(map[string]App)

	defaultApps = map[string]struct{}{
		"ntp":     {},
		"serial":  {},
		"startup": {},
		"vrouter": {},
	}
)

var ErrUserAppAlreadyRegistered = fmt.Errorf("user app already registered")

func init() {
	// Default apps (always run)
	apps["ntp"] = new(NTP)
	apps["serial"] = new(Serial)
	apps["startup"] = new(Startup)
	apps["vrouter"] = new(Vrouter)

	// External user apps
	apps["user-shell"] = new(UserApp)
}

func RegisterUserApp(app App) error {
	if _, ok := apps[app.Name()]; ok {
		return ErrUserAppAlreadyRegistered
	}

	apps[app.Name()] = app
	return nil
}

// List returns a list of non-default phenix applications.
func List() []string {
	var names []string

	for name := range apps {
		// Don't include app that wraps external user apps.
		if name == "user-shell" {
			continue
		}

		// Don't include default apps in the list since they always get applied.
		if _, ok := defaultApps[name]; ok {
			continue
		}

		names = append(names, name)
	}

	for _, name := range shell.FindCommandsWithPrefix("phenix-app-") {
		names = append(names, name)
	}

	return names
}

// GetApp returns the initialized phenix app with the given name. If an app with
// the given name is not known internally, it returns the generic `user-shell`
// app that handles shelling out to external custom user apps.
func GetApp(name string) App {
	app, ok := apps[name]
	if !ok {
		app = apps["user-shell"]
	}

	return app
}

// DefaultApps returns a slice of all the initialized default phenix apps.
func DefaultApps() []App {
	var a []App

	for app := range defaultApps {
		a = append(a, apps[app])
	}

	return a
}

// App is the interface that identifies all the required functionality for a
// phenix app. Each experiment lifecycle hook function is passed a pointer to
// the experiment the app is being applied to, and the lifecycle hook function
// should modify the experiment as necessary. Not all lifecycle hook functions
// have to be implemented. If one (or more) isn't needed for an app, it should
// simply return nil.
type App interface {
	// Init is used to initialize a phenix app with options generic to all apps.
	Init(...Option) error

	// Name returns the name of the phenix app.
	Name() string

	// Configure is called for an app at the `configure` experiment lifecycle
	// phase.
	Configure(context.Context, *types.Experiment) error

	// Start is called for an app at the `pre-start` experiment lifecycle phase.
	PreStart(context.Context, *types.Experiment) error

	// PostStart is called for an app at the `post-start` experiment lifecycle
	// phase.
	PostStart(context.Context, *types.Experiment) error

	// Running can be called for an app while an experiment is running. It will
	// only be called on-demand by a user or the web UI. The code that implements
	// this function should always be idempotent.
	Running(context.Context, *types.Experiment) error

	// Cleanup is called for an app at the `cleanup` experiment lifecycle
	// phase.
	Cleanup(context.Context, *types.Experiment) error
}

// ApplyApps applies all the default phenix apps and any configured user apps to
// the given experiment for the given lifecycle phase. It returns any errors
// encountered while applying the apps.
func ApplyApps(ctx context.Context, exp *types.Experiment, opts ...Option) error {
	var (
		options = NewOptions(opts...)
		err     error
	)

	if options.Stage == ACTIONPOSTSTART || options.Stage == ACTIONCLEANUP {
		// Reset status.apps for experiment. Note that this will get rid of any app
		// status from previous experiment deployments.
		exp.Status.ResetAppStatus()
	}

	for _, a := range DefaultApps() {
		switch options.Stage {
		case ACTIONCONFIG:
			err = a.Configure(ctx, exp)
		case ACTIONPRESTART:
			err = a.PreStart(ctx, exp)
		case ACTIONPOSTSTART:
			err = a.PostStart(ctx, exp)
		case ACTIONRUNNING:
			continue // silently ignore running stage for default apps
		case ACTIONCLEANUP:
			err = a.Cleanup(ctx, exp)
		}

		var (
			status  = "✓"
			printer = color.New(color.FgGreen)
		)

		if err != nil {
			status = "✗"
			printer = color.New(color.FgRed)
		}

		printer.Printf("[%s] '%s' default app (%s)\n", status, a.Name(), options.Stage)

		if err != nil {
			return fmt.Errorf("applying default app %s for action %s: %w", a.Name(), options.Stage, err)
		}
	}

	if exp.Spec.Scenario() != nil {
		for _, app := range exp.Spec.Scenario().Apps() {
			// Don't apply default apps again if configured via the Scenario.
			if _, ok := defaultApps[app.Name()]; ok {
				continue
			}

			a := GetApp(app.Name())
			a.Init(Name(app.Name()), DryRun(options.DryRun))

			switch options.Stage {
			case ACTIONCONFIG:
				err = a.Configure(ctx, exp)
			case ACTIONPRESTART:
				err = a.PreStart(ctx, exp)
			case ACTIONPOSTSTART:
				err = a.PostStart(ctx, exp)
			case ACTIONRUNNING:
				if len(options.Filter) > 0 {
					if _, ok := options.Filter[app.Name()]; !ok {
						printer := color.New(color.FgYellow)
						printer.Printf("Skipping '%s' experiment app (%s)\n", app.Name(), options.Stage)

						continue
					}
				}

				// Check to make sure this app isn't already running via an automatic
				// periodic execution.
				if running := exp.Status.AppRunning()[app.Name()]; running {
					color.New(color.FgBlue).Printf("[✓] app %s is currently already executing its running stage -- skipping\n", app.Name())
					continue
				}

				exp.Status.SetAppRunning(app.Name(), true)

				if err := exp.WriteToStore(true); err != nil {
					color.New(color.FgRed).Printf("[✗] error updating store with experiment (%s): %v\n", exp.Metadata.Name, err)
				}

				err = a.Running(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)

				if err := exp.WriteToStore(true); err != nil {
					color.New(color.FgRed).Printf("[✗] error updating store with experiment (%s): %v\n", exp.Metadata.Name, err)
				}
			case ACTIONCLEANUP:
				err = a.Cleanup(ctx, exp)
			}

			var (
				status  = "✓"
				printer = color.New(color.FgGreen)
			)

			if err != nil {
				if errors.Is(err, ErrUserAppNotFound) {
					status = "?"
					printer = color.New(color.FgYellow)
				} else {
					status = "✗"
					printer = color.New(color.FgRed)
				}
			}

			printer.Printf("[%s] '%s' user app (%s)\n", status, a.Name(), options.Stage)

			if err != nil {
				if errors.Is(err, ErrUserAppNotFound) {
					continue
				}

				return fmt.Errorf("applying user app %s for action %s: %w", a.Name(), options.Stage, err)
			}
		}
	}

	if options.Stage == ACTIONCONFIG || options.Stage == ACTIONPRESTART {
		// just in case one of the apps added some nodes to the topology...
		exp.Spec.Topology().Init()
	}

	return nil
}

// PeriodicallyRunApps checks the configuration for each app in the scenario to
// see if it's configured to have its "running" stage run periodically. A
// Goroutine is scheduled for each applicable app.
func PeriodicallyRunApps(ctx context.Context, wg *sync.WaitGroup, exp *types.Experiment) error {
	if exp.Spec.Scenario() != nil {
		for _, app := range exp.Spec.Scenario().Apps() {
			// Don't consider default apps as candidates for running periodically.
			if _, ok := defaultApps[app.Name()]; ok {
				continue
			}

			if app.RunPeriodically() != "" {
				duration, err := time.ParseDuration(app.RunPeriodically())
				if err != nil {
					color.New(color.FgRed).Printf("[✗] invalid periodic duration of '%s' for app (%s)\n", app.RunPeriodically(), app.Name())
					continue
				}

				color.New(color.FgBlue).Printf("[✓] scheduling 'running' stage for app (%s) every %s\n", app.Name(), app.RunPeriodically())

				wg.Add(1)

				go func(exp *types.Experiment, app ifaces.ScenarioApp, duration time.Duration) {
					defer wg.Done()

					exp.Status.SetAppFrequency(app.Name(), app.RunPeriodically())
					exp.Status.SetAppRunning(app.Name(), false)

					if err := exp.WriteToStore(true); err != nil {
						color.New(color.FgRed).Printf("[✗] error updating store with experiment (%s): %v\n", exp.Metadata.Name, err)
					}

					timer := time.NewTimer(duration)

					for {
						select {
						case <-ctx.Done():
							if !timer.Stop() {
								<-timer.C
							}

							exp.Status.SetAppFrequency(app.Name(), "")
							exp.Status.SetAppRunning(app.Name(), false)

							if err := exp.WriteToStore(true); err != nil {
								color.New(color.FgRed).Printf("[✗] error updating store with experiment (%s): %v\n", exp.Metadata.Name, err)
							}

							return
						case <-timer.C:
							// Check to make sure this app wasn't triggered manually between
							// periodic runs.
							if running := exp.Status.AppRunning()[app.Name()]; running {
								color.New(color.FgBlue).Printf("[✓] app %s is currently already executing its running stage -- skipping\n", app.Name())
								continue
							}

							// NOTE: there is a chance for a race condition here. Between the
							// time we pull the experiment config from the store and read its
							// app running status above, a manual trigger could have set
							// running too. As of now, if this happens, multiple instances of
							// the running stage will be executing at the same time. This
							// might be a good place for optimistic locking.

							a := GetApp(app.Name())
							a.Init(Name(app.Name()))

							exp.Status.SetAppRunning(app.Name(), true)

							if err := exp.WriteToStore(true); err != nil {
								color.New(color.FgRed).Printf("[✗] error updating store with experiment (%s): %v\n", exp.Metadata.Name, err)
							}

							pubsub.Publish("trigger-app", Publication{Experiment: exp.Metadata.Name, App: app.Name(), State: "start"})

							if err := a.Running(ctx, exp); err != nil {
								pubsub.Publish("trigger-app", Publication{Experiment: exp.Metadata.Name, App: app.Name(), State: "error", Error: err})

								color.New(color.FgRed).Printf("[✗] error periodically running app (%s): %v\n", app.Name(), err)
							}

							pubsub.Publish("trigger-app", Publication{Experiment: exp.Metadata.Name, App: app.Name(), State: "success"})

							exp.Status.SetAppRunning(app.Name(), false)

							if err := exp.WriteToStore(true); err != nil {
								color.New(color.FgRed).Printf("[✗] error updating store with experiment (%s): %v\n", exp.Metadata.Name, err)
							}

							timer.Reset(duration)
						}
					}
				}(exp, app, duration)
			}
		}
	}

	return nil
}
