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
	ID     int    `json:"id"`
	Type   string `json:"type"`
	Source int    `json:"source"`
	Target int    `json:"target"`
	Length int    `json:"length"`
}

type Network struct {
	ExpStarted     bool     `json:"started"`
	SOHInitialized bool     `json:"soh_initialized"`
	SOHRunning     bool     `json:"soh_running"`
	Nodes          []Node   `json:"nodes"`
	Edges          []Edge   `json:"edges"`
	Hosts          []string `json:"hosts"`
	HostFlows      [][]int  `json:"host_flows"`
}

type State struct {
	Metadata  map[string]interface{} `json:"metadata" mapstructure:"metadata" structs:"metadata"`
	Timestamp string                 `json:"timestamp" mapstructure:"timestamp" structs:"timestamp"`
	Success   string                 `json:"success" mapstructure:"success" structs:"success"`
	Error     string                 `json:"error" mapstructure:"error" structs:"error"`
}

type HostState struct {
	Hostname     string  `json:"hostname" mapstructure:"hostname" structs:"hostname"`
	CPULoad      string  `json:"cpuLoad" mapstructure:"cpuLoad" structs:"cpuLoad"`
	Networking   []State `json:"networking,omitempty" mapstructure:"networking,omitempty" structs:"networking,omitempty"`
	Reachability []State `json:"reachability,omitempty" mapstructure:"reachability,omitempty" structs:"reachability,omitempty"`
	Processes    []State `json:"processes,omitempty" mapstructure:"processes,omitempty" structs:"processes,omitempty"`
	Listeners    []State `json:"listeners,omitempty" mapstructure:"listeners,omitempty" structs:"listeners,omitempty"`
	CustomTests  []State `json:"customTests,omitempty" mapstructure:"customTests,omitempty" structs:"customTests,omitempty"`

	// populated before sending to UI client
	Errors bool `json:"errors" mapstructure:"-" structs:"-"`
}

func (this HostState) AllStates() []State {
	var all []State

	all = append(all, this.Networking...)
	all = append(all, this.Reachability...)
	all = append(all, this.Processes...)
	all = append(all, this.Listeners...)
	all = append(all, this.CustomTests...)

	return all
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

type customReachability struct {
	Src    string `mapstructure:"src"`
	Dst    string `mapstructure:"dst"`
	Proto  string `mapstructure:"proto"`
	Port   int    `mapstructure:"port"`
	Wait   string `mapstructure:"wait"`
	Packet string `mapstructure:"udpPacketBase64"`
}

type customHostTest struct {
	Name           string `mapstructure:"name"`
	TestScript     string `mapstructure:"testScript"`
	Executor       string `mapstructure:"executor"`
	TestStdout     string `mapstructure:"testStdout"`
	TestStderr     string `mapstructure:"testStderr"`
	ValidateStdout string `mapstructure:"validateStdout"`
	ValidateStderr string `mapstructure:"validateStderr"`
}

type sohMetadata struct {
	AppProfileKey      string                      `mapstructure:"appMetadataProfileKey"`
	C2Timeout          string                      `mapstructure:"c2Timeout"`
	ExitOnError        bool                        `mapstructure:"exitOnError"`
	HostListeners      map[string][]string         `mapstructure:"hostListeners"`
	HostProcesses      map[string][]string         `mapstructure:"hostProcesses"`
	CustomHostTests    map[string][]customHostTest `mapstructure:"hostCustomTests"`
	InjectICMPAllow    bool                        `mapstructure:"injectICMPAllow"`
	PacketCapture      packetCapture               `mapstructure:"packetCapture"`
	Reachability       string                      `mapstructure:"testReachability"`
	CustomReachability []customReachability        `mapstructure:"testCustomReachability"`
	SkipNetworkConfig  bool                        `mapstructure:"skipInitialNetworkConfigTests"`
	SkipHosts          []string                    `mapstructure:"skipHosts"`

	// The `hostsToUseUUIDForC2Active` setting can be either a string or a slice
	// of strings. Decoding `hostsToUseUUIDForC2Active` into `UseUUIDForC2Active`
	// as a generic interface{} causes mapstructure.Decode to panic. We are using
	// the `mapstructure:",remain"` option below as a workaround.
	// UseUUIDForC2Active interface{} `mapstructure:"hostsToUseUUIDForC2Active"`

	Other map[string]interface{} `mapstructure:",remain"`

	// set after parsing
	c2Timeout time.Duration
	uuidHosts map[string]struct{}
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

	this.uuidHosts = make(map[string]struct{})

	if useUUID, ok := this.Other["hostsToUseUUIDForC2Active"]; ok {
		switch hosts := useUUID.(type) {
		case nil: // this is okay
		case string:
			this.uuidHosts[hosts] = struct{}{}
		case []interface{}:
			if len(hosts) > 0 {
				for _, host := range hosts {
					h, ok := host.(string)
					if !ok {
						return fmt.Errorf("parsing 'hostsToUseUUIDForC2Active': must be a string or string slice")
					}

					this.uuidHosts[h] = struct{}{}
				}
			}
		default:
			return fmt.Errorf("parsing 'hostsToUseUUIDForC2Active': must be a string or string slice")
		}
	}

	return nil
}

func (this sohMetadata) useUUIDForC2Active(host string) bool {
	if _, ok := this.uuidHosts["all"]; ok {
		return true
	}

	if _, ok := this.uuidHosts[host]; ok {
		return true
	}

	return false
}

type sohProfile struct {
	C2Timeout   string           `mapstructure:"c2Timeout"`
	Processes   []string         `mapstructure:"processes"`
	Listeners   []string         `mapstructure:"listeners"`
	CustomTests []customHostTest `mapstructure:"customTests"`
	Captures    []string         `mapstructure:"captureInterfaces"`

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
