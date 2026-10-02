package soh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/activeshadow/structs"
	"github.com/olivere/elastic/v7"

	"phenix/app"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog"
)

func init() { //nolint:gochecknoinits // app registration
	_ = app.RegisterUserApp("soh", func() app.App { return newSOH() })
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
	packetCapture map[string]any

	options app.Options
}

func newSOH() *SOH {
	return &SOH{ //nolint:exhaustruct // partial initialization
		nodes:             make(map[string]ifaces.NodeSpec),
		c2Hosts:           make(map[string]struct{}),
		reachabilityHosts: make(map[string]struct{}),
		addrHosts:         make(map[string]string),
		vlans:             make(map[string][]string),
		failedNetwork:     make(map[string]struct{}),
		hostIPs:           make(map[string]map[string]string),
		status:            make(map[string]HostState),
		packetCapture:     make(map[string]any),
	}
}

func (s *SOH) Init(opts ...app.Option) error {
	s.options = app.NewOptions(opts...)

	return nil
}

func (SOH) Name() string {
	return "soh"
}

func (s *SOH) Configure(ctx context.Context, exp *types.Experiment) error {
	err := s.decodeMetadata(exp)
	if err != nil {
		return err
	}

	if len(s.md.PacketCapture.CaptureHosts) == 0 {
		for _, server := range exp.Spec.Topology().FindNodesWithLabels("soh-elastic-server") {
			exp.Spec.Topology().RemoveNode(server.General().Hostname())
		}
	} else {
		if servers := exp.Spec.Topology().
			FindNodesWithLabels("soh-elastic-server"); len(
			servers,
		) == 0 {
			ip, mask, _ := net.ParseCIDR(s.md.PacketCapture.ElasticServer.IPAddress)
			cidr, _ := mask.Mask.Size()

			if _, err = s.buildElasticServerNode(exp, ip.String(), cidr); err != nil {
				return fmt.Errorf("building Elastic server node: %w", err)
			}

			_ = exp.Spec.Topology().Init(exp.Spec.DefaultBridge())
		}
	}

	if s.md.InjectICMPAllow {
		err = injectICMPAllowRules(exp.Spec.Topology().Nodes())
		if err != nil {
			return fmt.Errorf("injecting ICMP allow rules into topology: %w", err)
		}
	} else {
		removeICMPAllowRules(exp.Spec.Topology().Nodes())
	}

	return nil
}

func (s *SOH) PreStart(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (s *SOH) PostStart(ctx context.Context, exp *types.Experiment) error {
	logger := plog.LoggerFromContext(ctx, plog.TypeSoh)

	err := s.decodeMetadata(exp)
	if err != nil {
		return err
	}

	s.apps = exp.Spec.Scenario().Apps()

	err = s.deployCapture(exp, s.options.DryRun)
	if err != nil {
		if s.md.ExitOnError {
			return err
		}

		logger.Error("Error deploying packet capture", "err", err)
	}

	if s.options.DryRun {
		logger.Info("skipping SoH checks since this is a dry run")

		return nil
	}

	if s.md.startupDelay > 0 {
		logger.Info("Waiting before running SoH checks", "delay", s.md.startupDelay)
		time.Sleep(s.md.startupDelay)
	}

	err = s.runChecks(ctx, exp)
	if err != nil {
		if s.md.ExitOnError {
			return fmt.Errorf("running initial SoH checks: %w", err)
		}

		logger.Error("Error running initial SoH checks", "err", err)
	}

	return nil
}

func (s *SOH) Running(ctx context.Context, exp *types.Experiment) error {
	err := s.decodeMetadata(exp)
	if err != nil {
		return err
	}

	s.apps = exp.Spec.Scenario().Apps()

	return s.runChecks(ctx, exp)
}

func (SOH) Cleanup(ctx context.Context, exp *types.Experiment) error {
	err := mm.ClearC2Responses(mm.C2NS(exp.Spec.ExperimentName()))
	if err != nil {
		return fmt.Errorf("deleting minimega C2 responses: %w", err)
	}

	return nil
}

//nolint:cyclop,funlen,gocyclo,maintidx // complex logic
func (s *SOH) runChecks(ctx context.Context, exp *types.Experiment) error {
	logger := plog.LoggerFromContext(ctx, plog.TypeSoh)

	logger.Info("starting SOH checks")

	started := time.Now()

	// *** WAIT FOR NODES TO HAVE NETWORKING CONFIGURED *** //

	md := app.GetContextMetadata(ctx)
	ns := exp.Spec.ExperimentName()
	wg := new(mm.StateGroup)

	if val, ok := md["c2Timeout"]; ok {
		if duration, ok2 := val.(string); ok2 {
			if timeout, err := time.ParseDuration(duration); err == nil {
				s.md.c2Timeout = timeout
			}
		}
	}

	var checks map[string]bool

	if val, ok := md["checks"]; ok {
		if slice, ok2 := val.([]string); ok2 {
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
			"files":               true,
			"files-absent":        true,
			"services":            true,
			"processes":           true,
			"ports":               true,
			"docker":              true,
			"custom":              true,
			"cpu-load":            true,
			"flows":               true,
		}
	}

	// Guards the IP maps (addrHosts, vlans, hostIPs), which the DHCP waits
	// below fill in while this loop is still gathering static addresses.
	var ipsMu sync.Mutex

	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			// track IP addresses so custom reachability tests still work
			ipsMu.Lock()
			s.gatherNodeIPs(node)
			ipsMu.Unlock()

			continue
		}

		if *node.General().DoNotBoot() {
			continue
		}

		host := node.General().Hostname()

		s.nodes[host] = node

		if skip(node, s.md.SkipHosts) {
			logger.Debug("skipping host per config", hostKey, host)

			continue
		}

		// Assume C2 is working in this host. The host will get removed from this
		// mapping the first time C2 is proven to not be working.
		s.c2Hosts[host] = struct{}{}

		if s.md.SkipNetworkConfig || !checks["network-config"] {
			continue
		}

		var dhcp []dhcpInterface

		for idx, iface := range node.Network().Interfaces() {
			if strings.EqualFold(iface.VLAN(), "MGMT") {
				continue
			}

			if iface.Type() == "serial" {
				continue
			}

			s.reachabilityHosts[host] = struct{}{}

			if iface.Proto() == "dhcp" {
				// Waited on below, with the host's other DHCP interfaces. No need to
				// do any of the following stuff if this interface is configured
				// using DHCP.
				dhcp = append(dhcp, dhcpInterface{idx: idx, iface: iface})

				continue
			}

			ipsMu.Lock()
			s.gatherNodeIPs(node)
			ipsMu.Unlock()

			cidr := fmt.Sprintf("%s/%d", iface.Address(), iface.Mask())
			logger.Debug("waiting for IP on host to be set", hostKey, host, "ip", cidr)

			s.isNetworkingConfigured(ctx, wg, ns, node, iface)
		}

		if len(dhcp) > 0 {
			wg.Add(1)

			go func() {
				defer wg.Done()

				s.waitForDHCP(ctx, wg, &ipsMu, ns, host, dhcp)
			}()
		}
	}

	if s.md.SkipNetworkConfig || !checks["network-config"] {
		logger.Info("skipping initial network configuration tests per config")
	}

	cancel := periodicallyNotify(
		ctx,
		"waiting for initial network configurations to be validated...",
		notifyInterval,
	)

	// Wait for IP address / gateway configuration to be set for each VM, as well
	// as wait for each gateway to be reachable.
	wg.Wait()
	cancel()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	for _, state := range wg.States {
		host, _ := state.Meta[hostKey].(string)

		st := State{ //nolint:exhaustruct // partial initialization
			Metadata:  state.Meta,
			Timestamp: time.Now().Format(time.RFC3339),
		}

		err := state.Err
		if err != nil {
			logger.Error("[✗] failed to confirm networking", hostKey, host, "err", err)

			if errors.Is(err, mm.ErrC2ClientNotActive) {
				delete(s.c2Hosts, host)
			} else {
				s.failedNetwork[host] = struct{}{}
			}

			st.Error = err.Error()
		} else {
			st.Success = state.Msg
		}

		hostState, ok := s.status[host]
		if !ok {
			hostState = HostState{Hostname: host} //nolint:exhaustruct // partial initialization
		}

		hostState.Networking = append(hostState.Networking, st)
		s.status[host] = hostState
	}

	s.writeResults(exp)

	// *** RUN ACTUAL STATE OF HEALTH CHECKS *** //

	var errs bool

	if checks["network-config"] && (checks["reachability"] || checks["custom-reachability"]) {
		err := s.waitForReachabilityTest(ctx, ns, checks)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["files"] {
		err := s.waitForFileTest(ctx, ns)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["files-absent"] {
		err := s.waitForFileAbsentTest(ctx, ns)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["services"] {
		err := s.waitForServiceTest(ctx, ns)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["processes"] {
		err := s.waitForProcTest(ctx, ns)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["ports"] {
		err := s.waitForPortTest(ctx, ns)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["docker"] {
		err := s.waitForContainerTest(ctx, ns)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["custom"] {
		err := s.waitForCustomTest(ctx, ns)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["cpu-load"] {
		err := s.waitForCPULoad(ctx, ns)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		errs = errs || err
	}

	if checks["flows"] {
		s.getFlows(ctx, exp)
		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	s.writeRunFinished(ctx, exp, started)

	if errs || wg.ErrCount > 0 {
		return errors.New("errors encountered in state of health app")
	}

	return nil
}

func (s *SOH) getFlows(ctx context.Context, exp *types.Experiment) { //nolint:funlen // complex logic
	node := exp.Spec.Topology().FindNodesWithLabels("soh-elastic-server")

	if len(node) == 0 {
		return
	}

	hostname := node[0].General().Hostname()

	var id string

	for {
		var err error

		opts := []mm.C2Option{
			mm.C2NS(exp.Metadata.Name),
			mm.C2VM(hostname),
			mm.C2Command("query-flows.sh"),
		}

		if s.md.useUUIDForC2Active(hostname) {
			opts = append(opts, mm.C2IDClientsByUUID())
		}

		id, err = mm.ExecC2Command(opts...)
		if err != nil {
			if errors.Is(err, mm.ErrC2ClientNotActive) {
				time.Sleep(c2RetryDelay)

				continue
			}

			plog.Error(plog.TypeSoh, "error executing command 'query-flows.sh'", "err", err)

			return
		}

		if id != "" {
			break
		}
	}

	opts := []mm.C2Option{mm.C2NS(exp.Metadata.Name), mm.C2Context(ctx), mm.C2CommandID(id)}

	resp, err := mm.WaitForC2Response(opts...)
	if err != nil {
		plog.Error(plog.TypeSoh, "error getting response for command 'query-flows.sh'", "err", err)

		return
	}

	var result elastic.SearchResult

	if err = json.Unmarshal([]byte(resp), &result); err != nil {
		plog.Error(plog.TypeSoh, "error parsing Elasticsearch results", "err", err)

		return
	}

	if result.Hits == nil {
		plog.Info(plog.TypeSoh, "no flow data found")

		return
	}

	if len(result.Hits.Hits) == 0 {
		plog.Info(plog.TypeSoh, "no flow data found")

		return
	}

	raw := make(map[string]map[string]int)

	for _, hit := range result.Hits.Hits {
		var fields flowsStruct

		err = json.Unmarshal(hit.Source, &fields)
		if err != nil {
			plog.Error(plog.TypeSoh, "unable to parse hit source", "err", err)

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

	s.packetCapture["hosts"] = hosts
	s.packetCapture["flows"] = flows
}

// dhcpPollInterval is how often minimega is asked for a VM's DHCP addresses.
var dhcpPollInterval = 1 * time.Second //nolint:gochecknoglobals // shortened by tests

// dhcpInterface is an interface configured using DHCP, and its index among
// the node's interfaces (and so in minimega's list of the VM's IPs).
type dhcpInterface struct {
	idx   int
	iface ifaces.NodeNetworkInterface
}

// waitForDHCP waits for minimega to report an address for each of a host's
// DHCP interfaces, recording each address (under ipsMu) and its outcome in wg.
// One `vm info` a second serves all the host's DHCP interfaces.
func (s *SOH) waitForDHCP(
	ctx context.Context,
	wg *mm.StateGroup,
	ipsMu *sync.Mutex,
	ns, host string,
	pending []dhcpInterface,
) {
	logger := plog.LoggerFromContext(ctx, plog.TypeSoh)

	for range pending {
		logger.Debug("waiting for DHCP address", hostKey, host)
	}

	timer := time.After(s.md.c2Timeout)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer:
			for range pending {
				wg.AddError(
					errors.New("time expired waiting for DHCP details from minimega"),
					map[string]any{hostKey: host},
				)
			}

			return
		default:
		}

		addrs, err := mm.GetVMIPv4(mm.NS(ns), mm.VMName(host))
		if err != nil {
			for range pending {
				wg.AddError(
					errors.New("unable to get DHCP details from minimega"),
					map[string]any{hostKey: host},
				)
			}

			return
		}

		var waiting []dhcpInterface

		for _, d := range pending {
			// minimega may not list an address for every interface yet
			if d.idx >= len(addrs) || addrs[d.idx] == "" {
				waiting = append(waiting, d)

				continue
			}

			addr := addrs[d.idx]

			ipsMu.Lock()

			s.addrHosts[addr] = host
			s.vlans[d.iface.VLAN()] = append(s.vlans[d.iface.VLAN()], addr)

			ips, ok := s.hostIPs[host]
			if !ok {
				ips = make(map[string]string)
			}

			ips[d.iface.Name()] = addr
			s.hostIPs[host] = ips

			ipsMu.Unlock()

			wg.AddSuccess(
				fmt.Sprintf("IP %s configured via DHCP", addr),
				map[string]any{hostKey: host},
			)
		}

		if len(waiting) == 0 {
			return
		}

		pending = waiting

		time.Sleep(dhcpPollInterval)
	}
}

func (s *SOH) gatherNodeIPs(node ifaces.NodeSpec) {
	host := node.General().Hostname()

	for _, iface := range node.Network().Interfaces() {
		if iface.Address() == "" {
			continue
		}

		s.addrHosts[iface.Address()] = host

		if iface.VLAN() != "" {
			s.vlans[iface.VLAN()] = append(s.vlans[iface.VLAN()], iface.Address())
		}

		ips, ok := s.hostIPs[host]
		if !ok {
			ips = make(map[string]string)
		}

		ips[iface.Name()] = iface.Address()
		s.hostIPs[host] = ips
	}
}

func (s SOH) writeResults(exp *types.Experiment) {
	// we do this to make sure we don't overwrite the `initialized` status
	status := make(map[string]any)
	_ = exp.Status.ParseAppStatus("soh", &status)

	if len(s.status) > 0 {
		states := make([]map[string]any, 0, len(s.status))

		for _, state := range s.status {
			states = append(states, structs.Map(state))
		}

		status["hosts"] = states
	}

	if len(s.packetCapture) > 0 {
		status["packetCapture"] = s.packetCapture
	}

	exp.Status.SetAppStatus("soh", status)
	_ = exp.WriteToStore(true)
}

// writeRunFinished marks the app initialized and records the finished run:
// its time and duration, and its summary in the run history.
func (s SOH) writeRunFinished(ctx context.Context, exp *types.Experiment, started time.Time) {
	// we do this to make sure we don't overwrite the existing app status
	status := make(map[string]any)
	_ = exp.Status.ParseAppStatus("soh", &status)

	finished := time.Now()
	run := summarizeRun(s.status, finished, finished.Sub(started))

	status["initialized"] = true
	status[statusLastRun] = run.Time
	status[statusLastRunDuration] = run.Duration

	history, err := appendRunHistory(status[statusHistory], run)
	if err != nil {
		// the run's own results matter more than its predecessors' summaries
		plog.LoggerFromContext(ctx, plog.TypeSoh).
			Warn("replacing unreadable SoH run history", "exp", exp.Metadata.Name, "err", err)

		history, _ = appendRunHistory(nil, run)
	}

	status[statusHistory] = history

	exp.Status.SetAppStatus("soh", status)
	_ = exp.WriteToStore(true)
}
