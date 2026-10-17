package builder

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"

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

// historyState is what a history edit leaves in a draft's metadata.
type historyState struct {
	History        []string
	Cursor         int
	Current        string
	CanRedo        bool
	Publication    string
	Dirty          bool
	LastModifiedBy string
}

func historyStateOf(meta *DraftMetadata) historyState {
	state := historyState{
		History: make([]string, 0, len(meta.History)), Cursor: meta.Cursor, Current: "", CanRedo: meta.CanRedo(),
		Publication: "", Dirty: meta.Dirty(), LastModifiedBy: meta.LastModifiedBy,
	}

	for i := range meta.History {
		state.History = append(state.History, meta.History[i].ID)
	}

	if current := meta.Current(); current != nil {
		state.Current = current.ID
	}

	if meta.Publication != nil {
		state.Publication = meta.Publication.SnapshotID
	}

	return state
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
		t.Errorf("PutPublishedDocument(snapshot bytes).Digest = %s, want the snapshot's %s", published.Digest, saved.Manifest.Digest)
	}

	meta = appendTestSnapshot(t, h, meta, "topo-v1", testActor)
	meta = appendTestSnapshot(t, h, meta, "topo-v3", testActor)

	meta, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, Index: 2, SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	if len(meta.History) != 4 {
		t.Fatalf("MoveCursor returned a history of %d snapshots, want 4", len(meta.History))
	}

	first, v1, same, v3 := meta.History[0], meta.History[1], meta.History[2], meta.History[3]

	// Each save is stamped with its own time, so two saves of the same
	// content are two documents.
	if v1.Digest == same.Digest {
		t.Errorf("digest of the second save of the same content = %s, want it to differ from the first because it is stamped", same.Digest)
	}

	again, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(draft) at the second save returned error: %s", fmtErr(err))
	}

	if diff := contentDiff(t, saved.Data, again.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) at the second save content mismatch with the first (-want +got):\n%s", diff)
	}

	// A snapshot before the cursor: the cursor keeps pointing at the same
	// snapshot, and the publication naming the deleted snapshot is kept.
	meta = deleteTestSnapshot(t, h, meta, v1.ID, testPeer)

	want := historyState{
		History: []string{first.ID, same.ID, v3.ID}, Cursor: 1, Current: same.ID, CanRedo: true,
		Publication: v1.ID, Dirty: true, LastModifiedBy: testPeer,
	}
	if diff := cmp.Diff(want, historyStateOf(meta)); diff != "" {
		t.Errorf("DeleteSnapshot(before the cursor) mismatch (-want +got):\n%s", diff)
	}

	if chunks := chunkKeysOf(h, snapshotScope(meta.ID, v1.ID)); len(chunks) != 0 {
		t.Errorf("chunks of the deleted snapshot = %v, want none", chunks)
	}

	if _, err := h.service.GetSnapshot(ctx, meta.ID, v1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSnapshot(deleted snapshot) error = %s, want ErrNotFound", fmtErr(err))
	}

	// Chunks are never shared: the snapshot and the published document of
	// the same content keep theirs.
	if _, err := h.service.GetSnapshot(ctx, meta.ID, same.ID); err != nil {
		t.Errorf("GetSnapshot(snapshot of the same content) returned error: %s", fmtErr(err))
	}

	if _, _, err := h.service.GetPublishedDocumentData(ctx, published.ID); err != nil {
		t.Errorf("GetPublishedDocumentData(published) returned error: %s", fmtErr(err))
	}

	// A snapshot after the cursor, in the redo branch.
	meta = deleteTestSnapshot(t, h, meta, v3.ID, testActor)

	want = historyState{
		History: []string{first.ID, same.ID}, Cursor: 1, Current: same.ID, CanRedo: false,
		Publication: v1.ID, Dirty: true, LastModifiedBy: testActor,
	}
	if diff := cmp.Diff(want, historyStateOf(meta)); diff != "" {
		t.Errorf("DeleteSnapshot(after the cursor) mismatch (-want +got):\n%s", diff)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %s", fmtErr(err))
	}

	if diff := cmp.Diff(meta, stored); diff != "" {
		t.Errorf("GetDraft mismatch with the draft DeleteSnapshot returned (-want +got):\n%s", diff)
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
				t.Errorf("DeleteSnapshot(%s) error = %s, want %v", tt.name, fmtErr(err), tt.want)
			}
		})
	}

	if stored, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Errorf("GetDraft after refused deletes returned error: %s", fmtErr(err))
	} else if stored.Revision != meta.Revision {
		t.Errorf("GetDraft after refused deletes revision = %d, want %d of the untouched draft", stored.Revision, meta.Revision)
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
	if !errors.Is(err, ErrCleanup) {
		t.Errorf("DeleteSnapshot(chunk deletes fail) error = %s, want ErrCleanup", fmtErr(err))
	}

	if updated == nil {
		t.Fatal("DeleteSnapshot(chunk deletes fail) = nil, want the updated draft")
	}

	if len(updated.History) != 1 {
		t.Errorf("DeleteSnapshot(chunk deletes fail) history length = %d, want 1", len(updated.History))
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
		t.Errorf("encodeDraft(%d snapshots of 256 chunks) error = %s, want ErrTooLarge for metadata past %d bytes",
			MaxSnapshots, fmtErr(err), MaxMetadataBytes)
	}
}
