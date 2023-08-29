package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"phenix/types"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/util/shell"

	ifaces "phenix/types/interfaces"
)

// Action represents the different experiment lifecycle hooks.
type Action string

// AppFactory is a function that returns a new app struct.
type AppFactory func() App

type CreateTunnel struct {
	Experiment string
	VM         string
	Sport      string
	Dhost      string
	Dport      string
	User       string
}

type TriggerPublication struct {
	Experiment string
	Verb       string
	App        string
	Resource   string
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
	apps = make(map[string]AppFactory)

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
	apps["ntp"] = func() App { return new(NTP) }
	apps["serial"] = func() App { return new(Serial) }
	apps["startup"] = func() App { return new(Startup) }
	apps["vrouter"] = func() App { return new(Vrouter) }

	// External user apps
	apps["user-shell"] = func() App { return new(UserApp) }
}

func RegisterUserApp(name string, factory AppFactory) error {
	if _, ok := apps[name]; ok {
		return ErrUserAppAlreadyRegistered
	}

	apps[name] = factory
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

	names = append(names, shell.FindCommandsWithPrefix(USER_APP_PREFIX)...)

	return names
}

// GetApp returns the phenix app with the given name. Preference is given to a
// user app with the given name to allow users to override internal apps.
func GetApp(name string) App {
	cmdName := USER_APP_PREFIX + name

	// Default to shelling out to a user app with the given name so internal apps
	// can be overridden by users.
	if shell.CommandExists(cmdName) {
		return apps["user-shell"]()
	}

	app, ok := apps[name]
	if !ok {
		app = apps["user-shell"]
	}

	return app()
}

// DefaultApps returns a slice of all the initialized default phenix apps.
func DefaultApps() []string {
	var apps []string

	for app := range defaultApps {
		apps = append(apps, app)
	}

	return apps
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

	if options.Stage == ACTIONPRESTART {
		// Reset status.apps for experiment. Note that this will get rid of any app
		// status from previous experiment deployments. We do this in the pre-start
		// stage instead of the post-start stage to ensure there's no lingering app
		// status from previous deployments if we have to wait for the post-start
		// stage to run due to delayed start VMs. The state-of-health app was the
		// main driver for this.
		exp.Status.ResetAppStatus()
	}

	// Publish triggered app events so web broker can propogate the publish out to
	// web clients. This was initially setup to help convey SOH status in the UI.
	publish := func(app, state string, err error) {
		pubsub.Publish("trigger-app", TriggerPublication{
			Experiment: exp.Metadata.Name,
			App:        app,
			State:      state,
			Error:      err,
		})
	}

	for _, name := range DefaultApps() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		a := GetApp(name)
		a.Init(Name(name), DryRun(options.DryRun))

		publish(a.Name(), "start", nil)

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

		if err != nil {
			publish(a.Name(), "error", err)

			plog.Error(fmt.Sprintf("[✗] '%s' default app (%s)", a.Name(), options.Stage))
			return fmt.Errorf("applying default app %s for action %s: %w", a.Name(), options.Stage, err)
		}

		publish(a.Name(), "success", nil)

		plog.Info(fmt.Sprintf("[✓] '%s' default app (%s)", a.Name(), options.Stage))
	}

	if exp.Spec.Scenario() != nil {
		for _, app := range exp.Spec.Scenario().Apps() {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			// Don't apply default apps again if configured via the Scenario.
			if _, ok := defaultApps[app.Name()]; ok {
				continue
			}

			// Skip app if disabled, unless stage is ACTIONRUNNING
			if app.Disabled() && options.Stage != ACTIONRUNNING {
				continue
			}

			a := GetApp(app.Name())
			a.Init(Name(app.Name()), DryRun(options.DryRun))

			publish(a.Name(), "start", nil)

			switch options.Stage {
			case ACTIONCONFIG:
				exp.Status.SetAppRunning(app.Name(), true)
				exp.WriteToStore(true)

				err = a.Configure(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)
				exp.WriteToStore(true)
			case ACTIONPRESTART:
				exp.Status.SetAppRunning(app.Name(), true)
				exp.WriteToStore(true)

				err = a.PreStart(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)
				exp.WriteToStore(true)
			case ACTIONPOSTSTART:
				exp.Status.SetAppRunning(app.Name(), true)
				exp.WriteToStore(true)

				err = a.PostStart(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)
				exp.WriteToStore(true)
			case ACTIONRUNNING:
				if len(options.Filter) > 0 {
					if _, ok := options.Filter[app.Name()]; !ok {
						plog.Warn(fmt.Sprintf("Skipping '%s' experiment app (%s)", app.Name(), options.Stage))
						continue
					}
				}

				// Check to make sure this app isn't already running via an automatic
				// periodic execution.
				if running := exp.Status.AppRunning()[app.Name()]; running {
					notes.AddInfo(ctx, false, fmt.Sprintf("app %s is currently already executing its running stage -- skipping", app.Name()))
					continue
				}

				exp.Status.SetAppRunning(app.Name(), true)

				if err := exp.WriteToStore(true); err != nil {
					notes.AddErrors(ctx, false, fmt.Errorf("error updating store with experiment (%s): %v", exp.Spec.ExperimentName(), err))
				}

				err = a.Running(ctx, exp)

				exp.Reload() // reload experiment from store in case status was updated during run
				exp.Status.SetAppRunning(app.Name(), false)

				if err := exp.WriteToStore(true); err != nil {
					notes.AddErrors(ctx, false, fmt.Errorf("error updating store with experiment (%s): %v", exp.Spec.ExperimentName(), err))
				}
			case ACTIONCLEANUP:
				exp.Status.SetAppRunning(app.Name(), true)
				exp.WriteToStore(true)

				err = a.Cleanup(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)
				exp.WriteToStore(true)
			}

			if err != nil {
				publish(a.Name(), "error", err)

				if errors.Is(err, ErrUserAppNotFound) {
					plog.Warn(fmt.Sprintf("[?] '%s' user app (%s)", a.Name(), options.Stage))
					continue
				}

				plog.Error(fmt.Sprintf("[✗] '%s' user app (%s)", a.Name(), options.Stage))
				return fmt.Errorf("applying user app %s for action %s: %w", a.Name(), options.Stage, err)
			}

			publish(a.Name(), "success", nil)

			plog.Info(fmt.Sprintf("[✓] '%s' user app (%s)", a.Name(), options.Stage))
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
					plog.Error("[✗] invalid periodic duration for app", "app", app.Name(), "duration", app.RunPeriodically())
					continue
				}

				plog.Info("[✓] scheduling 'running' stage for app", "app", app.Name(), "duration", app.RunPeriodically())

				wg.Add(1)

				go func(exp *types.Experiment, app ifaces.ScenarioApp, duration time.Duration) {
					defer wg.Done()

					exp.Status.SetAppFrequency(app.Name(), app.RunPeriodically())
					exp.Status.SetAppRunning(app.Name(), false)

					if err := exp.WriteToStore(true); err != nil {
						plog.Error("[✗] error updating store with experiment", "exp", exp.Metadata.Name, "err", err)
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
								plog.Error("[✗] error updating store with experiment", "exp", exp.Metadata.Name, "err", err)
							}

							return
						case <-timer.C:
							// Check to make sure this app wasn't triggered manually between
							// periodic runs.
							if running := exp.Status.AppRunning()[app.Name()]; running {
								plog.Info("[✓] app is currently already executing its running stage -- skipping", "app", app.Name())
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
								plog.Error("[✗] error updating store with experiment", "exp", exp.Metadata.Name, "err", err)
							}

							pubsub.Publish("trigger-app", TriggerPublication{
								Experiment: exp.Spec.ExperimentName(), App: app.Name(), State: "start",
							})

							if err := a.Running(ctx, exp); err != nil {
								pubsub.Publish("trigger-app", TriggerPublication{
									Experiment: exp.Spec.ExperimentName(), App: app.Name(), State: "error", Error: err,
								})

								plog.Error("[✗] error periodically running app", "app", app.Name(), "err", err)
							}

							pubsub.Publish("trigger-app", TriggerPublication{
								Experiment: exp.Spec.ExperimentName(), App: app.Name(), State: "success",
							})

							exp.Status.SetAppRunning(app.Name(), false)

							if err := exp.WriteToStore(true); err != nil {
								plog.Error("[✗] error updating store with experiment", "exp", exp.Metadata.Name, "err", err)
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
