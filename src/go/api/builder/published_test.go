package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"phenix/types/builder"
)

func publishTestDocument(t *testing.T, h *testHarness, target string, data []byte) *PublishedDocument {
	t.Helper()

	doc, err := h.service.PutPublishedDocument(context.Background(), PutPublishedDocumentRequest{
		Target: target, Kind: "Topology", Actor: testActor,
		Document: data, DraftID: "draft-1", SnapshotID: "snap-1",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument(%q) returned error: %v", target, err)
	}

	return doc
}

func TestPublishedDocumentRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 4000)
	doc := publishTestDocument(t, h, "topo", data)

	switch {
	case doc.Digest != digestOf(data):
		t.Fatal("published digest must be the document digest")
	case doc.ID != PublishedDocumentID("topo", doc.Digest):
		t.Fatalf("published ID = %q, want it content addressed", doc.ID)
	case doc.Size != int64(len(data)):
		t.Fatalf("size = %d, want %d", doc.Size, len(data))
	case len(doc.ChunkDigests) < 2:
		t.Fatalf("chunks = %d, want a multi-chunk payload", len(doc.ChunkDigests))
	case doc.CreatedBy != testActor || doc.Target != "topo" || doc.Kind != "Topology":
		t.Fatalf("published document = %+v, want provenance recorded", doc)
	}

	fetched, fetchedData, err := h.service.GetPublishedDocumentData(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData returned error: %v", err)
	}

	if !bytes.Equal(fetchedData, data) {
		t.Fatal("published document bytes changed in the store")
	}

	if fetched.SnapshotID != "snap-1" || fetched.DraftID != "draft-1" {
		t.Fatalf("published document = %+v, want the draft link preserved", fetched)
	}

	parsed, err := builder.Decode(fetchedData)
	if err != nil {
		t.Fatalf("decoding published document returned error: %v", err)
	}

	if parsed.ID != builder.DocumentID("topo") {
		t.Fatalf("decoded document ID = %q, want %q", parsed.ID, builder.DocumentID("topo"))
	}
}

func TestPublishedDocumentReferenceRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	doc := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 2000))
	ref := doc.Reference()

	encoded, err := ref.EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	if strings.Contains(encoded, "\n") || strings.Contains(encoded, "  ") {
		t.Fatalf("annotation value must be compact JSON, got %q", encoded)
	}

	if !json.Valid([]byte(encoded)) {
		t.Fatalf("annotation value is not valid JSON: %q", encoded)
	}

	decoded, err := DecodeReference(encoded)
	if err != nil {
		t.Fatalf("DecodeReference returned error: %v", err)
	}

	switch {
	case decoded.ID != ref.ID || decoded.Digest != ref.Digest:
		t.Fatalf("decoded reference = %+v, want %+v", decoded, ref)
	case decoded.Chunks != len(doc.ChunkDigests) || decoded.ChunkSize != doc.ChunkSize:
		t.Fatalf("decoded reference chunking = %d x %d, want %d x %d",
			decoded.Chunks, decoded.ChunkSize, len(doc.ChunkDigests), doc.ChunkSize)
	case decoded.Schema != builder.SchemaURI:
		t.Fatalf("reference schema = %q, want %q", decoded.Schema, builder.SchemaURI)
	}

	verified, err := h.service.VerifyPublishedDocument(ctx, decoded)
	if err != nil {
		t.Fatalf("VerifyPublishedDocument returned error: %v", err)
	}

	if !bytes.Equal(verified, testRandomDocument(t, "topo", 2000)) {
		t.Fatal("verified document bytes do not match what was published")
	}

	if _, err := DecodeReference("{}"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DecodeReference of an empty object error = %s, want ErrInvalid", fmtErr(err))
	}

	if _, err := DecodeReference("not json"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DecodeReference of invalid JSON error = %s, want ErrInvalid", fmtErr(err))
	}
}

func TestPublishedDocumentIsImmutableAndIdempotent(t *testing.T) {
	h := newHarness(t)

	data := testRandomDocument(t, "topo", 1500)

	first := publishTestDocument(t, h, "topo", data)
	chunks := h.store.count(NamespaceChunks)

	second := publishTestDocument(t, h, "topo", data)

	// The same document, whose record is stored again, unchanged, to say it
	// was published again (see TestRepublishedDocumentIsNotAnOrphan).
	if first.ID != second.ID || first.PayloadID != second.PayloadID ||
		!first.CreatedAt.Equal(second.CreatedAt) || second.Revision <= first.Revision {
		t.Fatalf("republishing identical content produced %+v then %+v", first, second)
	}

	if h.store.count(NamespaceChunks) != chunks {
		t.Fatal("republishing identical content must not write new chunks")
	}

	// Identical content published to a different target is a separate document.
	other := publishTestDocument(t, h, "other-topo", data)

	if other.ID == first.ID {
		t.Fatal("documents of different targets must not share an ID")
	}
}

func TestVerifyPublishedDocumentDetectsCorruptionAndLoss(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	doc := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 2000))
	ref := doc.Reference()

	mismatched := ref
	mismatched.Digest = "sha256:" + strings.Repeat("0", 64)

	if _, err := h.service.VerifyPublishedDocument(ctx, mismatched); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("VerifyPublishedDocument with a mismatched digest error = %s, want ErrCorrupt", fmtErr(err))
	}

	mismatched = ref
	mismatched.Chunks = ref.Chunks + 3

	if _, err := h.service.VerifyPublishedDocument(ctx, mismatched); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("VerifyPublishedDocument with a mismatched chunk count error = %s, want ErrCorrupt", fmtErr(err))
	}

	h.store.drop(NamespaceChunks, chunkKey(publishedPayloadScope(doc.ID, doc.PayloadID), 0))

	if _, err := h.service.VerifyPublishedDocument(ctx, ref); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("VerifyPublishedDocument with a missing chunk error = %s, want ErrCorrupt", fmtErr(err))
	}

	h.store.drop(NamespacePublished, doc.ID)

	if _, err := h.service.VerifyPublishedDocument(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("VerifyPublishedDocument of a deleted document error = %s, want ErrNotFound", fmtErr(err))
	}
}

// errPublishTimeout stands in for a store error that does not say whether the
// write happened, such as an etcd request that timed out after its proposal
// was applied.
var errPublishTimeout = errors.New("etcdserver: request timed out")

func TestPutPublishedDocumentSettlesAmbiguousWrites(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 2000)

	// The store applies the create, then reports it as failed.
	h.store.afterWrite = func(namespace, _ string) error {
		if namespace != NamespacePublished {
			return nil
		}

		return errPublishTimeout
	}

	doc, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor, Document: data, DraftID: "", SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument error = %s, want the applied document returned", fmtErr(err))
	}

	h.store.afterWrite = nil

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
		t.Fatalf("the applied document's content must be kept: %s", fmtErr(err))
	}

	again, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testPeer, Document: data, DraftID: "", SnapshotID: "",
	})
	if err != nil || again.PayloadID != doc.PayloadID {
		t.Fatalf("publishing again = %+v, %s; want the applied document", again, fmtErr(err))
	}

	// A create the store did not apply may still be applied later, so its
	// chunks are left to orphan cleanup instead of being removed at once.
	other := testRandomDocument(t, "other", 2000)
	otherID := PublishedDocumentID("other", digestOf(other))

	h.store.beforeCreate = func(namespace, _ string) error {
		if namespace != NamespacePublished {
			return nil
		}

		return errPublishTimeout
	}

	_, err = h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "other", Kind: "Topology", Actor: testActor, Document: other, DraftID: "", SnapshotID: "",
	})
	if !errors.Is(err, errPublishTimeout) {
		t.Fatalf("PutPublishedDocument error = %s, want the store error", fmtErr(err))
	}

	h.store.beforeCreate = nil

	pending := chunkKeysOf(h, publishedScope(otherID))
	if len(pending) == 0 {
		t.Fatal("the chunks of a create that may still be applied must be kept")
	}

	h.passOrphanGracePeriod()

	if removed, err := h.service.CleanupOrphanedChunks(ctx); err != nil || removed != len(pending) {
		t.Fatalf("CleanupOrphanedChunks = %d, %s; want the %d pending chunks removed", removed, fmtErr(err), len(pending))
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
		t.Fatalf("orphan cleanup must keep the applied document's content: %s", fmtErr(err))
	}
}

func TestPutPublishedDocumentRepairsCorruptContent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 2000)
	doc := publishTestDocument(t, h, "topo", data)

	h.store.drop(NamespaceChunks, chunkKey(publishedPayloadScope(doc.ID, doc.PayloadID), 0))

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("GetPublishedDocumentData with a lost chunk error = %s, want ErrCorrupt", fmtErr(err))
	}

	// Publishing the same content again replaces the corrupt copy instead of
	// returning it; the replacement is applied although the store reports an
	// error, and settling it must not undo the repair.
	h.store.afterWrite = func(namespace, _ string) error {
		if namespace != NamespacePublished {
			return nil
		}

		return errPublishTimeout
	}

	repaired := publishTestDocument(t, h, "topo", data)

	h.store.afterWrite = nil

	if repaired.ID != doc.ID || repaired.PayloadID == doc.PayloadID {
		t.Fatalf("republished document = %+v, want %s stored again with a new payload", repaired, doc.ID)
	}

	verified, err := h.service.VerifyPublishedDocument(ctx, repaired.Reference())
	if err != nil {
		t.Fatalf("VerifyPublishedDocument of the repaired document returned error: %s", fmtErr(err))
	}

	if !bytes.Equal(verified, data) {
		t.Fatal("the repaired document holds different content")
	}

	if chunks := chunkKeysOf(h, publishedPayloadScope(doc.ID, doc.PayloadID)); len(chunks) != 0 {
		t.Fatalf("chunks of the corrupt copy = %v, want none", chunks)
	}
}

func TestDeletePublishedDocumentKeepsConcurrentCopies(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 2000)
	first := publishTestDocument(t, h, "topo", data)

	// Another process publishes the same content again after the metadata was
	// deleted, but before the deleted document's chunks are.
	var second *PublishedDocument

	h.store.failDelete = func(namespace, _ string) error {
		if namespace == NamespaceChunks && second == nil {
			second = publishTestDocument(t, h, "topo", data)
		}

		return nil
	}

	if err := h.service.DeletePublishedDocument(ctx, first.ID); err != nil {
		t.Fatalf("DeletePublishedDocument returned error: %s", fmtErr(err))
	}

	h.store.failDelete = nil

	if second == nil || second.PayloadID == first.PayloadID {
		t.Fatalf("republished document = %+v, want a new payload", second)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, second.ID); err != nil {
		t.Fatalf("the copy published during the delete must stay readable: %s", fmtErr(err))
	}

	if chunks := chunkKeysOf(h, publishedPayloadScope(first.ID, first.PayloadID)); len(chunks) != 0 {
		t.Fatalf("chunks of the deleted document = %v, want none", chunks)
	}

	// A document deleted and stored again after a listing saw it is not the
	// listed document, so removing superseded documents skips it.
	current := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v2", 2000))

	var third *PublishedDocument

	h.store.failDelete = func(namespace, key string) error {
		if namespace == NamespacePublished && key == second.ID && third == nil {
			h.store.drop(NamespacePublished, second.ID)
			third = publishTestDocument(t, h, "topo", data)
		}

		return nil
	}

	removed, err := h.service.DeleteSupersededDocuments(ctx, "topo", current.ID)
	if err != nil || removed != 0 {
		t.Fatalf("DeleteSupersededDocuments = %d, %s; want the replaced document skipped", removed, fmtErr(err))
	}

	h.store.failDelete = nil

	if _, _, err := h.service.GetPublishedDocumentData(ctx, third.ID); err != nil {
		t.Fatalf("the copy stored after the listing must stay readable: %s", fmtErr(err))
	}
}

func TestDeleteSupersededDocuments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	first := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v1", 1200))
	second := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v2", 1200))
	otherTarget := publishTestDocument(t, h, "other", testRandomDocument(t, "other-v1", 1200))

	removed, err := h.service.DeleteSupersededDocuments(ctx, "topo", second.ID)
	if err != nil {
		t.Fatalf("DeleteSupersededDocuments returned error: %v", err)
	}

	if removed != 1 {
		t.Fatalf("removed %d documents, want 1", removed)
	}

	if _, err := h.service.GetPublishedDocument(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("superseded document error = %s, want ErrNotFound", fmtErr(err))
	}

	if _, err := h.service.GetPublishedDocument(ctx, second.ID); err != nil {
		t.Fatalf("the kept document must survive: %v", err)
	}

	if _, err := h.service.GetPublishedDocument(ctx, otherTarget.ID); err != nil {
		t.Fatalf("documents of other targets must survive: %v", err)
	}

	if chunks := chunkKeysOf(h, publishedScope(first.ID)); len(chunks) != 0 {
		t.Fatalf("chunks of the superseded document = %v, want none", chunks)
	}

	if chunks := chunkKeysOf(h, publishedScope(second.ID)); len(chunks) == 0 {
		t.Fatal("chunks of the kept document must survive")
	}

	if _, err := h.service.DeleteSupersededDocuments(ctx, "", second.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DeleteSupersededDocuments without a target error = %s, want ErrInvalid", fmtErr(err))
	}
}

// passOrphanGracePeriod moves the service clock far enough that everything
// stored so far is older than the [OrphanGracePeriod].
func (h *testHarness) passOrphanGracePeriod() {
	h.now.Add(int64(2 * OrphanGracePeriod / time.Second))
}

func TestCleanupOrphanedDocuments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	keep := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))
	orphan := publishTestDocument(t, h, "gone", testRandomDocument(t, "gone", 1000))

	// A document is stored before the config that references it, so a recent
	// one may be a publication still in flight.
	if removed, err := h.service.CleanupOrphanedDocuments(ctx, []DocumentReference{keep.Reference()}); err != nil || removed != 0 {
		t.Fatalf("CleanupOrphanedDocuments of a recent document = %d, %s; want it kept", removed, fmtErr(err))
	}

	h.passOrphanGracePeriod()

	removed, err := h.service.CleanupOrphanedDocuments(ctx, []DocumentReference{keep.Reference()})
	if err != nil {
		t.Fatalf("CleanupOrphanedDocuments returned error: %v", err)
	}

	if removed != 1 {
		t.Fatalf("removed %d documents, want 1", removed)
	}

	if chunks := chunkKeysOf(h, publishedScope(orphan.ID)); len(chunks) != 0 {
		t.Fatalf("chunks of the orphaned document = %v, want none", chunks)
	}

	if _, err := h.service.GetPublishedDocument(ctx, orphan.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphaned document error = %s, want ErrNotFound", fmtErr(err))
	}

	if _, err := h.service.GetPublishedDocument(ctx, keep.ID); err != nil {
		t.Fatalf("referenced document must survive: %v", err)
	}
}

// TestRepublishedDocumentIsNotAnOrphan publishes content again that an old
// document no config references any more holds. Until the config that will
// reference it is written, cleanup, perhaps in another phenix sharing the
// store, must not remove it: it counts as just published.
func TestRepublishedDocumentIsNotAnOrphan(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	data := testRandomDocument(t, "topo", 1000)

	old := publishTestDocument(t, h, "topo", data)

	h.passOrphanGracePeriod()

	reused := publishTestDocument(t, h, "topo", data)
	if reused.PayloadID != old.PayloadID {
		t.Fatalf("republished document = %+v, want the existing one %+v", reused, old)
	}

	if removed, err := h.service.CleanupOrphanedDocuments(ctx, nil); err != nil || removed != 0 {
		t.Fatalf("CleanupOrphanedDocuments after the republish = %d, %s; want it kept", removed, fmtErr(err))
	}

	if _, err := h.service.VerifyPublishedDocument(ctx, reused.Reference()); err != nil {
		t.Fatalf("the republished document: %v", err)
	}

	// Once it is old again, it is an orphan like any other.
	h.passOrphanGracePeriod()

	if removed, err := h.service.CleanupOrphanedDocuments(ctx, nil); err != nil || removed != 1 {
		t.Fatalf("CleanupOrphanedDocuments later = %d, %s; want it removed", removed, fmtErr(err))
	}
}

func TestCleanupOrphanedChunks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	draft := createTestDraft(t, h, "topo")
	doc := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))

	// Chunks left behind by a crash between writing content and metadata.
	orphanManifest := storePayload(t, h, snapshotScope("vanished-draft", "vanished-snapshot"), []byte(randomText(9, 2000)))

	live := h.store.count(NamespaceChunks) - len(orphanManifest.ChunkDigests)

	// Chunks are written before the metadata that references them, so recent
	// ones may belong to a write still in flight.
	if removed, err := h.service.CleanupOrphanedChunks(ctx); err != nil || removed != 0 {
		t.Fatalf("CleanupOrphanedChunks of recent chunks = %d, %s; want them kept", removed, fmtErr(err))
	}

	h.passOrphanGracePeriod()

	// A payload written since is kept while the settled orphan is removed.
	recent := storePayload(t, h, snapshotScope("vanished-draft", "recent-snapshot"), []byte(randomText(10, 500)))

	removed, err := h.service.CleanupOrphanedChunks(ctx)
	if err != nil {
		t.Fatalf("CleanupOrphanedChunks returned error: %v", err)
	}

	if removed != len(orphanManifest.ChunkDigests) {
		t.Fatalf("removed %d chunks, want %d", removed, len(orphanManifest.ChunkDigests))
	}

	if h.store.count(NamespaceChunks) != live+len(recent.ChunkDigests) {
		t.Fatalf("chunks = %d, want %d live and %d recent", h.store.count(NamespaceChunks), live, len(recent.ChunkDigests))
	}

	if _, err := h.service.GetCurrentDocument(ctx, draft.ID); err != nil {
		t.Fatalf("draft content must survive chunk cleanup: %v", err)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
		t.Fatalf("published content must survive chunk cleanup: %v", err)
	}
}

func TestCleanupOrphanedChunksKeepsInFlightWrites(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	h.passOrphanGracePeriod()

	// Another process cleans up after this append wrote its chunks but before
	// it wrote the metadata that references them.
	var (
		ran        bool
		removed    int
		cleanupErr error
	)

	h.store.failUpdate = func(namespace, _ string) error {
		if namespace == NamespaceDrafts && !ran {
			ran = true
			removed, cleanupErr = h.service.CleanupOrphanedChunks(ctx)
		}

		return nil
	}

	appendTestSnapshot(t, h, meta, "topo-v2", testActor)

	h.store.failUpdate = nil

	if !ran || cleanupErr != nil || removed != 0 {
		t.Fatalf("cleanup during the append = ran %v, removed %d, %s; want nothing removed", ran, removed, fmtErr(cleanupErr))
	}

	if _, err := h.service.GetCurrentDocument(ctx, meta.ID); err != nil {
		t.Fatalf("the appended snapshot must stay readable: %s", fmtErr(err))
	}
}

func TestPublishedDocumentValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testDocument(t, "topo", 0)

	tests := []struct {
		name string
		req  PutPublishedDocumentRequest
	}{
		{
			name: "no target",
			req:  PutPublishedDocumentRequest{Target: "", Kind: "Topology", Actor: testActor, Document: data, DraftID: "", SnapshotID: ""},
		},
		{
			name: "no kind",
			req:  PutPublishedDocumentRequest{Target: "topo", Kind: "", Actor: testActor, Document: data, DraftID: "", SnapshotID: ""},
		},
		{
			name: "no actor",
			req:  PutPublishedDocumentRequest{Target: "topo", Kind: "Topology", Actor: "", Document: data, DraftID: "", SnapshotID: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := h.service.PutPublishedDocument(ctx, tt.req); !errors.Is(err, ErrInvalid) {
				t.Fatalf("PutPublishedDocument error = %s, want ErrInvalid", fmtErr(err))
			}
		})
	}

	if _, err := h.service.GetPublishedDocument(ctx, "missing-document"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPublishedDocument error = %s, want ErrNotFound", fmtErr(err))
	}

	if err := h.service.DeletePublishedDocument(ctx, "../escape"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DeletePublishedDocument with an unsafe ID error = %s, want ErrInvalid", fmtErr(err))
	}
}

func TestMarkPublishedLinksPublishedDocument(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	doc, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: snapshot.Data, DraftID: meta.ID, SnapshotID: snapshot.Manifest.ID,
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	published, err := h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		SnapshotID: snapshot.Manifest.ID, Mode: PublishModeTopology,
		TopologyTarget: "topo", TopologyAction: TopologyActionCreate,
		ExperimentTarget: "", ScenarioTarget: "", DocumentID: doc.ID,
	})
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	if published.Publication.DocumentID != doc.ID {
		t.Fatalf("publication documentID = %q, want %q", published.Publication.DocumentID, doc.ID)
	}

	if doc.Digest != snapshot.Manifest.Digest {
		t.Fatal("published document digest must match the published snapshot digest")
	}

	if published.Dirty() {
		t.Fatal("draft must be clean immediately after publishing its current snapshot")
	}
}
