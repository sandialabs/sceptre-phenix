package util

import (
	"context"
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
	// screenshotStep is what screenshot widths are rounded up to a multiple of.
	screenshotStep = 200
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

// ScreenshotWidth returns size rounded up to a multiple of 200 pixels, at most 4096, so the
// UI's tables, tiles and console zoom levels share a few cached widths.
func ScreenshotWidth(size string) (string, error) {
	if !ValidScreenshotSize(size) {
		return "", ErrInvalidScreenshotSize
	}

	n, _ := strconv.Atoi(size)

	return strconv.Itoa(min((n+screenshotStep-1)/screenshotStep*screenshotStep, maxScreenshotSize)), nil
}

func GetScreenshot(expName, vmName, size string) ([]byte, error) {
	return GetScreenshotContext(context.Background(), expName, vmName, size)
}

// GetScreenshotContext is GetScreenshot at size's ScreenshotWidth, returning ctx's error once ctx
// is done while the shared minimega command runs on and caches its screenshot.
func GetScreenshotContext(ctx context.Context, expName, vmName, size string) ([]byte, error) {
	size, err := ScreenshotWidth(size)
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("%s_%s_%s", expName, vmName, size)

	if screenshot, ok := cache.Get(name); ok {
		return screenshot, nil
	}

	flight := screenshotFlights.DoChan(name, func() (any, error) {
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

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for screenshot: %w", ctx.Err())
	case res := <-flight:
		if res.Err != nil {
			return nil, res.Err //nolint:wrapcheck // wrapped in the flight
		}

		screenshot, _ := res.Val.([]byte)

		return screenshot, nil
	}
}
