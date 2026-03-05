package soh

import (
	"errors"
	"fmt"
	"time"
)

const (
	reachabilityOff  = "off"
	defaultC2Timeout = 5 * time.Minute
	notifyInterval   = 5 * time.Second
	c2RetryDelay     = 5 * time.Second
	monitorMemory    = 512
)

type Node struct {
	ID     int               `json:"id"`
	Label  string            `json:"label"`
	Image  string            `json:"image"`
	Tags   map[string]string `json:"tags"`
	Status string            `json:"status"`
	SOH    *HostState        `json:"soh"`
}

type Edge struct {
	ID     int `json:"id"`
	Source int `json:"source"`
	Target int `json:"target"`
	Length int `json:"length"`
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
	Metadata  map[string]any `json:"metadata"  mapstructure:"metadata"  structs:"metadata"`
	Timestamp string         `json:"timestamp" mapstructure:"timestamp" structs:"timestamp"`
	Success   string         `json:"success"   mapstructure:"success"   structs:"success"`
	Error     string         `json:"error"     mapstructure:"error"     structs:"error"`
}

type HostState struct {
	Hostname     string  `json:"hostname"               mapstructure:"hostname"               structs:"hostname"`
	CPULoad      string  `json:"cpuLoad"                mapstructure:"cpuLoad"                structs:"cpuLoad"`
	Networking   []State `json:"networking,omitempty"   mapstructure:"networking,omitempty"   structs:"networking,omitempty"`
	Reachability []State `json:"reachability,omitempty" mapstructure:"reachability,omitempty" structs:"reachability,omitempty"`
	Processes    []State `json:"processes,omitempty"    mapstructure:"processes,omitempty"    structs:"processes,omitempty"`
	Listeners    []State `json:"listeners,omitempty"    mapstructure:"listeners,omitempty"    structs:"listeners,omitempty"`
	CustomTests  []State `json:"customTests,omitempty"  mapstructure:"customTests,omitempty"  structs:"customTests,omitempty"`

	// populated before sending to UI client
	Errors bool `json:"errors" mapstructure:"-" structs:"-"`
}

func (h HostState) AllStates() []State {
	all := make(
		[]State,
		0,
		len(h.Networking)+len(h.Reachability)+len(h.Processes)+len(h.Listeners)+len(
			h.CustomTests,
		),
	)

	all = append(all, h.Networking...)
	all = append(all, h.Reachability...)
	all = append(all, h.Processes...)
	all = append(all, h.Listeners...)
	all = append(all, h.CustomTests...)

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
	StartupDelay       string                      `mapstructure:"startupDelay"`

	// The `hostsToUseUUIDForC2Active` setting can be either a string or a slice
	// of strings. Decoding `hostsToUseUUIDForC2Active` into `UseUUIDForC2Active`
	// as a generic interface{} causes mapstructure.Decode to panic. We are using
	// the `mapstructure:",remain"` option below as a workaround.
	// UseUUIDForC2Active interface{} `mapstructure:"hostsToUseUUIDForC2Active"`

	Other map[string]any `mapstructure:",remain"`

	// set after parsing
	c2Timeout    time.Duration
	startupDelay time.Duration
	uuidHosts    map[string]struct{}
}

func (m *sohMetadata) init() error {
	if m.SkipNetworkConfig {
		// Default reachability test to off if skipping initial network config
		// tests.
		m.Reachability = reachabilityOff
	}

	if m.Reachability == "" {
		// Default to reachability test being disabled if not specified in the
		// scenario app config.
		m.Reachability = reachabilityOff
	}

	if m.Reachability == reachabilityOff {
		// Default to ICMP rule injection being disabled if reachability testing is
		// disabled.
		m.InjectICMPAllow = false
	}

	if m.C2Timeout == "" {
		// Default C2 timeout to 5m if not specified in the scenario app config.
		m.c2Timeout = defaultC2Timeout
	} else {
		var err error

		if m.c2Timeout, err = time.ParseDuration(m.C2Timeout); err != nil {
			return fmt.Errorf("parsing C2 timeout setting '%s': %w", m.C2Timeout, err)
		}
	}

	// Default startup delay is 0 if not set
	if m.StartupDelay != "" {
		var err error
		if m.startupDelay, err = time.ParseDuration(m.StartupDelay); err != nil {
			return fmt.Errorf("parsing startup delay setting `%s`: %w", m.StartupDelay, err)
		}
	}

	if m.AppProfileKey == "" {
		m.AppProfileKey = "sohProfile"
	}

	m.uuidHosts = make(map[string]struct{})

	if useUUID, ok := m.Other["hostsToUseUUIDForC2Active"]; ok {
		switch hosts := useUUID.(type) {
		case nil: // this is okay
		case string:
			m.uuidHosts[hosts] = struct{}{}
		case []any:
			if len(hosts) > 0 {
				for _, host := range hosts {
					h, ok2 := host.(string)
					if !ok2 {
						return errors.New(
							"parsing 'hostsToUseUUIDForC2Active': must be a string or string slice",
						)
					}

					m.uuidHosts[h] = struct{}{}
				}
			}
		default:
			return errors.New(
				"parsing 'hostsToUseUUIDForC2Active': must be a string or string slice",
			)
		}
	}

	return nil
}

func (m sohMetadata) useUUIDForC2Active(host string) bool {
	if _, ok := m.uuidHosts["all"]; ok {
		return true
	}

	if _, ok := m.uuidHosts[host]; ok {
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

func (p *sohProfile) init() error {
	if p.C2Timeout == "" {
		// Default C2 timeout to 5m if not specified in the SoH Profile config.
		p.c2Timeout = defaultC2Timeout
	} else {
		var err error

		if p.c2Timeout, err = time.ParseDuration(p.C2Timeout); err != nil {
			return fmt.Errorf("parsing C2 timeout setting '%s': %w", p.C2Timeout, err)
		}
	}

	return nil
}
