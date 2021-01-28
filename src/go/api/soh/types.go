package soh

import (
	"fmt"
	"time"
)

type Font struct {
	Color string `json:"color"`
	Align string `json:"align"`
}

type Node struct {
	ID     int        `json:"id"`
	Label  string     `json:"label"`
	Image  string     `json:"image"`
	Fonts  Font       `json:"font"`
	Status string     `json:"status"`
	SOH    *HostState `json:"soh"`
}

type Edge struct {
	ID     int `json:"id"`
	Source int `json:"source"`
	Target int `json:"target"`
	Length int `json:"length"`
}

type Network struct {
	Started         bool     `json:"started"`
	Nodes           []Node   `json:"nodes"`
	Edges           []Edge   `json:"edges"`
	NetworkEvents   string   `json:"networkEvents"`
	RunningCount    int      `json:"running_count"`
	NotRunningCount int      `json:"notrunning_count"`
	NotDeployCount  int      `json:"notdeploy_count"`
	NotBootCount    int      `json:"notboot_count"`
	TotalCount      int      `json:"total_count"`
	Hosts           []string `json:"hosts"`
	HostFlows       [][]int  `json:"host_flows"`
}

type Reachability struct {
	Hostname  string `json:"hostname" mapstructure:"hostname" structs:"hostname"`
	Timestamp string `json:"timestamp" mapstructure:"timestamp" structs:"timestamp"`
	Error     string `json:"error" mapstructure:"error" structs:"error"`
}

type Process struct {
	Process   string `json:"process" mapstructure:"process" structs:"process"`
	Timestamp string `json:"timestamp" mapstructure:"timestamp" structs:"timestamp"`
	Error     string `json:"error" mapstructure:"error" structs:"error"`
}

type Listener struct {
	Listener  string `json:"listener" mapstructure:"listener" structs:"listener"`
	Timestamp string `json:"timestamp" mapstructure:"timestamp" structs:"timestamp"`
	Error     string `json:"error" mapstructure:"error" structs:"error"`
}

type HostState struct {
	Hostname     string         `json:"hostname" mapstructure:"hostname" structs:"hostname"`
	CPULoad      string         `json:"cpuLoad" mapstructure:"cpuLoad" structs:"cpuLoad"`
	Reachability []Reachability `json:"reachability,omitempty" mapstructure:"reachability,omitempty" structs:"reachability,omitempty"`
	Processes    []Process      `json:"processes,omitempty" mapstructure:"processes,omitempty" structs:"processes,omitempty"`
	Listeners    []Listener     `json:"listeners,omitempty" mapstructure:"listeners,omitempty" structs:"listeners,omitempty"`
}

type flowsStruct struct {
	Source struct {
		IP    string `json:"ip"`
		Bytes int    `json:"bytes"`
	} `json:"source"`
	Destination struct {
		IP    string `json:"ip"`
		Bytes int    `json:"bytes"`
	} `json:"destination"`
}

type packetCapture struct {
	ElasticImage    string              `mapstructure:"elasticImage"`
	PacketBeatImage string              `mapstructure:"packetBeatImage"`
	ElasticServer   elasticServer       `mapstructure:"elasticServer"`
	CaptureHosts    map[string][]string `mapstructure:"captureHosts"`
}

type elasticServer struct {
	Hostname  string `mapstructure:"hostname"`
	VCPU      int    `mapstructure:"vcpus"`
	Memory    int    `mapstructure:"memory"`
	IPAddress string `mapstructure:"ipAddress"`
	VLAN      string `mapstructure:"vlan"`
}

type sohMetadata struct {
	AppProfileKey     string              `mapstructure:"appMetadataProfileKey"`
	C2Timeout         string              `mapstructure:"c2Timeout"`
	ExitOnError       bool                `mapstructure:"exitOnError"`
	HostListeners     map[string][]string `mapstructure:"hostListeners"`
	HostProcesses     map[string][]string `mapstructure:"hostProcesses"`
	InjectICMPAllow   bool                `mapstructure:"injectICMPAllow"`
	PacketCapture     packetCapture       `mapstructure:"packetCapture"`
	Reachability      string              `mapstructure:"testReachability"`
	SkipNetworkConfig bool                `mapstructure:"skipInitialNetworkConfigTests"`
	SkipHosts         []string            `mapstructure:"skipHosts"`

	// set after parsing
	c2Timeout time.Duration
}

func (this *sohMetadata) init() error {
	if this.SkipNetworkConfig {
		// Default reachability test to off if skipping initial network config
		// tests.
		this.Reachability = "off"
	}

	if this.Reachability == "" {
		// Default to reachability test being disabled if not specified in the
		// scenario app config.
		this.Reachability = "off"
	}

	if this.Reachability == "off" {
		// Default to ICMP rule injection being disabled if reachability testing is
		// disabled.
		this.InjectICMPAllow = false
	}

	if this.C2Timeout == "" {
		// Default C2 timeout to 5m if not specified in the scenario app config.
		this.c2Timeout = 5 * time.Minute
	} else {
		var err error

		if this.c2Timeout, err = time.ParseDuration(this.C2Timeout); err != nil {
			return fmt.Errorf("parsing C2 timeout setting '%s': %w", this.C2Timeout, err)
		}
	}

	if this.AppProfileKey == "" {
		this.AppProfileKey = "sohProfile"
	}

	return nil
}

type sohProfile struct {
	C2Timeout string   `mapstructure:"c2Timeout"`
	Processes []string `mapstructure:"processes"`
	Listeners []string `mapstructure:"listeners"`
	Captures  []string `mapstructure:"captureInterfaces"`

	// set after parsing
	c2Timeout time.Duration
}

func (this *sohProfile) init() error {
	if this.C2Timeout == "" {
		// Default C2 timeout to 5m if not specified in the SoH Profile config.
		this.c2Timeout = 5 * time.Minute
	} else {
		var err error

		if this.c2Timeout, err = time.ParseDuration(this.C2Timeout); err != nil {
			return fmt.Errorf("parsing C2 timeout setting '%s': %w", this.C2Timeout, err)
		}
	}

	return nil
}
