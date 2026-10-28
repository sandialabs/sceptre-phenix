package builder

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"phenix/store"
	"phenix/types/version"
	"phenix/util/common"
)

// Layout constants for generated documents. Positions are deterministic so a
// config always imports to the same canvas.
const (
	defaultGridSize    = 16.0
	layoutColumns      = 6
	layoutSpacingX     = 320.0
	layoutSpacingY     = 240.0
	layoutSwitchOffset = 160.0
)

// maxIncludeResolutions bounds how many included topologies one generation
// resolves, so a pathological include graph (each topology including the
// next one several times) cannot cause an unbounded number of reads.
const maxIncludeResolutions = 100

// maxListedIncludes is the most included topologies a warning names.
const maxListedIncludes = 8

// ErrUnsupportedKind is returned by [FromConfig] for configs that are neither a
// Topology nor an Experiment.
var ErrUnsupportedKind = errors.New("unsupported config kind for builder document")

// ErrUnsupportedVersion is returned by [FromConfig] for configs whose
// apiVersion is not the stored version of their kind.
var ErrUnsupportedVersion = errors.New("unsupported config version for builder document")

// ErrCombineExperiment is returned by [FromConfig] for an Experiment config
// with [WithCombinedIncludes].
var ErrCombineExperiment = errors.New("the Builder can combine only the included topologies of a topology")

// TopologyLoader reads the stored Topology config an includeTopologies entry
// names. It returns an error when there is no such topology or the caller may
// not read it. Generation reports the error as a warning and continues.
type TopologyLoader func(name string) (*store.Config, error)

// GenerateOption configures [FromConfig].
type GenerateOption func(*generator)

// WithTopologyLoader makes [FromConfig] resolve includeTopologies through load,
// recursively and without cycles, as phenix merges them, and mark the included
// devices with [Device.IncludedFrom]. Without a loader, [FromConfig] keeps the
// references only.
func WithTopologyLoader(load TopologyLoader) GenerateOption {
	return func(g *generator) {
		g.load = load
	}
}

// ScenarioResolver reports whether the Scenario config named name is stored
// on this server for the caller: it exists, and the caller may list it. It
// returns an error only when it cannot tell, for example when it cannot read
// the store. Generation then fails with that error.
type ScenarioResolver func(name string) (bool, error)

// WithScenarioResolver makes [FromConfig] list the Scenario config that an
// Experiment config names, when resolve finds it. Else generation gives a
// warning. The document never takes scenario content.
func WithScenarioResolver(resolve ScenarioResolver) GenerateOption {
	return func(g *generator) {
		g.resolveScenario = resolve
	}
}

// WithCombinedIncludes makes [FromConfig] copy the devices of included
// topologies into the document as its own (see [Document.CombineIncludes]).
// Without [WithTopologyLoader] it copies nothing. [FromConfig] refuses an
// Experiment config with [ErrCombineExperiment]: its topology holds the nodes.
func WithCombinedIncludes() GenerateOption {
	return func(g *generator) {
		g.combine = true
	}
}

// FromConfig generates a builder document from a validated Topology or
// Experiment config. It copies every node spec into its device, and drops from
// an experiment what phenix and its apps added. It makes a network and a
// switch for each VLAN. Generation is deterministic, so [Source.ImportedAt]
// stays empty. The returned warnings are also stored on the [Source].
func FromConfig(config store.Config, options ...GenerateOption) (*Document, []string, error) {
	kind, err := canonicalKind(config.Kind)
	if err != nil {
		return nil, nil, err
	}

	spec, err := specForConfig(config, kind)
	if err != nil {
		return nil, nil, err
	}

	gen := &generator{ //nolint:exhaustruct // accumulators fill in as nodes are read
		doc:       NewDocument(config.Metadata.Name),
		networks:  map[string]*Network{},
		hostnames: map[string]string{},
		included:  map[string]int{},
	}

	for _, option := range options {
		option(gen)
	}

	if gen.combine && kind == kindExperiment {
		return nil, nil, ErrCombineExperiment
	}

	switch kind {
	case kindTopology:
		gen.doc.Source = &Source{ //nolint:exhaustruct // warnings are attached once generation finishes
			Kind:       SourceKindTopology,
			Name:       config.Metadata.Name,
			APIVersion: config.Version,
		}

		gen.importTopology(spec, config.Metadata.Name)
	case kindExperiment:
		gen.doc.Source = &Source{ //nolint:exhaustruct // warnings are attached once generation finishes
			Kind:       SourceKindExperiment,
			Name:       config.Metadata.Name,
			APIVersion: config.Version,
			Topology:   config.Metadata.Annotations["topology"],
		}

		topology, err := normalizeSpecMap(spec["topology"])
		if err != nil {
			return nil, nil, fmt.Errorf("reading experiment topology: %w", err)
		}

		dropAppInjections(topology, experimentBaseDir(spec, config.Metadata.Name))
		defaultBridge, _ := spec["defaultBridge"].(string)
		dropExperimentResidue(topology, defaultBridge)
		gen.importTopology(topology, config.Metadata.Annotations["topology"])
		gen.importVLANs(spec["vlans"])

		if err := gen.importScenario(spec["scenario"], config.Metadata.Annotations["scenario"]); err != nil {
			return nil, nil, err
		}

		gen.warnUnrepresentedExperimentFields(spec)
	}

	gen.importAnnotations(config.Metadata.Annotations)

	digest, err := ImportDigest(config)
	if err != nil {
		return nil, nil, err
	}

	gen.doc.Source.Digest = digest
	gen.doc.Source.UpdatedAt = config.Metadata.Updated

	gen.finish()

	if gen.combine {
		gen.doc.CombineIncludes()
	}

	if err := gen.doc.Validate(); err != nil {
		return nil, nil, fmt.Errorf("the imported document is not valid: %w", err)
	}

	return gen.doc, gen.warnings, nil
}

const (
	kindTopology   = "Topology"
	kindExperiment = "Experiment"
)

type generator struct {
	doc      *Document
	warnings []string

	devices  []Node
	switches []Node
	// networks is keyed by the exact VLAN name: minimega VLAN aliases are
	// case sensitive, so VLANs differing only by case are different VLANs.
	networks map[string]*Network
	// folded maps each case-folded VLAN name onto the first VLAN name seen
	// with it, to warn about the others.
	folded map[string]string
	edges  []Edge
	// members counts the interfaces attached to each network.
	members map[string]int

	// load resolves included topologies. A nil load leaves them unresolved.
	load TopologyLoader
	// resolveScenario tells whether the scenario that an experiment names is
	// stored. A nil resolveScenario lists none.
	resolveScenario ScenarioResolver
	// combine makes the devices of included topologies the document's own
	// once it is generated (see [WithCombinedIncludes]).
	combine bool
	// hostnames maps the case-folded hostname of every device added so far to
	// the included topology defining it, or "" for the source's own devices.
	hostnames map[string]string
	// included counts the devices marked as included, per defining topology.
	included map[string]int
	// resolutions counts included topologies read, see maxIncludeResolutions.
	resolutions int
	// includedBy lists, for every included topology reached, the topologies
	// that include it, in the order reached. includeOrder is the order in
	// which they were first reached.
	includedBy   map[string][]string
	includeOrder []string
	// unreadable collects the includes that could not be used.
	unreadable []IncludeError
}

// IncludeError is an includeTopologies entry that could not be resolved.
type IncludeError struct {
	// Name is the included topology's name, as the entry gives it.
	Name string
	// Err says why: the loader's error, or [ErrTooManyIncludes].
	Err error
}

// ErrTooManyIncludes reports includes left unresolved because resolution
// stopped at maxIncludeResolutions.
var ErrTooManyIncludes = fmt.Errorf("more than %d included topologies", maxIncludeResolutions)

// HostnameClash is a node of an included topology whose hostname the
// including topology also defines. phenix refuses to merge such a topology.
type HostnameClash struct {
	// Include names the included topology defining the node.
	Include string
	// Hostname is the node's hostname in the included topology.
	Hostname string
}

// RefusedHostname is a node of an included topology whose hostname phenix
// refuses when it creates or updates an experiment (see [checkHostname]).
type RefusedHostname struct {
	// Include names the included topology defining the node.
	Include string
	// Reason is phenix's, which names the hostname.
	Reason string
}

// IncludeReport is what [CheckIncludes] found.
type IncludeReport struct {
	// Unreadable lists the included topologies that could not be read.
	Unreadable []IncludeError
	// Clashes lists the nodes of included topologies that duplicate a
	// hostname of the topology itself.
	Clashes []HostnameClash
	// Refused lists the nodes of included topologies whose hostname phenix
	// refuses in an experiment.
	Refused []RefusedHostname
}

// CheckIncludes resolves the includes of the topology spec name as generation
// does, and reports what stops phenix from merging them now: unreadable
// topologies, and included hostnames that the spec defines or phenix refuses.
func CheckIncludes(name string, spec map[string]any, load TopologyLoader) (IncludeReport, error) {
	normalized, err := normalizeSpecMap(spec)
	if err != nil {
		return IncludeReport{}, fmt.Errorf("reading topology spec: %w", err)
	}

	gen := &generator{load: load} //nolint:exhaustruct // only include resolution is used

	resolved := gen.resolveAll(includeNames(normalized), name)
	report := IncludeReport{Unreadable: gen.unreadable, Clashes: nil, Refused: nil}

	own := map[string]bool{}
	for _, hostname := range specHostnames(normalized) {
		own[foldKey(hostname)] = true
	}

	for _, topology := range resolved {
		for _, hostname := range specHostnames(topology.spec) {
			if own[foldKey(hostname)] {
				report.Clashes = append(report.Clashes, HostnameClash{Include: topology.name, Hostname: hostname})
			}
		}

		startedNodes(topology.spec, func(_ int, hostname, osType string) {
			if _, err := checkHostname(hostname, osType); err != nil {
				report.Refused = append(report.Refused, RefusedHostname{Include: topology.name, Reason: err.Error()})
			}
		})
	}

	return report, nil
}

// includedTopology is one resolved included topology.
type includedTopology struct {
	name string
	spec map[string]any
}

func canonicalKind(kind string) (string, error) {
	switch {
	case strings.EqualFold(kind, kindTopology):
		return kindTopology, nil
	case strings.EqualFold(kind, kindExperiment):
		return kindExperiment, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedKind, kind)
	}
}

// specForConfig returns the config spec. phenix has no config upgraders, so
// a config at another apiVersion than the stored one is refused.
func specForConfig(config store.Config, kind string) (map[string]any, error) {
	if config.APIVersion() != version.StoredVersion[kind] {
		return nil, fmt.Errorf("%w: %s %s", ErrUnsupportedVersion, kind, config.APIVersion())
	}

	spec, err := normalizeSpecMap(config.Spec)
	if err != nil {
		return nil, fmt.Errorf("reading %s spec: %w", kind, err)
	}

	if spec == nil {
		spec = map[string]any{}
	}

	return spec, nil
}

func (g *generator) warnf(format string, args ...any) {
	g.warnings = append(g.warnings, fmt.Sprintf(format, args...))
}

// importTopology imports a topology spec named name. It adds the devices of a
// topology's includes after its own, in phenix merge order. For an experiment,
// which already holds them, it marks them by hostname instead, never a
// hostname the topology defines itself.
func (g *generator) importTopology(spec map[string]any, name string) {
	if spec == nil {
		return
	}

	includes := g.recordIncludes(spec)

	if g.doc.Source.Kind == SourceKindExperiment {
		owners, resolved := g.experimentIncludes(spec, includes, name)

		g.importNodes(spec, "", owners)
		g.warnIncluded("The Builder marked %s as included from", "experiment node", resolved)

		return
	}

	g.importNodes(spec, "", nil)
	g.importIncludes(includes, name)
}

// recordIncludes keeps the topology's includeTopologies on the document's
// source, so publishing writes them back, and returns them.
func (g *generator) recordIncludes(spec map[string]any) []string {
	for i, value := range includeEntries(spec) {
		name, ok := value.(string)
		if !ok || !validIncludeName(name) {
			g.warnf("included topology at index %d has an invalid name. The Builder skipped it", i)

			continue
		}

		g.doc.Source.IncludeTopologies = append(g.doc.Source.IncludeTopologies, name)
	}

	return g.doc.Source.IncludeTopologies
}

func validIncludeName(name string) bool {
	return strings.TrimSpace(name) != "" && !hasNameSpace(name)
}

// importIncludes adds the devices of a topology's included topologies, and
// records the included topologies whose devices it could not add.
func (g *generator) importIncludes(includes []string, root string) {
	if len(includes) == 0 {
		return
	}

	if g.load == nil {
		g.doc.Source.UnresolvedIncludes = unresolvedIncludes(includes, root, nil)

		g.warnf(
			"topology includes %d other topologies that the Builder did not resolve. "+
				"The diagram does not show their nodes, but publishing keeps the references",
			len(includes),
		)

		if g.combine {
			g.warnNotCombined()
		}

		return
	}

	resolved := g.resolveAll(includes, root)
	g.doc.Source.UnresolvedIncludes = unresolvedIncludes(includes, root, resolved)

	for _, problem := range g.unreadable {
		if errors.Is(problem.Err, ErrTooManyIncludes) {
			g.warnf(
				"the Builder resolved only the first %d included topologies. "+
					"The diagram does not show the nodes of %q and later includes",
				maxIncludeResolutions, problem.Name,
			)

			continue
		}

		g.warnf("the Builder could not read included topology %q: %v. The diagram does not show its nodes", problem.Name, problem.Err)
	}

	for _, topology := range resolved {
		g.importNodes(topology.spec, topology.name, nil)
	}

	if g.combine {
		g.warnCombined(resolved)
		g.warnNotCombined()

		return
	}

	g.warnIncluded("The Builder added %s from", "node", resolved)
}

// unresolvedIncludes lists, in first-met order, the includes of root and of
// resolved topologies whose devices are not in the document. It reads only the
// resolved specs, so the list is complete even when resolution stopped early.
func unresolvedIncludes(includes []string, root string, resolved []includedTopology) []string {
	specs := make(map[string]map[string]any, len(resolved))
	for _, topology := range resolved {
		specs[topology.name] = topology.spec
	}

	var (
		unresolved []string
		seen       = map[string]bool{root: true}
		walk       func(names []string)
	)

	walk = func(names []string) {
		for _, name := range names {
			if seen[name] {
				continue
			}

			seen[name] = true

			spec, ok := specs[name]
			if !ok {
				unresolved = append(unresolved, name)

				continue
			}

			walk(includeNames(spec))
		}
	}

	walk(includes)

	return unresolved
}

// IncludeCount returns the number of valid includeTopologies entries of a
// Topology config, and 0 for any other kind or an unreadable spec.
func IncludeCount(config store.Config) int {
	kind, err := canonicalKind(config.Kind)
	if err != nil || kind != kindTopology {
		return 0
	}

	// An import refuses a config at another version, so it has no includes.
	if config.APIVersion() != version.StoredVersion[kindTopology] {
		return 0
	}

	return len(includeNames(config.Spec))
}

// experimentIncludes maps the case-folded hostname of each node of the
// experiment's included topologies to its topology, except the hostnames root
// defines, and returns the resolved topologies.
func (g *generator) experimentIncludes(
	spec map[string]any, includes []string, root string,
) (map[string]string, []includedTopology) {
	if len(includes) == 0 {
		return nil, nil
	}

	if g.load == nil {
		g.warnf(
			"the experiment's topology includes %d other topologies that the Builder did not resolve. "+
				"The diagram shows the nodes that they added as the topology's own nodes",
			len(includes),
		)

		return nil, nil
	}

	own := g.rootHostnames(root)
	resolved := g.resolveAll(includes, root)
	owners := map[string]string{}

	for _, topology := range resolved {
		for _, hostname := range specHostnames(topology.spec) {
			key := foldKey(hostname)
			owner, taken := owners[key]

			switch {
			case own[key]:
				g.warnHostnameClash(topology.name, hostname, "", "")
			case taken && owner != topology.name:
				g.warnHostnameClash(topology.name, hostname, owner, "")
			case !taken:
				owners[key] = topology.name
			}
		}
	}

	g.markUnreadable(spec, root, own, owners)

	return owners, resolved
}

// markUnreadable marks the experiment nodes that neither root nor a readable
// include defines as from an unreadable include, so a publish leaves them out.
// When root is unreadable (own is nil) it marks none, because its own nodes
// look the same.
func (g *generator) markUnreadable(spec map[string]any, root string, own map[string]bool, owners map[string]string) {
	if len(g.unreadable) == 0 {
		return
	}

	first := g.unreadable[0].Name
	marked := 0

	if own != nil {
		for _, hostname := range specHostnames(spec) {
			key := foldKey(hostname)
			if _, taken := owners[key]; !taken && !own[key] {
				owners[key] = first
				marked++
			}
		}
	}

	reported := map[string]bool{}

	for _, problem := range g.unreadable {
		if reported[problem.Name] {
			continue
		}

		reported[problem.Name] = true
		reason := fmt.Sprintf("the Builder could not read included topology %q: %v", problem.Name, problem.Err)

		switch {
		case own == nil:
			g.warnf(
				"%s. Without the topology that the experiment was created from, the Builder cannot distinguish "+
					"its nodes in the experiment from the topology's own nodes. Thus the diagram shows them "+
					"as the topology's own nodes, and publishing the topology copies them into it",
				reason,
			)
		case problem.Name != first:
			g.warnf(
				"%s. The Builder cannot distinguish its nodes in the experiment from those of included topology %q, "+
					"which the Builder also could not read. Thus the Builder marks them as included from %q",
				reason, first, first,
			)
		case marked == 0:
			g.warnf(
				"%s. Topology %q and its readable included topologies define every experiment node. "+
					"Thus the Builder marks no node as included from it",
				reason, root,
			)
		default:
			g.warnf(
				"%s. The Builder marks the experiment's %s that neither topology %q nor a readable included topology "+
					"defines as included from it. The diagram shows each such node read only, "+
					"and publishing does not copy it into the topology",
				reason, countOf(marked, "node"), root,
			)
		}
	}
}

// rootHostnames returns the case-folded hostnames that root, the topology an
// experiment was created from, defines now. When root cannot be read it
// warns and returns nil: nodes are then matched to included topologies by
// hostname alone.
func (g *generator) rootHostnames(root string) map[string]bool {
	if root == "" {
		g.warnf(
			"the experiment does not name the topology that it was created from. " +
				"Thus the Builder matches its nodes to included topologies only by hostname",
		)

		return nil
	}

	spec, err := g.loadIncluded(root)
	if err != nil {
		g.warnf(
			"the Builder could not read topology %q, which the experiment was created from: %v. "+
				"Thus the Builder matches the experiment nodes to included topologies only by hostname",
			root, err,
		)

		return nil
	}

	own := map[string]bool{}
	for _, hostname := range specHostnames(spec) {
		own[foldKey(hostname)] = true
	}

	return own
}

// warnHostnameClash reports a node of included topology from whose hostname
// is already defined: by the topology itself when owner is "", or by
// included topology owner. outcome, when set, says what the diagram shows.
func (g *generator) warnHostnameClash(from, hostname, owner, outcome string) {
	definedBy := "the topology itself"
	if owner != "" {
		definedBy = fmt.Sprintf("included topology %q", owner)
	}

	message := fmt.Sprintf(
		"included topology %q defines node %q, which duplicates a hostname in %s. "+
			"phenix rejects duplicate hostnames. Rename one of the two nodes",
		from, hostname, definedBy,
	)

	if outcome != "" {
		message += ". " + outcome
	}

	g.warnings = append(g.warnings, message)
}

// specHostnames lists the hostnames of a topology spec's nodes, leaving out
// nodes without one.
func specHostnames(spec map[string]any) []string {
	nodes, _ := spec[keyNodes].([]any)
	hostnames := make([]string, 0, len(nodes))

	for _, entry := range nodes {
		nodeSpec, _ := entry.(map[string]any)

		if hostname := specString(nodeSpec, "general", "hostname"); strings.TrimSpace(hostname) != "" {
			hostnames = append(hostnames, hostname)
		}
	}

	return hostnames
}

// includeEntries returns the includeTopologies entries of a spec: as stored
// ([]any), or as text ([]string), which is how a projected spec (see
// [Document.ToTopology]) holds them.
func includeEntries(spec map[string]any) []any {
	switch typed := spec["includeTopologies"].(type) {
	case []any:
		return typed
	case []string:
		values := make([]any, len(typed))
		for i, name := range typed {
			values[i] = name
		}

		return values
	default:
		return nil
	}
}

// includeNames lists the valid includeTopologies entries of a spec.
func includeNames(spec map[string]any) []string {
	values := includeEntries(spec)
	names := make([]string, 0, len(values))

	for _, value := range values {
		if name, ok := value.(string); ok && validIncludeName(name) {
			names = append(names, name)
		}
	}

	return names
}

// resolveAll resolves the includes of the topology named root (see
// resolveIncludes), then warns once about each topology included more than
// once, which phenix merges each time and so rejects for its duplicated
// hostnames.
func (g *generator) resolveAll(includes []string, root string) []includedTopology {
	g.includedBy = map[string][]string{}
	g.includeOrder = nil

	visited := map[string]bool{}
	if root != "" {
		visited[root] = true
	}

	resolved := g.resolveIncludes(includes, root, visited)

	for _, name := range g.includeOrder {
		if parents := g.includedBy[name]; len(parents) > 1 {
			g.warnf(
				"included topology %q occurs more than once (through %s). "+
					"phenix rejects the duplicate hostnames that this causes. Thus the diagram shows its nodes only once",
				name, includers(parents),
			)
		}
	}

	return resolved
}

// includers names the topologies that include one, as "a and b", or
// "a twice" when one topology includes it twice.
func includers(parents []string) string {
	var (
		order  []string
		counts = map[string]int{}
	)

	for _, parent := range parents {
		if parent == "" {
			parent = "the experiment's topology"
		}

		if counts[parent] == 0 {
			order = append(order, parent)
		}

		counts[parent]++
	}

	parts := make([]string, 0, len(order))

	for _, parent := range order {
		switch count := counts[parent]; count {
		case 1:
			parts = append(parts, parent)
		case 2: //nolint:mnd // "twice" reads better than "2 times"
			parts = append(parts, parent+" twice")
		default:
			parts = append(parts, fmt.Sprintf("%s %d times", parent, count))
		}
	}

	return listOf(parts)
}

// resolveIncludes reads included topologies depth first, the order in which
// phenix appends their nodes. It skips and reports a cycle (visited holds the
// current path), resolves a topology reached twice once, and records an
// unreadable one for the caller.
func (g *generator) resolveIncludes(names []string, parent string, visited map[string]bool) []includedTopology {
	var resolved []includedTopology

	for _, name := range names {
		if visited[name] {
			g.warnf(
				"included topology %q includes itself through a cycle, which phenix rejects. "+
					"The Builder skipped the repeated include",
				name,
			)

			continue
		}

		if parents, reached := g.includedBy[name]; reached {
			g.includedBy[name] = append(parents, parent)

			continue
		}

		if g.resolutions >= maxIncludeResolutions {
			g.unreadable = append(g.unreadable, IncludeError{Name: name, Err: ErrTooManyIncludes})

			return resolved
		}

		g.resolutions++
		g.includedBy[name] = []string{parent}
		g.includeOrder = append(g.includeOrder, name)

		spec, err := g.loadIncluded(name)
		if err != nil {
			g.unreadable = append(g.unreadable, IncludeError{Name: name, Err: err})

			continue
		}

		resolved = append(resolved, includedTopology{name: name, spec: spec})

		nested := includeNames(spec)
		if len(nested) == 0 {
			continue
		}

		path := maps.Clone(visited)
		path[name] = true

		resolved = append(resolved, g.resolveIncludes(nested, name, path)...)
	}

	return resolved
}

// loadIncluded reads an included topology's spec in the latest stored
// representation.
func (g *generator) loadIncluded(name string) (map[string]any, error) {
	config, err := g.load(name)
	if err != nil {
		return nil, err
	}

	if config == nil {
		return nil, errors.New("the loader returned no topology")
	}

	if kind, err := canonicalKind(config.Kind); err != nil || kind != kindTopology {
		return nil, fmt.Errorf("%s is not a topology", config.Kind)
	}

	return specForConfig(*config, kindTopology)
}

// warnIncluded summarizes the devices marked as included: how many, and from
// which of the resolved topologies. lead is the start of the sentence, with a
// %s for the count of noun.
func (g *generator) warnIncluded(lead, noun string, resolved []includedTopology) {
	if len(resolved) == 0 {
		return
	}

	total, parts := g.includedCounts(resolved)

	g.warnf(
		"%s included %s %s. The diagram shows them read only. Edit them in their own topology. "+
			"Publishing keeps includeTopologies and does not copy them.",
		fmt.Sprintf(lead, countOf(total, noun)), pluralOf(len(parts), "topology", "topologies"), listOf(parts),
	)
}

// warnCombined summarizes the devices copied from the resolved included
// topologies, which [Document.CombineIncludes] then makes the document's own.
func (g *generator) warnCombined(resolved []includedTopology) {
	if len(resolved) == 0 {
		return
	}

	total, parts := g.includedCounts(resolved)

	g.warnf(
		"The Builder copied %s from included %s %s. They are ordinary nodes of this diagram now: "+
			"changes here do not reach %s, and later changes there do not reach this diagram.",
		countOf(total, "node"), pluralOf(len(parts), "topology", "topologies"), listOf(parts),
		pluralOf(len(parts), "that topology", "those topologies"),
	)
}

// warnNotCombined names the included topologies a combined document still
// includes, because their devices could not be copied into it.
func (g *generator) warnNotCombined() {
	names := g.doc.Source.UnresolvedIncludes
	if len(names) == 0 {
		return
	}

	listed := names
	if len(names) > maxListedIncludes {
		listed = append(slices.Clip(names[:maxListedIncludes]), fmt.Sprintf("%d more", len(names)-maxListedIncludes))
	}

	if len(names) == 1 {
		g.warnf(
			"The Builder did not combine included topology %s. It stays in includeTopologies, and publishing keeps the reference.",
			names[0],
		)

		return
	}

	g.warnf(
		"The Builder did not combine included topologies %s. They stay in includeTopologies, and publishing keeps the references.",
		listOf(listed),
	)
}

// includedCounts returns how many devices are marked as included in all, and
// each of the resolved topologies with its count, as "name (2 nodes)".
func (g *generator) includedCounts(resolved []includedTopology) (int, []string) {
	var (
		total int
		parts []string
		seen  = map[string]bool{}
	)

	for _, topology := range resolved {
		if seen[topology.name] {
			continue
		}

		seen[topology.name] = true
		count := g.included[topology.name]
		total += count
		parts = append(parts, fmt.Sprintf("%s (%s)", topology.name, countOf(count, "node")))
	}

	return total, parts
}

func countOf(n int, noun string) string {
	return fmt.Sprintf("%d %s", n, pluralOf(n, noun, noun+"s"))
}

func pluralOf(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}

// listOf joins items as "a", "a and b", or "a, b and c".
func listOf(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}

	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// importNodes adds the devices of one topology spec. from is "" for the
// source topology's own nodes, and names the included topology defining them
// otherwise. owners, for an experiment, maps hostnames of nodes phenix merged
// in from included topologies onto the topology defining them.
func (g *generator) importNodes(spec map[string]any, from string, owners map[string]string) {
	nodes, _ := spec[keyNodes].([]any)
	seen := map[string]int{}

	where := func(i int) string {
		if from == "" {
			return fmt.Sprintf("topology node at index %d", i)
		}

		return fmt.Sprintf("node at index %d of included topology %q", i, from)
	}

	for i, entry := range nodes {
		nodeSpec, ok := entry.(map[string]any)
		if !ok {
			g.warnf("%s is not an object. The Builder skipped it", where(i))

			continue
		}

		hostname := specString(nodeSpec, "general", "hostname")
		if strings.TrimSpace(hostname) == "" {
			g.warnf("%s has no hostname. The Builder skipped it", where(i))

			continue
		}

		if prev, dup := seen[foldKey(hostname)]; dup {
			g.warnf(
				"%s duplicates the hostname of the node at index %d (%q). The Builder skipped it",
				where(i), prev, hostname,
			)

			continue
		}

		seen[foldKey(hostname)] = i

		if owner, dup := g.hostnames[foldKey(hostname)]; dup {
			g.warnHostnameClash(from, hostname, owner, "The diagram does not show the included node")

			continue
		}

		includedFrom := from
		if owners != nil {
			includedFrom = owners[foldKey(hostname)]
		}

		g.addDevice(hostname, nodeSpec, includedFrom)
	}
}

func (g *generator) addDevice(hostname string, spec map[string]any, includedFrom string) {
	g.hostnames[foldKey(hostname)] = includedFrom

	if includedFrom != "" {
		g.included[includedFrom]++
	}

	device := &Device{ //nolint:exhaustruct // a custom icon and colors are the editor's to set
		Hostname:     hostname,
		IconKey:      iconKeyForSpec(spec),
		Spec:         spec,
		Interfaces:   []InterfaceHandle{},
		IncludedFrom: includedFrom,
	}

	node := Node{ //nolint:exhaustruct // only device nodes carry a device payload
		ID:       DeviceNodeID(hostname),
		Kind:     NodeKindDevice,
		Label:    hostname,
		Position: Position{X: 0, Y: 0},
		Device:   device,
	}

	seen := map[string]bool{}

	for index, iface := range specNodeInterfaces(spec) {
		name := interfaceName(iface)
		if strings.TrimSpace(name) == "" {
			g.warnf(
				"interface at index %d of node %q has no name. The Builder keeps it, but you cannot connect it",
				index, hostname,
			)

			continue
		}

		if seen[foldKey(name)] {
			g.warnf(
				"node %q declares interface %q more than once. You can connect only the first one",
				hostname, name,
			)

			continue
		}

		seen[foldKey(name)] = true

		handle := InterfaceHandle{
			ID:    InterfaceHandleID(hostname, name, index),
			Name:  name,
			Index: index,
		}

		device.Interfaces = append(device.Interfaces, handle)

		vlan := interfaceVLAN(iface)
		if strings.TrimSpace(vlan) == "" {
			continue
		}

		// A network name must not hold whitespace, so this VLAN gets no
		// network, as an invalid alias gets none.
		if hasNameSpace(vlan) {
			g.warnf(
				"VLAN %q of interface %q of node %q contains whitespace. The Builder keeps the interface, but does not connect it",
				vlan, name, hostname,
			)

			continue
		}

		network := g.network(vlan)

		g.connect(node.ID, handle, network)
	}

	g.devices = append(g.devices, node)
}

// network returns the canonical network for a VLAN name, and makes it when
// needed.
// VLAN names are compared exactly, as minimega compares them, so a VLAN that
// differs from another only by case gets a network of its own, with a warning
// in case the difference is a typo.
func (g *generator) network(name string) *Network {
	if existing, ok := g.networks[name]; ok {
		return existing
	}

	if g.folded == nil {
		g.folded = map[string]string{}
	}

	if other, clash := g.folded[foldKey(name)]; clash {
		g.warnf(
			"VLAN %q differs only by case from VLAN %q. minimega treats them as different VLANs. "+
				"Thus they are separate networks",
			name, other,
		)
	} else {
		g.folded[foldKey(name)] = name
	}

	network := &Network{ //nolint:exhaustruct // aliases and presentation are optional
		ID:   NetworkID(name),
		Name: name,
	}
	g.networks[name] = network

	return network
}

func (g *generator) connect(nodeID string, handle InterfaceHandle, network *Network) {
	switchID := SwitchNodeID(network.Name)

	if g.members == nil {
		g.members = map[string]int{}
	}

	g.members[network.ID]++

	g.edges = append(g.edges, Edge{ //nolint:exhaustruct // switch endpoints carry no handle
		ID:             EdgeID(nodeID, handle.ID, switchID, ""),
		SourceNodeID:   nodeID,
		SourceHandleID: handle.ID,
		TargetNodeID:   switchID,
		NetworkID:      network.ID,
	})
}

// importVLANs imports experiment VLAN aliases, creating canonical networks for
// aliases that no interface references.
func (g *generator) importVLANs(value any) {
	vlans, err := normalizeSpecMap(value)
	if err != nil || vlans == nil {
		return
	}

	if minimum, ok := toInt(vlans["min"]); ok && minimum != 0 {
		g.warnf("the builder document does not hold the minimum (%d) of the experiment VLAN range", minimum)
	}

	if maximum, ok := toInt(vlans["max"]); ok && maximum != 0 {
		g.warnf("the builder document does not hold the maximum (%d) of the experiment VLAN range", maximum)
	}

	aliases, ok := vlans["aliases"].(map[string]any)
	if !ok {
		return
	}

	for _, name := range slices.Sorted(maps.Keys(aliases)) {
		if name == "" {
			// phenix records the interfaces without a VLAN under "" (see
			// v1.ExperimentSpec.Init).
			continue
		}

		if strings.TrimSpace(name) == "" || hasNameSpace(name) {
			g.warnf("VLAN alias name %q is invalid. The Builder dropped it", name)

			continue
		}

		alias, ok := toInt(aliases[name])
		if !ok {
			g.warnf("VLAN alias for %q is not an integer. The Builder dropped it", name)

			continue
		}

		if alias == 0 {
			// phenix records unassigned VLANs with an alias of 0.
			continue
		}

		if alias < 1 || alias > maxVLANAlias {
			g.warnf("VLAN alias %d for %q is out of range (1-%d). The Builder dropped it", alias, name, maxVLANAlias)

			continue
		}

		network := g.network(name)
		value := alias
		network.Alias = &value
	}
}

// importScenario lists the Scenario config an experiment names in its
// "scenario" annotation when the resolver finds it. Else it gives a warning,
// because the document never holds the merged scenario content.
func (g *generator) importScenario(value any, name string) error {
	content, err := normalizeSpecMap(value)
	if err != nil {
		return fmt.Errorf("reading experiment scenario: %w", err)
	}

	if name == "" {
		if len(content) > 0 {
			g.warnf("the experiment's scenario is not a stored Scenario config. The Builder did not attach it")
		}

		return nil
	}

	if g.resolveScenario != nil && IsConfigName(name) {
		stored, lookupErr := g.resolveScenario(name)
		if lookupErr != nil {
			return fmt.Errorf("finding scenario %s: %w", name, lookupErr)
		}

		if stored {
			g.doc.Scenarios = []string{name}

			return nil
		}
	}

	g.warnf("the experiment's scenario %q is not a stored Scenario config. The Builder did not attach it", truncate(name))

	return nil
}

// builderAnnotationPrefix starts the config annotations the Builders keep for
// themselves: the legacy Builder's diagram (builder-xml, often large), the
// published document a topology names (builder-doc) and the record of a
// published experiment (builder-experiment).
const builderAnnotationPrefix = "builder-"

// maxListedAnnotations is the most annotation keys a warning names.
const maxListedAnnotations = 5

// IsBuilderAnnotation reports whether a config annotation is one the Builders
// keep for themselves, which a document never carries in
// [Source.Annotations].
func IsBuilderAnnotation(key string) bool {
	return strings.HasPrefix(key, builderAnnotationPrefix)
}

// importAnnotations keeps the source config's annotations, other than the
// Builders' own, on the document's source. In key order, it keeps those that
// fit the bounds [Document.Validate] puts on them, and warns about the rest.
func (g *generator) importAnnotations(annotations map[string]string) {
	var (
		kept    = map[string]string{}
		size    int
		dropped []string
	)

	for _, key := range slices.Sorted(maps.Keys(annotations)) {
		if IsBuilderAnnotation(key) {
			continue
		}

		value := annotations[key]

		if _, problem := annotationKeyProblem(key); problem != "" || len(kept) == MaxAnnotations ||
			size+len(key)+len(value) > MaxAnnotationBytes {
			dropped = append(dropped, strconv.Quote(truncate(key)))

			continue
		}

		kept[key] = value
		size += len(key) + len(value)
	}

	if len(kept) > 0 {
		g.doc.Source.Annotations = kept
	}

	if len(dropped) == 0 {
		return
	}

	listed := dropped
	if len(dropped) > maxListedAnnotations {
		listed = append(
			slices.Clip(dropped[:maxListedAnnotations]),
			fmt.Sprintf("%d more", len(dropped)-maxListedAnnotations),
		)
	}

	g.warnf(
		"The Builder did not keep %s %s of the source config. A builder document keeps at most %d annotations "+
			"of %d KiB in all. Each key is not blank, is at most %d bytes long, and has no control characters",
		pluralOf(len(dropped), "annotation", "annotations"), listOf(listed),
		MaxAnnotations, maxAnnotationKiB, MaxNameBytes,
	)
}

// experimentBaseDir returns the directory phenix keeps an experiment's files
// in: its baseDir, or the one phenix gives an experiment without one.
func experimentBaseDir(spec map[string]any, name string) string {
	if dir, _ := spec["baseDir"].(string); dir != "" {
		return dir
	}

	return common.PhenixBase + "/experiments/" + name
}

// dropAppInjections removes the injections whose source is under the
// experiment's base directory, which apps add at start. A published topology
// must not point at another experiment's files.
func dropAppInjections(topology map[string]any, baseDir string) {
	dir := path.Clean(baseDir)
	if !path.IsAbs(dir) || dir == "/" {
		return
	}

	nodes, _ := topology[keyNodes].([]any)

	for _, entry := range nodes {
		node, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		injections, ok := node["injections"].([]any)
		if !ok {
			continue
		}

		kept := slices.DeleteFunc(slices.Clone(injections), func(injection any) bool {
			spec, _ := injection.(map[string]any)
			src, _ := spec["src"].(string)

			return path.IsAbs(src) && strings.HasPrefix(path.Clean(src), dir+"/")
		})

		switch {
		case len(kept) == len(injections):
		case len(kept) == 0:
			delete(node, "injections")
		default:
			node["injections"] = kept
		}
	}
}

// warnUnrepresentedExperimentFields reports experiment settings the builder
// document does not model, so publishing never silently drops them.
func (g *generator) warnUnrepresentedExperimentFields(spec map[string]any) {
	fields := []string{
		"baseDir", "defaultBridge", "deployMode", "schedules", "useGREMesh",
	}

	var present []string

	for _, field := range fields {
		switch value := spec[field].(type) {
		case nil:
		case string:
			if value != "" {
				present = append(present, field)
			}
		case bool:
			if value {
				present = append(present, field)
			}
		case map[string]any:
			if len(value) > 0 {
				present = append(present, field)
			}
		default:
			present = append(present, field)
		}
	}

	if len(present) > 0 {
		g.warnf(
			"the builder document does not hold these experiment fields: %s",
			strings.Join(present, ", "),
		)
	}
}

// finish makes the networks, the switch hubs, the node order and the layout.
func (g *generator) finish() {
	networks := slices.Collect(maps.Values(g.networks))

	sort.SliceStable(networks, func(i, j int) bool {
		return networkBefore(networks[i].Name, networks[j].Name)
	})

	g.doc.Networks = make([]Network, 0, len(networks))

	for _, network := range networks {
		g.doc.Networks = append(g.doc.Networks, *network)

		if g.members[network.ID] == 0 {
			// VLANs with no attached interface (alias-only) get a canonical
			// network but no switch hub.
			continue
		}

		g.switches = append(g.switches, Node{ //nolint:exhaustruct // only switch nodes carry a switch payload
			ID:       SwitchNodeID(network.Name),
			Kind:     NodeKindSwitch,
			Label:    network.Name,
			Position: Position{X: 0, Y: 0},
			Switch:   &Switch{NetworkID: network.ID}, //nolint:exhaustruct // colors are the editor's to set
		})
	}

	groupIncludedDevices(g.devices)

	sort.SliceStable(g.edges, func(i, j int) bool {
		return g.edges[i].ID < g.edges[j].ID
	})

	rows := layoutDevices(g.devices)
	layout(g.switches, float64(rows)*layoutSpacingY+layoutSwitchOffset)

	g.doc.Nodes = make([]Node, 0, len(g.devices)+len(g.switches))
	g.doc.Nodes = append(g.doc.Nodes, g.devices...)
	g.doc.Nodes = append(g.doc.Nodes, g.switches...)
	g.doc.Edges = g.edges

	if g.doc.Edges == nil {
		g.doc.Edges = []Edge{}
	}

	if g.doc.Source != nil {
		g.doc.Source.Warnings = g.warnings
	}
}

// groupIncludedDevices puts the topology's own devices first, then those of
// each include, each group in source order. A publish then writes the nodes in
// source order, which is the order the VMs launch in.
func groupIncludedDevices(devices []Node) {
	rank := map[string]int{"": 0}

	for _, node := range devices {
		if _, ranked := rank[node.Device.IncludedFrom]; !ranked {
			rank[node.Device.IncludedFrom] = len(rank)
		}
	}

	sort.SliceStable(devices, func(i, j int) bool {
		return rank[devices[i].Device.IncludedFrom] < rank[devices[j].Device.IncludedFrom]
	})
}

// networkBefore orders the networks of a document by name, without case.
// Networks differing only by case sort by their exact names, so the order
// never depends on map iteration.
func networkBefore(first, second string) bool {
	a, b := foldKey(first), foldKey(second)
	if a != b {
		return a < b
	}

	return first < second
}

func layout(nodes []Node, originY float64) {
	for i := range nodes {
		nodes[i].Position = Position{
			X: float64(i%layoutColumns) * layoutSpacingX,
			Y: originY + float64(i/layoutColumns)*layoutSpacingY,
		}
	}
}

// layoutDevices puts devices in rows like [layout], but starts a new row
// for each included topology so its devices stay together. It returns the
// number of rows used.
func layoutDevices(nodes []Node) int {
	row, column := 0, 0

	for i := range nodes {
		if i > 0 && (column == layoutColumns ||
			nodes[i].Device.IncludedFrom != nodes[i-1].Device.IncludedFrom) {
			row++
			column = 0
		}

		nodes[i].Position = Position{
			X: float64(column) * layoutSpacingX,
			Y: float64(row) * layoutSpacingY,
		}
		column++
	}

	if len(nodes) == 0 {
		return 0
	}

	return row + 1
}

// iconKeyForSpec derives the icon key of a node spec, or "" for the default
// icon. Only "external": true marks hardware, as in catalog.js, because phenix
// writes "external": null on every VM.
func iconKeyForSpec(spec map[string]any) string {
	if external, _ := spec["external"].(bool); external {
		return "external"
	}

	nodeType, _ := spec["type"].(string)

	switch key := foldKey(nodeType); key {
	case iconRouter, iconFirewall, "printer", "switch", iconContainer, IconServer, iconDesktop:
		return key
	case "virtualmachine", "":
		return iconKeyForOS(spec)
	default:
		if IsIconKey(key) {
			return key
		}

		return iconKeyForOS(spec)
	}
}

// iconKeyForOS derives an icon key from a node's VM type and operating system,
// or else gives the generic server icon. It maps them as iconKeyForSpec in
// the front end's catalog.js does, so a node gets the same icon whether it
// was made in the editor or generated here (testdata/icon-keys.json holds the
// cases both test suites check).
func iconKeyForOS(spec map[string]any) string {
	if foldKey(specString(spec, "general", "vm_type")) == iconContainer {
		return iconContainer
	}

	switch osType := foldKey(specString(spec, "hardware", "os_type")); osType {
	case "":
		return ""
	case "centos", "linux", "windows":
		return osType
	case "rhel":
		return "redhat"
	case "minirouter", "vyatta", "vyos":
		return iconRouter
	default:
		return IconServer
	}
}

// toInt reads a number of a spec that [normalizeSpec] gave, which makes
// every number an int or a float64.
func toInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

// SourceDigest returns the "sha256:<hex>" digest of the canonical JSON of the
// apiVersion, kind, name and spec of a config. It excludes status, timestamps,
// annotations and labels, so an unchanged config keeps its digest.
func SourceDigest(config store.Config) (string, error) {
	return ContentDigest(sourceDigestInput(config))
}

func sourceDigestInput(config store.Config) map[string]any {
	return map[string]any{
		keyAPIVersion: config.Version,
		keyKind:       config.Kind,
		keyName:       config.Metadata.Name,
		keySpec:       config.Spec,
	}
}

// ImportDigest returns the digest [FromConfig] records as [Source.Digest]. For
// a topology with a legacy diagram it also covers the diagram, which publishing
// removes, so a changed diagram makes the working copy stale.
func ImportDigest(config store.Config) (string, error) {
	if !HasLegacyDiagram(config) {
		return SourceDigest(config)
	}

	input := sourceDigestInput(config)
	input[LegacyXMLAnnotation] = config.Metadata.Annotations[LegacyXMLAnnotation]

	return ContentDigest(input)
}
