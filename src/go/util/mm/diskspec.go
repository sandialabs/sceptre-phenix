package mm

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrMalformedDiskSpec is the error for a disk spec that minimega does not
// parse.
var ErrMalformedDiskSpec = errors.New("malformed diskspec")

// diskSpec is one minimega disk spec: <path>[,<interface>][,<cache>]. The
// interface and the cache mode are empty when the spec does not set them.
type diskSpec struct {
	path  string
	iface string
	cache string
}

// parseDiskSpec parses spec with the rules of minimega's ParseDiskConfig. A
// second field is the cache mode when it is one, else the interface. A third
// field is the cache mode, after an interface.
func parseDiskSpec(spec string) (diskSpec, error) {
	f := strings.Split(spec, ",")

	var d diskSpec

	switch {
	case f[0] == "":
		return d, fmt.Errorf("%w %q: no path", ErrMalformedDiskSpec, spec)
	case len(f) == 1:
		d.path = f[0]
	case len(f) == 2 && isDiskCache(f[1]):
		d.path, d.cache = f[0], f[1]
	case len(f) == 2 && isDiskInterface(f[1]):
		d.path, d.iface = f[0], f[1]
	case len(f) == 3 && isDiskInterface(f[1]) && isDiskCache(f[2]):
		d.path, d.iface, d.cache = f[0], f[1], f[2]
	default:
		return d, fmt.Errorf("%w %q", ErrMalformedDiskSpec, spec)
	}

	return d, nil
}

// DiskSpecPath returns the path of the minimega disk spec
// <path>[,<interface>][,<cache>]. It returns an error that wraps
// ErrMalformedDiskSpec when minimega does not parse spec.
func DiskSpecPath(spec string) (string, error) {
	d, err := parseDiskSpec(spec)

	return d.path, err
}

// parseDiskSpecs parses the space-separated disk specs of a VM, as `vm info`
// shows them in its disks column.
func parseDiskSpecs(disks string) ([]diskSpec, error) {
	fields := strings.Fields(disks)
	specs := make([]diskSpec, 0, len(fields))

	for _, f := range fields {
		d, err := parseDiskSpec(f)
		if err != nil {
			return nil, err
		}

		specs = append(specs, d)
	}

	return specs, nil
}

// String returns the spec in the form that parseDiskSpec reads.
func (d diskSpec) String() string {
	parts := []string{d.path}

	if d.iface != "" {
		parts = append(parts, d.iface)
	}

	if d.cache != "" {
		parts = append(parts, d.cache)
	}

	return strings.Join(parts, ",")
}

// joinDiskSpecs returns the specs as the arguments of `vm config disk`.
func joinDiskSpecs(specs []diskSpec) string {
	parts := make([]string, len(specs))

	for i, d := range specs {
		parts[i] = d.String()
	}

	return strings.Join(parts, " ")
}

// isDiskCache reports whether c is a QEMU disk cache mode that minimega
// accepts.
func isDiskCache(c string) bool {
	switch c {
	case "none", "writeback", "unsafe", "directsync", "writethrough":
		return true
	default:
		return false
	}
}

// isDiskInterface reports whether i is a disk interface that minimega
// accepts: the QEMU interfaces and minimega's own "ahci".
func isDiskInterface(i string) bool {
	switch i {
	case "ahci", "ide", "scsi", "sd", "mtd", "floppy", "pflash", "virtio":
		return true
	default:
		return false
	}
}

// redeployDisks is the disk part of a VM redeploy.
type redeployDisks struct {
	// snapshot is the name of the disk snapshot to make again from source,
	// with the injects in it. It is empty when there is no snapshot to make.
	snapshot string
	source   string
	// specs are the arguments of `vm config disk`.
	specs []diskSpec
}

// planRedeployDisks returns how a redeploy changes the disks of a VM, from
// the VM's disks column and the new disk spec. Only the first disk changes. It
// keeps the interface and cache mode of the old first disk that the new spec
// does not set. With injects, the first disk of a VM deployed with a snapshot
// keeps its path and gets a new snapshot of the new disk.
func planRedeployDisks(disks, disk string, injects bool) (redeployDisks, error) {
	var plan redeployDisks

	newDisk, err := parseDiskSpec(disk)
	if err != nil {
		return plan, fmt.Errorf("new disk: %w", err)
	}

	old, err := parseDiskSpecs(disks)
	if err != nil {
		return plan, fmt.Errorf("current disks: %w", err)
	}

	first := newDisk

	if len(old) > 0 {
		first.iface = cmp.Or(newDisk.iface, old[0].iface)
		first.cache = cmp.Or(newDisk.cache, old[0].cache)
	}

	if injects && len(old) > 0 && strings.Contains(filepath.Base(old[0].path), "_snapshot") {
		plan.snapshot = filepath.Base(old[0].path)
		plan.source = newDisk.path
		first.path = old[0].path
	}

	plan.specs = []diskSpec{first}

	if len(old) > 1 {
		plan.specs = append(plan.specs, old[1:]...)
	}

	return plan, nil
}
