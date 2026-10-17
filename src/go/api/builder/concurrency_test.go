package builder

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestConcurrentIdenticalAppendKeepsWinnerReadable lets a writer commit the same
// document first; the loser's cleanup must not remove the winner's chunks.
func TestConcurrentIdenticalAppendKeepsWinnerReadable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	document := testDocument(t, "topo-v2", 0)

	var (
		winner    *DraftMetadata
		winnerErr error
		raced     bool
	)

	h.store.BeforeUpdate = func(namespace, _ string) error {
		if namespace != NamespaceDrafts || raced {
			return nil
		}

		raced = true

		// The other writer commits the identical document first.
		winner, winnerErr = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
			DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
			Document: document, Summary: "winner",
		})

		return nil
	}

	loser, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: document, Summary: "loser",
	})

	if winnerErr != nil {
		t.Fatalf("winning AppendSnapshot returned error: %v", winnerErr)
	}

	if !raced {
		t.Fatal("the test did not interleave the two appends")
	}

	if !errors.Is(err, ErrConflict) {
		t.Errorf("losing AppendSnapshot error = %s, want ErrConflict", fmtErr(err))
	}

	if loser != nil {
		t.Errorf("losing AppendSnapshot = %+v, want no metadata", loser)
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(draft) of the winner returned error: %v", err)
	}

	if diff := contentDiff(t, document, current.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) content mismatch with the winner (-want +got):\n%s", diff)
	}

	if got := documentProvenance(t, current.Data).UpdatedBy; got != testPeer {
		t.Errorf("GetCurrentDocument(draft) UpdatedBy = %q, want the winner %q", got, testPeer)
	}

	if want := winner.History[winner.Cursor].ID; current.Manifest.ID != want {
		t.Errorf("GetCurrentDocument(draft) snapshot = %q, want the winner's snapshot %q", current.Manifest.ID, want)
	}

	// The winner's chunks survived; the loser's are gone.
	if keys := chunkKeysOf(h, snapshotScope(meta.ID, current.Manifest.ID)); len(keys) == 0 {
		t.Error("chunks of the winner's snapshot = none, want them kept by the loser's cleanup")
	}

	want := 0
	for i := range winner.History {
		want += len(winner.History[i].ChunkDigests)
	}

	if keys := chunkKeysOf(h, draftScope(meta.ID)); len(keys) != want {
		t.Errorf("draft holds %d chunks, want %d: the losing attempt leaked chunks", len(keys), want)
	}
}

func TestConcurrentAppendsElectExactlyOneWinner(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	document := testDocument(t, "topo-v2", 0)

	const writers = 4

	var (
		wg      sync.WaitGroup
		results = make([]*DraftMetadata, writers)
		errs    = make([]error, writers)
	)

	wg.Add(writers)

	for i := range writers {
		go func() {
			defer wg.Done()

			results[i], errs[i] = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
				DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
				Document: document, Summary: "concurrent",
			})
		}()
	}

	wg.Wait()

	var winners int

	for i := range writers {
		switch {
		case errs[i] == nil:
			winners++
		case errors.Is(errs[i], ErrConflict):
		default:
			t.Errorf("AppendSnapshot of writer %d error = %s, want nil or ErrConflict", i, fmtErr(errs[i]))
		}
	}

	if winners != 1 {
		t.Errorf("%d concurrent AppendSnapshot calls succeeded, want exactly 1", winners)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if len(stored.History) != 2 {
		t.Errorf("GetDraft(draft) history length = %d, want 2 with the single winning append", len(stored.History))
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(draft) of the winner returned error: %v", err)
	}

	if diff := contentDiff(t, document, current.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) content mismatch with the winner (-want +got):\n%s", diff)
	}

	want := 0
	for i := range stored.History {
		want += len(stored.History[i].ChunkDigests)
	}

	if keys := chunkKeysOf(h, draftScope(meta.ID)); len(keys) != want {
		t.Errorf("draft holds %d chunks, want %d: losing attempts leaked chunks", len(keys), want)
	}
}

// TestConcurrentPublishKeepsWinnerReadable lets a winner store the same document
// after the loser wrote its chunks; the loser's cleanup must not touch it.
func TestConcurrentPublishKeepsWinnerReadable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	req := PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: testRandomDocument(t, "topo", 3000), DraftID: "", SnapshotID: "",
	}

	var (
		winner    *PublishedDocument
		winnerErr error
		raced     bool
		loserKeys []string
	)

	h.store.BeforeCreate = func(namespace, _ string) error {
		if namespace != NamespacePublished || raced {
			return nil
		}

		raced = true

		// At this point the loser has already checked for absence and written
		// its chunks; record them so the cleanup can be checked below.
		loserKeys = chunkKeysOf(h, publishedScope(PublishedDocumentID(req.Target, digestOf(req.Document))))

		winner, winnerErr = h.service.PutPublishedDocument(ctx, req)

		return nil
	}

	doc, err := h.service.PutPublishedDocument(ctx, req)

	if winnerErr != nil {
		t.Fatalf("winning PutPublishedDocument returned error: %v", winnerErr)
	}

	if !raced {
		t.Fatal("the test did not interleave the two publications")
	}

	if err != nil {
		t.Fatalf("losing PutPublishedDocument returned error: %s", fmtErr(err))
	}

	if diff := cmp.Diff(winner, doc, cmpopts.IgnoreUnexported(PublishedDocument{})); diff != "" {
		t.Errorf("losing PutPublishedDocument mismatch with the winner (-want +got):\n%s", diff)
	}

	if len(loserKeys) != len(winner.ChunkDigests) {
		t.Errorf("losing PutPublishedDocument wrote %d chunks, want %d", len(loserKeys), len(winner.ChunkDigests))
	}

	// The loser's private scope is gone and the winner's is untouched.
	remaining := chunkKeysOf(h, publishedScope(winner.ID))
	if len(remaining) != len(winner.ChunkDigests) {
		t.Errorf("published document holds %d chunks, want the winner's %d", len(remaining), len(winner.ChunkDigests))
	}

	scope := publishedPayloadScope(winner.ID, winner.PayloadID)
	for _, key := range remaining {
		if !strings.HasPrefix(key, scope) {
			t.Errorf("published document chunk %q is outside the winner's payload scope %q", key, scope)
		}
	}

	_, data, err := h.service.GetPublishedDocumentData(ctx, winner.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData(winner) after the loser cleaned up returned error: %v", err)
	}

	if diff := cmp.Diff(string(req.Document), string(data)); diff != "" {
		t.Errorf("GetPublishedDocumentData(winner) mismatch (-want +got):\n%s", diff)
	}
}

func TestWriteChunksCleansUpPartialFailures(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")

	load, err := buildPayload([]byte(randomText(11, 4000)), h.service.chunkSize)
	if err != nil {
		t.Fatalf("buildPayload returned error: %v", err)
	}

	if len(load.chunks) < 3 {
		t.Fatalf("chunks = %d, want a payload of at least 3 chunks", len(load.chunks))
	}

	failing := chunkKey(scope, 2)

	h.store.BeforeCreate = func(namespace, key string) error {
		if namespace == NamespaceChunks && key == failing {
			return errors.New("chunk store is unavailable")
		}

		return nil
	}

	created, err := h.service.writeChunks(scope, load)

	if err == nil {
		t.Error("writeChunks(store rejects a chunk) error = nil, want an error")
	}

	if created != nil {
		t.Errorf("writeChunks(store rejects a chunk) = %v, want no keys after cleaning up", created)
	}

	if keys := chunkKeysOf(h, scope); len(keys) != 0 {
		t.Errorf("chunks after a partial writeChunks failure = %v, want none", keys)
	}

	// A cleanup failure on top of the write failure is reported, not swallowed.
	h.store.FailDelete = func(namespace, _ string) error {
		if namespace != NamespaceChunks {
			return nil
		}

		return errors.New("chunk store is unavailable for deletes too")
	}

	_, err = h.service.writeChunks(scope, load)
	if !errors.Is(err, ErrCleanup) {
		t.Errorf("writeChunks(store rejects a chunk and its cleanup) error = %s, want ErrCleanup", fmtErr(err))
	}
}
