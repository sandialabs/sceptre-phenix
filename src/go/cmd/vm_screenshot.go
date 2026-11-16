package cmd

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"phenix/api/vm"
	"phenix/util/file"
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

// writeScreenshot writes png to the --output-file value output, or to a stdout that is not a terminal. For a
// terminal with no --output-file, it makes a new file in the folder dir, with the name that vm.ScreenshotFileName
// gives for now, and prints that name. It does not replace a file there (see file.WriteNew).
func writeScreenshot(cmd *cobra.Command, output, dir, vmName string, now time.Time, png []byte) error {
	if output == "" && isTerminalFile(cmd.OutOrStdout()) {
		saved, err := file.WriteNew(dir, vm.ScreenshotFileName(vmName, now), png)
		if err != nil {
			return fmt.Errorf("writing the screenshot: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", saved)

		return nil
	}

	return writeBinaryOutput(cmd, output, "a PNG image", func(w io.Writer) error {
		if _, err := w.Write(png); err != nil {
			return fmt.Errorf("writing the screenshot: %w", err)
		}

		return nil
	})
}

func newVMScreenshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "screenshot <experiment name> <vm name>",
		Short: "Save a screenshot of the screen of a running VM",
		Long: `Save a screenshot of the screen of a running VM

  Writes the screen of the VM as a PNG image to the --output-file file, or to
  stdout when stdout is not a terminal. With no --output-file and a terminal,
  it makes the file <vm name>-<YYYYMMDD-HHMMSS>.png (UTC time) in the current
  folder, as the web UI names a saved screenshot, and prints the file name.
  It does not replace a file with that name: it adds -1, -2 and so on. With
  --size, minimega scales the image so that its longest side is at most that
  many pixels. The experiment must run.`,
		Example: `
  phenix vm screenshot my-exp web
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

			return writeScreenshot(cmd, mustGet(cmd.Flags().GetString, outputFileFlag), ".", vmName, time.Now(), png)
		},
	}

	cmd.Flags().StringP(outputFileFlag, "O", "", "Write the PNG image to this file (\"-\" for stdout)")
	cmd.Flags().Int("size", 0, "Longest side of the image in pixels, from 1 to 4096 (0 for the full size)")

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("vm").AddCommand(newVMScreenshotCmd())
}
