package cmd

import (
	"fmt"
	"os"
	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/app"
	"phenix/scheduler"
	"phenix/util"
	"strings"

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
				fmt.Println("\nThere are no experiments available\n")
			} else {
				util.PrintTableOfExperiments(os.Stdout, exps...)
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
				fmt.Printf("\nApps: none\n\n")
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
				fmt.Printf("\nSchedulers: none\n\n")
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

  Used to create an experiment from an existing configuration; can be a 
  topology, or topology and scenario. (Optional are the arguments for scenario 
  or base directory.)`

	example := `
  phenix experiment create <experiment name> -t <topology name>
  phenix experiment create <experiment name> -t <topology name> -s <scenario name>
  phenix experiment create <experiment name> -t <topology name> -s <scenario name> -d </path/to/dir/>`

	cmd := &cobra.Command{ //need to check args
		Use:     "create <experiment name>",
		Short:   "Create an experiment",
		Long:    desc,
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("Must provide an experiment name") // this does not work because of topology requirement
			}

			opts := []experiment.CreateOption{
				experiment.CreateWithName(args[0]),
				experiment.CreateWithTopology(MustGetString(cmd.Flags(), "topology")),
				experiment.CreateWithScenario(MustGetString(cmd.Flags(), "scenario")),
				experiment.CreateWithBaseDirectory(MustGetString(cmd.Flags(), "base-dir")),
			}

			if err := experiment.Create(opts...); err != nil {
				err := util.HumanizeError(err, "Unable to create the "+args[0]+" experiment")
				return err.Humanized()
			}

			fmt.Printf("The %s experiment was created\n", args[0])

			return nil
		},
	}

	cmd.Flags().StringP("topology", "t", "", "Name of an existing topology to use")
	cmd.MarkFlagRequired("topology")
	cmd.Flags().StringP("scenario", "s", "", "Name of an existing scenario to use (optional)")
	cmd.Flags().StringP("base-dir", "d", "", "Base directory to use for experiment (optional)")

	return cmd
}

func newExperimentDeleteCmd() *cobra.Command {
	desc := `Delete an experiment

  Used to delete an exisitng experiment; experiment must be stopped`

	cmd := &cobra.Command{
		Use:   "delete <experiment name>",
		Short: "Delete an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0] // errors out without line 153, cobra error is not very informative

			exp, err := experiment.Get(name)
			if err != nil {
				err := util.HumanizeError(err, "Unable to get the "+name+" experiment")
				return err.Humanized()
			}

			if exp.Status.Running() {
				return fmt.Errorf("Cannot delete a running experiment")
			}

			if err := config.Delete("experiment/" + name); err != nil {
				err := util.HumanizeError(err, "Unable to delete the "+name+" experiment")
				return err.Humanized()
			}

			fmt.Printf("The %s experiment was deleted\n", name)

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

			fmt.Printf("The %s experiment was scheduled with %s\n", args[0], args[1])

			return nil
		},
	}

	return cmd
}

func newExperimentStartCmd() *cobra.Command {
	desc := `Start an experiment

  Used to start a stopped experiment, using 'all' instead of a specific 
  experiment name will include all experiments; dry-run will do everything but 
  call out to minimega.`

	cmd := &cobra.Command{
		Use:   "start <experiment name>",
		Short: "Start an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				dryrun      = MustGetBool(cmd.Flags(), "dry-run")
				experiments []string
			)

			if args[0] == "all" {
				exps, err := experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to start all experiments") // did not return in testing; do we want to test for length of experiments list?
					return err.Humanized()
				}

				for _, exp := range exps {
					if exp.Status.StartTime == "" {
						experiments = append(experiments, exp.Spec.ExperimentName)
					}
				}
			} else {
				experiments = []string{args[0]}
			}

			for _, exp := range experiments {
				if err := experiment.Start(exp, dryrun); err != nil {
					err := util.HumanizeError(err, "Unable to start the "+exp+" experiment")
					return err.Humanized()
				}

				if dryrun {
					fmt.Printf("The %s experiment was started in a dry-run\n", exp)
				} else {
					fmt.Printf("The %s experiment was started\n", exp)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolP("dry-run", "", false, "Do everything but actually call out to minimega")

	return cmd
}

func newExperimentStopCmd() *cobra.Command {
	desc := `Stop an experiment

  Used to stop a running experiment, using 'all' instead of a specific 
  experiment name will include all experiments.`

	cmd := &cobra.Command{
		Use:   "stop <experiment name>",
		Short: "Stop an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var experiments []string

			if args[0] == "all" {
				exps, err := experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to stop all experiments")
					return err.Humanized()
				}

				for _, exp := range exps {
					if exp.Status.StartTime != "" {
						experiments = append(experiments, exp.Spec.ExperimentName)
					}
				}
			} else {
				experiments = []string{args[0]}
			}

			for _, exp := range experiments {
				if err := experiment.Stop(exp); err != nil {
					err := util.HumanizeError(err, "Unable to stop the "+exp+" experiment")
					return err.Humanized()
				}

				fmt.Printf("The %s experiment was stopped\n", exp)
			}

			return nil
		},
	}

	return cmd
}

func newExperimentRestartCmd() *cobra.Command {
	desc := `Restart an experiment

  Used to restart a running experiment; dry-run will do everything but call out 
  to minimega.`

	cmd := &cobra.Command{
		Use:   "restart <experiment name>",
		Short: "Start an experiment",
		Long:  desc,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				exp    = args[0]
				dryrun = MustGetBool(cmd.Flags(), "dry-run")
			)

			if err := experiment.Stop(exp); err != nil {
				err := util.HumanizeError(err, "Unable to stop the "+exp+" experiment")
				return err.Humanized()
			}

			if err := experiment.Start(exp, dryrun); err != nil {
				err := util.HumanizeError(err, "Unable to start the "+exp+" experiment")
				return err.Humanized()
			}

			fmt.Printf("The %s experiment was restarted\n", exp)

			return nil
		},
	}

	cmd.Flags().BoolP("dry-run", "", false, "Do everything but actually call out to minimega")

	return cmd
}

func init() {
	experimentCmd := newExperimentCmd()

	experimentCmd.AddCommand(newExperimentListCmd())
	experimentCmd.AddCommand(newExperimentAppsCmd())
	experimentCmd.AddCommand(newExperimentSchedulersCmd())
	experimentCmd.AddCommand(newExperimentCreateCmd())
	experimentCmd.AddCommand(newExperimentDeleteCmd())
	experimentCmd.AddCommand(newExperimentScheduleCmd())
	experimentCmd.AddCommand(newExperimentStartCmd())
	experimentCmd.AddCommand(newExperimentStopCmd())
	experimentCmd.AddCommand(newExperimentRestartCmd())

	rootCmd.AddCommand(experimentCmd)
}