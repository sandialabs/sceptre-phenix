package soh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/activeshadow/structs"
	"github.com/olivere/elastic/v7"

	"phenix/app"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/plog"
)

func init() { //nolint:gochecknoinits // app registration
	_ = app.RegisterUserApp("soh", func() app.App { return newSOH() })
}

// ErrAlreadyRunning is returned when checks are already in progress for the
// experiment.
var ErrAlreadyRunning = errors.New("state of health checks already running")

// errC2Dead marks checks a host could not run because its C2 client had gone.
var errC2Dead = errors.New("skipped: C2 client not active on host")

// running guards against concurrent check runs for one experiment. The
// experiment status flag cannot serve: every trigger writes it from its own
// copy of the experiment.
var running sync.Map //nolint:gochecknoglobals // per-experiment run guard

type SOH struct {
	// App configuration metadata (from scenario config)
	md sohMetadata

	// Track Hostname -> Node mapping
	nodes map[string]ifaces.NodeSpec
	// Track hosts with active C2
	c2Hosts map[string]struct{}
	// Track hosts whose C2 client went away mid-run (as opposed to skipped ones)
	c2Dead map[string]struct{}
	// Track VMs as minimega launched them (name, UUID, host) by hostname
	vms map[string]mm.VM
	// Bounds in-flight C2 probes for this run
	limiter *mm.C2Limiter
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
		c2Dead:            make(map[string]struct{}),
		vms:               make(map[string]mm.VM),
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

		err = util.SleepContext(ctx, s.md.startupDelay)
		if err != nil {
			return err
		}
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
	var (
		logger = plog.LoggerFromContext(ctx, plog.TypeSoh)
		md     = app.GetContextMetadata(ctx)
		ns     = exp.Spec.ExperimentName()
		wg     = new(mm.StateGroup)
	)

	if _, loaded := running.LoadOrStore(ns, struct{}{}); loaded {
		return ErrAlreadyRunning
	}

	defer running.Delete(ns)

	logger.Info("starting SOH checks")

	// Resolve VM identities once; every C2 command would otherwise look them up
	// again.
	for _, vm := range mm.GetVMInfo(mm.NS(ns)) {
		s.vms[vm.Name] = vm
	}

	// *** WAIT FOR NODES TO HAVE NETWORKING CONFIGURED *** //

	if d := metadataDuration(md["c2Timeout"]); d != nil {
		s.md.c2Timeout = *d
	}

	if d := metadataDuration(md["c2AppearGrace"]); d != nil {
		s.md.c2AppearGrace = d
	}

	if d := metadataDuration(md["c2ClientGrace"]); d != nil {
		s.md.c2ClientGrace = d
	}

	if n, err := strconv.Atoi(metadataString(md["c2Concurrency"])); err == nil {
		s.md.c2Concurrency = n
	}

	s.limiter = mm.NewC2Limiter(s.md.c2Concurrency)

	var checks map[string]bool

	if val, ok := md["checks"]; ok {
		if slice := metadataStrings(val); len(slice) > 0 {
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

	if !checks["network-config"] && (checks["reachability"] || checks["custom-reachability"]) {
		logger.Warn("reachability checks depend on network-config and will be skipped")
	}

	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			// track IP addresses so custom reachability tests still work
			s.gatherNodeIPs(node)

			continue
		}

		if *node.General().DoNotBoot() {
			continue
		}

		host := node.General().Hostname()

		s.nodes[host] = node

		if skip(node, s.md.SkipHosts) {
			logger.Debug("skipping host per config", "host", host)

			continue
		}

		// Assume C2 is working in this host. The host will get removed from this
		// mapping the first time C2 is proven to not be working.
		s.c2Hosts[host] = struct{}{}

		if s.md.SkipNetworkConfig || !checks["network-config"] {
			continue
		}

		gathered := false // static addresses are recorded once per node

		for idx, iface := range node.Network().Interfaces() {
			if strings.EqualFold(iface.VLAN(), "MGMT") {
				continue
			}

			if iface.Type() == "serial" {
				continue
			}

			s.reachabilityHosts[host] = struct{}{}

			if iface.Proto() == "dhcp" {
				wg.Add(1)

				go s.waitForDHCP(ctx, wg, ns, host, idx, iface)

				// No need to do any of the following stuff if this interface is
				// configured using DHCP.
				continue
			}

			if !gathered {
				s.gatherNodeIPs(node)
				gathered = true
			}

			cidr := fmt.Sprintf("%s/%d", iface.Address(), iface.Mask())
			logger.Debug("waiting for IP on host to be set", "host", host, "ip", cidr)

			s.isNetworkingConfigured(ctx, wg, ns, node, iface)
		}
	}

	if s.md.SkipNetworkConfig || !checks["network-config"] {
		logger.Info("skipping initial network configuration tests per config")
	}

	// Wait for IP address / gateway configuration to be set for each VM, as well
	// as wait for each gateway to be reachable.
	if waitAll(ctx, wg, "waiting for initial network configurations to be validated...") {
		return ctx.Err()
	}

	for _, state := range wg.States {
		host, _ := state.Meta["host"].(string)

		// DHCP addresses are only known now; the reachability tests need them
		if ip, ok := state.Meta["ip"].(string); ok && state.Err == nil {
			iface, _ := state.Meta["iface"].(string)
			vlan, _ := state.Meta["vlan"].(string)

			s.recordHostIP(host, iface, vlan, ip)
		}

		st := s.newState(state)
		if st.Error != "" {
			logger.Error("[✗] failed to confirm networking", "host", host, "err", state.Err)

			if !errors.Is(state.Err, mm.ErrC2ClientNotActive) {
				s.failedNetwork[host] = struct{}{}
			}
		}

		s.updateHost(host, func(h *HostState) { h.Networking = append(h.Networking, st) })
	}

	s.writeResults(exp)

	// *** RUN ACTUAL STATE OF HEALTH CHECKS *** //

	reachability := checks["network-config"] && (checks["reachability"] || checks["custom-reachability"])

	steps := []struct {
		enabled bool
		run     func() bool
	}{
		{reachability, func() bool { return s.waitForReachabilityTest(ctx, ns, checks) }},
		{checks["files"], func() bool { return s.waitForFileTest(ctx, ns) }},
		{checks["files-absent"], func() bool { return s.waitForFileAbsentTest(ctx, ns) }},
		{checks["services"], func() bool { return s.waitForServiceTest(ctx, ns) }},
		{checks["processes"], func() bool { return s.waitForProcTest(ctx, ns) }},
		{checks["ports"], func() bool { return s.waitForPortTest(ctx, ns) }},
		{checks["docker"], func() bool { return s.waitForContainerTest(ctx, ns) }},
		{checks["custom"], func() bool { return s.waitForCustomTest(ctx, ns) }},
		{checks["cpu-load"], func() bool { return s.waitForCPULoad(ctx, ns) }},
		{checks["flows"], func() bool { s.getFlows(ctx, exp); return false }},
	}

	var errs bool

	for _, step := range steps {
		if !step.enabled {
			continue
		}

		if step.run() {
			errs = true
		}

		s.writeResults(exp)

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	s.writeInitialized(exp)

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

	var (
		logger   = plog.LoggerFromContext(ctx, plog.TypeSoh)
		ns       = exp.Spec.ExperimentName()
		hostname = node[0].General().Hostname()
	)

	opts := append(s.c2Options(ns, hostname), mm.C2Context(ctx), mm.C2Command("query-flows.sh"))

	id, err := mm.ExecC2Command(opts...)
	if err != nil {
		logger.Error("error executing command 'query-flows.sh'", "err", err)

		return
	}

	opts = append(opts, mm.C2CommandID(id))

	resp, err := mm.WaitForC2Response(opts...)
	if err != nil {
		logger.Error("error getting response for command 'query-flows.sh'", "err", err)

		return
	}

	var result elastic.SearchResult

	if err = json.Unmarshal([]byte(resp), &result); err != nil {
		logger.Error("error parsing Elasticsearch results", "err", err)

		return
	}

	if result.Hits == nil {
		logger.Info("no flow data found")

		return
	}

	if len(result.Hits.Hits) == 0 {
		logger.Info("no flow data found")

		return
	}

	raw := make(map[string]map[string]int)

	for _, hit := range result.Hits.Hits {
		var fields flowsStruct

		err = json.Unmarshal(hit.Source, &fields)
		if err != nil {
			logger.Error("unable to parse hit source", "err", err)

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

// waitForDHCP polls minimega until the interface at idx reports an address,
// then hands it back through wg. wg.Add must be done by the caller.
func (s *SOH) waitForDHCP(
	ctx context.Context,
	wg *mm.StateGroup,
	ns, host string,
	idx int,
	iface ifaces.NodeNetworkInterface,
) {
	defer wg.Done()

	var (
		logger = plog.LoggerFromContext(ctx, plog.TypeSoh)
		meta   = map[string]any{"host": host}
		timer  = time.After(s.md.c2Timeout)
	)

	logger.Debug("waiting for DHCP address", "host", host)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer:
			wg.AddError(errors.New("time expired waiting for DHCP details from minimega"), meta)

			return
		default:
			vms := mm.GetVMInfo(mm.NS(ns), mm.VMName(host))
			if len(vms) == 0 {
				wg.AddError(errors.New("unable to get DHCP details from minimega"), meta)

				return
			}

			// minimega lists one address per configured interface, in order
			addrs := vmNamed(vms, host).IPv4

			if idx >= len(addrs) || addrs[idx] == "" {
				time.Sleep(1 * time.Second)

				continue
			}

			wg.AddSuccess(
				fmt.Sprintf("IP %s configured via DHCP", addrs[idx]),
				map[string]any{"host": host, "iface": iface.Name(), "vlan": iface.VLAN(), "ip": addrs[idx]},
			)

			return
		}
	}
}

// vmNamed returns the VM minimega tracks under exactly name, else the first
// match: the `name=` filter folds case.
func vmNamed(vms mm.VMs, name string) mm.VM {
	for _, vm := range vms {
		if vm.Name == name {
			return vm
		}
	}

	return vms[0]
}

func (s *SOH) gatherNodeIPs(node ifaces.NodeSpec) {
	host := node.General().Hostname()

	for _, iface := range node.Network().Interfaces() {
		if iface.Address() == "" {
			continue
		}

		s.recordHostIP(host, iface.Name(), iface.VLAN(), iface.Address())
	}
}

// recordHostIP tracks an address by host, VLAN and interface. Not safe for
// concurrent use: results gathered in goroutines are recorded after they join.
func (s *SOH) recordHostIP(host, iface, vlan, ip string) {
	s.addrHosts[ip] = host

	if vlan != "" {
		s.vlans[vlan] = append(s.vlans[vlan], ip)
	}

	ips, ok := s.hostIPs[host]
	if !ok {
		ips = make(map[string]string)
		s.hostIPs[host] = ips
	}

	ips[iface] = ip
}

// markC2Dead drops a host whose C2 client stopped answering from further
// checks, remembering it so the checks it misses are reported rather than
// silently absent.
func (s *SOH) markC2Dead(host string) {
	delete(s.c2Hosts, host)
	s.c2Dead[host] = struct{}{}
}

// skipHost reports whether host cannot be tested, recording an error state
// for a host whose C2 client went away.
func (s *SOH) skipHost(wg *mm.StateGroup, host string, meta map[string]any) bool {
	if _, ok := s.c2Hosts[host]; ok {
		return false
	}

	if _, dead := s.c2Dead[host]; dead {
		wg.AddError(errC2Dead, meta)
	}

	return true
}

// metadataString returns a context metadata value that arrives as a string
// (scorch, CLI) or as the single-valued slice a URL query yields (UI).
func metadataString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case []string:
		if len(v) > 0 {
			return v[0]
		}
	}

	return ""
}

// metadataDuration parses a context metadata duration, nil when absent or
// malformed.
func metadataDuration(val any) *time.Duration {
	d, err := time.ParseDuration(metadataString(val))
	if err != nil {
		return nil
	}

	return &d
}

// metadataStrings returns a context metadata list that arrives as a slice or
// as comma-separated strings.
func metadataStrings(val any) []string {
	var raw []string

	switch v := val.(type) {
	case string:
		raw = []string{v}
	case []string:
		raw = v
	}

	var out []string

	for _, r := range raw {
		for item := range strings.SplitSeq(r, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	}

	return out
}

// writeResults merges the run's results into the stored app status, keeping
// keys such as `initialized` and whatever other apps stored meanwhile.
func (s SOH) writeResults(exp *types.Experiment) {
	err := exp.UpdateAppStatus("soh", func(status map[string]any) {
		if len(s.status) > 0 {
			hosts := slices.Sorted(maps.Keys(s.status))
			states := make([]map[string]any, 0, len(hosts))

			for _, host := range hosts {
				states = append(states, structs.Map(s.status[host]))
			}

			status["hosts"] = states
		}

		if len(s.packetCapture) > 0 {
			status["packetCapture"] = s.packetCapture
		}
	})
	if err != nil {
		plog.Error(plog.TypeSoh, "saving SoH results", "exp", exp.Metadata.Name, "err", err)
	}
}

func (s SOH) writeInitialized(exp *types.Experiment) {
	err := exp.UpdateAppStatus("soh", func(status map[string]any) {
		status["initialized"] = true
	})
	if err != nil {
		plog.Error(plog.TypeSoh, "saving SoH status", "exp", exp.Metadata.Name, "err", err)
	}
}
