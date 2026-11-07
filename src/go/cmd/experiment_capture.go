package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"phenix/api/capture"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/printer"
	"phenix/web"
)

// captureSettings applies the capture settings that phenix ui applies: ui.features with minimega-captures turns the
// capture support on whatever minimega's version. It returns ui.captures.max-size, in MiB.
func captureSettings() (int64, error) {
	if slices.Contains(viper.GetStringSlice("ui.features"), web.FeatureMinimegaCaptures) {
		mm.SetCaptureFeaturesForced(true)
	}

	return captureMaxSize(viper.GetViper())
}

// captureFormatFlag is the format a --format flag of a capture start gives, or an error for another format.
func captureFormatFlag(cmd *cobra.Command) (string, error) {
	format := mustGet(cmd.Flags().GetString, "format")

	switch format {
	case mm.CaptureFormatPcapng, mm.CaptureFormatPcap:
		return format, nil
	}

	return "", fmt.Errorf("invalid --format %q: it must be %s or %s", format, mm.CaptureFormatPcapng, mm.CaptureFormatPcap)
}

// addCaptureFormatFlag adds the --format flag of a capture start to cmd.
func addCaptureFormatFlag(cmd *cobra.Command) {
	cmd.Flags().String(
		"format", mm.CaptureFormatPcapng,
		"capture file format, pcapng or pcap (minimega before 3.4.0 writes only pcap)",
	)
}

func newExperimentCaptureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Experiment capture management",
		Long: `Experiment capture management

  An experiment capture records the traffic of all the VLANs, or of the given
  VLANs, of a running experiment on all its hosts into one file in the
  experiment's files directory. It needs minimega 3.4.0 or later.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newExperimentCaptureStartCmd())
	cmd.AddCommand(newExperimentCaptureStopCmd())
	cmd.AddCommand(newExperimentCaptureListCmd())

	return cmd
}

func newExperimentCaptureStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <experiment name>",
		Short: "Start an experiment capture",
		Long: `Start an experiment capture

  Starts one packet capture of the running experiment's traffic, on every
  VLAN or on the VLANs that --vlan gives, and prints its ID, its file and the
  Open vSwitch mirror budget warnings. phenix refuses the start when a host
  and bridge that the capture needs has 32 mirrors.`,
		Example: `  phenix experiment capture start exp1
  phenix experiment capture start exp1 --vlan EXP_1 --vlan EXP_2 --filter "tcp port 502" --format pcap`,
		ValidArgsFunction: expNameCompletion(false),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			format, err := captureFormatFlag(cmd)
			if err != nil {
				return err
			}

			maxSize, err := captureSettings()
			if err != nil {
				return err
			}

			started, err := capture.Start(context.Background(), name, capture.StartRequest{
				VLANs:   mustGet(cmd.Flags().GetStringArray, "vlan"),
				Filter:  mustGet(cmd.Flags().GetString, "filter"),
				Snaplen: mustGet(cmd.Flags().GetInt, "snaplen"),
				Name:    mustGet(cmd.Flags().GetString, "name"),
				Format:  format,
			}, maxSize)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to start an experiment capture of "+name)

				return err.Humanized()
			}

			c := started.Capture

			plog.Info(plog.TypeSystem, "experiment capture started", "exp", name, "capture", c.ID)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Started experiment capture %s of %s into %s\n", c.ID, name, capture.File(name, c))

			for _, warning := range started.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", warning)
			}

			return nil
		},
	}

	cmd.Flags().StringArray("vlan", nil, "VLAN alias to capture (repeat for more; all VLANs when not given)")
	cmd.Flags().String("filter", "", "BPF filter of the frames to capture")
	cmd.Flags().Int("snaplen", 0, "most bytes of each frame to keep, from 14 to 65535 (0 keeps 1600)")
	cmd.Flags().String("name", "", "file name in the experiment's files directory (default <exp>[-<vlan>]-<time>)")
	addCaptureFormatFlag(cmd)

	return cmd
}

func newExperimentCaptureStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <experiment name> <capture ID>",
		Short: "Stop an experiment capture",
		Long: `Stop an experiment capture

  Stops the experiment capture and prints its final counters. Its file stays
  in the experiment's files directory.`,
		ValidArgsFunction: expNameCompletion(false),
		Args:              argsWithUsage(cobra.ExactArgs(2)), //nolint:mnd // experiment and capture ID
		RunE: func(cmd *cobra.Command, args []string) error {
			name, id := args[0], args[1]

			if _, err := captureSettings(); err != nil {
				return err
			}

			c, err := capture.Stop(context.Background(), name, id)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to stop experiment capture "+id+" of "+name)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "experiment capture stopped", "exp", name, "capture", id)

			printer.PrintTableOfExperimentCaptures(cmd.OutOrStdout(), []mm.NamespaceCapture{c})

			return nil
		},
	}
}

func newExperimentCaptureListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <experiment name>",
		Short: "List the experiment captures of an experiment",
		Long: `List the experiment captures of an experiment

  Prints each experiment capture with a row for each host and bridge that it
  records, then the Open vSwitch mirror budget of each host and bridge that
  the experiment uses.`,
		ValidArgsFunction: expNameCompletion(false),
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				name = args[0]
				ctx  = context.Background()
				out  = cmd.OutOrStdout()
			)

			if _, err := captureSettings(); err != nil {
				return err
			}

			captures, err := capture.List(ctx, name)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to list the experiment captures of "+name)

				return err.Humanized()
			}

			if len(captures) == 0 {
				fmt.Fprintf(out, "Experiment %s has no experiment captures\n", name)
			} else {
				printer.PrintTableOfExperimentCaptures(out, captures)
			}

			budgets, err := capture.Budgets(ctx, name)

			switch {
			case errors.Is(err, capture.ErrNotRunning):
				return nil
			case err != nil:
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: cannot count the Open vSwitch mirrors: %v\n", err)
			case len(budgets) > 0:
				fmt.Fprintln(out)
				printer.PrintTableOfMirrorBudgets(out, budgets)
			}

			return nil
		},
	}
}
