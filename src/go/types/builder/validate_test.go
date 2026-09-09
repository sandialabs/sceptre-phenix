package builder_test

import (
	"errors"
	"math"
	"strings"
	"testing"

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
				nodeWithHostname(d, "host-a").Device.Interfaces[0].ID = idHRouterEth1
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
			mutate:  func(d *builder.Document) { d.Networks[1].Name = "exp" },
			wantMsg: "conflicting network name",
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
