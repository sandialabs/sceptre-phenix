package builder

import (
	"cmp"
	"slices"

	"phenix/types/builder"
)

// HostPin is the host pin of a VM: an entry of an experiment's
// spec.schedules, which makes phenix start the VM on that cluster host.
type HostPin struct {
	// Hostname is the hostname of the VM, the key of the entry.
	Hostname string `json:"hostname"`
	// Host is the cluster host that the VM is pinned to.
	Host string `json:"host"`
	// RenamedTo is the new hostname of the device, when the diagram renamed
	// it (see [DeviceRenames]). It is empty otherwise.
	RenamedTo string `json:"renamedTo,omitempty"`
}

// PruneHostPins splits schedules into the pins that name a node of hostnames
// and the removed pins, sorted by hostname. It matches hostnames exactly, case
// included, as the phenix schedulers and minimega do. It returns schedules
// itself when it removes no pin.
func PruneHostPins(schedules map[string]string, hostnames []string) (map[string]string, []HostPin) {
	nodes := make(map[string]bool, len(hostnames))
	for _, hostname := range hostnames {
		nodes[hostname] = true
	}

	var removed []HostPin

	for hostname, host := range schedules {
		if !nodes[hostname] {
			removed = append(removed, HostPin{Hostname: hostname, Host: host, RenamedTo: ""})
		}
	}

	if len(removed) == 0 {
		return schedules, nil
	}

	kept := make(map[string]string, len(schedules)-len(removed))

	for hostname, host := range schedules {
		if nodes[hostname] {
			kept[hostname] = host
		}
	}

	slices.SortFunc(removed, func(a, b HostPin) int { return cmp.Compare(a.Hostname, b.Hostname) })

	return kept, removed
}

// DeviceRenames tells which devices a diagram renamed since a document that
// was published before. The editor keeps the node ID of a device when it
// renames the device.
type DeviceRenames struct {
	// before maps each hostname of a device of the earlier document to the
	// device's node ID.
	before map[string]string
	// after maps the node ID of each device of the diagram to its hostname.
	after map[string]string
}

// NewDeviceRenames compares the devices of the diagram current with those of
// previous. previous is nil when the earlier document is not known.
func NewDeviceRenames(previous, current *builder.Document) DeviceRenames {
	renames := DeviceRenames{before: map[string]string{}, after: map[string]string{}}

	if previous != nil {
		for _, node := range previous.Nodes {
			if node.Kind == builder.NodeKindDevice && node.Device != nil {
				renames.before[node.Device.Hostname] = node.ID
			}
		}
	}

	for _, node := range current.Nodes {
		if node.Kind == builder.NodeKindDevice && node.Device != nil {
			renames.after[node.ID] = node.Device.Hostname
		}
	}

	return renames
}

// RenamedTo returns the hostname that the diagram gives the device that had
// hostname, or "" when the diagram does not rename it. The device is the one
// of the earlier document with that hostname. Without one, it is the device
// that an import of a config gave that hostname: an import gives a device
// the node ID [builder.DeviceNodeID] of its hostname.
func (r DeviceRenames) RenamedTo(hostname string) string {
	id, ok := r.before[hostname]
	if !ok {
		id = builder.DeviceNodeID(hostname)
	}

	if renamed := r.after[id]; renamed != hostname {
		return renamed
	}

	return ""
}
