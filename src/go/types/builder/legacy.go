package builder

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"phenix/store"
	"phenix/types/version"
)

// LegacyXMLAnnotation is the Topology annotation the legacy Builder kept its
// mxGraph diagram in, as plain XML.
const LegacyXMLAnnotation = "builder-xml"

// How a legacy diagram goes onto the canvas. A legacy icon is 80 by 80 and a
// device card is twice as wide, so positions are doubled. Thus what did not
// overlap in the diagram does not overlap in the document.
const (
	legacyScale = 2.0

	// The sizes the editor draws a device and a switch in (DEFAULT_SIZES in
	// the front end's model.js), and the least size of a group and of a note.
	legacyDeviceWidth    = 160.0
	legacyDeviceHeight   = 96.0
	legacySwitchWidth    = 180.0
	legacySwitchHeight   = 72.0
	legacyGroupMinWidth  = 120.0
	legacyGroupMinHeight = 80.0
	legacyNoteMinWidth   = 160.0
	legacyNoteMinHeight  = 64.0

	// legacyNudge moves an added switch down while its card covers another,
	// at most legacyNudges times.
	legacyNudge  = 128.0
	legacyNudges = 64

	// legacyListed is the most names one warning lists.
	legacyListed = 8
)

// The settings of a legacy cell (its schemaVars) that are read by name.
const (
	legacyKeyDevice   = "device"
	legacyKeyHostname = "hostname"
	legacyKeyGeneral  = "general"
	legacyDeviceIsHub = "switch"
)

// legacyRole is what a cell of a legacy diagram becomes.
type legacyRole int

const (
	legacyUnused legacyRole = iota
	legacyDeviceCell
	legacySwitchCell
	legacyGroupCell
	legacyNoteCell
)

// HasLegacyDiagram reports whether a config is a Topology that carries a
// diagram of the legacy Builder. An empty diagram counts.
func HasLegacyDiagram(config store.Config) bool {
	kind, err := canonicalKind(config.Kind)

	return err == nil && kind == kindTopology && config.HasAnnotation(LegacyXMLAnnotation)
}

// FromLegacy converts a diagram of the legacy Builder that comes without a
// topology into a document named name. The settings the diagram holds for
// each device (its schemaVars) are the node specs, as the legacy Builder
// wrote them when it saved a topology:
//
//   - the editor's own keys (device, schema) are dropped, and annotations
//     and labels kept as lists of key and value become maps,
//   - a $NAME placeholder is replaced by the diagram's experiment variable
//     of that name, or else by the legacy default of the four names that
//     had one. Values stay text, and nothing is read from the environment,
//   - white space in a hostname or a VLAN name becomes "-", since a document
//     holds neither,
//   - interfaces that are not a list of objects are left out, which a
//     warning says: the editor reads nothing else.
//
// The diagram is then applied to the document that [FromConfig] generates
// from those nodes, as [FromLegacyTopology] describes. The source of the
// document is manual. It names no config, so a publish of it never changes
// one.
//
// The returned warnings are a closed set of sentences, and are also stored
// on the document's [Source].
func FromLegacy(diagram *LegacyDiagram, name string) (*Document, []string, error) {
	if diagram == nil {
		diagram = newLegacyDiagram()
	}

	notes := &legacyNotes{} //nolint:exhaustruct // warnings accumulate
	nodes, hostnames := diagram.topologyNodes(notes)

	config := store.Config{ //nolint:exhaustruct // an in-memory topology with nothing but its nodes
		Version:  store.APIGroup + "/" + version.StoredVersion[kindTopology],
		Kind:     kindTopology,
		Metadata: store.ConfigMetadata{Name: name}, //nolint:exhaustruct // only the name is needed
		Spec:     map[string]any{keyNodes: nodes},
	}

	doc, warnings, err := FromConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("converting the legacy diagram: %w", err)
	}

	newLegacyOverlay(doc, diagram, notes, hostnames).run()

	if len(doc.Nodes) == 0 {
		notes.empty = true
	}

	warnings = append(warnings, notes.sentences()...)
	doc.Source = &Source{Kind: SourceKindManual, Warnings: warnings} //nolint:exhaustruct // a bare diagram names no config

	if err := doc.Validate(); err != nil {
		return nil, nil, fmt.Errorf("the converted diagram is not a valid Builder document: %w", err)
	}

	return doc, warnings, nil
}

// FromLegacyTopology generates a document from a Topology config that
// carries a diagram of the legacy Builder (see [HasLegacyDiagram]). The
// topology's spec is the truth for every node: the document is the one
// [FromConfig] generates, with the same options, source and digest. The
// diagram is applied to it:
//
//   - a device takes the position and icon of the diagram's cell of the same
//     hostname (compared without case). A device the diagram does not hold
//     is placed below it, and a cell the topology does not hold is left out,
//   - a switch cell becomes the switch of its VLAN, with its position and
//     name. Further switch cells of one VLAN become further switches, and
//     lines to them move the connections. A VLAN without a switch cell,
//     such as one that only joined two devices directly, keeps the switch
//     [FromConfig] gave it, placed between its devices,
//   - a VLAN ID of a switch cell or a line becomes the network's alias,
//   - a container becomes a group and other text becomes a note. Their
//     formatting, and every other shape and line, is dropped.
//
// A diagram that cannot be read, or that holds nothing, leaves the document
// as [FromConfig] generated it. An unreadable diagram also gives a warning.
// The returned warnings are also stored on the document's [Source].
func FromLegacyTopology(config store.Config, options ...GenerateOption) (*Document, []string, error) {
	doc, warnings, err := FromConfig(config, options...)
	if err != nil || !HasLegacyDiagram(config) {
		return doc, warnings, err
	}

	diagram, err := DecodeLegacy([]byte(config.Metadata.Annotations[LegacyXMLAnnotation]))
	if err != nil {
		warnings = slices.Insert(warnings, 0, fmt.Sprintf(
			"The legacy diagram of topology %s could not be read (%v), so its layout was not used. "+
				"Nodes were placed automatically.",
			cutBytes(config.Metadata.Name, maxLegacyShown), err,
		))
		doc.Source.Warnings = warnings

		return doc, warnings, nil
	}

	if diagram.blank() {
		return doc, warnings, nil
	}

	notes := &legacyNotes{} //nolint:exhaustruct // warnings accumulate
	overlay := newLegacyOverlay(doc, diagram, notes, nil)
	overlay.merge = true
	overlay.run()

	warnings = append(warnings, notes.sentences()...)
	doc.Source.Warnings = warnings

	if err := doc.Validate(); err != nil {
		return nil, nil, fmt.Errorf("the converted diagram is not a valid Builder document: %w", err)
	}

	return doc, warnings, nil
}

// blank reports a diagram that draws nothing: it has no vertex and no edge,
// only the root and layer cells every model has.
func (d *LegacyDiagram) blank() bool {
	for _, cell := range d.cells {
		if cell.vertex || cell.edge {
			return false
		}
	}

	return true
}

// isDevice reports a vertex whose settings are those of a device: any
// readable settings other than those of a switch.
func (c *legacyCell) isDevice() bool {
	return c.isVertex() && c.settings != nil && !c.isSwitch()
}

func (c *legacyCell) isSwitch() bool {
	kind, _ := c.settings[legacyKeyDevice].(string)

	return c.isVertex() && kind == legacyDeviceIsHub
}

func (c *legacyCell) isVertex() bool {
	return c.vertex && !c.edge
}

// shown names the cell in a warning: the first line of its label, or else
// its id.
func (c *legacyCell) shown() string {
	line, _, _ := strings.Cut(c.text(), "\n")
	if line = strings.TrimSpace(line); line != "" {
		return cutBytes(line, maxLegacyShown)
	}

	return "cell " + cutBytes(c.id, maxLegacyShown)
}

// legacyName makes text usable as a hostname or a VLAN name: trimmed, with
// each run of white space replaced by "-".
func legacyName(text string) string {
	return strings.Join(strings.Fields(text), "-")
}

// topologyNodes builds the topology nodes the legacy Builder would have
// saved from the diagram's device cells, in document order, and returns with
// them the hostname of each device cell that has one, by cell index.
func (d *LegacyDiagram) topologyNodes(notes *legacyNotes) ([]any, map[int]string) {
	var (
		nodes     = []any{}
		hostnames = map[int]string{}
		variables = newLegacyVariables(d.variables)
	)

	for index, cell := range d.cells {
		if !cell.isDevice() {
			continue
		}

		// A deep copy: the diagram stays as it was decoded.
		spec, err := normalizeSpecMap(cell.settings)
		if err != nil || spec == nil {
			continue
		}

		delete(spec, legacyKeyDevice)
		delete(spec, "schema")

		for _, key := range []string{"annotations", "labels"} {
			if value, ok := spec[key]; ok {
				if pairs := legacyPairs(value); pairs != nil {
					spec[key] = pairs
				} else {
					delete(spec, key)
				}
			}
		}

		variables.resolve(spec)

		name := specString(spec, legacyKeyGeneral, legacyKeyHostname)
		if strings.TrimSpace(name) == "" {
			name = cell.text()
		}

		shown := cell.shown()

		if hostname := legacyName(name); hostname != "" {
			if hostname != name {
				notes.renamedNodes = append(notes.renamedNodes, legacyRenamed("node", name, hostname, "hostname"))
			}

			general, ok := spec[legacyKeyGeneral].(map[string]any)
			if !ok {
				general = map[string]any{}
				spec[legacyKeyGeneral] = general
			}

			general[legacyKeyHostname] = hostname
			hostnames[index] = hostname
			shown = cutBytes(hostname, maxLegacyShown)
		}

		if legacyInterfaces(spec) {
			notes.unreadInterfaces = append(notes.unreadInterfaces, shown)
		}

		for _, entry := range specNodeInterfaces(spec) {
			iface, _ := entry.(map[string]any)

			if vlan, ok := iface["vlan"].(string); ok && strings.TrimSpace(vlan) != "" {
				iface["vlan"] = notes.networkName(vlan)
			}
		}

		nodes = append(nodes, spec)
	}

	variables.report(notes)

	return nodes, hostnames
}

// legacyInterfaces makes the interfaces of a legacy device's settings the
// list of objects a topology holds, in place: a value that is no list is
// removed, and so is each entry of a list that is no object. It reports
// whether any interface was left out.
func legacyInterfaces(spec map[string]any) bool {
	const key = "interfaces"

	network, ok := spec["network"].(map[string]any)
	if !ok {
		return false
	}

	value, set := network[key]
	if !set {
		return false
	}

	entries, ok := value.([]any)
	if !ok {
		delete(network, key)

		// A null holds no interface.
		return value != nil
	}

	kept := make([]any, 0, len(entries))

	for _, entry := range entries {
		if _, object := entry.(map[string]any); object {
			kept = append(kept, entry)
		}
	}

	network[key] = kept

	return len(kept) != len(entries)
}

// legacyPairs changes the annotations or labels of a legacy device into the
// map that a topology holds. The legacy Builder kept them as a list of key
// and value, where a later key wins. A map is kept as it is. It returns nil
// for anything else.
func legacyPairs(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case []any:
		pairs := map[string]any{}

		for _, entry := range typed {
			fields, _ := entry.(map[string]any)
			key, named := fields["key"].(string)

			if value, has := fields["value"]; named && has {
				pairs[key] = value
			}
		}

		return pairs
	}

	return nil
}

func legacyRenamed(what, from, to, noun string) string {
	return fmt.Sprintf(
		"Renamed %s %q to %q: a %s cannot hold spaces.",
		what, cutBytes(from, maxLegacyShown), cutBytes(to, maxLegacyShown), noun,
	)
}

var legacyPlaceholder = regexp.MustCompile(`\$[A-Z_0-9]+`)

// legacyVariables resolves the $NAME placeholders of a legacy diagram.
type legacyVariables struct {
	// defined are the diagram's own variables, and values those with the
	// legacy defaults beneath them.
	defined map[string]string
	values  map[string]string
	// defaults are the defaults used for names the diagram does not define,
	// and unknown the placeholders that are a whole value and have none.
	defaults map[string]string
	unknown  map[string]bool
}

func newLegacyVariables(defined map[string]string) *legacyVariables {
	// What the legacy Builder started every diagram with.
	values := map[string]string{
		"DEFAULT_MEMORY":       "2048",
		"DEFAULT_VCPU":         "1",
		"DEFAULT_VM_IMAGE":     "ubuntu.qc2",
		"DEFAULT_ROUTER_IMAGE": "vyos.qc2",
	}

	maps.Copy(values, defined)

	return &legacyVariables{
		defined: defined, values: values, defaults: map[string]string{}, unknown: map[string]bool{},
	}
}

// resolve replaces the placeholders in every text value of a spec, in place.
// Keys are left alone, and a replaced value stays text.
func (v *legacyVariables) resolve(value any) any {
	switch typed := value.(type) {
	case string:
		return v.resolveText(typed)
	case map[string]any:
		for key, child := range typed {
			typed[key] = v.resolve(child)
		}
	case []any:
		for index, child := range typed {
			typed[index] = v.resolve(child)
		}
	}

	return value
}

func (v *legacyVariables) resolveText(text string) string {
	if !strings.Contains(text, "$") {
		return text
	}

	whole := legacyPlaceholder.FindString(text) == text

	return legacyPlaceholder.ReplaceAllStringFunc(text, func(placeholder string) string {
		name := placeholder[1:]

		value, known := v.values[name]
		if !known {
			if whole {
				v.unknown[placeholder] = true
			}

			return placeholder
		}

		if _, defined := v.defined[name]; !defined {
			v.defaults[placeholder] = value
		}

		return value
	})
}

func (v *legacyVariables) report(notes *legacyNotes) {
	for _, placeholder := range slices.Sorted(maps.Keys(v.defaults)) {
		notes.defaults = append(notes.defaults, placeholder+" = "+v.defaults[placeholder])
	}

	notes.placeholders = slices.Sorted(maps.Keys(v.unknown))
}

// legacyNotes collects what a conversion has to say, and words it as the
// closed set of sentences the documentation lists.
type legacyNotes struct {
	absent, unplaced, added, unlinked, unreadable []string
	unreadInterfaces                              []string
	badIDs, manyIDs, sharedIDs                    []string
	renamedNodes, renamedNetworks                 []string
	defaults, placeholders                        []string
	layers, lines, shapes                         int
	decorated, empty                              bool
	// said holds what was added once only.
	said map[string]bool
}

// once adds text to a list unless the list already holds it.
func (n *legacyNotes) once(list *[]string, text string) {
	if n.said == nil {
		n.said = map[string]bool{}
	}

	if !n.said[text] {
		n.said[text] = true
		*list = append(*list, text)
	}
}

// networkName returns name as a VLAN name (see [legacyName]), and notes a
// change.
func (n *legacyNotes) networkName(name string) string {
	fixed := legacyName(name)
	if fixed != name && fixed != "" {
		n.once(&n.renamedNetworks, legacyRenamed("network", name, fixed, "network name"))
	}

	return fixed
}

// sentences returns the warnings in the order they are documented in.
func (n *legacyNotes) sentences() []string {
	var out []string

	list := func(names []string, lead, one, many string) {
		if len(names) > 0 {
			out = append(out, lead+legacyCount(len(names), one, many)+": "+legacyList(names)+".")
		}
	}

	list(n.absent, "",
		"node of the legacy diagram is not in the topology and was left out",
		"nodes of the legacy diagram are not in the topology and were left out")
	list(n.unplaced, "",
		"node of the topology was not in the legacy diagram and was placed below it",
		"nodes of the topology were not in the legacy diagram and were placed below it")
	list(n.added, "Added a switch for ",
		"network that had none in the legacy diagram", "networks that had none in the legacy diagram")
	list(n.unlinked, "Left out ",
		"link whose node has no interface on that network", "links whose nodes have no interface on that network")

	out = slices.Concat(out, n.badIDs, n.manyIDs, n.sharedIDs)

	if n.layers > 1 {
		out = append(out, fmt.Sprintf("The legacy diagram had %d layers. They were merged into one.", n.layers))
	}

	list(n.unreadable, "",
		"item has settings that could not be read and was left out",
		"items have settings that could not be read and were left out")
	list(n.unreadInterfaces, "",
		"node has interfaces that could not be read and were left out",
		"nodes have interfaces that could not be read and were left out")

	out = slices.Concat(out, n.renamedNodes, n.renamedNetworks)

	if len(n.defaults) > 0 {
		out = append(out,
			"Used the legacy defaults for variables the diagram does not define: "+legacyList(n.defaults)+".")
	}

	list(n.placeholders, "",
		"placeholder has no value in the diagram and was kept as text",
		"placeholders have no value in the diagram and were kept as text")

	if n.lines > 0 {
		out = append(out, "Left out "+
			legacyCount(n.lines, "line that was not a network link", "lines that were not network links")+".")
	}

	if n.shapes > 0 {
		out = append(out, "Left out "+legacyCount(n.shapes, "shape that had no text", "shapes that had no text")+".")
	}

	if n.decorated {
		out = append(out, "Text and containers were kept as notes and groups. "+
			"Their colors, fonts and other formatting were not converted.")
	}

	if n.empty {
		out = append(out, "The diagram has no nodes.")
	}

	return out
}

func legacyCount(n int, one, many string) string {
	return strconv.Itoa(n) + " " + pluralOf(n, one, many)
}

// legacyList names at most legacyListed of names, sorted, as "a, b and c",
// and says how many more there are.
func legacyList(names []string) string {
	names = slices.Sorted(slices.Values(names))

	if len(names) > legacyListed {
		names = append(slices.Clip(names[:legacyListed]), strconv.Itoa(len(names)-legacyListed)+" more")
	}

	return listOf(names)
}

// legacyVLAN is a VLAN ID the diagram gives for a network.
type legacyVLAN struct {
	network string
	id      any
}

// legacyOverlay applies a legacy diagram to the document generated from its
// topology.
type legacyOverlay struct {
	doc     *Document
	diagram *LegacyDiagram
	notes   *legacyNotes
	// merge reports that the document came from a stored topology, whose
	// nodes are matched to cells by hostname. Otherwise hostnames gives the
	// hostname of each device cell.
	merge     bool
	hostnames map[int]string

	// Per cell: its parent cell (or -1), its rectangle in absolute diagram
	// coordinates, what it becomes, the node it became, whether it may hold
	// other cells by their position, and the group cell it joined (or -1).
	parents []int
	boxes   []legacyBox
	roles   []legacyRole
	byCell  []*Node
	plain   []bool
	groupOf []int
	// vlans is the VLAN name of each switch cell.
	vlans map[int]string

	// The document's nodes, in the order they are written back: its devices
	// and switches, then the switches, groups and notes the diagram adds.
	devices, hubs, switches, groups, texts []*Node
	// byHostname finds a device by its case-folded hostname, hubOf the first
	// switch of a network, and claimed the nodes a cell took.
	byHostname map[string]*Node
	hubOf      map[string]*Node
	claimed    map[string]bool
	// networks finds a network of the document by name.
	networks map[string]int
	// edgesOf lists the edges of a device on a network, by "<node>/<network>"
	// and in interface order. taken marks those that a line of the diagram
	// used.
	edgesOf map[string][]int
	taken   map[int]bool
	// ids are the VLAN IDs the diagram gives, in the order they count.
	ids []legacyVLAN
}

func newLegacyOverlay(doc *Document, diagram *LegacyDiagram, notes *legacyNotes, hostnames map[int]string) *legacyOverlay {
	count := len(diagram.cells)

	overlay := &legacyOverlay{ //nolint:exhaustruct // the node lists fill in as the diagram is read
		doc: doc, diagram: diagram, notes: notes, merge: false, hostnames: hostnames,
		parents: make([]int, count), boxes: make([]legacyBox, count), roles: make([]legacyRole, count),
		byCell: make([]*Node, count), plain: make([]bool, count), groupOf: make([]int, count),
		vlans:      map[int]string{},
		byHostname: map[string]*Node{}, hubOf: map[string]*Node{}, claimed: map[string]bool{},
		networks: map[string]int{}, edgesOf: map[string][]int{}, taken: map[int]bool{},
	}

	order := map[string]int{}

	for i := range doc.Nodes {
		node := doc.Nodes[i]

		switch node.Kind {
		case NodeKindDevice:
			overlay.devices = append(overlay.devices, &node)
			overlay.byHostname[foldKey(node.Device.Hostname)] = &node

			for _, handle := range node.Device.Interfaces {
				order[handle.ID] = handle.Index
			}
		case NodeKindSwitch:
			overlay.hubs = append(overlay.hubs, &node)
			overlay.hubOf[node.Switch.NetworkID] = &node
		case NodeKindNote, NodeKindGroup, NodeKindShape, NodeKindIcon, NodeKindLine:
		}
	}

	for i := range doc.Networks {
		overlay.networks[doc.Networks[i].Name] = i
	}

	for i := range doc.Edges {
		key := doc.Edges[i].SourceNodeID + "/" + doc.Edges[i].NetworkID
		overlay.edgesOf[key] = append(overlay.edgesOf[key], i)
	}

	for _, edges := range overlay.edgesOf {
		sort.SliceStable(edges, func(a, b int) bool {
			return order[doc.Edges[edges[a]].SourceHandleID] < order[doc.Edges[edges[b]].SourceHandleID]
		})
	}

	return overlay
}

func (o *legacyOverlay) run() {
	o.resolveParents()
	o.resolveBoxes()
	o.placeDevices()
	o.placeSwitches()
	o.readLines()
	o.assignVLANs()
	o.decorate()
	o.joinGroups()
	o.placeHubs()
	o.placeRest()
	o.finish()
}

// resolveParents finds the parent cell of every cell. A parent that is not
// in the diagram, and a chain of parents that runs in a circle, leave the
// cell without one. It also counts the diagram's layers: the cells below a
// root cell that are not drawn themselves.
func (o *legacyOverlay) resolveParents() {
	cells := o.diagram.cells

	for i, cell := range cells {
		o.parents[i] = -1

		if parent, ok := o.diagram.byID[cell.parent]; ok && cell.parent != "" {
			o.parents[i] = parent
		}
	}

	// 0 is not visited, 1 is on the chain being walked, 2 is settled.
	const walking, settled = 1, 2

	var (
		state  = make([]int, len(cells))
		circle = make([]bool, len(cells))
		chain  []int
	)

	for i := range cells {
		chain = chain[:0]

		at := i
		for at >= 0 && state[at] == 0 {
			state[at] = walking
			chain = append(chain, at)
			at = o.parents[at]
		}

		// The walk ended on its own chain, or on a cell known to lead into
		// a circle: every cell walked does too.
		broken := at >= 0 && (state[at] == walking || circle[at])

		for _, walked := range chain {
			state[walked], circle[walked] = settled, broken
		}
	}

	for i := range cells {
		if circle[i] {
			o.parents[i] = -1
		}
	}

	for i, cell := range cells {
		parent := o.parents[i]
		if parent >= 0 && !cell.vertex && !cell.edge && o.parents[parent] < 0 &&
			!cells[parent].vertex && !cells[parent].edge {
			o.notes.layers++
		}
	}
}

// resolveBoxes gives every cell its rectangle in absolute coordinates: the
// geometry of a cell is relative to its parent when that is a vertex.
func (o *legacyOverlay) resolveBoxes() {
	cells := o.diagram.cells
	done := make([]bool, len(cells))

	var chain []int

	for i := range cells {
		chain = chain[:0]

		for at := i; at >= 0 && !done[at]; at = o.vertexParent(at) {
			chain = append(chain, at)
		}

		for _, at := range slices.Backward(chain) {
			box := cells[at].box

			if parent := o.vertexParent(at); parent >= 0 {
				outer := o.boxes[parent]
				if cells[at].relative {
					box.x, box.y = box.x*outer.width, box.y*outer.height
				}

				box.x, box.y = box.x+outer.x, box.y+outer.y
			} else if cells[at].relative {
				box.x, box.y = 0, 0
			}

			o.boxes[at], done[at] = box, true
		}
	}
}

// vertexParent is the parent of a cell when that parent is a vertex, and -1
// otherwise.
func (o *legacyOverlay) vertexParent(cell int) int {
	if parent := o.parents[cell]; parent >= 0 && o.diagram.cells[parent].isVertex() {
		return parent
	}

	return -1
}

// placeDevices gives each device of the document the position and icon of
// the first cell of its hostname.
func (o *legacyOverlay) placeDevices() {
	absent := map[string]bool{}

	for i, cell := range o.diagram.cells {
		if !cell.isVertex() || !cell.hasSettings || cell.isSwitch() {
			continue
		}

		if cell.settings != nil {
			o.roles[i] = legacyDeviceCell
		}

		name := o.hostname(i, cell)
		node := o.byHostname[foldKey(name)]

		switch {
		case node == nil && cell.settings == nil:
			o.notes.unreadable = append(o.notes.unreadable, cell.shown())
		case node == nil && o.merge && foldKey(name) == "":
			o.notes.absent = append(o.notes.absent, cell.shown())
		case node == nil && o.merge && !absent[foldKey(name)]:
			absent[foldKey(name)] = true
			o.notes.absent = append(o.notes.absent, cutBytes(strings.TrimSpace(name), maxLegacyShown))
		case node == nil || o.claimed[node.ID]:
			// In a diagram of its own, the generation has said why a node
			// was skipped. A later cell of a hostname is ignored.
		default:
			o.roles[i] = legacyDeviceCell
			o.claim(i, node)
			node.Position = legacyCard(o.boxes[i], legacyDeviceWidth, legacyDeviceHeight)
			legacyIcon(node.Device, cell)
		}
	}
}

// hostname is the hostname a device cell stands for: the one its settings
// give, or else its label.
func (o *legacyOverlay) hostname(index int, cell *legacyCell) string {
	if !o.merge {
		return o.hostnames[index]
	}

	if name := specString(cell.settings, legacyKeyGeneral, legacyKeyHostname); strings.TrimSpace(name) != "" {
		return name
	}

	return cell.text()
}

func (o *legacyOverlay) claim(cell int, node *Node) {
	o.byCell[cell] = node
	o.claimed[node.ID] = true
}

// legacyIcon gives a device the one icon key of its legacy kind, which the
// cell's settings name or else the file name of its image shows. The color
// and artwork variants of the legacy icons are dropped, and the image is
// never read. External hardware, and a kind without an icon key, keep the
// key generation chose.
func legacyIcon(device *Device, cell *legacyCell) {
	if external, _ := device.Spec["external"].(bool); external {
		return
	}

	kinds := []string{iconRouter, iconFirewall, iconDesktop, IconServer}

	if kind, _ := cell.settings[legacyKeyDevice].(string); slices.Contains(kinds, kind) {
		device.IconKey = kind

		return
	}

	image := parseLegacyStyle(cell.style).imageName()

	for _, kind := range kinds {
		if strings.HasPrefix(image, kind+"_") {
			device.IconKey = kind

			return
		}
	}
}

// legacyCard is where a card of the given size sits so that its center is
// the center of the cell's rectangle.
func legacyCard(box legacyBox, width, height float64) Position {
	x, y := box.center()

	return legacySnap(x*legacyScale-width/2, y*legacyScale-height/2)
}

// legacySnap puts a point on the grid.
func legacySnap(x, y float64) Position {
	snap := func(value float64) float64 {
		// Adding zero turns a negative zero into zero.
		return math.Round(value/defaultGridSize)*defaultGridSize + 0
	}

	return Position{X: snap(x), Y: snap(y)}
}

// placeSwitches makes each switch cell a switch of its VLAN: the one
// generation made for the network, or a further one. The VLAN is the one
// the cell's settings name, or else its label.
func (o *legacyOverlay) placeSwitches() {
	for i, cell := range o.diagram.cells {
		if !cell.isSwitch() {
			continue
		}

		name, _ := cell.settings[keyName].(string)
		if strings.TrimSpace(name) == "" {
			name = cell.text()
		}

		name = o.notes.networkName(name)
		if name == "" {
			o.notes.unreadable = append(o.notes.unreadable, cell.shown())

			continue
		}

		network := o.network(name)
		hub := o.hubOf[network.ID]

		var node *Node

		switch {
		case hub != nil && !o.claimed[hub.ID]:
			node = hub
		case hub == nil:
			node = legacySwitch(SwitchNodeID(name), network)
			o.hubOf[network.ID] = node
			o.switches = append(o.switches, node)
		default:
			node = legacySwitch(LegacyNodeID(NodeKindSwitch, cell.id), network)
			o.switches = append(o.switches, node)
		}

		node.Position = legacyCard(o.boxes[i], legacySwitchWidth, legacySwitchHeight)
		o.roles[i], o.vlans[i] = legacySwitchCell, name
		o.claim(i, node)
		o.ids = append(o.ids, legacyVLAN{network: name, id: cell.settings[keyID]})
	}
}

// legacySwitch is a further switch of a network. Like every switch, it is
// labeled with the network's name: the editor names a switch after its
// network, so the name the legacy Builder gave the switch itself is dropped.
func legacySwitch(id string, network *Network) *Node {
	return &Node{ //nolint:exhaustruct // only switch nodes carry a switch payload
		ID:       id,
		Kind:     NodeKindSwitch,
		Label:    network.Name,
		Position: Position{X: 0, Y: 0},
		Switch:   &Switch{NetworkID: network.ID}, //nolint:exhaustruct // colors and notes are the editor's to set
	}
}

// network returns the document's network of a VLAN name, adding it when no
// interface names that VLAN.
func (o *legacyOverlay) network(name string) *Network {
	index, ok := o.networks[name]
	if !ok {
		index = len(o.doc.Networks)
		o.networks[name] = index
		o.doc.Networks = append(o.doc.Networks, Network{ //nolint:exhaustruct // aliases and presentation are optional
			ID: NetworkID(name), Name: name,
		})
	}

	return &o.doc.Networks[index]
}

// readLines reads the diagram's lines. A line holds no interface: the
// connections are those generation made from the interfaces. A line between
// a device and a switch tells which switch of the VLAN a connection goes to.
// A line between two devices names a VLAN that they share. Both can give the
// VLAN its ID. Any other line is left out.
func (o *legacyOverlay) readLines() {
	for _, cell := range o.diagram.cells {
		if !cell.edge {
			continue
		}

		source, target := o.cellIndex(cell.source), o.cellIndex(cell.target)

		switch from, to := o.role(source), o.role(target); {
		case from == legacyDeviceCell && to == legacySwitchCell:
			o.link(source, target, cell)
		case from == legacySwitchCell && to == legacyDeviceCell:
			o.link(target, source, cell)
		case from == legacyDeviceCell && to == legacyDeviceCell:
			if !o.direct(source, target, cell) {
				o.notes.lines++
			}
		default:
			o.notes.lines++
		}
	}
}

func (o *legacyOverlay) cellIndex(id string) int {
	if index, ok := o.diagram.byID[id]; ok && id != "" {
		return index
	}

	return -1
}

func (o *legacyOverlay) role(cell int) legacyRole {
	if cell < 0 {
		return legacyUnused
	}

	return o.roles[cell]
}

// link moves a connection of the device to the switch the line ends at. The
// first connection of the device on that VLAN that no other line used is
// taken. A device without an interface on the VLAN gets none.
func (o *legacyOverlay) link(device, hub int, line *legacyCell) {
	name := o.vlans[hub]
	o.ids = append(o.ids, legacyVLAN{network: name, id: line.settings[keyID]})

	node, target := o.byCell[device], o.byCell[hub]
	if node == nil || target == nil {
		return
	}

	edges := o.edgesOf[node.ID+"/"+target.Switch.NetworkID]
	if len(edges) == 0 {
		o.unlinked(node, name)

		return
	}

	for _, index := range edges {
		if o.taken[index] {
			continue
		}

		o.taken[index] = true

		if edge := &o.doc.Edges[index]; edge.TargetNodeID != target.ID {
			edge.TargetNodeID = target.ID
			edge.ID = EdgeID(edge.SourceNodeID, edge.SourceHandleID, edge.TargetNodeID, edge.TargetHandleID)
		}

		return
	}
}

// direct reads a line between two devices, which the legacy Builder drew
// for a VLAN that had no switch. It reports false for a line that names no
// VLAN, which is no link.
func (o *legacyOverlay) direct(first, second int, line *legacyCell) bool {
	name, _ := line.settings[keyName].(string)
	if strings.TrimSpace(name) == "" {
		return false
	}

	name = o.notes.networkName(name)
	index, exists := o.networks[name]

	if exists {
		o.ids = append(o.ids, legacyVLAN{network: name, id: line.settings[keyID]})
	}

	for _, cell := range []int{first, second} {
		node := o.byCell[cell]
		if node == nil {
			continue
		}

		if !exists || len(o.edgesOf[node.ID+"/"+o.doc.Networks[index].ID]) == 0 {
			o.unlinked(node, name)
		}
	}

	return true
}

func (o *legacyOverlay) unlinked(node *Node, network string) {
	o.notes.once(
		&o.notes.unlinked,
		cutBytes(node.Device.Hostname, maxLegacyShown)+" to "+cutBytes(network, maxLegacyShown),
	)
}

// assignVLANs makes the VLAN IDs of the diagram the aliases of its networks:
// those of the switch cells first, then those of the lines. The first usable
// ID of a network wins, and no two networks share one.
func (o *legacyOverlay) assignVLANs() {
	owners := map[int]string{}

	for i := range o.doc.Networks {
		if alias := o.doc.Networks[i].Alias; alias != nil {
			owners[*alias] = o.doc.Networks[i].Name
		}
	}

	note := o.notes.once

	for _, entry := range o.ids {
		id, state := legacyVLANID(entry.id)
		network := o.network(entry.network)
		name := cutBytes(network.Name, maxLegacyShown)

		switch {
		case state == legacyIDAutomatic:
		case state == legacyIDInvalid:
			note(&o.notes.badIDs, fmt.Sprintf(
				"VLAN ID %s of network %s is not a number from 1 to %d and was left out.",
				legacyShownValue(entry.id), name, maxVLANAlias,
			))
		case network.Alias != nil && *network.Alias == id:
		case network.Alias != nil:
			note(&o.notes.manyIDs, fmt.Sprintf(
				"Network %s has more than one VLAN ID in the legacy diagram. The first, %d, was kept.",
				name, *network.Alias,
			))
		case owners[id] != "":
			owner := cutBytes(owners[id], maxLegacyShown)

			note(&o.notes.sharedIDs, fmt.Sprintf(
				"VLAN ID %d is used by networks %s and %s. It was kept for %s only.", id, owner, name, owner,
			))
		default:
			network.Alias = &id
			owners[id] = network.Name
		}
	}
}

// What a VLAN ID of a legacy diagram says.
const (
	legacyIDAutomatic = iota
	legacyIDInvalid
	legacyIDUsable
)

// legacyVLANID reads the VLAN ID of a switch cell or a line. Nothing, 0, ""
// and "auto" leave the ID to phenix, as they did in the legacy Builder. A
// whole number from 1 to 4094, also as text, is the ID.
func legacyVLANID(value any) (int, int) {
	var id int

	switch typed := value.(type) {
	case nil:
		return 0, legacyIDAutomatic
	case int:
		id = typed
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || text == "auto" {
			return 0, legacyIDAutomatic
		}

		parsed, err := strconv.Atoi(text)
		if err != nil || strings.Trim(text, "0123456789") != "" {
			return 0, legacyIDInvalid
		}

		id = parsed
	default:
		return 0, legacyIDInvalid
	}

	switch {
	case id == 0:
		return 0, legacyIDAutomatic
	case id < 1 || id > maxVLANAlias:
		return 0, legacyIDInvalid
	}

	return id, legacyIDUsable
}

// legacyShownValue writes a refused VLAN ID in a warning.
func legacyShownValue(value any) string {
	text, ok := value.(string)
	if !ok {
		text = fmt.Sprint(value)
	}

	return cutBytes(text, maxLegacyShown)
}

// decorate makes groups and notes from the vertices without settings. A
// vertex that is the parent of another, and a plain rectangle around the
// center of a placed device or switch, is a group. Any other vertex with
// text is a note. The rest is left out. A label on a line goes with its
// line.
func (o *legacyOverlay) decorate() {
	cells := o.diagram.cells
	holder := make([]bool, len(cells))

	var placed []legacyBox

	for i, cell := range cells {
		if parent := o.parents[i]; parent >= 0 && cell.isVertex() {
			holder[parent] = true
		}

		if o.byCell[i] != nil {
			placed = append(placed, o.boxes[i])
		}
	}

	for i, cell := range cells {
		if !cell.isVertex() || cell.hasSettings {
			continue
		}

		if parent := o.parents[i]; parent >= 0 && cells[parent].edge {
			continue
		}

		box, text := o.boxes[i], cell.text()
		o.plain[i] = parseLegacyStyle(cell.style).rectangleLike()

		switch {
		case holder[i] || (o.plain[i] && legacyHolds(box, placed)):
			title, _, _ := strings.Cut(text, "\n")

			node := &Node{ //nolint:exhaustruct // only group nodes carry a group payload
				ID:       LegacyNodeID(NodeKindGroup, cell.id),
				Kind:     NodeKindGroup,
				Position: legacySnap(box.x*legacyScale, box.y*legacyScale),
				Size: &Size{
					Width:  math.Max(box.width*legacyScale, legacyGroupMinWidth),
					Height: math.Max(box.height*legacyScale, legacyGroupMinHeight),
				},
				Group: &Group{Title: cutBytes(strings.TrimSpace(title), MaxNameBytes)}, //nolint:exhaustruct // formatting is dropped
			}

			o.roles[i], o.byCell[i] = legacyGroupCell, node
			o.groups = append(o.groups, node)
		case text != "":
			node := &Node{ //nolint:exhaustruct // only note nodes carry a note payload
				ID:       NoteNodeID("legacy/" + cell.id),
				Kind:     NodeKindNote,
				Position: legacySnap(box.x*legacyScale, box.y*legacyScale),
				Size: &Size{
					Width:  math.Max(box.width*legacyScale, legacyNoteMinWidth),
					Height: math.Max(box.height*legacyScale, legacyNoteMinHeight),
				},
				Note: &Note{Text: text, Color: ""},
			}

			o.roles[i], o.byCell[i] = legacyNoteCell, node
			o.texts = append(o.texts, node)
		default:
			o.notes.shapes++
		}
	}

	o.notes.decorated = len(o.groups)+len(o.texts) > 0
}

// legacyHolds reports whether the center of one of the rectangles lies in
// box.
func legacyHolds(box legacyBox, placed []legacyBox) bool {
	for _, other := range placed {
		if box.contains(other.center()) {
			return true
		}
	}

	return false
}

// joinGroups puts the nodes in the groups. A cell whose parent cell became a
// group joins it. Any other cell joins the smallest plain rectangle that
// became a group and holds its center (of equal ones, the earlier cell). A
// group only joins a larger one, or an equal one before it, and never one
// that is inside it.
func (o *legacyOverlay) joinGroups() {
	var holders []int

	for i := range o.diagram.cells {
		o.groupOf[i] = -1

		if o.byCell[i] == nil {
			continue
		}

		if parent := o.parents[i]; parent >= 0 && o.roles[parent] == legacyGroupCell {
			o.groupOf[i] = parent
		}

		if o.roles[i] == legacyGroupCell && o.plain[i] {
			holders = append(holders, i)
		}
	}

	sort.SliceStable(holders, func(a, b int) bool {
		return o.boxes[holders[a]].area() < o.boxes[holders[b]].area()
	})

	o.limitGroupDepth()

	for i := range o.diagram.cells {
		if o.byCell[i] == nil || o.groupOf[i] >= 0 {
			continue
		}

		x, y := o.boxes[i].center()

		for _, holder := range holders {
			if holder != i && o.boxes[holder].contains(x, y) && o.mayJoin(i, holder) {
				o.groupOf[i] = holder

				break
			}
		}
	}

	o.limitGroupDepth()

	for i, group := range o.groupOf {
		if group >= 0 {
			o.byCell[i].ParentID = o.byCell[group].ID
		}
	}
}

// limitGroupDepth stops groups from nesting more than maxLegacyDepth levels,
// the bound on the XML itself. A cell that would be deeper stays at the top
// level. No diagram drawn by hand comes near this bound. It bounds the work
// that a made-up diagram can cause wherever the chain of groups is walked.
func (o *legacyOverlay) limitGroupDepth() {
	const unknown = -1

	depth := make([]int, len(o.groupOf))
	for i := range depth {
		depth[i] = unknown
	}

	var chain []int

	for i := range o.groupOf {
		chain = chain[:0]

		at := i
		for at >= 0 && depth[at] == unknown {
			chain = append(chain, at)
			at = o.groupOf[at]
		}

		for _, cell := range slices.Backward(chain) {
			switch group := o.groupOf[cell]; {
			case group < 0:
				depth[cell] = 0
			case depth[group] == maxLegacyDepth:
				o.groupOf[cell], depth[cell] = -1, 0
			default:
				depth[cell] = depth[group] + 1
			}
		}
	}
}

// mayJoin reports whether a cell may join a group by its position: a group
// only joins one that is larger, or as large and earlier, and no cell joins
// a group that is inside it.
func (o *legacyOverlay) mayJoin(cell, group int) bool {
	if o.roles[cell] == legacyGroupCell {
		inner, outer := o.boxes[cell].area(), o.boxes[group].area()
		if outer < inner || (outer == inner && group > cell) {
			return false
		}
	}

	for at := group; at >= 0; at = o.groupOf[at] {
		if at == cell {
			return false
		}
	}

	return true
}

// placeHubs places the switches that no switch cell stands for. The legacy
// Builder also joined two devices directly, and a topology may have gained a
// VLAN since. Such a switch sits between its placed devices. It moves down
// until its card covers no device and no other switch, and it joins the
// group of its devices when they share one. When it has no placed device, or
// no free place near them, [legacyOverlay.placeRest] places it.
func (o *legacyOverlay) placeHubs() {
	devices := map[string]*Node{}
	for _, node := range o.devices {
		devices[node.ID] = node
	}

	linked := map[string][]*Node{}

	for i := range o.doc.Edges {
		edge := &o.doc.Edges[i]

		if device := devices[edge.SourceNodeID]; device != nil {
			linked[edge.TargetNodeID] = append(linked[edge.TargetNodeID], device)
		}
	}

	cards := newLegacyCards()

	for _, node := range slices.Concat(o.devices, o.hubs, o.switches) {
		if o.claimed[node.ID] {
			cards.add(legacyCardBox(node))
		}
	}

	// resume is the end position of the last switch that started at a
	// position. The next switch continues from there.
	resume := map[Position]Position{}
	placed := 0

	for _, hub := range o.hubs {
		if o.claimed[hub.ID] {
			continue
		}

		o.notes.added = append(o.notes.added, cutBytes(hub.Label, maxLegacyShown))

		start, parent, found := o.between(linked[hub.ID])
		if !found || placed == maxLegacyCells {
			continue
		}

		hub.Position = start
		if last, again := resume[start]; again {
			hub.Position = Position{X: last.X, Y: last.Y + legacyNudge}
		}

		for nudges := 0; cards.covered(legacyCardBox(hub)) && nudges < legacyNudges; nudges++ {
			hub.Position.Y += legacyNudge
		}

		if cards.covered(legacyCardBox(hub)) {
			continue
		}

		placed++
		resume[start] = hub.Position
		hub.ParentID = parent
		o.claimed[hub.ID] = true

		cards.add(legacyCardBox(hub))
	}
}

// between is where a switch card sits so that its center is the middle of
// the cards of the placed devices among those given, and the group all of
// the devices are in, if there is one. It reports false when none of them
// is placed.
func (o *legacyOverlay) between(devices []*Node) (Position, string, bool) {
	var (
		x, y   float64
		count  int
		parent string
	)

	for i, device := range devices {
		if i == 0 {
			parent = device.ParentID
		}

		if device.ParentID != parent {
			parent = ""
		}

		if o.claimed[device.ID] {
			centerX, centerY := legacyCardBox(device).center()
			x, y = x+centerX, y+centerY
			count++
		}
	}

	if count == 0 {
		return Position{X: 0, Y: 0}, "", false
	}

	return legacySnap(
		x/float64(count)-legacySwitchWidth/2,
		y/float64(count)-legacySwitchHeight/2,
	), parent, true
}

// legacyCardBox is the rectangle the editor draws a device or a switch in.
func legacyCardBox(node *Node) legacyBox {
	box := legacyBox{x: node.Position.X, y: node.Position.Y, width: legacyDeviceWidth, height: legacyDeviceHeight}
	if node.Kind == NodeKindSwitch {
		box.width, box.height = legacySwitchWidth, legacySwitchHeight
	}

	return box
}

// legacyCards answers whether a card would cover one already placed. The
// cards are kept by the squares of a coarse grid they touch, so a question
// looks at the few cards near it.
type legacyCards struct {
	squares map[[2]int][]legacyBox
	known   map[legacyBox]bool
}

func newLegacyCards() *legacyCards {
	return &legacyCards{squares: map[[2]int][]legacyBox{}, known: map[legacyBox]bool{}}
}

// squaresOf calls visit for each square the box touches, until it returns
// true.
func (c *legacyCards) squaresOf(box legacyBox, visit func(square [2]int) bool) {
	const width, height = 256.0, 128.0

	for column := int(math.Floor(box.x / width)); column <= int(math.Floor((box.x+box.width)/width)); column++ {
		for row := int(math.Floor(box.y / height)); row <= int(math.Floor((box.y+box.height)/height)); row++ {
			if visit([2]int{column, row}) {
				return
			}
		}
	}
}

func (c *legacyCards) add(box legacyBox) {
	if c.known[box] {
		return
	}

	c.known[box] = true

	c.squaresOf(box, func(square [2]int) bool {
		c.squares[square] = append(c.squares[square], box)

		return false
	})
}

func (c *legacyCards) covered(box legacyBox) bool {
	covered := false

	c.squaresOf(box, func(square [2]int) bool {
		for _, other := range c.squares[square] {
			if box.x < other.x+other.width && other.x < box.x+box.width &&
				box.y < other.y+other.height && other.y < box.y+box.height {
				covered = true

				return true
			}
		}

		return false
	})

	return covered
}

// placeRest puts the nodes that the diagram did not place in a grid below
// everything that it placed: the devices, then the switches.
func (o *legacyOverlay) placeRest() {
	var (
		rest         []*Node
		left, bottom = math.Inf(1), math.Inf(-1)
	)

	for _, node := range slices.Concat(o.devices, o.hubs, o.switches, o.groups, o.texts) {
		if !o.claimed[node.ID] && node.Kind != NodeKindGroup && node.Kind != NodeKindNote {
			rest = append(rest, node)

			if node.Kind == NodeKindDevice && o.merge {
				o.notes.unplaced = append(o.notes.unplaced, cutBytes(node.Device.Hostname, maxLegacyShown))
			}

			continue
		}

		height := legacyDeviceHeight

		switch {
		case node.Size != nil:
			height = node.Size.Height
		case node.Kind == NodeKindSwitch:
			height = legacySwitchHeight
		}

		left = math.Min(left, node.Position.X)
		bottom = math.Max(bottom, node.Position.Y+height)
	}

	origin := Position{X: 0, Y: 0}
	if !math.IsInf(left, 0) {
		origin = legacySnap(left, bottom+layoutSpacingY)
	}

	for i, node := range rest {
		node.Position = Position{
			X: origin.X + float64(i%layoutColumns)*layoutSpacingX,
			Y: origin.Y + float64(i/layoutColumns)*layoutSpacingY,
		}
	}
}

// finish writes the nodes back in their order, sorts what the overlay
// changed as generation sorts it, and takes the grid setting of the diagram.
func (o *legacyOverlay) finish() {
	nodes := slices.Concat(o.devices, o.hubs, o.switches, o.groups, o.texts)
	o.doc.Nodes = make([]Node, 0, len(nodes))

	for _, node := range nodes {
		o.doc.Nodes = append(o.doc.Nodes, *node)
	}

	sort.SliceStable(o.doc.Networks, func(i, j int) bool {
		return networkBefore(o.doc.Networks[i].Name, o.doc.Networks[j].Name)
	})

	sort.SliceStable(o.doc.Edges, func(i, j int) bool {
		return o.doc.Edges[i].ID < o.doc.Edges[j].ID
	})

	if o.diagram.grid == "0" {
		o.doc.Grid.Enabled = false
	}

	// The positions are the diagram's, not those of a layout.
	o.doc.Layout = ""
}
