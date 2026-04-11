package soh

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/plog"
)

const appName = "soh"

const (
	loadAvgParts = 5
	portParts    = 2
	byteMask     = 0xFF
	shift8       = 8
	shift16      = 16
	shift24      = 24
)

var stringSpacePattern = regexp.MustCompile(`\s+`)

func (s *SOH) deployCapture(exp *types.Experiment, dryrun bool) error {
	err := s.decodeMetadata(exp)
	if err != nil {
		return err
	}

	if len(s.md.PacketCapture.CaptureHosts) == 0 {
		return nil
	}

	currentIP, mask, _ := net.ParseCIDR(s.md.PacketCapture.ElasticServer.IPAddress)
	cidr, _ := mask.Mask.Size()
	svrAddr := currentIP.String()

	var (
		caps     []ifaces.NodeSpec
		sched    = make(map[string]string)
		monitors = make(map[string][]string)
	)

	for nodeToMonitor := range s.md.PacketCapture.CaptureHosts {
		node := exp.Spec.Topology().FindNodeByName(nodeToMonitor)

		if node == nil {
			return fmt.Errorf("node %s to monitor via packet capture does not exist", nodeToMonitor)
		}

		if node.External() {
			return fmt.Errorf(
				"node %s to monitor via packet capture is not running in minimega",
				nodeToMonitor,
			)
		}

		currentIP = nextIP(currentIP)

		captureNode, mon, err := s.buildPacketBeatNode(exp, node, svrAddr, currentIP.String(), cidr)
		if err != nil {
			return fmt.Errorf("building PacketBeat node: %w", err)
		}

		caps = append(caps, captureNode)

		sched[captureNode.General().Hostname()] = exp.Status.Schedules()[nodeToMonitor]
		monitors[captureNode.General().Hostname()] = mon
	}

	spec := map[string]any{
		"experimentName": exp.Spec.ExperimentName(),
		"topology": map[string]any{
			"nodes": caps,
		},
		"schedules": sched,
	}

	expMonitor, _ := version.GetStoredSpecForKind("Experiment")

	err = mapstructure.Decode(spec, &expMonitor)
	if err != nil {
		return fmt.Errorf("decoding experiment spec for monitor nodes: %w", err)
	}

	expSpec, _ := expMonitor.(ifaces.ExperimentSpec)
	data := struct {
		Exp ifaces.ExperimentSpec
		Mon map[string][]string
	}{
		Exp: expSpec,
		Mon: monitors,
	}

	filename := fmt.Sprintf(
		"%s/mm_files/%s-monitor.mm",
		exp.Spec.BaseDir(),
		exp.Spec.ExperimentName(),
	)

	err = tmpl.CreateFileFromTemplate("packet_capture_script.tmpl", data, filename)
	if err != nil {
		return fmt.Errorf("generating packet capture script: %w", err)
	}

	if !dryrun {
		err = mm.ReadScriptFromFile(filename)
		if err != nil {
			return fmt.Errorf("reading packet capture script: %w", err)
		}
	}

	return nil
}

func (s *SOH) buildElasticServerNode( //nolint:ireturn // complex logic
	exp *types.Experiment,
	ip string,
	cidr int,
) (ifaces.NodeSpec, error) {
	var (
		name       = s.md.PacketCapture.ElasticServer.Hostname
		cpu        = s.md.PacketCapture.ElasticServer.VCPU
		mem        = s.md.PacketCapture.ElasticServer.Memory
		startupDir = exp.Spec.BaseDir() + "/startup"

		elasticConfigFile = fmt.Sprintf("%s/%s-elasticsearch.yml", startupDir, name)
		kibanaConfigFile  = fmt.Sprintf("%s/%s-kibana.yml", startupDir, name)
	)

	if cpu == 0 {
		cpu = 4
	}

	if mem == 0 {
		mem = 4096
	}

	node := exp.Spec.Topology().AddNode("VirtualMachine", name)
	node.AddLabel("soh-elastic-server", "true")
	node.AddHardware("linux", cpu, mem)
	node.Hardware().AddDrive(s.md.PacketCapture.ElasticImage, 1)
	node.AddInject(elasticConfigFile, "/etc/elasticsearch/elasticsearch.yml", "", "")
	node.AddInject(kibanaConfigFile, "/etc/kibana/kibana.yml", "", "")

	iface := node.AddNetworkInterface("ethernet", "IF0", "MGMT")
	iface.SetAddress(ip)
	iface.SetMask(cidr)
	iface.SetProto("static")
	iface.SetBridge(exp.Spec.DefaultBridge())

	data := struct {
		Hostname       string
		ExperimentName string
	}{
		Hostname:       name,
		ExperimentName: exp.Spec.ExperimentName(),
	}

	err := tmpl.CreateFileFromTemplate("elasticsearch.yml.tmpl", data, elasticConfigFile)
	if err != nil {
		return nil, fmt.Errorf("generating elasticsearch config: %w", err)
	}

	err = tmpl.CreateFileFromTemplate("kibana.yml.tmpl", name, kibanaConfigFile)
	if err != nil {
		return nil, fmt.Errorf("generating kibana config: %w", err)
	}

	return node, nil
}

func (s *SOH) buildPacketBeatNode( //nolint:funlen,ireturn // complex logic
	exp *types.Experiment,
	target ifaces.NodeSpec,
	es, ip string,
	cidr int,
) (ifaces.NodeSpec, []string, error) {
	var (
		monitored = target.General().Hostname()
		name      = monitored + "-soh-monitor"
		tz        = "Etc/UTC"

		startupDir   = exp.Spec.BaseDir() + "/startup"
		hostnameFile = startupDir + "/" + name + "-hostname.sh"
		timezoneFile = startupDir + "/" + name + "-timezone.sh"
		ifaceFile    = startupDir + "/" + name + "-interfaces.sh"

		packetBeatConfigFile = fmt.Sprintf("%s/%s-packetbeat.yml", startupDir, name)

		monitors []string
	)

	nets := []map[string]any{
		{
			"name":    "IF0",
			"type":    "ethernet",
			"vlan":    "MGMT",
			"address": ip,
			"mask":    cidr,
			"proto":   "static",
			"bridge":  exp.Spec.DefaultBridge(),
		},
	}

	for i, ifaceToMonitor := range s.md.PacketCapture.CaptureHosts[monitored] {
		for j, iface := range target.Network().Interfaces() {
			if iface.Name() == ifaceToMonitor {
				monitorIface := map[string]any{
					"name":   fmt.Sprintf("MONITOR%d", i),
					"type":   "ethernet",
					"vlan":   iface.VLAN(),
					"proto":  "static",
					"bridge": exp.Spec.DefaultBridge(),
				}

				nets = append(nets, monitorIface)

				monitors = append(monitors, fmt.Sprintf("%s %d", monitored, j))

				break
			}
		}
	}

	spec := map[string]any{
		"labels": map[string]string{"soh-monitor-node": "true"},
		"type":   "VirtualMachine",
		"general": map[string]any{
			"hostname": name,
			"vm_type":  "kvm",
		},
		"hardware": map[string]any{
			"vcpus":  1,
			"memory": monitorMemory,
			"drives": []map[string]any{
				{
					"image": s.md.PacketCapture.PacketBeatImage,
				},
			},
			"os_type": "linux",
		},
		"injections": []map[string]any{
			{
				"src": hostnameFile,
				"dst": "/etc/phenix/startup/1_hostname-start.sh",
			},
			{
				"src": timezoneFile,
				"dst": "/etc/phenix/startup/2_timezone-start.sh",
			},
			{
				"src": ifaceFile,
				"dst": "/etc/phenix/startup/3_interfaces-start.sh",
			},
			{
				"src": packetBeatConfigFile,
				"dst": "/etc/packetbeat/packetbeat.yml",
			},
		},
		"network": map[string]any{
			"interfaces": nets,
		},
	}

	node, _ := version.GetStoredSpecForKind("Node")

	err := mapstructure.Decode(spec, &node)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding node spec for Elastic server: %w", err)
	}

	err = tmpl.CreateFileFromTemplate("linux_hostname.tmpl", name, hostnameFile)
	if err != nil {
		return nil, nil, fmt.Errorf("generating linux hostname config: %w", err)
	}

	err = tmpl.CreateFileFromTemplate("linux_timezone.tmpl", tz, timezoneFile)
	if err != nil {
		return nil, nil, fmt.Errorf("generating linux timezone config: %w", err)
	}

	err = tmpl.CreateFileFromTemplate("linux_interfaces.tmpl", node, ifaceFile)
	if err != nil {
		return nil, nil, fmt.Errorf("generating linux interfaces config: %w", err)
	}

	data := struct {
		ElasticServer string
		Hostname      string
	}{
		ElasticServer: es,
		Hostname:      name,
	}

	err = tmpl.CreateFileFromTemplate("packetbeat.yml.tmpl", data, packetBeatConfigFile)
	if err != nil {
		return nil, nil, fmt.Errorf("generating packetbeat config: %w", err)
	}

	nodeSpec, _ := node.(ifaces.NodeSpec)
	return nodeSpec, monitors, nil
}

func (s *SOH) decodeMetadata(exp *types.Experiment) error {
	var ms map[string]any

	for _, app := range exp.Spec.Scenario().Apps() {
		if app.Name() == appName {
			ms = app.Metadata()
		}
	}

	if ms == nil {
		return errors.New("soh app must have metadata defined")
	}

	err := mapstructure.Decode(ms, &s.md)
	if err != nil {
		return fmt.Errorf("decoding app metadata: %w", err)
	}

	err = s.md.init()
	if err != nil {
		return fmt.Errorf("initializing app metadata: %w", err)
	}

	return nil
}

//nolint:cyclop,funlen,gocyclo,maintidx // complex logic
func (s *SOH) waitForReachabilityTest(ctx context.Context, ns string, checks map[string]bool) bool {
	if s.md.SkipNetworkConfig || !checks["network-config"] {
		return false
	}

	logger := plog.LoggerFromContext(ctx, plog.TypeSoh)

	var (
		icmpDisabled   = strings.EqualFold(s.md.Reachability, "off") || !checks["reachability"]
		customDisabled = len(s.md.CustomReachability) == 0 || !checks["custom-reachability"]
	)

	if icmpDisabled {
		logger.Warn("ICMP reachability test is disabled")
	}

	if customDisabled {
		logger.Warn("no custom reachability tests configured")
	}

	if icmpDisabled && customDisabled {
		return false
	}

	logger.Info(fmt.Sprintf("reachability test mode setset to %s mode", s.md.Reachability))

	wg := new(mm.StateGroup)

	if !icmpDisabled {
		for host := range s.reachabilityHosts {
			// Assume we're not skipping this host by default.
			var skipHost error

			if _, ok := s.c2Hosts[host]; !ok {
				// This host is known to not have C2 active, so don't test from it.
				skipHost = errors.New("c2 not active on host")
			}

			if _, ok := s.failedNetwork[host]; ok {
				// This host failed the network config test, so don't test from it.
				skipHost = errors.New("networking not configured on host")
			}

			for _, ips := range s.vlans {
				// Each host should try to ping a single random host in each VLAN.
				if strings.EqualFold(s.md.Reachability, "sample") {
					var targeted bool

					// Range over IPs to prevent this for-loop from going on forever if
					// all IPs in VLAN failed network connectivity test.
					for range ips {
						idx := rand.IntN(len(ips)) //nolint:gosec // weak random number generator
						targetIP := ips[idx]

						targetHost := s.addrHosts[targetIP]

						if _, ok := s.failedNetwork[targetHost]; ok {
							continue
						}

						targeted = true

						if skipHost != nil {
							wg.AddError(skipHost, map[string]any{"host": host, "target": targetIP})
						} else {
							logger.Debug(
								"running ping test",
								"from",
								host,
								"to",
								targetHost,
								"ip",
								targetIP,
							)
							s.pingTest(ctx, wg, ns, s.nodes[host], targetIP)
						}

						break
					}

					if !targeted {
						// Choose random host in VLAN to create error for.
						idx := rand.IntN(len(ips)) //nolint:gosec // weak random number generator
						targetIP := ips[idx]

						// This target host failed the network config test, so don't try
						// to do any reachability to it.
						var (
							err  = errors.New("networking not configured on target")
							meta = map[string]any{"host": host, "target": targetIP}
						)

						wg.AddError(err, meta)
					}
				}

				// Each host should try to ping every host in each VLAN.
				if strings.EqualFold(s.md.Reachability, "full") {
					for _, targetIP := range ips {
						targetHost := s.addrHosts[targetIP]

						if _, ok := s.failedNetwork[targetHost]; ok {
							// This target host failed the network config test, so don't try
							// to do any reachability to it.
							var (
								err  = errors.New("networking not configured on target")
								meta = map[string]any{"host": host, "target": targetIP}
							)

							wg.AddError(err, meta)

							continue
						}

						if skipHost != nil {
							wg.AddError(skipHost, map[string]any{"host": host, "target": targetIP})
						} else {
							logger.Debug(
								"running ping test",
								"from",
								host,
								"to",
								targetHost,
								"ip",
								targetIP,
							)
							s.pingTest(ctx, wg, ns, s.nodes[host], targetIP)
						}
					}
				}
			}
		}
	}

	if !customDisabled {
		for _, reach := range s.md.CustomReachability {
			host := reach.Src

			if _, ok := s.c2Hosts[host]; !ok {
				// This host is known to not have C2 active, so don't test from it.
				wg.AddError(
					errors.New("c2 not active on host"),
					map[string]any{"host": host, "target": reach.Dst},
				)

				continue
			}

			if _, ok := s.failedNetwork[host]; ok {
				// This host failed the network config test, so don't test from it.
				wg.AddError(
					errors.New("networking not configured on host"),
					map[string]any{"host": host, "target": reach.Dst},
				)

				continue
			}

			target := reach.Dst

			if fields := strings.Split(reach.Dst, "|"); len(fields) > 1 {
				target = s.hostIPs[fields[0]][fields[1]]
			}

			logger.Debug(
				"running custom reachability test",
				"from",
				host,
				"to",
				fmt.Sprintf("%s://%s:%d", reach.Proto, target, reach.Port),
			)

			wait, err := time.ParseDuration(reach.Wait)
			if err != nil && reach.Wait != "" {
				logger.Warn("invalid wait time provided, using default", "provided", reach.Wait)
			}

			s.connTest(ctx, wg, ns, host, target, reach.Proto, reach.Port, wait, reach.Packet)
		}
	}

	cancel := periodicallyNotify(
		ctx,
		"waiting for reachability tests to complete...",
		notifyInterval,
	)

	// Wait for hosts to test reachability to other hosts.
	wg.Wait()
	cancel()

	for _, state := range wg.States {
		var (
			host, _   = state.Meta["host"].(string)
			target, _ = state.Meta["target"].(string)
		)

		// Convert target IP to hostname.
		hostname := s.addrHosts[target]

		if hostname != "" { // might be empty if target IP not in topology
			state.Meta["target"] = hostname
		}

		state.Meta["ip"] = target

		st := State{ //nolint:exhaustruct // partial initialization
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		err := state.Err
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(s.c2Hosts, host)
			}

			st.Error = err.Error()

			if _, ok := state.Meta["port"]; ok {
				var (
					port, _  = state.Meta["port"].(int)
					proto, _ = state.Meta["proto"].(string)
				)

				logger.Error(
					"[✗] failed to connect",
					"from",
					host,
					"to",
					fmt.Sprintf("%s://%s:%d", proto, target, port),
				)
			} else {
				logger.Error("[✗] failed to ping", "from", host, "to", hostname, "ip", target)
			}
		} else {
			st.Success = state.Msg
		}

		hostState, ok := s.status[host]
		if !ok {
			hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
		}

		hostState.Reachability = append(hostState.Reachability, st)
		s.status[host] = hostState

		if hostname != "" { // might be empty if target IP not in topology
			hostState, ok = s.status[hostname]
			if !ok {
				hostState = HostState{Hostname: hostname} //nolint:exhaustruct // partial initialization
			}

			hostState.Reachability = append(hostState.Reachability, st)
			s.status[hostname] = hostState
		}
	}

	return wg.ErrCount > 0
}

//nolint:dupl // similar to waitForPortTest
func (s *SOH) waitForProcTest(ctx context.Context, ns string) bool {
	var (
		logger = plog.LoggerFromContext(ctx, plog.TypeSoh)
		wg     = new(mm.StateGroup)
	)

	for host, processes := range s.md.HostProcesses {
		// If the host isn't in the C2 hosts map, then don't operate on it since it
		// was likely skipped for a reason.
		if _, ok := s.c2Hosts[host]; !ok {
			logger.Debug("skipping host per config", "host", host)

			continue
		}

		for _, proc := range processes {
			logger.Debug("checking for process on host", "host", host, "process", proc)
			s.procTest(ctx, wg, ns, s.nodes[host], proc)
		}
	}

	// Check to see if any of the apps have hosts with metadata that include an SoH profile.
	for _, app := range s.apps {
		for _, host := range app.Hosts() {
			if ms, ok := host.Metadata()[s.md.AppProfileKey]; ok {
				if _, ok := s.c2Hosts[host.Hostname()]; !ok {
					logger.Debug("skipping host per config", "host", host.Hostname())

					continue
				}

				var profile sohProfile

				err := mapstructure.Decode(ms, &profile)
				if err != nil {
					logger.Warn(
						"incorrect SoH profile for host in app",
						"host",
						host.Hostname(),
						"app",
						app.Name(),
					)

					continue
				}

				for _, proc := range profile.Processes {
					logger.Debug(
						"checking for process on host",
						"host",
						host.Hostname(),
						"process",
						proc,
					)
					s.procTest(ctx, wg, ns, s.nodes[host.Hostname()], proc)
				}
			}
		}
	}

	cancel := periodicallyNotify(ctx, "waiting for process tests to complete...", notifyInterval)

	wg.Wait()
	cancel()

	for _, state := range wg.States {
		var (
			host, _ = state.Meta["host"].(string)
			proc, _ = state.Meta["proc"].(string)
		)

		st := State{ //nolint:exhaustruct // partial initialization
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		err := state.Err
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(s.c2Hosts, host)
			}

			st.Error = err.Error()

			logger.Error("[✗] process not running on host", "host", host, "process", proc)
		} else {
			st.Success = state.Msg
		}

		hostState, ok := s.status[host]
		if !ok {
			hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
		}

		hostState.Processes = append(hostState.Processes, st)
		s.status[host] = hostState
	}

	return wg.ErrCount > 0
}

//nolint:dupl // similar to waitForProcTest
func (s *SOH) waitForPortTest(ctx context.Context, ns string) bool {
	var (
		logger = plog.LoggerFromContext(ctx, plog.TypeSoh)
		wg     = new(mm.StateGroup)
	)

	for host, listeners := range s.md.HostListeners {
		// If the host isn't in the C2 hosts map, then don't operate on it since it
		// was likely skipped for a reason.
		if _, ok := s.c2Hosts[host]; !ok {
			logger.Debug("skipping host per config", "host", host)

			continue
		}

		for _, port := range listeners {
			logger.Debug("checking for listener on host", "host", host, "listener", port)
			s.portTest(ctx, wg, ns, s.nodes[host], port)
		}
	}

	// Check to see if any of the apps have hosts with metadata that include an SoH profile.
	for _, app := range s.apps {
		for _, host := range app.Hosts() {
			if ms, ok := host.Metadata()[s.md.AppProfileKey]; ok {
				if _, ok := s.c2Hosts[host.Hostname()]; !ok {
					logger.Debug("skipping host per config", "host", host.Hostname())

					continue
				}

				var profile sohProfile

				err := mapstructure.Decode(ms, &profile)
				if err != nil {
					logger.Warn(
						"incorrect SoH profile for host in app",
						"host",
						host.Hostname(),
						"app",
						app.Name(),
					)

					continue
				}

				for _, port := range profile.Listeners {
					logger.Debug(
						"checking for listener on host",
						"host",
						host.Hostname(),
						"listener",
						port,
					)
					s.portTest(ctx, wg, ns, s.nodes[host.Hostname()], port)
				}
			}
		}
	}

	cancel := periodicallyNotify(ctx, "waiting for listener tests to complete...", notifyInterval)

	wg.Wait()
	cancel()

	for _, state := range wg.States {
		var (
			host, _ = state.Meta["host"].(string)
			port, _ = state.Meta["port"].(string)
		)

		st := State{ //nolint:exhaustruct // partial initialization
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		err := state.Err
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(s.c2Hosts, host)
			}

			st.Error = err.Error()

			logger.Error("[✗] host not listening on port", "host", host, "port", port)
		} else {
			st.Success = state.Msg
		}

		hostState, ok := s.status[host]
		if !ok {
			hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
		}

		hostState.Listeners = append(hostState.Listeners, st)
		s.status[host] = hostState
	}

	return wg.ErrCount > 0
}

func (s *SOH) waitForCustomTest(ctx context.Context, ns string) bool {
	var (
		logger = plog.LoggerFromContext(ctx, plog.TypeSoh)
		wg     = new(mm.StateGroup)
	)

	for host, tests := range s.md.CustomHostTests {
		// If the host isn't in the C2 hosts map, then don't operate on it since it
		// was likely skipped for a reason.
		if _, ok := s.c2Hosts[host]; !ok {
			logger.Debug("skipping host per config", "host", host)

			continue
		}

		for _, test := range tests {
			logger.Debug("running custom test on host", "host", host, "test", test.Name)
			s.customTest(ctx, wg, ns, s.nodes[host], test)
		}
	}

	// Check to see if any of the apps have hosts with metadata that include an SoH profile.
	for _, app := range s.apps {
		for _, host := range app.Hosts() {
			if ms, ok := host.Metadata()[s.md.AppProfileKey]; ok {
				if _, ok := s.c2Hosts[host.Hostname()]; !ok {
					logger.Debug("skipping host per config", "host", host.Hostname())

					continue
				}

				var profile sohProfile

				err := mapstructure.Decode(ms, &profile)
				if err != nil {
					logger.Warn(
						"incorrect SoH profile for host in app",
						"host",
						host.Hostname(),
						"app",
						app.Name(),
					)

					continue
				}

				for _, test := range profile.CustomTests {
					logger.Debug(
						"running custom test on host",
						"host",
						host.Hostname(),
						"test",
						test.Name,
					)
					s.customTest(ctx, wg, ns, s.nodes[host.Hostname()], test)
				}
			}
		}
	}

	cancel := periodicallyNotify(ctx, "waiting for custom tests to complete...", notifyInterval)

	wg.Wait()
	cancel()

	for _, state := range wg.States {
		var (
			host, _ = state.Meta["host"].(string)
			test, _ = state.Meta["test"].(string)
		)

		st := State{ //nolint:exhaustruct // partial initialization
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		err := state.Err
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(s.c2Hosts, host)
			}

			st.Error = err.Error()

			logger.Error("[✗] test failed on host", "host", host, "test", test)
		} else {
			st.Success = state.Msg
		}

		hostState, ok := s.status[host]
		if !ok {
			hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
		}

		hostState.CustomTests = append(hostState.CustomTests, st)
		s.status[host] = hostState
	}

	return wg.ErrCount > 0
}

func (s *SOH) waitForCPULoad(ctx context.Context, ns string) bool { //nolint:funlen // complex logic
	var (
		logger = plog.LoggerFromContext(ctx, plog.TypeSoh)
		wg     = new(mm.StateGroup)
	)

	logger.Info("querying nodes for CPU load")

	// Only check for CPU load in hosts that have confirmed C2 availability.
	for host := range s.c2Hosts {
		wg.Add(1)

		go func(host string) {
			defer wg.Done()

			node := s.nodes[host]
			exec := `cat /proc/loadavg`

			if strings.EqualFold(node.Hardware().OSType(), "windows") {
				exec = `powershell -command "Get-WmiObject Win32_Processor | Measure-Object -Property LoadPercentage -Average | Select -ExpandProperty Average"`
			}

			opts := []mm.C2Option{
				mm.C2NS(ns),
				mm.C2VM(host),
				mm.C2Command(exec),
				mm.C2Timeout(s.md.c2Timeout),
			}

			if s.md.useUUIDForC2Active(host) {
				opts = append(opts, mm.C2IDClientsByUUID())
			}

			id, err := mm.ExecC2Command(opts...)
			if err != nil {
				wg.AddError(
					fmt.Errorf("executing command '%s': %w", exec, err),
					map[string]any{"host": host},
				)

				return
			}

			opts = []mm.C2Option{
				mm.C2NS(ns),
				mm.C2Context(ctx),
				mm.C2CommandID(id),
				mm.C2Timeout(s.md.c2Timeout),
			}

			resp, err := mm.WaitForC2Response(opts...)
			if err != nil {
				wg.AddError(
					fmt.Errorf("getting response for command '%s': %w", exec, err),
					map[string]any{"host": host},
				)

				return
			}

			hostState, ok := s.status[host]
			if !ok {
				hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
			}

			if strings.EqualFold(node.Hardware().OSType(), "windows") {
				if resp == "" {
					wg.AddError(
						fmt.Errorf("no response for command '%s'", exec),
						map[string]any{"host": host},
					)

					return
				}

				hostState.CPULoad = resp
			} else {
				parts := strings.Fields(resp)

				if len(parts) != loadAvgParts {
					wg.AddError(
						fmt.Errorf("invalid response for command '%s': %s", exec, resp),
						map[string]any{"host": host},
					)

					return
				}

				hostState.CPULoad = parts[0]
			}

			s.status[host] = hostState
		}(host)
	}

	cancel := periodicallyNotify(ctx, "waiting for CPU load details...", notifyInterval)

	// Wait for CPU load requests to complete.
	wg.Wait()
	cancel()

	for _, state := range wg.States {
		host, _ := state.Meta["host"].(string)

		err := state.Err
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(s.c2Hosts, host)
			}

			hostState, ok := s.status[host]
			if !ok {
				hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
			}

			hostState.CPULoad = err.Error()
			s.status[host] = hostState

			logger.Error("[✗] failed to get CPU load from host", "host", host, "err", err)
		}
	}

	return wg.ErrCount > 0
}

func (s SOH) isNetworkingConfigured( //nolint:funlen // complex logic
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	iface ifaces.NodeNetworkInterface,
) {
	retryUntil := time.Now().Add(s.md.c2Timeout)

	var (
		addr    = iface.Address()
		host    = node.General().Hostname()
		gateway = iface.Gateway()
		meta    = map[string]any{"host": host}
	)

	// First, we wait for the IP address to be set on the interface. Then, we wait
	// for the default gateway to be set. Last, we wait for the default gateway to
	// be up (pingable). This is all done via nested commands streamed to the C2
	// processor within `expected` functions.
	ipExpected := func(resp string) error {
		if addr != "" {
			if strings.EqualFold(node.Hardware().OSType(), "windows") {
				// If `resp` doesn't contain the IP address, then the IP address isn't
				// configured yet, so keep retrying the C2 command.
				if !strings.Contains(resp, addr) {
					if time.Now().After(retryUntil) {
						return errors.New("retry time expired waiting for IP to be set")
					}

					return mm.C2RetryError{Delay: c2RetryDelay}
				}
			} else {
				cidr := fmt.Sprintf("%s/%d", addr, iface.Mask())

				// If `resp` doesn't contain the IP address, then the IP address isn't
				// configured yet, so keep retrying the C2 command.
				if !strings.Contains(resp, cidr) {
					if time.Now().After(retryUntil) {
						return errors.New("retry time expired waiting for IP to be set")
					}

					return mm.C2RetryError{Delay: c2RetryDelay}
				}
			}

			wg.AddSuccess(fmt.Sprintf("IP %s configured", addr), meta)
		}

		if gateway != "" {
			// The IP address is now set, so schedule a C2 command for determining if
			// the default gateway is set.
			gwExpected := func(resp string) error {
				if strings.EqualFold(node.Hardware().OSType(), "windows") {
					expected := "0.0.0.0\\s+0.0.0.0\\s+" + gateway

					// If `resp` doesn't contain the default gateway, then the default gateway
					// isn't configured yet, so keep retrying the C2 command.
					if found, _ := regexp.MatchString(expected, resp); !found {
						if time.Now().After(retryUntil) {
							return errors.New("retry time expired waiting for gateway to be set")
						}

						return mm.C2RetryError{Delay: c2RetryDelay}
					}
				} else {
					expected := "default via " + gateway

					// If `resp` doesn't contain the default gateway, then the default gateway
					// isn't configured yet, so keep retrying the C2 command.
					if !strings.Contains(resp, expected) {
						if time.Now().After(retryUntil) {
							return errors.New("retry time expired waiting for gateway to be set")
						}

						return mm.C2RetryError{Delay: c2RetryDelay}
					}
				}

				wg.AddSuccess(fmt.Sprintf("gateway %s configured", gateway), meta)

				// The default gateway is now set, so schedule a C2 command for
				// determining if the default gateway is up (pingable).
				gwPingExpected := func(resp string) error {
					if strings.EqualFold(node.Hardware().OSType(), "windows") {
						// If `resp` contains `Destination host unreachable`, the
						// default gateway isn't up (pingable) yet, so keep retrying the C2
						// command.
						if strings.Contains(resp, "Destination host unreachable") {
							if time.Now().After(retryUntil) {
								return errors.New("retry time expired waiting for gateway to be up")
							}

							return mm.C2RetryError{Delay: c2RetryDelay}
						}
					} else {
						// If `resp` contains `0 received`, the default gateway isn't up
						// (pingable) yet, so keep retrying the C2 command.
						if strings.Contains(resp, "0 received") {
							if time.Now().After(retryUntil) {
								return errors.New("retry time expired waiting for gateway to be up")
							}

							return mm.C2RetryError{Delay: c2RetryDelay}
						}
					}

					wg.AddSuccess(fmt.Sprintf("gateway %s is up", gateway), meta)

					return nil
				}

				exec := "ping -c 1 " + gateway

				if strings.EqualFold(node.Hardware().OSType(), "windows") {
					exec = "ping -n 1 " + gateway
				}

				cmd := s.newParallelCommand(ns, host, exec)
				cmd.Wait = wg
				cmd.Meta = map[string]any{"host": host}
				cmd.Expected = gwPingExpected

				mm.ScheduleC2ParallelCommand(ctx, cmd)

				return nil
			}

			exec := "ip route"

			if strings.EqualFold(node.Hardware().OSType(), "windows") {
				exec = "route print"
			}

			cmd := s.newParallelCommand(ns, host, exec)
			cmd.Wait = wg
			cmd.Meta = map[string]any{"host": host}
			cmd.Expected = gwExpected

			mm.ScheduleC2ParallelCommand(ctx, cmd)
		}

		return nil
	}

	exec := "ip addr"

	if strings.EqualFold(node.Hardware().OSType(), "windows") {
		exec = "ipconfig /all"
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = ipExpected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func (s SOH) pingTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	target string,
) {
	exec := "ping -c 1 " + target

	if strings.EqualFold(node.Hardware().OSType(), "windows") {
		exec = "ping -n 1 " + target
	}

	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "target": target}
	)

	expected := func(resp string) error {
		if strings.EqualFold(node.Hardware().OSType(), "windows") {
			// If `resp` contains `Destination host unreachable`, the
			// default gateway isn't up (pingable) yet, so keep retrying the C2
			// command.
			if strings.Contains(resp, "Destination host unreachable") {
				return errors.New("no successful pings")
			}
		} else {
			// If `resp` contains `0 received`, the default gateway isn't up
			// (pingable) yet, so keep retrying the C2 command.
			if strings.Contains(resp, "0 received") {
				return errors.New("no successful pings")
			}
		}

		wg.AddSuccess(fmt.Sprintf("pinging %s succeeded", target), meta)

		return nil
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func (s SOH) connTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns, src, dst, proto string,
	port int,
	wait time.Duration,
	packet string,
) {
	test := fmt.Sprintf("%s %s %d wait", proto, dst, port)

	if wait == 0 {
		test = fmt.Sprintf("%s %v", test, c2RetryDelay)
	} else {
		test = fmt.Sprintf("%s %v", test, wait)
	}

	if proto == "udp" && packet != "" {
		test = fmt.Sprintf("%s %s", test, packet)
	}

	meta := map[string]any{"host": src, "target": dst, "port": port, "proto": proto}
	opts := []mm.C2Option{
		mm.C2NS(ns),
		mm.C2VM(src),
		mm.C2TestConn(test),
		mm.C2Timeout(s.md.c2Timeout),
	}

	if s.md.useUUIDForC2Active(src) {
		opts = append(opts, mm.C2IDClientsByUUID())
	}

	cmd := &mm.C2ParallelCommand{ //nolint:exhaustruct // partial initialization
		Wait:    wg,
		Options: opts,
		Meta:    meta,
		Expected: func(resp string) error {
			if strings.Contains(resp, "fail") {
				return fmt.Errorf("failed to connect to %s://%s:%d", proto, dst, port)
			}

			wg.AddSuccess(fmt.Sprintf("connection to %s://%s:%d succeeded", proto, dst, port), meta)

			return nil
		},
	}

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func (s SOH) procTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	proc string,
) {
	exec := "pgrep -f " + proc

	if strings.EqualFold(node.Hardware().OSType(), "windows") {
		exec = fmt.Sprintf(
			`powershell -command "Get-Process %s -ErrorAction SilentlyContinue"`,
			proc,
		)
	}

	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "proc": proc}
	)

	retries := 5
	expected := func(resp string) error {
		if resp == "" {
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: c2RetryDelay}
			}

			return errors.New("process not running")
		}

		wg.AddSuccess("process running", meta)

		return nil
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func (s SOH) portTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	port string,
) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "port": port}
	)

	exec := "ss -lntu state all"

	if strings.EqualFold(node.Hardware().OSType(), "windows") {
		exec = fmt.Sprintf(
			`powershell -command "netstat -an | select-string -pattern 'listening' | select-string -pattern '%s'"`,
			port,
		)
	} else {
		target := strings.Split(port, ":")

		switch len(target) {
		case 1:
			exec = fmt.Sprintf("%s 'sport = %s'", exec, target[0])
		case portParts:
			switch {
			case target[0] == "": // :<port>
				exec = fmt.Sprintf("%s 'sport = %s'", exec, target[1])
			case target[1] == "": // <ip>: (why?!)
				exec = fmt.Sprintf("%s 'src = %s'", exec, target[0])
			default: // <ip>:<port>
				exec = fmt.Sprintf("%s 'src = %s and sport = %s'", exec, target[0], target[1])
			}
		default:
			wg.AddError(fmt.Errorf("invalid port %s provided", port), meta)

			return
		}
	}

	retries := 5
	expected := func(resp string) error {
		lineCount := 1

		if strings.EqualFold(node.Hardware().OSType(), "windows") {
			lineCount = 0
		}

		lines := trim(resp)

		if len(lines) <= lineCount {
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: c2RetryDelay}
			}

			return errors.New("not listening on port")
		}

		wg.AddSuccess("listening on port", meta)

		return nil
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func (s SOH) newParallelCommand(ns, host, exec string) *mm.C2ParallelCommand {
	opts := []mm.C2Option{
		mm.C2NS(ns),
		mm.C2VM(host),
		mm.C2Command(exec),
		mm.C2Timeout(s.md.c2Timeout),
	}

	if s.md.useUUIDForC2Active(host) {
		opts = append(opts, mm.C2IDClientsByUUID())
	}

	return &mm.C2ParallelCommand{Options: opts} //nolint:exhaustruct // partial initialization
}

func injectICMPAllowRules(nodes []ifaces.NodeSpec) error {
	for _, node := range nodes {
		// This only adds ICMP allow rules if one or more rulesets already exist. If
		// no rulesets exist then ICMP should already be allowed.
		// TODO: right now, we simply add a rule to allow ICMP to/from anywhere
		// without checking the default rule or seeing if an ICMP rule already
		// exists. May want to improve on this if it causes issues.
		for _, ruleset := range node.Network().Rulesets() {
			var present bool

			for _, rule := range ruleset.Rules() {
				if strings.HasPrefix(rule.Description(), "[SOH ICMP ALL]") {
					present = true

					break
				}
			}

			if present {
				continue
			}

			rule := ruleset.UnshiftRule()

			if rule == nil {
				return fmt.Errorf(
					"unable to prepend rule to %s - no ID available",
					node.General().Hostname(),
				)
			}

			rule.SetDescription("[SOH ICMP ALL] Allow all ICMP for SoH reachability")
			rule.SetAction("accept")
			rule.SetProtocol("icmp")
			rule.SetSource("0.0.0.0/0", 0)
			rule.SetDestination("0.0.0.0/0", 0)
		}
	}

	return nil
}

func removeICMPAllowRules(nodes []ifaces.NodeSpec) {
	for _, node := range nodes {
		for _, ruleset := range node.Network().Rulesets() {
			var rule ifaces.NodeNetworkRulesetRule

			for _, r := range ruleset.Rules() {
				if strings.HasPrefix(r.Description(), "[SOH ICMP ALL]") {
					rule = r

					break
				}
			}

			if rule != nil {
				ruleset.RemoveRule(rule.ID())
			}
		}
	}
}

func (s SOH) customTest( //nolint:funlen // complex logic
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	test customHostTest,
) {
	host := node.General().Hostname()
	meta := map[string]any{"host": host, "test": test.Name}

	if test.TestScript == "" {
		wg.AddError(errors.New("no test script provided"), meta)

		return
	}

	if test.TestStdout == "" && test.TestStderr == "" && test.ValidateStdout == "" &&
		test.ValidateStderr == "" {
		wg.AddError(errors.New("no output test strings or validators provided"), meta)

		return
	}

	script := fmt.Sprintf("%s-%s", host, stringSpacePattern.ReplaceAllString(test.Name, "_"))

	executor := test.Executor
	if executor == "" {
		switch strings.ToLower(node.Hardware().OSType()) {
		case "windows":
			executor = "powershell -NoProfile -ExecutionPolicy bypass -File"
		default:
			executor = "bash"
		}
	}

	if strings.HasPrefix(executor, "powershell") {
		script += ".ps1"
	}

	path := fmt.Sprintf("%s/images/%s/%s", common.PhenixBase, ns, script)

	err := os.WriteFile(path, []byte(test.TestScript), 0o600)
	if err != nil {
		wg.AddError(fmt.Errorf("unable to write test script to file: %w", err), meta)

		return
	}

	command := fmt.Sprintf("%s /tmp/miniccc/files/%s", executor, script)
	opts := []mm.C2Option{
		mm.C2NS(ns),
		mm.C2VM(host),
		mm.C2SendFile(script),
		mm.C2Command(command),
		mm.C2Timeout(s.md.c2Timeout),
	}

	if s.md.useUUIDForC2Active(host) {
		opts = append(opts, mm.C2IDClientsByUUID())
	}

	cmd := &mm.C2ParallelCommand{ //nolint:exhaustruct // partial initialization
		Wait:    wg,
		Options: opts,
		Meta:    meta,
	}

	if test.TestStdout != "" {
		cmd.ExpectedStdout = func(resp string) error {
			if strings.Contains(resp, test.TestStdout) {
				wg.AddSuccess("STDOUT contained "+test.TestStdout, meta)

				return nil
			}

			return errors.New("script STDOUT did not contain test output")
		}
	}

	if test.TestStderr != "" {
		cmd.ExpectedStderr = func(resp string) error {
			if strings.Contains(resp, test.TestStderr) {
				wg.AddSuccess("STDERR contained "+test.TestStderr, meta)

				return nil
			}

			return errors.New("script STDERR did not contain test output")
		}
	}

	if test.ValidateStdout != "" {
		cmd.ExpectedStdout = func(resp string) error {
			if err := runValidator(ctx, test.ValidateStdout, resp, "STDOUT"); err != nil {
				return err
			}
			wg.AddSuccess("STDOUT validated", meta)
			return nil
		}
	}

	if test.ValidateStderr != "" {
		cmd.ExpectedStderr = func(resp string) error {
			if err := runValidator(ctx, test.ValidateStderr, resp, "STDERR"); err != nil {
				return err
			}
			wg.AddSuccess("STDERR validated", meta)
			return nil
		}
	}

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func runValidator(ctx context.Context, script, input, label string) error {
	f, err := os.CreateTemp("", "soh-validator-")
	if err != nil {
		return fmt.Errorf("unable to create %s validator script", label)
	}
	defer func() { _ = os.Remove(f.Name()) }() //nolint:gosec // Path traversal via taint analysis

	if _, err := f.WriteString(script); err != nil {
		_ = f.Close()
		return fmt.Errorf("unable to create %s validator script", label)
	}
	_ = f.Close()

	bash, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash command is not available for %s validation", label)
	}

	cmd := exec.CommandContext(ctx, bash, f.Name()) //nolint:gosec // Command injection via taint analysis
	cmd.Stdin = strings.NewReader(input)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script %s was not valid", label)
	}
	return nil
}

func skip(node ifaces.NodeSpec, toSkip []string) bool {
	for _, skipHost := range toSkip {
		// Check to see if this is a reference to an image. If so, skip this host if
		// it's using the referenced image.
		if ext := filepath.Ext(skipHost); ext == ".qc2" || ext == ".qcow2" {
			if filepath.Base(node.Hardware().Drives()[0].Image()) == skipHost {
				return true
			}
		}

		// Check to see if this node's hostname matches one to be skipped.
		if node.General().Hostname() == skipHost {
			return true
		}
	}

	return false
}

func trim(str string) []string {
	var trimmed []string

	for l := range strings.SplitSeq(str, "\n") {
		if l == "" {
			continue
		}

		trimmed = append(trimmed, strings.TrimSpace(l))
	}

	return trimmed
}

func periodicallyNotify(
	ctx context.Context,
	msg string,
	d time.Duration, //nolint:unparam // utility function
) context.CancelFunc {
	var (
		logger       = plog.LoggerFromContext(ctx, plog.TypeSoh)
		cctx, cancel = context.WithCancel(ctx)
		ticker       = time.NewTicker(d)
	)

	go func() {
		for {
			select {
			case <-cctx.Done():
				ticker.Stop()

				return
			case <-ticker.C:
				logger.Debug(msg)
			}
		}
	}()

	return cancel
}

func nextIP(ip net.IP) net.IP {
	i := ip.To4()
	v := uint(i[0])<<shift24 + uint(i[1])<<shift16 + uint(i[2])<<shift8 + uint(i[3])

	v++

	v3 := byte(v & byteMask)
	v2 := byte((v >> shift8) & byteMask)
	v1 := byte((v >> shift16) & byteMask)
	v0 := byte((v >> shift24) & byteMask)

	return net.IPv4(v0, v1, v2, v3)
}
