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
	"phenix/internal/mm"
	"phenix/types"
	ifaces "phenix/types/interfaces"

	"github.com/activeshadow/structs"
	"github.com/fatih/color"
	"github.com/olivere/elastic/v7"
)

func init() {
	app.RegisterUserApp(newSOH())
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

			exp.Spec.Topology().Init()
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

	if err := this.runChecks(ctx, exp, true); err != nil {
		if this.md.ExitOnError {
			return err
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

	return this.runChecks(ctx, exp, false)
}

func (SOH) Cleanup(ctx context.Context, exp *types.Experiment) error {
	if err := mm.ClearC2Responses(mm.C2NS(exp.Spec.ExperimentName())); err != nil {
		return fmt.Errorf("deleting minimega C2 responses: %w", err)
	}

	return nil
}

func (this *SOH) runChecks(ctx context.Context, exp *types.Experiment, initial bool) error {
	printer := color.New(color.FgBlue)

	printer.Println("  Starting SoH checks...")

	// *** WAIT FOR NODES TO HAVE NETWORKING CONFIGURED *** //

	ns := exp.Spec.ExperimentName()
	wg := new(mm.ErrGroup)

	for _, node := range exp.Spec.Topology().Nodes() {
		if !strings.EqualFold(node.Type(), "VirtualMachine") {
			continue
		}

		if *node.General().DoNotBoot() {
			continue
		}

		host := node.General().Hostname()

		this.nodes[host] = node

		if skip(node, this.md.SkipHosts) {
			printer.Printf("  Skipping host %s per config\n", host)
			continue
		}

		// Assume C2 is working in this host. The host will get removed from this
		// mapping the first time C2 is proven to not be working.
		this.c2Hosts[host] = struct{}{}

		for _, iface := range node.Network().Interfaces() {
			if strings.EqualFold(iface.VLAN(), "MGMT") {
				continue
			}

			if iface.Type() == "serial" {
				continue
			}

			this.reachabilityHosts[host] = struct{}{}
			this.addrHosts[iface.Address()] = host
			this.vlans[iface.VLAN()] = append(this.vlans[iface.VLAN()], iface.Address())

			if !this.md.SkipNetworkConfig {
				cidr := fmt.Sprintf("%s/%d", iface.Address(), iface.Mask())

				printer.Printf("  Waiting for IP %s on host %s to be set...\n", cidr, host)

				isNetworkingConfigured(ctx, wg, ns, node, iface)
			}
		}
	}

	if this.md.SkipNetworkConfig {
		printer = color.New(color.FgYellow)
		printer.Println("  Skipping initial network configuration tests per config")
	}

	notifier := periodicallyNotify("waiting for initial network configurations to be validated...", 5*time.Second)

	// Wait for IP address / gateway configuration to be set for each VM, as well
	// as wait for each gateway to be reachable.
	wg.Wait()
	close(notifier)

	printer = color.New(color.FgRed)

	for _, err := range wg.Errors {
		host := err.Meta["host"].(string)

		printer.Printf("  [✗] failed to confirm networking on %s: %v\n", host, err)

		if errors.Is(err, mm.ErrC2ClientNotActive) {
			delete(this.c2Hosts, host)
		} else {
			this.failedNetwork[host] = struct{}{}
		}
	}

	rand.Seed(time.Now().Unix())

	// *** RUN ACTUAL STATE OF HEALTH CHECKS *** //

	this.waitForReachabilityTest(ctx, ns)
	this.waitForProcTest(ctx, ns)
	this.waitForPortTest(ctx, ns)
	this.waitForCPULoad(ctx, ns)

	if !initial {
		this.getFlows(ctx, exp)
	}

	// *** WRITE RESULTS TO EXPERIMENT STATUS *** //

	appStatus := make(map[string]interface{})

	if len(this.status) > 0 {
		var states []map[string]interface{}

		for _, state := range this.status {
			states = append(states, structs.Map(state))
		}

		appStatus["hosts"] = states
	}

	if len(this.packetCapture) > 0 {
		appStatus["packetCapture"] = this.packetCapture
	}

	exp.Status.SetAppStatus("soh", appStatus)

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

	opts := []mm.C2Option{mm.C2NS(exp.Metadata.Name), mm.C2CommandID(id)}

	resp, err := mm.WaitForC2Response(ctx, opts...)
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
