package builder

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/activeshadow/structs"

	"phenix/store"
	"phenix/types"
	"phenix/types/version"
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
// next one several times) cannot turn into an unbounded number of reads.
const maxIncludeResolutions = 100

// ErrUnsupportedKind is returned by [FromConfig] for configs that are neither a
// Topology nor an Experiment.
var ErrUnsupportedKind = errors.New("unsupported config kind for builder document")

// TopologyLoader reads the stored Topology config an includeTopologies entry
// names. It returns an error when there is no such topology or the caller may
// not read it; generation reports the error as a warning and goes on.
type TopologyLoader func(name string) (*store.Config, error)

// GenerateOption configures [FromConfig].
type GenerateOption func(*generator)

// WithTopologyLoader makes [FromConfig] resolve includeTopologies through load,
// recursively and with cycle protection, the way phenix merges included
// topologies when it creates an experiment. The devices of included
// topologies are added to a document generated from a topology, and
// recognized among the (already merged) nodes of an experiment; either way
// they are marked with [Device.IncludedFrom]. Without a loader the references
// are kept but not resolved.
func WithTopologyLoader(load TopologyLoader) GenerateOption {
	return func(g *generator) {
		g.load = load
	}
}

// FromConfig generates a builder document from a validated phenix config of
// kind Topology or Experiment.
//
// Generation is lossless with respect to phenix node semantics: every node spec
// (including general.description and keys unknown to this package) is copied
// verbatim into its device. In addition:
//
//   - an interface handle is created for every named interface of every node,
//   - a canonical network is created for every VLAN referenced by an interface
//     and for every experiment VLAN alias; VLAN names are case sensitive, as
//     in minimega, so VLANs differing only by case get separate networks,
//   - a switch hub is created for every non-empty VLAN, including VLANs with a
//     single attached interface,
//   - every interface declaring a VLAN is connected to that VLAN's switch,
//   - interfaces without a VLAN are preserved unconnected,
//   - experiment VLAN aliases and scenarios are imported when available,
//   - included topologies are resolved when a loader is given (see
//     [WithTopologyLoader]); their devices are marked [Device.IncludedFrom]
//     and connected to the VLAN switches like any other device.
//
// Identifiers and initial positions are derived from hostnames, interface
// names, and VLAN names, so repeated imports produce identical documents.
// [Source.ImportedAt] is deliberately left empty for the same reason; callers
// that want a timestamp should set it themselves. [Source.Digest] and
// [Source.UpdatedAt] record the identity of the source config so publishing can
// detect a stale working copy.
//
// The returned warnings are also stored on the document's [Source].
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

		gen.importTopology(topology, config.Metadata.Annotations["topology"])
		gen.importVLANs(spec["vlans"])

		if err := gen.importScenario(spec["scenario"], config.Metadata.Annotations["scenario"]); err != nil {
			return nil, nil, err
		}

		gen.warnUnrepresentedExperimentFields(spec)
	}

	digest, err := SourceDigest(config)
	if err != nil {
		return nil, nil, err
	}

	gen.doc.Source.Digest = digest
	gen.doc.Source.UpdatedAt = config.Metadata.Updated

	gen.finish()

	if err := gen.doc.Validate(); err != nil {
		return nil, nil, fmt.Errorf("imported document failed validation: %w", err)
	}

	return gen.doc, gen.warnings, nil
}

const (
	kindTopology   = "Topology"
	kindExperiment = "Experiment"
	kindScenario   = "Scenario"
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

	// load resolves included topologies; nil leaves them unresolved.
	load TopologyLoader
	// hostnames maps the case-folded hostname of every device added so far to
	// the included topology defining it, or "" for the source's own devices.
	hostnames map[string]string
	// included counts the devices marked as included, per defining topology.
	included map[string]int
	// resolutions counts included topologies read, see maxIncludeResolutions.
	resolutions int
	// includedBy lists, for every included topology reached, the topologies
	// that include it, in the order they were reached; includeOrder is the
	// order they were first reached in.
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

func (e IncludeError) Error() string {
	return fmt.Sprintf("included topology %q: %v", e.Name, e.Err)
}

func (e IncludeError) Unwrap() error {
	return e.Err
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

// IncludeReport is what [CheckIncludes] found.
type IncludeReport struct {
	// Unreadable lists the included topologies that could not be read.
	Unreadable []IncludeError
	// Clashes lists the nodes of included topologies that duplicate a
	// hostname of the topology itself.
	Clashes []HostnameClash
}

// CheckIncludes resolves the includeTopologies of the topology spec named
// name through load, recursively and each included topology once, the way
// generation does. It reports what would stop phenix from merging them now:
// included topologies that cannot be read, and nodes of included topologies
// whose hostname the spec itself defines (compared without case, as the
// Builder compares hostnames).
func CheckIncludes(name string, spec map[string]any, load TopologyLoader) (IncludeReport, error) {
	normalized, err := normalizeSpecMap(spec)
	if err != nil {
		return IncludeReport{}, fmt.Errorf("reading topology spec: %w", err)
	}

	gen := &generator{load: load} //nolint:exhaustruct // only include resolution is used

	resolved := gen.resolveAll(includeNames(normalized), name)
	report := IncludeReport{Unreadable: gen.unreadable, Clashes: nil}

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

// specForConfig returns the config spec in the latest stored representation,
// upgrading it first when the config carries an older apiVersion.
func specForConfig(config store.Config, kind string) (map[string]any, error) {
	latest, ok := version.StoredVersion[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedKind, kind)
	}

	if config.APIVersion() == latest {
		spec, err := normalizeSpecMap(config.Spec)
		if err != nil {
			return nil, fmt.Errorf("reading %s spec: %w", kind, err)
		}

		if spec == nil {
			spec = map[string]any{}
		}

		return spec, nil
	}

	upgrader := types.GetUpgrader(kind + "/" + latest)
	if upgrader == nil {
		return nil, fmt.Errorf("no upgrader found for %s version %s", kind, latest)
	}

	upgraded, err := upgrader.Upgrade(config.APIVersion(), config.Spec, config.Metadata)
	if err != nil {
		return nil, fmt.Errorf("upgrading %s to %s: %w", kind, latest, err)
	}

	spec, err := normalizeSpecMap(structs.MapWithOptions(
		upgraded,
		structs.DefaultCase(structs.CASE_SNAKE),
		structs.DefaultOmitEmpty(),
	))
	if err != nil {
		return nil, fmt.Errorf("reading upgraded %s spec: %w", kind, err)
	}

	return spec, nil
}

func (g *generator) warnf(format string, args ...any) {
	g.warnings = append(g.warnings, fmt.Sprintf(format, args...))
}

// importTopology imports a topology spec named name: the source topology, or
// the topology an experiment was created from.
//
// A topology's included topologies are resolved and their devices added after
// its own, in the order phenix merges them. An experiment's topology already
// holds those devices, merged by phenix when the experiment was created, so
// they are recognized by hostname and marked instead of being added twice;
// a hostname the topology defines itself is never taken for an included one.
// The nodes phenix merged in from an included topology that cannot be read
// now are marked too, as long as the topology itself can be read.
func (g *generator) importTopology(spec map[string]any, name string) {
	if spec == nil {
		return
	}

	includes := g.recordIncludes(spec)

	if g.doc.Source.Kind == SourceKindExperiment {
		owners, resolved := g.experimentIncludes(spec, includes, name)

		g.importNodes(spec, "", owners)
		g.warnIncluded("Marked %s as coming from", "experiment node", resolved)

		return
	}

	g.importNodes(spec, "", nil)
	g.importIncludes(includes, name)
}

// recordIncludes keeps the topology's includeTopologies on the document's
// source, so publishing writes them back, and returns them.
func (g *generator) recordIncludes(spec map[string]any) []string {
	names, ok := spec["includeTopologies"].([]any)
	if !ok {
		return nil
	}

	for i, value := range names {
		name, ok := value.(string)
		if !ok || !validIncludeName(name) {
			g.warnf("included topology at index %d has an invalid name and was skipped", i)

			continue
		}

		g.doc.Source.IncludeTopologies = append(g.doc.Source.IncludeTopologies, name)
	}

	return g.doc.Source.IncludeTopologies
}

func validIncludeName(name string) bool {
	return strings.TrimSpace(name) != "" && !strings.ContainsAny(name, " \t\n")
}

// importIncludes adds the devices of a topology's included topologies.
func (g *generator) importIncludes(includes []string, root string) {
	if len(includes) == 0 {
		return
	}

	if g.load == nil {
		g.warnf(
			"topology includes %d other topologies that were not resolved; their nodes are not shown, "+
				"but the references are kept when published",
			len(includes),
		)

		return
	}

	resolved := g.resolveAll(includes, root)

	for _, problem := range g.unreadable {
		if errors.Is(problem.Err, ErrTooManyIncludes) {
			g.warnf(
				"stopped resolving included topologies after %d; the nodes of %q and later includes are not shown",
				maxIncludeResolutions, problem.Name,
			)

			continue
		}

		g.warnf("included topology %q could not be read and its nodes are not shown: %v", problem.Name, problem.Err)
	}

	for _, topology := range resolved {
		g.importNodes(topology.spec, topology.name, nil)
	}

	g.warnIncluded("Added %s from", "node", resolved)
}

// experimentIncludes maps the case-folded hostname of every node defined by
// an experiment's included topologies onto the topology defining it, and
// returns the resolved topologies. A hostname the experiment's own topology
// (root) defines is left out: that node is the topology's own, even when an
// included topology has gained a node of the same name since. Nodes of the
// experiment's topology spec that no readable topology defines are left to
// [generator.markUnreadable].
func (g *generator) experimentIncludes(
	spec map[string]any, includes []string, root string,
) (map[string]string, []includedTopology) {
	if len(includes) == 0 {
		return nil, nil
	}

	if g.load == nil {
		g.warnf(
			"the experiment's topology includes %d other topologies that were not resolved; "+
				"the nodes they added are shown as the topology's own",
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

// markUnreadable reports the included topologies of an experiment's topology
// that cannot be read now. phenix merged their nodes into the experiment
// when it created it, so a node of spec that neither root (own) nor a
// readable included topology (owners) defines came from one of them: it is
// added to owners as coming from the unreadable topology, or from the first
// when there are several and their nodes cannot be told apart. Publishing the
// topology then leaves it out, as phenix merges the include again. When root
// cannot be read (own is nil), its own nodes cannot be told apart from those
// either, so none is marked and they are published as the topology's own.
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
		reason := fmt.Sprintf("included topology %q could not be read: %v", problem.Name, problem.Err)

		switch {
		case own == nil:
			g.warnf(
				"%s. Without the topology the experiment was created from, its nodes in the experiment "+
					"cannot be told apart from the topology's own, so they are shown as the topology's own "+
					"and publishing the topology copies them into it",
				reason,
			)
		case problem.Name != first:
			g.warnf(
				"%s. Its nodes in the experiment cannot be told apart from those of included topology %q, "+
					"which could not be read either, so they are marked as coming from %q",
				reason, first, first,
			)
		case marked == 0:
			g.warnf(
				"%s. Topology %q and its readable included topologies define every experiment node, "+
					"so none is marked as coming from it",
				reason, root,
			)
		default:
			g.warnf(
				"%s. The experiment's %s that neither topology %q nor a readable included topology defines "+
					"%s marked as coming from it: shown read only, and not copied into the topology",
				reason, countOf(marked, "node"), root, pluralOf(marked, "is", "are"),
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
			"the experiment does not name the topology it was created from, " +
				"so its nodes are matched to included topologies by hostname alone",
		)

		return nil
	}

	spec, err := g.loadIncluded(root)
	if err != nil {
		g.warnf(
			"topology %q, which the experiment was created from, could not be read, "+
				"so its nodes are matched to included topologies by hostname alone: %v",
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
		"included topology %q defines node %q, which duplicates a hostname in %s; "+
			"phenix rejects duplicate hostnames, so rename one of them",
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

// includeNames lists the valid includeTopologies entries of a spec, stored
// ([]any) or projected ([]string, see [Document.ToTopology]).
func includeNames(spec map[string]any) []string {
	var values []any

	switch typed := spec["includeTopologies"].(type) {
	case []any:
		values = typed
	case []string:
		for _, name := range typed {
			values = append(values, name)
		}
	}

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
				"included topology %q is included more than once (through %s); "+
					"phenix rejects the duplicate hostnames this causes, so its nodes are shown once",
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

// resolveIncludes reads included topologies depth first: each topology,
// then the topologies it includes in turn, which is the order phenix appends
// their nodes in. parent names the topology including names. visited holds
// the topologies on the current include path; a topology including one of
// them is a cycle, which phenix rejects. A topology reached again by another
// path is recorded in includedBy and skipped, so it is resolved once. A cycle
// is reported and skipped; a topology that cannot be read is recorded in
// unreadable and skipped, for the caller to report.
func (g *generator) resolveIncludes(names []string, parent string, visited map[string]bool) []includedTopology {
	var resolved []includedTopology

	for _, name := range names {
		if visited[name] {
			g.warnf(
				"included topology %q includes itself through a cycle, which phenix rejects; "+
					"the repeated include was skipped",
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
		return nil, errors.New("no topology was returned")
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

	g.warnf(
		"%s included %s %s. They are shown read only: edit them in their own topology. "+
			"Publishing keeps includeTopologies instead of copying them.",
		fmt.Sprintf(lead, countOf(total, noun)), pluralOf(len(parts), "topology", "topologies"), listOf(parts),
	)
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
			g.warnf("%s is not an object and was skipped", where(i))

			continue
		}

		hostname := specString(nodeSpec, "general", "hostname")
		if strings.TrimSpace(hostname) == "" {
			g.warnf("%s has no hostname and was skipped", where(i))

			continue
		}

		if prev, dup := seen[foldKey(hostname)]; dup {
			g.warnf(
				"%s duplicates the hostname of the node at index %d (%q) and was skipped",
				where(i), prev, hostname,
			)

			continue
		}

		seen[foldKey(hostname)] = i

		if owner, dup := g.hostnames[foldKey(hostname)]; dup {
			g.warnHostnameClash(from, hostname, owner, "The included node is not shown")

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

	device := &Device{
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
				"interface at index %d of node %q has no name; it was preserved but cannot be connected",
				index, hostname,
			)

			continue
		}

		if seen[foldKey(name)] {
			g.warnf(
				"node %q declares interface %q more than once; only the first is connectable",
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

		network := g.network(vlan)

		g.connect(node.ID, handle, network)
	}

	g.devices = append(g.devices, node)
}

// network returns (creating if needed) the canonical network for a VLAN name.
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
			"VLAN %q differs only by case from VLAN %q; minimega treats them as different VLANs, "+
				"so they are separate networks",
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
		g.warnf("experiment VLAN range minimum (%d) is not represented in the builder document", minimum)
	}

	if maximum, ok := toInt(vlans["max"]); ok && maximum != 0 {
		g.warnf("experiment VLAN range maximum (%d) is not represented in the builder document", maximum)
	}

	aliases, ok := vlans["aliases"].(map[string]any)
	if !ok {
		return
	}

	for _, name := range slices.Sorted(maps.Keys(aliases)) {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, " \t\n") {
			g.warnf("VLAN alias name %q is invalid and was dropped", name)

			continue
		}

		alias, ok := toInt(aliases[name])
		if !ok {
			g.warnf("VLAN alias for %q is not an integer and was dropped", name)

			continue
		}

		if alias == 0 {
			// phenix records unassigned VLANs with an alias of 0.
			continue
		}

		if alias < 1 || alias > maxVLANAlias {
			g.warnf("VLAN alias %d for %q is out of range (1-%d) and was dropped", alias, name, maxVLANAlias)

			continue
		}

		network := g.network(name)
		value := alias
		network.Alias = &value
	}
}

func (g *generator) importScenario(value any, name string) error {
	content, err := normalizeSpecMap(value)
	if err != nil {
		return fmt.Errorf("reading experiment scenario: %w", err)
	}

	if len(content) == 0 {
		return nil
	}

	digest, err := ContentDigest(content)
	if err != nil {
		return fmt.Errorf("digesting experiment scenario: %w", err)
	}

	kind := ScenarioRefUploaded
	if name != "" {
		kind = ScenarioRefStored
	}

	g.doc.Scenario = &ScenarioRef{
		Kind:       kind,
		Name:       name,
		Content:    content,
		APIVersion: ScenarioAPIVersion(),
		Digest:     digest,
	}

	return nil
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
			"experiment fields not represented in the builder document: %s",
			strings.Join(present, ", "),
		)
	}
}

// finish materializes networks, switch hubs, node ordering, and layout.
func (g *generator) finish() {
	networks := slices.Collect(maps.Values(g.networks))

	// Networks differing only by case sort by their exact names, so the order
	// never depends on map iteration.
	sort.SliceStable(networks, func(i, j int) bool {
		a, b := foldKey(networks[i].Name), foldKey(networks[j].Name)
		if a != b {
			return a < b
		}

		return networks[i].Name < networks[j].Name
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
			Switch:   &Switch{NetworkID: network.ID},
		})
	}

	// The topology's own devices come first, then those of each included
	// topology, so a topology's devices are laid out together.
	sort.SliceStable(g.devices, func(i, j int) bool {
		a, b := g.devices[i].Device, g.devices[j].Device
		if a.IncludedFrom != b.IncludedFrom {
			return a.IncludedFrom < b.IncludedFrom
		}

		return foldKey(a.Hostname) < foldKey(b.Hostname)
	})

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

func layout(nodes []Node, originY float64) {
	for i := range nodes {
		nodes[i].Position = Position{
			X: float64(i%layoutColumns) * layoutSpacingX,
			Y: originY + float64(i/layoutColumns)*layoutSpacingY,
		}
	}
}

// layoutDevices lays devices out in rows like [layout], but starts a new row
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

// iconKeyForSpec derives the builder-local icon hint of a node spec. It only
// ever returns a member of the [IconKeys] registry, or the empty string when no
// registry key applies (the front end then falls back to its default icon).
//
// Only "external": true marks external hardware, as iconKeyForSpec in the
// front end's catalog.js decides it: phenix stores experiments with
// structs.MapDefaultCase, which writes "external": null on every VM.
func iconKeyForSpec(spec map[string]any) string {
	if external, _ := spec["external"].(bool); external {
		return "external"
	}

	nodeType, _ := spec["type"].(string)

	switch key := foldKey(nodeType); key {
	case iconRouter, "firewall", "printer", "switch", iconContainer, IconServer, "desktop":
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
// falling back to the generic server icon. It maps them as iconKeyForSpec in
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

func toInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	default:
		return 0, false
	}
}

// SourceDigest returns the deterministic "sha256:<hex>" digest identifying a
// source config, for use as [Source.Digest].
//
// The digest input is the canonical JSON encoding of exactly these fields:
//
//	{"apiVersion": <config.Version>,
//	 "kind":       <config.Kind>,
//	 "name":       <config.Metadata.Name>,
//	 "spec":       <config.Spec>}
//
// Mutable bookkeeping is deliberately excluded: status, metadata timestamps,
// annotations, and labels do not change the digest, so re-importing an
// unchanged config yields an unchanged digest. Object keys are sorted by
// encoding/json, so the digest does not depend on map iteration order.
func SourceDigest(config store.Config) (string, error) {
	return ContentDigest(map[string]any{
		keyAPIVersion: config.Version,
		keyKind:       config.Kind,
		keyName:       config.Metadata.Name,
		keySpec:       config.Spec,
	})
}
