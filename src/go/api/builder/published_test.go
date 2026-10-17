package builder

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

// publishedDiff returns how two published documents differ, as cmp.Diff does,
// leaving out what the test cannot know: the payload, its ID, the time and the
// revision.
func publishedDiff(want, got *PublishedDocument) string {
	return cmp.Diff(want, got, cmpopts.IgnoreUnexported(PublishedDocument{}), cmpopts.IgnoreFields(
		PublishedDocument{}, "CompressedSize", "ChunkDigests", "PayloadID", "CreatedAt", "Revision",
	))
}

// publishedTestDocument is what publishTestDocument stores for data.
func publishedTestDocument(h *testHarness, target string, data []byte) *PublishedDocument {
	return &PublishedDocument{
		ID: PublishedDocumentID(target, digestOf(data)), Digest: digestOf(data), Size: int64(len(data)),
		ChunkSize: h.service.chunkSize, Target: target, Kind: "Topology", DraftID: "draft-1", SnapshotID: "snap-1",
		CreatedBy: testActor, Schema: builder.SchemaURI,
	}
}

func TestPublishedDocumentRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 4000)
	doc := publishTestDocument(t, h, "topo", data)

	if diff := publishedDiff(publishedTestDocument(h, "topo", data), doc); diff != "" {
		t.Errorf("PutPublishedDocument(topo) mismatch (-want +got):\n%s", diff)
	}

	if len(doc.ChunkDigests) < 2 {
		t.Errorf("PutPublishedDocument(topo) chunks = %d, want a multi-chunk payload", len(doc.ChunkDigests))
	}

	fetched, fetchedData, err := h.service.GetPublishedDocumentData(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData(%s) returned error: %v", doc.ID, err)
	}

	if diff := cmp.Diff(string(data), string(fetchedData)); diff != "" {
		t.Errorf("GetPublishedDocumentData(%s) data mismatch (-want +got):\n%s", doc.ID, diff)
	}

	if diff := cmp.Diff(doc, fetched, cmpopts.IgnoreUnexported(PublishedDocument{})); diff != "" {
		t.Errorf("GetPublishedDocumentData(%s) document mismatch (-want +got):\n%s", doc.ID, diff)
	}

	parsed, err := builder.Decode(fetchedData)
	if err != nil {
		t.Fatalf("decoding published document returned error: %v", err)
	}

	if got, want := parsed.Metadata.ID, builder.DocumentID("topo"); got != want {
		t.Errorf("GetPublishedDocumentData(%s) document ID = %q, want %q", doc.ID, got, want)
	}
}

// TestPublishedDocumentReferenceRoundTrip encodes a published document's
// reference as an annotation and reads the document it names.
func TestPublishedDocumentReferenceRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	doc := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 2000))
	ref := doc.Reference()

	if diff := cmp.Diff(DocumentReference{Digest: doc.Digest, ID: doc.ID}, ref); diff != "" {
		t.Errorf("Reference() mismatch (-want +got):\n%s", diff)
	}

	encoded, err := ref.EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	// The annotation value is the compact JSON of the reference.
	if diff := cmp.Diff(`{"digest":"`+doc.Digest+`","id":"`+doc.ID+`"}`, encoded); diff != "" {
		t.Errorf("EncodeReference() mismatch (-want +got):\n%s", diff)
	}

	decoded, err := DecodeReference(encoded)
	if err != nil {
		t.Fatalf("DecodeReference(%s) returned error: %v", encoded, err)
	}

	if diff := cmp.Diff(ref, decoded); diff != "" {
		t.Errorf("DecodeReference(%s) mismatch (-want +got):\n%s", encoded, diff)
	}

	if !decoded.Names(doc) {
		t.Errorf("DecodeReference(%s).Names(%s) = false, want true", encoded, doc.ID)
	}

	stored, verified, err := h.service.GetPublishedDocumentData(ctx, decoded.StoredID("topo"))
	if err != nil {
		t.Fatalf("GetPublishedDocumentData(%s) returned error: %v", decoded.StoredID("topo"), err)
	}

	data := testRandomDocument(t, "topo", 2000)

	if diff := cmp.Diff(string(data), string(verified)); diff != "" {
		t.Errorf("GetPublishedDocumentData(%s) data mismatch (-want +got):\n%s", doc.ID, diff)
	}

	if _, err := builder.Parse(verified); err != nil {
		t.Errorf("builder.Parse(GetPublishedDocumentData(%s)) returned error: %v", doc.ID, err)
	}

	// The record holds what the reference no longer does.
	if diff := publishedDiff(publishedTestDocument(h, "topo", data), stored); diff != "" {
		t.Errorf("GetPublishedDocumentData(%s) document mismatch (-want +got):\n%s", doc.ID, diff)
	}
}

// TestReferenceStoredID asserts a reference names its ID, else the ID its digest
// derives for the topology, else none.
func TestReferenceStoredID(t *testing.T) {
	t.Parallel()

	const (
		digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		id     = "another-document"
		file   = "/phenix/builder.yaml"
	)

	derived := PublishedDocumentID("topo", digest)

	for _, test := range []struct {
		name string
		ref  DocumentReference
		want string
	}{
		{name: "digest", ref: DocumentReference{Digest: digest}, want: derived},
		{name: "id", ref: DocumentReference{ID: id}, want: id},
		{name: "digest and id", ref: DocumentReference{Digest: digest, ID: id}, want: id},
		{name: "path", ref: DocumentReference{Path: file}, want: ""},
		{name: "path and digest", ref: DocumentReference{Digest: digest, Path: file}, want: derived},
		{name: "path and id", ref: DocumentReference{ID: id, Path: file}, want: id},
		{name: "all three", ref: DocumentReference{Digest: digest, ID: id, Path: file}, want: id},
		{name: "none", ref: DocumentReference{}, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.ref.StoredID("topo"); got != test.want {
				t.Errorf("%+v.StoredID(topo) = %q, want %q", test.ref, got, test.want)
			}
		})
	}

	if other := (DocumentReference{Digest: digest}).StoredID("other"); other == derived || other == "" {
		t.Errorf("StoredID(other) of a digest = %q, want an ID of its own, not %q or none", other, derived)
	}
}

// TestReferenceNames asserts a reference names a document only for the topology
// it was published to, with the digest it pins.
func TestReferenceNames(t *testing.T) {
	h := newHarness(t)

	doc := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))
	other := publishTestDocument(t, h, "other", testRandomDocument(t, "other", 1000))
	// The same content as doc, published to the other topology.
	copied := publishTestDocument(t, h, "other", testRandomDocument(t, "topo", 1000))

	const file = "/phenix/builder.yaml"

	for _, test := range []struct {
		name string
		ref  DocumentReference
		doc  *PublishedDocument
		want bool
	}{
		{name: "digest", ref: DocumentReference{Digest: doc.Digest}, doc: doc, want: true},
		{name: "id", ref: DocumentReference{ID: doc.ID}, doc: doc, want: true},
		{name: "digest and id", ref: doc.Reference(), doc: doc, want: true},
		{name: "all three", ref: DocumentReference{Digest: doc.Digest, ID: doc.ID, Path: file}, doc: doc, want: true},
		{name: "path and digest", ref: DocumentReference{Digest: doc.Digest, Path: file}, doc: doc, want: true},
		{name: "path", ref: DocumentReference{Path: file}, doc: doc, want: false},
		{name: "none", ref: DocumentReference{}, doc: doc, want: false},
		{name: "no document", ref: doc.Reference(), doc: nil, want: false},
		{name: "another digest", ref: DocumentReference{Digest: other.Digest, ID: doc.ID}, doc: doc, want: false},
		{name: "another digest alone", ref: DocumentReference{Digest: other.Digest}, doc: doc, want: false},
		{name: "ID of another document", ref: DocumentReference{ID: other.ID}, doc: doc, want: false},
		{name: "ID of another document and this digest", ref: DocumentReference{Digest: doc.Digest, ID: other.ID}, doc: doc, want: false},
		// The digest of a document published to another topology derives that
		// topology's own document, never this one.
		{name: "the same content of another topology", ref: DocumentReference{Digest: doc.Digest}, doc: copied, want: true},
		{name: "reference of another topology", ref: doc.Reference(), doc: copied, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.ref.Names(test.doc); got != test.want {
				t.Errorf("%+v.Names(document) = %t, want %t", test.ref, got, test.want)
			}
		})
	}
}

func TestPublishedDocumentIsImmutableAndIdempotent(t *testing.T) {
	h := newHarness(t)

	data := testRandomDocument(t, "topo", 1500)

	first := publishTestDocument(t, h, "topo", data)
	chunks := h.store.Count(NamespaceChunks)

	second := publishTestDocument(t, h, "topo", data)

	// The same document, whose record is stored again, unchanged, to say it
	// was published again (see TestRepublishedDocumentIsNotAnOrphan).
	if diff := cmp.Diff(first, second, cmpopts.IgnoreUnexported(PublishedDocument{}),
		cmpopts.IgnoreFields(PublishedDocument{}, "Revision")); diff != "" {
		t.Errorf("PutPublishedDocument(topo) of the same content mismatch (-want +got):\n%s", diff)
	}

	if second.Revision <= first.Revision {
		t.Errorf("PutPublishedDocument(topo) of the same content revision = %d, want more than %d",
			second.Revision, first.Revision)
	}

	if got := h.store.Count(NamespaceChunks); got != chunks {
		t.Errorf("PutPublishedDocument(topo) of the same content left %d chunks, want the %d already stored", got, chunks)
	}

	// Identical content published to a different target is a separate document.
	other := publishTestDocument(t, h, "other-topo", data)

	if other.ID == first.ID {
		t.Errorf("PutPublishedDocument(other-topo) ID = %s, want an ID other than that of topo", other.ID)
	}
}

// TestPublishedDocumentDataDetectsCorruptionAndLoss reads changed content as
// corrupt and a removed record as not found.
func TestPublishedDocumentDataDetectsCorruptionAndLoss(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 2000)
	doc := publishTestDocument(t, h, "topo", data)

	if _, read, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) returned error: %s", doc.ID, fmtErr(err))
	} else if diff := cmp.Diff(string(data), string(read)); diff != "" {
		t.Errorf("GetPublishedDocumentData(%s) data mismatch (-want +got):\n%s", doc.ID, diff)
	}

	// A reference that pins another digest does not name the document, so
	// nothing is read through it.
	mismatched := doc.Reference()
	mismatched.Digest = "sha256:" + strings.Repeat("0", 64)

	if mismatched.Names(doc) {
		t.Errorf("%+v.Names(%s) = true, want false for a mismatched digest", mismatched, doc.ID)
	}

	h.store.Drop(NamespaceChunks, chunkKey(publishedPayloadScope(doc.ID, doc.PayloadID), 0))

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); !errors.Is(err, ErrCorrupt) {
		t.Errorf("GetPublishedDocumentData(%s) with a missing chunk error = %s, want ErrCorrupt", doc.ID, fmtErr(err))
	}

	h.store.Drop(NamespacePublished, doc.ID)

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPublishedDocumentData(%s) of a deleted document error = %s, want ErrNotFound", doc.ID, fmtErr(err))
	}
}

// TestPutPublishedDocumentRemovesTheChunksOfAFailedWrite asserts that a
// published document write the store refused leaves no chunk behind.
func TestPutPublishedDocumentRemovesTheChunksOfAFailedWrite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	other := testRandomDocument(t, "other", 2000)
	otherID := PublishedDocumentID("other", digestOf(other))
	refused := errors.New("disk full")

	h.store.BeforeCreate = func(namespace, _ string) error {
		if namespace != NamespacePublished {
			return nil
		}

		return refused
	}

	_, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "other", Kind: "Topology", Actor: testActor, Document: other, DraftID: "", SnapshotID: "",
	})
	if !errors.Is(err, refused) {
		t.Errorf("PutPublishedDocument(other) error = %s, want the store error", fmtErr(err))
	}

	if chunks := chunkKeysOf(h, publishedScope(otherID)); len(chunks) != 0 {
		t.Errorf("PutPublishedDocument(other) left chunks %v, want the refused write's chunks removed", chunks)
	}
}

func TestPutPublishedDocumentRepairsCorruptContent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 2000)
	doc := publishTestDocument(t, h, "topo", data)

	h.store.Drop(NamespaceChunks, chunkKey(publishedPayloadScope(doc.ID, doc.PayloadID), 0))

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); !errors.Is(err, ErrCorrupt) {
		t.Errorf("GetPublishedDocumentData(%s) with a lost chunk error = %s, want ErrCorrupt", doc.ID, fmtErr(err))
	}

	// Publishing the same content again replaces the corrupt copy instead of
	// returning it.
	repaired := publishTestDocument(t, h, "topo", data)

	if repaired.ID != doc.ID {
		t.Errorf("PutPublishedDocument(topo) of corrupt content ID = %s, want %s", repaired.ID, doc.ID)
	}

	if repaired.PayloadID == doc.PayloadID {
		t.Errorf("PutPublishedDocument(topo) of corrupt content payload = %s, want a new payload", repaired.PayloadID)
	}

	_, verified, err := h.service.GetPublishedDocumentData(ctx, repaired.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData(%s) of the repaired document returned error: %s", repaired.ID, fmtErr(err))
	}

	if diff := cmp.Diff(string(data), string(verified)); diff != "" {
		t.Errorf("GetPublishedDocumentData(%s) of the repaired document mismatch (-want +got):\n%s", repaired.ID, diff)
	}

	if chunks := chunkKeysOf(h, publishedPayloadScope(doc.ID, doc.PayloadID)); len(chunks) != 0 {
		t.Errorf("chunks of the corrupt copy = %v, want none", chunks)
	}
}

func TestDeletingPublishedDocumentKeepsConcurrentCopies(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 2000)
	first := publishTestDocument(t, h, "topo", data)

	// Another process publishes the same content again after the metadata was
	// deleted, but before the deleted document's chunks are.
	var second *PublishedDocument

	h.store.FailDelete = func(namespace, _ string) error {
		if namespace == NamespaceChunks && second == nil {
			second = publishTestDocument(t, h, "topo", data)
		}

		return nil
	}

	removed, err := h.service.DeleteTargetDocuments(ctx, "topo")
	if err != nil {
		t.Fatalf("DeleteTargetDocuments(topo) returned error: %s", fmtErr(err))
	}

	if removed != 1 {
		t.Errorf("DeleteTargetDocuments(topo) = %d, want 1", removed)
	}

	h.store.FailDelete = nil

	if second == nil {
		t.Fatal("no document was published again during DeleteTargetDocuments(topo)")
	}

	if second.PayloadID == first.PayloadID {
		t.Errorf("republished document payload = %s, want a new payload", second.PayloadID)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, second.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) of the copy published during the delete returned error: %s",
			second.ID, fmtErr(err))
	}

	if chunks := chunkKeysOf(h, publishedPayloadScope(first.ID, first.PayloadID)); len(chunks) != 0 {
		t.Errorf("chunks of the deleted document = %v, want none", chunks)
	}

	// A document deleted and stored again after a listing saw it is not the
	// listed document, so removing superseded documents skips it.
	current := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v2", 2000))

	h.passOrphanGracePeriod()

	var third *PublishedDocument

	h.store.FailDelete = func(namespace, key string) error {
		if namespace == NamespacePublished && key == second.ID && third == nil {
			h.store.Drop(NamespacePublished, second.ID)
			third = publishTestDocument(t, h, "topo", data)
		}

		return nil
	}

	removed, err = h.service.DeleteSupersededDocuments(ctx, "topo", current.ID)
	if err != nil {
		t.Fatalf("DeleteSupersededDocuments(topo, %s) returned error: %s", current.ID, fmtErr(err))
	}

	if removed != 0 {
		t.Errorf("DeleteSupersededDocuments(topo, %s) = %d, want the replaced document skipped", current.ID, removed)
	}

	h.store.FailDelete = nil

	if third == nil {
		t.Fatalf("no document was stored again during DeleteSupersededDocuments(topo, %s)", current.ID)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, third.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) of the copy stored after the listing returned error: %s",
			third.ID, fmtErr(err))
	}
}

func TestDeleteSupersededDocuments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	first := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v1", 1200))

	h.passOrphanGracePeriod()

	second := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v2", 1200))
	otherTarget := publishTestDocument(t, h, "other", testRandomDocument(t, "other-v1", 1200))

	// A document this recent may belong to a publication in flight, whose config
	// does not reference it yet.
	inFlight := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v3", 1200))

	removed, err := h.service.DeleteSupersededDocuments(ctx, "topo", second.ID)
	if err != nil {
		t.Fatalf("DeleteSupersededDocuments(topo, %s) returned error: %v", second.ID, err)
	}

	if removed != 1 {
		t.Errorf("DeleteSupersededDocuments(topo, %s) = %d, want 1", second.ID, removed)
	}

	if _, err := h.service.GetPublishedDocument(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPublishedDocument(%s) of the superseded document error = %s, want ErrNotFound", first.ID, fmtErr(err))
	}

	if _, err := h.service.GetPublishedDocument(ctx, second.ID); err != nil {
		t.Errorf("GetPublishedDocument(%s) of the kept document returned error: %v", second.ID, err)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, inFlight.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) of a document within the grace period returned error: %s",
			inFlight.ID, fmtErr(err))
	}

	if _, err := h.service.GetPublishedDocument(ctx, otherTarget.ID); err != nil {
		t.Errorf("GetPublishedDocument(%s) of another target returned error: %v", otherTarget.ID, err)
	}

	if chunks := chunkKeysOf(h, publishedScope(first.ID)); len(chunks) != 0 {
		t.Errorf("chunks of the superseded document = %v, want none", chunks)
	}

	if chunks := chunkKeysOf(h, publishedScope(second.ID)); len(chunks) == 0 {
		t.Error("chunks of the kept document = none, want them kept")
	}

	if _, err := h.service.DeleteSupersededDocuments(ctx, "", second.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteSupersededDocuments(\"\", %s) error = %s, want ErrInvalid", second.ID, fmtErr(err))
	}
}

// TestDeleteTargetDocuments removes every document of a deleted config's
// target, the one just published too, and nothing of any other target.
func TestDeleteTargetDocuments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	old := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v1", 1200))

	h.passOrphanGracePeriod()

	recent := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v2", 1200))
	other := publishTestDocument(t, h, "other", testRandomDocument(t, "other-v1", 1200))

	removed, err := h.service.DeleteTargetDocuments(ctx, "topo")
	if err != nil {
		t.Fatalf("DeleteTargetDocuments(topo) returned error: %s", fmtErr(err))
	}

	if removed != 2 {
		t.Errorf("DeleteTargetDocuments(topo) = %d, want 2", removed)
	}

	for _, doc := range []*PublishedDocument{old, recent} {
		if _, err := h.service.GetPublishedDocument(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetPublishedDocument(%s) error = %s, want ErrNotFound", doc.ID, fmtErr(err))
		}

		if chunks := chunkKeysOf(h, publishedScope(doc.ID)); len(chunks) != 0 {
			t.Errorf("chunks of document %s = %v, want none", doc.ID, chunks)
		}
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, other.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) of another target returned error: %s", other.ID, fmtErr(err))
	}

	if _, err := h.service.DeleteTargetDocuments(ctx, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteTargetDocuments(\"\") error = %s, want ErrInvalid", fmtErr(err))
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
	live := []LiveDocument{{Target: "topo", ID: keep.Reference().StoredID("topo")}}

	// A document is stored before the config that references it, so a recent
	// one may be a publication still in flight.
	if removed, err := h.service.CleanupOrphanedDocuments(ctx, live); err != nil {
		t.Errorf("CleanupOrphanedDocuments of a recent document returned error: %s", fmtErr(err))
	} else if removed != 0 {
		t.Errorf("CleanupOrphanedDocuments of a recent document = %d, want 0", removed)
	}

	h.passOrphanGracePeriod()

	removed, err := h.service.CleanupOrphanedDocuments(ctx, live)
	if err != nil {
		t.Fatalf("CleanupOrphanedDocuments returned error: %v", err)
	}

	if removed != 1 {
		t.Errorf("CleanupOrphanedDocuments = %d, want 1", removed)
	}

	if chunks := chunkKeysOf(h, publishedScope(orphan.ID)); len(chunks) != 0 {
		t.Errorf("chunks of the orphaned document = %v, want none", chunks)
	}

	if _, err := h.service.GetPublishedDocument(ctx, orphan.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPublishedDocument(%s) of the orphaned document error = %s, want ErrNotFound", orphan.ID, fmtErr(err))
	}

	if _, err := h.service.GetPublishedDocument(ctx, keep.ID); err != nil {
		t.Errorf("GetPublishedDocument(%s) of the referenced document returned error: %v", keep.ID, err)
	}
}

// TestCleanupOrphanedDocumentsKeepsOnlyATopologysOwn keeps a document only when
// its own topology names it, by digest alone or by ID.
func TestCleanupOrphanedDocumentsKeepsOnlyATopologysOwn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	byDigest := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))
	superseded := publishTestDocument(t, h, "other", testRandomDocument(t, "other-v1", 1000))
	current := publishTestDocument(t, h, "other", testRandomDocument(t, "other-v2", 1000))

	h.passOrphanGracePeriod()

	live := []LiveDocument{
		// The topology names its document by the digest alone.
		{Target: "topo", ID: DocumentReference{Digest: byDigest.Digest}.StoredID("topo")},
		{Target: "other", ID: current.Reference().StoredID("other")},
		// A third topology names, by ID alone, a document of another one.
		{Target: "copy", ID: DocumentReference{ID: superseded.ID}.StoredID("copy")},
	}

	if removed, err := h.service.CleanupOrphanedDocuments(ctx, live); err != nil {
		t.Errorf("CleanupOrphanedDocuments returned error: %s", fmtErr(err))
	} else if removed != 1 {
		t.Errorf("CleanupOrphanedDocuments = %d, want the document another topology names removed", removed)
	}

	if _, err := h.service.GetPublishedDocument(ctx, superseded.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPublishedDocument(%s) of the superseded document error = %s, want ErrNotFound",
			superseded.ID, fmtErr(err))
	}

	for _, doc := range []*PublishedDocument{byDigest, current} {
		if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
			t.Errorf("GetPublishedDocumentData(%s) of topology %s returned error: %s", doc.ID, doc.Target, fmtErr(err))
		}
	}
}

// TestRepublishedDocumentIsNotAnOrphan publishes an unreferenced document again:
// cleanup keeps it as just published.
func TestRepublishedDocumentIsNotAnOrphan(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	data := testRandomDocument(t, "topo", 1000)

	old := publishTestDocument(t, h, "topo", data)

	h.passOrphanGracePeriod()

	reused := publishTestDocument(t, h, "topo", data)
	if reused.PayloadID != old.PayloadID {
		t.Errorf("PutPublishedDocument(topo) again payload = %s, want the existing %s", reused.PayloadID, old.PayloadID)
	}

	if removed, err := h.service.CleanupOrphanedDocuments(ctx, nil); err != nil {
		t.Errorf("CleanupOrphanedDocuments after the republish returned error: %s", fmtErr(err))
	} else if removed != 0 {
		t.Errorf("CleanupOrphanedDocuments after the republish = %d, want 0", removed)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, reused.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) of the republished document returned error: %v", reused.ID, err)
	}

	// Once it is old again, it is an orphan like any other.
	h.passOrphanGracePeriod()

	if removed, err := h.service.CleanupOrphanedDocuments(ctx, nil); err != nil {
		t.Errorf("CleanupOrphanedDocuments later returned error: %s", fmtErr(err))
	} else if removed != 1 {
		t.Errorf("CleanupOrphanedDocuments later = %d, want 1", removed)
	}
}

func TestCleanupOrphanedChunks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	draft := createTestDraft(t, h, "topo")
	doc := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))

	// Chunks left behind by a crash between writing content and metadata.
	orphanManifest := storePayload(t, h, snapshotScope("vanished-draft", "vanished-snapshot"), []byte(randomText(9, 2000)))

	live := h.store.Count(NamespaceChunks) - len(orphanManifest.ChunkDigests)

	// Chunks are written before the metadata that references them, so recent
	// ones may belong to a write still in flight.
	if removed, err := h.service.CleanupOrphanedChunks(ctx); err != nil {
		t.Errorf("CleanupOrphanedChunks of recent chunks returned error: %s", fmtErr(err))
	} else if removed != 0 {
		t.Errorf("CleanupOrphanedChunks of recent chunks = %d, want 0", removed)
	}

	h.passOrphanGracePeriod()

	// A payload written since is kept while the settled orphan is removed.
	recent := storePayload(t, h, snapshotScope("vanished-draft", "recent-snapshot"), []byte(randomText(10, 500)))

	removed, err := h.service.CleanupOrphanedChunks(ctx)
	if err != nil {
		t.Fatalf("CleanupOrphanedChunks returned error: %v", err)
	}

	if removed != len(orphanManifest.ChunkDigests) {
		t.Errorf("CleanupOrphanedChunks = %d, want %d", removed, len(orphanManifest.ChunkDigests))
	}

	if got := h.store.Count(NamespaceChunks); got != live+len(recent.ChunkDigests) {
		t.Errorf("chunks after CleanupOrphanedChunks = %d, want %d live and %d recent", got, live, len(recent.ChunkDigests))
	}

	if _, err := h.service.GetCurrentDocument(ctx, draft.ID); err != nil {
		t.Errorf("GetCurrentDocument(%s) after chunk cleanup returned error: %v", draft.ID, err)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) after chunk cleanup returned error: %v", doc.ID, err)
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

	h.store.BeforeUpdate = func(namespace, _ string) error {
		if namespace == NamespaceDrafts && !ran {
			ran = true
			removed, cleanupErr = h.service.CleanupOrphanedChunks(ctx)
		}

		return nil
	}

	appendTestSnapshot(t, h, meta, "topo-v2", testActor)

	h.store.BeforeUpdate = nil

	switch {
	case !ran:
		t.Error("CleanupOrphanedChunks did not run during the append")
	case cleanupErr != nil:
		t.Errorf("CleanupOrphanedChunks during the append returned error: %s", fmtErr(cleanupErr))
	case removed != 0:
		t.Errorf("CleanupOrphanedChunks during the append = %d, want 0", removed)
	}

	if _, err := h.service.GetCurrentDocument(ctx, meta.ID); err != nil {
		t.Errorf("GetCurrentDocument(%s) of the appended snapshot returned error: %s", meta.ID, fmtErr(err))
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
				t.Errorf("PutPublishedDocument(%q, %q, %q) error = %s, want ErrInvalid",
					tt.req.Target, tt.req.Kind, tt.req.Actor, fmtErr(err))
			}
		})
	}

	if _, err := h.service.GetPublishedDocument(ctx, "missing-document"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPublishedDocument(missing-document) error = %s, want ErrNotFound", fmtErr(err))
	}
}

func TestMarkPublishedLinksPublishedDocument(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(%s) returned error: %v", meta.ID, err)
	}

	doc, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: snapshot.Data, DraftID: meta.ID, SnapshotID: snapshot.Manifest.ID,
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument(topo) returned error: %v", err)
	}

	published, err := h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		SnapshotID: snapshot.Manifest.ID, Mode: PublishModeTopology,
		TopologyTarget: "topo", TopologyAction: TopologyActionCreate,
		ExperimentTarget: "", ScenarioTarget: "", DocumentID: doc.ID,
	})
	if err != nil {
		t.Fatalf("MarkPublished(%s) returned error: %v", meta.ID, err)
	}

	want := &PublicationState{
		Mode: PublishModeTopology, TopologyTarget: "topo", TopologyAction: TopologyActionCreate,
		SnapshotID: snapshot.Manifest.ID, Digest: snapshot.Manifest.Digest, Revision: meta.Revision,
		DocumentID: doc.ID, PublishedBy: testActor,
	}

	if diff := publicationDiff(want, published.Publication); diff != "" {
		t.Errorf("MarkPublished(%s) publication mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if doc.Digest != snapshot.Manifest.Digest {
		t.Errorf("PutPublishedDocument(topo) digest = %s, want the snapshot digest %s", doc.Digest, snapshot.Manifest.Digest)
	}

	if published.Dirty() {
		t.Errorf("MarkPublished(%s) Dirty() = true, want false right after publishing the current snapshot", meta.ID)
	}
}
