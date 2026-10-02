package util

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"phenix/api/vm"
	"phenix/web/cache"
)

const (
	// ScreenshotCacheDuration is how long a screenshot is reused. The websocket
	// screenshot ticker runs just longer, so clients whose rounds are offset in
	// time share screenshots.
	ScreenshotCacheDuration = 14 * time.Second
	// maxScreenshotSize bounds the size clients may ask for; it is passed to
	// minimega as part of a command.
	maxScreenshotSize = 4096
)

var ErrInvalidScreenshotSize = errors.New("screenshot size must be a whole number from 1 to 4096")

// screenshotFlights merges concurrent requests for the same screenshot into one
// minimega command.
var screenshotFlights singleflight.Group //nolint:gochecknoglobals // shared by concurrent callers

// ValidScreenshotSize reports whether size is a whole number of pixels
// minimega can take a screenshot at.
func ValidScreenshotSize(size string) bool {
	n, err := strconv.Atoi(size)

	return err == nil && n >= 1 && n <= maxScreenshotSize
}

func GetScreenshot(expName, vmName, size string) ([]byte, error) {
	if !ValidScreenshotSize(size) {
		return nil, ErrInvalidScreenshotSize
	}

	// clients ask for different sizes (table rows, tiles, VNC zoom)
	name := fmt.Sprintf("%s_%s_%s", expName, vmName, size)

	if screenshot, ok := cache.Get(name); ok {
		return screenshot, nil
	}

	val, err, _ := screenshotFlights.Do(name, func() (any, error) {
		// a flight that finished after the check above has cached the screenshot
		if screenshot, ok := cache.Get(name); ok {
			return screenshot, nil
		}

		screenshot, err := vm.Screenshot(expName, vmName, size)
		if err != nil {
			return nil, fmt.Errorf("getting screenshot for VM: %w", err)
		}

		if screenshot == nil {
			return nil, errors.New("vm screenshot not found")
		}

		_ = cache.SetWithExpire(name, screenshot, ScreenshotCacheDuration)

		return screenshot, nil
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped in the flight
	}

	screenshot, _ := val.([]byte)

	return screenshot, nil
}
