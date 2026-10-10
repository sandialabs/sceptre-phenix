package builder

import (
	"strings"
	"time"
)

const (
	// SchemaURI identifies the builder document schema. Documents that do not
	// carry this exact value are rejected by [Decode].
	SchemaURI = "https://phenix.sandia.gov/schemas/builder/v1"

	// SchemaRevision is the revision of [SchemaURI] understood by this package.
	// The revision is bumped for backwards compatible additions; the schema URI
	// is bumped for breaking changes. Revision 1 is changed in place until it
	// is first released.
	SchemaRevision = 1
)

const (
	// MaxUserBytes bounds a user a document names (see [Metadata.CreatedBy]
	// and [Metadata.UpdatedBy]). It is the bound the draft service puts on
	// the owner and the actors of a draft.
	MaxUserBytes = 256

	// TimeLayout is the one form a time in the document metadata takes (see
	// [Metadata.CreatedAt] and [Metadata.UpdatedAt]): RFC 3339 in UTC, whole
	// seconds, with a literal "Z". The editor checks the same form, so a
	// document is accepted or refused the same way on both sides.
	TimeLayout = "2006-01-02T15:04:05Z"

	// MaxDiagramNotes is the most notes a document's metadata may carry (see
	// [Metadata.Notes]). A switch carries at most as many (see
	// [Switch.Notes]).
	MaxDiagramNotes = 100

	// MaxDiagramNoteBytes bounds one of those notes, and one note of a
	// switch.
	MaxDiagramNoteBytes = 4096

	// MaxScenarios is the most Scenario configs a document may name (see
	// [Document.Scenarios]).
	MaxScenarios = 20

	// MaxScenarioNameBytes bounds the name of one of those configs. It is the
	// bound the draft service puts on the names a publication records, so the
	// scenario an experiment is published with can always be recorded.
	MaxScenarioNameBytes = 256

	// MinLinePoints and MaxLinePoints bound the points of a line node (see
	// [Line.Points]): its two ends, and at most 62 bends between them.
	MinLinePoints = 2
	MaxLinePoints = 64
)

// NodeKind enumerates the kinds of nodes a builder document can contain.
type NodeKind string

const (
	// NodeKindDevice is a phenix node (VM, container, external device, ...). It
	// is the only node kind that maps to a topology node.
	NodeKindDevice NodeKind = "device"
	// NodeKindSwitch is a visual hub representing a network (VLAN). Switches are
	// never written to a topology spec; they exist so device interfaces attached
	// to the same network share a single visual attachment point.
	NodeKindSwitch NodeKind = "switch"
	// NodeKindNote is free-floating annotation text with no phenix semantics.
	NodeKindNote NodeKind = "note"
	// NodeKindGroup is a visual container other nodes may be parented to. Groups
	// have no phenix semantics.
	NodeKindGroup NodeKind = "group"
	// NodeKindShape is a rectangle or a circle drawn on the canvas, with no
	// phenix semantics.
	NodeKindShape NodeKind = "shape"
	// NodeKindIcon is a built-in or custom icon drawn on the canvas, with no
	// phenix semantics.
	NodeKindIcon NodeKind = "icon"
	// NodeKindLine is a free polyline drawn on the canvas, tied to no node or
	// network, with no phenix semantics.
	NodeKindLine NodeKind = "line"
)

// SourceKind describes where a document originated.
type SourceKind string

const (
	// SourceKindManual marks a document authored in the builder.
	SourceKindManual SourceKind = "manual"
	// SourceKindTopology marks a document generated from a Topology config.
	SourceKindTopology SourceKind = "topology"
	// SourceKindExperiment marks a document generated from an Experiment config.
	SourceKindExperiment SourceKind = "experiment"
)

// Document is the root of the builder model. It is versioned by [Document.Schema]
// and [Document.Revision] and is safe to persist verbatim.
type Document struct {
	Schema   string `json:"$schema"`
	Revision int    `json:"revision"`
	// Metadata is what the document says of itself: its identity, name and
	// description, who made and last saved it and when, and its notes.
	Metadata Metadata  `json:"metadata"`
	Nodes    []Node    `json:"nodes"`
	Networks []Network `json:"networks"`
	Edges    []Edge    `json:"edges"`
	Viewport Viewport  `json:"viewport"`
	Grid     Grid      `json:"grid"`
	// Scenarios names the Scenario configs of the phenix store the diagram is
	// used with: at most [MaxScenarios], each a config name of at most
	// [MaxScenarioNameBytes], none named twice ignoring case. The document
	// carries no scenario content. Publishing adds the topology to the
	// "topology" annotation of each, and an experiment published with the
	// topology uses one of them.
	Scenarios []string `json:"scenarios,omitempty"`
	Source    *Source  `json:"source,omitempty"`
	// Layout is the id of the automatic layout that last laid this document
	// out, which the editor names in its layout menu. Empty, or an id the
	// editor does not know, means the positions were not made by a layout. It
	// is presentation only and never written to a config.
	Layout string `json:"layout,omitempty"`
	// Templates are the device templates saved with this diagram, which the
	// editor offers beside its own. They are presentation only and never
	// written to a config.
	Templates []Template `json:"templates,omitempty"`
	// Icons holds copies of custom icons the document's nodes and templates
	// name, by icon name (see [IconNameProblem]). On a phenix server a name
	// resolves through the server's icon library and a draft carries none;
	// a downloaded document carries every icon it uses, so it stands on its
	// own. A copy wins over the library's icon of its name. Presentation
	// only, never written to a config.
	Icons map[string]Icon `json:"icons,omitempty"`
}

// Metadata is the part of a [Document] that describes the document itself
// rather than the diagram on its canvas. It is document content and part of
// the document's digest; none of it is written to a config.
type Metadata struct {
	// ID is the document's identifier, a UUID (see [DocumentID]).
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// CreatedBy is the user who first made the document, and CreatedAt is
	// when, in [TimeLayout]. The draft service sets each when a draft is
	// created from a document that has none, and writes both into every
	// later snapshot of that draft (see phenix/api/builder). A document it
	// never stored may have neither.
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	// UpdatedBy is the user whose save stored this content, and UpdatedAt is
	// when, in [TimeLayout]. The draft service sets both on every save. They
	// are not [Source.UpdatedAt], which is a time of the source config.
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	// Notes are free text about the diagram as a whole, in the order the
	// editor lists them: at most [MaxDiagramNotes], each not blank, at most
	// [MaxDiagramNoteBytes] and free of control characters but newline and
	// tab.
	Notes []string `json:"notes,omitempty"`
}

// Node is a single item on the canvas. Exactly one of the kind-specific payload
// fields must be populated, matching Kind.
type Node struct {
	ID       string   `json:"id"`
	Kind     NodeKind `json:"kind"`
	Label    string   `json:"label,omitempty"`
	Position Position `json:"position"`
	Size     *Size    `json:"size,omitempty"`
	// ParentID optionally parents this node to a group node. Any node may be
	// free (no parent) or inside a group, groups included.
	ParentID string    `json:"parentId,omitempty"`
	Device   *Device   `json:"device,omitempty"`
	Switch   *Switch   `json:"switch,omitempty"`
	Note     *Note     `json:"note,omitempty"`
	Group    *Group    `json:"group,omitempty"`
	Shape    *Shape    `json:"shape,omitempty"`
	Icon     *IconNode `json:"icon,omitempty"`
	Line     *Line     `json:"line,omitempty"`
}

// Device carries the complete phenix semantics of a topology node.
type Device struct {
	// Hostname mirrors Spec["general"]["hostname"] and is the identity of the
	// device within the document.
	Hostname string `json:"hostname"`
	// IconKey is a builder-local presentation hint drawn from the bounded
	// icon key registry (see [IsIconKey]). It is never written to a topology
	// spec.
	IconKey string `json:"iconKey,omitempty"`
	// Icon is the name of a custom icon, drawn in place of IconKey: a copy
	// in [Document.Icons], else the server's icon library's icon of that
	// name; with neither, IconKey is drawn. Like the colors below, it is
	// presentation only and never written to a topology spec.
	Icon string `json:"icon,omitempty"`
	// OutlineColor and FillColor color the node's border and background, as
	// "#rrggbb". Empty leaves the editor's own.
	OutlineColor string `json:"outlineColor,omitempty"`
	FillColor    string `json:"fillColor,omitempty"`
	// Spec is the complete phenix node spec, using the stored (snake_case)
	// representation. Unknown keys are preserved verbatim so documents survive
	// schema growth without data loss.
	Spec map[string]any `json:"spec"`
	// Interfaces maps stable canvas handles onto interfaces of Spec.
	Interfaces []InterfaceHandle `json:"interfaces"`
	// IncludedFrom names the topology that defines the device when it came
	// from the source topology's includeTopologies (directly or through a
	// nested include). Such a device is shown for context only: it is never
	// written to a published topology, whose includeTopologies brings it back,
	// and the editor does not change it. Empty for the document's own devices.
	IncludedFrom string `json:"includedFrom,omitempty"`
}

// InterfaceHandle is a stable mapping between a canvas connection handle and a
// named interface of the owning device's spec.
type InterfaceHandle struct {
	ID string `json:"id"`
	// Name is the interface name within the device spec (e.g. "eth0").
	Name string `json:"name"`
	// Index is the position of the interface within the device spec's interface
	// list at the time the handle was created. It is presentation ordering only.
	Index int `json:"index"`
}

// Switch is the payload of a [NodeKindSwitch] node: a visual hub bound to
// exactly one network.
type Switch struct {
	NetworkID string `json:"networkId"`
	// OutlineColor and FillColor color the node's border and background, as
	// "#rrggbb". They are not the network's color, which its edges are drawn
	// in (see [Network.Color]).
	OutlineColor string `json:"outlineColor,omitempty"`
	FillColor    string `json:"fillColor,omitempty"`
	// Notes are free text about the switch, which the editor shows below it,
	// held to the rules of [Metadata.Notes]. A switch is no topology node, so
	// they stay in the document and are never written to a config; a
	// device's notes are its spec's general.notes, which are.
	Notes []string `json:"notes,omitempty"`
}

// Note is the payload of a [NodeKindNote] node.
type Note struct {
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
}

// Group is the payload of a [NodeKindGroup] node.
type Group struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	// BorderStyle is the pattern of the group's border, one of
	// [BorderStyles]. Empty leaves the editor's own.
	BorderStyle string `json:"borderStyle,omitempty"`
	// IconKey is drawn beside the title, from the icon key registry (see
	// [IsIconKey]), and Icon, the name of a custom icon (see [Device.Icon]),
	// in its place.
	IconKey   string `json:"iconKey,omitempty"`
	Icon      string `json:"icon,omitempty"`
	Collapsed bool   `json:"collapsed,omitempty"`
}

// Shape is the payload of a [NodeKindShape] node: a figure that fills the
// node's box, a rectangle or a circle (an ellipse when the box is not
// square), with a label at its center. It is presentation only and never
// written to a config.
type Shape struct {
	// Shape is the figure drawn, one of [ShapeFigures].
	Shape string `json:"shape"`
	Label string `json:"label,omitempty"`
	// FillColor and OutlineColor color the figure's inside and its border,
	// as "#rrggbb". Empty leaves the editor's own.
	FillColor    string `json:"fillColor,omitempty"`
	OutlineColor string `json:"outlineColor,omitempty"`
	// BorderStyle is the pattern of the figure's border, one of
	// [BorderStyles]. Empty leaves the editor's own.
	BorderStyle string `json:"borderStyle,omitempty"`
}

// IconNode is the payload of a [NodeKindIcon] node: an icon scaled to the
// node's box, with an optional label below it. It names exactly one icon:
// IconKey from the icon key registry (see [IsIconKey]), or Icon, a custom
// icon of [Document.Icons]. It is presentation only and never written to a
// config.
type IconNode struct {
	IconKey string `json:"iconKey,omitempty"`
	Icon    string `json:"icon,omitempty"`
	Label   string `json:"label,omitempty"`
}

// Line is the payload of a [NodeKindLine] node: a polyline tied to no node
// or network, drawn like a connection. It is presentation only and never
// written to a config.
type Line struct {
	// Points are the line's ends and bends in order, from [MinLinePoints] to
	// [MaxLinePoints] of them, relative to the node's position. The editor
	// keeps the node's position at the top left corner of the points' box,
	// and its size that box.
	Points []Position `json:"points"`
	Label  string     `json:"label,omitempty"`
	// Color is the line's color, as "#rrggbb". Empty leaves the editor's own.
	Color string `json:"color,omitempty"`
	// LineStyle is the line's dash pattern, one of [LineStyles]. Empty is
	// solid.
	LineStyle string `json:"lineStyle,omitempty"`
	// StartArrow and EndArrow draw an arrowhead at the first and the last
	// point.
	StartArrow bool `json:"startArrow,omitempty"`
	EndArrow   bool `json:"endArrow,omitempty"`
}

// Network is a canonical phenix network (VLAN).
type Network struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Alias is the optional integer VLAN alias published to an experiment's
	// vlans.aliases map. Nil means "unassigned".
	Alias       *int   `json:"alias,omitempty"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	// LineStyle is the dash pattern the network's edges are drawn in, one of
	// [LineStyles]. Empty leaves the pattern to the editor. Presentation
	// only.
	LineStyle string `json:"lineStyle,omitempty"`
}

// Edge attaches a device interface handle to a switch hub, and therefore to the
// switch's network.
type Edge struct {
	ID             string `json:"id"`
	SourceNodeID   string `json:"sourceNodeId"`
	SourceHandleID string `json:"sourceHandleId,omitempty"`
	TargetNodeID   string `json:"targetNodeId"`
	TargetHandleID string `json:"targetHandleId,omitempty"`
	// NetworkID must match the network of the switch endpoint.
	NetworkID string `json:"networkId"`
	Label     string `json:"label,omitempty"`
	// Color is drawn in place of the network's color. Like Label, it is
	// presentation only and never written to a topology spec.
	Color string `json:"color,omitempty"`
	// LineStyle is drawn in place of the network's line style, as Color is in
	// place of its color.
	LineStyle string `json:"lineStyle,omitempty"`
	// Route is the path an automatic layout drew for the edge, in absolute
	// canvas coordinates from the source handle to the target handle. The
	// editor drops it once either end moves. Presentation only.
	Route []Position `json:"route,omitempty"`
}

// Position is a canvas coordinate.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Size is an optional explicit canvas size.
type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Viewport records the canvas pan/zoom state.
type Viewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}

// Grid records canvas grid settings.
type Grid struct {
	Enabled bool    `json:"enabled"`
	Size    float64 `json:"size"`
	Snap    bool    `json:"snap"`
}

// Source records document provenance and any warnings raised while generating
// it.
type Source struct {
	Kind       SourceKind `json:"kind"`
	Name       string     `json:"name,omitempty"`
	APIVersion string     `json:"apiVersion,omitempty"`
	// Topology is the name of the topology an imported experiment was built
	// from, when known.
	Topology   string `json:"topology,omitempty"`
	ImportedAt string `json:"importedAt,omitempty"`
	// Digest is the "sha256:<hex>" digest of the source config identity and
	// spec, and of the legacy Builder diagram of a topology that has one, as
	// returned by [ImportDigest]. Publishing compares it against the current
	// stored config to detect a stale working copy.
	Digest string `json:"digest,omitempty"`
	// UpdatedAt is the metadata.updated timestamp of the source config at
	// import time. It is informational; [Source.Digest] is authoritative.
	UpdatedAt string `json:"updatedAt,omitempty"`
	// IncludeTopologies preserves the source topology's includeTopologies, so
	// publishing writes the references back instead of flattening the included
	// devices (see [Device.IncludedFrom]) into the topology.
	IncludeTopologies []string `json:"includeTopologies,omitempty"`
	// UnresolvedIncludes lists the included topologies, at any depth, whose
	// nodes are not in the document: they could not be read when it was
	// generated. Combining keeps exactly these in IncludeTopologies.
	UnresolvedIncludes []string `json:"unresolvedIncludes,omitempty"`
	// Annotations are the source config's metadata.annotations at import time,
	// such as an experiment's topology and scenario, without the Builders' own
	// (see [IsBuilderAnnotation]). They are informational: [Source.Digest]
	// leaves them out, and publishing never writes them.
	Annotations map[string]string `json:"annotations,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
}

// NewDocument returns an empty, valid document with a deterministic ID derived
// from name.
func NewDocument(name string) *Document {
	return &Document{ //nolint:exhaustruct // optional sections start empty
		Schema:   SchemaURI,
		Revision: SchemaRevision,
		Metadata: Metadata{ //nolint:exhaustruct // provenance and notes start empty
			ID:   DocumentID(name),
			Name: name,
		},
		Nodes:    []Node{},
		Networks: []Network{},
		Edges:    []Edge{},
		Viewport: Viewport{X: 0, Y: 0, Zoom: 1},
		Grid:     Grid{Enabled: true, Size: defaultGridSize, Snap: true},
	}
}

// Provenance is who made a document and who last saved it, and when: the
// four metadata fields the draft service sets (see [Metadata.CreatedBy] and
// [Metadata.UpdatedBy]). An empty field is one the document does not have.
type Provenance struct {
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// FormatTime returns t as a time of the document metadata, in
// [TimeLayout]: in UTC, cut to whole seconds.
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

// Provenance returns who made the document and when, and who last saved it
// and when.
func (d *Document) Provenance() Provenance {
	return Provenance{
		CreatedBy: d.Metadata.CreatedBy,
		CreatedAt: d.Metadata.CreatedAt,
		UpdatedBy: d.Metadata.UpdatedBy,
		UpdatedAt: d.Metadata.UpdatedAt,
	}
}

// SetProvenance replaces who made the document and when, and who last saved
// it and when. An empty field removes the one the document had.
func (d *Document) SetProvenance(provenance Provenance) {
	d.Metadata.CreatedBy = provenance.CreatedBy
	d.Metadata.CreatedAt = provenance.CreatedAt
	d.Metadata.UpdatedBy = provenance.UpdatedBy
	d.Metadata.UpdatedAt = provenance.UpdatedAt
}

// NodeByID returns the node with the given ID, or nil.
func (d *Document) NodeByID(id string) *Node {
	for i := range d.Nodes {
		if d.Nodes[i].ID == id {
			return &d.Nodes[i]
		}
	}

	return nil
}

// NetworkByID returns the network with the given ID, or nil.
func (d *Document) NetworkByID(id string) *Network {
	for i := range d.Networks {
		if d.Networks[i].ID == id {
			return &d.Networks[i]
		}
	}

	return nil
}

// FindDevice returns the device node owning the given hostname, matched
// case-insensitively, or nil.
func (d *Document) FindDevice(hostname string) *Node {
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.Kind == NodeKindDevice && n.Device != nil &&
			strings.EqualFold(n.Device.Hostname, hostname) {
			return n
		}
	}

	return nil
}

// InterfaceHandle returns the handle with the given ID owned by this device, or
// nil.
func (dev *Device) InterfaceHandle(id string) *InterfaceHandle {
	if dev == nil {
		return nil
	}

	for i := range dev.Interfaces {
		if dev.Interfaces[i].ID == id {
			return &dev.Interfaces[i]
		}
	}

	return nil
}
