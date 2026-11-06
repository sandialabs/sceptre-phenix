package util

import (
	"errors"
	"fmt"
	"time"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/firecracker"
	"phenix/web/cache"
)

const (
	screenshotCacheDuration = 10 * time.Second
	vmTypeCacheDuration     = 1 * time.Minute
)

// GetScreenshot returns a screenshot of a running VM. A firecracker VM has no
// screen, so for one it returns an error that wraps vm.ErrNotSupported and
// sends no command to minimega.
func GetScreenshot(expName, vmName, size string) ([]byte, error) {
	name := fmt.Sprintf("%s_%s", expName, vmName)

	if screenshot, ok := cache.Get(name); ok {
		return screenshot, nil
	}

	if nodeVMType(expName, vmName) == firecracker.VMType {
		return nil, fmt.Errorf("getting screenshot for VM: %w", vm.NotSupportedError("taking a screenshot"))
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

// nodeVMType returns the minimega VM type of a topology node, or "" when the
// experiment does not have the node. It reads the experiment at most once a
// minute and caches the type of every node in it, because the screenshot
// callers ask for many VMs of one experiment in turn.
func nodeVMType(expName, vmName string) string {
	if vmType, ok := cache.Get(vmTypeCacheKey(expName, vmName)); ok {
		return string(vmType)
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return ""
	}

	var found string

	for _, node := range exp.Spec.Topology().Nodes() {
		vmType := vm.NodeVMType(node)

		_ = cache.SetWithExpire(
			vmTypeCacheKey(expName, node.General().Hostname()),
			[]byte(vmType),
			vmTypeCacheDuration,
		)

		if node.General().Hostname() == vmName {
			found = vmType
		}
	}

	return found
}

func vmTypeCacheKey(expName, vmName string) string {
	return "vm-type/" + expName + "/" + vmName
}
