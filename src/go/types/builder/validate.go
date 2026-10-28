package builder

import (
	"encoding/binary"
	"fmt"
	"maps"
	"math"
	"net/netip"
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

// The messages of the issues for a layer ID out of range, of a device, a
// switch or a template, and of a layer the document defines. The decoder
// refuses a layer that is not an integer with the same messages (see
// [decodeRule]). The range is 0 to [MaxLayerID].
const (
	layerRangeMessage   = "layer must be an integer from 0 to 99"
	layerIDRangeMessage = "layer ID must be an integer from 0 to 99"
)

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
)

// The sizes the editor draws the icon of a device, a switch or a group at:
// 16, 24 and 32 pixels. Small is the size of a document that names none.
const (
	IconSizeSmall  = "small"
	IconSizeMedium = "medium"
	IconSizeLarge  = "large"
)

// Issue is a single problem in a document, a template file or a publication, with its [Code], [Severity] and location.
// NodeID, EdgeID and NetworkID still find the element after an edit moves the indexes in Path.
// The web UI validator reports its issues in the same shape, with the same codes.
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

	return fmt.Sprintf("%s: %s", ErrInvalidDocument.Error(), strings.Join(messages, ". "))
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

// Validate performs structural and semantic validation of the document, and returns nil or a *[ValidationError].
// It does not check size limits on purpose, because the API layer checks them.
// The exceptions are the bounds that the editor also checks before it saves, such as names, notes, templates and icons.
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
	val.validateLayers()
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

	if size := v.doc.Grid.Size; size != math.Trunc(size) || size < MinGridSize || size > MaxGridSize {
		v.addf(
			CodeDocumentGridInvalid, "grid.size",
			"grid size must be a whole number from %d to %d", MinGridSize, MaxGridSize,
		)
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
	v.validateNotes(meta.Notes)
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

// validateNotes checks the diagram notes: at most [MaxDiagramNotes], each not blank, at most
// [MaxDiagramNoteBytes], and with no control characters other than newlines and tabs.
func (v *validator) validateNotes(notes []string) {
	at := metadataPath(keyNotes)

	if len(notes) > MaxDiagramNotes {
		v.addf(CodeMetadataNotesTooMany, at, "a document can have at most %d notes, not %d", MaxDiagramNotes, len(notes))
	}

	for i, note := range notes {
		path := fmt.Sprintf("%s[%d]", at, i)

		switch {
		case strings.TrimSpace(note) == "":
			v.addf(CodeMetadataNoteBlank, path, "note must not be blank")
		case len(note) > MaxDiagramNoteBytes:
			v.addf(CodeMetadataNoteTooLong, path, "note must be at most %d bytes", MaxDiagramNoteBytes)
		case strings.ContainsFunc(note, isNoteControl):
			v.addf(CodeMetadataNoteControl, path, "note must not contain control characters other than newline and tab")
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
			v.addf(CodeNetworkNameRequired, path+".name", "a network needs a name")
		case hasNameSpace(network.Name):
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
		v.validateNetworkAddressing(network, path)

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
		v.addf(CodeNodeHostnameRequired, path+".device.hostname", "a device needs a hostname")
	case hasNameSpace(hostname):
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
	v.validateLayer(node.Device.Layer, path+".device."+keyLayer, CodeDeviceLayerRange)
	v.validateIncludedFrom(node.Device.IncludedFrom, path+".device.includedFrom")
}

// validateSwitch checks the payload of a switch node at path: the network
// it names, which the document has, its colors, its icon size and its layer.
func (v *validator) validateSwitch(hub *Switch, path string) {
	if hub.NetworkID == "" {
		v.addf(CodeSwitchNetworkRequired, path+".networkId", "switch must reference a network")
	} else if _, ok := v.networksByID[hub.NetworkID]; !ok {
		v.addf(CodeSwitchNetworkUnknown, path+".networkId", "unknown network %q", hub.NetworkID)
	}

	v.validateColor(hub.OutlineColor, path+".outlineColor", CodeSwitchColorInvalid)
	v.validateColor(hub.FillColor, path+".fillColor", CodeSwitchColorInvalid)
	v.validateIconSize(hub.IconSize, path+"."+keyIconSize, CodeSwitchIconSizeUnknown)
	v.validateLayer(hub.Layer, path+"."+keyLayer, CodeSwitchLayerRange)
}

func (v *validator) validateNodePayload(node *Node, path string) {
	switch node.Kind {
	case NodeKindDevice, NodeKindSwitch, NodeKindNote, NodeKindGroup,
		NodeKindShape, NodeKindIcon, NodeKindLine:
	default:
		v.addf(CodeNodeKindUnknown, path+".kind", "unknown node kind %q", node.Kind)

		return
	}

	// The payloads in the order of NODE_KINDS in model.js, so the issues
	// come out in one order.
	payloads := []struct {
		kind    NodeKind
		present bool
	}{
		{NodeKindDevice, node.Device != nil},
		{NodeKindSwitch, node.Switch != nil},
		{NodeKindNote, node.Note != nil},
		{NodeKindGroup, node.Group != nil},
		{NodeKindShape, node.Shape != nil},
		{NodeKindIcon, node.Icon != nil},
		{NodeKindLine, node.Line != nil},
	}

	for _, payload := range payloads {
		if payload.kind == node.Kind && !payload.present {
			v.addf(CodeNodePayloadMissing, path, "node of kind %q has no %q payload", node.Kind, node.Kind)
		}
	}

	for _, payload := range payloads {
		if payload.present && payload.kind != node.Kind {
			v.addf(CodeNodePayloadExtra, path, "node of kind %q must not carry a %q payload", node.Kind, payload.kind)
		}
	}

	if node.Kind == NodeKindDevice && node.Device != nil && node.Device.Spec == nil {
		v.addf(CodeDeviceSpecRequired, path+".device.spec", "a device needs a spec")
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

// validateNetworkAddressing checks the subnet and the gateway of a network:
// each none, or the subnet an IPv4 network in CIDR form without host bits
// (see [ParseSubnet]) and the gateway an IPv4 address, a host address of the
// subnet when the network has a valid one (see [IsSubnetHost]).
func (v *validator) validateNetworkAddressing(network *Network, path string) {
	subnet, subnetOK := netip.Prefix{}, false

	if network.Subnet != "" {
		subnet, subnetOK = ParseSubnet(network.Subnet)
		if !subnetOK {
			v.addf(
				CodeNetworkSubnetInvalid, path+".subnet",
				"subnet %q is not an IPv4 network in CIDR form, such as 10.1.0.0/24", truncate(network.Subnet),
			)
		}
	}

	if network.Gateway == "" {
		return
	}

	gateway, err := netip.ParseAddr(network.Gateway)

	switch {
	case err != nil || !gateway.Is4():
		v.addf(CodeNetworkGatewayInvalid, path+".gateway", "gateway %q is not an IPv4 address", truncate(network.Gateway))
	case subnetOK && !IsSubnetHost(subnet, gateway):
		v.addf(
			CodeNetworkGatewayOutside, path+".gateway",
			"gateway %s is not a host address of subnet %s", network.Gateway, network.Subnet,
		)
	}
}

// ParseSubnet reads the subnet of a network (see [Network.Subnet]): an IPv4
// address without leading zeros, a slash and a prefix length from 0 to 32
// without a sign or leading zeros, and no host bits set. The editor reads it
// the same way (parseSubnet in addressing.js).
func ParseSubnet(text string) (netip.Prefix, bool) {
	address, bits, found := strings.Cut(text, "/")
	if !found {
		return netip.Prefix{}, false
	}

	addr, err := netip.ParseAddr(address)
	if err != nil || !addr.Is4() {
		return netip.Prefix{}, false
	}

	length, err := strconv.Atoi(bits)
	if err != nil || strconv.Itoa(length) != bits || length < 0 || length > addr.BitLen() {
		return netip.Prefix{}, false
	}

	prefix := netip.PrefixFrom(addr, length)

	return prefix, prefix.Masked() == prefix
}

// IsSubnetHost reports whether addr is a host address of subnet: an address
// in it other than its network and broadcast addresses. A /31 has no such
// addresses to leave out (RFC 3021), and a /32 is its one host.
func IsSubnetHost(subnet netip.Prefix, addr netip.Addr) bool {
	if !subnet.Contains(addr) {
		return false
	}

	hostBits := addr.BitLen() - subnet.Bits()
	if hostBits <= 1 {
		return true
	}

	first := ipv4Value(subnet.Addr())
	value := ipv4Value(addr)

	return value != first && value != first|(1<<hostBits-1)
}

// ipv4Value is an IPv4 address as a number.
func ipv4Value(addr netip.Addr) uint32 {
	octets := addr.As4()

	return binary.BigEndian.Uint32(octets[:])
}

// validateLineStyle checks the line style of a network or an edge: none,
// or one of solid, dashed, dotted and dash-dot.
func (v *validator) validateLineStyle(style, path string, code Code) {
	if style == "" || slices.Contains(lineStyles, style) {
		return
	}

	v.addf(code, path, "unknown line style %q (expected one of %s)", truncate(style), strings.Join(lineStyles, ", "))
}

// validateIconSize checks the icon size of the document, a device, a switch
// or a group: none, small, medium or large.
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

// validateLayer checks the layer of a device or a switch: none, or an ID
// from 0 to [MaxLayerID].
func (v *validator) validateLayer(layer *int, path string, code Code) {
	if problem := layerProblem(layer); problem != "" {
		v.addf(code, path, "%s", problem)
	}
}

// layerProblem says why layer is no layer a device, a switch or a template
// may name, or returns "".
func layerProblem(layer *int) string {
	if layer == nil || isLayerID(*layer) {
		return ""
	}

	return layerRangeMessage
}

// isLayerID reports whether id is from 0 to [MaxLayerID].
func isLayerID(id int) bool {
	return id >= 0 && id <= MaxLayerID
}

// validateLayers checks the layers the document defines (see
// [layerListIssues]) and the options of the layer set they came from (see
// [documentLayerSetIssues]).
func (v *validator) validateLayers() {
	v.issues = append(v.issues, layerListIssues(v.doc.Layers, keyLayers)...)

	if v.doc.LayerSet != nil {
		v.issues = append(v.issues, documentLayerSetIssues(v.doc.LayerSet)...)
	}
}

// validateBorderStyle checks the border style of a group or a shape: none,
// or one of solid, dashed, dotted and double.
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
			"a document can have at most %d templates, not %d", MaxTemplates, len(v.doc.Templates),
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
	case strings.TrimSpace(name) == "" || hasNameSpace(name):
		v.addf(
			CodeDeviceIncludedFromInvalid, path,
			"included topology name %q must not be blank or contain whitespace", name,
		)
	case v.doc.Source == nil || len(v.doc.Source.IncludeTopologies) == 0:
		v.addf(
			CodeDeviceIncludedFromNoSource,
			path,
			"device is from included topology %q, but the document includes no topologies",
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
			handlePath+".id", "connection point", handle.ID, CodeInterfaceIDRequired, CodeInterfaceIDInvalid,
		) {
			if owner, ok := v.handleOwner[foldKey(handle.ID)]; ok {
				v.addf(
					CodeInterfaceIDDuplicate,
					handlePath+".id",
					"duplicate connection point ID %q (node %q also has it)",
					handle.ID, owner.ID,
				)
			} else {
				v.handleOwner[foldKey(handle.ID)] = node
			}
		}

		if strings.TrimSpace(handle.Name) == "" {
			v.addf(CodeInterfaceNameRequired, handlePath+".name", "an interface needs a name")

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
	states := map[string]chainState{}

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

		if v.parentCycle(node.ParentID, states) {
			v.addf(CodeNodeParentCycle, path, "groups contain each other in a cycle at node %q", node.ID)
		}
	}
}

// chainState is what is known of the chain of parents from a node ID.
type chainState uint8

const (
	chainUnknown chainState = iota
	// chainWalking marks a node of the walk in progress.
	chainWalking
	// chainEnds marks a node whose parents end at a node without one.
	chainEnds
	// chainCycles marks a node whose parents lead into a cycle.
	chainCycles
)

// parentCycle reports whether the chain of parents from the node with the ID
// leads into a cycle. states keeps each result, so each node is walked once.
func (v *validator) parentCycle(id string, states map[string]chainState) bool {
	var walk []string

	state := chainEnds

	for {
		node, ok := v.nodesByID[id]
		if !ok {
			break
		}

		if known := states[id]; known != chainUnknown {
			if known == chainWalking {
				state = chainCycles
			} else {
				state = known
			}

			break
		}

		states[id] = chainWalking
		walk = append(walk, id)

		if node.ParentID == "" {
			break
		}

		id = node.ParentID
	}

	for _, walked := range walk {
		states[walked] = state
	}

	return state == chainCycles
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

		if v.validateID(path+".id", "connection", edge.ID, CodeEdgeIDRequired, CodeEdgeIDInvalid) {
			if prev, ok := seenIDs[foldKey(edge.ID)]; ok {
				v.addf(CodeEdgeIDDuplicate, path+".id", "duplicate connection ID %q (also edges[%d])", edge.ID, prev)
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
			v.addf(CodeEdgeEndpointsSame, path, "connection endpoints must differ")

			continue
		}

		device, deviceHandle, switchNode, ok := edgeEndpoints(source, edge.SourceHandleID, target, edge.TargetHandleID)
		if !ok {
			v.addf(CodeEdgeEndpointsInvalid, path, "a connection must join one device interface to one switch")

			continue
		}

		handle := handles.find(device, deviceHandle)
		if handle == nil {
			v.addf(
				CodeEdgeHandleUnknown,
				path,
				"unknown connection point %q on device node %q",
				deviceHandle, device.ID,
			)

			continue
		}

		if prev, ok := connected[deviceHandle]; ok {
			v.addf(
				CodeEdgeInterfaceTaken,
				path,
				"interface %q of device %q already has another connection, edges[%d]",
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
		v.addf(CodeEdgeNetworkRequired, path+".networkId", "connection must reference a network")

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
			"a document can have at most %d scenarios, not %d", MaxScenarios, len(v.doc.Scenarios),
		)
	}

	seen := map[string]int{}

	for i, name := range v.doc.Scenarios {
		path := fmt.Sprintf("%s[%d]", keyScenarios, i)

		switch {
		case name == "":
			v.addf(CodeScenarioNameRequired, path, "a scenario needs a name")

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
			v.addf(CodeIncludeNameRequired, at, "an included topology needs a name")
		case hasNameSpace(name):
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
			"the source can have at most %d annotations, not %d", MaxAnnotations, len(annotations),
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

// validateID checks that an entity identifier is an RFC 4122 UUID, and reports whether there is one at all.
// A blank identifier gets the code required, so it is never a duplicate or a lookup name. A non-UUID gets the code invalid.
func (v *validator) validateID(path, kindName, id string, required, invalid Code) bool {
	if strings.TrimSpace(id) == "" {
		v.addf(required, path, "a %s needs an ID", kindName)

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
