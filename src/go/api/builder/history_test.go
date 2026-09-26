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

func TestCreateDraftRejectsMetadataOverLimit(t *testing.T) {
	h := newHarness(t)

	meta := &DraftMetadata{
		ID: "draft", Owner: testOwner, Title: "", SourceToken: "",
		Created: fakeTime(1), Updated: fakeTime(1), LastModifiedBy: testActor,
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
			CreatedAt: fakeTime(1), CreatedBy: testActor, Summary: "",
		})
	}

	if _, err := encodeDraft(meta); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("encodeDraft error = %s, want ErrTooLarge for metadata past %d bytes", fmtErr(err), MaxMetadataBytes)
	}
}
