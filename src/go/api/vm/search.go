package vm

import "fmt"

type TopologySearch struct {
	Hostname   map[string]int   `json:"hostname"`
	Disk       map[string][]int `json:"disk"`
	Type       map[string][]int `json:"node-type"`
	OSType     map[string][]int `json:"os-type"`
	Label      map[string][]int `json:"label"`
	Annotation map[string][]int `json:"annotation"`
	VLAN       map[string][]int `json:"vlan"`
	IP         map[string][]int `json:"ip"`
}

func (this *TopologySearch) AddHostname(k string, n int) {
	if this.Hostname == nil {
		this.Hostname = make(map[string]int)
	}

	this.Hostname[k] = n
}

func (this *TopologySearch) AddDisk(k string, n int) {
	if this.Disk == nil {
		this.Disk = make(map[string][]int)
	}

	this.Disk[k] = append(this.Disk[k], n)
}

func (this *TopologySearch) AddType(k string, n int) {
	if this.Type == nil {
		this.Type = make(map[string][]int)
	}

	this.Type[k] = append(this.Type[k], n)
}

func (this *TopologySearch) AddOSType(k string, n int) {
	if this.OSType == nil {
		this.OSType = make(map[string][]int)
	}

	this.OSType[k] = append(this.OSType[k], n)
}

func (this *TopologySearch) AddLabel(k, v string, n int) {
	if this.Label == nil {
		this.Label = make(map[string][]int)
	}

	k = fmt.Sprintf("%s=%s", k, v)

	this.Label[k] = append(this.Label[k], n)
}

func (this *TopologySearch) AddAnnotation(k string, n int) {
	if this.Annotation == nil {
		this.Annotation = make(map[string][]int)
	}

	this.Annotation[k] = append(this.Annotation[k], n)
}

func (this *TopologySearch) AddVLAN(k string, n int) {
	if this.VLAN == nil {
		this.VLAN = make(map[string][]int)
	}

	this.VLAN[k] = append(this.VLAN[k], n)
}

func (this *TopologySearch) AddIP(k string, n int) {
	if this.IP == nil {
		this.IP = make(map[string][]int)
	}

	this.IP[k] = append(this.IP[k], n)
}
