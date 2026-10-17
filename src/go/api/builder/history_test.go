package builder

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"phenix/store/recordtest/memrecord"
)

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

// TestDeleteSnapshot deletes snapshots before and after the cursor, in a history
// with two snapshots of the same content, one of them published.
func TestDeleteSnapshot(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	meta = appendTestSnapshot(t, h, meta, "topo-v1", testActor)

	meta, err := h.service.MarkPublished(ctx, markPublishedRequest(meta, meta.Revision))
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	// The published document is the snapshot's own bytes, as a publication
	// stores them.
	saved, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	published := publishTestDocument(t, h, "topo", saved.Data)

	if published.Digest != saved.Manifest.Digest {
		t.Fatalf("published digest = %s, want the snapshot's %s", published.Digest, saved.Manifest.Digest)
	}

	meta = appendTestSnapshot(t, h, meta, "topo-v1", testActor)
	meta = appendTestSnapshot(t, h, meta, "topo-v3", testActor)

	meta, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, Index: 2, SnapshotID: "", UseIndex: true,
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	v1, same, v3 := meta.History[1], meta.History[2], meta.History[3]

	// Each save is stamped with its own time, so two saves of the same
	// content are two documents.
	if v1.Digest == same.Digest {
		t.Fatal("the two saves of the same content share a digest, so the second was not stamped")
	}

	again, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil || !sameContent(t, again.Data, saved.Data) {
		t.Fatalf("the second save does not hold the content of the first: %s", fmtErr(err))
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
		ID: "draft", CreatedBy: testActor, Title: "", SourceToken: "",
		Created: memrecord.Time(1), Updated: memrecord.Time(1), LastModifiedBy: testActor,
		History: make([]SnapshotManifest, 0, MaxSnapshots), Cursor: 0, Publication: nil, Revision: 0,
	}

	digest := digestOf([]byte("chunk"))

	for i := range MaxSnapshots {
		digests := make([]string, 0, 256)
		for range 256 {
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
