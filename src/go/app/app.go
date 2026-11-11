package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/util/shell"
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

// TriggerStateCanceled is the state published when a triggered app run is canceled.
const TriggerStateCanceled = "canceled"

// ErrAlreadyRunning is the error of an app whose running stage is already in
// progress for the experiment. A run that gets it does not clear the running
// flag, because the flag belongs to the run in progress.
var ErrAlreadyRunning = errors.New("app running stage already in progress")

// claimRunning sets the running flag of app in the stored status of exp when
// no other run has set it, and tells if it did. When the store fails, it uses
// the flag in memory and also returns the store error.
func claimRunning(exp *types.Experiment, app string) (bool, error) {
	var claimed bool

	err := exp.UpdateStatus(func(s ifaces.ExperimentStatus) {
		if !s.AppRunning()[app] {
			s.SetAppRunning(app, true)

			claimed = true
		}
	})
	if err != nil {
		claimed = !exp.Status.AppRunning()[app]
		exp.Status.SetAppRunning(app, true)

		return claimed, fmt.Errorf("claiming the running stage of app %s: %w", app, err)
	}

	return claimed, nil
}

// releaseRunning clears the running flag of app in the stored status of exp,
// and in memory when the store fails.
func releaseRunning(exp *types.Experiment, app string) error {
	err := exp.UpdateStatus(func(s ifaces.ExperimentStatus) { s.SetAppRunning(app, false) })
	if err != nil {
		exp.Status.SetAppRunning(app, false)

		return fmt.Errorf("releasing the running stage of app %s: %w", app, err)
	}

	return nil
}

// publishRunResult announces how an app's running stage ended: error or success, never both.
func publishRunResult(expName, appName string, err error) {
	pub := TriggerPublication{Experiment: expName, App: appName, State: "success"} //nolint:exhaustruct // partial initialization
	if err != nil {
		pub.State, pub.Error = "error", err
	}

	pubsub.Publish("trigger-app", pub)
}

type TriggerPublication struct {
	Experiment string
	Verb       string
	App        string
	Resource   string
	// Stage is the lifecycle stage of the app run, when the publisher names it.
	Stage string
	State string
	Error error
}

const (
	ActionConfigure Action = "configure"
	ActionPreStart  Action = "pre-start"
	ActionPostStart Action = "post-start"
	ActionRunning   Action = "running"
	ActionCleanup   Action = "cleanup"

	defaultAppsAnnotation = "phenix/default-apps"
	osWindows             = "windows"
	osLinux               = "linux"
	osRHEL                = "rhel"
	osCentOS              = "centos"
)

// Actions returns the supported experiment app lifecycle actions.
func Actions() []Action {
	return []Action{
		ActionConfigure,
		ActionPreStart,
		ActionPostStart,
		ActionRunning,
		ActionCleanup,
	}
}

// ErrUnknownAction is wrapped by the error of ParseAction for text that names
// no lifecycle stage.
var ErrUnknownAction = errors.New("invalid lifecycle")

// ActionChoices lists the lifecycle stages with their shorthands, as
// "configure (config), pre-start (pre), ...".
func ActionChoices() string {
	actions := Actions()
	choices := make([]string, 0, len(actions))

	for _, action := range actions {
		choices = append(choices, fmt.Sprintf("%s (%s)", action, action.Shorthand()))
	}

	return strings.Join(choices, ", ")
}

// ParseAction returns the lifecycle stage that value names: its full name,
// such as pre-start, or its shorthand, such as pre. Letter case and leading
// and trailing spaces do not matter. For other text, the error wraps
// ErrUnknownAction, quotes value and lists the stages.
func ParseAction(value string) (Action, error) {
	text := strings.ToLower(strings.TrimSpace(value))

	for _, action := range Actions() {
		if text == string(action) || text == action.Shorthand() {
			return action, nil
		}
	}

	return "", fmt.Errorf(
		"%w %q (valid lifecycles and shorthands: %s)", ErrUnknownAction, value, ActionChoices(),
	)
}

// Shorthand returns the shorthand for the action.
func (a Action) Shorthand() string {
	switch a {
	case ActionConfigure:
		return "config"
	case ActionPreStart:
		return "pre"
	case ActionPostStart:
		return "post"
	case ActionRunning:
		return "run"
	case ActionCleanup:
		return "clean"
	default:
		return ""
	}
}

// Valid reports whether the action is a supported experiment app lifecycle action.
func (a Action) Valid() bool {
	switch a {
	case ActionConfigure, ActionPreStart, ActionPostStart, ActionRunning, ActionCleanup:
		return true
	default:
		return false
	}
}

var (
	apps = make(map[string]AppFactory) //nolint:gochecknoglobals // global registry

	defaultApps = map[string]struct{}{ //nolint:gochecknoglobals // global constant
		appNameNTP:     {},
		appNameSerial:  {},
		appNameStartup: {},
		"vrouter":      {},
	}
)

var ErrUserAppAlreadyRegistered = errors.New("user app already registered")

func init() { //nolint:gochecknoinits // app registration
	// Default apps (always run)
	apps[appNameNTP] = func() App { return new(NTP) }
	apps[appNameSerial] = func() App { return new(Serial) }
	apps[appNameStartup] = func() App { return new(Startup) }
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

	names = append(names, shell.FindCommandsWithPrefix(UserAppPrefix)...)

	return names
}

// BuiltinApps returns, sorted, the names of the apps built into phenix: the
// default apps and the other apps that register themselves. phenix runs a
// phenix-app-<name> on PATH for any other name.
func BuiltinApps() []string {
	names := make([]string, 0, len(apps))

	for name := range apps {
		if name != "user-shell" {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

// GetApp returns the phenix app with the given name. Preference is given to a
// user app with the given name to allow users to override internal apps.
func GetApp(name string) App {
	cmdName := UserAppPrefix + name

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

// DefaultApps returns the names of the default phenix apps, sorted, which is
// the order ApplyApps runs them in.
func DefaultApps() []string {
	return slices.Sorted(maps.Keys(defaultApps))
}

// ExperimentApps returns the names of all apps (default and scenario) that
// are part of the given experiment.
func ExperimentApps(exp *types.Experiment) []string {
	names := DefaultApps()

	if exp.Spec.Scenario() != nil {
		for _, a := range exp.Spec.Scenario().Apps() {
			names = append(names, a.Name())
		}
	}

	return names
}

// StageApplicable returns whether the given lifecycle stage is applicable for
// the named app. Default apps run automatically as part of the other
// lifecycle stages and don't support being triggered for the running stage.
func StageApplicable(stage Action, name string) bool {
	if stage == ActionRunning {
		if _, ok := defaultApps[name]; ok {
			return false
		}
	}

	return true
}

func defaultAppsEnabled(node ifaces.NodeSpec) bool {
	enabled, ok := node.GetAnnotation(defaultAppsAnnotation)
	if !ok {
		return true
	}

	value, ok := enabled.(bool)

	return !ok || value
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

// PlannedApps lists the apps that ApplyApps with opts runs on exp, in order: the default apps, then the scenario
// apps. For the running stage it lists no default app, because they have no running stage.
func PlannedApps(exp *types.Experiment, opts ...Option) []string {
	options := NewOptions(opts...)

	var names []string

	if options.Stage != ActionRunning {
		for _, name := range DefaultApps() {
			if options.matches(name) {
				names = append(names, name)
			}
		}
	}

	if exp.Spec.Scenario() == nil {
		return names
	}

	for _, a := range exp.Spec.Scenario().Apps() {
		if _, ok := defaultApps[a.Name()]; ok || !options.matches(a.Name()) || options.skipsDisabled(exp, a) {
			continue
		}

		names = append(names, a.Name())
	}

	return names
}

// ApplyApps applies all the default phenix apps and any configured user apps to
// the given experiment for the given lifecycle phase. It returns any errors
// encountered while applying the apps.
//
//nolint:cyclop,funlen,gocyclo,maintidx // complex lifecycle dispatch logic
func ApplyApps(ctx context.Context, exp *types.Experiment, opts ...Option) error {
	var (
		options = NewOptions(opts...)
		err     error
	)

	// External apps in this run share one reading of the cluster's hosts and
	// the experiment's VMs.
	ctx = withStageRuntime(ctx)

	if options.Stage == ActionPreStart && !options.Trigger {
		// Reset status.apps for experiment. Note that this will get rid of any app
		// status from previous experiment deployments. We do this in the pre-start
		// stage instead of the post-start stage to ensure there's no lingering app
		// status from previous deployments if we have to wait for the post-start
		// stage to run due to delayed start VMs. The state-of-health app was the
		// main driver for this.
		exp.Status.ResetAppStatus()
	}

	// Publish app events, with their stage, so web broker can propagate the publish out to
	// web clients. This was initially setup to help convey SOH status in the UI.
	publish := func(app, state string, err error) {
		pubsub.Publish("trigger-app", TriggerPublication{ //nolint:exhaustruct // partial initialization
			Experiment: exp.Metadata.Name,
			App:        app,
			Stage:      string(options.Stage),
			State:      state,
			Error:      err,
		})
	}

	// how many apps ran, for OnApp
	ran := 0

	onApp := func(name string) {
		if options.OnApp != nil {
			options.OnApp(ran, name)
		}

		ran++
	}

	queueCleanup := func(name string) {
		if !options.Trigger {
			return
		}

		switch options.Stage {
		case ActionConfigure, ActionPreStart, ActionPostStart:
			exp.Status.SetAppCleanup(name, true)
		case ActionRunning, ActionCleanup:
			// Running stage: apps handle cleanup at experiment stop time.
			// Cleanup stage: the cleanup queue is consumed, not added to.
		}
	}

	for _, name := range DefaultApps() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !options.matches(name) {
			continue
		}

		if options.Stage != ActionRunning {
			onApp(name)
		}

		a := GetApp(name)
		_ = a.Init(Name(name), DryRun(options.DryRun))

		queueCleanup(a.Name())
		publish(a.Name(), "start", nil)

		begun := time.Now()

		switch options.Stage {
		case ActionConfigure:
			err = a.Configure(ctx, exp)
		case ActionPreStart:
			err = a.PreStart(ctx, exp)
		case ActionPostStart:
			err = a.PostStart(ctx, exp)
		case ActionRunning:
			continue // silently ignore running stage for default apps
		case ActionCleanup:
			err = a.Cleanup(ctx, exp)
		}

		if err != nil {
			publish(a.Name(), "error", err)

			plog.Error(
				plog.TypePhenixApp,
				fmt.Sprintf("[✗] '%s' default app (%s)", a.Name(), options.Stage),
				"duration", stageDuration(begun),
			)

			return fmt.Errorf(
				"applying default app %s for action %s: %w",
				a.Name(),
				options.Stage,
				err,
			)
		}

		if options.Stage == ActionCleanup {
			exp.Status.SetAppCleanup(a.Name(), false)
		}

		publish(a.Name(), "success", nil)

		plog.Info(
			plog.TypePhenixApp,
			fmt.Sprintf("[✓] '%s' default app (%s)", a.Name(), options.Stage),
			"duration", stageDuration(begun),
		)
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

			if !options.matches(app.Name()) {
				plog.Debug(
					plog.TypePhenixApp,
					fmt.Sprintf(
						"Skipping '%s' experiment app (%s)",
						app.Name(),
						options.Stage,
					),
				)

				continue
			}

			if options.skipsDisabled(exp, app) {
				continue
			}

			onApp(app.Name())

			a := GetApp(app.Name())
			_ = a.Init(Name(app.Name()), DryRun(options.DryRun))

			queueCleanup(a.Name())
			publish(a.Name(), "start", nil)

			begun := time.Now()

			switch options.Stage {
			case ActionConfigure:
				exp.Status.SetAppRunning(app.Name(), true)
				_ = exp.WriteToStore(true)

				err = a.Configure(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)
				_ = exp.WriteToStore(true)
			case ActionPreStart:
				exp.Status.SetAppRunning(app.Name(), true)
				_ = exp.WriteToStore(true)

				err = a.PreStart(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)
				_ = exp.WriteToStore(true)
			case ActionPostStart:
				exp.Status.SetAppRunning(app.Name(), true)
				_ = exp.WriteToStore(true)

				err = a.PostStart(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)
				_ = exp.WriteToStore(true)
			case ActionRunning:
				// Skip the app when another run, such as a periodic one, has the flag
				// in the store; the copy of the caller can be older.
				claimed, storeErr := claimRunning(exp, app.Name())
				if storeErr != nil {
					notes.AddErrors(
						ctx,
						false,
						fmt.Errorf(
							"error updating store with experiment (%s): %w",
							exp.Spec.ExperimentName(),
							storeErr,
						),
					)
				}

				if claimed {
					// Assign to the function-level err to carry out
					err = a.Running(ctx, exp)
				}

				if !claimed || errors.Is(err, ErrAlreadyRunning) {
					notes.AddInfo(
						ctx,
						false,
						fmt.Sprintf(
							"app %s is currently already executing its running stage -- skipping",
							app.Name(),
						),
					)

					continue
				}

				storeErr = releaseRunning(exp, app.Name())
				if storeErr != nil {
					notes.AddErrors(
						ctx,
						false,
						fmt.Errorf(
							"error updating store with experiment (%s): %w",
							exp.Spec.ExperimentName(),
							storeErr,
						),
					)
				}
			case ActionCleanup:
				exp.Status.SetAppRunning(app.Name(), true)
				_ = exp.WriteToStore(true)

				err = a.Cleanup(ctx, exp)

				exp.Status.SetAppRunning(app.Name(), false)
				_ = exp.WriteToStore(true)
			}

			if err != nil {
				publish(a.Name(), "error", err)

				if errors.Is(err, ErrUserAppNotFound) {
					plog.Warn(
						plog.TypePhenixApp,
						fmt.Sprintf("[?] '%s' user app (%s)", a.Name(), options.Stage),
						"duration", stageDuration(begun),
					)

					continue
				}

				plog.Error(
					plog.TypePhenixApp,
					fmt.Sprintf("[✗] '%s' user app (%s)", a.Name(), options.Stage),
					"duration", stageDuration(begun),
				)

				return fmt.Errorf(
					"applying user app %s for action %s: %w",
					a.Name(),
					options.Stage,
					err,
				)
			}

			if options.Stage == ActionCleanup {
				exp.Status.SetAppCleanup(a.Name(), false)
			}

			publish(a.Name(), "success", nil)

			plog.Info(
				plog.TypePhenixApp,
				fmt.Sprintf("[✓] '%s' user app (%s)", a.Name(), options.Stage),
				"duration", stageDuration(begun),
			)
		}
	}

	if options.Stage == ActionConfigure || options.Stage == ActionPreStart {
		// just in case one of the apps added some nodes to the topology...
		_ = exp.Spec.Topology().Init(exp.Spec.DefaultBridge())
	}

	return nil
}

// ScheduledApps are the names of the apps of the experiment's scenario that
// PeriodicallyRunApps runs on a schedule: each app, other than a default app,
// whose run-periodically value is a duration.
func ScheduledApps(exp *types.Experiment) []string {
	var names []string

	if exp.Spec.Scenario() == nil {
		return names
	}

	for _, a := range exp.Spec.Scenario().Apps() {
		if _, ok := defaultApps[a.Name()]; ok || a.RunPeriodically() == "" {
			continue
		}

		if _, err := time.ParseDuration(a.RunPeriodically()); err == nil {
			names = append(names, a.Name())
		}
	}

	return names
}

// PeriodicallyRunApps checks the configuration for each app in the scenario to
// see if it's configured to have its "running" stage run periodically. A
// Goroutine is scheduled for each applicable app.
//
//nolint:funlen // complex logic
func PeriodicallyRunApps(ctx context.Context, wg *sync.WaitGroup, exp *types.Experiment) {
	if exp.Spec.Scenario() != nil {
		for _, app := range exp.Spec.Scenario().Apps() {
			// Don't consider default apps as candidates for running periodically.
			if _, ok := defaultApps[app.Name()]; ok {
				continue
			}

			if app.RunPeriodically() != "" {
				duration, err := time.ParseDuration(app.RunPeriodically())
				if err != nil {
					plog.Error(
						plog.TypePhenixApp,
						"[✗] invalid periodic duration for app",
						"app",
						app.Name(),
						"duration",
						app.RunPeriodically(),
					)

					continue
				}

				plog.Info(
					plog.TypePhenixApp,
					"[✓] scheduling 'running' stage for app",
					"app",
					app.Name(),
					"duration",
					app.RunPeriodically(),
				)

				wg.Add(1)

				// Each worker has its own copy, so workers do not share a status.
				go func(exp types.Experiment, app ifaces.ScenarioApp, duration time.Duration) {
					defer wg.Done()

					logStoreErr := func(err error) {
						if err != nil {
							plog.Error(
								plog.TypePhenixApp,
								"[✗] error updating store with experiment",
								"exp",
								exp.Metadata.Name,
								"err",
								err,
							)
						}
					}

					logStoreErr(exp.UpdateStatus(func(s ifaces.ExperimentStatus) {
						s.SetAppFrequency(app.Name(), app.RunPeriodically())
						s.SetAppRunning(app.Name(), false)
					}))

					timer := time.NewTimer(duration)

					for {
						select {
						case <-ctx.Done():
							timer.Stop()

							logStoreErr(exp.UpdateStatus(func(s ifaces.ExperimentStatus) {
								s.SetAppFrequency(app.Name(), "")
								s.SetAppRunning(app.Name(), false)
							}))

							return
						case <-timer.C:
							// Skip the tick when a manual trigger has the flag in the store.
							claimed, err := claimRunning(&exp, app.Name())
							logStoreErr(err)

							if claimed {
								a := GetApp(app.Name())
								_ = a.Init(Name(app.Name()))

								pubsub.Publish("trigger-app", TriggerPublication{ //nolint:exhaustruct // partial initialization
									Experiment: exp.Spec.ExperimentName(),
									App:        app.Name(),
									State:      "start",
								})

								err = a.Running(ctx, &exp)
							}

							if !claimed || errors.Is(err, ErrAlreadyRunning) {
								plog.Info(
									plog.TypePhenixApp,
									"[✓] app is currently already executing its running stage -- skipping",
									"app",
									app.Name(),
								)

								timer.Reset(duration)

								continue
							}

							if err != nil {
								plog.Error(
									plog.TypePhenixApp,
									"[✗] error periodically running app",
									"app",
									app.Name(),
									"err",
									err,
								)
							}

							publishRunResult(exp.Spec.ExperimentName(), app.Name(), err)

							logStoreErr(releaseRunning(&exp, app.Name()))

							timer.Reset(duration)
						}
					}
				}(*exp, app, duration)
			}
		}
	}
}

// stageDuration returns the time since begun to the millisecond, as text such as 1.25s.
func stageDuration(begun time.Time) string {
	return time.Since(begun).Round(time.Millisecond).String()
}
