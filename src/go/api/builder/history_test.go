package builder

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

// bigDocumentPadding sizes a test document just under [MaxDocumentBytes], so
// ten of them fit inside [MaxDraftHistoryBytes] and eleven do not. The padding
// compresses well, which keeps the test fast without changing the uncompressed
// sizes the byte limit is enforced on.
const bigDocumentPadding = MaxDocumentBytes - 4096

func TestHistoryByteLimitRejectsTheEditInsteadOfPruning(t *testing.T) {
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
	chunks := h.store.count(NamespaceChunks)

	// The eleventh would exceed the byte limit, so it is rejected outright.
	_, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, "v10", bigDocumentPadding), Summary: "v10",
	})

	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("AppendSnapshot error = %s, want *TooLargeError", fmtErr(err))
	}

	if tooLarge.Limit != MaxDraftHistoryBytes {
		t.Fatalf("limit = %d, want the draft history limit %d", tooLarge.Limit, int64(MaxDraftHistoryBytes))
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	switch {
	case len(stored.History) != 10:
		t.Fatalf("history length = %d, want the retained history untouched", len(stored.History))
	case stored.History[0].ID != oldest.ID:
		t.Fatalf("oldest snapshot = %q, want %q: history must never be dropped to make room", stored.History[0].ID, oldest.ID)
	case stored.Revision != meta.Revision:
		t.Fatalf("revision = %d, want the rejected edit to leave metadata alone", stored.Revision)
	case h.store.count(NamespaceChunks) != chunks:
		t.Fatal("a rejected edit must not leave chunks behind")
	}

	// The oldest snapshot is still readable, proving nothing was pruned.
	if _, err := h.service.GetSnapshot(ctx, meta.ID, oldest.ID); err != nil {
		t.Fatalf("GetSnapshot of the oldest snapshot returned error: %v", err)
	}

	// Undoing and branching from an earlier point frees history, so an edit that
	// fits is accepted again.
	moved, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: stored.Revision,
		Index: 3, SnapshotID: "", UseIndex: true,
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	if _, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: moved.Revision,
		Document: testDocument(t, "v10", bigDocumentPadding), Summary: "v10",
	}); err != nil {
		t.Fatalf("AppendSnapshot after truncating the redo branch returned error: %v", err)
	}
}

func TestCreateDraftRejectsMetadataOverLimit(t *testing.T) {
	h := newHarness(t)

	meta := &DraftMetadata{
		ID: "draft", Owner: testOwner, Title: "", SourceToken: "",
		Created: fakeTime(1), Updated: fakeTime(1), LastModifiedBy: testActor,
		History: make([]SnapshotManifest, 0, MaxSnapshots), Cursor: 0, Publication: nil, Revision: 0,
	}

	digest := digestOf([]byte("chunk"))

	for i := range MaxSnapshots {
		digests := make([]string, 0, 128)
		for range 128 {
			digests = append(digests, digest)
		}

		meta.History = append(meta.History, SnapshotManifest{
			ID: "snap-" + strconv.Itoa(i), Digest: digest, Size: 1, CompressedSize: 1,
			ChunkDigests: digests, ChunkSize: h.service.chunkSize,
			CreatedAt: fakeTime(1), CreatedBy: testActor, Summary: "",
		})
	}

	if _, err := encodeDraft(meta); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("encodeDraft error = %s, want ErrTooLarge for metadata past %d bytes", fmtErr(err), MaxMetadataBytes)
	}
}
