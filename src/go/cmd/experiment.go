package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/experiment/startopts"
	"phenix/api/scorch/scorchexe"
	"phenix/app"
	"phenix/scheduler"
	"phenix/store"
	"phenix/types"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/printer"
	"phenix/util/sigterm"
)

const allExperiments = "all"
const scheduleArgs = 2
const triggerArgs = 2

func newExperimentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     configKindExperiment,
		Aliases: []string{"exp"},
		Short:   "Experiment management",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

// experimentListRow is one experiment of phenix experiment list -o json or
// -o yaml. Its keys are those of GET /experiments.
type experimentListRow struct {
	Name        string   `json:"name"        yaml:"name"`
	Topology    string   `json:"topology"    yaml:"topology"`
	Scenario    string   `json:"scenario"    yaml:"scenario"`
	StartTime   string   `json:"start_time"  yaml:"start_time"`
	Running     bool     `json:"running"     yaml:"running"`
	Interrupted string   `json:"interrupted" yaml:"interrupted"`
	VMCount     int      `json:"vm_count"    yaml:"vm_count"`
	VLANCount   int      `json:"vlan_count"  yaml:"vlan_count"`
	Apps        []string `json:"apps"        yaml:"apps"`
}

// experimentListRows returns the rows of exps, with the values of the table
// of phenix experiment list.
func experimentListRows(exps []types.Experiment) []experimentListRow {
	rows := make([]experimentListRow, 0, len(exps))

	for _, exp := range exps {
		apps := make([]string, 0, len(exp.Apps()))
		for _, a := range exp.Apps() {
			apps = append(apps, a.Name())
		}

		row := experimentListRow{
			Name:        exp.Spec.ExperimentName(),
			Topology:    exp.Metadata.Annotations["topology"],
			Scenario:    exp.Metadata.Annotations["scenario"],
			StartTime:   exp.Status.StartTime(),
			Running:     exp.Running(),
			Interrupted: "",
			VMCount:     0,
			VLANCount:   0,
			Apps:        apps,
		}

		if exp.Running() {
			row.Interrupted = exp.Status.Interrupted()
		}

		if topo := exp.Spec.Topology(); topo != nil {
			row.VMCount = len(topo.Nodes())
		}

		if vlans := exp.Spec.VLANs(); vlans != nil {
			row.VLANCount = len(vlans.Aliases())
		}

		rows = append(rows, row)
	}

	return rows
}

func newExperimentListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   commandList,
		Short: "Display a table of available experiments",
		Args:  argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			exps, err := experiment.List()
			if err != nil {
				err := util.HumanizeError(err, "Unable to list known experiments")

				return err.Humanized()
			}

			return out.print(cmd, experimentListRows(exps), outputViews{
				formatTable: func(w io.Writer) error {
					if len(exps) == 0 {
						plog.Warn(plog.TypeSystem, "no experiments available")

						return nil
					}

					printer.PrintTableOfExperiments(w, outputPalette(w), exps...)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML})

	return cmd
}

func newExperimentAppsCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "apps",
		Short: "List the apps that a scenario can add to an experiment",
		Long: `List the apps that a scenario can add to an experiment

  Lists the apps that phenix includes and the phenix-app-<name> executables
  on this host. The default apps of each experiment are not in the list.`,
		Args: argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			apps := app.List()

			return out.printAPI(cmd, apps, outputViews{
				formatText: func(w io.Writer) error {
					if len(apps) == 0 {
						plog.Warn(plog.TypeSystem, "no apps available")

						return nil
					}

					_, err := fmt.Fprintf(w, "Apps: %s\n", strings.Join(apps, ", "))

					return err //nolint:wrapcheck // the writer's own error
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatText, []string{formatText, FormatJSON, FormatYAML})

	return cmd
}

func newExperimentSchedulersCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "schedulers",
		Short: "List the schedulers that put the VMs of an experiment on cluster hosts",
		Args:  argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			schedulers := scheduler.Available()

			return out.printAPI(cmd, schedulers, outputViews{
				formatText: func(w io.Writer) error {
					if len(schedulers) == 0 {
						plog.Warn(plog.TypeSystem, "no schedulers available")

						return nil
					}

					printSchedulers(w, schedulers)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatText, []string{formatText, FormatJSON, FormatYAML})

	return cmd
}

// schedulerDescriptionWidth is the most characters on one line of a
// scheduler's description, after its indent.
const schedulerDescriptionWidth = 72

// printSchedulers prints each scheduler's name and kind, then what it does,
// wrapped and indented below the name.
func printSchedulers(out io.Writer, schedulers []scheduler.Info) {
	for _, info := range schedulers {
		kind, text := "built-in", info.Description
		if !info.Builtin {
			kind = "user"
			text = "Runs phenix-scheduler-" + info.Name + " on this host."
		}

		fmt.Fprintf(out, "%s (%s)\n", info.Name, kind)

		line := ""

		for word := range strings.FieldsSeq(text) {
			if line != "" && len(line)+1+len(word) > schedulerDescriptionWidth {
				fmt.Fprintf(out, "    %s\n", line)

				line = ""
			}

			line = strings.TrimSpace(line + " " + word)
		}

		if line != "" {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}
}

//nolint:funlen // command definition
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
  phenix experiment create <experiment name> -t <topology name or /path/to/filename> -s <scenario name or /path/to/filename> --disabled-apps "app1,app2"
  phenix experiment create <experiment name> -t <topology name> --node-annotation phenix/default-apps=false --annotation phenix.workflow/tags=nightly`

	cmd := &cobra.Command{
		Use:     "create <experiment name>",
		Short:   "Create an experiment",
		Long:    desc,
		Example: example,
		Args:    argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				topology = mustGet(cmd.Flags().GetString, "topology")
				scenario = mustGet(cmd.Flags().GetString, "scenario")
			)

			if ext := filepath.Ext(topology); ext != "" {
				opts := []config.CreateOption{
					config.CreateFromPath(topology),
					config.CreateWithValidation(),
				}

				c, err := config.Create(opts...)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to create configuration from "+topology,
					)

					return err.Humanized()
				}

				topology = c.Metadata.Name
			}

			// If scenario is not provided, then ext will be an empty string, so the
			// following won't be run.
			if ext := filepath.Ext(scenario); ext != "" {
				opts := []config.CreateOption{
					config.CreateFromPath(scenario),
					config.CreateWithValidation(),
				}

				c, err := config.Create(opts...)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to create configuration from "+scenario,
					)

					return err.Humanized()
				}

				scenario = c.Metadata.Name
			}

			disabledApps, err := cmd.Flags().GetStringSlice("disabled-apps")
			if err != nil {
				err := util.HumanizeError(
					err,
					"Bad list of disabled-apps provided: %v",
					disabledApps,
				)

				return err.Humanized()
			}

			for idx := range disabledApps {
				disabledApps[idx] = strings.TrimSpace(disabledApps[idx])
			}

			annotations, err := parseAnnotationFlags(mustGet(cmd.Flags().GetStringArray, "annotation"))
			if err != nil {
				return util.HumanizeError(err, "").Humanized()
			}

			nodeAnnotations, err := parseNodeAnnotationFlags(
				mustGet(cmd.Flags().GetStringArray, "node-annotation"),
			)
			if err != nil {
				return util.HumanizeError(err, "").Humanized()
			}

			opts := []experiment.CreateOption{
				experiment.CreateWithName(args[0]),
				experiment.CreateWithTopology(topology),
				experiment.CreateWithScenario(scenario),
				experiment.CreateWithBaseDirectory(mustGet(cmd.Flags().GetString, "base-dir")),
				experiment.CreateWithVLANMin(mustGet(cmd.Flags().GetInt, "vlan-min")),
				experiment.CreateWithVLANMax(mustGet(cmd.Flags().GetInt, "vlan-max")),
				experiment.CreatedWithDisabledApplications(disabledApps),
				experiment.CreateWithDefaultBridge(mustGet(cmd.Flags().GetString, "default-bridge")),
				experiment.CreateWithAnnotations(annotations),
				experiment.CreateWithNodeAnnotations(nodeAnnotations),
			}

			ctx := notes.Context(context.Background(), false)

			if err := experiment.Create(opts...); err != nil {
				err := util.HumanizeError(err, "%s", "Unable to create the "+args[0]+" experiment")

				return err.Humanized()
			}

			notes.PrettyPrint(ctx, false)

			plog.Info(plog.TypeSystem, "experiment created", "exp", args[0])

			return nil
		},
	}

	cmd.Flags().StringP("topology", "t", "", "Name of an existing topology to use")
	_ = cmd.MarkFlagRequired("topology")
	cmd.Flags().StringP("scenario", "s", "", "Name of an existing scenario to use (optional)")
	cmd.Flags().StringP("base-dir", "d", "", "Base directory to use for experiment (optional)")
	cmd.Flags().
		StringP("default-bridge", "b", appName, "Default bridge name to use for experiment (optional)")
	cmd.Flags().Int("vlan-min", 0, "VLAN pool minimum")
	cmd.Flags().Int("vlan-max", 0, "VLAN pool maximum")
	cmd.Flags().StringSlice("disabled-apps", []string{}, "Comma separated list of apps to disable")
	cmd.Flags().StringArray(
		"annotation",
		nil,
		"Experiment annotation as key=value; may be repeated",
	)
	cmd.Flags().StringArray(
		"node-annotation",
		nil,
		"Annotation for every VM as key=value; true, false and JSON lists or objects keep their type; may be repeated",
	)

	_ = cmd.RegisterFlagCompletionFunc("topology", completeConfigNamesOrFiles("Topology"))
	_ = cmd.RegisterFlagCompletionFunc("scenario", completeConfigNamesOrFiles("Scenario"))
	_ = cmd.RegisterFlagCompletionFunc("disabled-apps", completeAppNames)
	_ = cmd.MarkFlagDirname("base-dir")

	return cmd
}

// parseAnnotationFlags turns key=value flag values into annotations.
func parseAnnotationFlags(values []string) (map[string]string, error) {
	annotations := make(map[string]string, len(values))

	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("annotation %q is not of the form key=value", value)
		}

		annotations[key] = val
	}

	return annotations, nil
}

// parseNodeAnnotationFlags turns key=value flag values into node annotations,
// keeping the type of values the default apps read as booleans or lists.
func parseNodeAnnotationFlags(values []string) (map[string]any, error) {
	flat, err := parseAnnotationFlags(values)
	if err != nil {
		return nil, err
	}

	annotations := make(map[string]any, len(flat))

	for key, value := range flat {
		annotations[key] = nodeAnnotationValue(value)
	}

	return annotations, nil
}

// nodeAnnotationValue reads true and false as booleans and a JSON list or
// object as that value; anything else, numbers included, stays a string.
func nodeAnnotationValue(value string) any {
	switch trimmed := strings.TrimSpace(value); {
	case trimmed == "true":
		return true
	case trimmed == "false":
		return false
	case strings.HasPrefix(trimmed, "["), strings.HasPrefix(trimmed, "{"):
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return parsed
		}
	}

	return value
}

func newExperimentEditCmd() *cobra.Command {
	desc := `Edit an experiment

  This subcommand is used to edit an experiment using your default editor.
	`

	cmd := &cobra.Command{
		Use:               "edit <experiment name>",
		Short:             "Edit an experiment",
		Long:              desc,
		ValidArgsFunction: expNameCompletion(false),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				force = mustGet(cmd.Flags().GetBool, "force")
				exp   = "experiment/" + args[0]
			)

			_, err := config.Edit(exp, force)
			if err != nil {
				if config.IsConfigNotModified(err) {
					plog.Warn(plog.TypeSystem, "experiment not updated", "exp", args[0])

					return nil
				}

				err := util.HumanizeError(err, "Unable to edit the %s experiment", args[0])

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "experiment updated", "exp", args[0])

			return nil
		},
	}

	cmd.Flags().Bool("force", false, "override checks")

	return cmd
}

func newExperimentDeleteCmd() *cobra.Command {
	desc := `Delete an experiment

  Used to delete an existing experiment; experiment must be stopped.
  Using 'all' instead of a specific experiment name will include all
  stopped experiments. Use the -f/--force flag to automatically stop
  a running experiment before deleting it.

  When phenix cannot stop or delete an experiment, it continues with the
  other experiments. Then it exits with an error that names each failure.

  With --dry-run, phenix shows the experiments that it would stop and
  delete, and changes nothing.`

	var dry dryRun

	cmd := &cobra.Command{
		Use:               "delete <experiment name>",
		Aliases:           []string{deleteAlias},
		Short:             "Delete an experiment",
		Long:              desc,
		ValidArgsFunction: expNameCompletion(true),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				force       = mustGet(cmd.Flags().GetBool, "force")
				experiments []types.Experiment
			)

			if name == allExperiments {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to delete all experiments")

					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "%s", "Unable to delete the "+name+" experiment")

					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			var failed []*util.HumanizedError

			for _, exp := range experiments {
				if exp.Running() {
					if !force {
						plog.Warn(
							plog.TypeSystem,
							"not deleting running experiment",
							"exp",
							exp.Metadata.Name,
						)

						continue
					}

					if err := stopBeforeDelete(dry, exp.Metadata.Name); err != nil {
						plog.Error(plog.TypeSystem, err.Humanize())
						failed = append(failed, err)

						continue
					}
				}

				if dry.skip("delete", "experiment "+exp.Metadata.Name) {
					continue
				}

				err := config.Delete("experiment/" + exp.Metadata.Name)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to delete the "+exp.Metadata.Name+" experiment",
					)
					plog.Error(plog.TypeSystem, err.Humanize())
					failed = append(failed, err)

					continue
				}

				plog.Info(plog.TypeSystem, "experiment deleted", "exp", exp.Metadata.Name)
			}

			return deleteFailures(failed, "experiments")
		},
	}

	cmd.Flags().BoolP("force", "f", false, "Stop a running experiment before deleting it")
	dry = addDryRunFlag(cmd)

	return cmd
}

// stopBeforeDelete stops the running experiment name that experiment delete
// --force deletes, or shows the stop in a dry run. It stops the experiment
// even when minimega cannot kill its VMs, as docker rm -f does.
func stopBeforeDelete(dry dryRun, name string) *util.HumanizedError {
	if dry.skip("stop", "experiment "+name) {
		return nil
	}

	if err := experiment.Stop(name, experiment.StopWithForce(true)); err != nil {
		return util.HumanizeError(err, "%s", "Unable to stop the "+name+" experiment before deleting it")
	}

	plog.Info(plog.TypeSystem, "experiment stopped", "exp", name)

	return nil
}

// deleteFailures is the error of an experiment delete in which the experiments of failed could not be stopped or
// deleted: nil for none, the one error for one, and an error that names each failure for more.
func deleteFailures(failed []*util.HumanizedError, what string) error {
	switch len(failed) {
	case 0:
		return nil
	case 1:
		return failed[0].Humanized()
	}

	msgs := make([]string, 0, len(failed))
	for _, err := range failed {
		msgs = append(msgs, err.Humanize())
	}

	return fmt.Errorf("unable to delete %d %s:\n  %s", len(failed), what, strings.Join(msgs, "\n  "))
}

func newExperimentScheduleCmd() *cobra.Command {
	desc := `Schedule an experiment

  Apply an algorithm to a given experiment. Run 'phenix experiment schedulers'
  to return a list of algorithms`

	cmd := &cobra.Command{
		Use:   "schedule <experiment name> <algorithm>",
		Short: "Schedule an experiment",
		Long:  desc,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return expNameCompletion(false)(cmd, args, toComplete)
			}
			if len(args) == 1 {
				scheds := scheduler.List()
				var matches []string
				for _, s := range scheds {
					if strings.HasPrefix(s, toComplete) {
						matches = append(matches, s)
					}
				}
				return matches, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		Args: argsWithUsage(cobra.ExactArgs(scheduleArgs)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := []experiment.ScheduleOption{
				experiment.ScheduleForName(args[0]),
				experiment.ScheduleWithAlgorithm(args[1]),
			}

			err := experiment.Schedule(opts...)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to schedule the "+args[0]+" experiment with the "+args[1]+" algorithm",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "experiment scheduled", "exp", args[0], "algorithm", args[1])

			return nil
		},
	}

	return cmd
}

//nolint:funlen // command definition
func newExperimentStartCmd() *cobra.Command {
	desc := `Start an experiment

  Used to start a stopped experiment, using 'all' instead of a specific
  experiment name will include all stopped experiments; dry-run will do
  everything but call out to minimega.

  Before minimega launches a VM, phenix checks that each disk image and
  CD-ROM image of the VMs exists, and that the backing chain of each disk
  image is complete. A missing file refuses the start and lists each VM,
  disk and missing file. With --dry-run or --treat-mm-errors-as-warnings,
  phenix shows the problems as warnings. The experiment.disk-check setting
  changes this for every start: warn shows the problems as warnings, and
  off skips the check.

  NOTE: passing the --honor-run-periodically flag will prevent the CLI from
  returning. If Ctrl+c is pressed, the experiment will continue to run but
  the running stage will no longer continue to be triggered for any apps
  configured (via the scenario) to have their running stage triggered
  periodically.`

	cmd := &cobra.Command{
		Use:               "start <experiment name>",
		Short:             "Start an experiment",
		Long:              desc,
		ValidArgsFunction: expNameCompletion(true),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				experiments []types.Experiment

				ctx = notes.Context(sigterm.CancelContext(context.Background()), true)
				wg  sync.WaitGroup
			)

			opts, err := startFlags(cmd)
			if err != nil {
				return err
			}

			dryrun, periodic := opts.DryRun, opts.Periodic(false)

			if name == allExperiments {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to start all experiments")

					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "%s", "Unable to start the "+name+" experiment")

					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if exp.Running() {
					plog.Warn(
						plog.TypeSystem,
						"not starting already running experiment",
						"exp",
						exp.Metadata.Name,
					)

					continue
				}

				begun := time.Now()

				err := experiment.Start(ctx, opts.StartOptions(exp.Metadata.Name)...)
				if err != nil {
					return startError(err, exp.Metadata.Name)
				}

				notes.PrettyPrint(ctx, false)

				plog.Info(
					plog.TypeSystem,
					"experiment started",
					"exp",
					exp.Metadata.Name,
					"dryrun",
					dryrun,
					"deploy-mode",
					exp.Spec.DeployMode(),
					"duration",
					time.Since(begun).Round(time.Millisecond).String(),
				)

				if periodic {
					plog.Info(plog.TypeSystem, "honor-run-periodically flag was passed")

					app.PeriodicallyRunApps(ctx, &wg, &exp)
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

	addStartFlags(cmd)

	return cmd
}

// addStartFlags adds the flags of experiment start, which startOptions reads.
func addStartFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, "Do everything but actually call out to minimega")
	cmd.Flags().
		Bool("honor-run-periodically", false, "Periodically trigger running stage in apps if configured in scenario")
	cmd.Flags().
		Bool("treat-mm-errors-as-warnings", false, "Treat errors from minimega as warnings instead of failing")
	cmd.Flags().Int("vlan-min", 0, "VLAN pool minimum for this start (1-4094; 0 keeps the minimum of the experiment)")
	cmd.Flags().Int("vlan-max", 0, "VLAN pool maximum for this start (1-4094; 0 keeps the maximum of the experiment)")
}

// startFlags are the start options of the flags that addStartFlags added to
// cmd, checked as POST /experiments/{name}/start checks its body.
func startFlags(cmd *cobra.Command) (startopts.Options, error) {
	periodic := mustGet(cmd.Flags().GetBool, "honor-run-periodically")

	opts := startopts.Options{
		DryRun:                        mustGet(cmd.Flags().GetBool, "dry-run"),
		TreatMinimegaErrorsAsWarnings: mustGet(cmd.Flags().GetBool, "treat-mm-errors-as-warnings"),
		HonorRunPeriodically:          &periodic,
		VLANMin:                       mustGet(cmd.Flags().GetInt, "vlan-min"),
		VLANMax:                       mustGet(cmd.Flags().GetInt, "vlan-max"),
	}

	if err := opts.Check(); err != nil {
		return startopts.Options{}, fmt.Errorf("--vlan-min, --vlan-max: %w", err)
	}

	return opts, nil
}

// addStopFlags adds the flags of experiment stop, which stopOptions reads.
func addStopFlags(cmd *cobra.Command) {
	cmd.Flags().
		BoolP("keep-injection-snapshots", "K", false, "Keep VM injection snapshots after stopping")
	cmd.Flags().
		BoolP("force", "f", false, "Mark the experiment stopped even if minimega cannot kill its VMs")
}

// stopOptions are the options of a stop, from the flags that addStopFlags
// added to cmd.
func stopOptions(cmd *cobra.Command) []experiment.StopOption {
	return []experiment.StopOption{
		experiment.StopWithKeepInjectionSnapshots(mustGet(cmd.Flags().GetBool, "keep-injection-snapshots")),
		experiment.StopWithForce(mustGet(cmd.Flags().GetBool, "force")),
	}
}

// stopError is the error of a stop of experiment name that failed with err,
// which begins with msg.
func stopError(err error, msg string) error {
	if errors.Is(err, experiment.ErrTeardownFailed) {
		msg += " (it is still running: try again, or pass --force to mark it stopped anyway)"
	}

	herr := util.HumanizeError(err, "%s", msg)

	return herr.Humanized()
}

// startError is the error a start of experiment name that failed with err returns: err itself for broken disk images,
// so the list of VMs, disks and missing files is printed, and for a start that another process runs. Otherwise it is
// a humanized error.
func startError(err error, name string) error {
	if errors.Is(err, experiment.ErrBrokenDisks) || errors.Is(err, experiment.ErrExperimentStarting) {
		return fmt.Errorf("unable to start the %s experiment: %w", name, err)
	}

	herr := util.HumanizeError(err, "%s", "Unable to start the "+name+" experiment")

	return herr.Humanized()
}

func newExperimentStopCmd() *cobra.Command {
	desc := `Stop an experiment

  Used to stop a running experiment, using 'all' instead of a specific
  experiment name will include all running experiments. An experiment whose
  VMs minimega cannot kill stays running; --force marks it stopped anyway.`

	cmd := &cobra.Command{
		Use:               "stop <experiment name>",
		Short:             "Stop an experiment",
		Long:              desc,
		ValidArgsFunction: expNameCompletion(true),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				experiments []types.Experiment
			)

			if name == allExperiments {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to stop all experiments")

					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(err, "%s", "Unable to stop the "+name+" experiment")

					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if !exp.Running() {
					plog.Warn(
						plog.TypeSystem,
						"not stopping already stopped experiment",
						"exp",
						exp.Metadata.Name,
					)

					continue
				}

				err := experiment.Stop(exp.Metadata.Name, stopOptions(cmd)...)
				if err != nil {
					return stopError(err, "Problem encountered while stopping the "+exp.Metadata.Name+" experiment")
				}

				plog.Info(plog.TypeSystem, "experiment stopped", "exp", exp.Metadata.Name)
			}

			return nil
		},
	}

	addStopFlags(cmd)

	return cmd
}

//nolint:funlen // command definition
func newExperimentRestartCmd() *cobra.Command {
	desc := `Restart an experiment

  Used to restart a running experiment, using 'all' instead of a specific
  experiment name will include all running experiments; dry-run will do
  everything but call out to minimega.

  Before phenix stops the experiment, it checks the disk images and CD-ROM
  images of the VMs as 'phenix experiment start' does. A missing file
  refuses the restart, and the experiment continues to run. When the
  scenario has a user app, the app can change the disks, so this check
  only shows warnings, and the check before the start can still refuse it.

  The stop of the experiment takes the flags of 'phenix experiment stop'
  (--force and --keep-injection-snapshots), and its start takes the flags
  of 'phenix experiment start'. With --treat-mm-errors-as-warnings, the
  check before the stop shows the problems as warnings.

  NOTE: passing the --honor-run-periodically flag will prevent the CLI from
  returning. If Ctrl+c is pressed, the experiment will continue to run but
  the running stage will no longer continue to be triggered for any apps
  configured (via the scenario) to have their running stage triggered
  periodically.`

	cmd := &cobra.Command{
		Use:               "restart <experiment name>",
		Aliases:           []string{restartAlias},
		Short:             "Restart an experiment",
		Long:              desc,
		ValidArgsFunction: expNameCompletion(true),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				experiments []types.Experiment

				ctx = notes.Context(sigterm.CancelContext(context.Background()), true)
				wg  sync.WaitGroup
			)

			opts, err := startFlags(cmd)
			if err != nil {
				return err
			}

			dryrun, periodic := opts.DryRun, opts.Periodic(false)
			warnOnly := dryrun || opts.TreatMinimegaErrorsAsWarnings

			if name == allExperiments {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to restart all experiments")

					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to restart the "+name+" experiment",
					)

					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if !exp.Running() {
					plog.Warn(
						plog.TypeSystem,
						"not restarting stopped experiment",
						"exp",
						exp.Metadata.Name,
					)

					continue
				}

				warnings, err := experiment.CheckDisksBeforeStop(ctx, exp.Spec, experiment.CheckAsWarnings(warnOnly))
				notes.AddWarnings(ctx, true, warnings...)

				if err != nil {
					return fmt.Errorf("unable to restart the %s experiment, which is still running: %w", exp.Metadata.Name, err)
				}

				err = experiment.Stop(exp.Metadata.Name, stopOptions(cmd)...)
				if err != nil {
					return stopError(err, "Unable to stop the "+exp.Metadata.Name+" experiment")
				}

				err = experiment.Start(ctx, opts.StartOptions(exp.Metadata.Name)...)
				if err != nil {
					return startError(err, exp.Metadata.Name)
				}

				notes.PrettyPrint(ctx, false)

				plog.Info(
					plog.TypeSystem,
					"experiment restarted",
					"exp",
					exp.Metadata.Name,
					"dryrun",
					dryrun,
					"deploy-mode",
					exp.Spec.DeployMode(),
				)

				if periodic {
					plog.Info(plog.TypeSystem, "honor-run-periodically flag was passed")

					app.PeriodicallyRunApps(ctx, &wg, &exp)
				}
			}

			// returns at once unless --honor-run-periodically started apps, as in experiment start
			wg.Wait()

			return nil
		},
	}

	addStopFlags(cmd)
	addStartFlags(cmd)

	return cmd
}

func newExperimentReconfigureCmd() *cobra.Command {
	desc := `Reconfigure an experiment

  Used to rerun the 'configure' stage of all the apps (both default and user)
  for the given experiment (as long as it's not running). Using 'all' instead
  of a specific experiment name will reconfigure all non-running
  experiments.

  This differs from 'phenix experiment trigger <experiment name> configure'
  in that it re-runs config validation and any registered config hooks, and
  always applies to all apps. Use this after editing an experiment's stored
  topology, scenario, or deployment settings. Use 'trigger configure' to
  manually re-run a specific app's (or apps') configure hook on demand for
  debugging; note it does not validate the config, run config hooks, or
  guard against running experiments the way 'reconfigure' does.`

	cmd := &cobra.Command{
		Use:               "reconfigure <experiment name>",
		Aliases:           []string{"rec"},
		Short:             "Reconfigure an experiment",
		Long:              desc,
		ValidArgsFunction: expNameCompletion(true),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name        = args[0]
				experiments []types.Experiment
			)

			if name == allExperiments {
				var err error

				experiments, err = experiment.List()
				if err != nil {
					err := util.HumanizeError(err, "Unable to reconfigure all experiments")

					return err.Humanized()
				}
			} else {
				exp, err := experiment.Get(name)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to reconfigure the "+name+" experiment",
					)

					return err.Humanized()
				}

				experiments = []types.Experiment{*exp}
			}

			for _, exp := range experiments {
				if exp.Running() {
					plog.Warn(
						plog.TypeSystem,
						"not reconfiguring running experiment",
						"exp",
						exp.Metadata.Name,
					)

					continue
				}

				err := experiment.Reconfigure(exp.Metadata.Name)
				if err != nil {
					err := util.HumanizeError(
						err,
						"%s",
						"Unable to reconfigure the "+exp.Metadata.Name+" experiment",
					)

					return err.Humanized()
				}

				plog.Info(plog.TypeSystem, "experiment reconfigured", "exp", exp.Metadata.Name)
			}

			return nil
		},
	}

	return cmd
}

// experimentTriggerCompletion completes the arguments of experiment trigger
// when stageArg is set, <experiment name> <lifecycle> [<app name> ...], or
// of experiment trigger-running otherwise, <experiment name> [<app name> ...].
// With experiment trigger --cancel, it completes the experiment's apps after
// the experiment name.
func experimentTriggerCompletion(
	stageArg bool,
) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return expNameCompletion(true)(cmd, nil, toComplete)
		}

		cancel := false
		if cmd != nil && cmd.Flags().Lookup("cancel") != nil {
			cancel = mustGet(cmd.Flags().GetBool, "cancel")
		}

		stage := app.ActionRunning

		if stageArg && !cancel {
			if len(args) == 1 {
				var matches []string

				for _, stage := range app.Actions() {
					for _, value := range []string{string(stage), stage.Shorthand()} {
						if strings.HasPrefix(value, toComplete) {
							matches = append(matches, value)
						}
					}
				}

				return matches, cobra.ShellCompDirectiveNoFileComp
			}

			var err error

			stage, err = app.ParseAction(args[1])
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
		}

		name := args[0]

		var candidates []string

		if name == allExperiments {
			candidates = append(app.DefaultApps(), app.List()...)
		} else {
			_ = store.Init(store.Endpoint(viper.GetString("store.endpoint")))

			exp, err := experiment.Get(name)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}

			candidates = app.ExperimentApps(exp)
		}

		var matches []string

		for _, a := range candidates {
			if !cancel && !app.StageApplicable(stage, a) {
				continue
			}

			if strings.HasPrefix(a, toComplete) {
				matches = append(matches, a)
			}
		}

		return matches, cobra.ShellCompDirectiveNoFileComp
	}
}

func triggerExperimentApps(
	ctx context.Context,
	stage app.Action,
	name string,
	apps []string,
) error {
	var experiments []types.Experiment

	if name == allExperiments {
		var err error

		experiments, err = experiment.List()
		if err != nil {
			err := util.HumanizeError(
				err,
				"%s",
				fmt.Sprintf("Unable to trigger the %s stage for apps in all experiments", stage),
			)

			return err.Humanized()
		}
	} else {
		exp, err := experiment.Get(name)
		if err != nil {
			err := util.HumanizeError(
				err,
				"%s",
				fmt.Sprintf(
					"Unable to trigger the %s stage for apps in the %s experiment",
					stage,
					name,
				),
			)

			return err.Humanized()
		}

		experiments = []types.Experiment{*exp}
	}

	ctx = app.SetContextTriggerCLI(ctx)

	var processedCount int

	for _, exp := range experiments {
		if stage == app.ActionRunning && !exp.Running() {
			plog.Warn(
				plog.TypeSystem,
				"not triggering the running stage for apps in stopped experiment",
				"exp",
				exp.Metadata.Name,
			)

			continue
		}

		err := experiment.Trigger(ctx, exp.Metadata.Name, stage, apps...)
		if err != nil {
			err := util.HumanizeError(
				err,
				"%s",
				fmt.Sprintf(
					"Unable to trigger the %s stage for apps in the %s experiment",
					stage,
					exp.Metadata.Name,
				),
			)

			return err.Humanized()
		}

		processedCount++

		plog.Info(
			plog.TypeSystem,
			fmt.Sprintf("%s stage triggered for apps", stage),
			"exp",
			exp.Metadata.Name,
		)
	}

	if processedCount == 0 && name == allExperiments && stage == app.ActionRunning {
		plog.Warn(
			plog.TypeSystem,
			"no running experiments found for trigger request",
		)
	}

	return nil
}

// triggerRequest is what the arguments of experiment trigger ask for.
type triggerRequest struct {
	experiment string
	stage      app.Action // empty for a cancel
	apps       []string
	// legacy is set when the lifecycle came before the experiment name
	legacy bool
}

// errTriggerCancelLifecycle is the error of a lifecycle argument with
// experiment trigger --cancel.
var errTriggerCancelLifecycle = errors.New(
	"--cancel takes no lifecycle: use phenix experiment trigger <experiment name> --cancel [<app name> ...]",
)

// checkTriggerArgs checks the number of arguments of experiment trigger, and
// that one of the first two is a lifecycle unless cancel is set. It does not
// use the store, which is not open yet when cobra checks arguments.
func checkTriggerArgs(args []string, cancel bool) error {
	if cancel {
		return cobra.MinimumNArgs(1)(nil, args) //nolint:wrapcheck // cobra's own argument error
	}

	if err := cobra.MinimumNArgs(triggerArgs)(nil, args); err != nil {
		return err //nolint:wrapcheck // cobra's own argument error
	}

	if _, err := app.ParseAction(args[0]); err == nil {
		return nil
	}

	_, err := app.ParseAction(args[1])

	return err //nolint:wrapcheck // the error names the argument and the stages
}

// parseTriggerArgs returns what the arguments of experiment trigger ask for:
// <experiment name> <lifecycle> [<app name> ...], or, with cancel, <experiment name> [<app name> ...].
// The old order, <lifecycle> <experiment name> [<app name> ...], is accepted when only the first
// argument is a lifecycle and isExperiment reports that no experiment has it as its name.
func parseTriggerArgs(args []string, cancel bool, isExperiment func(string) bool) (triggerRequest, error) {
	if err := checkTriggerArgs(args, cancel); err != nil {
		return triggerRequest{}, err
	}

	if cancel {
		if _, err := app.ParseAction(args[0]); err == nil && !isExperiment(args[0]) {
			return triggerRequest{}, errTriggerCancelLifecycle
		}

		for _, a := range args[1:] {
			if _, err := app.ParseAction(a); err == nil {
				return triggerRequest{}, errTriggerCancelLifecycle
			}
		}

		return triggerRequest{experiment: args[0], stage: "", apps: args[1:], legacy: false}, nil
	}

	first, firstErr := app.ParseAction(args[0])
	_, secondErr := app.ParseAction(args[1])

	if firstErr == nil && secondErr != nil && !isExperiment(args[0]) {
		return triggerRequest{experiment: args[1], stage: first, apps: args[2:], legacy: true}, nil
	}

	stage, err := app.ParseAction(args[1])
	if err != nil {
		return triggerRequest{}, err //nolint:wrapcheck // the error names the argument and the stages
	}

	return triggerRequest{experiment: args[0], stage: stage, apps: args[2:], legacy: false}, nil
}

// storedExperiment reports whether the store holds an experiment named name.
func storedExperiment(name string) bool {
	_, err := experiment.Get(name)

	return err == nil
}

// newOrderCommand is the experiment trigger command line of r in the
// current argument order.
func (r triggerRequest) newOrderCommand() string {
	return strings.Join(append([]string{"phenix experiment trigger", r.experiment, string(r.stage)}, r.apps...), " ")
}

func newExperimentTriggerCmd() *cobra.Command {
	desc := fmt.Sprintf(`Trigger an app lifecycle stage in an experiment

  Used to manually trigger the "configure", "pre-start", "post-start", "running",
  or "cleanup" stage of an app (or apps) for the given experiment on demand.
  Using 'all' instead of a specific experiment name triggers the stage for the
  given app(s) in all experiments. Providing no apps triggers the stage for all
  apps in the experiment(s). The "running" stage is skipped for stopped
  experiments.

  Triggering the "configure" stage is not the same as
  'phenix experiment reconfigure': this command does not validate the
  experiment config, run config hooks, or guard against running
  experiments. Use 'phenix experiment reconfigure' after
  editing an experiment's stored topology, scenario, or deployment settings.

  This command runs the stage in its own process. Ctrl+C stops a trigger
  that this command runs. --cancel stops the triggers that phenix ui runs,
  which the web UI or the REST API started: give no lifecycle with it, and
  give app names to stop only those apps. Without app names, --cancel also
  ends the scheduled runs of the running stage that phenix ui runs after a
  start (the experiments.periodic job). --cancel needs phenix ui. With
  --cancel --dry-run, phenix shows the cancels that it would request, and
  requests none.

  The old argument order, <lifecycle> <experiment name> [<app name> ...],
  is deprecated. It still works, with a warning, when no experiment has the
  lifecycle as its name.

  Valid lifecycles and shorthands: %s.`, app.ActionChoices())

	validateArgs := func(cmd *cobra.Command, args []string) error {
		return checkTriggerArgs(args, mustGet(cmd.Flags().GetBool, "cancel"))
	}

	var (
		oldOrder deprecatedArgs
		dry      dryRun
	)

	cmd := &cobra.Command{
		Use:   "trigger <experiment name> <lifecycle> [<app name> ...]",
		Short: "Trigger lifecycle stage for app(s) in experiment",
		Long:  desc,
		Example: `
  # Run the configure stage of every app of exp1
  phenix experiment trigger exp1 configure

  # Run the running stage of the soh app in every experiment
  phenix experiment trigger all running soh

  # Stop the soh trigger that phenix ui runs for exp1
  phenix experiment trigger exp1 --cancel soh`,
		ValidArgsFunction: experimentTriggerCompletion(true),
		Args:              argsWithUsage(validateArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := sigterm.CancelContext(commandContext(cmd))
			cancel := mustGet(cmd.Flags().GetBool, "cancel")

			req, err := parseTriggerArgs(args, cancel, storedExperiment)
			if err != nil {
				return err
			}

			if cancel {
				return cancelExperimentTriggers(ctx, cmd.OutOrStdout(), newExperimentClient(common.UnixSocket), req, dry)
			}

			if dry.enabled() {
				return errors.New("--dry-run works only with --cancel")
			}

			if req.legacy {
				oldOrder.warn(req.newOrderCommand())
			}

			return triggerExperimentApps(ctx, req.stage, req.experiment, req.apps)
		},
	}

	cmd.Flags().Bool("cancel", false, "Stop the triggers that phenix ui runs for the experiment (needs phenix ui)")
	dry = addDryRunFlag(cmd)

	oldOrder = deprecateArgs(
		cmd, "<lifecycle> <experiment name> [<app name> ...]", "<experiment name> <lifecycle> [<app name> ...]",
	)

	return cmd
}

// cancelExperimentTriggers asks the server to stop the triggers that it runs
// for the experiment of req, or for each stored experiment for 'all'. In a dry
// run, it checks that the server answers and that each experiment exists, and
// shows each cancel instead.
func cancelExperimentTriggers(
	ctx context.Context, w io.Writer, client *experimentClient, req triggerRequest, dry dryRun,
) error {
	names := []string{req.experiment}

	if req.experiment == allExperiments {
		exps, err := experiment.List()
		if err != nil {
			err := util.HumanizeError(err, "Unable to cancel the triggered apps of all experiments")

			return err.Humanized()
		}

		names = names[:0]
		for _, exp := range exps {
			names = append(names, exp.Metadata.Name)
		}
	}

	apps := "every triggered app"
	if len(req.apps) > 0 {
		apps = "the triggered apps " + strings.Join(req.apps, ", ")
	}

	if dry.enabled() {
		if _, err := client.options(ctx); err != nil {
			return fmt.Errorf(
				"canceling the triggered apps: %w",
				client.notRunning(err, "the triggers that it runs exist only in the running server"),
			)
		}
	}

	for _, name := range names {
		if dry.enabled() && req.experiment != allExperiments {
			if err := requireExperiment(name); err != nil {
				return err
			}
		}

		if dry.skip("cancel", apps+" of experiment "+name) {
			continue
		}

		if err := client.cancelTriggers(ctx, name, req.apps); err != nil {
			return fmt.Errorf(
				"canceling the triggered apps of experiment %s: %w",
				name, client.notRunning(err, "the triggers that it runs exist only in the running server"),
			)
		}

		fmt.Fprintf(w, "Cancel requested for %s of experiment %s\n", apps, name)
	}

	return nil
}

func newExperimentTriggerRunningCmd() *cobra.Command {
	desc := `Trigger an app's "running" stage in an experiment

  Used to manually trigger the "running" stage of an app (or apps) for the
  given experiment on demand. Using 'all' instead of a specific experiment
  name will trigger the "running" stage of the given app(s) for all running
  experiments. Providing no apps will cause all apps for the experiment(s) to
  be run.`

	cmd := &cobra.Command{
		Use:               "trigger-running <experiment name> [<app name> ...]",
		Aliases:           []string{"trig"},
		Short:             "Trigger running stage for app(s) in experiment",
		Long:              desc,
		ValidArgsFunction: experimentTriggerCompletion(false),
		Args:              argsWithUsage(cobra.MinimumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx := sigterm.CancelContext(context.Background())

			return triggerExperimentApps(ctx, app.ActionRunning, args[0], args[1:])
		},
	}

	deprecateCommand(cmd, "phenix experiment trigger <experiment name> running [<app name> ...]")

	return cmd
}

//nolint:funlen // command definition
func newExperimentScorchCmd() *cobra.Command {
	desc := `Start a Scorch run for an experiment

  Used to start the Scorch run that --run selects (0 when not given) for the
  given experiment, and to wait until it ends. The command prints the
  progress of the run, and exits with an error when the run fails.

  When phenix ui is running, the server executes the run, so the web UI
  shows it and can cancel it. Ctrl+C cancels the run on the server. When
  phenix ui is not running, this command executes the run in its own
  process. The web UI does not show that run.

  The --status, --cancel, --cleanup and --clear flags need phenix ui. Each
  acts on the run that --run selects, and only one can be given:

    --status   shows the status of each stage and component of the run
    --cancel   cancels the run on the server
    --cleanup  runs only the cleanup stage of the run, and waits for it
    --clear    clears the status of a run that is not executing

  -o/--output sets the output format of --status. With --cancel or --clear,
  --dry-run checks that the server has the run, shows what phenix would
  request, and requests nothing.`

	var (
		out *outputFlag
		dry dryRun
	)

	cmd := &cobra.Command{
		Use:   "scorch <experiment name>",
		Short: "Start a Scorch run for experiment",
		Long:  desc,
		Example: `
  # Run Scorch run 1 and wait until it ends
  phenix experiment scorch exp1 --run 1

  # Show the status of Scorch run 1
  phenix experiment scorch exp1 --run 1 --status

  # Cancel Scorch run 1 on the server
  phenix experiment scorch exp1 --run 1 --cancel

  # The status of Scorch run 1 as JSON
  phenix experiment scorch exp1 --run 1 --status -o json`,
		ValidArgsFunction: expNameCompletion(false),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name = args[0]
				run  = mustGet(cmd.Flags().GetInt, "run")
				ctx  = sigterm.CancelContext(commandContext(cmd))
			)

			client := newExperimentClient(common.UnixSocket)

			cancelRun, clearRun := mustGet(cmd.Flags().GetBool, "cancel"), mustGet(cmd.Flags().GetBool, "clear")
			if dry.enabled() && !cancelRun && !clearRun {
				return errors.New("--dry-run works only with --cancel or --clear")
			}

			switch {
			case mustGet(cmd.Flags().GetBool, "status"):
				pipelines, err := client.scorchPipelines(ctx, name)
				if err != nil {
					return fmt.Errorf("getting Scorch run %d of experiment %s: %w", run, name, client.notRunning(err, scorchServerOnly))
				}

				status, err := newScorchRunStatus(name, run, pipelines)
				if err != nil {
					return err
				}

				return out.printAPI(cmd, status, outputViews{
					formatText: func(w io.Writer) error { printScorchStatus(w, status); return nil },
				})
			case cancelRun:
				if err := checkScorchDryRun(ctx, client, dry, "canceling", name, run); err != nil {
					return err
				}

				if dry.skip("cancel", fmt.Sprintf("Scorch run %d of experiment %s", run, name)) {
					return nil
				}

				if err := client.cancelScorch(ctx, name, run); err != nil {
					return fmt.Errorf("canceling Scorch run %d of experiment %s: %w", run, name, client.notRunning(err, scorchServerOnly))
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Cancel requested for Scorch run %d of experiment %s\n", run, name)

				return nil
			case clearRun:
				if err := checkScorchDryRun(ctx, client, dry, "clearing", name, run); err != nil {
					return err
				}

				if dry.skip("clear the status of", fmt.Sprintf("Scorch run %d of experiment %s", run, name)) {
					return nil
				}

				if err := client.clearScorch(ctx, name, run); err != nil {
					return fmt.Errorf("clearing Scorch run %d of experiment %s: %w", run, name, client.notRunning(err, scorchServerOnly))
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Cleared the status of Scorch run %d of experiment %s\n", run, name)

				return nil
			}

			cleanupOnly := mustGet(cmd.Flags().GetBool, "cleanup")

			if _, err := client.options(ctx); err != nil {
				if !serverAbsent(err) || cleanupOnly {
					return fmt.Errorf("starting Scorch run %d of experiment %s: %w", run, name, client.notRunning(err, scorchServerOnly))
				}

				return runScorchInProcess(ctx, name, run)
			}

			wait := scorchWait{
				client: client, exp: name, run: run, cleanupOnly: cleanupOnly, interval: scorchWaitInterval, out: cmd.OutOrStdout(),
			}

			err := wait.startAndWait(ctx)
			if errors.Is(err, context.Canceled) {
				return nil
			}

			if err != nil && !errors.Is(err, errScorchRunFailed) {
				return fmt.Errorf("starting Scorch run %d of experiment %s: %w", run, name, err)
			}

			return err
		},
	}

	cmd.Flags().IntP("run", "r", 0, "ID of the Scorch run to act on (defaults to 0)")
	cmd.Flags().Bool("status", false, "Show the status of the run (needs phenix ui)")
	cmd.Flags().Bool("cancel", false, "Cancel the run on the server (needs phenix ui)")
	cmd.Flags().Bool("cleanup", false, "Run only the cleanup stage of the run (needs phenix ui)")
	cmd.Flags().Bool("clear", false, "Clear the status of the run (needs phenix ui)")
	cmd.MarkFlagsMutuallyExclusive("status", "cancel", "cleanup", "clear")

	out = addOutputFlag(cmd, formatText, []string{formatText, FormatJSON, FormatYAML})
	dry = addDryRunFlag(cmd)

	return cmd
}

// scorchServerOnly says why a SCORCH request needs the server.
const scorchServerOnly = "the Scorch runs that it executes exist only in the running server"

// checkScorchDryRun checks, in a dry run of verb on run of experiment name,
// that the server answers and has the run, as the real request needs.
func checkScorchDryRun(ctx context.Context, client *experimentClient, dry dryRun, verb, name string, run int) error {
	if !dry.enabled() {
		return nil
	}

	pipelines, err := client.scorchPipelines(ctx, name)
	if err != nil {
		return fmt.Errorf("%s Scorch run %d of experiment %s: %w", verb, run, name, client.notRunning(err, scorchServerOnly))
	}

	if _, err := pipelines.run(name, run); err != nil {
		return err
	}

	return nil
}

// runScorchInProcess executes run of experiment name in this process, as
// phenix experiment scorch does when phenix ui is not running.
func runScorchInProcess(ctx context.Context, name string, run int) error {
	exp, err := experiment.Get(name)
	if err != nil {
		err := util.HumanizeError(
			err,
			"%s",
			fmt.Sprintf(
				"Unable to start Scorch run %d for %s experiment",
				run,
				name,
			),
		)

		return err.Humanized()
	}

	plog.Warn(
		plog.TypeSystem,
		"phenix ui is not running, so this process executes the Scorch run, and the web UI does not show it",
		"exp", name, "run", run,
	)

	ctx = app.SetContextTriggerCLI(ctx)

	if err := scorchexe.Execute(ctx, exp, run); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}

		err := util.HumanizeError(
			err,
			"%s",
			fmt.Sprintf(
				"Unable to start Scorch run %d for %s experiment",
				run,
				exp.Metadata.Name,
			),
		)

		return err.Humanized()
	}

	plog.Info(plog.TypeSystem, "scorch run triggered", "exp", exp.Metadata.Name, "run", run)

	return nil
}

func init() { //nolint:gochecknoinits // cobra command
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
	experimentCmd.AddCommand(newExperimentTriggerCmd())
	experimentCmd.AddCommand(newExperimentTriggerRunningCmd())
	experimentCmd.AddCommand(newExperimentScorchCmd())
	experimentCmd.AddCommand(newExperimentCaptureCmd())

	addCommandToRoot(experimentCmd, true)
}
