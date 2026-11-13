package vm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"phenix/api/experiment"
	"phenix/util/mm"
)

// FullScreenshotSize is the size that SaveScreenshot asks minimega for: the largest that phenix allows. minimega only
// makes a larger screen smaller, so the screenshot of a screen up to this size is the full size.
const FullScreenshotSize = "4096"

// screenshotTimeLayout is the UTC time in the name of a saved screenshot.
const screenshotTimeLayout = "20060102-150405"

// ErrNoScreenshot is the error of SaveScreenshot when minimega gives no image.
var ErrNoScreenshot = errors.New("minimega gave no screenshot")

// SaveScreenshot takes a full-size screenshot of the running VM and saves it as a PNG in the experiment's files
// directory, at screenshots/<vm>-<YYYYMMDD-HHMMSS>.png for the UTC time now. It never replaces a file (see
// experiment.WriteNewFile). It returns the path of the file, relative to the files directory. It refuses a VM that is
// not running (ErrVMNotRunning). Once ctx is done, a minimega command not yet sent gives up.
func SaveScreenshot(ctx context.Context, expName, vmName string, now time.Time) (string, error) {
	v, err := Get(ctx, expName, vmName)
	if err != nil {
		return "", fmt.Errorf("getting VM details: %w", err)
	}

	if !v.Running {
		return "", ErrVMNotRunning
	}

	png, err := mm.GetVMScreenshot(
		mm.NS(expName), mm.VMName(vmName), mm.ScreenshotSize(FullScreenshotSize), mm.Context(ctx),
	)
	if err != nil {
		return "", fmt.Errorf("getting VM screenshot: %w", err)
	}

	if len(png) == 0 {
		return "", ErrNoScreenshot
	}

	name := fmt.Sprintf("screenshots/%s-%s.png", vmName, now.UTC().Format(screenshotTimeLayout))

	saved, err := experiment.WriteNewFile(expName, name, png)
	if err != nil {
		return "", fmt.Errorf("saving VM screenshot: %w", err)
	}

	return saved, nil
}
