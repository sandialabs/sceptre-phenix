package builder

// The examples of the schema documentation (see [documented]) describe one
// small diagram: a router on network EXP, a note next to it, a group, and,
// drawn next to them, the Internet as an icon, a rectangle around the DMZ and
// a line from the Internet to the router. Each function returns its own
// value, so a caller that changes one schema that [Schema] returns changes no
// other. Every example is valid against the schema it documents. A test in
// the front end uses ajv to check this.
const (
	exampleDocumentID = "49d876d1-571a-5b5c-91b7-aadf5bb5209c"
	exampleGroupID    = "c60601dd-6d6b-56c7-97e5-149caf5ed993"
	exampleDeviceID   = "4113dfc0-f4be-57f9-8115-6cecd1f1aea3"
	exampleSwitchID   = "dee6bc70-8103-581e-8b31-6c95cda798a0"
	exampleNoteID     = "874e945f-4f75-57c7-9533-0d8ffd6ec32b"
	exampleNetworkID  = "a8af611d-c68f-54e0-94a5-9176cea64e44"
	exampleHandleID   = "7f010dcd-e23d-514c-9206-3609df999026"
	exampleEdgeID     = "503c4f01-e7e0-5a2e-a07d-26e2776fea8f"
	exampleTemplateID = "0d5f8f4e-6a57-4b53-9d0a-5c0f4e3b2a11"
	exampleShapeID    = "3c9e5d2a-7b41-5f08-9a6e-2d4b8c1f0e73"
	exampleIconNodeID = "b2f4a6c8-1d3e-5f70-8a9b-0c1d2e3f4a5b"
	exampleLineID     = "e7d1c3b5-9a8f-5e6d-b4c3-a2918f7e6d5c"

	exampleHostname      = "router"
	exampleIconKey       = "router"
	exampleInterface     = "eth0"
	exampleNetworkName   = "EXP"
	exampleTopology      = "pump-station"
	exampleInclude       = "shared-services"
	exampleScenarioName  = "pump-station-ntp"
	exampleAPIVersion    = "phenix.sandia.gov/v1"
	exampleDigest        = "sha256:1f7f8940e18192505e3ec1f993db7204b6baa8d6c1d9156917a82c663656a1b4"
	exampleOutlineColor  = "#2f6fbf"
	exampleFillColor     = "#eef3fb"
	exampleNoteText      = "Snapshot the PLC images before each run."
	exampleGroupTitle    = "Control network"
	exampleCreatedBy     = "alice"
	exampleCreatedAt     = "2026-10-01T15:04:05Z"
	exampleUpdatedBy     = "bob"
	exampleUpdatedAt     = "2026-10-02T09:30:00Z"
	exampleSourceUpdated = "2026-09-30T08:00:00Z"
	exampleShapeLabel    = "DMZ"
	exampleIconLabel     = "Internet"
	exampleIconNodeKey   = "external"
	exampleLineLabel     = "uplink to ISP"
	exampleLineColor     = "#c0392b"

	// exampleIconName names a custom icon, and exampleIconData is a PNG of
	// one pixel.
	exampleIconData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	exampleIconName = "plc"

	// Canvas coordinates and sizes, in pixels, and the router's address.
	exampleX       = 160
	exampleY       = 256
	exampleLineX   = 80
	exampleNoteX   = 320
	exampleWidth   = 720
	exampleHeight  = 420
	exampleAlias   = 101
	exampleMask    = 24
	exampleAddress = "10.0.0.1"
)

// examplePosition returns a canvas position.
func examplePosition(x, y float64) map[string]any {
	return map[string]any{"x": x, "y": y}
}

// exampleSize returns the size of a group.
func exampleSize() map[string]any {
	return map[string]any{"width": exampleWidth, "height": exampleHeight}
}

// exampleViewport returns the viewport of a canvas at 100 percent.
func exampleViewport() map[string]any {
	return map[string]any{"x": 0, "y": 0, "zoom": 1}
}

// exampleGrid returns the editor's default grid.
func exampleGrid() map[string]any {
	return map[string]any{"enabled": true, keySize: defaultGridSize, "snap": true}
}

// exampleSpec returns the phenix node spec of the router: a VyOS VM with one
// statically addressed interface on network EXP.
func exampleSpec() map[string]any {
	return map[string]any{
		keyType:   "Router",
		"general": map[string]any{"hostname": exampleHostname},
		"hardware": map[string]any{
			"os_type": "vyos",
			"drives":  []any{map[string]any{"image": "vyos.qc2"}},
		},
		"network": map[string]any{
			"interfaces": []any{map[string]any{
				keyName:   exampleInterface,
				keyType:   "ethernet",
				"proto":   "static",
				"address": exampleAddress,
				"mask":    exampleMask,
				"vlan":    exampleNetworkName,
			}},
		},
	}
}

// exampleHandle returns the connection handle of the router's interface.
func exampleHandle() map[string]any {
	return map[string]any{keyID: exampleHandleID, keyName: exampleInterface, "index": 0}
}

// exampleDevice returns the payload of the router's node.
func exampleDevice() map[string]any {
	return map[string]any{
		"hostname":   exampleHostname,
		keyIconKey:   exampleIconKey,
		keySpec:      exampleSpec(),
		"interfaces": []any{exampleHandle()},
	}
}

// exampleDeviceNode returns the router's node.
func exampleDeviceNode() map[string]any {
	return map[string]any{
		keyID:                  exampleDeviceID,
		keyKind:                string(NodeKindDevice),
		"label":                exampleHostname,
		keyPosition:            examplePosition(0, 0),
		string(NodeKindDevice): exampleDevice(),
	}
}

// exampleSwitch returns the payload of the switch of network EXP.
func exampleSwitch() map[string]any {
	return map[string]any{keyNetworkID: exampleNetworkID}
}

// exampleSwitchNotes returns notes about the switch of network EXP.
func exampleSwitchNotes() []any {
	return []any{"Mirror port 24 feeds the IDS.", "Patch panel B, rack 2"}
}

// exampleSwitchNode returns the switch of network EXP.
func exampleSwitchNode() map[string]any {
	return map[string]any{
		keyID:                  exampleSwitchID,
		keyKind:                string(NodeKindSwitch),
		"label":                exampleNetworkName,
		keyPosition:            examplePosition(exampleX, exampleY),
		string(NodeKindSwitch): exampleSwitch(),
	}
}

// exampleNote returns the payload of the note beside the router.
func exampleNote() map[string]any {
	return map[string]any{"text": exampleNoteText}
}

// exampleNoteNode returns the note beside the router.
func exampleNoteNode() map[string]any {
	return map[string]any{
		keyID:                exampleNoteID,
		keyKind:              string(NodeKindNote),
		keyPosition:          examplePosition(exampleNoteX, 0),
		string(NodeKindNote): exampleNote(),
	}
}

// exampleGroup returns the payload of a group.
func exampleGroup() map[string]any {
	return map[string]any{"title": exampleGroupTitle}
}

// exampleShape returns the payload of a rectangle marking the DMZ.
func exampleShape() map[string]any {
	return map[string]any{
		"shape":         "rectangle",
		keyLabel:        exampleShapeLabel,
		keyFillColor:    exampleFillColor,
		keyOutlineColor: exampleOutlineColor,
		keyBorderStyle:  "dashed",
	}
}

// exampleShapeNode returns the rectangle marking the DMZ.
func exampleShapeNode() map[string]any {
	return map[string]any{
		keyID:                 exampleShapeID,
		keyKind:               string(NodeKindShape),
		keyPosition:           examplePosition(-exampleNoteX, 0),
		keySize:               map[string]any{"width": exampleNoteX, "height": exampleX},
		string(NodeKindShape): exampleShape(),
	}
}

// exampleIconMark returns the payload of the icon standing for the
// Internet.
func exampleIconMark() map[string]any {
	return map[string]any{keyIconKey: exampleIconNodeKey, keyLabel: exampleIconLabel}
}

// exampleIconNode returns the icon standing for the Internet.
func exampleIconNode() map[string]any {
	return map[string]any{
		keyID:                exampleIconNodeID,
		keyKind:              string(NodeKindIcon),
		keyPosition:          examplePosition(0, -exampleY),
		string(NodeKindIcon): exampleIconMark(),
	}
}

// exampleLinePoints returns the points of the line from the Internet to the
// router: down, then across, relative to the line node's position.
func exampleLinePoints() []any {
	return []any{
		examplePosition(0, 0),
		examplePosition(0, exampleX),
		examplePosition(exampleX, exampleX),
	}
}

// exampleLine returns the payload of the line from the Internet to the
// router, with an arrowhead at the router.
func exampleLine() map[string]any {
	return map[string]any{
		"points":     exampleLinePoints(),
		keyLabel:     exampleLineLabel,
		keyColor:     exampleLineColor,
		keyLineStyle: "dashed",
		"endArrow":   true,
	}
}

// exampleLineNode returns the line from the Internet to the router.
func exampleLineNode() map[string]any {
	return map[string]any{
		keyID:                exampleLineID,
		keyKind:              string(NodeKindLine),
		keyPosition:          examplePosition(exampleLineX, -exampleX),
		keySize:              map[string]any{"width": exampleX, "height": exampleX},
		string(NodeKindLine): exampleLine(),
	}
}

// exampleNetwork returns network EXP.
func exampleNetwork() map[string]any {
	return map[string]any{
		keyID:          exampleNetworkID,
		keyName:        exampleNetworkName,
		"alias":        exampleAlias,
		keyDescription: "Experiment network",
	}
}

// exampleEdge returns the connection of the router's interface to the
// switch of network EXP.
func exampleEdge() map[string]any {
	return map[string]any{
		keyID:            exampleEdgeID,
		"sourceNodeId":   exampleDeviceID,
		"sourceHandleId": exampleHandleID,
		"targetNodeId":   exampleSwitchID,
		keyNetworkID:     exampleNetworkID,
	}
}

// exampleRoute returns the path an automatic layout drew for the router's
// connection.
func exampleRoute() []any {
	return []any{
		examplePosition(exampleX, 0),
		examplePosition(exampleX, exampleY),
	}
}

// exampleSource returns the source of a document generated from topology
// pump-station.
func exampleSource() map[string]any {
	return map[string]any{
		keyKind:       string(SourceKindTopology),
		keyName:       exampleTopology,
		keyAPIVersion: exampleAPIVersion,
		"importedAt":  exampleCreatedAt,
		keyDigest:     exampleDigest,
		keyUpdatedAt:  exampleSourceUpdated,
	}
}

// exampleWarning returns a warning generation raises.
func exampleWarning() string {
	return "included topology " + exampleInclude + " could not be read and its nodes are not shown"
}

// exampleIcon returns a custom icon.
func exampleIcon() map[string]any {
	return map[string]any{"data": exampleIconData}
}

// exampleTemplateDevice returns what a template of the router fills in.
func exampleTemplateDevice() map[string]any {
	return map[string]any{keyIconKey: exampleIconKey, keySpec: exampleSpec()}
}

// exampleTemplate returns a template of the router.
func exampleTemplate() map[string]any {
	return map[string]any{
		keyID:          exampleTemplateID,
		keyName:        "Edge router",
		keyDescription: "VyOS router with one static interface",
		"device":       exampleTemplateDevice(),
	}
}

// exampleNotes returns notes about a diagram.
func exampleNotes() []any {
	return []any{exampleNoteText, "VLAN " + exampleNetworkName + " carries the control traffic.\nKeep it isolated."}
}

// exampleMetadata returns the metadata of a document a user made and
// another saved.
func exampleMetadata() map[string]any {
	return map[string]any{
		keyID:          exampleDocumentID,
		keyName:        "Pump station",
		keyDescription: "Water treatment pump station with two PLCs and an HMI.",
		keyCreatedBy:   exampleCreatedBy,
		keyCreatedAt:   exampleCreatedAt,
		keyUpdatedBy:   exampleUpdatedBy,
		keyUpdatedAt:   exampleUpdatedAt,
		keyNotes:       exampleNotes(),
	}
}
