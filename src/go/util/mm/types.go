package mm

import (
	"errors"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
)

var ErrHostNotFound = errors.New("host not found")

// Hosts is a list of cluster hosts. Its Sort methods are stable: hosts that
// tie keep their order.
type Hosts []Host

func (h Hosts) SortByUnallocatedCPU(asc bool) {
	sort.SliceStable(h, func(i, j int) bool {
		ui := h[i].CPUs - h[i].CPUCommit
		uj := h[j].CPUs - h[j].CPUCommit

		if asc {
			return ui < uj
		}

		return ui > uj
	})
}

func (h Hosts) SortByCommittedCPU(asc bool) {
	sort.SliceStable(h, func(i, j int) bool {
		if asc {
			return h[i].CPUCommit < h[j].CPUCommit
		}

		return h[i].CPUCommit > h[j].CPUCommit
	})
}

func (h Hosts) SortByUnallocatedMem(asc bool) {
	sort.SliceStable(h, func(i, j int) bool {
		ui := h[i].MemTotal - h[i].MemCommit
		uj := h[j].MemTotal - h[j].MemCommit

		if asc {
			return ui < uj
		}

		return ui > uj
	})
}

func (h Hosts) SortByCommittedMem(asc bool) {
	sort.SliceStable(h, func(i, j int) bool {
		if asc {
			return h[i].MemCommit < h[j].MemCommit
		}

		return h[i].MemCommit > h[j].MemCommit
	})
}

func (h Hosts) SortByVMs(asc bool) {
	sort.SliceStable(h, func(i, j int) bool {
		if asc {
			return h[i].VMs < h[j].VMs
		}

		return h[i].VMs > h[j].VMs
	})
}

func (h Hosts) FindHostByName(name string) *Host {
	for _, host := range h {
		if host.Name == name {
			return &host
		}
	}

	return nil
}

func (h Hosts) IncrHostVMs(name string, incr int) error {
	for idx, host := range h {
		if host.Name == name {
			host.VMs += incr
			h[idx] = host

			return nil
		}
	}

	return ErrHostNotFound
}

func (h Hosts) IncrHostCPUCommit(name string, incr int) error {
	for idx, host := range h {
		if host.Name == name {
			host.CPUCommit += incr
			h[idx] = host

			return nil
		}
	}

	return ErrHostNotFound
}

func (h Hosts) IncrHostMemCommit(name string, incr int) error {
	for idx, host := range h {
		if host.Name == name {
			host.MemCommit += incr
			h[idx] = host

			return nil
		}
	}

	return ErrHostNotFound
}

type Cluster struct {
	Hosts []Host `json:"hosts"`
}

type Host struct {
	Name        string     `json:"name"`
	CPUs        int        `json:"cpus"`
	CPUCommit   int        `json:"cpucommit"`
	Load        []string   `json:"load"`
	MemUsed     int        `json:"memused"`
	MemTotal    int        `json:"memtotal"`
	MemCommit   int        `json:"memcommit"`
	Tx          float64    `json:"tx"`
	Rx          float64    `json:"rx"`
	Bandwidth   string     `json:"bandwidth"`
	DiskUsage   *DiskUsage `json:"diskusage,omitempty"` // nil unless the caller asked for it
	NetCommit   int        `json:"netcommit"`
	VMs         int        `json:"vms"`
	Uptime      float64    `json:"uptime"`
	Schedulable bool       `json:"schedulable"`
	Headnode    bool       `json:"headnode"`
}

type DiskUsage struct {
	Phenix   float64 `json:"diskphenix"`
	Minimega float64 `json:"diskminimega"`
}

type VMs []VM

func (v VMs) SortByName(asc bool) {
	sort.Slice(v, func(i, j int) bool {
		if asc {
			return strings.ToLower(v[i].Name) < strings.ToLower(v[j].Name)
		}

		return strings.ToLower(v[i].Name) > strings.ToLower(v[j].Name)
	})
}

func (v VMs) SortByHost(asc bool) {
	sort.Slice(v, func(i, j int) bool {
		if asc {
			return strings.ToLower(v[i].Host) < strings.ToLower(v[j].Host)
		}

		return strings.ToLower(v[i].Host) > strings.ToLower(v[j].Host)
	})
}

func (v VMs) SortByUptime(asc bool) {
	sort.Slice(v, func(i, j int) bool {
		if asc {
			return v[i].Uptime < v[j].Uptime
		}

		return v[i].Uptime > v[j].Uptime
	})
}

func (v VMs) SortBy(col string, asc bool) {
	switch col {
	case "name":
		v.SortByName(asc)
	case hostColumn:
		v.SortByHost(asc)
	case "uptime":
		v.SortByUptime(asc)
	}
}

func (v VMs) Paginate(page, size int) VMs {
	var (
		start = (page - 1) * size
		end   = start + size
	)

	if start >= len(v) {
		return VMs{}
	}

	if end > len(v) {
		end = len(v)
	}

	return v[start:end]
}

type VM struct {
	ID              int               `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Type            string            `json:"type"`
	Experiment      string            `json:"experiment"`
	Host            string            `json:"host"`
	IPv4            []string          `json:"ipv4"`
	CPUs            int               `json:"cpus"`
	RAM             int               `json:"ram"`
	Disk            string            `json:"disk"`
	InjectPartition int               `json:"inject_partition"`
	OSType          string            `json:"osType"`
	DoNotBoot       bool              `json:"dnb"`
	Networks        []string          `json:"networks"`
	Taps            []string          `json:"taps"`
	Captures        []Capture         `json:"captures"`
	State           string            `json:"state"`
	Running         bool              `json:"running"`
	Busy            bool              `json:"busy"`
	CCActive        bool              `json:"ccActive"`
	Uptime          float64           `json:"uptime"`
	Screenshot      string            `json:"screenshot,omitempty"`
	CdRom           string            `json:"cdRom"`
	Tags            map[string]string `json:"tags"`
	Snapshot        bool              `json:"snapshot"`
	Labels          map[string]string `json:"labels"`

	// Used internally to track network <--> IP relationship, since
	// network ordering from minimega may not be the same as network
	// ordering in the experiment database.
	Interfaces map[string]string `json:"-"`

	// IfaceNames holds the topology-declared name of each network
	// interface, index-aligned with Networks. It's used internally to
	// resolve a user-supplied interface name (as opposed to index) to the
	// interface's index for operations like packet captures.
	IfaceNames []string `json:"-"`

	// Used internally for showing VM details.
	Metadata    map[string]any `json:"-"`
	Annotations map[string]any `json:"-"`

	// Used internally to check for active CC agent.
	UUID string `json:"-"`
}

// OnAnyHost reports whether the VM runs on one of hosts, named exactly as
// minimega reports them; a VM with no host matches none.
func (v VM) OnAnyHost(hosts []string) bool {
	return v.Host != "" && slices.Contains(hosts, v.Host)
}

// Copy returns a deep copy of the VM. No slice or map field of the copy
// shares memory with v. Copy makes shallow copies of the values in Metadata
// and Annotations.
func (v VM) Copy() VM {
	vm := v

	vm.IPv4 = make([]string, len(v.IPv4))
	copy(vm.IPv4, v.IPv4)

	vm.Networks = make([]string, len(v.Networks))
	copy(vm.Networks, v.Networks)

	vm.IfaceNames = make([]string, len(v.IfaceNames))
	copy(vm.IfaceNames, v.IfaceNames)

	vm.Taps = make([]string, len(v.Taps))
	copy(vm.Taps, v.Taps)

	// This works because the Capture struct is only made up of primitives.
	vm.Captures = make([]Capture, len(v.Captures))
	copy(vm.Captures, v.Captures)

	vm.Tags = make(map[string]string, len(v.Tags))
	maps.Copy(vm.Tags, v.Tags)

	vm.Labels = maps.Clone(v.Labels)
	vm.Interfaces = maps.Clone(v.Interfaces)
	vm.Metadata = maps.Clone(v.Metadata)
	vm.Annotations = maps.Clone(v.Annotations)

	return vm
}

type Captures struct {
	Captures []Capture `json:"captures"`
}

// Capture is a packet capture on one VM interface. The fields after Filepath are zero with a minimega older than
// MinCaptureVersion.
type Capture struct {
	VM        string `json:"vm"`
	Interface int    `json:"interface"`
	Filepath  string `json:"filepath"`

	ID      string `json:"id,omitempty"`
	State   string `json:"state,omitempty"`
	Format  string `json:"format,omitempty"`
	Packets uint64 `json:"packets,omitempty"`
	KDrops  uint64 `json:"kdrops,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Error   string `json:"error,omitempty"`
}

// CaptureCounters are the packet counters of an experiment capture, or of one of its streams.
type CaptureCounters struct {
	Ports, Packets, Bytes, KDrops, PDrops, QDrops, Lost, Late uint64
	Lag, Skew                                                 time.Duration
}

// CaptureStream is the part of an experiment capture that one host records from one bridge.
type CaptureStream struct {
	CaptureCounters

	Host, Bridge, State string
}

// NamespaceCapture is an experiment capture: one capture of all the VLANs, or of the given VLANs, of a namespace.
// Its counters are those of its summary row. Its streams are sorted by host, then bridge.
type NamespaceCapture struct {
	CaptureCounters

	ID, State, Path, Format, Filter, Start, Error string
	VLANs                                         []string
	Size                                          int64
	Streams                                       []CaptureStream
}

// Running reports whether minimega still records the capture.
func (c NamespaceCapture) Running() bool {
	switch c.State {
	case "starting", "running", "stalled", "stopping":
		return true
	}

	return false
}

// BridgeMirror is one Open vSwitch mirror on a bridge of a host, as `bridge mirrors` lists it.
type BridgeMirror struct {
	Host, Bridge, Mirror, Output, Namespace, Kind string
}

type BlockDumpResponse struct {
	Return struct {
		Completed int    `json:"completed"`
		Status    string `json:"status"`
		Total     int    `json:"total"`
	} `json:"return"`
}
