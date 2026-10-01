package builder

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"phenix/store/recordtest/memrecord"
)

// bigDocumentPadding sizes a test document just under [MaxDocumentBytes], so
// ten of them fit inside [MaxDraftHistoryBytes] and eleven do not. The padding
// compresses well, which keeps the test fast without changing the uncompressed
// sizes the byte limit is enforced on.
const bigDocumentPadding = MaxDocumentBytes - 4096

func TestHistoryByteLimitPrunesTheOldestSnapshots(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
		Document: testDocument(t, "v0", bigDocumentPadding), Summary: "v0", ID: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	// Ten ~5 MiB snapshots fit inside the 50 MiB history limit.
	for i := 1; i < 10; i++ {
		meta, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
			DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
			Document: testDocument(t, "v"+strconv.Itoa(i), bigDocumentPadding), Summary: "v" + strconv.Itoa(i),
		})
		if err != nil {
			t.Fatalf("AppendSnapshot %d returned error: %v", i, err)
		}
	}

	if len(meta.History) != 10 {
		t.Fatalf("history length = %d, want 10", len(meta.History))
	}

	oldest := meta.History[0]

	// The eleventh would exceed the byte limit, so the oldest snapshot makes
	// room for it, the same way the snapshot count limit prunes.
	meta, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, "v10", bigDocumentPadding), Summary: "v10",
	})
	if err != nil {
		t.Fatalf("AppendSnapshot past the byte limit returned error: %v", err)
	}

	switch {
	case len(meta.History) != 10:
		t.Fatalf("history length = %d, want 10", len(meta.History))
	case meta.History[0].Summary != "v1":
		t.Fatalf("oldest snapshot = %q, want v1 once v0 is pruned", meta.History[0].Summary)
	case meta.Current().Summary != "v10":
		t.Fatalf("current snapshot = %q, want the new v10", meta.Current().Summary)
	case meta.HistoryBytes() > MaxDraftHistoryBytes:
		t.Fatalf("history bytes = %d, want at most %d", meta.HistoryBytes(), int64(MaxDraftHistoryBytes))
	}

	// The pruned snapshot and its chunks are gone; the new one is readable.
	if _, err := h.service.GetSnapshot(ctx, meta.ID, oldest.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSnapshot of the pruned snapshot error = %s, want ErrNotFound", fmtErr(err))
	}

	if got := chunkKeysOf(h, snapshotScope(meta.ID, oldest.ID)); len(got) != 0 {
		t.Fatalf("chunks of the pruned snapshot = %v, want none", got)
	}

	if _, err := h.service.GetCurrentDocument(ctx, meta.ID); err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}
}

// deleteTestSnapshot deletes the snapshot of a draft as actor, failing the
// test on error.
func deleteTestSnapshot(t *testing.T, h *testHarness, meta *DraftMetadata, snapshotID, actor string) *DraftMetadata {
	t.Helper()

	updated, err := h.service.DeleteSnapshot(context.Background(), DeleteSnapshotRequest{
		DraftID: meta.ID, Actor: actor, ExpectedRevision: meta.Revision, SnapshotID: snapshotID,
	})
	if err != nil {
		t.Fatalf("DeleteSnapshot(%s) returned error: %s", snapshotID, fmtErr(err))
	}

	return updated
}

// TestDeleteSnapshot deletes snapshots before and after the cursor of a draft
// whose history holds two snapshots of the same content, one of them
// published.
func TestDeleteSnapshot(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	meta = appendTestSnapshot(t, h, meta, "topo-v1", testActor)

	meta, err := h.service.MarkPublished(ctx, markPublishedRequest(meta, meta.Revision))
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	published := publishTestDocument(t, h, "topo", testDocument(t, "topo-v1", 0))

	meta = appendTestSnapshot(t, h, meta, "topo-v1", testActor)
	meta = appendTestSnapshot(t, h, meta, "topo-v3", testActor)

	meta, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, Index: 2, SnapshotID: "", UseIndex: true,
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	v1, same, v3 := meta.History[1], meta.History[2], meta.History[3]
	if v1.Digest != same.Digest {
		t.Fatal("the two snapshots of the same content should share a digest")
	}

	// A snapshot before the cursor: the cursor keeps pointing at the same
	// snapshot, and the publication naming the deleted snapshot is kept.
	meta = deleteTestSnapshot(t, h, meta, v1.ID, testPeer)

	switch {
	case len(meta.History) != 3 || meta.Cursor != 1 || meta.Current().ID != same.ID:
		t.Fatalf("history %d, cursor %d at %s; want 3 and 1 at %s", len(meta.History), meta.Cursor, meta.Current().ID, same.ID)
	case meta.Publication == nil || meta.Publication.SnapshotID != v1.ID || !meta.Dirty():
		t.Fatalf("publication = %+v, dirty %t; want it kept and the draft dirty", meta.Publication, meta.Dirty())
	case meta.LastModifiedBy != testPeer:
		t.Fatalf("last modified by %q, want %q", meta.LastModifiedBy, testPeer)
	}

	if chunks := chunkKeysOf(h, snapshotScope(meta.ID, v1.ID)); len(chunks) != 0 {
		t.Fatalf("chunks of the deleted snapshot = %v, want none", chunks)
	}

	if _, err := h.service.GetSnapshot(ctx, meta.ID, v1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSnapshot of the deleted snapshot error = %s, want ErrNotFound", fmtErr(err))
	}

	// Chunks are never shared: the snapshot and the published document of
	// the same content keep theirs.
	if _, err := h.service.GetSnapshot(ctx, meta.ID, same.ID); err != nil {
		t.Fatalf("GetSnapshot of the same content returned error: %s", fmtErr(err))
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, published.ID); err != nil {
		t.Fatalf("GetPublishedDocumentData returned error: %s", fmtErr(err))
	}

	// A snapshot after the cursor, in the redo branch.
	meta = deleteTestSnapshot(t, h, meta, v3.ID, testActor)

	if len(meta.History) != 2 || meta.Cursor != 1 || meta.Current().ID != same.ID || meta.CanRedo() {
		t.Fatalf("history %d, cursor %d at %s; want 2 and 1 at %s", len(meta.History), meta.Cursor, meta.Current().ID, same.ID)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil || stored.Revision != meta.Revision || stored.Publication == nil {
		t.Fatalf("GetDraft = %+v, %s; want the returned draft", stored, fmtErr(err))
	}
}

func TestDeleteSnapshotRefusals(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	meta = appendTestSnapshot(t, h, meta, "topo-v1", testActor)
	current := meta.Current().ID

	for _, tt := range []struct {
		name     string
		snapshot string
		revision int64
		want     error
	}{
		{name: "current snapshot", snapshot: current, revision: meta.Revision, want: ErrConflict},
		{name: "unknown snapshot", snapshot: "id-999", revision: meta.Revision, want: ErrNotFound},
		{name: "stale revision", snapshot: meta.History[0].ID, revision: meta.Revision - 1, want: ErrConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.service.DeleteSnapshot(ctx, DeleteSnapshotRequest{
				DraftID: meta.ID, Actor: testActor, ExpectedRevision: tt.revision, SnapshotID: tt.snapshot,
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("DeleteSnapshot error = %s, want %v", fmtErr(err), tt.want)
			}
		})
	}

	if stored, err := h.service.GetDraft(ctx, meta.ID); err != nil || stored.Revision != meta.Revision {
		t.Fatalf("GetDraft = %+v, %s; want the draft untouched", stored, fmtErr(err))
	}

	// Removing the chunks after the metadata write is only a cleanup failure.
	h.store.FailDelete = func(namespace, _ string) error {
		if namespace == NamespaceChunks {
			return errors.New("chunk store is unavailable")
		}

		return nil
	}

	updated, err := h.service.DeleteSnapshot(ctx, DeleteSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, SnapshotID: meta.History[0].ID,
	})
	if !errors.Is(err, ErrCleanup) || updated == nil || len(updated.History) != 1 {
		t.Fatalf("DeleteSnapshot = %+v, %s; want the updated draft and ErrCleanup", updated, fmtErr(err))
	}
}

func TestCreateDraftRejectsMetadataOverLimit(t *testing.T) {
	h := newHarness(t)

	meta := &DraftMetadata{
		ID: "draft", Owner: testOwner, Title: "", SourceToken: "",
		Created: memrecord.Time(1), Updated: memrecord.Time(1), LastModifiedBy: testActor,
		History: make([]SnapshotManifest, 0, maxStoredSnapshots), Cursor: 0, Publication: nil, Revision: 0,
	}

	digest := digestOf([]byte("chunk"))

	for i := range maxStoredSnapshots {
		digests := make([]string, 0, 128)
		for range 128 {
			digests = append(digests, digest)
		}

		meta.History = append(meta.History, SnapshotManifest{
			ID: "snap-" + strconv.Itoa(i), Digest: digest, Size: 1, CompressedSize: 1,
			ChunkDigests: digests, ChunkSize: h.service.chunkSize,
			CreatedAt: memrecord.Time(1), CreatedBy: testActor, Summary: "",
		})
	}

	if _, err := encodeDraft(meta); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("encodeDraft error = %s, want ErrTooLarge for metadata past %d bytes", fmtErr(err), MaxMetadataBytes)
	}
}
