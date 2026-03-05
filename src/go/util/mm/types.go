package mm

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"
)

var ErrHostNotFound = errors.New("host not found")

type Hosts []Host

func (h Hosts) SortByUnallocatedCPU(asc bool) {
	sort.Slice(h, func(i, j int) bool {
		ui := h[i].CPUs - h[i].CPUCommit
		uj := h[j].CPUs - h[j].CPUCommit

		if asc {
			return ui < uj
		}

		return ui > uj
	})
}

func (h Hosts) SortByCommittedCPU(asc bool) {
	sort.Slice(h, func(i, j int) bool {
		if asc {
			return h[i].CPUCommit < h[j].CPUCommit
		}

		return h[i].CPUCommit > h[j].CPUCommit
	})
}

func (h Hosts) SortByUnallocatedMem(asc bool) {
	sort.Slice(h, func(i, j int) bool {
		ui := h[i].MemTotal - h[i].MemCommit
		uj := h[j].MemTotal - h[j].MemCommit

		if asc {
			return ui < uj
		}

		return ui > uj
	})
}

func (h Hosts) SortByCommittedMem(asc bool) {
	sort.Slice(h, func(i, j int) bool {
		if asc {
			return h[i].MemCommit < h[j].MemCommit
		}

		return h[i].MemCommit > h[j].MemCommit
	})
}

func (h Hosts) SortByVMs(asc bool) {
	sort.Slice(h, func(i, j int) bool {
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
	Name        string    `json:"name"`
	CPUs        int       `json:"cpus"`
	CPUCommit   int       `json:"cpucommit"`
	Load        []string  `json:"load"`
	MemUsed     int       `json:"memused"`
	MemTotal    int       `json:"memtotal"`
	MemCommit   int       `json:"memcommit"`
	Tx          float64   `json:"tx"`
	Rx          float64   `json:"rx"`
	Bandwidth   string    `json:"bandwidth"`
	DiskUsage   DiskUsage `json:"diskusage"`
	NetCommit   int       `json:"netcommit"`
	VMs         int       `json:"vms"`
	Uptime      float64   `json:"uptime"`
	Schedulable bool      `json:"schedulable"`
	Headnode    bool      `json:"headnode"`
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
	case "host":
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

	// Used internally to track network <--> IP relationship, since
	// network ordering from minimega may not be the same as network
	// ordering in the experiment database.
	Interfaces map[string]string `json:"-"`

	// Used internally for showing VM details.
	Metadata    map[string]any    `json:"-"`
	Labels      map[string]string `json:"-"`
	Annotations map[string]any    `json:"-"`

	// Used internally to check for active CC agent.
	UUID string `json:"-"`
}

// Copy returns a deep copy of the VM. It only makes deep copies of fields that
// are exported as JSON.
func (v VM) Copy() VM {
	vm := v

	vm.IPv4 = make([]string, len(v.IPv4))
	copy(vm.IPv4, v.IPv4)

	vm.Networks = make([]string, len(v.Networks))
	copy(vm.Networks, v.Networks)

	vm.Taps = make([]string, len(v.Taps))
	copy(vm.Taps, v.Taps)

	// This works because the Capture struct is only made up of primitives.
	vm.Captures = make([]Capture, len(v.Captures))
	copy(vm.Captures, v.Captures)

	vm.Tags = make(map[string]string, len(v.Tags))
	maps.Copy(vm.Tags, v.Tags)

	return vm
}

type Captures struct {
	Captures []Capture `json:"captures"`
}

type Capture struct {
	VM        string `json:"vm"`
	Interface int    `json:"interface"`
	Filepath  string `json:"filepath"`
}

type BlockDevice struct {
	Device   string `json:"device"`
	Inserted *struct {
		File string `json:"file"`
	} `json:"inserted"`
}

type BlockDeviceJobs struct {
	Device string `json:"device"`
	Status string `json:"io-status"`
	Length int    `json:"len"`
	Offset int    `json:"offset"`
}

type BlockDumpResponse struct {
	Return struct {
		Completed int    `json:"completed"`
		Status    string `json:"status"`
		Total     int    `json:"total"`
	} `json:"return"`
}

type diskConfig struct {
	path  string
	base  string
	cache string
}

func newDiskConfig(c string) diskConfig {
	tokens := strings.Split(c, ",")

	cfg := diskConfig{ //nolint:exhaustruct // partial initialization
		path: tokens[0],
		base: filepath.Base(tokens[0]),
	}

	if len(tokens) > 1 {
		cfg.cache = tokens[1]
	}

	return cfg
}

func (d diskConfig) string(cache string) string {
	if cache != "" {
		return fmt.Sprintf("%s,%s", d.path, cache)
	}

	if d.cache != "" {
		return fmt.Sprintf("%s,%s", d.path, d.cache)
	}

	return d.path
}
