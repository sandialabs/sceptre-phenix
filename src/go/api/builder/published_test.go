package builder

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
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
// leaving out what the test cannot know: the compressed size, the time and
// the revision.
func publishedDiff(want, got *PublishedDocument) string {
	return cmp.Diff(want, got, cmpopts.IgnoreUnexported(PublishedDocument{}), cmpopts.IgnoreFields(
		PublishedDocument{}, "CompressedSize", "CreatedAt", "Revision",
	))
}

// publishedTestDocument is what publishTestDocument stores for data.
func publishedTestDocument(target string, data []byte) *PublishedDocument {
	return &PublishedDocument{
		ID: PublishedDocumentID(target, digestOf(data)), Digest: digestOf(data), Size: int64(len(data)),
		Target: target, Kind: "Topology", DraftID: "draft-1", SnapshotID: "snap-1",
		CreatedBy: testActor, Schema: builder.SchemaURI,
	}
}

func TestPublishedDocumentRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 4000)
	doc := publishTestDocument(t, h, "topo", data)

	if diff := publishedDiff(publishedTestDocument("topo", data), doc); diff != "" {
		t.Errorf("PutPublishedDocument(topo) mismatch (-want +got):\n%s", diff)
	}

	if keys := contentKeysOf(h, publishedKey(doc.ID)); len(keys) != 1 {
		t.Errorf("PutPublishedDocument(topo) content records = %q, want one", keys)
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
	if diff := publishedDiff(publishedTestDocument("topo", data), stored); diff != "" {
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
	content := h.store.Count(NamespaceContent)

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

	if got := h.store.Count(NamespaceContent); got != content {
		t.Errorf("PutPublishedDocument(topo) of the same content left %d content records, want the %d already stored", got, content)
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

	h.store.Drop(NamespaceContent, publishedKey(doc.ID))

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); !errors.Is(err, ErrCorrupt) {
		t.Errorf("GetPublishedDocumentData(%s) with missing content error = %s, want ErrCorrupt", doc.ID, fmtErr(err))
	}

	h.store.Drop(NamespacePublished, doc.ID)

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPublishedDocumentData(%s) of a deleted document error = %s, want ErrNotFound", doc.ID, fmtErr(err))
	}
}

// TestPutPublishedDocumentStoresNothingWhenRefused asserts that a published
// document write the store refused leaves no content behind.
func TestPutPublishedDocumentStoresNothingWhenRefused(t *testing.T) {
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

	if keys := contentKeysOf(h, publishedKey(otherID)); len(keys) != 0 {
		t.Errorf("PutPublishedDocument(other) left content %v, want none", keys)
	}
}

func TestPutPublishedDocumentRepairsCorruptContent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 2000)
	doc := publishTestDocument(t, h, "topo", data)

	h.store.Drop(NamespaceContent, publishedKey(doc.ID))

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); !errors.Is(err, ErrCorrupt) {
		t.Errorf("GetPublishedDocumentData(%s) with lost content error = %s, want ErrCorrupt", doc.ID, fmtErr(err))
	}

	// Publishing the same content again replaces the corrupt copy instead of
	// returning it.
	repaired := publishTestDocument(t, h, "topo", data)

	if repaired.ID != doc.ID {
		t.Errorf("PutPublishedDocument(topo) of corrupt content ID = %s, want %s", repaired.ID, doc.ID)
	}

	if repaired.Revision <= doc.Revision {
		t.Errorf("PutPublishedDocument(topo) of corrupt content revision = %d, want more than %d", repaired.Revision, doc.Revision)
	}

	_, verified, err := h.service.GetPublishedDocumentData(ctx, repaired.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData(%s) of the repaired document returned error: %s", repaired.ID, fmtErr(err))
	}

	if diff := cmp.Diff(string(data), string(verified)); diff != "" {
		t.Errorf("GetPublishedDocumentData(%s) of the repaired document mismatch (-want +got):\n%s", repaired.ID, diff)
	}

	if keys := contentKeysOf(h, publishedKey(doc.ID)); len(keys) != 1 {
		t.Errorf("content of the repaired document = %q, want one record", keys)
	}
}

func TestDeletingPublishedDocumentKeepsConcurrentCopies(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testRandomDocument(t, "topo", 2000)
	first := publishTestDocument(t, h, "topo", data)

	// Another process publishes the same content again while the delete
	// runs. The delete then finds the document stored again, and keeps it.
	var second *PublishedDocument

	h.store.FailDelete = func(namespace, _ string) error {
		if namespace == NamespacePublished && second == nil {
			second = publishTestDocument(t, h, "topo", data)
		}

		return nil
	}

	removed, err := h.service.DeleteTargetDocuments(ctx, "topo")
	if err != nil {
		t.Fatalf("DeleteTargetDocuments(topo) returned error: %s", fmtErr(err))
	}

	if removed != 0 {
		t.Errorf("DeleteTargetDocuments(topo) = %d, want the document published again kept", removed)
	}

	h.store.FailDelete = nil

	if second == nil {
		t.Fatal("no document was published again during DeleteTargetDocuments(topo)")
	}

	if second.Revision <= first.Revision {
		t.Errorf("republished document revision = %d, want more than %d", second.Revision, first.Revision)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, second.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) of the copy published during the delete returned error: %s",
			second.ID, fmtErr(err))
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

	if keys := contentKeysOf(h, publishedKey(first.ID)); len(keys) != 0 {
		t.Errorf("content of the superseded document = %v, want none", keys)
	}

	if keys := contentKeysOf(h, publishedKey(second.ID)); len(keys) != 1 {
		t.Errorf("content of the kept document = %q, want it kept", keys)
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

		if keys := contentKeysOf(h, publishedKey(doc.ID)); len(keys) != 0 {
			t.Errorf("content of document %s = %v, want none", doc.ID, keys)
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
	if removed, err := h.service.CleanupOrphanedDocuments(ctx, live, false); err != nil {
		t.Errorf("CleanupOrphanedDocuments of a recent document returned error: %s", fmtErr(err))
	} else if removed != 0 {
		t.Errorf("CleanupOrphanedDocuments of a recent document = %d, want 0", removed)
	}

	h.passOrphanGracePeriod()

	removed, err := h.service.CleanupOrphanedDocuments(ctx, live, false)
	if err != nil {
		t.Fatalf("CleanupOrphanedDocuments returned error: %v", err)
	}

	if removed != 1 {
		t.Errorf("CleanupOrphanedDocuments = %d, want 1", removed)
	}

	if keys := contentKeysOf(h, publishedKey(orphan.ID)); len(keys) != 0 {
		t.Errorf("content of the orphaned document = %v, want none", keys)
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

	if removed, err := h.service.CleanupOrphanedDocuments(ctx, live, false); err != nil {
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
	if !reused.CreatedAt.Equal(old.CreatedAt) {
		t.Errorf("PutPublishedDocument(topo) again created at %v, want the existing document of %v", reused.CreatedAt, old.CreatedAt)
	}

	if removed, err := h.service.CleanupOrphanedDocuments(ctx, nil, false); err != nil {
		t.Errorf("CleanupOrphanedDocuments after the republish returned error: %s", fmtErr(err))
	} else if removed != 0 {
		t.Errorf("CleanupOrphanedDocuments after the republish = %d, want 0", removed)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, reused.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) of the republished document returned error: %v", reused.ID, err)
	}

	// Once it is old again, it is an orphan like any other.
	h.passOrphanGracePeriod()

	if removed, err := h.service.CleanupOrphanedDocuments(ctx, nil, false); err != nil {
		t.Errorf("CleanupOrphanedDocuments later returned error: %s", fmtErr(err))
	} else if removed != 1 {
		t.Errorf("CleanupOrphanedDocuments later = %d, want 1", removed)
	}
}

func TestCleanupOrphanedContent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	draft := createTestDraft(t, h, "topo")
	doc := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))

	// Content that no metadata names, as an earlier version or a failure
	// of the store could leave.
	storePayload(t, h, snapshotKey("vanished-draft", "vanished-snapshot"), []byte(randomText(9, 2000)))
	storePayload(t, h, publishedKey("vanished-document"), []byte(randomText(13, 2000)))

	live := h.store.Count(NamespaceContent) - 2

	// A cleanup reads the metadata before the content, so recent content may
	// belong to a save that came between the two reads.
	if removed, err := h.service.CleanupOrphanedContent(ctx, false); err != nil {
		t.Errorf("CleanupOrphanedContent of recent content returned error: %s", fmtErr(err))
	} else if removed != 0 {
		t.Errorf("CleanupOrphanedContent of recent content = %d, want 0", removed)
	}

	h.passOrphanGracePeriod()

	// Content written since is kept while the settled orphans are removed.
	storePayload(t, h, snapshotKey("vanished-draft", "recent-snapshot"), []byte(randomText(10, 500)))

	if counted, err := h.service.CleanupOrphanedContent(ctx, true); err != nil || counted != 2 {
		t.Errorf("CleanupOrphanedContent(dry run) = %d, %s, want 2 and no error", counted, fmtErr(err))
	}

	if got := h.store.Count(NamespaceContent); got != live+3 {
		t.Errorf("content after a dry run = %d records, want all %d kept", got, live+3)
	}

	removed, err := h.service.CleanupOrphanedContent(ctx, false)
	if err != nil {
		t.Fatalf("CleanupOrphanedContent returned error: %v", err)
	}

	if removed != 2 {
		t.Errorf("CleanupOrphanedContent = %d, want 2", removed)
	}

	if got := h.store.Count(NamespaceContent); got != live+1 {
		t.Errorf("content after CleanupOrphanedContent = %d records, want %d live and 1 recent", got, live)
	}

	if _, err := h.service.GetCurrentDocument(ctx, draft.ID); err != nil {
		t.Errorf("GetCurrentDocument(%s) after the cleanup returned error: %v", draft.ID, err)
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) after the cleanup returned error: %v", doc.ID, err)
	}
}

// TestCleanupOrphansSkipsADamagedDocument stores a published record whose
// metadata does not decode. The cleanups remove the other orphans, keep the
// damaged record and its content, and report the damaged record.
func TestCleanupOrphansSkipsADamagedDocument(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	orphan := publishTestDocument(t, h, "gone", testRandomDocument(t, "gone", 1000))

	const damaged = "damaged-document"

	if _, err := h.store.CreateRecord(NamespacePublished, damaged, []byte("not metadata")); err != nil {
		t.Fatalf("CreateRecord(%s) returned error: %v", damaged, err)
	}

	storePayload(t, h, publishedKey(damaged), []byte(randomText(11, 500)))

	h.passOrphanGracePeriod()

	removed, err := h.service.CleanupOrphanedDocuments(ctx, nil, false)
	if !errors.Is(err, ErrCleanup) || !errors.Is(err, ErrCorrupt) {
		t.Errorf("CleanupOrphanedDocuments error = %s, want ErrCleanup with the damaged record", fmtErr(err))
	}

	if removed != 1 {
		t.Errorf("CleanupOrphanedDocuments = %d, want the readable orphan %s removed", removed, orphan.ID)
	}

	if removed, err := h.service.CleanupOrphanedContent(ctx, false); err != nil {
		t.Errorf("CleanupOrphanedContent returned error: %s", fmtErr(err))
	} else if removed != 0 {
		t.Errorf("CleanupOrphanedContent = %d, want the content of %s kept", removed, damaged)
	}

	if diff := cmp.Diff([]string{damaged}, h.store.Keys(NamespacePublished)); diff != "" {
		t.Errorf("published records after the cleanups mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{publishedKey(damaged)}, h.store.Keys(NamespaceContent)); diff != "" {
		t.Errorf("content after the cleanups mismatch (-want +got):\n%s", diff)
	}
}

// TestDamagedDocumentDoesNotBlockListing stores a published record whose
// metadata does not validate. The listing and the delete helpers skip it, and
// DeleteDamagedDocument removes it and its content.
func TestDamagedDocumentDoesNotBlockListing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	readable := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))

	const damaged = "damaged-document"

	record, err := h.store.CreateRecord(NamespacePublished, damaged, []byte(`{"kind":"Topology","target":"topo"}`))
	if err != nil {
		t.Fatalf("CreateRecord(%s) returned error: %v", damaged, err)
	}

	storePayload(t, h, publishedKey(damaged), []byte(randomText(11, 500)))

	docs, err := h.service.ListPublishedDocuments(ctx)
	if err != nil {
		t.Fatalf("ListPublishedDocuments returned error: %s", fmtErr(err))
	}

	if len(docs) != 1 || docs[0].ID != readable.ID {
		t.Errorf("ListPublishedDocuments = %v, want only %s", docs, readable.ID)
	}

	got, err := h.service.GetDamagedDocument(ctx, damaged)
	if err != nil {
		t.Fatalf("GetDamagedDocument(%s) returned error: %s", damaged, fmtErr(err))
	}

	want := &DamagedDocument{ID: damaged, Kind: "Topology", Target: "topo", Revision: record.Revision}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetDamagedDocument(%s) mismatch (-want +got):\n%s", damaged, diff)
	}

	if _, err := h.service.GetDamagedDocument(ctx, readable.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("GetDamagedDocument(%s) of a readable document error = %s, want ErrInvalid", readable.ID, fmtErr(err))
	}

	if err := h.service.DeleteDamagedDocument(ctx, readable.ID, readable.Revision); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteDamagedDocument(%s) of a readable document error = %s, want ErrInvalid", readable.ID, fmtErr(err))
	}

	if err := h.service.DeleteDamagedDocument(ctx, damaged, record.Revision+1); !errors.Is(err, ErrConflict) {
		t.Errorf("DeleteDamagedDocument(%s) at another revision error = %s, want ErrConflict", damaged, fmtErr(err))
	}

	if removed, err := h.service.DeleteTargetDocuments(ctx, "topo"); err != nil || removed != 1 {
		t.Errorf("DeleteTargetDocuments(topo) = %d, %s, want 1 and no error", removed, fmtErr(err))
	}

	if err := h.service.DeleteDamagedDocument(ctx, damaged, record.Revision); err != nil {
		t.Errorf("DeleteDamagedDocument(%s) returned error: %s", damaged, fmtErr(err))
	}

	if keys := h.store.Keys(NamespacePublished); len(keys) != 0 {
		t.Errorf("published records after the deletes = %v, want none", keys)
	}

	if keys := h.store.Keys(NamespaceContent); len(keys) != 0 {
		t.Errorf("content after the deletes = %v, want none", keys)
	}
}

// TestGetDamagedDocumentFields stores damaged published records. GetDamagedDocument
// keeps the kind and target only when each is valid text within its limit.
func TestGetDamagedDocumentFields(t *testing.T) {
	longKind := strings.Repeat("k", MaxKindLength+1)
	longTarget := strings.Repeat("t", MaxTargetLength+1)

	tests := []struct {
		name, value, kind, target string
	}{
		{name: "kind and target", value: `{"kind":"Topology","target":"topo"}`, kind: "Topology", target: "topo"},
		{
			name:  "kind at the limit",
			value: `{"kind":"` + strings.Repeat("k", MaxKindLength) + `","target":"topo"}`,
			kind:  strings.Repeat("k", MaxKindLength), target: "topo",
		},
		{name: "kind over the limit", value: `{"kind":"` + longKind + `","target":"topo"}`},
		{name: "target over the limit", value: `{"kind":"Topology","target":"` + longTarget + `"}`},
		{name: "not JSON", value: `not metadata`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)

			record, err := h.store.CreateRecord(NamespacePublished, "damaged", []byte(tt.value))
			if err != nil {
				t.Fatalf("CreateRecord(damaged) returned error: %v", err)
			}

			got, err := h.service.GetDamagedDocument(context.Background(), "damaged")
			if err != nil {
				t.Fatalf("GetDamagedDocument(%s) returned error: %s", tt.value, fmtErr(err))
			}

			want := &DamagedDocument{ID: "damaged", Kind: tt.kind, Target: tt.target, Revision: record.Revision}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("GetDamagedDocument(%s) mismatch (-want +got):\n%s", tt.value, diff)
			}
		})
	}
}

// getCountingStore counts the records read outside a transaction.
type getCountingStore struct {
	*memrecord.Store

	gets int
}

func (s *getCountingStore) GetRecord(namespace, key string) (store.Record, error) {
	s.gets++

	return s.Store.GetRecord(namespace, key) //nolint:wrapcheck // the wrapped store's answer
}

// TestDeleteDamagedDocumentChecksInItsTransaction deletes damaged and readable
// records. DeleteDamagedDocument reads each record only in its transaction,
// and checks the revision before it checks the damage.
func TestDeleteDamagedDocumentChecksInItsTransaction(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	counting := &getCountingStore{Store: h.store, gets: 0}

	service, err := New(WithStore(counting), WithClock(h.service.clock), WithIDSource(h.service.newID))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	readable := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))

	if err := service.DeleteDamagedDocument(ctx, readable.ID, readable.Revision+1); !errors.Is(err, ErrConflict) {
		t.Errorf("DeleteDamagedDocument(%s) of a readable document at another revision error = %s, want ErrConflict",
			readable.ID, fmtErr(err))
	}

	record, err := h.store.CreateRecord(NamespacePublished, "damaged", []byte(`not metadata`))
	if err != nil {
		t.Fatalf("CreateRecord(damaged) returned error: %v", err)
	}

	counting.gets = 0

	if err := service.DeleteDamagedDocument(ctx, "damaged", record.Revision); err != nil {
		t.Fatalf("DeleteDamagedDocument(damaged) returned error: %s", fmtErr(err))
	}

	if counting.gets != 0 {
		t.Errorf("DeleteDamagedDocument(damaged) made %d reads outside its transaction, want 0", counting.gets)
	}
}

// keyListHookStore runs a hook before each listing of keys, so a test can
// put a write between the reads of a cleanup.
type keyListHookStore struct {
	*memrecord.Store

	beforeKeys func(namespace string)
}

func (s *keyListHookStore) ListRecordKeys(namespace, prefix string) ([]string, error) {
	if s.beforeKeys != nil {
		s.beforeKeys(namespace)
	}

	return s.Store.ListRecordKeys(namespace, prefix) //nolint:wrapcheck // the wrapped store's answer
}

// TestCleanupOrphanedContentKeepsASaveBetweenItsReads saves a snapshot after
// the cleanup read the drafts and before it read the content. The new
// content is in no draft the cleanup read, and the grace period keeps it.
func TestCleanupOrphanedContentKeepsASaveBetweenItsReads(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	hooked := &keyListHookStore{Store: h.store, beforeKeys: nil}

	service, err := New(WithStore(hooked), WithClock(h.service.clock), WithIDSource(h.service.newID))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	meta := createTestDraft(t, h, "topo")

	h.passOrphanGracePeriod()

	var saved bool

	hooked.beforeKeys = func(namespace string) {
		if namespace == NamespaceContent && !saved {
			saved = true
			appendTestSnapshot(t, h, meta, "topo-v2", testActor)
		}
	}

	removed, err := service.CleanupOrphanedContent(ctx, false)

	switch {
	case !saved:
		t.Error("no snapshot was saved during CleanupOrphanedContent")
	case err != nil:
		t.Errorf("CleanupOrphanedContent during a save returned error: %s", fmtErr(err))
	case removed != 0:
		t.Errorf("CleanupOrphanedContent during a save = %d, want 0", removed)
	}

	if _, err := h.service.GetCurrentDocument(ctx, meta.ID); err != nil {
		t.Errorf("GetCurrentDocument(%s) of the saved snapshot returned error: %s", meta.ID, fmtErr(err))
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
