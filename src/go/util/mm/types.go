package mm

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

var ErrHostNotFound = errors.New("host not found")

type Hosts []Host

func (this Hosts) SortByUnallocatedCPU(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		ui := this[i].CPUs - this[i].CPUCommit
		uj := this[j].CPUs - this[j].CPUCommit

		if asc {
			return ui < uj
		}

		return ui > uj
	})
}

func (this Hosts) SortByCommittedCPU(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return this[i].CPUCommit < this[j].CPUCommit
		}

		return this[i].CPUCommit > this[j].CPUCommit
	})
}

func (this Hosts) SortByUnallocatedMem(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		ui := this[i].MemTotal - this[i].MemCommit
		uj := this[j].MemTotal - this[j].MemCommit

		if asc {
			return ui < uj
		}

		return ui > uj
	})
}

func (this Hosts) SortByCommittedMem(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return this[i].MemCommit < this[j].MemCommit
		}

		return this[i].MemCommit > this[j].MemCommit
	})
}

func (this Hosts) SortByVMs(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return this[i].VMs < this[j].VMs
		}

		return this[i].VMs > this[j].VMs
	})
}

func (this Hosts) FindHostByName(name string) *Host {
	for _, host := range this {
		if host.Name == name {
			return &host
		}
	}

	return nil
}

func (this Hosts) IncrHostVMs(name string, incr int) error {
	for idx, host := range this {
		if host.Name == name {
			host.VMs += incr
			this[idx] = host

			return nil
		}
	}

	return ErrHostNotFound
}

func (this Hosts) IncrHostCPUCommit(name string, incr int) error {
	for idx, host := range this {
		if host.Name == name {
			host.CPUCommit += incr
			this[idx] = host

			return nil
		}
	}

	return ErrHostNotFound
}

func (this Hosts) IncrHostMemCommit(name string, incr int) error {
	for idx, host := range this {
		if host.Name == name {
			host.MemCommit += incr
			this[idx] = host

			return nil
		}
	}

	return ErrHostNotFound
}

type Cluster struct {
	Hosts []Host `json:"hosts"`
}

type Host struct {
	Name        string   `json:"name"`
	CPUs        int      `json:"cpus"`
	CPUCommit   int      `json:"cpucommit"`
	Load        []string `json:"load"`
	MemUsed     int      `json:"memused"`
	MemTotal    int      `json:"memtotal"`
	MemCommit   int      `json:"memcommit"`
	Tx          float64  `json:"tx"`
	Rx          float64  `json:"rx"`
	Bandwidth   string   `json:"bandwidth"`
	NetCommit   int      `json:"netcommit"`
	VMs         int      `json:"vms"`
	Uptime      float64  `json:"uptime"`
	Schedulable bool     `json:"schedulable"`
	Headnode    bool     `json:"headnode"`
}

type VMs []VM

func (this VMs) SortByName(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return strings.ToLower(this[i].Name) < strings.ToLower(this[j].Name)
		}

		return strings.ToLower(this[i].Name) > strings.ToLower(this[j].Name)
	})
}

func (this VMs) SortByHost(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return strings.ToLower(this[i].Host) < strings.ToLower(this[j].Host)
		}

		return strings.ToLower(this[i].Host) > strings.ToLower(this[j].Host)
	})
}

func (this VMs) SortByUptime(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return this[i].Uptime < this[j].Uptime
		}

		return this[i].Uptime > this[j].Uptime
	})
}

func (this VMs) SortBy(col string, asc bool) {
	switch col {
	case "name":
		this.SortByName(asc)
	case "host":
		this.SortByHost(asc)
	case "uptime":
		this.SortByUptime(asc)
	}
}

func (this VMs) Paginate(page, size int) VMs {
	var (
		start = (page - 1) * size
		end   = start + size
	)

	if start >= len(this) {
		return VMs{}
	}

	if end > len(this) {
		end = len(this)
	}

	return this[start:end]
}

type VM struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	Experiment string    `json:"experiment"`
	Host       string    `json:"host"`
	IPv4       []string  `json:"ipv4"`
	CPUs       int       `json:"cpus"`
	RAM        int       `json:"ram"`
	Disk       string    `json:"disk"`
	DoNotBoot  bool      `json:"dnb"`
	Networks   []string  `json:"networks"`
	Taps       []string  `json:"taps"`
	Captures   []Capture `json:"captures"`
	Running    bool      `json:"running"`
	Busy       bool      `json:"busy"`
	CCActive   bool      `json:"ccActive"`
	Uptime     float64   `json:"uptime"`
	Screenshot string    `json:"screenshot,omitempty"`
	CdRom      string    `json:"cdRom"`
	Tags       []string  `json:"tags"`

	// Used internally to track network <--> IP relationship, since
	// network ordering from minimega may not be the same as network
	// ordering in the experiment database.
	Interfaces map[string]string `json:"-"`

	// Used internally for showing VM details.
	Type        string                 `json:"-"`
	OSType      string                 `json:"-"`
	Metadata    map[string]interface{} `json:"-"`
	Labels      map[string]string      `json:"-"`
	Annotations map[string]interface{} `json:"-"`

	// Used internally to track state of VM in minimega.
	State string `json:"-"`

	// Used internally to check for active CC agent.
	UUID string `json:"-"`
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

	cfg := diskConfig{
		path: tokens[0],
		base: filepath.Base(tokens[0]),
	}

	if len(tokens) > 1 {
		cfg.cache = tokens[1]
	}

	return cfg
}

func (this diskConfig) string(cache string) string {
	if cache != "" {
		return fmt.Sprintf("%s,%s", this.path, cache)
	}

	if this.cache != "" {
		return fmt.Sprintf("%s,%s", this.path, this.cache)
	}

	return this.path
}
