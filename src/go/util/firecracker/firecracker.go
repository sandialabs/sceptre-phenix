// Package firecracker holds the limits of minimega's firecracker VM type that
// phenix checks before it launches a node, so that topology validation and
// the Builder preflight checks apply the same rules.
package firecracker

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// VMType is the general.vm_type of a node that minimega runs as a
	// Firecracker microVM.
	VMType = "firecracker"

	// MaxVCPUs is the most vCPUs a Firecracker VM can have.
	MaxVCPUs = 32

	// MaxDevices is the most virtio devices a Firecracker VM can have on
	// x86_64 with the virtio-MMIO transport, and MaxDevicesPCI the most with
	// the PCI transport (firecracker-pci).
	MaxDevices    = 17
	MaxDevicesPCI = 31

	// EntropyKey and PCIKey are the minimega vm config settings, set through
	// a node's advanced section, that add or remove devices.
	EntropyKey = "firecracker-entropy"
	PCIKey     = "firecracker-pci"

	// DefaultRoot is what phenix adds to the kernel arguments of a node that
	// does not set root=, to boot the first partition of the first drive.
	DefaultRoot = "root=/dev/vda1 rw"
)

// Devices is what a Firecracker VM attaches as virtio devices: its disks, its
// NICs, the vsock device (always counted, because cc can use it), and the
// entropy device unless it is turned off. PCI tells which device limit
// applies.
type Devices struct {
	Disks   int
	NICs    int
	Entropy bool
	PCI     bool
}

// NewDevices returns the devices of a VM with the given disks and NICs.
// entropy and pci are the values of the firecracker-entropy and
// firecracker-pci settings, or "" when they are not set. The entropy device is
// on unless entropy is false, and the PCI transport is off unless pci is true.
func NewDevices(disks, nics int, entropy, pci string) Devices {
	devices := Devices{Disks: disks, NICs: nics, Entropy: true, PCI: false}

	if on, err := strconv.ParseBool(strings.TrimSpace(entropy)); err == nil {
		devices.Entropy = on
	}

	if on, err := strconv.ParseBool(strings.TrimSpace(pci)); err == nil {
		devices.PCI = on
	}

	return devices
}

// Count returns how many virtio devices the VM needs.
func (d Devices) Count() int {
	count := d.Disks + d.NICs + 1

	if d.Entropy {
		count++
	}

	return count
}

// Limit returns the most virtio devices the VM can have.
func (d Devices) Limit() int {
	if d.PCI {
		return MaxDevicesPCI
	}

	return MaxDevices
}

// Fits reports whether the VM's devices are within the limit.
func (d Devices) Fits() bool {
	return d.Count() <= d.Limit()
}

// String describes the devices and the limit, for example "19 Firecracker
// devices (2 disks, 15 NICs, vsock, entropy); the limit is 17 without
// firecracker-pci".
func (d Devices) String() string {
	parts := []string{plural(d.Disks, "disk", "disks"), plural(d.NICs, "NIC", "NICs"), "vsock"}

	if d.Entropy {
		parts = append(parts, "entropy")
	}

	transport := "without " + PCIKey
	if d.PCI {
		transport = "with " + PCIKey
	}

	return fmt.Sprintf(
		"%d Firecracker devices (%s); the limit is %d %s",
		d.Count(), strings.Join(parts, ", "), d.Limit(), transport,
	)
}

// VCPUsFit reports whether a Firecracker VM can have vcpus vCPUs.
func VCPUsFit(vcpus int) bool {
	return vcpus >= 1 && vcpus <= MaxVCPUs
}

// IsQCOW2 reports whether image names a qcow2 file (.qcow2 or .qc2), which
// Firecracker can boot only from a snapshot copy that minimega converts.
func IsQCOW2(image string) bool {
	switch strings.ToLower(filepath.Ext(image)) {
	case ".qcow2", ".qc2":
		return true
	}

	return false
}

// KernelArgs returns the kernel command line for a node with the given
// kernel_args. It adds [DefaultRoot] when args sets no root=.
func KernelArgs(args string) string {
	args = strings.TrimSpace(args)

	for field := range strings.FieldsSeq(args) {
		if strings.HasPrefix(field, "root=") {
			return args
		}
	}

	if args == "" {
		return DefaultRoot
	}

	return args + " " + DefaultRoot
}

func plural(n int, one, many string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + one
	}

	return strconv.Itoa(n) + " " + many
}
