package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types/builder"
)

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*builder.Document)
		wantMsg string
	}{
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
			name: "missing device spec",
			mutate: func(d *builder.Document) {
				nodeWithHostname(d, "host-a").Device.Spec = nil
			},
			wantMsg: "a device needs a spec",
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
			wantMsg: `has no "device" payload`,
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
			wantMsg: "duplicate connection point ID",
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
			wantMsg: "groups contain each other in a cycle",
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
			wantMsg: "must join one device interface to one switch",
		},
		{
			name:    "edge to a note",
			mutate:  func(d *builder.Document) { d.Edges[0].TargetNodeID = idNote1 },
			wantMsg: "must join one device interface to one switch",
		},
		{
			name:    "edge without a device handle",
			mutate:  func(d *builder.Document) { d.Edges[0].SourceHandleID = "" },
			wantMsg: "must join one device interface to one switch",
		},
		{
			name:    "edge with unknown handle",
			mutate:  func(d *builder.Document) { d.Edges[0].SourceHandleID = idHHostEth0 },
			wantMsg: "unknown connection point",
		},
		{
			name: "interface connected twice",
			mutate: func(d *builder.Document) {
				extra := d.Edges[0]
				extra.ID = idEDuplicate
				d.Edges = append(d.Edges, extra)
			},
			wantMsg: "already has another connection",
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
			wantMsg: "duplicate connection ID",
		},
		{
			name:    "duplicate network id",
			mutate:  func(d *builder.Document) { d.Networks[1].ID = strings.ToUpper(idNetExp) },
			wantMsg: "duplicate network ID",
		},
		{
			name:    "non finite position",
			mutate:  func(d *builder.Document) { d.Nodes[1].Position.X = math.NaN() },
			wantMsg: "position values must be finite",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")

			test.mutate(doc)

			err := doc.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}

			if !errors.Is(err, builder.ErrInvalidDocument) {
				t.Errorf("Validate() = %v, want an error wrapping ErrInvalidDocument", err)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Errorf("Validate() = %q, want it to contain %q", err.Error(), test.wantMsg)
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
		t.Errorf("Validate() of free notes and groups = %v, want nil", err)
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
		t.Errorf("Validate() of nested groups = %v, want nil", err)
	}
}

func TestValidateReportsEveryIssue(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	doc.Metadata.ID = ""
	doc.Networks[0].Name = ""
	doc.Edges[0].NetworkID = idNetNope

	err := doc.Validate()

	var validationErr *builder.ValidationError

	if !errors.As(err, &validationErr) {
		t.Fatalf("Validate() = %T %v, want a *ValidationError", err, err)
	}

	if len(validationErr.Issues) < 3 {
		t.Errorf("Validate() = %d issues, want at least 3: %v", len(validationErr.Issues), validationErr.Issues)
	}
}

// TestValidateEdgesOfADeviceWithManyInterfaces checks that each edge finds its handle, an unknown
// handle is reported, and of two handles with one ID an edge names the first.
func TestValidateEdgesOfADeviceWithManyInterfaces(t *testing.T) {
	const count = 8

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
		t.Fatalf("FromConfig(wide) = %d handles and %d edges, want %d of each",
			len(device.Device.Interfaces), len(doc.Edges), count)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
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
	if want := "edges[" + strconv.Itoa(count-1) + "]: unknown connection point"; err == nil ||
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
		`" of device "wide" already has another connection, edges[0]`; err == nil || !strings.Contains(err.Error(), want) {
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

// validationCorpus is testdata/validation-corpus.json, which validate.contract.test.js
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
			// Delete removes the key at Path in place of setting it.
			Delete bool `json:"delete"`
		} `json:"set"`
		Error string `json:"error"`
		// Message is what the issue at Error says, when the case pins it.
		Message string `json:"message"`
		// Code is the code of the issue at Error, when the case pins it.
		Code builder.Code `json:"code"`
		// Issues is every issue of the document, with its code, in any
		// order, when the case pins that there are no others.
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

// setIn puts value at path in a decoded JSON document, or removes the key
// there when remove is set.
func setIn(t *testing.T, doc any, path []any, value any, remove bool) {
	t.Helper()

	for i, key := range path {
		last := i == len(path)-1

		switch container := doc.(type) {
		case map[string]any:
			switch {
			case last && remove:
				delete(container, key.(string)) //nolint:forcetypeassert // corpus keys are strings
			case last:
				container[key.(string)] = value //nolint:forcetypeassert // corpus keys are strings
			default:
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

	// validate.contract.test.js runs the same cases by name, so each name is one case. Registered cases are
	// counted, so the count holds whichever subtests -run picks.
	names := make(map[string]bool, len(corpus.Cases))
	registered := 0

	for _, test := range corpus.Cases {
		if names[test.Name] {
			t.Errorf("validation corpus has case %q twice, want each name once", test.Name)
		}

		names[test.Name] = true
		registered++

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

				setIn(t, doc, set.Path, value, set.Delete)
			}

			encoded, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("encoding: %v", err)
			}

			_, err = builder.Parse(encoded)

			switch {
			case test.Error == "" && err != nil:
				t.Errorf("Parse() = %v, want nil", err)
			case test.Error != "" && err == nil:
				t.Errorf("Parse() = nil, want an issue at %s", test.Error)
			case test.Error != "" && !refusedAt(err, test.Error, test.Message, test.Code):
				t.Errorf("Parse() = %q, want an issue at %s of code %q saying %q", err.Error(), test.Error, test.Code, test.Message)
			}

			if test.Issues == nil {
				return
			}

			var invalid *builder.ValidationError

			if !errors.As(err, &invalid) {
				t.Fatalf("Parse() = %v, want a *ValidationError with issues", err)
			}

			if diff := cmp.Diff(issueLines(test.Issues), issueLines(invalid.Issues)); diff != "" {
				t.Errorf("Parse() issues mismatch (-want +got):\n%s", diff)
			}
		})
	}

	if registered != len(corpus.Cases) {
		t.Errorf("TestValidationCorpus registered %d subtests, want one for each of the %d corpus cases",
			registered, len(corpus.Cases))
	}
}

// issueLines returns the issues as sorted lines of their code, path and
// message, to compare two lists of them whatever their order.
func issueLines(issues []builder.Issue) []string {
	lines := make([]string, 0, len(issues))

	for _, issue := range issues {
		lines = append(lines, string(issue.Code)+" "+issue.Path+": "+issue.Message)
	}

	slices.Sort(lines)

	return lines
}

// refusedAt reports whether err refuses the document at path: a validation issue there (with message
// and code when given), or a decoder refusal of a wrong type or unknown key there.
func refusedAt(err error, path, message string, code builder.Code) bool {
	var (
		invalid  *builder.ValidationError
		mistyped *json.UnmarshalTypeError
	)

	if errors.As(err, &invalid) {
		return slices.ContainsFunc(invalid.Issues, func(issue builder.Issue) bool {
			return issue.Path == path && (message == "" || issue.Message == message) && (code == "" || issue.Code == code)
		})
	}

	if message != "" || code != "" {
		return false
	}

	parent, key := path, path
	if i := strings.LastIndex(path, "."); i >= 0 {
		parent, key = path[:i], path[i+1:]
	}

	if !errors.As(err, &mistyped) {
		return strings.Contains(err.Error(), "unknown field "+strconv.Quote(key))
	}

	return mistyped.Field == path || mistyped.Field == parent
}

// TestValidateExtraPayloadsInKindOrder checks that the extra payloads of a
// node are reported in the order of the node kinds, each time.
func TestValidateExtraPayloadsInKindOrder(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	doc.Nodes = append(doc.Nodes, builder.Node{
		ID:       idNoteFree,
		Kind:     builder.NodeKindNote,
		Position: builder.Position{X: 900, Y: 100},
		Note:     &builder.Note{Text: "free floating"},
		Shape:    &builder.Shape{Shape: "rectangle"},
		Group:    &builder.Group{Title: "extra"},
		Switch:   &builder.Switch{NetworkID: idNetExp},
	})

	want := []string{
		`node of kind "note" must not carry a "switch" payload`,
		`node of kind "note" must not carry a "group" payload`,
		`node of kind "note" must not carry a "shape" payload`,
	}

	for range 20 {
		var validationErr *builder.ValidationError
		if err := doc.Validate(); !errors.As(err, &validationErr) {
			t.Fatalf("Validate() = %v, want a *ValidationError", err)
		}

		var got []string

		for _, issue := range validationErr.Issues {
			if issue.Code == builder.CodeNodePayloadExtra {
				got = append(got, issue.Message)
			}
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Validate() extra payload issues mismatch (-want +got):\n%s", diff)
		}
	}
}

// TestNameSpaceCorpus checks that the white space a name must not hold is
// the list of testdata/name-spaces.json, which the editor also checks.
func TestNameSpaceCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "name-spaces.json"))
	if err != nil {
		t.Fatalf("reading name-spaces.json: %v", err)
	}

	var corpus struct {
		CodePoints []string `json:"codePoints"`
	}

	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decoding name-spaces.json: %v", err)
	}

	var got []string

	for r := range rune(unicode.MaxRune + 1) {
		if builder.IsNameSpace(r) {
			got = append(got, fmt.Sprintf("U+%04X", r))
		}
	}

	if diff := cmp.Diff(corpus.CodePoints, got); diff != "" {
		t.Errorf("IsNameSpace() code points mismatch (-want +got):\n%s", diff)
	}
}
