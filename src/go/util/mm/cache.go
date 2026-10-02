package mm

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// launchTolerance is how far apart two estimates of a VM's launch time (read
// as "now minus uptime") may be and still name the same launch. The estimates
// differ by however long minimega's response took to arrive; relaunching a VM
// takes far longer.
const launchTolerance = 10 * time.Second

// clusterHostsTTL is how long the full list of cluster hosts is reused for
// callers that only show it (see GetClusterHosts).
const clusterHostsTTL = 5 * time.Second

var (
	vmInfoFlights       flightGroup[VMs]          //nolint:gochecknoglobals // shared by concurrent callers
	clusterHostsFlights flightGroup[clusterHosts] //nolint:gochecknoglobals // shared by concurrent callers
	snapshotDisks       snapshotDiskCache         //nolint:gochecknoglobals // process-lifetime cache
	recentHosts         recentClusterHosts        //nolint:gochecknoglobals // shared by all callers
)

// clusterHosts is one GetClusterHosts result, as shared between the callers of
// one run.
type clusterHosts struct {
	hosts Hosts
	err   error
}

// recentClusterHosts holds the latest full list of cluster hosts.
type recentClusterHosts struct {
	mu    sync.Mutex
	hosts Hosts
	at    time.Time // when the run that read hosts started
}

// vmLaunch identifies one launch of a VM with a snapshot disk. at is zero when
// the launch time is unknown, or the VM's process may not be running.
type vmLaunch struct {
	ns   string
	name string
	uuid string
	host string
	disk string
	at   time.Time
}

type snapshotDiskEntry struct {
	launch  vmLaunch
	backing string
}

// snapshotDiskCache remembers the image each running VM's snapshot disk is
// based on, which otherwise takes a `disk info` (a qemu-img run, usually via
// `mesh send`) per VM every time VMs are listed. A snapshot disk is only ever
// recreated or rebased while its VM's process is gone, and relaunching a VM
// changes its launch time, so a cached result is only reused for the same
// launch.
type snapshotDiskCache struct {
	mu      sync.Mutex
	entries map[string]snapshotDiskEntry
}

// get returns the latest list if it is younger than clusterHostsTTL. Callers
// must not modify it.
func (c *recentClusterHosts) get() (Hosts, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hosts == nil || time.Since(c.at) >= clusterHostsTTL {
		return nil, false
	}

	return c.hosts, true
}

// put stores a list read by a run started at `at`, unless a later run's list
// is already stored. hosts must not be modified afterwards.
func (c *recentClusterHosts) put(hosts Hosts, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hosts != nil && c.at.After(at) {
		return
	}

	c.hosts, c.at = hosts, at
}

func (launch vmLaunch) key() string {
	return launch.ns + "\x00" + launch.name
}

// sameAs reports whether two launches are the same launch of the same VM and
// snapshot disk, as far as can be told.
func (launch vmLaunch) sameAs(other vmLaunch) bool {
	if launch.at.IsZero() || other.at.IsZero() {
		return false
	}

	if launch.ns != other.ns || launch.name != other.name || launch.uuid != other.uuid ||
		launch.host != other.host || launch.disk != other.disk {
		return false
	}

	diff := launch.at.Sub(other.at)

	return diff <= launchTolerance && diff >= -launchTolerance
}

func (c *snapshotDiskCache) lookup(launch vmLaunch) (string, bool) {
	if launch.at.IsZero() {
		return "", false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[launch.key()]
	if !ok || !entry.launch.sameAs(launch) {
		return "", false
	}

	return entry.backing, true
}

func (c *snapshotDiskCache) store(launch vmLaunch, backing string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if launch.at.IsZero() {
		// Don't let a result that can't be reused keep an older one alive.
		delete(c.entries, launch.key())

		return
	}

	if c.entries == nil {
		c.entries = make(map[string]snapshotDiskEntry)
	}

	c.entries[launch.key()] = snapshotDiskEntry{launch: launch, backing: backing}
}

// forget drops every entry for the given namespace.
func (c *snapshotDiskCache) forget(ns string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := ns + "\x00"

	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
}

// forgetSnapshotDisks drops what is known about the snapshot disks of the VMs
// in a namespace, for when phenix itself kills or redeploys them. It is only
// a shortcut: the launch time check catches the same changes made elsewhere.
func forgetSnapshotDisks(ns string) {
	snapshotDisks.forget(ns)
}

// GroupCapturesByVM groups captures by VM name, keeping their order.
func GroupCapturesByVM(captures []Capture) map[string][]Capture {
	grouped := make(map[string][]Capture)

	for _, capture := range captures {
		grouped[capture.VM] = append(grouped[capture.VM], capture)
	}

	return grouped
}

// parseList parses a list column from `vm info`, such as `[a, b]`, returning
// nil for an empty list.
func parseList(s string) []string {
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")

	if s == "" {
		return nil
	}

	return strings.Split(s, ", ")
}

// cloneVMs deep copies VMs as a shared result is handed to each caller,
// keeping nil slices and maps nil so a copy marshals exactly as the original.
func cloneVMs(vms VMs) VMs {
	if vms == nil {
		return nil
	}

	clone := make(VMs, len(vms))

	for i, vm := range vms {
		vm.IPv4 = slices.Clone(vm.IPv4)
		vm.Networks = slices.Clone(vm.Networks)
		vm.Taps = slices.Clone(vm.Taps)
		vm.Captures = slices.Clone(vm.Captures)
		vm.IfaceNames = slices.Clone(vm.IfaceNames)
		vm.Tags = maps.Clone(vm.Tags)
		vm.Labels = maps.Clone(vm.Labels)
		vm.Interfaces = maps.Clone(vm.Interfaces)
		vm.Metadata = maps.Clone(vm.Metadata)
		vm.Annotations = maps.Clone(vm.Annotations)

		clone[i] = vm
	}

	return clone
}

// cloneHosts deep copies hosts as a shared result is handed to each caller,
// keeping nil slices nil.
func cloneHosts(hosts Hosts) Hosts {
	if hosts == nil {
		return nil
	}

	clone := make(Hosts, len(hosts))

	for i, host := range hosts {
		host.Load = slices.Clone(host.Load)

		clone[i] = host
	}

	return clone
}
