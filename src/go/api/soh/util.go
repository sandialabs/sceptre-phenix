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
		err = mm.ReadScriptFromFile(exp.Spec.ExperimentName(), filename)
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
		s.icmpReachabilityTests(ctx, wg, ns)
	}

	if !customDisabled {
		s.customReachabilityTests(ctx, wg, ns)
	}

	if waitAll(ctx, wg, "waiting for reachability tests to complete...") {
		return true
	}

	for _, state := range wg.States {
		var (
			host, _   = state.Meta["host"].(string)
			target, _ = state.Meta["target"].(string)
			hostname  = s.addrHosts[target] // empty when the target is not in the topology
		)

		if hostname != "" {
			state.Meta["target"] = hostname
		}

		state.Meta["ip"] = target

		st := s.newState(state)
		if st.Error != "" {
			if port, ok := state.Meta["port"].(int); ok {
				proto, _ := state.Meta["proto"].(string)

				logger.Error("[✗] failed to connect", "from", host, "to", fmt.Sprintf("%s://%s:%d", proto, target, port))
			} else {
				logger.Error("[✗] failed to ping", "from", host, "to", hostname, "ip", target)
			}
		}

		// The result belongs to both ends of the test.
		record := func(h *HostState) { h.Reachability = append(h.Reachability, st) }

		s.updateHost(host, record)

		if hostname != "" {
			s.updateHost(hostname, record)
		}
	}

	return wg.ErrCount > 0
}

// icmpReachabilityTests pings across VLANs from every host under test: one
// random target per VLAN in sample mode, every target in full mode.
func (s *SOH) icmpReachabilityTests(ctx context.Context, wg *mm.StateGroup, ns string) {
	logger := plog.LoggerFromContext(ctx, plog.TypeSoh)

	// ping runs a ping from host to targetIP, or records why it cannot run.
	ping := func(host, targetIP string) {
		var (
			meta       = map[string]any{"host": host, "target": targetIP}
			targetHost = s.addrHosts[targetIP]
		)

		if _, failed := s.failedNetwork[targetHost]; failed {
			wg.AddError(errors.New("networking not configured on target"), meta)

			return
		}

		if err := s.cannotReach(host); err != nil {
			wg.AddError(err, meta)

			return
		}

		logger.Debug("running ping test", "from", host, "to", targetHost, "ip", targetIP)
		s.pingTest(ctx, wg, ns, s.nodes[host], targetIP)
	}

	for host := range s.reachabilityHosts {
		for _, ips := range s.vlans {
			switch {
			case strings.EqualFold(s.md.Reachability, "sample"):
				ping(host, s.sampleTarget(ips))
			case strings.EqualFold(s.md.Reachability, "full"):
				for _, targetIP := range ips {
					ping(host, targetIP)
				}
			}
		}
	}
}

// customReachabilityTests runs the configured connection tests.
func (s *SOH) customReachabilityTests(ctx context.Context, wg *mm.StateGroup, ns string) {
	logger := plog.LoggerFromContext(ctx, plog.TypeSoh)

	for _, reach := range s.md.CustomReachability {
		var (
			host = reach.Src
			meta = map[string]any{"host": host, "target": reach.Dst}
		)

		if err := s.cannotReach(host); err != nil {
			wg.AddError(err, meta)

			continue
		}

		target := reach.Dst

		if fields := strings.Split(reach.Dst, "|"); len(fields) > 1 {
			target = s.hostIPs[fields[0]][fields[1]]
		}

		logger.Debug(
			"running custom reachability test",
			"from", host,
			"to", fmt.Sprintf("%s://%s:%d", reach.Proto, target, reach.Port),
		)

		wait, err := time.ParseDuration(reach.Wait)
		if err != nil && reach.Wait != "" {
			logger.Warn("invalid wait time provided, using default", "provided", reach.Wait)
		}

		s.connTest(ctx, wg, ns, host, target, reach.Proto, reach.Port, wait, reach.Packet)
	}
}

// cannotReach explains why host cannot originate reachability tests, nil when
// it can.
func (s *SOH) cannotReach(host string) error {
	if _, ok := s.failedNetwork[host]; ok {
		return errors.New("networking not configured on host")
	}

	if _, ok := s.c2Hosts[host]; !ok {
		return errors.New("c2 not active on host")
	}

	return nil
}

// sampleTarget picks a random address from ips, preferring one whose host
// passed the network config check.
func (s *SOH) sampleTarget(ips []string) string {
	var candidates []string

	for _, ip := range ips {
		if _, failed := s.failedNetwork[s.addrHosts[ip]]; !failed {
			candidates = append(candidates, ip)
		}
	}

	if len(candidates) == 0 {
		candidates = ips
	}

	return candidates[rand.IntN(len(candidates))] //nolint:gosec // weak random number generator
}

func (s *SOH) waitForFileTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // partial initialization
		kind:    "path",
		targets: s.md.HostFiles,
		test:    s.fileTest,
		waitMsg: "waiting for file tests to complete...",
		failMsg: "[✗] file not found on host",
		record:  func(h *HostState, st State) { h.Files = append(h.Files, st) },
	})
}

func (s *SOH) waitForFileAbsentTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // partial initialization
		kind:    "path",
		targets: s.md.HostFilesAbsent,
		test:    s.fileAbsentTest,
		waitMsg: "waiting for file absence tests to complete...",
		failMsg: "[✗] file found on host",
		record:  func(h *HostState, st State) { h.FilesAbsent = append(h.FilesAbsent, st) },
	})
}

func (s *SOH) waitForServiceTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // partial initialization
		kind:    "service",
		targets: s.md.HostServices,
		test:    s.serviceTest,
		waitMsg: "waiting for service tests to complete...",
		failMsg: "[✗] service not running on host",
		record:  func(h *HostState, st State) { h.Services = append(h.Services, st) },
	})
}

func (s *SOH) waitForContainerTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // partial initialization
		kind:    "container",
		targets: s.md.HostContainers,
		test:    s.containerTest,
		waitMsg: "waiting for docker container tests to complete...",
		failMsg: "[✗] docker container not up/healthy on host",
		record:  func(h *HostState, st State) { h.Containers = append(h.Containers, st) },
	})
}

func (s *SOH) waitForProcTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // partial initialization
		kind:    "proc",
		targets: s.md.HostProcesses,
		profile: func(p sohProfile) []string { return p.Processes },
		test:    s.procTest,
		waitMsg: "waiting for process tests to complete...",
		failMsg: "[✗] process not running on host",
		record:  func(h *HostState, st State) { h.Processes = append(h.Processes, st) },
	})
}

func (s *SOH) waitForPortTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // partial initialization
		kind:    "port",
		targets: s.md.HostListeners,
		profile: func(p sohProfile) []string { return p.Listeners },
		test:    s.portTest,
		waitMsg: "waiting for listener tests to complete...",
		failMsg: "[✗] host not listening on port",
		record:  func(h *HostState, st State) { h.Listeners = append(h.Listeners, st) },
	})
}

func (s *SOH) waitForCustomTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[customHostTest]{
		kind:    "test",
		targets: s.md.CustomHostTests,
		profile: func(p sohProfile) []customHostTest { return p.CustomTests },
		label:   func(t customHostTest) string { return t.Name },
		test:    s.customTest,
		waitMsg: "waiting for custom tests to complete...",
		failMsg: "[✗] test failed on host",
		record:  func(h *HostState, st State) { h.CustomTests = append(h.CustomTests, st) },
	})
}

func (s *SOH) waitForCPULoad(ctx context.Context, ns string) bool {
	var (
		logger = plog.LoggerFromContext(ctx, plog.TypeSoh)
		wg     = new(mm.StateGroup)
	)

	logger.Info("querying nodes for CPU load")

	// Only check for CPU load in hosts that have confirmed C2 availability.
	for host := range s.c2Hosts {
		s.cpuLoadTest(ctx, wg, ns, s.nodes[host])
	}

	for host := range s.c2Dead {
		wg.AddError(errC2Dead, map[string]any{"host": host})
	}

	if waitAll(ctx, wg, "waiting for CPU load details...") {
		return true
	}

	for _, state := range wg.States {
		host, _ := state.Meta["host"].(string)

		st := s.newState(state)
		if st.Error != "" {
			logger.Error("[✗] failed to get CPU load from host", "host", host, "err", state.Err)
		}

		s.updateHost(host, func(h *HostState) {
			h.CPULoad = st.Success

			if st.Error != "" {
				h.CPULoad = st.Error
			}
		})
	}

	return wg.ErrCount > 0
}

func (s SOH) cpuLoadTest(ctx context.Context, wg *mm.StateGroup, ns string, node ifaces.NodeSpec) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host}
		exec = `cat /proc/loadavg`
	)

	if isWindows(node) {
		exec = `powershell -command "Get-WmiObject Win32_Processor | Measure-Object -Property LoadPercentage -Average | Select -ExpandProperty Average"`
	}

	expected := func(resp string) error {
		if isWindows(node) {
			if resp == "" {
				return fmt.Errorf("no response for command '%s'", exec)
			}

			wg.AddSuccess(resp, meta)

			return nil
		}

		parts := strings.Fields(resp)

		if len(parts) != loadAvgParts {
			return fmt.Errorf("invalid response for command '%s': %s", exec, resp)
		}

		wg.AddSuccess(parts[0], meta)

		return nil
	}

	s.schedule(ctx, wg, ns, host, exec, meta, expected)
}

func (s SOH) isNetworkingConfigured(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	iface ifaces.NodeNetworkInterface,
) {
	var (
		addr    = iface.Address()
		host    = node.General().Hostname()
		gateway = iface.Gateway()
		meta    = map[string]any{"host": host}
		fail    = failAfterDeadline(time.Now().Add(s.md.c2Timeout))
	)

	// The address, then the default gateway, then the gateway answering pings
	// are confirmed in turn, each step scheduling the next once it passes.
	gwPingExpected := func(resp string) error {
		if pingFailed(node, resp) {
			return fail(errors.New("retry time expired waiting for gateway to be up"))
		}

		wg.AddSuccess(fmt.Sprintf("gateway %s is up", gateway), meta)

		return nil
	}

	gwExpected := func(resp string) error {
		var found bool

		if isWindows(node) {
			found, _ = regexp.MatchString("0.0.0.0\\s+0.0.0.0\\s+"+gateway, resp)
		} else {
			found = strings.Contains(resp, "default via "+gateway)
		}

		if !found {
			return fail(errors.New("retry time expired waiting for gateway to be set"))
		}

		wg.AddSuccess(fmt.Sprintf("gateway %s configured", gateway), meta)
		s.schedule(ctx, wg, ns, host, pingCommand(node, gateway), meta, gwPingExpected)

		return nil
	}

	ipExpected := func(resp string) error {
		if addr != "" {
			want := fmt.Sprintf("%s/%d", addr, iface.Mask())

			if isWindows(node) {
				want = addr
			}

			if !strings.Contains(resp, want) {
				return fail(errors.New("retry time expired waiting for IP to be set"))
			}

			wg.AddSuccess(fmt.Sprintf("IP %s configured", addr), meta)
		}

		if gateway != "" {
			exec := "ip route"

			if isWindows(node) {
				exec = "route print"
			}

			s.schedule(ctx, wg, ns, host, exec, meta, gwExpected)
		}

		return nil
	}

	exec := "ip addr"

	if isWindows(node) {
		exec = "ipconfig /all"
	}

	s.schedule(ctx, wg, ns, host, exec, meta, ipExpected)
}

func (s SOH) pingTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	target string,
) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "target": target}
	)

	expected := func(resp string) error {
		if pingFailed(node, resp) {
			return errors.New("no successful pings")
		}

		wg.AddSuccess(fmt.Sprintf("pinging %s succeeded", target), meta)

		return nil
	}

	s.schedule(ctx, wg, ns, host, pingCommand(node, target), meta, expected)
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
	cmd := &mm.C2ParallelCommand{ //nolint:exhaustruct // partial initialization
		Wait:    wg,
		Limiter: s.limiter,
		Options: append(s.c2Options(ns, src), mm.C2TestConn(test)),
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

	if isWindows(node) {
		exec = fmt.Sprintf(`powershell -command "Get-Process %s -ErrorAction SilentlyContinue"`, proc)
	}

	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "proc": proc}
		fail = failAfterRetries()
	)

	expected := func(resp string) error {
		if resp == "" {
			return fail(errors.New("process not running"))
		}

		wg.AddSuccess("process running", meta)

		return nil
	}

	s.schedule(ctx, wg, ns, host, exec, meta, expected)
}

func (s SOH) fileTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	path string,
) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "path": path}
		fail = failAfterRetries()
	)

	expected := func(resp string) error {
		if strings.TrimSpace(resp) != "present" {
			return fail(errors.New("file not found"))
		}

		wg.AddSuccess("file found", meta)

		return nil
	}

	s.schedule(ctx, wg, ns, host, fileCheckCommand(node.Hardware().OSType(), path), meta, expected)
}

func fileCheckCommand(osType, path string) string {
	if strings.EqualFold(osType, "windows") {
		path = strings.ReplaceAll(path, "'", "''")

		return fmt.Sprintf(
			`powershell -NoProfile -Command "if (Test-Path -LiteralPath '%s' -PathType Leaf) { 'present' }"`,
			path,
		)
	}

	path = strings.ReplaceAll(path, "'", `'"'"'`)

	return fmt.Sprintf("stat -c present -- '%s'", path)
}

func (s SOH) fileAbsentTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	path string,
) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "path": path}
		fail = failAfterRetries()
	)

	expected := func(resp string) error {
		if strings.TrimSpace(resp) == "present" {
			return fail(errors.New("file exists"))
		}

		wg.AddSuccess("file not found", meta)

		return nil
	}

	s.schedule(ctx, wg, ns, host, fileCheckCommand(node.Hardware().OSType(), path), meta, expected)
}

func (s SOH) serviceTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	service string,
) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "service": service}
		fail = failAfterRetries()
	)

	expected := func(resp string) error {
		if strings.TrimSpace(resp) != "active" {
			return fail(errors.New("service not active"))
		}

		wg.AddSuccess("service running", meta)

		return nil
	}

	s.schedule(ctx, wg, ns, host, serviceCheckCommand(node.Hardware().OSType(), service), meta, expected)
}

func serviceCheckCommand(osType, service string) string {
	if strings.EqualFold(osType, "windows") {
		service = strings.ReplaceAll(service, "'", "''")

		return fmt.Sprintf(
			`powershell -NoProfile -Command "if ((Get-Service -Name '%s' -ErrorAction SilentlyContinue).Status -eq 'Running') { 'active' }"`,
			service,
		)
	}

	service = strings.ReplaceAll(service, "'", `'"'"'`)

	return fmt.Sprintf("systemctl is-active -- '%s'", service)
}

func (s SOH) containerTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	container string,
) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{"host": host, "container": container}
		fail = failAfterRetries()
	)

	expected := func(resp string) error {
		status := strings.ToLower(strings.TrimSpace(resp))

		switch status {
		case "healthy", "running":
			wg.AddSuccess(fmt.Sprintf("container %s", status), meta)

			return nil
		case "unhealthy":
			return errors.New("container unhealthy")
		case "starting", "":
			return fail(errors.New("container not up"))
		default: // exited, dead, paused, restarting, created, or unknown container
			return fail(fmt.Errorf("container not up (state: %s)", status))
		}
	}

	s.schedule(ctx, wg, ns, host, containerCheckCommand(node.Hardware().OSType(), container), meta, expected)
}

// containerCheckCommand builds a command that queries the state of a docker
// container on the host. If the container has a configured healthcheck, its
// health status (eg. "healthy", "unhealthy", "starting") is returned.
// Otherwise, the container's run state (eg. "running", "exited") is returned.
//
// Note: commands are executed directly by the guest's C2 agent (miniccc),
// not via a shell, so shell-only syntax (eg. stderr redirection) must not be
// used here on Linux -- it would be passed through as a literal, unparsed
// argument and corrupt the command. The Windows command is an exception
// since it's wrapped in a `powershell -Command "..."` invocation, which
// itself acts as an interpreter for the quoted script text.
func containerCheckCommand(osType, container string) string {
	const tmpl = `{{if .State.Health}}{{.State.Health.Status}}` +
		`{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}`

	if strings.EqualFold(osType, "windows") {
		container = strings.ReplaceAll(container, "'", "''")

		return fmt.Sprintf(
			`powershell -NoProfile -Command "docker inspect --format='%s' -- '%s' 2>$null"`,
			tmpl,
			container,
		)
	}

	container = strings.ReplaceAll(container, "'", `'"'"'`)

	return fmt.Sprintf("docker inspect --format='%s' -- '%s'", tmpl, container)
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
		fail = failAfterRetries()
	)

	exec := "ss -lntu state all"
	target := strings.Split(port, ":")

	if isWindows(node) {
		var filter string

		switch len(target) {
		case 1:
			filter = fmt.Sprintf("$_.LocalPort -eq %s", target[0])
		case portParts:
			switch {
			case target[0] == "": // :<port>
				filter = fmt.Sprintf("$_.LocalPort -eq %s", target[1])
			case target[1] == "": // <ip>: (why?!)
				filter = fmt.Sprintf("$_.LocalAddress -eq '%s'", target[0])
			default: // <ip>:<port>
				filter = fmt.Sprintf("$_.LocalAddress -eq '%s' -and $_.LocalPort -eq %s", target[0], target[1])
			}
		default:
			wg.AddError(fmt.Errorf("invalid port %s provided", port), meta)

			return
		}

		exec = fmt.Sprintf(
			`powershell -command "$tcp=Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object { %s }; $udp=Get-NetUDPEndpoint -ErrorAction SilentlyContinue | Where-Object { %s }; @($tcp+$udp) | Select-Object -First 1"`,
			filter,
			filter,
		)
	} else {
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

	expected := func(resp string) error {
		// ss prints a header line; the Windows query prints only matches.
		lineCount := 1

		if isWindows(node) {
			lineCount = 0
		}

		if lines := trim(resp); len(lines) <= lineCount {
			return fail(errors.New("not listening on port"))
		}

		wg.AddSuccess("listening on port", meta)

		return nil
	}

	s.schedule(ctx, wg, ns, host, exec, meta, expected)
}

// c2Options addresses host for a C2 command, by UUID and cluster host where
// the VM was found when the run started.
func (s SOH) c2Options(ns, host string) []mm.C2Option {
	opts := []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Timeout(s.md.c2Timeout)}

	if vm, ok := s.vms[host]; ok && vm.UUID != "" {
		opts = append(opts, mm.C2VMUUID(vm.UUID), mm.C2VMHost(vm.Host))
	}

	if s.md.c2AppearGrace != nil {
		opts = append(opts, mm.C2AppearGrace(*s.md.c2AppearGrace))
	}

	if s.md.c2ClientGrace != nil {
		opts = append(opts, mm.C2ClientGrace(*s.md.c2ClientGrace))
	}

	if s.md.useUUIDForC2Active(host) {
		opts = append(opts, mm.C2IDClientsByUUID())
	}

	return opts
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

	// All three paths below must agree on the same minimega-relative path
	relPath := fmt.Sprintf("%s/%s", ns, script)
	path := mm.GetMMFullPath(relPath)
	err := os.WriteFile(path, []byte(test.TestScript), 0o600)
	if err != nil {
		wg.AddError(fmt.Errorf("unable to write test script to file: %w", err), meta)

		return
	}

	command := fmt.Sprintf("%s /tmp/miniccc/files/%s", executor, relPath)

	cmd := &mm.C2ParallelCommand{ //nolint:exhaustruct // partial initialization
		Wait:    wg,
		Limiter: s.limiter,
		Options: append(s.c2Options(ns, host), mm.C2SendFile(relPath), mm.C2Command(command)),
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
			if drives := node.Hardware().Drives(); len(drives) > 0 &&
				filepath.Base(drives[0].Image()) == skipHost {
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

func periodicallyNotify(ctx context.Context, msg string) context.CancelFunc {
	var (
		logger       = plog.LoggerFromContext(ctx, plog.TypeSoh)
		cctx, cancel = context.WithCancel(ctx)
		ticker       = time.NewTicker(notifyInterval)
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
