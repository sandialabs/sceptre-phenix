package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	bdoc "phenix/types/builder"
)

// builderTestFile is a Builder file path a reference in these tests names.
// It is outside the directory the tests' server reads Builder files from, so
// no test reads it.
const builderTestFile = "/phenix/topologies/site/phenix-configs/builder.yaml"

// builderTopologyReference returns the document reference the stored topology
// name holds.
func builderTopologyReference(t *testing.T, harness *builderHarness, name string) bapi.DocumentReference {
	t.Helper()

	topology, err := harness.getConfig(builderKindTopology + "/" + name)
	if err != nil {
		t.Fatalf("topology %s missing: %v", name, err)
	}

	reference, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("topology %s: DecodeReference returned error: %v", name, err)
	}

	return reference
}

// setBuilderTopologyReference stores reference as the document annotation
// of the stored topology name, as a write past the Topology config hook
// does. A reference with no sub-key removes the annotation.
func setBuilderTopologyReference(t *testing.T, harness *builderHarness, name string, reference bapi.DocumentReference) {
	t.Helper()

	for i := range harness.configs {
		if harness.configs[i].FullName() != builderKindTopology+"/"+name {
			continue
		}

		annotations := store.Annotations{}
		maps.Copy(annotations, harness.configs[i].Metadata.Annotations)

		if reference == (bapi.DocumentReference{}) {
			delete(annotations, bapi.DocumentAnnotation)
		} else {
			annotations[bapi.DocumentAnnotation] = encodeBuilderReference(t, reference)
		}

		harness.configs[i].Metadata.Annotations = annotations

		return
	}

	t.Fatalf("topology %s missing", name)
}

// encodeBuilderReference returns ref as a topology's annotation holds it.
func encodeBuilderReference(t *testing.T, ref bapi.DocumentReference) string {
	t.Helper()

	encoded, err := ref.EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	return encoded
}

// listedBuilderDocuments returns what GET /builder/documents lists.
func listedBuilderDocuments(t *testing.T, harness *builderHarness) []builderDocumentResponse {
	t.Helper()

	recorder := harness.do(builderRequest{method: http.MethodGet, path: "/builder/documents", user: builderTestOwner})
	if recorder.Code != http.StatusOK {
		t.Fatalf("listing documents: status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.decode(recorder, &response)

	return response.Documents
}

// TestBuilderDocumentsByReferenceForm lists and reads a published document
// through each form of reference its topology may hold: its digest, its ID,
// or both, with or without a path. A reference that pins another digest,
// names another document, or names only a file names no stored document, and
// nor does another topology that names the document by its ID. A topology
// that names a file and no stored document is listed as read from the file.
func TestBuilderDocumentsByReferenceForm(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, builderKindTopology, "site"), builderConfig(t, builderKindTopology, "copy"))

	put := func(target, content string) *bapi.PublishedDocument {
		t.Helper()

		document, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
			Target: target, Kind: builderKindTopology, Actor: builderTestOwner,
			Document: builderDocument(t, content), DraftID: "", SnapshotID: "",
		})
		if err != nil {
			t.Fatalf("PutPublishedDocument returned error: %v", err)
		}

		return document
	}

	published := put("site", "site")
	other := put("copy", "copy")

	for _, test := range []struct {
		name string
		// site and copy are the references the two topologies hold.
		site, copy bapi.DocumentReference
		listed     bool
		// file is set when topology site is listed with its file.
		file bool
	}{
		{name: "digest", site: bapi.DocumentReference{Digest: published.Digest}, listed: true},
		{name: "id", site: bapi.DocumentReference{ID: published.ID}, listed: true},
		{name: "digest and id", site: published.Reference(), listed: true},
		{
			name: "digest, id and path", listed: true,
			site: bapi.DocumentReference{Digest: published.Digest, ID: published.ID, Path: builderTestFile},
		},
		{
			name: "digest and path", listed: true,
			site: bapi.DocumentReference{Digest: published.Digest, Path: builderTestFile},
		},
		{name: "path", site: bapi.DocumentReference{Path: builderTestFile}, file: true},
		{
			name: "another digest and path", file: true,
			site: bapi.DocumentReference{Digest: other.Digest, ID: published.ID, Path: builderTestFile},
		},
		{name: "another digest", site: bapi.DocumentReference{Digest: other.Digest, ID: published.ID}},
		{name: "another digest alone", site: bapi.DocumentReference{Digest: other.Digest}},
		{name: "another topology's id", site: bapi.DocumentReference{ID: other.ID}},
		{name: "another topology's id with this digest", site: bapi.DocumentReference{Digest: published.Digest, ID: other.ID}},
		{name: "only another topology names it", copy: bapi.DocumentReference{ID: published.ID}},
		{name: "only another topology names it, with its digest", copy: published.Reference()},
	} {
		t.Run(test.name, func(t *testing.T) {
			setBuilderTopologyReference(t, harness, "site", test.site)
			setBuilderTopologyReference(t, harness, "copy", test.copy)

			listed := listedBuilderDocuments(t, harness)

			if test.listed != (len(listed) == 1 && listed[0].ID == published.ID && listed[0].Target == "site" &&
				listed[0].Source == builderDocumentSourceStore && listed[0].Path == "") {
				t.Fatalf("listed documents = %+v, want the document of topology site listed: %t", listed, test.listed)
			}

			if test.file != (len(listed) == 1 && reflect.DeepEqual(listed[0], newBuilderFileResponse("site", builderTestFile))) {
				t.Fatalf("listed documents = %+v, want the file of topology site listed: %t", listed, test.file)
			}

			if !test.listed && !test.file && len(listed) != 0 {
				t.Fatalf("listed documents = %+v, want none", listed)
			}

			recorder := harness.do(builderRequest{
				method: http.MethodGet, path: "/builder/documents/" + published.ID, user: builderTestOwner,
			})

			if want := map[bool]int{true: http.StatusOK, false: http.StatusNotFound}[test.listed]; recorder.Code != want {
				t.Fatalf("getting the document: status = %d, want %d: %s", recorder.Code, want, recorder.Body.String())
			}

			if !test.listed {
				return
			}

			var response builderDocumentResponse
			harness.decode(recorder, &response)

			// The response holds the canonical JSON, compacted.
			var want bytes.Buffer
			if err := json.Compact(&want, builderDocument(t, "site")); err != nil {
				t.Fatalf("compacting the document: %v", err)
			}

			if response.Digest != published.Digest || !bytes.Equal(response.Document, want.Bytes()) {
				t.Fatalf("document = %+v, want the published content", response)
			}
		})
	}
}

// TestBuilderDocumentStoredAfreshStaysCurrent stores a topology's document
// again after its record was lost, as publishing the same content again
// does: the new record has another author, draft and time. The topology's
// reference names the document by its digest and ID only, so it still lists
// and reads, as the new record.
func TestBuilderDocumentStoredAfreshStaysCurrent(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderTestOwner, "topo")

	publishBuilderDraft(t, harness, draft, `{"mode":"topology","topology":{"name":"topo","action":"create"}}`, http.StatusOK)

	reference := builderTopologyReference(t, harness, "topo")

	first := listedBuilderDocuments(t, harness)
	if len(first) != 1 || first[0].ID != reference.ID || first[0].DraftID != draft.ID {
		t.Fatalf("listed documents = %+v, want the one draft %s published", first, draft.ID)
	}

	_, data, err := harness.service.GetPublishedDocumentData(t.Context(), reference.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData returned error: %v", err)
	}

	if _, err := harness.service.DeleteTargetDocuments(t.Context(), "topo"); err != nil {
		t.Fatalf("DeleteTargetDocuments returned error: %v", err)
	}

	if listed := listedBuilderDocuments(t, harness); len(listed) != 0 {
		t.Fatalf("listed documents = %+v, want none while the record is gone", listed)
	}

	stored, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
		Target: "topo", Kind: builderKindTopology, Actor: "someone-else",
		Document: data, DraftID: "another-draft", SnapshotID: "another-snapshot",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	if stored.ID != reference.ID || stored.CreatedAt.Equal(first[0].CreatedAt) {
		t.Fatalf("stored document = %+v, want %s stored afresh", stored, reference.ID)
	}

	if builderTopologyReference(t, harness, "topo") != reference {
		t.Fatal("the topology's reference changed")
	}

	listed := listedBuilderDocuments(t, harness)
	if len(listed) != 1 || listed[0].ID != reference.ID || listed[0].DraftID != "another-draft" ||
		listed[0].CreatedBy != "someone-else" || !listed[0].CreatedAt.Equal(stored.CreatedAt) {
		t.Fatalf("listed documents = %+v, want the record stored afresh", listed)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/documents/" + reference.ID, user: builderTestOwner,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("getting the document: status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

// TestBuilderPublishKeepsReferencePath publishes to a topology whose
// reference names a Builder file: the reference then names the stored
// document by its digest and ID, and still names the file. A topology the
// publication creates names no file, and nor does one whose reference could
// not be decoded.
func TestBuilderPublishKeepsReferencePath(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"site","action":"update"}}`

	site := builderConfig(t, builderKindTopology, "site")
	site.Spec = map[string]any{"nodes": []any{includeNode("aa")}}
	site.Metadata.Annotations = store.Annotations{
		"keep":                  "value",
		bapi.DocumentAnnotation: encodeBuilderReference(t, bapi.DocumentReference{Path: builderTestFile}),
	}

	broken := builderConfig(t, builderKindTopology, "broken")
	broken.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: "{"}

	harness := newBuilderHarness(t, site, broken)

	document := generateBuilderDocument(t, harness, "Topology/site")
	draft := createBuilderPublishDraft(t, harness, document, "Topology/site")

	// The first publication updates the topology the draft was imported from,
	// and the second the topology that holds the draft's own document.
	for _, hostname := range []string{"bb", "cc"} {
		draft = editBuilderDraft(t, harness, draft, document, hostname)

		response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)
		if response.Stages[1].Status != "updated" {
			t.Fatalf("stages = %#v, want the topology updated", response.Stages)
		}

		if !slices.Contains(response.Warnings, builderFileNotWrittenWarning("site", builderTestFile)) {
			t.Fatalf("warnings = %q, want one saying the file is not written", response.Warnings)
		}

		draft = response.Draft

		want := bapi.DocumentReference{
			Digest: draft.Digest, ID: bapi.PublishedDocumentID("site", draft.Digest), Path: builderTestFile,
		}

		if got := builderTopologyReference(t, harness, "site"); got != want {
			t.Fatalf("reference after publishing %s = %+v, want %+v", hostname, got, want)
		}

		if listed := listedBuilderDocuments(t, harness); len(listed) != 1 || listed[0].ID != want.ID {
			t.Fatalf("listed documents = %+v, want the stored document %s", listed, want.ID)
		}
	}

	stored, err := harness.getConfig("Topology/site")
	if err != nil || stored.Metadata.Annotations["keep"] != "value" {
		t.Fatalf("topology = %+v, %v; want its other annotation kept", stored, err)
	}

	if got := topologyHostnames(t, harness, "site"); !slices.Equal(got, []string{"aa", "bb", "cc"}) {
		t.Fatalf("topology nodes = %v, want aa, bb and cc", got)
	}

	created, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"elsewhere","action":"create"}}`, http.StatusOK)

	if got := builderTopologyReference(t, harness, "elsewhere"); got.Path != "" || got.Digest != draft.Digest {
		t.Fatalf("reference of a created topology = %+v, want the document and no file", got)
	}

	if slices.ContainsFunc(created.Warnings, func(warning string) bool { return strings.Contains(warning, "Builder file") }) {
		t.Fatalf("warnings = %q, want none of a file for a topology that names none", created.Warnings)
	}

	other := generateBuilderDocument(t, harness, "Topology/broken")
	publishBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, other, "Topology/broken"),
		`{"mode":"topology","topology":{"name":"broken","action":"update"}}`, http.StatusOK)

	if got := builderTopologyReference(t, harness, "broken"); got.Path != "" || got.ID == "" || got.Digest == "" {
		t.Fatalf("reference replacing an undecodable one = %+v, want the document and no file", got)
	}
}

// TestBuilderPublishAppliedByReferenceForm publishes a snapshot whose
// document its topology already names, by its digest, its ID or both: the
// topology stage is already applied, and nothing is written. A reference
// that names another document, or only a file, is not the publication's.
func TestBuilderPublishAppliedByReferenceForm(t *testing.T) {
	const create = `{"mode":"topology","topology":{"name":"lab","action":"create"}}`

	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("lab")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")

	response, _ := publishBuilderDraft(t, harness, draft, create, http.StatusOK)
	draft = response.Draft

	own := builderTopologyReference(t, harness, "lab")
	foreign := "sha256:" + strings.Repeat("0", 64)

	for _, test := range []struct {
		name    string
		ref     bapi.DocumentReference
		applied bool
	}{
		{name: "digest and id", ref: own, applied: true},
		{name: "digest", ref: bapi.DocumentReference{Digest: own.Digest}, applied: true},
		{name: "id", ref: bapi.DocumentReference{ID: own.ID}, applied: true},
		{name: "digest, id and path", ref: bapi.DocumentReference{Digest: own.Digest, ID: own.ID, Path: builderTestFile}, applied: true},
		{name: "digest and path", ref: bapi.DocumentReference{Digest: own.Digest, Path: builderTestFile}, applied: true},
		{name: "path", ref: bapi.DocumentReference{Path: builderTestFile}},
		{name: "another digest", ref: bapi.DocumentReference{Digest: foreign}},
		{name: "its id with another digest", ref: bapi.DocumentReference{Digest: foreign, ID: own.ID}},
		{name: "its digest with another id", ref: bapi.DocumentReference{Digest: own.Digest, ID: "another-document"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			setBuilderTopologyReference(t, harness, "lab", test.ref)

			writes := harness.configWrites

			if !test.applied {
				_, reason := publishBuilderDraft(t, harness, draft, create, http.StatusConflict)
				if reason != "config lab already exists; choose update explicitly" || harness.configWrites != writes {
					t.Fatalf("refusal = %q after %d writes, want the existing topology refused", reason, harness.configWrites-writes)
				}

				return
			}

			response, _ := publishBuilderDraft(t, harness, draft, create, http.StatusOK)
			draft = response.Draft

			if response.Stages[1].Status != bapi.PublishSkipped || harness.configWrites != writes {
				t.Fatalf("stages = %#v after %d writes, want the topology stage skipped", response.Stages, harness.configWrites-writes)
			}

			// An applied stage leaves the reference as it is.
			if got := builderTopologyReference(t, harness, "lab"); got != test.ref {
				t.Fatalf("reference = %+v, want %+v left as it was", got, test.ref)
			}
		})
	}
}

// TestBuilderPublishUpdateOwnedByTheRecordsDraft updates a topology whose
// reference names a document the draft holds no record of having published:
// the topology was put back as it was after an earlier publication of the
// draft. The document's record says which draft published it, so the draft
// may update the topology, for as long as that record can be read.
func TestBuilderPublishUpdateOwnedByTheRecordsDraft(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`

	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("lab")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)
	draft = response.Draft

	first, err := harness.getConfig("Topology/lab")
	if err != nil {
		t.Fatalf("published topology missing: %v", err)
	}

	first = cloneBuilderConfig(first)
	firstReference := builderTopologyReference(t, harness, "lab")

	// restore puts the topology back as its first publication wrote it.
	restore := func() {
		for i := range harness.configs {
			if harness.configs[i].FullName() == "Topology/lab" {
				harness.configs[i] = *cloneBuilderConfig(first)
			}
		}
	}

	draft = editBuilderDraft(t, harness, draft, document, "bb")
	response, _ = publishBuilderDraft(t, harness, draft, update, http.StatusOK)
	draft = response.Draft

	if draft.Publication == nil || draft.Publication.DocumentID == firstReference.ID {
		t.Fatalf("publication = %+v, want the draft to record its second document", draft.Publication)
	}

	record, err := harness.service.GetPublishedDocument(t.Context(), firstReference.ID)
	if err != nil || record.DraftID != draft.ID {
		t.Fatalf("the first document = %+v, %v; want it stored, naming the draft", record, err)
	}

	restore()

	draft = editBuilderDraft(t, harness, draft, document, "cc")
	response, _ = publishBuilderDraft(t, harness, draft, update, http.StatusOK)
	draft = response.Draft

	if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"aa", "bb", "cc"}) {
		t.Fatalf("topology nodes = %v, want aa, bb and cc", got)
	}

	// Another draft's record vouches for nothing.
	other := harness.createDraft(builderTestOwner, "other")
	if _, reason := publishBuilderDraft(t, harness, other, update, http.StatusConflict); reason !=
		"topology lab is not the source this draft was loaded from" {
		t.Fatalf("refusal = %q, want another draft refused", reason)
	}

	// Without its record, the first document no longer says whose it is.
	restore()
	harness.store.Drop(bapi.NamespacePublished, firstReference.ID)

	draft = editBuilderDraft(t, harness, draft, document, "dd")
	if _, reason := publishBuilderDraft(t, harness, draft, update, http.StatusConflict); reason !=
		"topology lab is not the source this draft was loaded from" {
		t.Fatalf("refusal = %q, want a document without a record refused", reason)
	}

	// A document the draft recorded is its own by the reference alone, and
	// counts as changed once it cannot be read.
	setBuilderTopologyReference(t, harness, "lab", bapi.DocumentReference{ID: draft.Publication.DocumentID})
	harness.store.Drop(bapi.NamespacePublished, draft.Publication.DocumentID)

	if _, reason := publishBuilderDraft(t, harness, draft, update, http.StatusConflict); reason !=
		"topology lab changed after this draft published it" {
		t.Fatalf("refusal = %q, want the topology changed", reason)
	}
}

// TestBuilderPublishRefusesAnotherTopologysDocument stores a copy of a
// published topology under another name, with a reference that names the
// original's document by its ID alone, which the Topology config hook cannot
// tell is another topology's. The document is not the copy's, so the draft
// that published the original may not update the copy through it, although
// the copy still equals the document's projection.
func TestBuilderPublishRefusesAnotherTopologysDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("lab")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)
	draft = response.Draft

	lab, err := harness.getConfig("Topology/lab")
	if err != nil {
		t.Fatalf("published topology missing: %v", err)
	}

	copied := cloneBuilderConfig(lab)
	copied.Metadata.Name = "copy"
	harness.configs = append(harness.configs, *copied)

	// Each case edits the draft by adding the host its name is.
	for name, reference := range map[string]bapi.DocumentReference{
		"id":            {ID: draft.Publication.DocumentID},
		"id-and-digest": builderTopologyReference(t, harness, "lab"),
	} {
		setBuilderTopologyReference(t, harness, "copy", reference)

		writes := harness.configWrites
		edited := editBuilderDraft(t, harness, draft, document, name)

		_, reason := publishBuilderDraft(t, harness, edited,
			`{"mode":"topology","topology":{"name":"copy","action":"update"}}`, http.StatusConflict)
		if reason != "topology copy changed after this draft published it" || harness.configWrites != writes {
			t.Fatalf("%s: refusal = %q after %d writes, want the copy refused", name, reason, harness.configWrites-writes)
		}

		if listed := listedBuilderDocuments(t, harness); len(listed) != 1 || listed[0].Target != "lab" {
			t.Fatalf("%s: listed documents = %+v, want only the document of topology lab", name, listed)
		}

		draft = edited
	}
}

// TestBuilderCleanupKeepsOnlyATopologysOwnDocument runs the startup
// cleanup over topologies holding each kind of reference. A document its own
// topology names, by digest or by ID, is kept. One that only another topology
// names is removed, and a reference that names only a file keeps nothing and
// does not stop the cleanup. An undecodable reference still stops it.
func TestBuilderCleanupKeepsOnlyATopologysOwnDocument(t *testing.T) {
	t.Parallel()

	var (
		fake  = memrecord.New()
		clock = new(atomic.Int64)
		now   = func() time.Time { return memrecord.Time(clock.Load()) }
	)

	fake.Stamp = now

	service, err := bapi.New(bapi.WithStore(fake), bapi.WithChunkSize(1024), bapi.WithClock(now))
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	put := func(target, content string) *bapi.PublishedDocument {
		t.Helper()

		document, err := service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
			Target: target, Kind: builderKindTopology, Actor: builderTestOwner,
			Document: builderDocument(t, content), DraftID: "", SnapshotID: "",
		})
		if err != nil {
			t.Fatalf("PutPublishedDocument returned error: %v", err)
		}

		return document
	}

	byDigest := put("by-digest", "by-digest")
	byID := put("by-id", "by-id")
	byBoth := put("by-both", "by-both")
	superseded := put("by-both", "by-both-v0")
	unnamed := put("file-only", "file-only")

	topology := func(name string, reference bapi.DocumentReference) store.Config {
		config := builderConfig(t, builderKindTopology, name)
		config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: encodeBuilderReference(t, reference)}

		return config
	}

	configs := store.Configs{
		topology("by-digest", bapi.DocumentReference{Digest: byDigest.Digest}),
		topology("by-id", bapi.DocumentReference{ID: byID.ID}),
		topology("by-both", bapi.DocumentReference{Digest: byBoth.Digest, ID: byBoth.ID, Path: builderTestFile}),
		// Another topology names, by its ID alone, a document that is not its own.
		topology("copy", bapi.DocumentReference{ID: superseded.ID}),
		topology("file-only", bapi.DocumentReference{Path: builderTestFile}),
		builderConfig(t, builderKindTopology, "plain"),
	}

	api := &builderAPI{
		drafts:        service,
		listConfigs:   func(string) (store.Configs, error) { return configs, nil },
		getConfig:     func(string) (*store.Config, error) { return nil, store.ErrNotExist },
		publish:       builderPublishOps{},
		documentFiles: func() (string, []string) { return "", nil },
		templateFiles: "",
	}

	stored := func(document *bapi.PublishedDocument) bool {
		t.Helper()

		_, _, err := service.GetPublishedDocumentData(context.Background(), document.ID)
		if err != nil && !errors.Is(err, bapi.ErrNotFound) {
			t.Fatalf("reading document %s: %v", document.ID, err)
		}

		return err == nil
	}

	// A document stored within the grace period is kept, whatever names it.
	api.cleanupStorage()

	for _, document := range []*bapi.PublishedDocument{byDigest, byID, byBoth, superseded, unnamed} {
		if !stored(document) {
			t.Fatalf("the recent document of %s was removed", document.Target)
		}
	}

	clock.Add(int64(2 * bapi.OrphanGracePeriod / time.Second))

	// An undecodable reference stops the cleanup of documents.
	broken := builderConfig(t, builderKindTopology, "broken")
	broken.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: `{"id":"` + byID.ID + `","draftId":"draft-1"}`}
	configs = append(configs, broken)

	api.cleanupStorage()

	if !stored(superseded) || !stored(unnamed) {
		t.Fatal("documents were removed although a topology's reference could not be decoded")
	}

	configs = configs[:len(configs)-1]

	api.cleanupStorage()

	for _, document := range []*bapi.PublishedDocument{byDigest, byID, byBoth} {
		if !stored(document) {
			t.Errorf("the document topology %s names was removed", document.Target)
		}
	}

	for _, document := range []*bapi.PublishedDocument{superseded, unnamed} {
		if stored(document) {
			t.Errorf("document %s of topology %s was kept, though its topology does not name it", document.ID, document.Target)
		}
	}
}

// TestConfigRoutesShowBuilderDocumentAsMap creates, gets, lists and updates
// a topology through /configs: its builder-doc annotation is a map of
// sub-keys in every request and response, the store keeps it as one string,
// and a reference that is not valid is refused with a message that says why.
func TestConfigRoutesShowBuilderDocumentAsMap(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	harness := newBuilderStoreHarness(t, nil)

	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	topology := func(reference string) string {
		return `{"apiVersion": "phenix.sandia.gov/v1", "kind": "Topology", "metadata": {"name": "site",` +
			` "annotations": {"keep": "v", "builder-doc": ` + reference + `}}, "spec": {"nodes": []}}`
	}

	type shown struct {
		Metadata struct {
			Name        string         `json:"name"`
			Annotations map[string]any `json:"annotations"`
		} `json:"metadata"`
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/configs", user: builderTestOwner, contentType: "application/json",
		body: topology(`{"path": "` + builderTestFile + `", "digest": "` + digest + `"}`),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST /configs: status = %d: %s", recorder.Code, recorder.Body)
	}

	want := map[string]any{"keep": "v", "builder-doc": map[string]any{"digest": digest, "path": builderTestFile}}

	check := func(stage string) {
		t.Helper()

		var got shown

		harness.decode(harness.do(builderRequest{
			method: http.MethodGet, path: "/configs/topology/site", user: builderTestOwner,
		}), &got)

		if !reflect.DeepEqual(got.Metadata.Annotations, want) {
			t.Fatalf("%s: GET /configs/topology/site shows annotations %#v, want %#v", stage, got.Metadata.Annotations, want)
		}

		var listed struct {
			Configs []shown `json:"configs"`
		}

		harness.decode(harness.do(builderRequest{
			method: http.MethodGet, path: "/configs?kind=Topology", user: builderTestOwner,
		}), &listed)

		if len(listed.Configs) != 1 || !reflect.DeepEqual(listed.Configs[0].Metadata.Annotations, want) {
			t.Fatalf("%s: GET /configs lists %#v, want the annotations %#v", stage, listed.Configs, want)
		}

		stored, err := config.Get("Topology/site", false)
		if err != nil {
			t.Fatalf("%s: getting the topology returned error: %v", stage, err)
		}

		reference := encodeBuilderReference(t, bapi.DocumentReference{Digest: digest, Path: builderTestFile})
		if got := stored.Metadata.Annotations[bapi.DocumentAnnotation]; got != reference {
			t.Fatalf("%s: the stored annotation = %q, want the string %q", stage, got, reference)
		}
	}

	check("create")

	// The body GET returns is accepted back as it is.
	body := harness.do(builderRequest{method: http.MethodGet, path: "/configs/topology/site", user: builderTestOwner}).Body.String()

	recorder = harness.do(builderRequest{
		method: http.MethodPut, path: "/configs/topology/site", user: builderTestOwner, contentType: "application/json", body: body,
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("PUT /configs: status = %d: %s", recorder.Code, recorder.Body)
	}

	check("update")

	for name, test := range map[string]struct {
		reference, reason string
		// unparsed is set for a body the config routes cannot parse at all,
		// which they answer with a status of their own.
		unparsed bool
	}{
		"an unknown sub-key": {reference: `{"file": "/phenix/builder.yaml"}`, reason: `property "file" is unsupported`},
		"no sub-key":         {reference: `{}`, reason: "at least 1 properties"},
		"a string":           {reference: `"not a map"`, reason: "value must be an object"},
		"the reference an earlier Builder wrote": {
			reference: strconv.Quote(`{"id":"0f1e2d","digest":"` + digest + `","size":10,"chunks":1}`), reason: "value must be an object",
		},
		"a relative path":   {reference: `{"path": "builder.yaml"}`, reason: "must be an absolute path"},
		"an invalid digest": {reference: `{"digest": "deadbeef"}`, reason: "digest is not a sha256 digest"},
		"an invalid id":     {reference: `{"id": "../escape"}`, reason: "id is not a valid document identifier"},
		"a number":          {reference: `42`, reason: "annotation builder-doc", unparsed: true},
		"a list sub-key":    {reference: `{"id": ["a"]}`, reason: "annotation builder-doc", unparsed: true},
	} {
		for _, request := range []builderRequest{
			{method: http.MethodPut, path: "/configs/topology/site"},
			{method: http.MethodPost, path: "/configs"},
		} {
			request.user, request.contentType = builderTestOwner, "application/json"
			request.body = strings.Replace(topology(test.reference), `"name": "site"`, `"name": "refused"`, 1)

			if request.method == http.MethodPut {
				request.body = topology(test.reference)
			}

			recorder := harness.do(request)

			var refused struct {
				Message  string         `json:"message"`
				Cause    string         `json:"cause"`
				Metadata map[string]any `json:"metadata"`
			}

			harness.decode(recorder, &refused)

			said := refused.Message + " " + refused.Cause + " " + fmt.Sprint(refused.Metadata)
			if !strings.Contains(said, test.reason) || recorder.Code < http.StatusBadRequest ||
				(!test.unparsed && recorder.Code != http.StatusBadRequest) {
				t.Errorf("%s: %s %s: status = %d, said %q; want it refused with %q",
					name, request.method, request.path, recorder.Code, said, test.reason)
			}
		}

		if _, err := config.Get("Topology/refused", false); !errors.Is(err, store.ErrNotExist) {
			t.Errorf("%s: the refused topology was stored: error = %v", name, err)
		}

		check(name)
	}
}
