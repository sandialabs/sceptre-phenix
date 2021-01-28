package soh

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"phenix/internal/mm"
	"phenix/tmpl"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"

	"github.com/fatih/color"
	"github.com/mitchellh/mapstructure"
)

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
		ifaceFile    = startupDir + "/" + name + "-interfaces"

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
				"dst": "/etc/network/interfaces",
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
	if strings.EqualFold(this.md.Reachability, "off") {
		printer := color.New(color.FgYellow)
		printer.Println("  Reachability test is disabled")
		return
	}

	printer := color.New(color.FgBlue)

	printer.Printf("  Reachability test set to %s mode\n", this.md.Reachability)

	wg := new(mm.ErrGroup)

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
						pingTest(ctx, wg, ns, this.nodes[host], targetIP)
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
						pingTest(ctx, wg, ns, this.nodes[host], targetIP)
					}
				}
			}
		}
	}

	notifier := periodicallyNotify("waiting for reachability tests to complete...", 5*time.Second)

	// Wait for hosts to test reachability to other hosts.
	wg.Wait()
	close(notifier)

	printer = color.New(color.FgRed)

	for _, err := range wg.Errors {
		var (
			host   = err.Meta["host"].(string)
			target = err.Meta["target"].(string)
		)

		if errors.Is(err, mm.ErrC2ClientNotActive) {
			delete(this.c2Hosts, host)
		}

		// Convert target IP to hostname.
		hostname := this.addrHosts[target]

		r := Reachability{
			Hostname:  fmt.Sprintf("%s (%s)", hostname, target),
			Timestamp: time.Now().Format(time.RFC3339),
			Error:     err.Error(),
		}

		state, ok := this.status[host]
		if !ok {
			state = HostState{Hostname: host}
		}

		state.Reachability = append(state.Reachability, r)
		this.status[host] = state

		printer.Printf("  [✗] failed to ping %s (%s) from %s\n", hostname, target, host)
	}
}

func (this *SOH) waitForProcTest(ctx context.Context, ns string) {
	wg := new(mm.ErrGroup)
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
			procTest(ctx, wg, ns, this.nodes[host], proc)
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
					procTest(ctx, wg, ns, this.nodes[host.Hostname()], proc)
				}
			}
		}
	}

	notifier := periodicallyNotify("waiting for process tests to complete...", 5*time.Second)

	wg.Wait()
	close(notifier)

	printer = color.New(color.FgRed)

	for _, err := range wg.Errors {
		var (
			host = err.Meta["host"].(string)
			proc = err.Meta["proc"].(string)
		)

		if errors.Is(err, mm.ErrC2ClientNotActive) {
			delete(this.c2Hosts, host)
		}

		p := Process{
			Process:   proc,
			Timestamp: time.Now().Format(time.RFC3339),
			Error:     err.Error(),
		}

		state, ok := this.status[host]
		if !ok {
			state = HostState{Hostname: host}
		}

		state.Processes = append(state.Processes, p)
		this.status[host] = state

		printer.Printf("  [✗] process %s not running on host %s\n", proc, host)
	}
}

func (this *SOH) waitForPortTest(ctx context.Context, ns string) {
	wg := new(mm.ErrGroup)
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
			portTest(ctx, wg, ns, this.nodes[host], port)
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
					portTest(ctx, wg, ns, this.nodes[host.Hostname()], port)
				}
			}
		}
	}

	notifier := periodicallyNotify("waiting for listener tests to complete...", 5*time.Second)

	wg.Wait()
	close(notifier)

	printer = color.New(color.FgRed)

	for _, err := range wg.Errors {
		var (
			host = err.Meta["host"].(string)
			port = err.Meta["port"].(string)
		)

		if errors.Is(err, mm.ErrC2ClientNotActive) {
			delete(this.c2Hosts, host)
		}

		l := Listener{
			Listener:  port,
			Timestamp: time.Now().Format(time.RFC3339),
			Error:     err.Error(),
		}

		state, ok := this.status[host]
		if !ok {
			state = HostState{Hostname: host}
		}

		state.Listeners = append(state.Listeners, l)
		this.status[host] = state

		printer.Printf("  [✗] not listening on port %s on host %s\n", port, host)
	}
}

func (this *SOH) waitForCPULoad(ctx context.Context, ns string) {
	printer := color.New(color.FgBlue)
	printer.Println("  Querying nodes for CPU load")

	wg := new(mm.ErrGroup)

	// Only check for CPU load in hosts that have confirmed C2 availability.
	for host := range this.c2Hosts {
		wg.Add(1)

		go func(host string) {
			defer wg.Done()

			node := this.nodes[host]
			exec := fmt.Sprintf("cat /proc/loadavg")

			if node.Hardware().OSType() == "windows" {
				exec = fmt.Sprintf(`powershell -command "Get-WmiObject Win32_Processor | Measure-Object -Property LoadPercentage -Average | Select Average"`)
			}

			opts := []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec)}

			id, err := mm.ExecC2Command(opts...)
			if err != nil {
				wg.AddError(fmt.Errorf("executing command '%s': %w", exec, err), map[string]interface{}{"host": host})
				return
			}

			opts = []mm.C2Option{mm.C2NS(ns), mm.C2CommandID(id)}

			resp, err := mm.WaitForC2Response(ctx, opts...)
			if err != nil {
				wg.AddError(fmt.Errorf("getting response for command '%s': %w", exec, err), map[string]interface{}{"host": host})
				return
			}

			// TODO: handle Windows response format

			parts := strings.Fields(resp)

			if len(parts) != 5 {
				wg.AddError(fmt.Errorf("invalid response for command '%s': %s", exec, resp), map[string]interface{}{"host": host})
				return
			}

			state, ok := this.status[host]
			if !ok {
				state = HostState{Hostname: host}
			}

			state.CPULoad = parts[0]
			this.status[host] = state
		}(host)
	}

	notifier := periodicallyNotify("waiting for CPU load details...", 5*time.Second)

	// Wait for CPU load requests to complete.
	wg.Wait()
	close(notifier)

	printer = color.New(color.FgRed)

	for _, err := range wg.Errors {
		host := err.Meta["host"].(string)

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

func isNetworkingConfigured(ctx context.Context, wg *mm.ErrGroup, ns string, node ifaces.NodeSpec, iface ifaces.NodeNetworkInterface) {
	retryUntil := time.Now().Add(5 * time.Minute)

	host := node.General().Hostname()
	gateway := iface.Gateway()

	// First, we wait for the IP address to be set on the interface. Then, we wait
	// for the default gateway to be set. Last, we wait for the default gateway to
	// be up (pingable). This is all done via nested commands streamed to the C2
	// processor within `expected` functions.
	ipExpected := func(resp string) error {
		switch node.Hardware().OSType() {
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

		if gateway != "" {
			// The IP address is now set, so schedule a C2 command for determining if
			// the default gateway is set.
			gwExpected := func(resp string) error {
				switch node.Hardware().OSType() {
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
					expected := fmt.Sprintf(`%s\s+Default`, gateway)

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

				// The default gateway is now set, so schedule a C2 command for
				// determining if the default gateway is up (pingable).
				gwPingExpected := func(resp string) error {
					switch node.Hardware().OSType() {
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

					return nil
				}

				exec := fmt.Sprintf("ping -c 1 %s", gateway)

				if node.Hardware().OSType() == "windows" {
					exec = fmt.Sprintf("ping -n 1 %s", gateway)
				}

				cmd := &mm.C2ParallelCommand{
					Wait:     wg,
					Options:  []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec)},
					Meta:     map[string]interface{}{"host": host},
					Expected: gwPingExpected,
				}

				mm.ScheduleC2ParallelCommand(ctx, cmd)

				return nil
			}

			exec := "ip route"

			if node.Hardware().OSType() == "windows" {
				exec = "route print"
			}

			cmd := &mm.C2ParallelCommand{
				Wait:     wg,
				Options:  []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec)},
				Meta:     map[string]interface{}{"host": host},
				Expected: gwExpected,
			}

			mm.ScheduleC2ParallelCommand(ctx, cmd)
		}

		return nil
	}

	exec := "ip addr"

	if node.Hardware().OSType() == "windows" {
		exec = "ipconfig /all"
	}

	cmd := &mm.C2ParallelCommand{
		Wait:     wg,
		Options:  []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec)},
		Meta:     map[string]interface{}{"host": host},
		Expected: ipExpected,
	}

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func pingTest(ctx context.Context, wg *mm.ErrGroup, ns string, node ifaces.NodeSpec, target string) {
	exec := fmt.Sprintf("ping -c 1 %s", target)

	if node.Hardware().OSType() == "windows" {
		exec = fmt.Sprintf("ping -n 1 %s", target)
	}

	host := node.General().Hostname()

	cmd := &mm.C2ParallelCommand{
		Wait:    wg,
		Options: []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec)},
		Meta:    map[string]interface{}{"host": host, "target": target},
		Expected: func(resp string) error {
			switch node.Hardware().OSType() {
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

			return nil
		},
	}

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func procTest(ctx context.Context, wg *mm.ErrGroup, ns string, node ifaces.NodeSpec, proc string) {
	exec := fmt.Sprintf("pgrep %s", proc)

	if node.Hardware().OSType() == "windows" {
		exec = fmt.Sprintf(`powershell -command "Get-Process %s -ErrorAction SilentlyContinue"`, proc)
	}

	host := node.General().Hostname()
	retries := 5

	cmd := &mm.C2ParallelCommand{
		Wait:    wg,
		Options: []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec)},
		Meta:    map[string]interface{}{"host": host, "proc": proc},
		Expected: func(resp string) error {
			if resp == "" {
				if retries > 0 {
					retries--
					return mm.C2RetryError{Delay: 5 * time.Second}
				}

				return fmt.Errorf("process not running")
			}

			return nil
		},
	}

	mm.ScheduleC2ParallelCommand(ctx, cmd)
}

func portTest(ctx context.Context, wg *mm.ErrGroup, ns string, node ifaces.NodeSpec, port string) {
	exec := fmt.Sprintf("ss -lntu state all 'sport = %s'", port)

	if node.Hardware().OSType() == "windows" {
		exec = fmt.Sprintf(`powershell -command "netstat -an | select-string -pattern 'listening' | select-string -pattern '%s'"`, port)
	}

	host := node.General().Hostname()
	retries := 5

	cmd := &mm.C2ParallelCommand{
		Wait:    wg,
		Options: []mm.C2Option{mm.C2NS(ns), mm.C2VM(host), mm.C2Command(exec)},
		Meta:    map[string]interface{}{"host": host, "port": port},
		Expected: func(resp string) error {
			lineCount := 1

			if node.Hardware().OSType() == "windows" {
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

			return nil
		},
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

func periodicallyNotify(msg string, d time.Duration) chan struct{} {
	var (
		done   = make(chan struct{})
		ticker = time.NewTicker(d)
	)

	go func() {
		for {
			select {
			case <-done:
				ticker.Stop()
				return
			case t := <-ticker.C:
				printer := color.New(color.FgYellow)
				printer.Printf("  [%s] %s\n", t.Format(time.RFC3339), msg)
			}
		}
	}()

	return done
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
