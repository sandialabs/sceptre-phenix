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

// ErrUnsupportedKind is returned by [FromConfig] for configs that are neither a
// Topology nor an Experiment.
var ErrUnsupportedKind = errors.New("unsupported config kind for builder document")

// FromConfig generates a builder document from a validated phenix config of
// kind Topology or Experiment.
//
// Generation is lossless with respect to phenix node semantics: every node spec
// (including general.description and keys unknown to this package) is copied
// verbatim into its device. In addition:
//
//   - an interface handle is created for every named interface of every node,
//   - a canonical network is created for every VLAN referenced by an interface
//     and for every experiment VLAN alias,
//   - a switch hub is created for every non-empty VLAN, including VLANs with a
//     single attached interface,
//   - every interface declaring a VLAN is connected to that VLAN's switch,
//   - interfaces without a VLAN are preserved unconnected,
//   - experiment VLAN aliases and scenarios are imported when available.
//
// Identifiers and initial positions are derived from hostnames, interface
// names, and VLAN names, so repeated imports produce identical documents.
// [Source.ImportedAt] is deliberately left empty for the same reason; callers
// that want a timestamp should set it themselves. [Source.Digest] and
// [Source.UpdatedAt] record the identity of the source config so publishing can
// detect a stale working copy.
//
// The returned warnings are also stored on the document's [Source].
func FromConfig(config store.Config) (*Document, []string, error) {
	kind, err := canonicalKind(config.Kind)
	if err != nil {
		return nil, nil, err
	}

	spec, err := specForConfig(config, kind)
	if err != nil {
		return nil, nil, err
	}

	gen := &generator{ //nolint:exhaustruct // accumulators fill in as nodes are read
		doc:      NewDocument(config.Metadata.Name),
		networks: map[string]*Network{},
	}

	switch kind {
	case kindTopology:
		gen.doc.Source = &Source{ //nolint:exhaustruct // warnings are attached once generation finishes
			Kind:       SourceKindTopology,
			Name:       config.Metadata.Name,
			APIVersion: config.Version,
		}

		gen.importTopology(spec)
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

		gen.importTopology(topology)
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
		return nil, nil, fmt.Errorf("generated document failed validation: %w", err)
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
	// networks is keyed by the case-folded VLAN name.
	networks map[string]*Network
	edges    []Edge
	// members counts the interfaces attached to each network.
	members map[string]int
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

func (g *generator) importTopology(spec map[string]any) {
	if spec == nil {
		return
	}

	if included, ok := spec["includeTopologies"].([]any); ok && len(included) > 0 {
		g.warnf(
			"topology includes %d other topologies, which the builder does not "+
				"represent; included nodes are not shown",
			len(included),
		)
	}

	nodes, _ := spec[keyNodes].([]any)
	seen := map[string]int{}

	for i, entry := range nodes {
		nodeSpec, ok := entry.(map[string]any)
		if !ok {
			g.warnf("topology node at index %d is not an object and was skipped", i)

			continue
		}

		hostname := specString(nodeSpec, "general", "hostname")
		if strings.TrimSpace(hostname) == "" {
			g.warnf("topology node at index %d has no hostname and was skipped", i)

			continue
		}

		if prev, dup := seen[foldKey(hostname)]; dup {
			g.warnf(
				"topology node at index %d duplicates the hostname of the node at index %d (%q) and was skipped",
				i, prev, hostname,
			)

			continue
		}

		seen[foldKey(hostname)] = i

		g.addDevice(hostname, nodeSpec)
	}
}

func (g *generator) addDevice(hostname string, spec map[string]any) {
	device := &Device{
		Hostname:   hostname,
		IconKey:    iconKeyForSpec(spec),
		Spec:       spec,
		Interfaces: []InterfaceHandle{},
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
func (g *generator) network(name string) *Network {
	if existing, ok := g.networks[foldKey(name)]; ok {
		if existing.Name != name {
			g.warnf(
				"VLAN %q differs only by case from VLAN %q; both were mapped to network %q",
				name, existing.Name, existing.Name,
			)
		}

		return existing
	}

	network := &Network{ //nolint:exhaustruct // aliases and presentation are optional
		ID:   NetworkID(name),
		Name: name,
	}
	g.networks[foldKey(name)] = network

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
		alias, ok := toInt(aliases[name])
		if !ok {
			g.warnf("VLAN alias for %q is not an integer and was dropped", name)

			continue
		}

		network := g.network(name)

		if alias == 0 {
			// phenix records unassigned VLANs with an alias of 0.
			continue
		}

		if alias < 1 || alias > maxVLANAlias {
			g.warnf("VLAN alias %d for %q is out of range (1-%d) and was dropped", alias, name, maxVLANAlias)

			continue
		}

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

	sort.SliceStable(networks, func(i, j int) bool {
		return foldKey(networks[i].Name) < foldKey(networks[j].Name)
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

	sort.SliceStable(g.devices, func(i, j int) bool {
		return foldKey(g.devices[i].Device.Hostname) < foldKey(g.devices[j].Device.Hostname)
	})

	sort.SliceStable(g.edges, func(i, j int) bool {
		return g.edges[i].ID < g.edges[j].ID
	})

	layout(g.devices, 0)
	layout(g.switches, switchOriginY(len(g.devices)))

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

func switchOriginY(devices int) float64 {
	rows := (devices + layoutColumns - 1) / layoutColumns

	return float64(rows)*layoutSpacingY + layoutSwitchOffset
}

// iconKeyForSpec derives the builder-local icon hint of a node spec. It only
// ever returns a member of the [IconKeys] registry, or the empty string when no
// registry key applies (the front end then falls back to its default icon).
func iconKeyForSpec(spec map[string]any) string {
	if _, external := spec["external"]; external {
		return "external"
	}

	nodeType, _ := spec["type"].(string)

	switch key := foldKey(nodeType); key {
	case "router", "firewall", "printer", "switch", "container", IconServer, "desktop":
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

// iconKeyForOS derives an icon key from a node's operating system, falling back
// to the generic server icon.
func iconKeyForOS(spec map[string]any) string {
	osType := foldKey(specString(spec, "hardware", "os_type"))

	if IsIconKey(osType) {
		return osType
	}

	if osType == "" {
		return ""
	}

	return IconServer
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
