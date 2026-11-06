package vm

import (
	"context"
	"time"

	ifaces "phenix/types/interfaces"
)

// ShutdownWithTiming is Shutdown, asking for the VM's state every poll and killing it after timeout.
func ShutdownWithTiming(ctx context.Context, expName, vmName string, poll, timeout time.Duration) error {
	return shutdown(ctx, expName, vmName, poll, timeout)
}

// InjectPartitionOf is injectPartitionOf, the partition of drive that phenix injects files into.
func InjectPartitionOf(drive ifaces.NodeDrive) int {
	return injectPartitionOf(drive)
}

// VMImage is getVMImage, the base image of the VM's first drive in the topology.
func VMImage(expName, vmName string) (string, error) {
	return getVMImage(expName, vmName)
}

// CaptureFileOf is captureFile, the file of a capture of the experiment expName into out in format.
func CaptureFileOf(expName, out, format string) string {
	return captureFile(expName, out, format)
}

// CaptureFormatOf is the format that minimega writes the captures of opts in.
func CaptureFormatOf(ctx context.Context, opts CaptureOptions) (string, error) {
	return opts.format(ctx)
}
