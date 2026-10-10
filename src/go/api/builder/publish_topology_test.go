package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"phenix/api/config"
	"phenix/store"
	"phenix/types/builder"
)

// publishTestStore is the phenix store of a publishTest. It counts the
// configs written, and fails every config write while failWrite is set.
type publishTestStore struct {
	*hookTestStore

	failWrite error
	writes    int
}

func (s *publishTestStore) Create(c *store.Config) error {
	if s.failWrite != nil {
		return s.failWrite
	}

	s.writes++

	return s.hookTestStore.Create(c)
}

func (s *publishTestStore) Update(c *store.Config) error {
	if s.failWrite != nil {
		return s.failWrite
	}

	s.writes++

	return s.hookTestStore.Update(c)
}

// publishTest publishes documents as topologies in a phenix store of its own:
// configs in a BoltDB, where the config hooks run, and records in memory.
type publishTest struct {
	*hookTest

	configs *publishTestStore
}

func newPublishTest(t *testing.T) *publishTest {
	t.Helper()

	h := newHookTest(t)
	configs := &publishTestStore{hookTestStore: h.store, failWrite: nil, writes: 0}

	// newHookTest restores the store the test started with.
	store.DefaultStore = configs //nolint:reassign // the test's own store

	return &publishTest{hookTest: h, configs: configs}
}

// publish publishes the document as req asks, with the test actor unless
// req names one.
func (p *publishTest) publish(doc *builder.Document, req PublishTopologyRequest) (*TopologyPublication, error) {
	p.t.Helper()

	data, err := EncodeDocument(doc)
	if err != nil {
		p.t.Fatalf("EncodeDocument returned error: %v", err)
	}

	req.Document = data
	if req.Actor == "" {
		req.Actor = testActor
	}

	return p.service.PublishTopology(context.Background(), req)
}

// mustPublish is publish for a publication that must go through with the
// given outcome.
func (p *publishTest) mustPublish(
	doc *builder.Document, req PublishTopologyRequest, want TopologyOutcome,
) *TopologyPublication {
	p.t.Helper()

	publication, err := p.publish(doc, req)
	if err != nil {
		p.t.Fatalf("PublishTopology returned error: %s", fmtErr(err))
	}

	if publication.Outcome != want {
		p.t.Fatalf("outcome = %q, want %q", publication.Outcome, want)
	}

	return publication
}

// refused is publish for a publication that must be refused for the given
// reason, and returns the refusal.
func (p *publishTest) refused(doc *builder.Document, req PublishTopologyRequest, want PublishRefusal) *PublishRefusedError {
	p.t.Helper()

	publication, err := p.publish(doc, req)

	var refusal *PublishRefusedError
	if !errors.As(err, &refusal) || refusal.Refusal != want {
		p.t.Fatalf("PublishTopology = %+v, %s, want it refused as %q", publication, fmtErr(err), want)
	}

	return refusal
}

// stored returns the stored topology name, or nil when there is none.
func (p *publishTest) stored(name string) *store.Config {
	p.t.Helper()

	c, err := config.Get("topology/"+name, false)

	switch {
	case errors.Is(err, store.ErrNotExist):
		return nil
	case err != nil:
		p.t.Fatalf("getting topology %s returned error: %v", name, err)
	}

	return c
}

// reference returns the document reference the stored topology name holds.
func (p *publishTest) reference(name string) DocumentReference {
	p.t.Helper()

	c := p.stored(name)
	if c == nil {
		p.t.Fatalf("topology %s is not stored", name)
	}

	reference, err := DecodeReference(c.Metadata.Annotations[DocumentAnnotation])
	if err != nil {
		p.t.Fatalf("topology %s holds no valid document reference: %v", name, err)
	}

	return reference
}

// documents returns the IDs of the published documents stored, sorted.
func (p *publishTest) documents() []string {
	p.t.Helper()

	documents, err := p.service.ListPublishedDocuments(context.Background())
	if err != nil {
		p.t.Fatalf("ListPublishedDocuments returned error: %v", err)
	}

	ids := make([]string, 0, len(documents))
	for _, document := range documents {
		ids = append(ids, document.ID)
	}

	slices.Sort(ids)

	return ids
}

// plainTopology stores a topology no Builder document was published to,
// holding the spec doc publishes, and returns it as stored.
func (p *publishTest) plainTopology(name string, doc *builder.Document, annotations store.Annotations) *store.Config {
	p.t.Helper()

	c, _, err := doc.ToTopologyConfig(name)
	if err != nil {
		p.t.Fatalf("ToTopologyConfig returned error: %v", err)
	}

	c.Metadata.Annotations = annotations

	if _, err := config.Create(config.CreateFromConfig(c), config.CreateWithValidation()); err != nil {
		p.t.Fatalf("creating topology %s returned error: %v", name, err)
	}

	return p.stored(name)
}

// testInterface is an interface spec named name on the VLAN vlan, with an
// address of its own.
func testInterface(name, vlan, address string) map[string]any {
	return map[string]any{
		"name": name, "type": "ethernet", "proto": "static", "address": address, "mask": 24, "vlan": vlan,
	}
}

// testDevice is a device node phenix's topology schema accepts.
func testDevice(hostname string, interfaces ...any) builder.Node {
	return builder.Node{
		ID: builder.DeviceNodeID(hostname), Kind: builder.NodeKindDevice, Label: hostname,
		Device: &builder.Device{
			Hostname: hostname,
			Spec: map[string]any{
				"type":    "VirtualMachine",
				"general": map[string]any{"hostname": hostname},
				"hardware": map[string]any{
					"os_type": "linux", "drives": []any{map[string]any{"image": "ubuntu.qc2"}},
				},
				"network": map[string]any{"interfaces": interfaces},
			},
			Interfaces: []builder.InterfaceHandle{},
		},
	}
}

// publishableDocument returns a document named name with a device of each
// hostname, on the VLAN EXP at an address of its own.
func publishableDocument(name string, hostnames ...string) *builder.Document {
	doc := builder.NewDocument(name)

	for i, hostname := range hostnames {
		doc.Nodes = append(doc.Nodes, testDevice(hostname, testInterface("eth0", "EXP", fmt.Sprintf("10.0.0.%d", i+1))))
	}

	return doc
}

// generatedDocument returns the document the Builder generates from a stored
// topology, with one more device at an address of its own, as a diagram
// imported and then edited.
func generatedDocument(t *testing.T, topology *store.Config, hostname string) *builder.Document {
	t.Helper()

	doc, _, err := builder.FromConfig(*topology)
	if err != nil {
		t.Fatalf("FromConfig returned error: %v", err)
	}

	doc.Nodes = append(doc.Nodes, testDevice(hostname, testInterface("eth0", "EXP", fmt.Sprintf("10.0.9.%d", len(doc.Nodes)+1))))

	return doc
}

// hostnames returns the hostnames of the nodes of a topology config.
func hostnames(t *testing.T, c *store.Config) []string {
	t.Helper()

	nodes, _ := c.Spec["nodes"].([]any)
	names := make([]string, 0, len(nodes))

	for _, node := range nodes {
		spec, _ := node.(map[string]any)
		general, _ := spec["general"].(map[string]any)
		name, _ := general["hostname"].(string)

		names = append(names, name)
	}

	return names
}

// TestPublishTopologyCreates publishes a document no topology exists for:
// the topology is stored under the name the Publish dialog proposes, holds
// the document's projection and a reference of the digest and the ID, and
// the document is stored as it is, recorded under the actor and no draft.
func TestPublishTopologyCreates(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	doc := publishableDocument(" Lab topology (2) ", "alpha", "beta")
	doc.SetProvenance(builder.Provenance{
		CreatedBy: "carol", CreatedAt: "2026-01-02T03:04:05Z", UpdatedBy: "dave", UpdatedAt: "2026-02-03T04:05:06Z",
	})

	data, err := EncodeDocument(doc)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	publication := p.mustPublish(doc, PublishTopologyRequest{Actor: "operator"}, TopologyCreated)

	if publication.Name != "Lab-topology-2" || publication.Title != " Lab topology (2) " {
		t.Fatalf("name = %q, title = %q, want the proposed name and the document's own", publication.Name, publication.Title)
	}

	if publication.Digest != digestOf(data) || len(publication.Warnings) != 0 {
		t.Fatalf("digest = %q, warnings = %v, want the document's digest and no warning", publication.Digest, publication.Warnings)
	}

	topology := p.stored("Lab-topology-2")
	if topology == nil {
		t.Fatal("the topology was not stored")
	}

	if got := hostnames(t, topology); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Errorf("stored nodes = %v, want alpha and beta", got)
	}

	if holds, err := TopologyHoldsDocument(data, topology); err != nil || !holds {
		t.Errorf("TopologyHoldsDocument = %t, %v, want the stored topology to be the document's projection", holds, err)
	}

	want := DocumentReference{Digest: publication.Digest, ID: PublishedDocumentID("Lab-topology-2", publication.Digest), Path: ""}
	if got := p.reference("Lab-topology-2"); got != want || publication.Reference != want {
		t.Errorf("reference = %+v, reported %+v, want %+v", got, publication.Reference, want)
	}

	if len(topology.Metadata.Annotations) != 1 {
		t.Errorf("annotations = %v, want the document reference only", topology.Metadata.Annotations)
	}

	record, stored, err := p.service.GetPublishedDocumentData(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData returned error: %s", fmtErr(err))
	}

	// The document is not stamped: it is the file's, whoever publishes it.
	if string(stored) != string(data) {
		t.Errorf("the stored document is not the published one:\n%s", stored)
	}

	if record.CreatedBy != "operator" || record.DraftID != "" || record.SnapshotID != "" ||
		record.Target != "Lab-topology-2" || record.Kind != configKindTopology {
		t.Errorf("record = %+v, want it published by operator to Topology Lab-topology-2 from no draft", record)
	}

	if publication.Document == nil || publication.Document.ID != want.ID {
		t.Errorf("reported document = %+v, want %s", publication.Document, want.ID)
	}

	// A name given is used as it is.
	named := p.mustPublish(doc, PublishTopologyRequest{Name: "lab_2"}, TopologyCreated)
	if named.Name != "lab_2" || p.stored("lab_2") == nil {
		t.Errorf("published as %q, want lab_2 stored", named.Name)
	}

	if named.Document.ID == want.ID {
		t.Error("the same content published to another topology shares its document")
	}
}

// TestPublishTopologySameDocumentIsANoOp publishes one document twice: the
// second publication succeeds, writes no config, and needs no update flag.
// It stores the document again when the record was lost.
func TestPublishTopologySameDocumentIsANoOp(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)
	doc := publishableDocument("lab", "alpha")

	first := p.mustPublish(doc, PublishTopologyRequest{}, TopologyCreated)
	before := p.stored("lab")
	writes := p.configs.writes

	for _, update := range []bool{false, true} {
		again := p.mustPublish(doc, PublishTopologyRequest{Update: update}, TopologyUnchanged)

		if again.Reference != first.Reference || again.Document.ID != first.Document.ID {
			t.Errorf("update %t: reference = %+v, document %s, want those of the first publication",
				update, again.Reference, again.Document.ID)
		}
	}

	if p.configs.writes != writes {
		t.Errorf("config writes = %d, want none after the first publication's %d", p.configs.writes, writes)
	}

	if after := p.stored("lab"); !reflect.DeepEqual(after, before) {
		t.Errorf("the topology changed:\n%+v\nwant\n%+v", after, before)
	}

	if got := p.documents(); !slices.Equal(got, []string{first.Document.ID}) {
		t.Errorf("documents = %v, want only %s", got, first.Document.ID)
	}

	// A record that was lost is stored again.
	if _, err := p.service.DeleteTargetDocuments(context.Background(), "lab"); err != nil {
		t.Fatalf("DeleteTargetDocuments returned error: %v", err)
	}

	p.mustPublish(doc, PublishTopologyRequest{}, TopologyUnchanged)
	p.assertReadable(first.Document)

	// A topology that names the document but is no longer what it publishes
	// is not up to date.
	edited := p.stored("lab")
	edited.Spec = map[string]any{"nodes": []any{}}

	if err := store.Update(edited); err != nil {
		t.Fatalf("editing the topology returned error: %v", err)
	}

	p.refused(doc, PublishTopologyRequest{}, PublishRefusedExists)
}

// TestPublishTopologyNamesEveryBlocker refuses a document that cannot be
// published, naming everything that blocks it and writing nothing.
func TestPublishTopologyNamesEveryBlocker(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	blocked := builder.NewDocument("blocked")
	blocked.Nodes = append(blocked.Nodes,
		testDevice("aa", testInterface("eth0", "", "10.0.0.1"), testInterface("eth1", "EXP", "10.0.0.2")),
		testDevice("bb", testInterface("eth0", "EXP", "10.0.0.2"), testInterface("eth1", " ", "10.0.0.3")),
		testDevice("all", testInterface("eth0", "EXP", "10.0.0.4")),
	)

	refusal := p.refused(blocked, PublishTopologyRequest{}, PublishRefusedBlocked)

	for _, want := range []string{
		`interface "eth0" of device "aa" has no VLAN`,
		`interface "eth1" of device "bb" has no VLAN`,
		`IP address 10.0.0.2 on VLAN "EXP" is used by`,
		`all`,
	} {
		if !slices.ContainsFunc(refusal.Problems, func(problem string) bool { return strings.Contains(problem, want) }) {
			t.Errorf("problems %q do not name %q", refusal.Problems, want)
		}
	}

	if len(refusal.Problems) != 4 || !errors.Is(refusal, ErrInvalid) {
		t.Errorf("refusal = %s with %d problems, want 4 problems and ErrInvalid", fmtErr(refusal), len(refusal.Problems))
	}

	// Each problem is an issue with the code of its rule, at the device it is
	// about.
	codes := []builder.Code{
		builder.CodeInterfaceVLANMissing, builder.CodeInterfaceVLANMissing,
		builder.CodeInterfaceIPShared, builder.CodeNodeHostnameReserved,
	}

	if issues := refusal.Issues(); refusal.Code != builder.CodePublishBlocked || len(issues) != len(codes) {
		t.Errorf(
			"refusal code %s with issues %+v, want %s and one issue for each problem",
			refusal.Code,
			issues,
			builder.CodePublishBlocked,
		)
	} else {
		for i, issue := range issues {
			if issue.Code != codes[i] || issue.Message != refusal.Problems[i] || issue.NodeID == "" {
				t.Errorf("issue %d = %+v, want code %s saying %q at its device", i, issue, codes[i], refusal.Problems[i])
			}
		}
	}

	// Each problem is a line of its own in the text a caller shows.
	if lines := strings.Split(refusal.Error(), "\n"); len(lines) != 5 ||
		lines[0] != "the document cannot be published as topology blocked:" {
		t.Errorf("refusal text = %q, want a first line and a line for each problem", refusal.Error())
	}

	// What phenix's config validation refuses blocks too: an interface with
	// no vlan key, which is named as the interface it is.
	absent := builder.NewDocument("absent")
	absent.Nodes = append(absent.Nodes, testDevice("host", map[string]any{"name": "eth0"}))

	refusal = p.refused(absent, PublishTopologyRequest{}, PublishRefusedBlocked)
	if len(refusal.Problems) != 1 || !strings.Contains(refusal.Problems[0], `interface "eth0" of device "host" has no VLAN`) {
		t.Errorf("problems = %q, want the interface without a vlan key", refusal.Problems)
	}

	// Such a blocker does not hide the others: an address interfaces share
	// is named beside the interface with no vlan key, and beside a hostname
	// of one character, which the schema refuses too.
	for name, test := range map[string]struct {
		nodes []builder.Node
		want  []string
	}{
		"no vlan key": {
			nodes: []builder.Node{
				testDevice("aa", map[string]any{"name": "eth0"}, testInterface("eth1", "EXP", "10.0.0.1")),
				testDevice("bb", testInterface("eth0", "EXP", "10.0.0.1")),
			},
			want: []string{`interface "eth0" of device "aa" has no VLAN`, `IP address 10.0.0.1 on VLAN "EXP" is used by`},
		},
		"a hostname of one character": {
			nodes: []builder.Node{
				testDevice("a", testInterface("eth0", "EXP", "10.0.0.1")),
				testDevice("bb", testInterface("eth0", "EXP", "10.0.0.1")),
			},
			want: []string{`IP address 10.0.0.1 on VLAN "EXP" is used by`, `hostname 'a'`},
		},
	} {
		several := builder.NewDocument("several")
		several.Nodes = append(several.Nodes, test.nodes...)

		refusal = p.refused(several, PublishTopologyRequest{}, PublishRefusedBlocked)
		if len(refusal.Problems) != len(test.want) {
			t.Errorf("%s: problems = %q, want %d of them", name, refusal.Problems, len(test.want))

			continue
		}

		for i, want := range test.want {
			if !strings.Contains(refusal.Problems[i], want) {
				t.Errorf("%s: problem %d = %q, want it to name %q", name, i, refusal.Problems[i], want)
			}
		}
	}

	// And a spec the schema refuses for a reason of its own.
	invalid := builder.NewDocument("invalid")
	invalid.Nodes = append(invalid.Nodes, testDevice("host", testInterface("eth0", "EXP", "10.0.0.1")))
	invalid.Nodes[0].Device.Spec["type"] = "NotAType"

	refusal = p.refused(invalid, PublishTopologyRequest{}, PublishRefusedBlocked)
	if len(refusal.Problems) != 1 || !strings.Contains(refusal.Problems[0], "validating topology projection") ||
		refusal.Issues()[0].Code != builder.CodePublishTopologyInvalid {
		t.Errorf("problems = %q (issues %+v), want the schema's own error", refusal.Problems, refusal.Issues())
	}

	// A name that is not a config name, given or proposed.
	for _, name := range []string{"has space", "a/b", strings.Repeat("n", MaxTargetLength+1)} {
		refusal = p.refused(publishableDocument("ok", "host"), PublishTopologyRequest{Name: name}, PublishRefusedName)
		if !errors.Is(refusal, ErrInvalid) {
			t.Errorf("name %q: refusal = %s, want ErrInvalid", name, fmtErr(refusal))
		}
	}

	p.refused(publishableDocument(strings.Repeat("n", MaxTargetLength+1), "host"), PublishTopologyRequest{}, PublishRefusedName)

	// An actor is required, as storing a document requires one, on a dry
	// run too.
	for _, dryRun := range []bool{false, true} {
		if _, err := p.service.PublishTopology(context.Background(), PublishTopologyRequest{
			Document: testDocument(t, "no-actor", 0), DryRun: dryRun,
		}); !errors.Is(err, ErrInvalid) {
			t.Errorf("no actor, dry run %t: error = %s, want ErrInvalid", dryRun, fmtErr(err))
		}
	}

	// And a document that is one.
	if _, err := p.service.PublishTopology(context.Background(), PublishTopologyRequest{
		Document: []byte(`{"kind": "Topology"}`), Actor: testActor,
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("not a document: error = %s, want ErrInvalid", fmtErr(err))
	}

	if documents := p.documents(); len(documents) != 0 || p.configs.writes != 0 {
		t.Errorf("refused publications stored documents %v and wrote %d configs", documents, p.configs.writes)
	}
}

// TestPublishTopologyChecksIncludes refuses a topology that defines a
// hostname one of its stored included topologies defines too, and warns of an
// included topology that is not stored.
func TestPublishTopologyChecksIncludes(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	p.plainTopology("services", publishableDocument("services", "dns", "mail"), nil)

	including := func(hosts []string, includes ...string) *builder.Document {
		doc := publishableDocument("site", hosts...)
		doc.Source = &builder.Source{Kind: builder.SourceKindManual, Name: "site", IncludeTopologies: includes}

		return doc
	}

	writes := p.configs.writes

	refusal := p.refused(including([]string{"web", "DNS", "mail"}, "services"), PublishTopologyRequest{}, PublishRefusedIncludes)

	want := []string{
		"node dns is defined both here and in the included topology services",
		"node mail is defined both here and in the included topology services",
	}
	if !slices.Equal(refusal.Problems, want) || !errors.Is(refusal, ErrConflict) {
		t.Errorf("refusal = %s with problems %q, want %q and ErrConflict", fmtErr(refusal), refusal.Problems, want)
	}

	if p.stored("site") != nil || len(p.documents()) != 0 || p.configs.writes != writes {
		t.Error("a refused publication wrote something")
	}

	// An included topology that is not stored, or is named by a path, is not
	// read: the topology is published with a warning for each.
	publication := p.mustPublish(
		including([]string{"web"}, "services", "missing", "/phenix/topologies/other.yaml"),
		PublishTopologyRequest{}, TopologyCreated,
	)

	warnings := builder.IssueMessages(publication.Warnings)
	if len(warnings) != 2 ||
		!strings.Contains(warnings[0], "Included topology missing was not checked") ||
		!strings.Contains(warnings[0], "no stored topology has that name") ||
		!strings.Contains(warnings[1], "/phenix/topologies/other.yaml") ||
		!strings.Contains(warnings[1], "not from files") ||
		publication.Warnings[0].Code != builder.CodePublishIncludeUnchecked ||
		publication.Warnings[1].Code != builder.CodePublishIncludeUnchecked {
		t.Errorf("warnings = %+v, want one for each included topology that was not read", publication.Warnings)
	}

	stored, _ := p.stored("site").Spec["includeTopologies"].([]any)
	if len(stored) != 3 {
		t.Errorf("stored includeTopologies = %v, want the three the document names", stored)
	}
}

// legacyTestDiagram is a diagram of the legacy Builder that draws the node
// alpha, with the settings of a node phenix accepts.
func legacyTestDiagram(t *testing.T) string {
	t.Helper()

	settings := maps.Clone(testDevice("alpha", testInterface("eth0", "EXP", "10.0.0.1")).Device.Spec)
	settings["device"] = "server"
	settings["schema"] = "kvm"

	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("encoding the settings: %v", err)
	}

	return `<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/>` +
		`<object label="alpha" schemaVars="` + html.EscapeString(string(encoded)) + `" id="2">` +
		`<mxCell style="image;html=1;image=/server_grey_vm.png" vertex="1" parent="1">` +
		`<mxGeometry x="200" y="100" width="80" height="80" as="geometry"/></mxCell></object>` +
		`</root></mxGraphModel>`
}

// TestPublishTopologyReplacesLegacyDiagram updates a topology the legacy
// Builder drew as it updates any topology no Builder document was published
// to: only when asked, and only from a document made from the topology as it
// is now. The update removes the legacy diagram, says so, and keeps every
// other annotation.
func TestPublishTopologyReplacesLegacyDiagram(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	before := p.plainTopology("legacy", publishableDocument("legacy", "alpha"), store.Annotations{
		builder.LegacyXMLAnnotation: legacyTestDiagram(t), "owner": "range-team",
	})

	// The document the Builder converts the topology into, then edited.
	converted, _, err := builder.FromLegacyTopology(*before)
	if err != nil {
		t.Fatalf("FromLegacyTopology returned error: %v", err)
	}

	if alpha := converted.FindDevice("alpha"); alpha == nil || alpha.Position != (builder.Position{X: 400, Y: 240}) {
		t.Fatalf("alpha = %+v, want it where the legacy diagram has it", alpha)
	}

	converted.Nodes = append(converted.Nodes, testDevice("beta", testInterface("eth0", "EXP", "10.0.9.2")))

	untouched := func(step string) {
		t.Helper()

		if after := p.stored("legacy"); !reflect.DeepEqual(after, before) || len(p.documents()) != 0 {
			t.Fatalf("%s: the legacy topology changed, or a document was stored: %+v", step, after)
		}
	}

	// Never without the flag.
	refusal := p.refused(converted, PublishTopologyRequest{Name: "legacy"}, PublishRefusedExists)
	if refusal.Message != "topology legacy already exists" {
		t.Errorf("refusal = %s, want it to say the topology exists", fmtErr(refusal))
	}

	untouched("without --update")

	// Never from a document that was not made from the topology: one drawn
	// by hand, one generated from another topology, or one uploaded as a
	// bare diagram.
	other := generatedDocument(t, p.plainTopology("other", publishableDocument("other", "alpha"), nil), "beta")

	diagram, err := builder.DecodeLegacy([]byte(legacyTestDiagram(t)))
	if err != nil {
		t.Fatalf("DecodeLegacy returned error: %v", err)
	}

	bare, _, err := builder.FromLegacy(diagram, "legacy")
	if err != nil {
		t.Fatalf("FromLegacy returned error: %v", err)
	}

	for name, doc := range map[string]*builder.Document{
		"a hand-drawn document":                publishableDocument("legacy", "alpha", "beta"),
		"a document of another topology":       other,
		"a diagram that came with no topology": bare,
	} {
		refusal := p.refused(doc, PublishTopologyRequest{Name: "legacy", Update: true}, PublishRefusedChanged)
		if !strings.Contains(refusal.Message, "was not made from the topology as it is now") {
			t.Errorf("%s: refusal = %s, want it to say what the document was not made from", name, fmtErr(refusal))
		}

		untouched(name)
	}

	// A dry run says what the update would do, and writes nothing.
	planned := p.mustPublish(converted, PublishTopologyRequest{Name: "legacy", Update: true, DryRun: true}, TopologyUpdated)

	replaced := builder.NewIssue(builder.CodePublishLegacyReplaced, "",
		"The legacy Builder diagram of topology legacy was replaced by this diagram.")
	if !slices.Contains(planned.Warnings, replaced) {
		t.Errorf("dry run warnings = %q, want %q", planned.Warnings, replaced)
	}

	untouched("a dry run")

	// The update: the legacy diagram goes, the document reference comes, and
	// the other annotation stays.
	publication := p.mustPublish(converted, PublishTopologyRequest{Name: "legacy", Update: true}, TopologyUpdated)

	count := 0

	for _, warning := range publication.Warnings {
		if warning == replaced {
			count++
		}
	}

	if count != 1 {
		t.Errorf("warnings = %q, want %q once", publication.Warnings, replaced)
	}

	after := p.stored("legacy")
	if _, legacy := after.Metadata.Annotations[builder.LegacyXMLAnnotation]; legacy {
		t.Error("the published topology still carries the legacy diagram")
	}

	if got := p.reference("legacy"); got != publication.Reference || after.Metadata.Annotations["owner"] != "range-team" {
		t.Errorf("annotations = %v, want the document reference %+v and the owner", after.Metadata.Annotations, publication.Reference)
	}

	if got := hostnames(t, after); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Errorf("nodes = %v, want alpha and beta", got)
	}

	// It is a Builder topology from then on: publishing the same document
	// again changes nothing and warns of no legacy diagram.
	again := p.mustPublish(converted, PublishTopologyRequest{Name: "legacy", Update: true}, TopologyUnchanged)
	if slices.Contains(again.Warnings, replaced) {
		t.Errorf("warnings of an unchanged topology = %q, want none of a legacy diagram", again.Warnings)
	}
}

// TestPublishTopologyKeepsChangedLegacyTopology refuses to replace a
// topology the legacy Builder drew with a document made from it before
// someone changed it: its nodes, or its legacy diagram, which the update
// would remove unseen.
func TestPublishTopologyKeepsChangedLegacyTopology(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	for name, change := range map[string]func(stored *store.Config){
		"its nodes": func(stored *store.Config) {
			stored.Spec["nodes"] = append(stored.Spec["nodes"].([]any), testDevice("manual").Device.Spec)
		},
		"its legacy diagram": func(stored *store.Config) {
			stored.Metadata.Annotations[builder.LegacyXMLAnnotation] = strings.Replace(
				legacyTestDiagram(t), `x="200"`, `x="640"`, 1)
		},
		"its legacy diagram, removed": func(stored *store.Config) {
			delete(stored.Metadata.Annotations, builder.LegacyXMLAnnotation)
		},
	} {
		t.Run(name, func(t *testing.T) { //nolint:paralleltest // replaces the phenix store
			p := newPublishTest(t)

			stored := p.plainTopology("legacy", publishableDocument("legacy", "alpha"), store.Annotations{
				builder.LegacyXMLAnnotation: legacyTestDiagram(t),
			})

			converted, _, err := builder.FromLegacyTopology(*stored)
			if err != nil {
				t.Fatalf("FromLegacyTopology returned error: %v", err)
			}

			change(stored)

			if err := store.Update(stored); err != nil {
				t.Fatalf("editing the topology returned error: %v", err)
			}

			edited := p.stored("legacy")

			p.refused(converted, PublishTopologyRequest{Name: "legacy", Update: true}, PublishRefusedChanged)

			if after := p.stored("legacy"); !reflect.DeepEqual(after, edited) || len(p.documents()) != 0 {
				t.Errorf("the changed legacy topology was written, or a document was stored: %+v", after)
			}
		})
	}
}

// TestPublishTopologyRemovesUnreadableLegacyDiagram updates a topology whose
// legacy diagram cannot be read, from the document made from it: nothing of
// the diagram is in the document, so the warning says it was removed, not
// replaced.
func TestPublishTopologyRemovesUnreadableLegacyDiagram(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	stored := p.plainTopology("legacy", publishableDocument("legacy", "alpha"), store.Annotations{
		builder.LegacyXMLAnnotation: "PG14R3JhcGhNb2RlbC8+", "owner": "range-team",
	})

	converted, warnings, err := builder.FromLegacyTopology(*stored)
	if err != nil {
		t.Fatalf("FromLegacyTopology returned error: %v", err)
	}

	if len(warnings) == 0 || !strings.HasPrefix(warnings[0], "The legacy diagram of topology legacy could not be read (") {
		t.Fatalf("warnings = %q, want the diagram reported as not read", warnings)
	}

	publication := p.mustPublish(converted, PublishTopologyRequest{Name: "legacy", Update: true}, TopologyUpdated)

	removed := builder.NewIssue(builder.CodePublishLegacyRemoved, "",
		"The legacy Builder diagram of topology legacy could not be read and was removed.")
	if !slices.Contains(publication.Warnings, removed) ||
		slices.ContainsFunc(
			publication.Warnings,
			func(warning builder.Issue) bool { return strings.Contains(warning.Message, "replaced") },
		) {
		t.Errorf("warnings = %q, want %q and nothing of a replaced diagram", publication.Warnings, removed)
	}

	annotations := p.stored("legacy").Metadata.Annotations
	if _, legacy := annotations[builder.LegacyXMLAnnotation]; legacy || annotations["owner"] != "range-team" ||
		annotations[DocumentAnnotation] == "" {
		t.Errorf("annotations = %v, want the diagram gone, the owner kept and the document reference", annotations)
	}
}

// TestPublishTopologyUpdate replaces an existing topology only when asked,
// and only when the update takes nothing from it: it is unchanged since its
// document was published, or the document was generated from it as it is.
func TestPublishTopologyUpdate(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	first := publishableDocument("lab", "alpha")
	second := publishableDocument("lab", "alpha", "beta")
	third := publishableDocument("lab", "alpha", "beta", "gamma")

	created := p.mustPublish(first, PublishTopologyRequest{}, TopologyCreated)

	// An annotation of the stored topology that is not the Builder's, and a
	// status.
	annotated := p.stored("lab")
	annotated.Metadata.Annotations["owner"] = "range-team"
	annotated.Status = map[string]any{"checked": "yes"}

	if err := store.Update(annotated); err != nil {
		t.Fatalf("annotating the topology returned error: %v", err)
	}

	// Without the flag an existing topology is never replaced.
	refusal := p.refused(second, PublishTopologyRequest{}, PublishRefusedExists)
	if refusal.Message != "topology lab already exists" || !errors.Is(refusal, ErrConflict) {
		t.Errorf("refusal = %s, want it to say the topology exists, as ErrConflict", fmtErr(refusal))
	}

	if got := hostnames(t, p.stored("lab")); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("nodes = %v after a refused publication, want alpha", got)
	}

	// Unchanged since it was published: the update goes through, keeps the
	// stored metadata, and names the new document.
	p.now = p.now.Add(2 * OrphanGracePeriod)

	updated := p.mustPublish(second, PublishTopologyRequest{Update: true}, TopologyUpdated)

	topology := p.stored("lab")
	if got := hostnames(t, topology); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Errorf("nodes = %v, want alpha and beta", got)
	}

	if topology.Metadata.Annotations["owner"] != "range-team" || topology.Metadata.Created != annotated.Metadata.Created ||
		topology.Status["checked"] != "yes" {
		t.Errorf("metadata = %+v, status = %v, want the stored annotation, creation time and status kept",
			topology.Metadata, topology.Status)
	}

	if got := p.reference("lab"); got != updated.Reference || got.ID == created.Reference.ID {
		t.Errorf("reference = %+v, want the second document's %+v", got, updated.Reference)
	}

	// The document the topology no longer names is removed once it is old
	// enough to belong to no publication in flight.
	if got := p.documents(); !slices.Equal(got, []string{updated.Document.ID}) {
		t.Errorf("documents = %v, want only %s", got, updated.Document.ID)
	}

	// Changed by hand since: an update would discard the change.
	edited := p.stored("lab")
	edited.Spec["nodes"] = append(edited.Spec["nodes"].([]any), testDevice("manual").Device.Spec)

	if err := store.Update(edited); err != nil {
		t.Fatalf("editing the topology returned error: %v", err)
	}

	writes := p.configs.writes

	refusal = p.refused(third, PublishTopologyRequest{Update: true}, PublishRefusedChanged)
	if !strings.Contains(refusal.Message, "was changed after it was published") || !errors.Is(refusal, ErrConflict) {
		t.Errorf("refusal = %s, want it to say the topology changed, as ErrConflict", fmtErr(refusal))
	}

	if got := hostnames(t, p.stored("lab")); !slices.Equal(got, []string{"alpha", "beta", "manual"}) || p.configs.writes != writes {
		t.Errorf("nodes = %v after a refused update, want the hand edit kept and no write", got)
	}

	// A document generated from the topology as it is now replaces it: its
	// author saw the change.
	generated := generatedDocument(t, p.stored("lab"), "delta")

	p.mustPublish(generated, PublishTopologyRequest{Name: "lab", Update: true}, TopologyUpdated)

	if got := hostnames(t, p.stored("lab")); !slices.Equal(got, []string{"alpha", "beta", "manual", "delta"}) {
		t.Errorf("nodes = %v, want the generated document's", got)
	}
}

// TestPublishTopologyUpdatesFromItsSource updates a topology no Builder
// document was published to only from a document generated from it as it is
// now.
func TestPublishTopologyUpdatesFromItsSource(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	plain := p.plainTopology("plain", publishableDocument("plain", "alpha"), store.Annotations{"owner": "range-team"})
	generated := generatedDocument(t, plain, "beta")

	// Not the topology's document: one drawn by hand, or generated from
	// another topology or from this one as it was.
	manual := publishableDocument("plain", "alpha", "beta")

	other := generatedDocument(t, plain, "beta")
	other.Source.Name = "another"

	stale := generatedDocument(t, plain, "beta")
	stale.Source.Digest = "sha256:" + strings.Repeat("0", 64)

	for name, doc := range map[string]*builder.Document{"drawn by hand": manual, "of another topology": other, "stale": stale} {
		refusal := p.refused(doc, PublishTopologyRequest{Name: "plain", Update: true}, PublishRefusedChanged)
		if !strings.Contains(refusal.Message, "was not made from the topology as it is now") {
			t.Errorf("%s: message = %q", name, refusal.Message)
		}
	}

	p.refused(generated, PublishTopologyRequest{Name: "plain"}, PublishRefusedExists)

	if got := p.stored("plain"); !reflect.DeepEqual(got, plain) || len(p.documents()) != 0 {
		t.Fatalf("refused publications changed the topology or stored a document: %+v", got)
	}

	publication := p.mustPublish(generated, PublishTopologyRequest{Name: "plain", Update: true}, TopologyUpdated)

	topology := p.stored("plain")
	if got := hostnames(t, topology); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Errorf("nodes = %v, want alpha and beta", got)
	}

	if topology.Metadata.Annotations["owner"] != "range-team" || p.reference("plain") != publication.Reference {
		t.Errorf("annotations = %v, want the stored one kept and the reference %+v", topology.Metadata.Annotations, publication.Reference)
	}

	// A reference that names a document no longer stored vouches for
	// nothing, so the same rule applies.
	if _, err := p.service.DeleteTargetDocuments(context.Background(), "plain"); err != nil {
		t.Fatalf("DeleteTargetDocuments returned error: %v", err)
	}

	p.refused(publishableDocument("plain", "omega"), PublishTopologyRequest{Update: true}, PublishRefusedChanged)
	p.mustPublish(generatedDocument(t, p.stored("plain"), "gamma"), PublishTopologyRequest{Name: "plain", Update: true}, TopologyUpdated)
}

// TestPublishTopologyKeepsDocumentWhenConfigWriteFails fails the config
// write of a create and of an update: the publication fails, the document it
// stored stays for the next publication to use, and the topology is as it
// was.
func TestPublishTopologyKeepsDocumentWhenConfigWriteFails(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	first := publishableDocument("lab", "alpha")
	second := publishableDocument("lab", "alpha", "beta")
	failure := errors.New("injected config write failure")

	p.configs.failWrite = failure

	if _, err := p.publish(first, PublishTopologyRequest{}); !errors.Is(err, failure) {
		t.Fatalf("create: error = %s, want the config write failure", fmtErr(err))
	}

	var refusal *PublishRefusedError
	if _, err := p.publish(first, PublishTopologyRequest{}); errors.As(err, &refusal) {
		t.Fatalf("a failed config write is reported as the refusal %s", fmtErr(err))
	}

	kept := p.documents()
	if len(kept) != 1 || p.stored("lab") != nil {
		t.Fatalf("documents = %v, topology stored = %t, want the document kept and no topology", kept, p.stored("lab") != nil)
	}

	p.configs.failWrite = nil

	created := p.mustPublish(first, PublishTopologyRequest{}, TopologyCreated)
	if created.Document.ID != kept[0] {
		t.Errorf("the publication stored %s, want it to use the document %s kept", created.Document.ID, kept[0])
	}

	p.configs.failWrite = failure

	if _, err := p.publish(second, PublishTopologyRequest{Update: true}); !errors.Is(err, failure) {
		t.Fatalf("update: error = %s, want the config write failure", fmtErr(err))
	}

	if got := p.reference("lab"); got != created.Reference || len(p.documents()) != 2 {
		t.Errorf("reference = %+v, documents = %v, want the first reference and both documents", got, p.documents())
	}

	// The topology is still what its document publishes, so the update goes
	// through once the store takes it.
	p.configs.failWrite = nil

	p.mustPublish(second, PublishTopologyRequest{Update: true}, TopologyUpdated)
}

// TestPublishTopologyRecordsPath writes the path of the published file
// beside the digest and the ID only when asked, keeps a path the topology
// already names, and replaces it only when asked again.
func TestPublishTopologyRecordsPath(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	const (
		file  = "/phenix/topologies/lab/lab.builder.yaml"
		moved = "/phenix/topologies/lab/moved.json"
	)

	first := publishableDocument("lab", "alpha")
	second := publishableDocument("lab", "alpha", "beta")

	// Without the request, the path of the file is not recorded.
	p.mustPublish(first, PublishTopologyRequest{Name: "unrecorded", Path: file}, TopologyCreated)

	if got := p.reference("unrecorded"); got.Path != "" {
		t.Errorf("reference = %+v, want no path", got)
	}

	created := p.mustPublish(first, PublishTopologyRequest{Path: file, RecordPath: true}, TopologyCreated)

	want := DocumentReference{Digest: created.Digest, ID: PublishedDocumentID("lab", created.Digest), Path: file}
	if got := p.reference("lab"); got != want || len(created.Warnings) != 0 {
		t.Fatalf("reference = %+v, warnings %q, want the three keys %+v and no warning", got, created.Warnings, want)
	}

	// The annotation is the map of the three keys in every encoding.
	if got, _ := p.stored("lab").StoredJSON(); !strings.Contains(string(got), `\"path\":\"`+file+`\"`) {
		t.Errorf("stored config = %s, want it to hold the path", got)
	}

	// An update keeps the path, and says nothing while the file published is
	// the one named.
	updated := p.mustPublish(second, PublishTopologyRequest{Path: file, Update: true}, TopologyUpdated)
	if got := p.reference("lab"); got.Path != file || got.Digest != updated.Digest || len(updated.Warnings) != 0 {
		t.Errorf("reference = %+v, warnings %q, want the path kept beside the new digest, and no warning", got, updated.Warnings)
	}

	// Published from another file, the path is still kept, with a warning
	// that the file it names was not changed.
	third := publishableDocument("lab", "alpha", "beta", "gamma")

	elsewhere := p.mustPublish(third, PublishTopologyRequest{Path: moved, Update: true}, TopologyUpdated)
	if got := p.reference("lab"); got.Path != file || len(elsewhere.Warnings) != 1 ||
		!strings.Contains(elsewhere.Warnings[0].Message, "names the Builder file "+file) ||
		elsewhere.Warnings[0].Code != builder.CodePublishFileUnchanged {
		t.Errorf("reference = %+v, warnings %q, want the path kept and a warning that names it", got, elsewhere.Warnings)
	}

	// Recording the other path of the same document changes the topology's
	// annotation, so it needs the update flag, and nothing else changes.
	writes := p.configs.writes

	p.refused(third, PublishTopologyRequest{Path: moved, RecordPath: true}, PublishRefusedExists)

	replaced := p.mustPublish(third, PublishTopologyRequest{Path: moved, RecordPath: true, Update: true}, TopologyUpdated)
	if got := p.reference("lab"); got.Path != moved || got.ID != elsewhere.Reference.ID || len(replaced.Warnings) != 0 ||
		p.configs.writes != writes+1 {
		t.Errorf("reference = %+v after %d writes, want the path replaced in one write", got, p.configs.writes-writes)
	}

	p.mustPublish(third, PublishTopologyRequest{Path: moved, RecordPath: true}, TopologyUnchanged)

	// A path a reference may not hold is refused before anything is
	// written.
	for _, path := range []string{"", "lab.yaml", "/phenix/lab.txt", "/phenix/../lab.yaml", "/phenix/lab\n.yaml"} {
		refusal := p.refused(first, PublishTopologyRequest{Name: "refused", Path: path, RecordPath: true}, PublishRefusedPath)
		if !errors.Is(refusal, ErrInvalid) {
			t.Errorf("path %q: refusal = %s, want ErrInvalid", path, fmtErr(refusal))
		}
	}

	if p.stored("refused") != nil {
		t.Error("a publication with a refused path stored the topology")
	}
}

// TestPublishTopologyDryRun makes every check and writes nothing: it reports
// what a publication would do, and refuses what a publication would refuse.
func TestPublishTopologyDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	first := publishableDocument("lab", "alpha")
	second := publishableDocument("lab", "alpha", "beta")

	dry := p.mustPublish(first, PublishTopologyRequest{DryRun: true}, TopologyCreated)

	if dry.Document != nil || p.stored("lab") != nil || len(p.documents()) != 0 || p.configs.writes != 0 {
		t.Fatal("a dry run stored something")
	}

	if dry.Reference.ID != PublishedDocumentID("lab", dry.Digest) || !slices.Equal(hostnames(t, dry.Config), []string{"alpha"}) {
		t.Errorf("dry run = %+v, want the reference and the config a publication would write", dry)
	}

	created := p.mustPublish(first, PublishTopologyRequest{}, TopologyCreated)
	if created.Reference != dry.Reference {
		t.Errorf("published reference = %+v, want the one the dry run reported, %+v", created.Reference, dry.Reference)
	}

	before, documents, writes := p.stored("lab"), p.documents(), p.configs.writes

	p.mustPublish(first, PublishTopologyRequest{DryRun: true}, TopologyUnchanged)
	p.refused(second, PublishTopologyRequest{DryRun: true}, PublishRefusedExists)

	would := p.mustPublish(second, PublishTopologyRequest{DryRun: true, Update: true}, TopologyUpdated)
	if !slices.Equal(hostnames(t, would.Config), []string{"alpha", "beta"}) {
		t.Errorf("dry run config holds %v, want what the update would store", hostnames(t, would.Config))
	}

	blocked := builder.NewDocument("blocked")
	blocked.Nodes = append(blocked.Nodes, testDevice("host", testInterface("eth0", "", "10.0.0.1")))

	p.refused(blocked, PublishTopologyRequest{DryRun: true}, PublishRefusedBlocked)

	if after := p.stored("lab"); !reflect.DeepEqual(after, before) || !slices.Equal(p.documents(), documents) ||
		p.configs.writes != writes {
		t.Error("a dry run changed the topology, stored a document or wrote a config")
	}
}

// TestPublishTopologyWarnsOfWhatIsNotPublished publishes documents with a
// scenario, which a topology publication leaves alone, and VLAN aliases,
// which a topology has no place for.
func TestPublishTopologyWarnsOfWhatIsNotPublished(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	read := func(elements ...string) []byte {
		data, err := os.ReadFile(filepath.Join(elements...))
		if err != nil {
			t.Fatalf("reading a document: %v", err)
		}

		return data
	}

	publish := func(data []byte, name string) *TopologyPublication {
		file, err := ParseDocumentText(data)
		if err != nil {
			t.Fatalf("ParseDocumentText returned error: %s", fmtErr(err))
		}

		publication, err := p.service.PublishTopology(context.Background(), PublishTopologyRequest{
			Document: file.Data, Name: name, Actor: testActor,
		})
		if err != nil {
			t.Fatalf("PublishTopology returned error: %s", fmtErr(err))
		}

		return publication
	}

	examples := filepath.Join("..", "..", "..", "..", "docs", "content", "builder", "examples")

	// The docs example with a scenario, and an included topology that is not
	// stored here.
	riverside := publish(read(examples, "riverside-water.builder.json"), "")
	if riverside.Name != "Riverside-Water" ||
		!slices.Contains(riverside.Warnings, builder.NewIssue(builder.CodePublishScenarioUnchanged, "",
			"The document's scenario is not changed: only the topology is published.")) ||
		!slices.ContainsFunc(riverside.Warnings, func(warning builder.Issue) bool {
			return strings.HasPrefix(warning.Message, "Included topology corp-services was not checked")
		}) {
		t.Errorf("published as %q with warnings %q, want the scenario and the include named", riverside.Name, riverside.Warnings)
	}

	if scenarios, err := config.List("scenario"); err != nil || len(scenarios) != 0 {
		t.Errorf("scenarios = %v, %v, want none created", scenarios, err)
	}

	aliased := publish(read("..", "..", "types", "builder", "testdata", "strict-document.json"), "aliased")
	if !slices.Contains(aliased.Warnings, builder.NewIssue(builder.CodePublishAliasUnpublished, "",
		"The document's VLAN alias is not published: a topology holds none.")) {
		t.Errorf("warnings = %q, want the VLAN alias named", aliased.Warnings)
	}

	several := publishableDocument("several", "alpha")
	for i, name := range []string{"EXP", "MGMT"} {
		alias := 100 + i
		several.Networks = append(several.Networks, builder.Network{ID: builder.NetworkID(name), Name: name, Alias: &alias})
	}

	publication := p.mustPublish(several, PublishTopologyRequest{}, TopologyCreated)
	if !slices.Contains(builder.IssueMessages(publication.Warnings),
		"The document's 2 VLAN aliases are not published: a topology holds none.") {
		t.Errorf("warnings = %q, want both VLAN aliases counted", publication.Warnings)
	}
}

// TestPublishTopologyWarnsWhenSupersededDocumentsStay publishes although the
// document the topology no longer names cannot be removed whole: that is a
// warning, and the topology is published.
func TestPublishTopologyWarnsWhenSupersededDocumentsStay(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	first := publishableDocument("lab", "alpha")
	second := publishableDocument("lab", "alpha", "beta")

	created := p.mustPublish(first, PublishTopologyRequest{}, TopologyCreated)

	p.now = p.now.Add(2 * OrphanGracePeriod)
	p.store.FailPrefixDelete = func(string, string) error { return errors.New("injected delete failure") }

	updated := p.mustPublish(second, PublishTopologyRequest{Update: true}, TopologyUpdated)

	if len(updated.Warnings) != 1 || !strings.Contains(updated.Warnings[0].Message, "could not be removed") ||
		updated.Warnings[0].Code != builder.CodePublishCleanupFailed {
		t.Errorf("warnings = %q, want one for the documents that stay", updated.Warnings)
	}

	if got := p.reference("lab"); got != updated.Reference || got == created.Reference {
		t.Errorf("reference = %+v, want the update's %+v", got, updated.Reference)
	}
}

// TestDocumentReferencePublishes tells the references that name the document
// a publication of a digest stores for a topology.
func TestDocumentReferencePublishes(t *testing.T) {
	t.Parallel()

	const (
		digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		other  = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	)

	id := PublishedDocumentID("lab", digest)

	for name, test := range map[string]struct {
		reference DocumentReference
		want      bool
	}{
		"digest and id":           {DocumentReference{Digest: digest, ID: id}, true},
		"digest only":             {DocumentReference{Digest: digest}, true},
		"id only":                 {DocumentReference{ID: id}, true},
		"with a path":             {DocumentReference{Digest: digest, ID: id, Path: "/phenix/lab.yaml"}, true},
		"another digest":          {DocumentReference{Digest: other}, false},
		"another digest, this id": {DocumentReference{Digest: other, ID: id}, false},
		"another topology's id":   {DocumentReference{Digest: digest, ID: PublishedDocumentID("other", digest)}, false},
		"a path only":             {DocumentReference{Path: "/phenix/lab.yaml"}, false},
		"nothing":                 {DocumentReference{}, false},
		"this digest, another id": {DocumentReference{Digest: digest, ID: PublishedDocumentID("lab", other)}, false},
	} {
		if got := test.reference.Publishes("lab", digest); got != test.want {
			t.Errorf("%s: Publishes = %t, want %t", name, got, test.want)
		}
	}
}

// TestPublishTopologyHonorsContext publishes nothing once its context is
// done.
func TestPublishTopologyHonorsContext(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.service.PublishTopology(ctx, PublishTopologyRequest{
		Document: testDocument(t, "cancelled", 0), Actor: testActor,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %s, want context.Canceled", fmtErr(err))
	}

	if p.stored("cancelled") != nil || len(p.documents()) != 0 {
		t.Error("a cancelled publication wrote something")
	}
}

// publishedAt is when the record of a published document was last stored.
func publishedAt(t *testing.T, p *publishTest, id string) time.Time {
	t.Helper()

	document, err := p.service.GetPublishedDocument(context.Background(), id)
	if err != nil {
		t.Fatalf("GetPublishedDocument returned error: %v", err)
	}

	return document.lastPublished()
}

// TestPublishTopologyAgainKeepsTheDocumentFromCleanup asserts that
// publishing a document again dates its record now, as publishing a draft
// again does, so a cleanup that runs before the config is written leaves it.
func TestPublishTopologyAgainKeepsTheDocumentFromCleanup(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	p := newPublishTest(t)
	doc := publishableDocument("lab", "alpha")

	created := p.mustPublish(doc, PublishTopologyRequest{}, TopologyCreated)
	first := publishedAt(t, p, created.Document.ID)

	p.now = p.now.Add(2 * OrphanGracePeriod)

	p.mustPublish(doc, PublishTopologyRequest{}, TopologyUnchanged)

	if again := publishedAt(t, p, created.Document.ID); !again.After(first) {
		t.Errorf("record dated %s after publishing again, want it later than %s", again, first)
	}

	if !maps.Equal(p.stored("lab").Metadata.Annotations, store.Annotations{
		DocumentAnnotation: encodeTestReference(t, created.Reference),
	}) {
		t.Errorf("annotations = %v, want the reference unchanged", p.stored("lab").Metadata.Annotations)
	}
}
