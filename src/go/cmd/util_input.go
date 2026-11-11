package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/api/scorch"
	"phenix/api/scorch/scorchmd"
	"phenix/scheduler"
	"phenix/util"
	"phenix/util/mm"
)

// scorchJSONArgs is the number of arguments of phenix util scorch-json.
const scorchJSONArgs = 2

// rawJSONViews returns the output views of data, a JSON document: JSON is
// data as it is, with a newline at the end, and YAML has the keys of data
// in their order.
func rawJSONViews(data []byte) outputViews {
	return outputViews{
		FormatJSON: func(w io.Writer) error {
			if _, err := w.Write(append(slices.Clip(data), '\n')); err != nil {
				return fmt.Errorf("writing JSON: %w", err)
			}

			return nil
		},
		FormatYAML: func(w io.Writer) error { return writeJSONKeyedYAML(w, json.RawMessage(data)) },
	}
}

func newUtilSchedulerJSONCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "scheduler-json <experiment name>",
		Short: "Print user scheduler JSON input for given experiment to STDOUT",
		Long: `Print user scheduler JSON input for given experiment to STDOUT

  Prints the JSON that a user scheduler (phenix-scheduler-<name>) gets on
  stdin when phenix schedules the stored experiment: the experiment spec
  (spec) and the schedulable cluster hosts with their disk usage (hosts).
  The command uses the code of a user scheduler run. It reads the hosts
  from minimega within the time limit of a user scheduler, and it stores
  nothing. With -o json (the default), the output is the exact bytes that
  the scheduler gets, with a newline at the end.`,
		Example: `
  phenix util scheduler-json my-exp
  phenix util scheduler-json my-exp | phenix-scheduler-custom`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			data, err := experiment.SchedulerInput(commandContext(cmd), name)

			switch {
			case errors.Is(err, scheduler.ErrTimeout):
				err := util.HumanizeError(err, "Unable to read the cluster hosts within %v", scheduler.UserTimeout)

				return err.Humanized()
			case errors.Is(err, mm.ErrMeshUnavailable):
				err := util.HumanizeError(err, "Unable to read the cluster hosts: the mesh peers did not answer")

				return err.Humanized()
			case errors.Is(err, experiment.ErrSchedulerHosts):
				err := util.HumanizeError(err, "Unable to read the cluster hosts")

				return err.Humanized()
			case err != nil:
				err := util.HumanizeError(err, "%s", "Unable to get the "+name+" experiment")

				return err.Humanized()
			}

			return out.print(cmd, nil, rawJSONViews(data))
		},
	}

	out = addOutputFlag(cmd, FormatJSON, []string{FormatJSON, FormatYAML})

	return cmd
}

func newUtilScorchJSONCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "scorch-json <experiment name> <component name>",
		Short: "Print SCORCH component JSON input for given experiment to STDOUT",
		Long: `Print SCORCH component JSON input for given experiment to STDOUT

  Prints the JSON that the program of a SCORCH user component
  (phenix-scorch-component-<type>) gets on stdin in a SCORCH run of the
  stored experiment. The command uses the code of a SCORCH run that starts
  from the web UI, the REST API or phenix experiment scorch, and it stores
  nothing. With -o json (the default), the output is the exact bytes that
  the component gets, with a newline at the end.

  --run, --loop and --stage select where the run starts the component. Loop
  0 is the run, loop 1 is the loop of the run, and so on. When you do not
  give them, the command uses the first place that runs the component.
  --count is the pass of that loop, from 0. Only the arguments change with
  the stage and the count.

  A SCORCH run picks a random value for each replacement (replace) at each
  pass of a loop. The command picks the values in the same way. --replace
  KEY=VALUE sets a value. The command reads VALUE as JSON when it is valid
  JSON, and as text when it is not.

  --runtime adds the cluster hosts and the VMs of the experiment (hosts and
  vms). A component gets them when an external app ran before the scorch
  app in the same app stage.

  On stderr, the command prints the program, its arguments, the PHENIX_*
  variables that phenix adds, the replacement values and warnings. When you
  give -o json, it prints only the warnings.`,
		Example: `
  phenix util scorch-json my-exp capture
  phenix util scorch-json my-exp capture --run 1 --loop 1 --stage stop --count 2
  phenix util scorch-json my-exp capture --replace '{{DURATION}}=30' -o json \
    | phenix-scorch-component-tcpdump start capture 0 0 0`,
		Args:              argsWithUsage(cobra.ExactArgs(scorchJSONArgs)),
		ValidArgsFunction: completeArgs(completeExperiments(expAny, false), completeScorchComponents),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := scorchInputOptions(cmd)
			if err != nil {
				return err
			}

			expName, name := args[0], args[1]

			input, err := scorch.UserComponentInput(commandContext(cmd), expName, name, opts)

			switch {
			case errors.Is(err, experiment.ErrAppRuntime):
				err := util.HumanizeError(err, "Unable to collect runtime experiment information")

				return err.Humanized()
			case errors.Is(err, scorch.ErrComponentNotFound), errors.Is(err, scorch.ErrBuiltinComponent),
				errors.Is(err, scorch.ErrComponentNotRun), errors.Is(err, scorch.ErrUnknownReplacement),
				errors.Is(err, scorchmd.ErrScorchNotConfigured):
				return fmt.Errorf("unable to get the input of the SCORCH component %s: %w", name, err)
			case err != nil:
				err := util.HumanizeError(err, "%s", "Unable to get the "+expName+" experiment")

				return err.Humanized()
			}

			details := !cmd.Flags().Changed(outputFlagName) || out.format() != FormatJSON
			printComponentDetails(cmd.ErrOrStderr(), input, details)

			return out.print(cmd, nil, rawJSONViews(input.Stdin))
		},
	}

	cmd.Flags().Int("run", 0, "Index of the SCORCH run (default: the first run that runs the component)")
	cmd.Flags().Int("loop", 0, "Depth of the loop, 0 for the run (default: the first loop that runs the component)")
	cmd.Flags().String("stage", "", "Stage: "+strings.Join(scorch.InputStages(), ", ")+
		" (default: the first stage that runs the component)")
	cmd.Flags().Int("count", 0, "Pass of the loop, from 0")
	cmd.Flags().StringArray("replace", nil, "Value of a replacement as KEY=VALUE, read as JSON when it is valid JSON")
	cmd.Flags().Bool("runtime", false, "Add the cluster hosts and the VMs of the experiment")

	_ = cmd.RegisterFlagCompletionFunc("stage", completeValues(scorch.InputStages()...))

	out = addOutputFlag(cmd, FormatJSON, []string{FormatJSON, FormatYAML})

	return cmd
}

// scorchInputOptions returns the input options that the flags of phenix util
// scorch-json give.
func scorchInputOptions(cmd *cobra.Command) (scorch.InputOptions, error) {
	opts := scorch.InputOptions{ //nolint:exhaustruct // the run, loop and start time are optional
		Count:   mustGet(cmd.Flags().GetInt, "count"),
		Runtime: mustGet(cmd.Flags().GetBool, "runtime"),
	}

	for _, flag := range []string{"run", "loop"} {
		if cmd.Flags().Changed(flag) {
			value := mustGet(cmd.Flags().GetInt, flag)

			if flag == "run" {
				opts.Run = &value
			} else {
				opts.Loop = &value
			}
		}
	}

	if stage := mustGet(cmd.Flags().GetString, "stage"); stage != "" {
		var err error
		if opts.Stage, err = scorch.ParseInputStage(stage); err != nil {
			return opts, err
		}
	}

	replace, err := scorch.ParseReplacements(mustGet(cmd.Flags().GetStringArray, "replace"))
	if err != nil {
		return opts, err
	}

	opts.Replace = replace

	return opts, nil
}

// printComponentDetails writes to w the program of input, its arguments, its
// PHENIX_* variables and the replacement values when details is set, and
// the warnings of input.
func printComponentDetails(w io.Writer, input *scorch.ComponentInput, details bool) {
	if details {
		fmt.Fprintf(w, "Program: %s\nArguments: %s\nEnvironment:\n", input.Program, strings.Join(input.Args, " "))

		for _, env := range input.Env {
			fmt.Fprintf(w, "  %s\n", env)
		}

		if len(input.Replacements) > 0 {
			fmt.Fprintln(w, "Replacements:")

			for _, key := range slices.Sorted(maps.Keys(input.Replacements)) {
				value, _ := json.Marshal(input.Replacements[key])
				fmt.Fprintf(w, "  %s=%s\n", key, value)
			}
		}
	}

	for _, warning := range input.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}
