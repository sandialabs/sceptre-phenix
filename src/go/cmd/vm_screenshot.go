package cmd

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"phenix/api/vm"
	webutil "phenix/web/util"
)

const screenshotArgs = 2

// errBadScreenshotSize is returned for a --size that minimega cannot take a screenshot at.
var errBadScreenshotSize = errors.New("invalid screenshot size")

// screenshotSize returns the --size of phenix vm screenshot as minimega takes
// it: empty for 0, the full size, else a whole number of pixels from 1 to 4096.
func screenshotSize(size int) (string, error) {
	if size == 0 {
		return "", nil
	}

	value := strconv.Itoa(size)
	if !webutil.ValidScreenshotSize(value) {
		return "", fmt.Errorf("%w: %d is not 0 (full size) or a whole number of pixels from 1 to 4096", errBadScreenshotSize, size)
	}

	return value, nil
}

func newVMScreenshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "screenshot <experiment name> <vm name>",
		Short: "Save a screenshot of the screen of a running VM",
		Long: `Save a screenshot of the screen of a running VM

  Writes the screen of the VM as a PNG image to the --output-file file, or to
  stdout when stdout is not a terminal. With --size, minimega scales the
  image so that its longest side is at most that many pixels. The experiment
  must run.`,
		Example: `
  phenix vm screenshot my-exp web --output-file web.png
  phenix vm screenshot my-exp web --size 800 > web.png`,
		Args:              argsWithUsage(cobra.ExactArgs(screenshotArgs)),
		ValidArgsFunction: completeArgs(completeExperiments(expRunning, false), completeVMs),
		RunE: func(cmd *cobra.Command, args []string) error {
			expName, vmName := args[0], args[1]

			size, err := screenshotSize(mustGet(cmd.Flags().GetInt, "size"))
			if err != nil {
				return err
			}

			if _, err := vm.CheckTarget(expName, vmName, true); err != nil {
				return fmt.Errorf("unable to take a screenshot of the %s VM: %w", vmName, err)
			}

			png, err := vm.Screenshot(expName, vmName, size)
			if err != nil {
				return fmt.Errorf("unable to take a screenshot of the %s VM: %w", vmName, err)
			}

			if len(png) == 0 {
				return fmt.Errorf("unable to take a screenshot of the %s VM: minimega returned no image", vmName)
			}

			return writeBinaryOutput(cmd, mustGet(cmd.Flags().GetString, outputFileFlag), "a PNG image", func(w io.Writer) error {
				if _, err := w.Write(png); err != nil {
					return fmt.Errorf("writing the screenshot: %w", err)
				}

				return nil
			})
		},
	}

	cmd.Flags().StringP(outputFileFlag, "O", "", "Write the PNG image to this file instead of stdout")
	cmd.Flags().Int("size", 0, "Longest side of the image in pixels, from 1 to 4096 (0 for the full size)")

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("vm").AddCommand(newVMScreenshotCmd())
}
