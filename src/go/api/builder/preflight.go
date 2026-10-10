package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"phenix/types/builder"
)

// PreflightCheck names one of the preflight checks a draft's document can be
// put through before an experiment is started from it (see [RunPreflight]).
type PreflightCheck string

// The preflight checks, in the order the editor lists them.
const (
	// PreflightCapacity compares the CPUs and memory the document's devices
	// take with what the schedulable cluster hosts have free, and checks that
	// each device fits on at least one host.
	PreflightCapacity PreflightCheck = "capacity"
	// PreflightNetwork compares the VLANs the document's devices use with an
	// experiment's VLAN range, its networks' VLAN aliases with the VLANs
	// running experiments use, and the bridges its interfaces use with those
	// the cluster hosts have.
	PreflightNetwork PreflightCheck = "network"
	// PreflightDisks looks up each drive image among the server's disk
	// images, and checks it is of the kind its device needs.
	PreflightDisks PreflightCheck = "disks"
	// PreflightApps looks up each app the document's scenarios name among the
	// apps the server has.
	PreflightApps PreflightCheck = "apps"
)

// PreflightStatus is the outcome of one preflight check.
type PreflightStatus string

// The outcomes of a preflight check (see [PreflightResult]).
const (
	PreflightPassed      PreflightStatus = "passed"
	PreflightFailed      PreflightStatus = "failed"
	PreflightUnavailable PreflightStatus = "unavailable"
)

// The kinds of disk image a [PreflightImage] names, as phenix/api/disk names
// them. Any other kind is unknown, and fits any device.
const (
	PreflightImageVM        = "VM"
	PreflightImageContainer = "Container"
	PreflightImageISO       = "ISO"
)

const (
	// PreflightTimeout bounds one preflight check: a check that takes longer
	// is reported unavailable, and the others are reported as they finish.
	PreflightTimeout = 20 * time.Second

	// preflightDefaultVCPUs and preflightDefaultMemory are what a node spec
	// without hardware.vcpus or hardware.memory gets, in vCPUs and MB, when
	// phenix sets a topology's defaults.
	preflightDefaultVCPUs  = 1
	preflightDefaultMemory = 512

	// preflightDefaultBridge is the bridge an experiment's interfaces are
	// on unless it names another default bridge; an interface naming it, or
	// none, is on the experiment's default bridge.
	preflightDefaultBridge = "phenix"

	// preflightContainerType is the general.vm_type of a device phenix starts
	// as a container; any other is a kvm VM.
	preflightContainerType = "container"

	// preflightListed is how many names a message lists before it says how
	// many more there are.
	preflightListed = 3
)

// ErrPreflightUnavailable is what a [PreflightUnavailableError] unwraps to.
var ErrPreflightUnavailable = errors.New("builder: preflight source unavailable")

// PreflightUnavailableError is what a [PreflightEnvironment] returns for what
// it may not or cannot read: the caller's role does not allow it, or the
// service holding it cannot be reached. Reason says which, in words shown to
// the caller. It unwraps to [ErrPreflightUnavailable].
type PreflightUnavailableError struct {
	Reason string
}

func (e *PreflightUnavailableError) Error() string {
	return e.Reason
}

// Unwrap allows [errors.Is](err, ErrPreflightUnavailable) to succeed.
func (e *PreflightUnavailableError) Unwrap() error { return ErrPreflightUnavailable }

// NewPreflightUnavailable returns the error a [PreflightEnvironment] returns
// for what it may not or cannot read, for the reason given: a lowercase
// phrase such as "your role may not list the cluster hosts".
func NewPreflightUnavailable(reason string) error {
	return &PreflightUnavailableError{Reason: reason}
}

// PreflightHost is a cluster host VMs can be scheduled on, as minimega
// reports it: its CPUs, and its memory in MB, with what the VMs already on it
// take of each.
type PreflightHost struct {
	Name      string
	CPUs      int
	CPUCommit int
	MemTotal  int
	MemCommit int
}

// PreflightImage is a disk image of the server: its file name, and its kind,
// one of [PreflightImageVM], [PreflightImageContainer] and
// [PreflightImageISO], or another for an image of unknown kind.
type PreflightImage struct {
	Name string
	Kind string
}

// PreflightExperiment is what the network check reads of the experiment a
// request names: its VLAN range, which phenix applies only when both ends are
// set, and its default bridge ("" for phenix's).
type PreflightExperiment struct {
	VLANMin       int
	VLANMax       int
	DefaultBridge string
}

// PreflightVLAN is a VLAN a running experiment holds: its ID, its alias there,
// and the experiment.
type PreflightVLAN struct {
	ID         int
	Alias      string
	Experiment string
}

// PreflightVLANs is what the network check reads of the running experiments:
// the VLANs those the caller may see hold, and whether a running experiment
// the caller may not see was left out. Nothing names such an experiment or
// the VLANs it holds, which the check then does not compare.
type PreflightVLANs struct {
	InUse  []PreflightVLAN
	Hidden bool
}

// PreflightEnvironment is what the preflight checks read, as the caller may
// read it: each method returns a [PreflightUnavailableError] for what the
// caller's role does not allow, and any other error for what cannot be read.
// Its methods may be called at once from several goroutines, and must never
// write anything.
type PreflightEnvironment interface {
	// ClusterHosts returns the hosts VMs can be scheduled on.
	ClusterHosts(ctx context.Context) ([]PreflightHost, error)
	// DiskImages returns the server's disk images.
	DiskImages(ctx context.Context) ([]PreflightImage, error)
	// Apps returns the names of the apps the server can run: the default
	// apps, the built-in ones, and the user apps on its PATH.
	Apps(ctx context.Context) ([]string, error)
	// ScenarioApps returns the names of the apps the named Scenario config
	// runs (those it does not disable), or an error matching [ErrNotFound]
	// when there is no such config.
	ScenarioApps(ctx context.Context, scenario string) ([]string, error)
	// Experiment returns the VLAN range and default bridge of the named
	// experiment.
	Experiment(ctx context.Context, name string) (PreflightExperiment, error)
	// VLANsInUse returns the VLANs the running experiments the caller may
	// see hold, but the one named except, and whether a running experiment
	// the caller may not see was left out.
	VLANsInUse(ctx context.Context, except string) (PreflightVLANs, error)
	// Bridges returns the bridges each schedulable host has, by host name.
	Bridges(ctx context.Context) (map[string][]string, error)
}

// PreflightResult is what one check found: its status, a summary of one
// line, and its issues, each located at the element of the document it is
// about when it is about one. A check failed when an issue is an error; it is
// unavailable when it, or a part of it, could not be made and nothing failed,
// and then a warning of code preflight.unavailable (or, for a scenario, of
// preflight.app.scenario-unreadable) says why; otherwise it passed, with
// warnings, if any, among its issues.
type PreflightResult struct {
	Name    PreflightCheck  `json:"name"`
	Status  PreflightStatus `json:"status"`
	Summary string          `json:"summary"`
	Issues  []builder.Issue `json:"issues"`
}

// PreflightReport is the result of each check a request names, in its order,
// and the names of those that passed, failed and were unavailable, in the
// same order.
type PreflightReport struct {
	Checks      []PreflightResult `json:"checks"`
	Passed      []PreflightCheck  `json:"passed"`
	Failed      []PreflightCheck  `json:"failed"`
	Unavailable []PreflightCheck  `json:"unavailable"`
}

// PreflightRequest names the checks to make, in order, and the experiment
// whose VLAN range and default bridge the network check goes by ("" for
// none). Timeout bounds each check; zero is [PreflightTimeout].
type PreflightRequest struct {
	Checks     []PreflightCheck
	Experiment string
	Timeout    time.Duration
	// checkTimeouts bounds each check it names instead of Timeout, so one
	// check can time out at once while the others have all the time they
	// need, and a test of the timeout does not depend on how fast the other
	// checks run.
	checkTimeouts map[PreflightCheck]time.Duration
}

// PreflightChecks returns every preflight check, in the order the editor
// lists them.
func PreflightChecks() []PreflightCheck {
	return []PreflightCheck{PreflightCapacity, PreflightNetwork, PreflightDisks, PreflightApps}
}

// ParsePreflightCheck returns the check name names, and whether there is
// one.
func ParsePreflightCheck(name string) (PreflightCheck, bool) {
	check := PreflightCheck(name)

	return check, slices.Contains(PreflightChecks(), check)
}

// RunPreflight makes the checks request names on doc, reading what they need
// through env, all at once and each for at most request.Timeout, and reports
// them in the order asked. The checks only read: nothing is written, and no
// VM or experiment is started. A check whose source cannot be read, or that
// does not finish in time, is unavailable, and never keeps the others from
// being made.
func RunPreflight(
	ctx context.Context,
	doc *builder.Document,
	request PreflightRequest,
	env PreflightEnvironment,
) PreflightReport {
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = PreflightTimeout
	}

	results := make([]PreflightResult, len(request.Checks))

	var group sync.WaitGroup

	for i, check := range request.Checks {
		limit := timeout
		if own := request.checkTimeouts[check]; own > 0 {
			limit = own
		}

		group.Add(1)

		go func() {
			defer group.Done()

			results[i] = runPreflightCheck(ctx, doc, check, request.Experiment, limit, env)
		}()
	}

	group.Wait()

	report := PreflightReport{
		Checks:      results,
		Passed:      []PreflightCheck{},
		Failed:      []PreflightCheck{},
		Unavailable: []PreflightCheck{},
	}

	for _, result := range results {
		switch result.Status {
		case PreflightPassed:
			report.Passed = append(report.Passed, result.Name)
		case PreflightFailed:
			report.Failed = append(report.Failed, result.Name)
		case PreflightUnavailable:
			report.Unavailable = append(report.Unavailable, result.Name)
		}
	}

	return report
}

// runPreflightCheck makes one check, and reports it unavailable once it has
// taken longer than timeout. A check left behind finishes on its own; its
// result is dropped.
func runPreflightCheck(
	ctx context.Context,
	doc *builder.Document,
	check PreflightCheck,
	experiment string,
	timeout time.Duration,
	env PreflightEnvironment,
) PreflightResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan PreflightResult, 1)

	go func() {
		done <- preflightCheck(ctx, doc, check, experiment, env)
	}()

	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		return unavailableResult(check, fmt.Sprintf("the check did not finish within %s", timeout))
	}
}

// preflightCheck makes one check.
func preflightCheck(
	ctx context.Context,
	doc *builder.Document,
	check PreflightCheck,
	experiment string,
	env PreflightEnvironment,
) PreflightResult {
	switch check {
	case PreflightCapacity:
		return capacityCheck(ctx, doc, env)
	case PreflightNetwork:
		return networkCheck(ctx, doc, experiment, env)
	case PreflightDisks:
		return disksCheck(ctx, doc, env)
	case PreflightApps:
		return appsCheck(ctx, doc, env)
	}

	return unavailableResult(check, "there is no such check")
}

// preflightResult is the result of a check that found issues: failed when
// one is an error, else unavailable when incomplete, else passed.
func preflightResult(check PreflightCheck, summary string, issues []builder.Issue, incomplete bool) PreflightResult {
	status := PreflightPassed

	switch {
	case slices.ContainsFunc(issues, func(issue builder.Issue) bool { return issue.Severity == builder.SeverityError }):
		status = PreflightFailed
	case incomplete:
		status = PreflightUnavailable
	}

	if issues == nil {
		issues = []builder.Issue{}
	}

	return PreflightResult{Name: check, Status: status, Summary: summary, Issues: issues}
}

// unavailableResult is the result of a check that could not be made, for
// the reason given.
func unavailableResult(check PreflightCheck, reason string) PreflightResult {
	return preflightResult(check, sentence(reason), []builder.Issue{unavailableIssue(reason)}, true)
}

// unavailableIssue is the issue saying why a check, or a part of one, could
// not be made.
func unavailableIssue(reason string) builder.Issue {
	return builder.NewIssue(builder.CodePreflightUnavailable, "", reason)
}

// unavailableReason is why a source of a check could not be read: the reason
// a [PreflightUnavailableError] gives, else that what it holds cannot be
// read, and why.
func unavailableReason(what string, err error) string {
	var unavailable *PreflightUnavailableError
	if errors.As(err, &unavailable) {
		return unavailable.Reason
	}

	return fmt.Sprintf("%s cannot be read: %v", what, err)
}

// preflightDevice is a device of the document that phenix starts, as a VM or
// a container: every device but an external one, those included from another
// topology too, as an experiment merges them in.
type preflightDevice struct {
	index    int
	node     *builder.Node
	hostname string
	spec     map[string]any
}

// preflightDevices returns the devices of doc phenix starts, in document
// order.
func preflightDevices(doc *builder.Document) []preflightDevice {
	devices := make([]preflightDevice, 0, len(doc.Nodes))

	for i := range doc.Nodes {
		node := &doc.Nodes[i]
		if node.Kind != builder.NodeKindDevice || node.Device == nil {
			continue
		}

		if external, _ := node.Device.Spec["external"].(bool); external {
			continue
		}

		devices = append(devices, preflightDevice{
			index: i, node: node, hostname: node.Device.Hostname, spec: node.Device.Spec,
		})
	}

	return devices
}

// issue is the issue of code about the field of the device's spec at path
// (below the device's payload, with bracketed indexes) and field (the same
// path as the Inspector names it), saying message.
func (d preflightDevice) issue(code builder.Code, path, field, message string) builder.Issue {
	issue := builder.NewIssue(code, fmt.Sprintf("nodes[%d].device.%s", d.index, path), message)
	issue.NodeID = d.node.ID
	issue.Field = field

	return issue
}

// specMap reads an object of a node spec, or nil for anything else.
func specMap(value any) map[string]any {
	object, _ := value.(map[string]any)

	return object
}

// specItems reads a list of a node spec, or nil for anything else.
func specItems(value any) []any {
	items, _ := value.([]any)

	return items
}

// specString reads text of a node spec without the white space around it,
// or "" for anything else.
func specString(value any) string {
	text, _ := value.(string)

	return strings.TrimSpace(text)
}

// specWhole reads a positive whole number of a node spec: a number, or text
// holding one, which phenix's schema allows for vcpus and memory. It reports
// none for zero, which phenix replaces with its default, and for anything
// else.
func specWhole(value any) (int, bool) {
	switch number := value.(type) {
	case float64:
		if number >= 1 && number <= math.MaxInt32 && number == math.Trunc(number) {
			return int(number), true
		}
	case int:
		if number > 0 {
			return number, true
		}
	case json.Number:
		return positiveWhole(number.String())
	case string:
		return positiveWhole(number)
	}

	return 0, false
}

// positiveWhole reads text holding a positive whole number.
func positiveWhole(text string) (int, bool) {
	number, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || number <= 0 {
		return 0, false
	}

	return number, true
}

// capacityCheck compares what the document's devices take with what the
// schedulable hosts have: the CPUs and memory they have free together, and
// the CPUs and memory of each host, since a device runs on one host and so
// must fit on at least one. Sharing CPUs is how VMs usually run, so having
// too few free is a warning; too little memory, or a device no host can
// hold, is an error.
func capacityCheck(ctx context.Context, doc *builder.Document, env PreflightEnvironment) PreflightResult {
	hosts, err := env.ClusterHosts(ctx)
	if err != nil {
		return unavailableResult(PreflightCapacity, unavailableReason("the cluster hosts", err))
	}

	if len(hosts) == 0 {
		return unavailableResult(PreflightCapacity, "no schedulable cluster host is listed")
	}

	var freeCPUs, freeMemory int

	for _, host := range hosts {
		freeCPUs += max(host.CPUs-host.CPUCommit, 0)
		freeMemory += max(host.MemTotal-host.MemCommit, 0)
	}

	devices := preflightDevices(doc)

	var issues []builder.Issue

	neededCPUs, neededMemory := 0, 0

	for _, device := range devices {
		hardware := specMap(device.spec["hardware"])

		vcpus, ok := specWhole(hardware["vcpus"])
		if !ok {
			vcpus = preflightDefaultVCPUs
		}

		memory, ok := specWhole(hardware["memory"])
		if !ok {
			memory = preflightDefaultMemory
		}

		neededCPUs += vcpus
		neededMemory += memory

		issues = append(issues, deviceFit(device, vcpus, memory, hosts)...)
	}

	if neededMemory > freeMemory {
		issues = append(issues, builder.NewIssue(builder.CodePreflightCapacityMemory, "", fmt.Sprintf(
			"the diagram's devices take %d MB of memory, and the schedulable hosts have %d MB free",
			neededMemory, freeMemory,
		)))
	}

	if neededCPUs > freeCPUs {
		issues = append(issues, builder.NewIssue(builder.CodePreflightCapacityCPU, "", fmt.Sprintf(
			"the diagram's devices take %s, and the schedulable hosts have %d free, so VMs would share CPUs",
			plural(neededCPUs, "vCPU", "vCPUs"), freeCPUs,
		)))
	}

	summary := fmt.Sprintf(
		"Needed: %s and %d MB of memory for %s. Free: %s and %d MB on %s.",
		plural(neededCPUs, "vCPU", "vCPUs"), neededMemory, plural(len(devices), "device", "devices"),
		plural(freeCPUs, "vCPU", "vCPUs"), freeMemory, plural(len(hosts), "schedulable host", "schedulable hosts"),
	)

	return preflightResult(PreflightCapacity, summary, issues, false)
}

// deviceFit returns the issues of a device taking vcpus and memory (in MB)
// that no single host can hold: one with both at least that many CPUs and at
// least that much memory. A device taking more CPUs, or more memory, than
// every host has gets an issue at that field; one whose CPUs and memory each
// fit some host, but never the same one, gets one issue at its vcpus.
func deviceFit(device preflightDevice, vcpus, memory int, hosts []PreflightHost) []builder.Issue {
	var largestCPUs, largestMemory int

	for _, host := range hosts {
		if vcpus <= host.CPUs && memory <= host.MemTotal {
			return nil
		}

		largestCPUs = max(largestCPUs, host.CPUs)
		largestMemory = max(largestMemory, host.MemTotal)
	}

	const (
		vcpusField  = "spec.hardware.vcpus"
		memoryField = "spec.hardware.memory"
	)

	tooBig := func(field, message string) builder.Issue {
		return device.issue(builder.CodePreflightCapacityVMTooBig, field, field, message)
	}

	var issues []builder.Issue

	if vcpus > largestCPUs {
		issues = append(issues, tooBig(vcpusField, fmt.Sprintf(
			"device %q takes %s, more than any schedulable host has (at most %d)",
			device.hostname, plural(vcpus, "vCPU", "vCPUs"), largestCPUs,
		)))
	}

	if memory > largestMemory {
		issues = append(issues, tooBig(memoryField, fmt.Sprintf(
			"device %q takes %d MB of memory, more than any schedulable host has (at most %d MB)",
			device.hostname, memory, largestMemory,
		)))
	}

	if len(issues) == 0 {
		issues = append(issues, tooBig(vcpusField, fmt.Sprintf(
			"device %q takes %s and %d MB of memory, and no single schedulable host has both that many CPUs "+
				"and that much memory",
			device.hostname, plural(vcpus, "vCPU", "vCPUs"), memory,
		)))
	}

	return issues
}

// preflightBridgeUse is the first interface of a device that names a bridge,
// which an issue about the bridge is located at.
type preflightBridgeUse struct {
	device   preflightDevice
	position int
}

// preflightNetworks is what the document's devices need of the network: the
// VLANs their interfaces are on, each name once and sorted, and the bridges
// they name, each with its first use ("" for the experiment's default one,
// which an interface names by naming none or phenix's).
type preflightNetworks struct {
	vlans   []string
	bridges map[string]*preflightBridgeUse
}

// networkNeeds reads what the document's devices need of the network. An
// interface whose connection point is connected on the canvas is on the
// network it is connected to, as publishing writes it; any other is on the
// VLAN its spec names, if any.
func networkNeeds(doc *builder.Document) preflightNetworks {
	var (
		connected = connectedNetworks(doc)
		vlans     = map[string]bool{}
		needs     = preflightNetworks{vlans: nil, bridges: map[string]*preflightBridgeUse{}}
	)

	for _, device := range preflightDevices(doc) {
		for position, entry := range specItems(specMap(device.spec["network"])["interfaces"]) {
			iface := specMap(entry)
			if iface == nil {
				continue
			}

			vlan := specString(iface["vlan"])
			if network, ok := handleNetwork(device, specString(iface["name"]), connected); ok {
				vlan = network
			}

			if vlan != "" {
				vlans[vlan] = true
			}

			bridge := specString(iface["bridge"])
			if bridge == preflightDefaultBridge {
				bridge = ""
			}

			if needs.bridges[bridge] == nil {
				needs.bridges[bridge] = &preflightBridgeUse{device: device, position: position}
			}
		}
	}

	needs.vlans = slices.Sorted(maps.Keys(vlans))

	return needs
}

// connectedNetworks returns the name of the network each connected
// connection point of a device is connected to, by the device's node ID and
// the connection point's ID, joined by a slash.
func connectedNetworks(doc *builder.Document) map[string]string {
	devices := make(map[string]bool, len(doc.Nodes))

	for i := range doc.Nodes {
		if doc.Nodes[i].Kind == builder.NodeKindDevice {
			devices[doc.Nodes[i].ID] = true
		}
	}

	networks := make(map[string]string, len(doc.Networks))

	for i := range doc.Networks {
		networks[doc.Networks[i].ID] = doc.Networks[i].Name
	}

	connected := make(map[string]string, len(doc.Edges))

	for i := range doc.Edges {
		edge := &doc.Edges[i]

		network, ok := networks[edge.NetworkID]
		if !ok {
			continue
		}

		if devices[edge.SourceNodeID] && edge.SourceHandleID != "" {
			connected[edge.SourceNodeID+"/"+edge.SourceHandleID] = network
		}

		if devices[edge.TargetNodeID] && edge.TargetHandleID != "" {
			connected[edge.TargetNodeID+"/"+edge.TargetHandleID] = network
		}
	}

	return connected
}

// handleNetwork returns the network the device's connection point for the
// spec interface named name is connected to, matching the name exactly, else
// ignoring case, as publishing does.
func handleNetwork(device preflightDevice, name string, connected map[string]string) (string, bool) {
	var (
		fallback string
		found    bool
	)

	for _, handle := range device.node.Device.Interfaces {
		network, ok := connected[device.node.ID+"/"+handle.ID]
		if !ok {
			continue
		}

		if handle.Name == name {
			return network, true
		}

		if !found && strings.EqualFold(handle.Name, name) {
			fallback, found = network, true
		}
	}

	return fallback, found
}

// networkCheck compares what the document's devices need of the network
// with the experiment named (if any), the VLANs running experiments hold and
// the bridges of the cluster hosts. A part that cannot be read leaves only
// that part unchecked.
func networkCheck(ctx context.Context, doc *builder.Document, experiment string, env PreflightEnvironment) PreflightResult {
	var (
		needs         = networkNeeds(doc)
		parts         []string
		unchecked     []string
		issues        []builder.Issue
		defaultBridge = preflightDefaultBridge
		bridgeKnown   = true
	)

	switch experiment {
	case "":
		parts = append(parts, plural(len(needs.vlans), "VLAN", "VLANs")+"; no experiment is named, so no VLAN range applies")
	default:
		settings, err := env.Experiment(ctx, experiment)
		if err != nil {
			bridgeKnown = false

			unchecked = append(unchecked, unavailableReason("experiment "+experiment, err)+
				", so its VLAN range and default bridge were not checked")

			break
		}

		if settings.DefaultBridge != "" {
			defaultBridge = settings.DefaultBridge
		}

		rangeIssues, part := vlanRangeIssues(doc, needs.vlans, experiment, settings)
		issues = append(issues, rangeIssues...)
		parts = append(parts, part)
	}

	aliasIssues, aliasPart, aliasUnchecked := aliasesInUse(ctx, doc, experiment, env)
	issues = append(issues, aliasIssues...)
	parts = append(parts, aliasPart...)
	unchecked = append(unchecked, aliasUnchecked...)

	wanted := wantedBridges(needs.bridges, defaultBridge, bridgeKnown)

	bridgeIssues, bridgePart, bridgeUnchecked := missingBridges(ctx, wanted, env)
	issues = append(issues, bridgeIssues...)
	parts = append(parts, bridgePart...)
	unchecked = append(unchecked, bridgeUnchecked...)

	for _, reason := range unchecked {
		issues = append(issues, unavailableIssue(reason))
	}

	summary := ""
	if len(parts) > 0 {
		summary = sentence(strings.Join(parts, "; "))
	}

	if len(unchecked) > 0 {
		summary = strings.TrimSpace(summary + " Not checked: " + strings.Join(unchecked, "; ") + ".")
	}

	return preflightResult(PreflightNetwork, summary, issues, len(unchecked) > 0)
}

// vlanRangeIssues compares the VLANs the document's devices use, and its
// networks' VLAN aliases, with the experiment's VLAN range, which phenix
// applies only when both of its ends are set. It returns the issues and the
// part of the summary that says what was compared.
func vlanRangeIssues(
	doc *builder.Document,
	vlans []string,
	experiment string,
	settings PreflightExperiment,
) ([]builder.Issue, string) {
	used := plural(len(vlans), "VLAN", "VLANs")

	if settings.VLANMin <= 0 || settings.VLANMax <= 0 {
		return nil, fmt.Sprintf("%s; experiment %s sets no VLAN range", used, experiment)
	}

	var (
		low, high = settings.VLANMin, settings.VLANMax
		size      = max(high-low+1, 0)
		issues    []builder.Issue
	)

	if len(vlans) > size {
		issues = append(issues, builder.NewIssue(builder.CodePreflightNetworkVLANRange, "", fmt.Sprintf(
			"the diagram's devices use %s, and experiment %s's VLAN range %d to %d holds %s",
			used, experiment, low, high, plural(size, "VLAN", "VLANs"),
		)))
	}

	for i := range doc.Networks {
		network := &doc.Networks[i]
		if network.Alias == nil || (*network.Alias >= low && *network.Alias <= high) {
			continue
		}

		issue := builder.NewIssue(builder.CodePreflightNetworkVLANRange, fmt.Sprintf("networks[%d].alias", i), fmt.Sprintf(
			"network %q has VLAN alias %d, outside experiment %s's VLAN range %d to %d",
			network.Name, *network.Alias, experiment, low, high,
		))
		issue.NetworkID = network.ID
		issues = append(issues, issue)
	}

	return issues, fmt.Sprintf("%s; experiment %s's VLAN range is %d to %d", used, experiment, low, high)
}

// aliasesInUse looks up each VLAN alias the document's networks fix among
// the VLANs the running experiments the caller may see hold, but the
// experiment named. It returns the issues, the part of the summary that
// says what was compared, and why the aliases could not be compared, if
// they could not. When a running experiment the caller may not see was left
// out, the part says so, without naming it or its VLANs.
func aliasesInUse(
	ctx context.Context,
	doc *builder.Document,
	experiment string,
	env PreflightEnvironment,
) ([]builder.Issue, []string, []string) {
	aliased := 0

	for i := range doc.Networks {
		if doc.Networks[i].Alias != nil {
			aliased++
		}
	}

	if aliased == 0 {
		return nil, nil, nil
	}

	used, err := env.VLANsInUse(ctx, experiment)
	if err != nil {
		return nil, nil, []string{
			unavailableReason("the VLANs of running experiments", err) + ", so VLAN aliases were not checked",
		}
	}

	byID := make(map[int]PreflightVLAN, len(used.InUse))

	for _, vlan := range used.InUse {
		if _, ok := byID[vlan.ID]; !ok {
			byID[vlan.ID] = vlan
		}
	}

	var issues []builder.Issue

	for i := range doc.Networks {
		network := &doc.Networks[i]
		if network.Alias == nil {
			continue
		}

		vlan, ok := byID[*network.Alias]
		if !ok {
			continue
		}

		message := fmt.Sprintf(
			"network %q has VLAN alias %d, which running experiment %s uses for VLAN %q",
			network.Name, *network.Alias, vlan.Experiment, vlan.Alias,
		)

		issue := builder.NewIssue(builder.CodePreflightNetworkAliasInUse, fmt.Sprintf("networks[%d].alias", i), message)
		issue.NetworkID = network.ID
		issues = append(issues, issue)
	}

	part := plural(aliased, "VLAN alias", "VLAN aliases") + " compared with running experiments"
	if used.Hidden {
		part += "; running experiments your role may not list were not compared"
	}

	return issues, []string{part}, nil
}

// wantedBridges returns the bridges the document's devices are on, by name,
// each with the interface an issue about it is located at (none for the
// experiment's default bridge, which no interface names). The default bridge
// is left out when it is not known.
func wantedBridges(
	uses map[string]*preflightBridgeUse,
	defaultBridge string,
	known bool,
) map[string]*preflightBridgeUse {
	wanted := make(map[string]*preflightBridgeUse, len(uses))

	for name, use := range uses {
		switch {
		case name != "":
			wanted[name] = use
		case known:
			if _, ok := wanted[defaultBridge]; !ok {
				wanted[defaultBridge] = nil
			}
		}
	}

	return wanted
}

// missingBridges looks up each wanted bridge among the bridges of the
// schedulable hosts. A bridge a host lacks is a warning, since minimega
// creates a bridge when a VM starts on it. It returns the issues, the part
// of the summary that says what was compared, and why the bridges could not
// be compared, if they could not.
func missingBridges(
	ctx context.Context,
	wanted map[string]*preflightBridgeUse,
	env PreflightEnvironment,
) ([]builder.Issue, []string, []string) {
	if len(wanted) == 0 {
		return nil, nil, nil
	}

	hostBridges, err := env.Bridges(ctx)

	switch {
	case err != nil:
		return nil, nil, []string{unavailableReason("the bridges of the cluster hosts", err) + ", so bridges were not checked"}
	case len(hostBridges) == 0:
		return nil, nil, []string{"no cluster host listed its bridges, so bridges were not checked"}
	}

	var (
		hosts  = slices.Sorted(maps.Keys(hostBridges))
		names  = slices.Sorted(maps.Keys(wanted))
		issues []builder.Issue
	)

	for _, name := range names {
		var missing []string

		for _, host := range hosts {
			if !slices.Contains(hostBridges[host], name) {
				missing = append(missing, host)
			}
		}

		if len(missing) == 0 {
			continue
		}

		message := fmt.Sprintf(
			"bridge %q does not exist on %s %s; minimega creates it there when the experiment starts",
			name, pluralWord(len(missing), "host", "hosts"), listed(missing),
		)

		if use := wanted[name]; use != nil {
			issues = append(issues, use.device.issue(
				builder.CodePreflightNetworkBridge,
				fmt.Sprintf("spec.network.interfaces[%d].bridge", use.position),
				fmt.Sprintf("spec.network.interfaces.%d.bridge", use.position),
				message,
			))

			continue
		}

		issues = append(issues, builder.NewIssue(builder.CodePreflightNetworkBridge, "", message))
	}

	return issues, []string{fmt.Sprintf(
		"%s compared with the bridges of %s",
		plural(len(names), "bridge", "bridges"), plural(len(hosts), "host", "hosts"),
	)}, nil
}

// disksCheck looks up each drive image of the document's devices among the
// server's disk images by file name, as the editor's own check does, and
// checks it is of the kind its device needs: a VM or ISO image for a kvm VM,
// a container image for a container. An empty list of images is what the
// server gives when it cannot ask minimega, so the check is then unavailable,
// as the editor then checks no image either.
func disksCheck(ctx context.Context, doc *builder.Document, env PreflightEnvironment) PreflightResult {
	images, err := env.DiskImages(ctx)
	if err != nil {
		return unavailableResult(PreflightDisks, unavailableReason("the server's disk images", err))
	}

	if len(images) == 0 {
		return unavailableResult(PreflightDisks, "the server listed no disk images, as it does when minimega cannot be reached")
	}

	kinds := make(map[string]string, len(images))
	for _, image := range images {
		kinds[image.Name] = image.Kind
	}

	var (
		issues []builder.Issue
		drives int
	)

	for _, device := range preflightDevices(doc) {
		container := strings.EqualFold(specString(specMap(device.spec["general"])["vm_type"]), preflightContainerType)

		for position, entry := range specItems(specMap(device.spec["hardware"])["drives"]) {
			image := specString(specMap(entry)["image"])
			if image == "" {
				continue
			}

			drives++

			var (
				path  = fmt.Sprintf("spec.hardware.drives[%d].image", position)
				field = fmt.Sprintf("spec.hardware.drives.%d.image", position)
			)

			kind, found := kinds[image[strings.LastIndex(image, "/")+1:]]

			switch {
			case !found:
				issues = append(issues, device.issue(builder.CodePreflightDiskMissing, path, field, fmt.Sprintf(
					"device %q uses drive image %q, which is not one of the server's disk images", device.hostname, image,
				)))
			case !imageFits(kind, container):
				issues = append(issues, device.issue(builder.CodePreflightDiskKind, path, field, fmt.Sprintf(
					"device %q is %s, but its drive image %q is %s",
					device.hostname, deviceKind(container), image, imageKind(kind),
				)))
			}
		}
	}

	summary := "No device names a drive image."
	if drives > 0 {
		summary = fmt.Sprintf(
			"Found: %d of %s, among the server's %s.",
			drives-len(issues), plural(drives, "drive image", "drive images"),
			plural(len(images), "disk image", "disk images"),
		)
	}

	return preflightResult(PreflightDisks, summary, issues, false)
}

// imageFits reports whether an image of kind fits a device that is a
// container, or a kvm VM. An image of unknown kind fits either.
func imageFits(kind string, container bool) bool {
	switch kind {
	case PreflightImageContainer:
		return container
	case PreflightImageVM, PreflightImageISO:
		return !container
	}

	return true
}

// deviceKind names what a device is, as a message says it.
func deviceKind(container bool) string {
	if container {
		return "a container"
	}

	return "a kvm VM"
}

// imageKind names the kind of an image, as a message says it.
func imageKind(kind string) string {
	switch kind {
	case PreflightImageContainer:
		return "a container image"
	case PreflightImageISO:
		return "an ISO image"
	case PreflightImageVM:
		return "a VM image"
	}

	return "an image of unknown kind"
}

// appsCheck reads each Scenario config the document lists and looks up each
// app it runs among the apps the server has. A scenario that does not exist
// fails the check; one the caller may not read, or that cannot be read,
// leaves the check unavailable unless something else fails it.
func appsCheck(ctx context.Context, doc *builder.Document, env PreflightEnvironment) PreflightResult {
	if len(doc.Scenarios) == 0 {
		return preflightResult(PreflightApps, "The diagram lists no scenarios, so it needs no apps.", nil, false)
	}

	available, err := env.Apps(ctx)
	if err != nil {
		return unavailableResult(PreflightApps, unavailableReason("the server's apps", err))
	}

	var (
		issues     []builder.Issue
		apps       int
		missing    int
		read       int
		unreadable int
	)

	for i, scenario := range doc.Scenarios {
		path := fmt.Sprintf("scenarios[%d]", i)

		names, err := env.ScenarioApps(ctx, scenario)
		if err != nil {
			issue := scenarioIssue(path, scenario, err)
			if issue.Severity != builder.SeverityError {
				unreadable++
			}

			issues = append(issues, issue)

			continue
		}

		read++

		for _, name := range names {
			apps++

			if slices.Contains(available, name) {
				continue
			}

			missing++

			issues = append(issues, builder.NewIssue(builder.CodePreflightAppMissing, path, fmt.Sprintf(
				"scenario %q names app %q, which is not a default app, a built-in app or a user app "+
					"(phenix-app-%s on the server's PATH) of this server",
				scenario, name, name,
			)))
		}
	}

	summary := fmt.Sprintf(
		"Found: %d of %s, named by %s.",
		apps-missing, plural(apps, "app", "apps"), plural(read, "scenario", "scenarios"),
	)

	if unread := len(doc.Scenarios) - read; unread > 0 {
		summary += fmt.Sprintf(" Not read: %s.", plural(unread, "scenario", "scenarios"))
	}

	return preflightResult(PreflightApps, summary, issues, unreadable > 0)
}

// scenarioIssue is the issue of a listed scenario that could not be read:
// the error preflight.app.scenario-missing for one that does not exist, the
// warning preflight.app.scenario-unreadable for one the caller may not read
// or that cannot be read now.
func scenarioIssue(path, scenario string, err error) builder.Issue {
	if errors.Is(err, ErrNotFound) {
		return builder.NewIssue(
			builder.CodePreflightScenarioMissing, path, fmt.Sprintf("scenario %q does not exist", scenario),
		)
	}

	return builder.NewIssue(
		builder.CodePreflightScenarioUnread, path, unavailableReason(fmt.Sprintf("scenario %q", scenario), err),
	)
}

// plural is n and the word for that many: "1 host", "2 hosts".
func plural(n int, one, many string) string {
	return strconv.Itoa(n) + " " + pluralWord(n, one, many)
}

// pluralWord is the word for n things.
func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}

// listed names the first few of names, then how many more there are.
func listed(names []string) string {
	if len(names) <= preflightListed {
		return strings.Join(names, ", ")
	}

	return fmt.Sprintf("%s and %d more", strings.Join(names[:preflightListed], ", "), len(names)-preflightListed)
}

// sentence is text with its first letter capitalized and a final period.
func sentence(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	if size == 0 {
		return text
	}

	text = string(unicode.ToUpper(first)) + text[size:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}

	return text
}
