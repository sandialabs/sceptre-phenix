package builder

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"phenix/store"
	"phenix/types"
)

// maxVLANAlias is the largest 802.1Q VLAN ID an alias may take.
const maxVLANAlias = 4094

// Issue is a single validation failure, located by a JSON-ish path within the
// document.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
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

// Validate performs structural and semantic validation of the document.
//
// Size limits (counts, lengths, payload sizes) are intentionally not checked
// here; they belong to the API layer. Validate rejects:
//
//   - wrong schema URI or revision,
//   - identifiers that are not RFC 4122 UUIDs, and duplicate
//     node/network/edge/handle identifiers (case-insensitive),
//   - duplicate device hostnames (case-insensitive),
//   - nodes whose payload does not match their kind,
//   - dangling parent/network/node/handle references and group cycles,
//   - invalid edge topology (an edge must join one device handle to one switch),
//   - interfaces attached to more than one network,
//   - conflicting network names or VLAN aliases,
//   - inconsistent scenario references (missing name, content, apiVersion, or
//     a missing, malformed, or mismatched digest), and scenario content that
//     fails the existing phenix scenario schema,
//   - icon keys outside the bounded registry returned by [IconKeys],
//   - a malformed [Source.Digest],
//   - non-finite geometry, and sizes, zoom, or grid spacing that are not
//     strictly positive.
//
// It returns nil or a *[ValidationError].
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
	val.validateScenario()
	val.validateSource()

	if len(val.issues) == 0 {
		return nil
	}

	sort.SliceStable(val.issues, func(i, j int) bool {
		return val.issues[i].Path < val.issues[j].Path
	})

	return &ValidationError{Issues: val.issues}
}

func (v *validator) addf(path, format string, args ...any) {
	v.issues = append(v.issues, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) validateHeader() {
	if v.doc.Schema != SchemaURI {
		v.addf("schema", "expected %q, got %q", SchemaURI, v.doc.Schema)
	}

	if v.doc.Revision != SchemaRevision {
		v.addf("revision", "expected %d, got %d", SchemaRevision, v.doc.Revision)
	}

	v.validateID("id", "document", v.doc.ID)

	if !finite(v.doc.Viewport.X) || !finite(v.doc.Viewport.Y) || !finite(v.doc.Viewport.Zoom) {
		v.addf("viewport", "viewport values must be finite numbers")
	}

	if finite(v.doc.Viewport.Zoom) && v.doc.Viewport.Zoom <= 0 {
		v.addf("viewport.zoom", "zoom must be a positive number")
	}

	if !finite(v.doc.Grid.Size) || v.doc.Grid.Size <= 0 {
		v.addf("grid.size", "grid size must be a positive finite number")
	}
}

func (v *validator) validateNetworks() {
	seenIDs := map[string]int{}
	seenNames := map[string]int{}
	seenAliases := map[int]int{}

	for i := range v.doc.Networks {
		network := &v.doc.Networks[i]
		path := fmt.Sprintf("networks[%d]", i)

		v.validateID(path+".id", "network", network.ID)

		if prev, ok := seenIDs[foldKey(network.ID)]; ok {
			v.addf(path+".id", "duplicate network ID %q (also networks[%d])", network.ID, prev)
		} else {
			seenIDs[foldKey(network.ID)] = i
			v.networksByID[network.ID] = network
		}

		switch {
		case strings.TrimSpace(network.Name) == "":
			v.addf(path+".name", "network name is required")
		case strings.ContainsAny(network.Name, " \t\n"):
			v.addf(path+".name", "network name %q must not contain whitespace", network.Name)
		default:
			if prev, ok := seenNames[foldKey(network.Name)]; ok {
				v.addf(
					path+".name",
					"conflicting network name %q (also networks[%d])",
					network.Name, prev,
				)
			} else {
				seenNames[foldKey(network.Name)] = i
			}
		}

		if network.Alias == nil {
			continue
		}

		alias := *network.Alias

		if alias < 1 || alias > maxVLANAlias {
			v.addf(path+".alias", "VLAN alias %d is out of range (1-%d)", alias, maxVLANAlias)

			continue
		}

		if prev, ok := seenAliases[alias]; ok {
			v.addf(path+".alias", "conflicting VLAN alias %d (also networks[%d])", alias, prev)
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

		v.validateID(path+".id", "node", node.ID)

		if prev, ok := seenIDs[foldKey(node.ID)]; ok {
			v.addf(path+".id", "duplicate node ID %q (also nodes[%d])", node.ID, prev)
		} else {
			seenIDs[foldKey(node.ID)] = i
			v.nodesByID[node.ID] = node
		}

		if !finite(node.Position.X) || !finite(node.Position.Y) {
			v.addf(path+".position", "position values must be finite numbers")
		}

		if node.Size != nil {
			if !finite(node.Size.Width) || !finite(node.Size.Height) ||
				node.Size.Width <= 0 || node.Size.Height <= 0 {
				v.addf(path+".size", "size values must be positive finite numbers")
			}
		}

		v.validateNodePayload(node, path)

		switch node.Kind {
		case NodeKindDevice:
			if node.Device == nil {
				break
			}

			hostname := node.Device.Hostname

			switch {
			case strings.TrimSpace(hostname) == "":
				v.addf(path+".device.hostname", "hostname is required")
			case strings.ContainsAny(hostname, " \t\n"):
				v.addf(path+".device.hostname", "hostname %q must not contain whitespace", hostname)
			default:
				if prev, ok := seenHostnames[foldKey(hostname)]; ok {
					v.addf(
						path+".device.hostname",
						"duplicate hostname %q (also nodes[%d])",
						hostname, prev,
					)
				} else {
					seenHostnames[foldKey(hostname)] = i
				}
			}

			v.validateDeviceHandles(node, path)
			v.validateIconKey(node.Device.IconKey, path+".device.iconKey")
		case NodeKindSwitch:
			if node.Switch == nil {
				break
			}

			if node.Switch.NetworkID == "" {
				v.addf(path+".switch.networkId", "switch must reference a network")
			} else if _, ok := v.networksByID[node.Switch.NetworkID]; !ok {
				v.addf(
					path+".switch.networkId",
					"unknown network %q",
					node.Switch.NetworkID,
				)
			}
		case NodeKindNote, NodeKindGroup:
			// Notes and groups carry no phenix semantics.
		}
	}
}

func (v *validator) validateNodePayload(node *Node, path string) {
	switch node.Kind {
	case NodeKindDevice, NodeKindSwitch, NodeKindNote, NodeKindGroup:
	default:
		v.addf(path+".kind", "unknown node kind %q", node.Kind)

		return
	}

	payloads := map[NodeKind]bool{
		NodeKindDevice: node.Device != nil,
		NodeKindSwitch: node.Switch != nil,
		NodeKindNote:   node.Note != nil,
		NodeKindGroup:  node.Group != nil,
	}

	if !payloads[node.Kind] {
		v.addf(path, "node of kind %q is missing its %q payload", node.Kind, node.Kind)
	}

	for kind, present := range payloads {
		if present && kind != node.Kind {
			v.addf(path, "node of kind %q must not carry a %q payload", node.Kind, kind)
		}
	}

	if node.Kind == NodeKindDevice && node.Device != nil && node.Device.Spec == nil {
		v.addf(path+".device.spec", "device spec is required")
	}
}

// validateIconKey enforces the bounded icon key registry shared with the
// generated JSON Schema. An empty key means "use the default icon".
func (v *validator) validateIconKey(key, path string) {
	if key == "" || IsIconKey(key) {
		return
	}

	if iconKeyLooksExternal(key) {
		v.addf(
			path,
			"icon key %q must be one of the built-in keys, not a URL or path",
			key,
		)

		return
	}

	v.addf(path, "unknown icon key %q (expected one of %s)", key, strings.Join(iconKeys, ", "))
}

func (v *validator) validateDeviceHandles(node *Node, path string) {
	seenNames := map[string]int{}

	for j := range node.Device.Interfaces {
		handle := &node.Device.Interfaces[j]
		handlePath := fmt.Sprintf("%s.device.interfaces[%d]", path, j)

		v.validateID(handlePath+".id", "interface handle", handle.ID)

		if owner, ok := v.handleOwner[foldKey(handle.ID)]; ok {
			v.addf(
				handlePath+".id",
				"duplicate interface handle ID %q (also used by node %q)",
				handle.ID, owner.ID,
			)
		} else if handle.ID != "" {
			v.handleOwner[foldKey(handle.ID)] = node
		}

		if strings.TrimSpace(handle.Name) == "" {
			v.addf(handlePath+".name", "interface name is required")

			continue
		}

		if prev, ok := seenNames[foldKey(handle.Name)]; ok {
			v.addf(
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
			v.addf(path, "node cannot be its own parent")

			continue
		}

		parent, ok := v.nodesByID[node.ParentID]
		if !ok {
			v.addf(path, "unknown parent node %q", node.ParentID)

			continue
		}

		if parent.Kind != NodeKindGroup {
			v.addf(path, "parent node %q is not a group", node.ParentID)

			continue
		}

		if v.parentCycle(node) {
			v.addf(path, "group membership cycle detected at node %q", node.ID)
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

func (v *validator) validateEdges() {
	seenIDs := map[string]int{}
	connected := map[string]int{}

	for i := range v.doc.Edges {
		edge := &v.doc.Edges[i]
		path := fmt.Sprintf("edges[%d]", i)

		v.validateID(path+".id", "edge", edge.ID)

		if prev, ok := seenIDs[foldKey(edge.ID)]; ok {
			v.addf(path+".id", "duplicate edge ID %q (also edges[%d])", edge.ID, prev)
		} else {
			seenIDs[foldKey(edge.ID)] = i
		}

		source, sourceOK := v.nodesByID[edge.SourceNodeID]
		if !sourceOK {
			v.addf(path+".sourceNodeId", "unknown node %q", edge.SourceNodeID)
		}

		target, targetOK := v.nodesByID[edge.TargetNodeID]
		if !targetOK {
			v.addf(path+".targetNodeId", "unknown node %q", edge.TargetNodeID)
		}

		if !sourceOK || !targetOK {
			continue
		}

		if source.ID == target.ID {
			v.addf(path, "edge endpoints must differ")

			continue
		}

		device, deviceHandle, switchNode, ok := edgeEndpoints(source, edge.SourceHandleID, target, edge.TargetHandleID)
		if !ok {
			v.addf(path, "an edge must connect one device interface to one switch")

			continue
		}

		handle := device.Device.InterfaceHandle(deviceHandle)
		if handle == nil {
			v.addf(
				path,
				"unknown interface handle %q on device node %q",
				deviceHandle, device.ID,
			)

			continue
		}

		if prev, ok := connected[deviceHandle]; ok {
			v.addf(
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

		if edge.NetworkID == "" {
			v.addf(path+".networkId", "edge must reference a network")

			continue
		}

		if _, ok := v.networksByID[edge.NetworkID]; !ok {
			v.addf(path+".networkId", "unknown network %q", edge.NetworkID)

			continue
		}

		if edge.NetworkID != switchNode.Switch.NetworkID {
			v.addf(
				path+".networkId",
				"network %q does not match network %q of switch %q",
				edge.NetworkID, switchNode.Switch.NetworkID, switchNode.ID,
			)
		}
	}
}

func (v *validator) validateScenario() {
	ref := v.doc.Scenario
	if ref == nil {
		return
	}

	switch ref.Kind {
	case ScenarioRefStored:
		if strings.TrimSpace(ref.Name) == "" {
			v.addf("scenario.name", "stored scenario reference requires a name")
		}
	case ScenarioRefUploaded:
		if len(ref.Content) == 0 {
			v.addf("scenario.content", "uploaded scenario reference requires content")
		}
	default:
		v.addf("scenario.kind", "unknown scenario reference kind %q", ref.Kind)

		return
	}

	if strings.TrimSpace(ref.APIVersion) == "" {
		v.addf("scenario.apiVersion", "scenario reference requires an apiVersion")
	}

	if v.validateScenarioDigest(ref) {
		v.validateScenarioContent(ref)
	}
}

// scenarioValidationName is the placeholder config name used when validating
// scenario content. A reference's name is a stored config name or an uploaded
// file name, neither of which is guaranteed to satisfy the config metadata name
// pattern, so it is deliberately not used here.
const scenarioValidationName = "scenario"

// validateScenarioContent validates cached or uploaded scenario content against
// the existing phenix scenario schema, so a complete scenario is rejected here
// rather than at publish time. Content-less stored references are validated
// when the referenced config is loaded.
//
// It runs types.ValidateConfigSpec, which never re-enters this package.
func (v *validator) validateScenarioContent(ref *ScenarioRef) {
	if len(ref.Content) == 0 {
		return
	}

	if ref.APIVersion != ScenarioAPIVersion() {
		v.addf(
			"scenario.apiVersion",
			"unsupported scenario apiVersion %q (expected %q)",
			ref.APIVersion, ScenarioAPIVersion(),
		)

		return
	}

	config, err := store.NewConfig(kindScenario + "/" + scenarioValidationName)
	if err != nil {
		v.addf("scenario.content", "building scenario config: %v", err)

		return
	}

	config.Version = ref.APIVersion
	config.Spec = ref.Content

	if err := types.ValidateConfigSpec(*config); err != nil {
		v.addf("scenario.content", "invalid scenario content: %v", err)
	}
}

// validateScenarioDigest requires a well-formed content digest on every
// scenario reference, and requires it to match any cached content. It reports
// whether the digest is trustworthy, so content validation can be skipped when
// it is not.
func (v *validator) validateScenarioDigest(ref *ScenarioRef) bool {
	switch {
	case strings.TrimSpace(ref.Digest) == "":
		v.addf("scenario.digest", "scenario reference requires a content digest")

		return false
	case !isContentDigest(ref.Digest):
		v.addf(
			"scenario.digest",
			"malformed scenario digest %q (expected sha256:<64 hex>)",
			ref.Digest,
		)

		return false
	case len(ref.Content) == 0:
		return true
	}

	digest, err := ContentDigest(ref.Content)
	if err != nil {
		v.addf("scenario.content", "content is not encodable: %v", err)

		return false
	}

	if ref.Digest != digest {
		v.addf("scenario.digest", "content digest mismatch (expected %s)", digest)

		return false
	}

	return true
}

func (v *validator) validateSource() {
	if v.doc.Source == nil {
		return
	}

	switch v.doc.Source.Kind {
	case SourceKindManual, SourceKindTopology, SourceKindExperiment:
	default:
		v.addf("source.kind", "unknown source kind %q", v.doc.Source.Kind)
	}

	for i, name := range v.doc.Source.IncludeTopologies {
		path := fmt.Sprintf("source.includeTopologies[%d]", i)

		switch {
		case strings.TrimSpace(name) == "":
			v.addf(path, "included topology name is required")
		case strings.ContainsAny(name, " \t\n"):
			v.addf(
				path,
				"included topology name %q must not contain whitespace",
				name,
			)
		}
	}

	if digest := v.doc.Source.Digest; digest != "" && !isContentDigest(digest) {
		v.addf("source.digest", "malformed source digest %q (expected sha256:<64 hex>)", digest)
	}
}

// validateID enforces the identifier contract: every entity identifier is an
// RFC 4122 UUID. Generated identifiers are name based UUIDs; identifiers minted
// by the front end come from crypto.randomUUID.
func (v *validator) validateID(path, kindName, id string) {
	if strings.TrimSpace(id) == "" {
		v.addf(path, "%s ID is required", kindName)

		return
	}

	if !IsUUID(id) {
		v.addf(path, "%s ID %q is not a valid UUID", kindName, id)
	}
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
