package util

import (
	"errors"
	"fmt"
	"time"

	"phenix/api/vm"
	"phenix/web/cache"
)

const screenshotCacheDuration = 10 * time.Second

func GetScreenshot(expName, vmName, size string) ([]byte, error) {
	name := fmt.Sprintf("%s_%s", expName, vmName)

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

	_ = cache.SetWithExpire(name, screenshot, screenshotCacheDuration)

	return screenshot, nil
}
