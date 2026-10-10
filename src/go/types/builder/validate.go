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
	"time"
)

// maxVLANAlias is the largest 802.1Q VLAN ID an alias may take.
const maxVLANAlias = 4094

// MaxNameBytes bounds the document name, which the draft service records as
// the draft title.
const MaxNameBytes = 512

// Bounds on the source config annotations a document carries (see
// [Source.Annotations]). They keep the content that the document only shows
// to a small part of the 5 MiB that a stored document can take. An annotation
// key is bounded like the document name.
const (
	// MaxAnnotations is the most annotations a document's source may carry.
	MaxAnnotations = 100

	// MaxAnnotationBytes bounds the keys and values of those annotations
	// together (256 KiB).
	MaxAnnotationBytes = 256 << 10

	// maxAnnotationKiB is [MaxAnnotationBytes] in KiB, as messages give it.
	maxAnnotationKiB = MaxAnnotationBytes >> 10
)

// minRoutePoints is the fewest points an edge route may hold: its two ends.
const minRoutePoints = 2

// hexColorPattern matches the one form an outline or fill color takes:
// "#rrggbb", in either case.
const hexColorPattern = `#[0-9a-fA-F]{6}`

var (
	hexColor = regexp.MustCompile(`^` + hexColorPattern + `$`)

	// configName matches the names phenix gives configs, which a document
	// names its scenarios by (see [IsConfigName]).
	configNameRegexp = regexp.MustCompile(configNamePattern)

	// elementPath matches the start of the path of an issue about a node, an
	// edge or a network: the list and the index (see [Document.LocateIssues]).
	elementPath = regexp.MustCompile(`^(nodes|edges|networks)\[(\d+)\]`)

	// lineStyles are the dash patterns a network, an edge or a line may
	// name, and borderStyles the border patterns a group or a shape may. In
	// each, none (the empty string) leaves the pattern to the editor.
	lineStyles   = []string{"solid", "dashed", "dotted", "dash-dot"} //nolint:gochecknoglobals // immutable list
	borderStyles = []string{"solid", "dashed", "dotted", "double"}   //nolint:gochecknoglobals // immutable list

	// shapeFigures are the figures a shape node may draw.
	shapeFigures = []string{"rectangle", "circle"} //nolint:gochecknoglobals // immutable list

	// iconSizes are the sizes node icons may be drawn at, smallest first.
	iconSizes = []string{IconSizeSmall, IconSizeMedium, IconSizeLarge} //nolint:gochecknoglobals // immutable list

	// purdueLevels are the levels of the Purdue model a device or a switch
	// may name, from the top (the enterprise network) to the bottom (the
	// physical process). Level 3.5 is the industrial DMZ.
	purdueLevels = []string{"5", "4", "3.5", "3", "2", "1", "0"} //nolint:gochecknoglobals // immutable list
)

// The sizes the editor draws the icon of a device, a switch or a group at:
// 16, 24 and 32 pixels. Small is the size of a document that names none.
const (
	IconSizeSmall  = "small"
	IconSizeMedium = "medium"
	IconSizeLarge  = "large"
)

// IconSizes returns the sizes [Document.IconSize], [Device.IconSize],
// [Switch.IconSize] and [Group.IconSize] may name, smallest first.
func IconSizes() []string {
	return slices.Clone(iconSizes)
}

// PurdueLevels returns the levels of the Purdue model [Device.PurdueLevel],
// [Switch.PurdueLevel] and [TemplateDevice.PurdueLevel] may name, from the
// top level to the bottom one.
func PurdueLevels() []string {
	return slices.Clone(purdueLevels)
}

// LineStyles returns the dash patterns [Network.LineStyle],
// [Edge.LineStyle] and [Line.LineStyle] may name.
func LineStyles() []string {
	return slices.Clone(lineStyles)
}

// BorderStyles returns the border patterns [Group.BorderStyle] and
// [Shape.BorderStyle] may name.
func BorderStyles() []string {
	return slices.Clone(borderStyles)
}

// ShapeFigures returns the figures [Shape.Shape] may name.
func ShapeFigures() []string {
	return slices.Clone(shapeFigures)
}

// Issue is a single problem found in a document, a template file or a
// publication. It holds the [Code] of the rule it breaks, its [Severity], its
// message, and its location, by the parts that are known:
//
//   - Path is a JSON-like path in the document (nodes[0].device.hostname).
//   - NodeID, EdgeID and NetworkID name the element that the issue is about.
//     They still find it after an edit moves the indexes in the path.
//   - Field is the JSON Forms data path of the device field that the issue
//     is about (spec.hardware.drives.0.image).
//
// The validator of the web UI reports its issues in the same shape, with the
// same codes for the same rules.
type Issue struct {
	Code      Code     `json:"code"`
	Severity  Severity `json:"severity"`
	Message   string   `json:"message"`
	Path      string   `json:"path,omitempty"`
	NodeID    string   `json:"nodeId,omitempty"`
	EdgeID    string   `json:"edgeId,omitempty"`
	NetworkID string   `json:"networkId,omitempty"`
	Field     string   `json:"field,omitempty"`
}

func (i Issue) String() string {
	if i.Path == "" {
		return i.Message
	}

	return i.Path + ": " + i.Message
}

// ValidationError aggregates every validation failure found in a document. It
// unwraps to [ErrInvalidDocument].
type ValidationError struct {
	Issues []Issue `json:"issues"`
}

func (e *ValidationError) Error() string {
	messages := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		messages[i] = issue.String()
	}

	return fmt.Sprintf("%s: %s", ErrInvalidDocument.Error(), strings.Join(messages, "; "))
}

// Unwrap allows [errors.Is](err, ErrInvalidDocument).
func (e *ValidationError) Unwrap() error {
	return ErrInvalidDocument
}

type validator struct {
	doc    *Document
	issues []Issue

	nodesByID    map[string]*Node
	networksByID map[string]*Network
	handleOwner  map[string]*Node
}

// noteCodes are the codes of the rules of the notes of the diagram or of a
// switch (see [validator.validateNotes]).
type noteCodes struct {
	tooMany, blank, tooLong, control Code
}

// Validate performs structural and semantic validation of the document.
//
// Validate does not check size limits (counts, lengths, payload sizes) on
// purpose. The API layer checks them. The exceptions are the bounds that the
// editor also checks, with the same rules, before it saves: the bounds on the
// names and notes of the metadata, the scenarios, the source annotations, the
// templates and the custom icons. Validate rejects:
//
//   - wrong schema URI or revision,
//   - a document name longer than [MaxNameBytes] or containing control
//     characters, which the draft service could not record as a title,
//   - a createdBy or updatedBy longer than [MaxUserBytes] or containing
//     control characters,
//   - a createdAt or updatedAt that is not a time in [TimeLayout],
//   - more than [MaxDiagramNotes] notes, and a note that is blank, longer
//     than [MaxDiagramNoteBytes] or holds a control character other than a
//     newline or a tab, in the metadata or on a switch (a device's notes are
//     its spec's general.notes, which the phenix schema checks when the
//     document is published, as it checks the rest of the spec),
//   - missing or null nodes, networks, or edges (the editor requires arrays,
//     empty when there is nothing in them),
//   - identifiers that are not RFC 4122 UUIDs, and duplicate
//     node/network/edge/handle identifiers (case-insensitive),
//   - duplicate device hostnames (case-insensitive),
//   - nodes whose payload does not match their kind,
//   - dangling parent/network/node/handle references and group cycles,
//   - invalid edge topology (an edge must join one device handle to one switch),
//   - interfaces attached to more than one network,
//   - conflicting network names (compared exactly, as minimega compares
//     VLAN names) or VLAN aliases,
//   - more than [MaxScenarios] scenarios, a scenario name that is not a
//     config name of at most [MaxScenarioNameBytes], and a scenario named
//     twice (case-insensitive),
//   - icon keys outside the bounded icon key registry (see [IsIconKey]), on
//     a device or a group,
//   - an outline or fill color of a device, a switch or a shape, and the
//     color of a line, that is not "#rrggbb",
//   - a group or shape border style outside [BorderStyles], and a network,
//     edge or line style outside [LineStyles],
//   - an icon size outside [IconSizes], of the document, a device, a switch,
//     a group or a template,
//   - a Purdue level outside [PurdueLevels], of a device, a switch or a
//     template,
//   - a shape whose figure is not one of [ShapeFigures], an icon node that
//     does not name exactly one of a built-in and a custom icon, and a line
//     with fewer than [MinLinePoints] or more than [MaxLinePoints] points,
//   - custom icons [ValidateIcons] refuses, and a device, group, icon node
//     or template whose custom icon is not an icon name (see
//     [IconNameProblem]),
//   - more than [MaxTemplates] templates, a template whose id is not a UUID
//     or is used twice (case-insensitive), and one [Template.Issues]
//     refuses,
//   - devices marked [Device.IncludedFrom] with a malformed topology name, or
//     in a document whose source includes no topologies,
//   - a malformed [Source.Digest], and a blank or whitespace-holding entry
//     of [Source.IncludeTopologies] or [Source.UnresolvedIncludes],
//   - source annotations beyond [MaxAnnotations] or [MaxAnnotationBytes], or
//     with a blank key, a key longer than [MaxNameBytes] or one containing
//     control characters,
//   - non-finite geometry (line points included), and sizes, zoom, or grid
//     spacing that are not strictly positive,
//   - an edge route with fewer than two points.
//
// It returns nil or a *[ValidationError], whose issues each carry the code
// of the rule they break and the IDs of the node, edge or network they are
// about (see [Document.LocateIssues]).
func (d *Document) Validate() error {
	val := &validator{ //nolint:exhaustruct // issues accumulate during validation
		doc:          d,
		nodesByID:    map[string]*Node{},
		networksByID: map[string]*Network{},
		handleOwner:  map[string]*Node{},
	}

	val.validateHeader()
	val.validateNetworks()
	val.validateNodes()
	val.validateParents()
	val.validateEdges()
	val.validateScenarios()
	val.validateSource()
	val.validateTemplates()

	val.issues = append(val.issues, ValidateIcons(d.Icons, keyIcons)...)

	if len(val.issues) == 0 {
		return nil
	}

	sort.SliceStable(val.issues, func(i, j int) bool {
		return val.issues[i].Path < val.issues[j].Path
	})

	d.LocateIssues(val.issues)

	return &ValidationError{Issues: val.issues}
}

// LocateIssues gives the ID of an element to each issue whose path starts at
// a node, an edge or a network of the document, as the editor locates the
// issues it finds. Thus an issue still names its element after an edit moves
// the indexes in its path. It does not give a blank ID. An issue that already
// names an element keeps it.
func (d *Document) LocateIssues(issues []Issue) {
	for i := range issues {
		d.locateIssue(&issues[i])
	}
}

// locateIssue gives one issue the ID of the element its path starts at (see
// [Document.LocateIssues]).
func (d *Document) locateIssue(issue *Issue) {
	match := elementPath.FindStringSubmatch(issue.Path)
	if match == nil {
		return
	}

	index, err := strconv.Atoi(match[2])
	if err != nil {
		return
	}

	switch match[1] {
	case keyNodes:
		if index < len(d.Nodes) && issue.NodeID == "" && strings.TrimSpace(d.Nodes[index].ID) != "" {
			issue.NodeID = d.Nodes[index].ID
		}
	case keyEdges:
		if index < len(d.Edges) && issue.EdgeID == "" && strings.TrimSpace(d.Edges[index].ID) != "" {
			issue.EdgeID = d.Edges[index].ID
		}
	case keyNetworks:
		if index < len(d.Networks) && issue.NetworkID == "" && strings.TrimSpace(d.Networks[index].ID) != "" {
			issue.NetworkID = d.Networks[index].ID
		}
	}
}

func (v *validator) addf(code Code, path, format string, args ...any) {
	v.issues = append(v.issues, NewIssue(code, path, fmt.Sprintf(format, args...)))
}

func (v *validator) validateHeader() {
	if v.doc.Schema != SchemaURI {
		v.addf(CodeDocumentSchemaMismatch, "schema", "expected %q, got %q", SchemaURI, v.doc.Schema)
	}

	if v.doc.Revision != SchemaRevision {
		v.addf(CodeDocumentRevisionMismatch, "revision", "expected %d, got %d", SchemaRevision, v.doc.Revision)
	}

	v.validateMetadata()

	// decodeDocument in the front end's decode.js refuses a document without
	// these arrays, so one stored here could never be opened.
	for name, missing := range map[string]bool{
		keyNodes:    v.doc.Nodes == nil,
		keyNetworks: v.doc.Networks == nil,
		keyEdges:    v.doc.Edges == nil,
	} {
		if missing {
			v.addf(CodeDocumentListMissing, name, "%s must be an array", name)
		}
	}

	if !finite(v.doc.Viewport.X) || !finite(v.doc.Viewport.Y) || !finite(v.doc.Viewport.Zoom) {
		v.addf(CodeDocumentViewportNotFinite, "viewport", "viewport values must be finite numbers")
	}

	if finite(v.doc.Viewport.Zoom) && v.doc.Viewport.Zoom <= 0 {
		v.addf(CodeDocumentZoomNotPositive, "viewport.zoom", "zoom must be a positive number")
	}

	if !finite(v.doc.Grid.Size) || v.doc.Grid.Size <= 0 {
		v.addf(CodeDocumentGridInvalid, "grid.size", "grid size must be a positive finite number")
	}

	v.validateIconSize(v.doc.IconSize, keyIconSize, CodeDocumentIconSizeUnknown)
}

// metadataPath is the path of a field of the document metadata.
func metadataPath(key string) string {
	return keyMetadata + "." + key
}

// validateMetadata checks the document metadata: its identifier, the name,
// the users and times it names, and its notes.
func (v *validator) validateMetadata() {
	meta := &v.doc.Metadata

	v.validateID(metadataPath(keyID), "document", meta.ID, CodeMetadataIDRequired, CodeMetadataIDInvalid)

	switch {
	case len(meta.Name) > MaxNameBytes:
		v.addf(CodeMetadataNameTooLong, metadataPath(keyName), "document name must be at most %d bytes", MaxNameBytes)
	case strings.ContainsFunc(meta.Name, isControl):
		v.addf(CodeMetadataNameControl, metadataPath(keyName), "document name must not contain control characters")
	}

	v.validateUser(metadataPath(keyCreatedBy), meta.CreatedBy)
	v.validateTime(metadataPath(keyCreatedAt), meta.CreatedAt)
	v.validateUser(metadataPath(keyUpdatedBy), meta.UpdatedBy)
	v.validateTime(metadataPath(keyUpdatedAt), meta.UpdatedAt)
	v.validateNotes(metadataPath(keyNotes), meta.Notes, noteCodes{
		tooMany: CodeMetadataNotesTooMany, blank: CodeMetadataNoteBlank,
		tooLong: CodeMetadataNoteTooLong, control: CodeMetadataNoteControl,
	})
}

// isControl reports whether r is a control character, which no single line
// of text the document metadata carries may hold.
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// isNoteControl reports whether r is a control character a diagram note
// may not hold: any other than the newline and the tab, which multiline text
// needs.
func isNoteControl(r rune) bool {
	return isControl(r) && r != '\n' && r != '\t'
}

// validateNotes checks the notes at path, of the diagram or of a switch: at
// most [MaxDiagramNotes], each not blank, at most [MaxDiagramNoteBytes], and
// free of control characters other than newlines and tabs. codes are the
// codes of the owner of the notes.
func (v *validator) validateNotes(at string, notes []string, codes noteCodes) {
	if len(notes) > MaxDiagramNotes {
		v.addf(codes.tooMany, at, "at most %d notes are allowed, not %d", MaxDiagramNotes, len(notes))
	}

	for i, note := range notes {
		path := fmt.Sprintf("%s[%d]", at, i)

		switch {
		case strings.TrimSpace(note) == "":
			v.addf(codes.blank, path, "note must not be blank")
		case len(note) > MaxDiagramNoteBytes:
			v.addf(codes.tooLong, path, "note must be at most %d bytes", MaxDiagramNoteBytes)
		case strings.ContainsFunc(note, isNoteControl):
			v.addf(codes.control, path, "note must not contain control characters other than newline and tab")
		}
	}
}

// validateUser checks a user the document metadata names: bounded like the
// owner of a draft, and free of control characters. The name itself is not
// checked against the names this server issues, since a document made on
// another server may name a user of that one.
func (v *validator) validateUser(path, user string) {
	switch {
	case len(user) > MaxUserBytes:
		v.addf(CodeMetadataUserTooLong, path, "%s must be at most %d bytes", path, MaxUserBytes)
	case strings.ContainsFunc(user, isControl):
		v.addf(CodeMetadataUserControl, path, "%s must not contain control characters", path)
	}
}

// IsTime reports whether value is a time in exactly the form of
// [TimeLayout]. Parsing alone accepts a fraction of a second, so the value
// must also be what formatting the parsed time gives back.
func IsTime(value string) bool {
	parsed, err := time.Parse(TimeLayout, value)

	return err == nil && parsed.Format(TimeLayout) == value
}

// validateTime checks a time of the document metadata, when it has one.
func (v *validator) validateTime(path, value string) {
	if value != "" && !IsTime(value) {
		v.addf(CodeMetadataTimeInvalid, path, "%s must be a UTC time in the form YYYY-MM-DDTHH:MM:SSZ", path)
	}
}

func (v *validator) validateNetworks() {
	seenIDs := map[string]int{}
	seenNames := map[string]int{}
	seenAliases := map[int]int{}

	for i := range v.doc.Networks {
		network := &v.doc.Networks[i]
		path := fmt.Sprintf("networks[%d]", i)

		if v.validateID(path+".id", "network", network.ID, CodeNetworkIDRequired, CodeNetworkIDInvalid) {
			if prev, ok := seenIDs[foldKey(network.ID)]; ok {
				v.addf(CodeNetworkIDDuplicate, path+".id", "duplicate network ID %q (also networks[%d])", network.ID, prev)
			} else {
				seenIDs[foldKey(network.ID)] = i
				v.networksByID[network.ID] = network
			}
		}

		switch {
		case strings.TrimSpace(network.Name) == "":
			v.addf(CodeNetworkNameRequired, path+".name", "network name is required")
		case strings.ContainsAny(network.Name, " \t\n"):
			v.addf(CodeNetworkNameWhitespace, path+".name", "network name %q must not contain whitespace", network.Name)
		default:
			// Exactly: minimega VLAN names are case sensitive, so networks
			// differing only by case are different VLANs.
			if prev, ok := seenNames[network.Name]; ok {
				v.addf(
					CodeNetworkNameDuplicate,
					path+".name",
					"conflicting network name %q (also networks[%d])",
					network.Name, prev,
				)
			} else {
				seenNames[network.Name] = i
			}
		}

		v.validateLineStyle(network.LineStyle, path+".lineStyle", CodeNetworkLineStyle)

		if network.Alias == nil {
			continue
		}

		alias := *network.Alias

		if alias < 1 || alias > maxVLANAlias {
			v.addf(CodeNetworkAliasRange, path+".alias", "VLAN alias %d is out of range (1-%d)", alias, maxVLANAlias)

			continue
		}

		if prev, ok := seenAliases[alias]; ok {
			v.addf(CodeNetworkAliasDuplicate, path+".alias", "conflicting VLAN alias %d (also networks[%d])", alias, prev)
		} else {
			seenAliases[alias] = i
		}
	}
}

func (v *validator) validateNodes() {
	seenIDs := map[string]int{}
	seenHostnames := map[string]int{}

	for i := range v.doc.Nodes {
		node := &v.doc.Nodes[i]
		path := fmt.Sprintf("nodes[%d]", i)

		if v.validateID(path+".id", "node", node.ID, CodeNodeIDRequired, CodeNodeIDInvalid) {
			if prev, ok := seenIDs[foldKey(node.ID)]; ok {
				v.addf(CodeNodeIDDuplicate, path+".id", "duplicate node ID %q (also nodes[%d])", node.ID, prev)
			} else {
				seenIDs[foldKey(node.ID)] = i
				v.nodesByID[node.ID] = node
			}
		}

		if !finite(node.Position.X) || !finite(node.Position.Y) {
			v.addf(CodeNodePositionNotFinite, path+".position", "position values must be finite numbers")
		}

		if node.Size != nil {
			if !finite(node.Size.Width) || !finite(node.Size.Height) ||
				node.Size.Width <= 0 || node.Size.Height <= 0 {
				v.addf(CodeNodeSizeInvalid, path+".size", "size values must be positive finite numbers")
			}
		}

		v.validateNodePayload(node, path)
		v.validateKind(node, path, i, seenHostnames)
	}
}

// validateKind checks what the payload of the node at index i says, by its
// kind. seenHostnames holds the index of each device hostname seen so far,
// folded, to find a duplicate.
func (v *validator) validateKind(node *Node, path string, i int, seenHostnames map[string]int) {
	switch node.Kind {
	case NodeKindDevice:
		if node.Device != nil {
			v.validateDevice(node, path, i, seenHostnames)
		}
	case NodeKindSwitch:
		if node.Switch != nil {
			v.validateSwitch(node.Switch, path+".switch")
		}
	case NodeKindGroup:
		if node.Group != nil {
			v.validateBorderStyle(node.Group.BorderStyle, path+".group.borderStyle", CodeGroupBorderStyleUnknown)
			v.validateIconKey(node.Group.IconKey, path+".group.iconKey", CodeGroupIconKeyUnknown)
			v.validateIconRef(node.Group.Icon, path+".group.icon", CodeGroupIconInvalid)
			v.validateIconSize(node.Group.IconSize, path+".group.iconSize", CodeGroupIconSizeUnknown)
		}
	case NodeKindShape:
		if node.Shape != nil {
			v.validateShape(node.Shape, path+".shape")
		}
	case NodeKindIcon:
		if node.Icon != nil {
			v.validateIconNode(node.Icon, path+".icon")
		}
	case NodeKindLine:
		if node.Line != nil {
			v.validateLine(node.Line, path+".line")
		}
	case NodeKindNote:
		// Notes carry no phenix semantics and nothing to check.
	}
}

// validateDevice checks the payload of the device node at index i: its
// hostname, unique among the devices ignoring case, its interface handles,
// its icon and colors, and the topology it is included from.
func (v *validator) validateDevice(node *Node, path string, i int, seenHostnames map[string]int) {
	hostname := node.Device.Hostname

	switch {
	case strings.TrimSpace(hostname) == "":
		v.addf(CodeNodeHostnameRequired, path+".device.hostname", "hostname is required")
	case strings.ContainsAny(hostname, " \t\n"):
		v.addf(CodeNodeHostnameWhitespace, path+".device.hostname", "hostname %q must not contain whitespace", hostname)
	default:
		if prev, ok := seenHostnames[foldKey(hostname)]; ok {
			v.addf(
				CodeNodeHostnameDuplicate,
				path+".device.hostname",
				"duplicate hostname %q (also nodes[%d])",
				hostname, prev,
			)
		} else {
			seenHostnames[foldKey(hostname)] = i
		}
	}

	v.validateDeviceHandles(node, path)
	v.validateIconKey(node.Device.IconKey, path+".device.iconKey", CodeDeviceIconKeyUnknown)
	v.validateIconRef(node.Device.Icon, path+".device.icon", CodeDeviceIconInvalid)
	v.validateIconSize(node.Device.IconSize, path+".device.iconSize", CodeDeviceIconSizeUnknown)
	v.validateColor(node.Device.OutlineColor, path+".device.outlineColor", CodeDeviceColorInvalid)
	v.validateColor(node.Device.FillColor, path+".device.fillColor", CodeDeviceColorInvalid)
	v.validatePurdueLevel(node.Device.PurdueLevel, path+".device."+keyPurdueLevel, CodeDevicePurdueLevelUnknown)
	v.validateIncludedFrom(node.Device.IncludedFrom, path+".device.includedFrom")
}

// validateSwitch checks the payload of a switch node at path: the network
// it names, which the document has, its colors, its icon size, its Purdue
// level and its notes.
func (v *validator) validateSwitch(hub *Switch, path string) {
	if hub.NetworkID == "" {
		v.addf(CodeSwitchNetworkRequired, path+".networkId", "switch must reference a network")
	} else if _, ok := v.networksByID[hub.NetworkID]; !ok {
		v.addf(CodeSwitchNetworkUnknown, path+".networkId", "unknown network %q", hub.NetworkID)
	}

	v.validateColor(hub.OutlineColor, path+".outlineColor", CodeSwitchColorInvalid)
	v.validateColor(hub.FillColor, path+".fillColor", CodeSwitchColorInvalid)
	v.validateIconSize(hub.IconSize, path+"."+keyIconSize, CodeSwitchIconSizeUnknown)
	v.validatePurdueLevel(hub.PurdueLevel, path+"."+keyPurdueLevel, CodeSwitchPurdueLevelUnknown)
	v.validateNotes(path+"."+keyNotes, hub.Notes, noteCodes{
		tooMany: CodeSwitchNotesTooMany, blank: CodeSwitchNoteBlank,
		tooLong: CodeSwitchNoteTooLong, control: CodeSwitchNoteControl,
	})
}

func (v *validator) validateNodePayload(node *Node, path string) {
	switch node.Kind {
	case NodeKindDevice, NodeKindSwitch, NodeKindNote, NodeKindGroup,
		NodeKindShape, NodeKindIcon, NodeKindLine:
	default:
		v.addf(CodeNodeKindUnknown, path+".kind", "unknown node kind %q", node.Kind)

		return
	}

	payloads := map[NodeKind]bool{
		NodeKindDevice: node.Device != nil,
		NodeKindSwitch: node.Switch != nil,
		NodeKindNote:   node.Note != nil,
		NodeKindGroup:  node.Group != nil,
		NodeKindShape:  node.Shape != nil,
		NodeKindIcon:   node.Icon != nil,
		NodeKindLine:   node.Line != nil,
	}

	if !payloads[node.Kind] {
		v.addf(CodeNodePayloadMissing, path, "node of kind %q is missing its %q payload", node.Kind, node.Kind)
	}

	for kind, present := range payloads {
		if present && kind != node.Kind {
			v.addf(CodeNodePayloadExtra, path, "node of kind %q must not carry a %q payload", node.Kind, kind)
		}
	}

	if node.Kind == NodeKindDevice && node.Device != nil && node.Device.Spec == nil {
		v.addf(CodeDeviceSpecRequired, path+".device.spec", "device spec is required")
	}
}

// validateIconKey enforces the bounded icon key registry shared with the
// generated JSON Schema. An empty key means "use the default icon". code is
// the code of the key's owner.
func (v *validator) validateIconKey(key, path string, code Code) {
	if problem := iconKeyProblem(key); problem != "" {
		v.addf(code, path, "%s", problem)
	}
}

// iconKeyProblem says why key is no icon key a node may have, or returns
// "".
func iconKeyProblem(key string) string {
	switch {
	case key == "" || IsIconKey(key):
		return ""
	case iconKeyLooksExternal(key):
		return fmt.Sprintf("icon key %q must be one of the built-in keys, not a URL or path", key)
	default:
		return fmt.Sprintf("unknown icon key %q (expected one of %s)", key, strings.Join(iconKeys, ", "))
	}
}

// validateIconRef checks the custom icon a device, a group or an icon node
// names: none, or an icon name (see [IconNameProblem]). A name that the
// document does not carry is allowed. On a phenix server, it names an icon of
// the icon library of the server. Where nothing resolves it, the node shows
// its built-in icon.
func (v *validator) validateIconRef(name, path string, code Code) {
	if name == "" {
		return
	}

	if problem := IconNameProblem(name); problem != "" {
		v.addf(code, path, "%s", problem)
	}
}

// validateColor checks an outline or fill color: none, or "#rrggbb".
func (v *validator) validateColor(color, path string, code Code) {
	if problem := colorProblem(color); problem != "" {
		v.addf(code, path, "%s", problem)
	}
}

// colorProblem says why color is no outline or fill color, or returns "".
func colorProblem(color string) string {
	if color == "" || hexColor.MatchString(color) {
		return ""
	}

	return fmt.Sprintf("color %q must be a hex color such as #2f6fbf", truncate(color))
}

// validateLineStyle checks the line style of a network or an edge: none,
// or one of [LineStyles].
func (v *validator) validateLineStyle(style, path string, code Code) {
	if style == "" || slices.Contains(lineStyles, style) {
		return
	}

	v.addf(code, path, "unknown line style %q (expected one of %s)", truncate(style), strings.Join(lineStyles, ", "))
}

// validateIconSize checks the icon size of the document, a device, a switch
// or a group: none, or one of [IconSizes].
func (v *validator) validateIconSize(size, path string, code Code) {
	if problem := iconSizeProblem(size); problem != "" {
		v.addf(code, path, "%s", problem)
	}
}

// iconSizeProblem says why size is no icon size, or returns "".
func iconSizeProblem(size string) string {
	if size == "" || slices.Contains(iconSizes, size) {
		return ""
	}

	return fmt.Sprintf("unknown icon size %q (expected one of %s)", truncate(size), strings.Join(iconSizes, ", "))
}

// validatePurdueLevel checks the Purdue level of a device or a switch:
// none, or one of [PurdueLevels].
func (v *validator) validatePurdueLevel(level, path string, code Code) {
	if problem := purdueLevelProblem(level); problem != "" {
		v.addf(code, path, "%s", problem)
	}
}

// purdueLevelProblem says why level is no Purdue level, or returns "".
func purdueLevelProblem(level string) string {
	if level == "" || slices.Contains(purdueLevels, level) {
		return ""
	}

	return fmt.Sprintf(
		"unknown Purdue layer %q (expected one of %s)", truncate(level), strings.Join(purdueLevels, ", "),
	)
}

// validateBorderStyle checks the border style of a group or a shape: none,
// or one of [BorderStyles].
func (v *validator) validateBorderStyle(style, path string, code Code) {
	if style == "" || slices.Contains(borderStyles, style) {
		return
	}

	v.addf(code, path, "unknown border style %q (expected one of %s)", truncate(style), strings.Join(borderStyles, ", "))
}

// validateShape checks the payload of a shape node at path: its figure, its
// colors and its border style.
func (v *validator) validateShape(shape *Shape, path string) {
	if !slices.Contains(shapeFigures, shape.Shape) {
		v.addf(
			CodeDrawingShapeUnknown,
			path+".shape", "unknown shape %q (expected one of %s)",
			truncate(shape.Shape), strings.Join(shapeFigures, ", "),
		)
	}

	v.validateColor(shape.FillColor, path+".fillColor", CodeDrawingColorInvalid)
	v.validateColor(shape.OutlineColor, path+".outlineColor", CodeDrawingColorInvalid)
	v.validateBorderStyle(shape.BorderStyle, path+".borderStyle", CodeDrawingBorderStyleUnknown)
}

// validateIconNode checks the payload of an icon node at path: exactly one
// of a built-in icon key and a custom icon name.
func (v *validator) validateIconNode(icon *IconNode, path string) {
	if (icon.IconKey == "") == (icon.Icon == "") {
		v.addf(CodeDrawingIconAmbiguous, path, "an icon node must name exactly one of a built-in icon and a custom icon")
	}

	v.validateIconKey(icon.IconKey, path+".iconKey", CodeDrawingIconKeyUnknown)
	v.validateIconRef(icon.Icon, path+".icon", CodeDrawingIconInvalid)
}

// validateLine checks the payload of a line node at path: from
// [MinLinePoints] to [MaxLinePoints] points, each a finite coordinate, and
// its color and line style.
func (v *validator) validateLine(line *Line, path string) {
	switch {
	case len(line.Points) < MinLinePoints:
		v.addf(CodeDrawingPointsTooFew, path+".points", "a line must have at least %d points", MinLinePoints)
	case len(line.Points) > MaxLinePoints:
		v.addf(CodeDrawingPointsTooMany, path+".points", "a line must have at most %d points", MaxLinePoints)
	default:
		for _, point := range line.Points {
			if !finite(point.X) || !finite(point.Y) {
				v.addf(CodeDrawingPointsNotFinite, path+".points", "line points must be finite numbers")

				break
			}
		}
	}

	v.validateColor(line.Color, path+".color", CodeDrawingColorInvalid)
	v.validateLineStyle(line.LineStyle, path+".lineStyle", CodeDrawingLineStyleUnknown)
}

// validateTemplates checks the document's templates: how many, the id of
// each, which is unique among them, and what [Template.Issues] checks,
// which includes the custom icon each names.
func (v *validator) validateTemplates() {
	if len(v.doc.Templates) > MaxTemplates {
		v.addf(
			CodeTemplateListTooMany, keyTemplates,
			"at most %d templates are allowed, not %d", MaxTemplates, len(v.doc.Templates),
		)
	}

	seenIDs := map[string]int{}

	for i := range v.doc.Templates {
		template := &v.doc.Templates[i]
		path := fmt.Sprintf("%s[%d]", keyTemplates, i)

		if v.validateID(path+".id", "template", template.ID, CodeTemplateIDRequired, CodeTemplateIDInvalid) {
			if prev, ok := seenIDs[foldKey(template.ID)]; ok {
				v.addf(
					CodeTemplateIDDuplicate, path+".id",
					"duplicate template ID %q (also %s[%d])", template.ID, keyTemplates, prev,
				)
			} else {
				seenIDs[foldKey(template.ID)] = i
			}
		}

		v.issues = append(v.issues, template.Issues(path)...)
	}
}

// validateIncludedFrom checks the topology an included device names. A
// publish leaves included devices out of the topology because the
// includeTopologies of the document bring them back. Thus a document that
// includes nothing must not carry any: a publish would silently drop them.
func (v *validator) validateIncludedFrom(name, path string) {
	switch {
	case name == "":
	case strings.TrimSpace(name) == "" || strings.ContainsAny(name, " \t\n"):
		v.addf(
			CodeDeviceIncludedFromInvalid, path,
			"included topology name %q must not be blank or contain whitespace", name,
		)
	case v.doc.Source == nil || len(v.doc.Source.IncludeTopologies) == 0:
		v.addf(
			CodeDeviceIncludedFromNoSource,
			path,
			"device is included from topology %q, but the document includes no topologies",
			name,
		)
	}
}

func (v *validator) validateDeviceHandles(node *Node, path string) {
	seenNames := map[string]int{}

	for j := range node.Device.Interfaces {
		handle := &node.Device.Interfaces[j]
		handlePath := fmt.Sprintf("%s.device.interfaces[%d]", path, j)

		if v.validateID(
			handlePath+".id", "interface handle", handle.ID, CodeInterfaceIDRequired, CodeInterfaceIDInvalid,
		) {
			if owner, ok := v.handleOwner[foldKey(handle.ID)]; ok {
				v.addf(
					CodeInterfaceIDDuplicate,
					handlePath+".id",
					"duplicate interface handle ID %q (also used by node %q)",
					handle.ID, owner.ID,
				)
			} else {
				v.handleOwner[foldKey(handle.ID)] = node
			}
		}

		if strings.TrimSpace(handle.Name) == "" {
			v.addf(CodeInterfaceNameRequired, handlePath+".name", "interface name is required")

			continue
		}

		if prev, ok := seenNames[foldKey(handle.Name)]; ok {
			v.addf(
				CodeInterfaceNameDuplicate,
				handlePath+".name",
				"duplicate interface name %q (also interfaces[%d])",
				handle.Name, prev,
			)
		} else {
			seenNames[foldKey(handle.Name)] = j
		}
	}
}

func (v *validator) validateParents() {
	for i := range v.doc.Nodes {
		node := &v.doc.Nodes[i]
		path := fmt.Sprintf("nodes[%d].parentId", i)

		if node.ParentID == "" {
			continue
		}

		if node.ParentID == node.ID {
			v.addf(CodeNodeParentSelf, path, "node cannot be its own parent")

			continue
		}

		parent, ok := v.nodesByID[node.ParentID]
		if !ok {
			v.addf(CodeNodeParentUnknown, path, "unknown parent node %q", node.ParentID)

			continue
		}

		if parent.Kind != NodeKindGroup {
			v.addf(CodeNodeParentNotGroup, path, "parent node %q is not a group", node.ParentID)

			continue
		}

		if v.parentCycle(node) {
			v.addf(CodeNodeParentCycle, path, "group membership cycle detected at node %q", node.ID)
		}
	}
}

func (v *validator) parentCycle(start *Node) bool {
	seen := map[string]bool{start.ID: true}
	current := start

	for current.ParentID != "" {
		next, ok := v.nodesByID[current.ParentID]
		if !ok {
			return false
		}

		if seen[next.ID] {
			return true
		}

		seen[next.ID] = true
		current = next
	}

	return false
}

// handleIndex holds the interface handles of device nodes by ID. A device is
// indexed the first time a handle of it is looked for, so a device with many
// interfaces is not searched once for every edge that names it.
type handleIndex map[*Node]map[string]*InterfaceHandle

// find returns the handle [Device.InterfaceHandle] returns for the device of
// the node: the first with that ID, or nil.
func (h handleIndex) find(device *Node, id string) *InterfaceHandle {
	if device.Device == nil {
		return nil
	}

	byID, indexed := h[device]
	if !indexed {
		byID = make(map[string]*InterfaceHandle, len(device.Device.Interfaces))

		for i := range device.Device.Interfaces {
			handle := &device.Device.Interfaces[i]

			if _, seen := byID[handle.ID]; !seen {
				byID[handle.ID] = handle
			}
		}

		h[device] = byID
	}

	return byID[id]
}

func (v *validator) validateEdges() {
	seenIDs := map[string]int{}
	connected := map[string]int{}
	handles := handleIndex{}

	for i := range v.doc.Edges {
		edge := &v.doc.Edges[i]
		path := fmt.Sprintf("edges[%d]", i)

		if v.validateID(path+".id", "edge", edge.ID, CodeEdgeIDRequired, CodeEdgeIDInvalid) {
			if prev, ok := seenIDs[foldKey(edge.ID)]; ok {
				v.addf(CodeEdgeIDDuplicate, path+".id", "duplicate edge ID %q (also edges[%d])", edge.ID, prev)
			} else {
				seenIDs[foldKey(edge.ID)] = i
			}
		}

		v.validateRoute(path+".route", edge.Route)
		v.validateLineStyle(edge.LineStyle, path+".lineStyle", CodeEdgeLineStyleUnknown)

		source, sourceOK := v.nodesByID[edge.SourceNodeID]
		if !sourceOK {
			v.addf(CodeEdgeSourceUnknown, path+".sourceNodeId", "unknown node %q", edge.SourceNodeID)
		}

		target, targetOK := v.nodesByID[edge.TargetNodeID]
		if !targetOK {
			v.addf(CodeEdgeTargetUnknown, path+".targetNodeId", "unknown node %q", edge.TargetNodeID)
		}

		if !sourceOK || !targetOK {
			continue
		}

		if source.ID == target.ID {
			v.addf(CodeEdgeEndpointsSame, path, "edge endpoints must differ")

			continue
		}

		device, deviceHandle, switchNode, ok := edgeEndpoints(source, edge.SourceHandleID, target, edge.TargetHandleID)
		if !ok {
			v.addf(CodeEdgeEndpointsInvalid, path, "an edge must connect one device interface to one switch")

			continue
		}

		handle := handles.find(device, deviceHandle)
		if handle == nil {
			v.addf(
				CodeEdgeHandleUnknown,
				path,
				"unknown interface handle %q on device node %q",
				deviceHandle, device.ID,
			)

			continue
		}

		if prev, ok := connected[deviceHandle]; ok {
			v.addf(
				CodeEdgeInterfaceTaken,
				path,
				"interface %q of device %q is already connected by edges[%d]",
				handle.Name, device.Device.Hostname, prev,
			)
		} else {
			connected[deviceHandle] = i
		}

		if switchNode.Switch == nil {
			continue
		}

		v.validateEdgeNetwork(edge, path, switchNode)
	}
}

// validateEdgeNetwork checks the network an edge at path names: one of the
// document, and the network of the switch it connects to.
func (v *validator) validateEdgeNetwork(edge *Edge, path string, switchNode *Node) {
	if edge.NetworkID == "" {
		v.addf(CodeEdgeNetworkRequired, path+".networkId", "edge must reference a network")

		return
	}

	if _, ok := v.networksByID[edge.NetworkID]; !ok {
		v.addf(CodeEdgeNetworkUnknown, path+".networkId", "unknown network %q", edge.NetworkID)

		return
	}

	if edge.NetworkID != switchNode.Switch.NetworkID {
		v.addf(
			CodeEdgeNetworkMismatch,
			path+".networkId",
			"network %q does not match network %q of switch %q",
			edge.NetworkID, switchNode.Switch.NetworkID, switchNode.ID,
		)
	}
}

// validateRoute checks an edge route: absent, or at least its two ends, every
// point a finite coordinate.
func (v *validator) validateRoute(path string, route []Position) {
	if route == nil {
		return
	}

	if len(route) < minRoutePoints {
		v.addf(CodeEdgeRouteTooFew, path, "a route must have at least %d points", minRoutePoints)

		return
	}

	for _, point := range route {
		if !finite(point.X) || !finite(point.Y) {
			v.addf(CodeEdgeRouteNotFinite, path, "route points must be finite numbers")

			return
		}
	}
}

// IsConfigName reports whether name is a name phenix gives a config (see
// phenix/api/config.NameRegex), and one a document may name a Scenario config
// by: not empty, and at most [MaxScenarioNameBytes].
func IsConfigName(name string) bool {
	return name != "" && len(name) <= MaxScenarioNameBytes && configNameRegexp.MatchString(name)
}

// validateScenarios checks the Scenario configs the document names: at most
// [MaxScenarios], each by a config name of at most [MaxScenarioNameBytes],
// and none twice, ignoring case as the editor compares them.
func (v *validator) validateScenarios() {
	if len(v.doc.Scenarios) > MaxScenarios {
		v.addf(
			CodeScenarioListTooMany, keyScenarios,
			"at most %d scenarios are allowed, not %d", MaxScenarios, len(v.doc.Scenarios),
		)
	}

	seen := map[string]int{}

	for i, name := range v.doc.Scenarios {
		path := fmt.Sprintf("%s[%d]", keyScenarios, i)

		switch {
		case name == "":
			v.addf(CodeScenarioNameRequired, path, "scenario name is required")

			continue
		case len(name) > MaxScenarioNameBytes:
			v.addf(CodeScenarioNameTooLong, path, "scenario name must be at most %d bytes", MaxScenarioNameBytes)

			continue
		case !configNameRegexp.MatchString(name):
			v.addf(
				CodeScenarioNameInvalid,
				path,
				"scenario name %q may use only letters, numbers, underscores, at signs, periods and hyphens",
				truncate(name),
			)

			continue
		}

		if prev, ok := seen[foldKey(name)]; ok {
			v.addf(CodeScenarioNameDuplicate, path, "duplicate scenario %q (also %s[%d])", name, keyScenarios, prev)
		} else {
			seen[foldKey(name)] = i
		}
	}
}

func (v *validator) validateSource() {
	if v.doc.Source == nil {
		return
	}

	switch v.doc.Source.Kind {
	case SourceKindManual, SourceKindTopology, SourceKindExperiment:
	default:
		v.addf(CodeSourceKindUnknown, "source.kind", "unknown source kind %q", v.doc.Source.Kind)
	}

	v.validateIncludes("source.includeTopologies", v.doc.Source.IncludeTopologies)
	v.validateIncludes("source.unresolvedIncludes", v.doc.Source.UnresolvedIncludes)

	if digest := v.doc.Source.Digest; digest != "" && !IsDigest(digest) {
		v.addf(CodeSourceDigestMalformed, "source.digest", "malformed source digest %q (expected sha256:<64 hex>)", digest)
	}

	v.validateAnnotations(v.doc.Source.Annotations)
}

// validateIncludes checks the names of included topologies the source
// lists at path: none blank, none with whitespace.
func (v *validator) validateIncludes(path string, names []string) {
	for i, name := range names {
		at := fmt.Sprintf("%s[%d]", path, i)

		switch {
		case strings.TrimSpace(name) == "":
			v.addf(CodeIncludeNameRequired, at, "included topology name is required")
		case strings.ContainsAny(name, " \t\n"):
			v.addf(
				CodeIncludeNameWhitespace,
				at,
				"included topology name %q must not contain whitespace",
				name,
			)
		}
	}
}

// validateAnnotations bounds the source annotations, the way generation
// keeps them (see [generator.importAnnotations]).
func (v *validator) validateAnnotations(annotations map[string]string) {
	const path = "source.annotations"

	if len(annotations) > MaxAnnotations {
		v.addf(
			CodeSourceAnnotationsTooMany, path,
			"at most %d annotations are allowed, not %d", MaxAnnotations, len(annotations),
		)
	}

	if size := annotationBytes(annotations); size > MaxAnnotationBytes {
		v.addf(
			CodeSourceAnnotationsTooLarge, path,
			"annotations must take at most %d bytes in all, not %d", MaxAnnotationBytes, size,
		)
	}

	for _, key := range slices.Sorted(maps.Keys(annotations)) {
		if code, problem := annotationKeyProblem(key); problem != "" {
			v.addf(code, path, "annotation key %q %s", truncate(key), problem)
		}
	}
}

// annotationBytes is the size of annotations' keys and values together.
func annotationBytes(annotations map[string]string) int {
	size := 0

	for key, value := range annotations {
		size += len(key) + len(value)
	}

	return size
}

// annotationKeyProblem says what makes an annotation key unusable, with the
// code of that rule, or returns "".
func annotationKeyProblem(key string) (Code, string) {
	switch {
	case strings.TrimSpace(key) == "":
		return CodeSourceAnnotationKeyBlank, "must not be blank"
	case len(key) > MaxNameBytes:
		return CodeSourceAnnotationKeyLong, fmt.Sprintf("must be at most %d bytes", MaxNameBytes)
	case strings.ContainsFunc(key, isControl):
		return CodeSourceAnnotationKeyCtrl, "must not contain control characters"
	default:
		return "", ""
	}
}

// validateID enforces the identifier contract: every entity identifier is an
// RFC 4122 UUID. Generated identifiers are name-based UUIDs. Identifiers that
// the front end makes are random (version 4) UUIDs. It reports whether there
// is an identifier at all. It reports a blank identifier as missing, with the
// code required. A blank identifier is then neither a duplicate of another
// blank one nor a name to find an entity by. It reports an identifier that is
// not a UUID with the code invalid.
func (v *validator) validateID(path, kindName, id string, required, invalid Code) bool {
	if strings.TrimSpace(id) == "" {
		v.addf(required, path, "%s ID is required", kindName)

		return false
	}

	if !IsUUID(id) {
		v.addf(invalid, path, "%s ID %q is not a valid UUID", kindName, id)
	}

	return true
}

// edgeEndpoints normalizes an edge's endpoints into (device, device handle,
// switch). It reports false when the edge does not join exactly one device to
// exactly one switch, or when the device endpoint carries no handle.
func edgeEndpoints(
	source *Node, sourceHandle string, target *Node, targetHandle string,
) (*Node, string, *Node, bool) {
	switch {
	case source.Kind == NodeKindDevice && target.Kind == NodeKindSwitch:
		if source.Device == nil || sourceHandle == "" {
			return nil, "", nil, false
		}

		return source, sourceHandle, target, true
	case source.Kind == NodeKindSwitch && target.Kind == NodeKindDevice:
		if target.Device == nil || targetHandle == "" {
			return nil, "", nil, false
		}

		return target, targetHandle, source, true
	default:
		return nil, "", nil, false
	}
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
