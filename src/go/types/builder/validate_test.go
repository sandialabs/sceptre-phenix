package builder_test

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types/builder"
)

//nolint:maintidx // table driven validation coverage
func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*builder.Document)
		wantMsg string
	}{
		{
			name:    "missing document id",
			mutate:  func(d *builder.Document) { d.ID = "" },
			wantMsg: "document ID is required",
		},
		{
			name:    "wrong schema",
			mutate:  func(d *builder.Document) { d.Schema = "https://example.com/x" },
			wantMsg: "schema",
		},
		{
			name:    "wrong revision",
			mutate:  func(d *builder.Document) { d.Revision = 7 },
			wantMsg: "revision",
		},
		{
			// The draft service records the name as the draft title.
			name:    "name longer than a draft title",
			mutate:  func(d *builder.Document) { d.Name = strings.Repeat("é", builder.MaxNameBytes/2+1) },
			wantMsg: "document name must be at most 512 bytes",
		},
		{
			name:    "name with a control character",
			mutate:  func(d *builder.Document) { d.Name = "my\ttopology" },
			wantMsg: "document name must not contain control characters",
		},
		{
			name: "duplicate node id ignoring case",
			mutate: func(d *builder.Document) {
				d.Nodes[1].ID = strings.ToUpper(d.Nodes[2].ID)
			},
			wantMsg: "duplicate node ID",
		},
		{
			name: "duplicate hostname ignoring case",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "host-a").Device.Hostname = "ROUTER"
			},
			wantMsg: "duplicate hostname",
		},
		{
			name: "hostname with whitespace",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "host-a").Device.Hostname = "bad host"
			},
			wantMsg: "must not contain whitespace",
		},
		{
			name: "missing device spec",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "host-a").Device.Spec = nil
			},
			wantMsg: "device spec is required",
		},
		{
			name: "payload does not match kind",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "host-a").Switch = &builder.Switch{NetworkID: idNetExp}
			},
			wantMsg: `must not carry a "switch" payload`,
		},
		{
			name: "missing payload",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "host-a").Device = nil
			},
			wantMsg: `missing its "device" payload`,
		},
		{
			name: "unknown kind",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "host-a").Kind = "gadget"
			},
			wantMsg: "unknown node kind",
		},
		{
			name: "duplicate interface handle id",
			mutate: func(d *builder.Document) {
				// Handle IDs compare ignoring case, which still catches an
				// exact duplicate.
				nodeWithHostname(d, "host-a").Device.Interfaces[0].ID = strings.ToUpper(idHRouterEth1)
			},
			wantMsg: "duplicate interface handle ID",
		},
		{
			name: "duplicate interface name",
			mutate: func(d *builder.Document) {
				node := nodeWithHostname(d, "router")
				node.Device.Interfaces[1].Name = "ETH0"
			},
			wantMsg: "duplicate interface name",
		},
		{
			name:    "dangling parent",
			mutate:  func(d *builder.Document) { d.Nodes[1].ParentID = "missing" },
			wantMsg: "unknown parent node",
		},
		{
			name:    "parent is not a group",
			mutate:  func(d *builder.Document) { d.Nodes[1].ParentID = idSwExp },
			wantMsg: "is not a group",
		},
		{
			name:    "self parent",
			mutate:  func(d *builder.Document) { d.Nodes[1].ParentID = d.Nodes[1].ID },
			wantMsg: "cannot be its own parent",
		},
		{
			name: "group cycle",
			mutate: func(d *builder.Document) {
				d.Nodes = append(d.Nodes, builder.Node{
					ID:    idGrpInner,
					Kind:  builder.NodeKindGroup,
					Group: &builder.Group{Title: "inner"},
				})

				d.Nodes[len(d.Nodes)-1].ParentID = idGrpRack
				groupNode(d, idGrpRack).ParentID = idGrpInner
			},
			wantMsg: "group membership cycle",
		},
		{
			name:    "switch without network",
			mutate:  func(d *builder.Document) { switchNode(d).Switch.NetworkID = "" },
			wantMsg: "switch must reference a network",
		},
		{
			name:    "switch with dangling network",
			mutate:  func(d *builder.Document) { switchNode(d).Switch.NetworkID = idNetNope },
			wantMsg: "unknown network",
		},
		{
			name:    "edge with dangling node",
			mutate:  func(d *builder.Document) { d.Edges[0].SourceNodeID = "nope" },
			wantMsg: "unknown node",
		},
		{
			name: "edge between two devices",
			mutate: func(d *builder.Document) {
				d.Edges[0].TargetNodeID = idDevHost
				d.Edges[0].TargetHandleID = idHHostEth0
			},
			wantMsg: "must connect one device interface to one switch",
		},
		{
			name:    "edge to a note",
			mutate:  func(d *builder.Document) { d.Edges[0].TargetNodeID = idNote1 },
			wantMsg: "must connect one device interface to one switch",
		},
		{
			name:    "edge without a device handle",
			mutate:  func(d *builder.Document) { d.Edges[0].SourceHandleID = "" },
			wantMsg: "must connect one device interface to one switch",
		},
		{
			name:    "edge with unknown handle",
			mutate:  func(d *builder.Document) { d.Edges[0].SourceHandleID = idHHostEth0 },
			wantMsg: "unknown interface handle",
		},
		{
			name: "interface connected twice",
			mutate: func(d *builder.Document) {
				extra := d.Edges[0]
				extra.ID = idEDuplicate
				d.Edges = append(d.Edges, extra)
			},
			wantMsg: "is already connected",
		},
		{
			name:    "edge with dangling network",
			mutate:  func(d *builder.Document) { d.Edges[0].NetworkID = idNetNope },
			wantMsg: "unknown network",
		},
		{
			name:    "edge network does not match switch",
			mutate:  func(d *builder.Document) { d.Edges[0].NetworkID = idNetUnused },
			wantMsg: "does not match network",
		},
		{
			name:    "duplicate edge id",
			mutate:  func(d *builder.Document) { d.Edges[1].ID = d.Edges[0].ID },
			wantMsg: "duplicate edge ID",
		},
		{
			name:    "duplicate network id",
			mutate:  func(d *builder.Document) { d.Networks[1].ID = strings.ToUpper(idNetExp) },
			wantMsg: "duplicate network ID",
		},
		{
			name:    "conflicting network names",
			mutate:  func(d *builder.Document) { d.Networks[1].Name = d.Networks[0].Name },
			wantMsg: "conflicting network name",
		},
		{
			name:    "null edges",
			mutate:  func(d *builder.Document) { d.Edges = nil },
			wantMsg: "edges: edges must be an array",
		},
		{
			name:    "network name with whitespace",
			mutate:  func(d *builder.Document) { d.Networks[1].Name = "two words" },
			wantMsg: "must not contain whitespace",
		},
		{
			name:    "conflicting aliases",
			mutate:  func(d *builder.Document) { *d.Networks[1].Alias = 101 },
			wantMsg: "conflicting VLAN alias",
		},
		{
			name:    "alias out of range",
			mutate:  func(d *builder.Document) { *d.Networks[1].Alias = 9000 },
			wantMsg: "out of range",
		},
		{
			name:    "stored scenario without name",
			mutate:  func(d *builder.Document) { d.Scenario.Name = "" },
			wantMsg: "stored scenario reference requires a name",
		},
		{
			name: "stored scenario without api version",
			mutate: func(d *builder.Document) {
				d.Scenario.APIVersion = ""
			},
			wantMsg: "scenario reference requires an apiVersion",
		},
		{
			name:    "stored scenario without digest",
			mutate:  func(d *builder.Document) { d.Scenario.Digest = "" },
			wantMsg: "scenario reference requires a content digest",
		},
		{
			name:    "stored scenario with malformed digest",
			mutate:  func(d *builder.Document) { d.Scenario.Digest = "sha256:deadbeef" },
			wantMsg: "malformed scenario digest",
		},
		{
			// The schema requires lowercase hex, and publish compares
			// digests as strings, so an uppercase one could never match.
			name: "stored scenario with uppercase digest",
			mutate: func(d *builder.Document) {
				d.Scenario.Digest = "sha256:" + strings.Repeat("AB", 32)
			},
			wantMsg: "malformed scenario digest",
		},
		{
			name: "uploaded scenario without content",
			mutate: func(d *builder.Document) {
				d.Scenario = uploadedScenario(nil)
			},
			wantMsg: "uploaded scenario reference requires content",
		},
		{
			name: "uploaded scenario without api version",
			mutate: func(d *builder.Document) {
				ref := uploadedScenario(map[string]any{"apps": []any{}})
				ref.APIVersion = ""
				d.Scenario = ref
			},
			wantMsg: "scenario reference requires an apiVersion",
		},
		{
			// The Scenario dialog refuses such an upload; a document that
			// carries one anyway is refused here, rather than upgraded.
			name: "uploaded v1 scenario",
			mutate: func(d *builder.Document) {
				ref := uploadedScenario(map[string]any{"apps": map[string]any{"experiment": []any{}}})
				ref.APIVersion = "phenix.sandia.gov/v1"
				d.Scenario = ref
			},
			wantMsg: `unsupported scenario apiVersion "phenix.sandia.gov/v1" (expected "phenix.sandia.gov/v2")`,
		},
		{
			name: "scenario content without digest",
			mutate: func(d *builder.Document) {
				ref := uploadedScenario(map[string]any{"apps": []any{}})
				ref.Digest = ""
				d.Scenario = ref
			},
			wantMsg: "scenario reference requires a content digest",
		},
		{
			name: "scenario digest mismatch",
			mutate: func(d *builder.Document) {
				ref := uploadedScenario(map[string]any{"apps": []any{}})
				ref.Content = map[string]any{"apps": []any{"changed"}}
				d.Scenario = ref
			},
			wantMsg: "content digest mismatch",
		},
		{
			name: "unknown scenario kind",
			mutate: func(d *builder.Document) {
				d.Scenario.Kind = "linked"
			},
			wantMsg: "unknown scenario reference kind",
		},
		{
			name: "unknown source kind",
			mutate: func(d *builder.Document) {
				d.Source = &builder.Source{Kind: "telepathy"}
			},
			wantMsg: "unknown source kind",
		},
		{
			name:    "non finite position",
			mutate:  func(d *builder.Document) { d.Nodes[1].Position.X = math.NaN() },
			wantMsg: "position values must be finite",
		},
		{
			name: "negative size",
			mutate: func(d *builder.Document) {
				d.Nodes[1].Size = &builder.Size{Width: -1, Height: 10}
			},
			wantMsg: "size values must be positive",
		},
		{
			name:    "negative zoom",
			mutate:  func(d *builder.Document) { d.Viewport.Zoom = -1 },
			wantMsg: "zoom must be a positive number",
		},
		{
			// Publishing would drop the device with nothing to bring it back.
			name: "included device in a document that includes nothing",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "host-a").Device.IncludedFrom = "shared"
			},
			wantMsg: `included from topology "shared", but the document includes no topologies`,
		},
		{
			name: "included device naming a topology with whitespace",
			mutate: func(d *builder.Document) {
				d.Source.IncludeTopologies = []string{"shared"}
				nodeWithHostname(d, "host-a").Device.IncludedFrom = "bad name"
			},
			wantMsg: "must not be blank or contain whitespace",
		},
		{
			name:    "zero zoom",
			mutate:  func(d *builder.Document) { d.Viewport.Zoom = 0 },
			wantMsg: "zoom must be a positive number",
		},
		{
			name:    "zero grid size",
			mutate:  func(d *builder.Document) { d.Grid.Size = 0 },
			wantMsg: "grid size must be a positive finite number",
		},
		{
			name: "zero width",
			mutate: func(d *builder.Document) {
				d.Nodes[0].Size = &builder.Size{Width: 0, Height: 10}
			},
			wantMsg: "size values must be positive finite numbers",
		},
		{
			name: "zero height",
			mutate: func(d *builder.Document) {
				d.Nodes[0].Size = &builder.Size{Width: 10, Height: 0}
			},
			wantMsg: "size values must be positive finite numbers",
		},
		{
			name:    "document id that is no UUID",
			mutate:  func(d *builder.Document) { d.ID = "doc-fixture" },
			wantMsg: "document ID \"doc-fixture\" is not a valid UUID",
		},
		{
			name:    "node id that is no UUID",
			mutate:  func(d *builder.Document) { d.Nodes[0].ID = "grp-rack" },
			wantMsg: "node ID \"grp-rack\" is not a valid UUID",
		},
		{
			name:    "network id that is no UUID",
			mutate:  func(d *builder.Document) { d.Networks[0].ID = "net-exp" },
			wantMsg: "network ID \"net-exp\" is not a valid UUID",
		},
		{
			name:    "edge id that is no UUID",
			mutate:  func(d *builder.Document) { d.Edges[0].ID = "e-router-eth0" },
			wantMsg: "edge ID \"e-router-eth0\" is not a valid UUID",
		},
		{
			name: "interface handle id that is no UUID",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "router").Device.Interfaces[0].ID = "h-router-eth0"
			},
			wantMsg: "interface handle ID \"h-router-eth0\" is not a valid UUID",
		},
		{
			name:    "device outline color that is a name",
			mutate:  func(d *builder.Document) { nodeWithHostname(d, "router").Device.OutlineColor = "red" },
			wantMsg: `nodes[1].device.outlineColor: color "red" must be a hex color such as #2f6fbf`,
		},
		{
			name:    "device fill color of three digits",
			mutate:  func(d *builder.Document) { nodeWithHostname(d, "router").Device.FillColor = "#abc" },
			wantMsg: `nodes[1].device.fillColor: color "#abc" must be a hex color such as #2f6fbf`,
		},
		{
			name:    "device fill color with alpha",
			mutate:  func(d *builder.Document) { nodeWithHostname(d, "router").Device.FillColor = "#2f6fbf80" },
			wantMsg: `nodes[1].device.fillColor: color "#2f6fbf80" must be a hex color`,
		},
		{
			name:    "switch outline color that is a function",
			mutate:  func(d *builder.Document) { switchNode(d).Switch.OutlineColor = "rgb(0, 0, 0)" },
			wantMsg: `nodes[3].switch.outlineColor: color "rgb(0, 0, 0)" must be a hex color`,
		},
		{
			name:    "switch fill color that is CSS",
			mutate:  func(d *builder.Document) { switchNode(d).Switch.FillColor = "#fff;background:url(//example.com)" },
			wantMsg: `nodes[3].switch.fillColor: color "#fff;background:url(//example.com)" must be a hex color`,
		},
		{
			name:    "a long color, cut in the message",
			mutate:  func(d *builder.Document) { switchNode(d).Switch.FillColor = strings.Repeat("c", 100) },
			wantMsg: `color "` + strings.Repeat("c", 64) + `..." must be a hex color`,
		},
		{
			name:    "unknown group border style",
			mutate:  func(d *builder.Document) { d.NodeByID(idGrpRack).Group.BorderStyle = "wavy" },
			wantMsg: `nodes[0].group.borderStyle: unknown border style "wavy" (expected one of solid, dashed, dotted, double)`,
		},
		{
			// A line style is not a border style.
			name:    "group border style that is a line style",
			mutate:  func(d *builder.Document) { d.NodeByID(idGrpRack).Group.BorderStyle = "dash-dot" },
			wantMsg: `nodes[0].group.borderStyle: unknown border style "dash-dot"`,
		},
		{
			name:    "unknown network line style",
			mutate:  func(d *builder.Document) { d.Networks[1].LineStyle = "double" },
			wantMsg: `networks[1].lineStyle: unknown line style "double" (expected one of solid, dashed, dotted, dash-dot)`,
		},
		{
			name:    "edge line style in another case",
			mutate:  func(d *builder.Document) { d.Edges[1].LineStyle = "Dashed" },
			wantMsg: `edges[1].lineStyle: unknown line style "Dashed"`,
		},
		{
			name:    "device custom icon the document does not carry",
			mutate:  func(d *builder.Document) { nodeWithHostname(d, "router").Device.Icon = iconFixtureID },
			wantMsg: `nodes[1].device.icon: unknown custom icon "` + iconFixtureID[:64] + `..."`,
		},
		{
			name: "device custom icon that is a built-in key",
			mutate: func(d *builder.Document) {
				d.Icons = map[string]builder.Icon{iconFixtureID: {Data: iconFixtureData}}
				nodeWithHostname(d, "router").Device.Icon = "server"
			},
			wantMsg: `nodes[1].device.icon: unknown custom icon "server"`,
		},
		{
			name:    "group custom icon the document does not carry",
			mutate:  func(d *builder.Document) { d.NodeByID(idGrpRack).Group.Icon = iconFixtureID },
			wantMsg: `nodes[0].group.icon: unknown custom icon`,
		},
		{
			name: "custom icon whose key is not its data's",
			mutate: func(d *builder.Document) {
				d.Icons = map[string]builder.Icon{"sha256:" + strings.Repeat("0", 64): {Data: iconFixtureData}}
			},
			wantMsg: "icons: icon \"sha256:" + strings.Repeat("0", 57) + "...\" does not match its data",
		},
		{
			name: "custom icon that is not a PNG",
			mutate: func(d *builder.Document) {
				d.Icons = map[string]builder.Icon{iconFixtureID: {Data: "PHN2Zy8+"}}
			},
			wantMsg: "is not an accepted PNG: the image is not a PNG",
		},
		{
			name: "unresolved include with whitespace",
			mutate: func(d *builder.Document) {
				d.Source.UnresolvedIncludes = []string{"shared", "two words"}
			},
			wantMsg: `source.unresolvedIncludes[1]: included topology name "two words" must not contain whitespace`,
		},
		{
			name:    "blank unresolved include",
			mutate:  func(d *builder.Document) { d.Source.UnresolvedIncludes = []string{" "} },
			wantMsg: "source.unresolvedIncludes[0]: included topology name is required",
		},
		{
			name:    "blank included topology",
			mutate:  func(d *builder.Document) { d.Source.IncludeTopologies = []string{""} },
			wantMsg: "source.includeTopologies[0]: included topology name is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")

			test.mutate(doc)

			err := doc.Validate()
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}

			if !errors.Is(err, builder.ErrInvalidDocument) {
				t.Fatalf("error %v does not wrap ErrInvalidDocument", err)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.wantMsg)
			}
		})
	}
}

// The presentation fields take the values the editor offers, and none.
func TestValidateAllowsPresentationFields(t *testing.T) {
	for _, style := range append([]string{""}, builder.LineStyles()...) {
		doc := loadDocumentFixture(t, "document.json")
		doc.Networks[0].LineStyle = style
		doc.Edges[0].LineStyle = style

		if err := doc.Validate(); err != nil {
			t.Fatalf("line style %q is refused: %v", style, err)
		}
	}

	for _, style := range append([]string{""}, builder.BorderStyles()...) {
		doc := loadDocumentFixture(t, "document.json")
		doc.NodeByID(idGrpRack).Group.BorderStyle = style

		if err := doc.Validate(); err != nil {
			t.Fatalf("border style %q is refused: %v", style, err)
		}
	}

	for _, value := range []string{"", "#000000", "#2f6fbf", "#ABCDEF", "#aBc123"} {
		doc := loadDocumentFixture(t, "document.json")
		nodeWithHostname(doc, "router").Device.OutlineColor = value
		nodeWithHostname(doc, "router").Device.FillColor = value
		switchNode(doc).Switch.OutlineColor = value
		switchNode(doc).Switch.FillColor = value

		if err := doc.Validate(); err != nil {
			t.Fatalf("color %q is refused: %v", value, err)
		}
	}

	// An icon nothing uses is valid: the editor drops it on its next edit.
	// A group's description is free text, and the colors a note, a group, a
	// network and an edge already had are not checked.
	doc := loadDocumentFixture(t, "document.json")
	doc.Icons = map[string]builder.Icon{iconFixtureID: {Data: iconFixtureData}}
	doc.NodeByID(idGrpRack).Group.Description = "one\ntwo\t" + strings.Repeat("long ", 500)
	doc.NodeByID(idGrpRack).Group.Color = "papayawhip"
	doc.Networks[0].Color = "#abc"
	doc.Edges[0].Color = "rgb(1, 2, 3)"
	doc.Source.UnresolvedIncludes = []string{"plant"}

	if err := doc.Validate(); err != nil {
		t.Fatalf("refused: %v", err)
	}
}

func TestValidateAllowsFreeNotesAndGroups(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	doc.Nodes = append(doc.Nodes,
		builder.Node{
			ID:       idNoteFree,
			Kind:     builder.NodeKindNote,
			Position: builder.Position{X: 900, Y: 100},
			Note:     &builder.Note{Text: "free floating"},
		},
		builder.Node{
			ID:       idGrpFree,
			Kind:     builder.NodeKindGroup,
			Position: builder.Position{X: 900, Y: 300},
			Group:    &builder.Group{Title: "empty group"},
		},
	)

	if err := doc.Validate(); err != nil {
		t.Fatalf("free notes/groups should be valid: %v", err)
	}
}

func TestValidateAllowsNestedGroups(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	doc.Nodes = append(doc.Nodes, builder.Node{
		ID:       idGrpInner,
		Kind:     builder.NodeKindGroup,
		ParentID: idGrpRack,
		Group:    &builder.Group{Title: "inner"},
	})

	nodeWithHostname(doc, "host-a").ParentID = idGrpInner

	if err := doc.Validate(); err != nil {
		t.Fatalf("nested groups should be valid: %v", err)
	}
}

func TestValidateReportsEveryIssue(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	doc.ID = ""
	doc.Networks[0].Name = ""
	doc.Edges[0].NetworkID = idNetNope

	err := doc.Validate()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	var validationErr *builder.ValidationError

	if !errors.As(err, &validationErr) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}

	if len(validationErr.Issues) < 3 {
		t.Fatalf("expected at least 3 issues, got %d: %v", len(validationErr.Issues), validationErr.Issues)
	}
}

// TestValidateEdgesOfADeviceWithManyInterfaces checks the edges of a device
// with thousands of interfaces: each names a handle that is found, a handle
// the device does not have is reported, and of two handles with one ID an
// edge names the first.
func TestValidateEdgesOfADeviceWithManyInterfaces(t *testing.T) {
	const count = 4000

	interfaces := make([]any, count)
	for i := range interfaces {
		interfaces[i] = map[string]any{"name": "eth" + strconv.Itoa(i), "vlan": "vlan" + strconv.Itoa(i)}
	}

	doc, _ := documentFromConfig(t, store.Config{
		Version:  builder.TopologyAPIVersion,
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "wide"},
		Spec: map[string]any{"nodes": []any{map[string]any{
			"general": map[string]any{"hostname": "wide"},
			"network": map[string]any{"interfaces": interfaces},
		}}},
	})

	device := nodeWithHostname(doc, "wide")
	if len(device.Device.Interfaces) != count || len(doc.Edges) != count {
		t.Fatalf("handles = %d, edges = %d, want %d of each", len(device.Device.Interfaces), len(doc.Edges), count)
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	// handleOf points at the end of an edge that names the device's handle.
	handleOf := func(edge *builder.Edge) *string {
		if edge.SourceNodeID == device.ID {
			return &edge.SourceHandleID
		}

		return &edge.TargetHandleID
	}

	first, last := handleOf(&doc.Edges[0]), handleOf(&doc.Edges[count-1])
	kept := *last

	*last = idHHostEth0

	err := doc.Validate()
	if want := "edges[" + strconv.Itoa(count-1) + "]: unknown interface handle"; err == nil ||
		!strings.Contains(err.Error(), want) {
		t.Errorf("Validate = %v, want %q", err, want)
	}

	// Two handles now share the ID the first edge names, and both edges
	// name it: the interface they are told to share is the first of the two.
	for i := range device.Device.Interfaces {
		if device.Device.Interfaces[i].ID == kept {
			device.Device.Interfaces[i].ID = *first
		}
	}

	*last = *first

	shared := device.Device.InterfaceHandle(*first).Name

	err = doc.Validate()
	if want := "edges[" + strconv.Itoa(count-1) + `]: interface "` + shared +
		`" of device "wide" is already connected by edges[0]`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Validate = %v, want %q", err, want)
	}
}

func nodeWithHostname(doc *builder.Document, hostname string) *builder.Node {
	return doc.FindDevice(hostname)
}

func switchNode(doc *builder.Document) *builder.Node {
	nodes := doc.SwitchNodes()
	if len(nodes) == 0 {
		return nil
	}

	return nodes[0]
}

func groupNode(doc *builder.Document, id string) *builder.Node {
	return doc.NodeByID(id)
}

// validationCorpus is testdata/validation-corpus.json, which validate.test.js
// runs through the front end's parseDocument.
type validationCorpus struct {
	Document string `json:"document"`
	Cases    []struct {
		Name string `json:"name"`
		Set  []struct {
			Path  []any `json:"path"`
			Value any   `json:"value"`
			// Join stands in for a long text value: its parts, each text, or
			// text and how many times to repeat it.
			Join []any `json:"join"`
		} `json:"set"`
		Error string `json:"error"`
		// Message is what the issue at Error says, when the case pins it.
		Message string `json:"message"`
		// Issues is every issue of the document, in any order, when the case
		// pins that there are no others.
		Issues []builder.Issue `json:"issues"`
	} `json:"cases"`
}

// joinParts returns the text the join parts of a corpus set entry make.
func joinParts(t *testing.T, parts []any) string {
	t.Helper()

	var text strings.Builder

	for _, part := range parts {
		switch typed := part.(type) {
		case string:
			text.WriteString(typed)
		case []any:
			if len(typed) != 2 {
				t.Fatalf("join part %v is not text and a count", part)
			}

			repeated, isText := typed[0].(string)
			count, isCount := typed[1].(float64)

			if !isText || !isCount {
				t.Fatalf("join part %v is not text and a count", part)
			}

			text.WriteString(strings.Repeat(repeated, int(count)))
		default:
			t.Fatalf("join part %v is not text, or text and a count", part)
		}
	}

	return text.String()
}

// setIn puts value at path in a decoded JSON document.
func setIn(t *testing.T, doc any, path []any, value any) {
	t.Helper()

	for i, key := range path {
		last := i == len(path)-1

		switch container := doc.(type) {
		case map[string]any:
			if last {
				container[key.(string)] = value //nolint:forcetypeassert // corpus keys are strings
			} else {
				doc = container[key.(string)] //nolint:forcetypeassert // corpus keys are strings
			}
		case []any:
			index := int(key.(float64)) //nolint:forcetypeassert // corpus indexes are numbers
			if last {
				container[index] = value
			} else {
				doc = container[index]
			}
		default:
			t.Fatalf("path %v does not reach into the document", path)
		}
	}
}

func TestValidationCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "validation-corpus.json"))
	if err != nil {
		t.Fatalf("reading corpus: %v", err)
	}

	var corpus validationCorpus

	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decoding corpus: %v", err)
	}

	base, err := os.ReadFile(filepath.Join("testdata", corpus.Document))
	if err != nil {
		t.Fatalf("reading %s: %v", corpus.Document, err)
	}

	for _, test := range corpus.Cases {
		t.Run(test.Name, func(t *testing.T) {
			var doc any

			if err := json.Unmarshal(base, &doc); err != nil {
				t.Fatalf("decoding %s: %v", corpus.Document, err)
			}

			for _, set := range test.Set {
				value := set.Value
				if set.Join != nil {
					value = joinParts(t, set.Join)
				}

				setIn(t, doc, set.Path, value)
			}

			encoded, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("encoding: %v", err)
			}

			_, err = builder.Parse(encoded)

			switch {
			case test.Error == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case test.Error != "" && err == nil:
				t.Fatalf("accepted; want an issue at %s", test.Error)
			case test.Error != "" && !refusedAt(err, test.Error, test.Message):
				t.Fatalf("error %q has no issue at %s saying %q", err.Error(), test.Error, test.Message)
			}

			if test.Issues == nil {
				return
			}

			var invalid *builder.ValidationError

			if !errors.As(err, &invalid) {
				t.Fatalf("error %v is not a validation error with issues", err)
			}

			if got, want := issueLines(invalid.Issues), issueLines(test.Issues); !slices.Equal(got, want) {
				t.Fatalf("issues:\n  %s\nwant only:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

// issueLines returns the issues as sorted lines, to compare two lists of
// them whatever their order.
func issueLines(issues []builder.Issue) []string {
	lines := make([]string, 0, len(issues))

	for _, issue := range issues {
		lines = append(lines, issue.Path+": "+issue.Message)
	}

	slices.Sort(lines)

	return lines
}

// refusedAt reports whether err refuses a document at exactly path: an issue
// of a validation error there, saying message when one is given, or a value
// of the wrong type there, which the decoder refuses before validation. For
// a map value of the wrong type, Go before 1.27 names only the map, so the
// map's own path counts too.
func refusedAt(err error, path, message string) bool {
	var (
		invalid  *builder.ValidationError
		mistyped *json.UnmarshalTypeError
	)

	if errors.As(err, &invalid) {
		return slices.ContainsFunc(invalid.Issues, func(issue builder.Issue) bool {
			return issue.Path == path && (message == "" || issue.Message == message)
		})
	}

	if !errors.As(err, &mistyped) || message != "" {
		return false
	}

	parent := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		parent = path[:i]
	}

	return mistyped.Field == path || mistyped.Field == parent
}
