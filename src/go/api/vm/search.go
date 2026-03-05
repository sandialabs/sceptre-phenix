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

func (s *TopologySearch) AddHostname(k string, n int) {
	if s.Hostname == nil {
		s.Hostname = make(map[string]int)
	}

	s.Hostname[k] = n
}

func (s *TopologySearch) AddDisk(k string, n int) {
	if s.Disk == nil {
		s.Disk = make(map[string][]int)
	}

	s.Disk[k] = append(s.Disk[k], n)
}

func (s *TopologySearch) AddType(k string, n int) {
	if s.Type == nil {
		s.Type = make(map[string][]int)
	}

	s.Type[k] = append(s.Type[k], n)
}

func (s *TopologySearch) AddOSType(k string, n int) {
	if s.OSType == nil {
		s.OSType = make(map[string][]int)
	}

	s.OSType[k] = append(s.OSType[k], n)
}

func (s *TopologySearch) AddLabel(k, v string, n int) {
	if s.Label == nil {
		s.Label = make(map[string][]int)
	}

	k = fmt.Sprintf("%s=%s", k, v)

	s.Label[k] = append(s.Label[k], n)
}

func (s *TopologySearch) AddAnnotation(k string, n int) {
	if s.Annotation == nil {
		s.Annotation = make(map[string][]int)
	}

	s.Annotation[k] = append(s.Annotation[k], n)
}

func (s *TopologySearch) AddVLAN(k string, n int) {
	if s.VLAN == nil {
		s.VLAN = make(map[string][]int)
	}

	s.VLAN[k] = append(s.VLAN[k], n)
}

func (s *TopologySearch) AddIP(k string, n int) {
	if s.IP == nil {
		s.IP = make(map[string][]int)
	}

	s.IP[k] = append(s.IP[k], n)
}
