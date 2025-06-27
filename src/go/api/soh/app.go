package soh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"phenix/app"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog"

	"github.com/activeshadow/structs"
	"github.com/olivere/elastic/v7"
)

func init() {
	app.RegisterUserApp("soh", func() app.App { return newSOH() })
}

type SOH struct {
	// App configuration metadata (from scenario config)
	md sohMetadata

	// Track Hostname -> Node mapping
	nodes map[string]ifaces.NodeSpec
	// Track hosts with active C2
	c2Hosts map[string]struct{}
	// Track hosts that should be tested for reachability
	// (ie. hosts that have at least one interface in an experiment VLAN)
	reachabilityHosts map[string]struct{}
	// Track IP -> Hostname mapping
	addrHosts map[string]string
	// Track VLAN -> IPs mapping
	vlans map[string][]string
	// Track hosts that failed network config test
	failedNetwork map[string]struct{}
	// Track host per-interface IPs (can't use node spec for this due to possible use of DHCP)
	hostIPs map[string]map[string]string

	// Track app status for Experiment Config status
	status map[string]HostState

	// Experiment apps to examine hosts for SoH profile data
	apps []ifaces.ScenarioApp

	// Track packet capture flows if deployed
	packetCapture map[string]interface{}

	options app.Options
}

func newSOH() *SOH {
	return &SOH{
		nodes:             make(map[string]ifaces.NodeSpec),
		c2Hosts:           make(map[string]struct{}),
		reachabilityHosts: make(map[string]struct{}),
		addrHosts:         make(map[string]string),
		vlans:             make(map[string][]string),
		failedNetwork:     make(map[string]struct{}),
		hostIPs:           make(map[string]map[string]string),
		status:            make(map[string]HostState),
		packetCapture:     make(map[string]interface{}),
	}
}

func (this *SOH) Init(opts ...app.Option) error {
	this.options = app.NewOptions(opts...)
	return nil
}

func (SOH) Name() string {
	return "soh"
}

func (this *SOH) Configure(ctx context.Context, exp *types.Experiment) error {
	if err := this.decodeMetadata(exp); err != nil {
		return err
	}

	if len(this.md.PacketCapture.CaptureHosts) == 0 {
		for _, server := range exp.Spec.Topology().FindNodesWithLabels("soh-elastic-server") {
			exp.Spec.Topology().RemoveNode(server.General().Hostname())
		}
	} else {
		if servers := exp.Spec.Topology().FindNodesWithLabels("soh-elastic-server"); len(servers) == 0 {
			ip, mask, _ := net.ParseCIDR(this.md.PacketCapture.ElasticServer.IPAddress)
			cidr, _ := mask.Mask.Size()

			if _, err := this.buildElasticServerNode(exp, ip.String(), cidr); err != nil {
				return fmt.Errorf("building Elastic server node: %w", err)
			}

			exp.Spec.Topology().Init(exp.Spec.DefaultBridge())
		}
	}

	if this.md.InjectICMPAllow {
		if err := injectICMPAllowRules(exp.Spec.Topology().Nodes()); err != nil {
			return fmt.Errorf("injecting ICMP allow rules into topology: %w", err)
		}
	} else {
		if err := removeICMPAllowRules(exp.Spec.Topology().Nodes()); err != nil {
			return fmt.Errorf("removing ICMP allow rules from topology: %w", err)
		}
	}

	return nil
}

func (this *SOH) PreStart(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (this *SOH) PostStart(ctx context.Context, exp *types.Experiment) error {
	if err := this.decodeMetadata(exp); err != nil {
		return err
	}

	this.apps = exp.Spec.Scenario().Apps()

	if err := this.deployCapture(exp, this.options.DryRun); err != nil {
		if this.md.ExitOnError {
			return err
		}

		fmt.Printf("Error deploying packet capture: %v\n", err)
	}

	if this.options.DryRun {
		fmt.Printf("skipping SoH checks since this is a dry run")
		return nil
	}

	if err := this.runChecks(ctx, exp); err != nil {
		if this.md.ExitOnError {
			return fmt.Errorf("running initial SoH checks: %w", err)
		}

		fmt.Printf("Error running initial SoH checks: %v\n", err)
	}

	return nil
}

func (this *SOH) Running(ctx context.Context, exp *types.Experiment) error {
	if err := this.decodeMetadata(exp); err != nil {
		return err
	}

	this.apps = exp.Spec.Scenario().Apps()

	return this.runChecks(ctx, exp)
}

func (SOH) Cleanup(ctx context.Context, exp *types.Experiment) error {
	if err := mm.ClearC2Responses(mm.C2NS(exp.Spec.ExperimentName())); err != nil {
		return fmt.Errorf("deleting minimega C2 responses: %w", err)
	}

	return nil
}

func (this *SOH) runChecks(ctx context.Context, exp *types.Experiment) error {
	logger := plog.LoggerFromContext(ctx, plog.TypeSoh)

	logger.Info("starting SOH checks")

	// *** WAIT FOR NODES TO HAVE NETWORKING CONFIGURED *** //

	md := app.GetContextMetadata(ctx)
	ns := exp.Spec.ExperimentName()
	wg := new(mm.StateGroup)

	if val, ok := md["c2Timeout"]; ok {
		if duration, ok := val.(string); ok {
			if timeout, err := time.ParseDuration(duration); err == nil {
				this.md.c2Timeout = timeout
			}
		}
	}

	var checks map[string]bool

	if val, ok := md["checks"]; ok {
		if slice, ok := val.([]string); ok {
			checks = make(map[string]bool)

			for _, check := range slice {
				checks[check] = true
			}
		}
	}

	if checks == nil { // default to all checks
		checks = map[string]bool{
			"network-config":      true,
			"reachability":        true,
			"custom-reachability": true,
			"processes":           true,
			"ports":               true,
			"custom":              true,
			"cpu-load":            true,
			"flows":               true,
		}
	}

	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			// track IP addresses so custom reachability tests still work
			this.gatherNodeIPs(node)
			continue
		}

		if *node.General().DoNotBoot() {
			continue
		}

		host := node.General().Hostname()

		this.nodes[host] = node

		if skip(node, this.md.SkipHosts) {
			logger.Debug("skipping host per config", "host", host)
			continue
		}

		// Assume C2 is working in this host. The host will get removed from this
		// mapping the first time C2 is proven to not be working.
		this.c2Hosts[host] = struct{}{}

		if this.md.SkipNetworkConfig || !checks["network-config"] {
			continue
		}

		for idx, iface := range node.Network().Interfaces() {
			if strings.EqualFold(iface.VLAN(), "MGMT") {
				continue
			}

			if iface.Type() == "serial" {
				continue
			}

			this.reachabilityHosts[host] = struct{}{}

			if iface.Proto() == "dhcp" {
				wg.Add(1)

				go func(idx int, iface ifaces.NodeNetworkInterface) { // using an anonymous function here so we can break out of the inner select statement
					defer wg.Done()

					logger.Debug("waiting for DHCP address", "host", host)

					timer := time.After(this.md.c2Timeout)

					for {
						select {
						case <-ctx.Done():
							return
						case <-timer:
							wg.AddError(fmt.Errorf("time expired waiting for DHCP details from minimega"), map[string]interface{}{"host": host})
							return
						default:
							vms := mm.GetVMInfo(mm.NS(ns), mm.VMName(host))

							if vms == nil {
								wg.AddError(fmt.Errorf("unable to get DHCP details from minimega"), map[string]interface{}{"host": host})
								return
							} else {
								addrs := vms[0].IPv4

								if addrs == nil || addrs[idx] == "" {
									time.Sleep(1 * time.Second)
									continue
								}

								this.addrHosts[addrs[idx]] = host
								this.vlans[iface.VLAN()] = append(this.vlans[iface.VLAN()], addrs[idx])

								ips, ok := this.hostIPs[host]
								if !ok {
									ips = make(map[string]string)
								}

								ips[iface.Name()] = addrs[idx]
								this.hostIPs[host] = ips

								wg.AddSuccess(fmt.Sprintf("IP %s configured via DHCP", addrs[idx]), map[string]interface{}{"host": host})
								return
							}
						}
					}
				}(idx, iface)

				// No need to do any of the following stuff if this interface is
				// configured using DHCP.
				continue
			}

			this.gatherNodeIPs(node)

			cidr := fmt.Sprintf("%s/%d", iface.Address(), iface.Mask())
			logger.Debug("waiting for IP on host to be set", "host", host, "ip", cidr)

			this.isNetworkingConfigured(ctx, wg, ns, node, iface)
		}
	}

	if this.md.SkipNetworkConfig || !checks["network-config"] {
		logger.Info("skipping initial network configuration tests per config")
	}

	cancel := periodicallyNotify(ctx, "waiting for initial network configurations to be validated...", 5*time.Second)

	// Wait for IP address / gateway configuration to be set for each VM, as well
	// as wait for each gateway to be reachable.
	wg.Wait()
	cancel()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	for _, state := range wg.States {
		host := state.Meta["host"].(string)

		s := State{
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		if err := state.Err; err != nil {
			logger.Error("[✗] failed to confirm networking", "host", host, "err", err)

			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(this.c2Hosts, host)
			} else {
				this.failedNetwork[host] = struct{}{}
			}

			s.Error = err.Error()
		} else {
			s.Success = state.Msg
		}

		state, ok := this.status[host]
		if !ok {
			state = HostState{Hostname: host}
		}

		state.Networking = append(state.Networking, s)
		this.status[host] = state
	}

	this.writeResults(exp)

	rand.Seed(time.Now().Unix())

	// *** RUN ACTUAL STATE OF HEALTH CHECKS *** //

	var errs bool

	if checks["network-config"] && (checks["reachability"] || checks["custom-reachability"]) {
		err := this.waitForReachabilityTest(ctx, ns, checks)
		this.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["processes"] {
		err := this.waitForProcTest(ctx, ns)
		this.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["ports"] {
		err := this.waitForPortTest(ctx, ns)
		this.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["custom"] {
		err := this.waitForCustomTest(ctx, ns)
		this.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["cpu-load"] {
		err := this.waitForCPULoad(ctx, ns)
		this.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["flows"] {
		this.getFlows(ctx, exp)
		this.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	this.writeInitialized(exp)

	if errs || wg.ErrCount > 0 {
		return fmt.Errorf("errors encountered in state of health app")
	}

	return nil
}

func (this *SOH) getFlows(ctx context.Context, exp *types.Experiment) {
	node := exp.Spec.Topology().FindNodesWithLabels("soh-elastic-server")

	if len(node) == 0 {
		return
	}

	hostname := node[0].General().Hostname()
	var id string

	for {
		var err error

		opts := []mm.C2Option{mm.C2NS(exp.Metadata.Name), mm.C2VM(hostname), mm.C2Command("query-flows.sh")}

		if this.md.useUUIDForC2Active(hostname) {
			opts = append(opts, mm.C2IDClientsByUUID())
		}

		id, err = mm.ExecC2Command(opts...)
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				time.Sleep(5 * time.Second)
				continue
			}

			fmt.Printf("error executing command 'query-flows.sh': %v\n", err)
			return
		}

		if id != "" {
			break
		}
	}

	opts := []mm.C2Option{mm.C2NS(exp.Metadata.Name), mm.C2Context(ctx), mm.C2CommandID(id)}

	resp, err := mm.WaitForC2Response(opts...)
	if err != nil {
		fmt.Printf("error getting response for command 'query-flows.sh': %v\n", err)
		return
	}

	var result elastic.SearchResult

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		fmt.Printf("error parsing Elasticsearch results: %v\n", err)
		return
	}

	if result.Hits == nil {
		fmt.Println("no flow data found")
		return
	}

	if len(result.Hits.Hits) == 0 {
		fmt.Println("no flow data found")
		return
	}

	raw := make(map[string]map[string]int)

	for _, hit := range result.Hits.Hits {
		var fields flowsStruct

		if err := json.Unmarshal(hit.Source, &fields); err != nil {
			fmt.Printf("unable to parse hit source: %v\n", err)
			return
		}

		var (
			src      = fields.Source.IP
			srcBytes = fields.Source.Bytes
			dst      = fields.Destination.IP
			dstBytes = fields.Destination.Bytes
		)

		v, ok := raw[src]
		if !ok {
			v = make(map[string]int)
		}

		v[dst] += srcBytes
		raw[src] = v

		v, ok = raw[dst]
		if !ok {
			v = make(map[string]int)
		}

		v[src] += dstBytes
		raw[dst] = v
	}

	var hosts []string

	for k := range raw {
		hosts = append(hosts, k)
	}

	flows := make([][]int, len(hosts))

	for i, s := range hosts {
		flows[i] = make([]int, len(hosts))

		for j, d := range hosts {
			flows[i][j] = raw[s][d]
		}
	}

	this.packetCapture["hosts"] = hosts
	this.packetCapture["flows"] = flows
}

func (this *SOH) gatherNodeIPs(node ifaces.NodeSpec) {
	host := node.General().Hostname()

	for _, iface := range node.Network().Interfaces() {
		if iface.Address() == "" {
			continue
		}

		this.addrHosts[iface.Address()] = host

		if iface.VLAN() != "" {
			this.vlans[iface.VLAN()] = append(this.vlans[iface.VLAN()], iface.Address())
		}

		ips, ok := this.hostIPs[host]
		if !ok {
			ips = make(map[string]string)
		}

		ips[iface.Name()] = iface.Address()
		this.hostIPs[host] = ips
	}
}

func (this SOH) writeResults(exp *types.Experiment) {
	// we do this to make sure we don't overwrite the `initialized` status
	status := make(map[string]any)
	exp.Status.ParseAppStatus("soh", &status)

	if len(this.status) > 0 {
		var states []map[string]any

		for _, state := range this.status {
			states = append(states, structs.Map(state))
		}

		status["hosts"] = states
	}

	if len(this.packetCapture) > 0 {
		status["packetCapture"] = this.packetCapture
	}

	exp.Status.SetAppStatus("soh", status)
	exp.WriteToStore(true)
}

func (this SOH) writeInitialized(exp *types.Experiment) {
	// we do this to make sure we don't overwrite the existing app status
	status := make(map[string]any)
	exp.Status.ParseAppStatus("soh", &status)

	status["initialized"] = true

	exp.Status.SetAppStatus("soh", status)
	exp.WriteToStore(true)
}
