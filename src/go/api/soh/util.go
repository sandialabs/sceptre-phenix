package soh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog"
)

const (
	appName       = "soh"
	expKey        = "exp"
	hostKey       = "host"
	typeKey       = "type"
	protoKey      = "proto"
	srcKey        = "src"
	dstKey        = "dst"
	targetKey     = "target"
	osTypeLinux   = "linux"
	osTypeWindows = "windows"
	loadAvgParts  = 5
	portParts     = 2
	byteMask      = 0xFF
	shift8        = 8
	shift16       = 16
	shift24       = 24
	filePresent   = "present" // what the file check command prints for a file
)

var stringSpacePattern = regexp.MustCompile(`\s+`)

var errC2NotActive = errors.New("c2 not active on host")

func (s *SOH) deployCapture(ctx context.Context, exp *types.Experiment, dryrun bool) error {
	logger := sohLogger(ctx, exp.Metadata.Name)

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

		if reason := captureSkipReason(node); reason != "" {
			logger.Warn("skipping packet capture for host", hostKey, nodeToMonitor, "reason", reason)

			continue
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

	if len(caps) == 0 {
		return nil
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

// captureSkipReason gives the reason that soh deploys no packet capture monitor
// for node, or "" when soh deploys one. A node marked not to boot includes a
// node whose disk image the startup app found missing.
func captureSkipReason(node ifaces.NodeSpec) string {
	switch {
	case node == nil:
		return "host is not in the topology"
	case node.External():
		return "node is external and not running in minimega"
	case *node.General().DoNotBoot():
		return "node will not boot"
	default:
		return ""
	}
}

func (s *SOH) buildElasticServerNode(
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
	node.AddHardware(osTypeLinux, cpu, mem)
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

func (s *SOH) buildPacketBeatNode( //nolint:funlen // complex logic
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
			typeKey:   "ethernet",
			"vlan":    "MGMT",
			"address": ip,
			"mask":    cidr,
			protoKey:  "static",
			"bridge":  exp.Spec.DefaultBridge(),
		},
	}

	for i, ifaceToMonitor := range s.md.PacketCapture.CaptureHosts[monitored] {
		for j, iface := range target.Network().Interfaces() {
			if iface.Name() == ifaceToMonitor {
				monitorIface := map[string]any{
					"name":   fmt.Sprintf("MONITOR%d", i),
					typeKey:  "ethernet",
					"vlan":   iface.VLAN(),
					protoKey: "static",
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
		typeKey:  "VirtualMachine",
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
			"os_type": osTypeLinux,
		},
		"injections": []map[string]any{
			{
				srcKey: hostnameFile,
				dstKey: "/etc/phenix/startup/1_hostname-start.sh",
			},
			{
				srcKey: timezoneFile,
				dstKey: "/etc/phenix/startup/2_timezone-start.sh",
			},
			{
				srcKey: ifaceFile,
				dstKey: "/etc/phenix/startup/3_interfaces-start.sh",
			},
			{
				srcKey: packetBeatConfigFile,
				dstKey: "/etc/packetbeat/packetbeat.yml",
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

// markNoC2 stops checks to host for the rest of this run, because each check
// waits c2Timeout again, and records that host has no active C2.
func (s *SOH) markNoC2(host string) {
	delete(s.c2Hosts, host)
	s.noC2[host] = struct{}{}
}

// c2Unavailable reports whether soh cannot send a check to host. When an earlier
// check in this run found no active C2 on host, it also records the check that
// meta describes as failed. Hosts that soh skips per config get no entry.
func (s *SOH) c2Unavailable(logger *slog.Logger, wg *mm.StateGroup, host string, meta map[string]any) bool {
	if _, ok := s.c2Hosts[host]; ok {
		return false
	}

	if _, ok := s.noC2[host]; ok {
		wg.AddError(errC2NotActive, meta)

		return true
	}

	logger.Debug("skipping host per config", hostKey, host)

	return true
}

//nolint:cyclop,funlen,gocyclo,maintidx // complex logic
func (s *SOH) waitForReachabilityTest(ctx context.Context, ns string, checks map[string]bool) bool {
	if s.md.SkipNetworkConfig || !checks["network-config"] {
		return false
	}

	logger := sohLogger(ctx, ns)

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
				skipHost = errC2NotActive
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
						idx := rand.IntN(len(ips)) //nolint:gosec // G404 picks a target, not a secret
						targetIP := ips[idx]

						targetHost := s.addrHosts[targetIP]

						if _, ok := s.failedNetwork[targetHost]; ok {
							continue
						}

						targeted = true

						if skipHost != nil {
							wg.AddError(skipHost, map[string]any{hostKey: host, targetKey: targetIP})
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
						idx := rand.IntN(len(ips)) //nolint:gosec // G404 picks a target, not a secret
						targetIP := ips[idx]

						// This target host failed the network config test, so don't try
						// to do any reachability to it.
						var (
							err  = errors.New("networking not configured on target")
							meta = map[string]any{hostKey: host, targetKey: targetIP}
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
								meta = map[string]any{hostKey: host, targetKey: targetIP}
							)

							wg.AddError(err, meta)

							continue
						}

						if skipHost != nil {
							wg.AddError(skipHost, map[string]any{hostKey: host, targetKey: targetIP})
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

			if s.c2Unavailable(logger, wg, host, map[string]any{hostKey: host, targetKey: reach.Dst}) {
				continue
			}

			if _, ok := s.failedNetwork[host]; ok {
				// This host failed the network config test, so don't test from it.
				wg.AddError(
					errors.New("networking not configured on host"),
					map[string]any{hostKey: host, targetKey: reach.Dst},
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
		logger,
		"waiting for reachability tests to complete...",
		notifyInterval,
	)

	// Wait for hosts to test reachability to other hosts.
	wg.Wait()
	cancel()

	for _, state := range wg.States {
		var (
			host, _   = state.Meta[hostKey].(string)
			target, _ = state.Meta[targetKey].(string)
		)

		// Convert target IP to hostname.
		hostname := s.addrHosts[target]

		if hostname != "" { // might be empty if target IP not in topology
			state.Meta[targetKey] = hostname
		}

		state.Meta["ip"] = target

		st := State{ //nolint:exhaustruct // partial initialization
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		err := state.Err
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				s.markNoC2(host)
			}

			st.Error = err.Error()

			if _, ok := state.Meta["port"]; ok {
				var (
					port, _  = state.Meta["port"].(int)
					proto, _ = state.Meta[protoKey].(string)
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

func (s *SOH) waitForFileTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // no profile form
		kind:    "path",
		targets: s.md.HostFiles,
		test:    s.fileTest,
		waitMsg: "waiting for file tests to complete...",
		failMsg: "[✗] file not found on host",
		record:  func(h *HostState, st State) { h.Files = append(h.Files, st) },
	})
}

func (s *SOH) waitForFileAbsentTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // no profile form
		kind:    "path",
		targets: s.md.HostFilesAbsent,
		test:    s.fileAbsentTest,
		waitMsg: "waiting for file absence tests to complete...",
		failMsg: "[✗] file found on host",
		record:  func(h *HostState, st State) { h.FilesAbsent = append(h.FilesAbsent, st) },
	})
}

func (s *SOH) waitForServiceTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // no profile form
		kind:    "service",
		targets: s.md.HostServices,
		test:    s.serviceTest,
		waitMsg: "waiting for service tests to complete...",
		failMsg: "[✗] service not running on host",
		record:  func(h *HostState, st State) { h.Services = append(h.Services, st) },
	})
}

func (s *SOH) waitForContainerTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // no profile form
		kind:    "container",
		targets: s.md.HostContainers,
		test:    s.containerTest,
		waitMsg: "waiting for docker container tests to complete...",
		failMsg: "[✗] docker container not up/healthy on host",
		record:  func(h *HostState, st State) { h.Containers = append(h.Containers, st) },
	})
}

func (s *SOH) waitForProcTest(ctx context.Context, ns string) bool {
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // label is the string
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
	return runHostCheck(ctx, s, ns, hostCheck[string]{ //nolint:exhaustruct // label is the string
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

func (s *SOH) waitForCPULoad(ctx context.Context, ns string) bool { //nolint:funlen // complex logic
	var (
		logger = sohLogger(ctx, ns)
		wg     = new(mm.StateGroup)

		statusMu sync.Mutex // guards s.status, which each host's goroutine updates
	)

	logger.Info("querying nodes for CPU load")

	// Hosts found without C2 earlier in this run get no command, but their CPU
	// load still says why it is missing.
	for host := range s.noC2 {
		wg.AddError(errC2NotActive, map[string]any{hostKey: host})
	}

	// Only check for CPU load in hosts that have confirmed C2 availability.
	for host := range s.c2Hosts {
		wg.Add(1)

		go func(host string) {
			defer wg.Done()

			node := s.nodes[host]
			exec := `cat /proc/loadavg`

			if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
				exec = `powershell -command "Get-WmiObject Win32_Processor | Measure-Object -Property LoadPercentage -Average | Select -ExpandProperty Average"`
			}

			if err := s.limiter.Acquire(ctx); err != nil {
				wg.AddError(fmt.Errorf("waiting to run command '%s': %w", exec, err), map[string]any{hostKey: host})

				return
			}

			defer s.limiter.Release()

			id, err := mm.ExecC2Command(append(s.c2Options(ns, host), mm.C2Command(exec))...)
			if err != nil {
				wg.AddError(
					fmt.Errorf("executing command '%s': %w", exec, err),
					map[string]any{hostKey: host},
				)

				return
			}

			resp, err := mm.WaitForC2Response(append(s.c2Options(ns, host), mm.C2CommandID(id), mm.C2Context(ctx))...)
			if err != nil {
				wg.AddError(
					fmt.Errorf("getting response for command '%s': %w", exec, err),
					map[string]any{hostKey: host},
				)

				return
			}

			load := resp

			if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
				if resp == "" {
					wg.AddError(
						fmt.Errorf("no response for command '%s'", exec),
						map[string]any{hostKey: host},
					)

					return
				}
			} else {
				parts := strings.Fields(resp)

				if len(parts) != loadAvgParts {
					wg.AddError(
						fmt.Errorf("invalid response for command '%s': %s", exec, resp),
						map[string]any{hostKey: host},
					)

					return
				}

				load = parts[0]
			}

			statusMu.Lock()
			defer statusMu.Unlock()

			hostState, ok := s.status[host]
			if !ok {
				hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
			}

			hostState.CPULoad = load
			s.status[host] = hostState
		}(host)
	}

	cancel := periodicallyNotify(ctx, logger, "waiting for CPU load details...", notifyInterval)

	// Wait for CPU load requests to complete.
	wg.Wait()
	cancel()

	for _, state := range wg.States {
		host, _ := state.Meta[hostKey].(string)

		err := state.Err
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				s.markNoC2(host)
			}

			hostState, ok := s.status[host]
			if !ok {
				hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
			}

			hostState.CPULoad = err.Error()
			s.status[host] = hostState

			logger.Error("[✗] failed to get CPU load from host", hostKey, host, "err", err)
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
	// Time spent waiting for a limiter slot does not count against the probes.
	deadline := newRetryDeadline(s.now, s.md.c2Timeout)

	var (
		addr    = iface.Address()
		host    = node.General().Hostname()
		gateway = iface.Gateway()
		meta    = map[string]any{hostKey: host}
	)

	// First, we wait for the IP address to be set on the interface. Then, we wait
	// for the default gateway to be set. Last, we wait for the default gateway to
	// be up (pingable). This is all done via nested commands streamed to the C2
	// processor within `expected` functions.
	ipExpected := func(resp string) error {
		if addr != "" {
			if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
				// If `resp` doesn't contain the IP address, then the IP address isn't
				// configured yet, so keep retrying the C2 command.
				if !strings.Contains(resp, addr) {
					if deadline.passed() {
						return errors.New("retry time expired waiting for IP to be set")
					}

					return mm.C2RetryError{Delay: s.retryDelay}
				}
			} else {
				cidr := fmt.Sprintf("%s/%d", addr, iface.Mask())

				// If `resp` doesn't contain the IP address, then the IP address isn't
				// configured yet, so keep retrying the C2 command.
				if !strings.Contains(resp, cidr) {
					if deadline.passed() {
						return errors.New("retry time expired waiting for IP to be set")
					}

					return mm.C2RetryError{Delay: s.retryDelay}
				}
			}

			wg.AddSuccess(fmt.Sprintf("IP %s configured", addr), meta)
		}

		if gateway != "" {
			// The IP address is now set, so schedule a C2 command for determining if
			// the default gateway is set.
			gwExpected := func(resp string) error {
				if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
					expected := "0.0.0.0\\s+0.0.0.0\\s+" + gateway

					// If `resp` doesn't contain the default gateway, then the default gateway
					// isn't configured yet, so keep retrying the C2 command.
					if found, _ := regexp.MatchString(expected, resp); !found {
						if deadline.passed() {
							return errors.New("retry time expired waiting for gateway to be set")
						}

						return mm.C2RetryError{Delay: s.retryDelay}
					}
				} else {
					expected := "default via " + gateway

					// If `resp` doesn't contain the default gateway, then the default gateway
					// isn't configured yet, so keep retrying the C2 command.
					if !strings.Contains(resp, expected) {
						if deadline.passed() {
							return errors.New("retry time expired waiting for gateway to be set")
						}

						return mm.C2RetryError{Delay: s.retryDelay}
					}
				}

				wg.AddSuccess(fmt.Sprintf("gateway %s configured", gateway), meta)

				// The default gateway is now set, so schedule a C2 command for
				// determining if the default gateway is up (pingable).
				gwPingExpected := func(resp string) error {
					if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
						// If `resp` contains `Destination host unreachable`, the
						// default gateway isn't up (pingable) yet, so keep retrying the C2
						// command.
						if strings.Contains(resp, "Destination host unreachable") {
							if deadline.passed() {
								return errors.New("retry time expired waiting for gateway to be up")
							}

							return mm.C2RetryError{Delay: s.retryDelay}
						}
					} else {
						// If `resp` contains `0 received`, the default gateway isn't up
						// (pingable) yet, so keep retrying the C2 command.
						if strings.Contains(resp, "0 received") {
							if deadline.passed() {
								return errors.New("retry time expired waiting for gateway to be up")
							}

							return mm.C2RetryError{Delay: s.retryDelay}
						}
					}

					wg.AddSuccess(fmt.Sprintf("gateway %s is up", gateway), meta)

					return nil
				}

				exec, err := pingCommand(node, gateway)
				if err != nil {
					return fmt.Errorf("pinging gateway: %w", err)
				}

				cmd := s.newParallelCommand(ns, host, exec)
				cmd.Wait = wg
				cmd.Meta = map[string]any{hostKey: host}
				cmd.Expected = gwPingExpected
				cmd.Queued = deadline.queued

				mm.ScheduleC2ParallelCommand(ctx, cmd)

				return nil
			}

			exec := "ip route"

			if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
				exec = "route print"
			}

			cmd := s.newParallelCommand(ns, host, exec)
			cmd.Wait = wg
			cmd.Meta = map[string]any{hostKey: host}
			cmd.Expected = gwExpected
			cmd.Queued = deadline.queued

			mm.ScheduleC2ParallelCommand(ctx, cmd)
		}

		return nil
	}

	exec := "ip addr"

	if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
		exec = "ipconfig /all"
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = ipExpected
	cmd.Queued = deadline.queued

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

// pingCommand returns the cc exec command that pings target once from node.
func pingCommand(node ifaces.NodeSpec, target string) (string, error) {
	count := "-c"
	if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
		count = "-n"
	}

	return ccExecCommand("ping", count, "1", target)
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
		meta = map[string]any{hostKey: host, targetKey: target}
	)

	exec, err := pingCommand(node, target)
	if err != nil {
		wg.AddError(fmt.Errorf("pinging %s: %w", target, err), meta)

		return
	}

	expected := func(resp string) error {
		if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
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

	meta := map[string]any{hostKey: src, targetKey: dst, "port": port, protoKey: proto}

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
	var (
		host = node.General().Hostname()
		meta = map[string]any{hostKey: host, "proc": proc}
	)

	exec, err := procCheckCommand(node.Hardware().OSType(), proc)
	if err != nil {
		wg.AddError(err, meta)

		return
	}

	retries := 5
	expected := func(resp string) error {
		running, err := procRunning(resp)
		if err != nil {
			return err
		}

		if !running {
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: s.retryDelay}
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

// procRunning reads the response of the procCheckCommand command. The
// response holds stdout and stderr together. It reports true when the
// response holds one or more process IDs, and an error when a line that is
// not empty is not a process ID, for example a pgrep or PowerShell error.
func procRunning(resp string) (bool, error) {
	running := false

	for line := range strings.Lines(resp) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.Trim(line, "0123456789") != "" {
			return false, fmt.Errorf("process check failed: %q", line)
		}

		running = true
	}

	return running, nil
}

// procCheckCommand builds the command that prints the ID of each process that
// matches the pattern proc, one on each line: `pgrep -f` on Linux and
// Get-Process on Windows.
func procCheckCommand(osType, proc string) (string, error) {
	if proc == "" {
		return "", errors.New("empty process pattern provided")
	}

	if strings.EqualFold(osType, osTypeWindows) {
		return ccExecCommand(
			"powershell", "-NoProfile", "-Command",
			"Get-Process -Name "+psQuote(proc)+" -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id",
		)
	}

	return ccExecCommand("pgrep", "-f", "--", proc)
}

//nolint:dupl // similar to fileAbsentTest
func (s SOH) fileTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	path string,
) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{hostKey: host, "path": path}
	)

	retries := 5
	expected := func(resp string) error {
		if strings.TrimSpace(resp) != filePresent {
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: s.retryDelay}
			}

			return errors.New("file not found")
		}

		wg.AddSuccess("file found", meta)

		return nil
	}

	exec, err := fileCheckCommand(node.Hardware().OSType(), path)
	if err != nil {
		wg.AddError(err, meta)

		return
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func fileCheckCommand(osType, path string) (string, error) {
	if strings.EqualFold(osType, osTypeWindows) {
		return ccExecCommand(
			"powershell", "-NoProfile", "-Command",
			"if (Test-Path -LiteralPath "+psQuote(path)+" -PathType Leaf) { 'present' }",
		)
	}

	return ccExecCommand("stat", "-c", filePresent, "--", path)
}

//nolint:dupl // similar to fileTest
func (s SOH) fileAbsentTest(
	ctx context.Context,
	wg *mm.StateGroup,
	ns string,
	node ifaces.NodeSpec,
	path string,
) {
	var (
		host = node.General().Hostname()
		meta = map[string]any{hostKey: host, "path": path}
	)

	retries := 5
	expected := func(resp string) error {
		if strings.TrimSpace(resp) == filePresent {
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: s.retryDelay}
			}

			return errors.New("file exists")
		}

		wg.AddSuccess("file not found", meta)

		return nil
	}

	exec, err := fileCheckCommand(node.Hardware().OSType(), path)
	if err != nil {
		wg.AddError(err, meta)

		return
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
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
	)

	retries := 5
	expected := func(resp string) error {
		if strings.TrimSpace(resp) != "active" {
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: s.retryDelay}
			}

			return errors.New("service not active")
		}

		wg.AddSuccess("service running", meta)

		return nil
	}

	exec, err := serviceCheckCommand(node.Hardware().OSType(), service)
	if err != nil {
		wg.AddError(err, meta)

		return
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func serviceCheckCommand(osType, service string) (string, error) {
	if strings.EqualFold(osType, osTypeWindows) {
		return ccExecCommand(
			"powershell", "-NoProfile", "-Command",
			"if ((Get-Service -Name "+psQuote(service)+
				" -ErrorAction SilentlyContinue).Status -eq 'Running') { 'active' }",
		)
	}

	return ccExecCommand("systemctl", "is-active", "--", service)
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
	)

	retries := 5
	expected := func(resp string) error {
		status := strings.ToLower(strings.TrimSpace(resp))

		switch status {
		case "healthy", "running":
			wg.AddSuccess(fmt.Sprintf("container %s", status), meta)

			return nil
		case "unhealthy":
			return errors.New("container unhealthy")
		case "starting", "":
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: s.retryDelay}
			}

			return errors.New("container not up")
		default: // exited, dead, paused, restarting, created, or unknown container
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: s.retryDelay}
			}

			return fmt.Errorf("container not up (state: %s)", status)
		}
	}

	exec, err := containerCheckCommand(node.Hardware().OSType(), container)
	if err != nil {
		wg.AddError(err, meta)

		return
	}

	cmd := s.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

// containerCheckCommand builds a command that queries the state of a docker
// container on the host. If the container has a configured healthcheck, its
// health status (eg. "healthy", "unhealthy", "starting") is returned.
// Otherwise, the container's run state (eg. "running", "exited") is returned.
// miniccc runs the Linux command without a shell. On Windows, PowerShell
// removes the docker error lines, without a $ that minimega would expand.
func containerCheckCommand(osType, container string) (string, error) {
	const tmpl = `{{if .State.Health}}{{.State.Health.Status}}` +
		`{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}`

	if strings.EqualFold(osType, osTypeWindows) {
		return ccExecCommand(
			"powershell", "-NoProfile", "-Command",
			"docker inspect --format="+psQuote(tmpl)+" -- "+psQuote(container)+
				" 2>&1 | Where-Object -Property Exception -Not",
		)
	}

	return ccExecCommand("docker", "inspect", "--format="+tmpl, "--", container)
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
		meta = map[string]any{hostKey: host, "port": port}
	)

	exec, err := portCheckCommand(node.Hardware().OSType(), port)
	if err != nil {
		wg.AddError(err, meta)

		return
	}

	retries := 5
	expected := func(resp string) error {
		lineCount := 1

		if strings.EqualFold(node.Hardware().OSType(), osTypeWindows) {
			lineCount = 0
		}

		lines := trim(resp)

		if len(lines) <= lineCount {
			if retries > 0 {
				retries--

				return mm.C2RetryError{Delay: s.retryDelay}
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

// portCheckCommand builds the command that lists the listeners that match
// port, in the form <port>, :<port>, <ip>: or <ip>:<port>: `ss` on Linux and
// Get-NetTCPConnection and Get-NetUDPEndpoint on Windows.
func portCheckCommand(osType, port string) (string, error) {
	target := strings.Split(port, ":")

	var addr, num string

	switch len(target) {
	case 1:
		num = target[0]
	case portParts:
		addr, num = target[0], target[1]
	}

	if len(target) > portParts || (addr == "" && num == "") {
		return "", fmt.Errorf("invalid port %s provided", port)
	}

	if strings.Trim(addr+num, alnumChars+listenerPunct) != "" {
		return "", fmt.Errorf(
			"%w: listener %q may contain only letters, digits and the characters %s",
			errUnsafeC2Arg, port, listenerPunct,
		)
	}

	if strings.EqualFold(osType, osTypeWindows) {
		script := "@(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue; " +
			"Get-NetUDPEndpoint -ErrorAction SilentlyContinue)"

		if addr != "" {
			script += " | Where-Object LocalAddress -eq " + psQuote(addr)
		}

		if num != "" {
			if strings.Trim(num, "0123456789") != "" {
				num = psQuote(num)
			}

			script += " | Where-Object LocalPort -eq " + num
		}

		return ccExecCommand("powershell", "-NoProfile", "-Command", script+" | Select-Object -First 1")
	}

	var filter []string

	if addr != "" {
		filter = append(filter, "src = "+addr)
	}

	if num != "" {
		filter = append(filter, "sport = "+num)
	}

	return ccExecCommand("ss", "-lntu", "state", "all", strings.Join(filter, " and "))
}

func (s SOH) newParallelCommand(ns, host, exec string) *mm.C2ParallelCommand {
	return &mm.C2ParallelCommand{ //nolint:exhaustruct // partial initialization
		Limiter: s.limiter,
		Options: append(s.c2Options(ns, host), mm.C2Command(exec)),
	}
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
	meta := map[string]any{hostKey: host, "test": test.Name}

	if test.TestScript == "" {
		wg.AddError(errors.New("no test script provided"), meta)

		return
	}

	if test.TestStdout == "" && test.TestStderr == "" && test.ValidateStdout == "" &&
		test.ValidateStderr == "" {
		wg.AddError(errors.New("no output test strings or validators provided"), meta)

		return
	}

	script := customScriptName(host, test.Name)

	executor := test.Executor
	if executor == "" {
		switch strings.ToLower(node.Hardware().OSType()) {
		case osTypeWindows:
			executor = "powershell -NoProfile -ExecutionPolicy bypass -File"
		default:
			executor = "bash"
		}
	}

	if strings.HasPrefix(executor, "powershell") {
		script += ".ps1"
	}

	// All three paths below must agree on the same minimega-relative path
	relPath := ns + "/" + script

	path := mm.GetMMFullPath(relPath)
	if dir := mm.GetMMFullPath(""); path == dir || !file.WithinDir(dir, path) {
		wg.AddError(fmt.Errorf("test script path %s is not in the minimega files directory %s", path, dir), meta)

		return
	}

	send, err := ccExecCommand(relPath)
	if err != nil {
		wg.AddError(err, meta)

		return
	}

	vmPath, err := ccExecCommand("/tmp/miniccc/files/" + relPath)
	if err != nil {
		wg.AddError(err, meta)

		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		wg.AddError(fmt.Errorf("unable to make the test script folder: %w", err), meta)

		return
	}

	if err := os.WriteFile(path, []byte(test.TestScript), 0o600); err != nil {
		wg.AddError(fmt.Errorf("unable to write test script to file: %w", err), meta)

		return
	}

	cmd := &mm.C2ParallelCommand{ //nolint:exhaustruct // partial initialization
		Wait:    wg,
		Limiter: s.limiter,
		Options: append(s.c2Options(ns, host), mm.C2SendFile(send), mm.C2Command(executor+" "+vmPath)),
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

// customScriptName returns the file name of the script of the custom test
// name on host: "<host>-<name>". Each run of white space and each other
// character that is not a letter, a digit, '-', '_' or '.' becomes '_'. If
// this changes the name, a '-' and a hash of host and name follow, so that two
// test names on one host do not share one file.
func customScriptName(host, name string) string {
	safe := func(r rune) rune {
		if strings.ContainsRune(alnumChars+"-_.", r) {
			return r
		}

		return '_'
	}

	script := strings.Map(safe, host) + "-" + strings.Map(safe, stringSpacePattern.ReplaceAllString(name, "_"))
	if script == host+"-"+name {
		return script
	}

	sum := sha256.Sum256([]byte(host + "\x00" + name))

	return script + "-" + hex.EncodeToString(sum[:4])
}

func runValidator(ctx context.Context, script, input, label string) error {
	f, err := os.CreateTemp("", "soh-validator-")
	if err != nil {
		return fmt.Errorf("unable to create %s validator script", label)
	}
	defer func() { _ = os.Remove(f.Name()) }() //nolint:gosec // G703 the temporary file it created

	if _, err := f.WriteString(script); err != nil {
		_ = f.Close()
		return fmt.Errorf("unable to create %s validator script", label)
	}
	_ = f.Close()

	bash, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash command is not available for %s validation", label)
	}

	cmd := exec.CommandContext(ctx, bash, f.Name()) //nolint:gosec // G702 runs the script it wrote with bash
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
		if ext := filepath.Ext(skipHost); ext == ".qc2" || ext == ".qcow" || ext == ".qcow2" {
			if drives := node.Hardware().Drives(); len(drives) > 0 && filepath.Base(drives[0].Image()) == skipHost {
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

// sohLogger returns the soh logger from ctx with the experiment name added.
func sohLogger(ctx context.Context, exp string) *slog.Logger {
	return plog.LoggerFromContext(ctx, plog.TypeSoh).With(expKey, exp)
}

func periodicallyNotify(
	ctx context.Context,
	logger *slog.Logger,
	msg string,
	d time.Duration,
) context.CancelFunc {
	var (
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
