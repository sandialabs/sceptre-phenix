package soh

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util/common"
	"phenix/util/mm"

	"github.com/fatih/color"
	"github.com/mitchellh/mapstructure"
)

var stringSpacePattern = regexp.MustCompile(`\s+`)

func (this *SOH) deployCapture(exp *types.Experiment, dryrun bool) error {
	if err := this.decodeMetadata(exp); err != nil {
		return err
	}

	if len(this.md.PacketCapture.CaptureHosts) == 0 {
		return nil
	}

	currentIP, mask, _ := net.ParseCIDR(this.md.PacketCapture.ElasticServer.IPAddress)
	cidr, _ := mask.Mask.Size()
	svrAddr := currentIP.String()

	var (
		caps     []ifaces.NodeSpec
		sched    = make(map[string]string)
		monitors = make(map[string][]string)
	)

	for nodeToMonitor := range this.md.PacketCapture.CaptureHosts {
		node := exp.Spec.Topology().FindNodeByName(nodeToMonitor)

		if node == nil {
			// TODO: yell loudly
			continue
		}

		currentIP = nextIP(currentIP)

		cap, mon, err := this.buildPacketBeatNode(exp, node, svrAddr, currentIP.String(), cidr)
		if err != nil {
			return fmt.Errorf("building PacketBeat node: %w", err)
		}

		caps = append(caps, cap)

		sched[cap.General().Hostname()] = exp.Status.Schedules()[nodeToMonitor]
		monitors[cap.General().Hostname()] = mon
	}

	spec := map[string]interface{}{
		"experimentName": exp.Spec.ExperimentName(),
		"topology": map[string]interface{}{
			"nodes": caps,
		},
		"schedules": sched,
	}

	expMonitor, _ := version.GetStoredSpecForKind("Experiment")

	if err := mapstructure.Decode(spec, &expMonitor); err != nil {
		return fmt.Errorf("decoding experiment spec for monitor nodes: %w", err)
	}

	data := struct {
		Exp ifaces.ExperimentSpec
		Mon map[string][]string
	}{
		Exp: expMonitor.(ifaces.ExperimentSpec),
		Mon: monitors,
	}

	filename := fmt.Sprintf("%s/mm_files/%s-monitor.mm", exp.Spec.BaseDir(), exp.Spec.ExperimentName())

	if err := tmpl.CreateFileFromTemplate("packet_capture_script.tmpl", data, filename); err != nil {
		return fmt.Errorf("generating packet capture script: %w", err)
	}

	if !dryrun {
		if err := mm.ReadScriptFromFile(filename); err != nil {
			return fmt.Errorf("reading packet capture script: %w", err)
		}
	}

	return nil
}

func (this *SOH) buildElasticServerNode(exp *types.Experiment, ip string, cidr int) (ifaces.NodeSpec, error) {
	var (
		name       = this.md.PacketCapture.ElasticServer.Hostname
		cpu        = this.md.PacketCapture.ElasticServer.VCPU
		mem        = this.md.PacketCapture.ElasticServer.Memory
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
	node.Hardware().AddDrive(this.md.PacketCapture.ElasticImage, 1)
	node.AddInject(elasticConfigFile, "/etc/elasticsearch/elasticsearch.yml", "", "")
	node.AddInject(kibanaConfigFile, "/etc/kibana/kibana.yml", "", "")

	iface := node.AddNetworkInterface("ethernet", "IF0", "MGMT")
	iface.SetAddress(ip)
	iface.SetMask(cidr)
	iface.SetProto("static")
	iface.SetBridge("phenix")

	data := struct {
		Hostname       string
		ExperimentName string
	}{
		Hostname:       name,
		ExperimentName: exp.Spec.ExperimentName(),
	}

	if err := tmpl.CreateFileFromTemplate("elasticsearch.yml.tmpl", data, elasticConfigFile); err != nil {
		return nil, fmt.Errorf("generating elasticsearch config: %w", err)
	}

	if err := tmpl.CreateFileFromTemplate("kibana.yml.tmpl", name, kibanaConfigFile); err != nil {
		return nil, fmt.Errorf("generating kibana config: %w", err)
	}

	return node, nil
}

func (this *SOH) buildPacketBeatNode(exp *types.Experiment, target ifaces.NodeSpec, es, ip string, cidr int) (ifaces.NodeSpec, []string, error) {
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

	nets := []map[string]interface{}{
		{
			"name":    "IF0",
			"type":    "ethernet",
			"vlan":    "MGMT",
			"address": ip,
			"mask":    cidr,
			"proto":   "static",
			"bridge":  "phenix",
		},
	}

	for i, ifaceToMonitor := range this.md.PacketCapture.CaptureHosts[monitored] {
		for j, iface := range target.Network().Interfaces() {
			if iface.Name() == ifaceToMonitor {
				monitorIface := map[string]interface{}{
					"name":   fmt.Sprintf("MONITOR%d", i),
					"type":   "ethernet",
					"vlan":   iface.VLAN(),
					"proto":  "static",
					"bridge": "phenix",
				}

				nets = append(nets, monitorIface)

				monitors = append(monitors, fmt.Sprintf("%s %d", monitored, j))

				break
			}
		}
	}

	spec := map[string]interface{}{
		"labels": map[string]string{"soh-monitor-node": "true"},
		"type":   "VirtualMachine",
		"general": map[string]interface{}{
			"hostname": name,
			"vm_type":  "kvm",
		},
		"hardware": map[string]interface{}{
			"vcpus":  1,
			"memory": 512,
			"drives": []map[string]interface{}{
				{
					"image": this.md.PacketCapture.PacketBeatImage,
				},
			},
			"os_type": "linux",
		},
		"injections": []map[string]interface{}{
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
		"network": map[string]interface{}{
			"interfaces": nets,
		},
	}

	node, _ := version.GetStoredSpecForKind("Node")

	if err := mapstructure.Decode(spec, &node); err != nil {
		return nil, nil, fmt.Errorf("decoding node spec for Elastic server: %w", err)
	}

	if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", name, hostnameFile); err != nil {
		return nil, nil, fmt.Errorf("generating linux hostname config: %w", err)
	}

	if err := tmpl.CreateFileFromTemplate("linux_timezone.tmpl", tz, timezoneFile); err != nil {
		return nil, nil, fmt.Errorf("generating linux timezone config: %w", err)
	}

	if err := tmpl.CreateFileFromTemplate("linux_interfaces.tmpl", node, ifaceFile); err != nil {
		return nil, nil, fmt.Errorf("generating linux interfaces config: %w", err)
	}

	data := struct {
		ElasticServer string
		Hostname      string
	}{
		ElasticServer: es,
		Hostname:      name,
	}

	if err := tmpl.CreateFileFromTemplate("packetbeat.yml.tmpl", data, packetBeatConfigFile); err != nil {
		return nil, nil, fmt.Errorf("generating packetbeat config: %w", err)
	}

	return node.(ifaces.NodeSpec), monitors, nil
}

func (this *SOH) decodeMetadata(exp *types.Experiment) error {
	var ms map[string]interface{}

	for _, app := range exp.Spec.Scenario().Apps() {
		if app.Name() == "soh" {
			ms = app.Metadata()
		}
	}

	if ms == nil {
		return fmt.Errorf("soh app must have metadata defined")
	}

	if err := mapstructure.Decode(ms, &this.md); err != nil {
		return fmt.Errorf("decoding app metadata: %w", err)
	}

	if err := this.md.init(); err != nil {
		return fmt.Errorf("initializing app metadata: %w", err)
	}

	return nil
}

func (this *SOH) waitForReachabilityTest(ctx context.Context, ns string) {
	if this.md.SkipNetworkConfig {
		return
	}

	var (
		icmpDisabled   bool
		customDisabled bool
	)

	if icmpDisabled = strings.EqualFold(this.md.Reachability, "off"); icmpDisabled {
		printer := color.New(color.FgYellow)
		printer.Println("  ICMP reachability test is disabled")
	}

	if customDisabled = len(this.md.CustomReachability) == 0; customDisabled {
		printer := color.New(color.FgYellow)
		printer.Println("  No custom reachability tests configured")
	}

	if icmpDisabled && customDisabled {
		return
	}

	printer := color.New(color.FgBlue)

	printer.Printf("  Reachability test set to %s mode\n", this.md.Reachability)

	wg := new(mm.StateGroup)

	for host := range this.reachabilityHosts {
		// Assume we're not skipping this host by default.
		var skipHost error

		if _, ok := this.c2Hosts[host]; !ok {
			// This host is known to not have C2 active, so don't test from it.
			skipHost = fmt.Errorf("C2 not active on host")
		}

		if _, ok := this.failedNetwork[host]; ok {
			// This host failed the network config test, so don't test from it.
			skipHost = fmt.Errorf("networking not configured on host")
		}

		for _, ips := range this.vlans {
			// Each host should try to ping a single random host in each VLAN.
			if strings.EqualFold(this.md.Reachability, "sample") {
				var targeted bool

				// Range over IPs to prevent this for-loop from going on forever if
				// all IPs in VLAN failed network connectivity test.
				for range ips {
					idx := rand.Intn(len(ips))
					targetIP := ips[idx]

					targetHost := this.addrHosts[targetIP]

					if _, ok := this.failedNetwork[targetHost]; ok {
						continue
					}

					targeted = true

					if skipHost != nil {
						wg.AddError(skipHost, map[string]interface{}{"host": host, "target": targetIP})
					} else {
						printer.Printf("  Pinging %s (%s) from host %s\n", targetHost, targetIP, host)
						this.pingTest(ctx, wg, ns, this.nodes[host], targetIP)
					}

					break
				}

				if !targeted {
					// Choose random host in VLAN to create error for.
					idx := rand.Intn(len(ips))
					targetIP := ips[idx]

					// This target host failed the network config test, so don't try
					// to do any reachability to it.
					var (
						err  = fmt.Errorf("networking not configured on target")
						meta = map[string]interface{}{"host": host, "target": targetIP}
					)

					wg.AddError(err, meta)
				}
			}

			// Each host should try to ping every host in each VLAN.
			if strings.EqualFold(this.md.Reachability, "full") {
				for _, targetIP := range ips {
					targetHost := this.addrHosts[targetIP]

					if _, ok := this.failedNetwork[targetHost]; ok {
						// This target host failed the network config test, so don't try
						// to do any reachability to it.
						var (
							err  = fmt.Errorf("networking not configured on target")
							meta = map[string]interface{}{"host": host, "target": targetIP}
						)

						wg.AddError(err, meta)
						continue
					}

					if skipHost != nil {
						wg.AddError(skipHost, map[string]interface{}{"host": host, "target": targetIP})
					} else {
						printer.Printf("  Pinging %s from host %s\n", targetIP, host)
						this.pingTest(ctx, wg, ns, this.nodes[host], targetIP)
					}
				}
			}
		}
	}

	for _, reach := range this.md.CustomReachability {
		host := reach.Src

		if _, ok := this.c2Hosts[host]; !ok {
			// This host is known to not have C2 active, so don't test from it.
			wg.AddError(fmt.Errorf("C2 not active on host"), map[string]interface{}{"host": host, "target": reach.Dst})
			continue
		}

		if _, ok := this.failedNetwork[host]; ok {
			// This host failed the network config test, so don't test from it.
			wg.AddError(fmt.Errorf("networking not configured on host"), map[string]interface{}{"host": host, "target": reach.Dst})
			continue
		}

		target := reach.Dst

		if fields := strings.Split(reach.Dst, "|"); len(fields) > 1 {
			target = this.hostIPs[fields[0]][fields[1]]
		}

		printer.Printf("  Connecting to %s://%s:%d from host %s\n", reach.Proto, target, reach.Port, host)

		wait, err := time.ParseDuration(reach.Wait)
		if err != nil && reach.Wait != "" {
			printer.Printf("    invalid wait time of %s provided, using default\n", reach.Wait)
		}

		connTest(ctx, wg, ns, host, target, reach.Proto, reach.Port, wait, reach.Packet)
	}

	cancel := periodicallyNotify(ctx, "waiting for reachability tests to complete...", 5*time.Second)

	// Wait for hosts to test reachability to other hosts.
	wg.Wait()
	cancel()

	printer = color.New(color.FgRed)

	for _, state := range wg.States {
		var (
			host   = state.Meta["host"].(string)
			target = state.Meta["target"].(string)
		)

		s := State{
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		// Convert target IP to hostname.
		hostname := this.addrHosts[target]
		s.Metadata["hostname"] = hostname

		if err := state.Err; err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(this.c2Hosts, host)
			}

			s.Error = err.Error()

			if _, ok := state.Meta["port"]; ok {
				var (
					port  = state.Meta["port"].(int)
					proto = state.Meta["proto"].(string)
				)

				printer.Printf("  [✗] failed to connect to %s://%s:%d from %s\n", proto, target, port, host)
			} else {
				printer.Printf("  [✗] failed to ping %s (%s) from %s\n", hostname, target, host)
			}
		} else {
			s.Success = state.Msg
		}

		state, ok := this.status[host]
		if !ok {
			state = HostState{Hostname: host}
		}

		state.Reachability = append(state.Reachability, s)
		this.status[host] = state
	}
}

func (this *SOH) waitForProcTest(ctx context.Context, ns string) {
	wg := new(mm.StateGroup)
	printer := color.New(color.FgBlue)

	for host, processes := range this.md.HostProcesses {
		// If the host isn't in the C2 hosts map, then don't operate on it since it
		// was likely skipped for a reason.
		if _, ok := this.c2Hosts[host]; !ok {
			printer.Printf("  Skipping host %s per config\n", host)
			continue
		}

		for _, proc := range processes {
			printer.Printf("  Checking for process %s on host %s\n", proc, host)
			this.procTest(ctx, wg, ns, this.nodes[host], proc)
		}
	}

	// Check to see if any of the apps have hosts with metadata that include an SoH profile.
	for _, app := range this.apps {
		for _, host := range app.Hosts() {
			if ms, ok := host.Metadata()[this.md.AppProfileKey]; ok {
				if _, ok := this.c2Hosts[host.Hostname()]; !ok {
					printer.Printf("  Skipping host %s per config\n", host.Hostname())
					continue
				}

				var profile sohProfile

				if err := mapstructure.Decode(ms, &profile); err != nil {
					printer.Printf("incorrect SoH profile for host %s in app %s", host.Hostname(), app.Name())
					continue
				}

				for _, proc := range profile.Processes {
					printer.Printf("  Checking for process %s on host %s\n", proc, host.Hostname())
					this.procTest(ctx, wg, ns, this.nodes[host.Hostname()], proc)
				}
			}
		}
	}

	cancel := periodicallyNotify(ctx, "waiting for process tests to complete...", 5*time.Second)

	wg.Wait()
	cancel()

	printer = color.New(color.FgRed)

	for _, state := range wg.States {
		var (
			host = state.Meta["host"].(string)
			proc = state.Meta["proc"].(string)
		)

		s := State{
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		if err := state.Err; err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(this.c2Hosts, host)
			}

			s.Error = err.Error()

			printer.Printf("  [✗] process %s not running on host %s\n", proc, host)
		} else {
			s.Success = state.Msg
		}

		state, ok := this.status[host]
		if !ok {
			state = HostState{Hostname: host}
		}

		state.Processes = append(state.Processes, s)
		this.status[host] = state
	}
}

func (this *SOH) waitForPortTest(ctx context.Context, ns string) {
	wg := new(mm.StateGroup)
	printer := color.New(color.FgBlue)

	for host, listeners := range this.md.HostListeners {
		// If the host isn't in the C2 hosts map, then don't operate on it since it
		// was likely skipped for a reason.
		if _, ok := this.c2Hosts[host]; !ok {
			printer.Printf("  Skipping host %s per config\n", host)
			continue
		}

		for _, port := range listeners {
			printer.Printf("  Checking for listener %s on host %s\n", port, host)
			this.portTest(ctx, wg, ns, this.nodes[host], port)
		}
	}

	// Check to see if any of the apps have hosts with metadata that include an SoH profile.
	for _, app := range this.apps {
		for _, host := range app.Hosts() {
			if ms, ok := host.Metadata()[this.md.AppProfileKey]; ok {
				if _, ok := this.c2Hosts[host.Hostname()]; !ok {
					printer.Printf("  Skipping host %s per config\n", host.Hostname())
					continue
				}

				var profile sohProfile

				if err := mapstructure.Decode(ms, &profile); err != nil {
					printer.Printf("incorrect SoH profile for host %s in app %s", host.Hostname(), app.Name())
					continue
				}

				for _, port := range profile.Listeners {
					printer.Printf("  Checking for listener %s on host %s\n", port, host.Hostname())
					this.portTest(ctx, wg, ns, this.nodes[host.Hostname()], port)
				}
			}
		}
	}

	cancel := periodicallyNotify(ctx, "waiting for listener tests to complete...", 5*time.Second)

	wg.Wait()
	cancel()

	printer = color.New(color.FgRed)

	for _, state := range wg.States {
		var (
			host = state.Meta["host"].(string)
			port = state.Meta["port"].(string)
		)

		s := State{
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		if err := state.Err; err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(this.c2Hosts, host)
			}

			s.Error = err.Error()

			printer.Printf("  [✗] not listening on port %s on host %s\n", port, host)
		} else {
			s.Success = state.Msg
		}

		state, ok := this.status[host]
		if !ok {
			state = HostState{Hostname: host}
		}

		state.Listeners = append(state.Listeners, s)
		this.status[host] = state
	}
}

func (this *SOH) waitForCustomTest(ctx context.Context, ns string) {
	wg := new(mm.StateGroup)
	printer := color.New(color.FgBlue)

	for host, tests := range this.md.CustomHostTests {
		// If the host isn't in the C2 hosts map, then don't operate on it since it
		// was likely skipped for a reason.
		if _, ok := this.c2Hosts[host]; !ok {
			printer.Printf("  Skipping host %s per config\n", host)
			continue
		}

		for _, test := range tests {
			printer.Printf("  Running custom test %s on host %s\n", test.Name, host)
			customTest(ctx, wg, ns, this.nodes[host], test)
		}
	}

	// Check to see if any of the apps have hosts with metadata that include an SoH profile.
	for _, app := range this.apps {
		for _, host := range app.Hosts() {
			if ms, ok := host.Metadata()[this.md.AppProfileKey]; ok {
				if _, ok := this.c2Hosts[host.Hostname()]; !ok {
					printer.Printf("  Skipping host %s per config\n", host.Hostname())
					continue
				}

				var profile sohProfile

				if err := mapstructure.Decode(ms, &profile); err != nil {
					printer.Printf("incorrect SoH profile for host %s in app %s", host.Hostname(), app.Name())
					continue
				}

				for _, test := range profile.CustomTests {
					printer.Printf("  Running custom test %s on host %s\n", test.Name, host.Hostname())
					customTest(ctx, wg, ns, this.nodes[host.Hostname()], test)
				}
			}
		}
	}

	cancel := periodicallyNotify(ctx, "waiting for custom tests to complete...", 5*time.Second)

	wg.Wait()
	cancel()

	printer = color.New(color.FgRed)

	for _, state := range wg.States {
		var (
			host = state.Meta["host"].(string)
			test = state.Meta["test"].(string)
		)

		s := State{
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		if err := state.Err; err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(this.c2Hosts, host)
			}

			s.Error = err.Error()

			printer.Printf("  [✗] test %s failed on host %s\n", test, host)
		} else {
			s.Success = state.Msg
		}

		state, ok := this.status[host]
		if !ok {
			state = HostState{Hostname: host}
		}

		state.CustomTests = append(state.CustomTests, s)
		this.status[host] = state
	}
}

func (this *SOH) waitForCPULoad(ctx context.Context, ns string) {
	printer := color.New(color.FgBlue)
	printer.Println("  Querying nodes for CPU load")

	wg := new(mm.StateGroup)

	// Only check for CPU load in hosts that have confirmed C2 availability.
	for host := range this.c2Hosts {
		wg.Add(1)

		go func(host string) {
			defer wg.Done()

			node := this.nodes[host]
			exec := `cat /proc/loadavg`

			if strings.EqualFold(node.Hardware().OSType(), "windows") {
				exec = `powershell -command "Get-WmiObject Win32_Processor | Measure-Object -Property LoadPercentage -Average | Select -ExpandProperty Average"`
			}

			opts := []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec)}

			if this.md.useUUIDForC2Active(host) {
				opts = append(opts, mm.C2IDClientsByUUID())
			}

			id, err := mm.ExecC2Command(opts...)
			if err != nil {
				wg.AddError(fmt.Errorf("executing command '%s': %w", exec, err), map[string]interface{}{"host": host})
				return
			}

			opts = []mm.C2Option{mm.C2NS(ns), mm.C2Context(ctx), mm.C2CommandID(id)}

			resp, err := mm.WaitForC2Response(opts...)
			if err != nil {
				wg.AddError(fmt.Errorf("getting response for command '%s': %w", exec, err), map[string]interface{}{"host": host})
				return
			}

			state, ok := this.status[host]
			if !ok {
				state = HostState{Hostname: host}
			}

			if strings.EqualFold(node.Hardware().OSType(), "windows") {
				if resp == "" {
					wg.AddError(fmt.Errorf("no response for command '%s'", exec), map[string]interface{}{"host": host})
					return
				}

				state.CPULoad = resp
			} else {
				parts := strings.Fields(resp)

				if len(parts) != 5 {
					wg.AddError(fmt.Errorf("invalid response for command '%s': %s", exec, resp), map[string]interface{}{"host": host})
					return
				}

				state.CPULoad = parts[0]
			}

			this.status[host] = state
		}(host)
	}

	cancel := periodicallyNotify(ctx, "waiting for CPU load details...", 5*time.Second)

	// Wait for CPU load requests to complete.
	wg.Wait()
	cancel()

	printer = color.New(color.FgRed)

	for _, state := range wg.States {
		host := state.Meta["host"].(string)

		if err := state.Err; err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(this.c2Hosts, host)
			}

			state, ok := this.status[host]
			if !ok {
				state = HostState{Hostname: host}
			}

			state.CPULoad = err.Error()
			this.status[host] = state

			printer.Printf("  [✗] failed to get CPU load from %s: %v\n", host, err)
		}
	}
}

func (this SOH) isNetworkingConfigured(ctx context.Context, wg *mm.StateGroup, ns string, node ifaces.NodeSpec, iface ifaces.NodeNetworkInterface) {
	retryUntil := time.Now().Add(5 * time.Minute)

	var (
		host    = node.General().Hostname()
		gateway = iface.Gateway()
		meta    = map[string]interface{}{"host": host}
	)

	// First, we wait for the IP address to be set on the interface. Then, we wait
	// for the default gateway to be set. Last, we wait for the default gateway to
	// be up (pingable). This is all done via nested commands streamed to the C2
	// processor within `expected` functions.
	ipExpected := func(resp string) error {
		switch strings.ToLower(node.Hardware().OSType()) {
		case "linux", "rhel", "centos":
			cidr := fmt.Sprintf("%s/%d", iface.Address(), iface.Mask())

			// If `resp` doesn't contain the IP address, then the IP address isn't
			// configured yet, so keep retrying the C2 command.
			if !strings.Contains(resp, cidr) {
				if time.Now().After(retryUntil) {
					return fmt.Errorf("retry time expired waiting for IP to be set")
				}

				return mm.C2RetryError{Delay: 5 * time.Second}
			}
		case "windows":
			// If `resp` doesn't contain the IP address, then the IP address isn't
			// configured yet, so keep retrying the C2 command.
			if !strings.Contains(resp, iface.Address()) {
				if time.Now().After(retryUntil) {
					return fmt.Errorf("retry time expired waiting for IP to be set")
				}

				return mm.C2RetryError{Delay: 5 * time.Second}
			}
		default:
			return fmt.Errorf("unknown OS type %s when checking interface IP", node.Hardware().OSType())
		}

		wg.AddSuccess(fmt.Sprintf("IP %s configured", iface.Address()), meta)

		if gateway != "" {
			// The IP address is now set, so schedule a C2 command for determining if
			// the default gateway is set.
			gwExpected := func(resp string) error {
				switch strings.ToLower(node.Hardware().OSType()) {
				case "linux", "rhel", "centos":
					expected := fmt.Sprintf("default via %s", gateway)

					// If `resp` doesn't contain the default gateway, then the default gateway
					// isn't configured yet, so keep retrying the C2 command.
					if !strings.Contains(resp, expected) {
						if time.Now().After(retryUntil) {
							return fmt.Errorf("retry time expired waiting for gateway to be set")
						}

						return mm.C2RetryError{Delay: 5 * time.Second}
					}
				case "windows":
					expected := fmt.Sprintf(`0.0.0.0\s+0.0.0.0\s+%s`, gateway)

					// If `resp` doesn't contain the default gateway, then the default gateway
					// isn't configured yet, so keep retrying the C2 command.
					if found, _ := regexp.MatchString(expected, resp); !found {
						if time.Now().After(retryUntil) {
							return fmt.Errorf("retry time expired waiting for gateway to be set")
						}

						return mm.C2RetryError{Delay: 5 * time.Second}
					}
				default:
					return fmt.Errorf("unknown OS type %s when checking default route", node.Hardware().OSType())
				}

				wg.AddSuccess(fmt.Sprintf("gateway %s configured", gateway), meta)

				// The default gateway is now set, so schedule a C2 command for
				// determining if the default gateway is up (pingable).
				gwPingExpected := func(resp string) error {
					switch strings.ToLower(node.Hardware().OSType()) {
					case "linux", "rhel", "centos":
						// If `resp` contains `0 received`, the default gateway isn't up
						// (pingable) yet, so keep retrying the C2 command.
						if strings.Contains(resp, "0 received") {
							if time.Now().After(retryUntil) {
								return fmt.Errorf("retry time expired waiting for gateway to be up")
							}

							return mm.C2RetryError{Delay: 5 * time.Second}
						}
					case "windows":
						// If `resp` contains `Destination host unreachable`, the
						// default gateway isn't up (pingable) yet, so keep retrying the C2
						// command.
						if strings.Contains(resp, "Destination host unreachable") {
							if time.Now().After(retryUntil) {
								return fmt.Errorf("retry time expired waiting for gateway to be up")
							}

							return mm.C2RetryError{Delay: 5 * time.Second}
						}
					default:
						return fmt.Errorf("unknown OS type %s waiting for gateway to be up", node.Hardware().OSType())
					}

					wg.AddSuccess(fmt.Sprintf("gateway %s is up", gateway), meta)
					return nil
				}

				exec := fmt.Sprintf("ping -c 1 %s", gateway)

				if strings.EqualFold(node.Hardware().OSType(), "windows") {
					exec = fmt.Sprintf("ping -n 1 %s", gateway)
				}

				cmd := this.newParallelCommand(ns, host, exec)
				cmd.Wait = wg
				cmd.Meta = map[string]interface{}{"host": host}
				cmd.Expected = gwPingExpected

				mm.ScheduleC2ParallelCommand(ctx, cmd)

				return nil
			}

			exec := "ip route"

			if strings.EqualFold(node.Hardware().OSType(), "windows") {
				exec = "route print"
			}

			cmd := this.newParallelCommand(ns, host, exec)
			cmd.Wait = wg
			cmd.Meta = map[string]interface{}{"host": host}
			cmd.Expected = gwExpected

			mm.ScheduleC2ParallelCommand(ctx, cmd)
		}

		return nil
	}

	exec := "ip addr"

	if strings.EqualFold(node.Hardware().OSType(), "windows") {
		exec = "ipconfig /all"
	}

	cmd := this.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = ipExpected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func (this SOH) pingTest(ctx context.Context, wg *mm.StateGroup, ns string, node ifaces.NodeSpec, target string) {
	exec := fmt.Sprintf("ping -c 1 %s", target)

	if strings.EqualFold(node.Hardware().OSType(), "windows") {
		exec = fmt.Sprintf("ping -n 1 %s", target)
	}

	var (
		host = node.General().Hostname()
		meta = map[string]interface{}{"host": host, "target": target}
	)

	expected := func(resp string) error {
		switch strings.ToLower(node.Hardware().OSType()) {
		case "linux", "rhel", "centos":
			// If `resp` contains `0 received`, the default gateway isn't up
			// (pingable) yet, so keep retrying the C2 command.
			if strings.Contains(resp, "0 received") {
				return fmt.Errorf("no successful pings")
			}
		case "windows":
			// If `resp` contains `Destination host unreachable`, the
			// default gateway isn't up (pingable) yet, so keep retrying the C2
			// command.
			if strings.Contains(resp, "Destination host unreachable") {
				return fmt.Errorf("no successful pings")
			}
		default:
			return fmt.Errorf("unknown OS type %s waiting for gateway to be up", node.Hardware().OSType())
		}

		wg.AddSuccess(fmt.Sprintf("pinging %s succeeded", target), meta)
		return nil
	}

	cmd := this.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func connTest(ctx context.Context, wg *mm.StateGroup, ns, src, dst, proto string, port int, wait time.Duration, packet string) {
	test := fmt.Sprintf("%s %s %d wait", proto, dst, port)

	if wait == 0 {
		test = fmt.Sprintf("%s %v", test, 5*time.Second)
	} else {
		test = fmt.Sprintf("%s %v", test, wait)
	}

	if proto == "udp" && packet != "" {
		test = fmt.Sprintf("%s %s", test, packet)
	}

	meta := map[string]interface{}{"host": src, "target": dst, "port": port, "proto": proto}

	cmd := &mm.C2ParallelCommand{
		Wait:    wg,
		Options: []mm.C2Option{mm.C2NS(ns), mm.C2VM(src), mm.C2TestConn(test)},
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

func (this SOH) procTest(ctx context.Context, wg *mm.StateGroup, ns string, node ifaces.NodeSpec, proc string) {
	exec := fmt.Sprintf("pgrep -f %s", proc)

	if strings.EqualFold(node.Hardware().OSType(), "windows") {
		exec = fmt.Sprintf(`powershell -command "Get-Process %s -ErrorAction SilentlyContinue"`, proc)
	}

	var (
		host = node.General().Hostname()
		meta = map[string]interface{}{"host": host, "proc": proc}
	)

	retries := 5
	expected := func(resp string) error {
		if resp == "" {
			if retries > 0 {
				retries--
				return mm.C2RetryError{Delay: 5 * time.Second}
			}

			return fmt.Errorf("process not running")
		}

		wg.AddSuccess("process running", meta)
		return nil
	}

	cmd := this.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func (this SOH) portTest(ctx context.Context, wg *mm.StateGroup, ns string, node ifaces.NodeSpec, port string) {
	exec := fmt.Sprintf("ss -lntu state all 'sport = %s'", port)

	if strings.EqualFold(node.Hardware().OSType(), "windows") {
		exec = fmt.Sprintf(`powershell -command "netstat -an | select-string -pattern 'listening' | select-string -pattern '%s'"`, port)
	}

	var (
		host = node.General().Hostname()
		meta = map[string]interface{}{"host": host, "port": port}
	)

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
				return mm.C2RetryError{Delay: 5 * time.Second}
			}

			return fmt.Errorf("not listening on port")
		}

		wg.AddSuccess("listening on port", meta)
		return nil
	}

	cmd := this.newParallelCommand(ns, host, exec)
	cmd.Wait = wg
	cmd.Meta = meta
	cmd.Expected = expected

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func (this SOH) newParallelCommand(ns, host, exec string) *mm.C2ParallelCommand {
	opts := []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec), mm.C2Timeout(this.md.c2Timeout)}

	if this.md.useUUIDForC2Active(host) {
		opts = append(opts, mm.C2IDClientsByUUID())
	}

	return &mm.C2ParallelCommand{Options: opts}
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
				return fmt.Errorf("unable to prepend rule to %s - no ID available", node.General().Hostname())
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

func removeICMPAllowRules(nodes []ifaces.NodeSpec) error {
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

	return nil
}

func customTest(ctx context.Context, wg *mm.StateGroup, ns string, node ifaces.NodeSpec, test customHostTest) {
	host := node.General().Hostname()
	meta := map[string]interface{}{"host": host, "test": test.Name}

	if test.TestScript == "" {
		wg.AddError(fmt.Errorf("no test script provided"), meta)
		return
	}

	if test.TestStdout == "" && test.TestStderr == "" && test.ValidateStdout == "" && test.ValidateStderr == "" {
		wg.AddError(fmt.Errorf("no output test strings or validators provided"), meta)
		return
	}

	script := fmt.Sprintf("%s-%s", host, stringSpacePattern.ReplaceAllString(test.Name, "_"))
	path := fmt.Sprintf("%s/images/%s/%s", common.PhenixBase, ns, script)

	if err := os.WriteFile(path, []byte(test.TestScript), 0600); err != nil {
		wg.AddError(fmt.Errorf("unable to write test script to file: %v", err), meta)
		return
	}

	executor := test.Executor
	if executor == "" {
		switch strings.ToLower(node.Hardware().OSType()) {
		case "windows":
			executor = "powershell -NoProfile -ExecutionPolicy bypass -File"
		default:
			executor = "bash"
		}
	}

	command := fmt.Sprintf("%s /tmp/miniccc/files/%s/%s", executor, ns, script)

	cmd := &mm.C2ParallelCommand{
		Wait:    wg,
		Options: []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2SendFile(script), mm.C2Command(command)},
		Meta:    meta,
	}

	if test.TestStdout != "" {
		cmd.ExpectedStdout = func(resp string) error {
			if strings.Contains(resp, test.TestStdout) {
				wg.AddSuccess(fmt.Sprintf("STDOUT contained %s", test.TestStdout), meta)
				return nil
			}

			return fmt.Errorf("script STDOUT did not contain test output")
		}
	}

	if test.TestStderr != "" {
		cmd.ExpectedStderr = func(resp string) error {
			if strings.Contains(resp, test.TestStderr) {
				wg.AddSuccess(fmt.Sprintf("STDERR contained %s", test.TestStderr), meta)
				return nil
			}

			return fmt.Errorf("script STDERR did not contain test output")
		}
	}

	if test.ValidateStdout != "" {
		cmd.ExpectedStdout = func(resp string) error {
			f, err := os.CreateTemp("", "soh-validator-")
			if err != nil {
				return fmt.Errorf("unable to create STDOUT validator script")
			}

			defer os.Remove(f.Name())

			if _, err := f.Write([]byte(test.ValidateStdout)); err != nil {
				return fmt.Errorf("unable to create STDOUT validator script")
			}

			f.Close()

			bash, err := exec.LookPath("bash")
			if err != nil {
				return fmt.Errorf("bash command is not available for STDOUT validation")
			}

			cmd := exec.Command(bash, f.Name())
			cmd.Stdin = strings.NewReader(resp)

			if err := cmd.Run(); err != nil {
				return fmt.Errorf("script STDOUT was not valid")
			}

			wg.AddSuccess("STDOUT validated", meta)
			return nil
		}
	}

	if test.ValidateStderr != "" {
		cmd.ExpectedStderr = func(resp string) error {
			f, err := os.CreateTemp("", "soh-validator-")
			if err != nil {
				return fmt.Errorf("unable to create STDERR validator script")
			}

			defer os.Remove(f.Name())

			if _, err := f.Write([]byte(test.ValidateStderr)); err != nil {
				return fmt.Errorf("unable to create STDERR validator script")
			}

			f.Close()

			bash, err := exec.LookPath("bash")
			if err != nil {
				return fmt.Errorf("bash command is not available for STDERR validation")
			}

			cmd := exec.Command(bash, f.Name())
			cmd.Stdin = strings.NewReader(resp)

			if err := cmd.Run(); err != nil {
				return fmt.Errorf("script STDERR was not valid")
			}

			wg.AddSuccess("STDERR validated", meta)
			return nil
		}
	}

	mm.ScheduleC2ParallelCommand(ctx, cmd)
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

	for _, l := range strings.Split(str, "\n") {
		if l == "" {
			continue
		}

		trimmed = append(trimmed, strings.TrimSpace(l))
	}

	return trimmed
}

func periodicallyNotify(ctx context.Context, msg string, d time.Duration) context.CancelFunc {
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
			case t := <-ticker.C:
				printer := color.New(color.FgYellow)
				printer.Printf("  [%s] %s\n", t.Format(time.RFC3339), msg)
			}
		}
	}()

	return cancel
}

func nextIP(ip net.IP) net.IP {
	i := ip.To4()
	v := uint(i[0])<<24 + uint(i[1])<<16 + uint(i[2])<<8 + uint(i[3])

	v++

	v3 := byte(v & 0xFF)
	v2 := byte((v >> 8) & 0xFF)
	v1 := byte((v >> 16) & 0xFF)
	v0 := byte((v >> 24) & 0xFF)

	return net.IPv4(v0, v1, v2, v3)
}
