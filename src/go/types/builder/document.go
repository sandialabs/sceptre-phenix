package builder

import (
	"strings"

	"phenix/store"
	"phenix/types/version"
)

const (
	// SchemaURI identifies the builder document schema. Documents that do not
	// carry this exact value are rejected by [Decode].
	SchemaURI = "https://phenix.sandia.gov/schemas/builder/v1"

	// SchemaRevision is the revision of [SchemaURI] understood by this package.
	// The revision is bumped for backwards compatible additions; the schema URI
	// is bumped for breaking changes.
	SchemaRevision = 1
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

// ScenarioRefKind describes how a scenario is referenced by a document.
type ScenarioRefKind string

const (
	// ScenarioRefStored references a scenario config held in the phenix store by
	// name. The content may additionally be cached in the document.
	ScenarioRefStored ScenarioRefKind = "stored"
	// ScenarioRefUploaded references scenario content uploaded (or embedded from
	// an experiment) and carried inside the document.
	ScenarioRefUploaded ScenarioRefKind = "uploaded"
)

// Document is the root of the builder model. It is versioned by [Document.Schema]
// and [Document.Revision] and is safe to persist verbatim.
type Document struct {
	Schema      string       `json:"$schema"`
	Revision    int          `json:"revision"`
	ID          string       `json:"id"`
	Name        string       `json:"name,omitempty"`
	Description string       `json:"description,omitempty"`
	Nodes       []Node       `json:"nodes"`
	Networks    []Network    `json:"networks"`
	Edges       []Edge       `json:"edges"`
	Viewport    Viewport     `json:"viewport"`
	Grid        Grid         `json:"grid"`
	Scenario    *ScenarioRef `json:"scenario,omitempty"`
	Source      *Source      `json:"source,omitempty"`
}

// Node is a single item on the canvas. Exactly one of the kind-specific payload
// fields must be populated, matching Kind.
type Node struct {
	ID       string   `json:"id"`
	Kind     NodeKind `json:"kind"`
	Label    string   `json:"label,omitempty"`
	Position Position `json:"position"`
	Size     *Size    `json:"size,omitempty"`
	// ParentID optionally parents this node to a group node. Notes and groups
	// may be free (no parent) or nested inside another group.
	ParentID string  `json:"parentId,omitempty"`
	Device   *Device `json:"device,omitempty"`
	Switch   *Switch `json:"switch,omitempty"`
	Note     *Note   `json:"note,omitempty"`
	Group    *Group  `json:"group,omitempty"`
}

// Device carries the complete phenix semantics of a topology node.
type Device struct {
	// Hostname mirrors Spec["general"]["hostname"] and is the identity of the
	// device within the document.
	Hostname string `json:"hostname"`
	// IconKey is a builder-local presentation hint drawn from the bounded
	// registry returned by [IconKeys]. It is never written to a topology spec.
	IconKey string `json:"iconKey,omitempty"`
	// Spec is the complete phenix node spec, using the stored (snake_case)
	// representation. Unknown keys are preserved verbatim so documents survive
	// schema growth without data loss.
	Spec map[string]any `json:"spec"`
	// Interfaces maps stable canvas handles onto interfaces of Spec.
	Interfaces []InterfaceHandle `json:"interfaces"`
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
}

// Note is the payload of a [NodeKindNote] node.
type Note struct {
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
}

// Group is the payload of a [NodeKindGroup] node.
type Group struct {
	Title     string `json:"title,omitempty"`
	Color     string `json:"color,omitempty"`
	Collapsed bool   `json:"collapsed,omitempty"`
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

// ScenarioRef optionally binds a scenario to the document.
type ScenarioRef struct {
	Kind ScenarioRefKind `json:"kind"`
	// Name is the stored config name for [ScenarioRefStored], and the original
	// file name (optional) for [ScenarioRefUploaded].
	Name string `json:"name,omitempty"`
	// Content is the scenario spec. Required for [ScenarioRefUploaded], optional
	// (cached) for [ScenarioRefStored]. Whenever it is present, Document.Validate
	// validates it against the phenix scenario schema for [ScenarioAPIVersion].
	Content map[string]any `json:"content,omitempty"`
	// APIVersion is the scenario config apiVersion the reference was taken
	// from. It is required for both reference kinds; see [ScenarioAPIVersion].
	APIVersion string `json:"apiVersion,omitempty"`
	// Digest is the content digest, as produced by [ContentDigest]. It is
	// required for both reference kinds, and must match Content whenever
	// Content is present.
	Digest string `json:"digest,omitempty"`
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
	// spec, as returned by [SourceDigest]. Publishing compares it against the
	// current stored config to detect a stale working copy.
	Digest string `json:"digest,omitempty"`
	// UpdatedAt is the metadata.updated timestamp of the source config at
	// import time. It is informational; [Source.Digest] is authoritative.
	UpdatedAt string   `json:"updatedAt,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

// NewDocument returns an empty, valid document with a deterministic ID derived
// from name.
func NewDocument(name string) *Document {
	return &Document{ //nolint:exhaustruct // optional sections start empty
		Schema:   SchemaURI,
		Revision: SchemaRevision,
		ID:       DocumentID(name),
		Name:     name,
		Nodes:    []Node{},
		Networks: []Network{},
		Edges:    []Edge{},
		Viewport: Viewport{X: 0, Y: 0, Zoom: 1},
		Grid:     Grid{Enabled: true, Size: defaultGridSize, Snap: true},
	}
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

// NetworkByName returns the network with the given name, matched
// case-insensitively, or nil.
func (d *Document) NetworkByName(name string) *Network {
	for i := range d.Networks {
		if strings.EqualFold(d.Networks[i].Name, name) {
			return &d.Networks[i]
		}
	}

	return nil
}

// DeviceNodes returns all device nodes in document order.
func (d *Document) DeviceNodes() []*Node {
	return d.nodesOfKind(NodeKindDevice)
}

// SwitchNodes returns all switch nodes in document order.
func (d *Document) SwitchNodes() []*Node {
	return d.nodesOfKind(NodeKindSwitch)
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

func (d *Document) nodesOfKind(kind NodeKind) []*Node {
	var nodes []*Node

	for i := range d.Nodes {
		if d.Nodes[i].Kind == kind {
			nodes = append(nodes, &d.Nodes[i])
		}
	}

	return nodes
}

// ScenarioAPIVersion returns the config apiVersion of scenario content carried
// by a [ScenarioRef]. Scenario content is always imported from a config spec in
// the latest stored representation, so it matches the latest stored scenario
// version.
func ScenarioAPIVersion() string {
	return store.APIGroup + "/" + version.StoredVersion[kindScenario]
}
