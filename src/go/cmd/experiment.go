package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/scorch/scorchexe"
	"phenix/app"
	"phenix/scheduler"
	"phenix/types"
	"phenix/util"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/printer"
	"phenix/util/sigterm"

	"github.com/spf13/cobra"
)

func newExperimentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "experiment",
		Aliases: []string{"exp"},
		Short:   "Experiment management",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newExperimentListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Display a table of available experiments",
		RunE: func(cmd *cobra.Command, args []string) error {
			exps, err := experiment.List()
			if err != nil {
				err := util.HumanizeError(err, "Unable to list known experiments")
				return err.Humanized()
			}

			if len(exps) == 0 {
				plog.Warn("no experiments available")
			} else {
				printer.PrintTableOfExperiments(os.Stdout, exps...)
			}

			return nil
		},
	}

	return cmd
}

func newExperimentAppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "List of available apps to assign an experiment",
		RunE: func(cmd *cobra.Command, args []string) error {
			apps := app.List()

			if len(apps) == 0 {
				plog.Warn("no apps available")
				return nil
			}

			fmt.Printf("\nApps: %s\n", strings.Join(apps, ", "))

			return nil
		},
	}

	return cmd
}

func newExperimentSchedulersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedulers",
		Short: "List of available schedulers to assign an experiment",
		RunE: func(cmd *cobra.Command, args []string) error {
			schedulers := scheduler.List()

			if len(schedulers) == 0 {
				plog.Warn("no schedulers available")
				return nil
			}

			fmt.Printf("\nSchedulers: %s\n", strings.Join(schedulers, ", "))

			return nil
		},
	}

	return cmd
}

func newExperimentCreateCmd() *cobra.Command {
	desc := `Create an experiment

  Used to create an experiment from existing configurations; can be a
  topology, or topology and scenario, or paths to topology/scenario
  configuration files (YAML or JSON). (Optional are the arguments for
  scenario or base directory.)`

	example := `
  phenix experiment create <experiment name> -t <topology name or /path/to/filename>
  phenix experiment create <experiment name> -t <topology name or /path/to/filename> -s <scenario name or /path/to/filename>
  phenix experiment create <experiment name> -t <topology name or /path/to/filename> -s <scenario name or /path/to/filename> -d </path/to/dir/>
  phenix experiment create <experiment name> -t <topology name or /path/to/filename> -s <scenario naem or /path/to/filename> --disabled-apps "app1,app2"`

	cmd := &cobra.Command{
		Use:     "create <experiment name>",
		Short:   "Create an experiment",
		Long:    desc,
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("must provide an experiment name")
			}

			var (
				topology = MustGetString(cmd.Flags(), "topology")
				scenario = MustGetString(cmd.Flags(), "scenario")
			)

			if ext := filepath.Ext(topology); ext != "" {
				opts := []config.CreateOption{config.CreateFromPath(topology), config.CreateWithValidation()}

				c, err := config.Create(opts...)
				if err != nil {
					err := util.HumanizeError(err, "Unable to create configuration from "+topology)
					return err.Humanized()
				}

				topology = c.Metadata.Name
			}

			// If scenario is not provided, then ext will be an empty string, so the
			// following won't be run.
			if ext := filepath.Ext(scenario); ext != "" {
				opts := []config.CreateOption{config.CreateFromPath(scenario), config.CreateWithValidation()}

				c, err := config.Create(opts...)
				if err != nil {
					err := util.HumanizeError(err, "Unable to create configuration from "+scenario)
					return err.Humanized()
				}

				scenario = c.Metadata.Name
			}

			disabledApps, err := cmd.Flags().GetStringSlice("disabled-apps")
			if err != nil {
				err := util.HumanizeError(err, "Bad list of disabled-apps provided: %v", disabledApps)
				return err.Humanized()
			}
			for idx := range disabledApps {
				disabledApps[idx] = strings.TrimSpace(disabledApps[idx])
			}

			opts := []experiment.CreateOption{
				experiment.CreateWithName(args[0]),
				experiment.CreateWithTopology(topology),
				experiment.CreateWithScenario(scenario),
				experiment.CreateWithBaseDirectory(MustGetString(cmd.Flags(), "base-dir")),
				experiment.CreateWithVLANMin(MustGetInt(cmd.Flags(), "vlan-min")),
				experiment.CreateWithVLANMax(MustGetInt(cmd.Flags(), "vlan-max")),
				experiment.CreatedWithDisabledApplications(disabledApps),
			}

			ctx := notes.Context(context.Background(), false)

			if err := experiment.Create(ctx, opts...); err != nil {
				err := util.HumanizeError(err, "Unable to create the "+args[0]+" experiment")
				return err.Humanized()
			}

			notes.PrettyPrint(ctx, false)

			plog.Info("experiment created", "exp", args[0])

			return nil
		},
	}

	cmd.Flags().StringP("topology", "t", "", "Name of an existing topology to use")
	cmd.MarkFlagRequired("topology")
	cmd.Flags().StringP("scenario", "s", "", "Name of an existing scenario to use (optional)")
	cmd.Flags().StringP("base-dir", "d", "", "Base directory to use for experiment (optional)")
	cmd.Flags().Int("vlan-min", 0, "VLAN pool minimum")
	cmd.Flags().Int("vlan-max", 0, "VLAN pool maximum")
	cmd.Flags().StringSlice("disabled-apps", []string{}, "Comma separated ist of apps to disable")
	return cmd
}

func newExperimentEditCmd() *cobra.Command {
	desc := `Edit an experiment

  This subcommand is used to edit an experiment using your default editor.
	`

	cmd := &cobra.Command{
		Use:   "edit <experiment name>",
		Short: "Edit an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				force = MustGetBool(cmd.Flags(), "force")
				exp   = fmt.Sprintf("experiment/%s", args[0])
			)

			_, err := config.Edit(exp, force)
			if err != nil {
				if config.IsConfigNotModified(err) {
					fmt.Printf("The %s experiment was not updated\n", args[0])
					return nil
				}

				err := util.HumanizeError(err, "Unable to edit the %s experiment", args[0])
				return err.Humanized()
			}

			fmt.Printf("The %s experiment was updated\n", args[0])

			return nil
		},
	}

	cmd.Flags().Bool("force", false, "override checks")

	return cmd
}

func newExperimentDeleteCmd() *cobra.Command {
	desc := `Delete an experiment

  Used to delete an exisitng experiment; experiment must be stopped.
  Using 'all' instead of a specific experiment name will include all 
  stopped experiments`

	cmd := &cobra.Command{
		Use:   "delete <experiment name>",
		Short: "Delete an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				experiments []types.Experiment
			)

			if name == "all" {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to delete all experiments")
					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "Unable to delete the "+name+" experiment")
					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if exp.Running() {
					plog.Warn("not deleting running experiment", "exp", exp.Metadata.Name)
					continue
				}

				if err := config.Delete("experiment/" + exp.Metadata.Name); err != nil {
					err := util.HumanizeError(err, "Unable to delete the "+exp.Metadata.Name+" experiment")
					fmt.Println(err.Humanize())
					continue
				}

				plog.Info("experiment deleted", "exp", exp.Metadata.Name)
			}

			return nil
		},
	}

	return cmd
}

func newExperimentScheduleCmd() *cobra.Command {
	desc := `Schedule an experiment
	
  Apply an algorithm to a given experiment. Run 'phenix experiment schedulers' 
  to return a list of algorithms`

	cmd := &cobra.Command{
		Use:   "schedule <experiment name> <algorithm>",
		Short: "Schedule an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := []experiment.ScheduleOption{
				experiment.ScheduleForName(args[0]),
				experiment.ScheduleWithAlgorithm(args[1]),
			}

			if err := experiment.Schedule(opts...); err != nil {
				err := util.HumanizeError(err, "Unable to schedule the "+args[0]+" experiment with the "+args[1]+" algorithm")
				return err.Humanized()
			}

			plog.Info("experiment scheduled", "exp", args[0], "algorithm", args[1])

			return nil
		},
	}

	return cmd
}

func newExperimentStartCmd() *cobra.Command {
	desc := `Start an experiment

  Used to start a stopped experiment, using 'all' instead of a specific 
  experiment name will include all stopped experiments; dry-run will do 
	everything but call out to minimega.
	
	NOTE: passing the --honor-run-periodically flag will prevent the CLI from
	returning. If Ctrl+c is pressed, the experiment will continue to run but
	the running stage will no longer continue to be triggered for any apps
	configured (via the scenario) to have their running stage triggered
	periodically.`

	cmd := &cobra.Command{
		Use:   "start <experiment name>",
		Short: "Start an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				dryrun      = MustGetBool(cmd.Flags(), "dry-run")
				periodic    = MustGetBool(cmd.Flags(), "honor-run-periodically")
				experiments []types.Experiment

				ctx = notes.Context(sigterm.CancelContext(context.Background()), true)
				wg  sync.WaitGroup
			)

			if name == "all" {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to start all experiments")
					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "Unable to start the "+name+" experiment")
					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if exp.Running() {
					plog.Warn("not starting already running experiment", "exp", exp.Metadata.Name)
					continue
				}

				opts := []experiment.StartOption{
					experiment.StartWithName(exp.Metadata.Name),
					experiment.StartWithDryRun(dryrun),
					experiment.StartWithVLANMin(MustGetInt(cmd.Flags(), "vlan-min")),
					experiment.StartWithVLANMax(MustGetInt(cmd.Flags(), "vlan-max")),
					experiment.StartWithMMErrorsAsWarnings(MustGetBool(cmd.Flags(), "treat-mm-errors-as-warnings")),
				}

				if err := experiment.Start(ctx, opts...); err != nil {
					err := util.HumanizeError(err, "Unable to start the "+exp.Metadata.Name+" experiment")
					return err.Humanized()
				}

				notes.PrettyPrint(ctx, false)

				plog.Info("experiment started", "exp", exp.Metadata.Name, "dryrun", dryrun)

				if periodic {
					plog.Info("honor-run-periodically flag was passed")

					if err := app.PeriodicallyRunApps(ctx, &wg, &exp); err != nil {
						plog.Error("scheduling experiment apps to run periodically", "err", err)
					}
				}
			}

			// If --honor-run-periodically was not passed, then this will return
			// immediately so no harm no foul. Otherwise, it will wait until all
			// Goroutines that are periodically running apps exit, which will be
			// triggered by the context being canceled via an OS signal.
			wg.Wait()

			return nil
		},
	}

	cmd.Flags().Bool("dry-run", false, "Do everything but actually call out to minimega")
	cmd.Flags().Bool("honor-run-periodically", false, "Periodically trigger running stage in apps if configured in scenario")
	cmd.Flags().Bool("treat-mm-errors-as-warnings", false, "Treat errors from minimega as warnings instead of failing")
	cmd.Flags().Int("vlan-min", 0, "VLAN pool minimum")
	cmd.Flags().Int("vlan-max", 0, "VLAN pool maximum")

	return cmd
}

func newExperimentStopCmd() *cobra.Command {
	desc := `Stop an experiment

  Used to stop a running experiment, using 'all' instead of a specific 
  experiment name will include all running experiments.`

	cmd := &cobra.Command{
		Use:   "stop <experiment name>",
		Short: "Stop an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				experiments []types.Experiment
			)

			if name == "all" {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to stop all experiments")
					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "Unable to stop the "+name+" experiment")
					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if !exp.Running() {
					plog.Warn("not stopping already stopped experiment", "exp", exp.Metadata.Name)
					continue
				}

				if err := experiment.Stop(exp.Metadata.Name); err != nil {
					err := util.HumanizeError(err, "Problem encountered while stopping the "+exp.Metadata.Name+" experiment")
					return err.Humanized()
				}

				plog.Info("experiment stopped", "exp", exp.Metadata.Name)
			}

			return nil
		},
	}

	return cmd
}

func newExperimentRestartCmd() *cobra.Command {
	desc := `Restart an experiment

  Used to restart a running experiment, using 'all' instead of a specific 
  experiment name will include all running experiments; dry-run will do 
  everything but call out to minimega.`

	cmd := &cobra.Command{
		Use:   "restart <experiment name>",
		Short: "Restart an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				dryrun      = MustGetBool(cmd.Flags(), "dry-run")
				experiments []types.Experiment

				ctx = sigterm.CancelContext(context.Background())
			)

			if name == "all" {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to restart all experiments")
					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "Unable to restart the "+name+" experiment")
					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if !exp.Running() {
					plog.Warn("not restarting stopped experiment", "exp", exp.Metadata.Name)
					continue
				}

				if err := experiment.Stop(exp.Metadata.Name); err != nil {
					err := util.HumanizeError(err, "Unable to stop the "+exp.Metadata.Name+" experiment")
					return err.Humanized()
				}

				if err := experiment.Start(ctx, experiment.StartWithName(exp.Metadata.Name), experiment.StartWithDryRun(dryrun)); err != nil {
					err := util.HumanizeError(err, "Unable to start the "+exp.Metadata.Name+" experiment")
					return err.Humanized()
				}

				plog.Info("experiment restarted", "exp", exp.Metadata.Name)
			}

			return nil
		},
	}

	cmd.Flags().Bool("dry-run", false, "Do everything but actually call out to minimega")

	return cmd
}

func newExperimentReconfigureCmd() *cobra.Command {
	desc := `Reconfigure an experiment

  Used to rerun the 'configure' stage of all the apps (both default and user)
  for the given experiment (as long as it's not running). Using 'all' instead
  of a specific experiment name will reconfigure all non-running
  experiments.`

	cmd := &cobra.Command{
		Use:   "reconfigure <experiment name>",
		Short: "Reconfigure an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				experiments []types.Experiment
			)

			if name == "all" {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to reconfigure all experiments")
					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "Unable to reconfigure the "+name+" experiment")
					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if exp.Running() {
					plog.Warn("not reconfiguring running experiment", "exp", exp.Metadata.Name)
					continue
				}

				if err := experiment.Reconfigure(exp.Metadata.Name); err != nil {
					err := util.HumanizeError(err, "Unable to reconfigure the "+exp.Metadata.Name+" experiment")
					return err.Humanized()
				}

				plog.Info("experiment reconfigured", "exp", exp.Metadata.Name)
			}

			return nil
		},
	}

	return cmd
}

func newExperimentTriggerRunningCmd() *cobra.Command {
	desc := `Trigger an app's "running" stage in an experiment

	Used to manually trigger the "running" stage of an app (or apps) for the
	given experiment on demand. Using 'all' instead of a specific experiment
	name will trigger the "running" stage of the given app(s) for all running
	experiments. Providing no apps will cause all apps for the experiment(s) to
	be run.`

	cmd := &cobra.Command{
		Use:   "trigger-running <experiment name> [<app name> ...]",
		Short: "Trigger running stage for app(s) in experiment",
		Long:  desc,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				experiments []types.Experiment

				ctx = sigterm.CancelContext(context.Background())
			)

			if name == "all" {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to trigger running stage for apps in all experiments")
					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "Unable to trigger the running stage for apps in the "+name+" experiment")
					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			ctx = app.SetContextTriggerCLI(ctx)

			for _, exp := range experiments {
				if !exp.Running() {
					plog.Warn("not triggering the running stage for apps in stopped experiment", "exp", exp.Metadata.Name)
					continue
				}

				if err := experiment.TriggerRunning(ctx, exp.Metadata.Name, args[1:]...); err != nil {
					err := util.HumanizeError(err, "Unable to trigger the running stage for apps in the "+exp.Metadata.Name+" experiment")
					return err.Humanized()
				}

				plog.Info("running stage triggered for apps", "exp", exp.Metadata.Name)
			}

			return nil
		},
	}

	return cmd
}

func newExperimentScorchCmd() *cobra.Command {
	desc := `Start a Scorch run for an experiment

	Used to start a Scorch run with the given run ID for the given experiment. By
	default, the run ID used will be 0 if not provided.`

	cmd := &cobra.Command{
		Use:   "scorch <experiment name>",
		Short: "Start a Scorch run for experiment",
		Long:  desc,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name = args[0]
				run  = MustGetInt(cmd.Flags(), "run")
				ctx  = sigterm.CancelContext(context.Background())
			)

			exp, err := experiment.Get(name)
			if err != nil {
				err := util.HumanizeError(err, fmt.Sprintf("Unable to start Scorch run %d for %s experiment", run, exp.Metadata.Name))
				return err.Humanized()
			}

			ctx = app.SetContextTriggerCLI(ctx)

			if err := scorchexe.Execute(ctx, exp, run); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}

				err := util.HumanizeError(err, fmt.Sprintf("Unable to start Scorch run %d for %s experiment", run, exp.Metadata.Name))
				return err.Humanized()
			}

			plog.Info("scorch run triggered", "exp", exp.Metadata.Name, "run", run)

			return nil
		},
	}

	cmd.Flags().IntP("run", "r", 0, "ID of Scorch run to start for experiment (defaults to 0)")

	return cmd
}

func init() {
	experimentCmd := newExperimentCmd()

	experimentCmd.AddCommand(newExperimentListCmd())
	experimentCmd.AddCommand(newExperimentAppsCmd())
	experimentCmd.AddCommand(newExperimentSchedulersCmd())
	experimentCmd.AddCommand(newExperimentCreateCmd())
	experimentCmd.AddCommand(newExperimentEditCmd())
	experimentCmd.AddCommand(newExperimentDeleteCmd())
	experimentCmd.AddCommand(newExperimentScheduleCmd())
	experimentCmd.AddCommand(newExperimentStartCmd())
	experimentCmd.AddCommand(newExperimentStopCmd())
	experimentCmd.AddCommand(newExperimentRestartCmd())
	experimentCmd.AddCommand(newExperimentReconfigureCmd())
	experimentCmd.AddCommand(newExperimentTriggerRunningCmd())
	experimentCmd.AddCommand(newExperimentScorchCmd())

	rootCmd.AddCommand(experimentCmd)
}
