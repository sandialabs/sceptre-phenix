package workflow

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/mitchellh/mapstructure"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
)

const (
	// topologyKey and scenarioKey are the config kinds as the store keys them
	// and the experiment annotations that name a topology and a scenario.
	topologyKey = "topology"
	scenarioKey = "scenario"

	// scenarioKind and topologyKind are the canonical config kinds.
	scenarioKind = "Scenario"
	topologyKind = "Topology"

	// pendingFrom and pendingInclude key the fromScenario and include
	// references in a pending ref; its topology annotation is keyed by
	// topologyKey.
	pendingFrom    = "from"
	pendingInclude = "include"
)

// ErrUnresolved is wrapped by the errors [Prepare] returns when a topology or
// scenario the plan needs is missing, does not decode or merge, or cannot be
// used in an experiment.
var ErrUnresolved = errors.New("unresolved workflow reference")

// Prepared holds what carrying out a [Plan] takes from the store, resolved
// and checked before anything changes.
type Prepared struct {
	// Topology is the decoded topology with its includes merged in and without
	// the node defaults an experiment fills in. It is nil when the topology is
	// pending.
	TopologyName string
	Topology     ifaces.TopologySpec

	// Scenario is the decoded scenario with its fromScenario apps merged in. It
	// is nil when there is no scenario or it is pending.
	ScenarioName string
	Scenario     ifaces.ScenarioSpec

	// Aliases maps each VLAN alias in the topology to the workflow's ID for
	// it, or 0 so minimega picks one, and Schedules each scheduled node to its
	// cluster host. Only an update with a stored topology sets them: a create
	// passes the workflow's own mappings to the new experiment.
	Aliases   map[string]int
	Schedules map[string]string
}

// pendingConfig is what a pending ref says about a config the caller upserts
// before it carries out the plan. described is false for a bare <kind>/<name>.
type pendingConfig struct {
	described   bool
	annotations store.Annotations
	from        []string
	includes    []string
}

// Prepare resolves and checks every config that carrying out plan needs, so
// an apply fails before it creates, stops or updates an experiment. exp is
// the experiment mapped to the branch, if any; an update falls back to its
// topology and scenario annotations. A create also requires the scenario's
// topology annotation to name the topology, as creating an experiment does.
//
// pending names configs the caller upserts before the real apply, so only a
// dry run may pass any. Each is a ref from [PendingRef] or a bare
// <kind>/<name>, compared as a store config name. A pending config is not
// read, even when a stored one would be replaced; what its ref describes is
// checked as the real apply will check it.
//
// Errors wrap [ErrUnresolved] for a config that is missing, does not decode
// or merge, or whose nodes an experiment could not use, and [ErrInvalidSpec]
// for a malformed pending ref, an unknown action or a create without a
// topology.
func Prepare(spec Spec, plan Plan, exp *types.Experiment, pending ...string) (Prepared, error) {
	skip, err := pendingConfigs(pending)
	if err != nil {
		return Prepared{}, err
	}

	switch plan.Action {
	case ActionNone:
		return Prepared{}, nil
	case ActionCreate, ActionCreateAndStart:
		return prepareCreate(spec, skip)
	case ActionUpdate, ActionUpdateAndStart, ActionRestart:
		return prepareUpdate(spec, exp, skip)
	}

	return Prepared{}, fmt.Errorf("%w: unknown action %q", ErrInvalidSpec, plan.Action)
}

// CheckBridge checks the default bridge that carrying out plan gives its
// experiment as the experiment config hooks do, so an apply fails before it
// stops a running experiment. With the auto bridge mode the bridge is named
// after the experiment; otherwise it is the workflow's default bridge, which
// no other experiment in exps may use unless it is the shared phenix bridge.
// Errors wrap [ErrInvalidSpec].
func CheckBridge(spec Spec, plan Plan, exps []types.Experiment, mode common.BridgingMode) error {
	if plan.Action == ActionNone {
		return nil
	}

	bridge := spec.DefaultBridgeName()

	if mode == common.BridgeModeAuto {
		if len(plan.Experiment) > experiment.MaxBridgeNameLength {
			return fmt.Errorf(
				"%w: experiment name %q must be %d characters or less when the bridge mode is auto",
				ErrInvalidSpec, plan.Experiment, experiment.MaxBridgeNameLength,
			)
		}

		bridge = plan.Experiment
	}

	if bridge == defaultBridgeName {
		return nil
	}

	for _, other := range exps {
		if other.Metadata.Name != plan.Experiment && other.Spec.DefaultBridge() == bridge {
			return fmt.Errorf(
				"%w: experiment %q already uses default bridge %q",
				ErrInvalidSpec, other.Metadata.Name, bridge,
			)
		}
	}

	return nil
}

// PendingRef returns the ref that names cfg as pending in an apply dry run:
// its <kind>/<name>, such as "Topology/foo". A scenario's ref, and that of a
// topology with includes, go on with "?" and the url-encoded references
// [Prepare] checks: the scenario's topology annotation ("topology") and the
// scenarios its apps take settings from ("from"), or the topologies the
// topology includes ("include"). The references are decoded as the real apply
// decodes the stored config; a config that does not decode lists none.
func PendingRef(cfg store.Config) string {
	var (
		ref   = cfg.FullName()
		facts = url.Values{}
	)

	switch cfg.Kind {
	case scenarioKind:
		if topo, ok := cfg.Metadata.Annotations[topologyKey]; ok {
			facts.Set(topologyKey, topo)
		}

		if spec, err := types.DecodeScenarioFromConfig(cfg); err == nil {
			for _, app := range spec.Apps() {
				if from := app.FromScenario(); from != "" && !slices.Contains(facts[pendingFrom], from) {
					facts.Add(pendingFrom, from)
				}
			}
		}

		// Always described, so a missing topology annotation is reported.
		return ref + "?" + facts.Encode()
	case topologyKind:
		var spec v1.TopologySpec
		if err := mapstructure.WeakDecode(cfg.Spec, &spec); err == nil {
			for _, include := range spec.IncludedTopologies() {
				facts.Add(pendingInclude, include)
			}
		}

		if len(facts) > 0 {
			return ref + "?" + facts.Encode()
		}
	}

	return ref
}

// pendingConfigs parses refs, keyed by canonical store name such as
// "Topology/foo": the kind is canonicalized as the store does and the name
// kept exactly.
func pendingConfigs(refs []string) (map[string]pendingConfig, error) {
	pending := make(map[string]pendingConfig, len(refs))

	for _, ref := range refs {
		name, query, described := strings.Cut(ref, "?")

		full := store.ConfigFullName(name)
		if full == "" {
			return nil, fmt.Errorf("%w: pending config %q is not a <kind>/<name> config name", ErrInvalidSpec, ref)
		}

		facts, err := url.ParseQuery(query)
		if err != nil {
			return nil, fmt.Errorf("%w: pending config %q: %w", ErrInvalidSpec, ref, err)
		}

		var annotations store.Annotations
		if facts.Has(topologyKey) {
			annotations = store.Annotations{topologyKey: facts.Get(topologyKey)}
		}

		pending[full] = pendingConfig{
			described:   described,
			annotations: annotations,
			from:        facts[pendingFrom],
			includes:    facts[pendingInclude],
		}
	}

	return pending, nil
}

// prepareCreate resolves the topology and scenario a new experiment is
// created from.
func prepareCreate(spec Spec, pending map[string]pendingConfig) (Prepared, error) {
	if spec.ExperimentTopology() == "" {
		return Prepared{}, fmt.Errorf("%w: auto.create is set but topology is not", ErrInvalidSpec)
	}

	return resolveConfigs(spec.ExperimentTopology(), spec.ExperimentScenario(), true, pending)
}

// prepareUpdate resolves the topology and scenario an update applies to exp,
// and builds the VLAN aliases and schedules it sets.
func prepareUpdate(spec Spec, exp *types.Experiment, pending map[string]pendingConfig) (Prepared, error) {
	topoName := cmp.Or(spec.ExperimentTopology(), experimentAnnotation(exp, topologyKey))
	if topoName == "" {
		return Prepared{}, fmt.Errorf(
			"%w: no topology: the workflow config sets none and the experiment has no %s annotation",
			ErrUnresolved, topologyKey,
		)
	}

	scenarioName := cmp.Or(spec.ExperimentScenario(), experimentAnnotation(exp, scenarioKey))

	prep, err := resolveConfigs(topoName, scenarioName, false, pending)
	if err != nil {
		return Prepared{}, err
	}

	// A pending topology is not stored yet, so there is nothing to build from.
	if prep.Topology != nil {
		prep.Aliases = vlanAliases(prep.Topology, spec.VLANMappings())
		prep.Schedules = nodeSchedules(prep.Topology, spec.ScheduleMappings())
	}

	return prep, nil
}

// resolveConfigs reads and decodes the named topology and scenario. A pending
// config is not read: its name is kept, its spec left nil and only what its
// ref describes is checked. checkScenarioTopology requires the scenario's
// topology annotation to name topoName.
func resolveConfigs(
	topoName, scenarioName string,
	checkScenarioTopology bool,
	pending map[string]pendingConfig,
) (Prepared, error) {
	prep := Prepared{
		TopologyName: topoName,
		Topology:     nil,
		ScenarioName: scenarioName,
		Scenario:     nil,
		Aliases:      nil,
		Schedules:    nil,
	}

	if topo, ok := pending[store.ConfigFullName(topologyKey, topoName)]; ok {
		if err := checkPendingIncludes(topoName, topo.includes, pending); err != nil {
			return Prepared{}, err
		}
	} else if err := resolveTopology(&prep); err != nil {
		return Prepared{}, err
	}

	if scenarioName == "" {
		return prep, nil
	}

	if scn, ok := pending[store.ConfigFullName(scenarioKey, scenarioName)]; ok {
		if err := checkPendingScenario(scenarioName, topoName, scn, checkScenarioTopology, pending); err != nil {
			return Prepared{}, err
		}
	} else if err := resolveScenario(&prep, checkScenarioTopology); err != nil {
		return Prepared{}, err
	}

	return prep, nil
}

// checkPendingIncludes checks that each topology the pending topology named
// topoName includes can be found where decoding it looks: among the pending
// configs, in the store or as a file. None is decoded, so their own includes
// are not checked.
func checkPendingIncludes(topoName string, includes []string, pending map[string]pendingConfig) error {
	for _, include := range includes {
		if _, ok := pending[store.ConfigFullName(topologyKey, include)]; ok {
			continue
		}

		if _, err := types.LoadTopology(include); err != nil {
			return fmt.Errorf(
				"%w: decoding topology %q: loading included topology %s: %w",
				ErrUnresolved, topoName, include, err,
			)
		}
	}

	return nil
}

// checkPendingScenario checks what the ref of the pending scenario named name
// describes: with checkTopology, its topology annotation must name topoName,
// and each scenario its apps take settings from must be found and marked for
// topoName. A bare ref describes nothing, so nothing is checked.
func checkPendingScenario(name, topoName string, scn pendingConfig, checkTopology bool, pending map[string]pendingConfig) error {
	if !scn.described {
		return nil
	}

	if checkTopology {
		if err := scenarioMatchesTopology(name, scn.annotations, topoName); err != nil {
			return err
		}
	}

	for _, from := range scn.from {
		if err := checkFromScenario(name, from, topoName, pending); err != nil {
			return err
		}
	}

	return nil
}

// checkFromScenario checks the scenario named from, which the pending
// scenario named name takes app settings from, as merging scenarios does.
func checkFromScenario(name, from, topoName string, pending map[string]pendingConfig) error {
	var err error

	if scn, ok := pending[store.ConfigFullName(scenarioKey, from)]; ok {
		if !scn.described {
			return nil
		}

		err = types.CheckScenarioTopology(from, scn.annotations, topoName)
	} else {
		_, err = types.FromScenarioConfig(from, topoName)
	}

	if err != nil {
		return fmt.Errorf("%w: merging scenario %q: %w", ErrUnresolved, name, err)
	}

	return nil
}

// resolveTopology reads the topology prep names, checks its nodes and decodes
// it, untouched, into prep. The check fills in node defaults, so it runs on a
// decode of its own; a violation returns before the second decode.
func resolveTopology(prep *Prepared) error {
	cfg, err := readConfig(topologyKey, prep.TopologyName)
	if err != nil {
		return err
	}

	checked, err := decodeTopology(*cfg, prep.TopologyName)
	if err != nil {
		return err
	}

	if err := checkTopology(prep.TopologyName, checked); err != nil {
		return fmt.Errorf("%w: %w", ErrUnresolved, err)
	}

	prep.Topology, err = decodeTopology(*cfg, prep.TopologyName)

	return err
}

// decodeTopology decodes cfg, the stored topology named name, with its
// includes merged in. The error wraps [ErrUnresolved].
func decodeTopology(cfg store.Config, name string) (ifaces.TopologySpec, error) { //nolint:ireturn // interface
	spec, err := types.DecodeTopologyFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: decoding topology %q: %w", ErrUnresolved, name, err)
	}

	return spec, nil
}

// checkTopology checks the nodes of topo, the decoded topology named name, as
// [ifaces.TopologySpec.Init] does when an experiment is created or updated
// from it. Init also fills in node defaults, so topo must be a throwaway
// decode. The bridge it is given is not read by any check.
func checkTopology(name string, topo ifaces.TopologySpec) error {
	if err := checkNodeSections(topo); err != nil {
		return unusableTopology(name, err)
	}

	err := topo.Init("")
	if err == nil {
		return nil
	}

	// Init collects the violations in a multierror whose text spans several
	// lines; one line suits a JSON cause and a single-line log record.
	var errs *multierror.Error
	if errors.As(err, &errs) {
		errs.ErrorFormat = joinOnOneLine
	}

	return unusableTopology(name, err)
}

// checkNodeSections refuses what Init would crash on: an empty node entry, a
// node without a general section, a node minimega launches without a hardware
// section, and an empty interface entry. The schema refuses all four, but a
// topology stored without validation may have them.
func checkNodeSections(topo ifaces.TopologySpec) error {
	for i, node := range topo.Nodes() {
		if entry, _ := node.(*v1.Node); entry == nil {
			return fmt.Errorf("nodes[%d] is empty", i)
		}

		general, _ := node.General().(*v1.General)
		if general == nil {
			return fmt.Errorf("nodes[%d] has no general section", i)
		}

		hardware, _ := node.Hardware().(*v1.Hardware)
		if hardware == nil && !node.External() {
			return fmt.Errorf("node %s (nodes[%d]) has no hardware section", general.Hostname(), i)
		}

		for j, iface := range node.Network().Interfaces() {
			if entry, _ := iface.(*v1.Interface); entry == nil {
				return fmt.Errorf("node %s (nodes[%d]): interfaces[%d] is empty", general.Hostname(), i, j)
			}
		}
	}

	return nil
}

// unusableTopology wraps err, why no experiment can use the topology named
// name.
func unusableTopology(name string, err error) error {
	return fmt.Errorf("topology %q cannot be used in an experiment: %w", name, err)
}

// joinOnOneLine formats the errors a multierror collected on one line.
func joinOnOneLine(errs []error) string {
	texts := make([]string, len(errs))
	for i, err := range errs {
		texts[i] = err.Error()
	}

	return strings.Join(texts, "; ")
}

// CheckConfig checks cfg, a schema-checked config, for what no experiment
// could use, so a config dry run and the upsert refuse it before it is
// stored. Only a Topology has such checks, on the nodes it defines itself: an
// included topology may not be stored yet. A topology that does not decode is
// refused too. Configs of other kinds pass.
func CheckConfig(cfg store.Config) error {
	if cfg.Kind != topologyKind {
		return nil
	}

	var spec v1.TopologySpec
	if err := mapstructure.WeakDecode(cfg.Spec, &spec); err != nil {
		return fmt.Errorf("decoding topology %q: %w", cfg.Metadata.Name, err)
	}

	return checkTopology(cfg.Metadata.Name, &spec)
}

// resolveScenario reads the scenario prep names, decodes it into prep and
// merges in its fromScenario apps. checkScenarioTopology requires the
// scenario's topology annotation to name prep's topology.
func resolveScenario(prep *Prepared, checkScenarioTopology bool) error {
	cfg, err := readConfig(scenarioKey, prep.ScenarioName)
	if err != nil {
		return err
	}

	if checkScenarioTopology {
		err = scenarioMatchesTopology(prep.ScenarioName, cfg.Metadata.Annotations, prep.TopologyName)
		if err != nil {
			return err
		}
	}

	prep.Scenario, err = types.DecodeScenarioFromConfig(*cfg)
	if err != nil {
		return fmt.Errorf("%w: decoding scenario %q: %w", ErrUnresolved, prep.ScenarioName, err)
	}

	err = types.MergeScenariosForTopology(prep.Scenario, prep.TopologyName)
	if err != nil {
		return fmt.Errorf("%w: merging scenario %q: %w", ErrUnresolved, prep.ScenarioName, err)
	}

	return nil
}

// readConfig reads the config of the given kind and name from the store. The
// error wraps [ErrUnresolved]: the etcd store does not mark a missing config,
// so a missing config and a failed read cannot be told apart.
func readConfig(kind, name string) (*store.Config, error) {
	cfg, err := store.NewConfig(kind + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %q: %w", ErrUnresolved, kind, name, err)
	}

	if err := store.Get(cfg); err != nil {
		return nil, fmt.Errorf("%w: %s %q: %w", ErrUnresolved, kind, name, err)
	}

	return cfg, nil
}

// scenarioMatchesTopology checks that the scenario named name, which has the
// given annotations, is meant for the topology named topoName, as creating an
// experiment requires.
func scenarioMatchesTopology(name string, annotations store.Annotations, topoName string) error {
	annotated, ok := annotations[topologyKey]
	if !ok {
		return fmt.Errorf("%w: scenario %q has no %s annotation", ErrUnresolved, name, topologyKey)
	}

	if !strings.Contains(annotated, topoName) {
		return fmt.Errorf(
			"%w: scenario %q is annotated for topology %q, not %q",
			ErrUnresolved, name, annotated, topoName,
		)
	}

	return nil
}

// experimentAnnotation returns the value of the annotation key on exp, or ""
// when exp is nil or has no such annotation.
func experimentAnnotation(exp *types.Experiment, key string) string {
	if exp == nil {
		return ""
	}

	return exp.Metadata.Annotations[key]
}

// vlanAliases maps the VLAN alias of every interface in topo, external nodes
// included, to its ID in mappings, or to 0 so minimega picks one. Rebuilding
// from the topology drops aliases a changed topology no longer uses.
func vlanAliases(topo ifaces.TopologySpec, mappings map[string]int) map[string]int {
	aliases := make(map[string]int)

	// TODO: only consider nodes schedulable by minimega? Or should HIL nodes
	// be taken into account here still as well?
	for _, node := range topo.Nodes() {
		for _, iface := range node.Network().Interfaces() {
			alias := iface.VLAN()
			aliases[alias] = mappings[alias]
		}
	}

	return aliases
}

// nodeSchedules maps each non-external node in topo that mappings schedules
// to its cluster host. Rebuilding from the topology drops hostnames a changed
// topology no longer has.
func nodeSchedules(topo ifaces.TopologySpec, mappings map[string]string) map[string]string {
	schedules := make(map[string]string)

	for _, node := range topo.Nodes() {
		if node.External() {
			continue
		}

		hostname := node.General().Hostname()

		if host, ok := mappings[hostname]; ok {
			schedules[hostname] = host
		}
	}

	return schedules
}
